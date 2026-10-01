// Package lifecycle is the compiled source for docs/guide/lifecycle-hooks.md.
//
// There are three hooks: mount, event, and teardown. This file is all three.
package lifecycle

import (
	"context"
	"sync"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// Topic is the application-owned pubsub the session subscribes to at mount and
// unsubscribes from at teardown.
type Topic struct {
	mu      sync.Mutex
	members map[live.ID]string
}

func NewTopic() *Topic { return &Topic{members: map[live.ID]string{}} }

func (t *Topic) Join(id live.ID, subject string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.members[id] = subject
}

func (t *Topic) Leave(id live.ID) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.members, id)
}

func (t *Topic) Len() int {
	t.mu.Lock()
	defer t.mu.Unlock()
	return len(t.members)
}

// WatchEffect is the subscription pump the mount hook schedules.
//
// It is a constructor over a concrete live.Effect[live.AnonymousIdentity]: a source, which provenance
// and metrics see, and a Run closing over the topic. The Run holds the session
// open for as long as the session lasts, which is what a subscription is, and
// returns when the library cancels it at shutdown.
func (t *Topic) WatchEffect() live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: "room.watch",
		Run: func(ctx context.Context, sess live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			// A real pump would select on the topic's own signal as well and
			// emit an event per update. This one has nothing to deliver, so it
			// demonstrates the half every pump has: return promptly on
			// cancellation, because the library waits for this goroutine at
			// shutdown.
			<-ctx.Done()
			return ctx.Err()
		},
	}
}

// State is one session's view.
type State struct {
	Me      live.ID
	Subject string
}

// Init is the mount hook. It runs once, as the first transition, before the
// first snapshot, and it returns two things: the session's initial state, and
// any startup effects.
//
// Registration happens here, synchronously, and the long-running pump is
// returned as an effect. The order matters: if the pump did the registering,
// a change published between the mount and the pump's first loop would be
// missed, and the session would render a value that was already stale.
//
// Values a mount needs from the upgrade request arrive through the context —
// that is the whole reason there is no Session.Request(), which would keep an
// *http.Request alive for the connection's life.
func Init(topic *Topic) func(ctx context.Context, session live.Session[live.AnonymousIdentity]) (State, []live.Effect[live.AnonymousIdentity], error) {
	return func(ctx context.Context, sess live.Session[live.AnonymousIdentity]) (State, []live.Effect[live.AnonymousIdentity], error) {
		subject := sess.Identity().Subject()
		topic.Join(sess.ID(), subject)
		return State{Me: sess.ID(), Subject: subject}, []live.Effect[live.AnonymousIdentity]{topic.WatchEffect()}, nil
	}
}

// Authorize is the event hook. It runs before the reducer for every event, at
// the single mailbox ingress, so a new event kind cannot skip it.
//
// Returning nil allows the event. A *live.DenyError rejects one event and
// leaves the connection open. A *live.FatalDenyError rejects it and closes the
// connection with UNAUTHORIZED (close code 4006). An error of any other shape
// is treated as a denial, because a hook that failed open by accident is the
// one failure mode an authorization hook must not have.
func Authorize(_ context.Context, sess live.Session[live.AnonymousIdentity], ev live.Event) error {
	if ev.Name == "room.purge" && sess.Identity().Subject() != "admin" {
		return &live.DenyError{Reason: "only an admin may purge the room"}
	}
	if ev.FragmentID == "" {
		return &live.FatalDenyError{Reason: "an event named no fragment, which no binding this application ships can produce"}
	}
	return nil
}

// Teardown is the unmount hook. It runs after the session actor has exited,
// with the final state, and it is where a subscription taken at mount is
// released.
//
// It is optional, and leaving it nil is what leaks. It is not called with a
// state value when Init itself failed, because there is no final state to hand
// over.
func Teardown(topic *Topic) func(context.Context, live.Session[live.AnonymousIdentity], State) {
	return func(_ context.Context, sess live.Session[live.AnonymousIdentity], _ State) {
		topic.Leave(sess.ID())
	}
}
