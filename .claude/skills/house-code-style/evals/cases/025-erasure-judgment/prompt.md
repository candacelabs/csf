# Case 025 — the erasure that moves and the erasure that stays

| | |
|---|---|
| **tier** | 2 (judged against `rubric.md`) |
| **kind** | **split, with a third answer** — one half keeps its `any`, and the other half's correct answer is neither "make it generic" nor "leave it" but "relocate the erasure" |
| **rule under test** | CS-7, and specifically the two things that keep it from meaning "replace every `any` with a type parameter" |

Give the model the prompt below with the `house-code-style` skill available.
The scenario is synthetic; no such package exists in the repo, and the answer
must not depend on one existing.

Both halves are an exported `any` in a non-internal package, so **the gate
finding is identical on both** — which is the point, and the same construction
`023` used when it gave both its halves the same syntax.

---

```
right so CS-7. two `any`s in the notification service and i want to know if
they're the same problem.

one: the sink registry.

    type Payload any

    type ISink interface {
        Name() string
        Deliver(payload Payload) error
        Describe(payload Payload) string
    }

    type Registry struct {
        sinks []ISink
    }

every sink is written against its own event type — the pager sink wants a
PageEvent, the digest sink wants []Summary, the audit sink wants an AuditRow.
so every Deliver starts with payload.(PageEvent) and a branch that can't
happen. we generate about half of these sinks now and the generator emits the
assertion too, so the unchecked cast is in code nobody even reads.

two: the inbound webhook. third parties POST us JSON, we don't own the schema
and it changes without telling us. we store the body and hand it to whoever
subscribed.

    type Envelope struct {
        Received time.Time
        Source   string
        Payload  any
    }

we json.Unmarshal into Payload and json.Marshal it back out. nothing in our
code ever reads a field off it.

the gate flags both. do i make them both generic
```

---

## Context the judge should assume the model can discover

- `scripts/check_style.py` reports **both**, identically: an exported member of
  an exported type, typed bare `any` (or a package-local alias for it), in a
  non-internal package. The heuristic sees one shape and cannot tell them
  apart, so the correct answer leaves one of them firing forever.
- The gate is report-only and will never be blocking. No CI failure is pending
  on this.
- `Registry` genuinely holds sinks of different payload types in one sequence.
  Nothing in the prompt offers to give that up, and a response may not quietly
  assume it away.
- Half the sinks are generated (CS-4), so the fix is a change to a generator,
  not only to handwritten code.
