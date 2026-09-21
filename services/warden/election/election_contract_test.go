package election_test

// Contract tests for the election state machine's SAFETY properties, exercised
// only through the exported surface (NewManager, Run, HandleVote,
// HandleHeartbeat, View) plus the deterministic testclock and MemStore. These
// freeze the Raft-style guarantees warden's split-brain-freedom rests on:
//   - a node persists (term, vote) BEFORE granting, so a restart never
//     double-votes in a term;
//   - terms never regress; a stale term is rejected with the newer term;
//   - a candidate becomes leader only on a majority quorum;
//   - the randomized election timeout stays within [min, max).

import (
	"context"
	"sync"
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/election"
	"github.com/candacelabs/csf/services/warden/store"
	"github.com/candacelabs/csf/services/warden/testclock"
)

func TestElectionContract(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "election contract suite")
}

var clockStart = time.Date(2026, 7, 21, 12, 0, 0, 0, time.UTC)

// fakeTransport is a warden.ITransport whose vote/heartbeat outcomes are
// programmable per peer. grant lists the peer IDs that return Granted=true;
// every other peer returns an error (unreachable), which the manager treats as
// a vote not granted.
type fakeTransport struct {
	mu    sync.Mutex
	grant map[warden.NodeID]bool
	term  warden.Term // term the granting peers echo back
}

func newFakeTransport(grant ...warden.NodeID) *fakeTransport {
	g := map[warden.NodeID]bool{}
	for _, id := range grant {
		g[id] = true
	}
	return &fakeTransport{grant: g}
}

func (t *fakeTransport) RequestVote(ctx context.Context, peer warden.Node, req warden.VoteRequest) (warden.VoteResponse, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.grant[peer.ID] {
		return warden.VoteResponse{Term: req.Term, Granted: true, VoterID: peer.ID}, nil
	}
	return warden.VoteResponse{}, context.DeadlineExceeded // unreachable => not granted
}

func (t *fakeTransport) SendHeartbeat(ctx context.Context, peer warden.Node, req warden.HeartbeatRequest) (warden.HeartbeatResponse, error) {
	return warden.HeartbeatResponse{}, context.DeadlineExceeded
}

func (t *fakeTransport) Identify(ctx context.Context, peer warden.Node) (warden.IdentifyResponse, error) {
	return warden.IdentifyResponse{}, context.DeadlineExceeded
}

func nodes(ids ...string) []warden.Node {
	var out []warden.Node
	for _, id := range ids {
		out = append(out, warden.Node{ID: warden.NodeID(id), Addr: "127.0.0.1:0"})
	}
	return out
}

// runManager starts m.Run in a goroutine and returns a stop function that
// cancels it and blocks until Run has fully returned (so a subsequent Manager
// can safely reuse the same store to model a restart).
func runManager(m *election.Manager) (stop func()) {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = m.Run(ctx)
	}()
	return func() {
		cancel()
		<-done
	}
}

func newManager(self string, peers []warden.Node, tr warden.ITransport, st warden.IStore, clock warden.IClock) *election.Manager {
	m, err := election.NewManager(election.Config{
		Self:               warden.Node{ID: warden.NodeID(self), Addr: "127.0.0.1:0"},
		Peers:              peers,
		HeartbeatInterval:  10 * time.Second, // large so its ticker never confounds timing specs
		ElectionTimeoutMin: 2 * time.Second,
		ElectionTimeoutMax: 4 * time.Second,
		RPCTimeout:         500 * time.Millisecond,
	}, tr, st, clock)
	Expect(err).NotTo(HaveOccurred())
	return m
}

var _ = Describe("NewManager construction validation", func() {
	valid := nodes("a", "b", "c")
	tr := newFakeTransport()
	st := store.NewMemStore()
	ck := testclock.New(clockStart)

	DescribeTable("rejects structurally invalid configuration",
		func(mutate func(cfg *election.Config), wantErr error) {
			cfg := election.Config{Self: warden.Node{ID: "a", Addr: "h:1"}, Peers: valid}
			mutate(&cfg)
			_, err := election.NewManager(cfg, tr, st, ck)
			Expect(err).To(MatchError(wantErr))
		},
		Entry("empty Self.ID", func(c *election.Config) { c.Self = warden.Node{} }, election.ErrNoSelf),
		Entry("empty Peers", func(c *election.Config) { c.Peers = nil }, election.ErrNoPeers),
		Entry("Self not in Peers", func(c *election.Config) { c.Self = warden.Node{ID: "ghost"} }, election.ErrSelfMissing),
		Entry("duplicate peer id", func(c *election.Config) {
			c.Peers = []warden.Node{{ID: "a", Addr: "h:1"}, {ID: "a", Addr: "h:2"}}
		}, election.ErrDuplicate),
	)

	It("rejects nil transport/store/clock", func() {
		cfg := election.Config{Self: warden.Node{ID: "a", Addr: "h:1"}, Peers: valid}
		_, err := election.NewManager(cfg, nil, st, ck)
		Expect(err).To(MatchError(election.ErrNoTransport))
		_, err = election.NewManager(cfg, tr, nil, ck)
		Expect(err).To(MatchError(election.ErrNoStore))
		_, err = election.NewManager(cfg, tr, st, nil)
		Expect(err).To(MatchError(election.ErrNoClock))
	})
})

var _ = Describe("Vote safety", func() {
	var (
		st    *store.MemStore
		clock *testclock.Clock
		m     *election.Manager
		stop  func()
	)

	BeforeEach(func() {
		st = store.NewMemStore()
		clock = testclock.New(clockStart)
		m = newManager("a", nodes("a", "b", "c"), newFakeTransport(), st, clock)
		stop = runManager(m)
	})
	AfterEach(func() { stop() })

	It("persists (term, vote) BEFORE the grant is returned", func() {
		resp := m.HandleVote(context.Background(), warden.VoteRequest{Term: 1, CandidateID: "b"})
		Expect(resp.Granted).To(BeTrue())
		Expect(resp.Term).To(Equal(warden.Term(1)))
		Expect(resp.VoterID).To(Equal(warden.NodeID("a")))

		// The grant response is proof the Save already happened: the store must
		// already hold the vote by the time HandleVote returned.
		ps, ok, err := st.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(ps).To(Equal(warden.PersistentState{CurrentTerm: 1, VotedFor: "b"}))
	})

	It("grants at most one distinct candidate per term (no double-vote)", func() {
		Expect(m.HandleVote(context.Background(), warden.VoteRequest{Term: 5, CandidateID: "b"}).Granted).To(BeTrue())
		// Same term, different candidate: must be refused.
		second := m.HandleVote(context.Background(), warden.VoteRequest{Term: 5, CandidateID: "c"})
		Expect(second.Granted).To(BeFalse())
		Expect(second.Term).To(Equal(warden.Term(5)))
		// Same term, same candidate: idempotently granted.
		Expect(m.HandleVote(context.Background(), warden.VoteRequest{Term: 5, CandidateID: "b"}).Granted).To(BeTrue())
	})

	It("adopts a higher term and re-opens the vote", func() {
		Expect(m.HandleVote(context.Background(), warden.VoteRequest{Term: 5, CandidateID: "b"}).Granted).To(BeTrue())
		Expect(m.HandleVote(context.Background(), warden.VoteRequest{Term: 6, CandidateID: "c"}).Granted).To(BeTrue())
		ps, _, _ := st.Load()
		Expect(ps).To(Equal(warden.PersistentState{CurrentTerm: 6, VotedFor: "c"}))
	})

	It("rejects a vote request for a stale term, echoing the current term", func() {
		// Adopt term 10 via a heartbeat.
		Expect(m.HandleHeartbeat(context.Background(), warden.HeartbeatRequest{Term: 10, LeaderID: "b"}).OK).To(BeTrue())
		resp := m.HandleVote(context.Background(), warden.VoteRequest{Term: 5, CandidateID: "c"})
		Expect(resp.Granted).To(BeFalse())
		Expect(resp.Term).To(Equal(warden.Term(10)))
	})
})

var _ = Describe("Term monotonicity via heartbeats", func() {
	var (
		st    *store.MemStore
		clock *testclock.Clock
		m     *election.Manager
		stop  func()
	)

	BeforeEach(func() {
		st = store.NewMemStore()
		clock = testclock.New(clockStart)
		m = newManager("a", nodes("a", "b", "c"), newFakeTransport(), st, clock)
		stop = runManager(m)
	})
	AfterEach(func() { stop() })

	It("accepts a heartbeat with a higher term and records the leader", func() {
		resp := m.HandleHeartbeat(context.Background(), warden.HeartbeatRequest{Term: 3, LeaderID: "b"})
		Expect(resp.OK).To(BeTrue())
		Expect(resp.Term).To(Equal(warden.Term(3)))
		v := m.View()
		Expect(v.LeaderID).To(Equal(warden.NodeID("b")))
		Expect(v.Term).To(Equal(warden.Term(3)))
		Expect(v.Role).To(Equal(warden.RoleFollower))
	})

	It("rejects a stale heartbeat with OK=false and the newer term", func() {
		Expect(m.HandleHeartbeat(context.Background(), warden.HeartbeatRequest{Term: 10, LeaderID: "b"}).OK).To(BeTrue())
		resp := m.HandleHeartbeat(context.Background(), warden.HeartbeatRequest{Term: 4, LeaderID: "c"})
		Expect(resp.OK).To(BeFalse())
		Expect(resp.Term).To(Equal(warden.Term(10)))
	})

	It("surfaces the leader's authoritative view to a follower via the heartbeat", func() {
		leaderView := &warden.ClusterView{
			Self: "b", Role: warden.RoleLeader, Term: 3, LeaderID: "b", Source: "b",
			Authoritative: true, UpdatedAt: clockStart,
			Peers: []warden.PeerView{
				{Node: warden.Node{ID: "a", Addr: "127.0.0.1:0"}, Status: warden.StatusAlive},
				{Node: warden.Node{ID: "b", Addr: "127.0.0.1:0"}, Status: warden.StatusAlive},
				{Node: warden.Node{ID: "c", Addr: "127.0.0.1:0"}, Status: warden.StatusAlive},
			},
		}
		Expect(m.HandleHeartbeat(context.Background(), warden.HeartbeatRequest{Term: 3, LeaderID: "b", View: leaderView}).OK).To(BeTrue())
		v := m.View()
		// The follower re-badges the cached leader view as its own identity but
		// keeps it marked authoritative and leader-sourced.
		Expect(v.Self).To(Equal(warden.NodeID("a")))
		Expect(v.Authoritative).To(BeTrue())
		Expect(v.Source).To(Equal(warden.NodeID("b")))
	})
})

var _ = Describe("Restart safety (the crown jewel)", func() {
	It("a node restarted on the same store never double-votes in a term", func() {
		st := store.NewMemStore()
		clock := testclock.New(clockStart)

		// First incarnation votes for b in term 5.
		m1 := newManager("a", nodes("a", "b", "c"), newFakeTransport(), st, clock)
		stop1 := runManager(m1)
		Expect(m1.HandleVote(context.Background(), warden.VoteRequest{Term: 5, CandidateID: "b"}).Granted).To(BeTrue())
		stop1() // fully stop before "restarting"

		// Second incarnation on the SAME store models a crash+restart.
		m2 := newManager("a", nodes("a", "b", "c"), newFakeTransport(), st, clock)
		stop2 := runManager(m2)
		defer stop2()

		// The persisted vote for b in term 5 must survive: c cannot be granted.
		Expect(m2.HandleVote(context.Background(), warden.VoteRequest{Term: 5, CandidateID: "c"}).Granted).To(BeFalse())
		// But re-affirming the same candidate is safe.
		Expect(m2.HandleVote(context.Background(), warden.VoteRequest{Term: 5, CandidateID: "b"}).Granted).To(BeTrue())
	})

	It("resumes from the persisted term after a restart", func() {
		st := store.NewMemStore()
		Expect(st.Save(warden.PersistentState{CurrentTerm: 42, VotedFor: "b"})).To(Succeed())
		clock := testclock.New(clockStart)
		m := newManager("a", nodes("a", "b", "c"), newFakeTransport(), st, clock)
		stop := runManager(m)
		defer stop()
		Expect(m.View().Term).To(Equal(warden.Term(42)))
		// A vote request below the resumed term is rejected.
		Expect(m.HandleVote(context.Background(), warden.VoteRequest{Term: 10, CandidateID: "c"}).Granted).To(BeFalse())
	})
})

var _ = Describe("Quorum-gated leadership", func() {
	// Drives a real election under the testclock and asserts the majority
	// threshold: a candidate becomes leader only once it has a quorum of votes
	// (its own plus grants), never fewer.
	triggerElection := func(clock *testclock.Clock, m *election.Manager) {
		clock.BlockUntilTimers(3) // election timer + heartbeat ticker + publish ticker
		// Advance past election_timeout_max so the (randomized) election timer,
		// whatever value it drew in [2s,4s), is certainly due.
		clock.Advance(5 * time.Second)
	}

	It("n=3: becomes leader with one peer grant (self+1 = quorum 2)", func() {
		st := store.NewMemStore()
		clock := testclock.New(clockStart)
		m := newManager("a", nodes("a", "b", "c"), newFakeTransport("b"), st, clock)
		stop := runManager(m)
		defer stop()
		triggerElection(clock, m)
		Eventually(func() warden.Role { return m.View().Role }, "2s", "10ms").
			Should(Equal(warden.RoleLeader))
	})

	It("n=5: stays candidate with only one grant (self+1 = 2 < quorum 3)", func() {
		st := store.NewMemStore()
		clock := testclock.New(clockStart)
		m := newManager("a", nodes("a", "b", "c", "d", "e"), newFakeTransport("b"), st, clock)
		stop := runManager(m)
		defer stop()
		triggerElection(clock, m)
		// Reaches candidate but never leader (2 < 3).
		Eventually(func() warden.Role { return m.View().Role }, "2s", "10ms").
			Should(Equal(warden.RoleCandidate))
		Consistently(func() warden.Role { return m.View().Role }, "300ms", "20ms").
			Should(Equal(warden.RoleCandidate))
	})

	It("n=5: becomes leader with two peer grants (self+2 = quorum 3)", func() {
		st := store.NewMemStore()
		clock := testclock.New(clockStart)
		m := newManager("a", nodes("a", "b", "c", "d", "e"), newFakeTransport("b", "c"), st, clock)
		stop := runManager(m)
		defer stop()
		triggerElection(clock, m)
		Eventually(func() warden.Role { return m.View().Role }, "2s", "10ms").
			Should(Equal(warden.RoleLeader))
	})

	It("n=4 (the real fleet size, even split): becomes leader with two grants (self+2 = quorum 3)", func() {
		st := store.NewMemStore()
		clock := testclock.New(clockStart)
		m := newManager("a", nodes("a", "b", "c", "d"), newFakeTransport("b", "c"), st, clock)
		stop := runManager(m)
		defer stop()
		triggerElection(clock, m)
		Eventually(func() warden.Role { return m.View().Role }, "2s", "10ms").
			Should(Equal(warden.RoleLeader))
	})

	It("n=4: stays candidate with only one grant (self+1 = 2 < quorum 3, no even-split tie win)", func() {
		st := store.NewMemStore()
		clock := testclock.New(clockStart)
		m := newManager("a", nodes("a", "b", "c", "d"), newFakeTransport("b"), st, clock)
		stop := runManager(m)
		defer stop()
		triggerElection(clock, m)
		Eventually(func() warden.Role { return m.View().Role }, "2s", "10ms").
			Should(Equal(warden.RoleCandidate))
		Consistently(func() warden.Role { return m.View().Role }, "300ms", "20ms").
			Should(Equal(warden.RoleCandidate))
	})
})

var _ = Describe("Randomized election timeout bounds", func() {
	// For each node identity the manager draws ONE timeout in [min, max) at
	// startup. We prove the bound per instance: at just under min no election
	// has started (lower bound), and after max one has (upper bound). Several
	// node ids sample different draws from the range.
	DescribeTable("first election fires within [election_timeout_min, election_timeout_max)",
		func(nodeID string) {
			st := store.NewMemStore()
			clock := testclock.New(clockStart)
			// A 3-node cluster with an unreachable transport keeps the node a
			// candidate (never leader), so ElectionsStarted is the clean signal.
			m := newManager(nodeID, nodes(nodeID, "x1", "x2"), newFakeTransport(), st, clock)
			stop := runManager(m)
			defer stop()

			clock.BlockUntilTimers(3)

			// Lower bound: at min-1ns no timer is due, so no election can have
			// started. Time does not advance during polling, so this cannot flake.
			clock.Advance(2*time.Second - time.Nanosecond)
			Consistently(func() warden.Term { return m.View().Term }, "150ms", "15ms").
				Should(Equal(warden.Term(0)), "election started before election_timeout_min")

			// Upper bound: by max the drawn timeout (< max) is certainly due.
			clock.Advance(2 * time.Second) // total elapsed now 4s (== max)
			Eventually(func() warden.Term { return m.View().Term }, "2s", "10ms").
				Should(BeNumerically(">=", 1), "election did not start by election_timeout_max")
		},
		Entry("node a", "a"),
		Entry("node node-c", "node-c"),
		Entry("node node-d", "node-d"),
		Entry("node n3", "n3"),
		Entry("node zeta", "zeta"),
	)
})
