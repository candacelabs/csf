# Phase 0 design package — L9-1 review, cycle 2 of 2 (final)

| | |
|---|---|
| **Reviewer** | L9-1 (Principal Engineer) |
| **Date** | 2026-08-04 |
| **Cycle** | 2 of a maximum 2. **This verdict is final.** There is no cycle 3; nothing below is returnable. |
| **Reviewed against** | [review checklist §11](../review-checklist.md), plus the cycle-1 objections B-1…B-12 and advisories A-1…A-13 |
| **Reviewed revisions** | `83954b5f` (ADR-001) · `086ff218` (protocol.md) · `e87a0dc3` (RFC-0001) · `8a5b92bf` + `efb8786f` (instrumentation.md) · `087a6395` (ledgers) |
| **Prior cycle** | [001-review-cycle-1.md](001-review-cycle-1.md) — decisions **D1–D6** settled there and not reopened here |

## Verdicts

| Document | Cycle 1 | **Cycle 2 (final)** | Blockers outstanding | Conditions |
|---|---|---|---|---|
| [ADR-001 — transport](../adr/001-transport.md) | APPROVE | **APPROVE** | 0 | 1 (C-14) |
| [RFC-0001 — architecture](001-architecture.md) | RETURN (5) | **APPROVE-WITH-CONDITIONS** | 0 | 5 (C-1…C-4, C-12) |
| [protocol.md](../protocol.md) | RETURN (5) | **APPROVE** | 0 | 0 own; feeds C-8, C-9 |
| [instrumentation.md](../instrumentation.md) | RETURN (2) | **APPROVE-WITH-CONDITIONS** | 0 | 3 (C-7, C-8, C-9) |
| [api-surface.md](../api-surface.md) *(first review)* | — | **APPROVE-WITH-CONDITIONS** | 0 | 2 (C-12, C-13) |
| [dependencies.md](../dependencies.md) *(first review)* | — | **APPROVE-WITH-CONDITIONS** | 0 | 2 (C-10, C-11) |

**All twelve blocking objections are genuinely closed.** I walked each one to the
text rather than to the changelog, and in every case the change is real, lands
where I said it had to land, and says what the changelog claims it says — with
one exception (**C-1**), where the changelog asserts an edit that was not made.
Three of the twelve — B-3, B-7, B-11 — are answered better than I asked.

Two of the fixes changed the design rather than the prose, and both changes are
improvements: `chan *inbound` over `chan inbound` (B-1) takes 6,656 B off every
idle connection, and a 32-byte `spanRef` over a `trace.SpanContext` (B-12) takes
another ~512 B off the ack window. A revision cycle that *reduces* the number it
was asked to correct is doing the work properly.

Fourteen conditions follow. **None of them is a design defect.** Twelve are
document-consistency obligations produced by the fixes themselves — a fix in one
document creating an obligation in another — and two (C-5, C-6) are edits owed by
owners other than DEV-1. They are tracked, not waived, and each carries an owner
and a phase.

---

## 0. Two things on the record before the walk

1. **DEV-1 declined nothing, and that was the right call — but I want the record
   to show that B-9 was contested-worthy and was conceded on the merits.** I said
   in cycle 1 that B-9 was the objection most likely to be argued. protocol.md's
   changelog answers it with "**Accepted without contest** — this was the
   objection most likely to be argued and I do not argue it," and then implements
   it as `ValidateOutbound` on the single write path, not optional, with a
   failure metric and an ADR requirement to remove it. That is the correct
   disposition and it is the one that turns P1 from a discipline into a property.
2. **The changelog-driven review format worked, and it also caught its own
   failure.** Every revised document maps objection ID → change, which made this
   pass a diff walk instead of a re-read. It is precisely because the format is
   reliable that **C-1** matters: one row of RFC §17 claims §3.5 "now cites the
   amended FR-2 rather than proposing a change," and §3.5 is unchanged. A
   changelog that is right 97 % of the time is trusted, which is what makes the
   3 % expensive. Fix it and keep the format.

---

## 1. Blocking objections — disposition walk

### RFC-0001

**B-1 — mailbox slots costed as free — FIXED, and the fix improved the design.**

§6.2 now carries three separately sized lines where cycle 1 had one wrong one:
mailbox backing array **512 B**, ack channel backing array **256 B**, unacked
window **1,024 B** — 1,792 B against the old 1,500 B line that was supposed to
cover two of the three and did not include what instrumentation §3.3 stores.
§3.3 states the coupling in the terms I asked for: *"Mailbox capacity is
therefore a memory parameter as well as a flood-control parameter."*

The design change behind it is the part worth noting. Recognising eager
allocation led to `chan *inbound` with pooled structs: 64 × 8 B = 512 B instead
of 64 × ~112 B = 7,168 B. That is 6,656 B per idle connection, 14 % of the new
gate, recovered from an objection about an arithmetic error. §6.4 now lists it as
the fifth architectural decision the ceiling rests on, which is the right place
for it — it means reversing it is visibly a budget decision.

Arithmetic verified line by line: heap-resident subtotal **10,516** (4,096 +
2,000 + 2,000 + 128 + 512 + 256 + 1,024 + 500 ✓), non-heap 21,384, GC headroom
10,516, total **42,416** ✓, headroom against 46,080 = 7.95 % ✓, Blazor ratio
256,000/46,080 = 5.56× ✓.

*Residual:* §7.1 still reads "32 bytes × 16 = **512 bytes**" for the same window
§6.2 now sizes at 1,024 B. That is the exact sentence B-1 named. → **C-2**.

**B-2 — unbounded ack channel — FIXED.**

`acks chan uint64` cap **32**, policy **drop-and-count** on
`gotthlive_frames_rejected_total{reason="ack_channel_full"}`, in a §3.3 table
that gives every one of the three inputs a capacity, a policy, and a metric —
which is what checklist §6.6 asks for per queue.

The justification is better than a tolerance argument: `Ack.server_seq` is a
cumulative high-water mark, so a dropped ack is superseded by the next one and
the window re-opens one round trip later. Dropping is lossless *in the limit*,
which is exactly why blocking is indefensible here and unbounded is a memory
vector. That is a protocol property doing the work, not a shrug.

§11.3's claim is tightened to my formulation verbatim — *one goroutine owns
session state; three typed inputs; only the mailbox reaches a reducer* — and
§3.1's struct lists all three (which also discharges A-1).

*Residual:* the new `ack_channel_full` label value is not in instrumentation
§2.2's enumeration. → **C-8**.

**B-3 — TLS comparability and un-pre-registered remedy — FIXED, and better than
what I asked for.** Ruled on in §5.1 below.

**B-4 — `contributing_event_ids` overflow — FIXED exactly as specified.**

The H-4 bound is now a **flush trigger**: at `coalesce_flush_at` (default 512,
half the ceiling) the actor emits the coalesced patch rather than continuing to
coalesce. §7.4 states it and states both rejected alternatives and why each is
wrong (truncation falsifies P5 silently; erroring lets a slow client kill its own
session by a path nobody designed). protocol.md H-4 cross-references it, the
`Limits` ledger carries `CoalesceFlushAt`, and new **O11** flags the 512 default
as chosen for margin rather than measured, owned by QA-2 at Phase 3. Provenance
is now unloseable on this path by construction rather than by bound.

**B-5 — `ResyncRequest` amplification — FIXED, and the closure is complete.**

New §7.6: `MinResyncInterval` 1 s and `ResyncBurst` 3 in a bucket **independent
of** `MaxEventsPerSecond`, `Error{RATE_LIMITED}` with **no render** on a
too-early request, close `4008` on sustained abuse,
`gotthlive_resync_requests_total{result}` with a three-value domain. Carried
through to protocol.md **H-14**, `Limits` ×2, §11.5's limit list, and exit
criterion **E13** with a falsifier.

The addition I did not ask for and would have: the **no-op short circuit** — if
`last_applied_seq` already equals `server_seq` there is no gap, so the reply is
an `Ack`, not a `Snapshot`. That makes the common spurious request free rather
than merely rate-limited, which is the difference between a mitigation and a fix.
This was the one genuine security gap in the cycle-1 package and it is closed.

### protocol.md

**B-6 — `protocol_version <= 15` — FIXED.**

The upper bound is gone; §3.1 is `where this > 0` and states the layering I asked
to have confirmed: *refinements reject what cannot be parsed; H-2 rejects what
cannot be served, with a reason.* The `0`-is-indistinguishable-from-unset
argument for keeping the lower bound is correct.

*Residual, fixed by me in place:* §8.2 still described the middle layer as
"refined to `1..15`" — a stale reference directly contradicting the section it
points at, and the one place I explicitly asked to have re-read. See §7 below.

**B-7 — P6 unsatisfiable for server-initiated frames; resync had no origin —
FIXED, and this is the best work in the revision.**

Three changes, all correct:

1. P6 restated with PRD G4's disjunction, and its test now exercises **both
   arms** — a `CLIENT_EVENT` patch and an `EFFECT`/`TIMER`/`PUBSUB` patch. A
   property whose test only ever ran one arm was not being tested.
2. `ResyncRequest` mints an `event_id`; the resulting `Snapshot` carries
   `Origin{kind: RESYNC, event_id, client_ref}`; `RESYNC` is removed from §4.2's
   `event_id = 0` list. Verified in the text, not just the changelog.
3. **New schema fields** `Snapshot.superseded_from_seq` / `superseded_through_seq`
   (10, 11) plus new §4.3 and cross-field invariant **H-13**, and P7 restated as
   a causal property rather than a reconciliation category.

§4.3's argument for a *range* over the union of contributing event IDs is the
right one and it is the argument I would have made second: the union is
unbounded, it would collide with H-4, and it would reintroduce exactly the
truncation-is-provenance-loss problem B-4 just closed — while the range is two
varints, is exact, and is sufficient *because P8 guarantees the superseded
patches are themselves in the capture*. That last clause is what makes it sound;
it is stated, not assumed. Provenance now survives the resync boundary.

**B-8 — the close frame is also not a `Frame` — FIXED.**

§1 is rewritten around the amended checklist §3.1 carve-out and discharges both
of its obligations explicitly. The audit is now defined **by WebSocket opcode**
in a five-row table covering `0x2`/`0x1`/`0x8`/`0x9`/`0xA` and the HTTP upgrade
bytes. The row I most wanted is there: ping/pong are counted, excluded by opcode,
and a non-zero count is **reported, not hidden** — which is the same
degradation-must-have-a-signal rule the rest of the package runs on, applied to
the audit itself. E5/X4's "zero non-`Frame` bytes" now has a scope instead of a
literal reading that failed on every clean disconnect.

**B-9 — outbound frames never crossed a `Refine*` boundary — FIXED, conceded on
the merits.**

New §5.3 `ValidateOutbound`: the ingress pipeline minus the unmarshal, called by
the framer immediately before `proto.Marshal`, on the single write path, not
optional. Failure drops the frame, increments
`gotthlive_outbound_validation_failed_total{kind}`, and emits an `Error` with the
causal chain intact via §9's panic-guard path — treating a frame we cannot
construct correctly as a library bug, which it is. P1's justification is restated
in terms of the boundary rather than "unparseable otherwise." RFC §3.2 step 5
routes emission through it, so the two documents agree. Removing it later
requires an ADR with a measurement, which is the correct default direction.

*Residual:* the new metric is not in instrumentation §2.2. → **C-8**.

**B-10 — audit independence overclaimed — FIXED.**

§10.3 now states the narrowing in one paragraph: independent **as to framing**,
only **partially as to field validity**, because the codec enforces length
predicates and nothing else — and names §5.3 as what actually covers the gap.
This is the honest form and it is the form `docs/` must keep.

### instrumentation.md

**B-11 — the provenance log was load-bearing everywhere and specified nowhere —
FIXED. This is the single strongest piece of cycle-2 work.**

New §4A answers every question the objection listed, and answers the hardest one
first: **it is a structured log stream, not a library-owned store.** One record
per transition, fixed field set, dedicated `gotthlive.provenance` logger, exempt
from `Info` sampling, stored and queried by the operator's existing pipeline.
§4A.1 costs out the in-memory-ring alternative (~95,400 records/session for G4's
30-minute soak) and rejects it on the numbers.

Three parts deserve specific approval:

- **§4A.5** — the independence property. The log is emitted by the actor in
  `step`; the counters it audits are incremented in the framer and the transport;
  different path, different sink, different scrape. *"A PR that couples them is a
  block."* That is the property checklist §4.5 exists to obtain, stated as a
  reviewable rule rather than an aspiration.
- **§4A.3** — the distinction that was being blurred everywhere: *the frames
  always carry the causal chain; the provenance log is the server-side index that
  makes the reverse lookup possible.* Disabling the log costs queryability, never
  wire-level provenance. That sentence should survive into the godoc.
- **§4A.2/§4A.4** — ≈200 B per record, ≈10.6 KB/s/session at the dashboard
  workload, and new **I6** flagging that volume as an operational question for
  QA-2 + PM-1. A number that lets an operator plan, rather than discover.

With §4A defined, §3.5's excuse for 5 % trace sampling now points at an artifact
that exists, which is what my cycle-1 acceptance of that default was conditioned
on. That condition is discharged.

**B-12 — per-slot span-context accounting understated and double-counted —
FIXED, and the fix reduced the cost.**

§3.3 now stores a compact **32-byte `spanRef`** (`TraceID`, `SpanID`,
`traceFlags`) and reconstructs the `SpanContext` at link time — exact for this
use because `TraceState` is never populated — instead of a real `trace.SpanContext`
at ~56–64 B. 16 × 64 B = **1,024 B**, and it is now its own §6.2 line rather than
"declared already covered." Both errors are fixed and the correction of my own
size estimate is accurate.

---

## 2. Advisories — all thirteen applied, none declined

| # | Verified | Note |
|---|---|---|
| A-1 | ✓ | §3.1 lists `acks` and `ticker` with capacities. |
| A-2 | ✓ | §6.2 gains a **Heap?** column, an explicit 10,516 B heap subtotal, and a **derived** GC line. The figure barely moved (10,516 vs the guessed 10,000) — the value is that the method is now legible. |
| A-3 | ✓ | Applied, and it forced a decision the note had deferred. Ruled on in §5.2. |
| A-4 | ✓ | §5.1 option (b) now leads with *"the mechanism's defining production failure is that ordinary code silently defeats it"* and demotes the fork-a-dependency argument. |
| A-5 | ✓ | ADR §4.5.2 → RFC §10.4. |
| A-6 | ✓ | ADR §5 F2 → `gotthlive_connections_closed_total`. |
| A-7 | ✓ | **Changed a number.** Mapping X3 to its four §6.2 lines showed 14,788 B against a 12 KB ceiling — the ceiling was already breached by the design it bounded. Ruled on in **C-14**. |
| A-8 | ✓ | Q-P2 **closed**, not deferred. H-5 named as the single authoritative limit because it is the only one enforced before allocation; the field and list bounds are explicitly subordinate defence-in-depth *so nobody removes them later as redundant*. `len(Event.fields)` reduced 256 → 64. Exactly the reconciliation I asked for. |
| A-9 | ✓ | §9 records both reasons, and the second is the stronger one: a direction-dependent predicate is **inexpressible** in a grammar with no free variables, so the alternative costs the totality of `len(this) == 16`. |
| A-10 | ✓ | §3.3 states the prefix vocabulary and resolves the registration gap correctly — there *is* no registration step, so cardinality is capped at the metric. See **C-9**: the resolution contradicts instrumentation §2.1, which still says the opposite. |
| A-11 | ✓ | protocol.md §8.3 gains a `code` label column; instrumentation §2.2 enumerates the fourteen lower-case values and forbids the numeric and upper-case forms. One source, three consumers. |
| A-12 | ✓ | `gotthlive_heap_bytes_per_session_mean` dropped; the undivided pair exported instead; QA-2's ownership of I5 correctly preserved rather than overridden. |
| A-13 | ✓ | §4.2's nil-check bullet is MUST-phrased, names the no-op-interface anti-pattern, and states that a PR routing a hot path through one fails the requirement. |

---

## 3. Cross-document consistency spot-check

I checked the five places a fix was most likely to break something else.

| Surface | Result |
|---|---|
| **RFC §6.2 memory table** | Arithmetic correct line by line; subtotal, total, headroom %, and the Blazor ratio all reproduce. One method inconsistency in the *secondary* row → **C-3**. |
| **protocol.md H-1…H-14** | All fourteen present, no gaps, no duplicates, each with an enforcement site and a test. H-13 is printed after H-14 in the §6 table — ordering only, non-blocking `nit:`. |
| **RFC §15 E-numbers** | E1–E13 all present and each carries a falsifier. E12/E13 were printed out of order; **fixed in place** (§7). E13 is referenced correctly from §7.6. ADR X1–X6 intact. |
| **api-surface `Limits` ledger** | The five new fields match the RFC exactly, defaults included: `MailboxDepth` 64, `AckChannelDepth` 32, `MinResyncInterval` 1 s, `ResyncBurst` 3, `CoalesceFlushAt` 512, `AckWindow` 16. Identifier arithmetic 86 + 14 = 100; §8 said 95 — **fixed in place**. Two symbols referenced elsewhere are absent from the ledger → **C-4**, **C-7**. |
| **dependencies ledger** | D1's three conditions and D2's one condition are all recorded as ledger obligations, correctly. The templ `go` directive resolution reached the changelog but not the body → **C-10**. |
| **metric catalogue (checklist §4.8, §9.5)** | Four signals introduced by cycle-2 fixes are named in the RFC or protocol.md and absent from instrumentation.md → **C-8**. This is the single most common knock-on and the one most likely to rot. |

---

## 4. The two queued API-surface rulings

### A1 — Two exported packages (`live` + `live/livetest`) vs RFC §14.2's one. **ACCEPTED.**

The `net/http/httptest` precedent is the right precedent and the argument is
better than precedent alone. PRD **FR-15 requires** the library to ship a
determinism helper; `ReplayN` and `AssertDirtyComplete` take `testing.TB`; and
importing `testing` from `live` would put `testing`, and with it `flag`,
`regexp`, `runtime/pprof` and `runtime/trace`, into the transitive import set of
every consumer's production binary. That is a concrete, measurable cost, not a
taste preference — which is the standard I hold every other structural claim in
this package to.

The stdlib does exactly this and does it repeatedly: `net/http/httptest`,
`testing/fstest`, `testing/iotest`, `testing/quick`. FR-65's concern is
*surface*, not *package count*, and the surface is unchanged by where the eight
livetest symbols live — they are counted either way.

Three conditions, all in **C-12**:

1. **RFC §14.2 is amended in the same PR that creates the module**, so "one
   exported package, `live`" stops being the written rule while two ship. I will
   not have a repeat of the FR-2 pattern where a document mandates a mechanism
   the design has already declined.
2. **The justification becomes a test, not a claim.** The architecture test that
   already asserts `internal/{session,render,protocol}` do not import
   `internal/wsx` gains one assertion: `live` does not transitively import
   `testing`. The entire argument for the second package is that claim; an
   unverified claim is how it quietly becomes false at the first convenient
   import.
3. **Two is the cap.** A third exported package requires an L9-1 ruling.
   `live/livetest` is admissible because production code must not link it; that
   reasoning does not generalise to a `live/middleware` or a `live/otel` arriving
   later on convenience grounds. (Note: D1's pre-registered Option-B fallback
   would create `gotth-live/otel` as a *separate module*, not a third package of
   this one — that path is already ruled on and is unaffected.)

### A2 — No FR-56 patch hook. **READING ACCEPTED. I do not name a consumer, because there isn't one.**

I looked for the consumer before accepting the argument, which is the only honest
way to answer "name it or accept it."

- FR-56's own sufficiency test — subscribe to a pubsub topic on mount,
  unsubscribe on teardown, without leaking — is satisfied by `Config.Init` and
  `Config.Teardown`. RFC §8.1 makes cross-tab shared state an application concern
  solved by exactly that pair, so the requirement's motivating scenario is
  covered.
- The two things in this design that genuinely want per-patch visibility are the
  **Phase 4 dev inspector** (FR-44) and **`livetest.Client.WaitFor`** — and
  neither is application code. The inspector reads the instrumentation stream;
  `WaitFor` lives in `livetest`. Both are served without a `Config` hook, which
  is evidence *for* the reading rather than merely absence of evidence against.
- Patch observability is now genuinely delegated rather than promised:
  `gotthlive_patches_sent_total{op}`, the `gotthlive.encode`/`gotthlive.send`
  spans, and the §4A provenance log's per-transition record. That delegation
  would have been hollow in cycle 1 — B-11 is precisely the objection that made
  it hollow — and B-11 is now closed. **The two rulings are connected: I can
  accept this one because §4A exists.**
- An `OnPatch` field would be an export with no named call site, which checklist
  §1.4 forbids and FR-65 makes a rejection trigger. I am not going to enforce
  §1.4 against a `Transport` interface and then wave through a lifecycle hook on
  weaker grounds.

**One condition (C-13), and it is not optional:** FR-56's *text* asks for four
hooks and the surface ships three. That disagreement must be reconciled in the
requirement, not left as a footnote in §7.1 — PM-1 either amends FR-56 to name
mount/event/teardown and record that patch observability is instrumentation's,
or records §7.1 as an accepted partial with a named revisit. FR-2 is the
precedent for how this repo handles a requirement the design declines: amend it
in the open, with the reasoning attached. A shipped surface and a requirement
text that disagree silently is how the next reviewer gets misled.

If a real consumer appears later — an application that must audit patches from
its own code rather than from telemetry — the hook lands in Phase 2 **with that
consumer named in the PR**. Not before.

---

## 5. The two cycle-2 decisions that changed substance

### 5.1 The TLS gate inversion — **APPROVED**, with three conditions

The gate is now **≤ 46,080 B with TLS terminated outside the measured
container**, with in-process TLS as a labelled secondary carrying no target. I
asked for a decision about which figure enters the comparison and a
pre-registered rule; I got a better answer than either.

Three reasons I approve it, in order of weight:

1. **It closes the outcome-shopping vector structurally rather than
   procedurally.** B-3(b) asked for a pre-registered rule because choosing
   between "move the target" and "move TLS out of the container" *after* seeing
   the number is exactly what the equivalence spec exists to prevent. §6.1.2's
   first bullet does something better: because the gate is the TLS-outside
   figure, **there is no measurement outcome for which changing the TLS boundary
   is an available remedy.** The rule becomes residue rather than load-bearing.
   Removing the incentive beats regulating it.
2. **The symmetry argument is correct and it cuts against DEV-1's own interest.**
   Measuring gotth-live with `crypto/tls` record buffers against a Node process
   without them is an ~18,000 B asymmetry *in our disfavour*. FR-73's honesty
   clause cutting both ways — an unfair-to-ourselves comparison is still an
   unfair comparison — is the right reading, and a team that corrects a
   benchmark in its own favour on a symmetry argument has to be held to the same
   standard when the correction runs the other way. §6.1.1's "disqualifying
   method error, **in either direction**" is that standard, written down.
3. **The gate got harder, not easier.** 46,080 B against cycle 1's 64 KiB is a
   ~28 % tightening, and §6.1.2 adds a **ratchet-down clause** — under 36,864 B
   and the gate re-tightens to measured + 10 % in the same PR. I have rarely been
   handed a target that can tighten itself. Nobody should read this inversion as
   a relaxation; it is the opposite, and 7.9 % headroom on an estimate with three
   remaining unmeasured lines is genuinely tight. If the WebSocket conn struct is
   2× the estimate the gate is breached, and §6.1.2 correctly forecloses the
   escape: the target does not move, the overage is attributed to a named line
   and engineered down, and only an ADR with the measurement in hand moves it.

Conditions **C-3**, **C-5**, **C-6**.

**C-5 is the one that matters.** §6.1.1 is written as transplantable text for
equivalence-spec §3.6 — same proxy image, separate container, proxy excluded from
`M(x)` — and the transplant has not happened. The equivalence spec still contains
**zero** occurrences of "TLS". Until QA-2 lands it, the fairness contract binds
gotth-live and not the Next.js side, which is the asymmetry the inversion exists
to remove, pointed the other way. RFC §6.1.1's text may be transplanted verbatim;
it needs no editing, only an owner.

### 5.2 The toolchain floor moving to `go 1.25` — **APPROVED**, with two conditions

This is a forced move, not a chosen one, and the RFC says so plainly: `a-h/templ`
v0.3.1020 declares `go 1.25.0`, a dependency's directive raises the floor whether
we like it or not, and PRD §4 makes templ the only v1 authoring path. Only two
options existed and the right one was taken.

Pinning templ backwards to hold a 1.24 floor would freeze the only permitted
authoring path, on the Tier-1 dependency with the weakest bus factor in the
ledger (~85 % single author, dependencies.md §1.3 / D4), to buy consumers one
minor version of a toolchain that `go/go.mod` in this repository already
declares. That trade costs upstream fixes and gains close to nothing. My cycle-1
A-3 said the `go 1.24` floor was well-argued; the ledger produced a fact I did
not have when I wrote that, and the fact wins. Correctly, the RFC also keeps the
monorepo's own 1.24/1.25 discrepancy out of scope rather than waiting on it.

Conditions **C-10** and **C-11**. C-11 is the durable one: this episode happened
because a Tier-1 dependency silently moved a consumer-visible floor, and
dependencies.md §5's standing measurement obligations list five items, none of
which is "the `go` directive." Add it, and the next occurrence is caught at
review time instead of at ledger-writing time.

---

## 6. Conditions register

Each is an obligation, not a suggestion. Each has an owner and a phase. None
blocks the start of implementation.

| # | Condition | Owner | Phase |
|---|---|---|---|
| **C-1** | **RFC §3.5 still proposes the FR-2 amendment that PM-1 already accepted** (`PRD.md:243`) — the heading, "Proposed resolution, for PM-1 and L9-1," and "This RFC requests a PRD amendment" are unchanged, while RFC §17's D4 row claims the section now cites the amended FR-2. Rewrite §3.5 in the past tense against amended FR-2. **The design is settled and correct in both documents; only the framing is stale.** | DEV-1 | Phase 1 (module-init PR) |
| **C-2** | **RFC §7.1's "32 bytes × 16 = 512 bytes"** contradicts §6.2's 1,024 B window line and instrumentation §3.3's 64 B/slot. This is the line B-1 named. Restate as 32 B ack metadata + 32 B `spanRef` = 64 B × 16. | DEV-1 | Phase 1 |
| **C-3** | **RFC §6.2's secondary total is not derived by §6.2's own method.** The `crypto/tls` line is marked heap-resident but is added without the GOGC doubling every other heap line receives; ≈62,000 is neither 60,416 (undoubled) nor 78,416 (doubled). Either exempt the line with a stated reason or restate the secondary. Diagnostic only — the gate is unaffected. | DEV-1 | Phase 1 baseline |
| **C-4** | **RFC §11.1 cites `live.AllowAnyOrigin()`**, which api-surface §4 replaced with the const `AnyOrigin` and §7 records as cut. Align on the ledger's spelling. | DEV-1 | Phase 1 |
| **C-5** | **The TLS boundary must land in equivalence-spec §3.6**, where it binds both stacks. The spec currently contains zero occurrences of "TLS"; RFC §6.1.1 supplies transplantable text needing no edit. Until then the fairness contract is one-sided. | QA-2 (spec owner), PM-1 to accept | Before any Phase 1 memory baseline is quoted as comparable; **hard gate before Phase 5** |
| **C-6** | **PRD's memory annotations are inverted relative to the approved decision** — the preamble and §7.2 Q2 still record "≤64 KiB with TLS in-process (gate), ≤40 KiB external as secondary." G2's target is `set by RFC-0001`, so the PRD is now the stale authority on a number this review just approved. | PM-1 | **Before the Phase 0 gate is recorded closed** |
| **C-7** | **instrumentation.md's enabling API contradicts the API surface.** §2 says `live.WithMetrics(reg)`, §3 says `live.WithTracing(tp)`, §2.1 says `live.WithPerSessionMetrics()`; api-surface cut every `WithX` in favour of `Config.Metrics`/`Config.Tracer`/`Config.Logger`. `reg` additionally implies a Prometheus registry where D1 settled on an OTel `MeterProvider`, and `WithPerSessionMetrics` is an exported symbol in no ledger. FR-38's "exactly one option" is satisfied by one field either way. | DEV-1 | Phase 1 |
| **C-8** | **Four cycle-2 signals are absent from instrumentation.md's catalogue**, which checklist §4.8/§9.5 make authoritative: `gotthlive_resync_requests_total{result}` (RFC §7.6), `gotthlive_source_label_overflow_total` (protocol §3.3), `gotthlive_outbound_validation_failed_total{kind}` (protocol §5.3), and the `ack_channel_full` value of `gotthlive_frames_rejected_total{reason}` (RFC §3.3). Add them, and give each a row in §5.1 or state why it needs none. | DEV-1 | Phase 1 |
| **C-9** | **instrumentation §2.1 and protocol §3.3 contradict each other on effect-source registration.** §2.1 says `source` values come "from the registered effect sources… fixed at startup"; protocol §3.3 (the A-10 fix) says there **is** no registration step and caps the label at 64 values with an overflow counter. **protocol.md is correct** — an `Effect` is an application type and nothing registers it. Fix §2.1, and re-check §2.1's "product at startup exceeds 1,000 series" warning, which assumes the registration that does not exist. | DEV-1 | Phase 1 |
| **C-10** | **The `go 1.24` floor survives in the dependency ledger's body** after the changelog closed D2 at `go 1.25`: §1.1 ("below our `go 1.24` floor"), §1.3 ("above our intended `go 1.24` floor… the RFC's `go 1.24` should be corrected"), and api-surface A5's parenthetical. | DEV-1 | Phase 1 |
| **C-11** | **Add the `go` directive to dependencies.md §5's standing measurement obligations.** A Tier-1 dependency moved a consumer-visible toolchain floor silently; every `go.mod`-changing PR should quote each Tier-1 dependency's directive alongside the module-count and binary deltas. | DEV-1 | Phase 1 (module init) |
| **C-12** | **Ruling A1's three conditions:** amend RFC §14.2's "one exported package" in the module-init PR; extend the architecture test to assert `live` does not transitively import `testing`; two exported packages is a cap, a third needs an L9-1 ruling. | DEV-1 | Phase 1 |
| **C-13** | **Ruling A2's condition:** reconcile FR-56's text with the three hooks shipped — amend the FR in the open (the FR-2 precedent) or record api-surface §7.1 as an accepted partial with a named revisit. A requirement and a surface must not disagree silently. | PM-1 (wording) + DEV-1 (ledger) | Phase 0 close; Phase 2 if a consumer appears |
| **C-14** | **ADR-001 X3 rose 12 KB → 16,384 B under advisory A-7. Accepted** — the old ceiling was already breached by the design it bounded (14,788 B across four §6.2 lines), and a derived number with 9.7 % stated headroom is strictly better than an unmeasured one that was quietly false. Condition: X3 is now **derived**, so if any of its four §6.2 lines moves — O2's possible third goroutine is the live risk — X3 and §6.2 change in the same PR, and X3 never becomes the looser of the two. | DEV-1 + QA-2 | Phase 1 baseline |

**Non-blocking nits** (no obligation, fix if you are in the file): protocol.md §6
prints H-13 after H-14; instrumentation.md §5's table is referenced as "§5.1"
throughout but the heading does not exist (§5.2 does).

---

## 7. Changes I made in place

Typo-grade only, per the terms of this cycle. Recorded so they are not mistaken
for DEV-1's work:

1. **protocol.md §8.2** — "envelope `protocol_version` refined to `1..15`" →
   `this > 0`, with a pointer to §3.1. A stale reference to the bound **B-6
   removed**, contradicting the section it cites; the correct value was already
   decided and stated two sections earlier.
2. **api-surface.md §8** — FR-65's baseline "48 / **95**" → "48 / **100**". §0's
   own table sums to 100 (86 + 14) and the changelog says 100; 95 was a
   transcription error in the CI-gate figure.
3. **RFC-0001 §15** — E12 and E13 printed in the wrong order; rows swapped. No
   text changed.

Nothing else in any document was edited by me.

---

## 8. Checklist §11 re-check

| §11 item | RFC-0001 | ADR-001 | protocol.md | instrumentation.md |
|---|---|---|---|---|
| 11.1 decision stated up front | pass | pass | pass | pass |
| 11.2 status/date/author/supersession | pass | pass | pass | pass |
| 11.3 alternatives, strongest form | pass | exemplary | pass | pass |
| 11.4 failure modes | pass | pass | pass | pass — §4A.5 supplies the audit-disagreement mode that was partial in cycle 1 |
| 11.5 observability/provenance impact | pass | pass | **pass** (was B-7/B-9) | **pass** (was B-11) |
| 11.6 measurable exit criteria | pass (E1–E13) | pass (X1–X6) | pass (P1–P8, H-1–H-14) | pass |
| 11.7 budgets named with numbers | pass | pass | pass | pass |
| 11.8 single-node v1, no smuggled scope | pass | pass | pass | pass |
| 11.9 no deferral on the six hard parts | **pass — all six** | pass | n/a | n/a |
| 11.10 open questions have owners | pass | pass | pass | pass |

§11.9 re-verified against the six: transport decided; resync answers "nothing is
serialized" and now carries an explicit supersession edge across the boundary;
per-event authorization has a structural non-bypass proof, an enumerated
exemption list, and — new in cycle 2 — the one authorized-but-expensive frame is
independently rate-limited; origin validation and authenticated establishment
remain ordered before allocation; the memory ceiling is a number with a method, a
falsifier, a pre-registered decision rule, and a ratchet; slow-client eviction has
three stages, each with a signal, a threshold, a metric, and now a provenance
flush trigger. **None deferred.**

Every open question in every document carries an owner and a phase. O1–O6 are
closed and struck rather than carried; the survivors (O7–O12, I3–I6, D4–D6,
Q-P1/P3/P4, A4/A5) are genuine unknowns with named owners, which is what §11.10
asks for.

---

## 9. Phase 0 technical exit

**The Phase 0 design package is APPROVED for implementation.**

All four design documents clear checklist §11. The two exit criteria I named in
cycle 1 as independent of my approval — `docs/api-surface.md` (FR-65) and
`docs/dependencies.md` (NFR-9/FR-69), carried as D5 — are written, committed, and
reviewed here for the first time; both are of a standard that made this review
easier rather than harder, and the dependency ledger's §4 "considered and
rejected" section is doing work most ledgers skip. The two API questions queued
against the surface are ruled on above. The six hard parts are answered. The
functional core is uncompromised, the provenance chain is now closed at both the
outbound boundary and the resync boundary, and the budgets are numbers with
methods, falsifiers, and — uniquely, in §6.1.2 — a rule that fixes the response
to a miss before the miss can happen.

**One condition gates the recording of the gate itself: C-6.** The PRD is this
project's scope authority and it currently records a memory target inverted from
the one I have just approved; G2 defers to RFC-0001 for that number, so the two
authorities disagree in public. It is a one-paragraph PM-1 edit and it must land
before Phase 0 is written down as closed. Implementation does not wait on it.

**C-5 is the one to watch.** It is not DEV-1's to discharge and it is the only
condition whose omission would degrade a *result* rather than a document: an
equivalence spec that does not bind the Next.js side to the same TLS boundary
gives back exactly the asymmetry the inversion was adopted to remove. Land it
before Phase 1 quotes a comparable baseline.

The remaining twelve conditions are document-consistency obligations, all owned,
all cheap, and all of a kind that gets more expensive the longer they sit —
they should be swept in the module-init PR rather than carried to Phase 2.

For the avoidance of doubt: **decisions D1–D6 (cycle 1) and the two rulings and
two approvals in this document are final.** Reopening any of them requires an ADR
with new evidence, not a revision cycle. There is no cycle 3.

— L9-1, 2026-08-04
