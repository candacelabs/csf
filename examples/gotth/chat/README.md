# The chat example

One room that lives in Go, several browsers, and every message reaching every
session over a server push. It is the example PRD **FR-61** asks for and, with
its Ginkgo suite, **FR-63**; it is also the application Phase 2's exit criteria
are gated against, so most of what is below is there because a criterion names
it.

Where the library was not enough, [`FRICTION.md`](FRICTION.md) says so, item by
item, and the code carries a comment naming the item.

---

## Run it

The host needs no Go. Build the library's own toolchain image once — `dis run`
is not the recipe any more, because the dis workspace is `pkg/gotth/` and this
example is that directory's sibling — then run from the export root, which is
this repository's root:

```bash
docker build -t dis-gotth-live:latest pkg/gotth/.dis
docker run --rm -v "$PWD:/workspace" -w /workspace/examples/gotth/chat \
    -p 8081:8081 dis-gotth-live:latest go run . -addr 0.0.0.0:8081
```

Or, if you have Go 1.26 locally:

```bash
cd examples/gotth/chat
go run .                    # http://127.0.0.1:8081
go run . -addr 127.0.0.1:9001
go run . -origin http://192.168.1.10:8081   # allow another browser Origin
go run . -provenance        # print the causal log for every transition
```

There is no `npm install`, no bundler, and no code generation step to run: the
client runtime is compiled into the binary and served by the same handler that
serves the WebSocket. The only generated file here is `view_templ.go`, and it is
committed. Regenerate it with `bash pkg/gotth/gen.sh` from the export root
after editing `view.templ` — that is the script FR-7's reproducibility gate
runs, and it lists this file among the outputs it compares.

## What to expect

Open <http://127.0.0.1:8081>. Pick somebody to be — the sign-in page lists the
cast — and then open a **second browser**, or a private window, so the two hold
different cookies. Sign in as somebody else.

1. Both pages render with the room already in them; the connection dot goes
   **connecting → live**. The second window's arrival makes the first one's
   roster grow before you have touched anything.
2. Say something in one. It appears in both. Nothing in either browser holds the
   log.
3. **Start typing in one window and send a message from the other.** Your
   half-typed sentence does not move — not because the browser kept it, but
   because the server never sent markup for that box. This is the case FR-55
   names and the one naive implementations get wrong.
4. Type more than 280 characters and hit send. The server refuses it, tells you
   why, and **gives you your text back**.
5. Sign in as `olive`. The box is disabled, and an event sent anyway is refused
   with an `UNAUTHORIZED` frame that does not close the connection.
6. Sign in as `mallory` to get the **Clear the room** button. As anybody else the
   same event is denied.
7. Sign in as `trudy`. The first thing you do closes the connection.
8. Send `/panic reducer`, `/panic effect` or `/panic render`. Your window is
   affected; the other one keeps working. Watch the server log.

### The cast

| Name | Role | May post | May clear the room | On any event |
|---|---|---|---|---|
| `alice`, `bob` | member | yes | no — `DenyError` | — |
| `mallory` | moderator | yes | yes | — |
| `olive` | observer | no — `DenyError` | no | — |
| `trudy` | banned | — | — | `FatalDenyError`, connection closes |

Identity is a cookie, set by `/login?user=<name>`, read by
`Config.Authenticate` off the upgrade request. It is immutable for the life of
the connection: promoting somebody is a reconnect.

## Where to look in the source

| File | What is in it |
|---|---|
| [`chat.go`](chat.go) | `State`, the pure `Reduce`, validation, the identity and the authorization rule, and the `live.Config` |
| [`room.go`](room.go) | The shared room every session reads, the effects, and the subscription that pushes changes |
| [`view.templ`](view.templ) | Three fragments and two pages, with `live.Region`, `live.On`, `live.OnWith` and `app.Document` — the sign-in page passes `live.NoRuntime`, because it is deliberately not live |
| [`main.go`](main.go) | Flags, routing, sign-in, the Origin allowlist, graceful shutdown |
| [`chat_test.go`](chat_test.go) | The reducer, authorization, determinism, input preservation, escaping and markup specs |
| [`wire_test.go`](wire_test.go) | The same claims measured on the frames, over real dialled connections |
| [`FRICTION.md`](FRICTION.md) | Seven things the library made harder than they should have been, three of them since closed |

## The three fragments, and why there are three

This is the whole design and everything else follows from it.

| Fragment | Whose it is | Re-renders when |
|---|---|---|
| `chat.log` | everybody's | the room's revision moved |
| `chat.roster` | derived | the member list moved |
| `chat.composer` | **this session's alone** | **this session's** draft, its verdict, or a notice moved |

The composer's `Dirty` function names three fields and says nothing about the
room. So when somebody else speaks, the composer is not in the patch at all —
the browser is never handed markup for the box you are typing into. Widening
that declaration by one comparison would still pass
`livetest.AssertDirtyComplete` (over-declaring is safe as far as that helper is
concerned) and would break the feature, which is why it has specs of its own in
both test files.

The second half is the `value={ s.Draft }` on the input. In the common case the
browser hides its absence — an uncontrolled input keeps what you typed across a
morph — and it stops hiding it the moment the composer genuinely is re-rendered:
a validation message, a reconnect, a resync. Then an empty `value` is a sentence
deleted out from under somebody.

## The loop, once through

Alice sends a message. Nothing on this path is specific to alice's tab, which is
why bob's repaints for free.

```
alice's browser            alice's session goroutine        room (application-owned)
   │
   │ data-gotth-on="submit:chat.send"
   │──── Event frame ────────▶ Authorize(member, "chat.send")
   │                           Reduce(state, ev)
   │                             → Draft cleared, DraftError cleared
   │                             → PostEffect{Body: "…"}   ← no author
   │                                    │
   │                                    ▼ actor boundary
   │                                  Execute(ctx, session, effect, emit)
   │                                    author = session.Identity()  ─────▶ Post: seq++, version++
   │                                                                          broadcast to every
   │◀─── Patch{origin: event:chat.send, fragments:[chat.composer]}              subscriber's queue
   │                                                                                 │
   │                           ◀── chat.posted (emitted by alice's own pump) ◀────────┤
   │                           Reduce → Room replaced                                 │
   │◀─── Patch{origin: effect:chat.subscribe, fragments:[chat.log]}                   │
                                                                                      │
bob's session goroutine    ◀── chat.posted (emitted by bob's pump) ◀──────────────────┘
                           Reduce → Room replaced
   bob's browser ◀─── Patch{origin: effect:chat.subscribe, fragments:[chat.log]}
```

Three things in that diagram are requirements rather than choices.

**The reducer never records the message.** It returns an effect and learns the
result the same way every other session does. That is what makes two tabs unable
to disagree.

**`PostEffect` carries no author.** The executor reads it from the `live.Session`
it is handed, which is the identity `Authorize` permitted the event under. A
reducer cannot attribute a sentence to somebody who did not write it, because a
reducer never gets to say who wrote it. (This is the argument `Config.Execute`
gained a `Session` parameter for, and a spec asserts it by performing *the same
effect value* for two sessions and getting two authors.)

**Every patch names its cause.** `event:chat.send` for the transition alice
caused, `effect:chat.subscribe` for the ones the room pushed, `mount` for a
session's first snapshot. FR-42 forbids `unknown`; a spec walks every frame both
browsers received and asserts the origin of each.

## The error boundaries, and how to provoke them

FR-23 requires a panic in a reducer, an effect and a render to be contained to
its session. Type each one into the composer:

| Command | Site | What the browser is told | Other sessions |
|---|---|---|---|
| `/panic reducer` | reducer | `Error` frame, `INTERNAL`, non-fatal, carrying the event's causal identifiers | unaffected |
| `/panic render` | render | `Error` frame, `INTERNAL`, carrying the event's causal identifiers | unaffected |
| `/panic effect` | effect | **no `Error` frame at all** | unaffected |

The third row is the asymmetry, and it is the requirement rather than an
implementation detail. An effect panic leaves state consistent — the reducer
never ran on a bad value — so the only party who can say whether the failure is
user-visible is the application. It arrives at the reducer as
`gotth.effect_failed` with `retryable = "false"`, and the patch that reducer
produces carries origin `effect:chat.panic` with the submission that scheduled
it as a contributing edge. The spec for it asserts the **absence** of an `Error`
frame directly, because a test that accepts one fails the criterion.

`applyFailure` renders `EffectFailedSourceField` and never
`EffectFailedErrorField`. The second carries the error's own text, or the raw
panic value, unredacted and in production, ungated by `Config.Dev` — rendering
it publishes internal error text to every browser holding the fragment. A spec
puts a fake connection string in that field and asserts it does not reach the
markup.

`Config.Dev` is what decides how much of a panic reaches the browser and is the
only thing that field does. `main.go` sets it true because this is an example;
a spec asserts that with it false, a person typing `/panic reducer` is not shown
the panic value.

## The subscription, and the leak test

`Config.Init` calls `room.Join`, which registers the session and reads the room
under one lock — split in two, a message landing between them is either shown
twice or missed entirely, and the window is exactly as wide as a page load.
It then returns a `SubscribeEffect`, because `Config.Execute` is the only place
an application is handed a `live.Emitter`.

`Config.Teardown` calls `room.Leave`, after the session's goroutine has exited.

FR-56's sufficiency test is that pair plus no leak, and the spec is exactly that:
twenty connect/disconnect cycles, then `room.Occupants()` back to zero — the
exact half — and the goroutine count back to its baseline within a tolerance —
the approximate half, because the HTTP server and the WebSocket library keep
goroutines on their own schedule and what would fail is twenty subscription
pumps still running.

## Two things this example does deliberately differently from the counter

**It is mounted at `/chat/live`, not `/live`.** `live.Script` used to default to
`/live`, so an application mounted anywhere else served a page whose script
404'd — the page loaded, nothing was live, and no error appeared anywhere on the
server. The mount path is a parameter now (L9-1 condition C-23), and this
example being somewhere else is what keeps that fix honest. A spec pulls the
`src` off the rendered page and fetches it.

**Its state holds a `*Log` rather than a slice.** A chat needs a message list and
a Go state struct containing a slice is not comparable, which the library reads
as "changed" on every transition — so a no-op event bumps the state version and
every fragment's `Dirty` is asked about a change that did not happen. An
immutable value replaced wholesale keeps `State` comparable, and it also makes
the reducer's no-mutation rule impossible to break by accident. A spec folds two
different messages onto one prior state and asserts the two results are
independent, which is the shape `livetest.ReplayN` exercises by construction.

## The suite

```bash
docker run --rm -v "$PWD:/workspace" -w /workspace/examples/gotth/chat \
    dis-gotth-live:latest go test -race -count=1 ./...
```

157 specs. Ginkgo v2 + Gomega throughout; no gomock, because nothing here is an
expectation-based interface assertion — the specs assert on returned state, on
rendered bytes, or on frames off the wire.

`wire_test.go` drives real dialled WebSocket connections against a real
`httptest` server through **`live/livetest`'s `Client`**, which dials, decodes
and hands each frame back as a plain value. It deliberately does not import the
library's generated types, which live under `internal/` and which this module's
import path would in fact permit: a consumer's module could never do it, so a
criterion proved that way would be proved with a tool no reader of this example
can pick up. `livetest` is the library's second exported package, so it
satisfies that constraint by construction — a reader of this example can import
exactly what these specs import.

It was not always so. This file used to carry its own reader over
`google.golang.org/protobuf/encoding/protowire`, written against the published
`.proto`, and a WebSocket driver around it, for want of anything exported that
could read a frame. [`FRICTION.md`](FRICTION.md) item **F-1** filed that as
friction, said what it cost and named the fix; the fix landed, and the item is
closed. What is left in `wire_test.go` is the part that is about chat: the
identity cookie, and the three verbs a member has.

One spec, "The names this application puts on the wire", pins every fragment
identifier, event name, effect source and `/panic` command to a string literal.
Every other spec builds its input from the same constant the code under test
matches on, which tests the branch and not the name; that one exists so a rename
announces itself somewhere.
