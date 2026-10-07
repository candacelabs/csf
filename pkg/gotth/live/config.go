package live

import (
	"context"
	"net/http"

	"go.opentelemetry.io/otel/metric"
	"go.opentelemetry.io/otel/trace"

	"log/slog"
)

// Config declares one live application: its state type, its mount hook, its
// reducer, its fragments, its effect executor, and its security hooks.
//
// The zero value is invalid, and New reports exactly which field is missing
// and what to set it to. It is a struct rather than a set of functional
// options because that is the standard library's shape for this — http.Server,
// tls.Config, net.Dialer — and because it makes the security configuration one
// object a reviewer can read at a glance.
//
// # What S has to be
//
// S is unconstrained at the type level and there is one rule about it that the
// compiler cannot state: Reduce must RETURN the next state rather than modify
// the one it was given. A value type gets this for free. A reference type —
// S = *Foo, a map, a slice — does not, and the failure is quiet in both
// directions:
//
//   - state_version rises exactly when state changed, and the library decides
//     that by comparing prev with next. For a reference S it cannot: == would
//     ask whether the two are the same object, and a reducer that mutated in
//     place and returned the same handle would answer yes. The library
//     therefore treats every transition of a reference S as a change, which
//     costs a render that may be suppressed and keeps the version honest.
//   - Fragment.Dirty is handed prev and next, and if the reducer mutated in
//     place they are the same value. The declaration then compares something
//     against itself, reports no change, and that region is never re-rendered.
//     Nothing can repair this from inside the library.
//
// So a pointer S is allowed and is not refused at construction — used purely it
// is perfectly correct — but it is the shape in which forgetting the rule is
// silent. The determinism helpers in live/livetest are what catch a reducer
// that has forgotten it: replay the same event log twice and the results
// diverge.
type Config[S any, I IIdentity] struct {
	// Init is the mount hook: it produces the session's initial state and any
	// startup effects, such as a pubsub subscription. It runs once per session,
	// as the first transition, before the first snapshot.
	//
	// Optional. Nil means the zero value of S, no startup effects and no error
	// — which is the only total, side-effect-free thing an unwritten mount hook
	// could mean, and it is what an application whose sessions all start empty
	// writes out by hand. Teardown, the hook on the other end of the same
	// session, has always been optional on the same argument.
	//
	// It is the ONE field New fills in rather than refusing, and the line
	// between it and the rest is that the rest cannot be guessed: a reducer, a
	// region, the set of accepted event names and the four security hooks are
	// each something only the application knows, and a library that picked for
	// them would be picking deny-by-default's opposite. Getting this one wrong
	// is also visible on the first run rather than in production — sessions
	// start empty, and so does the page, because [App.PageHandler] renders from
	// this same hook — where a guessed origin allowlist or a guessed
	// authorization rule would not be visible at all.
	//
	// [App.PageHandler] calls it once per page request as well, to render the
	// first paint from the state a session would start at. Init is therefore a
	// loader: it must be safe to call for a read. The effects it returns are
	// performed only for a real session; on a page render they are discarded.
	// See that method.
	Init func(ctx context.Context, session Session[I]) (S, []Effect[I], error)

	// Reduce is the pure state transition. Required.
	Reduce Reducer[S, I]

	// Fragments are the server-owned live regions. Required, non-empty, and
	// every ID must be unique.
	Fragments []Fragment[S]

	// Events are the event names this application accepts. Required.
	//
	// An event whose name is not here is refused with UNKNOWN_EVENT and
	// counted, never dispatched and never ignored: unknown input is
	// default-deny. Declaring the set up front is also what bounds the
	// cardinality of the per-event metric label before the first connection.
	Events []string

	// There is no Execute hook. It was here until 2026-09-03, taking one
	// effect interface and type-switching on its dynamic type to decide what to
	// do, and it went with that interface: [Effect.Run] is where an effect's
	// behaviour lives now, so there is nothing left for a central executor to
	// dispatch on. An application that had one moves each `case` arm into the
	// constructor that builds the effect, which is also where the store, broker
	// or pool it needs is already in scope.
	//
	// Everything that hook's signature carried, Run's carries: the session it
	// acts for, the [Emitter] that injects results back, and the error that
	// reaches the reducer as an [EffectFailedEvent] whose
	// EffectFailedErrorField holds its message verbatim, in production and
	// unredacted.

	// Teardown runs after the session actor exits, with the final state, for
	// unsubscribing. Optional.
	Teardown func(ctx context.Context, session Session[I], state S)

	// Origins is the allowlist of permitted Origin values, checked on the
	// upgrade request before any per-session memory is allocated. Required
	// unless it contains AnyOrigin. Deny by default: there is no wildcard, no
	// reflection of the request's own Origin, and no pass for a request that
	// sends none.
	Origins []string

	// Authenticate derives the session identity from the upgrade request.
	// Required; use Anonymous to opt out.
	Authenticate func(request *http.Request) (I, error)

	// Authorize runs before the reducer for every event, at the single
	// mailbox ingress, so a new event kind cannot skip it. Required; use
	// AllowAll to opt out.
	//
	// Returning nil allows the event. Returning a *DenyError rejects it
	// without closing the connection. Returning a *FatalDenyError rejects it
	// and closes the connection.
	Authorize func(ctx context.Context, session Session[I], event Event) error

	// CSRF validates a token bound to the authenticated application session.
	// Required; use NoCSRFCheck to opt out.
	CSRF func(request *http.Request) error

	// Limits are the resource bounds. Any zero field takes its documented
	// default.
	Limits Limits

	// Logger is the structured log sink. Nil disables library logging and the
	// provenance log with it, which makes the reverse lookup from a captured
	// patch back to its cause unavailable. The frames still carry the causal
	// chain either way; what is lost is the server-side index.
	Logger *slog.Logger

	// Metrics enables the full metric set with one field. Nil disables it, at
	// a cost of one predictable branch per call site.
	Metrics metric.MeterProvider

	// Tracer enables the full trace set with one field. The provider is taken
	// explicitly rather than read from the OpenTelemetry global, which is what
	// lets this library depend on the trace API submodule rather than the root.
	Tracer trace.TracerProvider

	// Dev turns on developer mode. It must be false in production.
	//
	// It does three things, and nothing else.
	//
	// # 1. Panic detail in the Error frame
	//
	// In
	// production such a frame carries a fixed generic message and the causal
	// identifiers, and nothing else; with Dev set, the same frame also carries
	// the panic value and its stack. The full stack is written to Logger at
	// error level in both modes — dev mode only puts it where a developer with
	// a browser open will see it (FR-23, checklist §5.9).
	//
	// It reaches both sites that produce an Error frame: a panicking reducer,
	// and a panicking fragment render or Dirty declaration. The third site
	// FR-23 names, a panicking effect, deliberately becomes an
	// EffectFailedEvent instead of a frame, and EffectFailedErrorField already
	// carries the panic value in production and in dev alike — see that
	// constant, because it is a disclosure path this field does not gate.
	//
	// What reaches the browser is bounded: protocol.md caps an error frame's
	// message at 512 bytes, so a long stack arrives truncated. The frame is a
	// pointer into the log, not a copy of it.
	//
	// # 2. The dev session inspector (FR-44, NFR-8)
	//
	// With Dev set, App.Handler serves the inspector's JavaScript under the
	// mount and (*App).InspectorScript renders the tag that loads it. With Dev
	// false the route answers 404 and the component writes nothing, which is
	// how NFR-8's "MUST NOT load in production builds" is enforced rather than
	// asserted. The inspector is a separate artifact and costs the shipped
	// runtime nothing at all: it reads the session's frames off the WebSocket
	// and there is no seam for it anywhere in client/runtime.js.
	//
	// The library still logs no HTMX ownership violation, in either mode:
	// RFC-0001 §10.3 chose a documented precedence rule over a server-side
	// scan of rendered HTML for hx-* attributes, so there is nothing
	// server-side to detect. Flagging an hx-* element inside an unpreserved
	// live fragment is the inspector's job, in the browser, where the element
	// actually is.
	//
	// # 3. Dev reload (FR-57)
	//
	// With Dev set, App.Handler serves the dev-reload client's JavaScript and
	// the build-identity route under the mount, and (*App).DevReloadScript
	// renders the tag that loads it; with Dev false all three write nothing or
	// answer 404. A rebuilt-and-restarted process then reloads the page by
	// itself, which is the only way a change outside a live fragment — the
	// page shell, the head, a fragment whose markup moved while its state did
	// not — ever reaches a browser that is already connected.
	//
	// It preserves nothing the process itself did not preserve. See
	// DevBuildID and docs/guide/dev-reload.md, both of which say so in more
	// detail than a field comment can.
	Dev bool

	// DevBuildID overrides the identity gotth-live uses to tell one build of
	// this application from another. It is read only when Dev is set.
	//
	// Leave it empty and the identity is derived, once per process and lazily,
	// from a SHA-256 of the running executable. That default is what makes a
	// restart that rebuilt nothing — a crash loop, a `docker compose restart`,
	// a rebuild of source that did not actually change — leave the page alone
	// and let the client runtime's own reconnect restore it, instead of
	// reloading the document out from under a developer who changed nothing.
	//
	// Set it when the derived value cannot work or is not what you mean: a
	// commit hash injected with -ldflags, a container image digest, or a
	// counter your own reload loop increments. Any value works as long as it
	// CHANGES when the code changes and does not change when it does not; a
	// constant string turns dev reload off without turning Dev off.
	//
	// It is validated at New whatever Dev is set to — at most 128 bytes, no
	// control bytes, no leading or trailing whitespace — because a field
	// checked in only one mode is a field that starts failing on the deploy
	// that flips the mode. The bounds are what the value has to survive: it is
	// rendered into a script tag and returned as the entire body of the poll
	// the browser makes.
	DevBuildID string
}

// AnyOrigin is the sentinel for Config.Origins that disables origin
// validation. It is a named, greppable value so that auditing every deployment
// which turned the check off is one search. Never use it outside local
// development.
const AnyOrigin = "*"

// AllowAll is a Config.Authorize implementation permitting every event. It is
// the explicit opt-out from per-event authorization.
//
// It carries the identity type parameter because the hook it satisfies does,
// and it must be INSTANTIATED at the call site — `live.AllowAll[Member]`, not
// `live.AllowAll`. Go infers type arguments for a generic function assigned to
// a variable of function type but not for one assigned to a composite
// literal's field, which is exactly how a Config is written. Naming the type is
// two words and the alternative is an error message about instantiation.
func AllowAll[I IIdentity](ctx context.Context, session Session[I], event Event) error { return nil }

// NoCSRFCheck is a Config.CSRF implementation performing no check. It is the
// explicit opt-out, and it is only safe when Config.Origins is a real
// allowlist, since the origin check is then the whole of the CSRF posture.
func NoCSRFCheck(request *http.Request) error { return nil }
