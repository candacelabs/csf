# Bench `candaws-roundabout-v1`

The second task in the agentic A/B benchmark, and the first that measures the
**language** rather than the skill. `csp-fanin-v1` varies the skill stack; this
one holds the skill on in both conditions and varies whether the implementer has
the [widget](../../../../../../csf/docs/generated/ontology_cgen.md#term-widget) dialect and its generator.

> Does writing a [widget](../../../../../../csf/docs/generated/ontology_cgen.md#term-widget) as a `.widget` document and regenerating it cost less
> than writing the same card by hand against the SDK?

The design is `examples/widget/candaws/docs/bench.md`. **This directory
is the runnable half of it** — the brief, the two fixed fixtures, and the two
scripts that prepare and grade a run. Read the design for what the fields mean
and why the conditions are what they are; read this for how to run one.

## Files

| File | Who reads it |
|---|---|
| `task.md` | the implementer, verbatim and alone — the full Roundabout [service](../../../../../../csf/docs/generated/ontology_cgen.md#term-service) spec, self-contained, naming no path in this repository |
| `engine_spec_test.go.txt` | the implementer, as a fixed file they may not edit |
| `fragments_test.go.txt` | the implementer, as a fixed file they may not edit |
| `bootstrap.sh` | the operator, to prepare one isolated run |
| `grade.sh` | the grader |
| `README.md` | the grader only — the implementer must **not** see it, for `csp-fanin-v1`'s reason: an implementer who knows the grep is coming is no longer a sample of ordinary behavior |

Both fixtures carry the `.txt` suffix for the reason `csp-fanin-v1`'s does: a
tracked `.go` file here would join the census corpus and the reuse gate's
corpus, and neither is house code. `bootstrap.sh` renames them into the scratch
module.

## Running one condition

```bash
work="$(bash bootstrap.sh with-dialect    | tail -1)"   # condition B
work="$(bash bootstrap.sh without-dialect | tail -1)"   # condition A
```

`bootstrap.sh` builds a scratch tree that holds the SDK **with the whole CandaWS
directory removed**, the scratch module, the brief, the two fixtures under the
names the brief promises, and the templ CLI. Condition B additionally gets
`dialect.md`, `errors.md`, examples 01 and 03, and a built `widgetc`.

The pruning is deliberate. `bench.md` § *Isolation* makes "the implementer read
a CandaWS document" a void condition, and a procedural rule that a mechanical
one could enforce is a rule that will eventually be broken by accident. The
shipped Roundabout document, the five built fleet [services](../../../../../../csf/docs/generated/ontology_cgen.md#term-service) and the probe design
are all under `examples/widget/candaws`, and none of them is in the tree a run
can see.

Give the implementer the work directory and `task.md`, and nothing else.

```bash
bash grade.sh with-dialect "$work"
```

## Grading

Every outcome is mechanical, and every one comes from **rendered output and test
results**. Nothing in `grade.sh` reads a line of the implementation's source: it
opens its own two fixtures, to recover their test names and their checksums, and
it counts how many lines landed in which file — never what those lines say. That
is not fastidiousness. A hand-written card and a generated card share no file
names and no function names, so a grader that greps source is grading one
condition on a bar the other cannot reach.

| Field | From |
|---|---|
| `compiled` | `go vet ./...` exits 0 |
| `engine_specs_pass` | the engine fixture's own tests, run alone |
| `card_fragments_pass` | the fragment fixture's own tests, run alone |
| `tests_pass` | `go test -race ./...` over both, exits 0 |
| `race_clean` | no `WARNING: DATA RACE` in that output |
| `widgetc_clean` | condition B only: the document validates with zero findings |
| `diff_in_source_pct` | of the card lines the run **authored**, the percentage in the [widget](../../../../../../csf/docs/generated/ontology_cgen.md#term-widget) document |
| `amplification` | emitted lines over source lines; absent for condition A |

The two fixtures are run separately as well as together, because a run that got
the concurrency right and the card wrong is a different result from the reverse
and one boolean cannot say which.

No style outcome is scored. `csp-fanin-v1`'s `used_mutex` rule stands: an
observation is not a score, and a bench row that treats a house rule as a
failure is measuring obedience.

## Satisfiability

`csp-fanin-v1`'s rule — a spec test nothing has ever passed is not a spec — was
met before this directory shipped. On 2026-09-02, in `golang:1.26`, against two
throwaway reference implementations written in a scratch directory and deleted
afterwards:

- **generated** — the shipped Roundabout document run through `widgetc` and
  `templ`, a 31-line seam, and an engine written by hand against the engine
  fixture: `go vet` clean, 57 tests green, race-clean, `widgetc` clean,
  `diff_in_source_pct` **100.0**, `amplification` **2.33**.
- **hand-written** — the same engine, and the card written directly against the
  SDK as a `templ.ComponentFunc` with no generator involved at all: `go vet`
  clean, the same 57 tests green, race-clean, `diff_in_source_pct` **0.0**.

Both pass, which is the property the A/B design actually needs: **the bar does
not favour the generator.** The hand-written card compiled and passed the
fragment assertions on its first run, which is the strongest available evidence
that the fragment fixture describes a card rather than a way of producing one.

The fixtures were then checked for the opposite failure — a spec that passes
anything. Four mutations, each caught, each on the field it should be:

| Mutation | What failed |
|---|---|
| the third stat line dropped from the generated view | `card_fragments_pass`, on the stat count and the two tests that read the health line; engine specs still green |
| `aria-labelledby` dropped from the hand-written card's root | `card_fragments_pass`, on the landmark; engine specs still green |
| ejection at the first probe failure instead of at the threshold | `engine_specs_pass`, on "below the threshold never ejects" and "a success resets the count"; card fragments still green |
| an unsynchronised counter read in [`Observe`](../../../../../../csf/docs/generated/ontology_cgen.md#term-observe) | `race_clean` **and** `tests_pass`, with `WARNING: DATA RACE`; card fragments still green |

Neither reference implementation is committed, and neither is the throwaway copy
of the document. They exist in this file as evidence that the grading bar is
reachable; publishing them would hand the answer to the next implementer.

## Ledger row

The shape is in `bench.md` § 6. `grade.sh` prints its mechanical half as JSON on
its last line; the run's externally-recorded fields — `tokens`, `tool_calls`,
`wall_clock_seconds`, `skill_sha`, `widget_sha`, `notes` — are added by the
operator and the whole row is appended to `../../../metrics/ledger.jsonl`.

The append-only doctrine applies unchanged. A failed run gets a row. A void run
gets a row saying so. A rerun that quietly replaces a bad run is how a benchmark
starts flattering whoever runs it.
