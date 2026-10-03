#!/usr/bin/env bash
# One pinned build, one OCaml rule registry, one accumulated CI verdict.
set -Eeuo pipefail
root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
run_tests=false
build_only=false
arguments=()
while [[ $# -gt 0 ]]; do
  case "$1" in
    --test) run_tests=true; shift ;;
    --build-only) build_only=true; shift ;;
    --root|--summary) arguments+=("$1" "${2:?option requires a path}"); shift 2 ;;
    --native-only|--report-only|--quiet) arguments+=("$1"); shift ;;
    -h|--help)
      printf 'Usage: check-house-lint.sh [--test] [--build-only] [--root PATH] [--summary PATH] [--native-only] [--report-only] [--quiet]\n'
      exit 0 ;;
    *) printf 'house-lint: unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done
export CANDACE_BAZEL_WORKSPACE="$root"
if "$run_tests"; then
  bash "$root/tools/bazel.sh" test //tools/house_lint:all \
    //tools/gorilla_mux_lint:checker_test \
    --test_output=errors --lockfile_mode=error >&2
fi
# Build the binary as the sole top-level target so Bazel's convenience link
# follows its OCaml transition instead of the tests' default configuration.
bash "$root/tools/bazel.sh" build //tools/house_lint:check --lockfile_mode=error >&2
[[ -x "$root/bazel-bin/tools/house_lint/check.exe" ]] || {
  printf 'house-lint: the native build produced no checker at the expected output path.\n' >&2
  exit 2
}
if "$build_only"; then exit 0; fi
exec "$root/bazel-bin/tools/house_lint/check.exe" --root "$root" "${arguments[@]}"
