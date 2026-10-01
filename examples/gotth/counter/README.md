# The counter example

The smallest complete gotth-live application: a number that lives in Go, four
buttons that change it, and every open tab kept in step by the server.

It is the app [`pkg/gotth/docs/bench/equivalence-spec.md` §2.1](../../../pkg/gotth/docs/bench/equivalence-spec.md)
specifies as **C-B** (features F-CTR-1..7), so the Phase 5 benchmark measures
this rather than a second counter written to be measured. It satisfies PRD
FR-60 and, with its Ginkgo suite, FR-63.

---

## Run it

The host needs no Go. Build the library's own toolchain image once — `dis run`
is not the recipe any more, because the dis workspace is `pkg/gotth/` and this
example is that directory's sibling — then run from the export root, which is
this repository's root:

```bash
docker build -t dis-gotth-live:latest pkg/gotth/.dis
docker run --rm -v "$PWD:/workspace" -w /workspace/examples/gotth/counter \
    -p 8080:8080 dis-gotth-live:latest go run . -addr 0.0.0.0:8080
```

Or, if you have Go 1.26 locally:

```bash
cd examples/gotth/counter
go run .                    # http://127.0.0.1:8080
go run . -addr 127.0.0.1:9000
go run . -origin http://192.168.1.10:8080   # allow another browser Origin
go run . -provenance        # print the causal log for every transition
```

There is no `npm install`, no bundler, and no code generation step to run: the
client runtime is compiled into the binary and served by the same handler that
serves the WebSocket. The only generated file here is `view_templ.go`, and it
is committed.

## What to expect

Open <http://127.0.0.1:8080>.

1. The page renders with the number already in it — that first paint is
   server-rendered HTML, not a placeholder. The connection dot goes
   **connecting → live**.
2. Click **+1**. The number, the `even`/`odd` label, the coloured badge and the
   "changed just now" line all update. Nothing in your browser holds that
   value.
3. **Open a second tab.** Both say *"2 tabs sharing this counter"* — the first
   tab repainted when the second one merely connected. Click in either tab and
   both numbers move. The second tab shows *"changed just now by another tab"*.
4. **Reload.** The count survives, because it was never in the browser.
5. Kill the server. The dot goes **closed** and the page freezes at the last
   patch it applied — still scrollable, still focusable, just not updating.
   (Reconnection is checkpoint 3's; the runtime classifies the close and
   stops.)

## Where to look in the source

| File | What is in it |
|---|---|
| [`counter.go`](counter.go) | `State`, the pure `Reduce`, the derived display, and the `live.Config` — all the application logic |
| [`store.go`](store.go) | The shared counter every session reads and writes, and the subscription that pushes changes |
| [`view.templ`](view.templ) | Two fragments and the page, with `live.Region`, `live.On` and `app.Document` |
| [`main.go`](main.go) | Flags, routing, the Origin allowlist, graceful shutdown |
| [`counter_test.go`](counter_test.go) | 52 specs, including the two `livetest` determinism helpers |

## The loop, once through

A click travels this path. Nothing on it is specific to the tab that clicked,
which is why the other tab repaints for free.

```
browser                     session goroutine            store (application-owned)
   │
   │ data-gotth-on="click:counter.increment"
   │──── Event frame ────────▶ Authorize
   │                           Reduce(state, ev)
   │                             → state unchanged
   │                             → ChangeEffect{Op: OpAdd, Delta: 1}
   │                                    │
   │                                    ▼ actor boundary
   │                                  Execute ──────────▶ Apply: value++, version++
   │                                                      broadcast to every session
   │                                                             │
   │                           Emitter(counter.sync) ◀────────────┘
   │                           Reduce(state, sync)
   │                             → Value = 1
   │                           Dirty says counter.value moved
   │                           render, hash, compare
   │◀─── Patch frame ──────────  one fragment's markup
   │
   morph
```

Three details that are decisions rather than accidents:

**The reducer never changes the value.** It returns a `ChangeEffect` and finds
out the result the same way every other tab does. That is what
"server-authoritative" means concretely, and it is why two tabs cannot
disagree.

**One event name per button, not one name with a `delta` field.**
`Config.Events` is default-deny: an unregistered name is refused with
`UNKNOWN_EVENT` before the reducer runs. Four names bound what a hostile client
can ask for. One name and a number bound nothing.

**A sync carries the whole snapshot, not a delta.** Emitted events are
best-effort — a full mailbox drops one and tells the effect so. A dropped delta
leaves a session wrong forever; a dropped snapshot is superseded by the next
one. `applySync` also drops a snapshot older than the one it holds, so
out-of-order delivery repairs itself.

## Security posture

`Config.Origins` is a **real allowlist**, derived from `-addr`, never
`live.AnyOrigin`. A request whose `Origin` is not on it is refused with 403
before any per-session memory is allocated, and a request with no `Origin` at
all is refused too — an absent Origin is not an allowed one.

The three escape hatches this example does use are each there because a counter
demo has no accounts, and each is named so an audit is one `grep`:

| Hatch | Why here | What production sets instead |
|---|---|---|
| `live.Anonymous` | no user database to derive an identity from | the session cookie or bearer token the app already trusts |
| `live.AllowAll` | no per-identity rule about who may count | the check that says which identities may change what |
| `live.NoCSRFCheck` | safe **only because** `Origins` is a real allowlist — the origin check is then the whole CSRF posture | a token bound to the authenticated application session |

`Config.Dev` is `true` here, which puts stack traces in error frames. It must
be `false` in production.

## Provenance: from a click to the markup it produced

Run with `-provenance` and every transition emits one JSON record on the
`gotthlive.provenance` logger ([`pkg/gotth/docs/instrumentation.md` §4A](../../../pkg/gotth/docs/instrumentation.md)).
The fields to follow are `event_id` → `transition_id` → `patch_id`.

```bash
go run . -provenance | grep gotthlive.provenance
```

One click produces **two** records, and the pair is the whole design visible in
a log:

```json
{"msg":"transition","logger":"gotthlive.provenance",
 "session_id":"a9a9904e3445571d376c68f5864b74d1",
 "event_id":11,"client_ref":11,"transition_id":23,"state_version":14,
 "patch_id":0,"server_seq":0,
 "origin_kind":"CLIENT_EVENT","origin_source":"event:counter.increment",
 "fragment_ids":null}

{"msg":"transition","logger":"gotthlive.provenance",
 "session_id":"a9a9904e3445571d376c68f5864b74d1",
 "event_id":0,"client_ref":0,"transition_id":24,"state_version":15,
 "patch_id":13,"server_seq":13,
 "origin_kind":"EFFECT","origin_source":"effect:counter.watch",
 "fragment_ids":["counter.value"]}
```

Read it as two lines of one story.

The **first** record is the click. `origin_kind` is `CLIENT_EVENT` and
`origin_source` names the event, so `client_ref: 11` ties it back to the
eleventh interaction that browser sent. Its `patch_id` is **0**: the reducer
returned an effect and changed no state, so no markup moved and nothing was
sent. A transition that emits no patch still gets a record — without it, the
transitions that produced nothing would be invisible and "the state version
rises exactly when state changed" would be unverifiable.

The **second** record is the consequence. `origin_kind` is `EFFECT` and
`origin_source` is `effect:counter.watch` — the subscription pump, which is the
`EffectSource()` string `store.go` declares. It carries `patch_id: 13` and
`fragment_ids: ["counter.value"]`, so the markup the user is now looking at is
attributable to exactly one patch, on exactly one fragment.

To go the other way — from a patch a browser captured back to its cause — take
the `patch_id` off the frame, find the record, read its `origin_source`, and if
it is an effect, find the `CLIENT_EVENT` record whose `transition_id` precedes
it in the same session.

**A second tab's records look identical except for `session_id`.** Every
session is patched from its own transition; there is no shared render and no
broadcast frame. That is the property that makes the per-session numbers in the
benchmark mean what they say.

Leaving `Config.Logger` nil disables all of this. The frames still carry the
causal chain either way — what is lost is the server-side index that makes the
reverse lookup a log query.

## Tests

```bash
docker run --rm -v "$PWD:/workspace" -w /workspace/examples/gotth/counter \
    dis-gotth-live:latest go test -race ./...
```

52 specs, Ginkgo v2 with Gomega. The two that carry the most weight:

- **`livetest.ReplayN`** replays a whole session — four clicks and the
  snapshots the store pushed back — 25 times, and fails unless the state and
  the effects are identical every run. It is FR-15's mandatory harness pointed
  at this reducer.
- **`livetest.AssertDirtyComplete`** replays the same log and fails if either
  fragment declared itself unchanged while its markup moved. That is the one
  rendering bug that shows up as a stale region in production and as nothing at
  all in development.

The rest cover the store's convergence and its latest-value-wins queue, the
rendered attribute vocabulary, and the WebSocket handshake answered against the
origin allowlist from a raw socket.

## Notes for anyone copying this

**It was a separate Go module,** with a `replace` pointing at the checkout, and
it is a package of `github.com/candacelabs/csf` now. It still sits outside
`pkg/gotth/`, so it reaches the library through the same import path a consumer
uses; a real consumer requires a published version of the module instead of
holding a checkout.

**`view_templ.go` is generated and committed.** Regenerate it with
`bash pkg/gotth/gen.sh` from the export root after editing `view.templ` — that
is the script FR-7's reproducibility gate runs, and it lists this file among
the outputs it compares.

**The page and the fragments render the same components.** `Page` composes
`ValueRegion` and `ControlsRegion` from the same state, so the snapshot that
arrives over the WebSocket morphs the page to bytes it already has. Render them
differently and the first patch after connecting visibly rewrites a page that
was already correct.

**Keyboard `+`/`-`, which C-B lists as F-CTR-6, is not implemented.** The
`data-gotth-*` vocabulary binds a DOM event type to a server event and sends
form values; it has no way to say "only when the key was `+`", and a `keydown`
binding would fire on every key. The buttons are natively keyboard-operable
(Tab, then Enter or Space), which is the accessible answer, but it is not what
CTR-6 measures. Expressing it needs a key filter in the vocabulary, which is a
Phase 4 decision, not something to work around here.

> **⟨CORRECTED 2026-08-05 — the conclusion above stands and its reason is dead.
> `reviews/fr-54.md` §27, condition FR54-13.⟩** The paragraph is kept because it
> is the record of what this page could see when it was written; what follows is
> what is true now.
>
> **The key filter landed at `591c275a`, and it is `live.Bind.Keys`.** The
> vocabulary *does* have a way to say "only when the key was `+`" — component 3
> of `data-gotth-on` — so *"has no way to say"* is false, and *"a Phase 4
> decision"* names a decision that has already been taken. `Bind.Keys` compares
> exactly and case-sensitively against the browser's own `KeyboardEvent.key`,
> and a binding it filters out **falls through** to the next binding for the same
> DOM event, which is what lets one element route two keys to two events:
>
> ```go
> live.OnAll(
>     live.OnWith("keydown", EventIncrement, live.Bind{Keys: []string{"+"}}),
>     live.OnWith("keydown", EventDecrement, live.Bind{Keys: []string{"-"}}),
> )
> ```
>
> That is not a sketch. It is `bench/apps/counter/gotth/bindings.go` as it
> stands, exported helpers only and no hand-written JavaScript — a standing
> proof that F-CTR-6 is expressible today.
>
> **So the real reason this example does not implement it is a scope
> judgement about this example, not a limit of the library.** `examples/counter`
> is *"the smallest complete gotth-live application"*, and it is deliberately
> the demonstration of one idea — state in Go, buttons that change it, tabs kept
> in step — rather than a checklist of C-B's seven features. F-CTR-6 is the one
> feature of the seven that would add a second binding idiom (`OnAll` with a key
> filter and a fall-through) to a page whose whole argument is that four
> `live.On` spreads are enough. **Whether that trade is still right now that the
> filter costs one field is a scope call, and it is not this correction's to
> make** — this correction's job is to stop the page giving a false reason for a
> true state of affairs.
>
> **One trap, recorded here because it is where a reader would go next.** Do
> **not** reach for `Bind.NoModifiers` on these bindings. On most layouts `+`
> **is** `Shift`+`=`, so a binding that named `"+"` and demanded that no modifier
> be held would match **nothing at all** — the option would silently disable the
> feature it was added to sharpen. `bench/apps/counter/gotth/bindings.go` records
> the same trap for the same reason, and `reviews/fr-54.md` §13 refuses a full
> modifier set partly on it.
