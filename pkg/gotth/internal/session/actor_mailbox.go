package session

import (
	"sync"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
)

// msgKind is what a mailbox message asks the actor to do. Only the first three
// reach a reducer.
type msgKind uint8

const (
	msgEvent msgKind = iota + 1
	msgEffectResult
	msgSynthetic
	msgResync
	msgTelemetry
)

// inbound is one mailbox message.
//
// The mailbox is a channel of pointers to these, and that is a memory decision
// as much as a flood-control one. A Go buffered channel allocates its entire
// backing array eagerly, at make time, for the life of the channel, occupied
// or not. A channel of values at this struct's size would reserve several
// kilobytes per idle connection — a sixth of the whole per-connection budget,
// paid by every connection whether or not it ever sends an event. A channel of
// pointers reserves eight bytes a slot, and the structs come from a pool only
// while actually queued.
type inbound struct {
	kind   msgKind
	ev     Event
	origin protocol.Origin

	// resync
	lastAppliedSeq uint64

	// telemetry
	patchID     uint64
	morphMicros uint32
	applyMicros uint32

	// span links the work the actor is about to do back to the span the read
	// pump opened when the frame arrived.
	span obs.SpanRef
}

func (m *inbound) reset() {
	*m = inbound{}
}

var inboundPool = sync.Pool{New: func() any { return new(inbound) }}

func getInbound() *inbound { return inboundPool.Get().(*inbound) }

func putInbound(m *inbound) {
	m.reset()
	inboundPool.Put(m)
}

// post hands a message to the actor without ever blocking.
//
// Blocking here would let one client's flood stall the read pump, and with it
// the connection's own liveness detection — which is the failure the bound
// exists to prevent, arrived at by a different route. A full mailbox drops,
// and the caller answers the client with a typed error.
func (a *Actor[I]) post(m *inbound) bool {
	select {
	case a.mailbox <- m:
		return true
	default:
		putInbound(m)
		return false
	}
}
