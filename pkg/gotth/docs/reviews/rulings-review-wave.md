# L9-1 rulings — the review wave

| | |
|---|---|
| **Author** | L9-1 (principal engineer) |
| **Date** | 2026-08-04 · **§8 added 2026-08-05** |
| **Ruling on** | [deletion.md](deletion.md) (REV-DEL) · [deduplication.md](deduplication.md) (REV-DUP) · [invariants.md](invariants.md) (REV-INV) · **§8: the four rulings owed at the checkpoint-3 gate** — ADR-001 X3 (C-14/C-35(a)), ADR-002, C-35(b), C-41 |
| **Governed by** | [api-surface.md](../api-surface.md) §0 (FR-65) · [review-checklist.md](../review-checklist.md) §1.4, §1.7 · [instrumentation.md](../instrumentation.md) §2.1 · PRD **NFR-10** |
| **My writes** | docs, `live/livetest/`, and nothing else. Two dev streams are implementing the non-ruling items concurrently; every finding I bless below is theirs to land, not mine |
| **Verification** | every measurement here was produced in `dis-gotth-live:latest` on this tree. Where I quote a peer review's number and it had moved, I re-derived it rather than carried it, and say which |

Nine rulings. **Two remove exported surface** (one symbol withdrawn, one field
refused), **one overrides a presumptive preference on measurement**, and the
rest bless, scope, or sequence work already in flight.

---

## 0. Summary

| # | Question | Ruling |
|---:|---|---|
| **1** | `livetest.NewTB` vs `ginkgo.GinkgoTB()` (REV-DUP D-2) | **REMOVE.** Applied: symbol and suite deleted, ledger moved 10 → 9, four documents corrected. Call-site migration **specified and verified**, not performed |
| **2** | The `fragment` metrics label (REV-DEL 3) | **DELETE the field and the doc row.** The `Config` opt-in is refused on FR-65 — and independently, the label would have misattributed |
| **3** | REV-DEL's now-deletion list, and its finding 8 | **BLESSED with four amendments.** RFC §14.2 corrected by me; §6.2's memory row **marked, not patched**, and assigned |
| **4** | U-5 — `Framer.Write` bypassing `ValidateOutbound` | **OVERRIDDEN.** "Unexport if no consumer" is unavailable: there is a consumer. Take the opaque-token form instead |
| **5.1** | `livetest.Client`'s stated blocker (REV-DUP D-3 / REV-DEL 1) | **UNBLOCKED.** Its named consumer had already evaporated; re-based in api-surface §6 |
| **5.2** | H-13's enforcement sites after `79403c6a` | **SPLIT BY CLAUSE.** Range: both ends. Kind: outbound only. Applied to protocol.md §6 |
| **5.3** | U-9 — H-6 "at parse" | **STRUCK.** Applied |
| **5.4** | Three cross-review collisions in one function each | **SEQUENCED.** BR-1 before finding 2; BR-3 before finding 6; finding 3 and BR-4 in `emitPatch` |
| **5.5** | BR-7's step 2 — refusing a pointer `S` at `New` | **REFUSED without a separate ruling.** Step 1 stands alone; step 2 is an A4-shaped decision |

Two items I was asked to consider and am **not** ruling on are recorded in §7,
so nobody re-opens them looking for a decision I declined to make.

**§8 is a second wave, added 2026-08-05**, and it is here rather than in a new
file because this is where this project records what I rule: the four rulings
owed at the checkpoint-3 gate, two of which were blocking it.

---

## 1. `livetest.NewTB` — REMOVE

REV-DUP is right, and it is right for a reason it understates. **Ruling: remove
the symbol.** Applied in this commit.

### 1.1 The premise is false, and NFR-10 is why the fallback is empty

`NewTB`'s justification, stated as fact in four documents — its own godoc,
`doc.go:11-16`, api-surface §6, and `docs/guide/testing-your-app.md` — is *"a
Ginkgo suite has no way to produce a `testing.TB`"*. True of `GinkgoT()`. False
of `GinkgoTB()`, which Ginkgo ships for exactly this and which this module has
required since it pinned `ginkgo/v2 v2.32.0` — before `NewTB` landed. Measured
in-container:

```
$ go doc github.com/onsi/ginkgo/v2 GinkgoTB
func GinkgoTB(optionalOffset ...int) *GinkgoTBWrapper
    GinkgoTB() implements a wrapper that exactly matches the testing.TB interface.
    ...  intended to be used as a drop-in replacement with third party libraries
    that accept testing.TB.
```

What survives the false premise is the *generality*: `NewTB` works for a
framework that is neither Ginkgo nor `testing`. Checklist **§1.4** wants ≥ 2 real
call sites for an abstraction; this one has **zero**, and **NFR-10 is the reason
it always would have**:

> **NFR-10 — Repo test conventions.** Go tests use Ginkgo v2 + Gomega for
> behaviour specs …

api-surface §6 cited NFR-10 as a requirement *for* the adapter. It is the
requirement that empties it. That is the finding I want on the record above
REV-DUP's own framing: this was not a symbol that became redundant, it was a
symbol whose only remaining justification was contradicted by the requirement
cited in support of it.

### 1.2 Three checks I ran against myself

**The dependency argument is neutral, not favourable.** dependencies.md §4
measures a Ginkgo import of `livetest` at **+17 modules / +3,484,016 B**. That
is the cost of `livetest` importing Ginkgo. `GinkgoTB()` is called from the
*consumer's own test file*, so `livetest` imports nothing either way. **Zero
difference**, and the §4 row is a standing rejection of an import neither answer
proposes. It stays; its closing clause naming `NewTB` as the alternative is
corrected.

**The panic property is obsoleted, not lost — and it was the weaker half
anyway.** `NewTB` embeds a nil `testing.TB` so an unimplemented method panics,
which its godoc calls *"the failure mode to want"*. `GinkgoTB()` implements
`Cleanup`, `Helper`, `Name`, `TempDir` and the rest for real, which is strictly
better than stopping. Sharper: `NewTB.Helper()` is **a no-op**, and the file
carries `failCallerSkip = 1` to hand-roll caller attribution;
`GinkgoTBWrapper.Helper()` calls `types.MarkAsHelper(1)`, the real mechanism.
The adapter reimplemented — worse — the one thing it existed to work around.

**"Exported symbols are permanent" cuts toward removing now, not toward
keeping.** api-surface §0 is my own law and BL-30 makes v0.1 a
no-compatibility-commitment release. This is the last moment the removal costs
16 mechanical edits rather than a deprecation cycle. A symbol kept because it is
already exported is how the rule that exports are permanent becomes the rule
that mistakes are permanent.

### 1.3 The repository had already decided, in both directions

| Idiom | Call sites | Modules |
|---|---:|---:|
| `livetest.NewTB(Fail, GinkgoWriter)` | 16 Go, + 4 fenced blocks in the guide | 7 |
| `GinkgoTB()` | 10 Go | 4 |

Four files spell it both ways — `examples/{chat,counter}/*_test.go`,
`bench/apps/{chat,counter}/gotth/*_test.go`. The fifth site is sharper than a
file: in `livetest`'s own package, `session_test.go` used `GinkgoTB()`
exclusively while `tb_test.go` next door existed only to prove the adapter.

REV-DUP's counts read *"23 / 10, five files"*; they count doc blocks and the
adapter's own suite as call sites. Re-derived, the number that matters for the
migration is **16 Go call sites in 7 modules**.

### 1.4 What I did, and the migration spec

**Deleted:** `live/livetest/tb.go` (119 lines), `live/livetest/tb_test.go` (193).
**Corrected:** `live/livetest/doc.go`, api-surface §0 / §0.1 / §6 / §10,
dependencies.md §2.1 and §4. `tools/apisurface` now reads `live/livetest 3/9`
and `0/6` against the ledger, and `live` is unmoved at `49/49`, `50/50`.

**Added, because a ruling that deletes a spec should leave one behind.**
`live/livetest/livetest_test.go` gains two specs: one drives `ReplayN` **and**
`AssertDirtyComplete` through `GinkgoTB()` with a compile-time
`var _ testing.TB = tb`, and one asserts `Cleanup`/`Helper`/`Name` do not panic —
the property the withdrawn adapter left to explode. **Mutated**: making that
spec's reducer impure turns it red *through* `GinkgoTB()`'s own failure path, so
it is not passing vacuously.

**Migration spec — owner: the `livetest.Client` wave, which is already opening
every one of these files.** One substitution, no import changes (all seven
modules dot-import Ginkgo, so `GinkgoTB` is already in scope):

```
livetest.NewTB(Fail, GinkgoWriter)   →   GinkgoTB()
```

| Where | Sites |
|---|---:|
| `examples/{counter,chat,dashboard}/*_test.go` | 2 each |
| `bench/apps/{counter,chat}/gotth/*_test.go` | 2 each |
| `bench/apps/dashboard/gotth/dashboard_test.go` | 3 |
| `docs/guide/_samples/apptest/app_test.go` | 3 |
| Comment references | `bench/apps/{chat,counter,dashboard}/gotth/*_suite_test.go` |

**Verified, not asserted.** I applied the substitution to a scratch tree with
the symbol already deleted: `go build ./...` and `go vet ./...` clean, and
`go test -race -count=1` green, in all seven modules **and** the root
(`examples/counter` 2.19 s, `chat` 10.33 s, `dashboard` 10.14 s, the three bench
apps 1.03/1.21/6.14 s, `live` + `live/livetest` 1.42/1.02 s). Then reverted the
call sites and kept the deletion.

**Two constraints on whoever lands it.**

1. **`docs/guide/testing-your-app.md` and `docs/guide/_samples/apptest/` migrate
   in the same commit.** `samples_test.go:155` asserts the guide's fenced blocks
   are byte-equal to the sample module, and it is not decorative — it caught the
   split during my verification, failing at exactly four blocks
   (`testing-your-app.md:51, 94, 139, 175`). The page also has prose to remove:
   the shipped-helpers table row at `:25`, the section heading at `:36`, and the
   signature at `:43`.
2. **Between now and that commit the tree is red in seven modules** at those 16
   lines, and nowhere else — every site is in a `_test.go`, so `go build` stays
   green everywhere. I am recording this as a knowingly-accepted two-commit
   window, not as an oversight: the substitution is mechanical and verified, and
   the files belong to another stream by ownership rather than by difficulty.

---

## 2. The `fragment` metrics label — DELETE the field and the row

REV-DEL 3 offers a fork: delete `obs.Metrics.FragmentLabels` and
instrumentation.md's row, or add the `Config` opt-in that makes the row true.
**Ruling: delete.** Applied on the doc side; the ~28 lines of Go are the server
stream's to land.

**The FR-65 half is settled precedent, not a new judgement.** A `Config` field
that no consumer has asked for is an export with no named call site, which
api-surface §7.1 already ruled a review rejection for `Config.OnPatch` — with
the same revisit condition, which I am re-registering here.

**The half that settles it is that the label would have been wrong.**
`gotthlive_render_duration_seconds` is recorded **once per render pass**:
`actor.go` times `a.renderPass(ctx, false)` — which renders every dirty
fragment — and passes `firstFragment(res.Updates)`, whichever fragment happens to
sit first in the update slice. Enabling the knob would have attributed a
whole-pass duration to one fragment's name. A knob nobody can reach is a
documentation defect; a knob that would misattribute if reached is a reason not
to build it. Per-fragment attribution needs the timing moved inside the
per-fragment loop *first* — and that is the pre-registered re-add trigger.

**On cardinality, which the brief asked me to weigh: it is not the objection,
and I am saying so explicitly so the next reader does not re-derive it.**
instrumentation §2.1's rule is *no causal ID is ever a metric label*. A fragment
ID is a **declaration** — it comes from `Config.Fragments`, is fixed before the
first connection, and §2.1's own bullet already bounded it by registration,
exactly as it bounds `event` and unlike `source`. Refusing this label on
cardinality grounds would have been reaching for the wrong rule and would have
left the misattribution undiagnosed.

**Applied:** instrumentation §2.3's row rewritten to state what the instrument
measures; §2.1's bullet narrowed to `event`, relocating `fragment` to the
`fragment.id` span attribute on `gotthlive.render.fragment`, which keeps full
per-fragment fidelity at zero time-series cost; §9 changelog entry; api-surface
§10 records the field **not** added, because the reviewable half of this item is
the refusal.

**For the server stream:** delete `FragmentLabels`, `fragmentAttr` and its
initialiser, the branch, `session.firstFragment`, and `RenderDuration`'s third
parameter — it becomes `(ctx, seconds)`. Surface unmoved at 49/49, 50/50.

---

## 3. REV-DEL's deletion list — BLESSED, with four amendments

### 3.1 The blessing

| Finding | Ruling |
|---|---|
| **4** `obs.U64s` / `obs.Strs` | **DELETE-NOW.** Blessed as written. Zero references, production or test |
| **5** `live/app.go`'s hand-rolled `itoa` | **DELETE-NOW.** Blessed. `strconv` is already in `live`'s graph via `templ.go` |
| **7** `wsx.Options.Now` | **DELETE-NOW.** Blessed, with the note that `session.Options.Now`/`Ticks` — the seam the actor's testability actually rests on — stay. Deleting a second, forwarding seam is not deleting the first |
| **9** `obs.SpanRef.IsValid` | **DELETE-NOW.** Blessed. The two production-looking callers are OTel's `SpanContext.IsValid`, not this |
| **10** `match()`'s inner cursor guard | **DELETE-NOW.** Blessed, and **already landed** in the client stream (`client/SIZE.md` §1.1.4 records it separately at −3/−3 rather than netted against the U-1/U-2 spend, which is the right way to publish two changes in opposite directions) |
| **11** ADR-001's "replay window" | **DELETE-NOW.** Blessed. Three words, and the ADR's conclusion is unaffected |
| **12** the generated `OriginKind` export | **DELETE-NOW in the generator**, blessed — and note this is now load-bearing beyond hygiene: ruling 5.2 turns on the client *not* importing that enum, so a generated export with no importer is the shape a future reader would take as permission |
| **6** `Renderer.MarkID` / `MarkAll` | **BLESSED with amendment** — see 3.3 |
| **1** the two example codecs | **DELETE-AFTER**, and the dependency is unblocked by ruling 5.1 |
| **2** `slot.bytes` / `slot.emittedNS` | **DELETE-AFTER**, and the figures are re-derived — see 3.2 |
| **8** RFC §14.2 | **Applied by me** — see 3.4 |
| **13** the three non-recommendations | **Closed.** `newSession()`'s two lines, `morphEl`'s second `preserved` check, and `save()`/`restore()` stay. The third was already closed as QA-1 D-21 and must not be re-opened a third time |

### 3.2 Amendment 1 — finding 2's numbers are stale, and its cell now has two claimants

REV-DEL measured *"256 B per session"* and the spec moving *"1800 → 1544"*. Both
were derived against `trackedBytes() = cap × 64`. **`37df5537` (REV-INV BR-1)
landed during this wave and made the ring retain `AckWindow + 1` slots**, so
today it is `retentionSlots() × 64` and `actor_test.go` already expects
`1088 + 512 + 256 + 8`.

**Ruling.** Finding 2 lands **after** BR-1, and every figure in it is re-derived
rather than carried:

- the per-session saving is `retentionSlots() × 16`, which is **272 B** at the
  default `AckWindow` of 16 — not 256;
- the window row is `17 × 48 = 816`, not `16 × 48 = 768`;
- the spec constant moves from BR-1's value, not from 1800.

BR-1 also *raises* the value of finding 2, which is worth stating because it
reverses the usual ordering intuition: the ring now holds `cap + 1` slots **in
steady state** rather than only under a provenance flush, so 16 dead bytes per
slot are now paid continuously.

**And the doc cell is stale in two directions at once.** RFC §6.2's window row
says `16 slots × 64 B = 1,024` and §7.1's paragraph enumerates the per-slot
fields *by name* — *"server_seq, patch_id, byte count, emit timestamp"* — which
is precisely the list finding 2 shortens. I have **marked both and patched
neither**: correcting 1,024 → 1,088 now, knowing 816 is coming, publishes a
third wrong number and re-derives the subtotal, the GOGC doubling, the total and
the headroom percentage twice, on a table that feeds a **gate**. The marker
names the arithmetic and the source of truth (`retentionSlots() × sizeof(slot)`);
the finding-2 commit owns the edit and takes the figure from the struct.

### 3.3 Amendment 2 — `MarkAll`'s substitution needs a mutation check

`MarkID` is DELETE-NOW: a method with no caller and a spec that asserts only
that it does what it does is checklist §1.4 with the test as the call site.
Blessed.

`MarkAll` is scaffolding at `renderer_test.go:144` and `:282`, forcing a
re-render so that *suppression* can be observed, and REV-DEL's substitution is
`v.Mark(state, state)` on the ground that every fragment there has a nil
`Dirty`. **Condition:** the substitution is only sound if those two specs still
fail when suppression breaks. Mutate the suppression check and show them red
before landing it — a scaffolding rewrite that quietly makes a spec vacuous is
worse than the eleven lines it saves, and "every fragment has a nil `Dirty`" is
a property of today's fixture, not of the spec.

**Sequencing:** finding 6 touches `internal/render/renderer.go`, and so does
REV-INV **BR-3**, whose fix changes `render()` to return the new hashes instead
of committing them. BR-3 first.

### 3.4 Amendment 3 — finding 8 applied, and REV-DEL's own count corrected

RFC §14.2 corrected by me under D-checkpoint convention. **Authorship noted in
the RFC's own §17 changelog**: the document is DEV-1's, the edit is mine, and
which is which is recorded there rather than inferred from `git blame`.

The tree is **re-derived**, not patched — four missing `internal/` packages
(`obs`, `obstest`, `livebridge`, `clientcodec`), `test/` and `tools/` added,
`internal/refine` corrected from *"59-line"* to 69. The stale *"the **eight**
`livetest` symbols"* is replaced by **no number**, per §0's own rule that a
number a program reads lives in one place; the same sentence in api-surface §0.1
had the same eight and is corrected the same way.

**One correction to the finding itself:** REV-DEL says `test/` holds *"four
separate Go modules"*. It holds **three** — `routers`, `sampling`, `memory`;
`test/internal/{conformance,chaos}` are in the root module. `find . -name go.mod`
prints twelve in total. Recorded because a deletion sweep's authority is that its
numbers were measured, and this one was not.

### 3.5 Amendment 4 — finding 3's Go half collides with BR-4

Deleting `RenderDuration`'s parameter and `firstFragment` edits
`actor.go`'s `emitPatch`, and so does REV-INV **BR-4**, whose fix stops
`takePending` committing on the two exits that never emit. Same function, same
twenty lines. Land them in either order but **not concurrently**, and whichever
is second re-runs the other's spec rather than assuming a clean merge.

---

## 4. U-5 — `Framer.Write`: the presumptive preference is OVERRIDDEN

DEV-REM-1 is implementing *"unexport if no consumer"*. **The condition fails on
measurement, so the outcome does not follow. Ruling: do not unexport.**

`Framer.Write` has exactly **one** caller outside `internal/protocol`, and it is
a real one:

```
internal/session/actor.go:699    n, err = a.fr.Write(sendCtx, kind, encoded)
internal/protocol/outbound.go:164   return f.Write(ctx, kind, b)   // Send's own composition
```

`internal/session` and `internal/protocol` are different packages, so `Write`
must stay exported for that line to compile. Unexporting it has two exits and
both are worse than the hazard:

- **collapse the actor back onto `Send`** — which re-creates the defect
  instrumentation §2.3 records fixing: `Send` validated, marshalled and wrote in
  one call, so `gotthlive_encode_duration_seconds` and
  `gotthlive_send_duration_seconds` were **equal by construction** and the
  write-stall signal could not detect a write stall. FR-36's `gotthlive.send`
  span goes with it;
- **move the actor into `internal/protocol`** — which trades a type-system
  hazard for a package-boundary one, in the module's most concurrency-sensitive
  file.

**Ratified instead: REV-INV's option (a), the opaque token.** Have `Encode`
return a one-field unexported-payload type that only it can construct, and have
`Write` take that instead of `(Kind, []byte)`. It preserves the split, keeps the
cross-package call, costs nothing at runtime, and makes the bypass
*unconstructable* rather than merely observed — which is the difference between
an invariant and a convention. `internal/protocol` is internal, so this moves no
ledgered surface and needs no api-surface row.

The enumeration spec REV-INV offers as its alternative is the fallback, not the
answer: it detects a second write path after somebody writes one, where the
token means nobody can. Take the spec **as well** if it is cheap — protocol.md
§8.3's close-code enumeration is the established pattern here — but not
*instead*.

I note for the record that P8 is held **today** by grep, and REV-INV verified it.
This ruling is about keeping it held, which is U-5's own framing and the right
one.

---

## 5. Further rulings

### 5.1 `livetest.Client` is unblocked — its stated blocker had already evaporated

REV-DEL 1 and REV-DUP D-3 converge on the same 2,400 lines of hand-rolled
protocol decoder across `examples/chat`, `examples/dashboard` and `test/routers`,
and both name the same cause: `Client`/`Audit`/`Report` are ledgered and
unimplemented.

api-surface §6 marked them experimental *"because their shape depends on what
Phase 5's bench harness actually needs"*. **Phase 5's bench harness has landed as
`bench/harness/*.mjs` — Node and CDP — and will never call a Go client.** The
consumer that was going to fix the shape cannot, and the row was waiting on an
event that had already happened and gone the other way.

**Ruling: re-based, and the row no longer blocks.** api-surface §6's
justification now names the real consumers — the FR-63 end-to-end example tests
and the instrumentation audit — which have written the shape twice already and
are therefore *better* evidence than the harness would have been. `livetest`'s
`doc.go` says the same. No new exported symbol: `Client`, `Send`, `WaitFor`,
`Close`, `Audit` and `Report` are all ledgered, so **this needs no further
ruling from me** — DEV-1 implements against §6.

Read `examples/chat/FRICTION.md:60` first; it sketches `NewClient(tb, h, o)` and
is the consumer report the shape was waiting on. Note the first parameter is a
`testing.TB` — under ruling 1 that is `GinkgoTB()` at every call site in this
repository, and the new symbol must not acquire an adapter parameter.

### 5.2 H-13's enforcement, split by clause

`79403c6a` landed REV-INV U-1/U-2: the client now reads `superseded_from_seq`
and `superseded_through_seq` — decoded since they were added, read by nothing —
and enforces the **range** clauses in `applied()`, pinning `from === seq + 1`
against the sequence it actually holds and closing `4002` with a reason that
names what disagreed.

It deliberately does **not** enforce the `Origin.kind == RESYNC` iff-range
clause. **Ruling: correct, and the H-table is scoped to match rather than the
client being asked to widen.** Two grounds, and the second is the one I am
ruling on:

- **cost**: comparing one `OriginKind` member client-side requires importing the
  generated enum, which is one object, so it ships all six members — measured at
  **126 gzipped bytes** for the identically-shaped `ErrorCode` import
  (`client/SIZE.md` §1.1.3), against a whole landing of 72 (§1.1.4);
- **who can act**: the range clause constrains **what the client does next** and
  the client is the only side that knows where its own DOM stopped. The kind
  clause only *labels* the frame, and a mislabelled frame is already refused by
  the outbound boundary before it is written. A second check that cannot change
  any behaviour is not defence in depth, it is 126 bytes of restatement.

Applied to protocol.md §6: H-13 is split into clause (a) the range — outbound
**and** client — and clause (b) the kind — outbound only — with a note carrying
the measurement, and a §12 changelog entry. The table now matches the code in
both directions, which is what it lost once already at H-6.

### 5.3 U-9 — H-6's "at parse" struck

`validateOrigin` is reachable only from `refineOriginAndUpdates`, itself
reachable only from `ValidateOutbound`; `Origin` occurs only in `Patch` and
`Snapshot`, both server→client. There is no parse path it could run on. The
invariant holds and always did; the enforcement column named a site that does not
exist. **Struck**, applied, and changelogged in the same edit as 5.2 — both are
H-table rows overstating where a check lives, which is one class and deserves one
correction.

### 5.4 The cross-review collisions, sequenced

Three functions have two claimants each. Stated once, here, because each review
saw only its own half:

| Site | Claimants | Order |
|---|---|---|
| `internal/session/window.go` + `TrackedBytes` + RFC §6.2/§7.1 | BR-1 (landed) → REV-DEL 2 | finding 2 second, figures re-derived — §3.2 |
| `internal/render/renderer.go` | BR-3 (hash commit) + REV-DEL 6 (`MarkAll`) | BR-3 first — §3.3 |
| `internal/session/actor.go` `emitPatch` | BR-4 (`takePending`) + REV-DEL 3 (`RenderDuration` arg) | either order, not concurrent — §3.5 |

### 5.5 BR-7 — step 1 yes, step 2 not without its own ruling

`sameState`'s comparable fast path compares pointer **identity** for `S = *Foo`,
so an in-place reducer freezes `state_version` and makes P4 false on the wire.
REV-INV's step 1 — have `sameState` refuse the fast path for
`Ptr`/`Map`/`Slice`/`Chan`/`Func`/`UnsafePointer` and return "changed" — is three
lines in the documented-safe direction. **Blessed, land it.**

**Step 2's second option is refused as written.** *"`live.Config[S].validate()`
can refuse a pointer `S` at construction"* would change what the exported generic
parameter accepts, breaking every application whose state is a pointer, and it
sits directly on api-surface **§9 A4** (whether `Config[S]`'s generic parameter
is right at all), which is open. That is a surface decision with a blast radius,
not a bug fix riding along with one. **If it is wanted, it comes back as its own
ruling with the api-surface row attached.** Step 2's *first* option — the
determinism detector in `livetest`, replaying a log twice and comparing
`state_version` and emitted patch bytes — has no such problem and is the one to
pursue; it is also FR-15's own shape.

---

## 6. What I changed

| File | Change |
|---|---|
| `live/livetest/tb.go`, `tb_test.go` | **deleted** (312 lines) — ruling 1 |
| `live/livetest/doc.go` | false premise removed; `Client`/`Audit` status re-based — rulings 1, 5.1 |
| `live/livetest/livetest_test.go` | two specs pinning `GinkgoTB()` through the helpers; mutation-checked — ruling 1 |
| `docs/api-surface.md` | §0 ceiling 10 → 9; §0.1's stale eight removed; §6 row deleted and the handler argument replaced by "deliberately absent"; §6's `Client` justification re-based; two §10 changelog entries — rulings 1, 2, 5.1 |
| `docs/instrumentation.md` | §2.1 bullet narrowed to `event`; §2.3's render-duration row rewritten; §9 changelog entry — ruling 2 |
| `docs/rfc/001-architecture.md` | §14.2 tree re-derived, count removed, note added; §6.2 and §7.1 marked and assigned; §17 changelog with authorship note — ruling 3 |
| `docs/protocol.md` | H-13 split by clause, H-6's "at parse" struck, two notes, §12 changelog — rulings 5.2, 5.3 |
| `docs/dependencies.md` | §2.1 paragraph and §4's row: the measurement and the rejection stand, the alternative they name is corrected — ruling 1 |
| `docs/reviews/rulings-review-wave.md` | this document |

**Verified in `dis-gotth-live:latest`:** `gofmt -l live/livetest/` clean,
`go vet ./live/...` clean, `go test -race -count=1 ./live/...` green
(`live` 1.42 s, `live/livetest` 1.02 s, 16 specs), `tools/apisurface` reports
`live 49/49 50/50` and `live/livetest 3/9 0/6` — *the surface matches the
ledger*. The migration verification is in §1.4.

---

## 7. Asked to consider, not ruled on

- **REV-DUP D-5/D-8/D-9 (the build scripts), D-6/D-7 (bench), D-10 (`":PORT"`).**
  No exported surface, no contradicted document — the build and bench owners
  decide, and their reviews already say what to do. **One exception worth
  saying out loud:** D-8 is a **latent infinite loop** (`gen.sh:320` dropped the
  `/` sentinel; `dirname /` returns `/` forever) and it is one line. Fix it
  **independently of** D-5's extraction, which is specified and may sit for a
  while. A bug queued behind a refactor is a bug that ships.
- **REV-DUP §9's `examples/dashboard` flake** (`wire_test.go:1316`, 1 in 8 then
  clean). Not diagnosed here and not mine, but it is **not in
  `docs/qa/ci-intermittents.md`** and that is the actual defect — an unrecorded
  intermittent is one the next person diagnoses from scratch. File it before
  fixing it.
- **REV-DUP's nine DELIBERATE rulings (R-1…R-9)** are correct and I am not
  re-opening any. R-8 in particular — every doc sample re-scaffolding its own
  server — was judged per-lead as the brief asked, and the answer *"repetition
  here is the product"* is right.
- **REV-INV's remaining BROKEN findings (BR-5, BR-6, BR-8, BR-9) and U-1…U-8.**
  Each is a defect with a reproduction, none changes exported surface or
  contradicts an approved document, and the server stream has REV-INV's own
  ordering. They need implementing, not ruling on.

---

## 8. The checkpoint-3 gate rulings — 2026-08-05

| | |
|---|---|
| **Ruling on** | [checkpoint-3.md](checkpoint-3.md) conditions **C-14/C-35(a)**, **C-35(b)**, **C-41**, and [ADR-002](../adr/002-observability-memory-budget.md), which is PROPOSED and requires my approval |
| **Requested by** | [PM-1's closure ledger](../pm/checkpoint-3-closure.md) §8 item 5 (blocking) and §2 rows C-35(a), C-35(b), C-41 |
| **Evidence base** | [g2-baseline](../bench/g2-baseline.md) **§9.10 and its subsections — the settled campaign**, plus its published raw data; and the tree at HEAD |
| **My writes** | `docs/adr/001-transport.md`, `docs/adr/002-observability-memory-budget.md`, `docs/rfc/001-architecture.md`, this file. **No code.** QA-2 was writing `docs/qa/` and `test/internal/chaos/` in the same worktree and I touched neither |
| **Verification** | **No suite was run and no measurement was taken.** Every number below is either read out of the tree at HEAD (a code constant, a struct, a `grep`) or **recomputed by me from published raw data** — `(mn − m0)/N` over `docs/bench/data/g2-baseline/remeasure-2026-08-05/*/step*/introspect-{m0,mn}.json`, N = 1000, which reproduces DEV-1's published medians exactly. Where a number is an estimate I say so and name who owes the measurement |

### 8.0 Summary

| # | Question | Ruling |
|---:|---|---|
| **8.1** | ADR-001 §7.1's proposed **X3 = 13,759 B** (C-14 / C-35(a)) | **ADOPT.** Write buffer in as the fifth line; §6.2.2 pointed at, not edited — **upheld with one added sentence**; X3's quantity clarified to *retained* bytes; **C-45**, **C-46** filed. My own C-35 arithmetic of 11,204 B is refused, and it was mine to get wrong |
| **8.2** | **ADR-002** — a memory budget for default-on observability | **APPROVED WITH CONDITIONS.** §3.3 (inside the gate) granted unamended; §3.2 granted plus run counts; **§3.1's "derived, never measured" clause refused** on the campaign's own secondaries. Budget **4,050 B/session** in RFC §6.2.6 **this landing**; retained-state row is a follow-up. **C-47**, **C-48** filed |
| **8.3** | **C-35(b)** — RFC §3.4's *"there is no bare `go func()` in the library"* | **CORRECTED FROM THE TREE.** Five sites, tabulated with owner and waiter. The second clause is now true and the mechanism is named. Source half → **C-49**, DEV-1 |
| **8.4** | **C-41 / D-31** — the client's `Error{RATE_LIMITED}` retry schedule | **DISCHARGED.** RFC §7.6.1 written from `client/runtime.js`; §8.4 gains the sentence that stops this RFC contradicting the client |

**Two of the four were blocking. Neither blocks any longer.** What still gates
checkpoint 3 is not mine: PM-1's §8 item 3 — **QA-2 has no sign-off at HEAD**.

---

### 8.1 X3 — ADOPT 13,759 B/connection

**The full ruling is [ADR-001 §7.2](../adr/001-transport.md), because that is
where a reader looking up X3 will be.** What follows is why, and the part I had
to check hardest.

#### 8.1.1 The arithmetic I adopted, and the one I refused

```
512  read buffer     internal/wsx/hijack.go:63   code constant, read at HEAD
1,024  write buffer    internal/wsx/hijack.go:64   code constant, read at HEAD
2,370  conn struct     MEASURED at ce52d2f9        per-component heap profile
8,192  read-pump stack ESTIMATE                    bounded above — 8.1.3
  410  runtime g       MEASURED at ce52d2f9        runtime.malg, 820 for two
─────
12,508  × 1.1 (§6.1.2's ratchet)  ⇒  X3 = 13,759 B
```

**Refused: my own 11,204 B.** C-35 restated X3's four named lines and the
transport pays five — it has always retained net/http's `bufio.Writer` as well as
its reader. Setting the ceiling below a line the transport actually pays is the
"quietly false ceiling" ADR §7 names, and I would have committed it while
enforcing the rule against it. §7.1 declined it and argued the decline in place,
which is the behaviour the condition was for.

**Refused: waiting.** The tree was carrying a ceiling marked stale, which is the
state C-14 exists to prevent, and §8.1.3 supplies what a deferral was waiting for.

#### 8.1.2 Ruling against the settled campaign, which is what PM-1 asked for

§8 item 5 required me to rule against the settled campaign rather than
`ae61f325`'s snapshot. Done, and the answer has a shape worth stating:

- **The two measured lines did not move, because the campaign did not measure
  them.** §9.10.11.3 records that RFC §6.3's per-component heap profile was **not
  re-run at `d66e4953`**. So 2,370 B and 410 B are quoted at the tree and date
  that produced them (`ce52d2f9`), and "they survived the re-measurement" would
  be a false sentence. There is no re-measurement.
- **One supporting figure did move.** §7.1 argues from a 12,780 B combined
  goroutine-stack class; the settled campaign measures 12,943 B (obs on, 5 runs)
  and 13,681 B (obs off, 2 runs). The argument survives and the number is
  restated.
- **O2 is closed, favourably.** Exactly **2.0** goroutines per session in all
  seven runs of both cells at `d66e4953`. `coder/websocket` starts no third
  per-connection goroutine, so C-14(1)'s named live risk is retired.

#### 8.1.3 The bound that made adoption safe rather than optimistic

Adopting a ceiling whose largest term is unmeasured needs exactly one property:
**the term cannot exceed the estimate.** It is derivable from the settled
campaign, so no new run was needed.

| Cell at `d66e4953` | per-session goroutine stacks, by run | median |
|---|---|---:|
| obs on, 5 runs | 12,845.1 · 12,877.8 · 12,943.4 · 13,008.9 · 13,041.7 | 12,943.4 |
| obs off, 2 runs | 13,336.6 · 14,024.7 | 13,680.7 |

Two goroutines per session, stacks in powers of two from 2,048 B. The largest
reading anywhere is 14,024.7 B; add back the *entire* `M(0)` stack reserve as if
none of it were replenished (655.4 B/session, the most the subtraction could
hide) and the two stacks are **≤ 14,680 B together**. Two at 8,192 would be
16,384. **So at most one of the two is at 8,192 B, none is at 16,384 B, and 8,192
is a true upper bound on the read pump whichever goroutine is the deep one.**

X3 at 13,759 B therefore cannot be *false* on that line — only loose, and C-14(2)
collects looseness later. If the probe finds the read pump at 4,096 B the
composition is 8,412 B and X3 ratchets to 9,253 B.

#### 8.1.4 Two things I found while checking, and both are conditions

- **C-45 — the read-pump stack. DEV-1.** `memsrv -probe` at the shipping tree.
  **The falsifier is written against what the instrument can actually do**: the
  probe reports observed relocations and a used-bytes *lower bound*, not
  allocated sizes. If the lower bound exceeds 4,096 B the line is 8,192 B; if it
  does not, the probe cannot settle it and a second instrument is owed rather
  than an inference.
- **C-46 — a transport line still in neither table. DEV-1.** The per-connection
  `context.WithCancel` (`internal/wsx/conn.go:78`). `ce52d2f9`'s profile carries
  ≈1,200 B for two of them and does not split the transport's from the actor's.
  Rather than assume a half I charged the transport **both**: 12,508 + 1,200 =
  **13,708 ≤ 13,759**. The adopted ceiling survives the strongest form of its own
  objection **by 51 B**, and that thinness is the finding — the next unbudgeted
  per-connection line forces a re-derivation rather than a quiet exceedance.

#### 8.1.5 The clarification that makes the tighter ceiling mean something

X3's five lines are **retained** bytes; equivalence-spec §3.6's headline is
unforced steady state, which under `GOGC=100` carries up to one further copy of
X3's 3,906 B of heap lines. A no-op-session harness measured §3.6's headline way
would read up to ≈**16,414 B** for a transport paying exactly its budget — 2,655 B
over a 13,759 B ceiling with nothing wrong. So X3's method column now names the
quantity and the instrument (§3.6's secondaries, or §6.3's profile).

**This is not a method change and §6.1.2 is not engaged**: it does not touch the
46,080 B gate, no X3 measurement exists to be fitted to, §3.6 is not amended, and
the change makes X3 *harder* to satisfy. It is advisory A-7's second half,
arriving three months late.

---

### 8.2 ADR-002 — APPROVED WITH CONDITIONS

**The full ruling is [ADR-002 §8](../adr/002-observability-memory-budget.md);
the budget lands in [RFC §6.2.6](../rfc/001-architecture.md).**

**The hole is real and I am not closing it by rejecting the document that names
it.** A per-session term went from 25,424 B to 3,682 B across two landings, in a
configuration equivalence-spec §5.6 freezes inside the headline, and no line in
any document changed colour in either direction. Both discoveries cost an hour of
a shared host. That is the argument, and it survives the term getting smaller —
§4 is right that a smaller number is not a reason to stop counting it.

**§3.3 is the half that decides the document and it is granted unamended.** The
budget is a sub-line *inside* the 46,080 B gate. Carving 3,682 B out of a 311 B
margin would turn an unresolved result into a pass by definition; §3.3 refuses
that in advance and at its own cost.

#### 8.2.1 Where I overrode the author, and on what

§3.1 asks for the line to be **derived and never set from the measurement**. I
refused that clause. The reasons are in ADR-002 §8.2; the load-bearing one is a
recomputation from the campaign's own published readings:

| per session, `d66e4953` | obs ON (5 runs) | obs OFF (2 runs) | difference | ranges separate? |
|---|---:|---:|---:|---|
| headline | 45,768.7 | 42,086.4 | **+3,682** | **yes** — band +1,765 … +6,124 |
| live heap | 12,189.7 | 11,846.5 | +343 | yes — band +174 … +542 |
| goroutine stacks | 12,943.4 | 13,680.7 | **−737** | yes, and **negative** |
| total Go-runtime mapped | 44,920.8 | 40,601.6 | +4,319 | **no** — overlapping, not resolvable |

**The term is not retained state.** Retained per-session bytes attributable to
the hooks are at most a few hundred and ambiguous in sign — and §9.10.1's caveat
makes even +343 B an upper bound, since that secondary carries the process's
fixed live heap over N and the instrumented process's includes the OTel SDK's. A
§6.2 composition row budgets retained bytes and is then doubled by the `GOGC`
line; setting it to 3,682 B would budget churn as though it were retained state
and then double it.

**And one of §3.1's own enumerated components is already budgeted.** Every window
slot holds a 32 B `obs.SpanRef` **whether or not a `Tracer` is configured**
(`internal/session/window.go:15-19`), and §6.2.2's window row already carries it.
Following the recipe would have counted it twice — in a line whose measured
counterpart is the difference between the two configurations, where it cannot
appear at all.

#### 8.2.2 The budget, and its conditions

**4,050 B/session** — measured 3,682 B plus §6.1.2's 10 %, the same ratchet rule
I applied to X3 an hour earlier, and it lands in RFC §6.2 **in this landing**
rather than in a follow-up, because "no line anywhere went red" is the thing this
ADR is about and a follow-up would extend it.

- **C-47 — DEV-1.** The obs-off cell at **five** runs. The value is provisional
  until it lands and is labelled so wherever it is quoted; it ratchets under
  §6.1.2 in the same PR.
- **C-48 — DEV-1.** The retained-state composition row in §6.2, derived,
  excluding `obs.SpanRef` and anything else paid with the hooks nil, with its
  measured counterpart quoted as the **live-heap** difference and never as
  `headline − observability_off`.

**What this approval does not license**, stated because approvals get quoted:
it is **not** "G2 met" (§3.6's driver-validation gate has been run by none of the
four campaigns), it is **not** a carve-out, and it is **not** a step toward
moving the 46,080 B target.

---

### 8.3 C-35(b) — RFC §3.4, corrected from the tree

*"There is no bare `go func()` in the library"* was false when I filed it and is
still false. `grep -rn 'go func' live/ internal/` over non-test files returns
**five** sites, and I read each one rather than counting them: `spawn` itself,
the session goroutine, the actor pump, `Close`'s fan-out, and `waitFor`'s
deadline helper. §3.4 now carries the table, with each site's owner and the place
that waits for it.

**The other clause is now true and deserves its mechanism written down.** C-34
made both per-session goroutines waited for at shutdown, and the chain is worth a
reader's time: `Close` waits on `c.done`; `c.done` closes in `serve`'s deferred
teardown, **after** `actorDone.Wait()` and **after** `deregister`, so `Sessions()`
cannot report a live session at the instant `Close` returns; and the wait is
bounded by the caller's context, which returns an error naming the count rather
than hanging.

**No source change is warranted for the four non-`spawn` sites** and I looked for
one. Each has a named owner, a stop condition and a waiter — checklist §6.4's
three — including `waitFor`'s helper, whose *not* being waited for is what makes
the drain deadline enforceable, and whose failure mode is counted
(`EffectAbandoned`) rather than silent.

> **C-49 — the source half. Owner: DEV-1.** `internal/session/effects.go:26-32`
> claims *"Every goroutine in this library is started here"* and calls a bare `go`
> statement *"a defect a reviewer can look for"*. That is the same false sentence
> in the same words, one directory away, and it is the one a maintainer reads
> first. **Falsifier:** the godoc describes `spawn` as the *effect* spawner and
> names the four sites that are not it, or the tree makes the original claim true.
> One comment. LOW; not blocking.

---

### 8.4 C-41 / D-31 — discharged, from DEV-2's source

Mine to discharge, and I said so when I upheld it. RFC **§7.6.1** now states what
`client/runtime.js` does on `Error{RATE_LIMITED}`: the latch is *not* cleared, the
request is re-armed on **equal jitter** — `b = min(15 s, 1,000 ms · 2ⁿ)/2`,
`delay = b + random(0, b)`, first retry in [500, 1000) ms — at most one request in
flight per gap, cleared by any applied `Patch` or `Snapshot`, reset by a
reconnect, left armed while the tab is hidden and only pulled forward by
visibility, and terminating at the server's own `3 × ResyncBurst` close (`4008`),
which the client retries. **The base is recorded as a guess at a server-side
default**, because the wire carries no retry-after and the `Snapshot`'s session
parameters carry the heartbeat interval, the frame cap and the ack window but not
the resync budget. Closing that is a wire change; it is filed, not smuggled in.

**§8.4 gains the sentence that matters more than the paragraph**: there are two
schedules, they are deliberately different, and full jitter's draw from zero — the
right answer for a reconnect herd — is the wrong answer for a refusal, where a
delay near zero is precisely the request the server just declined. A reader who
implemented a second client from §8.4 alone would have built the wrong one, which
is the defect D-31 filed.

**My own cheap falsifier now passes:** `grep -c 'equal jitter'
docs/rfc/001-architecture.md` returns **4**, where it returned 0. The real
falsifier is still the expensive one and it is not discharged by a grep: a second
client implemented from §7.6 and §8.4 alone re-arms after a refusal and does not
re-create D-29. Nobody has built one.

---

### 8.5 What I changed

| File | Change |
|---|---|
| `docs/adr/001-transport.md` | §7's X3 row, its "four lines" framing and its closing arithmetic; §7.1's banner (body untouched — it is the record of what was proposed); **new §7.2**, the ruling, with C-45 and C-46; §9's **O2 closed by measurement**; §3.1.3's *"ack + replay window"* corrected to what `window.go` does (PM-1 ledger §7 item 11, blessed DELETE-NOW); changelog |
| `docs/rfc/001-architecture.md` | **§3.4** rewritten from the tree (C-35(b), C-49); **§6.2.2** gains one sentence *below* the untouched table, naming the line it is missing; **new §6.2.5** (X3's same-landing move, C-14(1)); **new §6.2.6** (ADR-002's budget); **new §7.6.1** and a §8.4 paragraph (C-41); changelog |
| `docs/adr/002-observability-memory-budget.md` | Status → **ACCEPTED WITH CONDITIONS**, DEV-1's non-approval left standing; **new §8**, the ruling, with C-47 and C-48 |
| `docs/reviews/rulings-review-wave.md` | this section |

**Nothing else.** No code, no `docs/pm/`, no `docs/gates/`, no `docs/bench/`, no
`ci.sh`, and nothing QA-2 had open.

### 8.6 Named for their owners, not ruled on

- **RFC §6.2.2's window row still reads 1,024** where the tree says
  `retentionSlots() × sizeof(slot)` = 17 × 48 = **816**. Both halves the review-wave
  ⚠ note was waiting for have now landed — `slot` at HEAD is
  `{serverSeq, patchID, span}` = 48 B — so the row is patchable and the note's own
  instruction applies: take it from the code, not by retyping. **DEV-1**, and it
  feeds a gate figure.
- **C-35(c) — QA-2 has no sign-off at HEAD.** ~~Unchanged by anything here, and
  it is the item that most clearly gates the checkpoint (PM-1 §8 item 3). Nothing
  in §8 substitutes for it.~~ **DISCHARGED the same day**, by `34945818` and
  `73f5bf2f`: QA-2's PASS at `1864cf92` covers `5a2ca417` and everything after
  it. **Struck rather than deleted, because a sentence that was true when written
  and false an hour later is exactly what this project keeps catching**, and the
  strike is the cheap form of the fix. The re-review that accepts it is
  [checkpoint-3.md §10](checkpoint-3.md), verdict **APPROVE**.
- **`docs/instrumentation.md` asserts a deletion that has not happened**
  (PM-1 §7.3). Not mine this turn; it is the same defect class as C-35(b) and it
  is still open.
