#!/usr/bin/env bash
set -euo pipefail

readonly source_threshold=100
readonly all_threshold=200

if ! command -v dupl >/dev/null 2>&1; then
  echo "dupl is required; tools/house_lint/runner.ml pins the version the house runner installs" >&2
  exit 2
fi

repo_root="$(git rev-parse --show-toplevel)"
cd "$repo_root"

reuse_tmp="$(mktemp -d "${TMPDIR:-/tmp}/candace-go-reuse.XXXXXX")"
trap 'rm -rf -- "$reuse_tmp"' EXIT

all_files="$reuse_tmp/all-files"
source_files="$reuse_tmp/source-files"
: >"$all_files"
: >"$source_files"

while IFS= read -r -d '' file; do
  case "$file" in
    research/* | vendor/* | */vendor/*) continue ;;
  esac

  case "$file" in
    pkg/gotth/bench/* | examples/gotth/* | pkg/gotth/docs/guide/_samples/*) continue ;;
  esac

  if grep -Eq '^// Code generated .* DO NOT EDIT\.$' "$file"; then
    continue
  fi

  printf '%s\n' "$file" >>"$all_files"
  case "$file" in
    *_test.go) ;;
    *) printf '%s\n' "$file" >>"$source_files" ;;
  esac
done < <(git ls-files -z -- '*.go')

if [[ ! -s "$source_files" || ! -s "$all_files" ]]; then
  echo "Go reuse check selected no files; refusing a vacuous pass" >&2
  exit 2
fi

run_check() {
  local label="$1"
  local threshold="$2"
  local files="$3"
  local report="$4"
  local count

  count="$(wc -l <"$files")"
  if ! dupl -plumbing -threshold "$threshold" -files <"$files" >"$report"; then
    echo "dupl failed during the $label scan" >&2
    return 2
  fi

  if [[ -s "$report" ]]; then
    echo "Duplicated Go detected by the $label scan (threshold: $threshold tokens):" >&2
    cat "$report" >&2
    echo "Centralize the shared primitive, or narrow an intentional self-contained example outside this gate." >&2
    return 1
  fi

  echo "Go reuse check passed: $label ($count files, $threshold-token threshold)"
}

status=0
run_check "production source" "$source_threshold" "$source_files" "$reuse_tmp/source-report" || status=$?
all_status=0
run_check "all handwritten code" "$all_threshold" "$all_files" "$reuse_tmp/all-report" || all_status=$?
if ((all_status > status)); then status=$all_status; fi
exit "$status"
