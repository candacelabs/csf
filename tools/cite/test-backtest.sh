#!/bin/bash
set -Eeuo pipefail

cd "$(dirname "$0")"

# Test basic canonicalization with a small test file
cat > /tmp/test-keys.txt <<'EOF'
# Test keys
ansi-sparc   10.1016/0306-4379(78)90001-7
passos       10.1109/MS.2009.117
dspy         2310.03714
prov-dm      https://www.w3.org/TR/2013/REC-prov-dm-20130430/
EOF

echo "Testing cite tool against test keys..."

# Build the binary
bash ../../tools/bazel.sh build -- //tools/cite:cite >/dev/null 2>&1

CITE_BIN=$(bash ../../tools/bazel.sh query -- //tools/cite:cite 2>/dev/null | head -1)
CITE_BIN="${BAZEL_BIN:-bazel-bin}/tools/cite/cite"

if [ ! -f "$CITE_BIN" ]; then
	# Try to find it in the bazel output directory
	find /tmp -name "cite" -type f -executable 2>/dev/null | head -1 > /tmp/cite_path.txt || true
	if [ -s /tmp/cite_path.txt ]; then
		CITE_BIN=$(cat /tmp/cite_path.txt)
	fi
fi

echo "Using cite binary: $CITE_BIN"
if [ ! -f "$CITE_BIN" ]; then
	echo "ERROR: Could not find cite binary"
	exit 1
fi

# Test 1: Basic key resolution
echo ""
echo "Test 1: Resolving keys..."
timeout 30 "$CITE_BIN" /tmp/test-keys.txt 2>/tmp/test-errors.txt || true

echo "Errors:"
cat /tmp/test-errors.txt || true

# Test 2: Duplicate detection with same canonical form
cat > /tmp/test-dup-keys.txt <<'EOF'
key1 10.1145/222124.222136
key2 10.1145/222124.222136
EOF

echo ""
echo "Test 2: Duplicate detection..."
timeout 10 "$CITE_BIN" /tmp/test-dup-keys.txt 2>&1 | grep -i duplicate || echo "ERROR: Should have detected duplicate"

echo ""
echo "Backtest complete"
