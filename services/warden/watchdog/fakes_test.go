package watchdog

import (
	"context"
	"errors"
	"sync"
	"time"

	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/internal/mocks"
)

// baseTime is the deterministic starting instant for tests.
var baseTime = time.Date(2026, 7, 20, 5, 0, 0, 0, time.UTC)

const (
	selfID   = warden.NodeID("node-c")
	selfAddr = "203.0.113.13:7717"
)

// --- notifier recorder (replaces the retired notify.Mock) -------------------

// errDeliveryFailed is the error a fail-primed MockINotifier returns; it stands
// in for the retired notify.Mock's ErrMockFailure. The retry specs only assert
// on the recorded-delivery count, so the exact error value is irrelevant.
var errDeliveryFailed = errors.New("watchdog test: simulated delivery failure")

// notifyRecorder captures incidents delivered to a mocks.MockINotifier via a
// DoAndReturn hook — the replacement for the retired notify.Mock's .Sent()
// recording. It is concurrency-safe because deliveries run on watchdog
// goroutines while the test goroutine reads Sent().
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

// Sent returns a defensive copy of all recorded incidents in delivery order.
func (r *notifyRecorder) Sent() []warden.Incident {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]warden.Incident, len(r.sent))
	copy(out, r.sent)
	return out
}

// recordingNotifier returns a MockINotifier whose Notify records every delivery
// into the returned recorder (DoAndReturn capture-to-slice).
func recordingNotifier(ctrl *gomock.Controller) (*mocks.MockINotifier, *notifyRecorder) {
	rec := &notifyRecorder{}
	m := mocks.NewMockINotifier(ctrl)
	m.EXPECT().Notify(gomock.Any(), gomock.Any()).DoAndReturn(rec.record).AnyTimes()
	return m, rec
}

// failThenRecordNotifier returns a MockINotifier that fails its first n Notify
// calls (returning errDeliveryFailed) and records every call thereafter — the
// gomock equivalent of the retired notify.Mock's FailNext(n) retry priming
// (first-N-error .Return sequencing, then DoAndReturn capture).
func failThenRecordNotifier(ctrl *gomock.Controller, n int) (*mocks.MockINotifier, *notifyRecorder) {
	rec := &notifyRecorder{}
	m := mocks.NewMockINotifier(ctrl)
	seq := make([]any, 0, n+1)
	for i := 0; i < n; i++ {
		seq = append(seq, m.EXPECT().Notify(gomock.Any(), gomock.Any()).Return(errDeliveryFailed))
	}
	seq = append(seq, m.EXPECT().Notify(gomock.Any(), gomock.Any()).DoAndReturn(rec.record).AnyTimes())
	gomock.InOrder(seq...)
	return m, rec
}

// --- fake clock -------------------------------------------------------------

// fakeClock is a deterministic warden.IClock. Now advances only via Advance,
// and its single Ticker fires only when the test calls tick(). After/NewTimer
// return channels that never fire; the watchdog uses neither. It is a
// behavioral simulator of time, not a mock.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	ticks  chan time.Time
	ticker warden.Ticker
}

func newFakeClock(now time.Time) *fakeClock {
	ticks := make(chan time.Time, 1)
	return &fakeClock{
		now:   now,
		ticks: ticks,
		// Stop is a no-op: this ticker is driven by tick(), never by time.
		ticker: warden.Ticker{C: ticks, Stop: func() {}},
	}
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *fakeClock) After(d time.Duration) <-chan time.Time { return make(chan time.Time) }

// NewTimer hands back a timer that never fires. The watchdog does not arm one;
// the fields exist so the value satisfies the documented Timer contract if it
// ever does.
func (c *fakeClock) NewTimer(d time.Duration) warden.Timer {
	return warden.Timer{
		C:     make(chan time.Time),
		Stop:  func() bool { return false },
		Reset: func(d time.Duration) bool { return false },
	}
}

func (c *fakeClock) NewTicker(d time.Duration) warden.Ticker { return c.ticker }

// tick fires the ticker once with the current time.
func (c *fakeClock) tick() { c.ticks <- c.Now() }

// --- fake view source -------------------------------------------------------

// fakeSource is a test warden.IViewSource. Tests set the view directly and,
// for Run-loop tests, push it to subscribers. viewCalls counts View() reads so
// tests can deterministically wait for the loop to have processed an evaluation
// without sleeping. It is a behavioral simulator, not a mock.
type fakeSource struct {
	mu        sync.Mutex
	view      warden.ClusterView
	subs      []chan warden.ClusterView
	viewCalls int
}

func newFakeSource() *fakeSource { return &fakeSource{} }

func (s *fakeSource) set(v warden.ClusterView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.view = v
}

func (s *fakeSource) View() warden.ClusterView {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.viewCalls++
	return s.view
}

func (s *fakeSource) views() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.viewCalls
}

func (s *fakeSource) Subscribe(buf int) (<-chan warden.ClusterView, func()) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch := make(chan warden.ClusterView, buf)
	s.subs = append(s.subs, ch)
	cancel := func() {
		s.mu.Lock()
		defer s.mu.Unlock()
		for i, c := range s.subs {
			if c == ch {
				s.subs = append(s.subs[:i], s.subs[i+1:]...)
				close(ch)
				return
			}
		}
	}
	return ch, cancel
}

// push sets the view and signals subscribers (best-effort, non-blocking).
func (s *fakeSource) push(v warden.ClusterView) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.view = v
	for _, c := range s.subs {
		select {
		case c <- v:
		default:
		}
	}
}

// --- view/peer builders -----------------------------------------------------

func node(id, addr string) warden.Node {
	return warden.Node{ID: warden.NodeID(id), Addr: addr}
}

func peer(id, addr string, status warden.PeerStatus, lastSeen time.Time) warden.PeerView {
	return warden.PeerView{Node: node(id, addr), Status: status, LastSeen: lastSeen}
}

// memberPeer is peer() with an explicit membership kind, for dynamic-membership
// scenarios (voter / observer / discovered).
func memberPeer(id, addr string, status warden.PeerStatus, lastSeen time.Time, kind warden.MemberKind) warden.PeerView {
	return warden.PeerView{Node: node(id, addr), Status: status, LastSeen: lastSeen, Member: kind}
}

// membership builds a voting configuration from a version and voter IDs
// (addresses are irrelevant to quorum, which keys on ID).
func membership(version uint64, voterIDs ...string) warden.Membership {
	m := warden.Membership{Version: version}
	for _, id := range voterIDs {
		m.Voters = append(m.Voters, node(id, ""))
	}
	return m
}

// membershipLeaderView is an authoritative self-sourced leader view carrying an
// explicit voting configuration. Unlike leaderView it adds no anchor peers: the
// caller supplies the exact peer set (including self) so quorum math is exact.
func membershipLeaderView(term warden.Term, m warden.Membership, peers ...warden.PeerView) warden.ClusterView {
	return warden.ClusterView{
		Self:          selfID,
		Role:          warden.RoleLeader,
		Term:          term,
		LeaderID:      selfID,
		Source:        selfID,
		Authoritative: true,
		Membership:    m,
		Peers:         peers,
	}
}

// leaderView returns an authoritative self-sourced leader view. Per the
// contract, Peers always contains self (alive in its own view); two alive
// "anchor" peers are added so the leader retains a live majority and the
// isolation guard in evaluate does not suppress the scenario under test.
// Tests that exercise quorum loss build their views directly instead.
func leaderView(term warden.Term, peers ...warden.PeerView) warden.ClusterView {
	all := []warden.PeerView{
		peer(string(selfID), "203.0.113.13:7717", warden.StatusAlive, baseTime),
		peer("anchor-a", "100.64.0.10:7717", warden.StatusAlive, baseTime),
		peer("anchor-b", "100.64.0.11:7717", warden.StatusAlive, baseTime),
	}
	all = append(all, peers...)
	return warden.ClusterView{
		Self:          selfID,
		Role:          warden.RoleLeader,
		Term:          term,
		LeaderID:      selfID,
		Source:        selfID,
		Authoritative: true,
		Peers:         all,
	}
}

func followerView(term warden.Term, leader warden.NodeID, peers ...warden.PeerView) warden.ClusterView {
	return warden.ClusterView{
		Self:          selfID,
		Role:          warden.RoleFollower,
		Term:          term,
		LeaderID:      leader,
		Source:        leader,
		Authoritative: true,
		Peers:         peers,
	}
}
