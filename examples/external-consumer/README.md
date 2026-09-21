# Building against candace from another repository

This is a complete Bazel repository that depends on `candace` the way a real
consumer does: it pins one published source archive and, from that archive
alone, links a Core binary of its own wearing its own identity, serving its own
page, running its own services, and driven by its own agent runtime. It never
forks or vendors the source tree, and it holds every one of Core's extension
seams to a compiled test.

Everything under [`_workspace/`](_workspace) is that repository. The leading
underscore keeps it out of both toolchains that would otherwise claim it: the
`go` command ignores such a directory, and `.bazelignore` stops Bazel from
reading its `BUILD.bazel` files as packages of this module. They are not — they
belong to a build that resolves `@candace` to a downloaded archive.

## What it proves

Core resolves its extension points at compile time; a consumer chooses them by
handing options to `bootstrap.Run`. Every option that composes behavior is
exercised here, and every candace package behind them arrives through an
`@candace//` label pointing at a tarball:

| Seam | What this repository supplies |
|---|---|
| `WithComponent` | three components of its own, in a graph Core orders |
| `WithHarnessFactory` | a full `harness.IFactory` and `harness.IRuntime` |
| `WithBrand` | an invented product's name, agent, wordmark, and palette |
| `WithUIOverlay` | one shipped template block, redefined |
| `WithNavItem` | one sidebar entry, after Core's own four |
| `WithHTTPService` | the page that entry links to |

The one option missing from that table is `bootstrap.WithPII`, which is not a
seam: it turns off Core's redaction of configuration-derived values in errors
and diagnostics. Nothing a consumer writes changes with it, so this repository
leaves the redaction on, as a product shipping to operators would.

The product is called Quillfern. It is invented for this example — not a real
product, company, or service — and it exists so the identity seams are exercised
by something other than candace's own name.

## What it builds

- `steering/` — a bounded store and a service, composed with
  `component.WithRequires` so Core assembles and starts the store first and
  stops it last. Core constructs neither and reads neither one's configuration;
  it owns only the order.
- `noteboard/` — this repository's own service, and the one with business logic
  rather than a fixture's. It keeps a bounded ledger of the steering inputs the
  harness observed, treats a consecutive repeat as a retry, counts sequence
  numbers past an evicted note, and records nothing until Core has started it.
  It joins the graph as a component *requiring* the steering service — an edge
  between two of this repository's own components, resolved by Core — and it
  mounts its own operator page through the HTTP seam.
- `identity/` — the product identity: the two brand-bearing names, a wordmark
  that reuses the shipped lockup's markup so it needs no overlay asset at all,
  a palette delivered as a served same-origin stylesheet, and an overlay
  carrying exactly one file, a redefinition of the shipped `"statusPill"` block.
  Everything the overlay does not name keeps shipping from candace.
- `customharness/` — a full `harness.IFactory` and `harness.IRuntime`
  implementation compiled outside the CandaceOS tree, publishing typed events
  through the host boundary and holding the steering service the composition
  root handed it.
- `composition/` — the composition root, as a library rather than inline in
  `main`, so the suites assert on the option list the binary is linked with
  instead of a second copy of it written to be asserted on.
- `cmd/` — `bootstrap.Run` with that option list, producing a Core binary with a
  different agent runtime, a different identity, an extra page, and the stock
  control plane.
- The suites — Ginkgo specs over the harness with a `gomock` host, over the
  ledger's own rules, over the resolved component order, and over the rendered
  UI. The presentation specs bring this repository's graph up through
  `Assemble` and `Start` in the resolved order, exactly as Core would, and then
  render the pages on the same engine Core builds. None of them needs a Core, a
  PostgreSQL, or a network.

What Core keeps is as much the point: its routes, including the `/claws/...`
paths, its snapshot contract, its API, its persistence, and every string in the
UI that does not name the product or the agent. The specs assert that too.

## The two ways to pin the archive

Each release of `candacelabs/csf` carries a `candace-<sha12>.tar.gz` and its
`.sha256`. `MODULE.bazel` at the archive root makes the tarball a Bazel module,
so there are two shapes, and this example is built both ways before any archive
is published.

**`bazel_dep` + `archive_override` — use this one.**
[`_workspace/MODULE.archive-override.bazel.in`](_workspace/MODULE.archive-override.bazel.in)
names the module, pins the exact bytes with Subresource Integrity, and lets
candace's own `MODULE.bazel` supply the versions of its dependency closure,
register the Go SDK its committed BUILD files were generated against, and
declare the repositories candace packages for itself.

```python
bazel_dep(name = "candace", version = "0.0.0")

archive_override(
    module_name = "candace",
    integrity = "sha256-...",          # `integrity` from the packager
    strip_prefix = "candace-<sha12>",  # `strip_prefix` from the packager
    urls = ["https://github.com/candacelabs/csf/releases/download/export-<sha12>/candace-<sha12>.tar.gz"],
)
```

**`http_archive` — the fallback.**
[`_workspace/MODULE.http-archive.bazel.in`](_workspace/MODULE.http-archive.bazel.in)
fetches the same tarball without treating it as a module. That is sometimes what
a consumer wants, and it costs the repository mapping: a repository fetched this
way is not a module, so the labels inside candace's BUILD files resolve through
the *consumer's* mapping. Every repository those files name has to be visible in
the consumer's `MODULE.bazel`, and a missing one is a build error inside
candace. The repositories candace declares for itself are the ones that cannot
be supplied by pasting a `use_repo` block: `@candace//pkg/pgmem` names
`@pg_query_go`, a patched archive candace's own `MODULE.bazel` fetches, so in
this shape it does not resolve at all until the consumer copies that declaration
too. This shape also has to download the Go SDK itself, because candace
registers no toolchain when it is not a module.

**Both shapes read candace's `go.mod`, so both carry its `use_repo` list.**
This workspace has no `go.mod` of its own and cannot have one — it lives inside
the export root, and the published archive is exactly one Go module — so both
files point `go_deps.from_file` at candace's. Gazelle then treats candace's
direct dependencies as the consumer's, which is what `bazel mod tidy` writes;
a shorter list, even one naming exactly the repositories this workspace's BUILD
files use, makes every build print a warning telling the consumer to fix its
module file. Five of the entries are named by a BUILD file here and the rest
ride along. A consumer repository with a `go.mod` of its own points `from_file`
at that one, and then the list is its own direct dependencies.

Both files carry `@CANDACE_ARCHIVE_...@` placeholders and a
`@CANDACE_GO_REPOS@` line. The monorepo's
`tools/test_candace_external_consumer.sh` substitutes them with the lock
material `tools/package_candace_archive.sh` prints and with the `use_repo` block
read out of the archive's own `MODULE.bazel` — so neither file can describe a
dependency set the archive no longer has — serves the archive from a throwaway
container, and then builds *every* target and runs *every* suite in this
workspace, in both shapes, inside the pinned Bazel image. It fails the run if
either build reports the consumer's `use_repo` list as incorrect. That is how a
release learns that its archive works before anyone depends on it.

## Running it yourself

Against a published release, copy `_workspace/`, write one of the two module
templates as `MODULE.bazel`, and fill it in by hand: the release's `integrity`
(or `sha256`), its `strip_prefix`, its archive URL, and — in place of the
`@CANDACE_GO_REPOS@` line — the `use_repo(go_deps, ...)` block from the
archive's own `MODULE.bazel`, which `bazel mod tidy` will also write for you.
Keep `_workspace/.bazelrc` too: CSF's OCaml compiler resolves `tools_opam` from
the OBazl registry listed there, before Bazel searches the public registry.
This outside-product example builds the Go runtime, so Candace does not ask it
to resolve the archive's development-only compiler toolchain.
Then, from inside that copy:

```bash
bazel build //...
bazel test //...
```

`//cmd:custom-candaceos` is the resulting Linux Core executable, with the custom
harness, the custom components, and the custom presentation compiled in. It
reads exactly the configuration the stock command reads — a PostgreSQL URL, a
writable data directory and workspace, a Warden URL, and a harness selection —
and adds no setting of its own. `candaceos/README.md` describes how a fleet
deployment layers such a binary over the standard Core runtime.

## Consuming from a legacy WORKSPACE build

There is a second-class path for a repository that has not migrated to bzlmod.
It is documented in [`bazel/README.md`](../../bazel/README.md), it is not what
this example demonstrates, and no test in this repository can exercise it: the
Bazel release this module pins removed WORKSPACE support entirely.

## What the external consumer proves

Run from a pristine extraction of the export root, the end-to-end check
establishes one checkable thing: a repository with no candace source in it
builds every target it declares and passes every suite it declares, in both
pinning shapes, against a tarball served over HTTP, using nothing from the
monorepo but that tarball. The composition it links is the whole extension
surface at once — three components in a graph Core orders, a full agent
harness, an invented identity, an overlay, a sidebar entry, and a page of its
own — so "a service is easy to add" is a claim the build either satisfies or
breaks on.

It stops short of running Core. An assembled Core opens PostgreSQL, a Warden
client, and a harness, so the suites hold the values this repository hands
`bootstrap.Run` to the same web UI and the same Gin engine Core assembles them
into, and the Core binary itself is proven by linking rather than by booting.
Deploying such a binary over a fleet is `candaceos/README.md`'s subject, not
this example's.
