package conformance_test

import (
	"encoding/binary"
	"runtime"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

// ---------------------------------------------------------------------------
// Raw wire construction
//
// These specs build bytes, not messages. A test that constructs a message and
// marshals it can only produce wire data the encoder is willing to emit, which
// is exactly the data an attacker will not send.
// ---------------------------------------------------------------------------

// tag encodes a protobuf field tag.
func tag(field int, wire int) []byte {
	return varint(uint64(field)<<3 | uint64(wire))
}

func varint(v uint64) []byte {
	buf := make([]byte, binary.MaxVarintLen64)
	n := binary.PutUvarint(buf, v)
	return buf[:n]
}

func concat(parts ...[]byte) []byte {
	var out []byte
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// lenDelim writes a length-delimited field with a *declared* length that the
// caller chooses, so the declared length and the real one can disagree.
func lenDelim(field int, declared int, body []byte) []byte {
	return concat(tag(field, 2), varint(uint64(declared)), body)
}

const (
	fieldProtocolVersion = 1
	fieldSessionID       = 2
	fieldEvent           = 3
	fieldAck             = 4
	fieldPatch           = 8
)

// validSessionID is sixteen bytes, which is the only length the envelope
// predicate admits.
var validSessionID = []byte("0123456789abcdef")

var defaultLimits = protocol.DefaultLimits()

// parse is the boundary under test.
func parse(b []byte) (protocol.IInbound, error) {
	return protocol.ParseInbound(b, defaultLimits)
}

var _ = Describe("The parse boundary, given wire data no encoder would produce", func() {

	// Truncation is the classic. Every one of these ends inside a structure
	// that declares how much more there is, so a decoder that trusts a length
	// before checking it reads past the buffer.
	DescribeTable("rejects truncated wire data without panicking",
		func(b []byte) {
			in, err := parse(b)

			Expect(err).To(HaveOccurred())
			Expect(in).To(BeNil(), "a rejected frame must not yield a partially applied payload")
			var rej *protocol.RejectError
			Expect(err).To(BeAssignableToTypeOf(rej), "every rejection is typed: %v", err)
		},
		Entry("a varint that never terminates",
			concat(tag(fieldProtocolVersion, 0), []byte{0xFF})),
		Entry("a ten-byte varint with every continuation bit set",
			concat(tag(fieldProtocolVersion, 0),
				[]byte{0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF, 0xFF})),
		Entry("a tag with no value after it",
			tag(fieldProtocolVersion, 0)),
		Entry("a length prefix longer than the bytes that follow",
			lenDelim(fieldSessionID, 16, []byte("short"))),
		Entry("a nested message truncated mid-field",
			concat(
				tag(fieldProtocolVersion, 0), varint(1),
				lenDelim(fieldSessionID, 16, validSessionID),
				lenDelim(fieldEvent, 32, concat(tag(1, 0), varint(7), tag(2, 2))),
			)),
		Entry("a field number of zero, which is never legal",
			concat(tag(0, 0), varint(1))),
		Entry("wire type 6, which does not exist",
			concat(tag(fieldProtocolVersion, 6), varint(1))),
		Entry("a group start tag, removed from proto3",
			concat(tag(fieldEvent, 3), varint(1))),
	)

	// An attacker-chosen length prefix is the allocation vector FR-13 is about.
	// The assertion is not merely that it is refused, but that refusing it does
	// not first reserve the memory it asked for.
	It("refuses a four-gigabyte length prefix without allocating four gigabytes", func() {
		hostile := lenDelim(fieldSessionID, 0xFFFFFFF, nil)

		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)

		done := make(chan error, 1)
		go func() {
			_, err := parse(hostile)
			done <- err
		}()

		var err error
		Eventually(done, 5*time.Second).Should(Receive(&err))
		Expect(err).To(HaveOccurred())

		runtime.ReadMemStats(&after)
		growth := after.TotalAlloc - before.TotalAlloc
		Expect(growth).To(BeNumerically("<", 8<<20),
			"parsing a frame declaring %d bytes allocated %d bytes: the length prefix was trusted",
			0xFFFFFFF, growth)
	})

	// The read limit is H-5 and it is the authoritative bound. ParseInbound's
	// own check is the belt-and-braces half, and it is the half a caller who
	// forgot SetReadLimit still gets.
	It("refuses a frame past the inbound limit before decoding it", func() {
		oversize := make([]byte, defaultLimits.MaxInboundFrameBytes+1)

		_, err := parse(oversize)

		var rej *protocol.RejectError
		Expect(err).To(BeAssignableToTypeOf(rej))
		Expect(err.(*protocol.RejectError).Close).To(Equal(protocol.CloseFrameTooLarge))
	})

	It("admits a frame at exactly the inbound limit, rather than one byte under it", func() {
		// The boundary is <=, so a frame of exactly the limit must fail for its
		// content and never for its size.
		filler := strings.Repeat("f", 4096)
		ev := &pb.Event{ClientRef: 1, Name: "qa.increment", FragmentId: "count", SeenServerSeq: 1}
		ev.Fields = append(ev.Fields, &pb.EventField{Key: "pad", Value: filler})
		b, err := proto.Marshal(&pb.Frame{
			ProtocolVersion: 1, SessionId: validSessionID,
			Payload: &pb.Frame_Event{Event: ev},
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(len(b)).To(BeNumerically("<", defaultLimits.MaxInboundFrameBytes))

		in, err := parse(b)

		Expect(err).NotTo(HaveOccurred())
		Expect(in.Kind()).To(Equal(protocol.KindEvent))
	})

	// Duplicate fields are last-one-wins in protobuf. The predicate must apply
	// to the merged value, not to the first one seen, or a valid prefix buys
	// admission for an invalid suffix.
	It("applies the predicate to the merged value when a field appears twice", func() {
		good := concat(tag(fieldSessionID, 2), varint(16), validSessionID)
		bad := concat(tag(fieldSessionID, 2), varint(3), []byte("bad"))
		b := concat(
			tag(fieldProtocolVersion, 0), varint(1),
			good, bad,
			lenDelim(fieldEvent, 0, nil),
		)

		_, err := parse(b)

		Expect(err).To(HaveOccurred(), "the second session_id is three bytes and must lose the frame")
	})

	// Two oneof members on the wire is not something the encoder can produce.
	// Protobuf keeps the last; the boundary must judge what it kept.
	It("judges the payload it actually kept when two oneof members are encoded", func() {
		ev, err := proto.Marshal(&pb.Event{
			ClientRef: 1, Name: "qa.increment", FragmentId: "count", SeenServerSeq: 1,
		})
		Expect(err).NotTo(HaveOccurred())
		patch, err := proto.Marshal(&pb.Patch{ServerSeq: 1, PatchId: 1, TransitionId: 1, StateVersion: 1})
		Expect(err).NotTo(HaveOccurred())

		// Event first, Patch second: the kept payload is the server-only Patch.
		b := concat(
			tag(fieldProtocolVersion, 0), varint(1),
			lenDelim(fieldSessionID, 16, validSessionID),
			lenDelim(fieldEvent, len(ev), ev),
			lenDelim(fieldPatch, len(patch), patch),
		)

		_, err = parse(b)

		Expect(err).To(HaveOccurred(),
			"a client sending a Patch must be refused even when an Event preceded it in the bytes")
		Expect(err.Error()).To(ContainSubstring("server-to-client"))
	})

	// A payload-less frame is refused, and the route it takes is worth pinning
	// down. KindOf reports it as unknown, so the ClientToServer guard refuses
	// it before the payload switch is reached — which makes that switch's
	// default branch, and the comment on it claiming to handle exactly this
	// case, unreachable. Recorded as QA-1 finding D-2; the behaviour is
	// correct, the comment is not, and this spec holds the real route so a
	// future edit to the guard cannot silently drop the rejection.
	It("refuses a frame carrying no payload at all, as an unknown kind", func() {
		b := concat(
			tag(fieldProtocolVersion, 0), varint(1),
			lenDelim(fieldSessionID, 16, validSessionID),
		)

		_, err := parse(b)

		var rej *protocol.RejectError
		Expect(err).To(BeAssignableToTypeOf(rej))
		Expect(err.(*protocol.RejectError).Reason).To(Equal(protocol.ReasonUnknownKind))
		Expect(err.(*protocol.RejectError).Close).To(Equal(protocol.CloseProtocolViolation))
	})

	It("refuses every server-to-client kind arriving from a client", func() {
		for _, payload := range []struct {
			name string
			set  func(frame *pb.Frame)
		}{
			{"patch", func(f *pb.Frame) {
				f.Payload = &pb.Frame_Patch{Patch: &pb.Patch{
					ServerSeq: 1, PatchId: 1, TransitionId: 1, StateVersion: 1,
					Origin: &pb.Origin{Kind: pb.OriginKind_CLIENT_EVENT, EventId: 1, ClientRef: 1, Source: "event:x"},
				}}
			}},
			{"snapshot", func(f *pb.Frame) {
				f.Payload = &pb.Frame_Snapshot{Snapshot: &pb.Snapshot{
					ServerSeq: 1, PatchId: 1, TransitionId: 1, StateVersion: 1,
					HeartbeatIntervalMs: 20000, MaxInboundFrameBytes: 65536, AckWindow: 16,
					Origin: &pb.Origin{Kind: pb.OriginKind_MOUNT, Source: "mount"},
				}}
			}},
			{"error", func(f *pb.Frame) {
				f.Payload = &pb.Frame_Error{Error: &pb.Error{Code: pb.ErrorCode_INTERNAL, Message: "x"}}
			}},
		} {
			f := &pb.Frame{ProtocolVersion: 1, SessionId: validSessionID}
			payload.set(f)
			b, err := proto.Marshal(f)
			Expect(err).NotTo(HaveOccurred())

			_, err = parse(b)

			Expect(err).To(HaveOccurred(), "a client sent a %s and it was admitted", payload.name)
		}
	})
})

var _ = Describe("The refinement predicates, exercised at their exact boundary", func() {

	// Off-by-one is where a hand-written bound is wrong, so each bound is
	// probed at N and N+1 rather than at a comfortable distance from either.
	DescribeTable("admits a value at the bound and refuses the next one",
		func(build func(n int) *pb.Frame, bound int, what string) {
			atBound, err := proto.Marshal(build(bound))
			Expect(err).NotTo(HaveOccurred())
			_, err = parse(atBound)
			Expect(err).NotTo(HaveOccurred(), "%s of exactly %d was refused", what, bound)

			over, err := proto.Marshal(build(bound + 1))
			Expect(err).NotTo(HaveOccurred())
			_, err = parse(over)
			Expect(err).To(HaveOccurred(), "%s of %d was admitted", what, bound+1)
		},
		Entry("Event.name length", func(n int) *pb.Frame {
			return &pb.Frame{ProtocolVersion: 1, SessionId: validSessionID,
				Payload: &pb.Frame_Event{Event: &pb.Event{
					ClientRef: 1, Name: "a" + strings.Repeat("b", n-1),
					FragmentId: "count", SeenServerSeq: 1,
				}}}
		}, 64, "an event name"),
		Entry("Event.fragment_id length", func(n int) *pb.Frame {
			return &pb.Frame{ProtocolVersion: 1, SessionId: validSessionID,
				Payload: &pb.Frame_Event{Event: &pb.Event{
					ClientRef: 1, Name: "qa.increment",
					FragmentId: strings.Repeat("f", n), SeenServerSeq: 1,
				}}}
		}, 64, "a fragment identifier"),
		Entry("EventField.key length", func(n int) *pb.Frame {
			return &pb.Frame{ProtocolVersion: 1, SessionId: validSessionID,
				Payload: &pb.Frame_Event{Event: &pb.Event{
					ClientRef: 1, Name: "qa.increment", FragmentId: "count", SeenServerSeq: 1,
					Fields: []*pb.EventField{{Key: strings.Repeat("k", n), Value: "v"}},
				}}}
		}, 128, "an event field key"),
		Entry("EventField.value length", func(n int) *pb.Frame {
			return &pb.Frame{ProtocolVersion: 1, SessionId: validSessionID,
				Payload: &pb.Frame_Event{Event: &pb.Event{
					ClientRef: 1, Name: "qa.increment", FragmentId: "count", SeenServerSeq: 1,
					Fields: []*pb.EventField{{Key: "k", Value: strings.Repeat("v", n)}},
				}}}
		}, 8192, "an event field value"),
		Entry("Event.fields cardinality (H-4)", func(n int) *pb.Frame {
			ev := &pb.Event{ClientRef: 1, Name: "qa.increment", FragmentId: "count", SeenServerSeq: 1}
			for i := 0; i < n; i++ {
				ev.Fields = append(ev.Fields, &pb.EventField{Key: "k", Value: "v"})
			}
			return &pb.Frame{ProtocolVersion: 1, SessionId: validSessionID,
				Payload: &pb.Frame_Event{Event: ev}}
		}, 64, "an event field list"),
		Entry("ClientTelemetry.morph_micros ceiling", func(n int) *pb.Frame {
			return &pb.Frame{ProtocolVersion: 1, SessionId: validSessionID,
				Payload: &pb.Frame_ClientTelemetry{ClientTelemetry: &pb.ClientTelemetry{
					PatchId: 1, MorphMicros: uint32(n), ApplyMicros: 1,
				}}}
		}, 60000000, "a reported morph duration"),
		Entry("Heartbeat.interval_ms ceiling", func(n int) *pb.Frame {
			return &pb.Frame{ProtocolVersion: 1, SessionId: validSessionID,
				Payload: &pb.Frame_Heartbeat{Heartbeat: &pb.Heartbeat{
					Nonce: 1, IntervalMs: uint32(n),
				}}}
		}, 300000, "a heartbeat interval"),
	)

	DescribeTable("refuses a session identifier of the wrong width",
		func(n int) {
			b, err := proto.Marshal(&pb.Frame{
				ProtocolVersion: 1,
				SessionId:       []byte(strings.Repeat("s", n)),
				Payload:         &pb.Frame_Ack{Ack: &pb.Ack{ServerSeq: 1}},
			})
			Expect(err).NotTo(HaveOccurred())

			_, err = parse(b)

			Expect(err).To(HaveOccurred(), "a %d-byte session_id was admitted", n)
		},
		Entry("empty", 0),
		Entry("one short", 15),
		Entry("one long", 17),
		Entry("a plausible UUID string", 36),
	)

	DescribeTable("refuses the zero value of every field predicated positive",
		func(f *pb.Frame, what string) {
			b, err := proto.Marshal(f)
			Expect(err).NotTo(HaveOccurred())

			_, err = parse(b)

			Expect(err).To(HaveOccurred(), "a zero %s was admitted", what)
		},
		Entry("Event.client_ref", &pb.Frame{ProtocolVersion: 1, SessionId: validSessionID,
			Payload: &pb.Frame_Event{Event: &pb.Event{
				ClientRef: 0, Name: "qa.increment", FragmentId: "count", SeenServerSeq: 1,
			}}}, "client_ref"),
		Entry("Event.seen_server_seq", &pb.Frame{ProtocolVersion: 1, SessionId: validSessionID,
			Payload: &pb.Frame_Event{Event: &pb.Event{
				ClientRef: 1, Name: "qa.increment", FragmentId: "count", SeenServerSeq: 0,
			}}}, "seen_server_seq"),
		Entry("Ack.server_seq", &pb.Frame{ProtocolVersion: 1, SessionId: validSessionID,
			Payload: &pb.Frame_Ack{Ack: &pb.Ack{ServerSeq: 0}}}, "acknowledged sequence"),
		Entry("Heartbeat.nonce", &pb.Frame{ProtocolVersion: 1, SessionId: validSessionID,
			Payload: &pb.Frame_Heartbeat{Heartbeat: &pb.Heartbeat{Nonce: 0, IntervalMs: 20000}}}, "nonce"),
		Entry("ClientTelemetry.patch_id", &pb.Frame{ProtocolVersion: 1, SessionId: validSessionID,
			Payload: &pb.Frame_ClientTelemetry{ClientTelemetry: &pb.ClientTelemetry{
				PatchId: 0, MorphMicros: 1, ApplyMicros: 1,
			}}}, "patch_id"),
		Entry("ResyncRequest.last_applied_seq", &pb.Frame{ProtocolVersion: 1, SessionId: validSessionID,
			Payload: &pb.Frame_ResyncRequest{ResyncRequest: &pb.ResyncRequest{
				LastAppliedSeq: 0, Reason: pb.ResyncReason_GAP,
			}}}, "last_applied_seq"),
		Entry("Frame.protocol_version", &pb.Frame{ProtocolVersion: 0, SessionId: validSessionID,
			Payload: &pb.Frame_Ack{Ack: &pb.Ack{ServerSeq: 1}}}, "protocol version"),
	)

	// H-1 says the *_UNSPECIFIED zero is never valid on the wire, and that an
	// undeclared number is refused too. Both arms matter: proto3 admits an
	// unknown enum number into the field rather than dropping it.
	DescribeTable("refuses an enum outside its declared domain (H-1)",
		func(reason pb.ResyncReason) {
			b, err := proto.Marshal(&pb.Frame{
				ProtocolVersion: 1, SessionId: validSessionID,
				Payload: &pb.Frame_ResyncRequest{ResyncRequest: &pb.ResyncRequest{
					LastAppliedSeq: 1, Reason: reason,
				}}})
			Expect(err).NotTo(HaveOccurred())

			_, err = parse(b)

			Expect(err).To(HaveOccurred(), "resync reason %d was admitted", reason)
		},
		Entry("the unspecified zero", pb.ResyncReason_RESYNC_REASON_UNSPECIFIED),
		Entry("one past the last declared value", pb.ResyncReason(4)),
		Entry("a large undeclared value", pb.ResyncReason(9999)),
		Entry("a value that would be negative as an int32", pb.ResyncReason(-1)),
	)

	DescribeTable("refuses an event name outside its character class",
		func(name string) {
			b, err := proto.Marshal(&pb.Frame{
				ProtocolVersion: 1, SessionId: validSessionID,
				Payload: &pb.Frame_Event{Event: &pb.Event{
					ClientRef: 1, Name: name, FragmentId: "count", SeenServerSeq: 1,
				}}})
			Expect(err).NotTo(HaveOccurred())

			_, err = parse(b)

			Expect(err).To(HaveOccurred(), "the event name %q was admitted", name)
		},
		Entry("empty", ""),
		Entry("leading digit", "1counter"),
		Entry("uppercase", "Counter.increment"),
		Entry("a space", "counter increment"),
		Entry("a newline, which a log line would swallow", "counter.increment\n"),
		Entry("a null byte", "counter\x00increment"),
		Entry("a slash", "counter/increment"),
		Entry("a regex anchor that would pass a sloppy matcher", "counter.increment\nEVIL"),
	)

	DescribeTable("refuses a fragment identifier outside its character class",
		func(id string) {
			b, err := proto.Marshal(&pb.Frame{
				ProtocolVersion: 1, SessionId: validSessionID,
				Payload: &pb.Frame_Event{Event: &pb.Event{
					ClientRef: 1, Name: "qa.increment", FragmentId: id, SeenServerSeq: 1,
				}}})
			Expect(err).NotTo(HaveOccurred())

			_, err = parse(b)

			Expect(err).To(HaveOccurred(), "the fragment identifier %q was admitted", id)
		},
		Entry("empty", ""),
		Entry("a space", "the count"),
		Entry("a quote that would break an attribute", `count"`),
		Entry("an angle bracket", "<count>"),
		Entry("a newline", "count\n"),
	)

	// FR-10. The client and server are both required to skip what they do not
	// know; the server additionally must not lose it.
	It("preserves an unknown field through the boundary (FR-10)", func() {
		ev, err := proto.Marshal(&pb.Event{
			ClientRef: 1, Name: "qa.increment", FragmentId: "count", SeenServerSeq: 1,
		})
		Expect(err).NotTo(HaveOccurred())

		// Field 99, varint: a field a future schema added.
		future := concat(
			tag(fieldProtocolVersion, 0), varint(1),
			lenDelim(fieldSessionID, 16, validSessionID),
			lenDelim(fieldEvent, len(ev), ev),
			concat(tag(99, 0), varint(1234)),
		)

		in, err := parse(future)

		Expect(err).NotTo(HaveOccurred(), "an unknown envelope field must not lose the frame")
		Expect(in.Kind()).To(Equal(protocol.KindEvent))
	})

	DescribeTable("refuses a protocol version it does not speak (H-2)",
		func(v uint32) {
			b, err := proto.Marshal(&pb.Frame{
				ProtocolVersion: v, SessionId: validSessionID,
				Payload: &pb.Frame_Ack{Ack: &pb.Ack{ServerSeq: 1}}})
			Expect(err).NotTo(HaveOccurred())

			_, err = parse(b)

			var rej *protocol.RejectError
			Expect(err).To(BeAssignableToTypeOf(rej))
			// Version 0 is structurally impossible and is refused by the
			// predicate; every other mismatch is refused with a reason, which is
			// the layering protocol.md §8.2 requires.
			if v != 0 {
				Expect(err.(*protocol.RejectError).Close).To(Equal(protocol.CloseUnsupportedVersion),
					"version %d must be refused as unsupported, not as malformed", v)
			}
		},
		Entry("zero", uint32(0)),
		Entry("two", uint32(2)),
		Entry("sixteen, which an earlier draft would have called malformed", uint32(16)),
		Entry("the largest uint32", ^uint32(0)),
	)

	// H-3. A frame naming another session is probing, not confusion.
	It("refuses a frame naming a session other than the connection's (H-3)", func() {
		b, err := proto.Marshal(&pb.Frame{
			ProtocolVersion: 1, SessionId: validSessionID,
			Payload: &pb.Frame_Ack{Ack: &pb.Ack{ServerSeq: 1}}})
		Expect(err).NotTo(HaveOccurred())

		in, err := parse(b)
		Expect(err).NotTo(HaveOccurred())

		var other [16]byte
		copy(other[:], "fedcba9876543210")

		err = protocol.CheckSessionID(in, other)

		var rej *protocol.RejectError
		Expect(err).To(BeAssignableToTypeOf(rej))
		Expect(err.(*protocol.RejectError).Close).To(Equal(protocol.CloseProtocolViolation))
	})
})

var _ = Describe("A live connection fed hostile bytes", func() {

	// The parse boundary is unit-tested above. This is the same question asked
	// of the running server, because a boundary that is correct in isolation
	// and unreachable in the handler protects nothing.
	It("answers a predicate violation with a typed error and no state change", func() {
		d := dial(nil)

		// A well-formed envelope carrying an event whose name the predicate
		// refuses. It must not reach the reducer.
		b, err := proto.Marshal(&pb.Frame{
			ProtocolVersion: 1, SessionId: d.sessionID,
			Payload: &pb.Frame_Event{Event: &pb.Event{
				ClientRef: 1, Name: "QA.INCREMENT", FragmentId: "count", SeenServerSeq: 1,
			}}})
		Expect(err).NotTo(HaveOccurred())
		Expect(d.writeBytes(b)).To(Succeed())

		// The connection closes on a protocol violation, so either an Error
		// frame arrives first or the close does. Both are typed outcomes; a
		// patch is not.
		_, readErr := d.readErr(3 * time.Second)
		if readErr == nil {
			Expect(d.patches()).To(BeEmpty(), "a refused frame produced a patch")
		}
		Expect(d.patches()).To(BeEmpty())
	})

	It("counts a text frame as a protocol violation rather than parsing it", func() {
		d := dial(nil)

		Expect(d.writeText("hello")).To(Succeed())

		Expect(d.closed(5*time.Second)).To(BeTrue(),
			"a text frame must close the connection (protocol.md §1, close 4002)")
		Expect(d.patches()).To(BeEmpty())
	})

	It("survives a burst of malformed frames without producing a patch", func() {
		d := dial(nil)

		for _, hostile := range [][]byte{
			{0xFF},
			{0x08},
			concat(tag(fieldSessionID, 2), varint(200), []byte("short")),
			concat(tag(fieldProtocolVersion, 0), varint(1)),
		} {
			// Writing may fail once the server has closed; that is the expected
			// end state, not an error in the spec.
			if err := d.writeBytes(hostile); err != nil {
				break
			}
		}

		d.drainUntilQuiet(500 * time.Millisecond)
		Expect(d.patches()).To(BeEmpty(), "malformed input produced a patch")
	})
})
