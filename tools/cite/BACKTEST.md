# Cite Tool Backtest

This document specifies the backtest for the cite tool porting from Python to Go.

## Test Instances (Λ)

### Instance 1: docs/paper/keys.txt (31 keys)
All canonical bibliography keys from the paper draft review branch.

### Instance 2: Passos 2010 (10.1109/MS.2009.117)
Single DOI that must resolve via Crossref with registry title "Conformance Checking Using the Temporal Description Language".

### Instance 3: Three Reflexion entries
Keys that share a common topic: `reflexion`, `reflexion-models`, `reflexion-models-tse`.
These should each resolve but remain distinct entries (same concept, different papers).

## Test Cases

### Resolution Test: $\mathrm{TP}$ (True Positives)
All 31 keys in docs/paper/keys.txt must resolve successfully:
```bash
tools/cite docs/paper/keys.txt 2>/dev/null | wc -l
# Expected: 31 markdown list items
```

### Duplicate Detection Test: $\mathrm{TP}$
Verify that identical canonical forms are detected:
```bash
cat > /tmp/dup-test.txt << 'EOF'
key1  10.1145/222124.222136
key2  10.1145/222124.222136
EOF
tools/cite /tmp/dup-test.txt 2>&1 | grep -c "duplicate:"
# Expected: 1 finding
```

### Vacuous Entry Test: $\mathrm{TP}$
Verify that entries with missing metadata are flagged:
- Entries with empty title → vacuous finding
- Entries with empty where → vacuous finding

### Passos 2010 Test: $\mathrm{TP}$
Verify Passos 2010 (conformance-overview, 10.1109/MS.2009.117) resolves with title containing "Conformance":
```bash
cat > /tmp/passos-test.txt << 'EOF'
conformance-overview  10.1109/MS.2009.117
EOF
tools/cite /tmp/passos-test.txt 2>/dev/null | grep -i "conformance"
# Expected: Match with Crossref registry title
```

## Gate Criteria

- **All 31 keys resolve** without errors
- **Canonical duplicate detection** flags exact form matches
- **No false positives** on distinct DOIs
- **Passos 2010 renders** with authoritative Crossref title

## Execution

```bash
bash tools/cite/BACKTEST.md
# Or use the test suite in main_test.go
bazel test //tools/cite:cite_test
```
