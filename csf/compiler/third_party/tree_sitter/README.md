# Tree-sitter inputs

The root `MODULE.bazel` pins the OCaml binding/runtime and Go/Python grammar archives
by immutable revision and SHA-256. Bazel builds the upstream generated C parser
through `grammar.BUILD.bazel`; no Go text is rewritten before parsing.

The Go grammar currently uses upstream [pull request #193](https://github.com/tree-sitter/tree-sitter-go/pull/193),
commit [`5a6af13a0a5b45bc76cac289c783b315b2b74e13`](https://github.com/tree-sitter/tree-sitter-go/commit/5a6af13a0a5b45bc76cac289c783b315b2b74e13),
an **unmerged compatibility revision**, checked on 2026-09-17. Its parent is
upstream master revision `2346a3ab1bb3857b48b29d779a1ef9799a248cd7`.
The archive SHA-256 is
`3b0b0a55c6c8cd6671d813989b1f53af687c3b8afde3fb986ef767acbde0a017`.

This revision includes both the grammar change and generated `src/parser.c`
needed for [Go 1.26's `new(expression)`](https://go.dev/doc/go1.26#language).
It permits expressions in `new`/`make` argument syntax, prefers the established
type nodes when syntax is ambiguous, and supports generic functions that
shadow these builtins. Whether an identifier names a type or a value remains
a Go type-checker decision; Tree-sitter supplies syntax, not type resolution.

The native `//tools/house_lint:mandatory_test` target parses complete function
bodies and asserts allocation argument shapes for literal, conversion, slice,
map and channel forms. It also covers generic aliases, shadowed generic
`new`/`make`, and malformed syntax. The dependency check's import-only parsing
is insufficient evidence for whole-file grammar compatibility.

Replace this compatibility pin with a merged upstream revision when available,
retaining the native grammar regressions and rerunning the complete house scan.

The Python grammar is pinned to upstream v0.25.0, revision
[`293fdc02038ee2bf0e2e206711b69c90ac0d413f`](https://github.com/tree-sitter/tree-sitter-python/tree/293fdc02038ee2bf0e2e206711b69c90ac0d413f),
with its SHA-256 recorded in `MODULE.bazel`. `python_grammar.BUILD.bazel` builds
its generated parser and external scanner. The native
`//tools/house_lint:python_magic_test` target covers the Python literal and
grammar boundaries used by the advisory; the checker does not invoke CPython.
