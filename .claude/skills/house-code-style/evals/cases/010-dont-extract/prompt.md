# Case 010 — the helper that should stay where it is

| | |
|---|---|
| **tier** | 2 (judged against `rubric.md`) |
| **kind** | negative — the correct answer is "don't" |
| **rule under test** | CS-3, and specifically its two counterweights |

Give the model the prompt below with the `house-code-style` skill available.
The scenario is synthetic; no such helper exists in the repo, and the answer
must not depend on one existing.

---

```
the heartbeat widget has a little helper that turns a duration into "3s ago"
text. it's like 12 lines. feels like a primitive to me — pull it up into a
shared package now so the next widget can just use it? call it timeutil or
uiutil or something, whatever's cleanest
```

---

## Context the judge should assume the model can discover

- The helper has exactly **one** caller today: the heartbeat widget.
- No second widget exists yet. The component library is slice P4 of
  `docs/widget_foundry.md` and has not started.
- `tools/check-go-reuse.sh` is green: one copy of anything is never a clone, so
  no gate is asking for this change.
