#!/usr/bin/env bash
set -Eeuo pipefail
action_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
module_root=$(cd -- "$action_root/../../.." && pwd -P)
git -C "$module_root" ls-files --stage -z -- \
  MODULE.bazel MODULE.bazel.lock BUILD.bazel .bazelrc .bazelversion \
  go.mod go.sum tools/bazel.sh xetcas/Cargo.lock xetcas/Cargo.toml \
  ':(glob)xetcas/**/Cargo.toml' ':(glob)third_party/**' \
  ':(glob).github/actions/bazel-runtime-cache/**' \
  | sha256sum | cut -d ' ' -f 1
