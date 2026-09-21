package main

import (
	"bytes"
	"context"
	"errors"
	"io/fs"
	"net/http"
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

var tabA = live.ID{0xa0, 0xa1, 0xa2, 0xa3, 0xa4, 0xa5, 0xa6, 0xa7, 0xa8, 0xa9, 0xaa, 0xab, 0xac, 0xad, 0xae, 0xaf}

// baseTime is the wall clock the specs use. Nothing under test reads a clock,
// so it is a constant rather than a fixture.
var baseTime = time.Date(2026, 8, 4, 12, 0, 0, 0, time.UTC)

var testOrigins = []string{"http://127.0.0.1:3000"}

func render(c templ.Component) string {
	GinkgoHelper()
	var buf bytes.Buffer
	Expect(c.Render(context.Background(), &buf)).To(Succeed())
	return buf.String()
}

// testFixture is a miniature §2.4: four rows instead of two hundred, two KPIs
// instead of eight, and a short history. The shapes the spec bounds are asserted
// against the REAL corpus in the fixture spec below; everything else reads
// better against numbers a person can hold in their head.
func testFixture() *Fixture {
	return &Fixture{
		Base: Base{
			KPILabels: []string{"requests", "errors"},
			KPI:       []int{100, 200},
			Spark:     [][]int{{100}, {200}},
			Series:    [][]int{{300}, {400}},
			Rows: []BaseRow{
				{ID: 1, Name: "ingest-001", Status: "ok", M1: 10, M2: 1, M3: 1, TS: 0},
				{ID: 2, Name: "replay-002", Status: "warn", M1: 30, M2: 2, M3: 2, TS: 137},
				{ID: 3, Name: "shard-003", Status: "error", M1: 20, M2: 3, M3: 3, TS: 274},
				{ID: 4, Name: "router-004", Status: "warn", M1: 20, M2: 4, M3: 4, TS: 411},
			},
		},
	}
}

func testFeed() *Feed { return NewFeed(testFixture()) }

// mounted is a session as Config.Init would build it, over the test fixture.
func mounted() State {
	feed := testFeed()
	snap := feed.Frame()
	return State{Self: tabA, SID: "sid", Shown: snap, Live: snap, Controls: DefaultControls, NowMs: baseTime.UnixMilli()}
}

func control(name string, fields map[string]string, id uint64) live.Event {
	return live.Event{
		Name: name, FragmentID: FragmentControls, ID: id, At: baseTime,
		Fields: live.NewFields(fields),
	}
}

// feedTick is what the feed emits: the same encoding, built by the same
// function, so a spec cannot assert against a format the feed does not use.
func feedTick(t Tick) live.Event {
	ev := TickEvent(t)
	ev.At = baseTime
	return ev
}

// reduce is the reducer under test, bound to a feed the pure specs never reach:
// the transition builds effects that close over it, and a spec that cares about
// what an effect DOES builds its own feed and runs the effect.
var reduce = Reducer(testFeed())

var _ = Describe("§2.4 the five regions", func() {
	Describe("region A — the KPI strip", func() {
		It("derives the delta from two SERVER samples, not from the client", func() {
			state := mounted()
			state, _ = reduce(state, feedTick(Tick{N: 0, E: []DashEvent{{Kind: FixtureKPI, V: []int{110, 180}}}}))

			kpis := state.KPIs()
			Expect(kpis).To(HaveLen(2))
			Expect(kpis[0].Value).To(Equal(110))
			Expect(kpis[0].DeltaText()).To(Equal("+10.0%"))
			Expect(kpis[0].Band()).To(Equal("up"))
			Expect(kpis[1].DeltaText()).To(Equal("-10.0%"))
			Expect(kpis[1].Band()).To(Equal("down"))
		})

		// §2.4 sizes region A at "8 × ~70 nodes = 560", which is unreachable with
		// a single <polyline>: the region would be an order of magnitude cheaper
		// than the document the spec asks to be measured. BENCH-1 recorded that
		// as reading R-4 and rendered per-point elements; so does this side, and
		// this is the spec that would fail if somebody "optimised" it back.
		It("renders one element per sparkline point", func() {
			state := mounted()
			for n := 0; n < 3; n++ {
				state, _ = reduce(state, feedTick(Tick{N: n, E: []DashEvent{{Kind: FixtureKPI, V: []int{110, 180}}}}))
			}
			Expect(state.KPIs()[0].Bars()).To(HaveLen(4), "one seed plus three samples")
			Expect(strings.Count(render(KPIRegion(state)), "<rect")).To(Equal(8))
		})

		It("keeps the sparkline history at §2.4's 60 points", func() {
			state := mounted()
			for n := 0; n < SparkPoints+20; n++ {
				state, _ = reduce(state, feedTick(Tick{N: n, E: []DashEvent{{Kind: FixtureKPI, V: []int{n, n}}}}))
			}
			Expect(state.KPIs()[0].Bars()).To(HaveLen(SparkPoints))
		})

		// The Next.js tile renders `${sign}${delta.toFixed(1)}%`, and toFixed is
		// not strconv.FormatFloat: at a tie Go takes the even digit and ECMA-262
		// takes the one away from zero. The tie is reachable through the
		// fixture's own integers — prev 400 to 401 is exactly +0.25 %, to 399 is
		// exactly −0.25 % — and a tile whose text differs by one digit between
		// the stacks is a §2.5 conformance failure that would read as a fixture
		// problem.
		//
		// The negative entries below asserted "-0.2" and "-1.7" until L9-1's
		// C-33: the table was written from ECMA-262 step 10's "pick the larger n"
		// without step 6, which has already taken the sign off. It agreed with
		// the implementation and both were wrong together, which is the failure
		// mode a table transcribed from the same misreading as the code always
		// has. The values here are now node v24's, taken from the bench image.
		DescribeTable("the delta is ECMAScript toFixed(1), ties included",
			func(x float64, want string) {
				Expect(Fixed1(x)).To(Equal(want))
			},
			Entry("nothing to round", 10.0, "10.0"),
			Entry("rounds down", 1.2399999, "1.2"),
			Entry("rounds up", 1.26, "1.3"),
			Entry("zero", 0.0, "0.0"),
			Entry("a positive tie goes away from zero, not to the even digit", 0.25, "0.3"),
			Entry("and a negative tie goes away from zero too", -0.25, "-0.3"),
			Entry("above one", 1.75, "1.8"),
			Entry("below minus one", -1.75, "-1.8"),
			Entry("the other quarter, positive", 0.75, "0.8"),
			Entry("the other quarter, negative", -0.75, "-0.8"),
		)

		DescribeTable("and the tile reaches those ties through the arithmetic, not a literal",
			func(value int, want string) {
				k := KPI{Value: value, Delta: float64(value-400) / float64(400) * 100}
				Expect(k.DeltaText()).To(Equal(want))
			},
			Entry("prev 400 → 401 is exactly +0.25 %", 401, "+0.3%"),
			Entry("prev 400 → 399 is exactly −0.25 %", 399, "-0.3%"),
		)
	})

	Describe("region B — the live table and its pipeline", func() {
		// §2.4: the filter and the search filter region B SERVER-SIDE on both
		// stacks, and E4 says a feature server-authoritative in one is
		// server-authoritative in the other. A client-side filter would paint in
		// the same frame here and lose the comparison its meaning.
		DescribeTable("the status filter (DSH-1)",
			func(filter string, wantIDs []int) {
				state := mounted()
				state, _ = reduce(state, control(EventFilter, map[string]string{fieldValue: filter}, 1))
				Expect(idsOf(state.TableView().Rows)).To(Equal(wantIDs))
			},
			Entry("all", "all", []int{1, 2, 3, 4}),
			Entry("ok", "ok", []int{1}),
			Entry("warn", "warn", []int{2, 4}),
			Entry("error", "error", []int{3}),
		)

		It("refuses a filter that is not one of the four", func() {
			state := mounted()
			next, _ := reduce(state, control(EventFilter, map[string]string{fieldValue: "'; drop table"}, 1))
			Expect(next.Controls.Filter).To(Equal("all"))
		})

		It("searches the name column, case-insensitively (DSH-2)", func() {
			state := mounted()
			state, _ = reduce(state, control(EventSearch, map[string]string{fieldQuery: "SHARD"}, 1))
			Expect(idsOf(state.TableView().Rows)).To(Equal([]int{3}))
		})

		// DSH-3's predicate asserts the ORDERING PROPERTY rather than a
		// remembered id — metric_1 ascending, ties broken by id — because the
		// rows churn at 2 Hz and any remembered id goes stale. This is the same
		// comparator, asserted here where it is cheap.
		It("sorts by metric_1 with id as the tie-break (DSH-3)", func() {
			state := mounted()
			state, _ = reduce(state, control(EventSort, nil, 1))
			Expect(state.SortMode()).To(Equal("asc"))
			Expect(idsOf(state.TableView().Rows)).To(Equal([]int{1, 3, 4, 2}))

			state, _ = reduce(state, control(EventSort, nil, 2))
			Expect(state.SortMode()).To(Equal("desc"))
			Expect(idsOf(state.TableView().Rows)).To(Equal([]int{2, 3, 4, 1}),
				"descending by metric_1, and the id tie-break stays ascending in both directions")
		})

		It("cycles the sort off → asc → desc → off", func() {
			Expect(NextSort("off")).To(Equal("asc"))
			Expect(NextSort("asc")).To(Equal("desc"))
			Expect(NextSort("desc")).To(Equal("off"))
		})

		DescribeTable("rows per page (DSH-4)",
			func(n int, want int) {
				state := mounted()
				state, _ = reduce(state, control(EventPerPage, map[string]string{fieldValue: strconv.Itoa(n)}, 1))
				Expect(state.Controls.PerPage).To(Equal(want))
			},
			Entry("50", 50, 50),
			Entry("100", 100, 100),
			Entry("200", 200, 200),
		)

		It("refuses a page size that is not one of the three", func() {
			state := mounted()
			next, _ := reduce(state, control(EventPerPage, map[string]string{fieldValue: "1000000"}, 1))
			Expect(next.Controls.PerPage).To(Equal(DefaultControls.PerPage),
				"a client-chosen page size is a client choosing how much work the server does")
		})

		It("changes only the rows a tick names, and leaves the rest identical", func() {
			state := mounted()
			before := state.Shown.Table.Rows

			state, _ = reduce(state, feedTick(Tick{N: 0, E: []DashEvent{{Kind: FixtureRows, R: []RowUpdate{
				{ID: 2, Status: "ok", M1: 999, M2: 9, M3: 9, TS: 1000},
			}}}}))

			after := state.Shown.Table.Rows
			Expect(after[1].Status).To(Equal("ok"))
			Expect(after[1].M1).To(Equal(999))
			Expect(after[0]).To(BeIdenticalTo(before[0]),
				"an unchanged row is the same pointer, so a tick allocates twenty rows and not two hundred")
			Expect(before[1].Status).To(Equal("warn"),
				"the previous frame is immutable: a paused session is still rendering it")
		})
	})

	Describe("region C — the time series", func() {
		It("shifts one point per series and drops the oldest", func() {
			state := mounted()
			for n := 0; n < SeriesPoints+5; n++ {
				state, _ = reduce(state, feedTick(Tick{N: n, E: []DashEvent{{Kind: FixtureSeries, V: []int{n, n}}}}))
			}
			Expect(state.Shown.Series.Points[0]).To(HaveLen(SeriesPoints))
			Expect(state.SeriesLast()).To(Equal(strconv.Itoa(SeriesPoints + 4)))
		})

		It("renders one element per point, per series (R-4)", func() {
			state := mounted()
			state, _ = reduce(state, feedTick(Tick{N: 0, E: []DashEvent{{Kind: FixtureSeries, V: []int{500, 600}}}}))
			Expect(strings.Count(render(SeriesRegion(state)), "<circle")).To(Equal(4),
				"one seed plus one sample, two series")
		})
	})

	Describe("region D — the event log", func() {
		It("appends and caps at 50", func() {
			state := mounted()
			for n := 1; n <= LogCap+10; n++ {
				state, _ = reduce(state, feedTick(Tick{N: n, E: []DashEvent{{Kind: FixtureLog, Seq: n, Text: "row " + strconv.Itoa(n)}}}))
			}
			entries := state.LogEntries()
			Expect(entries).To(HaveLen(LogCap))
			Expect(entries[0].Seq).To(Equal(11))
			Expect(entries[LogCap-1].Seq).To(Equal(LogCap + 10))
		})
	})

	Describe("region E — the manual panel (AS-3)", func() {
		// It is the one thing on the page that is not pushed: it changes when a
		// person presses the button and at no other time.
		It("advances its sequence on a refresh and at no other time (DSH-6)", func() {
			feed := testFeed()
			Expect(feed.PanelOf("sid").Seq).To(BeZero())
			Expect(feed.PanelOf("sid").Text).To(Equal(DefaultPanelText))

			p := feed.RefreshPanel("sid")
			Expect(p.Seq).To(Equal(1))
			Expect(p.Text).To(Equal("1 rows in error at tick 0"))
			Expect(feed.RefreshPanel("sid").Seq).To(Equal(2))
		})

		It("does not mint an entry for a page that never pressed the button", func() {
			feed := testFeed()
			feed.PanelOf("never-pressed")
			Expect(feed.Panels()).To(BeZero(),
				"D4 asks for the document at the highest rate the stack will serve and presses nothing")
		})

		It("evicts an unbound panel after the grace period and keeps a bound one", func() {
			feed := testFeed()
			now := baseTime
			feed.now = func() time.Time { return now }

			feed.RefreshPanel("gone")
			feed.RefreshPanel("held")
			feed.Join(tabA, "held")
			Expect(feed.Panels()).To(Equal(2))

			now = baseTime.Add(PanelGrace + time.Second)
			feed.mu.Lock()
			feed.sweepPanelsLocked(now)
			feed.mu.Unlock()

			Expect(feed.Panels()).To(Equal(1))
			Expect(feed.PanelOf("held").Seq).To(Equal(1))
		})

		// Region E is NOT a live fragment, and that is what makes AS-3 true: a
		// patch that named it would revert HTMX's swap on the next tick.
		It("is not among the fragments a patch can name", func() {
			for _, f := range Config(testFeed(), testOrigins).Fragments {
				Expect(f.ID).NotTo(ContainSubstring("panel"))
			}
			Expect(render(PanelRegion(Panel{}))).NotTo(ContainSubstring("data-gotth-region"))
		})
	})
})

var _ = Describe("§2.4 pause and resume (DSH-5)", func() {
	// "halts application of live updates (client-visible), stream continues
	// server-side." BENCH-1 read that as server-authoritative (R-2) because a
	// client-side pause would make DSH-5 a local paint here and a round trip
	// there — the category error §2.2 exists to keep out of the tables.
	It("freezes what is shown while the feed keeps moving", func() {
		state := mounted()
		state, _ = reduce(state, feedTick(Tick{N: 1, E: []DashEvent{{Kind: FixtureLog, Seq: 1, Text: "before"}}}))
		Expect(state.Tick()).To(Equal("1"))

		state, _ = reduce(state, control(EventPause, nil, 1))
		Expect(state.Paused()).To(Equal("paused"))

		for n := 2; n <= 6; n++ {
			state, _ = reduce(state, feedTick(Tick{N: n, E: []DashEvent{{Kind: FixtureLog, Seq: n, Text: "during"}}}))
		}
		Expect(state.Tick()).To(Equal("1"), "a pause that lets the tick move is not a pause")
		Expect(state.Live.Tick).To(Equal(6), "the stream continues server-side")
		Expect(state.LogEntries()).To(HaveLen(1))
	})

	It("resumes to the CURRENT tick rather than replaying what was missed", func() {
		state := mounted()
		state, _ = reduce(state, control(EventPause, nil, 1))
		for n := 1; n <= 6; n++ {
			state, _ = reduce(state, feedTick(Tick{N: n, E: []DashEvent{{Kind: FixtureLog, Seq: n, Text: "missed"}}}))
		}

		state, _ = reduce(state, control(EventPause, nil, 2))
		Expect(state.Paused()).To(Equal("running"))
		Expect(state.Tick()).To(Equal("6"))
		Expect(state.LogEntries()).To(HaveLen(6),
			"region D is append-only, so catching up to the current frame shows what accumulated in it")
	})
})

var _ = Describe("§2.5 the committed fixture", func() {
	It("reads the same bytes the Next.js side reads, and says so", func() {
		fixture, err := LoadFixture(DefaultFixtureDir)
		if errors.Is(err, fs.ErrNotExist) {
			Skip("run `npm run fixtures` in bench/ first (§2.5)")
		}
		Expect(err).NotTo(HaveOccurred())

		want, err := os.ReadFile(DefaultFixtureDir + "/dashboard/ticks.jsonl.sha256")
		Expect(err).NotTo(HaveOccurred())
		Expect(fixture.SHA256).To(Equal(strings.Fields(string(want))[0]),
			"neither server generates data; both read the same bytes")
	})

	// The shapes §2.4 bounds, asserted against the REAL corpus rather than
	// against the miniature one the other specs use.
	It("carries §2.4's shapes: 200 rows, 8 KPIs, 60 spark points, 2×120 series", func() {
		fixture, err := LoadFixture(DefaultFixtureDir)
		if errors.Is(err, fs.ErrNotExist) {
			Skip("run `npm run fixtures` in bench/ first (§2.5)")
		}
		Expect(err).NotTo(HaveOccurred())
		Expect(fixture.Base.Rows).To(HaveLen(RowCount))
		Expect(fixture.Base.KPI).To(HaveLen(KPICount))
		Expect(fixture.Base.Spark).To(HaveLen(KPICount))
		Expect(fixture.Base.Spark[0]).To(HaveLen(SparkPoints))
		Expect(fixture.Base.Series).To(HaveLen(2))
		Expect(fixture.Base.Series[0]).To(HaveLen(SeriesPoints))
	})

	// §2.4's region B is "2 Hz, 20 rows changed per tick (10 % churn)". The
	// generator is BENCH-1's and this is the assertion that it is the corpus
	// this side thinks it is.
	It("changes twenty rows on a region-B tick", func() {
		fixture, err := LoadFixture(DefaultFixtureDir)
		if errors.Is(err, fs.ErrNotExist) {
			Skip("run `npm run fixtures` in bench/ first (§2.5)")
		}
		Expect(err).NotTo(HaveOccurred())
		for _, e := range fixture.Ticks[0].E {
			if e.Kind == FixtureRows {
				Expect(e.R).To(HaveLen(20))
				return
			}
		}
		Fail("tick 0 carried no rows event")
	})

	// C-33. The four skips above are the only thing standing between a CI run on
	// a fresh checkout — where bench/fixtures/*/ticks.jsonl is gitignored and
	// absent — and a red suite, and they were guarded with os.IsNotExist, which
	// DOES NOT UNWRAP. LoadFixture and LoadHTMX both wrap with %w so the error
	// names the file and the command that regenerates it, so the guard was false
	// for the one error it exists to catch: five specs across two modules failed
	// while the gate printed "skipped".
	//
	// This pins both halves for both loaders: the wrap stays recognisable, and
	// the idiom that could not see it is asserted to still not see it, so a
	// future author who reaches for the shorter spelling finds out here rather
	// than in CI.
	DescribeTable("a missing input is wrapped so the skip guards can still recognise it",
		func(load func(string) error) {
			err := load(GinkgoT().TempDir())
			Expect(err).To(HaveOccurred())
			Expect(errors.Is(err, fs.ErrNotExist)).To(BeTrue(),
				"the skip guards are errors.Is(err, fs.ErrNotExist); keep the loaders wrapping with %w")
			Expect(os.IsNotExist(err)).To(BeFalse(),
				"os.IsNotExist does not unwrap, which is exactly how these guards were wrong")
		},
		Entry("the fixture", func(dir string) error { _, err := LoadFixture(dir); return err }),
		Entry("the HTMX bundle", func(dir string) error { _, err := LoadHTMX(dir + "/htmx.min.js"); return err }),
	)

	// The authority folds through the same function every session's reducer
	// does, which is what makes "both stacks emit the same logical state for
	// tick N" a property of one function rather than of two that agree today.
	It("folds the authority and a session through the same function", func() {
		feed := testFeed()
		tick := Tick{N: 0, E: []DashEvent{
			{Kind: FixtureRows, R: []RowUpdate{{ID: 1, Status: "error", M1: 5, M2: 5, M3: 5, TS: 10}}},
			{Kind: FixtureKPI, V: []int{111, 222}},
		}}
		feed.applyTick(tick)

		state := mounted()
		state, _ = reduce(state, feedTick(tick))

		Expect(state.Shown.Table.Rows[0].Status).To(Equal(feed.Frame().Table.Rows[0].Status))
		Expect(state.KPIs()[0].Value).To(Equal(111))
		Expect(feed.Frame().Tick).To(Equal(0))
	})

	It("ignores a tick it has already folded", func() {
		state := mounted()
		state, _ = reduce(state, feedTick(Tick{N: 5, E: []DashEvent{{Kind: FixtureLog, Seq: 1, Text: "once"}}}))
		state, _ = reduce(state, feedTick(Tick{N: 5, E: []DashEvent{{Kind: FixtureLog, Seq: 1, Text: "once"}}}))
		Expect(state.LogEntries()).To(HaveLen(1),
			"emitted events are best-effort; region C shifts a point per tick and a double fold is not stale, it is wrong")
	})

	It("round-trips the row encoding H-4's field bound forced", func() {
		updates := []RowUpdate{{ID: 7, Status: "warn", M1: 1, M2: 2, M3: 3, TS: 400}}
		Expect(EncodeRows(updates)).To(Equal("7,warn,1,2,3,400"))
	})

	// The encoding is compact strings rather than one field per value because
	// protocol H-4 bounds Event.fields at 64 and §2.4's "20 rows changed per
	// tick" is 120 values on its own. This is that bound, asserted against a
	// full-sized tick rather than against the comment claiming it.
	It("encodes a full §2.4 tick as one event inside H-4's bound", func() {
		updates := make([]RowUpdate, 0, 20)
		for i := 1; i <= 20; i++ {
			updates = append(updates, RowUpdate{ID: i, Status: "warn", M1: i, M2: i, M3: i, TS: 500})
		}
		ev := TickEvent(Tick{N: 5, E: []DashEvent{
			{Kind: FixtureKPI, V: []int{1, 2, 3, 4, 5, 6, 7, 8}},
			{Kind: FixtureSeries, V: []int{9, 10}},
			{Kind: FixtureRows, R: updates},
			{Kind: FixtureLog, Seq: 3, Text: "an entry"},
		}})
		Expect(ev.Name).To(Equal(EventTick))
		Expect(ev.Fields.Len()).To(BeNumerically("<=", 64))
		Expect(ev.Fields.Get(fieldKPI)).To(Equal("1,2,3,4,5,6,7,8"))
		Expect(ev.Fields.Get(fieldTick)).To(Equal("5"))
	})
})

var _ = Describe("§2.0 the markup hooks the harness drives", func() {
	var page string

	BeforeEach(func() {
		state := mounted()
		state, _ = reduce(state, feedTick(Tick{N: 0, E: []DashEvent{
			{Kind: FixtureKPI, V: []int{110, 180}},
			{Kind: FixtureSeries, V: []int{310, 410}},
			{Kind: FixtureLog, Seq: 1, Text: "ingest-001 scaled"},
		}}))
		page = render(Page(state, Panel{Text: DefaultPanelText}))
	})

	DescribeTable("every data-bench-id the DSH-* interaction files select",
		func(id string) {
			Expect(page).To(ContainSubstring(`data-bench-id="` + id + `"`))
		},
		Entry("DSH-1 setup", "filter-all"),
		Entry("DSH-1 drive + predicate", "filter-warn"),
		Entry("DSH-2 focus + drive", "search"),
		Entry("DSH-3 drive + predicate", "sort-m1"),
		Entry("DSH-4 setup", "per-page-50"),
		Entry("DSH-4 drive", "per-page-200"),
		Entry("DSH-5 drive + predicate", "pause"),
		Entry("DSH-5/DSH-7 predicate subject", "tick"),
		Entry("DSH-6 drive", "refresh"),
		Entry("DSH-6 predicate subject", "panel"),
		Entry("DSH-1..4 row nodes", "row"),
	)

	It("marks all five regions A–E", func() {
		for _, region := range []string{"A", "B", "C", "D", "E"} {
			Expect(page).To(ContainSubstring(`data-bench-region="` + region + `"`))
		}
	})

	// DSH-1 reads row.children[2], DSH-2 reads children[1], DSH-3 reads
	// children[3] and children[0]. The column ORDER is therefore part of the
	// harness contract and not a rendering choice.
	It("renders §2.4's eight columns in the order the predicates index", func() {
		row := render(TableRow(&Row{ID: 42, Name: "ingest-042", Status: "warn", M1: 1, M2: 2, M3: 3, TS: 61_230}))
		Expect(row).To(ContainSubstring("<td>42</td><td>ingest-042</td><td>warn</td><td>1</td><td>2</td><td>3</td>"))
		Expect(row).To(ContainSubstring("<td>01:01.23</td>"))
		Expect(row).To(ContainSubstring(`<span class="badge warn">warn</span>`))
	})

	It("carries the control state the predicates read off the controls", func() {
		Expect(page).To(MatchRegexp(`data-bench-id="filter-all"[^>]*aria-pressed="true"`))
		Expect(page).To(MatchRegexp(`data-bench-id="sort-m1"[^>]*data-bench-value="off"`))
		Expect(page).To(MatchRegexp(`data-bench-id="pause"[^>]*data-bench-value="running"`))
	})

	// DSH-1's predicate reads getAttribute('aria-pressed') === 'true', and React
	// renders the attribute in both states. templ's boolean form would omit it
	// when false, so the two documents would differ on every chip that is not
	// currently selected.
	It("renders aria-pressed on an unpressed chip too, as React does", func() {
		Expect(page).To(MatchRegexp(`data-bench-id="filter-warn"[^>]*aria-pressed="false"`))
		Expect(page).To(MatchRegexp(`data-bench-id="per-page-50"[^>]*aria-pressed="false"`))
	})

	// The other half of DSH-2. The runtime's controlled/uncontrolled rule reads
	// an input's declared value as the server's claim on the property, so an
	// input rendered WITH one would have the user's keystrokes replaced by
	// whatever the server last heard — 150 ms ago, with ten repaints a second
	// arriving in between.
	It("renders the search box with no value attribute", func() {
		Expect(render(SearchInput())).NotTo(ContainSubstring("value="))
		Expect(render(SearchInput())).To(ContainSubstring(`name="` + fieldQuery + `"`))
	})

	It("debounces the search at §2.4's 150 ms, trailing edge", func() {
		// The interval is the fourth component of the binding rather than an
		// attribute of its own: `2ab18690` moved every Bind option inside
		// data-gotth-on so that an option a binding declares scopes to that
		// binding. The empty third component is the key filter this binding
		// does not have. §2.4's 150 ms is unchanged; only where it is written
		// moved, which is why this spec asserts the interval and the event in
		// one string rather than in two independent places.
		Expect(page).To(ContainSubstring(`data-gotth-on="input:` + EventSearch + `::150"`))
		Expect(SearchBinding()).To(HaveKeyWithValue("data-gotth-on", "input:"+EventSearch+"::150"))
		Expect(page).NotTo(ContainSubstring(`data-gotth-debounce=`))
	})

	It("loads the shim before the runtime, undeferred, per §3.2", func() {
		shim := strings.Index(page, ShimRoute)
		runtime := strings.Index(page, "gotth-live.min.js")
		Expect(shim).To(BeNumerically(">", 0))
		Expect(runtime).To(BeNumerically(">", shim))
		Expect(page).To(ContainSubstring(`<script src="` + ShimRoute + `"></script>`))
	})

	It("serves HTMX for region E and counts it against gotth-live (AS-3)", func() {
		Expect(page).To(ContainSubstring(HTMXRoute))
		Expect(page).To(ContainSubstring(`hx-get="` + PanelRoute + `"`))
	})

	// E5 — bounded DOM. §2.4: "Whole document ≤ 4000 elements, of which ≤ 800
	// inline SVG nodes."
	It("stays inside §2.4's element and SVG bounds at the real shapes", func() {
		fixture, err := LoadFixture(DefaultFixtureDir)
		if errors.Is(err, fs.ErrNotExist) {
			Skip("run `npm run fixtures` in bench/ first (§2.5)")
		}
		Expect(err).NotTo(HaveOccurred())

		feed := NewFeed(fixture)
		snap := feed.Frame()
		state := State{Shown: snap, Live: snap, Controls: DefaultControls}
		full := render(Page(state, Panel{}))

		Expect(countElements(full)).To(BeNumerically("<=", 4000))
		Expect(countSVG(full)).To(BeNumerically("<=", 800))
		Expect(countSVG(full)).To(Equal(KPICount*SparkPoints+2*SeriesPoints+KPICount+1),
			"8 sparklines of 60 bars, one chart of 2×120 points, and the nine <svg> roots that carry them")
	})
})

var _ = Describe("§2.0 the shared assets, byte for byte", func() {
	It("resolves the harness shim from the app's own directory", func() {
		shim, err := LoadShim(DefaultShimPath)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(shim)).To(ContainSubstring("window.__bench = bench;"))
	})

	It("serves the stylesheet the Next.js side serves", func() {
		want, err := os.ReadFile("../next/src/app/dashboard.css")
		if errors.Is(err, fs.ErrNotExist) {
			Skip("the Next.js side is not in this checkout")
		}
		Expect(err).NotTo(HaveOccurred())
		Expect(stylesheet).To(Equal(want))
	})

	// This application serves somebody else's JavaScript to a browser, and "the
	// file at this path" is not provenance. A bundle that is not the artifact
	// this repository recorded is refused rather than served — the same rule the
	// conformance suite applies to the same file.
	It("refuses an HTMX bundle whose digest is not the recorded one", func() {
		_, err := LoadHTMX("dashboard.css")
		Expect(err).To(MatchError(ContainSubstring("digest")))
	})

	It("verifies the vendored bundle this repository already keeps", func() {
		htmx, err := LoadHTMX(DefaultHTMXPath)
		if errors.Is(err, fs.ErrNotExist) {
			Skip("the conformance testdata is not in this checkout")
		}
		Expect(err).NotTo(HaveOccurred())
		Expect(htmx).NotTo(BeEmpty())
	})
})

var _ = Describe("Determinism (FR-15)", func() {
	// FR-15's mandatory harness. It is also the property §2.5's conformance test
	// rests on: both servers must emit the same logical state for tick N, and a
	// reducer whose output depended on when it ran could not.
	It("replays the whole session to the same state and the same effects", func() {
		livetest.ReplayN(GinkgoTB(), reduce, mounted(), mixedLog(), 25)
	})

	It("replays to the dashboard the log describes", func() {
		state := mounted()
		for _, ev := range mixedLog() {
			state, _ = reduce(state, ev)
		}
		Expect(state.Controls.Filter).To(Equal("warn"))
		Expect(state.Controls.Sort).To(Equal("asc"))
		Expect(state.Controls.PerPage).To(Equal(50))
		Expect(state.Controls.Paused).To(BeFalse())
		Expect(state.Tick()).To(Equal("6"))
	})

	It("declares every fragment that its own markup changes", func() {
		livetest.AssertDirtyComplete(GinkgoTB(), Config(testFeed(), testOrigins), mounted(), mixedLog())
	})

	// Over-declaring is safe but not free, and §4.6's wire-byte row is counting
	// the patches these decide not to send. Five fragments patched
	// independently is half of what FR-62 asks a live dashboard to demonstrate.
	DescribeTable("a region stays clean when its own inputs did not move",
		func(index int, id string, tick Tick) {
			cfg := Config(testFeed(), testOrigins)
			fragment := cfg.Fragments[index]
			Expect(fragment.ID).To(Equal(id))

			prev := mounted()
			next, _ := reduce(prev, feedTick(tick))
			Expect(fragment.Dirty(prev, next)).To(BeFalse())
		},
		Entry("the KPI strip ignores a row churn", 0, FragmentKPIs,
			Tick{N: 1, E: []DashEvent{{Kind: FixtureRows, R: []RowUpdate{{ID: 1, Status: "ok"}}}}}),
		Entry("the series ignores the event log", 2, FragmentSeries,
			Tick{N: 1, E: []DashEvent{{Kind: FixtureLog, Seq: 1, Text: "x"}}}),
		Entry("the event log ignores a KPI sample", 3, FragmentLog,
			Tick{N: 1, E: []DashEvent{{Kind: FixtureKPI, V: []int{1, 2}}}}),
		Entry("the controls ignore everything the feed does", 4, FragmentControls,
			Tick{N: 1, E: []DashEvent{{Kind: FixtureKPI, V: []int{1, 2}}}}),
	)

	It("re-renders the table when only a control moved", func() {
		cfg := Config(testFeed(), testOrigins)
		table := cfg.Fragments[1]
		Expect(table.ID).To(Equal(FragmentTable))

		prev := mounted()
		next, _ := reduce(prev, control(EventFilter, map[string]string{fieldValue: "warn"}, 1))
		Expect(table.Dirty(prev, next)).To(BeTrue(),
			"the filter is applied server-side, so the rows it removes are removed by a patch")
	})

	It("registers exactly the five names a browser may send", func() {
		Expect(Config(testFeed(), testOrigins).Events).To(ConsistOf(
			EventFilter, EventSearch, EventSort, EventPerPage, EventPause))
	})

	// A client that could send dash.tick could put any reading it liked on its
	// own screen, and readings come from the fixture. Events an effect emits
	// never came from the wire.
	It("does not register the tick a browser must never send", func() {
		Expect(Config(testFeed(), testOrigins).Events).NotTo(ContainElement(EventTick))
	})
})

var _ = Describe("The subscription, and losing it", func() {
	// Losing the subscription is the failure worth acting on: the tab keeps
	// rendering the last frame it saw and stops learning about any other, which
	// looks right while being wrong.
	It("re-subscribes when the library says the failure was transient", func() {
		_, effects := reduce(mounted(), effectFailed(SourceSubscribe, "true"))
		Expect(effects).To(HaveLen(1))
		Expect(effects[0].Source).To(Equal(SourceSubscribe))
	})

	DescribeTable("and does not otherwise",
		func(source, retryable string) {
			_, effects := reduce(mounted(), effectFailed(source, retryable))
			Expect(effects).To(BeEmpty())
		},
		Entry("a terminal failure re-runs whatever made it terminal", SourceSubscribe, "false"),
		Entry("an unreadable classification parses as false", SourceSubscribe, "yes please"),
		Entry("another effect's failure is not this one's", "dash.something-else", "true"),
	)

	It("hands a joining session the frame as of the instant it registered", func() {
		feed := testFeed()
		feed.applyTick(Tick{N: 5, E: []DashEvent{{Kind: FixtureLog, Seq: 1, Text: "before the join"}}})

		snap := feed.Join(tabA, "sid")
		Expect(snap.Tick).To(Equal(5))
		Expect(snap.Log.Entries).To(HaveLen(1))
		Expect(feed.Subscribers()).To(Equal(1))
	})

	It("leaves no subscription behind when a session ends", func() {
		feed := testFeed()
		feed.Join(tabA, "sid")
		feed.Leave(tabA, "sid")
		Expect(feed.Subscribers()).To(BeZero(),
			"a teardown that did not unsubscribe is a leak one connection at a time")
	})

	It("refuses to pump a session that never joined", func() {
		err := testFeed().SubscribeEffect().
			Run(context.Background(), specSession(), func(live.Event) error { return nil })
		Expect(err).To(MatchError(ContainSubstring("not subscribed")),
			"Config.Init must Join before it returns a subscribe effect, and saying so is cheaper than a silent no-op")
	})

	// "refuses an effect it has no executor for" is gone with the executor: an
	// effect this feed does not own is one it cannot be handed, now that a
	// live.Effect[live.AnonymousIdentity] carries its own Run.
})

// specSession is a session as this application's own Authenticate would build
// one: live.Anonymous, because a read-only operator dashboard has no accounts
// and nothing in an effect reads an identity. livetest refuses a nil one,
// which is the trap live.Session[live.AnonymousIdentity]{} already sets.
func specSession() live.Session[live.AnonymousIdentity] {
	GinkgoHelper()
	identity, err := live.Anonymous(&http.Request{})
	Expect(err).NotTo(HaveOccurred())
	return livetest.NewSession(GinkgoTB(), tabA, identity)
}

func effectFailed(source, retryable string) live.Event {
	return live.Event{
		Name: live.EffectFailedEvent, At: baseTime,
		Fields: live.NewFields(map[string]string{
			live.EffectFailedSourceField:    source,
			live.EffectFailedRetryableField: retryable,
		}),
	}
}

// mixedLog is one session's whole event log: every control §2.4 names, a pause
// straddling several ticks, and feed ticks moving all four pushed regions.
func mixedLog() []live.Event {
	return []live.Event{
		feedTick(Tick{N: 0, E: []DashEvent{
			{Kind: FixtureKPI, V: []int{110, 180}},
			{Kind: FixtureSeries, V: []int{310, 410}},
			{Kind: FixtureRows, R: []RowUpdate{{ID: 1, Status: "warn", M1: 40, M2: 1, M3: 1, TS: 0}}},
			{Kind: FixtureLog, Seq: 1, Text: "ingest-001 scaled"},
		}}),
		control(EventFilter, map[string]string{fieldValue: "warn"}, 1),
		feedTick(Tick{N: 1, E: []DashEvent{{Kind: FixtureLog, Seq: 2, Text: "replay-002 drained"}}}),
		control(EventSearch, map[string]string{fieldQuery: "0"}, 2),
		control(EventSort, nil, 3),
		control(EventPause, nil, 4),
		feedTick(Tick{N: 2, E: []DashEvent{{Kind: FixtureKPI, V: []int{120, 170}}}}),
		feedTick(Tick{N: 3, E: []DashEvent{{Kind: FixtureRows, R: []RowUpdate{{ID: 4, Status: "error", M1: 5, M2: 5, M3: 5, TS: 300}}}}}),
		control(EventPause, nil, 5),
		control(EventPerPage, map[string]string{fieldValue: "50"}, 6),
		feedTick(Tick{N: 6, E: []DashEvent{
			{Kind: FixtureSeries, V: []int{320, 420}},
			{Kind: FixtureLog, Seq: 3, Text: "shard-003 promoted"},
		}}),
	}
}

func idsOf(rows []*Row) []int {
	out := make([]int, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.ID)
	}
	return out
}

// countElements counts opening tags, which is close enough for an E5 guard and
// deliberately does not parse: the browser's own count is what the smoke run
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
	return n
}

// countSVG counts the inline SVG nodes §2.4 bounds at 800: the per-point
// elements R-4 requires, plus the eight sparkline roots and the chart's. The
// roots are counted because they are nodes, and because 729 is the number
// BENCH-1's smoke run reports for the Next.js document against the same bound.
func countSVG(html string) int {
	return strings.Count(html, "<svg") + strings.Count(html, "<rect") + strings.Count(html, "<circle")
}
