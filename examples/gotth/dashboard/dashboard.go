package main

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The fragment identifiers. They are constants because a fragment ID is a
// contract in three places at once — the Config, the markup's
// data-gotth-region attribute, and every patch frame on the wire — and a typo
// in any one of them is a region that silently stops updating.
//
// There are three, and the split is half the reason this example exists. The
// meters move on every sample, twenty times a second. The alerts move only when
// a series crosses a threshold, which is minutes apart. The controls belong to
// the person at this keyboard and move only when they act or when the library
// tells this session it is falling behind. One fragment covering all three
// would repaint the alert log and the control panel twenty times a second to
// deliver a number that changed in one of them — which is the cost FR-62's
// "multiple independent live regions" is asking to be shown, and the property
// wire_test.go asserts on the frames rather than on the declarations.
const (
	FragmentMeters   = "dashboard.meters"
	FragmentAlerts   = "dashboard.alerts"
	FragmentControls = "dashboard.controls"
)

// The event names a browser may send.
//
// Config.Events is default-deny: a name that is not registered there is refused
// with UNKNOWN_EVENT before the reducer runs. One name per operation rather
// than one name carrying a "kind" field, for the reason the counter gives — an
// allowlist of four names bounds what a hostile client can ask for, where one
// name and a discriminator field bounds nothing.
const (
	EventProbe  = "dashboard.probe"
	EventPause  = "dashboard.pause"
	EventResume = "dashboard.resume"
	EventClear  = "dashboard.clear"
)

// MaxWindow is how many recent readings a session keeps for the sparkline.
//
// It is small on purpose: this is the one unbounded-looking thing in the state,
// and a live dashboard that accumulated every sample it had ever received would
// grow without limit at twenty samples a second per session — which is the
// memory failure a resilience example should not be demonstrating by accident.
const MaxWindow = 30

// State is one browser tab's view of the dashboard.
//
// The split between what is shared and what is this session's alone is the same
// one the chat example draws, and for the same reason. Meters and Alerts came
// from the feed and every session holds the same values. Paused, Health and
// Notice are this tab's and nobody else's.
type State struct {
	// Self identifies this session. It is here so a render could tell this
	// tab's own probe from another tab's; nothing renders it today.
	Self live.ID

	// Meters is the latest reading, and Window is the recent history behind
	// it. Both are replaced wholesale rather than appended to in place: a
	// reducer must not mutate the state it was given, which is what makes
	// panic recovery free, and an immutable value replaced wholesale makes
	// breaking that rule by accident impossible.
	Meters *Reading
	Window *History

	// Alerts is the shared alert log as of the last update this session folded
	// in.
	Alerts *AlertLog

	// Paused stops this session applying samples. It is per-session and not
	// per-feed: pausing your own dashboard must not stop everybody else's, and
	// the feed keeps producing either way — which is what makes the resume
	// show the CURRENT reading rather than a replay of the ones missed.
	Paused bool

	// Degraded is set when the library told this session its outbound window
	// filled, and cleared when it drained. It is the application half of
	// FR-51: the library's policy is to stop emitting and then to evict, and
	// this is the application deciding what the person should be shown about
	// it.
	Degraded bool

	// Notice is the last thing that went wrong for this session — a failed
	// effect, a subscription that died.
	Notice string
}

// History is the recent readings, oldest first. Immutable and held behind a
// pointer, for the reason Reading's doc comment gives: a state type that is not
// comparable is reported as changed on every transition.
type History struct {
	Values []int
}

func (h *History) values() []int {
	if h == nil {
		return nil
	}
	return h.Values
}

// with returns the history that results from one new headline value, trimmed to
// MaxWindow. The receiver is not touched.
func (h *History) with(v int) *History {
	base := h.values()
	keep := min(len(base), MaxWindow-1)

	next := make([]int, 0, keep+1)
	next = append(next, base[len(base)-keep:]...)
	next = append(next, v)
	return &History{Values: next}
}

// Readings, Entries and the rest read the shared values without assuming there
// are any. The zero State renders — every spec that builds a State by hand
// depends on that, and so does the page a browser gets before the first sample.
func (s State) Reading(series string) int {
	v, _ := s.Meters.Value(series)
	return v
}

// Level is the CSS class for a reading, so the template holds no thresholds of
// its own.
func (s State) Level(series string) string {
	switch v := s.Reading(series); {
	case v > AlertAbove:
		return "over"
	case v > 70:
		return "high"
	default:
		return "ok"
	}
}

// BarClass buckets a reading into one of eleven CSS classes, w0 to w10.
//
// A class rather than an inline style attribute, and not for taste: an inline
// style would need a CSP hash or 'unsafe-inline' to survive FR-49's
// `script-src 'self'; object-src 'none'` posture the moment anybody tightened
// style-src too, and a bucketed class is a fixed vocabulary the stylesheet
// already knows.
func (s State) BarClass(series string) string {
	return "w" + strconv.Itoa(clamp(s.Reading(series)/10, 0, 10))
}

// SampleSeq is the sample number the meters are showing. It renders, which is
// what keeps two consecutive readings with identical values from producing
// byte-identical HTML and a suppressed patch — see Reading.Seq.
func (s State) SampleSeq() uint64 {
	if s.Meters == nil {
		return 0
	}
	return s.Meters.Seq
}

// SampleClock renders the sample's timestamp. It formats a stamp the feed took
// and reads no clock of its own, for the reason Alert.Clock gives.
func (s State) SampleClock() string {
	if s.Meters == nil || s.Meters.AtUnixMilli == 0 {
		return "--:--:--"
	}
	return time.UnixMilli(s.Meters.AtUnixMilli).UTC().Format("15:04:05")
}

// Sparkline renders the recent history as one string of block characters. It is
// a pure function of Window and it is the cheapest honest way to show that the
// history is real rather than decorative.
func (s State) Sparkline() string {
	const blocks = " ▁▂▃▄▅▆▇█"
	runes := []rune(blocks)
	var b strings.Builder
	for _, v := range s.Window.values() {
		i := v * (len(runes) - 1) / 100
		b.WriteRune(runes[clamp(i, 0, len(runes)-1)])
	}
	return b.String()
}

// Alerts, AlertCount and Quiet read the shared alert log.
func (s State) AlertEntries() []Alert { return s.Alerts.entries() }

func (s State) AlertCount() string { return strconv.Itoa(len(s.AlertEntries())) }

func (s State) AlertVersion() uint64 {
	if s.Alerts == nil {
		return 0
	}
	return s.Alerts.Version
}

// FeedLabel is what the controls region says the feed is doing for this
// session.
func (s State) FeedLabel() string {
	if s.Paused {
		return "paused"
	}
	return "live"
}

// StatusLabel is what the controls region says about backpressure. It is the
// user-facing half of FR-51's defined degradation.
func (s State) StatusLabel() string {
	if s.Degraded {
		return "falling behind: updates are being held until this browser catches up"
	}
	return "keeping up"
}

// Reducer returns the pure state transition, bound to the feed its effects act
// on.
//
// It is a constructor because a live.Effect[live.AnonymousIdentity] carries its own behaviour since the
// 2026-09-03 ruling, so a reducer scheduling one has to hold what that effect
// closes over.
//
// It reads no clock, performs no I/O, and reaches the feed not at all. Asking
// for a reading does not take one: it returns the feed's probe effect, the
// library performs it
// at the actor boundary, the feed samples and broadcasts, and this session
// learns the result the same way every other session does — through an event
// the feed pushed. That is what makes two tabs unable to disagree about what
// the server measured.
func Reducer(feed *Feed) live.Reducer[State, live.AnonymousIdentity] {
	return func(state State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
		switch ev.Name {
		case EventPause:
			state.Paused = true
			return state, nil

		case EventResume:
			state.Paused = false
			return state, nil

		case EventProbe:
			// The event identifier rides into the effect and back out of the feed
			// on the emitted event's contributing list. Without it the patch that
			// finally shows the reading names only "effect:dashboard.subscribe",
			// and an operator holding that frame cannot reach the click.
			return state, []live.Effect[live.AnonymousIdentity]{feed.ProbeEffect(ev.ID)}

		case EventClear:
			return state, []live.Effect[live.AnonymousIdentity]{feed.ClearEffect(ev.ID)}

		case EventSample:
			return applySample(state, ev), nil

		case EventAlert:
			return applyAlert(state, ev), nil

		case EventCleared:
			return applyCleared(state, ev), nil

		case live.SlowClientEvent:
			// FR-51's degradation, as the application sees it. The library
			// synthesizes this into the session's own mailbox when the outbound
			// window fills, and it is named by an exported constant rather than
			// spelled out here — which it was, until FRICTION.md F-1 argued that
			// every application implementing a defined degradation should not have
			// to copy the library's private vocabulary and get it right by luck.
			//
			// The library has already stopped emitting by the time this arrives, so
			// the notice this sets will reach the browser only once the window
			// re-opens — which is the honest behaviour and is asserted as such in
			// wire_test.go.
			state.Degraded = true
			return state, nil

		case live.ClientRecoveredEvent:
			state.Degraded = false
			return state, nil

		case live.EffectFailedEvent:
			return applyFailure(feed, state, ev)
		}

		// An unregistered name cannot reach here from a browser — the library
		// refuses one before the reducer runs — so anything arriving here is
		// something the library synthesised that this application has no answer
		// for. Ignoring it is correct.
		return state, nil
	}
}

// applySample folds one reading.
//
// A paused session drops it, and drops it WITHOUT changing state, which is what
// makes pausing free: the state is unchanged, so the library's own comparison
// finds no transition, no fragment is asked whether it is dirty, and no patch
// is built. A paused dashboard costs a reducer call per sample and nothing
// else.
func applySample(state State, ev live.Event) State {
	if state.Paused {
		return state
	}
	seq, err := strconv.ParseUint(ev.Fields.Get(fieldSeq), 10, 64)
	if err != nil {
		return state
	}
	// A reading this session has already passed is dropped. Emitted events are
	// best-effort — the library tells an effect when a mailbox is full rather
	// than letting the event vanish — and this is the line that keeps a
	// duplicate or a late delivery from moving the meters backwards.
	if state.Meters != nil && seq <= state.Meters.Seq {
		return state
	}
	at, err := strconv.ParseInt(ev.Fields.Get(fieldAtMilli), 10, 64)
	if err != nil {
		return state
	}

	values := make([]int, 0, len(Series))
	for _, name := range Series {
		raw, ok := ev.Fields.Lookup(name)
		if !ok {
			return state
		}
		v, err := strconv.Atoi(raw)
		if err != nil {
			return state
		}
		values = append(values, v)
	}

	state.Meters = &Reading{Seq: seq, AtUnixMilli: at, Values: values}
	state.Window = state.Window.with(values[0])
	return state
}

// applyAlert folds one threshold crossing. It touches Alerts and nothing else,
// which is what makes the alert region's Dirty declaration true.
func applyAlert(state State, ev live.Event) State {
	version, ok := newerVersion(state, ev)
	if !ok {
		return state
	}
	seq, err := strconv.ParseUint(ev.Fields.Get(fieldSeq), 10, 64)
	if err != nil {
		return state
	}
	at, err := strconv.ParseInt(ev.Fields.Get(fieldAtMilli), 10, 64)
	if err != nil {
		return state
	}
	value, err := strconv.Atoi(ev.Fields.Get(fieldValue))
	if err != nil {
		return state
	}
	series := ev.Fields.Get(fieldSeries)
	if series == "" {
		return state
	}

	state.Alerts = state.Alerts.with(
		Alert{Seq: seq, Series: series, Value: value, AtUnixMilli: at}, version)
	return state
}

// applyCleared folds somebody clearing the shared alert log.
func applyCleared(state State, ev live.Event) State {
	version, ok := newerVersion(state, ev)
	if !ok {
		return state
	}
	state.Alerts = &AlertLog{Version: version}
	return state
}

// applyFailure decides what to do about an effect that failed or panicked.
//
// Note what reaches the browser and what does not. EffectFailedSourceField is a
// name this application chose — "dashboard.subscribe" — so it is safe to
// render. EffectFailedErrorField is NOT: it carries the error's own message, or
// a raw panic value, unredacted and in production, ungated by Config.Dev. It is
// read here only for the classification beside it.
//
// The retry is the library's claim rather than this reducer's guess: an
// unreadable or absent classification parses as false and nothing is retried.
// The one failure worth retrying is a dead subscription, because a session
// without one keeps rendering the last reading it saw and stops learning
// anything — a dashboard that looks right while being wrong, which is the worst
// failure a dashboard has.
func applyFailure(feed *Feed, state State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
	source := ev.Fields.Get(live.EffectFailedSourceField)
	state.Notice = "the server could not complete an operation: " + source

	retryable, _ := strconv.ParseBool(ev.Fields.Get(live.EffectFailedRetryableField))
	if retryable && source == SourceSubscribe {
		return state, []live.Effect[live.AnonymousIdentity]{feed.SubscribeEffect()}
	}
	return state, nil
}

// newerVersion reads the feed revision off an event and reports whether it is
// newer than the one this session holds.
func newerVersion(state State, ev live.Event) (uint64, bool) {
	version, err := strconv.ParseUint(ev.Fields.Get(fieldVersion), 10, 64)
	if err != nil || version <= state.AlertVersion() {
		return 0, false
	}
	return version, true
}

// Config builds the live application over a feed.
//
// Everything security-relevant is set here and nothing is left to a default,
// because live.New refuses a Config with a hole in it rather than starting with
// one.
func Config(feed *Feed, origins []string) live.Config[State, live.AnonymousIdentity] {
	return live.Config[State, live.AnonymousIdentity]{
		// Init is the mount hook, and it is where FR-56's subscribe-on-mount
		// happens. Join registers this session for pushes and reads the feed
		// under one lock — split in two, a sample landing between them is
		// either shown twice or missed entirely, and the window is exactly as
		// wide as a page load.
		Init: func(ctx context.Context, s live.Session[live.AnonymousIdentity]) (State, []live.Effect[live.AnonymousIdentity], error) {
			reading, alerts := feed.Join(s.ID())
			state := State{Self: s.ID(), Meters: reading, Alerts: alerts}
			if len(reading.Values) > 0 {
				state.Window = (&History{}).with(reading.Values[0])
			}
			return state, []live.Effect[live.AnonymousIdentity]{feed.SubscribeEffect()}, nil
		},

		Reduce: Reducer(feed),

		Fragments: []live.Fragment[State]{
			{
				ID:     FragmentMeters,
				Render: func(s State) templ.Component { return MetersRegion(s) },
				// Pointer comparisons, because the values behind them are
				// immutable and replaced wholesale. This is the declaration
				// FR-62's second property rests on: a sample moves Meters and
				// Window, so this returns true and the two declarations below
				// return false, and the patch on the wire carries this
				// fragment alone.
				Dirty: func(prev, next State) bool {
					return prev.Meters != next.Meters || prev.Window != next.Window
				},
			},
			{
				ID:     FragmentAlerts,
				Render: func(s State) templ.Component { return AlertsRegion(s) },
				Dirty: func(prev, next State) bool {
					return prev.Alerts != next.Alerts
				},
			},
			{
				ID:     FragmentControls,
				Render: func(s State) templ.Component { return ControlsRegion(s) },
				// This names only what belongs to this session and says
				// nothing about the feed, which is why twenty samples a second
				// do not repaint the control panel. Widening it to include the
				// reading would be legal, would pass
				// livetest.AssertDirtyComplete, and would cost a re-render of
				// this region on every sample — which is the thing FR-62 asks
				// an example to demonstrate is avoidable.
				Dirty: func(prev, next State) bool {
					return prev.Paused != next.Paused ||
						prev.Degraded != next.Degraded ||
						prev.Notice != next.Notice
				},
			},
		},

		// The allowlist. The three names the feed emits are absent on purpose;
		// see their doc comment. So are the two the library synthesizes: a
		// browser that could send timer:slow_client could tell its own session
		// it was degraded, which is a claim only the transport is in a position
		// to make.
		Events: []string{EventProbe, EventPause, EventResume, EventClear},

		Teardown: func(_ context.Context, s live.Session[live.AnonymousIdentity], _ State) { feed.Leave(s.ID()) },

		// A real allowlist, not live.AnyOrigin. main.go derives it from the
		// listen address; production lists the scheme and host the app is
		// actually served from, and nothing else.
		Origins: origins,

		// Three escape hatches, each named so that
		// `grep -rn 'live\.Anonymous\|live\.AllowAll\|live\.NoCSRFCheck'`
		// finds every one of them.
		//
		// A read-only operations dashboard demo has no accounts, so there is
		// no identity to derive and no per-event rule to apply. That is a
		// property of this example and not of the library: examples/chat binds
		// a session to a member at the handshake and authorizes every event
		// against it, and a real dashboard behind an SSO proxy would do the
		// same. NoCSRFCheck is only safe because Origins above is a real
		// allowlist, which is the library's own stated condition.
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
	}
}
