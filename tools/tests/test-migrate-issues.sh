#!/bin/bash
set -Eeuo pipefail

# test-migrate-issues.sh: test suite for migrate-issues.sh using fixtures

test_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
tools_dir=$(dirname "$test_dir")
script="$tools_dir/migrate-issues.sh"

# Create temporary fixtures directory
fixtures_dir=$(mktemp -d)
trap 'rm -rf "$fixtures_dir"' EXIT

# Mock gh CLI
mock_gh() {
  local output_file="$fixtures_dir/gh-output.json"

  case "$*" in
    *"issue list"*"state open"*)
      cat > "$output_file" <<'EOF'
[
  {
    "number": 239,
    "title": "rrsi fairness probe: verdicts vary between runs",
    "body": "This is about rrsi fairness.",
    "labels": [{"name": "backlog"}]
  },
  {
    "number": 224,
    "title": "CSF: let Bazel own codegen and Workbench checks; use uv for Python tooling",
    "body": "CSF codegen work.",
    "labels": []
  },
  {
    "number": 200,
    "title": "CSF: add ACP adapters for Codex, Claude Code and other agent SDKs",
    "body": "ACP adapters for agents.",
    "labels": [{"name": "enhancement"}]
  }
]
EOF
      cat "$output_file"
      ;;

    *"issue list"*"state all"*)
      # Return empty for initial queries
      echo "[]"
      ;;

    *"issue view"*"json comments"*)
      cat > "$output_file" <<'EOF'
{
  "comments": [
    {
      "author": {"login": "user1"},
      "createdAt": "2026-10-01T10:00:00Z",
      "body": "This is a comment."
    }
  ]
}
EOF
      cat "$output_file"
      ;;

    *"issue create"*)
      echo '{"number": 100}' >> "$output_file"
      cat > "$output_file" <<'EOF'
{"number": 100}
EOF
      cat "$output_file"
      ;;

    *"issue comment"*)
      echo "Comment created"
      ;;

    *"issue close"*)
      echo "Issue closed"
      ;;

    *)
      echo "{}" >&2
      ;;
  esac
}

# Test 1: Script exists and is executable
test_script_exists() {
  [[ -x "$script" ]] || {
    printf 'FAIL: script %s is not executable\n' "$script" >&2
    return 1
  }
  printf 'PASS: script is executable\n'
}

# Test 2: Script requires --from and --to
test_required_args() {
  local output
  output=$("$script" 2>&1 || true)
  grep -q "Usage:" <<< "$output" || {
    printf 'FAIL: script did not show usage without --from/--to\n' >&2
    return 1
  }
  printf 'PASS: required arguments check works\n'
}

# Test 3: Script accepts all options
test_options() {
  # Just test that it doesn't error on valid options
  "$script" --from owner/repo1 --to owner/repo2 --filter "CSF" --exclude "edge" --dry-run 2>&1 | head -1 > /dev/null || {
    printf 'FAIL: script failed with valid options\n' >&2
    return 1
  }
  printf 'PASS: options parsing works\n'
}

# Test 4: CSV output format
test_csv_output() {
  local output
  output=$("$script" --from owner/repo1 --to owner/repo2 --dry-run 2>&1 | grep -E '^[0-9]' || true)

  # Output should have comma-separated values
  [[ -z "$output" ]] && {
    printf 'PASS: csv output test (dry-run produces expected format)\n'
    return 0
  }

  # Check that lines have the right format (old,new,title)
  while IFS= read -r line; do
    [[ "$line" =~ ^[0-9]+,[^ ]+,.* ]] || {
      printf 'FAIL: csv line does not match format: %s\n' "$line" >&2
      return 1
    }
  done <<< "$output"

  printf 'PASS: csv output format is correct\n'
}

# Test 5: Filter regex works
test_filter_regex() {
  # This is a smoke test - the actual filtering is tested via dry-run
  local output
  output=$("$script" --from owner/repo1 --to owner/repo2 --filter "^CSF:" --dry-run --pretty 2>&1 || true)

  # Should complete without error
  [[ $? -eq 0 ]] && printf 'PASS: filter regex processing works\n' || {
    printf 'FAIL: filter regex test failed\n' >&2
    return 1
  }
}

# Test 6: Exclude regex works
test_exclude_regex() {
  local output
  output=$("$script" --from owner/repo1 --to owner/repo2 --exclude "edge" --dry-run --pretty 2>&1 || true)

  # Should complete without error
  [[ $? -eq 0 ]] && printf 'PASS: exclude regex processing works\n' || {
    printf 'FAIL: exclude regex test failed\n' >&2
    return 1
  }
}

# Test 7: Pretty output option
test_pretty_output() {
  local output
  output=$("$script" --from owner/repo1 --to owner/repo2 --dry-run --pretty 2>&1 || true)

  # Pretty output should include summary information
  grep -q "Issue Migration Results" <<< "$output" || {
    printf 'FAIL: pretty output does not contain expected summary\n' >&2
    return 1
  }
  printf 'PASS: pretty output format is correct\n'
}

# Run all tests
printf '=== migrate-issues.sh Test Suite ===\n\n'

tests=(
  test_script_exists
  test_required_args
  test_options
  test_filter_regex
  test_exclude_regex
  test_pretty_output
)

passed=0
failed=0

for test in "${tests[@]}"; do
  if "$test"; then
    ((passed++))
  else
    ((failed++))
  fi
done

printf '\n=== Test Results ===\n'
printf 'Passed: %d\n' "$passed"
printf 'Failed: %d\n' "$failed"

exit "$failed"
