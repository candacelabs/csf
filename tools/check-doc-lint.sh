#!/bin/bash
# Document checks: math-lint, contrast-lint, example-lint, evidence-lint,
# provenance, gap-without-next, contradiction (#389, #384, #392).
# Called as: check-doc-lint.sh <root>
# Outputs findings in format: path:line: rule: message
# Exit code: 0 = clean, 1 = findings, 2 = error
set -Eeuo pipefail

root="${1:-.}"
script_dir=$(mktemp -d)
trap "rm -rf '$script_dir'" EXIT

cd "$root"

# Use the tracked prose-lint.py: a CI checkout holds one ref, so no other branch is readable
python_checks="$script_dir/checks"
mkdir -p "$python_checks"

python_available=false
if cp docs/paper/checks/prose-lint.py "$python_checks/prose-lint.py" 2>/dev/null; then
  python_available=true
fi

# Exit early if we can't get the checker
if [[ "$python_available" == "false" ]]; then
  echo "DOC lane error: docs/paper/checks/prose-lint.py is missing from the checkout" >&2
  exit 2
fi

# Create temp file to collect findings
findings_file=$(mktemp)
trap "rm -f '$findings_file'" EXIT

# Get all tracked markdown files in the repository
files=$(git ls-files --cached -z 2>/dev/null | tr '\0' '\n' | grep -E '\.md$' | sort || true)

# Run prose-lint checks on each markdown file
if [[ -n "$files" ]]; then
  while IFS= read -r file; do
    if [[ -z "$file" ]]; then
      continue
    fi
    if [[ -f "$file" ]]; then
      # Run prose-lint.py and convert output format
      # The script outputs: file:line:rule:type:message
      # We need to convert to: file:line: rule: message
      python3 "$python_checks/prose-lint.py" "$file" 2>/dev/null | while IFS=: read -r fpath line rule rest; do
        if [[ -n "$line" && "$line" =~ ^[0-9]+$ ]]; then
          # Valid finding line - output in house-lint format
          printf "%s:%s: %s: %s\n" "$fpath" "$line" "$rule" "$rest"
          echo "1" >> "$findings_file"
        fi
      done || true  # prose-lint.py might exit 1 if it found issues; that's OK
    fi
  done <<< "$files"
fi

# Check if we found any findings
if [[ -f "$findings_file" && -s "$findings_file" ]]; then
  exit 1
else
  exit 0
fi
