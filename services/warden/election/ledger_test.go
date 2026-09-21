package election

// Regression-ledger specs: each pins a defect found during the project so its
// reintroduction turns a spec red.
//
//   - duplicate/replayed vote grants must never form a false majority
//     (review minor m1: vote counter -> per-voter set);
//   - static mode must ignore a persisted Membership (fan-in collision:
//     main.go once passed a NewStatic discoverer in static mode, which would
//     have made persisted rosters override config edits);
//   - a *typed* nil discoverer is not a nil discoverer: discoveryMode is
//     `cfg.Discoverer != nil`, so a nil concrete pointer stored in the
//     interface silently selects dynamic membership. This is the hazard that
//     kept the mode switch inside main's wiring rather than in a factory
//     returning warden.IPeerDiscoverer (house rule CS-8);
//   - a restarted node must not replay its previous election-timeout
//     schedule (review ratification of the seed-entropy fix).

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/store"
	"github.com/candacelabs/csf/services/warden/testclock"
)

func ledgerNodes(ids ...warden.NodeID) []warden.Node {
	out := make([]warden.Node, 0, len(ids))
	for _, id := range ids {
		out = append(out, warden.Node{ID: id, Addr: string(id)})
	}
	return out
}

// ledgerDisc is a minimal PeerDiscoverer whose channel never delivers: it
// exists purely to flip a Manager into discovery mode for white-box tests.
type ledgerDisc struct{}

func (ledgerDisc) Discover(ctx context.Context) (<-chan warden.Roster, error) {
	ch := make(chan warden.Roster)
	go func() { <-ctx.Done(); close(ch) }()
	return ch, nil
}

func ledgerLeader(t iHarnessT) *Manager {
	t.Helper()
	tim := defaultTimings()
	cfg := Config{
		Self:               warden.Node{ID: "a", Addr: "a"},
		Peers:              ledgerNodes("a", "b", "c"),
		HeartbeatInterval:  tim.Heartbeat,
		SuspectAfter:       tim.Suspect,
		DeadAfter:          tim.Dead,
		ElectionTimeoutMin: tim.ETMin,
		ElectionTimeoutMax: tim.ETMax,
		RPCTimeout:         tim.RPCTimeout,
		JoinStability:      time.Second,
		Discoverer:         ledgerDisc{},
	}
	m, err := NewManager(cfg, stubTransport(), store.NewMemStore(), testclock.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

var _ = Describe("election regression ledger", func() {
	// TestDuplicateVoteGrantCannotFormMajority: drives onVoteResult directly
	// (white-box, loop not running): in a 5-voter cluster (quorum 3), the same
	// voter's grant replayed any number of times contributes exactly one vote.
	It("does not let a duplicate/replayed vote grant form a majority", func() {
		tim := defaultTimings()
		cfg := Config{
			Self:               warden.Node{ID: "a", Addr: "a"},
			Peers:              ledgerNodes("a", "b", "c", "d", "e"),
			HeartbeatInterval:  tim.Heartbeat,
			SuspectAfter:       tim.Suspect,
			DeadAfter:          tim.Dead,
			ElectionTimeoutMin: tim.ETMin,
			ElectionTimeoutMax: tim.ETMax,
			RPCTimeout:         tim.RPCTimeout,
		}
		m, err := NewManager(cfg, stubTransport(), store.NewMemStore(), testclock.New(time.Unix(0, 0)))
		Expect(err).NotTo(HaveOccurred(), "NewManager")

		// Candidate at term 1 holding only its self-vote.
		m.role = warden.RoleCandidate
		m.currentTerm = 1
		m.votesFrom = map[warden.NodeID]bool{"a": true}

		grant := func(from warden.NodeID) voteResultMsg {
			return voteResultMsg{
				term: 1,
				from: from,
				resp: warden.VoteResponse{Term: 1, Granted: true, VoterID: from},
			}
		}

		// The same voter replayed five times: still 2 distinct votes of 3 needed.
		for i := 0; i < 5; i++ {
			m.onVoteResult(grant("b"))
		}
		Expect(m.role).To(Equal(warden.RoleCandidate), "duplicates must not count toward majority")

		// A grant from a DISTINCT voter completes the real majority (a, b, c).
		m.onVoteResult(grant("c"))
		Expect(m.role).To(Equal(warden.RoleLeader), "grants from distinct voters a,b,c should elect")
	})

	// TestStaticModeIgnoresPersistedMembership: with no Discoverer configured,
	// effective membership mirrors the config peer list exactly (Version 0) even
	// when the store holds a persisted Membership from an earlier dynamic life.
	It("ignores a persisted Membership in static mode", func() {
		tim := defaultTimings()
		st := store.NewMemStore()
		persisted := warden.Membership{Version: 5, Voters: ledgerNodes("a", "b", "c", "ghost")}
		Expect(st.Save(warden.PersistentState{CurrentTerm: 3, VotedFor: "b", Membership: &persisted})).To(Succeed(), "seeding store")

		cfg := Config{
			Self:               warden.Node{ID: "a", Addr: "a"},
			Peers:              ledgerNodes("a", "b", "c"),
			HeartbeatInterval:  tim.Heartbeat,
			SuspectAfter:       tim.Suspect,
			DeadAfter:          tim.Dead,
			ElectionTimeoutMin: tim.ETMin,
			ElectionTimeoutMax: tim.ETMax,
			RPCTimeout:         tim.RPCTimeout,
			// Discoverer deliberately nil: static mode.
		}
		m, err := NewManager(cfg, stubTransport(), st, testclock.New(time.Unix(0, 0)))
		Expect(err).NotTo(HaveOccurred(), "NewManager")
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- m.Run(ctx) }()
		defer func() { cancel(); <-done }()

		v := m.View()
		Expect(v.Membership.Version).To(Equal(uint64(0)), "persisted membership must be ignored in static mode")
		Expect(v.Membership.Voters).To(HaveLen(3), "static-mode voters should be exactly the 3 config peers")
		Expect(v.Membership.HasVoter("ghost")).To(BeFalse(), "persisted ghost voter must not appear")
		// Term/vote persistence is unaffected by static membership semantics.
		Expect(v.Term).To(Equal(warden.Term(3)), "term durability must survive")
	})

	// TestTypedNilDiscovererIsNotStatic pins the trap the spec above depends on
	// for its meaning. discoveryMode is `cfg.Discoverer != nil`, and a nil
	// *fakeDiscoverer stored in a warden.IPeerDiscoverer is NOT nil — so this
	// config, which looks static in every way a reader checks, adopts the
	// persisted roster instead of the config peer list. Nothing fails loudly.
	//
	// The spec exists because main's wiring is what stands between a fleet and
	// this: the discovery mode switch assigns concrete constructors into a
	// `var discoverer warden.IPeerDiscoverer` and its static arm assigns
	// nothing, because an unassigned variable is the one nil that cannot be
	// typed. A factory returning the interface has no such arm available.
	//
	// It does not Run the manager: with a typed-nil discoverer the run loop
	// would call Discover on a nil pointer, which is the second half of the
	// same bug and not what this pins.
	It("treats a typed-nil discoverer as discovery mode, not as static", func() {
		tim := defaultTimings()
		st := store.NewMemStore()
		persisted := warden.Membership{Version: 5, Voters: ledgerNodes("a", "b", "c", "ghost")}
		Expect(st.Save(warden.PersistentState{CurrentTerm: 3, VotedFor: "b", Membership: &persisted})).To(Succeed(), "seeding store")

		var absent *fakeDiscoverer // nil pointer, non-nil interface
		cfg := Config{
			Self:               warden.Node{ID: "a", Addr: "a"},
			Peers:              ledgerNodes("a", "b", "c"),
			HeartbeatInterval:  tim.Heartbeat,
			SuspectAfter:       tim.Suspect,
			DeadAfter:          tim.Dead,
			ElectionTimeoutMin: tim.ETMin,
			ElectionTimeoutMax: tim.ETMax,
			RPCTimeout:         tim.RPCTimeout,
			Discoverer:         absent,
			ClusterID:          "candacenet-test",
		}
		// Spelled as the language spells it, deliberately. Gomega's BeNil() is
		// reflection-based and reports this value as nil, which is the opposite
		// of what `cfg.Discoverer != nil` — the expression election.NewManager
		// actually evaluates — answers. A spec written with BeNil() would have
		// agreed with the wrong one.
		Expect(cfg.Discoverer != nil).To(BeTrue(), "a nil concrete pointer in an interface is not a nil interface")

		m, err := NewManager(cfg, stubTransport(), st, testclock.New(time.Unix(0, 0)))
		Expect(err).NotTo(HaveOccurred(), "NewManager")
		Expect(m.discoveryMode).To(BeTrue(), "a typed nil selects discovery mode, which is the whole hazard")
		Expect(m.membership.Version).To(Equal(uint64(5)), "the persisted roster is adopted, not ignored")
		Expect(m.membership.HasVoter("ghost")).To(BeTrue(), "a node absent from config is now a voter")
	})

	// TestRestartUsesFreshTimeoutSchedule pins the seed-entropy fix: the timeout
	// PRNG is seeded from the node ID XOR the clock at construction, so a
	// restarted node draws a different schedule, while identical clocks keep
	// test runs deterministic.
	It("draws a fresh timeout schedule on restart but stays deterministic for identical clocks", func() {
		tim := defaultTimings()
		build := func(startTime time.Time) *Manager {
			cfg := Config{
				Self:               warden.Node{ID: "a", Addr: "a"},
				Peers:              ledgerNodes("a", "b", "c"),
				HeartbeatInterval:  tim.Heartbeat,
				SuspectAfter:       tim.Suspect,
				DeadAfter:          tim.Dead,
				ElectionTimeoutMin: tim.ETMin,
				ElectionTimeoutMax: tim.ETMax,
				RPCTimeout:         tim.RPCTimeout,
			}
			m, err := NewManager(cfg, stubTransport(), store.NewMemStore(), testclock.New(startTime))
			Expect(err).NotTo(HaveOccurred(), "NewManager")
			return m
		}
		draw := func(m *Manager) []time.Duration {
			out := make([]time.Duration, 16)
			for i := range out {
				out[i] = m.randTimeout()
			}
			return out
		}

		t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		before := draw(build(t0))
		after := draw(build(t0.Add(time.Hour))) // "restart" one hour later
		Expect(after).NotTo(Equal(before), "restarted node replayed its previous timeout schedule")

		// Identical construction times yield identical schedules: the property
		// that keeps the simulated-clock test harness deterministic.
		a, b := draw(build(t0)), draw(build(t0))
		Expect(a).To(Equal(b), "identical seeds must produce identical schedules")
	})

	// TestChangeSettledRequiresMajorityAcks pins the settle predicate itself: a
	// membership change is settled ONLY once a majority of the current voter set
	// has acked its version.
	It("settles a membership change only on a majority of matching-version acks", func() {
		m := ledgerLeader(GinkgoT())
		m.role = warden.RoleLeader
		m.membership = warden.Membership{Version: 2, CreatedInTerm: 6, Voters: ledgerNodes("a", "b", "c", "o1")}

		m.ackedVersion = map[warden.NodeID]ackRef{"a": {2, 6}, "b": {2, 6}} // 2 of 4 < quorum 3
		Expect(m.changeSettled()).To(BeFalse(), "settle requires a majority of the NEW set (2/4)")
		m.ackedVersion["c"] = ackRef{2, 6} // 3 of 4
		Expect(m.changeSettled()).To(BeTrue(), "3/4 acks should settle")
		m.ackedVersion["c"] = ackRef{1, 6} // stale (lower-version) ack must not count
		Expect(m.changeSettled()).To(BeFalse(), "a stale (lower-version) ack must not count")
		// A voter holding a DIVERGENT SIBLING config — same Version minted by a
		// deposed leader in an OLDER term — must never count toward settling it.
		m.ackedVersion["c"] = ackRef{2, 5}
		Expect(m.changeSettled()).To(BeFalse(), "a divergent equal-version (older-term) ack must not count")
	})

	// TestNoSecondChangeWhileUnsettled pins one-at-a-time at the driver level:
	// with an unsettled change in flight, an admission-eligible observer must
	// NOT be admitted; once the change settles, it must be.
	It("admits no second membership change while the previous one is unsettled", func() {
		m := ledgerLeader(GinkgoT())
		m.role = warden.RoleLeader
		m.membership = warden.Membership{Version: 2, CreatedInTerm: 6, Voters: ledgerNodes("a", "b", "c", "o1")}
		m.currentTerm = 6
		m.ackedVersion = map[warden.NodeID]ackRef{"a": {2, 6}, "b": {2, 6}} // unsettled: 2/4

		o2 := warden.Node{ID: "o2", Addr: "o2"}
		m.candidates["o2"] = &candidate{
			node:          o2,
			inRoster:      true,
			verified:      true,
			eligibleSince: m.clock.Now().Add(-time.Minute), // well past JoinStability
		}

		m.maybeStartMembershipChange()
		Expect(m.membership.Version).To(Equal(uint64(2)), "membership must not change while previous change unsettled")
		Expect(m.membership.Voters).To(HaveLen(4), "one-at-a-time must hold voters at 4")

		m.ackedVersion["c"] = ackRef{2, 6} // settle v2
		m.maybeStartMembershipChange()
		Expect(m.membership.Version).To(Equal(uint64(3)), "eligible observer should be admitted after settle")
		Expect(m.membership.HasVoter("o2")).To(BeTrue(), "o2 should be a voter after admission")
	})

	// TestNonVoterGrantNotCounted pins the isVoter gate in vote counting: grants
	// from nodes outside the voter set — however many — must never contribute to
	// a majority.
	It("does not count grants from nodes outside the voter set", func() {
		tim := defaultTimings()
		cfg := Config{
			Self:               warden.Node{ID: "a", Addr: "a"},
			Peers:              ledgerNodes("a", "b", "c"),
			HeartbeatInterval:  tim.Heartbeat,
			SuspectAfter:       tim.Suspect,
			DeadAfter:          tim.Dead,
			ElectionTimeoutMin: tim.ETMin,
			ElectionTimeoutMax: tim.ETMax,
			RPCTimeout:         tim.RPCTimeout,
		}
		m, err := NewManager(cfg, stubTransport(), store.NewMemStore(), testclock.New(time.Unix(0, 0)))
		Expect(err).NotTo(HaveOccurred(), "NewManager")
		m.role = warden.RoleCandidate
		m.currentTerm = 1
		m.votesFrom = map[warden.NodeID]bool{"a": true}

		for _, imposter := range []warden.NodeID{"zed", "x", "y"} {
			m.onVoteResult(voteResultMsg{term: 1, from: imposter,
				resp: warden.VoteResponse{Term: 1, Granted: true, VoterID: imposter}})
		}
		Expect(m.role).To(Equal(warden.RoleCandidate), "non-voters must not count")
		m.onVoteResult(voteResultMsg{term: 1, from: "b",
			resp: warden.VoteResponse{Term: 1, Granted: true, VoterID: "b"}})
		Expect(m.role).To(Equal(warden.RoleLeader), "a real voter grant completing quorum 2/3 should elect")
	})

	// TestStaleTermHeartbeatCannotAdoptMembership pins the term-gate-before-
	// adoption ordering: a heartbeat from a stale term must be rejected BEFORE
	// its membership payload is considered, no matter how high the payload's
	// version claims to be.
	It("rejects a stale-term heartbeat before considering its membership payload", func() {
		tim := defaultTimings()
		st := store.NewMemStore()
		Expect(st.Save(warden.PersistentState{CurrentTerm: 6})).To(Succeed(), "seeding store")
		cfg := Config{
			Self:               warden.Node{ID: "a", Addr: "a"},
			Peers:              ledgerNodes("a", "b", "c"),
			HeartbeatInterval:  tim.Heartbeat,
			SuspectAfter:       tim.Suspect,
			DeadAfter:          tim.Dead,
			ElectionTimeoutMin: tim.ETMin,
			ElectionTimeoutMax: tim.ETMax,
			RPCTimeout:         tim.RPCTimeout,
			JoinStability:      time.Second,
			Discoverer:         ledgerDisc{},
		}
		m, err := NewManager(cfg, stubTransport(), st, testclock.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)))
		Expect(err).NotTo(HaveOccurred(), "NewManager")
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- m.Run(ctx) }()
		defer func() { cancel(); <-done }()

		evil := warden.Membership{Version: 99, CreatedInTerm: 5, Voters: ledgerNodes("a", "mallory")}
		resp := m.HandleHeartbeat(context.Background(), warden.HeartbeatRequest{
			Term: 5, LeaderID: "b", Membership: &evil,
		})
		Expect(resp.OK).To(BeFalse(), "stale-term heartbeat must be rejected")
		v := m.View()
		Expect(v.Membership.Version).To(Equal(uint64(1)), "term gate must precede adoption")
		Expect(v.Membership.HasVoter("mallory")).To(BeFalse(), "membership must not be adopted from a stale-term heartbeat")
	})
})
