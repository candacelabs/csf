package protocol_test

import (
	"google.golang.org/protobuf/reflect/protoreflect"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// The corpus these specs are built from. Every helper produces a frame that is
// valid in every respect, so a spec can name exactly one thing it breaks and
// the failure it asserts cannot be coming from somewhere else.

func sessionKey() [16]byte {
	var id [16]byte
	copy(id[:], "0123456789abcdef")
	return id
}

func sessionBytes() []byte {
	id := sessionKey()
	return id[:]
}

func envelope() *pb.Frame {
	return &pb.Frame{ProtocolVersion: 1, SessionId: sessionBytes()}
}

func validEvent() *pb.Event {
	return &pb.Event{
		ClientRef:     7,
		Name:          "counter.increment",
		FragmentId:    "counter",
		SeenServerSeq: 1,
		Fields:        []*pb.EventField{{Key: "amount", Value: "1"}},
	}
}

func validOrigin() *pb.Origin {
	return &pb.Origin{
		Kind:      pb.OriginKind_CLIENT_EVENT,
		EventId:   1,
		ClientRef: 7,
		Source:    "event:counter.increment",
	}
}

func validPatch() *pb.Patch {
	return &pb.Patch{
		ServerSeq:    2,
		PatchId:      2,
		TransitionId: 2,
		StateVersion: 2,
		Origin:       validOrigin(),
		Updates: []*pb.FragmentUpdate{
			{FragmentId: "counter", Op: pb.PatchOp_MORPH, Html: "<div>1</div>"},
		},
	}
}

func validSnapshot() *pb.Snapshot {
	return &pb.Snapshot{
		ServerSeq:            1,
		PatchId:              1,
		TransitionId:         1,
		StateVersion:         1,
		Origin:               &pb.Origin{Kind: pb.OriginKind_MOUNT, Source: "mount"},
		HeartbeatIntervalMs:  20000,
		MaxInboundFrameBytes: 65536,
		AckWindow:            16,
		Updates: []*pb.FragmentUpdate{
			{FragmentId: "counter", Op: pb.PatchOp_MORPH, Html: "<div>0</div>"},
		},
	}
}

// validClientPayloads builds one well-formed payload per client-to-server
// member of the oneof. The conformance walk asserts this table names every
// such member, so a new client frame kind cannot be added without a spec that
// exercises it.
var validClientPayloads = map[protoreflect.Name]func(frame *pb.Frame){
	"event": func(f *pb.Frame) { f.Payload = &pb.Frame_Event{Event: validEvent()} },
	"ack":   func(f *pb.Frame) { f.Payload = &pb.Frame_Ack{Ack: &pb.Ack{ServerSeq: 3}} },
	"heartbeat": func(f *pb.Frame) {
		f.Payload = &pb.Frame_Heartbeat{Heartbeat: &pb.Heartbeat{Nonce: 9, IntervalMs: 20000}}
	},
	"client_telemetry": func(f *pb.Frame) {
		f.Payload = &pb.Frame_ClientTelemetry{ClientTelemetry: &pb.ClientTelemetry{
			PatchId: 2, MorphMicros: 1200, ApplyMicros: 900,
		}}
	},
	"resync_request": func(f *pb.Frame) {
		f.Payload = &pb.Frame_ResyncRequest{ResyncRequest: &pb.ResyncRequest{
			LastAppliedSeq: 1, Reason: pb.ResyncReason_GAP,
		}}
	},
}

// validServerPayloads is the same for the server-to-client members, and is
// what the outbound validation specs are built on.
var validServerPayloads = map[protoreflect.Name]func(frame *pb.Frame){
	"patch":    func(f *pb.Frame) { f.Payload = &pb.Frame_Patch{Patch: validPatch()} },
	"snapshot": func(f *pb.Frame) { f.Payload = &pb.Frame_Snapshot{Snapshot: validSnapshot()} },
	"error": func(f *pb.Frame) {
		f.Payload = &pb.Frame_Error{Error: &pb.Error{
			Code: pb.ErrorCode_RATE_LIMITED, Message: "slow down",
		}}
	},
	"heartbeat": func(f *pb.Frame) {
		f.Payload = &pb.Frame_Heartbeat{Heartbeat: &pb.Heartbeat{Nonce: 9, IntervalMs: 20000}}
	},
	"ack": func(f *pb.Frame) { f.Payload = &pb.Frame_Ack{Ack: &pb.Ack{ServerSeq: 4}} },
}

func clientFrame(member protoreflect.Name) *pb.Frame {
	f := envelope()
	validClientPayloads[member](f)
	return f
}

func serverFrame(member protoreflect.Name) *pb.Frame {
	f := envelope()
	validServerPayloads[member](f)
	return f
}
