package session

import (
	"time"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
)

// MaxEventContributing is the largest Event.Contributing an application may put
// on one event it emits through an Emitter. It is enforced at the emit path,
// before the event reaches a mailbox, so an over-long list is a deterministic
// failure of that effect rather than a frame that dies later on the actor
// goroutine (D-18).
//
// 64 is H-4's bound on every other repeated field in the schema —
// Event.fields, Patch.updates, Snapshot.updates. Origin.contributing_event_ids
// is 1024 not because one event may name a thousand causes but because it is an
// accumulator this library fills by coalescing, and a per-event claim is an
// ordinary per-message list. The bound also has to be small for a second
// reason, which the arithmetic below makes explicit: every identifier an
// application may add to one event is an identifier the library may not
// coalesce, so this number is subtracted from the coalescing headroom.
//
// It is documented in live.Event.Contributing's godoc and derived here. There
// is no exported constant and no Limits field: making it configurable would let
// an operator configure their way back into D-18.
const MaxEventContributing = 64

// MaxCoalesceFlushAt is the largest CoalesceFlushAt a session can honour.
//
// It is below the schema ceiling, and the difference is not a safety margin —
// it is arithmetic. The derivation below is stated in ONE set of terms, which
// it previously was not: the prose described the second term as
// "MaxEventContributing plus the scheduledBy edge, bounded together" (65) while
// the formula used MaxEventContributing (64) and carried the library's edge in
// a separate "+ 1". Both readings land on 959 only because the two spare terms
// cancel, and a derivation that is right by cancellation is one edit from being
// wrong (U-3).
//
// The flush trigger is evaluated against the union the frame will actually
// carry (Actor.unionReaches), so a transition is deferred only while that union
// is strictly below CoalesceFlushAt — at most F - 1 identifiers. The next
// emission adds at most:
//
//	1 identifier    the deferred transition's own event id. unionEdges excludes
//	                an origin's own id from the union it is the origin of, so it
//	                is not in the F - 1 above; takePending folds it in one
//	                emission later.
//	1 identifier    the scheduledBy edge this library prepends to an effect's
//	                emission (effects.go, scheduledEdge).
//	64 identifiers  MaxEventContributing, the largest Contributing an
//	                application may put on the event being emitted. The emit
//	                path refuses more, which is what D-18 closed.
//
// so the widest frame carries (F - 1) + 1 + 1 + MaxEventContributing
// = F + 1 + MaxEventContributing, and F may go up to
// CoalesceFlushCeiling - 1 - MaxEventContributing. At the maximum that is
// exactly 1024, which checkListBounds accepts because it refuses only a list
// LONGER than the bound. The headroom is one element, and
// backpressure_test.go's "carries exactly the widest union the schema permits"
// is what drives it there — a margin no test touches is a margin the next edit
// spends.
//
// The "- 1" alone is D-14's constant, and this is that constant with the term
// D-14 did not have: before D-18 the library was the only contributor, the
// application's half was effectively 0, and 1023 was correct. It stopped being
// correct the moment an application was allowed to contribute, which is what
// C-31(b) is about — a legal per-event bound plus a legal CoalesceFlushAt could
// still overflow at flush, because the trigger was reading a proxy
// (len(pendingIDs)) and not the frame.
//
// Measured over the D-14 repro, 4,000 unacknowledged transitions and a resync,
// with the library the only contributor:
//
//	CoalesceFlushAt   largest union on the wire   provenance
//	           512                          512   3,978 of 3,978 carried
//	           959                          959   3,982 of 3,982 carried
//
// The union column used to read one higher at each row, because the old trigger
// counted the deferred set and the frame carried one more than it counted.
// Counting the frame removed the discrepancy rather than the identifier: the
// same provenance reaches the wire, one flush earlier.
const MaxCoalesceFlushAt = protocol.CoalesceFlushCeiling - 1 - MaxEventContributing

// Limits are a session's resource bounds. Every zero field takes its
// documented default through Normalize, so a caller can set one field without
// restating the rest.
type Limits struct {
	// MaxInboundFrameBytes caps a decoded frame, applied to the connection
	// before any payload is allocated.
	MaxInboundFrameBytes int

	// MaxEventsPerSecond and EventBurst are the inbound event token bucket.
	MaxEventsPerSecond float64

	// EventBurst is that bucket's depth: how far a flurry of interactions may
	// run ahead of the sustained rate before the limiter refuses.
	EventBurst int

	// MailboxDepth bounds the actor's mailbox. It is also a memory parameter:
	// a Go buffered channel allocates its whole backing array at make time,
	// for the life of the channel, occupied or not. The mailbox holds
	// pointers for that reason.
	MailboxDepth int

	// AckChannelDepth bounds the acknowledgement channel. A full channel
	// drops, which is lossless because an acknowledgement is a cumulative
	// high-water mark: the next one supersedes the one dropped and the window
	// re-opens a round trip later.
	AckChannelDepth int

	// AckWindow is the number of unacknowledged patches allowed in flight.
	AckWindow int

	// CoalesceFlushAt is the size of the contributing-event union at which a
	// coalesced patch is emitted immediately rather than coalesced further.
	// It is the union the frame will carry, not a proxy for it: the trigger
	// counts what emitPatch is about to build, including the identifiers the
	// application contributed to the event being emitted.
	//
	// It is a flush trigger, so the schema's ceiling is unreachable and no
	// contributing event is ever dropped — but only while it stays at or below
	// MaxCoalesceFlushAt. Above that the frame the flush constructs is one the
	// schema refuses, and the trigger becomes the loss. The exported boundary
	// enforces the range; this package documents it.
	CoalesceFlushAt int

	// MinResyncInterval and ResyncBurst are the resync bucket, deliberately
	// independent of the event bucket. A resync is the one client frame that
	// triggers work proportional to the whole state, so sharing a budget with
	// ordinary events would make it an amplification vector.
	MinResyncInterval time.Duration

	// ResyncBurst is that bucket's depth. It is small deliberately: a client
	// that legitimately needs a snapshot needs one.
	ResyncBurst int

	// WriteDeadline bounds a single socket write. Exceeding it with a full
	// outbound window is what evicts a client rather than blocking the actor
	// behind it.
	WriteDeadline time.Duration

	// SlowClientGrace is how long a full window is tolerated before the
	// session is closed as a slow client. It is the gap between "stop
	// emitting" and "give up".
	SlowClientGrace time.Duration

	// HeartbeatInterval is the value announced to the client in the mount
	// snapshot: how often it should send a heartbeat.
	HeartbeatInterval time.Duration

	// HeartbeatTimeout is how long the server waits for one before treating
	// the connection as dead. It is deliberately larger than
	// HeartbeatInterval, because equality would close a session on one late
	// frame.
	HeartbeatTimeout time.Duration

	// IdleTimeout closes a session that has exchanged no application traffic,
	// heartbeats excluded — a tab left open forever is a session holding
	// memory for nobody.
	IdleTimeout time.Duration

	// EffectDrainTimeout bounds how long teardown waits for in-flight effects
	// to return before the actor exits anyway. An effect that outlives it has
	// its result discarded, not cancelled: the I/O may already have happened.
	EffectDrainTimeout time.Duration

	// PanicBudget is how many times one site may panic in a session before
	// the session closes. Other sessions are unaffected either way.
	PanicBudget int
}

// DefaultLimits returns the documented defaults.
func DefaultLimits() Limits {
	return Limits{
		MaxInboundFrameBytes: 65536,
		MaxEventsPerSecond:   50,
		EventBurst:           100,
		MailboxDepth:         64,
		AckChannelDepth:      32,
		AckWindow:            16,
		CoalesceFlushAt:      512,
		MinResyncInterval:    time.Second,
		ResyncBurst:          3,
		WriteDeadline:        5 * time.Second,
		SlowClientGrace:      30 * time.Second,
		HeartbeatInterval:    20 * time.Second,
		HeartbeatTimeout:     50 * time.Second,
		IdleTimeout:          30 * time.Minute,
		EffectDrainTimeout:   5 * time.Second,
		PanicBudget:          3,
	}
}

// Normalize fills every zero field from the defaults and returns the result.
func (l Limits) Normalize() Limits {
	d := DefaultLimits()
	if l.MaxInboundFrameBytes == 0 {
		l.MaxInboundFrameBytes = d.MaxInboundFrameBytes
	}
	if l.MaxEventsPerSecond == 0 {
		l.MaxEventsPerSecond = d.MaxEventsPerSecond
	}
	if l.EventBurst == 0 {
		l.EventBurst = d.EventBurst
	}
	if l.MailboxDepth == 0 {
		l.MailboxDepth = d.MailboxDepth
	}
	if l.AckChannelDepth == 0 {
		l.AckChannelDepth = d.AckChannelDepth
	}
	if l.AckWindow == 0 {
		l.AckWindow = d.AckWindow
	}
	if l.CoalesceFlushAt == 0 {
		l.CoalesceFlushAt = d.CoalesceFlushAt
	}
	if l.MinResyncInterval == 0 {
		l.MinResyncInterval = d.MinResyncInterval
	}
	if l.ResyncBurst == 0 {
		l.ResyncBurst = d.ResyncBurst
	}
	if l.WriteDeadline == 0 {
		l.WriteDeadline = d.WriteDeadline
	}
	if l.SlowClientGrace == 0 {
		l.SlowClientGrace = d.SlowClientGrace
	}
	if l.HeartbeatInterval == 0 {
		l.HeartbeatInterval = d.HeartbeatInterval
	}
	if l.HeartbeatTimeout == 0 {
		l.HeartbeatTimeout = d.HeartbeatTimeout
	}
	if l.IdleTimeout == 0 {
		l.IdleTimeout = d.IdleTimeout
	}
	if l.EffectDrainTimeout == 0 {
		l.EffectDrainTimeout = d.EffectDrainTimeout
	}
	if l.PanicBudget == 0 {
		l.PanicBudget = d.PanicBudget
	}
	return l
}

// bucket is a token bucket over an injected clock. It is not concurrency-safe
// and does not need to be: each bucket has exactly one owner goroutine, which
// is stated at the field that holds it.
type bucket struct {
	perSecond float64
	burst     float64
	tokens    float64
	last      time.Time
}

func newBucket(perSecond float64, burst int, now time.Time) *bucket {
	return &bucket{
		perSecond: perSecond,
		burst:     float64(burst),
		tokens:    float64(burst),
		last:      now,
	}
}

// allow consumes one token if the bucket has one.
func (b *bucket) allow(now time.Time) bool {
	if elapsed := now.Sub(b.last); elapsed > 0 {
		b.tokens += elapsed.Seconds() * b.perSecond
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
	}
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}
