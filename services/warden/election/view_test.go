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

var _ = Describe("cluster view propagation", func() {
	It("ages only the configured leader from startup while preserving zero LastSeen", func() {
		clk := testclock.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
		cfg := Config{
			Self:               warden.Node{ID: "a", Addr: "a"},
			Peers:              []warden.Node{{ID: "a", Addr: "a"}, {ID: "b", Addr: "b"}, {ID: "c", Addr: "c"}},
			LeaderID:           "b",
			HeartbeatInterval:  100 * time.Millisecond,
			ElectionTimeoutMin: 300 * time.Millisecond,
			ElectionTimeoutMax: 600 * time.Millisecond,
			SuspectAfter:       time.Second,
			DeadAfter:          3 * time.Second,
			RPCTimeout:         50 * time.Millisecond,
		}
		m, err := NewManager(cfg, stubTransport(), store.NewMemStore(), clk)
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- m.Run(ctx) }()
		defer func() { cancel(); Expect(<-done).To(Succeed()) }()
		clk.BlockUntilTimers(3)

		clk.Advance(4 * time.Second)
		v := m.View()
		Expect(v.Source).To(Equal(warden.NodeID("a")))
		Expect(v.Authoritative).To(BeFalse())
		Expect(v.Role).To(Equal(warden.RoleFollower))
		Expect(v.ElectionsStarted).To(BeZero())
		for _, peer := range v.Peers {
			switch peer.Node.ID {
			case "b":
				Expect(peer.Status).To(Equal(warden.StatusDead))
				Expect(peer.LastSeen).To(BeZero())
			case "c":
				Expect(peer.Status).To(Equal(warden.StatusUnknown))
				Expect(peer.LastSeen).To(BeZero())
			}
		}
	})

	// TestFollowersReceiveAuthoritativeView: in a running cluster, every
	// follower's view is the leader's authoritative view adapted to the
	// follower's own identity.
	It("gives every follower the leader's authoritative view adapted to its identity", func() {
		c := newCluster(GinkgoT(), "n1", "n2", "n3")
		leader := c.electLeader()
		c.Advance(c.timings.Heartbeat * 2)

		for _, id := range c.others(leader) {
			v := c.view(id)
			Expect(v.Authoritative).To(BeTrue(), "follower %s view should be authoritative", id)
			Expect(v.Source).To(Equal(leader), "follower %s view source should be the leader", id)
			Expect(v.LeaderID).To(Equal(leader), "follower %s view leader mismatch", id)
			Expect(v.Self).To(Equal(id), "follower %s view self mismatch", id)
			Expect(v.Role).To(Equal(warden.RoleFollower), "follower %s view role", id)
			// The adapted view must list every cluster member.
			Expect(v.Peers).To(HaveLen(len(c.ids)), "follower %s view peer count", id)
		}
	})

	// TestFollowerViewFallsBackAfterFreshness drives a single follower directly
	// to isolate the ViewFreshFor boundary: while heartbeats keep arriving (so
	// the node stays a follower) but stop carrying a view, the cached leader
	// view goes stale after ViewFreshFor and the node returns a local,
	// non-authoritative view.
	It("falls back to a local non-authoritative view after ViewFreshFor", func() {
		s := newSolo(GinkgoT(), store.NewMemStore())
		ctx := context.Background()

		leaderView := warden.ClusterView{
			Self:          "b",
			Role:          warden.RoleLeader,
			Term:          1,
			LeaderID:      "b",
			Source:        "b",
			Authoritative: true,
			UpdatedAt:     s.clock.Now(),
			Peers: []warden.PeerView{
				{Node: warden.Node{ID: "a", Addr: "a"}, Status: warden.StatusAlive},
				{Node: warden.Node{ID: "b", Addr: "b"}, Status: warden.StatusAlive},
				{Node: warden.Node{ID: "c", Addr: "c"}, Status: warden.StatusAlive},
			},
		}

		// A heartbeat carrying the leader's view makes our view authoritative.
		s.m.HandleHeartbeat(heartbeatContext(ctx, "b"), warden.HeartbeatRequest{Term: 1, LeaderID: "b", View: &leaderView})
		v := s.m.View()
		Expect(v.Authoritative).To(BeTrue(), "after view-bearing heartbeat, view should be authoritative: %+v", v)
		Expect(v.Source).To(Equal(warden.NodeID("b")))
		Expect(v.LeaderID).To(Equal(warden.NodeID("b")))
		Expect(v.Self).To(Equal(warden.NodeID("a")))
		Expect(v.Role).To(Equal(warden.RoleFollower))

		// Keep the node a follower with heartbeats that carry NO view,
		// advancing past ViewFreshFor. The cache must go stale.
		freshDeadline := s.clock.Now().Add(s.m.cfg.ViewFreshFor)
		step := s.m.cfg.ElectionTimeoutMin / 2
		for s.clock.Now().Before(freshDeadline.Add(step)) {
			s.clock.Advance(step)
			s.m.HandleHeartbeat(heartbeatContext(ctx, "b"), warden.HeartbeatRequest{Term: 1, LeaderID: "b", View: nil})
		}

		v = s.m.View()
		Expect(v.Authoritative).To(BeFalse(), "after ViewFreshFor without a fresh view, view must NOT be authoritative: %+v", v)
		Expect(v.Source).To(Equal(warden.NodeID("a")), "stale fallback view source should be self a")
		Expect(v.LeaderID).To(Equal(warden.NodeID("b")), "fallback view should still name leader b")
		Expect(v.Role).To(Equal(warden.RoleFollower), "node should still be a follower")
		// In the local fallback, the leader is alive (heard from very recently)
		// and self is alive.
		for _, p := range v.Peers {
			switch p.Node.ID {
			case "b":
				Expect(p.Status).To(Equal(warden.StatusAlive), "fallback: leader b should be alive")
			case "a":
				Expect(p.Status).To(Equal(warden.StatusAlive), "fallback: self a should be alive")
			case "c":
				Expect(p.Status).To(Equal(warden.StatusUnknown), "fallback: non-leader peer c should be unknown")
			}
		}
	})
})
