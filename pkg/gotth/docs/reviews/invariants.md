# REV-INV — correctness through invariants

| | |
|---|---|
| **Reviewer** | REV-INV (principal engineer, invariants lens) |
| **Date** | 2026-08-04 |
| **Scope** | `internal/session/`, `internal/protocol/`, `internal/wsx/`, `internal/render/`, `client/runtime.js` |
| **Contract under test** | [protocol.md](../protocol.md) §4–§7 (H-1…H-14, P1…P8), [RFC-0001](../rfc/001-architecture.md) §7–§9, [instrumentation.md](../instrumentation.md) §4A |
| **Method** | state each invariant precisely, then construct the input or interleaving that breaks it; every BROKEN finding below was executed in `dis-gotth-live:latest` against a throwaway spec, not argued from reading |

Baseline before probing: `go test ./internal/session/` green, 85 specs.

Nine invariants are BROKEN with a reproduction. Two of the three highest-severity
ones share one root cause — **emission side effects are committed before the
emission succeeds** — and the third is an unvalidated application string reaching
a wire predicate, which is exactly the D-18 shape that `live/app.go` closed for
`Event.Contributing` and left open for `Origin.source`.

---

## 0. Severity table

| # | Invariant | Status | Severity | Site |
|---|---|---|---|---|
| BR-1 | H-11 / FR-36 cl.3 — client telemetry names a patch in the window | **BROKEN** (100 % of reports) | **High** | `window.go:86`, `runtime.js:769` |
| BR-2 | `Origin.source` predicate (`len<=64`, RE2) | **BROKEN** | **High** | `ingress.go:132`, `effects.go:163` |
| BR-3 | Renderer hash = markup the client last received | **BROKEN** | **High** | `renderer.go:174,209` + `actor.go:537` |
| BR-4 | H-4 / P5 — coalescing is a flush, never a loss | **BROKEN** | Med-High | `actor.go:508,520,537` |
| BR-5 | H-10 — `Snapshot` is the connection's first frame | **BROKEN** | Medium | `actor.go:297`, `actor.go:185` |
| BR-6 | H-14 — `ResyncRequest` obeys its own rate budget | **BROKEN** | Medium | `resync.go:52` |
| BR-7 | P4 — `state_version` rises iff state changed | **BROKEN** | Medium | `actor.go:1184` |
| BR-8 | `MaxSessions` bounds the process; drain covers every session | **BROKEN** | Medium | `handler.go:265,287` |
| BR-9 | P7 — supersession ranges are contiguous and non-overlapping | **BROKEN** | Low-Med | `resync.go:82` |
| U-1 | H-13 second enforcement site ("the client decoder") | HELD-UNCHECKED (absent) | Medium | `runtime.js:943` |
| U-2 | Client gap detection across the snapshot boundary | HELD-UNCHECKED | Medium | `runtime.js:750` |
| U-3 | H-4 headroom arithmetic has exactly zero margin | HELD-UNCHECKED | Medium | `session/limits.go:70` |
| U-4 | `unionReaches` agrees with `unionEdges` exactly | HELD-UNCHECKED | Medium | `actor.go:810,870` |
| U-5 | `Framer.Write` is only ever fed `Encode`'s output (B-9) | HELD-UNCHECKED | Medium | `outbound.go:201` |
| U-6 | Render input/output never aliases session state | HELD-UNCHECKED | Medium | `renderer.go:191` |
| U-7 | `Actor.Close`'s code survives to `finalCode()` | HELD-UNCHECKED | Low | `actor.go:261`, `actor.go:221` |
| U-8 | `Mark` runs before `emitPatch` (defer-argument evaluation) | HELD-UNCHECKED | Low | `actor.go:394` |
| U-9 | H-6 "at parse" | HELD (vacuously) — doc drift | Low | `invariants.go:85` |
| — | H-1, H-2, H-3, H-5, H-7, H-9, H-12, P1, P3, P8 | HELD-AND-CHECKED | — | — |

---

# Part 1 — BROKEN

## BR-1 (High) — every legitimate `ClientTelemetry` report is rejected as forged

**Invariant.** H-11: *"`ClientTelemetry.patch_id` names a patch actually sent to
this session … unknown → counted and dropped, never used to fabricate a span."*
Enforcement is `window.slotFor` (`window.go:101`), which searches only the
**still-unacknowledged** slots.

**The break.** The shipped client sends the ack *before* the telemetry for the
same patch:

```js
// client/runtime.js:767-770, applied()
send({ ack: { server_seq: seq } });
send({ client_telemetry: { patch_id: p.patch_id, morph_micros: …, apply_micros: … } });
```

The ack lands on `a.acks`; the telemetry lands on `a.mailbox`. `window.ack`
evicts the slot the telemetry is about to name:

```go
// internal/session/window.go:86-88
for len(w.slots) > 0 && w.slots[0].serverSeq <= seq {
    w.slots = w.slots[1:]
}
```

`Run`'s `select` has no ordering between the two channels, and in practice the
actor is idle when the ack arrives, so it drains the ack first essentially every
time. `slotFor` then misses, and the report is discarded as an attack.

**Repro (executed).** 40 events; after each patch, ack then telemetry, exactly as
the browser does:

```
telemetry reports dropped as unknown_patch: 40 of 40
```

```go
It("drops legitimate client telemetry as unknown_patch", func() {
    h := newHarness(app, session.DefaultLimits()); h.start()
    for i := 0; i < 40; i++ {
        h.sendEvent("counter.increment")
        Eventually(h.sink.patches).Should(HaveLen(i + 1))
        p := h.sink.patches()[i]
        h.sendAck(p.GetServerSeq())        // the browser's order,
        h.sendTelemetry(p.GetPatchId())    // from applied()
    }
    Expect(droppedUnknownPatch(h)).To(Equal(0)) // got 40
})
```

**Consequences.** `gotthlive.client.morph` (instrumentation §3.2, FR-36 clause 3)
is **never emitted for any patch**; `ClientTiming` is never recorded, so the
client-side half of the latency budget has no data at all;
`gotthlive_client_telemetry_dropped_total{reason="unknown_patch"}` is pegged at
100 %; and a `Warn` record accusing the client of forgery
(`resync.go:108`) is written once per patch — at the dashboard workload, 53
false accusations per second per session.

**Fix shape.** Swapping the two `send` calls in `applied()` is *not* sufficient —
the `select` is still unordered and would drop roughly half. Fix it on the server:
retain the `(patchID → serverSeq, span)` mapping for one window's worth of
sequences past `w.acked` — a second small ring, or simply do not trim `w.slots`
on ack until `len(w.slots) > w.cap+1`, and have `slotFor` search that history
while `ack` continues to move `w.acked`. That keeps H-11's rejection meaningful
(a forged `patch_id` still misses) while giving a real report somewhere to land.
Whatever the shape, the spec that pins it must send **ack then telemetry**,
because that is the order the shipped client uses.

---

## BR-2 (High) — `Origin.source` is built from unvalidated application input against a wire predicate

**Invariant.** Schema (`frame.proto:287`):
`Origin.source where len(this) > 0 && len(this) <= 64 && matches(this, "^[a-z][a-z0-9_.:/-]*$")`.
`ValidateOutbound` enforces it on the single write path (§5.3), and a failure is
"an internal error, not a client-visible one … the frame is dropped".

**The break.** The value is composed from two application-supplied strings that
nothing validates:

```go
// internal/session/ingress.go:132
Source: protocol.SourceEventPrefix + name,   // "event:" (6) + Event.name (<= 64)
// internal/session/effects.go:163, 207
Source: protocol.SourceEffectPrefix + source, // "effect:" (7) + EffectSource()
```

* `Event.name` is bounded at **64** by its own predicate (`frame.proto:115`), so
  any registered name of **59 characters or more** yields a `source` of 65+ and
  every patch that event causes is unsendable. The name passes the *inbound*
  boundary cleanly — this is well-formed client traffic against a well-formed
  registration.
* `EffectSource()` has **no bound and no charset check anywhere** — protocol.md
  §3.3 says so explicitly ("There is **no registration step**"). Any upper-case
  letter, space, or leading digit fails the RE2 predicate.
* `Config.Events` is not validated at all: `live/config.go` checks limits for
  negativity and range and never looks at the event names.

**Repro (executed).**

```
value "event:abbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
  does not satisfy "len(this) > 0 && len(this) <= 64 && matches(…)"
ERROR FRAME: code:INTERNAL  message:"the server could not encode an update"

value "effect:Chat.Broadcast"
  does not satisfy "len(this) > 0 && len(this) <= 64 && matches(…)"
```

```go
long := "a" + strings.Repeat("b", 58) // 59 chars, matches ^[a-z][a-z0-9_.:-]*$
app.events[long] = true
h.sendEvent(long)
Expect(h.sink.patches()).To(BeEmpty())            // passes — the patch is gone
Expect(h.sink.errors()[0].GetCode()).To(Equal(pb.ErrorCode_INTERNAL))
```

**Consequences.** The application is told nothing (the effect's `Execute`
returned `nil`); the state change never reaches the client; the client receives
a non-fatal `Error{INTERNAL, "the server could not encode an update"}` it cannot
act on; and `gotthlive_outbound_validation_failed_total` — documented as *"any
occurrence is actionable … the frame was built on this side, so it is never a
client problem"* — is incremented by ordinary application input. This is
verbatim the failure `live/app.go:236-243` describes and closed for
`Event.Contributing`; the same class was left open one field over. It also makes
the log line at `actor.go:728` ("this is not a client problem") send the operator
to the wrong repository, which is the exact outcome the D-18 comment above it
says it was written to avoid.

**Fix shape.** Validate at the boundary that owns the string, not at the frame:

1. `Config.validate()` rejects an `Events` entry that fails
   `len(name) <= 64 - len("event:")` **and** the `Origin.source` charset. A
   registration error is a startup error; a dropped patch is not.
2. `render.NewRegistry`-style validation for effects: check
   `len("effect:"+EffectSource()) <= 64` and the charset the first time an effect
   source is seen (`effects.go:72`), and turn a failure into the effect's own
   deterministic failure event, not into a dropped patch three layers later.
3. Add a spec asserting that no application-supplied string can reach
   `ValidateOutbound` and fail it — the property `OnInvalid` claims.

---

## BR-3 (High) — the render hash and the dirty bit are committed before the send succeeds

**Invariant (implied by `renderer.go:57-62` and by suppression being correct).**
`Renderer.hashes[i]` is *"the hash of each fragment's last emitted bytes"* — i.e.
the markup the client actually holds. Suppression (`renderer.go:202`) is only
sound if that is true.

**The break.** `render()` clears the dirty bit and writes the new hash *inside
the render pass*:

```go
// internal/render/renderer.go:174, 209
v.dirty.clear(i)
…
v.hashes[i] = sum
res.Updates = append(res.Updates, Update{…})
```

The send happens afterwards, in the caller, and can fail *survivably*:

```go
// internal/session/actor.go:537-540
n, span, ok := a.send(ctx, frame, causal)
if !ok {
    return          // hash already updated, dirty bit already cleared
}
```

`send`'s `*InvalidFrameError` branch (`actor.go:715-735`) deliberately keeps the
session alive. So the renderer now believes the client holds markup that was
never written to the socket, and **every subsequent render of that fragment that
produces the same bytes is suppressed**. The region is stale for the life of the
connection, with no metric saying so and no resync triggered.

**Repro (executed).** Chained onto BR-2, which is the reachable trigger today:

```go
h.sendEvent(long)                       // patch dropped by ValidateOutbound
// hashes["counter"] now == hash("<b>1</b>"), which the client never received
h.sendEvent("counter.relabel", field)   // real state change, same rendered bytes
Consistently(h.sink.patches).Should(HaveLen(0))   // passes: suppressed forever
```

**Fix shape.** Make the render pass's commit conditional on the emission. Either
(a) have `render()` return the new hashes and have the caller install them only
after `send` reports `ok`, or (b) on the `!ok` path re-dirty the fragments in
`res.Updates` and roll their hashes back. (a) is cleaner and makes the invariant
statable: *`hashes[i]` is only ever written by the code path that observed a
successful `Framer.Write`.* An assertion comment belongs at `renderer.go:209`
naming who is allowed to commit it.

---

## BR-4 (Med-High) — coalesced provenance is dropped on two exits from `emitPatch`

**Invariant.** H-4: the `contributing_event_ids` bound *"acts as a coalescing
flush trigger, **never a truncation**"*. P5: *"the union of those ids over a run
equals the set of events that produced a state change and were not individually
patched"* — *"set equality, not sampling"*. `deferPatch`'s own comment
(`actor.go:748`): *"Nothing is dropped on the way."*

**The break.** `takePending` clears the deferred state unconditionally:

```go
// internal/session/actor.go:849-861
func (a *Actor) takePending(origin protocol.Origin) (protocol.Origin, []uint64) {
    contributing := a.pendingIDs
    a.pendingIDs = nil
    if prev := a.pendingOrig; prev != nil { …; a.pendingOrig = nil }
    return origin, contributing
}
```

and `emitPatch` has **two exits after that call that never emit**:

* `actor.go:520-526` — `len(res.Updates) == 0`. Reachable whenever the flushing
  transition's render is fully suppressed: the fragments were marked dirty (a
  real state change) but rendered to the bytes already on the wire. The
  provenance row written there carries the *pre-union* origin, so the ids are
  absent from the log as well as from the wire.
* `actor.go:537-540` — `send` failed survivably (the BR-2/BR-3 path). Here
  `origin.Contributing = unionEdges(...)` has already run, so the whole union is
  built and then discarded with the local.

`emitSnapshot` has the same shape at `actor.go:582-585`.

**Repro (executed).** `AckWindow=4`; a fragment that renders only `N`; state
carries an unrendered `Label`.

```
contributing ids seen anywhere: map[]
[FAILED] event 2's contributing edge was dropped
```

```go
h.sendEvent("counter.increment")                                   // e1 -> patch, depth 2
h.sendEvent("counter.relabel", field("a"))                         // e2 -> deferred (coalesce)
h.sendEvent("counter.relabel", field("b"))                         // e3 -> flushes, renders identically
// union over every frame AND every provenance row:
Expect(seen[2]).To(BeTrue())   // fails
Expect(seen[3]).To(BeTrue())   // fails
```

Both e2 and e3 changed state (`state_version` rose for each) and neither was
individually patched, so P5 requires both in the union. Neither is anywhere.

**Fix shape.** `takePending` must not be a commit. Either return the pending set
without clearing and have the caller clear it only after `send` reports `ok`, or
give both non-emitting exits a `a.redefer(origin, contributing)` that folds the
union back into `pendingIDs`/`pendingOrig`. The second is a smaller diff and
keeps `unionReaches`'s accounting honest, because the set it counts next time is
the set that is still owed. A spec belongs beside the existing flush-trigger
spec in `backpressure_test.go`: *"a flush whose render is fully suppressed still
carries its deferred provenance on the next patch."*

---

## BR-5 (Medium) — a mount snapshot that fails validation leaves a zombie session

**Invariant.** H-10: *"`Snapshot` is the first frame on a connection."*
`mount`'s own contract (`actor.go:283`): a mount that cannot be established
emits `Error{INTERNAL, fatal}` and closes `4012`.

**The break.** `mount` handles `Init` failing but ignores `emitSnapshot`'s
result:

```go
// internal/session/actor.go:296-299
origin := protocol.Origin{Kind: pb.OriginKind_MOUNT, Source: protocol.SourceMount}
a.emitSnapshot(ctx, origin, protocol.Supersession{}, 0)   // returns int, discarded
a.runEffects(ctx, effects, 0)
```

and `Run` releases the read pump immediately afterwards
(`actor.go:185-186`, `close(a.ingressReady)`). If `send` fails with
`*InvalidFrameError` — reachable today via BR-2, or via any fragment whose markup
exceeds `FragmentUpdate.html`'s 1 MiB bound — then `a.serverSeq` stays 0, no
`Snapshot` is ever written, no close code is named, and the connection is
accepting frames.

**Repro (executed).** One fragment rendering 1,048,577 bytes:

```
frames: 1  snapshots: 0  errors: 1  closes: []
```

`Ready()` returns `nil`, so the read pump runs. The client, correctly, sends
nothing (`runtime.js:731`, `if (!seq) return`), so the session sits open through
heartbeats until `IdleTimeout` (30 min default) evicts it as `4011
session_evicted` — a close code that describes the wrong thing.

**Fix shape.** `emitSnapshot` already returns the byte count; make it return
`(int, bool)` or have `mount` check for zero and take the existing failure path:
`emitError(INTERNAL, …, fatal=true)` + `Close(CloseInternalError, "mount failed")`.
Add the assertion to the existing wsx spec *"sends a snapshot as the first frame
and nothing before it"* in its negative form: a mount whose snapshot cannot be
validated closes rather than serving.

---

## BR-6 (Medium) — the no-op resync short circuit bypasses H-14's budget entirely

**Invariant.** H-14: *"`ResyncRequest` obeys its **own** rate budget, independent
of the event bucket: minimum interval 1 s, burst 3. … A resync whose
`last_applied_seq` already equals the current `server_seq` is answered with an
`Ack`, not a `Snapshot`."* Two clauses; the code treats the second as taking
precedence over the first.

**The break.** `resync()` short-circuits before consulting the bucket:

```go
// internal/session/resync.go:52-59
if m.lastAppliedSeq >= a.serverSeq {
    a.m.ResyncRequest(ctx, resyncNoop, 0)
    a.fr.Send(ctx, protocol.NewAck(a.peer.ID, a.serverSeq))
    return
}
if !a.resyncBucket.allow(a.now()) { … }   // never reached on the no-op path
```

`ingressResync` (`ingress.go:200`) does not consult `a.eventBucket` either, so a
no-op resync is charged to **no bucket at all**. Each one still mints an
`eventSeq`, runs the application's `Authorize` hook on the read pump (arbitrary
application work, per frame), occupies a mailbox slot, and produces an outbound
`Ack` frame.

**Repro (executed).** 40 `ResyncRequest{last_applied_seq: 1}` after a mount that
left `server_seq = 1`:

```go
for i := 0; i < 40; i++ { h.sendResync(1) }
Eventually(ackFrames).Should(Equal(40))    // passes — all 40 answered
Expect(h.sink.errors()).To(BeEmpty())      // passes — nothing rate limited
Expect(h.closeRecords()).To(BeEmpty())     // passes — no sustained-abuse close
```

H-14's own test (*"50 ResyncRequest/s from one authenticated client must not
produce 50 full renders"*) passes vacuously: it asserts about renders, and this
path produces none. The invariant it was written for — that the resync *frame
kind* has a budget — is not held.

**Fix shape.** Move `a.resyncBucket.allow` above the no-op short circuit, or
charge the no-op to the ordinary event bucket in `ingressResync`. The former is
truer to H-14's first clause; either way, the amplification spec needs a second
arm that counts **frames answered**, not renders performed.

---

## BR-7 (Medium) — `sameState`'s comparable fast path makes P4 false for a pointer state

**Invariant.** P4: *"`state_version` is non-decreasing and increases **iff** state
changed."* `sameState`'s comment (`actor.go:1176-1183`) claims the implementation
errs in the safe direction: *"reporting no change that did happen would freeze
the version and make the provenance property false."*

**The break.** A pointer type *is* comparable, so the fast path compares
identity, not value:

```go
// internal/session/actor.go:1188-1192
t := reflect.TypeOf(prev)
if t != reflect.TypeOf(next) || !t.Comparable() { return false }
return prev == next
```

`Config[S]` places no constraint on `S`. For `S = *Foo`, a reducer that mutates
in place and returns the same pointer — the ordinary Go mistake, and the one the
purity rule exists to forbid — makes `prev == next` **true**, and the "unsafe
direction" the comment says was avoided is the one taken. The same aliasing also
reaches `a.view.Mark(prev, next)` (`actor.go:394`) with two identical pointers,
so an application's `Dirty` declaration compares a value against itself.

**Repro (executed).** `S = *ptrState`, `Dirty: prev.N != next.N`:

```
patches after two in-place transitions: 0
prov: transition=1 state_version=1 patch_id=1
prov: transition=2 state_version=1 patch_id=0
prov: transition=3 state_version=1 patch_id=0
```

Two real state changes; zero patches; `state_version` frozen at 1. With
`Dirty == nil` the patches do go out (every fragment is force-marked) but
`state_version` is still frozen, so P4 is false on the wire either way.

**Fix shape.** Two layers.
1. `sameState` should refuse the pointer fast path: if `reflect.TypeOf(prev).Kind()`
   is `Ptr`/`Map`/`Slice`/`Chan`/`Func`/`UnsafePointer`, return `false` (the
   documented-safe direction) rather than comparing identity. Cheap and total.
2. The purity boundary needs a detector, not only an import allowlist. The
   `livetest` determinism harness is the right home: replay an event log twice
   and assert `state_version` and the emitted patch bytes agree — an in-place
   reducer diverges immediately. Failing that, `live.Config[S].validate()` can
   refuse a pointer `S` at construction with a message naming the rule.

The import-allowlist test in `internal/arch` cannot see any of this: it is a
statement about *what a package imports*, and purity fails here through a
retained reference, not through an import.

---

## BR-8 (Medium) — `MaxSessions` is checked against a registry that is populated later

**Invariant.** `Options.MaxSessions`: *"bounds the whole process."*
`Handler.Close`: *"drains every live session."*

**The break.** `admit` reads `h.sessions`, which nothing has written yet:

```go
// internal/wsx/handler.go:265-273
if h.opts.MaxSessions > 0 && len(h.sessions) >= h.opts.MaxSessions { … }
h.perID[subject]++                      // per-identity IS reserved here
```

`h.sessions[c.peer.ID] = c` happens in `register` (`handler.go:287`), which runs
inside `serve`, on a different goroutine, after `mintID`, after
`websocket.Accept` (a network write), after `NewApp`, and after `session.New`.
Between `admit` and `register` there is a wide window in which every concurrent
upgrade sees the same stale `len(h.sessions)`, so N simultaneous upgrades all
pass a limit of 1. `MaxSessionsPerIdentity` is reserved correctly and is the only
one of the two that holds.

The same window breaks the drain: `Handler.Close` snapshots `h.sessions`
(`handler.go:305`), so a connection admitted but not yet registered is neither
closed with `4001 going_away` nor waited for, and `Close(ctx)` returns reporting
success. `admit`'s `draining` check narrows but does not close the window — a
connection that passed `admit` before `draining` was set still escapes.

**Repro.** By inspection; there is no spec for process-wide `MaxSessions` in
`internal/wsx/wsx_test.go` (only *"refuses a connection past the per-identity
session limit"*), which is why it has not been noticed.

**Fix shape.** Reserve the process slot in `admit` the way `perID` is reserved —
a `pending int` counter incremented under the same mutex and decremented by
`release`, with the limit checked against `len(h.sessions) + h.pending`. For the
drain, register the `*conn` before `Accept` returns or add pending connections to
a set `Close` also waits on. A spec: *"N concurrent upgrades against MaxSessions=1
admit exactly one"*, and *"a connection admitted during the drain window is still
closed with going_away"*.

---

## BR-9 (Low-Med) — a client can falsify P7's non-overlap by understating `last_applied_seq`

**Invariant.** P7: *"Additionally assert the ranges are contiguous and
non-overlapping per session."* H-13 constrains a snapshot's range internally
(`from <= through < server_seq`) but says nothing across snapshots.

**The break.** The range's lower bound is taken straight from untrusted input:

```go
// internal/session/resync.go:82-85
sup := protocol.Supersession{
    FromSeq:    m.lastAppliedSeq + 1,
    ThroughSeq: a.serverSeq,
}
```

`ResyncRequest.last_applied_seq` is never required to be non-decreasing, and is
never compared against `w.acked` — which the server holds and which is the
client's *own* previously stated high-water mark. H-8's `seen_server_seq` check
lives in `transition` (`actor.go:350`) and a resync routes to `resync()`, so it
never runs for this field; the only guard is the no-op short circuit, which
catches an *over*-stated value and lets every under-stated one through.

A client that always sends `last_applied_seq: 1` produces overlapping ranges
`[2, S₁]`, `[2, S₂]`, … and the conformance property fails through no server
fault. `validateSnapshot` (`invariants.go:130`) cannot catch it: it is a
cross-*frame* property and validation is per frame.

**Fix shape.** Clamp in `resync()`: `from := max(m.lastAppliedSeq, a.win.acked) + 1`,
and reject (or count) a `last_applied_seq` below `w.acked` as the H-7-shaped
contradiction it is — a client cannot simultaneously have acked *n* and claim to
have applied fewer than *n*. That makes the ranges monotone by construction and
turns P7's second assertion into something the server, not the auditor, keeps.
`window` needs an accessor for `acked`; it currently has none.

---

# Part 2 — HELD but UNCHECKED or UNSTATED

## U-1 (Medium) — H-13's second enforcement site does not exist

H-13 names its enforcement as *"`protocol.validateSnapshot`, on both the outbound
boundary (§5.3) **and the client decoder**."* The generated codec decodes fields
10 and 11 (`client/codec.gen.js:41`), but `runtime.js` never reads them:
`onMessage` (`runtime.js:943`) goes straight to `applied(f.snapshot)`.

The normative table therefore names an enforcement that ships in one of the two
places it claims. This is the same failure mode protocol.md §12 records for H-6 —
a table and an implementation disagreeing, with the table stating the stronger
claim. **Action:** either implement the check (it is four comparisons and ~60
bytes: both zero, or both non-zero with `from <= through < server_seq`, closing
`4002` on violation) or amend H-13 to name only the outbound boundary. Do not
leave it as written.

## U-2 (Medium) — the client never checks the snapshot boundary it is told about

The brief's off-by-one question has a clean answer: **the client does not look**.
Server-side the arithmetic is right — `from = lastApplied+1`, `through = serverSeq`,
snapshot at `serverSeq+1`, so `through < server_seq` and the next patch is
`server_seq+1`, which the client's `f.patch.server_seq !== seq + 1` test
(`runtime.js:964`) accepts. Verified by construction against `validateSnapshot`.

What is missing is the check the range exists for. `applied()` (`runtime.js:750`)
sets `seq = p.server_seq` for a snapshot with no relation asserted to what the
client held. Two things go unnoticed:

* `superseded_from_seq !== seq + 1` on a resync snapshot means the server
  superseded a range that does not begin where the client actually stopped —
  precisely the "which events produced the DOM I am looking at" question §4.3
  says the edge exists to answer. Free to check, never checked.
* `p.server_seq <= seq` would silently move the client's ack high-water mark
  **backwards**, which the server then closes as `4002` (H-7) — the client
  causing its own eviction rather than naming the server's error.

**Action:** one guard in `applied()`, before the assignment, asserting
`p.server_seq > seq` and (when the frame is a snapshot carrying a range)
`p.superseded_from_seq === seq + 1`; violation closes `4002`. This is the same
place U-1's check belongs, so they are one edit.

## U-3 (Medium) — H-4's headroom has exactly zero margin, and the derivation is off in its terms

I re-derived the bound. With `F = CoalesceFlushAt`, deferral requires the union
`< F`, and one more deferral adds at most the deferred origin's own event id
(1, excluded from its own union) plus the next origin's `Contributing`. The next
origin's `Contributing` for an effect emission is `scheduledEdge(1) +
ev.Contributing(≤64)` = **65**, because `live/app.go:238` bounds the
application's half *before* `effects.go:164` prepends the library's. So

```
widest union = F + 65 = 959 + 65 = 1024 = CoalesceFlushCeiling
```

and `checkListBounds` compares `list.Len() > bound`, so 1024 passes — by exactly
one element. The invariant holds.

But `session/limits.go:36-49` derives it as `F + 1 + B` with
`MaxCoalesceFlushAt = Ceiling - 1 - MaxEventContributing`, and its prose says
`B` is *"MaxEventContributing … plus the scheduledBy edge this library prepends —
bounded together"*, i.e. `B = 65` — under which the constant should be 958, not
959. The two readings land on the same number only because the prose's `+1` and
my derivation's `+1` are the same term counted in different places. **Nothing in
the repository drives the union to 1024**; `backpressure_test.go:266` asserts
`<= CoalesceFlushCeiling`, which is satisfied by every union including tiny ones.

**Action:** a spec that constructs the worst case — `CoalesceFlushAt =
MaxCoalesceFlushAt`, deferral to `F-1`, then an emission whose origin carries
`MaxEventContributing` app ids plus a `scheduledBy` — and asserts the emitted
list is exactly `CoalesceFlushCeiling` and that `ValidateOutbound` accepts it.
Then restate the comment in one set of terms. A one-element margin that no test
touches is a margin that will be spent by the next edit.

## U-4 (Medium) — `unionReaches` and `unionEdges` duplicate three rules with nothing asserting they agree

`actor.go:806-809` states the requirement precisely — *"The exact half must agree
with `unionEdges` exactly, so it applies the same three rules"* — and then
implements those rules twice, 60 lines apart (`actor.go:819-843` and
`actor.go:875-890`). I checked them by hand and they do agree today (same
seeding of `seen` with `origin.EventID`, same zero skip, same first-seen dedup,
same traversal order over `origin.Contributing` then `pendingIDs` then
`prev.EventID` then `prev.Contributing`).

There is no test that would notice if they stopped. **Action:** a property spec —
for randomized `(origin, pendingIDs, pendingOrig)`,
`a.unionReaches(o, n) == (len(unionEdges(o, take)) >= n)` for every `n` in range.
That is the assertion the comment is asking for and it costs twenty lines.

## U-5 (Medium) — `Framer.Write` is an exported bypass of `ValidateOutbound`

B-9 is genuinely held: every socket write in the module goes through
`Framer.Encode` (`outbound.go:178`), which calls `ValidateOutbound` and refuses
on failure, and the only callers are `actor.go:680/699` and the three
`Framer.Send` sites. Verified by grep over the whole module.

But `Framer.Write` (`outbound.go:201`) is exported, takes pre-encoded bytes, and
carries no coupling to `Encode` beyond the `Kind` it is handed. The split exists
for a good reason (the `gotthlive.send` span, `outbound.go:167-177`), but nothing
structurally prevents a future caller from marshalling a frame itself and calling
`Write`. **Action:** either make `Write` accept an opaque `encoded` type that only
`Encode` can produce — a one-field unexported struct is enough and costs nothing
at runtime — or add a spec that enumerates `Framer.Write` call sites the way the
close-code spec enumerates `Close(` call sites (protocol.md §8.3 already
establishes that pattern in this repo).

## U-6 (Medium) — render receives the renderer's shared buffer as its `io.Writer`

The `[]byte` aliasing hazard from the refinement research is real here, in the
render direction. `renderer.go:191-192`:

```go
v.buf.Reset()
if fail := callRender(f, fctx, state, &v.buf); fail != nil { … }
```

`v.buf` is per-session state reused for **every fragment of every pass**. An
application fragment that retains the `io.Writer`, or type-asserts it to
`*bytes.Buffer` and keeps `.Bytes()`, holds a live handle into storage that is
`Reset` before the next fragment renders. `html := v.buf.String()` and
`maphash.Bytes(…, v.buf.Bytes())` are both safe (`String()` copies), so the
library's own use is correct — the hole is the one it hands out.

The inbound direction is clean: `copyFields` (`ingress.go:261`) copies the slice
header, and proto3 `string` fields are already independent of the wire buffer, so
a reducer retaining `ev.Fields` is safe. (The comment's stated reason — pool
recycling — is not quite the mechanism; `putInbound` zeroes the struct, it does
not touch the backing array. The copy is right; the justification could be.)

**Action:** state the contract in `render.Fragment.Render`'s godoc — *the writer
is valid only for the duration of the call and its storage is reused* — and
consider handing fragments an `io.Writer` wrapper that panics after the call
returns, in dev mode only. A comment at `renderer.go:191` naming `v.buf` as
shared-across-fragments would already prevent the next reader from assuming
otherwise.

## U-7 (Low) — a close code named during shutdown is swallowed

`Actor.Close` uses `a.closing` as its once-flag (`actor.go:261`), and
`Actor.shutdown` sets the same flag as its first act (`actor.go:221`). So any
close the actor wants to name after teardown has begun returns without calling
`a.closer`, and `conn.finalCode()` (`conn.go:286`) falls back to
`CloseNormal` — reporting `normal` for a session that ended some other way. The
window is narrow (shutdown runs after `ctx.Done()`, by which point the transport
usually already has a code) but the coupling is accidental: `closing` means
"refuse new effect emissions" at `effects.go:139` and "the close has been named"
at `actor.go:261`, and those are two facts sharing one bit.

**Action:** give `Actor.Close` its own `sync.Once`, or a separate
`closeNamed atomic.Bool`, and leave `closing` to mean only what `emitter` reads
it for.

## U-8 (Low) — the `Mark`-at-defer-statement trick is load-bearing and undefended

`actor.go:394`:

```go
defer a.noteRenderFailures(ctx, a.view.Mark(prev, next), ev.ID, ev.ClientRef)
```

`a.view.Mark(prev, next)` is a *deferred call's argument*, so it evaluates
**immediately** — which is required, because `emitPatch` two lines later reads
the dirty set `Mark` just wrote. Only the reporting is deferred. The comment
above it says so, and it is correct.

It is also one refactor away from silent breakage: wrapping it as
`defer func() { a.noteRenderFailures(ctx, a.view.Mark(prev, next), …) }()` —
the mechanical "make this clearer" edit — moves `Mark` after the emission and
every patch renders against a stale dirty set. **Action:** the comment should say
*why the argument position is required*, not only that Mark runs now; and the
spec that would catch it (one transition, one dirty fragment, assert the patch
carries the fragment) should be labelled as pinning this ordering.

## U-9 (Low) — H-6's "at parse" is vacuous

H-6's enforcement column reads *"`protocol.validateOrigin` at construction **and
at parse**"*. `validateOrigin` is called only from `refineOriginAndUpdates`
(`outbound.go:88`), which is reached only from `ValidateOutbound`. `Origin`
appears only in `Patch` and `Snapshot`, both server→client, so there is no parse
path it could run on. The invariant holds; the second half of its enforcement
claim describes nothing. Worth a one-word correction in the same pass as U-1,
since both are H-table rows overstating where a check lives.

---

# Part 3 — HELD-AND-CHECKED (verified, no action)

| Invariant | How it holds | Where it is pinned |
|---|---|---|
| **H-1** enum domain | one `protoreflect` walk (`invariants.go:20`), run on **both** boundaries — `ParseInbound` step 6 (`inbound.go:117`) and `ValidateOutbound` (`outbound.go:32`) | `conformance_test.go` walks the descriptor |
| **H-2** version | `checkVersion` (`inbound.go:201`); equality is correct for v1 (no minor on the wire) and the comment says so | `wsx_test.go` "refuses an unsupported protocol version with a reason rather than as malformed" |
| **H-3** session id | `CheckSessionID` on the read pump before dispatch (`conn.go:213`) | `wsx_test.go` "refuses a frame naming another session" |
| **H-4** list bounds | table + descriptor walk on both boundaries; a repeated field with no entry is itself a violation (`limits.go:103`) | `conformance_test.go` "Every repeated field … has a declared cardinality bound" — see U-3 for the untested extreme |
| **H-5** frame size | `SetReadLimit` before the first read (`conn.go:120`) + re-check in `ParseInbound` (`inbound.go:84`) | `wsx_test.go` "refuses an oversize frame at the transport, before it is decoded" |
| **H-7** ack monotonicity | `window.ack` (`window.go:74`) refuses above `highest` and below `acked`, closes `4002` | verified by probe: `sendAck(maxSeq+500)` → `CloseProtocolViolation` |
| **H-8** `seen_server_seq` | checked on the actor goroutine, where `serverSeq` lives (`actor.go:350`) — correctly *not* at the ingress | `actor_test.go`; **note** it does not cover `ResyncRequest.last_applied_seq`, which is BR-9 |
| **H-9 / P3** contiguity | `send` advances `serverSeq`/`patchID` only after `Write` returns nil (`actor.go:709-712`); an invalid frame is dropped without leaving a hole | wire-capture property |
| **H-12** error identifiers | `validateError` on the outbound boundary; every `emitError` call site passes `(ev.ID, ev.ClientRef)` as a pair, and I checked all seven — the forced-emission sites derive both from the same origin, so they are zero or non-zero together | `outbound_test.go` |
| **P1** no orphan patches | `Origin.source`'s `len > 0` predicate makes it unconstructable — and see BR-2 for the cost of the *upper* bound | `ValidateOutbound` |
| **P8** single write path | every write goes through `Framer.Write`, which is the only `OnSent` incrementer; verified by grep over the module | see U-5 for the structural caveat |
| **client H-10** | `sendEvent` returns unless `seq` is set (`runtime.js:731`) and `send` returns unless `sid` is set (`runtime.js:924`); `newSession()` clears both per connection | `client/test/reconnect.test.mjs` |
| **client morph single entry** | `apply()` (`runtime.js:458`) is the only application-DOM mutator; `innerHTML` appears once, in `parse()`; `setStatus` is a declared carve-out on `<html>`. `morphElement` and `bind` are exported for the node suite — a test surface, not a second runtime path | `client/test/morph.test.mjs` |
| **backpressure ladder reachability** | `coalesceHeld` (`actor.go:500,507`) guarantees a deferral is followed by an emission, so depth keeps rising and `degrade` stays reachable — the "two stages wearing three stages' clothes" failure the comment names is genuinely avoided | `backpressure_test.go` "visits emit, then coalesce, then degrade, in that order" |
| **`unionReaches` short circuit** | `upper = len(pending) + len(origin.Contributing) [+ 1 + len(prev.Contributing)]` is a true upper bound on the union, so `upper < n ⇒ false` is sound and allocation-free | — |

---

# Part 4 — recommended order of work

1. **BR-1** — the client-timing subsystem currently produces no data and one
   false accusation per patch. Cheapest high-value fix.
2. **BR-2 + BR-3 together** — they compound: an application naming mistake
   becomes a permanently stale region. Fix BR-3 first (it makes every future
   send failure survivable-and-recoverable rather than survivable-and-silent),
   then BR-2 (which removes today's only reachable trigger).
3. **BR-4** — P5 is stated as set equality and is currently false.
4. **BR-7** step 1 (the `sameState` kind check) — three lines, closes a silent
   total-data-loss path.
5. **BR-5, BR-6, BR-8** — bounded blast radius, each a small diff.
6. **U-1 + U-2 + U-9** as one edit to the client and the H-table.
7. **BR-9, U-3, U-4, U-5** — the checks that keep the held invariants held.

None of the BROKEN findings requires a wire change. BR-9's fix and U-1's fix are
the only two that touch the client, and neither adds a field.
