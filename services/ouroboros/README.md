<div align="center">

# Ouroboros

**Mine what your [agents](../../csf/docs/generated/ontology_cgen.md#term-agent) did for the next rule they need, and prove the rule before it ships.**

[What it is](#what-it-is) · [What you can do](#what-you-can-do-with-it) · [The loop](#the-loop) · [Quick start](#quick-start) · [Worked example](#worked-example-draft-pr-late) · [Write a miner](#writing-a-miner) · [Status](#status) · [Reference](#reference)

</div>

<!-- agent-drafted (#379): awaiting operator approval -->

## What it is

[CSF](../../README.md) runs AI coding [agents](../../csf/docs/generated/ontology_cgen.md#term-agent) under rules: every [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) works in a
harness session, and every shell command and commit it makes passes a
[gate](../../csf/docs/generated/ontology_cgen.md#term-session_gate). Gates are only as good as the mistakes someone thought of in
advance. [Ouroboros](../../csf/docs/generated/ontology_cgen.md#term-ouroboros) finds the ones nobody did.

> [Ouroboros](../../csf/docs/generated/ontology_cgen.md#term-ouroboros) uses evaluated results to propose the next version of an [agent](../../csf/docs/generated/ontology_cgen.md#term-agent)'s instructions or an allowed workflow; RRSI is its method.

That sentence is the ontology's definition, unchanged. This directory is its
mining half. A **[miner](../../csf/docs/generated/ontology_cgen.md#term-miner)** reads a corpus (harness event logs, tickets, pull
requests, files at a revision), turns it into facts, and derives verdicts with a
Datalog rule. A [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) is accepted only when a [walk-forward](../../docs/GLOSSARY.md#lit-walk_forward_analysis) [backtest](../../csf/docs/generated/ontology_cgen.md#term-backtest) finds every
labeled offense it should, and an accepted [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) becomes the next gate. The
gated runs then become the corpus for the next [miner](../../csf/docs/generated/ontology_cgen.md#term-miner): the snake eats its tail.

You write two files, `extract.ml` and `rules.dl`, on a contract that is already built.

## What you can do with it

- **Turn a complaint into a gate.** A mistake the operator flags becomes labeled instances; a [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) that finds every one of them, and nothing clean, closes the ticket.
- **Prove a rule before you enforce it.** Thresholds are fitted on the earlier half of the record and judged on the later half, so a [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) cannot pass by memorizing its examples.
- **See the evidence behind every finding.** Each verdict carries its proof: the facts it used and the source line of each.
- **Measure how early a rule would have fired.** The [backtest](../../csf/docs/generated/ontology_cgen.md#term-backtest) reports lead time: how long before the operator noticed, the [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) would have.
- **Close tickets only on proof.** `tools/close-ticket.sh` closes a ticket only when the merged pull request's [backtest](../../csf/docs/generated/ontology_cgen.md#term-backtest) shows no missed offense.

## The loop

Figure 1 shows one turn of the loop.

<a id="figure-1"></a>

<p align="center"><a href="../../docs/assets/tour/tour-ouroboros.svg"><img src="../../docs/assets/tour/tour-ouroboros.svg" width="1000" alt="The Ouroboros loop"></a></p>

**Figure 1.** The [Ouroboros](../../csf/docs/generated/ontology_cgen.md#term-ouroboros) loop. A [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) extracts facts from the corpus, a Datalog rule derives verdicts with proofs, and a [walk-forward](../../docs/GLOSSARY.md#lit-walk_forward_analysis) [backtest](../../csf/docs/generated/ontology_cgen.md#term-backtest) decides: a [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) with no missed offense becomes a typed finding that can close its ticket, and the next runs become the next corpus.

Each arrow is one typed artifact, and each has a check: `extract.ml` and `rules.dl` must pass `miner_test`, the [backtest](../../csf/docs/generated/ontology_cgen.md#term-backtest) block is generated (never typed), and `tools/close-ticket.sh` closes a ticket only when the merged pull request's `Backtest FN` row is 0 and every check row exits 0.
## Quick start

Copy the template and give it your [miner](../../csf/docs/generated/ontology_cgen.md#term-miner)'s name (in `extract.ml`, set `name`, `package` and `verdicts`):

```bash
cp -r services/ouroboros/miners/_template services/ouroboros/miners/my_miner
```

Test, build and run the template as it ships. This is real output from this branch, with Bazel's progress lines, the findings' `proof` arrays and the [backtest](../../csf/docs/generated/ontology_cgen.md#term-backtest)'s first rows trimmed (`…`):

```console
$ tools/bazel.sh test //services/ouroboros/miners/_template:miner_test --test_output=all
…
draft-pr-late: 4 tests passed
…
//services/ouroboros/miners/_template:miner_test                (cached) PASSED in 0.0s

$ tools/bazel.sh build //services/ouroboros/miners/_template:miner
INFO: Build completed successfully, 4 total actions

$ bazel-bin/services/ouroboros/miners/_template/miner.exe findings --knee 468 'services/ouroboros/miners/_template/fixtures/*/events.jsonl'
{"miner":"draft-pr-late","rule":"invisible","subject":[{"text":"6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e31"}],"severity":"SEVERITY_S3","proof":[…]}
{"miner":"draft-pr-late","rule":"invisible","subject":[{"text":"6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60"}],"severity":"SEVERITY_S3","proof":[…]}

$ bazel-bin/services/ouroboros/miners/_template/miner.exe backtest services/ouroboros/miners/_template/fixtures/labels.tsv 'services/ouroboros/miners/_template/fixtures/*/events.jsonl'
…
| Backtest $\kappa^\star$ | 468 (argmax precision on $\Lambda_{\le t}$ s.t. FN = 0; 3 candidates from `score`) |
| Backtest TP | 1: 6c1f2a10-3b7e-4d6a-9e51-0a1b2c3d4e60 |
| Backtest FP | 0 |
| Backtest FN | 0 |
```

The [miner](../../csf/docs/generated/ontology_cgen.md#term-miner)'s verbs are `facts`, `findings [--knee K]`, `backtest [--json] LABELS ITEM...` `mutate [--json] LABELS ITEM...` and `scope`. Swap the fixture glob for `'<state>/*/events.jsonl'`, where `<state>` is the harness state directory, to run over every recorded run.
## Worked example: DRAFT-PR-LATE

The template flags a harness run whose work stayed invisible to the operator, with no draft pull request, for longer than a knee measured from the runs themselves ([#102](https://github.com/candacelabs/csf_staging/issues/102)). Its fixtures are three real runs.

**Facts** (`miner.exe facts` on the fixtures; `score` is seconds from start to the first draft pull request, or to the last event when there is none):

| Run | `gated` | `opened` | `score` (s) | Source lines |
|---|---|---|---|---|
| `4e31` | yes | yes | 3089 | 2, 3 |
| `4e59` | yes | yes | 468 | 2, 3 |
| `4e60` | yes | no | 1259 | 2, 3 |

**Rule** (`rules.dl`):

```prolog
invisible(R) :- gated(R), score(R, S), knee(K), gt(S, K).
```

**Labels** (`fixtures/labels.tsv`), ordered by start and split at $t$ = 2026-10-02T18:04:45Z:

| Run | Label | Half |
|---|---|---|
| `4e31` | + | fit |
| `4e59` | − | fit |
| `4e60` | + | accept |

**Knee.** The candidates are the measured scores, $K = \{468, 1259, 3089\}$, and the fit half chooses among them:

$$\kappa^\star = \arg\max_{\kappa \in K} \mathrm{precision}(\kappa; \Lambda_{\le t}) \ \text{s.t.}\ \mathrm{FN}(\kappa; \Lambda_{\le t}) = 0$$

| $\kappa$ | Fires in fit half | Precision | FN |
|---|---|---|---|
| 468 | `4e31` | 1 | 0 |
| 1259 | `4e31` | 1 | 0 |
| 3089 | none | 0 | 1 |

Ties keep the smallest knee, which fires earliest, so $\kappa^\star = 468$.

**[Backtest](../../csf/docs/generated/ontology_cgen.md#term-backtest)** (`miners/_template/backtest.md`, generated, and checked current by `miner_test`):

| Measured | Result |
|---|---|
| TP | 1 (`4e60`) |
| FP | 0 |
| FN | 0 |
| Lead | 304 s |

Lead is $t_{\mathrm{flag}} - (t_{\mathrm{start}} + \kappa^\star) = 772 - 468 = 304$ s: the [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) would have fired five minutes before the operator flagged `4e60`. The finding carries its proof, `gated(4e60)`, `score(4e60, 1259)` and `knee(468)`, each with its source line.
## Writing a miner

1. Read the ticket and every comment: its named instances are your labels and its predicate is your rule.
2. Copy `miners/_template` to `miners/<name>`; in `extract.ml` set `name`, `package` and `verdicts`.
3. Write `extract`: one corpus item in, facts out, each with its source span. Emit a `score` fact if the rule needs a knee.
4. Write `rules.dl` with its `/* predicate */` block; thresholds read `knee(K)`.
5. Put each labeled instance in `fixtures/<instance>/events.jsonl` (only the lines a fact needs) and list it in `fixtures/labels.tsv`: instance, `+` or `-`, start, flagged time or `-`, source.
6. Run `tools/bazel.sh test //services/ouroboros/miners/<name>:miner_test` until it passes; paste the blocks it prints into `backtest.md` and `mutation.md`. A surviving mutant is printed with the rules or labels that survived: strengthen the fixtures or the rule until the test kills it.
7. Write the README from the template's, with the agent-drafted marker and the [backtest](../../csf/docs/generated/ontology_cgen.md#term-backtest) block.
8. Run `python3 tools/check_operator_identifiers.py`, commit with the `CSF-Session` and `CSF-Model` trailers, push, and open a draft pull request.
9. Run `bash tools/check-merge.sh`; on exit 0 mark the pull request ready. Never merge.
## Pitfalls

Each row is a mistake seen in tonight's proposals or in the template's own history.

| Pitfall | Counterexample (wrong) | Example (right) |
|---|---|---|
| nested term | `gt(L, knee(K))` | `knee(K), gt(L, K)` |
| written threshold | `gt(L, 2000)` | `knee(K), gt(L, K)` |
| unbound negation | `bad(X) :- ~seen(X).` | `bad(X) :- run(X), ~seen(X).` |
| relation no extractor emits | `latency(Q, L)` | `extract.ml` emits `latency` |
| label that is not an instance | `#222` | `6c1f2a10-…-4e60` |
| knee fit on the judged labels | fit and accept on $\Lambda$ | fit $\Lambda_{\le t}$, accept $\Lambda_{>t}$ |
| no positive after the split | `+` labels only before $t$ | a `+` in the later half |
| hand-set knee | $\kappa = 900$: lead −128 s | $\kappa^\star = 468$: lead 304 s |
| `>=` for "longer than" | `ge(S, K)`: $\kappa^\star = 1259$, lead −487 s | `gt(S, K)` |
| test blind to a written knee | `gt(S, 468)` passes every label check | the knee-binds check: a knee below every score and one above fire different runs |

The engine checks neither safety nor stratification, so `Contract.check_rules` does: an unbound negation raises `Invalid_rules "negation before its variables are bound"`, and every [miner](../../csf/docs/generated/ontology_cgen.md#term-miner)'s test runs it first.
## The service

The Go package in this directory is the loop itself, [mounted](../../csf/docs/generated/ontology_cgen.md#term-mount) into `csf serve` (#120): [miners](../../csf/docs/generated/ontology_cgen.md#term-miner) run all the time over the harness corpus, a [pre-check](../../csf/docs/generated/ontology_cgen.md#term-precheck) decides at no model cost which tickets a [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) can take, [fixers](../../csf/docs/generated/ontology_cgen.md#term-fixer) run through the harness under a daily budget, the [merge train](../../csf/docs/generated/ontology_cgen.md#term-merge_train) merges what passes the gate, and the [compounding rate](../../csf/docs/generated/ontology_cgen.md#term-compounding_rate) is measured and shown on the [ops view](../../csf/docs/generated/ontology_cgen.md#term-ops_view). It replaces the ad hoc scripts that launched [fixers](../../csf/docs/generated/ontology_cgen.md#term-fixer) outside the [session gates](../../csf/docs/generated/ontology_cgen.md#term-session_gate) and receipts.

```bash
csf serve -database-config database.json -ouroboros-repository /path/to/checkout            # detector, pre-check, measure, merge train
csf serve -database-config database.json -ouroboros-repository /path/to/checkout -ouroboros-fixers   # and fixer launch
csf ouroboros precheck -repository /path/to/checkout [-ticket N]...                             # the pre-check by hand, one row per ticket
curl http://127.0.0.1:14120/api/ouroboros                                                        # the latest numbers as JSON
```

The ledger is six `csf_ouroboros_*` tables of CSF's schema ([`ipc/db/csfpg`](../../ipc/db/csfpg/QUERIES.md#ouroboros-ledger)); every proposal, verdict, [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) session, merge result and series point is a row there. Fixers launch unattended only when sandboxed launch is available; until then `-ouroboros-fixers` is the switch, and without it the loop detects, [pre-checks](../../csf/docs/generated/ontology_cgen.md#term-precheck), measures and merges.

### Triggers

Four cron triggers in `America/Los_Angeles`, the zone #330's measurement uses; every cadence is derived from a measurement, and every occurrence skips while the previous one runs.

| Trigger | Cadence | Derived from |
|---|---|---|
| `ouroboros.detector` | every 15 min, and on every change to a run's event log after a 10 s settle | the template [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) reads every log (125 runs, 240 MB) in 1.3 s; an executor logs several lines a second, so the settle bounds reruns to one per burst and the sweep is the catch-up after a restart |
| `ouroboros.fixers` | every 10 min | the mean [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) session is 21.8 min, so a [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) is reconciled about twice before it ends |
| `ouroboros.merge_train` | every 30 min | one run of `tools/merge-pr.sh` builds the checkers and takes about 10 min; the train takes the ready pull requests oldest first |
| `ouroboros.measure` | hourly, and once at start | the compounding number is a per-day series; an hour keeps the day's point current |

### The pre-check

Before any model is paid for a ticket, its latest proposal (the comment the overnight loop posted on every mining ticket) must label at least one instance that exists in the corpus and is of a kind the proposed extractor reads: a run directory for `jsonl`, an existing issue or pull request other than the ticket itself for `issue` and [`pull_request`](../../csf/docs/generated/ontology_cgen.md#term-pull_request), a commit in the repository for `file_at`. A proposal that names none gets the verdict `needs_labels` and waits in the [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler)'s queue; one that does gets `launch`.

Replayed over the 107 tickets whose [fixers](../../csf/docs/generated/ontology_cgen.md#term-fixer) ran on 2026-10-03 (`csf ouroboros precheck -ticket ...`, 0 model calls):

| Outcome on 2026-10-03 | Tickets | Skipped by the [pre-check](../../csf/docs/generated/ontology_cgen.md#term-precheck) | [Fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) spend the skip would have avoided |
|---|---|---|---|
| synthetic labels | 85 | 84 | $60.78 of $61.90 |
| miner_test failed | 11 | 11 | $11.78 of $11.78 |
| fabricated fixtures | 5 | 5 | $3.48 of $3.48 |
| ready pull request | 3 | 2 | $1.08 of $2.21 |
| error | 3 | 3 | $0.00 of $0.00 |
| all | 107 | 105 | $77.12 of $79.37 |

The two that pass are #249 (its proposal names pull requests #226 and #245, and its [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) reached a ready pull request) and #122 (its proposal names commit `a1c14af`; its [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) still wrote synthetic labels, which no [pre-check](../../csf/docs/generated/ontology_cgen.md#term-precheck) can foresee). The one ready pull request the check would have skipped, #236, came from a proposal that named no instance: the [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) found one on its own.

### Fixers and the budget

A [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) is one harness session: the [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) on `claude-opus-5-5` (the only model the harness admits, by the operator's ruling of 2026-10-05), a worktree of its own on `miner/<name>-<assignment prefix>`, the [session gates](../../csf/docs/generated/ontology_cgen.md#term-session_gate) on, and a brief that names the instances the [pre-check](../../csf/docs/generated/ontology_cgen.md#term-precheck) found. The loop launches at most one per ticket, stops when the harness's admission reports no room, and stops for the day when the spend reaches the cap:

| Threshold | Value | Derived from |
|---|---|---|
| daily cap | $100 | operator ruling, 2026-10-04 |
| reservation per running [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) | $0.74 | the measured mean [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) session, $79.37 over 107 on 2026-10-03, until the ledger has 10 finished [fixers](../../csf/docs/generated/ontology_cgen.md#term-fixer) to measure its own mean from |
| budget day | calendar day in `America/Los_Angeles` | the measurement's day |

A [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer)'s cost is folded from its event log: the executor reports `total_cost_usd` cumulatively within one process and from zero when the process is reopened, so the cost is the sum of the maximum of each non-decreasing run. Once a [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer)'s one turn is done the loop closes the session and reads the outcome from its pull request: `ready`, `draft` or `no_pull_request`.

### The merge train

The train runs `tools/merge-pr.sh` over every ready, non-`LANG` pull request, oldest first, once per head; a refused head gets a comment with the refusal lines and is retried only after its author pushes a new one. NO-SELF-MERGE (#249): the merger is `csf-serve/<pid>`, never a session, and a pull request whose commits carry the merger's own identity in a `CSF-Session:` trailer is refused without running the path.

### The compounding number

The objective of #120 is the [struggle rate](../../csf/docs/generated/ontology_cgen.md#term-struggle_rate)'s [compounding rate](../../csf/docs/generated/ontology_cgen.md#term-compounding_rate), shown with its interval. Amendment 1 (operator rulings, 2026-10-04) fixes the primary series as #330's weekly one and adds an internal check. What the loop measures, exactly:

| Measured | Definition |
|---|---|
| struggle episode | the deterministic part of #330's definition (`rrsi-mine traces`): a hit is a tool result with `is_error`, an executor `permission_denied`, a [session gate](../../csf/docs/generated/ontology_cgen.md#term-session_gate) denial, or a tool call equal to one of the previous 4 (rrsi-mine's retry at exact equality in place of 0.85 similarity); hits at most 6 events apart are one episode; every episode counts, since no [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler) marks noise, canonical vocabulary or harness-fixability here, so this rate is an upper bound on #330's |
| tool calls | every `tool_use` block of every assistant record, subagents included: the per-session denominator #330 reads from the harness run record |
| [struggle rate](../../csf/docs/generated/ontology_cgen.md#term-struggle_rate), primary | episodes per 1,000 tool calls per week (weeks start Monday, days in `America/Los_Angeles`), with the 95% Garwood Poisson interval (Wilson-Hilferty), as `measure.py` computes it; the per-day rate is kept beside it |
| [compounding rate](../../csf/docs/generated/ontology_cgen.md#term-compounding_rate), primary | the weekly growth factor: the exponent of a weighted log-linear fit of ln(episodes/calls) on the week index over weeks with at least one episode and 200 tool calls (weights: episodes), with its 95% interval; `measure.py`'s trend; not estimable under three such weeks, and the same fit per day is the early read until then |
| baseline to beat | ×1.156 per week (×1.135 to ×1.177) at 49.9 per 1,000 in the week of 2026-09-28 (#330, 2026-10-02 comment); exponential improvement is the factor below 1 and staying there; the view shows the factor, its interval and this baseline |
| [structure per intervention](../../csf/docs/generated/ontology_cgen.md#term-structure_per_intervention), internal | merged enforced structure gained on main per week over the operator interventions of that week; structure is [session gates](../../csf/docs/generated/ontology_cgen.md#term-session_gate) (`Gate` constants of the [session gate](../../csf/docs/generated/ontology_cgen.md#term-session_gate)), accepted [miners](../../csf/docs/generated/ontology_cgen.md#term-miner) (directories with `rules.dl`) and ontology terms (`term` declarations), counted from git at each week's end (recorded intents live in csfpg and are not counted yet); interventions are the operator-authored messages into sessions, every prompt after a run's first turn, marked proxy until the AFFECT [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) labels corrections |
| compounding exponent, internal | the ordinary least-squares slope of log([structure per intervention](../../csf/docs/generated/ontology_cgen.md#term-structure_per_intervention)) on the week index with its 95% interval; not estimable under three weeks; first point: the week of 2026-09-28 |
| $ per ready pull request | [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) spend over [fixers](../../csf/docs/generated/ontology_cgen.md#term-fixer) whose pull request is ready |
| [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) yield | ready pull requests over finished [fixers](../../csf/docs/generated/ontology_cgen.md#term-fixer) |
| findings per day | findings over the days since the first |

Series: `ouroboros.struggle_rate_per_1k_tool_calls_weekly` (one point per week) and `ouroboros.struggle_rate_per_1k_tool_calls` (per day), `ouroboros.compounding_per_week` and `ouroboros.compounding_per_day`, `ouroboros.structure_per_intervention` (per week) and `ouroboros.structure_exponent`, `ouroboros.findings`, `ouroboros.fixer_spend_usd`, `ouroboros.cost_per_ready_pull_request_usd`, `ouroboros.fixer_yield`. The snapshot is written to `<state>/ouroboros.json`, which the [ops view](../opsview) follows into its panel, and served at `/api/ouroboros`.

### Generic and tenant gates

A [miner](../../csf/docs/generated/ontology_cgen.md#term-miner)'s [scope](../../csf/docs/generated/ontology_cgen.md#term-scope) is decided when it is accepted and carried on every finding (`scope` in `extract.ml`, [`Scope`](../../csf/docs/generated/ontology_cgen.md#term-scope) in `records.proto`, `miner.exe scope`): a [generic gate](../../csf/docs/generated/ontology_cgen.md#term-generic_gate) holds for any harness user (a draft pull request opened late, a sleep loop, a self-matching pgrep); a [tenant gate](../../csf/docs/generated/ontology_cgen.md#term-tenant_gate) encodes one operator's intents, rulings or vocabulary and never leaves the tenant. The ledger keeps the [scope](../../csf/docs/generated/ontology_cgen.md#term-scope) on every finding and the snapshot counts the accepted [miners](../../csf/docs/generated/ontology_cgen.md#term-miner) by [scope](../../csf/docs/generated/ontology_cgen.md#term-scope). Today's accepted [miners](../../csf/docs/generated/ontology_cgen.md#term-miner): 1 generic (`_template`, DRAFT-PR-LATE), 0 tenant. The tenant record, its opt-in field (default off) and the export of generic findings into a shared [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) set (after the operator-identifier gate, facts and spans only, instances re-keyed) are planned: CSF has no tenant record yet to carry the field.

### The labeler's queue

`csf_ouroboros_proposals` rows with the verdict `needs_labels` are the queue the [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler) (#318) reads; the record is `LabelRequest` in [`contract/records.proto`](contract/records.proto): ticket, proposal, [miner](../../csf/docs/generated/ontology_cgen.md#term-miner), corpus kinds, the instances that named nothing, the reason and when. The [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler) never edits the row: it posts a new proposal comment with real instances, and the loop [pre-checks](../../csf/docs/generated/ontology_cgen.md#term-precheck) that as a new proposal.
## Labeler

Labels are the bottleneck. On 2026-10-03, 85 of 107 [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) sessions ($61.90 of
$79.37) ended on a ticket that named no real instance, so each invented one.
The [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler) (`labeler/`, [#318](https://github.com/candacelabs/csf_staging/issues/318)) proposes real ones at no model
spend: a local model on the host's otherwise idle GPU, reached through the
[Ollama provider](../../csf/docs/generated/ontology_cgen.md#term-ollama) behind the [brain](../../csf/docs/generated/ontology_cgen.md#term-brain) contract.

```bash
csf label -repo OWNER/NAME -model-container ollama-1 -model-endpoint http://HOST:11434 \
  -checkout "$PWD" -ticket 105 -ticket 71            # or -tickets-file landed.tsv
```

One ticket is one call, in four steps:

1. **Parse.** The request is a `LabelRequest`: the loop's queue row (ticket, proposal, [miner](../../csf/docs/generated/ontology_cgen.md#term-miner), corpus, the instances the proposal named, the [pre-check](../../csf/docs/generated/ontology_cgen.md#term-precheck)'s reason) plus the [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler)'s own reading of the ticket (repository, predicate, facts). A row that carries only the ticket is completed here: the [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler) reads the ticket through `gh`, takes the predicate from its title and body, and the [miner](../../csf/docs/generated/ontology_cgen.md#term-miner), corpus kinds and fact lines from its latest proposal comment, keeping every value the row already carries.
2. **Prefilter, deterministically.** The fact lines' quoted literals, snake_case names and flags, and the predicate's backticked spans, are the search terms. Every line of the corpus (`<state>/*/events.jsonl` through the file capability; issues, pull requests and `git log` through the process capability) that carries one is a candidate, scored by inverse document frequency, so a term on every line counts for nothing and a rare one for much, and interleaved across instances so one verbose run cannot fill a batch. No threshold is picked.
3. **Label.** The model reads a batch of candidates, each with its instance, source line and an excerpt centred on the rarest matched term, and answers a schema-constrained JSON object: for each candidate `+` or `-`, a verbatim quote and a reason. The prompt asks for the concrete evidence the predicate describes; shared vocabulary is `-`.
4. **Accept, with no human in the loop.** A positive stands only when it reproduces: through the ticket's [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) when one is registered (`-miner name=executable`; its `facts` must name the instance), otherwise by its quote being verbatim on the line it names: every fragment of the quote, split at the excerpt's cut mark, on the line in order, read as written or with the JSON line's string escapes decoded. A quote lifted from the ticket's own words fails. Everything else is a `ProposedLabel` with `ACCEPTANCE_REJECTED` and why, or `ACCEPTANCE_UNCHECKED` for a negative.

Every label goes to `<state>/labeler.jsonl` and the run record `LabelerRun` to `<state>/labeler.json`, which the [ops view](../../csf/docs/generated/ontology_cgen.md#term-ops_view)'s panel shows live. Both records are typed in `contract/records.proto`, the shape the LOOP slice and the [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler) share.

**GPU sharing.** Before each batch the [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler) lists the running containers and yields while any other than the model server was created with the GPU (Docker's `--runtime nvidia`, or a device request for the nvidia driver or the gpu capability); it looks again every batch's worth of model time. Every request carries a keep-alive, so the model unloads soon after a run ends: twice the median gap between batch starts, floored at twice the measured load.

**Derivations.** Every threshold is measured, and the derivation stands beside it in `labeler.go`:

| Threshold | Value | Measured |
|---|---|---|
| context window | 16384 tokens | 8192 overran: 7674 prompt + 824 answer tokens in one batch |
| prompt tokens per candidate | running max, starts at 265 | 222 (event lines) to 265 (issue bodies) |
| answer tokens per label | running max, starts at 57 | 39 to 57 |
| batches per ticket | 2 | 34 to 37 s per batch of 44 to 51 candidates |
| yield wait | 35 s | one batch of model time |
| keep-alive | 2 × median gap, floor 2 × load | load 4.7 s cold, 1.4 s warm; gaps 37 s |

**Model choice, by measurement.** The host holds one local model, `qwen3:8b`
(the 27B GGUF the exited llama.cpp container names is not on disk), so the
choice was between its two configurations, measured on tickets #105, #71 and
#199 on 2026-10-04 with the same corpus and prompt:

| Configuration | Tickets with an instance | Positives reproduced | Precision | Wall time |
|---|---|---|---|---|
| thinking off | 3 of 3 | 27 of 31 | 0.87 | 207 s |
| thinking on | 2 of 3 | 9 of 10 | 0.90 | 249 s |

Thinking labels fewer lines positive, so it loses whole tickets for three
points of precision and a fifth more time; the [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler) runs with it off.

**The proof run.** On 2026-10-04 the [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler) ran over the 85 tickets whose
[fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) session had invented an instance (`landed.tsv`, gate column
"synthetic labels"), on this host's RTX 5070, at $0:

| Measured | Value |
|---|---|
| tickets labeled | 85 (2 ended in a model-server error: a 10-minute timeout after a 6225-token answer, and one partial answer) |
| tickets with at least one reproducible real instance | 48 |
| labels proposed | 3370 |
| positives proposed / reproduced / rejected | 417 / 376 / 41 |
| precision under the acceptance rule | 0.90 |
| labels per hour | 3620 |
| GPU utilization, mean of a sample after each batch | 90% (6.7 GB in use) |
| wall time | 56 min, 136 batches, 1 window overflow |
| accepted spans by corpus | 308 event-log lines, 48 commits, 13 issues, 7 pull requests |

Every 21st proposed positive, 20 in all, was read against its source line.
All 19 accepted quotes are verbatim on their lines and the one rejected quote
is not, so the rule judged 20 of 20 correctly. Only 4 of the 20 lines are an
instance of their predicate (a session refusing with the ticket's exact mkdir
error, a cold-cache usage record, a list of terms minted after the fact, a
brief stating an operator's intent and deadline); the other 15 reproduce a
line that carries the ticket's vocabulary: a brief, a tool result, a commit
footer. The rule guarantees that a positive is real text at a real span, not
that the span is an offense; that judgment waits for the ticket's extractor,
which is the rule's first clause. The same rule read strictly (no fragments,
no decoded escapes) over the first 11 tickets kept 54 of 88 positives
(0.61); the final rule kept 58 of 68 on the same tickets (0.85).

## Status

The contract, the template [miner](../../csf/docs/generated/ontology_cgen.md#term-miner), its gates and the loop [service](../../csf/docs/generated/ontology_cgen.md#term-service) are built; the PBO guard and dreaming are planned.

| Measured | Before this [service](../../csf/docs/generated/ontology_cgen.md#term-service) | Now | Command |
|---|---|---|---|
| [miners](../../csf/docs/generated/ontology_cgen.md#term-miner) that build and run | 0 | 1 | `ls services/ouroboros/miners` |
| [miners](../../csf/docs/generated/ontology_cgen.md#term-miner) with [walk-forward](../../docs/GLOSSARY.md#lit-walk_forward_analysis) FN = 0 | 0 | 1 | `tools/bazel.sh test //services/ouroboros/...` |
| contract and template tests | 0 | 2 pass | same |
| [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) proposals from tickets | 0 | 110 | [first mining run](#first-mining-run) |
| proposals that can reach TP > 0 on the template extractor | — | 0 | [census](https://github.com/candacelabs/csf_staging/pull/41#issuecomment-5964142217) |
| tickets of 2026-10-03 the [pre-check](../../csf/docs/generated/ontology_cgen.md#term-precheck) skips before a model call | 0 of 107 | 105 of 107 | `csf ouroboros precheck -repository . -ticket ...` |
| loop specs | 0 | 54 pass | `go test -race ./services/ouroboros/` |
| synthetic-label tickets with a reproducible real instance | 0 of 85 | 48 of 85 | [the proof run](#labeler) |
| model spend per labeled ticket | $0.73 ([fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer) sessions) | $0 | same |

### First mining run

On 2026-10-03 the orchestrator ran a first pass over the mining tickets. These are **proposals, not [miners](../../csf/docs/generated/ontology_cgen.md#term-miner)**: the orchestrator's [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) loop asked one stateless safe-mode Haiku call per ticket batch to write a predicate, facts, rules and labels for each open mining ticket, and posted each as a ticket comment. No [backtest](../../csf/docs/generated/ontology_cgen.md#term-backtest) has run on any of them.

| Measured | Value |
|---|---|
| tickets mined | 110 |
| model calls | 17 |
| largest batch | 11 tickets |
| artifacts written | 110 |
| ticket comments posted | 110 |
| refused by the identifier gate | 0 |
| cost | $1.4033 |
| cost per ticket | $0.0128 |
| call seconds per ticket | 18.4 |
| input tokens (uncached + cache creation) | 170 + 170,878 = 171,048 |
| cache read tokens | 0 |
| output tokens | 212,278 |
| load1 at call | 33.73–278.46 |
| backtested | 0 |

Source: the loop's `state/costs.tsv` (one row per call: family, tickets, artifacts, input, cache read, cache creation, output tokens, USD, wall seconds, load1), `state/posted.tsv` (one row per comment) and no `state/refused.tsv`. Reproduce the sums from the loop's directory:

```bash
python3 -c "R=[l.split('\t') for l in open('state/costs.tsv') if l.strip()]; s=lambda i: sum(float(r[i]) for r in R); print(len(R), s(1), max(int(r[1]) for r in R), s(3), s(4), s(5), s(6), round(s(7), 4), round(s(8) / s(1), 1))"
```

Call seconds per ticket sum each call's wall time; calls ran four at a time, so elapsed time was shorter. Every call wrote its whole prompt into the cache and none read it back. The [census on #41](https://github.com/candacelabs/csf_staging/pull/41#issuecomment-5964142217) measures these proposals against the contract: 0 of 110 can reach TP > 0 with the template's extractor, because each needs its own.

## Reference

### The contract

Every record is typed in `contract/contract.mli`, with its wire form in `contract/records.proto`; `Corpus` reads event logs and transcripts in place, `gh` tickets and pull requests, and files at a revision.

| Record | Fields |
|---|---|
| Fact | `relation`, `args`, `span` |
| Finding | `miner`, `rule`, `subject`, `severity`, `proof` |
| [Backtest](../../csf/docs/generated/ontology_cgen.md#term-backtest) | `labels`, `split`, `knee`, `families`, `tp`, `fp`, `fn`, `lead` |
| Mutation | `miner`, `at`, `killed`, `survived`, `excluded`, `score`, `floor`, `accepted`, `surviving`, `seconds` |

An argument is `Text` or `Number`, and a span is a file and a line. Severity runs S0 to S3 ([#105](https://github.com/candacelabs/csf_staging/issues/105)): S0 is a gate escape, S3 an offense found only by mining. `families` counts the $\kappa$ candidates tried, so multiple testing is visible on every [backtest](../../csf/docs/generated/ontology_cgen.md#term-backtest).

### The gates this service owns

| Gate | Passes when |
|---|---|
| `miner_test` | rules safe, labels fire as labeled, FN = 0, TP > 0, the knee binds, `backtest.md` current, every mutant killed, `mutation.md` current |
| `tools/close-ticket.sh <ticket> <pr>` | `Backtest FN` row is 0 and every check row exits 0 |

`tools/close-ticket.sh --dry-run 211 23` exits 1 with `the pull request body has no | Backtest FN | row`; without `--dry-run` it comments that row and leaves the ticket open. Walk-forward acceptance follows [Bergmeir and Benítez 2012](https://doi.org/10.1016/j.ins.2011.12.028); the acceptance first written for #390 chose the knee on the record it then judged, and [#86](https://github.com/candacelabs/csf_staging/issues/86) replaced it.

### Mutation score

A [miner](../../csf/docs/generated/ontology_cgen.md#term-miner)'s test is itself tested ([#317](https://github.com/candacelabs/csf_staging/issues/317)): `Mutate` (`contract/mutate.ml`) changes the rules or the labels one way at a time, reruns the [miner](../../csf/docs/generated/ontology_cgen.md#term-miner)'s checks on each mutant, and counts.

| Mutant kind | Change |
|---|---|
| drop atom | one body literal removed |
| swap comparison | `gt` for `ge`, `lt` for `le`, and back |
| write knee | `knee(K)` removed, a fixture score written for `K` |
| negate atom | a positive atom negated |
| drop rule | one clause removed |
| swap head | two verdict relations exchange their rules |
| flip label | one label's sign flipped |
| move split | the two labels either side of the split exchange starts |

A mutant the checks fail is killed; one they pass has survived; the score is killed over killed plus survived. Two kinds leave the denominator, each with its reason in `mutation.md`: a mutant `check_rules` refuses (an unbound variable after a drop or a negation), and an equivalent mutant, one that derives the same verdicts as the original under no knee and under every knee candidate.

The gate is `Mutate.rejections`: the score must reach the floor, and no surviving mutant may change a labeled instance's verdict; the survivor is printed with its rules or labels. The floor is derived, not picked: it is the template [miner](../../csf/docs/generated/ontology_cgen.md#term-miner)'s measured score, recorded as `Mutate.template` and checked by the template's own test, so it moves only when the template's measurement does.

| Measured on the template | Value |
|---|---|
| mutants | 16 |
| excluded | 6: 1 equivalent (`gated(R)` dropped: every fixture run is gated), 5 refused |
| killed | 10 of 10 |
| floor | 10/10 = 1.00 |
| wall time | 4 ms (`miner.exe mutate --json`, `seconds`) |

Mutation analysis found one blind spot in the template's test as it first shipped: a knee written into the rules, `gt(S, 468)`, passed every check, because the fitted knee was 468 too. The knee-binds check closes it; `mutation.md` records that it is the only check that kills that mutant. Mutants change rule text and labels only, so they run inside the test's own Bazel sandbox; the extractor runs no further.

### The $h_{\mathrm{fleet}}$ floor

$h_{\mathrm{fleet}}$ is the fleet's prompt-cache hit ratio ([#235](https://github.com/candacelabs/csf_staging/issues/235)) over every harness `result` event's usage:

$$h_{\mathrm{fleet}} = \frac{\sum \mathrm{cache\_read}}{\sum (\mathrm{input} + \mathrm{cache\_creation} + \mathrm{cache\_read})}$$

The 0.9867 floor is a stated number, so it carries its reality check ([#241](https://github.com/candacelabs/csf_staging/issues/241)):

| Stated | Measured | Runs | Runs below | Used by |
|---|---|---|---|---|
| 0.9867 | 0.9870 | 73 | 40 | none |

Measured on 2026-10-03 with:

```bash
jq -n '[inputs | select(.event_type == "result") | .event.usage // {}
  | {r: (.cache_read_input_tokens // 0),
     t: ((.input_tokens // 0) + (.cache_creation_input_tokens // 0) + (.cache_read_input_tokens // 0))}]
  | (map(.r) | add) / (map(.t) | add)' <state>/*/events.jsonl
```

The fleet clears the floor (0.9870 ≥ 0.9867), yet 40 of the 73 runs fall below it on their own ratio $h_r$, so a fleet-wide pass hides a majority of offending runs; #235 ranks those offenders for a [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) to drive down. Tonight's 17 proposal calls read 0 cached tokens, so their ratio is 0.

### PBO guard (planned)

Walk-forward answers in-sample selection ([Bailey, Borwein, López de Prado and Zhu 2014](https://doi.org/10.1090/noti1105)) but not multiple testing. The probability of [backtest](../../csf/docs/generated/ontology_cgen.md#term-backtest) overfitting ([Bailey et al. 2016](https://doi.org/10.21314/JCF.2016.322)) splits the record into $S$ blocks; each of the $\binom{S}{S/2}$ halvings $c$ picks the best candidate in sample, with relative rank $\omega_c$ out of sample:

$$\mathrm{PBO} = \Pr_c\left[\lambda_c \le 0\right], \qquad \lambda_c = \ln\frac{\omega_c}{1 - \omega_c}$$

| Record | Labels | After $t$ | `families` | PBO |
|---|---|---|---|---|
| template fixtures | 3 | 1 | 3 | undefined |
| every recorded run | 3 | 1 | 60 | undefined |

With $S = 2$ each half holds one or two labels, too few to rank candidates; the guard waits for a label set with a positive in each of $S \ge 4$ blocks.

### Dreaming (planned)

Dreaming is the loop pointed at the language and the queue rather than at one gate ([#140](https://github.com/candacelabs/csf_staging/issues/140)): a dreamer classifies operator intents and proposes terms, gates and ranked work, and an actor executes turns and never edits the language. Its objective is minimum description length ([Rissanen 1978](https://doi.org/10.1016/0005-1098(78)90005-5)), choosing the language $L$ that minimizes $|L| + \sum_{i} |\mathrm{encode}(i \mid L)|$. It runs at two speeds, after complementary learning systems ([McClelland, McNaughton and O'Reilly 1995](https://doi.org/10.1037/0033-295X.102.3.419)): the nightly dream ratifies, and online dreaming ([#141](https://github.com/candacelabs/csf_staging/issues/141)) mints provisional [session gates](../../csf/docs/generated/ontology_cgen.md#term-session_gate) that expire unless ratified.

- **Example (build it, #141):** swap the gates at a tool-call boundary; the [hook](../../csf/docs/generated/ontology_cgen.md#term-hook) binary reads its rules on every call, so a rule published between two calls binds from the next one.
- **Counterexample (dropped, #141):** swap code into the Go process during a GC pause; Go has no JIT, and a `plugin` can never be unloaded.

Neither speed has code here yet; a [miner](../../csf/docs/generated/ontology_cgen.md#term-miner)'s findings are the dream's input once it lands.
