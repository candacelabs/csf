package main

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/gotth/live/livetest"
)

// tabA and tabB stand for two sessions. A session identifier is sixteen bytes
// the server mints; these are fixed so a spec can assert on "this tab" versus
// "another tab" without a running server.
var (
	tabA = live.ID{0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf}
	tabB = live.ID{0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xbb, 0xbc, 0xbd, 0xbe, 0xbf}
)

// baseTime is the wall clock the specs use. Nothing under test reads a clock,
// so it is a constant rather than a fixture.
var baseTime = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

// session builds the live.Session[live.AnonymousIdentity] a Config hook is called with.
//
// This is what livetest.NewSession is for. Before it existed, Store.Execute had
// to be an adapter over an unexported method taking a live.ID, because a
// live.Session[live.AnonymousIdentity] cannot be built outside the library and a hook taking one was
// reachable only through a running server. The adapter is gone; the specs call
// the exported hook the library calls.
func session(id live.ID) live.Session[live.AnonymousIdentity] {
	GinkgoHelper()
	return livetest.NewSession(GinkgoTB(), id, live.AnonymousIdentity{})
}

// sources projects what a transition scheduled into the one thing a
// specification can compare.
//
// live.Effect[live.AnonymousIdentity] carries its behaviour in a function field and Go cannot compare
// two function values, so a spec asserts on the names the reducer chose. Where
// the behaviour is the point, the spec runs the effect instead — see the click
// table, which reads the decision off the store the effect wrote to.
func sources(effects []live.Effect[live.AnonymousIdentity]) []string {
	if len(effects) == 0 {
		// nil rather than an empty slice, so "scheduled nothing" compares equal
		// to the nil a reducer returns for it.
		return nil
	}
	names := make([]string, 0, len(effects))
	for _, effect := range effects {
		names = append(names, effect.Source)
	}
	return names
}

// noEmit is the Emitter handed to an effect whose emissions a spec does not
// read.
func noEmit(live.Event) error { return nil }

// click builds the event the client runtime would send for one button.
func click(name string, id uint64, at time.Time) live.Event {
	return live.Event{Name: name, FragmentID: FragmentControls, ID: id, At: at}
}

// pushed builds the event the store's subscription pump emits to tabA. It
// carries no event identifier of its own, because a server-initiated
// transition has no client interaction to name as its cause — but it may carry
// a contributing edge back to the click that produced the snapshot.
func pushed(snap Snapshot, at time.Time) live.Event {
	ev := SyncEvent(snap, tabA)
	ev.At = at
	return ev
}

var _ = Describe("The reducer", func() {
	// The reducer never changes the value. That is the whole
	// server-authoritative claim in one property: a click asks the store to
	// change the shared counter and this session finds out the same way every
	// other tab does.
	DescribeTable("turns a click into an effect and changes nothing itself",
		func(event string, want int64) {
			store := NewStore()
			store.Join(tabA)
			state := State{Self: tabA, Value: 41, Version: 7}

			next, effects := Reducer(store)(state, click(event, 1, baseTime))

			Expect(sources(effects)).To(Equal([]string{SourceChange}))
			Expect(next.Value).To(Equal(int64(41)), "a reducer must not apply the change itself")
			Expect(next.Version).To(Equal(uint64(7)))

			// What the reducer decided is read by RUNNING the effect, because
			// the decision now lives in a closure rather than in comparable
			// fields. The store is where it becomes visible, which is also the
			// only place it was ever meant to be visible.
			Expect(effects[0].Run(context.Background(), session(tabA), noEmit)).To(Succeed())
			Expect(store.Snapshot().Value).To(Equal(want))
		},
		Entry("+1", EventIncrement, int64(1)),
		Entry("-1", EventDecrement, int64(-1)),
		Entry("+10", EventIncrement10, int64(10)),
		Entry("reset", EventReset, int64(0)),
	)

	It("folds a store snapshot into this session's view", func() {
		state := State{Self: tabA}
		snap := Snapshot{
			Value:              12,
			Version:            3,
			Tabs:               2,
			ChangedBy:          tabB,
			ChangedAtUnixMilli: baseTime.UnixMilli(),
		}

		next, effects := Reducer(NewStore())(state, pushed(snap, baseTime.Add(4*time.Second)))

		Expect(effects).To(BeEmpty(), "applying a snapshot is not itself a reason to do I/O")
		Expect(next.Value).To(Equal(int64(12)))
		Expect(next.Version).To(Equal(uint64(3)))
		Expect(next.Tabs).To(Equal(2))
		Expect(next.ChangedBy).To(Equal(tabB))
		Expect(next.Age).To(Equal(4 * time.Second))
		Expect(next.Author()).To(Equal("another tab"))
	})

	// The property that makes a dropped push harmless. Emitted events are
	// best-effort — a full mailbox drops one and tells the effect so — and a
	// snapshot is absolute, so the counter converges on the newest one it saw.
	It("ignores a snapshot older than the one it holds", func() {
		state := State{Self: tabA, Value: 9, Version: 5}

		next, _ := Reducer(NewStore())(state, pushed(Snapshot{Value: 2, Version: 4}, baseTime))

		Expect(next.Value).To(Equal(int64(9)))
		Expect(next.Version).To(Equal(uint64(5)))
	})

	It("ignores a snapshot it cannot read rather than half-applying it", func() {
		state := State{Self: tabA, Value: 9, Version: 5, Tabs: 1}

		malformed := live.Event{
			Name: EventSync,
			At:   baseTime,
			Fields: live.NewFields(map[string]string{
				fieldVersion: "6",
				fieldValue:   "not a number",
				fieldTabs:    "2",
			}),
		}
		next, _ := Reducer(NewStore())(state, malformed)

		Expect(next.Value).To(Equal(int64(9)))
		Expect(next.Version).To(Equal(uint64(5)))
		Expect(next.Tabs).To(Equal(1))
	})

	// The library refuses an unregistered name before the reducer runs, so a
	// name reaching this branch is one the library synthesised and this
	// application has nothing to say about.
	It("leaves state alone for a name it does not handle", func() {
		state := State{Self: tabA, Value: 3, Version: 1, ChangedAtUnixMilli: baseTime.UnixMilli()}

		next, effects := Reducer(NewStore())(state, live.Event{Name: "counter.nothing_emits_this", At: baseTime})

		Expect(effects).To(BeEmpty())
		Expect(next.Value).To(Equal(int64(3)))
	})

	// The failure path, on the name the library actually emits.
	//
	// This spec is here because its predecessor asserted on
	// "gotthlive.effect_failed", which nothing emits: it landed in the
	// reducer's default branch, the branch did nothing, and the spec passed
	// while proving nothing. live.EffectFailedEvent exists so the name is not
	// something an application has to remember.
	DescribeTable("acts on a failed effect only when the library says a retry is safe",
		func(source, retryable string, want []string) {
			state := State{Self: tabA, Value: 3, Version: 1}

			next, effects := Reducer(NewStore())(state, live.Event{
				Name: live.EffectFailedEvent,
				At:   baseTime,
				Fields: live.NewFields(map[string]string{
					live.EffectFailedSourceField:    source,
					live.EffectFailedErrorField:     "the session refused 20 snapshots in a row",
					live.EffectFailedRetryableField: retryable,
				}),
			})

			Expect(sources(effects)).To(Equal(want))
			Expect(next.Value).To(Equal(int64(3)), "a failed effect is not a change to the counter")
		},
		Entry("a transient subscription failure is re-subscribed",
			SourceWatch, "true", []string{SourceWatch}),
		Entry("a terminal subscription failure is not",
			SourceWatch, "false", nil),
		Entry("an unreadable classification is terminal",
			SourceWatch, "", nil),
		Entry("a transient failure of an effect with nothing to retry is left alone",
			SourceChange, "true", nil),
	)

	// The relative timestamp F-CTR-7 asks for, computed where it is allowed to
	// be. A render is a pure function of state and may not read a clock, so
	// the age is derived at the transition from the event's own At stamp.
	It("refreshes the relative timestamp on every transition", func() {
		state := State{Self: tabA, ChangedAtUnixMilli: baseTime.UnixMilli()}

		next, _ := Reducer(NewStore())(state, click(EventIncrement, 1, baseTime.Add(90*time.Second)))

		Expect(next.Age).To(Equal(90 * time.Second))
		Expect(next.AgeLabel()).To(Equal("1m ago"))
	})

	DescribeTable("derives the display F-CTR-3 asks for",
		func(value int64, parity, band string) {
			s := State{Value: value}
			Expect(s.Parity()).To(Equal(parity))
			Expect(s.Band()).To(Equal(band))
		},
		Entry("negative", int64(-3), "odd", "negative"),
		Entry("zero", int64(0), "even", "zero"),
		Entry("single digit", int64(7), "odd", "low"),
		Entry("ten and up", int64(10), "even", "high"),
	)

	It("reads a session identifier back out of a sync field", func() {
		Expect(parseID(tabB.String())).To(Equal(tabB))
		Expect(parseID("")).To(Equal(live.ID{}))
		Expect(parseID("zz")).To(Equal(live.ID{}))
		Expect(parseID(tabB.String()[:30])).To(Equal(live.ID{}))
	})
})

// mixedLog is a session's whole life: four clicks and the snapshots the store
// pushed back, in the order a browser would have produced them.
func mixedLog() []live.Event {
	at := baseTime
	log := []live.Event{}
	value := int64(0)
	version := uint64(0)

	for i, event := range []string{EventIncrement, EventIncrement10, EventDecrement, EventReset} {
		at = at.Add(time.Second)
		log = append(log, click(event, uint64(i+1), at))

		switch event {
		case EventIncrement:
			value++
		case EventIncrement10:
			value += 10
		case EventDecrement:
			value--
		case EventReset:
			value = 0
		}
		version++

		at = at.Add(3 * time.Millisecond)
		log = append(log, pushed(Snapshot{
			Value:              value,
			Version:            version,
			Tabs:               2,
			ChangedBy:          tabA,
			ChangedAtUnixMilli: at.UnixMilli(),
		}, at))
	}
	return log
}

var _ = Describe("Determinism", func() {
	initial := State{Self: tabA}

	// FR-15's mandatory harness, pointed at this example's reducer. A reducer
	// that read a clock, a random source or the iteration order of a map would
	// fail it; nothing else in a pure function of two values can differ
	// between runs.
	It("replays the whole session to the same state and the same effects", func() {
		livetest.ReplayN(GinkgoTB(), Reducer(NewStore()), initial, mixedLog(), 25)
	})

	It("replays to the value the log describes", func() {
		state := initial
		for _, ev := range mixedLog() {
			state, _ = Reducer(NewStore())(state, ev)
		}
		Expect(state.Value).To(Equal(int64(0)))
		Expect(state.Version).To(Equal(uint64(4)))
		Expect(state.Author()).To(Equal("this tab"))
	})

	// The dual mistake, in rendering rather than in reducing: a fragment that
	// declared itself unchanged while its markup moved. That is the one bug
	// that produces a stale region in production and nothing at all in
	// development, because some other transition usually re-renders it before
	// anybody looks.
	It("declares every fragment that its own markup changes", func() {
		livetest.AssertDirtyComplete(GinkgoTB(), Config(NewStore(), []string{"http://127.0.0.1:8080"}), initial, mixedLog())
	})

	// Over-declaring is safe but not free, and a Dirty function that always
	// returns true is a Dirty function that is not doing its job. This is the
	// half AssertDirtyComplete cannot check: that the declaration is tight.
	It("does not re-render the controls when only the value moved", func() {
		cfg := Config(NewStore(), []string{"http://127.0.0.1:8080"})
		controls := cfg.Fragments[1]
		Expect(controls.ID).To(Equal(FragmentControls))

		prev := State{Self: tabA, Value: 1, Tabs: 2, ChangedBy: tabA}
		next := prev
		next.Value = 2

		Expect(controls.Dirty(prev, next)).To(BeFalse())

		next.Tabs = 3
		Expect(controls.Dirty(prev, next)).To(BeTrue())
	})
})

var _ = Describe("The shared store", func() {
	var store *Store

	BeforeEach(func() {
		store = NewStore()
		store.now = func() time.Time { return baseTime }
	})

	It("registers a session and reads the counter under one lock", func() {
		Expect(store.Join(tabA)).To(Equal(Snapshot{Tabs: 1}))

		store.Apply(Change{Op: OpAdd, Delta: 4, By: tabA})

		Expect(store.Join(tabB)).To(Equal(Snapshot{
			Value:              4,
			Version:            1,
			Tabs:               2,
			ChangedBy:          tabA,
			ChangedAtUnixMilli: baseTime.UnixMilli(),
		}))
	})

	It("gives a joining session the value a reload must preserve", func() {
		store.Join(tabA)
		store.Apply(Change{Op: OpAdd, Delta: 7, By: tabA})
		store.Leave(tabA)

		// The reload: a brand-new session, and the count is still there
		// because it was never in the browser.
		Expect(store.Join(tabA).Value).To(Equal(int64(7)))
	})

	It("counts down again when a tab goes away", func() {
		store.Join(tabA)
		store.Join(tabB)
		store.Leave(tabB)

		Expect(store.Snapshot().Tabs).To(Equal(1))
	})

	It("resets to zero and keeps counting revisions", func() {
		store.Join(tabA)
		store.Apply(Change{Op: OpAdd, Delta: 5, By: tabA})
		snap := store.Apply(Change{Op: OpReset, By: tabB})

		Expect(snap.Value).To(BeZero())
		Expect(snap.Version).To(Equal(uint64(2)))
		Expect(snap.ChangedBy).To(Equal(tabB))
	})

	It("wakes every subscriber with the newest snapshot", func() {
		store.Join(tabA)
		store.Join(tabB)

		store.Apply(Change{Op: OpAdd, Delta: 3, By: tabA})

		for _, id := range []live.ID{tabA, tabB} {
			sub := store.subs[id]
			Eventually(sub.wake).Should(Receive())
			Expect(sub.snapshot().Value).To(Equal(int64(3)))
		}
	})

	// Latest-value-wins, asserted rather than assumed. A subscriber that was
	// not read between two changes sees the second, not the first, and the
	// signal is still exactly one.
	It("collapses changes a subscriber has not read yet", func() {
		store.Join(tabA)

		store.Apply(Change{Op: OpAdd, Delta: 1, By: tabA})
		store.Apply(Change{Op: OpAdd, Delta: 1, By: tabA})
		store.Apply(Change{Op: OpAdd, Delta: 1, By: tabA})

		sub := store.subs[tabA]
		Expect(sub.snapshot().Value).To(Equal(int64(3)))
		Expect(sub.wake).To(HaveLen(1))
	})

	// There is no "refuses an effect it has no executor for" spec any more, and
	// its absence is the point: since live.Effect[live.AnonymousIdentity] became a concrete struct with
	// its own Run, an effect this store does not own is an effect this store
	// cannot be handed. The failure that spec covered — an effect that names
	// itself and performs nothing — is the library's now, and live's own suite
	// is where it is held.
})

var _ = Describe("The subscription pump", func() {
	// The end-to-end shape of the push channel, with the library's Emitter
	// replaced by a channel a spec can read: a change made by one session
	// reaches every other session as a sync event carrying the whole snapshot.
	It("emits one sync event per change, to every subscribed session", func() {
		store := NewStore()
		store.now = func() time.Time { return baseTime }
		store.Join(tabA)
		store.Join(tabB)

		ctx, cancel := context.WithCancel(GinkgoT().Context())
		defer cancel()

		emitted := make(chan live.Event, 8)
		go func() {
			_ = store.WatchEffect().Run(ctx, session(tabB), func(ev live.Event) error {
				emitted <- ev
				return nil
			})
		}()

		// A joining session is not pushed its own snapshot — Config.Init
		// already returned it as the initial state — so nothing has been
		// emitted yet. The change comes from the other tab.
		Consistently(emitted, 50*time.Millisecond).ShouldNot(Receive())
		store.Apply(Change{Op: OpAdd, Delta: 6, By: tabA})

		var ev live.Event
		Eventually(emitted).Should(Receive(&ev))
		Expect(ev.Name).To(Equal(EventSync))
		Expect(ev.Fields.Get(fieldValue)).To(Equal("6"))
		Expect(ev.Fields.Get(fieldChangedBy)).To(Equal(tabA.String()))

		// And the event a reducer receives folds to the value tabA set.
		next, _ := Reducer(NewStore())(State{Self: tabB}, live.Event{Name: ev.Name, Fields: ev.Fields, At: baseTime})
		Expect(next.Value).To(Equal(int64(6)))
	})

	It("retries a snapshot the session could not accept", func() {
		store := NewStore()
		store.now = func() time.Time { return baseTime }
		store.Join(tabA)

		ctx, cancel := context.WithCancel(GinkgoT().Context())
		defer cancel()

		accepted := make(chan live.Event, 4)
		refusals := 0
		go func() {
			_ = store.WatchEffect().Run(ctx, session(tabA), func(ev live.Event) error {
				if refusals < 2 {
					refusals++
					return errSaturated
				}
				accepted <- ev
				return nil
			})
		}()

		store.Apply(Change{Op: OpAdd, Delta: 2, By: tabA})

		var ev live.Event
		Eventually(accepted, 2*time.Second).Should(Receive(&ev))
		Expect(ev.Fields.Get(fieldValue)).To(Equal("2"))
	})

	// The bound on that retry, and the handoff it creates. A pump that retried
	// forever would keep a subscription that is going nowhere looking alive:
	// the session would stop learning about other tabs and nothing above the
	// effect would ever hear about it. Giving up produces a failure event the
	// reducer re-subscribes on, which the reducer's own table above covers.
	It("gives up after a run of refusals rather than hiding a stuck subscription", func() {
		store := NewStore()
		store.now = func() time.Time { return baseTime }
		store.Join(tabA)

		ctx, cancel := context.WithCancel(GinkgoT().Context())
		defer cancel()

		done := make(chan error, 1)
		go func() {
			done <- store.WatchEffect().Run(ctx, session(tabA), func(live.Event) error { return errSaturated })
		}()

		store.Apply(Change{Op: OpAdd, Delta: 1, By: tabA})

		var err error
		Eventually(done, 5*time.Second).Should(Receive(&err))
		Expect(err).To(MatchError(ContainSubstring(strconv.Itoa(maxRefusals) + " snapshots in a row")))
	})

	It("returns as soon as the session's context is cancelled", func() {
		store := NewStore()
		store.Join(tabA)

		ctx, cancel := context.WithCancel(GinkgoT().Context())
		done := make(chan error, 1)
		go func() {
			done <- store.WatchEffect().Run(ctx, session(tabA), func(live.Event) error { return nil })
		}()

		cancel()
		Eventually(done).Should(Receive(MatchError(context.Canceled)))
	})

	It("reports a session that was never joined rather than blocking forever", func() {
		store := NewStore()

		err := store.WatchEffect().Run(GinkgoT().Context(), session(tabA), func(live.Event) error { return nil })

		Expect(err).To(MatchError(ContainSubstring("not subscribed")))
	})
})

var errSaturated = errors.New("the session mailbox is full")

var _ = Describe("The markup", func() {
	// The attribute vocabulary is a contract with the client runtime, and a
	// disagreement is a silent no-op in the browser rather than an error
	// anywhere. Asserting on the rendered bytes is what makes it loud.
	It("marks each live region with the fragment ID the patches name", func() {
		html := render(page(State{Self: tabA, Value: 3, Tabs: 1}))

		Expect(html).To(ContainSubstring(`data-gotth-region="` + FragmentValue + `"`))
		Expect(html).To(ContainSubstring(`data-gotth-region="` + FragmentControls + `"`))
	})

	It("binds every button to a registered event", func() {
		html := render(ControlsRegion(State{Self: tabA}))
		cfg := Config(NewStore(), []string{"http://127.0.0.1:8080"})

		for _, event := range cfg.Events {
			Expect(html).To(ContainSubstring(`data-gotth-on="click:`+event+`"`),
				"the markup must bind %s, or the button does nothing", event)
		}
	})

	// The runtime resolves an event's fragment by walking up from the clicked
	// element to the nearest data-gotth-region ancestor. A control outside
	// every region raises nothing at all.
	It("keeps the controls inside their region", func() {
		html := render(ControlsRegion(State{Self: tabA}))

		region := strings.Index(html, "data-gotth-region")
		button := strings.Index(html, "data-gotth-on")
		Expect(region).To(BeNumerically(">=", 0))
		Expect(button).To(BeNumerically(">", region))
	})

	It("serves the client runtime from a script tag with no build step", func() {
		html := render(page(State{Self: tabA}))

		Expect(html).To(ContainSubstring(`src="/live/gotth-live.min.js"`))
		Expect(html).To(ContainSubstring(`data-gotth-url="/live"`))
	})

	It("carries the benchmark harness's handles", func() {
		html := render(page(State{Self: tabA, Value: 42}))

		for _, id := range []string{"value", "inc", "dec", "inc10", "reset"} {
			Expect(html).To(ContainSubstring(`data-bench-id="` + id + `"`))
		}
		Expect(html).To(ContainSubstring(`data-bench-value`))
		Expect(html).To(ContainSubstring(`>42<`))
	})

	// The page and the fragments must render the same bytes for the same
	// state, or the first patch after connecting would visibly rewrite a page
	// that was already correct.
	It("composes the page from the same components the fragments render", func() {
		state := State{Self: tabA, Value: 5, Tabs: 2, ChangedBy: tabB, ChangedAtUnixMilli: baseTime.UnixMilli()}

		Expect(render(page(state))).To(ContainSubstring(render(ValueRegion(state))))
		Expect(render(page(state))).To(ContainSubstring(render(ControlsRegion(state))))
	})

	It("renders the same state to the same bytes, every time", func() {
		state := State{Self: tabA, Value: 5, Tabs: 2, ChangedBy: tabB}
		first := render(page(state))
		for range 20 {
			Expect(render(page(state))).To(Equal(first))
		}
	})

	// FR-57 and NFR-8. The page is given the app rather than a flag, so the
	// template is the same template in both environments; what changes is that
	// two of the three script tags render zero bytes. Both halves are asserted
	// here, because the one that matters in production is the empty one.
	//
	// The ordering assertion is the library's invariant seen from an
	// application: the inspector wraps the WebSocket constructor, both tags are
	// deferred, and a deferred script that runs second wraps nothing. This page
	// no longer places any of the three, so it is here to catch the library
	// changing its mind rather than this file.
	It("renders both dev tags when Dev is set, with the inspector above the runtime", func() {
		html := render(Page(devApp(true), State{Self: tabA}))

		Expect(html).To(ContainSubstring(`src="/live/gotth-live-dev-reload.min.js"`))
		Expect(html).To(ContainSubstring(`data-gotth-dev-url="/live"`))
		Expect(html).To(ContainSubstring(`data-gotth-dev-build="`))
		Expect(html).To(ContainSubstring(`src="/live/gotth-live-inspector.min.js"`))
		Expect(strings.Index(html, "gotth-live-inspector.min.js")).
			To(BeNumerically("<", strings.Index(html, `src="/live/gotth-live.min.js"`)))
	})

	It("renders no dev reference at all when Dev is false", func() {
		html := render(Page(devApp(false), State{Self: tabA}))

		Expect(html).NotTo(ContainSubstring("gotth-live-dev"))
		Expect(html).NotTo(ContainSubstring("gotth-live-inspector"))
		Expect(html).To(ContainSubstring(`src="/live/gotth-live.min.js"`),
			"turning Dev off must take the two dev tags away and nothing else")
	})
})

// page renders the document with Dev off, which is what every spec above
// wants: they are about the application's own markup, and the two dev tags are
// the library's.
func page(s State) templ.Component {
	return Page(devApp(false), s)
}

// devApp is this example's application with Dev set either way. The page needs
// one because app.Document is a method — what it puts in the head depends on
// this Config.
func devApp(dev bool) *live.App[State, live.AnonymousIdentity] {
	GinkgoHelper()

	app, err := live.New(devConfig(dev))
	Expect(err).NotTo(HaveOccurred())
	return app
}

// devConfig is this example's config with Dev set either way.
func devConfig(dev bool) live.Config[State, live.AnonymousIdentity] {
	cfg := Config(NewStore(), []string{"http://127.0.0.1:8080"})
	cfg.Dev = dev
	return cfg
}

func render(c templ.Component) string {
	GinkgoHelper()
	var buf bytes.Buffer
	Expect(c.Render(context.Background(), &buf)).To(Succeed())
	return buf.String()
}

var _ = Describe("The mounted application", func() {
	const origin = "http://127.0.0.1:8080"

	var (
		app    *live.App[State, live.AnonymousIdentity]
		server *httptest.Server
	)

	BeforeEach(func() {
		var err error
		app, err = live.New(Config(NewStore(), []string{origin}))
		Expect(err).NotTo(HaveOccurred())

		server = httptest.NewServer(NewMux(app, NewStore()))
		DeferCleanup(func() {
			server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			Expect(app.Close(ctx)).To(Succeed())
		})
	})

	It("serves the page with the regions already rendered", func() {
		resp, err := http.Get(server.URL + "/")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(string(body)).To(ContainSubstring(`data-gotth-region="` + FragmentValue + `"`))
	})

	It("serves the embedded client runtime, cacheable forever", func() {
		resp, err := http.Get(server.URL + "/live/gotth-live.min.js")
		Expect(err).NotTo(HaveOccurred())
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		Expect(err).NotTo(HaveOccurred())
		Expect(resp.StatusCode).To(Equal(http.StatusOK))
		Expect(resp.Header.Get("Content-Type")).To(HavePrefix("text/javascript"))
		Expect(resp.Header.Get("ETag")).NotTo(BeEmpty())
		Expect(len(body)).To(BeNumerically(">", 1000))
	})

	// Deny by default, checked from outside rather than read off the Config.
	// A request with the wrong Origin is refused before any per-session memory
	// is allocated, and a request with no Origin at all is refused too: an
	// absent Origin is not an allowed one.
	DescribeTable("answers the WebSocket handshake according to the allowlist",
		func(headers map[string]string, want int) {
			Expect(handshake(server.URL, headers)).To(Equal(want))
		},
		Entry("the allowed origin", map[string]string{
			"Origin":                 origin,
			"Sec-WebSocket-Protocol": "gotth-live.v1",
		}, http.StatusSwitchingProtocols),
		Entry("a foreign origin", map[string]string{
			"Origin":                 "https://evil.example",
			"Sec-WebSocket-Protocol": "gotth-live.v1",
		}, http.StatusForbidden),
		Entry("no origin at all", map[string]string{
			"Sec-WebSocket-Protocol": "gotth-live.v1",
		}, http.StatusForbidden),
		Entry("the right origin but the wrong subprotocol", map[string]string{
			"Origin":                 origin,
			"Sec-WebSocket-Protocol": "gotth-live.v0",
		}, http.StatusUpgradeRequired),
	)
})

// handshake performs a raw WebSocket upgrade and returns the status the server
// answered with.
//
// It is written against net.Dial rather than a WebSocket client library
// because what is under test is the HTTP half of the handshake — the origin
// allowlist and the subprotocol negotiation — and a client library would
// turn every refusal into the same error.
func handshake(serverURL string, headers map[string]string) int {
	GinkgoHelper()

	u, err := url.Parse(serverURL)
	Expect(err).NotTo(HaveOccurred())

	conn, err := net.Dial("tcp", u.Host)
	Expect(err).NotTo(HaveOccurred())
	defer conn.Close()
	Expect(conn.SetDeadline(time.Now().Add(5 * time.Second))).To(Succeed())

	req := "GET /live HTTP/1.1\r\nHost: " + u.Host + "\r\n" +
		"Connection: Upgrade\r\nUpgrade: websocket\r\n" +
		"Sec-WebSocket-Version: 13\r\nSec-WebSocket-Key: dGhlIHNhbXBsZSBub25jZQ==\r\n"
	for k, v := range headers {
		req += k + ": " + v + "\r\n"
	}
	req += "\r\n"

	_, err = conn.Write([]byte(req))
	Expect(err).NotTo(HaveOccurred())

	status, err := bufio.NewReader(conn).ReadString('\n')
	Expect(err).NotTo(HaveOccurred())

	code, err := strconv.Atoi(strings.Fields(status)[1])
	Expect(err).NotTo(HaveOccurred())
	return code
}

var _ = Describe("Startup", func() {
	Describe("the Origin allowlist", func() {
		It("names both loopback spellings, because a browser sends the host you typed", func() {
			Expect(allowedOrigins("127.0.0.1:8080", "")).To(ConsistOf(
				"http://127.0.0.1:8080", "http://localhost:8080"))
			Expect(allowedOrigins("localhost:8080", "")).To(ConsistOf(
				"http://localhost:8080", "http://127.0.0.1:8080"))
		})

		// The README's container invocation is "-addr 0.0.0.0:8080". No browser
		// ever sends 0.0.0.0 as an Origin, so without the bind-all arm the
		// documented way to run this example allows exactly one Origin nothing
		// can produce, and every upgrade is refused with 403.
		It("names them for the bind-all address the README tells you to use", func() {
			Expect(allowedOrigins("0.0.0.0:8080", "")).To(ContainElements(
				"http://127.0.0.1:8080", "http://localhost:8080"))
		})

		It("appends what the operator asked for and nothing else", func() {
			Expect(allowedOrigins("127.0.0.1:8080", "http://192.168.1.10:8080 , ")).To(ContainElement(
				"http://192.168.1.10:8080"))
		})

		It("never produces the wildcard, whatever it is given", func() {
			for _, addr := range []string{"127.0.0.1:8080", "0.0.0.0:8080", "localhost:8080", ":8080"} {
				Expect(allowedOrigins(addr, "")).NotTo(ContainElement(live.AnyOrigin), "addr %q", addr)
			}
		})
	})
})
