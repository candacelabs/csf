# candace — agent guide

This repository contains one Go module and one Rust workspace, built by its root
Bazel module. The [CSF compiler](csf/compiler/README.md) uses development-only OCaml dependencies in the public
Bazel module; its canonical sources live under `csf/compiler`. This is the
public half of a private infrastructure monorepo, published whole rather than
assembled from parts. It carries deploy (an agent-operated app lab and its
deployment kit), Warden (a fleet watchdog), gotth-live (server-driven live UI),
xetcas (a self-hosted Xet CAS server), a set of domain-neutral Go primitives,
and the examples that hold every extension seam to a compiled test.

Read this file first, then the one nearest your work. It links down rather than
repeating: nothing here is the authority on anything, and every section names
the file that is.

To install the harness, a sample assignment recipe and the miners into another
repository with no host toolchain, follow [`tools/kit/README.md`](tools/kit/README.md).

Vocabulary: the canonical dictionary is
[`csf/docs/generated/ontology_cgen.md`](csf/docs/generated/ontology_cgen.md).
Agents must not read `docs/GLOSSARY.md`; it is a generated plain-language
reference for humans and adds nothing an agent needs.

## Where changes land

CSF is developed in `candacelabs/csf_staging`, its canonical repository since
2026-10-02 (slice C2): changes there land as reviewed pull requests against
`main`, held to the gates and rules below. The public `candacelabs/csf`
repository is a generated snapshot of it, and the section that follows
describes that snapshot.

## The public repository is generated

Every file in the public repository is the tracked content of the canonical
tree at one exact source revision, published as a fresh snapshot with no
upstream history. The provenance marker `.candace-export.json` records the
source repository, source path, exact source revision, selected-tree object ID,
and destination. Each published snapshot also carries an immutable
`export-<sha12>` tag and a semantic-version tag; the GitHub Release uses
`v<version>`. First-party source is Apache-2.0, per the `LICENSE` at this root.

Three consequences for the public repository, and acting against any of them is
expensive:

- **No change lands in the public repository.** Snapshot updates arrive there
  as ready PRs against `main` from `candace-release`. The exporter compares the
  destination byte-for-byte against the snapshot it last published and halts
  on any divergence, so a commit made there is not merely overwritten later —
  it wedges every future export until an operator investigates.
- **A fix belongs in the canonical repository.** If you can reach
  `candacelabs/csf_staging`, make the change there as a pull request and let
  the next publish propose a snapshot PR. If you cannot reach it, report that
  limitation and describe the required canonical fix. Do not commit fixes to
  the generated destination or open hand-written source PRs against it.
  Consumers may vendor and adapt their own checkout; those customizations do
  not update the canonical snapshot.
- **Version identity is immutable.** Use a semantic-version tag for consumption.
  When tracing exact source behavior, cite the
  `export-<sha12>` tag or the source revision in `.candace-export.json`, never
  a branch. A branch name here means "whatever the last snapshot happened to
  be".

For CSF composition, generated tools, and agent-consumer setup, read
[`csf/AGENTS.md`](csf/AGENTS.md) and the [consumer example](examples/csf-consumer).

## Taxonomy

Three top-level Go trees, and the rule that separates them is about who may
depend on whom:

| Tree | Contains | Depends on |
|---|---|---|
| `pkg/` | domain-neutral primitives — nothing in them knows what deploy is | each other and third-party libraries, never `services/` or `app/` |
| `services/` | composable business logic — the parts a different composition could reuse | `pkg/`, and each other |
| `app/` | runnable compositions — each owns a `cmd/` and wires services into a binary | everything |

`pkg/` is [`argv`](pkg/argv) (whether another program's argument vector
carries a flag) `boundedbuffer` `config` `core` `cron` `eventually` `labels`
`liquidproto` `mailbox` `pgmem` `redact` `telemetry`, plus [`pkg/gotth`](pkg/gotth)
and [`pkg/widget`](pkg/widget) — two libraries large enough to have their own
documentation sets — and two directories that hold tooling rather than a
package, `pkg/proto` and `pkg/scripts`.
`services/` is [`deploy`](services/deploy) and
[`warden`](services/warden). `app/` is
`deploy`, `nodeexec`, and `warden`; each carries a `CLAUDE.md`
naming what may not be changed casually.

Around them: `proto/` (the `.proto` sources and their committed bindings),
[`infra/deploy-kit/`](infra/deploy-kit) (the deployment kit — Compose, installer, fleet
driver, updater), [`xetcas/`](xetcas) (a Rust workspace with Go bindings),
`extensions/copilot-pair/`, [`examples/`](examples), and
[`bazel/`](bazel) (the legacy WORKSPACE shim).

There is exactly one `go.mod`, at this root. A nested one is a defect, and
`pkg/gotth/ci.sh`'s D-5 step fails on it.

## Consuming this repository

The unit of consumption is a **deterministic source archive**, not a package
registry entry. Each `v<version>` Release carries `csf-<sha12>.tar.gz`
and its `.sha256`; the tarball is this tree re-rooted so `MODULE.bazel` sits at
the archive root, built twice and byte-compared before it is kept.

```python
bazel_dep(name = "csf", version = "0.1.0")

archive_override(
    module_name = "csf",
    integrity = "sha256-...",
    strip_prefix = "csf-<sha12>",
    urls = ["https://github.com/candacelabs/csf/releases/download/v0.1.0/csf-<sha12>.tar.gz"],
)
```

That is the recommended shape. A plain `use_repo_rule` `http_archive` also
works and costs one thing: a non-module repository resolves candace's BUILD
labels through *your* repository mapping, so you mirror candace's own
`use_repo(go_deps, ...)` list. A Go-only consumer needs neither:
`go get github.com/candacelabs/csf@v0.1.0` applies after that public release
exists. Private staging does not publish this public module tag; use the
[local-archive path](examples/csf-consumer) and preserve the public import path.

[`examples/external-consumer`](examples/external-consumer) is the worked
consumer and the acceptance test every archive passes before publication — it
is built both supported ways in the pinned Bazel image. Start there.
[`docs/extending.md`](docs/extending.md) is the narrative version, and
[`bazel/README.md`](bazel/README.md) covers the second-class legacy WORKSPACE
path.

## Extension seams

Deploy resolves its extension points at compile time. You link your own
Core binary; nothing is loaded at runtime. Each seam has a worked example whose
suite compiles the same code, and [`docs/extending.md`](docs/extending.md) is
the guide that walks all four.

| Seam | Option | Worked example |
|---|---|---|
| Ordered component graph | `bootstrap.WithComponent` | [`examples/external-consumer`](examples/external-consumer) — a three-component chain resolved by `component.Order` |
| Agent harness | `bootstrap.WithHarnessFactory` | [`examples/external-consumer`](examples/external-consumer) — a full `harness.IFactory` compiled outside this tree |
| Identity: name, agent, wordmark, palette | `bootstrap.WithBrand` | [`examples/custom-brand`](examples/custom-brand) — a total rebrand with no Core edit |
| Presentation: overlay, sidebar, routes | `WithUIOverlay`, `WithNavItem`, `WithHTTPService` | [`examples/custom-ui-page`](examples/custom-ui-page) for the smallest shape; `custom-brand` for all of it |

Every row is also proven from *outside* this module.
[`examples/external-consumer`](examples/external-consumer) composes every option
named above into one binary and a service of its own, resolving each package
through an `@csf//` label pointing at a downloaded archive rather than a
relative one; its whole workspace is built and tested in both supported pinning
shapes before an archive is kept.

Two rules survive every seam, and both are enforced rather than advised. Core's
routes — including the `/claws/...` paths — are unchanged by any of them; and
`Wordmark` and overlay templates are **operator-trusted markup**, emitted
verbatim, never assembled from a browser request, a fleet node, or an agent.
Palette values are validated rather than escaped, so an invalid brand fails
assembly instead of rendering a half-branded page.

The contract for the UI seams is the package documentation in
[`web/deploy/webui`](web/deploy/webui): the overridable block
names, the data each receives, and which two the browser client depends on.

## Inherited invariants

Deploy is operated by exactly the kind of system reading this file, and
[`infra/deploy-kit/AGENTS.md`](infra/deploy-kit/AGENTS.md) states its trust model as eight
numbered invariants, each naming its enforcement point. **Read that file before
changing anything under `infra/deploy-kit/`, `app/deploy`, `app/nodeexec`, or
`services/deploy/`.** In one line each, so you know what you would be
breaking:

1. **The default install is harmless** — demo harness, dry-run executor, no
   Docker socket anywhere in `compose.yaml` outside the `live` profile.
2. **The live executor is the sole one-box socket holder**, behind an agent
   backend flag, an explicit `--live-executor`, and a typed confirmation
   phrase.
3. **Core never receives the Docker socket**, in any Compose file in this tree.
4. **The executor's mutation surface is two commands**: `compose config
   --quiet` and `compose up -d --remove-orphans <service>`, invoked without a
   shell.
5. **Approval and fencing live in Core.** Every mutation carries the Warden
   leader ID and term; losing quorum blocks approval and never fails open.
6. **One bounded writable workspace**, with a bounded revision cache that
   rejects new snapshots rather than evicting live ones.
7. **Secrets are generated, mode-600, and never widened**; provider API keys
   pass through the invoking environment and are never persisted.
8. **Nothing here changes the host** — no firewall rule, no Tailscale ACL, no
   systemd unit, no Docker daemon setting, no unrelated Compose project.

If a task appears to require breaking one, stop and surface the trust-model
question rather than writing it.

## Reserved CSF vocabulary

Before describing or changing CSF architecture, read the generated dictionary
named at the top of this file. Its source is
`csf/compiler/language/architecture.csf`; after changing a definition,
regenerate the dictionary and the glossary with the language generator's
`write` verb ([`csf/compiler/language/README.md`](csf/compiler/language/README.md)),
and the ontology score's `generated-drift` signal checks that you did.
**Application means a process/runtime-owning runnable composition. Widgets are
not applications.** Use `widget`, `service`, `web layer` or `page` according to
the defined responsibility; do not use `application` as a casual synonym.
**Widgets exist only within gotth-live**; do not describe them as independently
mounted application components. gotth-live belongs to CSF's web layer within
the application's runtime. Quote upstream identifiers such as `live.App`
literally without treating their spelling as a new process boundary. Nothing
new is named `engine`, `util`, `common` or `core`, and no new "CandaceOS"
names. The architecture checker enforces the selected source/process
boundaries; wording alone does not establish a runtime or cleanup guarantee.

## Work in vertical slices

All code work is planned and delivered as vertical slices. A slice is defined
by its acceptance surface, not by the directories it edits: it carries every
ontology term, primitive, gate rule, consumer migration, test and user-visible
proof that surface needs, and it is validated on top of its completed
dependency frontier. An ontology-only, gate-only, database-only or
frontend-only change is a layer, not a slice, even when it is independently
mergeable — do not plan or propose one. Every plan is a slice graph whose
prefixes are working increments; every PR names its slice and its proof.
Restate this section in every agent brief that writes code.

## Shared primitive reuse

Centralize stable, useful primitives at the lowest shared layer that owns
their semantics: the standard library first, then a focused package under
`pkg/`, and only then a new primitive, written under `pkg/` before the code
that needs it. Keep unsettled or service-specific behavior with its owner until
at least two callers need the same semantics. Do not create catch-all `util`,
`common` or `core` packages. The house checker's mandatory CS-3 specialist
(pinned `dupl`) reports clones across the handwritten Go; when it fires,
centralize the primitive instead of raising the threshold.

## Agent operating rules

Defaults distilled from mined session struggles. A rule a linked file owns wins
over its one-liner here.

- **Intent first.** Before the first tool call on a new message, classify the
  deliverable: answer, text, code change, PR or investigation. Answers and
  text get prose, not commits. Answer a direct question before resuming a
  running loop, and touch only the component the user named.
- **Bash shape.** One plain command per call with literal absolute paths
  inside the active worktree; never rely on a previous call's cwd. No `cd`
  chains, loops, shell variables or `$(...)` in arguments. Create and change
  files with the file tools; commit with `git commit -F <message file>`.
  Reshape a rejected command, never resend it verbatim.
- **Hook timeouts.** When a gated tool times out in a hook, retry once. On a
  second timeout use the documented fallback (a heredoc, or a python
  replace-once asserting `count == 1`) where the guard allows it; if that is
  blocked too, stop and report which tool and hook are blocked.
- **Waiting.** Never `sleep N; check` or a sleep loop. Start a command in the
  background and act on its completion; wait for a condition with a bounded
  watcher, never a shell loop. CI: `gh run watch <id> --exit-status` in the
  background. Current state only: run the check once.
- **Verify before use.** Confirm any path, symbol, JSON key, CLI verb or flag
  not yet seen this session: list the path (exact case), grep the definition
  of a type or function, print the top-level type and keys before parsing
  JSON, `<cmd> --help` before an unfamiliar verb. Re-query session handles
  (run IDs, tab IDs) after a gap.
- **Re-anchor before Edit.** Copy the text to replace from a read taken after
  the latest change to that file by any tool, never from memory or your own
  earlier text. In hard-wrapped prose anchor on one unique line. After one
  failed edit, re-read the range before retrying.
- **Verify scripted edits.** Every sed/python/heredoc rewrite prints expected
  vs replaced counts per file and exits non-zero on a mismatch; then run the
  cheap check (`gofmt -l`, `go vet` on the package, a YAML or config parse)
  before any test run. Prefer the edit tool for five files or fewer.
- **Progress cadence.** Post one line before an investigation or wait longer
  than two tool calls and at least every three calls after; report a root
  cause or a changed plan the moment you find it. Long waits show elapsed time
  and the latest log lines.
- **Toolchains.** Go, OCaml, formatters and linters run in the pinned
  containers (`tools/bazel.sh`; the Go image `.github/workflows/ci.yml` names)
  or a project venv, never on the host: do not hunt the filesystem for a
  toolchain or install into system Python. The repository's own
  standard-library Python scripts (`tools/check_operator_identifiers.py`) are
  the one thing that runs with the host interpreter.
- **Draft PR immediately.** Push and open a draft PR within minutes of your
  first commit (`wip(...)` commits are fine), then push after every commit. A
  branch with commits and no PR is a gap. Briefs say this; never "open the PR
  when done".
- **Skills.** Before writing or extending tests read
  [writing-new-tests](.claude/skills/writing-new-tests/SKILL.md); before the
  first edit of a new feature, service or package read
  [conventions-preflight](.claude/skills/conventions-preflight/SKILL.md);
  before reporting done on a change to a public symbol, string, doc, sample or
  rule read [change-impact-sweep](.claude/skills/change-impact-sweep/SKILL.md).
  Go and UI style is owned by
  [house-code-style](.claude/skills/house-code-style/SKILL.md): read it before
  writing or reviewing any Go here, and paste its
  `references/brief-snippet.md` verbatim into any subagent brief that will.
  Subagents read only their brief.
- **Gates never exempt tests.** A spec reaches a socket, a database or a child
  process through the same capability as production, or uses a gomock double
  or `pkg/pgmem`; the house rules read `_test.go` files like any other file.

Two further rules apply to this whole repository. **Generated files are
projections, not owners**: `.env.example`, `environment.generated.sh`,
`compose.environment.generated.yaml`, the committed `*.pb.go` bindings, and
every `BUILD.bazel` are regenerated from something else, and a hand edit is
erased by the next regeneration. **No operator identifiers**: no real tailnet
IP, hostname, machine name, username, or private repository slug belongs in a
tracked file here. `tools/check_operator_identifiers.py` enforces it on every
run; its pattern list is append-only, so when it fires, fix the content.

## Building and testing

Bazel is the primary build, and it comes from a pinned container rather than
from the host — the same command on a laptop and on a runner:

```bash
tools/bazel.sh build -- //... -//xetcas/...   # everything but the Rust workspace
tools/bazel.sh test  -- //... -//xetcas/...
tools/bazel.sh build //xetcas/...             # the Rust workspace and its Go bindings
tools/bazel.sh run //:gazelle                 # regenerate BUILD files
tools/check-bazel-metadata.sh                 # fail on generated-metadata drift
```

`.bazelversion` (Bazel 9.2.0) and `MODULE.bazel` (rules_go 0.62.0, Gazelle
0.52.2, Go SDK 1.26.5, rules_rust 0.73.0) are the only version authority;
[`bazel/versions.bzl`](bazel/versions.bzl) mirrors them for Starlark and a test
fails if the mirror drifts.

The plain `go` command works on the same tree, needs no Bazel, and is the
authority for the handful of targets tagged `manual` in
`tools/bazel-manual-tests.txt`:

```bash
go build ./...
go test ./...
go test ./services/warden/...
```

The destination's `.github/workflows/ci.yml` runs the complete build, a
vet/API check, and four test shards in the pinned Go container. Those shards
partition the full `go list ./...` inventory, including Warden end-to-end
tests and other packages whose Bazel tests are tagged `manual`.

### House gates

Two gates hold every change here, and both run from a checkout with the same
commands CI uses ([`house-lint.yml`](.github/workflows/house-lint.yml),
[`ontology-alignment.yml`](.github/workflows/ontology-alignment.yml)). Private
staging allocates no hosted runner, so the local run is the bar, before every
push:

```bash
bash tools/check-house-lint.sh --test --summary house-lint-report.md  # 0 mandatory findings
bash tools/ontology-score.sh                                        # one JSON record for HEAD
bash tools/ontology-score.sh --pr-spec                              # HEAD against origin/main, for the PR body
bash tools/ontology-score.sh --ratchet                              # exit 1 if the penalty or a blocking signal rose
python3 tools/check_operator_identifiers.py                         # no operator identifier in the tree
bash tools/check-merge.sh                                           # the merge path's checks on HEAD
```

**No consistency regression reaches main.** The repository has no branch
protection; the merge path is the gate. Pull requests merge only through
`tools/merge-pr.sh <number>`. It merges the head with `origin/main` locally
and runs [`tools/check-merge.sh`](tools/check-merge.sh) on the result:
[`tools/check-generated.sh`](tools/check-generated.sh) (one ontology source,
`csf/compiler/language/architecture.csf`, and no other tracked `.csf`
declaring terms; no orphan, meaning every file under a `docs/generated/`
directory is written back by a generator after all of them are deleted; no
drift after regenerating everything), the score ratchet against
`origin/main`, and the house lint ratchet against `origin/main`
([`tools/house-lint-ratchet.sh`](tools/house-lint-ratchet.sh): no blocking
rule gains a finding; the local bar stays 0 mandatory). Only then
does it squash-merge, pinned to the head it checked. The harness session gate
runs the same script before `gh pr ready`, so a pull request cannot be marked
ready with a regression in it.

The [house lint](tools/house_lint/README.md) is one OCaml checker over tracked
Go and Python plus three pinned specialists (`dupl`, the typed
interface-return analyzer, golangci `funlen`). Its registry,
[`tools/house_lint/policy.ml`](tools/house_lint/policy.ml), decides what blocks
(CS-1, CS-2, CS-3, CS-8, CS-11, CS-16-DB, HANDLER-DB-IO, ELSE-AFTER-RETURN,
TEST-BOOTSTRAP, DEPENDENCIES) and what is advisory. Exit 1 is a mandatory
finding, exit 2 a failed or incomplete scan, and a failed scan is never a
pass. An advisory finding is answered at its site, never silenced with a
narrowed detector, an exclusion or a marker comment.

The [ontology alignment score](tools/house_lint/README.md#ontology-alignment-score)
measures the tree's distance from the ontology: `csfc check`, generated drift,
CS-15, CS-16, CS-17, ONTOLOGY-DIRS and README vocabulary, each with a stated
weight. The pull-request ratchet fails when the comparable penalty or a
blocking signal rises; the `ontology-regression-accepted` label records an
accepted regression. Never keep or drop code for the score.

A gate whose path filters do not fire on a change to its own implementation is
green by never looking, so a new check's filters name every input its verdict
depends on; [`tools/gates`](tools/gates) holds the specs that pin the two
workflows' filters and wiring.

The Rust workspace also builds with plain Cargo from `xetcas/`, which is the
path its demo, container images, and `just` targets take —
[`xetcas/README.md`](xetcas/README.md) owns that story. `MODULE.bazel` hands
crate_universe the same `Cargo.toml` and `Cargo.lock` and writes to neither, so
the two paths cannot disagree.

`infra/deploy-kit/` is shell rather than Go. Its hermetic suites —
`infra/deploy-kit/test-install-validation.sh`, `infra/deploy-kit/test-updater.sh`,
`infra/deploy-kit/test-fleet.sh` — need nothing but bash, python3, and Docker, and run
on every snapshot in `.github/workflows/ci.yml`.

## Conventions

- **Ginkgo/Gomega** for behavior suites, with `go.uber.org/mock` doubles where a
  package already uses them. Ginkgo rejects `go test -count` above 1; repeat a
  suite with the Ginkgo CLI's `-repeat` instead. `pkg/scripts/check-test-style.sh`
  enforces this over `pkg/` (minus `pkg/gotth`, which has its own gate) and
  `tools/`, and the house lint's mandatory TEST-BOOTSTRAP and CS-11 rules
  enforce it in CI; a stdlib `TestXxx` is allowed only as the `RunSpecs`
  bootstrap.
- **Tests never hand-roll time.** Poll with [`pkg/eventually`](pkg/eventually) rather
  than a sleep loop or a bespoke deadline.
- **Structured logging** through `pkg/core` (zerolog) in the Go trees, and
  `pkg/gotth/internal/obs` inside gotth, which is a deliberately separate
  boundary with its own redaction rules.
- **Migrations are the only schema source.** No DDL literal belongs in Go,
  `_test.go` included.
- **Bash style** for the deployment kit: `set -Eeuo pipefail`, a
  component-prefixed `die()`, `printf` over `echo`, `%q` for anything crossing
  SSH, atomic writes through a mode-600 temporary file plus `mv`.
