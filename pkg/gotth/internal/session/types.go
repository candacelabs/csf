package session

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/candacelabs/csf/pkg/gotth/internal/render"
)

// ID is a session identifier: sixteen random bytes minted by the server at
// handshake. It is server-minted so that untrusted input can never name
// another session, and sixteen bytes wide so a patch frame captured in
// isolation is resolvable.
type ID [16]byte

// String returns the lower-case hex form used in logs and span attributes.
func (id ID) String() string { return hex.EncodeToString(id[:]) }

// IIdentity is what this package needs from an application's identity, and it
// appears only as a CONSTRAINT and in the admission bookkeeping that calls
// Subject(). Since 2026-09-03 the identity itself travels as a type parameter,
// so nothing here returns one or hands one back through a field.
type IIdentity interface {
	// Subject returns a stable, non-secret identifier used for logging and
	// per-identity session limits.
	Subject() string
}

// Effect is one unit of I/O the actor performs at its boundary.
//
// It mirrors the public live.Effect, which is a concrete struct by operator
// ruling of 2026-09-03, and it is a second declaration rather than an alias
// because Run is in THIS package's vocabulary: the public signature speaks of a
// live.Session and a live.Emitter, neither of which an internal package can
// name. live's own adapter re-expresses one as the other, once, and that is the
// only translation between the two.
type Effect[I IIdentity] struct {
	// Source names the effect for provenance and metrics. It is refined before
	// the effect runs, because it becomes half of an Origin.source.
	Source string

	// Run performs the effect, on the goroutine spawn starts for it.
	//
	// scheduledBy is the identifier of the event whose transition returned this
	// effect, or zero when the server started the transition itself. It is
	// handed over rather than re-derived because FR-58 requires every
	// library-produced error to name the causal identifier where one exists,
	// and the errors the public adapter raises against an emitted event are
	// raised before any identifier of their own is minted.
	//
	// A nil Run is a mistake rather than a no-op, and execute refuses it with a
	// failure event: an effect that never runs is a change that never happens.
	Run func(ctx context.Context, p Peer[I], scheduledBy uint64, emit Emit) error
}

// Peer is the immutable pair a session is bound to.
type Peer[I IIdentity] struct {
	// ID is the sixteen server-minted bytes naming this session, carried on
	// every frame in both directions.
	ID ID

	// Identity is the authenticated principal, derived once from the upgrade
	// request, as the application's own type. It never changes for the life of
	// the session.
	Identity I
}

// Field is one form value carried by an event.
type Field struct {
	// Key is the form field's name, as the browser sent it.
	Key string

	// Value is its value, already past the refinement boundary — length
	// bounded and valid UTF-8 — and past authorization, but otherwise
	// untrusted application input.
	Value string
}

// Event is one input to a reducer, already past the refinement boundary and
// past authorization.
//
// ID and At are stamped at the actor boundary, never read by a reducer from a
// clock or a random source: that is what makes an event log replay to a
// byte-identical result.
type Event struct {
	// ID is the server-minted causal root. It is zero for the transitions the
	// server started on its own, where the origin source names the cause.
	ID uint64
	// ClientRef is the client's own correlation handle, echoed back so the
	// browser can match a patch to the interaction that caused it.
	ClientRef uint64
	// SeenServerSeq is the causation edge: the sequence number of the last
	// patch the user had applied when they acted.
	SeenServerSeq uint64

	// Name is the event name the application registered, such as
	// "cart.add". An unregistered name never reaches here: it is refused at
	// ingress and counted.
	Name string

	// FragmentID is the live region the interaction happened in, empty when
	// the server started the transition itself.
	FragmentID string

	// At is stamped at the actor boundary, not read by the reducer from a
	// clock. That is what lets an event log replay to a byte-identical result.
	At time.Time

	// Fields are the form values the interaction carried, in the order they
	// arrived.
	Fields []Field

	// Contributing lists events in this session whose state changes this
	// event carries. It is only ever set on an event an effect emits, where
	// the application is the only party that knows the edge — the library
	// knows which event scheduled an effect, but not which event caused the
	// value an asynchronous fan-out is delivering.
	Contributing []uint64
}

// EffectFailedEvent is the name of the event a failed effect is turned into,
// so that a reducer sees a deterministic failure rather than silence.
//
// It is spelled again in package live, which is where an application can reach
// it; live's own suite asserts the two are equal, because a reducer that
// matches on the wrong string handles nothing and looks like it handles
// something.
const EffectFailedEvent = "gotth.effect_failed"

// The fields an EffectFailedEvent carries: which effect failed, what it said,
// and whether the failure was classified as transient.
const (
	EffectFailedSourceField    = "source"
	EffectFailedErrorField     = "error"
	EffectFailedRetryableField = "retryable"
)

// RetryableError marks an effect's failure as transient, so the failure event
// says so and a reducer can decide to schedule another attempt.
//
// It carries no message of its own. The classification travels as its own
// field on the event, and prefixing the error text would put the same fact in
// two places — one of which a reducer would then be tempted to parse.
type RetryableError struct {
	// Err is the failure being classified. It is never nil: Retryable(nil) is
	// nil rather than a marked nil.
	Err error
}

// Error is the wrapped error's message, unchanged. The classification is not
// prefixed onto it deliberately — it travels as its own event field, and a
// message that also carried it would invite a reducer to parse the string.
func (e *RetryableError) Error() string { return e.Err.Error() }

// Unwrap exposes the underlying failure, so the mark survives errors.Is and
// errors.As in both directions.
func (e *RetryableError) Unwrap() error { return e.Err }

// IsRetryable reports whether a failure was explicitly classified as transient.
//
// Unclassified is terminal, and that direction is the whole point. An effect
// may have committed externally before it failed, so re-running one nobody
// classified risks duplicating a side effect that already happened; retrying
// is a claim about idempotence, and only the code that performed the effect
// can make it. A failure never retried costs a change that does not happen. A
// failure retried blindly costs one that happens twice.
//
// errors.As rather than a type assertion, so the mark survives being wrapped
// by helpers between the effect that set it and the actor that reads it. It is
// exported out of this package because live.IsRetryable is this function and
// not a second copy of it: a predicate an application can call and a predicate
// the library decides on must not be two implementations that can disagree.
func IsRetryable(err error) bool {
	var marked *RetryableError
	return errors.As(err, &marked)
}

// DenyError rejects one event without closing the connection.
type DenyError struct {
	// Reason is operator-facing. The client is told a generic denial, because
	// the reason an event was refused describes the rule that refused it.
	Reason string
}

// Error renders that operator-facing reason, for the log.
func (e *DenyError) Error() string {
	return fmt.Sprintf("gotth-live: event denied: %s", e.Reason)
}

// FatalDenyError rejects an event and closes the connection.
type FatalDenyError struct {
	// Reason is operator-facing, as for DenyError.
	Reason string
}

// Error renders the reason and says the connection is going with it, so a log
// line distinguishes this from the survivable denial without the reader having
// to know which type produced it.
func (e *FatalDenyError) Error() string {
	return fmt.Sprintf("gotth-live: event denied, closing the connection: %s", e.Reason)
}

// Emit injects an event into the session that spawned an effect.
type Emit func(event Event) error

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
