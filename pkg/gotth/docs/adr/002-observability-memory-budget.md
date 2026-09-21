# ADR-002 — A memory budget for default-on observability

| | |
|---|---|
| **Status** | **ACCEPTED WITH CONDITIONS — L9-1, 2026-08-05. The ruling is [§8](#8-the-ruling--l9-1-2026-08-05).** DEV-1 wrote it and did not approve it; that is left standing rather than tidied away, because the author's refusal to bless their own document is part of why it was worth reading |
| **Date** | 2026-08-04/05 |
| **Author** | DEV-1 (Server Core / Go) |
| **Supersedes** | — |
| **Superseded by** | — |
| **Decides** | The gap PM-1's `9d3029ab` records as a scope decision nobody has taken: **default-on observability has no per-session memory budget line anywhere** — not in the PRD, not in RFC-0001 §6.2, not in `docs/instrumentation.md`, and not in NFR-1, which budgets its *latency* |
| **Does not decide** | The 46,080 B G2 gate. RFC-0001 §6.1.2's number is untouched by this document. Nor does it amend equivalence-spec §3.6 or §5.6, both frozen |
| **Evidence base** | [docs/bench/g2-baseline.md](../bench/g2-baseline.md) §4, §9 and **§9.10** — the last of which measures the tree this PR ships and is the only section whose figures this ADR relies on for the present state |

## Decision requested

**Add a per-session memory budget line for default-on observability to
RFC-0001 §6.2, derived by §6.2.2's composition method, with a change rule
modelled on ADR-001's condition C-14 — and keep it INSIDE the G2 gate rather
than carving it out.**

---

## 1. What the measurement did and did not settle

RFC-0001 §6.1.2 pre-registered, before any measurement existed, that a
TLS-outside figure over 46,080 B is answered by attributing the overage to a
named line and then **either engineering it down or escalating to an ADR that
moves the target**. This document started life as that escalation.

**An earlier draft of this section said the overage had been engineered down, so
that no target moves and no residual needs accepting, and that the options to
move the target or accept a residual were moot. That was written before the
measurement it depended on existed, and it claims more than the measurement
supports.** What follows is what was measured.

| Tree | What it is | Campaign | Headline, obs ON | Runs |
|---|---|---|---:|---:|
| `70abe339` | the code the original baseline measured | `c1` | 82,104 B | 3 |
| `ce52d2f9` | after `9f88d75e`'s instrumentation-path work | `c1` | 64,971 B | 3 |
| `5a2ca417` | after that commit's transport work | `c2` | 45,181 B | 2 |
| **`d66e4953`** | **the tree this PR ships**, 54 commits later | **`c3`** | **45,769 B** | **5** |

**Rows are not comparable to each other and the deltas that matter are drawn
inside a campaign, never across two.** Baseline §9.10.7 measures why: the
*unchanged* `ce52d2f9` tree pooled 64,971 B, 66,396 B and 69,673 B in the three
campaigns that measured it — **7.2 % of drift on code that did not change**,
larger than most of the differences between adjacent rows above. The paired
within-campaign deltas are −20.9 % (`c1`), −32.0 % (`c2`) and −34.3 % (`c3`).

### 1.1 The gate verdict, stated with its resolution

| | |
|---|---:|
| measured, `d66e4953`, 5 runs | **45,768.7 B** |
| the G2 gate | 46,080 B |
| **margin** | **under by 311.3 B — 0.68 %** |
| the same cell's run-to-run spread | 2,523 B — 5.5 % |
| **head runs individually over the gate** | **2 of 5** |
| between-campaign drift on unchanged code | 4,702 B — 7.2 % |
| the §6.1.2 ratchet at 36,864 B | **checked; does not trigger**, 8,905 B above it |

**The pooled figure does not exceed 46,080 B, so §6.1.2's escalation trigger is
not tripped. It is also not closed.** The margin is an eighth of the cell's own
spread and a fifteenth of the drift the method shows on unchanged code, and two
of the five runs that produced the median are themselves over the gate. Baseline
§9.10.9 states the consequence and this ADR adopts it without softening: **the
measurement resolves that the tree is *at* the gate, not which side of it the
tree is on.**

Two further reasons no reading of this is "G2 met", both from the baseline:

- §3.6's **10-real-tab driver validation gate has not been run** by any of the
  four campaigns, so in §3.6's own words every figure here is "an assertion about
  a synthetic client, not about sessions" (§9.10.11.1).
- The transport saving is conditional on a **non-pipelining client** (C-37).
  An all-pipelining client population restores ≈6,656 B/session — **twenty-one
  times the margin** (§9.10.11.4).

### 1.2 The four options, since §1 previously named two that appear nowhere

| | Option | Status after the measurement |
|---|---|---|
| **A** | **Engineer the overage down** — §6.1.2's preferred branch | **Taken, three times, and it worked**: 82,104 B → 45,769 B, a 44 % reduction across `9f88d75e`, `5a2ca417` and the 54 commits after them, each measured rather than claimed |
| **B** | **Move the target** — an ADR raising 46,080 B, with L9-1's approval and the measurement in hand | **Not proposed, and not moot.** It is not triggered, because the pooled figure is not over the gate. It is not dead either, because a figure this close cannot establish that it never will be. **If it is ever proposed it must be proposed on its own evidence**, and "the measurement was ambiguous" is not evidence for a looser target — §6.1.2 puts the burden the other way, and this ADR does not discharge it |
| **C** | **Budget the term inside the gate** — §3 below | **Proposed.** It is orthogonal to A, B and D: it neither moves the gate nor relaxes it, and it is the only option that addresses the thing none of the others do |
| **D** | **Accept the residual and let E1 fail at Phase 5** | **Not required on the measured median, and live on the runs.** Two of five runs exceed 46,080 B; E1's first falsifier is `> 46,080 B` and is not tripped by the pooled figure. E1's **second** falsifier — N=1000 within 15 % of N=100 — has **not been re-measured at any tree since §4.3 measured it at −15.6 %**, so E1 cannot be called satisfied on this evidence either way (§9.10.11.2) |

**Neither B nor D is argued for here, and neither is declared moot.** The honest
position is that the measurement leaves both undetermined, and that saying so is
the point of §6.1.2's pre-registration: the response was fixed before the number,
so an ambiguous number does not get to pick its own branch.

**What is not fixed by any of them is the hole underneath**, and it would have
been tidier to let this document disappear. That is exactly why it should not.

---

## 2. The hole, stated with its evidence

**Nothing in this project budgets observability's memory.**

- **NFR-1** budgets observability at ≤ 5 % of p50 latency. Latency only.
- **RFC-0001 §6.2**'s composition table has thirteen lines and none of them is
  instrumentation. The measured column added at checkpoint 3 has none either, and
  neither does §6.2.4's re-derivation.
- **`docs/instrumentation.md`** specifies cardinality rules, span structure,
  sampling and an overhead budget — §4's budget is latency, and §2.4 is titled
  "Gauges, and the honest limit on per-session memory" but budgets nothing.
- **equivalence-spec §5.6 is FROZEN and puts default-on observability inside the
  HEADLINE configuration** — "that is what a user gets". So the gate is
  evaluated against a configuration whose instrumentation cost is unbudgeted by
  construction.

**It was large, it is now small, and nobody would have known either number
without measuring — which is the argument.**

| | obs ON | obs OFF | observability's share |
|---|---:|---:|---:|
| 2026-08-04, `35eb24a4` (§4) | 82,559 B | 57,135 B | **25,424 B — 30.8 % of the headline** |
| **2026-08-05, `d66e4953` (§9.10.10)** | **45,769 B** | **42,086 B** | **3,682 B — 8.0 %** |

**An earlier draft of this section argued from the 25,424 B figure and called
the term "this project's largest attributed per-session memory term". At the
shipping tree that is no longer true.** `9f88d75e` cut the term by 85.5 %, and
the baseline's §9.10.10 measures what is left. The claim is corrected rather than
carried, for the same reason §9.9.4 refused to let anyone carry the old share
forward by subtraction — that arithmetic would have produced ≈20,300 B for a cell
that measures 42,086 B.

**The hole is the same hole.** A term moved by 21,742 B per session across two
landings, in a configuration the frozen spec puts inside the headline, and **no
line anywhere went red, green, or anything at all**, in either direction. The
project learned the term's size twice, both times by spending an hour of a shared
host on a measurement campaign, because that is the only instrument it has.

**The margin is the sharpest form of the argument, and it is now arithmetic
rather than rhetoric.** The shipping tree sits **311 B** under the gate while
default-on observability costs **3,682 B/session** — the margin is **8.4 % of the
instrumentation term**. One more span attribute on the read pump's path, one more
label set, one more record on the mount path, and a term nine times the size of
the entire remaining headroom moves by an amount nobody would be able to see. The
same change would also be invisible to NFR-1, which measures latency, and to
`docs/instrumentation.md`, which measures cardinality.

---

## 3. The proposal

### 3.1 A budget line in RFC-0001 §6.2, derived rather than observed

Add one line to §6.2's composition, with the same status as every other line
there: **an estimate derived from the design, which the measurement then
corrects.**

It must be derived and not simply set to the measured value. §6.1.2's whole
discipline is that a target chosen after seeing a number is not a target, and a
budget back-filled from a measurement would be the same error one level down. The
derivation §6.2.2 uses is available: per-session instrumentation state is
enumerable — the metric label sets a session retains, the `spanRef` per unacked
slot that instrumentation §3.3 already sizes at 32 B, the logger's retained
attributes, and the goroutine-stack depth the instrumented paths force.

**One caution for whoever derives it**, from §9.10.10: the measured difference
between the two cells is no longer dominated by any of those, and one of its
components has changed sign. Turning observability **off** made the
goroutine-stack class ≈738 B/session **larger**, on two runs against five, with
non-overlapping ranges — the inverse of §5.1's finding, which was true of the
code it measured. A derivation that reuses §5.1's stack-doubling reasoning will
be deriving a term the current code does not have.

### 3.2 A change rule, modelled on ADR-001's C-14

C-14 is the mechanism that worked in this batch: it is why L9-1 caught X3
drifting from its four lines (C-35) rather than the drift being found at Phase 5.
The same shape applies:

1. If a change to instrumentation moves the line, the line and §6.2's
   composition change **in the same PR**.
2. The budget **never becomes the looser of the two**: if the measurement lands
   under it, it ratchets down under §6.1.2's rule.
3. A change to it **quotes the arithmetic**, not just the new figure.

### 3.3 The budget sits INSIDE the G2 gate, and is not an exemption

This is the load-bearing half of the proposal and the reason it is safe to adopt
after a measurement.

An obvious-looking alternative is to let the gate cover the library and report
observability separately against its own budget. **That would be the
disqualifying method error** §6.1.2 exists to prevent: equivalence-spec §5.6 is
frozen, it makes default-on observability part of the headline, and carving it
out *after* seeing that it is a term worth carving out would be changing what is
measured because of what was measured. It would also make the gate easier to
pass, which is the direction that should require the most evidence and here has
none — and at a 311 B margin, carving out 3,682 B would convert an unresolved
result into a comfortable pass by definition rather than by engineering. That is
precisely the move this document exists to refuse.

So: the budget is a **sub-line of the 46,080 B gate**, not a parallel allowance.
It constrains; it does not relax. Adopting it cannot move the G2 figure or the
G2 verdict in any direction.

### 3.4 What must be measured to set it — measured, and what is still owed

The line's measured counterpart is exactly `headline − observability_off` at
N = 1000. **That cell was owed by two campaigns and is now measured**: baseline
§9.10.10, at `d66e4953`, two runs, same campaign, same method — **3,682 B/session**.
This ADR still does not propose that number as the budget, for §3.1's reason: the
budget is derived and the measurement corrects it, not the reverse.

Three things remain owed, and each has an owner rather than a hope:

| Owed | Why it matters here | Owner |
|---|---|---|
| the observability-off cell at **five runs** rather than two | the share is a difference of two cells whose spreads are 5.5 % and 4.4 %; 3,682 B is the difference of two medians, not a tight figure | DEV-1 |
| RFC §6.3's **per-component heap profile at `d66e4953`** | it would attribute the term to lines instead of to a difference, and it is not subject to the GC sawtooth §9.10.9 describes | DEV-1 or QA-2 |
| the **N = 100 sub-linearity cell** at the shipping tree | E1's second falsifier has been unmeasured since §4.3, so option D above cannot be evaluated | QA-2 |

---

## 4. The alternative this ADR rejects, and why it is written down

**Withdraw ADR-002, because the gate is met.** It is the tidy option, and after
the measurement it is even tidier than it was: the term this ADR is about has
shrunk by 85 %.

It is rejected twice over.

**First, the gate is not met.** §1.1: the pooled figure is under by less than a
quarter of its own cell's spread, two of five runs are over, and the driver gate
that §3.6 makes mandatory before any 1k number is quoted has never been run.
Withdrawing on the ground that the gate is met would be withdrawing on a claim
this project's own baseline declines to make.

**Second, and independently, the gate being met and the term being unmanaged are
different facts, and only one of them moved.** The project has now discovered, by
measurement and three times running, that a per-session term nobody costed was
first 25,424 B and then 3,682 B. Both discoveries cost an hour of a shared host.
Neither was visible to any line in any document. Closing the document that says
so, because the number came out smaller, would close PM-1's `9d3029ab` scope
decision by silence rather than by decision — and would do it at the exact moment
the remaining headroom became smaller than the term itself.

---

## 5. Recommendation

**Adopt §3: one derived budget line in RFC-0001 §6.2, C-14's change rule applied
to it, the budget kept inside the G2 gate, and §3.4's three owed measurements
scheduled with the owners named there.**

It costs one line in a table, one condition, and three measurement cells. It buys
the thing this project spent three campaigns discovering it did not have: a
number that goes red when instrumentation quietly spends the gate's headroom —
headroom which is now **311 B**, against a term of **3,682 B**.

**DEV-1 does not approve this.** L9-1 owns it, PM-1 owns the scope half
`9d3029ab` records as untaken, and the cells in §3.4 are DEV-1's or QA-2's to run
once someone has said which.

---

## 6. Consequences for equivalence-spec §3.6's reporting

1. **Nothing here amends §3.6 or §5.6.** No option in this document changes
   `mem_per_session = (M(N) − M(0))/N`, the window, the workload, the TLS
   boundary, or the driver; and §3.3 explicitly refuses the one change that
   would have touched §5.6's frozen headline rule.
2. **The 10-real-tab driver validation gate still gates every figure quoted
   here.** §3.6 makes it mandatory before any 1k number is quoted; it has been
   run by none of the four campaigns; and until QA-2 runs it, every number in
   this ADR and in the baseline is "an assertion about a synthetic client, not
   about sessions" in §3.6's own words. **A figure that is now under the gate is
   not G2 met.**
3. **The measured pass is conditional in two further ways, both documented**
   (baseline §9.6.1): it assumes a ResponseWriter that reaches this library's
   hijack wrapper — true of the measured harness, and made true behind ordinary
   middleware by `0929bf5a` (C-36) — and a client that does not pipeline behind
   its upgrade request, which the measured driver provably cannot do but a real
   peer can (C-37), at ≈6,656 B/session. **That last figure is twenty-one times
   the margin under the gate.**
4. **E1's second falsifier is unresolved, not satisfied.** The N = 100
   sub-linearity cell has not been re-run by `c1`, `c2` or `c3`, so the −15.6 %
   the original baseline reported is neither confirmed nor cleared at the current
   code.
5. **The method's resolution is now itself a measured quantity** (§9.10.9), and
   it is coarser than this ADR's margins. Any future document quoting a
   sub-1 % margin against the G2 gate on this method should quote §9.10.9's
   spreads beside it.

---

## 7. What this ADR does not do

- It does not change the 46,080 B number in RFC-0001 §6.1.2, §6.1, or PRD G2.
- It does not amend equivalence-spec §3.6 or §5.6.
- It does not set the budget's value. It proposes that there be one, how it is
  derived, and what governs it.
- It does not claim the G2 gate is met, and §1.1 is explicit that the
  measurement cannot establish it either way.
- It does not approve itself.

---

## 8. The ruling — L9-1, 2026-08-05

**ACCEPTED WITH CONDITIONS.** The decision requested in §3 is granted; §3.3 is
granted without amendment; §3.2 is granted with one addition; **§3.1's "derived,
never measured" clause is REFUSED and replaced**, on evidence from the same
campaign this ADR relies on. RFC-0001 §6.2 moves **in this landing** for the
budget itself and in a **named follow-up** for the composition row. **Two new
conditions here** — C-47 and C-48 — and two more filed in the same pass live
where they bind: C-45 and C-46 in ADR-001, C-49 in RFC §3.4.

I am judging the argument and not the author's discomfort, as I was asked to.
For the record: **DEV-1 declining to approve this was the right call and the
document is better for it.** §1's honesty about the earlier draft, §4's refusal
to withdraw on a smaller number, and §3.4's list of what is still owed are what
make it approvable at all.

### 8.1 The verdict, clause by clause

| Clause | Ruling |
|---|---|
| **The hole exists** (§2) — default-on observability has a per-session memory cost budgeted nowhere, in a configuration equivalence-spec §5.6 freezes *inside* the headline | **UPHELD.** It is checkable and I checked it: NFR-1 budgets latency; `instrumentation.md` §4 is *"Overhead budget (NFR-1, G6) — ≤ 5 % of p50 event→paint"*, i.e. latency again; §2.4 **exports** `gotthlive_session_tracked_bytes` and sets no budget for it, and by its own definition that gauge covers only *"structures the library owns and can size exactly"* — which excludes everything the two cells differ by; and RFC §6.2's composition has no instrumentation line. A term moved 21,742 B/session across two landings and no line anywhere changed colour |
| **§3.3 — the budget sits INSIDE the 46,080 B gate and is not a carve-out** | **GRANTED, unamended, and it is the half that decides the document.** Carving 3,682 B out of a 311 B margin would convert an unresolved result into a pass by definition. §3.3 refuses that in advance, at its own cost, which is the only kind of refusal worth anything |
| **§3.2 — a C-14-shaped change rule** | **GRANTED, with one addition**: a change quotes **both cells' run counts**, not only the arithmetic. This line is a *difference of two measurements*, and 5-runs-against-2 is a different claim from 5-against-5 |
| **§3.1 — the line is derived and never back-filled from the measurement** | **REFUSED, and replaced — §8.2.** The recipe cannot produce the quantity §3.4 measures, one of its components is already budgeted elsewhere, and the derivation would be performed by the person holding the measurement anyway |
| **§3.4's three owed cells** | **UPHELD with the owners named there**, and the first of them becomes **C-47** because the budget's value is provisional until it lands |
| **§1.1, §4, §6 — that this is not "G2 met"** | **AFFIRMED without softening.** A pooled figure 0.68 % under a gate whose cell spreads 5.5 %, with 2 of 5 runs over it and the §3.6 driver gate never run, resolves that the tree is *at* the gate. Nothing in this approval may be quoted as G2 met |

### 8.2 Why §3.1's derivation clause is refused, and what replaces it

§3.1's discipline is the right instinct — a budget back-filled from a measurement
is a target chosen after seeing a number. It fails here for three reasons, and
the third is the one that matters.

1. **The project's own rule already sets budgets from measurements, in the
   tightening direction.** RFC §6.1.2 ratchets a gate to *"the measured value plus
   10 %"*. That rule was pre-registered before any measurement existed, which is
   exactly what makes it usable afterwards. I applied it to ADR-001's X3 in this
   same landing; applying it here is consistency, not opportunism.
2. **One of §3.1's enumerated components is already budgeted, and is paid with
   observability OFF.** `internal/session/window.go:15-19`: every slot holds a
   32 B `obs.SpanRef` **unconditionally**, whether or not a `Tracer` is
   configured, and RFC §6.2.2's window row already carries it (16 × 64 B, of which
   32 B is the `spanRef` — instrumentation §3.3 and RFC cycle-2 B-12 both say so).
   A line whose measured counterpart is `headline − observability_off` cannot
   contain a term that appears in both cells. Following §3.1's recipe would count
   it twice.
3. **The measurement says the term is not retained state at all**, so a §6.2
   composition row — which budgets retained bytes and is then doubled by the
   `GOGC` line — is the wrong instrument for it. Recomputed by me from the
   published `introspect-*.json` of §9.10.5 and §9.10.10, as `(mn − m0)/N` and
   `heap_live(N)/N`, N = 1000:

| Signal, per session, `d66e4953` | obs ON (5 runs) | obs OFF (2 runs) | difference | do the runs' ranges separate? |
|---|---:|---:|---:|---|
| **headline** (§3.6) | 45,768.7 | 42,086.4 | **+3,682** | **yes** — every obs-on run exceeds every obs-off run. Supported band **+1,765 … +6,124** |
| live heap | 12,189.7 | 11,846.5 | **+343** | yes — band **+174 … +542** |
| goroutine stacks | 12,943.4 | 13,680.7 | **−737** | yes, and **the sign is negative**: obs-on is *smaller* |
| total Go-runtime mapped | 44,920.8 | 40,601.6 | +4,319 | **no** — the ranges overlap heavily; not resolvable at these run counts |

  Retained per-session state attributable to the hooks is therefore **at most a
  few hundred bytes and ambiguous in sign** — and §9.10.1's caveat means even the
  +343 B is an upper bound, because that secondary carries the process's fixed
  live heap divided by N and the instrumented process's fixed live heap includes
  the OTel SDK's. **The 3,682 B lives in mapped-but-not-live heap**, consistent
  with §9.10.10's own GC-cycle hypothesis (12–13 cycles against 9–10), which this
  ruling does not upgrade to a finding either.

**So the budget is set where the term is.** It is a **gate sub-line measured the
way the gate is measured** — `headline − observability_off` at N = 1000 — and
**not** a composition row that would budget churn as though it were retained
bytes and then double it.

### 8.3 The budget, and where §6.2 moves

| | |
|---|---:|
| measured, `d66e4953`, 5 runs against 2 (§9.10.10) | **3,682 B/session** |
| **the budget line, = measured + §6.1.2's 10 %** | **4,050 B/session** |
| what the runs support | **+1,765 … +6,124 B** |
| the same term when §4 first measured it | 25,424 B |

**It lands in RFC-0001 §6.2 in this landing, as §6.2.6**, because the thing this
ADR is about is that no line anywhere went red and a follow-up would extend that
state. **The retained-state composition row is the follow-up** (C-48), because it
is small, derived, and must exclude what §6.2.2 already budgets.

**The budget is provisional in one stated way and no other:** its value comes
from a 2-run obs-off cell, and C-47 is what makes it five. It ratchets down under
§6.1.2's rule when it does, in the same PR, per §3.2 as granted.

### 8.4 Conditions

> **C-47 — the observability-off cell at five runs. Owner: DEV-1.** §3.4's first
> owed cell. **Falsifier:** g2-baseline carries an obs-off cell with five runs at
> the shipping tree, and RFC §6.2.6's budget line is restated as
> `measured + 10 %` with **both** cells' run counts quoted. Until then the line
> is labelled provisional wherever it is quoted. *Not blocking this gate; owed
> before Phase 5 reports memory.*

> **C-48 — the retained-state composition row. Owner: DEV-1.** RFC §6.2 gains an
> "instrumentation, retained per session" row, **derived** from state that exists
> only when a hook is non-nil, naming each component and its site. **It must
> exclude `obs.SpanRef`**, which every slot holds whether or not a `Tracer` is
> configured and which §6.2.2's window row already budgets. **Falsifier:** the
> row's derivation is checkable line by line against the code, and its measured
> counterpart is quoted as the **live-heap** difference (+174 … +542 B/session
> across the runs), never as `headline − observability_off`. *Not blocking.*

**The other conditions from this ruling wave belong elsewhere and are not this
document's:** **C-45** (the read-pump goroutine stack, `memsrv -probe`) and
**C-46** (the per-connection `context.WithCancel` line) are in
[ADR-001 §7.2](001-transport.md) and bind X3's next re-derivation; **C-49** (the
`spawn` godoc, which claims what RFC §3.4 used to claim) is in RFC-0001 §3.4.

§3.4's other two owed cells stand as written, with the owners it names: RFC §6.3's
per-component heap profile at `d66e4953` (DEV-1 or QA-2), which is what would
attribute this term to lines rather than to a difference and is not subject to the
GC sawtooth; and the N = 100 sub-linearity cell (QA-2), without which E1's second
falsifier is neither tripped nor cleared.

### 8.5 What this approval does not license

- **Not "G2 met."** §6's five points stand unamended. The §3.6 driver-validation
  gate has been run by none of the four campaigns, and until QA-2 runs it every
  figure here — including 3,682 B and 4,050 B — is, in §3.6's own words, an
  assertion about a synthetic client.
- **Not a carve-out.** Adopting the budget cannot move the G2 figure or the G2
  verdict in any direction. If anyone proposes evaluating the gate net of this
  line, this ruling is the standing refusal.
- **Not option B.** Nothing here proposes or prepares moving the 46,080 B target,
  and §1.2's statement of the burden is affirmed: an ambiguous measurement is not
  evidence for a looser gate.
