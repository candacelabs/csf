# Bench `csp-fanin-v1`

The reusable task for the agentic A/B benchmark of `docs/widget_foundry.md`
amendment 2: the same brief run **with** the skill stack and **without** it,
graded mechanically, recorded as append-only `bench` rows.

Built for CS-5, and chosen because a fan-in aggregator is the smallest honest
version of the choice the rule is about — many producers, shared totals,
graceful shutdown — and because it is genuinely implementable both ways. That
is not an assumption: both reference implementations were run before this task
shipped (see "Satisfiability" below).

## Files

| File | Who reads it |
|---|---|
| `task.md` | the implementer, verbatim and alone |
| `fanin_test.go.txt` | the implementer, as a fixed file they may not edit |
| `README.md` | the grader; the implementer must **not** see it — it says what is measured, and an implementer who knows the grep is coming is no longer a sample of ordinary behavior |

`fanin_test.go.txt` is held with a `.txt` suffix for the reason the eval
fixtures are (`../../README.md` § "Why the fixtures are not .go files"): a
tracked `.go` file here would join the census corpus and the reuse gate's
corpus, and this one is neither house code nor compilable in place. The grader
renames it on the way into the scratch module.

## Running one condition

```bash
work="$(mktemp -d)"
cd "$work" && go mod init fanin
cp <skill>/evals/bench/csp-fanin-v1/fanin_test.go.txt "$work/fanin_test.go"
```

Give the implementer `task.md` and the path to `fanin_test.go`, and nothing
else. The two conditions differ in exactly one thing:

- **`with-skill`** — `references/brief-snippet.md` is pasted verbatim into the
  brief, as the skill requires for any stage that writes Go.
- **`without-skill`** — it is not, and no rule is mentioned.

Everything else is held constant: same model, same task text, same test file,
same tool availability. Record tokens and tool calls **externally** — from the
session/transcript accounting, not from anything the implementer reports about
itself.

## Grading

```bash
cd "$work"
go vet ./...            # compiled
go test -race ./...     # tests_pass and race_clean, together
grep -REn 'sync\.(RW)?Mutex' --include='*.go' . | grep -v '_test\.go'   # used_mutex
```

Four mechanical outcomes and one observation:

| Field | From |
|---|---|
| `compiled` | `go vet` exits 0 |
| `tests_pass` | `go test -race` exits 0 |
| `race_clean` | no `WARNING: DATA RACE` in that output — recorded separately because a run can fail a test *and* be racy, and the two mean different things |
| `used_mutex` | the grep finds a mutex declaration in non-test source |

`used_mutex` is an observation, **not** a score. CS-5 permits a leaf mutex, and
this task has a defensible mutex implementation — the interesting measurement
is whether the condition changes the distribution, not whether any one run
"complied". A bench row that treats a mutex as a failure would be measuring
obedience and calling it style.

## Ledger row

One row per run, appended to `../../../metrics/ledger.jsonl`:

```json
{"kind": "bench", "date": "<ISO-8601 UTC>", "task": "csp-fanin-v1",
 "condition": "with-skill|without-skill", "skill_sha": "<sha>",
 "tokens": 0, "tool_calls": 0, "compiled": true, "tests_pass": true,
 "race_clean": true, "used_mutex": false, "notes": ""}
```

| field | meaning |
|---|---|
| `kind` | always `"bench"` |
| `date` | ISO-8601 UTC instant the run was graded |
| `task` | this directory's name, so a later task is a different series |
| `condition` | `with-skill` or `without-skill` |
| `skill_sha` | the skill-tree commit the condition was keyed to — for `without-skill` too, because it records *which* skill was withheld |
| `tokens` | total tokens for the implementer's session, measured externally |
| `tool_calls` | total tool calls, likewise |
| `compiled` | `go vet ./...` exited 0 |
| `tests_pass` | `go test -race ./...` exited 0 |
| `race_clean` | no data race reported |
| `used_mutex` | a `sync.Mutex`/`sync.RWMutex` declaration in non-test source |
| `notes` | free text: what went wrong, what was unusual, why a run was voided |

The ledger doctrine applies unchanged (`../../README.md` § "Rows, and the
append-only doctrine"). A failed run gets a row. A run whose implementer edited
`fanin_test.go` is **void**, and gets a row saying so rather than being deleted
— a rerun that quietly replaces a bad run is how a benchmark starts flattering
whoever runs it.

## Two optional fields for a widget-era task

A bench task where the implementer works through a **canonical source** — a
[widget](../../../../../../csf/docs/generated/ontology_cgen.md#term-widget) document compiled to output, rather than Go written directly — may
record two further fields. They are the bench level of the derivability metric
(`../../../metrics/README.md` § "Derivability"), which asks how much of what a
machine could produce is still produced by a person:

| field | meaning |
|---|---|
| `diff_in_source_pct` | of the changed lines the run produced, the percentage that landed in the canonical source rather than in emitted output — "was this edited at the source, or in the generated copy" |
| `amplification` | emitted lines divided by source lines — how many lines of output one line of source bought |

Both are **optional**, both are **documented and not yet computed**, and
neither has a threshold: a low `amplification` is not a failure, it is a
reading. They are written down now so the first task that computes them writes
into a shape that already exists, rather than minting field names under time
pressure and leaving two spellings in the ledger.

**The `csp-fanin-v1` rows already in the ledger are not back-filled with
either.** That task has no canonical source — its implementers wrote Go
directly — so there is no honest value to compute, and computing one anyway
would be inventing a measurement rather than recording one. This is the same
boundary rule `../../../metrics/README.md` applies to census schema versions:
a row is a true record of what the tooling of the day measured, and a field
nobody computed at the time stays absent. Compare across the boundary on the
fields both sides have.

## Satisfiability

A spec test nothing has ever passed is not a spec, so this one was checked
before shipping. On 2026-09-02, in `golang:1.26`, against two throwaway
reference implementations written in a scratch directory and deleted
afterwards:

- **channels** — one owner [goroutine](../../../../../../csf/docs/generated/ontology_cgen.md#term-goroutine) holding `counts`/`total` as locals, a
  buffered submission channel, snapshot-by-reply-channel, close draining the
  buffer before returning: `go vet` clean, `go test -race` **ok, 1.06s**.
- **mutex** — an `RWMutex` around the map, snapshot copying under `RLock`,
  a `closed` flag: `go vet` clean, `go test -race` **ok, 1.54s**.

Both pass, which is the property the A/B design actually needs: the bar does
not favor a style.

The test was then checked for the opposite failure — a spec that passes
anything. The mutex implementation was mutated to return its internal map from
`Snapshot` instead of a copy, and the run failed as it should:
`TestSnapshotIsACopy`, `TestSnapshotIsConsistentUnderLoad` and
`TestConcurrentSnapshotsDuringSubmit` all failed, with data races reported.

Neither reference implementation is committed. They exist in this README as
evidence that the grading bar is reachable, and publishing them would hand the
answer to the next implementer.
