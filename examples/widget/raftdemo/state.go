package raftdemo

import "math/bits"

// role is what a node is doing in its current term.
type role string

// The three roles. They are strings rather than integers because they are read
// far more often than they are compared — in a report, in a failure message, in
// the fleet view — and a role that prints as 2 costs every reader a lookup.
const (
	roleFollower  role = "follower"
	roleCandidate role = "candidate"
	roleLeader    role = "leader"
)

// unvoted is votedFor when a node has not voted in its current term.
const unvoted = -1

// unknownLeader is leader when a node does not know who leads its term.
const unknownLeader = -1

// nodeState is everything one node knows.
//
// It is a plain copyable value with no pointer and no slice in it, and that is
// the property the whole package rests on: a transition takes one and returns
// the next, so the protocol is a pure function that a specification can drive
// with no goroutine, no timer and no channel — and the goroutine that owns one
// can hand a copy to a transition without any chance of the transition keeping a
// reference to state it does not own.
//
// The two liveness fields are bitmasks for the same reason, which is what fixes
// the cluster ceiling at maxNodes.
type nodeState struct {
	// id is this node's index, and the bit it occupies in every mask below.
	id int

	// peers is the cluster's size, so that quorum is derivable here rather
	// than passed into every transition.
	peers int

	// term is the node's current term. It only ever rises.
	term uint64

	// role is what the node is doing in that term.
	role role

	// votedFor is who the node voted for in that term, or unvoted. It survives
	// a crash, because a node that forgot its vote could vote twice in one term
	// and two leaders could then hold the same term.
	votedFor int

	// leader is who the node believes leads its term, or unknownLeader.
	leader int

	// votes is the set of nodes that granted this candidate a vote in its
	// current term, including its own.
	votes uint64

	// acked is the set of nodes that acknowledged the heartbeat round in
	// progress, including the leader itself.
	acked uint64

	// ackedPrevious is the set that acknowledged the round before. Liveness
	// reads both, so a peer that answers every round is never briefly counted
	// dead in the instant after a round rolls over.
	ackedPrevious uint64

	// down is whether this node has been crashed. A down node drops everything
	// but a recovery, and sends nothing at all.
	down bool
}

// newNodeState is a node as it starts: a follower in term zero, having voted for
// nobody and knowing no leader.
//
// It is a constructor rather than a zero value because the zero value of role is
// the empty string, and a node whose role prints as "" is a node whose reports
// are unreadable at exactly the moment somebody is reading them.
func newNodeState(id int, peers int) nodeState {
	return nodeState{
		id:       id,
		peers:    peers,
		role:     roleFollower,
		votedFor: unvoted,
		leader:   unknownLeader,
	}
}

// quorum is the majority this node needs: n/2+1, counting itself.
func (state nodeState) quorum() int { return state.peers/2 + 1 }

// aliveVoters is how many members a leader can currently see, itself included.
//
// It reads two rounds rather than one so that the answer does not dip at every
// rollover: a peer that answered the last round is alive now, whatever it has
// done about the round that started a moment ago.
func (state nodeState) aliveVoters() int {
	return bits.OnesCount64(state.acked | state.ackedPrevious)
}

// observable is the part of a node's state the fleet view is built from.
//
// It is a separate comparable value so that "did anything worth telling the
// observer about change" is one comparison rather than a list of fields that
// grows a bug the day somebody adds a field and forgets this line.
type observable struct {
	term   uint64
	role   role
	leader int
	down   bool
}

// observable projects the node's state into the part the observer reads.
func (state nodeState) observable() observable {
	return observable{term: state.term, role: state.role, leader: state.leader, down: state.down}
}

// nodeName is one node's neutral name: node-a, node-b, … node-z, node-aa.
//
// Names are positional and generic on purpose. This engine drives a widget whose
// document names no host, no address and no credential, and a demo that leaked a
// real machine's name into the picture would undo that by the back door.
func nodeName(index int) string {
	letters := ""
	for position := index; ; position = position/26 - 1 {
		letters = string(rune('a'+position%26)) + letters
		if position < 26 {
			break
		}
	}
	return "node-" + letters
}
