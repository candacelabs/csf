package protocol

import (
	"google.golang.org/protobuf/proto"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// IInbound is a closed sum type over the frames a client may send. Values
// returned by ParseInbound hold immutable scalar snapshots copied only after
// the generated Liquid Proto validators succeed.
//
// The type is closed by an unexported method: a package outside this one
// cannot add a variant, which is what makes the switch in the session ingress
// exhaustive in fact and not merely by convention.
type IInbound interface {
	isInbound()
	// Kind reports which payload this variant carries.
	Kind() Kind
	// Envelope returns a fresh copy of the validated frame envelope. Mutating
	// the copy cannot alter the accepted IInbound value.
	Envelope() *pb.Frame
	sessionIDValue() [16]byte
}

type inboundBase struct {
	frame     *pb.Frame
	kind      Kind
	sessionID [16]byte
}

func (b inboundBase) isInbound() {}
func (b inboundBase) Kind() Kind { return b.kind }
func (b inboundBase) Envelope() *pb.Frame {
	return proto.Clone(b.frame).(*pb.Frame)
}
func (b inboundBase) sessionIDValue() [16]byte { return b.sessionID }

// EventField is one validated event form field copied out of the decoded
// protobuf. Its string values do not alias mutable wire storage.
type EventField struct {
	// Key is the validated form-control name. It is non-empty, at most 128
	// bytes, and contains only ASCII letters, digits, underscores, periods,
	// brackets, or hyphens.
	Key string
	// Value is the copied form-control value. Validation limits it to 8 KiB.
	Value string
}

// InboundEvent is one interaction raised by the browser. Fields are refined
// element by element, because a repeated message field is neither refined nor
// bounded by the generated boundary.
type InboundEvent struct {
	inboundBase
	clientRef     uint64
	name          string
	fragmentID    string
	seenServerSeq uint64
	fields        []EventField
}

// ClientRef is the validated client-local correlation identifier.
func (e InboundEvent) ClientRef() uint64 { return e.clientRef }

// Name is the validated registered event name.
func (e InboundEvent) Name() string { return e.name }

// FragmentID is the validated fragment the event targets.
func (e InboundEvent) FragmentID() string { return e.fragmentID }

// SeenServerSeq is the latest server sequence the client had observed.
func (e InboundEvent) SeenServerSeq() uint64 { return e.seenServerSeq }

// Fields returns a copy of the event's validated form fields.
func (e InboundEvent) Fields() []EventField { return append([]EventField(nil), e.fields...) }

// InboundAck reports the highest contiguous server_seq the client has applied.
type InboundAck struct {
	inboundBase
	serverSeq uint64
}

// ServerSeq is the validated cumulative high-water mark.
func (a InboundAck) ServerSeq() uint64 { return a.serverSeq }

// InboundHeartbeat is the liveness signal.
type InboundHeartbeat struct {
	inboundBase
}

// InboundClientTelemetry reports how long the browser took to apply a patch.
// Every field of it is untrusted input.
type InboundClientTelemetry struct {
	inboundBase
	patchID     uint64
	morphMicros uint32
	applyMicros uint32
}

// PatchID is the validated patch identifier the telemetry describes.
func (t InboundClientTelemetry) PatchID() uint64 { return t.patchID }

// MorphMicros is the validated client-side DOM morph duration.
func (t InboundClientTelemetry) MorphMicros() uint32 { return t.morphMicros }

// ApplyMicros is the validated total client-side patch application duration.
func (t InboundClientTelemetry) ApplyMicros() uint32 { return t.applyMicros }

// InboundResyncRequest asks for a full re-render. It is the one client frame
// that reaches the actor and triggers work proportional to the whole state, so
// it is rate limited in a bucket of its own.
type InboundResyncRequest struct {
	inboundBase
	lastAppliedSeq uint64
	reason         pb.ResyncReason
}

// LastAppliedSeq is the validated last contiguous sequence the client applied.
func (r InboundResyncRequest) LastAppliedSeq() uint64 { return r.lastAppliedSeq }

// Reason is the validated reason the client requested a full snapshot.
func (r InboundResyncRequest) Reason() pb.ResyncReason { return r.reason }

// ParseInbound is the sole entry point for bytes arriving from a client. There
// is no exported way to obtain an inbound payload that has not passed through
// it.
//
// It runs, in order: unmarshal into the generated frame, which is also where
// protobuf's own UTF-8 validation of string fields happens; the envelope
// refinement boundary; the version compatibility check; the payload's own
// refinement, per element for repeated messages; the enum domain walk; and the
// list cardinality walk. A new payload kind cannot skip a step, because the
// switch below is the only way to reach a payload and a conformance test walks
// the oneof descriptor to assert every member has a case here.
//
// Errors are always *RejectError, so the caller has the metric label, the
// reply code and the close code without re-deriving any of them.
func ParseInbound(b []byte, limits Limits) (IInbound, error) {
	if limits.MaxInboundFrameBytes > 0 && len(b) > limits.MaxInboundFrameBytes {
		// The connection's read limit is the authoritative enforcement of H-5
		// and has already refused this frame before allocating it. This is the
		// belt-and-braces re-check, and it is here so that a caller who forgot
		// to set the read limit still cannot hand an unbounded frame onward.
		return nil, reject(ReasonOversize, pb.ErrorCode_INVALID_FRAME, CloseFrameTooLarge, nil,
			"frame of %d bytes exceeds the %d byte inbound limit: lower the payload or raise Limits.MaxInboundFrameBytes",
			len(b), limits.MaxInboundFrameBytes)
	}

	var raw pb.Frame
	if err := proto.Unmarshal(b, &raw); err != nil {
		return nil, reject(ReasonRefineFailed, pb.ErrorCode_INVALID_FRAME, CloseProtocolViolation, err,
			"payload is not an encoded gotthlive.v1.Frame: send only binary frames carrying one encoded Frame")
	}

	if err := pb.ValidateFrame(&raw); err != nil {
		return nil, reject(ReasonRefineFailed, pb.ErrorCode_INVALID_FRAME, CloseProtocolViolation, err,
			"frame envelope violates its schema: correct the offending field")
	}
	frame := proto.Clone(&raw).(*pb.Frame)

	if err := checkVersion(frame.ProtocolVersion); err != nil {
		return nil, err
	}

	kind := KindOf(&raw)
	if !kind.ClientToServer() {
		return nil, reject(ReasonUnknownKind, pb.ErrorCode_INVALID_FRAME, CloseProtocolViolation, nil,
			"payload kind %q is server-to-client only: a client may send event, ack, heartbeat, client_telemetry or resync_request",
			kind)
	}

	// H-1 and H-4 in one walk rather than two, which is a traversal of every
	// inbound frame removed from the parse path. The reason names which of the
	// two failed, because the client is answered differently.
	if reason, err := checkFieldInvariants(raw.ProtoReflect()); err != nil {
		if reason == ReasonEnumDomain {
			return nil, reject(reason, pb.ErrorCode_INVALID_FRAME, CloseProtocolViolation, err,
				"enum field is outside its declared domain: send a declared, non-zero value")
		}
		return nil, reject(reason, pb.ErrorCode_INVALID_FRAME, CloseProtocolViolation, err,
			"repeated field exceeds its cardinality bound: send fewer elements")
	}

	base := inboundBase{frame: frame, kind: kind}
	copy(base.sessionID[:], raw.SessionId)

	switch p := raw.GetPayload().(type) {
	case *pb.Frame_Event:
		if err := pb.ValidateEvent(p.Event); err != nil {
			return nil, reject(ReasonRefineFailed, pb.ErrorCode_INVALID_FRAME, CloseNone, err,
				"event violates its schema: correct the offending field")
		}
		fields := make([]EventField, 0, len(p.Event.Fields))
		for _, f := range p.Event.Fields {
			if err := pb.ValidateEventField(f); err != nil {
				return nil, reject(ReasonRefineFailed, pb.ErrorCode_INVALID_FRAME, CloseNone, err,
					"event field violates its schema: correct the offending key or value")
			}
			fields = append(fields, EventField{Key: f.Key, Value: f.Value})
		}
		return InboundEvent{
			inboundBase:   base,
			clientRef:     p.Event.ClientRef,
			name:          p.Event.Name,
			fragmentID:    p.Event.FragmentId,
			seenServerSeq: p.Event.SeenServerSeq,
			fields:        fields,
		}, nil

	case *pb.Frame_Ack:
		if err := pb.ValidateAck(p.Ack); err != nil {
			return nil, reject(ReasonRefineFailed, pb.ErrorCode_INVALID_FRAME, CloseNone, err,
				"ack violates its schema: server_seq must be positive")
		}
		return InboundAck{inboundBase: base, serverSeq: p.Ack.ServerSeq}, nil

	case *pb.Frame_Heartbeat:
		if err := pb.ValidateHeartbeat(p.Heartbeat); err != nil {
			return nil, reject(ReasonRefineFailed, pb.ErrorCode_INVALID_FRAME, CloseNone, err,
				"heartbeat violates its schema: echo the server's nonce and interval_ms verbatim")
		}
		return InboundHeartbeat{inboundBase: base}, nil

	case *pb.Frame_ClientTelemetry:
		if err := pb.ValidateClientTelemetry(p.ClientTelemetry); err != nil {
			return nil, reject(ReasonRefineFailed, pb.ErrorCode_INVALID_FRAME, CloseNone, err,
				"client telemetry violates its schema: report a real patch_id and durations under 60 seconds")
		}
		return InboundClientTelemetry{
			inboundBase: base,
			patchID:     p.ClientTelemetry.PatchId,
			morphMicros: p.ClientTelemetry.MorphMicros,
			applyMicros: p.ClientTelemetry.ApplyMicros,
		}, nil

	case *pb.Frame_ResyncRequest:
		if err := pb.ValidateResyncRequest(p.ResyncRequest); err != nil {
			return nil, reject(ReasonRefineFailed, pb.ErrorCode_INVALID_FRAME, CloseNone, err,
				"resync request violates its schema: last_applied_seq must be positive")
		}
		return InboundResyncRequest{
			inboundBase:    base,
			lastAppliedSeq: p.ResyncRequest.LastAppliedSeq,
			reason:         p.ResyncRequest.Reason,
		}, nil

	default:
		// Unreachable today, and deliberately kept. A frame carrying no
		// payload, or one carrying a payload this build cannot name, is
		// already refused by the ClientToServer guard above, because KindOf
		// reports both as unknown — that is the route the specs pin.
		//
		// What this arm covers is a future edit: a new client-sendable kind
		// added to the kind table but not to this switch. Falling through to
		// "accepted and ignored" is the one outcome that must not be
		// available, so the arm refuses instead. The conformance walk over the
		// payload oneof is what makes the omission fail at build time rather
		// than waiting for this arm to be hit in production.
		return nil, reject(ReasonUnknownKind, pb.ErrorCode_INVALID_FRAME, CloseProtocolViolation, nil,
			"payload kind %q has no parse case: this is a library bug, not a client problem", kind)
	}
}

// checkVersion is H-2: the peer's major version must match this build's.
//
// Version 1 has no minor component on the wire, so the comparison is equality;
// when a minor arrives it is compared on the major alone, which is the whole
// point of separating this from the field predicate. A mismatch is never
// resolved by silently reinterpreting fields.
func checkVersion(v uint32) error {
	if v == Version {
		return nil
	}
	return reject(ReasonBadVersion, pb.ErrorCode_UNSUPPORTED_VERSION, CloseUnsupportedVersion, nil,
		"protocol version %d is not supported by this server, which speaks version %d: upgrade the client runtime",
		v, Version)
}

// CheckSessionID is H-3: a frame's session_id must equal the session bound to
// the connection it arrived on.
//
// It is a separate call rather than a ParseInbound step because the expected
// value is transport state, and the transport ingress is the only place that
// holds it. A mismatch is a protocol violation and closes the connection: a
// client that names another session is not confused, it is probing.
func CheckSessionID(in IInbound, want [16]byte) error {
	got := in.sessionIDValue()
	if got == want {
		return nil
	}
	return reject(ReasonSessionMismatch, pb.ErrorCode_INVALID_FRAME, CloseProtocolViolation, nil,
		"frame names a session that is not the one bound to this connection: send the session_id from the first Snapshot")
}
