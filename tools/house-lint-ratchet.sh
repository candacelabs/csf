#!/usr/bin/env bash
# House lint as a ratchet: the tree at --root may not have more blocking
# findings than its merge base with --base, rule by rule, both scanned by this
# checkout's checker so only the tree differs. A blocking finding is one
# Policy.blocks counts: every finding of a mandatory rule, and a per-owner
# rule's findings inside the owners it is mandatory in. Mandatory specialists
# report clean or findings, counted as 0 or 1. Prints the per-rule table.
#
#   house-lint-ratchet.sh [--root PATH] [--base REF]
#   house-lint-ratchet.sh --count SUMMARY NATIVE_LOG   one report's counts
#
# Exit 0: no rule rose. 1: a rule rose. 2: a scan could not complete.
set -Eeuo pipefail
checkout=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
die() { printf 'house-lint-ratchet: %s\n' "$*" >&2; exit 2; }

# count SUMMARY NATIVE_LOG: one "RULE COUNT" line per rule that can block.
count() {
  local summary=$1 native=$2 rule policy result owners prefix total
  [[ -f "$summary" && -f "$native" ]] || die "missing report: $summary or $native"
  while IFS='|' read -r _ rule policy result _; do
    rule=${rule// /} policy=${policy# } policy=${policy% } result=${result# } result=${result% }
    case "$policy" in
      mandatory)
        case "$result" in
          *"SCAN ERROR"*|"not run"*) die "$rule did not run: $result" ;;
          *": clean") printf '%s 0\n' "$rule" ;;
          *": findings") printf '%s 1\n' "$rule" ;;
          *) [[ "$result" =~ ^[0-9]+$ ]] || die "$rule: unreadable result '$result'"
             printf '%s %s\n' "$rule" "$result" ;;
        esac ;;
      "advisory; mandatory in "*)
        owners=${policy#advisory; mandatory in } total=0
        IFS=',' read -ra prefixes <<<"$owners"
        for prefix in "${prefixes[@]}"; do
          prefix=${prefix# }
          total=$(( total + $(awk -v rule="$rule" -v prefix="$prefix" -F': ' \
            '$2 == rule && index($1, prefix) == 1 { n++ } END { print n + 0 }' "$native") ))
        done
        printf '%s %s\n' "$rule" "$total" ;;
    esac
  done < <(grep -E '^\| [A-Z0-9-]+ \| (mandatory|advisory; mandatory in )' "$summary")
}

if [[ "${1:-}" == --count ]]; then
  [[ $# -eq 3 ]] || die 'usage: house-lint-ratchet.sh --count SUMMARY NATIVE_LOG'
  count "$2" "$3"
  exit 0
fi

root=$checkout base=origin/main
while [[ $# -gt 0 ]]; do
  case "$1" in
    --root) root=$(cd -- "${2:?--root requires a path}" && pwd -P); shift 2 ;;
    --base) base="${2:?--base requires a git revision}"; shift 2 ;;
    *) die "unknown argument: $1" ;;
  esac
done
stage=$(mktemp -d "${TMPDIR:-/tmp}/house-lint-ratchet.XXXXXX")
base_tree="$stage/base"
cleanup() {
  git -C "$root" worktree remove --force "$base_tree" >/dev/null 2>&1 || true
  rm -rf -- "$stage"
}
trap cleanup EXIT
merge_base=$(git -C "$root" merge-base "$base" HEAD) || die "no merge base between $base and HEAD"
git -C "$root" worktree add --quiet --detach "$base_tree" "$merge_base" >&2

scan() {  # scan TREE NAME: house lint report-only, then its counts
  bash "$checkout/tools/check-house-lint.sh" --root "$1" --report-only --quiet --summary "$stage/$2.md" >&2 ||
    die "house lint could not complete on the $2 tree"
  count "$stage/$2.md" "$1/house-lint-output/native.log" > "$stage/$2.counts"
}
scan "$base_tree" base
scan "$root" head

rose=0
printf '| Rule | Base | Head | Delta |\n|---|---|---|---|\n'
while read -r rule after; do
  before=$(awk -v rule="$rule" '$1 == rule { print $2 }' "$stage/base.counts")
  before=${before:-0}
  if (( after > before )); then rose=1; fi
  if (( before > 0 || after > 0 )); then
    printf '| %s | %d | %d | %+d |\n' "$rule" "$before" "$after" "$(( after - before ))"
  fi
done < "$stage/head.counts"
if (( rose )); then
  printf '\nResult: a blocking rule rose over %s (%s).\n' "$base" "${merge_base:0:12}"
  exit 1
fi
printf '\nResult: no blocking rule rose over %s (%s).\n' "$base" "${merge_base:0:12}"
