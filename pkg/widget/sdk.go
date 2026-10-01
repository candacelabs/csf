package widget

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
)

// SnapshotField is one field of a widget's state, rendered as text.
type SnapshotField struct {
	// Name is the state field's declared name, as the document spells it.
	Name string
	// Value is the field's current value rendered as text: "true"/"false" for
	// a flag, base-10 digits for a counter or a count, the string itself for
	// text.
	Value string
}

// Snapshot is a widget's whole state as ordered name/value pairs.
//
// It is the only way a host, a test or an operator reads a widget's state
// without knowing its type, and it is ordered by state-field declaration order
// rather than by name so that two snapshots of equal state are equal
// element-for-element. A map would have made a snapshot's own text depend on
// iteration order, which is the same defect the IR forbids in a render.
type Snapshot struct {
	// Widget is the name of the widget the snapshot came from. A widget fills
	// it from its own registration, so a snapshot is self-describing once it
	// has left the widget that made it.
	Widget string
	// Fields are the state fields in declaration order.
	Fields []SnapshotField
}

// StreamDeclaration is one long-running subscription a widget declares and the
// host owns.
//
// It is a declaration and never a connection: it names a source and the event
// that source delivers, and nothing else. The widget cannot open it — a widget
// document names no host, no address and no credential by construction — so the
// host reads this and wires the source it has.
type StreamDeclaration struct {
	// Name is the stream's declared name in the document.
	Name string
	// Source is the source name the document declared. It is a name the host
	// resolves, never an address.
	Source string
	// Delivers is the wire name of the event this stream delivers.
	Delivers string
}

// Registration is everything a widget declares once per process, before any
// session exists.
//
// It is the `register` phase as a value. The ontology's cardinality is what
// makes it a value rather than a call: registration happens exactly once per
// widget per process and cannot depend on a session, so a widget that could
// only describe itself while mounted could not be registered at all.
type Registration struct {
	// Name identifies the widget within one host, and is the name its own
	// snapshots carry.
	Name string

	// Region is the widget's single server-owned live region, and the only
	// identity that crosses the wire: a patch names it, so changing it is a
	// client-visible change. It matches ^[A-Za-z0-9_:.-]{1,64}$.
	Region string

	// Events are the wire names a browser may send to this widget. The set is
	// exhaustive and default-deny: the host registers exactly these with the
	// live library, and a name absent here is refused before any reducer runs.
	Events []string

	// Internal are the wire names only this widget's own streams and effects
	// emit. They are deliberately not registered with the live library —
	// registration is what makes a name sendable by a browser — but the host
	// still needs them to route an emitted event back to the widget that
	// emitted it.
	//
	// A generated widget puts every stream-delivered event here, because the
	// subscription is what delivers it and a browser posting one of its own
	// would be forging the source's own truth.
	Internal []string

	// Streams are the subscriptions the widget declares and the host owns.
	Streams []StreamDeclaration

	// Payloads are the wire field names each declared event carries, in
	// declaration order, for the events that carry any.
	//
	// They are here because the field names are a contract between two
	// programs that never read each other's source: the widget takes them out
	// of an event, and whatever fills that event — a host adapter resolving a
	// declared stream, a browser control — puts them in. Until this existed the
	// filling half was a literal somebody typed, so renaming a field in the
	// document still compiled and silently stopped updating the widget.
	Payloads []EventPayload
}

// EventPayload is the wire field names one declared event carries.
type EventPayload struct {
	// Event is the event's wire name, and must be one this registration
	// declares in Events or Internal.
	Event string

	// Fields are the event's wire field names, in the order the document
	// declares them. Declaration order rather than sorted, for the same reason
	// a Snapshot is ordered: two equal declarations compare equal
	// element-for-element.
	Fields []string
}

// Payload returns the wire field names one declared event carries.
//
// It is the read half of [Registration.Payloads]: a host filling an event, or a
// specification asserting that a host fills exactly the declared set, asks the
// registration rather than restating the names.
func (registration Registration) Payload(event string) ([]string, bool) {
	for _, payload := range registration.Payloads {
		if payload.Event == event {
			return payload.Fields, true
		}
	}
	return nil, false
}

// IWidget is one widget: a self-contained slice of live UI that a host
// registers once and mounts per session.
//
// S is the widget's own state type, and it is a type parameter rather than an
// opaque one because nothing an author writes has any reason not to know it. A
// widget is written by whoever owns S; only the host holds widgets of several
// different S at once, and that is the host's problem rather than the author's.
// [Register] is where it is solved, exactly once and under a comment saying so.
//
// The six methods are the lifecycle phases of docs/ontology.md, in the order
// a session drives them — register, mount, event, render, unmount — plus the
// one projection a host can read without knowing the widget's type. The set is
// closed by the ontology rather than by convenience: a widget cannot invent a
// phase, and a generator emits code for these and no others.
//
// `effect` is the second phase with no method of its own, and it lost one on
// 2026-09-03 rather than never having had one. A [live.Effect] is a concrete
// struct carrying its own Run, so an effect a widget schedules already holds
// the closure that performs it — over whatever that widget owns — and a method
// the host called to hand the effect back had nothing left to decide. The phase
// is still the widget's: it is spelled in the effects Mount and Reduce return.
//
// `tick` is the one phase with no method of its own, and its absence is the
// ontology's own reading rather than an omission. A tick is what a Stream
// delivers without a user, so it arrives at Reduce as the event the stream
// carries — the same way an effect failure arrives at Reduce as an event, so
// that a reducer sees every cause in one switch and every one of them is
// replayable.
//
// Nothing here is called concurrently with itself for one session: a session is
// one goroutine, and Reduce and Render run on it. An effect's own Run is the
// only application code that may perform I/O, and the library runs it on a
// goroutine of its own.
type IWidget[S any, I live.IIdentity] interface {
	// Register declares the widget, once per process and before any session.
	// It must be pure and must return the same registration on every call.
	Register() Registration

	// Mount opens one session's copy of the widget and returns its initial
	// state together with any effects that start it. It is the first phase of a
	// session and happens exactly once.
	Mount(ctx context.Context, session live.Session[I]) (S, []live.Effect[I], error)

	// Reduce is the event phase: the pure transition from one state to the
	// next. Given equal state and an equal event it must return equal state and
	// equal effects, perform no I/O, read no clock, and mutate nothing it was
	// given.
	Reduce(state S, event live.Event) (S, []live.Effect[I])

	// Render draws the widget's live region. It must be a pure function of
	// state — equal state renders byte-identical markup — because that
	// comparison is what suppresses a patch nobody needs.
	Render(state S) templ.Component

	// Unmount releases whatever the session held. It is the last phase of a
	// session and happens exactly once, after which no other phase occurs.
	Unmount(ctx context.Context, session live.Session[I], state S)

	// Snapshot projects state into ordered name/value pairs, so a host, a test
	// or an operator can read a widget's state without knowing its type.
	Snapshot(state S) Snapshot
}

// IDirtyDeclarer is a widget that declares which state changes its own region's
// markup depends on.
//
// It is optional, and deliberately not part of [IWidget]: a widget that does not
// implement it gets the registry's whole-state comparison, which never reports
// equal for two states that differ and is therefore always safe. What the
// declaration buys is the other direction — a transition that moved state this
// region does not render is not a patch anybody needs — and what it costs if it
// is wrong is a region that stops updating, which is why the safe behaviour is
// the default and this is the opt-in.
//
// A generated widget implements it from its document's computed dirty
// projection, so the declaration is derived from the same source the render is
// and cannot drift from it. A hand-written widget should implement it only when
// it can state which fields its markup reads; "all of them" is what the default
// already does.
type IDirtyDeclarer[S any] interface {
	// Dirty reports whether a transition may have changed this widget's markup.
	// It is handed this widget's own state, before and after, never the host's.
	//
	// Over-declaring costs a suppressed render; under-declaring is a
	// correctness bug, and live/livetest.AssertDirtyComplete is what catches
	// it.
	Dirty(previous S, next S) bool
}

// The registration faults a host can commit. Every one of them is a startup
// mistake in a literal somebody wrote, which is why they are reported when the
// widget is registered rather than at the first connection.
var (
	// ErrEmptyName is returned for a registration with no widget name.
	ErrEmptyName = errors.New("widget: a registration needs a name")
	// ErrInvalidRegion is returned for a region identity the wire cannot carry.
	ErrInvalidRegion = errors.New("widget: a region identity must match " + regionPattern)
	// ErrDuplicateName is returned when two widgets claim one name.
	ErrDuplicateName = errors.New("widget: two widgets claim one name")
	// ErrDuplicateRegion is returned when two widgets claim one live region.
	ErrDuplicateRegion = errors.New("widget: two widgets claim one live region")
	// ErrDuplicateEvent is returned when two widgets claim one wire name.
	ErrDuplicateEvent = errors.New("widget: two widgets claim one event wire name")
	// ErrEmptyEvent is returned for a wire name that is the empty string.
	ErrEmptyEvent = errors.New("widget: every event needs a wire name")
	// ErrUndeliveredStream is returned for a stream delivering an event the
	// widget did not declare, which is a subscription whose payload nothing
	// would route.
	ErrUndeliveredStream = errors.New("widget: a stream delivers an event the widget does not declare")
	// ErrUnknownPayload is returned for a payload describing an event the
	// widget does not declare, which is a set of field names nothing can fill.
	ErrUnknownPayload = errors.New("widget: a payload describes an event the widget does not declare")
	// ErrDuplicatePayload is returned when one event is described twice, which
	// leaves two answers to "what does this event carry".
	ErrDuplicatePayload = errors.New("widget: one event carries two payload declarations")
	// ErrEmptyField is returned for a payload field with no wire name.
	ErrEmptyField = errors.New("widget: every payload field needs a wire name")
	// ErrDuplicateField is returned when one event declares one field twice.
	ErrDuplicateField = errors.New("widget: one event declares one payload field twice")
	// ErrNoWidgets is returned when a host asks a registry holding nothing for
	// a configuration, which would serve a page with no live region on it.
	ErrNoWidgets = errors.New("widget: a host needs at least one registered widget")
)

// regionPattern is the ontology's region identity: stable across releases,
// carried on the wire by every patch, and the one identifier a widget shares
// with a browser.
const regionPattern = `^[A-Za-z0-9_:.-]{1,64}$`

var regionExpression = regexp.MustCompile(regionPattern)

// Validate reports the first fault in a registration, or nil.
//
// It checks what one registration can be wrong about on its own. What only a
// set can be wrong about — two widgets claiming one name, one region or one
// wire name — is checked by [Registry.Register], because neither registration
// is at fault by itself.
func (registration Registration) Validate() error {
	if strings.TrimSpace(registration.Name) == "" {
		return ErrEmptyName
	}
	if !regionExpression.MatchString(registration.Region) {
		return fmt.Errorf("%w: widget %s declares region %q", ErrInvalidRegion, registration.Name, registration.Region)
	}
	declared := make(map[string]struct{}, len(registration.Events)+len(registration.Internal))
	for _, wireName := range registration.wireNames() {
		if wireName == "" {
			return fmt.Errorf("%w: widget %s", ErrEmptyEvent, registration.Name)
		}
		if _, taken := declared[wireName]; taken {
			return fmt.Errorf("%w: widget %s declares %s twice", ErrDuplicateEvent, registration.Name, wireName)
		}
		declared[wireName] = struct{}{}
	}
	for _, stream := range registration.Streams {
		if _, known := declared[stream.Delivers]; !known {
			return fmt.Errorf("%w: widget %s stream %s delivers %s",
				ErrUndeliveredStream, registration.Name, stream.Name, stream.Delivers)
		}
	}
	return registration.validatePayloads(declared)
}

// validatePayloads reports the first fault in the payload declarations, given
// the wire names this widget already answers to.
//
// A payload names an event and its field names, and both halves can be wrong in
// a literal: an event nobody declared, one event described twice, a field with
// no name, one field named twice. All four are startup mistakes for the same
// reason the rest of Validate's faults are — a host that discovered them at the
// first connection would discover them from a widget that had stopped updating.
func (registration Registration) validatePayloads(declared map[string]struct{}) error {
	described := make(map[string]struct{}, len(registration.Payloads))
	for _, payload := range registration.Payloads {
		if _, known := declared[payload.Event]; !known {
			return fmt.Errorf("%w: widget %s describes %s",
				ErrUnknownPayload, registration.Name, payload.Event)
		}
		if _, twice := described[payload.Event]; twice {
			return fmt.Errorf("%w: widget %s describes %s twice",
				ErrDuplicatePayload, registration.Name, payload.Event)
		}
		described[payload.Event] = struct{}{}

		named := make(map[string]struct{}, len(payload.Fields))
		for _, field := range payload.Fields {
			if field == "" {
				return fmt.Errorf("%w: widget %s event %s",
					ErrEmptyField, registration.Name, payload.Event)
			}
			if _, twice := named[field]; twice {
				return fmt.Errorf("%w: widget %s event %s declares %s twice",
					ErrDuplicateField, registration.Name, payload.Event, field)
			}
			named[field] = struct{}{}
		}
	}
	return nil
}

// wireNames returns every event name this widget answers to, browser-sendable
// first, in declaration order.
func (registration Registration) wireNames() []string {
	names := make([]string, 0, len(registration.Events)+len(registration.Internal))
	names = append(names, registration.Events...)
	names = append(names, registration.Internal...)
	return names
}
