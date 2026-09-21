package discovery

import (
	"context"
	"io"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
)

// tsPoll is a short poll interval keeping tailscale tests fast but not flaky.
const tsPoll = 10 * time.Millisecond

// fakeTS is a canned tailscaled LocalAPI: it serves whatever status body/code
// the test currently has set at /localapi/v0/status. The body can be rotated
// between polls to exercise change detection, errors, and recovery. It is a
// behavioral simulator of tailscaled, not a mock.
type fakeTS struct {
	mu    sync.Mutex
	code  int
	body  string
	calls int
}

func (f *fakeTS) set(code int, body string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.code, f.body = code, body
}

func (f *fakeTS) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

func (f *fakeTS) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/localapi/v0/status" {
		http.NotFound(w, r)
		return
	}
	f.mu.Lock()
	code, body := f.code, f.body
	f.calls++
	f.mu.Unlock()
	if code == 0 {
		code = http.StatusOK
	}
	w.WriteHeader(code)
	_, _ = io.WriteString(w, body)
}

// startFakeTS listens on a short-path unix socket in GinkgoT().TempDir and
// serves f.
func startFakeTS(f *fakeTS) string {
	GinkgoHelper()
	sock := filepath.Join(GinkgoT().TempDir(), "s") // short name: unix path length limit
	ln, err := net.Listen("unix", sock)
	Expect(err).NotTo(HaveOccurred(), "listen unix %q", sock)
	srv := &http.Server{Handler: f}
	go func() { _ = srv.Serve(ln) }()
	DeferCleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	})
	return sock
}

// statusTwoTagged is a small LocalAPI status body with two tagged peers.
const statusTwoTagged = `{
  "Self": {"HostName":"self-node","TailscaleIPs":["100.64.0.1"],"Tags":["tag:candacenet"],"Online":true},
  "Peer": {
    "k1": {"HostName":"node-c","TailscaleIPs":["203.0.113.13"],"Tags":["tag:candacenet"],"Online":true},
    "k2": {"HostName":"stranger","TailscaleIPs":["100.64.0.29"],"Tags":["tag:other"],"Online":true}
  }
}`

var _ = Describe("Tailscale discoverer", func() {
	// TestTailscaleTagFilter
	It("selects only tag-matching peers and keeps self when included", func() {
		f := &fakeTS{}
		f.set(http.StatusOK, statusTwoTagged)
		sock := startFakeTS(f)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, err := NewTailscale(TailscaleConfig{
			Socket:       sock,
			Tag:          "tag:candacenet",
			Port:         7717,
			PollInterval: tsPoll,
			IncludeSelf:  true,
		}).Discover(ctx)
		Expect(err).NotTo(HaveOccurred())

		got := recvRoster(ch)
		// self-node + node-c are tagged; stranger (tag:other) is excluded.
		Expect(ids(got)).To(Equal([]warden.NodeID{"node-c", "self-node"}))
		Expect(addrOf(got, "node-c")).To(Equal("203.0.113.13:7717"))
	})

	// TestTailscaleExcludeSelf
	It("excludes self when IncludeSelf is false", func() {
		f := &fakeTS{}
		f.set(http.StatusOK, statusTwoTagged)
		sock := startFakeTS(f)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, _ := NewTailscale(TailscaleConfig{
			Socket:       sock,
			Tag:          "tag:candacenet",
			PollInterval: tsPoll,
			IncludeSelf:  false,
		}).Discover(ctx)

		got := recvRoster(ch)
		Expect(ids(got)).To(Equal([]warden.NodeID{"node-c"}))
	})

	// TestTailscaleHostPatternFilter
	It("selects peers by anchored host pattern", func() {
		f := &fakeTS{}
		f.set(http.StatusOK, `{
      "Peer": {
        "k1": {"HostName":"node-c","TailscaleIPs":["203.0.113.13"],"Online":true},
        "k2": {"HostName":"node-b","TailscaleIPs":["203.0.113.12"],"Online":true},
        "k3": {"HostName":"laptop","TailscaleIPs":["100.64.0.24"],"Online":true}
      }
    }`)
		sock := startFakeTS(f)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, _ := NewTailscale(TailscaleConfig{
			Socket:       sock,
			HostPattern:  `node-.*`,
			PollInterval: tsPoll,
			IncludeSelf:  false,
		}).Discover(ctx)

		got := recvRoster(ch)
		Expect(ids(got)).To(Equal([]warden.NodeID{"node-b", "node-c"}))
	})

	// TestTailscaleHostPatternAnchored
	It("anchors the host pattern so a prefix does not partial-match", func() {
		// Anchored: "node-a" must NOT match "node-c" (no partial matches).
		f := &fakeTS{}
		f.set(http.StatusOK, `{
      "Peer": {
        "k1": {"HostName":"node-c","TailscaleIPs":["203.0.113.13"],"Online":true},
        "k2": {"HostName":"node-a","TailscaleIPs":["203.0.113.11"],"Online":true}
      }
    }`)
		sock := startFakeTS(f)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, _ := NewTailscale(TailscaleConfig{
			Socket:       sock,
			HostPattern:  `node-a`,
			PollInterval: tsPoll,
			IncludeSelf:  false,
		}).Discover(ctx)

		got := recvRoster(ch)
		Expect(ids(got)).To(Equal([]warden.NodeID{"node-a"}))
	})

	// TestTailscaleTagOrPattern
	It("selects a peer matching either the tag or the host pattern", func() {
		// Both filters set: a peer matching EITHER is selected.
		f := &fakeTS{}
		f.set(http.StatusOK, `{
      "Peer": {
        "k1": {"HostName":"tagged","TailscaleIPs":["100.64.0.21"],"Tags":["tag:candacenet"],"Online":true},
        "k2": {"HostName":"warden-2","TailscaleIPs":["100.64.0.22"],"Online":true},
        "k3": {"HostName":"nope","TailscaleIPs":["100.64.0.23"],"Tags":["tag:other"],"Online":true}
      }
    }`)
		sock := startFakeTS(f)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, _ := NewTailscale(TailscaleConfig{
			Socket:       sock,
			Tag:          "tag:candacenet",
			HostPattern:  `warden-\d+`,
			PollInterval: tsPoll,
			IncludeSelf:  false,
		}).Discover(ctx)

		got := recvRoster(ch)
		Expect(ids(got)).To(Equal([]warden.NodeID{"tagged", "warden-2"}))
	})

	// TestTailscaleFirstIPv4Selected
	It("chooses the first IPv4 for the peer address", func() {
		// IPv6 first, then IPv4: the IPv4 must be chosen for the Addr.
		f := &fakeTS{}
		f.set(http.StatusOK, `{
      "Peer": {
        "k1": {"HostName":"n","TailscaleIPs":["fd7a:115c:a1e0::1","100.64.0.26"],"Tags":["tag:candacenet"],"Online":true}
      }
    }`)
		sock := startFakeTS(f)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, _ := NewTailscale(TailscaleConfig{
			Socket: sock, Tag: "tag:candacenet", Port: 7717, PollInterval: tsPoll,
		}).Discover(ctx)

		got := recvRoster(ch)
		Expect(addrOf(got, "n")).To(Equal("100.64.0.26:7717"))
	})

	// TestTailscaleOfflinePeerRetained
	It("retains offline peers in the roster", func() {
		f := &fakeTS{}
		f.set(http.StatusOK, `{
      "Peer": {
        "k1": {"HostName":"rebooting","TailscaleIPs":["100.64.0.27"],"Tags":["tag:candacenet"],"Online":false}
      }
    }`)
		sock := startFakeTS(f)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, _ := NewTailscale(TailscaleConfig{
			Socket: sock, Tag: "tag:candacenet", PollInterval: tsPoll,
		}).Discover(ctx)

		got := recvRoster(ch)
		Expect(ids(got)).To(Equal([]warden.NodeID{"rebooting"}))
	})

	// TestTailscaleNoMatchExcluded
	It("emits an empty roster when no peer matches", func() {
		f := &fakeTS{}
		f.set(http.StatusOK, `{
      "Peer": {
        "k1": {"HostName":"stranger","TailscaleIPs":["100.64.0.29"],"Tags":["tag:other"],"Online":true}
      }
    }`)
		sock := startFakeTS(f)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, _ := NewTailscale(TailscaleConfig{
			Socket: sock, Tag: "tag:candacenet", PollInterval: tsPoll, IncludeSelf: false,
		}).Discover(ctx)

		got := recvRoster(ch)
		Expect(got.Nodes).To(BeEmpty())
	})

	// TestTailscaleChangeOnlySends
	It("keeps polling but only sends on a real change", func() {
		f := &fakeTS{}
		f.set(http.StatusOK, `{"Peer":{"k1":{"HostName":"a","TailscaleIPs":["100.64.0.21"],"Tags":["tag:candacenet"]}}}`)
		sock := startFakeTS(f)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, _ := NewTailscale(TailscaleConfig{
			Socket: sock, Tag: "tag:candacenet", PollInterval: tsPoll, IncludeSelf: false,
		}).Discover(ctx)

		first := recvRoster(ch)
		Expect(ids(first)).To(Equal([]warden.NodeID{"a"}))

		// Identical status across several polls => no duplicate snapshot.
		before := f.callCount()
		expectNoRoster(ch, 80*time.Millisecond)
		Expect(f.callCount()).To(BeNumerically(">", before),
			"expected the poller to keep polling; calls did not advance")

		// A real change (new peer) => exactly one new snapshot.
		f.set(http.StatusOK, `{"Peer":{
      "k1":{"HostName":"a","TailscaleIPs":["100.64.0.21"],"Tags":["tag:candacenet"]},
      "k2":{"HostName":"b","TailscaleIPs":["100.64.0.22"],"Tags":["tag:candacenet"]}}}`)
		second := recvRoster(ch)
		Expect(ids(second)).To(Equal([]warden.NodeID{"a", "b"}))
	})

	// TestTailscaleAPIErrorNoSendThenRecovery
	It("sends nothing on an API error, then recovers", func() {
		f := &fakeTS{}
		f.set(http.StatusInternalServerError, "boom")
		sock := startFakeTS(f)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, _ := NewTailscale(TailscaleConfig{
			Socket: sock, Tag: "tag:candacenet", PollInterval: tsPoll,
		}).Discover(ctx)

		// HTTP 500: nothing sent.
		expectNoRoster(ch, 80*time.Millisecond)

		// Recovery: valid status now yields a snapshot.
		f.set(http.StatusOK, `{"Peer":{"k1":{"HostName":"a","TailscaleIPs":["100.64.0.21"],"Tags":["tag:candacenet"]}}}`)
		got := recvRoster(ch)
		Expect(ids(got)).To(Equal([]warden.NodeID{"a"}))
	})

	// TestTailscaleMalformedJSONNoSend
	It("sends nothing on malformed JSON, then recovers", func() {
		f := &fakeTS{}
		f.set(http.StatusOK, `{"Peer": { this is not valid json`)
		sock := startFakeTS(f)

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		ch, _ := NewTailscale(TailscaleConfig{
			Socket: sock, Tag: "tag:candacenet", PollInterval: tsPoll,
		}).Discover(ctx)

		expectNoRoster(ch, 80*time.Millisecond)

		f.set(http.StatusOK, `{"Peer":{"k1":{"HostName":"a","TailscaleIPs":["100.64.0.21"],"Tags":["tag:candacenet"]}}}`)
		got := recvRoster(ch)
		Expect(ids(got)).To(Equal([]warden.NodeID{"a"}))
	})

	// TestTailscaleClosesOnCtxEnd
	It("closes the channel when the context ends", func() {
		f := &fakeTS{}
		f.set(http.StatusOK, `{"Peer":{"k1":{"HostName":"a","TailscaleIPs":["100.64.0.21"],"Tags":["tag:candacenet"]}}}`)
		sock := startFakeTS(f)

		ctx, cancel := context.WithCancel(context.Background())
		ch, _ := NewTailscale(TailscaleConfig{
			Socket: sock, Tag: "tag:candacenet", PollInterval: tsPoll,
		}).Discover(ctx)
		_ = recvRoster(ch)
		cancel()
		expectClosed(ch)
	})

	// TestTailscaleDefaults
	It("defaults socket, port, and poll interval", func() {
		ts := NewTailscale(TailscaleConfig{Tag: "tag:candacenet"})
		Expect(ts.socket).To(Equal(defaultTSSocket))
		Expect(ts.port).To(Equal(defaultWardenPort))
		Expect(ts.poll).To(Equal(defaultTSPollInterval))
	})

	// TestCompileHostPatternRejectsGarbage
	It("compiles anchored host patterns and rejects garbage", func() {
		_, err := CompileHostPattern(`(`)
		Expect(err).To(HaveOccurred(), "CompileHostPattern should reject an unbalanced pattern")

		re, err := CompileHostPattern(`node-.*`)
		Expect(err).NotTo(HaveOccurred())
		Expect(re.MatchString("xnode-c")).To(BeFalse(), "compiled pattern should be anchored")
		Expect(re.MatchString("node-c")).To(BeTrue(), "compiled pattern should match node-c")
	})
})
