#!/usr/bin/env bash
# Fail when the committed Bazel metadata has drifted from the source it
# describes.
#
# BUILD files here are generated, not written: Gazelle owns them, and
# MODULE.bazel's use_repo list mirrors go.mod. Committing generated files is
# only safe with a gate that notices when they stop matching, so this runs both
# generators and fails if either would change anything.
#
# Usage: tools/check-bazel-metadata.sh
set -Eeuo pipefail

module_root=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
bazel="$module_root/tools/bazel.sh"

die() {
  printf 'candace bazel metadata: %s\n' "$*" >&2
  exit 1
}

command -v git >/dev/null 2>&1 || die 'git is required'
[[ -x "$bazel" ]] || die "the Bazel wrapper is not executable: $bazel"

# bzlmod is the only dependency mechanism here, and the pinned Bazel 9 would not
# read a WORKSPACE file even if one appeared. One appearing anyway is the signal
# that somebody is building this module with an older Bazel and did not say so;
# bazel/README.md explains where the legacy path actually lives instead.
printf 'candace bazel metadata: checking that no WORKSPACE file was reintroduced\n' >&2
for workspace_file in WORKSPACE WORKSPACE.bazel WORKSPACE.bzlmod; do
  [[ ! -e "$module_root/$workspace_file" ]] || \
    die "$workspace_file exists. bzlmod is the only dependency mechanism here; the legacy consumer path is bazel/deps.bzl."
done

# `bazel mod tidy` rewrites MODULE.bazel in place rather than reporting, so the
# check is: let it write, then ask git whether it had anything to say.
printf 'candace bazel metadata: reconciling MODULE.bazel with go.mod\n' >&2
"$bazel" mod tidy

if ! git -C "$module_root" diff --exit-code -- MODULE.bazel MODULE.bazel.lock; then
  die "MODULE.bazel is out of date. \`tools/bazel.sh mod tidy\` produced the diff above; commit it."
fi

printf 'candace bazel metadata: checking the generated BUILD files\n' >&2
if ! "$bazel" run //:gazelle -- -mode=diff; then
  die "the BUILD files are out of date. Run \`tools/bazel.sh run //:gazelle\` and commit the result."
fi

printf 'candace bazel metadata: clean\n' >&2
