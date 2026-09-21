# Comparison-app equivalence specification — gotth-live vs Next.js

| Field | Value |
|---|---|
| Owner | QA-2 (Resilience & Performance) |
| Status | **FROZEN under §12, 2026-08-05.** §2 (apps, interactions, data volumes, asymmetry register), §3 (definitions), §5 (fairness rules), §7 (not measured) and §8's row set are frozen. Signed by **L9-1** ([reviews/checkpoint-3.md](../reviews/checkpoint-3.md) §10.6, `dd173542`), **PM-1** ([gates/checkpoint-3.md](../gates/checkpoint-3.md) §8.3, `26b61cf9`) and **QA-2** (§12's sign-off table). Phase 0 exit artifact |
| Version | 0.3 (amendments A-1, A-2, §12) |
| Last updated | 2026-08-05 |
| Satisfies | PRD Phase 0 exit criterion "Comparison-app equivalence spec agreed and merged"; FR-70, FR-71, FR-72, FR-73, FR-75; open questions §7.2 Q15, Q16; L9-1 cycle-2 conditions **C-5** and **C-3** (RFC-0001 §6.1.1) |
| Gates | QA-2 (method), L9-1 (fairness veto), PM-1 (product-surface equivalence) |
| Freeze rule | §12 |

**Purpose.** This document fixes, *before any number is measured*, (a) what the
two applications are, (b) what every measured word means operationally, and
(c) the rules under which the two stacks are run. It exists because a
comparison whose author built both sides is structurally biased (PRD R-15) and
because "server memory per session" and "SSR throughput" do not natively mean
the same thing in a persistent-connection stack and a request/response stack
(PRD R-16). The defence against both is a definition agreed in advance and
frozen.

**What the freeze means, for a reader who opens this file during Phase 5.** The
sections named in the status line may not be edited. A change to any of them
costs a §12 amendment: a dated log entry, L9-1's approval, and — if any
measurement has been taken under the old text — re-collection **in full** of
every affected cell plus disclosure in the report body rather than a footnote.
The freeze was applied **before any number existed**: `bench/data/` contained no
run ids and Phase 5 was unstarted on the day all three signatures were given,
which is the only state in which freezing a comparison specification means
anything and is recorded in the amendment log's own column. Sections outside the
frozen set — §4's reported rows, §6, §9, §10, §11 and the appendices — remain
ordinary documents. **Two things this freeze does not assert:** that the built
bench applications conform to §2 (**Q-BENCH-1** is open and is QA-2's), and that
the independent Next.js reviewer of §5.4 has been found (Appendix A.1, still
pending, and not one of §12's three freeze signatories).

**Reading rule for engineers.** Every definition in §3 is executable: it names
the signal, the API that yields it, the boundary, and what is excluded. If a
definition requires interpretation to implement, it is a defect in this
document — file it, do not interpret it.

**This document authorizes nothing to be run.** It specifies a harness. Phase 5
execution requires operator approval per §5.7.

---

## 0. Contents

1. Scope, non-scope, and the shape of the claim
2. The comparison applications (counter, chat, dashboard)
3. Identical definitions (paint, interactive, active session, memory, payload)
4. Measured dimensions and their harnesses
5. Fairness rules
6. Sampling, variance, and reporting
7. Not measured, and why
8. Feature-parity table skeleton (two-directional)
9. Threats to validity
10. Repository layout and reproducibility
11. Pre-registered predictions
12. Change control and sign-off

---

## 1. Scope, non-scope, and the shape of the claim

### 1.1 The claim being evidenced

Two claims ship in the PR, and they are different claims with different
evidence:

| Claim | Evidence | Requirement |
|---|---|---|
| **Performance claim** — how gotth-live compares on five measurable dimensions | §4 numbers, reported as measured including losses | FR-71, FR-73 |
| **Product-surface claim** — "not losing much product surface" | §8 parity table, both directions, consequence per row | FR-72 |

The performance claim never substitutes for the parity claim and vice versa.
FR-72's gate is absolute: the phrase "not losing much product surface", or any
paraphrase, does not appear in the report, the README, or the PR description
unless §8 is filled in.

### 1.2 What "equivalent" means here

Two applications are equivalent under this spec when all of the following hold.
Each is checkable by inspection before measurement:

- **E1 — Same product surface.** Every feature in §2's feature list is present
  and reachable in both, with the same visible behaviour.
- **E2 — Same interaction set.** Every interaction ID in §2 exists in both and
  is driven by the identical harness script against identical `data-bench-id`
  hooks.
- **E3 — Same data.** Both servers consume the identical committed fixture
  (§2.5) and therefore render the identical information at the identical times.
- **E4 — Same semantics.** Where a feature is server-authoritative in one, it is
  server-authoritative in the other (§2.2's C-B rule is the load-bearing case).
- **E5 — Bounded DOM.** Both stay inside the stated element-count bounds, so
  neither is measured on a materially larger document.
- **E6 — Declared asymmetries only.** Any place the two differ appears in §2.6's
  asymmetry register with a reason, and is either excluded from the measured
  surface or measured as its own labelled row.

**E6 is the honesty mechanism.** An undeclared asymmetry discovered after
measurement invalidates the affected dimension and forces a re-run under §12.

### 1.3 Non-scope

This spec does not cover: the absolute (own-stack) bench criteria in PRD Phase 5
(event→paint gate G1, memory gate G2, observability overhead NFR-1, wire-byte
accounting) — those are measured against gotth-live alone and are specified in
the Phase 5 bench plan, not here. It does not cover BL-27/BL-28 comparisons
(LiveView, Hotwire, Blazor, Datastar, SvelteKit, plain HTMX). It does not cover
the chaos suite (PRD Phase 3) — with one recorded exception: Appendix B lists the
three Phase-3 measurement obligations QA-2 owns whose outcomes can move a number
*this* spec publishes. The suite itself is specified elsewhere; Appendix B is a
dependency register, not a test plan.

---

## 2. The comparison applications

Three applications, matching gotth-live's three examples (FR-60, FR-61, FR-62)
and PRD §5.L's requirement that the Next.js app be built to the same product
surface for **counter, chat, and dashboard**. The dashboard is the demanding
case and the primary source of the headline push-latency and memory numbers;
the counter is the simplest case and the cleanest isolation of round-trip cost.

Every app is a single route. No client-side routing on either side (gotth-live
excludes it, §4/BL-9; the Next.js app therefore does not get a routing win it
would not exercise — that advantage is a §8 parity row, not a benchmark row).

### 2.0 Markup hooks (both stacks, mandatory)

Both implementations MUST emit these attributes on the named elements. They are
the only markup this spec dictates, they are present in both production builds,
and their byte cost is counted on both sides.

| Attribute | Meaning |
|---|---|
| `data-bench-id="<id>"` | Stable handle the harness clicks/types into or asserts on |
| `data-bench-region="<A..E>"` | Live-region boundary, for ROI pixel hashing (§3.1) |
| `data-bench-value` | Element whose `textContent` is a paint predicate's subject |

`window.__bench` is the shared shim contract (§3.1, §3.2). The shim source is
**one file, byte-identical, served by both apps**, committed at
`bench/harness/shim.js`, and its transfer bytes are subtracted from both stacks'
client-JS figures with the subtracted amount stated.

### 2.1 App A — Counter (`bench/apps/counter`)

Maps to FR-60. Route `/counter`.

**Feature list (F-CTR):**

| ID | Feature |
|---|---|
| F-CTR-1 | A counter whose integer value is **server state**, per session |
| F-CTR-2 | Buttons: `−1`, `+1`, `+10`, `Reset` |
| F-CTR-3 | Value rendered as text, plus a derived display: parity label ("even"/"odd") and a status badge whose colour class changes at thresholds (<0, 0, 1–9, ≥10) — so a render is not a single text node |
| F-CTR-4 | Value survives a full page reload (server-authoritative, E4) |
| F-CTR-5 | Two tabs of the same session show the same value and **both repaint** when either changes (requires a server→client push channel on both stacks) |
| F-CTR-6 | Keyboard: `+`/`-` on the focused counter apply `+1`/`−1` |
| F-CTR-7 | A visible "last updated" relative timestamp, re-rendered with the value |

F-CTR-5 is deliberate: it forces the Next.js implementation to have a real push
channel rather than a local `useState`, which is what makes the counter latency
row an equivalent-semantics comparison rather than a category error.

**Data volume:** session state = one `int64` + session id + one timestamp.
Rendered live region ≤ 40 elements. Whole document ≤ 150 elements. No images.
One stylesheet.

**Interaction list (all interactions; measured set marked):**

| ID | Action | Paint predicate | Measured |
|---|---|---|---|
| CTR-1 | click `[data-bench-id=inc]` | `[data-bench-id=value]` textContent === expected | **headline** |
| CTR-2 | click `[data-bench-id=dec]` | same | yes |
| CTR-3 | click `[data-bench-id=inc10]` | same | yes |
| CTR-4 | click `[data-bench-id=reset]` | textContent === "0" | yes |
| CTR-5 | `keydown` `+` on focused counter | same as CTR-1 | yes |
| CTR-6 | 20 clicks on `inc` within 1000 ms | textContent === start+20 (final convergence only) | yes, reported as convergence latency, not per-click |
| CTR-7 | `+1` in tab A → repaint in tab B | tab B's value === expected | **headline (push)** |
| CTR-8 | reload | value preserved | correctness assertion only |

### 2.2 Counter variants and the local-state asymmetry

A competent Next.js counter that had no cross-tab or persistence requirement
would hold the count in `useState` and paint in the same frame as the click.
gotth-live cannot do that by construction (no client state; client-side
prediction and optimistic UI are v1 exclusions, PRD §4 / BL-3 / BL-4).

Handling, fixed in advance:

- **C-B (the app):** F-CTR-1..7 above. Server-authoritative on both sides. This
  is the equivalence-bearing app and the source of the CTR-* rows.
- **C-A (structural floor, Next.js only):** an additional `useState` counter,
  measured once, reported in the latency table as a separate, clearly labelled
  row: *"Next.js, client-local state — no gotth-live equivalent."* It is not
  averaged with C-B, not omitted, and not buried. It quantifies the ceiling that
  client-side state buys, which is a real Next.js advantage and a §8 parity row.

Reporting C-A is the point. Suppressing it would be the strawman FR-73 forbids.

### 2.3 App B — Chat (`bench/apps/chat`)

Maps to FR-61. Route `/chat/[room]`, three fixed rooms (`alpha`, `beta`,
`gamma`).

**Feature list (F-CHT):**

| ID | Feature |
|---|---|
| F-CHT-1 | Message list, **hard cap 200 rendered messages** (oldest dropped). No virtualization on either side — forbidden, so DOM size is identical |
| F-CHT-2 | Each message: author name, avatar initial (CSS circle, no image), body, absolute timestamp, relative timestamp |
| F-CHT-3 | Composer: `<textarea>` + Send button; Enter sends, Shift+Enter newlines |
| F-CHT-4 | Server-side validation: body length 1..500; violation renders an inline error next to the composer without clearing the input |
| F-CHT-5 | Presence list: names of participants currently in the room, live |
| F-CHT-6 | Typing indicator: "N people are typing", live, 3 s decay |
| F-CHT-7 | Room switcher (3 rooms) with per-room unread badge |
| F-CHT-8 | Composer input is **preserved** when other participants' messages arrive (FR-25/FR-55 on the gotth-live side; the same visible behaviour required of Next.js) |
| F-CHT-9 | Authorization: a designated `readonly` participant's send attempt is rejected server-side with a visible error |

**Data volume:** fixed-seed corpus, committed. Message bodies 1..500 chars,
mean 62. 200 messages rendered. 8 simulated peers. Peer traffic replayed from
the fixture (§2.5) at **2 msg/s aggregate** for latency runs and **20 msg/s**
for the stress row. Typing-indicator events at 4/s. DOM bound: ≤ 200 message
nodes × ≤ 8 elements = ≤ 1600; whole document ≤ 2000 elements.

**Interaction list:**

| ID | Action | Paint predicate | Measured |
|---|---|---|---|
| CHT-1 | type one character into the composer | composer value updated | yes — MUST NOT round-trip on either side; a round-trip here is an implementation defect, not a result |
| CHT-2 | click Send → **server-confirmed** message appears | last message node's `data-bench-value` === sent body **and** its `data-bench-state` === `confirmed` | **headline** |
| CHT-2b | click Send → **optimistic local** message appears (Next.js only, `useOptimistic`) | last message node `data-bench-state` === `pending` | Next.js-only row, labelled |
| CHT-3 | a peer message arrives | new last message node matches fixture entry | **headline (push)** |
| CHT-4 | switch room | room header + list content match target room | yes |
| CHT-5 | send a 501-char body | inline error visible, composer content intact | yes |
| CHT-6 | scroll the list 20 screens | none | correctness/jank only |
| CHT-7 | type continuously while peer messages arrive at 2/s for 30 s | zero dropped keystrokes, caret position stable | correctness only (FR-25, FR-55) |
| CHT-8 | `readonly` participant sends | error visible, no message appended | correctness only (FR-47) |

CHT-2 vs CHT-2b is the second deliberate asymmetry. Optimistic UI is idiomatic
App Router practice and a genuine Next.js capability. It is measured with
optimistic UI **on** (CHT-2b, the local paint) and the same interaction is
measured to **server confirmation** (CHT-2) on both stacks. Both rows ship.

### 2.4 App C — Live dashboard (`bench/apps/dashboard`) — most demanding

Maps to FR-62. Route `/dashboard`. This app supplies the headline push-latency
number, the heavy memory workload, and the wire-byte comparison.

**Regions and update model:**

| Region | Content | DOM bound | Update rate | Payload per tick (logical) |
|---|---|---|---|---|
| A — KPI strip | 8 tiles: label, value, delta %, inline SVG sparkline of 60 points | 8 × ~70 nodes = 560 | 1 Hz | 8 values + 8 deltas + 8×60 sparkline points |
| B — live table | 200 rows × 8 cols (id, name, status enum, metric×3, timestamp, badge); stable sort by id unless user sorts | 200 × 10 = 2000 | 2 Hz, **20 rows changed per tick** (10 % churn) | 20 rows × 8 fields |
| C — time series | inline SVG line chart, 2 series × 120 points, shift-one-point | ~250 | 1 Hz | 2 new points, 2 dropped |
| D — event log | append-only, capped 50 entries | 50 × 4 = 200 | 5 Hz | 1 entry |
| E — manual panel | a small panel refreshed by an explicit button press (gotth-live: plain HTMX per FR-62; Next.js: Server Action form) — same visible behaviour | ~40 | on demand | small |

Aggregate ≈ **53 logical updates/s per session**. Whole document ≤ 4000
elements, of which ≤ 800 inline SVG nodes.

**Controls (interactive surface):**

| Control | Behaviour |
|---|---|
| Status filter | `all \| ok \| warn \| error`, filters Region B server-side on both stacks |
| Text search | over row `name`, **debounced 150 ms on both stacks with identical debounce implementation semantics**, filters server-side |
| Column sort | toggle sort by `metric_1` asc/desc/off |
| Rows per page | select 50 / 100 / 200 |
| Pause / resume | halts application of live updates (client-visible), stream continues server-side |
| Region E refresh | button |

**Interaction list:**

| ID | Action | Paint predicate | Measured |
|---|---|---|---|
| DSH-1 | set status filter to `warn` | Region B row count === expected for fixture state | **headline** |
| DSH-2 | type one character into search | Region B row set matches predicate after debounce | yes |
| DSH-3 | toggle sort on `metric_1` | first row's `data-bench-value` === expected | yes |
| DSH-4 | rows-per-page 50 → 200 | row count === 200 | yes |
| DSH-5 | pause, then resume | Region B frozen then converged to current tick | yes |
| DSH-6 | Region E refresh | panel content === expected | yes |
| DSH-7 | passive tick N applied | Region B rows for tick N match fixture | **headline (push)** |
| DSH-8 | as DSH-7 under 4× CPU throttle (`Emulation.setCPUThrottlingRate`) | same | reported, not gated — degradation shape |

### 2.5 The shared fixture — both servers replay identical data

Reimplementing a data generator twice invites accidental asymmetry. Instead:

- One committed fixture per app: `bench/fixtures/<app>/ticks.jsonl`, generated
  once by a committed seeded generator, with a committed SHA-256.
- Dashboard fixture: 36 000 ticks = 1 hour at 10 Hz, seed `0xG07TH11VE`.
- Chat fixture: peer messages, joins/leaves, typing events, 1 hour.
- **Both servers replay the fixture against a monotonic schedule**: tick *N* is
  emitted at `T0 + N × 100 ms`, where `T0` is recorded per run. Neither server
  generates data; both read the same bytes.
- The fixture is a **bench input file, never wire traffic**. It does not
  interact with the review checklist §3.2 ban on JSON side channels: gotth-live
  reads the file server-side and emits liquid proto frames as always. Stated
  here so a reviewer does not have to wonder.
- A conformance test asserts both servers emit the same logical state for tick
  *N* (compare rendered DOM snapshots at a fixed tick under a paused clock).
  This test gates the measurement: it must pass before any run counts.

### 2.6 Asymmetry register (E6)

Every declared difference between the two implementations. Closed list at
freeze; additions require §12.

| # | Asymmetry | Side | Handling |
|---|---|---|---|
| AS-1 | Client-local counter state | Next.js only | Measured as C-A, separate labelled row (§2.2) |
| AS-2 | Optimistic send | Next.js only | CHT-2b, separate labelled row (§2.3) |
| AS-3 | Region E mechanism (HTMX vs Server Action) | both, different mechanism | Same visible behaviour **within one tab** — qualified by A-2, §12. Both included in the app and in both bundle measurements (HTMX's bytes count against gotth-live, §3.5). **The qualification:** on the gotth-live side region E's panel is keyed by the **page-load cookie** rather than by the live session, because a plain HTMX `GET` carries cookies and nothing else — so two tabs of the gotth-live app in one browser share region E's refresh counter where two Next.js tabs do not (`bench/README.md` G-5). No `DSH-*` row opens a second tab, so no measured cell depends on the difference; it is **declared rather than excluded** because E6 requires any place the two differ to appear here, not only the places a row happens to touch |
| AS-4 | Push transport (liquid proto over ADR-001 transport vs SSE/WS) | both | Inherent; that is the thing being compared. Both measured on wire bytes (§4.6) |
| AS-5 | Client-side routing | Next.js capable, unused | Not used in either app; §8 parity row |
| AS-6 | Static generation / ISR of the measured route | Next.js capable | Forbidden on the measured dynamic route (§5.5) and measured **separately** as a Next.js-advantage row (§4.5) |
| AS-7 | Server Components rendering the static shell | Next.js only | Idiomatic, permitted, and encouraged (§5.4) |
| AS-8 | **Per-session duplication of the shared data** — every gotth-live session folds its own copy of all three chat rooms' logs and of the dashboard's 200 rows, where the Next.js stores keep one array and derive per-session views from it | **gotth-live only** | **Not excluded from the measured surface**, and deliberately so: it is a real per-session memory cost and it is inside gotth-live's D3 figure where it belongs. E6's other remedy applies — **its own labelled row in §4.5**, publishing how much of the D3 figure it is, so that the cost is visible rather than merely present. It is a property of today's `live` API rather than an implementation choice: `live.Event.Fields` is `map[string]string`, so an effect cannot hand a session a pointer to a shared immutable value, and a reducer that reached into the feed for one would not be a pure function of `(state, event)` (`bench/README.md` G-3) |

**AS-8 is the first entry in this register on gotth-live's side of the line.**
AS-1, AS-2, AS-6 and AS-7 are Next.js capabilities and AS-3 and AS-4 are shared;
until A-2 the register recorded only differences that could flatter this stack or
were inherent to the comparison. FR-73's honesty clause cuts both ways, and a
register that only ever names the other stack's advantages is a register that is
not being read hard enough.

---

## 3. Identical definitions, operationalized

Every definition below is implemented once, in `bench/harness/`, and applied to
both stacks by the **same code**. Any per-stack branch in the harness is a
review finding.

### 3.1 "Paint"

Two signals. The main-thread signal is the headline because it has sub-ms
resolution; the compositor signal bounds the systematic error the headline
omits. Both are collected by the same shim on both stacks, so any bias is
common-mode.

**`paint_main` (primary, gated).** Definition: *the `performance.now()` value
sampled on the first macrotask that runs after the `requestAnimationFrame`
callback of the frame in which the interaction's paint predicate first became
true.* Executable recipe, in `shim.js`:

1. Register a `MutationObserver` on the region named by the interaction's
   `data-bench-region`, with `{subtree:true, childList:true,
   characterData:true, attributes:true}`.
2. On each observer callback (a microtask after the mutation batch), evaluate
   the interaction's **paint predicate** (§2's tables). If false, return and
   keep observing.
3. On the first true evaluation, call `requestAnimationFrame(() => port.postMessage(0))`
   using a pre-created `MessageChannel`.
4. `port.onmessage` records `t_paint = performance.now()`.

Step 3–4 yields the first main-thread timestamp after the rendering steps for
that frame have run. `MessageChannel` is used rather than `setTimeout(…, 0)` to
avoid the nested-timeout 4 ms clamp. Timer resolution is `performance.now()`'s,
which Chrome coarsens based on cross-origin isolation state — 100 µs without,
5 µs with. Both apps are served with **identical COOP/COEP headers** so both get
the identical clamp; the effective resolution is recorded in the run manifest.
Because both stacks are reached through the same proxy at the same origin
(§3.6), secure-context state and the header set the browser actually sees are
identical by construction rather than by two configurations agreeing.

**`paint_present` (cross-check, subsampled 1-in-20).** Definition: *the
`metadata.timestamp` of the first CDP `Page.screencastFrame` whose
region-of-interest pixel hash differs from the pre-interaction hash.* ROI = the
bounding box of `[data-bench-region]` for the interaction. Screencast at
`everyNthFrame: 1`, `format: png`, `maxWidth/maxHeight` = viewport. Quantization
= the display frame interval; recorded per run.

**Reporting rule.** Headline latency tables use `paint_main`. The report
publishes the measured distribution of `paint_present − paint_main` per stack,
so a reader can see how much presentation lag is excluded and whether it differs
between stacks. If that offset differs between stacks by more than 5 ms at p50,
`paint_main` alone is declared insufficient and both signals are reported as
co-headline.

**Not "paint":** Event Timing API `duration` (measures to the next paint after
the input handler, which for a server-authoritative interaction ends before the
server has replied — it measures the wrong thing here); Element Timing (fires
once per element, not per update); `LargestContentfulPaint` (load-time only).
Each is excluded for the stated reason so no reader assumes we overlooked it.

### 3.2 "Event→paint latency" — the measurement boundary on each side

`latency = t_paint − t_input`, both values from the **same page's**
`performance.now()` timeline. No cross-process clock is involved for
input-driven interactions, which removes an entire class of error. Stated
explicitly because it is the main reason to prefer an in-page boundary.

**`t_input` — input-driven interactions (CTR-1..6, CHT-1/2/4/5, DSH-1..6).**
`event.timeStamp` of the native `pointerdown` (or `keydown` for keyboard
interactions), captured by a `{capture:true, passive:true}` listener registered
at `window` by the shim **before any application script**. Using the browser's
own hardware event timestamp — not `performance.now()` inside a handler —
removes any advantage from whose listener runs first.

Boundary on each side:

| | Start | End |
|---|---|---|
| gotth-live | native input timestamp in the tab | first main-thread macrotask after the frame in which the morphed DOM satisfied the predicate |
| Next.js | native input timestamp in the tab | first main-thread macrotask after the frame in which React's committed DOM satisfied the predicate |

Identical boundaries. What differs is what happens in between, which is the
finding.

**`t_input` — push interactions (CTR-7, CHT-3, DSH-7).** There is no local
input; the causal start is the server's emission of tick *N*. Both servers emit
tick *N* at `T0 + N × 100 ms` on `CLOCK_MONOTONIC` (§2.5). Procedure:

1. At run start, estimate the offset between the server's `CLOCK_MONOTONIC` and
   the page's `performance.now()` origin with 100 NTP-style exchanges over the
   harness's control channel; take the sample with minimum round-trip and record
   the estimated skew bound.
2. `t_input(N) = T0 + N × 100 ms`, translated onto the page timeline.
3. Publish the skew bound alongside every push row.

The skew is **common-mode**: the same procedure, same host, same clock, both
stacks. It biases both absolute numbers identically and cancels in the A-vs-B
delta. Stated in the report, not assumed by the reader.

For CTR-7 (cross-tab), `t_input` is instead tab A's native input timestamp, and
`t_paint` is tab B's; both tabs run in the same browser process group and share
a `performance` time origin only if same-origin — enforced, since both tabs load
the same origin.

### 3.3 "Interactive" and "time-to-interactive on first load"

**Definition.** A page is *interactive* at the first moment it can accept a user
action and carry it end to end to a server-authoritative state change that
paints.

**Operationalization.** Both apps set `window.__bench.ready = true` exactly once,
and the shim stamps `window.__bench.t_ready = performance.now()` at that
assignment. The condition for setting it:

| Stack | Condition |
|---|---|
| gotth-live | live connection handshake complete (FR-8) **and** the initial `Snapshot`/first patch applied **and** the delegated event root attached (FR-28) |
| Next.js | React hydration complete for the interactive region (signalled from a `useEffect` in the top-level Client Component of that region) **and** the live-data channel (SSE or WS, §5.4) open **and** its first message applied |

**TTI** = `t_ready` (the `performance.now()` timeline starts at
`performance.timeOrigin` = navigation start).

**Verification, not trust.** `t_ready` is self-reported by the app, so it is
independently validated: for 100 loads per stack, the harness fires the app's
headline interaction at `t_ready + 0 ms` and asserts it completes with a normal
latency distribution; and separately fires it at `t_ready − 50 ms` and asserts a
materially higher failure/latency rate. If firing early does *not* degrade, the
`ready` signal is late (conservative) and must be corrected. This closes the
obvious cheat in either direction. (Mirrors review checklist §4.5 — self-reported
telemetry must be externally verifiable.)

**Cold vs warm cache.** *Cold*: fresh browser profile directory,
`Network.clearBrowserCache` + `Network.clearBrowserCookies`, new context, per
iteration. *Warm*: second navigation in the same context with cache retained and
the session cookie retained. Both reported (FR-71.5).

**Also reported, for context only, not gated:** FCP, LCP, TBT, CLS, and INP from
the same Lighthouse/CDP run on both stacks. **Legacy Lighthouse "Time to
Interactive" and Speed Index are NOT used**, because both depend on a network-
quiet window and a long-lived connection with heartbeats can prevent one — that
would penalize the persistent-connection architecture for a property of the
metric rather than of the user experience. Disclosed in the report body.

### 3.4 "Active session"

| Stack | An active session is… |
|---|---|
| gotth-live | one open live connection bound to one browser tab, with a session actor registered, having exchanged at least one frame within the last heartbeat interval, subscribed to the app's live regions |
| Next.js — push variant | one browser tab (or synthetic equivalent) holding the app's SSE stream or WebSocket, with the server-side per-connection state that variant requires, plus its session cookie |
| Next.js — polling variant | one client issuing the app's polling requests at the specified interval, with no persistent server-side connection state |

**Idle / active-light / active-heavy** are three distinct workloads, all
reported:

| Workload | Traffic |
|---|---|
| Idle | connected, no application events, heartbeats only |
| Active-light | counter workload: one `+1` every 10 s per session |
| Active-heavy | dashboard workload: full 53 updates/s push per session, one control interaction every 30 s |

**The polling incomparability, handled head-on (PRD R-16).** A polling stack
holds ~no per-connection server memory and pays in CPU and request overhead
instead. Reporting only memory would flatter it. Therefore **every memory row is
published beside a server-CPU row** for the same workload and concurrency:
mean server CPU seconds per session per minute, from the container cgroup
(`cpu.stat` `usage_usec` delta / duration / N). Memory without CPU is not a
result under this spec.

The TLS-terminating proxy container (§3.6) is **excluded from that figure on both
sides**, for the same reason it is excluded from `M(x)`, and its own `cpu.stat`
delta is **published as its own line** per workload and per variant. The polling
variant shifts work into the proxy (connection churn, TLS handshakes) rather than
into the application server; hiding the proxy's CPU would let it look free by a
second route, which is the same error §3.4 exists to prevent.

### 3.5 "Client JS payload"

**Counts** — every response with a JavaScript MIME type
(`application/javascript`, `text/javascript`, `application/ecmascript`, or a
`<script type="module">` fetch) requested by the page from navigation start
through:

- (a) `t_ready` (first interactive load), **and**
- (b) the completion of the app's full measured interaction set.

Both (a) and (b) are reported. (b) exists because Next.js code-splits and lazily
fetches chunks on interaction; measuring only (a) would understate its payload,
which is not a win we want to take.

**Two figures per FR-71.1:**

- **Transfer bytes** — CDP `Network.loadingFinished.encodedDataLength` (bytes on
  the wire, as served).
- **Decoded/parsed bytes** — `Network.getResponseBody` length after
  decompression.

**Compression normalization.** Both servers serve the comparison figure with
**gzip level 6**. Brotli is reported for information on both, mirroring NFR-2's
convention (gzip is the gate, brotli is informational). Serving one stack with
brotli and the other with gzip is a disqualifying method error.

**Excluded, with reasons:**

| Excluded | Reason |
|---|---|
| HTML documents | measured separately as transfer bytes (§4.6) |
| CSS, fonts, images | not JS; both apps use one stylesheet, no web fonts, no raster images (§2 bounds) |
| Source maps (`.map`) | not executed; not served in production on either side |
| WebSocket / SSE frame payloads | measured as wire bytes (§4.6), not as JS |
| RSC / Flight payload (`text/x-component`) | not JS, but **reported as its own line** beside gotth-live's HTML-fragment wire bytes so the reader sees both totals — excluding it silently would be a hidden asymmetry |
| `shim.js` | identical on both sides; subtracted from both, with the subtracted byte count stated |

**gotth-live's honest inclusion.** gotth-live's client JS = the embedded runtime
file. Where an app uses HTMX (dashboard Region E, AS-3), **HTMX's gzipped bytes
count against gotth-live** in that app's figure. templ generates no client JS,
so app code contributes ~0 — that is a real property, stated, not hidden.

**Also reported:** total transfer bytes to `t_ready` (all content types), as the
un-gameable aggregate.

### 3.6 "Memory per session" — RSS attribution on each side

**Headline figure (both stacks, identical method):**

```
mem_per_session = ( M(N) − M(0) ) / N
```

where `M(x)` is the **median of 60 samples taken at 1 Hz over the last 60 s of a
5-minute steady-state window**, with `x` sessions established, and:

`M(x)` = the serving container's cgroup v2 `memory.current` minus `memory.stat`'s
`file` (page cache), i.e. anonymous + kernel memory attributable to the workload.
Read from outside the process. **Whatever processes the idiomatic architecture
requires are inside that one container and are all counted** — that is how
Next.js's Node server, any worker threads, and any separate WebSocket process
are attributed, and it is symmetric with gotth-live's single Go binary being the
whole container. The one thing that is deliberately outside that container, on
both sides, is TLS termination:

> **TLS boundary (binding on both stacks).** TLS is terminated **outside the
> measured container**, identically on both sides. Each stack runs behind the
> same reverse-proxy image, in a separate container, on the same host; the proxy
> container is **not** included in `M(x)`. The measured container therefore
> serves plaintext HTTP/WebSocket on its container port in both cases, and
> `M(x)` — cgroup v2 `memory.current` minus `memory.stat`'s `file` — covers only
> the application server. Terminating TLS inside one stack's container and
> outside the other's is a **disqualifying method error**, in either direction.
> gotth-live additionally reports an in-process-TLS figure as a labelled
> secondary diagnostic; it is not a comparison row and no Next.js counterpart is
> required.

**Why this rule is here and not only in RFC-0001.** RFC-0001 §6.1 gates
gotth-live's own idle-connection budget on the TLS-outside figure, precisely so
that no measurement outcome makes moving the TLS boundary an available remedy
(RFC §6.1.2). That discipline is one-sided until the same boundary binds the
Next.js side, which is what the rule above does. Measuring gotth-live *with*
`crypto/tls` record buffers against a Node process *without* them would be an
≈18,000 B asymmetry **against** gotth-live; FR-73's honesty clause cuts both
ways, and an unfair-to-ourselves comparison is still an unfair comparison. Both
directions are disqualifying, and the harness asserts the boundary rather than
trusting it: before any D3 cell is recorded, it verifies that the measured
container holds no TLS listener and that the proxy running in front of it is the
same image digest as the one recorded for the other stack's runs, and it writes
both facts into the run manifest (§6).

**The in-process-TLS secondary is measured, never derived (L9-1 condition C-3).**
That figure is produced by re-running this same §3.6 procedure — same session
count, same workload, same sampling window — with the gotth-live container
terminating TLS itself and the proxy removed from the path or reduced to a
layer-4 passthrough, with which of the two stated in the run manifest. It may
**not** be produced by arithmetic over a composition budget — specifically not by
adding an estimated `crypto/tls` line to RFC §6.2 and re-applying that table's
GC-headroom method, which is the derivation C-3 found to be internally
inconsistent there. A secondary derived that way would confirm the estimate it
exists to test. If the in-process run is not performed, the row reads "not
measured" per §7; it is never inferred.

`M(0)` is measured after the *same* warm-up as `M(N)`: the same number of
full-page loads and the same elapsed time, so JIT-warmed code and lazily
compiled routes are in the baseline on both sides.

**Concurrencies:** N = 100 and N = 1000 (FR-71.3; matches G2's 1k). Three
workloads (§3.4). Reported as a 2 × 3 grid per stack, each cell with a paired
CPU figure.

**Secondary figures, reported symmetrically:**

| | gotth-live (Go) | Next.js (Node) |
|---|---|---|
| Runtime-internal | `runtime/metrics` `/memory/classes/total:bytes`, `/gc/heap/live:bytes`, goroutine count | `process.memoryUsage()` rss / heapUsed / external / arrayBuffers; `v8.getHeapStatistics()` |
| Post-forced-GC floor | `debug.FreeOSMemory()` then re-measure | `--expose-gc` + `global.gc()` then re-measure |

The forced-GC floor is a **secondary, labelled** number on both sides or on
neither. Forcing GC on only one stack — in either direction — is a method error.
The headline stays unforced steady state, because that is what a deployment
sees.

**GC configuration is pinned and disclosed:** `GOGC` and `GOMEMLIMIT` on the Go
side; `--max-old-space-size` on the Node side, set equal to the container memory
limit. Values recorded in the run manifest. Neither is tuned for the benchmark
beyond its framework's documented production default; any deviation is an
FR-73 "equivalent tuning effort on the other side, disclosed" event.

**Session driver (how 1000 sessions are created).** 1000 real Chromium tabs is
not feasible on one host. A synthetic driver speaks each stack's actual
protocol — gotth-live: liquid proto over the ADR-001 transport, real handshake,
real events; Next.js: the same document fetch plus the same SSE/WS channel — and
consumes and discards pushed payloads at the rate a browser would (no artificial
backpressure). It is pinned to CPUs disjoint from the server under test.

**Driver validation gate (mandatory before any 1k number is quoted):** measure
per-session memory with **10 real Chromium tabs** and with **10 synthetic
sessions**, on both stacks. If the per-session figures differ by more than 10 %
on either stack, the driver misrepresents a browser and MUST be fixed before the
1k run. The validation numbers are published with the report. Without this, the
1k number is an assertion about a synthetic client, not about sessions.

### 3.7 "SSR / full-page render throughput (RPS)"

**Definition (FR-71.4 — throughput without a latency bound is not a number):**
the highest sustained offered request rate at which, over a 60 s measurement
window following a 30 s warm-up, **p99 response latency ≤ 200 ms and error
rate = 0**.

- **Load model: open (constant arrival rate), not closed.** A closed-loop
  generator suffers coordinated omission and reports optimistic latency.
  Generator: `oha` or `vegeta` in constant-rate mode (choice pinned in
  `bench/versions.lock.md`); `wrk2` acceptable as a cross-check.
- **Rate discovery:** binary search over offered rate, ≥ 8 probe points,
  20 s each, then a full 60 s confirmation run at the discovered rate and at
  rate × 1.1 (which must fail the ceiling, proving the ceiling is the ceiling).
- **What is requested:** the app's main document only. No sub-resources. Fresh
  session cookie per request (cold session — this is the SSR path, not the live
  path). Identical `Accept`, `Accept-Encoding: gzip`, keep-alive on both,
  HTTP/1.1 on both.
- **Isolation:** generator in its own container, `cpuset` disjoint from the
  server under test, so it cannot steal the SUT's CPU. Core counts stated.
- **Path:** the generator addresses the **proxy** (§3.6), not the measured
  container, on both sides; the client→proxy leg is TLS and the proxy→app leg is
  plaintext, identically for both stacks, so the extra hop is common-mode and
  cancels in the A-vs-B delta. Because the proxy is in the path, it can be the
  ceiling: its own saturation rate is measured once against a static response
  served by the proxy itself, and any discovered rate within 20 % of that
  saturation rate is marked **proxy-limited** in the manifest and is reported as
  a bound on the harness, not as a stack's throughput.
- **Caching:** the measured route MUST be dynamic on both sides (§5.5), because
  the equivalent gotth-live route is dynamic. Next.js's ability to serve the
  same page from the full route cache / ISR is measured **separately** and
  reported as an explicit Next.js-advantage row (§4.5, AS-6). We give Next.js
  that win in a labelled row rather than suppressing it by forbidding it.

---

## 4. Dimensions measured, and the harness for each

Browser-side measurement uses **one harness for both stacks**: Playwright
driving headless Chromium over CDP, pinned version, same browser binary, same
flags, same viewport (1440 × 900, DPR 1), same profile handling. No
stack-specific branch exists in the harness beyond app URL and the `ready`
condition wiring of §3.3 — and because both stacks are served sequentially
through the same proxy at the same origin (§3.6), even the URL is identical,
leaving the `ready` wiring as the only per-stack line in the harness.

| # | Dimension | Signal | Harness | Sample plan | Gate |
|---|---|---|---|---|---|
| D1 | Client JS payload, gzipped transfer + decoded | CDP `Network.*` (§3.5) | Playwright/CDP, cold context | 20 loads per app per stack; deterministic — report exact bytes + any variance | Report only (gotth-live's own 12 KB gate is NFR-2, separate) |
| D2 | Event→paint latency p50/p95/p99 | `paint_main`, `paint_present` cross-check (§3.1, §3.2) | Playwright/CDP + `shim.js` | 200 samples/run × 5 runs = 1000 per interaction ID per stack per network profile; inter-interaction gap U(400, 600) ms | G1 applies to gotth-live only; the comparison is reported, not gated |
| D3 | Server memory per active session at N = 100, 1000 | cgroup `memory.current − file`, **TLS terminated outside the measured container** (§3.6) | synthetic session driver + cgroup sampler; 10-tab validation gate; boundary assertion before any cell is recorded | 5 runs × 2 concurrencies × 3 workloads | G2 applies to gotth-live only |
| D4 | SSR/full-page throughput at p99 ≤ 200 ms | open-model load generator (§3.7) | `oha`/`vegeta`, disjoint cpuset | rate sweep + 60 s confirm, × 5 runs | Report only |
| D5 | Time-to-interactive, cold and warm | `t_ready` (§3.3), externally validated | Playwright/CDP | 100 cold + 100 warm per stack, across 5 runs | Report only |

### 4.5 Additional reported rows (not among FR-71's five, but required for honesty)

| Row | Why it exists |
|---|---|
| Next.js static/ISR variant of the measured route, RPS | Gives Next.js its full route-cache win explicitly (AS-6) rather than hiding it behind §5.5's dynamic-route rule |
| C-A local-state counter latency (Next.js only) | Quantifies the client-state ceiling gotth-live structurally cannot reach (AS-1) |
| CHT-2b optimistic send latency (Next.js only) | Quantifies optimistic UI, a v1 gotth-live exclusion (AS-2) |
| Client main-thread CPU time to `t_ready`, and long-task time during the interaction set | CDP `Performance.getMetrics` `TaskDuration` + `Tracing`; cheap, informative, and a place gotth-live may or may not win |
| Server CPU per session per minute, paired with every D3 cell | Prevents a polling architecture from looking free (§3.4) |
| RSC/Flight payload bytes vs gotth-live wire bytes, per app per minute | The two stacks' "data down" channels, compared directly (§3.5, §4.6) |
| **Shared-data duplication per session, gotth-live only, paired with every D3 cell** (A-2, §12) | **AS-8**'s labelled row. Every gotth-live session folds its own copy of the shared data; the Next.js stores keep one array. That cost is **inside** the D3 headline and stays there — it is real and excluding it would be the fairness failure in the other direction — but E6 requires a declared asymmetry to be excluded *or* measured on its own, and this row is the second. **Method:** the same D3 procedure of §3.6, with the app's shared corpus reduced to a single room and twenty rows against §2.4's three rooms and 200, differenced against the ordinary cell. That difference is the duplication's share of `M(N) − M(0)`, measured rather than derived from a struct size, and it is reported as a **gotth-live-side diagnostic and not a comparison row** — there is no Next.js counterpart because there is nothing on that side to measure. If the run is not performed the row reads "not measured" per §7; it is never inferred |

### 4.6 Wire bytes

Per app, per minute, per session, at the specified update rates: total bytes
server→client and client→server. gotth-live: liquid proto frames including
provenance overhead broken out as its own line (PRD Phase 5, R-9). Next.js:
SSE/WS frames plus RSC payloads plus any polling responses. Captured at the
**measured container's** network boundary — that is, on the plaintext proxy→app
leg (§3.6), so TLS handshake and record-framing bytes are outside the accounting
on both sides — and cross-checked against CDP `Network.webSocketFrameReceived` /
`eventSourceMessageReceived`. The client→proxy leg is not the accounting
boundary; if it is ever reported, it is reported for both stacks or neither.

---

## 5. Fairness rules

### 5.1 Production defaults, both sides

| | Configuration |
|---|---|
| **Next.js** | `next build` with `output: 'standalone'`; `NODE_ENV=production`; React production build; **no** `next dev`; no StrictMode double-invocation; no source maps served; default minifier; `next start`/standalone server entrypoint; `reactStrictMode` per template default; `next/font` for any font; `next/image` for any image (there are none, §2 bounds) |
| **gotth-live** | `go build` with default optimization (no `-race`, no `-N -l`); templ generated ahead of time; client runtime minified and gzipped exactly as CI ships it; release configuration of the library (dev-mode inspector NOT loaded, per NFR-8); observability at the setting stated in the manifest (see §5.6) |

Tuning either side beyond its framework's documented production default requires
equivalent tuning effort on the other, disclosed in the method section (FR-73).

**Neither production configuration terminates TLS in the measured container**
(§3.6): both serve plaintext HTTP/WebSocket on their container port and sit
behind the same proxy image. This is the idiomatic self-hosted deployment for
both — it is how `next start` behind a terminating proxy is normally run, and it
is what this monorepo does (Caddy terminates at the edge, backends are reached
over Tailscale). gotth-live's in-process-TLS single-binary deployment is a real
configuration and is measured as the §3.6 labelled secondary; it is not the
comparison configuration.

### 5.2 Machine, isolation, and containers

- Both stacks run **in Docker on the same host**, sequentially, **never
  concurrently**.
- Identical container constraints, recorded from `docker inspect` into each run
  manifest: `--cpus`, `--cpuset-cpus`, `--memory`, `--memory-swap` equal to
  `--memory` (swap disabled), `--pids-limit`, `ulimit -n`, same network mode.
- **Four disjoint cpusets:** **server under test**, **TLS-terminating proxy**,
  **load generator / session driver**, **browser harness**. Core assignments
  stated.
- **The proxy container (§3.6) is part of the topology on both sides and is
  identical on both sides:** same image **by digest**, same configuration file,
  same container constraints, same cpuset, started and stopped with the stack
  under test. Its `docker inspect` output is recorded in the manifest exactly
  like the SUT's. It is excluded from every `M(x)` (§3.6) and from the paired
  server-CPU figure, and its own CPU is published as its own line (§3.4). A run
  in which the two sides' proxy image digests differ is void, not corrected
  after the fact.
- Base images pinned **by digest**, not tag — including the proxy image.
- **Runs are interleaved A/B/A/B** at the run level. We cannot pin the host CPU
  governor (host changes are out of bounds for this project — repo policy), so
  thermal and frequency drift is made common-mode by interleaving instead of
  controlled away. This follows the house A/B convention already used for the
  repo's Go GC benchmark.
- The harness records `hostname`, kernel, CPU model, core count, RAM, Docker
  version, and cgroup limits into every run manifest, and records whether any
  other Compose project is running on the host at run start. A run with
  co-tenancy is marked **contended** in the manifest and is excluded from the
  headline set but still published.

### 5.3 The bench stack is quarantined (FR-74)

Everything node/npm lives under `gotth-live/bench/`, with a committed lockfile
and pinned versions. The bench Compose file is **standalone**: it is never
`include:`d from the repo root Compose, uses its own project name, and binds
only `127.0.0.1` ephemeral ports. With the §3.6 topology, the **proxy container
is the only one that publishes a port**; the measured container's port stays on
the bench project's internal network, which also makes "no TLS listener in the
measured container" checkable by inspection. The proxy's certificate is a
locally generated self-signed cert committed to the bench tree, trusted only by
the harness; no ACME, no public DNS, no host firewall or network-policy change
of any kind is involved. Verified by building and running the library
and all three gotth-live examples on a machine with no node installed (G11).

### 5.4 The Next.js implementation must be idiomatic — and is audited for it

Target configuration (resolves PRD §7.2 Q15): **Next.js 15.x App Router, React
19, Node LTS**, `output: 'standalone'`, self-hosted node server. Exact versions
pinned in `bench/versions.lock.md`. Rationale: this is what a competent team
ships in 2026 for a self-hosted, dynamic, live-data app, and it is the
configuration that makes the comparison meaningful rather than the one that
loses.

| Concern | Required idiomatic approach |
|---|---|
| Static shell | React Server Components |
| Interactive regions | Client Components, scoped as narrowly as a competent team would scope them |
| Mutations (counter, chat send, filters) | **Server Actions** |
| Optimistic feedback | `useOptimistic` for chat send (AS-2) |
| Live data — **primary variant** | **SSE via a streaming Route Handler** (`ReadableStream` in a `GET` route handler), consumed with SWR (`useSWRSubscription`). Fully inside Next.js, no extra process, no custom server |
| Live data — **secondary variant, also measured** | Dedicated WebSocket server (`ws`) alongside the standalone Next server, in the same container. Measured because a perf-minded team would plausibly ship this, and because it changes the memory and latency picture |
| Data fetching / revalidation | SWR where a competent team would use it |
| Code splitting | `next/dynamic` where a competent team would split |

Both live-data variants are measured and both are reported. A polling variant
(`SWR refreshInterval`) is measured **only for D3/D4** (where it changes the
memory-vs-CPU trade fundamentally, §3.4) and reported as a third labelled
column there.

**Pessimization audit — the Next.js app does not get measured until this
passes.** Checklist, executed and its output committed:

- [ ] No `'use client'` at or near the root; client boundaries are as deep as
      the interactivity requires.
- [ ] `@next/bundle-analyzer` output committed; no barrel-file import pulling in
      an unused tree; no unexpectedly large chunk.
- [ ] No unused dependency in `package.json`; `depcheck` clean.
- [ ] Production React confirmed at runtime (no dev-build warnings, no
      `react-dom/profiling`).
- [ ] No `next dev`, no Turbopack dev server, no HMR runtime in the bundle.
- [ ] No artificial `await`/delay, no disabled caching beyond §5.5's dynamic-route
      rule, no throttled revalidation.
- [ ] Lighthouse performance score recorded; a score materially below what the
      app's content warrants is treated as evidence of pessimization and
      investigated before measuring.
- [ ] Every deviation from the Next.js docs' recommended pattern is listed with
      a reason.

**Independent review (resolves PRD §7.2 Q16).** The Next.js implementation is
reviewed for fairness by a reviewer **who is not an author of gotth-live**.
If no external reviewer is available, the report says so plainly in its body
(not a footnote), publishes the complete Next.js source in the PR, and states
that the fairness control was internal only. The answer either way is disclosed.

### 5.5 Route dynamism and caching

The measured route is dynamic on both sides. On the Next.js side this means the
measured route is not `force-static` and is not served from the full route
cache — because the equivalent gotth-live route renders current session state
and cannot be. This is a *fairness constraint on the comparison*, not a claim
that Next.js cannot cache: the cached variant is measured and published as its
own advantage row (§4.5, AS-6).

### 5.6 Observability configuration

gotth-live's default-on observability (FR-38) costs something (NFR-1 budgets it
at ≤ 5 % of p50). Both configurations are measured and both reported:
gotth-live with observability **at its default-on setting** (headline — that is
what a user gets) and with it disabled (secondary). The Next.js app carries no
equivalent default instrumentation, which is itself a §8 parity row. Reporting
only gotth-live's instrumentation-off number would be a tuning asymmetry under
FR-73.

**The provenance log is inside the default-on configuration.** Per
instrumentation §4A.3 it is emitted whenever `Config.Logger` is non-nil and is
exempt from `Info` sampling, so its cost — ≈200 B/record, ≈10.6 KB/s/session at
the dashboard workload — is part of the headline gotth-live number, not outside
it. The run manifest records whether the logger was configured and where the
stream was sunk, because a sink on the SUT's own disk is a contention source
(T-5) at the aggregate volumes Appendix B QA3-3 has to measure.

### 5.7 Warm-up and run hygiene

| Measurement | Warm-up |
|---|---|
| Latency (D2) | 200 discarded interactions per run per interaction ID |
| Throughput (D4) | 30 s at the offered rate, discarded |
| Memory (D3) | establish N sessions, then 5 min settle; sample the last 60 s |
| TTI (D5) | 10 discarded loads per run; the Node process receives the same warm-up request volume as the Go process before any measurement |

**The proxy is warmed with the stack, not before it.** The proxy container
(§3.6) is started fresh alongside the SUT for every run and receives exactly the
warm-up volume in the table above, so its connection pools, session cache, and
any keep-alive state to the upstream are in the same condition on both sides. A
proxy carried warm across an A/B pair, or reused across the two stacks, is a
method error of the same kind as forcing GC on one side only (§3.6) — it makes
the second stack measured look different for a reason that is not the stack.

**JIT asymmetry is disclosed, not engineered away.** Node's V8 needs warm-up to
reach steady state; Go is AOT-compiled and does not. Equal warm-up request
volume gives V8 its steady state. The report states that cold-process behaviour
(first-N-requests latency) is *not* the headline and, where it differs
materially, publishes the first-100-requests latency for both as a separate
disclosed row.

**Network profiles**, applied by CDP `Network.emulateNetworkConditions`
identically to both (browser-side, therefore symmetric):

| Profile | Added RTT | Throughput |
|---|---|---|
| LAN | 0 ms | unthrottled |
| Broadband | 25 ms | 20 Mbps down / 5 Mbps up |
| Mobile | 100 ms | 4 Mbps down / 1 Mbps up |

LAN is the profile G1 is stated against. All three are reported for D2.

**Operator approval.** Bench runs are operator-initiated. The harness refuses to
start unless explicitly invoked, and records host state (§5.2). This spec does
not authorize a run.

---

## 6. Sampling, variance, and reporting

- **5 independent runs** per cell, each with a fresh start of **both** the
  measured container and its proxy (§3.6), a fresh browser profile, and a fresh
  run id.
- **Report per-run p50 alongside the pooled p50/p95/p99.** Percentiles, never
  means, for latency (FR-73).
- **Bootstrap 95 % confidence intervals** (10 000 resamples) on the pooled p50
  and p99. A difference whose CIs overlap is reported as "no measured
  difference", not as a win.
- **Instability rule:** if the spread of per-run p50s exceeds 20 % of the pooled
  p50, the cell is marked **unstable**, the whole cell is re-collected (not
  selectively), and the unstable set is still published.
- **No selective re-running.** The harness writes a manifest for every run it
  starts, including aborted and failed runs, with the abort reason. The report's
  raw-data directory contains every run id the harness ever emitted for the
  final report; a gap in the id sequence is an audit failure.
- **Raw data** committed under `bench/data/<run-id>/` as CSV (one row per
  sample) plus `manifest.json` (versions, host, cgroup limits, GC settings,
  network profile, fixture SHA-256, git SHA of both apps, contended flag,
  **TLS boundary (`outside` | `in-process`), proxy image digest, the
  no-TLS-listener assertion result, provenance-logger state and sink**, and the
  proxy-limited flag of §3.7).
- **Prominence rule (FR-73):** every dimension appears in one table with both
  stacks side by side. Dimensions where Next.js wins are in the same table, same
  typography, with no hedging sentence attached that is not also attached to
  dimensions where gotth-live wins.

---

## 7. Not measured, and why

FR-73 requires that a dimension which cannot be measured fairly is reported as
"not measured, and why" rather than estimated. The closed list at freeze:

| Not measured | Why |
|---|---|
| Serverless / edge cold start, scale-to-zero | gotth-live v1 is a single self-hosted process (PRD §4). There is no gotth-live artifact to measure. This is a Next.js advantage and appears as a §8 parity row, not a fabricated number |
| Multi-node horizontal scaling | Out of v1 scope (§4, BL-1). Next.js scales horizontally by default; gotth-live v1 does not. §8 parity row |
| Real WAN / real mobile radio | We emulate RTT and bandwidth at the browser; we do not reproduce radio scheduling, packet loss, or carrier NAT idle timeouts. Persistent-connection architectures are more exposed to these than request/response ones, so this omission favours gotth-live and is disclosed as such |
| CDN / edge caching of the app shell | Next.js benefits substantially; single-host bench cannot represent it. §8 parity row |
| Image optimization | Neither app has images (§2 bounds), chosen deliberately so the comparison is about reactivity, not about `next/image`. `next/image` is a real Next.js capability and appears as a §8 parity row |
| Build time, CI cost, dependency-audit surface | Plausibly a gotth-live win; excluded because including only the wins we expect to take is exactly the bias this spec exists to prevent. §8 parity rows, unmeasured, marked as such |
| Behaviour under adversarial or slow clients | Covered for gotth-live by the Phase 3 chaos suite; no equivalent Next.js harness exists and building one credibly is out of scope. DSH-8 (4× CPU throttle) is the one degradation shape we do measure, on both |
| SEO, crawlability, accessibility | Not a performance dimension; no honest single number |
| Energy / carbon | No calibrated instrumentation available on this host |
| Client memory (browser tab RSS) | Chromium tab RSS attribution across processes is unreliable enough on a shared host that the number would mislead; client main-thread CPU time (§4.5) is measured instead |

---

## 8. Feature-parity table skeleton (FR-72, filled in Phase 5)

Two directions, one row per capability, **a practical consequence per row — not
a checkmark** (FR-72). "Evidence" names where the claim is checked, so a reader
can audit it. Rows are fixed now; the consequence and evidence columns are
filled in Phase 5. Adding or removing a row after freeze requires §12.

### 8.1 What a Next.js app gets that gotth-live v0.1 does not

| # | Capability | Practical consequence for a team | Evidence / where checked | Status |
|---|---|---|---|---|
| N-1 | Client-side state and sub-RTT local feedback | Interactions that need feedback faster than a round trip (drag, draw, keystroke-driven canvases) are simply not buildable on gotth-live | C-A row (§4.5); PRD §4 BL-3 | |
| N-2 | Optimistic UI (`useOptimistic`) with rollback | A send *feels* instant on a 150 ms link; gotth-live's send feels like the link | CHT-2b row (§4.5); BL-4 | |
| N-3 | Offline / intermittent connectivity | A tunnel, a lift, a flaky mobile radio degrades gotth-live to unusable; the Next.js app can queue | Not measured (§7); BL-2 | |
| N-4 | npm ecosystem and React component libraries | Datepickers, rich text editors, charting libraries, design systems — reuse instead of rebuild | Parity claim; count of components the dashboard would otherwise have needed | |
| N-5 | Static generation, ISR, full route cache | Marketing/content routes serve from cache at throughput gotth-live cannot approach | §4.5 static/ISR RPS row (AS-6) | |
| N-6 | Edge and serverless deployment, scale-to-zero | Deploy targets and cost models gotth-live v1 has no story for | Not measured (§7); PRD §4 | |
| N-7 | Horizontal scale-out; no single-process session ownership | Capacity is an autoscaler setting, not a redesign | Not measured (§7); BL-1, R-14 | |
| N-8 | Client-side routing / SPA navigation | Navigations without a full document round trip | AS-5; BL-9 | |
| N-9 | `next/image`, `next/font`, asset pipeline | Image and font optimization for free | Not measured (§7) | |
| N-10 | Mobile-network tolerance | High-RTT users get a materially worse gotth-live experience | D2 mobile profile (§5.7) | |
| N-11 | Third-party JS component integration | gotth-live's answer is `data-gotth-preserve` (preserve-and-ignore), not integration | FR-27; BL-12 | |
| N-12 | Enormous hiring pool, documentation, Stack Overflow surface | Onboarding cost | Parity claim, unmeasured | |
| N-13 | Streaming SSR with Suspense boundaries | Progressive first paint on slow data | D5 FCP/LCP context metrics (§3.3) | |
| N-14 | Independent client deploy cadence | Client and server can ship separately | Parity claim, unmeasured | |

### 8.2 What gotth-live gets that the Next.js app does not

| # | Capability | Practical consequence for a team | Evidence / where checked | Status |
|---|---|---|---|---|
| G-1 | No client state layer | The desync bug class (server truth vs client cache) does not exist; no reconciliation code to write or review | Count of state-management LOC in each app's diff | |
| G-2 | No build step, no npm, no lockfile for the consumer | One toolchain, one language, no dependency-audit surface on the client | G11 clean-clone check; NFR-5; FR-74 | |
| G-3 | Single-binary deploy | `go build` and ship; no node runtime, no `node_modules` in the image | Container image size, both stacks (reported) | |
| G-4 | Typed, refinement-checked wire protocol | Invalid wire data is rejected at a generated parse boundary before any handler sees it; the protocol has a schema instead of a convention | FR-3, FR-5, wire-audit test; hostile-wire-data suite | |
| G-5 | Per-patch provenance (event → transition → patch) | "Why did this element change?" is answerable in production from a captured frame | FR-41 resolution test; G4 soak (0 unknown origins) | |
| G-6 | Default-on per-connection observability | Metrics and OTel traces spanning event→paint including the client morph, with one option each — no instrumentation code | FR-34, FR-36, FR-38; §5.6 both-configurations measurement | |
| G-7 | One language for the whole app | No second build system, no second on-call surface for a settings page | Parity claim; PRD §1.3 | |
| G-8 | Server-side rendering is the only rendering | No hydration mismatch class of bug; no dual render path | FR-18, FR-19 determinism tests | |
| G-9 | Incremental adoption alongside HTMX | Existing HTMX pages untouched; live regions added page by page | FR-30, FR-31, FR-32; G8 | |
| G-10 | Pure reducers, replayable state | State transitions are testable as pure functions; event logs replay deterministically | FR-14, FR-15; §2.10 property test | |
| G-11 | Client payload bounded by a CI gate | 12 KB gzipped is enforced, not aspired to; it cannot grow silently | NFR-2, NFR-3 size ledger; D1 | |
| G-12 | No client-side dependency supply chain | Nothing to audit, pin, or patch on the client | NFR-5, NFR-6 | |
| G-13 | Session state is authoritative and singular | Two tabs cannot disagree; there is one copy of the truth | F-CTR-5 / CTR-7 cross-tab correctness | |

**Rule for filling this in.** Every "practical consequence" cell states a
concrete cost or capability a team would feel, in one sentence. Rows whose
evidence is "parity claim, unmeasured" say so in the Status column; they are not
dressed up as measured findings.

---

## 9. Threats to validity

Each threat names its mitigation *or* its disclosure. A threat with neither is
not acceptable in this spec.

| # | Threat | Mitigation / disclosure |
|---|---|---|
| T-1 | **App-choice bias.** Three apps chosen by gotth-live's authors are three apps gotth-live is good at (server-authoritative, list/dashboard/chat shaped) | Disclosed in the report body as a limitation. Mitigated partly by making the dashboard genuinely demanding (53 updates/s, 4000 elements) and by including C-A and CHT-2b, the interactions gotth-live structurally loses. §7 names the app classes deliberately excluded (drag, draw, canvas, offline) and PRD §1.3 already bounds them |
| T-2 | **Strawman Next.js implementation** (PRD R-15) | §5.4 pessimization audit with committed output; §5.4 independent-reviewer requirement with mandatory disclosure if unavailable; the Next.js source ships in the PR; §5.4 pins the configuration in advance (Q15) so it cannot be chosen after seeing results |
| T-3 | **Dimension incomparability** (PRD R-16): "memory per session" and "SSR throughput" mean different things in the two architectures | §3.4's three-workload definition plus a paired **CPU** figure on every memory row, so a polling architecture cannot look free; §3.7's latency-bounded throughput; §7's "not measured, and why" escape used rather than forcing a number |
| T-4 | **LAN-only latency flatters the round-trip architecture** | Three network profiles measured (§5.7), all three reported. Real radio behaviour still not reproduced — disclosed in §7 as an omission that favours gotth-live |
| T-5 | **Single-machine contention**: browser, load generator, and server share one host | Disjoint cpusets (§5.2); sequential, never concurrent, execution; contended runs flagged in the manifest and excluded from headline; run-level A/B interleaving so drift is common-mode |
| T-6 | **CPU frequency / thermal drift**, which we cannot control without a host change (out of bounds) | A/B interleaving at run level (§5.2); 5 runs; per-run p50 published so drift is visible; instability rule (§6) |
| T-7 | **Node version / V8 JIT warm state vs AOT Go** | Equal warm-up request volume (§5.7); first-100-requests latency published separately for both; versions pinned and recorded |
| T-8 | **Allocator differences** (Go's runtime allocator + GC vs V8's generational GC + glibc malloc) make RSS comparisons partly a GC-scheduling artifact | Headline is unforced steady-state over a 5-minute window (§3.6); a post-forced-GC floor is published symmetrically for both or neither; GC settings pinned and disclosed; runtime-internal figures published alongside cgroup RSS |
| T-9 | **Synthetic session driver may not represent a browser** | Mandatory 10-real-tabs vs 10-synthetic validation gate with a 10 % tolerance, on both stacks, published (§3.6). No 1k figure is quoted without it |
| T-10 | **`paint_main` is a main-thread signal, not photons on glass** | `paint_present` cross-check on a 1-in-20 subsample; the measured offset distribution published per stack; if the offset differs between stacks by > 5 ms at p50, both signals become co-headline (§3.1) |
| T-11 | **Self-reported `ready` could be gamed in either direction** | Externally validated by firing the headline interaction at `t_ready` and at `t_ready − 50 ms` and asserting the expected difference (§3.3) |
| T-12 | **Lighthouse TTI / Speed Index penalize persistent connections** for a property of the metric, not the UX | Legacy TTI and Speed Index explicitly not used; functional `t_ready` is the definition; the exclusion and its reason are in the report body (§3.3) |
| T-13 | **Compression asymmetry** could swing D1 by ~20 % | gzip level 6 mandated on both for the headline; brotli reported for information on both (§3.5) |
| T-14 | **Lazy-loaded chunks could hide Next.js payload** | D1 measures both to `t_ready` and through the full interaction set (§3.5) |
| T-15 | **RSC/Flight bytes are neither JS nor HTML** and could fall through the accounting | Reported as their own line beside gotth-live's wire bytes (§3.5, §4.6) |
| T-16 | **Observability overhead asymmetry**: gotth-live instruments by default, the Next.js app does not | Both gotth-live configurations measured and published; default-on is the headline (§5.6) |
| T-17 | **Fixture replay could differ between servers** | One committed fixture, one committed SHA-256, one monotonic emit schedule; a conformance test compares rendered DOM at a fixed tick and gates measurement (§2.5) |
| T-18 | **Post-hoc definition drift** — moving a definition after seeing a number | §12 freeze; §11 pre-registered predictions; every amendment dated, reasoned, and disclosed in the report |
| T-19 | **Selective reporting / re-run-until-favourable** | Every started run emits a manifest; contiguous run-id sequence is auditable; instability forces full-cell re-collection with the unstable set published (§6) |
| T-20 | **Both sides authored by the same team**, so both may embody the same misunderstanding | §5.4 independent review; complete source for both apps in the PR; single-command reproducibility (FR-75, §10) so a reader can rerun and disagree with evidence |
| T-21 | **TLS-boundary asymmetry** — measuring one stack with TLS inside the container and the other without swings D3 by ~18 KB/session, and the direction it swings is a choice available after seeing the number | §3.6's boundary rule binds both stacks, in either direction, as a disqualifying method error; the harness asserts no TLS listener in either measured container and equal proxy image digests before recording a cell, and writes both into the manifest (§6). RFC-0001 §6.1.2 additionally removes the incentive: gotth-live's own gate is the TLS-outside figure, so no outcome makes moving the boundary a remedy |
| T-22 | **A derived secondary can confirm its own estimate** — the in-process-TLS figure could be produced by adding an estimated `crypto/tls` line to a composition budget rather than measured (L9-1 C-3 found exactly that arithmetic inconsistent in RFC §6.2) | §3.6 requires the secondary to be an independent run of the same procedure with the boundary moved, or the row reads "not measured" (§7). Arithmetic over a budget table is not a measurement and may not fill that row |

---

## 10. Repository layout and reproducibility

```
gotth-live/bench/
  README.md                  one documented command per side (FR-75)
  versions.lock.md           pinned: Next.js, React, Node, Go, Chromium/Playwright,
                             Docker, oha/vegeta, base image digests, and the
                             TLS-terminating proxy image digest (§3.6)
  package-lock.json          committed (FR-74)
  docker/                    standalone compose; never included from repo root;
                             127.0.0.1 ephemeral ports only; carries the shared
                             proxy service + its config and self-signed cert,
                             identical for both stacks (§3.6, §5.2)
  harness/
    shim.js                  byte-identical, served by both apps
    interactions/            one file per interaction ID: driver + paint predicate
    measure-latency.*        D2
    measure-payload.*        D1
    measure-memory.*         D3 (incl. synthetic session driver + validation gate)
    measure-throughput.*     D4
    measure-tti.*            D5
    analyze.*                percentiles, bootstrap CIs, instability check
  apps/
    counter/{gotth,next}/
    chat/{gotth,next}/
    dashboard/{gotth,next}/
  fixtures/<app>/ticks.jsonl + .sha256
  data/<run-id>/{samples.csv, manifest.json}
  audit/nextjs-pessimization-checklist.md   (§5.4, output committed)
  REPORT.md                  the published report
```

**Reproducibility (FR-75):** one documented command per side regenerates that
side's raw data; one command regenerates the report from `data/`. A number that
cannot be regenerated by that command does not appear in the report.

**Quarantine (FR-74):** nothing under `bench/` is in the Go module build; the
library and all three examples build and run on a machine with no node
installed (G11), and that check is part of the Phase 5 exit evidence.

---

## 11. Pre-registered predictions (fill in before Phase 5 measurement begins)

Recorded before measuring, published unchanged next to the results. Where a
result contradicts a prediction, the report says so. This exists so post-hoc
rationalization is visible rather than invisible.

| Dimension | gotth-live prediction | Next.js prediction | Who wins (predicted) | Actual |
|---|---|---|---|---|
| D1 client JS, counter | | | | |
| D1 client JS, dashboard (incl. HTMX) | | | | |
| D2 CTR-1 p50 / p99, LAN | | | | |
| D2 CTR-1 p50 / p99, mobile profile | | | | |
| D2 CHT-2 (server-confirmed) p50 | | | | |
| D2 CHT-2b (optimistic, Next.js only) p50 | n/a | | Next.js | |
| D2 DSH-7 push p50 / p99 | | | | |
| D3 memory/session, idle, N=1000 | | | | |
| D3 memory/session, active-heavy, N=1000 | | | | |
| D3 server CPU/session/min, active-heavy | | | | |
| D4 RPS at p99 ≤ 200 ms, dashboard | | | | |
| D4 RPS, Next.js static/ISR variant | n/a | | Next.js | |
| D5 TTI cold / warm, dashboard | | | | |
| D3 gotth-live in-process-TLS secondary (§3.6) | | n/a | n/a — not a comparison row | |

All D3 rows are the **TLS-outside** figure (§3.6). The final row is gotth-live's
labelled secondary diagnostic: it carries a prediction so the measured value can
contradict it, and no Next.js counterpart exists by design.

---

## 12. Change control and sign-off

**Freeze.** On L9-1 + PM-1 + QA-2 sign-off, §2 (apps, interactions, data
volumes, asymmetry register), §3 (definitions), §5 (fairness rules), §7 (not
measured), and §8's row set are **frozen**.

**Amendment.** Any change after freeze requires:

1. A dated entry in the amendment log below, stating what changed, why, and
   whether any measurement had already been taken under the old text.
2. L9-1 approval.
3. If measurement had begun, **every affected cell is re-collected in full** and
   the amendment is disclosed in the report body — not a footnote (FR-73).

**Post-measurement amendments are the failure mode this spec exists to
prevent.** An amendment that arrives after a number is known and that moves a
definition in the direction of that number is a fairness failure regardless of
its technical merit; L9-1 should reject it and require the number be published
as measured.

### Amendment log

| Date | Change | Reason | Measurement taken under old text? | Approved by |
|---|---|---|---|---|
| — | initial draft | — | no | pending |
| 2026-08-04 | **A-1 — TLS boundary made binding on both stacks.** §3.6 gains the TLS-boundary rule transplanted from RFC-0001 §6.1.1 (TLS terminated outside the measured container, same proxy image in a separate container on the same host, proxy excluded from `M(x)`, asymmetry disqualifying in either direction, gotth-live's in-process-TLS figure a labelled secondary and not a comparison row), plus a requirement that the secondary be independently measured rather than derived. Collateral alignment in §3.4, §3.7, §4.6, §5.1, §5.2, §5.3, §5.6, §5.7, §6, §9 (new T-21, T-22), §10, §11, Appendix A.4; new Appendix B | L9-1 cycle-2 condition **C-5**: the spec contained zero occurrences of "TLS", so the memory method was underspecified and the fairness contract bound gotth-live alone — the ~18,000 B `crypto/tls` asymmetry ran **against** gotth-live, and FR-73's honesty clause cuts both ways. C-3 additionally forbids deriving the in-process secondary from RFC §6.2's own GC-headroom arithmetic, which would be self-confirming | **No.** No measurement of any kind has been taken under any version of this spec. The document is pre-freeze (L9-1 and PM-1 sign-off still pending) and Phase 5 is unstarted, so §12's re-collection obligation is not engaged and nothing is invalidated | L9-1 (substance approved in [RFC-0001 cycle-2 review](../rfc/001-review-cycle-2.md) §5.1 and conditions C-5/C-3); PM-1 acceptance pending per C-5 |

| 2026-08-05 | **A-2 — two declared asymmetries enter the register, and AS-3 is qualified.** §2.6 gains **AS-8** (per-session duplication of the shared data, gotth-live only, not excluded from the measured surface and given its own labelled row) and **AS-3's "Same visible behaviour" is qualified** to "within one tab", naming region E's page-load-cookie keying and the two-tabs consequence. Collateral: one new row in §4.5 carrying AS-8's measurement and its method, so that AS-8's Handling column points at something that exists | L9-1 condition **C-43** ([checkpoint-3 review](../reviews/checkpoint-3.md) §6.3): two of `bench/README.md`'s G-series entries — **G-3** and **G-5** — are asymmetries rather than construction notes, and E6 is *"any place the two differ appears in §2.6's asymmetry register"*, a closed list only §12 may add to. G-3 is expressly **not** excluded from the measured surface (*"a real per-session memory cost that D3 will measure"*), so E6's remedy is a labelled row; G-5 qualifies AS-3's own sentence from outside the register. E6's cost of getting this wrong: *"an undeclared asymmetry discovered after measurement invalidates the affected dimension and forces a re-run under §12"* | **No.** `bench/data/` contains no run ids and Phase 5 is unstarted, so §12's re-collection obligation is not engaged and nothing is invalidated. L9-1's §10.6: *"this is the last moment that is true"* | **L9-1 approved** in [checkpoint-3 review](../reviews/checkpoint-3.md) §10.6 (`dd173542`): *"I approve, as §12 requires, the amendment adding **AS-8** for the two `bench/README.md` G-series entries and qualifying **AS-3**'s 'same visible behaviour'."* **PM-1** records no product-surface objection in [gates/checkpoint-3.md](../gates/checkpoint-3.md) §8.3 (`26b61cf9`). Applied by QA-2 (spec owner) |

**A-2 in full, and three things about it that a later auditor should not have to
reconstruct.**

1. **It is logged even though the spec was not yet frozen when it was written.**
   §12's amendment procedure binds changes made *after* freeze, and A-2 lands in
   the same session as the freeze rather than after it, so a narrow reading would
   not require a log entry at all. It is logged because L9-1's C-43 falsifier
   asks for one by name, and because a register that gains its last entry in the
   same landing that closes it should say so where the closing is recorded. The
   stricter reading costs one row and removes an argument nobody should have to
   have later.
2. **Both entries follow the shape L9-1 approved, and not the other one C-43
   offered.** §6.3's falsifier allowed *"either an AS-9 or a qualification on
   AS-3's 'same visible behaviour'"* for G-5; §10.6's approval names the
   qualification. AS-3 is qualified in place and there is no AS-9, because that
   is the branch that was signed.
3. **AS-8 runs against this stack, and that is the point.** The remedy chosen is
   the one that keeps the cost inside the D3 headline and publishes its size
   beside it. The alternative E6 permits — excluding it from the measured
   surface — was available and is refused here in writing, because G-3 itself
   says the cost is real and D3 will measure it, and an exclusion granted to the
   author's own stack after that sentence was written is precisely the failure
   mode the paragraph below A-1 describes.

**A-1 in full, for the reader who will audit this later.** The change is a
*transplant*, not a new decision: the substance was argued and approved in the
RFC-0001 cycle-2 review, and this spec is where it becomes binding on the
Next.js side. The transplanted paragraph in §3.6 is reproduced with its substance
intact; only the surrounding prose is this document's own. Three things about the
timing are worth stating plainly, because §12 exists to catch exactly the pattern
this is not:

1. **It moves a definition before any number exists**, which is the only time a
   definition may safely move. §12's failure mode — an amendment that arrives
   after a number is known and moves a definition toward that number — cannot
   apply here, and the "no measurement taken" column above is the checkable form
   of that claim: `bench/data/` contains no run ids.
2. **It makes the contract harder for gotth-live, not easier.** The boundary it
   fixes is the one that removes an ~18,000 B advantage gotth-live would
   otherwise have been *penalised* by — but it equally forecloses ever taking
   the reverse asymmetry, and RFC §6.1.2 has already committed gotth-live's own
   gate to the same figure with a ratchet-down clause. The amendment closes an
   escape hatch in both directions.
3. **It adds an assertion, not just a rule.** §3.6 now requires the harness to
   verify the boundary (no TLS listener in either measured container, equal
   proxy image digests) before a D3 cell is recorded, and §6 records the result
   in the manifest. A fairness rule nobody checks is a fairness rule that
   silently stops being true — the same standard §3.3 applies to the
   self-reported `ready` signal.

### Sign-off

| Role | Name | Verdict | Date | Where it is recorded |
|---|---|---|---|---|
| QA-2 (author, method) | QA-2 | **signed** | 2026-08-05 | this table, and the paragraph below it |
| L9-1 (fairness veto) | L9-1 | **signed** | 2026-08-05 | [`docs/reviews/checkpoint-3.md`](../reviews/checkpoint-3.md) §10.6, `dd173542` |
| PM-1 (product-surface equivalence) | PM-1 | **signed** | 2026-08-05 | [`docs/gates/checkpoint-3.md`](../gates/checkpoint-3.md) §8.3, `26b61cf9` |
| Independent Next.js reviewer (§5.4) | | pending — or disclosed as unavailable | | Appendix A.1; **not a freeze condition** — §12's freeze names L9-1 + PM-1 + QA-2 |

The fifth column is new. Three of these four signatures live in three different
documents owned by three different people, and a table that records a verdict
without saying where the verdict was actually given is a table that has to be
taken on trust. **Every row above was read in the commit named beside it before
it was written here**; none of them was inferred from a message, a summary or
somebody's report of somebody else's decision.

**QA-2's own sign-off, in the terms the header assigns me — method.** I sign
`docs/bench/equivalence-spec.md` for the freeze under §12 at version 0.3
including amendments A-1 and A-2, on the gate this document gives me: that §3's
definitions are operational rather than aspirational, that §5's fairness rules
bind both stacks symmetrically, and that §7 says what is not being measured. The
ground is that §3.6 has been **run** rather than only written — four measurement
campaigns have executed it unmodified, `measure.sh` carrying one sha256 across
every tree — and that §5.6's headline rule has been upheld against the person
quoting it rather than for them, which is the only evidence that a fairness rule
is real.

**What my signature does not cover, stated so it cannot be read wider.**

- **Not product-surface equivalence.** That is PM-1's gate and PM-1 signed it in
  their own document, in their own terms.
- **Not the fairness veto.** L9-1's, unexercised, in their own document.
- **Not a claim that the built apps conform to §2.** They may not: **Q-BENCH-1**
  is open — §2.1's F-CTR-1 makes the counter's state per session and both stacks'
  bench counters are global. PM-1 §8.3 leaves that with me and it stays mine. It
  is an **E1 conformance question about the applications**, and after this freeze
  it is one that must be answered by changing the apps or by paying §12's
  amendment price, which is exactly the discipline the freeze exists to impose.
- **Not a claim that any number exists.** None does. Freezing a specification
  before measurement is the entire point of §12 and the only state in which a
  freeze means anything; `bench/data/` contains no run ids at the moment this
  signature is written, and the amendment log's column above says so in the form
  a later reader can check.

**Why signing rather than waiting.** §12's freeze is what makes §2's definitions
binding, and four documents — `docs/PRD.md`, `docs/OPERATOR-QUESTIONS.md`,
`docs/api-surface.md` and `docs/pm/checkpoint-3-scope.md` — have been relying on
that freeze while this document said of itself that it was a draft. The two
available resolutions were to sign or to make four documents stop saying frozen,
and the second weakens a fairness contract to match a missing signature rather
than the other way round. Nothing in either review argues the spec is unready.

---

## Appendix A — Open questions for the orchestrator

1. **§7.2 Q16 — independent reviewer.** Is a reviewer outside the gotth-live
   author group available for the Next.js implementation? §5.4 requires either a
   name or an explicit disclosure of "internal control only" in the report body.
   QA-2 cannot resolve this alone.
2. **Bench host.** §5.2 requires a host that is not serving production traffic
   during the run and is not the public edge. Which machine, and does the
   operator approve Phase 5 runs on it? This spec deliberately does not choose.
   Amendment A-1 raises the requirement from three disjoint cpusets to four
   (§5.2), so the host must have enough cores to give the proxy its own without
   squeezing the SUT's — a sizing input for whoever picks the machine.
3. **ADR-001 transport.** §3.4 and §3.6's synthetic session driver must speak
   gotth-live's real transport. The driver cannot be written until ADR-001
   lands; the spec is written to be transport-agnostic, but the schedule is not.
4. **RFC-0001 memory target — resolved, and now reciprocal.** G2's
   `target: set by RFC-0001` is the gate for gotth-live's own memory number.
   RFC-0001 §6.3 adopts this spec's §3.6 verbatim, so Phase 5 measures one thing
   once, as asked. The dependency now runs both ways: RFC §6.1.1 supplied the
   TLS boundary that amendment A-1 transplanted into §3.6, and RFC §6.1's gate
   (≤ 46,080 B, TLS outside) is stated against this document's method. **A change
   to §3.6 is therefore a change to RFC-0001's gate**, and must be raised with
   DEV-1 and L9-1 in the same PR, not landed here alone. Remaining open item:
   PM-1 still owes C-6 (the PRD's memory annotations are inverted relative to the
   approved decision) — not QA-2's to discharge, but it is the last place a
   reader can find the old TLS-in-process framing presented as authoritative.
5. **Next.js live-data variant count.** §5.4 measures SSE (primary), WebSocket
   (secondary), and polling (D3/D4 only). That is 2–3 columns per cell and a
   material increase in Phase 5 run time. PM-1/L9-1 may cut the WebSocket
   variant; QA-2's position is that cutting it weakens the fairness story more
   than it saves time.
6. **Proxy image choice.** §3.6 requires the same reverse-proxy image on both
   sides but deliberately does not name it; the obvious candidate is the one this
   monorepo already runs at the edge. Whichever it is, it is pinned by digest in
   `bench/versions.lock.md` and is a bench-project container only — amendment A-1
   changes nothing about this repo's production edge, adds no host or network
   policy, and does not touch the Caddyfile.

---

## Appendix B — Phase 3 measurement obligations owned by QA-2

Three defaults approved in the Phase 0 design package are **safety-chosen, not
measured**. L9-1's cycle-2 review assigns each to QA-2 at Phase 3, because the
chaos and performance suites are the only place they can be validated. They are
recorded here — in the fairness contract rather than only in the RFC — for two
reasons: this document is QA-2's memory, and each of the three can move a number
this spec publishes.

None of them is a comparison row. They are gotth-live-internal defaults; the
Next.js side has no counterpart and none is required.

| # | Obligation | Value under test | Why it is open | Source | How it gets validated |
|---|---|---|---|---|---|
| **QA3-1** | `coalesce_flush_at` — the H-4 flush trigger | **512**, half of protocol.md H-4's ceiling (RFC §7.4) | Chosen for margin, never measured. Too low costs extra frames to a client that is already behind; too high walks back toward the ceiling the flush trigger exists to keep clear | RFC-0001 §16 **O11**; cycle-2 review §1 (B-4) | Dashboard workload (§2.4, ≈53 logical updates/s) against a client held behind by §5.7's mobile profile and by DSH-8's 4× CPU throttle. Report frames emitted per second and the maximum observed `contributing_event_ids` length, so the margin is a measured distance rather than a chosen fraction |
| **QA3-2** | `MinResyncInterval` / `ResyncBurst` | **1 s** / **3** (RFC §7.6) | Set to make amplification impossible, not tuned. A legitimate client on a lossy link may need to resync more often than the bucket allows, and the failure is user-visible: `Error{RATE_LIMITED}` with no render | RFC-0001 §16 **O12**; cycle-2 review §1 (B-5) | Chaos-suite loss and reconnect-storm cases. The deciding number is the rate at which a **legitimate** client is rate-limited, not the amplification bound — the bound is already proven, the false-positive rate is what is unknown |
| **QA3-3** | Provenance-log volume | ≈200 B/record ⇒ **≈10.6 KB/s/session** at the dashboard workload (instrumentation §4A.2/§4A.4) | An estimate, and an aggregate one: at D3's active-heavy N = 1000 it implies ≈10.6 MB/s out of a single container. That is a host-contention source under T-5 and plausibly a buffer line inside `M(x)` itself, so it can touch the headline memory number rather than only the operator's disk budget | instrumentation.md **I6** (QA-2 + PM-1) | Measure the real per-record size and per-session rate on the dashboard workload; measure the log path's share of §5.6's default-on-vs-off delta and of NFR-1's ≤ 5 % budget. The PM-1 half — whether a sampled-in-production / full-in-soak mode is wanted — cannot be settled before that number exists, and §4A.3 constrains the answer: G4 depends on the log being unsampled |

**The values in force are recorded, not assumed.** Every run manifest (§6) states
the `coalesce_flush_at`, `MinResyncInterval`, and `ResyncBurst` in effect and
whether the provenance logger was configured, so a re-tune between runs is
visible in the data rather than inferable only from a git log.

**Interaction with this spec's freeze.** All three are *implementation defaults*,
not definitions in §2/§3/§5, so re-tuning one is not an amendment to this
document. But two of them move numbers this spec publishes: QA3-1 changes
gotth-live's wire bytes (§4.6) and can change push latency (D2), and QA3-3 is
inside the default-on observability configuration §5.6 makes the headline.
Therefore **a Phase 3 re-tune that lands after Phase 5 measurement has begun
forces full re-collection of the affected cells under §12's rule**, exactly as a
definition change would. The cheap way to avoid that is to finish Phase 3's
tuning before Phase 5 starts — which is the sequencing QA-2 will hold to, and the
reason these three are written down here now rather than discovered later.
