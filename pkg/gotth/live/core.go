package live

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
)

// Reducer is the pure state transition at the centre of a live application.
//
// Given the same state and the same event it must return the same next state
// and the same effects, on every run and in every process. It must not perform
// I/O, read a clock or a random source, start a goroutine, touch a channel, or
// mutate the state it was given: time and generated identifiers arrive on the
// event, stamped at the boundary, and effects are returned as values for the
// library to perform.
//
// The no-mutation rule is not stylistic. It is what makes panic recovery free:
// if a reducer panics, the pre-transition state is still intact and correct, so
// the library simply keeps it.
//
// A reducer is called from the session's own goroutine and never concurrently
// with itself.
type Reducer[S any, I IIdentity] func(state S, ev Event) (S, []Effect[I])

// Fragment declares one server-owned live region and how to render it.
type Fragment[S any] struct {
	// ID is a stable identity matching ^[A-Za-z0-9_:.-]{1,64}$, unique within
	// an application. It is what a patch names, so changing it is a
	// client-visible change.
	ID string

	// Render produces this region's markup from state. It must be a pure
	// function of state: the same state must render byte-identical HTML,
	// across runs and across processes. The known hazard is ranging over a Go
	// map in a template; range a sorted slice instead.
	Render func(state S) templ.Component

	// Dirty optionally declares whether a transition may have changed this
	// fragment. Nil means "re-render on every transition", which is always
	// safe. Over-declaring costs a suppressed render; under-declaring is a
	// correctness bug, and livetest.AssertDirtyComplete is what catches it.
	Dirty func(prev, next S) bool

	// Children declares an ordered, session-local collection of nested regions.
	// Each child ID must start with ID + ":", be unique, and declare no children
	// of its own. Definitions remain fixed; membership is a pure projection of
	// state. Render must include every child, in this order, inside this region.
	//
	// Membership/order changes, snapshots, or this parent's Dirty returning true
	// render the complete parent. Otherwise only dirty children render and patch.
	// Set Dirty to the parent's structural projection; nil still means always.
	// Child Dirty functions receive the same previous/next application states.
	// Events may address only children in the last successfully sent render;
	// reducers must also reject members removed by a queued state transition.
	Children func(state S) []Fragment[S]
}

// Event is one inbound interaction, already past the refinement boundary and
// past authorization.
//
// A reducer receives it by value and must not retain it: Fields holds a copy
// of the wire data rather than an alias into it, but the copy is the session's.
type Event struct {
	// Name is the registered event name.
	Name string
	// FragmentID is the fragment whose markup raised the event.
	FragmentID string
	// Fields are the form values the event carried.
	Fields Fields
	// At is stamped at the actor boundary. A reducer reads it here rather
	// than calling a clock, which is what makes an event log replayable.
	//
	// On an event constructed for an [Emitter] it must be left zero: the
	// boundary stamps it, and a value set here is rejected rather than
	// silently replaced.
	At time.Time

	// ID is the server-minted causal identifier, session-scoped and
	// monotonic. It is zero for the transitions the server started on its
	// own, where the origin source names the cause instead.
	//
	// It is read-only in practice. On an event constructed for an [Emitter]
	// it must be left zero, and a non-zero value is rejected with an error
	// rather than dropped: causal identifiers are minted by the server so that
	// untrusted or mistaken input cannot forge provenance, and an application
	// does not need to set one — the library carries the edge from the event
	// that scheduled an effect to the patches the effect produces, and records
	// it in the patch's contributing events.
	ID uint64

	// Contributing names events of this session whose state changes this
	// event carries, and is the one causal field an application sets rather
	// than reads. It belongs on an event constructed for an [Emitter] and is
	// ignored anywhere else.
	//
	// It exists because an asynchronous fan-out through shared state splits
	// the knowledge in two. The library knows which event scheduled an effect;
	// only the application knows which event produced the value that effect is
	// now delivering, and on a shared store those are different events — the
	// subscription was scheduled at mount, the value came from a click. Listing
	// the click here is what lets an operator holding the patch that changed
	// the number reach the interaction that changed it.
	//
	// It is a contributing claim, never a causal one: the patch's own cause
	// stays the server-minted origin, and these identifiers land in the
	// patch's contributing events beside any the library added. Naming an
	// event of another session is not possible — identifiers are
	// session-scoped — and naming the wrong one of your own is an application
	// bug rather than a way to forge provenance.
	//
	// At most 64 identifiers. The [Emitter] rejects a longer list with an
	// error naming the field and the count, so the effect learns about it and
	// the reducer sees a deterministic effect failure; it is not truncated to
	// fit, because dropping provenance to save room is the failure the
	// coalescing flush exists to prevent. The number is not configurable: the
	// patch's contributing list is bounded by the protocol, this is one event's
	// share of it, and every identifier listed here is one the library may not
	// coalesce. If more than 64 events genuinely contributed to one emission,
	// the claim being made is about the whole session rather than about this
	// value, and the provenance an operator can act on is the narrower one.
	Contributing []uint64
}

// Fields are the form values carried by an event. It is read-only and holds no
// alias into wire data.
type Fields struct {
	fields []session.Field
}

// NewFields returns the Fields an application constructs for itself, ordered
// by key.
//
// Two callers need it and neither can reach the fields a browser sent. An
// [Emitter] injects an event from inside an effect, and a server-initiated
// transition that cannot carry data is a transition that cannot deliver
// anything a subscription learned — which is every pubsub push. And a
// determinism test builds the event log it replays, so without this
// livetest.ReplayN would be usable only by applications whose events carry no
// form values.
//
// The ordering is not cosmetic. A Go map has no iteration order, and Fields
// is compared by value in the replay harness, so an unordered copy would make
// a reducer that reads its fields fail a determinism check that has found
// nothing wrong.
func NewFields(fields map[string]string) Fields {
	if len(fields) == 0 {
		return Fields{}
	}
	out := make([]session.Field, 0, len(fields))
	for k, v := range fields {
		out = append(out, session.Field{Key: k, Value: v})
	}
	slices.SortFunc(out, func(a, b session.Field) int {
		return strings.Compare(a.Key, b.Key)
	})
	return Fields{fields: out}
}

// Get returns the value for a key, or the empty string. For the difference
// between an absent key and an empty value — an unchecked checkbox, most
// often — use Lookup.
func (f Fields) Get(key string) string {
	v, _ := f.Lookup(key)
	return v
}

// Lookup returns the value for a key and whether the key was present.
func (f Fields) Lookup(key string) (string, bool) {
	for _, kv := range f.fields {
		if kv.Key == key {
			return kv.Value, true
		}
	}
	return "", false
}

// Len returns the number of fields.
func (f Fields) Len() int { return len(f.fields) }

// All iterates the fields in wire order, stopping early if yield returns
// false.
func (f Fields) All(yield func(key, value string) bool) {
	for _, kv := range f.fields {
		if !yield(kv.Key, kv.Value) {
			return
		}
	}
}

// Effect is one unit of I/O the library performs at the actor boundary, on a
// goroutine of its own, after the transition that returned it has committed.
//
// It is a concrete struct and not an interface. Operator ruling, 2026-09-03:
// a reducer signature handing back a slice of one-method effect interfaces
// hands a caller a slice of abstractions where every element is one named thing
// this application decided to do, and CS-8's pass-through exemption covers
// third-party libraries only —
// this library is the repository's own, so its contract is the repository's
// choice. What used to be an implementation of a one-method interface is now
// a value with two fields: what to call it, and what it does.
//
// Both fields are the effect's whole content, and neither is optional:
//
//   - A zero Effect — no source and no Run — is INERT. It is dropped before it
//     reaches the boundary, exactly as a nil element of the old effect slice
//     was, so `append`ing a conditional effect that turned out not to apply
//     costs nothing and cannot execute anything.
//   - An Effect with a source and no Run is a MISTAKE, and it fails
//     deterministically rather than silently succeeding: an effect that never
//     runs is a change that never happens, and the reducer learns about it in
//     an [EffectFailedEvent] like any other failure.
//   - An Effect with a Run and no usable source is refused the same way, by
//     the same boundary check that has always refused an unusable
//     EffectSource: the source becomes an origin the wire cannot carry, so no
//     patch this effect caused could be sent.
//
// # What a test can assert on
//
// A function value cannot be compared, so a specification asserts on what the
// reducer *named* — the Source — rather than on a struct literal's fields.
// That is a real narrowing from the interface's plain-value contract and it is
// the price of the ruling: [livetest.ReplayN] compares the source sequence two
// runs produced, which catches a reducer that scheduled a different effect and
// no longer catches one that scheduled the same effect with a different
// argument. Effects worth distinguishing should therefore be worth naming
// distinctly.
type Effect[I IIdentity] struct {
	// Source names the effect for provenance and metrics, in the form
	// "package.action" — it becomes the origin source "effect:<name>" on every
	// patch the effect causes, and it is the value a failure event reports in
	// [EffectFailedSourceField].
	//
	// It is at most the protocol's origin-source budget less the library's
	// "effect:" prefix, and must match ^[a-z][a-z0-9_.:/-]*$. A source that
	// cannot name an origin is refused before Run is called.
	Source string

	// Run performs the effect. It is called once, on a goroutine the library
	// owns and waits for at shutdown, and it is the only place in an
	// application where I/O belongs: a reducer returns this value and performs
	// nothing.
	//
	// It receives the session the effect is acting for — an effect acts on a
	// session's behalf and its identity is an input to what it does — and the
	// [Emitter] that injects the results back into that session as events. A
	// returned error becomes an [EffectFailedEvent] the reducer handles; wrap
	// it in [Retryable] to classify it as transient. A panic is contained,
	// counted, and delivered as the same failure event, classified terminal.
	//
	// Everything the effect needs beyond those three is captured: this is a
	// closure over whatever the application owns — a store, a broker, a
	// connection pool — which is what makes a central executor unnecessary and
	// is why there is no longer a Config.Execute to type-switch in.
	Run func(ctx context.Context, session Session[I], emit Emitter) error
}

// inert reports whether this is the zero Effect, which is dropped rather than
// run or refused.
//
// The test is deliberately "both fields empty" rather than "no Run". An Effect
// that names itself and forgot its behaviour is the failure this library keeps
// naming — a change that never happens — so it is refused loudly at the
// boundary; an Effect that is nothing at all is the conditional that did not
// apply, and dropping it is what a nil element of the old effect slice did.
func (effect Effect[I]) inert() bool { return effect.Source == "" && effect.Run == nil }

// Emitter injects an event into the session that spawned an effect. It is
// passed to Config.Execute and is safe to call from the effect's goroutine.
//
// It returns an error when the session is saturated or closing, so an effect
// learns about backpressure rather than having its event vanish.
type Emitter func(event Event) error

// EffectFailedEvent is the name of the event a failed or panicking effect
// becomes, so that a reducer sees a deterministic failure rather than silence.
//
// It is not in Config.Events and must not be: registration is what makes a
// name sendable by a browser, and this one is minted by the library. A reducer
// handles it in the same switch as everything else, which is the whole point —
// a failure that arrives as an event is replayable, and one that arrives as a
// log line is not.
//
// It is spelled here because the constant the library emits lives under
// internal/ and an application cannot import it. Before this existed the only
// way to handle a failed effect was to hard-code the string, and the counter
// example duly hard-coded the wrong one — shipping a failure path that had
// never run.
const EffectFailedEvent = "gotth.effect_failed"

// The fields an EffectFailedEvent carries.
//
// EffectFailedRetryableField holds "true" only when the effect classified its
// own failure as transient with [Retryable]. Read it with strconv.ParseBool
// and take the error as false: an unreadable classification is an unclassified
// one, and unclassified is terminal.
const (
	// EffectFailedSourceField holds the EffectSource of the effect that failed.
	// It is a name the application chose, so it is the field that is safe to
	// render into a fragment.
	EffectFailedSourceField = "source"

	// EffectFailedErrorField holds the error's message, or the panic value.
	//
	// It is not redacted. The string is whatever the effect's error said, in
	// production, with no relation to Config.Dev — which is right for a
	// reducer, because a reducer is server code and an unredacted detail is
	// what makes a failure actionable. But it is also whatever an upstream
	// library chose to put in an error: a connection string, a query, an
	// internal hostname, a panic value with a type name in it.
	//
	// So rendering this field into a fragment publishes it to the browser.
	// Error frames carry a fixed generic message in production for exactly
	// this reason; this is a second path to the same disclosure, and the
	// library cannot apply the same discipline to it because only the
	// application knows what its own effects put in their errors. Branch on
	// it in the reducer, and render EffectFailedSourceField instead of it.
	//
	// Log it and count it somewhere that is not the reducer. FR-16 makes
	// logging application data I/O, and a reducer may not perform I/O: a log
	// call inside one is not replayable, so the same event log produces a
	// different sequence of records on every run and the determinism the
	// reducer is written for stops meaning anything. The homes that work are
	// Config.Execute, which is already at the actor boundary, and the
	// slog.Handler an application gives Config.Logger.
	//
	// Both paragraphs are here because their absence produced a deviation, and
	// the first one is worded as it is because its earlier wording produced the
	// same deviation a second time: docs/exceptions.md E-2. It opened by
	// telling the reader to log and count this field, and left the constraint
	// to a paragraph below, so a reader who stopped at the first sentence wrote
	// the logging reducer FR-16 forbids — which is what the sample on
	// docs/guide/error-handling.md did.
	EffectFailedErrorField = "error"

	// EffectFailedRetryableField holds the transient-or-terminal classification.
	EffectFailedRetryableField = "retryable"
)

// SlowClientEvent and ClientRecoveredEvent name the two events the library
// synthesizes into a session's own mailbox: SlowClientEvent when the outbound
// window fills, and ClientRecoveredEvent when an acknowledgement drains it
// again. Their values are "timer:slow_client" and "timer:client_recovered".
//
// Neither is ever accepted from a client, and neither belongs in Config.Events
// — registration is what makes a name sendable by a browser, and these two are
// minted by the library. A reducer handles them in the same switch as
// everything else, which is what keeps a degradation the application decided on
// replayable from the event log. Letting a reducer read the transport window
// instead would make it return different state for the same log under different
// network conditions.
//
// They are the application half of the defined degradation, and the ordering is
// worth knowing before writing the reducer: the library has already stopped
// emitting by the time SlowClientEvent arrives, so a notice set in response to
// it reaches the browser only once an acknowledgement re-opens the window.
//
// Each is declared as the internal constant the session actually synthesizes,
// so the module holds one spelling of each string rather than two that agree
// until somebody edits one. Godoc therefore shows a name from a package a
// reader cannot import, which is what the two quoted values above are for.
const (
	SlowClientEvent      = protocol.SourceSlowClient
	ClientRecoveredEvent = protocol.SourceClientRecovered
)

// Retryable marks an error returned from Config.Execute as transient, so that
// the failure event carries the classification and the reducer can decide to
// schedule another attempt.
//
// The unmarked default is terminal, deliberately. An effect may have committed
// externally before it failed — the message was published, the row was
// written — so retrying a failure nobody classified risks doing that twice,
// and retrying is a claim about idempotence that only the code which performed
// the effect is in a position to make. A failure never retried costs a change
// that does not happen, and shows up as a session that stops updating. A
// failure retried blindly costs a change that happens twice, and shows up as
// corrupt data somebody else owns. Between a visible omission and an invisible
// duplicate, the default belongs on the omission.
//
// Retryable(nil) is nil, so a result can be wrapped unconditionally. The mark
// survives wrapping with %w and is invisible in the error's message.
func Retryable(err error) error {
	if err == nil {
		return nil
	}
	return &session.RetryableError{Err: err}
}

// IsRetryable reports whether err carries the mark set by [Retryable].
//
// It is the symmetric partner of Retryable, and it exists because the library
// itself asks this question of an error it did not produce — the actor calls
// exactly this function to fill EffectFailedRetryableField — while an
// application holding the same error had no way to ask it. A setter whose mark
// nothing exported can read is a one-way door: a value can be created and not
// inspected.
//
// The reader most applications want is still the field on the failure event,
// because what a reducer holds is an event. This is for the code that holds
// the error: an executor deciding between its own retry and handing the
// decision up, and the spec that checks it decided correctly. Asserting on the
// mark by asking whether the error wraps anything — the workaround this
// replaces — is an assertion about wrapping standing in for one about
// classification, and it passes for any error that happens to wrap.
//
// IsRetryable(nil) is false, as is IsRetryable(Retryable(nil)), because
// Retryable(nil) is nil. The mark is found through errors.As, so it survives
// arbitrary wrapping with %w in either direction.
func IsRetryable(err error) bool { return session.IsRetryable(err) }

// DenyError rejects one event without closing the connection. The client is
// told the event was not permitted, no state changes, and the session
// continues.
type DenyError struct {
	// Reason is operator-facing. A generic message reaches the client in
	// production, because an authorization reason is an authorization input.
	Reason string
}

// Error renders the operator-facing reason. This string reaches a log, not a
// browser: the client is told a generic denial, because the reason an event was
// refused describes the authorization rule that refused it.
func (e *DenyError) Error() string {
	return fmt.Sprintf("gotth-live: event denied: %s", e.Reason)
}

// FatalDenyError rejects an event and closes the connection as unauthorized.
// Return it when the request is not merely disallowed but evidence that the
// session should not continue.
type FatalDenyError struct {
	// Reason is operator-facing, as for DenyError.
	Reason string
}

// Error renders the operator-facing reason and says that the connection is
// going with it, so a log line distinguishes this from the survivable denial
// without the reader having to know which type produced it.
func (e *FatalDenyError) Error() string {
	return fmt.Sprintf("gotth-live: event denied, closing the connection: %s", e.Reason)
}

// ID is a session identifier: sixteen bytes minted by the server, carried in
// every frame in both directions so that one patch captured in isolation is
// resolvable.
type ID [16]byte

// String returns the lower-case hex form, which is what appears in logs, span
// attributes and the provenance stream.
func (id ID) String() string { return session.ID(id).String() }

// IIdentity is what this library needs from an application's identity, and
// since 2026-09-03 it appears in exactly two positions: as the CONSTRAINT on
// the type parameter every generic declaration here carries, and as the
// parameter type of the internal admission bookkeeping that calls Subject().
//
// It is never a return type and never a field a getter hands back. Operator
// ruling, 2026-09-03, on the signature this replaces:
//
//	func (s Session) Identity() IIdentity { return s.identity }
//	RETURN TYPE IS IIDENTITY FUCK YOU
//
// That signature was the last "application-type pass-through" exemption in the
// library: the concrete type genuinely lives in the caller's package and this
// package cannot name it — which is the existential case, and generics are what
// Go has instead of existential types. The type parameter is the answer, and
// the assertion every application used to write, `sess.Identity().(Member)`, is
// now a compile-time fact.
type IIdentity interface {
	// Subject returns a stable, non-secret identifier, used for logging and
	// for per-identity session limits. It must not be a token.
	Subject() string
}

// Session identifies one live connection, typed by the identity the
// application's own Authenticate hook produced. It is passed to Config.Init,
// Config.Authorize, Config.Teardown and every [Effect.Run], and is safe to
// copy.
//
// I is the application's own identity type — a struct it declared, not an
// interface — so [Session.Identity] hands back the thing rather than an
// abstraction over it. An application with no meaningful identity instantiates
// on [AnonymousIdentity], which is a small concrete struct for exactly that.
type Session[I IIdentity] struct {
	id       ID
	identity I
}

// ID returns the session's identifier.
func (s Session[I]) ID() ID { return s.id }

// Identity returns the identity bound at the handshake, as the application's
// own type. No assertion, and nothing to get wrong.
func (s Session[I]) Identity() I { return s.identity }
