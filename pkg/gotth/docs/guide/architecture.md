# Architecture: what runs where

At the end of this page you can say, for any line of code you write against this
library, **which goroutine it runs on, what it is allowed to do there, what is
queued behind it, and what happens when that queue fills** — and you can read a
stack trace, a latency spike or a `SLOW_CLIENT` close without opening the
library's source.

This is the runtime model as built, not as designed. Every number below is the
default in [`live`](../api-surface.md) at this commit, and the last section says
where each claim was checked.

Compiled source: [`_samples/architecture`](_samples/architecture).

---

## One tab, one connection, one session, one goroutine

```text
browser tab                          Go process
───────────                          ──────────
                        ┌──────────────────────────────────┐
                        │ http.Handler (the upgrade)       │  HTTP goroutine
                        │   origin → Authenticate → CSRF   │
                        └───────────────┬──────────────────┘
                                        │ 101, then the session is minted
   ┌────────────┐                       ▼
   │  WebSocket │◀────────┬───────────────────────────┐
   └────────────┘         │ conn read pump            │  goroutine 1 of 2
        │  ▲              │   parse → rate limit →    │
        │  │              │   registered? → Authorize │
        │  │              └─────────────┬─────────────┘
        │  │                            ▼
        │  │                    ┌───────────────┐
        │  │                    │ mailbox, 64   │  bounded, never blocks
        │  │                    └───────┬───────┘
        │  │                            ▼
        │  │      ┌─────────────────────────────────────────┐
        │  │      │ session actor — owns State, sole writer  │  goroutine 2 of 2
        │  └──────┤   Reduce → Dirty → Render → compare      │
        │  patch  │   → Patch frame → outbound window, 16    │
        │         └──────────────────┬──────────────────────┘
        ▼                            │ each Effect
      morph                          ▼
                              ┌──────────────┐
                              │ Execute      │  one transient goroutine each
                              └──────────────┘
```

**Two goroutines per live session, and that is a measurement rather than an
intention** — exactly 2.0 per session in every cell of the
[G2 baseline](../bench/g2-baseline.md). Writes are performed by the actor under a
write deadline, so there is no third pump and no write queue. Effect goroutines
are extra, transient, and yours to bound: they last as long as your `Execute`
does.

**A session is one connection.** Not one tab over time, not one user, not one
browser: one WebSocket. It is minted at the `101` and it dies with the socket.
Everything that follows depends on that sentence, and
[§The session is the connection](#the-session-is-the-connection) says what it
costs you.

---

## The six places your code runs

| Hook | Goroutine | Queued behind it | May block? | May do I/O? |
|---|---|---|---|---|
| `Authenticate`, `CSRF` | the HTTP handler's, on the upgrade request | nothing — no session exists yet | yes, it is an ordinary request | yes |
| `Authorize` | **the connection's read pump** | every remaining frame on that connection, acks and heartbeats included | **no** | yes, but see below |
| `Init` | the session's actor goroutine, as the first transition | the first snapshot, so the first paint | **no** | yes — it is the actor |
| `Reduce` | the session's actor goroutine | that session's mailbox | **no** | **no** — FR-14 |
| `Render`, `Dirty` | the session's actor goroutine, straight after `Reduce` | the same | **no** | **no** — FR-18 |
| `Execute` | a fresh goroutine per effect | nothing | yes, that is what it is for | yes |
| `Teardown` | the actor's exit path, after the mailbox drains | the connection's release | briefly | yes |

The whole of that table is one compiled `Config`:

<!-- sample: architecture/architecture.go -->
```go
	return live.Config[State, live.AnonymousIdentity]{
		Init: func(ctx context.Context, sess live.Session[live.AnonymousIdentity]) (State, []live.Effect[live.AnonymousIdentity], error) {
			room.Join(sess.ID())
			return State{}, nil, nil
		},
		// ...
		Reduce: Reducer(room),
		// ...
		Authorize: room.Authorize,
		// ...
		Teardown: func(_ context.Context, sess live.Session[live.AnonymousIdentity], _ State) { room.Leave(sess.ID()) },
		// ...
		Origins:      origins,
		Authenticate: live.Anonymous,
		CSRF:         live.NoCSRFCheck,
	}
```

**One goroutine owns each session's state and is the only writer.** That is why
`Reduce` never needs a mutex and never gets one, and why no session's state is
reachable from another session's goroutine. It is also why anything shared
*between* sessions — a room, a pubsub topic, a cache — is **yours** to
synchronise, and is reached from `Init`, an effect's own `Run` and `Teardown`,
never from inside the transition itself:

<!-- sample: architecture/architecture.go -->
```go
func Reducer(room *Room) live.Reducer[State, live.AnonymousIdentity] {
	return func(s State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
		if ev.Name != EventShout {
			return s, nil
		}
		body := ev.Fields.Get(FieldBody)
		if body == "" {
			s.Notice = "say something first"
			return s, nil
		}
		s.Heard++
		s.Notice = ""
		return s, []live.Effect[live.AnonymousIdentity]{room.ShoutEffect(body)}
	}
}
```

The reducer asks for the write; it does not perform it. What performs it runs on
its own goroutine one hop later —
[effects-and-server-push.md](effects-and-server-push.md) is that hop in full.

---

## `Authorize` is ahead of the mailbox, not behind it

This is the one row of that table you cannot infer from the outside, and it has
two consequences that pull in opposite directions.

**It is the security property.** An event is size-checked, rate-limited, checked
against `Config.Events`, checked against the declared fragments, and only then
authorized — *before* it occupies a mailbox slot. A refused event therefore costs
the session nothing, and a **new** frame kind cannot quietly skip the hook: the
routing switch is exhaustive over a sum type closed in another package, so
adding a kind is a compile-time decision about authorization rather than an
omission. Exactly three kinds skip it today, each of which can be checked against
that rule — an acknowledgement and a heartbeat are transport plumbing no reducer
can observe, and client telemetry is a report about a patch this session already
sent. None of the three can reach application state.

**It is also a sharper blocking hazard than a slow reducer.** A reducer that
takes a second stalls that session's mailbox. An `Authorize` that takes a second
stalls **the connection**: no further frames are read, which includes the
acknowledgements that re-open the outbound window and the heartbeats that prove
the client is alive. Since the server's liveness clock is "time since the last
inbound frame", a long enough stall in this hook ends the session with **4010
`HEARTBEAT_TIMEOUT`** — a self-inflicted close that looks exactly like a network
problem.

So: keep the hook a decision over data you already have. If it needs a database,
cache the answer at `Init` and re-check it in `Execute`, where blocking is free.

<!-- sample: architecture/architecture.go -->
```go
func (r *Room) Authorize(_ context.Context, _ live.Session[live.AnonymousIdentity], ev live.Event) error {
	if len(ev.Fields.Get(FieldBody)) > 280 {
		return &live.DenyError{Reason: "that is too long for this room"}
	}
	return nil
}
```

> The table above said "the session's actor goroutine" for `Authorize` until
> 2026-08-05, on this page's neighbour
> [lifecycle-hooks.md](lifecycle-hooks.md#what-runs-where), which is now
> corrected. The spec beside the sample asserts the live behaviour — that
> `Authorize` and `Init` observe different goroutine identifiers, that one
> connection authorizes all of its events on one goroutine, and that a denied
> event never reaches the reducer — so the claim fails a test rather than
> rotting quietly if the library ever moves it.

---

## One event, end to end

The order matters because each step has its own refusal, and the refusals are
distinguishable on purpose.

| # | Step | Where | If it refuses |
|---|---|---|---|
| 1 | frame arrives, size-checked against `MaxInboundFrameBytes` (**65536**) | read pump | close **4007 `FRAME_TOO_LARGE`** |
| 2 | parse, and check the session identifier | read pump | close **4002 `PROTOCOL_VIOLATION`** |
| 3 | inbound token bucket, `MaxEventsPerSecond` (**50**) / `EventBurst` (**100**) | read pump | `Error{RATE_LIMITED}`, connection survives |
| 4 | is the name in `Config.Events`? | read pump | `Error{UNKNOWN_EVENT}` — default-deny, never dispatched, never ignored |
| 5 | does the named fragment exist? | read pump | `Error{UNKNOWN_FRAGMENT}` |
| 6 | `Authorize` | read pump | `Error{UNAUTHORIZED}`; `*FatalDenyError` also closes with **4006** |
| 7 | into the mailbox (**64**) | read pump → actor | full: `Error{RATE_LIMITED}`, the frame is **dropped, not queued**, and the read pump never blocks |
| 8 | `Reduce` | actor | a panic is contained: pre-transition state survives, no patch, `Error` frame |
| 9 | `Dirty` per fragment, then `Render` for those it named | actor | a render panic leaves one region stale; the others patch normally |
| 10 | hash and compare against the last markup sent | actor | identical render → **no patch at all** |
| 11 | `Patch` frame out, sequence-numbered, under the window (**16** unacknowledged) | actor | window full → coalesce, then degrade, then evict with **4009 `SLOW_CLIENT`** after `SlowClientGrace` (**30 s**) |
| 12 | morph into the DOM | browser | — |

Steps 1–6 all happen **before** any per-session memory is spent on the event.
That is the same ordering the handshake uses one level up — origin, then
`Authenticate`, then `CSRF`, then the upgrade — and for the same reason.

Every one of those refusals, with its close code and what it usually means, is
[error-handling.md](error-handling.md). What steps 9 and 10 mean for how you
split a page is [fragments-and-dirty-tracking.md](fragments-and-dirty-tracking.md).

---

## What is bounded, and what happens at the bound

Nothing in a session grows without a ceiling, and every ceiling has a stated
policy for being hit. **Blocking is never the policy.**

| Bound | Default | At the bound |
|---|---|---|
| `MaxInboundFrameBytes` | 65536 | close 4007 |
| `MaxEventsPerSecond` / `EventBurst` | 50 / 100 | `Error{RATE_LIMITED}`; sustained flooding closes 4008 |
| `MailboxDepth` | 64 | drop the frame, tell the client, count it — never block the read pump |
| `AckChannelDepth` | 32 | drop silently and count: an ack is a cumulative high-water mark, so the next one supersedes it |
| `AckWindow` | 16 unacknowledged patches | stop emitting; coalesce; `SlowClientEvent` reaches your reducer |
| `CoalesceFlushAt` | 512 contributing events | flush the coalesced patch immediately rather than coalescing further |
| `MinResyncInterval` / `ResyncBurst` | 1 s / 3 | `Error{RATE_LIMITED}` on its **own** budget, not the event bucket's — a resync is a full re-render and the most expensive thing a client can ask for; sustained flooding closes 4008 |
| `HeartbeatInterval` / `HeartbeatTimeout` | 20 s / 50 s | close 4010 |
| `IdleTimeout` | 30 min | close 4011 |
| `PanicBudget` | 3 per site per session | close 4012 |
| `EffectDrainTimeout` | 5 s | abandon the effect, counted, at shutdown |

Two of those are worth internalising rather than looking up.

**A full mailbox drops rather than queues, and that is a memory decision as much
as a flood-control one.** The mailbox holds pointers, so its 64 slots reserve 512
bytes per connection whether or not any event ever arrives; the messages
themselves are pooled and exist only while queued. Raising `MailboxDepth`
therefore costs every idle connection, not just the busy ones.

**Backpressure is visible to your application, not hidden from it.** When the
window fills, the library stops emitting and synthesizes `live.SlowClientEvent`
into the session's own mailbox; `live.ClientRecoveredEvent` arrives when an
acknowledgement drains it. Your reducer handles them in the same switch as
everything else, which is what keeps a degradation your application chose
replayable from the event log. The ordering to know before you write that branch:
the library has **already** stopped emitting by the time the event arrives, so a
notice set in response to it reaches the browser only once the window re-opens.

---

## The session is the connection

**Nothing is serialized. There is no session store, no sticky-session
requirement inside the process, and no state to migrate.** A session's `State`
lives in one goroutine's stack-and-heap and dies with it.

That single decision produces the four behaviours you will actually meet:

- **A reconnect is a new session.** Fresh `Init`, fresh `State`, fresh snapshot,
  fresh identity binding. Anything the old session had that was not rebuildable
  from your own storage is gone. This is why `Init` is where you *read*, and why
  `examples/*` all rebuild their state from a shared store rather than trusting
  the last one.
- **A resync is not a reconnect.** The client asks for the current markup over a
  connection that is still up; the actor re-renders every fragment from the state
  it still holds and sends a fresh snapshot. Nothing is deserialized, because
  nothing was serialized.
- **Two tabs are two sessions.** They share nothing by default. Making them agree
  is an effect writing to state you own —
  [effects-and-server-push.md](effects-and-server-push.md).
- **A second replica does not break correctness, but it does not share sessions
  either.** [deploying.md](deploying.md) has what a proxy and a load balancer
  have to be told.

**Events are at-most-once; patches are exactly-once and in order.** An
unacknowledged event is not retried across a reconnect and a duplicate is not
deduplicated. If an effect commits outside the process, the obligation to make it
idempotent is yours — that is the bound
[when-not-to-use-this.md](when-not-to-use-this.md) opens with, and it is a
consequence of this section rather than a gap.

---

## What a session costs

| | |
|---|---|
| Goroutines | **exactly 2**, plus one transient per in-flight effect |
| Memory | one measured figure, on [deploying.md §Resource sizing](deploying.md#resource-sizing), with its own staleness note — it is not repeated here, because a number in two places goes stale in one of them |
| Client bytes | one runtime, embedded in your binary, served by the same handler; no CDN, no npm, no build step. The measured size is in [`client/SIZE.md`](../../client/SIZE.md) |
| Per-event cost | one round trip, one reducer call, and a render of only the fragments `Dirty` named |

The shape of the memory answer matters more than the figure: **it is per
connection and it is dominated by what *you* put in `State`.** A session holding
a 200-item slice per user holds it for as long as the tab is open, times every
open tab. If a page's data is large and shared, keeping it in a store your effects
read and rendering a window of it is the difference between a server that scales
with tabs and one that scales with tabs times data.

---

## What the library owns, and what you own

| The library owns | You own |
|---|---|
| the markup inside every `data-gotth-region` element | every other element on the page |
| the WebSocket, the frames, the sequence numbers | the HTTP route the handler is mounted at |
| `State`'s lifetime and its single writer | `State`'s type, and everything shared between sessions |
| the morph that applies a patch | the DOM outside the regions, including HTMX's |

The boundary is enforced, not conventional: HTMX and live regions may share a
page in exactly two arrangements, and
[htmx-interop.md](htmx-interop.md) is which two. Inside a region, the server is
the author of the markup and anything else writing there will be overwritten by
the next patch.

---

## Where these claims were checked

A page about a runtime model is worth exactly as much as its evidence, so here is
the evidence.

| Claim | Checked against |
|---|---|
| `Authorize` runs on the read pump, not the actor | `internal/session/ingress.go`'s `Ingress`, called from `internal/wsx/conn.go`'s read loop — and the spec in [`_samples/architecture`](_samples/architecture), which observes two different goroutines |
| `Init`, `Reduce`, `Render` run on the actor | `internal/session/actor.go`'s `Run` → `mount` → `step` |
| `Execute` runs on a goroutine per effect | `internal/session/effects.go`'s `spawn` |
| Every default in the tables | `live.DefaultLimits`, one file, one struct literal |
| Two goroutines per session | [g2-baseline](../bench/g2-baseline.md), every cell, every run |
| The refusal each bound produces | the close-code table in [error-handling.md](error-handling.md), which enumerates the library's own `Close` call sites |

**Why this page exists when [rfc/001-architecture.md](../rfc/001-architecture.md)
already did.** The RFC is the design record: it argues the alternatives, carries
the measurement campaign that set the memory budget, and keeps its corrections as
changelog entries because that is what a design record is for. It is 1,800 lines
addressed to a reviewer holding a checklist, and it is dated before the library
existed. This page is addressed to you, states the model as the code runs it
today, and is held to that by a compiled sample and a spec. Read the RFC when you
want to know **why** a decision went the way it did; read this when you want to
know **what happens**.
