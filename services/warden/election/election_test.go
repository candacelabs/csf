package election

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
	"github.com/candacelabs/csf/services/warden/store"
	"github.com/candacelabs/csf/services/warden/testclock"
)

// solo builds and runs a single Manager with three peers against a stub
// transport and the given store. Because the returned clock is never advanced
// unless the test does so, the node never starts its own election and stays a
// follower at its loaded term — ideal for exercising the RPC handlers in
// isolation.
type solo struct {
	m       *Manager
	clock   *testclock.Clock
	cancel  context.CancelFunc
	runDone chan struct{}
}

func newSolo(t iHarnessT, st warden.IStore) *solo {
	t.Helper()
	clk := testclock.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	peers := []warden.Node{{ID: "a", Addr: "a"}, {ID: "b", Addr: "b"}, {ID: "c", Addr: "c"}}
	cfg := Config{
		Self:               warden.Node{ID: "a", Addr: "a"},
		Peers:              peers,
		HeartbeatInterval:  100 * time.Millisecond,
		ElectionTimeoutMin: 300 * time.Millisecond,
		ElectionTimeoutMax: 600 * time.Millisecond,
		SuspectAfter:       time.Second,
		DeadAfter:          3 * time.Second,
		RPCTimeout:         50 * time.Millisecond,
	}
	m, err := NewManager(cfg, stubTransport(), st, clk)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	runDone := make(chan struct{})
	go func() { _ = m.Run(ctx); close(runDone) }()
	s := &solo{m: m, clock: clk, cancel: cancel, runDone: runDone}
	t.Cleanup(func() { cancel(); <-runDone })
	return s
}

var _ = Describe("election safety", func() {
	// TestThreeNodeElectsSingleLeader
	It("a three-node cluster elects a single agreed leader", func() {
		c := newCluster(GinkgoT(), "n1", "n2", "n3")
		leader := c.electLeader()

		Expect(c.leaders()).To(HaveLen(1), "expected exactly one leader")
		for _, id := range c.ids {
			v := c.view(id)
			if id == leader {
				Expect(v.Role).To(Equal(warden.RoleLeader), "%s should be leader", id)
				continue
			}
			Expect(v.Role).To(Equal(warden.RoleFollower), "%s should be follower", id)
			Expect(v.LeaderID).To(Equal(leader), "%s should follow leader %q", id, leader)
		}
	})

	// TestVoteSafetyOneVotePerTerm
	It("grants at most one vote per term and persists it", func() {
		st := store.NewMemStore()
		s := newSolo(GinkgoT(), st)

		// First candidate in term 5 gets the vote.
		resp := s.m.HandleVote(context.Background(), warden.VoteRequest{Term: 5, CandidateID: "b"})
		Expect(resp.Granted).To(BeTrue(), "first vote should be granted at term 5")
		Expect(resp.Term).To(Equal(warden.Term(5)))

		// A different candidate in the SAME term is refused.
		resp = s.m.HandleVote(context.Background(), warden.VoteRequest{Term: 5, CandidateID: "c"})
		Expect(resp.Granted).To(BeFalse(), "second candidate in term 5 should be refused")

		// The same candidate asking again is still granted (idempotent).
		resp = s.m.HandleVote(context.Background(), warden.VoteRequest{Term: 5, CandidateID: "b"})
		Expect(resp.Granted).To(BeTrue(), "same candidate re-asking in term 5 should be granted")

		// The vote must be persisted.
		ps, ok, err := st.Load()
		Expect(err).NotTo(HaveOccurred())
		Expect(ok).To(BeTrue())
		Expect(ps.CurrentTerm).To(Equal(warden.Term(5)))
		Expect(ps.VotedFor).To(Equal(warden.NodeID("b")))
	})

	// TestVoteSafetyPersistsAcrossRestart
	It("remembers its vote across a process restart", func() {
		st := store.NewMemStore()

		s1 := newSolo(GinkgoT(), st)
		resp := s1.m.HandleVote(context.Background(), warden.VoteRequest{Term: 7, CandidateID: "b"})
		Expect(resp.Granted).To(BeTrue(), "term 7 vote for b should be granted")

		// Simulate a process restart: stop the first Manager, build a new one
		// on the SAME store.
		s1.cancel()
		<-s1.runDone

		s2 := newSolo(GinkgoT(), st)
		// The restarted node must remember it voted for b in term 7 and refuse c.
		resp = s2.m.HandleVote(context.Background(), warden.VoteRequest{Term: 7, CandidateID: "c"})
		Expect(resp.Granted).To(BeFalse(), "after restart, term 7 vote for c must be refused (already voted b)")
		Expect(resp.Term).To(Equal(warden.Term(7)), "restarted node should report term 7")

		// And still honor b.
		resp = s2.m.HandleVote(context.Background(), warden.VoteRequest{Term: 7, CandidateID: "b"})
		Expect(resp.Granted).To(BeTrue(), "after restart, term 7 vote for b should still be granted")
	})
})
