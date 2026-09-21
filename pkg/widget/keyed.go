package widget

import (
	"context"
	"fmt"
	"io"
	"reflect"
	"slices"
	"strings"

	"github.com/a-h/templ"
	"github.com/candacelabs/csf/pkg/gotth/live"
)

const keyedRegionClose = "</div>"

// KeyedItem is one instance's stable key and controlled state. A key is local
// to its collection; all instances share one widget definition and event set.
type KeyedItem[S any] struct {
	Key   string
	State S
}

// KeyedState is an immutable ordered snapshot. Its zero value is empty.
// S must also be treated immutably, exactly as live.Config requires.
type KeyedState[S any] struct{ data *keyedData[S] }

type keyedData[S any] struct {
	items []KeyedItem[S]
	index map[string]int
}

// NewKeyedState copies items, rejecting duplicate or invalid keys. A collection
// also checks the complete region length through its State constructor.
func NewKeyedState[S any](items []KeyedItem[S]) (KeyedState[S], error) {
	index := make(map[string]int, len(items))
	for position, item := range items {
		if !regionExpression.MatchString(item.Key) {
			return KeyedState[S]{}, fmt.Errorf("widget: invalid instance key %q: use the region identifier alphabet", item.Key)
		}
		if _, duplicate := index[item.Key]; duplicate {
			return KeyedState[S]{}, fmt.Errorf("widget: duplicate instance key %q: each collection member needs a unique key", item.Key)
		}
		index[item.Key] = position
	}
	return KeyedState[S]{data: &keyedData[S]{items: slices.Clone(items), index: index}}, nil
}

// Items returns a copy in display order.
func (state KeyedState[S]) Items() []KeyedItem[S] {
	if state.data == nil {
		return nil
	}
	return slices.Clone(state.data.items)
}

// Get looks up one current member.
func (state KeyedState[S]) Get(key string) (S, bool) {
	var zero S
	if state.data == nil {
		return zero, false
	}
	index, exists := state.data.index[key]
	if !exists {
		return zero, false
	}
	return state.data.items[index].State, true
}

// Upsert replaces one member without moving it, or appends a new member.
func (state KeyedState[S]) Upsert(key string, value S) (KeyedState[S], error) {
	items := state.Items()
	if state.data != nil {
		if index, exists := state.data.index[key]; exists {
			items[index].State = value
			return NewKeyedState(items)
		}
	}
	return NewKeyedState(append(items, KeyedItem[S]{Key: key, State: value}))
}

// Remove returns a new snapshot without key; a missing key leaves it unchanged.
func (state KeyedState[S]) Remove(key string) KeyedState[S] {
	if state.data == nil {
		return state
	}
	index, exists := state.data.index[key]
	if !exists {
		return state
	}
	items := state.Items()
	next, err := NewKeyedState(slices.Delete(items, index, index+1))
	if err != nil {
		// Removing a member from validated, privately stored keys cannot make
		// another key invalid or duplicate. Failure means our state bookkeeping
		// is broken; returning a plausible snapshot would conceal that bug.
		panic(err)
	}
	return next
}

// Reorder accepts exactly one permutation of the current membership.
func (state KeyedState[S]) Reorder(keys []string) (KeyedState[S], error) {
	if len(keys) != len(state.Items()) {
		return KeyedState[S]{}, fmt.Errorf("widget: reorder must name every current member exactly once")
	}
	items := make([]KeyedItem[S], 0, len(keys))
	for _, key := range keys {
		value, exists := state.Get(key)
		if !exists {
			return KeyedState[S]{}, fmt.Errorf("widget: reorder names missing member %q", key)
		}
		items = append(items, KeyedItem[S]{Key: key, State: value})
	}
	return NewKeyedState(items)
}

// KeyedCollection reuses one generated IWidget definition for controlled,
// snapshot-driven instances. It has no mutable session state and owns no IO.
// Register, Reduce, Render and Snapshot are the actual widget methods; initial
// state comes from the host snapshot, so per-instance Mount/Unmount are not
// invoked. Use it for pure generated cards, not widgets owning mount resources.
// Host Init/effects/Teardown own subscriptions and their cancellation.
//
// W preserves the concrete factory result. Neither authors nor this homogeneous
// collection erase S. Registry remains startup-only and is not modified here.
//
// # Validation and panics
//
// Call State after loading or changing membership before passing a snapshot to
// this collection. NewKeyedState and KeyedState.Upsert validate keys without a
// collection's region prefix; State also checks the combined wire identifier.
// State returns errors for invalid input. Rendering or reducing a snapshot that
// bypassed that check panics if its identifiers cannot fit this collection.
// A factory that changes its registration after construction also panics: its
// Go implementation violated the fixed identity/event contract used by gotth-live.
type KeyedCollection[S any, I live.IIdentity, W IWidget[S, I]] struct {
	region       string
	factory      func(region string) W
	registration Registration
}

// NewKeyedCollection validates a definition once at startup. factory must be
// pure, bind the supplied region, and preserve the definition's registration.
func NewKeyedCollection[S any, I live.IIdentity, W IWidget[S, I]](
	region string, factory func(region string) W,
) (*KeyedCollection[S, I, W], error) {
	if !regionExpression.MatchString(region) || len(region) > 62 || factory == nil {
		return nil, fmt.Errorf("widget: keyed collection requires a valid region and a widget factory")
	}
	registration := factory(region).Register()
	if err := registration.Validate(); err != nil {
		return nil, err
	}
	if registration.Region != region {
		return nil, fmt.Errorf("widget: keyed factory registered %q instead of assigned region %q", registration.Region, region)
	}
	return &KeyedCollection[S, I, W]{region: region, factory: factory, registration: registration}, nil
}

// Events returns the definition's browser-sendable event names. Internal stream
// events stay out of live.Config.Events, preserving default-deny ingress.
func (collection *KeyedCollection[S, I, W]) Events() []string {
	return slices.Clone(collection.registration.Events)
}

// Region derives the unique wire identity without lossy key normalization.
func (collection *KeyedCollection[S, I, W]) Region(key string) (string, error) {
	region := collection.region + ":" + key
	if key == "" || !regionExpression.MatchString(region) {
		return "", fmt.Errorf("widget: instance key %q exceeds region %q's alphabet or 64-byte budget", key, collection.region)
	}
	return region, nil
}

// State validates a loaded snapshot against this collection's region budget.
func (collection *KeyedCollection[S, I, W]) State(items []KeyedItem[S]) (KeyedState[S], error) {
	for _, item := range items {
		if _, err := collection.Region(item.Key); err != nil {
			return KeyedState[S]{}, err
		}
	}
	return NewKeyedState(items)
}

func (collection *KeyedCollection[S, I, W]) instance(key string) W {
	region, err := collection.Region(key)
	if err != nil {
		// State already checks this same key plus collection prefix. Reaching
		// this branch means the Go caller skipped collection validation; these
		// callback signatures cannot return a validation error. Do not silently
		// drop the widget or generate an identifier the wire protocol rejects.
		panic(err)
	}
	instance := collection.factory(region)
	registration := instance.Register()
	expected := collection.registration
	expected.Region = region
	if !reflect.DeepEqual(registration, expected) {
		// The factory is caller-supplied Go code, not browser input. A changed
		// registration contradicts the identities and event permissions fixed
		// at startup. Continuing would dispatch against a different contract.
		panic(fmt.Errorf("widget: instance %q changed its definition registration: preserve name, events and assigned region", key))
	}
	return instance
}

// Lookup addresses only an exact member of the current snapshot. It rejects
// removed IDs even if their browser event was queued before the removal patch.
func (collection *KeyedCollection[S, I, W]) Lookup(state KeyedState[S], region string) (string, S, bool) {
	var zero S
	key, owned := strings.CutPrefix(region, collection.region+":")
	if !owned {
		return "", zero, false
	}
	value, exists := state.Get(key)
	return key, value, exists
}

// Reduce routes only declared events to the addressed live instance. The
// browser boundary additionally refuses Internal names before dispatch.
func (collection *KeyedCollection[S, I, W]) Reduce(state KeyedState[S], event live.Event) (KeyedState[S], []live.Effect[I]) {
	notice := event.Name == live.SlowClientEvent || event.Name == live.ClientRecoveredEvent || event.Name == live.EffectFailedEvent
	if notice && event.FragmentID == "" {
		items := state.Items()
		var effects []live.Effect[I]
		for index, item := range items {
			next, emitted := collection.instance(item.Key).Reduce(item.State, event)
			items[index].State = next
			effects = append(effects, emitted...)
		}
		next, err := NewKeyedState(items)
		if err != nil {
			// Only values changed above; the validated keys and their order did
			// not. An invalid key set here indicates an internal bookkeeping bug.
			panic(err)
		}
		return next, effects
	}
	key, value, exists := collection.Lookup(state, event.FragmentID)
	if !exists || (!notice && !slices.Contains(collection.registration.Events, event.Name) && !slices.Contains(collection.registration.Internal, event.Name)) {
		return state, nil
	}
	next, effects := collection.instance(key).Reduce(value, event)
	updated, err := state.Upsert(key, next)
	if err != nil {
		// Lookup supplied an existing validated key. Replacing only its value
		// cannot fail key validation; a failure means our state invariant broke.
		panic(err)
	}
	return updated, effects
}

// Snapshot asks the addressed widget for its named state projection.
func (collection *KeyedCollection[S, I, W]) Snapshot(state KeyedState[S], key string) (Snapshot, bool) {
	value, exists := state.Get(key)
	if !exists {
		return Snapshot{}, false
	}
	return collection.instance(key).Snapshot(value), true
}

// RenderItem renders one generated widget at its assigned region. This lets a
// host compose columns or other structural markup in the parent fragment.
func (collection *KeyedCollection[S, I, W]) RenderItem(state KeyedState[S], key string) templ.Component {
	value, exists := state.Get(key)
	if !exists {
		return templ.NopComponent
	}
	return collection.instance(key).Render(value)
}

// Render is the default parent, a div containing members in snapshot order.
func (collection *KeyedCollection[S, I, W]) Render(state KeyedState[S]) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		if _, err := fmt.Fprintf(writer, `<div data-gotth-region="%s">`, collection.region); err != nil {
			return err
		}
		for _, item := range state.Items() {
			if err := collection.RenderItem(state, item.Key).Render(ctx, writer); err != nil {
				return err
			}
		}
		_, err := io.WriteString(writer, keyedRegionClose)
		return err
	})
}

// Children declares independently dirty regions, without registering anything
// at runtime. Use Fragment or KeyedFragment to mount them in live.Config.
func (collection *KeyedCollection[S, I, W]) Children(state KeyedState[S]) []live.Fragment[KeyedState[S]] {
	children := make([]live.Fragment[KeyedState[S]], 0, len(state.Items()))
	for _, item := range state.Items() {
		key := item.Key
		instance := collection.instance(key)
		children = append(children, live.Fragment[KeyedState[S]]{
			ID:     instance.Register().Region,
			Render: func(current KeyedState[S]) templ.Component { return collection.RenderItem(current, key) },
			Dirty: func(previous, next KeyedState[S]) bool {
				before, existed := previous.Get(key)
				after, exists := next.Get(key)
				return existed != exists || !reflect.DeepEqual(before, after)
			},
		})
	}
	return children
}

// Fragment mounts this definition once; sessions carry only KeyedState values.
func (collection *KeyedCollection[S, I, W]) Fragment() live.Fragment[KeyedState[S]] {
	return live.Fragment[KeyedState[S]]{
		ID: collection.region, Render: collection.Render, Children: collection.Children,
		Dirty: func(previous, next KeyedState[S]) bool {
			before, after := previous.Items(), next.Items()
			if len(before) != len(after) {
				return true
			}
			for index, item := range before {
				if item.Key != after[index].Key {
					return true
				}
			}
			return false
		},
	}
}

// KeyedFragment projects a collection into its enclosing gotth-live state. A host
// may override the returned Render and Dirty for columns or lane changes, while
// retaining Children for targeted card patches. Render must include all members.
func KeyedFragment[B any, S any, I live.IIdentity, W IWidget[S, I]](
	collection *KeyedCollection[S, I, W], project func(state B) KeyedState[S],
) live.Fragment[B] {
	return projectKeyedFragment(collection.Fragment(), project)
}

func projectKeyedFragment[B any, S any](fragment live.Fragment[KeyedState[S]], project func(state B) KeyedState[S]) live.Fragment[B] {
	result := live.Fragment[B]{
		ID:     fragment.ID,
		Render: func(state B) templ.Component { return fragment.Render(project(state)) },
		Dirty:  func(previous, next B) bool { return fragment.Dirty(project(previous), project(next)) },
	}
	if fragment.Children != nil {
		result.Children = func(state B) []live.Fragment[B] {
			children := fragment.Children(project(state))
			result := make([]live.Fragment[B], len(children))
			for index, child := range children {
				result[index] = projectKeyedFragment(child, project)
			}
			return result
		}
	}
	return result
}
