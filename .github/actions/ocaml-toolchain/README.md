# OCaml installation cache

Run `mode: repository` to initialize pinned opam and its repository metadata,
`mode: bytecode` to build the compiler bootstrap, `mode: compiler` to finish
the native installation, then `mode: prepare` to complete its locked package
graph. Native check jobs use `mode: restore` after
all four preparation stages. Each stage has its own job budget. A missing exact
prerequisite cache fails the consumer instead of rebuilding inside its budget.

The pinned Bazel launcher mounts `.git/csf-ocaml-toolchain/` at
`/csf-ocaml-toolchain`. The repository and bytecode stages also share the pinned
Bazel installation and immutable repository downloads with the following stage,
avoiding repeat extraction and downloads within its five-minute budget.
Bazel output bases, generated repository
manifests and configuration helpers are never shared. Each checkout resolves
its own graph.
The package lock, Bazel modules and toolchain implementation identify the cache.
There are no fallback keys. Repository preparation validates the opam version
and availability of the requested compiler; it does not claim to have installed
a compiler. Bytecode preparation builds the upstream `core` and `opt-core`
targets and verifies both bootstrap compiler versions, but leaves the opam
switch uninstalled. A restored bootstrap must match its recorded file contents
and modes. Native preparation executes the full upstream opam
configuration/build/install recipe with `--reuse-build-dir`.
A temporary opam build wrapper restores recorded timestamps only for unchanged
bootstrap files after source extraction and configuration; changed bytes or modes
force rebuilding. The wrapper is removed before saving the installation. Both
`ocamlc` and `ocamlopt` versions and the installed package subset must match the
lock. The intermediate compiler cache has a separate key. It is preparation
evidence only and never satisfies a native check job.
Final preparation completes the full package and library lock validation before
saving. This target does not build the Bazel configuration helper; native
consumers build that helper through a separate repository dependency after
package preparation. Repository materialization repeats the full graph
validation when a consumer uses the installation. Tests and generated-output
checks remain the responsibility of each consuming job.

Without `CANDACE_OCAML_TOOLCHAIN_CACHE`, the launcher retains its ordinary
repository-local installation behavior. Bazel tracks the optional container
environment through `repository_ctx.getenv`.
