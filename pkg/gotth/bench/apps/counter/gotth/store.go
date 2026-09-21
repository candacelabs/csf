package main

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// retryDelay is how long the subscription pump waits before re-offering a
// snapshot the session could not accept. It is long enough that a saturated
// mailbox has drained and short enough that a person does not notice.
const retryDelay = 50 * time.Millisecond

// maxRefusals is how many snapshots in a row the pump absorbs before it gives
// up and hands the decision to the reducer.
//
// Retrying forever inside the effect is the shape that hides a stuck
// subscription: the pump keeps offering, the session keeps not accepting, and
// nothing above the effect ever learns. A bound turns that into a retryable
// failure event, which is something a reducer can act on. At retryDelay apiece
// this is a second of trying before asking.
const maxRefusals = 20

// The effect sources. They name the effect in provenance and metrics, so they
// are the strings an operator greps for — and, since a failure event reports
// the source of the effect that failed, the strings the reducer matches on.
const (
	SourceChange = "counter.change"
	SourceWatch  = "counter.watch"
)

// Op is one operation the shared counter understands.
type Op uint8

// The operations. There is no OpSet, because nothing the browser can send
// carries a number: see the Event* constants.
const (
	OpAdd Op = iota + 1
	OpReset
)

// Change is one operation asked of the shared counter: what to do, by whom,
// and which event asked for it.
//
// It is a plain comparable value and it is NOT the effect. Since the
// 2026-09-03 ruling made live.Effect[live.AnonymousIdentity] a concrete struct carrying its own Run,
// what a reducer returns is the effect [Store.ChangeEffect] builds from one of
// these — a closure over this store — and this type is the operation that
// closure applies. Keeping the two apart is what lets [Store.Apply] be called
// directly by a specification that has no session.
type Change struct {
	Op    Op
	Delta int64
	By    live.ID
	// Cause is the identifier of the event that asked for this change. It
	// rides through the store so that the broadcast which finally changes a
	// number can name the click that changed it: the subscription delivering
	// that broadcast was scheduled at mount, so without carrying the cause the
	// only provenance edge left is "some effect did it".
	Cause uint64
}

// ChangeEffect is the effect that applies one operation to the shared counter.
//
// It is a constructor returning a concrete live.Effect[live.AnonymousIdentity], which is what CS-8
// asks of every factory: the caller receives the thing rather than an
// abstraction over it. The behaviour is a closure over this store, so the
// reducer that returns one does not have to be handed an executor and the
// library does not have to type-switch to find one.
//
// The source it stamps becomes the origin "effect:counter.change" on every
// patch this effect causes, which is the string to grep for in the provenance
// log.
func (s *Store) ChangeEffect(change Change) live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: SourceChange,
		Run: func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			s.Apply(change)
			return nil
		},
	}
}

// WatchEffect is the effect that pushes this session every change until the
// session ends.
//
// It exists because an effect's Run is the only place an application is handed
// a live.Emitter, so a subscription that wants to inject events has to be
// expressed as a long-running effect. Config.Init registers the session with
// the store; this pumps what the registration collects.
//
// It captures nothing, and that is the point of the live.Session[live.AnonymousIdentity] parameter Run
// takes. A subscription's address is the session it belongs to, which the
// library knows; what an effect should carry is what the reducer decided, not
// who decided it.
func (s *Store) WatchEffect() live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: SourceWatch,
		Run: func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			return s.pump(ctx, session.ID(), emit)
		},
	}
}

// Snapshot is the shared counter at one revision. It is a value, so a
// subscriber holds a consistent picture rather than a window onto something
// still moving.
type Snapshot struct {
	Value              int64
	Version            uint64
	Tabs               int
	ChangedBy          live.ID
	ChangedAtUnixMilli int64
	// ChangedByEvent is the event of the ChangedBy session that produced this
	// version. It is meaningful only to that session: every other subscriber
	// receives the same snapshot with no event of its own to point at.
	ChangedByEvent uint64
}

// Store is the counter itself: the server-authoritative value every session
// shares, and the subscription list that pushes it to them.
//
// It is the application's own state, not the library's. gotth-live gives each
// session a goroutine that owns that session's state and nothing else — there
// is deliberately no cross-session write API — so anything genuinely shared
// lives out here, behind an ordinary mutex, and reaches sessions through
// effects and emitted events. That is the same shape a Redis key or a Postgres
// row would take; this one is a struct because the example should be runnable
// with nothing installed.
type Store struct {
	mu             sync.Mutex
	value          int64
	version        uint64
	changedBy      live.ID
	changedByEvent uint64
	changedAt      time.Time
	subs           map[live.ID]*subscriber

	// now is the clock, injectable so a spec can drive the relative timestamp
	// without waiting for one.
	now func() time.Time
}

// NewStore returns an empty counter.
func NewStore() *Store {
	return &Store{subs: make(map[live.ID]*subscriber), now: time.Now}
}

// subscriber is one session's slot: the latest snapshot, and a one-slot signal
// saying it is unread.
//
// Latest-value-wins is the right queue here and not a simplification. A
// snapshot is absolute, so an older one that was never delivered has no
// information the newer one lacks; queueing them all would trade memory for
// nothing and would deliver a burst of intermediate values a person never
// asked to see.
type subscriber struct {
	mu   sync.Mutex
	snap Snapshot
	wake chan struct{}
}

func (s *subscriber) set(snap Snapshot) {
	s.mu.Lock()
	s.snap = snap
	s.mu.Unlock()
	s.signal()
}

func (s *subscriber) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *subscriber) snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snap
}

// Snapshot returns the counter as it stands, for the initial HTTP render.
func (s *Store) Snapshot() Snapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.snapshotLocked()
}

func (s *Store) snapshotLocked() Snapshot {
	snap := Snapshot{
		Value:          s.value,
		Version:        s.version,
		Tabs:           len(s.subs),
		ChangedBy:      s.changedBy,
		ChangedByEvent: s.changedByEvent,
	}
	if !s.changedAt.IsZero() {
		snap.ChangedAtUnixMilli = s.changedAt.UnixMilli()
	}
	return snap
}

// Join registers a session for pushes and returns the counter as of that
// moment.
//
// Registering and reading happen under one lock, and that is the whole reason
// this is one method rather than a Subscribe and a Snapshot. Split in two,
// a change landing between them is either applied twice or missed entirely,
// depending on the order — and the window is exactly as wide as a page load,
// which is when a second tab opens.
func (s *Store) Join(id live.ID) Snapshot {
	s.mu.Lock()
	s.subs[id] = &subscriber{wake: make(chan struct{}, 1)}
	snap := s.snapshotLocked()
	others := s.subscribersLocked(&id)
	s.mu.Unlock()

	// The joiner changed the tab count every other tab is showing.
	broadcast(others, snap)
	return snap
}

// Leave unregisters a session. Config.Teardown calls it, which runs after the
// session's goroutine has exited, so a session that dropped its connection
// does not leave a subscription behind.
func (s *Store) Leave(id live.ID) {
	s.mu.Lock()
	delete(s.subs, id)
	snap := s.snapshotLocked()
	others := s.subscribersLocked(&id)
	s.mu.Unlock()

	broadcast(others, snap)
}

// Apply performs one operation and pushes the result to every session.
func (s *Store) Apply(change Change) Snapshot {
	s.mu.Lock()
	switch change.Op {
	case OpAdd:
		s.value += change.Delta
	case OpReset:
		s.value = 0
	}
	s.version++
	s.changedBy = change.By
	s.changedByEvent = change.Cause
	s.changedAt = s.now()
	snap := s.snapshotLocked()
	all := s.subscribersLocked(nil)
	s.mu.Unlock()

	broadcast(all, snap)
	return snap
}

// subscribersLocked copies the subscriber list, optionally skipping one.
//
// The copy is taken under the store's lock and delivered outside it. A
// subscriber's set() takes its own lock, and calling into it while holding
// this one would establish a lock order between the two that nothing else in
// this file needs.
func (s *Store) subscribersLocked(except *live.ID) []*subscriber {
	out := make([]*subscriber, 0, len(s.subs))
	for id, sub := range s.subs {
		if except != nil && id == *except {
			continue
		}
		out = append(out, sub)
	}
	return out
}

func broadcast(subs []*subscriber, snap Snapshot) {
	for _, sub := range subs {
		sub.set(snap)
	}
}

// pump delivers snapshots to one session until its context is cancelled.
//
// It runs for the session's whole life, on a goroutine the library owns and
// waits for at shutdown, which is why it returns promptly on cancellation
// rather than on a store signal it might never get.
func (s *Store) pump(ctx context.Context, id live.ID, emit live.Emitter) error {
	s.mu.Lock()
	sub := s.subs[id]
	s.mu.Unlock()
	if sub == nil {
		return fmt.Errorf("counter: session %s is not subscribed: Config.Init must Join before it returns a WatchEffect", id)
	}

	refusals := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-sub.wake:
			if err := emit(SyncEvent(sub.snapshot(), id)); err == nil {
				refusals = 0
				continue
			}
			// The mailbox was full, or the session is closing.
			refusals++
			if refusals >= maxRefusals {
				// Transient by construction. Neither a full mailbox nor a
				// session mid-shutdown is a property of this effect, so a
				// fresh subscription has every chance of working — which is
				// exactly the claim live.Retryable makes, and the reason it is
				// this code making it rather than the reducer guessing.
				return live.Retryable(fmt.Errorf(
					"counter: the session refused %d snapshots in a row", refusals))
			}
			// Re-arm and try again with whatever the newest snapshot is by
			// then: a sync is absolute, so re-offering one is never a change
			// applied twice, and a session that really is closing has had its
			// context cancelled a moment earlier.
			sub.signal()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(retryDelay):
			}
		}
	}
}

// SyncEvent is the event a snapshot travels in.
//
// It carries the whole snapshot rather than a delta. That is the difference
// between a push channel that repairs itself and one that does not: emitted
// events are best-effort, and a dropped delta leaves a session wrong forever
// where a dropped snapshot is superseded by the next one.
// to is the session the event is being delivered to. It decides whether the
// snapshot's originating event is a contributing edge for this delivery: it is
// one for the session whose click produced the version, and there is none to
// claim for anybody else watching the same store.
func SyncEvent(snap Snapshot, to live.ID) live.Event {
	var contributing []uint64
	if snap.ChangedByEvent != 0 && snap.ChangedBy == to {
		contributing = []uint64{snap.ChangedByEvent}
	}
	return live.Event{
		Name:         EventSync,
		Contributing: contributing,
		Fields: live.NewFields(map[string]string{
			fieldValue:     strconv.FormatInt(snap.Value, 10),
			fieldVersion:   strconv.FormatUint(snap.Version, 10),
			fieldTabs:      strconv.Itoa(snap.Tabs),
			fieldChangedAt: strconv.FormatInt(snap.ChangedAtUnixMilli, 10),
			fieldChangedBy: snap.ChangedBy.String(),
		}),
	}
}
