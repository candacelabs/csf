# `tools_opam` compatibility patch

[`bazel9.patch`](bazel9.patch) is a maintained unified diff against
[`tools_opam` 1.0.0.beta.1](https://github.com/obazl/tools_opam/releases/tag/1.0.0.beta.1).
Bazel downloads that pinned dependency and applies the patch through
`single_version_override` in `MODULE.bazel`, with `patch_strip = 1`.

The patch contains source because it describes edits to upstream source files.
`---` and `+++` name the old and new files; `@@` identifies a changed region;
lines beginning with `+` are added, `-` are removed, and a space marks context.
The `/dev/null` section adds `extensions/preserve_configure_mtime.py` inside the
downloaded dependency. Bazel then loads the patched Starlark, which invokes that
helper during compiler preparation. The `.patch` file is not itself executed.

| Upstream file | Reason for the change |
|---|---|
| `extensions/BUILD.bazel` | Expose the Python helper as a Bazel label. |
| `extensions/opam.bzl` | Enable Bazel 9 C++ rule autoloading, keep nested Bazel on this patched module, and declare the locked preparation dependency chain. |
| `extensions/opam/opam_dep.bzl` | Require completed preparation before generating package imports; invalidate imports when a version changes. |
| `extensions/opam/opam_ops.bzl` | Use Bazel's path-existence primitive instead of relying on the runner's `file` command. |
| `extensions/opam/opam_toolchain_xdg.bzl` | Prepare the isolated compiler in stages, resolve package constraints together, and disable nested bubblewrap inside the build container. |
| `extensions/preserve_configure_mtime.py` | Verify restored bootstrap contents, run the real configure command, and retain timestamps only for identical regular files. |

## Preparation and validation

The first four stages have separate five-minute CI jobs. Markers are dependency
edges and stage results; an intermediate marker is not a complete toolchain.

| Repository | Result | Required validation |
|---|---|---|
| `opam.repository` | OPAM executable and package repository metadata | Requested OPAM version and compiler availability. |
| `opam.bytecode` | Intermediate OCaml bootstrap source/build tree in an empty switch | Both bytecode compiler versions; contents and modes of restored regular files. |
| `opam.compiler` | Installed native compiler | Upstream configure/build/install succeeds; `ocamlc` and `ocamlopt` versions match; installed preparation packages match the lock. |
| `opam.packages` | Complete installed dependency graph | Exact package-version map and findlib-library list match the lock. |
| `opam.bootstrap` | Configuration helper and manifest for library imports | Read the validated package manifest before preparing native Bazel metadata. |

The temporary configure wrapper prevents redundant rebuilds when OPAM copies
byte-identical inputs with new timestamps. It still executes configure and
propagates failure. Changed contents or modes receive newer timestamps so Make
rebuilds them. The prior OPAM wrapper setting is restored after the build
command returns, including a failed build.

Package names and findlib library names are separate namespaces. The owning
`csf/compiler/opam.lock.json` records both; normal builds reject drift.
The source monorepo's `deploy/home/update-opam-lock.sh` explicitly refreshes it
after direct pins change; review the resulting graph before committing it.
`MODULE.bazel.lock` separately records Bazel's module and extension resolution.

## Maintaining the patch

Edit the compiler-owned patch in this directory. Both standalone and monorepo
Bazel builds apply this same file; there is no compatibility copy to regenerate.

Apply the patch to the exact pinned upstream release, edit the resulting source,
then regenerate the diff so hunk sizes and context remain valid. Preserve the
explanatory preamble. Verify clean application before running the focused
`tools/tests/test_ocaml_configure_reuse.py` regressions. Starlark changes also
require refreshing both Bazel lockfiles and checking the actual staged compiler
build with `--lockfile_mode=error`.
