# House lint

One OCaml runner owns mandatory and advisory house rules. CI prepares and tests
that checker, runs its complete lane inventory in separate bounded jobs, and
requires all of them in one aggregate check.
[`policy.ml`](policy.ml) owns rule IDs, descriptions and severities;
[`house-lint.yml`](../../.github/workflows/house-lint.yml) invokes the
runner on every pull request and main push, with no path filter, and
[`tools/check-house-lint.sh`](../check-house-lint.sh) is the same procedure
from a checkout. Go and Python source are parsed by
pinned Tree-sitter grammars in the native OCaml checker; no Python interpreter
is needed for these scans.

## Run

From the repository root:

```bash
bash tools/check-house-lint.sh --test --summary house-lint-report.md
bash tools/check-house-lint.sh                # same complete scan, without native regressions
bash tools/check-house-lint.sh --report-only  # report all findings; scanner errors still fail
bash tools/check-house-lint.sh --native-only --quiet
```

Use the launcher from this checkout. It requires Bash, Git and Docker and builds
OCaml through the pinned Bazel toolchain; no host OCaml or Go installation is
required. The resulting native executable runs on a compatible Linux host.
Initial builds and specialist tool installation may download pinned dependencies.

| Option | Effect |
|---|---|
| `--test` | Run native house-lint and dependency-checker regressions before scanning. |
| `--build-only` | Prepare the native checker, including regressions when paired with `--test`; this does not scan the repository. |
| `--root PATH` | Scan another Git checkout with this checkout's checker. |
| `--summary PATH` | Write the Markdown rule report; a relative path is relative to the scanned checkout. |
| `--native-only` | Run native AST and dependency checks; explicitly skip reuse, typed interface returns and function length. This is a partial check. |
| `--report-only` | Make findings non-blocking for this invocation; scanner and tool errors still fail. |
| `--quiet` | Suppress individual findings and ordinary specialist output; retain counts and errors. |

Exit 0 means the requested checks completed without a mandatory finding; advisory
findings may remain. Exit 1 means mandatory findings. Exit 2 means an incomplete
or failed scan. `--report-only` changes finding severity for the invocation, never
error handling. The runner collects findings and specialist results before
returning its final status, so one violation does not hide other rule reports.
Build or native-regression failures stop before the repository scan.

The Markdown summary lists every rule's policy, native count or specialist
status, and [scope](../../csf/docs/generated/ontology_cgen.md#term-scope). CI always attempts to publish it to the step summary and
retains `house-lint-report.md` plus `house-lint-output/` as an artifact, including
failed scans. Specialist logs live under `house-lint-output/` in the scanned
checkout. Missing reports after build/setup failure are identified as missing.
Neither reports nor logs are source files to commit.

The native executable exposes `--ci-lanes` as the complete JSON inventory and
`--lane NAME` for one partial scan. The workflow derives its matrix from that
inventory instead of maintaining a second list of scanner commands or policies.
Each consumer verifies the source revision and checker checksum before running.
Partial reports explicitly mark unselected rules as not run. Preparation,
native AST checks, reuse, typed interface returns and both function-length scans
must all succeed for the aggregate gate; advisory findings remain visible and
scanner errors still fail. Ordinary CLI invocations continue to run every lane.

## Rule coverage

OCaml traverses the pinned Tree-sitter Go AST for the native rules. A locator
identifies code for review; a clean scan does not prove the corresponding design
property. The registry, rather than workflow flags, decides which findings block.

| Rule | Policy | Mechanical coverage |
|---|---|---|
| CS-1 | Mandatory | Interface declarations with the required public/private `I`/`i` prefix. |
| CS-2 | Mandatory | Named input parameters in function declarations, literals, function types and interface methods. Return names remain optional. |
| CS-3 | Mandatory specialist | Pinned `dupl`: 100-token production and 200-token all-handwritten thresholds, with the existing reuse corpus. |
| CS-4 | Advisory | In non-test source: SQL-shaped string literals, handwritten `Mock*` struct declarations, and `.pb.go`, `.gen.go` or `_generated.go` files lacking a recognized generated header. |
| CS-5 | Advisory | Mutex declarations, including legitimate leaf critical sections. |
| CS-6 | Advisory | Hardcoded dispatch lists of argument-less calls on the same receiver. |
| CS-7 | Advisory | Erased public contracts using `any`/empty interfaces and locally resolvable aliases, including a generic type instantiated with `any` in an exported field, interface method or function signature. |
| CS-8 | Mandatory | Owned interface results within the narrow house-rule [scope](../../csf/docs/generated/ontology_cgen.md#term-scope) and its structural exemptions. |
| CS-9 | Advisory | Sleep loops in tests; intent such as polling versus pacing needs review. |
| CS-10 | Advisory | In non-test [service](../../csf/docs/generated/ontology_cgen.md#term-service) source, process-ownership candidates: `main`, configuration/environment, signal, listener and Gin-engine calls, plus [service](../../csf/docs/generated/ontology_cgen.md#term-service) `cmd/` and Dockerfile artifacts. Compose files are allowed. |
| CS-11 | Mandatory | Repo-wide handwritten Ginkgo/Gomega test dot imports, preserving structural package-name collision exemptions. |
| CS-12 | Advisory | Generic constructor names `New`, `new`, `NewStore`, `NewClient`, `NewService` and private variants; marked generated code is exempt. |
| CS-13 | Advisory | Go inline semantic string values, with the documented prose, import, tag, separator and test counterweights. |
| PY-MAGIC-STRING | Advisory | Python identifier-shaped string values in functions, methods and lambdas, with named-constant, docstring and human-message exemptions. OCaml is outside the magic-string corpus. |
| CS-14 | Advisory | Optional-value conversion helpers duplicating library primitives, and first-error latches (`make(chan error, 1)` filled by a non-blocking `select` send) duplicating `context.WithCancelCause`. |
| HANDLER-DB-IO | Mandatory | Database handles and direct database/store calls stay behind [service](../../csf/docs/generated/ontology_cgen.md#term-service) operations. |
| ELSE-AFTER-RETURN | Mandatory | An `else` or `else if` cannot follow a branch that returns on every path. |
| TEST-BOOTSTRAP | Mandatory | Handwritten Ginkgo suite bootstrap convention in `pkg/` and `tools/`, excluding `pkg/gotth`. |
| DEPENDENCIES | Mandatory | Existing OCaml Gorilla import/module/workspace/checksum policy. |
| INTERFACE-RETURNS | Advisory specialist | Existing Go type analyzer across tracked first-party modules; `error` exempt, other interface results and returned interface-bearing structs reported. |
| FUNCTION-LENGTH | Advisory specialist | Pinned golangci-lint `funlen` over the module: over 60 non-comment lines, no statement threshold. |
| CS-15 | Advisory | `go` statements in selected Go, tests included, whose enclosing function or literal shows no owner: no `.Wait()` call, no receive, `range` or waiter call (`Eventually(done)`) on a channel the [goroutine](../../csf/docs/generated/ontology_cgen.md#term-goroutine) sends on or closes, and no context-driven exit (`.Done()`/`.Err()` or a context value) in the [goroutine](../../csf/docs/generated/ontology_cgen.md#term-goroutine). |
| CS-16 | Advisory | Outside `ipc/`, in selected Go, tests included: import-resolved `net.Listen*`/`net.Dial*`, package-level `net/http` `ListenAndServe`/`ListenAndServeTLS`/`Serve`/`ServeTLS`, `grpc.NewClient`/`Dial`/`DialContext`, and `net.Dialer`/`net.ListenConfig` literals. Outside `ipc/proc/`, tests included: imports of `os/exec` and `github.com/creack/pty`, and import-resolved `os.StartProcess`, `syscall.Exec`/`ForkExec`/`StartProcess`. Method calls are not resolved. |
| CS-17 | Advisory | Import-resolved `os.Getenv`, `os.LookupEnv` and `os.Environ` calls in selected Go, tests included, outside `runtime/config/` and a binary's own `app/<name>/config/`. `os.Setenv` and `GinkgoT().Setenv` write and are not reported. |
| CS-18-MOCKGEN | Advisory | Exported, mockable interfaces in selected non-test Go (excluding package `main`) not covered by a `//go:generate` mockgen directive whose `-destination` is a tracked file with the MockGen generated header. Source-mode `-source` paths resolve relative to the directive; package-mode import paths resolve through the tracked `go.mod` module table. |
| CS-18-CROSSING | Advisory | In every handwritten `_test.go` file, in-package and external: import-resolved listen/dial, package-level `net/http` serving, `httptest.NewServer`/`NewTLSServer`/`NewUnstartedServer`, `os/exec.Command`/`CommandContext`, `pgxpool.New`/`NewWithConfig`, `pgx.Connect`/`ConnectConfig`, `database/sql.Open` with a `pgx`/`postgres` driver literal, and `testcontainers-go` imports. A file whose leading `//go:build` constraint names the positive `acceptance` tag is the labelled acceptance suite and is out of [scope](../../csf/docs/generated/ontology_cgen.md#term-scope). |
| CS-18-EXTERNAL | Advisory | A non-`main` package whose selected non-test files declare exported API with no same-directory `<name>_test` package holding a `Test*`/`Example*`/`Fuzz*` function or a Ginkgo `Describe`/`DescribeTable`. One finding per package, at the first exporting file's package clause. |
| ONTOLOGY-DIRS | Advisory | Directories holding tracked Go whose role-position segment (the first under the repository root, and every one below `ipc`, `runtime` or `web`) is not a term identifier, or its plural, in `csf/compiler/language/architecture.csf`; catch-all names at any depth. `internal` is transparent. |
| GOROUTINE-SHARED-STATE | Advisory | An immediately launched Go closure captures a variable also accessed later by its caller, and at least one access is a write. Reports the write and launch/caller lines when local analysis cannot match synchronization. |

The mandatory `DEPENDENCIES` rule reuses the OCaml Gorilla dependency checker.
`check-house-lint.sh --test` runs its native regression target, and the complete
runner applies that scanner to the repository inventory.

The OCaml runner invokes pinned `dupl`, the existing Go typed interface-return
analyzer and pinned golangci-lint as specialist tools. These algorithms remain
with their existing owners; OCaml owns their invocation, severity, accumulated
status and report. An advisory specialist crashing or failing to load its
packages is a scan error, not an optional finding.

## Corpus and limitations

`PY-MAGIC-STRING` scans tracked first-party `.py` files, excluding vendor,
third-party and frozen research sources, generated headers, `tests/`,
`test_*.py` and `*_test.py`. Module/class declarations and uppercase named
constant initializers are declaration sites; imports, docstrings, empty strings
and separators are exempt. Recognizable log/error/print messages and strings
containing prose are exempt, but semantic values in structured log payloads
remain review candidates. These are syntax-based distinctions, not inferred
human intent. The existing Go rule keeps its own corpus and exemptions.

The Python locator covers ASCII identifier-shaped static strings, including
raw/triple/concatenated literals and supported escapes. It examines literals
inside f-string interpolations; dynamically constructed text, bytes, Unicode
names and signature defaults are outside its coverage. Input must be UTF-8.
Unreadable sources, grammar errors and malformed supported hexadecimal escapes
are scan errors rather than advisory results; this is not full CPython compiler
validation. OCaml `.ml`/`.mli` files are not scanned for
magic strings. The report's source count combines selected Go and Python files;
their rule counts remain separate.

`GOROUTINE-SHARED-STATE` locates potential conflicting access across a [goroutine](../../csf/docs/generated/ontology_cgen.md#term-goroutine)
boundary, including `*pointer = nil`, field and slice writes, and a caller
clearing a captured variable. Different lifetimes alone do not establish a
race. A finding asks for an ownership, completion or synchronization review;
it does not prove an execution races. Reads without writes, closure-local
variables, synchronous calls and captures with no later caller access are
outside this candidate pattern.

The local check recognizes matching `Lock`/`Unlock` sections on directly typed
`sync.Mutex`/`sync.RWMutex` variables and parameters, including import aliases
and deferred unlocks. Both accesses must use the same mutex; writes require an
exclusive lock. Merely declaring a mutex or locking one side does not discharge
a finding. Conditional lock changes do not establish an unconditional guard.
It also recognizes a directly typed `sync.WaitGroup` when `Add(1)` immediately
precedes the launch, the closure begins with `defer group.Done()`, and the
caller reaches an unconditional `group.Wait()` before the conflicting access.
Waiting before launch or only in a conditional branch does not qualify.

This is syntax-based review, not Go alias or control-flow analysis. It does not
follow pointer aliases, arguments passed into a [goroutine](../../csf/docs/generated/ontology_cgen.md#term-goroutine)'s parameters, named
worker functions or lifecycle callbacks invoked by libraries. It groups
accesses by their captured root variable, so independent fields can require
review. It does not establish channel completion, other WaitGroup patterns,
locks hidden in helpers or arbitrary control flow; those can still warn. A matched lexical
lock section does not prove what called helpers do. Keep race-enabled tests
and the [ownership contract](../../pkg/widget/README.md#channel-ownership-discipline)
alongside the check; a clean scan is not a concurrency-safety proof.

`HANDLER-DB-IO` first identifies packages declaring a database or SQLC/store
dependency, including imports in sibling and generated files. An in-memory
map called a store does not establish database I/O. In those packages it
[scopes](../../csf/docs/generated/ontology_cgen.md#term-scope) production functions by handler/middleware file or
directory names, handler/middleware receiver types, `net/http` and Gin
signatures, and generated strict-interface `RequestObject`/`ResponseObject`
signatures regardless of filename. Within those [scopes](../../csf/docs/generated/ontology_cgen.md#term-scope) it reports database
handles from `database/sql`, pgx/pgxpool and SQLC/store packages, direct
connection constructors, and calls through typed, locally aliased or chained
storage receivers. It visits nested function literals so persistence hidden in
an event callback remains in the handler's [scope](../../csf/docs/generated/ontology_cgen.md#term-scope). Calls through the [service](../../csf/docs/generated/ontology_cgen.md#term-service)
field are allowed; pure row/view DTO use and request parsing are not database
I/O. This is local syntax analysis: it does not follow a [service](../../csf/docs/generated/ontology_cgen.md#term-service) operation into
its implementation or prove transitive absence of I/O. A clean result only
establishes that this scanner found no covered direct access at the boundary.

`ELSE-AFTER-RETURN` inspects each `if` branch's final statement and recognizes
direct returns plus nested `if`/`else` statements whose two branches both
return. A conditional return inside an otherwise continuing branch does not
count. It does not infer termination through loops, switches, helper calls or
`goto`. Its finding points at the alternative branch; removing the nesting can change
local variable [scope](../../csf/docs/generated/ontology_cgen.md#term-scope), so keep an explicit block where that [scope](../../csf/docs/generated/ontology_cgen.md#term-scope) is needed.

The general native style corpus is tracked handwritten first-party Go. It
excludes vendor directories, frozen `research/`, the self-contained gotth
benchmarks/examples/guide samples, and generated files. Generated source is
recognized by the standard `// Code generated ... DO NOT EDIT.` marker or a
leading comment banner carrying the configured ownership marker and disclaimer
from the compiler's `Codegen_header` module. The latter also recognizes its
historical owner name and tolerates decorative borders without duplicating the
banner text. Names or disclaimers alone do not establish generated ownership.
Generated files are exempt from every native style and test-convention finding,
including CS-11 and TEST-BOOTSTRAP, but remain available as declaration,
collision and database-import evidence for checks on handwritten source.
The dependency lane deliberately has broader coverage: tracked
and unignored source and dependency records, including research and generated
files. CS-11 applies to tracked handwritten test imports repo-wide outside vendor,
including research tests. [Service](../../csf/docs/generated/ontology_cgen.md#term-service) artifact candidates come from the tracked
inventory, excluding recognized generated Go sources. CS-4 and CS-10 source
candidates exclude `_test.go`; [service](../../csf/docs/generated/ontology_cgen.md#term-service) artifact checks are inventory-based.
Individual rules retain their narrower production/test [scopes](../../csf/docs/generated/ontology_cgen.md#term-scope).

The native parser detects syntax, not architectural intent. CS-4 cannot decide
whether every type is derivable; generator reproducibility remains checked by
the owning component. CS-10 cannot prove that another binary can compose a
[service](../../csf/docs/generated/ontology_cgen.md#term-service), and a signal/listener/configuration candidate requires review of the
actual ownership boundary. Compose presence is not evidence of a violation and
does not authorize deleting deployment files. Reuse findings likewise require
identifying the lowest package that owns the shared semantics.

The native CS-8 [scope](../../csf/docs/generated/ontology_cgen.md#term-scope) remains narrower than Go type analysis. The specialist
reports legitimate upstream contracts and other accepted interface results on
purpose. Do not claim that zero narrow findings establishes no interface-valued
results, or that zero lexical candidates proves compliance with every principle.

The legacy Python `check_style.py`, census and tier-1 eval functions remain
available for historical measurements and fixture comparisons. They do not
measure the new AST coverage or new CS-4/CS-10/expanded CS-12 candidates, and
their counts are not the native CI verdict. Keep existing ledger rows and dated
research intact; do not present the old and new detector counts as one series
without recording that the measurement changed.

## Ontology alignment score

`bash tools/ontology-score.sh` prints one JSON record saying how far HEAD is
from the CSF ontology's placement and vocabulary rules. It is a measurement, not a gate: a worse score never fails, but a failed
or incomplete scan exits 2 and prints no record. `--format openmetrics` prints
the same record as timestamped samples; `--receipt DIR` writes both forms from
one run. [`alignment.ml`](alignment.ml) owns the definition and
[`score.ml`](score.ml) the procedure; the numbers below are theirs.

| Signal | Weight | Source |
|---|---|---|
| `csfc-check` | 10 | `csfc check` diagnostics for `csf/architecture/architecture.csf` |
| `generated-drift` | 10 | `csfc check-generated` drift plus files the language generator's `check` reports as differing |
| `cs-16` | 5 | CS-16 findings: crossings outside `ipc/` |
| `cs-15` | 3 | CS-15 findings: [goroutines](../../csf/docs/generated/ontology_cgen.md#term-goroutine) with no visible owner |
| `cs-17` | 3 | CS-17 findings: environment reads outside `runtime/config` |
| `ontology-dirs` | 3 | ONTOLOGY-DIRS findings |
| `retired-vocabulary` | 2 | retired words in tracked `**/README.md`, from the language generator's `lint` |
| `unlinked-terms` | 1 | unlinked ontology terms in the same READMEs |

`penalty = sum(weight * count)` over measured signals and
`score = penalty / (penalty + 1000)`: lower is better, 0 means every measured
signal is clean, and 1000 is the penalty that scores 0.5. A maximizer can use
`1 - score`. The weights rank how directly a finding breaks the ontology; they
are a stated choice, not a calibration. Any change to signals, weights or scale
bumps `method_version`, and records with different methods are not comparable.

A signal whose checker has not landed on the measured revision is
`not_measured`: its count is `null`, it contributes nothing and the record says
`"complete": false`, so an incomplete score is a lower bound. A house-rule
signal is measured exactly when its rule is registered in [`policy.ml`](policy.ml),
and the vocabulary signals when the language generator has its `lint` verb, so
an older revision measured with an older checker reports them as pending rather
than zero. The house-rule counts use the native scan's corpus and limitations
above; a scan error is never scored.

Main at `1630a84` (2026-10-02, the first measurement from this checkout)
scored 0.5450, penalty 1198, incomplete: `generated-drift` was not measured
because the language generator's `check` stops at two unlinked terms in the
root `README.md` before it compares documents. Unlinked terms contribute 739
(739 findings at weight 1), `cs-16` 240 (48), `cs-15` 108 (36), `cs-17` 93
(31) and `ontology-dirs` 18 (6); `csfc-check` and `retired-vocabulary` are
clean. Linking README terms is the largest single lever on the score.

### Pull-request ratchet and PR descriptions

The score's [ratchet](../../csf/docs/generated/ontology_cgen.md#term-ratchet)
mode, `bash tools/ontology-score.sh --ratchet [--base REF]`, measures HEAD and its
merge base with `REF` (default `origin/main`) using this checkout's checkers, so the
method stays fixed and only the tree changes. It prints a per-signal
before/after/delta table and exits 1 when the penalty over signals measured on
both sides rose, or when a **blocking** signal rose even though the total fell:
`csfc-check`, `generated-drift` and `cs-16`, each a broken contract or a
capability bypass that other improvements cannot pay for. A signal measured on
one side only is shown but neither regresses nor improves the comparison.

The `ratchet` job in the same workflow runs this on every pull request whose
diff can move the score, posts the table to the job summary and retains both
receipts. The `ontology-regression-accepted` label makes a reviewed regression
pass; the table still prints. The specs in [`tools/gates`](../gates) check
that the path filters cover every input the score reads.

`bash tools/ontology-score.sh --pr-spec` makes the same two measurements and
prints the pr-description skill's `ontology_score` field, so a description
quotes the measured numbers; every pull request here carries it.

### Series, panel and receipts

Every main push runs the
[`ontology-alignment`](../../.github/workflows/ontology-alignment.yml) workflow,
which retains `ontology-alignment.json` and `ontology-alignment.openmetrics` as
a 90-day artifact named for the commit. The OpenMetrics samples carry the commit
time and two bounded labels, `method` and `signal`; the revision lives only in
the JSON receipt. They are shaped for
`promtool tsdb create-blocks-from openmetrics` against central Prometheus
storage. The pinned Prometheus 3.13.0 `promtool` turns one receipt into a block
of 17 series offline; importing blocks into central storage is an operator step
and has not been run.

| Series | Meaning |
|---|---|
| `candace_ontology_alignment_score` | the score, in [0,1) |
| `candace_ontology_alignment_penalty` | the weighted sum |
| `candace_ontology_alignment_complete` | 1 when every signal was measured |
| `candace_ontology_alignment_commit_timestamp_seconds` | commit time, for record age |
| `candace_ontology_alignment_signal_measured{signal}` | 0 for a signal not yet measured |
| `candace_ontology_alignment_signal_count{signal}` | findings per measured signal; absent when not measured |

The monorepo's `candace-ontology-alignment` Grafana dashboard shows the latest
score, completeness, record age, unmeasured signals, and score, penalty and
per-signal counts per commit; missing records read as "No record", never as a
clean zero. Its fixture tests pin the method version and restate the weights,
and no record has been imported into Prometheus yet.
