#!/usr/bin/env bash
# Generate from Bazel inputs, using the same Docker-backed launcher as builds.
set -Eeuo pipefail
module_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/../.." && pwd -P)
mode=${1:-check}
case "$mode" in
  write|check) ;;
  *) printf 'usage: %s [write|check]\n' "$0" >&2; exit 2 ;;
esac
project() {
  local workspace=$1 target=$2 generated=$3 destination=$4
  CANDACE_BAZEL_WORKSPACE="$workspace" "$module_root/tools/bazel.sh" \
    --batch build "$target" --lockfile_mode=error
  if [[ "$mode" == write ]]; then
    install -m 0644 "$workspace/bazel-bin/$generated" "$destination"
  else
    diff -u "$destination" "$workspace/bazel-bin/$generated"
  fi
}
project "$module_root" //tools/devcontainer:dockerfile \
  tools/devcontainer/dockerfile.Dockerfile "$module_root/.dis/Dockerfile"
monorepo_root=$(dirname -- "$module_root")
if [[ -f "$monorepo_root/.dis/runtime.Dockerfile" && -f "$monorepo_root/MODULE.bazel" ]]; then
  project "$monorepo_root" //.dis:dockerfile \
    .dis/dockerfile.Dockerfile "$monorepo_root/.dis/Dockerfile"
fi
