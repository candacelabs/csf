package protocol

import (
	"fmt"

	"google.golang.org/protobuf/reflect/protoreflect"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// Version is the protocol version this build speaks. A peer whose major
// version differs is refused with a reason rather than reinterpreted (H-2).
const Version uint32 = 1

// Subprotocol is the WebSocket subprotocol token offered on the upgrade. It is
// a fast reject, not the source of truth: the negotiated version is re-asserted
// in band as Frame.protocol_version and validated there.
const Subprotocol = "gotth-live.v1"

// Limits are the inbound bounds this package enforces. They are the subset of
// the library's limits that the parse boundary needs; the rest belong to the
// session actor and the transport.
//
// MaxInboundFrameBytes is the authoritative one. It is applied to the
// connection before any payload is allocated, so it bounds memory rather than
// merely rejecting after the fact; the per-field and per-list bounds are
// subordinate defence in depth that fail early with a field-specific error.
type Limits struct {
	// MaxInboundFrameBytes caps a decoded frame (H-5).
	MaxInboundFrameBytes int
}

// DefaultLimits returns the documented defaults.
func DefaultLimits() Limits {
	return Limits{MaxInboundFrameBytes: 65536}
}

// listBounds is H-4: the cardinality of every repeated field in the schema.
//
// The predicate grammar cannot refine a repeated field, so these bounds are
// hand-checked. They are a table rather than per-field code so that the
// descriptor-walk test can fail when the schema grows a list this table does
// not name.
//
// The bound on contributing_event_ids is a coalescing flush trigger, never a
// truncation. Two bounds keep it unreachable, and both are enforced before a
// frame is built: session.MaxCoalesceFlushAt caps what an operator may
// configure the flush at, and session.MaxEventContributing caps what an
// application may put on one emitted event. The default flush is at half this
// ceiling, but the default is not the bound — describing this in terms of the
// default was true until D-14 gave the setting a validated range, and it is the
// range that keeps the invariant.
//
// So reaching it here means a frame was built from a set neither bound
// admitted, on the server side, from server-owned state. It is not a client's
// doing and it is not an application's either: the emit path refuses an
// over-long Contributing with an error naming the caller.
var listBounds = map[protoreflect.FullName]int{
	"gotthlive.v1.Event.fields":                  64,
	"gotthlive.v1.Patch.updates":                 64,
	"gotthlive.v1.Snapshot.updates":              64,
	"gotthlive.v1.Origin.contributing_event_ids": CoalesceFlushCeiling,
}

// CoalesceFlushCeiling is the H-4 bound on Origin.contributing_event_ids. The
// actor's own flush threshold defaults to half of it and is validated against
// it; this is the schema-level assertion that the flush worked.
const CoalesceFlushCeiling = 1024

// ListBound reports the H-4 bound declared for a repeated field, and whether
// one is declared at all. It is exported for the descriptor-walk test that
// holds the table complete.
func ListBound(name protoreflect.FullName) (int, bool) {
	n, ok := listBounds[name]
	return n, ok
}

// checkListBound is H-4's leaf: one repeated field against its declared
// cardinality bound. The traversal that reaches it is checkFieldInvariants.
//
// A repeated field with no entry in the table is itself a violation: an
// unbounded list is how a hand-checked invariant rots when the schema grows.
func checkListBound(fd protoreflect.FieldDescriptor, list protoreflect.List) error {
	bound, ok := listBounds[fd.FullName()]
	if !ok {
		return fmt.Errorf("%s: repeated field has no H-4 cardinality bound", fd.FullName())
	}
	if list.Len() > bound {
		return fmt.Errorf("%s: %d elements exceeds the bound of %d",
			fd.FullName(), list.Len(), bound)
	}
	return nil
}

// FrameDescriptor returns the descriptor of the one message on the wire. The
// conformance tests walk it rather than a hand-maintained list of kinds.
func FrameDescriptor() protoreflect.MessageDescriptor {
	return (&pb.Frame{}).ProtoReflect().Descriptor()
}
