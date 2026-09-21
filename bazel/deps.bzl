"""Legacy WORKSPACE support for repositories that consume this module.

**bzlmod is how this module is meant to be consumed.** `examples/external-consumer`
shows the two supported shapes — `bazel_dep` + `archive_override`, and the
`use_repo_rule` `http_archive` form — and both are exercised end to end before a
release archive is published. Everything in this file is the second-class path,
kept for a consumer whose own repository has not migrated yet.

It is second-class in a way worth stating plainly: Bazel 9 removed WORKSPACE
entirely, so a WORKSPACE consumer is on Bazel 7.x or 8.x with
`--enable_workspace`, which is *not* the Bazel this module is built and tested
with. Nothing in CI can run a WORKSPACE build, so this macro is maintained by
inspection, not by proof. See `bazel/README.md` for the full consumer recipe,
including the `repo_mapping` this module's BUILD files need and the one
dependency (`pg_query_go`) whose patches bzlmod applies and WORKSPACE does not.

Usage, from the consuming WORKSPACE, after `http_archive`-ing this module:

    load("@candace//bazel:deps.bzl", "candace_dependencies")

    candace_dependencies()
"""

load("@bazel_gazelle//:deps.bzl", "gazelle_dependencies")
load("@io_bazel_rules_go//go:deps.bzl", "go_register_toolchains", "go_rules_dependencies")
load(":versions.bzl", "GO_SDK_VERSION")

def candace_dependencies(go_sdk_version = GO_SDK_VERSION):
    """Set up the Go toolchain this module's targets are built with.

    This is the part a consumer cannot reasonably rederive: which Go SDK the
    committed BUILD files were generated and tested against. It deliberately
    does *not* declare this module's Go dependency closure — `go.mod` and
    `go.sum` in the archive root are the authority for that, and the supported
    way to turn them into `go_repository` rules in a WORKSPACE build is

        gazelle update-repos -from_file=external/candace/go.mod \\
            -to_macro=candace_go_deps.bzl%candace_go_deps -prune

    run in the consuming repository, where the result is that repository's to
    maintain and to patch. Shipping a generated copy here would be a second
    dependency list that no test in this repository can keep honest.

    Args:
      go_sdk_version: Go SDK to download and register. Defaults to the version
        MODULE.bazel pins; override it only to move ahead of this module.
    """
    go_rules_dependencies()
    go_register_toolchains(version = go_sdk_version)
    gazelle_dependencies()
