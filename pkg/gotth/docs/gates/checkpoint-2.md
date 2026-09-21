# Checkpoint 2 gate report — gotth-live

| Field | Value |
|---|---|
| Project | **gotth-live** — a Go library for server-driven live UI: state and rendering stay in Go, the browser holds one long-lived connection, events go up and re-rendered HTML fragments come down |
| Phase | **2 — Component model**, checkpoint 2 of the consolidated Phase 1–3 track |
| Report owner | **PM-1**, who owns scope and the requirements document |
| Date | 2026-08-04 |
| Tree | `02ff85c3` on `dev-/gotth-live-orchestrator-c3efc4` |
| Verdict | **CLOSED WITH CARRIED DEBT** |
| Correctness sign-off | **QA-1 — PASS WITH CONDITIONS** ([`docs/qa/checkpoint-2.md`](../qa/checkpoint-2.md)), merge-block **D-20 cleared** in `ca2219fc` |
| Technical veto | **L9-1 — not exercised** ([`docs/reviews/checkpoint-2-round.md`](../reviews/checkpoint-2-round.md) §9) |
| Requirements applied | [PRD](../PRD.md) **v0.5**, §9's seven rows, landed with this report |
| Format precedent | [`docs/gates/phase-0.md`](phase-0.md) |

**Who the named roles are**, since they appear throughout: **QA-1** owns
correctness and can block a merge; **QA-2** owns resilience and performance and
can also block a merge; **L9-1** is the Principal Engineer and holds technical
veto; **DEV-1** is the server-core Go engineer, **DEV-2** owns the browser-side
client runtime, **DEV-3** owns the examples and interoperability with HTMX.

---

## 1. Verdict

**Checkpoint 2 is closed with carried debt.** All thirteen Phase 2 exit criteria
are met. QA-1's one merge-blocking condition is cleared. L9-1 did not veto. Two
criteria are closed against requirements I **amended in this same landing**
rather than against the wording they were written to, and both amendments are
argued in PRD v0.5 §9 — NFR-7 (the browser matrix) and FR-55 (what "first-class"
forms means). Debt carries into checkpoint 3 with owners and, for the first time,
with **exit boxes**, because the reason two of these items went missing twice is
that they were owed by a phase and enforced by nobody.

**What the verdict is not.** It is not "closed", because three obligations leave
this checkpoint unfinished in a way a reader is entitled to see in the verdict
line: FR-36's clause 4 is a decision I made here and a mechanism nobody has
built; five of FR-36's eight spans still start nowhere; and G2 — steady-state
memory per idle connection — has **no measurement at all**, at a checkpoint where
the gate for it has been quoted in three documents. None of the three is a
checkpoint-2 exit criterion. All three would be worse if the verdict rounded them
away.

**The standing rule on this project is that a gate is what you ran, not what you
read.** Every number in §2 was produced by me, at `02ff85c3`, in the two project
images. QA-1 and L9-1 both measured `9d44742e`; their figures are correct for
that tree and several of them have moved since, because six commits landed
between their gate and this one. Where I quote a number I did **not** re-run, §2.4
says so and says why.

---

## 2. What I ran, and what came back

Two images: `dis-gotth-live:latest` (Go 1.26.5, no node, no browser — the image
CI's `library` job uses) and `dis-gotth-live-bench:latest` (node v24.19.0,
Chromium 151.0.7922.71, Debian 13.6 — the only image in the project with either).
Every invocation uses `bash -c`, never `bash -lc`: a login shell re-runs
`/etc/profile`, which resets `PATH` and silently removes the Go toolchain. QA-1
recorded that trap (§2.1 of their gate) and L9-1 hit it (§7.3); it is worth one
more line here because it is how a healthy tree reads as eleven broken gates.

### 2.1 The gate script

```bash
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live dis-gotth-live:latest \
    bash -c 'bash ci.sh; echo "CI_EXIT=$?"'
```

**`CI_EXIT=0`.** `gofmt` clean, `go vet` clean, `staticcheck` clean,
`go test -race` clean across the module and the three satellite modules
(`examples/counter`, `examples/chat`, `test/routers`). FR-7 **ran** rather than
skipping — the repository root is mounted, so the refinement plugin is built from
`research/` and the committed output is compared against a fresh generation:
*"the committed output is byte-identical to a fresh generation"*, protobuf **and**
templ.

**Two announced skips, and the second one is new this round:**

```
==> verdict
skipped (needs a context this invocation does not have):
  - client runtime suite (NFR-4)
  - browser conformance specs (19 + 3, the whole of FR-25…FR-28 and FR-30…FR-32 in a browser)
every gate this invocation could run is green
```

The browser skip is D-20's other half. Before `ca2219fc`, `ci.sh` printed *"every
gate this invocation could run is green"* over thirty specs that did not run and
said nothing about them. It now names them, prints the command that runs them,
and says outright that the race-detector step above does **not** cover them — it
ran them and they skipped. That sentence is the difference between a skip and a
silence.

### 2.2 The numbers, and which of them moved

| What | Measured at `02ff85c3` | At QA-1's gate (`9d44742e`) | Source |
|---|---|---|---|
| `ci.sh` | **exit 0**, 2 announced skips | exit 0, 1 announced skip | §2.1 |
| Client runtime, minified | **9,343 B** | 9,143 B | `ci.sh`, client size step |
| Client runtime, **gzip −9 — the NFR-2 gate** | **3,961 B** of 12,288 B — **8,327 B headroom, 67.8 %** | 3,874 B — 8,414 B, 68.5 % | same |
| Exported surface, `live` | **46/46** identifiers, **49/49** fields, **95/95** | 45/45, 49/49, 94/94 | `ci.sh`, FR-65 step |
| Exported surface, `live/livetest` | 3/9 identifiers, 0/6 fields, 3/15 | unchanged | same |
| Surface total | **49** identifiers, 49 fields, **98** | 48, 49, 97 | same, *"the surface matches the ledger"* |
| FR-7 | byte-identical, gate **ran** | same | §2.1 |
| Browser | **Chromium 151.0.7922.71**, Debian 13.6 | same | `"$CHROME_BIN" --version` |
| Node | **v24.19.0** | same | `node --version` |

**The bundle moved +200 minified / +87 gzipped**, and it is the good direction of
a size ledger doing its job: `client/SIZE.md` §1.1 attributes every byte to D-15's
reflected-attribute record and the `syncAttrs` half of the same fix, plus D-21's
covering work. NFR-2 is met with 67.8 % headroom. PRD §3, NFR-2 and R-2 carried
the old figure and are corrected in v0.5.

**`live` moved 45 → 46 identifiers**, entirely C-32: `live.IsRetryable(error) bool`,
re-added on L9-1's ruling that their own §5.4 cut was wrong and its pre-registered
trigger had fired. Fields did not move. The ledger was updated by hand and the
tool agrees with it, which is the arrangement FR-65 asks for.

### 2.3 Spec counts, per suite and per image

Each from `go test … -v` in the image named, `-ginkgo.no-color`.

| Suite | Image | Ran / total | Result |
|---|---|---:|---|
| `test/internal/conformance` | library | **124** of 157 | 124 passed, 0 failed, **0 pending**, 33 skipped |
| `test/internal/conformance` | bench, full | **146** of 157 | 146 passed, 0 failed, 0 pending, 11 skipped |
| … `-ginkgo.label-filter=browser` | bench | **22** of 157 | 22 passed, 0 failed, 0 pending |
| … the same, `GOTTHLIVE_E2E=1` — **this is the CI step** | bench | **25** of 157 | **25 passed, 0 failed, 0 pending** |
| `examples/chat` (`-race`) | library | 151 of 151 | 151 passed |
| `examples/counter` (`-race`) | library | 47 of 47 | 47 passed |
| `test/routers` (FR-33, `-race`) | library | 13 of 13 | 13 passed |
| `internal/session` | library | 81 of 81 | 81 passed |
| `live` | library | **101** of 101 | 101 passed |
| `client/test/*.test.mjs` | bench | **56** tests | 2 + 34 + 20; **56 pass, 0 fail, 0 todo** |

**Read the two conformance rows together, because they are the checkpoint's
central fact.** The library image runs 124 of 157 and exits 0; the bench image
runs 146 and exits 0. The 22-spec gap is every DOM-preservation and every
HTMX-coexistence spec in the repository — the evidence for four of the criteria
below. Until `ca2219fc` that gap ran in no CI job at all. It now runs in the
`client` job, with `GOTTHLIVE_E2E=1` set deliberately (three more specs) and
`-ginkgo.fail-on-empty` guarding the guard.

**Four counts changed since QA-1 measured**, all of them upward and all of them
explained by commits that landed after their gate: the suite total went 154 → 157
(D-15's server-half spec, D-21's replace-and-restore spec, C-31's boundary spec);
the browser label set went 19 passed + 1 pending → **22 passed + 0 pending**; the
node morph suite went 16 tests with a failing `todo` → **20 tests, 0 todo**; and
`live` went 84 → 101 specs (C-31 and C-32's required specs). **The pending count
is zero everywhere.** That matters more than the totals: at QA-1's gate there was
exactly one pending spec in the repository and it was D-15's, held open on purpose
as a requirement the implementation could not satisfy. There is nothing held open
now.

### 2.4 What I did not re-run, and why

Per FR-73's rule applied to ourselves — "not measured, and why" beats an estimate.

- **QA-1's thirteen mutations and L9-1's eighteen checks.** I did not re-run them.
  Both were run against pristine `git archive` exports of `9d44742e`, both are
  reproducible from their own documents, and nothing in the six commits since
  touches the properties they mutate — except D-21's, whose subject **did** change
  and which I verified differently and directly (§3, criterion 7). Re-running
  thirty-one mutations to reproduce thirty-one identical results is not evidence,
  it is ceremony.
- **L9-1's 0-of-300 sampling measurement (C-30).** Not re-run. It requires a
  scratch consumer module with a real OTel SDK, it is the finding I am ruling on
  rather than the ruling itself, and nothing landed this round that could move it —
  C-29 was documentation only and says so in its own changelog. The number is
  L9-1's and is cited as theirs throughout §5.2.
- **The Firefox and WebKit obstructions.** Not re-run, and this is the one place I
  want the reasoning explicit because I am **ruling** on it: QA-1 measured the
  obstruction rather than asserting it (`firefox-esr` speaks WebDriver BiDi;
  `cdp_test.go` speaks CDP only; `dpkg -l` and `ls /usr/bin` find no Firefox, no
  WebKit, no Epiphany in either image), L9-1 declined to re-measure for the same
  reason, and the images must not be rebuilt this round. A third identical
  negative result adds no information. What I did verify myself is the positive
  half — that the one cell we have runs, passes, and runs *in CI*.
- **G2's memory figure.** Nothing measured it, this round or any round. §6.2.

---

## 3. Verdict per exit criterion

PRD v0.5, *Phase 2 — Component model (consolidated track, checkpoint 2)*. Thirteen
criteria; each row names the evidence and the document that holds it.

| # | Criterion | Verdict | Evidence | Held in |
|---|---|---|---|---|
| 1 | Chat example passes QA-1's suite in full. **This is the gate** | **MET** | 151/151 under `-race`, re-run by me. QA-1's N7–N12 mutate six distinct properties of chat's own subject matter and each turns red exactly where claimed | [qa/checkpoint-2](../qa/checkpoint-2.md) §3, §4.2 |
| 2 | Event bindings from templ, no hand-written JS (FR-54) | **MET** | Chat carries no hand-written JavaScript and puts exactly one script element on the page, the runtime's. Both are markup assertions, so they hold without a browser | qa §3 |
| 3 | Forms: submit, per-field change, server-driven validation, input preserved across another user's event (FR-55) | **MET**, against **FR-55 as amended** | Chat's three input-preservation specs plus validation; QA-1's N7 widens the composer's `Dirty` to the room and turns exactly those three red. The amendment defines "first-class" and does not lower it — every property FR-55 now names was already shipped | qa §3; ruling §5.3 |
| 4 | Lifecycle hooks mount/event/teardown with a leak test (FR-56 as amended) | **MET** | Chat's two lifecycle specs; N12 stops `Teardown` unsubscribing and both go red | qa §3, §4.2 |
| 5 | Error boundaries (FR-23 as amended) — **reducer** | **MET** | Chat *answers a reducer panic with an Error frame naming the event* | qa §3 |
| 5b | … **render** | **MET** | C-26 closed in `87bf5647`; N9 reverts it and exactly that spec goes red | qa §3, §4.2 |
| 5c | … **effect**: no `Error` frame, `gotth.effect_failed`, `retryable="false"`, origin `effect:<source>` | **MET** | N10 makes the effect path emit an `Error` frame and the one spec named for the criterion's own sentence goes red. The criterion has a test that can fail it | qa §4.2 |
| 6 | `Config.Dev` implemented or cut (C-26) | **MET** — implemented | Chat *keeps a panic value off the wire in production and puts it on in dev*. FR-23's dev/prod sentence stands; no further amendment owed | qa §3 |
| 7 | DOM conformance green **across NFR-7's browser matrix** for every FR-25 case, plus FR-26 and FR-27 | **MET**, against **NFR-7 as amended** | Three parts, below | §5.1, §5.4 |
| 8 | Morphed subtrees remain interactive, no re-binding (FR-28) | **MET** | In the 25. N2 replaces delegation with per-node listeners bound once and exactly the FR-28 spec goes red | qa §4.1 |
| 9 | HTMX interop FR-30, FR-31, FR-32, G8 | **MET** | Seven specs against vendored HTMX 2.0.10 whose SHA-256 is **re-checked on every run**, not merely documented. N3 turns the FR-32 precedence spec red. D-16 is a documentation gap, not a failure of this criterion | qa §3; [reviews/checkpoint-2-round](../reviews/checkpoint-2-round.md) §2.3 |
| 10 | Counter mounts unchanged under `net/http`, `chi`, `gin` (FR-33) | **MET** | 13/13 at `/live`, `/app/live`, `/ui/gotth`; each opens a live session and drives an event to a patch rather than fetching a file. N6 hardcodes the mount and 7 of 13 go red. The suite is its own module, so chi's +1 and gin's +33 modules never reach a consumer — L9-1 measured 61 → 62 → 95 → 61 | round §1 check 5, §2.1 |
| 11 | Server-initiated patches carry a named effect origin; zero `unknown` (FR-42) | **MET** | Chat's three provenance specs; N8 sets the origin source to `"unknown"` and 5 specs go red | qa §4.2 |
| 12 | Escaping by default, XSS payload suite through chat (FR-50) | **MET** | Nine payload classes × two sites, plus roster and notices; N11 renders the body through `@templ.Raw` and **11** go red including the on-the-wire one | qa §3, §4.2 |
| 13 | Client runtime still ≤12 KB gzipped | **MET** | **3,961 B** of 12,288 B, re-measured by me. The gate is also non-vacuous in the negative direction: an edit without a rebuild makes `minify -check` say *stale*, exit 1 | §2.2; qa §4.3 |

### Criterion 7, in full, because it is the whole of the disagreement

QA-1 marked it **PARTIAL on two distinct gaps** and routed both to me rather than
descoping either. That was the correct disposition and it is why this report has
rulings in it. Both gaps are now closed, by different means, and the means matter.

**(a) "Every case in FR-25" — closed by a fix, not by an amendment.** D-15 was the
one named case failing: `<details>` open state, reverted by an unrelated patch
because `open` reflects to a content attribute, so the live attribute has two
authors and one bit and morph read the user's own state as a server declaration.
DEV-2 fixed it in `d8d190b6`. **I verified this against the evidence rather than
the claim** — the standing rule, and the one QA-1 applied to their own report when
they un-pended the `PIt` and watched it fail before accepting DEV-2's write-up:

```
go test ./test/internal/conformance/ -args -ginkgo.label-filter=browser \
    -ginkgo.focus="details|REPLACED"
    → Ran 3 of 157 Specs. SUCCESS! -- 3 Passed | 0 Failed | 0 Pending
```

Three specs, not one, and the shape is what makes the fix credible rather than
narrow. There is a spec for the **user's** half (*a `<details>` the user opened and
the server never mentioned*), a spec for the **server's** half (*still closes a
`<details>` when the server withdraws its open declaration*), and a third that
D-21 produced — *restores focus, the caret and scroll across a patch that REPLACED
the node holding them*. A one-sided fix here would have been easy and wrong: "morph
stops writing `<details> open`" satisfies the user's half and breaks the server's,
and the second spec exists precisely to fail on that. The whole browser set is
**25 passed, 0 pending**, and the node morph suite's failing `todo` is gone — 20
tests, 0 todo.

**(b) "Across NFR-7's browser matrix" — closed by amending NFR-7.** Ruling §5.1.
One cell of eight is verified, six of the other seven are not obtainable on this
infrastructure at any effort, and the requirement now says which is which. The
amendment is worth something only because of (c).

**(c) The gap that made both of the above possible to certify: D-20.** The
criterion's evidence lives in specs that, at QA-1's gate, **ran in no CI job**.
QA-1 blocked the merge on it; L9-1 reached the same measurement from the other
direction as C-28 and, in their addendum, merged the two under QA-1's number and
endorsed the block over their own softer wording. `ca2219fc` closes it: the
`client` job runs the browser specs, and DEV-1 proved the new step can go red
under a mutation only a browser can catch — `syncAttrs` writing an attribute it
already holds is a no-op in a DOM shim and a **media reload** in Chromium, and the
media-position spec fails with `1.250 -> 0.000` while every other gate stays green.
That is the right proof to have demanded.

---

## 4. The sign-offs, carried explicitly

**QA-1 — PASS WITH CONDITIONS.** Recorded in
[`docs/qa/checkpoint-2.md`](../qa/checkpoint-2.md) §9. Their affirmation is not
generic and I am carrying it verbatim in substance: the work was tested
adversarially rather than confirmed, thirteen mutations were run against pristine
exports, and **eleven turned red in exactly the place they claim to protect**.
Three conditions:

| | Condition | Disposition at this gate |
|---|---|---|
| 1 | **D-20 — the browser suites run nowhere in CI.** Merge-blocking on QA-1's own authority | **CLEARED**, `ca2219fc`. QA-1's more specific wording was taken over L9-1's: DEV-1 decided `GOTTHLIVE_E2E=1` **explicitly** and said why in the workflow, because a skip nobody explains becomes a skip nobody notices |
| 2 | **NFR-7's matrix is 1 cell of 8**, and the criterion says "across NFR-7's browser matrix". PM-1's call | **RULED**, §5.1. NFR-7 amended, PRD v0.5 |
| 3 | **D-15 — a case FR-25 names by hand fails in a browser.** PM-1 + DEV-2 | **CLOSED by fix**, `d8d190b6`, verified at this gate rather than accepted, §3 criterion 7(a) |

QA-1 also recorded the sentence that decided the shape of this report: *"Leaving
criterion 7 quietly marked green on one engine is the one option I will not
sign."* NFR-7 as amended does not do that. It states the cell.

**L9-1 — technical veto not exercised.** Recorded in
[`docs/reviews/checkpoint-2-round.md`](../reviews/checkpoint-2-round.md) §9: *"I do
not veto the checkpoint-2 gate… nothing I found this round falsifies the evidence
the gate rests on."* Six landings reviewed, six accepted, zero rejected, and — a
detail worth carrying because it is unusual — the six cost **zero exported
identifiers** between them. Four rulings issued, none deferred; five conditions
C-28…C-32, none blocking, of which:

| | Condition | Disposition at this gate |
|---|---|---|
| **C-28** | The browser suites must run somewhere that fails | **Merged into D-20** by L9-1's own addendum §A.1, under QA-1's number, with L9-1 withdrawing their softer "named in PM-1's gate report" for QA-1's "cleared before checkpoint 2 signs". Cleared, `ca2219fc` |
| **C-29** | `instrumentation.md` §3 amended to the tracer that shipped | **CLOSED**, `84df3561` + `02ff85c3`. All three disagreements re-measured before being written. One consequence lands in the PRD: FR-36 clause 3's link enumeration is now **two sites, not three** |
| **C-30** | The event path is three independently-sampled roots | **Mechanism OPEN** (DEV-1, now a Phase 3 box); **the scope half RULED here**, §5.2 |
| **C-31** | D-18: bound `Event.Contributing` on the emit path, hold the invariant at the flush trigger, stop four sites calling an application's mistake a library bug | **CLOSED**, `acd89ae7` + `02ff85c3`. `CoalesceFlushAt`'s range narrows 1–1023 → 1–959 with a spec asserting the two constants cannot move independently. No exported identifier, no `Limits` field, no truncation, no protocol change — as ruled |
| **C-32** | `live.IsRetryable` re-added, 45 → 46 | **CLOSED**, `7fa150b4`. Surface measured at 46/46 by me, §2.2 |

L9-1 asked that two things be carried in the report body rather than deferred to a
table. Both are: **C-28/D-20** is §3's criterion 7(c) and §4's condition 1, and
**NFR-7 at 1 of 8** is ruling §5.1, which takes L9-1's input that the narrow
reading is worth nothing without C-28 and makes that dependency part of the
amended requirement.

---

## 5. The four rulings

### 5.1 NFR-7 — amended, and the defect was never the coverage

**Ruling: amend. NFR-7 now states what is claimed, what is verified, and what is
explicitly out of scope for v0.1, with the obstruction measured per cell and no
estimates. Applied in PRD v0.5; the amended text is NFR-7 itself.**

The choice QA-1 put to me was: add a WebDriver BiDi harness plus Firefox to the
bench image, or amend NFR-7 to state the matrix CI can actually run.

**I rejected building it now**, on three grounds and not on cost. The images must
not be rebuilt this round, so it could not land here regardless. It buys **one**
cell, not two — Debian ships one Firefox ESR, and "latest two stable" of Gecko is
no more reachable than "latest two stable" of Chrome. And it would not have found
the one FR-25 failure this checkpoint actually had: QA-1's §7 makes the point
sharply and it is the most useful sentence in their write-up on this subject —
**D-15 is a specification problem, not an engine problem.** `open` reflecting to a
content attribute is standard behaviour every engine implements identically. A
Gecko cell would not have caught it, and has not hidden it.

**What I would not do is leave criterion 7 quietly green on one engine.** So the
requirement moves rather than the evidence, and it moves into three labelled
statements, because the actual defect in the old wording was not the coverage — it
was that a support **claim** was written as if it were a test **result**. The old
sentence, *"the DOM conformance suite runs against this matrix"*, was false on the
day it was written.

- **(a) Supported by intent** — latest two stable Chrome, Firefox, Safari macOS,
  Safari iOS. Unchanged, and it is not decoration: it is a falsifiable claim about
  how the runtime is *written* (no `eval`, no vendor-prefixed API, no engine
  sniffing, one code path), and a bug report from Gecko or WebKit is in scope for
  v0.1.
- **(b) Verified by test** — the DOM-preservation and HTMX-coexistence suites, on
  every PR, in a CI job that can go red, against the pinned Chromium. Measured:
  **Chromium 151.0.7922.71, 25 specs, 25 passed, 0 pending.** **This clause is the
  gate.** Adding an engine is always permitted; removing one needs a PRD amendment.
- **(c) Out of scope for v0.1**, as a table with the obstruction per row: no second
  Chrome build in any image (Debian's rolling `chromium` carries exactly one
  version); Firefox ×2 blocked by a **measured** protocol mismatch, not a flag;
  Safari macOS ×2 and iOS ×2 not obtainable here at any effort level.

**The dependency L9-1 named is now part of the requirement, not a note beside
it.** A requirement narrowed to "one browser, in CI" is worth something only
because those 25 specs run in a job that can go red — and until `ca2219fc` they
ran in no job at all. Had D-20 not cleared, this amendment would have been a
narrowing to nothing and I would have refused it.

Two consequences I took rather than left implicit. The README must carry (a), (b)
and (c) **with those labels**, because the whole point is that a reader can tell a
claim from a result. And **R-8 is restated**: its stated mitigation, "DEV-2, QA-1
from Phase 2", was a control that never existed — there is no WebKit on this
infrastructure and there was not going to be. It is now **accepted and
unmitigated for v0.1**, disclosed, with BL-32 holding the verification. A risk
register that names a control nobody can execute is worse than one that admits
the risk is carried.

Backlog: **BL-31** (BiDi harness + Firefox), **BL-32** (WebKit/Safari, needs
infrastructure and not effort), and NFR-7 records the three places a second engine
should be looked at first — caret on `setSelectionRange` after an attribute write,
IME composition, `Element.getAnimations()` — so that work is cheap when it happens.

### 5.2 C-30 — clause 1's sentence survives, and clause 4 is what makes it true

**Ruling: FR-36 clause 1's *"a defect, not a sampling artefact"* sentence stays,
scoped to "within one sampling decision", and FR-36 gains clause 4 requiring the
server-side event path to be exactly one sampling decision. Applied in PRD v0.5.
The mechanism is DEV-1's and now has a Phase 3 exit box.**

L9-1 measured, over 300 real interactions under `ParentBased(TraceIDRatioBased(0.05))`
— instrumentation §3.5's **stated default**: `gotthlive.authorize` sampled 11/300,
`gotthlive.event` 11/300, and **both together 0 of 300**. Three roots on the event
path, three independent decisions, and `ParentBased` does not look at links. The
arithmetic closes on itself — `encode` at 30/300 is exactly 19 + 11 — which is
what tells me the measurement is sound.

So two of my own requirements were incompatible in one configuration, and both are
mine: clause 1 asserts a distinction the design could not make, and PRD v0.4 made
that same 5 % configuration NFR-1's gate. **That is my defect, not DEV-1's**, and I
want it recorded as one. The clause was written in the same pass that ruled the
connected-graph reading, and I did not check what the default sampler does to the
graph I had just required.

**The tempting fix was to strike the sentence. It is the wrong one**, for the
reason I already gave twice in v0.4: about the five spans that start nowhere, and
about FR-74's old wording. A requirement edited down until the implementation
satisfies it tests nothing. Striking the sentence would make the tracer conformant
by making its structure unobservable in every deployment — the graph verified in a
conformance suite whose recorder stamps one hard-coded `TraceID` on everything
(QA-1's D-11), and verifiable nowhere else. That is the exact failure mode this
project keeps catching: **a check that cannot fail is indistinguishable from a
check that passes.**

So the requirement moves the other way and the design has to earn the sentence.
**The mechanism is L9-1's and I am adopting it rather than inventing one**:
`gotthlive.event` becomes a true child of `gotthlive.authorize` through the
`SpanRef` the ingress already carries. An ended span is still a valid parent, this
is the truthful causal direction (authorization precedes the transition), it
removes a link site — which clause 3 always permits — and it is free.

**What I refused: a parent edge on the morph.** Same call as v0.4 and the same
reason. Instrumentation §3.3 states the morph span's start timestamp is *derived*
— server receive time minus a client-reported duration — and a parent edge asserts
an enclosure we do not observe. A lie a trace viewer renders as a fact is worse
than a link, precisely because it looks more informative. The alternative, a
`traceparent` on the wire, is 55 bytes per event against a 12,288 B budget to buy
propagation no v1 requirement asks for; BL-17 already holds it.

**Therefore the morph is a second sampling decision, and FR-36 now says so rather
than letting it be discovered a fourth time.** L9-1 said either choice is
defensible and what is not defensible is the current state, where the choice was
never made and its consequence never measured. I am making it, and booking the
cost honestly: what independent sampling loses is **attribution, not
measurement** — morph duration is also a `ClientTelemetry` frame and an unsampled
histogram (FR-29, FR-34), so an operator loses the per-event link and not the
latency. That is a real loss and a small one, and it is the one I choose over an
enclosure claim we cannot support.

**The falsifier is a spec, not a review.** Over N interactions at any 0 < *p* < 1,
the number of *partial* server-side graphs must be **0** — each interaction records
the whole path or none of it. Today that spec fails, because the joint rate is
0/300 and every sampled interaction is partial. A requirement whose falsifier can
be run is the only kind this project has been keeping.

Consequence for NFR-1: after clause 4, the 5 % configuration stops being one in
which two requirements contradict each other. 5 % of interactions record the whole
server-side graph; 95 % record none of it; there are no partial graphs to
misdiagnose.

### 5.3 F-6 — "first-class" forms means the mechanism, not a vocabulary

**Ruling: the mechanism. FR-55 is amended to name the five properties it means,
and no form type ships in v1. The documented pattern F-6 actually asked for is now
owed by FR-59 at Phase 4. Applied in PRD v0.5; BL-33 holds the helpers.**

DEV-3 asked the question well and asked it early — *"I am not proposing a helper…
recorded so PM-1 can rule, rather than so DEV-1 can build"* — and it sat open two
rounds, which is two rounds too long for a word Phase 4's DX work would have been
built on either way. Ambiguity in a requirement is scope debt and it accrues.

FR-55 now says "first-class" means: one event path for a form and for a single
control; per-field change through the same helper; **absence distinguishable from
empty**, so an unchecked checkbox arrives absent rather than empty; validation as
reducer output rendered by the application; and user input surviving a re-render
caused by an unrelated event. Every one of those was already shipped. The
amendment defines the bar, it does not lower it — criterion 3 was met before and
after.

**I rule against typed helpers, and not on taste.** A `live.Field` or
`live.FormErrors` would be exported surface whose only consumer is an example,
which FR-65 and review checklist §1.4 make a rejection; I am not going to enforce
that standard against a `Transport` interface and an `OnPatch` hook and then waive
it for a form type. And the attributes such a type would want to own —
`aria-invalid`, `aria-describedby` — are markup decisions belonging to the
application's own design system. A live-connection library that starts rendering
other people's accessibility semantics is the "framework growing inside a library"
DEV-3 named, and they were right to name it.

This is the FR-56 ruling applied to a second surface, with the same re-open
trigger: **a named application consumer in the PR**, not a second opinion. That
mechanism has now fired once, correctly, on `IsRetryable` (C-32), which is the
evidence that it is a real trigger and not a way of saying no politely.

**What I did not do is close it for free.** Half of F-6 is a genuine gap and it is
documentation: DEV-3's twenty lines read well and exist only in an example nobody
is told to read. FR-59's docs set now owes a forms-and-validation page derived
from the chat example — submit, per-field change, absence versus empty, the
validation render, and the ARIA the application writes by hand. Phase 4, Gate
QA-1, DEV-3. The ruling is also written into `examples/chat/FRICTION.md` beside
the question, so the next reader of F-6 finds the answer where they found the
question.

### 5.4 D-15 — the criterion is met, verified against the evidence

**Ruling: FR-25's `<details>` clause is met. Confirmed by running it, not by
reading DEV-2's commit.**

QA-1 held criterion 7 not-met while this failed, and they were right to: FR-25
names `<details>` open state explicitly, and one named case failing makes "every
case in FR-25" false regardless of the matrix question. The alternative on the
table was amending FR-25 to say what the controlled/uncontrolled rule can promise
for reflected attributes. **I did not need to take it, and I would not have.**
Amending a requirement because the implementation could not meet it is the move I
refused twice in v0.4; it was available here and the fix arrived first.

Verified at this gate, §3 criterion 7(a): three specs focused and run in Chromium
151, all pass, zero pending; the full browser set at 25/25 with zero pending; the
node morph suite's failing `todo` replaced by passing tests. The two-sided shape —
a spec for the user's half and a spec for the server's half — is what makes it a
fix rather than a suppression, because the cheap wrong fix (stop writing
`<details> open` at all) passes the first and fails the second.

One thing DEV-2 got right that is worth recording because it is a design rule and
not a patch: the fix keeps a `declared` record of the server's word where the user
cannot write it, and seeds it at the two moments the live attribute genuinely *is*
the server's word — first paint, and markup a patch just inserted. That
generalises to the class QA-1 identified (`<dialog open>`, custom elements
reflecting internal state) rather than special-casing one tag, and it cost 87
gzipped bytes.

**What is not closed** is QA-1's general observation that a property test over the
reflected-attribute set is the right instrument, and that no suite checks the
general form. That is not a checkpoint-2 criterion and I am not manufacturing one;
it is recorded in §8 for DEV-2 as the natural companion to BL-31, since a second
engine and a property test over reflected attributes are the two things that would
find the next D-15.

---

## 6. The two items my own prior round left open

### 6.1 I3 — ruled and closed

**Ruling: NFR-1's gate stays the figure at the shipped default sample rate. The
100 % figure becomes a gate condition at instrumentation §4.3's own 15 %
threshold. The shipped default is pre-registered and may not move between the
start of the Phase 5 measurement and the report. Applied in PRD v0.5; the I3 row
in `instrumentation.md` §8 is closed with a pointer.**

I recorded the two-figure *reporting* rule in v0.4 and explicitly did not make the
ruling. C-30 made it due early, because it turns out that the 5 % configuration is
the one in which a second requirement lives, so leaving the gate's configuration
unsettled leaves both unsettled.

**I reject moving the gate to 100 %.** G6's claim is that observability is
*default-on*; the number that matters to the operator in P3's shoes is the one
they get from the shipped default. A gate on a configuration the documentation
tells people not to run would leave the real default ungated — the opposite of
what I3 wanted.

**But I3's worry is real and is now enforced.** The worry is that the gate could
be met by choosing a sample rate rather than by writing an efficient hot path.
Instrumentation §4.3 already contains exactly the right instrument as a
diagnostic: *"if the 5 % gate is met only at 5 % sampling and 100 % sampling
exceeds 15 %, that is a signal the hot path allocates, and it is fixed rather than
sampled around."* I am promoting it into the gate. **The threshold is adopted from
a document L9-1 reviewed at Phase 0, not invented here** — that distinction is the
difference between a gate and a number I liked the look of. And the
pre-registration closes the other door: without it, the gate could be met by
lowering the default rate before measuring, which is the same outcome-shop RFC
§6.1.2 designed out of the memory gate, and the reason that design is worth
copying is that it has already survived one attempt to move a target.

QA-2 retains the measurement. I3 is closed as a question.

### 6.2 G2 — not PM-1's number, and now a checkpoint-3 box

**Ruling: G2's missing baseline is checkpoint-3 work, owned by DEV-1 (the
measurement) and QA-2 (the method, and D-10). It is a new Phase 3 exit criterion
in PRD v0.5, and that box is the ruling.**

The state, stated plainly. RFC-0001 §6.2 opens with *"An **estimate**, not a
measurement. Phase 1 records the measured baseline and this table is corrected in
the same PR."* Phase 1 did not. Checkpoint 2 did not, and nothing this round
touched it — L9-1 says so in their own "not measured, and why", and I confirm it:
no per-idle-connection memory figure exists anywhere in this repository. So the
**46,080 B gate rests on a 42,416 B composition estimate**, 7.9 % headroom, inside
which the kernel-socket line (4,000 B) and the WebSocket conn-struct line
(2,000 B) are themselves estimates. If the conn struct is twice its estimate, the
gate is breached and nobody would know until Phase 5.

**Is it simply Phase-3 work? Yes — and the reason it went missing twice is more
useful than the answer.** It was owed by a *phase*, and a phase is not a gate.
Every other obligation on this project that survived is one somebody had to tick a
box for. So it has a box now, at checkpoint 3, and the box is placed there rather
than at Phase 5 for a concrete reason: FR-22's 10k-cycle leak test is where RSS
gets sampled, and QA-1's **D-10** — *the leak test asserts goroutines but not RSS*
— is already QA-2's and already Phase 3. The measurement and the place it belongs
are the same piece of work, and splitting them is how it went missing.

The box requires the baseline **and** RFC §6.2 corrected in the same PR, per §6.2's
own sentence. It is a **baseline, not the gate**: G2 is enforced in Phase 5 at 1k
idle sessions, RFC §6.1.2's pre-registered response to a miss is untouched, and a
benchmark-method change remains not an available remedy. A figure still estimated
at checkpoint 3 blocks any Phase 5 memory number being quotable, which is where
C-5 (the TLS boundary binding the Next.js side, applied by QA-2 at the Phase 0
close) also points.

---

## 7. What closed this round

| | | Commit |
|---|---|---|
| **D-20 / C-28** | The browser suites run in a CI job that can go red, and `ci.sh` announces the skip it used to swallow. Proved by a mutation only a browser catches | `ca2219fc` |
| **D-15** | `<details>` open state: a reflected attribute has two authors and one bit, so morph keeps the server's word where the user cannot write it. Two specs, both halves | `d8d190b6` |
| **D-19** | `ci.sh`'s gofmt step reported `clean` when `gofmt` was absent — and a second step nobody had found | `501499cd` |
| **D-21** | 617 minified bytes of `save`/`restore` with no covering test now have one: a patch that REPLACES the node holding focus, caret and scroll | `281ece98` |
| **C-31 / D-18** | The `Event.Contributing` bound goes on the emit path; the flush trigger counts what the frame will carry instead of a proxy; four sites stop calling an application's mistake a library bug | `acd89ae7`, `02ff85c3` |
| **C-32 / F-4** | `live.IsRetryable` re-added; the spec it replaces could not fail. 45 → 46 | `7fa150b4` |
| **C-29** | `instrumentation.md` §3 describes the tracer that shipped; one of the three disagreements was the document's fault in the code's favour. Link enumeration: two sites, not three | `84df3561` |
| **F-6** | Ruled: the mechanism, not a vocabulary. FR-55 amended, BL-33 opened, the docs page owed by FR-59 | this landing |
| **I3** | Ruled: the sampled figure is the gate, §4.3's 15 % falsifier becomes a gate condition, the default is pre-registered | this landing |
| **NFR-7** | Ruled: amended into claim / verified / out-of-scope, with the obstruction measured per cell | this landing |
| **C-30, scope half** | Ruled: clause 1's sentence survives and FR-36 gains clause 4 | this landing |

Earlier in the checkpoint, and carried here for the record: **C-26**
(`Config.Dev` and the render-panic `Error` frame), **C-27** (`Script` accepts a
path and only a path), **D-12** (FR-36's connected-graph reading, affirmed by L9-1
this round), **D-14** (`Limits` validated at construction, bound 1023 — since
narrowed to 959 by C-31), **FR-58**'s effect-panic causal ID, **O-1** (`gen.sh`
covers the templ outputs), and the three suites the checkpoint is about.

---

## 8. What carries into checkpoint 3

Nothing here blocks checkpoint 2. Everything here has an owner, and the three
items marked **box** are new Phase 3 exit criteria in PRD v0.5 rather than
entries in a table somebody has to remember to read.

| Item | What it is | Owner | Where it is enforced |
|---|---|---|---|
| **G2 has no baseline** | 46,080 B gate resting on a 42,416 B estimate with two estimated lines. §6.2 | DEV-1 (measurement) + QA-2 (method, D-10) | **Phase 3 box** |
| **C-30's mechanism** | FR-36 clause 4: one sampling decision, plus the falsifier spec, plus §3.5 stating what sampling does to trace *structure* | DEV-1 | **Phase 3 box** |
| **FR-36's five unstarted spans** | `parse`, `reduce`, `render`, `render.fragment`, `send` declared and started nowhere; `encode` covers encode and send. Recorded unmet since v0.4, unmoved | DEV-1 + L9-1 | **Phase 3 box** |
| **D-10** | The leak test asserts goroutines, not RSS. CP1-16's PARTIAL | QA-2 | Phase 3 chaos suite; folded into G2's box |
| **Checkpoint 1 has no PM-1 gate record** | QA-1 re-issued every CP1 verdict, only CP1-16 PARTIAL; the Phase 1 boxes stay unticked because ticking them would record a gate nobody held | **PM-1** | Checkpoint-3 report closes 1 and 3, or a short CP1 record lands first |
| **D-16** | `hx-*` markup a morph *inserts* is inert until `htmx.process`. One documented line; the spec is written to go red when the gap closes | DEV-3 (docs) | Phase 3 |
| **D-17** | The runtime artifact answers at unbounded URLs behind `ETag` + `immutable`. A cache-pollution primitive, not a disclosure. One `==` | DEV-1 | Phase 3; L9-1 §7.2 argues it should be scheduled rather than parked |
| **F-1** | `livetest.Client` documented and unimplemented; ~550 lines of hand-rolled `protowire` duplicated across two modules. L9-1: *"the most expensive open item in the project"* | DEV-1 | Should be **scheduled**, not queued |
| **F-3** | `live.On("keydown", …)` has no key filter; the bench equivalence spec's F-CTR-6 needs it | DEV-2 | Phase 3, before Phase 5's bench |
| **Reflected-attribute property test** | D-15's general shape (`<dialog open>`, custom elements) is checked nowhere. QA-1 §6 | DEV-2 | Phase 3; the companion to BL-31 |
| **`ci.sh` FR-7 residual** | The whole FR-7 step skips when `research/` is absent, taking the templ half with it although it needs neither. Named in its own notice rather than fixed | DEV-1 | Phase 3, low |
| **Ledger row for a dependency that is not there** | `dependencies.md` justifies `playwright-go`, which is in no `go.mod` and imported nowhere; the suite it names is driven by a hand-rolled CDP client. §9.1 | DEV-1 (ledger) + L9-1 (gates it) | Must not reach Phase 5, where the ledger is a gate |
| **BL-31 / BL-32 / BL-33** | Second-engine verification; WebKit verification; typed form helpers | backlog | Not v1 |

**Two things that are open by design and should not be read as debt.** NFR-7's six
unverified cells are now *out of scope for v0.1* with the reason recorded, not an
outstanding task; and R-8 is *accepted and unmitigated*, which is a disclosed
position rather than a missing control.

---

## 9. Two things I found at the gate

### 9.1 The dependency ledger names a direct dependency the module does not have

**DEV-1 (ledger) + L9-1 (who gates it). Does not block; it should not reach
Phase 5.**

`docs/dependencies.md` §2 carries a full justification row for
`github.com/playwright-community/playwright-go` v0.6100.0 — *"Drives the FR-25/FR-26
DOM conformance suite across NFR-7's browser matrix. There is no other way to
assert focus, caret, IME composition, and `<details>` state survive a morph"* —
followed by a long passage on condition C-18 about how its browser downloads
threaten G11 and how a `//go:build browser` tag does not save you, because Go
resolves requirements at module granularity.

Measured: **`playwright-go` is not in `go.mod`, not in `go.sum`, and imported by
no `.go` file in the repository.** The suite it claims to drive is driven by
`test/internal/conformance/cdp_test.go`, a hand-rolled Chrome DevTools Protocol
client written over `github.com/coder/websocket`, which the module already
depends on. Its own header says why, and the reasoning is better than the
ledger's:

> *"Written rather than imported, and the reason is FR-74 rather than pride:
> every browser-automation library on offer arrives through npm with a lockfile
> and a post-install download… This speaks CDP over the WebSocket library the
> module already depends on, so the browser evidence costs zero new dependencies
> in any `go.mod` and zero npm anywhere."*

So the ledger describes a design that was superseded by a **better** one, and the
sentence "there is no other way" was disproved by the person who found the other
way. Three reasons this is worth a numbered item rather than a shrug. It is the
same class this project keeps catching — C-21's unread `total` column, D-19's
`clean` without gofmt, D-20's green-because-it-never-ran — **a document asserting
something nobody re-derived**. It runs in the flattering direction, which is
exactly why nobody would notice: we spent fewer dependencies than we claimed. And
`docs/dependencies.md` is an **L9-1-gated Phase 5 deliverable** (NFR-9, FR-69,
*"Dependency ledger final… L9-1-approved. **Gate**"*), so a phantom row is a
phantom row in a gate artifact. The fix is to strike the row and the C-18 passage,
and to record the CDP client as the decision it is, in the ledger's own
"considered and rejected" section — where "we considered a browser-automation
library and wrote 200 lines instead, for FR-74" is a better entry than any
dependency justification would have been.

### 9.2 A count in a comment drifted within one round of being written

**Nit, DEV-1.**

`ci.sh`'s browser-skip notice says *"This is 19 specs … plus 3 more behind
GOTTHLIVE_E2E"*, and the workflow's comment says the label filter *"selects 22
specs — the 19 the library job cannot run, plus the counter's 3"*. Measured today,
at `02ff85c3`:

```
-ginkgo.label-filter=browser                    Ran 22 of 157   22 passed, 0 pending
-ginkgo.label-filter=browser, GOTTHLIVE_E2E=1   Ran 25 of 157   25 passed, 0 pending
```

Both counts are stale by exactly 3, and the 3 are D-15's server-half spec, D-21's
replace-and-restore spec, and the un-pended D-15 spec — that is, they are stale
*because the round worked*. **It is not a defect**: the step runs the right set
whatever the comment says, and `-ginkgo.fail-on-empty` guards the only failure
mode a wrong count could hide. I record it because this project's recurring defect
class is a number nobody re-derives, the comment was written four commits ago by
the person closing D-20, and the honest version of "we keep numbers current" is
noticing when we do not. One line, next time that file is touched.

---

## 10. Exit statement

**Checkpoint 2 is closed with carried debt.** Thirteen exit criteria, thirteen
met, each individually checked against evidence re-run at `02ff85c3` rather than
read from a report. QA-1's PASS WITH CONDITIONS stands with its merge-block
cleared; L9-1's veto was not exercised. The four scope questions routed to me are
ruled, and every ruling with a requirement consequence is applied in PRD v0.5 in
this same landing, because a ruling recorded only in a gate report is one nobody
applies.

The work of this round was tested adversarially rather than confirmed. Thirty-one
independent mutations and probes between QA-1 and L9-1; eleven of QA-1's thirteen
turned red exactly where they claim to protect, and the two that turned nothing
red are a numbered defect that is now fixed. Both reviewers found, independently
and from different directions, that the checkpoint's headline evidence ran in no
CI job — and the fix was proved by a mutation that only a browser can catch. That
is the mechanism working.

Two of the thirteen closed against requirements I moved in this landing, and I
want the shape of those two on the record, because the same shape can be honest or
dishonest depending on which way it runs. **FR-25's `<details>` clause was met by
fixing the code**, and amending FR-25 was available and refused. **NFR-7 was
amended**, and it was amended because the old sentence — *"the DOM conformance
suite runs against this matrix"* — was false when it was written and would have
stayed false at any effort level this infrastructure supports. The test I applied
to both is the one v0.4 set: a requirement edited down until the implementation
satisfies it tests nothing. NFR-7 as amended is not narrower than what we can
verify; it is exactly as wide, and it says which of its three statements is which.

Writing a gate report also turned up two things nobody was looking for, and both
are the house defect class rather than anything new: a dependency ledger row for a
module that is not in `go.mod` and was replaced by something better, and a spec
count in a comment that went stale because the round succeeded. Neither blocks.
Both are §9, and the first one has a Phase 5 gate behind it.

**What I am least comfortable signing** is not in the criteria at all. G2 has no
measurement, the 46,080 B gate has been quoted in three documents, and it has now
been owed by two checkpoints. That is a number this project will one day publish
against Next.js under FR-73's honesty clause, and today it is an estimate with two
estimates inside it. It has a box at checkpoint 3, and the box is the whole of the
remedy: everything on this project that survived was something somebody had to
tick.

Checkpoint 3 may begin.

— PM-1, Product Manager, 2026-08-04

---

*Reproduce this report:*

```bash
REPO_ROOT=<repository root, not gotth-live/>

# the whole gate, with FR-7 able to run — bash -c, NOT bash -lc
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live dis-gotth-live:latest \
    bash -c 'bash ci.sh; echo "CI_EXIT=$?"'

# the browser cell NFR-7(b) gates on — the same command the workflow runs
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live dis-gotth-live-bench:latest \
    bash -c 'GOTTHLIVE_E2E=1 go test ./test/internal/conformance/ -count=1 -v \
             -timeout 30m -args -ginkgo.label-filter=browser \
             -ginkgo.fail-on-empty -ginkgo.no-color'

# D-15 and D-21, focused
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live dis-gotth-live-bench:latest \
    bash -c 'go test ./test/internal/conformance/ -count=1 -v -args \
             -ginkgo.label-filter=browser -ginkgo.focus="details|REPLACED" \
             -ginkgo.no-color'

# the node client suite (NFR-4), which ci.sh announces as skipped
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live dis-gotth-live-bench:latest \
    bash -c 'for f in client/test/*.test.mjs; do node --test "$f"; done'

# per-suite spec counts in the image CI uses
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live dis-gotth-live:latest \
    bash -c 'go test ./test/internal/conformance/ ./internal/session/ ./live/ \
             -count=1 -v -args -ginkgo.no-color'
```
