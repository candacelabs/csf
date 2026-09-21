# Consuming this module from a legacy WORKSPACE build

**This is the second-class path.** The supported way to depend on `candace` is
bzlmod, in one of the two shapes `examples/external-consumer` demonstrates and
the release process proves before it publishes an archive. Read that example
first; come back here only if the consuming repository still builds from a
`WORKSPACE` file.

## Why there is no `WORKSPACE` file in this module

Bazel 9 — the release `.bazelversion` pins and the only one this module is
built and tested with — removed `WORKSPACE` support entirely. There is
therefore nothing for a `WORKSPACE` or `WORKSPACE.bzlmod` file *here* to do:
Bazel would not read it, and a file that looks load-bearing but is inert is
worse than its absence. `.bazelrc` states the same rule from the other side
(`common --enable_bzlmod`, "nothing may reintroduce a WORKSPACE silently"), and
`tools/check-bazel-metadata.sh` fails if one appears.

The file that matters in a legacy build is the **consumer's** `WORKSPACE`, and
what this module owes it is `bazel/deps.bzl`.

## What a legacy consumer writes

The consumer is on Bazel 7.x or 8.x with `--enable_workspace`. In its own
`WORKSPACE`:

```python
load("@bazel_tools//tools/build_defs/repo:http.bzl", "http_archive")

# rules_go and Gazelle first, at the releases bazel/versions.bzl names
# (RULES_GO_VERSION, GAZELLE_VERSION). Under WORKSPACE they must keep their
# legacy repository names.
http_archive(name = "io_bazel_rules_go", sha256 = "...", urls = ["..."])
http_archive(name = "bazel_gazelle", sha256 = "...", urls = ["..."])

http_archive(
    name = "candace",
    sha256 = "<sha256 from the release>",
    strip_prefix = "candace-<sha12>",
    urls = ["https://github.com/candacelabs/csf/releases/download/export-<sha12>/candace-<sha12>.tar.gz"],
    # This module's committed BUILD files were generated under bzlmod, where
    # rules_go and Gazelle are named `rules_go` and `gazelle`. Under WORKSPACE
    # they are not, so the load labels have to be remapped.
    repo_mapping = {
        "@rules_go": "@io_bazel_rules_go",
        "@gazelle": "@bazel_gazelle",
    },
)

load("@candace//bazel:deps.bzl", "candace_dependencies")

candace_dependencies()
```

`candace_dependencies()` is deliberately small. It calls
`go_rules_dependencies()`, registers the Go SDK version this module's BUILD
files were generated against (`GO_SDK_VERSION`), and calls
`gazelle_dependencies()`. That is the part a consumer cannot rederive.

## What it does not do, and what you do instead

`candace_dependencies()` does **not** declare this module's Go dependency
closure. `go.mod` and `go.sum` at the archive root are the authority for it,
and the supported way to project them into a WORKSPACE build is Gazelle's own
importer, run in the consuming repository:

```bash
gazelle update-repos \
  -from_file=bazel-<workspace>/external/candace/go.mod \
  -to_macro=candace_go_deps.bzl%candace_go_deps \
  -prune
```

Shipping a generated `go_repository` list here instead would be a second
dependency list that nothing in this repository can keep honest — no CI job
here can run a WORKSPACE build at all, because the pinned Bazel cannot.

Two consequences of that same limitation are yours to carry:

- **`github.com/pganalyze/pg_query_go/v6` needs patches.** `MODULE.bazel`
  applies `//third_party/pg_query_go:pg_query_go.patch` and turns off build
  file generation for that module; the same two settings belong on the
  `go_repository` your macro generates for it (`patches`, `patch_args`,
  `build_file_generation = "off"`), pointing at
  `@candace//third_party/pg_query_go:BUILD.bazel`. Only the packages that
  reach `pkg/liquidproto`'s SQL parsing need it.
- **The Rust workspace under `xetcas/` is bzlmod-only.** It resolves its crates
  through `crate_universe`, which has no WORKSPACE equivalent maintained here.
  A legacy consumer builds the Go targets and leaves `//xetcas/...` alone, or
  uses the Cargo build that `xetcas/README.md` documents.

## Keeping the pins honest

`bazel/versions.bzl` is a copy: the pins it publishes live in `MODULE.bazel`
and `.bazelversion`, which Starlark cannot read. `//bazel:version_pins_test`
compares the two on every `bazel test //...`, so the copy cannot go stale
quietly.
