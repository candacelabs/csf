# Destination CI

These workflows ship with every generated snapshot and run on GitHub-hosted
runners when the destination repository is public. Every job checks repository
visibility before allocating a runner. Private staging uses the canonical
repository's own runner fleet, source checks, exact snapshot comparison and
archive-consumer acceptance. Skipped staging workflows are not test results.
The monorepo's `Candacefile` sets `requires_workflows_write: true` so the
publisher may write this directory.

Public contract projections run in separate five-minute jobs. The CSF API
generator requires the prepared OCaml installation; `csf_workbench` independently
verifies browser generation, tests and the production build using only this
repository.

## They do not run where they are written

In the monorepo this file lives at `candace/.github/workflows/`, and GitHub only
reads workflows from a repository's own root `.github/workflows/`. So these are
inert there — by construction, not by an `if:` guard that someone could delete.
The export places them at the workflow root: `candace/` becomes the repository
root, and `candace/.github/` becomes `.github/`. Their visibility guards keep
hosted jobs inactive in a private destination.

The monorepo has its own gates over the same content, and those are the ones a
change to this tree must satisfy before it can ever reach here:

| here | monorepo |
|---|---|
| `ci.yml` → Bazel inventory, runtime partitions, compiler and metadata | `.github/workflows/candace-bazel-checks.yml` |
| `csfc.yml` → compiler and Lean stub | `.github/workflows/brain-spine-composition.yml` → `compiler-package` |
| `ci.yml` → `identifiers` | `.github/workflows/component-export-checks.yml` |
| `ci.yml` → `candaceos` | `.github/workflows/candaceos-acceptance.yml` |
| `ci.yml` → Go preparation, build, vet/API and four test shards | **no single counterpart.** The monorepo splits checks across `candace-go-checks.yml`, `gotth-live-checks.yml`, `pgmem-checks.yml` and `go-coverage.yml`. The destination shards partition the complete `go list ./...` inventory, including packages whose Bazel tests are tagged `manual`. |

## What they are for

The destination is generated: the monorepo is canonical, and a hand-written
destination commit blocks the exporter on divergence. In the public repository,
these jobs gate the generated snapshot PR before it is merged into `main`. They
check whether the snapshot is coherent on its own. A snapshot can be green in the monorepo and still be
broken here, because here it is a repository rather than a subdirectory: the
module root moves, `candaceos/` sits at the top level, and consumers take this
tree as a Bazel module. Every job here asks that question and nothing else:
`ci.yml` checks the runtime packages; `csfc.yml` checks the independent compiler
module and compiles its Lean verifier stub. An optional `notify_release` job in
the existing CI dispatches the canonical publisher after a public `main` push.

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

- **GitHub-hosted `ubuntu-24.04` runners for public updates.** Private staging
  allocates no hosted runners. These portable workflows never require the
  canonical repository's private fleet.
- **Least privilege.** The workflow default is `contents: read`, and no job
  asks for more. The export declaration still sets `requires_workflows_write`,
  because that is the publisher's permission to write this directory, not a
  permission any job in it holds.
- **`actions/checkout` with `persist-credentials: false`.** No job in this
  repository writes to it.
- **Bazel comes from the pinned container**, through `tools/bazel.sh`, rather
  than from a runner-provided Bazel or a `setup-` action. It is the same
  command a developer runs, and `.bazelversion` and `MODULE.bazel` remain the
  only version authority. Runtime jobs share immutable repository downloads
  and content-addressed build results; each job resolves its own module graph.
  Separate five-minute preparations compile Go standard libraries and generator
  tools, then Rust build helpers, network dependencies, storage and generator
  dependencies, and the remaining Xet libraries.
  Consumers require the exact successful preparation cache before proceeding.
  The longer chaos suite has its own Go partition so its full execution fits
  alongside compilation and transfer within the same five-minute job limit.
  Each partition builds its complete target inventory and runs non-manual
  tests in one invocation. Manual tests still build; their documented Go/Cargo
  owners provide the prerequisites needed to execute them.
  Compiler jobs restore a separately prepared OCaml installation whose exact
  package and library inventory is revalidated by Bazel. Neither a cache hit
  nor a preparation job substitutes for a consuming check.
- **Optional cache uploads have a separate budget.** Completed checks remain
  authoritative when an optional build-cache upload is skipped or times out.
  Required preparation caches still fail their producer or consumer when
  unavailable; the optional-save action does not apply to them.

## Operator prerequisites

For a public destination, verification jobs run on GitHub-hosted runners from a
`contents: read` checkout and need no private runner or application credential.
The operator-approved `notify_release` exception uses no checkout and an empty
`GITHUB_TOKEN` permission set. It runs only for public `main` pushes when the
`CSF_RELEASE_SOURCE_REPOSITORY` variable names the canonical source repository.
Its `CSF_RELEASE_DISPATCH_TOKEN` secret must have only Actions write on that
one source repository. It dispatches `publish-component-snapshots.yml` on source
`main` with `release_only: true`; it cannot write destination content. Keep the
variable unset in other consumers. Missing or rejected authentication fails the
configured job visibly; rerun it after repairing or rotating the token.
Repository visibility is an operator-controlled publication decision; these
workflows never change it. The one prerequisite
that used to live here — Pages source, custom domain, DNS — retired with
`pages.yml` on 2026-08-27.
