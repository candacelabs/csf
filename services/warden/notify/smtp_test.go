package notify

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"net/mail"
	"strconv"
	"strings"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
)

// --- message building -------------------------------------------------------

func parseMessage(raw []byte) *mail.Message {
	GinkgoHelper()
	m, err := mail.ReadMessage(strings.NewReader(string(raw)))
	Expect(err).NotTo(HaveOccurred(), "message is not well-formed:\n%s", raw)
	return m
}

func readBody(m *mail.Message) string {
	GinkgoHelper()
	data, err := io.ReadAll(m.Body)
	Expect(err).NotTo(HaveOccurred(), "reading body")
	return string(data)
}

var _ = Describe("buildMessage", func() {
	// TestBuildMessageDeadHeadersAndBody
	It("builds correct headers and body for a dead incident", func() {
		inc := deadIncident("node-a")
		raw, err := buildMessage("warden@example.invalid", []string{"ops@example.invalid"}, inc, testTime)
		Expect(err).NotTo(HaveOccurred())
		m := parseMessage(raw)

		Expect(m.Header.Get("From")).To(Equal("warden@example.invalid"))
		Expect(m.Header.Get("To")).To(Equal("ops@example.invalid"))
		Expect(m.Header.Get("Subject")).To(Equal("[warden] peer node-a DEAD (term 7)"))
		Expect(m.Header.Get("MIME-Version")).To(Equal("1.0"))
		ct := m.Header.Get("Content-Type")
		Expect(ct).To(ContainSubstring("text/plain"))
		Expect(ct).To(ContainSubstring("utf-8"))
		_, err = mail.ParseDate(m.Header.Get("Date"))
		Expect(err).NotTo(HaveOccurred(), "Date %q not RFC1123-parseable", m.Header.Get("Date"))
		id := m.Header.Get("Message-ID")
		Expect(id).To(HavePrefix("<"))
		Expect(id).To(HaveSuffix(">"))
		Expect(id).To(ContainSubstring("@example.invalid"))

		body := readBody(m)
		for _, want := range []string{
			string(inc.Type), inc.ID, "node-a", "203.0.113.11:7717",
			"node-c", "7", inc.Message,
		} {
			Expect(body).To(ContainSubstring(want), "body missing %q", want)
		}
	})

	// TestBuildMessageRecoverySubject
	It("uses a recovery subject for a recovery incident", func() {
		raw, err := buildMessage("warden@example.invalid", []string{"ops@example.invalid"}, recoveryIncident("node-a"), testTime)
		Expect(err).NotTo(HaveOccurred())
		m := parseMessage(raw)
		Expect(m.Header.Get("Subject")).To(Equal("[warden] peer node-a recovered"))
	})

	// TestBuildMessageMultipleRecipients
	It("joins multiple recipients into a parseable To header", func() {
		to := []string{"a@example.invalid", "b@example.invalid", "c@example.invalid"}
		raw, err := buildMessage("warden@example.invalid", to, deadIncident("node-a"), testTime)
		Expect(err).NotTo(HaveOccurred())
		m := parseMessage(raw)
		Expect(m.Header.Get("To")).To(Equal(strings.Join(to, ", ")))
		addrs, err := m.Header.AddressList("To")
		Expect(err).NotTo(HaveOccurred())
		Expect(addrs).To(HaveLen(len(to)))
	})

	// TestBuildMessageHeaderInjectionStrippedInSubject
	It("flattens CRLF-injected subject text and never emits an injected header line", func() {
		inc := deadIncident("node-a\r\nBcc: attacker@evil.example")
		raw, err := buildMessage("warden@example.invalid", []string{"ops@example.invalid"}, inc, testTime)
		Expect(err).NotTo(HaveOccurred())
		m := parseMessage(raw)
		Expect(m.Header.Get("Bcc")).To(BeEmpty(), "header injection succeeded")
		// The injected text is flattened into the single Subject line.
		Expect(m.Header.Get("Subject")).To(ContainSubstring("Bcc: attacker@evil.example"))
		// No line in the header block (everything before the blank line separating
		// headers from body) may be an injected header. Body content is not a
		// header-injection vector; the human-readable Message is legitimately
		// multi-line.
		headerBlock := string(raw)
		if i := strings.Index(headerBlock, "\r\n\r\n"); i >= 0 {
			headerBlock = headerBlock[:i]
		}
		for _, line := range strings.Split(headerBlock, "\r\n") {
			Expect(strings.HasPrefix(line, "Bcc:")).To(BeFalse(), "header block contains an injected header line: %q", line)
		}
	})

	// TestBuildMessageRejectsCRLFInAddresses
	It("rejects CRLF in From/To addresses with ErrHeaderInjection", func() {
		inc := deadIncident("node-a")
		_, err := buildMessage("warden@example.invalid\r\nBcc: x@evil", []string{"ops@example.invalid"}, inc, testTime)
		Expect(errors.Is(err, ErrHeaderInjection)).To(BeTrue(), "From injection: got %v", err)
		_, err = buildMessage("warden@example.invalid", []string{"ops@example.invalid\r\nBcc: x@evil"}, inc, testTime)
		Expect(errors.Is(err, ErrHeaderInjection)).To(BeTrue(), "To injection: got %v", err)
	})

	// TestBuildMessageValidatesAddresses
	It("rejects empty sender/recipients with sentinel errors", func() {
		inc := deadIncident("node-a")
		_, err := buildMessage("", []string{"ops@example.invalid"}, inc, testTime)
		Expect(errors.Is(err, ErrNoSender)).To(BeTrue(), "empty From: got %v", err)
		_, err = buildMessage("warden@example.invalid", nil, inc, testTime)
		Expect(errors.Is(err, ErrNoRecipients)).To(BeTrue(), "nil To: got %v", err)
		_, err = buildMessage("warden@example.invalid", []string{"  "}, inc, testTime)
		Expect(errors.Is(err, ErrNoRecipients)).To(BeTrue(), "blank To: got %v", err)
	})

	// TestBuildMessageMissingLastSeen
	It("renders a zero LastSeen as 'never'", func() {
		inc := deadIncident("node-a")
		inc.LastSeen = time.Time{}
		raw, err := buildMessage("warden@example.invalid", []string{"ops@example.invalid"}, inc, testTime)
		Expect(err).NotTo(HaveOccurred())
		Expect(readBody(parseMessage(raw))).To(ContainSubstring("Last seen:    never"))
	})

	// TestBuildMessageDeterministic
	It("is deterministic for identical inputs", func() {
		inc := deadIncident("node-a")
		a, err := buildMessage("warden@example.invalid", []string{"ops@example.invalid"}, inc, testTime)
		Expect(err).NotTo(HaveOccurred())
		b, err := buildMessage("warden@example.invalid", []string{"ops@example.invalid"}, inc, testTime)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(a)).To(Equal(string(b)))
	})
})

// --- STARTTLS fail-closed against a local fake SMTP server ------------------

type recordedLines struct {
	mu sync.Mutex
	l  []string
}

func (r *recordedLines) add(s string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.l = append(r.l, s)
}

func (r *recordedLines) lines() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.l))
	copy(out, r.l)
	return out
}

// startFakeSMTP serves a minimal SMTP dialogue on 127.0.0.1. When
// advertiseSTARTTLS is false it never offers STARTTLS, so a correct client must
// refuse to proceed. Every command line the client sends is recorded. It is a
// behavioral simulator of an SMTP server, not a mock.
func startFakeSMTP(advertiseSTARTTLS bool) (addr string, recv *recordedLines) {
	GinkgoHelper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	Expect(err).NotTo(HaveOccurred(), "listen")
	DeferCleanup(func() { _ = ln.Close() })
	recv = &recordedLines{}

	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		br := bufio.NewReader(conn)
		bw := bufio.NewWriter(conn)
		put := func(s string) {
			_, _ = bw.WriteString(s + "\r\n")
			_ = bw.Flush()
		}
		put("220 fake ESMTP ready")
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			recv.add(line)
			switch up := strings.ToUpper(line); {
			case strings.HasPrefix(up, "EHLO"), strings.HasPrefix(up, "HELO"):
				_, _ = bw.WriteString("250-fake greets you\r\n")
				if advertiseSTARTTLS {
					_, _ = bw.WriteString("250-STARTTLS\r\n")
				}
				_, _ = bw.WriteString("250 AUTH PLAIN LOGIN\r\n")
				_ = bw.Flush()
			case strings.HasPrefix(up, "QUIT"):
				put("221 bye")
				return
			default:
				put("250 ok")
			}
		}
	}()
	return ln.Addr().String(), recv
}

var _ = Describe("SMTPNotifier fail-closed", func() {
	// TestSMTPNotifierRefusesWithoutSTARTTLS
	It("refuses without STARTTLS and never transmits credentials or mail", func() {
		addr, recv := startFakeSMTP(false)
		host, portStr, err := net.SplitHostPort(addr)
		Expect(err).NotTo(HaveOccurred(), "split host port")
		port, _ := strconv.Atoi(portStr)

		const password = "sup3r-s3cr3t"
		n := NewSMTPNotifier(SMTPConfig{
			Host:     host,
			Port:     port,
			Username: "warden",
			Password: password,
			From:     "warden@example.invalid",
			To:       []string{"ops@example.invalid"},
		})

		err = n.Notify(context.Background(), deadIncident("node-a"))
		Expect(err).To(HaveOccurred(), "Notify must fail when the server does not advertise STARTTLS")
		Expect(errors.Is(err, ErrSTARTTLSRequired)).To(BeTrue(), "error = %v", err)

		// Credentials and mail must never have been transmitted.
		for _, line := range recv.lines() {
			up := strings.ToUpper(line)
			Expect(up).NotTo(ContainSubstring("AUTH"), "client sent AUTH before TLS: %q", line)
			Expect(up).NotTo(ContainSubstring("MAIL FROM"), "client sent MAIL before TLS: %q", line)
			Expect(line).NotTo(ContainSubstring(password), "password leaked in cleartext: %q", line)
		}
	})

	// TestSMTPNotifierRejectsInjectionBeforeDial: the SMTP notifier must reject a
	// header-injecting incident before any network contact (message is built first).
	It("rejects a header-injecting config before dialing", func() {
		n := NewSMTPNotifier(SMTPConfig{
			Host: "203.0.113.1", // unroutable; must never be dialed
			Port: 2525,
			From: "warden@example.invalid\r\nBcc: attacker@evil.example",
			To:   []string{"ops@example.invalid"},
		})
		err := n.Notify(context.Background(), deadIncident("node-a"))
		Expect(errors.Is(err, ErrHeaderInjection)).To(BeTrue(), "error = %v (no dial)", err)
	})
})

// Ensure the SMTPNotifier satisfies the Notifier contract at compile time via a
// value we can pass around.
var _ warden.INotifier = NewSMTPNotifier(SMTPConfig{})
