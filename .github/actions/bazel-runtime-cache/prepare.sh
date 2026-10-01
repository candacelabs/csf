#!/usr/bin/env bash
# These are preparation targets, not an inventory or a substitute for tests.
# The complete partition gate still builds every target and runs every test.
set -Eeuo pipefail
action_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
module_root=$(cd -- "$action_root/../../.." && pwd -P)
export CANDACE_BAZEL_WORKSPACE="$module_root"
case "${1:-}" in
  go)
    # Gazelle also warms Go's execution-tool configuration of the standard library.
    targets=(@rules_go//:stdlib //:gazelle)
    ;;
  rust-base)
    # A real crate with a build script compiles Cargo's helpers in the same
    # execution configuration that the complete Rust partition consumes.
    targets=(@crates//:tempfile)
    ;;
  rust-network)
    targets=(@crates//:reqwest)
    ;;
  rust-support)
    targets=(@crates//:rusqlite @crates//:protox @crates//:clap)
    ;;
  rust-deps)
    targets=(@crates//:xet-client @crates//:xet-data)
    ;;
  *) printf 'Unknown Bazel preparation stage: %s\n' "${1:-}" >&2; exit 2 ;;
esac
exec bash "$module_root/tools/bazel.sh" --batch build \
  --ignore_dev_dependency --lockfile_mode=error -- "${targets[@]}"
