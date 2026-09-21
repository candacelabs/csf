package notify_test

// Contract tests for the warden.INotifier implementations, asserted through the
// exported surface only. The security-critical property is SMTP fail-closed:
// when the server does not advertise STARTTLS, the notifier must abort BEFORE
// transmitting any credentials or mail — so a passive eavesdropper on a
// cleartext link never sees the SMTP password.
//
// (The exact Subject/Message-ID/body bytes are golden-tested against the
// unexported buildMessage in the internal smtp_test.go; they are unreachable
// from an external package without completing a trusted STARTTLS handshake, so
// this suite freezes the wire-observable security behaviour instead.)

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/notify"
)

func TestNotifyContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "notify contract suite")
}

func sampleIncident(peer string) warden.Incident {
	at := time.Date(2026, 7, 21, 15, 4, 5, 0, time.UTC)
	return warden.Incident{
		ID:         warden.NewIncidentID(warden.IncidentPeerDead, warden.NodeID(peer), at),
		Type:       warden.IncidentPeerDead,
		Peer:       warden.Node{ID: warden.NodeID(peer), Addr: "10.0.0.9:7717"},
		Term:       7,
		ReportedBy: "node-d",
		DetectedAt: at,
		LastSeen:   at,
		Message:    "peer " + peer + " declared dead",
	}
}

// fakeSMTP is a minimal SMTP server that greets, answers one EHLO WITHOUT
// advertising STARTTLS, and records every byte the client sends. It exists to
// prove the notifier fails closed without ever leaking credentials.
type fakeSMTP struct {
	ln       net.Listener
	mu       sync.Mutex
	received []byte
	done     chan struct{}
}

func startFakeSMTP() *fakeSMTP {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred())
	s := &fakeSMTP{ln: ln, done: make(chan struct{})}
	go s.serve()
	return s
}

func (s *fakeSMTP) addr() (host, port string) {
	h, p, err := net.SplitHostPort(s.ln.Addr().String())
	Expect(err).NotTo(HaveOccurred())
	return h, p
}

func (s *fakeSMTP) serve() {
	defer close(s.done)
	conn, err := s.ln.Accept()
	if err != nil {
		return
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	w := bufio.NewWriter(conn)
	r := bufio.NewReader(conn)
	writeLine := func(l string) { _, _ = w.WriteString(l + "\r\n"); _ = w.Flush() }

	writeLine("220 fake ESMTP ready")
	for {
		line, err := r.ReadString('\n')
		if line != "" {
			s.mu.Lock()
			s.received = append(s.received, []byte(line)...)
			s.mu.Unlock()
		}
		if err != nil {
			return
		}
		cmd := strings.ToUpper(strings.TrimSpace(line))
		switch {
		case strings.HasPrefix(cmd, "EHLO"), strings.HasPrefix(cmd, "HELO"):
			// Deliberately advertise NO STARTTLS extension.
			writeLine("250-fake.local")
			writeLine("250 HELP")
		case strings.HasPrefix(cmd, "QUIT"):
			writeLine("221 bye")
			return
		default:
			writeLine("250 ok")
		}
	}
}

func (s *fakeSMTP) wire() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return string(s.received)
}

func (s *fakeSMTP) close() { _ = s.ln.Close() }

var _ = Describe("SMTPNotifier fail-closed", func() {
	var server *fakeSMTP

	BeforeEach(func() { server = startFakeSMTP() })
	AfterEach(func() { server.close() })

	It("refuses to send when the server does not advertise STARTTLS", func() {
		host, port := server.addr()
		n := notify.NewSMTPNotifier(notify.SMTPConfig{
			Host: host, Port: atoiPort(port),
			Username: "warden@example.invalid", Password: "hunter2-secret-app-pw",
			From: "warden@example.invalid", To: []string{"ops@example.invalid"},
		})
		err := n.Notify(context.Background(), sampleIncident("node-a"))
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, notify.ErrSTARTTLSRequired)).To(BeTrue(),
			"expected ErrSTARTTLSRequired, got %v", err)
	})

	It("transmits ZERO credential bytes and no AUTH/MAIL/DATA over the cleartext link", func() {
		host, port := server.addr()
		const password = "hunter2-secret-app-pw"
		n := notify.NewSMTPNotifier(notify.SMTPConfig{
			Host: host, Port: atoiPort(port),
			Username: "warden@example.invalid", Password: password,
			From: "warden@example.invalid", To: []string{"ops@example.invalid"},
		})
		_ = n.Notify(context.Background(), sampleIncident("node-a"))

		// Give the server goroutine a moment to record whatever the client sent.
		Eventually(func() bool {
			select {
			case <-server.done:
				return true
			default:
				return false
			}
		}, "2s").Should(BeTrue())

		wire := server.wire()
		Expect(wire).NotTo(ContainSubstring(password), "the SMTP password leaked in cleartext")
		Expect(strings.ToUpper(wire)).NotTo(ContainSubstring("AUTH"))
		Expect(strings.ToUpper(wire)).NotTo(ContainSubstring("MAIL FROM"))
		Expect(strings.ToUpper(wire)).NotTo(ContainSubstring("DATA"))
		// The only command that should have reached the server is the EHLO.
		Expect(strings.ToUpper(wire)).To(ContainSubstring("EHLO"))
	})
})

var _ = Describe("SMTPNotifier pre-network validation", func() {
	// These failures happen inside buildMessage, BEFORE any dial. Pointing Host
	// at a refused port proves the error is not a connection error.
	unreachable := func(cfg *notify.SMTPConfig) { cfg.Host = "127.0.0.1"; cfg.Port = 1 }

	It("rejects a From address containing CRLF before dialing (header injection)", func() {
		cfg := notify.SMTPConfig{From: "warden@example.invalid\r\nBcc: attacker@evil", To: []string{"ops@example.invalid"}}
		unreachable(&cfg)
		err := notify.NewSMTPNotifier(cfg).Notify(context.Background(), sampleIncident("node-a"))
		Expect(errors.Is(err, notify.ErrHeaderInjection)).To(BeTrue(), "got %v", err)
	})

	It("rejects a To address containing CRLF before dialing", func() {
		cfg := notify.SMTPConfig{From: "warden@example.invalid", To: []string{"ops@example.invalid\r\nBcc: x@evil"}}
		unreachable(&cfg)
		err := notify.NewSMTPNotifier(cfg).Notify(context.Background(), sampleIncident("node-a"))
		Expect(errors.Is(err, notify.ErrHeaderInjection)).To(BeTrue(), "got %v", err)
	})

	It("rejects an empty From with ErrNoSender before dialing", func() {
		cfg := notify.SMTPConfig{From: "", To: []string{"ops@example.invalid"}}
		unreachable(&cfg)
		err := notify.NewSMTPNotifier(cfg).Notify(context.Background(), sampleIncident("node-a"))
		Expect(errors.Is(err, notify.ErrNoSender)).To(BeTrue(), "got %v", err)
	})

	It("rejects empty recipients with ErrNoRecipients before dialing", func() {
		cfg := notify.SMTPConfig{From: "warden@example.invalid", To: nil}
		unreachable(&cfg)
		err := notify.NewSMTPNotifier(cfg).Notify(context.Background(), sampleIncident("node-a"))
		Expect(errors.Is(err, notify.ErrNoRecipients)).To(BeTrue(), "got %v", err)
	})

	It("exposes distinct, non-nil sentinel errors", func() {
		errs := []error{
			notify.ErrSTARTTLSRequired, notify.ErrAuthUnsupported, notify.ErrNoRecipients,
			notify.ErrNoSender, notify.ErrHeaderInjection,
		}
		for i := range errs {
			Expect(errs[i]).NotTo(BeNil())
			for j := range errs {
				if i != j {
					Expect(errors.Is(errs[i], errs[j])).To(BeFalse())
				}
			}
		}
	})
})

var _ = Describe("LogNotifier", func() {
	It("implements warden.INotifier and always returns nil", func() {
		var n warden.INotifier = notify.NewLogNotifier()
		Expect(n.Notify(context.Background(), sampleIncident("node-a"))).To(Succeed())
	})

	It("ignores context cancellation (returns nil even when ctx is done)", func() {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		Expect(notify.NewLogNotifier().Notify(ctx, sampleIncident("node-a"))).To(Succeed())
	})
})

var _ = Describe("FileNotifier", func() {
	var path string

	BeforeEach(func() {
		path = filepath.Join(GinkgoT().TempDir(), "incidents.jsonl")
	})

	It("appends one JSON-encoded incident per line, byte-equal to json.Marshal", func() {
		n := notify.NewFileNotifier(path)
		inc := sampleIncident("node-a")
		Expect(n.Notify(context.Background(), inc)).To(Succeed())

		data, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(data[len(data)-1]).To(Equal(byte('\n')))
		want, _ := json.Marshal(inc)
		Expect(strings.TrimRight(string(data), "\n")).To(Equal(string(want)))
	})

	It("appends across calls, preserving order and decodability", func() {
		n := notify.NewFileNotifier(path)
		for _, p := range []string{"a", "b", "c"} {
			Expect(n.Notify(context.Background(), sampleIncident(p))).To(Succeed())
		}
		lines := nonEmptyLines(path)
		Expect(lines).To(HaveLen(3))
		var got []string
		for _, l := range lines {
			var inc warden.Incident
			Expect(json.Unmarshal([]byte(l), &inc)).To(Succeed())
			got = append(got, string(inc.Peer.ID))
		}
		Expect(got).To(Equal([]string{"a", "b", "c"}))
	})

	It("does not interleave or lose lines under concurrent Notify calls", func() {
		n := notify.NewFileNotifier(path)
		const workers = 20
		var wg sync.WaitGroup
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				defer GinkgoRecover()
				Expect(n.Notify(context.Background(), sampleIncident("p"))).To(Succeed())
			}(i)
		}
		wg.Wait()
		lines := nonEmptyLines(path)
		Expect(lines).To(HaveLen(workers))
		for _, l := range lines {
			var inc warden.Incident
			Expect(json.Unmarshal([]byte(l), &inc)).To(Succeed(),
				"a concurrently-written line was corrupt/interleaved: %q", l)
		}
	})
})

// atoiPort converts a numeric port string to int for SMTPConfig.
func atoiPort(p string) int {
	n := 0
	for _, r := range p {
		n = n*10 + int(r-'0')
	}
	return n
}

func nonEmptyLines(path string) []string {
	data, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	var out []string
	for _, l := range strings.Split(string(data), "\n") {
		if strings.TrimSpace(l) != "" {
			out = append(out, l)
		}
	}
	return out
}
