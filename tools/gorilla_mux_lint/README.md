# Gorilla mux dependency policy

The public export's `tools/gorilla_mux_lint` owns the shared dependency checker.
The infrastructure monorepo imports this same source and BUILD definition through
`@csf//tools/gorilla_mux_lint`; it maintains no compatibility copy.

In the infrastructure monorepo, this policy is part of the unified house-lint
gate. The canonical complete check runs the native regression suite and scans for forbidden dependencies
alongside the other mandatory rules:

```bash
bash tools/check-house-lint.sh --test --summary house-lint-report.md
```

The house-lint registry owns the blocking `DEPENDENCIES` verdict. The focused
script below remains available for targeted investigation and existing callers;
it is not a separate CI gate or a second policy implementation.

## Focused compatibility command

This repository policy rejects these dependency paths and their subpackages:

- `github.com/gorilla/mux`
- `github.com/getkin/kin-openapi/routers/gorillamux`
- `github.com/oapi-codegen/gin-middleware`

Run the native regression tests from the standalone repository root:

```bash
bash tools/bazel.sh --batch test //tools/gorilla_mux_lint:checker_test \
  --lockfile_mode=error --test_output=errors
```

In the infrastructure monorepo, `bash tools/check-no-gorilla-mux.sh --test`
runs the focused check. Its `--root /path/to/checkout` option scans another Git
checkout using this checkout's checker. Paths in diagnostics are relative to the scanned checkout.

The launcher requires Bash, Git and Docker. Bazel and OCaml build inside the
repository's pinned container; no host OCaml installation is needed. Initial
builds download the pinned toolchain and grammar sources. Build outputs are
cached under `${TMPDIR:-/tmp}/candace-server-bazel-cache`; set
`CANDACE_BAZEL_CACHE` to choose a different cache directory. The current
launcher executes the resulting Linux binary on the host, so a compatible
Linux host is required.

The unified house-lint runner imports this same OCaml checker as its
`DEPENDENCIES` rule. Its `--test` mode runs `@csf//tools/gorilla_mux_lint:checker_test`
before scanning. The Go style workflow invokes the unified runner once, and
`candace package check` invokes it before Go tooling regardless of selected
package paths. The focused compatibility command is useful when investigating
only this policy; its `--test` mode runs the same native checker tests.

## Scan contract

The native `check ROOT FILE_LIST` reads a NUL-delimited inventory of repository-relative
paths. The wrapper supplies tracked and unignored files using Git. The checker
reads `.go`, `go.mod`, `go.sum`, `go.work` and `go.work.sum` files in every
directory, including tests, examples, research and generated source.

The pinned upstream Tree-sitter Go grammar identifies source imports. Go syntax
errors are checked in the package/import header, reparsed through the first
recognized top-level declaration. An error node never ends that header; newer
syntax inside declaration bodies does not block this dependency check. Any
imports recovered after the header are still checked. The Go compiler checks
declaration bodies. OCaml owns the forbidden
package policy and Go string-value decoding. An OCaml scanner checks tokens in
module and workspace files, including quoted paths and replacement targets,
while ignoring comments. It accepts modern directives such as `godebug` and
`toolchain` without depending on a second grammar's release cycle. Go tooling
remains responsible for validating manifest grammar. Checksum files use
their three-whitespace-separated-field record format. The C bindings only
return language pointers from the upstream generated grammars.

Exit codes are 0 for a clean scan, 1 for forbidden dependencies and 2 for
inventory, file or parse errors. Diagnostics include the filename and line.
Import-header parse errors and manifest string errors take precedence over
findings, so an incomplete scan cannot pass. This is a dependency policy
check, not a replacement for `go build` or module validation.

The native `checker_test` target covers imports, literals, manifest records,
source locations, modern manifest directives, quoted local paths, scope and
unreadable inventories.

The source and module records are checked locally; the selected Go build/test
package graph does not need mux. Upstream kin-openapi still references mux in
its own tests. A fresh `go mod tidy -diff` on the private module downloads
`github.com/gorilla/mux v1.8.0` and proposes restoring its `go.sum` entries;
accepting those entries makes this gate fail. Removing imports here does not
remove that upstream test dependency. Builds and tests using `-mod=readonly`
work without those entries. This remaining dependency-maintenance limitation
needs an upstream dependency change; the checker does not fork or rewrite
upstream metadata.
