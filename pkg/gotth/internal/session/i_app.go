package session

import (
	"context"

	"github.com/candacelabs/csf/pkg/gotth/internal/render"
)

// IApp is the application behaviour the actor drives.
//
// It is an interface with one implementation, which this library otherwise
// refuses, and the reason it earns its place is the type parameter: the public
// package is generic over the application's state type and the actor holds
// state as an opaque value, so something has to be the seam where the type
// assertion happens exactly once. That seam is this interface, and every
// method of it is called only from the actor goroutine except Authorize.
type IApp[I IIdentity] interface {
	// Init produces the session's initial state and any startup effects. It
	// runs once, as the first transition, before the first snapshot.
	Init(ctx context.Context, p Peer[I]) (state any, effects []Effect[I], err error)

	// Authorize runs before the reducer for every event, at the single
	// mailbox ingress. It is the one method called from the read pump rather
	// than from the actor goroutine, because refusing an event before it
	// occupies a mailbox slot is the entire point of where it sits.
	Authorize(ctx context.Context, p Peer[I], ev Event) error

	// Reduce is the pure state transition.
	Reduce(state any, ev Event) (any, []Effect[I])

	// Teardown runs after the actor exits, with final state.
	Teardown(ctx context.Context, p Peer[I], state any)

	// Registry returns the application's fragments.
	Registry() *render.Registry

	// Registered reports whether an event name is declared. An unregistered
	// name is refused and counted, never dispatched and never ignored.
	Registered(name string) bool

	// StateComparable reports whether the application's state type may be
	// compared with == to decide whether a transition changed anything.
	//
	// It is false for a type Go cannot compare at all, and — the part that is
	// not obvious — for one Go compares only by IDENTITY: a pointer, map,
	// slice, channel, function or interface. Those are comparable in Go's
	// sense, so a naive type switch takes the fast path on them and asks "is
	// this the same object", which a reducer that mutates in place and returns
	// the same pointer answers "yes" to. That is the ordinary Go mistake the
	// purity rule exists to forbid, and answering it wrongly freezes
	// state_version and makes P4 false (BR-7).
	//
	// It is a method rather than a value the actor derives because deriving it
	// costs reflection over the state type, and the type does not change for
	// the life of an application: the answer is computed once, where the type
	// is still a type parameter and not an opaque any.
	StateComparable() bool
}
