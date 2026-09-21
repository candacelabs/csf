// Package effects is the compiled source for
// docs/guide/effects-and-server-push.md.
//
// It is a counter shared by every session: a click returns an effect, the
// store applies it, and a per-session pump emits the result back into every
// subscribed session.
package effects

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

const (
	EventInc  = "count.inc"
	EventSync = "count.sync"

	FieldValue   = "value"
	FieldVersion = "version"

	// SourceApply and SourceWatch are what EffectSource returns. They reach
	// the wire as the origin source "effect:<name>" on every patch the effect
	// causes, and they are the value the failure event carries in
	// live.EffectFailedSourceField.
	SourceApply = "count.apply"
	SourceWatch = "count.watch"
)

// ApplyEffect asks the store to add Delta.
//
// An effect is a concrete live.Effect[live.AnonymousIdentity]: a source, which is what provenance and
// metrics see, and a Run, which is what the library performs at the actor
// boundary. Both constructors below close over the store, which is why this
// application has no central executor — an effect performs itself.

// ApplyEffect asks the store to change the shared counter by delta.
//
// cause is the event that asked. It is carried through so the emission the
// store produces can name it — see Pump.
func (s *Store) ApplyEffect(delta int64, cause uint64) live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: SourceApply,
		Run: func(ctx context.Context, sess live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			s.apply(delta)
			return nil
		},
	}
}

// WatchEffect subscribes this session to the store for as long as it lives.
//
// The live.Session[live.AnonymousIdentity] is a parameter of Run and not something to fish out of the
// context: an effect's identity is an input to what the effect does, and a
// signature that omitted it would invite the effect to carry addressing
// information the library already has.
func (s *Store) WatchEffect() live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: SourceWatch,
		Run: func(ctx context.Context, sess live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			return s.Pump(ctx, sess.ID(), emit)
		},
	}
}

// State is one session's view of the shared counter.
type State struct {
	Value   int64
	Version uint64
}

// Reducer never changes Value. It returns an effect and learns the result the
// same way every other session does, which is what "server-authoritative"
// means concretely and why two tabs cannot disagree.
//
// It is a constructor over the store because an effect carries its own Run, so
// a reducer that schedules one has to be able to build it. The transition still
// touches the store not at all.
func Reducer(store *Store) live.Reducer[State, live.AnonymousIdentity] {
	return func(s State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
		switch ev.Name {
		case EventInc:
			return s, []live.Effect[live.AnonymousIdentity]{store.ApplyEffect(1, ev.ID)}

		case EventSync:
			return applySync(s, ev), nil

		case live.EffectFailedEvent:
			// A failed or panicking effect arrives here as an ordinary event
			// rather than being logged and dropped, so the failure is replayable.
			return s, retryWatch(store, ev)
		}
		return s, nil
	}
}

// applySync folds a store snapshot in, and drops one older than the snapshot
// already held.
//
// Emitter delivery is best effort: a full mailbox drops the event and tells
// the effect so. That is harmless here because a sync carries the whole value
// rather than a delta — a dropped snapshot is superseded by the next one,
// where a dropped delta would leave this session permanently wrong.
func applySync(s State, ev live.Event) State {
	version, err := strconv.ParseUint(ev.Fields.Get(FieldVersion), 10, 64)
	if err != nil || version < s.Version {
		return s
	}
	value, err := strconv.ParseInt(ev.Fields.Get(FieldValue), 10, 64)
	if err != nil {
		return s
	}
	s.Value, s.Version = value, version
	return s
}

// retryWatch decides what to do about a failed effect.
//
// It re-subscribes only when the executor classified the failure as transient.
// Re-running a terminal failure re-runs whatever made it terminal, and the
// classification is a claim the code that performed the effect is in a
// position to make and this reducer is not. An absent or unreadable value
// parses as false, and unclassified is terminal.
func retryWatch(store *Store, ev live.Event) []live.Effect[live.AnonymousIdentity] {
	retryable, _ := strconv.ParseBool(ev.Fields.Get(live.EffectFailedRetryableField))
	if retryable && ev.Fields.Get(live.EffectFailedSourceField) == SourceWatch {
		return []live.Effect[live.AnonymousIdentity]{store.WatchEffect()}
	}
	return nil
}

// Store is the shared counter. It is the application's, not the library's.
type Store struct {
	mu      sync.Mutex
	value   int64
	version uint64
	subs    map[live.ID]chan struct{}
}

func NewStore() *Store { return &Store{subs: map[live.ID]chan struct{}{}} }

// Pump delivers snapshots to one session until its context is cancelled.
//
// It runs for the session's whole life, on a goroutine the library owns and
// waits for at shutdown, which is why it returns on cancellation rather than
// on a store signal it might never get.
func (s *Store) Pump(ctx context.Context, id live.ID, emit live.Emitter) error {
	wake := s.join(id)
	defer s.leave(id)

	refusals := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-wake:
			if err := emit(s.syncEvent()); err == nil {
				refusals = 0
				continue
			}
			// The mailbox was full, or the session is closing.
			refusals++
			if refusals >= 5 {
				// Transient by construction: neither a full mailbox nor a
				// session mid-shutdown is a property of this effect, so a
				// fresh subscription has every chance of working. That is
				// exactly the claim live.Retryable makes, and the reason this
				// code makes it rather than the reducer guessing.
				return live.Retryable(fmt.Errorf("effects: the session refused %d snapshots in a row", refusals))
			}
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(50 * time.Millisecond):
			}
		}
	}
}

// syncEvent is the event a snapshot travels in.
//
// live.NewFields is the only way to give an emitted event a payload, and the
// ordering it imposes is part of the contract: Fields is compared by value in
// the replay harness, so an unordered copy would fail a determinism check that
// had found nothing wrong.
//
// Event.ID and Event.At are left zero. Both are server-minted, and the Emitter
// rejects an event that sets either rather than silently replacing it.
func (s *Store) syncEvent() live.Event {
	s.mu.Lock()
	defer s.mu.Unlock()
	return live.Event{
		Name: EventSync,
		Fields: live.NewFields(map[string]string{
			FieldValue:   strconv.FormatInt(s.value, 10),
			FieldVersion: strconv.FormatUint(s.version, 10),
		}),
	}
}

// SyncEventFor is syncEvent with a contributing claim attached.
//
// Contributing is the one causal field an application sets rather than reads,
// and it exists because a fan-out through shared state splits the knowledge in
// two: the library knows the subscription was scheduled at mount, and only
// this application knows the value came from somebody's click. Naming that
// click here is what lets an operator holding the patch that changed the
// number reach the interaction that changed it.
//
// At most 64 identifiers, and they must be events of this session. The Emitter
// rejects a longer list with an error rather than truncating, so the effect
// learns about it and the reducer sees a deterministic failure.
func (s *Store) SyncEventFor(cause uint64) live.Event {
	ev := s.syncEvent()
	if cause != 0 {
		ev.Contributing = []uint64{cause}
	}
	return ev
}

func (s *Store) apply(delta int64) {
	s.mu.Lock()
	s.value += delta
	s.version++
	subs := make([]chan struct{}, 0, len(s.subs))
	for _, wake := range s.subs {
		subs = append(subs, wake)
	}
	s.mu.Unlock()

	for _, wake := range subs {
		select {
		case wake <- struct{}{}:
		default: // Already armed. Latest value wins.
		}
	}
}

func (s *Store) join(id live.ID) chan struct{} {
	wake := make(chan struct{}, 1)
	wake <- struct{}{} // Deliver the current value on subscribe.
	s.mu.Lock()
	s.subs[id] = wake
	s.mu.Unlock()
	return wake
}

func (s *Store) leave(id live.ID) {
	s.mu.Lock()
	delete(s.subs, id)
	s.mu.Unlock()
}
