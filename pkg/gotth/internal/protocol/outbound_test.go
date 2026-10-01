package protocol_test

import (
	"context"
	"errors"
	"strings"
	"unicode/utf8"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
)

var _ = Describe("The outbound validation boundary", func() {

	It("accepts every well-formed frame the server sends", func() {
		for member := range validServerPayloads {
			Expect(protocol.ValidateOutbound(serverFrame(member))).To(Succeed(), "member %q", member)
		}
	})

	It("refuses a frame with no payload", func() {
		Expect(protocol.ValidateOutbound(envelope())).NotTo(Succeed())
	})

	It("refuses a nil frame rather than panicking", func() {
		Expect(protocol.ValidateOutbound(nil)).NotTo(Succeed())
	})

	// P1. An origin-less patch is the orphan the whole provenance story rests
	// on being impossible, and construction discipline is not what makes it so.
	Describe("origin totality", func() {
		It("refuses a patch with no origin at all", func() {
			f := serverFrame("patch")
			f.GetPatch().Origin = nil
			Expect(protocol.ValidateOutbound(f)).NotTo(Succeed())
		})

		It("refuses a patch whose origin names no source", func() {
			f := serverFrame("patch")
			f.GetPatch().GetOrigin().Source = ""
			Expect(protocol.ValidateOutbound(f)).NotTo(Succeed())
		})

		It("refuses a patch whose origin kind is unspecified", func() {
			f := serverFrame("patch")
			f.GetPatch().GetOrigin().Kind = pb.OriginKind_ORIGIN_KIND_UNSPECIFIED
			Expect(protocol.ValidateOutbound(f)).NotTo(Succeed())
		})

		It("refuses a source outside the namespacing pattern", func() {
			f := serverFrame("patch")
			f.GetPatch().GetOrigin().Source = "Event:Counter"
			Expect(protocol.ValidateOutbound(f)).NotTo(Succeed())
		})

		// BR-2. ValidOriginSource is a second, hand-written implementation of a
		// compiled predicate, so it is only worth having while the two agree.
		// The callers that use it — live.New's event-name check, the actor's
		// effect-source check — are refusing a string on this function's word,
		// and a divergence would either refuse a legal source at startup or let
		// an illegal one through to the frame it exists to keep it out of.
		DescribeTable("agrees with the compiled Origin.source predicate",
			func(source string) {
				f := serverFrame("patch")
				f.GetPatch().GetOrigin().Source = source

				Expect(protocol.ValidOriginSource(source)).
					To(Equal(protocol.ValidateOutbound(f) == nil),
						"the hand-written predicate and the schema disagree about %q", source)
			},
			Entry("empty", ""),
			Entry("a bare literal", "mount"),
			Entry("namespaced by event", "event:counter.increment"),
			Entry("namespaced by effect", "effect:chat.broadcast"),
			Entry("with every permitted punctuation", "a_b.c:d/e-f"),
			Entry("exactly at the bound", strings.Repeat("a", protocol.MaxOriginSource)),
			Entry("one byte over the bound", strings.Repeat("a", protocol.MaxOriginSource+1)),
			Entry("the composed event source at the bound",
				protocol.SourceEventPrefix+strings.Repeat("b", protocol.MaxOriginSource-len(protocol.SourceEventPrefix))),
			Entry("the composed event source one over",
				protocol.SourceEventPrefix+strings.Repeat("b", protocol.MaxOriginSource-len(protocol.SourceEventPrefix)+1)),
			Entry("leading digit", "1event"),
			Entry("leading punctuation", ":event"),
			Entry("upper case", "effect:Chat.Broadcast"),
			Entry("a space", "effect:chat broadcast"),
			Entry("non-ASCII", "effect:café"),
			Entry("a NUL byte", "effect:a\x00b"),
			Entry("the invalid-source stand-in", protocol.SourceEffectInvalid),
		)

		It("names sources this library composes that are all legal", func() {
			for _, s := range []string{
				protocol.SourceMount,
				protocol.SourceResync,
				protocol.SourceSlowClient,
				protocol.SourceClientRecovered,
				protocol.SourceEffectInvalid,
			} {
				Expect(protocol.ValidOriginSource(s)).To(BeTrue(), "source %q", s)
			}
		})
	})

	// H-6, over every kind rather than the two that happen to be interesting.
	DescribeTable("binds the causal identifiers to the origin kind (H-6)",
		func(kind pb.OriginKind, eventID, clientRef uint64, valid bool) {
			f := serverFrame("patch")
			o := f.GetPatch().GetOrigin()
			o.Kind = kind
			o.EventId = eventID
			o.ClientRef = clientRef
			o.Source = "effect:test"
			if kind == pb.OriginKind_CLIENT_EVENT {
				o.Source = "event:test"
			}

			err := protocol.ValidateOutbound(f)
			if valid {
				Expect(err).To(Succeed())
			} else {
				Expect(err).NotTo(Succeed())
			}
		},
		Entry("a client event carries both", pb.OriginKind_CLIENT_EVENT, uint64(3), uint64(4), true),
		Entry("a client event without an event id", pb.OriginKind_CLIENT_EVENT, uint64(0), uint64(4), false),
		Entry("a client event without a client ref", pb.OriginKind_CLIENT_EVENT, uint64(3), uint64(0), false),
		Entry("a resync carries both, being caused by a nameable frame", pb.OriginKind_RESYNC, uint64(3), uint64(4), true),
		Entry("a resync without them", pb.OriginKind_RESYNC, uint64(0), uint64(0), false),
		Entry("an effect carries neither", pb.OriginKind_EFFECT, uint64(0), uint64(0), true),
		Entry("an effect claiming an event id", pb.OriginKind_EFFECT, uint64(3), uint64(0), false),
		Entry("a timer carries neither", pb.OriginKind_TIMER, uint64(0), uint64(0), true),
		Entry("a pubsub delivery carries neither", pb.OriginKind_PUBSUB, uint64(0), uint64(0), true),
		Entry("a mount carries neither", pb.OriginKind_MOUNT, uint64(0), uint64(0), true),
		Entry("a mount claiming a client ref", pb.OriginKind_MOUNT, uint64(0), uint64(4), false),
	)

	// H-12.
	DescribeTable("keeps an error's causal identifiers consistent (H-12)",
		func(eventID, clientRef uint64, valid bool) {
			f := envelope()
			f.Payload = &pb.Frame_Error{Error: &pb.Error{
				Code: pb.ErrorCode_UNKNOWN_EVENT, Message: "no such event",
				EventId: eventID, ClientRef: clientRef,
			}}

			if valid {
				Expect(protocol.ValidateOutbound(f)).To(Succeed())
			} else {
				Expect(protocol.ValidateOutbound(f)).NotTo(Succeed())
			}
		},
		Entry("not event-scoped", uint64(0), uint64(0), true),
		Entry("event-scoped", uint64(5), uint64(6), true),
		Entry("half event-scoped", uint64(5), uint64(0), false),
		Entry("the other half", uint64(0), uint64(6), false),
	)

	// H-13.
	Describe("the supersession edge (H-13)", func() {
		snapshotWith := func(kind pb.OriginKind, serverSeq, from, through uint64) *pb.Frame {
			f := serverFrame("snapshot")
			s := f.GetSnapshot()
			s.ServerSeq = serverSeq
			s.Origin.Kind = kind
			s.Origin.Source = protocol.SourceMount
			if kind == pb.OriginKind_RESYNC {
				s.Origin.Source = protocol.SourceResync
				s.Origin.EventId = 11
				s.Origin.ClientRef = 12
			}
			s.SupersededFromSeq = from
			s.SupersededThroughSeq = through
			return f
		}

		It("accepts a first snapshot with no range", func() {
			Expect(protocol.ValidateOutbound(snapshotWith(pb.OriginKind_MOUNT, 1, 0, 0))).To(Succeed())
		})

		It("accepts a resync snapshot with a range below its own sequence", func() {
			Expect(protocol.ValidateOutbound(snapshotWith(pb.OriginKind_RESYNC, 10, 4, 9))).To(Succeed())
		})

		It("refuses a half-set range", func() {
			Expect(protocol.ValidateOutbound(snapshotWith(pb.OriginKind_RESYNC, 10, 4, 0))).NotTo(Succeed())
		})

		It("refuses a mount snapshot carrying a range", func() {
			Expect(protocol.ValidateOutbound(snapshotWith(pb.OriginKind_MOUNT, 10, 4, 9))).NotTo(Succeed())
		})

		It("refuses a resync snapshot with no range", func() {
			Expect(protocol.ValidateOutbound(snapshotWith(pb.OriginKind_RESYNC, 10, 0, 0))).NotTo(Succeed())
		})

		It("refuses an inverted range", func() {
			Expect(protocol.ValidateOutbound(snapshotWith(pb.OriginKind_RESYNC, 10, 9, 4))).NotTo(Succeed())
		})

		It("refuses a range that reaches its own sequence number", func() {
			Expect(protocol.ValidateOutbound(snapshotWith(pb.OriginKind_RESYNC, 10, 4, 10))).NotTo(Succeed())
		})
	})

	It("refuses a fragment update naming no fragment", func() {
		f := serverFrame("patch")
		f.GetPatch().GetUpdates()[0].FragmentId = ""
		Expect(protocol.ValidateOutbound(f)).NotTo(Succeed())
	})

	It("refuses a zero patch identifier, so a chain can have no hole", func() {
		for _, break_ := range []func(patch *pb.Patch){
			func(p *pb.Patch) { p.ServerSeq = 0 },
			func(p *pb.Patch) { p.PatchId = 0 },
			func(p *pb.Patch) { p.TransitionId = 0 },
			func(p *pb.Patch) { p.StateVersion = 0 },
		} {
			f := serverFrame("patch")
			break_(f.GetPatch())
			Expect(protocol.ValidateOutbound(f)).NotTo(Succeed())
		}
	})
})

var _ = Describe("The framer", func() {
	var (
		sent  [][]byte
		kinds []protocol.Kind
		bad   []protocol.Kind
		f     *protocol.Framer
	)

	BeforeEach(func() {
		sent, kinds, bad = nil, nil, nil
		f = protocol.NewFramer(func(_ context.Context, b []byte) error {
			sent = append(sent, b)
			return nil
		})
		f.OnSent = func(k protocol.Kind, _ int) { kinds = append(kinds, k) }
		f.OnInvalid = func(k protocol.Kind, _ error) { bad = append(bad, k) }
	})

	// P8: the emitted counter and the wire capture are the same number, or a
	// second write path exists.
	It("counts exactly the frames it writes", func() {
		for member := range validServerPayloads {
			_, err := f.Send(context.Background(), serverFrame(member))
			Expect(err).NotTo(HaveOccurred(), "member %q", member)
		}
		Expect(kinds).To(HaveLen(len(sent)))
		Expect(bad).To(BeEmpty())
	})

	It("never writes a frame it could not validate", func() {
		broken := serverFrame("patch")
		broken.GetPatch().GetOrigin().Source = ""

		n, err := f.Send(context.Background(), broken)

		Expect(n).To(BeZero())
		var invalid *protocol.InvalidFrameError
		Expect(errors.As(err, &invalid)).To(BeTrue(), "%v", err)
		Expect(invalid.Kind).To(Equal(protocol.KindPatch))
		Expect(sent).To(BeEmpty(), "an invalid frame reached the transport")
		Expect(bad).To(ConsistOf(protocol.KindPatch))
		Expect(kinds).To(BeEmpty())
	})

	// U-5, and L9's ruling on it. B-9 — every socket write in the module goes
	// through Encode, which calls ValidateOutbound and refuses on failure — was
	// held by grep. Write took a Kind and pre-encoded bytes, so nothing
	// structurally stopped a future caller marshalling a frame itself and
	// handing them over, and nothing stopped a caller that ignored Encode's
	// error from writing what it got back. The token closes both: Encode is the
	// only way to mint one, and the zero value a caller outside this package
	// can name is refused.
	Describe("the encoded-frame token", func() {
		It("writes what Encode produced, counting it exactly once", func() {
			enc, err := f.Encode(serverFrame("patch"))
			Expect(err).NotTo(HaveOccurred())
			Expect(enc.Kind()).To(Equal(protocol.KindPatch))
			Expect(enc.Len()).To(BeNumerically(">", 0))

			n, err := f.Write(context.Background(), enc)

			Expect(err).NotTo(HaveOccurred())
			Expect(n).To(Equal(enc.Len()))
			Expect(sent).To(HaveLen(1))
			Expect(kinds).To(ConsistOf(protocol.KindPatch))
		})

		It("refuses a token no Encode produced", func() {
			var forged protocol.Encoded

			n, err := f.Write(context.Background(), forged)

			Expect(n).To(BeZero())
			var invalid *protocol.InvalidFrameError
			Expect(errors.As(err, &invalid)).To(BeTrue(), "%v", err)
			Expect(sent).To(BeEmpty(), "bytes nothing validated reached the transport")
			Expect(kinds).To(BeEmpty())
		})

		// The failure mode the old (Kind, []byte, error) signature left open: a
		// caller that ignored the error still held something Write accepted.
		It("hands back nothing writable when validation fails", func() {
			broken := serverFrame("patch")
			broken.GetPatch().GetOrigin().Source = ""

			enc, err := f.Encode(broken)
			Expect(err).To(HaveOccurred())

			n, writeErr := f.Write(context.Background(), enc)

			Expect(n).To(BeZero())
			Expect(writeErr).To(HaveOccurred())
			Expect(sent).To(BeEmpty(),
				"ignoring Encode's error was enough to put an unvalidated frame on the wire")
		})
	})

	It("reports a transport failure separately from an invalid frame", func() {
		boom := errors.New("connection reset")
		f = protocol.NewFramer(func(ctx context.Context, payload []byte) error { return boom })

		_, err := f.Send(context.Background(), serverFrame("patch"))

		Expect(errors.Is(err, boom)).To(BeTrue())
		var invalid *protocol.InvalidFrameError
		Expect(errors.As(err, &invalid)).To(BeFalse(),
			"a transport failure was reported as a library bug")
	})

	It("truncates an over-long error message rather than failing to send it", func() {
		frame := protocol.NewError(sessionKey(), pb.ErrorCode_INTERNAL, strings.Repeat("x", 2000), 0, 0, true)

		_, err := f.Send(context.Background(), frame)

		Expect(err).NotTo(HaveOccurred())
		Expect(len(frame.GetError().GetMessage())).To(BeNumerically("<=", 512))
	})

	// Error.message is a proto3 string, so protobuf-go refuses to marshal
	// invalid UTF-8 into it. A cut through the middle of a multi-byte rune
	// would therefore lose exactly the frames the truncation exists to keep,
	// and it became reachable the moment dev mode began appending a panic
	// value the application chose.
	DescribeTable("cutting an over-long message on a rune boundary",
		func(message string) {
			frame := protocol.NewError(sessionKey(), pb.ErrorCode_INTERNAL, message, 0, 0, false)

			_, err := f.Send(context.Background(), frame)

			Expect(err).NotTo(HaveOccurred())
			out := frame.GetError().GetMessage()
			Expect(len(out)).To(BeNumerically("<=", 512))
			Expect(utf8.ValidString(out)).To(BeTrue(), "the message was cut through a rune")
		},
		// Each width puts a different byte of a rune on the 509th boundary,
		// so one of these lands on a split whatever the offset arithmetic is.
		Entry("two-byte runes", strings.Repeat("é", 400)),
		Entry("three-byte runes", strings.Repeat("→", 400)),
		Entry("four-byte runes", strings.Repeat("𝄞", 400)),
		Entry("a rune straddling the cut", strings.Repeat("x", 508)+strings.Repeat("é", 20)),
	)
})

var _ = Describe("The close-code enumeration", func() {

	It("gives every code a distinct lower-case label", func() {
		seen := map[string]protocol.CloseCode{}
		for _, c := range protocol.CloseCodes() {
			Expect(c.Valid()).To(BeTrue(), "%d is not in the enumeration", c)

			label := c.Label()
			Expect(label).To(Equal(strings.ToLower(label)), "%d has a non-lower-case label", c)
			Expect(label).NotTo(Equal("unenumerated"))
			Expect(seen).NotTo(HaveKey(label), "%d and %d share the label %q", c, seen[label], label)
			seen[label] = c

			Expect(int(c)).To(BeNumerically(">=", 4000))
			Expect(int(c)).To(BeNumerically("<=", 4999))
		}
		Expect(seen).To(HaveLen(14))
	})

	It("reports an unenumerated code as such rather than inventing one", func() {
		Expect(protocol.CloseCode(4999).Valid()).To(BeFalse())
		Expect(protocol.CloseCode(4999).Label()).To(Equal("unenumerated"))
		Expect(protocol.CloseNone.Valid()).To(BeFalse())
	})
})
