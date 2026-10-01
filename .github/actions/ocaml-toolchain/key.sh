#!/usr/bin/env bash
# Hash tracked toolchain identities before any generator edits the checkout.
set -Eeuo pipefail
action_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
module_root=$(cd -- "$action_root/../../.." && pwd -P)
repository_root=$(git -C "$module_root" rev-parse --show-toplevel)
{
  printf 'public-module\0'
  git -C "$module_root" ls-files --stage -z -- \
    MODULE.bazel MODULE.bazel.lock BUILD.bazel .bazelrc .bazelversion .gitattributes \
    tools/bazel.sh csf/compiler/opam.lock.json \
    ':(glob)csf/compiler/third_party/**' ':(glob).github/actions/ocaml-toolchain/**'
  if [[ "$repository_root" != "$module_root" ]]; then
    printf 'owning-module\0'
    git -C "$repository_root" ls-files --stage -z -- \
      MODULE.bazel MODULE.bazel.lock .bazelrc .bazelversion .gitattributes \
      deploy/home/opam.lock.json
  fi
} | sha256sum | cut -d ' ' -f 1
