# Phase 0 design package — L9-1 review, cycle 1 of 2

| | |
|---|---|
| **Reviewer** | L9-1 (Principal Engineer) |
| **Date** | 2026-08-04 |
| **Cycle** | 1 of a maximum 2. After cycle 2 I decide and the decision is final. |
| **Reviewed against** | [review checklist §11](../review-checklist.md), plus §3, §4, §5, §6 where the documents make implementation commitments |
| **Documents** | RFC-0001, ADR-001, protocol.md, instrumentation.md |
| **Context** | [PRD](../PRD.md) (scope authority) · [RFC 0000 teardown](0000-prior-art-teardown.md) (evidence) · [equivalence spec](../bench/equivalence-spec.md) (fairness contract) |

## Verdicts

| Document | Verdict | Blocking | Advisory |
|---|---|---|---|
| [ADR-001 — transport](../adr/001-transport.md) | **APPROVE** | 0 | 3 |
| [RFC-0001 — architecture](001-architecture.md) | **RETURN** | 5 | 4 |
| [protocol.md](../protocol.md) | **RETURN** | 5 | 3 |
| [instrumentation.md](../instrumentation.md) | **RETURN** | 2 | 3 |

Twelve blocking objections, all of them local fixes. **None of the four
documents needs restructuring, and no decision in any of them is reversed.** The
architecture is sound: the actor model is applied honestly, the functional core
is not compromised anywhere, the six §11.9 hard parts are genuinely answered
rather than deferred, and the transport decision is the best-argued document I
have been given on this project. What follows is the work of closing gaps in
otherwise-correct designs.

Two things I want on the record before the objections, because a revision cycle
tends to over-correct:

1. **The prior-art teardown is doing real work here.** Almost every contested
   choice — bounded mailbox vs BEAM (RFC §3.3), no replay vs Blazor (§7.1),
   jittered backoff vs LiveView's remount storms (§8.4), fragment granularity vs
   LiveView change tracking (§5.1) — is argued from a named failure in a named
   system, not from taste. That is what an evidence base is for.
2. **The source citations check out.** I verified protocol.md §2 and §5.3
   against `research/protobuf-refinement-types/` line by line:
   `expr/types.go:96` is `FromProtoKind`; `gen/gen.go:233/235/245/252` are the
   repeated / map / oneof / presence rejections; `gen/gen.go:36` is the
   hardcoded `runtimePackage`; `cmd/protoc-gen-gorefine/main.go:30` is the
   `flag.FlagSet`; `refine/refine.go` is 59 lines with stdlib-only imports. Every
   one is accurate. Documents that cite precisely get read differently, and
   should.

---

## 0. Decisions I owe, and governance rulings

These are mine to make. They are closed as of this document; do not carry them
into cycle 2 as open questions.

### D1 — O5 / I1: OTel is admitted as a Tier-1 dependency. **Option A**, narrowed.

**Decision: Option A — the core module depends on the OTel *API* only; the
consumer brings the SDK. Options B and C are rejected.**

Reasoning, at the §10 bar:

- **C is rejected on two counts.** It reinvents a standard badly, and a
  hand-rolled tracer interface with one implementation is a checklist §1.4
  violation on its face — the same speculative abstraction the RFC correctly
  refuses for transport in §3.5. I am not going to accept an argument against a
  `Transport` interface and then wave through a `Tracer` interface.
- **B is rejected for v1** because `live.WithTracing(tp)` living in a second
  module either makes FR-38's "one option" false for tracing, or forces a core
  interface for the provider — which is C wearing a hat. A second module is also
  a second tag, a second version skew, and a second changelog, for a library
  whose whole thesis is that observability is not bolted on.
- **A is admissible** because the API/SDK split exists precisely for library
  authors, and the API surface is stable under OTel's own compatibility policy.
  Supporting evidence that this is ecosystem-ubiquitous rather than exotic:
  `go/go.sum` in this monorepo already resolves `go.opentelemetry.io/otel`,
  `otel/trace`, `otel/metric`, `otel/sdk`, `otel/sdk/metric` and `auto/sdk`
  transitively, without anything in `go/` importing OTel directly.

**Conditions attached to D1 — all three are binding:**

1. **Depend on the narrowest API module that compiles.** Because
   `WithTracing(tp)` takes the provider explicitly, the library must not read
   the OTel global, and must not import `go.opentelemetry.io/otel` if
   `go.opentelemetry.io/otel/trace` (plus `otel/attribute` if unavoidable)
   suffices. State in `docs/dependencies.md` which modules are imported and why
   each is necessary.
2. **Quote the measured graph delta** (`go list -m all` before/after, plus
   binary-size delta) in the PR that adds it, per checklist §10.2. The host
   running Phase 0 has no Go toolchain, which is why this lands with the code
   and not now.
3. **Pre-registered fallback, decided now rather than after seeing the number:**
   if enabling tracing adds **more than 8 modules** to a consumer's build graph,
   fall back to Option B. I am fixing the trigger in advance so the choice is
   not made by whichever number is more convenient — the same discipline I
   demand of O7 in **B-3** below.

### D2 — O6 / I2: `log/slog` in the library. FR-37 wins over the monorepo convention.

**Decision: the library logs through `log/slog` and accepts a `*slog.Logger`. It
imposes no logging dependency. The ~40-line `slog.Handler` adapter binding to
`core.Logger` ships in the examples.**

The conflict is smaller than both documents present it. `go/CLAUDE.md`'s rule —
"use the internal logger from `pkg/core`, do not use zerolog directly" — is a
rule about not bypassing `core.Logger` *inside the `go/` module*. It is not a
rule that every Go artifact in this repository must depend on zerolog.
gotth-live is, per RFC §14.1, a standalone module outside `go/`, published for
external consumption at a stdlib-submission bar. A library that puts zerolog in
every consumer's `go.mod` fails that bar, and `log/slog` is the answer the
standard library added for exactly this problem.

**One binding condition:** the adapter must be **tested in the same PR that
ships it**, not pasted into a doc as a snippet. An untested adapter is how the
"nothing is lost on the inside" claim quietly becomes false. A single test that
drives a library log record through the adapter and asserts the fields arrive on
`core.Logger` is sufficient.

### D3 — The single-PR deliverable does not trip checklist §1.1

The human scope directive ships this as one PR against `main`, which collides
with my own "diff ≤ ~400 lines or a prior design note exists." Ruling: **the
Phase 0 package is that prior design note**, and it is prior in the sense §1.1
means — merged before the code, predicting it. §1.1 is satisfied for the
shipping PR.

This is not a blanket exemption from review. Internal review still walks §1–§10
**per subsystem as it lands**, QA-1 and QA-2 sign off per phase rather than once
at the end, and §1.3 (no scope smuggling) applies to each phase against its own
exit criteria. A single shipping PR is a packaging decision, not a review
regime.

### D4 — RFC §16 O1 is already closed; strike it

PRD FR-2 was amended on 2026-08-04 per RFC §3.5 and PM-1 accepted it
(`PRD.md:243`). The RFC still lists the amendment as open question O1 needing
"PM-1 + L9-1." It is resolved, and I agree with the resolution: the isolation
property verified by the `go list -deps` architecture test is what FR-2 was
always for, and the interface was mechanism masquerading as requirement. Strike
O1 and cite the amended FR-2.

### D5 — My approval of RFC-0001 is not Phase 0 exit

PRD Phase 0 lists two exit criteria that remain unmet regardless of what I
approve here: **`docs/api-surface.md`** (draft exported surface, one line per
symbol, FR-65) and **`docs/dependencies.md`** (justifications at the
stdlib-submission bar, NFR-9/FR-69). RFC §16 carries them as O3 and O4 with
DEV-1 "proposed" as owner. Assign them to DEV-1 and land them before the gate
closes. D1 above adds a required entry to `dependencies.md`, and ADR-001 §4.1
already contains a complete, well-formed justification for `coder/websocket`
that can be moved there verbatim as the template for the rest.

### D6 — I am amending review-checklist §3.1 (handshake carve-out)

protocol.md §1 names an honest conflict with my checklist: RFC 6455's opening
handshake is HTTP and cannot be a proto frame on any transport. The authors were
right to name it rather than quietly do it. My rule's intent was "no side
channel carries application state," not "violate RFC 6455." I have amended §3.1
to carve out transport establishment and closure explicitly, so the rule now
says what it meant. protocol.md's compensating control — re-asserting the
negotiated version in-band in the first `Snapshot`, so the handshake token is
not the source of truth — is exactly right and should stay. See **B-8** for the
one thing §1 still misses.

---

## 1. RFC-0001 — **RETURN**

### Blocking

**B-1 — §6.2's mailbox line is wrong in a way that costs real budget, and
"empty when idle" is the error.**

The line `unacked window metadata (§7.1) + mailbox slots | 1,500 | empty when
idle` treats mailbox slots as costing nothing until occupied. A Go buffered
channel allocates its entire backing array eagerly at `make` time:
`make(chan inbound, 64)` reserves `64 × sizeof(inbound)` for the life of the
channel, occupied or not. An idle connection pays for all 64 slots. At even 40
bytes per `inbound` that is 2,560 B against a 1,500 B line that must *also*
cover the window.

Compounding it: RFC §7.1 sizes window slots at 32 B × 16 = 512 B, but
instrumentation.md §3.3 adds a `trace.SpanContext` per slot and asserts it is
"inside the §6.2 budget" — it is not, because §6.2's line was not written to
include it (see **B-11**). Running total for that one line is roughly
2,560 + 512 + 384 ≈ 3,456 B against 1,500 B budgeted: about 2 KB, which is a
third of the 5,928 B headroom the whole table has.

Required: give `mailbox_capacity × sizeof(inbound)` its **own line** in §6.2
with the struct sized, and correct the window line to include whatever §3.3
stores. The consequence worth stating in the text: **mailbox capacity is a
memory parameter, not only a backpressure parameter** — §3.3 currently reasons
about 64 purely as a flood-control number, and the two decisions are coupled.

**B-2 — The ack channel has no stated bound, and checklist §6.6 requires one for
every queue.**

§3.1's `run` selects on `a.acks`, a channel distinct from the mailbox, written
by the read pump. §3.3 specifies the mailbox bound and its full-mailbox policy
in careful detail and says nothing about `acks`. Unbounded, it is a memory
vector under an ack flood; at capacity zero it blocks the read pump — the exact
failure §3.3 argues against for the mailbox. State the capacity and the
full-channel policy, and count rejections on the same
`gotthlive_frames_rejected_total` path.

While fixing this, tighten the §11.3 claim that reads "there is exactly one
function in the library that puts anything into a session mailbox." True as
written, but the actor has **three** typed inputs (mailbox, acks, ticker). The
property you actually hold — and it is the right one — is *one goroutine owns
session state; three typed inputs; only the mailbox reaches a reducer.* Say
that, because §11.3.1 already depends on the distinction.

**B-3 — The TLS decision makes the gate figure incomparable to the headline
benchmark, and O7's remedy is not pre-registered.**

Two problems, one root.

(a) §6.1 makes the **64 KiB in-process-TLS figure the gate**, justified by what
`go run ./examples/...` does. Reasonable for a library target. But the project's
headline is the Next.js comparison, and the equivalence spec — the pre-agreed
fairness contract — **does not mention TLS anywhere** (I grepped; zero hits).
Next.js is idiomatically deployed behind a terminating proxy, so a gotth-live
figure that includes in-process `crypto/tls` buffers and a Node figure that does
not is asymmetric in gotth-live's disfavour by roughly the 18,000 B line. Decide
which figure enters the comparison table, and get the TLS boundary written into
equivalence-spec §3.6 where both sides are bound by it.

(b) §16 O7 says that if TLS is materially worse than estimated, "either the
target moves (ADR) or TLS moves out of the measured container on both benchmark
sides." Choosing between those **after** seeing the number is outcome-shopping,
and the equivalence spec exists to prevent exactly that. **Pre-register the
decision rule before Phase 1 measures** — e.g. "if measured in-process TLS
exceeds 18 KB by more than X %, the benchmark measures with TLS terminated
outside the container on both sides, and the 40 KiB secondary becomes the gate;
the target does not move without an ADR." Any concrete rule is acceptable. A
rule chosen afterwards is not.

**B-4 — Coalescing can overflow `contributing_event_ids`, and the overflow
behaviour is a provenance-loss path.**

§7.4 unions contributing event IDs into `Origin.contributing_event_ids` when
coalescing. protocol.md H-4 bounds that list at 1,024. At the dashboard workload
(53 updates/s) with `slow_client_grace` at 30 s, a session can accumulate ~1,590
contributing events before eviction — the bound is reachable in normal
operation, not just under attack. Neither document says what happens when it is
hit. If it truncates, protocol.md P5 (set equality, "not sampling") is false and
provenance is silently lost, which by my own philosophy is an automatic return.
If it errors, a slow client kills its own session by a path nobody designed.

Specify it. The cheap correct answer is to treat the bound as a **flush
trigger**: when the union reaches the limit, emit the patch rather than continue
coalescing, so provenance is never dropped and the list never overflows. State
it in §7.4 and cross-reference H-4.

**B-5 — `ResyncRequest` is an amplification vector and is not rate-limited.**

§11.3.1 says `ResyncRequest` "does reach the actor" and is authorized as a
distinguished event kind. §8.3 says a GAP resync re-renders **every registered
fragment** and emits a full `Snapshot` — the most expensive operation the server
performs, and the one whose cost is unbounded by client input. §11.5's token
bucket is described in terms of "inbound events/sec," and §11.3.1's
rate-limiting sentence covers `Heartbeat`, `Ack`, and `ClientTelemetry` — the
frames that were *exempted* — while saying nothing about the one frame that
triggers a full re-render.

Even inside the 50/s event budget this is amplification: 50 full-state renders
per second per session, from one authenticated client, is a self-service DoS.
Give `ResyncRequest` its own much tighter limit (a minimum interval, or a small
bucket independent of the event bucket), a metric, and a close code on abuse.
This is the one security gap I found that is not merely a documentation
precision issue.

### Advisory

**A-1 — §3.1's struct listing omits `acks` and `ticker`**, which `run` uses. Fix
with B-2.

**A-2 — §6.2's "GC headroom (10,000)" applies `GOGC=100` doubling to a heap
portion that is not identified.** The table mixes goroutine stacks (not heap),
kernel socket memory (not heap), and library structures (heap) without marking
which lines the 2× applies to. Mark the heap-resident lines so the 10,000 is
derivable rather than asserted; §16 O7 already owns the risk, but the *method*
should be legible before Phase 1 tries to reconcile it.

**A-3 — §14.1's Go-version note is right to flag the discrepancy but understates
it.** `go/go.mod` declares `go 1.25.0` while repo-root `CLAUDE.md` says Go 1.24
is on the VMs. gotth-live's independent `go 1.24` floor is well-argued and I
approve it. The monorepo discrepancy is genuinely out of scope for this RFC —
keep the note, drop the implication that gotth-live should wait on it.

**A-4 — §5.1's rejection of option (b) is correct but leans on the wrong
argument first.** "Requires extending or forking `a-h/templ`" is the weaker
reason; the strong one, which you make second, is that the mechanism's defining
production failure is that ordinary code silently defeats it — a degradation
without a signal, which is the teardown's central lesson and this project's
stated design rule. Lead with that.

---

## 2. ADR-001 — **APPROVE**

This document does what I asked for in checklist §11.9.1 and does it better than
the mandate required. Specifically:

- **Intermediary behaviour** is argued in both directions (§3.1.1 concedes SSE's
  real advantage; §4.4 states the heartbeat rule with a number and names the
  operator action).
- **HTTP/2 and HTTP/3** are handled honestly: §3.1.4 concedes native
  multiplexing, §2.3 explains why it does not rescue SSE here (separate browser
  socket pools, source-cited to `client_socket_pool_manager.cc`), and §4.4
  admits the upgrade lands on HTTP/1.1 in practice rather than pretending RFC
  8441 is deployed.
- **Reconnect semantics** — §3.1.2 and §3.1.3 concede that `EventSource`
  reconnect and `Last-Event-ID` are free and ours are not, and §4.5 books both
  as costs.
- **The upstream event path** — §2.4 is the argument I most wanted to see and
  did not expect to get: two channels with no defined order between them, a
  sequence space observed through one channel and written through another, and
  correlation requiring an ambient credential on a state-mutating request.

§3.1's five conceded advantages are stated in their strongest form; this is not
a strawman comparison. §4.5 ("what we deliberately give up") is the section most
ADRs omit. §4.3's compression decision is a memory fact with a source, not a
preference. §4.1 contains all four elements checklist §10.1 requires and can be
lifted into `docs/dependencies.md` as-is.

§6 correctly concludes that the decision *strengthens* the causal chain, and
FR-43's ADR-with-measurements requirement does not apply because nothing is
trimmed. I agree.

### Advisory

**A-5 — §4.5.2's cross-reference is stale.** It cites "RFC-0001 §9, 'transport'
line" for the client byte budget; RFC §9 is panic recovery, and the size ledger
with the transport line is **§10.4**.

**A-6 — §5 F2 uses `gotthlive_connection_closed_total` (singular).** Every other
occurrence, in RFC §7.4 and instrumentation §2.2, is
`gotthlive_connections_closed_total` (plural). Fix the ADR; the plural is
correct.

**A-7 — §7 X3's 12 KB transport share and RFC §6.2's component lines should be
stated in the same units and boundaries.** X3 excludes TLS; §6.2 reports
with-and-without. Say explicitly that X3 maps to §6.2's read buffer + conn
struct + the two goroutine stacks, so Phase 1 does not have to reconstruct the
correspondence.

---

## 3. protocol.md — **RETURN**

### Blocking

**B-6 — `protocol_version` is refined `<= 15`, which defeats the version
negotiation it exists to serve.**

§3.1 refines `protocol_version` to `this >= 1 && this <= 15`. §8.2 then designs
three deliberately redundant negotiation layers, the third being a
human-readable `Error{UNSUPPORTED_VERSION}` + close `4003`. But a client
speaking protocol version 16 is rejected at **layer 2** — the refinement — as a
malformed frame, so it receives `INVALID_FRAME` / `PROTOCOL_VIOLATION` instead
of `UNSUPPORTED_VERSION`. The field whose entire purpose is to negotiate
versions is capped such that the graceful path is unreachable past version 15,
and the protocol acquires a hard ceiling it can never cross without a breaking
change to the negotiation field itself.

I can find no stated reason for 15. It is not the 1-byte varint boundary (that
is 127); §3.1's 1-byte-tag reasoning is about field numbers, not values.

Required: raise the upper bound so it does not bind within any plausible
protocol lifetime, or drop it and let H-2 own version semantics entirely. Then
confirm §8.2's layering still reads correctly: refinement rejects *structurally*
impossible values; H-2 rejects *semantically* unsupported ones with a reason.

**B-7 — P6 (standalone resolvability) is false as written for every
server-initiated frame, and the resync case has no origin at all.**

P6 claims: "Given only the bytes of one patch frame, `(session_id, patch_id)`
resolves to its transition, its originating event, and the render that produced
each fragment." But §4.2 says server-initiated patches — `EFFECT`, `TIMER`,
`PUBSUB`, `MOUNT`, `RESYNC` — carry `event_id = 0`. There is no originating
event to resolve to. PRD G4 gets this right by writing the disjunction
explicitly ("an originating event **or a named server-effect source**"); P6
omits it and is therefore unsatisfiable for exactly the frames FR-42 exists to
cover. Restate P6 with G4's disjunction.

The `RESYNC` case is worse than a wording problem, and it is the specific thing
the orchestrator asked me to check. A GAP resync is *triggered by a client
frame* — `ResyncRequest` — which §11.3.1 says reaches the actor and is
authorized as a distinguished event kind. It is already event-shaped. Yet the
resulting `Snapshot` carries `event_id = 0`, so the one server-initiated frame
that **does** have a specific client cause cannot name it. Mint an `event_id`
for `ResyncRequest` and put it in `Origin.event_id` with `kind = RESYNC`.

**B-8 — §1's carve-out is incomplete: the RFC 6455 close frame is also not a
`Frame`.**

§1 names the opening handshake as the one non-`Frame` wire interaction. The
close frame is a second one: §8.3 puts a numeric code and a reason string in the
WebSocket close frame, which is not proto. This matters operationally because
E5 and X4 both assert "zero non-`Frame` bytes on the wire" — a literal reading
of that audit fails on every clean disconnect. Name the close frame in §1's
carve-out and state how the audit accounts for it. Per **D6** I have amended the
checklist to carve out establishment *and* closure, so the rule and the design
now agree; the audit's definition still needs to say so.

**B-9 — Outbound frames never cross a `Refine*` boundary, which is the one
mechanism the research repo names as closing the zero-value hole.**

P1 justifies "every `Patch`/`Snapshot` has `origin.source != ''`" with
"refinement-enforced (unparseable otherwise)." For **server→client** frames the
server never parses — it constructs. The enforcement is therefore
construction-time (`New*`/`Must*`), not parse-time, and
`research/protobuf-refinement-types/plugin/IMPLEMENTATION.md` is explicit that
construction-time is precisely where the guarantee is weakest: *Go cannot forbid
the zero value of an opaque type — the hole is closed at the `Refine*` boundary,
which re-checks every field.* A `RefinedOrigin` assembled by struct literal
rather than constructor, anywhere in the emit path, produces an orphan patch that
nothing catches: the client codec does not enforce `matches` predicates (§10.3),
so the independent decode in `livetest.Audit` will not catch it either.

Required: run outbound frames through `RefineFrame` (or an equivalent named
`ValidateOutbound`) immediately before marshal, so the emit path crosses the
same re-checking boundary as the ingress path, and restate P1's justification in
terms of that boundary rather than "unparseable." This is a handful of lines at
the single write path you already have, and it converts P1 from a discipline
into a property. If the cost is judged material, measure it and say so — but the
default should be the boundary.

**B-10 — §10.3's claim about `livetest.Audit`'s independence needs narrowing.**

The claim "every byte the server accepts crosses a generated refinement
boundary" is accurate and well-phrased for the inbound direction, and §10.3's
final paragraph draws the right line between what may and may not be claimed.
But instrumentation §5.2 step 4 says the audit decodes captures with the
generated client codec "so a bug in the server's framing cannot hide itself" —
and the client codec enforces only `len()` predicates, not `matches` or numeric
ranges. So the audit is independent as to *framing*, and only partially
independent as to *field validity*. Say that explicitly in one sentence in
§10.3, so a reader does not infer a stronger check than exists. With B-9 fixed
the server-side boundary covers the gap; the audit's own limits should still be
stated.

### Advisory

**A-8 — Q-P2 (2 MiB theoretical `Event` vs H-5's frame cap) should be resolved
in cycle 2, not Phase 1.** Two limits that can disagree are how the smaller one
gets quietly removed later. It is a one-number reconciliation.

**A-9 — §9 should record why the 18-byte `session_id` rides client→server
frames.** The connection already determines the session; H-3 exists only because
the frame carries an ID that could disagree with it. That is defensible for a
symmetric envelope — and refinements cannot express a direction-dependent
predicate, so the alternative is not free — but the trade should be written down
now, while §9's arithmetic is fresh, so a future FR-43 ADR inherits it.

**A-10 — `Origin.source` regex allows `:` and `/` but the examples only use
`effect:`/`timer:` prefixes.** Consider stating the prefix vocabulary as a
convention, since instrumentation §2.1 bounds `source` label cardinality by
"registered effect sources" and that registration is not described anywhere.

---

## 4. instrumentation.md — **RETURN**

### Blocking

**B-11 — The provenance log is load-bearing everywhere and specified nowhere.**

`provenance log` appears in protocol.md P2, P4, P7 and instrumentation §3.5 and
§5.1 as the join target that makes provenance checkable. PRD G4 and RFC E4 —
100 % of patch frames resolve, 0 unknown origins — rest on it. §3.5 explicitly
excuses trace sampling on the grounds that "G4's provenance-totality guarantee
is served by the frames and the provenance log, **not** by traces."

No document defines it. Not its format, not where it is written, not whether it
is on by default, not its retention or size, not its cost on the hot path, not
whether it is bounded, not whether it survives process restart, and — critically
for checklist §4.5 — not whether it shares code with the metrics path it is used
to audit. If it shares the framer's counters it is not an independent
confirmation of them.

This document owns metrics, traces, logs, and auditing; the provenance log
belongs here. Specify it, or replace every reference with the artifact that
actually exists. It cannot stay a named dependency of the project's headline
guarantee with no definition behind it.

**B-12 — §3.3's per-slot span-context accounting is understated and
double-counted.**

Two errors in one paragraph. First, `trace.SpanContext` is not 24 bytes: a
`TraceID` (16) plus `SpanID` (8) is 24 before `traceFlags`, `remote`, and a
`TraceState` that carries a slice header — realistically 56–64 bytes on amd64.
Second, "16 slots × 24 B = 384 B, inside the §6.2 budget" is asserted against a
§6.2 line that was not written to include it (see **B-1**). Size the real
struct, and add it to §6.2's window line rather than declaring it already
covered.

### Advisory

**A-11 — Pin the value domain of the `code` label.** §2.2 says
`gotthlive_connections_closed_total{code}` takes "the 14 codes of protocol.md
§8.3" — but RFC §7.4 uses `code="slow_client"` (lower-case name) while
protocol.md's table gives both a numeric code and an upper-case name. Fix on the
lower-case name and say so, so dashboards and the §5 audit agree.

**A-12 — I5: I recommend dropping `gotthlive_heap_bytes_per_session_mean`.**
Your own §2.4 caveat is correct, and the value is derivable by a dashboard from
two series you already export. Exporting a pre-divided mean adds a series whose
only distinctive property is that it invites the misreading the `_mean` suffix
exists to prevent. Export the process heap figure and `sessions_active`; let the
query do the division. QA-2 owns I5 — this is my input, not an override.

**A-13 — §4.2's "nil-checked behind a single boolean per session" is the right
mechanism; state it as a requirement, not a design note.** It is the difference
between NFR-1's 5 % gate being met by architecture and being met by sampling,
which is exactly what §4.1 and I3 are anxious about.

---

## 5. The three things I was specifically asked to verify

**(a) Does the provenance story survive reconnect, given the ack window drops
replay?**

**Mostly yes — the reasoning is sound and the two holes are narrow.** Dropping
replay is the right call and RFC §7.1 defends it correctly: retaining frame
bytes would cost the entire §6 budget, and there is nothing to replay *into*
because the session does not outlive the connection. Collapsing two recovery
paths into the one that is exercised on every reconnect and every deploy is
strictly better engineering than maintaining a resume path that only runs in the
rare case — which is precisely why Microsoft still has open bugs in theirs.

The reconnect path itself is clean: new session, new `Snapshot`, `Origin{kind:
MOUNT}`, no orphan, nothing carried over that could dangle. **No orphan patches
across the reconnect boundary.**

The **GAP resync** boundary is where it leaks, in two places, both fixable:

1. The resync `Snapshot` carries `event_id = 0` even though a specific client
   frame caused it (**B-7**).
2. Nothing on the wire links the resync `Snapshot` to the `server_seq` range it
   supersedes. P7 categorises those emitted-but-never-applied frames as
   "superseded by a post-resync `Snapshot`", but supersession is bookkeeping,
   not a causal edge: an analyst holding the capture cannot answer "which events
   produced the DOM the user is now looking at" across the boundary, because the
   patches that carried those events were emitted, counted, and dropped, and the
   Snapshot that replaced them names none of them.

Fix (2) by carrying the superseded range on the resync `Snapshot` —
`superseded_from_seq` / `superseded_through_seq`, or the union of contributing
event IDs, which the `Origin` message can already express. Then P7 becomes a
checkable causal property rather than a reconciliation category. I am folding
this into **B-7**; it is one schema addition and it is the difference between
provenance that survives the resync boundary and provenance that stops at it.

**(b) Is the Q4 client-codec design coherent with protocol.md's conformance
properties?**

**Yes, with one narrowing.** The directional design is sound and the reasoning
in §10.3 is the right trade: the attacker is on the side where enforcement is
total, so client-side re-checking defends only against server bugs and version
skew, and spending 600–1,200 B on an RE2 subset instead of morph correctness
would be the wrong purchase against a 12,288 B ceiling. Generating the codec
from the same `FileDescriptorSet` (§10.2) is the right mechanism, the committed
drift-checked manifest turns the asymmetry into a reviewable artifact rather
than an unwritten assumption, and §10.3's closing paragraph — the claim you may
make versus the one you may not — is exactly the discipline I want.

The incoherence is not in the codec; it is that the codec is also cast as the
*independent decoder* in the audit harness, and a decoder that enforces only
length predicates is a partial check of field validity (**B-10**). Combined with
outbound frames never crossing a re-checking boundary (**B-9**), the result is
that `Origin.source`'s `matches` predicate — the one carrying FR-42's "no
unknown origins" — is enforced at exactly one place, by construction discipline,
with no independent confirmation. Fix B-9 and B-10 and the design is coherent.

**(c) Are the 59.6 KiB budget's two estimate lines flagged as the risk they
are?**

**Flagged, yes; adequately handled, not quite.** §6.2 marks the table as an
estimate, calls out the TLS and GC lines as the two most likely wrong, and §16
O7 carries them with an owner and a Phase-1 deadline. The arithmetic is right:
28,000 of 59,608 is 47 % of the budget resting on two unmeasured numbers, and
9 % headroom against the gate is honest rather than generous — I agree with §6.4
that a target with 50 % headroom would not constrain anything.

What is missing is that O7's remedy is a benchmark-method change dressed as an
engineering contingency (**B-3**), and that the third estimate — the mailbox
line — is not flagged at all because it is not recognised as an estimate
(**B-1**). Fix those two and the risk is handled as well as it can be before
measurement.

---

## 6. Checklist §11 compliance

| §11 item | RFC-0001 | ADR-001 | protocol.md | instrumentation.md |
|---|---|---|---|---|
| 11.1 decision stated up front | pass | pass | pass (§1) | pass (§0) |
| 11.2 status/date/author/supersession | pass | pass | pass | pass |
| 11.3 alternatives, strongest form | pass (§5.1) | **exemplary** (§3.1) | pass (§10.3) | pass (§3.4) |
| 11.4 failure modes | pass (§12) | pass (§5) | pass (§6 H-table) | partial — no failure mode for the audit harness disagreeing |
| 11.5 observability/provenance impact | pass | pass (§6) | **B-7, B-9** | **B-11** |
| 11.6 measurable exit criteria | pass (§15 E1–E12) | pass (§7 X1–X6) | pass (§7 P1–P8) | pass (§4) |
| 11.7 budgets named with numbers | pass | pass | pass (§9) | pass |
| 11.8 single-node v1, no smuggled scope | pass | pass | pass | pass |
| 11.9 no deferral on the six hard parts | **pass — all six** | pass (transport) | n/a | n/a |
| 11.10 open questions have owners | pass, minus **D4** | pass | pass | pass, minus **D1/D2** |

On §11.9 specifically, since this is my standing mandate: transport is decided
with real arguments on all four axes; resync serialization is answered with
"nothing is serialized," which is a genuine answer and a good one; per-event
authorization has a structural non-bypass proof plus an enumerated exemption
list that accounts for each exempted frame individually; origin validation and
authenticated establishment are ordered explicitly before allocation; the memory
ceiling is a number with a method and a falsifier; slow-client eviction has
three stages, each with a detection signal, a threshold, and a metric. **None of
the six is deferred.** That is the bar, and the package clears it.

## 7. What I am *not* objecting to

Listed so cycle 2 does not over-correct:

- **No `Transport` interface.** Correct, and consistent with FR-2 as amended.
- **Whole-fragment patches, no server-side diff.** Correct for v1, correctly
  argued beyond "the PRD said so," with a named falsifier and exit ramp.
- **Session = connection; no resumable sessions.** Correct.
- **At-most-once events.** Correct, and §8.5's honesty about effects that
  committed without the user seeing the result is exactly the right disclosure.
- **`SlowClient` as a synthesized event rather than reducer-readable transport
  state.** This is the single best design decision in the package: it delivers
  application-visible backpressure without making `Reduce` impure, and it keeps
  the replayability property intact. Do not weaken it.
- **The client codec generator.** An internal generator whose output is
  committed and `go:embed`-ed imposes no build step on consumers; it is not the
  "custom build tooling" my §1.5 forbids. Keep the output committed.
- **5 % default trace sampling.** Acceptable *given* that provenance totality
  rests on frames and the provenance log rather than traces — which is why
  **B-11** matters.
- **Bounded mailbox with drop-and-error rather than block.** Correct, and the
  contrast with BEAM's unbounded mailboxes is well drawn.

## 8. Cycle 2

Land the twelve blocking objections plus the advisories you agree with. For any
objection you want to contest, argue it in the revision — I would rather be
shown wrong than have a fix applied that the author thinks is incorrect. B-3,
B-6, and B-11 are the three I consider most important; B-9 is the one most
likely to be argued with.

Decisions **D1–D6** are settled and are not open for cycle 2. `api-surface.md`
and `dependencies.md` (**D5**) are Phase 0 exit criteria independent of this
review and should proceed in parallel rather than waiting on it.
