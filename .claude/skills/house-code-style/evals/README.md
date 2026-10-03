# Eval corpus

This skill exists because of one measurement: a rule that lived in
`go/CLAUDE.md` and was checked by nothing had a compliance rate of 0 out of 15
(`../references/lessons.md`). The same argument applies to the skill itself. A
skill nobody measures is a preference nobody enforces — it just fails later,
and more expensively, because by then people believe it is working.

So the skill is measured, in two tiers, and every run appends a row to
`../metrics/ledger.jsonl`.

## Tier 1 — mechanical

A dirty/clean fixture pair plus an `expected.json` naming the rule IDs that
must fire on the dirty fixture and must not fire on the clean one.
`run_eval.py` judges them with `../scripts/check_style.py`'s own
`collect_findings` and the shared lexer — not a copy of the rules — so a
scanner change that stops seeing a violation fails the eval instead of quietly
shrinking the census.

```bash
python3 evals/run_eval.py                       # human check: rows on stdout, detail on stderr
python3 evals/run_eval.py >> metrics/ledger.jsonl   # record it
```

Deterministic, runs in under a second, exits 1 on any failure. Three
assertions per case, all required: the listed rules fire on `dirty/`, **no
other rule fires there** (cases stay isolated to one rule, so a failure names
one cause), and **nothing at all** fires on `clean/` — including a rule the
case's `expected.json` never thought to list.

What tier 1 proves is narrow and worth stating plainly: the gate still detects
the shape. It cannot tell you whether an [agent](../../../../csf/docs/generated/ontology_cgen.md#term-agent) that loaded the skill wrote
better code. That is tier 2's job, and no amount of green tier-1 rows
substitutes for it.

## Tier 2 — judgment

A `prompt.md` and a `rubric.md` of numbered criteria, each with an explicit
"Fails if". The case is run by giving a model the prompt with this skill
available, and scoring the answer against the rubric — by the operator or by an
LLM judge. Every criterion is required unless the rubric says otherwise: the
case passes only when all of them pass, and the row records which ones failed.

`run_eval.py` counts tier-2 cases and names them on stderr but never scores
them. It will not emit a row it did not measure; the judge appends theirs.

## Tier 3 — the A/B bench

`bench/` holds reusable implementation tasks for the agentic A/B benchmark
(`docs/widget_foundry.md` amendment 2): the same brief run with and without the
skill stack, graded by compiling and running the result rather than by a
rubric. `bench/csp-fanin-v1/` is the first one, built for CS-5.

A bench task is not an eval case and does not live under `cases/`. Its rows are
`kind: "bench"`, its grading is mechanical, and `run_eval.py` neither sees nor
scores it — see `bench/csp-fanin-v1/README.md` for the row fields and the
procedure.

## Negative cases are not optional

Cases `010-dont-extract` and `011-dont-generate` are cases whose correct answer
is **"don't"**.

Without them the corpus is gameable by a model that has read nothing: answer
"yes, centralize it" and "yes, generate it" to everything and score full marks
on a corpus made only of positive cases. That is not a hypothetical failure
mode — it is the specific one CS-3 and CS-4 are shaped around. CS-3 carries two
counterweights (*no catch-all `util`, `common`, or `core` packages*; *"nothing
qualifies" is a legitimate result*) and CS-4 is a **check-first** rule, not a
generate-everything rule. Those clauses are what a rule looks like after
someone has paid for the lesson, and a corpus that only rewards the headline
teaches the opposite of the rule.

Keep the balance deliberately: a new positive case about CS-3 or CS-4 should
arrive with the negative case that keeps it honest.

**And watch the corpus, not only the case.** Once `010` and `011` were both
in, "answer *don't*" became its own winning strategy — a model that refuses
everything scores 2 of 2 on judged cases without reading a rule. That is the
same failure as the always-yes model, arrived at from the other side, and it is
invisible from inside either case. `021-csp-judgment` is built against it: its
correct answer keeps one lock and converts another, so neither blanket verdict
passes. When you add a judged case, ask what a model that has read nothing but
the *previous* cases would answer.

`023-composability-judgment` is the second split case, and adding it surfaced
the next strategy along: with two splits in the corpus, **"say it depends and
name two things"** starts to score. So `023` puts the same *syntax* on both
halves — each is a method whose body is nothing but sibling calls — and makes
them differ only in substance, then grades the reasoning (R23-2) separately
from the verdict (R23-1). A response can now split correctly for the wrong
reason and be marked down for it. Each new judged case should assume the
previous ones have been read and the shallow pattern extracted.

`025-erasure-judgment` is the third, and it had to answer the strategy that two
splits create: **"give a split verdict and justify it by substance"** is now
itself the extractable pattern, and a model can produce a well-shaped split
without reading the rule. So `025` breaks the binary. Its two halves still look
identical to the gate, but on one of them **neither available verdict is
correct** — the sink registry cannot be "made generic" (Go has no existential
types, so the erasure is forced) and cannot be left alone either. The right
answer is a third thing: the erasure moves to one unexported adapter. A response
that has learned only the shape of `021` and `023` produces "generify the sink,
keep the envelope", which looks right and fails R25-1 and R25-2 both.

`027-factory-judgment` is the fourth, and by now the extractable pattern is
**"give a split verdict, and propose relocation as the third option"** — which
is, awkwardly, the *correct* answer to half of `027`. So `027` does not try to
make the shape wrong. It grades a **mechanism** instead: the relocation it asks
for is only behaviour-preserving because an unassigned interface variable holds
an *untyped* nil, and a model producing the right diff by pattern-matching says
nothing about that and writes a typed nil into the empty arm. R27-2 is the
criterion no previous case can be pattern-matched into, and the judge's note
names the fluent answer that passes every other criterion and fails it.

`029-pacing-judgment` is the fifth, and it takes `027`'s move one step further:
`027` grades the reasoning under one verdict, and `029` grades the reasoning
under **two verdicts that agree**. Three findings, one verdict pattern —
keep, convert, keep — and the two keeps rest on *different* reasons. One sleep
is the load generator's rate and is the subject of its test; the other is a
best-effort quiesce that must be kept because something else polls it and a
fatal await inside a poll aborts the retry doing the waiting. A model that has
learned "split by substance" produces the right three verdicts and justifies
both keeps with the first reason, which is true of one and false of the other.
The verdict pattern is therefore not the discriminator, deliberately.

`033-keep-qualified-judgment` is the sixth, and it turns on an axis no earlier
case uses. `029` graded the reasoning under two agreeing verdicts; a model that
has read it splits by substance and justifies each half. `033` gives it two
gomega imports — one in a test, one in `eventually.go` — and the correct verdict
keeps the production one qualified while converting the test one. The trap is
that the fluent defence of keeping it, *"the qualifier reads more clearly"*, is
a readability claim true of both files, so it cannot be the reason one converts
and the other does not. The real discriminator is **[scope](../../../../csf/docs/generated/ontology_cgen.md#term-scope)**: CS-11 is a
convention about test files, and one of the two is not a test. `R33-3` grades
that reason, and a well-shaped split justified by taste fails it — while the
blanket *"apply the convention everywhere"* dot-imports the production file and
fails `R33-2`.

The general form, now that there are six: **each judged case should be built
against the strategy the previous ones taught**, and the strategy gets more
sophisticated each time. Positive-only was beaten by a negative case; negative
cases were beaten by a split; splits were beaten by a shape-matched split; the
answer to that was a case where the split itself is not the answer; the answer
to *that* is a case where the shape is right and the reasoning under it is what
is graded; the answer to *that* is a case where two findings share a verdict and
not a reason; and the answer to *that* is a case where the reason that
distinguishes the two is an axis — [scope](../../../../csf/docs/generated/ontology_cgen.md#term-scope) — that no earlier case grades on. Ask
what a model that has read every previous case *and nothing else* would say, and
make sure it fails.

## When a rule's exemption cannot live in a fixture

CS-5 permits an honest leaf mutex. The CS-5 heuristic flags every mutex. Those
two facts collide in exactly one place — the clean fixture of
`020-csp-over-mutexes`, which should be the exemplar an [agent](../../../../csf/docs/generated/ontology_cgen.md#term-agent) copies and
therefore should show the permitted leaf mutex, except that a leaf mutex there
fires CS-5 and fails the runner's "nothing fires on clean" invariant.

Three ways out, and why the third one won:

1. **Narrow the heuristic** so the leaf pattern is not flagged. Rejected: no
   lexer can tell a counter's lock from a state machine's lock. Any pattern
   narrow enough to be safe would be a pattern authors write *to pass*, and the
   gate would then be measuring the marker rather than the design.
2. **Add an opt-out marker comment** the scanner honours. Rejected for the same
   reason `../references/exceptions.md` rejects raising the `dupl` threshold:
   an escape hatch becomes the answer to the finding instead of the thinking
   being the answer to it.
3. **Split the rule across the tiers.** Tier 1 tests the *shape* (state machine
   under a lock → channels and one owner), and its clean fixture contains no
   mutex at all. The *judgment* — which mutex is honest — is tier 2's, and
   `021-csp-judgment` requires keeping one.

The general form, and the reason this section exists rather than a note in one
`expected.json`: **a tier-1 fixture can only encode a rule's mechanical half.**
If the rule has an exemption a lexer cannot see, the fixture must not pretend
otherwise, and the rule must say out loud that its gate over-reports. Writing
the pair is what forces that admission, which is an argument for writing the
pair rather than for weakening the invariant.

**CS-6 hit the identical wall, which is what makes this a form rather than an
anecdote.** CS-6 permits a short fixed sequence with real ordering
dependencies; its heuristic flags any method whose body is three or more
argument-less sibling calls, which a permitted `parse(); resolve(); emit()`
chain is exactly. So `022-composable-checks`'s clean fixture contains no method
chain at all, and the exemption is judged by `023-composability-judgment`, whose
correct answer keeps one. This was verified rather than assumed: appending that
three-step chain to the clean fixture and re-running produced

```
run_eval: FAIL 022-composable-checks
  clean: CS-6 fired 1 time(s); it must not fire
```

**CS-7 made it three for three, and the prediction above is why it is worth
writing predictions down.** CS-7 permits an `any` at a genuinely unknown-shape
boundary; its heuristic flags every exported bare `any` in a non-internal
package, which a permitted JSON envelope field is exactly. So
`024-generics-at-the-boundary`'s clean fixture contains no exported `any` at
all, and the exemption is judged by `025-erasure-judgment`, whose correct answer
keeps one. Verified the same way — appending the permitted envelope to the clean
fixture produced

```
run_eval: FAIL 024-generics-at-the-boundary
  clean: CS-7 fired 1 time(s); it must not fire
    clean: .../clean/plugin.go.txt:213: CS-7: exported struct field
    Envelope.Payload is typed any
```

Three rules in a row have now had an exemption their gate cannot see, and the
pattern is not a coincidence: **a rule worth writing down is usually a rule
about judgment, and a gate can only see syntax.** Expect the next one to hit
this too. Reach for the tier split before reaching for a narrower heuristic or a
marker comment — both were re-examined for CS-6 and again for CS-7, and rejected
both times for the reasons they were rejected for CS-5.

**CS-8 is the exception, and writing the prediction down is what made that
visible.** Its exemptions are structural rather than judgmental — a method's
result is fixed by the interface it satisfies, a sealed sum type is closed by an
unexported method, a hook implementation's signature is fixed by the func type
it fits — and a lexer can see all three. So `026-return-concrete`'s clean
fixture does the thing the three fixtures before it could not: it **contains the
permitted shapes**, an `Authenticator` implementation and a sealed-sum
`ParseEvent`, and nothing fires on them. That was verified in both directions
the way the others were — deleting the `isEvent()` marker that seals `IEvent`
produces

```
run_eval: FAIL 026-return-concrete
  clean: CS-8 fired 1 time(s); it must not fire
    clean: .../clean/wiring.go.txt:103: CS-8: function ParseEvent returns the
    interface IEvent as result 1 of 2
```

so the seal is load-bearing rather than decorative.

The distinction that predicts which kind a rule is: **CS-5, CS-6 and CS-7 each
detect a question** — is this mutex a leaf, is this list a sequence, is this
`any` an envelope — **and CS-8 detects a violation.** A question's exemption
lives in the reader's head and can only be tested in tier 2; a violation's
exemption lives in the syntax and belongs in tier 1's clean fixture. Ask which
kind you have *before* deciding the clean fixture must omit the exemption,
because omitting it when it could have been shown costs the exemplar its most
useful half.

**CS-9 is back on the first side of that line, and further out than any of
them.** Its exemptions — a load generator's rate, a throttled link, a sampler,
an observation window, a best-effort quiesce — are byte-identical to what it
reports, so `028-await-not-sleep`'s clean fixture contains no sleep at all and
the judgment is `029`'s. What makes CS-9 the sharpest case of the form is the
proportion: CS-5's heuristic over-reports and a human dismisses *most* of it,
while CS-9's, measured on this corpus after the conversion, reports **nothing
but** correct code — ten findings, ten keeps. A tier-1 fixture that tried to
show even one of them would fail the runner's clean invariant, and a rule whose
gate is 100% locator is exactly the rule whose clean fixture must stay silent
and whose tier-2 case has to carry everything.

## Why the fixtures are not `.go` files

Tier-1 fixtures are held as `*.go.txt`. Two reasons, both mechanical:

1. **They would pollute the census.** The corpus for `check_style.py`,
   `style_census.py` and `tools/check-go-reuse.sh` is `git ls-files -- '*.go'`.
   Tracked `.go` fixtures that violate CS-1 and CS-2 *on purpose* would be
   counted as repo style debt, indistinguishable in the ledger from the real
   thing, and would move the target the P3 retrofit is measured against.
2. **They would manufacture a clone.** A dirty/clean pair is near-identical by
   construction. `tools/check-go-reuse.sh` is blocking and scans production
   source at a 100-token threshold, so the pair would trip a gate that is
   correct to trip on it.

Adding an exclusion to both corpus predicates was the alternative and was
rejected: those lists exist for third-party and deliberately self-contained
code, and each entry is one more place the style gate and the reuse gate can
drift apart (`../references/exceptions.md` § "Corpus exclusions inherited from
the reuse gate").

The cost of `.go.txt` is that fixtures are never compiled or `gofmt`-checked.
Accepted — they are read, not built, and they are written to be lexed by the
same code that lexes the repo.

## Adding a case

Numbering: `000` is provenance, `001`–`009` are tier 1, `010`–`019` are tier 2.
Those blocks belong to the four rules the skill launched with. **From `020`, a
rule added later takes the next adjacent pair**, tier 1 first: CS-5 has `020`
and `021`, CS-6 has `022` and `023`, CS-7 has `024` and `025`, CS-8 has `026`
and `027`, CS-9 has `028` and `029`, CS-10 has `030`/`031`, CS-11 has `032`
and `033`. CS-12 took `034`, and CS-13 took `035` the same day rather than
waiting for CS-12's judgment half to be written — so **the adjacent-pair
invariant is broken exactly here, and the record is that sentence**. CS-12's
tier-2 case and CS-13's take `036` and `037` when somebody writes them, and the
next new rule takes `038`/`039` — which CS-14 did on 2026-09-04, taking `038`
and leaving `036`/`037` vacant and reserved rather than filling the first free
number. A reservation that gets quietly reused is not a reservation. Both rules are report-only, so the mechanical
half is the half that had to exist first.

**`030` is deliberately vacant, and is not reused.** CS-10 ships no mechanical
check: `check_style.py` has no CS-10 rule, for the reasons in
`../references/go-rules.md` § CS-10's enforcement paragraph. A tier-1 case is a
fixture pair judged by that script's own finding functions, so a rule the script
cannot see cannot have one. The pair's tier-1 slot is left empty rather than
renumbered, because the gap *is* the record: a reader scanning the case
directories sees one rule with no mechanical half and can go find out why. CS-10
is judged by `031` alone, plus the per-directory audit in
`../references/exceptions.md` § "CS-10: the [service](../../../../csf/docs/generated/ontology_cgen.md#term-service) audit".

Adjacency is the part that matters, and it is deliberate: a tier-1 case is only
honest when read next to the tier-2 case that carries its exemption — that is
true of CS-5's pair, true again of CS-6's, and true a third time of CS-7's, for
the same structural reason every time.
When CS-5 landed this was written down as "a block of ten per rule"; CS-6
landed at `022`/`023` instead, because reserving eight numbers nothing will use
puts distance between cases that are meant to be read together. The pair is the
unit. A revised prompt becomes a lettered sibling (`000a-…`), never an edit.

**Tier 1.**

1. `mkdir -p cases/00N-slug/{dirty,clean}`.
2. Write one `*.go.txt` in each — the smallest thing that shows the shape.
   Synthetic, neutral names; documentation-range values only (RFC 5737
   `203.0.113.x` / `198.51.100.x`, `example.invalid` mailboxes). Fixtures teach
   shapes, and a fixture carrying a real host teaches that too.
3. Exercise **one** rule. The runner fails a case whose dirty fixture fires
   anything outside `dirty_must_fire`.
4. Write `expected.json`:

   | field | meaning |
   |---|---|
   | `case_id` | must equal the directory name (the runner checks) |
   | `tier` | `1` |
   | `rule_under_test` | the rule the case is about |
   | `summary` | one sentence: what a reader should learn from the pair |
   | `dirty_must_fire` | rule IDs that must fire on `dirty/`, and the complete set allowed to |
   | `dirty_min_findings` | optional per-rule minimum, default 1 |
   | `clean_must_not_fire` | rule IDs that must not fire on `clean/` — nothing at all may fire there, and listing them only sharpens the failure message |

5. Run `run_eval.py`. Then **break the clean fixture on purpose and run it
   again**: a case that cannot fail is not a case, it is decoration.
6. Append the rows.

**Tier 2.** Write `prompt.md` — verbatim under a provenance header if it came
from a real message — and `rubric.md` with numbered criteria, each stating what
fails it, plus a judge's note naming the *plausible* wrong answer (the fluent
response that cites the rule and violates it). Then judge it and append a row.

## Rows, and the append-only doctrine

Every row lands in `../metrics/ledger.jsonl`, one JSON object per line.

| kind | produced by | says |
|---|---|---|
| `program` | appended once at a program milestone | the goal this work is measured against |
| `census` | `python3 scripts/style_census.py` | the style debt at one commit |
| `derivability` | `python3 scripts/derivability_census.py` | how much of the tree is derived, and how many registered projections are still hand-maintained |
| `eval` | `python3 evals/run_eval.py` (tier 1); the judge (tier 2) | whether a case passed |
| `bench` | the grader of one `evals/bench/` run | what one A/B condition cost and whether it worked |

A `derivability` row is the one kind that is **not** about a rule: derivability
has no CS number and no gate, and nothing fails because of it
(`../metrics/README.md` § "Derivability"). It is in this table because it is a
ledger row and obeys every doctrine below, not because it is a seventh rule.

An `eval` row is `{kind, date, skill_sha, case_id, tier, pass}`. `skill_sha` is
the last commit touching the skill directory — the version being judged, not
whatever `HEAD` happens to be. Tier-2 rows carry the same fields plus `judge`
(who or what scored it) and, when it failed, `criteria_failed` and a `note`.

Three rules about rows, in force since `docs/widget_foundry.md` decision 4:

- **Never edit or delete a row to improve a trend.** A ledger that is tidied is
  a ledger that proves nothing, and the tidying always happens for a good
  reason at the time.
- **A regression gets a row and a written reason.** `pass: false` in the
  history is the point of the history. Suppressing the run is worse than the
  regression.
- **Never hand-type a row.** Run the tool and append its stdout. A hand-typed
  row is a claim; a tool-written row is a measurement, and only one of those
  survives being checked later.

A count that nothing checks is a count that drifts.

## Case 000

`cases/000-originating-prompt/prompt.md` is the operator message this entire
program came from, preserved byte-for-byte with its typos. It is immutable, and
it is the case that closes the loop the prompt itself asked for: the prompt that
created the tool is an input the tool is scored against.

**The immutability is checked, not requested.** `cases/000-originating-prompt/prompt.sha256`
records the sha256 of the verbatim prompt block — the content between the two
` ``` ` fences, and nothing else — and `run_eval.py` recomputes it on every run
and exits 1 when it does not match, naming this section. The block only,
because the two halves of that file have opposite doctrines: the block is the
message that was sent and may not move, while the prose around it (the
provenance table, "What actually happened") is commentary on an outcome that is
still unfolding and is meant to grow. A digest over the whole file would have
forced a choice between never recording what the case turned into and
re-stamping so often that a mismatch stopped meaning anything.

A mismatch is repaired with `git checkout`, never with a new digest. The one
situation that earns a new digest is a corrected transcript of the same
message, and that is a lettered sibling — a different directory, with a digest
of its own. The check emits **no ledger row**: a row is a verdict on a case,
case 000's verdict is tier 2's and awaits a judge, and "the file is unedited"
is the precondition of that judgement rather than the judgement.
