#!/bin/bash
# Verify: "pkg/widget is imported directly by three services" (draft line 13)
# Against MEASURED FACTS: "imported by services/opsview (4 files), 
# services/copilot-adapter/kanban (3) and examples/widget (about 10)"

cd candacelabs/csf_staging 2>/dev/null || {
  echo "FAIL: csf_staging repo not found. Run: git clone https://github.com/candacelabs/csf_staging"
  exit 1
}

echo "=== Services importing pkg/widget ==="
echo "Expected: 3 services (services/opsview, services/copilot-adapter/kanban, examples/widget)"
echo ""

# Find all Go files that import pkg/widget
echo "Go files importing candacelabs/csf/pkg/widget or candace/pkg/widget:"
grep -r "candacelabs/csf/pkg/widget\|candace/pkg/widget" --include="*.go" | \
  sed 's/:.*import.*$//' | sort -u | wc -l

# List owning packages
echo ""
echo "Owning packages:"
grep -r "candacelabs/csf/pkg/widget\|candace/pkg/widget" --include="*.go" | \
  sed 's/:.*$//' | sed 's|^[^/]*/\([^/]*/[^/]*\)/.*|\1|' | sort -u
