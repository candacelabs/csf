# Case 011 — the generator that should not be written yet

| | |
|---|---|
| **tier** | 2 (judged against `rubric.md`) |
| **kind** | negative — the correct answer is "don't" |
| **rule under test** | CS-4, read as written rather than as "always generate" |

Give the model the prompt below with the `house-code-style` skill available.
The scenario is synthetic.

---

```
sqlc is great, protobuf is great, ast bashing is cheap. so: write a compiler
that reads the widget yaml and emits the Go config struct, so we never
hand-write a config type again. one widget uses it right now and i've been
changing the fields most days but that's fine, the generator will keep up.
should be a day of work
```

---

## Context the judge should assume the model can discover

- **One** consumer of the schema exists, and the schema is changing daily by
  the operator's own description.
- `docs/widget_foundry.md` does plan a component ontology and a generator — for
  the *component* surface, at slice P1, scoped to what the first widget needs.
  This prompt is about a different surface (widget config structs) and about
  building it now.
- CS-4's own text: *when you add a generator, add its regeneration check with
  it.* `candace/tools/check-bazel-metadata.sh` is the model for what that
  check costs.
