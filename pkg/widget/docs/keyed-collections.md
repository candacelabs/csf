# Keyed widget collections

A collection mounts one widget definition once and gives each current member its
own region on the existing gotth connection. The host owns ordered snapshots;
the generated widget owns each card's reducer, rendering and named state
projection. Registration stays a startup operation.

The snippets below use the actual generated `ClusterHeartbeats` widget. The
[compiling consumer and WebSocket tests](../keyed_test.go) exercise these calls,
including updates, removal, reorder, reconnect and subscription cleanup.

## Construct the definition and load members

```go
type CardState = clusterheartbeats.ClusterHeartbeatsState

cards, err := widget.NewKeyedCollection[CardState, live.AnonymousIdentity](
    "board.cards", clusterheartbeats.NewClusterHeartbeatsAt[live.AnonymousIdentity],
)
if err != nil {
    return err
}
state, err := cards.State([]widget.KeyedItem[CardState]{
    {Key: "a", State: CardState{Term: 1}},
    {Key: "b", State: CardState{Term: 2}},
})
```

`State` checks duplicate keys and the complete region identity. A member's
identity is `collection-region:key`, using the existing region alphabet and
64-byte limit; keys are never silently normalized. Generated `New…At` constructors
bind both the widget region and its accessible title and animation IDs.

Snapshots are immutable containers. Treat values held inside `CardState`
immutably too. Updating a member preserves its position; adding appends; reorder
must name exactly the current members:

```go
updated, err := state.Upsert("a", CardState{Term: 3})
added, err := updated.Upsert("c", CardState{Term: 4})
reordered, err := added.Reorder([]string{"c", "a", "b"})
removed := reordered.Remove("a")
```

Check each error before using the resulting snapshot. A committed source update
can instead replace the whole ordered snapshot with another `cards.State(items)`.

## Mount on the existing live application

A standalone collection uses the ordinary typed live configuration:

```go
config := live.Config[widget.KeyedState[CardState], live.AnonymousIdentity]{
    Init: func(ctx context.Context, session live.Session[live.AnonymousIdentity]) (
        widget.KeyedState[CardState], []live.Effect[live.AnonymousIdentity], error,
    ) {
        return state, nil, nil
    },
    Reduce:       cards.Reduce,
    Fragments:    []live.Fragment[widget.KeyedState[CardState]]{cards.Fragment()},
    Events:       cards.Events(),
    Origins:      []string{"http://localhost"},
    Authenticate: live.Anonymous,
    Authorize:    live.AllowAll[live.AnonymousIdentity],
    CSRF:         live.NoCSRFCheck,
}
app, err := live.New(config)
```

These security hooks deliberately opt out for the local example. A host supplies
its existing authentication and authorization. `Events()` includes only the
widget's browser events: internal snapshot/stream events remain unavailable to a
browser. Collection reduction additionally checks the addressed current member
and declared event name; an event queued for a removed card cannot reach another
card. `Lookup(state, event.FragmentID)` supports host-owned actions with the same
membership check. Payload fields never select the member.

For a board with other state, project its cards and retain the dynamic children:

```go
fragment := widget.KeyedFragment(cards, func(state BoardState) widget.KeyedState[CardState] {
    return state.Cards
})
fragment.Render = renderBoard
fragment.Dirty = boardStructureChanged
```

`renderBoard` renders the parent region and calls
`cards.RenderItem(state.Cards, key)` in its column containers.
`boardStructureChanged` compares column membership, order and other parent
markup. A dirty parent emits one complete parent patch. Otherwise each child's
own dirty projection is checked and only changed children patch. Membership and
order changes also force a parent render. Snapshots and reconnects contain the
complete parent, so no card requires a separate socket or registration call.
The protocol's existing update-count limit is handled by falling back to parent
renders for a bulk change; existing HTML/frame resource limits still apply.

## Keep subscriptions with the host

This is a **controlled collection for pure generated widgets**. It calls their
`Register`, `Reduce`, `Render` and `Snapshot` methods. Per-member `Mount` and
`Unmount` are not invoked: the source snapshot supplies their initial state,
and members must own no IO resources. Removing a card drops its state and render
identity. Widgets that need mount resources belong in the existing registry.

The host's `Init` returns a subscription effect. Load fresh data in that effect,
then emit an internal snapshot event; the reducer decodes the event into
`cards.State(items)` without reading the store. The compiling socket fixture uses
this cancellation shape:

```go
Run: func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
    defer unsubscribe()
    for {
        select {
        case <-ctx.Done():
            return nil
        case <-changes:
            event, err := loadSnapshotEvent(ctx)
            if err != nil {
                return err
            }
            if err := emit(event); err != nil {
                return err
            }
        }
    }
},
```

`changes`, `unsubscribe` and `loadSnapshotEvent` belong to the host's existing
committed-change source. Reconnect runs `Init` again to load current state and
starts one new subscription. Session cancellation stops the old effect; the
host's optional `Teardown` receives the final state. The
[real socket regression](../keyed_test.go) waits for both subscription exit and
teardown after each connection closes.
