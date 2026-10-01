package protocol_test

import (
	"errors"
	"math/rand"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// Hostile wire data. The corpus is the same shape the refinement plugin's own
// rejection-path tests use: not "does a good frame parse", but "does anything
// a hostile peer can put on a socket reach application code".
//
// The assertion every case shares is the one that matters: the parse boundary
// returns an error the caller can act on, and it never panics.

func parse(b []byte) (protocol.IInbound, error) {
	return protocol.ParseInbound(b, protocol.DefaultLimits())
}

func mustMarshal(m proto.Message) []byte {
	b, err := proto.Marshal(m)
	Expect(err).NotTo(HaveOccurred())
	return b
}

var _ = Describe("Hostile wire data", func() {

	DescribeTable("is refused rather than parsed",
		func(name string, payload func() []byte, wantReason string) {
			in, err := parse(payload())

			Expect(err).To(HaveOccurred(), "%s was accepted", name)
			Expect(in).To(BeNil())

			var rejected *protocol.RejectError
			Expect(errors.As(err, &rejected)).To(BeTrue(),
				"%s produced %T rather than a *RejectError, so the caller has no metric label", name, err)
			if wantReason != "" {
				Expect(rejected.Reason).To(Equal(wantReason), "%s: %v", name, err)
			}
			Expect(rejected.Error()).To(HavePrefix("gotth-live: "))
		},

		Entry("random bytes", "random bytes", func() []byte {
			r := rand.New(rand.NewSource(1))
			b := make([]byte, 64)
			for i := range b {
				b[i] = byte(r.Intn(256))
			}
			return b
		}, ""),

		Entry("empty payload", "an empty payload", func() []byte { return nil }, protocol.ReasonRefineFailed),

		Entry("a truncated frame", "a truncated frame", func() []byte {
			b := mustMarshal(clientFrame("event"))
			return b[:len(b)/2]
		}, ""),

		Entry("a frame with trailing garbage", "trailing garbage", func() []byte {
			return append(mustMarshal(clientFrame("event")), 0xff, 0xff, 0xff)
		}, ""),

		Entry("a field encoded with the wrong wire type", "a wrong wire type", func() []byte {
			// session_id is field 2, length-delimited. Send it as a varint.
			b := protowire.AppendTag(nil, 1, protowire.VarintType)
			b = protowire.AppendVarint(b, 1)
			b = protowire.AppendTag(b, 2, protowire.VarintType)
			b = protowire.AppendVarint(b, 0xdeadbeef)
			return b
		}, ""),

		Entry("a session identifier of the wrong width", "a short session id", func() []byte {
			f := clientFrame("ack")
			f.SessionId = []byte("too short")
			return mustMarshal(f)
		}, protocol.ReasonRefineFailed),

		Entry("a zero protocol version", "protocol version 0", func() []byte {
			f := clientFrame("ack")
			f.ProtocolVersion = 0
			return mustMarshal(f)
		}, protocol.ReasonRefineFailed),

		Entry("a future protocol version", "protocol version 99", func() []byte {
			f := clientFrame("ack")
			f.ProtocolVersion = 99
			return mustMarshal(f)
		}, protocol.ReasonBadVersion),

		Entry("a frame with no payload at all", "an empty envelope", func() []byte {
			return mustMarshal(envelope())
		}, protocol.ReasonUnknownKind),

		Entry("an event name outside its pattern", "a pattern violation", func() []byte {
			f := envelope()
			ev := validEvent()
			ev.Name = "Counter.Increment"
			f.Payload = &pb.Frame_Event{Event: ev}
			return mustMarshal(f)
		}, protocol.ReasonRefineFailed),

		Entry("an event name past its length bound", "an over-long event name", func() []byte {
			f := envelope()
			ev := validEvent()
			ev.Name = strings.Repeat("a", 65)
			f.Payload = &pb.Frame_Event{Event: ev}
			return mustMarshal(f)
		}, protocol.ReasonRefineFailed),

		Entry("an event before any snapshot", "a zero seen server sequence", func() []byte {
			f := envelope()
			ev := validEvent()
			ev.SeenServerSeq = 0
			f.Payload = &pb.Frame_Event{Event: ev}
			return mustMarshal(f)
		}, protocol.ReasonRefineFailed),

		Entry("an event field value past its length bound", "an over-long field value", func() []byte {
			f := envelope()
			ev := validEvent()
			ev.Fields = []*pb.EventField{{Key: "note", Value: strings.Repeat("x", 8193)}}
			f.Payload = &pb.Frame_Event{Event: ev}
			return mustMarshal(f)
		}, protocol.ReasonRefineFailed),

		Entry("a field key outside its pattern", "an injected field key", func() []byte {
			f := envelope()
			ev := validEvent()
			ev.Fields = []*pb.EventField{{Key: "a b", Value: "1"}}
			f.Payload = &pb.Frame_Event{Event: ev}
			return mustMarshal(f)
		}, protocol.ReasonRefineFailed),

		Entry("an unspecified enum value", "the zero enum", func() []byte {
			f := envelope()
			f.Payload = &pb.Frame_ResyncRequest{ResyncRequest: &pb.ResyncRequest{
				LastAppliedSeq: 1,
				Reason:         pb.ResyncReason_RESYNC_REASON_UNSPECIFIED,
			}}
			return mustMarshal(f)
		}, protocol.ReasonEnumDomain),

		Entry("an enum value outside the declared set", "an undeclared enum", func() []byte {
			f := envelope()
			f.Payload = &pb.Frame_ResyncRequest{ResyncRequest: &pb.ResyncRequest{
				LastAppliedSeq: 1,
				Reason:         pb.ResyncReason(9999),
			}}
			return mustMarshal(f)
		}, protocol.ReasonEnumDomain),

		Entry("a telemetry duration past its bound", "an implausible morph duration", func() []byte {
			f := envelope()
			f.Payload = &pb.Frame_ClientTelemetry{ClientTelemetry: &pb.ClientTelemetry{
				PatchId: 1, MorphMicros: 60000001,
			}}
			return mustMarshal(f)
		}, protocol.ReasonRefineFailed),

		Entry("a heartbeat interval outside its range", "an out-of-range heartbeat", func() []byte {
			f := envelope()
			f.Payload = &pb.Frame_Heartbeat{Heartbeat: &pb.Heartbeat{Nonce: 1, IntervalMs: 10}}
			return mustMarshal(f)
		}, protocol.ReasonRefineFailed),

		Entry("a non-UTF-8 string", "invalid UTF-8", func() []byte {
			b := protowire.AppendTag(nil, 1, protowire.VarintType)
			b = protowire.AppendVarint(b, 1)
			b = protowire.AppendTag(b, 2, protowire.BytesType)
			b = protowire.AppendBytes(b, sessionBytes())
			// payload: Event{name: <invalid utf-8>}
			ev := protowire.AppendTag(nil, 2, protowire.BytesType)
			ev = protowire.AppendBytes(ev, []byte{0xff, 0xfe})
			b = protowire.AppendTag(b, 3, protowire.BytesType)
			b = protowire.AppendBytes(b, ev)
			return b
		}, ""),
	)

	// H-5. The connection's read limit is the authoritative enforcement and
	// runs before allocation; this is the re-check behind it.
	It("refuses a frame larger than the inbound limit before refining it", func() {
		f := envelope()
		ev := validEvent()
		ev.Fields = []*pb.EventField{{Key: "blob", Value: strings.Repeat("x", 4096)}}
		f.Payload = &pb.Frame_Event{Event: ev}

		_, err := protocol.ParseInbound(mustMarshal(f), protocol.Limits{MaxInboundFrameBytes: 512})

		var rejected *protocol.RejectError
		Expect(errors.As(err, &rejected)).To(BeTrue())
		Expect(rejected.Reason).To(Equal(protocol.ReasonOversize))
		Expect(rejected.Close).To(Equal(protocol.CloseFrameTooLarge))
	})

	// H-3. A client that names another session is not confused, it is probing.
	It("refuses a frame naming a session other than the connection's", func() {
		in, err := parse(mustMarshal(clientFrame("ack")))
		Expect(err).NotTo(HaveOccurred())

		var other [16]byte
		copy(other[:], "fedcba9876543210")

		err = protocol.CheckSessionID(in, other)
		var rejected *protocol.RejectError
		Expect(errors.As(err, &rejected)).To(BeTrue())
		Expect(rejected.Reason).To(Equal(protocol.ReasonSessionMismatch))
		Expect(rejected.Close).To(Equal(protocol.CloseProtocolViolation))
	})

	It("does not expose mutable aliases into accepted inbound state", func() {
		in, err := parse(mustMarshal(clientFrame("event")))
		Expect(err).NotTo(HaveOccurred())

		frameCopy := in.Envelope()
		frameCopy.SessionId[0] ^= 0xff
		frameCopy.GetEvent().Name = "mutated"
		frameCopy.GetEvent().Fields[0].Value = "mutated"
		Expect(protocol.CheckSessionID(in, sessionKey())).To(Succeed(),
			"mutating Envelope's copy changed the accepted session identifier")

		event, ok := in.(protocol.InboundEvent)
		Expect(ok).To(BeTrue())
		Expect(event.Name()).To(Equal("counter.increment"),
			"mutating Envelope's payload changed the accepted event name")
		Expect(event.Fields()[0].Value).To(Equal("1"),
			"mutating Envelope's payload changed the accepted event fields")
		fields := event.Fields()
		fields[0].Key = "mutated"
		Expect(event.Fields()[0].Key).To(Equal("amount"),
			"mutating a Fields result changed the accepted event snapshot")
	})

	// FR-10. A frame from a newer schema keeps its unknown fields through the
	// refinement boundary, so a future field is preserved rather than dropped.
	It("preserves fields a newer peer sent that this build cannot name", func() {
		b := mustMarshal(clientFrame("ack"))
		b = protowire.AppendTag(b, 40, protowire.VarintType)
		b = protowire.AppendVarint(b, 1234)

		in, err := parse(b)
		Expect(err).NotTo(HaveOccurred())

		round := string(mustMarshal(in.Envelope()))
		unknown := string(protowire.AppendVarint(
			protowire.AppendTag(nil, 40, protowire.VarintType), 1234))
		Expect(round).To(ContainSubstring(unknown),
			"an unknown field was dropped rather than carried through")
	})
})
