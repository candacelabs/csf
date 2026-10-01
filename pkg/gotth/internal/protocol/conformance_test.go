package protocol_test

import (
	"errors"
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// The conformance walk. These specs are driven by the descriptors rather than
// by a list someone maintains, which is the whole point: the failure mode they
// exist to prevent is a new frame kind that quietly reaches application code
// without crossing the boundary every other kind crosses.

func payloadMembers() []protoreflect.FieldDescriptor {
	oneof := protocol.FrameDescriptor().Oneofs().ByName("payload")
	Expect(oneof).NotTo(BeNil(), "the Frame message has no payload oneof")

	out := make([]protoreflect.FieldDescriptor, 0, oneof.Fields().Len())
	for i := 0; i < oneof.Fields().Len(); i++ {
		out = append(out, oneof.Fields().Get(i))
	}
	Expect(out).NotTo(BeEmpty())
	return out
}

// zeroPayloadFrame builds a frame carrying member with a payload whose enum
// fields are declared-but-arbitrary and whose scalars are all zero. Every
// client-to-server payload declares at least one positive-value predicate, so
// such a frame is well-formed protobuf that must be refused by the payload's
// own refinement — which is exactly the step this walk is checking runs.
func zeroPayloadFrame(member protoreflect.FieldDescriptor) []byte {
	frame := envelope().ProtoReflect()
	msg := frame.NewField(member).Message()

	fields := member.Message().Fields()
	for i := 0; i < fields.Len(); i++ {
		fd := fields.Get(i)
		if fd.Kind() == protoreflect.EnumKind && !fd.IsList() {
			msg.Set(fd, protoreflect.ValueOfEnum(fd.Enum().Values().Get(1).Number()))
		}
	}
	frame.Set(member, protoreflect.ValueOfMessage(msg))

	b, err := proto.Marshal(frame.Interface())
	Expect(err).NotTo(HaveOccurred())
	return b
}

var _ = Describe("Every member of the payload oneof", func() {

	It("is named in the kind table", func() {
		for _, fd := range payloadMembers() {
			_, ok := protocol.KindByField(fd.Name())
			Expect(ok).To(BeTrue(),
				"payload member %q has no Kind: a new frame kind must be added to the kind table", fd.Name())
		}
	})

	It("is either exercised by a client corpus entry or is server-to-client only", func() {
		for _, fd := range payloadMembers() {
			kind, _ := protocol.KindByField(fd.Name())
			_, inCorpus := validClientPayloads[fd.Name()]
			Expect(inCorpus).To(Equal(kind.ClientToServer()),
				"payload member %q: client-to-server is %v but a valid-client-frame corpus entry is %v",
				fd.Name(), kind.ClientToServer(), inCorpus)
		}
	})

	It("routes a client-sendable payload through its own validation boundary", func() {
		for _, fd := range payloadMembers() {
			kind, _ := protocol.KindByField(fd.Name())
			if !kind.ClientToServer() {
				continue
			}

			_, err := protocol.ParseInbound(zeroPayloadFrame(fd), protocol.DefaultLimits())
			Expect(err).To(HaveOccurred(),
				"payload member %q: an all-zero payload was accepted, so its Validate function is not being called", fd.Name())

			var rejected *protocol.RejectError
			Expect(errors.As(err, &rejected)).To(BeTrue(), "member %q: %v", fd.Name(), err)
			Expect(rejected.Reason).To(Equal(protocol.ReasonRefineFailed),
				"payload member %q was rejected for %q rather than by its refinement boundary",
				fd.Name(), rejected.Reason)
		}
	})

	It("refuses a server-to-client payload arriving from a client", func() {
		for _, fd := range payloadMembers() {
			kind, _ := protocol.KindByField(fd.Name())
			if kind.ClientToServer() {
				continue
			}

			b, err := proto.Marshal(serverFrame(fd.Name()))
			Expect(err).NotTo(HaveOccurred())

			_, err = protocol.ParseInbound(b, protocol.DefaultLimits())
			Expect(err).To(HaveOccurred(), "member %q was accepted from a client", fd.Name())

			var rejected *protocol.RejectError
			Expect(errors.As(err, &rejected)).To(BeTrue())
			Expect(rejected.Reason).To(Equal(protocol.ReasonUnknownKind))
			Expect(rejected.Close).To(Equal(protocol.CloseProtocolViolation))
		}
	})

	It("parses a well-formed client frame into the variant that names it", func() {
		for member, build := range validClientPayloads {
			f := envelope()
			build(f)
			b, err := proto.Marshal(f)
			Expect(err).NotTo(HaveOccurred())

			in, err := protocol.ParseInbound(b, protocol.DefaultLimits())
			Expect(err).NotTo(HaveOccurred(), "member %q: %v", member, err)

			wantKind, _ := protocol.KindByField(member)
			Expect(in.Kind()).To(Equal(wantKind))
			Expect(protocol.CheckSessionID(in, sessionKey())).To(Succeed())
		}
	})
})

var _ = Describe("Every repeated field in the schema", func() {

	// H-4 is one of the two invariants a new field silently escapes, so the
	// bound is a table and this is the meta-test that keeps the table complete.
	It("has a declared cardinality bound", func() {
		var walk func(message protoreflect.MessageDescriptor, seen map[protoreflect.FullName]bool)
		walk = func(md protoreflect.MessageDescriptor, seen map[protoreflect.FullName]bool) {
			if seen[md.FullName()] {
				return
			}
			seen[md.FullName()] = true

			for i := 0; i < md.Fields().Len(); i++ {
				fd := md.Fields().Get(i)
				Expect(fd.IsMap()).To(BeFalse(),
					"%s is a map field: maps cannot be refined and this schema declares none", fd.FullName())
				if fd.IsList() {
					_, ok := protocol.ListBound(fd.FullName())
					Expect(ok).To(BeTrue(),
						"repeated field %s has no cardinality bound: add one, an unbounded list is a memory vector",
						fd.FullName())
				}
				if fd.Kind() == protoreflect.MessageKind {
					walk(fd.Message(), seen)
				}
			}
		}
		walk(protocol.FrameDescriptor(), map[protoreflect.FullName]bool{})
	})

	It("is rejected above its bound, in both directions", func() {
		By("an inbound event with too many fields")
		f := envelope()
		ev := validEvent()
		bound, ok := protocol.ListBound("gotthlive.v1.Event.fields")
		Expect(ok).To(BeTrue())
		ev.Fields = nil
		for i := 0; i <= bound; i++ {
			ev.Fields = append(ev.Fields, &pb.EventField{Key: fmt.Sprintf("k%d", i), Value: "v"})
		}
		f.Payload = &pb.Frame_Event{Event: ev}
		b, err := proto.Marshal(f)
		Expect(err).NotTo(HaveOccurred())

		_, err = protocol.ParseInbound(b, protocol.Limits{MaxInboundFrameBytes: 1 << 20})
		var rejected *protocol.RejectError
		Expect(errors.As(err, &rejected)).To(BeTrue(), "%v", err)
		Expect(rejected.Reason).To(Equal(protocol.ReasonListBound))

		By("an outbound patch whose contributing-event union overflowed")
		out := serverFrame("patch")
		ids := make([]uint64, protocol.CoalesceFlushCeiling+1)
		for i := range ids {
			ids[i] = uint64(i + 1)
		}
		out.GetPatch().GetOrigin().ContributingEventIds = ids
		Expect(protocol.ValidateOutbound(out)).NotTo(Succeed(),
			"an over-full contributing-event list was accepted: the coalescing flush did not happen")
	})
})
