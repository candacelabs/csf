# Bazel-derived dis development container

dis reads `.dis/Dockerfile`.

`tools/devcontainer/generate.sh write` asks Bazel to generate `.dis/Dockerfile`.
`tools/devcontainer/generate.sh check` fails if the committed output differs.
Only Docker must be running on the host. The renderer is the native OCaml
`devcontainer_codegen.ml`; Bazel supplies its compiler and executes it with
declared inputs. `tools/bazel.sh test //tools/devcontainer:codegen_test` checks
malformed image pins, runtime preservation and template projection.

`bazel/execution_image.txt` owns the digest-pinned execution image used by both
`tools/bazel.sh` and this Bazel rule. `MODULE.bazel` owns the toolchain and
library dependencies Bazel resolves inside that image. The development image
therefore does not maintain another list of Go, Rust or OCaml versions or
install their compilers independently. Run `bazel build` and `bazel test`
inside the development container; dependency downloads happen on demand.

Bazel's build graph does not describe application runtime configuration such
as database initialization, authentication mounts or forwarded ports. The rule
accepts an explicit `runtime` Dockerfile fragment for those independently
owned requirements. When provided, it retains that runtime's base image and
configuration and copies the pinned Bazel executable from a separate stage.
That variant shares the Bazel version, but its operating-system packages are
owned by the runtime fragment, not inferred from Bazel.

A `template` input instead projects `@BAZEL_EXECUTION_IMAGE@` in an existing
Dockerfile. The docs renderer uses this mode; it retains its own runtime
dependencies and obtains the Bazel stage pin from the same owner.

The canonical monorepo uses this variant for its existing adapter workbench.
Its `.dis/runtime.Dockerfile` retains the direct Go and Node tooling, PostgreSQL,
Copilot CLI, ports and mounts. The generator also projects that file when run
from the monorepo. The exported CSF development container contains only the
Bazel environment and no private runtime configuration.
