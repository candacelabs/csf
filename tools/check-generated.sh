#!/usr/bin/env bash
# Every generated file has a generator, and every generator's output is
# current. Checked with this checkout's generators against the tree at --root
# (default: this checkout), which must have no uncommitted changes.
#
#   single source  the ontology is declared once, in the language generator's
#                  input; no other tracked .csf declares a term;
#   orphans        every tracked file under a docs/generated/ directory is
#                  deleted, every generator runs, and a file no generator
#                  wrote back is an orphan (a copy nothing keeps current);
#   drift          after regeneration `git diff --exit-code` is clean.
#
# The tree is restored to its revision on exit. Exit 0 passes, 1 is a
# finding, 2 a check that could not run.
set -Eeuo pipefail
checkout=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
root=$checkout
die() { printf 'check-generated: %s\n' "$*" >&2; exit 2; }
while [[ $# -gt 0 ]]; do
  case "$1" in
    --root) root=$(cd -- "${2:?--root requires a path}" && pwd -P); shift 2 ;;
    -h|--help) printf 'Usage: check-generated.sh [--root PATH]\n'; exit 0 ;;
    *) die "unknown argument: $1" ;;
  esac
done
[[ -z "$(git -C "$root" status --porcelain)" ]] || die "$root has uncommitted changes; commit them so the check sees one revision"
ontology_source=csf/compiler/language/architecture.csf
export CANDACE_BAZEL_WORKSPACE="$checkout"
stage=$(mktemp -d "${TMPDIR:-/tmp}/check-generated.XXXXXX")
restore() {
  git -C "$root" checkout --quiet -- . 2>/dev/null || true
  git -C "$root" clean --quiet -fd 2>/dev/null || true
  rm -rf -- "$stage"
}
trap restore EXIT
findings=0

printf '== single ontology source (%s)\n' "$ontology_source"
mapfile -t declaring < <(git -C "$root" grep -lE '^[[:space:]]*term [a-z_]+ "' -- '*.csf' ":!$ontology_source" || true)
if [[ ! -f "$root/$ontology_source" ]]; then
  printf 'the ontology source %s is missing\n' "$ontology_source"; findings=1
elif [[ ${#declaring[@]} -gt 0 ]]; then
  printf 'declares ontology terms outside the source; generate it or delete it: %s\n' "${declaring[@]}"; findings=1
fi

build() {  # build TARGET OUTPUT NAME, copied out before the next build repoints bazel-bin
  bash "$checkout/tools/bazel.sh" build "$1" --lockfile_mode=error >&2
  [[ -x "$checkout/bazel-bin/$2" ]] || die "the native build produced no $2"
  install -m 0755 "$checkout/bazel-bin/$2" "$stage/$3"
}
build //csf/compiler/language:generate csf/compiler/language/generate.exe generate
build //csf/compiler/architecture:csfc csf/compiler/architecture/csfc.exe csfc

printf '== orphans and drift\n'
mapfile -d '' -t owned < <(git -C "$root" ls-files -z -- ':(glob)docs/generated/**' ':(glob)**/docs/generated/**')
for path in "${owned[@]}"; do rm -f -- "$root/$path"; done
"$stage/generate" --root "$root" write || findings=1
# csfc runs in the repository root with its default inputs, as score.ml does.
(cd -- "$root" && "$stage/csfc" emit) || findings=1
mapfile -t orphans < <(git -C "$root" ls-files --deleted -- ':(glob)docs/generated/**' ':(glob)**/docs/generated/**')
if [[ ${#orphans[@]} -gt 0 ]]; then
  printf 'orphan: no generator writes %s; delete it or make a generator own it\n' "${orphans[@]}"
  findings=1
fi
mapfile -t drifted < <(git -C "$root" ls-files --modified --others --exclude-standard | grep -vxF -f <(printf '%s\n' "${orphans[@]}" ) || true)
if [[ ${#drifted[@]} -gt 0 ]]; then
  printf 'drift: regeneration changed %s; commit the regenerated file with its source\n' "${drifted[@]}"
  findings=1
fi

if [[ "$findings" -ne 0 ]]; then
  printf 'check-generated: FAILED\n' >&2
  exit 1
fi
printf 'check-generated: passed\n'
