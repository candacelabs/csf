package protocol

import (
	"unicode/utf8"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// Origin sources. The vocabulary is a convention rather than a schema: a
// source is whatever an effect reports, so cardinality is bounded at the
// metric rather than here.
const (
	// SourceMount is the origin of the first snapshot of a session.
	SourceMount = "mount"
	// SourceResync is the origin of a snapshot answering a resync request.
	SourceResync = "resync"
	// SourceEventPrefix namespaces a client-caused transition by event name.
	SourceEventPrefix = "event:"
	// SourceEffectPrefix namespaces a transition caused by an effect result.
	SourceEffectPrefix = "effect:"
	// SourceSlowClient is the origin of a transition the library synthesized
	// because the outbound window filled.
	SourceSlowClient = "timer:slow_client"
	// SourceClientRecovered is its counterpart when the window drained.
	SourceClientRecovered = "timer:client_recovered"
	// SourceEffectInvalid stands in for an effect whose own EffectSource()
	// cannot be namespaced into a legal Origin.source. The effect is refused
	// before it runs and the application is told through the ordinary failure
	// event; this is what that event's origin says, because a frame carrying
	// the offending string is exactly what must not be constructed.
	SourceEffectInvalid = "effect:invalid_source"
)

// MaxOriginSource is the schema's byte bound on Origin.source
// (proto/gotthlive/v1/frame.proto, message Origin).
const MaxOriginSource = 64

// ValidOriginSource reports whether s satisfies Origin.source's predicate:
// non-empty, at most MaxOriginSource bytes, and matching
// ^[a-z][a-z0-9_.:/-]*$.
//
// It exists so that a string can be refused by the boundary that owns it
// rather than by the frame it eventually lands in. Origin.source is composed
// from application-supplied halves — an event name, an effect's reported
// source — and a composed value that fails the predicate is dropped by
// ValidateOutbound on the actor goroutine, three layers from the caller, as an
// INTERNAL error the application never hears about. That is the D-18 shape,
// and this is what lets the callers close it: live.New refuses an event name
// that cannot be namespaced, and the actor turns an unnameable effect into
// that effect's own deterministic failure.
//
// It is a hand-written second implementation of a compiled predicate, which is
// a cost. The alternative — constructing a throwaway Origin and calling
// ValidateOrigin — allocates a message per check on the emit path and reports
// "some field of some Origin is wrong" rather than naming this one. The
// conformance suite asserts the two agree over the boundary cases, which is
// what keeps the duplication honest.
func ValidOriginSource(s string) bool {
	if len(s) == 0 || len(s) > MaxOriginSource {
		return false
	}
	if s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c >= 'a' && c <= 'z', c >= '0' && c <= '9':
		case c == '_', c == '.', c == ':', c == '/', c == '-':
		default:
			return false
		}
	}
	return true
}

// Causal is the server-minted chain a sequenced frame carries. Every field is
// monotonic per session and every field is positive: the predicates make a
// frame with a hole in its chain unconstructable.
type Causal struct {
	// ServerSeq is the frame's place in the session's outbound order, from 1.
	// It is what an Ack acknowledges and what a client detects a gap in.
	ServerSeq uint64

	// PatchID names this emitted frame, one per Patch or Snapshot, and is what
	// a client's apply-latency report has to name to be believed.
	PatchID uint64

	// TransitionID names the reducer invocation this frame came from, one per
	// invocation including one that changed nothing.
	TransitionID uint64

	// StateVersion rises if and only if the transition changed state, which is
	// how a re-render is told apart from a state change downstream.
	StateVersion uint64
}

// Origin says what caused a patch. Nothing is ever emitted without one.
type Origin struct {
	// Kind is the category of cause. The zero value is never emitted: the
	// outbound boundary refuses a frame that names no category.
	Kind pb.OriginKind

	// EventID is the server-minted identity of the causing event, zero when
	// the server started the transition itself. It is unforgeable, which is
	// what makes it the authoritative root of the chain.
	EventID uint64

	// ClientRef is the client's own correlation handle, echoed unchanged. It
	// is the only value here that untrusted input chose, and it is carried
	// rather than trusted: nothing downstream keys on it.
	ClientRef uint64

	// Source is the specific cause, such as "event:cart.add". It is composed
	// from application-supplied halves, so ValidOriginSource is what keeps a
	// composed value from failing three layers away as an INTERNAL error.
	Source string
	// Contributing lists events whose state changes this patch carries but
	// which were not individually patched, because coalescing collapsed them.
	Contributing []uint64
}

func (o Origin) proto() *pb.Origin {
	return &pb.Origin{
		Kind:                 o.Kind,
		EventId:              o.EventID,
		ClientRef:            o.ClientRef,
		Source:               o.Source,
		ContributingEventIds: o.Contributing,
	}
}

// Update is the new markup for one live region.
type Update struct {
	// FragmentID is the region this markup belongs to.
	FragmentID string

	// Op is how the client applies it. The zero value is refused at the
	// outbound boundary rather than arriving as a silent morph.
	Op pb.PatchOp

	// HTML is the region's complete markup, not a diff: the diff happens in
	// the browser, against the live DOM.
	HTML string
}

func updateProtos(us []Update) []*pb.FragmentUpdate {
	if len(us) == 0 {
		return nil
	}
	out := make([]*pb.FragmentUpdate, len(us))
	for i, u := range us {
		out[i] = &pb.FragmentUpdate{FragmentId: u.FragmentID, Op: u.Op, Html: u.HTML}
	}
	return out
}

// SessionParams are the values a session sends once, in its first snapshot, so
// that the client needs no configuration of its own.
type SessionParams struct {
	// HeartbeatIntervalMS is how often the client should send a heartbeat.
	// The schema refines it to 1000..300000, so a value outside that builds a
	// frame this library then refuses to send.
	HeartbeatIntervalMS uint32

	// MaxInboundFrameBytes is the largest frame the server will read from this
	// client. Refined to 1024..1048576, for the same reason.
	MaxInboundFrameBytes uint32

	// AckWindow is how many unacknowledged patches the server will hold before
	// it stops emitting. Refined to 1..256.
	AckWindow uint32
}

// NewPatch builds a patch frame. It does not validate: the framer does that on
// the single write path, so there is exactly one place a constructed frame is
// checked and no way to reach the socket around it.
func NewPatch(session [16]byte, c Causal, o Origin, updates []Update) *pb.Frame {
	return &pb.Frame{
		ProtocolVersion: Version,
		SessionId:       session[:],
		Payload: &pb.Frame_Patch{Patch: &pb.Patch{
			ServerSeq:    c.ServerSeq,
			PatchId:      c.PatchID,
			TransitionId: c.TransitionID,
			StateVersion: c.StateVersion,
			Origin:       o.proto(),
			Updates:      updateProtos(updates),
		}},
	}
}

// Supersession is the inclusive server_seq range a resync snapshot replaces.
// Both fields are zero on a session's first snapshot.
type Supersession struct {
	// FromSeq is the first replaced server_seq, inclusive.
	FromSeq uint64

	// ThroughSeq is the last replaced server_seq, inclusive. A range rather
	// than the union of the contributing event identifiers, because the union
	// is unbounded and this is two varints.
	ThroughSeq uint64
}

// NewSnapshot builds a snapshot frame carrying every registered fragment and
// the session parameters.
func NewSnapshot(session [16]byte, c Causal, o Origin, p SessionParams, s Supersession, updates []Update) *pb.Frame {
	return &pb.Frame{
		ProtocolVersion: Version,
		SessionId:       session[:],
		Payload: &pb.Frame_Snapshot{Snapshot: &pb.Snapshot{
			ServerSeq:            c.ServerSeq,
			PatchId:              c.PatchID,
			TransitionId:         c.TransitionID,
			StateVersion:         c.StateVersion,
			Origin:               o.proto(),
			HeartbeatIntervalMs:  p.HeartbeatIntervalMS,
			MaxInboundFrameBytes: p.MaxInboundFrameBytes,
			AckWindow:            p.AckWindow,
			Updates:              updateProtos(updates),
			SupersededFromSeq:    s.FromSeq,
			SupersededThroughSeq: s.ThroughSeq,
		}},
	}
}

// NewError builds an error frame. eventID and clientRef are both zero unless
// the error concerns one event, which H-12 holds to.
func NewError(session [16]byte, code pb.ErrorCode, message string, eventID, clientRef uint64, fatal bool) *pb.Frame {
	return &pb.Frame{
		ProtocolVersion: Version,
		SessionId:       session[:],
		Payload: &pb.Frame_Error{Error: &pb.Error{
			Code:      code,
			Message:   truncateMessage(message),
			EventId:   eventID,
			ClientRef: clientRef,
			Fatal:     fatal,
		}},
	}
}

// maxErrorMessage is the schema's bound on Error.message. Truncating here
// rather than failing validation is deliberate: an error frame is the last
// thing a client hears before a close, and losing it to a length predicate
// would replace a diagnosable failure with a silent one.
const maxErrorMessage = 512

// truncateMessage cuts on a rune boundary, not on a byte.
//
// Error.message is a proto3 string, and protobuf-go refuses to marshal invalid
// UTF-8 into one — so a cut through the middle of a multi-byte rune would fail
// the Send this function exists to keep succeeding, and would do it only for
// the messages long enough to need cutting. That was unreachable while every
// message was a fixed ASCII string; it stopped being unreachable when dev mode
// began appending a panic value, which is whatever the application panicked
// with.
func truncateMessage(s string) string {
	if len(s) <= maxErrorMessage {
		return s
	}
	const ellipsis = "..."
	cut := maxErrorMessage - len(ellipsis)
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut] + ellipsis
}

// NewHeartbeat builds a heartbeat frame.
func NewHeartbeat(session [16]byte, nonce uint64, intervalMS uint32) *pb.Frame {
	return &pb.Frame{
		ProtocolVersion: Version,
		SessionId:       session[:],
		Payload: &pb.Frame_Heartbeat{Heartbeat: &pb.Heartbeat{
			Nonce:      nonce,
			IntervalMs: intervalMS,
		}},
	}
}

// NewAck builds the one acknowledgement the server sends: the answer to a
// resync request that describes no gap, where a full snapshot would be waste.
func NewAck(session [16]byte, serverSeq uint64) *pb.Frame {
	return &pb.Frame{
		ProtocolVersion: Version,
		SessionId:       session[:],
		Payload:         &pb.Frame_Ack{Ack: &pb.Ack{ServerSeq: serverSeq}},
	}
}
