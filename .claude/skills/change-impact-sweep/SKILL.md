---
name: change-impact-sweep
description: >-
  Before declaring any change done, sweep its impact: find every dependent of
  the changed public symbol, interface, user-visible string, flag, doc
  passage, sample or rule (callers, implementers, mocks, tests, READMEs,
  doc-asserting tests); regenerate the artifacts derived from it and check git
  status for drift; run the affected test scope beyond the edited package —
  importers across the module, generated-drift checks, docs and repository
  specs — against a baseline taken on the pre-change tree. Load it before
  editing a signature, exported name, interface, wire value, CLI verb, doc
  that tests read, or an AGENTS.md/skill rule, and again before saying a change
  is finished, pushing for review or reporting a slice complete.
---

# Change impact sweep

Mined session traces show changes reported as done and then breaking somewhere
else: a caller still on the old signature, a mock not regenerated, a doc a test
asserts on, a spec in another package that only a broader run reaches. 31 such
episodes over 7 sessions were measured on 2026-10-01
(lab entry (monorepo lab entry `2026-10-01-harness-operating-rules`)).
The coupling is invisible from the edited file, so the sweep makes it visible.

## 1. Before editing: list the dependents

For each identifier, string or phrase you will change:

```bash
git grep -n '<identifier or phrase>'
```

Include tests, mocks and fakes, interface implementers, READMEs, samples,
generated inputs, workflow path filters, and the specs under `tools/gates`
that read the gate workflows. Write the list down; it is the scope
for steps 2 and 3. Take a baseline of the step-3 commands on the unchanged tree
when the scope already has failures, so only new failures are yours.

## 2. After editing: regenerate and check drift

Run each generator whose input you touched — the directory's `generate.sh` or
`gen.sh`, `go generate`, Gazelle via `tools/check-bazel-metadata.sh`,
the CSF ontology regeneration (`csf/compiler/language/README.md`) — then:

```bash
git status --short
```

Every changed file must be one you expected. Update hardcoded counts and
asserted strings found in step 1.

## 3. Run the affected scope, not just the edited package

| Changed | Run |
|---|---|
| Go package | its tests with `-race`, then its importers: `go test ./...` at the root |
| Generated projection | the owning generate script's `--check`/drift mode where it has one |
| Gate workflows or launchers | `go test ./tools/gates/...` |
| READMEs, AGENTS.md, skills | re-read every section that links to the change; `bash tools/ontology-score.sh --pr-spec` reads every README for vocabulary |
| Style-relevant Go | `bash tools/check-house-lint.sh --test --summary house-lint-report.md` |

Run Go through the pinned Go image `.github/workflows/ci.yml` uses, or the
Bazel container ([tools/devcontainer/README.md](../../../tools/devcontainer/README.md)).
Fix every new failure before reporting; name any pre-existing failure with its
baseline evidence.

If the area has no single regenerate or check command, say so in the report and
propose one rather than guessing at coverage.
