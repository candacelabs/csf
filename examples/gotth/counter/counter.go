package main

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/a-h/templ"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The fragment identifiers. They are constants because a fragment ID is a
// contract in three places at once — the Config, the markup's
// data-gotth-region attribute, and every patch frame on the wire — and a typo
// in any one of them is a region that silently stops updating.
const (
	FragmentValue    = "counter.value"
	FragmentControls = "counter.controls"
)

// The event names the browser may send.
//
// One name per operation, rather than one name carrying a "delta" field, and
// the reason is security rather than taste. Config.Events is default-deny:
// a name that is not here is refused with UNKNOWN_EVENT before the reducer
// runs. With four names the allowlist bounds what a hostile client can ask
// for. With one name and a numeric field it bounds nothing, and the reducer
// would have to re-validate a number the browser chose.
const (
	EventIncrement   = "counter.increment"
	EventDecrement   = "counter.decrement"
	EventIncrement10 = "counter.increment10"
	EventReset       = "counter.reset"
)

// EventSync is the event the store emits into every subscribed session when
// the shared value changes.
//
// It is deliberately NOT in Config.Events. Registration is what makes a name
// sendable by a browser, and a client that could send counter.sync could
// declare the counter to be any value it liked. Events an effect emits do not
// pass through that check, because they never came from the wire.
const EventSync = "counter.sync"

// The field names on a sync event. A sync carries the whole snapshot rather
// than a delta: a delta that is dropped under backpressure leaves a session
// permanently wrong, where a dropped snapshot is repaired by the next one.
const (
	fieldValue     = "value"
	fieldVersion   = "version"
	fieldTabs      = "tabs"
	fieldChangedAt = "changed_at_ms"
	fieldChangedBy = "changed_by"
)

// State is one browser tab's view of the shared counter.
//
// Every field is either the value the server owns or something derived from
// it. Nothing here is client state: a reload throws this away and rebuilds it
// from the store, which is what makes the count survive one (F-CTR-4).
type State struct {
	// Self identifies this session, so a render can tell "you changed it"
	// from "another tab changed it".
	Self live.ID

	// Value is the shared counter, as of the last snapshot this session saw.
	Value int64

	// Version is the store revision Value came from. A sync carrying an older
	// revision is ignored, which is how out-of-order delivery repairs itself.
	Version uint64

	// Tabs is how many sessions currently share this counter.
	Tabs int

	// ChangedBy is the session that made the last change.
	ChangedBy live.ID

	// ChangedAtUnixMilli is when the value last changed, as the store stamped
	// it.
	//
	// It is an int64 rather than a time.Time because the Dirty functions below
	// compare state with ==, and a time.Time carries a monotonic reading that
	// makes == mean something subtler than it looks.
	ChangedAtUnixMilli int64

	// Age is how long ago that was, as of the transition being rendered.
	//
	// A render may not read a clock — it must be a pure function of state, or
	// two runs of the same state produce different bytes and the patch
	// suppression that depends on that comparison breaks. So the relative
	// timestamp F-CTR-7 asks for is computed here, at the transition, from the
	// event's own At stamp, and rendered as data.
	Age time.Duration
}

// Reducer returns the pure state transition, bound to the store its effects
// act on.
//
// It is a constructor rather than a package-level function, and that is what
// changed when live.Effect[live.AnonymousIdentity] stopped being an interface on 2026-09-03. An effect
// carries its own behaviour now, so the reducer that schedules one has to be
// able to build it — which means holding the store the effect closes over. The
// reducer still touches that store not at all: it builds a value describing
// what to do and the library performs it later, off this goroutine.
//
// The transition itself reads no clock and performs no I/O. A click does not
// change Value: it returns the store's change effect, and this session learns
// the result the same way every other tab does, through a sync event. That is
// what "server-authoritative" means concretely, and it is why two tabs cannot
// disagree.
func Reducer(store *Store) live.Reducer[State, live.AnonymousIdentity] {
	return func(state State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
		// Every transition refreshes the relative timestamp, so the "changed
		// 4s ago" line does not go stale between changes.
		state.Age = ageAt(state.ChangedAtUnixMilli, ev.At)

		switch ev.Name {
		case EventIncrement:
			return state, []live.Effect[live.AnonymousIdentity]{store.ChangeEffect(Change{Op: OpAdd, Delta: 1, By: state.Self, Cause: ev.ID})}
		case EventDecrement:
			return state, []live.Effect[live.AnonymousIdentity]{store.ChangeEffect(Change{Op: OpAdd, Delta: -1, By: state.Self, Cause: ev.ID})}
		case EventIncrement10:
			return state, []live.Effect[live.AnonymousIdentity]{store.ChangeEffect(Change{Op: OpAdd, Delta: 10, By: state.Self, Cause: ev.ID})}
		case EventReset:
			return state, []live.Effect[live.AnonymousIdentity]{store.ChangeEffect(Change{Op: OpReset, By: state.Self, Cause: ev.ID})}
		case EventSync:
			return applySync(state, ev), nil
		case live.EffectFailedEvent:
			return state, retryWatch(store, ev)
		}

		// An unknown name cannot reach here from a browser — the library
		// refuses unregistered names before the reducer runs — so anything
		// arriving here is something the library synthesised and this
		// application has no answer for. Ignoring it is correct.
		return state, nil
	}
}

// retryWatch decides what to do about a failed effect.
//
// The only failure this application can act on is a dead subscription. Without
// one the tab keeps rendering the last value it saw and stops learning about
// anybody else's changes — it looks right while being wrong, which is the
// failure worth writing code for, where a failed change effect is visible
// immediately because the number does not move.
//
// It re-subscribes only when the library says the failure was transient.
// Re-running a terminal failure re-runs whatever made it terminal, and the
// classification is the executor's claim rather than this reducer's guess: an
// unreadable or absent value parses as false and nothing is retried.
func retryWatch(store *Store, ev live.Event) []live.Effect[live.AnonymousIdentity] {
	retryable, _ := strconv.ParseBool(ev.Fields.Get(live.EffectFailedRetryableField))
	if retryable && ev.Fields.Get(live.EffectFailedSourceField) == SourceWatch {
		return []live.Effect[live.AnonymousIdentity]{store.WatchEffect()}
	}
	return nil
}

// applySync folds a store snapshot into this session's view.
//
// A snapshot older than the one already held is dropped. Emitter delivery is
// best-effort — a full mailbox drops the event and tells the effect so — and
// this is the line that makes that harmless: the counter converges on the
// newest snapshot it has seen and the next change repairs any gap.
func applySync(state State, ev live.Event) State {
	version, err := strconv.ParseUint(ev.Fields.Get(fieldVersion), 10, 64)
	if err != nil || version < state.Version {
		return state
	}

	value, err := strconv.ParseInt(ev.Fields.Get(fieldValue), 10, 64)
	if err != nil {
		return state
	}
	tabs, err := strconv.Atoi(ev.Fields.Get(fieldTabs))
	if err != nil {
		return state
	}
	changedAt, err := strconv.ParseInt(ev.Fields.Get(fieldChangedAt), 10, 64)
	if err != nil {
		return state
	}

	state.Value = value
	state.Version = version
	state.Tabs = tabs
	state.ChangedAtUnixMilli = changedAt
	state.ChangedBy = parseID(ev.Fields.Get(fieldChangedBy))
	state.Age = ageAt(changedAt, ev.At)
	return state
}

// ageAt is the relative-timestamp arithmetic, kept out of the reducer body so
// that the one impure-looking thing in this file — a duration — is visibly a
// function of two values the transition was given.
func ageAt(changedAtUnixMilli int64, at time.Time) time.Duration {
	if changedAtUnixMilli == 0 || at.IsZero() {
		return 0
	}
	d := time.Duration(at.UnixMilli()-changedAtUnixMilli) * time.Millisecond
	if d < 0 {
		return 0
	}
	return d
}

// Parity is the derived label F-CTR-3 asks for: a render that is more than one
// text node, so a morph has real work to do.
func (s State) Parity() string {
	if s.Value%2 == 0 {
		return "even"
	}
	return "odd"
}

// Band is the status badge's class, which changes at thresholds rather than on
// every value, so the badge is a second thing the morph must get right.
func (s State) Band() string {
	switch {
	case s.Value < 0:
		return "negative"
	case s.Value == 0:
		return "zero"
	case s.Value < 10:
		return "low"
	default:
		return "high"
	}
}

// AgeLabel renders Age the way a person reads it. It is a method on state
// rather than a template expression so that the value fragment's Dirty
// function can compare exactly what the markup shows.
func (s State) AgeLabel() string {
	switch {
	case s.ChangedAtUnixMilli == 0:
		return "never"
	case s.Age < 2*time.Second:
		return "just now"
	case s.Age < time.Minute:
		return fmt.Sprintf("%ds ago", int(s.Age.Seconds()))
	case s.Age < time.Hour:
		return fmt.Sprintf("%dm ago", int(s.Age.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(s.Age.Hours()))
	}
}

// Author says who made the last change, from this tab's point of view. It is
// the cheapest way to see, in a second tab, that the value arrived over the
// server's push channel rather than from anything local (F-CTR-5).
func (s State) Author() string {
	switch {
	case s.ChangedBy == (live.ID{}):
		return "nobody yet"
	case s.ChangedBy == s.Self:
		return "this tab"
	default:
		return "another tab"
	}
}

// TabLabel pluralises the shared-session count.
func (s State) TabLabel() string {
	if s.Tabs == 1 {
		return "1 tab"
	}
	return strconv.Itoa(s.Tabs) + " tabs"
}

// Value renders the number itself, for the template and for the bench
// harness's paint predicate.
func (s State) ValueText() string { return strconv.FormatInt(s.Value, 10) }

// Config builds the live application over a store.
//
// Everything security-relevant is set here and nothing is left to a default,
// because live.New refuses a Config with a hole in it rather than starting
// with one.
func Config(store *Store, origins []string) live.Config[State, live.AnonymousIdentity] {
	return live.Config[State, live.AnonymousIdentity]{
		// Init runs once per connection, before the first snapshot. It joins
		// the store — which both reads the current value and registers this
		// session for pushes, under one lock, so no change can slip through
		// the gap between the two — and asks for the subscription pump.
		Init: func(ctx context.Context, s live.Session[live.AnonymousIdentity]) (State, []live.Effect[live.AnonymousIdentity], error) {
			snap := store.Join(s.ID())
			return State{
				Self:               s.ID(),
				Value:              snap.Value,
				Version:            snap.Version,
				Tabs:               snap.Tabs,
				ChangedBy:          snap.ChangedBy,
				ChangedAtUnixMilli: snap.ChangedAtUnixMilli,
				Age:                ageAt(snap.ChangedAtUnixMilli, time.Now()),
			}, []live.Effect[live.AnonymousIdentity]{store.WatchEffect()}, nil
		},

		Reduce: Reducer(store),

		Fragments: []live.Fragment[State]{
			{
				ID:     FragmentValue,
				Render: func(s State) templ.Component { return ValueRegion(s) },
				// Everything ValueRegion renders, and nothing else. Widening
				// this is free — a render whose bytes did not move is
				// suppressed — and narrowing it is the one mistake that
				// produces a stale region in production and nothing at all in
				// development. livetest.AssertDirtyComplete is what holds it.
				Dirty: func(prev, next State) bool {
					return prev.Value != next.Value || prev.AgeLabel() != next.AgeLabel()
				},
			},
			{
				ID:     FragmentControls,
				Render: func(s State) templ.Component { return ControlsRegion(s) },
				Dirty: func(prev, next State) bool {
					return prev.Tabs != next.Tabs || prev.Author() != next.Author()
				},
			},
		},

		// The allowlist. EventSync is absent on purpose; see its doc comment.
		Events: []string{EventIncrement, EventDecrement, EventIncrement10, EventReset},

		Teardown: func(_ context.Context, s live.Session[live.AnonymousIdentity], _ State) { store.Leave(s.ID()) },

		// A real allowlist, not live.AnyOrigin. main.go derives it from the
		// listen address; production lists the scheme and host the app is
		// actually served from, and nothing else.
		Origins: origins,

		// Three escape hatches, each because this application genuinely has no
		// answer to give, and each named so that `grep -rn 'live\.Anonymous\|
		// live\.AllowAll\|live\.NoCSRFCheck'` finds every one of them.
		//
		// A counter demo has no accounts, so there is no identity to derive
		// and no per-event rule to apply. Production replaces Anonymous with
		// the session cookie or bearer token it already trusts, and AllowAll
		// with the check that says which identities may change what.
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],

		// NoCSRFCheck is only safe because Origins above is a real allowlist:
		// the origin check is then the whole of the CSRF posture, which is
		// exactly the condition the library's own doc comment states.
		// Production that authenticates with a cookie adds a token bound to
		// the application session here.
		CSRF: live.NoCSRFCheck,
	}
}

// parseID reads a session identifier back out of a sync field.
func parseID(hex string) live.ID {
	var id live.ID
	if len(hex) != 2*len(id) {
		return id
	}
	for i := range id {
		hi, ok := hexDigit(hex[2*i])
		if !ok {
			return live.ID{}
		}
		lo, ok := hexDigit(hex[2*i+1])
		if !ok {
			return live.ID{}
		}
		id[i] = hi<<4 | lo
	}
	return id
}

func hexDigit(c byte) (byte, bool) {
	switch {
	case c >= '0' && c <= '9':
		return c - '0', true
	case c >= 'a' && c <= 'f':
		return c - 'a' + 10, true
	default:
		return 0, false
	}
}
