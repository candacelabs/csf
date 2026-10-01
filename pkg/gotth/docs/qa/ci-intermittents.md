[identifiers genericized for publication - measurements unmodified]

# CI intermittents — evidence from the shared-runner context

Standing note for the orchestrator/QA-2: failures observed in the GitHub
Actions library gate that did NOT reproduce in the parallel run of the same
SHA, nor locally on the 32-core host. GitHub's shared runners are 2–4 vCPUs
and stall under load; a margin that holds on `node-b` is not a margin there.

## Observed on `a7050e6e` (run 30942082684, job 92102786176, 2026-08-04)

1. `test/internal/chaos/case6_partition_test.go:178` — "leaves other sessions
   serving while one is partitioned": `Eventually` timed out at 10.001s with
   the healthy session's counter at exactly 3 against a `> 3` bound. On a
   slow runner the cadence produced one fewer increment inside the window.
2. `test/internal/chaos/case5_flood_test.go:120` — the D-24 characterisation
   spec ("does not reach a defined close below that rate"): the sub-threshold
   flood DID reach close 4008 in this context only. Wall-clock pacing on a
   stalling runner bunches frames, so the instantaneous rate crossed the
   3× threshold even though the intended average was below it.

### Disposition

**Both are closed.** What follows is the evidence, because "fixed the
intermittent" is the sentence that most often means "widened a timeout until it
stopped failing here", and a margin fix that makes a spec unfalsifiable is worse
than the intermittent it removes.

#### 1 — case 6's bystander spec: it was never a margin (closed)

The first line of the report above is wrong about the cause, and finding that
out is the fix. The spec read:

```go
healthy.commit(0)
before := healthy.appliedSeq()                                    // sampled AFTER the send
Eventually(healthy.appliedSeq, 10*time.Second).Should(BeNumerically(">", before))
```

The sample was taken after the event went out, so whether the spec could pass at
all depended on whether that event's `Patch` had already been applied when the
sample landed. When it had, `before` already counted the only increment the spec
was ever going to see, no further event was sent, and the `Eventually` was
waiting for something that could not happen. That is exactly the recorded
failure — `before` came back 3, the bound was `> 3`, and it timed out at
10.001 s. **No timeout would have made it pass.** A bound sampled out of a race
is not a bound, and widening the window would have converted a red spec into a
red spec that took longer.

The bound is now a counted protocol event: sample first, send exactly one
state-changing event, require the applied sequence to land on exactly
`before+1`, because the server answers one such event with one `Patch`. The
remaining wall-clock term is a `roundTrip` timeout, named as a constant and
sized at the site for GitHub's 2-vCPU shared runner rather than for this host.

Evidence, all in `dis-gotth-live:latest` against a `git archive HEAD` export
under `/tmp/qa2-case6/` with the worktree untouched:

| tree | change | result |
|---|---|---|
| `oldrace` | HEAD's spec + a 500 ms sleep between the commit and the sample — the stall a 2-vCPU runner supplies for free | **RED**, `Timed out after 10.001s. Expected <uint64>: 3 to be > <uint64>: 3` — the CI failure reproduced deterministically, message for message |
| `newrace` | the new spec + the identical 500 ms sleep | **GREEN** in 8.5 s; the stall is irrelevant to a bound that is not sampled out of a race |
| `m5` | mutation **M5** from `checkpoint-3-chaos.md` §6 — `Actor.onTick`'s heartbeat-timeout close deleted, the mutation case 6 was written to catch | **RED**, both case-6 specs, the bystander at the reclamation assertion (`<int>: 2 to equal <int>: 1`) |
| `restored` | M5 reverted, spec unchanged | **GREEN**, 3 of 36 specs, 19.1 s |
| `tight` | the new bound moved to `before+2` | **RED**, `<uint64>: 3 to equal <uint64>: 4` — the assertion is evaluated and demands exactly one patch, so a bystander that stopped serving, or one that got two patches for one event, both fail it |

The `tight` row is the one that answers the question this project keeps having
to ask: the new assertion is not merely satisfied, it is *reachable in both
directions*.

#### 2 — case 5's D-24 flood pacing (closed in `3bfbdb8c`, verified here)

Not re-done; verified. `3bfbdb8c` replaced the unpaced send loop with a stated
3,000 frames/s — sixty times the configured 50/s limit and a fifth of the 15,000
frames/s close threshold — and, more importantly, made the spec assert its own
premise before asserting its conclusion:

```go
Expect(float64(sent)/elapsed.Seconds()).To(BeNumerically("<", 15000),
    "the sender outran the close threshold, so this run says nothing about the rate below it")
```

That is the general form of the fix and the reason this one needs no further
work: a run that stalls badly enough to bunch frames past the threshold now
fails on the premise, with a message saying the run was inconclusive, instead of
silently reporting the host's speed as the library's behaviour. §6.1 of
`checkpoint-3-chaos.md` records the same thing from the authoring side.

## The rule these imply

Chaos-suite margins must hold on a 2-vCPU runner under noisy-neighbour
stalls, not just on the dev host: prefer bounds derived from counted
protocol events over wall-clock windows; where wall-clock is inherent,
size `Eventually` windows and rate-pacing against the slowest supported
runner and say so at the site. (Same class as the checkpoint-1 obstest
race and the instrumentation Eventually fix — the reader/runner is slower
than the writer assumes.)

## Observed on `52a64e04` (REV-DUP §9, examples/dashboard, 2026-08-05)

`examples/dashboard/wire_test.go` — "attributes the mount snapshot to the mount
and indexes it in the provenance log": `Expect(found).NotTo(BeNil(), "patch %d
appears in no provenance row")` failed once in eight runs during the
deduplication review's verification, then passed 3/3 and 4/4 on re-run. That
review flagged it for the dashboard owner rather than diagnosing it, correctly
noting the spec "reads the provenance log after awaiting a patch off the wire,
and the log row is written asynchronously relative to the frame".

### Disposition

**Closed.** The one-line hypothesis above was right about the mechanism. What it
did not have is the *width* of the window, and the width is the whole question:
it decides whether the answer is a bigger number or a different kind of bound.

#### The gap is real, deliberate, and about 50 µs wide

`Actor.emitPatch` (`internal/session/actor.go`) writes the socket and then
writes the causal row — `a.send(...)` first, `a.provenance(...)` last. That
order is not an oversight and must not be swapped: the branch where the send
fails emits no row at all, because a frame that never reached the transport must
not appear in the causal log as delivered. **A patch off the wire is therefore
not a receipt for its own provenance row**, and a spec that reads the log the
instant the patch arrives is racing the actor's tail.

Measured by injecting a sleep at exactly that point in a scratch export and
sweeping it, twenty runs per step:

| injected stall | 0 | 50 µs | 100 µs | 200 µs | 1 ms | 500 ms |
|---|---|---|---|---|---|---|
| failures | 0/20 | 19/20 | 20/20 | 20/20 | 20/20 | 20/20 |

Fifty microseconds. That is not a margin anyone can size — it is one deschedule
of the actor goroutine, which a contended 2-vCPU runner hands out for free. This
is why the failure was 1-in-8 there and unreproducible on `node-b`: on a 32-core
host the actor simply does not lose the CPU in that window. It reproduces on
unmodified `HEAD` under cgroup throttling — `--cpus=0.2`, `GOMAXPROCS=6` —
**1 failure in 40 runs, message for message**:

```
[FAILED] patch 2 appears in no provenance row
Expected
    <*main.provenanceRow | 0x0>: nil
not to be nil
```

**This was never a margin, for a different reason than case 6 was not one.**
Case 6's bound was unreachable and no timeout would have helped. Here a timeout
*would* have worked — which is precisely the trap, because it would have been a
guess about a scheduler rather than a claim about the library, and the next
runner slower than the guess reopens the bug. The gap is an ordering the spec
never had, not a duration it failed to wait out.

#### The fix is a counted protocol event, not a window

One session's transitions are applied in sequence by a single actor goroutine,
so a **second patch on the socket proves the first patch's row was already
written**. The spec now samples twice and awaits the later patch before reading
the row for the earlier one. There is no wall-clock term in the property at all;
no stall can invalidate an ordering. The hand-rolled lock-and-scan the spec used
is now `provenanceLog.rowFor`, beside the `eventIDFor` that already existed, so
the next spec that wants this lookup gets the reason it cannot be read eagerly
at the point it would otherwise write the race again.

Evidence, all in `dis-gotth-live:latest` against a `git archive HEAD` export
under `/tmp/qaflake/` with the worktree untouched:

| tree | change | result |
|---|---|---|
| `oldstall` | HEAD's spec + a 500 ms sleep between the send and the provenance write — the deschedule a 2-vCPU runner supplies for free | **RED**, `patch 2 appears in no provenance row` — the reported failure reproduced deterministically, message for message |
| `newstall` | the new spec + the identical sleep | **GREEN** in 1.0 s; a stall is irrelevant to a bound that is an ordering |
| `mutate` | `a.provenance(...)` deleted from `emitPatch`'s success path — the emitted patch gets no causal row, the defect the spec exists to catch — and **no** stall | **RED**, `patch 2 appears in no provenance row` |
| `mutate2` | subtler: the row is emitted but names no regions (`fragmentIDs` → `nil`), no stall | **RED**, `<[]string \| len:0>: nil to equal ["dashboard.meters"]` — the content assertions are live, not just the existence one |
| `tight` | the lookup moved one patch further, to the patch the spec did *not* wait past, + the 500 ms stall | **RED**, `patch 3 appears in no provenance row` |

The `tight` row is this file's recurring question answered again, and here it
answers a sharper version of it: the new bound is not merely satisfied, it is
*exactly one patch deep*. Ask for the row of the patch the spec did not wait
past and it fails — so the ordering claim is the real load-bearing thing, and
the assertion is reachable in both directions.

#### The defect was one spec's, not the suite's

A 500 ms stall on every patch also reds seven other specs, but that proves
nothing: at half a second per emission the backpressure ladder, the resync byte
counts and the coalescing windows are all measuring a different system. The
control is a stall well above the 50 µs gap and well below anything else in the
suite. At **1 ms**, across all 72 specs: HEAD fails **only** this spec (71
passed, 1 failed); with the fix, **72 passed**.

Tallies for the fixed spec — 20 runs at each stall that was 20/20 red before,
and 25 runs under the throttling that reproduced it naturally:

| condition | result |
|---|---|
| injected stall 100 µs / 1 ms / 500 ms | **0 failures in 20** at each |
| `--cpus=0.2`, `GOMAXPROCS=6`, unmodified library | **25/25 green** |
| module gate: `go build`, `go vet`, `gofmt -l`, `staticcheck`, `go test -race -count=1` | green |

One thing found and deliberately not changed: the two coalescing specs read the
same log, and their bound is `Settle(settleIdle)` — 250 ms of silence on the
frame channel. That is a wall-clock quiescence bound where an ordering was
available, so it is the same shape as the bug above; it is also four orders of
magnitude wider than the gap, which is why it has never fired. Left alone rather
than rewritten on suspicion, and recorded here so that a future report against
either of them is read as this and not as a new discovery.

### What it implies

**Nothing new** — it is the rule already stated one section up, reaching further
than the `Eventually` windows it was written about. The general form: a spec
that awaits a frame and then reads state the library writes *outside* that frame
has no bound at all, only a race it usually wins. Await something the library
orders after the write, and prefer the cheapest counted event that gives it.

## CI load note (operator complaint) — DISCHARGED

Was: every push triggered BOTH the `push` and `pull_request` contexts — two
identical run sets per landing, so four jobs, four image builds, two runs of a
~7-minute chaos suite and two runs of the browser specs for one commit. On
2-vCPU shared runners that is not an accounting complaint: it is a direct cause
of the queue depth and the noisy-neighbour stalls behind both intermittents
above.

Both remedies the note offered are now in
`.github/workflows/gotth-live-checks.yml`, because they close different halves:

* **`push` is filtered to `branches: [main]`.** That is what removes the
  duplicate — a push to a branch with a PR open now matches `pull_request`
  only. It is narrowed rather than dropped so that `main` still gets a run:
  the post-merge verdict is a different claim from the pre-merge one (the merge
  commit is not the PR's test merge), and without it nothing would ever assert
  that main itself is green.
* **A `concurrency` group per ref, cancelling asymmetrically.**
  `cancel-in-progress: ${{ github.event_name == 'pull_request' }}`. A PR ref
  cancels, because pushing again makes the running gate a verdict on code that
  no longer exists while holding a runner the next PR needs. `main` does not
  cancel, because every commit on main has to keep its own verdict — a run
  cancelled by the next landing answers "was this commit green" with "unknown",
  which is indistinguishable from a skip.

The path filters are unchanged. Narrowing which refs run the gate is not the
same as narrowing what counts as a change to gotth-live; the second would be a
hole rather than a saving.

Steady state per landing is now one run set on the PR and one on main after
merge, against four before.

## The gate's own gaps, closed in the same turn

Not intermittents — the opposite, and recorded here because this is the standing
note on what CI does and does not actually run.

**`docs/guide/_samples` was run by nothing.** It is a nested module *and* its
directory begins with an underscore, so Go's package patterns skip it twice
over: `go test -race ./...` never reached it, no examples block named it, and
its Ginkgo suite was green in the sense that it had never run. `ci.sh` now has a
step for it — build, vet, staticcheck, gofmt, `go test -race` — placed with the
other separate-module steps. staticcheck runs there where the examples blocks do
not run it, because a reader copies a sample into their own program.

**FR-7 was red, and is now green by widening the check rather than the
exemption.** The gate run recorded in `c11045a9` had exactly one red step:
`gen.sh`'s `templ_sources` guard — the walk that holds the enumeration honest,
added after O-1 — fired on the five `docs/guide/_samples/*/view.templ` files. It
was right to fire. The five are now listed in `templ_sources`, so their
committed `view_templ.go` gets the same baseline-and-compare treatment as the
examples' and a sixth sample `.templ` still fails the gate until someone lists
it. Re-run in `dis-gotth-live:latest` from the repository root:

```
==> generating the templ views
==> comparing against the committed output
==> the committed output is byte-identical to a fresh generation
```

Byte-identical on the first attempt, so no regeneration commit was needed: the
samples' committed output was already what the pinned templ CLI produces. The
alternative — excluding `docs/` from the walk — would have made the guard green
by shrinking what it looks at, which is this repository's recurring defect
rather than a fix for it.

The walk then fired a second time in the same turn, on
`bench/apps/counter/gotth/view.templ`, which landed with its `view_templ.go`
committed beside it. Same answer, same result: listed, and byte-identical to a
fresh generation. Worth stating that this is the enumeration working rather than
a nuisance — twice in one turn it caught committed generated code that FR-7's
gate was not looking at, which is exactly the failure O-1 recorded. Whoever
lands `bench/apps/chat/gotth` or `bench/apps/dashboard/gotth` with a `.templ`
should expect it to fire again and should list the file, not exclude it.

### Gate run — `fe9b6772`, full `ci.sh` in `dis-gotth-live-bench:latest`

Against a clean `git archive HEAD` export, so the verdict is about committed
code rather than four agents' in-flight working tree. Every step green except
one:

* **red: gofmt (NFR-12)** — `bench/apps/counter/gotth/counter_test.go`, a
  one-line violation (`` `<script src="` + ShimRoute + `"></script>` `` wants
  no spaces around the `+`), landed in `2bf564c5`. Not touched here: `bench/**`
  is another agent's area, and papering over a red step is the thing this file
  exists to argue against.

Everything else, in order: build, vet, staticcheck, `-race` across the module
(chaos 73.9 s, conformance 169.1 s), the chaos soak and measurements (399.9 s),
examples/counter, examples/chat, examples/dashboard, **docs/guide/_samples**,
test/routers, test/sampling, test/memory, the gate's own tests, the FR-65
surface delta (52 identifiers, 50 fields, matches the ledger), the NFR-2/NFR-3
size gate (**4,360 B** `gzip -9` against a 12,288 B ceiling, 64.5 % headroom),
**FR-7 byte-identical**, the client runtime suite including the no-eval scan,
and the browser conformance specs under Chromium 151.0.7922.71.

> **Corrected 2026-08-05.** That row read *"10,178 B against a 12,288 B ceiling,
> 64.5% headroom"*, and those two figures are not the same measurement. QA-2
> caught it at the checkpoint-3 re-verification (§R8's client-runtime row) and
> correctly declined to edit a file that is not theirs; it is the orchestrator's,
> and this is the edit. 10,178 B is the **minified** artifact; the NFR-2 gate is
> **`gzip -9` over it**, which at `fe9b6772` was 4,360 B — and 64.5 % is
> `1 − 4360/12288`, so the percentage was always the gzip figure's while the byte
> count beside it was the minified one. The gate passed on either reading, which
> is exactly why the sentence survived: nothing it gated on could go red because
> of it. The tool prints both columns unambiguously — at `73f5bf2f`
> `go run ./minify -check` reports `Shipped gotth-live.min.js  10391  4429` and
> `ceiling 12288, headroom 7859 (64.0%)` — so the defect was this document
> merging two of its columns into one clause, not the instrument.

## Process note

These fired because mid-turn work was pushed to the PR branch before the
authoring turn's own gate hardening finished. Orchestrator policy from now
on: landings are pushed at turn boundaries after `ci.sh` is green, not
mid-turn.
