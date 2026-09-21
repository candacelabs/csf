# PM-1 — checkpoint-3 condition closure ledger

| | |
|---|---|
| **Owner** | PM-1 (scope) |
| **Date** | 2026-08-05 |
| **Pinned at** | `af9057d1..d06101bb` — **50 commits** since L9-1's BLOCK |
| **Accounts for** | [checkpoint-3.md](../reviews/checkpoint-3.md) **C-33…C-44** · [invariants.md](../reviews/invariants.md) **BR-1…BR-9**, **U-1…U-9** · [deletion.md](../reviews/deletion.md) **findings 1…13** · [deduplication.md](../reviews/deduplication.md) **D-1…D-10**, **R-1…R-9**, §9 |
| **Governed by** | [rulings-review-wave.md](../reviews/rulings-review-wave.md) · [review-checklist.md](../review-checklist.md) |
| **Not this document** | the checkpoint-3 **gate report**. This is the input to it, and to L9-1's re-review. Nothing here ticks a PRD box |

L9-1's closing line was *"Re-request review with the three green and I expect to
approve."* Fifty commits have landed since, whose subjects claim to close most of
the conditions and most of three reviews' findings, and nobody had checked that
claim end to end. A re-request that hands the reviewer a ledger of unverified
commit subjects wastes the round; a ledger that is *wrong* is worse than none,
because the next reviewer relies on it.

So every row below was opened. A commit subject is a claim.

---

## 0. What "verified" means in this document, and what it does not

**What I did.** For each row: read the diff of the commit that claims it
(`git show`), found the assertion or the code path in the tree at HEAD, and
checked that the thing the finding was about is no longer there. Where the
closure was supposed to be a **test**, I read the spec and asked whether it can
go red — a spec that cannot fail is not a closure, and this repository has
caught that five times (C-21's unread `total`, D-19's `clean` printed without
`gofmt`, D-20's suite that was green because it never ran, C-33's skip that never
skipped, and the `Fixed1` table that asserted the bug).

**What I ran.** Two things, both free: `sh bench/scripts/verify-ready.sh` (D-6 —
exits 0, three copies byte-identical, sha256 `9f701e1f…`), and `ci.sh`'s D-5
module-list comparison replayed by hand in bash (11 satellite modules on disk,
11 declared, no difference either way).

**What I did not run, and why.** No Go, no container, no `bash ci.sh`. **DEV-1 is
running a cpuset-pinned N=1000 memory campaign on this host as I write** and CPU
load perturbs it; three of the fifty commits above landed into `docs/bench/`
while this document was being written. Every row that would need a toolchain to
settle says so in its own words rather than being marked green on inference.

**What I did not read a number out of.** `docs/bench/**`. DEV-1 owns it and it is
moving. Where a condition's evidence lives there, the row says so and names DEV-1.

**Status vocabulary.**

| | |
|---|---|
| **CLOSED** | the finding's falsifier is satisfied in the tree at `d06101bb`, and I found the code or the assertion myself |
| **OPEN** | not closed. The owner is named and so is the specific next action |
| **RULING OWED** | the work is landed; what is missing is somebody's decision, not somebody's diff |
| **DELIBERATELY NOT FIXED** | closed as a decision to leave it, with the reasoning recorded so it is not re-opened as an oversight |

---

## 1. Counts

```
                     CLOSED   OPEN   RULING OWED   DELIBERATELY NOT FIXED   rows
C-33…C-44 (15 rows)       7      7             1                        0     15
BR-1…BR-9                 9      0             0                        0      9
U-1…U-9                   7      2             0                        0      9
REV-DEL 1…13              9      4             0                        0     13
REV-DUP D-1…D-10, R, §9   7      2             0                        3     12
                     ------ ------ ------------- ------------------------ ------
                         39     15             1                        3     58
```

C-33 and C-35 are split into lettered rows because each has halves that closed
separately — C-35 already carries L9-1's own `C-35(b)`, and this document keeps
that convention rather than averaging two states into one.

**All nine BROKEN invariants are closed.** That is the single most substantial
result in this ledger and it was the largest body of work in the fifty commits.

**Blocking the gate today: nothing that a diff closes.** The three blocking
conditions are discharged as engineering; what remains on them is one L9-1
ruling (C-35(a)), one QA-2 re-verification (C-35(c)) and one `bash ci.sh` run
nobody has quoted (C-33(a)'s falsifier). §8 says what each needs and from whom.

---

## 2. C-33 … C-44 — L9-1's conditions

| id | what it was | closed by | what I checked | status / owner |
|---|---|---|---|---|
| **C-33(a)** | **BLOCKING.** `os.IsNotExist` does not unwrap, so six fixture-skip guards never fired; `bash ci.sh` exited 1 on the two bench steps `af9057d1` added | `ebc2da8f` | All six guards read `errors.Is(err, fs.ErrNotExist)` at HEAD; `grep -rn 'os.IsNotExist' bench/ --include=*.go` returns only comments and the two new assertions that *pin* it. New specs at `chat_test.go:476` and `dashboard_test.go:424` assert both halves — the wrap stays recognisable to `errors.Is` **and** `os.IsNotExist` still cannot see it — against a real `GinkgoT().TempDir()`, so they go red if a loader drops its `%w`. `ebc2da8f` quotes both fixture conditions per module (absent: chat 61/62 1 skipped, dashboard 84/88 4 skipped; present: 62/62, 88/88, 0 skipped). **Not verified by me: no full `bash ci.sh` run at any commit in this range is quoted anywhere in the repository, and HEAD is 49 commits past `ebc2da8f`.** What would verify it: `docker run --rm -v "$REPO:/w" -w /w dis-gotth-live:latest bash -c 'cd gotth-live && bash ci.sh'` on a checkout with no `bench/fixtures/*/ticks.jsonl`, exit 0, quoted | **CLOSED** (the predicate). The run C-33's falsifier asks for is §8's first item |
| **C-33(b)** | The gate *prints a false statement*: `bench_fixture_note` is computed from `[ -f … ]` and announced as what the suite did, and its scope ("the §2.5 digest specs") is narrower than the set of specs that need the fixture. L9-1: *"Fix the predicate **and** the sentence"* (§2.3 pt 3, §7 pt 2) | — | `ci.sh:449-454` is unchanged: still `[ -f bench/fixtures/chat/ticks.jsonl ] && …` → `"fixtures ABSENT — the §2.5 digest specs skipped"`, still printed at `:492` before the suite runs. It is still a prediction dressed as an observation, and it is still narrower than the truth: `dashboard_test.go:582`'s *"stays inside §2.4's element and SVG bounds at the real shapes"* is one of the four dashboard skips and is **not** a §2.5 digest spec. `ebc2da8f`'s own measured table (4 skipped) is the proof that the sentence undercounts | **OPEN** — the orchestrator (`ci.sh`). Print the regeneration hint and let Ginkgo report its own skip count, which is D-19's rule for `gofmt` |
| **C-34** | **BLOCKING.** `App.Close` returned `nil` over sessions it never touched — 32 of 300 measured; `go doc` said *"drains **every** session"* | `ed9f73b6` | Two mechanisms, both read in the tree. (1) `Handler.Close` sets `draining` **and** snapshots `h.sessions` in one critical section, and `register` now returns an error and refuses under the same mutex — so a registration is on exactly one side of the snapshot and there is no third interleaving. (2) `newConn` is split out of `serve` so registration happens on the `ServeHTTP` goroutine before any session goroutine exists, and `deregister` now precedes `close(c.done)` so `Sessions()` cannot lag a `nil` return. `internal/wsx/close_race_test.go` is a 300-round Ginkgo spec asserting **both** documented halves (empty registry *and* the client saw `GOING_AWAY`), reports the rate as a `ReportEntry` either way, and reads **to error** rather than one frame — the spec-defect-that-looks-like-the-product-defect is called out in its own comment. `ed9f73b6` quotes 35/300 (11.7 %) before against L9-1's 10.7 %, and 0/300 after, which is the agreement that says it reproduces *their* defect | **CLOSED** |
| **C-35(a)** | **BLOCKING.** ADR-001 §7's X3 and condition C-14: `readBufferBytes = 512` moved one of X3's four named lines and neither X3 nor RFC §6.2 moved with it; X3 did not ratchet | `ae61f325` | ADR-001 gains **§7.1**: every line at its current value with its basis named (read buffer 512 code constant; **write buffer 1,024, a line X3 never had**; conn struct 2,370 measured; read-pump stack 8,192 still an estimate and named as owed; runtime `g` 410 measured) totalling 12,508, and C-14(2)'s ratchet applied and shown: 12,508 × 1.1 = **13,759 B proposed**. RFC §6.2 moves in the same landing as **§6.2.4**. **Two deliberate departures from L9-1's falsifier, both argued in place rather than taken silently**: X3 is not set to 11,204 (that would be a ceiling below the write buffer the transport actually pays — a false ratchet), and §6.2.2's estimate table is pointed at rather than edited, per its own "kept whole and unedited" preamble. So `grep -n '4,096' docs/adr/001-transport.md` still returns the X3 row — now carrying *"Re-derived … see §7.1, which proposes 13,759 B and awaits L9-1"* | **RULING OWED** — **L9-1**. The ADR is L9-1's and C-14 is L9-1's condition. What is owed: adopt or refuse 13,759, and rule on the two departures. **Also in flight**: the conn-struct 2,370 and `g` 410 are measurements and DEV-1 is re-measuring; DEV-1 confirms they survive |
| **C-35(b)** | RFC §3.4 contains a sentence that is false: *"there is no bare `go func()` in the library"*, and *"both owned and both waited for at shutdown"* | — (half) | The **second** clause is now true: C-34 makes the session goroutine waited for at shutdown. The **first** is still false. `docs/rfc/001-architecture.md:211` is unchanged, and `grep -rn 'go func' live/ internal/` over non-test files still returns five sites, of which `internal/wsx/handler.go:254` has no `WaitGroup` registration and no metric at the spawn. `ae61f325` and `e1360283` are the only two commits to touch that RFC and neither hunk is anywhere near §3.4 (`@@ -481`, `@@ -617`; `@@ -487`, `@@ -710`, `@@ -1252`, `@@ -1261`, `@@ -1448`) | **OPEN** — DEV-1 (the source) + L9-1 (the RFC). One sentence, in the edit pass C-35 was filed inside |
| **C-35(c)** | L9-1 **AFFIRMED but NARROWED** QA-2's PASS: it was earned at `ce52d2f9`, three commits before the transport change, so **there is no QA-2 sign-off covering `5a2ca417`** (checklist §8.6) | — | Still true, and now understated. `docs/qa/checkpoint-3-chaos.md:1676` still reads *"PASS. QA-2 clears its half of checkpoint 3 at `ce52d2f9`."* The only later QA-2 work in the range is `bc26dd86`'s D-30 addendum, re-verified at `b7840fb8` and `281586c3` — that is D-30, not §R8's rows. Since L9-1 wrote the narrowing, `internal/wsx` gained C-34 and BR-8, `internal/session` gained BR-1…BR-9, `internal/render` gained BR-3 and U-6 and `internal/protocol` gained D-4 and U-5, so the re-verification owed is now much larger than "the transport change" | **OPEN** — **QA-2**. Re-run the Phase-3 chaos verification at HEAD and state which of §R8's rows the change set could move. This is a gate item: the checkpoint cannot close without it |
| **C-36** | The 512/1,024 buffer saving was silently conditional on the ResponseWriter implementing `http.Hijacker` **directly** — 6,656 B/session lost behind Go 1.20+ `Unwrap`-only middleware | `0929bf5a` | `rightSized` now calls `hijackerOf(w)`, which walks `Unwrap` and tests `http.Hijacker` **before** `Unwrap` at each level, so middleware that implements its own `Hijack` still wins; `writeHeaderNowOf` does the same for gin. Five specs at `hijack_internal_test.go:282-340`, and the one that matters is *"prefers a middleware's own Hijack over anything further down"* — a fix that walked straight to the bottom would pass the other four and steal a declared capability. The doc half (*"one sentence … in the G2 method naming the ResponseWriter shape the figure assumes"*) landed at `42f197ff` as g2-baseline §9.6.1, which states that `memsrv` mounts on a plain `http.ServeMux` so the declining branch was never taken in any measured run | **CLOSED**. **In flight**: §9.6.1 lives under `docs/bench/` and DEV-1 is rewriting that document — DEV-1 confirms it survives the re-measurement |
| **C-37** | A peer decides whether the server keeps the memory: 513 pipelined bytes restore net/http's 4,096 pair, and no metric or report could say so afterwards | `42f197ff` | g2-baseline §9.6.1 answers the falsifier in its second form: the measured client **cannot** pipeline, and how that was checked is named (`test/memory/cmd/memdrv` dials with `websocket.Dial`, then enters a read loop with every `send` downstream of a read — no path puts a frame in the upgrade's segment), and the adversarial figure is stated beside the benign one (≈6,656 B/session back, returning the engineered arm to roughly the pre-`5a2ca417` live heap). Residual, named there and not discharged: **QA-2** owns whether the *Phase 5* comparison workload's client pipelines and whether that report carries both numbers; a `Buffered()` histogram at hijack time is the cheap answer and is not run | **CLOSED** against its falsifier. Phase-5 residual owned by QA-2. Same `docs/bench/` in-flight caveat as C-36 |
| **C-38** | `App.Handler()`'s meaning changed and its godoc did not; `App.Close`'s godoc was false | `cf2b3e73` | `live/app.go` — `Handler`'s godoc now has a "The live route returns at the upgrade" section naming all three observable consequences (middleware completes at the handshake, `context.WithoutCancel`, `Close` is how a session ends), and `Close`'s says "every session" is *"held by a spec rather than by this sentence (C-34)"* and states what it does not wait for. Both `api-surface.md` rows carry the same, citing `5a2ca417`/C-38 and `ed9f73b6`/C-34. **One clause of the falsifier is not met**: it asked for `5a2ca417` *"cited in §10's changelog"*, and `grep -n '5a2ca417' docs/api-surface.md` returns only the row. Trivial, and named so it is not mistaken for green | **CLOSED**, with that one-line residual for DEV-1 |
| **C-39** | `Fixed1` rounded negative ties toward +∞ instead of away from zero, and its own spec asserted the wrong value | `ebc2da8f` | `dashboard.go`'s `Fixed1` now takes the sign off first, exactly as ECMA-262 21.1.3.3 step 6 does, before resolving the tie on the magnitude. The `DescribeTable` entries carry node's values (`-0.25 → "-0.3"`, `-1.75 → "-1.8"`, plus two new quarters), so they are red against the old implementation — the defect is no longer held in place by the artifact that exists to prevent it. The second table drives the ties *through the arithmetic* at prev 400 → 401 and → 399. Cross-checked against the oracle rather than the spec text: `ebc2da8f` reports byte-identical output against node v24 over all **79,400** deltas the fixture's domain can produce, 306 of them exact ties, 146 negative, on all of which the old code disagreed | **CLOSED** |
| **C-40** | FR-70's new obligation (consumer-reachable API only: no `internal/` import, no build tag) is checkable and nothing checks it | — | Nothing landed. `grep -n 'FR-70\|internal.\|go:build' ci.sh` finds no such step, and `grep -rn 'C-40' .` outside the review returns nothing. L9-1 priced it at *"two lines in `ci.sh`, in the step `af9057d1` just added"* | **OPEN** — **BENCH-1 + QA-2**. Two greps in `ci.sh`'s bench loop: `internal/` imports under `bench/apps/*/gotth/`, and a build-tag scan |
| **C-41** | D-31 UPHELD: the client's `Error{RATE_LIMITED}` resync schedule is protocol-visible, described by no binding document, and **contradicted** by RFC §8.4's full-jitter schedule | — | Nothing landed. L9-1's own cheap proxy: `grep -c 'equal jitter' docs/rfc/001-architecture.md` returns **0**. §7.6 still describes the pre-D-29 behaviour, so the document that produced D-29 can still produce it | **OPEN** — **L9-1**, and L9-1 said so (*"C-41, and it is mine to discharge"*). LOW; owed before `protocol.md` is called complete, **not** before this gate |
| **C-42** | The distinguishing test for ruling 1 (*a criterion may be struck after a measurement only when the argument is invariant to that measurement's outcome*) goes into the PRD §9 preamble against §6.1.2 by name | — | Nothing landed. `docs/PRD.md` is untouched in the fifty commits, and the phrasing appears nowhere in it | **OPEN** — **PM-1**, i.e. me. Not done this turn: `docs/PRD.md` is outside my write area for this work item, and it is an amendment-log edit that belongs with the gate report where the §9 boxes are also being moved. Non-blocking; it should not survive the gate report |
| **C-43** | Two of `bench/README.md`'s G-series entries (G-3 per-session data duplication, G-5 the two-tabs cookie behaviour) are **asymmetries**, not construction notes, and live outside the closed §2.6 register E6 names | — | Nothing landed in the register. `docs/bench/equivalence-spec.md` §2.6 still ends at **AS-7**; there is no AS-8 and no qualification on AS-3's *"Same visible behaviour"*, and §12's amendment log has no entry. `bench/README.md` was edited (`65391654`) for a different reason (CTR-5/F-CTR-6). L9-1's window argument still holds and is the reason this is cheap now: `bench/data/` contains no run ids, so the amendment log's *"measurement taken under old text?"* column reads **no** | **OPEN** — **BENCH-1 + QA-2 (spec owner) + L9-1 (§12 approval)**. See §7.1: the spec's own status line is a second problem on the same document |
| **C-44** | The three bench Go modules are not in `dependencies.md`; §2.1 enumerates three separate modules where there are six | **`d7568355`** (this turn) | New Tier 3 row and **§3.1** naming `bench/apps/{counter,chat,dashboard}/gotth`, their direct requires read from the three `go.mod` files (identical: templ v0.3.1020, ginkgo/v2 v2.32.0, gomega v1.42.1, the library through a `replace`, plus 18 indirects each), and the FR-74 reason in checkable form — `grep -c 'bench' go.mod` at the root is **0**, and the `replace ../../../..` is the line a consumer cannot have. §2.1's enumeration now reads **six**, with the count derived from the tree: `find . -name go.mod` prints twelve, one root and eleven satellites, all eleven in `ci.sh`'s `ci_modules` (checked by hand, no difference in either direction) | **CLOSED** |

---

## 3. REV-INV — the nine BROKEN invariants

All nine are closed. Every one landed with at least one Ginkgo spec, and every
commit message states the before/after the spec produced.

| id | what it was | closed by | what I checked | status |
|---|---|---|---|---|
| **BR-1** | H-11: every legitimate `ClientTelemetry` report rejected as forged (40 of 40), because the ack evicts the slot the telemetry is about to name | `37df5537` | `window.ack` no longer evicts — it moves `acked` and nothing else — and eviction is by age, bounded by `retentionSlots() = cap + 1`, which is what `push` already allowed. `slotFor` searches the whole ring. H-11 keeps its meaning: a forged id still misses, and one past the retention bound misses and is counted. The spec at `actor_test.go:264` sends **ack then telemetry**, which is the shipped client's order, and asserts 40 morph observations and a zero drop counter — the review's own repro, which reported 40 of 40 dropped before | **CLOSED** |
| **BR-2** | `Origin.source` composed from unvalidated application strings against a wire predicate: a 59-byte event name makes every patch it causes unsendable; `EffectSource()` has no bound or charset check at all | `152c7911` | Each half refused by the boundary that owns the string. `live/app.go`'s `validate` rejects a `Config.Events` entry failing `protocol.ValidOriginSource("event:"+name)` as a `*ConfigError` naming the byte budget — a registration mistake is a startup error. `Actor.execute` refuses an effect whose source cannot be namespaced *before it runs* and turns it into that effect's own failure event, counted under the existing `error` result with a **fixed** source label so a malformed string cannot mint a metric label. `effectOrigin` makes the composition total. The session harness now records `Framer.OnInvalid`, so *"no application string reaches `ValidateOutbound` and fails it"* is asserted rather than hoped — which is BR-2's own fix-shape item 3 | **CLOSED** |
| **BR-3** | The render hash and dirty bit were committed inside the render pass, before a send that can fail survivably — leaving a region stale for the life of the connection | `2273b55a` | `render()` records `updated`/`hashes` on the `Result` and installs nothing. `Renderer.Commit` is the only writer of `v.hashes` and runs after the caller has seen the write succeed; `Renderer.Discard` re-marks the updated fragments so the retry cannot be suppressed as identical. Both call sites are correct in `actor.go` (`emitPatch` and `emitSnapshot`, on the `!ok` branch). Panicked fragments are deliberately not re-marked, with the reason. The invariant BR-3 asked to be *statable* now is: `hashes[i]` is only written by the path that observed a successful write | **CLOSED** |
| **BR-4** | `takePending` cleared deferred provenance unconditionally, and `emitPatch` had two exits after it that never emit (plus `emitSnapshot`'s third) — so P5's *set equality* was false | `dd2fd9c8` | All three exits now call `Actor.redefer`, which folds the taken ids back into `pendingIDs` and redefers the origin through `deferPatch`. `takePending`'s godoc now says it is a take and not a commit. Two specs beside the existing flush-trigger spec, one of them the review's executed repro (AckWindow=4, a fragment rendering only `N`, a `Label` that changes state without changing markup). **One deviation, argued rather than hidden**: `redefer` caps `pendingIDs` at `CoalesceFlushCeiling` with an `Error` record, because a session that cannot send is exactly the one that keeps failing and an uncapped list is unbounded per-session memory. So P5 holds except under sustained emission failure past the schema's own ceiling, where no frame could carry the list anyway | **CLOSED** |
| **BR-5** | A mount whose snapshot fails validation left a zombie session: no snapshot, no close code, read pump running, evicted 30 minutes later as `4011 session_evicted` | `f95bfd2d` | `emitSnapshot` returns `(int, bool)` — and the commit argues why the second value is not derivable from the first — and `mount` takes the existing failure path on `!ok`: `Error{INTERNAL, fatal}` plus `Close(CloseInternalError, "the mount snapshot could not be sent")`. `resync` takes the same signature and deliberately ignores the flag, with the reason (that connection already has a snapshot). Spec asserts the close code, that no snapshot was reported, and that the client's `Error` is marked fatal | **CLOSED** |
| **BR-6** | The no-op resync short circuit bypassed H-14's budget entirely — a whole frame kind charged to no bucket at all | `664f7543` | The `resyncBucket.allow` check now precedes the short circuit in `resync()`, and the comment states H-14's two clauses and why the Ack clause is about *cost*, not about *charging*. The new spec counts **frames answered** rather than renders performed, which is exactly why H-14's own amplification spec passed vacuously across this path | **CLOSED** |
| **BR-7** | `sameState`'s comparable fast path compared pointer **identity**, so an in-place reducer froze `state_version` and made P4 false on the wire | `86a43f18` | The predicate moved to `live`'s `comparableState[S]`, resolved once where `S` is still a type parameter, and reaches the actor as `App.StateComparable()` read at construction — so no reflection moves onto the transition path and the reference kinds fall through to "changed", the documented-safe direction. The per-transition type check stays, correctly, because `==` would panic without it. Two specs, one of them the review's repro. **L9-1's ruling 5.5 is respected**: step 2's refusal of a pointer `S` at `New` is *not* implemented, and `Config[S]`'s godoc documents the hazard at the type parameter instead. **One thing I checked and am reporting as not closed**: ruling 5.5 named the `livetest` determinism detector as *"the one to pursue"* for step 2, and `livetest.ReplayN` **cannot** catch this case — `fold` starts both replays from the same `initial` handle, so an in-place reducer returning that same pointer makes `reflect.DeepEqual(wantState, gotState)` compare the mutated object with itself. That is not a BR-7 regression; it is that step 2 remains genuinely open. See §7.4 | **CLOSED** (step 1, which is what was blessed) |
| **BR-8** | `MaxSessions` was checked against a registry populated later, so N concurrent upgrades all passed a limit of 1 | `fff99245` | `admit` now reserves **both** limits in one critical section — `perID` as before, plus a `pending` counter — and the process check reads `len(sessions) + pending`. `register` converts the reservation under the same mutex (`h.pending--`); `releaseAdmission` returns it on every path between the two, and `release` deliberately does **not** touch `pending`, with the reason (double-counting would bound the process at half the operator's setting). The drain half was closed separately by C-34. The first spec is **concurrent on purpose** — serially, the first registration always completes before the second admission, so a serial spec passes against the defect, which is why the existing suite covered only the per-identity limit | **CLOSED** |
| **BR-9** | A client understating `last_applied_seq` falsified P7's non-overlap through no server fault | `d3c06eb7` | `resync()` clamps against two floors before deriving anything: `win.ackedSeq()` (the client's own high-water mark; the contradiction is counted and logged) and `Actor.lastSnapshotSeq`. **The second floor is more than the review asked for and the commit shows why the review's `max(last_applied, acked)+1` is insufficient**: a retry that outruns an acknowledgement leaves `acked` at or below the latched cursor, so the second range still overlaps the first. Against the snapshot floor it begins where a client that applied the snapshot now stands. That is load-bearing rather than academic — `79403c6a` made the shipped client close `4002` on an overlap, so this arithmetic decides whether a *correct* client evicts itself. The clamp also keeps `validateSnapshot`'s empty-range arm unreachable | **CLOSED** |

---

## 4. REV-INV — the U-series

| id | what it was | closed by | what I checked | status |
|---|---|---|---|---|
| **U-1** | H-13's second enforcement site (*"the client decoder"*) does not exist: fields 10 and 11 decoded, read by nothing | `79403c6a` | `applied()` in `client/runtime.js` now reads `superseded_from_seq`/`superseded_through_seq` and enforces the **range** clause, closing `4002` with a reason naming what disagreed. The kind clause stays outbound-only, and that is not a gap — **L9-1's ruling 5.2 split H-13 by clause** and applied it to `protocol.md` §6, on a measurement (importing `OriginKind` ships all six members, priced at 126 gzipped bytes for the identically shaped `ErrorCode`) and on the argument that a check which cannot change behaviour is restatement, not defence in depth | **CLOSED** |
| **U-2** | The client never checks the snapshot boundary it is told about; `p.server_seq <= seq` would silently move its own high-water mark backwards | `79403c6a` | Same commit, same function: `if (p.server_seq <= seq) return close(4002, …)` is the first line of `applied()`, and the comment explains that the failure already ended the session under H-7 — it just ended it naming the *client's* ack rather than the server's frame | **CLOSED** |
| **U-3** | H-4's headroom has exactly zero margin, the derivation is stated in two incompatible sets of terms, and nothing drives the union to it | `5e4a1351` | `session/limits.go` restates the derivation once, term by term: `(F-1) + 1 + 1 + 64 = F + 1 + MaxEventContributing`, so `F ≤ Ceiling - 1 - MaxEventContributing`. The constant does not move — it was right by cancellation, which is what the finding said. `backpressure_test.go:307` *"carries exactly the widest union the schema permits, and the boundary accepts it"* constructs the worst case and asserts the emitted list is **exactly** `CoalesceFlushCeiling` and that the outbound boundary accepts it; `:391` pins the same arithmetic as an identity so a constant moving alone is the cheaper failure | **CLOSED** |
| **U-4** | `unionReaches` and `unionEdges` implement the same three rules 60 lines apart, with nothing asserting they agree | `b60a9640` | `internal/session/union_internal_test.go`, in-package because both are unexported. 300 randomized trials over `(origin, pendingIDs, pendingOrig)` asserting `unionReaches(o, n) == (exact >= n)` for **every** `n` in `0..exact+3` — so both sides of the sum-of-parts short circuit are covered. Ids are drawn from a pool of 12, smaller than the lists, so duplicates are the common case and the deduplication rule (the one most likely to drift) is actually exercised; zero is drawn deliberately. Seeded from `GinkgoRandomSeed()`, so a failure is reproducible | **CLOSED** |
| **U-5** | `Framer.Write` is an exported bypass of `ValidateOutbound` — pre-encoded bytes plus a `Kind`, coupled to `Encode` by convention only | `5ecbe954` | L9-1's **ruling 4** overrode "unexport if no consumer" (there is a real cross-package consumer) and ratified the opaque token. `Encode` now returns `protocol.Encoded` — unexported fields, no constructor — and `Write` takes it. The bypass is **unconstructable** rather than merely unobserved, which is the difference between an invariant and a convention. The split that instrumentation §2.3 needs (encode vs send histograms not equal by construction) is preserved, and the commit says so | **CLOSED** |
| **U-6** | Render receives the renderer's shared `*bytes.Buffer` as its `io.Writer`; a fragment can type-assert it and keep `.Bytes()`, or retain and write later | `52a64e04` | Fragments render through a `fragmentWriter`. The buffer is unreachable (a `*bytes.Buffer` assertion fails) and a write outside the call is refused with `errWriterEscaped`, which `callRender` already converts into that fragment's own render failure. **Stronger than the review's specification**, which asked for this in dev mode only: it is unconditional and costs no allocation (one pointer per session, not per fragment). The residual is stated in the spec rather than assumed — within one pass all fragments share the writer, so a handle retained by A and used during B's render lands in B's markup, which is where B was writing anyway. `RenderFunc`'s godoc states the contract at the type an application implements | **CLOSED** |
| **U-7** | `Actor.Close`'s code is swallowed during shutdown: `closing` is both the Close-once flag and "refuse new effect emissions", two facts sharing one bit | — | Nothing landed. `Actor.Close` still opens with `if a.closing.Swap(true) { return }` (`actor.go:274`) and `shutdown` still sets the same flag first (`actor.go:234`), so a close named after teardown begins still returns without calling `a.closer` and `conn.finalCode()` still falls back to `CloseNormal`. `effects.go:183,251` read the same bit for the other meaning. `grep -rn 'U-7'` outside the reviews returns nothing | **OPEN** — DEV-1. LOW. A `sync.Once` or a separate `closeNamed atomic.Bool` |
| **U-8** | The `Mark`-at-defer-argument trick is load-bearing and undefended: the mechanical "make this clearer" refactor silently breaks every patch | — | Nothing landed. `actor.go:419-423` is unchanged: the comment says *"Mark runs now; reporting … waits"* but still does not say **why the argument position is required**, which is what U-8 asked for, and no spec is labelled as pinning the ordering | **OPEN** — DEV-1. LOW, and cheap: two sentences and a spec label |
| **U-9** | H-6's enforcement column claims *"and at parse"*, which describes nothing | `e1360283` | **L9-1's ruling 5.3**, applied by L9-1. `protocol.md:518`'s H-6 row now reads *"on the outbound boundary (§5.3) and nowhere else"*, with the reason at `:552` and a §12 changelog entry at `:828` | **CLOSED** |

---

## 5. REV-DEL — the deletion sweep

| # | what it was | closed by | what I checked | status |
|---|---|---|---|---|
| **1** | `examples/chat` and `examples/dashboard` each carry a hand-rolled protobuf decoder and `browser` harness — ~1,050 lines, unlocked by implementing the already-ledgered `livetest.Client` | `8756b861` + `d853ed83` + `8725629e` | `livetest.Client` is built (`client.go` 493 lines, `frame.go` 246, plus 373 of specs). `grep -n eachField` finds **no** copy in `examples/chat` — `wire_test.go` is 1,309 → **853**. `examples/dashboard/wire_test.go` 1,408 → **1,332** and `wire.go` 428 → **394**. **One copy survives on purpose and the reason is structural**: `examples/dashboard/wire.go` keeps `eachField` because `MeasureResync` is in a *non-test* file (`go run . -resync-cost 200`) and `livetest.Client` takes a `testing.TB` first, deliberately. The file's header says so, which is the difference between a residual and a defect. **The honest arithmetic is in REV-DUP §3.1 rather than here**: the repository is not yet smaller (−595 in the examples against +739 of new non-test code), and the number to hold this to is *one decoder instead of five* | **CLOSED** for the two modules this finding names. `test/routers` is D-3's row |
| **2** | `slot.bytes` and `slot.emittedNS` written twice per patch, read nowhere | `059d3193` | Both fields gone from `window.go`; `emitPatch`'s `n` became `_` and `emitSnapshot` kept its (it feeds `gotthlive_resync_bytes`), so `send`'s signature did not change. **The figures are re-derived rather than carried, which is L9-1 ruling §3.2's condition**: `actor_test.go:483` asserts `TrackedBytes() == 816 + 512 + 256 + 8`, and 816 is `17 × 48` — BR-1's `retentionSlots()` at the default `AckWindow` of 16, times a slot that is now two sequence numbers and a span reference. That is L9-1's stated arithmetic to the byte, and it is **not** REV-DEL's own stale `16 × 48 = 768` | **CLOSED** |
| **3** | `Metrics.FragmentLabels`, `fragmentAttr`, `RenderDuration`'s `fragment` parameter and `firstFragment` cannot be turned on; instrumentation.md §2.3 promises a knob no operator can reach | `e1360283` (docs only) | **This row is not what the brief expected and the difference matters.** It is **not** RULING OWED: L9-1 **ruled** on 2026-08-04 (rulings-review-wave §2) — *delete the field and the row* — on FR-65 precedent **and** on the sharper ground that the label would have **misattributed**, since the duration is one observation per whole render pass and the value passed is whichever fragment sat first in the update slice. Cardinality was explicitly *not* the objection. The doc half landed. **The Go half did not**: `internal/obs/metrics.go:157,175,206,394` still declare, initialise and branch on `FragmentLabels`, `session/actor.go:541` still computes `firstFragment(res.Updates)` and `:1283` still defines it. So `instrumentation.md:133` now asserts *"Field, attribute, branch and label row deleted together"* — **which is false of the code at HEAD**. A dead field became a document that contradicts the tree, which is a worse state than the one REV-DEL found | **OPEN** — DEV-1 (the server stream). ~28 lines, and it must be sequenced with BR-4 per ruling §3.5 (same twenty lines of `emitPatch`); BR-4 has landed, so it is unblocked |
| **4** | `obs.U64s`, `obs.Strs` — zero callers anywhere | `c01ebef9` | Gone. `grep -rn 'func U64s\|func Strs' internal/obs/` is empty | **CLOSED** |
| **5** | `live/app.go`'s hand-rolled O(n²) `itoa` beside `strconv.Itoa` in the same package | `c01ebef9` | Gone from `live/app.go`. (`live/instrumentation_test.go:293` defines its own `itoa`; that is a test helper and predates this range, not this finding's subject) | **CLOSED** |
| **6** | `Renderer.MarkAll` and `Renderer.MarkID` have no production caller | — | Nothing landed. `renderer.go:150` and `:153` still declare both; the only callers are still `renderer_test.go:145, 328, 329, 353`. L9-1 blessed `MarkID` DELETE-NOW and `MarkAll` with a condition (mutate the suppression check and show the two scaffolding specs red before substituting `v.Mark(state, state)`), sequenced **after BR-3** — which has landed, so this is unblocked | **OPEN** — DEV-1 |
| **7** | `wsx.Options.Now` — an injectable clock nothing injects | `c01ebef9` | Gone from `internal/wsx/handler.go` and `conn.go`. `session.Options.Now`/`Ticks` — the seam the actor's testability actually rests on — are untouched, which is the distinction L9-1's blessing insisted on | **CLOSED** |
| **8** | RFC §14.2's package-layout tree omits four `internal/` packages, `test/` and `tools/`, and quotes a stale "eight `livetest` symbols" | `e1360283` | Applied by L9-1 under the D-checkpoint convention, with authorship recorded in the RFC's own §17 rather than left to `git blame`. The tree was re-derived rather than patched; the stale count was replaced by **no number**, per §0's rule that a number a program reads lives in one place. L9-1 also corrected the *finding*: `test/` holds three modules, not four | **CLOSED** |
| **9** | `obs.SpanRef.IsValid` is test-only | `c01ebef9` | Gone; the three assertions moved to `SpanContext()` directly | **CLOSED** |
| **10** | `match()`'s inner `if (cur)` guard and its unreachable `return null` | `79403c6a` | `client/runtime.js:168` is now the single `return`, with a comment naming `morphChildren`'s own guard as the one worth keeping (it also skips the call). Landed inside the U-1/U-2 client commit, and `client/SIZE.md` records it separately at −3/−3 rather than netting it against the U-1/U-2 spend — which L9-1 called the right way to publish two changes in opposite directions | **CLOSED** |
| **11** | ADR-001:154 credits this design with a *"replay window"* it does not have | — | **Nothing landed.** `docs/adr/001-transport.md:154` still reads *"Our replacement (ack + replay window) is more capable but is ours to build and ours to get wrong."* `window.go:23-30` says the opposite as a decision, and the client agrees. Blessed DELETE-NOW; it is three words | **OPEN** — DEV-1, or L9-1 with the C-35 edit pass, since both are in this ADR |
| **12** | The generated codec exports an `OriginKind` enum nothing imports | — | **Nothing landed.** `client/codec.gen.js:141` still carries `export var OriginKind = {…}`, and `internal/clientcodec/emit.go:29` still walks `s.Enums` and emits every one. `runtime.js:84` imports five names and not this one. L9-1 blessed DELETE-NOW **in the generator** and added that it is *"now load-bearing beyond hygiene: ruling 5.2 turns on the client not importing that enum, so a generated export with no importer is the shape a future reader would take as permission"* — and `runtime.js:801` now cites exactly that argument in its own comment | **OPEN** — DEV-1 (the generator, not the generated file). 97 source bytes, 0 shipped bytes, and the reason is no longer the bytes |
| **13** | Three things REV-DEL looked at and did **not** recommend (`newSession()`'s two lines, `morphEl`'s second `preserved(a)`, `save()`/`restore()`) | `e1360283` | Closed by ruling: all three stay. `save()`/`restore()` was already closed once as QA-1 **D-21** and *"must not be re-opened a third time"* | **CLOSED** (as a decision) |

---

## 6. REV-DUP — the duplication review

| id | what it was | closed by | what I checked | status |
|---|---|---|---|---|
| **D-1** | `allowedOrigins` six times, and a bind-all `0.0.0.0` arm three of six never got — every documented containerised example invocation was refused with 403 | `af485014` | The arm is in all three `examples/*/main.go`; each example gains an `It` naming the loopback spellings, and `counter`/`chat` gain the Origin-allowlist `Describe` they never had. Converged to the bench spelling **exactly** rather than improved past it, because a seventh variant is the disease. Mutation-checked (the counter spec fails on `ContainElements` with the arm removed) | **CLOSED** |
| **D-2** | `livetest.NewTB` duplicates `ginkgo.GinkgoTB()`, which Ginkgo ships for this purpose; two idioms live, five files spelling it both ways | `e1360283` + `281586c3` | Symbol and suite deleted by L9-1's ruling 1 (`tb.go` 119 lines, `tb_test.go` 193); the 16 Go call sites across 7 modules and the guide's four fenced blocks migrated in one commit. `grep -rn 'livetest.NewTB'` over `.go` files returns **nothing**; 16 files now call `GinkgoTB()`. L9-1's constraint 1 was honoured — `docs/guide/testing-your-app.md` and `docs/guide/_samples/apptest/app_test.go` moved in the same commit, which is what `docs/guide/_samples/samples_test.go`'s byte-equality assertion over the guide's fenced blocks would otherwise have caught | **CLOSED** |
| **D-3** | `livetest.Client` ledgered and unimplemented, so four suites hand-roll a session driver and three also hand-roll a decoder | `8756b861`, `d853ed83`, `8725629e` (partial) | Two of three named suites retired onto `Client`. **`test/routers` is not**: `wire_test.go:121` still holds its own `eachField` and `harness_test.go` still has five `browser` methods. REV-DUP's own §3.1 records it as *unclaimed by this wave rather than blocked* — its `go.mod:18-26` objection is fully answered by `livetest` being an exported package. §3.1 also records the correction that matters: this finding's *"no new exported symbol"* did not survive implementation (`Client` had no constructor, and the ledgered `WaitFor` could not express what the consumers assert), so `live/livetest` moved 9/6 → 37/33 with the argument in api-surface §6 and §10 | **OPEN** (partial) — DEV-1. `test/routers`, the smallest of the three, inheriting a proven recipe |
| **D-4** | `checkListBounds` and `checkEnums` are the same descriptor walk, twice, on **both** hot boundaries | `4e0f780b` | One `checkFieldInvariants` in `invariants.go:32`, called from `inbound.go:120` and `outbound.go:32`; H-1 and H-4 reduced to `checkEnumField` and `checkListBound`. The visitor is per **field** rather than per element, with the reason (H-4's leaf is a statement about the list as a whole). The map rejection moves from one check's leaf to the walk's own structural rule, so it now applies on both boundaries. One behavioural consequence is stated rather than smuggled: with two walks every enum violation was found before any list violation; with one, the first violation in field order wins | **CLOSED**. **See §7.2 — REV-DUP's own summary table still says SPECIFIED for this row** |
| **D-5** | The module list: six unrolled blocks in `ci.sh`, a second projection in `gen.sh`, held honest by nothing — which is how the bench gap survived | `451c88ca` | `ci_modules` plus `ci_modules_unrun` (deliberately empty, with the argument for why an entry there must be argued in place), checked three ways against the tree: a `go.mod` with no entry, an entry with no `go.mod`, and an entry named nowhere else in `ci.sh`. **Extracted as a guard rather than a unification, which is what this review specified** — the steps are deliberately not collapsed because documentation quotes them by name, by output string and in one case by line range. The third check's weakness (it proves a path is *named*, not *run*) is documented at the site rather than left to be discovered. I replayed the comparison by hand: 11 satellite modules on disk, 11 declared, no difference either way | **CLOSED** |
| **D-6** | `bench/ready.js` — three byte-identical **tracked** copies, no sync script, no verify target, nothing that fails when they drift | `a313e433` + `5abe878d` | `bench/harness/ready.js` is the source, `sync-ready.mjs` generates, `verify-ready.sh` verifies, and `5abe878d` gave the verifier the `ci.sh` step it was built for. **I ran it**: exit 0, three copies `ok`, 4,340 bytes, sha256 `9f701e1f…`. **One deviation from the specification, measured rather than argued**: the copies stay git-**tracked**, because `//go:embed bench/ready.js` is resolved by the compiler and a gitignored copy makes a clean checkout fail in `dis-gotth-live:latest` — the node-free image `ci.sh` builds all three bench modules in, which cannot run `sync-ready.mjs` to repair itself. The verifier is `sh` + `cmp` for the same no-node reason, with two callers rather than one per world | **CLOSED** |
| **D-7** | `LoadShim` / `serveScript` / `serveCSS` across the three bench modules, ~25 near-identical lines each | — | Unchanged, and correctly so. Three separate modules, so sharing needs a fourth module all three require — which is the quarantine the bench `go.mod`s exist to hold (and which §3.1 of `dependencies.md` now records as a Tier 3 property). REV-DUP priced it: *"cost exceeds benefit at 25 lines; recorded so the next reader does not re-derive it. Revisit only if a fourth bench app lands"* | **DELIBERATELY NOT FIXED** — revisit trigger: a fourth bench app |
| **D-8** | The "walk up to the enclosing `go.mod`" loop twice in `gen.sh`, the second copy missing its `/` sentinel — a latent infinite loop (`dirname /` returns `/` forever) | `f95b86c1` | One `enclosing_module_dir()` at `gen.sh:176`, both call sites on it (`:202`, `:341`), sentinel intact, and the first site's error message survives verbatim for both. Driven directly under `timeout 10`: outside every module it prints and exits non-zero instead of spinning. **Fixed independently of D-5's extraction**, which is what L9-1's §7 asked for in as many words — *"a bug queued behind a refactor is a bug that ships"* | **CLOSED** |
| **D-9** | `step`/`say`/`die`/`usage`/arg-parsing across four bash scripts; three `docker run` recipes disagreeing about `$PWD`; two live divergences | `451c88ca` (part) | **The docker-recipe half is closed**: `ci.sh:542`, `:590` and `:635` now all print `-v "$PWD:/workspace" -w /workspace/candace/pkg/gotth`, matching the workflow, with a comment at `:585` explaining that `docker run` *creates* a missing `-w` rather than refusing — so a wrong paste fails obscurely. **The rest is not**: `ci.sh:105`'s `step()` and `measure.sh:154`'s `say()` are still the same `printf` under two names, `gofmt` is still asserted two ways inside `ci.sh`, and both named live divergences stand — `diag.sh` still asserts no tools despite requiring docker, and `measure.sh` still mounts no module cache where `diag.sh:82,154` mount `/tmp/gotth-gomod:/go/pkg/mod` | **OPEN** (low) — build owner. `test/memory/` is DEV-1's and in flight |
| **D-10** | The `":PORT"` residual in all six `allowedOrigins` copies: `-addr :8080` plus a browser at `http://127.0.0.1:8080` is refused | — | Unchanged in all six, **by design**. D-1's whole argument is that a seventh variant is worse than a shared sixfold bug, and fixing three of six would re-create exactly the divergence D-1 had just closed. This is **not** an oversight, and it is recorded here so it is not filed as one. The specification stands and is one commit touching all six together — or, better, folded into whatever resolves D-3, since *"derive a dev allowlist from a listen address"* would have ≥ 6 call sites and is the only member of that review clearing checklist §1.4 on its own. L9-1's §7 declined to rule: the bench and build owners decide | **DELIBERATELY NOT FIXED** — owner for the eventual single commit: BENCH-1 + DEV-1 together. *(The brief that commissioned this ledger calls this "REV-DUP finding 8". It is **D-10**, §7 of that document; D-8 is the `gen.sh` sentinel and is closed.)* |
| **R-1…R-9** | Nine duplications examined and ruled correct as they stand | — | L9-1's §7: *"correct and I am not re-opening any"*, with R-8 singled out (*"repetition here is the product"*). Recorded so a future sweep does not re-derive them | **DELIBERATELY NOT FIXED** |
| **§9** | The `examples/dashboard/wire_test.go:1316` flake, 1 in 8 then clean, **not** in `ci-intermittents.md` — which L9-1 called *"the actual defect"* | `7ff41b56` | Filed **and** fixed, in that order. `docs/qa/ci-intermittents.md` now lists it. The width was the part nobody had and it is what decides the fix: swept by injecting a sleep between the socket write and the causal row (0/20 red at no stall, 19/20 at 50 µs, 20/20 at ≥100 µs), and reproduced naturally 1 in 40 only under cgroup throttling. **A test defect, not a library one, and the row that distinguishes them is the mutation**: with no stall, deleting the provenance call makes the *new* spec go red. The fix carries no wall-clock term — a second patch on the socket proves the first patch's row was written | **CLOSED** |

---

## 7. Things I found that no row above covers

Five, recorded because a ledger that reports only the rows it was given is not an
audit. None is blocking; three are documents that have become false of the tree,
which is this project's own most-caught defect class.

### 7.1 `equivalence-spec.md` still says "Draft — pending L9-1 + PM-1 sign-off", and four documents call §2 frozen

L9-1 recorded this at §6.3 and asked that it be checked rather than assumed. **It
is still true.** `docs/bench/equivalence-spec.md:6` reads *"Status | Draft —
Phase 0 exit artifact, pending L9-1 + PM-1 sign-off"*, and §12 makes the freeze
conditional on L9-1 + PM-1 + QA-2 sign-off. Meanwhile `docs/PRD.md:1084` states
*"§2 is frozen under spec §12"*, `docs/OPERATOR-QUESTIONS.md:197,205` treats it
as frozen, `docs/api-surface.md:553` calls F-CTR-6 frozen, and
**`docs/pm/checkpoint-3-scope.md:104,108,143` — my own predecessor's file —
says it three times**, including *"PM-1 may not amend a frozen spec"*. So the
document four others treat as binding says of itself that it is not.

This is not a typo. It decides whether C-43's asymmetry additions are an ordinary
edit or a §12 amendment, and it decides whether Q-BENCH-1's counter-scope
question is *"a conformance question against a frozen spec"* — which is exactly
how both my scope pass and `OPERATOR-QUESTIONS.md` describe it.

**Owner: L9-1 + PM-1 + QA-2, jointly, because §12 names all three.** The cheapest
correct outcome is to sign it — nothing here argues the spec is unready, and
L9-1's only stated reason for not signing is that he has not. **Reported and not
fixed by me**: `docs/bench/` is DEV-1's write area this turn, and a status line
is a sign-off, which PM-1 cannot supply alone.

### 7.2 REV-DUP's own summary table is stale for D-4

`deduplication.md`'s §0 table still reads *"D-4 … **SPECIFIED** — owner of
`internal/protocol`"*. D-4 landed at `4e0f780b`. `5abe878d` — *"REV-DUP's ledger
catches up to four closed findings"* — updated D-5, D-6, D-8 and §9 and missed
D-4, which had landed one commit earlier in the same sweep. §10's *"What I
changed"* table likewise still lists only `af485014`.

**Owner: whoever lands the next REV-DUP commit** (the D-3 `test/routers`
migration is the natural one). One row.

### 7.3 `instrumentation.md` asserts a deletion that has not happened

Covered as REV-DEL finding 3 above, repeated here because its *shape* is the
finding: `instrumentation.md:133` and `:853` now say the fragment label's
*"Field, attribute, branch and label row deleted together"*, and the field,
attribute and branch are all still in `internal/obs/metrics.go`. The document
half of a two-part fix landing alone converted dead code into a document that
contradicts the tree. **Owner: DEV-1**, and the Go deletion closes both.

### 7.4 BR-7 step 2's named detector cannot detect the case it was named for

L9-1's ruling 5.5 refused step 2's second option (refusing a pointer `S` at
`New`) and directed step 2's *first* option — *"the determinism detector in
`livetest`, replaying a log twice and comparing"* — as the one to pursue.
`live/config.go`'s new godoc now tells applications exactly that: *"The
determinism helpers in `live/livetest` are what catch a reducer that has
forgotten it."*

**They do not, for the pointer case.** `livetest/replay.go`'s `fold` starts from
the caller's `initial` on both runs, so a reducer that mutates in place and
returns the same pointer produces `wantState == gotState == initial` and
`reflect.DeepEqual` compares the mutated object with itself. `ReplayN` catches a
clock, a random source and map iteration order — the three things its own godoc
names — and not this. This is a **reading**, not a measurement: what would settle
it is a spec in `live/livetest/` that hands `ReplayN` an in-place `*Foo` reducer
and asserts it fails.

**Owner: DEV-1.** Not a BR-7 regression — BR-7 step 1 is closed and is what was
blessed. It is that step 2 is open and one document now implies it is not.

### 7.5 `d66e4953` changed the root `go.mod` and no obligation was re-quoted

L9-1's §8 recorded *"the root `go.mod` is **unchanged** across the whole batch — I
checked … The library gains no dependency, which is the right answer."* That
sentence is no longer true at HEAD. `d66e4953` moved templ, coder/websocket and
the three OTel modules from `// indirect` to direct, dropped
`go.opentelemetry.io/auto/sdk` and `go-logr/stdr`, and added
`github.com/rogpeppe/go-internal` and `gopkg.in/check.v1` as indirects.

Two consequences, both on `dependencies.md`:

1. **§5's standing obligation** is that every PR changing `go.mod` re-quotes
   obligations 1–6. It was not. §2.2(c) and §2.3(c) both quote a root build list
   of **61** and neither has been re-measured. **Not verified by me** — that needs
   a toolchain, and DEV-1 has the host. What would verify it:
   `docker run --rm -v "$REPO:/w" -w /w/gotth-live dis-gotth-live:latest bash -c 'go list -m all | wc -l'`.
2. **§1.4's D1 condition 1 has fired.** It says the library imports
   `otel/trace` and `otel/metric` and **never** `go.opentelemetry.io/otel`
   itself — *"if `otel/attribute` proves unavoidable for span attributes it is
   added here with a stated reason, per the condition"*. Library code imports
   `go.opentelemetry.io/otel/attribute`, which is a package **of the root
   module**; the root has therefore been a requirement all along and `d66e4953`
   only made it visible. The dependency did not change and the ledger's sentence
   about it became false. I have **marked §1.4 and not patched it**, following
   L9-1's own §3.2 precedent for RFC §6.2, because D1 is L9-1's decision.

**Owner: DEV-1** (the measurement and the row) **+ L9-1** (condition 1).

### 7.6 The commit-footer convention, which L9-1 raised as a nit at `0c969145`

L9-1 flagged one commit whose footer read `Claude Fable 5` where the batch's
convention is `Claude Opus 5`. Counted over `af9057d1..d06101bb`, one footer per
commit: **27 of the 50 read `Claude Fable 5`, 20 read `Claude Opus 5`, and 3
carry no `Co-Authored-By` trailer at all.** The nit is now the majority spelling
and there is a second, smaller one under it. Both are trivial and both are
stated conventions; recorded once here so they are either adopted or dropped
rather than drifting a third time.

---

## 8. What checkpoint 3 can and cannot claim, today

L9-1's §9 listed seven things checkpoint 3 **MAY NOT** claim on his authority.
Here is each, answered against the tree at `d06101bb`.

| # | L9-1's MAY-NOT | today | what it still needs, and from whom |
|---|---|---|---|
| **1** | *"That the gate is green."* | **NOT DISCHARGED — and it is the cheapest one left** | The defect is fixed (C-33(a)); the **run is not quoted**. Nobody has run `bash ci.sh` end to end at any commit in this range, and HEAD is 49 commits past the fix. **QA-2 or the orchestrator**: one run in `dis-gotth-live:latest` on a checkout with no `bench/fixtures/*/ticks.jsonl`, exit 0, the three bench steps reporting `clean` and the two suites reporting non-zero `Skipped`, pasted into the gate report. Until that exists, nothing in this checkpoint may be reported as CI-verified — L9-1's sentence stands word for word |
| **2** | *"That the three benchmark applications are gated."* | **DISCHARGED on the mechanism, pending #1's run** | The guard predicate is `errors.Is(err, fs.ErrNotExist)` in all six places and two new specs pin both halves so it cannot regress silently. `ebc2da8f` quotes per-module runs under **both** fixture conditions. What is missing is only the whole-gate run in #1. Separately, C-33(b) — the step still *announces* a skip it computed from file presence, in a sentence narrower than the set of specs that actually skip — is **OPEN** against the orchestrator |
| **3** | *"That QA-2 cleared the transport change."* | **NOT DISCHARGED, and the gap has grown** | `docs/qa/checkpoint-3-chaos.md:1676` still says PASS *at `ce52d2f9`*. **QA-2 must re-verify at HEAD** — not "pending QA": re-run the eight Phase-3 chaos cases with `GOTTHLIVE_SOAK=1 GOTTHLIVE_MEASURE=1` at `d06101bb` and state which of §R8's rows the change set could move. The set is no longer just `5a2ca417`: it is C-34 and BR-8 in `internal/wsx`, BR-1…BR-9 in `internal/session`, BR-3 and U-6 in `internal/render`, D-4 and U-5 in `internal/protocol`. Cases 3, 4, 6 and 7 are the named surface (§8.6). **This is the one open item that most clearly gates the checkpoint** |
| **4** | *"That `App.Close` drains every session, or that api-surface's row for it is true."* | **DISCHARGED** | `ed9f73b6` closes the window structurally (register refuses under `Close`'s own mutex) rather than narrowing it, `internal/wsx/close_race_test.go` asserts both documented halves over 300 rounds with the rate reported either way, and `cf2b3e73` made the godoc and the api-surface row say what the code does. 35/300 before, 0/300 after, against L9-1's own 32/300 |
| **5** | *"Any per-session byte figure from the transport change, until C-35's arithmetic is restated and C-36 says which ResponseWriter shape the figure assumes."* | **PARTLY** | C-36 is discharged in both halves (the wrapper walks `Unwrap`; g2-baseline §9.6.1 names the shape the measured arm had). C-35's arithmetic **is** restated, in ADR §7.1 and RFC §6.2.4, with two departures argued in place. What is missing is **L9-1's ruling**: adopt or refuse the proposed **13,759 B**, and rule on including the write buffer as X3's fifth line and on pointing at §6.2.2 rather than editing it. Until then ADR-001 §7's X3 row still shows the 4,096-based derivation, now marked. **Also**: the figure feeding it is DEV-1's and DEV-1 is re-measuring, so L9-1 should rule against the settled campaign, not against `ae61f325`'s snapshot of it |
| **6** | *"That `docs/bench/equivalence-spec.md` is frozen under §12."* | **NOT DISCHARGED — unchanged, and it is now inconsistent in five documents** | The status line still reads *"Draft — pending L9-1 + PM-1 sign-off"* while PRD, OPERATOR-QUESTIONS, api-surface and `checkpoint-3-scope.md` all describe §2 as frozen. §7.1 above. **L9-1 + PM-1 + QA-2 sign it, or the four other documents stop saying frozen.** Checkpoint 3 may still say §2 is treated as frozen *in practice*; it may not say the spec is frozen under §12 |
| **7** | *"That the §2.5 conformance of the dashboard's rendered numbers holds."* | **DISCHARGED** | `Fixed1` takes the sign off before resolving the tie, its `DescribeTable` carries node's values so it is red against the old code, and the function was cross-checked against node v24 over all 79,400 deltas the fixture's domain can produce — 306 ties, 146 negative, all previously wrong, all now byte-identical |

### The short version, for the re-request

**Discharged: 4 of 7** (#2 on the mechanism, #4, #5's C-36 half, #7).
**Outstanding: 3, and none of them is a diff.**

1. **`bash ci.sh` at HEAD, exit 0, quoted** — orchestrator or QA-2. Blocks the
   claim that anything here is CI-verified.
2. **QA-2 re-verification at HEAD**, naming the §R8 rows the change set could
   move — QA-2. This is the gate item with real work behind it.
3. **L9-1 rules on X3** (13,759 B, the write-buffer line, the two falsifier
   departures) — L9-1, against DEV-1's settled campaign rather than against the
   in-flight snapshot.

Non-blocking and owed before the gate report rather than by it: **C-42** (mine),
**C-41** (L9-1's, and explicitly not owed before this gate), **C-40**
(BENCH-1 + QA-2), **C-43** plus the freeze inconsistency in §7.1 (L9-1 + PM-1 +
QA-2), and the seven open engineering rows — REV-DEL 3, 6, 11, 12, U-7, U-8,
D-3's `test/routers` — none of which L9-1 made a condition of this gate.

---

*Verified at `d06101bb` by reading diffs and the tree; `git status --porcelain`
over my file area held only this document and `docs/dependencies.md`. No Go, no
container, and no figure read out of `docs/bench/`, which DEV-1 was writing
throughout.*

— PM-1, 2026-08-05

---

## 9. The gate run §8 item 1 asked for — run, quoted, and green

*Added by the orchestrator, 2026-08-05, against §8's first outstanding item and
C-33(a)'s falsifier. §8 named the orchestrator as one of two acceptable owners.*

**Tree: `99b769be`.** Every run below is against a `git archive HEAD` export
under `/tmp/ci-fresh`, not the worktree — the checkout the workflow actually
makes, with **no `bench/fixtures/*/ticks.jsonl`**, which is the condition
C-33(a)'s falsifier specifies and the one no previous run had met. Verified
rather than asserted: `git archive HEAD | tar -x` into a second directory diffs
clean against the tested export except for the compiled binaries the run itself
left behind.

| # | What ran | Where | Result |
|---|---|---|---|
| **1** | `bash ci.sh` | `dis-gotth-live:latest`, repository root mounted, **fixtures absent** | **EXIT 0.** `every gate this invocation could run is green`; `FAILED:` empty |
| **2** | client runtime suite, incl. the no-eval scan (NFR-4) | `dis-gotth-live-bench:latest` | **EXIT 0**, 6 of 6 `client/test/*.test.mjs` |
| **3** | browser conformance (FR-25…FR-28, FR-30…FR-32, NFR-7) | `dis-gotth-live-bench:latest`, `GOTTHLIVE_E2E=1`, `-ginkgo.fail-on-empty` | **EXIT 0**, 24.8 s — the 22 + 3 specs step 1 announces as skipped |

Steps 2 and 3 are the two the library image structurally cannot run, and they
are quoted here because "`ci.sh` exits 0" is not the same claim as "the gate is
green" while the script itself is printing two loud skips. `-ginkgo.fail-on-empty`
is what makes step 3 evidence rather than a tautology.

**The three bench steps, with the counts C-33(b) now makes observable:**

```
==> bench/apps/counter/gotth      0 of 49 specs skipped, per Ginkgo's own report
==> bench/apps/chat/gotth         1 of 62 specs SKIPPED, per Ginkgo's own report
==> bench/apps/dashboard/gotth    4 of 88 specs SKIPPED, per Ginkgo's own report
```

**§8 item 1 asked for "the two suites reporting non-zero `Skipped`". There are
two, and the third reports zero — which is the finding.**

### 9.1 C-33(b) is CLOSED, and running it found a third defect in the sentence

§2's row for C-33(b) is superseded. `99b769be` replaces the predicted sentence
with Ginkgo's own JSON report, written by the run being described. L9-1 named
two defects; the run named a third.

| | the old sentence | measured |
|---|---|---|
| 1. it was a **prediction** announced as an observation, computed from `[ -f … ]` before the suite ran | — | closed: the count is now read out of the report the run wrote. `go test` without `-v` discards a passing package's output, which is why the report file is the mechanism and not a parse of the log |
| 2. its **scope was narrower than the truth** — `dashboard_test.go`'s element-and-SVG-bounds spec needs the same fixture and is not a §2.5 digest spec | claimed "the §2.5 digest specs" | dashboard skips **4**, and the step no longer characterises which |
| 3. **it was printed for the counter, which has no fixture-reading spec at all** | claimed skips | **0 of 49.** The counter had been asserting a skip it has never had — the same shape as C-33(a), one directory out |

L9-1's §2.3 point 1 said the three-app loop *"was generalised from a green sample
of one"*. Point 3 above is that observation reaching the notice as well as the
guard.

One residual, recorded rather than left to be discovered: Ginkgo overwrites the
report file once per suite, so a module that grew a second test package would
report only the last. `go list` counts the packages that have tests and a second
one prints a notice instead of a quietly partial count.

### 9.2 What this does and does not discharge

- **§8 item 1 — DISCHARGED.** Checkpoint 3 may now say the gate is green, at
  `99b769be`, under the conditions quoted above.
- **§8 item 2 — DISCHARGED**, its only missing piece having been item 1's run.
- **§8 items 3 and 5 (QA-2 at HEAD, L9-1 on X3) — UNTOUCHED.** A green gate is
  not a QA-2 sign-off and is not an L9-1 ruling. **Two of the three outstanding
  items remain, and they are the two with real work behind them.**
- Nothing here bears on §8 item 6, the equivalence-spec freeze inconsistency.

*The runs took ~10 min (step 1), ~30 s (step 2) and ~25 s (step 3) on the
32-core host, after DEV-1's measurement campaign had finished and released it.*

— the orchestrator, 2026-08-05

---

## 10. This ledger's own open rows, answered at the gate

*Added by PM-1, 2026-08-05, with [`docs/gates/checkpoint-3.md`](../gates/checkpoint-3.md).
§0 says a ledger that is wrong is worse than none, because the next reviewer
relies on it — which applies to this document after its own items move as much as
it applied to the fifty commits it opened.*

**§8's three outstanding items are all discharged, and none of them by me.**

| §8 item | Answered by |
|---|---|
| 1. `bash ci.sh` at HEAD, exit 0, quoted | **§9 above** — the orchestrator, at `99b769be`, on a fixture-less export. §2.1 of the gate report says what has and has not been re-run since, and does not claim a green run at HEAD |
| 2. QA-2 re-verification at HEAD, naming the §R8 rows the change set could move | **QA-2**, `docs/qa/checkpoint-3-chaos.md` §R12–§R18. PASS at `1864cf92`; one row moved and was attributed by mutation. **C-35(c) discharged** |
| 3. L9-1 rules on X3 | **L9-1**, ADR-001 §7.2. **ADOPTED at 13,759 B**, against the settled campaign as this ledger asked, with the write buffer as a fifth line and both departures ruled |

**§7's five findings, answered.**

| | State at the gate |
|---|---|
| **§7.1** the equivalence spec calls itself a draft while four documents call §2 frozen | **Resolved as far as PM-1 can resolve it.** L9-1 signed in review §10.6; **PM-1's signature is gate report §8.3**, given there because QA-2 owns the file and was editing it. The status line moves when QA-2 applies it |
| **§7.2** REV-DUP's summary table stale for D-4 | Unchanged. Carried, one row, whoever lands the next REV-DUP commit |
| **§7.3** `instrumentation.md` asserts a deletion that has not happened | Unchanged. Carried as REV-DEL 3, DEV-1; L9-1's §10.4 forbids the claim meanwhile |
| **§7.4** BR-7 step 2's named detector cannot detect the case it was named for | Unchanged. Carried, DEV-1, and it is the qualification on **CP1-06** in the gate report §4.2 rather than a row hidden under a tick |
| **§7.5** `d66e4953` changed the root `go.mod` and no obligation was re-quoted | **Closed.** `docs/dependencies.md` **§5.4**: `go list -m all` is **62**, not 61, measured by the orchestrator in `dis-gotth-live:latest` against a `git archive` export; the direct-require set is published; §5.4.2 names the three parts still owed rather than letting the section look complete. **§1.4's D1 condition 1 is answered in part and filed as a question with L9-1**, not decided by PM-1 |
| **§7.6** the commit-footer convention | Unchanged. Gate report §10.2 — adopt it or drop it, rather than drifting a third time |

**And one row of §2 is superseded by the gate rather than by a commit.**
**C-42** is CLOSED: the distinguishing test for ruling 1 is in the PRD §9
preamble, against §6.1.2 by name, with v0.6 rows 1 and 5 written up as a strike
that passes it and a narrowing that was refused. This ledger said it *"should not
survive the gate report"*. It did not.

— PM-1, 2026-08-05


---

## 11. The Phase 3 exit gate act — 2026-08-05, and this ledger's last open phase row

*Added by PM-1 with [`docs/gates/checkpoint-3.md`](../gates/checkpoint-3.md)
**§12**, which is the act itself. This section records it here because §0 says a
ledger that is wrong is worse than none — and a closure ledger whose project has
exited the phase it was written for, and which does not say so, is wrong in the
one direction that matters.*

**Phase 3's seventeenth exit criterion — the dashboard's resync cost — is MET,
and PHASE 3 EXITS. Seventeen of seventeen.**

| | |
|---|---|
| **Remedy** | **DEV-3**, `1b16f4a9`, one file, both halves in one landing |
| **Gate act** | **PM-1**, tree `713a3192`, [`checkpoint-3.md` §12](../gates/checkpoint-3.md) |
| **How it was held** | By **running the measurement six times** — three of them on a pristine `git archive HEAD` export — and diffing the program's stdout against the README's own fence, plus the four suites that hold the behaviour. Not by reading the commit message. The sixth run is at **`2ab18690`**, which landed *during* the act and re-encodes `data-gotth-on`: the one class of change that could have moved the bytes, and they did not move |
| **Result** | Every published **byte** figure identical, 101 commits after it was taken, and again one commit further on. The **latency** line did not reproduce and the README says in advance that it will not; §12.3 gives that its own section rather than a footnote |

**What this does to this ledger's own scope, stated plainly so nobody has to
infer it.** The header row says *"Nothing here ticks a PRD box"*, and that is
still true of §§1–10: **this section ticks nothing either.** The box is ticked in
[`docs/PRD.md`](../PRD.md) §6 and the tick is applied by **PRD v1.4**, in the same
landing as this section. What is recorded here is that the phase this ledger was
written under has now exited, so a future reader does not open a fifty-commit
condition ledger and reason from a phase state that stopped being true.

**What is unchanged.** Every count in §1 — 39 CLOSED, 15 OPEN, 1 RULING OWED, 3
DELIBERATELY NOT FIXED across 58 rows — is untouched, and none of those rows is
this box: the resync figure was never a C-, BR-, U-, REV-DEL or REV-DUP row. It
was a **PRD exit criterion**, which is why it outlived the ledger and had to be
closed by a gate act rather than by a diff. **C-41 / D-31 is still OPEN** and is
still L9-1's; the client's `RATE_LIMITED` resync retry schedule being
protocol-visible and undocumented is a different fact about resync from the one
this act settles, and nothing here touches it.

**One reading to refuse before it happens.** This act does not re-open, re-grade
or ratify anything else in this ledger, and it is not a second signature on
QA-2's PASS or on L9-1's approval. It closes one box, on evidence, and the box it
closes is the one this project chose to leave open on a day when ticking it would
have cost nothing.

— PM-1, 2026-08-05
