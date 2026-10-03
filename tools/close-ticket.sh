#!/usr/bin/env bash
# Closes a ticket from the pull request that delivered it. The close comment
# is rendered from the pull request body's typed sections (the **Verdict:**
# line and the `| Backtest ... |` rows a miner's backtest block renders) and
# the merge run's ratchet tables, read on stdin from tools/merge-pr.sh:
#
#   tools/merge-pr.sh 31 2>&1 | tee merge.log
#   tools/close-ticket.sh 12 31 < merge.log
#
# Tickets and pull requests both live in the repository origin points at
# (candacelabs/csf_staging), so both are numbers there. The ticket is closed (gh issue close --comment) only when the pull request
# is merged, its `| Backtest FN |` row reads 0 and the merge run printed
# `check-merge: passed`. Otherwise one comment names the failing row and the
# ticket stays open (exit 1). Idempotent: a closed ticket, or one that already
# carries the same comment, is left alone. --dry-run prints the decision and
# the comment and writes nothing.
#
# Usage: tools/close-ticket.sh [--dry-run] <ticket number> <pull request number> < merge-log
set -Eeuo pipefail
die() { printf 'close-ticket: %s\n' "$*" >&2; exit 2; }
dry_run=false
if [[ "${1:-}" == --dry-run ]]; then dry_run=true; shift; fi
[[ $# -eq 2 && "$1" =~ ^[0-9]+$ && "$2" =~ ^[0-9]+$ ]] ||
  die 'usage: close-ticket.sh [--dry-run] <ticket number> <pull request number> < merge-log'
ticket=$1
number=$2
checkout=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd -P)
# gh is told the repository origin points at, never one resolved from a clone's
# upstream default.
repository=$(git -C "$checkout" remote get-url origin)
log=$(cat)

pull=$(gh pr view "$number" --repo "$repository" --json url,state,body,headRefOid,mergeCommit)
url=$(jq -r .url <<<"$pull")
state=$(jq -r .state <<<"$pull")
body=$(jq -r .body <<<"$pull")
head=$(jq -r .headRefOid <<<"$pull")
merge=$(jq -r '.mergeCommit.oid // "none"' <<<"$pull")

verdict=$(grep -m1 '^\*\*Verdict:\*\*' <<<"$body" || true)
backtest=$(grep '^| Backtest ' <<<"$body" || true)
fn=$(sed -n 's/^| Backtest FN | \([0-9][0-9]*\).*/\1/p' <<<"$backtest" | head -n 1)
# The two ratchet tables check-merge prints: the ontology score's per-signal
# table and the house lint's per-rule Base/Head table, a blank line apart.
ratchet=$(awk '/^\| (Signal|Rule \| Base) \|/ { table = 1; if (seen++) print "" } table && /^\|/ { print; next } { table = 0 }' <<<"$log")

failing=
if [[ "$state" != MERGED ]]; then
  failing="pull request $url is $state, not merged"
elif [[ -z "$fn" ]]; then
  failing='the pull request body has no `| Backtest FN |` row'
elif [[ "$fn" != 0 ]]; then
  failing=$(grep -m1 '^| Backtest FN ' <<<"$backtest")
elif ! grep -qx 'check-merge: passed' <<<"$log"; then
  failing=$(grep -m1 '^check-merge: ' <<<"$log" || printf 'the merge log on stdin has no check-merge result')
fi

comment=$(
  printf '<!-- close-ticket pr=%s head=%s -->\n' "$url" "$head"
  printf '**Delivered by** %s (head `%s`, merge `%s`)\n\n' "$url" "${head:0:12}" "${merge:0:12}"
  if [[ -n "$verdict" ]]; then printf '%s\n\n' "$verdict"; fi
  printf '### Backtest\n\n'
  if [[ -n "$backtest" ]]; then printf '| What we measured | Result |\n|---|---|\n%s\n\n' "$backtest"; else printf 'None in the pull request body.\n\n'; fi
  printf '### Merge ratchet\n\n'
  if [[ -n "$ratchet" ]]; then printf '%s\n' "$ratchet"; else printf 'None in the merge log.\n'; fi
  if [[ -n "$failing" ]]; then printf '\n**Not closed; failing row:** %s\n' "$failing"; fi
)

issue=$(gh issue view "$ticket" --repo "$repository" --json state,comments)
if [[ $(jq -r .state <<<"$issue") == CLOSED ]]; then
  printf 'close-ticket: #%s is already closed\n' "$ticket"
  exit 0
fi
if jq -e --arg body "$comment" 'any(.comments[]; .body == $body)' <<<"$issue" >/dev/null; then
  printf 'close-ticket: #%s already carries this comment\n' "$ticket"
  [[ -z "$failing" ]] || exit 1
  [[ "$dry_run" == true ]] || gh issue close "$ticket" --repo "$repository"
  exit 0
fi

if [[ "$dry_run" == true ]]; then
  printf 'close-ticket: would %s #%s\n\n%s\n' "$([[ -z "$failing" ]] && printf close || printf comment)" "$ticket" "$comment"
  [[ -z "$failing" ]] || exit 1
  exit 0
fi
if [[ -z "$failing" ]]; then
  gh issue close "$ticket" --repo "$repository" --comment "$comment"
  exit 0
fi
gh issue comment "$ticket" --repo "$repository" --body "$comment"
printf 'close-ticket: not closed: %s\n' "$failing" >&2
exit 1
