package widget

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"reflect"
	"slices"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// Registry is the set of widgets one host binary serves.
//
// It is the `register` phase's home: a widget is registered once per process,
// before any session exists, and the registry is what turns that set into one
// gotth-live application. Registration order is preserved everywhere — fragment
// order, snapshot order, the order Mount and Unmount run in — because a host
// that rendered its widgets in map order would render two byte-different pages
// from one state.
//
// A Registry is built at startup and read afterwards. [Register] is not safe to
// call concurrently with anything; everything else is read-only once the last
// widget is in.
//
// The registry is not generic and cannot be. Its whole purpose is holding
// widgets of several different state types in one ordered sequence, which is
// the heterogeneity CS-7 § 2 is about: it is erased exactly once, in [Register],
// into the unexported adapter below.
type Registry[I live.IIdentity] struct {
	widgets       []iErasedWidget[I]
	registrations []Registration
	byName        map[string]int
	byRegion      map[string]int
	byWire        map[string]int
}

// NewRegistry returns an empty registry.
func NewRegistry[I live.IIdentity]() *Registry[I] {
	return &Registry[I]{
		byName:   map[string]int{},
		byRegion: map[string]int{},
		byWire:   map[string]int{},
	}
}

// iErasedWidget is one registered widget with its state type forgotten.
//
// It exists so that [Registry] can hold widgets of different state types in one
// slice, which Go has no other way to express. Every method takes and returns
// the state as `any`, and every one of them is unexported: nothing outside this
// package can implement this interface, hold a value of it, or be handed an
// untyped state through it. That containment is the point — CS-7 permits
// erasure where heterogeneity genuinely forces it, on the condition that it
// happens in one audited place inside the library that owns the collection.
type iErasedWidget[I live.IIdentity] interface {
	// register, mount, reduce, render, unmount and snapshot are the six
	// phases of [IWidget], with S replaced by `any`.
	register() Registration
	mount(ctx context.Context, session live.Session[I]) (any, []live.Effect[I], error)
	reduce(state any, event live.Event) (any, []live.Effect[I])
	render(state any) templ.Component
	unmount(ctx context.Context, session live.Session[I], state any)
	snapshot(state any) Snapshot

	// dirty answers "did this transition reach my region", from the widget's
	// own [IDirtyDeclarer] declaration when it makes one and from a whole-state
	// comparison when it does not.
	dirty(previous any, next any) bool

	// instance returns the widget this adapter wraps, still erased, so that a
	// typed [Lookup] can assert it back to the type its caller asks for.
	instance() any
}

// erasedWidget is the one place in this module where a widget's state type is
// forgotten, and the one place it is asserted back.
//
// **CS-7's single audited erasure site.** The assertion below is total by
// construction rather than defensive, and the construction is this: the only
// way a value reaches `state` is that [Registry] put it there, and the only way
// [Registry] got one is that this adapter's own mount or reduce returned it. So
// the dynamic type is always S — or the state is nil, which happens for exactly
// one caller: a render of a HostState from before this widget was mounted, for
// which the zero S is the right answer and is what the failed assertion yields.
// Nothing else can construct an erasedWidget, because [Register] is the only
// function that does and it takes an IWidget[S] to do it.
type erasedWidget[S any, I live.IIdentity] struct {
	widget IWidget[S, I]
}

func (adapter erasedWidget[S, I]) register() Registration { return adapter.widget.Register() }

func (adapter erasedWidget[S, I]) mount(
	ctx context.Context, session live.Session[I],
) (any, []live.Effect[I], error) {
	return adapter.widget.Mount(ctx, session)
}

func (adapter erasedWidget[S, I]) reduce(state any, event live.Event) (any, []live.Effect[I]) {
	return adapter.widget.Reduce(stateOf[S](state), event)
}

func (adapter erasedWidget[S, I]) render(state any) templ.Component {
	return adapter.widget.Render(stateOf[S](state))
}

func (adapter erasedWidget[S, I]) unmount(ctx context.Context, session live.Session[I], state any) {
	adapter.widget.Unmount(ctx, session, stateOf[S](state))
}

func (adapter erasedWidget[S, I]) snapshot(state any) Snapshot {
	return adapter.widget.Snapshot(stateOf[S](state))
}

func (adapter erasedWidget[S, I]) dirty(previous any, next any) bool {
	declarer, declares := adapter.widget.(IDirtyDeclarer[S])
	if !declares {
		// Through the same assertion the declaring branch below uses, and then
		// a deep comparison.
		//
		// The assertion is what makes the two branches answer one question. A
		// HostState from before this widget mounted carries `nil` where its
		// state goes, and `nil` is never deeply equal to the zero S — not for a
		// pointer, whose zero value is a nil of a type, and not for an `int`,
		// whose zero value is 0. Comparing the erased pair therefore called
		// that transition dirty while a widget declaring its own test, handed
		// the zero S on both sides, called it clean.
		//
		// The comparison is deep because `==` on an interface holding an
		// uncomparable dynamic type panics at runtime, and S may be a map, a
		// slice, a function or a struct containing one. It never reports equal
		// for two states that differ, which is the direction a fallback has to
		// fail in: over-declaring costs a patch nobody needed and
		// under-declaring is a region that stops updating.
		return !reflect.DeepEqual(stateOf[S](previous), stateOf[S](next))
	}
	return declarer.Dirty(stateOf[S](previous), stateOf[S](next))
}

func (adapter erasedWidget[S, I]) instance() any { return adapter.widget }

// stateOf is the assertion itself, written once so that the audited site is one
// function rather than seven copies of one line. See [erasedWidget].
func stateOf[S any](state any) S {
	typed, _ := state.(S)
	return typed
}

// Register adds one widget, reporting the first fault in its registration or
// the first collision with a widget already registered.
//
// It is a function rather than a method because a method cannot take a type
// parameter, and the type parameter is the point: this is the generic shell
// CS-7 § 2 asks for, and [erasedWidget] is the unexported adapter behind it.
// Everything above the shell knows S; nothing below it ever needs to.
//
// Failing here rather than at the first connection is deliberate: a duplicated
// region identity is a region that stops updating for reasons nothing explains,
// and a duplicated wire name is an event delivered to the wrong widget. Both
// are mistakes in a literal somebody wrote, and startup is where a mistake in a
// literal belongs.
func Register[S any, I live.IIdentity](registry *Registry[I], instance IWidget[S, I]) error {
	if instance == nil {
		return fmt.Errorf("%w: a nil widget has no registration", ErrEmptyName)
	}
	return registry.add(erasedWidget[S, I]{widget: instance})
}

// MustRegister is [Register] for a caller with nowhere to put the error:
// package initialisation and main, where the registration is a literal in the
// source and every fault Register reports is a mistake in that literal.
func MustRegister[S any, I live.IIdentity](registry *Registry[I], instance IWidget[S, I]) {
	if registrationError := Register(registry, instance); registrationError != nil {
		panic(registrationError)
	}
}

// add is the untyped half of registration: everything that does not depend on
// the widget's state type, which is everything except the erasure above.
func (registry *Registry[I]) add(adapter iErasedWidget[I]) error {
	registration := adapter.register()
	if validationError := registration.Validate(); validationError != nil {
		return validationError
	}
	if existing, taken := registry.byName[registration.Name]; taken {
		return fmt.Errorf("%w: %s, already registered at index %d", ErrDuplicateName, registration.Name, existing)
	}
	if existing, taken := registry.byRegion[registration.Region]; taken {
		return fmt.Errorf("%w: %s, already claimed by %s",
			ErrDuplicateRegion, registration.Region, registry.registrations[existing].Name)
	}
	for _, wireName := range registration.wireNames() {
		if existing, taken := registry.byWire[wireName]; taken {
			return fmt.Errorf("%w: %s, already claimed by %s",
				ErrDuplicateEvent, wireName, registry.registrations[existing].Name)
		}
	}

	index := len(registry.widgets)
	registry.widgets = append(registry.widgets, adapter)
	registry.registrations = append(registry.registrations, registration)
	registry.byName[registration.Name] = index
	registry.byRegion[registration.Region] = index
	for _, wireName := range registration.wireNames() {
		registry.byWire[wireName] = index
	}
	return nil
}

// List returns every registration in registration order.
//
// The slice is a copy, so a caller enumerating the host's widgets — a status
// page, a test, an operator command — cannot reorder what the host renders.
func (registry *Registry[I]) List() []Registration {
	return slices.Clone(registry.registrations)
}

// Lookup returns the registration filed under a name.
//
// It returns the registration rather than the widget, and that is the one place
// the generic contract costs something: a name is a string, so a method that
// handed back an IWidget[S] would have to be told which S to assert to, which
// is a second erasure site for a question — "what does this widget declare" —
// that the registration already answers. [LookupWidget] is there for a caller
// that genuinely holds the widget's own type.
func (registry *Registry[I]) Lookup(name string) (Registration, bool) {
	index, known := registry.byName[name]
	if !known {
		return Registration{}, false
	}
	return registry.registrations[index], true
}

// LookupWidget returns the widget registered under a name, typed.
//
// The caller supplies S, so unlike [Register] this assertion is not total: it
// reports false for a name nobody registered and for a widget whose state type
// is not the one asked for. That is deliberate — asking the wrong type is a
// question with no answer, and a package that panicked would be answering it.
func LookupWidget[S any, I live.IIdentity](registry *Registry[I], name string) (IWidget[S, I], bool) {
	index, known := registry.byName[name]
	if !known {
		return nil, false
	}
	typed, matches := registry.widgets[index].instance().(IWidget[S, I])
	return typed, matches
}

// HostState is one session's state across every registered widget: one entry
// per widget, in registration order.
//
// It is opaque on purpose, and it is the collection whose heterogeneity forces
// the erasure: one entry per widget, each of a type only its own widget knows.
// A host holds it, the library carries it between transitions, and the way to
// read one is [Registry.Snapshots], which asks each widget for its own
// projection.
type HostState struct {
	states []any
}

// Len returns how many widgets this state covers.
func (state HostState) Len() int { return len(state.states) }

// Snapshots projects every widget's state, in registration order.
//
// It is what a test asserts on and what an operator reads: a host that could
// not describe its own widgets' state without knowing their types would have to
// be recompiled to answer the question.
func (registry *Registry[I]) Snapshots(state HostState) []Snapshot {
	snapshots := make([]Snapshot, 0, len(registry.widgets))
	for index, adapter := range registry.widgets {
		if index >= len(state.states) {
			break
		}
		snapshots = append(snapshots, adapter.snapshot(state.states[index]))
	}
	return snapshots
}

// MountOptions carries the decisions a registry cannot make for a host.
//
// Everything here is either a security posture or a resource the host owns.
// None of it has a defensible default that a library could pick — an allowlist
// a library chose would be an allowlist nobody read — so the four hooks are
// passed straight through to the live configuration, which refuses a nil one.
type MountOptions[I live.IIdentity] struct {
	// Origins is the browser Origin allowlist, passed through unchanged.
	Origins []string

	// Authenticate, Authorize and CSRF are the live library's three security
	// hooks. Pass live.Anonymous, live.AllowAll and live.NoCSRFCheck to opt
	// out deliberately; a nil one is refused rather than defaulted.
	Authenticate func(request *http.Request) (I, error)
	Authorize    func(ctx context.Context, session live.Session[I], event live.Event) error
	CSRF         func(request *http.Request) error

	// Init schedules the host's own startup effects for a session, alongside
	// the effects each widget's Mount returned. It is where a host opens the
	// sources its widgets' declared streams name.
	//
	// There is no Execute beside it, and there was one until 2026-09-03. A
	// [live.Effect] carries its own Run, so a host effect is performed by the
	// closure the host built it with — over the source, broker or pool that
	// host owns — and the executor that used to type-switch an effect back to
	// its owner had nothing left to decide. What that executor guaranteed is
	// now the library's: an effect that names itself and carries no behaviour
	// fails deterministically rather than succeeding at nothing.
	Init func(ctx context.Context, session live.Session[I]) ([]live.Effect[I], error)

	// Logger and Dev are passed through to the live configuration.
	Logger *slog.Logger
	Dev    bool
}

// LiveConfig turns the registry into one gotth-live configuration: one fragment
// per widget, the union of every widget's browser-sendable event names, and a
// reducer that routes each event to the widget that owns it.
//
// Only [Registration.Events] is registered with the live library.
// [Registration.Internal] is deliberately left out and is still routed, because
// registration is the only thing that makes a name sendable by a browser: an
// event a declared stream delivers has a server-side source, and a browser
// posting one of its own would be forging that source's truth. Default-deny is
// what the library does with a name nobody registered, so leaving the name out
// is the whole of the enforcement.
//
// This is the whole of "mounting a widget into a host". The host calls
// live.New on the result and serves the handler. Every widget on a connection
// shares its event loop and transport; effects may add goroutines. There is no
// per-widget process, port, connection or mandatory goroutine.
func (registry *Registry[I]) LiveConfig(options MountOptions[I]) (live.Config[HostState, I], error) {
	if len(registry.widgets) == 0 {
		return live.Config[HostState, I]{}, ErrNoWidgets
	}

	fragments := make([]live.Fragment[HostState], 0, len(registry.widgets))
	events := make([]string, 0, len(registry.widgets))
	for index, adapter := range registry.widgets {
		fragments = append(fragments, registry.fragment(index, adapter))
		events = append(events, registry.registrations[index].Events...)
	}

	return live.Config[HostState, I]{
		Init:         registry.initialiser(options),
		Reduce:       registry.reduce,
		Fragments:    fragments,
		Events:       events,
		Teardown:     registry.teardown,
		Origins:      options.Origins,
		Authenticate: options.Authenticate,
		Authorize:    options.Authorize,
		CSRF:         options.CSRF,
		Logger:       options.Logger,
		Dev:          options.Dev,
	}, nil
}

// fragment builds one widget's live region.
//
// Dirty compares the widget's own state and nothing else, so a transition
// touching one widget re-renders one region.
func (registry *Registry[I]) fragment(index int, adapter iErasedWidget[I]) live.Fragment[HostState] {
	return live.Fragment[HostState]{
		ID: registry.registrations[index].Region,
		Render: func(state HostState) templ.Component {
			return adapter.render(stateAt(state, index))
		},
		Dirty: func(previous HostState, next HostState) bool {
			return adapter.dirty(stateAt(previous, index), stateAt(next, index))
		},
	}
}

// stateAt returns one widget's entry, or nil for a state that predates it. The
// bound check is not defensive padding: live.Config.Init runs per session, and
// a render of the zero HostState is what a caller gets if it renders a page
// before any session has mounted.
func stateAt(state HostState, index int) any {
	if index >= len(state.states) {
		return nil
	}
	return state.states[index]
}

// initialiser runs the `mount` phase for every widget, in registration order,
// and collects their effects behind the host's own.
func (registry *Registry[I]) initialiser(
	options MountOptions[I],
) func(ctx context.Context, session live.Session[I]) (HostState, []live.Effect[I], error) {
	return func(ctx context.Context, session live.Session[I]) (HostState, []live.Effect[I], error) {
		state := HostState{states: make([]any, len(registry.widgets))}
		var effects []live.Effect[I]

		if options.Init != nil {
			hostEffects, initError := options.Init(ctx, session)
			if initError != nil {
				return HostState{}, nil, initError
			}
			effects = append(effects, hostEffects...)
		}

		for index, adapter := range registry.widgets {
			mounted, widgetEffects, mountError := adapter.mount(ctx, session)
			if mountError != nil {
				return HostState{}, nil, fmt.Errorf("widget %s: mount: %w", registry.registrations[index].Name, mountError)
			}
			state.states[index] = mounted
			effects = append(effects, widgetEffects...)
		}
		return state, effects, nil
	}
}

// reduce routes one event to the widget that owns it and folds the result back
// into the host's state.
//
// Routing is by region first and by wire name second, because those are the two
// things an event can carry that name a widget. Both halves of a registration's
// wire names route here, browser-sendable or internal: what Internal changes is
// whether a browser may send the name, not whether the host can deliver it.
// Anything naming neither — the library's own effect-failure, slow-client and
// client-recovered events — is delivered to every widget, which is the
// ontology's rule that a failure re-enters `event` rather than becoming a log
// line: each widget decides for itself whether the notice was about it.
func (registry *Registry[I]) reduce(state HostState, event live.Event) (HostState, []live.Effect[I]) {
	next := HostState{states: slices.Clone(state.states)}
	for len(next.states) < len(registry.widgets) {
		next.states = append(next.states, nil)
	}
	var effects []live.Effect[I]

	if index, addressed := registry.route(event); addressed {
		next.states[index], effects = registry.widgets[index].reduce(next.states[index], event)
		return next, effects
	}

	for index, adapter := range registry.widgets {
		reduced, widgetEffects := adapter.reduce(next.states[index], event)
		next.states[index] = reduced
		effects = append(effects, widgetEffects...)
	}
	return next, effects
}

// route returns the widget one event names, and whether it named one at all.
func (registry *Registry[I]) route(event live.Event) (int, bool) {
	if index, known := registry.byRegion[event.FragmentID]; known {
		return index, true
	}
	index, known := registry.byWire[event.Name]
	return index, known
}

// teardown runs the `unmount` phase for every widget, in reverse registration
// order: a widget registered later may have been mounted against something an
// earlier one owns.
func (registry *Registry[I]) teardown(ctx context.Context, session live.Session[I], state HostState) {
	for index := len(registry.widgets) - 1; index >= 0; index-- {
		registry.widgets[index].unmount(ctx, session, stateAt(state, index))
	}
}
