#!/usr/bin/env bash
# The ontology alignment score: build the pinned native checkers, then print one
# record for a checkout's HEAD, or compare HEAD with its merge base. The OCaml
# house runner owns the procedure (tools/house_lint/score.ml), the score's
# definition and the comparison (alignment.ml); this script only builds,
# stages the base tree and forwards.
set -Eeuo pipefail
checkout=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
root=$checkout
format=json
receipt=
base=
mode=score
accept=false
usage() {
  cat <<'USAGE'
Usage: ontology-score.sh [--root PATH] [--format json|openmetrics] [--receipt DIR]
       ontology-score.sh --ratchet [--base REF] [--accept-regression] [--receipt DIR]
       ontology-score.sh --pr-spec [--base REF]

Prints one ontology alignment record for HEAD; lower score is better.
--receipt DIR      also write the JSON and OpenMetrics forms of the same run there
                   (with --ratchet or --pr-spec: DIR/base and DIR/head).
--ratchet          measure HEAD and its merge base with REF (default origin/main)
                   using this checkout's checkers, print the per-signal table and
                   exit 1 if the comparable penalty or a blocking signal rose.
--accept-regression  with --ratchet: print a regression but exit 0.
--pr-spec          the same two measurements, printed as the pr-description
                   skill's ontology_score JSON field.
USAGE
}
while [[ $# -gt 0 ]]; do
  case "$1" in
    --format) format="${2:?--format requires json or openmetrics}"; shift 2 ;;
    --receipt) receipt="${2:?--receipt requires a directory}"; shift 2 ;;
    --root) root=$(cd -- "${2:?--root requires a path}" && pwd -P); shift 2 ;;
    --base) base="${2:?--base requires a git revision}"; shift 2 ;;
    --ratchet) mode=ratchet; shift ;;
    --pr-spec) mode=pr-spec; shift ;;
    --accept-regression) accept=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) printf 'ontology-score: unknown argument: %s\n' "$1" >&2; exit 2 ;;
  esac
done
case "$format" in json|openmetrics) ;; *) printf 'ontology-score: unknown format: %s\n' "$format" >&2; exit 2 ;; esac
if [[ "$mode" == score && ( -n "$base" || "$accept" == true ) ]]; then
  printf 'ontology-score: --base and --accept-regression need --ratchet or --pr-spec\n' >&2
  exit 2
fi
export CANDACE_BAZEL_WORKSPACE="$checkout"
stage=$(mktemp -d "${TMPDIR:-/tmp}/ontology-score.XXXXXX")
base_tree=
cleanup() {
  if [[ -n "$base_tree" ]]; then git -C "$root" worktree remove --force "$base_tree" >/dev/null 2>&1 || true; fi
  rm -rf -- "$stage"
}
trap cleanup EXIT
# One top-level target per build, copied out immediately: bazel-bin follows the
# last build's configuration, so a later build can repoint it away from an
# earlier target's output (see check-house-lint.sh).
build() {
  local target=$1 output=$2 name=$3
  bash "$checkout/tools/bazel.sh" build "$target" --lockfile_mode=error >&2
  [[ -x "$checkout/bazel-bin/$output" ]] || {
    printf 'ontology-score: the native build produced no %s.\n' "$output" >&2
    exit 2
  }
  install -m 0755 "$checkout/bazel-bin/$output" "$stage/$name"
}
build //tools/house_lint:check tools/house_lint/check.exe house-lint
build //csf/compiler/architecture:csfc csf/compiler/architecture/csfc.exe csfc
build //csf/compiler/language:generate csf/compiler/language/generate.exe csf-language-generate
measure() {  # measure TREE FORMAT [RECEIPT_DIR]
  local command=("$stage/house-lint" --root "$1" --ontology-score
    --csfc "$stage/csfc" --generator "$stage/csf-language-generate" --format "$2")
  if [[ -n "${3:-}" ]]; then mkdir -p -- "$3"; command+=(--receipt "$3"); fi
  "${command[@]}"
}
if [[ "$mode" == score ]]; then
  measure "$root" "$format" "$receipt"
  exit
fi
# Both sides are measured by this checkout's checkers, so the comparison holds
# the method fixed and only the measured tree changes.
merge_base=$(git -C "$root" merge-base "${base:-origin/main}" HEAD)
base_tree="$stage/base"
git -C "$root" worktree add --quiet --detach "$base_tree" "$merge_base" >&2
measure "$base_tree" json "${receipt:+$receipt/base}" > "$stage/base.json"
measure "$root" json "${receipt:+$receipt/head}" > "$stage/head.json"
if [[ "$mode" == pr-spec ]]; then
  "$stage/house-lint" --ontology-compare "$stage/base.json" "$stage/head.json" --format pr-spec
else
  compare=("$stage/house-lint" --ontology-compare "$stage/base.json" "$stage/head.json" --format markdown)
  if [[ "$accept" == true ]]; then compare+=(--accept-regression); fi
  "${compare[@]}"
fi
