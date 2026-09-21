package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"os"
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
// "another tab" (F-CTR-5) without a running server.
var (
	tabA = live.ID{0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf}
	tabB = live.ID{0xb0, 0xb1, 0xb2, 0xb3, 0xb4, 0xb5, 0xb6, 0xb7, 0xb8, 0xb9, 0xba, 0xbb, 0xbc, 0xbd, 0xbe, 0xbf}
)

// baseTime is the wall clock the specs use. Nothing under test reads a clock,
// so it is a constant rather than a fixture.
var baseTime = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

func click(name string, id uint64, at time.Time) live.Event {
	return live.Event{Name: name, FragmentID: FragmentControls, ID: id, At: at}
}

// key builds the event the runtime sends for F-CTR-6's keyboard binding. It is
// the SAME event name a click sends — which is the point of the feature and the
// reason CTR-5's paint predicate is "same as CTR-1" — so the only difference
// from click() is the fragment the binding sits in.
func key(name string, id uint64, at time.Time) live.Event {
	return live.Event{Name: name, FragmentID: FragmentValue, ID: id, At: at}
}

func pushed(snap Snapshot, at time.Time) live.Event {
	ev := SyncEvent(snap, tabA)
	ev.At = at
	return ev
}

func render(c templ.Component) string {
	GinkgoHelper()
	var buf bytes.Buffer
	Expect(c.Render(context.Background(), &buf)).To(Succeed())
	return buf.String()
}

var _ = Describe("§2.1 F-CTR — the feature table", func() {
	Describe("F-CTR-1/F-CTR-4: the value is server state and survives a reload", func() {
		// CTR-8 is "reload; value preserved" and it is a correctness assertion
		// whose failure voids the counter's cells. The property it rests on is
		// that the value lives in the store and not in the session: a reload is
		// a NEW session with a fresh Init, and Init reads the store.
		It("gives a freshly mounted session the store's current value", func() {
			store := NewStore()
			store.Apply(Change{Op: OpAdd, Delta: 7, By: tabA})

			cfg := Config(store, testOrigins)
			state, effects, err := cfg.Init(context.Background(), session(tabA))
			Expect(err).NotTo(HaveOccurred())
			Expect(state.Value).To(Equal(int64(7)), "a reload rebuilds state from the store")
			Expect(effects).To(HaveLen(1), "Init subscribes the session for pushes")
		})

		// R-6 in bench/README.md: the counter is GLOBAL, not per session.
		// F-CTR-1 says "server state, per session"; the app that gets measured
		// is the app that exists, and the shared counter is what makes F-CTR-5
		// observable at all. Recorded as a spec so a spec amendment toward the
		// literal reading fails here rather than silently changing the meaning
		// of every CTR-* row.
		It("shares one value between every session", func() {
			store := NewStore()
			store.Join(tabA)
			store.Join(tabB)
			store.Apply(Change{Op: OpAdd, Delta: 3, By: tabA})

			Expect(store.Snapshot().Value).To(Equal(int64(3)))
			Expect(store.Snapshot().Tabs).To(Equal(2))
		})
	})

	Describe("F-CTR-2: the four operations", func() {
		// One name per operation rather than one name carrying a delta:
		// Config.Events is default-deny, so four names bound what a hostile
		// client can ask for where one name and a number bounds nothing.
		DescribeTable("a click returns the effect that asks the store to apply it",
			func(event string, want int64) {
				store := NewStore()
				store.Join(tabA)
				state := State{Self: tabA}
				next, effects := Reducer(store)(state, click(event, 1, baseTime))

				Expect(next.Value).To(Equal(state.Value),
					"a click never changes the value locally: the store decides and the sync reports")
				Expect(effects).To(HaveLen(1))
				Expect(effects[0].Source).To(Equal(SourceChange))

				// The operation the reducer chose lives in the effect's Run
				// rather than in comparable fields, so it is read by running
				// the effect against the store it was built for.
				Expect(effects[0].Run(context.Background(), session(tabA), func(live.Event) error { return nil })).
					To(Succeed())
				Expect(store.Snapshot().Value).To(Equal(want))
			},
			Entry("−1  (CTR-2)", EventDecrement, int64(-1)),
			Entry("+1  (CTR-1)", EventIncrement, int64(1)),
			Entry("+10 (CTR-3)", EventIncrement10, int64(10)),
			Entry("Reset (CTR-4)", EventReset, int64(0)),
		)

		It("registers exactly the four names a browser may send", func() {
			Expect(Config(NewStore(), testOrigins).Events).To(ConsistOf(
				EventIncrement, EventDecrement, EventIncrement10, EventReset))
		})

		It("does not register the sync event a browser must never send", func() {
			Expect(Config(NewStore(), testOrigins).Events).NotTo(ContainElement(EventSync),
				"a client that could send counter.sync could declare the counter to be any value it liked")
		})
	})

	Describe("F-CTR-3: the derived display is not one text node", func() {
		DescribeTable("parity",
			func(value int64, want string) {
				Expect(State{Value: value}.Parity()).To(Equal(want))
			},
			Entry("negative even", int64(-2), "even"),
			Entry("zero", int64(0), "even"),
			Entry("odd", int64(7), "odd"),
		)

		// The badge's class changes at thresholds rather than on every value,
		// so the morph has to get a class right as well as a number.
		DescribeTable("the badge band, at §2.1's four thresholds",
			func(value int64, want string) {
				Expect(State{Value: value}.Band()).To(Equal(want))
			},
			Entry("< 0", int64(-1), "negative"),
			Entry("0", int64(0), "zero"),
			Entry("1", int64(1), "low"),
			Entry("9", int64(9), "low"),
			Entry("10", int64(10), "high"),
			Entry("> 10", int64(4242), "high"),
		)
	})

	Describe("F-CTR-5: two tabs share one value and both repaint", func() {
		It("pushes the new snapshot to every subscribed session", func() {
			store := NewStore()
			store.Join(tabA)
			store.Join(tabB)

			store.Apply(Change{Op: OpAdd, Delta: 1, By: tabA, Cause: 9})

			Expect(store.subs[tabB].snapshot().Value).To(Equal(int64(1)),
				"the tab that did not click is the one CTR-7 measures")
		})

		// The contributing edge is a claim about ONE recipient's own event.
		// Identifiers are session-scoped, so naming another session's event is
		// not a thing that can be true — and CTR-7's cross-tab row is exactly
		// the case where the temptation exists.
		It("claims the causing event only for the session that caused it", func() {
			snap := Snapshot{Value: 1, Version: 1, ChangedBy: tabA, ChangedByEvent: 9}
			Expect(SyncEvent(snap, tabA).Contributing).To(Equal([]uint64{9}))
			Expect(SyncEvent(snap, tabB).Contributing).To(BeEmpty())
		})

		It("renders who changed it from the receiving tab's point of view", func() {
			Expect(State{Self: tabA}.Author()).To(Equal("nobody yet"))
			Expect(State{Self: tabA, ChangedBy: tabA}.Author()).To(Equal("this tab"))
			Expect(State{Self: tabA, ChangedBy: tabB}.Author()).To(Equal("another tab"))
		})
	})

	Describe("F-CTR-6: `+` and `-` on the focused counter", func() {
		// The feature is a KEY FILTER, and the whole of its correctness is in
		// the markup rather than in the reducer: an unfiltered keydown binding
		// would raise counter.increment on Tab, on Shift and on every arrow.
		It("binds one key per event, filtered, and both on one element", func() {
			attrs := KeyBindings()
			Expect(attrs).To(HaveKeyWithValue("data-gotth-on",
				"keydown:"+EventIncrement+":+;keydown:"+EventDecrement+":-"))
		})

		It("applies the same transition a click does", func() {
			reduce := Reducer(NewStore())
			_, fromKey := reduce(State{Self: tabA}, key(EventIncrement, 1, baseTime))
			_, fromClick := reduce(State{Self: tabA}, click(EventIncrement, 1, baseTime))
			Expect(sources(fromKey)).To(Equal(sources(fromClick)),
				"CTR-5's paint predicate is 'same as CTR-1', which is only true if the transition is")
		})
	})

	Describe("F-CTR-7: the relative timestamp is re-rendered with the value", func() {
		DescribeTable("the label",
			func(age time.Duration, changedAt int64, want string) {
				Expect(State{Age: age, ChangedAtUnixMilli: changedAt}.AgeLabel()).To(Equal(want))
			},
			Entry("never changed", time.Duration(0), int64(0), "never"),
			Entry("under two seconds", 500*time.Millisecond, int64(1), "just now"),
			Entry("seconds", 42*time.Second, int64(1), "42s ago"),
			Entry("minutes", 3*time.Minute, int64(1), "3m ago"),
			Entry("hours", 5*time.Hour, int64(1), "5h ago"),
		)

		// The reducer may not read a clock, so the age is computed from the
		// event's own At stamp at every transition. That is what keeps the
		// line from going stale between changes without making the render
		// impure — and an impure render would break the byte comparison that
		// suppresses a patch nobody needs.
		It("refreshes the age on every transition, from the event's stamp", func() {
			state := State{Self: tabA, ChangedAtUnixMilli: baseTime.UnixMilli()}
			next, _ := Reducer(NewStore())(state, click(EventIncrement, 1, baseTime.Add(9*time.Second)))
			Expect(next.AgeLabel()).To(Equal("9s ago"))
		})
	})
})

var _ = Describe("§2.0 the markup hooks the harness drives", func() {
	// E2: "Every interaction ID in §2 exists in both and is driven by the
	// identical harness script against identical data-bench-id hooks." This is
	// that contract, checked against rendered bytes rather than read off the
	// template — a hook that a refactor dropped is a harness timeout twenty
	// minutes into a run, and this spec is the cheapest place to catch it.
	var page string

	BeforeEach(func() {
		page = render(Page(State{Self: tabA, Value: 3, Tabs: 2, ChangedBy: tabB}))
	})

	DescribeTable("every data-bench-id the CTR-* interaction files select",
		func(id string) {
			Expect(page).To(ContainSubstring(`data-bench-id="` + id + `"`))
		},
		Entry("CTR-1 drive + CTR-1..6 predicate subject", "value"),
		Entry("CTR-1 drive", "inc"),
		Entry("CTR-2 drive", "dec"),
		Entry("CTR-3 drive", "inc10"),
		Entry("CTR-4 drive", "reset"),
		Entry("CTR-5 focus target", "counter"),
		Entry("F-CTR-5's visible tab count", "tabs"),
	)

	It("marks both live regions with the data-bench-region the shim observes", func() {
		Expect(page).To(ContainSubstring(`data-bench-region="A"`))
		Expect(page).To(ContainSubstring(`data-bench-region="B"`))
	})

	It("puts the value's textContent where §2.0's paint predicate reads it", func() {
		// window.__bench.value('value') is textContent, so the number must be
		// the element's whole text and not a fragment of a sentence.
		Expect(page).To(ContainSubstring(`data-bench-id="value" data-bench-value>3</p>`))
	})

	It("makes the CTR-5 focus target focusable", func() {
		// A <section> is not focusable without tabindex, the key would go to
		// <body>, and the binding — resolved from the target's nearest
		// data-gotth-region ancestor — would never be reached.
		Expect(page).To(MatchRegexp(`data-bench-id="counter"[^>]*tabindex="0"`))
	})

	It("loads the shim before the runtime, unedferred, per §3.2", func() {
		shim := strings.Index(page, ShimRoute)
		runtime := strings.Index(page, "gotth-live.min.js")
		Expect(shim).To(BeNumerically(">", 0))
		Expect(runtime).To(BeNumerically(">", shim),
			"§3.2 registers the t_input listener before any application script")
		Expect(page).To(ContainSubstring(`<script src="`+ShimRoute+`"></script>`),
			"the shim tag carries no defer: a deferred shim runs after the runtime's own deferred tag")
	})

	It("serves exactly one stylesheet and no images (§2.1's data volume)", func() {
		Expect(strings.Count(page, "<link rel=\"stylesheet\"")).To(Equal(1))
		Expect(page).NotTo(ContainSubstring("<img"))
	})

	// E5 — bounded DOM. §2.1: "Rendered live region ≤ 40 elements. Whole
	// document ≤ 150 elements." Counting open tags is crude and it is enough:
	// the bound is an order-of-magnitude guard, and the smoke run reports the
	// browser's own count against the same number.
	It("stays inside §2.1's element bounds", func() {
		Expect(countElements(page)).To(BeNumerically("<=", 150))
		Expect(countElements(render(ValueRegion(State{Self: tabA})))).To(BeNumerically("<=", 40))
	})
})

var _ = Describe("§2.0 the shared assets, byte for byte", func() {
	// §2.0: "The shim source is ONE FILE, BYTE-IDENTICAL, SERVED BY BOTH APPS."
	// This app serves the original rather than a copy, so the only thing left
	// to check is that the path it defaults to resolves — which is exactly the
	// failure a container that moved the working directory would produce.
	It("resolves the harness shim from the app's own directory", func() {
		shim, err := LoadShim(DefaultShimPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(shim)).To(ContainSubstring("window.__bench = bench;"))
	})

	// The one stylesheet is the Next.js side's file. A copy is a second file
	// that agrees today, so the agreement is asserted rather than asserted-to.
	It("serves the stylesheet the Next.js side serves", func() {
		want, err := os.ReadFile("../next/src/app/counter.css")
		if errors.Is(err, fs.ErrNotExist) {
			Skip("the Next.js side is not in this checkout")
		}
		Expect(err).NotTo(HaveOccurred())
		Expect(stylesheet).To(Equal(want),
			"two stacks laid out differently are two documents, and E5's bounds would be measuring different pages")
	})
})

var _ = Describe("Determinism (FR-15)", func() {
	initial := State{Self: tabA}

	// FR-15's mandatory harness, pointed at this app's reducer. A reducer that
	// read a clock, a random source or the iteration order of a map would fail
	// it; nothing else in a pure function of two values can differ between
	// runs. It is also the property §2.5's conformance test rests on: both
	// servers must emit the same logical state for tick N, and a reducer whose
	// output depended on when it ran could not.
	It("replays the whole session to the same state and the same effects", func() {
		livetest.ReplayN(GinkgoTB(), Reducer(NewStore()), initial, mixedLog(), 25)
	})

	It("replays to the value the log describes", func() {
		state := initial
		for _, ev := range mixedLog() {
			state, _ = Reducer(NewStore())(state, ev)
		}
		Expect(state.Value).To(Equal(int64(0)))
		Expect(state.Version).To(Equal(uint64(5)))
	})

	It("declares every fragment that its own markup changes", func() {
		livetest.AssertDirtyComplete(GinkgoTB(), Config(NewStore(), testOrigins), initial, mixedLog())
	})

	// Over-declaring is safe but not free, and a Dirty function that always
	// returns true is one that is not doing its job. This is the half
	// AssertDirtyComplete cannot check: that the declaration is tight. It
	// matters more here than in the example, because §4.6's wire-byte row is
	// counting the patches this decides not to send.
	It("does not re-render the controls when only the value moved", func() {
		controls := Config(NewStore(), testOrigins).Fragments[1]
		Expect(controls.ID).To(Equal(FragmentControls))

		prev := State{Self: tabA, Value: 1, Tabs: 2, ChangedBy: tabA}
		next := prev
		next.Value = 2
		Expect(controls.Dirty(prev, next)).To(BeFalse())
	})

	// An out-of-order sync is dropped rather than applied. Emitted events are
	// best-effort — a full mailbox drops one and tells the effect so — and this
	// is the line that makes that harmless.
	It("ignores a snapshot older than the one it holds", func() {
		state := State{Self: tabA, Value: 9, Version: 4}
		next, _ := Reducer(NewStore())(state, pushed(Snapshot{Value: 1, Version: 3}, baseTime))
		Expect(next.Value).To(Equal(int64(9)))
	})
})

var testOrigins = []string{"http://127.0.0.1:3000"}

// sources projects what a transition scheduled into the one thing a
// specification can compare: live.Effect[live.AnonymousIdentity] carries its behaviour in a function
// field, and Go cannot compare two function values.
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

func session(id live.ID) live.Session[live.AnonymousIdentity] {
	GinkgoHelper()
	return livetest.NewSession(GinkgoTB(), id, live.AnonymousIdentity{})
}

type anonymous struct{}

func (anonymous) Subject() string { return "anonymous" }

// mixedLog is one session's whole event log: the four operations F-CTR-2 names,
// each followed by the sync the store pushes back, plus the keyboard path
// F-CTR-6 adds. It is the log ReplayN replays.
func mixedLog() []live.Event {
	at := baseTime
	log := []live.Event{}
	value := int64(0)
	version := uint64(0)

	steps := []struct {
		event string
		byKey bool
	}{
		{EventIncrement, false},
		{EventIncrement10, false},
		{EventDecrement, false},
		{EventIncrement, true}, // F-CTR-6: the same event, raised by a key
		{EventReset, false},
	}

	for i, step := range steps {
		at = at.Add(time.Second)
		if step.byKey {
			log = append(log, key(step.event, uint64(i+1), at))
		} else {
			log = append(log, click(step.event, uint64(i+1), at))
		}

		switch step.event {
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

// countElements counts opening tags, which is close enough for an E5 guard and
// deliberately does not parse: a parser here would be a second implementation
// of the browser's own count, and the browser's count is what the smoke run
// reports against the same bound.
func countElements(html string) int {
	n := 0
	for i := 0; i+1 < len(html); i++ {
		if html[i] != '<' {
			continue
		}
		c := html[i+1]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') {
			n++
		}
	}
	// <!DOCTYPE html> is not an element and neither is a comment; both start
	// with '<' followed by '!' and are already excluded above.
	return n
}

var _ = Describe("the element count helper", func() {
	It("counts opening tags and not text, comments or the doctype", func() {
		Expect(countElements("<!DOCTYPE html><p>a<span>b</span></p><!-- c -->")).To(Equal(2))
		Expect(strconv.Itoa(countElements("<br/>"))).To(Equal("1"))
	})
})
