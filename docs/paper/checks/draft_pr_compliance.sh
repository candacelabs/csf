#!/bin/bash
# Verify: "0 of 46 briefed runs lacked a PR, vs 104 of 126 unbriefed" (draft line 79)
# Source: source monorepo #294 mining

echo "=== Draft-PR Compliance Measurement ==="
echo "Expected baseline (from issue #294):"
echo "  Briefed runs (requested early draft PR): 0 of 46 lacked a PR (100% compliance)"
echo "  Unbriefed runs (no brief instruction): 104 of 126 lacked a PR (~18% compliance)"
echo ""
echo "To verify, query the source monorepo issue #294 comments"
echo "Command: gh issue view 294 -R "${CSF_SOURCE_REPO:?set CSF_SOURCE_REPO to the source monorepo}" --comments"
