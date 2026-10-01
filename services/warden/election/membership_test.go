package election

// Specs for dynamic membership + discovery consumption. They all run on the
// deterministic testclock harness (see harness_test.go) and its channel-driven
// fakeDiscoverer, and must be stable under -race across repeated runs
// (`ginkgo -race -repeat=2 .`; Ginkgo rejects `go test -count` above 1).

import (
	"context"
	"runtime"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/store"
	"github.com/candacelabs/csf/services/warden/testclock"
)

// viewMemberKind finds target's MemberKind in a view (or "" if absent).
func viewMemberKind(v warden.ClusterView, target warden.NodeID) warden.MemberKind {
	for _, p := range v.Peers {
		if p.Node.ID == target {
			return p.Member
		}
	}
	return ""
}

var _ = Describe("dynamic membership and discovery", func() {
	// TestJoinerConstructedWithoutSelfIsObserver: in discovery mode NewManager
	// MUST accept cfg.Self absent from cfg.Peers, and the node boots as a pure
	// observer — self not a voter, never a candidate/leader, term never advances.
	It("boots a joiner whose Self is absent from Peers as a pure observer", func() {
		clk := testclock.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		seed := []warden.Node{{ID: "n1", Addr: "n1"}, {ID: "n2", Addr: "n2"}, {ID: "n3", Addr: "n3"}}
		cfg := Config{
			Self:               warden.Node{ID: "d", Addr: "d"}, // deliberately NOT in seed
			Peers:              seed,
			Discoverer:         newFakeDiscoverer(),
			ClusterID:          "candacenet-test",
			HeartbeatInterval:  100 * time.Millisecond,
			ElectionTimeoutMin: 300 * time.Millisecond,
			ElectionTimeoutMax: 600 * time.Millisecond,
			SuspectAfter:       time.Second,
			DeadAfter:          3 * time.Second,
			RPCTimeout:         50 * time.Millisecond,
		}
		m, err := NewManager(cfg, stubTransport(), store.NewMemStore(), clk)
		Expect(err).NotTo(HaveOccurred(), "NewManager rejected a joiner (Self not in Peers) in discovery mode")
		ctx, cancel := context.WithCancel(context.Background())
		runDone := make(chan struct{})
		go func() { _ = m.Run(ctx); close(runDone) }()
		GinkgoT().Cleanup(func() { cancel(); <-runDone })

		v := m.View()
		Expect(v.Membership.Version).To(Equal(uint64(1)), "seed membership version should be 1")
		Expect(v.Membership.Voters).To(HaveLen(3), "seed membership should have 3 voters")
		Expect(v.Membership.HasVoter("d")).To(BeFalse(), "joiner d must NOT be a seed voter")
		Expect(viewMemberKind(v, "d")).To(Equal(warden.MemberObserver), "joiner self should render as observer")

		// Many election timeouts elapse: an observer never stands for election.
		for i := 0; i < 25; i++ {
			clk.Advance(cfg.ElectionTimeoutMax)
			m.barrier()
		}
		got := m.View()
		Expect(got.Role).NotTo(Equal(warden.RoleLeader), "observer must never lead")
		Expect(got.Role).NotTo(Equal(warden.RoleCandidate), "observer must never stand")
		Expect(got.Term).To(Equal(warden.Term(0)), "observer must never start an election")
		// A vote requested of the observer is refused.
		resp := m.HandleVote(context.Background(), warden.VoteRequest{Term: 9, CandidateID: "n1"})
		Expect(resp.Granted).To(BeFalse(), "observer granted a vote")
	})

	// TestDiscoveryAdmitsStableObserver (scenario 1): a 3-voter cluster gains
	// node d → d becomes a verified observer on every node's view, and after
	// JoinStability the leader admits it: Version bumps to 2, Voters == 4 on
	// every node, persisted on every node, and quorum is now 3.
	It("admits a stable observer after JoinStability, persisted on every node", func() {
		tim := defaultTimings()
		c := newDiscoveryCluster(GinkgoT(), tim, time.Second /*join*/, 0 /*remove*/, "n1", "n2", "n3")
		leader := c.electLeader("n1", "n2", "n3")

		// Brand-new joiner d whose seed is the existing voter set (no d).
		c.addObserver("d", rosterNodes("n1", "n2", "n3"))
		Expect(c.memberKind("d", "d")).To(Equal(warden.MemberObserver), "freshly-booted joiner d should be an observer")

		// The fleet roster now gains d, delivered to every node (including d).
		c.pushRoster(nil, "n1", "n2", "n3", "d")

		// Propagate the leader's updated authoritative view a couple of
		// heartbeat cycles — but stay well under JoinStability, so no admission.
		c.Advance(3 * tim.Heartbeat)

		for _, id := range []warden.NodeID{"n1", "n2", "n3", "d"} {
			Expect(c.memberKind(id, "d")).To(Equal(warden.MemberObserver), "%s should see d as MemberObserver", id)
			Expect(c.membershipVersion(id)).To(Equal(uint64(1)), "%s should have no admission before JoinStability", id)
		}
		Expect(c.role("d")).To(Equal(warden.RoleFollower), "observer d role should be follower")

		// Cross JoinStability: the leader admits d (a single node → Version 2).
		c.Advance(c.joinStability)

		want := []warden.NodeID{"d", "n1", "n2", "n3"}
		for _, id := range []warden.NodeID{"n1", "n2", "n3", "d"} {
			Expect(c.voterIDs(id)).To(Equal(want), "%s voters", id)
			Expect(c.membershipVersion(id)).To(Equal(uint64(2)), "%s version", id)
			Expect(c.persistedVoters(id)).To(Equal(want), "%s persisted voters", id)
			Expect(c.memberKind(id, "d")).To(Equal(warden.MemberVoter), "%s should now see d as MemberVoter", id)
		}
		Expect(warden.Quorum(len(c.voterIDs(leader)))).To(Equal(3), "quorum should be 3")
		Expect(c.view("d").LeaderID).To(Equal(leader), "admitted d should follow the leader")
	})

	// TestDiscoveryAdmitsOneAtATime (scenario 2): two nodes appear
	// simultaneously; admissions happen strictly sequentially (a distinct
	// 4-voter configuration is committed and observed before the 5-voter one),
	// with the version increasing by exactly one per admission.
	It("admits simultaneously-appearing observers strictly one at a time", func() {
		tim := defaultTimings()
		c := newDiscoveryCluster(GinkgoT(), tim, time.Second, 0, "n1", "n2", "n3")
		leader := c.electLeader("n1", "n2", "n3")

		c.addObserver("d", rosterNodes("n1", "n2", "n3"))
		c.addObserver("e", rosterNodes("n1", "n2", "n3"))

		// Watch the leader's published snapshots so we can prove an intermediate
		// 4-voter membership is committed before the 5-voter one.
		ch, cancel := c.manager(leader).Subscribe(256)
		defer cancel()

		// Both observers appear in the same roster snapshot.
		c.pushRoster(nil, "n1", "n2", "n3", "d", "e")

		var snaps []warden.ClusterView
		for i := 0; i < 400 && c.membershipVersion(leader) < 3; i++ {
			c.Advance(c.step())
			snaps = append(snaps, drainViews(ch)...)
		}
		snaps = append(snaps, drainViews(ch)...)

		// Ordering proof: the first 5-voter snapshot must be preceded by a
		// 4-voter one — i.e. d was admitted and committed before e.
		saw4, saw4Before5 := false, false
		for _, v := range snaps {
			switch len(v.Membership.Voters) {
			case 4:
				saw4 = true
			case 5:
				if saw4 {
					saw4Before5 = true
				}
			}
		}
		Expect(saw4).To(BeTrue(), "expected an intermediate 4-voter membership")
		Expect(saw4Before5).To(BeTrue(), "admissions were not sequential (4-voter before 5-voter)")

		// End state: both admitted, exactly two single-node version bumps.
		want := []warden.NodeID{"d", "e", "n1", "n2", "n3"}
		for _, id := range []warden.NodeID{"n1", "n2", "n3", "d", "e"} {
			Expect(c.voterIDs(id)).To(Equal(want), "%s voters", id)
			Expect(c.membershipVersion(id)).To(Equal(uint64(3)), "%s version (two sequential single-node admissions)", id)
		}
	})

	// TestObserverNeverElects (scenario 3): a node whose ID is not in Voters,
	// receiving no leader for many election timeouts, stays an observer, and
	// refuses any vote requested of it.
	It("keeps an observer that never hears a leader from ever electing", func() {
		tim := defaultTimings()
		c := newDiscoveryCluster(GinkgoT(), tim, time.Second, 0, "n1", "n2", "n3")
		c.electLeader("n1", "n2", "n3")

		// A joiner never placed in any roster: no leader ever heartbeats it.
		c.addObserver("d", rosterNodes("n1", "n2", "n3"))
		termBefore := c.term("d")

		for i := 0; i < 80; i++ {
			c.Advance(tim.ETMax)
			Expect(c.role("d")).To(Equal(warden.RoleFollower), "observer d became non-follower (step %d)", i)
			Expect(c.view("d").LeaderID).NotTo(Equal(warden.NodeID("d")), "observer d elected itself leader (step %d)", i)
		}
		Expect(c.term("d")).To(Equal(termBefore), "observer term advanced; observers must never start an election")
		resp := c.manager("d").HandleVote(context.Background(), warden.VoteRequest{Term: 99, CandidateID: "n1"})
		Expect(resp.Granted).To(BeFalse(), "observer granted a vote")
	})

	// TestVoteRejectedForNonVoterCandidate: the FLIP side of the observer
	// self-check above — a CURRENT VOTER must refuse a RequestVote whose
	// CANDIDATE is not in the current voter set (an observer, or a
	// not-yet-admitted joiner), even at a higher term, because that candidate
	// can never legitimately reach quorum (voterPeers/onVoteResult already
	// restrict candidacy and grant-counting to current voters). Critically,
	// refusing it must NOT burn the term's one-vote-per-term slot: a
	// legitimate voter candidate must still be able to collect that same
	// voter's vote for the very same (adopted, higher) term.
	It("rejects a vote for a non-voter candidate without burning the term's vote slot", func() {
		tim := defaultTimings()
		c := newDiscoveryCluster(GinkgoT(), tim, time.Hour /*join: effectively never, no Advance crosses it*/, 0, "n1", "n2", "n3")
		leader := c.electLeader("n1", "n2", "n3")
		c.addObserver("d", rosterNodes("n1", "n2", "n3")) // stays an observer for this test

		target := warden.NodeID("n1")
		if leader == target {
			target = "n2"
		}
		higherTerm := c.term(leader) + 10

		// Non-voter "d" requests a vote at a higher term: must be refused.
		resp := c.manager(target).HandleVote(context.Background(), warden.VoteRequest{Term: higherTerm, CandidateID: "d"})
		Expect(resp.Granted).To(BeFalse(), "voter %s granted a vote to non-voter candidate d", target)

		// The higher term must still be adopted (Raft term monotonicity)...
		Expect(c.term(target)).To(Equal(higherTerm), "voter %s failed to adopt the higher term carried by the rejected candidacy", target)

		// ...but the vote slot for that SAME term must remain free: a
		// legitimate voter candidate (n3, distinct from target) must still be
		// able to collect it.
		resp2 := c.manager(target).HandleVote(context.Background(), warden.VoteRequest{Term: higherTerm, CandidateID: "n3"})
		Expect(resp2.Granted).To(BeTrue(), "voter %s refused legitimate candidate n3 after a non-voter's candidacy was (correctly) rejected", target)
	})

	// TestFlappingObserverNeverAdmitted (scenario 4): a node that appears and
	// disappears faster than JoinStability is never admitted and causes no churn.
	It("never admits a node that flaps faster than JoinStability", func() {
		tim := defaultTimings()
		c := newDiscoveryCluster(GinkgoT(), tim, time.Second /*join*/, 0, "n1", "n2", "n3")
		leader := c.electLeader("n1", "n2", "n3")
		c.addObserver("d", rosterNodes("n1", "n2", "n3"))

		flap := c.joinStability / 4 // present/absent phases each < JoinStability
		for i := 0; i < 12; i++ {
			c.pushRoster(nil, "n1", "n2", "n3", "d") // d present
			c.Advance(flap)
			c.pushRoster(nil, "n1", "n2", "n3") // d gone
			c.Advance(flap)

			Expect(c.membershipVersion(leader)).To(Equal(uint64(1)), "membership churned during flapping at iter %d", i)
			Expect(contains(c.voterIDs(leader), "d")).To(BeFalse(), "flapping node d was admitted at iter %d", i)
		}
	})

	// TestPartitionedMinorityCannotShrink (scenario 5), THE safety test. A
	// partitioned minority ex-leader must NEVER remove a voter; the majority
	// elects a new leader and MAY remove genuinely dead + roster-absent nodes
	// one at a time; on heal the ex-minority adopts the newer (smaller)
	// membership and demotes itself to observer.
	It("never lets a partitioned minority shrink quorum; majority removes safely; heal demotes ex-minority", func() {
		tim := defaultTimings()
		remove := 2 * tim.Dead
		c := newDiscoveryCluster(GinkgoT(), tim, time.Second /*join*/, remove, "a", "b", "c", "d", "e")
		c.pushRoster(nil, "a", "b", "c", "d", "e")
		oldLeader := c.electLeader("a", "b", "c", "d", "e")

		follower := c.others(oldLeader)[0]
		minority := []warden.NodeID{oldLeader, follower}
		majority := c.others(oldLeader, follower) // the other three
		c.partition(minority, majority)

		// Each side's roster OMITS the other side: to each side the other is
		// both StatusDead AND roster-absent — the strongest bait for a wrongful
		// removal.
		c.pushRoster(minority, minority...)
		c.pushRoster(majority, majority...)

		newLeader := c.electWithin(majority...)
		Expect(newLeader).NotTo(Equal(oldLeader), "new leader must be on the majority side")
		Expect(contains(minority, newLeader)).To(BeFalse(), "new leader %q must be on the majority side %v", newLeader, majority)

		// Advance far beyond RemoveAfter.
		c.Advance(remove + 10*tim.Dead)

		// SAFETY: the isolated minority ex-leader removed NOBODY and kept 5
		// voters, even though from its view the majority is dead + roster-absent
		// past RemoveAfter (only the live-majority gate stops it).
		Expect(c.membershipVersion(oldLeader)).To(Equal(uint64(1)), "isolated minority ex-leader must never shrink membership")
		Expect(c.voterIDs(oldLeader)).To(HaveLen(5), "isolated minority ex-leader must keep 5 voters")
		for _, id := range minority {
			if id != oldLeader {
				Expect(c.role(id)).NotTo(Equal(warden.RoleLeader), "minority node %s became a leader; the minority must stay leaderless", id)
			}
		}

		// The well-connected majority leader legitimately removed BOTH dead +
		// roster-absent minority voters, one at a time (Version 1 -> 2 -> 3).
		Expect(c.membershipVersion(newLeader)).To(Equal(uint64(3)), "majority leader should perform two single-node removals")
		majWant := sortedIDs(majority)
		Expect(c.voterIDs(newLeader)).To(Equal(majWant), "majority voters")

		// Heal: the ex-minority nodes must adopt the newer (smaller) membership
		// and, being removed, demote themselves to observers.
		c.heal()
		c.pushRoster(nil, "a", "b", "c", "d", "e")
		c.Advance(8 * tim.Heartbeat)

		for _, id := range minority {
			Expect(c.voterIDs(id)).To(Equal(majWant), "healed ex-minority %s must adopt newer membership", id)
			Expect(c.memberKind(id, id)).To(Equal(warden.MemberObserver), "healed ex-minority %s should be an observer", id)
		}
	})

	// TestRestartResumesPersistedMembership (scenario 6): after admitting a 4th
	// voter, a node that restarts from its store boots with the persisted
	// 4-voter membership, not the 3-node config seed.
	It("resumes the persisted 4-voter membership after a restart", func() {
		tim := defaultTimings()
		c := newDiscoveryCluster(GinkgoT(), tim, time.Second, 0, "n1", "n2", "n3")
		c.electLeader("n1", "n2", "n3")
		c.addObserver("d", rosterNodes("n1", "n2", "n3"))
		c.pushRoster(nil, "n1", "n2", "n3", "d")
		c.Advance(c.joinStability + 10*tim.Heartbeat)

		want := []warden.NodeID{"d", "n1", "n2", "n3"}
		Expect(c.voterIDs("n2")).To(Equal(want), "precondition: n2 voters")

		c.kill("n2")
		c.restart("n2")

		// Immediately after restart (no heartbeat yet) n2 must already hold the
		// persisted 4-voter membership rather than its 3-node config seed.
		Expect(c.voterIDs("n2")).To(Equal(want), "restarted n2 should boot with persisted voters")
		Expect(c.membershipVersion("n2")).To(Equal(uint64(2)), "restarted n2 should boot with persisted version 2")
	})

	// TestRemovalDisabledByDefault (scenario 7): with RemoveAfter == 0 a dead,
	// roster-absent voter is never removed.
	It("never removes a dead, roster-absent voter when RemoveAfter is disabled", func() {
		tim := defaultTimings()
		c := newDiscoveryCluster(GinkgoT(), tim, time.Second, 0 /*RemoveAfter disabled*/, "n1", "n2", "n3")
		leader := c.electLeader("n1", "n2", "n3")
		c.pushRoster(nil, "n1", "n2", "n3")

		victim := c.others(leader)[0]
		c.kill(victim)
		// Survivors' roster drops the victim: it is now dead AND roster-absent.
		c.pushRoster(c.others(victim), c.others(victim)...)
		c.Advance(20 * tim.Dead)

		Expect(contains(c.voterIDs(leader), victim)).To(BeTrue(), "victim %s removed despite RemoveAfter=0", victim)
		Expect(c.membershipVersion(leader)).To(Equal(uint64(1)), "membership changed with removal disabled")
	})

	// TestNoRemovalBeforeAnyRealRoster: a leader that has NEVER received a
	// discovery roster snapshot must never remove a voter, even one that is
	// genuinely dead by heartbeat liveness. lastRoster's zero value (Nodes ==
	// nil) is bit-for-bit indistinguishable from a real, explicitly-empty
	// roster, so without an explicit "have we ever heard from discovery at
	// all" gate, mere discovery silence since boot (tailscaled not yet up, or
	// the roster file not yet read) would make every voter look
	// roster-absent — turning discovery silence into automatic quorum shrink
	// once RemoveAfter elapses. This is the startup-window instance of the
	// invariant the README's scenario 4 documents for steady-state discovery
	// failure ("Discovery failure therefore never removes anyone").
	It("never removes a dead voter before discovery has delivered any real roster snapshot", func() {
		tim := defaultTimings()
		remove := 2 * tim.Dead
		c := newDiscoveryCluster(GinkgoT(), tim, time.Second, remove, "n1", "n2", "n3")
		leader := c.electLeader("n1", "n2", "n3")

		// Deliberately never call c.pushRoster: this leader's discovery source
		// has not delivered a single snapshot since boot.
		victim := c.others(leader)[0]
		c.kill(victim)
		c.Advance(remove + 10*tim.Dead)

		Expect(contains(c.voterIDs(leader), victim)).To(BeTrue(),
			"leader removed voter %s despite never having observed a real discovery roster", victim)
		Expect(c.membershipVersion(leader)).To(Equal(uint64(1)),
			"membership changed before the leader ever saw a real roster snapshot")
	})

	// TestAdoptionRemovesSelfDemotesToObserver (scenario 8): the leader removes
	// a dead + absent voter X; X restarts (still believing it is a voter) and,
	// on the first heartbeat carrying the newer membership, demotes itself to
	// observer.
	It("has a removed voter demote itself to observer on adopting the newer membership", func() {
		tim := defaultTimings()
		remove := 2 * tim.Dead
		c := newDiscoveryCluster(GinkgoT(), tim, time.Second, remove, "a", "b", "c", "d", "e")
		c.pushRoster(nil, "a", "b", "c", "d", "e")
		leader := c.electLeader("a", "b", "c", "d", "e")

		victim := c.others(leader)[0]
		survivors := c.others(victim)
		c.kill(victim)
		c.pushRoster(survivors, survivors...) // victim now dead + roster-absent

		c.Advance(remove + 10*tim.Dead)
		Expect(contains(c.voterIDs(leader), victim)).To(BeFalse(), "leader failed to remove dead+absent voter %s", victim)
		removedVersion := c.membershipVersion(leader)
		Expect(removedVersion).To(BeNumerically(">=", uint64(2)), "expected a removal (version >= 2)")

		// The victim restarts from its stale store, rejoins the roster, and
		// adopts the newer membership on the first heartbeat — demoting itself
		// to observer.
		c.restart(victim)
		c.pushRoster(nil, "a", "b", "c", "d", "e")
		c.Advance(6 * tim.Heartbeat)

		Expect(contains(c.voterIDs(victim), victim)).To(BeFalse(), "restarted victim still lists itself as a voter after adoption")
		Expect(c.memberKind(victim, victim)).To(Equal(warden.MemberObserver), "restarted victim should have demoted to observer")
		Expect(c.membershipVersion(victim)).To(BeNumerically(">=", removedVersion), "restarted victim did not adopt the newer membership")
	})

	// TestDiscoveryNoGoroutineLeak: discovery-mode admissions leave no in-flight
	// RPC workers once settled, and identify-probe goroutines all join on
	// shutdown (goroutine count returns to baseline).
	It("leaks no RPC workers or goroutines through a discovery-mode admission", func() {
		before := runtime.NumGoroutine()

		func() {
			tim := defaultTimings()
			c := newDiscoveryCluster(GinkgoT(), tim, time.Second, 2*tim.Dead, "n1", "n2", "n3")
			c.electLeader("n1", "n2", "n3")
			c.addObserver("d", rosterNodes("n1", "n2", "n3"))
			c.pushRoster(nil, "n1", "n2", "n3", "d")
			c.Advance(c.joinStability + 10*tim.Heartbeat)

			for _, m := range c.aliveManagers() {
				Expect(m.rpc.count()).To(Equal(0), "manager %s has in-flight RPC workers while settled", m.self.ID)
			}
			c.stopAll()
			for _, n := range c.byID {
				Expect(n.m.rpc.count()).To(Equal(0), "manager %s has in-flight RPC workers after shutdown", n.m.self.ID)
			}
		}()

		leaked := true
		for i := 0; i < 200; i++ {
			if runtime.NumGoroutine() <= before {
				leaked = false
				break
			}
			runtime.Gosched()
		}
		Expect(leaked).To(BeFalse(), "goroutine leak: before=%d after=%d", before, runtime.NumGoroutine())
	})
})
