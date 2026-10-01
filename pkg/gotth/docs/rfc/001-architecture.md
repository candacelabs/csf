# RFC 0001 — gotth-live architecture

| | |
|---|---|
| **Status** | Approved at Phase 0 review cycle 2; implementation synced 2026-08-11 |
| **Date** | 2026-08-04; last synced 2026-08-11 |
| **Author** | DEV-1 (Server Core / Go) |
| **Supersedes** | — |
| **Superseded by** | — |
| **Companions** | [ADR-001 transport](../adr/001-transport.md) · [protocol.md](../protocol.md) · [instrumentation.md](../instrumentation.md) · [RFC 0000 teardown](0000-prior-art-teardown.md) |
| **Governed by** | [PRD](../PRD.md) · [review checklist §11](../review-checklist.md) |

## Decision, in one sentence

**gotth-live is a single-node Go library in which one goroutine and one bounded
mailbox own each live session, a pure reducer `(state, event) → (state, []Effect)`
advances that state, a pure `render(state)` produces whole HTML fragments, and
those fragments travel as sequence-numbered, acknowledged, causally-tagged liquid
proto frames over one WebSocket per tab to a ≤12,288-byte client that morphs them
into the DOM.**

---

## 0. What this document settles

| Checklist §11.9 hard part | Settled in |
|---|---|
| 1. Transport (WS vs SSE+fetch) | [ADR-001](../adr/001-transport.md) — WebSocket |
| 2. State serialization for resync | §8.3 — **nothing is serialized** |
| 3. Per-event authorization | §11.3 — single mailbox ingress |
| 4. Origin validation and authenticated establishment | §11.1–11.2 — ordered before allocation |
| 5. Memory ceiling per idle connection | §6 — **≤ 45 KiB**, TLS outside, equivalence-spec §3.6 method, with the decision rule pre-registered in §6.1.2 |
| 6. Slow-client eviction | §7.4 — coalesce → degrade → evict |

None is deferred. PRD §7.2's sixteen open questions are answered in §13.

---

## 1. Architecture overview

```
browser tab                                  Go process
───────────                                  ──────────
                                       ┌─────────────────────────────┐
 DOM ◀── morph ◀── client runtime      │  http.Handler (mount)       │
                        │  ▲           │    origin → auth → csrf     │  §11
                        │  │           └──────────────┬──────────────┘
                        ▼  │                          │ 101 + session_id
                   ┌────────────┐                     ▼
                   │ WebSocket  │◀───────────┬──────────────────┐
                   └────────────┘            │  conn goroutine  │  §3.4
                                             │  (read pump)     │
                                             └────────┬─────────┘
                                              ingress │ parse → authorize   §11.3
                                                      ▼
                                             ┌──────────────────┐
                                             │  mailbox (chan)  │  bounded, §3.3
                                             └────────┬─────────┘
                                                      ▼
                       ┌──────────────────────────────────────────────┐
                       │  session actor goroutine — the only writer   │  §3
                       │                                              │
                       │   reduce(state, event) → (state, []Effect)   │  §4  pure
                       │   render(state)        → []Fragment          │  §5  pure
                       │   emit  → framer → window → Conn.Write       │  §7
                       │   spawn effects (context-scoped)             │  §4.4
                       └──────────────────────────────────────────────┘
```

Three rules hold everywhere and are the reason the rest is simple:

1. **The actor is the lock.** Session state is reachable only through the
   mailbox (checklist §6.1, §6.2). No `sync.Mutex` guards session state anywhere.
2. **Purity is the budget mechanism.** Because `render` is a pure function of
   state, a render that cannot be sent is not queued — it is *skipped*, and the
   latest state is rendered later. Backpressure therefore costs O(fragments) of
   memory, not O(pending patches). §7.3.
3. **One write path.** Nothing reaches the socket except through the framer
   (checklist §4.4), which is what makes provenance property P8 checkable.

---

## 2. Connection lifecycle (FR-8)

| Phase | What happens | Failure |
|---|---|---|
| **Mount** | `http.Handler` from `live.Handler(app)`; origin → authenticate → CSRF → subprotocol (protocol.md §8.1). **No per-session memory is allocated in this phase** (checklist §5.2) | `403` / `401` / `426`, no state |
| **Open** | `101`; `session_id` minted; actor goroutine spawned; `Mount` hook runs as the first transition (`Origin{kind: MOUNT}`); `Snapshot` emitted with `server_seq = 1` | actor spawn failure → `500` before upgrade |
| **Live** | events up, patches down, heartbeats both ways (20 s default), acks per §7 | see §12 |
| **Close** | Every close names a code from protocol.md §8.3. Ordered: stop accepting events → cancel session context → in-flight effects observe cancellation → drain-or-abandon per §3.6 → close frame → deregister exactly once (checklist §6.8) | a close without an enumerated code is a bug (FR-8) |

**Idle eviction (FR-22):** a session with no inbound frame other than heartbeats
for `idle_timeout` (default 30 min, configurable) closes `4011 SESSION_EVICTED`.
After eviction no goroutine, timer, or heap retention attributable to the session
remains; the leak test is 10k connect/disconnect cycles back to baseline.

---

## 3. The session actor

### 3.1 Shape

```go
// internal/session
type actor struct {
    id       ID                 // 16 bytes, minted at handshake
    identity Identity           // immutable for the connection's life (FR-46)
    state    any                // application state; only this goroutine touches it

    // Three typed inputs, all bounded (§3.3). Only `mailbox` reaches a reducer.
    mailbox  chan *inbound      // cap 64  — events + effect results + SlowClient
    acks     chan uint64        // cap 32  — Ack frames from the read pump
    ticker   *time.Ticker       // heartbeat + idle-timeout tick

    out      *window            // §7.1
    frag     *render.Registry   // fragment identity (FR-21)
    seq, ev, tr, sv, pid uint64 // monotonic counters (protocol.md §4.1)
}

func (a *actor) run(ctx context.Context) {
    for {
        select {
        case <-ctx.Done():
            a.shutdown()
            return
        case m := <-a.mailbox:
            a.step(ctx, m)     // reduce → render → emit → spawn effects
        case <-a.ticker.C:
            a.heartbeat()
        case ack := <-a.acks:
            a.out.ack(ack)     // may re-open the window → a deferred render
        }
    }
}
```

There is exactly one `run` per session, and `state` has no other reader or
writer. `-race` with concurrent event injection is the test (FR-17).

### 3.2 One step

1. **Stamp** — the boundary attaches wall time and any generated IDs to the
   event (checklist §2.2, §2.3). Reducers never call `time.Now()` or a random
   source; they receive them.
2. **Reduce** — `state', effects := app.Reduce(state, event)`, wrapped in the
   panic guard (§9). `transition_id++`. `state_version++` **iff** `state' != state`.
3. **Mark dirty** — the reducer declares which fragments its transition may have
   changed, or the registry marks all of them; §5.3.
4. **Render** — for each dirty fragment, `render(state')` under the panic guard.
   A fragment whose rendered bytes are identical to the last emission is dropped
   (cheap `xxhash` per fragment; §5.4).
5. **Emit** — one `Patch` frame carrying the changed fragments, tagged with the
   full causal chain, through `protocol.ValidateOutbound` (protocol.md §5.3) and
   then the framer and the window (§7). The outbound validation is the same
   re-checking boundary the ingress path crosses, and it is what makes
   provenance property P1 a property rather than a discipline.
6. **Spawn effects** — each `Effect` value is executed by the actor boundary, in
   a goroutine scoped to the session context, never inside the reducer (FR-16).
   Results return as ordinary events into the mailbox.

### 3.3 The three bounded inputs (checklist §6.6)

The actor has **three typed inputs**, and every one is bounded with a stated
full-channel policy. Checklist §6.6 requires this per queue, not per actor.

| Input | Capacity | Full-channel policy | Metric |
|---|---|---|---|
| `mailbox chan *inbound` | **64** | `Error{RATE_LIMITED}` to the client; the frame is **dropped, not queued, and never blocks the read pump** | `gotthlive_events_rejected_total{reason="mailbox_full"}` |
| `acks chan uint64` | **32** | **drop, silently to the client, counted** | `gotthlive_frames_rejected_total{reason="ack_channel_full"}` |
| `ticker` | n/a | Go's `time.Ticker` drops ticks by construction | — |

**Why dropping an ack is safe, and why it is the right policy.** `Ack.server_seq`
is a *cumulative high-water mark* (protocol.md §3.2), so a dropped ack is
superseded by the next one and the window re-opens one round trip later. Dropping
is therefore lossless in the limit, which is exactly the property that makes an
unbounded ack channel unnecessary and a blocking one indefensible: blocking the
read pump on `acks` would stall the connection's own liveness detection — the
failure this section argues against for the mailbox — and an unbounded `acks` is
a memory vector under an ack flood from an authenticated client.

**Blocking is never the policy on either channel.** Blocking the read pump lets
one client's flood stall its own heartbeat processing; dropping with a typed
error keeps the connection diagnosable and bounds memory (FR-51).

**`mailbox` holds pointers, not values, and that is a memory decision.** A Go
buffered channel allocates its entire backing array eagerly at `make` time, for
the life of the channel, occupied or not. `chan inbound` at 64 × ~112 B would
reserve **7,168 B per idle connection** — 17 % of the whole §6.1 budget, paid by
every connection whether or not it ever sends an event. `chan *inbound` at 64 × 8 B
reserves **512 B**, with the `inbound` structs allocated from a `sync.Pool` only
while actually queued. **Mailbox capacity is therefore a memory parameter as well
as a flood-control parameter**, and §6.2 gives it its own line. The two decisions
are coupled and were not treated as coupled in cycle 1.

This is a deliberate departure from BEAM, where mailboxes are unbounded and a
flood becomes memory (teardown §1.6). Go's bounded channel makes the decision
explicit, which is the structural advantage the teardown argued for.

### 3.4 Goroutines per session (checklist §6.4)

Exactly **two**, both owned and both waited for at shutdown:

| Goroutine | Owner | Stop condition |
|---|---|---|
| conn read pump | the handler | `Conn.Read(ctx)` returns, or ctx cancelled |
| session actor | the handler | ctx cancelled and mailbox drained |

Writes are performed **by the actor** with a write deadline, so there is no third
goroutine and no write-side queue goroutine. **Two per session is measured, not
argued**: exactly 2.0 goroutines per session in every run of every cell of
[g2-baseline](../bench/g2-baseline.md) — §5.5's eleven runs, and §9.10.6 and
§9.10.10's seven at the shipping tree.

Effect goroutines are transient, spawned through one `spawn` helper that installs
the panic guard, the metric, and the `WaitGroup` registration.

**`spawn` is the helper for effects, and it is not the only place this library
starts a goroutine.** The sentence that used to stand here — *"there is no bare
`go func()` in the library"* — was **false of the tree** (L9-1 C-35(b), verified
by PM-1 and re-verified against HEAD before this edit). `grep -rn 'go func' live/
internal/` over non-test files returns **five** sites, and the rule they actually
satisfy is the one checklist §6.4 asks for — a named owner, a stop condition, and
a place that waits:

| Site | What it is | Owner | Waited by |
|---|---|---|---|
| `session/effects.go:36` | the `spawn` helper itself — panic guard, `Goroutines` metric, `WaitGroup` | the actor | `a.effects`, drained at shutdown under `EffectDrainTimeout` |
| `wsx/handler.go:254` | the session goroutine, started once `register` has succeeded | the handler | `App.Close` → `Handler.Close` → `<-c.done`, closed by `serve`'s teardown **after** `deregister` (C-34) |
| `wsx/conn.go:175` | the session actor's `Run` | `serve` | `actorDone.Wait()` in `serve`'s deferred teardown, before `c.done` closes |
| `wsx/handler.go:402` | `Close`'s per-session close fan-out, concurrent on purpose so one unresponsive peer cannot serialize the drain | `Close` | `closing.Wait()`, in the same function |
| `session/actor.go:254` | `waitFor`'s deadline helper: it blocks on a `WaitGroup` and closes a channel | `waitFor` | **not waited for, by construction** — it is what makes the drain's deadline enforceable. If an effect never returns, this goroutine stays blocked and the abandonment is counted (`EffectAbandoned`) rather than hidden |

**Both per-session goroutines are waited for at shutdown**, and that clause *is*
now true where it was not before C-34: `Close` waits on `c.done`, `c.done` closes
only after `actorDone.Wait()` and `deregister`, and the wait is bounded by the
caller's context — `Close` returns an error naming the count rather than blocking
for ever.

*(L9-1, 2026-08-05, C-35(b). The RFC half is this table. The source half — the
`spawn` godoc in `internal/session/effects.go:26-32`, which makes the same
claim — is **C-49**, owner DEV-1.)*

### 3.5 There is no `Transport` interface, and FR-2 no longer asks for one

FR-2 originally said transport "MUST sit behind one narrow Go interface", which
review checklist §1.6 and §1.4 contradict: a one-implementation interface is
speculative abstraction, and hedging an ADR is not a justification. **PM-1
amended FR-2 in PRD v0.2 on 2026-08-04** to require the isolation *property*
rather than that mechanism, and this section is written against the amended
requirement.

FR-2's verification clause was always the part that mattered: *"no reducer,
render, morph, or provenance code may reference the concrete transport… verified
by an architecture test asserting no import of the transport implementation
package from core packages."* That test passes without an interface, and this is
how:

- WebSocket code lives in `internal/wsx` and is the **only** package importing
  `github.com/coder/websocket`.
- `internal/session`, `internal/render`, and `internal/protocol` communicate with
  the connection through **channels and a `framer` function value**, not an
  interface.
- The architecture test asserts, via `go list -deps`, that none of those three
  packages transitively imports `internal/wsx` or the websocket library. It lives
  in `internal/arch` (§14.2) and shipped with the module-init PR, so the property
  has been checked from the first commit rather than from the first transport.

So FR-2's isolation property is delivered and FR-2's *mechanism* is not shipped.
Adding the interface is BL-13's job, when a second transport actually exists and
can say what the interface should be.

### 3.6 Shutdown (checklist §6.8)

Cancel session ctx → stop accepting inbound → in-flight effects observe
cancellation and have `effect_drain_timeout` (default 5 s) to return → emit any
final `Error` → close frame with the enumerated code → deregister once. Effects
that do not return within the drain window are abandoned with
`gotthlive_effects_abandoned_total`; the actor does not block shutdown on them.

---

## 4. Reducer contract (FR-14, FR-15, FR-16)

```go
// Reduce is a pure function. Given the same state and event it MUST return
// the same next state and the same effects, on every run and in every process.
type Reducer[S any] func(state S, ev Event) (S, []Effect)
```

- **Pure**: no I/O, no clock, no randomness, no goroutines, no channel ops, no
  `sync.*`, no map-iteration nondeterminism reaching output (checklist §2.1–§2.5).
  Enforced structurally: the reducer package's transitive import set is checked
  against an allowlist by a test (`go list -deps`), added by the PR that
  introduces the first reducer.
- **No input mutation** (checklist §2.6). This is not stylistic — it is what
  makes panic recovery free (§9): if `Reduce` panics, the pre-transition state is
  still intact and correct, so the actor simply keeps it.
- **Effects are data** (checklist §2.8): `[]Effect` values a test can assert on,
  not closures over live handles. The actor executes them.
- **Determinism is tested, not assumed** (FR-15): `live/livetest.ReplayN(reducer,
  log, n)` asserts identical state and effects across `n` replays; every example
  uses it.

---

## 5. Render, fragments, and diff granularity

### 5.1 Decision — whole-fragment patches, no server-side diff

**This RFC chooses fragment granularity: a patch carries the complete rendered
HTML of each changed fragment. There is no server-side diff of consecutive
renders and no compile-time statics/dynamics decomposition.** (PRD Q7; RFC 0000
open question 4.)

The three candidates from the teardown, and why this one:

| Option | Verdict |
|---|---|
| (a) **Whole fragment** — Datastar/Turbo class | **Chosen** |
| (b) **templ statics/dynamics change tracking** — LiveView class | Rejected: **the mechanism's defining production failure is that ordinary code silently defeats it** — a local variable in a template, a `Map.put` instead of `assign` — turning wire size into a property that erodes with no signal (teardown §1.4). That is precisely the degradation-without-a-signal this project's design rule exists to refuse, and it would be the most consequential instance of it in the whole system. Secondarily, buying it would mean extending or forking `a-h/templ`, a dependency we do not control. |
| (c) **Server-side diff of consecutive renders** | Rejected **for v1** — it is PRD **BL-14**, explicitly backlogged, and PRD §4 forbids designing for backlog items. It also costs the thing we are tightest on: retaining the previous render per fragment per session is direct pressure on §6's memory ceiling. |

**Defence, beyond "the PRD said so":**

1. **The diff already happens, on the client, where the DOM is.** Morph compares
   incoming HTML against the live DOM. A server-side diff would compute a second
   diff against a *server-side copy* of what it believes the client shows — the
   Blazor design, which requires the two sides never to drift.
2. **Granularity is the developer's lever, and it is legible.** A fragment is
   declared; making patches smaller means declaring smaller fragments. Contrast
   LiveView, where wire size depends on whether a template happened to introduce
   a local variable. We are trading automatic optimisation for a property nobody
   can silently break — the teardown's central lesson.
3. **Provenance becomes exact.** One fragment ↔ one render call ↔ one entry in
   `Patch.updates`. FR-41's standalone resolvability is a lookup, not a
   reconstruction.
4. **Memory.** Option (c) retains the previous render per fragment; at ~4 KB × 8
   fragments that is 32 KB per session — half of §6's entire ceiling, spent to
   save LAN bytes.

**Falsifier and the exit ramp.** If Phase 5 shows wire bytes dominating
event→paint at the dashboard workload (53 updates/s, equivalence spec §3.4),
BL-14 becomes the first post-v1 optimisation, and Phase 5 will already have
measured the baseline it needs.

### 5.2 Fragment identity (FR-21)

A fragment is declared in templ and carries a stable ID matching
`^[A-Za-z0-9_:.-]{1,64}$` (protocol.md §3.3). The registry rejects duplicate IDs
at registration with a developer-facing error naming both call sites — never a
silent last-write-wins.

### 5.3 Dirty tracking

The reducer may declare touched fragments; if it declares none, every registered
fragment is considered dirty. Over-declaring is safe (a no-op render is dropped
by §5.4); under-declaring is a correctness bug and is caught by a
`livetest.AssertDirtyComplete` helper that re-renders everything and compares.

### 5.4 Identical-render suppression

Each fragment's last emitted bytes are hashed (64-bit). A re-render producing the
same hash emits nothing. Cost: 8 bytes per fragment per session (≈128 B at 16
fragments), which is affordable in §6 in a way that retaining the bytes
themselves is not. **A suppressed render still advances `transition_id`**, so
the provenance log records that the transition happened and produced no patch —
otherwise P4 would be unverifiable.

### 5.5 Determinism (FR-19, R-7)

Same state → byte-identical HTML, across runs and processes. The known hazard is
Go map iteration in a template (PRD R-7): the documented rule is *no `range` over
a map in a templ component; range a sorted slice*. Enforced by the
repeated-render byte-equality test in the correctness suite, and by a lint in the
examples.

---

## 6. Memory ceiling per idle connection (checklist §11.9.5; PRD G2, Q2)

### 6.1 The target, and where TLS sits

> **Gate: ≤ 46,080 bytes (45 KiB) of steady-state memory per idle connection,
> measured with TLS terminated OUTSIDE the measured container.**
>
> **Secondary, reported alongside with no target attached: the same measurement
> with TLS terminated in-process, for the single-binary deployment.**

**Cycle 1 had this the other way round, and it was wrong.** The in-process-TLS
figure is what `go run ./examples/...` does, which is why it looked like the
right gate. But PRD §5.L makes the Next.js comparison the project's headline, and
Next.js is idiomatically deployed behind a terminating proxy. Measuring
gotth-live *with* `crypto/tls` record buffers against a Node process *without*
them is an ~18,000 B asymmetry in our own disfavour — and, worse, it is an
asymmetry the equivalence spec does not currently forbid, because it does not
mention TLS at all.

Three reasons the TLS-outside figure is the right gate:

1. **Symmetry.** It is the only configuration in which both stacks are measured
   doing the same work. FR-73's honesty clause cuts both ways; an
   unfair-to-ourselves comparison is still an unfair comparison.
2. **It measures the thing under comparison** — the application server's
   per-session cost — rather than a TLS library's buffer-retention strategy.
3. **It removes the single largest unmeasured line from the headline number.**
   The 18,000 B TLS estimate becomes a secondary diagnostic, where being wrong
   costs a corrected diagnostic rather than a moved gate.

It is also what this monorepo actually deploys: Caddy terminates TLS at the edge
and reaches backends over Tailscale.

#### 6.1.1 TLS decision text, for transplanting into equivalence-spec §3.6

> **TLS boundary (binding on both stacks).** TLS is terminated **outside the
> measured container**, identically on both sides. Each stack runs behind the
> same reverse-proxy image, in a separate container, on the same host; the proxy
> container is **not** included in `M(x)`. The measured container therefore
> serves plaintext HTTP/WebSocket on its container port in both cases, and
> `M(x)` — cgroup v2 `memory.current` minus `memory.stat`'s `file` — covers only
> the application server. Terminating TLS inside one stack's container and
> outside the other's is a **disqualifying method error**, in either direction.
> gotth-live additionally reports an in-process-TLS figure as a labelled
> secondary diagnostic; it is not a comparison row and no Next.js counterpart is
> required.

#### 6.1.2 Pre-registered decision rule (before any Phase 1 measurement)

Fixed **now**, so no choice is made after seeing a number — the discipline L9-1
required in B-3(b) and the same one the equivalence spec exists to enforce:

- The gate is the **TLS-outside** figure. The benchmark method is therefore
  **independent of what the TLS estimate turns out to be**: there is no
  measurement outcome for which changing the TLS boundary is an available
  remedy. This is the structural fix; the rules below are the residue.
- If the measured **in-process TLS** line differs from the 18,000 B estimate by
  any amount, **nothing about the gate or the benchmark changes.** The secondary
  diagnostic is re-reported at its measured value.
- If the measured **TLS-outside** total exceeds **46,080 B**, the target does
  **not** move. The overage is attributed to a named line in §6.2 and either
  engineered down or escalated to an ADR that moves the target with L9-1's
  approval and the measurement in hand. **A benchmark-method change is not an
  available remedy for a missed memory target.**
- If the measured total comes in below **36,864 B (36 KiB)**, the gate is
  **tightened** to the measured value plus 10 %, in the same PR. A target that
  cannot ratchet down is a target that stops constraining.

### 6.2 Where it goes — the measurement, and the estimate it replaces

> **Corrected at checkpoint 3 (PRD v0.5 Phase 3 box), with the measurement in
> hand. The estimate below was 42,416 B. The measurement is 82,559 B —
> 1.95× the estimate and 1.79× the 46,080 B gate — and with default-on
> observability removed it is still 57,135 B, 1.24× the gate.**
>
> **The gate does not move.** §6.1.2 pre-registered that before any measurement
> existed, and §6.1.2's remedy — attribute the overage to a named line and
> either engineer it down or escalate to an ADR carrying the measurement — is
> what §6.2.3 does. A benchmark-method change was not available and was not
> sought.
>
> Full method, manifests, host state and raw data:
> [docs/bench/g2-baseline.md](../bench/g2-baseline.md).

#### 6.2.1 What was measured

equivalence-spec §3.6, unmodified: `mem_per_session = (M(N) − M(0)) / N`, the
Idle workload, N = 1000, TLS terminated outside the measured container (asserted,
not assumed), `GOGC=100`, `GOMEMLIMIT=2GiB`, the smallest complete live
application, five independent runs at observability-on and three at
observability-off.

| Per idle session | **Headline** (obs on) | Secondary (obs off) | §6.2.2 estimate |
|---|---:|---:|---:|
| goroutine stacks, both goroutines (`/memory/classes/heap/stacks:bytes`) | 26,804 | **15,794** | 16,384 |
| **heap, live** (`/gc/heap/live:bytes`) | 22,715 | 22,239 | 10,516 |
| heap, allocated-but-not-live at unforced steady state (the `GOGC=100` headroom, measured rather than derived) | 27,313 | 14,485 | 10,516 |
| runtime metadata, scheduler, and resident anon not attributed elsewhere | 3,801 | 3,272 | 1,000 |
| kernel, charged to the cgroup (`memory.stat kernel`, of which `slab` ≈1,580) | 1,794 | 1,740 | 4,000 |
| kernel socket memory (`memory.stat sock`) | **0** | **0** | *inside the 4,000 above* |
| **Total — TLS terminated outside (the gate's quantity)** | **82,559** | **57,135** | **42,416** |
| *labelled secondary: the same measurement after `debug.FreeOSMemory()`* | *55,656* | *43,802* | *—* |

The **headline** column is the configuration equivalence-spec §5.6 makes the
headline: default-on observability, "that is what a user gets". The secondary
column is the configuration this table implicitly describes, since it has no
observability line. **Both miss the gate**, at 1.79× and 1.24×.

Three things the measurement **confirmed**, and they are worth as much as the
misses:

- **Exactly two goroutines per session.** 2,007 goroutines at N = 1000 against 7
  at N = 0, and 207 against 7 at N = 100, in all eleven runs of all three cells.
  §3.4's claim is measured, not argued.
- **This table's goroutine-stack line is right — for the library with
  observability off.** 15,794 B measured against 16,384 B budgeted, 96.4 %.
- **`GOGC=100` doubles the heap, as this table's model says.** Allocated-but-not-
  live measured 14,485 B against 22,239 B live with observability off. The model
  was right in shape; the base it doubled was less than half the real one.

#### 6.2.2 The estimate, as it stood, and what it got wrong

Kept whole and unedited, because a corrected number with the wrong one deleted
tells a reader nothing about how far off it was.

The **heap?** column marks the lines the `GOGC=100` doubling applies to, so the
GC-headroom figure is derived rather than asserted (advisory A-2). Goroutine
stacks are not GC heap; kernel socket memory is not in the process at all.

| Component | Bytes | Heap? | Basis |
|---|---:|:---:|---|
| session actor goroutine stack | 8,192 | no | Go initial stack; idle actor blocked in `select` |
| conn read-pump goroutine stack | 8,192 | no | §3.4 — exactly two goroutines |
| 2 × runtime `g` + scheduler bookkeeping | 1,000 | no | runtime-owned, not GC heap |
| TCP socket, kernel-side (cgroup v2 charges this — `memory.stat sock`) | 4,000 | no | idle socket structs |
| WebSocket read buffer | 4,096 | **yes** | library default — **moved to 512 in `5a2ca417`; see §6.2.4** |
| WebSocket conn struct, ping/close state | 2,000 | **yes** | |
| session struct: identity, 6 counters, registry ≤16 fragments | 2,000 | **yes** | |
| fragment render hashes (§5.4) | 128 | **yes** | 16 × 8 B |
| **mailbox backing array** | **512** | **yes** | `chan *inbound`, cap 64 × 8 B — allocated eagerly at `make`, §3.3 |
| **ack channel backing array** | **256** | **yes** | `chan uint64`, cap 32 × 8 B — eager, §3.3 |
| **unacked window** | **1,024** ⚠ | **yes** | 16 slots × 64 B = 32 B metadata + a **32 B compact `spanRef`** (instrumentation §3.3), not a full `trace.SpanContext`. ⚠ **stale, and knowingly left rather than patched** — see the note below |
| application state | 500 | **yes** | counter example's is 24 B |
| *heap-resident subtotal* | *10,516* | — | the base the next line doubles |
| GC headroom (GOGC=100 ⇒ up to 1× the heap subtotal again) | 10,516 | — | **derived**, not asserted |
| **Total — TLS terminated outside (the gate)** | **42,416** | | vs. 46,080 ⇒ **7.9 % headroom** |

**⚠ This table is also missing a line that no marked row can show you: the
transport keeps a WRITE buffer as well as a read buffer, and there has never been
a row for it.** It was 4,096 B for as long as this table has existed and is
1,024 B today. §6.2.3 item 2 found it, §6.2.4 carries it, and §6.2.5 makes it the
fifth line of ADR-001's X3. It is called out here because the preamble's rule —
keep the estimate whole so a reader can see how far off it was — cannot mark an
omission, only a wrong number, and an unmarked omission reads as a considered
zero. *(L9-1, 2026-08-05: this sentence is what discharges C-35's falsifier in
substance. That falsifier asked for the read-buffer row to read 512; editing the
row would break this section's own preamble, which I approved, so the departure
is upheld — ADR-001 §7.2 — and this is the sentence it is upheld with.)*

**⚠ The window row is stale in two directions at once, and is scheduled rather
than patched** ([review-wave ruling 3.2](../reviews/rulings-review-wave.md)).
`37df5537` (REV-INV BR-1) made the ring retain `AckWindow + 1` slots instead of
`AckWindow`, so the row is **17 × 64 = 1,088** today, not 1,024 — the actor's
own spec already moved (`actor_test.go`). REV-DEL finding 2 then deletes
`slot.bytes` and `slot.emittedNS`, which are two of the four fields §7.1's
paragraph enumerates, taking a slot from 64 B to 48 B and this row to
**17 × 48 = 816**. Both moves are the same cell, so patching it now would
publish a third wrong number and re-derive the subtotal, the GOGC doubling, the
total and the headroom percentage twice. **They land together, in the
finding-2 commit, with the figure taken from `retentionSlots() ×
sizeof(slot)` rather than retyped** — this table feeds a *gate*, and a gate
figure carried forward by hand is how the 41/48-versus-40/49 error in
api-surface §0 survived a checkpoint.

**The in-process-TLS secondary is measured, never derived from this table, and
that is why it no longer has a row.** Cycle 2 carried a `crypto/tls` line of
18,000 B marked heap-resident and a secondary total of "≈62,000" that was not
produced by this table's own method: 62,000 is neither 60,416 (the TLS line
added without the GOGC doubling every other heap line receives) nor 78,416 (the
doubling applied consistently). Rather than pick one, the line is withdrawn from
the arithmetic altogether, for a reason that is not bookkeeping:

- equivalence-spec §3.6 now **requires** the in-process-TLS figure to be
  obtained by re-running the same measurement procedure with a TLS listener in
  the measured container. A number derived from a composition budget cannot
  test a composition budget.
- `tls.Conn` sizes its retained record buffers to the largest record it has
  seen, so the 18,000 B estimate is a function of the workload, not of this
  design. Doubling a workload-dependent estimate and publishing the result to
  five figures would give it a precision it has never had.

The 18,000 B estimate survives as prose, where it belongs: it is the reason
§6.1 makes TLS-outside the gate, and O7 still flags it as unmeasured. Nothing
about the gate, the composition budget, or §6.1.2's decision rule depends on it.
For the record, had it stayed in the table, this table's own method would give
**78,416 B** — heap subtotal 28,516, doubled, plus 21,384 non-heap — and the
Phase 1 measurement will say what the real figure is.

**What changed from cycle 1 and why the number moved.** The old
"unacked window + mailbox slots | 1,500 | empty when idle" line was wrong twice:
a buffered channel's backing array is allocated eagerly, so "empty when idle" is
never true of it, and the line was never sized to include the per-slot span
context instrumentation §3.3 stores. Those are now three separate, sized lines
totalling 1,792 B, and two design changes hold the cost down — `chan *inbound`
instead of `chan inbound` (§3.3), which saves 6,656 B, and a 32-byte `spanRef`
instead of a `trace.SpanContext` (instrumentation §3.3), which saves ~512 B.

**Two lines in the table remained estimates and were flagged in §16 O7:** the
kernel socket line (4,000) and the WebSocket conn struct (2,000). The GC line is
not an estimate — it is derived from the marked subtotal. The TLS estimate
(18,000) is a third unmeasured number O7 still tracks, but it is no longer a
line *in this table*: see the note directly under it. §6.2.3 says what the
measurement did to each of them.

#### 6.2.3 Where the overage is, per §6.1.2

§6.1.2 requires the overage to be **attributed to a named line** and then either
engineered down or escalated with the measurement in hand. Named, largest first.

**1. Default-on observability costs ≈25,424 B per session, and ≈11,010 B of it
is a permanently doubled goroutine stack.** This line does not exist in §6.2.2
at all, and it is the single largest term in the overage. With the three
observability hooks nil, the two goroutines cost 15,794 B — 96.4 % of this
table's 16,384 B budget. With them wired, 26,804 B. Go's goroutine stack starts
at 2 KiB and grows by **doubling**: two at 8 KiB is 16,384 B, one of the two at
16 KiB instead is +8,192 B, and the measured +11,010 B is that plus the stack
allocator's span accounting. **Adding spans and metric recordings to the actor
and read-pump paths pushed at least one goroutine past a doubling boundary, and
Go shrinks stacks only at GC and only to a quarter occupancy — so it is
retained, not transient.** The forced-GC floor confirms it. Nothing budgets
this: NFR-1 budgets observability's *latency* at ≤ 5 % and nothing budgets its
memory. On the Idle workload the provenance log emits one record per mount and
nothing after, so log volume is not what this is.

**2. Live heap: 10,516 B budgeted, 22,239 B measured with observability off
(+11,723 B), and the instrumentation is not the cause** — the two cells differ by
476 B here, which is noise. One named line is visible without further
instrumentation, and it is a line the table does not have: **the transport keeps
a write buffer as well as a read buffer.** `websocket.Accept` takes the
connection through `http.Hijacker`, which hands over net/http's own
`*bufio.Reader` **and** `*bufio.Writer` — 4,096 B each in the standard library's
defaults — and `websocket.Conn` retains both for the connection's life
(`conn.br`, `conn.bw`). §6.2.2 budgets "WebSocket read buffer | 4,096" and
nothing for the writer, so **4,096 B per connection is missing from the table by
construction**. The remaining ≈7,600 B is not attributed to individual lines and
is not guessed at: cgroup accounting cannot see inside the heap, and separating
"WebSocket conn struct" from "session struct" needs the per-component heap
profile §6.3's diagnostic paragraph describes, which this baseline did not run
(docs/bench/g2-baseline.md §7.5).

**3. GC headroom: 10,516 B budgeted, 14,485 B measured with observability off.**
Not a separate mistake — it is line 2 propagating through a model that was
right. With observability on it is 27,313 B, and the extra is a second copy of
the garbage line 1 allocates per mount.

**4. The kernel socket line was over-estimated, in our favour, and is the one
line to come *down*: 4,000 B budgeted, ≈1,790 B measured**, of which `slab` is
≈1,580 B. `memory.stat`'s `sock` read **exactly 0** in every sample of every
window — an idle socket with nothing queued charges no socket memory to the
memcg, and the real kernel cost lands in `slab`. O7's kernel-socket line is
closed by measurement, and it closed favourably.

**5. The WebSocket conn struct (2,000 B) is still not separately measured.** It
is inside line 2. O7's second line is *not* closed; it is subsumed.

**And one figure that fits, which this document is not allowed to quote as the
baseline.** With observability off *and* a forced GC, an idle session costs
43,802 B — under 46,080. equivalence-spec §3.6 makes the unforced figure the
headline ("that is what a deployment sees") and §5.6 makes default-on
observability the headline configuration ("that is what a user gets"); reaching
43,802 B requires changing both rules after seeing the numbers, which is the
disqualifying method error §6.1.2 exists to prevent. It is recorded because
hiding it would be the mirror of quoting it, and because it says the gate is
reachable and says what would have to change to reach it.

**Escalation.** §6.1.2's response to a miss of this size is an ADR that moves the
target with L9-1's approval and the measurement in hand, or engineering the named
lines down. **Neither is DEV-1's to decide unilaterally and neither is done
here.** What this landing does is the part §6.1.2 makes a precondition for
either: the measurement exists, it is reproducible, and the overage has names.
PM-1 and L9-1 own what happens next; the gate stays at 46,080 B until one of
them moves it.

#### 6.2.4 The composition as of `5a2ca417` — 2026-08-04/05

**§6.2.2 above is the estimate as it stood and is deliberately not edited**, for
the reason its own preamble gives. This is the composition of the lines that
have since *moved*, with the basis of each, and it exists because ADR-001's
condition **C-14(1)** requires §6.2 and ADR-001 §7's **X3** to change in the same
landing when any of X3's four named lines moves. `5a2ca417` moved one and did
not do this; L9-1 found it as **C-35**.

| Line | §6.2.2 | Now | Basis |
|---|---:|---:|---|
| WebSocket **read** buffer | 4,096 | **512** | `internal/wsx/hijack.go: readBufferBytes`. A code constant |
| WebSocket **write** buffer | *no line* | **1,024** | `writeBufferBytes`. **The line this table never had.** The transport retains net/http's `bufio.Writer` as well as its reader for the connection's life (`conn.bw`), which §6.2.3 identified and which was 4,096 B for as long as this table has existed |
| WebSocket conn struct, ping/close state | 2,000 | **2,370** | **MEASURED** — `ce52d2f9`'s per-component heap profile. **§16 O7's conn-struct line is closed by measurement**, and it closed slightly against us |
| net/http retained request state | *no line* | **0** | Was ≈2,280 B — a `*conn`, a `*response` with a third 2,048 B buffer, and the `*Request`, all held for the session's life by a blocking `ServeHTTP`. `5a2ca417` returns at the upgrade, and it is gone |
| 2 × runtime `g` + scheduler bookkeeping | 1,000 | **820** | **MEASURED** — `runtime.malg` |
| session struct, mailbox, acks, window, hashes | 2,000 + 128 + 512 + 256 + 1,024 | **3,010 together** | **MEASURED** — `session.New`, against 3,920 B budgeted across those five lines. **The library's own per-session structures are UNDER budget** |
| kernel socket, cgroup-charged | 4,000 | **≈1,790** | **MEASURED**, §6.2.3 item 4, and `memory.stat`'s `sock` read exactly 0 |

**What the measured heap actually did**, by §3.6's own secondary rather than by
composition: live heap per session was 22,559 B before `9f88d75e`, 22,653 B
after it — unmoved — and **12,179 B after `5a2ca417`**. The −10,474 B is the two
hijacked `bufio` buffers and net/http's retained request state, which is what
that commit set out to remove and what this table now records.

**One line did not move and the prediction that it would was wrong.** Giving the
read pump a fresh goroutine, rather than net/http's, was expected to start it on
a smaller stack. `/memory/classes/heap/stacks:bytes` measures 12,812 B/session
before and **12,780 B after** — 32 B, which is nothing. The goroutine-stack class
was moved by `9f88d75e` and not by `5a2ca417`, and it is recorded here because
a prediction that fails is worth more than one that is quietly dropped.

#### 6.2.5 X3 adopted at 13,759 B — L9-1, 2026-08-05 (C-14(1)'s same-landing move)

ADR-001 condition **C-14(1)** requires X3 and this section to change together.
X3 is adopted at **13,759 B/connection** ([ADR-001 §7.2](../adr/001-transport.md)),
so this is §6.2's half of that landing. The composition is §6.2.4's, read as X3's
five lines:

| X3's line | B | In §6.2.4 |
|---|---:|---|
| WebSocket **read** buffer | 512 | row 1 — code constant |
| WebSocket **write** buffer | 1,024 | row 2 — **the line §6.2.2 never had** |
| WebSocket conn struct | 2,370 | row 3 — measured at `ce52d2f9` |
| conn read-pump goroutine stack | 8,192 | *not in §6.2.4* — §6.2.2's row, unmoved, and still an estimate |
| its runtime `g` | 410 | row 5's 820 B for two descriptors, halved |
| **composition** | **12,508** | |
| **X3 = composition × 1.1** (§6.1.2's ratchet) | **13,759** | replaces 16,384 B |

Three things about this figure bind anyone who moves it:

1. **It bounds RETAINED bytes, not this table's totals.** Three of the five lines
   are GC heap, totalling 3,906 B, and this table applies the `GOGC=100` doubling
   to heap lines as a separate derived row. A §3.6-headline measurement of the
   same connection would therefore read up to ≈16,414 B while the transport pays
   exactly its budget. ADR-001 §7.2.4 states the quantity and the instrument.
2. **The read-pump stack is bounded, not measured.** The settled campaign's
   goroutine-stack class at `d66e4953` — 12,943 B/session obs on (5 runs),
   13,681 B obs off (2 runs), §9.10.6 and §9.10.10 — is below 2 × 8,192 in every
   run, so at most one of the two per-session goroutines is at 8,192 B and none
   exceeds it. That makes 8,192 a true ceiling for the line; **ADR-001 C-45**
   owes the value.
3. **One transport line is still missing from both tables.** The per-connection
   `context.WithCancel` (`internal/wsx/conn.go:78`); `ce52d2f9`'s profile carries
   ≈1,200 B for two of them without splitting the transport's from the actor's.
   **ADR-001 C-46** owes it. Charging the transport all 1,200 B still leaves
   13,708 B under the adopted 13,759 B ceiling, which is why adopting now is safe
   and why the next unbudgeted line forces a re-derivation.

#### 6.2.6 A budget line for default-on observability — ADR-002, APPROVED WITH CONDITIONS

[ADR-002](../adr/002-observability-memory-budget.md) is **approved** by L9-1 on
2026-08-05, with §3.1's derivation clause refused and replaced. Its §8 carries the
ruling; this is the half that lands in §6.2, and it lands in two pieces because
the measurement says the term is in two places.

**Piece 1 — a gate sub-line, in this landing.**

| Budget | B/session | Basis |
|---|---:|---|
| **default-on observability, per session** | **4,050** | measured `headline − observability_off` at `d66e4953` = **3,682 B** (g2-baseline §9.10.10, 5 runs against 2), plus §6.1.2's 10 % |
| the same, when §4 first measured it | 25,424 | −85.5 % across `9f88d75e` and `5a2ca417` |
| what the runs actually support | **+1,765 … +6,124 B** | every obs-on run is above every obs-off run, so the term's *sign* is settled; its size is a difference of two cells whose own spreads are 5.5 % and 4.4 % |

It is a **sub-line of the 46,080 B gate and not an allowance beside it** —
ADR-002 §3.3, which is the load-bearing half of that document and is approved
without amendment. It constrains; it cannot move the G2 figure or the G2 verdict
in any direction. Its change rule is ADR-002 §3.2 (C-14's shape), with one
addition: a change quotes **both cells' run counts** as well as the arithmetic,
because this line is a difference of two measurements and a difference of two
medians taken at 5 and 2 runs is not the same claim as one taken at 5 and 5.

**Piece 2 — a composition row, in a named follow-up (ADR-002 C-48, DEV-1).**
§6.2's table budgets *retained* bytes. The measured 3,682 B is mostly **not**
retained: at the shipping tree the obs-on cell's live heap is +343 B/session
against obs-off (band +174 … +542 across the runs, and §9.10.1's caveat means
even that is an upper bound, since this secondary carries the process's fixed
live heap divided by N and the instrumented process's includes the OTel SDK's),
while its goroutine-stack class is **738 B/session smaller** (band −295 … −1,180,
non-overlapping). Retained per-session state attributable to the hooks is
therefore **not distinguishable from zero and is ambiguous in sign**; the
headline difference lives in mapped-but-not-live heap, consistent with
§9.10.10's GC-cycle hypothesis. So the composition row is small, is derived, and
**excludes `obs.SpanRef`** — `internal/session/window.go:15-19` holds it in every
slot whether or not a `Tracer` is configured, and §6.2.2's window row already
budgets it (16 × 64 B, of which 32 B is the `spanRef`). Deriving it into an
observability line would count it twice and would still not describe the term.

### 6.3 Measurement method — adopted verbatim from the equivalence spec

Per equivalence-spec Appendix A item 4, RFC-0001 adopts equivalence-spec **§3.6**
without modification, so Phase 5 measures one thing once:

```
mem_per_session = ( M(N) − M(0) ) / N
```

where `M(x)` is the **median of 60 samples taken at 1 Hz over the last 60 s of a
5-minute steady-state window**, with `x` sessions established, and `M(x)` is the
serving container's cgroup v2 `memory.current` minus `memory.stat`'s `file`
(page cache), read from outside the process. `M(0)` is measured after the *same*
warm-up as `M(N)`. Workload: **Idle** (connected, no application events,
heartbeats only), **with TLS terminated outside the measured container per
§6.1.1**. Concurrency: **N = 1000** for the gate, N = 100 reported alongside. `GOGC` and `GOMEMLIMIT` pinned and disclosed in the run manifest. The
synthetic session driver and its 10-real-Chromium-tab validation gate apply
unchanged.

**Additional gotth-live-only diagnostic** (does not replace the headline): the
per-component attribution of §6.2, obtained from `runtime/metrics`
(`/memory/classes/*`, `/gc/heap/live:bytes`), goroutine count, and a no-op-session
harness that opens connections without registering an application, isolating
library overhead from application state. This is what ADR-001 exit criterion X3
uses.

**Sub-linearity check:** per-session memory at N = 1000 must be within 15 % of
N = 100. If it grows, some structure is O(N) per session and that is a design
defect, not a tuning problem.

**Measured at checkpoint 3: −15.6 %** (82,559 B at N = 1000 against 97,812 B at
N = 100, three runs each). Outside the ±15 % bound, and outside it in the
direction this paragraph's diagnosis does not cover: the figure **fell**, which
is a term that is not per-session being divided by ten times more sessions, not
an O(N) structure — and the goroutine count is exactly 2 per session at both
concurrencies. It is reported as outside the bound anyway, because the bound as
written is two-sided and reading it as one-sided *after* seeing the number is
the move §6.1.2 exists to make unavailable. See docs/bench/g2-baseline.md §4.3.

### 6.4 Why 45 KiB, argued against the teardown's data

- **The only published vendor figure in the prior art is Blazor's ~250 KB per
  circuit** (~273 KB/user at 5,000 users, teardown §4.7). 45 KiB is **≈5.6×
  better**. The comparison is defensible because both numbers cover the
  framework's own per-connection state — and note Microsoft's figure is also for
  a process that is not doing TLS termination, so the boundary matches.
- **We deliberately do not claim to beat LiveView**, because LiveView publishes
  no per-connection number and the figures circulating in blog posts are
  uncorroborated (teardown §1.7). Any such claim requires measuring LiveView
  ourselves — PRD BL-27.
- **The number is set by architecture, not aspiration.** Two goroutines (§3.4),
  no per-connection compression contexts (ADR §4.3 — context-takeover deflate
  alone is 1.2 MB, ~19× this entire budget), no retained previous renders (§5.1),
  and metadata-only ack windows (§7.1) are the four decisions that make 45 KiB
  reachable — joined in cycle 2 by a fifth, `chan *inbound` over `chan inbound`
  (§3.3), which alone is 6,656 B per connection. Reversing any of them breaks it, which is why each is stated as a
  consequence here rather than left as an implementation preference.
- **~~Headroom is honest, not generous.~~ There is no headroom: the estimate was
  wrong by 1.95×.** This bullet argued that §6.2's 42,416 B against a 46,080 B
  gate — **7.9 %** — was a target tight enough to constrain, and that §6.1.2's
  ratchet would tighten it if the measurement came in far enough under. The
  measurement came in at 82,559 B (§6.2.1). The ratchet is not reached; the
  overage is. **All four architectural decisions above survive the measurement
  intact** — two goroutines per session is measured, no compression context is
  measured, no previous render is retained, and the ack window is metadata-only —
  and the goroutine-stack line they imply is measured at 96.4 % of its budget
  with observability off. What misses is a term none of the four is about: the
  per-session cost of default-on observability, which no budget in this document
  or in NFR-1 has a line for, plus a heap subtotal that was under by 2.1×
  (§6.2.3). The
  paragraph is left standing with its correction attached rather than deleted,
  because the argument for **45 KiB as a target** is unchanged by the
  measurement: what changed is that the design does not currently reach it, and
  §6.1.2 says which of us gets to respond to that and how.

---

## 7. Flow control: the acknowledged window (adopted from Blazor)

RFC 0000 §4.6 identified Blazor's `RemoteRenderer` as the best idea in the prior
art: sequence-numbered batches, a bounded unacknowledged window
(`MaxBufferedUnacknowledgedRenderBatches = 10`), explicit client acks, and replay
on reconnect. We adopt three of the four.

### 7.1 Our version

| Property | Blazor | gotth-live |
|---|---|---|
| Sequence numbers | `BatchId` | `server_seq`, refined `where this > 0` |
| Bounded in-flight window | 10 batches, **frames retained** | **16** frames (`ack_window`, 1–256), **metadata only** |
| Client acknowledgement | `OnRenderCompletedAsync(batchId)` | `Ack{server_seq}` = highest **contiguous** applied |
| Replay on reconnect | yes — circuit survives | **no** — the session dies with the connection (§8.5); recovery is a `Snapshot` |

**Why metadata-only, and why it is not a downgrade.** Retaining frame bytes for
replay costs up to `ack_window × max_patch_size`; at 16 × 4 KB that is 64 KiB —
**the entire §6 budget**, per session. And there is nothing to replay *into*: our
session does not outlive its connection, so a reconnect gets a fresh actor and a
`Snapshot` anyway. Dropping replay collapses two recovery paths into one, and the
surviving path (`Snapshot`) is the one exercised on every reconnect and every
deploy — so it is continuously tested, unlike Blazor's resume path, which
Microsoft still has open bugs in (teardown §4.9, `aspnetcore#64607`).

The window retains, per slot, **64 bytes in two halves**: 32 bytes of ack
metadata (`server_seq`, `patch_id`, byte count, emit timestamp) and the 32-byte
`spanRef` instrumentation §3.3 stores so a client's morph timing can be linked
back to the span that encoded the patch. **64 B × 16 = 1,024 bytes**, which is
the figure §6.2's window line carries.

⚠ **Both halves of that sentence are scheduled to move**, and this paragraph is
the other half of §6.2's marker. The count is `retentionSlots()`, which
`37df5537` made `AckWindow + 1`; and *"byte count, emit timestamp"* names
`slot.bytes` and `slot.emittedNS`, which are written twice per patch and read
nowhere (REV-DEL finding 2), so the enumeration is what will make the per-slot
figure 48 B. Whoever lands finding 2 rewrites this paragraph and §6.2's row in
the same commit, from the struct rather than from these sentences.

Cycle 1 said "32 bytes × 16 = 512 bytes" here, counting only the first half. The
second half is not optional — it is what closes the trace loop to the browser —
so the honest per-slot cost is 64 B, and §6.2, instrumentation §3.3 and this
line now agree on it.

### 7.2 What the window buys

1. Bounded server memory under a slow client — the failure LiveView has no
   defence against (teardown §1.6).
2. A **detection signal** with a number, which checklist §11.9.6 requires.
3. Confirmed-delivery provenance (protocol.md P7) — no system in the teardown has
   this.

### 7.3 Purity makes backpressure nearly free

When the window is full the actor **does not queue patches**. It keeps reducing
(state must stay current), marks fragments dirty, and skips render+emit. When an
ack re-opens the window, it renders **once, from current state**. Skipped renders
cost nothing because `render` is pure (FR-18) — the intermediate frames were
never needed, only the latest state is.

Memory under backpressure is therefore **O(number of fragments)**, not O(pending
patches): a dirty bitset, not a queue. This is the single most important
consequence of the functional core and it is why §6's ceiling survives the
dashboard workload.

### 7.4 Slow-client policy (checklist §11.9.6)

| Stage | Detection signal | Threshold (default) | Action | Metric |
|---|---|---|---|---|
| **Coalesce** | `unacked_depth` | ≥ `ack_window/2` = 8 | pending updates for the same `fragment_id` collapse, last-write-wins; contributing event IDs are **unioned into `Origin.contributing_event_ids`** so provenance survives coalescing (PRD Phase 3 requirement) | `gotthlive_patches_coalesced_total` |
| **Degrade** | `unacked_depth` | = `ack_window` = 16 | render+emit stall per §7.3; the actor **synthesizes a `SlowClient` event into its own mailbox** so the application can shed deterministically (§7.5) | `gotthlive_slow_client_events_total` |
| **Evict** | window full **and** (one `Conn.Write` exceeds `write_deadline` = 5 s **or** window continuously full for `slow_client_grace` = 30 s) | — | close `4009 SLOW_CLIENT` | `gotthlive_connections_closed_total{code="slow_client"}` |

`gotthlive_outbound_window_depth` is exported as a histogram so the stages are
visible before eviction, not only after.

**Coalescing has a hard flush trigger, and it exists to make provenance loss
unreachable.** `Origin.contributing_event_ids` is bounded at 1,024 by
protocol.md H-4. That bound is *reachable in normal operation*, not only under
attack: at the dashboard workload (53 updates/s) with `slow_client_grace` at
30 s, a single session can accumulate ~1,590 contributing events before
eviction. Two behaviours were available and both are wrong — truncating makes
protocol.md **P5** ("set equality, not sampling") false and loses provenance
silently, which is an automatic return under the reviewer's own philosophy; and
erroring lets a slow client kill its own session by a path nobody designed.

So the bound is neither: **it is a flush trigger.** When the union reaches
`coalesce_flush_at` (default **512**, half of H-4's ceiling), the actor emits the
coalesced patch immediately rather than continuing to coalesce — even though the
window is still under pressure. The list therefore cannot overflow, no
contributing event is ever dropped, and the cost of the flush is one extra frame
against a client that is already behind. The ceiling in H-4 remains as the
schema-level assertion that the flush worked.

### 7.5 Backpressure visibility to the application (PRD Q13)

**The reducer may not read transport state directly.** Doing so would make
`Reduce` non-deterministic — the same event log would produce different results
under different network conditions, destroying FR-15's determinism harness and
checklist §2.10's replayability property.

Instead, backpressure enters as **an event**: the actor synthesizes
`SlowClient{depth, since}` into its own mailbox at the degrade threshold, and
`ClientRecovered` when the window drains. The reducer sees it as ordinary input,
so the transition is in the event log, replayable, and deterministic. The
application can switch a dashboard to a coarser update mode; the library does not
have to guess.

This is the actor model paying for itself: application-visible backpressure
without impure application code.

---

### 7.6 `ResyncRequest` has its own, much tighter budget

A GAP resync re-renders **every registered fragment** and emits a full
`Snapshot` (§8.3) — the most expensive operation the server performs, and the
only one whose cost is triggered directly by client input. Cycle 1 left it inside
the general 50/s event bucket, which is amplification: **50 full-state renders
per second per session, from one authenticated client, is a self-service DoS.**
§11.3.1's rate-limiting sentence covered `Heartbeat`, `Ack`, and
`ClientTelemetry` — the frames that were *exempted* — and said nothing about the
one frame that triggers a full re-render.

| Control | Default | Behaviour |
|---|---|---|
| `MinResyncInterval` | **1 s** | a `ResyncRequest` arriving sooner is answered with `Error{RATE_LIMITED}` and **no render** |
| `ResyncBurst` | **3** | independent bucket; does **not** draw from `MaxEventsPerSecond` |
| sustained abuse | — | close `4008 RATE_LIMITED` |
| no-op short circuit | — | if `last_applied_seq` already equals the current `server_seq` there is no gap, so the server replies with an `Ack` rather than a `Snapshot` — the common case for a spurious request costs nothing |

Metrics: `gotthlive_resync_requests_total{result}` with
`result ∈ {snapshot, noop, rate_limited}`, and `gotthlive_resync_bytes` (already
in instrumentation §2.3). Carried into the protocol as invariant **H-14**, and
into the API as two `Limits` fields.

The amplification test is an exit criterion (§15 E13): 50 `ResyncRequest`/s from
one authenticated client must not produce 50 full renders.

#### 7.6.1 What a refused client does — the schedule, which is protocol-visible

*(L9-1, 2026-08-05, discharging **C-41**/D-31. Read out of `client/runtime.js`,
which is DEV-2's source; this paragraph describes what that code does, and where
it and this document disagreed, the code is what is documented.)*

`Error{RATE_LIMITED}` on a `ResyncRequest` is refused work, and the client is
still missing a patch — so a refusal **re-arms the request and does not clear the
gap latch**. This is a protocol-visible schedule and it was previously described
nowhere, which is how **D-29** happened: this section said a refused resync cost
"no render" and stopped, the client was built to that, and a latched client
stopped acking as a side effect of having stopped applying, filled the outbound
window, and was rescued ~30 s later by §7.4's slow-client eviction.

| Property | What the client does |
|---|---|
| trigger | `Error{code = RATE_LIMITED}` received while latched on a gap. The guard is the client's own latch, **not** the error's causal ids — a refused `ResyncRequest` carries a server-minted `event_id`, indistinguishable from the `client_ref` on an `Error` refusing an ordinary event |
| delay | **equal jitter**: `b = min(15 s, 1,000 ms · 2ⁿ) / 2`, then `delay = b + random(0, b)`, with `n` the count of consecutive refusals for this gap. The first retry lands in **[500, 1000) ms** |
| concurrency | **at most one timer armed, so at most one request in flight per gap.** A second refusal while a retry is armed does nothing |
| what it asks for | `ResyncRequest{last_applied_seq = the sequence the client actually holds, reason = GAP}` — one construction site, so the property holds for all three callers |
| what ends it | applying **any** `Patch` or `Snapshot` clears the latch and disarms the retry; a reconnect resets the latch, the timer and `n` together, because a retry armed against the old session would ask the new one for a gap it does not have |
| while the tab is hidden | the retry is **left armed** — unlike the reconnect timer, which is cancelled — because it writes one small frame on a socket that is already open. Becoming visible only *pulls it forward*; it cannot invent one |
| if the server refuses everything | the schedule reaches the server's own sustained-abuse close (`3 × ResyncBurst` consecutive denials, close `4008`), which is in the client's retried set, so the outer reconnect loop takes over |
| the base is a **guess** | `1,000 ms` is chosen because this table's default `MinResyncInterval` is 1 s. **The wire carries no retry-after**: `Error` carries a code, a message, the causal ids and `fatal`, and the `Snapshot`'s session parameters carry the heartbeat interval, the frame cap and the ack window — but not the resync budget. An operator who lengthens `MinResyncInterval` tells the client nothing, and the schedule grows until it stops being wrong. Closing that is a wire change and is **not** made here |

**Why equal jitter and not §8.4's full jitter.** Full jitter draws from zero
upwards to spread a herd of tabs disconnected by one event. A refused resync has
no herd — the bucket is per session and this client alone was refused — and a
delay near zero is precisely the request the server has just declined: spent,
counted against the same bucket, and one step closer to the `4008` close. **Here
the floor is the point.** Against a bucket that refills a token a second, a
legitimate client is served on its first or second retry and the server sees at
most one extra refusal.

---

## 8. Sessions, reconnect, and resync

### 8.1 Session ≠ tab state; session **=** connection

**Decision (PRD Q10): session lifetime is exactly connection lifetime. There are
no resumable sessions and no grace window in v1.**

Rationale:

- A server restart or deploy must work regardless, and that path is
  "reconnect → new session → `Snapshot`". If that path must exist and be correct,
  a *second* resume path is pure added surface — and it is the surface Microsoft
  still has open bugs in.
- Retention is a DoS surface. Blazor bounds it explicitly
  (`DisconnectedCircuitMaxRetained = 100`) for exactly this reason; not retaining
  is strictly simpler and strictly bounded.
- It matches PRD FR-22 ("evicted on close") and PRD §4's exclusion of durable
  session state (BL-8).
- It is LiveView's model, and LiveView's reconnect story is the one property of
  LiveView the teardown found unambiguously good (teardown §1.5).

Cross-tab shared state — e.g. equivalence-spec F-CTR-5, two tabs of one user
showing the same counter — is therefore **an application concern solved with
pubsub effects**, not a library concern solved with shared actors. Each tab has
its own actor; both subscribe on `Mount` and unsubscribe on teardown (FR-56).
The library never shares state between actors, which keeps checklist §2.9 true
by construction.

### 8.2 What the user sees

`<html data-gotth-status>` transitions `live → reconnecting → live`. During
`reconnecting` the DOM is frozen at the last applied patch and remains fully
interactive for HTMX and native controls. Live controls are not disabled by the
library; the application may style off the attribute.

### 8.3 Resync (checklist §11.9.2 — the hard part, answered)

| Question | Answer |
|---|---|
| **What is serialized?** | **Nothing.** State never leaves the server, in any form, ever. |
| **What is recomputed?** | **Everything.** Resync = re-run `Mount` (on reconnect) or re-`render` current state (on gap), then emit a `Snapshot` containing the full rendered HTML of every registered fragment. |
| **Size?** | Equal to the total rendered size of all live regions — i.e. the live portion of the initial page. Phase 3 measures it for the dashboard example (PRD §6). |
| **Versioning across restart/deploy?** | **There is none to version.** A reconnect after a deploy is indistinguishable from any other reconnect: new session, `server_seq = 1`, fresh `Snapshot` from the new binary's `Mount` and `render`. No state format crosses a version boundary, so there is no migration and no compatibility matrix. |
| **What does the client see?** | `reconnecting` status → `Snapshot` → **morph**, not replace. Because the Snapshot is morphed, focus, caret, scroll, uncontrolled input values, `<details>` state and the rest of FR-25 survive the resync even though server state did not. |

This makes checklist §2.11 ("state is serializable for resync") true **vacuously
and uniformly**: every state field is "derived-and-recomputed-on-resync". A new
state field can never break resync, which removes an entire class of review
burden.

Two resync triggers, one code path:

- **Gap** (FR-11): client's expected `server_seq` ≠ received → client stops
  applying immediately, sends `ResyncRequest{GAP}`; the actor is alive, so it
  re-renders current state.
- **Reconnect**: new connection, new actor, `Mount`, `Snapshot`.

### 8.4 Reconnect backoff

Exponential with **full jitter**: `delay = random(0, min(cap, base·2^n))`,
`base = 250 ms`, `cap = 15 s`, unlimited attempts while the tab is visible,
paused while hidden (`visibilitychange`) and resumed immediately on focus.

The jitter is not decoration: LiveView ships `RELOAD_JITTER_MIN/MAX = 5000/10000`
ms precisely because synchronised remount storms after a deploy are a real
production failure (teardown §1.5). Full jitter is the cheaper, better-studied
form.

**There are two client schedules in this design, deliberately different, and this
is the only place they are compared.** This one is reconnect: full jitter,
`base = 250 ms`. The other is the `ResyncRequest` retry after
`Error{RATE_LIMITED}` (§7.6.1): **equal jitter**, `b/2 + random(0, b/2)` over
`min(15 s, 1,000 ms · 2ⁿ)`, sharing this schedule's 15 s cap and not its base.
The difference is load-bearing rather than an inconsistency — full jitter's draw
from zero is what spreads a herd, and a resync refusal has no herd, so a delay
near zero there would only re-spend the budget the server has just declined.
*(L9-1, 2026-08-05, C-41: a reader who implemented a second client from §8.4
alone would build the wrong one, which is the defect D-31 filed.)*

### 8.5 Delivery semantics (PRD Q6 / R-12) — an application-visible contract

> **Events are at-most-once. Patches are exactly-once, in order, or a gap is
> detected and a `Snapshot` follows.**

- The client does **not** retry an event that was unacknowledged when the
  connection dropped. A click during a network failure may be lost; the user sees
  server truth after resync and can act again.
- The alternative (at-least-once) requires every application reducer to be
  idempotent — pushing correctness semantics into user code, which R-12 names as
  the thing to avoid.
- **In-flight effects at disconnect:** the session context is cancelled; effects
  observe cancellation. An effect that already committed externally **stays
  committed**, even though its patch never reached the client. This is stated in
  the docs as an application-visible contract, because it is the one place where
  at-most-once leaks: *an effect may have executed even though the user never saw
  its result.* Applications needing stronger guarantees put the idempotency key
  in their own domain, where it belongs.

---

## 9. Panic recovery (FR-23; RFC 0000 §6.3 concluded this is mandatory)

Go has no supervision tree. A panic in any goroutine kills the process unless
recovered. This is the largest single thing BEAM does better than Go for this
workload, and the mitigation is not optional.

**Three guarded sites, one helper.** Every goroutine in the library is started by
`session.spawn`, which installs the guard, the metric, and the `WaitGroup`
registration. A bare `go func()` in the library is a review return
(checklist §6.4).

| Site | On panic |
|---|---|
| **Reduce** | State is **not** advanced. Because reducers may not mutate their input (checklist §2.6), the pre-transition state is intact and correct — rollback is free, not implemented. `transition_id` advances (the attempt is in the log); `state_version` does not. |
| **Render** | The fragment is not patched and is marked failed. Resync is **not** triggered — a render that panics will panic again, and a resync loop is worse than a stale fragment. Other fragments in the same transition still emit. |
| **Effect** | The effect's result event is replaced by a synthetic failure event — `live.EffectFailedEvent`, carrying `source`, `error` and `retryable` — so the reducer sees a deterministic failure rather than silence. A panic is classified **terminal**, not by default but on its own merits: re-running a panicking effect re-runs the bug, and the panic budget would then close the session on a loop the library scheduled for itself. See the amendment note below. |

Every recovery: structured log at error level with `session_id`, `event_id`,
`transition_id`, site, and stack; `gotthlive_panics_total{site}`; and an `Error`
frame carrying the causal ID — **stack included in dev mode, generic message in
prod** (FR-23, checklist §5.9).

**Escalation.** The session survives a panic. If the same site panics
`panic_budget` times (default 3) within one session, the session closes
`4012 INTERNAL_ERROR`. Other sessions are unaffected — verified by the Phase 2
criterion that injected panics in reducer, effect, and render each contain to one
session while others keep serving.

**The effect-failure event — amended 2026-08-04, L9-1, on the
[checkpoint-2 ruling batch](../reviews/checkpoint-2-batch.md) item 4.** This
section previously wrote the synthetic event as `EffectFailed{source, err}`, a
shape that never shipped and could not have: the reducer receives an ordinary
`live.Event`, not a typed variant, so the contract is a **name and three field
keys**, and until DEV-1's `820752f6` all four lived under `internal/` where no
application could reach them. The shipped contract is:

| Symbol | Value | Meaning |
|---|---|---|
| `live.EffectFailedEvent` | `"gotth.effect_failed"` | the event name; **not** in `Config.Events`, because the library mints it and registration is what makes a name sendable by a browser |
| `live.EffectFailedSourceField` | `"source"` | the failed effect's `EffectSource()` |
| `live.EffectFailedErrorField` | `"error"` | the error's message, or the panic value |
| `live.EffectFailedRetryableField` | `"retryable"` | `"true"` only when the executor claimed the failure transient with `live.Retryable` |

`live.Retryable(error) error` is how an executor makes that claim, and it is the
only way to make it. **Unclassified is terminal, and the direction is the
design.** An effect may have committed externally before it failed — the message
was published, the row was written — so retrying a failure nobody classified
risks doing that twice, and retrying is an assertion about idempotence that only
the code which performed the effect is in a position to make. A failure never
retried costs a change that visibly does not happen; a failure retried blindly
costs a change that happens twice, in data somebody else owns. Between a visible
omission and an invisible duplicate the default belongs on the omission, which is
checklist §5.4's default-deny reading applied to failure rather than to
authorization.

There is deliberately **no** exported `IsRetryable(error) bool`. The
classification is *set* by the executor and *read* by the reducer, and what a
reducer holds is the event, not the error; a symmetric-looking predicate would be
an export with no call site, which FR-65 makes a rejection. It is re-addable, and
`docs/api-surface.md` §7 records the trigger: something needing to inspect an
error it did not itself produce.

**Why a field and not a second event name.** A reducer that does not care matches
one name; a reducer that does reads one field; and a reducer that reads it wrongly
reads a value that is not `"true"` — which is the terminal answer, and the safe
one. Two event names would have made not-caring the thing that requires effort.

---

## 10. Morph, client runtime, and HTMX interop

### 10.1 Morph strategy

Idiomorph-style ID-based matching with a persistent-ID pantry, implemented in our
runtime rather than vendored, because the FR-25 preservation contract, the
`data-gotth-preserve` opt-out (FR-27), the IME rule (FR-26), and the
fragment-ownership boundary (§10.3) all need to be *inside* the traversal rather
than wrapped around it — which is exactly why LiveView forks morphdom and why
Datastar inlined its own (teardown §1.4, §2.4).

**Ownership rule:** morph is applied **within a declared fragment's subtree
only**. Nothing outside a declared fragment is ever touched. This is what makes
FR-31 (HTMX regions on the same page) safe by construction rather than by care.

### 10.2 Event binding (FR-28)

**One delegated listener at the document**, dispatching on `data-gotth-*`
attributes.
No per-node handlers, so a morphed subtree is interactive with no re-binding
step, and morph cannot destroy bindings. The listener never calls
`preventDefault` on an event it does not own, so `hx-*` behaviour is untouched
(FR-31).

### 10.3 The HTMX boundary (FR-32; PRD R-11)

FR-32 permits either a developer-facing error or "a documented, tested precedence
rule". We choose the **precedence rule**, because a server-side scan of rendered
HTML for `hx-*` attributes would cost CPU on every render for a developer-time
mistake:

> **Innermost declaration wins.** Inside a declared live fragment, a node marked
> `data-gotth-preserve` and its subtree are never touched by morph — this is the
> sanctioned way to host an HTMX-driven region inside a live region. An `hx-*`
> element inside a live fragment **without** `data-gotth-preserve` is
> server-owned: morph will overwrite it, and any HTMX swap into it will be
> reverted by the next patch.

Deterministic, documented, and testable — the QA-1 case is exactly that sequence.
The Phase 4 inspector (FR-44) additionally flags `hx-*` inside an unpreserved
fragment, which is where a developer will actually notice it.

### 10.4 Size ledger (NFR-3; PRD R-2) — with measured evidence

| Subsystem | Budget (gzip B) | Evidence / basis |
|---|---|---|
| morph | 5,000 | idiomorph 0.7.4 minified measures **3,350 B gzip**; morphdom 2.7.7 measures **3,063 B** (both measured 2026-08-04 from jsDelivr, `gzip -9`). The delta is FR-25/26/27 plus §10.1's ownership boundary. |
| transport (WS, backoff+jitter, heartbeat, ack) | 1,600 | ADR-001 exit criterion X2 |
| proto codec (generated; varint, tags, skip-unknown, length bounds) | 2,000 | fixed 8-kind schema, protocol.md §10 |
| event binding + delegation | 1,300 | one document listener, attribute parsing |
| provenance + telemetry | 700 | `client_ref` counter, patch tracking, morph timing, `ClientTelemetry` |
| bootstrap, status attribute, error surfacing | 500 | |
| **Subtotal** | **11,100** | |
| **Reserve** | **1,188** | |
| **Ceiling (NFR-2)** | **12,288** | `gzip -9` over the minified single file |

**PRD R-2 is measurably overstated and should be downgraded.** It says
"idiomorph is roughly half the budget before anything else." Measured, idiomorph
is **3,350 B gzip = 27 %** of 12,288, not ~50 %. Reproduce:

```
curl -sSL -o m.js https://cdn.jsdelivr.net/npm/idiomorph@0.7.4/dist/idiomorph.min.js
gzip -9 -c m.js | wc -c        # 3350
```

The budget is tight but not the crisis R-2 describes. It remains the reason
protocol.md §10.3 declines to ship an RE2 engine on the client.

---

## 11. Security

### 11.1 Origin validation (FR-45; checklist §5.1, §11.9.4)

Validated on the **HTTP upgrade request**, against an allowlist the embedding
application supplies, **before any session state is allocated**. Deny by default:
no wildcard, no Origin-reflection, no empty-Origin pass. Binding to a
non-loopback address with no allowlist configured is a **startup error** unless
`Config.Origins` contains the deliberately greppable sentinel `live.AnyOrigin`
(api-surface §4; the cycle-1 spelling `live.AllowAnyOrigin()` was one of the
~14 `WithX` symbols the ledger cut in favour of `Config` fields). Negative test:
a disallowed origin is rejected with zero allocation attributable to the
attempt.

### 11.2 Authenticated establishment (FR-46; checklist §5.2)

Ordering is the security property, and it is fixed in protocol.md §8.1:
**origin → authenticate → CSRF → subprotocol → `101` → mint `session_id` → spawn
actor.** Authentication runs against the *HTTP request* via a consumer-supplied
hook, so it composes with whatever the application already uses (this monorepo's
Authelia included). Identity is **immutable for the connection's life**; there is
no re-auth and no privilege change mid-session, because a session cannot outlive
its connection (§8.1). Anonymous sessions require an explicit opt-in.

### 11.3 Per-event authorization (FR-47; checklist §5.3, §11.9.3)

```go
type Authorizer interface {
    Authorize(ctx context.Context, s Session, ev Event) error
}
```

**Where it sits:** at the **single mailbox ingress**. The precise property — and
it is the one that matters, since the actor has three typed inputs (§3.1), not
one — is:

> **One goroutine owns session state. It has three typed inputs — `mailbox`,
> `acks`, `ticker`. Only `mailbox` can reach a reducer, and exactly one function
> writes to it.**

`acks` and `ticker` are transport and timing plumbing that cannot reach
application code, which is what §11.3.1 already depends on. The one function is:

```
Conn.Read → protocol.ParseInbound → ingress() ─┬─ authorize()  ── mailbox <- ev
                                                └─ (non-event frames, §11.3.1)
```

`ingress` is the only caller of `mailbox <-`. A new event kind cannot skip the
hook because there is no other way in.

**What it receives:** the immutable session identity, the **refined** event
(name, fragment ID, fields — all past their predicates), and a context carrying
the request-scoped span. **What it can return:** `nil` (allow), a `DenyError`
(typed `Error{UNAUTHORIZED}` frame, **no state mutation**, connection stays open),
or a `FatalDenyError` (close `4006`).

**Structural proof, not convention.** A conformance test walks the `Frame`
payload oneof via protoreflect (the same walk as protocol.md §5.2) and asserts
every client→server kind either routes through `authorize()` or appears in an
explicit, reviewed exemption list. That list is §11.3.1 and nothing else may join
it without editing the test.

#### 11.3.1 The exemption list, stated precisely

`Heartbeat`, `Ack`, and `ClientTelemetry` are **protocol** frames: they never
reach a reducer, never mutate application state, and are handled entirely by
named plumbing (liveness, the §7 window, and the telemetry sink respectively).
They are rate-limited on the same path as events. `ResyncRequest` **does** reach
the actor but cannot mutate state — it triggers a re-render of current state — so
it is authorized as a distinguished event kind rather than exempted.

So the precise claim is: **no frame that can reach a reducer bypasses
`Authorize`, and the frames that cannot reach a reducer are enumerated here and
individually accounted for.**

**Unknown event kinds are default-deny** (checklist §5.4): an event whose `name`
is not registered produces `Error{UNKNOWN_EVENT}` and is counted — never ignored,
never dispatched.

### 11.4 CSRF (FR-48; checklist §5.5)

**Transport: WebSocket. Mechanism: origin allowlist + a handshake token bound to
the authenticated application session, both checked on the upgrade request.**

The posture is materially simpler than the SSE+fetch alternative (ADR-001 §2.5):
after the upgrade, **no ambient-credential request exists** — the open connection
is the capability, and a cross-origin page cannot obtain one because `Origin` is
browser-set on the upgrade and not forgeable from script. Verified by the FR-48
cross-origin attack test: a page on a disallowed origin can neither establish a
session nor inject an event into an existing one.

### 11.5 Resource limits (FR-51) and secret hygiene

Bounded, safe by default, each with a typed error and a defined close:
inbound events/sec (token bucket, default 50/s burst 100), **`ResyncRequest`
rate — a separate and much tighter bucket, 1 s minimum interval and burst 3
(§7.6), because a resync is the one client-triggered full re-render**, inbound
frame size (protocol.md H-5), mailbox depth and ack-channel depth (§3.3),
outbound window (§7), sessions per identity (default 20), total sessions
(default unbounded with a documented warning; set it).

**Never logged or traced at default levels** (checklist §5.6): session tokens,
cookies, `Authorization` headers, authorization-decision inputs, **full state
snapshots**, and **raw frame payload bodies**. Redaction is applied at the
logging boundary, not left to callers — `docs/instrumentation.md` §6.

### 11.6 CSP and escaping

The runtime works under `script-src 'self'; object-src 'none'` with no
`unsafe-inline` and no `unsafe-eval`: no `eval`, no `new Function`, no
`setTimeout("string")`, no inline handlers, no dynamic import (FR-49, NFR-4;
CI static scan). Rendering is templ's contextual escaping; any raw-HTML path is a
named typed opt-in documented as a footgun (FR-50).

---

## 12. Failure modes (checklist §11.4)

ADR-001 §5 covers transport-level failures. This table covers the architecture.

| Failure | Detection | Degradation | Recovery |
|---|---|---|---|
| Reducer panics | recover at §9 site | state unchanged; `Error` frame with causal ID; session survives | 3 panics in one session → close `4012` |
| Render panics | recover at §9 site | that fragment stale; others patch normally | app fix; no resync loop |
| Effect panics or hangs | recover / `effect_drain_timeout` | `live.EffectFailedEvent` carrying `source`, `error`, `retryable` → deterministic app handling (§9) | app decides, on the classification; a panic is always terminal |
| Client never acks (stalled tab) | `unacked_depth` = window, `write_deadline` | coalesce → degrade → evict (§7.4) | reconnect + `Snapshot` |
| Client floods events | token bucket, mailbox depth | `Error{RATE_LIMITED}`, then close `4008` | client backoff |
| Fragment ID collision | registry registration | developer-facing error naming both sites | app fix; fails loudly at startup, not at runtime |
| Under-declared dirty fragments | `livetest.AssertDirtyComplete` | stale fragment in prod | test catches it pre-merge |
| Map iteration in a template | repeated-render byte-equality test (FR-19) | nondeterministic HTML, churn | documented rule §5.5 |
| Deploy / restart | all connections close `4001` | every client reconnects | jittered backoff §8.4 → `Snapshot`; no state migration exists to fail (§8.3) |
| Memory ceiling exceeded | §6.3 measurement, Phase 1 baseline + Phase 5 gate | G2 miss | §6.2 attribution names the component; §14 lists the two likeliest |

---

## 13. PRD §7.2 open questions — answered

| # | Question | Answer | Where |
|---|---|---|---|
| 1 | Transport | **WebSocket** | ADR-001 |
| 2 | Memory target + method | **≤ 45 KiB, TLS terminated outside the measured container**; in-process TLS reported as an untargeted secondary. Equivalence-spec §3.6 verbatim, plus the TLS boundary text of §6.1.1 for QA-2 to transplant, and the pre-registered decision rule of §6.1.2 | §6 |
| 3 | Causal ID generation | **Both, layered**: server mints the authoritative chain (unforgeable); client mints a `uint64` local correlation handle echoed back in `Origin` | protocol.md §4.1 |
| 4 | Client-side refinement enforcement | **Directional and generated**: codec generated from the same descriptors; length predicates enforced client-side at ~0 marginal bytes; `matches`/range predicates server-only; a committed, drift-checked manifest states exactly which | protocol.md §10.3 |
| 5 | Resync strategy | **Full re-render snapshot. Nothing is serialized.** | §8.3 |
| 6 | Delivery semantics | **Events at-most-once; patches exactly-once-in-order-or-resync.** Effects may have executed without the user seeing the result — stated as an app-visible contract | §8.5 |
| 7 | Fragment granularity | **Whole declared fragment per patch**; developer controls size by declaring fragments | §5.1 |
| 8 | Nested-message validation | **Staged `Validate*` calls from `pkg/liquidproto`'s canonical Liquid Proto toolchain.** Envelope, matched payload, and repeated elements are validated explicitly; immutable scalar snapshots cross into the session. No research-tree implementation or vendored runtime remains | protocol.md §5 |
| 9 | Compression | **Off by default.** Context-takeover deflate costs ~1.2 MB/connection — ~19× the whole §6 budget. `NoContextTakeover` available as an option; Phase 5 measures it for R-9 | ADR-001 §4.3 |
| 10 | Session↔connection cardinality | **Session lifetime = connection lifetime.** No resumable sessions, no grace window | §8.1 |
| 11 | Rate-limit / slow-client policy | **Coalesce (≥8) → degrade (=16) → evict**, each with a metric | §7.4 |
| 12 | Module location and versioning | **Standalone module** `github.com/candacelabs/csf/pkg/gotth`, tagged `gotth-live/vX.Y.Z`. Joining `go/` would put gin and gRPC in consumers' `go.mod` | §14.1 |
| 13 | Backpressure visibility to the reducer | **Not directly** — that would break determinism. Surfaced as a synthesized `SlowClient` event | §7.5 |
| 14 | State version representation | **Monotonic `uint64`**, incremented iff state changed. Single node, single writer ⇒ no hash, no vector | protocol.md §4.1 |
| 15 | Next.js comparison configuration | **Deferred — not this RFC's.** The equivalence spec §5.4/§5.5 owns it | Owner: QA-2 + PM-1, Phase 0 |
| 16 | Who reviews the Next.js side | **Deferred — not this RFC's.** Already equivalence-spec Appendix A item 1 | Owner: PM-1 / orchestrator, before Phase 5 |

Q15 and Q16 are the only deferrals, both are outside this document's remit, both
have named owners, and neither is among checklist §11.9's six.

---

## 14. Module layout, dependencies, and the dev container

### 14.1 Standalone module — argued from what lands in users' `go.mod`

**Decision: a standalone Go module rooted at `candace/pkg/gotth/`, module path
`github.com/candacelabs/csf/pkg/gotth`, `go 1.26`, tagged
`gotth-live/vX.Y.Z`.**

The bar (NFR-9, FR-69, checklist §10.3 Tier 1) is what a consumer is forced to
accept. Joining the existing `go/` module fails it outright:

- `go/go.mod` is `module github.com/candace-server` and **requires 13 direct
  modules** including `gin-gonic/gin`, `google.golang.org/grpc`,
  `prometheus/client_golang`, `soheilhy/cmux`, and `rs/zerolog`. Go resolves
  requirements at **module** granularity, not package granularity, so a consumer
  importing gotth-live would inherit gin and gRPC in their build graph. That is
  disqualifying under a stdlib-submission bar, full stop.
- `github.com/candace-server` is not a resolvable remote import path. A consumer
  could not `go get` it.
- gotth-live must be able to release on its own cadence; a monorepo module ties
  its version to unrelated services.

**The current `go` directive is `go 1.26.0`.** Cycle 2 originally accepted Go
1.25 because templ required it. The 2026-08-11 shared-primitive centralization
then moved the primary Go module, gotth-live, and the Liquid Proto toolchain to
Go 1.26 together. That floor is now deliberate: gotth-live's generated Liquid
Proto validators import a Go 1.26 runtime, and — since the single-module fold —
it is not a separate module's runtime but a package of gotth-live's own module,
so the two cannot be on different toolchains. Pinning templ backwards would not
lower the current floor and remains the wrong trade.

During the unpublished bootstrap, a consumer needs ONE local replace,
`github.com/candacelabs/csf => /path/to/the/checkout/candace`, which brings
the library and the Liquid Proto runtime together. It used to take two, one per
module. The remaining replacement is transitional and is removed when the first
exported version is pinned after merge/publication.

The dedicated/vanity module path question is deliberately left to v1.0 (adjacent
to BL-30). Because the **directory** is `candace/pkg/gotth/` either way, changing it is
a one-line `go.mod` edit plus an import rewrite.

### 14.2 Package layout

```
candace/pkg/gotth/           the library's tree. The ONE go.mod is at candace/,
                               the export root two levels above
  ci.sh                        the build/vet/test sweep over this tree
  gen.sh                       codegen pipeline; needs the EXPORT ROOT mounted
  live/                        THE public package
  live/livetest/               test scaffolding (L9-1 ruling A1; see below)
  live/clientjs/               the shipped client artifact, go:embed'd by live —
                               data only, never a Go package
  proto/gotthlive/v1/          .proto sources
  internal/protocol/           ParseInbound, ValidateOutbound, framer, limits
  internal/protocol/gotthlivepb/   generated: frame messages + Validate* code
  internal/session/            actor, mailbox, window, effects, panic guard
  internal/render/             fragment registry, dirty tracking, hash suppression
  internal/wsx/                WebSocket — the ONLY importer of coder/websocket
  internal/obs/                metrics, traces, structured and provenance logging
  internal/obstest/            in-process collectors the obs specs assert against
  internal/livebridge/         the live→livetest var behind livetest.NewSession
  internal/clientcodec/        schema → client codec generator, and its golden
  internal/arch/               architecture tests over the real build graph
  internal/cmd/gen-clientcodec/
  client/                      client runtime SOURCE + its node tests; not a Go
                               package, not the shipped bytes. Built by tools/
                               into live/clientjs/
  test/internal/{conformance,chaos}/     packages of the one module
  test/{routers,sampling,memory}/        packages too — §14.1 argued them as
                               separate modules and the fold answered that
                               argument a different way
  tools/                       apisurface (FR-65) and minify (NFR-2)
  docs/
  docs/guide/_samples/         the guide's compiled samples
  bench/                       quarantined (FR-74) — node lives here and nowhere
                               else; bench/apps/*/gotth are three benchmark
                               applications, packages like the rest
  .dis/Dockerfile              library toolchain — NO node
  .dis/Dockerfile.bench        adds node/npm, bench only
```

**Updated 2026-08-11 against the tree it describes.** The annotation schema,
generator, and runtime live together at `pkg/liquidproto`, a sibling package of
this library in the same module; gotth-live keeps only its generated frame
messages and validators. The retired
`internal/refine`, `internal/protocol/refinepb`, and `frame_refine.pb.go` paths
are deliberately absent. The tree is re-derived from the tracked source rather
than patched, and no package or module count is retyped here: CI owns those
enumerations.

**Updated again 2026-08-27: every satellite module in the listing above is a
package now.** `tools/`, the three `test/` suites, the guide's samples and the
three benchmark applications each carried a `go.mod` when this section was
written; the single-module fold left exactly one, at the export root. Two
consequences are worth stating rather than inferring. The three example
applications are no longer in this tree at all — they are `examples/gotth/`,
beside it under the export root — and the guarantee that used to come from a
satellite's own `go.mod` now comes from its absence: `ci.sh`'s D-5 step fails if
any `go.mod` exists below the export root other than the root's own.
dependencies.md §0 carries the standing correction for the dependency claims
that argued from the old boundary.

**Two exported packages, and two is the cap** (amended here per L9-1 ruling A1,
condition C-12; cycle 2 said "one exported package, `live`"). Everything else
is `internal/`, so surface growth (FR-65) still has to be deliberate.

`live/livetest` is admissible on the `net/http/httptest` precedent and on a
concrete cost, not on taste. FR-15 *requires* the library to ship a determinism
helper; `ReplayN` and `AssertDirtyComplete` take `testing.TB`; and importing
`testing` from `live` would put `testing` — and with it `flag`, `regexp`,
`runtime/pprof` and `runtime/trace` — into the transitive import set of every
consumer's production binary. FR-65's concern is *surface*, and the surface is
unchanged by which of the two packages the eight `livetest` symbols live in;
they are counted either way (api-surface §0).

Three conditions attach, and all three are discharged or recorded here:

1. This section is the amendment, landed in the module-init PR, so no document
   mandates a rule the design has already declined.
2. **The justification is a test, not a claim.** `internal/arch` asserts that
   `live` does not transitively import `testing`, alongside the FR-2 isolation
   assertion of §3.5 and the no-`encoding/json`-on-the-wire assertion. The
   entire argument for the second package is that first claim; an unverified
   claim is how it quietly becomes false at the first convenient import.
3. **A third exported package requires an L9-1 ruling.** `livetest` is
   admissible because production code must not link it; that reasoning does not
   generalise to a `live/middleware` or a `live/otel` arriving later on
   convenience grounds. (D1's pre-registered Option-B fallback would create
   `gotth-live/otel` as a separate *module*, not a third package of this one.)

**Where the embedded client artifact lives** (amended 2026-08-04, L9-1, on the
addendum to the module-init review). `//go:embed` may not contain `..` and may
not name a file outside its own package directory, so `live` cannot embed from
`client/` and the original wording was not implementable. The shipped artifact
is emitted by the `tools/` esbuild module to `live/clientjs/gotth-live.min.js`
and embedded by exact filename; `client/` keeps the runtime source, the
generated codec, and the node tests. There is exactly one copy of the shipped
bytes, so no two-copy equality invariant exists to drift, and `-check` compares
a fresh build of `client/` against the committed artifact — the FR-7 staleness
check either way.

**This adds no exported package and the cap of two stands.** `live/clientjs/`
holds no Go file and is therefore not a package. Per C-20 the architecture test
asserts the module's non-`internal` package set is exactly `live` and
`live/livetest`, so the cap is enforced structurally rather than by vigilance.

**Two additions to the tree, recorded so the layout stays the written one.**
`internal/arch` exists so the architecture assertions have an owner and a name
rather than living in whichever package last needed them; it holds no non-test
code. Generated code lives in `internal/protocol/gotthlivepb` rather than in
`internal/protocol` itself, so the byte-reproducibility check has a clean target
and hand-written boundary code is never interleaved with generated code. The
base frame file imports `pkg/liquidproto`'s annotation binding, and the
generated validator file imports its runtime; neither is regenerated under a
gotth-live-local package.

### 14.3 Direct dependencies assumed by this design (NFR-9)

| Module | Tier | Why | Transitive |
|---|---|---|---|
| `github.com/coder/websocket` v1.8.15 | 1 | ADR-001 §4.1 | **0** |
| `google.golang.org/protobuf` | 1 | liquid proto is the wire format (FR-3); nothing else can produce or consume it | small, ubiquitous |
| `github.com/candacelabs/csf/pkg/liquidproto` | 1 | canonical Liquid Proto runtime used by generated `Validate*` code; the annotation schema and generator stay contributor-time only. **Not a module requirement** — it is a package of gotth-live's own module, so `go.mod` does not name it | no runtime module beyond protobuf, already required |
| `github.com/a-h/templ` v0.3.1020 | 1 | PRD's chosen and only v1 authoring path (BL-5 backlogs alternatives) | to be measured |
| `go.opentelemetry.io/otel/trace` + `go.opentelemetry.io/otel/metric` (**API modules only**) | 1 | FR-36 requires OTel-compatible traces and FR-38 one option each. **Settled by L9-1 D1: Option A — API only, consumer brings the SDK.** The concrete form proposed is the **submodules, never the `otel` root**: `otel/trace`'s own `go.mod` declares one runtime requirement, where the root declares eight (`otel/metric`, `otel/trace`, `go-logr/logr`, `go-logr/stdr`, `cespare/xxhash/v2`, `go.opentelemetry.io/auto/sdk`, …). D1 condition 1 requires the narrowest module that compiles, and this is it | measured graph delta quoted in the adding PR (D1 condition 2). **Pre-registered fallback (D1 condition 3): if enabling tracing adds more than 8 modules to a consumer's build graph, fall back to Option B** |
| Ginkgo v2 / Gomega / `go.uber.org/mock` | 2 | house convention (NFR-10, checklist §8.1) | test-only |

Logging is **`log/slog`** (stdlib) — **settled by L9-1 D2**, which reads
`go/CLAUDE.md`'s rule as "do not bypass `core.Logger` *inside the `go/` module*"
rather than "every Go artifact in this repository depends on zerolog".
gotth-live is a standalone module outside `go/` published at a
stdlib-submission bar, and a library that puts zerolog in every consumer's
`go.mod` fails that bar. **D2's binding condition:** the ~40-line
`slog.Handler` adapter binding library records to `core.Logger` ships **with a
test in the same PR** — a test that drives a library log record through the
adapter and asserts the fields arrive on `core.Logger`. An untested adapter is
how "nothing is lost on the inside" quietly becomes false.

Full justifications at the stdlib bar go in `docs/dependencies.md`, one entry per
direct dependency, in the PR that adds it (NFR-9).

### 14.4 Dev container

Contributor tooling runs in a [dis](https://github.com/candacelabs/dis)
container. Generation mounts the export root `candace/` — not just this
directory — because it builds the canonical Liquid Proto generator from
`pkg/liquidproto` in the same module.

**`.dis/Dockerfile` — library toolchain, deliberately node-free:**

```dockerfile
FROM golang:1.26
LABEL dis.shell="/bin/bash"
RUN apt-get update && apt-get install -y --no-install-recommends \
      git curl unzip ca-certificates && rm -rf /var/lib/apt/lists/*

ARG PROTOC_VERSION=35.1
RUN curl -sSL -o /tmp/protoc.zip \
      https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-x86_64.zip \
 && unzip -q /tmp/protoc.zip -d /usr/local bin/protoc 'include/*' \
 && rm /tmp/protoc.zip && chmod +x /usr/local/bin/protoc

RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@v1.36.11 \
 && go install github.com/a-h/templ/cmd/templ@v0.3.1020

# protoc-gen-liquidproto is built by pkg/gotth/gen.sh from
# /workspace/pkg/liquidproto, not fetched or baked into this image.
WORKDIR /workspace
```

**`.dis/Dockerfile.bench`** — `FROM` the above, adds pinned node/npm. It is the
**only** image with node, which makes FR-74's quarantine a property of the file
layout rather than a promise. G11 (clean-clone `go run` with no node, npm,
protoc, or `protoc-gen-liquidproto`) is verified in a stock **Go 1.26** image,
not the library image that already contains contributor tooling.

The contributor path is the module's committed scripts (`ci.sh`, `gen.sh`) and
the disposable toolchain container. There is no dependency on the retired
`candace research` operator surface, and no speculative `candace gotth` command
group is required to build, test, generate, or run an example.

---

## 15. Exit criteria (checklist §11.6, §11.7 — measurable, with falsifiers)

| # | Criterion | Number + method | Falsified if |
|---|---|---|---|
| E1 | Idle memory | **≤ 46,080 B/connection**, **TLS terminated outside the measured container** (§6.1.1), equivalence-spec §3.6 verbatim, Idle workload, N=1000 | > 46,080 B, or N=1000 differs from N=100 by > 15 %. Remedy is fixed in advance by §6.1.2 and does not include changing the benchmark method. **Checkpoint-3 baseline trips both: 82,559 B, and −15.6 % between N=1000 and N=100 (§6.2.1, docs/bench/g2-baseline.md). Not a Phase 5 failure yet — it is the baseline saying it will be** |
| E2 | Event→paint | **≤ 50 ms p50, ≤ 150 ms p99**, LAN, counter + chat, equivalence-spec §3.2 | p99 > 150 ms |
| E3 | Client size | **≤ 12,288 B**, `gzip -9` over the minified single file, with the §10.4 per-subsystem ledger reported every PR | > 12,288 B, or a subsystem exceeds its line without another under |
| E4 | Provenance totality | **100 %** of patch frames in a 30-min soak resolve; **0** unknown origins (protocol.md P1–P8) | any unresolved patch |
| E5 | Wire purity | **100 %** of captured message payloads parse as `Frame` and re-encode byte-identically; **0** text frames | any non-`Frame` byte |
| E6 | Actor isolation | `go test -race` clean under concurrent event injection; architecture test shows `internal/{session,render,protocol}` do not import `internal/wsx` (§3.5) | a race, or a forbidden import |
| E7 | Backpressure bounded | at 53 updates/s per session with a client throttled to 64 kbit/s: server memory per session stays within E1 + 25 %, other sessions unaffected, session degraded or closed per §7.4 | unbounded growth, or collateral impact |
| E8 | Leak | 10k connect/disconnect cycles return goroutine count and RSS to baseline ±5 % | drift beyond tolerance |
| E9 | Panic containment | injected panics in reduce/render/effect each contain to one session; other sessions keep serving; `Error` frame carries the causal ID | process death or cross-session impact |
| E10 | Authz has no bypass | descriptor-walk conformance test (§11.3) green; every client→server kind authorized or on the §11.3.1 list | a kind reaches a reducer unauthorized |
| E11 | Cross-origin | FR-48 attack test cannot establish a session or inject an event | either succeeds |
| E12 | Observability overhead | **≤ 5 %** of p50 event→paint with metrics + traces on vs fully off, chat example (NFR-1) | > 5 % |
| E13 | Resync amplification bounded | 50 `ResyncRequest`/s from one authenticated client produces **≤ 3 full renders in the first second and ≤ 1/s thereafter** (§7.6); the rest are `noop` or `rate_limited` | > 3 renders in the first second, or any sustained rate above `MinResyncInterval` |

---

## 16. Open questions (checklist §11.10 — distinct from the §11.9 six, none of which are deferred)

| # | Question | Owner | Needed by |
|---|---|---|---|
| O7 | **PARTLY CLOSED at checkpoint 3 by measurement (§6.2.1, docs/bench/g2-baseline.md).** The kernel socket line is closed and closed favourably: 4,000 B estimated, ≈1,790 B measured, with `memory.stat`'s `sock` reading exactly 0. The WebSocket conn struct (2,000 B) is **not** closed — cgroup accounting cannot see inside the heap, and it is subsumed into a live-heap total that measures ≈22,715 B against a 10,516 B budget; separating it needs the per-component heap profile §6.3 describes and this baseline did not run. The in-process TLS estimate (18,000) is **still unmeasured**, is still a secondary with no target, and is now the only wholly unmeasured number here. **The remedy is pre-registered in §6.1.2 and is not open**: a benchmark-method change is not available as a response to a missed target, and none was taken | DEV-1 + QA-2 | ~~Phase 1 baseline~~ measured at checkpoint 3; conn-struct attribution and the in-process-TLS secondary carry to Phase 5 |
| O11 | **`coalesce_flush_at` default of 512** (§7.4) is half of protocol.md H-4's ceiling, chosen for margin rather than measured. Phase 3's dashboard workload should tune it — too low costs extra frames to a client already behind, too high risks approaching the ceiling | QA-2 | Phase 3 |
| O12 | **`MinResyncInterval` 1 s / `ResyncBurst` 3** (§7.6) are set to make amplification impossible, not tuned. A legitimate client on a lossy link may resync more often than that | QA-2 | Phase 3 |
| O8 | **`ack_window` default of 16** is reasoned (§7.1) but not measured. Blazor uses 10 with retained frames; ours retains metadata, so a larger window is cheap. Phase 3 should tune it against the dashboard workload | QA-2 | Phase 3 |
| O9 | **Whether `Mount` re-running on every reconnect is acceptable for the application's database load.** LiveView's identical behaviour causes deploy-time query storms (teardown §1.5). §8.4's jitter mitigates the thundering herd but not the per-reconnect cost | DEV-1 + PM-1 | Phase 3 |
| O10 | **CLI command group wiring** (`candace gotth …`, §14.4) | unassigned | Phase 1 |

**Closed in cycle 2**, and deliberately not carried forward:

| Was | Disposition |
|---|---|
| **O1** — FR-2 amendment | **Struck (L9-1 D4).** PRD v0.2 amended FR-2 on 2026-08-04 and PM-1 accepted; §3.5 now cites the amended FR-2 rather than proposing a change. |
| **O2** — R-2 downgrade | **Accepted into PRD v0.2**, which restates R-2 with the measured 3,350 B / 27 % figure. |
| **O3 / O4** — `api-surface.md`, `dependencies.md` | **Written and committed** (L9-1 D5 assigned them to DEV-1). |
| **O5** — OTel tier | **Settled (L9-1 D1): Option A, API only.** §14.3 records the submodule form and all three binding conditions. |
| **O6** — logging | **Settled (L9-1 D2): `log/slog`**, with the adapter tested in its shipping PR. §14.3 records the condition. |

---

## 17. Changelog

### Checkpoint-3 gate — 2026-08-05: four L9-1 rulings, three of them against the settled campaign

Applied by **L9-1**, who holds ADR-001, ADR-002 and this RFC, under the same
authorship convention the review-wave entry below states: a reviewer corrects
drift in the document that drifted. Full reasoning:
[rulings-review-wave §8](../reviews/rulings-review-wave.md).

| Site | Change |
|---|---|
| **§6.2.5** (new) | **X3 adopted at 13,759 B/connection**, C-14(1)'s same-landing move for [ADR-001 §7.2](../adr/001-transport.md). `512 + 1,024 + 2,370 + 8,192 + 410 = 12,508`, plus §6.1.2's 10 %. Three binding notes: the figure bounds **retained** bytes and not this table's `GOGC`-doubled totals; the read-pump stack is **bounded above rather than measured** (the settled campaign's stack class is below 2 × 8,192 in every run, so no per-session goroutine exceeds 8,192 B); and one transport line — the per-connection `context.WithCancel` — is still in neither table (ADR-001 C-45, C-46) |
| **§6.2.2** | **One sentence added below the table, which is otherwise untouched**, naming the line the table is *missing* rather than the line it gets wrong. C-35's falsifier asked for the read-buffer row to read 512; editing it would break this section's own "kept whole and unedited" preamble, so the departure is upheld and this sentence is what discharges the falsifier in substance |
| **§6.2.6** (new) | **ADR-002 APPROVED WITH CONDITIONS.** A default-on observability budget of **4,050 B/session** (measured 3,682 B + 10 %) as a **sub-line of the 46,080 B gate**, never a carve-out, with the runs' supported band (+1,765 … +6,124 B) stated beside it. §3.1's "derived, never measured" clause is **refused**: its enumerated components are retained state, the measurement says the term is not retained state, and one component (`obs.SpanRef`) is paid with observability *off* and is already budgeted in §6.2.2's window row. The retained-state composition row is a named follow-up (C-48, DEV-1) |
| **§3.4** | **The sentence "there is no bare `go func()` in the library" was false of the tree and is replaced by a table of the five sites**, with each one's owner and the place that waits for it (C-35(b)). The "waited for at shutdown" clause is now true and the mechanism is named — `Close` → `c.done`, closed after `actorDone.Wait()` and `deregister` (C-34). "Exactly two goroutines per session" gains its measurement. The source half — `spawn`'s godoc, which makes the same claim — is **C-49**, DEV-1 |
| **§7.6.1** (new), **§8.4** | **C-41/D-31 discharged.** The client's `Error{RATE_LIMITED}` resync retry is written down from `client/runtime.js`: equal jitter over `min(15 s, 1,000 ms · 2ⁿ)`, one request in flight per gap, terminating at the server's own `4008`, with the base recorded as **a guess at a server default the wire cannot carry**. §8.4 gains the sentence saying there are two schedules and why they differ, which is the half that makes this document stop contradicting the client |

### Review wave — 2026-08-04: §14.2 re-derived from the tree, and §6.2's window row marked rather than patched

**Authorship note, because it matters who wrote which sentence.** This document
is DEV-1's. The edits below were applied by **L9-1** under
[review-wave ruling 3](../reviews/rulings-review-wave.md), on the D-checkpoint
convention that a reviewer corrects drift in the document that drifted rather
than filing a finding against it — the same convention that produced ruling A1's
amendment to §14.2 at module init. No design decision moved; three of the four
edits are the document catching up to `ls`.

| Site | Change |
|---|---|
| **§14.2, tree** | Re-derived from `find internal -name '*.go' -printf '%h\|sort -u'` rather than patched. It was missing four `internal/` packages that exist — `obs`, `obstest`, `livebridge`, `clientcodec` — and omitted `test/` and `tools/` entirely, which is where CI's FR-65 and NFR-2 gates run. `internal/refine` was described as a *"59-line"* runtime; it is 69 (REV-DEL finding 8). |
| **§14.2, prose** | *"the **eight** `livetest` symbols"* → no number at all. The ledger said ten when this was written and says nine now, and §0 of api-surface.md is the one place either count belongs because `tools/apisurface` reads it. A restated number in a second document is the failure mode api-surface §0 exists to have ended, and this was an instance of it. |
| **§14.2, note** | New paragraph records the correction and the derivation, so the next drift is caught by re-running a command rather than by a reviewer noticing. REV-DEL's own figure — *"`test/` (four separate Go modules)"* — is corrected to **three** in the process: `find . -name go.mod` prints twelve, of which `test/` holds `routers`, `sampling` and `memory`. |
| **§6.2 window row, §7.1 paragraph** | **Marked stale, not patched.** `37df5537` made the ring retain `AckWindow + 1` slots (1,024 → 1,088) without moving this table, and REV-DEL finding 2 will move the same cell again (→ 816) by deleting two slot fields §7.1 enumerates by name. Patching between two moves of one gate figure publishes a third wrong number; the two markers name the arithmetic, the source of truth (`retentionSlots() × sizeof(slot)`), and the commit that owns it. |

### Checkpoint 2 — 2026-08-04: the effect-failure contract, amended in the open

L9-1 [ruling batch](../reviews/checkpoint-2-batch.md) item 4, on DEV-1's
`c8f1aea2` and `820752f6`. **The change is accepted on its merits and this
document is amended to it**, following C-13's precedent — a requirement or a
design and a shipped surface that disagree silently is how the next reviewer
gets misled, so the RFC moves rather than acquiring a footnote.

| Site | Change |
|---|---|
| **§9, effect row** | `EffectFailed{source, err}` → the shipped contract: `live.EffectFailedEvent` carrying `source`, `error` and `retryable`. The old wording described a typed variant that never shipped and could not have, because a reducer receives an ordinary `live.Event`. |
| **§9, new note** | Records the four exported constants and `live.Retryable`, argues why unclassified is terminal, why the classification is a field rather than a second event name, and why there is no `IsRetryable` — with the re-add trigger named. |
| **§12, failure table** | The `EffectFailed` cell restated against the same contract, and the recovery cell now says a panic is *always* terminal rather than terminal by default. |

Nothing in the design moved. What moved is that an application can now reach the
contract: before `820752f6` the name and its field keys were `internal/`, so the
only way to handle a failed effect was to hard-code the string — and
`examples/counter` hard-coded a string nothing emits, shipping a failure path
that had never once run. That is the defect the amendment records, and the reason
this is an addition to the exported surface rather than a convenience.

### Phase 1, module init — 2026-08-04: conditions C-1…C-4 and C-12 closed

The five [cycle-2](001-review-cycle-2.md) conditions this document owns, swept
in the module-initialisation PR as L9-1 instructed rather than carried to
Phase 2. None was a design defect; all five were places where a fix in one
document left a stale sentence in this one.

| Condition | Closure |
|---|---|
| **C-1** — §3.5 still *proposed* the FR-2 amendment PM-1 had already accepted | §3.5 rewritten against amended FR-2 and retitled. The heading, the "Proposed resolution, for PM-1 and L9-1" framing and the "This RFC requests a PRD amendment" sentence are gone; the section now states the delivered isolation property and names `internal/arch` as where the test lives. L9-1 was right that this mattered: a changelog that is right 97 % of the time is trusted, which is what makes the 3 % expensive. |
| **C-2** — §7.1's "32 bytes × 16 = 512 bytes" contradicted §6.2's 1,024 B window line | Restated as **64 B per slot in two halves** — 32 B of ack metadata plus the 32 B `spanRef` — × 16 = **1,024 B**, with a sentence saying what cycle 1 undercounted and why the second half is not optional. §7.1, §6.2 and instrumentation §3.3 now agree. |
| **C-3** — §6.2's secondary total was not derived by §6.2's own method | **The secondary is withdrawn from the table.** "≈62,000" was neither 60,416 (undoubled) nor 78,416 (doubled), and picking one would have papered over the real problem: equivalence-spec §3.6 now *requires* the in-process-TLS figure to be measured by re-running the procedure, and a number derived from a composition budget cannot test that budget. The 18,000 B estimate survives as prose, where it is the reason §6.1 gates on TLS-outside; the arithmetic the table would have produced (78,416) is stated so nothing is hidden. The gate is untouched. |
| **C-4** — §11.1 cited `live.AllowAnyOrigin()` | Aligned on the ledger's spelling: the `Config.Origins` sentinel const `live.AnyOrigin`, with a parenthetical recording that the helper was one of the ~14 `WithX` symbols api-surface §7 cut. |
| **C-12** — ruling A1's three conditions | §14.2 amended in the PR that creates the module: **two exported packages, and two is the cap**, with the `httptest` precedent and the concrete cost argued rather than asserted, and the third-package rule stated. Condition 2 is discharged in code — `internal/arch` asserts `live` does not transitively import `testing`, and carries the §3.5 FR-2 assertion and the `encoding/json` ban with it. |

**Two layout additions are recorded in §14.2 in the same pass**, so the written
layout stays the built one: `internal/arch` (architecture tests, no non-test
code) and the split of generated code into `internal/protocol/gotthlivepb` and
`internal/protocol/refinepb` — the latter regenerated under our own import path
because `protoc-gen-go`'s blank import of the extension package would otherwise
name an unresolvable vanity path and break FR-7 and G11.

### Cycle 2 — 2026-08-04, in response to [L9-1 cycle-1 review](001-review-cycle-1.md)

**Verdict addressed: RETURN, 5 blocking + 4 advisory.** All five blockers fixed;
all four advisories applied; none declined. No decision is reversed and the
document is not restructured — §7 of the review lists what not to over-correct
and that list was respected.

| Objection | Change |
|---|---|
| **B-1** — §6.2's mailbox line, and "empty when idle" | Fixed, and it changed a design decision. A Go buffered channel allocates its backing array eagerly, so mailbox slots are never free. `chan inbound` at 64 × ~112 B would have been **7,168 B per idle connection**; the mailbox is now **`chan *inbound`** with pooled structs, costing **512 B**. §6.2 gives the mailbox, the ack channel, and the window **three separate sized lines** (512 / 256 / 1,024) totalling 1,792 B where cycle 1 had one 1,500 B line, and the window line now includes what instrumentation §3.3 actually stores. §3.3 states the coupling L9-1 identified: **mailbox capacity is a memory parameter, not only a flood-control parameter.** |
| **B-2** — unbounded ack channel | Fixed. `acks` is `chan uint64` cap **32**, full-channel policy **drop-and-count** on `gotthlive_frames_rejected_total{reason="ack_channel_full"}`. The justification is a protocol property, not a tolerance: `Ack.server_seq` is a *cumulative high-water mark*, so a dropped ack is superseded by the next and the window re-opens one round trip later — dropping is lossless in the limit, while blocking would stall the read pump's own liveness handling and unbounded would be a memory vector. §3.3 is retitled "The three bounded inputs" with a table. **§11.3's claim is tightened** to L9-1's formulation: *one goroutine owns session state; three typed inputs; only the mailbox reaches a reducer.* |
| **B-3** — TLS comparability and un-pre-registered remedy | **The gate is inverted.** §6.1 now gates on the **TLS-terminated-outside** figure at **≤ 46,080 B (45 KiB)**, with in-process TLS reported as an untargeted secondary. Cycle 1 had it backwards: measuring gotth-live with `crypto/tls` buffers against a Node process without them is an ~18,000 B asymmetry *against us*, and FR-73's honesty clause cuts both ways. **§6.1.1 is written as transplantable text for equivalence-spec §3.6** (same proxy image, separate container, proxy excluded from `M(x)`, asymmetry disqualifying in either direction). **§6.1.2 pre-registers the decision rule** before any Phase 1 measurement — the structural fix is that the gate no longer depends on the TLS estimate at all, so no measurement outcome makes a method change an available remedy; and the rule **ratchets down** if the measurement comes in under 36 KiB. |
| **B-4** — `contributing_event_ids` overflow | Fixed with L9-1's suggested mechanism. The H-4 bound is a **flush trigger**, not a truncation and not an error: at `coalesce_flush_at` (default **512**, half the ceiling) the actor emits the coalesced patch immediately. Provenance is never dropped (P5 stays true), the list cannot overflow, and a slow client cannot kill its own session by a path nobody designed. §7.4 states it; protocol.md H-4 cross-references it. New O11 flags the default as unmeasured. |
| **B-5** — `ResyncRequest` amplification | Fixed. New **§7.6**: `MinResyncInterval` 1 s, `ResyncBurst` 3, in a bucket **independent of the event bucket**, plus a no-op short circuit when `last_applied_seq` already equals `server_seq`. Metrics `gotthlive_resync_requests_total{result}`; sustained abuse closes `4008`. §11.5's limit list now names it. Carried into protocol.md as **H-14**, into the API as two `Limits` fields, and into §15 as exit criterion **E13**. L9-1 called this the one genuine security gap in the package; it was, and it is closed. |
| **A-1** — struct omits `acks`/`ticker` | Applied with B-2; §3.1 lists all three inputs with their capacities. |
| **A-2** — GC headroom applied to an unidentified heap portion | Applied. §6.2 gains a **Heap?** column, an explicit *heap-resident subtotal* (10,516 B), and the GC line is now **derived** from it rather than asserted. The figure barely moved — 10,516 against the guessed 10,000 — but the method is now legible before Phase 1 has to reconcile anything. |
| **A-3** — Go-version note understated | Applied, and it forced a decision the note had deferred. `a-h/templ` v0.3.1020 declares `go 1.25.0`, so the `go 1.24` floor was never available. §14.1 now sets **`go 1.25`** and argues it: pinning templ backwards to save one minor version would freeze the only authoring path PRD §4 permits, on a single-maintainer dependency, for a floor difference `go/go.mod` already declares. The monorepo's own 1.24/1.25 discrepancy is noted as **not** something gotth-live waits on. |
| **A-4** — §5.1 leads with the weaker argument | Applied. Option (b)'s rejection now leads with *the mechanism's defining production failure is that ordinary code silently defeats it*, and demotes the fork-a-dependency argument to second. |

**Governance incorporated (L9-1 §0, settled and not reopened):**

- **D1 / O5** — OTel admitted, **Option A, API only**. §14.3 records the concrete
  form: the **`otel/trace` and `otel/metric` submodules, never the `otel` root**
  (the root declares eight requirements; `otel/trace` declares one), which is
  D1's condition 1. Conditions 2 (measured graph delta in the adding PR) and 3
  (**pre-registered fallback to Option B above 8 added modules**) are recorded.
- **D2 / O6** — **`log/slog`**, with D2's binding condition that the
  `core.Logger` adapter ships **tested** in the same PR.
- **D4** — **O1 struck**; §3.5 cites the amended FR-2 rather than proposing it.
- **D5** — O3/O4 discharged: `api-surface.md` and `dependencies.md` are written
  and committed.
- **D3** — noted; no document change required.

**Cross-document consequences carried here:** §3.2 step 5 now routes emission
through `protocol.ValidateOutbound` (protocol.md **B-9**), and §6.2's window line
carries the 32-byte `spanRef` that replaced a full `trace.SpanContext`
(instrumentation **B-12**).
