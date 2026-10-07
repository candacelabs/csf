#!/bin/bash
set -Eeuo pipefail

# migrate-issues.sh: scriptable, idempotent issue migrator
# Usage: migrate-issues.sh --from OWNER/REPO --to OWNER/REPO [--filter REGEX] [--exclude REGEX] [--dry-run] [--pretty]

from_repo=""
to_repo=""
filter_pattern=""
exclude_pattern=""
dry_run=0
pretty=0

while [[ $# -gt 0 ]]; do
  case $1 in
    --from) from_repo="$2"; shift 2 ;;
    --to) to_repo="$2"; shift 2 ;;
    --filter) filter_pattern="$2"; shift 2 ;;
    --exclude) exclude_pattern="$2"; shift 2 ;;
    --dry-run) dry_run=1; shift ;;
    --pretty) pretty=1; shift ;;
    *) printf 'Unknown option: %s\n' "$1" >&2; exit 1 ;;
  esac
done

[[ -z "$from_repo" || -z "$to_repo" ]] && {
  printf 'Usage: migrate-issues.sh --from OWNER/REPO --to OWNER/REPO [--filter REGEX] [--exclude REGEX] [--dry-run] [--pretty]\n' >&2
  exit 1
}

mapping_file=$(mktemp)
temp_body_file=$(mktemp)
trap 'rm -f "$mapping_file" "$temp_body_file"' EXIT

last_api_call=0

rate_limit() {
  local now elapsed
  now=$(date +%s)
  elapsed=$((now - last_api_call))
  [[ $elapsed -lt 1 ]] && sleep $((1 - elapsed))
  last_api_call=$(date +%s)
}

is_issue_migrated() {
  local repo="$1" source_issue_num="$2"
  gh issue list --repo "$repo" --state all --limit 2000 --json number,body --jq ".[] | select(.body | contains(\"Migrated from the source repository, issue #${source_issue_num};\")) | .number" 2>/dev/null | head -1 || true
}

create_destination_issue() {
  local source_repo="$1" dest_repo="$2" source_issue_num="$3" title="$4" body="$5" labels="$6"
  local current_time
  current_time=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

  {
    printf 'Migrated from the source repository, issue #%d; comments there up to %s are the record.\n\n' "$source_issue_num" "$current_time"
    printf '%s\n' "$body"
  } > "$temp_body_file"

  local comments_section
  comments_section=$(gh issue view "$source_issue_num" --repo "$source_repo" --json comments 2>/dev/null | jq -r '.comments[] | "\n**@\(.author.login)** at \(.createdAt):\n\n\(.body)"' || true)

  [[ -n "$comments_section" ]] && {
    {
      printf '\n---\n\n## Comments from source issue:\n'
      printf '%s\n' "$comments_section"
    } >> "$temp_body_file"
  }

  local label_args=""
  if [[ -n "$labels" ]]; then
    if ! grep -q "backlog" <<< "$labels"; then
      label_args="--label ${labels},backlog"
    else
      label_args="--label ${labels}"
    fi
  else
    label_args="--label backlog"
  fi

  gh issue create --repo "$dest_repo" --title "$title" --body-file "$temp_body_file" $label_args --json number --jq '.number'
}

comment_on_source_issue() {
  gh issue comment "$1" --repo "$2" --body "Moved to $3"
}

close_source_issue() {
  gh issue close "$1" --repo "$2"
}

printf 'old,new,title\n' > "$mapping_file"

rate_limit
issues=$(gh issue list --repo "$from_repo" --state open --limit 2000 --json number,title,body,labels)

issue_count=0
migrated_count=0

while IFS= read -r issue_json; do
  [[ -z "$issue_json" ]] && continue

  issue_count=$((issue_count + 1))

  issue_num=$(printf '%s\n' "$issue_json" | jq -r '.number')
  title=$(printf '%s\n' "$issue_json" | jq -r '.title')
  body=$(printf '%s\n' "$issue_json" | jq -r '.body // ""')
  labels=$(printf '%s\n' "$issue_json" | jq -r '.labels | map(.name) | join(",")')
  [[ "$labels" == "null" ]] && labels=""

  [[ -n "$filter_pattern" ]] && ! grep -qEi "$filter_pattern" <<< "$title" && continue
  [[ -n "$exclude_pattern" ]] && grep -qEi "$exclude_pattern" <<< "$title" && continue

  migrated_count=$((migrated_count + 1))

  rate_limit

  existing_dest_num=$(is_issue_migrated "$to_repo" "$issue_num")

  if [[ -n "$existing_dest_num" ]]; then
    label_str=$(printf '%s\n' "$title" | sed 's/,/\\,/g')
    printf '%d,%s,%s\n' "$issue_num" "$existing_dest_num" "$label_str" >> "$mapping_file"
    continue
  fi

  if [[ $dry_run -eq 1 ]]; then
    printf '[DRY-RUN] Would migrate issue #%d: %s\n' "$issue_num" "$title"
    label_str=$(printf '%s\n' "$title" | sed 's/,/\\,/g')
    printf '%d,WOULD-CREATE,%s\n' "$issue_num" "$label_str" >> "$mapping_file"
  else
    rate_limit
    new_issue_num=$(create_destination_issue "$from_repo" "$to_repo" "$issue_num" "$title" "$body" "$labels")

    printf 'Created issue #%s for source #%d: %s\n' "$new_issue_num" "$issue_num" "$title"
    label_str=$(printf '%s\n' "$title" | sed 's/,/\\,/g')
    printf '%d,%s,%s\n' "$issue_num" "$new_issue_num" "$label_str" >> "$mapping_file"

    rate_limit
    comment_on_source_issue "$issue_num" "$from_repo" "https://github.com/${to_repo}/issues/${new_issue_num}"

    rate_limit
    close_source_issue "$issue_num" "$from_repo"
  fi
done < <(printf '%s\n' "$issues" | jq -c '.[]')

if [[ $pretty -eq 1 ]]; then
  printf '\n=== Issue Migration Results ===\n'
  printf 'Total issues checked: %d\n' "$issue_count"
  printf 'Issues matching filter: %d\n' "$migrated_count"
  printf '\nMapping (old issue -> new issue):\n'
  column -t -s',' "$mapping_file"
else
  cat "$mapping_file"
fi

exit 0
