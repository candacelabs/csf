package election

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
)

var _ = Describe("leader liveness", func() {
	// TestLeaderLivenessTransitions
	It("classifies a partitioned peer alive -> suspect -> dead and back on recovery", func() {
		c := newCluster(GinkgoT(), "n1", "n2", "n3")
		leader := c.electLeader()

		// Choose a follower and partition it away from everyone. The leader
		// keeps majority (itself + the other follower) and stays leader.
		victim := c.others(leader)[0]
		other := c.others(leader, victim)[0]
		c.partition([]warden.NodeID{victim}, c.others(victim))

		// Freeze the reference: the leader's last successful contact with victim.
		base := c.peerLastSeen(leader, victim)
		Expect(base.IsZero()).To(BeFalse(), "leader never contacted %s before partition", victim)
		// Sanity: still alive immediately after partition.
		Expect(c.peerStatus(leader, victim)).To(Equal(warden.StatusAlive), "right after partition %s should be alive", victim)

		// Just before SuspectAfter: still alive.
		c.advanceTo(base.Add(c.timings.Suspect - c.step()))
		Expect(c.peerStatus(leader, victim)).To(Equal(warden.StatusAlive), "before SuspectAfter %s should be alive", victim)
		// At SuspectAfter: suspect.
		c.advanceTo(base.Add(c.timings.Suspect))
		Expect(c.peerStatus(leader, victim)).To(Equal(warden.StatusSuspect), "at SuspectAfter %s should be suspect", victim)
		// Just before DeadAfter: still suspect.
		c.advanceTo(base.Add(c.timings.Dead - c.step()))
		Expect(c.peerStatus(leader, victim)).To(Equal(warden.StatusSuspect), "before DeadAfter %s should be suspect", victim)
		// At DeadAfter: dead.
		c.advanceTo(base.Add(c.timings.Dead))
		Expect(c.peerStatus(leader, victim)).To(Equal(warden.StatusDead), "at DeadAfter %s should be dead", victim)

		// The connected follower must have stayed a follower throughout.
		Expect(c.role(other)).To(Equal(warden.RoleFollower), "connected follower %s should have stayed follower", other)

		// Heal and let heartbeats resume: victim flips back to alive in
		// whichever leader's authoritative view now governs.
		c.heal()
		final := c.electLeader()
		c.Advance(c.timings.Heartbeat * 3)
		Expect(c.peerStatus(final, victim)).To(Equal(warden.StatusAlive), "after recovery %s should be alive in leader %s view", victim, final)
	})

	// TestNewLeaderMarksDownPeerDead
	It("marks an already-down, never-contacted peer dead within DeadAfter of takeover", func() {
		c := newCluster(GinkgoT(), "a", "b", "c", "d", "e")

		// "c" is down from the start.
		c.kill("c")

		// Elect a leader among the four survivors, then kill it to force a
		// fresh leader that never had contact with the already-dead "c".
		l1 := c.electLeader(c.others("c")...)
		c.kill(l1)

		l2 := c.electLeader(c.others("c", l1)...)
		t0 := c.clock.Now()

		// Immediately after taking over, the never-contacted peer is not yet Dead.
		Expect(c.peerStatus(l2, "c")).NotTo(Equal(warden.StatusDead), "new leader %s marked c dead too early", l2)

		// Within DeadAfter of taking over it must be Dead.
		c.advanceTo(t0.Add(c.timings.Dead))
		Expect(c.peerStatus(l2, "c")).To(Equal(warden.StatusDead), "new leader %s should mark c dead within DeadAfter", l2)
	})

	// TestLeaderReportsLatency
	It("records non-negative latency and a fresh LastSeen for live peers", func() {
		c := newCluster(GinkgoT(), "n1", "n2", "n3")
		leader := c.electLeader()
		c.Advance(c.timings.Heartbeat * 2)

		v := c.view(leader)
		Expect(v.Authoritative).To(BeTrue(), "leader view should be authoritative")
		Expect(v.Source).To(Equal(leader), "leader view should be sourced from itself")
		for _, p := range v.Peers {
			if p.Node.ID == leader {
				Expect(p.Status).To(Equal(warden.StatusAlive), "leader should see itself alive")
				continue
			}
			Expect(p.Status).To(Equal(warden.StatusAlive), "peer %s should be alive", p.Node.ID)
			Expect(p.LastSeen.IsZero()).To(BeFalse(), "peer %s should have a non-zero LastSeen", p.Node.ID)
			Expect(p.LatencyMS).To(BeNumerically(">=", 0), "peer %s latency should be non-negative", p.Node.ID)
		}
	})
})
