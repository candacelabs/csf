#!/usr/bin/env bash
# The consistency checks a change must pass before it reaches main: no
# consistency regression, checked with this checkout's tools against the tree
# at --root (default: this checkout). tools/merge-pr.sh runs them on the PR
# merged with main; the harness session gate runs them before a pull request
# is marked ready.
#
#   1. generated       tools/check-generated.sh: one ontology source, no
#                     orphan under docs/generated/, no drift after
#                     regenerating everything;
#   2. ontology score  --ratchet against --base: no rise in the penalty or in
#                     a blocking signal (prints the per-signal table);
#   3. house lint      tools/house-lint-ratchet.sh against --base: no
#                     blocking rule gains a finding (prints the per-rule
#                     table). A ratchet, like the score: a regression that
#                     reached main before this gate existed blocks only the
#                     change that worsens it, not every later change.
#
# Every check runs even after one fails, so one report names every failure.
# Exit 0 passes, 1 is a regression, 2 a check that could not run.
set -Eeuo pipefail
checkout=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
root=$checkout
base=origin/main
die() { printf 'check-merge: %s\n' "$*" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --root) root=$(cd -- "${2:?--root requires a path}" && pwd -P); shift 2 ;;
    --base) base="${2:?--base requires a git revision}"; shift 2 ;;
    -h|--help) printf 'Usage: check-merge.sh [--root PATH] [--base REF]\n'; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done
[[ -z "$(git -C "$root" status --porcelain)" ]] || die "$root has uncommitted changes; commit them so the checks see one revision"
export CANDACE_BAZEL_WORKSPACE="$checkout"
stage=$(mktemp -d "${TMPDIR:-/tmp}/check-merge.XXXXXX")
trap 'rm -rf -- "$stage"' EXIT
failed=()

bash "$checkout/tools/check-generated.sh" --root "$root" || failed+=(generated)

printf '== ontology score ratchet against %s\n' "$base"
bash "$checkout/tools/ontology-score.sh" --root "$root" --ratchet --base "$base" || failed+=(ontology-ratchet)

printf '== house lint ratchet against %s\n' "$base"
bash "$checkout/tools/house-lint-ratchet.sh" --root "$root" --base "$base" || failed+=(house-lint)

if [[ ${#failed[@]} -gt 0 ]]; then
  printf 'check-merge: REFUSED: %s\n' "${failed[*]}" >&2
  exit 1
fi
printf 'check-merge: passed\n'
