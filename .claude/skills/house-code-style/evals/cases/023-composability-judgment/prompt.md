# Case 023 — the chain that stays and the method set that goes

| | |
|---|---|
| **tier** | 2 (judged against `rubric.md`) |
| **kind** | **split** — one half of the answer is "keep the method chain", the other is "make it a registry" |
| **rule under test** | CS-6, and specifically the counterweight that keeps it from meaning "always a registry" |

Give the model the prompt below with the `house-code-style` skill available.
The scenario is synthetic; no such package exists in the repo, and the answer
must not depend on one existing.

Both halves of the prompt have three-or-more sibling calls in a dispatch
method, so **call count cannot be the discriminator** — which is the point.

---

```
ok so CS-6. two things in the widget host, both of them "a method that calls
its own methods", and i want to know if that means the same thing twice.

first one, and this is the one i actually care about: the builder.

    func (widgetBuilder *builder) build() {
        widgetBuilder.parse()
        widgetBuilder.resolve()
        widgetBuilder.emit()
    }

three steps. resolve can't run before parse, emit can't run before resolve.
it's been exactly these three for a year and i can't think of a fourth.

second one: diagnostics. there's a reporter struct with eleven checkX methods
and a checkAll that calls all eleven in a row. we added three of them last
month. every one of them reads the same document off the receiver and appends
to reporter.findings. and we keep adding a new diagnostic class and forgetting
to wire the check into checkAll — happened twice, both times we found out from
a bug report, not from a test.

do i turn these into registries or what
```

---

## Context the judge should assume the model can discover

- `scripts/check_style.py` reports **both** methods. `build()` is three
  argument-less calls on its own receiver and `checkAll()` is eleven; the
  heuristic sees one shape and cannot tell them apart. So there are exactly two
  CS-6 findings here, and the correct answer leaves one of them firing forever.
- The gate is report-only and will never be blocking. No CI failure is pending
  on this.
- Nothing is broken in the builder. The only reported symptom in the whole
  prompt is the diagnostics one: checks that exist but were never wired in.
