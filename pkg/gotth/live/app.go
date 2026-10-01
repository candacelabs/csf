package live

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"sync"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/internal/obs"
	"github.com/candacelabs/csf/pkg/gotth/internal/protocol"
	"github.com/candacelabs/csf/pkg/gotth/internal/render"
	"github.com/candacelabs/csf/pkg/gotth/internal/session"
	"github.com/candacelabs/csf/pkg/gotth/internal/wsx"
)

// App is a validated live application. It is safe for concurrent use, and one
// App serves any number of sessions.
type App[S any, I IIdentity] struct {
	cfg          Config[S, I]
	handler      *wsx.Handler[I]
	routeHandler http.HandlerFunc

	// logger is the same sink the session actor writes to, held here so that
	// the request-scoped routes this type serves itself — PageHandler's
	// failures — reach an operator by the path everything else does. A nil
	// *obs.Logger is the disabled configuration and every method tests its
	// receiver, so this needs no branch at the call sites.
	logger *obs.Logger

	// The FR-57 build identity, derived at most once and only if something
	// asks for it. Nothing asks for it unless Config.Dev is set, so a
	// production binary never hashes its own executable. See devBuildID.
	buildOnce sync.Once
	buildID   string
}

// New validates a Config and returns a mounted application.
//
// It reports a *ConfigError naming the offending field for a missing hook, a
// missing or duplicated fragment identifier, an unregistered event name, or an
// application that returns effects with no executor. Failing here rather than
// at the first connection is deliberate: every one of those is a startup
// mistake, and finding it at startup is the difference between a failed deploy
// and a session that misbehaves in production.
//
// The one field New fills in rather than refusing is [Config.Init]; see that
// field for the default and the argument for it. Everything else a Config must
// state, it must state.
func New[S any, I IIdentity](cfg Config[S, I]) (*App[S, I], error) {
	if err := validate(cfg); err != nil {
		return nil, err
	}

	// Resolved once, here, so that everything below — the session adapter, the
	// actor, and PageHandler — calls one non-nil hook rather than each testing
	// for nil in its own way and disagreeing about what nil meant.
	if cfg.Init == nil {
		cfg.Init = zeroInit[S, I]
	}

	frags := make([]render.Fragment, len(cfg.Fragments))
	for i, f := range cfg.Fragments {
		frags[i] = fragmentAdapter(f)
	}
	reg, err := render.NewRegistry(frags)
	if err != nil {
		return nil, &ConfigError{Field: "Fragments", Detail: err.Error()}
	}

	events := make(map[string]struct{}, len(cfg.Events))
	for _, name := range cfg.Events {
		events[name] = struct{}{}
	}

	metrics, err := obs.NewMetrics(cfg.Metrics)
	if err != nil {
		return nil, &ConfigError{Field: "Metrics", Detail: err.Error()}
	}

	behaviour := &appAdapter[S, I]{cfg: cfg, reg: reg, events: events, comparable: comparableState[S]()}
	app := &App[S, I]{cfg: cfg, logger: obs.NewLogger(cfg.Logger)}

	app.handler, err = wsx.NewHandler(wsx.Options[I]{
		Origins:                cfg.Origins,
		Authenticate:           cfg.Authenticate,
		CSRF:                   cfg.CSRF,
		NewApp:                 func(request *http.Request) session.IApp[I] { return behaviour },
		Limits:                 cfg.Limits.internal(),
		Metrics:                metrics,
		Tracer:                 obs.NewTracer(cfg.Tracer),
		Logger:                 obs.NewLogger(cfg.Logger),
		Dev:                    cfg.Dev,
		MaxSessions:            cfg.Limits.MaxSessions,
		MaxSessionsPerIdentity: orDefault(cfg.Limits.MaxSessionsPerIdentity, DefaultLimits().MaxSessionsPerIdentity),
	})
	if err != nil {
		return nil, &ConfigError{Field: "Origins", Detail: err.Error()}
	}

	app.routeHandler = app.routes()
	return app, nil
}

// MustNew is New for a caller that has nowhere to put the error: it returns the
// application, or panics with the *ConfigError New would have returned.
//
// It is for main and for package-level initialisation, which is where a Config
// is a literal somebody wrote and every failure New can report is a mistake in
// that literal — a missing hook, a duplicate fragment identifier, an event name
// the protocol cannot carry, a limit outside its range. A process that cannot
// construct its own application has nothing to serve, so the choice at such a
// call site is between panicking and printing the same message before exiting,
// and this spells the first in one line rather than four. template.Must and
// regexp.MustCompile are the same helper for the same reason and this follows
// their naming.
//
// The panic value is the error itself, so what a reader sees is the
// *ConfigError naming the field and what to set it to, above a stack naming the
// Config it came from. Nothing is lost but the choice of what to do next.
//
// Use New anywhere that choice exists: a server composing applications, a test
// that expects a rejection, anything building a Config out of configuration
// rather than out of source.
func MustNew[S any, I IIdentity](cfg Config[S, I]) *App[S, I] {
	app, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return app
}

// zeroInit is the default Config.Init: the zero value of S, no startup effects
// and no error. See Config.Init for why the field is optional and this is what
// it defaults to.
func zeroInit[S any, I IIdentity](ctx context.Context, session Session[I]) (S, []Effect[I], error) {
	var zero S
	return zero, nil, nil
}

func orDefault(v, fallback int) int {
	if v == 0 {
		return fallback
	}
	return v
}

func validate[S any, I IIdentity](cfg Config[S, I]) error {
	switch {
	// Config.Init is deliberately absent from this switch: it is optional, and
	// New substitutes zeroInit for a nil one. Everything below is a field with
	// no defensible default — a reducer, a region, an event name and the four
	// security hooks are all things only the application can say.
	case cfg.Reduce == nil:
		return &ConfigError{Field: "Reduce", Detail: "set the reducer that advances state"}
	case len(cfg.Fragments) == 0:
		return &ConfigError{Field: "Fragments", Detail: "declare at least one live region"}
	case len(cfg.Events) == 0:
		return &ConfigError{Field: "Events", Detail: "declare the event names this application accepts; unknown names are refused"}
	case len(cfg.Origins) == 0:
		return &ConfigError{Field: "Origins", Detail: "list the allowed Origin values, or set live.AnyOrigin for local development"}
	case cfg.Authenticate == nil:
		return &ConfigError{Field: "Authenticate", Detail: "set an authentication hook, or live.Anonymous to opt out"}
	case cfg.Authorize == nil:
		return &ConfigError{Field: "Authorize", Detail: "set a per-event authorization hook, or live.AllowAll[YourIdentity] to opt out"}
	case cfg.CSRF == nil:
		return &ConfigError{Field: "CSRF", Detail: "set a CSRF hook, or live.NoCSRFCheck to opt out"}
	}
	for i, name := range cfg.Events {
		if name == "" {
			return &ConfigError{Field: "Events", Detail: "Events[" + strconv.Itoa(i) + "] is empty; every event needs a name"}
		}
		// BR-2. The library namespaces a registered name as
		// "event:" + name onto the Origin.source of every patch that event
		// causes, and Origin.source is refined on the outbound boundary. A name
		// that cannot be namespaced is well-formed client traffic against a
		// well-formed registration whose every patch is unsendable: the state
		// change never reaches the browser, the client gets an
		// Error{INTERNAL} it cannot act on, and a metric documented as "never a
		// client problem" is incremented by ordinary input. That is D-18's
		// shape one field over from where D-18 closed it, and a registration
		// mistake belongs at startup rather than at the first click.
		//
		// The charset half is almost never the failure — Event.name's own
		// predicate is a subset of Origin.source's — so what this really catches
		// is the length, and the message leads with it.
		if !protocol.ValidOriginSource(protocol.SourceEventPrefix + name) {
			return &ConfigError{
				Field: "Events",
				Detail: fmt.Sprintf(
					"Events[%d] is %q (%d bytes); the library namespaces an event name as %q+name onto "+
						"the origin of every patch it causes, and that value is bounded at %d bytes and "+
						"must match ^[a-z][a-z0-9_.:/-]*$, so this name is at most %d bytes long: shorten it",
					i, name, len(name), protocol.SourceEventPrefix,
					protocol.MaxOriginSource, protocol.MaxOriginSource-len(protocol.SourceEventPrefix)),
			}
		}
	}
	if err := validateDevBuildID(cfg.DevBuildID); err != nil {
		return err
	}
	// Last, because a missing hook is a more useful first thing to be told
	// about than a limit that is out of range. Until D-14 this function
	// inspected no Limits field at all, which is how a CoalesceFlushAt above
	// the protocol ceiling reached the actor and turned the flush trigger into
	// an emission failure.
	return cfg.Limits.validate()
}

// Handler returns the http.Handler serving the live connection and the client
// runtime. It is mountable under any router at any prefix, and it holds no
// assumption at all about the path it is mounted at, because it routes by path
// SUFFIX: the four asset names are matched against the end of r.URL.Path and
// everything else is the upgrade.
//
// So do NOT wrap it in http.StripPrefix, at any prefix. Stripping is what makes
// a subtree pattern answer the upgrade with a 307 to the trailing-slash form,
// and a WebSocket client cannot follow a redirect on an upgrade — the page
// loads, the socket never opens, and the runtime retries forever. Register both
// the exact pattern and the subtree instead; docs/quickstart.md §2 has the
// measured table of all four mountings.
//
// Tell [Script] the same prefix, so the page points the browser back at wherever
// it was mounted. It is a parameter because this handler cannot supply it: it
// never learns where it was mounted, which is the same property that makes
// stripping unnecessary.
//
// # The live route returns at the upgrade, and the session outlives the request
//
// ServeHTTP RETURNS once the WebSocket handshake completes; the session then
// runs on a goroutine this package owns, for as long as the connection lasts.
// It does NOT block for the life of the session, which is what most WebSocket
// handlers do and what this one used to do. Three consequences a caller can
// observe, all deliberate:
//
//   - Middleware wrapping this handler completes at the upgrade rather than at
//     the end of the session. A request-scoped logger, timer or metric records
//     a handshake, not a connection — which is the honest boundary for a
//     request that became a connection, and the one that lets a request timeout
//     mean what it says.
//   - The session does not observe the REQUEST context's cancellation. It runs
//     under [context.WithoutCancel], so values an application or its middleware
//     put on the request context still resolve for the session's whole life,
//     and cancelling the request no longer ends it. In practice nothing
//     changes: that cancellation used to fire when ServeHTTP returned, which
//     was the end of the session.
//   - Close is how a session is ended from outside. There is no request to
//     cancel.
//
// The reason is memory, and it is measured: net/http holds a *conn — with two
// 4 KiB bufio buffers, a *response carrying a third, and the *Request — for as
// long as its handler has not returned, and a hijack means none of it goes back
// to net/http's pools. Under a blocking handler that is per-session memory held
// for hours. See docs/bench/g2-baseline.md.
func (a *App[S, I]) Handler() http.Handler { return a.routeHandler }

// ActiveConnections reports this application's registered WebSocket connections,
// including connections still cleaning up. It counts neither users nor tabs;
// reconnects can briefly overlap. It is safe to call concurrently with Close.
func (a *App[S, I]) ActiveConnections() int { return a.handler.Sessions() }

// Close drains every session, closing each with the going-away code, and waits
// for in-flight effects up to the context's deadline.
//
// "Every session" is exact and is held by a spec rather than by this sentence
// (C-34): a connection that has been admitted but not yet registered when Close
// begins is REFUSED and closed with the going-away code rather than being
// allowed to start, so there is no interval in which Close returns nil over a
// session it did not touch. When Close returns nil, no session remains
// registered and every client that had one has been sent a close frame.
//
// Close returns an error if the context's deadline passes before in-flight
// effects finish draining. It does not wait for a client to answer the close
// handshake beyond that deadline.
//
// After Close, the handler refuses new upgrades. It is not reusable.
func (a *App[S, I]) Close(ctx context.Context) error { return a.handler.Close(ctx) }

// routes adds asset dispatch to the existing WebSocket handler. The host owns
// routing and path canonicalization; this wrapper creates no router or listener.
func (a *App[S, I]) routes() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// FR-57's two routes are first, and the inspector's before the
		// runtime's, because all four JavaScript names end in ".min.js" and
		// only one ordering is safe to reason about. They do not actually
		// collide — none of the longer names ends in "gotth-live.min.js" —
		// but a future rename could make them collide, and the more specific
		// name being tested first is what makes that a non-event.
		//
		// The build-identity route is the exception that needs no ordering
		// care at all: it carries no extension, so no suffix test here can
		// reach it by accident.
		if strings.HasSuffix(r.URL.Path, devBuildRoute) {
			if !a.devOnly(w, "the dev-reload build identity") {
				return
			}
			a.serveDevBuild(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, clientDevReloadFile) {
			if !a.devOnly(w, "the dev-reload client") {
				return
			}
			serveClientDevReload(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, clientInspectorFile) {
			if !a.devOnly(w, "the dev session inspector") {
				return
			}
			serveClientInspector(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, clientRuntimeFile) {
			serveClientRuntime(w, r)
			return
		}
		a.handler.ServeHTTP(w, r)
	}
}

// devOnly is the server-side half of every dev-only gate: it reports whether
// Config.Dev is set, and answers 404 naming the switch when it is not.
//
// PRD NFR-8 says the inspector must not load in production builds and FR-57's
// dev reload is held to the same standard here. Each of the three dev routes
// has a second, independent gate in the component that would name it — a
// production page carries no tag at all — and this is the one that holds when
// a bookmark, a cached HTML document or a scanner asks for the path directly.
//
// The 404 names Config.Dev rather than being blank because the developer
// meeting it has almost always forgotten to set it, and FR-58 says a library
// error names the actionable next step.
func (a *App[S, I]) devOnly(w http.ResponseWriter, what string) bool {
	if a.cfg.Dev {
		return true
	}
	http.Error(w, "gotth-live: "+what+" is served only when live.Config.Dev is true", http.StatusNotFound)
	return false
}

// appAdapter is the one place the application's state type is asserted back
// out of the opaque value the actor holds. Everything below this line works in
// terms of the internal vocabulary; everything above it in terms of the
// exported one.
type appAdapter[S any, I IIdentity] struct {
	cfg    Config[S, I]
	reg    *render.Registry
	events map[string]struct{}

	// comparable is comparableState[S](), resolved at construction because it
	// is a property of the type and the actor holds only values.
	comparable bool
}

func (a *appAdapter[S, I]) Init(ctx context.Context, p session.Peer[I]) (any, []session.Effect[I], error) {
	state, effects, err := a.cfg.Init(ctx, sessionOf(p))
	if err != nil {
		return nil, nil, err
	}
	return state, toInternalEffects(effects), nil
}

func (a *appAdapter[S, I]) Authorize(ctx context.Context, p session.Peer[I], ev session.Event) error {
	err := a.cfg.Authorize(ctx, sessionOf(p), eventOf(ev))
	if err == nil {
		return nil
	}

	// The two denial shapes are translated rather than passed through, so the
	// actor can distinguish them without naming a type from this package.
	var fatal *FatalDenyError
	if errors.As(err, &fatal) {
		return &session.FatalDenyError{Reason: fatal.Reason}
	}
	var deny *DenyError
	if errors.As(err, &deny) {
		return &session.DenyError{Reason: deny.Reason}
	}
	// An error of any other shape is a denial too. Treating an unrecognised
	// error as an allow would make a hook that fails open by accident, which
	// is the one failure mode an authorization hook must not have.
	return &session.DenyError{Reason: err.Error()}
}

func (a *appAdapter[S, I]) Reduce(state any, ev session.Event) (any, []session.Effect[I]) {
	next, effects := a.cfg.Reduce(state.(S), eventOf(ev))
	return next, toInternalEffects(effects)
}

func (a *appAdapter[S, I]) Teardown(ctx context.Context, p session.Peer[I], state any) {
	if a.cfg.Teardown == nil {
		return
	}
	s, ok := state.(S)
	if !ok {
		// The mount hook failed, so there is no final state to hand over.
		return
	}
	a.cfg.Teardown(ctx, sessionOf(p), s)
}

func (a *appAdapter[S, I]) Registry() *render.Registry { return a.reg }

func (a *appAdapter[S, I]) Registered(name string) bool {
	_, ok := a.events[name]
	return ok
}

func (a *appAdapter[S, I]) StateComparable() bool { return a.comparable }

// comparableState answers, once per application rather than once per
// transition, whether == over two values of S means "these are the same state".
//
// Two ways it can be false, and the second is the one BR-7 was about.
//
// The obvious one is a type Go refuses to compare — a struct with a slice
// field, say — where == panics. The other is a type Go compares by IDENTITY: a
// pointer, map, slice, channel, function or interface. Those are comparable in
// Go's sense, so a reflect.Type.Comparable test accepts them, and == then asks
// "is this the same object". A reducer that mutates in place and returns the
// same pointer answers yes, so a real state change reported as no change froze
// state_version and made P4 false on the wire.
//
// The check does not descend into struct fields. A struct holding a pointer
// compares that pointer, so mutating through it is invisible here too — but
// that is the purity rule rather than a type-level property, and descending
// would report "changed" for every transition of any state holding, say, a
// *time.Location, which breaks P4 in the other direction. The constraint is
// documented at Config's type parameter instead.
//
// It is computed here, where S is still a type parameter, because everything
// below the adapter holds state as an opaque any and would have to reflect on
// each value to ask the same question.
func comparableState[S any]() bool {
	t := reflect.TypeOf((*S)(nil)).Elem()
	if !t.Comparable() {
		return false
	}
	switch t.Kind() {
	case reflect.Pointer, reflect.UnsafePointer, reflect.Map, reflect.Slice,
		reflect.Chan, reflect.Func, reflect.Interface:
		return false
	default:
		return true
	}
}

func sessionOf[I IIdentity](p session.Peer[I]) Session[I] {
	return Session[I]{id: ID(p.ID), identity: p.Identity}
}

// emissionContext is the subject clause of every error the [Emitter] raises
// against an event an application built.
//
// It is one string rather than a pair of format verbs at four call sites
// because FR-58's first two clauses — name the session, name the causal
// identifier where one exists — are the same two facts every time, and four
// copies of them is four chances for one to be dropped in an edit. The spec
// that holds this is live's own emit suite, which asserts the session and the
// scheduling event appear in all four.
//
// The scheduling event is the causal identifier that exists here. The event
// being emitted has none: identifiers are minted at the actor boundary, which
// this call has not reached, and refusing an application that mints one itself
// is what the first of the four refusals is about.
func emissionContext[I IIdentity](p session.Peer[I], scheduledBy uint64) string {
	if scheduledBy == 0 {
		return fmt.Sprintf(
			"session %s: an event emitted by an effect the server scheduled itself", p.ID)
	}
	return fmt.Sprintf(
		"session %s: an event emitted by an effect scheduled by event %d", p.ID, scheduledBy)
}

func eventOf(ev session.Event) Event {
	return Event{
		Name:       ev.Name,
		FragmentID: ev.FragmentID,
		Fields:     Fields{fields: ev.Fields},
		At:         ev.At,
		ID:         ev.ID,
	}
}

// toInternalEffects re-expresses a transition's effects in the actor's
// vocabulary. It is the ONE translation between the exported [Effect] and
// internal/session's, and it exists only because Run's signature names a
// [Session] and an [Emitter], which an internal package cannot.
//
// The inert zero Effect is dropped here as well as at the actor, so a reducer
// that appends a conditional effect which did not apply produces a shorter
// slice rather than a padded one — which is what the effects a specification
// reads should say.
func toInternalEffects[I IIdentity](effects []Effect[I]) []session.Effect[I] {
	if len(effects) == 0 {
		return nil
	}
	out := make([]session.Effect[I], 0, len(effects))
	for _, effect := range effects {
		if effect.inert() {
			continue
		}
		out = append(out, session.Effect[I]{Source: effect.Source, Run: internalRun(effect.Run)})
	}
	return out
}

// internalRun wraps one effect's behaviour in the actor's vocabulary: the peer
// becomes the [Session] the effect acts for, and the actor's raw emitter becomes
// the guarded [Emitter] an application is allowed to hold.
//
// A nil Run stays nil rather than becoming a closure that does nothing. The
// actor refuses it with a failure event, which is the whole point: an effect
// that named itself and forgot its behaviour is a change that never happens,
// and wrapping it here would have hidden that behind a function that succeeds.
func internalRun[I IIdentity](
	run func(ctx context.Context, session Session[I], emit Emitter) error,
) func(ctx context.Context, p session.Peer[I], scheduledBy uint64, emit session.Emit) error {
	if run == nil {
		return nil
	}
	return func(ctx context.Context, p session.Peer[I], scheduledBy uint64, emit session.Emit) error {
		return run(ctx, sessionOf(p), guardedEmitter(p, scheduledBy, emit))
	}
}

// guardedEmitter is the [Emitter] an effect is handed: the actor's own
// injection function behind the four refusals an application-built event has to
// survive.
//
// It was the body of the adapter's Execute method until the effect started
// carrying its own behaviour. Nothing about the refusals changed with the move,
// and the specification that holds them — live's emit suite — is what says so.
func guardedEmitter[I IIdentity](p session.Peer[I], scheduledBy uint64, emit session.Emit) Emitter {
	// FR-58's two clauses that are not the message itself, resolved once for
	// the four refusals below. The session is the peer this effect is acting
	// for; the causal identifier is the event whose transition returned the
	// effect, because the event being emitted has none of its own yet — that
	// is the whole subject of the first refusal.
	where := emissionContext(p, scheduledBy)
	return func(ev Event) error {
		// Both of these are server-minted, and both used to be accepted and
		// then silently discarded — which is the shape of field that becomes
		// somebody's wrong fix later. Rejecting is louder and cheaper: the
		// error reaches the reducer as a deterministic effect failure.
		if ev.ID != 0 {
			return fmt.Errorf(
				"gotth-live: %s set Event.ID to %d: causal identifiers are minted by the server, "+
					"so leave it zero — the library already carries the edge from the scheduling event",
				where, ev.ID)
		}
		if !ev.At.IsZero() {
			return fmt.Errorf(
				"gotth-live: %s set Event.At: the actor boundary stamps it, so leave it zero", where)
		}
		// The bound D-18 found missing. An over-long Contributing used to be
		// accepted here, folded into a coalescing union with a schema ceiling,
		// and refused by the outbound validator on the actor goroutine — after
		// this call had already returned nil. The application was told nothing,
		// the patch was replaced by a non-fatal Error{INTERNAL}, the state
		// change never reached the client, and a metric documented as "any
		// non-zero value is a library bug" was incremented by application
		// input. Rejecting here makes it a deterministic failure of this
		// effect, which is the contract the three checks above already have.
		//
		// Where the number comes from is in internal/session's godoc; the
		// short version is that the schema bounds every ordinary per-message
		// list at 64, and every identifier an application may add to one event
		// is one the library may not coalesce.
		if len(ev.Contributing) > session.MaxEventContributing {
			return fmt.Errorf(
				"gotth-live: %s listed %d identifiers in Event.Contributing, "+
					"above the limit of %d: name the events whose state changes this event "+
					"carries, not every event the session has seen",
				where, len(ev.Contributing), session.MaxEventContributing)
		}
		for _, id := range ev.Contributing {
			if id == 0 {
				return fmt.Errorf(
					"gotth-live: %s listed 0 in Event.Contributing: "+
						"list the identifiers of real events, or leave the field nil", where)
			}
		}
		return emit(session.Event{
			Name:         ev.Name,
			FragmentID:   ev.FragmentID,
			Fields:       ev.Fields.fields,
			Contributing: ev.Contributing,
		})
	}
}

// authenticateAdapter is gone with the nil check it existed for.
//
// It wrapped the application's hook to refuse an identity that was nil with no
// error — the shape an interface result made expressible. Since 2026-09-03 the
// hook returns the application's OWN type, so `nil, nil` is not a value it can
// produce, and wsx takes the hook unchanged. That is one fewer indirection and
// one fewer library-authored error; internal/arch's FR-58 census records the
// removal.

// fragmentAdapter keeps dynamic children behind the same typed state boundary
// as static fragments. Applications never hold an erased state.
func fragmentAdapter[S any](fragment Fragment[S]) render.Fragment {
	adapted := render.Fragment{
		ID:    fragment.ID,
		Dirty: dirtyAdapter(fragment.Dirty),
	}
	if fragment.Render != nil {
		adapted.Render = renderAdapter(fragment.ID, fragment.Render)
	}
	if fragment.Children != nil {
		adapted.Children = func(state any) []render.Fragment {
			children := fragment.Children(state.(S))
			result := make([]render.Fragment, len(children))
			for index, child := range children {
				result[index] = fragmentAdapter(child)
			}
			return result
		}
	}
	return adapted
}

// renderAdapter turns a fragment's typed render into the opaque one the
// renderer calls. The type assertion happens here, exactly once per render,
// and nowhere else in the library.
//
// It closes over the fragment's identity so that the one error it can produce
// names WHICH region returned nil. The renderer records the identity too, on
// the Failure it builds, and the actor's log line carries both that and the
// session — but the error value travels through Config's own render hook and
// can be wrapped by an application before either of those sees it, and
// "a fragment rendered no component" is not a sentence anybody can act on when
// a page declares nine of them (FR-58).
func renderAdapter[S any](id string, fn func(state S) templ.Component) render.RenderFunc {
	return func(ctx context.Context, state any, w io.Writer) error {
		component := fn(state.(S))
		if component == nil {
			return fmt.Errorf("gotth-live: fragment %q rendered no component: "+
				"return a templ component rather than nil — an empty one for the state that has "+
				"nothing to show", id)
		}
		return component.Render(ctx, w)
	}
}

func dirtyAdapter[S any](fn func(prev, next S) bool) render.DirtyFunc {
	if fn == nil {
		return nil
	}
	return func(prev, next any) bool { return fn(prev.(S), next.(S)) }
}
