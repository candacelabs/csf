package live_test

import (
	"errors"
	"reflect"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// D-14: the Limits an application is allowed to set.
//
// Before this, New inspected no Limits field at all. QA-1 measured what that
// cost through the one field with a protocol ceiling behind it: at
// CoalesceFlushAt 4000, the contributing-event union outgrew H-4's bound, the
// outbound validator refused the frame the library itself had built, and the
// deferred set had already been taken — 1,377 event identifiers on no frame at
// all, through the field documented as the reason provenance is never
// truncated.
//
// These specs are about the boundary, not about the loss: the loss is a
// conformance property and lives in the conformance suite, which drives a real
// session. What is asserted here is that the configuration never reaches a
// session in the first place.
// ---------------------------------------------------------------------------

var _ = Describe("New, validating Limits", func() {
	DescribeTable("refuses a CoalesceFlushAt the protocol cannot carry",
		func(at int) {
			cfg := validConfig()
			cfg.Limits.CoalesceFlushAt = at

			_, err := live.New(cfg)

			var cfgErr *live.ConfigError
			Expect(errors.As(err, &cfgErr)).To(BeTrue(),
				"a CoalesceFlushAt of %d started an application whose flush trigger builds frames "+
					"the protocol refuses, losing the provenance the field exists to keep; got %v", at, err)
			Expect(cfgErr.Field).To(Equal("Limits.CoalesceFlushAt"))
		},
		// One above the boundary. It is the entry that would survive a fix
		// written against a round number rather than against the arithmetic.
		Entry("one above the largest value the union can carry", session.MaxCoalesceFlushAt+1),
		// The schema ceiling itself. It looks safe — it is the number H-4
		// names — and it is not: the flush emits one identifier more than it
		// counted, so the frame holds 1,025.
		Entry("the schema ceiling itself", 1024),
		// QA-1's measured repro.
		Entry("D-14's measured value", 4000),
	)

	DescribeTable("accepts the range an application may tune within",
		func(at int) {
			cfg := validConfig()
			cfg.Limits.CoalesceFlushAt = at

			app, err := live.New(cfg)

			Expect(err).NotTo(HaveOccurred())
			Expect(app).NotTo(BeNil())
		},
		Entry("zero, which takes the default", 0),
		Entry("one, the tightest possible flush", 1),
		Entry("the default", session.DefaultLimits().CoalesceFlushAt),
		// The boundary itself is accepted, and it is accepted because it was
		// measured to work rather than because it looked safe: at this value
		// the largest union on the wire is exactly the schema ceiling.
		Entry("the largest value the union can carry", session.MaxCoalesceFlushAt),
	)

	// The arithmetic behind the boundary, asserted rather than commented.
	//
	// D-14 fixed it at CoalesceFlushCeiling - 1 and the "- 1" was the deferred
	// transition's own event identifier. C-31 gives an application a second way
	// to add identifiers to the frame the flush builds, so the headroom is that
	// one plus the per-event bound, and this is what fails if either constant is
	// ever moved without the other. Stated in terms of the frame — the widest
	// legal setting plus everything one more emission can add must still fit —
	// so it says why the number is the number.
	It("leaves exactly enough headroom for one maximal emission", func() {
		widest := session.MaxCoalesceFlushAt + 1 + session.MaxEventContributing

		Expect(widest).To(Equal(protocol.CoalesceFlushCeiling),
			"the largest CoalesceFlushAt is %d and one more emission can add %d identifiers, "+
				"which builds a frame of %d against H-4's ceiling of %d: either the flush trigger "+
				"can overflow or it is flushing earlier than it needs to",
			session.MaxCoalesceFlushAt, 1+session.MaxEventContributing,
			widest, protocol.CoalesceFlushCeiling)
	})

	It("says what to set CoalesceFlushAt to instead", func() {
		cfg := validConfig()
		cfg.Limits.CoalesceFlushAt = 4000

		_, err := live.New(cfg)

		// FR-58: an error names the field, the offending value and the
		// actionable next step. "invalid limit" is the failure this asserts
		// against.
		Expect(err).To(MatchError(ContainSubstring("Limits.CoalesceFlushAt")))
		Expect(err).To(MatchError(ContainSubstring("4000")))
		// The literal rather than the constant, deliberately. This spec is
		// about what an operator reads, and interpolating the same constant the
		// message interpolates would agree with any value at all — including
		// the empty one a botched format string produces.
		Expect(err).To(MatchError(ContainSubstring("959")))
		Expect(err).To(MatchError(ContainSubstring("512")))
	})

	// The completeness property, in the shape protocol's H-4 list-bounds table
	// uses for the same reason: the check that rots is the one a new field
	// slips past. Every numeric field of Limits is set negative in turn, and
	// New must name that field.
	//
	// This is written by reflection over the exported type rather than as a
	// table of names, because a table of names is a second thing to keep in
	// step with the struct and would go stale in exactly the case it exists to
	// catch. A negative is the one value that is meaningless for every field
	// here — zero already means "take the default" — and two of them,
	// MailboxDepth and AckChannelDepth, are channel capacities, where negative
	// is a runtime panic at the first connection rather than an error at all:
	//
	//	make(chan int, -1) → panic: makechan: size out of range
	It("rejects a negative value in every field of Limits, naming it", func() {
		t := reflect.TypeOf(live.Limits{})
		Expect(t.NumField()).To(BeNumerically(">", 0))

		checked := 0
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)

			cfg := validConfig()
			v := reflect.ValueOf(&cfg.Limits).Elem().Field(i)
			switch f.Type.Kind() {
			case reflect.Int, reflect.Int64:
				// time.Duration is an Int64 and negative is meaningless for
				// every one of them: a deadline in the past, an interval that
				// never elapses.
				v.SetInt(-1)
			case reflect.Float64:
				v.SetFloat(-1)
			default:
				Fail("live.Limits." + f.Name + " is a " + f.Type.Kind().String() +
					", which this spec does not know how to make invalid: decide its range " +
					"in Limits.validate and teach this spec the kind")
			}
			checked++

			_, err := live.New(cfg)

			var cfgErr *live.ConfigError
			Expect(errors.As(err, &cfgErr)).To(BeTrue(),
				"live.Limits.%s accepts a negative value: New started an application with it", f.Name)
			Expect(cfgErr.Field).To(Equal("Limits."+f.Name),
				"a negative %s was rejected as %q, so the operator is sent to the wrong field",
				f.Name, cfgErr.Field)
		}
		Expect(checked).To(Equal(t.NumField()))
	})

	It("still takes the defaults for a Limits nobody set", func() {
		// The zero value must stay valid, or every application that never
		// mentions Limits fails to start. Stated as its own spec because it is
		// the regression a validator gets wrong first.
		_, err := live.New(validConfig())
		Expect(err).NotTo(HaveOccurred())
	})
})

// Deliberately not written: a spec asserting that New "does not clamp". Config
// is taken by value, so New could not write back to the caller's struct if it
// wanted to, and a spec asserting that would pass on a build that clamps
// internally — which is the vacuity this suite has been finding elsewhere. The
// no-clamp decision is observable as the refusal table above, and nowhere
// else.

// ---------------------------------------------------------------------------
// D-30: two Limits fields each inside their own range and fatal together.
//
// QA-2's re-verification of checkpoint 3 measured it: at HeartbeatInterval=2s
// and HeartbeatTimeout=1s — both accepted by New, both inside every range D-23
// checks — a client echoing every heartbeat was closed 4010 HEARTBEAT_TIMEOUT
// after ZERO heartbeats, because onTick evaluates the liveness deadline on a
// ticker of HeartbeatInterval and does it before sending the solicitation the
// client would have answered. The close reason blames the peer for a value the
// operator set.
//
// These specs are the boundary, in the same package and the same shape as
// D-14's and D-23's above: what is asserted is that the configuration never
// reaches a session. The timed observation of the failure is QA-2's, in the
// chaos suite, and is deliberately not duplicated here — a spec that has to
// wait four heartbeat intervals to fail belongs where the clock already does.
// ---------------------------------------------------------------------------

var _ = Describe("New, validating the two heartbeat fields against each other (D-30)", func() {
	d := session.DefaultLimits()

	DescribeTable("refuses a timeout no client can satisfy",
		func(interval, timeout time.Duration) {
			cfg := validConfig()
			cfg.Limits.HeartbeatInterval = interval
			cfg.Limits.HeartbeatTimeout = timeout

			_, err := live.New(cfg)

			var cfgErr *live.ConfigError
			Expect(errors.As(err, &cfgErr)).To(BeTrue(),
				"New started an application on HeartbeatInterval=%s, HeartbeatTimeout=%s, which closes "+
					"every quiet session 4010 on a %s cycle for ever; got %v",
				interval, timeout, interval, err)
			Expect(cfgErr.Field).To(Equal("Limits.HeartbeatTimeout"))
			// The message has to carry both values, because one of them may be
			// a default the operator never wrote and the whole defect is that
			// the evidence points at the client.
			Expect(cfgErr.Error()).To(ContainSubstring("HeartbeatInterval"))
			Expect(cfgErr.Error()).To(ContainSubstring("4010"))
		},
		// QA-2's measured reproduction, verbatim.
		Entry("QA-2's measured pair: 2s interval, 1s timeout", 2*time.Second, time.Second),
		// The case reachable from D-23's own error message: the ceiling that
		// message recommends, against the timeout default it does not mention.
		// Zero means "take the default", which is the point.
		Entry("the ceiling D-23's message names, against the untouched default", 5*time.Minute, time.Duration(0)),
		// "An entirely ordinary thing to write", per QA-2's report.
		Entry("a one-minute interval against the untouched default", time.Minute, time.Duration(0)),
		// Equality: a timeout of exactly one interval is the bare failure, since
		// the first tick finds the deadline exactly met and `>` is not `>=`.
		Entry("a timeout of exactly one interval", 10*time.Second, 10*time.Second),
		// One nanosecond below the boundary. This is the entry that survives a
		// fix written against a round number instead of against the rule.
		Entry("one nanosecond below two intervals", 10*time.Second, 20*time.Second-time.Nanosecond),
		// The other direction: the operator sets only the timeout, wanting
		// faster dead-peer detection, and the default interval is too coarse
		// for it.
		Entry("a tightened timeout against the untouched default interval", time.Duration(0), 30*time.Second),
	)

	DescribeTable("accepts a pair a client can answer",
		func(interval, timeout time.Duration) {
			cfg := validConfig()
			cfg.Limits.HeartbeatInterval = interval
			cfg.Limits.HeartbeatTimeout = timeout

			app, err := live.New(cfg)

			Expect(err).NotTo(HaveOccurred())
			Expect(app).NotTo(BeNil())
		},
		Entry("both zero, which takes the defaults", time.Duration(0), time.Duration(0)),
		Entry("the defaults, written out", d.HeartbeatInterval, d.HeartbeatTimeout),
		// The boundary itself is accepted, and it is the boundary the rule
		// names rather than a margin above it.
		Entry("exactly two intervals", 10*time.Second, 20*time.Second),
		// QA-2's control pair, which their chaos spec watches stay alive.
		Entry("QA-2's control pair: 1s interval, 5s timeout", time.Second, 5*time.Second),
		// The interval ceiling, paired coherently. An operator who wants a
		// five-minute heartbeat can still have one; they have to say what
		// dead-peer detection then costs.
		Entry("the interval ceiling with a timeout to match", 5*time.Minute, 10*time.Minute),
		// Only the timeout set, comfortably above two default intervals.
		Entry("a lengthened timeout against the untouched default interval", time.Duration(0), 5*time.Minute),
	)

	It("holds the library's own defaults to the rule it enforces", func() {
		// The refusal above quotes the defaults as the example to copy, and the
		// error path for "both values are defaults" says it would be a library
		// bug. This is what makes that claim checkable rather than a comment: if
		// either default moves such that the pair stops being coherent, this
		// fails here instead of in every application that never mentions
		// Limits.
		Expect(d.HeartbeatTimeout).To(BeNumerically(">=", 2*d.HeartbeatInterval),
			"the default HeartbeatTimeout (%s) is below two default HeartbeatIntervals (%s), so the "+
				"configuration this library ships would be refused by its own validator and every "+
				"application that never sets Limits fails to start",
			d.HeartbeatTimeout, 2*d.HeartbeatInterval)
	})

	It("prefers the range error when the interval is out of range as well", func() {
		// Ordering, asserted rather than assumed: an interval below the
		// protocol's floor is ALSO below any coherent timeout, so both checks
		// have an opinion. The useful one is that the field is illegal.
		cfg := validConfig()
		cfg.Limits.HeartbeatInterval = 500 * time.Millisecond
		cfg.Limits.HeartbeatTimeout = 100 * time.Millisecond

		_, err := live.New(cfg)

		var cfgErr *live.ConfigError
		Expect(errors.As(err, &cfgErr)).To(BeTrue())
		Expect(cfgErr.Field).To(Equal("Limits.HeartbeatInterval"),
			"an interval the protocol refuses outright was reported as a relational defect, which "+
				"sends the operator to raise a timeout for a value that is illegal on its own")
	})
})
