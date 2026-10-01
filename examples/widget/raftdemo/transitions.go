package raftdemo

import "math/bits"

// outcome is what one transition produced: the next state, whatever the node
// must send, and the two things the goroutine around it has to do that a pure
// function cannot.
//
// It is a named struct rather than three return values because the two flags are
// easy to swap and impossible to swap here, and because a transition that grows
// a fourth obligation should not change eight signatures to say so.
type outcome struct {
	// State is the node's state after the transition. It is always set: a
	// transition that changes nothing returns what it was given.
	State nodeState

	// Send is what leaves the node, in order.
	Send []envelope

	// ResetElectionTimer is whether the node has just heard something that
	// entitles the current leader to more time — a heartbeat, a vote it
	// granted — or has just started campaigning and owes itself a fresh
	// timeout before campaigning again.
	ResetElectionTimer bool

	// Beat marks the transition as a leader's heartbeat round. It is the one
	// signal the fleet view is minted from, which is what makes one snapshot
	// one round: the observer emits when a leader beats, and on its own timer
	// only while there is no leader to beat.
	Beat bool
}

// transition is one protocol rule: a pure function from a state and a message to
// the next state and what it sends.
//
// Every rule has this shape, so they are values in a table rather than methods
// on a node. That is what lets a specification enumerate the protocol —
// [messageKinds] against [transitions] — and drive each rule with a literal
// state instead of standing a cluster up to reach it.
type transition func(current nodeState, incoming message) outcome

// transitions is the protocol: one rule per message kind, and no rule anywhere
// else.
//
// A kind absent from this table is a message a node silently ignores, which is
// the failure the completeness specification exists to prevent. The table is
// only ever read, and only ever by key, so its iteration order is never a
// question.
var transitions = map[messageKind]transition{
	kindVoteRequest:     onVoteRequest,
	kindVoteGrant:       onVoteGrant,
	kindHeartbeat:       onHeartbeat,
	kindAck:             onAck,
	kindElectionTimeout: onElectionTimeout,
	kindHeartbeatTick:   onHeartbeatTick,
	kindCrash:           onCrash,
	kindRecover:         onRecover,
}

// step applies one message to one state.
//
// Two rules live here rather than in the table, because both are universal and a
// rule repeated in eight places is a rule that will differ in one of them:
//
//  1. A crashed node does nothing but recover. It receives no message, sends no
//     message and keeps the term and the vote it had when it went down, which is
//     what a node whose state is on disk does when its process restarts.
//  2. Any peer message carrying a higher term demotes the node to a follower of
//     that term with no vote cast — Raft §5.1, and the reason two leaders cannot
//     share a term. It applies to peer traffic only: a timer and an operator
//     control carry no term and must not be read as if they did.
func step(current nodeState, incoming message) outcome {
	if current.down && incoming.Kind != kindRecover {
		return outcome{State: current}
	}

	working := current
	if incoming.From != selfDelivered && incoming.Term > working.term {
		working.term = incoming.Term
		working.role = roleFollower
		working.votedFor = unvoted
		working.leader = unknownLeader
		working.votes = 0
	}

	rule, known := transitions[incoming.Kind]
	if !known {
		return outcome{State: working}
	}
	return rule(working, incoming)
}

// onVoteRequest answers a candidate.
//
// The answer is sent either way. A refusal costs one message and saves the
// candidate a whole election timeout of waiting to find out it lost, and it is
// the only reason a split vote resolves in one round rather than two.
//
// There is no up-to-date check, because there is no log to be behind on. That is
// the whole of what "Raft's election half" removes.
func onVoteRequest(current nodeState, incoming message) outcome {
	granted := incoming.Term == current.term &&
		(current.votedFor == unvoted || current.votedFor == incoming.From)

	next := current
	if granted {
		next.votedFor = incoming.From
	}
	return outcome{
		State: next,
		Send: []envelope{{
			To:      incoming.From,
			Message: message{Kind: kindVoteGrant, Term: next.term, From: next.id, Granted: granted},
		}},
		// A node that has just voted owes the candidate it voted for the time
		// to win. Without this a cluster campaigns over its own election.
		ResetElectionTimer: granted,
	}
}

// onVoteGrant counts a vote, and promotes the candidate the moment it has a
// majority.
//
// A grant for a term the node has left, or arriving at a node that is no longer
// a candidate, is dropped rather than counted: both are the same stale message,
// and counting one would let a node reach a majority out of two elections.
func onVoteGrant(current nodeState, incoming message) outcome {
	if current.role != roleCandidate || incoming.Term != current.term || !incoming.Granted {
		return outcome{State: current}
	}

	next := current
	next.votes |= 1 << incoming.From
	if bits.OnesCount64(next.votes) < next.quorum() {
		return outcome{State: next}
	}
	return promote(next)
}

// promote turns a candidate holding a majority into the leader of its term.
//
// The votes it won become the previous round's acknowledgements. That is not
// bookkeeping convenience: every one of those nodes answered a message in this
// term a moment ago, so they are demonstrably alive, and a leader that started
// from zero would report a quorum it actually has as a quorum it has lost for
// exactly one heartbeat — which the card beside this package would draw as an
// outage every single election.
func promote(current nodeState) outcome {
	next := current
	next.role = roleLeader
	next.leader = next.id
	next.acked = 1 << next.id
	next.ackedPrevious = current.votes
	return outcome{
		State: next,
		Send: []envelope{{
			To:      broadcast,
			Message: message{Kind: kindHeartbeat, Term: next.term, From: next.id},
		}},
		ResetElectionTimer: true,
		Beat:               true,
	}
}

// onHeartbeat accepts a leader for the term.
//
// A heartbeat from a stale term is ignored rather than answered: the sender has
// already been superseded, and an acknowledgement would tell it otherwise. A
// heartbeat at the node's own term demotes a candidate, which is how a losing
// candidate stops campaigning without waiting for its timeout.
func onHeartbeat(current nodeState, incoming message) outcome {
	if incoming.Term < current.term {
		return outcome{State: current}
	}

	next := current
	next.role = roleFollower
	next.leader = incoming.From
	next.votes = 0
	return outcome{
		State: next,
		Send: []envelope{{
			To:      incoming.From,
			Message: message{Kind: kindAck, Term: next.term, From: next.id},
		}},
		ResetElectionTimer: true,
	}
}

// onAck records that a peer is alive this round. Only a leader of the
// acknowledged term counts one; anything else is a message that outlived its
// term.
func onAck(current nodeState, incoming message) outcome {
	if current.role != roleLeader || incoming.Term != current.term {
		return outcome{State: current}
	}

	next := current
	next.acked |= 1 << incoming.From
	return outcome{State: next}
}

// onElectionTimeout stands the node for election in a new term.
//
// A leader has heard from nobody because it is the one talking, so its timeout
// is a wakeup and nothing more. Everyone else bumps the term, votes for itself
// and asks — and a single-node cluster is its own majority, so it is elected on
// the spot rather than waiting for an answer that has nobody to come from.
func onElectionTimeout(current nodeState, incoming message) outcome {
	if current.role == roleLeader {
		return outcome{State: current, ResetElectionTimer: true}
	}

	next := current
	next.term++
	next.role = roleCandidate
	next.votedFor = next.id
	next.leader = unknownLeader
	next.votes = 1 << next.id

	if bits.OnesCount64(next.votes) >= next.quorum() {
		return promote(next)
	}
	return outcome{
		State: next,
		Send: []envelope{{
			To:      broadcast,
			Message: message{Kind: kindVoteRequest, Term: next.term, From: next.id},
		}},
		ResetElectionTimer: true,
	}
}

// onHeartbeatTick is a leader's round.
//
// The round rolls over first — what acknowledged the last round becomes the
// previous round, and this one starts with the leader itself — and then the
// heartbeat goes out. Every node runs this timer; only a leader acts on it,
// because a follower that is about to be elected should not have to wait for a
// ticker it never started.
func onHeartbeatTick(current nodeState, incoming message) outcome {
	if current.role != roleLeader {
		return outcome{State: current}
	}

	next := current
	next.ackedPrevious = next.acked
	next.acked = 1 << next.id
	return outcome{
		State: next,
		Send: []envelope{{
			To:      broadcast,
			Message: message{Kind: kindHeartbeat, Term: next.term, From: next.id},
		}},
		Beat: true,
	}
}

// onCrash fails a node.
//
// What it keeps is the term and the vote, because those are the two things
// warden writes to disk: a node that came back having forgotten its vote could
// vote a second time in the same term, and two majorities of one term would then
// be reachable. What it drops is everything a running process holds — its role,
// who it thought led, the votes it was collecting, who it had heard from.
func onCrash(current nodeState, incoming message) outcome {
	next := current
	next.down = true
	next.role = roleFollower
	next.leader = unknownLeader
	next.votes = 0
	next.acked = 0
	next.ackedPrevious = 0
	return outcome{State: next}
}

// onRecover restarts a crashed node as a follower.
//
// It comes back not knowing who leads, and with a fresh election timeout rather
// than an expired one — so a cluster that still has a healthy leader gets to tell
// the returning node about it before the returning node campaigns over it.
func onRecover(current nodeState, incoming message) outcome {
	next := current
	next.down = false
	next.role = roleFollower
	next.leader = unknownLeader
	next.votes = 0
	return outcome{State: next, ResetElectionTimer: true}
}
