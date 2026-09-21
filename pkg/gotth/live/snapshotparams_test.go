package live_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	pb "github.com/candacelabs/csf/pkg/gotth/internal/protocol/gotthlivepb"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// ---------------------------------------------------------------------------
// D-23: the three Limits fields the mount Snapshot carries as refined session
// parameters.
//
// HeartbeatInterval, MaxInboundFrameBytes and AckWindow are copied into the
// first frame of every session, into fields the schema refines:
//
//	heartbeat_interval_ms    this >= 1000 && this <= 300000
//	max_inbound_frame_bytes  this >= 1024 && this <= 1048576
//	ack_window               this >= 1 && this <= 256
//
// Until this file they were validated nowhere. live.New accepted a
// HeartbeatInterval of 500ms — which is an entirely reasonable thing for an
// operator to write — and then every session on that server died at
// establishment with Error{INTERNAL} "the server could not encode an update",
// above a log line saying the frame "was built by this library, so this is not
// a client problem". A startup mistake that presents as a library bug at
// runtime, and sends the person holding the pager to the wrong repository.
//
// It is D-14's defect class, three fields wider, in the function that was
// extended to close D-14 — so it is closed the same way and in the same place:
// refuse at construction, name the field, quote the range and whose rule it
// is. The last spec here is the one that would have caught the class rather
// than the three instances, and it is written to cover the fields nobody
// thought of: it walks Limits by reflection and requires that every
// configuration New accepts actually mounts.
// ---------------------------------------------------------------------------

var _ = Describe("New, validating the Limits the mount snapshot carries (D-23)", func() {
	DescribeTable("refuses a session parameter the protocol's refinement does not admit",
		func(field string, set func(limits *live.Limits)) {
			cfg := validConfig()
			set(&cfg.Limits)

			_, err := live.New(cfg)

			var cfgErr *live.ConfigError
			Expect(errors.As(err, &cfgErr)).To(BeTrue(),
				"live.New accepted a %s the mount Snapshot cannot carry, so every session on this "+
					"configuration dies at establishment with Error{INTERNAL} and the operator is "+
					"told it is a library bug; got %v", field, err)
			Expect(cfgErr.Field).To(Equal("Limits."+field),
				"the rejection named %q, so the operator is sent to the wrong field", cfgErr.Field)
		},

		// HeartbeatInterval. The floor is reachable in two ways that read
		// differently and narrow the same: an interval below one second, and
		// one below a single millisecond, which is zero on a wire that counts
		// whole milliseconds and is not zero as a Duration, so it does not take
		// the default either.
		Entry("HeartbeatInterval one millisecond below the floor", "HeartbeatInterval",
			func(l *live.Limits) { l.HeartbeatInterval = 999 * time.Millisecond }),
		Entry("HeartbeatInterval at QA-2's reachable value", "HeartbeatInterval",
			func(l *live.Limits) { l.HeartbeatInterval = 500 * time.Millisecond }),
		Entry("HeartbeatInterval at the value the chaos suite first hit", "HeartbeatInterval",
			func(l *live.Limits) { l.HeartbeatInterval = 300 * time.Millisecond }),
		Entry("HeartbeatInterval below one whole millisecond", "HeartbeatInterval",
			func(l *live.Limits) { l.HeartbeatInterval = 500 * time.Microsecond }),
		Entry("HeartbeatInterval one millisecond above the ceiling", "HeartbeatInterval",
			func(l *live.Limits) { l.HeartbeatInterval = 300*time.Second + time.Millisecond }),
		Entry("HeartbeatInterval an hour above the ceiling", "HeartbeatInterval",
			func(l *live.Limits) { l.HeartbeatInterval = time.Hour }),

		// MaxInboundFrameBytes. Both ends are reachable as plain integers.
		Entry("MaxInboundFrameBytes one byte below the floor", "MaxInboundFrameBytes",
			func(l *live.Limits) { l.MaxInboundFrameBytes = 1023 }),
		Entry("MaxInboundFrameBytes at one, the smallest non-default", "MaxInboundFrameBytes",
			func(l *live.Limits) { l.MaxInboundFrameBytes = 1 }),
		Entry("MaxInboundFrameBytes one byte above the ceiling", "MaxInboundFrameBytes",
			func(l *live.Limits) { l.MaxInboundFrameBytes = (1 << 20) + 1 }),

		// AckWindow. Only the ceiling is reachable: below the floor of 1 there
		// is 0, which means "take the default", and below that a negative,
		// which the whole-struct check already refuses by name. The floor is
		// asserted in the acceptance table instead, where 1 must mount.
		Entry("AckWindow one above the ceiling", "AckWindow",
			func(l *live.Limits) { l.AckWindow = 257 }),
		Entry("AckWindow an order of magnitude above the ceiling", "AckWindow",
			func(l *live.Limits) { l.AckWindow = 4096 }),
	)

	// The narrowing, which is the reason the check is on an int64 and not on
	// the uint32 the frame carries. Each of these three values is enormous and
	// each of them lands inside its range once truncated to 32 bits, so a check
	// written against the wire type would accept all three and configure a
	// heartbeat of seven weeks.
	It("refuses a value that is only in range once narrowed to the wire's uint32", func() {
		if strconv.IntSize < 64 {
			Skip("an int field cannot hold a value above 2^32 on a 32-bit platform, " +
				"so two thirds of this spec is not expressible here")
		}
		const wrap = int64(1) << 32

		byField := map[string]func(limits *live.Limits){
			"HeartbeatInterval": func(l *live.Limits) {
				l.HeartbeatInterval = time.Duration(wrap+20000) * time.Millisecond
			},
			"MaxInboundFrameBytes": func(l *live.Limits) {
				l.MaxInboundFrameBytes = int(wrap) + 65536
			},
			"AckWindow": func(l *live.Limits) {
				l.AckWindow = int(wrap) + 16
			},
		}

		for field, set := range byField {
			cfg := validConfig()
			set(&cfg.Limits)

			_, err := live.New(cfg)

			var cfgErr *live.ConfigError
			Expect(errors.As(err, &cfgErr)).To(BeTrue(),
				"live.Limits.%s was accepted at 2^32 plus its default, which is the default again "+
					"once the actor narrows it to uint32 for the wire: the check is reading the "+
					"truncated value rather than the one the operator set; got %v", field, err)
			Expect(cfgErr.Field).To(Equal("Limits." + field))
		}
	})

	// FR-58: the error names the field, the offending value, the range, and
	// whose rule the range is — the last because "out of range" from a library
	// is an assertion, and the predicate is the evidence for it.
	It("says what to set HeartbeatInterval to instead, and on whose authority", func() {
		cfg := validConfig()
		cfg.Limits.HeartbeatInterval = 500 * time.Millisecond

		_, err := live.New(cfg)

		Expect(err).To(MatchError(ContainSubstring("Limits.HeartbeatInterval")))
		// The value as the operator wrote it, not as the wire would carry it.
		Expect(err).To(MatchError(ContainSubstring("500ms")))
		// The range, in the units the field is set in, so it can be pasted back
		// into the configuration it came from.
		Expect(err).To(MatchError(ContainSubstring("between 1s and 5m0s")))
		Expect(err).To(MatchError(ContainSubstring("default of 20s")))
		// Where the range comes from: the refinement, quoted, and the field it
		// is declared on.
		Expect(err).To(MatchError(ContainSubstring("gotthlive.v1.Snapshot.heartbeat_interval_ms")))
		Expect(err).To(MatchError(ContainSubstring("this >= 1000 && this <= 300000")))
		// Literals throughout, deliberately, as in the CoalesceFlushAt spec
		// this follows: interpolating the same constants the message
		// interpolates would agree with any value at all, including the empty
		// one a botched format string produces.
	})

	DescribeTable("says what to set the other two to instead",
		func(field string, set func(limits *live.Limits), want ...string) {
			cfg := validConfig()
			set(&cfg.Limits)

			_, err := live.New(cfg)

			Expect(err).To(MatchError(ContainSubstring("Limits." + field)))
			for _, s := range want {
				Expect(err).To(MatchError(ContainSubstring(s)))
			}
		},
		Entry("MaxInboundFrameBytes", "MaxInboundFrameBytes",
			func(l *live.Limits) { l.MaxInboundFrameBytes = 512 },
			"512", "between 1024 and 1048576", "default of 65536",
			"gotthlive.v1.Snapshot.max_inbound_frame_bytes", "this >= 1024 && this <= 1048576"),
		Entry("AckWindow", "AckWindow",
			func(l *live.Limits) { l.AckWindow = 257 },
			"257", "between 1 and 256", "default of 16",
			"gotthlive.v1.Snapshot.ack_window", "this >= 1 && this <= 256"),
	)

	// The other half of a boundary: the values inside it must not merely be
	// accepted by New, they must mount, and the value the operator set must be
	// the value the client is told. Reading it back off the wire is what ties
	// live's three unit conversions to the actor's — the two places that know
	// HeartbeatInterval is milliseconds on the wire — so they cannot disagree
	// without this going red.
	DescribeTable("accepts every value the refinement admits, and carries it to the client",
		func(set func(limits *live.Limits), read func(snapshot *pb.Snapshot) uint32, want uint32) {
			m := mount(func(c *live.Config[counter, user]) { set(&c.Limits) })
			defer m.stop()

			Expect(read(m.snapshot)).To(Equal(want),
				"the session mounted but announced a session parameter other than the one "+
					"configured, so live and the actor disagree about the conversion")
		},

		Entry("HeartbeatInterval at the floor",
			func(l *live.Limits) { l.HeartbeatInterval = time.Second },
			(*pb.Snapshot).GetHeartbeatIntervalMs, uint32(1000)),
		// The ceiling now has to carry a HeartbeatTimeout with it, and this
		// entry is where D-30 was hiding in plain sight: until the relational
		// check landed, this spec asserted that a 5m interval against the
		// untouched 50 s default MOUNTS — which it did, and then closed every
		// quiet session on it 4010 for ever. The entry's subject is unchanged
		// (the ceiling reaches the wire unconverted); what it no longer also
		// asserts is that the pair is startable.
		Entry("HeartbeatInterval at the ceiling, with a timeout that can be met",
			func(l *live.Limits) {
				l.HeartbeatInterval = 300 * time.Second
				l.HeartbeatTimeout = 600 * time.Second
			},
			(*pb.Snapshot).GetHeartbeatIntervalMs, uint32(300000)),
		Entry("HeartbeatInterval at the default, set explicitly",
			func(l *live.Limits) { l.HeartbeatInterval = 20 * time.Second },
			(*pb.Snapshot).GetHeartbeatIntervalMs, uint32(20000)),

		Entry("MaxInboundFrameBytes at the floor",
			func(l *live.Limits) { l.MaxInboundFrameBytes = 1024 },
			(*pb.Snapshot).GetMaxInboundFrameBytes, uint32(1024)),
		Entry("MaxInboundFrameBytes at the ceiling",
			func(l *live.Limits) { l.MaxInboundFrameBytes = 1 << 20 },
			(*pb.Snapshot).GetMaxInboundFrameBytes, uint32(1<<20)),
		Entry("MaxInboundFrameBytes at the default, set explicitly",
			func(l *live.Limits) { l.MaxInboundFrameBytes = 65536 },
			(*pb.Snapshot).GetMaxInboundFrameBytes, uint32(65536)),

		Entry("AckWindow at the floor",
			func(l *live.Limits) { l.AckWindow = 1 },
			(*pb.Snapshot).GetAckWindow, uint32(1)),
		Entry("AckWindow at the ceiling",
			func(l *live.Limits) { l.AckWindow = 256 },
			(*pb.Snapshot).GetAckWindow, uint32(256)),
		Entry("AckWindow at the default, set explicitly",
			func(l *live.Limits) { l.AckWindow = 16 },
			(*pb.Snapshot).GetAckWindow, uint32(16)),
	)

	It("still takes the defaults, which are inside every range", func() {
		// Zero must stay valid in all three, or every application that never
		// mentions Limits fails to start — and the default each zero becomes
		// must itself mount, which is the half a construction-time spec alone
		// would not see.
		m := mount(nil)
		defer m.stop()

		d := session.DefaultLimits()
		Expect(m.snapshot.GetHeartbeatIntervalMs()).
			To(Equal(uint32(d.HeartbeatInterval / time.Millisecond)))
		Expect(m.snapshot.GetMaxInboundFrameBytes()).To(Equal(uint32(d.MaxInboundFrameBytes)))
		Expect(m.snapshot.GetAckWindow()).To(Equal(uint32(d.AckWindow)))
	})

	// -----------------------------------------------------------------------
	// The property, and the reason this file is not three range checks.
	//
	// D-23 is not "three fields were forgotten". It is that nothing anywhere
	// held the invariant those three fields violate: a configuration live.New
	// accepts must produce a mount Snapshot this library can encode. Three more
	// range checks close the three known instances and leave the class open,
	// and the fourth field to reach a refined wire value would arrive the same
	// way — silently, and only in production.
	//
	// So this spec knows nothing about which fields matter. It walks Limits by
	// reflection, sets each field in turn to values chosen for their kind
	// rather than for their meaning, and for every configuration New accepts it
	// mounts a real session over a real socket and requires the first frame to
	// be the Snapshot. A field added tomorrow is covered on the day it is
	// added, and so is one that exists today and nobody noticed.
	// -----------------------------------------------------------------------
	It("mounts every configuration it accepts", func() {
		t := reflect.TypeOf(live.Limits{})
		Expect(t.NumField()).To(BeNumerically(">", 0))

		accepted, refused := 0, 0
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			probes := probeValues(f.Type)
			Expect(probes).NotTo(BeEmpty(),
				"live.Limits.%s is a %s, which this spec does not know how to probe: teach "+
					"probeValues the kind, or the field joins the class D-23 was about",
				f.Name, f.Type.Kind())

			for _, probe := range probes {
				cfg := validConfig()
				reflect.ValueOf(&cfg.Limits).Elem().Field(i).Set(probe)

				app, err := live.New(cfg)
				if err != nil {
					// Refused at construction, which is the whole point: the
					// property is about what New accepts.
					refused++
					continue
				}
				accepted++

				frame, ferr := firstFrameOf(app)
				Expect(ferr).NotTo(HaveOccurred(),
					"live.New accepted Limits.%s = %v and the session it mounted never produced "+
						"a first frame: %v", f.Name, probe, ferr)
				Expect(frame.GetError()).To(BeNil(),
					"live.New accepted Limits.%s = %v and the session's first frame was an Error "+
						"rather than the mount Snapshot H-10 requires: %s. A configuration this "+
						"library admits must never build a frame it then refuses to send",
					f.Name, probe, frame.GetError().GetMessage())
				Expect(frame.GetSnapshot()).NotTo(BeNil(),
					"live.New accepted Limits.%s = %v and the first frame was a %T rather than a "+
						"Snapshot", f.Name, probe, frame.GetPayload())
			}
		}

		// Both counters are reported rather than asserted at a number: the
		// figure moves whenever a field or a probe is added, and pinning it
		// would make this spec fail for the one reason it is not about. What is
		// asserted is that the loop did both things, because a probe table that
		// stopped producing rejections — or acceptances — would leave this
		// passing vacuously.
		AddReportEntry("configurations mounted", accepted)
		AddReportEntry("configurations refused at New", refused)
		Expect(accepted).To(BeNumerically(">", 0))
		Expect(refused).To(BeNumerically(">", 0),
			"no probe was refused at construction, so this spec is no longer exercising the "+
				"validation it exists to hold")
	})
})

// probeValues are the values one Limits field is set to by the property above.
//
// They are chosen by kind and not by field, deliberately: a table that knew
// what each field meant would probe exactly the fields somebody had already
// thought about, which is the failure D-23 is. What each kind needs is one
// value near the small end and one far past the large end of anything a
// protocol predicate is likely to admit, since a range is violated from one
// side or the other.
//
// Durations are probed at 500ms rather than at a nanosecond. A nanosecond is a
// better probe of a range and a worse probe of everything else: it is also
// WriteDeadline, HeartbeatTimeout, IdleTimeout and SlowClientGrace, each of
// which would then evict or fail the session before its first frame for a
// reason that has nothing to do with encoding, and it would do so as a race
// against the mount. 500ms is below every millisecond-denominated protocol
// floor — it is D-23's own reachable value — and is comfortably above the time
// a loopback mount takes.
func probeValues(t reflect.Type) []reflect.Value {
	as := func(v any) reflect.Value { return reflect.ValueOf(v).Convert(t) }
	switch t.Kind() {
	case reflect.Int64:
		// time.Duration and nothing else, today.
		return []reflect.Value{
			as(500 * time.Millisecond),
			as(24 * time.Hour),
		}
	case reflect.Int:
		return []reflect.Value{as(1), as(1 << 20)}
	case reflect.Float64:
		return []reflect.Value{as(0.5), as(1e6)}
	default:
		return nil
	}
}

// firstFrameOf dials app and returns its session's first frame.
//
// It asserts nothing about that frame, which is the difference between it and
// mount: the property above exists to find out what the first frame turns out
// to be, so a helper that already required a Snapshot would fail with the
// wrong message for the wrong reason.
func firstFrameOf(app *live.App[counter, user]) (*pb.Frame, error) {
	ts := httptest.NewServer(app.Handler())
	defer ts.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer func() { _ = app.Close(ctx) }()

	headers := http.Header{}
	headers.Set("Origin", "https://app.example")
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http"),
		&websocket.DialOptions{HTTPHeader: headers, Subprotocols: []string{"gotth-live.v1"}})
	if err != nil {
		return nil, err
	}
	defer conn.CloseNow()

	_, data, err := conn.Read(ctx)
	if err != nil {
		return nil, err
	}
	var f pb.Frame
	if err := proto.Unmarshal(data, &f); err != nil {
		return nil, err
	}
	return &f, nil
}

// Deliberately not written: a spec asserting that the three ranges are the
// numbers above. They are held against the generated refinement in
// internal/protocol/sessionparams_test.go, which asks the compiled predicate
// rather than a second copy of the literals — and a spec here that restated
// protocol.HeartbeatIntervalMSRange.Min would agree with whatever that field
// happened to say.
