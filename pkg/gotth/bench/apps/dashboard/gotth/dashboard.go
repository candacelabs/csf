// The gotth-live side of equivalence-spec §2.4's live dashboard.
//
// It is built to §2.4's regions A–E and its six controls, and not to
// examples/dashboard, which is a different program: that example has meters,
// alerts and its own controls rather than a KPI strip, a 200-row table, a time
// series, an event log and a manual panel (bench/README.md, ambiguity Q-E).
//
// This is the demanding case: ≈55 logical updates per second per session (the
// rates §2.4 states; their stated sum of 53 is BENCH-1's ambiguity Q-C and the
// rates are implemented as written), a document of ~3,000 elements of which
// ~720 are inline SVG, and the source of the headline push-latency number.
//
// # Declared differences from bench/apps/dashboard/next
//
// §2.6's register is closed at freeze, so nothing here adds a row to it. What
// follows is where this side lands against the rows that already exist, plus
// the two differences that are this implementation's own and are declared here
// rather than discovered in a DOM diff. The chat app records its own the same
// way, in the file where the difference lives.
//
//   - AS-3, region E. Plain HTMX on this stack per FR-62; a Server Action form
//     on the other. Same visible behaviour, both mechanisms in both apps, and
//     HTMX's bytes counted against this one (§3.5). It is the reason region E
//     is not a live fragment: see view.templ.
//   - AS-4, the push transport. Liquid proto over the ADR-001 transport here,
//     SSE/WS there. Inherent — it is the thing being compared.
//   - AS-7, the static shell. A Server Component there; on this stack the whole
//     document is server-rendered by templ and there is no counterpart, which
//     is not a difference in what a browser receives.
//   - Region E's panel is keyed by a per-page-load COOKIE here and by a
//     per-page-load prop there, because a plain HTMX GET carries cookies and
//     nothing else. Two tabs of this app in one browser therefore share region
//     E's refresh counter, where two Next.js tabs do not. No DSH-* interaction
//     opens a second tab — CTR-7 is the counter's — and the alternative, a
//     panel keyed by the live session, is unreachable from the ordinary GET
//     that "plain HTMX" means. Declared, not hidden; bench.go carries the
//     mechanism and feed.go the eviction rule it forces.
//   - Every session folds its own copy of the 200 rows, where the Next.js store
//     keeps one array and derives per-session views from it. That is forced by
//     live.Event.Fields being map[string]string — see State — it costs real
//     memory in D3, and it is a property of today's API rather than a choice
//     made to look good.
package main

import (
	"context"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// §2.4's shapes, as named constants rather than as literals in a loop, so a
// change that breaks E5's bounds breaks something a reviewer can find.
const (
	// KPICount and SparkPoints are region A: "8 tiles: label, value, delta %,
	// inline SVG sparkline of 60 points".
	KPICount    = 8
	SparkPoints = 60

	// RowCount is region B: "200 rows × 8 cols".
	RowCount = 200

	// SeriesPoints is region C: "2 series × 120 points, shift-one-point".
	SeriesPoints = 120

	// LogCap is region D: "append-only, capped 50 entries".
	LogCap = 50

	// SearchDebounce is §2.4's "debounced 150 ms on both stacks with identical
	// debounce implementation semantics". Those semantics, as BENCH-1 wrote them
	// down in DSH-2 so this side could match rather than approximate: TRAILING
	// edge, timer reset on every keystroke, one request fired 150 ms after the
	// last one, no leading call and no maximum wait. That is exactly what
	// live.Bind.Debounce renders and what the runtime implements.
	SearchDebounce = 150 * time.Millisecond
)

// Statuses is region B's status enum, in the order the filter renders them.
var Statuses = []string{"ok", "warn", "error"}

// StatusFilters is §2.4's "all | ok | warn | error".
var StatusFilters = append([]string{"all"}, Statuses...)

// PerPageChoices is §2.4's "select 50 / 100 / 200", rendered as buttons.
//
// BENCH-1's reading R-3: "select" is read as "choose one of". Buttons make DSH-1
// and DSH-4 a native pointerdown, which is what §3.2's t_input is defined
// against; a <select> would put the causal start in a change event the spec does
// not define. This side follows that reading rather than reopening it.
var PerPageChoices = []int{50, 100, 200}

// The fragment identifiers, one per live region. Region E is not among them:
// §2.4 gives it to HTMX on this stack (FR-62, AS-3), so it is not server-owned
// markup and no patch may name it.
const (
	FragmentKPIs     = "dash.kpis"     // region A
	FragmentTable    = "dash.table"    // region B
	FragmentSeries   = "dash.series"   // region C
	FragmentLog      = "dash.log"      // region D
	FragmentControls = "dash.controls" // the control bar
)

// The event names a browser may send: one per control, and none carrying a
// discriminator. Config.Events is default-deny, so five names bound what a
// hostile client can ask for.
const (
	EventFilter  = "dash.filter"
	EventSearch  = "dash.search"
	EventSort    = "dash.sort"
	EventPerPage = "dash.perpage"
	EventPause   = "dash.pause"
)

// EventTick is the event the feed emits into every subscribed session.
//
// It is deliberately NOT in Config.Events: a client that could send dash.tick
// could put any reading it liked on its own screen, and readings come from the
// fixture. Events an effect emits never came from the wire.
const EventTick = "dash.tick"

// The field names on the events the feed emits and the controls send.
const (
	fieldTick   = "tick"
	fieldRows   = "rows"
	fieldKPI    = "kpi"
	fieldSeries = "series"
	fieldLogSeq = "log_seq"
	fieldLogTxt = "log_text"
	fieldValue  = "v"
	fieldQuery  = "q"
)

/* --------------------------------------------------------------- values --- */

// Row is one line of region B: §2.4's eight columns.
type Row struct {
	ID     int
	Name   string
	Status string
	M1     int
	M2     int
	M3     int
	// TS is milliseconds since T0, so the rendered clock is a server number.
	TS int64
}

// Table is region B's 200 rows as one immutable value, held behind a pointer
// for the reason every other app in this tree gives: a State carrying a slice
// directly is not comparable, and internal/session reports a non-comparable
// state as changed on every transition.
//
// The rows are pointers so a tick that changes twenty of them allocates twenty
// rows and one 200-pointer slice rather than copying two hundred structs.
type Table struct {
	Version uint64
	Rows    []*Row
}

// KPISet is region A: eight values, the previous sample the delta is computed
// against, and SparkPoints of history per tile.
type KPISet struct {
	Version uint64
	Labels  []string
	Values  []int
	Prev    []int
	Spark   [][]int
}

// SeriesSet is region C: two series of SeriesPoints, shifted one point per
// second.
type SeriesSet struct {
	Version uint64
	Points  [2][]int
}

// LogEntry is one line of region D.
type LogEntry struct {
	Seq  int
	Text string
	TS   int64
}

// EventLog is region D: append-only, capped at LogCap.
type EventLog struct {
	Version uint64
	Entries []LogEntry
}

// Snap is one server frame: every region's immutable value plus the tick it
// reflects. It is comparable — an int and four pointers — which is what lets
// State hold two of them.
type Snap struct {
	Tick   int
	Table  *Table
	KPIs   *KPISet
	Series *SeriesSet
	Log    *EventLog
}

// TickNone is the tick of a frame that has folded nothing.
//
// It is −1 and not 0 because the fixture's FIRST tick is numbered 0, and a zero
// here would make FoldSnap drop it as already folded — an off-by-one that would
// cost region D its first entry and nothing else, which is exactly the kind that
// survives review. State.Tick renders it as 0, for the reason given there.
const TickNone = -1

// Controls is §2.4's six-control surface, all server-authoritative (E4).
type Controls struct {
	Filter  string
	Search  string
	Sort    string // "off" | "asc" | "desc"
	PerPage int
	Paused  bool
}

// DefaultControls is the state a session mounts in.
//
// PerPage is 200, which is the size §2.4's DOM bound is stated against
// ("200 × 10 = 2000") and the state DSH-7's push row is measured in. DSH-4
// drives 50 → 200 and establishes 50 in its own setup; making 50 the default
// instead would mean every other interaction was measured on a quarter of the
// table the spec sizes. BENCH-1 took the same default and said so.
var DefaultControls = Controls{Filter: "all", Sort: "off", PerPage: 200}

// Panel is region E — "a small panel refreshed by an explicit button press".
type Panel struct {
	Text string
	// Seq is the refresh count, so a repaint is provable rather than plausible:
	// DSH-6's predicate is that it went up by one.
	Seq int
	// TS is the tick the text was computed at, mirroring the Next.js Panel's
	// field. Neither side renders it — the tick is already inside Text — and it
	// is kept so the two structures do not differ in a way a reader would have
	// to check the components to explain.
	TS int64
}

// DefaultPanelText is what region E says before anybody presses the button,
// word for word as the Next.js side renders it.
const DefaultPanelText = "Press refresh to load the operator panel."

/* ---------------------------------------------------------------- state --- */

// State is one browser tab's view of the dashboard.
//
// Shown and Live are the whole of DSH-5, and they are two pointers rather than
// two copies. §2.4: "Pause / resume | halts application of live updates
// (client-visible), stream continues server-side". So the feed keeps folding
// into Live whatever a session's controls say, and Shown stops following it
// while paused. A resume shows the CURRENT tick rather than replaying what was
// missed, which is BENCH-1's reading R-2 and the gotth-live dashboard's own
// behaviour.
//
// Why every session folds its own copy of the 200 rows, when the Next.js store
// keeps ONE array and derives per-session views from it: live.Event.Fields is
// map[string]string, so an effect cannot hand a session a pointer to a shared
// immutable frame, and a reducer that reached into the feed for one would not
// be a pure function of (state, event). The per-session cost is real, it is
// visible in D3, and bench/README.md reports it as a property of today's API
// rather than as an implementation choice.
type State struct {
	Self live.ID
	// SID is the bench session cookie this tab was served with. It is state so
	// Teardown can release region E's panel entry without the upgrade request's
	// context, which by then is gone. Region E is not rendered from it — the
	// panel is HTMX's, not a fragment's — so it never appears in a Dirty.
	SID string

	Shown    Snap
	Live     Snap
	Controls Controls
	NowMs    int64
}

/* -------------------------------------------------------------- derived --- */

// Tick is what region B's [data-bench-id=tick] carries, and what DSH-5 and
// DSH-7 read. It is the SHOWN tick: a paused session's tick must not move, or
// the pause is not a pause.
//
// A frame that has folded nothing renders 0 rather than TickNone. The attribute
// is a fixture position that the Next.js store starts at 0 and that DSH-5 and
// DSH-7 compare with `>`, so the two documents must not differ during the
// hundred milliseconds before the first tick lands.
func (s State) Tick() string {
	if s.Shown.Tick < 0 {
		return "0"
	}
	return strconv.Itoa(s.Shown.Tick)
}

// Paused is the pause control's data-bench-value.
func (s State) Paused() string {
	if s.Controls.Paused {
		return "paused"
	}
	return "running"
}

// SortMode is the sort control's data-bench-value, which DSH-3's predicate
// reads and whose setup cycles back to "off".
func (s State) SortMode() string { return s.Controls.Sort }

// NextSort is the mode one press moves to: off → asc → desc → off.
func NextSort(mode string) string {
	switch mode {
	case "off":
		return "asc"
	case "asc":
		return "desc"
	default:
		return "off"
	}
}

// TableView is region B as one render sees it: the visible page and the number
// of rows that matched before paging.
//
// It exists so the pipeline runs ONCE per render. The region renders three
// things derived from it — the rows, the rendered count and the "of m" total —
// and a method per derivation would filter, search and sort 200 rows three
// times, twice a second, per session. At §3.6's thousand sessions that is the
// difference between two thousand passes a second and six thousand, in the
// path D2 and D4 are measuring.
type TableView struct {
	Rows  []*Row
	Total int
}

// Count is the rendered row count, which the harness reads as
// [data-bench-id=row-count]'s data-bench-value.
func (v TableView) Count() string { return strconv.Itoa(len(v.Rows)) }

// TotalText is the "of m" half of the count line.
func (v TableView) TotalText() string { return strconv.Itoa(v.Total) }

// TableView applies §2.4's filter/search/sort/page pipeline SERVER-SIDE — on
// both stacks, because §2.4 says the filter and the search filter region B
// server-side on both and E4 says a feature server-authoritative in one is
// server-authoritative in the other. A client-side filter would paint in the
// same frame here and lose the comparison its meaning.
//
// It is a pure function of the shown table and the controls, which is what lets
// it be called from a render.
func (s State) TableView() TableView {
	page, total := Visible(s.Shown.Table, s.Controls)
	return TableView{Rows: page, Total: total}
}

// Visible is the pipeline as a free function, so a spec can drive it without a
// State.
func Visible(table *Table, c Controls) (page []*Row, total int) {
	rows := table.rows()

	out := make([]*Row, 0, len(rows))
	needle := strings.ToLower(c.Search)
	for _, row := range rows {
		if c.Filter != "all" && row.Status != c.Filter {
			continue
		}
		if needle != "" && !strings.Contains(strings.ToLower(row.Name), needle) {
			continue
		}
		out = append(out, row)
	}
	total = len(out)

	if c.Sort != "off" {
		dir := 1
		if c.Sort == "desc" {
			dir = -1
		}
		// "stable sort by id unless user sorts" (§2.4) is the base order, so the
		// tie-break is id — which is also exactly the comparator DSH-3's
		// predicate asserts the rendered order against.
		sort.SliceStable(out, func(i, j int) bool {
			if out[i].M1 != out[j].M1 {
				return (out[i].M1-out[j].M1)*dir < 0
			}
			return out[i].ID < out[j].ID
		})
	}

	if len(out) > c.PerPage {
		out = out[:c.PerPage]
	}
	return out, total
}

func (t *Table) rows() []*Row {
	if t == nil {
		return nil
	}
	return t.Rows
}

func (t *Table) version() uint64 {
	if t == nil {
		return 0
	}
	return t.Version
}

// KPIs is region A's eight tiles, each with the delta % §2.4 asks for. The
// delta is a SERVER derivation from two server samples (E4), never a client
// one.
func (s State) KPIs() []KPI {
	set := s.Shown.KPIs
	if set == nil {
		return nil
	}
	out := make([]KPI, 0, len(set.Values))
	for i, v := range set.Values {
		prev := 1
		if i < len(set.Prev) && set.Prev[i] != 0 {
			prev = set.Prev[i]
		}
		label := ""
		if i < len(set.Labels) {
			label = set.Labels[i]
		}
		spark := []int(nil)
		if i < len(set.Spark) {
			spark = set.Spark[i]
		}
		out = append(out, KPI{
			Index: i,
			Label: label,
			Value: v,
			Delta: float64(v-prev) / float64(prev) * 100,
			Spark: spark,
		})
	}
	return out
}

// KPI is one rendered tile.
type KPI struct {
	Index int
	Label string
	Value int
	Delta float64
	Spark []int
}

// ValueText and DeltaText are the two numbers a tile renders.
func (k KPI) ValueText() string { return strconv.Itoa(k.Value) }

// DeltaText is the delta with an explicit sign and one decimal, matching the
// Next.js side's formatDelta.
func (k KPI) DeltaText() string {
	sign := ""
	if k.Delta > 0 {
		sign = "+"
	}
	return sign + Fixed1(k.Delta) + "%"
}

// Fixed1 is ECMAScript's Number.prototype.toFixed(1), transcribed.
//
// It is not strconv.FormatFloat(x, 'f', 1, 64), and the difference is one case:
// a value whose exact decimal expansion is a tie at the second place. Go's
// formatter resolves such a tie to the EVEN digit; ECMA-262 resolves it away
// from zero. So 0.25 renders "0.3" on the Next.js side and Go alone would render
// "0.2".
//
// That value is reachable, which is why this exists rather than a comment saying
// it is unlikely. The delta is 100·(v−prev)/prev over integers the fixture
// supplies, and prev = 400 with v = 401 is exactly +0.25 while v = 399 is
// exactly −0.25 — 8 tiles at 1 Hz for an hour is 28,800 chances at each. A KPI
// tile whose text differs by one digit between the stacks is a §2.5 conformance
// failure that would read as a fixture problem, and E1 is checked by comparing
// rendered DOM.
//
// AWAY FROM ZERO, NOT TOWARD +∞, and the distinction is the whole of L9-1's
// C-33 finding on this function. ECMA-262 21.1.3.3 reads "let n be an integer
// for which n / 10^f − x is as close to zero as possible; if there are two such
// n, pick the larger n" — but step 6 has already replaced x with −x and stashed
// the sign, so "larger" is chosen over the MAGNITUDE. This function first
// rounded −0.25 to "-0.2" on the letter of step 10 read without step 6; node
// prints "-0.3", and node is the oracle §2.5 conformance is measured against.
// The corrected behaviour is cross-checked against it over every delta the
// fixture's own integers can produce, not against the spec text alone.
//
// The float arithmetic itself is NOT re-derived: both stacks compute the same
// IEEE-754 double from the same expression in the same order, so the only thing
// that has to be transcribed is the rounding of that double. A tie is detected
// exactly and cheaply — the exact value of a double x is a one-decimal tie iff
// 4x is an odd integer, and multiplying by 4 is exact — so the ordinary path is
// one comparison and the library formatter.
func Fixed1(x float64) string {
	q := x * 4
	if q != math.Trunc(q) || math.Mod(q, 2) == 0 {
		return strconv.FormatFloat(x, 'f', 1, 64)
	}
	// The sign comes off first, exactly as ECMA-262's step 6 takes it off, and
	// the tie is then resolved on the magnitude.
	sign := ""
	if x < 0 {
		sign, x = "-", -x
	}
	// x*10 is exact here, since a tie is an odd multiple of a quarter and ten
	// quarters need one fraction bit. The tenths are rendered from an integer
	// rather than handed back to the float formatter, because n/10 is not
	// representable and formatting it would put the rounding decision back where
	// it started. n ≥ 2 for every tie, so there is no "-0.0" to get wrong.
	n := int64(math.Floor(x*10)) + 1
	return sign + strconv.FormatInt(n/10, 10) + "." + strconv.FormatInt(n%10, 10)
}

// Band is the delta badge's class, so a tile's repaint is not one text node.
func (k KPI) Band() string {
	switch {
	case k.Delta > 0.05:
		return "up"
	case k.Delta < -0.05:
		return "down"
	default:
		return "flat"
	}
}

// Bars is the sparkline's geometry: SparkPoints elements, one per point.
//
// §2.4 sizes region A at "8 × ~70 nodes = 560", which is unreachable with a
// single <polyline> — the region would be an order of magnitude cheaper than the
// document the spec asks to be measured. BENCH-1 recorded that as reading R-4
// and rendered per-point elements on the Next.js side; this does the same, and
// the document's SVG budget lands at 8×60 + 2×120 = 720 of §2.4's ≤ 800.
func (k KPI) Bars() []Bar {
	out := make([]Bar, 0, len(k.Spark))
	for i, v := range k.Spark {
		h := max(1, int(float64(v)/1000*20+0.5))
		out = append(out, Bar{X: i, Y: 20 - h, H: h})
	}
	return out
}

// Bar is one sparkline element's geometry.
type Bar struct{ X, Y, H int }

// SeriesPointsOf is region C's geometry: one element per point, per series.
func (s State) SeriesPointsOf(series int) []Point {
	set := s.Shown.Series
	if set == nil || series < 0 || series > 1 {
		return nil
	}
	values := set.Points[series]
	out := make([]Point, 0, len(values))
	for i, v := range values {
		out = append(out, Point{CX: i, CY: 100 - int(float64(v)/1000*100+0.5)})
	}
	return out
}

// Point is one series element's geometry.
type Point struct{ CX, CY int }

// SparkViewBox and ChartViewBox are regions A and C's SVG coordinate systems,
// derived from the point counts rather than written out, so a change to either
// count moves the geometry with it instead of silently clipping it.
var (
	SparkViewBox = "0 0 " + strconv.Itoa(SparkPoints) + " 20"
	ChartViewBox = "0 0 " + strconv.Itoa(SeriesPoints) + " 100"
)

// BoolAttr renders a boolean as the string an ARIA state attribute takes.
//
// It exists because templ's `attr?={ b }` omits the attribute when b is false,
// and React renders aria-pressed="false" instead. DSH-1's predicate reads
// getAttribute('aria-pressed'), so an omitted attribute is null where the other
// stack has a string — and the two documents would differ on every control that
// is not currently selected.
func BoolAttr(b bool) string { return strconv.FormatBool(b) }

// SeriesLast is what region C's data-bench-value carries: the newest point of
// the first series, so a repaint is provable from an attribute.
func (s State) SeriesLast() string {
	set := s.Shown.Series
	if set == nil || len(set.Points[0]) == 0 {
		return "0"
	}
	return strconv.Itoa(set.Points[0][len(set.Points[0])-1])
}

// LogEntries is region D's capped list.
func (s State) LogEntries() []LogEntry {
	if s.Shown.Log == nil {
		return nil
	}
	return s.Shown.Log.Entries
}

// LogCount is region D's count attribute.
func (s State) LogCount() string { return strconv.Itoa(len(s.LogEntries())) }

// Stamp is the absolute timestamp a row and a log entry render.
//
// Formatted from milliseconds-since-T0 with explicit arithmetic rather than a
// locale-aware formatter, because the container's locale and timezone are not
// part of the equivalence contract and the Next.js side formats the same
// mm:ss.cs from the same number.
func Stamp(ms int64) string {
	total := ms / 1000
	return pad(int((total/60)%60)) + ":" + pad(int(total%60)) + "." + pad(int((ms%1000)/10))
}

func pad(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

/* -------------------------------------------------------------- reducer --- */

// Reducer returns the pure state transition, bound to the feed its effects
// act on.
//
// It is a constructor because a live.Effect[live.AnonymousIdentity] carries its own behaviour since the
// 2026-09-03 ruling, so a reducer scheduling one has to hold what that effect
// closes over.
//
// It reads no clock, performs no I/O and touches the shared feed not at all.
// Every control is a round trip: a click returns an effect only where the feed
// has to be told (it does not, for any of the six), and otherwise changes this
// session's own controls — which is server state either way, because this
// reducer runs on the server.
func Reducer(feed *Feed) live.Reducer[State, live.AnonymousIdentity] {
	return func(state State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
		if !ev.At.IsZero() {
			state.NowMs = ev.At.UnixMilli()
		}

		switch ev.Name {
		case EventTick:
			return tick(state, ev), nil

		case EventFilter:
			v := ev.Fields.Get(fieldValue)
			if !slices.Contains(StatusFilters, v) {
				return state, nil
			}
			state.Controls.Filter = v
			return state, nil

		case EventSearch:
			// The debounce is the CLIENT's (live.Bind.Debounce renders the interval
			// as a component of this binding inside data-gotth-on, and the runtime
			// holds the timer against that binding), so by the time this runs the
			// trailing edge has already fired. The value is the input's own,
			// serialised from its name because it is not inside a form.
			state.Controls.Search = ev.Fields.Get(fieldQuery)
			return state, nil

		case EventSort:
			state.Controls.Sort = NextSort(state.Controls.Sort)
			return state, nil

		case EventPerPage:
			n, err := strconv.Atoi(ev.Fields.Get(fieldValue))
			if err != nil || !slices.Contains(PerPageChoices, n) {
				return state, nil
			}
			state.Controls.PerPage = n
			return state, nil

		case EventPause:
			state.Controls.Paused = !state.Controls.Paused
			if !state.Controls.Paused {
				// A resume shows the CURRENT tick rather than replaying what was
				// missed (R-2). One assignment, because Live has been following the
				// feed the whole time.
				state.Shown = state.Live
			}
			return state, nil

		case live.EffectFailedEvent:
			return state, retrySubscription(feed, ev)
		}

		// An unknown name cannot reach here from a browser — the library refuses
		// unregistered names before the reducer runs — so anything arriving here is
		// something the library synthesised and this application has no answer for.
		return state, nil
	}
}

// retrySubscription decides what to do about a failed effect.
//
// The only effect this application has is the subscription, and losing it is
// the failure worth acting on: the tab keeps rendering the last frame it saw
// and stops learning about any other, which looks right while being wrong. It
// re-subscribes only when the library says the failure was transient —
// re-running a terminal failure re-runs whatever made it terminal — and an
// unreadable classification parses as false.
func retrySubscription(feed *Feed, ev live.Event) []live.Effect[live.AnonymousIdentity] {
	retryable, _ := strconv.ParseBool(ev.Fields.Get(live.EffectFailedRetryableField))
	if retryable && ev.Fields.Get(live.EffectFailedSourceField) == SourceSubscribe {
		return []live.Effect[live.AnonymousIdentity]{feed.SubscribeEffect()}
	}
	return nil
}

// tick folds one feed emission into Live, and into Shown unless this session is
// paused.
func tick(state State, ev live.Event) State {
	next, ok := FoldSnap(state.Live, ev)
	if !ok {
		return state
	}
	state.Live = next
	if !state.Controls.Paused {
		state.Shown = next
	}
	return state
}

// FoldSnap folds one EventTick into a frame, reporting whether it moved.
//
// The feed's own authoritative frame goes through this function and so does
// every session's, which is what makes "both stacks emit the same logical state
// for tick N" (§2.5's conformance test) a property of one function rather than
// of two implementations that agree today. A session that joins at tick 900 is
// handed the feed's frame and folds 901 onward; the feed folded 0..900 through
// the same code.
//
// The encoding is compact strings rather than one field per value, and that is
// forced rather than chosen: protocol H-4 bounds Event.fields at 64, and §2.4's
// "20 rows changed per tick" is 120 field values on its own. It never reaches a
// wire — an emitted event is delivered in-process to the session actor and what
// leaves the server is rendered HTML — so this is not the JSON side channel the
// review checklist §3.2 forbids. It is a shape the library's payload type
// forced, and it is recorded as one here rather than left to be inferred.
func FoldSnap(prev Snap, ev live.Event) (Snap, bool) {
	n, err := strconv.Atoi(ev.Fields.Get(fieldTick))
	if err != nil || n <= prev.Tick {
		// Out of order or already folded. Emitted events are best-effort, and a
		// tick folded twice would shift region C's window by two points.
		return prev, false
	}
	next := prev
	next.Tick = n

	if raw := ev.Fields.Get(fieldRows); raw != "" {
		next.Table = applyRows(next.Table, raw)
	}
	if raw := ev.Fields.Get(fieldKPI); raw != "" {
		next.KPIs = applyKPI(next.KPIs, parseInts(raw))
	}
	if raw := ev.Fields.Get(fieldSeries); raw != "" {
		next.Series = applySeries(next.Series, parseInts(raw))
	}
	if raw := ev.Fields.Get(fieldLogTxt); raw != "" {
		seq, _ := strconv.Atoi(ev.Fields.Get(fieldLogSeq))
		next.Log = applyLog(next.Log, LogEntry{Seq: seq, Text: raw, TS: int64(n) * TickMs})
	}
	return next, true
}

// EncodeRows is the compact form applyRows parses: `id,status,m1,m2,m3,ts` per
// row, rows separated by `;`. It is exported so the feed and the reducer cannot
// disagree about the format, and so a spec can round-trip it.
func EncodeRows(updates []RowUpdate) string {
	var b strings.Builder
	for i, u := range updates {
		if i > 0 {
			b.WriteByte(';')
		}
		b.WriteString(strconv.Itoa(u.ID))
		b.WriteByte(',')
		b.WriteString(u.Status)
		b.WriteByte(',')
		b.WriteString(strconv.Itoa(u.M1))
		b.WriteByte(',')
		b.WriteString(strconv.Itoa(u.M2))
		b.WriteByte(',')
		b.WriteString(strconv.Itoa(u.M3))
		b.WriteByte(',')
		b.WriteString(strconv.FormatInt(u.TS, 10))
	}
	return b.String()
}

func applyRows(table *Table, raw string) *Table {
	rows := table.rows()
	next := make([]*Row, len(rows))
	copy(next, rows)

	byID := make(map[int]int, len(next))
	for i, row := range next {
		byID[row.ID] = i
	}

	for _, part := range strings.Split(raw, ";") {
		f := strings.Split(part, ",")
		if len(f) != 6 {
			continue
		}
		id, err := strconv.Atoi(f[0])
		if err != nil {
			continue
		}
		at, ok := byID[id]
		if !ok {
			continue
		}
		// A changed row is a NEW row value, so every session that is still
		// holding the old one — a paused one, for instance — keeps rendering
		// exactly what it was rendering.
		row := *next[at]
		row.Status = f[1]
		row.M1, _ = strconv.Atoi(f[2])
		row.M2, _ = strconv.Atoi(f[3])
		row.M3, _ = strconv.Atoi(f[4])
		row.TS, _ = strconv.ParseInt(f[5], 10, 64)
		next[at] = &row
	}
	return &Table{Version: table.version() + 1, Rows: next}
}

func applyKPI(set *KPISet, values []int) *KPISet {
	if set == nil {
		return &KPISet{Version: 1, Values: values, Prev: values}
	}
	spark := make([][]int, len(set.Spark))
	for i := range set.Spark {
		history := set.Spark[i]
		v := 0
		if i < len(values) {
			v = values[i]
		}
		grown := make([]int, 0, min(len(history)+1, SparkPoints))
		start := 0
		if len(history)+1 > SparkPoints {
			start = len(history) + 1 - SparkPoints
		}
		grown = append(grown, history[start:]...)
		grown = append(grown, v)
		spark[i] = grown
	}
	return &KPISet{
		Version: set.Version + 1,
		Labels:  set.Labels,
		Values:  values,
		Prev:    set.Values,
		Spark:   spark,
	}
}

func applySeries(set *SeriesSet, values []int) *SeriesSet {
	if set == nil {
		return nil
	}
	next := SeriesSet{Version: set.Version + 1}
	for i := 0; i < 2; i++ {
		// "shift-one-point": two new points in, two dropped.
		history := set.Points[i]
		v := 0
		if i < len(values) {
			v = values[i]
		}
		start := 0
		if len(history)+1 > SeriesPoints {
			start = len(history) + 1 - SeriesPoints
		}
		grown := make([]int, 0, SeriesPoints)
		grown = append(grown, history[start:]...)
		grown = append(grown, v)
		next.Points[i] = grown
	}
	return &next
}

func applyLog(log *EventLog, entry LogEntry) *EventLog {
	base := []LogEntry(nil)
	version := uint64(0)
	if log != nil {
		base, version = log.Entries, log.Version
	}
	keep := min(len(base), LogCap-1)
	next := make([]LogEntry, 0, keep+1)
	next = append(next, base[len(base)-keep:]...)
	next = append(next, entry)
	return &EventLog{Version: version + 1, Entries: next}
}

func parseInts(raw string) []int {
	parts := strings.Split(raw, ",")
	out := make([]int, 0, len(parts))
	for _, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil {
			return nil
		}
		out = append(out, n)
	}
	return out
}

/* --------------------------------------------------------------- config --- */

// Config builds the live application over the shared feed.
func Config(feed *Feed, origins []string) live.Config[State, live.AnonymousIdentity] {
	return live.Config[State, live.AnonymousIdentity]{
		// Init runs once per connection. It joins the feed — which both reads the
		// current frame and registers this session for pushes, under one lock, so
		// no tick can slip through the gap between the two — and asks for the
		// subscription pump.
		//
		// The bench session id arrives through the context derived from the
		// upgrade request (api-surface §3 deliberately omits Session.Request()).
		// Binding it here is what keeps region E's panel entry alive for as long
		// as this tab holds a connection, which is the rule the Next.js store
		// applies to its own session map.
		Init: func(ctx context.Context, s live.Session[live.AnonymousIdentity]) (State, []live.Effect[live.AnonymousIdentity], error) {
			sid := SIDFromContext(ctx)
			snap := feed.Join(s.ID(), sid)
			return State{
				Self:     s.ID(),
				SID:      sid,
				Shown:    snap,
				Live:     snap,
				Controls: DefaultControls,
				NowMs:    time.Now().UnixMilli(),
			}, []live.Effect[live.AnonymousIdentity]{feed.SubscribeEffect()}, nil
		},

		Reduce: Reducer(feed),

		// Five fragments, patched independently, which is half of what FR-62 asks
		// a live dashboard to demonstrate. One fragment covering the page would
		// re-render 200 rows every time the event log appended, five times a
		// second.
		Fragments: []live.Fragment[State]{
			{
				ID:     FragmentKPIs,
				Render: func(s State) templ.Component { return KPIRegion(s) },
				Dirty:  func(prev, next State) bool { return prev.Shown.KPIs != next.Shown.KPIs },
			},
			{
				ID:     FragmentTable,
				Render: func(s State) templ.Component { return TableRegion(s, s.TableView()) },
				Dirty: func(prev, next State) bool {
					return prev.Shown.Table != next.Shown.Table ||
						prev.Shown.Tick != next.Shown.Tick ||
						prev.Controls != next.Controls
				},
			},
			{
				ID:     FragmentSeries,
				Render: func(s State) templ.Component { return SeriesRegion(s) },
				Dirty:  func(prev, next State) bool { return prev.Shown.Series != next.Shown.Series },
			},
			{
				ID:     FragmentLog,
				Render: func(s State) templ.Component { return LogRegion(s) },
				Dirty:  func(prev, next State) bool { return prev.Shown.Log != next.Shown.Log },
			},
			{
				ID:     FragmentControls,
				Render: func(s State) templ.Component { return ControlsRegion(s) },
				Dirty:  func(prev, next State) bool { return prev.Controls != next.Controls },
			},
		},

		Events: []string{EventFilter, EventSearch, EventSort, EventPerPage, EventPause},

		Teardown: func(_ context.Context, s live.Session[live.AnonymousIdentity], state State) {
			feed.Leave(s.ID(), state.SID)
		},

		// A real allowlist, not live.AnyOrigin. PRODUCTION replaces it with the
		// one scheme-and-host the page is served from.
		Origins: origins,

		// A read-only operator dashboard has no accounts, so there is no identity
		// to derive and no per-event rule to apply — the same position
		// examples/dashboard takes, and the reason its package comment points at
		// examples/chat for the identity story. PRODUCTION behind an SSO proxy
		// replaces Anonymous with the trusted header or cookie and AllowAll with
		// the check that says who may pause whose dashboard.
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],

		// live.NoCSRFCheck is safe here ONLY because Origins above is a real
		// allowlist: the origin check is then the whole of the CSRF posture.
		CSRF: live.NoCSRFCheck,
	}
}
