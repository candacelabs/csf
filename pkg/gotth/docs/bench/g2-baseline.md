[identifiers genericized for publication - measurements unmodified]

# G2 baseline — measured steady-state memory per idle connection

| Field | Value |
|---|---|
| Owner | DEV-1 (the measurement); QA-2 (the method, and D-10) |
| Status | **Measured.** Baseline, not the gate — G2 is enforced at Phase 5. **The current figure is §9.10's**, measured at the tree this PR ships: 45,768.7 B, which is *at* the 46,080 B gate rather than clear of it (§9.10.9). §4's figure below missed it by 79 % and is kept as measured. **The gate has not been moved and the method has not been changed** |
| Method | [equivalence-spec §3.6](equivalence-spec.md), unmodified |
| Satisfies | PRD v0.5 Phase 3 exit box "G2's memory baseline exists and RFC-0001 §6.2 is corrected in the same PR"; checkpoint-2 gate §6.2; RFC-0001 §16 **O7** |
| Harness | [`test/memory/`](../../test/memory), its own module |
| Raw data | [`docs/bench/data/g2-baseline/`](data/g2-baseline/) — every run id the harness emitted. One was missing and is now here; that story is in the [re-measurement README](data/g2-baseline/remeasure-2026-08-05/README.md) |
| Measured | 2026-08-04 (§4); re-measured 2026-08-04/05 (§9, §9.10) |

> **The headline, first, because burying it would be the failure this project
> keeps writing documents to prevent.**
>
> **Measured: 82,559 B of steady-state memory per idle connection**, at N = 1000,
> Idle workload, TLS terminated outside the measured container, pooled over five
> independent runs with a 2.2 % spread.
>
> RFC-0001 §6.2's composition estimate was **42,416 B**. The measurement is
> **1.95×** that.
>
> The G2 gate is **46,080 B**. The measurement is **1.79×** that — over by
> **36,479 B**, or 79.2 %.
>
> Removing default-on observability — which equivalence-spec §5.6 does **not**
> allow the headline to do — takes it to **57,135 B**, still **1.24×** the gate.
>
> **The gate has not been moved and the method has not been touched.** RFC-0001
> §6.1.2 pre-registered the response to exactly this outcome before any
> measurement existed: the target does not move, the overage is attributed to a
> named line, and a benchmark-method change is not an available remedy. §5
> attributes it; §6 says whose decision the remedy is (it is not DEV-1's).
>
> **Three cells, eleven runs, 2.2 % spread on the headline.** The single largest
> term in the overage is one no budget in this project has a line for: **≈25 KB
> per session of default-on observability, of which ≈11 KB is a permanently
> doubled goroutine stack** (§5.1).

> ### ⚠ SUPERSEDED AS A CURRENT FIGURE — see §9.10, re-measured 2026-08-05
>
> **Everything above and in §1–§8 is left exactly as it was**, because it was
> true of the code it measured and a corrected number with the wrong one deleted
> tells a reader nothing about how far off it was. It is **not** the current
> figure. §9 re-measured it after three landings; **§9.10 re-measures it again at
> `d66e4953`, the tree this PR ships**, by the same method, paired against the
> same reference tree in one session.
>
> | | headline, obs on | vs the 46,080 B gate |
> |---|---:|---:|
> | §4 — `35eb24a4`, 5 runs | 82,559 B | 1.79× |
> | §9.4 — `ce52d2f9`, 3 runs | 64,970 B | 1.41× |
> | §9.10.3 — `5a2ca417`, 2 runs | 45,181 B | 0.98× |
> | **§9.10.5 — `d66e4953`, 5 runs** | **45,769 B** | **0.993×** |
>
> **Quote §9.10, not this blockquote, and quote it with §9.10.9 attached** — the
> current figure is under the gate by 0.68 %, which is smaller than its own
> cell's 5.5 % spread, so it says the tree is *at* the gate and not that it has
> cleared it. And quote none of them as G2: §3.6's 10-real-tab driver validation
> gate has still not been run, by any of the four campaigns, so every figure in
> this document is an assertion about a synthetic client (§7.1, §9.9.1,
> §9.10.11).

---

## 1. What this is, and what it is not

RFC-0001 §6.2 opens with *"An **estimate**, not a measurement. Phase 1 records
the measured baseline (checklist §8.8) and this table is corrected in the same
PR."* Phase 1 did not. Checkpoint 2 did not. PM-1's checkpoint-2 ruling §6.2
made it a Phase 3 exit box, on the reasoning that it went missing twice because
it was owed by a *phase* and a phase is not a gate. This document is that
baseline, and RFC-0001 §6.2 is corrected in the same landing.

**It is a baseline, not G2.** G2 is enforced in Phase 5, at 1k idle sessions, by
QA-2, against the comparison stack. Nothing here ticks G2.

**The method is equivalence-spec §3.6, unmodified.** §3.6 is frozen (§12) and
was not amended, extended or interpreted for this run. Where §3.6 asks for
something this run did not do, §7 below says so and does not substitute
anything.

**A benchmark-method change was not available and was not sought.** RFC-0001
§6.1.2 pre-registers the response to a miss *before* any measurement, precisely
so that no outcome makes a method change a remedy. The figure below misses. The
method is the one that was written down first.

---

## 2. Method as run

### 2.1 The figure

```
mem_per_session = ( M(N) − M(0) ) / N
```

`M(x)` is the median of 60 samples taken at 1 Hz over the last 60 s of a
five-minute steady-state window, with `x` sessions established, where `M(x)` is
the serving container's cgroup v2 `memory.current` minus `memory.stat`'s `file`,
read from **outside** the process.

Read from outside means read by a shell loop on the host, out of
`/sys/fs/cgroup/system.slice/docker-<container-id>.scope/`. The path was
resolved and verified per container before any number was believed;
`docker stats` was never substituted, because it is a different quantity
reached through a different path.

`file` was **0 in every sample of every window**, so `M(x)` equalled
`memory.current` throughout. That is a property of the container's page cache,
not of the arithmetic: the binaries are on a read-only bind mount whose pages
were first faulted outside this cgroup. The subtraction is still performed and
still recorded.

### 2.2 Warm-up, and why M(0) has one

`M(0)` was measured after the *same* warm-up as `M(N)`: **200 full-page loads**
of the application document, from a container on the driver's cpuset, in both
windows. §3.6 requires this so that JIT-warmed code and lazily compiled routes
are in the baseline on both sides.

The two windows of a run are **separate container lifecycles**, and the order
**alternates between runs** — `M(0)` first on odd runs, `M(N)` first on even
ones — so that a host drifting during a run does not bias every run in the same
direction. The order is recorded per run.

### 2.3 Workload

**Idle**, per §3.4: connected, no application events, heartbeat frames only. The
driver acknowledges every `Snapshot` and every `Patch` the moment it reads it,
and echoes every server `Heartbeat` with the nonce and the interval verbatim.

Both are load-bearing and neither is optional:

- Not acknowledging would leave patches occupying the server's unacked window,
  which is per-session memory the server would be holding *because of the
  driver* — the artificial backpressure §3.6 forbids, in the direction that
  inflates the number being measured.
- Not echoing heartbeats would evict every session at `HeartbeatTimeout`
  (50 s), because liveness in this protocol is the `Heartbeat` **frame** and not
  an RFC 6455 ping (protocol.md §3.4). A five-minute window would have measured
  a server whose sessions were dying.

The driver reports `live`, `mounted`, `closed`, `dial_errors` and `read_errors`
per run. **Every measured run reached exactly N live sessions with zero dial
errors, zero read errors and zero closes**, and a run that had not would have
aborted rather than been reported.

### 2.4 The TLS boundary

> §3.6: *"TLS is terminated **outside the measured container**… The measured
> container therefore serves plaintext HTTP/WebSocket on its container port…
> Terminating TLS inside one stack's container and outside the other's is a
> disqualifying method error, in either direction."*

**The measured container terminated no TLS.** `memsrv` has no `crypto/tls`
import, no certificate flag and one `net.Listen` listener; the boundary is
enforced by there being nothing to misconfigure. The harness nevertheless
**asserts** it from outside before recording any sample, per §3.6's "the harness
asserts the boundary rather than trusting it", in three ways, all recorded in
every run manifest:

| Assertion | Result |
|---|---|
| the container's own `/introspect` reports `tls_listeners` | `0`, every window |
| a plaintext `http://` request to the published port succeeds | yes, every window |
| a `https://` request to the same port completes a TLS handshake | **no**, every window |

**No reverse proxy was run, and that is stated rather than left ambiguous.**
§3.6 places the same proxy image in front of *both* stacks so that the extra hop
is common-mode and cancels in the A-vs-B delta, and it excludes the proxy
container from `M(x)` by definition. With one stack measured and the proxy
excluded from `M(x)`, its absence cannot move `M(x)`. The half of the rule that
binds a gotth-live-only baseline is the half asserted above: the measured
container terminates no TLS. **The reverse-proxy symmetry binds the Next.js side
at Phase 5 and QA-2 owns it there** — including §3.6's requirement that the
proxy image digest recorded for one stack equal the one recorded for the other.
It is not discharged here and is not claimed to be.

The **in-process-TLS secondary** of §3.6 is **not measured** here. See §7.

### 2.5 The application under test

The smallest complete live application: one fragment, one event, an `int64` and
a session ID of state. That is deliberate. RFC-0001 §6.2's budget is a
*library*-per-connection budget whose application-state line is 500 B ("the
counter example's is 24 B"), and a figure meant to correct that table must not
carry an application the table never budgeted for.

Each connection authenticates as its **own subject**. A thousand idle sessions
are a thousand users, not one user with a thousand tabs, and
`MaxSessionsPerIdentity` (default 20) is a real production bound the harness
must not have to raise to reach N=1000. Raising it would have been a
benchmark-shaped configuration change inside a measurement whose whole point is
that the configuration is the shipped one.

### 2.6 Observability configuration

equivalence-spec §5.6 puts gotth-live's default-on observability inside the
**headline** configuration — "that is what a user gets" — and puts the
provenance log inside it explicitly. Both configurations were measured:

| Cell | `Config.Logger` | `Config.Metrics` | `Config.Tracer` |
|---|---|---|---|
| **headline** | slog JSON handler | OTel SDK meter provider, manual reader | OTel SDK tracer provider, batch processor over a dropping exporter |
| secondary | nil | nil | nil |

The provenance stream was sunk to the container's **stderr** (the docker log),
not to a file on the SUT's own filesystem. §5.6 asks where it was sunk because a
sink on the SUT's disk is a T-5 contention source; on the Idle workload the
volume is one record per mount and silence thereafter, so this choice cannot
move the number either way, and it is recorded because §5.6 says to record it.

### 2.7 Everything pinned, and the manifest

Every run writes `run.json` carrying all of it. The load-bearing values:

| | |
|---|---|
| Measured container | `dis-gotth-live:latest`, `sha256:e146d50d5de6…` |
| Container memory limit | `--memory 2g --memory-swap 2g` |
| Measured container CPUs | `--cpuset-cpus 24-27` (4 cores) |
| Driver CPUs | `--cpuset-cpus 28-31` (4 cores, **disjoint**) |
| `GOGC` | **100** — Go's documented production default, untuned |
| `GOMEMLIMIT` | **2GiB** — set equal to the container limit, for symmetry with §3.6's Node rule (`--max-old-space-size` = container limit). The working set is two orders of magnitude below it, so it is never approached and never changes GC behaviour |
| `GOMAXPROCS` | 4, derived from the cpuset by the runtime |
| Go | go1.26.5 |
| Sessions per identity | default 20, unraised; one subject per connection |
| `coalesce_flush_at` / `MinResyncInterval` / `ResyncBurst` | 512 / 1s / 3 — the defaults equivalence-spec Appendix B (QA3-1, QA3-2) requires every manifest to state |
| `MailboxDepth` / `AckChannelDepth` / `AckWindow` | 64 / 32 / 16 |
| `HeartbeatInterval` / `HeartbeatTimeout` / `IdleTimeout` | 20s / 50s / 30m |

**The three cells were measured against the same binary, and that was checked
rather than assumed.** The manifests record two different git SHAs — `35eb24a4`
for the headline cell, `7e2d918d` for the other two — because another agent was
committing to this branch while the campaign ran. The diff between them touches
`internal/obstest/obstest.go`, `internal/wsx/wsx_test.go` and
`live/instrumentation_test.go` and nothing else, and `go list -deps ./cmd/memsrv`
does not contain `internal/obstest`. No library code that the measured binary
links changed between the first run and the last. The `git_dirty: true` on the
later cells is this document and its data directory, which are not linked into
anything.

---

## 3. The host, and contention

This is a shared 32-core VM with 38 GB of RAM running roughly nineteen unrelated
containers, none of which were touched. The measured container and the driver
were pinned to disjoint four-core sets; the unrelated containers are **not**
pinned and can be scheduled onto those cores.

| | At the start of the five headline runs |
|---|---|
| uptime / cores / RAM | up 6 days, 32 cores, 38 GB |
| one-minute load average | 2.65, 3.96, 5.93, 9.78, 11.46 |
| unrelated containers running | 19–20, none touched, none stopped, none restarted |

**Every run in this data set is flagged contended, and the flag has a stated
rule**: a window is contended if the one-minute load average at either end of it
is at least the number of cores the measured container was given. That rule is
deliberately sensitive — with four SUT cores and a background load average of
4-6, every window trips it.

**Why the number is published anyway, and what contention can and cannot do to
it.** CPU contention delays the *sampler* and slows *GC*. The harness catches the
first directly: `CheckWindow` rejects any window whose samples drift outside
1 Hz ± 150 ms, and **no window in this data set was rejected**. What contention
cannot do is change the number of resident bytes attributable to a thousand
established, idle sessions; the second-order path — a starved GC leaving more
garbage resident — pushes the figure **up**, against gotth-live, which is the
direction in which reporting is safe.

**That is an argument, and there is also a measurement of it.** The load average
at the start of the headline cell's windows ranged from 2.65 to 11.46 across the
runs, a factor of 4.3 — the high end being this project's own `ci.sh` running on
a disjoint cpuset at the time. Against that 4.3× range of host load, the five
per-session figures spread **2.2 %**, and the *least* contended run (load 2.65)
produced the *lowest* figure (81,080 B) while the most contended (load 11.46)
produced 82,670 B. The sign is the one the argument above predicts and the
magnitude is inside the noise. **The contention is real, it is flagged on every
run, and it is not what makes this number miss the gate by 79 %.**

---

## 4. Results

### 4.1 The headline

Cell: **N = 1000, Idle, TLS outside, observability on, five independent runs.**

| Run | Order | M(0) | M(N) | Δ | mem_per_session |
|---|---|---:|---:|---:|---:|
| r01 | M(0) first | 3,661,824 | 85,475,328 | 81,813,504 | **81,813.5 B** |
| r02 | M(N) first | 3,846,144 | 86,724,608 | 82,878,464 | **82,878.5 B** |
| r03 | M(0) first | 3,665,920 | 86,335,488 | 82,669,568 | **82,669.6 B** |
| r04 | M(N) first | 3,805,184 | 84,885,504 | 81,080,320 | **81,080.3 B** |
| r05 | M(0) first | 3,649,536 | 86,208,512 | 82,558,976 | **82,559.0 B** |

Every window is 60 samples at 1 Hz; none was rejected. Every M(N) window held
exactly 1000 live sessions, 1000 mounted, 0 closed, 0 dial errors, 0 read
errors.

| | |
|---|---:|
| **Pooled (median of the five per-run figures)** | **82,559.0 B/session** |
| min / max | 81,080.3 / 82,878.5 B |
| per-run spread | **2.2 %** — well inside §6's 20 % instability rule; the cell is **stable** |
| vs RFC-0001 §6.2's 42,416 B composition estimate | **1.95×** |
| vs the 46,080 B G2 gate | **1.79× — over by 36,479 B, i.e. 79.2 %** |

§6 asks for bootstrap 95 % confidence intervals on pooled percentiles. That rule
is written for latency, over thousands of samples; five run-level figures with a
2.2 % spread do not support a bootstrap and one was not manufactured. The five
figures are all published, which is the part of §6 that applies.

### 4.2 Secondary figures (§3.6 requires these alongside, never instead)

Medians across the same five runs, per session.

| Signal | Source | Per session |
|---|---|---:|
| goroutines | `runtime.NumGoroutine` | **exactly 2** (2,007 at N=1000 against 7 at N=0, every run) |
| live heap | `/gc/heap/live:bytes` | **22,715 B** |
| goroutine stacks | `/memory/classes/heap/stacks:bytes` | **26,805 B** |
| total Go-runtime mapped | `/memory/classes/total:bytes` | **79,270 B** |
| kernel charged to the cgroup | `memory.stat kernel` | **1,794 B** (of which `slab` 1,582 B) |
| kernel socket memory | `memory.stat sock` | **0 B**, in every sample of every window |
| **labelled: post-`debug.FreeOSMemory()` floor** | cgroup, same method, taken after the headline window closed | **55,656 B** (54,902 – 55,873) |

The forced-GC floor is a **labelled secondary**, per §3.6, and never the
headline: "the headline stays unforced steady state, because that is what a
deployment sees". It is reported because it answers the obvious question, and
the answer does not help — **even with every collectable byte collected and
returned to the OS, an idle session costs 55,656 B, which is 20.8 % over the
46,080 B gate.** The gate is not missed because of GC scheduling.

### 4.3 The other two cells

**Cell 2 — N = 1000, observability OFF, three runs.** The configuration
RFC §6.2's table implicitly describes, since that table has no observability
line.

| Run | mem_per_session |
|---|---:|
| r01 | 57,761.8 B |
| r02 | 57,135.1 B |
| r03 | 56,463.4 B |
| **pooled** | **57,135.1 B** (spread 2.3 %, stable) |
| labelled forced-GC floor | 43,802 B (43,524 – 44,929) |

**25,424 B/session — 30.8 % of the headline — is the cost of default-on
observability.** §5.1 says where it goes, and it is not where anyone expected.

**Cell 3 — N = 100, observability ON, three runs.** RFC §6.3's sub-linearity
check.

| Run | mem_per_session |
|---|---:|
| r01 | 97,812.5 B |
| r02 | 98,181.1 B |
| r03 | 89,374.7 B |
| **pooled** | **97,812.5 B** (spread **9.0 %** — inside §6's 20 % rule, but the loosest cell here) |
| labelled forced-GC floor | 66,478 B (63,856 – 69,099) |
| goroutines | 207 at N=100 against 7 at N=0 — **exactly 2 per session**, again |

**The sub-linearity check does not pass as written.** RFC §6.3: *"per-session
memory at N = 1000 must be within 15 % of N = 100."* RFC §15's **E1** repeats it
as a falsifier: *"N=1000 differs from N=100 by > 15 %."*

```
(82,559 − 97,812) / 97,812  =  −15.6 %
```

**−15.6 % against a ±15 % bound.** Two things about it, and neither is an
argument for ignoring it:

- **The sign is the opposite of the one §6.3 diagnoses.** §6.3's sentence is
  *"If it **grows**, some structure is O(N) per session and that is a design
  defect."* It did not grow; it **fell**. A per-session figure that falls with N
  is the signature of a term that is *not* per-session being divided by more
  sessions — the ordinary amortization every fixed cost shows — and not of an
  O(N) structure. The goroutine count is exactly 2 per session at both
  concurrencies, which is the structural claim the check exists to protect.
- **The bound is two-sided and the measurement is outside it, so it is reported
  as outside it.** E1's falsifier says "differs by > 15 %", not "grows by
  > 15 %", and reading it as one-sided after seeing a number that fails the
  two-sided version is exactly the move this document refuses to make. It is
  flagged for PM-1 and L9-1 in §6 alongside the headline miss.

---

## 5. Where the overage is — attribution against RFC-0001 §6.2

§6.1.2 requires the overage to be attributed to a **named line** in §6.2 rather
than absorbed into a bigger number. The two cells make that attribution sharper
than one cell could, because they separate the library's cost from its
instrumentation's.

Rows are medians across each cell's runs of a `runtime/metrics` reading taken at
the close of each window, plus `memory.stat` from the cgroup. They are point
readings, not 60-sample medians, so each column sums to within 0.7 % of its
cell's headline rather than to it exactly; the difference is the difference
between one reading and a median of sixty, and it is stated rather than
reconciled away.

| Per idle session | **Headline** (obs on) | Secondary (obs off) | Instrumentation's share | §6.2 estimate |
|---|---:|---:|---:|---:|
| goroutine stacks, both goroutines | **26,804** | **15,794** | **+11,010** | 16,384 (8,192 × 2) |
| heap, live | **22,715** | **22,239** | +476 | 10,516 (heap-resident subtotal) |
| heap, allocated but not live at unforced steady state | **27,313** | **14,485** | +12,828 | 10,516 (the `GOGC=100` doubling) |
| runtime metadata, scheduler, and resident anon not attributed above | **3,801** | **3,272** | +529 | 1,000 |
| kernel, charged to the cgroup (`slab` ≈1,580 of it) | **1,794** | **1,740** | +54 | 4,000 |
| kernel socket memory (`memory.stat sock`) | **0** | **0** | 0 | *inside the 4,000* |
| *sum of the rows* | *82,427* | *57,530* | | 42,416 |
| **pooled headline (§3.6's arithmetic)** | **82,559** | **57,135** | **+25,424** | — |

### 5.1 §6.2's goroutine-stack line is right — for a library nobody ships

**15,794 B measured against a 16,384 B budget: 96.4 %.** With `Config.Logger`,
`Config.Metrics` and `Config.Tracer` nil, the two goroutines per session cost
almost exactly what §6.2 says they cost. That line was not wrong.

**With observability on it costs 26,804 B — +11,010 B, and the shape of the
number says what happened.** Go's goroutine stack starts at 2 KiB and grows by
**doubling**; it does not grow by a little. Two goroutines at 8 KiB each is
16,384 B, which is what the uninstrumented cell measures; one of the two at
16 KiB instead is +8,192 B, and the measured +11,010 B is that plus the stack
allocator's own span accounting. **Adding spans and metric recordings to the
actor and read-pump paths pushed at least one of the two goroutines past a
doubling boundary, and Go shrinks stacks only at GC and only to a quarter
occupancy — so the doubled stack is retained, not transient.** The forced-GC
floor confirms it: the class still measures ≈22.5 KB/session after
`debug.FreeOSMemory`.

This is the finding with the clearest engineering lead in it, and it is nobody's
existing budget: **NFR-1 budgets observability at ≤ 5 % of p50 latency, and
nothing anywhere budgets its memory.** equivalence-spec Appendix B **QA3-3**
guessed at the shape — "plausibly a buffer line inside `M(x)` itself" — and the
measurement says the guess was right and the mechanism is not the one QA3-3
expected: it is not log buffering, it is stack depth. On the Idle workload the
provenance log emits one record per mount and nothing after, so log *volume*
cannot be what this is.

### 5.2 §6.2's heap subtotal is wrong by 2.1×, in both configurations

**22,239 B (obs off) and 22,715 B (obs on) of live heap against a 10,516 B
budget.** The 476 B between the two cells is noise; instrumentation is not the
cause, and neither is the workload. This is the library's per-session heap.

One line is identifiable without further instrumentation, and it is a line §6.2
does not have at all:

> **The transport keeps a write buffer as well as a read buffer, and §6.2
> budgets only the reader.** `websocket.Accept` takes the connection through
> `http.Hijacker`, which hands over net/http's own `*bufio.Reader` **and**
> `*bufio.Writer` — 4,096 B each at the standard library's defaults
> (`net/http/server.go`: `bufio.NewReader`, and `newBufioWriterSize(…, 4<<10)`)
> — and `websocket.Conn` retains both for the connection's life (`conn.br`,
> `conn.bw`, `conn.go:100-101`). §6.2 has "WebSocket read buffer | 4,096" and
> nothing for the writer. **4,096 B per connection is missing from that table by
> construction.**

The remaining ≈7,600 B of live heap is **not attributed here and is not guessed
at**. Cgroup accounting cannot see inside the heap, and separating §6.2's
"WebSocket conn struct" line from its "session struct" line needs the
per-component heap profile RFC §6.3's diagnostic paragraph describes, which this
baseline did not run (§7.5).

### 5.3 The `GOGC` line is right in shape and doubles the wrong base

§6.2 derives GC headroom as one further copy of the heap-resident subtotal, and
that model holds: measured allocated-but-not-live is 14,485 B against 22,239 B
live with observability off, and 27,313 B against 22,715 B with it on. The
model was never the problem. The base it doubles is less than half the real one,
and with instrumentation on there is a second copy of the *garbage* that
instrumentation allocates per mount and the GC has not yet had reason to collect.

### 5.4 The one line that came in under, and the one that read zero

**Kernel socket memory: 4,000 B budgeted, 1,740–1,794 B measured — the only line
that came in favourably.** Inside it, `memory.stat`'s `sock` read **exactly 0**
in every sample of every window of every run of both cells. An idle socket with
nothing queued charges no socket memory to the memory cgroup; the real
per-connection kernel cost lands in `slab` (≈1,580 B/session). RFC §16 **O7**'s
kernel-socket line is closed by measurement, and it closed in gotth-live's
favour.

### 5.5 What the measurement confirmed

Worth as much as the misses, and none of these was previously measured:

- **Exactly two goroutines per session.** §3.4's claim, in all eleven runs of
  all three cells: 2,007 goroutines against 7 at N = 1000, and 207 against 7 at
  N = 100.
- **`GOGC=100` doubles the heap, as §6.2's model says** (§5.3).
- **§6.2's goroutine-stack line, for the uninstrumented library** (§5.1).
- **No per-connection compression context.** ADR §4.3 forbids context-takeover
  deflate on the ground that it alone would cost 1.2 MB per connection; the
  handler sets `CompressionDisabled`, and a 1.2 MB-per-session term is not
  something an 82 KB measurement could be hiding.

### 5.6 Four numbers, and only one of them is under the gate

Because the two cells and the two GC states make four figures, and it would be
easy to quote whichever one suited an argument, all four are here in one table
with the two that §3.6 and §5.6 make the headline marked as such.

| | unforced steady state | after `debug.FreeOSMemory()` |
|---|---:|---:|
| **observability on** (equivalence-spec §5.6's headline) | **82,559 B — the G2 baseline, 1.79× the gate** | 55,656 B — 1.21× |
| observability off | 57,135 B — 1.24× | **43,802 B — 0.95×, the only figure under 46,080** |

> **Post-remediation, as of 2026-08-04/05 (§9).** The table above is left as it
> was measured. Only one of its four cells has been re-measured, and the other
> three are **not** re-stated by inference from it:
>
> | | unforced steady state | after `debug.FreeOSMemory()` |
> |---|---:|---:|
> | **observability on** (the headline) | **64,970 B — 1.41× the gate** (`ce52d2f9`, 3 runs, §9.4) | 45,822 B — 0.99× |
> | observability off |  **not re-measured** — §9.9.4 |  **not re-measured** — §9.9.4 |
>
> **And again at the tree this PR ships (§9.10.5), which is the current row.**
> `ce52d2f9` is no longer the shipping tree and neither of the two rows above is
> the current figure:
>
> | `d66e4953`, 5 runs | unforced steady state | after `debug.FreeOSMemory()` |
> |---|---:|---:|
> | **observability on** (the headline) | **45,769 B — 0.993× the gate**, and *at* it rather than clear of it (§9.10.9) | 35,029 B — 0.76× |
> | observability off (2 runs, §9.10.10) | **42,086 B — 0.913×** | 31,947 B — 0.69× |
>
> **Default-on observability's share is 3,682 B/session — 8.0 % of the headline**,
> against the 25,424 B (30.8 %) §4 measured. §9.9.4's refusal to carry the old
> share forward was right by a factor of seven.
>
> The forced-GC floor is still a **labelled secondary and never the headline**,
> for §3.6's reason: *"the headline stays unforced steady state, because that is
> what a deployment sees."* That it has now crossed under 46,080 B changes
> nothing about which number is quotable, and reading it as the result would be
> the disqualifying method error §4.2 and §5.6 already refuse. It is reported
> because it scopes the remedy, and because hiding it would be the mirror of
> quoting it.

The bottom-right cell is the only one that fits, and **it is not a number this
project is allowed to quote as the baseline.** §3.6: *"The forced-GC floor is a
secondary, labelled number on both sides or on neither… The headline stays
unforced steady state, because that is what a deployment sees."* §5.6:
observability at its default-on setting is the headline, "that is what a user
gets". Quoting 43,802 B would require changing both rules after seeing the
numbers, which is the disqualifying method error this whole document exists to
not commit. It is published because hiding it would be the mirror of quoting it,
and because it is the useful number for scoping the remedy: **it says the gate is
reachable, and it says what has to change to reach it.**

## 6. What this means for the gate (RFC-0001 §6.1.2)

RFC-0001 §6.1.2 fixed the response to this outcome **before any measurement
existed**, which is the only reason the response can be trusted now:

> - If the measured **TLS-outside** total exceeds **46,080 B**, the target does
>   **not** move. The overage is attributed to a named line in §6.2 and either
>   engineered down or escalated to an ADR that moves the target with L9-1's
>   approval and the measurement in hand. **A benchmark-method change is not an
>   available remedy for a missed memory target.**

Applied, clause by clause:

| Clause | Done |
|---|---|
| the target does not move | **The gate is still 46,080 B.** Nothing in this landing, in RFC §6.1, or in equivalence-spec §3.6 changed it |
| attributed to a named line | §5 above: goroutine stacks, live heap (including a 4,096 B write buffer the table does not have), and the `GOGC` headroom that follows from the second |
| engineered down **or** escalated to an ADR | **Neither, here, and deliberately.** Both are decisions above DEV-1: engineering the named lines down is a design change to §3.4's goroutine shape or to the transport's buffering, and an ADR that moves the target needs L9-1's approval. What this landing supplies is what §6.1.2 makes a precondition for either — a measurement that exists, is reproducible, and has names |
| no benchmark-method change | **None was made and none was sought.** §3.6 was not amended (it is frozen under §12), not extended, and not reinterpreted. The one place where a reading was genuinely ambiguous — the driver validation gate — is filed to QA-2 unresolved rather than resolved in our favour (§7.1) |
| the ratchet (under 36,864 B ⇒ tighten) | Not reached. Not close |

**Flagged for PM-1 and L9-1 — both halves of E1, not one.** RFC §15's exit
criterion **E1** has two falsifiers and this baseline trips both:

| E1 falsifier | Measured | |
|---|---|---|
| `> 46,080 B` | **82,559 B** (57,135 B with observability off) | tripped |
| `N=1000 differs from N=100 by > 15 %` | **−15.6 %** | tripped, marginally, in the amortization direction (§4.3) |

E1 is a Phase 5 criterion and this is a Phase 3 baseline, so nothing is failed
*yet* — but it will be, on this design, and the ADR §6.1.2 points at is now due
work rather than hypothetical.

**What the four figures of §5.6 say about scoping it**, offered as input to that
decision and not as a proposal:

- The gap is **not** GC scheduling. With observability on, forcing a full GC and
  returning everything to the OS still leaves 55,656 B — 21 % over.
- The gap is **not** the library's goroutine count or its socket cost. Both were
  measured and both came in at or under budget (§5.1, §5.4).
- **≈25 KB of the ≈36 KB overage is the cost of default-on observability**, and
  ≈11 KB of that is a permanently doubled goroutine stack rather than anything
  buffered (§5.1). That is a number nobody has a budget for: NFR-1 budgets
  observability's *latency* at ≤ 5 % and nothing budgets its memory.
- The remaining ≈11 KB is the library's own heap against §6.2's subtotal, of
  which 4,096 B is a `bufio.Writer` the table never had a line for (§5.2).

Whether the answer is an ADR that moves the target, a memory budget for
observability that §5.6's headline rule then has to reckon with, or engineering
on the two named lines, is PM-1's and L9-1's call. It is not DEV-1's, and this
document does not make it.

---

## 7. Not measured, and why

FR-73's rule — "a dimension which cannot be measured fairly is reported as *not
measured, and why* rather than estimated" — is applied to ourselves here,
because that is the only thing that makes it mean anything when it is applied to
Next.js.

### 7.1 §3.6's driver validation gate — NOT run, and the 1k figure is labelled accordingly

§3.6 makes this mandatory before any 1k number is quoted:

> **Driver validation gate (mandatory before any 1k number is quoted):** measure
> per-session memory with **10 real Chromium tabs** and with **10 synthetic
> sessions**, on both stacks. If the per-session figures differ by more than 10 %
> on either stack, the driver misrepresents a browser and MUST be fixed before
> the 1k run.

**It was not run.** The consequence is stated in §3.6's own words and is accepted
rather than argued around: without it, *"the 1k number is an assertion about a
synthetic client, not about sessions"*. This baseline is published as exactly
that, and it is **not a Phase 5 quotable number** until QA-2 runs the gate.

Two things are worth putting on the record for whoever does run it.

**First, what the driver was checked against instead.** The shipped client
runtime's behaviour on an idle session is three things, and the driver was
written against the source, not against a description of it:

| `client/runtime.js` | `test/memory/cmd/memdrv` |
|---|---|
| acknowledges every applied frame, cumulative high-water mark (`send({ ack: { server_seq: seq } })`, line 642) | acknowledges every `Snapshot` and `Patch` on read |
| echoes the heartbeat with `nonce` and `interval_ms` **verbatim** (line 694) | same |
| sends **no** `ClientTelemetry` frame — the shipped runtime has no such call site | same |

For the Idle workload that is the whole of what a browser puts on the wire, and
the server cannot distinguish the two by any means other than TCP read timing.
This is **evidence, not the gate**: the gate compares measured memory, this
compares behaviour, and QA-2's gate exists because behaviour arguments of exactly
this shape are what it was written to stop being sufficient.

**Second, a resolution problem in the gate as written, filed rather than
interpreted.** §3.6's reading rule says a definition needing interpretation is a
defect to file, so: the gate compares *per-session* figures at N = 10, and this
method's per-session figure is not scale-free. Measured with the identical
driver, the identical method and the identical host:

| N | mem_per_session | vs N = 1000 |
|---:|---:|---:|
| 100 | 97,812 B | **+15.6 %** |
| 1000 | 82,559 B | — |

Going from N = 1000 to N = 100 moves the figure by 15.6 %, because a
per-session figure carries a share of everything that is not per-session, and
that share is ten times larger at N = 100 and a hundred times larger at N = 10.
Both sides of the gate's comparison — 10 Chromium tabs and 10 synthetic sessions
— would carry the same inflated share, so a ±10 % criterion at N = 10 compares
two numbers largely made of the same thing and will pass more easily than it
looks. **This is filed for QA-2, not acted on. equivalence-spec §3.6 is frozen,
was not amended, and no measurement here was taken under a different reading of
it.**

### 7.2 The in-process-TLS secondary — not measured

§3.6 requires it to be *measured*, by re-running the same procedure with TLS
terminated inside the measured container, and forbids deriving it from a
composition budget (L9-1 condition C-3). This box asked for the TLS-**outside**
baseline, which is the gate's quantity; the in-process figure is a labelled
secondary with no target attached (RFC §6.1). `memsrv` has no TLS listener by
construction, so producing it means a second binary and a second cell.

**Not measured, not derived, and not estimated.** RFC §16 O7's 18,000 B figure
remains what it was: prose explaining why §6.1 gates on TLS-outside.

### 7.3 The reverse proxy — not run

Explained in §2.4. Excluded from `M(x)` by §3.6's own definition, so its absence
cannot move `M(x)`; the symmetry it exists for binds the Next.js side at Phase 5
and is QA-2's, including §3.6's requirement that the proxy image digest recorded
for one stack equal the one recorded for the other. The manifest records
`proxy: "none"` rather than leaving the field absent.

### 7.4 Workloads other than Idle — not measured

§3.4 defines three workloads and §3.6 reports a 2 × 3 grid per stack at Phase 5.
RFC §6.3 specifies **Idle** for the gate, and Idle is what was run. The other two
are Phase 5's and QA-2's.

### 7.5 Per-component heap attribution — not measured *(DISCHARGED 2026-08-04, `ce52d2f9`)*

> **Discharged.** RFC §6.3's per-component heap profile was run in `ce52d2f9`,
> which added `/heapprofile` to `memsrv` and `--cells`/`--memprofilerate` to
> `diag.sh`, and it attributed the live heap line by line at N = 1000,
> observability off, `MemProfileRate=8192`:
>
> | Line | B/session | §6.2's budget |
> |---|---:|---|
> | `bufio.Writer` (hijacked from net/http) | 4,120 | **§6.2 has no line** |
> | `bufio.Reader` (hijacked from net/http) | 3,940 | 4,096 |
> | `websocket.Accept`/`newConn` | 2,370 | 2,000 (conn struct) |
> | `session.New` (actor, mailbox, acks, window, renderer, hashes) | 3,010 | 3,920 — **under budget** |
> | net/http retained request state | 2,280 | **§6.2 has no line** |
> | `context.WithCancel` × 2 | ≈1,200 | **§6.2 has no line** |
> | `runtime.malg` (two goroutine descriptors) | 820 | 1,000 |
> | *profiled total* | *20,240* | *heap subtotal 10,516* |
>
> **The library's own per-session structures are UNDER their combined budget.**
> The overage is the transport's and net/http's, and two of the three largest
> lines do not exist in §6.2 at all. RFC §16 O7's conn-struct line is closed by
> this: `websocket.Accept`/`newConn` measures 2,370 B against the 2,000 B
> estimate. §9.6 is what was done about the two lines that had no budget.
>
> The paragraph below is the state before that run, kept because §7's whole point
> is that "not measured, and why" is a claim with a date on it.



RFC §6.3's "additional gotth-live-only diagnostic" — a per-component heap
profile plus a no-op-session harness that opens connections without registering
an application — was **not** run. It is what would separate §6.2's "WebSocket
conn struct" line from its "session struct" line, and without it the 22,239 B of
measured live heap per session (observability off, the library's own) is
attributed to one named line — the 4,096 B `bufio.Writer` the table does not
have — and no further.
RFC §16 O7's conn-struct line is therefore **not** closed by this landing, and
§6.2.3 says so.

### 7.6 Run counts below §6's five, on two of the three cells

§6 asks for five independent runs per cell. The headline cell has five. The
observability-off and N = 100 cells have three each, because each run is
~12 minutes of exclusive use of a shared host and the headline was the run to
spend it on. Both are reported with their run counts beside them and neither is
presented as five.

### 7.7 The Next.js side — not measured, and not this box

Phase 5, QA-2, the whole equivalence spec. Nothing here is a comparison.

### 7.8 Client memory — not measured

Closed by §7 of the equivalence spec: Chromium tab RSS attribution across
processes is unreliable enough on a shared host that the number would mislead.

---

## 8. Reproducing this

Everything is committed. On a host with docker, curl, jq, awk and cgroup v2:

```bash
cd <checkout>/gotth-live

# one cell: N sessions, R independent runs, ~12 minutes per run
bash test/memory/measure.sh --n 1000 --runs 5 --observability on \
     --out /tmp/g2/n1000-obs-on

# the figure, computed in the project image — no Go on the host
docker run --rm -v "$PWD:/w" -v /tmp/g2/n1000-obs-on:/cell \
    -w /w/test/memory dis-gotth-live:latest \
    bash -c 'go run ./cmd/memstat -cell /cell'
```

`measure.sh --help` lists the cpusets, the memory limit, the settle time and the
warm-up count, all of which are recorded in each `run.json` whether or not they
are changed.

The harness's own arithmetic has specs and runs in `ci.sh`
(`test/memory (its own module, G2 baseline harness)`). The measurement does not
run in CI and cannot: it needs two containers, a pinned cpuset and twenty-two
minutes per cell, and its numbers are host-dependent by construction. What CI
protects is that the harness still compiles and still refuses a window that is
not §3.6's window — because a baseline nobody can re-take is a baseline that
will be stale and unfixable at Phase 5.

---

## 9. Re-measurement — 2026-08-04/05

| Field | Value |
|---|---|
| Owner | DEV-1 |
| Status | **Measured. Still over the gate at the tree it measured.** Superseded as the current figure by **§9.10**, which measures `d66e4953`; this section's `cur` and `old` arms remain the record of what those trees measured. The gate has not been moved and the method has not been changed |
| Method | [equivalence-spec §3.6](equivalence-spec.md), unmodified. `test/memory/measure.sh` is **byte-identical across every measured tree** and was not edited for this campaign |
| Re-measured | campaign began 2026-08-04 21:49 UTC |
| Raw data | [`data/g2-baseline/remeasure-2026-08-05/`](data/g2-baseline/remeasure-2026-08-05/) |
| Applies | RFC-0001 §6.1.2, clause by clause, to the figure measured here rather than to §4's |

> **Measured: 64,970.8 B per idle session at the as-landed tree** (`ce52d2f9`),
> N = 1000, Idle, observability on, TLS outside, three runs.
>
> §4 measured **82,559 B** for the code as it stood before `9f88d75e`. Paired
> against that same older tree **in this campaign and on this host**, the older
> tree reads **85,680 B** and the as-landed tree reads **64,970 B** — a real
> reduction of **≈24 %**, and the first genuine movement this dimension has had.
>
> **It is not enough.** The G2 gate is **46,080 B**. 64,970 B is **1.41×** it —
> over by **18,891 B**. §9.7 applies §6.1.2 to that.

**§4's numbers, §5's attribution and §6's verdict are left exactly as they
were.** They were true of the code they measured. This section says what changed
in the code, what was re-run, what moved and what did not.

### 9.1 What changed in the code since §4 measured it

| Commit | What it did | Claimed a §3.6 number? |
|---|---|---|
| `9f88d75e` | Pre-resolved the metric label sets `docs/instrumentation.md` §4.2 already required; hex-encoded the session id once per session; `obs.Logger` moved to `[]slog.Attr` + `LogAttrs` | **No.** It reported Go-runtime *mapped* bytes per session (85,774 → 63,877) and per-session goroutine stack (30,802 → 15,892), and said in as many words that it was not the §3.6 measurement |
| `ce52d2f9` | The per-component heap profile RFC §6.3 asks for and §7.5 recorded as owed. Diagnostic only; no library code | No, and none was owed |
| `5a2ca417` | `ServeHTTP` returns at the upgrade, and the transport is handed buffers this library sized rather than net/http's (§9.6) | **No, deliberately.** It said the §3.6 number would land here |

`fb0b21c5` and `985b5f61` follow. The first is a comment in
`internal/wsx/doc.go`. The second is **D-30** — a relational check in
`live.Limits.validate` refusing a `HeartbeatTimeout` below two
`HeartbeatInterval`s — and it is **not in any measured binary here**, which
cannot move a figure in this section: `memsrv` configures
`live.DefaultLimits()` (20 s against 50 s), so the new check runs once at
`live.New`, returns nil, and allocates nothing per session. A fixed per-process
cost is cancelled by `(M(N) − M(0))/N` by construction, which is why that
arithmetic subtracts `M(0)`.

### 9.2 The design of the campaign, and where it departs from §2.2

**Arms measured in one session against one host**, because the drift §2.2's
alternation guards against is far larger across a campaign than within a run,
and this host is shared with three other agents, a CI gate run, and twenty
unrelated containers:

| Arm | Tree | What it is |
|---|---|---|
| **old** | `70abe339` | the commit immediately before `9f88d75e` — the code §4 measured, modulo the two test-only files §2.7 already accounts for |
| **cur** | `ce52d2f9` | as-landed before this turn's engineering |
| **eng** | `5a2ca417` | as-landed after it — §9.6 |

Each tree is a `git archive` export outside the worktree, so neither another
agent's commits nor this turn's edits could move a tree mid-campaign. **Two
manifest fields read oddly as a result and are not defects:** `git_sha` reads
`unknown` and `git_dirty` reads `true`, because `measure.sh` asks git about a
directory that is not a repository. The shas are in the table above and in
`campaign.log`.

**The independent check that this pairing measures the code and not the host.**
The `old` arm is the same code §4 measured on the previous day, and it was
re-measured here without being told what answer to give: **81,948.7 B on its
second run, against §4's pooled 82,559 B — 0.7 % apart, a day later, on a host
whose load average moved by a factor of four in between.** That is worth more
than a third run of it. It is what licenses reading the `old`-to-`cur`
difference as a property of the code.

**Runs alternate arms ABBA rather than AB AB**, so drift linear in time cancels
in the A-vs-B difference instead of loading one arm. The cost is stated rather
than hidden: `measure.sh` derives its within-run window order from the run index
inside ONE invocation, and interleaving arms run-by-run means one invocation per
run, so **every run in this campaign is `m0-then-mn`** where §2.2 alternated.
That is a real departure, and the reason it is tolerable is measurable in §4.1's
own data: the three `m0`-first runs there averaged 82,347 B and the two
`mn`-first runs 81,979 B — **0.45 % apart**, inside that cell's own 2.2 %
spread. The order is also identical in every arm, so whatever it is worth it is
common-mode in exactly the comparison the pairing exists to protect.

**The measured binary differs between `old` and the other arms by one HTTP route
and one flag.** `ce52d2f9` added `/heapprofile` and `-memprofilerate` to
`memsrv`; the flag defaults to `runtime.MemProfileRate` and assigning it back is
a no-op, and a mux entry is a fixed cost paid once per process — cancelled by
the same subtraction as D-30's check.

### 9.3 The host, and contention

The same shared 32-core, 38 GB VM as §3, the same stated rule for the contended
flag, and — as in §4 — **every run in this campaign is flagged contended**.

| | During this campaign |
|---|---|
| one-minute load average at the start of a measured `M(N)` window | 2.57 – 17.10 |
| unrelated containers running | 20–21, none touched, none stopped, none restarted |
| this project's own load | three other agents building and running suites, plus a full `ci.sh` gate run in flight against a clean export |

**The GPU streaming container (`gpu-desktop-steam-1`) was checked rather than
assumed**, because it is a shared GPU desktop and an active session on it would
be a different contention story from an idle one. It has been up five days. **There is no interactive session on
it**: its log's most recent events are the controller peer disconnecting, the
media pipeline stopping, and the signalling client re-registering and waiting;
and its network counters were *identical* — 243 MB in, 449 MB out — in samples
taken 25 minutes apart, which is evidence independent of the log. **It burns
≈375–381 % CPU anyway**, `Xorg` at ≈242 % and `steamwebhelper` at ≈93 %,
measured twice with `docker stats --no-stream` during the campaign.

So the honest label is neither "a user was on the box" nor "the box was quiet":
**≈3.8 cores of unpinned load that is not user activity and does not stop**,
plus this project's own agents. Nothing was stopped, restarted or touched.

**The spreads are looser than §4's and that is reported rather than smoothed.**
§4's headline cell spread 2.2 % over five runs; the cells here spread 8.7 % and
10.0 % over two and three. Both are inside §6's 20 % instability rule and both
cells are therefore **stable** by that rule, but they are three to five times
looser, and the campaign ran against more contention than §4's did. The pooled
figure is a median of per-run figures for exactly this reason.

### 9.4 Results — N = 1000, Idle, observability ON, TLS outside

The headline configuration: equivalence-spec §5.6's default-on observability,
"that is what a user gets". Every window is 60 samples at 1 Hz and none was
rejected; every `M(N)` window held exactly 1000 live sessions with 0 dial
errors, 0 read errors and 0 closes.

**Arm `old` — `70abe339`, the code §4 measured.**

| Run | Order | M(0) | M(N) | mem_per_session |
|---|---|---:|---:|---:|
| step02 | M(0) first | 3,624,960 | 93,036,544 | **89,411.6 B** |
| step03 | M(0) first | 3,641,344 | 85,590,016 | **81,948.7 B** |
| **pooled (2 runs)** | | | | **85,680.1 B** — spread 8.7 %, stable |
| *labelled forced-GC floor* | | | | *56,365.1 B* |

**Arm `cur` — `ce52d2f9`, as landed before this turn's engineering.**

| Run | Order | M(0) | M(N) | mem_per_session |
|---|---|---:|---:|---:|
| step01 | M(0) first | 3,817,472 | 65,830,912 | **62,013.4 B** |
| step04 | M(0) first | 3,792,896 | 68,763,648 | **64,970.8 B** |
| step05 | M(0) first | 3,670,016 | 72,179,712 | **68,509.7 B** |
| **pooled (3 runs)** | | | | **64,970.8 B** — spread 10.0 %, stable |
| *labelled forced-GC floor* | | | | *45,822.0 B* |

| | |
|---|---:|
| **`old` → `cur`, paired in this session** | **−20,709 B, −24.2 %** |
| vs the 46,080 B G2 gate | **1.41× — over by 18,891 B, i.e. 41.0 %** |
| vs RFC-0001 §6.2's 42,416 B composition estimate | 1.53× |

§6 asks for bootstrap 95 % confidence intervals on pooled percentiles. That rule
is written for latency over thousands of samples; two and three run-level
figures do not support a bootstrap and one was not manufactured. Every run is
published, which is the part of §6 that applies.

### 9.5 The §3.6 secondaries, and where the −20,709 B came from

Medians of the point readings taken at each window's close, per session.

| Signal | `old` (`70abe339`) | `cur` (`ce52d2f9`) | moved |
|---|---:|---:|---:|
| goroutines | **exactly 2** | **exactly 2** | — |
| goroutine stacks (`/memory/classes/heap/stacks:bytes`) | 25,215 | 12,812 | **−12,403** |
| live heap (`/gc/heap/live:bytes`) | 22,559 | 22,653 | +94 |
| total Go-runtime mapped (`/memory/classes/total:bytes`) | 81,371 | 62,362 | −19,009 |

Two things this says, and the second is the whole reason §9.6 exists.

- **`9f88d75e`'s claim is confirmed at the §3.6 level.** It reported the
  goroutine-stack class falling 30,802 → 15,892 from its own diagnostic and
  declined to claim the headline. The headline agrees: 25,215 → 12,812 on the
  same class, and the total figure moved by 20,709 B against a stack movement of
  12,403 B plus the GC headroom that follows it. §5.1's finding — that ≈11 KB of
  the overage was a permanently doubled goroutine stack, not anything buffered —
  was correct, and removing the per-record label allocation from the read pump's
  path is what un-doubled it.
- **The live heap did not move at all.** 22,559 → 22,653 B is noise. §5.2's
  22,239 B against a 10,516 B budget is untouched by everything that has landed
  so far, and `ce52d2f9`'s per-component profile (§7.5, now discharged) says
  what is in it. That is the line §9.6 attacks.

### 9.6 What `5a2ca417` changed, and the arm measuring it

`ce52d2f9`'s profile named the three largest lines of that live heap, and two of
them belong to net/http rather than to this library: `bufio.Writer` 4,120 B,
`bufio.Reader` 3,940 B — both hijacked — and 2,280 B of retained request state.

net/http holds a `*conn` for as long as its handler has not returned, and that
`*conn` holds a 4,096 B `bufio.Reader`, a 4,096 B `bufio.Writer`, the
`*response` with its own 2,048 B `bufio.Writer` and header map, and the
`*Request`. For an ordinary request that is scratch which lives a millisecond
and returns to net/http's pools. For a WebSocket held open by a **blocking**
`ServeHTTP` it is per-session memory retained for hours, and it never returns to
those pools, because `finishRequest` is what returns them and hijacking skips
it. So `ServeHTTP` now returns at the upgrade and the session runs on a
goroutine the handler owns — which then makes a second change possible that was
counter-productive on its own: a `ResponseWriter` wrapper whose `Hijack` hands
the transport a 512 B reader and a 1,024 B writer now **frees** net/http's
4,096 B pair instead of adding to it. The goroutine count per session is
unchanged at two.

The write buffer is a trade and it is priced rather than asserted:
`BenchmarkFrameWrite` in `internal/wsx` measures **+1,800 ns at a 2,048 B
payload and +2,493 ns at 4,000 B**, and nothing outside the (1,014, 4,086] band,
against PRD G1's 50 ms p50 budget.

**The `eng` arm measuring it was still collecting when this section landed.**
Its cell and its figures are added in the follow-up commit to this document
rather than estimated here, which is the same rule `5a2ca417` itself applied
when it declined to claim a byte count without a §3.6 number.

> **The follow-up commit did not happen, and the arm is in §9.10.** The `eng`
> arm finished collecting at `2026-08-04T23:36:52+00:00` — four complete runs —
> and the turn ended between collecting and committing. The data sat unpublished
> until it was recovered, verified and landed in **§9.10.3**, which is where its
> cell and its figures now are. §9.10 also carries the arm this section could not
> know it would need: `5a2ca417` is no longer the tree this PR ships.

### 9.6.1 Two conditions the engineered figure depends on, and which one the measurement met

L9-1's review of `5a2ca417` found that the buffer half of the saving is not
unconditional, and a per-session number whose validity depends on something the
document does not state is a benchmark artifact rather than a property. Both
conditions are stated here, and each is answered for the arm that was measured.

**1. The ResponseWriter shape (C-36).** The saving requires the transport's
`http.Hijacker` call to reach this library's wrapper. As `5a2ca417` shipped it,
the wrapper declined unless the ResponseWriter implemented `http.Hijacker`
*directly* — so behind Go 1.20+'s documented `Unwrap`-only middleware shape it
handed the original back and the session kept net/http's 4,096 + 4,096 pair.
L9-1 measured that at **6,656 B/session, lost silently**.

**What the measured arm got, and it is not luck: `memsrv` mounts the handler on
a plain `http.ServeMux`, so the ResponseWriter is net/http's own `*response`,
which implements `http.Hijacker` directly.** The declining branch was never
taken in any run in this document. `0929bf5a` then made the wrapper walk
`Unwrap`, so the saving now applies behind ordinary middleware too — which
widens where the figure holds and does not move what was measured.

**2. The client (C-37).** A peer that pipelines more than `readBufferBytes`
(512) behind its upgrade request trips the wrapper's correctness fallback and
gets net/http's 4,096 B pair back — measured by L9-1 at 513 bytes. That is the
fallback working as designed, and the ceiling it restores is the OLD behaviour,
so nothing grows past where it already was. But it means roughly 6.6 KB/session
of the saving is a property of the *client's* behaviour, and neither the metric
set nor this report could say afterwards whether it had been given up.

**The measured client cannot pipeline, and that is checked rather than
assumed.** `test/memory/cmd/memdrv` dials with `websocket.Dial` — which writes
the upgrade request and reads the 101 response before returning — and then
enters a read loop that writes nothing until it has read and parsed a frame
(`cmd/memdrv/main.go`: the `for { conn.Read(ctx) … }` loop, with every `send`
downstream of a read). There is no code path on which it can put a frame in the
same segment as its upgrade.

**So the figures in §9.4 and §9.6 are the benign-client figures**, and the
adversarial figure is stated rather than left to be discovered: a population of
clients that all pipelined behind the upgrade would return ≈6,656 B/session,
putting the engineered arm back at roughly the pre-`5a2ca417` live heap. QA-2
owns whether the Phase-5 comparison workload's client pipelines and whether the
report carries both numbers; a `Buffered()` histogram at hijack time is the
cheap way to answer it and is **not run here**.

### 9.7 RFC-0001 §6.1.2, applied to the figure in hand

> - If the measured **TLS-outside** total exceeds **46,080 B**, the target does
>   **not** move. The overage is attributed to a named line in §6.2 and either
>   engineered down or escalated to an ADR that moves the target with L9-1's
>   approval and the measurement in hand. **A benchmark-method change is not an
>   available remedy for a missed memory target.**

| Clause | As of 2026-08-04/05 |
|---|---|
| the target does not move | **The gate is still 46,080 B.** Nothing in this landing, in RFC §6.1, or in equivalence-spec §3.6 changed it, and nothing in RFC §6.1.2's number was touched |
| attributed to a named line | **Sharper than §5 could be.** §9.5 attributes the −20,709 B that moved to the goroutine-stack class; `ce52d2f9`'s per-component profile (§7.5) names the live heap that did **not** move, line by line, and two of its three largest lines are net/http's rather than this library's |
| engineered down **or** escalated | **Both, this time, where §6 could do neither.** Engineered: `9f88d75e` (−24.2 %, measured here) and `5a2ca417` (§9.6, measuring). Escalated: [`docs/adr/002-...`](../adr/) carries the residual, the options and a recommendation, and is **PROPOSED — it requires L9-1 approval and DEV-1 does not approve it** |
| no benchmark-method change | **None was made and none was sought.** §3.6 was not amended (frozen under §12), not extended, not reinterpreted. `measure.sh` is byte-identical across every arm, which is checkable rather than asserted |
| the ratchet (under 36,864 B ⇒ tighten) | Not reached |

**E1, both falsifiers, restated honestly.**

| RFC §15 E1 falsifier | §4 | Here |
|---|---|---|
| `> 46,080 B` | 82,559 B — tripped | **64,970 B — still tripped** at the as-landed tree |
| `N=1000 differs from N=100 by > 15 %` | −15.6 % — tripped | **NOT RE-MEASURED** — §9.9.2 |

### 9.9 Not measured in this re-measurement, and why

§7 applies unchanged except where this section says otherwise. Three items need
restating because a reader could otherwise take this section to have moved them.

#### 9.9.1 §3.6's driver validation gate — STILL not run, and this figure is still not Phase-5 quotable

§7.1's label is intact and is not softened by anything here. The gate — measure
per-session memory with **10 real Chromium tabs** and with **10 synthetic
sessions**, on both stacks, and fix the driver if they differ by more than 10 % —
**has not been run**, by §4's campaign or by this one. It is QA-2's and a later
turn's.

The consequence is §3.6's own and is accepted rather than argued around: without
it, *"the 1k number is an assertion about a synthetic client, not about
sessions"*. **Every figure in this section is that kind of assertion.** A number
below 46,080 B here is not G2 met, is not a Phase-5 quotable figure, and does not
become one by being smaller than the one above it. G2 is enforced at Phase 5, at
1k idle sessions, by QA-2, against the comparison stack, after the driver gate
has run. Nothing here ticks it.

The resolution problem §7.1 filed for QA-2 — that a per-session figure is not
scale-free, so a ±10 % criterion at N = 10 compares two numbers largely made of
the same non-per-session share — is also unchanged and still filed rather than
acted on.

#### 9.9.2 The sub-linearity check and E1's second falsifier — NOT re-measured

§4.3 measured N = 100 at 97,812 B against N = 1000 at 82,559 B, i.e. **−15.6 %**
against RFC §6.3's ±15 % bound, and §6 reported E1's second falsifier as tripped.
**This campaign did not re-run the N = 100 cell.** The task it discharges is the
§3.6 headline at N = 1000 in both observability configurations, and each cell is
~12 minutes per run of exclusive use of a shared host.

So: **the status of E1's second falsifier at this code is unknown**, and it is
not inferable from the numbers here. It is not "still tripped" and it is not
"fixed". Both changes since §4 alter the per-session term and neither obviously
alters the fixed term that the check is sensitive to, which is an argument and
not a measurement, and it is written down as an argument. Re-running N = 100 is
the next thing this dimension owes.

#### 9.9.3 Everything else in §7, unchanged

The in-process-TLS secondary (§7.2), the reverse proxy (§7.3), the two workloads
other than Idle (§7.4), the Next.js side (§7.7) and client memory (§7.8) are all
exactly as §7 leaves them: not measured, for the reasons stated there, and not
estimated here either.

#### 9.9.4 The observability-OFF cell — NOT re-measured in this pass

§4.3's secondary cell measured 57,135 B with `Config.Logger`, `Config.Metrics`
and `Config.Tracer` nil, over three runs, and §5.6's table quotes it. **It was
not re-run here.** The pass was scoped to the headline configuration — the one
equivalence-spec §5.6 makes the number the gate is evaluated against — and each
run is ~12 minutes of exclusive use of a shared host.

So §5.6's post-remediation table has one measured cell and three that say "not
re-measured", and **the observability-off figure must not be inferred by
subtracting §4's 25,424 B instrumentation share from the new headline.** That
share is precisely what `9f88d75e` changed: §9.5 measures the goroutine-stack
class, which was ≈11 KB of it, falling by 12,403 B. Carrying the old share
forward would be arithmetic on a number this campaign has already shown to have
moved.

#### 9.9.5 Run counts, below §6's five again

§6 asks for five independent runs per cell. This campaign's counts are stated
beside every figure and none is presented as five, exactly as §7.6 does for §4's
cells. The reason is the same one and it is the same host: each run is ~12
minutes of a shared machine, and three arms at two observability settings is
already ~2.9 hours of it.

---

## 9.10 Re-measurement — 2026-08-05: the tree this PR ships

| Field | Value |
|---|---|
| Owner | DEV-1 |
| Status | **Measured. At the gate, not clear of it** — the pooled figure is under 46,080 B by 0.68 %, which is smaller than the cell's own spread (§9.10.9). Three cells, twelve runs. The gate has not been moved and the method has not been changed |
| Method | [equivalence-spec §3.6](equivalence-spec.md), unmodified. `test/memory/measure.sh` is **byte-identical across every measured tree in this document** — one sha256, `6f1155ed…4182c`, checked rather than asserted — and was not edited for this campaign |
| Raw data | [`data/g2-baseline/remeasure-2026-08-05/`](data/g2-baseline/remeasure-2026-08-05/) |
| Applies | RFC-0001 §6.1.2, clause by clause, to the figure measured here rather than to §4's or §9.4's |

> **Measured: 45,768.7 B per idle session at the tree this PR ships**
> (`d66e4953`), N = 1000, Idle, observability on, TLS outside, **five runs**.
>
> The G2 gate is **46,080 B**. The measurement is **0.993×** it — under by
> **311 B**, or 0.68 %.
>
> §4 measured **82,559 B** and §9.4 measured **64,970 B**. Paired inside this
> campaign against `ce52d2f9`, that reference arm reads **69,673 B** and the
> shipping tree reads **45,769 B** — **−34.3 %**.
>
> **This is not G2 met, and §9.10.9 is the reason it is not reported as met.**
> The margin is 311 B; the same cell's five runs spread 2,523 B, **two of them
> are individually over the gate**, and the *unchanged* `ce52d2f9` tree moved
> 4,702 B between campaigns on this host. The measurement resolves that the tree
> is **at** the gate, where it was once 79 % above it. It does not resolve which
> side of it the tree is on.
>
> **The §6.1.2 ratchet was checked and does not trigger**: 45,768.7 B is
> 8,905 B above the 36,864 B threshold.
>
> **Observability-off, owed since §9.9.4, is measured too:** 42,086 B over two
> runs, so **default-on observability now costs 3,682 B/session — 8.0 % of the
> headline**, against the 25,424 B (30.8 %) §4 measured (§9.10.10).
>
> And §3.6's 10-real-tab driver-validation gate **has still not been run**, by
> any of the four campaigns, so in §3.6's own words every figure here remains
> "an assertion about a synthetic client, not about sessions".

**Three things land in this section, and the first two are corrections to §9
rather than new code.**

1. **§9.4's `old` arm was pooled over two runs and three exist.** `c1`'s step 06
   completed and was never copied out of the campaign's scratch area. It is now
   published, and §9.10.2 states what it moves.
2. **§9.6's promised `eng` arm exists.** It was collected on 2026-08-04, the turn
   that collected it ended before committing it, and §9.10.3 publishes it with
   the provenance checks that recovered data has to earn.
3. **HEAD is not `5a2ca417`.** Fifty-four commits separate them. §9.10.4 onward
   measures the tree this PR actually ships, against `ce52d2f9` in one session,
   because that is the only arm that can answer G2 for what is being shipped.

**No analysis was run on this host during a measured window.** Every `memstat`
and every container-based recomputation in this section was deferred until the
campaign's windows had closed, because the arithmetic runs in a container and
this document's own contention rule would otherwise have to count it.

### 9.10.1 A note on one §3.6 secondary, recorded because it is not obvious

`/gc/heap/live:bytes` reads **exactly 0** in the `M(0)` introspection of every
run in this document, in every campaign, because `gc_cycles_total` is also 0
there: Go publishes that metric at the end of a GC cycle, and an idle server that
has served 200 warm-up page loads has not completed one. So the "live heap per
session" secondary is `heap_live(N)/N` and not a difference of two readings, and
it therefore carries the process's fixed live heap divided by N rather than
subtracting it.

At N = 1000 that is a small inflation and it is the same one in every arm and
every campaign, so it cancels in every comparison this document draws. It is
written down because a reader checking the arithmetic would find a subtraction
that subtracts nothing and be entitled to wonder. **The headline is unaffected:**
`mem_per_session` is the cgroup figure, whose `M(0)` term is 3.6–3.9 MB and is
subtracted in full.

### 9.10.2 `c1`'s third `old` run, and what it corrects in §9.4 and §9.5

Campaign `c1` ran six slots and completed all six. Its step 06 — the third `old`
run — was written at `2026-08-04T22:53:56+00:00` and left in the scratch area
when the publishing turn ended; the `campaign.log` committed alongside it was a
copy taken while that run was still in its `mn` window, which is why it appeared
to stop mid-campaign. The run is now published, along with the complete log. The
raw-data README carries the full account and quotes both false claims it
replaces, including this turn's own first attempt at the correction.

**§9.4 and §9.5 are left exactly as they were written**, because they were true
of the runs they had. What follows is what they would say with the third run
included.

| Arm `old` — `70abe339` | Order | M(0) | M(N) | mem_per_session |
|---|---|---:|---:|---:|
| step02 | M(0) first | 3,624,960 | 93,036,544 | 89,411.6 B |
| step03 | M(0) first | 3,641,344 | 85,590,016 | 81,948.7 B |
| **step06 — the recovered run** | M(0) first | 3,850,240 | 85,954,560 | **82,104.3 B** |
| **pooled (3 runs)** | | | | **82,104.3 B** — spread 9.1 %, stable |
| *labelled forced-GC floor* | | | | *55,418.9 B* |

| | §9.4/§9.5, 2 runs | With step06, 3 runs |
|---|---:|---:|
| `old` pooled | 85,680.1 B | **82,104.3 B** |
| `old` forced-GC floor | 56,365.1 B | 55,418.9 B |
| `old` goroutine stacks | 25,215 B | 26,575 B |
| `old` live heap | 22,559 B | 22,621 B |
| `old` total Go-runtime mapped | 81,371 B | 79,532 B |
| **`old` → `cur`, paired in campaign `c1`** | **−20,709 B, −24.2 %** | **−17,133 B, −20.9 %** |

**The correction runs against the reduction `9f88d75e` was credited with, and it
is reported that way rather than buried.** §9.4's headline for that landing was
−24.2 %; with the run that existed all along, it is **−20.9 %**. The reduction is
still large and still real. It is 3.3 points smaller than this document has been
claiming.

**§9.2's independent check gets stronger, not weaker.** That paragraph's argument
for reading the `old`→`cur` difference as a property of the code rests on the
`old` arm reproducing §4's figure a day later on a drifting host. §4 pooled
**82,559.0 B** over five runs. The three-run `old` arm pools **82,104.3 B** —
**0.55 % apart**, where the two-run figure was 3.8 % apart. The run that was
missing was the one that made the pairing argument look weaker than it is.

### 9.10.3 Campaign `c2` — the `eng` arm §9.6 promised

Four runs, ABBA, one session, one host, on 2026-08-04 between 22:54 and 23:36.
`eng` is `5a2ca417`; `cur` is `ce52d2f9`, the same tree `c1` measured, measured
again inside this campaign so the difference is paired rather than carried across
sessions.

**This data was collected by a turn that ended before publishing it, and was
recovered and verified in a later one.** What was checked is in the raw-data
README: both measured trees diff clean against `git archive` of the commit each
claims to be, `measure.sh` carries this document's single sha256, all eight
windows hold 60 samples, and all four `mn` windows report 1000 live sessions with
0 closes, 0 dial errors and 0 read errors. What could not be checked — an
independent witness to the host during those four runs — is stated rather than
glossed; each `run.json`'s own host records are the same evidence every other run
here rests on.

**Arm `eng` — `5a2ca417`.**

| Run | Order | M(0) | M(N) | mem_per_session |
|---|---|---:|---:|---:|
| step01 | M(0) first | 3,694,592 | 49,086,464 | **45,391.9 B** |
| step04 | M(0) first | 3,870,720 | 48,840,704 | **44,970.0 B** |
| **pooled (2 runs)** | | | | **45,180.9 B** — spread 0.9 %, stable |
| *labelled forced-GC floor* | | | | *35,131.4 B* |

**Arm `cur` — `ce52d2f9`, re-measured inside `c2`.**

| Run | Order | M(0) | M(N) | mem_per_session |
|---|---|---:|---:|---:|
| step02 | M(0) first | 3,772,416 | 68,931,584 | **65,159.2 B** |
| step03 | M(0) first | 3,588,096 | 71,221,248 | **67,633.2 B** |
| **pooled (2 runs)** | | | | **66,396.2 B** — spread 3.7 %, stable |
| *labelled forced-GC floor* | | | | *45,774.8 B* |

| | |
|---|---:|
| **`cur` → `eng`, paired in campaign `c2`** | **−21,215 B, −32.0 %** |
| `eng` vs the 46,080 B G2 gate | **0.98× — under it by 899 B, a 2.0 % margin** |
| `eng` vs RFC-0001 §6.2's 42,416 B composition estimate | 1.07× |

The §3.6 secondaries, medians of the point readings at each window's close:

| Signal | `cur` (`ce52d2f9`) | `eng` (`5a2ca417`) | moved |
|---|---:|---:|---:|
| goroutines | **exactly 2** | **exactly 2** | — |
| goroutine stacks | 12,861 | 12,911 | +50 |
| live heap | 22,747 | 12,251 | **−10,496** |
| total Go-runtime mapped | 64,131 | 47,092 | −17,039 |

**This is the measurement RFC §6.2.4 was written from, and it confirms that
table's two claims.** The live heap fell by 10,496 B — the two hijacked `bufio`
buffers and net/http's retained request state, which is exactly what `5a2ca417`
set out to remove — and the goroutine-stack class did **not** move, against the
prediction that a fresh read-pump goroutine would start on a smaller stack. RFC
§6.2.4 records that failed prediction and it is confirmed here: +50 B, which is
nothing.

One imprecision in §6.2.4 is worth noting for whoever maintains it, and it is not
an error in the arithmetic: its `12,179 B` live heap and `12,780 B` stack figures
are **step01's readings**, not the two-run cell medians (`12,251` and `12,911`).
The method is §9.5's; the sample is one run rather than the cell.

**`cur` was measured in two campaigns, and the two do not agree to better than
2 %.** `c1` pooled it at 64,970.8 B over three runs; `c2` pooled it at 66,396.2 B
over two. Identical code, identical harness, same host, ninety minutes apart:
**+1,425 B, +2.2 %**. That is the size of this host's between-campaign drift on
this measurement, measured rather than assumed, and it is why every comparison in
§9.10 is drawn **inside** a campaign and never across two.

### 9.10.4 Campaign `c3` — the tree this PR ships, against `ce52d2f9`

| Arm | Tree | What it is |
|---|---|---|
| **head** | `d66e4953` | **the tree this PR ships.** Fifty-four commits after `5a2ca417` |
| **cur** | `ce52d2f9` | the same reference `c1` and `c2` used, measured a third time so this campaign's delta is paired inside itself |

**Why HEAD and not `5a2ca417`.** G2 is an absolute comparison against a fixed
46,080 B, and the tree that has to clear it is the one that ships. `5a2ca417` is
not that tree: between it and `d66e4953` sit the REV-DEL, REV-DUP and REV-INV
remediations, BR-1…BR-9, U-3…U-6, C-34, C-35, C-36, C-38, D-30 and the `livetest`
extraction — **54 commits**, of which the ones touching code the measured binary
links are `internal/wsx/{conn,handler,hijack}.go`, `internal/session/{types,window}.go`
and `live/{app,config}.go`. A figure for `5a2ca417` is not a figure for what is
being shipped, and §9.10.7 is careful about what the difference between them can
and cannot be attributed to.

**The measured tree is still the shipping library**, and that is checkable rather
than asserted: `git diff d66e4953..HEAD -- live internal proto client` is **empty**
at the time of writing. Everything committed to the branch since the arm was
exported is documentation and this campaign's own data.

**Slots, ABBA, one `measure.sh` invocation each**, so arms interleave run by run
and drift linear in time cancels in the difference instead of loading one arm:

```
01 head   02 cur   03 cur   04 head   05 head
06 cur    07 head  08 cur   09 head   10 cur
```

Slots 07–10 were an **extension, decided and written into `campaign-c3.log`
before they ran**, with a stopping rule fixed at the same time. The three-run
head cell had pooled 45,768.7 B against a 46,080 B gate — a 0.68 % margin against
a 3.0 % spread, with one of its three runs already over — and a verdict that thin
should not rest on three runs when §6 asks for five. **The stopping rule was five
per cell whatever the five said, and five is where it stopped.** Extending toward
the spec's own run count is method-conforming; extending past it after seeing a
number would be the move §6.1.2 exists to make unavailable.

Both trees are `git archive` exports outside the worktree, for the reason §9.2
gives and with the same two manifest fields reading oddly in consequence
(`git_sha: unknown`, `git_dirty: true`). Each arm's export was diffed against
`git archive` of its commit before the campaign and matched byte for byte, and
`measure.sh` carries this document's single sha256 in both.

**Every run in this campaign is flagged contended**, by §3's stated rule and for
§3's stated reason, and this campaign is the **least** loaded of the three:

| | `c1` | `c2` | `c3` |
|---|---:|---:|---:|
| one-minute load average across all windows, median | 11.21 | 8.21 | **4.08** |
| the same, min – max | 2.57 – 22.66 | 4.55 – 13.55 | **1.92 – 9.52** |
| unrelated containers running | 19–24 | 20–23 | **20–22** |
| host memory available | 30.6 – 33.1 GB | 29.9 – 33.3 GB | **33.4 – 33.7 GB** |

`gpu-desktop-steam-1` was checked rather than assumed, as in §9.3, and the answer is
the same: **no interactive session** — the log's most recent events are the
controller peer disconnecting, the media pipeline stopping and the signalling
client re-registering and waiting — while it burns **≈365–377 % CPU** throughout,
measured with `docker stats --no-stream` at the start of every slot and recorded
in `campaign-c3.log`. Nothing was stopped, restarted or touched. **One other
agent was working in this repository during the campaign**, in `docs/pm/` and
`docs/dependencies.md`; that work is documentation and ran no suites, and its
commits landed in the shared worktree while the measured trees sat outside it,
which is exactly what the export discipline is for.

**No analysis ran on this host during a measured window.** Every recomputation is
containerised, and running one would have been load this document's own
contention rule would then have to account for.

### 9.10.5 Results — N = 1000, Idle, observability ON, TLS outside

Every window is 60 samples at 1 Hz and **no window was rejected**; every `M(N)`
window held exactly 1000 live sessions with 0 closes, 0 dial errors and 0 read
errors. **Every run is published, including the two that are over the gate.**

**Arm `head` — `d66e4953`, the tree this PR ships.**

| Run | Order | M(0) | M(N) | mem_per_session |
|---|---|---:|---:|---:|
| step01 | M(0) first | 3,678,208 | 49,823,744 | **46,145.5 B** — *over the gate* |
| step04 | M(0) first | 3,674,112 | 49,442,816 | **45,768.7 B** |
| step05 | M(0) first | 3,624,960 | 48,394,240 | **44,769.3 B** |
| step07 | M(0) first | 3,825,664 | 51,118,080 | **47,292.4 B** — *over the gate* |
| step09 | M(0) first | 3,899,392 | 48,795,648 | **44,896.3 B** |
| **pooled (5 runs)** | | | | **45,768.7 B** — spread 5.5 %, stable |
| *labelled forced-GC floor* | | | | *35,029.0 B* |

**Arm `cur` — `ce52d2f9`, re-measured inside `c3`.**

| Run | Order | M(0) | M(N) | mem_per_session |
|---|---|---:|---:|---:|
| step02 | M(0) first | 3,842,048 | 73,670,656 | **69,828.6 B** |
| step03 | M(0) first | 3,829,760 | 70,950,912 | **67,121.2 B** |
| step06 | M(0) first | 3,809,280 | 73,482,240 | **69,673.0 B** |
| step08 | M(0) first | 3,567,616 | 74,412,032 | **70,844.4 B** |
| step10 | M(0) first | 3,850,240 | 68,980,736 | **65,130.5 B** |
| **pooled (5 runs)** | | | | **69,673.0 B** — spread 8.2 %, stable |
| *labelled forced-GC floor* | | | | *46,374.9 B* |

| | |
|---|---:|
| **`cur` → `head`, paired inside `c3`** | **−23,904 B, −34.3 %** |
| **`head` vs the 46,080 B G2 gate** | **0.993× — under it by 311.3 B, i.e. 0.68 %** |
| head runs individually over the gate | **2 of 5** (46,145.5 B and 47,292.4 B) |
| `head` vs RFC-0001 §6.2's 42,416 B composition estimate | 1.079× |
| the §6.1.2 ratchet at 36,864 B | **not reached** — 8,905 B above it |

§6 asks for bootstrap 95 % confidence intervals on pooled percentiles. That rule
is written for latency over thousands of samples; five run-level figures do not
support a bootstrap and one was not manufactured. Every run is published, which
is the part of §6 that applies.

### 9.10.6 The §3.6 secondaries

Medians of the point readings taken at each window's close, per session.

| Signal | `cur` (`ce52d2f9`) | `head` (`d66e4953`) | moved |
|---|---:|---:|---:|
| goroutines | **exactly 2** | **exactly 2** | — |
| goroutine stacks (`/memory/classes/heap/stacks:bytes`) | 12,911 | 12,943 | +32 |
| live heap (`/gc/heap/live:bytes`) | 22,733 | 12,190 | **−10,543** |
| total Go-runtime mapped (`/memory/classes/total:bytes`) | 66,556 | 44,921 | −21,635 |
| kernel socket memory (`memory.stat sock`) | **0** | **0** | — |

**This reproduces `c2`'s signature in an independent campaign**, which is worth
more than either campaign alone. `c2` measured the same two arms' difference as
live heap −10,496 B and stacks +50 B; `c3` measures −10,543 B and +32 B against a
tree fifty-four commits further on. The transport work's saving is a property of
the code and not of a campaign, and **§3.4's two-goroutines-per-session claim
holds in every run of every cell in this document.**

### 9.10.7 What the delta attributes, and what it does not

**`cur` → `head` is −23,904 B, and it is a delta across fifty-four commits.**
It is not `5a2ca417`'s delta, it is not any single commit's delta, and this
section does not divide it up. What §9.10.6 shows is that the *shape* of the
movement is the one `5a2ca417` predicted and `c2` measured — the live heap falls
by ≈10.5 KB and the goroutine-stack class does not move — which is evidence that
the transport work is most of it and is **not** a measurement of how much.
Attributing the remainder needs the per-component heap profile RFC §6.3
describes, run against `d66e4953`. **It was not run here, and no share is
estimated.**

**`head` versus `eng` (`5a2ca417`) cannot be read off these numbers, and the
reason is measured rather than argued.** The two were measured in different
campaigns, and this document now has three independent measurements of one
unchanged tree with which to size that:

| `ce52d2f9`, identical code, three campaigns | headline | forced-GC floor |
|---|---:|---:|
| `c1` (2026-08-04, median load 11.21) | 64,970.8 B | 45,822.0 B |
| `c2` (2026-08-04, median load 8.21) | 66,396.2 B | 45,774.8 B |
| `c3` (2026-08-05, median load 4.08) | **69,673.0 B** | 46,374.9 B |
| **spread across campaigns** | **7.2 %** | **1.3 %** |

**The between-campaign spread on unchanged code is 7.2 %, and the head-versus-eng
difference is 1.3 %.** `head` pools 45,768.7 B and `eng` pooled 45,180.9 B —
+587.8 B — while `c3`'s reference arm sits 4.9 % above `c2`'s. The difference
between the two trees is comfortably inside the offset between the two campaigns
that measured them. **The honest statement is that this measurement cannot
distinguish `d66e4953` from `5a2ca417` per session, not that they are equal.**

Two further things follow, and the second is uncomfortable:

- **Relative to each campaign's own reference arm, `head` is if anything the
  better of the two.** `eng`/`cur` in `c2` is 0.680; `head`/`cur` in `c3` is
  0.657. That is a ratio argument offered as an argument, not as a §3.6
  measurement, and it is the only form in which the two trees can be compared at
  all. What it supports is the modest claim that **fifty-four commits did not
  add measurable per-session memory**; what it does not support is a byte count.
- **The direction of the cross-campaign drift is the opposite of the one §3
  predicts.** §3 argues that contention can only push the figure **up**, "which
  is the direction in which reporting is safe". Across these three campaigns the
  *least* loaded produced the *highest* figure for identical code, and the
  correlation is monotone in all three. So §3's argument does not explain this
  drift, and this document should stop leaning on it as though it explained
  everything. The floor moving only 1.3 % while the headline moves 7.2 % locates
  the drift in **collectable garbage rather than in retained per-session memory**,
  and §9.10.9 says what that means for a 0.68 % margin.

### 9.10.8 RFC-0001 §6.1.2, applied clause by clause to the figure in hand

> - If the measured **TLS-outside** total exceeds **46,080 B**, the target does
>   **not** move. The overage is attributed to a named line in §6.2 and either
>   engineered down or escalated to an ADR that moves the target with L9-1's
>   approval and the measurement in hand. **A benchmark-method change is not an
>   available remedy for a missed memory target.**
> - If the measured total comes in below **36,864 B (36 KiB)**, the gate is
>   **tightened** to the measured value plus 10 %, in the same PR.

| Clause | As of 2026-08-05, at `d66e4953` |
|---|---|
| does the measured total exceed 46,080 B? | **The pooled figure does not: 45,768.7 B, under by 311.3 B.** Two of the five runs do. §9.10.9 is why that is reported as an unresolved margin and not as a pass |
| the target does not move | **The gate is still 46,080 B.** Nothing in this landing, in RFC §6.1, or in equivalence-spec §3.6 changed it, and no number in §6.1.2 was touched |
| attributed to a named line | Where the movement went is named in §9.10.6 — the live-heap line, by ≈10.5 KB, with the goroutine-stack class unmoved — and §9.10.7 states plainly that the remainder of a 54-commit delta is **not** attributed and is not guessed at. RFC §6.2.4's composition carries the per-line detail for `5a2ca417`; it has **not** been re-derived at `d66e4953` |
| engineered down **or** escalated | **Engineered down, over three landings** — `9f88d75e`, `5a2ca417`, and whatever in the intervening 54 commits did not undo them: 82,104 B → 69,673 B → 45,181 B → 45,769 B by arm. **Escalation is not withdrawn**: [ADR-002](../adr/002-observability-memory-budget.md) is **PROPOSED**, it requires L9-1's approval, and **DEV-1 does not approve it** |
| no benchmark-method change | **None was made and none was sought.** §3.6 is frozen under §12 and was not amended, extended or reinterpreted. `measure.sh` is byte-identical across every tree measured in this document — one sha256 — which is checkable rather than asserted. The one campaign decision taken mid-flight, extending 3 runs to §6's 5, was written into the log with its stopping rule **before** the extra runs ran |
| **the ratchet — under 36,864 B ⇒ tighten** | **Checked, and it does not trigger.** 45,768.7 B is **8,904.7 B above** the 36,864 B ratchet threshold, so the gate is not tightened. It is checked and stated either way because §6.1.2 makes it mandatory to, not because it was close |

**E1, both falsifiers, restated honestly.**

| RFC §15 E1 falsifier | §4 | §9.4 | Here |
|---|---|---|---|
| `> 46,080 B` | 82,559 B — tripped | 64,970 B — tripped | **45,769 B — not tripped on the pooled figure, and tripped by 2 of the 5 runs that produced it** |
| `N=1000 differs from N=100 by > 15 %` | −15.6 % — tripped | not re-measured | **STILL NOT RE-MEASURED** — §9.10.11 |

### 9.10.9 The margin is smaller than the method's own resolution, and that is the result

**This is the part of §9.10 that matters most, and it is the part that is least
convenient.**

The pooled figure is under the gate by **311.3 B**. Here is what this campaign
measured about how much a figure produced this way can be trusted to that
precision:

| | |
|---|---:|
| the margin under the gate | **311 B (0.68 %)** |
| the head cell's own run-to-run spread, 5 runs | **2,523 B (5.5 %)** |
| head runs individually over the gate | **2 of 5** |
| between-campaign movement of the **unchanged** `ce52d2f9` tree | **4,702 B (7.2 %)** |

**The margin is an eighth of the cell's own spread and a fifteenth of the drift
this method shows on code that did not change.** A number under the gate by
0.68 % is not evidence that the tree is under the gate; it is evidence that the
tree is *at* it. Had `step07` (47,292.4 B) fallen where `step09` (44,896.3 B)
did, the median would have been over, and nothing about the code would have
differed.

**So this section does not report G2 as met, and no reading of it should.** What
it reports is that the tree this PR ships measures **at** the 46,080 B gate,
where §4 measured 79 % above it and §9.4 measured 41 % above it. That is a large
and real change, and it is a different claim from clearing a threshold.

**Where the variance is, because that is actionable.** The labelled forced-GC
floor is two to five times more stable than the headline in every cell here, and
across campaigns it moves 1.3 % where the headline moves 7.2 %:

| Cell | headline spread | floor spread |
|---|---:|---:|
| `c3-head-obson` (5 runs) | 5.5 % | 3.1 % |
| `c3-cur-obson` (5 runs) | 8.2 % | 2.1 % |
| `c2-eng-obson` (2 runs) | 0.9 % | 1.3 % |
| `c1-cur-obson` (3 runs) | 10.0 % | 3.0 % |

The mechanism is visible in the manifests and is not mysterious: `GOGC=100` makes
the heap sawtooth between the live set and twice it, and the `M(N)` window
records **8–13 completed GC cycles over five minutes** — so the 60-second sampled
window spans roughly two cycles, which averages the sawtooth partially and not
well. The headline is unforced steady state **by §3.6's deliberate choice**
("the headline stays unforced steady state, because that is what a deployment
sees"), and the price of that choice is a per-run figure that carries where in
the sawtooth its window happened to fall.

**None of this is a reason to quote the floor instead, and it is not quoted
instead.** §3.6 makes the floor a labelled secondary on both stacks or on
neither, and swapping to it after seeing that it is tighter *and* lower —
35,029.0 B, which is under the ratchet threshold — would be the disqualifying
method error this document has refused three times now. It is reported because
it scopes the variance and because hiding it would be the mirror of quoting it.

**What would resolve the margin**, offered as the next measurement rather than as
a complaint: more runs at this one cell, on a host that is not also serving a
GPU desktop at ≈3.8 cores; and RFC §6.3's per-component heap profile at
`d66e4953`, which measures retained per-session bytes directly and is not subject
to the sawtooth at all. Neither is a method change — both are §3.6 and §6.3 as
written.

### 9.10.10 The observability-OFF cell at the shipping tree — owed since §9.9.4, now measured

§9.9.4 recorded this cell as not re-measured and forbade inferring it by carrying
§4's 25,424 B instrumentation share forward. **It is measured here**, at the same
tree, in the same campaign, by the same method: N = 1000, Idle, TLS outside,
`Config.Logger`, `Config.Metrics` and `Config.Tracer` all nil.

| Run | Order | M(0) | M(N) | mem_per_session |
|---|---|---:|---:|---:|
| step11 | M(0) first | 3,579,904 | 46,583,808 | **43,003.9 B** |
| step12 | M(0) first | 3,522,560 | 44,691,456 | **41,168.9 B** |
| **pooled (2 runs)** | | | | **42,086.4 B** — spread 4.4 %, stable |
| *labelled forced-GC floor* | | | | *31,946.8 B* |

**Two runs, not five, and it is reported as two** — the same disclosure §7.6 and
§9.9.5 make. The obs-on cell was the one to spend five runs on, because it is the
one the gate is evaluated against.

| | |
|---|---:|
| observability ON, `d66e4953` (5 runs) | 45,768.7 B |
| observability OFF, `d66e4953` (2 runs) | 42,086.4 B |
| **default-on observability's share, at the shipping tree** | **3,682 B/session — 8.0 % of the headline** |
| the same share when §4 first measured it | **25,424 B/session — 30.8 %** |
| | **−21,742 B, −85.5 %** |

**§9.9.4's refusal to carry the old share forward was right, and by a factor of
seven.** Anyone who had inferred the observability-off cell by subtracting
§4's 25,424 B would have published ≈20,300 B — less than half the measured
42,086 B. The instruction not to do the arithmetic was not pedantry.

**One secondary moved in the direction nobody would predict, and it is published
unexplained.**

| Signal, per session | obs ON (5 runs) | obs OFF (2 runs) |
|---|---:|---:|
| goroutines | exactly 2 | exactly 2 |
| goroutine stacks | 12,943 | **13,681** |
| live heap | 12,190 | 11,846 |
| total Go-runtime mapped | 44,921 | 40,602 |
| completed GC cycles in the `M(N)` window | 12–13 | 9–10 |

**Turning observability off made the goroutine-stack class ≈738 B/session
larger**, and the two cells' per-run ranges do not overlap (12,845–13,042 against
13,337–14,025). That is the exact inverse of §5.1, which found instrumentation
*doubling* a stack — a finding that was true of the code it measured and that
`9f88d75e` then removed. What is left is small and points the other way.

**A hypothesis is offered and is labelled as one, because two runs do not support
a finding**: Go shrinks goroutine stacks only at GC and only to a quarter
occupancy, and the instrumented cell completes 12–13 GC cycles in the window
against the uninstrumented cell's 9–10. More collections mean more
stack-shrinking passes. **That is a mechanism that would explain the sign; it is
not a measurement of it, and no attempt is made here to turn it into one.**

### 9.10.11 Not measured in this campaign, and why

§7 and §9.9 apply unchanged except where stated. Four items need restating so
that nothing here is read as having moved them.

#### 9.10.11.1 §3.6's driver validation gate — STILL not run, and this figure is still not G2

**Four campaigns have now measured this dimension and none has run the gate.**
§3.6 makes it mandatory before any 1k number is quoted: per-session memory with
**10 real Chromium tabs** against **10 synthetic sessions**, on both stacks, the
driver fixed if they differ by more than 10 %.

The consequence is §3.6's own and is accepted rather than argued around: without
it, *"the 1k number is an assertion about a synthetic client, not about
sessions"*. **Every figure in §9.10 is that kind of assertion**, including
45,768.7 B. A number below 46,080 B is **not G2 met**, is not a Phase-5 quotable
figure, and does not become one by being the smallest this document has printed.
G2 is enforced at Phase 5, at 1k idle sessions, by QA-2, against the comparison
stack, after the driver gate has run. **Nothing here ticks it**, and §9.10.9 is a
second, independent reason not to read it as ticked.

§7.1's filed resolution problem — that a per-session figure is not scale-free, so
a ±10 % criterion at N = 10 compares two numbers largely made of the same
non-per-session share — is unchanged and still filed rather than acted on.

#### 9.10.11.2 The N = 100 sub-linearity cell and E1's second falsifier — STILL not re-measured

§4.3 measured −15.6 % against RFC §6.3's ±15 % bound and §6 reported E1's second
falsifier as tripped. **Neither `c1`, `c2` nor `c3` re-ran the N = 100 cell.** The
status of E1's second falsifier at `d66e4953` is **unknown**: it is not "still
tripped" and it is not "cleared", and it is not inferable from anything here.

It is now more owed than it was, not less, and the reason is §9.10.9: a
per-session figure that carries a fixed cost divided by N is exactly what the
sub-linearity check is sensitive to, and the fixed term has moved a long way
since §4.3 measured it. **Re-running N = 100 at the shipping tree is the next
thing this dimension owes**, and it is one cell of the same method.

#### 9.10.11.3 Per-component heap attribution at `d66e4953` — not run

RFC §6.2.4's composition was derived from `ce52d2f9`'s profile and `5a2ca417`'s
code, and §6.3's per-component heap profile has **not** been re-run at the
shipping tree. That is what would attribute the −23,904 B of §9.10.7 line by
line, and what would measure retained per-session bytes without the GC sawtooth
§9.10.9 describes. `memsrv` still exposes `/heapprofile` and `diag.sh` still
takes `--memprofilerate`, so it is a run and not a build. **It was not run here
and nothing is estimated from its absence.**

#### 9.10.11.4 Everything else in §7 and §9.9, unchanged

The in-process-TLS secondary (§7.2), the reverse proxy (§7.3), the two workloads
other than Idle (§7.4), the Next.js side (§7.7) and client memory (§7.8) are
exactly as §7 leaves them: not measured, for the reasons stated there, and not
estimated here either. The two conditions §9.6.1 attaches to the transport
saving — the ResponseWriter shape (C-36) and the non-pipelining client (C-37) —
bind the figures in §9.10 exactly as they bind §9.4's and §9.6's, and the
≈6,656 B/session an all-pipelining client population would restore is **larger
than this section's entire 311 B margin under the gate**. `memdrv` provably
cannot pipeline; a real peer can. QA-2 owns whether the Phase-5 workload does.
