# Operator questions — gotth-live

Questions only the operator can settle. Each one is recorded here with the
**default the orchestrator took** so that work continued, and with what it would
cost to change the answer later. A default recorded here is a decision that was
made without the operator in the room; it is not a decision that was avoided.

Format: question, why it needs the operator, the default in force, the cost of
overturning it.

**A note on names.** This tree publishes, so the machines it was developed and
measured on are named neutrally throughout: `node-a` is the GPU application
host the Phase 5 measurements are taken on, `node-b` is the 32-core host the QA
transcripts were run on. Every hardware fact, load figure and container count
below is the one that was observed; only the names are stand-ins.

---

## Q-1 — Independent Next.js reviewer for the comparison apps

**Source:** [equivalence-spec.md](bench/equivalence-spec.md) Appendix A.1, §5.4;
spec §7.2 Q16.

§5.4 requires the Next.js implementation to be audited for idiomatic practice by
a reviewer **outside the gotth-live author group**, and requires the report body
to carry either that reviewer's name or an explicit disclosure that no such
reviewer existed.

**Why it needs the operator.** There is no reviewer outside the author group
available to this project. Every agent on this team was briefed by the same
orchestrator against the same documents, which is precisely the correlation an
independent review is supposed to break.

**Default in force:** *internal control only, disclosed in the report body.* The
pessimization audit (`bench/audit/nextjs-pessimization-checklist.md`) is run by
an agent that did not write the Next.js apps, against the spec's checklist, and
its output is committed. The report body states — not in a footnote — that the
Next.js implementation received **an internal control review and no independent
external review**, and names that as a threat to validity alongside the spec's
own T-list. An internal control is weaker evidence than an outside reviewer and
the report says so in those words.

**Cost of overturning:** low, and it stays open. An outside reviewer can read the
committed apps and the committed checklist output at any time; their findings
become a §12 amendment plus re-collection of any cell whose app changed.

---

## Q-2 — Which machine runs the Phase 5 measurements

**Source:** equivalence-spec Appendix A.2, §5.2.

§5.2 requires a host that is **not serving production traffic during the run**
and is **not the public edge**, with enough cores to give four disjoint cpusets
(SUT, load generator, TLS proxy, everything else) without squeezing the SUT.

**Why it needs the operator.** Every machine on this tailnet is live
infrastructure. Choosing one means choosing whose traffic shares a kernel with
the measurement.

**Default in force:** *this host —* `node-a` *— with the non-quiescence
disclosed rather than claimed away.* It satisfies the hard half of §5.2 (it is
not the deployment's public edge, which is a different machine) and has the
cores: 32 logical CPUs, 38 GB RAM, kernel 6.8.0-136-generic. It does **not**
satisfy the soft half: it is the GPU application host, it serves a
browser-streamed Steam/Selkies desktop to authenticated users, and it runs a
long tail of other project containers. Observed load average at the time this
default was taken was 9.79 / 32 cores.

Mitigations, all of which are obligations on the measurement runs and not
aspirations:

1. Measured containers are pinned to a dedicated cpuset with a memory limit, per
   §5.2; the load generator and the TLS proxy get their own.
2. Every run manifest records host state at run start — load average, the
   container list, free memory — so a contended run is visible in the data
   rather than inferable.
3. `docker ps` is checked for an **active** GPU streaming session before any
   measured run; if one is live the run waits. A GPU session on this host is the
   single largest contention source. Which container that is belongs to the
   deployment rather than to this library, so the harness matches it from
   `BENCH_GPU_SESSION_MATCH` (`bench/docker/.env.example`) rather than from a
   name written into its source.
4. The report states in its body that measurements were taken on a shared host
   that was not quiescent, and that this is spec threat **T-5**, not a solved
   problem. Absolute numbers on both sides are therefore conservative; the
   comparison holds because both stacks are measured under the same conditions,
   interleaved (§5.7), on the same host, in the same session.

**Cost of overturning:** high once collection starts. A different host is a full
re-collection of every cell — §12 does not permit mixing hosts within a table.

---

## Q-3 — ADR-001 transport, for the synthetic session driver

**Source:** equivalence-spec Appendix A.3.

**Resolved, not deferred.** ADR-001 landed and the transport is WebSocket
(`internal/wsx`). The synthetic session driver §3.4 and §3.6 require can be
written against it; it is no longer a schedule risk. No operator input needed.

---

## Q-4 — RFC-0001 memory target

**Source:** equivalence-spec Appendix A.4.

**Resolved.** G2's gate is ≤ 46,080 B per idle session with TLS terminated
outside the measured container, and RFC-0001 §6.3 adopts the spec's §3.6
verbatim, so Phase 5 measures one thing once. The remaining item is PM-1's C-6 —
the PRD's memory annotations still read in the pre-A-1 (TLS-in-process) framing
and must be corrected so no reader can find the old framing presented as
authoritative. That is an internal assignment, not an operator question.

---

## Q-5 — Next.js live-data variant count

**Source:** equivalence-spec Appendix A.5.

**Resolved by the PRD, which outranks the schedule.** PRD §6 Phase 5 makes it a
gate: *"All Next.js live-data variants measured and reported — SSE (primary),
WebSocket (secondary), polling (D3/D4) — none dropped for schedule (FR-76)."*
All three ship. Dropping the WebSocket variant would need a PRD amendment, and
QA-2's recorded position is that cutting it weakens the fairness story more than
it saves time. No operator input needed unless the operator wants to overrule
their own PRD.

---

## Q-6 — TLS-terminating proxy image for the bench topology

**Source:** equivalence-spec Appendix A.6, §3.6.

§3.6 requires the **same** reverse-proxy image on both sides, pinned by digest,
and deliberately does not name it.

**Default in force:** the official upstream **Caddy** image, pinned by digest in
`bench/versions.lock.md`. Rationale: it is the proxy this monorepo already runs
at the edge, so its behaviour is the one the operator already reasons about, and
using the same family removes a "you picked a proxy that flatters your stack"
question. It is a **bench-project container only** — it is not the edge, does not
read the production Caddyfile, binds `127.0.0.1` ephemeral ports, adds no host or
network policy, and touches nothing in `caddy/`.

**Cost of overturning:** low before collection, full re-collection of D3 after —
the proxy is inside the topology every memory cell is measured against.

---

## Q-7 — Bench host quiescence during the measurement window

**Source:** the handoff's live-infrastructure rule; spec §5.7, T-5.

**Default in force:** measured runs are **skipped, not degraded**, while an
active GPU streaming session is present, and the check is part of the harness
rather than a habit. No measured run may proceed without its host-state record.
The orchestrator does not stop, restart, or reconfigure a co-tenant container,
any compose project, or any host service to make a bench run cleaner — waiting
is the only permitted mitigation.

---

# The Q-BENCH series — bench defaults, and what they are not

*(Added 2026-08-04 by PM-1, checkpoint-3 scope pass §2. See
[`docs/pm/checkpoint-3-scope.md`](pm/checkpoint-3-scope.md).)*

**Read this before the two entries below. They are not Q-1..Q-7.** Everything
above this line is a question **only the operator can settle**. The two entries
below are **defaults the bench tree took**, cited by committed code under ids
that did not exist until now, and each one has an owner on this project who can
settle it without the operator. They are recorded here because
`bench/apps/counter/next/src/lib/store.ts` and `.../variant.ts` cite
`Q-BENCH-1` and `Q-BENCH-2` by name, `bench/README.md` **Q-D** flagged both as
dangling references, and a citation to an id that does not exist is a broken
reference in the one document whose whole job is traceability.

They are kept in a fenced series rather than appended to the numbered set,
because "only the operator can settle this" is the single contract this file
has, and two bench defaults filed under it would dilute it. **No operator
decided either of these.** Each entry names who actually did, and who can
change it.

---

## Q-BENCH-1 — Counter scope: one shared counter, or one per session

**Cited by:** `bench/apps/counter/next/src/lib/store.ts`.
**Also recorded as:** `bench/README.md` **R-6** and **Q-D**.

equivalence-spec §2.1 **F-CTR-1** says the counter's value is "**server state,
per session**". Both committed counter apps keep **one counter shared by every
connection** — the gotth-live app's own state names it "the shared counter" and
counts "how many sessions currently share this counter", and the Next.js app
defaults `SCOPE` to `global` to match it.

**Default in force:** *global (shared) on both stacks*, with `session` scope
implemented on the Next.js side so that the literal reading is not blocked by
that side if it is ever adopted.

**The rationale on record, and the part of it that has since expired.** R-6's
reason was that "under E1/E3/E4 the app that gets measured is the app that
exists" — meaning `examples/counter`, which is global. **That reason no longer
holds.** PM-1 ratified bench ambiguity **Q-E** at checkpoint 3 (PRD §9 v0.6
row 3, FR-70 as amended): the measured programs are built to §2's frozen feature
tables and are **not** `examples/counter`. So nothing forced the purpose-built
bench counter to be global, and the default now rests on nothing but itself.

**What is actually undecided.** Whether two apps that are global together
satisfy **E1** ("every feature in §2's feature list is present … with the same
visible behaviour") against a §2.1 that says per session. The stacks are
symmetric, so this is not a fairness question between them; it is a
**conformance question against a frozen spec**, and §2 can only move by a §12
amendment.

**Owner: QA-2**, as the equivalence spec's owner — not the operator, and not
PM-1, who may not amend a frozen spec. **Needed by:** before Phase 5 collection
starts.

**Cost of overturning:** low today, high after collection. Changing scope is a
small change to two apps plus a §12 amendment now; after collection it is a
re-run of every counter cell on both stacks.

---

## Q-BENCH-2 — Polling interval for the `poll` variant

**Cited by:** `bench/apps/counter/next/src/lib/variant.ts`.
**Also recorded as:** the "Defaults this tree took" table in
`bench/README.md`.

equivalence-spec §5.4 names the polling **mechanism** (`SWR refreshInterval`)
and not the interval, and the interval is the entire memory-versus-CPU trade
that the polling column exists to show.

**Default in force:** **1000 ms**, identical in all three apps so the three
polling columns are comparable, overridable by `BENCH_POLL_INTERVAL_MS`.

**Rationale.** It is the rate at which the dashboard's slowest region updates
(§2.4 region A, 1 Hz), so a polling client is not asked to be slower than the
app's own slowest live region. Picking a slower interval would flatter the
polling variant's memory number by making it a worse product; picking a faster
one would flatter ours. Anchoring it to a rate the spec already states is the
only choice here that is not ours to argue after seeing results.

**No operator content at all.** The spec left the value open, the bench tree
picked one, and D4 is supposed to **sweep** it rather than assume it — which is
the real answer to this question and is already in the plan.

**Owner: QA-2**, via the D4 sweep and the report. **Cost of overturning:** none
before collection (an environment variable); after collection, the sweep is the
mechanism, so a changed default is a re-run of the polling columns only.
