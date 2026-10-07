#!/usr/bin/env bash
set -uo pipefail

DRAFT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
CHECKS_DIR="${DRAFT_DIR}/checks"
TEMP_REPO="/tmp/csf_staging_check_$$"
MEASUREMENTS_FILE="${CHECKS_DIR}/measurements.txt"

cleanup() {
  rm -rf "$TEMP_REPO"
}
trap cleanup EXIT

: > "$MEASUREMENTS_FILE"

log_measurement() {
  local name="$1" expected="$2" found="$3" status="$4" cmd="$5"
  printf '%s|%s|%s|%s|%s\n' "$name" "$expected" "$found" "$status" "$cmd" >> "$MEASUREMENTS_FILE"
}

check_eq() {
  local name="$1" expected="$2" cmd="$3"
  local found
  found=$(eval "$cmd") || found="ERROR"
  if [ "$found" = "$expected" ]; then
    log_measurement "$name" "$expected" "$found" "PASS" "$cmd"
    echo "PASS: $name"
    return 0
  else
    log_measurement "$name" "$expected" "$found" "FAIL" "$cmd"
    echo "FAIL: $name - expected '$expected', found '$found'"
    return 1
  fi
}

check_gte() {
  local name="$1" threshold="$2" cmd="$3"
  local found
  found=$(eval "$cmd") || found="ERROR"
  if [ "$found" -ge "$threshold" ] 2>/dev/null; then
    log_measurement "$name" ">=$threshold" "$found" "PASS" "$cmd"
    echo "PASS: $name"
    return 0
  else
    log_measurement "$name" ">=$threshold" "$found" "FAIL" "$cmd"
    echo "FAIL: $name - expected >=$threshold, found '$found'"
    return 1
  fi
}

check_contains() {
  local name="$1" pattern="$2" cmd="$3"
  local found
  found=$(eval "$cmd") || found=""
  if echo "$found" | grep -q "$pattern"; then
    log_measurement "$name" "contains:$pattern" "YES" "PASS" "$cmd"
    echo "PASS: $name"
    return 0
  else
    log_measurement "$name" "contains:$pattern" "NO" "FAIL" "$cmd"
    echo "FAIL: $name - does not contain '$pattern'"
    return 1
  fi
}

check_exists() {
  local name="$1" cmd="$2"
  if eval "$cmd" >/dev/null 2>&1; then
    log_measurement "$name" "exists" "YES" "PASS" "$cmd"
    echo "PASS: $name"
    return 0
  else
    log_measurement "$name" "exists" "NO" "FAIL" "$cmd"
    echo "FAIL: $name - not found"
    return 1
  fi
}

echo "Setting up csf_staging repository..."
git clone --depth 1 --branch main https://github.com/candacelabs/csf_staging.git "$TEMP_REPO" 2>&1 | tail -1

echo ""
echo "=== Core Repository Measurements ==="
echo ""

check_eq "architecture.csf_lines" "372" \
  "git -C '$TEMP_REPO' show origin/main:csf/compiler/language/architecture.csf | wc -l" || true

check_eq "diagram_blocks" "4" \
  "git -C '$TEMP_REPO' show origin/main:csf/compiler/language/architecture.csf | grep -c '^diagram'" || true

check_eq "diagram_node_edge_group_lines" "81" \
  "git -C '$TEMP_REPO' show origin/main:csf/compiler/language/architecture.csf | awk '/^diagram.*{/,/^}/ { if (/^\s*(node|edge|group)\s/) count++ } END { print count }'" || true

check_eq "markdown_files" "211" \
  "git -C '$TEMP_REPO' ls-tree -r --name-only origin/main | grep -c '\\.md\$' || echo 0" || true

check_eq "pkg_widget_files" "83" \
  "git -C '$TEMP_REPO' ls-tree -r --name-only origin/main | grep '^pkg/widget/' | wc -l" || true

echo ""
echo "=== Definition and Importer Checks ==="
echo ""

check_contains "widget_def_line34" "widget" \
  "git -C '$TEMP_REPO' show origin/main:csf/compiler/language/architecture.csf | sed -n '34p'" || true

check_gte "widget_importers" 3 \
  "git -C '$TEMP_REPO' grep -l 'pkg/widget' origin/main -- '*.go' 2>/dev/null | grep -cE '(opsview|copilot-adapter|examples/widget)' || echo 0" || true

echo ""
echo "=== GitHub Issues ==="
echo ""

check_exists "issue_294" \
  "gh issue view 294 -R "${CSF_SOURCE_REPO:?set CSF_SOURCE_REPO to the source monorepo}" --json title 2>/dev/null | grep -q title" || true

check_exists "issue_330" \
  "gh issue view 330 -R "${CSF_SOURCE_REPO:?set CSF_SOURCE_REPO to the source monorepo}" --json title 2>/dev/null | grep -q title" || true

echo ""
echo "=== Unverifiable Claims ==="
echo ""
echo "The following require ratchet run output committed to csf_staging:"
echo "  1. Ontology alignment score: 0.5434 → 0.5383 across PR #9"
echo "  2. CS-16 findings: 48 → 47 → 43 across recent merges"
log_measurement "alignment_score" "from_ratchet" "UNVERIFIABLE" "UNVERIFIABLE" "needs ontology-score-*.json"
log_measurement "cs16_findings" "from_ratchet" "UNVERIFIABLE" "UNVERIFIABLE" "needs cs16-*.json"

echo ""
echo "=== MEASUREMENT RESULTS ==="
printf '%-40s | %-25s | %-15s | %s\n' "Measurement" "Expected" "Found" "Status"
printf '%s\n' "$(printf '=%.0s' {1..110})"

failed=0
while IFS='|' read -r name expected found status cmd; do
  if [ "$status" = "FAIL" ]; then
    ((failed++))
  fi
  printf '%-40s | %-25s | %-15s | %s\n' "$name" "$expected" "$found" "$status"
done < "$MEASUREMENTS_FILE"

echo ""
if [ $failed -eq 0 ]; then
  echo "✓ All verifiable measurements passed."
  exit 0
else
  echo "✗ $failed measurement(s) failed."
  exit 1
fi
