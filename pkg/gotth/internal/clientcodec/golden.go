package clientcodec

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// Vector is one cross-runtime round-trip case.
//
// Hex is what the Go protobuf runtime produced for Frame. The JS test decodes
// it and asserts the result equals Frame, which checks Go→JS; when Reencode is
// set it also encodes Frame and asserts the bytes come back byte-identical to
// Hex, which checks JS→Go — byte equality with the canonical Go encoding is a
// stronger statement than "Go can parse it", because it also rules out a
// client that is merely tolerantly parseable.
type Vector struct {
	// Name identifies the case in the JS test's failure output.
	Name string `json:"name"`

	// Note says what the case is for, so a failure names the property rather
	// than only the vector.
	Note string `json:"note"`

	// Hex is the canonical Go encoding of Frame, hex-encoded because the
	// fixture is JSON.
	Hex string `json:"hex"`

	// Frame is the expected decoded value in JavaScript shape, read back
	// through protoreflect rather than written by hand so that it omits
	// exactly the fields proto3 omits on the wire.
	Frame map[string]any `json:"frame"`

	// Reencode asks the JS test for the harder half: encode Frame and require
	// the bytes to equal Hex. A vector without it checks only that the client
	// can read what Go writes.
	Reencode bool `json:"reencode"`
}

// EmitGolden renders client/test/golden.json.
//
// The frames below are built with the Go runtime, marshalled, and then read
// back through protoreflect to produce the expected JavaScript-shaped value.
// Reading it back rather than writing it by hand is deliberate: the expected
// object then omits exactly the fields proto3 omits on the wire, so a
// disagreement about default-value handling shows up as a test failure instead
// of being encoded into the fixture by whoever wrote it.
func EmitGolden() ([]byte, error) {
	var vectors []Vector

	for _, c := range cases() {
		raw, err := proto.Marshal(c.frame)
		if err != nil {
			return nil, fmt.Errorf("marshalling %s: %w", c.name, err)
		}
		if c.extra != nil {
			raw = append(raw, c.extra...)
		}

		var back pb.Frame
		if err := proto.Unmarshal(raw, &back); err != nil {
			return nil, fmt.Errorf("re-reading %s: %w", c.name, err)
		}

		vectors = append(vectors, Vector{
			Name:     c.name,
			Note:     c.note,
			Hex:      hex.EncodeToString(raw),
			Frame:    jsMessage(c.frame.ProtoReflect()),
			Reencode: c.extra == nil,
		})
	}

	out, err := json.MarshalIndent(vectors, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

type goldenCase struct {
	name  string
	note  string
	frame *pb.Frame
	// extra is appended raw after the marshalled frame, to build unknown-tag
	// cases. A case with extra bytes is decode-only: re-encoding it cannot
	// reproduce fields the client deliberately did not retain.
	extra []byte
}

// sessionID is fixed rather than random so the vectors are reproducible.
func sessionID() []byte {
	b := make([]byte, 16)
	for i := range b {
		b[i] = byte(0xa0 + i)
	}
	return b
}

func envelope(p any) *pb.Frame {
	f := &pb.Frame{ProtocolVersion: 1, SessionId: sessionID()}
	switch v := p.(type) {
	case *pb.Event:
		f.Payload = &pb.Frame_Event{Event: v}
	case *pb.Ack:
		f.Payload = &pb.Frame_Ack{Ack: v}
	case *pb.Heartbeat:
		f.Payload = &pb.Frame_Heartbeat{Heartbeat: v}
	case *pb.ClientTelemetry:
		f.Payload = &pb.Frame_ClientTelemetry{ClientTelemetry: v}
	case *pb.ResyncRequest:
		f.Payload = &pb.Frame_ResyncRequest{ResyncRequest: v}
	case *pb.Patch:
		f.Payload = &pb.Frame_Patch{Patch: v}
	case *pb.Snapshot:
		f.Payload = &pb.Frame_Snapshot{Snapshot: v}
	case *pb.Error:
		f.Payload = &pb.Frame_Error{Error: v}
	default:
		panic(fmt.Sprintf("envelope: unhandled payload %T", p))
	}
	return f
}

func cases() []goldenCase {
	return []goldenCase{
		{
			name: "snapshot_first",
			note: "the first frame on every connection: session parameters plus every fragment",
			frame: envelope(&pb.Snapshot{
				ServerSeq: 1, PatchId: 1, TransitionId: 1, StateVersion: 1,
				Origin: &pb.Origin{
					Kind:   pb.OriginKind_MOUNT,
					Source: "mount",
				},
				HeartbeatIntervalMs:  20000,
				MaxInboundFrameBytes: 65536,
				AckWindow:            16,
				Updates: []*pb.FragmentUpdate{
					{FragmentId: "counter", Op: pb.PatchOp_MORPH, Html: `<div data-gotth-region="counter"><b id="n">0</b></div>`},
					{FragmentId: "list", Op: pb.PatchOp_MORPH, Html: `<ul data-gotth-region="list"></ul>`},
				},
			}),
		},
		{
			name: "snapshot_resync",
			note: "a resync snapshot: RESYNC origin carrying the request's causal ids, plus the supersession edge (H-13)",
			frame: envelope(&pb.Snapshot{
				ServerSeq: 41, PatchId: 40, TransitionId: 39, StateVersion: 22,
				Origin: &pb.Origin{
					Kind:      pb.OriginKind_RESYNC,
					EventId:   17,
					ClientRef: 9,
					Source:    "resync",
				},
				HeartbeatIntervalMs:  20000,
				MaxInboundFrameBytes: 65536,
				AckWindow:            16,
				Updates: []*pb.FragmentUpdate{
					{FragmentId: "counter", Op: pb.PatchOp_MORPH, Html: `<div data-gotth-region="counter"><b id="n">7</b></div>`},
				},
				SupersededFromSeq:    33,
				SupersededThroughSeq: 40,
			}),
		},
		{
			name: "patch_client_event",
			note: "the ordinary case: one fragment, CLIENT_EVENT origin with both causal ids",
			frame: envelope(&pb.Patch{
				ServerSeq: 2, PatchId: 2, TransitionId: 2, StateVersion: 2,
				Origin: &pb.Origin{
					Kind:      pb.OriginKind_CLIENT_EVENT,
					EventId:   1,
					ClientRef: 1,
					Source:    "event:increment",
				},
				Updates: []*pb.FragmentUpdate{
					{FragmentId: "counter", Op: pb.PatchOp_MORPH, Html: `<div data-gotth-region="counter"><b id="n">1</b></div>`},
				},
			}),
		},
		{
			name: "patch_effect_coalesced",
			note: "a server-initiated patch: event_id 0 by design, provenance carried by contributing_event_ids (packed repeated uint64)",
			frame: envelope(&pb.Patch{
				ServerSeq: 3, PatchId: 3, TransitionId: 4, StateVersion: 3,
				Origin: &pb.Origin{
					Kind:                 pb.OriginKind_EFFECT,
					Source:               "effect:chat.broadcast",
					ContributingEventIds: []uint64{2, 3, 5, 8, 13, 21, 4294967296},
				},
				Updates: []*pb.FragmentUpdate{
					{FragmentId: "list", Op: pb.PatchOp_APPEND, Html: `<li>café — 世界</li>`},
					{FragmentId: "counter", Op: pb.PatchOp_MORPH, Html: `<div data-gotth-region="counter"><b id="n">` + strings.Repeat("9", 200) + `</b></div>`},
				},
			}),
		},
		{
			name: "patch_remove",
			note: "the REMOVE op, and a fragment update with no html at all",
			frame: envelope(&pb.Patch{
				ServerSeq: 4, PatchId: 4, TransitionId: 5, StateVersion: 4,
				Origin: &pb.Origin{
					Kind:   pb.OriginKind_TIMER,
					Source: "timer:dashboard.tick",
				},
				Updates: []*pb.FragmentUpdate{
					{FragmentId: "banner", Op: pb.PatchOp_REMOVE},
				},
			}),
		},
		{
			name:  "error_fatal",
			note:  "a fatal error frame, event-scoped (H-12)",
			frame: envelope(&pb.Error{Code: pb.ErrorCode_UNKNOWN_EVENT, Message: "unregistered event", EventId: 12, ClientRef: 7, Fatal: true}),
		},
		{
			name:  "error_not_event_scoped",
			note:  "the same frame without causal ids and without fatal: proto3 omits all three, so the decoder must leave them unset",
			frame: envelope(&pb.Error{Code: pb.ErrorCode_RATE_LIMITED, Message: "slow down"}),
		},
		{
			name:  "heartbeat",
			note:  "server to client, and byte-identical to the echo the client sends back (docs/protocol.md §3.4)",
			frame: envelope(&pb.Heartbeat{Nonce: 1234567890123, IntervalMs: 20000}),
		},
		{
			name: "event_form",
			note: "a form submission: repeated EventField, and a client_ref past 2^32 to exercise multi-byte varints",
			frame: envelope(&pb.Event{
				ClientRef: 4294967297, Name: "submit.message", FragmentId: "composer", SeenServerSeq: 41,
				Fields: []*pb.EventField{
					{Key: "body", Value: "hello — 世界"},
					{Key: "tags[0]", Value: "a"},
					{Key: "empty", Value: ""},
				},
			}),
		},
		{
			name:  "event_bare",
			note:  "a click with no form around it: no fields at all",
			frame: envelope(&pb.Event{ClientRef: 1, Name: "increment", FragmentId: "counter", SeenServerSeq: 1}),
		},
		{
			name:  "ack",
			note:  "the cumulative high-water mark (RFC-0001 §7.1)",
			frame: envelope(&pb.Ack{ServerSeq: 41}),
		},
		{
			name:  "resync_request",
			note:  "sequence-gap detection, the one client frame that triggers a full re-render",
			frame: envelope(&pb.ResyncRequest{LastAppliedSeq: 32, Reason: pb.ResyncReason_GAP}),
		},
		{
			name:  "client_telemetry",
			note:  "FR-29 morph timing, correlated by patch_id",
			frame: envelope(&pb.ClientTelemetry{PatchId: 2, MorphMicros: 431, ApplyMicros: 1204}),
		},
		{
			name:  "unknown_tags",
			note:  "FR-10: a newer server adds fields. The decoder must skip every wire type and never throw.",
			frame: envelope(&pb.Ack{ServerSeq: 7}),
			// Field 63 varint, field 62 length-delimited, field 61 fixed32,
			// field 60 fixed64 — one of each skippable wire type.
			extra: []byte{
				0xf8, 0x03, 0x96, 0x01,
				0xf2, 0x03, 0x03, 'n', 'e', 'w',
				0xed, 0x03, 0x01, 0x02, 0x03, 0x04,
				0xe1, 0x03, 1, 2, 3, 4, 5, 6, 7, 8,
			},
		},
	}
}

// jsMessage renders a message the way the generated JavaScript decoder
// renders it: only populated fields, snake_case keys, numbers as numbers, and
// bytes as an array of byte values.
func jsMessage(m protoreflect.Message) map[string]any {
	out := map[string]any{}
	m.Range(func(fd protoreflect.FieldDescriptor, v protoreflect.Value) bool {
		out[string(fd.Name())] = jsField(fd, v)
		return true
	})
	return out
}

func jsField(fd protoreflect.FieldDescriptor, v protoreflect.Value) any {
	if fd.IsList() {
		l := v.List()
		arr := make([]any, 0, l.Len())
		for i := 0; i < l.Len(); i++ {
			arr = append(arr, jsScalar(fd, l.Get(i)))
		}
		return arr
	}
	return jsScalar(fd, v)
}

func jsScalar(fd protoreflect.FieldDescriptor, v protoreflect.Value) any {
	switch fd.Kind() {
	case protoreflect.MessageKind:
		return jsMessage(v.Message())
	case protoreflect.BoolKind:
		return v.Bool()
	case protoreflect.StringKind:
		return v.String()
	case protoreflect.BytesKind:
		b := v.Bytes()
		a := make([]int, len(b))
		for i, c := range b {
			a[i] = int(c)
		}
		return a
	case protoreflect.EnumKind:
		return int64(v.Enum())
	case protoreflect.Uint32Kind, protoreflect.Uint64Kind:
		return v.Uint()
	case protoreflect.Int32Kind, protoreflect.Int64Kind:
		return v.Int()
	}
	panic(fmt.Sprintf("jsScalar: unhandled kind %s", fd.Kind()))
}
