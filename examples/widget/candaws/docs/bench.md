# `candaws-roundabout-v1` — the BEN probe design

The second task in the agentic A/B benchmark, and the first one that measures
the **language** rather than the skill. `csp-fanin-v1` asked whether a style
skill changes how an agent writes Go. This one asks the question the whole
Widget Foundry programme rests on:

> Does writing a widget as a `.widget` document and regenerating it cost less
> than writing the same card by hand against the SDK?

It was a design. The task directory now exists, because the blocking obligation
in § *Satisfiability* has been met: both reference implementations were run, the
fixtures were mutation-checked, and the two assertions this document got wrong
were repaired against what generated markup actually is. § 10 is the run
protocol the operator follows verbatim.

**This document is grader-side.** `csp-fanin-v1`'s rule — the implementer must
not see the README, because "an implementer who knows the grep is coming is no
longer a sample of ordinary behavior" — applies to this file for the same
reason and with the same force.

---

## 1. The task: Roundabout

The reserved service of the [CandaWS fleet](fleet.md): a managed load balancer,
five nodes, a request/response round trip, health checking with ejection and a
retry budget. [`fleet.md`](fleet.md) § *Reserved* states why it and not another:
it is mid-width, its topology is Coldstart's — which **is** built, so both
conditions inherit exactly one worked precedent for a round trip and neither
inherits two — and its engine is fresh, so the work is real.

It is not built in P4. That is the point: a probe run must produce a service
that did not previously exist, or it is measuring recall.

---

## 2. What varies, and what is nailed down

**The single varied thing is whether the implementer has the dialect and the
generator.**

| | Condition A — `without-dialect` | Condition B — `with-dialect` |
|---|---|---|
| Writes the engine | yes | yes |
| Writes the card | by hand: `templ`, CSS and Go against the SDK, implementing `IWidget[S]` directly | as a `.widget` document, then regenerates |
| Given the dialect docs | no | `dialect.md`, `errors.md`, examples 01 and 03 |
| Given `widgetc` / `gen.sh` | no | yes, as a built binary |
| Given the `templ` CLI | **yes** | **yes** |
| Given the SDK README and `raftdemo/` as a worked host | yes | yes |
| Given the `house-code-style` brief snippet | **yes** | **yes** |

**The skill is held ON in both conditions, and this is the design's most
important line.** `csp-fanin-v1` varies the skill; this task varies the
language. Varying both at once would produce a number that cannot be attributed
to either, and the two tasks would stop being comparable on the fields they
share. If a later run wants the 2×2, it is a third task with four conditions
and its own name — not a quiet widening of this one.

Everything else is held: same model, same task text, same fixed files, same
tool availability, same scratch-module bootstrap.

The `templ` row is the one addition the build stage made to this table, and it
is a holding rather than a widening. `templ` is how the SDK renders — its README
documents it and `raftdemo` uses it — so a condition A that could not compile a
hand-written `.templ` would be missing a tool the SDK expects rather than the one
this task withholds. The varied thing stays the dialect and `widgetc`.

---

## 3. Files

Living in the private skill tree's `evals/bench/` directory, beside
`csp-fanin-v1`, and following its layout exactly.

| File | Who reads it |
|---|---|
| `task.md` | the implementer, verbatim and alone — the full Roundabout service spec, self-contained, naming no path in this repository |
| `engine_spec_test.go.txt` | the implementer, as a fixed file they may not edit |
| `fragments_test.go.txt` | the implementer, as a fixed file they may not edit |
| `bootstrap.sh` | the operator, to prepare one isolated run |
| `grade.sh` | the grader |
| `README.md` | the grader only |

Both fixtures carry the `.txt` suffix for the reason `csp-fanin-v1`'s does: a
tracked `.go` file there would join the census corpus and the reuse gate's
corpus, and neither is house code. `bootstrap.sh` renames them into the scratch
module.

`bootstrap.sh` also makes void rule 2 mechanical rather than only procedural.
The SDK copy a run builds against is this export root **with
`examples/widget/candaws` removed**, so the shipped document, the five built
services and this file are not reachable from inside a run at all. A procedural
rule that a mechanical one could enforce is a rule that will eventually be
broken by accident.

### Why the card assertions must be on rendered output

`fragments_test.go.txt` is the only file both conditions are graded against on
the card, so it may assert **nothing about source**. A hand-written card and a
generated card share no file names, no function names and no formatting; the
one thing they can be held to identically is what comes out of `Render`. So the
fixture mounts the widget in a registry through `widgettest.Mount`, drives it
through the states a report, a control click and the runtime's slow-client
signal can put it in, and asserts on the rendered fragment:

- the region's own addressing attribute, `data-gotth-region="…"`, exactly once
- the root is an `<aside>` whose `aria-labelledby` matches the title's `id`, and
  some element in the fragment carries that `id`
- the title text and the source text
- five nodes, five node titles and **four** node captions — the client carries
  none — and one orbit
- the three stat lines, in declaration order, and the text each of them takes in
  each state it can be in
- both legend entries, in declaration order
- the scene's accessible description, for each of its four clauses
- the balancer's caption for each of the three states, and the backends' for
  both of theirs
- the indicator's positive tone when the rotation is full and its warning tone
  when it is not, and its text for each of its five clauses
- `aria-pressed="true"` on the drain control when draining, `"false"` when not,
  and `"false"` again after a second click
- the motion gate — `data-motion` — open exactly when the balancer is routing
  and not draining, over all eight states that reach it
- the scene's eight pulse elements, in every state

That list is derived from the shipped Roundabout document, which is what makes
it fair to both conditions: it describes a card, not a way of producing one.

### Two assertions this section had wrong, and the test that found them

Both were caught by asking of each line: *would a hand-written card that draws
the same picture fail this?* If it would, the assertion is measuring how the
generator writes markup, and the A/B design's second reference implementation is
exactly the thing that has to survive it.

**"Zero pulse elements when the gate is shut" was unsatisfiable.** The generator
emits every declared pulse unconditionally and gates them with `data-motion` on
the root, which the stylesheet reads. That is the right shape — declared motion
is a property of the document rather than of state, so equal state renders
byte-identical markup either way — but it means the pulse count is constant
across every state a card can be in, and "nothing is animating" is a question
about one attribute and never about a count. The finding is recorded in
[`dialect-v1-asks.md`](dialect-v1-asks.md) § *Adjacent*; it was found while
writing the fleet's card assertions and flagged rather than fixed, because it is
a repair in a document its finder did not own.

The repair is not simply to delete the clause. The assertion the card actually
wants is **two**: the gate, read as `data-motion` and checked over every state
that opens or shuts it, and the eight pulses, asserted *in every state* rather
than only in one. The second half is what stops a card from making motion
disappear — which would render differently for equal state and defeat the patch
suppression the SDK's `Dirty` projection exists for. `widgettest` spells them as
`MotionOpen()` and `Pulses()`, and its doc comment says why they are two methods.

**"The region identity, exactly once" counted the wrong string.** The region is
`widget.candaws.roundabout` and the title element's `id` is
`widget.candaws.roundabout-title`, so a bare substring count of the region finds
it twice in a correct card and the assertion fails on a card that has done
nothing wrong. It is counted as its own attribute — `data-gotth-region="…"` —
which is the thing the claim was always about: one live region, addressable
once.

Every other assertion in the list survived the test, and the hand-written
reference implementation passed all of them on its first compile.

---

## 4. Grading — mechanical only

```bash
cd "$work"
go vet ./...                         # compiled
go test -race ./...                  # tests_pass and race_clean, together
widgetc validate *.widget            # widgetc_clean          (condition B only)
```

| Field | From |
|---|---|
| `compiled` | `go vet ./...` exits 0 |
| `tests_pass` | `go test -race ./...` exits 0 — the engine spec and the fragment assertions together |
| `race_clean` | no `WARNING: DATA RACE` in that output, recorded separately because a run can fail a test *and* be racy and the two mean different things |
| `engine_specs_pass` | the engine spec file's own tests, recorded separately from the fragment file's, because a run that got the concurrency right and the card wrong is a different result from the reverse |
| `card_fragments_pass` | likewise |
| `widgetc_clean` | condition B only: the document validates with zero findings. **Absent for A**, which has no document — a field nobody computed stays absent |

No style outcome is scored. `csp-fanin-v1`'s `used_mutex` rule stands: an
observation is not a score, and a bench row that treats a house rule as a
failure is measuring obedience.

---

## 5. What is measured

### Externally recorded

`tokens`, `tool_calls` and `wall_clock_seconds`, taken from the session
accounting and never from anything the implementer reports about itself.
`wall_clock_seconds` runs from the implementer's first tool call to its last,
so grading time is outside it.

### `diff_in_source_pct`

Documented in the metrics README and computed by nothing until now. The
definition this task fixes:

> Of the card lines the run **authored**, the percentage that landed in a
> canonical source rather than in hand-written card code.

**Scoping ruling: the engine is excluded from both numerator and denominator.**
The engine is written by hand in both conditions, so including it makes the
metric a function of how big the engine is rather than of what the language
did — and this engine is the larger half. Excluding it is what makes the number
mean "was this card edited at its source".

```bash
cd "$work" && git add -A
# canonical source: the widget document (condition B only)
src=$(git diff --cached --numstat -- '*.widget'                     | awk '{s+=$1} END {print s+0}')
# hand-written card code: card files a person wrote, generated output excluded
hand=$(git diff --cached --numstat -- 'card*.go' 'card*.templ' '*.css' \
     | grep -v '_templ\.go' | awk '{s+=$1} END {print s+0}')
python3 -c "print(round(100*$src/($src+$hand), 1))"
```

**Second scoping ruling: emitted output is in neither the numerator nor the
denominator.** The design stage put it in the denominator, and building the
harness measured what that does. The clean condition-B reference — a document
generated and not touched afterwards — scored **30.1**, which is
`1/(1 + amplification)` to within rounding: with emitted output in the
denominator and no hand-written card code, the field is algebraically
`amplification` said a second time and carries no information of its own. It
also leaves § 5's own reading, "a run that hand-edits scores below 100", with no
100 to be below.

The denominator is therefore what the run **authored**: the document plus card
code a person wrote. That is what "was this edited at the source, or in the
generated copy" was always asking, and it restores both intended readings
exactly — the two references measured **100.0** and **0.0**.

The `hand` pattern excludes `*_templ.go` rather than naming the three files a
particular generator writes, so it stays correct for a hand-written card whose
`.templ` was compiled by the same CLI. `task.md` fixes the file layout it
depends on, which is why that section exists at all.

Added lines only: the scratch module starts empty, so there is nothing to
delete. Two readings follow from the definition and both are intended:

- **Condition A scores 0.** Not absent — *computed*, and zero. The denominator
  is non-empty and the numerator is zero because no canonical source exists.
  The contrast between 0 and whatever B scores is the finding, so recording it
  as absent would delete the comparison.
- **A condition-B run that writes card code by hand beside its document scores
  below 100.** That is the metric working. It is not a void and not a failure;
  it is the number saying an author went round the source, which is precisely
  what derivability was defined to catch.

  What it does **not** see is an edit made *inside* a generated file, and that
  is worth stating rather than leaving to be discovered. Such an edit lands in
  emitted output, which this field excludes; what catches it is `gen.sh --check`
  in the repository the card eventually lands in, not a bench row. The field
  measures where a person chose to write, and a person who edits generated
  output has chosen to write in a file the next generation deletes.

### `amplification`

> Emitted lines divided by source lines — how many lines of output one line of
> source bought.

**Two rulings this task fixes.**

*What counts as emitted:* `view.templ`, `view_templ.go` and `widget.gen.go`.
`view_templ.go` is machine output from machine output and counts;
`BUILD.bazel` is a build-system artefact, not widget output, and does not.

*What counts as source:* every line of the `.widget` document, comments
included. Comments in a widget document are load-bearing — the shipped
exemplars carry their teaching in them — and a metric that rewards deleting
them is a metric that will get them deleted.

```bash
emit=$( cat view.templ view_templ.go widget.gen.go | wc -l )
src=$(  wc -l < *.widget )
python3 -c "print(round($emit/$src, 2))"
```

**Absent for condition A**, which has no source; a ratio over zero is not a
reading.

**A prior, so the first row is not a blank slate.** Measured over the three
widgets already committed in this repository, source counted without comments
and without blank lines:

| Widget | source | emitted | ratio |
|---|---|---|---|
| node status | 57 | 338 | 5.9 |
| relay pipeline | 154 | 646 | 4.2 |
| cluster heartbeats | 233 | 867 | 3.7 |

And measured over the fleet documents as generated in P4's design stage —
`widget.gen.go` and `view.templ` only, before `templ` runs — Roundabout is
1.32 against its full 385-line document. So the number the ledger records for
a condition-B run should land near 2 counted the way this document counts it,
and near 4 counted without comments. **A first row far outside that is a
finding about the run, not a new prior**, and belongs in `notes`.

---

## 6. The ledger row

One row per run, appended to the metrics ledger, extending `csp-fanin-v1`'s
shape rather than replacing it.

```json
{"kind": "bench", "date": "<ISO-8601 UTC>", "task": "candaws-roundabout-v1",
 "condition": "with-dialect|without-dialect",
 "skill_sha": "<sha>", "widget_sha": "<sha>",
 "tokens": 0, "tool_calls": 0, "wall_clock_seconds": 0,
 "fixtures_intact": true, "compiled": true, "tests_pass": true, "race_clean": true,
 "engine_specs_pass": true, "card_fragments_pass": true,
 "widgetc_clean": true, "diff_in_source_pct": 0.0, "amplification": 0.0,
 "notes": ""}
```

`fixtures_intact` is `false` on a run that edited a fixed file, which is void
rule 1 (§ 10.3): the row is still appended, and everything after that field in
it describes a run that does not count.

`skill_sha` is recorded for both conditions and is the same in both, because
the skill is held on; `widget_sha` is the widget package commit the dialect
half was keyed to, and is recorded for `without-dialect` too, because it
records *which* dialect was withheld. `widgetc_clean` and `amplification` are
absent from a `without-dialect` row.

The append-only doctrine applies unchanged. A failed run gets a row. A rerun
that quietly replaces a bad run is how a benchmark starts flattering whoever
runs it.

---

## 7. Isolation, and the void rules

**The shipped `roundabout.widget` is condition B's answer key**, and this is
faced rather than avoided. The alternative — designing a seventh service and
never writing its document — was considered and rejected: `csp-fanin-v1`'s own
rule is that a spec nothing has ever passed is not a spec, and a condition that
asks for a validated document must be known satisfiable before the task ships.
Validating that document **is** the satisfiability proof for condition B.
Withholding it would buy secrecy with an unproven task.

So the isolation starts from the one `csp-fanin-v1` already uses — the run
happens in `mktemp -d`, and the implementer gets `task.md`, the two fixed
fixtures, the `templ` CLI, and, in condition B, the dialect documents and the
`widgetc` binary — and then goes one step further than procedure. The run has to
build against the SDK, so it is given a copy of this export root; that copy has
`examples/widget/candaws` removed. The shipped document, the five built
services and this file are therefore not reachable from inside a run at all,
and rule 2 below is a check on the transcript rather than the only thing
standing between a run and the answer key.

Three void conditions. A void run gets a row saying so; it is not deleted.

1. The implementer edited `engine_spec_test.go` or `fragments_test.go`.
2. The implementer read any file under the CandaWS documents directory, this
   file included. Checkable from the transcript, and it is the exact analogue
   of rule 1 — one is the answer to the card, the other to the test.
3. The implementer was shown the grader's `README.md`.

---

## 8. Satisfiability — met on 2026-09-02

`csp-fanin-v1` shipped only after both of its reference implementations had
been run and after the test was checked for the opposite failure — a spec that
passes anything. The same bar applied here, and the task directory exists
because it was cleared. All of it ran in `golang:1.26`, in a scratch directory,
against implementations that were deleted afterwards.

1. **A condition-B reference run.** The shipped `roundabout.widget` through
   `widgetc` and `templ`, a 31-line seam, and an engine written by hand against
   `engine_spec_test.go.txt`. `go vet` clean, 57 tests green, race-clean,
   `widgetc` clean. `diff_in_source_pct` **100.0**, `amplification` **2.33** —
   which lands where § 5's prior said a condition-B run should. The fragment
   assertions are satisfiable *by generated output*.
2. **A condition-A reference run.** The same engine, and the card written by
   hand directly against the SDK as a `templ.ComponentFunc`, with no generator
   involved at any point. Green against the same 57 tests on its **first
   compile**. This is the property the A/B design actually needs — the bar does
   not favour the generator — and it is the exact property `csp-fanin-v1`
   checked when it ran both a channel and a mutex implementation.
3. **A mutation check**, four of them, each caught on the field it should be:
   the third stat dropped from the generated view failed
   `card_fragments_pass` while the engine specs stayed green; `aria-labelledby`
   dropped from the hand-written card failed the landmark assertion and nothing
   else; ejecting at the first probe failure instead of at the threshold failed
   `engine_specs_pass` while the card stayed green; and an unsynchronised
   counter read in `Observe` failed `race_clean` and `tests_pass` together, with
   `WARNING: DATA RACE`. A spec that passes anything measures nothing, and this
   one does not.

The second run is what repaired § 3. Two of the assertions this document
originally listed could not have been passed by any card — one by no card at
all, one by no *correct* card — and both were found by writing the
implementation that had to pass them rather than by re-reading the list.

Neither reference implementation is committed, and neither is the throwaway copy
of the document, for `csp-fanin-v1`'s reason: publishing them hands the answer to
the next implementer. The evidence lives in the grader's `README.md` — which sits
in the private skill tree of § 3 and does not travel with this repository — and
in `docs/lab/2026-09-02-p4-probe-harness/`, a lab entry in the private monorepo
this file is exported from. Both describe what the references proved without
reproducing them; both are pointers a reader of the published repository cannot
follow, and they are named here anyway because a citation to something a reader
cannot open is still more honest than a claim with no source at all.

---

## 9. What this probe cannot measure

Written down so no row is over-read.

- **It measures one service, once, per condition.** `csp-fanin-v1` has a
  distribution; this will not, for a long time. A single pair of rows is an
  anecdote with a ledger entry.
- **The engine is constant across conditions and is the larger half of the
  work.** It cannot be held out — a card with no engine has nothing to render —
  so it dilutes the token and wall-clock signal in both directions equally.
  `diff_in_source_pct` and `amplification` are the two fields that see past it,
  which is why the scoping ruling in § 5 matters more than it looks.
- **It cannot separate "the language helped" from "the examples helped", and
  that applies to both arms rather than only to B.** Condition B receives two
  commented exemplars; condition A receives `raftdemo/` and the SDK README.
  Both are worked precedent and neither is neutral. A run in which B wins is
  evidence for the *stack*, not for the grammar.

  Harness revision 2 does not change this, and it is worth saying plainly
  because the rows might be read as though it did. *Without dialect* means
  without the grammar and the generator; it does **not** mean without the
  dialect's *output*. A stripped tree still carries three fully generated
  example cards, because the examples it keeps do not compile without them, and
  the notes on A2 and A3 record that both runs modelled their card on the
  committed `clusterheartbeats` exemplar. That is disclosed rather than hidden,
  and it is the reason this bullet is about both conditions.

- **`diff_in_source_pct` is a cooperative measurement.** Its "hand-written card
  code" term is the pathspec `card*.go card*.templ *.css` (§ 5), so it measures
  a run that named its files the way the brief asked. `task.md` does ask, under
  "card", and § 5 states the pathspec — so this is disclosed rather than
  hidden — but the field cannot tell a run that wrote no card code by hand from
  one that wrote it under another name. Demonstrated by the P4 audit rather
  than argued: sixty lines of hand-written card code added to a clean
  condition-B tree as `card_extra.go` drop the field from 100.0 to **86.3**,
  and the same sixty lines named `roundabout_card.go` leave it at **100.0**.
- **It says nothing about maintenance.** The claim the ontology actually makes
  is that the *Nth* widget is nearly free and that a change is one edit at the
  source. A first-write benchmark cannot see either. The task that could is a
  third one: change the same service's spec and measure the second diff.

---

## 10. The run protocol

Followed verbatim, in this order, once per condition. Everything here is
mechanical on purpose: a benchmark whose procedure is remembered rather than
written is a benchmark that drifts between its own two halves.

`<kit>` is the task directory in the private skill tree, § 3.

### 10.1 Prepare the run

```bash
work="$(bash <kit>/bootstrap.sh with-dialect    | tail -1)"   # condition B
work="$(bash <kit>/bootstrap.sh without-dialect | tail -1)"   # condition A
```

That writes `$work/sdk` (this export root **without**
`examples/widget/candaws`), `$work/run` (the scratch module, the brief and the
two fixtures, committed as a baseline), and `$work/bin/templ`. Condition B also
gets `$work/dialect/` and `$work/bin/widgetc`.

Record `skill_sha` and `widget_sha` now, before the run starts:

```bash
skill_sha=$(git log -1 --format=%H -- .claude/skills/house-code-style)
widget_sha=$(git log -1 --format=%H -- candace/pkg/widget)
```

Both are recorded for **both** conditions. `widget_sha` on a `without-dialect`
row records *which* dialect was withheld.

### 10.2 The brief

The implementer receives `$work/run/task.md` verbatim and alone, plus the
`house-code-style` brief snippet pasted verbatim — in **both** conditions,
because the skill is held on — plus the paths to the two fixtures, the SDK at
`$work/sdk` with its README and `raftdemo/`, and `$work/bin/templ`.

**The conditions differ in exactly one line of the brief.**

- **`with-dialect`** — *"The widget dialect is at `$work/dialect/`:
  `dialect.md`, `errors.md`, and two worked exemplars. `$work/bin/widgetc`
  validates a document and generates a card from it:
  `widgetc validate x.widget` and
  `widgetc generate -package roundabout -out . x.widget`, then
  `templ generate -f view.templ`."*
- **`without-dialect`** — the line is absent, and no dialect, generator or
  widget document is mentioned anywhere in the brief.

Nothing else in the brief differs. Not a word of encouragement, not an ordering
hint, not a mention of what is being measured.

### 10.3 The void rules

A void run gets a row saying so; it is not deleted. § 7 states them and this is
how each is checked.

1. **The implementer edited a fixture.** `grade.sh` recomputes both fixtures'
   checksums against the grader's own copies and reports `VOID` before anything
   else runs, and the row it prints carries `fixtures_intact` so the verdict
   survives being transcribed.
2. **The implementer read a CandaWS document.** `bootstrap.sh` removes the whole
   directory from the tree the run can see, so this is now mechanically
   prevented rather than only forbidden. It is still checked from the transcript,
   because a determined run could fetch the public repository, and that is a void
   for the same reason.

   Read that sentence as the standing risk it is: `roundabout.widget` publishes
   with this repository and says in its own header that it is condition B's
   answer key, so once the snapshot is public the transcript is the *only* thing
   checking this rule, and the next run's operator is the one who has to check
   it.
3. **The implementer was shown this file or the grader's `README.md`.** Checked
   from the brief that was actually sent.

A fourth, procedural: the implementer runs in its own isolation worktree and
never in this checkout. The run's tree is `$work/run` and nothing in this
repository is on its path.

### 10.4 Grade

```bash
bash <kit>/grade.sh with-dialect "$work"     # or without-dialect
```

It prints one line per field and, last, the row's mechanical half as JSON:
`fixtures_intact`, `compiled`, `tests_pass`, `race_clean`, `engine_specs_pass`,
`card_fragments_pass`, `widgetc_clean` (condition B only), `diff_in_source_pct`
and `amplification` (condition B only). Exit status is 0 when every outcome
passed, 1 when any did not, 2 when the run could not be graded at all.

`fixtures_intact` leads the row because a void invalidates every field after it,
and it is in the row at all because the P4 audit found that it was not: the
`VOID` line was printed in English above a JSON object reading
`"compiled": true, "tests_pass": true, …`, so an operator following § 10.5 by
copying the row rather than the prose recorded a clean pass for a voided run.

The two derivability fields are computed there, from § 5's commands. To read
them by hand:

```bash
cd "$work/run" && git add -A
src=$(git diff --cached --numstat -- '*.widget'                     | awk '{s+=$1} END {print s+0}')
hand=$(git diff --cached --numstat -- 'card*.go' 'card*.templ' '*.css' \
     | grep -v '_templ\.go' | awk '{s+=$1} END {print s+0}')
python3 -c "print(round(100*$src/($src+$hand), 1))"                  # diff_in_source_pct

emit=$( cat view.templ view_templ.go widget.gen.go | wc -l )
python3 -c "print(round($emit/$(wc -l < ./*.widget), 2))"            # amplification, condition B only
```

### 10.5 The row

Everything `grade.sh` printed, plus the fields only the operator can supply:

| Field | Where it comes from |
|---|---|
| `kind`, `task` | always `"bench"` and `"candaws-roundabout-v1"` |
| `date` | the ISO-8601 UTC instant the run was **graded** |
| `condition` | `with-dialect` or `without-dialect` |
| `skill_sha`, `widget_sha` | § 10.1, recorded before the run |
| `tokens`, `tool_calls`, `wall_clock_seconds` | the session accounting, **externally** — never anything the implementer reports about itself. Wall clock runs from the implementer's first tool call to its last, so grading time is outside it |
| `fixtures_intact` | `grade.sh`, both conditions — `false` is void rule 1 firing, and every field after it in the row describes a run that does not count |
| `compiled`, `tests_pass`, `race_clean`, `engine_specs_pass`, `card_fragments_pass` | `grade.sh` |
| `widgetc_clean`, `amplification` | `grade.sh`, condition B only — **absent** from a `without-dialect` row |
| `diff_in_source_pct` | `grade.sh`, both conditions — **0.0 and present** for A, never absent |
| `notes` | free text: what went wrong, what was unusual, why a run was voided, and any reading far outside § 5's prior |

Appended to the metrics ledger, one row per run, append-only. § 6 has the shape.

### 10.6 What to do about a surprise

`amplification` near 2 and `diff_in_source_pct` at 100 is what a clean
condition-B run looks like; 0.0 is what a clean condition-A run looks like on the
second field and the first is absent. A first row far outside that is **a finding
about the run, not a new prior**, and belongs in `notes` — not in an edit to this
document. § 9 is the standing list of what a pair of rows cannot tell you, and it
is worth re-reading before any of them is quoted anywhere.

### 10.7 Harness revision 2 — the boundary moved from the brief to the world

Run A1 (2026-09-02) dissolved the control: its brief withheld the dialect, but
the SDK copy carried the generator, the dialect corpus and worked documents,
and CS-4 — held on in both arms by design — led the run to write a clean
document and generate the card. Not void under § 7; kept as a finding, and the
cleanest reading it produced is the **discovery cost**: same method in both
arms, +16,097 tokens and +150 s for the arm that had to find the tool.

From revision 2, `bootstrap.sh` strips a `without-dialect` tree of the dialect
corpus, the generator, its CLI, and every `.widget` document, so the withheld
condition is a property of the world the run can reach, not of the prose it
was sent. A1's row stands, append-only, labelled with the dissolution.
