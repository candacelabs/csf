# Gorilla mux dependency check

This repository policy rejects these dependency paths and their subpackages:

- `github.com/gorilla/mux`
- `github.com/getkin/kin-openapi/routers/gorillamux`
- `github.com/oapi-codegen/gin-middleware`

Run the native regression tests from the standalone repository root:

```bash
bash tools/bazel.sh --batch test //tools/gorilla_mux_lint:checker_test \
  --lockfile_mode=error --test_output=errors
```

The standalone repository's Bazel CI runs this target with the rest of the
compiler tests.

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
