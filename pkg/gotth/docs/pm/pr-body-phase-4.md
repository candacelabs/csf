# PR #49 body — the Phase-4 replacements

| Field | Value |
|---|---|
| Author | **PM-1** |
| Date | 2026-08-05 |
| Tree | `8a06cb04` for §§1–2; **`b04ba138` for §5**, on `dev-/gotth-live-orchestrator-c3efc4` |
| For | the **orchestrator**, to apply with `gh api -X PATCH repos/…/pulls/49` (see §4 — `gh pr edit` fails here) |
| PR | the source monorepo's pull request #49 |
| Why | Two pieces of the body are false as of `cac72589`, and a stale row is a defect. [`docs/gates/phase-4.md`](../gates/phase-4.md) §7.2 |
| Status | §§1–2 **applied** at `e784e49b` (§4); **§5 applied** at `857d0bd9` (§6); **§7 applied** at `e2b9899b` (§8); **§9 applied** at `b777f12e` (§10) — **all four of its blocks, including §9.4, which was offered and not required, plus one orchestrator delta PM-1 asked for in §9.5 and did not draft.** Nothing in this file is outstanding. *(What this row said while §9 was outstanding, kept beneath itself rather than replaced, because the warning it carried is the reason §9 exists: "**§9 is OUTSTANDING** — drafted by PM-1 at `e751f6de`, three replacement strings plus one offered and not required (§9.4), for the orchestrator to apply.")* *(What this row said before §9 was drafted, kept beneath itself rather than replaced, because it was true of every tree between `e2b9899b` and `e751f6de`: "Nothing in this file is outstanding. The body now says **twelve** of thirteen boxes are ticked and that FR-53 is **met at exactly 31 with zero margin**." Both of those are still true. **What §9 replaces is not a count** — it is a Phase 3 row saying the gate has not been re-held when it has, a Phase 4 row saying three things about FR-54 that were true last round and are now all false, and a status blurb whose FR-54 clause describes the tree at revision 4. **All three are stale in the direction that under-reports**, which is the direction §5.3 named and this file has now caught three times.)* *(And the row above this one still holds: this file's earlier warning read "**§7 is OUTSTANDING**" until the orchestrator applied it, and the defect it warned about — a shipped row carrying a stale count **and** a stale threshold — is what §7 replaced.)* |

**What is false, exactly.** The status quote block near the top says this PR goes
ready-for-review only after the docs gate is held, *"which has not happened"*. It
happened at `cac72589` and it **passed**. The Phase-4 table row says the gate is
unheld and closes with *"no QA gate has been held on the docs at all"*.

**Scope of these replacements.** Two strings: one sentence inside the status
quote block, and the whole `| 4 — DX & docs … |` table row. **Nothing else in the
body changes** — not the Phase 5 row, not the CI paragraph, not the memory
numbers, not the client-size figures. Every figure below is traceable: the gate
record is `docs/qa/phase-4-docs-alone.md`, the byte figures are `client/SIZE.md`
§2.1 and §2.2, the 268 is `1370229c`'s commit body, and the 46 is counted by PM-1
at HEAD by the quickstart's own method.

---

## 1. The status blurb — replacement sentence

**Find**, inside the `> **Status: DRAFT …**` quote block:

```
This PR goes ready-for-review only after the docs gate is actually held — QA-1 building a working app from the documentation alone, which has not happened — and the headline benchmark report lands with real measurements.
```

**Replace with:**

```
The docs gate has since been held and **passed** — QA-1 built a working app from the documentation alone, in 2m12s, with zero source-diving breaches — and Phase 4 still does not exit: six of its thirteen boxes are ticked and seven are not. This PR goes ready-for-review after those seven close and the headline benchmark report lands with real measurements.
```

---

## 2. The Phase-4 table row — replacement row

**Find** the row beginning `| 4 — DX & docs (quickstart, templ helpers, provenance inspector, docs-alone gate) |`.

**Replace with** (one line, no raw newlines, markdown-table-safe — the only `|`
characters are the three column separators):

```
| 4 — DX & docs (quickstart, templ helpers, provenance inspector, dev reload, godoc gate, docs-alone gate) | QA-1 builds an app from the docs alone | 🔄 in progress — **the gate has been held and it PASSED, and Phase 4 still does not exit.** QA-1 built a working counter from [`quickstart.md`](gotth-live/docs/quickstart.md) alone in **2m12s**, compiled first attempt, clicked in real chromium 0→1→2→3 with zero console errors and **zero source-diving breaches**: 8 findings, **0 blockers**, 3 high-severity friction ([gate record](gotth-live/docs/qa/phase-4-docs-alone.md)). QA-1 published the counterweight themselves and it is carried rather than dropped: **the gate measures a document that is copy-paste-correct, not one that survives being deviated from** — both high-severity findings were found by deliberately building the *wrong* variant, and in both the page's own troubleshooting text sent the reader the wrong way. DEV-3 fixed seven of the eight, declined one with a reason, and routed the one whose real fix is an API change; a top-level [`README.md`](gotth-live/README.md) now exists, under the same drift check as the guide. **FR-53's ≤30-line budget is still missed at 46** — 27 Go + 19 templ — reproduced independently by QA-1 and re-counted after the remediation, which moved it by zero; the box is a conjunction, the ≤15-minute half passes, and raising 30 was pre-registered as unavailable, so closing it needs the app to shrink (12 of the 27 Go lines are `Config` fields `live.New` demands). **Dev reload ships** (FR-57): a build identity polled over HTTP, third client artifact at **1,260 B gzipped against an 8,192 B ceiling**, the shipped runtime unmoved at 10,391/4,429 and **no new dependency**; measured in headless chromium, a templ change reloaded in **1,810ms**, a Go change in **2,715ms**, and a rebuild that changed no bytes reloaded nothing. **A godoc gate exists where FR-66 and FR-68 had named CI while nothing ran**: `tools/doccheck`, wired into `ci.sh`, four rules, its own tests including one asserting it cannot pass by walking an empty tree — **142 undocumented exported symbols found and fixed**, `Example*` 2 → 6 all with `// Output:`. **Its scope is narrower than FR-66's words and that is reported, not buried: 268 undocumented symbols sit outside the enforced module boundary**, printed on every CI run; PM-1 ratified the narrowing as a PRD amendment rather than letting a tick absorb it. **What is still open: seven boxes**, six of them on work nobody has done rather than on any disagreement — `docs/exceptions.md` (FR-20) **does not exist at all**, there is no deployment page (FR-59), the error-message audit (FR-58) is unstarted, G11's clean-clone `go run` has never actually been run, the examples' "polished and documented" clause is graded by nobody, and FR-54's "complete" has never been defined — plus FR-53's 46. **And the browser evidence for dev reload and the inspector came from harnesses that were thrown away**, so neither is guarded by CI; that is a named condition on the exit, not a footnote. [PM-1's interim gate record](gotth-live/docs/gates/phase-4.md) has every box, every owner, and which carried items are conditions |
```

---

## 3. Notes for whoever applies this

- **The row's first column changed too.** It gained "dev reload" and "godoc
  gate", because the parenthetical was written before either existed and a
  column that under-names what a phase contains is the same defect one size
  smaller.
- **The honesty register is the body's own.** The row names the 46-line miss, the
  268 unenforced symbols and the uncommitted browser harnesses at the same
  prominence as the PASS. A row reporting only the PASS would repeat the exact
  defect that made this file necessary — a claim written once about a moment and
  never re-derived — in the opposite direction.
- **Do not fold the Phase 3 remedy sentence into this row.** The Phase 3 box is
  still not ticked by PM-1, and the Phase 3 row already says so accurately.
- **If the whole-gate `ci.sh` run in flight at `8a06cb04` comes back red**, the
  clause about the godoc gate is the one to qualify: what is quoted green today
  is DEV-1's run of the four tools-module steps at `1e59bb04`, not a whole-gate
  run at HEAD. The gate record's §2.1 says which tree each green belongs to.

---

## 4. Applied — orchestrator, 2026-08-05

Applied to PR #49 at `e784e49b`, with **two deltas from §§1–2 above**, recorded
here rather than left as silent drift between what PM-1 wrote and what shipped:

1. **268 → 269.** §2's row cited `1370229c`'s measurement. The condition PM-1
   attached to the ratification — reconcile the figure to one value — was
   discharged at `e784e49b` by making the run's printed number the tree's
   answer: `tools/doccheck`'s two prose figures now either date themselves to
   the tree they were measured at (`452e1e74`, 359 of 410 struct fields, which
   is the argument's evidence and does not move) or are gone. The live figure
   is what the gate prints, and at HEAD it is **269**. That is what the PR row
   says.
2. **The "gate, quoted rather than asserted" paragraph was rewritten**, which
   §2 did not cover, because the in-flight run PM-1 anticipated **did come back
   red** — and on FR-7, not on the godoc gate. The new godoc gate found
   `internal/refine/refine.go`'s `Error()` undocumented; a comment was written
   into a file `gen.sh` copies verbatim out of `research/`; `gen.sh --check`
   then reported the generated code did not match its generator. Fixed at the
   generator (`gen.sh` now emits the canonical `// Code generated … DO NOT
   EDIT.` line, so `doccheck`, `go vet` and `staticcheck` all recognise the
   file by the one convention they already share), and the whole gate re-run at
   `e784e49b` in `dis-gotth-live-bench:latest`: **exit 0, zero skipped steps**.
   Both runs are in the paragraph. §2's godoc clause needed no qualification.

Nothing else in the body changed — not the Phase 5 row, not the memory numbers,
not the Phase 3 row. The one addition outside §§1–2 is the dev-reload artifact's
1,260 B in the client-size sentence, at the same prominence as the other two.

`gh pr edit --body-file` fails on this repository with a GraphQL error about
Projects (classic) being deprecated; `gh api -X PATCH repos/…/pulls/49` with a
JSON body works and is what applied this.

---

## 5. Revision 3 replacements — PM-1, 2026-08-05, at `b04ba138`

**The body is stale again, and this file exists because that is a defect rather
than a fact of life.** §§1–2 were applied at `e784e49b` and were true of
`8a06cb04`. They are now false in the direction §7.2 of the gate record named as
the one nobody checks: **the body under-reports a result we earned.** It says six
of thirteen boxes are ticked; **eleven are.** It says FR-53 misses at 46; it
misses at **39**. It says `docs/exceptions.md` *"does not exist at all"*; it
exists, is walked, and is **signed by L9-1**.

**Every figure below is traceable**: the four QA-1 grades are
[`docs/qa/phase-4-grading.md`](../qa/phase-4-grading.md) (`954afa9a`, re-verified
`3fe09676`), the signature is `bdf91971` with L9-1's note at
[`docs/reviews/phase-4-exceptions.md`](../reviews/phase-4-exceptions.md), and the
**39** is counted by PM-1 at HEAD by the quickstart's own method and independently
by QA-1 over the shipping sample.

### 5.1 The status blurb — replacement sentence

**Find** the sentence applied at `e784e49b`, beginning *"The docs gate has since
been held and **passed**"*.

**Replace with:**

```
The docs gate has since been held and **passed** — QA-1 built a working app from the documentation alone, in 2m12s, with zero source-diving breaches — and Phase 4 still does not exit: **eleven of its thirteen boxes are ticked and two are not**. QA-1 has since graded four more boxes and passed all four, and L9-1 has signed the functional-discipline exceptions register. The two that remain are a measurement PM-1 now argues was never reachable (FR-53's ≤30 lines, missed at 39) and a requirement whose key word was undefined until this week (FR-54's "complete", now defined and failing on three named gaps). This PR goes ready-for-review after those two close and the headline benchmark report lands with real measurements.
```

### 5.2 The Phase-4 table row — replacement row

**Find** the row beginning `| 4 — DX & docs (quickstart, templ helpers, provenance inspector, dev reload, godoc gate, docs-alone gate) |`. **Note that the row on the PR is not byte-identical to §2 above** — §4 records the two deltas the orchestrator applied — so match on the leading column, not on the full string.

**Replace with** (one line, no raw newlines, markdown-table-safe):

```
| 4 — DX & docs (quickstart, templ helpers, provenance inspector, dev reload, godoc gate, error audit, exceptions register, docs-alone gate) | QA-1 builds an app from the docs alone | 🔄 in progress — **the gate PASSED, eleven of thirteen boxes are ticked, and Phase 4 still does not exit.** QA-1 built a working counter from [`quickstart.md`](gotth-live/docs/quickstart.md) alone in **2m12s**, compiled first attempt, clicked in real chromium 0→1→2→3, **zero source-diving breaches**: 8 findings, **0 blockers**. QA-1 published the counterweight themselves and it is carried rather than dropped: **the gate measures a document that is copy-paste-correct, not one that survives being deviated from** — both high-severity findings came from deliberately building the *wrong* variant, and in both the page's own troubleshooting text sent the reader the wrong way. **QA-1 has since graded four more boxes and passed all four** ([grading pass](gotth-live/docs/qa/phase-4-grading.md)): the error-message audit (FR-58 — **117 error sites enumerated, 25 graded failures, 25 fixed**, and QA-1 re-implemented the enumeration rule from the document's *prose* in their own AST program rather than re-running ours), the clean-clone property (G11 — a real `git clone` into stock `golang:1.25-bookworm` with node/npm/protoc/refinec **proved absent fatally**, plus a negative control QA-1 invented: an identical image with a `node` shim on PATH, which the runner refuses), the docs set (FR-59 — nine subjects, every default and close code checked against the shipping source, and QA-1 drove the architecture page's sharpest claim with a **control**), and the examples' polish clause — **which QA-1 FAILED first**, on six places where the tree described the world before a testing-API migration; DEV-3 remediated, put the spec counts under a check whose vacuous-pass path QA-1 then broke on purpose, and **QA-1 overturned one of their own prescriptions in DEV-3's favour** because complying would have made a note state something false. **L9-1 has signed the exceptions register** (FR-20): two functional-discipline deviations found by a walk of a file that had never existed since Phase 1 — one **fixed**, one **accepted** with an argument, and L9-1 corrected three of the register's own numbers before signing, including that its walk commands did not print its own counts. **FR-53's ≤30-line budget is still missed, at 39** — 20 Go + 19 templ, down from 46 after a library change that made a documented hazard unwritable; the box is a conjunction and the ≤15-minute half passes. **PM-1's argument, owed since the budget was set: 30 was never reachable** — hiding the entire HTML document inside a library component lands at **31**, and the only remaining line is a security-hook bundle this project has refused twice on review-signal grounds. **The number is not being moved in the pass that measured it**; the amendment is pre-registered with the measurement on the record. **Dev reload ships** (FR-57): third client artifact at **1,260 B gzipped against an 8,192 B ceiling**, shipped runtime unmoved, **no new dependency**; a templ change reloaded in **1,810ms**, a Go change in **2,715ms**, and a byte-identical rebuild reloaded nothing. **A godoc gate exists where FR-66/FR-68 had named CI while nothing ran**: **142 undocumented exported symbols found and fixed**, `Example*` 2 → 6 all with `// Output:`, and **its scope is narrower than FR-66's words, reported rather than buried** — the unenforced count prints on every run and the prose figure was deleted precisely because it had been written three different ways. **What is still open: two boxes, and both are decisions rather than chores** — FR-53's number, and FR-54's helper set, which fails a completeness definition PM-1 wrote this week on three named gaps (a keyboard chord that is inexpressible, a debounce that is read from the element so two bindings on one control share one timer, and a friction note that outlived the API that closed it). **The browser evidence for dev reload and the inspector still comes from harnesses that were thrown away**, so neither is guarded by CI — that is the oldest unaddressed condition in the gate record and it is named as one, not as a footnote. [PM-1's interim gate record](gotth-live/docs/gates/phase-4.md) has every box, every owner, and three corrections to its own prior revisions |
```

### 5.3 Notes for whoever applies this

- **The first column gained "error audit" and "exceptions register"**, for §3's
  reason unchanged: a column that under-names what a phase contains is the same
  defect one size smaller.
- **The honesty register is still the body's own.** The row names the 39-line
  miss, the FAIL that preceded a PASS, the three FR-54 gaps, and the uncommitted
  browser harnesses at the same prominence as the five ticks. **A row reporting
  only that eleven boxes now tick would repeat the exact defect that made this
  file necessary** — §7.2 of the gate record — in the flattering direction this
  time, which is the direction that attracts no second reader.
- **Do not describe Phase 4 as nearly done.** Eleven of thirteen is true and
  misleading on its own: one of the two remaining boxes is a budget PM-1 has just
  argued is unreachable as written, and it may close by amendment rather than by
  work. The row says so.
- **Do not fold the Phase 3 remedy sentence into this row.** Unchanged from §3.
- **This file found a defect in the gate record while being written**, and it is
  worth knowing why: §4 above records the orchestrator discharging the godoc-count
  condition at `e784e49b`, **an hour before revision 2 of the gate record was
  written carrying it as open**. Revision 3 nearly carried it a third time.
  §7.8 correction 3.

## 6. Applied — orchestrator, 2026-08-05, at `857d0bd9`

§§5.1–5.2 applied to PR #49 **verbatim, with zero deltas.** That is worth
recording as its own fact: §4 had two, and the reason this one has none is that
§5 was written against a tree that had stopped moving under it — every figure it
cites (`954afa9a`, `3fe09676`, `bdf91971`, the 39, the eleven of thirteen) was
already committed when PM-1 wrote the replacement, where §2's 268 was a
measurement still in flight.

Applied mechanically rather than by hand, because the row is 4,379 characters on
one line and a hand-edit of that is a silent-corruption risk: the two fenced
blocks were extracted from this file by a script, the status sentence matched on
its opening clause through `lands with real measurements.`, and the table row
matched on the leading column only — as §5.2 instructs, since the shipped row was
never byte-identical to §2's. The script asserted exactly one match for each
before writing, that the row contained no newline, and that the applied body
still carried the footer.

**What the body says after this, checked against the body and not against the
intent:** `eleven of its thirteen boxes are ticked` appears once, `six of its
thirteen` is gone, and FR-53 reads `still missed, at 39`.

Gates green at the pushed tree: `ci.sh` **exit 0** (with its four documented
out-of-context skips), `gen.sh --check` **byte-identical**, and G11's runner
**PASS on all three examples** from a real clone in stock
`golang:1.25-bookworm`, re-run because `examples/chat`'s `go.mod` and `go.sum`
moved under `go mod tidy` this turn and a clean-clone gate that is not re-run
after a dependency edit is a gate that graded a different tree.

`gh pr edit --body-file` still fails on this repository with the Projects
(classic) GraphQL error; `gh api -X PATCH repos/…/pulls/49` applied it, as §4.

---

## 7. Revision 4 replacements — PM-1, 2026-08-05, at `73fd1e34`

**§5 is now false in the flattering direction, which is the direction §5.3
warned about and did not anticipate happening to itself.** The body says
*"eleven of its thirteen boxes are ticked and two are not"* and *"FR-53's
≤30-line budget is still missed, at 39"*. **Twelve are ticked, one is not, and
FR-53 is met at 31.** The budget in that sentence has also been **≤31 since PRD
v1.1**, so the row has carried a stale *threshold* as well as a stale count.

**§§1–2 and §5 stay exactly as they are.** They are dated replacement blocks and
they were true of the trees they name; this file's whole method is that a
superseded block is shown beside its replacement rather than overwritten.

### 7.1 The status blurb — replacement sentence

**Find** the sentence applied at `857d0bd9`, beginning *"The docs gate has since
been held and **passed**"*.

**Replace with:**

```
The docs gate has since been held and **passed** — QA-1 built a working app from the documentation alone, in 2m12s, with zero source-diving breaches — and Phase 4 still does not exit: **twelve of its thirteen boxes are ticked and one is not**. The box that had been open longest closed this week and it closed by engineering rather than by moving its number: FR-53's ≤31-line quickstart budget is **met, at exactly 31 with zero margin**, after a library-owned page shell absorbed the hand-written HTML document — built by DEV-1, gated by the principal engineer against nine constraints written before the component existed, and re-counted and graded by QA-1, who graded it **PASS with four conditions**. The one box left is FR-54's templ helper set, which fails a completeness definition on three named gaps; one of the three was a derivation and is now **measured in a real browser**, and it turned out to be worse than derived. This PR goes ready-for-review after that box closes and the headline benchmark report lands with real measurements.
```

### 7.2 The Phase-4 table row — replacement row

**Find** the row beginning `| 4 — DX & docs (quickstart, templ helpers, provenance inspector, dev reload, godoc gate, error audit, exceptions register, docs-alone gate) |`.

**Replace with** (one line, no raw newlines, markdown-table-safe):

```
| 4 — DX & docs (quickstart, templ helpers, provenance inspector, dev reload, godoc gate, error audit, exceptions register, docs-alone gate) | QA-1 builds an app from the docs alone | 🔄 in progress — **the gate PASSED, twelve of thirteen boxes are ticked, and Phase 4 still does not exit.** **FR-53 is met**, and how it was met is the part worth reading. The box asks for a working counter in ≤15 minutes and ≤31 lines of application code and had been open since the budget was set. It closed **by the app shrinking, not by the number moving**: DEV-1 gave the library a page shell that owns the doctype, `<html>`, the charset, the title and all three script tags, which took the quickstart's templ half from 19 counted lines to 11 — **20 Go + 11 templ = 31, against a budget of 31, margin zero**. The number itself was fixed **before that component existed**, from the fields the library requires and the shape of an HTML document, and the ratchet protecting it was repaired **before** the shell landed rather than in the same change — verified by commit ancestry, because under the unrepaired version the budget would have moved up to whatever the shell cost and the requirement could not have failed at any price. **All five of the budget's pre-registered re-open triggers were evaluated and none fired**; at 32 the budget would not have moved and the amendment would have been withdrawn with the box open, and the reviewer disclosed *before* the build that two of their nine constraints could each have cost that line. **The principal engineer gated the component before it was counted** and failed it — not on bytes but on a **sentence**, made in five places, claiming the component made a script-ordering bug inexpressible: a probe of the library's own public API put the runtime tag above the inspector and blinded it silently. DEV-1 fixed the **code** rather than the sentence, at **zero new exported symbols**, and the reviewer then broke the component **seven ways and killed seven mutants** before accepting. QA-1's grade carries **four conditions** and they are published rather than folded in: the printed build path was missing a step and errored for **every** reader; a troubleshooting row pointed at a log the counted application cannot write; the counting rule was ambiguous in a way worth **7 lines** on a clause with none to spare; and **nothing in the tree could fail if the count went to 32** — there was no line-count check anywhere, and the pin credited with holding the two counting paths together does not hold a count in either direction. That last one is now a gate with its budget authorised by the party that owns the budget rather than by the party measured by it. **What is still open is one box: FR-54's helper set**, on three named gaps — a keyboard chord that is inexpressible, a debounce read from the element so two bindings on one control share one timer, and a friction note that outlived the API that closed it. The second was a derivation from three source files and has now been **driven in Chromium against the real runtime**: the composed Escape-to-clear is **not delayed, it is destroyed** — no error, no console warning, nothing on the wire — the interference runs **both ways**, and a mutation control turns three of eight specs red, so the checks can fail. The third's *reason* has been corrected and the *affordance* is still absent, so the example's own source still tells a reader the library cannot do something it does. **The browser evidence for dev reload and the inspector still comes from harnesses that were thrown away**, and the harness that would settle it has now been written twice by two different agents and discarded both times — that is the oldest unaddressed condition in the gate record and it is named as one. [PM-1's interim gate record](gotth-live/docs/gates/phase-4.md) has every box, every owner, and **five corrections to its own prior revisions, three of which exist because it deferred something rather than because it got a fact wrong** |
```

### 7.3 Notes for whoever applies this

- **The honesty register is still the body's own, and this is the revision where
  that costs something.** §5.3 said a row reporting only that eleven boxes tick
  *"would repeat the exact defect that made this file necessary… in the
  flattering direction, which is the direction that attracts no second reader."*
  **This row reports a box closing and it names, at the same prominence: the zero
  margin, the four conditions, the reviewer's failed constraint, the still-open
  box, the still-thrown-away harnesses, and five corrections the gate record owes
  itself.** A row that reported "twelve of thirteen" and stopped would be the
  warning in §5.3 arriving one revision later.
- **Do not describe Phase 4 as nearly done, still.** Twelve of thirteen is true
  and misleading on its own for a new reason: the remaining box needs an **API
  decision** on a defect that was measured this week, not a chore.
- **Do not fold the Phase 3 remedy sentence into this row.** Unchanged from §3
  and §5.3.
- **The margin sentence is not decoration and should not be cut for length.**
  The count is 31 against 31. **A body that says "met" without saying "with zero
  margin" is a body that will be wrong the first time somebody adds a line**, and
  the check that would catch that is one QA-1 has not yet watched fail.
- **§5's blocks were applied verbatim with zero deltas because §5 was written
  against a tree that had stopped moving.** This one was not: `f555f3b5` landed
  while the gate record's §7 and §8.3 were being written. **The figures in §7.1
  and §7.2 were re-derived at `f555f3b5` and hold there** — the count is still
  31 and the two counted sample files are byte-identical across it — but whoever
  applies this should check that `git log --oneline -1` is still what they
  expect before assuming the same.

---

## 8. Applied — orchestrator, 2026-08-05, at `e2b9899b`

§§7.1–7.2 applied to PR #49 **verbatim, with zero content deltas.** The one byte
of difference is GitHub's: the API appends a trailing newline, and the body read
back differs from the file written by exactly that. Applied mechanically for the
reason §6 gives — the row is 4,014 characters on one line — by the same method:
the two fenced blocks extracted from this file by script, the status sentence
matched on its opening clause through `real measurements.`, the table row matched
on its leading column only, exactly one match asserted for each before writing.
`gh pr edit --body-file` was not attempted; it still fails on this repository with
the Projects (classic) GraphQL error, and `gh api -X PATCH repos/…/pulls/49`
applied it as in §4 and §6.

**§7.3's instruction was to check `git log --oneline -1` rather than assume, and
it was the right instruction: the tree had moved twice more.** *(Note added at
revision 5: it was the right instruction again — §9 is written at `e751f6de`,
four landings and a technical review past the tree §7 was applied at.)* §7.1 and §7.2 were
written at `73fd1e34` and re-derived by PM-1 at `f555f3b5`; they were applied at
`e2b9899b`, two commits further on. Both are PM-1's own — the gate record's
revision 4 and this file — and neither touches a counted file. **Re-counted at the
applied tree rather than carried forward: `docs/guide/_samples/quickstart/main.go`
20, `view.templ` 11, total 31.** The figures in the applied blocks hold at
`e2b9899b`.

**What the body says after this, checked against the body and not against the
intent:** `eleven of its thirteen` is gone (zero occurrences), `twelve of its
thirteen` appears once, and no live sentence in the body still reads
`still missed, at 39` or `≤30-line`.

Gates green at the pushed tree: `ci.sh` **`CI-EXIT=0`** — *"every gate this
invocation could run is green"* — run as `dis run bash ci.sh` in
`dis-gotth-live:latest`, with its five documented out-of-context skips (codegen
reproducibility, the client runtime suite, the bench harness suite, the browser
conformance specs, and G11). **G11 was run from the host, where it needs the
runner's own docker socket, and passed at this exact tree** — `tree:
e2b9899b0338…`, counter, chat and dashboard all PASS from a real clone in stock
`golang:1.25-bookworm` with node, npm, protoc and refinec proven absent.

**One correction to this record's own method, made because the alternative is a
green claim resting on a run that proved nothing.** The orchestrator's first
attempt ran `bash ci.sh` **bare on the host**, which has no Go toolchain, so every
Go-dependent step reported FAILED for want of a compiler and the verdict block was
meaningless. It was re-run inside the container, which is the invocation `ci.sh:50`
documents. The G11 result above is the one part of that first run that was real —
it shells out to docker rather than to `go` — and it is cited here from that run
rather than re-run, with the reason it survives stated instead of assumed.

---

## 9. Revision 5 replacements — PM-1, 2026-08-05, at `e751f6de`

**Two rows are stale and both are stale in the direction that under-reports —
which is the direction §5.3 named, §7.3 warned about, and this file has now
caught three times running.**

- **The Phase 3 row** ends *"PM-1 has not re-convened to tick the box; what is
  claimed here is that its three stated conditions are met, not that the gate
  has been re-held."* **The gate has been re-held.** It was held on 2026-08-05
  at `713a3192`, recorded in [`docs/gates/checkpoint-3.md`](../gates/checkpoint-3.md)
  §12, applied to the PRD at **v1.4** (`f0690a2c`). **Phase 3 exits at seventeen
  of seventeen.**
- **The Phase 4 row** says three things that are now all false: that failure 2 is
  a derivation-then-driven defect **still open**; that failure 3's *affordance is
  still absent*; and that **the browser evidence comes from harnesses that were
  thrown away**. Failure 2 is fixed, failure 3 is fixed, and the harness is in
  CI.

**And a third string moves, which the brief for this revision asked me to
determine rather than assume: the status blurb near the top is NOT still true.**
Two of its clauses are stale. *"The resilience checkpoint has since closed"* is
true and now under-reports — Phase 3 **exits**. And *"The one box left is FR-54's
templ helper set, which fails a completeness definition on three named gaps; one
of the three was a derivation and is now measured in a real browser"* describes
the tree at revision 4: **two of the three gaps are fixed and the third is
decided.** §9.1 replaces both clauses in one contiguous string.

**What does NOT move, having read the whole body**, so the orchestrator does not
have to re-derive it: the Phase 0, 1 and 2 rows; the Phase 5 row (nothing this
round produced a comparison timing, and `d12870a0`'s bench-dashboard fix is a
consumer repair rather than a spec or topology change); the memory paragraph and
its 45,768.7 B; the two-corrected-numbers paragraph; the merge-block paragraph;
and the three bullets under *"What a reviewer should know"*. **Two things
elsewhere in the body are stale in ways I am flagging rather than silently
folding into a row** — §9.4 for the CI paragraph, and §9.5's notes for the client
size figures, which are five other people's files and are already routed as
conditions in the gate record.

**Every figure below is traceable and two of them are mine.** The FR-54 review is
[`docs/reviews/fr-54.md`](../reviews/fr-54.md) at `e751f6de`; the Phase 3 act is
`checkpoint-3.md` §12 at `713a3192`; the browser-loop landing is `13a1ca1e`; and
**`live 56/56, 51/51` and `10306 / 4421` were re-run by PM-1 in
`dis-gotth-live:latest` at `e751f6de`** rather than quoted from a commit body —
[`docs/gates/phase-4.md`](../gates/phase-4.md) §2.8.

### 9.1 The status blurb — replacement sentences

**Find** the contiguous string beginning *"The resilience checkpoint has since
closed."* and ending *"lands with real measurements."* — **1,089 characters, one
occurrence in the body.** It is the sentence applied at `857d0bd9`/`e2b9899b`
plus the resilience sentence immediately before it.

**Replace with** (one line, no raw newlines, no `|` characters at all — it sits
inside a `>` quote block, not in a table):

```
**Phase 3 EXITS — seventeen of seventeen.** Its last open box was ticked by re-running the measurement six times rather than by reading the commit that claims it: every published byte figure identical **101 commits** after it was taken, three of the six runs on a pristine export, the method paragraph checked against the code at HEAD rather than against the commit body — and **the one published latency figure that did *not* reproduce given its own section rather than a footnote.** The docs gate has since been held and **passed** — QA-1 built a working app from the documentation alone, in 2m12s, with zero source-diving breaches — and Phase 4 still does not exit: **twelve of its thirteen boxes are ticked and one is not**, which is the same count as last round and is the point. FR-53's ≤31-line quickstart budget is **met, at exactly 31 with zero margin**, after a library-owned page shell absorbed the hand-written HTML document — gated by the principal engineer against nine constraints written before the component existed, and graded **PASS with four conditions** by QA-1, none of which PM-1 has discharged for them. **The one box left is FR-54's templ helper set, and two of its three named gaps are now fixed**: an option a binding declares now scopes to that binding, and the artifact got **smaller** doing it; and the shipped chat example implements the interaction its own source used to tell readers was impossible. **The third is decided rather than built.** The principal engineer ruled on it, found that **both refusal arguments standing on the record were aimed at the wrong target**, accepted a two-field surface at **+0 exported identifiers and +34 gzipped bytes**, refused the full modifier set with a checkable re-open trigger, and found a defect in the accepted design by prototyping it rather than by accepting it. **Nobody has built it**, and the box closes on the reviewer's own three blocking conditions plus a QA-1 grade nobody has asked for. This PR goes ready-for-review after that box closes and the headline benchmark report lands with real measurements.
```

### 9.2 The Phase-3 table row — replacement row

**Find** the row beginning `| 3 — Resilience (reconnect/resync, backpressure tuning, chaos suite, dashboard example, memory baseline) |`. **Match on the leading column only**, as §5.2 and §7.2 instruct; one occurrence.

**Replace with** (one line, no raw newlines, markdown-table-safe — the only `|` characters are the row's own four column delimiters):

```
| 3 — Resilience (reconnect/resync, backpressure tuning, chaos suite, dashboard example, memory baseline) | QA-2 chaos suite + L9-1 batch review + PM-1 gate report | ✅ **closed — PHASE 3 EXITS, seventeen of seventeen.** All three signatures were in and each was earned at this tree. QA-2 re-verified **at HEAD** — [PASS](gotth-live/docs/qa/checkpoint-3-chaos.md), 42/42 chaos specs under `-race` with the soak and the Appendix-B measurements on, on a host labelled contended per the benchmark contract; one exit-criterion row moved and was attributed to a specific fix **by mutation rather than by argument**; and they filed a defect against *their own* suite (a spec that stayed green against both the fixed library and the un-fixed one). L9-1 [**APPROVED**](gotth-live/docs/reviews/checkpoint-3.md) with **no conditions on this gate**, after auditing QA-2's falsifiers and finding one that does not isolate what it claims. **The seventeenth box did not tick for a long time, and it was not narrowed to make it tick**: the dashboard's published resync-cost figure was byte-identical across the whole change set while the program that produces it was rewritten underneath, so the method paragraph described a request shape the fixed program deliberately no longer sends. The remedy landed at `1b16f4a9` and **published a figure one to two bytes *worse*** (frame p50 2377 → **2378 B**, protocol overhead 146 → **147 B**), which is what `resync.go` predicted a two-varint change would cost. **The box has now been re-held, and how it was held is the whole of why it counts** ([gate act](gotth-live/docs/gates/checkpoint-3.md) §12, `713a3192`, applied at PRD v1.4): PM-1 **re-ran the measurement six times** rather than reading the commit that claims it, **three of the six on a pristine `git archive` export** because other agents' uncommitted files appeared in this shared worktree mid-act, and compared against the published fence **with `diff -u` against the program's own stdout rather than by eye**. **Every byte figure the README publishes is identical on all six runs, 101 commits after they were taken** — frame, markup, protocol overhead, all three per-region figures, and the library's own `gotthlive_resync_bytes` mean and max. **A sixth run nobody planned is the most informative**: `2ab18690` landed during the act and re-encodes rendered markup **inside live regions**, which is the one class of change that could have moved these bytes; re-measured against an export of it, identical again, with `internal/session`, `internal/protocol`, the chaos suite and the dashboard's own `-race` run green there too. The act is held at `713a3192` and **re-confirmed** at `2ab18690`, and the two are not collapsed into one claim. The method paragraph was checked **against `examples/dashboard/resync.go` at HEAD**, clause by clause, rather than against the commit body — including that the server-side clamp it rests on is in force at `internal/session/resync.go`. **And one published number did not reproduce, which is recorded at the same prominence as the ones that did**: the README's `max 579µs` is the **low outlier of eight runs this host has produced** (PM-1's six maxima were 1.399 ms, 1.79 ms, 1.511 ms, 2.623 ms, 1.15 ms and 568 µs; DEV-3's own second run reported 1.771 ms), it has **its own section** in the gate act rather than a footnote, and the act states plainly what a reader quoting 579 µs is quoting. **That does not fail the box and the reason was written before anybody re-ran it**: the criterion asks for bytes *and* latency, the README publishes the latency as a distribution with its host, its load average and its container count stated, and tells the reader to quote the bytes. A document that predicts its own irreproducibility and is then found irreproducible in exactly the manner it predicted is behaving correctly. Everything carried forward is enumerated with an owner in the report's §7, and marked explicitly as *not* a condition, so it stops being re-litigated |
```

### 9.3 The Phase-4 table row — replacement row

**Find** the row beginning `| 4 — DX & docs (quickstart, templ helpers, provenance inspector, dev reload, godoc gate, error audit, exceptions register, docs-alone gate) |`. **Match on the leading column only**; one occurrence, and it is the row applied at `e2b9899b`.

**Replace with** (one line, no raw newlines, markdown-table-safe — the only `|` characters are the row's own four column delimiters):

```
| 4 — DX & docs (quickstart, templ helpers, provenance inspector, dev reload, godoc gate, error audit, exceptions register, docs-alone gate) | QA-1 builds an app from the docs alone | 🔄 in progress — **the gate PASSED, twelve of thirteen boxes are ticked, Phase 4 still does not exit, and the count is the same as last round.** That last clause is the one to read: four landings and a technical ruling arrived this week and **not one of them ticks a box**. **FR-53 stays met at exactly 31 against a budget of 31, margin zero**, after a library-owned page shell absorbed the hand-written HTML document — gated by the principal engineer against nine constraints written before the component existed, and graded **PASS with four conditions** by QA-1, **none of which PM-1 has discharged**, because they are QA-1's and a remediation landing is not a grade. **FR-54's failure 2 is FIXED, and the artifact got smaller doing it**: every `Bind` option moved out of its own element attribute into the binding that declared it, so `data-gotth-fields`, `-debounce` and `-throttle` no longer exist — **+0 exported identifiers and −85 B minified / −8 B gzipped**, 10,391 → **10,306** and 4,429 → **4,421**, re-measured by the reviewer and **again by PM-1 in the container** rather than taken from a commit body. Driven in Chromium against the very reproduction that found it: where QA-1 had measured the composed clear **destroyed**, it now arrives at ~1.7 ms beside the debounced draft at ~157 ms, and the symmetric case delivers both events with server and browser agreeing. **FR-54's failure 3 is FIXED, in the shipped example rather than in a fixture**: `examples/chat` implements Escape-to-clear as a real reducer case bound with `live.OnAll`, driven 6/6 in Chromium; the source comment that told a reader the library could not do it now says it can; and the friction note takes its *"Closed"* heading **with the argument that refused the heading kept above it**. **The browser loop is in CI**, which was the oldest unaddressed condition in the gate record and had been named as one for four consecutive revisions: two new conformance files, six standing specs, **three mutation controls and all three killed the owning spec**, and a negative control — a rebuild that changes no byte restarts the process and reloads **nothing** — guarded against two ways of passing by not looking. **The step's spec counts were re-derived at the tree rather than carried, and that immediately caught that the published counts were stale**: it is **43 specs without `GOTTHLIVE_E2E` and 50 with it**, where the page had said **22/25**, and 19/22 before that. **A mutation control also disproved the commit message it came from** and that is published rather than quietly dropped: the empty inspector panel of `0c711b70` was real, but its stated cause — an *"Illegal invocation"* thrown by `render()`'s `globalThis.rAF`-or-`setTimeout` fallback, said to invoke the scheduler with no receiver — **does not reproduce in Chromium 151**, because `requestAnimationFrame` is on the `[Global]` `Window` interface and Web IDL defaults an undefined receiver there to the global object. The panel was real; the diagnosis was not; three documents including the gate record carried it for four revisions. **FR-54's failure 1 is RULED, and decided is not fixed.** The principal engineer reviewed the landing (**ACCEPT WITH CONDITIONS**, six of them) and then ruled on the gap: **both refusal arguments standing on the record were aimed at the wrong target.** *"A chord belongs to the browser"* is true of Ctrl, Meta and Alt and **false of Shift+Enter in a textarea**, which no browser and no operating system claims — and since `KeyboardEvent.key` already folds Shift into every printable value, the gap was only ever *"distinguish Shift on a non-printable key"*. *"A library that preventDefaults on the application's behalf takes over Ctrl+F"* describes an **unconditional default and reaches no opt-in**; the runtime already calls `preventDefault` for a declared form submit and a declared anchor click. **ACCEPTED: two struct fields, at +0 exported identifiers, +2 fields and +62 B minified / +34 B gzipped**, priced on a prototype **before** the shape was accepted and verified at zero delta across 156 client and 7 browser specs. **REFUSED: the full modifier set**, at +57 gzipped for the modifier half alone, on a ground the reviewer had just ratified against themselves, with a **three-limbed pre-registered re-open trigger** — one of whose limbs fires if the reviewer's **own** evidence turns out to be insufficient, because *"a node harness builds the event object it then reads"*. **The box is still open, and the honesty register is the point of this row.** The accepted surface **does not exist**: it is a prototype in a container's `/tmp`, and building it is what found that the client calls `preventDefault` **before** its IME composition guard, so the accepted flag placed where the prototype places it **would break every CJK composer** — a defect in the accepted design, found by construction and published by the person who accepted it. **Two of the reviewer's twelve mutation controls survived everything**: one leaves **all 156 client specs and all 7 browser specs green** while reintroducing the exact defect this landing fixed, for two bindings that share an event name — the landing's own motivating case, claimed in three documents and pinned in none, and the reviewer wrote the missing spec — and one survived every client spec and was killed only by a browser package the author had not cited. **And a mixed-version window was driven, not derived**: the client runtime is served from an unfingerprinted path with a year of `immutable` caching, so an old cached runtime against new markup reports **`armed timers: 0`** and silently drops every declared field — no error, no console warning, no close code, because the version check is on the wire protocol and the attribute grammar is a second contract it does not cover. The ledger's *"there is no mixed-version window"* was false and is corrected **beneath itself**. **What closes the box is the reviewer's sentence and not this project's optimism**: three of the six conditions discharged — a grammar that refuses an unbindable key instead of silently turning a debounce into a throttle, the missing spec, and the accepted surface built against nine pre-registered constraints — **and QA-1 grading them, which nobody has asked for.** [PM-1's interim gate record](gotth-live/docs/gates/phase-4.md) is at **revision 5**, has every box, every owner, and **seven corrections to its own prior revisions — two of them added this round, both found by an engineer running a control that was meant to confirm them rather than by any reader** |
```

### 9.4 The CI paragraph — recommended, not required, and flagged rather than folded in

**The *"gate, quoted rather than asserted"* paragraph is now under-reporting in
one specific way and I am not rewriting it, because it is a dated narrative and
its two gate defects are still the useful part.** What is stale is that it quotes
the browser conformance specs as **`ok … 24.971s`** run *beside* the gate, and
lists them among *"four steps skipped in that image by construction"*. **Since
`13a1ca1e` the browser specs are inside a whole-gate run**: `bash ci.sh` in
`dis-gotth-live-bench:latest` with `GOTTHLIVE_E2E=1` and `CHROME_BIN` set, at
`d12870a0`, **exit 0**, *"every gate this invocation could run is green"*, browser
conformance **`ok 50.369s`**, and **exactly one step skipped — G11**, which by
design needs a host docker daemon and cannot run inside any image.

**If the orchestrator wants that recorded, this is the minimal surgical form** —
a prepend, so nothing dated is overwritten:

**Find** (one occurrence):

```
**The gate, quoted rather than asserted.** `bash ci.sh` exits **0** with the working tree clean, run in `dis-gotth-live:latest`
```

**Replace with** (one line, no raw newlines):

```
**The gate now runs the browser inside itself, which no previous version of this paragraph could say.** At `d12870a0`, `bash ci.sh` in `dis-gotth-live-bench:latest` with `GOTTHLIVE_E2E=1` and `CHROME_BIN` set exits **0** — *"every gate this invocation could run is green"* — with browser conformance **`ok 50.369s`** as a step of the gate rather than a run beside it, and **exactly one step skipped: G11**, which by design needs a host docker daemon and cannot run inside any image. `apisurface` (**live 56/56, 51/51**, *"the surface matches the ledger"*) and `doccheck` were re-run at `e751f6de` after the FR-54 review landed and both exit 0. **The paragraph below is kept as the dated record it is, because its two gate defects are still the useful part.** **The gate, quoted rather than asserted.** `bash ci.sh` exits **0** with the working tree clean, run in `dis-gotth-live:latest`
```

**If it is not applied, nothing in the body becomes false** — the old paragraph is
explicitly about the `134e69c5` and `93db6557`…`667d3db7` runs and says so. It
just stops being the best evidence available.

### 9.5 Notes for whoever applies this

- **This is the first revision of this row where the count does not move, and the
  row must not read like a victory lap.** §5.3 warned about the flattering
  direction; §7.3 said twelve of thirteen is *"true and misleading on its own"*.
  **Both are still in force and the risk is higher this time**, because four
  landings and a principal-engineer ruling arrived and a reader will read that as
  a phase exit. **§9.3 names, at the same prominence as the three fixes: the two
  mutants that survived, the mixed-version window, the defect found inside the
  accepted design, the accepted surface having no artifact, the four QA
  conditions nobody has discharged, and the seven corrections the gate record owes
  itself.** If the row is cut for length, cut a fix and not one of those.
- **"Decided" and "fixed" are different words and the row spends a clause on the
  difference deliberately.** Failure 1 has a ruling, a price, a refusal and a
  trigger, and **no code**. A body that says "failure 1 is resolved" would be the
  §7.2 defect one more time, in the direction that attracts no second reader.
- **The Phase 3 row is now a closed-phase row and should be read as one.** It ends
  with a number that did **not** reproduce, on purpose. **Do not cut the `579µs`
  sentence for length** — it is the sentence that makes the six identical byte
  figures worth quoting, and the gate act it summarises gives it a section of its
  own for the same reason.
- **Nothing else in the body was touched by this round**, and §9's preamble
  enumerates what was checked and left alone rather than leaving the orchestrator
  to re-derive it. The one deliberate exception is §9.4, which is offered and not
  required.
- **The five stale client-size figures are not this file's to fix and should not
  be folded into a row.** `README.md:113`, `docs/guide/deploying.md:24`,
  `docs/quickstart.md:161`, `docs/guide/inspector.md:198` and
  `docs/instrumentation.md:835` all still say **10,391 / 4,429** against a shipped
  **10,306 / 4,421**. They are DEV-3's and are routed as such in the gate record's
  §6; `client/SIZE.md:628` is DEV-2's and is a **blocking** condition on the
  landing. **The PR body's own client-size sentence should be re-checked against
  `tools/minify` before the next application** — PM-1 ran it at `e751f6de` and it
  reads 10,306 / 4,421, and a body that quotes 4,429 would be the sixth carrier.
- **All three required blocks were applied to a copy of the live body before this
  file was committed, and the result was checked rather than eyeballed.** The body
  was read from `/tmp/pr49-body.md` (42 lines, the tree the orchestrator saved);
  §9.1's Find matched **once** at 1,089 characters; §9.2 and §9.3 matched **once
  each** on their leading columns; §9.4's Find matched **once**. After
  substitution: **every row of the `| Phase | Gate | Status |` table has exactly
  four `|` characters**, no replacement contains a raw newline, `twelve of its
  thirteen` appears **once**, and **all five stale strings this revision exists to
  remove are gone** — `PM-1 has not re-convened`, `still comes from harnesses that
  were thrown away`, `affordance is still absent`, `The resilience checkpoint has
  since closed`, and the FR-54 clause about a derivation *"now measured in a real
  browser"*. **This is a dry run and not an application**; the orchestrator still
  applies against the live body, which may have moved.
- **Check `git log --oneline -1` before applying**, as §7.3 said and as §8 found
  was right twice over. §9 is written at `e751f6de`. **The figures most likely to
  move under it are the ones a landing against FR54-3, FR54-4 or FR54-6 would
  change** — the byte figures, the identifier and field counts, and the word
  "decided" in §9.1 and §9.3. If any of those three conditions has been
  discharged between this file and the application, **stop and route it back to
  PM-1 rather than editing the row by hand**: the row is over 5,000 characters on
  one line and a hand-edit of that is the silent-corruption risk §6 named.

---

## 10. Applied — orchestrator, 2026-08-05, at `b777f12e`

**All four of §9's blocks are applied, plus one delta §9.5 asked for and did not
draft.** The body is now **31,962 characters**, up from 26,075.

**Method, which is §6's and §8's and is the reason these applications have
stopped producing drift.** A script extracts the fenced blocks from this file by
walking §9's lines rather than by copying them into a shell, so what ships is
what PM-1 wrote, byte for byte. It then asserts before writing anything: §9.1's
Find, reconstructed from the two endpoint sentences §9.1 names, is **1,089
characters — exactly the length PM-1 stated**, and both endpoints occur once;
§9.2's and §9.3's leading columns match **once each**; §9.4's Find matches once.
After substitution it asserts that **every row of the phase table has exactly
four `|` characters**, that no replacement carries a raw newline, and that five
named stale strings are gone. Then `gh api -X PATCH … --input` with a JSON body,
because `gh pr edit --body-file` still fails on this repository with the
Projects-classic GraphQL error §4 recorded.

**Verified after the fact rather than assumed**: the body was read back from the
API and compared to what was sent. **It is byte-identical apart from a single
trailing newline GitHub appends**, which is the server's normalisation and not a
content delta — `a.rstrip("\n") == b.rstrip("\n")` is true, and that is the check
that was run.

**§9.4 was applied, though PM-1 offered it as not required.** It is this turn's
own evidence and it is the strongest form the CI paragraph has ever been able to
take: the browser is now a **step of the gate** rather than a run beside it. The
prepend leaves the dated paragraph beneath it untouched, exactly as drafted.

### 10.1 The one delta from what PM-1 drafted, recorded rather than left as drift

**§9.5 named a figure and did not draft its replacement**: *"The PR body's own
client-size sentence should be re-checked against `tools/minify` before the next
application — PM-1 ran it at `e751f6de` and it reads 10,306 / 4,421, and a body
that quotes 4,429 would be the sixth carrier."*

The body did quote **4,429 B gzipped** for the client runtime. It is now
**4,421**, which is what `tools/minify -check` prints at `e751f6de` — a figure
this turn saw **three** times independently: in L9-1's review, in PM-1's own
container run, and in the whole-gate `minify -check` step at `d12870a0`. The
substitution was asserted unique before it was made, and the surviving `4,429`
occurrences were each checked to sit inside the new Phase-4 row's own
before-and-after narrative (`4,429 → 4,421`), which is a historical statement and
correct.

**The five other carriers of the stale figure are untouched and are not this
file's**: `README.md:113`, `docs/guide/deploying.md:24`, `docs/quickstart.md:161`,
`docs/guide/inspector.md:198` and `docs/instrumentation.md:835` (DEV-3's), and
`client/SIZE.md:628` (DEV-2's, and a **blocking** condition on the FR-54 landing
as FR54-2). They are routed in [`docs/gates/phase-4.md`](../gates/phase-4.md) §6.
**Fixing the body's copy and leaving those six is deliberate**: the body is the
artifact a reviewer reads today, and the other six close as one condition rather
than as six drive-by edits — which is the rule this project adopted after the
six-way `allowedOrigins` divergence, where fixing three of six recreated the
divergence it was meant to end.

### 10.2 What this application does NOT claim

**The count did not move and the body says so twice.** Twelve of thirteen, same
as revision 4. Phase 3 exits; Phase 4 does not. **FR-54's failure 1 is decided
and not built**, and both the blurb and the row spend a clause on that
distinction, because a reader who takes "ruled" for "resolved" has been given the
§7.2 defect in the direction that attracts no second reader.

---

## 11. Revision 6 replacements — PM-1, 2026-08-06, at `9efb7e5b`

**Phase 4 EXITS, thirteen of thirteen, and every sentence §9 wrote about Phase 4
is now false.** The row applied at `b777f12e` says the gate passed, **twelve of
thirteen** are ticked, the phase **does not exit**, and *"the count is the same as
last round."* All four clauses are stale. **This project's standing rule is that a
stale row in the PR body is a defect fixed as part of the landing that changes
it**, and this is that landing.

**What this revision replaces:** the Phase-4 half of the status blurb (§11.1), the
Phase-4 table row (§11.2), and **one live figure the body carries as
current-state** (§11.3). **The Phase-3 row applied at `b777f12e` is untouched and
correct** — Phase 3 closed, and its row already ends on the number that did not
reproduce, which §9.5 asked not be cut and which should still not be cut.

**Checked and deliberately not changed:** the Phase 3 row; the Phase 1–2 rows; §9.4's
CI paragraph, which is still accurate as written (`ci.sh` exit 0 at `eb4971c6` in
`dis-gotth-live-bench:latest` with `GOTTHLIVE_E2E=1`, **one step skipped, G11**);
and the *"gate, quoted rather than asserted"* framing. **The tree moved under §9
and it has moved under this one too** — §11.4 says what to re-check before
applying.

### 11.1 The status blurb — replacement for its Phase-4 half only

**Find** the contiguous string beginning *"The docs gate has since been held"* and
ending *"lands with real measurements."* — **1,604 characters, one occurrence in
the body.** It is the Phase-4 half of the blurb applied at `b777f12e`. **The
Phase-3 sentence before it (485 characters, beginning *"Phase 3 EXITS — seventeen
of seventeen."*) is correct and must NOT be replaced.**

**Replace with** (one line, no raw newlines, no `|` characters at all — it sits
inside a `>` quote block, not in a table):

```
The docs gate has since been held and **passed** — QA-1 built a working app from the documentation alone, in 2m12s, with zero source-diving breaches — and **PHASE 4 NOW EXITS: thirteen of its thirteen boxes are ticked**, on a grade the project asked for rather than on this report's reading. The last box was **FR-54's templ helper set**, and all three of its named gaps are closed: an option a binding declares now scopes to that binding, the shipped chat example implements the interaction its own source used to call impossible, and the keyboard chord that was inexpressible is **half built and half refused** — `Bind.NoModifiers` and `Bind.PreventDefault` shipped at **+0 exported identifiers, +2 struct fields and +81 bytes minified / +38 gzipped**, with "Enter sends, Shift+Enter inserts a newline" **driven end to end in real Chromium**, while the full modifier set is **REFUSED** with a three-limbed re-open trigger that a grader then fired every limb of. **Four QA conditions travel with that tick and are not discharged by it**, one of them a finding that the refusal's own leading price has been wrong since the landing it sits beside. **The honesty register is the most valuable thing in this round**: the principal engineer pre-registered nine constraints before the artifact existed and **three of the nine turned out to be defective** — one count invented, one byte budget that no correct artifact could satisfy because it priced a prototype that would break every CJK composer, and one that would have certified a runtime with a dropped modifier read. **All three were caught by the people building against them, not by their author, and their author published the finding against themselves.** **Phase 4 exiting is not this project finishing.** Phase 5 is what remains — the benchmark measurement, the headline report and the feature-parity table — and **no benchmark timing has been collected.** This PR goes ready-for-review when that report lands with real measurements.
```

### 11.2 The Phase-4 table row — replacement row

**Find** the row beginning `| 4 — DX & docs (quickstart, templ helpers, provenance inspector, dev reload, godoc gate, error audit, exceptions register, docs-alone gate) |`. **Match on the leading column only**; one occurrence, and it is the row applied at `b777f12e`.

**Replace with** (one line, no raw newlines, markdown-table-safe — the only `|` characters are the row's own four column delimiters):

```
| 4 — DX & docs (quickstart, templ helpers, provenance inspector, dev reload, godoc gate, error audit, exceptions register, docs-alone gate) | QA-1 builds an app from the docs alone | ✅ **closed — PHASE 4 EXITS, thirteen of thirteen, with four conditions that travel with the tick.** The last box was **FR-54's templ helper set**, graded **PASS WITH CONDITIONS** by QA-1 at `eb4971c6` ([grading pass](gotth-live/docs/qa/phase-4-grading.md) §11) — **not on the project's own reading**, which is the distinction this row has spent five revisions on. **All three of FR-54's failures are closed, and how differs by failure.** **Failure 1 — by a RULING and then by an ARTIFACT.** The principal engineer found that **both refusal arguments standing on the record were aimed at the wrong target**: *"a chord belongs to the browser"* is true of Ctrl, Meta and Alt and **false of Shift+Enter in a textarea**, and *"a library that preventDefaults on the application's behalf takes over Ctrl+F"* describes an unconditional default that reaches no opt-in. **ACCEPTED: two struct fields**, `Bind.NoModifiers` and `Bind.PreventDefault`, now shipped as grammar components 7 and 8 at **+0 exported identifiers, +2 fields and +81 B minified / +38 B gzipped** with **zero output delta** — every binding in the tree renders byte-identically — and *"Enter sends, Shift+Enter inserts a newline"* is **driven end to end in real Chromium 151** rather than in a harness that builds the event object it then reads. **REFUSED: the full modifier set**, with a **pre-registered three-limbed re-open trigger** naming the price at which the answer becomes yes rather than reserving the right to move it; the grader **fired all three limbs themselves** and none fired. **Failures 2 and 3 — by engineering**: an option a binding declares now scopes to that binding, and the shipped chat example implements the Escape-to-clear its own source used to tell readers was impossible. **The honesty register is the most valuable thing in this round and it is not being smoothed away.** The reviewer pre-registered **nine constraints before the artifact existed** — and **three of the nine were defective**. One invented a count. One set a byte budget **no correct artifact could satisfy**, because it was priced against a prototype that calls `preventDefault` above the browser's IME composition guard and **would break every CJK composer**; a landing that complied with it as written would have had to be a landing that breaks Chinese, Japanese and Korean input. One **would have certified a runtime with a dropped modifier read** — deleting the Alt-key check leaves its designated spec green in both harnesses, and the landing is safe only because an engineer wrote a spec the constraint did not ask for. **All three were caught by the people building against them rather than by their author, and their author published the finding against themselves, with the defective constraints left unedited** on the ground that *"a pre-registered constraint that gets quietly edited after the artifact exists is worth nothing."* **QA-1 then re-drove the third independently, in two harnesses, and confirmed it.** The narrower claim this supports, and the one worth taking: **pre-registration did not stop three defective constraints from being written; it made them findable, attributable and correctable before they graded anything.** **What is still open, at the same prominence.** **Four QA conditions travel with this tick and only one has been discharged** — the one that belonged to the person writing this row. Of the other three: the refusal's **leading price is wrong**, and the grader found it by setting out to prove the re-open trigger was dead and **measuring it alive instead** — the full modifier set costs **+10 gzipped bytes** at today's artifact, not the *"fourteen times"* the refusal argues from; a spec introduced as *"the one that would go red if the runtime stopped reading one of the four"* **stays green** when two of the four are dropped, which is a false sentence sitting inside the evidence file for the constraint it belongs to; and **a ruling and the shipped code disagree** about a fifth refusal, with the code self-consistent and the ruling the outlier — that one is a fifth condition, tracked separately, and it is open. **And what this pass does NOT prove, in the grader's own words rather than summarised:** the clean-clone check **did not run** in the gate this exit quotes (it needs a host docker daemon and is graded separately); it is **one browser at one version**, so the chord, the mouse-event clause and the modifier reads are unproven on Firefox, Safari and WebKit; the *"a PreventDefault binding outside every region suppresses the default and sends nothing"* behaviour is **true and asserted nowhere**; and the sweep that found the requirement's third clause empty is **bounded at fifteen phrasings**, in a project that has now found four such sentences after four declarations that the sweep was complete. **Phase 4 exiting is not this project finishing, and this row must not be read as one.** **Phase 5 is what remains — the benchmark measurement, the headline report and the feature-parity table — and no benchmark timing has been collected.** There are apps, a harness and a frozen equivalence spec; there are no numbers. [PM-1's gate record](gotth-live/docs/gates/phase-4.md) is at **revision 6**, the Phase-4 exit act: every box with the name of whoever signed it (**seven on QA-1's grades, five on PM-1's reading, one on the principal engineer's signature**), every carried condition with an owner, and **eleven corrections to its own prior revisions — four added this round, and three of those four were found by executing its own reproduce block rather than reading it**, including one figure falsified by the exact landing this row reports |
```

### 11.3 The client-size figure — the body's own current-state number, which has moved again

**This is the same item §10.1 handled last round, and it needs handling again for
the same reason.** The body carries the shipped client runtime's gzipped size as a
**current-state claim**. `§10.1` moved it from `4,429` to **`4,421`**. **The Part B
landing has moved it again**, and a body that ships `4,421` on this application
would be carrying a figure that the landing this very row reports is what
invalidated.

**The authoritative figure is the tool at HEAD, not this file and not any review.**
PM-1 ran it at `9efb7e5b`:

```
cd gotth-live
~/bin/dis run bash -c 'cd tools && go run ./minify -check'
  -> Shipped gotth-live.min.js 10387 / 4459, ceiling 12288, headroom 7829 (63.7%)
```

**Find** the string `4,421` where it is the client runtime's **current** size.
**Replace with** `4,459`. If the minified figure `10,306` appears as a current
size, **replace with** `10,387`.

**Do NOT replace either figure where it sits inside a before-and-after
narrative.** The revision-5 row contains `10,391 → 10,306` and `4,429 → 4,421` as
a **historical** statement about what the failure-2 landing did, and that
statement is **true and must survive** — it is the sentence that makes *"the
artifact got smaller doing it"* checkable. §11.2's replacement row removes the
whole of the revision-5 Phase-4 row anyway, so **apply §11.2 first and then check
what `4,421` occurrences remain**; each survivor should be inside a
before-and-after clause or should not exist.

**Assert before writing**, as §6's and §10's method does: after §11.2 is applied,
`4,429` and `4,421` should appear **only** inside historical before-and-after
clauses, and `4,459` should appear at least once as the current figure.

### 11.4 Notes for whoever applies this

- **This is the first revision of this row where the count moves upward to a
  phase exit, and every previous note in this file warned about the flattering
  direction.** §5.3 named it, §7.3 said twelve of thirteen was *"true and
  misleading on its own"*, and §9.5 said the risk was highest when a reader would
  read landings as an exit. **The risk is highest now.** §11.2 puts the four open
  conditions, the fifth open condition, the unrun clean-clone check, the single
  browser, the unasserted behaviour, the bounded sweep, and **"no benchmark timing
  has been collected"** at the same prominence as the exit. **If the row is cut
  for length, cut a closure and not one of those.**
- **"Exits" and "finished" are different words and the row spends its last clause
  on the difference deliberately.** Phase 5 has apps and no numbers. A body that
  lets a green Phase-4 row read as project completion is the §7.2 defect in the
  direction that attracts no second reader — which is the sentence this file has
  now carried for four revisions, and the first one where the pressure runs the
  other way.
- **The `+62 B minified / +34 B gzipped` figure is retired and must not survive
  anywhere as a current price.** It was the price of a prototype in a container's
  `/tmp`; **the landed price is +81 / +38.** This file carries the stale pair at
  `:375` and `:395` — both inside revision 5's dated replacement blocks, which are
  **records of what was applied** and are correctly left alone. It is the **live
  body** that must not carry it, and §11.2's replacement row is what removes it.
  This is the PM half of the reviewer's routed condition FR54-9; the gate record's
  six carriers are handled in `docs/gates/phase-4.md` revision 6 §7.11.
- **Re-check the two figures against the tools before applying, not against this
  file.** `+81 / +38`, `10,387 / 4,459` and `+2 fields (51 → 53)` all come from
  `tools/minify -check` and `tools/apisurface` at `9efb7e5b`. **Two agents in this
  batch were handed a stale pair by a document and one of them nearly published
  it.** Run:
  ```
  cd gotth-live
  ~/bin/dis run bash -c 'cd tools && go run ./minify -check'
  ~/bin/dis run bash -c 'cd tools && go run ./apisurface'
  ```
  **`bash -c`, never `bash -lc`** — a login shell strips the Go toolchain from
  `PATH` in these images. **And do not substitute the shell's `gzip -9` for the
  tool**: it reads **4,415** where the tool reads **4,459**, because GNU gzip and
  Go's `compress/gzip` are different implementations of the same level. PM-1's
  first measurement of the delta used the wrong compressor and was discarded;
  publishing from it would have introduced a fresh stale figure into the very
  round that exists to remove seven.
- **Check `git log --oneline -1` before applying.** §11 is written at `9efb7e5b`
  plus PM-1's own two documentation commits. **The figures most likely to move
  under it are the ones a landing against Q-6, Q-8 or FR54-7 would change** — the
  refusal count in `refuseUnbindable` (four today, five if FR54-7 lands), the
  binding-spec comment Q-6 names, and the byte figures. If any of those has been
  discharged between this file and the application, **stop and route it back to
  PM-1 rather than editing the row by hand**: the row is over 5,000 characters on
  one line and a hand-edit of that is the silent-corruption risk §6 named.
- **Verify after writing rather than assuming**, which is the method §6, §8 and
  §10 established and which has produced zero content drift for three
  applications: read the body back from the API, compare byte-for-byte against
  what was sent modulo GitHub's single appended trailing newline, assert that
  **every row of the `| Phase | Gate | Status |` table has exactly four `|`
  characters**, that no replacement contains a raw newline, and that these strings
  are **gone** from the live body: `twelve of thirteen`, `Phase 4 still does not
  exit`, `the count is the same as last round`, `The accepted surface`, `Nobody
  has built it`, `+62 B minified`, and `three blocking conditions plus a QA-1
  grade nobody has asked for`.

---

## 12. Applied — orchestrator, 2026-08-06, at `66da6d13`

**All three of §11's blocks are applied, and this one has no delta at all.** The
body is now **31,428 characters**, down from 31,962 — the first application in
this file's history that makes the body *shorter*, because §11.2's replacement
row retires a revision-5 row that had to argue at length that a landing was not
an exit. It is an exit now, and the row says so in fewer words than the row that
said it was not.

**Method, which is §6's, §8's and §10's, unchanged because it has stopped
producing drift.** A script walks §11's headings and extracts the fenced blocks
from this file, so what ships is what PM-1 wrote byte for byte and no replacement
text was ever retyped into a shell. It then asserts **before** writing anything:

- §11.1's Find, reconstructed from the two endpoint sentences PM-1 names, is
  **1,604 characters — exactly the length PM-1 stated** — and both endpoints
  occur once. **This is the check that matters most**, because §11.1 replaces
  *half* of a blurb whose other half is Phase 3's and is correct; a Find that
  came back the wrong length would have meant the boundary had moved.
- §11.2's leading column matches **exactly once**, and the replacement row
  carries **four `|` characters and no raw newline**, so the phase table cannot
  be broken by it.
- §11.3 is applied **after** §11.2 and only then, per PM-1's own sequencing
  instruction, and the survivors are inspected rather than assumed: after
  §11.2, exactly **one** `4,421` remained and it is a current-state claim
  (*"client runtime 4,421 B gzipped of the 12,288 B budget"*). `10,306`,
  `10,391` and `4,429` did not survive §11.2 at all, because the only rows that
  carried them were the ones it replaced.
- Post-substitution, five named stale strings are asserted **absent**: `4,429`,
  `4,421`, `10,306`, `10,391`, and the retired prototype price `+62 B` /
  `+34 B`. This is the PM half of L9-1's routed condition **FR54-9**, and it is
  now discharged on both halves — the gate record's six carriers in
  `docs/gates/phase-4.md` revision 6 §7.11, and the body's two here.

**PM-1 asked that the two figures be re-checked against the tools rather than
against this file, and they were, before the PATCH rather than after it:**

```
cd gotth-live
~/bin/dis run bash -c 'cd tools && go run ./minify -check'
  -> Shipped gotth-live.min.js 10387 / 4459, ceiling 12288, headroom 7829 (63.7%)
     inspector 14905 / 6211 of 40960 ; dev-reload 2452 / 1260 of 8192
~/bin/dis run bash -c 'cd tools && go run ./apisurface'
  -> live 56/56  53/53  109/109 ; "the surface matches the ledger"
```

Both reproduce PM-1's figures exactly. **The inspector's 6,211 and the dev-reload
client's 1,260, which sit in the same body sentence as the figure that moved, were
checked too and had not moved** — §11.3 named one figure and the sentence carries
three, and replacing the named one while leaving a stale neighbour is the shape of
this round's own FR54-2.

**Verified after the fact rather than assumed.** The body was read back from the
API and compared with what was sent: **byte-identical apart from a single trailing
newline GitHub appends** — `a.rstrip("\n") == b.rstrip("\n")` is true, and that is
the check that was run, not an eye. **Zero content deltas**, where §10 had one and
§8 had two.

**What this application does NOT claim.** It does not claim the PR is ready — `gh
pr view 49 --json isDraft` reads **`true`** and must, because Phase 5 has not run
and the body's own last clause says *"no benchmark timing has been collected."*
It does not claim `ci.sh` is green at `66da6d13`: the whole-gate run this turn
quotes is **exit 0 at `eb4971c6`** in `dis-gotth-live-bench:latest` with
`GOTTHLIVE_E2E=1` and `CHROME_BIN=/usr/bin/chromium`, *"every gate this invocation
could run is green"*, one step skipped (**G11**, which needs a host docker daemon);
a second whole-gate run over the five documentation commits on top of it was
started before this application and its result belongs to whoever records it. And
it does not discharge **any** of QA-1's four conditions or the fifth tracked
separately — those travel with the tick, they are named in the row at the same
prominence as the exit, and none of them is the orchestrator's to close.
