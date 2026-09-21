package main

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// The feed: the authoritative frame every session's view is folded from, the
// fixture replay that moves it, and region E's per-session panel.
//
// This is the application's own shared state, not the library's. gotth-live
// gives each session a goroutine that owns that session's state and nothing
// else — there is deliberately no cross-session write API — so anything
// genuinely shared lives out here behind an ordinary mutex and reaches sessions
// through effects and emitted events. A Redis stream or a Postgres LISTEN would
// take the same shape.

// SourceSubscribe names the subscription effect in provenance and in metrics,
// so it is the string an operator greps for — and, because a failure event
// reports the source of the effect that failed, the string the reducer matches
// on.
const SourceSubscribe = "dash.subscribe"

// retryDelay and maxRefusals bound the subscription pump's patience before it
// hands the decision to the reducer. Retrying forever inside the effect is the
// shape that hides a stuck subscription.
const (
	retryDelay  = 20 * time.Millisecond
	maxRefusals = 50
)

// backlogDepth is how many ticks one session may be behind before the feed
// stops keeping them. It is generous on purpose: it must not be the thing that
// bounds a slow session, because the library's outbound window is what FR-51
// makes that claim about and a spec cannot tell the two apart if both are
// tight. At §2.5's 10 Hz it is nearly seven minutes of backlog.
const backlogDepth = 4096

// PanelGrace is how long region E's panel survives with no live session holding
// it.
//
// Only a refresh press creates an entry — the page renders the default panel
// without touching the store — so a document request cannot mint one and D4's
// throughput run, which asks for nothing else, leaves this map empty. The grace
// exists for the one remaining creator: a client that pressed refresh and never
// opened a connection. It is BENCH-1's 30 s session-eviction default, taken
// rather than re-derived so the two stacks evict on the same clock.
const PanelGrace = 30 * time.Second

// panelSweepEvery is how often the sweep runs, in ticks. Once a second at
// §2.5's schedule; sweeping on every tick would take the lock ten times a second
// to walk a map that is almost always empty.
const panelSweepEvery = 10

// SubscribeEffect is the effect that pushes this session every tick until the
// session ends. It captures nothing: a subscription's address is the session it
// belongs to, which the library already knows and hands to Run.
func (f *Feed) SubscribeEffect() live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: SourceSubscribe,
		Run: func(ctx context.Context, sess live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			return f.pump(ctx, sess.ID(), emit)
		},
	}
}

// subscriber is one session's slot: a bounded backlog and a mark saying the
// feed gave up keeping it.
type subscriber struct {
	queue  chan live.Event
	behind atomic.Bool
}

func (s *subscriber) offer(ev live.Event) {
	select {
	case s.queue <- ev:
	default:
		s.behind.Store(true)
	}
}

// panelState is region E for one bench session id: the panel and what keeps it
// alive.
type panelState struct {
	panel Panel
	// bound is how many live sessions were served with this id. A bound entry
	// is never swept, which is the rule the Next.js store applies to a session
	// holding its push channel.
	bound   int
	touched time.Time
}

// Feed is the whole shared surface.
type Feed struct {
	mu     sync.Mutex
	frame  Snap
	subs   map[live.ID]*subscriber
	panels map[string]*panelState
	replay *Replay
	sha    string

	// now is the clock, a field so a spec can drive the panel sweep without
	// waiting for it. Nothing in a reducer reads it: every non-deterministic
	// thing this application does is behind the effect boundary, which is what
	// makes the reducer replayable.
	now func() time.Time
}

// NewFeed builds the authoritative frame from the committed fixture's base
// record and prepares the replay. Start begins it.
//
// The base record is READ rather than computed — the 200 rows, the eight KPI
// seeds, the 60 points of sparkline history and the 2×120 of series history are
// all in the file — so E3 ("both servers render identical information at
// identical times") rests on both servers reading the same bytes rather than on
// both running the same generator (§2.5).
func NewFeed(fixture *Fixture) *Feed {
	rows := make([]*Row, 0, len(fixture.Base.Rows))
	for _, r := range fixture.Base.Rows {
		rows = append(rows, &Row{ID: r.ID, Name: r.Name, Status: r.Status, M1: r.M1, M2: r.M2, M3: r.M3, TS: r.TS})
	}

	spark := make([][]int, len(fixture.Base.Spark))
	for i, history := range fixture.Base.Spark {
		spark[i] = append([]int(nil), history...)
	}

	var series SeriesSet
	for i := 0; i < 2 && i < len(fixture.Base.Series); i++ {
		series.Points[i] = append([]int(nil), fixture.Base.Series[i]...)
	}

	f := &Feed{
		frame: Snap{
			Tick:  TickNone,
			Table: &Table{Rows: rows},
			KPIs: &KPISet{
				Labels: fixture.Base.KPILabels,
				Values: fixture.Base.KPI,
				// The first delta is against the first sample, so it is zero and
				// not a jump from nothing. The Next.js store seeds kpiPrev the
				// same way, from the same numbers.
				Prev:  fixture.Base.KPI,
				Spark: spark,
			},
			Series: &series,
			Log:    &EventLog{},
		},
		subs:   make(map[live.ID]*subscriber),
		panels: make(map[string]*panelState),
		sha:    fixture.SHA256,
		now:    time.Now,
	}
	f.replay = NewReplay(fixture.Ticks, TickMs*time.Millisecond, f.applyTick)
	return f
}

// SetInterval changes the replay interval before Start. §2.5's schedule is
// 100 ms; the flag exists so a soak can be driven faster without a second
// fixture, and the interval in force is recorded in the run manifest.
func (f *Feed) SetInterval(d time.Duration) {
	if d > 0 {
		f.replay.interval = d
	}
}

// Start begins the fixture replay.
func (f *Feed) Start() { f.replay.Start() }

// Stop ends the fixture replay and waits for it.
func (f *Feed) Stop() { f.replay.Stop() }

// FixtureSHA256 is the digest of the bytes this process read, for the run
// manifest (§6).
func (f *Feed) FixtureSHA256() string { return f.sha }

// Clock is §3.2's control channel content: T0 and the replay position.
func (f *Feed) Clock() (t0 time.Time, tick int) { return f.replay.T0(), f.replay.TickNow() }

/* --------------------------------------------------------------- replay --- */

// TickEvent encodes one fixture tick as the event every session folds.
//
// It is built ONCE per tick and handed to every subscriber, rather than once per
// subscriber: at §2.4's rates and §3.6's 1,000 sessions the difference is a
// thousand encodings a tick against one, and the event carries nothing
// session-specific — a tick is not caused by anybody's click, so there is no
// contributing edge to attach and no reason for two sessions to receive
// different bytes.
//
// It is a pure function of the tick, which is what lets a spec assert the
// encoding without a feed.
func TickEvent(t Tick) live.Event {
	fields := map[string]string{fieldTick: strconv.Itoa(t.N)}
	for _, e := range t.E {
		switch e.Kind {
		case FixtureRows:
			fields[fieldRows] = EncodeRows(e.R)
		case FixtureKPI:
			fields[fieldKPI] = encodeInts(e.V)
		case FixtureSeries:
			fields[fieldSeries] = encodeInts(e.V)
		case FixtureLog:
			fields[fieldLogSeq] = strconv.Itoa(e.Seq)
			fields[fieldLogTxt] = e.Text
		}
	}
	return live.Event{Name: EventTick, Fields: live.NewFields(fields)}
}

func encodeInts(values []int) string {
	var b strings.Builder
	for i, v := range values {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.Itoa(v))
	}
	return b.String()
}

// applyTick folds one fixture tick into the authoritative frame and pushes it.
//
// Neither this process nor the Next.js one generates the data: both read the
// same committed bytes and apply them on the same monotonic schedule (§2.5), so
// tick N is the same information on both stacks at the same instant.
//
// The frame is folded through FoldSnap — the same function every session's
// reducer uses — so the authority and a session that joined ten minutes later
// cannot disagree about what tick N meant.
func (f *Feed) applyTick(t Tick) {
	ev := TickEvent(t)

	f.mu.Lock()
	next, moved := FoldSnap(f.frame, ev)
	if moved {
		f.frame = next
	}
	if t.N%panelSweepEvery == 0 {
		f.sweepPanelsLocked(f.now())
	}
	subs := f.subscribersLocked()
	f.mu.Unlock()

	if !moved {
		return
	}
	// Delivered outside the lock: an offer touches a channel and an atomic, and
	// doing that while holding this lock would let one slow session block every
	// other session's tick.
	for _, sub := range subs {
		sub.offer(ev)
	}
}

func (f *Feed) subscribersLocked() []*subscriber {
	out := make([]*subscriber, 0, len(f.subs))
	for _, sub := range f.subs {
		out = append(out, sub)
	}
	return out
}

/* ------------------------------------------------------------ lifecycle --- */

// Join registers a session for pushes and returns the frame as of that moment.
//
// Registering and reading happen under one lock, and that is the whole reason
// this is one method rather than a Subscribe and a Snapshot: a tick landing
// between them is either folded twice or missed entirely, and the window is
// exactly as wide as a page load.
func (f *Feed) Join(id live.ID, sid string) Snap {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.subs[id] = &subscriber{queue: make(chan live.Event, backlogDepth)}
	if p := f.panels[sid]; p != nil {
		p.bound++
		p.touched = f.now()
	}
	return f.frame
}

// Leave unregisters a session and releases its hold on region E's panel.
//
// Config.Teardown calls it, after the session's goroutine has exited, so a
// session that dropped its connection does not leave a subscription behind.
//
// The queue is dropped, not closed. Closing it would be a nicer way to stop a
// pump that is still running and it is unsafe: a tick that took the subscriber
// list under the lock a moment ago is about to offer into that channel from
// another goroutine, and a send on a closed channel is a panic in the feed
// rather than a contained one in a session.
func (f *Feed) Leave(id live.ID, sid string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.subs, id)
	if p := f.panels[sid]; p != nil && p.bound > 0 {
		p.bound--
		p.touched = f.now()
	}
}

// Subscribers is how many sessions are subscribed. A leak spec reads it: a
// teardown that did not unsubscribe leaves this above zero with every
// connection closed.
func (f *Feed) Subscribers() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.subs)
}

/* -------------------------------------------------------------- region E --- */

// RefreshPanel is region E's button, arriving as an ordinary HTTP GET from
// HTMX (AS-3, FR-62's "plain HTMX region on the same page").
//
// It is the one thing on this page that is not pushed: it changes when a person
// presses the button and at no other time, which is what "on demand" means in
// §2.4's table. The text is the Next.js action's, word for word, so the two
// panels are the same string for the same feed state.
func (f *Feed) RefreshPanel(sid string) Panel {
	f.mu.Lock()
	defer f.mu.Unlock()

	errors := 0
	for _, row := range f.frame.Table.rows() {
		if row.Status == "error" {
			errors++
		}
	}

	tick := f.frame.Tick
	if tick < 0 {
		tick = 0
	}

	p := f.panels[sid]
	if p == nil {
		p = &panelState{}
		f.panels[sid] = p
	}
	p.panel = Panel{
		Seq:  p.panel.Seq + 1,
		Text: strconv.Itoa(errors) + " rows in error at tick " + strconv.Itoa(tick),
		TS:   int64(tick) * TickMs,
	}
	p.touched = f.now()
	return p.panel
}

// PanelOf is what the page renders for region E.
//
// A session that has never pressed the button gets the default panel WITHOUT an
// entry being created for it. That is what keeps the map bounded under D4,
// which asks for the document at the highest rate the stack will serve and
// presses nothing.
func (f *Feed) PanelOf(sid string) Panel {
	f.mu.Lock()
	defer f.mu.Unlock()
	if p := f.panels[sid]; p != nil {
		return p.panel
	}
	return Panel{Text: DefaultPanelText}
}

// Panels is how many panel entries exist, for a bounded-growth spec.
func (f *Feed) Panels() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.panels)
}

func (f *Feed) sweepPanelsLocked(now time.Time) {
	for sid, p := range f.panels {
		if p.bound > 0 || now.Sub(p.touched) < PanelGrace {
			continue
		}
		delete(f.panels, sid)
	}
}

// pump delivers ticks to one session until its context is cancelled.
//
// It runs for the session's whole life, on a goroutine the library owns and
// waits for at shutdown, which is why it returns promptly on cancellation
// rather than on a signal it might never get. That is also the property a leak
// spec measures: no goroutine of this application outlives its session.
func (f *Feed) pump(ctx context.Context, id live.ID, emit live.Emitter) error {
	f.mu.Lock()
	sub := f.subs[id]
	f.mu.Unlock()
	if sub == nil {
		return fmt.Errorf(
			"dashboard-gotth: session %s is not subscribed: Config.Init must Join before it returns a SubscribeEffect", id)
	}

	var (
		pending  live.Event
		holding  bool
		refusals int
	)
	for {
		if !holding {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case ev := <-sub.queue:
				pending, holding = ev, true
			}
		}

		if sub.behind.Load() {
			// Terminal, and not merely unclassified. Re-subscribing cannot
			// refill a gap: region C shifts one point per tick and region D
			// appends, so a session that missed ticks is not stale, it is
			// WRONG — and a dashboard that is wrong looks right.
			return fmt.Errorf(
				"dashboard-gotth: this session fell more than %d ticks behind: reload to catch up", backlogDepth)
		}

		if err := emit(pending); err == nil {
			holding, refusals = false, 0
			continue
		}

		refusals++
		if refusals >= maxRefusals {
			// Transient by construction. Neither a full mailbox nor a session
			// mid-shutdown is a property of this effect, so a fresh
			// subscription has every chance of working — which is the claim
			// live.Retryable makes, and the reason it is this code making it
			// rather than the reducer guessing.
			return live.Retryable(fmt.Errorf(
				"dashboard-gotth: the session refused %d ticks in a row", refusals))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(retryDelay):
		}
	}
}

/* ------------------------------------------------------------ page paint --- */

// Frame is the authoritative frame, for the first HTTP paint and for specs.
func (f *Feed) Frame() Snap {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.frame
}
