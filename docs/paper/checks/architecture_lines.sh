#!/bin/bash
# Verify: "81 node and edge lines across 4 diagrams" (draft line 61)
# Expected from MEASURED FACTS: 4 diagram blocks with 81 hand-written node/edge/group lines

cd candacelabs/csf_staging 2>/dev/null || {
  echo "FAIL: csf_staging repo not found"
  exit 1
}

FILE="candace/csf/compiler/language/architecture.csf"
if [ ! -f "$FILE" ]; then
  echo "FAIL: $FILE not found"
  exit 1
fi

echo "=== Counting diagram blocks and lines ==="
echo "File: $FILE"
echo "Total lines: $(wc -l < "$FILE")"
echo ""

echo "Diagram blocks (containing node/edge/group declarations):"
grep -E "^diagram|^  node|^  edge|^  group" "$FILE" | head -20

echo ""
echo "Count of node/edge/group lines (not including diagram headers):"
grep -E "^  (node|edge|group)" "$FILE" | wc -l

echo ""
echo "Expected: 81 hand-written node/edge/group lines"
