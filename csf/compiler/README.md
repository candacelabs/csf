# csfc - the CSF compiler

`csfc` checks declared process and [scope](../docs/generated/ontology_cgen.md#term-scope) relationships and selected Go source
boundaries. The [architecture compiler](architecture/README.md) owns this
implementation; the [documentation compiler](language/README.md) owns the
shared vocabulary and diagrams. These sources, build definitions, policies and
generators ship in the CSF export. The monorepo builds this same tree directly.
The [API projection compiler](api_codegen/README.md) generates typed Go clients,
HTTP/MCP registration and CLI catalogs from the protobuf/OpenAPI contracts.

The public module supplies pinned OCaml dependencies as development dependencies,
so Go consumers do not resolve the compiler toolchain. From its root:

```sh
bash tools/bazel.sh test //csf/compiler/architecture:all //csf/compiler:csfc --nobuild_tests_only --lockfile_mode=error
bazel-bin/csf/compiler/bin/csfc check
```

## Why a language, and not plain Datalog

csfc's checks are Datalog rules, but plain Datalog constants are strings, and a
string rule fails silently. CSF declares every noun a rule may name, and csfc
refuses a rule that names something undeclared. The tour explains both figures
in [its compiler stop](../../docs/TOUR.md#figure-6).

<p align="center"><a href="../../docs/assets/tour/csf-typed-rules.svg"><img src="../../docs/assets/tour/csf-typed-rules.svg" width="1000" alt="Untyped rules break silently; typed rules fail at compile time"></a></p>

Each layer then has one job: CSF and csfc build typed, checked axioms; Datalog
says how the nouns may play; Lean proves the definitions sound and the rules
true over them.

<p align="center"><a href="../../docs/assets/tour/csf-nouns-rules-theorems.svg"><img src="../../docs/assets/tour/csf-nouns-rules-theorems.svg" width="1000" alt="Nouns, rules, theorems: who builds what"></a></p>

## Where to go next

The monorepo also stages the executable at `bazel-bin/bin/csfc`. Start with the
[runnable walkthrough](architecture/WALKTHROUGH.md) and the
[language extension guide](architecture/README.md#extend-the-language).
Consumers can modify their own checkout and rebuild the compiler; no private
monorepo source or synchronization step is required. The compiling
[Lean verifier stub](verification/README.md) returns `notImplemented` for every
input; it proves no compiler, [runtime](../docs/generated/ontology_cgen.md#term-runtime), or physical-safety property.
