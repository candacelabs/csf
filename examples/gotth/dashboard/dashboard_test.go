package main

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

// The specs in this file are about the application in isolation: the reducer,
// the state's render helpers, the feed, and the two things main.go does that can
// be wrong before a connection exists. Everything that is a claim about what
// reaches a browser lives in wire_test.go, measured on the frames.

// baseTime is the clock the feed is given in specs.
//
// A fixed clock is what makes a rendered timestamp reproducible; the sample
// number is what keeps two readings distinguishable anyway, which is the whole
// argument in Reading.Seq's doc comment and is worth having a spec depend on.
var baseTime = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

// sampleEvent builds the event the feed emits for one reading, the way
// update.event does, so a reducer spec exercises the same field vocabulary the
// pump produces rather than a convenient invention.
func sampleEvent(seq uint64, at int64, values ...int) live.Event {
	fields := map[string]string{
		fieldSeq:     strconv.FormatUint(seq, 10),
		fieldAtMilli: strconv.FormatInt(at, 10),
		fieldVersion: strconv.FormatUint(seq, 10),
	}
	for i, name := range Series {
		if i < len(values) {
			fields[name] = strconv.Itoa(values[i])
		}
	}
	return live.Event{Name: EventSample, Fields: live.NewFields(fields)}
}

func alertEvent(seq, version uint64, series string, value int) live.Event {
	return live.Event{Name: EventAlert, Fields: live.NewFields(map[string]string{
		fieldSeq:     strconv.FormatUint(seq, 10),
		fieldAtMilli: strconv.FormatInt(baseTime.UnixMilli(), 10),
		fieldVersion: strconv.FormatUint(version, 10),
		fieldSeries:  series,
		fieldValue:   strconv.Itoa(value),
	})}
}

// reduce is the reducer under test, bound to a feed the pure specs never reach:
// the transition builds effects that close over it, and a spec that cares about
// what an effect DOES builds its own feed and runs the effect.
var reduce = Reducer(NewFeed(1, time.Hour))

// sources projects what a transition scheduled into the one thing a
// specification can compare: live.Effect[live.AnonymousIdentity] carries its behaviour in a function
// field, and Go cannot compare two function values.
func sources(effects []live.Effect[live.AnonymousIdentity]) []string {
	if len(effects) == 0 {
		return nil
	}
	names := make([]string, 0, len(effects))
	for _, effect := range effects {
		names = append(names, effect.Source)
	}
	return names
}

var _ = Describe("The reducer", func() {
	It("folds a reading into the meters and the history", func() {
		state, effects := reduce(State{}, sampleEvent(1, baseTime.UnixMilli(), 40, 50, 60))

		Expect(effects).To(BeEmpty(), "a reading is data, not a reason to do anything")
		Expect(state.Reading("cpu")).To(Equal(40))
		Expect(state.Reading("memory")).To(Equal(50))
		Expect(state.Reading("requests")).To(Equal(60))
		Expect(state.SampleSeq()).To(Equal(uint64(1)))
		Expect(state.Window.values()).To(Equal([]int{40}),
			"the sparkline follows the first series and nothing else")
	})

	It("leaves a paused session's state untouched, which is what makes pausing free", func() {
		paused := State{Paused: true}
		next, effects := reduce(paused, sampleEvent(1, baseTime.UnixMilli(), 40, 50, 60))

		Expect(effects).To(BeEmpty())
		// Identity, not equality. An unchanged state is what the library's own
		// comparison sees, so no fragment is asked whether it is dirty and no
		// patch is built. A reducer that returned an equal-but-rebuilt state
		// would pass an equality assertion and cost a render per sample.
		Expect(next).To(Equal(paused))
		Expect(next.Meters).To(BeNil())
	})

	It("ignores a reading it has already passed", func() {
		state, _ := reduce(State{}, sampleEvent(7, baseTime.UnixMilli(), 40, 50, 60))
		late, _ := reduce(state, sampleEvent(3, baseTime.UnixMilli(), 99, 99, 99))

		Expect(late).To(Equal(state), "a late or duplicated delivery must not move the meters backwards")
	})

	It("ignores a reading whose fields are missing or unparseable", func() {
		state, _ := reduce(State{}, sampleEvent(1, baseTime.UnixMilli(), 40, 50, 60))

		for _, bad := range []live.Event{
			{Name: EventSample, Fields: live.NewFields(map[string]string{fieldSeq: "not a number"})},
			{Name: EventSample, Fields: live.NewFields(map[string]string{
				fieldSeq: "9", fieldAtMilli: "also not a number"})},
			// Every series must be present: a partial reading rendered beside a
			// stale one would show three numbers taken at two different times.
			{Name: EventSample, Fields: live.NewFields(map[string]string{
				fieldSeq: "9", fieldAtMilli: "1", "cpu": "10"})},
		} {
			next, effects := reduce(state, bad)
			Expect(effects).To(BeEmpty())
			Expect(next).To(Equal(state), "a malformed emission must not half-apply")
		}
	})

	It("trims the sparkline window and never grows past MaxWindow", func() {
		state := State{}
		for i := 1; i <= MaxWindow*2; i++ {
			state, _ = reduce(state, sampleEvent(uint64(i), baseTime.UnixMilli(), i%100, 1, 1))
		}
		Expect(state.Window.values()).To(HaveLen(MaxWindow))
		Expect(state.Window.values()[MaxWindow-1]).To(Equal((MaxWindow*2)%100),
			"the newest value is last")
	})

	It("folds alerts in feed-revision order and refuses a stale revision", func() {
		state, _ := reduce(State{}, alertEvent(4, 9, "cpu", 95))
		Expect(state.AlertEntries()).To(HaveLen(1))
		Expect(state.AlertVersion()).To(Equal(uint64(9)))

		stale, _ := reduce(state, alertEvent(2, 5, "memory", 92))
		Expect(stale).To(Equal(state), "a revision this session has already passed carries nothing new")

		newer, _ := reduce(state, alertEvent(6, 11, "memory", 92))
		Expect(newer.AlertEntries()).To(HaveLen(2))
	})

	It("empties the alert log when somebody clears it, and only on a newer revision", func() {
		state, _ := reduce(State{}, alertEvent(4, 9, "cpu", 95))

		cleared, effects := reduce(state, live.Event{Name: EventCleared,
			Fields: live.NewFields(map[string]string{fieldVersion: "10"})})
		Expect(effects).To(BeEmpty())
		Expect(cleared.AlertEntries()).To(BeEmpty())
		Expect(cleared.AlertVersion()).To(Equal(uint64(10)))
	})

	It("turns a probe into an effect carrying the event that asked for it", func() {
		_, effects := reduce(State{}, live.Event{Name: EventProbe, ID: 41})

		Expect(sources(effects)).To(Equal([]string{SourceProbe}),
			"without the causal edge the patch that shows the reading names only the subscription")
	})

	It("turns a clear into an effect carrying the event that asked for it", func() {
		_, effects := reduce(State{}, live.Event{Name: EventClear, ID: 42})
		Expect(sources(effects)).To(Equal([]string{SourceClear}))
	})

	It("pauses and resumes only this session", func() {
		paused, _ := reduce(State{}, live.Event{Name: EventPause})
		Expect(paused.Paused).To(BeTrue())
		Expect(paused.FeedLabel()).To(Equal("paused"))

		resumed, _ := reduce(paused, live.Event{Name: EventResume})
		Expect(resumed.Paused).To(BeFalse())
		Expect(resumed.FeedLabel()).To(Equal("live"))
	})

	It("records the library's backpressure signal and clears it on recovery", func() {
		degraded, effects := reduce(State{}, live.Event{Name: live.SlowClientEvent})
		Expect(effects).To(BeEmpty())
		Expect(degraded.Degraded).To(BeTrue())
		Expect(degraded.StatusLabel()).To(ContainSubstring("falling behind"))

		recovered, _ := reduce(degraded, live.Event{Name: live.ClientRecoveredEvent})
		Expect(recovered.Degraded).To(BeFalse())
		Expect(recovered.StatusLabel()).To(Equal("keeping up"))
	})

	Describe("a failed effect", func() {
		failure := func(source string, retryable bool) live.Event {
			return live.Event{Name: live.EffectFailedEvent, Fields: live.NewFields(map[string]string{
				live.EffectFailedSourceField:    source,
				live.EffectFailedErrorField:     "dial tcp: connection refused to 10.0.0.7:5432",
				live.EffectFailedRetryableField: strconv.FormatBool(retryable),
			})}
		}

		It("re-subscribes when a retryable subscription dies, because a dashboard that stops learning looks right", func() {
			state, effects := reduce(State{}, failure(SourceSubscribe, true))

			Expect(sources(effects)).To(Equal([]string{SourceSubscribe}))
			Expect(state.Notice).To(ContainSubstring(SourceSubscribe))
		})

		It("does not retry a failure the library did not classify as retryable", func() {
			_, effects := reduce(State{}, failure(SourceSubscribe, false))
			Expect(effects).To(BeEmpty())
		})

		It("does not retry a different effect even when it is retryable", func() {
			_, effects := reduce(State{}, failure(SourceProbe, true))
			Expect(effects).To(BeEmpty(), "one probe that failed is not a session that stopped learning")
		})

		It("keeps the error's own message out of the state that renders", func() {
			state, _ := reduce(State{}, failure(SourceSubscribe, true))

			// EffectFailedErrorField carries an error string or a raw panic
			// value, unredacted, in production, ungated by Config.Dev. The
			// source is a name this application chose and is safe; the detail
			// is not, and this spec is what stops somebody helpfully rendering
			// it later.
			Expect(state.Notice).NotTo(ContainSubstring("10.0.0.7"))
			Expect(state.Notice).NotTo(ContainSubstring("connection refused"))
		})
	})

	It("ignores a synthesized name it has no answer for", func() {
		state := State{Paused: true}
		next, effects := reduce(state, live.Event{Name: "gotth.some_future_signal"})

		Expect(effects).To(BeEmpty())
		Expect(next).To(Equal(state))
	})
})

var _ = Describe("Determinism and dirty declarations", func() {
	// A log with one of everything, including the two the library synthesizes
	// and the failure event, because a reducer is replayed over whatever it was
	// actually sent and not over the happy path.
	log := []live.Event{
		sampleEvent(1, baseTime.UnixMilli(), 40, 50, 60),
		{Name: EventProbe, ID: 7},
		sampleEvent(2, baseTime.UnixMilli(), 91, 50, 60),
		alertEvent(2, 3, "cpu", 91),
		{Name: EventPause},
		sampleEvent(3, baseTime.UnixMilli(), 20, 20, 20),
		{Name: EventResume},
		sampleEvent(4, baseTime.UnixMilli(), 30, 30, 30),
		{Name: live.SlowClientEvent},
		{Name: live.ClientRecoveredEvent},
		{Name: EventClear, ID: 8},
		{Name: EventCleared, Fields: live.NewFields(map[string]string{fieldVersion: "9"})},
		{Name: live.EffectFailedEvent, Fields: live.NewFields(map[string]string{
			live.EffectFailedSourceField:    SourceSubscribe,
			live.EffectFailedRetryableField: "true",
		})},
	}

	It("produces the same state and the same effects on every replay", func() {
		livetest.ReplayN(GinkgoTB(), reduce, State{}, log, 32)
	})

	It("declares every fragment that moved", func() {
		// The helper only catches UNDER-declaring, which is the mistake that
		// produces a stale region in production and nothing at all in
		// development. Over-declaring passes it and is what FR-62's
		// independent-regions property is actually about, so that half is
		// asserted on the frames in wire_test.go instead.
		feed := NewFeed(1, time.Millisecond)
		livetest.AssertDirtyComplete(GinkgoTB(), Config(feed, []string{"http://127.0.0.1:8082"}), State{}, log)
	})
})

var _ = Describe("The state's render helpers", func() {
	It("renders a zero state, which is what the first HTTP paint gets", func() {
		var s State

		Expect(s.SampleSeq()).To(BeZero())
		Expect(s.SampleClock()).To(Equal("--:--:--"))
		Expect(s.Reading("cpu")).To(BeZero())
		Expect(s.Sparkline()).To(BeEmpty())
		Expect(s.AlertEntries()).To(BeEmpty())
		Expect(s.AlertCount()).To(Equal("0"))
		Expect(s.BarClass("cpu")).To(Equal("w0"))
	})

	It("buckets a reading into the eleven classes the stylesheet knows", func() {
		for reading, want := range map[int]string{0: "w0", 5: "w0", 10: "w1", 73: "w7", 100: "w10"} {
			s := State{Meters: &Reading{Values: []int{reading, 0, 0}}}
			Expect(s.BarClass("cpu")).To(Equal(want), "reading %d", reading)
		}
	})

	It("classifies a reading against the alert threshold", func() {
		level := func(v int) string {
			return State{Meters: &Reading{Values: []int{v, 0, 0}}}.Level("cpu")
		}
		Expect(level(10)).To(Equal("ok"))
		Expect(level(71)).To(Equal("high"))
		Expect(level(AlertAbove + 1)).To(Equal("over"))
		Expect(level(AlertAbove)).To(Equal("high"), "the threshold is crossed, not reached")
	})

	It("formats a stamp the feed took rather than reading a clock", func() {
		s := State{Meters: &Reading{Seq: 1, AtUnixMilli: baseTime.UnixMilli()}}

		// Twice, because a render that read a clock would be a render that
		// produces different bytes for the same state — and the patch
		// suppression that compares those bytes is what keeps an unchanged
		// region off the wire.
		Expect(s.SampleClock()).To(Equal("12:00:00"))
		Expect(s.SampleClock()).To(Equal("12:00:00"))
	})

	It("draws a sparkline of exactly one block per remembered sample", func() {
		s := State{Window: &History{Values: []int{0, 50, 100}}}
		Expect([]rune(s.Sparkline())).To(HaveLen(3))
		Expect(s.Sparkline()).To(Equal(" ▄█"))
	})
})

var _ = Describe("The feed", func() {
	It("raises one alert per crossing rather than one per sample above it", func() {
		feed := NewFeed(1, time.Hour)
		feed.now = func() time.Time { return baseTime }

		// Driven directly rather than through the random walk: the property is
		// about the edge, and a spec that waited for the walk to cross would be
		// asserting about the seed.
		feed.values = []int{AlertAbove + 5, 0, 0}
		feed.step = func(v int) int { return v }

		feed.Sample(live.ID{}, 0)
		Expect(feed.Alerts().Entries).To(HaveLen(1))

		feed.Sample(live.ID{}, 0)
		feed.Sample(live.ID{}, 0)
		Expect(feed.Alerts().Entries).To(HaveLen(1), "a series that sits above the threshold is not news")

		feed.values[0] = 10
		feed.Sample(live.ID{}, 0)
		feed.values[0] = AlertAbove + 5
		feed.Sample(live.ID{}, 0)
		Expect(feed.Alerts().Entries).To(HaveLen(2), "coming back down re-arms the crossing")
	})

	It("trims the shared alert log without writing through a log it already handed out", func() {
		feed := NewFeed(1, time.Hour)
		feed.now = func() time.Time { return baseTime }
		feed.step = func(v int) int { return v }

		held := feed.Alerts()
		for i := 0; i < MaxAlerts*2; i++ {
			feed.values[0] = 10
			feed.Sample(live.ID{}, 0)
			feed.values[0] = AlertAbove + 5
			feed.Sample(live.ID{}, 0)
		}

		Expect(feed.Alerts().Entries).To(HaveLen(MaxAlerts))
		Expect(held.Entries).To(BeEmpty(),
			"an *AlertLog handed to a session is promised immutable; a slide over the old backing array breaks that")
	})

	It("registers and unregisters a session, which is what the pump and the leak check read", func() {
		feed := NewFeed(1, time.Hour)
		id := live.ID{1}

		reading, alerts := feed.Join(id)
		Expect(reading).NotTo(BeNil())
		Expect(alerts).NotTo(BeNil())
		Expect(feed.Subscribers()).To(Equal(1))

		feed.Leave(id)
		Expect(feed.Subscribers()).To(BeZero())
	})

	It("marks a session behind rather than blocking the sample that could not be delivered", func() {
		feed := NewFeed(1, time.Hour)
		feed.now = func() time.Time { return baseTime }
		id := live.ID{2}
		feed.Join(id)

		// Nothing is pumping this subscriber, so the backlog fills and then
		// overflows. The property is that Sample returns either way: one
		// session that stopped reading must not stop the feed for everybody.
		for i := 0; i < backlogDepth+8; i++ {
			feed.Sample(live.ID{}, 0)
		}

		Expect(feed.QueueDepth()).To(Equal(backlogDepth))
		Expect(feed.subs[id].behind.Load()).To(BeTrue())
	})

	It("stops once and stays stopped", func() {
		feed := NewFeed(1, time.Millisecond)
		feed.Start()
		feed.Stop()
		Expect(feed.Stop).NotTo(Panic(), "a shutdown path that panics on a second call is one nobody can call twice")
	})

	It("refuses to pump a session that was never joined", func() {
		feed := NewFeed(1, time.Hour)
		err := feed.pump(context.Background(), live.ID{3}, func(live.Event) error { return nil })

		Expect(err).To(HaveOccurred())
		Expect(err.Error()).To(ContainSubstring("Config.Init must Join"),
			"the error has to name the mistake, because the symptom is a dashboard that never updates")
	})

	// The spec that used to close this block — "has no executor for an effect
	// it does not know" — went with the executor. An effect this feed does not
	// own is one this feed cannot be handed, now that a live.Effect[live.AnonymousIdentity] carries its
	// own Run.
})

var _ = Describe("Startup", func() {
	Describe("the Origin allowlist", func() {
		It("names both loopback spellings, because a browser sends the host you typed", func() {
			Expect(allowedOrigins("127.0.0.1:8082", "")).To(ConsistOf(
				"http://127.0.0.1:8082", "http://localhost:8082"))
			Expect(allowedOrigins("localhost:8082", "")).To(ConsistOf(
				"http://localhost:8082", "http://127.0.0.1:8082"))
		})

		// The README's container invocation is "-addr 0.0.0.0:8082". No browser
		// ever sends 0.0.0.0 as an Origin, so without the bind-all arm the
		// documented way to run this example allows exactly one Origin nothing
		// can produce, and every upgrade is refused with 403. The wildcard spec
		// below already named this address and passed anyway: "not the wildcard"
		// is satisfied by an allowlist that matches nothing at all.
		It("names them for the bind-all address the README tells you to use", func() {
			Expect(allowedOrigins("0.0.0.0:8082", "")).To(ContainElements(
				"http://127.0.0.1:8082", "http://localhost:8082"))
		})

		It("appends what the operator asked for and nothing else", func() {
			Expect(allowedOrigins("127.0.0.1:8082", "http://192.168.1.10:8082 , ")).To(ContainElement(
				"http://192.168.1.10:8082"))
		})

		It("never produces the wildcard, whatever it is given", func() {
			for _, addr := range []string{"127.0.0.1:8082", "0.0.0.0:8082", "localhost:8082", ":8082"} {
				Expect(allowedOrigins(addr, "")).NotTo(ContainElement(live.AnyOrigin), "addr %q", addr)
			}
		})
	})

	Describe("the vendored HTMX bundle", func() {
		It("loads the artifact this repository recorded", func() {
			b, err := LoadHTMX(DefaultHTMXPath)

			Expect(err).NotTo(HaveOccurred(),
				"the path is relative to this directory, which is where the README says to run the example from")
			Expect(b).NotTo(BeEmpty())
		})

		It("refuses a file whose digest is not the recorded one", func() {
			path := filepath.Join(GinkgoT().TempDir(), "htmx.min.js")
			Expect(os.WriteFile(path, []byte("/* not htmx */"), 0o600)).To(Succeed())

			_, err := LoadHTMX(path)
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("will not serve bytes it cannot vouch for"))
			Expect(err.Error()).To(ContainSubstring(HTMXSHA256))
		})

		It("reports a missing bundle by path rather than by silence", func() {
			_, err := LoadHTMX(filepath.Join(GinkgoT().TempDir(), "absent.js"))
			Expect(err).To(HaveOccurred())
			Expect(err.Error()).To(ContainSubstring("could not read the HTMX bundle at"))
		})

		It("treats an empty path as an absent bundle rather than reading the directory", func() {
			_, err := LoadHTMX("")
			Expect(err).To(HaveOccurred())
		})
	})

	It("refuses a configuration with a hole in it rather than starting with one", func() {
		feed := NewFeed(1, time.Hour)
		cfg := Config(feed, nil)

		_, err := live.New(cfg)
		Expect(err).To(HaveOccurred(), "an empty Origins list is deny-by-default, not allow-everything")
		Expect(err.Error()).To(ContainSubstring("Origins"))
	})

	It("mounts the live handler somewhere other than /live, and the page agrees", func() {
		// MountPath being unusual is the point: the library used to default the
		// script tag to /live, so an application mounted anywhere else served a
		// page whose script 404'd, with no error anywhere on the server.
		Expect(MountPath).NotTo(Equal("/live"))
		Expect(strings.HasPrefix(MountPath, "/")).To(BeTrue())
	})
})
