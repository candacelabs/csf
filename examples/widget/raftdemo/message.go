package raftdemo

import "strconv"

// messageKind is what one delivery to a node is.
//
// The set is closed and small, and it is the key the transition table is indexed
// by, so a kind added without a transition is caught by the completeness spec
// rather than by a node that quietly ignores a new message.
type messageKind uint8

// The eight kinds. Four cross the network, two are a node's own timers firing,
// and two are an operator reaching in to fail and restore a node.
const (
	kindVoteRequest messageKind = iota + 1
	kindVoteGrant
	kindHeartbeat
	kindAck
	kindElectionTimeout
	kindHeartbeatTick
	kindCrash
	kindRecover
)

// messageKinds is every kind, in declaration order.
//
// It exists so a specification can enumerate the protocol's alphabet and hold
// the transition table complete against it. A method set cannot be enumerated;
// a slice can, and the assertion "every kind has a transition" is only writable
// against something that can.
func messageKinds() []messageKind {
	return []messageKind{
		kindVoteRequest,
		kindVoteGrant,
		kindHeartbeat,
		kindAck,
		kindElectionTimeout,
		kindHeartbeatTick,
		kindCrash,
		kindRecover,
	}
}

// String names a kind for a failure message. An unnamed kind prints its number
// rather than a placeholder, because a number is something a reader can look up
// and "unknown" is not.
func (kind messageKind) String() string {
	switch kind {
	case kindVoteRequest:
		return "vote-request"
	case kindVoteGrant:
		return "vote-grant"
	case kindHeartbeat:
		return "heartbeat"
	case kindAck:
		return "ack"
	case kindElectionTimeout:
		return "election-timeout"
	case kindHeartbeatTick:
		return "heartbeat-tick"
	case kindCrash:
		return "crash"
	case kindRecover:
		return "recover"
	default:
		return "messageKind(" + strconv.Itoa(int(kind)) + ")"
	}
}

// selfDelivered is the sender of a message no peer sent: a timer firing, or an
// operator crashing a node. The distinction matters at exactly one place — the
// term rule in step, which applies to peer traffic and to nothing else.
const selfDelivered = -1

// broadcast is an envelope's destination when every peer but the sender gets it.
const broadcast = -1

// message is one delivery.
//
// It is a single flat struct rather than one type per kind because a node's
// inbox is one channel: a channel of an interface would put an allocation and a
// type assertion on the hot path of a demo, and the union here is four fields
// wide.
type message struct {
	// Kind is which of the eight this is, and therefore which fields below
	// carry anything.
	Kind messageKind

	// Term is the sender's term. Zero on a timer and on an operator control,
	// neither of which has one.
	Term uint64

	// From is the sender's index, or selfDelivered.
	From int

	// Granted is a vote grant's answer. A refusal is sent rather than dropped
	// so a candidate that lost learns it lost instead of waiting out its
	// timeout.
	Granted bool
}

// envelope is one message and where it goes: a peer's index, or broadcast.
type envelope struct {
	// To is the destination node's index, or broadcast for every peer but the
	// sender.
	To int

	// Message is what arrives.
	Message message
}
