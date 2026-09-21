package raftdemo

import (
	"math/bits"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// The protocol is a table of pure functions, so every rule below is driven from
// a literal state. Not one specification in this file starts a goroutine, and
// that is the property being demonstrated as much as the rules are: a cluster
// standing up to reach a state is a cluster whose timing has to be waited out.

// sole returns the one envelope an outcome sent, failing when it sent any other
// number — which is always the more useful failure than indexing into an empty
// slice.
func sole(result outcome) envelope {
	GinkgoHelper()
	Expect(result.Send).To(HaveLen(1), "expected exactly one message to leave the node")
	return result.Send[0]
}

var _ = Describe("The transition table", func() {
	// A method set cannot be enumerated and a map can, which is the whole
	// reason the protocol is a table: this assertion is unwritable against a
	// dispatch chain, and without it a ninth message kind is a message every
	// node silently ignores.
	It("has exactly one rule per message kind, and no rule for anything else", func() {
		kinds := messageKinds()
		Expect(transitions).To(HaveLen(len(kinds)),
			"every message kind needs a rule and no rule may exist without a kind")
		for _, kind := range kinds {
			Expect(transitions).To(HaveKey(kind), "no rule handles %s", kind)
		}
	})

	It("names every kind it dispatches on", func() {
		for _, kind := range messageKinds() {
			Expect(kind.String()).ToNot(ContainSubstring("messageKind("),
				"kind %d prints as a number, so a failure naming it says nothing", uint8(kind))
		}
	})
})

var _ = Describe("A node answering a vote request", func() {
	It("grants the first candidate of a term and refuses the second", func() {
		voter := newNodeState(0, 3)

		first := step(voter, message{Kind: kindVoteRequest, Term: 1, From: 1})
		Expect(sole(first).Message.Granted).To(BeTrue())
		Expect(first.State.votedFor).To(Equal(1))
		Expect(first.State.term).To(Equal(uint64(1)), "the request's term is adopted before it is answered")
		Expect(first.ResetElectionTimer).To(BeTrue(),
			"a node that voted owes the candidate it voted for the time to win")

		second := step(first.State, message{Kind: kindVoteRequest, Term: 1, From: 2})
		Expect(sole(second).Message.Granted).To(BeFalse(),
			"a second vote in one term is what lets two majorities of that term exist")
		Expect(second.State.votedFor).To(Equal(1))
		Expect(second.ResetElectionTimer).To(BeFalse())
	})

	It("grants the same candidate again, because a retry is not a second vote", func() {
		voter := newNodeState(0, 3)
		granted := step(voter, message{Kind: kindVoteRequest, Term: 1, From: 1})

		retry := step(granted.State, message{Kind: kindVoteRequest, Term: 1, From: 1})
		Expect(sole(retry).Message.Granted).To(BeTrue())
	})

	It("refuses a candidate campaigning in a term it has already left", func() {
		voter := newNodeState(0, 3)
		voter.term = 4

		stale := step(voter, message{Kind: kindVoteRequest, Term: 2, From: 1})
		Expect(sole(stale).Message.Granted).To(BeFalse())
		Expect(stale.State.term).To(Equal(uint64(4)), "a term never walks backwards")
	})

	It("answers a refusal rather than dropping it", func() {
		voter := newNodeState(0, 3)
		voter.term, voter.votedFor = 1, 2

		refused := step(voter, message{Kind: kindVoteRequest, Term: 1, From: 1})
		Expect(sole(refused).To).To(Equal(1),
			"a candidate that is told it lost stops waiting out its whole timeout")
	})
})

var _ = Describe("The universal term rule", func() {
	It("demotes a leader that meets a higher term, and clears the vote it cast", func() {
		leader := promote(candidateWithVotes(0, 3, 1, 0b011)).State
		Expect(leader.role).To(Equal(roleLeader))

		demoted := step(leader, message{Kind: kindHeartbeat, Term: 9, From: 2})
		Expect(demoted.State.role).To(Equal(roleFollower))
		Expect(demoted.State.term).To(Equal(uint64(9)))
		Expect(demoted.State.leader).To(Equal(2))
		Expect(demoted.State.votes).To(BeZero())
	})

	It("does not read a term off a timer, which has none", func() {
		leader := promote(candidateWithVotes(0, 3, 5, 0b011)).State

		woken := step(leader, message{Kind: kindElectionTimeout, From: selfDelivered})
		Expect(woken.State.term).To(Equal(uint64(5)),
			"a zero-term timer must not be read as a term below the node's own")
		Expect(woken.State.role).To(Equal(roleLeader))
		Expect(woken.ResetElectionTimer).To(BeTrue())
	})
})

var _ = Describe("A candidate", func() {
	It("is promoted the moment its votes reach a majority, and beats immediately", func() {
		candidate := candidateWithVotes(0, 3, 1, 0b001)

		promoted := step(candidate, message{Kind: kindVoteGrant, Term: 1, From: 1, Granted: true})
		Expect(promoted.State.role).To(Equal(roleLeader))
		Expect(promoted.State.leader).To(Equal(0))
		Expect(promoted.Beat).To(BeTrue(), "the view a leader mints starts at its own election")
		Expect(sole(promoted).Message.Kind).To(Equal(kindHeartbeat))
		Expect(sole(promoted).To).To(Equal(broadcast))
	})

	It("counts the nodes that voted for it as alive, so an election is not an outage", func() {
		candidate := candidateWithVotes(0, 3, 1, 0b001)

		promoted := step(candidate, message{Kind: kindVoteGrant, Term: 1, From: 1, Granted: true})
		Expect(promoted.State.aliveVoters()).To(Equal(2),
			"a node that answered this term a moment ago is demonstrably reachable")
		Expect(promoted.State.aliveVoters()).To(BeNumerically(">=", promoted.State.quorum()))
	})

	It("ignores a grant for a term it has left", func() {
		candidate := candidateWithVotes(0, 5, 7, 0b00001)

		stale := step(candidate, message{Kind: kindVoteGrant, Term: 6, From: 1, Granted: true})
		Expect(stale.State.votes).To(Equal(uint64(0b00001)))
		Expect(stale.State.role).To(Equal(roleCandidate))
	})

	It("stops campaigning when the winner's heartbeat arrives at its own term", func() {
		candidate := candidateWithVotes(0, 3, 1, 0b001)

		beaten := step(candidate, message{Kind: kindHeartbeat, Term: 1, From: 2})
		Expect(beaten.State.role).To(Equal(roleFollower))
		Expect(beaten.State.leader).To(Equal(2))
		Expect(sole(beaten).Message.Kind).To(Equal(kindAck))
	})
})

var _ = Describe("An election timeout", func() {
	It("bumps the term, votes for itself and asks everyone", func() {
		follower := newNodeState(1, 3)

		standing := step(follower, message{Kind: kindElectionTimeout, From: selfDelivered})
		Expect(standing.State.term).To(Equal(uint64(1)))
		Expect(standing.State.role).To(Equal(roleCandidate))
		Expect(standing.State.votedFor).To(Equal(1))
		Expect(bits.OnesCount64(standing.State.votes)).To(Equal(1))
		Expect(sole(standing).Message).To(Equal(
			message{Kind: kindVoteRequest, Term: 1, From: 1}))
		Expect(standing.ResetElectionTimer).To(BeTrue(),
			"a candidate that loses must campaign again rather than wait forever")
	})

	It("elects a one-node cluster on the spot, because it is its own majority", func() {
		alone := newNodeState(0, 1)

		standing := step(alone, message{Kind: kindElectionTimeout, From: selfDelivered})
		Expect(standing.State.role).To(Equal(roleLeader))
		Expect(standing.Beat).To(BeTrue())
	})
})

var _ = Describe("A leader's heartbeat round", func() {
	It("rolls the acknowledgement window over and beats", func() {
		leader := promote(candidateWithVotes(0, 3, 1, 0b011)).State
		acked := step(leader, message{Kind: kindAck, Term: 1, From: 2})
		Expect(acked.State.acked).To(Equal(uint64(0b101)))

		rolled := step(acked.State, message{Kind: kindHeartbeatTick, From: selfDelivered})
		Expect(rolled.State.ackedPrevious).To(Equal(uint64(0b101)))
		Expect(rolled.State.acked).To(Equal(uint64(0b001)), "a round starts with the leader alone")
		Expect(rolled.State.aliveVoters()).To(Equal(2),
			"reading two rounds is what stops the count dipping at every rollover")
		Expect(rolled.Beat).To(BeTrue())
	})

	It("is nothing at all to a node that is not the leader", func() {
		follower := newNodeState(2, 3)

		ticked := step(follower, message{Kind: kindHeartbeatTick, From: selfDelivered})
		Expect(ticked).To(Equal(outcome{State: follower}))
	})

	It("does not count an acknowledgement from a term it has left", func() {
		leader := promote(candidateWithVotes(0, 3, 4, 0b011)).State

		stale := step(leader, message{Kind: kindAck, Term: 3, From: 2})
		Expect(stale.State.acked).To(Equal(leader.acked))
	})
})

var _ = Describe("A crashed node", func() {
	It("drops every message but a recovery, and sends none", func() {
		leader := promote(candidateWithVotes(0, 3, 2, 0b011)).State
		crashed := step(leader, message{Kind: kindCrash, From: selfDelivered})
		Expect(crashed.State.down).To(BeTrue())
		Expect(crashed.State.role).To(Equal(roleFollower))

		for _, ignored := range []message{
			{Kind: kindHeartbeat, Term: 9, From: 1},
			{Kind: kindVoteRequest, Term: 9, From: 1},
			{Kind: kindElectionTimeout, From: selfDelivered},
			{Kind: kindHeartbeatTick, From: selfDelivered},
		} {
			after := step(crashed.State, ignored)
			Expect(after).To(Equal(outcome{State: crashed.State}),
				"%s reached a node that is not running", ignored.Kind)
		}
	})

	It("comes back knowing the term it voted in, so it cannot vote twice in it", func() {
		voter := newNodeState(0, 3)
		voted := step(voter, message{Kind: kindVoteRequest, Term: 1, From: 1})
		crashed := step(voted.State, message{Kind: kindCrash, From: selfDelivered})

		restarted := step(crashed.State, message{Kind: kindRecover, From: selfDelivered})
		Expect(restarted.State.down).To(BeFalse())
		Expect(restarted.State.term).To(Equal(uint64(1)))
		Expect(restarted.State.votedFor).To(Equal(1))
		Expect(restarted.State.leader).To(Equal(unknownLeader))
		Expect(restarted.ResetElectionTimer).To(BeTrue(),
			"a returning node must hear a healthy leader before it campaigns over one")

		second := step(restarted.State, message{Kind: kindVoteRequest, Term: 1, From: 2})
		Expect(sole(second).Message.Granted).To(BeFalse())
	})
})

var _ = Describe("Node names", func() {
	It("are positional and generic, and never a machine anybody owns", func() {
		Expect(nodeName(0)).To(Equal("node-a"))
		Expect(nodeName(2)).To(Equal("node-c"))
		Expect(nodeName(25)).To(Equal("node-z"))
		Expect(nodeName(26)).To(Equal("node-aa"))
		Expect(nodeName(maxNodes - 1)).To(MatchRegexp(`^node-[a-z]+$`))
	})
})

// candidateWithVotes is a node mid-election: campaigning in term, holding the
// votes in the mask, and having voted for itself.
func candidateWithVotes(id int, peers int, term uint64, votes uint64) nodeState {
	state := newNodeState(id, peers)
	state.term = term
	state.role = roleCandidate
	state.votedFor = id
	state.votes = votes
	return state
}
