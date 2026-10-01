package widgettest

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget"
)

// ErrNoRegion is a registry that produced no fragment for the widget's own
// region, which means the registration and the live configuration disagree
// about what this widget is called on the wire.
var ErrNoRegion = errors.New("widgettest: the mounted widget has no fragment for its own region")

// Card is one widget mounted alone in a registry of its own: the fragment the
// live path patches, the reducer that routes to it, and the state a session
// would hold.
//
// It is not generic, and the erasure is deliberate. [Mount] takes the type
// parameter — that is the boundary — and the registry is where a widget's state
// type is already forgotten exactly once, so a second generic shell here would
// be a second place the same erasure happens.
type Card struct {
	// region is the widget's own live region, taken from its registration.
	region string

	// fragment is the live region the host patches, rather than the widget's
	// own Render: what a viewer receives is what came through here.
	fragment live.Fragment[widget.HostState]

	// reduce is the registry's router. A card drives events through it so that
	// an event named by no registration is refused here exactly as it would be
	// on the wire.
	reduce live.Reducer[widget.HostState, live.AnonymousIdentity]

	// state is this card's session state. It is a field of a value only the
	// caller's own goroutine holds; a Card is not safe to drive from two.
	state widget.HostState
}

// Mount registers one widget, mounts a session for it, and returns the card.
//
// The security posture is the anonymous one, because a card that is rendered and
// never served has no socket to authenticate: nothing here opens a listener, and
// [Mount] is the wrong tool for asserting anything about authorization.
//
// That is also why the identity type is fixed rather than a second parameter.
// A widget is generic in its host's identity type since 2026-09-03, and a card
// has no host: it instantiates on [live.AnonymousIdentity], the concrete type
// live.Anonymous produces, which is the honest identity for a session that
// never opened.
func Mount[S any](ctx context.Context, instance widget.IWidget[S, live.AnonymousIdentity]) (*Card, error) {
	registry := widget.NewRegistry[live.AnonymousIdentity]()
	if registerError := widget.Register(registry, instance); registerError != nil {
		return nil, registerError
	}

	config, configError := registry.LiveConfig(widget.MountOptions[live.AnonymousIdentity]{
		Origins:      []string{"http://127.0.0.1:0"},
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
	})
	if configError != nil {
		return nil, configError
	}

	state, _, initError := config.Init(ctx, live.Session[live.AnonymousIdentity]{})
	if initError != nil {
		return nil, initError
	}

	region := instance.Register().Region
	for _, fragment := range config.Fragments {
		if fragment.ID != region {
			continue
		}
		return &Card{region: region, fragment: fragment, reduce: config.Reduce, state: state}, nil
	}
	return nil, fmt.Errorf("%w: %q", ErrNoRegion, region)
}

// Region is the live region this card patches, and the identity every event
// sent to it must name.
func (card *Card) Region() string { return card.region }

// Apply drives the card through events in order, and returns whatever effects
// the transitions scheduled.
//
// The effects are returned rather than dropped because "this card scheduled
// none" is a thing worth asserting: a widget that schedules an effect a host
// cannot execute is a change that never happens.
func (card *Card) Apply(events ...live.Event) []live.Effect[live.AnonymousIdentity] {
	var scheduled []live.Effect[live.AnonymousIdentity]
	for _, event := range events {
		if event.FragmentID == "" {
			event.FragmentID = card.region
		}
		next, effects := card.reduce(card.state, event)
		card.state = next
		scheduled = append(scheduled, effects...)
	}
	return scheduled
}

// Render returns the card's live region as the markup a patch would carry.
func (card *Card) Render(ctx context.Context) (Rendered, error) {
	var markup strings.Builder
	if renderError := card.fragment.Render(card.state).Render(ctx, &markup); renderError != nil {
		return "", renderError
	}
	return Rendered(markup.String()), nil
}

// Deliver is one event as a stream delivers it: a wire name and the wire field
// names the widget's registration declared.
//
// It is here rather than in each specification because the field map is the
// contract between the widget and whatever fills its events, and a literal
// retyped per specification is a contract with one more copy of itself.
func Deliver(name string, fields map[string]string) live.Event {
	return live.Event{Name: name, Fields: live.NewFields(fields)}
}
