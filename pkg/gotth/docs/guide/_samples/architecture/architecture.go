// Package architecture is the compiled source for docs/guide/architecture.md.
//
// One application with every hook filled in, so that the page's central claim —
// which goroutine each piece of your code runs on, and what is waiting behind
// it — is attached to code that compiles rather than to a diagram.
package architecture

import (
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

const (
	// EventShout is the one event a browser may send.
	EventShout = "room.shout"

	// FragmentBoard is the one live region.
	FragmentBoard = "room.board"

	// FieldBody is the form field EventShout carries.
	FieldBody = "body"
)

// State is one session's state. It is per session and per connection: two tabs
// are two sessions with two of these, and neither can reach the other's.
//
// It is comparable, which is what lets the library tell a transition that
// changed state from one that did not, and suppress the patch for the second.
type State struct {
	Heard  int
	Notice string
}

// Room is state shared between sessions, and it is the application's, not the
// library's. Every session has its own goroutine, so anything reachable from
// more than one of them is yours to synchronise — which is why this has a
// mutex and State does not.
type Room struct {
	mu      sync.Mutex
	members map[live.ID]struct{}
	said    []string
}

// NewRoom returns an empty room.
func NewRoom() *Room { return &Room{members: map[live.ID]struct{}{}} }

// Join and Leave are called from Init and Teardown, both on the session's
// actor goroutine but from different sessions, so they lock.
func (r *Room) Join(id live.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.members[id] = struct{}{}
}

// Leave releases the registration Join took.
func (r *Room) Leave(id live.ID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.members, id)
}

// Said returns what the room has heard, for a spec to read.
func (r *Room) Said() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.said...)
}

// ShoutEffect is the I/O the reducer asks for rather than performs.
//
// Its Run is not on the actor goroutine, and that is the point: it is allowed
// to block, to call a database, to take as long as it takes. The session keeps
// handling events while it runs, and what it produces reaches the reducer as an
// ordinary event rather than as a return value. The source names the effect on
// every patch it causes and in every metric it moves: the origin source becomes
// "effect:room.shout".
func (r *Room) ShoutEffect(body string) live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: "room.shout",
		Run: func(ctx context.Context, sess live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			r.mu.Lock()
			defer r.mu.Unlock()
			r.said = append(r.said, sess.ID().String()+": "+body)
			return nil
		},
	}
}

// Reducer is the pure transition, and it runs on the session's actor goroutine.
//
// One goroutine owns this session's state and is the only writer, so the
// function it returns needs no mutex and gets none. What it may not do is the
// price of that: no I/O, no clock, no randomness, no logging, and no mutation
// of the state it was handed. It returns the work it wants done as a value.
func Reducer(room *Room) live.Reducer[State, live.AnonymousIdentity] {
	return func(s State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
		if ev.Name != EventShout {
			return s, nil
		}
		body := ev.Fields.Get(FieldBody)
		if body == "" {
			s.Notice = "say something first"
			return s, nil
		}
		s.Heard++
		s.Notice = ""
		return s, []live.Effect[live.AnonymousIdentity]{room.ShoutEffect(body)}
	}
}

// Authorize runs on the connection's read pump, ahead of the mailbox, and not
// on the session's actor goroutine.
//
// That placement is the security property: an event is rate limited, checked
// against the registered names and fragments, and authorized before it
// occupies a mailbox slot, so a refused event costs the session nothing. Its
// consequence is the one to remember while writing this hook — blocking in
// here stalls the whole connection, acknowledgements and heartbeats included,
// where blocking in a reducer would stall only the mailbox behind it.
func (r *Room) Authorize(_ context.Context, _ live.Session[live.AnonymousIdentity], ev live.Event) error {
	if len(ev.Fields.Get(FieldBody)) > 280 {
		return &live.DenyError{Reason: "that is too long for this room"}
	}
	return nil
}

// Config is the whole application, and the comment above each hook is the
// goroutine it runs on.
func Config(room *Room, origins []string) live.Config[State, live.AnonymousIdentity] {
	return live.Config[State, live.AnonymousIdentity]{
		// The session's actor goroutine, as the first transition, before the
		// first snapshot reaches the browser. A slow Init is a slow first
		// paint.
		Init: func(ctx context.Context, sess live.Session[live.AnonymousIdentity]) (State, []live.Effect[live.AnonymousIdentity], error) {
			room.Join(sess.ID())
			return State{}, nil, nil
		},

		// The session's actor goroutine, one event at a time, in order.
		Reduce: Reducer(room),

		// The session's actor goroutine, immediately after Reduce, for each
		// fragment whose Dirty said the transition could have changed it.
		Fragments: []live.Fragment[State]{{
			ID: FragmentBoard,
			Render: func(s State) templ.Component {
				return templ.ComponentFunc(func(_ context.Context, w io.Writer) error {
					_, err := fmt.Fprintf(w, "<p>%d heard. %s</p>", s.Heard, s.Notice)
					return err
				})
			},
			Dirty: func(prev, next State) bool {
				return prev.Heard != next.Heard || prev.Notice != next.Notice
			},
		}},

		Events: []string{EventShout},

		// The connection's read pump, before the mailbox.
		Authorize: room.Authorize,

		// A goroutine per effect, spawned by the actor after the transition
		// that returned it: ShoutEffect's own Run, above.

		// The actor's exit path, after the mailbox has drained.
		Teardown: func(_ context.Context, sess live.Session[live.AnonymousIdentity], _ State) { room.Leave(sess.ID()) },

		// The HTTP handler's goroutine, on the upgrade request, before any
		// per-session memory is allocated. These two are ordinary request
		// handling and may block like any handler.
		Origins:      origins,
		Authenticate: live.Anonymous,
		CSRF:         live.NoCSRFCheck,
	}
}
