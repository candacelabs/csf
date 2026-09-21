# Checkpoint 2 — conformance: the C-22 residue, closed

| | |
|---|---|
| **Author** | QA-1 (Correctness) |
| **Scope** | the C-22 residue recorded in the [checkpoint-2 batch](../reviews/checkpoint-2-batch.md)'s orchestrator log |
| **Date** | 2026-08-04 |
| **Held against** | [protocol.md](../protocol.md) §6 (H-4, H-6, H-13, H-14) and §7 (P2, P5, P6); [PRD](../PRD.md) FR-41, FR-43, G4 |
| **Files touched** | `test/internal/conformance/provenance_test.go`, this document |
| **Verdict** | **C-22 closed.** P5, P6 and the soak-labelled run each observe a `RESYNC`-origin frame, and each fails rather than passes when the run holds none. One new defect, **D-14**, reported and not fixed |

The condition being discharged is the residue, not C-22 itself. C-22's code
half — P2's conformance spec extended to the `RESYNC` arm, and the inverted
comment in `internal/protocol/invariants.go` — landed in `1b9f0743`. The
orchestrator log recorded what it left:

> The code half is done; P6's resync clause, P5's coalescing set-equality, and
> the soak-labelled run still execute over `exercise(...)`, which sends no
> resync, so those clauses are unexercised for the arm C-22 exists to cover.

---

## 1. What was vacuous, and how that was established

By reading: `exercise(rounds)` dials, sends `qa.increment` / `qa.relabel` /
`qa.noop` in rotation, acknowledges as it goes, and never writes a
`ResyncRequest`. Every property pointed at it therefore ran over a capture in
which `OriginKind_RESYNC` does not occur.

By running, before anything was changed — a probe that printed each run's
origin-kind histogram:

```
exercise(9)                  frames=7   kinds=map[CLIENT_EVENT:6 MOUNT:1]
exercise(12)                 frames=9   kinds=map[CLIENT_EVENT:8 MOUNT:1]
exerciseWithResync(9)        frames=11  kinds=map[CLIENT_EVENT:9 MOUNT:1 RESYNC:1]
coalescing run (40, no ack)  frames=16  kinds=map[CLIENT_EVENT:15 MOUNT:1]
exercise(400) [soak]         frames=268 kinds=map[CLIENT_EVENT:267 MOUNT:1]
```

One run in the suite contained a `RESYNC` frame, and it was the one C-22
pointed P2 at.

| Property | Ran over | Saw a `RESYNC` before | Now |
|---|---|---|---|
| P2 — chain closure (both specs) | `exerciseWithResync(9)`, `(6)` | **yes** — C-22's code half | unchanged |
| P5 — coalescing | 40 unacked events, no resync | **no** | `floodAndFlush()` |
| P6 — resync resolvability | *did not exist* | — | `exerciseWithResync(6)` |
| P6 — totality, zero unknown | `exercise(12)` | **no** | `exerciseWithResync(12)` |
| P6 — totality, soak | `exercise(400)` | **no** | `exerciseResyncing(400, 100)` |
| P7 — resync boundary (H-13) | its own inline resync | **yes** | unchanged |
| P1, P3, P4, P7 first arm, P8 | `exercise(...)` | **no** | **deliberately unchanged**, see §5 |

A second kind of vacuity was found in the same place and is worth naming
separately, because no document had flagged it. The coalescing spec ended:

```go
if coalesced == nil {
    Skip("the run did not fill the outbound window, so coalescing was not exercised")
}
```

**A `Skip` is a pass.** That spec would have reported green on a build in which
the backpressure ladder stopped folding provenance forward at all — which is
the same defect class as D-4 (the Coalesce stage that was specified, tested for,
and wired to nothing) arriving one layer up. It is now a failure with a message
that names both ways it can happen.

---

## 2. What changed

All of it in `test/internal/conformance/provenance_test.go`.

**Run builders.** `exercise`'s loop moved to `(*driven).mixedRounds(phase, n)`
so a run can be resumed across an interruption; `exercise` itself is unchanged
in behaviour and its doc comment now says out loud that it sends no resync.
`(*driven).resync(lastApplied)` and `(*driven).resyncGap(patches)` are the gap
construction that was inline in `exerciseWithResync`, extracted because three
runs now need it. `exerciseResyncing(rounds, every)` is the soak form.
`floodAndFlush()` is P5's run.

**`mountSeq`.** `ResyncRequest.last_applied_seq` is refined `this > 0`, so a
client cannot ask to supersede the mount itself, and a request carrying `0` is
rejected at the parse boundary and answered with **no `Snapshot` at all**. That
was found the expensive way — a resync from `0` looks like "supersede
everything" and instead produces a five-second read timeout — and it is now a
named constant with the reason attached.

**H-14 is a lower bound on the soak's cadence.** `exerciseResyncing` asserts
`every >= 50` rather than trusting its caller: the resync budget is one per
second with a burst of three, and a rate-limited request is answered with an
`Error` and no `Snapshot`, which would **hang** the run rather than fail it.

**P5** — the `Describe` is renamed from "Coalescing under a full window" to
"P5 — coalescing preserves provenance", which is what §7 calls it. Three specs:
the existing H-4 bound with its `Skip` removed; the set equality §7 P5 actually
states; and the `RESYNC` arm.

**P6** — a new spec resolving a `RESYNC` snapshot from its bytes alone through
the first arm of G4's disjunction, which §7 P6 requires by name. It joins the
originating event, the `client_ref`, the transition, the state version, the
origin source, the fragments and §4.3's supersession range against the log. The
totality spec and the soak move to resync-bearing runs and gain a non-vacuity
clause.

### 2.1 Why P5 needs a resync — the substantive part

This is not a resync bolted onto a run to satisfy a checklist.

§7 P5 is *"the union of those ids over a run equals the set of events that
produced a state change and were not individually patched"*, as set equality
and not sampling. A full outbound window **stops emitting entirely**, so a
flood that is never acknowledged ends holding deferred provenance that never
reached the wire. Measured on the run P5 drives:

```
no-flush   U = [8 10 12 14 16 18 20 22]                     (8 identifiers)
no-flush   S = [8 10 12 14 16 18 20 22 24 25 ... 40]        (25 identifiers)
```

P5's set equality over that run is not merely unexercised. It is **false**.

A resync `Snapshot` renders everything and folds the pending set in on the way
— `emitSnapshot` calls `takePending` exactly as `emitPatch` does — so it is the
frame that carries the provenance of every event the full window swallowed:

```
resync snapshot kind=RESYNC event_id=41 sup=[2,16]
  contributing=[24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40]
with-flush U(25) = [8 10 12 14 16 18 20 22 24 25 ... 40]
with-flush S(25) = [8 10 12 14 16 18 20 22 24 25 ... 40]     equal=true
```

An acknowledgement would also flush. A resync is used because it is the arm
C-22 exists to cover, and because it flushes the whole set in one frame.

The two sides are computed from artifacts written by different code on
different paths — the union from the wire capture, which the framer produced,
and the set from the provenance log, which the actor produced in step. That is
the mechanism this whole file rests on: agreement between them is evidence,
agreement of either with itself is not.

---

## 3. Non-vacuity, by mutation

Every run below was executed in `dis-gotth-live:latest` against a pristine
`git archive HEAD` export with only `test/` overlaid. That was not fastidiousness:
another agent's in-flight edits to `internal/session` had the shared worktree in
a state where `go vet ./...` did not compile, and a result read out of a broken
tree is not a result.

### M1 — the run no longer contains a resync

The mutation C-22 is about. `floodAndFlush` drops its `d.resync(mountSeq)`; the
three P6 call sites become `exercise(6)`, `exercise(12)`, `exercise(400)`.

```
• [FAILED] P6 [It] resolves a resync snapshot from its bytes alone, through its originating event
  [FAILED] the run produced no resync snapshot, so this property's RESYNC arm never
  executed. protocol.md §7 requires that to fail rather than pass vacuously: the resync
  event_id is the identifier cycle 2's B-7 added in order to preserve provenance across a
  resync boundary, and an arm that never runs is satisfied by anything
    Expected <[]uint8 | len:0, cap:0>: nil not to be empty

• [FAILED] P6 [It] resolves every sequenced frame in the run, with zero unknown
  [FAILED] ...same message...
    Expected <[]conformance_test.carrier | len:0, cap:0>: nil not to be empty

• [FAILED] P6 [It] resolves every sequenced frame over a long run [soak]
  [FAILED] ...same message... — this run held 0 of them
    Expected <int>: 0 to be >= <int>: 4

• [FAILED] P5 [It] puts exactly the events the window swallowed onto the frames that flush them
  [FAILED] the union on the wire is [8 10 12 14 16 18 20 22] and the log says the swallowed
  set is [8 10 12 14 16 18 20 22 24 25 26 27 28 29 30 31 32 33 34 35 36 37 38 39 40]:
  P5 is set equality, so a difference either way is provenance created or lost
    the missing elements were <[]uint64 | len:17, cap:17>: [24 ... 40]

• [FAILED] P5 [It] flushes the events a full window swallowed onto the resync snapshot
  [FAILED] ...vacuousWithoutResync...

Ran 8 of 130 Specs — FAIL! -- 3 Passed | 5 Failed
```

### M2 — the snapshot path stops folding deferred provenance

`internal/session/actor.go:475`, in `emitSnapshot`:

```diff
-	origin.Contributing = unionEdges(origin, contributing)
+	_ = contributing // MUTATION
```

```
• [FAILED] P5 [It] puts exactly the events the window swallowed onto the frames that flush them
  the union on the wire is [8 10 12 14 16 18 20 22] ... the missing elements were 17

• [FAILED] P5 [It] flushes the events a full window swallowed onto the resync snapshot
  [FAILED] the resync snapshot named no contributing event, so the transitions the full
  window swallowed reached the wire on no frame at all: H-4 calls the bound a flush trigger
  and FR-43 forbids losing provenance, and a resync that renders everything must carry
  what it renders

Ran 9 of 130 Specs — FAIL! -- 7 Passed | 2 Failed
```

Exactly the two P5 clauses and nothing else. P2 and P6 stay green, which is
correct: the identifiers are intact, only the folded set is gone.

### M3 — the server emits `CLIENT_EVENT` where it should emit `RESYNC`

`internal/session/resync.go:78`, `Kind: pb.OriginKind_RESYNC` →
`pb.OriginKind_CLIENT_EVENT`.

```
Ran 12 of 130 Specs — FAIL! -- 4 Passed | 8 Failed
  P2 ×2, P5 ×3, P6 ×2, and P7's resync-boundary spec
```

**This is a weaker red than M1's and M2's, and saying so is the point.** All
eight fail as `conformance: no frame within 5s`: the mutated snapshot never
reaches the wire at all. `internal/protocol/invariants.go:142` is H-13's
`(from != 0) != resync` check, so `ValidateOutbound` (§5.3) refuses a snapshot
carrying a supersession range under any kind but `RESYNC`, drops the frame and
emits an `Error` in its place. The specs are witnessing a **missing** frame,
not a wrong one.

That is worth recording as a positive result rather than an inconvenience: H-6
and H-13 are enforced at the construction boundary and not merely asserted
about on the wire, so the class of mutation that would make a resync snapshot
lie about its own kind is unsendable. §5.3 says converting P1 from a discipline
into a property was the goal; measured, it did the same for H-13.

---

## 4. Defects

### D-14 — MEDIUM — `CoalesceFlushAt` above H-4's ceiling turns the flush trigger into an emission failure

**H-4, FR-43, §7 P5. Owner: DEV-1. Not fixed here — QA-1 does not change
library code.**

`live.Limits.CoalesceFlushAt` is exported, documented as tunable, and
documented as *"the size of the contributing-event union at which a coalesced
patch is emitted immediately rather than coalesced further, **so provenance is
never truncated**"* (`live/config.go:187`). Nothing validates it.
`live/app.go`'s `validate` inspects no `Limits` field at all, and
`internal/session/limits.go`'s `Normalize` only fills zeros. The schema ceiling
it must stay under is `CoalesceFlushCeiling = 1024`
(`internal/protocol/limits.go:58`), and no exported doc names that number.

Set above the ceiling it stops being a flush trigger. The union outgrows H-4's
bound, `ValidateOutbound` refuses the frame the library itself constructed, the
frame is dropped — and `takePending` has already taken the deferred set, so it
is gone.

**Repro.** `CoalesceFlushAt: 4000`, 1,400 unacknowledged state-changing events,
then one `ResyncRequest`:

```
rows=1402 carriers=16 swallowed=1385
resync answered with Error code=INTERNAL message="the server could not encode an update" fatal=false
log[ERROR] gotth-live: refused to send a frame this library could not validate: this is a library bug
second resync answered with a Snapshot, contributing=0
final: union on wire=8, swallowed in log=1385, lost=1377
```

**Expected**, per §7 P5: the union on the wire equals the set of events that
changed state and were not individually patched — 1,385. **Got:** 8. One
thousand three hundred and seventy-seven event identifiers reach the wire on no
frame at all, which is precisely the *"truncation-is-provenance-loss problem
H-4 exists to prevent"* (§4.3), arriving through the field whose documented
purpose is to prevent it.

Three things make this worse than its severity suggests, and one makes it
better:

- The client asked for a resync and got a **non-fatal** `Error{INTERNAL}`. It
  is not told its re-render failed in a way it should retry, and the second
  request succeeds — so a wire consumer sees a transient blip, not
  provenance loss.
- The failure is at the outbound boundary, so the log line is *"this is a
  library bug"* — accurate, and pointed at the library rather than at the
  configuration that caused it.
- Every other `Limits` field is unvalidated on the same path. This is the one
  with a schema ceiling behind it, so it is the one that fails; the shape is
  general.
- **Not reachable on defaults.** `CoalesceFlushAt` defaults to 512 and the
  flush keeps the union under it, so a consumer who sets nothing is safe. This
  is why it is MEDIUM and not HIGH.

**Fix shape** (DEV-1's call, not QA-1's): reject a `CoalesceFlushAt` above
`protocol.CoalesceFlushCeiling` in `live.New`, or clamp it in `Normalize`, and
name the ceiling in the exported doc comment. Rejecting is the better shape by
this project's own precedent — a limit that silently becomes a different limit
is the `total`-column failure in another costume.

Held as a **pending spec** (`PIt`, *"holds P5 under every CoalesceFlushAt an
application is allowed to set"*) in `provenance_test.go`, following D-1's
precedent, so the requirement is executable when someone comes to it rather
than a note in a document.

---

## 5. What this deliberately does not close

**P1, P3, P4, P7's first arm and P8 still run over `exercise(...)` and still
see no `RESYNC` frame.** That is a decision and not an oversight, recorded here
so it is not re-discovered as a finding:

- None of them has a resync clause in §7. P1 is about `source` and `kind` being
  set at all, P3 about contiguity, P4 about the state version, P8 about the
  bijection between the capture and the log. A resync frame is one more frame
  to each of them, not a new arm.
- P7's second spec already drives its own resync and asserts H-13's range
  arithmetic, which is the resync-specific half of P7.
- Widening the rest would be a different condition with a different argument,
  and pointing them at a slower run for no stated property is how a suite gets
  expensive without getting stronger.

**The one that would be worth doing next**, and is not in scope here: §4.3's
closure claim in full. The argument that a range is sufficient rests on *"the
superseded patches are themselves in the capture … so an analyst walks
`[from, through]`, reads each patch's `Origin`, and recovers the event set"* —
and the deferred transitions are **not** in that range, because they were never
emitted. Completeness is therefore the union of three things: the origins of
the patches inside the superseded range, the resync snapshot's own contributing
set, and its own `event_id`. The new P5 and P6 specs check the second and third
directly. Nothing checks the whole disjunction as one statement.

---

## 6. The gate, as run

`~/bin/dis run bash ci.sh` from `gotth-live/`, in `dis-gotth-live:latest`,
against the shared worktree at `d3630006`. **Exit 0.** Eleven steps ran:
build, vet, gofmt, staticcheck, `-race` across the module, `examples/counter`'s
own module, the gate's own tests, the FR-65 surface delta, the client size gate,
and the two that announce themselves as skipped for want of a context this
invocation does not have.

```
==> verdict
skipped (needs a context this invocation does not have):
  - codegen reproducibility (FR-7)
  - client runtime suite (NFR-4)
every gate this invocation could run is green
```

The soak class is not in `ci.sh` by default, so it was run separately, and it
is the run that changed most in this work:

```
$ dis run env GOTTHLIVE_SOAK=1 go test -race ./test/... -count=1 \
      -args -ginkgo.label-filter=soak
ok  github.com/candacelabs/candace/pkg/gotth/test/internal/conformance  14.872s
```

The same gate was run against the pristine export used for the mutations, with
the same result, before and after each mutation was reverted.

## 7. Sign-off

C-22's residue is closed on its own terms: the three clauses named in the
orchestrator log each observe a `RESYNC`-origin snapshot, and each was shown to
go red when the run holds none. D-14 is new, reported, unfixed, and
pre-registered as an executable spec.

— QA-1, 2026-08-04

---

## 8. Addendum — D-14 closed (DEV-1, 2026-08-04)

Appended rather than folded into §4: QA-1's report is the record of what was
found, and this is the record of what was decided. The defect text above is
unchanged.

### 8.1 The boundary, measured

QA-1's fix shape offered two directions — reject in `live.New`, or clamp in
`Normalize` — and named 1024 (`protocol.CoalesceFlushCeiling`) as the number.
The number is wrong by one, and that was found by running the repro across the
boundary rather than by reading it:

| `CoalesceFlushAt` | widest union on the wire | union on the wire / swallowed | library-bug log lines |
|---:|---:|---:|---:|
| 512 (default) | 513 | 3,978 / 3,978 | 0 |
| **1023** | **1024** | **3,982 / 3,982** | **0** |
| 1024 | 899 | 907 / 3,982 | 3 |
| 1025 | 896 | 904 / 3,982 | 3 |
| 4000 | — resync refused — | 8 / 1,385 | 3 |

*(4,000 unacknowledged `qa.increment` events followed by a `ResyncRequest`, the
same run QA-1 used at 1,400; the 4000 row is QA-1's figure, reproduced.)*

The flush trigger counts the transitions already deferred. The frame it forces
carries **one more** identifier than that, because `takePending` folds in the
origin of the transition being emitted at the time. So the largest setting
whose flush produces a frame the schema accepts is `CoalesceFlushCeiling - 1`,
and at exactly that setting the widest union on the wire is exactly the ceiling
— which is the row above, not an argument. Any fix written against the round
number 1024 would have shipped the defect one value narrower.

### 8.2 The three decisions

**Reject at construction, do not clamp.** `live.New` returns
`*ConfigError{Field: "Limits.CoalesceFlushAt"}`. A clamp keeps the process up
and makes the running limit different from the configured one, so the
operator's next reading of their own configuration is wrong. L9-1 has already
ruled that way once this checkpoint, on `normalizeMount` — *"silently rewriting
more of it… would make this function a second, quieter router"* — and checklist
§5.4's posture is default-deny. `New` already fails at startup for a missing
hook and a duplicate fragment identifier; this is the same class of mistake.

**The bound is documented, not exported.** `Limits.CoalesceFlushAt`'s godoc now
states the range and the arithmetic behind it. No exported constant was added:
`internal/session.MaxCoalesceFlushAt` holds the derivation, and an application
that wants the largest legal value is exactly the application that should read
the paragraph. `live` stays at 45 identifiers and 49 fields, and
`tools/apisurface` agrees.

**Negatives are rejected across the whole struct; upper bounds are not
invented.** QA-1's *"every other `Limits` field is unvalidated on the same
path"* is answered in one direction and deliberately not in the other. Zero
already means "take the default", so a negative is meaningless in every field,
and two of them are channel capacities where a negative is
`panic: makechan: size out of range` at the first connection rather than an
error at all. Upper bounds are a different question: `CoalesceFlushAt` is the
only field with a *protocol* ceiling behind it, and a library that capped
`MailboxDepth` would be deciding an operator's capacity for them. Completeness
of the negative check is held by a spec that walks `Limits` by reflection and
requires `New` to name each field in turn, so a field added without a decision
about its range fails there.

### 8.3 Where the pending spec went

QA-1's `PIt` — *"holds P5 under every CoalesceFlushAt an application is allowed
to set"* — is now two running specs in
`test/internal/conformance/limits_test.go`, under *"The coalescing flush
trigger an application configures"*: one holds P5 at
`session.MaxCoalesceFlushAt` over 3,000 unacknowledged transitions and a
resync, and one holds that 4000 never reaches a session at all. The second is
what makes the first's *"every value an application is allowed to set"* a
closed statement. `provenance_test.go` keeps a comment where the `PIt` was.

### 8.4 What this does not close

**An application-supplied `Event.Contributing` is still unbounded**, and it
reaches the same H-4 emission failure on **default** limits. `live.Event`
documents `Contributing` as *"the one causal field an application sets"*; the
emit path checks only that no entry is zero, and `deferPatch` folds a deferred
origin's `Contributing` into `pendingIDs` alongside its event identifier. So
the "+1" arithmetic that fixes `MaxCoalesceFlushAt` holds for the edges *this
library* adds and not for the ones an application adds.

Measured, on `DefaultLimits`, with no coalescing involved at all — one effect
emitting one event whose `Contributing` holds 1,200 identifiers:

```
PROBE patches=0 errors=1
PROBE error code=INTERNAL msg="the server could not encode an update" fatal=false
```

The transition happened, the state changed, and the client received a
non-fatal error instead of the patch. It is the same failure as D-14 reached
by a shorter route. Reported to the
orchestrator as a separate finding rather than fixed here: bounding it changes
an exported contract, and where the bound belongs — the emit path, the flush
trigger, or both — is a design question and not a validation one.

— DEV-1, 2026-08-04
