#!/usr/bin/env bash
# Run Bazel against this module inside the pinned Bazel container.
#
# There is no host Bazel and no host Go anywhere in this project's toolchain
# story: bazel/execution_image.txt pins the image digest and .bazelversion
# pins Bazel. The Go SDK is downloaded by rules_go (see MODULE.bazel).
# That makes `tools/bazel.sh build //...` mean the same thing on a developer's
# machine and on a CI runner.
#
# Bazel's output base lives outside the checkout so a build never leaves
# anything in the worktree except the bazel-* convenience symlinks, which
# .gitignore covers. Set CANDACE_BAZEL_CACHE to move it.
# Set CANDACE_BAZEL_WORKSPACE to build another workspace with the same launcher.
# Set CANDACE_BAZEL_DISK_CACHE to pass a mounted disk-cache path to build, test,
# and run commands. Startup flags and other commands are forwarded unchanged.
# Set CANDACE_OCAML_TOOLCHAIN_CACHE to reuse a separately validated OCaml
# installation at a stable container path across independent output bases.
#
# Usage: tools/bazel.sh <bazel arguments...>
set -Eeuo pipefail

die() {
  printf 'candace bazel: %s\n' "$*" >&2
  exit 1
}

command -v docker >/dev/null 2>&1 || die 'docker is required to run the pinned Bazel image'
[[ $# -gt 0 ]] || die 'no Bazel arguments were given'

module_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
bazel_image=$(<"$module_root/bazel/execution_image.txt")
workspace_root=$(cd -- "${CANDACE_BAZEL_WORKSPACE:-$module_root}" && pwd -P)
[[ -f "$workspace_root/MODULE.bazel" ]] || die 'workspace requires MODULE.bazel'
# Bazel keys output bases by the container workspace path. Each selected host
# workspace needs a distinct identity, or a second package replaces the first
# package's bazel-bin outputs. Keep the shared download cache.
workspace_mount=/candace
if [[ "$workspace_root" != "$module_root" ]]; then
  workspace_mount="/workspace/$(printf '%s' "$workspace_root" | sha256sum | cut -c1-16)"
fi
cache_root=${CANDACE_BAZEL_CACHE:-${TMPDIR:-/tmp}/candace-bazel-cache}
mkdir -p -- "$cache_root/home" "$cache_root/output"
# Host-visible output paths make the selected workspace's bazel-bin links
# usable after the container exits, including from a standalone module checkout.
output_root=$(cd -- "$cache_root/output" && pwd -P)

toolchain_mount=()
if [[ -n "${CANDACE_OCAML_TOOLCHAIN_CACHE:-}" ]]; then
  mkdir -p -- "$CANDACE_OCAML_TOOLCHAIN_CACHE"
  toolchain_root=$(cd -- "$CANDACE_OCAML_TOOLCHAIN_CACHE" && pwd -P)
  toolchain_mount=(
    --volume "$toolchain_root:/csf-ocaml-toolchain"
    --env CSF_OCAML_TOOLCHAIN_ROOT=/csf-ocaml-toolchain
  )
fi

bazel_arguments=("$@")
if [[ -n "${CANDACE_BAZEL_DISK_CACHE:-}" ]]; then
  for ((argument_index = 0; argument_index < ${#bazel_arguments[@]}; argument_index++)); do
    case "${bazel_arguments[$argument_index]}" in
      analyze-profile|aquery|canonicalize-flags|clean|config|coverage|cquery|dump|fetch|help|info|license|mobile-install|mod|print_action|query|shutdown|sync|version)
        break
        ;;
      build|test|run)
        before_command=("${bazel_arguments[@]:0:$((argument_index + 1))}")
        after_command=("${bazel_arguments[@]:$((argument_index + 1))}")
        bazel_arguments=(
          "${before_command[@]}"
          "--disk_cache=${CANDACE_BAZEL_DISK_CACHE}"
          "${after_command[@]}"
        )
        break
        ;;
    esac
  done
fi

exec docker run --rm \
  --network "${CANDACE_BAZEL_NETWORK:-default}" \
  --user "$(id -u):$(id -g)" \
  --env HOME=/bazel-home \
  --env USER="${USER:-bazel}" \
  "${toolchain_mount[@]}" \
  --volume "$cache_root/home:/bazel-home" \
  --volume "$cache_root/output:$output_root" \
  --volume "$cache_root/output:/bazel-output" \
  --volume "$workspace_root:$workspace_mount" \
  --workdir "$workspace_mount" \
  --entrypoint /usr/local/bin/bazel \
  "$bazel_image" \
  --output_user_root="$output_root" "${bazel_arguments[@]}"
