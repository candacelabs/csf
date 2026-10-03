# Datalog engine

The architecture compiler's relational checks are Datalog rules
([`architecture/rules.dl`](../../architecture/rules.dl)) evaluated by the
top-down engine of [`datalog`](https://github.com/c-cube/datalog) v0.7
(BSD-2-Clause, opam package `datalog`). The root `MODULE.bazel` pins the
release archive by the same SHA-256 the opam-repository manifest records,
`13ca520bddf4f0c44d1468bc89347be72ec543be58fff29469a0da24956be541`.
`source.BUILD.bazel` runs `ocamllex` and `ocamlyacc` from the pinned OCaml
toolchain over the upstream lexer and parser; `BUILD.bazel` compiles the four
modules of the `datalog.top_down` library. Nothing is patched.

## Why this engine

Measured on 2026-10-02 against what the compiler's checks need.

| Need | `datalog` 0.7 top-down (chosen) | Soufflé 2.5 | Our own evaluator |
|---|---|---|---|
| Recursion | SLG resolution with tabling; terminates on cyclic graphs | yes, semi-naive | would have to be written |
| Stratified negation | `~p(...)` on ground goals | yes, checked at compile time | would have to be written |
| Counting | aggregates over a guard with a pluggable builtin; the compiler registers `count` | `count : {...}` | would have to be written |
| OCaml integration | in-process library; facts are built from `Model` records as terms | separate C++ binary or generated C++ linked by FFI; facts cross a process or FFI boundary | native |
| Pinned, hermetic build | one archive, pure OCaml, built by rules_ocaml with the pinned compiler (OCaml 5.3.0) | not in the Bazel Central Registry; CMake, flex, bison, libffi, a hand-written Bazel build of the whole C++ tree | native |

Soufflé is the stronger engine and the wrong fit: its integration cost is a
second toolchain and a process crossing for a graph of a few dozen facts.
The opam-repository has no other Datalog engine. `datalog` 0.6 bounds
`ocaml < 5.0`; 0.7 lifts it. No requirement was left that justifies writing an
evaluator.

## Engine limits the rules respect

These were measured, not taken from the documentation. `rules_test` guards the
first two over every rule in `rules.dl`.

- **No safety check.** `C.mk_clause` accepts any clause; an unsafe head or a
  negative literal reached before its variables are bound fails at query time.
  Every head variable and every variable of a negative or builtin literal must
  occur in an earlier positive literal.
- **No stratification check.** `NonStratifiedProgram` is declared and never
  raised. Negation through recursion is silently unsound, so no predicate may
  depend negatively on itself.
- **A body ends at its aggregate.** Literals after `N := count V : guard` are
  ignored, so an aggregate is the whole body of its clause.
- **A goal with a repeated variable has no answers.** `reach(X, X)` finds
  nothing; bind the node first, as in `node(S), reach(S, S)`.
- **Recursion is left-recursive.** The only recursive call in a rule is a
  variant of its head, so no tabled goal can complete while a different goal it
  depends on is still open.
