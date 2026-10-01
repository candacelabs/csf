# Phase 0 gate report — gotth-live

| Field | Value |
|---|---|
| Project | **gotth-live** — a Go library for server-driven live UI: state and rendering stay in Go, the browser holds one long-lived connection, events go up and re-rendered HTML fragments come down |
| Phase | **0 — Design** (no product code is written in this phase) |
| Report owner | **PM-1**, the Product Manager, who owns scope and the requirements document |
| Date | 2026-08-04 |
| Verdict | **PASSED — the design package is approved for implementation** |
| Approving authority | **L9-1**, the Principal Engineer, who holds technical veto over the design |
| Review record | `gotth-live/docs/rfc/001-review-cycle-2.md` (final verdicts, 14 conditions) and `gotth-live/docs/rfc/001-review-cycle-1.md` (the first pass and the six governance decisions D1–D6) |

**Who the other named roles are**, since they appear throughout: **QA-1** owns
correctness and can block a merge; **QA-2** owns resilience and performance and
can also block a merge; **DEV-1** is the server-core Go engineer and author of
most of the design package; **DEV-2** owns the browser-side client runtime;
**DEV-3** owns interoperability with HTMX (the existing request/response library
these pages already use).

---

## 1. What Phase 0 was for

Phase 0 produced no library code by design. Its purpose was to make the hard
decisions in writing, in advance, where they could be reviewed and argued —
transport, wire protocol, memory ceiling, observability, the exported API, the
dependency list, and the rules of the benchmark this project will publish — so
that implementation is execution rather than discovery. The phase gate was a
single question: **does the Principal Engineer approve the architecture RFC.**
He does.

## 2. What shipped

Twelve documents, all committed under `gotth-live/docs/` (the two review passes
share a row).

| # | Artifact | What it is | Author | Review status |
|---|---|---|---|---|
| 1 | `docs/PRD.md` — product requirements, now **v0.3** | The scope authority: 76 functional and 13 non-functional requirements, 13 numbered goals each with a measurable gate, the v1 exclusions, the five-phase plan with per-phase exit criteria, the risk register, and the amendment log | PM-1 | Merged. v0.3 applies the two conditions this review put on PM-1 (C-6, C-13) |
| 2 | `docs/rfc/0000-prior-art-teardown.md` | Evidence base: a teardown of Phoenix LiveView (Elixir), Hotwire/Turbo (Rails), Blazor Server (.NET), Livewire (PHP), Datastar, and the existing Go attempts — wire format, state location, reconnect strategy, DOM update strategy, observability, and the production failure each is known for | DEV-1 | Informational; not separately voted. The Principal Engineer recorded that it is "doing real work here" — nearly every contested design choice is argued from a named failure in a named system rather than from taste |
| 3 | `docs/adr/001-transport.md` — ADR-001 | The transport decision: **one WebSocket per browser tab, binary frames both directions**, with Server-Sent Events + `fetch` rejected on stated grounds, and the costs of the choice booked openly | DEV-1 | **APPROVE** (both cycles). 1 condition (C-14) |
| 4 | `docs/rfc/001-architecture.md` — RFC-0001 | The consolidated architecture, and the document the gate turned on: one goroutine and one bounded mailbox own each session; a pure reducer advances state; a pure render produces whole HTML fragments; frames are sequence-numbered, acknowledged and causally tagged; the memory ceiling, the flow-control window, the module layout, and the exit criteria with falsifiers | DEV-1 | **APPROVE-WITH-CONDITIONS**. 5 conditions (C-1…C-4, C-12) |
| 5 | `docs/protocol.md` | The wire-format mapping spec: the single `Frame` envelope, the eight frame kinds, every refinement predicate, the causal-ID fields, the close-code enumeration, and the version-negotiation rule | DEV-1 | **APPROVE** (was RETURN in cycle 1; all five objections closed) |
| 6 | `docs/instrumentation.md` | Metrics, traces, logs, and how they are audited — including the new §4A that finally specifies the **provenance log**, the artifact on which this project's headline "every patch is traceable" guarantee depends | DEV-1 | **APPROVE-WITH-CONDITIONS**. 3 conditions (C-7, C-8, C-9) |
| 7 | `docs/api-surface.md` | The exported API ledger: every exported identifier with a one-line reason it exists and the requirement it satisfies. Baseline **48 identifiers / 100 including struct fields**; continuous integration reports the delta on every pull request | DEV-1 | **APPROVE-WITH-CONDITIONS** (first review). 2 conditions (C-12, C-13) |
| 8 | `docs/dependencies.md` | The dependency ledger at a Go-standard-library-submission bar: what each dependency buys, its maintenance health, its transitive weight, why the standard library cannot do it, and what removal would cost if the project were abandoned — plus a "considered and rejected" section | DEV-1 | **APPROVE-WITH-CONDITIONS** (first review). 2 conditions (C-10, C-11) |
| 9 | `docs/bench/equivalence-spec.md` | The fairness contract for the flagship benchmark against an equivalent Next.js application: what the two apps are, and the operational definition of every measured word — "paint", "interactive", "active session" — fixed **before** any number is measured | QA-2 | Substance approved. **Condition C-5 landed while this report was being written** (commit `eb6cf6dd`): the TLS boundary is transplanted into §3.6 and now binds both stacks. PM-1 accepts it as to product surface (§4.2); the Principal Engineer's freeze sign-off follows |
| 10 | `docs/review-checklist.md` | The Principal Engineer's own standing checklist, walked top to bottom on every pull request; §11 is the separate pass for design documents | L9-1 | Live. Amended during this phase (decision D6) to carve out transport establishment and closure from the "everything on the wire is a typed frame" rule, because the WebSocket handshake and close frame provably cannot be |
| 11 | `docs/rfc/001-review-cycle-1.md` and `docs/rfc/001-review-cycle-2.md` | The two review passes themselves — 12 blocking objections and 13 advisories in cycle 1, all closed in cycle 2; 6 governance decisions; the 14 conditions in this report | L9-1 | Final. **There is no cycle 3**; reopening any decision requires a new architecture decision record with new evidence |

**Two of the fixes made the design better, not merely correct.** Reworking the
mailbox to hold pointers rather than values removed **6,656 bytes from every idle
connection**, and compacting the stored trace reference removed a further ~512
bytes. A revision cycle that *reduces* the number it was asked to correct is a
good sign about the review process.

**Six governance decisions were settled in cycle 1 and are closed** (they are
recorded here because they constrain implementation): tracing uses the
OpenTelemetry **API only**, with the consumer supplying the implementation
(D1); logging goes through the Go standard library's `log/slog` so the library
imposes no logging dependency on anyone (D2); the single-pull-request delivery is
compatible with the review rules because this design package *is* the prior
design note (D3); the transport-interface question is closed (D4); the API and
dependency ledgers were assigned owners and landed (D5); the checklist carve-out
above (D6).

## 3. What was cut, and why — PM-1's rationale as scope owner

Cuts are where a design document is honest or is not. These are the ones that
matter.

**3.1 Benchmarks against LiveView, Hotwire, Blazor and Datastar — moved to the
backlog (BL-27). The shipping comparison is Next.js only.**

Those four systems remain a Phase 0 *design* teardown and are cited throughout
the architecture; what they are no longer is a measured comparison in the
shipping deliverable. The reasoning is about credibility per unit of effort. A
benchmark whose author built both sides is structurally suspect (risk R-15), and
the defence is expensive: an agreed equivalence specification, production
defaults on both sides, published methodology, published variance, and ideally an
external reviewer — per stack. We can afford that defence properly **once**.
Spending it on Next.js is the right call because Next.js is the stack a team
would actually choose instead of this one, and it is the comparison an informed
reader will demand. Four half-defended comparisons would discredit the one
well-defended one sitting next to them.

**3.2 The `Transport` interface — cut (FR-2 amended in v0.2).**

The original requirement mandated that the transport sit behind a narrow Go
interface. With WebSocket decided as the only v1 transport, that interface would
have had exactly one implementation for the whole of v1 — speculative
abstraction, which the review checklist forbids. What the requirement was
*actually* protecting is **isolation**: that no reducer, render, protocol or
provenance code touches the transport, so a second transport remains possible
later. FR-2 now states the isolation property and keeps the identical
verification — an automated architecture test asserting the core packages do not
import the transport package or its WebSocket dependency. Nothing is lost; a
speculative type is not shipped. The second transport, and the interface it would
then justify, are backlog item BL-13.

**3.3 API surface cuts — roughly 30 exported symbols removed before any code was
written.**

Exported symbols are permanent, so the ledger was written first and cut hard:
the `Option` type and ~13 `WithX(...)` functions collapse into one `Config`
struct (which also makes the security configuration a single object a reviewer
can read at a glance); the `Executor` interface plus registration method becomes
one struct field; `App.Broadcast(...)` is cut because a cross-session write API
would violate session isolation by construction; six error sentinel values become
one structured configuration error carrying the offending field, which is more
actionable; two form-helper functions move into the client runtime, where one
code path replaces three attribute vocabularies. Every cut is recorded with its
replacement in the API ledger's "what was cut, and why" section, so nothing
disappears silently.

**3.4 The patch lifecycle hook — cut, and the requirement amended in the open
(condition C-13, applied in PRD v0.3).**

FR-56 asked for four hooks: mount, event, patch, teardown. The shipped surface
has three. The Principal Engineer went looking for the consumer of a patch hook
before accepting the cut and found none: FR-56's own sufficiency test —
subscribe to a message topic when a session starts, unsubscribe when it ends,
without leaking — is met by mount and teardown, and the two things in this design
that genuinely want per-patch visibility (the developer-mode inspector and the
test client) are library code, not application code. Patch observability is
delegated to instrumentation: a counter, two trace spans, and a per-transition
record in the provenance log. Adding the hook anyway would export a symbol with
no call site, which the API-minimality requirement makes a rejection. **I amended
FR-56 rather than footnoting the disagreement**, following the precedent set by
FR-2: a requirement and a shipped surface that disagree silently are how the next
reviewer gets misled. If an application appears that must audit patches from its
own code, the hook lands in Phase 2 with that consumer named in the pull request.

**3.5 Already excluded from v1 and unchanged**: multi-node scale-out, offline
mode, client-side prediction, optimistic UI, non-templ template engines,
non-Go clients, file upload over the live connection, durable session state
across restart, client-side routing, animation orchestration, internationalisation
helpers, third-party JavaScript component wrapping, a second transport, and
server-side diffing of consecutive renders. Each is one line in the backlog with
the reason attached.

## 4. Open risks

These are the ones I would raise in a status meeting. The full register is §7 of
the requirements document, `gotth-live/docs/PRD.md`.

**4.1 Two separate budgets are tight, and they are often confused. Both are
carried.**

- **Client runtime size.** The ceiling is **12,288 bytes** gzipped, in one file,
  enforced by continuous integration. The current ledger totals **11,100 bytes,
  leaving 1,188 bytes of reserve — 9.7 %**. One line of that ledger (the DOM
  morphing library, 3,350 bytes) is measured; **the other five subsystems are
  estimates**. Five estimated lines overrunning ~20 % each consumes the entire
  reserve. This is not a redesign risk — a breach costs a feature-level cut late
  — but it is why every pull request touching the runtime must report its size
  delta broken down by subsystem from Phase 1 onward.
- **Server memory per idle connection.** The gate is **46,080 bytes (45 KiB)**
  with TLS terminated outside the measured container. The composition estimate
  lands at **42,416 bytes — 7.9 % headroom** — and **three lines of it remain
  unmeasured**: the kernel socket allocation (4,000 B), the WebSocket connection
  struct (2,000 B), and the now-secondary in-process TLS buffers (18,000 B). If
  the WebSocket connection struct is twice its estimate, the gate is breached.
  The response is pre-registered and deliberately leaves no wriggle room: the
  target does **not** move, the overage is attributed to a named line and
  engineered down, and only an architecture decision record with the measurement
  in hand can change the target. Changing the benchmark method is explicitly not
  an available remedy. Phase 1 measures the real baseline and corrects the table
  in the same pull request.

The gate also **ratchets down**: if the measured figure comes in under 36,864
bytes, the gate re-tightens to the measured value plus 10 %.

**4.2 The memory gate was inverted late, and the fairness contract had to catch
up (condition C-5) — now closed.**

The approved gate measures with TLS terminated *outside* the measured container,
because Next.js is idiomatically deployed behind a terminating proxy and
measuring our stack *with* TLS buffers against a Node process *without* them is
an ~18,000-byte asymmetry **against ourselves**. Our honest-measurement rule cuts
both ways: an unfair-to-ourselves comparison is still unfair. Until that boundary
is written into the equivalence specification, where it binds the Next.js side
too, the contract binds one stack only.

**Status: closed, hours after the review.** QA-2 landed the amendment while this
report was being written. The specification's §3.6 now carries the boundary rule
verbatim from the architecture RFC, adds a harness check that refuses to record a
memory result unless the measured container holds no TLS listener and the proxy
in front of it is the same image digest used for the other stack, and — taking
condition C-3's point in the same edit — requires the in-process-TLS secondary
figure to be **measured by re-running the same procedure, never derived** from
the composition budget it exists to test. (C-3 itself, the arithmetic fix in the
architecture RFC, stays with DEV-1 at Phase 1.) I accept it: it binds both stacks
identically and changes no product surface. **The risk that remains is ordinary
drift** — this specification is the fairness contract, and every later edit to it
must clear the same bar before Phase 5 quotes a comparable number.

**4.3 templ's bus factor — the weakest link in the dependency ledger.**

`a-h/templ` is the HTML component library gotth-live renders through, and PRD §4
makes it the **only** authoring path in v1, so there is no in-tree fallback. It
is healthy by every other measure — 10.4k stars, three releases in six months,
42 open issues — but roughly **85 % of its commits come from one author**. It
already exercised its leverage once during this phase: templ's declared Go
version forced gotth-live's toolchain floor from Go 1.24 up to Go 1.25, and we
accepted rather than pin backwards onto an older release of our only render path.
The open question (ledger item D4, owned jointly by me and the Principal
Engineer) is whether to pull backlog item BL-5 — a render adapter for the Go
standard library's own template engine — forward as insurance. It does not block
Phase 1, and I am not deciding it before we have felt the real cost of the
coupling.

**4.4 Eleven of the fourteen conditions are still outstanding** (C-5, C-6 and
C-13 were discharged during this gate close). None is a design defect; twelve of
the fourteen are document-consistency obligations that the fixes themselves
created — a correction in one document creating an obligation in another. The
risk is ordinary rot: they get more expensive the longer they sit, and the Principal
Engineer's instruction is to sweep them in the module-initialisation pull request
rather than carry them to Phase 2. The register is §5 below.

**4.5 QA-2 carries three Phase 3 obligations that are unmeasured defaults, not
open design questions.** They are called out here so they are not discovered
late:

- **O11** — the coalescing flush threshold (default 512, half the protocol
  ceiling) was chosen for margin, not measured. Too low costs extra frames to a
  client that is already behind; too high risks approaching the ceiling. Tune it
  against the dashboard workload.
- **O12** — the resync rate limit (minimum 1 second between requests, burst of 3)
  was set to make request amplification impossible, not tuned. A legitimate
  client on a lossy link may need to resync more often than that.
- **I6** — the provenance log runs at roughly **10.6 KB per second per session**
  at the dashboard workload. That is a real operational cost, and the question
  — whether a sampled-in-production/full-in-soak mode is wanted — is jointly
  QA-2's and mine, because the 100 %-provenance guarantee depends on the log
  being unsampled.

**4.6 One process item is unowned.** RFC-0001 open question O10 — wiring the
`candace gotth …` command group into the existing Python command-line tool — has
no assignee. It is small and it is Phase 1; it needs a name on it.

**4.7 The single large pull request remains a review risk** (R-17). The
deliverable is one pull request held to a Go-standard-library bar, and
consolidating Phases 1–3 increases the volume landing at once. The mitigations
are structural: coherent per-subsystem commits, QA sign-off recorded per
checkpoint rather than once at the end, and the Principal Engineer reviewing
continuously rather than at the end.

## 5. Conditions register — C-1…C-14

Each condition is an obligation with an owner and a due phase. **None blocks the
start of implementation.** Three rows are marked **APPLIED**: C-6 and C-13 in
requirements-document v0.3, committed with this report, and C-5 by QA-2 in a
parallel commit during this gate close.

| # | Condition, in short | Owner | Due | Status |
|---|---|---|---|---|
| **C-1** | RFC-0001 §3.5 still *proposes* the FR-2 amendment the requirements document already accepted. Rewrite in the past tense against the amended requirement. Design is correct in both documents; only the framing is stale | DEV-1 | Phase 1 (module-init pull request) | Open |
| **C-2** | RFC-0001 §7.1's per-slot arithmetic ("32 bytes × 16") contradicts the corrected 1,024-byte line in §6.2. Restate as 64 B × 16 | DEV-1 | Phase 1 | Open |
| **C-3** | RFC-0001 §6.2's *secondary* (in-process TLS) total is not derived by the table's own method — the TLS line is marked heap-resident but escapes the garbage-collector doubling every other heap line receives. Exempt it with a stated reason or restate the total. Diagnostic only; the gate is unaffected | DEV-1 | Phase 1 baseline | Open |
| **C-4** | RFC-0001 §11.1 cites a helper function the API ledger replaced with a constant. Align on the ledger's spelling | DEV-1 | Phase 1 | Open |
| **C-5** | **The TLS boundary must land in the equivalence specification §3.6, where it binds both stacks.** The spec contained zero occurrences of "TLS"; RFC-0001 §6.1.1 supplies transplantable text needing no edit. Until it lands, the fairness contract is one-sided | QA-2 (spec owner); PM-1 accepts | Before any Phase 1 memory baseline is quoted as comparable; **hard gate before Phase 5** | **APPLIED** — landed in commit `eb6cf6dd` during this gate close; §3.6 carries the rule verbatim plus a harness assertion, and takes C-3's point by requiring the in-process secondary to be measured rather than derived. PM-1 accepts; L9-1's freeze sign-off follows |
| **C-6** | **The requirements document's memory annotations were inverted relative to the approved decision.** Replace "≤64 KiB in-process (gate), ≤40 KiB external (secondary)" with **≤46,080 B with TLS outside** as the gate and in-process TLS as a labelled secondary with no target | PM-1 | **Before the Phase 0 gate is recorded closed** | **APPLIED** — PRD v0.3, at the preamble, goal G2, the Phase 5 gate line, open question Q2, and risk R-10. The transport placeholder is resolved to WebSocket in the same pass, both owning artifacts now being approved |
| **C-7** | `instrumentation.md`'s enabling API contradicts the API ledger: it names three `WithX(...)` functions the ledger cut in favour of `Config` fields, and implies a Prometheus registry where the settled decision is an OpenTelemetry provider | DEV-1 | Phase 1 | Open |
| **C-8** | Four signals introduced by the cycle-2 fixes are named in the architecture and protocol documents but missing from the instrumentation catalogue, which is authoritative: resync request counts, source-label overflow, outbound validation failures, and the `ack_channel_full` rejection reason. **The most likely of the fourteen to rot** | DEV-1 | Phase 1 | Open |
| **C-9** | `instrumentation.md` §2.1 and `protocol.md` §3.3 contradict each other on effect-source registration. protocol.md is correct — there is no registration step — so §2.1 and its cardinality warning need fixing | DEV-1 | Phase 1 | Open |
| **C-10** | The superseded `go 1.24` floor survives in the dependency ledger's body in three places after the decision moved to `go 1.25` | DEV-1 | Phase 1 | Open |
| **C-11** | Add the Go directive to the dependency ledger's standing measurement obligations, so the next time a dependency silently raises the consumer's toolchain floor it is caught at review time | DEV-1 | Phase 1 (module init) | Open |
| **C-12** | The three conditions attached to allowing a second exported package (a test-helper package): amend RFC-0001 §14.2's "one exported package" rule in the same pull request that creates the module; extend the architecture test to assert the main package does not transitively import Go's `testing` package; **two exported packages is a cap** — a third needs a Principal Engineer ruling | DEV-1 | Phase 1 | Open |
| **C-13** | **Reconcile FR-56's text with the three hooks actually shipped** — amend the requirement in the open, or record the API ledger's note as an accepted partial with a named revisit | PM-1 (wording) + DEV-1 (ledger) | Phase 0 close; Phase 2 if a consumer appears | **APPLIED (PM-1 half)** — FR-56 amended in PRD v0.3 to mount/event/teardown with patch observability delegated to instrumentation, plus the Phase 2 exit criterion reworded to match. DEV-1's ledger note remains, at Phase 1 |
| **C-14** | ADR-001's transport memory-share ceiling rose from 12 KB to 16,384 B, accepted because the old ceiling was already breached by the design it bounded. Condition: it is now a **derived** number, so if any of its four component lines moves, both change in the same pull request and the ceiling never becomes the looser of the two | DEV-1 + QA-2 | Phase 1 baseline | Open |

## 6. Next phase plan

**6.1 Phases 1–3 run as one consolidated delivery track**, on a single project
branch, reviewed as one body of work (PRD §6). Consolidation changes review
packaging only: every exit criterion remains individually checked, QA-1 and QA-2
record sign-off **per checkpoint** rather than once at the end, and the Principal
Engineer's veto applies continuously. **The Phase 1 checkpoint is a hard ordering
constraint** — the counter demonstration must work end to end, with provenance
resolvable and the size budget met, before Phase 2 work is reviewed. We are not
building a component model on an unproven core loop.

**Checkpoint 1 — the core loop.** Connection lifecycle (handshake, authentication
binding, origin validation, heartbeat, enumerated close codes); the session actor
— one goroutine, one bounded mailbox, verified race-free under concurrent event
injection; pure reducer and pure render with a repeated-render byte-equality
test; render plus DOM morph in the browser; and the **counter example** working
end to end: click → event → reducer → render → patch → morph → visible change.
Shipping with it, not after it: metrics and traces flowing with one option each,
the provenance test that takes a patch captured off the wire and resolves it back
to its originating event, the hostile-wire-data suite, the cross-origin attack
test, the leak test over 10,000 connect/disconnect cycles, and the client runtime
under its 12,288-byte ceiling with the per-subsystem size ledger reporting.
Latency is **measured and published** at this checkpoint; the latency gate itself
is enforced in Phase 5.

**Checkpoint 2 — the component model.** The chat example, which is where
multi-user behaviour, forms, per-event authorisation, error boundaries and
server-initiated updates all become real. Plus the full DOM-preservation
conformance suite across the browser matrix (focus, caret, scroll, uncontrolled
input values, input-method composition), coexistence with HTMX on the same page,
and mounting under three different Go HTTP routers unchanged.

**Checkpoint 3 — resilience.** QA-2's chaos suite is the gate: connection dropped
mid-patch, sequence gaps, server restart under load, throttled slow client,
hostile event flood, network partition and half-open connections, 10,000-cycle
churn, and duplicate frames — each with a defined, tested outcome and no
unbounded memory anywhere. The live-dashboard example lands here, along with
batching and backpressure metrics, and QA-2's three unmeasured defaults (§4.5)
get tuned against a real workload.

**6.2 The build environment is a container, and it is where the no-npm promise is
enforced structurally.** The host has no Go toolchain; everything runs in a
`dis` development container, mirroring what the refinement-types research in this
repository already does. `.dis/Dockerfile` is the library image — Go 1.26, the
protocol-buffer compiler, and the templ code generator, **and deliberately no
node**. `.dis/Dockerfile.bench` derives from it and adds pinned node and npm; it
is the **only** image in the project with node in it. That makes the benchmark
quarantine a property of the file layout rather than a promise, and it means the
"clean clone runs with no node, npm, or code generators installed" guarantee is
verified continuously in the library image rather than as a Phase 5 ritual. A
`candace gotth …` command group (`up`, `test`, `gen`, `size`, `example`, `bench`)
follows the existing command-line tool's conventions in a separate pull request —
**and currently has no owner** (§4.6).

**6.3 Where the Next.js benchmark work begins.** The measurement itself is Phase
5, but three things happen before it and two have already happened:

1. **The equivalence specification is written and agreed** (Phase 0) — the
   product surface of both apps, and the operational definition of every measured
   word, fixed before any number exists.
2. **Condition C-5 lands** — the TLS boundary written into that specification so
   it binds the Next.js side too, with my acceptance. **Done during this gate
   close.**
3. **The comparison app is built under `gotth-live/bench/`**, quarantined from
   the Go module with pinned versions and a committed lockfile, in the
   node-bearing container image only. All three live-data variants are measured —
   streaming Server-Sent Events (primary), a dedicated WebSocket server
   (secondary), and polling — and **no variant may be dropped for schedule**
   without a requirements amendment from me. That rule exists because the
   WebSocket variant is precisely the one an informed critic would assume we
   omitted, being the one that competes with us on latency and memory.

Two operator-facing notes on the benchmark: the report publishes **every
dimension where Next.js wins with equal prominence**, in the same table, with no
softening — that is a requirement, not an aspiration — and the benchmark harness
**does not authorise itself to run**. Bench runs are operator-initiated by design,
and the harness refuses to start unless explicitly invoked.

**6.4 Phases 4 and 5 are deliberately not consolidated.** Phase 4 gates on QA-1
building a working application **from the documentation alone**, which is invalid
if run against undocumented in-flight work; Phase 5 produces the numbers that
ship. The end deliverable across all phases remains one pull request against
`main`, organised into coherent commits with per-checkpoint sign-offs recorded in
the description.

## 7. Phase exit statement

**The Phase 0 design package is approved for implementation. The gate criteria
are all met or condition-tracked, and Phase 0 is closed.**

All four core design documents clear the Principal Engineer's design checklist.
The six questions he had ruled must not be deferred — transport, state
serialisation for resync, per-event authorisation, origin validation and
authenticated connection establishment, the memory ceiling, and slow-client
eviction — are each answered with a mechanism, a number, and a falsifier; none
is deferred. Every one of the twelve blocking objections from the first review
pass is genuinely closed, verified against the text rather than the changelog.
The functional core is uncompromised, the provenance chain is now closed at both
the outbound boundary and the resync boundary, and the budgets are numbers with
methods and pre-registered responses to a miss.

The condition that gated the *recording* of this gate — C-6, the inverted memory
annotation in the requirements document — is applied in PRD v0.3, committed
alongside this report. C-13 is applied in the same revision. **C-5 — the one
condition whose omission would have degraded a *result* rather than a document —
was applied by QA-2 during this gate close and is accepted.** Eleven conditions
remain open, all owned, all cheap, and all due in Phase 1, to be swept in the
module-initialisation pull request rather than carried forward.

Implementation may begin.

— PM-1, Product Manager, 2026-08-04
