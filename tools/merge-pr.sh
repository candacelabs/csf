#!/usr/bin/env bash
# The one path a pull request takes into main. The repository has no branch
# protection, so this is the gate: the pull request's head is merged with
# origin/main locally, tools/check-merge.sh runs on that merge with this
# checkout's checkers, and only a passing merge is squash-merged through gh,
# pinned to the head that was checked.
#
# Usage: tools/merge-pr.sh <pull request number>
set -Eeuo pipefail
die() { printf 'merge-pr: %s\n' "$*" >&2; exit "${2:-2}"; }
[[ $# -eq 1 && "$1" =~ ^[0-9]+$ ]] || die 'usage: merge-pr.sh <pull request number>'
number=$1
checkout=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
# gh is told the repository origin points at, never one resolved from a clone's
# upstream default.
repository=$(git -C "$checkout" remote get-url origin)
git -C "$checkout" fetch --quiet origin main
git -C "$checkout" fetch --quiet origin "pull/$number/head"
head=$(git -C "$checkout" rev-parse FETCH_HEAD)
stage=$(mktemp -d "${TMPDIR:-/tmp}/merge-pr.XXXXXX")
tree="$stage/merge"
cleanup() {
  git -C "$checkout" worktree remove --force "$tree" >/dev/null 2>&1 || true
  rm -rf -- "$stage"
}
trap cleanup EXIT
git -C "$checkout" worktree add --quiet --detach "$tree" origin/main
git -C "$tree" -c user.name=merge-pr -c user.email=merge-pr@localhost \
  merge --quiet --no-edit "$head" || die "pull request #$number does not merge cleanly with origin/main; rebase it" 1
printf 'merge-pr: checking #%s (head %s) merged with origin/main\n' "$number" "${head:0:12}"
bash "$checkout/tools/check-merge.sh" --root "$tree" --base origin/main ||
  die "pull request #$number is refused; see the report above" 1
gh pr merge "$number" --repo "$repository" --squash --match-head-commit "$head"
