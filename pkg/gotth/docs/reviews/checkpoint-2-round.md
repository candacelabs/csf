# Checkpoint-2 round — L9-1 review and rulings

| | |
|---|---|
| **Reviewer** | L9-1 (Principal Engineer) |
| **Date** | 2026-08-04 |
| **Reviewed** | the three-suite round's six landings, against `9d44742e` |
| **Ruled on** | D-12 second half, D-18, F-4, `instrumentation.md` §3 vs. the shipped tracer |
| **Ruled against** | [PRD](../PRD.md) v0.4 FR-23, FR-33, FR-36, FR-65 · [instrumentation.md](../instrumentation.md) §3 · [protocol.md](../protocol.md) H-4 · [api-surface.md](../api-surface.md) · [dependencies.md](../dependencies.md) |
| **Prior rulings** | [module-init](module-init.md) (C-1…C-20) · [checkpoint-2 batch](checkpoint-2-batch.md) (C-21…C-26, addendum C-27) |
| **Disposition** | **six landings accepted; four rulings; five conditions C-28…C-32; I do NOT veto the checkpoint-2 gate** |

```
Landings reviewed: 6, accepted: 6, rejected: 0
Rulings: 4 issued, 0 deferred
  1. D-12, second half        AFFIRM  — PM-1's connected-graph reading stands
  2. D-18                     CHANGE  — the bound goes on the emit path, and
                                        the flush trigger stops lying about it
  3. F-4                      RE-ADD  — live.IsRetryable; 45 -> 46
  4. instrumentation §3       AMEND   — all three PM-1 claims confirmed by
                                        measurement; the doc moves, not the code

Conditions opened: C-28…C-32          Blocking the gate: none
Defects only running could find, this round: 2 (C-28, C-30)
Files I did not touch, by rule: any .go, .js, .templ; docs/qa/; gotth-live/bench/
```

**Measurement hygiene.** QA-1 is working in this checkout concurrently. Following
the precedent I set in the C-27 addendum, **every number below was taken against
a pristine `git archive HEAD` export of `9d44742e`** in `/tmp`, never against the
live worktree. Probe code, mutations and scratch consumer modules were written
into that export or into separate scratch modules; nothing in the worktree was
edited. Where a mutation is quoted, it was applied to a fresh copy of the export
and the copy discarded.

**One environment note before the table.** The brief's invocation
`docker run … bash -lc '…'` **silently removes the toolchain**: `/etc/profile`
resets `PATH` and the image's `ENV PATH=/go/bin:/usr/local/go/bin:…` is lost, so
`ci.sh` runs to completion reporting eleven failed gates, all of them
`go: command not found`. Every measurement below uses `bash -c`. This is not a
repository defect — the GitHub workflow uses `bash ci.sh` — but it is a trap, and
it is recorded in §7.

---

## 1. What I verified by running it, not by reading it

| # | Check | Command | Result |
|---|---|---|---|
| **1** | The whole gate | `bash ci.sh` in `dis-gotth-live:latest`, repository root mounted | **exit 0**, *"every gate this invocation could run is green"*. FR-7 **ran** (protobuf + templ). One announced skip: the node suite. `live` **45/45** identifiers, **49/49** fields; `live/livetest` 3/9 and 0/6. Client runtime **9,143 B** minified / **3,874 B** gzip against NFR-2's 12,288 B — 68.5 % headroom |
| **2** | The conformance suite **with a browser** | `go test ./test/internal/conformance/` in `dis-gotth-live-bench:latest` (Chromium **151.0.7922.71**) | **Ran 142 of 154 — 142 passed, 0 failed, 1 pending, 11 skipped.** The pending one is D-15's `PIt`, held as promised |
| **3** | **The same suite in the image CI actually uses** | the same command in `dis-gotth-live:latest` | **Ran 123 of 154 — 30 skipped**, exit 0, *silently*. The delta is **19 specs**: this round's entire browser evidence. See §7.1 → **C-28** |
| **4** | The node client suite | `node --test client/test/*.test.mjs` in the bench image | three files, **exit 0** each, including the D-15 `todo` whose assertion prints and does not fail the run |
| **5** | **FR-33's 34 modules, independently** | `go list -m all` on the export; then `go get chi`, then `go get gin`; then restored | **61 → 62 → 95**, restored to **61**. chi is +1, gin is +33, **+34 together**, and the shipped root module is unchanged. `test/routers`'s own graph is 96. The ledger's number is exact |
| **6** | **O-1: an unlisted `.templ`** | added `examples/counter/unlisted.templ`, ran `gen.sh --check` | **exit 1**, naming the file in a listed-vs-found diff |
| **7** | **O-1: a drifted committed output** | changed one token inside a string literal in the committed `examples/chat/view_templ.go` | **exit 1**: *"examples/chat/view_templ.go is not what this generator produces"*. Before `84fed635` this was exit 0 |
| **8** | **D-18, reproduced and bounded** | a probe emitting one event whose `Contributing` holds *n* identifiers, on **default** limits, no coalescing | n=1022 → 1 patch, 0 errors. n=1023 → 1 patch. **n=1024 → 1 patch** (DEV-1's write-up did not have this row). **n=1200 → `patches=0 errors=1`**, `Error{INTERNAL}` non-fatal, and the effect's `emit` returned **`nil`** — the application is told nothing |
| **9** | **D-18's real cost: the metric lies** | same probe, reading `obstest.Metrics` | `gotthlive_outbound_validation_failed_total` = **1**, driven entirely by application input. Its own registration says *"Any non-zero value is a library bug."* Three other sites say the same. See §4 |
| **10** | **instrumentation claim 1 — `authorize` is a root** | span-graph probe over the real handler | `gotthlive.authorize` **parentValid=false**. §3.1 draws it as a child of `gotthlive.event`; the real edge runs the other way, as a link *from* event *to* authorize |
| **11** | **instrumentation claim 2 — effect spans nest** | same probe, with an `Execute` that deliberately outlives the transition | `gotthlive.effect.probe.watch` **child-of `gotthlive.event`, links=0**. §3.1 says effect spans are *"**linked**, not nested"* |
| **12** | **instrumentation claim 3 — five spans start nowhere** | `grep` for every `Span*` constant's call sites, confirmed against the probe's recorded set | started: `event`, `authorize`, `encode`, `origin`, `client.morph`, `effect.*`. **Not started: `parse`, `reduce`, `render`, `render.fragment`, `send`.** `gotthlive.render.fragment` is not even declared as a constant |
| **13** | **The same path under a REAL OTel SDK** | a scratch **consumer** module (`replace` onto the export) with `sdktrace` + `tracetest.SpanRecorder`, `AlwaysSample` | **3 distinct TraceIDs.** `authorize` alone in one; `event` + its `encode` + its `effect` in a second; the mount `origin` + its `encode` in a third. The link from `event` to `authorize` is **cross-trace** (`sameTrace=false`) |
| **14** | **C-30, the finding: the documented default sampler destroys the graph** | 300 full interactions under `ParentBased(TraceIDRatioBased(0.05))`, instrumentation §3.5's stated default | `authorize` sampled **11/300 (3.7 %)**, `event` **11/300**, `effect` **11/300** (inherited), `origin` 19/300, `encode` 30/300 (= 19+11, exactly). **Interactions recording BOTH `authorize` and `event`: 0 of 300 (0.00 %).** Expected joint rate 0.25 % |
| **15** | `client.morph` is a third independent root | probe sending `ClientTelemetry` | `gotthlive.client.morph` **parentValid=false, links=1**. Three roots on the event path, three independent sampling decisions |
| **16** | **F-4: the workaround cannot fail** | replaced chat's `live.Retryable(fmt.Errorf(…))` with a plain `fmt.Errorf("…: %w", …)` — the mark **gone** | `examples/chat` suite: **ok**, green. The spec whose message is *"a saturated mailbox is transient, so the pump must have wrapped it with live.Retryable"* passes when the pump did not mark it at all. The mutation is behaviour-changing: `chat.go:511` re-subscribes only on `retryable == true` |
| **17** | **The browser suite is NOT vacuous** | removed the `preserved()` guard from **both** `morphNode` and `morphEl`, rebuilt through `tools/minify` into the served bundle | **FAIL — 140 passed, 2 failed**, in exactly the right two places: `dom_preservation_test.go:1086` (FR-27, *"morph rewrote the inside of a `data-gotth-preserve` element"*, got `<b>server 2</b>`) and `htmx_test.go:639` (FR-32/R-11). The evidence chain is real |
| **18** | The two guards are individually redundant | the same mutation applied to **one** guard at a time | Both single-guard mutations are **survived**, because whichever guard remains covers the other's path. Not a defect; a note on mutation method (§7.4) |

**Not measured, and why.** I did not attempt Firefox, Safari or a second Chrome
channel: QA-1 measured the obstruction (`firefox-esr` speaks WebDriver BiDi,
`cdp_test.go` speaks CDP only) and the images must not be rebuilt this round, so
re-running it would add a second identical negative result and no information.
NFR-7's coverage is a scope decision, and it is PM-1's — §7.5. I did not re-run
the C-27 browser battery: nothing this round touches `normalizeMount` or
`Script`'s writer, and I have `live`'s specs green over them. I did not measure
`bench/`: still untracked WIP, still not mine. I did not measure the memory
figure G2 wants — it is a Phase 1 debt with an owner (DEV-1 + QA-2) and no work
this round moved it. And I did **not** re-derive D-14's 1023 from scratch: check
8 exercises the same ceiling from the other side and agrees with it, and DEV-1's
boundary table is reproducible from the committed conformance spec.

---

## 2. The six landings, judged

**All six are accepted.** Collectively they add **zero exported identifiers** —
`live` is 45/45 and 49/49 before and after — which is the right answer to FR-65
for a round whose whole content is test coverage and one validation. I want that
noted because it is unusual: three suites, two defect fixes and a generator gate
landed without spending a single symbol.

### 2.1 FR-33, the three-router mount suite (`ee476ed4`, `8aea799b`)

**Accepted, and the separate module is the right call.** I measured the number
rather than taking it: chi is +1 module and gin is +33, so the suite's own
`go.mod` keeps **34 modules** out of every consumer's build list, and the root
module is still 61 (check 5). Go resolves requirements at module granularity, so
a test-only *import* is not a test-only *dependency* — `docs/dependencies.md` §2
already had to correct itself on exactly this, and the suite is the correction
applied. The second reason in `test/routers/doc.go` is the better one and is
easy to miss: a package under the module path would be counted by
`internal/arch`'s two-exported-package assertion (C-12/C-20), and widening that
assertion to let tests through would weaken the thing it exists to catch. That is
the right instinct — **do not weaken an invariant to accommodate a test**.

The suite satisfies C-23 as written: three distinct prefixes, `/live`,
`/app/live`, `/ui/gotth`, two of which are not `/live`. It goes past what I
required in the one way that matters — property 3 opens a **live session**
through each prefix and drives an event to a patch, rather than proving a static
file is reachable. A mount test that only fetches the runtime proves a `ServeMux`
matched a suffix.

`mount_test.go:52` deserves a line of praise, because it is the shape I keep
asking for and rarely get:

> *"Removing the second line does not make this spec fail today — `normalizeMount`'s `//` clause is what keeps the input away — which is exactly why it is here: it is the assertion that survives that clause being removed."*

An assertion whose comment states that it is currently redundant, and why it is
kept anyway, is a defence against the next person deleting it. C-27 §A.6.3(4) is
discharged.

**One thing the suite pays for twice, already numbered.** `wire_test.go` is a
second hand-rolled `protowire` frame reader, after `examples/chat`'s. That is
F-1, correctly routed, and I am not re-opening it — but I will say plainly that
**F-1 is now the most expensive open item in the project**: `live/livetest`'s
ledger ceiling is 9, measured is 3, and the gap has cost roughly 550 lines of
duplicated wire decoding across two modules. It should be scheduled, not queued.

### 2.2 The DOM-preservation browser suite (`4de374d8`, `a9416e69`, `3c9a9a2d`)

**Accepted, and it is the strongest artifact of the round.** I asked whether the
node DOM shim now models the browser or merely agrees with itself. It models the
browser, and `3c9a9a2d` is the proof: `details.open` was a plain property beside
`checked` and `selected`, and the asymmetry runs the other way round —
`input.checked` is *checkedness*, a state distinct from its attribute;
`details.open` is a plain reflected IDL attribute. Making the shim reflect turned
a **green node test that Chromium contradicts** into a `todo` carrying D-15's
reason. That is exactly the C-27 addendum's rule applied by someone else, without
being told: when the value under test is something a browser parses, the oracle
is the browser, and a shim's job is to lose the argument with it.

I mutation-tested the suite rather than trusting it (check 17). With the
`Preserve` guard removed from both morph entry points and the bundle rebuilt
through `tools/minify`, the suite goes **red in exactly the two right places**,
with messages that name the requirement and print the wrong value. It tests the
*served* artifact, not the source. That is not a vacuous suite.

D-15 is held as a `PIt` with a browser reason, and the requirement stays
executable rather than being edited to fit. Correct handling; the item is DEV-2's
and I am not ruling on the fix here.

### 2.3 HTMX coexistence (`c4f937ed`, `3269a6aa`)

**Accepted.** The vendoring is the part worth singling out. `testdata/README.md`
records package, version, publication date, size, SHA-256 and licence, names
three independent origins that produced byte-identical files, cross-checks the
tarball against the registry's own integrity value, and — the part that makes it
matter — **`htmx_test.go` re-checks the digest on every run**. Provenance that is
enforced rather than documented is the difference between a supply-chain claim
and a supply-chain wish, and this is the first time this project has had the
former. NFR-9 and FR-74 are untouched: no `go.mod`, no `package.json`, no image
change.

D-16 (markup a morph *inserts* is inert until `htmx.process`) is correctly
identified as the seam, and correctly measured against its good half — a control
that already existed keeps working through two morphs with no processing. That
contrast is what makes it a documentation gap rather than a bug. Routed, not
ruled.

### 2.4 D-14's validated `Limits` range (`2dfed02d`)

**Accepted. It rejects at the right boundary and in the right place.**

*The right boundary.* 1023, not 1024, and the difference was found by running the
repro across it rather than by reading the constant. `MaxCoalesceFlushAt` carries
the measured table rather than a claim, which is the correct home for a number
whose justification is arithmetic plus an experiment. A fix written against the
round number would have shipped the defect one value narrower — DEV-1 says so in
the commit message, and being that specific about a near-miss is behaviour I want
repeated.

*The right place.* Rejecting in `New` rather than clamping in `Normalize` is
right, and the argument DEV-1 gives is the one I would give: a clamp makes the
running limit differ from the configured one, so an operator reading their own
configuration reads a number that is not in force. It is the same ruling I made
on `normalizeMount`, applied by analogy without being asked, and cited.

*The right scope.* Negatives rejected across the whole struct; upper bounds not
invented. `CoalesceFlushAt` is the only field with a *protocol* ceiling behind
it; capping `MailboxDepth` would be deciding an operator's capacity for them.
That asymmetry is defensible and is defended in the godoc. Completeness held by a
reflection-walking spec rather than by review is the right mechanism — the same
one `protocol`'s H-4 table uses.

*Zero exported identifiers.* The bound is documented, not exported. Consistent
with C-27 and with FR-65.

**What it does not close is bigger than what it closes, and DEV-1 said so.** See
§4.

### 2.5 FR-58's effect-panic causal ID (`8fb6ade9`)

**Accepted.** One field, `scheduled_by`, at a site that already held it as a
parameter. The godoc's reasoning is right on the detail that usually goes wrong:
it is emitted **unconditionally, including the zero** the server's own
transitions carry, *"because a field that appears only sometimes cannot be
queried for"*. A log field that is present conditionally is a log field an
operator cannot write a query against, and zero already means "no event caused
this" everywhere else in this stream. That is a correct instinct about
operability, not about tidiness.

### 2.6 O-1, `gen.sh` templ coverage (`84fed635`)

**Accepted, and the enumeration-plus-walk shape is the right answer to a question
this project has now got wrong once.** FR-7 named `gen.sh --check` as the gate
for byte-reproducible generated code, and half the generated code in the
repository — two `_templ.go` files, written by `templ generate`, committed — was
outside the list. I confirmed both halves of the fix independently (checks 6 and
7): an unlisted `.templ` fails by name, and a one-token drift in a committed
output fails with the file named.

Two judgement calls I looked at and let stand. **Checking the templ CLI version
against each source module's own `go.mod`** rather than against a literal in the
script: correct, because a literal would be a third place to keep in step, and
`.dis/Dockerfile` already states the invariant with nothing enforcing it. Version
drift would eventually surface as a byte diff anyway — but as an *unexplained*
one, which is the failure mode of a gate nobody can act on. **Not passing
`-lazy`**: correct, and for the stated reason — it compares mtimes, and a fresh
checkout's mtimes arrive in an arbitrary order, so `--check` would compare the
committed file against itself.

The `ci.sh` FR-7 residual (the whole step skips when `research/` is absent, so
the templ half skips with the protobuf half although it needs neither) is
recorded rather than dropped. I agree it is not worth a third `gen.sh` mode. It
folds into C-28, which is about the same class of problem one level up.

---

## 3. Ruling 1 — D-12, second half: PM-1's reading is affirmed

**Ruling: AFFIRM. FR-36 carries the connected trace graph reading, as shipped in
PRD v0.4 (`5b6f086e`). I have read it and I am not moving it.** The literal
"one trace per event" reading is rejected, and rejected on the merits rather than
on convenience.

### 3.1 Why the connected-graph reading is right

I measured the alternative before affirming (check 13). Under a real
OpenTelemetry SDK the path is **three roots and two TraceIDs** on the event path
proper, joined by a **cross-trace link** from `gotthlive.event` to
`gotthlive.authorize`. To make it literally one trace, one of two things must
happen, and PM-1 rejected both correctly:

- **A parent edge on the morph.** The morph span's start timestamp is *derived*
  (server receive time minus a client-reported duration) and §3.3 says so
  explicitly. A parent edge asserts enclosure. Asserting enclosure over a
  fabricated start time is a lie a trace viewer will render as a fact, and it is
  worse than a link precisely because it looks more informative.
- **A `traceparent` on the wire.** 55 bytes per event against a 12,288 B budget,
  to buy browser-side context propagation no v1 requirement asks for. BL-17
  already holds it. Correctly deferred.

The three checkable properties PM-1 substituted are a genuine improvement on the
old sentence, and clause 2 is the one that earns the ruling: *"the actor's own
work MUST be true descendants of the transition span, not links"*. Without it a
graph of nothing but links satisfies clause 1, and a trace that asserts no
containment anywhere is not a trace. Measured, the one boundary where a parent is
possible does have one — `gotthlive.encode` is a true child of `gotthlive.event`
— and the conformance suite has a spec that fails when it is reparented.

Recording the five unstarted spans as **unmet rather than narrowing the
requirement to what shipped** is the right call and the one I would have insisted
on. A requirement edited down to the implementation stops being a requirement.

### 3.2 What I am not blessing, and what I found instead

PM-1 left the read-pump link open for me: *"Not blessed: the read-pump link,
which L9-1 may still rule into a real parent."*

**I am not ruling it into a parent, and the reason is not the one either of us
expected.** The link's *shape* is defensible — authorization runs on the read
pump, before the event reaches the mailbox, and clause 3 permits a link where a
parent edge would misdescribe a boundary. The problem is not the edge. It is that
**`gotthlive.authorize` is a root**, and a root makes its own sampling decision.

That is C-30, it is a new finding, and it is §6.3.

---

## 4. Ruling 2 — D-18: the bound goes on the emit path, and the flush trigger stops lying about it

**Ruling: CHANGE. Both, and the emit path carries the rejection.** The design
question DEV-1 put to me is *"emit path, flush trigger, or both"*. It is both, and
the two halves are not symmetric — one is about **attribution** and the other is
about an **invariant**. No exported identifier is needed for either.

### 4.1 What I measured that the write-up does not have

The write-up quotes n=1200. The boundary is more interesting (check 8):

| app-supplied `Contributing` | patches | errors | `outbound_validation_failed_total` |
|---:|---:|---:|---:|
| 1022 | 1 | 0 | 0 |
| 1023 | 1 | 0 | 0 |
| **1024** | **1** | **0** | **0** |
| 1200 | **0** | **1** `INTERNAL`, non-fatal | **1** |

1024 passes because `unionEdges` excludes the origin's own `EventID` and dedupes,
so the library's `scheduledBy` edge happened to collide with an identifier the
application had already listed. **The effective bound depends on whether the
application's identifiers happen to overlap the library's.** That is not a
boundary anybody can reason about, and it is the strongest single argument for
making the bound explicit and checked rather than emergent.

And the emit call returned **`nil`**. The application is told nothing: the emit
succeeded, the frame died later, on the actor goroutine.

### 4.2 The argument that decides the shape — the metric is now false

`gotthlive_outbound_validation_failed_total` reached **1** on default limits from
pure application input. Its own registration reads:

> *"Constructed frames that failed outbound validation. **Any non-zero value is a library bug.**"*

Three other sites say the same thing — `internal/obs/metrics.go:214`,
`internal/session/actor.go:570`, `internal/protocol/outbound.go:128`/`:149`
(*"this is a library bug, not a client problem"*). A fourth,
`internal/protocol/limits.go:47`, is stale in a second way as well: it says *"the
actor emits at half this ceiling"*, which after D-14 describes the **default**
and not the **bound**.

So today an application can page an operator with an alert that says *"this is a
library bug"* about its own configuration. That is worse than the dropped patch,
because it sends the person holding the pager to the wrong repository.

**A bound on the flush trigger alone cannot fix that**, and this is what settles
the question. The flush trigger runs inside the actor, long after the application
returned, with no way to name the caller. Only the emit path knows whose value it
is. So:

- **The emit path carries the rejection.** `appAdapter.Execute`'s emit closure
  (`live/app.go:224`) already rejects a non-zero `Event.ID`, a non-zero
  `Event.At`, and a `0` inside `Contributing`, each with an error naming what the
  application did wrong, delivered to the effect through the `Emitter` return
  that already exists. A length bound is a **fourth entry in a list of three**,
  not a new mechanism, and it turns a silent lost patch into a deterministic
  effect failure the reducer can handle — which is the contract `820752f6` built.
- **The flush trigger carries the invariant.** `mustFlush` compares
  `len(a.pendingIDs)` against `CoalesceFlushAt`, but `deferPatch` folds an
  application's `Contributing` into `pendingIDs` too, so the trigger's "+1"
  arithmetic — exact for the edges the library adds, as `MaxCoalesceFlushAt`'s
  comment correctly says — is **not** exact once an application contributes. A
  legal per-event bound plus a legal `CoalesceFlushAt` can still overflow at
  flush. The trigger must be evaluated against **what the frame will actually
  carry**, not against a proxy for it.

### 4.3 What I am explicitly not requiring

**Not truncation.** Dropping identifiers to fit is the failure `CoalesceFlushAt`
exists to prevent, reintroduced as a fix for it.

**Not an exported constant.** D-14's precedent: documented in
`Event.Contributing`'s godoc, derived in `internal/`. `live` stays at 46 after
§5's re-add and gains nothing here.

**Not a protocol change.** Splitting one patch's provenance across frames is a
wire change for an input an application should not be producing.

**Not a `Limits` field.** This is a per-event bound flowing from H-4, not an
operator's capacity decision. Making it configurable would let an operator
configure their way back into the defect.

→ **C-31.**

---

## 5. Ruling 3 — F-4: `IsRetryable` is re-added

**Ruling: RE-ADD `live.IsRetryable(error) bool`. My §5.4 cut is restated as
wrong, and the trigger I pre-registered has fired.** `live` goes **45 → 46**
identifiers. Fields unchanged at 49.

### 5.1 The trigger fired, and the evidence is stronger than the referral

§5.4 pre-registered the re-add trigger as *"something needing to inspect an error
it did not itself produce"* and said it was re-addable in one PR if the chat
example found it. `FRICTION.md` F-4 reports it found it *at one remove* — the
consumer is the example's spec, not its production code — and is scrupulous about
that being a weaker case.

**It is a stronger case than F-4 claims, and I have the mutation to prove it**
(check 16). The workaround is `errors.Unwrap(err) != nil`, standing in for "the
mark is set". I replaced chat's `live.Retryable(fmt.Errorf(…))` with a plain
`fmt.Errorf("…: %w", …)` — the retryable mark **removed entirely** — and
`examples/chat`'s suite is **green**. The spec whose failure message reads *"a
saturated mailbox is transient, so the pump must have wrapped it with
live.Retryable"* passes when the pump did no such thing.

That is not a weak spec. It is a **spec that cannot fail for the reason it
states**, which is the D-13 class this project has now found four times. And the
mutation is behaviour-changing, not cosmetic: `chat.go:511` re-subscribes only
when `retryable == true`, so under the mutation a subscription that should
recover silently does not, and nothing goes red.

The reducer-side table at `chat_test.go:393` does not cover it, and cannot: it
builds its input from `live.EffectFailedRetryableField` with a literal `"true"`,
so it tests the branch, not the value the pump produced. That is precisely the
blind spot I recorded in the batch's §8 — *"any example spec that constructs its
input from the same constant its code matches on is testing the branch and not
the name"* — and here it is the same shape one level down, on the value.

### 5.2 Why `live.IsRetryable` and not `livetest.AssertRetryable`

F-4 offers both. I rule for the predicate, for three reasons in ascending weight.

1. **A predicate composes; an assertion helper does not.**
   `Expect(live.IsRetryable(err)).To(BeTrue())` works inside `Eventually`,
   `ContainElement`, a `DescribeTable` entry and a plain `if`.
   `livetest.AssertRetryable(GinkgoT(), err, true)` works in exactly one place
   and imports a second package to do it.
2. **The C-25 precedent does not transfer, and it is worth saying why so the
   asymmetry is not read as inconsistency.** `NewSession` went to `livetest`
   because a `Session` **constructor** in `live` is a forgery route reachable
   from any handler — the guard was doing real work. `IsRetryable` is a pure
   predicate over an error the caller already holds. There is nothing to guard
   against, so `testing.TB` would be ceremony.
3. **A set/get pair where only the setter is exported is a defect in its own
   right at a stdlib bar.** `live.Retryable` lets an application create a value
   whose classification it cannot then read back. That is a one-way door in the
   type system, and the only justification offered for it — mine — was "no call
   site". There is one. FR-65 asks whether a symbol earns its place, and the
   symmetric partner of an exported setter earns it at the lowest price any
   symbol can.

The composition hazard I recorded in §5.4 — *"an application that wants to test a
mark on an error travelling through its own helpers must track that itself"* — is
what has now bitten, one checkpoint later, exactly as written. The trigger was a
real trigger.

### 5.3 The spec, so nobody needs a second ruling

```go
// IsRetryable reports whether err carries the mark set by [Retryable].
func IsRetryable(err error) bool
```

Implemented over the same `errors.As` the internal `retryable` uses, so the mark
survives arbitrary wrapping — a predicate that only sees the outermost error
would be a different and worse function, and the existing internal one is already
right. `IsRetryable(nil)` is `false`. `IsRetryable(Retryable(nil))` is `false`,
because `Retryable(nil)` is `nil`.

**Specs required** (Ginkgo v2 + Gomega, per the standing convention): a marked
error, an unmarked one, `nil`, a marked error wrapped twice by `fmt.Errorf("%w")`,
and — the one that matters — a **plain `%w` wrap of an unmarked error**, which is
the input the current chat workaround cannot distinguish. `examples/chat`'s two
specs drop `errors.Unwrap` for `live.IsRetryable` in the same PR, and F-4 comes
out of `FRICTION.md`; a friction item documenting a missing feature must not
outlive the feature. Ledger §5.x gains a row and the count goes 45 → 46 by hand.

→ **C-32.**

---

## 6. Ruling 4 — `instrumentation.md` §3 versus the shipped tracer

**Ruling: AMEND the document in all three places. All three of PM-1's claims are
confirmed by measurement, and in the two where document and code disagree about
design, the code is right.** Plus one finding neither of us had, which is the
serious one.

### 6.1 The three claims, verified

I checked each against the running tracer rather than against the source
(checks 10–12), and then again under a real SDK (check 13).

| Claim | Verdict | Evidence |
|---|---|---|
| `authorize` drawn as a child of `event`, shipped as a **root** | **Confirmed** | `parentValid=false`. Under a real SDK it is alone in its own TraceID, and `event`'s link to it is cross-trace |
| effect spans described as *"linked, not nested"*, shipped **nested** | **Confirmed** | `child-of gotthlive.event`, `links=0`, with an `Execute` that outlived the transition span |
| eight spans drawn, five started nowhere | **Confirmed** | `parse`, `reduce`, `render`, `render.fragment`, `send`. `render.fragment` has no constant at all |

### 6.2 Where the document moves and where it does not

**The effect span: the code is right, §3.1 is wrong.** §3.1's stated reason for a
link is *"an effect may finish after the event span closes"*. That is not a reason
for a link. **OpenTelemetry does not require a child to end before its parent** —
async work outliving its parent span is ordinary and is what parent edges are for.
A parent edge here asserts something true (this effect was scheduled by this
transition) and gives an operator a tree; a link would assert the same thing more
weakly and cost a root. Strike the sentence and draw the effect as a child, noting
that it may outlive its parent and that this is permitted. PM-1's amended clause 3
already blesses the direction: *"removing a link by making the edge a real parent
is always permitted."*

**`authorize`: the drawing is wrong in both direction and shape.** It is not a
child of `gotthlive.event`; it precedes it, and the shipped edge is a link running
from `event` **to** `authorize`. Redraw it as a root, with the link, and keep the
code comment's explanation — which is good, and better than the diagram it
contradicts.

**The five unstarted spans: nothing to amend, and nothing to soften.** PRD v0.4
already records them as unmet, with the sites named. §3.1 must gain a marker
saying which boxes are drawn-but-unimplemented, because a reader consulting §3.1
today is told eight spans exist and finds three. Do **not** delete the boxes: the
requirement stands.

**One thing to fix while in the file.** `gotthlive.render.fragment` is drawn and
is not declared anywhere. Either declare the constant with the other nine, or say
in §3.1 that it is unnamed. A span name that exists only in a diagram cannot be
grepped for, which is how it stayed missing.

→ **C-29** (documentation only).

### 6.3 The finding neither claim contains: three roots, three sampling decisions

**This is the round's serious observability defect, and it is only visible by
running.** It is not a documentation gap and C-29 does not cover it.

Measured (checks 10, 13, 15): the event path has **three root spans** —
`gotthlive.authorize`, `gotthlive.event`, `gotthlive.client.morph`. A root span
consults the root sampler; `ParentBased` follows a parent's decision and **does
not look at links**. So under instrumentation §3.5's stated default,
`ParentBased(TraceIDRatioBased(0.05))`, those are **three independent 5 %
decisions**.

Over 300 real interactions (check 14):

```
gotthlive.authorize   11/300   (3.7 %)
gotthlive.event       11/300   (3.7 %)
gotthlive.effect.*    11/300   (3.7 %)   — inherited from event, exactly
gotthlive.origin      19/300   (6.3 %)
gotthlive.encode      30/300  (10.0 %)   — 19 + 11, exactly

BOTH authorize AND event in the same interaction:   0 of 300   (0.00 %)
```

The arithmetic closes on itself, which is what tells me the measurement is
sound: `encode` is emitted once per origin and once per event, and 19 + 11 = 30.

**Why this matters more than it first looks.** PM-1's amended FR-36 clause 1 says:

> *"Every span on the path MUST be reachable from the transition span by following parent and link edges. A span on the path that is reachable from nothing is a defect, **not a sampling artefact**."*

That sentence asserts the two can be told apart. Measured, at the project's own
documented default they **cannot**: 95 % of the time `authorize` is reachable
from nothing because it was never sampled, and there is no way to distinguish
that from the defect the clause is written to catch. FR-36's structure is
verifiable in the conformance suite — which uses `AlwaysSample` and a recorder
that stamps one hard-coded TraceID on everything (QA-1's D-11) — and unverifiable
in every deployment.

Worse, the two requirements are stated against **the same configuration and are
incompatible in it**: PRD v0.4 makes NFR-1's gate *"the default 5 %-sampled
configuration"*, and at 5 % sampling FR-36's connected graph occurs once per
8,000 events with the morph span included. This is not exotic —
`ParentBased(TraceIDRatioBased(x))` with `x < 1` is *the* standard production
sampler.

**The fix I would take, stated so the condition is actionable.** Make the event
path **one sampling decision**, by making the roots into children:

- `gotthlive.event` becomes a **true child of `gotthlive.authorize`**, using the
  `SpanRef` the ingress already carries, instead of linking to it. This is the
  truthful causal direction (authorize precedes the transition), it removes a
  link site — always permitted under clause 3 — and it is free: the reference is
  already stored and already crosses the goroutine boundary. That `authorize`
  has ended by then is not an obstacle; a parent is a `SpanContext`, not a
  lifetime.
- `gotthlive.client.morph` is the harder half and I am **not** pre-deciding it.
  §3.3's argument against a parent edge is real — the start timestamp is derived
  and a parent asserts enclosure over it. Options are a parent edge with the
  derived-timestamp caveat kept in the attributes; or a documented, deliberate
  acceptance that morph timing is sampled independently. Either is defensible;
  what is not defensible is the current state, where the choice was never made
  and its consequence was never measured.
- **§3.5 must state the interaction.** It currently discusses sampling only
  against NFR-1's overhead budget and G4's provenance guarantee, and says nothing
  about what sampling does to the trace *structure* §3.1 promises. That omission
  is how this survived two reviews.

Whichever way the morph goes, the number to publish is the one above: **0 of
300** today.

→ **C-30.** Owner **DEV-1** for the mechanism, **PM-1** for whether FR-36 clause
1's *"not a sampling artefact"* sentence survives contact with it.

---

## 7. What I found outside the referred set

### 7.1 The three new browser suites run in no CI job — C-28

**This is the round's other only-by-running finding, and it is the one that bears
directly on the gate.**

| Image | Specs run | Skipped | Exit |
|---|---:|---:|---|
| `dis-gotth-live-bench:latest` (browser present) | **142** of 154 | 11 | 0 |
| `dis-gotth-live:latest` — **what `ci.sh` and the workflow's `library` job use** | **123** of 154 | **30** | **0, silently** |

The 19-spec delta is precisely this round's DOM-preservation and HTMX evidence.
`browserOnly()` calls `Skip` when `CHROME_BIN` is unset, `go test ./...` swallows
it, and `ci.sh` prints **"every gate this invocation could run is green"** while
30 specs did not run. Meanwhile the workflow's `client` job *has* the browser and
runs only `client/test/*.test.mjs` plus `chromium --version`. **Neither job
executes the browser suites.**

What makes this a defect rather than an oversight is that `ci.sh` already solves
exactly this problem, well, one line away. It announces the node-suite skip
loudly, with the command to run it, and its own header explains why:

> *"Two steps need more than the module directory and are skipped with a loud notice when they cannot run, rather than passing quietly."*

The browser skip gets none of that treatment. And `test/routers/go.mod`'s own doc
names the failure mode in the same words: *"A suite in a module nothing invokes is
a suite that is green because it never ran."* Here it is one **image** out rather
than one directory out.

The suites are correct — I ran them and I mutation-tested them (checks 2, 17).
The defect is that nothing will tell anyone when they stop being correct.

### 7.2 D-17, confirmed and worth more than LOW

`live/app.go:152` routes the embedded runtime by
`strings.HasSuffix(r.URL.Path, clientRuntimeFile)`. DEV-1 numbered it LOW on the
ground that the file is public and immutable. I agree it is not a disclosure. I
disagree slightly on severity, for one reason DEV-1 half-states: the artifact
ships `ETag` plus `max-age=31536000, immutable`, a design that assumes **one URL
per artifact**, and a suffix match gives an unbounded key space for one 9 KB
body. A CDN or caching proxy in front of the app can be made to store the same
9 KB under arbitrarily many keys by anyone who can issue requests. That is a
cache-pollution primitive, not a disclosure, and it costs one `==` to remove.
Left with DEV-1 at its existing number; I am recording the reason it should be
scheduled rather than parked.

### 7.3 The `bash -lc` toolchain trap

`dis-gotth-live:latest` sets `PATH` via `ENV`. A **login** shell re-runs
`/etc/profile` and resets it, so `bash -lc 'bash ci.sh'` produces a full-length
CI run in which every Go step fails with `command not found` and the script
correctly reports eleven failures — which reads exactly like a broken tree. CI is
unaffected (`bash ci.sh`, no `-l`). Worth one line in `.dis/Dockerfile`
(`/etc/profile.d/`) so the image is robust to how it is invoked, and worth
correcting in whatever brief propagates `-lc`. **Nit, DEV-1.**

### 7.4 A note on mutation method, for QA-1

The `Preserve` guard exists at **two** morph entry points, and either alone covers
the other's path (check 18). Both single-guard mutations survive the full suite;
only removing both goes red. That is redundancy, not vacuity — but a mutation
campaign that changes one call site at a time would report "the suite does not
cover `Preserve`" and be wrong. When a property is defended in two places, the
mutation has to remove both, and the campaign should record which mutations were
compound and why.

### 7.5 NFR-7 at 1 of 8 — routed, not ruled

The suites exist and pass on Chromium 151. The matrix does not. QA-1 measured the
Firefox obstruction rather than estimating it, which is the right way to report a
negative. **This is a scope decision and it is PM-1's**, and PM-1 already has it.
My only input: if it is amended, amend it to what CI can verify **and** land
C-28, because an NFR-7 narrowed to "one browser, in CI" is worth nothing while no
CI job runs that browser.

### 7.6 Nits

- `internal/protocol/limits.go:47` — *"the actor emits at half this ceiling"*
  describes the **default**, not the bound, since D-14. Folded into C-31.
- The orchestrator log's *"19 of 154 specs, 19 passed"* is a label-filtered run,
  not the suite total; the suite total is 142 of 154 with a browser and 123
  without. Worth stating that way in the gate report so the two numbers are not
  read as disagreeing.
- `docs/dependencies.md`'s 34-module figure is exact (check 5). Recorded because
  I checked it, not because it was in doubt.

---

## 8. Conditions

Numbering continues the chain. **None blocks the checkpoint-2 gate**; C-28 must
be *named in* the gate report.

| # | Condition | Owner | Phase |
|---|---|---|---|
| **C-28** | **The browser suites must run somewhere that fails.** Measured: `dis-gotth-live:latest` runs **123 of 154** conformance specs and skips **30 silently at exit 0**; the bench image runs **142**. The 19-spec delta is this round's entire DOM-preservation and HTMX evidence, and **neither GitHub workflow job executes it** — the `library` job has no browser, the `client` job has a browser and runs only the node suites. Add a workflow step running `go test ./test/internal/conformance/` in `dis-gotth-live-bench:latest`, and make `ci.sh` **announce the browser skip** the way it already announces the node one, with the command to run it — the header already promises that treatment for steps needing more than the module directory. While there: the FR-7 residual (the whole step skipping when `research/` is absent, taking the templ half with it) is the same class and should be named in the same notice even if it is not split. Prove it in the commit message by the route I used: quote both spec counts, and show the new step going red under a mutation that only a browser can catch. | DEV-1 + QA-1 | checkpoint 2; **named in PM-1's gate report** |
| **C-29** | **`instrumentation.md` §3 is amended to describe the tracer that shipped.** All three disagreements confirmed by measurement (§6.1). (a) §3.1 draws `gotthlive.authorize` as a child of `gotthlive.event`; it is a **root**, and the shipped edge is a link running from `event` **to** it — redraw, keeping the code comment's explanation, which is better than the diagram it contradicts. (b) §3.1 says effect spans are *"linked, not nested"* and gives *"may outlive the parent"* as the reason; measured they are **nested with zero links**, and the reason is not a reason — OTel does not require a child to end before its parent. **The code is right here**: strike the sentence, draw the effect as a child, note that it may outlive its parent and that this is permitted. (c) Five of the eight drawn spans — `parse`, `reduce`, `render`, `render.fragment`, `send` — are started nowhere; mark them drawn-but-unimplemented in §3.1 rather than deleting the boxes, since PRD v0.4 records the requirement as unmet. Also: `gotthlive.render.fragment` is drawn and declared nowhere — declare the constant or say in §3.1 that it is unnamed. Documentation only; no code change, no surface change. | DEV-1 | checkpoint 2 |
| **C-30** | **The event path is three independently-sampled roots, so FR-36 clause 1's connected graph does not survive the project's own default sampler.** Measured over 300 real interactions under `ParentBased(TraceIDRatioBased(0.05))` — instrumentation §3.5's stated default: `authorize` 11/300, `event` 11/300, and **both together 0 of 300**. `gotthlive.authorize`, `gotthlive.event` and `gotthlive.client.morph` are all roots; `ParentBased` does not consider links. PM-1's clause 1 says an unreachable span is *"a defect, not a sampling artefact"* — at the default the two are indistinguishable, and PRD v0.4 makes that same 5 % configuration NFR-1's gate, so two requirements are stated against one configuration and are incompatible in it. **Make the event path one sampling decision.** Concretely: `gotthlive.event` becomes a **true child of `gotthlive.authorize`** via the `SpanRef` the ingress already carries — the truthful causal direction, one fewer link site (always permitted under clause 3), and free, since an ended span is still a valid parent. The `client.morph` half is **not** pre-decided: either a parent edge keeping §3.3's derived-timestamp caveat in the attributes, or a documented decision to sample it independently — but the choice must be made and its consequence measured. `instrumentation.md` §3.5 must state what sampling does to trace **structure**, not only to overhead and provenance; that omission is how this survived two reviews. Publish the 0/300 figure and its replacement. | DEV-1 (mechanism) + PM-1 (whether clause 1's *"not a sampling artefact"* sentence survives) | checkpoint 2 for the decision; Phase 1 debt |
| **C-31** | **D-18: bound `Event.Contributing` on the emit path, hold the invariant at the flush trigger, and stop four sites calling an application's mistake a library bug.** Measured on **default** limits: 1024 identifiers pass, 1200 give `patches=0 errors=1`, a non-fatal `Error{INTERNAL}`, a state change the client never sees, and `emit` returning **`nil`** so the effect learns nothing. 1024 passes only because `unionEdges` deduped the library's own edge against one the application supplied — **the effective bound depends on accidental overlap**, which is why it must be explicit. (a) **Emit path**: reject an over-long `Contributing` in `appAdapter.Execute`'s emit closure (`live/app.go:224`), as a fourth entry beside the three checks already there, returning through the `Emitter` error that already exists, so the failure is a deterministic effect failure the reducer can handle. (b) **Flush trigger**: `mustFlush` compares `len(pendingIDs)` against `CoalesceFlushAt`, but `deferPatch` folds application identifiers into `pendingIDs`, so `MaxCoalesceFlushAt`'s "+1" — correct for the library's own edges — is not exact once an application contributes; evaluate the trigger against what the frame will actually carry. (c) **Attribution**: `gotthlive_outbound_validation_failed_total` is documented as *"any non-zero value is a library bug"* and an application can drive it; fix that comment, `internal/obs/metrics.go:214`, `internal/session/actor.go:570`, `internal/protocol/outbound.go:128`/`:149`, and `internal/protocol/limits.go:47` — which additionally still says *"the actor emits at half this ceiling"*, true of the default and not of the bound since D-14. **No truncation, no exported constant, no `Limits` field, no protocol change** (§4.3). Specs per the standing convention, including one at the boundary and one showing the metric stays zero. | DEV-1 | checkpoint 2 |
| **C-32** | **`live.IsRetryable(error) bool` is re-added; `live` goes 45 → 46.** My §5.4 cut was wrong and its own pre-registered trigger has fired. Mutation evidence: with `live.Retryable` replaced by a plain `fmt.Errorf("%w")` — the mark **gone** — `examples/chat`'s suite is **green**, including the spec whose message asserts the pump *"must have wrapped it with live.Retryable"*. The workaround `errors.Unwrap(err) != nil` tests wrapping, not classification, and the mutation is behaviour-changing (`chat.go:511` re-subscribes only when retryable). Ruled for the predicate over `livetest.AssertRetryable` (§5.2): a predicate composes with Gomega and an assertion helper does not; C-25's `testing.TB` guard does not transfer because there is nothing to guard; and an exported setter whose mark nothing exported can read is a one-way door at a stdlib bar. Implement over the same `errors.As` the internal `retryable` uses so the mark survives wrapping. Specs: marked, unmarked, `nil`, doubly wrapped, and **a plain `%w` wrap of an unmarked error** — the input the current workaround cannot distinguish. In the same PR: chat's two specs switch to it, `FRICTION.md` F-4 comes out, and api-surface gains a row with the count moved by hand to 46. | DEV-1 (symbol, specs, ledger) + DEV-3 (chat, FRICTION.md) | checkpoint 2 |

---

## 9. Disposition, and the gate

**I do not veto the checkpoint-2 gate.** Stated unambiguously for PM-1 to carry:
nothing I found this round falsifies the evidence the gate rests on. I ran the
three new suites, I mutation-tested the browser one and watched it go red in the
two right places, I reproduced the FR-33 module figure independently, and I
reproduced O-1's generator gate from both directions. The six landings are sound,
and they cost **zero exported identifiers** between them.

Two things the gate report must carry rather than merely link:

1. **C-28.** The browser evidence this gate is largely *about* is produced by no
   CI job. That does not make the evidence false — I reproduced it — but it means
   the gate is certifying a property nothing will re-check. PM-1 should say so in
   the report, not defer it to a condition table.
2. **NFR-7 at 1 of 8 is PM-1's to rule on**, and C-28 is a prerequisite for the
   narrow reading being worth anything.

**On the two rulings that changed my own prior positions.** F-4 reverses my §5.4
cut, and C-30 finds that the read-pump link PM-1 left open for me is defensible
in *shape* and broken in *sampling* — a question neither of us asked. I want both
recorded as corrections rather than refinements. The re-add trigger I wrote in
§5.4 was a real trigger and it fired on schedule; that is the mechanism working,
not failing.

**And the standing observation, which held again.** Every round of this project
has produced at least one defect that only running could find. This round
produced two, and both were invisible to careful reading in the specific way that
makes the rule worth keeping: `ci.sh` *says* "every gate this invocation could
run is green" while thirty specs do not run, and the tracer's span graph is
perfectly correct in the suite and comes apart the moment a real sampler touches
it. In both cases the artifact that lies is the one designed to tell the truth —
a CI verdict and a trace. The count is six.

— L9-1, 2026-08-04

---

# Addendum — L9-1, 2026-08-04: QA-1's gate report, read after the fact

**This is an addendum, not part of the review above.** QA-1's
[`docs/qa/checkpoint-2.md`](../qa/checkpoint-2.md) landed in this worktree while
I was writing; I reviewed against `9d44742e` and had not seen it. Nothing above
changes. Three things need saying so PM-1 does not track one finding twice, and
so one of my notes gets the weight it turns out to deserve.

### A.1 C-28 and QA-1's D-20 are the same defect, found twice, independently

QA-1 measured the same thing I did and got the same numbers to the spec:
**30 skips in the library image, of which 19 are `CHROME_BIN is unset`**, and
neither workflow job runs them. They numbered it **D-20** and made it
**merge-blocking on QA-1's own authority**.

**Merge them. Keep D-20's number, drop C-28's, and carry my §7.1 as the
reviewer's concurrence rather than as a second item.** Two agents reaching the
same measurement from different directions — QA-1 from the skip reasons, me from
the spec-count delta between two images — is worth more than either finding
alone, and it should be recorded that way rather than as duplicate work.

**I endorse the merge-block.** I wrote in §9 that I do not veto the gate and that
stands: the evidence is real and I reproduced it. But "the gate may pass" and
"this may merge before D-20 clears" are different questions, and QA-1 owns the
second one. Their answer is right, and my C-28 was written a notch too soft —
"named in PM-1's gate report" should read "**cleared before checkpoint 2 signs**",
which is QA-1's disposition, not mine to weaken.

QA-1's condition 1 is also more specific than mine in one useful way: it asks
DEV-1 to decide **explicitly** whether `GOTTHLIVE_E2E=1` is included, and to say
in the workflow why if it is not, *"because a skip nobody explains becomes a skip
nobody notices."* That is the right instinct and it is the same one `ci.sh`'s own
header already encodes for the node suite. Take QA-1's wording over mine.

### A.2 D-19 — and I reproduced it by accident before QA-1 reported it

QA-1's injection G-c removes `gofmt` from `PATH` and finds that `ci.sh` prints
`gofmt: command not found` and then **`clean`**, recording no failure.

**My §7.3 note contains independent evidence of exactly that, and I did not read
it correctly at the time.** My first run of `ci.sh` — under `bash -lc`, which
strips the toolchain — produced this:

```
==> gofmt (NFR-12)
ci.sh: line 77: gofmt: command not found
clean
```

and `gofmt (NFR-12)` does **not** appear in that run's failure list, while every
other Go step does. I filed it as an environment trap. It is an environment trap
*and* it is a gate that reports `clean` when the tool it runs is absent, which is
QA-1's D-19 reached from the other side. The `unformatted="$(gofmt -l . | …|| true)"`
construction cannot distinguish "no files are unformatted" from "the command did
not run", and the `|| true` is what swallows it.

**Reproduced in isolation, so this does not rest on my accidental run.** With
`gofmt` alone removed from `PATH` and every other tool intact, `ci.sh` prints
`gofmt: command not found`, then **`clean`**, and `gofmt` appears **nowhere in
the verdict**. Three lines below, `staticcheck` is guarded by an explicit
`command -v` and *is* recorded as a failure under the same conditions. The
asymmetry between two adjacent steps in one file is the whole of D-19, and the
fix is to give the `gofmt` step the guard the `staticcheck` step already has.

**So §7.3 is upgraded from a nit to corroboration of D-19**, and I withdraw the
sentence *"This is not a repository defect."* The `bash -lc` half is not; the
`clean` half is, and it is QA-1's to carry. The `/etc/profile.d/` suggestion
stands on its own as image hygiene and is still only a nit.

The general shape is worth naming, because this project has now produced it three
times in two documents: **a check that cannot fail is indistinguishable from a
check that passes, and the failure is always silent.** D-13 (reading `go list`'s
stderr), C-21 (the unread `total` column), and now D-19 and D-20 are four
instances. A gate step should assert that its tool exists before believing its
output — `ci.sh` already does exactly this for `staticcheck`, three lines below,
and the asymmetry between the two steps is the whole bug.

### A.3 D-21 — 617 bytes standing on nothing

QA-1's N4/N5 are the better half of their mutation campaign: `save()` and
`restore()` in `client/runtime.js` can be **removed entirely** and nothing in the
repository fails — not the browser suite, not with `GOTTHLIVE_E2E=1`, not the
node suite, not `ci.sh`. 617 minified bytes, 6.7 % of an artifact this project
measures to the byte, in the subsystem R-2 says will grow.

I am not opening a condition on it — it is QA-1's finding, correctly numbered and
routed, and the code looks right. Two observations for whoever picks it up:

1. **The diagnosis is exact and it points at the missing spec.** `restore()` is
   for the case where the node **was** replaced — a tag change, a fragment-root
   swap, a `REPLACE` op — and nothing in the repository puts the runtime in that
   position. The suite to write is not "test `restore()`"; it is **one spec that
   forces a replace and then asserts focus, caret and scroll survived it**. My
   check 17 is the adjacent evidence: the DOM suite goes red when morph stops
   preserving, which proves it tests the *preserve* path — and D-21 is the proof
   that it never reaches the *restore* path at all. The two results are
   complementary and both are needed to describe the suite's real coverage.
2. **It sharpens D-20/C-28 rather than competing with it.** A subsystem no test
   exercises, in a suite no CI job runs, is two independent reasons the next
   regression there is silent. Whichever lands first, the other still matters.

### A.4 Where QA-1 and I differ, and it is not much

QA-1 gates **PASS WITH CONDITIONS**; I do not veto. Those agree. On criterion 7
QA-1 marks **PARTIAL** on two distinct gaps (the NFR-7 matrix, and `<details>`),
and routes both to PM-1 rather than descoping either. That is the correct
disposition and I have nothing to add to it — §7.5 above says the same thing
about the matrix and defers to PM-1 for the same reason.

The one thing I hold that QA-1's report does not reach, and that PM-1 should not
lose between the two documents, is **C-30**: the tracer's span graph is correct
in the conformance suite and comes apart under the project's own default sampler,
0 of 300. It is not a checkpoint-2 exit criterion and it does not bear on this
gate — it is Phase 1 observability debt — but it is the only finding of this round
that no suite can currently see, because the suite's recorder stamps one TraceID
on everything by construction.

— L9-1, 2026-08-04
