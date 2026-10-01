# PM-1 — checkpoint-3 scope pass: four rulings other gates were waiting on

| | |
|---|---|
| **Owner** | PM-1 (scope) |
| **Date** | 2026-08-04 |
| **Ruled in** | [PRD](../PRD.md) §9, amendment log **v0.6** (five rows) |
| **Answering** | QA-2 **§4.8**, BENCH-1 **Q-E** and **Q-D**, DEV-3's Phase-4 line count, and the carried stale-number sweep |
| **Not this document** | the checkpoint-3 gate report. It comes after QA-2 and L9-1 report, and nothing here ticks a Phase 3 box |

Every ruling below was blocking somebody else's gate, which is why they land
**before** the gate report rather than inside it. A gate agent who has to guess
PM-1's answer is a gate agent making scope decisions.

The rulings themselves are in the documents where they gate — the PRD, the
operator-questions file, `protocol.md`'s Q-P1 record. This file holds the
decision, the cost, and the things the pass turned up that are **not PM-1's to
fix**, so they are found by the next person rather than by the next reviewer.

---

## 1. PRD Phase 3 case 8 loses its second clause; Q-P1 stays closed

**Ruling: strike "no double state transition". Do not re-open Q-P1.** Full
argument in PRD §9 v0.6 row 1.

**Why, in one line:** the library never emits a duplicate, so a second identical
frame is always sender-originated, and deduplicating it would collapse two
genuine user intents while buying nothing against an attacker who mints their
own nonce.

I checked that first claim rather than taking it from RFC §8.5. `client/runtime.js`
has no queue, no pending buffer and no resend — `send()` returns `false` when the
socket is not `OPEN` and the event is dropped — and that is still true through
the reconnect state machine that landed since checkpoint 2. If a future change
adds an outbound retry queue, **this ruling's premise is gone and case 8 must
come back to PM-1.** That is the trigger; it is not a general re-opening.

**The consequence for a user who writes a payment button**, stated plainly
because the ruling is worth nothing if this is left implicit:

| what happens | intents | charges | whose problem |
|---|---|---|---|
| user double-clicks Pay | two | two | the application's, and the library is behaving correctly |
| effect commits, patch is lost, user retries what looks like a failure | **one** | **two** | the application's, and **at-most-once does not prevent it** |

The second row is the one that matters and it is not a duplicate-frame problem
at all — it is RFC §8.5's own stated leak, and no nonce on `Event` would touch
it. Which is a second, independent reason re-opening Q-P1 would have been the
wrong spend: it would have addressed the case that is already correct and left
the case that actually charges twice.

**The mitigation is FR-77**, new in §5.A, and its placement is the whole point.
The contract already existed in RFC-0001 §8.5 — a design document that nobody
shipping a checkout will ever open. FR-77 requires it on **the docs page that
introduces effects**, with both double-execution paths and a worked example on
an effect that moves money rather than on a counter, where getting it wrong is
invisible. FR-59's "when not to use this" page gains the bound.

**What this costs, recorded because R-12 claimed we had avoided it.** R-12's
argument for at-most-once was that at-least-once "requires every application
reducer to be idempotent". True, and incomplete: idempotence **moved** rather
than left, from *every reducer* to *every externally-committing effect*. Much
smaller set; also the expensive one. R-12 is restated on that basis.

Also closed in the same pass, because leaving them open would have left three
documents disagreeing: PRD §7.2 **Q6** (answered by RFC §8.5 since cycle 2, never
marked), and `protocol.md` **Q-P1**, which was two phases past its own "before
Phase 2 examples are written" date with PM-1 named as half its owner.

---

## 2. The Q-BENCH ids now exist, fenced, and one of them lost its reason

**Ruling: add `Q-BENCH-1` and `Q-BENCH-2` to
[`OPERATOR-QUESTIONS.md`](../OPERATOR-QUESTIONS.md) as an explicitly fenced
series that is not Q-1..Q-7.**

The citations are real and they dangled: `bench/apps/counter/next/src/lib/store.ts`
cites `Q-BENCH-1`, `.../variant.ts` cites `Q-BENCH-2`, and `bench/README.md`
**Q-D** had already found both, said the file "has Q-1..Q-7 and no bench
series", and correctly noted that fixing it was outside its own write scope.

**Neither was an operator decision and neither entry pretends otherwise.** Both
are defaults BENCH-1 took. The series therefore opens with a paragraph saying so
and naming the real owner per entry, because "only the operator can settle this"
is the single contract that file has and appending two bench defaults under it
unmarked would spend that contract for free.

**The alternative I did not take:** renumbering the two citations to point at
`bench/README.md`, which is where both defaults are actually documented. It is
outside PM-1's write area this turn, but that is not the main reason — a default
taken without the person who could ratify it is exactly what this file exists to
record, and the file's own preamble says so.

**The finding, which is worth more than the fix.** Q-BENCH-1 records the counter
as **global** on R-6's reasoning that "the app that gets measured is the app
that exists" — i.e. `examples/counter`. Ruling 3 below ratifies the reading
under which the measured app is **not** `examples/counter`. So the default
survives and **its stated reason does not**: nothing forced a purpose-built
bench counter to be global, and §2.1 F-CTR-1 says "server state, per session".

Both stacks are global *together*, so this is not a fairness question between
them. It is an **E1 conformance question against a frozen §2**, and §2 moves
only by a §12 amendment. **Owner: QA-2. Needed before Phase 5 collection
starts** — it is cheap now (two small app changes plus an amendment) and a full
re-run of every counter cell afterwards. Recorded and deliberately not resolved
in either direction here: PM-1 may not amend a frozen spec, and guessing which
way QA-2 will go would put a number in the report that nobody signed.

---

## 3. Bench ambiguity Q-E is RATIFIED, and FR-70 was the document that had to move

**Ruling: ratified. It is not a fairness change under §5. It *was* a PRD
contradiction, and that is the half that needed PM-1.**

BENCH-1's reading — `bench/apps/*/gotth/` are distinct programs from
`examples/*`, built to §2's feature tables — is confirmed. Two apps are built on
it (`2bf564c5`, `58b3dcc4`) and a third is in flight.

**Why it is not a §5 fairness change.** §5's rules govern how each stack is
*configured and run*: production defaults (5.1), machine and isolation (5.2),
quarantine (5.3), the Next.js pessimization audit (5.4), route dynamism (5.5),
observability (5.6), warm-up (5.7). Q-E governs which *program* is built,
applies the same construction rule to both sides, and §10 already puts both
stacks' apps under `bench/apps/<app>/{gotth,next}/`.

It is also the only reading under which the spec's own equivalence rules can be
satisfied. **E1** requires the same product surface and **E2** the same
interaction set driven by one harness against identical `data-bench-id` hooks;
`examples/chat` has one room, no typing indicator and no unread badges, and
`examples/dashboard` has meters and alerts rather than regions A–E. An
examples-based gotth-live side fails E1 against any Next.js app built to §2.3.
Refusing Q-E would not produce a fairer benchmark — it would produce one that
cannot satisfy its own definition of equivalence.

**Where the contradiction actually was: FR-70**, which said the Next.js app is
built "to the same product surface as gotth-live's **three examples**". That
is a PRD requirement, it is mine, it predates the equivalence spec, and three
committed apps already contradict it. Ratifying Q-E without amending FR-70 would
have left a requirement that three deliverables violate — which is the failure
the amendment log exists to prevent. FR-70 as amended points at §2's frozen
tables and states that `examples/` are FR-60/61/62's separate DX deliverable.

**What ratification costs, and what I attached to it.** "Same surface as the
examples" carried an implicit promise that the measured gotth-live app is one a
reader can find and run. Cutting the phrase loses it, and §5.4's pessimization
audit protects only the **Next.js** side — **nothing audits a gotth-live app
tuned for its own benchmark.** So FR-70 now requires: bench apps use only
consumer-reachable API (no `internal/` import, no build tag, no unexported hook,
no undocumented configuration), and any bench-driven construction choice that
could move a measured dimension is declared in the method section beside the
Next.js side's declared deviations. That is a checkable obligation replacing an
implicit guarantee, which is the most I can do without an audit that does not
exist.

**Ruled now, on purpose, while three apps depend on it and not five.** BENCH-1
asked for confirmation and called it "the largest open item"; discovering it at
Phase 5 would invalidate three apps and a table.

---

## 4. The stale-number sweep, and what needs somebody else's number

Re-measured rather than copied. `tools/minify` was run in the project image at
this HEAD; the rest was grepped and re-read.

| Number | Was | Is | Where fixed |
|---|---|---|---|
| Client bundle, gzipped | 3,961 B | **4,360 B** | §3 G3, NFR-2, R-2 |
| Client bundle, minified | 9,343 B | **10,178 B** | §3 G3, NFR-2 |
| Headroom | 8,327 B / 67.8 % | **7,928 B / 64.5 %** | §3 G3, NFR-2, R-2 |
| R-2's remaining booked additions | two | **one** (§8.4's reconnect state machine landed at +163 B) | R-2 |
| §3 G2 | "nothing has been measured at all" | **measured; misses the gate** | §3 G2 bullet, R-10 |
| Phase 2's ticked size box | bare figure | marked a **dated gate record** | Phase 2 |

`client/SIZE.md` agrees to the byte, which is worth knowing and is not the same
as having trusted it.

**The +399 B since checkpoint 2 is the part with a finding in it.** Three
landings: the D-29 resync re-arm (+223 B), RFC §8.4's reconnect state machine
(+163 B), the FR-54 key filter (+13 B). Only the middle one was **booked** in
R-2. The other +236 B arrived from defect fixes nobody had budgeted, and R-2 now
separates budgeted from unbudgeted growth, because with three checkpoints of
data (+87, then +399) the unbudgeted line is the one that will decide whether
64.5 % headroom is comfortable or merely large today.

**Checked and *not* changed**, recorded so it is not re-run: **G1's bullet.**
Checkpoint 1's 3.20 ms p50 / 4.80 ms p99 event→paint and the 91.86 µs
protocol-level floor are still the newest latency figures in the repository — I
grepped the checkpoint-2 and checkpoint-3 QA documents and neither measures
latency — so the "loopback, one host, headless, NOT PRD G1" labelling stands
exactly as written. Phase 0/1/2's status blocks were re-read and still describe
the state the project is in.

### Owed by somebody else, or by me, and named

| Item | What is needed | Owner |
|---|---|---|
| **The G2 figure itself** | §3's G2 bullet and R-10 now say a baseline exists and misses the gate, and deliberately carry **no copy of the number** — DEV-1 had a re-measurement in flight while this swept, and a second copy of a moving figure is how v0.4 row 7's failure repeats. `docs/bench/g2-baseline.md` is the single source. **When DEV-1's landing settles, the PRD needs no edit** — that is the point of pointing at the document — but the Phase 3 box does need ticking at the gate | DEV-1 (the figure), PM-1 (the gate box) |
| **The G2 remedy** | A measured miss of this size is a scope decision and nobody has taken it: cut the attributed cost, accept the miss with an ADR carrying the measurement, or change what v0.1 claims. RFC §6.1.2 fixes the *procedure* (the target does not move; a benchmark-method change is never a remedy) and says nothing about *which* remedy. The largest attributed term — default-on observability — has **no budget line anywhere in this project**, which is itself worth a decision, since G6 is a product goal and its cost has never been costed | **PM-1**, with DEV-1's measurement. Phase 5, and it should not wait for Phase 5 |
| **FR-53's 46 vs 30** | The counting rule is now fixed (Go **plus** templ) and Phase 4's box says so, so the docs-alone gate is not where it gets decided. The miss is real. Twelve of the 27 Go lines are the eight `Config` fields `live.New` requires, so a large part of the overage is an **API** finding, not a documentation one. Raising 30 to fit is pre-registered as unavailable, and specifically not in the same pass that measured the miss | DEV-1 (reduce ceremony), then PM-1 (amend 30 only with an argument) |
| **Q-BENCH-1's conformance question** | §2.1 F-CTR-1 vs two global counters — see §2 above | QA-2, before Phase 5 collection |
| **Phase 1 and Phase 3 exit boxes** | Still unticked, and still for the reason my predecessor gave: recording a phase as exited is a gate action. Phase 3's three v0.5 debt boxes are now individually deliverable — the G2 baseline landed with the §6.2 correction — but they are ticked at the gate report, with the evidence, not here | PM-1, at the checkpoint-3 gate report |

---

## 5. One thing I did not rule on, and why

**Case 8's box is not ticked.** The clause is struck and the semantics are
stated, which unblocks QA-2 — but FR-77's documentation half is Phase 4 work
that does not exist yet, and a Phase 3 box closed against a requirement whose
delivery is owed in the next phase should be closed by the gate report that can
see both, not by the amendment that created it. Flagged the same way v0.5 row 5
flagged its two amended boxes: a criterion closed against a requirement that
moved in the same landing should say so itself.

— PM-1, 2026-08-04
