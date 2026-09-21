package election

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/warden"
)

func contains(ids []warden.NodeID, id warden.NodeID) bool {
	for _, x := range ids {
		if x == id {
			return true
		}
	}
	return false
}

var _ = Describe("cluster failover and partition", func() {
	// TestLeaderFailoverElectsNewLeader
	It("elects a new, higher-term leader after the leader is killed", func() {
		c := newCluster(GinkgoT(), "n1", "n2", "n3")
		leader := c.electLeader()
		oldTerm := c.term(leader)

		c.kill(leader)

		survivors := c.others(leader)
		newLeader := c.electLeader(survivors...)

		Expect(newLeader).NotTo(Equal(leader), "new leader must differ from killed leader %q", leader)
		newTerm := c.term(newLeader)
		Expect(newTerm).To(BeNumerically(">", oldTerm), "term must strictly increase across failover: old=%d new=%d", oldTerm, newTerm)
		// Both survivors must agree on the new leader and its term.
		for _, id := range survivors {
			v := c.view(id)
			Expect(v.LeaderID).To(Equal(newLeader), "survivor %s should follow %q", id, newLeader)
			Expect(v.Term).To(Equal(newTerm), "survivor %s should be at term %d", id, newTerm)
		}
	})

	// TestKilledFollowerRestartsAndRejoins
	It("lets a killed follower restart and rejoin without a term explosion", func() {
		c := newCluster(GinkgoT(), "n1", "n2", "n3")
		leader := c.electLeader()

		// Pick a follower to kill.
		victim := c.others(leader)[0]
		termBefore := c.term(leader)

		c.kill(victim)
		// The leader retains majority (2 of 3) and keeps leading.
		c.Advance(c.timings.ETMax * 2)
		Expect(c.role(leader)).To(Equal(warden.RoleLeader), "leader %s lost leadership after a follower died", leader)

		c.restart(victim)
		// Give the leader's heartbeats time to reach the restarted node.
		c.Advance(c.timings.ETMax * 2)

		Expect(c.role(victim)).To(Equal(warden.RoleFollower), "restarted node %s should be follower", victim)
		Expect(c.view(victim).LeaderID).To(Equal(leader), "restarted node %s should follow %q", victim, leader)
		Expect(c.leaders()).To(Equal([]warden.NodeID{leader}), "the original leader %q should still lead alone", leader)
		// The restart must not have caused a disruptive term explosion; the
		// term should be unchanged (leader kept majority throughout).
		Expect(c.term(leader)).To(Equal(termBefore), "term changed across a non-disruptive restart")
	})

	// TestSplitBrainMinorityStaysLeaderless
	It("keeps a partitioned minority leaderless and converges on heal", func() {
		c := newCluster(GinkgoT(), "a", "b", "c", "d", "e")

		// Partition BEFORE any leader exists: minority {a,b}, majority {c,d,e}.
		minority := []warden.NodeID{"a", "b"}
		majority := []warden.NodeID{"c", "d", "e"}
		c.partition(minority, majority)

		// Only the majority side can reach quorum (3 of 5).
		leader := c.electLeader(majority...)
		Expect(contains(majority, leader)).To(BeTrue(), "leader %q must be on the majority side %v", leader, majority)

		// No node on the minority side may ever be leader.
		for _, id := range minority {
			Expect(c.role(id)).NotTo(Equal(warden.RoleLeader), "minority node %s became leader; split-brain!", id)
		}
		Expect(c.leaders()).To(HaveLen(1), "exactly one leader expected during partition")

		// Keep advancing across many further election timeouts and check at
		// every step: the minority must never elect at ANY instant during the
		// partition, and the cluster never has more than the one majority-side
		// leader.
		for i := 0; i < 60; i++ {
			c.Advance(c.step())
			for _, id := range minority {
				Expect(c.role(id)).NotTo(Equal(warden.RoleLeader), "minority node %s became leader mid-partition (step %d); split-brain!", id, i)
			}
			Expect(len(c.leaders())).To(BeNumerically("<=", 1), "multiple leaders mid-partition (step %d): %v", i, c.leaders())
		}

		// Heal: the cluster must converge to a single leader on a single term.
		c.heal()
		final := c.electLeader()
		term := c.term(final)
		for _, id := range c.ids {
			v := c.view(id)
			Expect(v.Term).To(Equal(term), "after heal, %s should be at agreed term %d", id, term)
			if id == final {
				Expect(v.Role).To(Equal(warden.RoleLeader), "%s should be the single leader", id)
			} else {
				Expect(v.Role).To(Equal(warden.RoleFollower), "%s should be a follower", id)
				Expect(v.LeaderID).To(Equal(final), "%s should follow %q", id, final)
			}
		}
	})
})
