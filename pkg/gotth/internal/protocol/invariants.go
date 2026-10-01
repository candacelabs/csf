package protocol

import (
	"fmt"

	"google.golang.org/protobuf/reflect/protoreflect"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// checkFieldInvariants is H-1 and H-4, in ONE descriptor walk.
//
// Both are hand-checked invariants over every field of every message a frame
// contains, and both used to be written as their own traversal:
// checkEnums and checkListBounds iterated the same descriptor, branched on the
// same IsList and MessageKind, recursed into singular submessages behind the
// same m.Has guard and into list elements by the same index, and differed only
// in the leaf action. They are called together from both boundaries, so every
// inbound frame was walked twice on the parse path and every outbound frame
// twice on the emit path (REV-DUP D-4).
//
// It returns the frame-rejection reason naming which invariant failed, because
// ParseInbound answers the two differently and a single walk has to say which
// one it was. The reason is empty on success and ignored by ValidateOutbound,
// which reports the error and nothing else.
//
// One behavioural consequence, stated because it is a change rather than a
// refactor: with two walks, EVERY enum violation was found before ANY list
// violation. With one, the first violation in field order wins. Both are
// refusals of the same frame with the same close code, and nothing downstream
// distinguishes them beyond the metric label.
func checkFieldInvariants(m protoreflect.Message) (string, error) {
	fields := m.Descriptor().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)

		if fd.IsMap() {
			// The schema deliberately contains no map field: a map cannot be
			// refined and cannot be bounded by the H-4 table. Refused
			// structurally, before either leaf, so it is one rule rather than
			// one of the two checks happening to have it.
			return ReasonListBound, fmt.Errorf(
				"%s: map fields are not permitted in this schema", fd.FullName())
		}

		v := m.Get(fd)

		if fd.IsList() {
			// H-4 is a statement about the list, so its leaf sees the list
			// rather than each element — which is why the visitor is per field
			// and not per element.
			if err := checkListBound(fd, v.List()); err != nil {
				return ReasonListBound, err
			}
		}
		if err := checkEnumField(fd, v); err != nil {
			return ReasonEnumDomain, err
		}

		if fd.Kind() != protoreflect.MessageKind {
			continue
		}
		if fd.IsList() {
			list := v.List()
			for j := 0; j < list.Len(); j++ {
				if reason, err := checkFieldInvariants(list.Get(j).Message()); err != nil {
					return reason, err
				}
			}
			continue
		}
		// m.Has is proto3's presence guard for a singular message field.
		// Without it, Get returns a read-only empty message whose fields would
		// be walked as though they had been sent.
		if m.Has(fd) {
			if reason, err := checkFieldInvariants(v.Message()); err != nil {
				return reason, err
			}
		}
	}
	return "", nil
}

// checkEnumField is H-1's leaf: every enum field holds a value declared in its
// descriptor, and never the *_UNSPECIFIED zero.
//
// It is applied by the generic walk rather than by per-field code, because H-1
// is one of the two invariants a new field silently escapes. A future enum
// field is covered the day it is added, without anyone remembering to cover it.
//
// Presence cannot help here: proto3 does not distinguish an unset enum from the
// zero value, which is exactly why the zero value is refused.
func checkEnumField(fd protoreflect.FieldDescriptor, v protoreflect.Value) error {
	if fd.Kind() != protoreflect.EnumKind {
		return nil
	}
	if fd.IsList() {
		list := v.List()
		for j := 0; j < list.Len(); j++ {
			if err := checkEnumValue(fd, list.Get(j).Enum()); err != nil {
				return err
			}
		}
		return nil
	}
	return checkEnumValue(fd, v.Enum())
}

func checkEnumValue(fd protoreflect.FieldDescriptor, n protoreflect.EnumNumber) error {
	if n == 0 {
		return fmt.Errorf("%s: the unspecified enum value 0 is never valid on the wire", fd.FullName())
	}
	if fd.Enum().Values().ByNumber(n) == nil {
		return fmt.Errorf("%s: %d is not a declared value of %s", fd.FullName(), n, fd.Enum().FullName())
	}
	return nil
}

// eventBearing reports whether an origin kind names an inbound frame of this
// session, and so carries that frame's identifiers (H-6).
//
// CLIENT_EVENT is the obvious one. RESYNC is here because a resync snapshot is
// caused by a nameable client frame — the ResyncRequest — which is minted an
// event identifier like any other inbound event, and discarding that would
// throw away provenance we already hold. protocol.md §4.2 states this directly
// and §6's H-6 now states it in the same words.
//
// Exactly two kinds are event-bearing. A third is a protocol change that must
// move H-6, P2, P6 and this function together (protocol.md §6).
func eventBearing(kind pb.OriginKind) bool {
	return kind == pb.OriginKind_CLIENT_EVENT || kind == pb.OriginKind_RESYNC
}

// validateOrigin is H-6: event_id and client_ref are non-zero exactly when the
// origin names a specific client frame, and zero for the patches the server
// started on its own, where source names the cause instead.
func validateOrigin(o *pb.Origin) error {
	if o == nil {
		return fmt.Errorf("gotthlive.v1.Origin: a patch without an origin is an orphan patch")
	}
	want := eventBearing(o.GetKind())
	if got := o.GetEventId() != 0; got != want {
		return fmt.Errorf("gotthlive.v1.Origin: kind %s requires event_id %s, got %d",
			o.GetKind(), zeroOrNot(want), o.GetEventId())
	}
	if got := o.GetClientRef() != 0; got != want {
		return fmt.Errorf("gotthlive.v1.Origin: kind %s requires client_ref %s, got %d",
			o.GetKind(), zeroOrNot(want), o.GetClientRef())
	}
	return nil
}

func zeroOrNot(nonZero bool) string {
	if nonZero {
		return "non-zero"
	}
	return "zero"
}

// validateError is H-12: an Error's causal identifiers are both zero or both
// non-zero. They are non-zero exactly when the error concerns one event, which
// is the only situation in which the server holds them — every inbound Event
// carries a client_ref past the predicate, and the server mints the event
// identifier beside it.
func validateError(e *pb.Error) error {
	if e == nil {
		return fmt.Errorf("gotthlive.v1.Error: nil payload")
	}
	if (e.GetEventId() == 0) != (e.GetClientRef() == 0) {
		return fmt.Errorf(
			"gotthlive.v1.Error: event_id and client_ref must both be set or both be zero, got %d and %d",
			e.GetEventId(), e.GetClientRef())
	}
	return nil
}

// validateSnapshot is H-13: the supersession edge is both zero on a session's
// first snapshot, or both non-zero with from <= through < server_seq on a
// resync snapshot, and the origin kind agrees with which of the two it is.
//
// The zero case is legitimate, which is why no field predicate can carry this.
func validateSnapshot(s *pb.Snapshot) error {
	if s == nil {
		return fmt.Errorf("gotthlive.v1.Snapshot: nil payload")
	}
	from, through := s.GetSupersededFromSeq(), s.GetSupersededThroughSeq()
	resync := s.GetOrigin().GetKind() == pb.OriginKind_RESYNC

	if (from == 0) != (through == 0) {
		return fmt.Errorf(
			"gotthlive.v1.Snapshot: superseded_from_seq and superseded_through_seq must both be set or both be zero, got %d and %d",
			from, through)
	}
	if (from != 0) != resync {
		return fmt.Errorf(
			"gotthlive.v1.Snapshot: a superseded range is set exactly on a resync snapshot; kind is %s and the range is [%d, %d]",
			s.GetOrigin().GetKind(), from, through)
	}
	if from == 0 {
		return nil
	}
	if from > through {
		return fmt.Errorf("gotthlive.v1.Snapshot: superseded range [%d, %d] is empty", from, through)
	}
	if through >= s.GetServerSeq() {
		return fmt.Errorf(
			"gotthlive.v1.Snapshot: superseded range [%d, %d] must end before this snapshot's server_seq %d",
			from, through, s.GetServerSeq())
	}
	return nil
}
