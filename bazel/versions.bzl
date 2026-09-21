"""Toolchain versions this module is built with, readable from Starlark.

`MODULE.bazel` is the authority for every pin here, and `.bazelversion` is the
authority for the Bazel release. Neither can be `load()`ed — a module file may
not load a `.bzl` file, and `.bazelversion` is a plain text file — so a legacy
WORKSPACE consumer that wants the same toolchain has nowhere to read them from.

These constants exist for that consumer alone. Nothing inside this module reads
them; `//bazel:version_pins_test` fails if any of them stops matching the file
that owns it, so this stays a mirror rather than a second opinion.
"""

# The Bazel release in .bazelversion.
BAZEL_VERSION = "9.2.0"

# The `bazel_dep` versions in MODULE.bazel. A WORKSPACE consumer declares
# rules_go and Gazelle itself; these say which releases this module's committed
# BUILD files were generated against and are known to build with.
RULES_GO_VERSION = "0.62.0"
GAZELLE_VERSION = "0.52.2"

# The Go SDK `go_sdk.download` names in MODULE.bazel. rules_go downloads this
# rather than taking Go from the host, in bzlmod and in WORKSPACE alike.
GO_SDK_VERSION = "1.26.5"
