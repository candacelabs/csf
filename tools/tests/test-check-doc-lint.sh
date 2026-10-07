#!/usr/bin/env bash
# Regression test for tools/check-doc-lint.sh: the document lane reads its
# linter from the checkout it scans, never from another branch. A CI checkout
# holds only the pushed ref, so a fixture repository with one branch and no
# review/paper-draft ref is the same condition the lane meets there.
set -Eeuo pipefail

test_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd -P)
script="$(dirname -- "$test_dir")/check-doc-lint.sh"

fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT

failures=0
expect() {
  local label=$1 want=$2 got=$3
  if [[ "$want" == "$got" ]]; then
    printf 'ok   %s\n' "$label"
  else
    printf 'FAIL %s: want %s, got %s\n' "$label" "$want" "$got" >&2
    failures=$((failures + 1))
  fi
}

# A stand-in linter with prose-lint.py's output contract: file:line:rule:type:message.
mkdir -p "$fixture/docs/paper/checks"
cat > "$fixture/docs/paper/checks/prose-lint.py" <<'PYEOF'
import sys
for path in sys.argv[1:]:
    for number, line in enumerate(open(path, encoding="utf-8"), start=1):
        if "FLAGGED" in line:
            print(f"{path}:{number}:math-lint:mandatory:fixture finding")
PYEOF
printf 'A clean paragraph.\n' > "$fixture/clean.md"

git -C "$fixture" init -q -b trunk
git -C "$fixture" -c user.name=fixture -c user.email=fixture@example.invalid add .
git -C "$fixture" -c user.name=fixture -c user.email=fixture@example.invalid commit -q -m fixture
if git -C "$fixture" rev-parse --verify -q review/paper-draft >/dev/null; then
  printf 'FAIL fixture unexpectedly has a review/paper-draft ref\n' >&2
  exit 1
fi

status=0
bash "$script" "$fixture" >/dev/null 2>&1 || status=$?
expect "a clean tree with only the checkout's own linter passes" 0 "$status"

printf 'A FLAGGED paragraph.\n' > "$fixture/flagged.md"
git -C "$fixture" -c user.name=fixture -c user.email=fixture@example.invalid add flagged.md
output=$(bash "$script" "$fixture" 2>/dev/null) || status=$?
expect "a finding is exit 1, not a scan error" 1 "$status"
expect "the finding is reported as path:line: rule: message" \
  "flagged.md:1: math-lint: mandatory:fixture finding" "$output"

git -C "$fixture" rm -q -f docs/paper/checks/prose-lint.py
status=0
message=$(bash "$script" "$fixture" 2>&1 >/dev/null) || status=$?
expect "a checkout without the linter is a scan error" 2 "$status"
expect "the scan error names the missing tracked file" \
  "DOC lane error: docs/paper/checks/prose-lint.py is missing from the checkout" "$message"

if ((failures > 0)); then
  printf '%d check(s) failed\n' "$failures" >&2
  exit 1
fi
printf 'check-doc-lint.sh tests passed\n'
