# Destination CI

These workflows run in **`candacelabs/csf`**, the private repository this
tree is exported to. They ship with the snapshot: the export declaration in the
monorepo's `Candacefile` sets `requires_workflows_write: true` precisely so the
publisher may write this directory.

The `csf_workbench` job also verifies public contract projections and builds/tests
the Workbench browser assets using only this repository.

## They do not run where they are written

In the monorepo this file lives at `candace/.github/workflows/`, and GitHub only
reads workflows from a repository's own root `.github/workflows/`. So these are
inert there — by construction, not by an `if:` guard that someone could delete.
The path that makes them live is the export itself: `candace/` becomes the
repository root, and `candace/.github/` becomes `.github/`.

The monorepo has its own gates over the same content, and those are the ones a
change to this tree must satisfy before it can ever reach here:

| here | monorepo |
|---|---|
| `ci.yml` → `go`, `rust` | `.github/workflows/candace-bazel-checks.yml` |
| `ci.yml` → `identifiers` | `.github/workflows/component-export-checks.yml` |
| `ci.yml` → `candaceos` | `.github/workflows/candaceos-acceptance.yml` |
| `ci.yml` → `go_toolchain` | **no single counterpart.** The monorepo splits the same packages across `candace-go-checks.yml`, `gotth-live-checks.yml` and `pgmem-checks.yml`, and none of them runs `go test ./...` over the whole module. `//app/warden/e2e:e2e_test` is `manual` in Bazel and is reached by this job and nothing else, in either repository |

## What they are for

The destination is generated and read-only: the monorepo is canonical, and an
edit made there is overwritten by the next export. So these jobs are not a
contribution gate. They answer a different question — *is this snapshot
coherent on its own?* A snapshot can be green in the monorepo and still be
broken here, because here it is a repository rather than a subdirectory: the
module root moves, `candaceos/` sits at the top level, and consumers take this
tree as a Bazel module. Every job here asks that question and nothing else:
`ci.yml` is the only workflow, and it deploys nothing.

Three inert workflow copies that predate this directory were folded into it and
deleted: `blog-site/.github/workflows/pages.yml`, `pkg/pgmem/.github/workflows/
ci.yml`, and `xetcas/.github/workflows/ci.yaml`. Each targeted a standalone
repository that this monorepo export retires, and each was already inert in
both repositories — nested `.github/` directories are read by nobody. The
blog-site copy briefly returned here as `pages.yml`, which published
blog.candace.cloud out of the exported `blog-site/` generator; the operator
retired that generator on 2026-08-27, so the workflow went with it. Nothing in
this repository publishes a website any more.

## Conventions

- **GitHub-hosted `ubuntu-24.04` runners.** The monorepo's workflows target a
  self-hosted fleet that does not exist here.
- **Least privilege.** The workflow default is `contents: read`, and no job
  asks for more. The export declaration still sets `requires_workflows_write`,
  because that is the publisher's permission to write this directory, not a
  permission any job in it holds.
- **`actions/checkout` with `persist-credentials: false`.** No job in this
  repository writes to it.
- **Bazel comes from the pinned container**, through `tools/bazel.sh`, rather
  than from a runner-provided Bazel or a `setup-` action. It is the same
  command a developer runs, and `.bazelversion` and `MODULE.bazel` remain the
  only version authority. Bazel's caches are deliberately not carried between
  runs; the reasoning is in `ci.yml`.

## Operator prerequisites

None. Every job runs on a GitHub-hosted runner from a `contents: read` checkout
and needs nothing configured in the destination's settings. The one prerequisite
that used to live here — Pages source, custom domain, DNS — retired with
`pages.yml` on 2026-08-27.
