# candace — agent guide

This repository is one Go module, one Bazel module, and one Rust workspace: the
public half of a private infrastructure monorepo, published whole rather than
assembled from parts. It carries CandaceOS (an agent-operated app lab and its
deployment kit), Warden (a fleet watchdog), gotth-live (server-driven live UI),
xetcas (a self-hosted Xet CAS server), a set of domain-neutral Go primitives,
and the examples that hold every extension seam to a compiled test.

Read this file first, then the one nearest your work. It links down rather than
repeating: nothing here is the authority on anything, and every section names
the file that is.

## This repository is generated

Every file here is the tracked content of the monorepo's `candace/` folder at
one exact source revision, published as a fresh snapshot with no upstream
history. The provenance marker `.candace-export.json` records the source
repository, source path, exact source revision, selected-tree object ID, and
destination. Each published snapshot also carries an immutable `export-<sha12>`
tag and a matching GitHub Release. First-party source is Apache-2.0, per the `LICENSE`
at this root.

Three consequences, and acting against any of them is expensive:

- **No change lands here.** Snapshot updates arrive as ready PRs from `candace-export` against `main`.
  Source fixes belong in the canonical repository. The exporter compares the destination
  byte-for-byte against the snapshot it last published and halts on any
  divergence, so a commit made here is not merely overwritten later — it wedges
  every future export until an operator investigates.
- **A fix belongs upstream.** If you can reach the canonical monorepo, make the
  change under its `candace/` folder and let the next source `main` push propose a snapshot PR.
  If you cannot reach it, say exactly that and stop. Never commit here, never
  open a PR against this repository, and never fork and patch it as a
  substitute for the upstream change.
- **Version identity is the export tag.** When citing behavior, cite the
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
| `pkg/` | domain-neutral primitives — nothing in them knows what CandaceOS is | each other and third-party libraries, never `services/` or `app/` |
| `services/` | composable business logic — the parts a different composition could reuse | `pkg/`, and each other |
| `app/` | runnable compositions — each owns a `cmd/` and wires services into a binary | everything |

`pkg/` is `boundedbuffer` `config` `core` `cron` `labels` `liquidproto`
`mailbox` `patience` `pgmem` `redact` `telemetry`, plus [`pkg/gotth`](pkg/gotth)
and [`pkg/widget`](pkg/widget) — two libraries large enough to have their own
documentation sets — and two directories that hold tooling rather than a
package, `pkg/proto` and `pkg/scripts`.
`services/` is [`candaceos`](services/candaceos) and
[`warden`](services/warden). `app/` is
`candaceos-core`, `candaceos-agent`, and `warden`; each carries a `CLAUDE.md`
naming what may not be changed casually.

Around them: `proto/` (the `.proto` sources and their committed bindings),
[`candaceos/`](candaceos) (the deployment kit — Compose, installer, fleet
driver, updater), [`xetcas/`](xetcas) (a Rust workspace with Go bindings),
`extensions/copilot-pair/`, [`examples/`](examples), and
[`bazel/`](bazel) (the legacy WORKSPACE shim).

There is exactly one `go.mod`, at this root. A nested one is a defect, and
`pkg/gotth/ci.sh`'s D-5 step fails on it.

## Consuming this repository

The unit of consumption is a **deterministic source archive**, not a package
registry entry. Each `export-<sha12>` Release carries `candace-<sha12>.tar.gz`
and its `.sha256`; the tarball is this tree re-rooted so `MODULE.bazel` sits at
the archive root, built twice and byte-compared before it is kept.

```python
bazel_dep(name = "candace", version = "0.0.0")

archive_override(
    module_name = "candace",
    integrity = "sha256-...",
    strip_prefix = "candace-<sha12>",
    urls = ["https://github.com/candacelabs/csf/releases/download/export-<sha12>/candace-<sha12>.tar.gz"],
)
```

That is the recommended shape. A plain `use_repo_rule` `http_archive` also
works and costs one thing: a non-module repository resolves candace's BUILD
labels through *your* repository mapping, so you mirror candace's own
`use_repo(go_deps, ...)` list. A Go-only consumer needs neither:
`go get github.com/candacelabs/csf@export-<sha12>` works, because the
module path is the repository path.

[`examples/external-consumer`](examples/external-consumer) is the worked
consumer and the acceptance test every archive passes before publication — it
is built both supported ways in the pinned Bazel image. Start there.
[`docs/extending.md`](docs/extending.md) is the narrative version, and
[`bazel/README.md`](bazel/README.md) covers the second-class legacy WORKSPACE
path.

## Extension seams

CandaceOS Core resolves its extension points at compile time. You link your own
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
through an `@candace//` label pointing at a downloaded archive rather than a
relative one; its whole workspace is built and tested in both supported pinning
shapes before an archive is kept.

Two rules survive every seam, and both are enforced rather than advised. Core's
routes — including the `/claws/...` paths — are unchanged by any of them; and
`Wordmark` and overlay templates are **operator-trusted markup**, emitted
verbatim, never assembled from a browser request, a fleet node, or an agent.
Palette values are validated rather than escaped, so an invalid brand fails
assembly instead of rendering a half-branded page.

The contract for the UI seams is the package documentation in
[`services/candaceos/webui`](services/candaceos/webui): the overridable block
names, the data each receives, and which two the browser client depends on.

## Inherited invariants

CandaceOS is operated by exactly the kind of system reading this file, and
[`candaceos/AGENTS.md`](candaceos/AGENTS.md) states its trust model as eight
numbered invariants, each naming its enforcement point. **Read that file before
changing anything under `candaceos/`, `app/candaceos-*`, or
`services/candaceos/`.** In one line each, so you know what you would be
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

That is an authority CI exercises, not a claim: `.github/workflows/ci.yml`'s
`Go build and test` job runs the first two verbatim in the pinned golang
container, and it is the only job in either repository that reaches
`//app/warden/e2e:e2e_test`.

The Rust workspace also builds with plain Cargo from `xetcas/`, which is the
path its demo, container images, and `just` targets take —
[`xetcas/README.md`](xetcas/README.md) owns that story. `MODULE.bazel` hands
crate_universe the same `Cargo.toml` and `Cargo.lock` and writes to neither, so
the two paths cannot disagree.

`candaceos/` is shell rather than Go. Its hermetic suites —
`candaceos/test-install-validation.sh`, `candaceos/test-updater.sh`,
`candaceos/test-fleet.sh` — need nothing but bash, python3, and Docker, and run
on every snapshot in `.github/workflows/ci.yml`.

## Conventions

- **Ginkgo/Gomega** for behavior suites, with `go.uber.org/mock` doubles where a
  package already uses them. Ginkgo rejects `go test -count` above 1; repeat a
  suite with the Ginkgo CLI's `-repeat` instead. `pkg/scripts/check-test-style.sh`
  enforces this over `pkg/` (minus `pkg/gotth`, which has its own gate) and
  `tools/`; a stdlib `TestXxx` is allowed only as the `RunSpecs` bootstrap.
- **Tests never hand-roll time.** Poll with [`pkg/patience`](pkg/patience) rather
  than a sleep loop or a bespoke deadline.
- **Structured logging** through `pkg/core` (zerolog) in the Go trees, and
  `pkg/gotth/internal/obs` inside gotth, which is a deliberately separate
  boundary with its own redaction rules.
- **Migrations are the only schema source.** No DDL literal belongs in Go,
  `_test.go` included.
- **Bash style** for the deployment kit: `set -Eeuo pipefail`, a
  component-prefixed `die()`, `printf` over `echo`, `%q` for anything crossing
  SSH, atomic writes through a mode-600 temporary file plus `mv`.
