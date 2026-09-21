package election

// Specs for the most safety-critical branch of the election state machine:
// what happens when persisting (term, votedFor) FAILS. Per the Raft safety
// argument, a node must never act on a vote (its own or a grant) that has not
// been made durable first — a Save failure must abort the action entirely, and
// the node must stay live so it can retry once the store recovers.

import (
	"context"
	"errors"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/internal/mocks"
	"github.com/candacelabs/csf/services/warden/notify"
	"github.com/candacelabs/csf/services/warden/store"
	"github.com/candacelabs/csf/services/warden/testclock"
	"github.com/candacelabs/csf/services/warden/watchdog"
)

var errSaveFail = errors.New("persist_fail_test: injected Save failure")

// failingStore wraps a real Store and fails Save on command. Load always
// delegates. Safe for concurrent use (Save is called from election loops).
//
// It is a behavioral fake plugged into the deterministic harness cluster (via
// newClusterWithStores) and driven by the running election loops across
// simulated time; it is NOT converted to a gomock expectation-based double,
// because expressing "fail every Save the loop attempts over N timeouts, then
// recover mid-run" as call-count expectations would be brittle and is exactly
// the harness-simulated behavior the mocks/simulators policy says not to mock.
type failingStore struct {
	inner warden.IStore

	mu   sync.Mutex
	fail bool
}

func newFailingStore(fail bool) *failingStore {
	return &failingStore{inner: store.NewMemStore(), fail: fail}
}

func (f *failingStore) SetFail(fail bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fail = fail
}

func (f *failingStore) Save(st warden.PersistentState) error {
	f.mu.Lock()
	fail := f.fail
	f.mu.Unlock()
	if fail {
		return errSaveFail
	}
	return f.inner.Save(st)
}

func (f *failingStore) Load() (warden.PersistentState, bool, error) {
	return f.inner.Load()
}

// newClusterWithStores mirrors newClusterWithTimings but lets each node bring
// its own Store, so tests can inject persistence failures per node.
func newClusterWithStores(t iHarnessT, tim harnessTimings, stores map[warden.NodeID]warden.IStore, ids ...warden.NodeID) *cluster {
	t.Helper()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := &cluster{
		t:       t,
		clock:   testclock.New(start),
		timings: tim,
		ids:     append([]warden.NodeID(nil), ids...),
		byID:    make(map[warden.NodeID]*node),
		killed:  make(map[warden.NodeID]bool),
		part:    make(map[warden.NodeID]int),
		startAt: start,
	}
	for _, id := range ids {
		c.nodes = append(c.nodes, warden.Node{ID: id, Addr: string(id)})
	}
	for _, id := range ids {
		c.start(id, stores[id])
	}
	t.Cleanup(c.stopAll)
	return c
}

// newDiscoveryClusterWithStores mirrors newDiscoveryCluster but lets each node
// bring its own Store (like newClusterWithStores), so tests can inject
// per-node persistence failures in discovery mode — e.g. a follower whose
// disk write fails while adopting a leader-committed membership change.
func newDiscoveryClusterWithStores(t iHarnessT, tim harnessTimings, stores map[warden.NodeID]warden.IStore, join, remove time.Duration, ids ...warden.NodeID) *cluster {
	t.Helper()
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := &cluster{
		t:             t,
		clock:         testclock.New(start),
		timings:       tim,
		ids:           append([]warden.NodeID(nil), ids...),
		byID:          make(map[warden.NodeID]*node),
		killed:        make(map[warden.NodeID]bool),
		part:          make(map[warden.NodeID]int),
		startAt:       start,
		discovery:     true,
		clusterID:     "candacenet-test",
		joinStability: join,
		removeAfter:   remove,
		disco:         make(map[warden.NodeID]*fakeDiscoverer),
		seedPeers:     make(map[warden.NodeID][]warden.Node),
	}
	for _, id := range ids {
		c.nodes = append(c.nodes, warden.Node{ID: id, Addr: string(id)})
	}
	for _, id := range ids {
		c.start(id, stores[id])
	}
	t.Cleanup(c.stopAll)
	return c
}

var _ = Describe("persistence-failure safety", func() {
	// TestElectionNotStartedWhenSaveFails: a node whose Save fails must not
	// start elections — no self-vote, no candidacy, no term advance — while the
	// election timer keeps re-arming so store recovery lets a normal election
	// succeed. This is a multi-node behavioral property over simulated time, so
	// it keeps the harness + failingStore fake (not gomock).
	It("starts no election while Save fails and elects once the store recovers", func() {
		tim := defaultTimings()
		stores := map[warden.NodeID]warden.IStore{
			"n1": newFailingStore(true),
			"n2": newFailingStore(true),
			"n3": newFailingStore(true),
		}
		c := newClusterWithStores(GinkgoT(), tim, stores, "n1", "n2", "n3")

		// Many election timeouts elapse; every candidacy attempt hits the
		// failing Save and must be abandoned before any state changes.
		c.Advance(10 * tim.ETMax)
		Expect(c.leaders()).To(BeEmpty(), "no node may become leader while Save fails")
		for _, id := range c.ids {
			Expect(c.role(id)).To(Equal(warden.RoleFollower), "%s should stay follower while Save fails", id)
			Expect(c.term(id)).To(Equal(warden.Term(0)), "%s term must not advance (no candidacy)", id)
			_, ok, err := stores[id].Load()
			Expect(err).NotTo(HaveOccurred(), "%s load", id)
			Expect(ok).To(BeFalse(), "%s: nothing may be persisted", id)
		}

		// Store recovery: the timers must still be armed, so a normal election
		// now proceeds — proving the failure path re-armed rather than wedged.
		for _, st := range stores {
			st.(*failingStore).SetFail(false)
		}
		leader := c.electLeader()
		Expect(c.term(leader)).NotTo(Equal(warden.Term(0)), "term did not advance after store recovery")
		ps, ok, err := stores[leader].Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue(), "leader state not persisted after recovery")
		Expect(ps.VotedFor).To(Equal(leader), "leader persisted VotedFor should be self")
	})

	// TestVoteNotGrantedWhenSaveFails: a vote grant whose Save fails must be
	// refused (Granted=false) with nothing persisted; once the store recovers
	// the same request is granted and durable.
	//
	// gomock swap: the hand-rolled failing Store is replaced by a MockIStore, and
	// persist-before-vote is expressed as an ORDERED expectation — the manager
	// must Load once at construction, then Save (term,vote) BEFORE granting; the
	// first Save fails (refuse), the identical second Save succeeds (grant). The
	// Save argument is matched exactly, which also pins the persisted content.
	It("refuses a vote when Save fails and grants + persists once the store recovers", func() {
		ctrl := gomock.NewController(GinkgoT())
		st := mocks.NewMockIStore(ctrl)

		req := warden.VoteRequest{Term: 10, CandidateID: "b"}
		wantPS := warden.PersistentState{CurrentTerm: req.Term, VotedFor: req.CandidateID}
		gomock.InOrder(
			st.EXPECT().Load().Return(warden.PersistentState{}, false, nil),
			st.EXPECT().Save(wantPS).Return(errSaveFail), // persist-before-vote attempt fails
			st.EXPECT().Save(wantPS).Return(nil),         // recovery: identical persist succeeds
		)

		// Solo manager (frozen clock => it never self-elects, so Save is called
		// only by the grant path under test).
		s := newSolo(GinkgoT(), st)

		resp := s.m.HandleVote(context.Background(), req)
		Expect(resp.Granted).To(BeFalse(), "vote granted despite failing Save — persist-before-vote violated")

		resp = s.m.HandleVote(context.Background(), req)
		Expect(resp.Granted).To(BeTrue(), "vote not granted after store recovery")
	})

	// TestMembershipAckNotCountedWhenSaveFails: the durability half of the
	// one-at-a-time settle rule. A follower whose disk write fails while
	// adopting a leader-committed membership change must NOT be counted as
	// having acked that version — onHeartbeat must fold adoptMembership's
	// persist failure into OK=false, and onHeartbeatResult must then skip
	// updating ackedVersion for that peer (see heartbeat.go, membership.go).
	//
	// Without that, a follower answering OK despite a failed Save lets the
	// leader's changeSettled() count a phantom ack, declaring a change
	// "settled" before a real quorum has durably stored it. Concretely (this
	// mirrors the reviewer's own counterexample): a 3-voter {a,b,c} cluster
	// has quorum 2. If BOTH non-leader followers' Saves fail, the leader's own
	// self-ack is the only DURABLE ack that can ever exist (1, short of
	// quorum 2) — so admitting one joiner must permanently block admitting a
	// second, one-at-a-time, until a real quorum acks. The phantom-ack bug
	// instead lets both admissions race through back-to-back even though b
	// and c never persisted anything past the founding membership.
	It("never counts a membership ack from a follower whose Save failed", func() {
		tim := defaultTimings()
		stores := map[warden.NodeID]warden.IStore{
			"a": newFailingStore(false),
			"b": newFailingStore(false),
			"c": newFailingStore(false),
		}
		c := newDiscoveryClusterWithStores(GinkgoT(), tim, stores, time.Second /*join*/, 0, "a", "b", "c")

		// Elect first (every store healthy, so whoever wins is irrelevant),
		// THEN fail the two followers' stores — sidesteps needing to predict
		// or force which node the randomized election picks as leader.
		leader := c.electLeader("a", "b", "c")
		followers := c.others(leader)
		Expect(followers).To(HaveLen(2))
		for _, f := range followers {
			stores[f].(*failingStore).SetFail(true)
		}

		// Leader admits a first joiner "d". Its own Save (on its own healthy
		// store) succeeds, so v2={a,b,c,d} commits locally — but neither
		// follower can durably adopt it.
		c.addObserver("d", rosterNodes("a", "b", "c"))
		c.pushRoster(nil, "a", "b", "c", "d")
		c.Advance(c.joinStability + 10*tim.Heartbeat)
		Expect(c.membershipVersion(leader)).To(Equal(uint64(2)), "leader failed to commit the first (locally-persisted) admission")

		// A second joiner "e" becomes eligible. It must NEVER be admitted:
		// doing so requires v2 to be "settled" first, which requires a real
		// quorum(3)=2 of DURABLE acks — leader(self)=1 is all that can ever
		// exist while both followers' Saves keep failing.
		c.addObserver("e", rosterNodes("a", "b", "c", "d"))
		c.pushRoster(nil, "a", "b", "c", "d", "e")
		c.Advance(c.joinStability + 20*tim.Heartbeat)

		Expect(c.membershipVersion(leader)).To(Equal(uint64(2)),
			"leader advanced past the unsettled v2 admission (phantom-ack bug: a non-durable quorum was counted as settled)")

		// Neither failing follower may ever have durably stored a membership
		// that includes "d" — proving whatever the leader believes about
		// their acks, it was never backed by a real Save.
		for _, f := range followers {
			Expect(c.persistedVoters(f)).NotTo(ContainElement(warden.NodeID("d")),
				"follower %s durably persisted the v2 admission despite its Save always failing", f)
		}
	})

	// TestReachableFollowerNotMarkedDeadOnMembershipSaveFailure: the liveness
	// half of the settle/durability fix above. A follower that is fully
	// reachable and answering every heartbeat on time — but whose disk
	// persistently fails while adopting a leader-committed membership
	// change — must NOT be misclassified as dead. Before this fix,
	// onHeartbeat's OK:false (correctly withholding the settle ack) also
	// made onHeartbeatResult skip updating lastContact, since OK gated BOTH
	// liveness and the ack. That let a perfectly healthy, reachable follower
	// age toward suspect/dead exactly like an unreachable one, and the
	// watchdog could raise a false peer_dead alert (and email the operator)
	// for a node that was never actually down. The fix decouples the two:
	// any well-formed response with an acceptable term proves liveness,
	// independent of OK; only the ack/settle advance is gated on OK.
	//
	// This wires a REAL watchdog.Watchdog against the leader's own View()
	// (the same production wiring cmd/main.go uses), sharing the harness's
	// simulated clock, for a literal end-to-end proof: no peer_dead incident
	// is ever recorded for the Save-failing follower. That is backed by the
	// more direct, deterministic assertion that actually guarantees it —
	// peerStatus never reaches StatusDead — since watchdog.evaluate's
	// incident trigger is gated exclusively on StatusDead; proving the
	// precondition false is sufficient to prove the incident can never fire.
	// It also re-confirms the companion invariant from the settle fix above:
	// the failing follower is still never counted toward settle.
	It("never marks a reachable follower dead merely because its membership Save fails", func() {
		tim := defaultTimings()
		stores := map[warden.NodeID]warden.IStore{
			"a": newFailingStore(false),
			"b": newFailingStore(false),
			"c": newFailingStore(false),
		}
		c := newDiscoveryClusterWithStores(GinkgoT(), tim, stores, time.Second /*join*/, 0, "a", "b", "c")

		// Elect first (every store healthy), THEN fail exactly one
		// follower's store — sidesteps needing to predict/force the
		// randomized election's winner.
		leader := c.electLeader("a", "b", "c")
		followers := c.others(leader)
		Expect(followers).To(HaveLen(2))
		victim, healthy := followers[0], followers[1]
		stores[victim].(*failingStore).SetFail(true)

		// A real watchdog watching the leader's own View(), exactly as
		// cmd/main.go wires it, sharing the harness's simulated clock so its
		// ticker advances in lockstep with c.Advance below.
		w := watchdog.New(watchdog.Config{CheckInterval: tim.Heartbeat}, c.manager(leader), notify.NewLogNotifier(), c.clock)
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- w.Run(ctx) }()
		GinkgoT().Cleanup(func() {
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				GinkgoT().Errorf("watchdog Run did not return after ctx cancel")
			}
		})

		// Trigger a membership change: admit "d". The leader's own Save
		// succeeds; victim's never does.
		c.addObserver("d", rosterNodes("a", "b", "c"))
		c.pushRoster(nil, "a", "b", "c", "d")

		// Advance well past DeadAfter — many heartbeat cycles — while
		// victim's Save keeps failing on every attempt to adopt the new
		// membership, yet it keeps answering heartbeats on time.
		c.Advance(c.joinStability + 5*tim.Dead)

		// Direct, deterministic proof: the leader's liveness view of victim
		// must still be Alive — it heartbeats on time every cycle; only its
		// membership Save fails.
		Expect(c.peerStatus(leader, victim)).To(Equal(warden.StatusAlive),
			"reachable follower %s marked non-alive despite heartbeating normally (only its membership Save fails)", victim)
		// Sanity: the healthy follower tracks normally too (the cluster
		// isn't silently broken some other way).
		Expect(c.peerStatus(leader, healthy)).To(Equal(warden.StatusAlive))

		// Literal, end-to-end proof: the real watchdog never raised a
		// peer_dead incident for victim (real-time polled: the watchdog's
		// own goroutine processes on its own schedule, independent of the
		// harness's Settle()).
		Eventually(func() []warden.Incident { return w.Incidents() }).
			WithTimeout(2*time.Second).WithPolling(time.Millisecond).
			Should(BeEmpty(), "watchdog raised an incident for a reachable peer whose only problem is a failing membership Save")

		// Companion invariant (from the settle/ack fix): victim is still
		// never counted toward settle — it never durably adopts "d".
		Expect(c.persistedVoters(victim)).NotTo(ContainElement(warden.NodeID("d")),
			"victim %s durably persisted the v2 admission despite its Save always failing", victim)
	})
})
