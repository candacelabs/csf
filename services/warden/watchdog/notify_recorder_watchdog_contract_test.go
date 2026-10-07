package watchdog_test

// Contract tests for the leader-only watchdog, exercised through its exported
// surface (New, Run, Incidents) with a fake ViewSource, a recording
// mocks.MockINotifier, and the deterministic testclock. These freeze the
// operator-visible alerting guarantees documented in the README:
//   - exactly one death notification per continuous outage (per episode);
//   - a repeat death within the cooldown window is recorded but NOT re-notified;
//   - a leader that cannot see a live majority stays silent (isolation guard);
//   - recovery is notified when enabled;
//   - only the acting leader notifies.

import (
	"context"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/internal/mocks"
	"github.com/candacelabs/csf/services/warden/testclock"
	"github.com/candacelabs/csf/services/warden/watchdog"
)

func TestWatchdogContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "watchdog contract suite")
}

const selfID = warden.NodeID("self")

var wdStart = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

// --- notifier recorder (replaces the retired notify.Mock) -------------------

// notifyRecorder captures incidents delivered to a mocks.MockINotifier via a
// DoAndReturn hook — the replacement for the retired notify.Mock's .Sent()
// recording. Safe for concurrent delivery goroutines while the spec reads Sent.
type notifyRecorder struct {
	mu   sync.Mutex
	sent []warden.Incident
}

func (r *notifyRecorder) record(_ context.Context, inc warden.Incident) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sent = append(r.sent, inc)
	return nil
}

func (r *notifyRecorder) Sent() []warden.Incident {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]warden.Incident, len(r.sent))
	copy(out, r.sent)
	return out
}

// recordingNotifier returns a MockINotifier whose Notify records every delivery
// into the returned recorder (DoAndReturn capture-to-slice).
func recordingNotifier() (*mocks.MockINotifier, *notifyRecorder) {
	GinkgoHelper()
	rec := &notifyRecorder{}
	ctrl := gomock.NewController(GinkgoT())
	m := mocks.NewMockINotifier(ctrl)
	m.EXPECT().Notify(gomock.Any(), gomock.Any()).DoAndReturn(rec.record).AnyTimes()
	return m, rec
}

// srcStub is a warden.IViewSource whose view is settable and whose Subscribe
// channels are signalled by push(), so a spec can wake the watchdog loop
// deterministically without relying on the (test-suppressed) tick.
type srcStub struct {
	mu   sync.Mutex
	view warden.ClusterView
	subs []chan warden.ClusterView
}

func newSrc(initial warden.ClusterView) *srcStub { return &srcStub{view: initial} }

func (s *srcStub) View() warden.ClusterView {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.view
}

func (s *srcStub) Subscribe(buf int) (<-chan warden.ClusterView, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan warden.ClusterView, buf)
	s.subs = append(s.subs, ch)
	return ch, func() {}
}

// push sets the view and signals subscribers so the loop re-evaluates.
func (s *srcStub) push(v warden.ClusterView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.view = v
	for _, ch := range s.subs {
		select {
		case ch <- v:
		default:
		}
	}
}

func peer(id string, status warden.PeerStatus) warden.PeerView {
	return warden.PeerView{Node: warden.Node{ID: warden.NodeID(id), Addr: id + ":7717"}, Status: status}
}

// leaderView builds an authoritative, self-sourced leader view over a 5-node
// cluster (quorum 3): self plus the given peers. Callers pass enough alive
// peers to keep the isolation guard satisfied unless they are testing it.
func leaderView(peers ...warden.PeerView) warden.ClusterView {
	all := append([]warden.PeerView{peer(string(selfID), warden.StatusAlive)}, peers...)
	return warden.ClusterView{
		Self: selfID, Role: warden.RoleLeader, Term: 7, LeaderID: selfID, Source: selfID,
		Authoritative: true, UpdatedAt: wdStart, Peers: all,
	}
}

// healthyView: self + 4 alive peers (fully alive quorum).
func healthyView() warden.ClusterView {
	return leaderView(peer("p1", warden.StatusAlive), peer("p2", warden.StatusAlive),
		peer("p3", warden.StatusAlive), peer("p4", warden.StatusAlive))
}

// deadPeer1View: p1 dead, p2..p4 alive => alive=4 >= quorum 3 (guard passes).
func deadPeer1View() warden.ClusterView {
	return leaderView(peer("p1", warden.StatusDead), peer("p2", warden.StatusAlive),
		peer("p3", warden.StatusAlive), peer("p4", warden.StatusAlive))
}

// aliveP1View: everyone alive again (closes p1's episode).
func aliveP1View() warden.ClusterView { return healthyView() }

// startWatchdog runs wd.Run and returns a stop func that cancels and joins it.
func startWatchdog(wd *watchdog.Watchdog) func() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); _ = wd.Run(ctx) }()
	return func() { cancel(); <-done }
}

func deathCount(rec *notifyRecorder) int {
	n := 0
	for _, inc := range rec.Sent() {
		if inc.Type == warden.IncidentPeerDead {
			n++
		}
	}
	return n
}

func recoveryCount(rec *notifyRecorder) int {
	n := 0
	for _, inc := range rec.Sent() {
		if inc.Type == warden.IncidentPeerRecovered {
			n++
		}
	}
	return n
}

var _ = Describe("Watchdog death alerting", func() {
	var (
		src *srcStub
		rec *notifyRecorder
		clk *testclock.Clock
		wd  *watchdog.Watchdog
	)

	newWD := func(cfg watchdog.Config) {
		var mock *mocks.MockINotifier
		mock, rec = recordingNotifier()
		clk = testclock.New(wdStart)
		src = newSrc(healthyView())
		// A large CheckInterval means the ticker never fires in-test; evaluations
		// are driven explicitly via src.push, and Now() moves only via Advance.
		cfg.CheckInterval = time.Hour
		wd = watchdog.New(cfg, src, mock, clk)
		// DeferCleanup(stop) is registered AFTER the mock controller's own Finish
		// cleanup, so LIFO ordering joins Run before the controller is finished.
		DeferCleanup(startWatchdog(wd))
	}

	It("emits exactly one death notification per continuous outage", func() {
		newWD(watchdog.Config{Cooldown: 10 * time.Minute, NotifyRecovery: false})
		// Peer dies and stays dead across several re-evaluations.
		for i := 0; i < 4; i++ {
			src.push(deadPeer1View())
		}
		Eventually(func() int { return deathCount(rec) }, "2s", "10ms").Should(Equal(1))
		// It never fires a second time for the same open episode.
		Consistently(func() int { return deathCount(rec) }, "300ms", "20ms").Should(Equal(1))
	})

	It("records the death incident in the log with the correct type and peer", func() {
		newWD(watchdog.Config{Cooldown: 10 * time.Minute, NotifyRecovery: false})
		src.push(deadPeer1View())
		Eventually(func() int { return deathCount(rec) }, "2s", "10ms").Should(Equal(1))

		var incidents []warden.Incident
		Eventually(func() []warden.Incident {
			incidents = wd.Incidents()
			return incidents
		}).ShouldNot(BeEmpty())
		inc := incidents[0] // most recent first
		Expect(inc.Type).To(Equal(warden.IncidentPeerDead))
		Expect(inc.Peer.ID).To(Equal(warden.NodeID("p1")))
		Expect(inc.ReportedBy).To(Equal(selfID))
	})

	It("suppresses a repeat death within the cooldown window but still records it", func() {
		newWD(watchdog.Config{Cooldown: 10 * time.Minute, NotifyRecovery: false})

		// Episode 1: delivered.
		src.push(deadPeer1View())
		Eventually(func() int { return deathCount(rec) }, "2s", "10ms").Should(Equal(1))

		// Close the episode and WAIT until that transition is observed (the
		// recovery is recorded even though NotifyRecovery is off). Without this
		// wait an alive->dead push pair could coalesce into a single evaluation.
		src.push(aliveP1View())
		Eventually(func() int { return countType(wd.Incidents(), warden.IncidentPeerRecovered) }, "2s", "10ms").Should(Equal(1))

		// 5 minutes later (< 10m cooldown) it dies again.
		clk.Advance(5 * time.Minute)
		src.push(deadPeer1View())

		// The second death IS recorded in the incident log (two peer_dead entries) ...
		Eventually(func() int { return countType(wd.Incidents(), warden.IncidentPeerDead) }, "2s", "10ms").
			Should(Equal(2))
		// ... but is NOT notified (suppressed by cooldown).
		Consistently(func() int { return deathCount(rec) }, "300ms", "20ms").Should(Equal(1))
	})

	It("re-notifies once the cooldown window has fully elapsed", func() {
		newWD(watchdog.Config{Cooldown: 10 * time.Minute, NotifyRecovery: false})

		src.push(deadPeer1View())
		Eventually(func() int { return deathCount(rec) }, "2s", "10ms").Should(Equal(1))

		src.push(aliveP1View())
		Eventually(func() int { return countType(wd.Incidents(), warden.IncidentPeerRecovered) }, "2s", "10ms").Should(Equal(1))

		clk.Advance(11 * time.Minute) // beyond the 10m cooldown
		src.push(deadPeer1View())

		Eventually(func() int { return deathCount(rec) }, "2s", "10ms").Should(Equal(2))
	})

	It("re-notifies at exactly the cooldown boundary (suppression is strict '<')", func() {
		// The suppression predicate is `now.Sub(last) < Cooldown`, so an elapsed
		// interval of EXACTLY Cooldown must NOT be suppressed.
		newWD(watchdog.Config{Cooldown: 10 * time.Minute, NotifyRecovery: false})

		src.push(deadPeer1View())
		Eventually(func() int { return deathCount(rec) }, "2s", "10ms").Should(Equal(1))

		src.push(aliveP1View())
		Eventually(func() int { return countType(wd.Incidents(), warden.IncidentPeerRecovered) }, "2s", "10ms").Should(Equal(1))

		clk.Advance(10 * time.Minute) // exactly the cooldown
		src.push(deadPeer1View())

		Eventually(func() int { return deathCount(rec) }, "2s", "10ms").Should(Equal(2))
	})
})

var _ = Describe("Watchdog isolation guard", func() {
	It("stays silent when the leader cannot see a live majority", func() {
		mock, rec := recordingNotifier()
		clk := testclock.New(wdStart)
		// self alive + p1 alive + p2,p3,p4 dead => alive=2 < quorum 3.
		isolated := leaderView(peer("p1", warden.StatusAlive),
			peer("p2", warden.StatusDead), peer("p3", warden.StatusDead), peer("p4", warden.StatusDead))
		src := newSrc(isolated)
		wd := watchdog.New(watchdog.Config{Cooldown: time.Minute, CheckInterval: time.Hour}, src, mock, clk)
		stop := startWatchdog(wd)
		defer stop()

		for i := 0; i < 4; i++ {
			src.push(isolated)
		}
		// No alerts AND no recorded incidents: evaluation is frozen below quorum.
		Consistently(func() int { return len(rec.Sent()) }, "400ms", "20ms").Should(Equal(0))
		Consistently(func() int { return len(wd.Incidents()) }, "100ms", "20ms").Should(Equal(0))
	})

	It("resumes alerting once a live majority is visible again", func() {
		mock, rec := recordingNotifier()
		clk := testclock.New(wdStart)
		isolated := leaderView(peer("p1", warden.StatusAlive),
			peer("p2", warden.StatusDead), peer("p3", warden.StatusDead), peer("p4", warden.StatusDead))
		src := newSrc(isolated)
		wd := watchdog.New(watchdog.Config{Cooldown: time.Minute, CheckInterval: time.Hour}, src, mock, clk)
		stop := startWatchdog(wd)
		defer stop()

		src.push(isolated)
		Consistently(func() int { return len(rec.Sent()) }, "200ms", "20ms").Should(Equal(0))

		// Quorum restored (p2,p3 alive), p1 still dead -> now it may alert.
		recovered := leaderView(peer("p1", warden.StatusDead), peer("p2", warden.StatusAlive),
			peer("p3", warden.StatusAlive), peer("p4", warden.StatusAlive))
		src.push(recovered)
		Eventually(func() int { return len(rec.Sent()) }, "2s", "10ms").Should(BeNumerically(">=", 1))
	})
})

var _ = Describe("Watchdog recovery and leadership gating", func() {
	It("notifies recovery when enabled, after a death episode closes", func() {
		mock, rec := recordingNotifier()
		clk := testclock.New(wdStart)
		src := newSrc(healthyView())
		wd := watchdog.New(watchdog.Config{Cooldown: time.Minute, NotifyRecovery: true, CheckInterval: time.Hour}, src, mock, clk)
		stop := startWatchdog(wd)
		defer stop()

		src.push(deadPeer1View())
		Eventually(func() int { return deathCount(rec) }, "2s", "10ms").Should(Equal(1))
		src.push(aliveP1View())
		Eventually(func() int { return recoveryCount(rec) }, "2s", "10ms").Should(Equal(1))
	})

	It("does NOT notify recovery when disabled, but still records it", func() {
		mock, rec := recordingNotifier()
		clk := testclock.New(wdStart)
		src := newSrc(healthyView())
		wd := watchdog.New(watchdog.Config{Cooldown: time.Minute, NotifyRecovery: false, CheckInterval: time.Hour}, src, mock, clk)
		stop := startWatchdog(wd)
		defer stop()

		src.push(deadPeer1View())
		Eventually(func() int { return deathCount(rec) }, "2s", "10ms").Should(Equal(1))
		src.push(aliveP1View())
		// recovery recorded in the log but never delivered.
		Eventually(func() int { return countType(wd.Incidents(), warden.IncidentPeerRecovered) }).Should(Equal(1))
		Consistently(func() int { return recoveryCount(rec) }, "200ms", "20ms").Should(Equal(0))
	})

	It("emits nothing when this node is not the acting leader", func() {
		mock, rec := recordingNotifier()
		clk := testclock.New(wdStart)
		follower := deadPeer1View()
		follower.Role = warden.RoleFollower // not leader
		follower.Authoritative = true
		src := newSrc(follower)
		wd := watchdog.New(watchdog.Config{Cooldown: time.Minute, CheckInterval: time.Hour}, src, mock, clk)
		stop := startWatchdog(wd)
		defer stop()

		for i := 0; i < 3; i++ {
			src.push(follower)
		}
		Consistently(func() int { return len(rec.Sent()) }, "300ms", "20ms").Should(Equal(0))
	})

	It("emits nothing when the leader view is non-authoritative", func() {
		mock, rec := recordingNotifier()
		clk := testclock.New(wdStart)
		v := deadPeer1View()
		v.Authoritative = false // leader-role but not the authoritative source
		src := newSrc(v)
		wd := watchdog.New(watchdog.Config{Cooldown: time.Minute, CheckInterval: time.Hour}, src, mock, clk)
		stop := startWatchdog(wd)
		defer stop()

		for i := 0; i < 3; i++ {
			src.push(v)
		}
		Consistently(func() int { return len(rec.Sent()) }, "300ms", "20ms").Should(Equal(0))
	})
})

var _ = Describe("IncidentLog contract", func() {
	It("returns incidents most-recent-first as defensive copies", func() {
		mock, rec := recordingNotifier()
		clk := testclock.New(wdStart)
		src := newSrc(healthyView())
		wd := watchdog.New(watchdog.Config{Cooldown: time.Hour, NotifyRecovery: true, CheckInterval: time.Hour}, src, mock, clk)
		stop := startWatchdog(wd)
		defer stop()

		src.push(deadPeer1View())
		Eventually(func() int { return deathCount(rec) }, "2s", "10ms").Should(Equal(1))
		src.push(aliveP1View())
		Eventually(func() int { return len(wd.Incidents()) }, "2s", "10ms").Should(Equal(2))

		got := wd.Incidents()
		// Most recent first: recovery precedes the earlier death.
		Expect(got[0].Type).To(Equal(warden.IncidentPeerRecovered))
		Expect(got[1].Type).To(Equal(warden.IncidentPeerDead))

		// Mutating the returned slice must not affect a later read (defensive copy).
		got[0].Message = "tampered"
		Expect(wd.Incidents()[0].Message).NotTo(Equal("tampered"))
	})
})

// --- helpers referenced above -------------------------------------------------

func countType(incidents []warden.Incident, typ warden.IncidentType) int {
	n := 0
	for _, inc := range incidents {
		if inc.Type == typ {
			n++
		}
	}
	return n
}
