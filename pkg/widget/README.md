# widget

The **widget SDK**: the contract a widget implements, the registry a host binary
mounts them through, and a small Mermaid dialect for declaring one, with the
interpreter that turns it into a typed, resolved IR a generator emits from. You
write fourteen blocks in a fixed order; the interpreter resolves every reference
to a handle, derives the three things an author must not be able to contradict,
and reports every mistake with a line, a column, a class and a repair.

**It is v0.1.** The API makes no compatibility commitment yet: the IR records,
the finding vocabulary, the widget contract and the entry points are all
expected to move as the first real widgets land. `dialect 0` is a hard version
pin — an interpreter that implements version *n* refuses any other version
rather than guessing which constructs it can still handle — so a document and
an interpreter are never quietly mismatched even while both are moving.

The language is a Mermaid dialect for a measured reason rather than an
aesthetic one: an invented DSL starts an agent below 20% accuracy on syntax
unfamiliarity alone, and mermaid's training-data presence means a dialect pays
tokens only for its custom vocabulary.

---

## The SDK, in one page

A widget is a vertical slice — its own state, its own events, its own live
region, its own UI — and every widget a host serves runs inside one process.
`IWidget[S, I]` is the contract — S is the widget's own state type and I is the
HOST's identity type, threaded through and never read — and its six methods are
the lifecycle phases of
[`docs/ontology.md`](docs/ontology.md) rather than a shape chosen for
convenience: `Register`, `Mount`, `Reduce`, `Render`, `Unmount`, and
`Snapshot`, which a host reads without knowing the widget's type. Effects are
returned as `live.Effect` values whose `Run` functions perform the work; there
is no `Effect` method on the widget interface. `S` is the
widget's own state type and every phase is written in it, so nothing an author
writes ever holds an untyped state. Only the registry holds widgets of several
different `S` at once, and `widget.Register` is where — once, behind a generic
shell, in an unexported adapter — that is erased.

`tick` is the one phase with no method of its own, and its absence is the
ontology's reading rather than an omission: a tick is what a `Stream` delivers
without a user, so it reaches `Reduce` as the event the stream carries — the
same way an effect failure does, so a reducer sees every cause in one switch.

**A live UI session means one accepted gotth-live WebSocket connection and the
server-side state maintained for that connection.** In this README, "session"
always means that connection's lifetime, not a login or a stored agent
conversation. Each connection gets an initial state value for each mounted
widget; its reducer and renderer run serially on the connection's owning
goroutine. A reconnect creates a new session and initializes state again;
restoring durable application data is the host's responsibility. Separate
state values can still refer to shared objects: this is not deep copying or
memory isolation. See the [connection implementation](../gotth/internal/wsx)
and [widget initialization](registry.go).

**Widget count does not determine connection count.** A date widget and a widget
with many asynchronous workers share the same WebSocket when mounted on the same
live page. `live.App.ActiveConnections()` counts that connection once. The
service that owns connection state (currently named `Actor` in
[`internal/session/actor.go`](../gotth/internal/session/actor.go)) serializes all
of its widget reducers and renderers on one goroutine; effects do their
asynchronous work on additional goroutines. Their count is a separate measurement.
A consumer explicitly
opening another transport creates another connection; widget mounting does not.

The host **app** is the runnable composition that owns the application process.
gotth-live acts as an in-process **service**, managing live connections and their
goroutines. That service can outlive many individual connections. Widget
`Mount`/`Unmount` follow each connection's lifetime; shared application services
retain their own owners and lifetimes. The current bounded cleanup behavior is
described below.

```go
registry := widget.NewRegistry()
widget.MustRegister(registry, nodestatus.NewNodeStatus())

config, err := registry.LiveConfig(widget.MountOptions{
    Origins:      []string{"http://127.0.0.1:8080"},
    Authenticate: live.Anonymous, // production replaces all three
    Authorize:    live.AllowAll,
    CSRF:         live.NoCSRFCheck,
})
app, err := live.New(config)
```

`LiveConfig` is the whole of "mounting a widget into a host": one gotth-live
fragment per widget, the union of their browser-sendable event names, and a
reducer that routes by region first and wire name second, broadcasting only what
names neither — which is the library's own effect-failure and slow-client
notices.

**`Events` and `Internal` are the two sides of one line.** A generated
registration puts an event a declared stream delivers in `Internal` and
everything else in `Events`, and `LiveConfig` registers only `Events` with the
live library while routing both. Registration is the only thing that makes a
wire name sendable by a browser, so an event with a server-side source is
refused before any reducer runs — otherwise a browser could post the source's
own truth and the widget could not tell the two apart.

**`Payloads` names what each event carries.** A generated widget emits one
constant per declared payload field and repeats the same names as data on the
registration, because the field names are a contract with whatever fills the
event. Renaming a field in the document is then a compile error at every site
that fills it, rather than a card that silently stops updating.

There is no per-widget port, container or connection. Widgets share the host's
address space; the session serializes reducer work and effects can run
concurrently. Mounting a widget does not allocate a private heap or require a
dedicated goroutine per widget.
[`examples/widget/`](../../examples/widget) is the smallest host that does it.

### Shared memory, ownership and cleanup

A widget is an in-process composition unit with per-session state and lifecycle
hooks. Those hooks do not create a memory-isolation boundary. Its interface
specifies operations, not an implementation strategy or automatic resource
ownership. The registry reuses registered widget implementations across
sessions; mutable receiver fields or injected dependencies can therefore be
shared even when each session has its own state value.

| Concern | Consumer contract |
|---|---|
| State passed by value | Copying a struct, slice, map or pointer does not recursively copy referenced data. Reducers must not mutate prior state; copy the parts that change or use immutable values. |
| Concurrent effects | Return results through events instead of mutating reducer-owned state. Shared mutable dependencies need an explicit owner or synchronization protocol: channels, locks or appropriate atomics. |
| Read-only sharing | Data initialized before publication and never mutated afterward may be shared without a mutex. Exclusive ownership transfer is another option, provided the sender stops using the transferred mutable data. |
| Borrowed resources | Passing a resource does not transfer cleanup responsibility automatically. Its owner must keep it usable until all authorized users finish; a child must not close a borrowed pool or clear shared state on its own exit. |
| Shared reads and writes | If one goroutine changes data while another accesses the same data, synchronize those accesses. When using a mutex, readers and writers must use the same mutex. Locking only the pointer handoff does not protect subsequent access to its contents. |
| Clearing a pointer | `thingy = nil` assigns to the variable `thingy`; `*thingy = nil` writes to the location it points to, when the stored type permits `nil`. A shared location needs synchronization with readers in either case. Clearing one pointer slot does not clear other copies of the pointer or wait for users of the old object. |
| Garbage collection | Ending a goroutine or unmounting a widget does not free everything it used. Ordinary Go objects remain alive while reachable. Unreachable memory becomes eligible for collection; neither immediate reclamation nor return to the OS is guaranteed. |
| External resources | Files, sockets, subscriptions and foreign allocations require their documented cleanup. A reachable Go wrapper does not guarantee that its underlying resource is still open or valid. |

For resources used by workers, the ownership protocol is: stop accepting new
work, request cancellation, perform any documented action needed to unblock
work, wait for affected workers to finish, then release resources they could
still use and drop retained references. Cancellation is a request, not proof
of completion. Clearing fields before users finish can introduce a race or a
nil dereference; closing a resource too early can cause a logical failure even
when its `Close` method is concurrency-safe.

**Current shutdown boundary:** gotth-live's
[`Actor.shutdown`](../gotth/internal/session/actor.go) waits for effects only up
to `EffectDrainTimeout`, records an abandoned effect if the deadline expires,
then calls teardown. The registry calls `Unmount` in reverse registration
order. An effect that ignores cancellation can therefore outlive `Unmount`;
neither that hook nor a closed session establishes that all effects have
finished. Application-created goroutines are not automatically tracked either.
Keep resources used by outstanding workers valid until their users finish;
the current SDK does not enforce that ownership protocol for arbitrary widget
implementations.

These rules follow the [Go memory model](https://go.dev/ref/mem),
[garbage collector guide](https://go.dev/doc/gc-guide), and
[`context.CancelFunc` contract](https://pkg.go.dev/context#CancelFunc).
Race-enabled tests can detect races on exercised paths; they do not prove
correct shutdown ordering or the absence of all races.

### Channel ownership discipline

Prefer a single owning goroutine and typed channel messages when coordinating
mutable state, queued work or lifecycle transitions. A mutex is appropriate for
a short synchronous operation on shared state. Immutable snapshots can be
shared after safe publication. Choose the simplest protocol that makes the
owner and completion visible; a widget does not need a dedicated goroutine
just to implement this convention. This follows Go's
[mutex-or-channel guidance](https://go.dev/wiki/MutexOrChannel).

For an API that **transfers ownership**, document this agreement:

| Stage | Ownership rule |
|---|---|
| Before sending | The sender owns the payload's mutable data and finishes any prior uses before handing it off. |
| Handoff | A successful send transfers the payload ownership specified by the API. A buffered send means the channel has accepted the value, not that processing has finished. If a cancellation branch wins instead of sending, ownership stays with the sender. |
| After sending | The sender must not read, mutate, clear, close or recycle the transferred mutable payload through any retained alias. The receiver may already be using it. |
| Receiving | The receiver owns subsequent mutation and whatever cleanup responsibility the API explicitly transfers. Borrowed dependencies, such as a host-owned database pool, retain their original owner. |
| Returning ownership | Use an explicit reply or other documented handoff before the original sender resumes access. A receipt acknowledging submission is not completion or return of ownership. |
| Sharing instead | If both sides need concurrent access, declare immutable sharing or a shared synchronization protocol. Sending a pointer does not itself authorize concurrent mutation. |

Go channel sends copy values. Sending a pointer leaves both pointer copies
valid and pointing to the same object; sending a slice or map does not deep-copy
its backing data. **The ownership transfer is an API discipline, not pointer
invalidation enforced by Go.** Setting one variable to `nil` does not revoke
other aliases. This resembles the intent of a move-based API, but C++
[`std::move`](https://eel.is/c++draft/forward) itself is a cast; the receiving
operation and type determine whether ownership actually moves.

Document the payload, owner, borrowed dependencies, completion signal and
shutdown behavior at each asynchronous boundary. These are implementation and
review obligations. The repository's `GOROUTINE-SHARED-STATE` advisory locates
captured writes and later caller accesses without locally matched mutex
sections or a recognized WaitGroup completion pattern. It does not follow arbitrary aliases or library lifecycle callbacks,
and cannot prove that a channel or mutex protocol is correct.

**A widget's region is a landmark.** The generated root is an `<aside>` carrying
`aria-labelledby` pointing at its own title's `id`, both spelled from the same
exported `<Widget>TitleID` constant. Both halves are required rather than
decorative: HTML-AAM maps a nameless `<aside>` inside sectioning content to
`generic`, so a landmark with no accessible name is not a landmark, and a
screen-reader user can neither jump to the widget nor skip it. `role` is not
emitted — the element already carries it, and ARIA that restates HTML is one
more thing that can disagree with it. Inside the chrome, the source line is
emitted before the title, because that is the order it is read in: DOM order is
reading order, and a host stylesheet that reversed it visually would be a
reading order only sighted users get.

**A browser event carries a region, and the region is not optional.** The
`gotthlive.v1` `Event` frame requires `fragment_id` to be non-empty, at most 64
characters and to match `^[A-Za-z0-9_:.-]+$`
([`pkg/gotth/docs/protocol.md § 3.2`](../gotth/docs/protocol.md)), so an event
sent without one is refused at the frame boundary and never reaches a reducer, a
widget or the registry's routing. It is not a check this SDK performs and not one
it can relax.

That string is the widget's own `region` directive — the identity `W108`
constrains to the same character set and the same 64 — so one spelling is
checked at both ends of the wire, and a generated widget exports it as
`<Widget>Region` for a caller to pass:

```go
client.Send(nodestatus.NodeStatusEventHealth, nodestatus.NodeStatusRegion, fields)
```

This is also why a deployed region identity may not be renamed casually: every
patch on the wire names it, and so does every event coming back.

One optional interface sits beside the contract. `IDirtyDeclarer[S]` lets a
widget say which state changes its own region's markup depends on; a widget that does
not implement it gets a whole-state comparison, which is always safe and never
reports equal for two states that differ. A generated widget implements it from
its document's computed dirty projection, so the declaration is derived from the
same source the render is.

## Generating a widget

**What `internal` means in Go:** only packages inside the directory tree rooted
at the parent of `internal` may import its packages. Here, that parent is
`pkg/widget`. Thus `pkg/widget/internal/cmd/widgetc` may import
`pkg/widget/internal/uigen`: they are different packages, but both are inside
the permitted tree. A package outside `pkg/widget` cannot import `uigen`, even
from the same repository or Go module. This is an import boundary enforced by
Go, not a process boundary or a restriction to a single package.

`internal/uigen` emits a `.templ` view and a Go scaffold implementing the
contract, from one resolved document. `gen.sh` writes every generated widget in
this checkout and, with `--check`, asserts the committed output is byte-identical
to a fresh generation. From the export root, inside the toolchain container:

```
docker run --rm -v "$PWD:/workspace" -w /workspace \
    dis-gotth-live:latest bash pkg/widget/gen.sh          # or: gen.sh --check
```

Every exemplar that validates generates — the flagship raft card, the minimal
status card and the relay pipeline — and `gen.sh` writes all three. The flagship
is what makes the list a check rather than a formality: every block of the
dialect appears in it, so a construct the generator stops emitting fails on the
document that uses it. The relay pipeline was verified by hand during the P2
audit and asserted by nothing, which is the same thing as uncovered. Edges, channels, orbits, motion, legend, indicator
and control all come out of it. What it still refuses is a control whose trigger
is `change`, `input` or `submit` — a control declares no element kind, so those
three have nothing to bind to and a binding on a button could never fire.
`uigen.Refusals()` names the list, and `Generate` reports every construct it met
rather than the first.

## The interpreter's API

```go
document, findings := widget.Interpret("card.widget", source)
if len(findings) > 0 {
    for _, finding := range findings {
        fmt.Print(finding) // card.widget:56:3: W401: … \n    fix: …
    }
    return // nothing generates before it validates
}
```

`InterpretFile` is the same call with the read done for you; its error is
returned only when the file could not be read at all, which is a failure of its
own rather than a clean run.

Three properties hold of every call:

1. **Both passes run to completion.** The findings are every finding, never the
   first, sorted by `(line, column, class)` so two runs print byte-identical
   output. An author who fixes one error and re-runs to find the next learns
   that the tool tells them a fraction of the truth, and starts guessing ahead
   of it.
2. **Every finding is anchored, classified and repairable.** It names its
   subject, states what is wrong in the present indicative, and ends with one
   imperative `fix:` naming the exact spelling to write.
3. **A document is sound only when no finding came with it.** The IR is
   returned either way — recovery is total, so tooling can still show what
   parsed — but generating from an unsound document is generating from a guess.

## The document, in twenty lines

```widget
widget NodeStatus
dialect 0
region "widget.node-status"
palette fieldStation

state
  field reachable type flag
end

bindings
  binding statusText
    when reachable then "reachable"
    otherwise "unreachable"
  end
end

labels
  label titleLabel text "Node status"
  label statusLabel binds statusText
end
```

…and on through fourteen blocks in one fixed order, which is also the
resolution order. [`docs/examples/02-node-status.widget`](docs/examples/02-node-status.widget)
is the whole minimal document; [`docs/examples/01-cluster-heartbeats.widget`](docs/examples/01-cluster-heartbeats.widget)
is the widest one.

## What the IR guarantees

`Document` is **resolved** (every reference is a handle, so a generator needs
no error path for an unknown name), **total** (no field's absence means "work it
out"), **ordered** (every collection is a sequence in declaration order, because
a render must be byte-identical for equal state), **anchored** (every record
carries a `SourceSpan`) and **closed** (nothing in it names a path, a host, an
address or a credential).

Three records are computed rather than parsed — `EdgeGeometry`, `Legend` and
`DirtyProjection`. They are in the IR so a generator need not re-derive them,
and absent from the grammar so an author cannot contradict them.

## What it does not do

The interpreter does not render or serve, and does not resolve a palette: a
document names seven semantic tokens — `surface`, `ink`, `muted`, `rule`,
`accent`, `positive`, `warning` — and the design system owns their values. It
does refuse a `palette` directive naming a palette that does not exist (`W208`),
because the set of names is closed even where the values are not somebody
else's. It
does not compile the host's connection status into a motion gate; `Motion`
carries `HostStatusGate` so a generator reads that obligation rather than
remembering it.

Resolving those seven names is the *host's* job and the SDK ships one palette
for it: `widget.PaletteByName` takes the name a document declared — a generated
widget exports it as a constant — and `widget.Stylesheet(palette)` returns that
palette's values, the token classes that read them, the scene's structure, the
motion gate and the reduced-motion rule. An unknown palette name is refused
rather than defaulted, because a fallback renders a plausible-looking widget in
the wrong colours.

The SDK does not open a stream. A `Registration` carries the streams a widget
declared and the host resolves each source name against the data plane it has,
because a widget document names no host, no address and no credential by
construction — which is what makes one publishable.

## Reading order

| | Document | What it settles |
|---|---|---|
| 1 | [`docs/dialect.md`](docs/dialect.md) | The surface syntax: the fourteen blocks, the four Anka rules, the document-level IR |
| 2 | [`docs/examples/`](docs/examples) | Four commented documents, the last of which does not validate on purpose |
| 3 | [`docs/errors.md`](docs/errors.md) | 70 error classes, each with its anchoring rule, message template and named fix |
| 4 | [`docs/ontology.md`](docs/ontology.md) | The 25 typed concepts the syntax is a surface for |
| 5 | [`docs/inventory.md`](docs/inventory.md) | The 135 concepts harvested from shipped code, with provenance |
| 6 | [`examples/widget/`](../../examples/widget) | A host that registers a generated widget and serves it |

An agent authoring a widget for the first time should read `docs/dialect.md`
and example 01, in that order, and reach for `docs/errors.md` only when the
validator names a class.

## Contributor tooling

`internal/cmd/widgetc` validates documents from a shell during development:

```
go run ./internal/cmd/widgetc validate docs/examples/*.widget
go run ./internal/cmd/widgetc generate -package nodestatus \
    -out examples/widget/nodestatus docs/examples/02-node-status.widget
```

It exits 0 clean, 1 on findings or a document that cannot be generated, and 2
when a document could not be read or the command line was wrong — an unrun check
must never read as a pass. **A script that reads the exit status must build the
binary rather than `go run` it**: `go run` reports a non-zero child status as its
own exit 1, so the two lines above collapse into one and "I could not read your
document" becomes indistinguishable from "your document is wrong".

`generate` refuses a document that reported anything and prints the findings
rather than only the refusal, because nothing generates before it validates.

It is not a product surface: a consumer of this package calls `Interpret` and
implements `IWidget[S, I]`, and `gen.sh` is what runs the generator here.

## Multiple instances of one definition

Use a typed keyed collection when a host snapshot contains many instances of the
same generated widget:

```go
cards, err := widget.NewKeyedCollection[CardState, Identity](
    "board.cards", generated.NewCardAt[Identity],
)
fragment := widget.KeyedFragment(cards, func(state BoardState) widget.KeyedState[CardState] {
    return state.Cards
})
```

The collection reuses actual generated widget methods, keeps registration fixed,
and adds, removes or reorders members through immutable state. Card changes patch
their own regions; structural changes patch the collection parent over the same
socket. Check construction errors before mounting. The
[keyed collection guide](docs/keyed-collections.md) includes concrete generated
consumer snippets, the host subscription/cleanup contract, and links to its
compiling WebSocket regression tests.
