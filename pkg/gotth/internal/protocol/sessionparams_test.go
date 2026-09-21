package protocol_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	"github.com/candacelabs/csf/pkg/liquidproto"
)

// ---------------------------------------------------------------------------
// D-23: the Snapshot session-parameter ranges are named once, and the naming
// is held against the refinement it claims to be naming.
//
// protocol.SessionParamRange exists because live.New has to reject an
// out-of-range Limits field at construction and tell the operator what the
// range is, and the generated code exposes the predicate as a validator rather
// than as two constants. That makes this file the seam: the ranges are written
// down in one place, and these specs are the only reason to believe the
// written-down interval is the compiled one.
//
// Each spec asks the handwritten outbound boundary that owns server frames.
// That boundary applies the generated predicate and the protocol's cross-field
// invariants exactly as the real write path does. Nothing here retypes a bound:
// every number comes from the range under test, so an entry cannot agree with
// itself.
// ---------------------------------------------------------------------------

var _ = Describe("The Snapshot session-parameter ranges (D-23)", func() {
	// admits is the production outbound boundary with one Snapshot field varied,
	// reduced to the question these specs ask of it.
	type admits func(value uint32) error

	heartbeat := admits(func(v uint32) error {
		frame := serverFrame("snapshot")
		frame.GetSnapshot().HeartbeatIntervalMs = v
		return protocol.ValidateOutbound(frame)
	})
	frameBytes := admits(func(v uint32) error {
		frame := serverFrame("snapshot")
		frame.GetSnapshot().MaxInboundFrameBytes = v
		return protocol.ValidateOutbound(frame)
	})
	ackWindow := admits(func(v uint32) error {
		frame := serverFrame("snapshot")
		frame.GetSnapshot().AckWindow = v
		return protocol.ValidateOutbound(frame)
	})

	DescribeTable("names exactly the interval the generated refinement admits",
		func(r protocol.SessionParamRange, ok admits) {
			// Both endpoints are inside. A range that is narrower than the
			// refinement costs an operator a legal configuration and says a
			// protocol rule is the reason, which is worse than not checking.
			Expect(ok(r.Min)).To(Succeed(),
				"%s claims a floor of %d, which the refinement refuses: New would reject a "+
					"configuration the protocol accepts", r.Field, r.Min)
			Expect(ok(r.Max)).To(Succeed(),
				"%s claims a ceiling of %d, which the refinement refuses: New would reject a "+
					"configuration the protocol accepts", r.Field, r.Max)

			// One past each end is outside. A range wider than the refinement
			// is D-23 itself, narrower by the width of the difference.
			Expect(ok(r.Min-1)).NotTo(Succeed(),
				"%d is one below the floor %s claims and the refinement admits it, so the floor "+
					"is not where this says it is", r.Min-1, r.Field)
			Expect(ok(r.Max+1)).NotTo(Succeed(),
				"%d is one above the ceiling %s claims and the refinement admits it, so a value "+
					"New accepts still builds a Snapshot the write path refuses", r.Max+1, r.Field)

			// And it is the field it says it is. The endpoints alone would
			// still hold if two ranges were swapped between two fields whose
			// intervals happen to nest, and the operator would then be sent to
			// the wrong predicate by an error message that reads correct.
			var rerr *liquidproto.Error
			Expect(errors.As(ok(r.Max+1), &rerr)).To(BeTrue(),
				"the refinement rejected %d with %T rather than a *liquidproto.Error, so a caller "+
					"cannot tell which field it was", r.Max+1, ok(r.Max+1))
			Expect(rerr.Field).To(Equal(r.Field))
			Expect(rerr.Predicate).To(Equal(r.Predicate),
				"the predicate compiled into the generated validator is %q and this range quotes "+
					"%q; live.New puts that text in front of an operator as the authority for the "+
					"range it just enforced", rerr.Predicate, r.Predicate)
		},
		Entry("heartbeat_interval_ms", protocol.HeartbeatIntervalMSRange, heartbeat),
		Entry("max_inbound_frame_bytes", protocol.MaxInboundFrameBytesRange, frameBytes),
		Entry("ack_window", protocol.AckWindowRange, ackWindow),
	)

	// The defaults have to be inside the ranges, or every application that
	// never mentions Limits fails to mount. Asserted against the refinement
	// rather than against the ranges, so it is a second, independent reason to
	// believe the ranges: session.DefaultLimits' values are what the actor puts
	// on the wire when nobody configures anything.
	//
	// The numbers are the documented defaults, written as literals rather than
	// read from session.DefaultLimits, because internal/protocol must not import
	// internal/session — and a literal is what an operator reads in the godoc
	// anyway.
	DescribeTable("admits the documented default",
		func(def uint32, ok admits) {
			Expect(ok(def)).To(Succeed())
		},
		Entry("HeartbeatInterval, 20 s", uint32(20000), heartbeat),
		Entry("MaxInboundFrameBytes, 64 KiB", uint32(65536), frameBytes),
		Entry("AckWindow, 16", uint32(16), ackWindow),
	)
})
