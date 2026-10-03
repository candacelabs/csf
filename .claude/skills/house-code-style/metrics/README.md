# Metrics ledger

`ledger.jsonl` is the [Widget](../../../../csf/docs/generated/ontology_cgen.md#term-widget) Foundry metrics ledger: one JSON object per line,
appended, never edited. It is how the program answers "is the cost per [widget](../../../../csf/docs/generated/ontology_cgen.md#term-widget)
actually falling" with something other than an impression
(`docs/widget_foundry.md`, decision 4).

Row kinds, the command that produces each, and the field-by-field meanings live
in `../evals/README.md` § "Rows, and the append-only doctrine"; the `census`
row's fields are documented in `../scripts/style_census.py`, the
`derivability` row's in `../scripts/derivability_census.py`, and the `bench`
row's in `../evals/bench/csp-fanin-v1/README.md`. They are not restated here,
because two copies of a schema is how a reader ends up trusting the stale one.

## Ledger schema versions

A schema-stamped row carries the version that produced it. What lives here is
the *version history* — which is this file's business, because reading the
ledger means knowing which rows can answer which question.

**[Schema](../../../../csf/docs/generated/ontology_cgen.md#term-schema) numbers are ledger-wide, not per-kind, and are never reused.** A
reader who sees `schema: N` knows exactly which field set produced the row
without first working out what kind it is. That is why the derivability row
launches at `4` rather than at `1`: `1`–`3` were spent by the census, and a
second numbering starting over would make `schema: 2` ambiguous the moment
anyone quoted it without its `kind`.

| `schema` | `kind` | From | Adds | Read it knowing |
|---|---|---|---|---|
| absent (1) | `census` | 2026-09-02, program start | — | Structural counts only. The number `check_style.py` printed at that commit **cannot be reconstructed** from these rows: CS-2's finding count is per-signature and the row records per-parameter totals, and CS-1's excludes nothing the row can identify. |
| `2` | `census` | 2026-09-02, with CS-5 | `schema`, `mutex_fields`, `mutex_vars`, `mutex_rwmutex`, `cs1_findings`, `cs2_findings`, `cs5_findings` | The gate's printed numbers are in the row (`docs/widget_foundry.md` amendment 7), alongside the structural counts that say what the debt is made of. `cs5_findings == mutex_fields + mutex_vars` by construction; a row where they differ means the gate and the scanner drifted. |
| `3` | `census` | 2026-09-02, with CS-6 | `dispatch_methods`, `dispatch_calls_total`, `dispatch_calls_max`, `cs6_findings` | Same construction as schema 2, one rule later. `cs6_findings == dispatch_methods`, with the same drift check. `dispatch_calls_max` is here because CS-6's count alone is uninformative at the low end: **a zero row is the expected reading on this corpus**, and what a non-zero row needs to say next is how long the worst dispatch list is. A schema-2 row cannot answer any CS-6 question; it was measured before the rule existed. |
| `4` | `derivability` | 2026-09-02, with the derivability metric | A **new row kind**, not a census field set: `generated_go_files`, `handwritten_go_files`, `generated_go_lines`, `handwritten_go_lines`, `derived_line_pct`, `backlog_open`, `backlog_derivation_built`, `backlog_derived`. Alongside it, a registry (`derivable_backlog.json`) and two optional `bench` fields. | The census row is untouched and stays at schema 3 — its field set did not change, and bumping a version number for a field set that did not move is how a version number stops meaning anything. `handwritten_go_files` equals the census row's `corpus_files` at the same commit by construction; a pair of same-sha rows where they differ means the two scripts have stopped sharing a corpus. |
| `5` | `census` | 2026-09-02, with CS-7 | `erased_boundaries`, `erased_boundary_types`, `erased_aliases`, `cs7_findings` | Same construction as schemas 2 and 3, one rule later. `cs7_findings == erased_boundaries` by construction, with the same drift check. The census skips 4 because that number belongs to the derivability row and schema numbers are ledger-wide. `erased_boundary_types` is here for the reason `dispatch_calls_max` is: the count alone is uninformative. 9 findings across 1 type is one contract threaded through its own methods — the founding shape, fixed in one commit — while 9 across 9 types is nine unrelated decisions, and those want opposite responses. `erased_aliases` counts package-local `type X any` declarations, which is erasure hidden behind a name; the CS-7 detector resolves them, and a corpus where that number rises is one where a bare-`any` search would increasingly miss the thing. A schema-3 row cannot answer any CS-7 question; it was measured before the rule existed. |
| `6` | `census` | 2026-09-02, with CS-8 | `interface_return_funcs`, `interface_return_exempt`, `interface_return_methods`, `cs8_findings` | Same construction as schemas 2, 3 and 5, one rule later. `cs8_findings == interface_return_funcs - interface_return_exempt` by construction, with the same drift check — this row's identity has a subtraction in it because CS-8 is the first rule whose detector reports fewer findings than shapes it found, and a reader who cannot see the exemptions cannot tell a rule that got narrower from a corpus that got better. `interface_return_methods` is the [scope](../../../../csf/docs/generated/ontology_cgen.md#term-scope) the rule declines to enforce: a method's result type is fixed by the interface it satisfies, so CS-8 never reports one, and a corpus where this number climbs while `cs8_findings` holds at 0 is one where factories moved into method sets. A schema-5 row cannot answer any CS-8 question; it was measured before the rule existed. |
| `7` | `census` | 2026-09-02, with CS-9 | `test_files`, `test_sleep_loops`, `test_sleep_files`, `cs9_findings` | Same construction as schemas 2, 3, 5 and 6, one rule later. `cs9_findings == test_sleep_loops` by construction, with the same drift check. This is the first census field set with a **denominator** in it, and CS-9 is the reason one is needed: a rising sleep count in a tree that doubled its test files is not the same event as a rising sleep count in a tree that did not, and every earlier rule's structural counts were over a corpus that moves slowly by comparison. `test_sleep_files` is the composition field, and it carries the whole argument the rule was minted on: ten sleeps across nine files is nine authors writing the same await helper, while ten in one chaos suite is one suite whose subject is pacing — the same number, opposite readings, and only the second is fine. A schema-6 row cannot answer any CS-9 question; it was measured before the rule existed. |
| `8` | `census` | 2026-09-03, with CS-11 and CS-8's struct-field amendment | `dot_assertion_test_imports`, `nondot_assertion_test_imports`, `assertion_import_exempt`, `cs11_findings`, `interface_fields` | Same construction as schemas 2, 3, 5, 6 and 7, one rule later. `cs11_findings == nondot_assertion_test_imports - assertion_import_exempt` by construction — a subtraction in the identity, like schema 6's, because CS-11 has an exemption its finding count nets out: an in-package test whose package declares a name the library exports keeps its import qualified because a dot import would not compile. `dot_assertion_test_imports` is the denominator, and it carries the fact CS-11 was minted on — the convention was followed 709 times before it was enforced, so the rule guards a near-universal habit rather than repairs a debt. `assertion_import_exempt` is the [scope](../../../../csf/docs/generated/ontology_cgen.md#term-scope) the rule cannot enforce: a corpus where it rises is one gaining packages that mirror the assertion vocabulary (redis's `Entry`, eventually's `Consistently`), not one getting worse. A schema-7 row cannot answer any CS-11 question; it was measured before the rule existed. `interface_fields` shares this schema because CS-8's struct-field amendment landed the same day: it counts struct fields whose declared type is an interface — the return-position rule reaching the field that would otherwise smuggle an interface out of a concrete value. Two rules bumped the census on 2026-09-03, so schema 8's field set is the union of both; the historical rows that predate the merge carry only the half their branch had measured, and old rows are never back-filled. |
| `9` | `census` | 2026-09-04, with CS-12 | `cs12_findings` | The bare-constructor count, and the first census field with no structural companion: `func New(` **is** the structure, so a second number would repeat it. Added after the fact — the schema-9 stage shipped the field without this row, and rows are never back-filled, so a schema-9 row in the ledger is exactly a schema-8 row plus `cs12_findings`. |
| `10` | `census` | 2026-09-04, with CS-13 | `magic_strings`, `cs13_findings` | Same construction as schemas 2, 3, 5, 6 and 7: `cs13_findings == magic_strings` by construction, because CS-13's exemptions are applied inside the detector rather than subtracted afterwards, and a row where the two differ means the gate and the scanner drifted. Read the number knowing what it is: **2856 at introduction**, which is a backlog and not a regression — CS-13 never blocks. |
| `11` | `census` | 2026-09-04, with CS-14 | `null_twins`, `cs14_findings` | Same construction as schema 10: `cs14_findings == null_twins`, because CS-14's [scope](../../../../csf/docs/generated/ontology_cgen.md#term-scope) is applied inside the detector rather than subtracted afterwards, and a row where the two differ means the gate and the scanner drifted. Read the number knowing what it is: the detector found **5 at introduction**, all five in one file (`pkg/cron/postgres/store.go`), and the first schema-11 row records **0**, because that file was migrated onto guregu/null in the same change. A rule whose first ledger row is already zero is the unusual case, and the reason is that the corpus had been mostly repaired before the lint existed to name it: the copilot adapter's three twins were deleted when its sqlc overrides landed. The 5 is therefore the remainder, not the debt, and it is recorded here because no ledger row holds it. A schema-10 row cannot answer any CS-14 question; it was measured before the rule existed. |
| `12` | `census` | 2026-10-01, with CS-18 | `test_crossings`, `test_crossing_files`, `acceptance_suites`, `cs18_findings` | Same construction as schema 11: `cs18_findings == test_crossings`, because the legacy `CS-18-CROSSING` detector applies the acceptance-suite [scope](../../../../csf/docs/generated/ontology_cgen.md#term-scope) itself. `cs18_findings` counts the **crossing part only**; the native `CS-18-MOCKGEN` and `CS-18-EXTERNAL` parts have no legacy lane and no census field, and their counts live in the native report and the CS-18 lab entry. `test_crossing_files` is the composition field (one harness versus many suites); `acceptance_suites` counts files labelled `//go:build acceptance`, so a falling crossing count with a rising acceptance count is suites being labelled, not mocked. Read it knowing the first schema-12 row was taken after CS-11..14 had been dormant in the ledger since 2026-09-16: the native checker replaced the legacy gate on 2026-09-17. A schema-11 row cannot answer any CS-18 question. |

## Why no census field counts every interface return

CS-8 gained a second, non-blocking CI lane on 2026-09-02
(`tools/check-ifacereturn.sh`, over the `go/analysis` analyzer at
`tools/ifacereturn`). It reports **every** interface-typed function and
method result in every first-party module — stdlib, third-party, `any`, method
position, generated code — with `error` the only exemption, and it measured 407
on landing.

That number is not a census field, and the omission is deliberate rather than
pending. `../scripts/style_census.py` is stdlib-only Python 3 **by design**, so
it runs on hosts with no Go toolchain and no Docker; that is the same property
that lets `check_style.py` gate a repository whose whole Go story is
containerized. A field that could only be filled by building and running a Go
analyzer would make the census either unrunnable or silently partial on exactly
the hosts it is most often run on, and a census row that is sometimes complete
is worse than one that never claimed to be.

The wide lane's number lives where it is produced: the CI step summary (count
plus a by-interface tally) and the lab entry that landed it. A schema bump would
be the wrong instrument for a measurement that needs a toolchain the census
refuses to require.

---

## The CS-1/CS-2/CS-8 gate went blocking, and why no row says so

**2026-09-02.** `.github/workflows/house-lint.yml` now runs
`check_style.py --strict --rule CS-1 --rule CS-2 --rule CS-8` on every pull
request and on every push to `main`, and exit 1 there fails the run. Before that
day all three were report-only; slice P3 of the [Widget](../../../../csf/docs/generated/ontology_cgen.md#term-widget) Foundry program took CS-1
from 61 findings to 0 and CS-2 from 374 to 0 over the same 699-file corpus, and
CS-8 shipped later the same day at 9 and was fixed to 0 in the commit before it
joined them. Each flip followed its 0; none preceded it. CS-5, CS-6 and CS-7 are
not in that command and never will be — each rule's own text in
`../references/go-rules.md` says so, and `../references/exceptions.md` records
what stays unfixed outside the corpus.

**This event has no ledger row, deliberately.** A `program` row would have been
the obvious way to mark it, and it would have been **hand-typed** — a claim, not
a measurement. `../evals/README.md` § "Rows, and the append-only doctrine" says
never hand-type a row: run the tool and append its stdout. Nothing measures "a
workflow file exists", so nothing can produce that row honestly, and inventing
one would put a sentence in a file whose entire value is that every line in it
came out of a program. The flip is therefore recorded *here*, in prose, next to
the schema history it belongs with.

What the ledger does carry is the evidence the flip rests on, and it carries it
the only way it can: the census rows at `6e3ff1031` (61 / 374), `1f83b0420`
(0 / 374) and `2c1eda90b` (0 / 0), each written by `../scripts/style_census.py`.
A reader who wants to know whether the gate could honestly block on a given
commit reads `cs1_findings` and `cs2_findings` in that commit's row. That is a
better record than a row asserting a policy, because it is checkable.

The census schema does **not** move for this. No field changed — `cs1_findings`
and `cs2_findings` meant the same thing the day before, and so did
`cs8_findings`, which schema 6 had already added while the rule was still
report-only — and bumping a version number for a field set that did not move is
how a version number stops meaning anything. The evidence for CS-8's flip is
read the same way as for the other two: the census rows either side of the fix
commit carry `cs8_findings` 9 and then 0.

Old rows are **not** back-filled. A schema-1 row is a true record of what was
measured with the tooling of the day, and rewriting it to carry fields nobody
computed at the time would be inventing a measurement — the same failure as
editing a row to improve a trend. Compare across the boundary on the fields
both versions have.

When a later schema arrives, add a row to this table with its kind and take the
next unused number; never reuse a version number for a different field set. A
census field change also bumps `_CENSUS_SCHEMA` in `../scripts/style_census.py`,
a derivability one `_LEDGER_SCHEMA` in `../scripts/derivability_census.py`. The
census going `3` → `5` is that rule working as intended rather than a gap: `4`
was spent by the derivability row, and a census numbered `4` would have made
`schema: 4` ambiguous the moment anyone quoted it without its `kind`.

Append a row by running the tool that measures it:

```bash
python3 .claude/skills/house-code-style/scripts/style_census.py \
  >> .claude/skills/house-code-style/metrics/ledger.jsonl
python3 .claude/skills/house-code-style/scripts/derivability_census.py \
  >> .claude/skills/house-code-style/metrics/ledger.jsonl
python3 .claude/skills/house-code-style/evals/run_eval.py \
  >> .claude/skills/house-code-style/metrics/ledger.jsonl
```

[Check](../../../../csf/docs/generated/ontology_cgen.md#term-check) the file still parses before committing it:

```bash
python3 -c "import json,sys; [json.loads(line) for line in open(sys.argv[1]) if line.strip()]" \
  .claude/skills/house-code-style/metrics/ledger.jsonl
```

Rows are never deleted or edited to improve a trend, and a regression gets a
row plus a written reason rather than a rerun.

## Derivability

Derivability is a **metric, not a style rule.** It has no CS number, no gate
and no strict mode, and nothing in this repository fails because of it. It
extends the ledger the way the `bench` rows did for CS-5: a new kind of reading,
not a new thing to obey. The definition, in full:

> **derivability = derived / (derived + derivable-but-hand-maintained)**

The interesting word is *derivable*. A number that only counted what has
already been generated would measure output volume; the denominator is what
makes it measure the question the program actually cares about — how much of
what a machine *could* produce is still produced by a person.

It is read at three levels, and they answer different questions.

### Level 1 — census, mechanical and repo-wide

`../scripts/derivability_census.py` partitions tracked first-party Go by the
`^// Code generated .* DO NOT EDIT\.$` marker and emits one `derivability` row:
file and line counts on both sides, `derived_line_pct`, and the backlog status
counts from level 2. It shares `go_style_scan.py`'s corpus predicate and its
`is_generated()` with the style gate, so the two can never disagree about which
files are derived.

This level is cheap, honest about what it measures, and **the gameable half.**
It moves if someone commits a larger generated artifact, with no projection
having stopped being hand-maintained.

### Level 2 — backlog, the honest denominator

`derivable_backlog.json` is the registry of projections that *could* be derived
from a canonical source and that shipped code maintains by hand instead. Each
entry is `{id, projection, canonical_source, hand_maintained_at, added, status,
status_note}`, with three statuses:

| `status` | Means |
|---|---|
| `open` | No derivation exists anywhere. |
| `derivation-built` | The derivation exists but the shipped code still reads a hand-maintained copy. |
| `derived` | The shipped code reads the derivation and the hand-maintained copy is gone. |

`derived` is deliberately the hardest to reach. A derivation that exists and is
not adopted has removed no hand-maintenance, and scoring it as success is how a
program congratulates itself for building a generator nobody runs.

**The registry is state; the ledger is measurement.** This is the one place in
the metrics directory where editing in place is correct, and it is worth saying
out loud next to a file whose whole doctrine is append-only. A `status` is
*updated* when reality changes — that is what a registry is for, and appending
a second entry for the same projection would double-count the denominator.
`ledger.jsonl` is the opposite and stays the opposite: a row is appended and
never touched again. What keeps the registry auditable despite being mutable is
that **every status change names the commit or sha that earned it** in
`status_note`, so the claim "this is derived now" always points at the change
that made it true.

**Why the denominator is the anti-gaming mechanism, and the two rules that
protect it.** Derivability cannot be improved by generating more boilerplate:
the census level would move, and the backlog ratio would not, because the score
at this level moves *only when a generator eats a registered projection*. That
leaves exactly one way to cheat, and it is closed explicitly:

1. **An entry is never deleted to improve the ratio.** Deleting one shrinks the
   denominator without deriving anything — the single edit that raises the score
   for free.
2. **A wrongly-registered projection keeps its entry** and gains a
   `status_note` explaining why registering it was a mistake. It stays in the
   file, and stays visible, because "we were wrong to call this derivable" is a
   finding and deleting it is a cover-up.

Adding an entry *lowers* the score, and that is the intended direction: a
registry that only ever grows when someone is about to derive something is a
registry that has been curated into a trophy.

### Level 3 — bench, documented and not computed

A `bench` row for a widget-era task may carry two optional fields,
`diff_in_source_pct` and `amplification`, defined in
`../evals/bench/csp-fanin-v1/README.md`. They say what share of a change landed
in the canonical source rather than in emitted output, and how many emitted
lines one source line bought.

Nothing computes them yet, and **the existing `csp-fanin-v1` rows are not
back-filled.** Back-filling a field nobody computed at the time would be
inventing a measurement — the same doctrine this file already applies to census
schema boundaries, and the same one that forbids editing a row to improve a
trend. The fields exist so the first task that *does* compute them has a shape
to write into.
