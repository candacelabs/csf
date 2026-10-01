# Checkpoint 2 — QA-1 gate

**2026-08-04.** Against the shared worktree at `9d44742e`
(`dev-/gotth-live-orchestrator-c3efc4`), in `dis-gotth-live:latest` (Go 1.26.5)
and `dis-gotth-live-bench:latest` (node v24.19.0, Chromium 151.0.7922.71,
Debian 13.6).

The subject of this gate is what the orchestrator's three-suite log lists: the
chat example (FR-61), the DOM-preservation conformance suite (FR-25, FR-26,
FR-27, FR-28), HTMX coexistence (FR-30, FR-31, FR-32, G8), the FR-33
three-router mount suite, and this round's three closures — D-14, FR-58 and
O-1.

This document does not repeat the two write-ups it rests on. The requirement
mapping, the browser evidence and the D-15/D-16 analysis are in
[`checkpoint-2-browser.md`](checkpoint-2-browser.md); the P5 resync work and
D-14/D-18 are in
[`checkpoint-2-conformance.md`](checkpoint-2-conformance.md). What is here is
what a gate is for: what I ran myself, what came back, what I broke to prove
the suites are not decorative, and what I will and will not sign.

---

## 1. Verdict

**PASS WITH CONDITIONS.**

Everything this checkpoint is *about* exists, runs green, and fails when the
behaviour it protects is broken. I ran thirteen of my own mutations against a
pristine `git archive HEAD` export; eleven turned exactly the specs red that
claim the mutated property, and **two turned nothing red at all** — §4.3, and
that is a new finding rather than a reassurance.

Three conditions. The first is merge-blocking on QA-1's own authority; the
other two are decisions I am routing rather than making.

| | Condition | Disposition |
|---|---|---|
| **1** | **D-20 — the browser suites run nowhere in CI.** `.github/workflows/gotth-live-checks.yml` runs `ci.sh` and the node suites, and its only browser step runs `chromium --version`. The DOM-preservation and HTMX-coexistence suites — the evidence for four of checkpoint 2's exit criteria — are enforced by nothing | **Merge-blocking.** §6, D-20 |
| **2** | **NFR-7's matrix is measured for 1 cell of 8**, and the FR-25 exit criterion says "across NFR-7's browser matrix" | **PM-1's call.** §7 states what is and is not verified, with no estimates |
| **3** | **D-15 — `<details>` open state is a case FR-25 names by hand, and it fails in a real browser.** The exit criterion says "every case in FR-25" | **PM-1 + DEV-2.** Not met as written; §6 |

**What I affirm.** The chat example is 151 specs green under `-race` and six
independent mutations of its own subject matter each turned red in exactly the
place they should. The three new browser suites are green and non-vacuous. The
three closures are real: D-14's bound is 1023 and rejection happens at
construction rather than by clamping; FR-58's effect-panic record carries
`scheduledBy`; `gen.sh --check` fails on a stale `_templ.go` and on a `.templ`
nobody enumerated. `ci.sh` is exit 0 with the FR-7 gate *running*, the client
is 3,874 B gzipped of a 12,288 B ceiling, and the exported surface matches its
ledger exactly. None of that is what the conditions are about.

---

## 2. What I ran, and what came back

Every number below is followed by the command that produced it. Nothing here
is quoted from another agent's report.

### 2.1 The gate script, with the repository root mounted

```bash
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live dis-gotth-live:latest \
    bash -c 'bash ci.sh; echo "CI_EXIT=$?"'
```

**`CI_EXIT=0`.** Thirteen steps. Twelve ran; one announced itself skipped:

```
==> verdict
skipped (needs a context this invocation does not have):
  - client runtime suite (NFR-4)
every gate this invocation could run is green
```

The FR-7 reproducibility gate **ran** rather than skipping, which is what the
root mount buys:

```
==> generated code is byte-reproducible (FR-7)
==> vendoring the refinement runtime into internal/refine
==> building protoc-gen-gorefine from research/protobuf-refinement-types
==> generating internal/protocol/refinepb (annotation schema)
==> generating internal/protocol/gotthlivepb (frames + refinements)
==> generating the templ views
==> comparing against the committed output
==> the committed output is byte-identical to a fresh generation
```

**One trap worth writing down, because I fell into it first.** `bash -lc`
inside `dis-gotth-live:latest` is not `bash -c`. The login shell sources
`/etc/profile`, which replaces `PATH` with the Debian default and drops
`/usr/local/go/bin` and `/go/bin`:

```
bash -c  → PATH=/go/bin:/usr/local/go/bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
bash -lc → PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
```

`ci.sh` under `bash -lc` reports eleven failures and one *false pass* — the
gofmt step, which says `clean` after `gofmt: command not found`. That is
**D-19**, §6.

### 2.2 The suites `ci.sh` cannot run

The node client suite, in the bench image, against the shared worktree:

```bash
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live dis-gotth-live-bench:latest \
    bash -c 'fail=0; for f in client/test/*.test.mjs; do node --test "$f" || fail=1; done; echo "NODE_SUITE_EXIT=$fail"'
```

| file | tests | pass | fail | todo |
|---|---:|---:|---:|---:|
| `client/test/bundle.test.mjs` | 2 | 2 | 0 | 0 |
| `client/test/codec.test.mjs` | 34 | 34 | 0 | 0 |
| `client/test/morph.test.mjs` | 16 | 15 | 0 | 1 |
| **total** | **52** | **51** | **0** | **1** |

`NODE_SUITE_EXIT=0`. The one `todo` is D-15, and it is a *failing* todo — the
assertion `an unrelated patch closed a <details> the user opened` is executed
and reported, and `node --test` exits 0 anyway. That is the intended shape and
it is worth knowing it is the shape: the D-15 evidence is in the log, not in
the exit code.

The browser suites, in the bench image:

```bash
docker run --rm -v <tree>:/w -w /w/gotth-live dis-gotth-live-bench:latest \
    bash -c 'go test ./test/internal/conformance/ -count=1 -v -timeout 20m \
             -args -ginkgo.label-filter=browser -ginkgo.v'
```

```
Ran 19 of 154 Specs in 9.664 seconds
SUCCESS! -- 19 Passed | 0 Failed | 1 Pending | 134 Skipped
```

And with the counter's own browser and CSP specs enabled, which the label
filter alone leaves out:

```bash
… bash -c 'GOTTHLIVE_E2E=1 go test ./test/internal/conformance/ … '

Ran 22 of 154 Specs in 22.741 seconds
SUCCESS! -- 22 Passed | 0 Failed | 1 Pending | 131 Skipped
```

### 2.3 The numbers

| What | Measured | Source |
|---|---|---|
| Client runtime, minified | **9,143 B** | `ci.sh` step *client size gate and subsystem ledger (NFR-2, NFR-3)* |
| Client runtime, gzipped | **3,874 B** of the 12,288 B ceiling — 8,414 B headroom, **68.5 %** | same |
| Exported surface, `live` | **45/45** identifiers, **49/49** fields, 94/94 | `ci.sh` step *exported-identifier delta (FR-65)* |
| Exported surface, `live/livetest` | **3/9** identifiers, **0/6** fields, 3/15 | same — the ceiling is a ceiling, not a target; this is F-1 |
| Surface total | 48 identifiers, 49 fields, 97 | same, *the surface matches the ledger* |
| FR-7 | byte-identical to a fresh generation | §2.1 |
| Browser | **Chromium 151.0.7922.71**, Debian 13.6 | `docker run --rm dis-gotth-live-bench:latest chromium --version` |
| Node | **v24.19.0** | same invocation |
| Bundle digest | `sha256 f4f5d424a91c46b7…` | `sha256sum live/clientjs/gotth-live.min.js` |

Spec counts, each from `go test … -v` in the image named:

| Suite | Image | Specs | Result |
|---|---|---:|---|
| `test/internal/conformance` | library | 154 | **123 passed**, 0 failed, 1 pending, 30 skipped |
| `test/internal/conformance`, `label-filter=browser` | bench | 154 | **19 passed**, 0 failed, 1 pending, 134 skipped |
| … the same, `GOTTHLIVE_E2E=1` | bench | 154 | **22 passed**, 0 failed, 1 pending, 131 skipped |
| `examples/chat` (`-race`) | library | 151 | **151 passed**, 0 failed |
| `examples/counter` (`-race`) | library | 47 | **47 passed**, 0 failed |
| `test/routers` (FR-33, `-race`) | library | 13 | **13 passed**, 0 failed |
| `internal/session` | library | 81 | **81 passed**, 0 failed |
| `live` | library | 84 | **84 passed**, 0 failed |

The 30 skips in the library image, with the reason each printed — this is the
measurement behind condition 1:

| Reason | Count |
|---|---:|
| `browser: CHROME_BIN is unset — run in dis-gotth-live-bench:latest` (7 direct + 12 via `BeforeAll`) | 19 |
| `e2e: set GOTTHLIVE_E2E=1 to run (compiles examples/counter)` | 9 |
| `soak-class: set GOTTHLIVE_SOAK=1 to run` | 2 |

The library image is the image CI runs. Those thirty are what CI does not run,
and nothing else in the workflow runs them either.

---

## 3. Verdict per exit criterion

PRD v0.4, *Phase 2 — Component model (consolidated track, checkpoint 2)*.

| # | Criterion | Verdict | What established it |
|---|---|---|---|
| 1 | Chat example passes QA-1's suite in full — **the gate** | **MET** | 151/151 under `-race`; six mutations of its own subject each turned red where claimed (§4.2, N7–N12) |
| 2 | Event bindings expressible from templ with no hand-written JS (FR-54) | **MET** | chat *carries no hand-written JavaScript at all* and *puts exactly one script element on the page, and it is the runtime's*; both are markup assertions, not browser ones |
| 3 | Forms: submit, per-field change, server-driven validation, input preserved across another user's event (FR-55) | **MET** | chat *Input preservation* ×3 + *Validation*; N7 (composer `Dirty` widened to the room) turns exactly those three red |
| 4 | Lifecycle hooks with a leak test (FR-56 as amended) | **MET** | chat *Lifecycle* ×2; N12 (`Teardown` stops unsubscribing) turns both red |
| 5 | Error boundaries (FR-23 as amended) — reducer | **MET** | chat *answers a reducer panic with an Error frame naming the event* |
| 5b | … render | **MET** | *answers a render panic with an Error frame naming the event*; N9 (revert C-26) turns exactly it red |
| 5c | … effect: **no** `Error` frame, `gotth.effect_failed`, `retryable="false"`, origin `effect:<source>` | **MET** | *answers an effect panic with a failure event and no Error frame*; N10 makes the effect path also emit an `Error` frame and exactly that spec goes red — which is the criterion's own sentence, executed |
| 6 | `Config.Dev` implemented or cut (C-26) | **MET** | chat *keeps a panic value off the wire in production and puts it on in dev* |
| 7 | DOM conformance green **across NFR-7's matrix** for every FR-25 case, plus FR-26 and FR-27 | **PARTIAL — two gaps** | The **suite** half is met and non-vacuous (N1–N3). The **matrix** half is 1 cell of 8 (§7). The **every case** half fails on `<details>`: D-15, verified live in §5 below |
| 8 | Morphed subtrees remain interactive with no re-binding (FR-28) | **MET** (Chromium 151 only) | N2 replaces delegation with per-node listeners bound once and exactly the FR-28 spec goes red |
| 9 | HTMX interop FR-30, FR-31, FR-32, G8 | **MET** (Chromium 151 only) | 7 specs green against vendored HTMX 2.0.10; N3 turns the FR-32 precedence spec red. D-16 is an undocumented behaviour, not a failure of this criterion |
| 10 | Counter mounts unchanged under `net/http`, `chi`, `gin` (FR-33) | **MET** | 13/13 at `/live`, `/app/live`, `/ui/gotth`; N6 (a hardcoded mount) turns **7 of 13** red |
| 11 | Server-initiated patches carry a named effect origin; zero `unknown` (FR-42) | **MET** | chat *Provenance* ×3; N8 (origin source → `"unknown"`) turns 5 specs red |
| 12 | Escaping-by-default verified with an XSS payload suite through chat (FR-50) | **MET** | 9 payload classes ×2 sites + the roster + notices; N11 (`@templ.Raw(m.Body)`) turns **11** red including the on-the-wire one |
| 13 | Client runtime still ≤12 KB gzipped | **MET** | 3,874 B of 12,288 B |

Eleven met, one met-with-an-engine-caveat noted twice, one partial with two
distinct gaps. Criterion 7 is the whole of the disagreement.

---

## 4. Non-vacuity, by mutation

This project has now shipped two tests that passed under the mutation they
existed to catch — the counter's effect-failure name, and the scroll spec that
read its identity tag off a captured node reference. The standing assumption is
that there is a third. So the question I asked was not "do these suites pass"
but "which of their claims survives having its implementation removed".

**Method.** Every mutation ran against its own copy of `git archive HEAD`
unpacked into `/tmp/qa1-scratch/`. The shared worktree was never modified — an
L9-1 agent was measuring against it in the same round. Client-runtime
mutations were rebuilt through `tools/minify` into
`live/clientjs/gotth-live.min.js` before running, because that file and not
`client/runtime.js` is what the browser loads; the rebuilt digest is recorded
per row so a reader can tell a real rebuild from a forgotten one. Pristine is
`f4f5d424a91c46b7`.

### 4.1 The client runtime

| | Mutation (`client/runtime.js`) | Bundle digest | Result |
|---|---|---|---|
| **N1** | `syncProps` writes `checked`/`selected` unconditionally instead of only when the incoming attribute disagrees | `9c8bd88f75c1158f` | **1 red** — FR-25 *keeps checkbox, radio and `<select>` state the server did not declare* |
| **N2** | delegation removed: per-node `addEventListener` at `bind` time, and `apply` no longer re-binds | `c852f88ba778cf42` | **1 red** — FR-28 *keeps a morphed subtree interactive, and a control the morph inserted works on its first click* |
| **N3** | `drop()` never removes a node the server stopped rendering | `81ff2a41ce3087b4` | **1 red** — FR-32 *reverts an HTMX swap into an unpreserved element inside a live fragment* |
| **N4** | `save()` stops capturing scroll offsets | `933093fd848fd624` | **0 red** |
| **N5** | `restore()` is a no-op — neither focus, caret, nor scroll is ever restored | `bfd69fa735995ce6` | **0 red** |

N1, N2 and N3 are the point of the exercise. Each is one line, each expresses
a *plausible alternative implementation* rather than sabotage, and each turned
exactly one spec red — the spec that claims the rule. That is stronger evidence
than the prior round's M1, which turned thirteen red by destroying node
identity: a suite that only detects "every node was replaced" is a suite that
tests one thing thirteen times. N1 in particular shows the checkbox spec is
about the *controlled/uncontrolled rule* and not about identity, which M1 could
not distinguish.

### 4.2 The Go side

| | Mutation | Result |
|---|---|---|
| **N6** | `live.Script` ignores its argument and hardcodes `"/live"` (`live/templ.go`) | **7 of 13 red** in `test/routers` — both non-`/live` prefixes lose the tag spec, the runtime-fetch spec and the live-session spec, plus the one-artifact spec |
| **N7** | the chat composer fragment is `Dirty` when the room moves (`examples/chat/chat.go`) | **3 red** — the FR-55 input-preservation set |
| **N8** | a server-initiated patch's origin source becomes `"unknown"` (`internal/session/effects.go`) | **5 red** — FR-42's provenance spec and four fan-out specs |
| **N9** | a render panic no longer emits an `Error` frame — C-26 reverted (`internal/session/actor.go`) | **1 red** — *answers a render panic with an Error frame naming the event* |
| **N10** | the effect-panic path *also* emits an `Error` frame (`internal/session/effects.go`) | **1 red** — *answers an effect panic with a failure event and no Error frame* |
| **N11** | the message body renders through `@templ.Raw` (`examples/chat/view.templ`, regenerated) | **11 red** — nine payload classes, the one-script-element assertion, and the two-browser wire spec |
| **N12** | `Teardown` no longer unsubscribes (`examples/chat/chat.go`) | **2 red** — the FR-56 leak spec and the roster spec |
| **N13** | `scheduled_by` dropped from the effect-panic log (`internal/session/effects.go`) | **1 red** — `internal/session` *names the event that scheduled a panicking effect in the log record*, failing with the missing key and its value |

N10 deserves a sentence on its own. The PRD's criterion says, in as many words,
*"A test that accepts an `Error` frame here fails this criterion."* N10 makes
the library emit one, and the chat spec named for that clause is the only
thing that goes red. The criterion has a test that can fail it.

### 4.3 The two mutations nothing caught

**N4 and N5 are the finding.** `save()` and `restore()` in `client/runtime.js`
capture and restore the focused element, its selection range, and the scroll
offsets of every identified descendant of the fragment being patched. I removed
the scroll capture (N4), and then made `restore()` a no-op entirely (N5). After
each:

* browser suite: **19 passed, 0 failed** — and **22 passed, 0 failed** with
  `GOTTHLIVE_E2E=1`, so the counter's own browser specs do not catch it either;
* node client suite: **52 tests, 51 pass, 0 fail, 1 todo** — unchanged;
* `ci.sh`: unaffected, since no Go test loads the runtime.

The FR-25 focus, caret and scroll specs pass *without `restore()` existing*,
because morph preserves those nodes in place and there is nothing to restore.
`restore()` exists for the case where the node **was** replaced — a tag change,
a fragment root swap, a `REPLACE` op — and nothing in this repository puts it
in that position.

Measured cost, by deleting both call sites and letting the minifier tree-shake
the bodies, then `(cd tools && go run ./minify -check)`:

| | Minified | Gzipped |
|---|---:|---:|
| Shipped | 9,143 B | 3,874 B |
| Without `save`/`restore` | 8,526 B | 3,631 B |
| **Untested** | **617 B** | **243 B** |

617 minified bytes of an NFR-2 budget the project measures to the byte, and
not one test in the repository fails when they stop working. Recorded as
**D-21**. It is not a bug — the code looks correct — it is 6.7 % of the shipped
artifact standing on nothing, in the one subsystem R-2 says will grow.

I also confirmed the negative direction of the size gate while I was there: an
edit to `client/runtime.js` without a rebuild makes `minify -check` say
`../live/clientjs/gotth-live.min.js is stale`, exit 1. NFR-2's gate is not
vacuous.

### 4.4 Gate-integrity injections

Three edits aimed at the gates themselves rather than at the library.

| | Injection | Result |
|---|---|---|
| **G-a** | one committed `examples/chat/view_templ.go` string literal changed | `gen.sh --check` exit **1**: *`examples/chat/view_templ.go` is not what this generator produces* |
| **G-b** | a new `examples/counter/extra.templ` added and not enumerated | `gen.sh --check` exit **1**: *templ_sources does not match the .templ files in this checkout*, printing listed vs found |
| **G-c** | a badly formatted `.go` file added, then `gofmt` removed from `PATH` | `ci.sh` prints `gofmt: command not found` and then **`clean`**, and records no failure — **D-19** |

---

## 5. This round's three closures, verified on their own merits

### 5.1 D-14 — the bound is 1023, and it rejects rather than clamps

Driven through the exported API only, as an application would meet it
(`live.New` with each value in turn):

```
CoalesceFlushAt=0     accepted            (takes the documented default of 512)
CoalesceFlushAt=512   accepted
CoalesceFlushAt=1022  accepted
CoalesceFlushAt=1023  accepted            ← MaxCoalesceFlushAt
CoalesceFlushAt=1024  REJECTED at construction
CoalesceFlushAt=1025  REJECTED at construction
CoalesceFlushAt=4000  REJECTED at construction
CoalesceFlushAt=-1    REJECTED at construction
```

The 1024 message, in full, because the quality of a validation error is part of
what is being closed:

> `gotth-live: Config.Limits.CoalesceFlushAt is invalid: 1024 is above the
> protocol's ceiling on a patch's contributing-event list, so the flush it
> triggers would build a frame the protocol refuses and the deferred provenance
> would be dropped with it; set it to at most 1023, or leave it zero for the
> default of 512`

Three things confirmed. The bound is **1023**, not the round number my own
write-up pointed at — DEV-1's `+1` arithmetic is right and I was wrong.
Rejection happens at **construction**: `live.New` returns a `*ConfigError` and
no `App`, so there is no running session at an illegal value to hold a property
against. And it **rejects rather than clamps** — 1024 does not become 1023, it
does not start, which is the same call the project already made on
`normalizeMount`. Zero still means "default" because `validate` runs before
`Normalize`. **D-14 closed.**

### 5.2 FR-58 — the effect-panic record names the scheduling event

`internal/session/effects.go:104` emits `obs.U64("scheduled_by", scheduledBy)`
unconditionally, including the zero a server-initiated transition carries.
Verified by removal (N13): with that one line deleted, `internal/session` goes
80 passed / 1 failed, and the failure message is the requirement —

> the effect-panic record names the effect and not the event that scheduled it,
> so an operator reading it can reach `"test.explode"` and cannot reach the
> interaction behind it (FR-58)

— with the whole record printed and `scheduled_by: 1` named as the missing
key. **FR-58 gap closed.**

### 5.3 O-1 — `gen.sh --check` covers the templ outputs

Both directions, §4.4 G-a and G-b. The second is the one that matters more:
the failure mode O-1 described was *a generated-output gate that misses half
the generated output*, and a gate that checks the two files it knows about
would have the same shape one file later. `gen.sh` derives `templ_outputs` from
`templ_sources`, then cross-checks `templ_sources` against a `find` of the
checkout and refuses when they differ. A new `.templ` cannot be silently
uncovered. It also pins the templ CLI version against each module's templ
runtime, which is the drift that would otherwise report as an unexplained
byte difference. **O-1 closed.**

### 5.4 D-15, re-verified rather than accepted

I un-pended the `PIt` and ran it in Chromium 151:

```
Ran 20 of 154 Specs in 9.715 seconds
FAIL! -- 19 Passed | 1 Failed | 0 Pending | 134 Skipped

[FAILED] a <details> the user opened was closed by a patch that never mentioned
it (FR-25). open reflects to the content attribute in a browser, so morph reads
the user's own state as a server declaration and reverts it
```

It fails at `dom_preservation_test.go:866` — the `open` assertion — *after* the
node-identity assertion on the line above has passed. So it is the
controlled/uncontrolled rule and not the traversal, exactly as reported. The
defect is real, the pending spec is well-formed, and it will go green on the
day it is fixed and not before.

---

## 6. Defects — rulings

### D-17 and D-18: confirmed, not renumbered

The round log says QA-1 may renumber these at the gate. **I am not renumbering
them.** Both were found by running code rather than reading it, both are
correctly scoped, and both are already cross-referenced from
`checkpoint-2-conformance.md` and the batch review. Renumbering would cost
every existing reference and buy nothing. What I am doing instead is
reproducing each independently and sharpening what it says.

**D-17 — LOW — confirmed, and the numbers.** `live/app.go:153` routes by
`strings.HasSuffix(r.URL.Path, clientRuntimeFile)`. Mounted at `/live` behind
an ordinary `ServeMux`:

```
/live/gotth-live.min.js              200   9143 B  ETag "f4f5d424…"  max-age=31536000, immutable
/live/a/b/c/gotth-live.min.js        200   9143 B  ETag "f4f5d424…"  max-age=31536000, immutable
/live/not-really-gotth-live.min.js   200   9143 B  ETag "f4f5d424…"  max-age=31536000, immutable
/live/xgotth-live.min.js             200   9143 B  ETag "f4f5d424…"  max-age=31536000, immutable
/live/deadbeef/gotth-live.min.js     200   9143 B  ETag "f4f5d424…"  max-age=31536000, immutable
```

Five URLs, one body, one `ETag`, one year of `immutable`. It is not a
disclosure — the artifact is public and identical — and it is not reachable
from anything the library itself renders, because `live.Script` writes exactly
one `src`. What it is: an unbounded cache-key space in front of a design whose
whole caching story is *one URL per artifact*. A CDN or caching proxy in front
of an application can be made to store the same 9 KB body under arbitrarily
many keys by anyone who can issue requests. **Does not block.** The fix is a
path equality check; the reason it is not urgent is that nothing generates
those URLs.

**D-18 — MEDIUM — confirmed, and the boundary is exactly D-14's.** I
reproduced it independently, and then narrowed it with contributing identifiers
chosen disjoint from anything the library assigns, on `DefaultLimits`, one
effect emitting one event, no coalescing:

```
Contributing=1020   patches=2  errors=0
Contributing=1021   patches=2  errors=0
Contributing=1022   patches=2  errors=0
Contributing=1023   patches=2  errors=0
Contributing=1024   patches=1  errors=1  code=INTERNAL msg="the server could not encode an update" fatal=false
Contributing=1025   patches=1  errors=1  code=INTERNAL msg="the server could not encode an update" fatal=false
```

That is the **same arithmetic and the same boundary as D-14**: the library adds
the `scheduledBy` edge, 1023 + 1 = 1024 is the ceiling and passes, 1024 + 1 =
1025 is refused. So the project now has one bound, 1023, enforced by
`live.New` on the field the *library* uses to trigger a flush and enforced
**nowhere** on the field the *application* fills in — which `live.Event`
documents as *"the one causal field an application sets"*. The state change
happens, the client gets a non-fatal `Error{INTERNAL}` instead of the patch,
and nothing in the exported documentation names 1023.

D-18 is the more serious of the two open library defects and it is the one I
would fix first: it is reachable on defaults, it is silent, and it loses a
patch. **Does not block checkpoint 2** — it is a Phase 3 resilience concern
(FR-51's family) reached through an exported contract change, and the design
question DEV-1 raised (emit path, flush trigger, or both) is real. It should
not reach checkpoint 3's gate open.

### D-15 and D-16, restated now that the whole picture is visible

**D-15 — MEDIUM, and it is a checkpoint-2 exit-criterion failure, not just a
defect.** I said MEDIUM before on impact grounds and I still do: a reverted
disclosure widget is a bad experience, not a data loss. What has changed is the
*gate* reading. The criterion is *"DOM conformance suite green across NFR-7's
browser matrix for **every case in FR-25**"*, and FR-25 names `<details>` open
state explicitly. One named case fails. That makes criterion 7 not-met as
written, independently of the browser-matrix question, and PM-1 has to either
see it fixed or amend FR-25 — the same choice, one requirement down, that
NFR-7 presents.

The general shape is the part I want on the record: the controlled/uncontrolled
rule keys on *attribute presence in the incoming markup*, and that is unsound
for **any** attribute a browser reflects from user state. `<details open>` is
the case FR-25 names; `<dialog open>` has it; so does any custom element that
reflects internal state. Nothing in either suite checks the general form. A
property test over the reflected-attribute set is the right instrument.

**D-16 — LOW, downgraded from LOW-MEDIUM.** It was reported as LOW-MEDIUM with
the note that it needs "either a documented rule or a runtime option". Having
now run the FR-30/31/32 suite myself, the good half is stronger than the
reported severity implies: a control that existed at first paint keeps working
through two morphs with node identity intact and no `htmx.process` call, which
is the case an HTMX application actually spends its time in, and it is exactly
what FR-28's delegation argument predicts for a third-party library too. The
gap is confined to markup a morph **inserts** mid-session, the remedy is one
documented line, and the spec that records it is written to go red on the day
the gap closes. **LOW, documentation. Does not block.**

### New this round

**D-19 — LOW — `ci.sh`'s gofmt step reports `clean` when `gofmt` is absent.**
Every other step in that script either fails loudly or announces a skip;
`staticcheck` has an explicit `command -v` guard three lines below. gofmt does
not, and the idiom that reports it —

```bash
unformatted="$(gofmt -l . | grep -v '^$' || true)"
```

— cannot distinguish "no files are unformatted" from "the tool does not exist",
because both produce empty output. Proved in §4.4 G-c: with a deliberately
unformatted file present, the same step says `FAILS as it should` when gofmt is
on `PATH` and `reports CLEAN` when it is not. This is reachable today — the
project's own dev image under `bash -lc` produces that `PATH` (§2.1). It is the
failure the script's own header exists to prevent, in the script itself:
*a requirement whose gate is a tool nobody runs is a requirement in name only.*
One `command -v gofmt` guard. **Does not block**, because the GitHub workflow
uses `bash ci.sh` with the image's own `PATH`, where gofmt is present.

**D-20 — HIGH — the browser conformance suites run nowhere in CI. BLOCKING.**
`.github/workflows/gotth-live-checks.yml` has two jobs. The `library` job runs
`bash ci.sh` in `dis-gotth-live:latest`, which has no browser, so all nineteen
DOM-preservation and HTMX specs skip with a printed reason — seven directly and
twelve through the `BeforeAll` of an `Ordered` container — along with nine more
behind `GOTTHLIVE_E2E`, three of which are the counter's own browser and CSP
specs (§2.3). The `client` job runs the three node suites and then this:

```yaml
- name: Confirm the browser the checkpoint-2 suites need is present
  run: |
    docker run --rm dis-gotth-live-bench:latest \
        bash -c 'chromium --version && node --version'
```

The step is named for the checkpoint-2 suites and it stops one line short of
running them. So the evidence for exit criteria 7, 8, 9 and the browser half of
FR-26/FR-27 is, in CI terms, **a version string**. The suites are green — I ran
them, twice, and broke them five ways — but green-because-a-human-ran-it is the
state checkpoint 1 blocked on and the state `ci.sh`'s own header calls *a suite
that is green because it never ran*.

This is a one-file change: a third step in the `client` job invoking the bench
image with `-ginkgo.label-filter=browser`, plus `GOTTHLIVE_E2E=1` if the
counter's browser specs are wanted (they add 3 specs and ~13 s). No image
rebuild, no new dependency, nothing to design. I am blocking on it because the
alternative is a checkpoint whose headline criterion is enforced by nobody, and
because the same argument at checkpoint 1 was accepted and produced `ci.sh`.

**D-21 — LOW — `save()`/`restore()` is 617 minified bytes with no covering
test.** §4.3. Not a bug; a coverage hole in shipped bytes. The right fix is a
spec that forces a replace — a fragment root whose tag changes, or a `REPLACE`
op — and asserts focus, caret and scroll are restored across it. **Does not
block.** It should not survive Phase 4, where the runtime's byte ledger gets
quoted as a feature.

### The full open set at this gate

| | Severity | Owner | Blocks checkpoint 2? |
|---|---|---|---|
| **D-20** — browser suites unenforced in CI | HIGH | DEV-1 | **YES — merge-blocking** |
| **D-15** — `<details>` open state reverts (FR-25 case) | MEDIUM | DEV-2, then PM-1 | **Criterion 7 not met as written.** Fix or amend FR-25 |
| **NFR-7 at 1 of 8 cells** | — | PM-1, then L9-1 | **Criterion 7 not met as written.** §7 — scope decision, not QA's |
| **D-18** — application `Event.Contributing` unbounded at the same 1023 | MEDIUM | DEV-1, then L9-1 | No. Must not reach checkpoint 3's gate open |
| **D-16** — `hx-*` inserted by a morph is inert until `htmx.process` | LOW | DEV-3 (docs) | No |
| **D-17** — the runtime artifact answers at unbounded URLs | LOW | DEV-1 | No |
| **D-19** — `ci.sh`'s gofmt step passes without gofmt | LOW | DEV-1 | No |
| **D-21** — `save`/`restore` uncovered, 617 B | LOW | DEV-2 | No |

Carried from checkpoint 1 and unchanged by this gate: **D-10** (QA-2, Phase 3),
**D-12's second half** (L9-1, due in this same pass), **D-13**, and the
`instrumentation.md` §3 disagreements (DEV-1 + L9-1).

---

## 7. NFR-7 — what is verified, stated for PM-1

The scope call is PM-1's and I am not making it. What I owe PM-1 is a set of
measurements rather than impressions, so here is what I confirmed myself rather
than taking from the browser write-up.

**Verified, by running it:**

* **Chromium 151.0.7922.71**, headless, Debian 13.6, x86-64: all 19
  DOM-preservation and HTMX-coexistence specs pass, and 22 with the counter's
  own browser and CSP specs enabled. Five independent mutations turn the
  claimed specs red.
* That is **one cell** of NFR-7's eight.

**Not verified, with the obstruction measured rather than assumed:**

* **No Firefox exists in either project image.** `dpkg -l` and `ls /usr/bin` in
  `dis-gotth-live-bench:latest` return nothing for `firefox`, `firefox-esr`,
  `webkit` or `epiphany`. The prior round's measurement — a throwaway
  `firefox-esr 140.13.0esr` speaks WebDriver BiDi and answers
  `GET /json/version` with 404 — stands, and I confirmed the harness half of
  it directly: `cdp_test.go` bootstraps through `GET /json/version` →
  `webSocketDebuggerUrl` and drives `Page.navigate` / `Runtime.evaluate`. It
  speaks Chrome DevTools Protocol and nothing else. Firefox is a second
  protocol client plus an image change, not a flag.
* **No second Chrome build exists in any image.** The bench image installs
  Debian's rolling `chromium` package, which carries one version
  (`151.0.7922.71-1~deb13u1`).
* **No WebKit anywhere, and no macOS host.** Safari macOS ×2 and Safari iOS ×2
  are not obtainable on this infrastructure at any effort level.

**What that leaves.** Six of eight cells are not measurable without a decision
about infrastructure, and one (Chrome previous-stable) is not measurable
without pinning a second browser download into an image. The engine-independent
parts of these specs — the wire protocol, the traversal, node identity — say
nothing about Gecko or WebKit. The three cases most likely to diverge across
engines are precisely the ones that needed browser evidence in the first place:
caret behaviour on `setSelectionRange` after an attribute write, IME
composition semantics, and `Element.getAnimations()` for CSS transitions.

**And one thing NFR-7's wording does not currently capture,** which PM-1 should
decide alongside the matrix: D-15 is a *specification* problem, not an engine
problem. `open` reflecting to a content attribute is standard behaviour that
every engine implements; adding Firefox would not find it and has not hidden
it. A matrix amendment that reads "what CI can verify" should not be written in
a way that implies the FR-25 gap is about coverage.

---

## 8. Conditions

**Merge-blocking (QA-1's authority, must clear before checkpoint 2 signs):**

1. **D-20 — run the browser suites in CI.** A step in the `client` job of
   `.github/workflows/gotth-live-checks.yml` invoking
   `dis-gotth-live-bench:latest` with
   `go test ./test/internal/conformance/ -args -ginkgo.label-filter=browser`.
   Decide explicitly whether `GOTTHLIVE_E2E=1` is included; if it is not, say
   in the workflow why the counter's browser specs are excluded, because a skip
   nobody explains becomes a skip nobody notices. Owner DEV-1.

**Requires a PM-1 decision, not a fix (QA-1 does not descope):**

2. **NFR-7's matrix.** Either add a WebDriver BiDi harness and a Firefox to the
   bench image — two more cells, at the cost of an image change and a second
   protocol client — or amend NFR-7 to state the matrix CI can actually run and
   say in the README which browsers are *supported by intent* versus *verified
   by test*. §7. Leaving criterion 7 quietly marked green on one engine is the
   one option I will not sign.
3. **D-15 and FR-25's `<details>` clause.** Either fix it before the checkpoint
   signs, or amend FR-25 to say what the controlled/uncontrolled rule can
   actually promise for reflected attributes, and record the general shape
   (`<dialog>`, custom elements) rather than the one tag. Owner PM-1 with
   DEV-2.

**Recorded, not blocking:** D-16, D-17, D-18, D-19, D-21. D-18 should not reach
checkpoint 3's gate open.

**Housekeeping, not a defect and not acted on** — the shared worktree is a live
workspace and another agent was building in it: `examples/chat/go.sum`,
`examples/counter/go.sum` and `test/routers/go.sum` are owned by `root` from
container runs, as are the built `chat` and `counter` binaries (gitignored),
and there is a stray empty root-owned `gotth-live/candace/pkg/gotth/tools/`.
`gen.sh` hands its templ outputs back to the invoking user and does not do the
same for the `go.sum` files a `go test` in a module directory may rewrite.
Worth one line in `gen.sh`'s neighbourhood; not worth a defect number.

---

## 9. Sign-off

**QA-1 passes checkpoint 2 with conditions.** The work of this round is real
and it was tested adversarially rather than confirmed: 151 chat specs, 19
browser specs, 13 mount specs, 123 conformance specs, 52 node tests and 84
`live` specs all green, and thirteen mutations of my own — eleven of which
turned red in exactly the place they claim to protect. `ci.sh` is exit 0 with
the FR-7 gate running, the surface matches its ledger to the identifier, and
the client is 3,874 B of 12,288.

The conditions are not about the code that landed. Two of them are the same
sentence in different clothes — *the checkpoint's headline criterion is green
in one place and enforced in none* — and the third is a requirement that names
a case the implementation cannot currently satisfy. All three are cheap to
close and none of them is a design problem.

Two things this gate found that were not on anyone's list, and both are the
same species as the defects this project keeps catching only by running: the
gofmt step that passes when gofmt is missing (**D-19**), and 617 shipped bytes
of focus-and-scroll restoration that no test in the repository can tell has
stopped working (**D-21**). Neither blocks. Both are worth more than the effort
of finding them, because each is a place where a green result was standing on
nothing.

— QA-1, 2026-08-04

---

*Reproduce this gate:*

```bash
REPO_ROOT=<repository root, not gotth-live/>

# the whole gate, with FR-7 able to run — note bash -c, NOT bash -lc
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live dis-gotth-live:latest \
    bash -c 'bash ci.sh; echo "CI_EXIT=$?"'

# the node client suite (NFR-4), which ci.sh announces as skipped
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live dis-gotth-live-bench:latest \
    bash -c 'for f in client/test/*.test.mjs; do node --test "$f"; done'

# the checkpoint-2 browser suites
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live dis-gotth-live-bench:latest \
    bash -c 'go test ./test/internal/conformance/ -count=1 -v -timeout 20m \
             -args -ginkgo.label-filter=browser -ginkgo.v'

# … including the counter's own browser and CSP specs
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live dis-gotth-live-bench:latest \
    bash -c 'GOTTHLIVE_E2E=1 go test ./test/internal/conformance/ -count=1 -v \
             -timeout 30m -args -ginkgo.label-filter=browser -ginkgo.v'

# the browser inventory behind §7
docker run --rm dis-gotth-live-bench:latest bash -c \
    'chromium --version; node --version; ls /usr/bin | grep -iE "firefox|webkit|epiphany" || echo "none"'
```

*Every mutation in §4 ran against `git archive HEAD | tar -x -C /tmp/...`, one
copy per mutation, rebuilt through `tools/minify` where the client runtime was
involved. The shared worktree was never modified.*
