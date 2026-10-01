package main

import (
	"context"
	"fmt"
	"math/rand/v2"
	"slices"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The effect sources. They name the effect in provenance and in metrics, so
// they are the strings an operator greps for — and, because a failure event
// reports the source of the effect that failed, the strings the reducer
// matches on. Every server-initiated patch in this application carries one of
// them as "effect:<source>", which is FR-42's requirement in practice.
const (
	SourceSubscribe = "dashboard.subscribe"
	SourceProbe     = "dashboard.probe"
	SourceClear     = "dashboard.clear"
)

// retryDelay is how long the subscription pump waits before re-offering an
// update the session could not accept, and maxRefusals is how many refusals in
// a row it absorbs before handing the decision to the reducer.
//
// Both are the same shape examples/chat and examples/counter arrived at
// independently, and this is the third module to write them. FRICTION.md item
// F-4; chat's F-5 is the same observation with two data points instead of
// three.
const (
	retryDelay  = 20 * time.Millisecond
	maxRefusals = 50
)

// backlogDepth is how many feed updates one session may be behind before the
// feed stops keeping them.
//
// A real dashboard would very likely use the counter's latest-value-wins slot
// here: a gauge reading is absolute, so an undelivered one carries nothing the
// next one lacks. This one uses a real queue instead, deliberately, and the
// reason is the property this example exists to demonstrate. Collapsing
// samples in the application layer would do the batching that FR-62 asks the
// LIBRARY to do, and it would drop the causal edge a probe carries before the
// library ever saw it — so the coalescing this example measures would be
// partly this file's, and the provenance assertion in wire_test.go would be
// asserting against a queue that had already thrown the evidence away.
//
// The depth is generous for the same reason: it must not be the thing that
// bounds a slow session, because the outbound window is what FR-51 makes that
// claim about and a spec cannot tell the two apart if both are tight.
const backlogDepth = 4096

// SubscribeEffect is the effect that pushes this session every feed update
// until the session ends.
//
// It exists because an effect's Run is the only place an application is handed
// a live.Emitter, so a subscription that wants to inject events has to be
// expressed as a long-running effect. Config.Init registers the session; this
// pumps what the registration collects. It captures nothing: a subscription's
// address is the session it belongs to, which the library already knows and
// hands to Run.
//
// Every patch the feed causes in this session carries
// "effect:dashboard.subscribe" as its origin, which is the string that
// distinguishes a server-initiated repaint from one this browser asked for.
func (f *Feed) SubscribeEffect() live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: SourceSubscribe,
		Run: func(ctx context.Context, sess live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			return f.pump(ctx, sess.ID(), emit)
		},
	}
}

// ProbeEffect is the effect that asks the feed for one extra reading, right
// now.
//
// It is the one client interaction that produces a server-initiated patch
// through the shared feed, and it is here because that fan-out is where
// provenance gets interesting: the patch that finally shows the reading is
// emitted by the SUBSCRIPTION, which was scheduled at mount, so without an
// explicit edge the only thing an operator could recover from the frame is
// "some effect did it". cause is the identifier of the event that asked, it
// rides through the feed, and it comes back out on the emitted event's
// Contributing list for the session that asked and for nobody else.
func (f *Feed) ProbeEffect(cause uint64) live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: SourceProbe,
		Run: func(ctx context.Context, sess live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			f.Sample(sess.ID(), cause)
			return nil
		},
	}
}

// ClearEffect is the effect that empties the shared alert log.
func (f *Feed) ClearEffect(cause uint64) live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: SourceClear,
		Run: func(ctx context.Context, sess live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			f.Clear(sess.ID(), cause)
			return nil
		},
	}
}

// Series are the metrics this dashboard shows, in the order they render.
//
// A slice rather than a map, and iterated rather than ranged over a map
// anywhere: a fragment's render must be a pure function of state and produce
// byte-identical HTML for the same state, and Go's map iteration order is the
// standard way to break that without noticing.
var Series = []string{"cpu", "memory", "requests"}

// AlertAbove is the reading at which a series raises an alert. It is crossed
// rather than exceeded: an alert is raised when a series goes from at-or-below
// to above, so a series that sits at 95 for a minute produces one alert and not
// three thousand.
const AlertAbove = 90

// MaxAlerts is how many alerts the feed and a session keep. The feed trims when
// it raises and a session trims when it folds, so a session that connected an
// hour ago and one that connected a second ago hold the same log.
const MaxAlerts = 12

// Reading is one sample of every series, as one value.
//
// It is immutable and it is held behind a pointer in State, which is what lets
// the library's state comparison work: internal/session's sameState reports a
// state type that is not comparable as changed on EVERY transition, so a
// no-op event would bump the version and ask every fragment's Dirty about a
// change that did not happen. One pointer field keeps that machinery working,
// and the same argument is written out at length in examples/chat's Log.
type Reading struct {
	// Seq is the sample number, from 1. It is rendered, and that is not
	// decoration: two consecutive readings with identical values would
	// otherwise render identical bytes and the patch would be suppressed,
	// which is correct behaviour that would make "the feed is pushing" and
	// "the feed has stalled" look the same on the wire.
	Seq         uint64
	AtUnixMilli int64
	Values      []int
}

// Value returns the reading for one series, and whether the series was in the
// reading at all.
func (r *Reading) Value(series string) (int, bool) {
	if r == nil {
		return 0, false
	}
	i := slices.Index(Series, series)
	if i < 0 || i >= len(r.Values) {
		return 0, false
	}
	return r.Values[i], true
}

// Alert is one threshold crossing.
type Alert struct {
	Seq         uint64
	Series      string
	Value       int
	AtUnixMilli int64
}

// Clock renders an alert's timestamp the way the log shows it.
//
// A render may not read a clock — it must be a pure function of state, or two
// renders of the same state produce different bytes and the patch suppression
// that compares them breaks — so this formats a stamp the feed took, and takes
// no reading of its own.
func (a Alert) Clock() string {
	if a.AtUnixMilli == 0 {
		return "--:--:--"
	}
	return time.UnixMilli(a.AtUnixMilli).UTC().Format("15:04:05")
}

// AlertLog is the alert list as one session currently sees it. Immutable, held
// behind a pointer, for the reasons Reading's doc comment gives.
type AlertLog struct {
	Version uint64
	Entries []Alert
}

func (l *AlertLog) entries() []Alert {
	if l == nil {
		return nil
	}
	return l.Entries
}

// with returns the log that results from one new alert, trimmed. The receiver
// is not touched.
func (l *AlertLog) with(a Alert, version uint64) *AlertLog {
	base := l.entries()
	keep := min(len(base), MaxAlerts-1)

	next := make([]Alert, 0, keep+1)
	next = append(next, base[len(base)-keep:]...)
	next = append(next, a)
	return &AlertLog{Version: version, Entries: next}
}

// The event names the feed emits into every subscribed session.
//
// They are deliberately NOT in Config.Events. Registration is what makes a name
// sendable by a browser, and a client that could send dashboard.sample could
// put any reading it liked on every other operator's screen: readings are
// produced by the feed, and an event that did not come from the feed is not a
// reading. Events an effect emits never came from the wire and never pass
// through the registration check.
const (
	EventSample  = "dashboard.sample"
	EventAlert   = "dashboard.alert"
	EventCleared = "dashboard.cleared"
)

// The field names on the events the feed emits. The series names are field keys
// too, which is why Series holds strings that are legal field keys.
const (
	fieldSeq     = "seq"
	fieldAtMilli = "at_ms"
	fieldSeries  = "series"
	fieldValue   = "value"
	fieldVersion = "version"
)

// update is one thing that happened in the feed, on its way to one session.
//
// It is queued rather than pre-rendered as a live.Event because one of the
// event's fields depends on who is receiving it: the contributing edge back to
// a probe belongs to the session that probed and to nobody else.
type update struct {
	kind    string
	reading Reading
	alert   Alert
	version uint64

	// causeFor and cause are the contributing edge. It is a claim about one
	// recipient's own event, so it is only attached when the update reaches
	// that recipient — identifiers are session-scoped, and naming another
	// session's event is not a thing that can be true.
	causeFor live.ID
	cause    uint64
}

// event renders the update as the live.Event the pump emits into one session.
func (u update) event(to live.ID) live.Event {
	fields := map[string]string{
		fieldSeq:     strconv.FormatUint(u.reading.Seq, 10),
		fieldAtMilli: strconv.FormatInt(u.reading.AtUnixMilli, 10),
		fieldVersion: strconv.FormatUint(u.version, 10),
	}
	switch u.kind {
	case EventSample:
		for i, name := range Series {
			if i < len(u.reading.Values) {
				fields[name] = strconv.Itoa(u.reading.Values[i])
			}
		}
	case EventAlert:
		fields[fieldSeq] = strconv.FormatUint(u.alert.Seq, 10)
		fields[fieldAtMilli] = strconv.FormatInt(u.alert.AtUnixMilli, 10)
		fields[fieldSeries] = u.alert.Series
		fields[fieldValue] = strconv.Itoa(u.alert.Value)
	}

	var contributing []uint64
	if u.cause != 0 && u.causeFor == to {
		contributing = []uint64{u.cause}
	}
	return live.Event{Name: u.kind, Contributing: contributing, Fields: live.NewFields(fields)}
}

// subscriber is one session's slot in the feed: a bounded backlog and a mark
// saying the feed gave up keeping it.
type subscriber struct {
	queue chan update
	// behind is set when the feed dropped an update for this session. It is
	// read by the pump on its own goroutine and written by whichever goroutine
	// happened to be sampling, so it is atomic rather than under the feed's
	// lock: the feed must not hold its lock while touching a subscriber, or it
	// establishes a lock order nothing else in this file needs.
	behind atomic.Bool
}

func (s *subscriber) offer(u update) {
	select {
	case s.queue <- u:
	default:
		s.behind.Store(true)
	}
}

// Depth reports how many updates are waiting in this subscriber's queue. The
// backpressure spec reads it: it is the application-owned half of "the server's
// queues stay bounded", the library's half being the outbound window.
func (s *subscriber) Depth() int { return len(s.queue) }

// Feed is the simulated metrics source: the readings every session shares, and
// the subscription list that pushes them.
//
// It is SIMULATED, and this comment is the only place that needs to be said
// because nothing below pretends otherwise: the values are a bounded random
// walk over three made-up series, produced by a deterministic generator this
// process seeds. Nothing here reads a real machine, and an example that
// scraped /proc would be measuring this VM rather than demonstrating a library.
// What is real is everything the walk feeds into — a source of change that the
// server owns, at a rate no browser asked for, delivered by a push.
//
// It is the application's own state, not the library's. gotth-live gives each
// session a goroutine that owns that session's state and nothing else — there
// is deliberately no cross-session write API — so anything genuinely shared
// lives out here, behind an ordinary mutex, and reaches sessions through
// effects and emitted events. A Prometheus scrape or a Kafka topic would take
// the same shape; this one is a struct because the example should run with
// nothing installed.
type Feed struct {
	mu      sync.Mutex
	seq     uint64
	version uint64
	values  []int
	above   []bool
	alerts  []Alert
	subs    map[live.ID]*subscriber

	// lastAt is when the most recent sample was taken. It is stored rather
	// than read at render time because a render may not call a clock: the same
	// state must produce byte-identical HTML, and a timestamp taken during the
	// render would make every re-render of an unchanged reading a new patch.
	lastAt int64

	// step advances one series by one tick, and now is the clock.
	//
	// Both are fields rather than direct calls to math/rand and time.Now so a
	// spec can drive them, and they live out here rather than in a reducer for
	// the reason that matters: a reducer that read a random source or a clock
	// would fail livetest.ReplayN. Putting every non-deterministic thing this
	// application does behind the effect boundary is what makes the reducer
	// replayable, and this is where those things went.
	//
	// The default step is a bounded random walk over a generator this process
	// seeds, so the same seed produces the same readings in the same order —
	// which is what lets `-seed` reproduce a run.
	step func(v int) int
	now  func() time.Time

	// ticker is the goroutine that samples on a schedule, and stop ends it.
	// The feed owns it rather than a session, because the feed is what
	// produces the change: a source of server-side change that stopped when
	// one browser disconnected would be a browser-driven feed with extra
	// steps.
	interval time.Duration
	stop     chan struct{}
	stopped  chan struct{}
	stopOnce sync.Once
}

// NewFeed returns a feed at rest, with every series mid-range.
//
// seed makes the walk reproducible: the same seed produces the same readings in
// the same order, which is what lets a spec assert on a threshold crossing
// without waiting for one to happen by chance.
func NewFeed(seed uint64, interval time.Duration) *Feed {
	walk := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	return &Feed{
		values:   []int{42, 55, 30},
		above:    make([]bool, len(Series)),
		subs:     make(map[live.ID]*subscriber),
		step:     func(v int) int { return clamp(v+walk.IntN(21)-10, 0, 100) },
		now:      time.Now,
		interval: interval,
		stop:     make(chan struct{}),
		stopped:  make(chan struct{}),
	}
}

// Start begins sampling on a schedule. It returns immediately; Stop ends it and
// waits.
func (f *Feed) Start() {
	go func() {
		defer close(f.stopped)
		t := time.NewTicker(f.interval)
		defer t.Stop()
		for {
			select {
			case <-f.stop:
				return
			case <-t.C:
				f.Sample(live.ID{}, 0)
			}
		}
	}()
}

// Stop ends the sampling goroutine and waits for it. It is idempotent, because
// a shutdown path that panics on a second call is a shutdown path nobody can
// call from two places.
func (f *Feed) Stop() {
	f.stopOnce.Do(func() { close(f.stop) })
	<-f.stopped
}

// Reading returns the feed as it stands, for the first HTTP paint.
func (f *Feed) Reading() *Reading {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.readingLocked()
}

// Alerts returns the shared alert log, for the first HTTP paint.
func (f *Feed) Alerts() *AlertLog {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &AlertLog{Version: f.version, Entries: slices.Clone(f.alerts)}
}

// Subscribers is how many sessions are subscribed. The leak spec reads it: a
// teardown that did not unsubscribe leaves this above zero with every
// connection closed.
func (f *Feed) Subscribers() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subs)
}

// QueueDepth returns the deepest per-session backlog the feed is holding. It is
// the application's own queue-depth signal, and the backpressure spec asserts
// on it beside the library's outbound window.
func (f *Feed) QueueDepth() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	deepest := 0
	for _, sub := range f.subs {
		deepest = max(deepest, sub.Depth())
	}
	return deepest
}

func (f *Feed) readingLocked() *Reading {
	return &Reading{
		Seq:         f.seq,
		AtUnixMilli: f.stampLocked(),
		Values:      slices.Clone(f.values),
	}
}

func (f *Feed) stampLocked() int64 {
	if f.seq == 0 {
		return 0
	}
	return f.lastAt
}

// Join registers a session for pushes and returns the feed as of that moment.
//
// Registering and reading happen under one lock, and that is the whole reason
// this is one method rather than a Subscribe and a Reading. Split in two, a
// sample landing between them is either shown twice or missed entirely, and the
// window is exactly as wide as a page load.
func (f *Feed) Join(id live.ID) (*Reading, *AlertLog) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs[id] = &subscriber{queue: make(chan update, backlogDepth)}
	return f.readingLocked(), &AlertLog{Version: f.version, Entries: slices.Clone(f.alerts)}
}

// Leave unregisters a session. Config.Teardown calls it, after the session's
// goroutine has exited, so a session that dropped its connection does not leave
// a subscription behind.
//
// The queue is dropped, not closed. Closing it would be a nicer way to stop a
// pump that is still running, and it is unsafe: a sample that took the
// subscriber list under the lock a moment ago is about to offer into that
// channel from another goroutine, and a send on a closed channel is a panic in
// the feed rather than a contained one in a session. The pump exits on the
// session's context instead, which is cancelled when the session ends — and
// Teardown, which calls this, runs after that.
func (f *Feed) Leave(id live.ID) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.subs, id)
}

// Sample advances the walk by one step and pushes the result to every
// subscribed session.
//
// by and cause are the probe edge: they are the zero values for the scheduled
// tick, which nobody asked for, and the requesting session and its event
// identifier for a probe. A tick and a probe are otherwise the same operation,
// which is deliberate — a dashboard that produced a different KIND of reading
// when you pressed refresh would be two feeds.
func (f *Feed) Sample(by live.ID, cause uint64) Reading {
	f.mu.Lock()
	f.seq++
	f.version++
	f.lastAt = f.now().UnixMilli()

	for i := range f.values {
		f.values[i] = f.step(f.values[i])
	}
	reading := f.readingLocked()

	// Edge-triggered. A series that sits above the threshold raises one alert,
	// not one per sample: the second is not new information, and a log that
	// filled with it would be a log nobody reads.
	var raised []Alert
	for i, v := range f.values {
		switch {
		case v > AlertAbove && !f.above[i]:
			f.above[i] = true
			f.version++
			alert := Alert{Seq: f.seq, Series: Series[i], Value: v, AtUnixMilli: f.lastAt}
			f.alerts = append(f.alerts, alert)
			if len(f.alerts) > MaxAlerts {
				// Re-slice into a fresh backing array rather than sliding the
				// window over the old one, so the *AlertLog values handed out
				// earlier — which are promised to be immutable — cannot be
				// written through.
				f.alerts = slices.Clone(f.alerts[len(f.alerts)-MaxAlerts:])
			}
			raised = append(raised, alert)
		case v <= AlertAbove:
			f.above[i] = false
		}
	}
	version := f.version
	all := f.subscribersLocked()
	f.mu.Unlock()

	broadcast(all, update{
		kind: EventSample, reading: *reading, version: version,
		causeFor: by, cause: cause,
	})
	for _, alert := range raised {
		broadcast(all, update{
			kind: EventAlert, alert: alert, version: version,
			causeFor: by, cause: cause,
		})
	}
	return *reading
}

// Clear empties the shared alert log and tells every session.
func (f *Feed) Clear(by live.ID, cause uint64) {
	f.mu.Lock()
	f.alerts = nil
	f.version++
	version := f.version
	all := f.subscribersLocked()
	f.mu.Unlock()

	broadcast(all, update{kind: EventCleared, version: version, causeFor: by, cause: cause})
}

// subscribersLocked copies the subscriber list.
//
// The copy is taken under the feed's lock and delivered outside it. A
// subscriber's offer touches a channel and an atomic, and doing that while
// holding this lock would let one slow send block every other session's sample.
func (f *Feed) subscribersLocked() []*subscriber {
	out := make([]*subscriber, 0, len(f.subs))
	for _, sub := range f.subs {
		out = append(out, sub)
	}
	return out
}

func broadcast(subs []*subscriber, u update) {
	for _, sub := range subs {
		sub.offer(u)
	}
}

// pump delivers feed updates to one session until its context is cancelled.
//
// It runs for the session's whole life, on a goroutine the library owns and
// waits for at shutdown, which is why it returns promptly on cancellation
// rather than on a feed signal it might never get. That is also the property
// the leak spec measures: no goroutine of this application outlives the session
// it belongs to.
func (f *Feed) pump(ctx context.Context, id live.ID, emit live.Emitter) error {
	f.mu.Lock()
	sub := f.subs[id]
	f.mu.Unlock()
	if sub == nil {
		return fmt.Errorf("dashboard: session %s is not subscribed: Config.Init must Join before it returns a subscribe effect", id)
	}

	var (
		pending  update
		holding  bool
		refusals int
	)
	for {
		if !holding {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case u := <-sub.queue:
				pending, holding = u, true
			}
		}

		if sub.behind.Load() {
			// Terminal, and not merely unclassified. Re-subscribing cannot
			// refill a gap, and a dashboard that silently missed the sample
			// that crossed a threshold looks right while being wrong.
			return fmt.Errorf(
				"dashboard: this session fell more than %d updates behind the feed: reload to catch up",
				backlogDepth)
		}

		if err := emit(pending.event(id)); err == nil {
			holding, refusals = false, 0
			continue
		}

		// The mailbox was full, or the session is closing.
		refusals++
		if refusals >= maxRefusals {
			// Transient by construction. Neither a full mailbox nor a session
			// mid-shutdown is a property of this effect, so a fresh
			// subscription has every chance of working — which is the claim
			// live.Retryable makes, and the reason it is this code making it
			// rather than the reducer guessing.
			return live.Retryable(fmt.Errorf(
				"dashboard: the session refused %d feed updates in a row", refusals))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}
	}
}

func clamp(v, lo, hi int) int { return min(max(v, lo), hi) }
