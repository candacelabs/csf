# PM-1 — checkpoint-2 scope pass, and what it routed elsewhere

| | |
|---|---|
| **Owner** | PM-1 (scope) |
| **Date** | 2026-08-04 |
| **Ruled in** | [PRD](../PRD.md) §9, amendment log **v0.4** (ten rows) |
| **Answering** | QA-1 **D-12**, L9-1 **C-26**, and a stale-number sweep of the PRD |

The rulings are in the PRD, where they gate. This file holds the things the
sweep turned up that are **not PM-1's to fix**, so they are found by the next
person rather than by the next reviewer. Precedent is the orchestrator log at
the bottom of [checkpoint-2-batch.md](../reviews/checkpoint-2-batch.md).

None of these blocks anything in flight.

---

## 1. D-12 is answered; L9-1's half is unblocked

**PM-1's ruling: FR-36 carries the connected-graph reading.** The full argument
is PRD §9 v0.4 row 1. In one line: `instrumentation.md` specifies links at three
sites for three different and non-convenience reasons, the mechanism was costed
into RFC §6.2's memory budget, and the literal reading's only natural test is the
`TraceID`-counting assertion **D-11** proved could not fail.

FR-36 as amended requires a connected graph, true descendancy for the actor's own
spans, and links only where a parent edge would be false — each site enumerated
in `instrumentation.md` with its reason.

**What is left to L9-1**, per its own note that it would rule in the same pass as
the checkpoint-2 gate report: whether the **read-pump → actor** boundary should
stop being a link and become a real parent. QA-1 says it is feasible (the span
context can ride the mailbox message). FR-36 as amended is satisfied either way —
clause 3 permits the link only while it is enumerated, and always permits turning
it into a parent — so this is a technical call, not a scope one.

---

## 2. `instrumentation.md` §3 disagrees with the shipped tracer, in three places

Found while grounding the D-12 ruling in the code rather than in the document.
`instrumentation.md` is DEV-1's; PM-1 did not touch it.

| # | The document says | The code does | Where |
|---|---|---|---|
| a | §3.1 draws `gotthlive.authorize` as a **child** of `gotthlive.event` | `Actor.authorize` starts the span on the read pump from the connection context and **ends** it before the event reaches the mailbox; `Actor.transition` then starts the transition span as a **root**, with a link back to the stored `SpanRef` | `Actor.authorize` (`internal/session/ingress.go`), `Actor.transition` (`actor.go`) |
| b | §3.1 says `gotthlive.effect.*` spans are **"linked, not nested"** | `Actor.spawn` passes the transition's context straight through to `Actor.execute`, so the effect span is a **nested child** — of a parent whose `defer span.End()` fires on the next statement, while the child is still running | `Actor.spawn`, `Actor.execute` (`internal/session/effects.go`); the call is `runEffects` as the last statement of `Actor.transition` |
| c | §3.1 draws an eight-span tree | Five of the eight are declared and started nowhere: `gotthlive.parse`, `gotthlive.reduce`, `gotthlive.render`, `gotthlive.render.fragment`, `gotthlive.send`. `gotthlive.encode` covers encode **and** send, recording both durations from one span. Reduce and render are visible as histograms only | the span-name const block in `internal/obs/trace.go`; the only start sites in the module are `Actor.authorize`, `Actor.mount`, `Actor.transition`, `Actor.send`, `Actor.execute`, and `resync.go`'s origin and client-morph sites |

(b) is the interesting one, because the document's stated reason for the link —
an effect may finish after the event span closes — is **correct**, and the code
does the thing the reason argues against. Nesting a span under a parent that has
already ended is legal in OpenTelemetry and reads as a containment that did not
happen.

(c) is recorded in FR-36 itself rather than narrowed away. The requirement asks
for a span per named phase, an operator attributing latency inside one event
needs them, and shrinking the requirement to fit the six that exist would make it
true by construction — the same defect FR-74 had.

**Owner: DEV-1, with L9-1 on (a).** Phase: checkpoint 2. Non-blocking: none of
the three changes which reading FR-36 carries.

---

## 3. The effect-panic log omits the causal ID it is holding

FR-58: *"Every library-produced error MUST name the session, the causal ID where
one exists, and the actionable next step."*

`Actor.runOne`'s recover arm (`internal/session/effects.go`) logs an effect
panic at `Error` with
`session_id`, `effect_source`, `site`, `panic` and `stack`. It does **not** log
`scheduledBy` — the identifier of the event whose transition returned the effect
— which is a parameter of the enclosing `runOne` and is already carried all the
way to the resulting patch for exactly this reason (the comment on `runEffects`
argues the case: *"a patch that names only the effect leaves an operator able to
reach `effect:counter.watch` and unable to reach the click that scheduled it"*).

The causal chain is not lost — it rides on the failure event's origin and
contributing edge, which is why FR-23 as amended is satisfied — but the log
record that FR-23 also requires is the one an operator reads first, and it is
the one that drops the edge.

One field. **Owner: DEV-1.** Phase: checkpoint 2, alongside C-26.

---

## 4. G2 has no measurement, and RFC §6.2 says Phase 1 owed one

RFC-0001 §6.2 opens: *"An **estimate**, not a measurement. Phase 1 records the
measured baseline (checklist §8.8) and this table is corrected in the same PR."*
No per-idle-connection memory figure has been recorded. QA-1's D-10 is the
nearest thing and it is the leak test's RSS sample, handed to QA-2 for Phase 3.

So the 46,080 B gate rests entirely on a 42,416 B composition estimate with two
estimated lines inside it (kernel socket 4,000 B, WebSocket conn struct 2,000 B)
plus the 18,000 B TLS figure O7 tracks separately. PRD §3 now says so; the number
itself is not PM-1's to produce.

**Owner: DEV-1 (the baseline) + QA-2 (the method, equivalence-spec §3.6).**
Related and still outstanding from Phase 0: **C-5**, the TLS boundary landing in
equivalence-spec §3.6 so it binds the Next.js side. Both are owed before any
Phase 1 memory number is quoted as comparable.

---

## 5. Still open, and named as open

| Item | Why it is open | Owner |
|---|---|---|
| **I3** — should NFR-1's gate be the 100 %-sampled figure rather than the 5 %-sampled one? | PRD v0.4 records instrumentation §4.1's rule that **both** figures are reported and the sampled one is the gate. Whether the gate itself moves is a separate call and was not made here. | QA-2 + PM-1, Phase 5 |
| **`Config.Dev`** — implement or cut (C-26) | Both are defensible and only one leaves FR-23 true. If it is cut, FR-23's dev/prod sentence needs a further PM-1 amendment **in the same PR**. Phase 2's exit criteria now carry the box. | DEV-1, then PM-1 |
| **Phase 1 exit boxes** | Unchecked, though QA-1 re-issued every CP1 verdict with only CP1-16 PARTIAL. Recording a phase as exited is a gate action, and checkpoint 1 sits inside the consolidated Phase 1–3 track where sign-off is per checkpoint in the PR description. Ticking them here would record a gate PM-1 did not hold. | PM-1, at the checkpoint-2 gate pass |

— PM-1, 2026-08-04
