# REV-DEL — the deletion sweep

| | |
|---|---|
| **Reviewer** | REV-DEL (principal engineer, deletion lens only) |
| **Date** | 2026-08-04 |
| **Swept** | `live/`, `internal/**`, `client/runtime.js`, `client/codec.gen.js` + its generator, `test/**`, `examples/**`, `bench/**`, `docs/**` |
| **Read first** | [api-surface.md](../api-surface.md) (FR-65) · [review-checklist.md](../review-checklist.md) §1.4, §1.7 · [rfc/001-architecture.md](../rfc/001-architecture.md) |
| **Method** | every claim below was verified by build, spec run or measurement in `dis-gotth-live:latest` / `dis-gotth-live-bench:latest`, on a scratch copy of the tree with the deletion actually applied. Nothing here is asserted from reading alone. |
| **Disposition** | **13 findings. ~95 lines of shipping Go and 3 lines of client source are provably dead today; 256 B per session of per-connection memory is dead weight; ~1,050 duplicated lines in two example modules are unlocked by one already-ledgered symbol.** |

This is a report. Nothing in it has been applied to the tree.

---

## 0. Summary table

Ranked by savings × confidence. **Now** = provably dead, deleting it is the whole
change. **After** = needs one small enabling edit named in the finding.

| # | Where | Delete | Saving | When | Confidence |
|---:|---|---|---|---|---|
| 1 | `examples/chat/wire_test.go`, `examples/dashboard/wire.go` + `wire_test.go` | the second hand-rolled `protowire` frame codec and the second `browser` harness | **~1,050 lines** across two modules; **165 lines measured byte-identical** | After — needs `livetest.Client` (already ledgered) | High |
| 2 | `internal/session/window.go:18–19` + `actor.go:548–549, 590–591` | `slot.bytes`, `slot.emittedNS` — written twice, read nowhere | **256 B per session** at the default `AckWindow`; 6 lines | After — one spec constant + one doc figure | **Certain** (measured) |
| 3 | `internal/obs/metrics.go` + `internal/session/actor.go` | `Metrics.FragmentLabels`, `fragmentAttr`, `RenderDuration`'s `fragment` parameter, `firstFragment()` | ~28 lines; one call + one slice index per render pass | After — delete instrumentation.md §125's row, or wire a `Config` field | High |
| 4 | `internal/obs/log.go:62–71` | `U64s`, `Strs` — zero callers in the module, prod or test | 10 lines | **Now** | **Certain** |
| 5 | `live/app.go:126–136` | hand-rolled `itoa`; `strconv.Itoa` is already used in the same package | 11 lines | **Now** | **Certain** |
| 6 | `internal/render/renderer.go:91–101` | `MarkAll`, `MarkID` — no production caller | 11 lines + 7 spec lines | Now (`MarkID`) / After (`MarkAll`) | **Certain** |
| 7 | `internal/wsx/handler.go` + `conn.go` | `Options.Now` — injectable clock nothing injects | 8 lines | **Now** | **Certain** |
| 8 | `docs/rfc/001-architecture.md` §14.2 | the package-layout tree, which names 4 fewer `internal/` packages than exist, and "the eight `livetest` symbols" | drift, not lines | **Now** | **Certain** |
| 9 | `internal/obs/trace.go:210–211` | `SpanRef.IsValid` — test-only | 2 lines | **Now** | **Certain** |
| 10 | `client/runtime.js:163–166` | `match()`'s `if (cur)` guard and its unreachable `return null` | 3 source lines, **−7 minified / −4 gzipped** (measured) | **Now** | **Certain** |
| 11 | `docs/adr/001-transport.md:154` | "ack + **replay window**" — the replay window was cut | drift | **Now** | **Certain** |
| 12 | `internal/clientcodec` generator → `client/codec.gen.js:141` | the `OriginKind` enum export, which nothing imports | 97 source bytes, **0 shipped bytes** | **Now** | **Certain** |
| 13 | — | three things I looked at and am **not** recommending | — | — | — |

**Totals.** ~95 lines of shipping Go, 3 lines of client source, ~1,050 lines of
example test code, 256 B/session, 4 gzipped client bytes, 3 stale doc claims.
The exported surface does not move: `tools/apisurface` reports `live 49/49`
identifiers and `50/50` fields across findings 2–7 and 9, unchanged.

---

## 1. How this was checked

`staticcheck -checks=U1000` and `-tests=false` over `./...` are **clean**, in
both directions. So nothing below is a linter's finding; every item is either a
symbol the linter counts as used *because a test uses it*, a struct field that
is written and never read, a branch a caller's guard already made unreachable,
or a claim in a document that the code stopped making true.

Findings 2–7 and 9 were applied together to a scratch copy and the module built
and the suites run. The result is the evidence, and it is worth stating exactly:

```
go build ./...            BUILD_OK
go test ./live/...        ok  live   ok  live/livetest
go test ./internal/...    internal/session:  80 passed, 1 FAILED
                          internal/render:   [build failed]
                          internal/obs:      [build failed]
                          everything else:   ok
tools/apisurface          live 49/49  50/50 — the surface matches the ledger
```

Three failures, and each one is a finding rather than a refutation:

- `internal/session/actor_test.go:246` — `TrackedBytes` measured **1544** where
  the spec expects **1800**. That is finding 2's saving, measured by the
  project's own spec.
- `internal/render/renderer_test.go:144` — `v.MarkAll undefined`. The only
  consumer of `MarkAll` is the spec file. That is finding 6.
- `internal/obs/obs_test.go:79` — `too many arguments in call to
  m.RenderDuration`. The only place a fragment label is ever passed is the spec
  file. That is finding 3.

Nothing else in the module noticed.

The client half (findings 10 and 12) was applied to the same scratch copy,
re-minified with `tools/minify`, and all five node suites run in the bench
image: **105 specs, 0 failures** (`bundle` 2, `codec` 34, `morph` 20,
`reconnect` 35, `resync` 14).

---

## 2. Findings

### 2.1 — `examples/chat` and `examples/dashboard` each carry their own protobuf decoder

**Where.** `examples/chat/wire_test.go` (1,309 lines) holds `eachField`,
`decodeFrame`, `decodePatch`, `decodeOrigin`, `decodeUpdate`, `decodeError`,
`encodeEventFrame` and a `browser` type with `pump`/`nextErr`/`next`/`await`/
`settle`/`received`/`send`. `examples/dashboard/wire.go` (428 lines) holds
`EachField`… — the same functions, capitalised — and
`examples/dashboard/wire_test.go` (1,408 lines) holds the same `browser`.

**Measured, not estimated.** Normalising the chat decoder's type names to the
dashboard's spelling and stripping comments and blank lines:

```
chat decoder      181 lines
dashboard decoder 214 lines
identical lines   165
```

The whole diff is: one `Bytes: len(b)` field, one `"chat:"` vs `"dashboard:"` in
an error string, two `case` arms dashboard added (`ack`, and `superseded_*`), and
dashboard's four extra encoders. The `browser` harness is a second ~250-line
near-copy in each file, and `get`/`scriptSrc`/`handshake` are a third,
~65 lines each, where the diff is 60 lines of 61 — i.e. reworded rather than
different.

**Why it is here.** Both example READMEs and both `FRICTION.md` files say so, and
so does [gates/checkpoint-2.md](../gates/checkpoint-2.md) §F-1, which L9-1
called *"the most expensive open item in the project"*. `livetest.Client`,
`.Send`, `.WaitFor` and `.Close` are ledgered in
[api-surface.md](../api-surface.md) §6 and are not implemented; `livetest`
measures 4 identifiers against a ceiling of 10. An example module cannot import
`internal/protocol/gotthlivepb` — that is the point of the ledger row — so each
example wrote the codec again.

**Worth recording, because it changes the argument.** §6 says these symbols are
experimental *"because their shape depends on what Phase 5's bench harness
actually needs"*. Phase 5's bench harness has landed, and it is
`bench/harness/*.mjs` — Node and CDP. It will never call a Go `livetest.Client`.
The consumer that was going to fix the shape cannot; the consumers that need it
are the three example suites that already exist and have already written the
shape twice. The blocker on implementing it is gone.

**Saving.** ~1,050 lines deleted from two example modules, and the third copy in
`test/internal/chaos/wire_test.go` (749 lines) — which is under `internal/` and
so uses `proto` directly rather than hand-rolling — stops being the odd one out.

**When.** DELETE-AFTER. The enabling change is implementing four ledgered
methods. This finding does not ask for that change; it prices what the absence
costs.

---

### 2.2 — `slot.bytes` and `slot.emittedNS` are written twice per patch and read nowhere

**Where.** `internal/session/window.go:18–19` declares them.
`internal/session/actor.go:548–549` (`emitPatch`) and `:590–591`
(`emitSnapshot`) write them. Nothing reads them. `slot` is unexported, so the
search is closed: `grep -rn` over the module finds the two declarations and the
four writes and nothing else. `Actor.telemetry` — the one function that calls
`window.slotFor` and receives a whole `slot` — reads `sent.span` and
`sent.serverSeq` and neither of these.

**Why it is safe.** No package outside `internal/session` can name the type, and
no function inside it reads the fields. `emittedNS` in particular is a second,
unread copy of a timestamp: the slow-client eviction clock is
`window.fullSince`, maintained by `noteFullness`, and that is what `onTick`
reads.

**The saving is a memory saving and the project already measures it.**
`Actor.TrackedBytes` reports `w.cap * 64` per window — *"thirty-two of
acknowledgement metadata and a thirty-two byte span reference"*. Delete the two
fields and a slot is `uint64 + uint64 + SpanRef` = **48 bytes**. At the default
`AckWindow` of 16 that is **256 bytes per session**, and the tracked figure the
spec asserts moves **1800 → 1544**, which is what the scratch run reported.
RFC §6's per-connection ledger carries the 64.

**One consequence to apply with it.** `Actor.send` returns the bytes written, and
after this deletion `emitPatch`'s `n` is unused (the compiler says so). It
becomes `_`. `emitSnapshot` still needs its `n` — it feeds
`gotthlive_resync_bytes` — so `send`'s signature does not change.

**When.** DELETE-AFTER: `actor_test.go:246`'s constant and `trackedBytes`'s own
doc comment carry the 64, and RFC §6's memory table should move with them.

---

### 2.3 — the fragment label on `gotthlive_render_duration_seconds` cannot be turned on

**Where.** `internal/obs/metrics.go:154–157` declares `FragmentLabels bool`,
:206 initialises `fragmentAttr`, and :394 branches on it. `internal/session/
actor.go:512` computes `firstFragment(res.Updates)` to feed it, and
`firstFragment` is defined at :1169.

**Why it is dead.** Nothing sets `FragmentLabels`. `live.Config` has no field for
it, `wsx.Options` does not carry one, `live.New` does not touch it, and — the
part that settles it — no test sets it either. The field appears exactly three
times in the whole repository: in its own doc comment, its own declaration, and
the `if` that reads it. So `m.FragmentLabels && fragment != ""` is `false` on
every render pass in every configuration that exists, and the `fragment`
argument is computed and discarded once per transition.

**The doc drift that goes with it.**
[instrumentation.md](../instrumentation.md):125 says
`gotthlive_render_duration_seconds` carries `fragment` as an *"opt-in label;
unlabelled by default"*. There is no opt-in. That row promises a knob an
operator cannot reach.

**Saving.** ~28 lines (`FragmentLabels`, `fragmentAttr` and its initialiser, the
branch, `firstFragment`, and the parameter at two call sites), plus one function
call and one slice index removed from the render path. `RenderDuration` loses a
parameter and becomes `(ctx, seconds)`.

**When.** DELETE-AFTER, and the decision is a fork rather than a nit: either
delete the field *and* instrumentation.md's row, or add the `Config` field that
makes the row true — which is an exported identifier and an api-surface change,
so it is a decision, not a cleanup. Either is better than the third state, which
is what is shipping.

---

### 2.4 — `obs.U64s` and `obs.Strs` have no caller anywhere

**Where.** `internal/obs/log.go:62–71`.

**Why it is safe.** Zero references in the module — production or test. Their doc
comments name their intended consumers precisely: *"for the contributing-event
union"* and *"for the fragment identifiers a transition patched"*. Both of those
are carried by `Logger.Provenance`, which builds `slog.Any("fragment_ids", …)`
and `slog.Any("contributing_event_ids", …)` inline at :228 and :231 against a
pre-sized `[]slog.Attr`. That is vestigial: the provenance record was a `Field`
list before it became a typed `Provenance` struct with `LogAttrs`, and these two
helpers are what the rewrite left behind.

**Saving.** 10 lines. This is the cleanest item in the report — it is the only
one whose deletion breaks nothing at all.

**When.** DELETE-NOW.

---

### 2.5 — `live` hand-rolls `itoa` in one file and uses `strconv.Itoa` in another

**Where.** `live/app.go:126–136` defines an eleven-line `itoa` whose single call
site is `live/app.go:115`, building `"Events[" + itoa(i) + "] is empty"`. It
allocates a fresh `[]byte` per digit and prepends, which is O(n²) in a function
that formats at most a three-digit index.

**Why it is safe.** `live/templ.go:382` — the same package — already calls
`strconv.Itoa`, so `strconv` is in `live`'s import graph and has been since
before this function existed. The deletion is one import line in `app.go` in
exchange for eleven lines of body.

**Saving.** 11 lines. Verified: builds, and `go test ./live/...` passes.

**When.** DELETE-NOW.

---

### 2.6 — `Renderer.MarkAll` and `Renderer.MarkID` have no production caller

**Where.** `internal/render/renderer.go:91–101`.

**Why it is safe.** The only references outside the declarations are in
`renderer_test.go`. Nothing in `internal/session` marks fragments by name or in
bulk: the actor reaches the dirty set through `Mark(prev, next)`, and a resync
re-renders through `RenderAll`, which ignores the dirty set entirely. These two
are the residue of a targeted-resync design — "re-render fragment X" — that the
protocol does not have: `ResyncRequest` carries `last_applied_seq` and a reason,
and no fragment identifier.

**`MarkID` is the stronger half.** Its one spec —
`renderer_test.go:256–262`, *"marks a fragment by name, and reports an
undeclared one"* — asserts nothing except that `MarkID` does what `MarkID`
does. A method with no caller and a spec that exists only to exercise it is
exactly checklist §1.4's one-call-site abstraction with the call site being the
test. Delete both: 8 lines of method, 7 of spec.

**`MarkAll` is used as scaffolding**, at `renderer_test.go:144` and `:282`, to
force a re-render so that suppression can be observed. Those two specs test real
behaviour and must survive. Every fragment in both has a nil `Dirty`, so
`v.Mark(state, state)` marks all of them and is the substitution.

**Saving.** 11 lines of production code, 7 of spec, 3 call sites rewritten.

**When.** `MarkID` DELETE-NOW. `MarkAll` DELETE-AFTER (the two rewrites).

---

### 2.7 — `wsx.Options.Now` is an injectable clock nothing injects

**Where.** `internal/wsx/handler.go:57–58` declares it, `:90–92` defaults it to
`time.Now`, and `conn.go:146` passes it into `session.Options`.

**Why it is safe.** No `wsx.Options` literal in the module sets `Now` — not
`live/app.go`, not `wsx_test.go`, not the conformance or chaos suites. It is
therefore `time.Now` on every path that exists, and the three lines are a
pass-through for a value that has one possible source.

**What this does not touch.** `session.Options.Now` and `session.Options.Ticks`
*are* injected, by `internal/session/harness_test.go`, which builds actors
directly. That is the seam the actor's testability actually rests on, and it
stays. What goes is the second, unused seam one layer up, which merely forwards.

**Saving.** 8 lines, plus the now-unused `"time"` import in `handler.go`.

**When.** DELETE-NOW.

---

### 2.8 — RFC §14.2's package layout has drifted from the tree it describes

**Where.** `docs/rfc/001-architecture.md:1250–1281`.

**The tree it prints omits four `internal/` packages that exist:**
`internal/obs`, `internal/obstest`, `internal/livebridge`, `internal/clientcodec`.
It also omits `test/` (four separate Go modules) and `tools/` (its own module,
holding `apisurface` and `minify` — the two programs CI's FR-65 and NFR-2 gates
run). `internal/refine` is described as a *"vendored 59-line runtime"*; it is 69.

**And one stale count in the prose beneath it.** :1284 says *"the surface is
unchanged by which of the two packages the **eight** `livetest` symbols live
in"*. The ledger says ten — `NewSession` and `NewTB` both landed since, and both
are recorded in api-surface.md §10's changelog. The paragraph that argues for the
second package's existence is quoting a number the document it points at
corrected twice.

**Why this matters more than tidiness.** §14.2 is the section L9-1's ruling A1
amended, and its whole authority is that it is the written layout kept equal to
the built one. Four missing packages and a stale count are the failure mode the
amendment exists to prevent.

**When.** DELETE-NOW — re-derive the tree from `ls internal/` rather than
patching it, and take the count from `tools/apisurface`'s output rather than
retyping it, which is §0's own rule about numbers a program reads.

---

### 2.9 — `obs.SpanRef.IsValid` is test-only

**Where.** `internal/obs/trace.go:210–211`.

**Why it is safe.** The two production sites that look like callers are not:
`trace.go:116` and `:137` call `sc.IsValid()` on the reconstructed
`trace.SpanContext`, which is the OpenTelemetry method, not this one. The only
callers of `SpanRef.IsValid` are three assertions in `obs_test.go`. Those
assertions are about `SpanContext()` — they can call it directly and lose
nothing.

**Saving.** 2 lines and one method off an internal type.

**When.** DELETE-NOW.

---

### 2.10 — `match()` in the client runtime double-guards its cursor

**Where.** `client/runtime.js:163–166`.

```js
  if (cur) {
    return cur.nodeType === nc.nodeType && (…) ? cur : null;
  }
  return null;
}
```

**Why it is dead.** `match` has exactly one caller — `morphChildren` at line 320
— and it is `m = cur ? match(cur, nc) : null;`. `cur` is non-null on every entry
by the caller's own guard, so the `if` is always taken and line 166's
`return null` is unreachable. One of the two guards is redundant; the caller's
is the one worth keeping, because it also skips the call.

**Measured, on the shipped artifact.** Applying this (together with 2.12) moved
`live/clientjs/gotth-live.min.js` from **10,178 → 10,171** minified and
**4,360 → 4,356** gzipped. The morph region's marginal gzip fell 1,067 → 1,065.
Small, and the point of NFR-2 is that the budget is made of decisions this size.

**Verified.** All 105 node specs pass, including all 20 morph specs.

**When.** DELETE-NOW.

---

### 2.11 — ADR-001 credits this design with a replay window it does not have

**Where.** `docs/adr/001-transport.md:154`, in the list of what SSE gives for
free: *"`Last-Event-ID` is a standardised, server-controlled resume cursor. Our
replacement (ack + **replay window**) is more capable but is ours to build and
ours to get wrong."*

**Why it is drift.** `internal/session/window.go:23–30` says the opposite, and
says it as a decision: *"It retains metadata, never frame bytes. Retaining bytes
for replay would cost the entire per-connection memory budget, and there would be
nothing to replay into: a session does not outlive its connection, so a reconnect
gets a fresh actor and a snapshot regardless."* The client agrees —
`runtime.js`'s `newSession()` clears every connection-scoped identifier and the
reconnect path takes a fresh Snapshot. The replacement for `Last-Event-ID` is
ack **plus resync-to-snapshot**, and it is *less* capable than the sentence
claims, deliberately.

**When.** DELETE-NOW — three words. The ADR's conclusion is unaffected; this is
one clause in the argument for the option that was not chosen, and it overstates
what the chosen option built.

---

### 2.12 — the generated codec exports an enum nothing imports

**Where.** `client/codec.gen.js:141` — `export var OriginKind = {…}` — emitted by
`internal/clientcodec/emit.go`, which walks `s.Enums` and emits every one.
`runtime.js`'s single import line names `decodeFrame`, `encodeFrame`,
`ErrorCode`, `PatchOp` and `ResyncReason`. `OriginKind` is imported by nothing in
`client/`, including the node suites.

**Be precise about the saving, because it is smaller than it looks.** The
bundler already tree-shakes it: removing it dropped the codec's *un-shaken*
minified figure by 78 bytes and the **shipped** figure by ~0. So this is 97 bytes
of generated source and no shipped bytes. It is worth doing as generator hygiene
— a generated export with no importer reads as an interface somebody may rely on
— and it is worth *not* overstating.

**Contrast with a real one, ruled on and correct.** api-surface.md §10 records
that importing `ErrorCode` costs **126 gzipped bytes**, because naming it stops
the enum table being shaken, where the bare literal `6` measures 4,231. I
re-derived that trade and I am **not** recommending it be reversed: the reason
recorded — one generated table is the single source of truth for a value the wire
fixes — is the right one, the alternative was measured before the choice, and 126
bytes against 7,928 of headroom is not where NFR-2 is won.

**When.** DELETE-NOW, in the generator rather than in the generated file.

---

### 2.13 — three things I looked at and am not recommending

Recorded so the next reviewer does not spend the same hours.

**`newSession()`'s `seq = 0` and `gap = false`** (`runtime.js:721, 723`). The
comment above them already says these two lines are dominated by other functions
and that no spec fails when they are deleted — it says so having tried it. The
argument for keeping them (four fields of one kind of state, and the dominations
are orderings between *other* functions rather than properties of this one) is
sound and the cost is ~15 minified bytes. Leave them.

**`morphEl`'s second `preserved(a)` check** (`runtime.js:291`). It is redundant
on the `morphNode → morphEl` path, because `morphNode:269` already returned. It
is *not* redundant on the `morphElement` entry path, which `apply()` takes with a
fragment root — and a region root carrying `data-gotth-preserve` is degenerate
but constructible. ~20 bytes to save a real branch. Leave it.

**`save()`/`restore()`** (`runtime.js:386–433`, 617 minified bytes). QA-1's D-21
already asked this question, found no test that failed when they were removed,
and the answer was a spec rather than a deletion — the tag-change replacement
path in `dom_preservation_test.go` is real and now asserted. Correctly closed;
do not reopen.

---

## 3. What I did not find

Stated because a deletion sweep that reports only hits is not a measurement.

- **No dead branch in the session actor's backpressure ladder.** All three
  stages are reachable, and `emitPatch`'s comment explains why coalescing defers
  one transition rather than latching — which is precisely what keeps `degrade`
  reachable. The `mustFlush` arm with the empty body is a deliberate fallthrough,
  not a stub.
- **No defensive code for actor-unreachable states.** The nil checks on `*Logger`,
  `*Metrics` and `*Tracer` are the disabled configuration, are documented as such,
  and are exercised. `onTick`'s `if now.IsZero()` is reached by the injected-tick
  harness.
- **No committed build output.** `bench/apps/*/next/.next/`,
  `bench/apps/*/public/bench/shim.js` and the example binaries are all present on
  disk and all correctly gitignored; `git ls-files bench` matches none of them.
- **No duplicated fixtures in `client/test/`.** `harness.mjs` and `dom.mjs` are
  each imported by two suites and each says at the top why it is not two copies.
- **No no-information duplicate specs.** The chaos and conformance suites overlap
  the DEV suites in subject and not in method — `test/internal/chaos/wire_test.go`
  drives real sockets with real loss where `internal/session/actor_test.go` drives
  a harness. That is the independent-adversarial-spec design working, and it is
  not duplication.
- **`staticcheck` is clean**, `U1000` and everything else, with and without tests.

---

## 4. If only three things are done

1. **Finding 2** — 256 bytes per session, certain, and the project's own spec
   already measures the delta. The smallest change with a number attached.
2. **Finding 3** — the largest block of shipping code that cannot execute, and
   the decision it forces (delete the field or export the knob) is one somebody
   has to make anyway.
3. **Finding 1** — the enabling symbol is already ledgered, its stated blocker
   has evaporated, and ~1,050 lines are waiting behind it.
