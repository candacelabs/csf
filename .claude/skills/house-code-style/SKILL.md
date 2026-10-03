---
name: house-code-style
description: >-
  The operator's Go and UI code-style rules for this monorepo, as fourteen
  numbered invariants (CS-1 I-prefixed interfaces, CS-2 named parameters
  everywhere, CS-3 centralize primitives, CS-4 generate before you write, CS-5
  CSP over mutexes, CS-6 composable function values over method-set
  accumulation, CS-7 generics at the boundary with erasure in one place, CS-8
  return concrete implementations and only accept interfaces, CS-9 tests never
  hand-roll time, CS-10 a service is a conceptual library that mounts into some
  binary through functional options and never requires a container, CS-11 tests
  dot-import ginkgo and gomega, CS-12 a constructor names what it builds, CS-13
  no magic strings, CS-14 no hand-rolled twin of a library primitive) plus a
  unified OCaml checker with mandatory and advisory rules and historical census
  tooling. Load
  this before writing, reviewing, or generating any Go or UI code in this repo,
  and before briefing a subagent that will — pasting
  `references/brief-snippet.md` verbatim into that brief is a hard precondition
  of launching it, because a subagent reads its brief and nothing else. Load it
  whenever someone asks what to name an interface, whether a signature needs
  named parameters, where a shared helper belongs, whether something should be
  generated rather than hand-written, whether a piece of concurrency wants a
  mutex or a channel, whether a family of checks or stages should be a registry
  of function values rather than more methods, whether a public signature
  should take a type parameter instead of `any`, whether a constructor or a
  config-selected factory should hand back an interface or a concrete type,
  how a test should wait for something to become true, whether a new service
  needs a Dockerfile or a compose file to be a service at all, where a
  service's composition seam belongs, whether a string literal should be a named
  constant, whether a helper you are about to write already exists in a library
  the tree imports, or
  why CI is complaining about style or duplication. If
  you are about to write Go and are unsure whether this applies, it applies:
  the cost of loading it is one file read, and the cost of missing it is a
  rename that has to cross the public export boundary later.
---

# House code style

The operator's fourteen numbered Go/UI rules are checked by one
[OCaml house-lint runner](../../../tools/house_lint/README.md). Its
[rule registry](../../../tools/house_lint/policy.ml) owns severity and coverage;
`.github/workflows/house-lint.yml` runs the complete checker in one job on
every PR and main push. Native AST checks and the existing specialized tools
share one verdict and report. Go and Python source use pinned Tree-sitter
grammars in the OCaml checker; no Python interpreter implements these scans.

This skill's dated measurements, ledger and originating prompts remain evidence
of the earlier implementation. Legacy Python census and eval tools retain those
measurement definitions; their counts do not establish the new AST coverage.
Do not rewrite historical rows or describe them as a current native scan.

## 1. Before you write code

Read `references/go-rules.md`, including its current enforcement notice and
counterweights. The rules govern new code even where mechanical checks are
advisory or deliberately narrower than the architectural principle.

| Rule | Required design | Current enforcement |
|---|---|---|
| CS-1 | Interface names carry an `I` prefix; private names preserve visibility with `i`. | Mandatory native AST check. |
| CS-2 | Name every input parameter in every signature; result names are optional. | Mandatory native AST check. |
| CS-3 | Centralize reusable primitives at the lowest layer that owns their semantics. | Mandatory pinned `dupl` specialist. |
| CS-4 | Check for a generator before hand-writing its output. | Advisory native SQL-literal, `Mock*` struct and unmarked generated-filename candidates; actual generator drift stays with its component. |
| CS-5 | Prefer CSP for coordinated state; retain honest leaf critical sections. | Advisory native mutex-declaration locator. |
| CS-6 | Represent extensible families as named function values in a registry. | Advisory native hardcoded receiver-dispatch locator. |
| CS-7 | Keep public contracts typed; forced erasure belongs once inside the owning library. | Advisory native erased-contract locator. |
| CS-8 | Return concrete owned implementations, accept interfaces at consuming seams. Apply the data-shaped test before exemptions. | Mandatory native narrow check; advisory typed specialist reports the broader interface-return set. |
| CS-9 | Use `pkg/eventually` for test waits. | Advisory native sleep-loop locator; pacing and observation need review. |
| CS-10 | A service is a composable library; its binary owns configuration, process lifetime, engine/listener, `cmd/` and Dockerfile. | Advisory native process-ownership calls and tracked service `cmd/`/Dockerfile candidates. Compose files remain allowed. |
| CS-11 | Test files dot-import Ginkgo/Gomega unless a package-name collision forces qualification. | Mandatory native repo-wide import check. |
| CS-12 | Name constructors for the concrete thing, including private constructors. | Advisory native `New`, `new`, `NewStore`, `NewClient`, `NewService` and private variants; marked generated code exempt. |
| CS-13 | Declare and name semantic string values; prefer generated enums. | Advisory native locator with prose, tag, import, separator and test counterweights. |
| CS-14 | Use the owning optional-value library primitive rather than a handwritten conversion twin. | Advisory native locator. |
| CS-15 | Whoever starts a goroutine owns its cleanup: a context-driven exit and a join; a runtime `Scope` is the easy way. | Advisory native locator for `go` statements with no visible join or context-driven exit, tests included. |
| CS-16 | Boundary crossings only under `ipc`, granted to constructors as capabilities. | Advisory native locator for network listen/dial, package-level `net/http` serving and gRPC client creation outside `ipc`, and `os/exec`/PTY imports and fork/exec calls outside `ipc/proc`, tests included; the file part is pending. |
| CS-16-DB | PostgreSQL pools and connections only under `ipc/db/csfpg`; tests use gomock doubles of `csfpg.IDB` or pgmem. | **Mandatory** native locator for `pgxpool.New`/`NewWithConfig`/`pgx.Connect`/`ConnectConfig` outside `ipc/db/csfpg`, tests included. |
| CS-17 | The process environment is read only by the config capability (`runtime/config`) or a binary's own `go/app/<name>/config`; values reach constructors as values. | Advisory native `os.Getenv`/`os.LookupEnv`/`os.Environ` locator, tests included; the monorepo's Compose environment check checks Compose against what binaries read. |
| CS-18 | Unit tests in-package; integration tests are external `_test` specs on the exported API with gomock dependencies, covering misuse with defined errors; tests make no real crossings (gomock or pgmem; a labelled `//go:build acceptance` suite is the one opt-in exception); every exported interface has a committed mockgen mock; gates do not exempt tests. | Advisory native `CS-18-MOCKGEN`, `CS-18-CROSSING` and `CS-18-EXTERNAL` locators; each part flips to mandatory only at zero. Legacy lane for the crossing part. |
| ONTOLOGY-DIRS | Directories are named by CSF ontology terms; `internal` is transparent. | Advisory native locator reading the terms from `architecture.csf`. |

The same registry includes mandatory `TEST-BOOTSTRAP` for the scoped Ginkgo
suite-bootstrap convention, mandatory `DEPENDENCIES` for forbidden Gorilla
imports and dependency records, advisory `INTERFACE-RETURNS` for Go type
analysis, and advisory `FUNCTION-LENGTH` for functions exceeding 60 non-comment
lines. Every rule appears in the report, including skipped specialists under
`--native-only`.

`GOROUTINE-SHARED-STATE` is an advisory locator for captured writes and later
caller accesses, with bounded mutex/WaitGroup recognition. `PY-MAGIC-STRING`
adds advisory Python semantic-string checks. OCaml is excluded from magic-string
linting. Both rules' exact coverage and limitations live in the checker README;
a clean shared-state scan is not a proof of concurrency safety.

Mandatory finding IDs are CS-1, CS-2, CS-3, CS-8, CS-11, TEST-BOOTSTRAP and
DEPENDENCIES. All other findings are advisory. Failed or incomplete scans are
errors regardless of rule severity. Advisory findings require an answer in
review, not arbitrary source suppression or a narrowed detector.

Keep the counterweights with the rule:

- Shared primitives need stable semantics and real callers; do not create
  catch-all `util`, `common` or `core` packages.
- A mutex can be an honest leaf lock, a call sequence can have real ordering,
  a heterogeneous collection can require erasure, and a test sleep can generate
  load. Answer the specific finding before changing the design.
- CS-7 does not forbid unavoidable erasure. Put it behind one typed owner;
  `pkg/eventually` is the existing typed boundary around Gomega waiting.
- CS-4 locates derivable-looking syntax, not every possible generator. Read the
  owning contract and run its generator's drift check.
- CS-10 locates process-ownership candidates, not proof of a service's complete
  composition contract. The 2026-09-04 amendment puts `cmd/` and Dockerfile with
  the binary; existing service copies are advisory retrofit candidates. A
  Compose file may describe one deployment beside the library. Do not delete
  it to clear a conceptual-service rule. Container-first deployment and
  library composition govern different responsibilities.

The general native style corpus is tracked handwritten first-party Go,
excluding vendor, frozen research, designated self-contained gotth samples and
standard marked generated files. Dependency and CS-11 lanes deliberately have
broader scopes; TEST-BOOTSTRAP remains scoped to `pkg` and
`tools`, excluding `pkg/gotth`. See the checker README for exact scope.
Do not widen or narrow an exclusion merely to clear a finding.

## 2. Before you brief a subagent

Paste `references/brief-snippet.md` **verbatim** into every subagent brief whose
stage may write, review or generate Go or UI code. Do not substitute a summary
or a pointer to this file. This is a precondition of launching that stage.
Verify its diff yourself; a brief is not enforcement.

## 3. Before you hand work back

Run the complete native regression suite and repository check:

```bash
bash tools/check-house-lint.sh --test --summary house-lint-report.md
```

`bash tools/check-house-lint.sh` runs the same complete rule set without the native test
suite. `bash tools/check-house-lint.sh --report-only` makes findings report-only for that invocation;
errors still fail. `--quiet` prints counts instead of individual findings.
`--native-only` skips reuse, typed interface returns and function length; it
must be described as a partial check rather than a CI pass.

Exit 0 means the requested scans completed with no blocking findings; advisory
findings can remain. Exit 1 means mandatory findings. Exit 2 means a scan or
specialist tool failed. Read the per-rule report and the findings in edited
files. Report unresolved candidates with the reason they remain.

The OCaml runner owns native AST analysis and invokes pinned `dupl`, the Go
interface-return analyzer and pinned golangci-lint for their specialized
algorithms. Their execution failures remain errors. CI retains the report and
`house-lint-output/` logs even when findings fail the job. Those files are local
artifacts, not tracked source.

For package quality beside the house checker, run `gofmt -l` and `go vet` over
the touched packages in the pinned Go image `.github/workflows/ci.yml` uses.
Tests, generator drift and deployed acceptance remain separate obligations.

## 4. Before you change this skill or a checker

Native scanner changes require native regressions through `--test`, including
both positive and negative syntax fixtures and the error/empty-corpus contract.
The specs over the gate workflows live in `tools/gates`. Preserve rule
severities and individual corpus boundaries unless the operator requested a
change.

Historical measurement commands remain available:

```bash
python3 .claude/skills/house-code-style/scripts/style_census.py
python3 .claude/skills/house-code-style/scripts/derivability_census.py
python3 .claude/skills/house-code-style/evals/run_eval.py
```

These retain the legacy Python detector and its fixture semantics. They neither
measure new CS-4/CS-10/expanded CS-12 coverage nor replace native regressions.
Tier-2 judgment rubrics still test decisions the syntax scanner cannot make.
Read `evals/README.md` and `metrics/README.md` before recording measurements.

Existing `metrics/ledger.jsonl` rows are append-only: never edit or delete a
row to improve a trend, never hand-type a row, and retain the source revision
and detector identity for a measured result. The originating prompt at
`evals/cases/000-originating-prompt/` remains immutable. Run tools to produce
any new evidence and keep legacy and native measurements distinguishable.
Derivability remains a metric, not a lint threshold; actual generated-output
reproducibility stays with each owning generator.
