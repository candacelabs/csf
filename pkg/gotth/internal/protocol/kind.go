package protocol

import (
	"google.golang.org/protobuf/reflect/protoreflect"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// Kind names one member of the Frame payload oneof. It is the domain of the
// kind label on gotthlive_frames_received_total and _sent_total.
type Kind int

// The eight kinds. KindUnknown is not a member of the oneof: it is what a
// frame carrying no payload, or a payload from a newer schema, parses as, and
// it is always a rejection rather than a silent pass.
const (
	KindUnknown Kind = iota
	KindEvent
	KindAck
	KindHeartbeat
	KindClientTelemetry
	KindResyncRequest
	KindPatch
	KindSnapshot
	KindError
)

var kindNames = map[Kind]string{
	KindUnknown:         "unknown",
	KindEvent:           "event",
	KindAck:             "ack",
	KindHeartbeat:       "heartbeat",
	KindClientTelemetry: "client_telemetry",
	KindResyncRequest:   "resync_request",
	KindPatch:           "patch",
	KindSnapshot:        "snapshot",
	KindError:           "error",
}

// String returns the metric label value for k.
func (k Kind) String() string {
	if s, ok := kindNames[k]; ok {
		return s
	}
	return "unknown"
}

// ClientToServer reports whether a client may send this kind. A server-only
// kind arriving from a client is a protocol violation, not a curiosity.
func (k Kind) ClientToServer() bool {
	switch k {
	case KindEvent, KindAck, KindHeartbeat, KindClientTelemetry, KindResyncRequest:
		return true
	default:
		return false
	}
}

// kindByField maps a oneof member's proto field name to its Kind. It is keyed
// by descriptor name so that the conformance walk over the payload oneof can
// assert every member is named here — a new member with no entry fails the
// test rather than parsing as unknown at runtime.
var kindByField = map[protoreflect.Name]Kind{
	"event":            KindEvent,
	"ack":              KindAck,
	"heartbeat":        KindHeartbeat,
	"client_telemetry": KindClientTelemetry,
	"resync_request":   KindResyncRequest,
	"patch":            KindPatch,
	"snapshot":         KindSnapshot,
	"error":            KindError,
}

// KindByField returns the Kind of a payload oneof member by its proto field
// name, and whether one is declared. Exported for the conformance test.
func KindByField(name protoreflect.Name) (Kind, bool) {
	k, ok := kindByField[name]
	return k, ok
}

// KindOf reports which payload a frame carries.
func KindOf(f *pb.Frame) Kind {
	switch f.GetPayload().(type) {
	case *pb.Frame_Event:
		return KindEvent
	case *pb.Frame_Ack:
		return KindAck
	case *pb.Frame_Heartbeat:
		return KindHeartbeat
	case *pb.Frame_ClientTelemetry:
		return KindClientTelemetry
	case *pb.Frame_ResyncRequest:
		return KindResyncRequest
	case *pb.Frame_Patch:
		return KindPatch
	case *pb.Frame_Snapshot:
		return KindSnapshot
	case *pb.Frame_Error:
		return KindError
	default:
		return KindUnknown
	}
}
