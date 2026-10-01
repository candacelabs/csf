[identifiers genericized for publication - measurements unmodified]

# Checkpoint 3 gate report — gotth-live

| Field | Value |
|---|---|
| Project | **gotth-live** — a Go library for server-driven live UI: state and rendering stay in Go, the browser holds one long-lived connection, events go up and re-rendered HTML fragments come down |
| Phase | **3 — Resilience**, checkpoint 3 of the consolidated Phase 1–3 track |
| Report owner | **PM-1**, who owns scope and the requirements document |
| Date | 2026-08-05 |
| Tree | `495c7855` on `dev-/gotth-live-orchestrator-c3efc4` |
| Verdict | **CLOSED WITH CARRIED DEBT.** **PHASE 3 EXITS**, on the re-held gate act of **2026-08-05 at tree `713a3192`** recorded in **§12** — seventeen of seventeen. *What this row said when the report was written, kept beneath itself rather than replaced, because it was true for the life of the open box and §12 is a correction to it and not a denial of it:* **"CLOSED WITH CARRIED DEBT — and Phase 3 does not exit, on one open criterion."** |
| Also closes | **Checkpoint 1**, which has never had a gate record. §4 |
| Resilience sign-off | **QA-2 — PASS** at `1864cf92` ([`docs/qa/checkpoint-3-chaos.md`](../qa/checkpoint-3-chaos.md) §R18), covering `5a2ca417` and everything after it. C-35(c) discharged |
| Technical veto | **L9-1 — APPROVED, no conditions on this gate** ([`docs/reviews/checkpoint-3.md`](../reviews/checkpoint-3.md) §10), after a BLOCK whose three items were all discharged |
| Requirements applied | [PRD](../PRD.md) **v0.7**, §9's five rows, landed with this report |
| Format precedent | [`docs/gates/checkpoint-2.md`](checkpoint-2.md), itself following [`phase-0.md`](phase-0.md) |
| Binding instrument | L9-1's [§10.4](../reviews/checkpoint-3.md) MAY-NOT list, which replaces their §9's. Every claim below is written against it |

**Who the named roles are**, since they appear throughout: **QA-1** owns
correctness and can block a merge; **QA-2** owns resilience and performance and
can also block a merge; **L9-1** is the Principal Engineer and holds technical
veto; **DEV-1** is the server-core Go engineer, **DEV-2** owns the browser-side
client runtime, **DEV-3** owns the examples and interoperability with HTMX;
**BENCH-1** owns the Phase 5 comparison apps; the **orchestrator** runs the
project and, at this checkpoint, ran the gate.

---

## 1. Verdict

**Checkpoint 3 is closed with carried debt. Checkpoint 1 is closed here too.
Phase 3 does not exit, because one of its seventeen exit boxes is not met and I
am not going to move it.**

Sixteen of Phase 3's seventeen criteria are met — nine top-level and the eight
chaos cases — each checked against evidence with its owner named. QA-2's PASS
covers the transport change and everything after it. L9-1 approved with **no
conditions on this gate**. The three debt boxes v0.5 opened because they were
*"owed by a phase and enforced by nobody"* are all three delivered, and that
mechanism working is the most useful structural result of this checkpoint.

**The seventeenth is the dashboard's resync cost, and it does not tick.** The
figure published in `examples/dashboard/README.md` was produced by a request
shape the measurement program no longer sends: `c1338120` rewrote `resync.go`
*because* BR-9 made the old request unanswerable, and the commit's own body says
the frame it was timing *"is one a browser would have hung up on"*. The README's
number and its stated method both predate that rewrite, so the document describes
a program that does not exist. PRD §6's rule is that a phase exits when every box
is checked. One is not. **So checkpoint 3 closes as a review checkpoint and
Phase 3 stays open on one command and one paragraph, owned by DEV-3.** §5.3 says
exactly what closes it.

> **Correction beneath, 2026-08-05: the one command and the one paragraph both
> landed, the box has been re-held, and PHASE 3 EXITS — seventeen of
> seventeen.** DEV-3's remedy is `1b16f4a9`; the gate act is PM-1's, at tree
> `713a3192`, and it is **§12** of this report. The two paragraphs above are left
> exactly as written because they were true for the whole life of the open box
> and because a verdict that is silently overwritten is one nobody can audit.
> **The tick is on evidence produced at the gate, not on the remedy's commit
> message:** the measurement was re-run six times at HEAD — three of them on a
> pristine `git archive HEAD` export — and the published byte figures reproduce
> byte-for-byte, 101 commits after they were taken, and again at `2ab18690`
> after a commit changing the rendered binding encoding landed mid-gate.

**What the verdict is not.** It is not "closed", for the reason above, and it is
not blocked either — nothing open here is a defect in the shipping library, and
QA-2 found no new library defect in the re-verification pass. It is also not a
claim that the gate is green **at HEAD**: §2.1 says which commit each gate result
belongs to, which run is still executing as I write, and where its result will be
appended by the owner who ran it.

**The standing rule on this project is that a gate is what you ran, not what you
read — and at this checkpoint I ran almost none of it.** That is a departure from
checkpoint 2, where every number in §2 was mine, and it needs stating in the
verdict rather than in a footnote. There is no Go toolchain on this host outside
a container, a whole-gate run was executing on the machine throughout, and three
other agents had measurements in flight. So §2 is mostly a table of other
people's numbers with their owner and their commit attached, in the form
`docs/qa/checkpoint-3-chaos.md` §R8 uses for the four rows that are not QA-2's.
**What I did do myself is the part that needs no toolchain and that nobody else
had done: I opened the tree and checked the claims** — every FR-36 span site by
name, the sampling falsifier's assertions, the dashboard README against
`resync.go`, and the four `git diff`s that decide whether QA-2's PASS and the
orchestrator's size figures describe the tree this report is about. Those
findings are mine and §2.2 says so.

---

## 2. What I ran, and what came back

### 2.1 The gate script — three states, and the one that is not mine to quote

This row is written so that it stays true after the run in flight finishes.
L9-1's §10.4 MAY-NOT #5 is the reason: *"no whole-gate green may be claimed at
HEAD until a run against a tree containing this step is quoted by the owner who
ran it"*, where "this step" is C-40's new `ci.sh` check, added at `597902f7`.

| Tree | What ran | Result | Whose |
|---|---|---|---|
| **`99b769be`** | `bash ci.sh` in `dis-gotth-live:latest`, repository root mounted, against a `git archive HEAD` export with **no `bench/fixtures/*/ticks.jsonl`** | **EXIT 0.** `every gate this invocation could run is green`; `FAILED:` empty | **the orchestrator**, quoted in [`docs/pm/checkpoint-3-closure.md`](../pm/checkpoint-3-closure.md) §9 |
| **`73f5bf2f`** | the same | **RED on `gofmt` (NFR-12) and green on everything else** — one whitespace violation in QA-2's own spec file | the orchestrator |
| `4c4b751a` | — | that single violation fixed | `4c4b751a` |
| **`4c4b751a`** | `bash ci.sh`, full re-run, **including C-40's new step** | **EXECUTING as this report is written.** §2.4 holds the row it lands in | the orchestrator, to quote |

**What checkpoint 3 therefore claims about CI, exactly.** The gate is green at
`99b769be`, on a fixture-less export, under the conditions C-33(a)'s falsifier
specifies — that is L9-1's §10.4 row 1 as lifted, with its anchor named.
**Checkpoint 3 does not claim a green whole-gate run at HEAD.** Two things have
changed the gate since `99b769be`: QA-2's spec file, and `ci.sh` itself.

**One thing I can say now that makes the pending run worth more, and I checked it
rather than assuming it.** `4c4b751a` is not HEAD — `495c7855` is — but the
difference is documentation only:

```
git diff 4c4b751a 495c7855 -- . ':!docs'   ->   EMPTY
```

So **a green run at `4c4b751a` covers every non-documentation file in the tree
this report is about**, byte for byte, including the new `ci.sh`. That is not a
claim that it is green; it is a claim about what its result will and will not
have to be qualified by. The first thing that run has to show is C-40's own
output line, `clean: 3 modules, no internal/ import and no build tag`, which I
read at `ci.sh:541`.

### 2.2 What I checked myself, in the tree

None of this needs a toolchain, all of it needed doing, and three of the six
rows changed something in this report.

| # | What I checked | How | Result |
|---|---|---|---|
| 1 | **Is the library QA-2 graded the library this gate closes?** | `git diff 1864cf92 495c7855 -- live internal client proto` | **Empty.** QA-2's PASS at `1864cf92` describes this tree's library byte for byte. The only non-doc changes in that range are `ci.sh` (+49, C-40's step) and QA-2's own chaos spec file |
| 2 | **Do the orchestrator's client figures describe this tree?** | `git diff 73f5bf2f 495c7855 -- client` | **Empty.** So 4,429 B gzipped is HEAD's figure, not a stale one |
| 3 | **Does the browser-conformance result describe this tree?** | `git diff 73f5bf2f 495c7855 -- test/internal/conformance` | **Empty.** The 25-spec browser cell ran against this suite |
| 4 | **Are FR-36's five unstarted spans started?** | `grep -rn 'obs.Span<name>' --include=*.go`, non-test only, one name at a time | **All five, on the real path**, plus the three that were already started. Sites in §3's row. This is the box's evidence and it is mine |
| 5 | **Can C-30's falsifier fail?** | read `test/sampling/sampling_test.go` and its `ci.sh` step | **Yes, and it cannot pass vacuously** — two anti-vacuity assertions, three sampling rates, a real SDK sampler, its own module and its own gate step. §3's row |
| 6 | **Is the dashboard's resync figure current?** | `git diff ce52d2f9 495c7855 -- examples/dashboard/README.md` against `git diff --stat … -- examples/dashboard/resync.go`, then both texts | **No, and worse than stale.** README byte-identical across the range; `resync.go` +101/−40 in `c1338120`. The README's *method* paragraph describes the request the program deliberately stopped sending. §5.3 |

### 2.3 The numbers, and whose each one is

**No figure in this table was produced by me.** Each names its owner and the
commit it belongs to, which is the rule the chaos report's §R8 applies to rows
that are not QA-2's, applied here to a report almost all of whose rows are
somebody else's.

| What | Figure | Tree | Whose, and where I read it |
|---|---|---|---|
| Chaos suite, `-race`, `GOTTHLIVE_SOAK=1 GOTTHLIVE_MEASURE=1` | **42 of 42**, 425.840 s | `1864cf92` | QA-2, chaos §R12.1 |
| Appendix-B measurements, unraced and pinned | **4 of 4** | `1864cf92` | QA-2, §R12.2 |
| `internal/wsx`, `-race` (C-34, BR-8, `hijack.go` all landed there) | **38 of 38** | `1864cf92` | QA-2, §R12.3 |
| D-10's two signals at 10,000 cycles | live heap **0.4 B/cycle** of 902,144 B; RSS **1,766.6 B/cycle** of 49,348,608 B | `1864cf92` | QA-2, §R12.3 — **this is what closes CP1-16**, §4 |
| Client runtime, minified | **10,391 B** | `73f5bf2f` | the orchestrator, `tools/minify` in `dis-gotth-live-bench:latest` |
| Client runtime, **`gzip -9` — the NFR-2 gate** | **4,429 B** of 12,288 B — **7,859 B headroom, 64.0 %** | `73f5bf2f` | the orchestrator; `client/SIZE.md`'s gate row agrees to the byte |
| Client runtime suite (NFR-4, incl. the no-eval scan) | **6 of 6** `client/test/*.test.mjs` | `73f5bf2f` | the orchestrator, bench image |
| Browser conformance, `GOTTHLIVE_E2E=1`, `-ginkgo.fail-on-empty` | **exit 0, 25.6 s** | `73f5bf2f` | the orchestrator, bench image |
| Bench fixture skips, per Ginkgo's own report | counter **0 of 49**, chat **1 of 62**, dashboard **4 of 88** | `99b769be` | the orchestrator, closure ledger §9 |
| G2 baseline, N=1000, Idle, obs on, TLS outside | **45,768.7 B/session** pooled over 5 runs; 2 of 5 individually over the 46,080 B gate; cell spread 5.5 % | `d66e4953` | **DEV-1**, `docs/bench/g2-baseline.md` §9.10.5. §5.1 |
| the same, observability **off** | **42,086.4 B**, 2 runs | `d66e4953` | DEV-1, §9.10.10 |
| X3, transport's share of retained idle memory | **13,759 B/connection**, adopted | — | **L9-1**, ADR-001 §7.2 |
| ADR-002's observability budget line | **4,050 B/session**, inside the gate | — | **L9-1** (the ruling), DEV-1 (the measurement), RFC §6.2.6 |
| Provenance log throughput at D3's N=1000 | **≈1.5 to ≈3 cores**, a range with its load attached, measured ABBA | `1864cf92` vs `ce52d2f9` | QA-2, §R14.3 |

### 2.4 The gate run in flight — a row for its result

*Left deliberately empty by PM-1. The run against `4c4b751a` was executing when
this report was written; its result belongs to the orchestrator who started it.
Appended below, 2026-08-05, in the way §9 of the closure ledger was appended.
Everything above this subsection was written before the result existed and
claims nothing about it.*

**Result: `bash ci.sh` EXIT 0 at `4c4b751a`.** Against a `git archive` export
under `/tmp/ci-head2` with **no `bench/fixtures/*/ticks.jsonl`** — the checkout
the workflow makes — in `dis-gotth-live:latest`, repository root mounted:

```
==> verdict
skipped (needs a context this invocation does not have):
  - client runtime suite (NFR-4)
  - browser conformance specs (22 + 3, the whole of FR-25…FR-28 and FR-30…FR-32 in a browser)
every gate this invocation could run is green
EXIT=0
```

`FAILED:` is empty. The three bench steps report the skips the run had, per
C-33(b): counter **0 of 49**, chat **1 of 62**, dashboard **4 of 88**. The step
that did not exist at the last quotable run reports itself:

```
==> bench/apps/*/gotth use consumer-reachable API only (FR-70, C-40)
clean: 3 modules, no internal/ import and no build tag
```

**What this run covers, checked rather than assumed.**
`git diff 4c4b751a HEAD -- . ':!*docs*'` is **empty**: every non-documentation
file at HEAD — including `ci.sh` itself, which C-40 moved at `597902f7` — is
byte-identical to the tree that produced the exit 0 above. The four commits
between them are documents.

**The two steps the library image structurally cannot run** were taken
separately in `dis-gotth-live-bench:latest` at `73f5bf2f`, and they describe
this tree for the same reason: `git diff 73f5bf2f HEAD` is **empty** over
`client/`, `test/internal/conformance/`, `examples/`, `internal/`, `live/`,
`proto/` and `tools/` — `ci.sh` is the only non-document file that moved in
that range.

| leg | result |
|---|---|
| client runtime suite incl. the no-eval scan (NFR-4) | **6 of 6** `client/test/*.test.mjs` pass |
| browser conformance, `GOTTHLIVE_E2E=1 … -ginkgo.label-filter=browser -ginkgo.fail-on-empty` | **EXIT 0**, 25.6 s. `-ginkgo.fail-on-empty` is what makes it evidence rather than a tautology |

**The run before this one was red, and that is part of the record.** The same
invocation at `73f5bf2f` failed on **gofmt (NFR-12) and on nothing else** — one
whitespace violation in `case8_replay_test.go`, `len(acks(w)) - acksBefore`
inside a call argument, fixed at `4c4b751a`. It is the second instance of that
exact shape (`docs/qa/ci-intermittents.md` records the first at `2bf564c5`), and
it is why this subsection quotes `4c4b751a` and not the tree PM-1 graded.

**What it discharges.** L9-1's re-review added *"no whole-gate green is
claimable at HEAD until its owner quotes it"* to the MAY-NOT list (§10.4 item
5). This is that quotation, from that owner, with the conditions of the run
stated. Checkpoint 3 may now say the gate is green **at `4c4b751a`, on a
fixture-less export, with the two browser-context steps quoted from
`73f5bf2f`'s identical bytes** — and may not say it more broadly than that.

*— the orchestrator, 2026-08-05*

### 2.5 What I did not run, and why

Per FR-73's rule applied to ourselves — "not measured, and why" beats an
estimate.

- **`bash ci.sh`.** A whole-gate run was already executing on this host. A second
  container would contend with a suite whose chaos specs carry timing bounds, and
  I would be risking a false red in the evidence this report depends on in order
  to produce a number I am not the owner of. L9-1 declined to re-run for the same
  reason and said so; this is that decision taken twice, independently, and it is
  worth recording that it was the same reason both times.
- **QA-2's 42 specs, the mutations, and the Appendix-B measurements.** Not
  re-run. They were taken on a clean export at `1864cf92`, on a host labelled
  contended per equivalence-spec §3.6, with the mutation evidence published and
  reproducible from `/tmp/qa2-mut3/mutate.py`. **The one thing I did check is the
  one thing re-running would not have told me**: that the library at `1864cf92`
  and the library at HEAD are the same bytes (§2.2 row 1). Re-running 42 green
  specs to get 42 green specs is ceremony; checking that they describe this tree
  is evidence.
- **The G2 campaign.** Four campaigns, twelve cells, ~2 hours of pinned
  measurement per campaign, on a host also serving a live Steam session at ≈3.8
  cores. It is DEV-1's instrument and DEV-1's document. **I read no number out of
  `docs/bench/` that I have not attributed to them by section**, and I did not
  re-derive any of it.
- **The dashboard's resync cost.** I could have run `go run . -resync-cost 200`
  and put a number in this report. **I deliberately did not**, for two reasons
  and neither is effort. It is a latency measurement and the host was running the
  gate, so the figure would carry a contention label and be worse than the one
  DEV-3 can take. And the number belongs in `examples/dashboard/README.md`, whose
  own method paragraph has to be rewritten in the same landing — publishing a
  second copy in a gate report while the first copy stays wrong is the exact
  defect class this project keeps catching, committed on purpose. §5.3.
- **Anything in `docs/bench/equivalence-spec.md`.** QA-2 is editing that file in
  parallel with this report. §8.3 gives my signature in this document instead,
  which is the input they are waiting on.

---

## 3. Verdict per Phase 3 exit criterion

PRD v0.7, *Phase 3 — Resilience (consolidated track, checkpoint 3)*. Seventeen
criteria — nine top-level and the eight chaos cases. Each row names the evidence
and whose measurement it is.

| # | Criterion | Verdict | Evidence, and whose |
|---|---|---|---|
| 1 | **QA-2 chaos suite green. This is the gate** | **MET** | 42 of 42 under `-race` with soak and measurement on, 425.840 s, at `1864cf92`; plus 4/4 unraced-and-pinned and `internal/wsx` 38/38. **QA-2's run, on a host labelled contended per §3.6** — a label that is what makes the numbers publishable. PM-1 checked that the library at that tree is byte-identical to this one (§2.2 row 1) |
| 1a | Dropped mid-patch → reconnect → resync → converges; no duplicated or lost effect | **MET** | Case 1: 40 interactions, 24 patched before the cut, 10 committed, **0 duplicated**, and the reconnected `Snapshot` matched truth. QA-2 §R17 |
| 1b | Sequence gap → resync rather than out-of-order (FR-11) | **MET** | Case 2, byte for byte unmoved *despite* BR-6 and BR-9 both landing on this exact path — which is a stronger result than a green cell, because the two changes that could have moved it did not. QA-2 §R17 |
| 1c | Server restarted under load, within a stated bound | **MET** | Case 3: SIGKILL of a real child process, bound 30 s, measured **611 ms**, port rebound in 4 ms. QA-2 |
| 1d | Slow client at a stated bandwidth (FR-51) | **MET** | Case 4 at 2,048 B/s: window 16 of 16, heap 169,664 B of a 4 MiB budget, 302 coalesced patches, other sessions unaffected, process alive; eviction arm closes `4009` at 3.879 s of a 4 s bound. **D-26 carried**, §7 — an eviction that cannot fire against a client that acknowledges is a policy gap, not a failure of this criterion's four clauses. QA-2 |
| 1e | Event flood (FR-51) | **MET on three clauses of four**, and the fourth is a named defect rather than a silent pass | Rate limit engages, typed error returned (9,961 `RATE_LIMITED`), allocation bounded (2,214,592 B of 4 MiB). **D-24**: the "defined close" was **still not reached at 2,562 frames/s — 60× the limit** — with the connection open throughout; D-24's own title puts the threshold above 300×. Carried, MEDIUM, DEV-1. QA-2 |
| 1f | Partition and half-open (FR-12) | **MET** | Detection 3.5 s of a 3.5 s bound; reclamation 7.945 s of an 8.5 s bound (**D-27**, carried, LOW); goroutines 13 → 8; the bystander kept being served. QA-2 |
| 1g | 10k churn, no goroutine/timer/heap leak (FR-22) | **MET** | Ten thousand **abnormal** cycles: goroutines 7 → 7, **−0.1 B/cycle** against a 902,144 B budget. The clean-close soak is CP1-16 and is also green (§4). QA-2 |
| 1h | Duplicate/replayed frames → defined semantics | **MET on the behaviour, against the box as amended in this landing.** §5.2 | One frame sent twice moved `state_version` 2 → 3 and ran the effect twice, asserted directly so that adding deduplication goes red. All five replay **defences** pass, and the stale-telemetry one now has three separate falsifiers after QA-2's own **D-32**. The documentation clause is FR-77's Phase 4 half and does **not** tick here; it has its own Phase 4 box. QA-2 §R17, and §5.2 for the amendment |
| 2 | Batching/debounce demonstrated; a coalesced patch names every contributing event | **MET** | Case 4 flushed with a union of 64; QA3-1 sweeps the whole legal range of `CoalesceFlushAt` with H-4 margins **960/896/768/512/65**, identical to the previous pass in every cell. The provenance half was hardened this checkpoint by **BR-4**, which found three emit exits that took the contributing ids and never emitted them — so this criterion is met by a mechanism that was measurably broken at the start of the checkpoint. QA-2 |
| 3 | Backpressure metrics exported: queue depth, drops, coalesce ratio (FR-34) | **MET, and the mapping is stated because one of the three words has no literal instrument** | Observed carrying real values: `gotthlive_outbound_window_depth` (16 of 16) and `gotthlive_mailbox_depth` for **depth**; `gotthlive_patches_coalesced_total` (302) against `gotthlive_patches_sent_total` for the **ratio**, exported as two counters and not as a ratio, which is correct practice; and for **drops**, `gotthlive_patches_suppressed_total`, `gotthlive_slow_client_events_total` (42) and `gotthlive_connections_closed_total{code}`. **This design does not drop a patch under backpressure** — it coalesces, then evicts — so "drops" maps onto suppression and eviction and there is no counter answering it literally. Said here rather than left for a reader to discover a mismatch between a requirement's word and a metric name. **D-22 carried**, and it is in the *connection* set, not this one. QA-2 §R17; the instrument list read by PM-1 in `internal/obs/metrics.go` |
| 4 | Live dashboard example (FR-62) built and running, plain-HTMX region on the same page | **MET** | Its own module, built, vetted and `-race` tested by `ci.sh`'s FR-62 step (`ci.sh:316`). Two HTMX regions at `/htmx/notes` and `/htmx/deploys` beside the live regions, against a vendored HTMX whose SHA-256 the program verifies at startup and **refuses to serve on mismatch** — a digest checked at run time rather than documented. Read in `examples/dashboard/main.go` by PM-1; QA-2 explicitly declined to grade this row as not theirs, and was right to |
| 5 | **Resync cost measured: bytes and latency for a full resync of the dashboard example** | **NOT MET** *(at this gate; **re-held MET** on 2026-08-05 at `713a3192` — **§12**, on DEV-3's remedy `1b16f4a9` and PM-1's own re-run)* | **§5.3.** A figure is published and it may not be quoted: `c1338120` rewrote the measurement program because BR-9 made its old request unanswerable, and the README's number *and its stated method* both predate that. Flagged first by QA-2 §R17 as DEV-3's; confirmed at this gate by PM-1 reading the two texts against each other |
| 6 | Client runtime still ≤12 KB gzipped | **MET** | **4,429 B** of 12,288 B — 7,859 B headroom, **64.0 %**, 10,391 B minified. The orchestrator's measurement at `73f5bf2f`; PM-1 checked `client/` is byte-identical to HEAD. `client/SIZE.md` agrees to the byte and attributes the last +69 B to U-1/U-2's snapshot-boundary check |
| 7 | **G2's memory baseline exists and RFC-0001 §6.2 is corrected in the same PR** | **MET on both clauses, with §3.6's unrun driver gate named inside the tick.** §5.1 | `docs/bench/g2-baseline.md` is four campaigns and 1,717 lines; RFC §6.2 is rewritten around the measurement with the estimate kept whole beside it, and §6.2.4–§6.2.6 carry the composition, X3's adoption and ADR-002's budget line. **DEV-1's measurement, QA-2's method, PM-1's grade.** What did not happen: §3.6's driver-validation gate, E1's second falsifier, and RFC §6.3's per-component profile at the shipping tree |
| 8 | **FR-36 clause 4 implemented and its falsifier is a spec that can fail** (C-30) | **MET, and I checked the falsifier rather than the claim** | Parent edge at `internal/session/actor.go:367` (`StartChildOf`, through the `SpanRef` the ingress already carried). Falsifier is `test/sampling` — its own module, its own `ci.sh` step at `ci.sh:402`, 300 interactions at p = 0.05 / 0.25 / 0.5 against a real `ParentBased(TraceIDRatioBased(p))` and a real SDK recorder, asserting zero partial graphs **plus two anti-vacuity assertions** (some sampled, some not, in the same run). Reverting the one line that makes `gotthlive.event` a child turns the run into 18 of 18 PARTIAL with 0 complete — C-30's shape exactly. The 0-of-300 this box quoted is now 12 of 300 complete, 0 partial. **DEV-1's landing; the spec read and the sites verified by PM-1** |
| 9 | **The five FR-36 spans that start nowhere** are started, or FR-36 comes back to PM-1 | **MET, by starting them** — the outcome the box preferred and did not assume | All five on the real path in non-test code, checked one at a time by PM-1: `parse` `internal/wsx/conn.go:228`, `reduce` `internal/session/actor.go:450`, `render` `:663`, `render.fragment` `:689`, `send` `:747`. **Starting `send` exposed a defect the missing span was hiding**: `Framer.Send` did validate, marshal and write in one call, so `gotthlive_encode_duration_seconds` and `gotthlive_send_duration_seconds` were two series equal by construction and one of them is the write-stall signal. `Framer` now splits `Encode` from `Write` |

**Two rows in that table are worth reading together**, because between them they
are the answer to a question this project asked twice. Rows 8 and 9 are the two
observability requirements that had been recorded unmet since v0.4 and unmoved by
checkpoint 2, and both were closed the same way: not by narrowing FR-36, which
was the alternative the box explicitly offered, but by building the thing. Row 9
then found a real defect — two metric series equal by construction, one of them
the write-stall signal — which is the second time on this project that an
unimplemented observability requirement turned out to be concealing a live one.
The first was C-30 itself.

---

## 4. Checkpoint 1 — the gate record that did not exist, closed here

**PRD §9 v0.6 row 6, in my predecessor's words:** *"there is no PM-1 gate record
for checkpoint 1 and ticking them would record a gate nobody held. That is now
debt with my name on it rather than a standing note, closed at the checkpoint-3
report."* This is that report, and this is the record.

**Phase 1's nineteen boxes are ticked.** The evidence is QA-1's
([`docs/qa/checkpoint-1.md`](../qa/checkpoint-1.md) §7.7), which re-issued every
CP1 verdict against `44f87764` after remediation and signed off in §7.10 —
*"QA-1 signs off checkpoint 1. The block is lifted."* Seventeen of nineteen were
PASS at that point. Two had moved from FAIL or PARTIAL to PASS by fixing the
thing. **One was PARTIAL and it is the only reason this record could not have
been written at checkpoint 2.**

### 4.1 CP1-16, and why it can be ticked now and could not be then

CP1-16 asks 10k connect/disconnect cycles to return **goroutines and RSS** to
baseline. The goroutine half ran and passed. RSS was never sampled — QA-1's
**D-10** — and QA-1 judged it non-blocking, handed it to QA-2 as a Phase 3 item,
and said why in §7.6: FR-22's own gate is QA-2, the leak class the criterion
exists to catch is covered and passing, and RSS-to-baseline belongs with
equivalence-spec §3.6's discipline rather than beside it as a weaker second
measurement.

**QA-2 closed it in checkpoint 3, and checked the claim rather than the commit
message** (chaos §3): `internal/wsx/wsx_test.go` now measures two signals —
`/gc/heap/live:bytes` after `debug.FreeOSMemory`, and RSS from
`/proc/self/statm` — both budgets derived in the source from stated
measurements, both asserted, the RSS reader **failing rather than skipping** when
`/proc` is absent, and the margins published through `AddReportEntry` rather than
only asserted. QA-2's own sentence is *"CP1-16 can be re-issued as met."*

**And they re-ran it at checkpoint-3 HEAD** (§R12.3), because three items of the
change set — C-34's registration and drain ordering, BR-8's admission
reservation, and `5a2ca417`'s new `hijack.go` — all landed in that package:

```
   100 cycles: live heap  3,528 B ( 35.3 B/cycle) against    268,544 B
   100 cycles: RSS      524,288 B (5242.9 B/cycle) against  8,798,208 B
10,000 cycles: live heap  3,672 B (  0.4 B/cycle) against    902,144 B
10,000 cycles: RSS   17,698,816 B (1766.6 B/cycle) against 49,348,608 B
```

So the criterion is met on both of its named signals, with the transport
rewritten underneath it. **That is the difference between now and checkpoint 2,
and it is a measurement rather than a decision** — which matters, because the
alternative reading of "close checkpoint 1 at checkpoint 3" is that time passed
and somebody got tired of the row.

**One thing the closure does not cover, stated because QA-2 stated it**: those
10,000 cycles are all *clean* closes. The abnormal-close soak is Phase 3's case 7
and is separately green at 10,000 cycles, goroutines 7 → 7, −0.1 B/cycle.

### 4.2 The nineteen, and the four that carry a qualification rather than a bare tick

| # | Criterion | QA-1's verdict | Ticked on |
|---|---|---|---|
| CP1-01 | Counter end to end in a real browser | PASS | QA-1 §7.3, in Chromium under CDP |
| CP1-02 | Lifecycle: handshake, auth, origin, heartbeat, close codes | PASS | QA-1. **D-24 and D-28** are carried defects against FR-51's *policy* — two close codes hard or impossible to reach — not against this box's enumeration |
| CP1-03 | Wire audit, 100 % parses as `Frame` | PASS | QA-1 |
| CP1-04 | Hostile wire data, typed errors, no partial application | PASS | QA-1. Strengthened since by D-4's single walk; its one behavioural consequence (first violation in *field* order wins) is a carried DEV-1 row |
| CP1-05 | Actor `-race` under concurrent injection | PASS | QA-1, and re-run under `-race` at every gate since |
| CP1-06 | Determinism helper exists; counter uses it | PASS | QA-1. **Qualified:** the helper does not catch an in-place reducer — `livetest.ReplayN`'s `fold` starts both replays from the same handle, so a mutating reducer compares the mutated object with itself. That is **BR-7 step 2**, carried to DEV-1 with its exact falsifier. The box asks that the helper exist and be used; it does, and the gap is on the record instead of inside the tick |
| CP1-07 | Repeated-render byte equality | PASS | QA-1 |
| CP1-08 | Event→paint measured and published, with the network path stated | PASS | QA-1 §4.1: **3.20 ms p50 / 4.80 ms p99** over 220 real-browser interactions, plus a **91.86 µs** p50 protocol floor over 300 samples, labelled loopback / one host / headless and **NOT PRD G1**. Still the newest latency figures in the repository at this gate — checked, not assumed |
| CP1-09 | Metrics flowing | PASS | QA-1, mutation-verified. **Qualified: D-22** — `gotthlive_sessions_active` counts down on rejected handshakes, reproduced at −50 by QA-2. Carried, MEDIUM, DEV-1 |
| CP1-10 | Traces flowing across the path | PASS (**D-12** escalated) | QA-1. **Met more strongly now than when QA-1 measured it**: the server-side path is one parent chain rooted at `gotthlive.parse`, the five drawn-but-unstarted spans are started, and the morph is a link rather than a parent for the reason FR-36 clause 4 gives. D-12 — FR-36's own self-contradictory sentence — was ruled at checkpoint 2 and its mechanism delivered here |
| CP1-11 | Provenance resolves; 100 %, zero unknown | PASS | QA-1, strengthened by D-1's two-sided fix |
| CP1-12 | Client ≤12 KB gzip with the ledger | PASS | QA-1: **3,874 B at that gate**. **Qualified: a dated record, not a live figure.** The live one is §3 row 6 |
| CP1-13 | No-eval green; strict CSP verified | PASS | QA-1 §7.3, in a real browser under a real policy |
| CP1-14 | Authorization before every reducer | PASS | QA-1 |
| CP1-15 | Cross-origin attack test | PASS | QA-1 |
| CP1-16 | 10k cycles: goroutines **and RSS** | **PARTIAL → MET** | **§4.1.** QA-2's closure, re-verified at checkpoint-3 HEAD |
| CP1-17 | Pre-generated proto; clean-machine build | PASS | QA-1, and re-run at every gate since — byte-identical to a fresh generation |
| CP1-18 | api-surface current; CI reports the delta | PASS | QA-1. One residual carried and named: §10's changelog does not cite `5a2ca417` (DEV-1, NIT) |
| CP1-19 | Toolchain clean incl. `staticcheck` | PASS | QA-1 §7.9, `ci.sh` exit 0. The **live** state of this gate is §2.1, not this row |

**Why the qualifications are in the table rather than in a note.** Four of these
boxes have a named open row underneath them. A tick that swallows the row is how
the row stops being found — which is the mechanism that produced this very debt:
checkpoint 1's boxes went unticked for two checkpoints precisely because nobody
wanted to record something they could not fully stand behind, and the cost of
that instinct was a phase with no gate record at all. The answer is not to tick
loosely; it is to tick and say what the tick does not cover.

**Checkpoint 1 is closed.** Nineteen criteria, nineteen met, on QA-1's
re-issued verdicts plus QA-2's closure of the one PARTIAL, with the gate record
being this section. Its debt column is empty: everything CP1 carried forward is
either closed above or is in §7 with an owner.

---

## 5. The three criteria that needed judgement

### 5.1 G2's baseline — MET, and the qualification is inside the tick

**Ruling: the box is met. It asks for a baseline and for RFC §6.2 corrected in
the same PR, and it got both. It does not ask for G2, it says so in its own
words, and nothing here may be read as G2 met.**

**The state, stated plainly and with the owner on every number.** DEV-1's
`docs/bench/g2-baseline.md` §9.10 measures the tree this PR ships at
**45,768.7 B/session** pooled over five runs of N = 1000 Idle with TLS outside,
against the **46,080 B** gate — under it by **311.3 B, 0.68 %**. Two of the five
runs are individually over. The cell's own run-to-run spread is 5.5 %, and the
same unchanged `ce52d2f9` tree measured 7.2 % apart across three campaigns.
DEV-1's own conclusion is the one I am adopting rather than softening: **the tree
is *at* the gate, not clear of it.** §4 of that document measured 79 % above it
and §9.4 measured 41 % above it, so this is a large and real change and it is a
different claim from clearing a threshold.

**Why the box is met anyway, and the argument does not read on the number.** The
box's headline is *"G2's memory baseline exists and RFC-0001 §6.2 is corrected in
the same PR."* Both are delivered. The estimate the box exists to replace — 42,416 B
with two estimated lines inside it — is replaced by a measurement; RFC §6.2 is
rewritten around it with the estimate kept whole and unedited beside it so a
reader can see how far off it was; §6.2.4 carries the composition, §6.2.5 X3's
adoption, §6.2.6 ADR-002's budget line. The box's own closing sentence is *"a
figure still estimated at this gate blocks any Phase 5 memory number being
quotable"*, and that blockage is lifted. **I would grade this row identically had
the figure come in at 60,000 B or at 30,000 B**, which is the test PRD §9's
preamble now states, and it is the reason I am comfortable ticking a box whose
number is inconvenient in both directions at once.

**What did not happen, in §3.6's own words rather than in a summary of them.**

| Not done | §3.6 / RFC's own consequence | Owner |
|---|---|---|
| **The driver-validation gate** — per-session memory with **10 real Chromium tabs** against **10 synthetic sessions**, on both stacks, driver fixed if they differ by more than 10 %. §3.6 marks it *"mandatory before any 1k number is quoted"* and **four campaigns have run it none of them** | *"Without this, the 1k number is an assertion about a synthetic client, not about sessions."* Every figure in §9.10 is that kind of assertion, including 45,768.7 B | **QA-2** (method) + **DEV-1** (run), before Phase 5 quotes G2 |
| **E1's second falsifier** — the N = 100 sub-linearity cell, ±15 % | Tripped at −15.6 % in §4 and **not re-measured by `c1`, `c2` or `c3`**. Its status at the shipping tree is *unknown*, and DEV-1 correctly refuses to call it either cleared or still tripped | DEV-1 |
| **RFC §6.3's per-component heap profile at `d66e4953`** | The −23,904 B of §9.10.7 is a 54-commit delta and **is not attributed line by line**; no share is estimated. It is a run, not a build — `memsrv` still exposes `/heapprofile` | DEV-1 |

**So the tick carries all three, and the PRD box carries them in its own text.**
That is the difference between a qualified pass and a quiet one: a reader who
reaches the ticked box in the PRD meets the driver gate there, not here.

**One thing I want on the record about DEV-1's document, because it is the
opposite of the failure mode this project keeps finding.** §9.10.9 is titled *"The
margin is smaller than the method's own resolution, and that is the result"*, and
it publishes the labelled forced-GC floor — 35,029.0 B, tighter *and* lower than
the headline, and under the ratchet threshold — while explaining at length why
quoting it instead would be a disqualifying method error. A document that
volunteers the number that would have flattered it, beside the reason it may not
be used, has earned the benefit of the doubt on the numbers it does quote.

### 5.2 Case 8 — MET on the behaviour, and the documentation clause moves to a box that can hold it

**Ruling: the Phase 3 box ticks on the behaviour. FR-77's documentation half
becomes a Phase 4 exit criterion, gated by QA-1, per FR-77's own phase and gate
line. Applied in PRD v0.7 §9 row 3. This is a correction to my own drafting, and
it is not a descope.**

**What the defect was.** v0.6 struck case 8's second clause on QA-2's escalation
and wrote into the same Phase 3 box: *"and the contract MUST be documented per
**FR-77**"*. In the same landing, FR-77 was created with the phase line
`Phase: 1 onward (behaviour), 4 (documentation)` and the gate line `Gate: QA-2
(semantics), QA-1 (docs)`. **Those two sentences cannot both be satisfied**: a
Phase 3 exit box required a Phase 4 deliverable gated by a different owner. My
predecessor saw the shape of it and deferred — *"a Phase 3 box closed against a
requirement whose delivery is owed in the next phase should be closed by the gate
report that can see both"* — which is this report.

**Applying PRD §9's own test, since it is being written down in the same
landing.** The argument for moving the clause is FR-77's phasing, fixed on
2026-08-04, before any checkpoint-3 measurement existed, and it does not read on
any number: it would be word-for-word the same had the documentation happened to
be written. That is invariant to the measurement's outcome, so the clause may
move. If it were not — if the only reason to move it were that moving it makes
the box tick — it would stay and the box would not.

**What I did not do is drop it, and I measured before deciding that.**
`docs/guide/effects-and-server-push.md` carries one sentence of FR-77(b): *"An
effect may have executed even though the user never saw its result. Patches are
exactly-once and ordered; events are at-most-once."* That is the **second** of
the two double-execution paths. It does not state FR-77(a)'s contract in
FR-77(a)'s words — two byte-identical frames are two events and the library must
not deduplicate — it has no worked idempotency-key example on an effect that
moves money, and FR-59's "when not to use this" page, where FR-77(c) puts the
bound, **does not exist**. So the clause is not substantially done and the
honest disposition is a box of its own rather than a sentence in a box that was
about to be ticked. It is in Phase 4 now, with DEV-3 named and QA-1 gating.

**What the box ticks on.** One `Event` frame's bytes sent twice moved
`state_version` 2 → 3 and ran the effect twice, asserted directly so that adding
deduplication goes red — which is what FR-77(a) requires of a test. All five
replay properties that *are* defences pass, and one of them is materially
stronger than it was: QA-2 found their own H-11 spec asserting the reverse of its
title after BR-1 inverted the mechanism, demonstrated it by running the same
unmodified spec against the library **and its exact inverse** and getting two
greens, filed it as **D-32** against themselves, and replaced it with three arms
carrying three separate falsifiers. **That is the most valuable single thing in
this checkpoint** and L9-1 said so independently.

### 5.3 The resync cost — NOT MET, and what would tick it

**Ruling: the criterion is not met. I am not moving it, not re-wording it, and
not measuring it myself.**

The criterion is *"Resync cost measured: bytes and latency for a full resync of
the dashboard example."* A figure exists in `examples/dashboard/README.md`:

```
bytes on the wire, per snapshot
  frame:  min 2220  p50 2377  p90 2660  max 2937   (n=200)
latency, request written to snapshot read
  resync: min 97µs  p50 163µs  p90 259µs  max 1.309ms   (n=200)
```

**Why it does not count, checked by me at the gate rather than taken from QA-2's
flag.** QA-2 recorded in §R17 that the README is byte-identical at `ce52d2f9` and
at `1864cf92` while `resync.go` was rewritten in between, and correctly declined
to grade a row that is not theirs. I checked both halves and found the sharper
version of it:

- `git diff ce52d2f9 495c7855 -- examples/dashboard/README.md` is **empty**.
- `git diff ce52d2f9 495c7855 -- examples/dashboard/resync.go` is **+101 / −40**,
  all of it `c1338120`.
- **The README's *method* paragraph describes the program that was replaced.** It
  says *"`last_applied_seq=1` on every request, so every snapshot supersedes the
  whole session"*. `resync.go` at HEAD does the opposite on purpose, and says so
  in a comment: it holds one patch back so the gap is **real rather than
  claimed**, because *"the server now clamps the claim up to what it already
  knows … so a caught-up client can no longer ask for a snapshot by understating
  the field"*.

So this is not a stale number beside a correct method. It is a document whose
stated method describes a program that no longer exists, and whose numbers were
produced by a frame `c1338120`'s own body says *"is one a browser would have hung
up on"*. **The snapshots the fixed harness measures supersede a tail range rather
than the whole session** — the code argues the byte difference is two varints,
and that argument may well be right, but "probably still roughly correct" is not
a measurement and this project does not publish one.

**What ticks it. Owner: DEV-3.**

1. `go run . -resync-cost 200` in `examples/dashboard`, in
   `dis-gotth-live:latest`, at the tree that ships, with the host state stated.
2. The README's method paragraph rewritten to the request the harness now sends —
   one held-back patch, a real gap, one feed interval per sample — so that the
   next reader can tell which program produced the numbers.
3. Both halves in one landing. A number without its method is what created this
   row.

**And it is enforced in two places on purpose.** The Phase 3 box stays open, and
Phase 4's "all three examples polished, documented, and green in CI end-to-end"
box now names this measurement explicitly. The checkpoint-2 lesson is that an
obligation owed by a phase and enforced by nobody goes missing twice; an
obligation that has just been *found* missing is exactly the one to enforce
twice.

**Why I did not simply run it.** §2.5. Briefly: it is a latency measurement and
the host was running the gate; and the number belongs in DEV-3's file beside a
method paragraph that has to change in the same landing, so putting a second copy
in a gate report while the first copy stays wrong would be this project's
signature defect committed deliberately.

**RE-HELD 2026-08-05 — the ruling above is superseded on its own terms, and the
terms were the three numbered items. The criterion is MET. §12 holds the
evidence, condition by condition, and this section is not rewritten to match it.**
The heading still says NOT MET because that is what this section ruled at the
checkpoint-3 gate and the ruling was correct on the day it was made. What ticks
the box is §12, held by PM-1 at `713a3192` on DEV-3's remedy `1b16f4a9`.

---

## 6. The scope decisions this gate leaves open — and the one it closes

### 6.1 G2's remedy — restated, not closed, and it is smaller than it was

My predecessor recorded in [`checkpoint-3-scope.md`](../pm/checkpoint-3-scope.md)
§4: *"A measured miss of this size is a scope decision and nobody has taken it:
cut the attributed cost, accept the miss with an ADR carrying the measurement, or
change what v0.1 claims."*

**That question no longer describes the tree, and the reason is engineering
rather than a ruling.** RFC §6.1.2's first branch — attribute the overage to a
named line and engineer it down — was taken, across `9f88d75e`, `5a2ca417` and
the fifty-odd commits that did not undo them: 82,104 → 69,673 → 45,181 → 45,769 B
by arm. The largest attributed term, default-on observability, **now has a budget
line inside the gate**: ADR-002, APPROVED WITH CONDITIONS by L9-1, at 4,050 B per
session, landed into RFC §6.2.6 as a **sub-line of the 46,080 B gate and not an
allowance beside it**. Escalation was not withdrawn; it was ruled.

**What is left of the decision, and it is mine.** It is narrower and it is real:
**whether v0.1 publishes a G2 figure at all while §3.6's driver-validation gate
is unrun.** G13 puts a server-memory-per-session row in a published head-to-head
against Next.js under FR-73's honesty clause. Today the only figure available for
that row is, in §3.6's own words, an assertion about a synthetic client. I am not
deciding it here — it is Phase 5's, the driver gate may well have run by then, and
deciding now would be deciding without the input that matters. **It is recorded as
PM-1's, open, with its trigger: the first draft of the G13 table.**

### 6.2 I6 — QA-2 has given me a range, and a range is what I will publish

QA-2 hands PM-1 and instrumentation **I6** the provenance-log throughput figure,
and hands it as a range rather than a point: **≈1.5 to ≈3 CPU-cores at D3's
N = 1000 × 53 updates/s**, because the same unmodified spec on the *same tree*
measured a factor of two apart depending on host load, demonstrated ABBA against
`ce52d2f9` to show that the host and not the change set did it.

**Accepted as a range, and the range is the answer rather than a caveat on one.**
A point figure here would be a number whose value is a property of the machine it
was taken on, published as a property of the library. What the range supports is
the conclusion QA-2 already draws and I am adopting: at the dashboard's realistic
per-session rate the log costs **0.15 % of a core per session**, two orders of
magnitude inside NFR-1's ≤5 %, so this is not an NFR-1 problem; at D3's N = 1000
it is one to three cores of dedicated CPU, which is an **operational capacity
statement** and belongs in the operator-facing text as one, with both ends and
the load at which each was taken. **PM-1, carried to the instrumentation §8 I6
row.** Nothing at this gate turns on it.

---

## 7. What carries forward, with owners

**Nothing in this section blocks checkpoint 3.** L9-1's §10.5 states that there
are **no conditions on this gate** and that a row here *"is not a condition and
should stop being re-litigated as one"*. I am carrying that framing forward
verbatim, and adding the rows this gate produced.

| Item | What it is | Owner | Where it is enforced |
|---|---|---|---|
| **The dashboard resync cost** | §5.3. The measurement, and the README method paragraph, in one landing | **DEV-3** | **Phase 3 box, open** + a named Phase 4 box — ***DISCHARGED 2026-08-05*** at `1b16f4a9`, box re-held **MET** by PM-1 at `713a3192`, **§12** |
| **FR-77's documentation half** | §5.2. The effects page's two paths and a money-moving worked example; the "when not to use this" bound | **DEV-3**, gated by **QA-1** | **New Phase 4 box**, PRD v0.7 |
| **§3.6's driver-validation gate** | 10 real tabs against 10 synthetic, both stacks, ±10 %. Mandatory before any 1k number is quoted; four campaigns, none run | **QA-2** (method) + **DEV-1** (run) | Before Phase 5 quotes G2 |
| **E1's second falsifier** (N=100 sub-linearity) | Tripped at −15.6 % and not re-measured at the shipping tree | DEV-1 | Phase 5 |
| **RFC §6.3's per-component profile at `d66e4953`** | Would attribute the −23,904 B line by line; a run, not a build | DEV-1 | Phase 5 |
| **C-45 … C-49** | L9-1's five non-blocking conditions from the X3/ADR-002 rulings: the read-pump stack via `memsrv -probe`; the per-connection `context.WithCancel` line; the observability-off cell at five runs; RFC §6.2's retained-state composition row; `spawn`'s godoc | **DEV-1** | L9-1 §10.5, all LOW |
| **D1 condition 1's wording** | `d66e4953` records `go.opentelemetry.io/otel` as a **direct** require. Condition 1 says "never the root module" and pre-registered the `otel/attribute` exception that made it unavoidable. Two readings; **PM-1 does not choose** | **L9-1** (the condition) | `dependencies.md` §1.4, §5.4, §7 D1. Before Phase 5, where the ledger is an L9-1-gated deliverable |
| **`dependencies.md` obligation re-quotes** | §5.4.2: which module the build list's +1 is, obligation 2's binary size, and condition 3's +6/+232,809 B tracing delta | **DEV-1** | `dependencies.md` §5.4.2 |
| **Q-BENCH-1's conformance question** | §2.1 F-CTR-1 says the counter is per session; both stacks' bench counters are global. An **E1 conformance question against §2**, not a fairness one | **QA-2** | Before Phase 5 collection. §8.3 |
| **BR-9's `lastSnapshotSeq` floor has no falsifier** | L9-1 found it auditing QA-2's mutations: the suite falsifies the clamp *as a whole*; the acked floor alone produces the measured result, so nothing isolates the snapshot floor. The exact mutation and the spec shape that would settle it are named in §10.3 of the review | **DEV-1 + QA-2** | `internal/session`, not the chaos suite. Test-coverage debt against a fix that is correct in the tree |
| **D-22, D-24, D-26, D-27, D-28** | Reproduced at HEAD by QA-2, graded by them and by L9-1 as not conditions. **D-26 is the one to schedule first** — an eviction that cannot fire against a client that acknowledges is a policy that does not exist | DEV-1 (D-26 also L9-1/PM-1) | §3 rows 1d–1f name each beside the criterion it touches |
| **D-25** | RFC §8.5's one-directional at-most-once leak; its evidence here is a printed number whose value is a race (14, then 13, then 0) | PM-1 + DEV-1 | LOW |
| **D-31 / C-41** | The client's resync retry schedule is protocol-visible and appears in neither RFC §7.6 nor §8.4 | **L9-1**, who took it | Owed before `protocol.md` is called complete, explicitly **not** before this gate |
| **REV-DEL 3** | `instrumentation.md` asserts a field/attribute/branch deletion that has not happened in `internal/obs/metrics.go`; the document half landed alone | DEV-1 | L9-1's §10.4 forbids the claim meanwhile |
| **REV-DEL 6, 11, 12; U-7; U-8; D-3's `test/routers`** | Engineering rows with reproductions, none changing exported surface or contradicting an approved document | DEV-1 / stream owners | L9-1 §10.5 |
| **BR-7 step 2** | `livetest.ReplayN` cannot catch an in-place reducer, and `live/config.go`'s godoc now tells applications it can. The spec that settles it is named | DEV-1 | CP1-06's qualification, §4.2 |
| **REV-DUP §7.2, api-surface §10's changelog, `Claude Fable 5` footers** | Three nits from the closure ledger, all one line each | whoever lands the next commit in each file | §10.2 |

**Open by design, and not debt.** NFR-7's six unverified browser cells are *out
of scope for v0.1* with the obstruction measured per cell; R-8 is *accepted and
unmitigated*, a disclosed position rather than a missing control; and D-7, D-10
(REV-DUP's), R-1…R-9 are recorded decisions not to fix, with reasons, so a future
sweep does not re-derive them.

---

## 8. The sign-offs, carried explicitly

### 8.1 QA-2 — PASS at `1864cf92`

Recorded in [`docs/qa/checkpoint-3-chaos.md`](../qa/checkpoint-3-chaos.md) §R18.
This is the re-verification L9-1's C-35(c) required, and it discharges it: there
is now a QA-2 sign-off covering `5a2ca417`, C-34, BR-1…BR-9, U-5, U-6, D-4 and
the `livetest` extraction, naming **which of §R8's rows each change-set item
could move and which one did**.

**Their affirmation is not generic and I am carrying its substance.** The suite
was re-run on a clean export with both cost classes on — 42 of 42 under `-race`
— and the analysis did not stop at the four cases review-checklist §8.6 named as
the likely surface. **One row moved**: case 8's H-14 replay, 3 snapshots → 0, and
it was **attributed by mutation rather than argued** — MH1 brings the 3 back and
nothing else does, corroborated independently by `c1338120` diagnosing the same
mechanism one directory over.

**Three declared coverage gaps, all three verified real by L9-1** and stated by
QA-2 rather than implied: BR-8's process-limit half (`MaxSessions` is unbounded
in every chaos configuration, and the `internal/wsx` spec that covers it is
deliberately concurrent because a serial one passes against the defect), BR-3's
`Commit`/`Discard` branch, and D-4's rejection-reason ordering. **An honestly
stated gap is worth more than a green cell**, and three of them in one pass is
the reason this PASS is worth signing against.

**One defect, and it is theirs against themselves.** D-32, MEDIUM: the H-11 spec
asserted the reverse of its own title after BR-1 and could not tell. Found by its
own author, published with the two-greens repro, graded MEDIUM rather than LOW
for the right reason (H-11 is a *defence*, and BR-1 deliberately widened what the
ring resolves), and fixed as three arms with three separate falsifiers. **No new
library defect was found in this pass.**

### 8.2 L9-1 — APPROVED, no conditions on this gate

Recorded in [`docs/reviews/checkpoint-3.md`](../reviews/checkpoint-3.md) §10. The
BLOCK's three items are all discharged — C-33 by the fixture-less gate run, C-34
by reading the ordering in the tree and QA-2's re-run, C-35 in all three parts —
and **five conditions filed the previous day plus every open engineering row are
carried forward with owners and explicitly marked not-conditions**, which is the
split I asked for and got.

**Two things about this re-review are worth carrying into the record rather than
citing.** L9-1 **ran nothing**, for the same reason §2.5 gives, and said so at
the top rather than letting a reader assume otherwise. And they audited QA-2's
falsifiers instead of admiring their candour, which produced the one finding of
the round: **MH1's mutation is larger than its own row label says** — it removes
the acked floor as well as the snapshot floor — and the arithmetic that follows
shows the acked floor alone produces the measured 0, so **nothing in that suite
isolates BR-9's `lastSnapshotSeq` half**. That is coverage debt against a fix
that is correct in the tree, it is carried in §7, and it is exactly the class of
finding that only appears when somebody checks a green result.

**Their §10.4 MAY-NOT list is the binding instrument on this report**, and it is
longer than the BLOCK's was — *"not because the batch got worse, but because
three campaigns of measurement and one honest QA-2 pass have produced more claims
that are nearly true."* Every one of its five new prohibitions is honoured above:
G2 is not claimed met (§5.1), `instrumentation.md`'s metric-set claim is carried
as REV-DEL 3 (§7), the dashboard resync figure is not quoted and its box does not
tick (§5.3), QA3-3 is carried as a range (§6.2), and BR-9's unfalsified half is
in §7.

### 8.3 PM-1's signature on the equivalence spec — given here, in terms QA-2 can act on

`docs/bench/equivalence-spec.md` §12 freezes §2, §3, §5, §7 and §8's row set on
**L9-1 + PM-1 + QA-2** sign-off. L9-1 signed in their §10.6, with the words *"so
that the status line waits on PM-1 and not on me"*. My predecessor's §7.1 found
the state this leaves: **the document four others treat as binding says of itself
that it is a draft.** So:

> **PM-1 signs off `docs/bench/equivalence-spec.md` for the freeze under §12, on
> the gate PM-1 holds there — product-surface equivalence — as of 2026-08-05, at
> version 0.2 including amendment A-1.** The sign-off table row reads:
> `PM-1 (product-surface equivalence) | PM-1 | signed | 2026-08-05`.

**What I am signing, precisely.** That §2's feature tables, interaction lists and
data volumes describe **the same product surface on both stacks**, and that §3's
operational definitions of "paint", "interactive" and "active session" are the
same words meaning the same things on each side. That is the gate the spec's own
header assigns me and it is the whole of what my signature covers.

**And I accept A-1**, the TLS-boundary amendment, which Phase 0's exit criterion
left as *"PM-1 acceptance pending per C-5"*. It makes the contract **harder** for
gotth-live rather than easier — it forecloses an ~18,000 B asymmetry that ran
*against* us — it moved before any number existed, and it adds an assertion the
harness checks rather than a rule nobody verifies. I have read L9-1's §10.6
approval of the **C-43** amendment (AS-8, and AS-3's "same visible behaviour"
qualified) and have **no product-surface objection**: recording two declared
asymmetries inside the closed register is the register doing its job.

**What my signature does *not* cover, stated so it cannot be read wider.**

- **Not the method.** §3.6's measurement procedure is QA-2's gate and L9-1's
  fairness veto, not mine.
- **Not a claim that the built apps conform to §2.** They may not: **Q-BENCH-1**
  is open — §2.1 F-CTR-1 says the counter's state is per session and both
  stacks' bench counters are global. That is an **E1 conformance question about
  the applications**, not a defect in the specification, and it stays QA-2's.
- **Not a waiver of §12.** Freezing now means that if Q-BENCH-1 resolves by
  changing §2 rather than by changing the apps, it costs an amendment-log entry
  and L9-1's approval. **That is correct and it is the cheapest it will ever
  be**: `bench/data/` contains no run ids, so the log's *"measurement taken under
  old text?"* column still reads **no**, and this is the last moment that is
  true.

**Why signing is the right act and not merely the convenient one.** Nothing in
either review argues the spec is unready; L9-1's only stated reason for not
signing was that they had not. Meanwhile the PRD, `OPERATOR-QUESTIONS.md`,
`api-surface.md` and my own predecessor's scope pass all describe §2 as frozen,
and one of them says *"PM-1 may not amend a frozen spec"* — so four documents
have been relying on a freeze that formally did not exist. The choice was to sign
it or to make four documents stop saying frozen, and the second would be
weakening a fairness contract to match a missing signature.

**The file is not mine and I have not touched it.** QA-2 owns it and is landing
C-43's §12 amendment and the status line as this report is written. **This
section is the input; the status line moves when QA-2 applies it.** Until it
does, L9-1's §10.4 prohibition stands and checkpoint 3 may say only that §2 is
treated as frozen in practice.

---

## 9. What closed this round

| | | Where |
|---|---|---|
| **The nine BROKEN invariants** | BR-1…BR-9, every one with at least one Ginkgo spec and a stated before/after. The largest single body of work in the checkpoint | closure ledger §3 |
| **C-33** | `os.IsNotExist` does not unwrap, so six fixture-skip guards never fired; **and** the gate printed a skip it had predicted from file presence rather than observed. Both halves; running it found a third defect — the counter was announcing a skip it has never had | `ebc2da8f`, `99b769be` |
| **C-34** | `App.Close` reported a successful drain over sessions it never touched, 32/300 measured by L9-1. Closed **structurally** — `register` refuses under `Close`'s own mutex — not narrowed. 35/300 before, 0/300 after | `ed9f73b6` |
| **C-35** | (a) X3 re-derived and **adopted at 13,759 B** by L9-1, with the write buffer as a fifth line X3 never had; (b) RFC §3.4's false sentence corrected from the tree; (c) discharged by QA-2's re-verification | `ae61f325`, `ead612c5`, §R18 |
| **C-36 / C-37** | The transport's memory saving was silently conditional on a `ResponseWriter` shape and on a peer's pipelining. Both answered against their falsifiers, with the adversarial figure stated beside the benign one | `0929bf5a`, `42f197ff` |
| **C-39** | `Fixed1` rounded negative ties the wrong way **and its own spec asserted the wrong value**. Cross-checked against node v24 over all 79,400 deltas the fixture's domain produces | `ebc2da8f` |
| **C-40** | FR-70's two mechanical clauses are a `ci.sh` step over the three bench modules, mutation-verified both ways, computing imports from the modules' own lists rather than `go list -deps` — and **naming the two clauses it does not check** | `597902f7` |
| **C-44** | The three bench Go modules enter `dependencies.md` at the tier their quarantine defines | `d7568355` |
| **ADR-002** | Default-on observability has a per-session budget line for the first time: 4,050 B, **inside** the gate rather than carved out of it. APPROVED WITH CONDITIONS, with §3.1's derivation clause refused and replaced | `ead612c5`, RFC §6.2.6 |
| **D-10 / CP1-16** | The leak test asserts RSS as well as goroutines, verified rather than believed, and re-verified at HEAD | chaos §3, §R12.3 |
| **D-23, D-29, D-30** | Three QA-2 defects from earlier in the checkpoint, each closed and then attacked | chaos §R3, §R4, §R10 |
| **D-32** | QA-2's own spec that could not fail, found by its author and replaced with three falsifiers | `34945818`, §R15 |
| **FR-36 clause 4, and the five unstarted spans** | The two observability debts recorded unmet since v0.4 | `22ee4b15`, `35eb24a4` |
| **G2's baseline, and RFC §6.2** | Four campaigns; the estimate replaced and kept beside the measurement | `docs/bench/g2-baseline.md`, RFC §6.2 |
| **C-42** | The distinguishing test for striking a criterion, into the PRD §9 preamble against §6.1.2 by name | **this landing**, PRD v0.7 |
| **Checkpoint 1's missing gate record** | §4 | **this landing** |
| **`dependencies.md`'s owed obligation re-quote** | Root build list 61 → **62**, direct set published, three named gaps | **this landing**, §5.4 |
| **The equivalence-spec freeze** | L9-1 §10.6 + **PM-1 §8.3**; QA-2 applies the status line | **this landing** |

---

## 10. Two things I found at the gate

### 10.1 My own G2 bullet became false in the flattering direction while nobody was reading it

**PM-1. Fixed in this landing.**

PRD v0.6 restated §3's G2 bullet and R-10 from *"nothing measured"* to *"measured,
and the target is missed"*, and wrote — deliberately, with the reasoning in the
amendment log — that the figure itself would **not** be copied into the PRD,
because DEV-1 had a re-measurement in flight and a second copy of a moving number
is how a previous failure repeats. That was the right call and it worked: no
stale figure entered the PRD.

**What went stale instead was the sentence around the absent figure.** The bullet
said the baseline *"comes in **well above** the 46,080 B gate"*. At the tree this
PR ships it comes in **under** it, by 0.68 %. The prose carried a claim the
document had explicitly refused to carry as a number, and the claim outlived its
measurement by exactly as long as it took DEV-1 to finish.

**Three reasons this is worth a numbered item rather than a silent fix.** It is
the house defect class — a document asserting something nobody re-derived — with
one twist that makes it *harder* to catch than the usual instance: **it runs in
the direction that understates our own position**, and a sentence that is unkind
to us attracts no complaints and no second reader. It shows that "do not copy the
number" is a necessary discipline and not a sufficient one: the qualitative
sentence is a number too, at one bit of precision, and it needs the same
treatment. And the fix is not simply to flip the adjective — the corrected bullet
now carries the three things a reader needs *before* quoting any figure: the
margin against the method's own resolution, the unrun driver gate, and E1's
unre-measured second falsifier.

### 10.2 Three carried nits are the same nit, and it is worth naming once

**Low, and not blocking anything.**

The closure ledger's §7 turned up three items that look unrelated and are not:
REV-DUP's own summary table still reads SPECIFIED for a finding that landed
(`4e0f780b`); `docs/api-surface.md` §10's changelog does not cite the commit its
own row is about; and 27 of 50 commit footers in one range read `Claude Fable 5`
against a stated convention of `Claude Opus 5`, with three carrying no trailer at
all.

**All three are a ledger that describes work correctly in one place and
incorrectly in another**, and each is one line. They are recorded together
because the individual severity is nil and the *pattern* is the thing this
project spends most of its review budget catching. QA-2 counts the
specs-that-cannot-fail instances and reached **six** at D-32 — C-21's unread
`total`, D-19's `clean` printed without `gofmt`, D-20's suite that was green
because it never ran, C-33's skip that never skipped, the `Fixed1` table that
asserted the bug, and D-32 itself. These three are the same disease in the
documents that describe the work rather than in the tests that check it, and
§10.1 is a fourth. The convention question is the only one needing a decision
rather than an edit:
the minority spelling is now the majority, and it should be **adopted or dropped
rather than drifting a third time**. Whoever lands the next commit in each file.

---

## 11. Exit statement

**Checkpoint 3 is closed with carried debt, checkpoint 1 is closed with it, and
Phase 3 does not exit.**

> ***Superseded on its last clause only, 2026-08-05: PHASE 3 EXITS.*** The box
> this statement was written around was re-held and met — **§12**. The rest of
> the sentence stands, and the sentence is not rewritten.

Seventeen Phase 3 exit criteria, sixteen met, each individually checked against
evidence with its owner and its commit named. *(Sixteen **at this gate**. The
seventeenth was re-held and met on 2026-08-05 — **seventeen of seventeen**, §12.)* Nineteen Phase 1 criteria, nineteen
met, on QA-1's re-issued verdicts plus QA-2's closure of the single PARTIAL —
which is the first PM-1 gate record checkpoint 1 has ever had, and it exists
because the thing that kept CP1-16 open got measured rather than because time
passed. QA-2's PASS covers the transport change and everything after it; L9-1
approved with no conditions on this gate. The three debt boxes v0.5 opened
because they were *owed by a phase and enforced by nobody* are all three
delivered — which is the clearest evidence this project has produced that the box
mechanism works, and it is the reason the two obligations found missing at *this*
gate both leave here with boxes of their own.

**The seventeenth criterion is not met and I want the shape of that on the
record**, because it is the smallest item in this report and the one I am most
confident about. The dashboard's resync figure could have been ticked three
ways: by quoting the published number, by running the measurement myself under
contention, or by reading "measured" loosely enough that a number produced by a
superseded program still counts. All three were available. The criterion says
"measured" and the document describes a program that does not exist, so the box
stays open, PRD §6's rule keeps Phase 3 open with it, and the remedy is one
command and one paragraph with DEV-3's name on it. **A gate that cannot fail on
a small thing does not mean anything when it passes on a large one** — which is
the same sentence this repository has now written about a skip that never
skipped, a suite that was green because it never ran, a table that asserted the
bug it existed to prevent, and a spec that passed against a library and its exact
inverse.

**What I am least comfortable signing** is, as at checkpoint 2, not in the
criteria. G2's baseline exists and it is a very large piece of honest work — but
the tree measures **at** the gate rather than clear of it, by a margin an eighth
the size of the cell's own spread, and equivalence-spec §3.6's driver-validation
gate has been mandatory since Phase 0 and has been run by none of four campaigns.
So the number that will one day be published against Next.js under FR-73's
honesty clause is today, in the specification's own words, *an assertion about a
synthetic client rather than about sessions*. Nothing in this report claims
otherwise, the PRD box says so inside its own tick, and the gate for it is Phase
5's. **It needs ten browser tabs and a morning, and it should not wait for Phase
5 to discover that.**

**What went right, and it is a mechanism rather than a result.** L9-1 blocked
this checkpoint on three items and every one was closed by making the thing true
rather than by arguing the sentence. QA-2 ran an unmodified spec against a
library and its exact inverse, got two greens, and reported it as a defect of
their own. L9-1 then audited the falsifiers behind a PASS they were about to
grant and found a half-invariant nobody covers. DEV-1 published the number that
would have flattered the campaign beside the reason it may not be used. **None of
those four is a review catching a mistake; all four are somebody checking a green
result.** That is the habit this project should be measured by, and it is the one
worth carrying into Phase 4.

**Phase 4 may begin. Phase 3 exits when DEV-3 re-measures the resync cost.**

> **2026-08-05, beneath the sentence it corrects: DEV-3 re-measured it at
> `1b16f4a9`, PM-1 re-held the box at `713a3192`, and PHASE 3 HAS EXITED.**
> Seventeen of seventeen. The gate act, its evidence and the one figure that did
> not reproduce are **§12**. This exit statement is left standing as written
> because the condition it names was the right one and it was met — which is the
> only way a sentence like it is worth writing.

— PM-1, Product Manager, 2026-08-05

---

*Reproduce this report:*

```bash
REPO_ROOT=<repository root, not gotth-live/>

# the whole gate — bash -c, NOT bash -lc (a login shell strips the Go toolchain)
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live -e GOFLAGS=-buildvcs=false \
    dis-gotth-live:latest bash -c 'bash ci.sh; echo "CI_EXIT=$?"'

# C-33(a)'s condition: the same, on an export with no bench fixtures
git archive HEAD | (mkdir -p /tmp/ci-fresh && tar -x -C /tmp/ci-fresh)
# then mount /tmp/ci-fresh instead of the worktree

# the chaos suite as QA-2 ran it, with both cost classes on
docker run --rm --cpuset-cpus=24-27 --memory=4g -v "$REPO_ROOT:/w" -w /w/gotth-live \
    -e GOFLAGS=-buildvcs=false -e GOTTHLIVE_SOAK=1 -e GOTTHLIVE_MEASURE=1 \
    dis-gotth-live:latest bash -c 'go test -v -race -count=1 -timeout 35m \
        ./test/internal/chaos/ -args -ginkgo.fail-on-empty'

# CP1-16's two signals, and case 7's package
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live -e GOFLAGS=-buildvcs=false \
    dis-gotth-live:latest bash -c 'go test -v -race -count=1 ./internal/wsx/'

# FR-36 clause 4's falsifier, in its own module
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live -e GOFLAGS=-buildvcs=false \
    dis-gotth-live:latest bash -c 'cd test/sampling && go test -v -race -count=1 ./...'

# the browser cell NFR-7(b) gates on
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live \
    dis-gotth-live-bench:latest bash -c 'GOTTHLIVE_E2E=1 go test \
        ./test/internal/conformance/ -count=1 -v -timeout 30m -args \
        -ginkgo.label-filter=browser -ginkgo.fail-on-empty -ginkgo.no-color'

# the client suite (NFR-4) and the size gate
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live \
    dis-gotth-live-bench:latest bash -c 'for f in client/test/*.test.mjs; do \
        node --test "$f"; done'

# the root build list §5.4 re-quotes
docker run --rm -v "$REPO_ROOT:/w" -w /w/gotth-live -e GOFLAGS=-buildvcs=false \
    dis-gotth-live:latest bash -c 'go list -m all | wc -l'

# what §2.2 checked, and it needs no toolchain at all
git diff 1864cf92 HEAD -- live internal client proto   # QA-2's tree vs this one
git diff 73f5bf2f HEAD -- client                       # the size figures' tree
git diff 4c4b751a HEAD -- . ':!docs'                   # what the pending run covers
git diff ce52d2f9 HEAD -- examples/dashboard/README.md # §5.3, and it is empty
```


---

## 12. The Phase 3 exit gate act — held 2026-08-05, and the box is MET

| Field | Value |
|---|---|
| What this is | **A gate act on Phase 3's seventeenth exit criterion**, the resync-cost box, which §5.3 of this report left open and which has stayed open through PRD v0.8, v0.9, v1.0, v1.1, v1.2 and v1.3 |
| Held by | **PM-1**. §5.3 named the remedy DEV-3's and the re-hold PM-1's, and [`docs/gates/phase-4.md`](phase-4.md) §6 carried the row *"no PM-1 gate act has re-held the box; Phase 3 stays open until one does"* |
| Date | 2026-08-05 |
| Tree | **`713a3192`** on `dev-/gotth-live-orchestrator-c3efc4`, `git rev-parse HEAD` — and, for the runs this section rests on, a **pristine `git archive HEAD` export**, because two other agents were writing uncommitted files into this shared worktree while the gate ran. **Re-confirmed at `2ab18690`**, which landed *during* this act and changes the `data-gotth-on` binding encoding — §12.2 |
| Remedy graded | **`1b16f4a9`** (DEV-3), which changed `examples/dashboard/README.md` and nothing else |
| **Verdict** | **MET, on all three of §5.3's conditions. Phase 3's box is ticked, PRD v1.4 applies it, and PHASE 3 EXITS — seventeen of seventeen** |
| What it does not do | It grades no Phase 4 box, reverses no QA-1 or L9-1 grade, and moves no requirement text |

**Why this section exists at all, in one sentence:** §5.3 refused to tick a box
because the document describing a measurement described a program that no longer
existed, and the only way to close that honestly is for somebody to run the
program and read the document against it — so that is what was done, rather than
reading `1b16f4a9`'s commit message and agreeing with it.

### 12.1 The three conditions, one at a time

§5.3 set exactly three. They are quoted here before they are graded, because a
condition paraphrased at the moment it is graded is a condition being moved.

> 1. `go run . -resync-cost 200` in `examples/dashboard`, in
>    `dis-gotth-live:latest`, at the tree that ships, with the host state stated.
> 2. The README's method paragraph rewritten to the request the harness now sends
>    — one held-back patch, a real gap, one feed interval per sample — so that
>    the next reader can tell which program produced the numbers.
> 3. Both halves in one landing. A number without its method is what created this
>    row.

| # | Condition | Verdict | Evidence, and who produced it |
|---|---|---|---|
| **1** | The run, in the image, at the tree that ships, host state stated | **MET** | **DEV-3 ran it at `35d4e258`** — which `git merge-base --is-ancestor 35d4e258 1b16f4a9` confirms is the remedy's own parent, so the numbers were taken at the tree the prose then landed on top of — and stated the host: `dis-gotth-live:latest` (Go 1.26.5) on `node-a`, 32 cores, load average 4.06 at the start, twenty containers up including a healthy `gpu-desktop-steam-1`. **PM-1 re-ran it six times** — five at `713a3192`, the sixth at `2ab18690` after that commit landed mid-act — three of the six on pristine exports (§12.2). The published byte figures reproduce **exactly**. |
| **2** | The method paragraph describes the request the harness now sends | **MET** | **Checked against the code, not against the prose.** `examples/dashboard/resync.go` at HEAD: `holdBack()` reads until a meters patch arrives and deliberately does not acknowledge it; the request is `EncodeResyncFrame(sessionID, client.applied, …)`, and `applied` is documented as *"the highest sequence this client has acknowledged, and is the only value it may honestly put in a `ResyncRequest`"*; `awaitSnapshot()` acknowledges nothing while the resync is outstanding; the Snapshot's cumulative `Ack` repairs the gap; and the wait at the top of the loop costs **one feed interval per sample**. That is the README's paragraph, clause for clause. **The server-side half the paragraph rests on is in force at HEAD too**: `internal/session/resync.go:119`–`:134` clamps a claimed cursor against `win.ackedSeq()` and `max(applied, a.lastSnapshotSeq)` before deriving a range — BR-9's fix — and the **only** change to that file since the measurement is an error log gaining `server_seq` and `last_applied_seq` under FR-58, which cannot move a frame. |
| **3** | Both halves in one landing | **MET** | `git show --stat 1b16f4a9` is **one file**, `examples/dashboard/README.md`, +101/−40 of prose. The numbers and the method paragraph moved together. `git diff 1b16f4a9 HEAD -- examples/dashboard/README.md` shows the resync section **untouched** since; the file's later diff is four rows of its own file table, a spec count 70 → 72 and a `livetest.Client` sentence. |

### 12.2 What was run, and what it printed

**All of it in `dis-gotth-live:latest` (Go 1.26.5), and the client suite in
`dis-gotth-live-bench:latest`, on `node-a` — 32 cores, load
average 3.57 / 4.43 / 4.12 at the start, 22 containers up including a healthy
`gpu-desktop-steam-1` with no streaming session.** The host is not quiescent and this
project does not pretend it has a quiet machine.

**The measurement, run 5 of 6, on the pristine `713a3192` export — the program's
own output, pasted rather than transcribed:**

```
state the snapshots rendered
  200 distinct state versions across 200 samples; last was version 236
  at the last snapshot: 5 alert rows, 30-sample sparkline

bytes on the wire, per snapshot
  frame: min 2220  p50 2378  p90 2661  max 2939  (n=200)
  markup: min 2079  p50 2231  p90 2512  max 2790  (n=200)
  protocol overhead (frame - markup, median): 147 B

  markup by region, last snapshot
    dashboard.alerts       925 B
    dashboard.controls     936 B
    dashboard.meters       929 B

  the library's own gotthlive_resync_bytes over the same run:
    n=200 mean=2368.1 B max=2939 B

latency, request written to snapshot read
  resync: min 71µs  p50 187µs  p90 279µs  max 1.15ms  (n=200)
```

**And the comparison was made by a diff, not by an eye.** The README's fence was
extracted from the export's own copy of the file and `diff -u`'d against the
program's stdout. It prints **one changed line**:

```
 latency, request written to snapshot read
-  resync: min 91µs  p50 172µs  p90 256µs  max 579µs  (n=200)
+  resync: min 71µs  p50 187µs  p90 279µs  max 1.15ms  (n=200)
```

**Every byte figure the README publishes — frame, markup, protocol overhead, the
three per-region figures, and the library's own `gotthlive_resync_bytes` mean and
max — is identical, on all six runs, 101 commits after they were taken.**

| Run | Where | Bytes | Latency (min / p50 / p90 / max) |
|---|---|---|---|
| 1 | worktree, `git status` clean | identical | 76 µs / 181 µs / 291 µs / 1.399 ms |
| 2 | worktree | identical | 101 µs / 176 µs / 260 µs / 1.79 ms |
| 3 | worktree, `diff`ed programmatically | identical | 114 µs / 184 µs / 269 µs / 1.511 ms |
| 4 | **`git archive HEAD` export** | identical | 56 µs / 202 µs / 305 µs / 2.623 ms |
| 5 | **`git archive HEAD` export**, `diff`ed | identical | 71 µs / 187 µs / 279 µs / 1.15 ms |
| 6 | **export of `2ab18690`**, `diff`ed | identical | 76 µs / 183 µs / 262 µs / 568 µs |

**Run 6 is the one this gate did not plan to do, and it is the most informative
of the six.** While this section was being written, **DEV-2 landed `2ab18690`** —
FR-54 failure 2's repair, which moves `Fields`, `Debounce` and `Throttle` out of
the element and into components 4, 5 and 6 of the binding in **`data-gotth-on`**,
with trailing empties trimmed. That is a change to **rendered markup inside live
regions**, which is exactly the class of change that can move this measurement's
byte figures, and it arrived in the window between the act and its record. The
measurement was re-run against an export of the new HEAD: **every byte figure
identical again**, because a binding declaring none of the three options
serialises to the string it always did. The suites were re-run there too —
`internal/session` **ok 6.388 s**, `internal/protocol` **ok 0.013 s**,
`test/internal/chaos` **ok 95.136 s**, `examples/dashboard` **ok 10.631 s under
`-race`**. **The act is held at `713a3192` and the figure is re-confirmed at
`2ab18690`**, and that distinction is kept rather than collapsed: a gate act is
held at a tree, and a re-confirmation is a second observation rather than a
second gate.

**The suites that hold the behaviour the criterion is about, all on the export:**

| Command | Result |
|---|---|
| `go test -count=1 ./internal/session/...` | **ok 6.374 s** |
| `go test -count=1 ./internal/protocol/...` | **ok 0.014 s** |
| `go test -count=1 ./test/internal/chaos/...` | **ok 96.139 s** — the eight chaos cases, including case 1 (drop → reconnect → resync) and case 2 (sequence gap → resync) |
| `cd examples/dashboard && go test -race -count=1 ./...` | **ok 10.570 s** |
| `for f in client/test/*.test.mjs; do node --test "$f"; done` | **8 files, 156 tests, 0 failures** — `bundle` 9, `codec` 34, `dev-reload` 18, `inspector` 15, `morph` 20, `reconnect` 35, `resync` 14, `supersession` 11 |

**Why the client suite is in this section rather than mentioned in passing.** The
README's argument for why the *old* figure may not be quoted is that the old
Snapshot superseded sequences the client had already applied and *"the shipped
runtime's `applied()` closes 4002 on exactly that overlap"*. That is a claim about
the browser runtime, and `client/test/supersession.test.mjs` is where it is
falsifiable: 11 tests, including *"a Snapshot at a sequence the client already
passed closes 4002 instead of acking backwards"*. It is green. The paragraph's
central factual claim is therefore checked rather than believed.

### 12.3 The number that did not reproduce, at the same prominence as the ones that did

The published latency line is `min 91µs p50 172µs p90 256µs max 579µs`. **It did
not reproduce, and it is not going to.** PM-1's six runs: p50 **181 / 176 / 184
/ 202 / 187 / 183 µs**, max **1.399 / 1.79 / 1.511 / 2.623 / 1.15 / 0.568 ms**,
min **76 / 101 / 114 / 56 / 71 / 76 µs**. **The published
`max 579µs` is the low outlier of the eight runs this host has now produced** —
DEV-3's own second run reported max 1.771 ms and said so in the README.

**This does not fail the box, and the reason is written down rather than
assumed.** The criterion asks for *"bytes and latency for a full resync"*. The
README publishes the latency **as a distribution, with its host, its load average
and its container count stated**, and it tells the reader, before anyone re-ran
it: *"the bytes are reproducible and the latency is not … quote the byte figures;
treat the latency as the shape of a distribution taken on a contended host."* A
document that predicts its own irreproducibility and is then found irreproducible
in exactly the manner it predicted is behaving correctly; a document that
published a mean and called it the latency would not be. **What would have failed
this box is a byte figure that moved** — that is the half the README instructs
readers to quote — and no byte figure moved.

**Stated so it can be held against this act later:** a reader who quotes
`max 579µs` as *the* resync latency of this library is quoting the fastest of
eight runs. The README does not invite that and neither does this section.

### 12.4 What this act found that §5.3 could not have

**The measurement survived a page-shell rewrite, and nobody checked that until
now.** `1b16f4a9` measured at `35d4e258`; HEAD is **101 commits** later. In
between, `live/` gained `(*App[S]).Document` and the dashboard's `Page` was
rewritten onto it (`3c66cc04`), `view.templ` and `view_templ.go` both changed,
and `internal/session/`, `internal/protocol/` and `internal/wsx/` all moved. **The
whole failure this box exists to catch is a published figure whose program moved
underneath it**, so a tick taken on the remedy's commit message would have
re-committed the original defect exactly one turn later.

It did not move, and the reason is structural rather than lucky: a resync
Snapshot carries the three regions' markup, and the page shell is not in any of
them. That is worth writing down because it is the thing a future reader needs in
order to know when this figure *can* go stale — a change to
`MetersRegion`/`AlertsRegion`/`ControlsRegion`, to the feed's shape, or to the
frame encoding, and not a change to the page.

**One thing found and routed rather than fixed, and it is not a condition.**
`examples/dashboard/README.md` attributes its figure to `35d4e258`. That
attribution is correct and is the practice that made this gate act cheap — but a
reader at a much later HEAD has no way to know the figure still holds without
doing what this section did. **Owner: DEV-3**, non-blocking, and PM-1 does not
edit `examples/**`. If a standing guard is wanted rather than a periodic re-run,
the trigger is `git diff <attributed-commit> HEAD -- examples/dashboard live
internal` being non-empty.

### 12.5 What moved in the tree with this act, and what deliberately did not

| Document | What moved |
|---|---|
| [`../PRD.md`](../PRD.md) | **v1.3 → v1.4.** Header Status row (with what it used to say kept beneath itself); §6's Phase-3 status block gains a v1.4 block beneath the v0.7 one; **the box itself is ticked**; Phase 4's box 12 gains a note that its resync clause's other enforcement point is now closed too; §9 gains the **v1.4** amendment entry, four rows |
| This report | The Verdict row, §1, §5's criteria table row 5, §5.3, §7's owed table, §11, and this §12. **Nothing above is rewritten**; every correction is beneath the text it corrects |
| [`../pm/checkpoint-3-closure.md`](../pm/checkpoint-3-closure.md) | A new §11 recording the act against the ledger that carried the row |
| [`../README.md`](../README.md) | The docs index row for this report, which told a reader Phase 3 does not exit |

**Not moved, deliberately, and reported instead of edited:**
[`phase-4.md`](phase-4.md) **§6's row** — *"no PM-1 gate act has re-held the box.
Phase 3 stays open until one does"* — and its §7 note near `:951`. **This act
discharges both.** They are left for the orchestrator because another stream is
changing the artifacts that file grades, and an agent editing another agent's
open file mid-flight is how two correct edits become one wrong document. The same
applies to `docs/README.md`'s **Phase 4** row, which still reads *"thirteen exit
criteria, six met, seven open"* against a tree at twelve of thirteen — **PM-1
moved the checkpoint-3 row directly above it and left the Phase 4 row alone**,
which is a deliberate asymmetry and is recorded here so it reads as a decision
rather than as an oversight.

**Not run and not claimed:** `bash ci.sh` — it is not this box's gate, and this
orchestrator has already published one `ci.sh` wall taken on a host with no Go;
no browser; no bench campaign; no Phase 4 or Phase 5 grading of any kind.

### 12.6 Reproduce this gate act

```bash
REPO_ROOT=<repository root, not gotth-live/>
cd "$REPO_ROOT/gotth-live"

# the tree this act was held at (713a3192), and the one it was re-confirmed at
git rev-parse 713a3192 2ab18690
git merge-base --is-ancestor 35d4e258 1b16f4a9 && echo "measured at the remedy's parent"
git show --stat 1b16f4a9               # one file: examples/dashboard/README.md
git diff 1b16f4a9 HEAD -- examples/dashboard/README.md   # the resync section is untouched

# an export, because this worktree has other agents in it
rm -rf /tmp/pm1-gate && mkdir -p /tmp/pm1-gate && git archive HEAD | tar -x -C /tmp/pm1-gate

# condition 1 — bash -c, NOT bash -lc (a login shell strips the Go toolchain)
docker run --rm -v /tmp/pm1-gate:/w -w /w -e GOFLAGS=-buildvcs=false \
    dis-gotth-live:latest bash -c 'cd examples/dashboard && go run . -resync-cost 200'
# then diff its stdout against the fence under "## The resync cost" in
# /tmp/pm1-gate/examples/dashboard/README.md — exactly one line differs, the latency line

# the suites
docker run --rm -v /tmp/pm1-gate:/w -w /w -e GOFLAGS=-buildvcs=false \
    dis-gotth-live:latest bash -c 'go test -count=1 \
        ./internal/session/... ./internal/protocol/... ./test/internal/chaos/...'
docker run --rm -v /tmp/pm1-gate:/w -w /w -e GOFLAGS=-buildvcs=false \
    dis-gotth-live:latest bash -c 'cd examples/dashboard && go test -race -count=1 ./...'
docker run --rm -v /tmp/pm1-gate:/w -w /w dis-gotth-live-bench:latest bash -c \
    'n=0; for f in client/test/*.test.mjs; do n=$((n+1)); node --test "$f"; done; \
     test "$n" -gt 0 || { echo "no specs matched" >&2; exit 1; }'

# condition 2, and it needs no toolchain
sed -n '/^func (c \*measureConn) holdBack/,/^}/p' examples/dashboard/resync.go
sed -n '110,140p' internal/session/resync.go     # BR-9's clamp, in force at HEAD
```

**Phase 3 exits. The consolidated Phase 1–3 track exits with it.**

— PM-1, Product Manager, 2026-08-05
