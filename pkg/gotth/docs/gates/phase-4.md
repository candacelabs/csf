# Phase 4 interim gate report — gotth-live

| Field | Value |
|---|---|
| Project | **gotth-live** — a Go library for server-driven live UI: state and rendering stay in Go, the browser holds one long-lived connection, events go up and re-rendered HTML fragments come down |
| Phase | **4 — DX & docs**. Not consolidated with 1–3; gates on its own |
| Report owner | **PM-1**, who owns scope and the requirements document |
| Date | 2026-08-05 (**revision 6**, written 2026-08-06) |
| Tree | **`9efb7e5b`** on `dev-/gotth-live-orchestrator-c3efc4`; PRD v1.5 lands with this revision. **Ten landings, one ruling and one grade moved under revision 5's record, and this time the count moves** — DEV-1's `0b31e67d` and `42b4e0e6` (Part A), `0b9e32e7`/`2311280b` (Part B), the parity and documentation sweep `1d0b5d14`…`8dc284e0` and `691e7f30`…`d5b20a9e`, L9-1's `d60042ae`/`f4b017ad`/`eb4971c6`, and **QA-1's grade at `9efb7e5b`**; §2.9 says what I ran at this tree and what carries somebody else's name. Revision 5 graded `e751f6de`, revision 4 `5d665226`, revision 3 `b04ba138`, revision 2 `134e69c5`, revision 1 `8a06cb04` |
| Verdict | **FINAL FOR PHASE 4 — the gate was held and PASSED, and PHASE 4 EXITS.** **THIRTEEN of thirteen exit boxes ticked, NONE open** *(was twelve and one at revisions 4 and 5; eleven and two at revision 3; six and seven at revisions 1 and 2)*. **The thirteenth box ticks on QA-1's grade and not on this report's reading**, [`docs/qa/phase-4-grading.md`](../qa/phase-4-grading.md) §11 at `eb4971c6`, committed at `9efb7e5b`: **PASS WITH CONDITIONS**, and **four conditions — Q-5…Q-8 — travel with the tick and are not discharged by it**. Revision 5's sentence was *"the count does not move, and saying that plainly is the whole job"*; this revision's job is the opposite one and it is not easier, because **a tick that swallows a named open row is how the row stops being found.** §8.5 |
| Correctness sign-off | **QA-1 — SEVEN boxes carry a QA-1 grade and all seven pass. The grade this phase was waiting for has been given.** The docs-alone gate ([`docs/qa/phase-4-docs-alone.md`](../qa/phase-4-docs-alone.md) §6) at `452e1e74`, plus **boxes 12, 7, 6 and 8** in [`docs/qa/phase-4-grading.md`](../qa/phase-4-grading.md) at `954afa9a`/`3fe09676`, plus **box 2** in the same file's §10 at `5d665226`, plus **box 3** in the same file's §11 at `eb4971c6`: **12 PASS** (one condition, discharged), **7 PASS** (no conditions), **6 FAIL then PASS on remediation**, **8 PASS** (one condition, closed), **2 PASS WITH CONDITIONS** — four, **Q-1…Q-4** — and **3 PASS WITH CONDITIONS** — four more, **Q-5…Q-8**. **Eight QA-1 conditions are open across two boxes and PM-1 has discharged one of them: Q-7, which is PM-1's own and is the PRD.** Boxes QA-1 gates and has still **not** graded: FR-57's dev reload, FR-66 and FR-68 — all three ticked under §5.2's rule, which is unchanged and is still the one to reverse if the orchestrator prefers the stricter reading |
| Technical veto | **L9-1 — exercised four times, and the fourth ends with the reviewer publishing three defects in their own pre-registered constraints.** *(Revision 6: `d60042ae`, `f4b017ad` and `eb4971c6` accept the Part B landing and discharge FR54-1…FR54-6, -8, -10, -11, -12 and -13; FR54-7 and FR54-9 travel behind. **L9-1 amended their own C-3 byte budget after finding it had priced the shape C-9 forbids**, and disclosed at [`reviews/fr-54.md`](../reviews/fr-54.md) §14 that **three of their nine pre-registered constraints were defective as written** — C-1's count, C-3's budget and C-6's sufficiency. All three were caught by the people building against them. §8.5.)* The three earlier exercises: `bdf91971` on the FR-20 register (note at [`docs/reviews/phase-4-exceptions.md`](../reviews/phase-4-exceptions.md), `cdb30b5d`): E-1 **accepted** and the scope ruling **refused**; E-2 **closed as fixed and retained**; three of the register's own numbers corrected before signing. `af4585b4` → `40b66b54` on the page shell under FR-65 ([`docs/reviews/page-shell.md`](../reviews/page-shell.md)): **ACCEPT WITH CONDITIONS**, eight of nine pre-registered constraints passed and **the ninth failed on its *claim***, then **ACCEPT** once discharged, verified by six probes of L9-1's own and **seven mutation kills, seven for seven**. And `e751f6de` on FR-54 ([`docs/reviews/fr-54.md`](../reviews/fr-54.md)): **Part A ACCEPT WITH CONDITIONS** — six, **FR54-1…FR54-6**, three of which L9-1 names as blocking the box — and **Part B RULED**, accepting `Bind.NoModifiers`/`Bind.PreventDefault` and refusing the full modifier set with a three-limbed re-open trigger. **Four PRD amendments routed to PM-1 across v1.0–v1.2, and a fifth is owed for the Part B ruling** — §6 |
| Requirements applied | [PRD](../PRD.md) **v1.5**, landing with this revision: FR-54's helper vocabulary amended to six `Bind` fields, its clause-3 refusal and three-limbed re-open trigger recorded **in the requirement** rather than only in a review, its three-failure grading block closed, and **box 3 ticked — Phase 4 goes twelve of thirteen → THIRTEEN of thirteen and EXITS**. Revision 5 applied v1.4, revision 4 v1.3, revision 3 v1.0, revision 2 v0.9, revision 1 v0.8 |
| Format precedent | [`docs/gates/checkpoint-3.md`](checkpoint-3.md), itself following [`checkpoint-2.md`](checkpoint-2.md) and [`phase-0.md`](phase-0.md) |

**Revisions, because a gate report that is silently overwritten is a gate report
nobody can audit.**

| Rev | Tree | What changed | Why |
|---|---|---|---|
| 1 | `8a06cb04` | The original thirteen-row grading: six met, seven not | The Phase 4 interim gate, PRD v0.8 |
| **2** | **`134e69c5`** | **§3 rows 7, 8, 12 and 13 re-graded; §4.7, §4.8, §4.12 and §4.13 rewritten; §5.3–§5.5 added; §6's table rebuilt; §7.3–§7.6 added; §8's critical path replaced.** Every superseded verdict is shown beside its replacement rather than deleted | DEV-1, DEV-2 and DEV-3 landed boxes 7, 8, 12 and 13 in `9b457e56`..`134e69c5`. PRD v0.9 |
| **3** | **`b04ba138`** | **§3 rows 2, 3, 6, 7, 8, 12 and 13 re-graded — five of them to MET; §2.5 added; §4.2.1, §4.3.1, §4.6.1, §4.7.2, §4.8.2, §4.12.2 and §4.13.2 added; §5.6–§5.9 added; §6's table rebuilt again; §7.7–§7.9 added; §8.2 added; the closing reproduce block's stale ranges corrected.** **Two corrections are owed to this report's own record and both are made in §7.8** | QA-1 graded four boxes (`954afa9a`, `3fe09676`); L9-1 signed the register (`bdf91971`); DEV-1 shrank the quickstart (`cd2c4cac`..`fde707f0`); DEV-3 closed F-10 (`b04ba138`). PRD v1.0 |
| **4** | **`5d665226`** | **§3 row 2 re-graded to MET and row 3 re-stated; §1.3 added; §2.6 added; §4.2.2 and §4.3.2 added; §5.10 (the count gate's number, authorised) added; §5.6's failure-2 row and §5.8's mis-citation corrected beneath themselves, as is §4.13.2's; §6's table rebuilt a third time; §7.10 added; §8.3 added.** **This revision was owed for two revisions and it pays four debts, three of which are corrections to PM-1's own text — including §8.2's prediction, which was wrong** | QA-1 graded box 2 (`5d665226`) and drove FR-54's failure 2 (`97ab20fb`); L9-1 gated and then accepted the page shell (`af4585b4`, `40b66b54`); DEV-1 built it (`8680e8c5`..`679e6695`) and discharged three conditions (`cbad05d8`..`8be955e5`); DEV-3 corrected chat's F-3 (`e1a56a0e`). PRD v1.3 |
| **5** | **`e751f6de`** | **§3 rows 3, 4 and 5 re-stated and NO row re-graded; §1.4 added; §2.8 added; §4.3.3, §4.4.1, §4.5.1 and §4.6.2 added; §4.5's own "Illegal invocation" mechanism and its `CLIENT_EVENT` transcript line corrected beneath themselves; §6's table rebuilt a fourth time — three rows closed, one of them the oldest in the report — and seven added; §8.4 added.** **This is the first revision in which four landings and a technical ruling arrive and the box count does not move, and saying that plainly is the whole job** | DEV-1/DEV-2 fixed FR-54 failure 2 (`2ab18690`) and failure 3 (`b6bfe108`); DEV-2 put the browser loop in CI (`13a1ca1e`); PM-1 held the Phase 3 exit gate act (`f0690a2c`, PRD v1.4); L9-1 reviewed FR-54 and ruled on failure 1 (`e751f6de`). PRD v1.4 |
| **6** | **`9efb7e5b`** | **§3 row 3 RE-GRADED to MET and the count moves for the first time since revision 4; §1.5 added; §2.9 added; §4.3.4 added; §5.11 added — which of the thirteen ticked on whose grade, as §5.2 requires; §6's table rebuilt a fifth time — six rows closed and five added, four of them QA-1's conditions; §7.11 added — four corrections owed to this report's own record, two of them figures this report published as current and one of them found by running its own reproduce block; §8.5 added; the reproduce block's revision-5 figures corrected beneath themselves and a revision-6 section added.** **THIRTEEN of thirteen. Phase 4 EXITS, on QA-1's grade, with four of QA-1's conditions travelling with the tick** | DEV-1 discharged L9-1's Part A conditions (`0b31e67d`, `42b4e0e6`) and landed Part B (`0b9e32e7`, `2311280b`); DEV-1/DEV-2/DEV-3/BENCH-1 ran the parity and documentation sweep (`1d0b5d14`…`8dc284e0`, `691e7f30`…`d5b20a9e`); L9-1 accepted and discharged (`d60042ae`, `f4b017ad`, `eb4971c6`); **QA-1 graded box 3 (`9efb7e5b`)**. PRD v1.5 |

**Who the named roles are**, since they appear throughout: **QA-1** owns
correctness and can block a merge; **QA-2** owns resilience and performance and
can also block a merge; **L9-1** is the Principal Engineer and holds technical
veto; **DEV-1** is the server-core Go engineer, **DEV-2** owns the browser-side
client runtime, **DEV-3** owns the examples, the documentation and
interoperability with HTMX; **BENCH-1** owns the Phase 5 comparison apps; the
**orchestrator** runs the project.

---

## 1. Verdict

**Phase 4's gate has been held and it passed. Phase 4 does not exit.** Those two
sentences are not in tension and the order matters, because the PR body has been
saying for two rounds that the gate *"has not happened"* and that sentence is now
false — a stale row is a defect, and this report is where it stops being one.

QA-1 built a working counter from `docs/quickstart.md` alone in **2 m 12 s**, it
compiled on the first attempt, it ran in real chromium under real trusted mouse
input, and there were **zero source-diving breaches** — no library source, no
example, no test, no `git log`. Eight findings, none of them a blocker, three of
them high-severity friction. That is the box §6 calls **the gate**, and it ticks.

**Seven of thirteen boxes do not tick**, and only one of the seven is open on a
judgement anybody could argue with. The other six are open on work nobody has
done: `docs/exceptions.md` does not exist, there is no deployment page, no
error-message audit has been started, G11's clean-clone invocation has never been
run, the examples' "polished and documented" clause has been graded by nobody,
and "complete" in FR-54 has never been defined. **Naming that distinction is most
of the value of this report**: a phase blocked on six chores and one measurement
is a different object from a phase blocked on a disagreement, and the next turns
should be able to see which they are picking up.

### 1.1 What revision 2 changes, and the sentence it turns on

*(Added 2026-08-05, revision 2, at `134e69c5`. The paragraph above is revision
1's and it is left standing, because four of the six chores it names are now
done and the record of them having been outstanding is the reason the next four
paragraphs are worth reading.)*

**Four of those six chores are done.** DEV-2 ran G11 for real; DEV-3 wrote the
deployment page and the security page; DEV-1 wrote the error audit and drafted
`docs/exceptions.md`. **The count of open boxes is still seven and none of the
four ticks.**

**The sentence this revision turns on is that work landing is not a gate
passing.** FR-58, FR-59 and G11 each carry `Gate: QA-1`, and QA-1 has graded none
of the three. FR-20 carries `Gate: L9-1`, and every sign-off line in the register
DEV-1 drafted is unsigned — DEV-1 put that in the file's second line themselves,
before anybody asked them to.

**The obvious objection is that revision 1 ticked five boxes QA-1 had not signed,
so why not these.** §5.2 stated that rule so it could be attacked, and it does not
reach here. FR-44, FR-57, FR-66, FR-68 and FR-77's docs half were ticked on
evidence PM-1 could **verify by reading**: a byte count against a ceiling, a
diff that is empty, four sentences a page either contains or does not. These four
ask for a **judgement** — is this audit good enough, is this docs set complete,
is this run the evidence G11 wanted, is this deviation acceptable — that the
requirement assigns by name to somebody who is not me. Supplying it would not be
applying revision 1's rule; it would be taking over four gates in the pass that
received the work they gate. **If that reading is wrong, it is wrong in the
direction of one more gate rather than one fewer**, and §5.2's offer stands: the
two ticks to reverse are still FR-44's and FR-57's, and reversing them still
changes no other row.

**What did change is the shape of the phase, and it is worth as much as a tick.**
A phase blocked on four chores is a phase that needs four turns of work. A phase
blocked on two signatures is a phase that needs somebody to be asked. §8 says who.

**The one that is a measurement is FR-53**, and it lands exactly where §6 said it
would. The box is a conjunction: ≤15 minutes and ≤30 lines. The first half passes
at 2 m 12 s. The second misses at **46** — 27 Go plus 19 templ — which is the
number v0.6 pre-registered, which QA-1 reproduced independently at `452e1e74`,
and which **I re-counted myself at `8a06cb04`** after DEV-3's seven documentation
fixes, getting 27 + 19 again. Seven fixes moved the line count by zero. The box
stays open, the threshold does not move, and §4.2 says what would close it.

**What this report is not.** It is not an exit review: no exit review has been
convened, QA-1 has signed one box and not the phase, and L9-1 has not been asked
for anything here. It is not a claim that CI is green at HEAD — §2.1 says which
tree each green belongs to and which run is executing as I write. And it is not a
record of my own measurements, with two exceptions I flag rather than blur: the
line count in §4.2 and the four-clause check of FR-77's pages in §4.9 are mine,
taken by reading the tree; everything else in §3 has another agent's name and a
commit beside it.

**The standing rule on this project is that a gate is what you ran, not what you
read.** I ran none of the toolchain work this round — there is no Go on this host
outside a container and a whole-gate run was executing on the machine throughout
— so §3's numbers are other people's with their owner attached, in the form
checkpoint 3 used. What I did do is the part needing no toolchain and that nobody
else had done: I opened the tree and checked the claims, which changed four rows
of this report and produced §7's two findings.

### 1.2 What revision 3 changes — the queue emptied, and the two boxes left are the two nobody could have cleared by working

*(Added 2026-08-05, revision 3, at `b04ba138`. §1 and §1.1 are left standing.
§1.1's closing claim — that the phase was blocked on people rather than on work
— is the thing this revision tests, and it turned out to be true.)*

**Revision 2 ended with a sentence: *"The single act that moves this phase
furthest is asking QA-1 to grade."* It was asked, and it moved five boxes.**
QA-1 graded four and passed four. L9-1 signed the register. **Phase 4 goes from
six of thirteen to eleven of thirteen in one turn, and not one box ticks on my
reading of anybody's work** — which is the claim §5.2 and revision 2's §8.1 both
staked, now paid off rather than argued.

**The four QA-1 grades are worth reading for how they were taken, not for what
they concluded.** Each tested the artifact's ability to **fail** before crediting
it with passing. QA-1 re-implemented FR-58's enumeration rule **from the audit's
prose** in their own AST program rather than re-running DEV-1's, and got 117
package-for-package at the graded tree. They built a **fourth** G11 negative
control of their own — an image identical to the valid one but with a `node` shim
on `PATH`, which the runner refuses. They drove five controls through DEV-3's new
spec-count check, including the vacuous-pass case a lesser guard would leave
open. And they wrote their own probe **and its control** for the architecture
page's least checkable sentence. **A check that cannot fail is indistinguishable
from one that passes, and this project has now caught six of the former.** None
of these four is one.

**Box 6 is the one to read, because it failed first.** QA-1 graded it **FAIL** on
six places where the tree contradicted itself after the `livetest.Client`
migration — including a README telling a reader that the library's supported
testing API did not exist, beside a file using it twenty-five times. DEV-3
remediated and put the three spec counts under a `ReportAfterSuite` check instead
of under prose; QA-1 re-graded **PASS** — and **adjudicated one of their own five
prescriptions in DEV-3's favour**: *"my finding stands; my prescription was
wrong."* Complying with it would have made a FRICTION item state something false,
which is the exact defect class the box failed for. **DEV-3 raised that against
the merge-blocking gate with the evidence attached rather than complying
quietly**, and QA-1 recorded a **seventh** instance they had missed in their own
first pass in the same section.

**The two boxes that remain are the two no amount of turning a handle would have
closed, and they are open for opposite reasons.**

- **Box 3 (FR-54) was open on an undefined word and is now open on evidence.**
  Revision 2 called it *"debt with my name on it and it did not move."* It has
  moved: "complete" is defined in the PRD, in FR-55's shape, and **the helper set
  fails the definition on three named gaps**. §5.6 is the ruling, and it says why
  I rejected revision 2's own proposed shape as circular rather than adopting it.
- **Box 2 (FR-53) is the only box open on a measurement, and it shrank without
  closing.** DEV-1 did the thing this box has asked for since v0.6 — **the app
  shrank, the number did not move** — 46 → **39**, the miss from sixteen lines to
  nine. §5.8 is the argument I have owed since v0.6, and its answer is that **30
  was never reachable**, for a reason that is a conflict between two decisions
  this project made rather than an engineering shortfall.

**And revision 2's structural finding is resolved rather than restated.** §7.6
said box 13 could not tick before Phase 5 by its own text, which made Phase 4
exit after a Phase 5 event. **§5.7 splits it.**

**Three corrections are owed to this report's own record and §7.8 makes all
three** — a verdict in my own §2.4 that was wrong when I wrote it, a reproduce
block that reproduced nothing, and **a condition this report has carried as
blocking for two revisions after it was discharged.** In a report whose §7.5 is
titled *"Four numbers in this landing did not reproduce, and the fourth is this
report's own"*, three more arriving is not an embarrassment to bury — it is the
section working, and the third was found only because I checked a row instead of
copying it.

### 1.3 What revision 4 changes — the box that had been open longest closed the way this report predicted it would not

*(Added 2026-08-05, revision 4, at `5d665226`. §1, §1.1 and §1.2 are left
standing. **§1.2's closing sentence — that box 2 is "the only box open on a
measurement" and "most likely closes by amendment" — is the claim this revision
falsifies, and §8.3 is where I pay it rather than here.**)*

**Twelve of thirteen boxes are ticked. Phase 4 does not exit. The one box left is
FR-54.**

**Box 2 — FR-53 and G7 — is green, after being open since v0.6.** It was graded
by **QA-1** ([`docs/qa/phase-4-grading.md`](../qa/phase-4-grading.md) §10,
`5d665226`): **≤15 min PASS at 2 m 29 s**, **≤31 lines PASS at exactly 31 with
margin zero**, **G7 discharged**, **PASS WITH CONDITIONS** — four of them,
**Q-1…Q-4**, which are QA-1's and travel with the tick. **PM-1 graded nothing**;
what §4.2.2 records is the application of somebody else's grade, and §5.10 is the
one thing in this revision that is genuinely a PM-1 act.

**Read this revision for the order the box closed in, not for the fact that it
did.** DEV-1 built a library-owned page shell. **L9-1 gated it before it was
counted**, against **nine constraints they had pre-registered before the artifact
existed** — and failed one of the nine, on its *claim* rather than its behaviour:
the symbol's central justification was that the `InspectorScript`-above-runtime
ordering was made *inexpressible*, and a head extension carrying `live.Script`
falsified that, in five places at once including the ledger row that spends an
identifier on it. DEV-1 discharged it **in code, at +0 exported identifiers**,
taking the harder of the two routes L9-1 offered on the ground that the cheaper
one would have demoted the claim to *"inexpressible unless you hand it a runtime
tag"*. L9-1 then broke the component **seven ways** in a throwaway copy and
**seven mutants died, each to the spec that owns that behaviour and to no
others**. Only then did QA-1 count. **A gate held before the measurement, on
constraints written before the artifact, is the difference between this tick and
a tick.**

**And the number arrived exactly on its target, which is the figure to be most
suspicious of.** 31 against ≤31, margin zero. Three parties treated it that way
and none of them found shopping: **31 was costed at PRD v1.1 from `validate`'s
required fields and the shape of an HTML document, at a tree where
`grep -rn Document live/` returned nothing**; L9-1 disclosed *before the build*
that two of the nine constraints could each cost a sixth line and that they would
report a floor of 32 if they did; QA-1 established that their counting method can
return numbers other than 31 by running it across six commits and reproducing 46,
46, 39, 39, 31; and **PM-1 evaluated all five of FR-53's re-open triggers and
recorded that none fires** (PRD §5.I (e)), so the budget did not move in either
direction in the pass that closed the box. **At 32 the budget would not have
moved and the amendment would have been withdrawn with the box open. That branch
was reachable on the day the shell landed.**

**Three corrections this report has owed are paid here and all three are mine.**
§8.2's prediction that box 2 closes by amendment (**wrong**, §8.3); the
`live.LocalDevelopment` mis-citation at §5.8 and §4.13.2 (the last two of six
sites, waiting since PRD v1.2); and **the stale count** — QA-1 §10.4 records that
this report was *the only document in the tree outside the dated rulings still
stating a count of 39 against a budget of 30 in live text*, which is §7.10. **A
fourth was found while writing this revision**: §5.6's failure-2 row attributes
the word *"a wart"* to the library's godoc and the godoc does not contain it.

**What did not change.** No other box moved. FR-54 is still open and still fails
on three named gaps, two of which moved without closing — §4.3.2. **Every box
QA-1 or L9-1 gates is still theirs**, and the offer §5.2 has carried since
revision 1 still stands.

### 1.4 What revision 5 changes — four landings, one ruling, and the count does not move

*(Added 2026-08-05, revision 5, at `e751f6de`. §1, §1.1, §1.2 and §1.3 are left
standing. **This is the revision most at risk of being read as a phase exit, and
§7.2 exists because a status line that under-reports is a defect while a status
line that over-reports is a defect nobody files.**)*

**Twelve of thirteen boxes are ticked. Phase 4 does not exit. The one box left is
still FR-54, and it is not closer to ticking than it looks — it is exactly as
close as its owner says it is, which is three conditions and one grade away.**

**Two of the three conditions §6 carried are discharged this turn and the third
is not.** `examples/chat` now implements Escape-to-clear and its `view.templ`
comment says the library can do what it does (`b6bfe108`); **DEV-2's browser loop
is in CI** (`13a1ca1e`) — the item this report has called the oldest unaddressed
thing in it for four consecutive revisions. What is **not** discharged is box 2's
**Q-1/Q-2** remediation at `f555f3b5`: it landed, I re-derived the count at it,
and **it is still QA-1's to discharge.** I have now said twice that I will not do
it for them and this is the third time, in the revision where doing it would cost
me nothing and buy the appearance of a finished phase.

**Box 3 does not close, and the reason is a distinction this revision turns on:
failure 1 is DECIDED, not FIXED.** L9-1 ruled on it at `e751f6de`
([`docs/reviews/fr-54.md`](../reviews/fr-54.md)) and the ruling is good — both
refusal arguments standing on this report's own §5.6 were **aimed at the wrong
target**, and L9-1 says so in §10 rather than adopting the convenient half.
`Bind.NoModifiers` and `Bind.PreventDefault` are **ACCEPTED** at +0 exported
identifiers, +2 fields and +34 gzipped bytes; the full modifier set is **REFUSED**
with a three-limbed checkable trigger. **But the accepted surface does not exist.**
It was measured on a prototype in a container's `/tmp`, against nine constraints
pre-registered before the artifact — and one of those nine, **C-9**, was found by
building the prototype and is a defect *in the prototype*: `dispatch` calls
`preventDefault` **before** the `if (composing) return` IME guard, so the flag has
to sit after it or every CJK composer breaks. **A ruling is not a landing**, and
this report has spent three revisions on the sentence that work landing is not a
gate passing; the converse — that a decision is not work — is the same sentence
read the other way and it is the one revision 5 needs.

**Plus L9-1's six conditions, three of which they name as blocking the box.**
FR54-3 (an unbindable key now silently turns a debounce into a **throttle** — the
grammar got *worse* at `2ab18690` and that is the one thing a landing may not
leave behind unremarked), FR54-4 (the record's spec-keying is claimed in three
documents and pinned in none — L9-1 wrote the missing spec and the mutant that
should kill it is green against all 156 client specs and all 7 browser specs), and
FR54-6 (the Part B landing itself). **Twelve of thirteen is the count and it does
not move.**

**The Phase 3 resync box is discharged and it is the one act in this revision that
is mine.** `f0690a2c` held the gate: the measurement re-run **six times**, three of
them on a pristine `git archive` export, every published byte figure identical
**101 commits** after it was taken and again at `2ab18690` after a commit that
re-encodes rendered markup landed mid-act; the method paragraph checked against
`examples/dashboard/resync.go` **at HEAD** rather than against the commit body; and
**one published latency figure (`max 579µs`) recorded as not reproducing, in its
own section, at the same prominence as the ones that did.** Phase 3 exits at
seventeen of seventeen. §4.6.2 and §6.

**Two corrections to this report's own text are made in this revision and both
were found by somebody else running something.** §4.5 attributes the empty
inspector panel to an *"Illegal invocation"* thrown by
`(globalThis.rAF || setTimeout)(...)`. **DEV-2 tried to reproduce it as a mutation
control and could not**: `requestAnimationFrame` is on the `[Global]` `Window`
interface and Web IDL defaults an undefined `this` to the global object, so it
does not throw in Chromium 151. The empty panel was real; **the mechanism named
for it is not.** And §4.5's transcript line
`CLIENT_EVENT event:counter.increment ← #2` **cannot have come from
`examples/counter`** — that reducer's own transition changes no session state and
is suppressed, so what the browser sees is the store's broadcast. Both corrections
sit beneath the sentences they correct, in §4.5.1, and **the copies in
`client/inspector.js` and `client/SIZE.md` §8 are routed, not fixed**, because they
are not mine.

### 1.5 What revision 6 changes — the count moves, and the four conditions that move with it

*(Added 2026-08-06, revision 6, at `9efb7e5b`. §1, §1.1, §1.2, §1.3 and §1.4 are
left standing. **§1.4 opened by saying it was the revision most at risk of being
read as a phase exit. This one is a phase exit, and the risk inverts: the danger
is no longer that a reader over-reads the news, it is that the tick absorbs four
named conditions and an open review condition and they stop being findable.**)*

**Thirteen of thirteen boxes are ticked. Phase 4 EXITS.** The thirteenth is box 3,
FR-54, and **it ticks on QA-1's grade — not on mine.** That distinction is §5.2's
rule and §5.11 now applies it to all thirteen rows at once, because a report that
exits a phase should say, once and in one place, which of its ticks are somebody
else's signature and which are its own reading.

**What closed the box, in the order it happened.** DEV-1 discharged L9-1's Part A
conditions — `0b31e67d` makes `binding()` **refuse**, with a full-sentence panic,
a `:` or `;` in a `Bind.Keys` entry, a `domEvent` or an `eventName`, and an empty
`eventName`, which closes the regression revision 5 named as FR54-3 (an unbindable
key silently turning a declared `Debounce` into a `Throttle`); `42b4e0e6` pinned
FR54-4. The **Part B landing** arrived at `0b9e32e7`/`2311280b`: `Bind.NoModifiers`
and `Bind.PreventDefault` as grammar components 7 and 8, **+0 exported
identifiers, +2 fields (51 → 53)**, zero output delta, and `preventDefault` placed
**below** the IME composition guard, which is C-9. `F-CHT-3` — *"Enter sends,
Shift+Enter inserts a newline"* — is **driven in real Chromium 151**, and that is
failure 1's expressible half closing against an artifact rather than a prototype.
A parity and documentation sweep followed. L9-1 accepted, discharged eleven of
their thirteen conditions, and **QA-1 graded** at `eb4971c6`, committed
`9efb7e5b`: **PASS WITH CONDITIONS.**

**Revision 5's own words, which this revision is bound by:** *"a tick that
swallows a named open row is how the row stops being found."* So, at the same
prominence as the tick:

- **QA-1's four conditions, Q-5…Q-8, travel with the tick and are not discharged
  by it.** Q-5 is L9-1's, Q-6 and Q-8 are DEV-1's, **Q-7 is mine and is the only
  one this revision discharges** — it is the PRD's own header Status row and
  FR-54's failure-3 block still describing a world two landings old. §6 carries
  all four with owners.
- **FR54-7 travels behind the box** — `refuseUnbindable` and L9-1's own §22.3
  disagree at HEAD about whether an empty `domEvent` is refused, and **the tree is
  self-consistent while the ruling is the outlier.** L9-1 placed it behind
  deliberately, on the ground that moving a closure condition after the artifact
  exists is C-3's error mirrored, and QA-1 agreed. Non-blocking, and open.
- **G11 did not run in the gate that produced this exit.** It needs a host docker
  daemon. Box 7 is graded separately at §3 row 7 on QA-1's own run, and nothing in
  this revision re-establishes it.
- **One browser, one version.** Chromium 151. QA-1 says so themselves and I am not
  softening it: `F-CHT-3`, the `MouseEvent` clause and the modifier reads are
  unproven on Firefox, Safari and WebKit.

**The finding of this round is not the tick, and it belongs at the top rather than
the bottom.** L9-1 pre-registered nine constraints before the artifact existed —
the method this report has praised twice — and **three of the nine were
defective**: C-1 stated a spec count that was never right, C-3 set a byte budget
**no correct artifact could satisfy** because it priced a prototype that would
break every CJK composer, and C-6 as written **would have certified a runtime with
a dropped `altKey` read**. **All three were caught by the people building against
them rather than by their author, and their author published the finding against
themselves.** QA-1 then re-drove the third independently, in node *and* in
Chromium, and confirmed it. **Pre-registration did not stop three defective
constraints from being written. What it did was make them findable, attributable
and correctable before they graded anything** — and that is a smaller claim than
this report has previously made for the method, made on this round's evidence.

**And one correction this revision owes itself, in the class it exists to
police.** FR54-9 is L9-1's condition that **this document** carries a superseded
byte figure — the `+62 B minified / +34 B gzipped` price for the Part B shape, in
six places, and an old shipped pair in six more. Running the tool at this tree
rather than reading the routing found a **seventh** stale current-state figure
nobody routed: the reproduce block's own `apisurface` line. §7.11.

---

## 2. What I checked, and what I did not run

### 2.1 CI — which tree each green belongs to

Two Phase-4 gates landed in `ci.sh` this round: the godoc step at `ci.sh:660` and
the third client ceiling folded into `ci.sh:669`'s label. Neither has been quoted
green at HEAD by anyone, and this report does not claim it.

| Tree | What ran | Result | Whose |
|---|---|---|---|
| **`452e1e74`** | `bash ci.sh` in `dis-gotth-live:latest`, repository root mounted, working tree clean | **EXIT 0**, quoted in the PR body — the run that covers everything up to and including the inspector landing, but **before** doccheck, its `ci.sh` step, and dev reload existed | the orchestrator |
| **`1e59bb04`** | the **four tools-module steps** for real in `dis-gotth-live:latest` — `go vet`/`go test ./...` over `tools`, `apisurface`, `doccheck`, `minify` | **green**, stated in that commit's own body, together with `bash -n` on `ci.sh` and the D-5 guard's third check recomputed by hand | **DEV-1** |
| **`8a06cb04`** (HEAD) | `bash ci.sh`, whole gate, including both new steps | **EXECUTING as this report is written.** Not quoted here | the orchestrator, to quote |

**What Phase 4 therefore claims about CI, exactly.** The godoc gate is green at
`1e59bb04` on its own four steps, on DEV-1's run. The only non-documentation
change between that tree and HEAD is `live/example_test.go`'s two new examples
(`git diff --stat 1e59bb04 HEAD -- . ':!docs'` → one file, +60), and
`8a06cb04`'s own body records both verified under `go test -v`. So the doccheck
result at `1e59bb04` covers every non-documentation file at HEAD except the two
functions whose own landing checked them. **That is not a claim that the whole
gate is green at HEAD**, and the FR-66 box's tick says so in its own text.

### 2.2 What I checked myself, in the tree

None of this needs a toolchain; four of the seven rows changed something in this
report or in the PRD.

| # | What I checked | How | Result |
|---|---|---|---|
| 1 | **Is FR-53's 46 still 46 after DEV-3's remediation?** | Counted `docs/quickstart.md` §2's and §3's blocks at HEAD by the quickstart's own method | **Yes: 27 + 19 = 46.** Seven fixes, zero lines. The counted app still registers `templ.Handler(Page(State{}))`; the per-request alternative F-4's fix added is prose beside it, not in the counted block. §4.2 |
| 2 | **Did FR-57 cost a dependency?** | `git diff --stat 452e1e74 HEAD -- go.mod go.sum` | **Empty.** DEV-2's "no new dependency" is true of the root module across the whole landing |
| 3 | **Did the shipped runtime move?** | `client/SIZE.md` §1 and §2.2 against `git show --stat 7cff113a` | **No.** 10,391 minified / 4,429 gzipped before and after; `live/clientjs/gotth-live.min.js` is not in dev reload's diff. The third artifact is 1,260 B of 8,192 |
| 4 | **Does FR-77's docs box actually meet its four clauses?** | Read `guide/effects-and-server-push.md` and `guide/when-not-to-use-this.md` against the box's own wording | **All four.** FR-77(a) quoted rather than paraphrased at `effects-and-server-push.md:308`; both double-execution paths tabulated at `:334`; the worked key on a charge at `:362`; FR-77(c) quoted at `when-not-to-use-this.md:27`. §4.9 |
| 5 | **Does `docs/exceptions.md` exist?** | `ls` | **No.** Not stale, not thin — absent, and FR-20 has required it since Phase 1. §4.13 |
| 6 | **Does FR-59's docs set cover its nine subjects?** | `docs/README.md`'s index against FR-59's list | **Seven of nine.** No deployment page; security configuration has no page of its own. §4.8 |
| 7 | **Is the unenforced godoc count the same number everywhere?** | `grep` for it in `tools/doccheck` and in `1370229c`'s body | **No — three values.** §7.1 |

### 2.3 What I did not run, and why

`ci.sh`, `doccheck`, `minify -check`, the node suites, the browser suites, and
any rebuild of the three client artifacts. There is no Go toolchain on this host
outside a container, a whole-gate run was executing against this tree throughout,
and the brief for this turn was documentation plus cheap reads. **Every number
in §3 and §4 therefore carries the name of the agent who produced it and the
commit it belongs to**, and where a figure has no such anchor I say so instead of
quoting it — see §7.1, which is exactly that case.

### 2.4 Revision 2 — what I checked, and what I did not run

*(Added 2026-08-05 at `134e69c5`. Same constraint as §2.3, same shape as §2.2: a
full `ci.sh` run was executing against this worktree throughout, there is still
no Go toolchain on this host outside a container, and I ran nothing but `git`,
`grep`, `sed`, `awk` and `wc`.)*

**Checked myself, in the tree.** Fourteen rows; six of them changed something in
this report.

| # | What I checked | How | Result |
|---|---|---|---|
| 1 | **Does the error census sum to the audit's 117?** | Added `internal/arch/errors_test.go`'s `errorCensus` map by hand | **Yes.** 3 + 4 + 40 + 8 + 8 + 10 + 37 + 7 = **117** across eight packages, which is the headline. §4.12 |
| 2 | **Do the audit's tables carry 25 graded failures, as `134e69c5` corrected the headline to say?** | `grep -c '\*\*was ' docs/error-audit.md` | **25.** The self-correction is right and the headline now matches the tables |
| 3 | **Did `docs/api-surface.md` need a row?** | `git diff --stat 8a06cb04..HEAD -- docs/api-surface.md` | **Empty.** DEV-1's "no exported symbol changed" holds as far as an empty diff can carry it — and **no `apisurface` run is quoted anywhere in the landing**, so this report does not claim one. §7.5 |
| 4 | **Is E-2's deviation live at HEAD?** | Read `docs/guide/_samples/errorhandling/errors.go` | **Yes.** `slog.Warn` with three fields off the event, inside `Reduce`, at **line 71** — the line `docs/exceptions.md` names. Unfixed and routed to DEV-3 |
| 5 | **Was E-2's stated root cause fixed?** | Read `live/core.go`'s `EffectFailedErrorField` doc comment | **Yes.** "Log it, count it, branch on it" is gone; the comment now says to branch in the reducer and log from `Config.Execute` or the `slog.Handler`, and names the deviation it caused |
| 6 | **Is every sign-off line in `docs/exceptions.md` unsigned?** | `grep -n 'sign-off' docs/exceptions.md` | **Yes**, both of them, plus the file's own second line and its §6 statement saying so |
| 7 | **Does `grep -n "G11" ci.sh` return what DEV-2 says?** | Ran it at HEAD and against `git show 5c751ae9:candace/pkg/gotth/ci.sh` | **17 lines at both**, with the step at `ci.sh:876`. **DEV-2's artifact says fifteen, twice.** §7.5 |
| 8 | **Are `docs/PRD.md:203` and `:1911` the two G11 sentences DEV-2's F-1 names?** | `grep -n 'go run ./examples' docs/PRD.md` | **Yes, exactly those two lines** and no others. Both amended in PRD v0.9 row 1 |
| 9 | **Does `docs/README.md` carry the rows and the gate index?** | Read it at HEAD against `git show f34ef2ca` | **Yes.** Rows for `guide/security.md` and `guide/deploying.md`, and a new **"The record"** section indexing four gate reports. §6's row is discharged |
| 10 | **Is the architecture RFC filed where DEV-3 says it is?** | Read `docs/README.md`'s "For the curious" preamble | **Yes**, verbatim: *"None of it is needed to build an application, and all of it argues rather than instructs."* That sentence is the whole of §5.4's ruling |
| 11 | **Did the counted quickstart blocks or `examples/` move?** | `git diff --stat 8a06cb04..HEAD -- docs/quickstart.md examples/`, then re-ran §2.2 row 1's two `awk` counts | **Neither moved**, and the count is **27 + 19 = 46** again. FR-53 is untouched by this landing |
| 12 | **Is the `Example*` census still 6/6?** | `grep '^func Example'` and `grep -c '// Output:'` over the two files | **6 functions, 6 `// Output:` comments.** FR-68's tick is not disturbed by the FR-58 remediation |
| 13 | **Do DEV-3's six code-versus-docs items say what they are reported to say?** | Read each named file and line | **Four of six confirmed as stated; two are not contradictions and are reclassified.** §7.4 — this is the row that changed the most |
| 14 | **How many call sites handle the two mailbox sentinels with a comment and no branch?** | `grep -rn 'mailbox was full, or the session is closing'` | **Five**, not four: the guide sample, all three examples, and `bench/apps/counter/gotth/store.go`. §5.5 |

**What I did not run, and what that costs.** Everything in §2.3, unchanged, plus
the three specific things this revision would most like to have run and did not:

- **DEV-2's `tools/g11/run.sh`.** It needs a docker daemon and it starts a
  container of its own; running it would have collided with the gate run in
  flight. **Every G11 figure in §3 row 7 and §4.7 is DEV-2's, at `5c751ae9`.**
- **`tools/apisurface`.** DEV-1's "no exported symbol changed" is checked here
  only as an empty diff on the ledger file, which is a weaker statement. §7.5.
- **The `docs/guide/_samples` drift suite**, which is what holds DEV-3's two new
  pages to their compiled samples. DEV-3 reports it green in `f34ef2ca`'s body;
  that is their run at their tree and this report does not extend it to HEAD.

### 2.5 Revision 3 — what I checked, and what I did not run

*(Added 2026-08-05 at `b04ba138`. Same constraint as §2.3 and §2.4: no Go
toolchain on this host outside a container, the orchestrator running codegen and
G11 gates in containers throughout, so I ran nothing but `git`, `grep`, `sed`,
`awk` and `wc`. **The instruction for this turn was "verify, do not read", and
§7.8 is what that produced** — two rows below overturn something this report or
my brief asserted.)*

**Checked myself, in the tree.** Twelve rows; five changed something.

| # | What I checked | How | Result |
|---|---|---|---|
| 1 | **Is FR-53 now 39, and do the reproduce block's ranges print it?** | The two `awk` counts at the ranges DEV-1 recomputed | **Yes: 20 + 19 = 39.** And the **old** ranges print **28 + 19 = 47**, which is neither the old 46 nor the new 39 — so the block as published reproduced nothing at all. §7.8, and the block is corrected at the end of this report |
| 2 | **Was §2.4's check 5 right?** | `git show 134e69c5:gotth-live/live/core.go \| grep -n 'Log it'` | **NO — my verdict was wrong when written.** The sentence was at **`:246`–`:247`** at the tree §2.4 graded. §7.8 |
| 3 | **How many copies of that sentence were there, and where?** | `grep -rn` at HEAD and across the fix commits | **Three, found by three different people.** `live/core.go` (DEV-1, `0bd5bb40`), `guide/effects-and-server-push.md` (the orchestrator, `368132f6`), and the original sample (DEV-3, `091dbae8`). At HEAD the phrase survives only as a **historical quotation** in `docs/exceptions.md:331` and `guide/error-handling.md:312`, both naming it as the wording that *caused* E-2 |
| 4 | **Is F-10, box 8's condition, actually closed?** | `sed -n '24p' docs/README.md` against `docs/quickstart.md:7` | **Yes.** The index row reads *"20 lines of Go, 19 of templ"* and the page reads *"20 lines of Go and 19 lines of templ markup"*. `b04ba138` |
| 5 | **Is the register signed, on every row?** | Read `docs/exceptions.md`'s header table and §7 | **Yes.** Three rows, each **`L9-1, 2026-08-05`**, and §7.7's own claim that *"no row in this document is now without a disposition"* holds against the file |
| 6 | **What does `live.New` actually require?** | Read `live/app.go:158`'s `validate` | **Seven fields** — `Reduce`, `Fragments`, `Events`, `Origins`, `Authenticate`, `Authorize`, `CSRF` — **four of them security hooks a caller must name to opt out of.** This is the spine of §5.8 and it corrects the "eight `Config` fields" this report has said since revision 1 |
| 7 | **Does the `live.Document` costing reproduce?** | Counted the quickstart's templ block by hand: shell `:336`–`:346`, fragment `:325`–`:330` | **Yes: 19 − 8 = 11 templ, 20 + 11 = 31.** I could not find DEV-1's costing written down anywhere in the tree, so I did the arithmetic rather than quoting a number I could not open. §5.8 |
| 8 | **What do the three examples, the guide and the bench apps actually bind?** | `grep -rn 'live\.On\|live\.OnAll\|live\.OnWith\|live\.Bind{'` over `examples/`, `docs/guide/`, `docs/quickstart.md`, `bench/apps/*/gotth/` | **`click`, `submit`, `input`+`Debounce`, `keydown`+`Keys`, and `OnAll` — and not one hand-assembled `data-gotth-*` string in any of them.** The input to §5.6 |
| 9 | **Is `Debounce` really element-scoped, with the consequence I claim?** | Read `live/templ.go:148`–`:207` and `client/runtime.js:622`–`:665` | **Yes.** `OnWith` emits the attribute only when `Debounce > 0`; `OnAll` keeps the first **present** value; the runtime reads it off the element and keys the timer by the element with `clearTimeout` on each dispatch. **So the guide's own composer debounces its `Escape` binding by 150 ms and a following keystroke cancels the clear.** §5.6 failure 2 — **derived from source, not driven in a browser**, and routed on that basis |
| 10 | **Is `examples/chat`'s F-3 stale, or is the affordance genuinely inexpressible?** | Read `FRICTION.md:119`–`:157` and `view.templ:64`, then `git log -S'Keys []string' -- live/templ.go` | **Stale.** F-3's own *"Proposed shape"* block is `live.OnWith("keydown", "chat.clear", live.Bind{Keys: []string{"Escape"}})`, and that API **landed at `591c275a` citing chat's F-3 by name**. F-1 and F-4 in the same file got *"— Closed."* headings; F-3 did not. **The affordance is still absent, so the conclusion is true and the reason is false.** §5.6 failure 3 |
| 11 | **Does `docs/OPERATOR-QUESTIONS.md` lack `Q-BENCH-1/2`, as my brief said?** | Read it | **No — it has both**, added 2026-08-04 in an explicitly fenced series, and **Q-E was ratified at PRD §9 v0.6 row 3** with FR-70 amended to match. **My brief's item did not reproduce.** What is stale is `bench/README.md:421`, which still says that file has no bench series and that Q-E awaits PM-1. Routed to BENCH-1; outside my write scope |
| 12 | **Did the counted quickstart blocks move after DEV-1's shrink?** | `git diff --stat fde707f0 HEAD -- docs/quickstart.md` | **Only `b04ba138`'s neighbouring index fix touches the page's region**; the two counted blocks are unchanged, and I re-ran both counts at HEAD rather than assuming it |

**What I did not run, and what that costs.** Everything in §2.3 and §2.4,
unchanged, plus the two this revision would most like to have run:

- **A browser.** §5.6's failure 2 is a **derivation from three source files**,
  not an observation. It should be driven before it is fixed, and the owner line
  says so. This is the same gap §4.4 and §4.5 carry, arriving in a third place.
- **Any of QA-1's or L9-1's work.** Four grading passes and one signature are
  taken here **as grades**, which is the whole point of §5.2's rule — I checked
  the two cheap facts a grade rests on that I could check without a toolchain
  (F-10's closure, the register's signature lines) and nothing else. **If QA-1's
  or L9-1's judgement is wrong, this report is wrong with it, and that is the
  correct exposure for a report whose author does not hold those gates.**

### 2.6 Revision 4 — what I checked, and what I did not run

*(Added 2026-08-05 at `5d665226`. Same constraint as §2.3, §2.4 and §2.5: no Go
and no node on this host outside a container, so `git`, `grep`, `sed` and
`python3` only. **The instruction for this turn was the same as revision 3's —
verify, do not read — and §7.10 is again what that produced.**)*

**Checked myself, in the tree.** Nine rows; four of them changed or confirmed a
correction.

| # | What I checked | How | Result |
|---|---|---|---|
| 1 | **Is the count actually 31, on both artifacts?** | My own classifier, not QA-1's and not v0.6's frozen `awk`: every physical line of each artifact bucketed under exactly the four exclusions the rule names (blank, comment, `package`, `import` declaration), printed so it can be checked by eye | **Yes, and on four artifacts rather than two.** `docs/quickstart.md:75`–`:117` → **20** (43 physical: 7 blank, 10 comment, 1 `package`, 5 import); `:331`–`:362` → **11** (32 physical: 4, 12, 1, 4); `docs/guide/_samples/quickstart/main.go` → **20**; `view.templ` → **11**. **31 on the page's fences and 31 on the pinned samples.** I did not use QA-1's figure to reach mine |
| 2 | **Trigger 2 — has `validate`'s required-field set moved?** | Read `live/app.go`'s `validate` at HEAD rather than quoting v1.1 | **No. Seven fields** — `Reduce`, `Fragments`, `Events`, `Origins`, `Authenticate`, `Authorize`, `CSRF` — with `Init` still deliberately absent from the switch and still optional, and the comment naming the four security hooks intact. The page shell added no `Config` field and removed none |
| 3 | **L9-1-C2 — was the repaired trigger 1 in force *before* the shell?** | `git merge-base --is-ancestor 667d3db7 8680e8c5` | **Yes** (`667d3db7` at 16:19, `8680e8c5` at 16:47). QA-1 ran the same check for their grade and I ran it again rather than citing theirs, because it is the single fact that decides whether today's PASS means anything |
| 4 | **Does the godoc call the shared debounce "a wart", as my §5.6 says?** | `grep -rn wart live/`, then over the whole tree | **NO — and it never has.** `live/` returns **nothing**. Three hits tree-wide and the only source of the word is **`docs/api-surface.md`**, in `OnAll`'s consequence row. **The phrase is in `591c275a`'s commit message** — *"The shared debounce timer is a wart and the godoc says so"* — which is where mine appears to have come from. §5.6's row is corrected beneath itself; §7.10 correction 4 |
| 5 | **Is FR-54's failure 3 closed by DEV-3's correction?** | Read `examples/chat/FRICTION.md` F-3, then `grep -n` for the sentence across the example | **No — relocated.** F-3 is corrected properly, beneath itself, with both false sentences quoted. But **`examples/chat/view.templ:64`–`:68`** still reads *"live.On has no key filter … Escape-to-clear has no expression at all and is therefore absent"*, and **`view_templ.go:188`–`:192`** carries the generated copy. DEV-3 reports both at `FRICTION.md:184`–`:187` and left them as another file's. §4.3.2 |
| 6 | **Does population (b) catch chat's Escape-to-clear, so that the clause-(c) question is moot?** | Read the equivalence spec's frozen §2 chat table | **NO, and this is the one check that cut against the comfortable answer.** `docs/bench/equivalence-spec.md:212`–`:220` lists `F-CHT-1`…`F-CHT-9` and **none of them is Escape-to-clear** — `F-CHT-3` is *"Enter sends, Shift+Enter newlines"*, which is failure **1**. So the ruling in PRD FR-54 rests entirely on population (a) and on the source comment in row 5, and it says so |
| 7 | **Do DEV-3's routed citation drifts reproduce?** | `sed -n` at each line they name | **One of two.** `bench/README.md:553` → **`:670`** ✔ (the `F-CHT-3` bullet is there). `docs/api-surface.md:615` → DEV-3 says `:651`; **`:651` is a table header**, and the modifier-state row is at **`:696`**. Routed on, corrected here |
| 8 | **Does QA-1's own citation for the "wart" row reproduce?** | `sed -n '654p' docs/api-surface.md`, then `git show 667d3db7:…` | **No, and it costs nothing.** They cite `:654`; the row is at **`:699`** at HEAD and was at **`:618`** at the tree they drove. **Same row, three line numbers, one appended-to file.** This is the same drift class as §7.8 correction 2 and it is now the fifth instance in this report |
| 9 | **Is this report the last document in the tree stating a stale FR-53 count in live text?** | QA-1 §10.4's claim, checked against `docs/README.md:24` and `docs/quickstart.md:7` | **Yes, and it was.** Both of those read 20/11/31 and track `8680e8c5`; **F-10 stays closed.** This report's §3 row 2, §6, §8.1 and §8.2 were the carriers. §7.10 correction 3 |

**What I did not run, and what that costs.** Everything in §2.3, §2.4 and §2.5,
unchanged, plus:

- **Any of QA-1's or L9-1's work, again.** Box 2's grade, the page-shell gate and
  the FR-54 reproduction are taken **as grades and as a review**. I checked the
  cheap facts each rests on that need no toolchain — the count, the ancestry, the
  `validate` set, the two stale carriers — and nothing else. **If QA-1's or
  L9-1's judgement is wrong, this report is wrong with it**, which is the same
  exposure §2.5 stated and the correct one.
- **A second timed run, a browser, and `ci.sh`.** FR-53 names **one** timed
  measurement; a browser is exactly what §5.6's failure 2 needed and QA-1 has now
  supplied it; and a `ci.sh` run tells this revision nothing it does not already
  have from L9-1's two full runs at `679e6695` and `8be955e5`, both `CI-EXIT=0`.
- **The count gate I authorise in §5.10.** It did not exist when I authorised
  the number. **Authorising a number is not implementing it and is not verifying
  it**, and the routing is three-party for that reason. See §2.7.

### 2.7 The tree moved under this revision, and what that changes

*(Added 2026-08-05, at the end of revision 4. **A gate report written over a
moving tree is stale on arrival — that is this report's own sentence, used twice
to defer this revision — so when the tree moves under it the honest act is to say
where and re-derive rather than to re-date the header.**)*

**This revision grades `5d665226` and was written across `ec50a412` and
`f555f3b5`.** PRD v1.3 (`ec50a412`) is mine and is this revision's requirements
baseline. **`f555f3b5` is a DEV stream's, it landed while §7 and §8.3 were being
written, and it discharges QA-1's Q-1, Q-2, Q-3's page half and Q-4.** It touches
`docs/quickstart.md`, `docs/guide/_samples/samples_test.go` and
`live/page_test.go`.

**Three things I re-derived at `f555f3b5` rather than assuming, because a
zero-margin count is exactly the thing a docs edit can move by accident.**

1. **The count is still 31.** Re-run at the new HEAD, on all four artifacts, by
   the classifier of §2.6 row 1: **20 + 11 off the page's fences and 20 + 11 off
   the pinned samples.** `git diff 5d665226 f555f3b5 -- docs/guide/_samples/quickstart/`
   is **empty**, so the two counted sample files did not move at all — which is
   what QA-1 said would be true of any Q-1 or Q-2 remedy, because both land in a
   `bash` block or in prose and the v0.6 rule excludes shell commands. **Box 2's
   grade is undisturbed by this commit and I checked that rather than assuming
   it.**
2. **QA-1's own fence citations are now stale by three lines, and that is the
   fifth instance of §7.10 correction 5 arriving before the ink dried.** §10.3
   cites `docs/quickstart.md:75`–`:117` and `:331`–`:362`; at `f555f3b5` the
   blocks are at **`:78`–`:120`** and **`:334`–`:365`**, because Q-1's remedy
   landed above them. **The ranges were exact at the tree QA-1 graded** and
   nothing about the grade is affected. **This report's own reproduce block is
   therefore written against the `<!-- sample: -->` markers rather than against
   line numbers**, which is the remedy §7.8 correction 2 prescribed and which
   this report had not previously applied to itself.
3. **The count gate landed and it matches the number and all four constraints I
   authorised at §5.10** — `const fr53Budget = 31` with a comment saying the
   budget is not that file's to move and citing PRD §9; `BeNumerically("<=",
   fr53Budget)`, not an equality; the parenthesised-import reading stated
   explicitly and justified by the same six-commit reproduction QA-1 used; the
   two marked blocks of `docs/quickstart.md` as the measured artifact; and a
   per-file split in the failure message. **I am recording that it matches my
   authorisation. I am not crediting it**: §5.10's fourth constraint is that it
   must be **shown red at 32**, that demonstration is **QA-1's**, and until they
   give it this is a check nobody has watched fail — which is the sixth time
   this report has had to say that sentence.

### 2.8 Revision 5 — what I ran, what I read, and what carries another agent's name

*(Added 2026-08-05 at `e751f6de`. **Different from §2.3–§2.7 in one respect and it
is the one that matters: this revision ran two of the gate's own tools rather than
quoting them.** There is still no Go and no node on this host outside a container,
so everything below that needs a toolchain ran through `~/bin/dis run bash -c …`
from `candace/pkg/gotth/`, and everything else is `git`, `grep`, `sed` and `ls`.)*

**CI at this revision, in §2.1's form, because a green quoted without its tree and
its skips is the defect §2.1 exists to prevent.**

| Tree | What ran | Result | Whose |
|---|---|---|---|
| **`d12870a0`** | `bash ci.sh` in **`dis-gotth-live-bench:latest`** with **`GOTTHLIVE_E2E=1`** and `CHROME_BIN` set — the image that has a browser, so the browser-labelled specs are inside the gate rather than beside it | **EXIT 0**, verdict *"every gate this invocation could run is green"*; browser conformance **`ok 50.369s`**. **ONE step skipped: G11**, which by design needs a host docker daemon and cannot run inside any image — §6's "No CI job runs G11" row and §4.7.1's split | the **orchestrator** |
| **`e751f6de`** | `apisurface` and `doccheck` re-run after L9-1's commit | **both exit 0** — `live 56/56, 51/51`, *"the surface matches the ledger"*; every exported symbol carries a doc comment | the **orchestrator**; `apisurface` **re-run independently by PM-1**, row 1 below |

**The difference between this and every previous revision's CI row is that the
browser step is inside the number.** At revision 4 the whole-gate run skipped the
browser specs by construction and they were run beside it; at `d12870a0` they are
50 specs of the gate. **That is `13a1ca1e`'s doing and it is the substantive half
of the condition §6 has carried since revision 1.**

**Ran, in `dis-gotth-live:latest`, at `e751f6de`.** Two tools, chosen because they
are the two whose output this revision quotes as a number rather than as a verdict.

| # | What I ran | What it printed | What it decides here |
|---|---|---|---|
| 1 | `cd tools && go run ./apisurface` | `live 56/56 identifiers, 51/51 fields, 107/107`; `live/livetest 37/37, 33/33, 70/70`; **"the surface matches the ledger"**, exit 0 | **The `+0` in "FR-54 failure 2 cost +0 exported identifiers" is mine now, not DEV-1's and not L9-1's.** It also fixes the baseline the Part B ruling's `51 → 53` is measured from, so a landing that arrives at 54 fields is detectable against a number PM-1 took |
| 2 | `cd tools && go run ./minify -check` | **Shipped `gotth-live.min.js` 10306 / 4421**, ceiling 12288, **headroom 7867 (64.0 %)**; inspector 14905 / **6211** of 40960; dev-reload 2452 / **1260** of 8192; exit 0 | **The −85 B / −8 B is confirmed from a run of my own.** 10,391 → 10,306 and 4,429 → 4,421. It is also the number L9-1's §11 measures the accepted shape against (10,368 / 4,455), so C-3's pre-registered budget is anchored to a figure this report has seen |

**Checked myself in the tree, without a toolchain.** Eight rows; every one of them
is a claim another agent made in a commit body this revision repeats.

| # | What I checked | How | Result |
|---|---|---|---|
| 1 | **Are the three element-level option attributes really gone?** | `grep -rn 'data-gotth-fields\|data-gotth-debounce\|data-gotth-throttle'` over `live`, `client`, `examples`, `test` | **Gone from every emitter and every reader.** The only five hits in the tree are **assertions that they are absent** — `live/binding_test.go:39`–`:41`, a historical comment at `:95`, and `examples/chat/chat_test.go:1390`. The claim and its regression guard landed together |
| 2 | **Does `examples/chat` actually implement Escape-to-clear, or does it describe it?** | Read `chat.go`, `view.templ` and the suite | **Implements it.** `EventClear = "chat.clear"` is a real reducer case (`chat.go:353`), it is in `Config.Events` (`:729`), it is bound with `live.OnAll(live.OnWith("keydown", EventClear, live.Bind{Keys: []string{"Escape"}}), …)` (`view.templ:107`–`:108`), the generated copy follows at `view_templ.go:343`, and the suite pins the rendered spec string **and** pins that the old inherited-debounce spelling is *not* emitted (`chat_test.go:1334`, `:1392`) |
| 3 | **Does the `view.templ` comment still say the library cannot do it?** | Read `view.templ:50`–`:85` — **as a paragraph, not as a grep**, which is §2.6 row 5's own lesson about wrapped Go comments | **No, and it is the inverse now.** *"F-3 is now closed. Escape-to-clear is implemented, below … the two do not interfere."* It also names **why** the second clause is newer than the first: until failure 2 landed, this binding would have inherited the input's 150 ms. **The condition §6 carried is discharged in the place it was carried against** |
| 4 | **Does `FRICTION.md` F-3 carry the `— Closed.` heading, and is the refusal argument kept?** | Read the file | **Both.** `## F-3 — *Closed.* Escape-to-clear is implemented, and this item's reason for its absence was false before that`, and at `:287` the section that refused the heading is kept with what changed written beneath it. **That is this project's house style applied by somebody who is not me, to a refusal that was right when it was made** |
| 5 | **Are the two conformance files real, and does `dev_reload_test.go` carry the negative control?** | `ls test/internal/conformance/`, then `grep -n` for FR-57 and the control | **Yes.** `dev_reload_test.go` (24,814 B) and `inspector_test.go` (20,180 B) exist, both dated at this landing. The negative control is at `:591`–`:659` with the two vacuity guards the commit body claims — the watcher's own "restarted" line, and `document.visibilityState` |
| 6 | **Did `ci.sh`'s counts get re-derived, and is the stale pair named?** | Read `ci.sh:811`–`:874` | **Yes, and in both places a count appears.** `:835` — *"the label selects 43 specs that pass without it and 50 with"*; `:863` and `:874` carry the same 43 and the 43 + 7 split in the skip notice. The commit body names the two stale predecessors (19/22 at checkpoint 3, 22/25 after it) rather than silently replacing them |
| 7 | **The five stale current-state size figures L9-1 routed — do they reproduce?** | `sed -n` at each of the five lines | **All five, exactly as routed.** `README.md:113` (10,391 / 4,429, "64 % headroom"), `docs/guide/deploying.md:24` (**"re-measured on every landing"**, which is self-refuting), `docs/quickstart.md:161` (the probe table a reader compares their own terminal against), `docs/guide/inspector.md:198` (4,429), `docs/instrumentation.md:835` (*"4,429 B, unchanged by this landing"*). **None of them is mine to fix** |
| 8 | **The three routed findings with file and line — do they land?** | `sed -n` at each | **All three.** `bench/README.md:670` opens *"for three independent reasons, any one of which is enough"* and reason 3 is the one `2ab18690` killed; `docs/guide/deploying.md:54`–`:56` still reasons only about the wire protocol; `docs/api-surface.md:719` still routes the `F-CHT-3` consequence as *"a finding for PM-1"*, which §12 of the review discharges. **`docs/api-surface.md:586`'s correction IS appended** — L9-1 wrote it themselves, beneath the false row rather than over it, at `:587` |

**What I did not run, and what that costs.**

- **`ci.sh`.** I did not run it and I do not need to: the whole-gate run this
  revision quotes is the **orchestrator's at `d12870a0`**, and §2.1's form is where
  it is quoted with its tree and its one skip. Running a second one would tell this
  revision nothing and would collide with a shared host.
- **The browser.** Every browser figure in this revision — `43`/`50`, the three
  mutation controls, the two that disproved `0c711b70`'s stated cause, L9-1's 64
  ordered pairs and their negative control, the four-spec `F-CHT-3` prototype — is
  **somebody else's, with their name on it.** I ran no Chromium. The two that would
  most change this report if they were wrong are DEV-2's `43`/`50` and L9-1's
  survivor mutant, and neither is checkable by reading.
- **QA-1's and L9-1's judgement, again.** Box 3's shape at this revision rests
  entirely on `docs/reviews/fr-54.md`. I read it in full and checked the cheap facts
  under it — the two tool runs above, the eight tree rows above, and the three
  routed line citations. **If L9-1's ruling is wrong, this report is wrong with
  it**, which is the same exposure §2.5, §2.6 and §2.7 stated and is still the
  correct one.
- **Anything that would discharge somebody else's condition.** Q-1…Q-4 are QA-1's,
  FR54-1…FR54-6 are L9-1's, and the five size figures are DEV-3's. **I re-derived
  what I needed to grade and stopped there**, which is the sentence this section has
  now carried in five consecutive revisions and the one revision where honouring it
  costs the appearance of a finished phase.

### 2.9 Revision 6 — what I ran, what I read, and the figure that only a run could have caught

*(Added 2026-08-06 at `9efb7e5b`. Same constraint as §2.8: no Go and no node on
this host outside a container, so everything needing a toolchain ran through
`~/bin/dis run bash -c …` from `candace/pkg/gotth/`. **`bash -c`, never `bash -lc`** — a
login shell strips the Go toolchain from `PATH` in these images.)*

**CI at this revision, in §2.1's form.**

| Tree | What ran | Result | Whose |
|---|---|---|---|
| **`eb4971c6`** | `bash ci.sh` in **`dis-gotth-live-bench:latest`** with **`GOTTHLIVE_E2E=1`** and `CHROME_BIN=/usr/bin/chromium` | **EXIT 0**, verdict *"every gate this invocation could run is green"*; race-detector `test/internal/conformance` **`ok 212.302s`**; browser step **`ok 51.467s`**. **ONE step skipped: G11**, which by design needs a host docker daemon and cannot run inside any image | the **orchestrator** |
| **`9efb7e5b`** | nothing — it is **documentation only** on top of `eb4971c6` (QA-1's grading file) | — | — |

**So the green belongs to `eb4971c6` and the grade belongs to `eb4971c6`, and
this report's tree is one documentation commit past both.** That sentence is the
one §2.1 exists for and it is cheap to get right, so it is stated rather than
implied.

**Ran, in `dis-gotth-live:latest`, at `9efb7e5b`. Two tools, and both of them
moved a number this report had published.**

| # | What I ran | What it printed | What it decides here |
|---|---|---|---|
| 1 | `cd tools && go run ./apisurface` | `live **56/56** identifiers, **53/53** fields, **109/109**`; `live/livetest 37/37, 33/33, 70/70`; total 93 / 86 / **179**; *"the surface matches the ledger"*, exit 0 | **The `+0 exported identifiers, +2 fields (51 → 53)` in the Part B ruling is mine now.** The fields row reads **53**, which is the landing's own claim measured rather than quoted, and **56 identifiers is unchanged from revision 5** — so the whole of failure 1's accepted surface cost zero exported names. **It also falsifies this report's own reproduce block**, which still published `51/51  107/107` as what this command prints. §7.11 correction 2 |
| 2 | `cd tools && go run ./minify -check` | **Shipped `gotth-live.min.js` 10387 / 4459**, ceiling 12288, **headroom 7829 (63.7 %)**; inspector 14905 / **6211** of 40960; dev-reload 2452 / **1260** of 8192; exit 0 | **This is the authoritative current-state pair and every current-state claim in this document now matches it.** It is also the number that prices FR54-9: see row 3 |
| 3 | The Part B delta, **measured rather than taken from any review**. Go's `compress/gzip` at `BestCompression` — which is what `tools/minify` uses (`tools/minify/main.go`'s `gz`) — over the committed artifact at `0b9e32e7~1` and at HEAD, in the container | `0b9e32e7~1` → **10,306 / 4,421**; HEAD → **10,387 / 4,459**. **Delta +81 B minified / +38 B gzipped** | **The landed price of `Bind.NoModifiers` + `Bind.PreventDefault` is +81 / +38, not the +62 / +34 this report published six times.** `0b9e32e7`'s parent **is** `42b4e0e6` (`git log -1 0b9e32e7~1`), and `0b9e32e7` is the only commit between Part A and HEAD that touches the artifact, so the whole delta is attributable to the Part B shape and to nothing else. **It agrees with L9-1's §18.4 and with `client/SIZE.md` §1.1.6, and I did not read either until after I had measured** — which is the only reason this row is worth anything, since agreeing with a document I had just read would prove nothing |

**A method note that cost me a wrong number and is worth more than the number.**
My first measurement of row 3 used the host's `gzip -9`, which reads **4,415** at
HEAD where the tool reads **4,459** — a 44-byte disagreement, because GNU `gzip`
and Go's `compress/gzip` are different implementations of the same level.
**`gzip -9` at a shell is not this project's gzip figure and never was.** Had I
published from it, this revision would have introduced an eighth stale figure into
the document whose whole business this round is removing seven. The tool is the
authority; the shell is not; and *"a gate is what you ran"* only holds if you ran
**the** thing.

**Checked myself in the tree, without a toolchain.** Five rows, each a claim in
the brief I was given or in another agent's commit body.

| # | What I checked | How | Result |
|---|---|---|---|
| 1 | **Does `binding()` actually refuse, and refuse the four things the discharge claims?** | Read `live/templ.go`'s `refuseUnbindable` as a function, not as a grep | **Four refusals, in this order:** `:`/`;` in `domEvent`; **empty** `eventName`; `:`/`;` in `eventName`; `:`/`;` in any `Bind.Keys` entry. Each panics with a full sentence naming the offending value *and* the consequence — the `Bind.Keys` one says in terms that the mis-render *"would widen this filter to EVERY key and move every option declared beside it one slot later, so a Bind.Debounce arrives at the client as a throttle"*, which is FR54-3's regression stated in the refusal that prevents it. **Matches the brief exactly** |
| 2 | **Is Q-8 real — do the code and L9-1's §22.3 disagree?** | Read the function against `reviews/fr-54.md` §22.3 and §23 | **Yes, and QA-1's characterisation is the right one.** §22.3 rules an **empty `domEvent`** refused; the code refuses an empty *`eventName`* and three separator cases, and its godoc says *"Those four and nothing else"*. **The tree is self-consistent; the ruling is the outlier.** FR54-7 is the open condition and it travels behind the box |
| 3 | **Are DEV-3's five routed size figures still stale?** | `grep` each of the five files for every figure in the `10,391 / 4,429 / 10,306 / 4,421 / 10,387 / 4,459` family | **All five are FIXED and now read 10,387 / 4,459** — `README.md:113`, `docs/guide/deploying.md:24`, `docs/quickstart.md:161`, `docs/guide/inspector.md:198`, `docs/instrumentation.md:835` — and **`client/SIZE.md`'s ledger reads 10,387 / 4,459 at `:45`–`:46`**, matching tool row 2 exactly. Two of them (`inspector.md:202`–`:204`, `instrumentation.md:837`–`:842`) record the **whole path** 4,429 → 4,421 → 4,459 with the commit that caused each move, which is better than the correction asked for. **§6's row claiming these five are stale is now false and closes** |
| 4 | **Is the `9efb7e5b` tree really documentation-only on top of the graded tree?** | `git diff --stat eb4971c6 9efb7e5b` | **One file, `docs/qa/phase-4-grading.md`.** So the `ci.sh` green at `eb4971c6` covers every non-documentation file at this report's tree, with no carve-out — the first time in six revisions that sentence has needed no qualification |
| 5 | **Does QA-1's §11 say what the brief says it says?** | Read §11 in full, all four clauses, the nine-constraint table, the controls table and §11.8 | **Yes, and §11.8 is the part a summary would drop.** *(One number in it does not reconcile and is routed rather than repeated: §11.1 says "fourteen controls, §11.6"; the table is §11.7 and has thirteen rows covering fifteen mutations. §4.3.4 has it.)* QA-1 lists seven things the pass does **not** prove, including that the `PreventDefault`-outside-a-region behaviour *"is a true sentence with no spec"*, and that clause (c) is a **bounded** sweep — *"a sentence that states a binding absent in words none of the fifteen matched would have survived me, and this project has now found four such sentences after four declarations that the sweep was complete."* **That sentence is carried into §6 rather than paraphrased away** |

**What I did not run, and what that costs.**

- **`ci.sh`.** Quoted at `eb4971c6`, the orchestrator's, with its one skip named.
  I did not run it and running a second one would tell this revision nothing.
- **The browser.** Every browser figure in this revision — the 61 conformance
  specs, `F-CHT-3` end to end, QA-1's B1 and B2 mutants, the Chromium half of the
  C-6 disproof — is **somebody else's, with their name on it.** I ran no Chromium
  and this report has never claimed to.
- **G11.** Not run here, not runnable here, and box 7 does not rest on this
  revision.
- **QA-1's and L9-1's judgement, a sixth time.** Box 3 rests on
  `docs/qa/phase-4-grading.md` §11 and on `docs/reviews/fr-54.md`. I read both in
  full and checked the cheap facts under them — the three tool rows and the five
  tree rows above. **If QA-1's grade is wrong, this report is wrong with it**, and
  that is the correct exposure for a gate record rather than a defect in one.
- **Anything that would discharge somebody else's condition.** Q-1…Q-4 and
  Q-5, Q-6, Q-8 are QA-1's; FR54-7 is DEV-1's. **Q-7 is mine and is discharged in
  this pass, in the PRD, at PRD v1.5** — and it is the only one, which is the same
  sentence §2.8 ended on and the sixth consecutive revision it has been true.

---

## 3. Verdict per Phase 4 exit criterion

PRD **v0.9**, *Phase 4 — DX & docs*. Thirteen criteria. Each row names the
evidence and whose measurement it is. §4 argues the ones that needed judgement.
**Rows 7, 8, 12 and 13 were re-graded at revision 2**; each shows its superseded
verdict struck beside its replacement, and its argument is in the `.1`
subsection of §4 rather than in the revision-1 section above it.

| # | Criterion | Verdict | Evidence, and whose |
|---|---|---|---|
| 1 | **QA-1 builds a small, working app from the docs alone. This is the gate** | **MET, with three qualifications inside the tick** | **QA-1's**, [`docs/qa/phase-4-docs-alone.md`](../qa/phase-4-docs-alone.md) §6 at `452e1e74`: PASS, 2 m 12 s, first-attempt compile, real chromium with trusted clicks 0→1→2→3 and reload→0, zero console errors, **zero source-diving breaches**, 8 findings / 0 blockers. §4.1 for the qualifications |
| 2 | First working counter in **≤15 minutes and ≤31 lines** (FR-53, G7) *(budget amended 30 → 31 at PRD v1.1, countersigned at v1.2; this row said "≤30" through revision 3 and that was stale for two PRD versions — §7.10)* | ~~NOT MET at 46~~ → ~~NOT MET at 39, and it is the only box open on a measurement~~ → **MET, on QA-1's grade: PASS WITH CONDITIONS, at exactly 31, margin zero** | **QA-1's**, [`phase-4-grading.md`](../qa/phase-4-grading.md) §10 at `5d665226`. ≤15 min **PASS at 2 m 29 s** — a fresh QA agent, docs alone, no library source read, counter driven 0→1→2→3 in headless chromium with the navigation entry still at 1 and a pre-click sentinel alive after the third (`dab16364`), and QA-1's own drive **with a negative control** that reported a dead counter. ≤31 lines **PASS at 31** = **20 Go + 11 templ**, on both counting paths, cross-checked as *ordered sequences*; **PM-1 re-derived it independently** (§2.6 row 1) and got the same on four artifacts. **G7 discharged.** **The app shrank by eight and the number did not move**: DEV-1's page shell (`8680e8c5`..`679e6695`), **gated by L9-1 under FR-65 before it was counted** (`af4585b4` → `40b66b54`), conditions discharged at **+0 exported identifiers**. **Four conditions Q-1…Q-4 travel with the tick and PM-1 has discharged none.** §4.2.2; §5.10 for the count gate's number; PRD §5.I (e) for the five triggers, **none of which fires** |
| 3 | templ helper set **complete** and documented (FR-54) | ~~NOT MET, and unticksable — "complete" is undefined~~ → ~~NOT MET on evidence, against a definition, on three named failures — two of which have now moved without closing~~ → ~~NOT MET. Two of the three failures are FIXED, the third is DECIDED and unbuilt, and the box closes on L9-1's own three-part sentence~~ → **MET, on QA-1's grade: PASS WITH CONDITIONS. All three failures closed; four conditions travel with the tick** | **"Complete" is defined in PRD FR-54**, in FR-55's shape. **Failure 2 — FIXED at `2ab18690`.** Every `Bind` option moved out of its element attribute into the binding that declared it (`<domEvent>:<eventName>[:<key>[:<debounceMs>[:<throttleMs>[:<fields>]]]]`, trailing empties trimmed); `data-gotth-fields`/`-debounce`/`-throttle` no longer exist and **PM-1 verified their absence and their regression guard** (§2.8 row 1). **+0 exported identifiers and the artifact got SMALLER: −85 B minified, −8 B gzipped**, re-measured by L9-1 and again by **PM-1** (§2.8, tool row 2). Driven in Chromium against QA-1's own reproduction: the clear that was *destroyed* now arrives at ~1.7 ms beside the debounced draft at ~157 ms. **Failure 3 — FIXED at `b6bfe108`.** `examples/chat` implements Escape-to-clear as a real `EventClear` reducer case bound with `live.OnAll`, driven 6/6 in Chromium against the **shipped** example; `view.templ`'s comment now says the library can do it; `FRICTION.md` F-3 carries its `— Closed.` heading with the refusal argument kept above it. **PM-1 verified all four in the tree** (§2.8 rows 2–4). **Failure 1 — DECIDED, NOT FIXED**, by L9-1 at `e751f6de` ([`reviews/fr-54.md`](../reviews/fr-54.md) Part B): `Bind.NoModifiers` + `Bind.PreventDefault` **ACCEPTED** at **+0 identifiers, +2 fields (51→53), +62 B minified / +34 B gzipped**, `F-CHT-3` expressible, verified by prototype at zero delta across 156 client + 7 browser specs; the **full modifier set REFUSED** with a three-limbed re-open trigger. **The accepted surface does not exist yet.** **Plus six conditions FR54-1…FR54-6**, and L9-1's own closure sentence: *"FR-54's box closes when FR54-3, FR54-4 and FR54-6 are discharged and QA-1 grades them."* §4.3.3, §4.3.2, §5.6 ⟨**RE-GRADED AT REVISION 6 — MET. The verdict above is superseded and is kept, and one figure inside it is wrong: `+62 B minified / +34 B gzipped` is the pre-landing price and the landed price is `+81 / +38`, measured by PM-1 at §2.9 tool row 3 — §7.11 correction 1.**⟩ **MET, on QA-1's grade: PASS WITH CONDITIONS, and the conditions travel with the tick.** [`phase-4-grading.md`](../qa/phase-4-grading.md) **§11** at `eb4971c6`, committed `9efb7e5b`. **All three failures are closed.** **Failure 1 — CLOSED, by decision *and* by artifact.** The expressible half landed at `0b9e32e7`/`2311280b` (`Bind.NoModifiers`, `Bind.PreventDefault` as grammar components 7 and 8, **+0 exported identifiers, +2 fields 51 → 53**, both re-measured by **PM-1** at §2.9 tool row 1, zero output delta, `preventDefault` **below** the IME guard per C-9) and `F-CHT-3` is **driven end to end in Chromium 151**; the refused half — the full modifier set — is **REFUSED** under clause 3 at [`reviews/fr-54.md`](../reviews/fr-54.md) §13 with a three-limbed pre-registered trigger **whose every limb QA-1 fired themselves**: T-1's consumer count is zero and QA-1 counted rather than quoted it, T-2's envelope QA-1 measured with three constructed shapes, T-3 does not fire on a 61/61 browser run. **Failure 2 — FIXED** (`2ab18690`) **and pinned** (`42b4e0e6`): the property three documents claimed is now the mutant **M1** kills, exactly one spec red. **Failure 3 — FIXED** (`b6bfe108`): the affordance is implemented and **population clause (c) is EMPTY** — QA-1 **swept it on fifteen phrasings rather than inheriting it**, found ten sites, and every one is corrected beneath itself with none deleted. **FR54-3, FR54-4 and FR54-6 are discharged on QA-1's own runs, not on the reviewer's** — FR54-3 by removing the refusal and watching **10 of 316** `live` specs go red, FR54-4 by M1, FR54-6 by C-1…C-9 as corrected. **All nine of L9-1's pre-registered constraints hold as corrected, and QA-1 accepts all three amendments**, C-6's on evidence QA-1 drove in node **and** in Chromium. **Four conditions — Q-5 (L9-1), Q-6 (DEV-1), Q-7 (PM-1, discharged at PRD v1.5), Q-8 (DEV-1) — travel with the tick and none of them makes any binding inexpressible, uncomposable, undecided or undocumented**, which QA-1 names as the only test that could have held the box open. **FR54-7 travels behind and is open.** §4.3.4, §5.11, §6 |
| 4 | **Dev reload works for Go and templ changes** (FR-57) | **MET on the behaviour** ~~*, with the missing regression guard carried as an exit condition*~~ → **and the regression guard now exists: the condition inside this tick is DISCHARGED** | **DEV-2's**: templ change → reload, Go change → reload, plus the negative control (a byte-identical rebuild restarts the process and reloads nothing). 1,260 B of 8,192, CI-gated at `ci.sh:669`; PM-1 re-measured the artifact at **1,260 B** (§2.8, tool row 2). **New at revision 5: `test/internal/conformance/dev_reload_test.go` (`13a1ca1e`) makes all three of those standing browser specs**, in the conformance package's own idiom, and mutation control **C** (`live.executableBuildID` frozen to a constant) turns both reload specs red while the negative control stays green — which is the shape that proves a control controls. **The millisecond figures are demoted to observations by their author**: 1,208 ms / 807 ms this run against 1,814 ms / 1,211 ms on the same host, and nothing asserts a millisecond. §4.4, §4.4.1 |
| 5 | **Live session inspector**: separate opt-in file, causal chain, does not load in production, ≤40KB gzipped (FR-44, NFR-8) | **MET, with two qualifications** ~~*, the second of which is a condition*~~ → **and the second qualification's condition is DISCHARGED; one of the two qualifications was itself partly false and is corrected** | **DEV-2's**: **6,211 B of 40,960**, re-measured by **PM-1** at 6,211 (§2.8, tool row 2), `ci.sh:669`; the chain folded from real codec frames in `client/test/inspector.test.mjs`; `Config.Dev` gates serving (404) and rendering (zero bytes). **New at revision 5: `test/internal/conformance/inspector_test.go` (`13a1ca1e`) makes the browser half two standing specs**, reading the panel out of its open shadow root and asserting that the row number in the patch's `← #n` **is** the event row's own number — with mutation controls **A** (`render()` schedules nothing → both specs red at `rows=0`) and **B** (the `client_ref` join removed → the panel still paints and the first spec goes red on precisely the join). **And the control that was meant to reproduce `0c711b70` disproved it**: the "Illegal invocation" mechanism this row's §4.5 states does **not** reproduce in Chromium 151. §4.5, §4.5.1 |
| 6 | All three examples **polished, documented, green in CI end-to-end** (FR-60…FR-63), including the dashboard's re-measured resync figure | ~~NOT MET — graded by nobody~~ → **MET, on QA-1's grade, which was a FAIL first** | **QA-1's**, `phase-4-grading.md` §4.5 → §9.1.7: **FAIL** at `091dbae8` on the *documented* conjunct in six places, DEV-3 remediated (`da827962`, `64d7ddfb`, `986ef434`), **PASS** at `368132f6`. The three spec counts are now held by a `ReportAfterSuite` check whose red **and vacuous-pass** paths QA-1 drove themselves. **DEV-3 declined one prescription with evidence and QA-1 ruled in their favour.** §4.6.1 |
| 7 | `git clone && cd gotth-live/examples/<name> && go run .` with no node, npm, protoc or refinec (G11), **wording corrected in PRD v0.9 row 1** | ~~NOT MET — asserted, never run~~ → ~~NOT MET — the property is measured green and QA-1 has not graded it** | **DEV-2's**, [`docs/qa/g11-clean-clone.md`](../qa/g11-clean-clone.md) at `5c751ae9`: a depth-1 real clone in `golang:1.25-bookworm` (`golang@sha256:ea341baa…9d58`, Go 1.25.12) with node, npm, protoc, refinec, templ, buf and protoc-gen-go **proved absent fatally before anything ran**; all three examples served their `data-gotth-region` markup and **10,391 B** of `gotth-live.min.js` from the URL each page named; clone pristine before **and after**; `--deadline 1` negative control turns it red. **PM-1 checked at HEAD:** `grep -n "G11" ci.sh` → **17 lines**, step at `ci.sh:876`. ~~**G11's gate is QA-1 and QA-1 has graded nothing.**~~ → **MET. QA-1 graded it PASS with no conditions**, re-running the runner themselves, checking the image and the clone directly rather than through the runner's own output, and building a **fourth** negative control: a `node` shim on `PATH`, which the runner refuses. §4.7.2 |
| 8 | Docs set complete per FR-59, including "when not to use this" | ~~NOT MET — seven of nine subjects~~ → ~~NOT MET — nine of nine by count, eight by the standard the phase is about~~ → **MET, on QA-1's grade: PASS with one condition, and the condition is closed** | **DEV-3's**: `guide/deploying.md` (`d7353b5e`) and `guide/security.md` (`5238c85a`), each with compiling samples under `docs/guide/_samples/`, indexed in `docs/README.md` at `f34ef2ca`. **PM-1's ruling (§5.4): the architecture subject is not discharged** by `rfc/001-architecture.md`, which `docs/README.md` files under *"none of it is needed to build an application, and all of it argues rather than instructs"* — **raised by DEV-3 against their own delivery**. **Discharged at `22a47a6b`** by a reader-facing page, and **QA-1 graded the page against my ruling's own terms** including the honest test, drove its sharpest claim with a probe **and a control** (a 9 s `Authorize` self-closes 4010; a quiet session on the same limits does not), and checked every default, close code and exported symbol the set names against the shipping source. Condition **F-10** (`docs/README.md:24`'s stale "27 lines of Go") **closed at `b04ba138`**, verified by PM-1. §4.8.2 |
| 9 | **FR-77's documentation half delivered** on the two pages FR-77 names | **MET on all four clauses** | **PM-1's check, against the box's own wording**, in the two pages DEV-3 landed at `f61f7ace`. Four line-anchored citations in §2.2 row 4. §4.9 |
| 10 | **Godoc CI check green**: zero undocumented exported symbols; package overviews present (FR-66) | **MET against FR-66 as amended in this landing** — the amendment is PRD §9 v0.8 row 2 and it is a narrowing, not a restatement | **DEV-1's**: `tools/doccheck` at `ci.sh:660`, its own 24 tests at `ci.sh:617`, **142 undocumented symbols found and fixed** in the published module, 2 package overviews as runnable `Example()`s. **268 symbols sit outside the enforced scope**, printed every run. §4.10, and §5.1 for the ruling |
| 11 | Godoc `Example*` functions compile and run under `go test` (FR-68) | **MET, and this one carries no carve-out** | **DEV-1's**: rule 4 enforced in **every** module; **2 → 6 examples**, all six with `// Output:`, counted by PM-1 at HEAD (5 in `live`, 1 in `live/livetest`). Residual: `live/livetest`'s harness half cannot carry examples at all. §4.11 |
| 12 | **Error-message audit** (FR-58) | ~~NOT MET — not started~~ → ~~NOT MET — the audit exists and QA-1 has not graded it~~ → **MET, on QA-1's grade: PASS, condition discharged** | **DEV-1's**, [`docs/error-audit.md`](../error-audit.md) at `70c78b60`, headline corrected at `134e69c5`: **117 sites / 8 packages / 25 graded failures / 25 fixed** (`ba5ce082`, `4d28146f`) **+ 4 non-site defects = 29 changes**. Three regression guards. **PM-1 checked the census sums to 117 and the "was …" rows count 25** (§2.4 rows 1–2). **DEV-1's own document says QA-1's grade is what ticks the box**, and QA-1 gave it. They **re-implemented §2.1's rule from the audit's prose** in their own AST program — 117 package-for-package at the graded tree, 119 at HEAD matching revision 3's headline — **mutated all three guards red**, and found the one real defect by driving rather than reading: `livetest.NextErr` returned five messages **without** the session prefix five audit rows claimed. DEV-1 fixed it at `131cb3cb`; QA-1 discharged the condition by removing the fix on a copy and watching all three specs go red. §4.12.2 |
| 13 | **`docs/exceptions.md` reviewed** (FR-20) — **SPLIT at revision 3**; this is the **Phase-4 half**: the register exists, is walked against the shipped tree, and every row carries an L9-1 disposition | ~~NOT MET — the file does not exist~~ → ~~NOT MET on three independent grounds~~ → **MET, on L9-1's signature and on the split** | **L9-1's**, `bdf91971`, note at [`docs/reviews/phase-4-exceptions.md`](../reviews/phase-4-exceptions.md). Taking revision 2's three grounds in reverse: **(c)** *"cannot tick before Phase 5"* is **resolved by splitting the criterion** — §5.7, and the Phase-5 half is now its own box in PRD §6. **(b)** Signed, all three rows. **(a)** E-2 **fixed** at `091dbae8` and verified by L9-1, so revision 2's exit condition is discharged; E-1 remains as an **accepted** exception with an argument, which is what FR-20 asks of a deviation rather than a reason to hold a box. **Rows without a disposition: zero.** §4.13.2 |

~~**Six met, seven not. Phase 4 does not exit**~~ *(revisions 1 and 2)*, by §6's
own rule that a phase exits when every box is checked and the gate owners sign
off.

**Revision 2's four re-grades all stay in the NOT MET column and none of them
stays there for its original reason.** That is the whole content of this
revision: rows 7, 8, 12 and 13 were graded against absent artifacts, the
artifacts are here, and the boxes are now blocked on the second clause of §6's
exit rule rather than the first. Rows 2, 3 and 6 are untouched — FR-53's
conjunction still fails at 46 (re-counted, §2.4 row 11), FR-54's "complete" is
still undefined and still mine to define, and the examples' "polished and
documented" clause is still graded by nobody.

**Revision 3, at `b04ba138`: eleven met, two not. Phase 4 still does not exit**,
by §6's own rule.

**Five boxes moved, and the sentence that moved them is the one revision 2 ended
on.** Rows 6, 7, 8 and 12 tick on **QA-1's grades**; row 13's Phase-4 half ticks
on **L9-1's signature** and on §5.7's split. **None of the five ticks on PM-1's
reading of the deliverable**, which is exactly what §5.2's rule and revision 2's
refusal were holding out for: the second clause of §6's exit rule was satisfied
by the people it names, in one turn, because they were asked.

**Rows 2 and 3 both moved without ticking, and that is the more interesting
half.** FR-53 shrank from **46 to 39** — the app shrank, the number did not — and
§5.8 now argues that the number was never reachable, which makes this the box
most likely to need an amendment rather than an engineer. FR-54 went from
**unticksable to unmet-on-evidence**, which is the entire value of defining a
word: the box can now be argued with. **Rows 1, 4, 5, 9, 10 and 11 are untouched
by this revision** and none of the FR-58 remediation, the FR-53 shrink or the
docs landing re-opens them — the counted `Example*` census, `docs/api-surface.md`
and the client artifacts are where §2.4 left them.

**Revision 4, at `5d665226`: twelve met, one not. Phase 4 still does not exit**,
by §6's own rule.

**One box moved and it is row 2, and the sentence that moved it is not one this
report wrote.** Box 2 ticks on **QA-1's grade**, over an artifact **L9-1 gated
first** under FR-65 against constraints written before it existed. **Rows 1, 4,
5, 6, 7, 8, 9, 10, 11, 12 and 13 are untouched by this revision**, and nothing in
the page shell, its discharge or the FR-54 reproduction re-opens any of them —
`tools/apisurface` reports **56/56 identifiers and 51/51 fields** across the whole
landing, so the surface delta is `+2` and the discharge cost `+0`; `ci.sh` was
green in full at `679e6695` and again at `8be955e5`, both runs L9-1's.

**Row 3 moved without moving, and that distinction is worth as much here as it
was at revision 3.** FR-54 gained a **measurement** where it had a derivation and
a **corrected reason** where it had a false one, and neither closes a failure.
**The box is exactly as far from ticking as it was** — three failures, each
needing to be closed or refused with an argument and a re-open trigger — but two
of the three are now decidable on evidence rather than on somebody's reading of
three source files, which is what the condition §6 carried was for.

**Revision 5, at `e751f6de`: twelve met, one not. Phase 4 still does not exit**,
by §6's own rule. **No row was re-graded. Three rows were re-stated and the count
is unchanged for the first time since revision 1.**

**This is the accounting revision 5 owes, and the honest form of it is
subtraction rather than addition.** Four landings and a technical ruling arrived
between `5d665226` and `e751f6de`. **What they bought, box by box:** row 3's
failures 2 and 3 went from *open* to **fixed**, and failure 1 went from
*undecided* to **decided**; rows 4 and 5 lost the qualification that had been
sitting inside their ticks since revision 1. **What they did not buy is a
tick.** Rows 1, 2, 6, 7, 8, 9, 10, 11, 12 and 13 are untouched by this revision,
and nothing in the four landings re-opens any of them — `tools/apisurface` reads
**56/56 identifiers and 51/51 fields** at `e751f6de`, run by PM-1, so the surface
is where revision 4 left it; `tools/minify` reads **10,306 / 4,421**, which is
*smaller* than revision 4's 10,391 / 4,429 and is the only figure in the report
that moved in the direction a budget likes; and `f0690a2c` is a **Phase 3** gate
act that touches no Phase 4 box.

**Why row 3 still does not tick, stated in the form that can be checked rather
than argued.** FR-54's clause 3 asks that each failure be *fixed or refused with
an argument and a re-open trigger*. Failure 2: **fixed**. Failure 3: **fixed**.
Failure 1: **refused in one half with a trigger, accepted in the other — and the
accepted half has no artifact.** L9-1's closure sentence is not this report's
paraphrase, it is theirs: *"FR-54's box closes when FR54-3, FR54-4 and FR54-6 are
discharged and QA-1 grades them."* **Three conditions and one grade.** At the time
of writing, FR54-3 has no code, FR54-4 has a spec that L9-1 wrote and did not
commit, FR54-6 has a prototype in a container's `/tmp`, and QA-1 has not been
asked. **A box that would tick if four things happened is not a box that ticks.**

**And one thing this revision must not be read as saying.** Two of the three
conditions §6 carried are discharged and the third is not, so the phase is
blocked on **one box plus one page**: box 2's Q-1/Q-2 remediation landed at
`f555f3b5` and **QA-1 has not graded it.** That is the same shape revision 2
found and revision 3 resolved — the phase is blocked on somebody being asked, and
§8.4 says who.

**Revision 6, at `9efb7e5b`: THIRTEEN met, none open. PHASE 4 EXITS**, by §6's own
rule — every box checked, and the gate owners have signed. **One row was
re-graded, row 3, and it is the last one.**

**The accounting, in the same subtractive form revision 5 used, because the
addition is the easy half.** Ten landings, one ruling and one grade arrived
between `e751f6de` and `9efb7e5b`. **What they bought:** row 3's failure 1 went
from *decided* to **closed**, on an artifact and a browser run rather than on a
prototype in a container's `/tmp`; L9-1's three blocking conditions FR54-3, FR54-4
and FR54-6 were discharged and then **re-discharged on QA-1's own mutations**; and
QA-1 gave the grade that L9-1's closure sentence named. **What they did not buy,
and this is the half that has to be said in the exit revision rather than
after it:** they did not discharge Q-1…Q-4, they did not discharge Q-5, Q-6 or
Q-8, they did not close FR54-7, and they did not run G11 or a second browser.
**Rows 1, 2, 4, 5, 6, 7, 8, 9, 10, 11, 12 and 13 are untouched by this revision.**
`tools/apisurface` reads **56/56 identifiers and 53/53 fields** at this tree, run
by PM-1 — identifiers unchanged from revision 5, fields +2, which is exactly the
accepted surface and nothing else; `tools/minify` reads **10,387 / 4,459**, which
is **larger** than revision 5's 10,306 / 4,421 by the +81 / +38 the Part B shape
costs, against an NFR-2 ceiling of 12,288 with **63.7 % headroom**.

**Why row 3 ticks, stated in the form that can be checked rather than argued** —
which is the same form §3 used for five revisions to say why it did not.
FR-54's clause 3 asks that each failure be *fixed or refused with an argument and
a re-open trigger*. **Failure 2: fixed, and the mutant that reintroduces it turns
exactly its owning spec red.** **Failure 3: fixed, and clause (c) is empty on a
sweep QA-1 ran rather than inherited.** **Failure 1: fixed in the half that was
accepted and refused in the half that was not** — and the refusal is a real one,
because QA-1 fired all three limbs of its trigger and none fired. L9-1's closure
sentence is not this report's paraphrase: *"FR-54's box closes when FR54-3, FR54-4
and FR54-6 are discharged and QA-1 grades them."* **All three are discharged. QA-1
has graded them. The box closes on its own stated terms and not on this report's
reading of them.**

**And one thing this revision must not be read as saying.** **Phase 4 exiting is
not the project finishing.** Phase 5 — the benchmark measurement, the report and
the feature-parity table — is what remains, and **no benchmark timing has been
collected.** §7.6 argued at revision 2 that Phase 4 could not exit before Phase 5
began, and §5.7 resolved that by **splitting box 13**; the Phase-5 half of FR-20
is still a Phase-5 box and is untouched by this exit. A reader who takes thirteen
of thirteen for a finished project has been handed the §7.2 defect in the
direction that attracts no second reader, and §8.5 spends a paragraph on it.

---

## 4. The boxes, one at a time

### 4.1 The gate — MET, and the caveat travels with it

**QA-1's PASS is unqualified as a PASS and heavily qualified as evidence, and
both halves are theirs rather than mine.** I am carrying the second half into the
tick because a tick that swallows it is how it stops being found.

1. **It measures a document that is copy-paste-correct, not one that survives
   being deviated from.** QA-1's own sentence. F-1 and F-2 — the two
   high-severity findings — were produced by *deliberately building the wrong
   variants*, and in both cases the quickstart's own troubleshooting text sent
   the reader the wrong way: a `404` diagnostic that could not fire behind a
   catch-all, and a "your router strips the prefix" instruction that produces a
   permanently reconnecting page. So the PASS is not evidence that the page
   diagnoses its own failure modes. Measured, it did not.
2. **QA-1 is not a human developer.** Their words: 2 m 12 s attests that the Go
   half of the quickstart compiles as printed, not that a person takes that long.
   The number is real and it is not a human-factors result.
3. **The page has changed since the gate.** The gate was held at `452e1e74`;
   DEV-3 then rewrote the quickstart in seven places *in response to this gate's
   own findings*, verifying each by rebuilding the failing variant rather than by
   reading. **That remediation has not been re-gated docs-alone by QA-1**, and it
   is not owed by this box — the box asks that the gate be held, and it was. But
   a reader who assumes the PASS describes the page in the tree today is wrong by
   seven edits, and this is where they find that out.

**What I am not doing is manufacturing a blocker.** QA-1 declined to and said
why; a PASS with recorded friction is what this is.

### 4.2 FR-53 — NOT MET, and it is a conjunction that failed on its second half

**≤15 minutes: PASS at 2 m 12 s. ≤30 lines: MISS at 46. The box is an "and".**

The counting rule was fixed in v0.6 *before* this gate, precisely so the agent
running the gate would not be the one deciding which number their own gate is
measured against. It binds Go **plus** templ. QA-1 applied it, counted
independently, and **reproduced the published 46 exactly** — which is a stronger
result than agreement, because an independent count that lands on the same number
is evidence the method is reproducible rather than interpretive.

**I re-counted at `8a06cb04`, after DEV-3's remediation, and got 27 + 19 again.**
That check exists because seven documentation fixes are exactly the kind of event
that quietly re-opens a measurement, and one of them (F-4) is *about* the line
that would change if the app shrank. It did not change: the counted `main.go`
still registers `templ.Handler(Page(State{}))`, and the per-request alternative
F-4's fix introduced is prose beside the block, not inside it. **DEV-3
deliberately did not chase the number**, which is the right call — chasing it
with prose is how a budget starts measuring file layout.

**What would close it, and it has not moved either:** the app shrinks. Twelve of
the 27 Go lines are the eight `Config` fields `live.New` requires, so most of the
16-line overage is **library ceremony**, which makes it DEV-1's finding and not
DEV-3's. What is new this round is that the candidate is concrete rather than
aspirational: **F-4's API fix** — a `live`-owned page handler taking the same
loader `Init` takes — would delete the `templ.Handler(Page(State{}))` line and
make the frozen-first-paint mistake unwritable in the same change. That is one
line of the 46 and one class of bug, and it is the only shrink anybody has
specified.

**Raising 30 is pre-registered as unavailable** (§9's preamble, RFC-0001 §6.1.2),
and this is the pass that measured the miss, which is specifically the pass in
which it may not be moved. **Owner: DEV-1 (the API), with PM-1 owing an argument
— not a gate day — on whether 30 was ever reachable for a real HTTP server plus a
view.**

#### 4.2.1 Revision 3 — the app shrank, the number did not move, and the miss is now nine

*(2026-08-05, at `b04ba138`.)*

**DEV-1 did exactly what this box has asked for since v0.6.** `cd2c4cac`..
`fde707f0` added three symbols — `(*App[S]).PageHandler`, which loads through
`Config.Init` per request so it **cannot be given a frozen state**; `(*App[S]).Mux`,
which makes the missing subtree registration and the `StripPrefix` repair
unexpressible rather than documented; and `MustNew` — and made `Config.Init`
optional. **27 Go → 20. The count is 20 + 19 = 39 against 30.**

**Three people counted and got the same thing**, which is why this number is
worth more than the last one: DEV-1 at the shrink, **QA-1 independently over the
shipping sample** in `docs/guide/_samples/quickstart/` (`phase-4-grading.md`
§9.2.6), and **PM-1 at HEAD** over the page's two blocks. §2.5 row 1.

**L9-1 reviewed all three symbols and ruled KEEP on `MustNew` against this very
budget**, measuring the economy rather than accepting it: three counted lines,
`regexp.MustCompile`'s exact shape, and the misuse — building a `Config` out of
runtime configuration — fenced in the godoc rather than left implicit. Their
strongest cut argument was that `MustNew` *"does not close FR-53's gap"* — 39
with it, 42 without, failing either way — and it loses because **the remaining
nine are in the `Config` literal and the templ view, where every candidate for
removal is a security hook or the event binding this library exists to provide.**

**Which is the whole of §5.8, and it is the argument this section has owed since
v0.6.** The short form: hiding the entire HTML document behind a `live.Document`
component lands at **31**, and the only route from 31 to 30 is the security-hook
bundle this project has refused twice. **The box stays open, the threshold does
not move here, and the amendment is pre-registered rather than made.**

#### 4.2.2 Revision 4 — MET, at exactly the number that was costed before the artifact existed

*(2026-08-05, at `5d665226`. §4.2 and §4.2.1 are left standing. **This box has
been open since v0.6 and it is the only box in this phase that was ever open on a
measurement.**)*

**MET. QA-1's grade, `5d665226`, [`docs/qa/phase-4-grading.md`](../qa/phase-4-grading.md)
§10: PASS WITH CONDITIONS.** **≤15 minutes: PASS at 2 m 29 s. ≤31 lines: PASS at
exactly 31, margin zero. G7: discharged.** **I did not grade this and I am not
re-grading it.** What follows is what I checked, what I applied, and the one
thing I refuse to let the tick swallow.

**The route it closed by is the whole of why this tick is worth something.**
§4.2's own answer to *"what would close it"* has been the same since v0.6 — **the
app shrinks, not the number** — and that is what happened, in the order §6's exit
box names:

1. **DEV-1 built** `(*App[S]).Document` and `live.NoRuntime` (`8680e8c5`), with
   the three examples as non-quickstart consumers (`3c66cc04`) and the FR-58
   census fired on the one new error in the same commit as the grading
   (`679e6695`) — which is §5's own rule about the census, honoured without being
   asked.
2. **L9-1 gated it as new surface under FR-65 *before it was counted***
   (`af4585b4`, [`docs/reviews/page-shell.md`](../reviews/page-shell.md)), against
   **nine constraints they pre-registered at their §3.3 before the artifact
   existed**. **ACCEPT WITH CONDITIONS**: eight passed, and the ninth **failed on
   its claim rather than its behaviour** — the symbol's central justification is
   that the `InspectorScript`-above-runtime ordering is made *inexpressible*, and
   a head extension carrying `live.Script` puts a runtime tag **above** the
   inspector, which (both being deferred) opens the socket before the inspector
   wraps `WebSocket` and blinds it silently for the life of the page. **That is
   the failure `api-surface.md:272` describes**, not the *"different mistake with
   a different shape"* the disclosure called it, and the sentence was live in
   **five** places including the ledger row that spends an identifier on it.
3. **DEV-1 discharged it in code, at +0 exported identifiers** (`cbad05d8`,
   `e7d47de6`, `8be955e5`), taking L9-1's route (a) over the cheaper route (b) on
   a ground L9-1 says is better than the one they gave themselves: restating the
   sentence would have demoted the justification to *"inexpressible unless you
   hand it a runtime tag"*, which is close to no claim at all.
4. **L9-1 ACCEPTED** (`40b66b54`) on **six probes of their own**, rebuilt from
   scratch rather than re-run from DEV-1's report, and on **seven mutants, seven
   killed** — each by the spec that owns that behaviour and, in every case, by
   *only* those specs out of 274. **Two deliberate boundaries are pinned by
   specs rather than described**, so neither can move without moving a spec.
5. **QA-1 counted and graded.**

**What I checked myself, because a report that applies a grade should have done
something.** §2.6 rows 1–3: **the count re-derived on four artifacts by my own
classifier** (20 + 11 on the page's fences, 20 + 11 on the pinned samples);
**`validate`'s seven required fields re-read at HEAD**, which is trigger 2's
subject; and **`git merge-base --is-ancestor 667d3db7 8680e8c5` → true**, which is
L9-1-C2 and is the single fact that decides whether this PASS means anything.
**Under trigger 1's pre-repair text the budget would have moved up to whatever the
shell cost and this box could not have failed at any cost.** It could have failed
and it did not.

**The five triggers were evaluated and none fires**, which is PM-1's act and is
recorded at PRD §5.I (e) rather than here. In one line: **trigger 1's condition is
a total *other than* 31 and the total is 31; trigger 4 needs the app *below* the
budget and 31 is not below 31; trigger 5 needs a cause other than a library shrink
and this was one; triggers 2 and 3 are unmoved.** **The budget did not move in
either direction, in the pass that closed the box.**

**The thing I refuse to let this tick swallow, and it is QA-1's finding rather
than mine.** **The margin is zero and nothing in this tree can fail if the count
goes to 32.** There is no line-count assertion anywhere; the samples suite that
§7 of L9-1's review credits with holding the two counting paths together
**does not hold a count in either direction** — QA-1 mutated it four ways and two
of the four stayed green, including a doc block that repeats an existing counted
line four times. **The whole margin is one 84-column call that no formatter will
ever reformat**, and L9-1 measured that `templ fmt` is idempotent on both it and
a hand-wrapped four-line version counting 15. **That is Q-4, it is the condition
QA-1 says they would keep if they could keep only one, and §5.10 is where I
authorise the number it needs.**

### 4.3 FR-54 — NOT MET, and the reason is a word

The helper set is real and it is documented: `Region`, `On`, `OnAll`, `OnWith`
with `Bind.Keys`/`Fields`/`Debounce`/`Throttle`, `Preserve`, `Script`, with the
full attribute vocabulary tabulated in `client/SIZE.md` §7 and the reader-facing
page at `guide/events-and-forms.md`. Phase 2's own FR-54 box — bindings
expressible from templ with no hand-written JS — is ticked and stays ticked.

**This box asks something Phase 2's did not: that the set be *complete*. Nobody
has said what that means.** This is FR-55's problem one requirement over: v0.5
had to rule what "first-class" meant before Phase 4 could build on it, for the
same reason it has to be ruled here — a Phase-4 box cannot be ticked against an
undefined word, and the agent who ticks it would be defining it in the act.
**Owner: PM-1 to define completeness on DEV-2's and DEV-3's evidence; QA-1
gates.** The likely shape, stated so the next turn has somewhere to start: the
set is complete when every event the three examples and the guide actually bind
is expressible without a hand-written attribute string, and any that is not is
either added or refused with the FR-55 argument.

#### 4.3.1 Revision 3 — defined, and the definition rejects the shape §4.3 proposed

*(2026-08-05, at `b04ba138`. **The ruling is PRD v1.0's FR-54 and §5.6 here; this
subsection is the grade that follows from it.**)*

**"Complete" is defined, and the first thing the definition had to do was throw
out the shape the paragraph above proposed.** §4.3's *"every event the three
examples and the guide actually bind"* is **circular**: an interaction the
library cannot express is an interaction the examples work around and therefore
do not bind, so the set of bindings-in-the-tree is partly *defined by* the gaps
and cannot measure them. The worked case is the one this box is about — the chat
composer uses a real `<form>` for Enter-to-send and omits Escape-to-clear
entirely, so under §4.3's shape **its two missing keyboard behaviours would have
counted as evidence of completeness.** §5.6 is the ruling; the population adds
the equivalence spec's frozen §2 tables and anything the repository says is
absent because it is inexpressible.

**Graded against it: NOT MET, on three failures**, each stated with the file and
line in §5.6 and in the PRD. In one line each: `F-CHT-3` is inexpressible and has
been **reported twice and refused never**; `Debounce` is element-scoped so the
guide's own composer silently debounces its `Escape` binding and a following
keystroke cancels the clear; and `examples/chat`'s F-3 still calls inexpressible
the binding that landed at `591c275a` **citing F-3 by name**.

**What changed about this box is not its verdict but what can be done with it.**
At revisions 1 and 2 it was unticksable — no evidence could have moved it,
because there was no standard to move it against. It is now unmet **on evidence
somebody can argue with**, with three named owners and a stated closing
condition. That is the debt revision 2 recorded as *"debt with my name on it and
it did not move"*, discharged.

#### 4.3.2 Revision 4 — still NOT MET, and now it is the only open box; two of the three failures moved

*(2026-08-05, at `5d665226`. §4.3 and §4.3.1 are left standing.)*

**NOT MET. The verdict does not move and two of the three failures do.** With box
2 green this is now the **only** open box in Phase 4, which is a fact about the
phase rather than about the requirement.

**Failure 1 — unchanged, undecided, and it is the one nobody has touched.**
`Bind.Keys` still compares `KeyboardEvent.key` and not modifier state, and a key
binding still never calls `preventDefault`, so `F-CHT-3` — *"Enter sends,
Shift+Enter newlines"*, `docs/bench/equivalence-spec.md:214` — is still
inexpressible. **It has now been reported three times and refused never**, which
is exactly what clause 3 counts as a failure. **Nothing has been routed to me to
rule on and I am not ruling unasked**: the refusal, if that is the answer, needs
DEV-2's client cost first, and both halves have real refusal arguments (a chord
belongs to the browser; a library that `preventDefault`s on the application's
behalf takes over `Ctrl+F`). *(Citation drift: §5.6's row cites
`docs/api-surface.md:615`; DEV-3 routes it as `:651` and it is actually **`:696`**
— §2.6 row 7. `bench/README.md:553` → **`:670`** ✔.)*

**Failure 2 — DISCHARGED as a condition, still open as a failure, and the
observation is worse than the derivation.** §5.6 said, and §6 carried as a
non-blocking condition, that *"an observation is worth more than a derivation and
this project has twice found that the browser is where this class of defect
actually lives — it should be driven before it is fixed."* **QA-1 drove it**
(`97ab20fb`, [`docs/qa/fr-54-debounce-repro.md`](../qa/fr-54-debounce-repro.md)):
Chromium 151 over CDP, the real shipped minified runtime, markup rendered *from*
`live.OnAll`/`live.OnWith`/`live.Region` with no hand-written `data-gotth-*`
anywhere, measured at **server-side arrival** rather than by DOM change. **Verdict:
REPRODUCES.** Eight specs, `8 Passed | 0 Failed`, **three negative controls**
including an undebounced twin differing in exactly one field and a **mutation
control** — a per-binding timer slot, +15 B minified — that turns **three of the
eight red**. Every one of §5.6's four cited sites still lands.

**Three things the observation adds, and each makes the defect larger than §5.6
said:** the clear is **destroyed, not delayed** — one event reaches the server for
the pair and it is the draft, with no error, no console warning and nothing on the
wire; the interference is **symmetric**, so an `Escape` inside the window destroys
a pending *draft* and the browser goes on showing a character the server was never
told about; and the key binding is **late by an interval it never asked for** even
when nothing follows it (158.8 ms against 1.3 ms undebounced) — **a defect the
mutation control shows *survives* the per-binding-timer fix.** Two defects, one
cause, and a repair that addresses only the `WeakMap` slot leaves the second
standing. **One correction against my §5.6, which narrows it:** it is the `input`
event and not the keystroke that cancels — `ArrowLeft` inside the window leaves
the clear standing — so *"a keystroke inside that window"* is right for the case
it names and slightly over-general as a rule.

**QA-1's §7 is a recommendation and I am recording it as one**, because that is
what it says it is: extend the `domEvent:name:key` grammar with the per-binding
numbers and read `d`/`th` off the matched spec rather than off the element — no
new `Bind` field, `Fields` left element-scoped where it belongs, `OnAll`'s
first-wins rule kept for what is genuinely shared, and tens of bytes in a bundle
with 64 % headroom. **`client/runtime.js:588`–`:593` makes that argument in as
many words about the key filter, one release early, and it was not carried across
to the timer sitting beside it.** **The API choice is L9-1's under FR-65 and this
report does not make it.**

**Failure 3 — the reason is corrected and the failure is RELOCATED rather than
closed.** DEV-3 fixed `examples/chat/FRICTION.md` F-3 at `e1a56a0e`, on QA-1's
measurement rather than on an assertion, and did it in this project's shape: both
false sentences quoted and corrected beneath themselves, the number and the
conclusion kept, the **"Proposed shape"** block keeping the call it drew and
gaining what now happens to a reader who writes it. **They declined a
*"— Closed."* heading and the reason is right and is the inverse of the defect
the item is about**: F-1 and F-4 closed because the feature arrived **and** the
specs weakened by its absence now do what they were written to do; here the symbol
arrived and the affordance did not.

**But `examples/chat/view.templ:64`–`:68` still reads *"live.On has no key filter
… Escape-to-clear has no expression at all and is therefore absent"***, and
`view_templ.go:188`–`:192` carries the generated copy (§2.6 row 5). Both have been
false since `591c275a`. DEV-3 reports them at `FRICTION.md:184`–`:187` and leaves
them as another file's; **I do not agree that they are another file's** —
`examples/**` is DEV-3's by the role list, and the generated copy follows from the
templ one through `gen.sh` — but `examples/**` is outside my write scope, so it is
**routed and not ruled**. **Until that comment moves, the tree still states as
inexpressible something the set expresses**, which is the box's *documented*
conjunct, so failure 3 is not closed. **And it is now in the worse of the two
places**: the friction note is a document a reader may reach, and the source
comment is the one a reader of the example arrives at by construction.

**One open question DEV-3 routed to me is answered, and the answer is in PRD
FR-54's own text rather than here** because it is a ruling about a requirement's
population and not a gate finding. In summary: **F-3-the-note has left population
clause (c) by its own repair; `view.templ:64` has taken its place in it, because
"any document in this repository" cannot exclude a shipped example's own source
without excluding the artifact class QA-1 failed box 6 over; and the interaction
was never in the measured set by way of F-3 alone** — the guide's composed sample
is population **(a)** and is the element QA-1 actually drove. **(a)/(b)/(c) fix
what is measured and clauses 1–4 fix what is measured about it**, so *"clause (c)
or clause 2?"* has the answer *both, on different axes*. **The check that cut
against the comfortable answer is §2.6 row 6 and it is recorded rather than
buried**: population (b) does **not** catch this — `F-CHT-1`…`F-CHT-9` contains no
Escape-to-clear row — so the ruling rests entirely on (a) and on the source
comment, and PRD FR-54 carries a pre-registration for the day the guide's sample
changes.

**What closes this box is unchanged**: each of the three failures fixed **or
refused with an argument and a re-open trigger**, per FR-54's clause 3. **Owners
unchanged: DEV-2 and DEV-1 on the two API questions, DEV-3 on the documentation
one, L9-1 on any new surface under FR-65, PM-1 on a refusal if that is the
answer; QA-1 gates.**

#### 4.3.3 Revision 5 — two failures fixed, one decided and unbuilt, and the box does not close

*(2026-08-05, at `e751f6de`. §4.3, §4.3.1 and §4.3.2 are left standing. **§4.3.2's
sentence — *"Nothing has been routed to me to rule on and I am not ruling unasked"*
— is the one this section retires: it was routed, it was ruled, and the ruling is
L9-1's rather than mine, which is what FR-65 says it should be.**)*

**NOT MET. Two of the three failures are fixed, the third is decided, and the box
is further along than it has ever been and no closer to ticking than L9-1 says it
is.** That sentence is uncomfortable and it is the accurate one.

**Failure 2 — FIXED at `2ab18690`, and the artifact got smaller doing it.** Every
`Bind` option now travels inside the binding that declared it:
`<domEvent>:<eventName>[:<key>[:<debounceMs>[:<throttleMs>[:<fields>]]]]`, trailing
empties trimmed, so **every binding in the tree that declares none of the three
renders the bytes it always did.** The three element attributes are gone — I
checked, and the only hits left in the tree are the specs asserting their absence
(§2.8 row 1). **`+0` exported identifiers, and I ran `apisurface` myself to say
so**: `live 56/56, 51/51`. **−85 B minified and −8 B gzipped**, and I ran
`minify -check` myself too: **10,306 / 4,421** against 10,391 / 4,429. That is the
first landing in `client/SIZE.md` §1.1 that **costs nothing** — three attribute
constants, three `getAttribute` calls and their argument strings replaced by three
subscripts into a `split` the dispatch path was already performing.

**And it was driven against the thing that found it.** QA-1's own Chromium
reproduction, re-run: where QA-1 measured the composed clear **destroyed**, it now
arrives at **~1.7 ms** beside the debounced draft at **~157 ms**, and the
symmetric case delivers both events with server and browser agreeing. **Both of
§4.3.2's enlargements are answered on their own terms** — the destruction and the
symmetry — and the third, the key binding being late by an interval it never asked
for, is answered by the interval no longer being inherited at all.

**`Bind.Fields` moved per-binding against QA-1's §6 recommendation, and L9-1
overruled QA-1.** I record the overrule rather than the outcome, because the
overrule is the part with a reason: L9-1's §3 decides it **on QA-1's own
distinction** — what the *element* contributes (`fields(el, st)` reading the form,
the `name`, the `value`) is untouched and is the same for every binding on it,
whereas `Bind.Fields` is a value written **inside one `OnWith` call**, and two
`OnWith` calls on one element are two authors. **The counter-case is a frozen
requirement rather than a hypothetical**: `F-CTR-6`'s two keys, written the
ordinary way as one event name with a `dir` field, would under element scope send
the second binding the first's payload — *"failure 2 with a different symptom."*

**Failure 3 — FIXED at `b6bfe108`, and it is fixed in the place §4.3.2 said it was
worse for being in.** `examples/chat` implements Escape-to-clear as a real
`EventClear` reducer case (`chat.go:353`), declared in `Config.Events` (`:729`),
bound with `live.OnAll` (`view.templ:107`), driven **6/6 in Chromium against the
shipped example** rather than against a fixture. The `view.templ` comment §4.3.2
quoted now reads *"F-3 is now closed. Escape-to-clear is implemented, below …
the two do not interfere"*, and it names why the second clause is newer than the
first. `view_templ.go` carries the generated copy through `gen.sh`, as predicted.
**`FRICTION.md` F-3 finally carries its `— Closed.` heading — with the argument
that refused it kept above** (`:287`), which is the correct handling of a refusal
that was right when it was made and is right no longer.

**Failure 1 — DECIDED at `e751f6de`, and decided is not fixed.** L9-1's Part B is
the ruling this box has waited three revisions for, and it did not adopt the
convenient half.

- **Both refusal arguments standing on my own §5.6 were aimed at the wrong
  target, and L9-1 says so rather than borrowing them.** *"A chord belongs to the
  browser"* is **true of `Ctrl`, `Meta` and `Alt`** and false of `Shift+Enter` in a
  `<textarea>`, which no browser and no operating system claims and whose default —
  insert a line break — is bare `Enter`'s own. And **`KeyboardEvent.key` already
  folds `Shift` into every printable value**, which is why `F-CTR-6` passes today
  with no modifier surface at all, so the gap was never "modifiers": it is
  precisely *"distinguish `Shift` on a non-printable key"*. *"A library that
  `preventDefault`s on the application's behalf takes over `Ctrl+F`"* describes an
  **unconditional default**, and `client/runtime.js:679` already calls
  `preventDefault` for a declared submit and a declared anchor click — **an opt-in,
  per binding, defaulting off is the submit case and not the `Ctrl+F` case.**
  **Both arguments are kept and narrowed to where they are true**, and the narrow
  one does the work: §13 refuses `Ctrl`/`Meta`/`Alt` on it.
- **L9-1 also applied this project's own quiet-versus-loud criterion and reported
  that it does not decide the question**, which is the harder finding. `F-CHT-3`'s
  failure *is* loud. But loudness in the page-shell precedent was doing a specific
  job — making it safe to leave a thing to a caller who could repair it **inside
  the library's vocabulary** — and here the only repair is hand-written JavaScript,
  which is the exact thing FR-54's own text exists to prevent. *"Loud-but-
  unremediable is not the shape the criterion was built for."*
- **ACCEPTED: `Bind.NoModifiers bool` and `Bind.PreventDefault bool`** — components
  7 and 8 of the grammar, each rendered `"1"` when set and trimmed when not, so
  **every binding the tree renders today is byte-identical**. **+0 exported
  identifiers, +2 fields (51 → 53), +62 B minified / +34 B gzipped**, priced on a
  prototype **before** the shape was accepted, with the full modifier set priced
  beside it at +57 for the modifier half alone.
- **REFUSED: the full modifier set**, on three grounds — the measured price, the
  fact that it **cannot be two-valued** (a default of "no modifier held" breaks
  `F-CTR-6`, because `+` **is** `Shift`+`=`, and every three-valued spelling costs
  a sentinel, a pointer, or a `nil`-versus-empty-slice trap, which is *"a rule with
  one unpredictable exception"* — the same test L9-1 ratified for `Bind.Fields` and
  declined to abandon here), and **no consumer**. **The trigger is pre-registered
  and checkable in three limbs**: T-1, a second consumer anywhere in the frozen §2
  tables, `examples/**`, `docs/guide/**` or `bench/**`; T-2, a shape inside a named
  envelope (**≤ 4,475 B gzipped, ≤ 57 identifiers and ≤ 54 fields, zero output
  delta**, all three measured *before* the argument); T-3, **L9-1's own evidence
  turning out to be insufficient** — a real browser showing `NoModifiers` does not
  express `F-CHT-3`. **T-3 is the one to notice**: the reviewer pre-registered the
  case in which their own ruling loses, on the ground that *"a node harness builds
  the event object it then reads"*, which is `keybinding_test.go`'s own objection
  turned against its author.

**Why the box does not close, in L9-1's words rather than mine.** *"FR-54's box
closes when FR54-3, FR54-4 and FR54-6 are discharged and QA-1 grades them."* Of
those, **FR54-3 is a real regression this landing introduced**: an unbindable key
in `Bind.Keys` used to widen the filter and leave the debounce working, and now it
widens the filter **and shifts every later option one slot left**, so
`Bind{Keys:[":"], Debounce:150ms}` renders a **throttle**. Nothing rejects it,
nothing warns, and *"the author wrote something the godoc told them was impossible
and got a third behaviour neither of them named."* **FR54-4 is a gap rather than a
regression, and it is the one L9-1 found by mutating**: replacing
`r = st[specs[i]]` with `r = st[name]` leaves **156/156 client specs and 7/7
browser specs green** while reintroducing failure 2 for two bindings that share an
event name — the landing's **own motivating case**. Three documents state the
stronger property and nothing pins it. L9-1 wrote the missing spec and ran it both
ways. **FR54-6 is the Part B landing, against nine constraints, and it has no
artifact.**

**The nine constraints are pre-registered and one of them was found by building
the thing.** **C-9**: `dispatch` calls `preventDefault` at `client/runtime.js:679`
and tests `if (composing) return` at `:680` — **in that order** — and `Enter`
during an IME composition *commits the candidate*, so the new flag must sit
**after** the guard or it breaks every CJK composer, which is the population FR-26
exists for. **L9-1's prototype gets this wrong and L9-1 says so in the same
document that publishes the prototype.** That is the argument for pre-registration
stated as a fact rather than as a principle.

**Two more findings from the review that this box carries even though neither is a
condition on it.** L9-1 **drove a mixed-version failure**: the client runtime is
served from an **unfingerprinted** path with `Cache-Control: public,
max-age=31536000, immutable`, so an old cached runtime against new markup produces
`armed timers: 0` and silently dropped `Bind.Fields` — no error, no console
warning, no `4003`, because the version check is on the **wire protocol** and
`client/SIZE.md` §7 is a second contract it does not cover.
`docs/api-surface.md:586`'s *"there is no mixed-version window"* was false; **L9-1
appended the correction beneath the row rather than editing it**, and I verified
the correction is there (§2.8 row 8). And L9-1 **re-ran both of DEV-1's mutation
controls** rather than reading them, added four of their own on the runtime and
five on the emitter, and reports that **one survivor was not a gap** — deleting the
key filter leaves all 156 client specs green but turns **4 of 7 browser conformance
specs red**, so `test/internal/conformance/keybinding_test.go` is the check that
says NO. **A survivor chased to the check that catches it is worth more than a
survivor not chased**, and it is recorded here because an unchased one would read
as a gap.

**What closes this box now:** FR54-3, FR54-4 and FR54-6 discharged, and **QA-1's
grade of the batch.** **Owners: DEV-1** (`live/**`, `client/test/**`, FR54-3,
FR54-4, and the Part B code), **DEV-2** (FR54-1's `deploying.md` paragraph,
FR54-2's `client/SIZE.md:628`), **L9-1** (the nine constraints and the C-3
budget), **QA-1** (the grade). **PM-1's part of this box is now recording rather
than ruling**, which is the correct end state for a question FR-65 assigns to
somebody else.

#### 4.3.4 The box closes — MET on QA-1's grade, and the four conditions are the part to read

*(Added 2026-08-06, revision 6, at `9efb7e5b`. §4.3, §4.3.1, §4.3.2 and §4.3.3 are
left standing, and §4.3.3's `+62 B / +34 B` is corrected at §7.11 rather than
edited. **This subsection exists because the box that stayed open longest is the
one most likely to be summarised into a tick, and the summary would lose the four
conditions, the open FR54-7, and the fact that the reviewer's own constraints
failed three ways.**)*

**Verdict: MET. QA-1, `docs/qa/phase-4-grading.md` §11 at `eb4971c6`, PASS WITH
CONDITIONS Q-5…Q-8. This tick is QA-1's signature and not PM-1's reading**, which
is §5.2's rule and §5.11's table.

**What QA-1 did that a weaker grade would not have.** Three things, and each is
the reason a different clause can be trusted:

1. **Clause (c) was swept rather than inherited.** The clause asks that no
   document state as absent something the set expresses. Four agents had found
   four different instances across this phase, each after a declaration that the
   sweep was complete. QA-1 swept **fifteen phrasings** and read every hit outside
   the four record families: ten sites, **all ten corrected beneath themselves and
   none deleted**. The set is **empty**. **And QA-1 published the bound on their
   own sweep in the same breath** — *"a sentence that states a binding absent in
   words none of the fifteen matched would have survived me"* — which is the
   sentence §6 carries forward rather than the ten-site table.
2. **The discharges were re-run rather than accepted.** L9-1 had already
   discharged FR54-3, FR54-4 and FR54-6. QA-1 treated that as a *technical
   sign-off and not a correctness grade* and re-drove each: **M8** removes the
   `refuseUnbindable` call and turns **10 of 316** `live` specs red — L9-1's
   figure reproducing exactly; **M1** is the mutant that survived everything
   before FR54-4 and now turns **exactly one** spec red; **fourteen controls by
   QA-1's own count**, with baseline green before every mutation and after every
   restore.

   *(**The count is attributed rather than asserted, because it does not
   reconcile and I could not make it.** QA-1's §11.1 says *"fourteen controls,
   §11.6"*. The controls table is at **§11.7**, not §11.6, and it has **thirteen
   rows** — M1…M8, B1, B2, D1, D2, and one row `P1–P3` — covering **fifteen**
   mutations if the three price probes are counted separately. **Thirteen or
   fifteen; not fourteen, on either reading.** This changes no verdict: every
   individual control in the table names its mutation and its result, I checked
   the ones this report leans on, and the grade does not rest on how many there
   were. **It is recorded because this report has spent four revisions on the
   rule that a number carried in prose beside the table that contradicts it is a
   defect** — §7.10 correction 5, §7.11 correction 4 — **and having just applied
   that rule to my own §5.11 in this same revision, I am not going to quietly
   round somebody else's. Routed to QA-1; it is their file and their count.*)
3. **Two controls disconfirmed what they were built to confirm, and QA-1 reported
   both as findings rather than dropping them.** That is the behaviour this report
   has asked for in every revision since revision 3 and has usually had to
   praise in somebody else's absence.

**The four conditions, and why none of them holds the box.** QA-1's own test is
the right one and it is quoted rather than paraphrased: *"not one of them makes
any binding in the population inexpressible, uncomposable, undecided or
undocumented, which is the only test that could have held the box open."*

| # | What it says | Owner | Where it lives now |
|---|---|---|---|
| **Q-5** | `reviews/fr-54.md` §13's **leading refusal ground states a price that is no longer the price**: *"+57 gzipped bytes for the modifier half alone … fourteen times the `preventDefault` half"*. Measured at HEAD, the marginal cost of the full modifier set is **+10 gzipped bytes**. Grounds 2 and 3 are untouched and **the refusal stands** — but the number a future T-2 proposal will be argued against is off by roughly five | **L9-1** | §6, open |
| **Q-6** | `client/test/binding.test.mjs`'s AltGr spec is introduced by **a false sentence** — *"this is the spec that would go red if the runtime stopped reading one of the four."* M4, M5 and B2 show it stays **green**. A vacuous claim asserted as its opposite, inside C-6's own evidence file | **DEV-1** | §6, open |
| **Q-7** | **`docs/PRD.md` is stale on two of the three failures it grades** — the header Status row still says failure 2 is *"measured"* and failure 3's affordance *"stays absent"*, and FR-54's failure-3 block still ends *"Until that comment moves…"*. All of it landed at `2ab18690` and `b6bfe108` | **PM-1** | **DISCHARGED at PRD v1.5, in this pass** |
| **Q-8** | **`refuseUnbindable` and L9-1's §22.3 disagree at HEAD** about the empty `domEvent`, and both are documented as if they did not. The **tree is self-consistent and the ruling is the outlier**. Either land the fifth refusal or note in the godoc that a fifth is ruled and pending | **DEV-1** | §6, open, and it is FR54-7's other half |

**Q-5 is the one I want on this report's record rather than only in QA-1's**,
because it is the third instance of one defect class in one document: a number
measured once and never re-priced. `reviews/fr-54.md` §18.3 **confessed that exact
class** about C-3 — and the confession sits in the same document as, and above,
the sentence that leads the refusal and carries the same defect. **This report is
not clean of it either**: FR54-9 is the same class, in six places, in this file,
and §7.11 pays it.

**What the tick does not cover, taken from QA-1's own §11.8 rather than
softened.** G11 did not run in this gate and is separately graded. `ci.sh` is
cited and was not re-run by QA-1. **One browser and one version** — Chromium 151;
`F-CHT-3`, the `MouseEvent` clause and the modifier reads are unproven on Firefox,
Safari and WebKit, and `AltGr`'s `ctrlKey`+`altKey` reporting was taken from the
spec and from CDP rather than from four engines. §18.2's seventeen byte spellings
were not rebuilt. QA-1's T-2 probe is **a price probe and not a proposal** — no Go
surface, no specs, and it fails T-2's zero-output-delta limb. And **the
`PreventDefault`-outside-a-region behaviour is true and asserted nowhere**: the
guide states, truthfully, that such a binding suppresses the browser's default and
sends nothing, and **no spec holds it** — the same shape FR54-8 had before
`8363396c`. QA-1 recorded it rather than conditioning on it, and this report
carries it in §6 for the same reason.

**Owners after the tick: L9-1** (Q-5), **DEV-1** (Q-6, Q-8, FR54-7). **PM-1's part
of this box is finished** — the definition, the population ruling, the amendment
and the record — and the only thing left with PM-1's name on it is Q-7, which is
discharged in this pass.

### 4.4 FR-57 — MET on the behaviour, and the guard is a condition

**The box asks whether dev reload works. It works, and both halves the box names
were demonstrated rather than one.** In headless Chromium 151 against
`examples/counter` under `internal/cmd/gotth-live-dev`: a **templ** change to an
`<h1>` *outside every live region* — so no patch could have carried it — reloaded
the page in **1,810 ms**; a **Go** change reloaded in **2,715 ms**. The negative
control was taken in the same run: a rebuild that changed no bytes restarted the
process and reloaded **nothing**, with the page's marker and its live socket both
intact, and restoring the sources returned the build identity byte-identically to
its baseline. All of that is DEV-2's, recorded in `client/SIZE.md` §8 with the
transcripts in `guide/dev-reload.md`.

**The mechanism, because "works" should be checkable rather than atmospheric.** A
build identity polled over HTTP: `(*App).DevReloadScript` stamps the build that
rendered the document into `data-gotth-dev-build`, `client/dev-reload.js` polls
`<mount>/gotth-live-dev-build` and reloads when the answer differs from the
stamp. Third client artifact, **1,260 B gzipped against an 8,192 B ceiling this
project invented and says it invented** (SIZE.md §2.2 — FR-57 has no PRD byte
budget), gated at `ci.sh:669`. **The shipped runtime did not move a byte** and
that is checkable rather than claimed: 10,391 / 4,429 before and after, the file
absent from `7cff113a`'s diff, with `client/test/bundle.test.mjs` holding the
property going forward. **No new dependency** — I checked `go.mod` and `go.sum`
across the whole landing and the diff is empty. Surface 50 → **51** identifiers
and 50 → **51** fields.

**The qualification is a condition on Phase 4's exit and it is listed as one in
§6: the browser loop is not in CI.** Committed coverage is the client decision
table (`client/test/dev-reload.test.mjs`: same build → nothing, different build →
reload, restart into the same build → reconnect not reload, a 200 that is not a
build identity → not evidence, the four-step poll cadence, a refused connection
and a 502 both reading as "waiting") and the Go specs
(`live/devreload_test.go`). **The end-to-end run used a throwaway harness that is
not committed.** DEV-2 wrote the honest sentence themselves — it "is evidence for
one tree and not a gate" — and named where the standing version belongs:
`test/internal/conformance/`, which already has the CDP client. **Owner: DEV-2.**

**Why this ticks anyway, stated as a rule so it can be attacked.** §6's exit
sentence has two clauses: every box checked **and** the named gate owners sign
off at the exit review. A box is therefore checked on evidence PM-1 has verified;
the signature is a separate act. Checkpoint 3 already worked this way — its row 4
was ticked on PM-1's own reading of `examples/dashboard/main.go` while QA-2
explicitly declined the row as not theirs. **QA-1 has not signed FR-57 and this
tick does not claim they have.**

#### 4.4.1 Revision 5 — the qualification inside this tick is discharged, and the reason it took four revisions is the finding

*(2026-08-05, at `13a1ca1e`. §4.4 is left standing. **Its closing paragraph —
*"The end-to-end run used a throwaway harness that is not committed"* — is what
this section retires.**)*

**DISCHARGED.** `test/internal/conformance/dev_reload_test.go` makes FR-57's three
behaviours standing browser specs in the conformance package, reusing that
package's CDP client, `launchChrome`, `startCounter` and `browserOnly` rather than
building a second harness — which is precisely the home DEV-2 named for it in
§4.4 and never occupied. **Ginkgo v2 + Gomega, no mocks**, correctly: nothing here
mocks an interface, every spec drives a real browser against a real server.

**The three specs are the three things the box asks about, including the one that
is a control rather than a claim:** a templ change to the `<h1>` **outside every
live region**, so no patch could carry it, reloads the page; a Go change reloads
the page; and **the negative control** — `os.Chtimes` on both sources with no byte
changed, the watcher rebuilds and restarts, and **nothing reloads**.

**Two details make this a gate rather than a demonstration, and both are the
author's own.** The discriminator is a marker on `window`, and it is the only
honest one available: the runtime's reconnect-and-resync repaints every live
region after **any** restart, so *"the number moved"* cannot tell a reload from a
resync, while `window` does not survive a reload and does survive a resync. And
the negative control is guarded against **two** vacuities — the watcher's own
"restarted" line must have appeared (a control over a process that never restarted
asserts nothing) and `document.visibilityState` must be `"visible"` (the
dev-reload client stops polling while hidden, so a backgrounded tab would pass by
not watching). **A negative control that can pass by not looking is the defect
class this project has now caught six times**, and this one is written against it.

**Mutation control C is what makes the two positive specs mean something**:
`live.executableBuildID` frozen to a constant turns both reload specs red on
*"the marker set before the edit is still on window"* while the negative control
**stays green**, restarts 1 → 2. A control that goes red everywhere proves nothing;
this one goes red exactly where the property lives.

**The `examples/counter` copy is not fastidiousness and the commit says why.**
These specs edit source; this checkout is shared with other agents; an in-place
edit is one panic away from leaving `[TEMPL CHANGE]` in DEV-3's example. **That is
a live-infrastructure argument made by the agent whose specs would have been
simplest without it.**

**And the millisecond figures §4.4 quotes are demoted by their own author.**
1,208 ms and 807 ms this run, against 1,814 ms and 1,211 ms on the same host, and
the thrown-away runs' 1,810 ms and 2,715 ms. **Nothing asserts a millisecond**: the
host is shared, the numbers move by 2× between runs on it, and the specs gate on
the property — reloaded, or did not. **§4.4's two figures stay on the page as what
they were, one run's observation, and they are no longer what ticks this box.**

### 4.5 FR-44 — MET, ruled under the same rule and in the same breath

**This box landed a turn before the rest of this reconciliation and had never
been ruled on.** I am ruling it here rather than leaving it, and I am ruling it
with FR-57's rule rather than a second one, because applying two rules to two
boxes of identical shape is how a gate stops meaning anything.

Clause by clause. **Separate opt-in file:**
`live/clientjs/gotth-live-inspector.min.js`, mounted by one export, bundling its
own codec copy rather than reaching into the runtime — which is why the runtime
has no inspector-shaped seam for a later feature to widen. **Shows the causal
chain:** events, the state versions they moved the server to, and the patches
each produced, joined on `Origin.client_ref` and `patch_id` — folded in
`client/test/inspector.test.mjs` from frames the real codec encoded and decoded,
and seen end to end in chromium after one real click, showing
`CLIENT_EVENT event:counter.increment ← #2` with the morph and apply timings.
**≤40KB gzipped: 6,211 B of 40,960**, CI-gated in the same step as the other two
artifacts.

**Two qualifications.** (a) **`Config.Dev` gates serving the file (404) and
rendering its tag (zero bytes); it does not gate embedding.** The bytes are in
every binary. The godoc, the guide and the ledger all say so rather than implying
a build-tag exclusion nobody wrote, and the box's phrase is "does not load in
production builds", which is met in the browser's sense — the sense the box uses.
Recording it anyway, because "does not ship to production" is what a hurried
reader will take from the row. (b) **Same gap as FR-57's**: the browser run was a
throwaway harness, not committed, and it is the run that found the defect no node
spec could — `render()` calling `requestAnimationFrame` through
`(globalThis.rAF || setTimeout)(...)`, invoking it with no receiver, throwing
"Illegal invocation" from inside `mount()` and leaving a panel that was mounted,
styled and permanently empty while every node spec passed (`0c711b70`). **That is
the argument for the condition in §6, not against the tick**: the browser is
where this class of defect lives, and nothing currently re-enters it.

#### 4.5.1 Revision 5 — the condition is discharged, and the control that was supposed to prove it disproved two sentences of mine

*(2026-08-05, at `13a1ca1e`. §4.5 is left standing **and is wrong in two places
that this section corrects beneath it rather than over it.** The tick does not
move; what moves is the confidence anybody should have in the paragraph
underneath it.)*

**DISCHARGED.** `test/internal/conformance/inspector_test.go` makes FR-44's
browser half two standing specs. The panel is read out of its **open shadow
root** — mounted, shadow root attached, constructed stylesheet adopted, and, the
assertion that matters, **rows in the body**. The first spec drives an app whose
reducer moves the session's own state, so the patch really is `CLIENT_EVENT` with
a `client_ref` and the panel draws the join FR-44 names; it asserts the event
row's `→ event N · vN · 1 patch`, the patch row's `← #n`, and that **the row
number in the patch's `← #n` IS the event row's own number**. The second drives
`examples/counter`, which is what the guide points a reader at.

**Two mutation controls, and they fail in different places, which is what makes
them two.** **A** — `render()` made to schedule nothing, rebuilt through
`tools/minify` — turns both specs red at
`mounted=true shadow=true styled=true bodyChildren=0 rows=0`, **which is
`0c711b70`'s symptom exactly**. **B** — the `client_ref` join removed from
`patchRow` — leaves the panel painting and turns the first spec red *precisely* on
the join, showing `→ no patch yet` where the joined row belongs. **A control that
reproduces the historical symptom and a control that reproduces only the property
are worth more than two of either.**

> **CORRECTION 1, to §4.5's qualification (b) above, and it is a correction to
> this report's own text.** §4.5 says the throwaway harness found `render()`
> calling `requestAnimationFrame` through `(globalThis.rAF || setTimeout)(...)`,
> *"invoking it with no receiver, throwing 'Illegal invocation' from inside
> `mount()`"*. **DEV-2 tried to reproduce that as the obvious control for FR-44
> and it does not reproduce.** `0c711b70^`'s whole `client/inspector.js` was
> restored, rebuilt through `tools/minify`, and run — twice, once by hand and once
> from the restored file — **and the panel painted both times.** Measured directly
> in `dis-gotth-live-bench:latest`, Chromium 151.0.7922.71:
> `(globalThis.requestAnimationFrame || setTimeout)(fn, 16)` **does not throw**,
> nor does the same inside `<script type="module">`; `const g =
> document.querySelector; g("div")` **does**. The reason is specified rather than
> guessed: **`requestAnimationFrame` is a member of the `[Global]` `Window`
> interface and Web IDL defaults an undefined `this` on those to the global
> object**, while `querySelector` is on `Document`, which is not `[Global]`.
> **The empty panel was real. The mechanism named for it is not.** The same claim
> sits in `client/inspector.js:639` and `client/SIZE.md:696` — **neither is mine
> and both are routed** (§6) — and **the copy in §4.5 is mine, which is why the
> correction is here and beneath the sentence rather than in a table.** What
> §4.5's qualification (b) was *arguing* survives intact and is now demonstrated
> instead of asserted: the browser is where this class of defect lives, and
> mutation control **A** reproduces the symptom from the tree side without needing
> the diagnosis to be right.

> **CORRECTION 2, same section, same shape, and DEV-2 found this one too.** §4.5
> says the browser run showed **`CLIENT_EVENT event:counter.increment ← #2`**
> against `examples/counter`. **It cannot have.** The counter's reducer returns a
> `ChangeEffect`; its own transition changes no session state and is suppressed
> (`patch_id` 0); what the browser sees is the **store's broadcast**. Measured this
> turn, in the browser: `↓ patch 2 seq 2 · v2 EFFECT effect:counter.watch`,
> `client_ref` 0, with the click reachable only through
> `contributing_event_ids`. **The `CLIENT_EVENT` join is real and is exercised by
> the first spec's own app, which does produce it** — so the *capability* §4.5
> claims is true and the *transcript* it quotes is from something else.
> `client/SIZE.md:696` carries the identical line and is routed, not fixed.

**What these two corrections are actually about, and it is not the inspector.**
Both sentences entered this report from a commit body and a size ledger, and
**neither was ever re-derived.** §7.5 is titled *"Four numbers in this landing did
not reproduce, and the fourth is this report's own"*; §7.10 adds five more; this
is the sixth and seventh, and **both were found by an engineer running the control
rather than by a reader checking the claim.** The mechanism is the same one this
report keeps catching in other documents: a plausible cause written once, at the
moment of the fix, by the person least able to be surprised by it. **The remedy is
the one that just landed — a standing spec — and not a more careful sentence.**

### 4.6 The three examples — NOT MET on the clause nobody has graded

**The v0.7 sub-clause has landed.** `1b16f4a9` re-measured the dashboard's resync
cost at the shipping tree and rewrote the README's method paragraph to the
request the harness now sends. I checked that it has not gone stale again:
`examples/` is byte-identical from `1b16f4a9` to HEAD except `examples/counter`,
which changed only to render FR-57's tag.

**What is unmet is the box's main clause, which is broader than the resync
sentence:** *all three examples polished, documented, and green in CI end to
end*. Green is real — `ci.sh:295`, `:302` and `:322` build, vet and race-test
each example as its own module. **"Polished and documented" has been graded by
nobody**, and FR-60…FR-63's gate is QA-1. **Owner: DEV-3 to present, QA-1 to
grade.**

**This box does not tick the Phase 3 resync box and I want that said plainly.**
The Phase 3 criterion appears to meet all three conditions checkpoint-3 §5.3 set,
and the PR body already says the remedy landed. But ticking a closed phase's box
is a gate act on *that* phase, in a record that says so, and a Phase 4 sweep is
not that record. It is carried in §6 with DEV-3's and PM-1's names.

#### 4.6.1 Revision 3 — MET, and the grade was a FAIL first, which is why it is worth something

*(2026-08-05, at `b04ba138`.)*

**QA-1 graded this box without waiting for the presentation §4.6 said was owed**,
recorded that they had done so, and **stated the standard before applying it** so
DEV-3 could contest the standard rather than only the verdict. That is the right
order and it is why the FAIL was actionable.

**The FAIL was six places where the tree contradicted itself after the
`livetest.Client` migration** — the worst of them a README telling a reader the
library's supported testing API did not exist, beside a file using it twenty-five
times. DEV-3 remediated across `da827962`, `64d7ddfb` and `986ef434`, and QA-1
re-graded **PASS** at `368132f6`.

**Three things make this a stronger tick than a re-grade usually is.**

1. **The stale number is now held by a check rather than by care.** DEV-3 put the
   three spec counts under a `ReportAfterSuite` node reading each README.
   **QA-1 audited the check instead of accepting it** — five controls on a copy,
   none of them run by DEV-3 — and the one that matters is **vacuity**: rewrite
   the README's claim into words so no `N specs` string remains, and the guard
   does not quietly pass, it **fails and says which of the two repairs to make**.
   A guard that cannot fail is the defect class this project has caught six
   times; this one was checked for it explicitly.
2. **QA-1 recorded a miss of their own inside the PASS.** A seventh instance of
   the same false sentence, in `examples/dashboard/wire.go`'s header — a file
   their §4.2 had read and graded accurate without reading its header.
3. **DEV-3 declined one of the five prescriptions and QA-1 ruled for them.**
   F-7 told DEV-3 to mark a FRICTION item "Closed."; they graded it *"closed for
   the specs, open for the measurement"* and attached the evidence. QA-1 checked
   both grounds against the tree and adjudicated: *"my finding stands; my
   prescription was wrong … had DEV-3 done what I asked, the file would state
   something false"* — **which is the defect class the box failed for**.

**One item found after the grade, and it is routed rather than swallowed.** While
gathering FR-54's evidence I found an eighth instance of the same class:
`examples/chat/FRICTION.md` F-3 and `view.templ:64` describe Escape-to-clear as
inexpressible, which `591c275a` made false. **I am not reversing a grade the gate
owner made** — that is not PM-1's act — so it is routed to **DEV-3** to fix and
to **QA-1** to say whether it disturbs their PASS, and carried as a condition in
§6. §5.6 failure 3 and §7.9.

#### 4.6.2 Revision 5 — the Phase 3 resync box is held, and this section's refusal to tick it in passing is what made that possible

*(2026-08-05, at `f0690a2c`. §4.6 and §4.6.1 are left standing. **§4.6's
paragraph *"This box does not tick the Phase 3 resync box and I want that said
plainly"* is the sentence this section answers, and answering it is the only thing
in revision 5 that is a PM-1 act rather than a PM-1 record.**)*

**The Phase 3 resync box is MET and Phase 3 EXITS at seventeen of seventeen.**
The gate act is [`docs/gates/checkpoint-3.md`](checkpoint-3.md) **§12**, held at
`713a3192`, applied to the PRD at v1.4 (`f0690a2c`), with
[`docs/pm/checkpoint-3-closure.md`](../pm/checkpoint-3-closure.md) and
`docs/README.md` moved with it. **It is a Phase 3 act recorded in a Phase 3
record**, which is what §4.6 said it had to be and why this section is a
cross-reference rather than a verdict.

**How it was held, because the *how* is the only part that distinguishes this from
what §4.6 refused to do.** The measurement was **re-run six times** —
`go run . -resync-cost 200` in `dis-gotth-live:latest` — and the last three on a
**pristine `git archive HEAD` export**, because partway through the act other
agents' uncommitted files appeared in this shared worktree and an export removes
the question rather than arguing it. The comparison against the published fence
was made by **`diff -u` against the program's stdout**, not by eye. **Every byte
figure the README publishes is identical on all six runs, 101 commits after they
were taken** — frame p50 2,378 B, markup p50 2,231 B, protocol overhead 147 B, the
three per-region figures, and the library's own `gotthlive_resync_bytes` mean and
max. The method paragraph was checked against **`examples/dashboard/resync.go` at
HEAD** rather than against the commit body, clause by clause, including that the
server-side clamp it rests on is in force at `internal/session/resync.go:119`–
`:134`.

**And the sixth run is the one the act did not plan to do.** `2ab18690` — FR-54
failure 2's repair, which **re-encodes rendered markup inside live regions**, the
one class of change that could move these bytes — landed *during* the act. The
measurement was re-run against an export of the new tree: **identical again**, and
the four suites green there too. **The act is held at `713a3192` and re-confirmed
at `2ab18690`, and the two are not collapsed into one claim**: a gate act is held
at a tree, and a re-confirmation is a second observation rather than a second
gate.

**One published number did not reproduce and it has its own section rather than a
footnote.** The README's `max 579µs` is the **low outlier of eight runs this host
has produced**; six PM-1 runs gave maxima of 1.399 ms, 1.79 ms, 1.511 ms, 2.623
ms, 1.15 ms and 568 µs, and DEV-3's own second run reported 1.771 ms. **This does
not fail the box and the reason was written before anybody re-ran it**: the
criterion asks for bytes *and* latency, the README publishes the latency as a
distribution with its host, its load average and its container count stated, and
it tells the reader to quote the bytes and read the latency as a distribution.
**A document that predicts its own irreproducibility and is then found
irreproducible in exactly the manner it predicted is behaving correctly.** What
would have failed the box is a byte figure that moved, and none did. §12.3 also
states the thing a reader could still get wrong — *"a reader who quotes `max
579µs` as the resync latency of this library is quoting the fastest of eight
runs"* — which is the sentence that makes the disclosure a disclosure.

**Why this is in a Phase 4 report at all.** §4.6 and §6 both carried the box as a
**Phase 3** item with two owners, DEV-3 (done) and PM-1 (the gate act), and both
said explicitly that Phase 4 would not tick it in passing. **It was not ticked in
passing. It was held.** The §6 row closes here, and §4.6's paragraph stands
unamended above this one because it was right.

### 4.7 G11 — NOT MET, and this is the shape that has fooled us twice

`docs/README.md` tells a reader that `git clone && go run .` works in any of the
three examples with no node, npm, protoc or generator, because the generated code
is committed. The claim is plausible and the mechanism that would make it true
exists — FR-7's byte-reproducibility gate, the committed `*_templ.go` and
`*.pb.go`.

**It has never been run as the criterion states it.** `grep -n "G11" ci.sh`
returns nothing; the three example steps run inside an image chosen for the
toolchain it *has*, not for the tools it lacks; and no artifact in the tree
records the invocation on a machine without those four tools. **A criterion whose
evidence is a sentence in a README is the exact shape this repository has now
caught six times** — a skip that never skipped, a suite green because it never
ran, a table that asserted the bug. **Owner: QA-1**, whose gate G11 is. The
cheapest honest form is one clean export, one image without node, three
`go run` invocations, and a recorded result.

#### 4.7.1 Revision 2 — the run happened, the criterion's sentence was impossible, and the box still does not tick

*(2026-08-05, at `134e69c5`. All three of revision 1's grounds above are now
false, and I am leaving them standing because the fourth paragraph of this
section is about what a criterion is worth when its evidence is a document, and
that argument is only checkable against the state it was written in.)*

**DEV-2 ran the thing §4.7 asked for and ran it harder than §4.7 asked.** The
prescription was *"one clean export, one image without node, three `go run`
invocations, and a recorded result"*. What landed is a **real `git clone`** —
over `file://`, so git takes the pack protocol a stranger gets rather than the
local hardlink shortcut, sharing no objects with this checkout — into **stock
`golang:1.25-bookworm`**, digest `golang@sha256:ea341baa…9d58`, Go 1.25.12.
An export would have been weaker: a copy carries the three built example
binaries, `bench/node_modules`, and whatever a previous run generated, any one
of which could make a failing tree pass.

**Four things about that run are worth more than its verdict, and each is a
thing this project has previously got wrong.**

1. **The precondition is asserted and fatal, not assumed.** `node`, `npm`,
   `protoc` and `refinec` are each proved absent inside the image before
   anything else runs, and so are `templ`, `buf` and `protoc-gen-go`, which G11
   does not name — templ because `docs/README.md`'s sentence says *"no
   generator"* and the examples' `view_templ.go` files are committed. A run in
   which any is present fails immediately. **A gate that does not verify its own
   precondition is asserting the thing it was written to measure**, and §3.2 of
   the artifact prints the measurement that makes the point: `dis-gotth-live:latest`
   ships **protoc 35.1 and templ v0.3.1020**, so `ci.sh`'s three existing example
   steps could never have answered this criterion at any relabelling.
2. **The clone is asserted pristine before *and after*.** The second assertion
   is the one that matters and it is the one nobody asked for: it says `go run`
   generated nothing, updated no `go.sum` and wrote nothing into the tree — that
   "clone and run" is the whole procedure rather than the procedure plus a step
   nobody wrote down.
3. **The runtime's URL is read out of the served page rather than hardcoded.**
   The three examples mount at `/live`, `/chat/live` and `/dashboard/live`; a
   gate with `/live` written into it reported two of three as serving no
   runtime, and that was this gate's own first run. Reading the URL from the
   page asserts the stronger property anyway — **the URL a browser would request
   is the URL that answers** — and all three served **10,391 bytes**, which is
   `client/SIZE.md`'s minified figure, out of a container with no node in it.
4. **The negative control was taken.** `--deadline 1` gives each example one
   second where counter needs seven; the runner exits 1 and the `ci.sh` step
   goes red. A check that cannot fail is indistinguishable from one that passes,
   and this one can fail.

**The other half of the verdict is that G11's own sentence cannot be satisfied by
any work on this tree, and DEV-2 refused to average the two halves into one
word.** `go run ./examples/<name>` fails from the repository root with `go:
cannot find main module` and from `candace/pkg/gotth/` with `main module … does not
contain package …/examples/counter`. **That is not a bug and fixing it in the
tree would be wrong:** each example is a separate module with its own `go.mod`
and its own `replace … => ../..`, and each of those headers argues for the
separation — an example must not be able to put a dependency into a consumer's
module graph, it has to *measure* like a consumer for `dependencies.md` §5, and
`internal/arch`'s two-package cap has to stay a statement about the library
rather than one with exceptions in it. **The wording is amended in PRD v0.9
row 1**, and the ordering is the defence: the property was measured green before
the sentence was touched, and the sentence was unsatisfiable before the property
was measured, so the argument for changing it is invariant to the outcome in the
strongest available sense — it is a fact about `go`'s module resolution and about
three files that predate the run.

**Why the box does not tick.** **G11's gate is QA-1 and QA-1 has not graded the
artifact.** PM-1 can check — and did — that a run happened, that its preconditions
were asserted, that its negative control fires, and that `grep -n "G11" ci.sh`
returns 17 lines with a step at `ci.sh:876`. What PM-1 cannot supply is the
judgement that this run is the evidence G11 wanted, because the requirement gives
that judgement to somebody else. **Owner: QA-1, to grade
[`docs/qa/g11-clean-clone.md`](../qa/g11-clean-clone.md).**

**And the standing gate is still missing, which is a condition on Phase 5's G11
box rather than on Phase 4's exit.** No CI job runs this: the library job runs
`ci.sh` inside `docker run` with no docker socket, so the step announces a skip
there exactly as it does under `dis run`. DEV-2 corrected `ci.sh`'s header, which
had said the workflow skips nothing, and supplied the four-line workflow step
that fixes it. **I am placing that as a Phase 5 condition and I want the reason
on the record rather than assumed**: the box asks whether the property holds and
it does, measured; blocking a phase's exit on a file (`.github/workflows/`) that
nobody on this team owns is how a phase gets blocked on a party who was never
asked. But **a release box may not close on a dated run of a check nothing
re-runs**, so it binds at the Phase 5 box that asks the same question at the tag.
**Owner: the workflow's owner**, with DEV-2's exact YAML in §7 F-3 of the
artifact.

#### 4.7.2 Revision 3 — MET, on a grade that checked the runner's own claims rather than reading them

*(2026-08-05, at `b04ba138`.)*

**PASS, no conditions** — `docs/qa/phase-4-grading.md` §3, the only one of QA-1's
four grades that carried no condition at any point. **What §4.7.1 could not
supply was the judgement that DEV-2's run is the evidence G11 wanted. QA-1
supplied it by not trusting the run.**

- They **re-ran `tools/g11/run.sh` themselves at HEAD** rather than grading the
  recorded artifact.
- They **checked the image for `node` directly**, rather than through the
  runner's own printed precondition — which is the difference between verifying a
  gate and reading its output.
- They **checked the clone for a `.git/objects/info/alternates` file and for
  untracked-or-ignored files**, rather than trusting the runner's pristine
  assertions. A clone sharing an object store with this checkout would have made
  the whole run meaningless in a way nothing else would have caught.
- They **built a fourth negative control that was nobody's idea but theirs**: an
  image identical to the valid one except for a `node` shim on `PATH`. The runner
  refuses it. DEV-2's `--deadline 1` control proves the runner can fail on a slow
  app; this one proves it can fail on **the precondition it exists to assert**,
  which is the stronger property and the one nobody had tested.

**Carried unchanged and still not a Phase-4 condition: no CI job runs this.**
§4.7.1 argued that split before the grade existed and the grade does not disturb
it. It binds at Phase 5's G11 box.

### 4.8 FR-59 — NOT MET, seven subjects of nine

Served: quickstart, architecture (`rfc/001-architecture.md`), protocol reference
(`protocol.md`), observability setup, HTMX interop, forms and validation
(`guide/events-and-forms.md`, FR-55's owed pattern), and the honest "when not to
use this" page, which now exists and is one of the two things this landing's
predecessor added.

**Not served: deployment, which has no page at all.** And **security
configuration has no page of its own** — it is served today by
`docs/quickstart.md` §2's "The security defaults, and the four ways out" and by
`guide/error-handling.md`. That may be sufficient; FR-59 lists it as a subject of
the set and nobody has ruled that the current placement satisfies it. **Owner:
DEV-3 for the deployment page; PM-1 to rule on the security question; QA-1
gates.**

#### 4.8.1 Revision 2 — nine of nine by subject count, and the ninth is the one DEV-3 flagged against themselves

*(2026-08-05, at `134e69c5`.)*

**Both of revision 1's gaps are closed.** `guide/deploying.md` landed at
`d7353b5e` and `guide/security.md` at `5238c85a`, each with compiling samples
under `docs/guide/_samples/{deploying,security}/` held by the same drift suite
that holds every other page, and `docs/README.md` gained a row for both at
`f34ef2ca`. **My open question about the security material is answered by the
landing rather than by a ruling** — it has a page now, so there is nothing left
to decide, and saying so is cheaper for the next reader than leaving a question
in the open list that a page already closed.

**The box does not tick, for two reasons that are independently sufficient.**

**First: FR-59's gate is QA-1 and QA-1 has graded nothing.** Same sentence as
§4.7.1's and §4.12.1's.

**Second, and this one is mine to state: the architecture subject is not
discharged, and DEV-3 raised it against their own delivery.** DEV-3's own grading
is that architecture is the weakest of the nine and that they did not fix it. It
is served today by `rfc/001-architecture.md`, which `docs/README.md` files under
a heading whose preamble reads, verbatim: *"The design record. None of it is
needed to build an application, and all of it argues rather than instructs."*
**A subject of the docs set discharged by a document the set's own index says is
not needed and does not instruct is discharged in name.** There is no
reader-facing page explaining the runtime model to somebody building an
application, and FR-59 sits in the phase whose gate is a person building an
application from the documentation alone.

**This is FR-54's failure one requirement over, and §4.3 already named it.**
"Complete" went undefined and the box became unticksable rather than unmet.
Letting "architecture" mean whatever the person ticking needs it to mean would
produce the same object one row down. **The ruling is PRD §9 v0.9 row 3 and
§5.4 here.**

**Two things would discharge it and either is sufficient**, which is the shape a
ruling has to have to be worth making: a reader-facing page explaining the
runtime model, **or** the architecture RFC moving out of "For the curious" and
into the guide index under a preamble that does not disclaim it. The second is
nearly free and it is also the honest test — **if it cannot be moved without
keeping the disclaimer, it was not instruction.** **Owner: DEV-3 to land
whichever; PM-1 has ruled; QA-1 gates.**

**Carried, not a condition: the deployment page's proxy section is derived, not
observed, and it says so itself.** ADR-001 criterion **X6** — *"upgrade succeeds
through this repo's Caddy edge with no Caddyfile change"* — names a Phase 2
integration test as its evidence and **no artifact in this tree records that test
running**. `4a40ed48` put that limit on the page in DEV-3's own words, before the
reverse-proxy section rather than after it: *"read this as 'what the transport
requires', not as 'what has been observed through nginx, Caddy, an ALB or an
ingress controller'."* **That is the right response to an unrun criterion and it
is not a substitute for running it.** **Owner: QA-1 or QA-2 to run X6 before
Phase 5 quotes the deployment guidance as observed.**

**Also carried: the page's sizing figure is 81 commits old.** It quotes
`docs/bench/g2-baseline.md` §9.10.5 — **45,769 B at N = 1000, observability on,
TLS outside, pooled over five runs**, measured at `d66e4953` — with §9.10.9
attached, which is the section saying the 0.68 % margin under the gate is smaller
than the cell's own 5.5 % spread and that **two of the five runs were
individually over**. The page states the staleness in the right way, as *"the
tree has moved since and no re-measurement has been published at HEAD"*, rather
than by quoting a commit count that would itself go stale. **PM-1 measured the
distance and it is 81, not the 68 DEV-3 reported at handoff** —
`git rev-list --count d66e4953..HEAD`; 76 at DEV-3's own last commit, so the
figure does not reproduce at any tree in this landing. §7.5. **Owner: QA-2 at
Phase 5**, where G2 is enforced; the page is not the place that number gets
re-measured.

#### 4.8.2 Revision 3 — MET, and my own §5.4 ruling was answered rather than worked around

*(2026-08-05, at `b04ba138`.)*

**PASS with one condition, and the condition is closed** —
`docs/qa/phase-4-grading.md` §9.2, graded separately and later than the other
three because **QA-1 refused to grade a docs set while its disputed subject was
being written**: `docs/guide/architecture.md` arrived mid-pass, and a verdict on
a tree that no longer exists by the time it is read is unattributable to any
commit. That refusal is the discipline §4.7.1 credits DEV-2 with, applied by QA-1
to their own schedule.

**§5.4's ruling was discharged by the harder of the two alternatives.** DEV-3
wrote the reader-facing page rather than re-filing the RFC, and **argued the
choice on my own honest test rather than dodging it**: the RFC is **1,805 lines**
(QA-1 checked with `wc -l`), its status line reads *"Draft for L9-1 approval
(Phase 0 gate)"* and it is dated before the library existed (QA-1 checked the
header table), and its strongest sections argue paths not taken — so it cannot be
filed as instruction without a preamble saying most of it is not needed, **which
is the disclaimer, which is the test failing.** §5.4 said *"if it cannot be moved
into the guide without keeping the disclaimer, it was not instruction."* That is
the answer the test was built to extract, and DEV-3 extracted it against their
own cheaper option.

**QA-1 then did the thing that makes this grade worth quoting: they drove the
page's least checkable sentence, with a control.** The page claims a slow
`Authorize` self-closes the session, because `Authorize` runs on the connection's
read pump rather than on the actor goroutine. A 9-second `Authorize` on tight
limits produced `StatusCode(4010)` *"no frame from the client within the
heartbeat timeout"* — **and the control is what makes it mean anything**: the same
limits and the same traffic without the stall keep the session open for 8 s, so
it is the stall and not the configuration. **No spec in the tree asserts that
consequence**; QA-1 wrote the probe themselves because DEV-3's own spec asserted
the weaker goroutine-identity claim. **Writing the disputed page also found and
fixed a false claim in a neighbouring page** — `lifecycle-hooks.md` had
`Authorize` on the actor goroutine — which is the strongest available evidence
that the page was not decorative.

**The factual surface was checked mechanically rather than impressionistically:**
every default in the page's tables against `DefaultLimits()`, every close code
against `closecode.go`, and **42 of the 43 `live.`/`livetest.` symbols the whole
docs set names** resolve in the shipping source — the 43rd being one the set
itself labels unimplemented on the page that names it.

**The condition — F-10 — is closed.** `docs/README.md:24` still indexed the
quickstart as *"27 lines of Go"* after `fde707f0` made it 20; DEV-3 fixed it at
`b04ba138` and **PM-1 verified the row and the page now agree** (§2.5 row 4).
QA-1's reason for making it a condition rather than a second FAIL is the
distinction that keeps both boxes on one standard: *"a set that has stopped
tracking its code, versus a row that lags a commit."*

**Carried unchanged: X6 has still never been run**, and the deployment page still
says so itself.

### 4.9 FR-77's documentation half — MET on all four clauses

**This box's whole content is a list of things two pages must say, which makes it
a box PM-1 can check by reading — so I read it rather than taking the commit
message.** All four, each anchored:

1. *States what a duplicate frame does in FR-77(a)'s own words.*
   `guide/effects-and-server-push.md:308` **quotes** it as a blockquote rather
   than paraphrasing.
2. *Distinguishes the two ways an application meets a double execution.*
   `:334`, "The two ways your application meets a double execution", both paths
   in one table — including the row that says at-most-once **does not** help
   against the second, which is the row that stops the section reassuring
   somebody.
3. *A worked idempotency key on an effect that moves money or sends a message.*
   `:362` onward: a charge, with the in-session `Status` guard included and then
   **explicitly demoted as useless across two tabs and across a reconnect**. That
   demotion is what makes the example teach instead of comfort.
4. *The "when not to use this" page names the bound in FR-77(c).*
   `guide/when-not-to-use-this.md:27` quotes FR-77(c) rather than paraphrasing.

The v0.7 measurement this box recorded — one sentence, the second path only, no
worked example, no page — is superseded. QA-1's signature on it is owed at the
exit review with every other box's.

### 4.10 FR-66 — MET against a requirement I narrowed in the same landing

See **§5.1**, which is the ruling. The box ticks; the narrowing is PRD §9 v0.8
row 2, in §9 rather than inside the tick, because that is where a scope change
has to live to be found.

**What exists.** `tools/doccheck`: a `go/ast` + `go/doc` walk enforcing four
rules, wired at `ci.sh:660`, with 24 of its own tests run by `ci.sh:617` —
including `TestRefusesATreeItWouldPassVacuously`, which is the falsifier that
matters. A doc-coverage tool's available silent failure is finding nothing to
check; DEV-1 built the test for exactly that and made the tool exit 2 on an empty
walk. **142 undocumented exported symbols were found and fixed** in the published
module, per-package counts in `9cce6829`'s body, and DEV-1 published the one
claim they wrote and then discovered was wrong (`wsx.Options`'s instrumentation
triple is not non-nil by construction) rather than shipping it. **Two package
overviews were added as runnable `Example()` functions**, which turns FR-66's
"runnable overview" from a word into the only construction `go test` executes.

### 4.11 FR-68 — MET, with no carve-out, which is worth saying beside FR-66's

doccheck's rule 4 — every `Example*` function **anywhere in the tree, in every
module**, carries an `// Output:` comment — is enforced everywhere. That is the
half of FR-68 a static check can carry, and it is the half that matters: an
example without that comment compiles, is never run, and can assert a behaviour
the library lost a year ago with no suite saying so. The running half is the
`go test` steps themselves, which is why the CI label names both.

**2 → 6 `Example*` functions, all six with `// Output:`** — counted by PM-1 at
HEAD: five in `live/example_test.go`, one in `live/livetest/example_test.go`.
`8a06cb04`'s two are aimed at the two places `live`'s own doc comments spend a
paragraph on a default a reader will get wrong, and its body records the Output
comment earning its keep on the spot: DEV-1 wrote
`keydown[Enter]:composer.send` and the real encoding is
`keydown:composer.send:Enter`.

**Residual, DEV-1's own and carried rather than hidden:** `live/livetest`'s
harness half **cannot** carry godoc examples at all, because every entry point
takes a `testing.TB` and an `Example` function has no way to obtain one. So that
module's single example covers what can be covered and the rest of its surface is
documented by doc comment only. That is a property of the API, not a gap in the
gate, and it is not fixable by writing more examples.

### 4.12 FR-58 — NOT MET, not started

`guide/error-handling.md` exists and is a good reader-facing page.
`9cce6829` wrote doc comments on error types that say which reader each string
is for — including that `DenyError.Error` is operator-facing while the client is
told something generic, a disclosure fact that had been written nowhere.
**Neither is the audit.** FR-58 requires that **every** library-produced error
name the session, the causal ID where one exists, and the actionable next step,
and nobody has enumerated the error set and checked it against those three.
**Owner: DEV-1 to enumerate, QA-1 to grade.**

#### 4.12.1 Revision 2 — the audit exists, it changed 29 things, and the half of its owner line that says "QA-1 to grade" has not happened

*(2026-08-05, at `134e69c5`.)*

**DEV-1 wrote it, and the first thing worth saying is that it is an audit rather
than a description.** §4.12 above said what would not count, and DEV-1 quoted
that paragraph back into the document's own §1 and answered it: *"A document that
grades and changes nothing is not an audit."* **25 of the 117 sites failed a
clause that applied to them, and all 25 were rewritten in code before the file
was written**, so §3's "message as it reads today" column describes the tree you
have rather than the one that was walked.

**The numbers, whose they are, and which two of them PM-1 checked.**

| | | Whose |
|---|---|---|
| Error-authoring sites enumerated | **117**, across **8** packages of the published module | DEV-1's walk, at `2ab0cd57`. **PM-1 summed `internal/arch/errors_test.go`'s census map by hand: 3+4+40+8+8+10+37+7 = 117** |
| Graded as failing an applicable clause | **25** | DEV-1's grading. **PM-1 counted the "**was …**" rows: 25** |
| Fixed in code | **25**, all of them, at `ba5ce082` (27 changes) and `4d28146f` (2) | DEV-1 |
| Further defects the same walk found, not authoring sites | **4** — three log records dropping a causal identifier the call site was already holding, one error path that logged nothing | DEV-1 |
| Total changes | **29** | DEV-1 |

**The headline said 22 and the tables said 25 until `134e69c5`, and DEV-1 landed
the correction themselves.** That is the third self-correction in this landing and
it is the pattern §8 of revision 1 said this project should be measured by: the
tables win, and the arithmetic is now written out so the next reader can check it
rather than trust it.

**The enumeration is not a grep, which is what makes the number auditable.** It
is a walk of the module's syntax trees for the places **a human wrote a sentence
that becomes an error message** — a function returning another function's error
verbatim authors nothing and is not a site — and the rule is stated in the file
so the walk can be re-run rather than believed.

**Three regression guards, and the split between them is DEV-1's argument rather
than three files that happened to land.**

- `live/fr58_test.go` (Ginkgo) drives the errors that reach **application code**
  as values, through the paths that build them. Where a session is named it
  asserts it is the *right* session — the identifier the client on the other end
  holds, not merely a 32-character hex run — and **the `ConfigError` rows assert
  the opposite way, that no session is named**, because none exists at `New`.
  That is what makes the audit's per-row "inapplicable, and here is why" a
  falsifiable claim instead of a waiver.
- `internal/session/emission_internal_test.go` covers the two mailbox sentinels
  at the seam. It is a **table-driven standard-library test rather than Ginkgo**,
  and DEV-1 declared it as such in the commit body with the reason, which is what
  the house rule asks for.
- `internal/arch/errors_test.go` **counts rather than grades**, and DEV-1 says
  why in the file: "the actionable next step" is a judgement, every automatic
  proxy for it is a rule a bad message passes and a good one fails, and **a gate
  that is green because it is weak is worse than no gate**. What rots about an
  audit is its coverage, so the walk asserts the per-package site counts and
  asserts the eight out-of-scope packages as an **exact set with a reason each** —
  a new directory under `internal/` cannot quietly avoid being graded.

**No exported symbol changed**, so `docs/api-surface.md` needed no row.
**PM-1 checked that as an empty diff on the ledger file across the landing, which
is a weaker statement than DEV-1's**, and **no `apisurface` run is quoted
anywhere in this landing** — this report does not claim one. §7.5.

**Why the box does not tick.** DEV-1's own second paragraph: *"QA-1's grade is
the one that ticks the box, and this document is the artifact it should be taken
against rather than a substitute for it."* I am not going to be the second person
to disagree with them about whose grade it is. **Owner: QA-1.**

**Two things carried, neither a condition on Phase 4's exit**, both DEV-1's own:
§6 of the audit is their list of where the document is weakest — **16
`Error`-level log records graded and 18 at lower levels not tabulated**, and
roughly a quarter of the PASS verdicts resting on composition rather than on the
message's own text — and §7.2 asks for an `internal/arch` assertion that the
wrapping stays in place, which is a new architectural claim rather than an audit
finding. **Owner: DEV-1**, at Phase 5.

**One finding routed out of the audit to PM-1 is ruled in §5.5**: an application
cannot `errors.Is` `ErrSessionSaturated` or `ErrSessionClosing`, because both
live under `internal/`.

#### 4.12.2 Revision 3 — MET, and the grade found a defect five audit rows had asserted away

*(2026-08-05, at `b04ba138`.)*

**PASS, condition discharged** — `docs/qa/phase-4-grading.md` §2 and §9.3.

**QA-1 did not grade this by reading it, and the method is the reusable part.**
They set four standards — the enumeration reproduces for somebody who did not
write it, the messages it says ship are the messages that ship, the guards it
puts up can actually go red, and its own account of its weakness is checkable —
and then **re-implemented §2.1's enumeration rule from the document's prose** in
their own AST program rather than re-running DEV-1's committed walk. That is the
distinction between checking a number and checking the *rule* that produced it,
and it is why the result is worth quoting: **117, package for package, at the
graded tree**, and **119 at HEAD**, with both moved packages accounted for by the
audit's own revision 3.

**The one defect was found by driving, and it is the shape §4.12.1 could not have
caught by reading.** `livetest.Client.NextErr` — the **exported** accessor — was
returning the five §3.4 messages **bare**, without the session prefix `where()`
adds on the `tb.Fatalf` path, so five audit rows said the returned value names
the session and the value a caller holds did not. **DEV-1 fixed it at `131cb3cb`
and did both halves of the disjunction QA-1 offered**, wrapping the value and
correcting the five rows to say the clause failed until revision 3. **QA-1
discharged the condition by removing the fix on a copy and watching all three
specs go red**, then printing the session id out of the value a caller holds.

**And the guard has now fired twice in production on real edits**, which QA-1
notes is worth more than any mutation they could write for it — the census went
red on the FR-53 shrink and again on the `livetest` wrap, each time naming the
package that moved.

### 4.13 FR-20 — NOT MET, and the file does not exist

`ls gotth-live/docs/exceptions.md` → no such file. FR-20 has said since Phase 1
that any deviation from FR-14/16/18 MUST be recorded there with a reason, a blast
radius and an L9-1 sign-off line, and that **unlisted deviations are merge
blockers**.

**Two readings, and they are not equally comfortable.** Either there has never
been a deviation — in which case the file should exist and say so, because "no
exceptions" is a claim somebody has to sign, and an absent file makes it
unfalsifiable — or there has been one and it went unrecorded, which the
requirement itself calls a merge blocker. **Nothing in the tree distinguishes the
two, and that is the finding.** This is the same class as G11 in §4.7 and as
checkpoint 3's own §10.2: a requirement enforced by nobody, discovered late.
**Owner: DEV-1 to draft from the tree, L9-1 to sign.**

#### 4.13.1 Revision 2 — the file exists, the walk picked the unwelcome reading, and the box is further from ticking than it looks

*(2026-08-05, at `134e69c5`.)*

**§4.13's two readings are distinguished now, and the second one is true.**
DEV-1's walk found **two deviations, and neither had been recorded**. Both are
merge blockers by FR-20's own sentence, and DEV-1 says in the file's §0 that this
document is *"where they stop being unlisted — not where they stop being
deviations."*

- **E-1 — `test/memory`'s fragment `Render` writes to a shared stack probe.**
  The render calls `probe.note`, which takes a mutex and mutates a shared map,
  against FR-18. It is a measurement binary nobody copies, and **29 of the tree's
  30 renders close over nothing at all** — each a `func(State) templ.Component`
  reading only its argument. The thirtieth is this one.
- **E-2 — the error-handling guide's sample reducer logs from inside the
  reducer.** `docs/guide/_samples/errorhandling/errors.go:71` calls `slog.Warn`
  with three fields read off the event, inside `Reduce`, against FR-16's
  *"logging of application data"* clause. **PM-1 read the file: it is still there
  at HEAD, at the line the register names.**

**E-2 is the one that matters and the reason is where it is.** That file is the
compiled source behind `docs/guide/error-handling.md`, held by CI so the page's
code blocks are real — so it is code the project **shows a reader and invites
them to imitate**, teaching the exact mistake FR-14 and FR-16 exist to prevent,
on the page about handling failure correctly. Its blast radius is readers, and it
is unbounded.

**Its root cause was the library's own godoc, and DEV-1 found that rather than
blaming the sample author.** `live/core.go`'s `EffectFailedErrorField` comment
said, and had said since `9cce6829`, *"Log it, count it, branch on it"* — inside
a paragraph about what a **reducer** may render. The sample read it as an
instruction to the reducer, which is a fair reading of a sentence in that
position. **PM-1 checked that half is fixed:** the comment now says to branch on
it in the reducer and to log it from `Config.Execute` or from the `slog.Handler`
given to `Config.Logger`, and it names the deviation its ambiguity produced.
**The sample itself is unfixed and routed to DEV-3**, because `docs/guide/**` is
outside DEV-1's ownership. **That fix is a condition on Phase 4's exit** — it is a
live merge blocker by FR-20's own text, in a file a documentation phase publishes.

**Three grounds this box does not tick, each independently sufficient.**

1. **Two live deviations**, one of which is unfixed at HEAD.
2. **Every sign-off line is UNSIGNED and FR-20's gate is L9-1.** DEV-1 put that
   in the file's second line before anybody asked: *"L9-1 signs, not DEV-1."*
   A drafted register is not a signed one, and this is not a gate PM-1 holds.
3. **The box's own v0.8 text says it cannot tick before Phase 5**, and DEV-1's §0
   repeats it back rather than quietly working around it. §7.6 is what that
   implies for the phase.

**§3 of the register clears six categories on the text, and one of them deserves
a reader's attention rather than a nod.** `Config.Init` reads clocks and joins
shared stores in three applications. It is **cleared**, correctly, because
FR-14's subject is reducers and FR-16 requires I/O at the actor boundary — and
`Init` *is* the actor, running the mount transition on its own goroutine before
the first snapshot. **The consequence is stated rather than buried, which is why
the clearing is worth something: a session's mount state is not reproducible from
its event log.** FR-15's replay harness takes an `initial S` from the caller so it
never depended on that, but anybody reading FR-14 as "the whole session is a pure
fold" should know the fold's seed is not part of the fold.

**Owner: L9-1 to sign or to rule** — DEV-1 notes that ruling `test/memory` out of
FR-20's scope entirely would be a cleaner outcome than a signed exception, and
that it is L9-1's call and not theirs. **Owner: DEV-3 for E-2's sample.**

#### 4.13.2 Revision 3 — MET on the Phase-4 half, and the third ground is resolved rather than waited out

*(2026-08-05, at `b04ba138`. **The split is §5.7 and PRD v1.0 row 3; the two
amendments are §5.9 and PRD v1.0 row 4.**)*

**Taking §4.13.1's three grounds in reverse, because the last one needed a scope
act rather than work.**

**(c) — "cannot tick before Phase 5" — is resolved by splitting the criterion.**
§7.6 named this and declined to resolve it; §5.7 resolves it. The Phase-4 half
asks whether a signed, walked register exists, and it does. **L9-1 explicitly
declined to resolve it for me** — *"It is PM-1's, it is a scope act, and nothing
in this file should be read as having chosen one"* — which is the correct
refusal and is why the choice is argued in §5.7 rather than assumed here.

**(b) — unsigned — is closed, and the signature came with three corrections
L9-1 made before giving it.** E-1's blast radius **overstated the deviation**:
the draft said the probe's mutex means the memory figures carry its cost, and
L9-1 found the probe is nil unless a `-probe` flag that **no measured run
passes**, so accepting E-1 concedes almost nothing. And the register's own walk
commands **did not print the register's own numbers** — 16 reducers where the
command printed 24, 30 renders where it printed 29 — which was *"the entire
re-walkability guarantee"*. **Both stated numbers were right**; DEV-1 read what
the grep could not match. What was wrong was the claim that the commands produced
them, and a Phase-5 walker would have hit three disagreements at once with no way
to tell the one real signal from the two artefacts. Corrected and pinned:
**17 / 31 / 11 at `29348a5a`**.

**(a) — two live deviations — is now one, and it is signed.** E-2 was **fixed**
at `091dbae8` by DEV-3 and verified in the tree by L9-1, so **revision 2's
condition on Phase 4's exit is discharged**. E-1 remains as an **accepted
exception with an argument and a signature**, which is what FR-20 asks of a
deviation rather than a reason to hold a box open. **Rows without a disposition:
zero** — which L9-1 makes the count that matters, rather than the number of rows.

**The two rulings inside the signature are now requirement text**, because a
ruling that lives only in the file it governs will not be found by the next
person drafting against FR-20. **E-2 is CLOSED and RETAINED and §4's "then delete
this row" is overturned** — deleting it would leave `guide/error-handling.md`,
which names E-2 and links the register, pointing at a document that does not
carry what it names. **E-1's scope ruling is REFUSED** — an exception is
per-instance and a scope ruling is standing, so exempting `test/` is the
process-level version of the `live.LocalDevelopment` bundle the API refused the
same week. §5.9.

> **⟨CITATION CORRECTED 2026-08-05, revision 4. The sentence above stays as it
> was written; only the attribution beneath it changes, and the ruling it
> describes does not move at all.⟩** *"The `live.LocalDevelopment` bundle the API
> refused"* implies the API ledger refused a bundle **under that name**. It
> refused an **unnamed** one: `docs/api-surface.md:530` records one clause — *"a
> bundle that set them in one line was considered and refused in the same pass"*
> — with **no symbol and no signature**, and `grep -c LocalDevelopment
> docs/api-surface.md` returns **0**, as it always has. **The name is L9-1's**,
> coined at `bdf91971` in `docs/exceptions.md` §7.1. The true clause is: *the
> bundle `docs/api-surface.md:530` refused, which L9-1 named
> `live.LocalDevelopment(origin)` at `bdf91971`.* **This is the last of the six
> sites L9-1 enumerated** ([`reviews/fr-53-line-budget.md`](../reviews/fr-53-line-budget.md)
> §7.1) and **one of the two they found that PM-1 had not**; L9-1 corrected their
> own two beneath themselves, PM-1 fixed the two live PRD sites at v1.2, and
> these two waited for this revision. **`docs/api-surface.md` is deliberately
> untouched by everybody, including the agent who holds its pen and could have
> made all six citations retroactively true by adding the name to the `:530`
> row.** They declined: a ledger row for a symbol that does not exist is the
> failure FR-65 names, and back-filling a name so that later prose about it reads
> better is retrofitting history to fit a citation. **The refusal itself is real,
> is unweakened, and its load-bearing citation is L9-1's ratification rather than
> the ledger's aside.**

**One correction owed to my own §4.13.1, and §7.8 makes it.** That subsection
said E-2's root cause in `live/core.go` was fixed. **The corrective paragraph was
there and the sentence it corrects was still above it.**

---

## 5. The scope decisions this gate ratifies, corrects and refuses

*(§5.1 and §5.2 are revision 1's. §5.3 onward are revision 2's.)*

### 5.1 DEV-1's godoc scope narrowing — RATIFIED, as an amendment, with three conditions

**Ruling: the narrowing stands. It is ratified in PRD §9 v0.8 row 2 and written
into FR-66's own text, not absorbed into the box's tick.**

**What was narrowed.** doccheck's rules 1 and 2 — a doc comment on every exported
symbol, a package comment on every package — are enforced on the **published
module** only: `live`, `live/livetest`, `internal/**`. The eight satellite
modules (`examples/*`, `docs/guide/_samples`, `test/routers`, `test/sampling`,
`test/memory`, `bench/apps/*/gotth`, `tools/`) are walked and **printed** but not
enforced, at **268 undocumented symbols** by `1370229c`'s own measurement. Rule 3
narrows once more to the published module's non-`internal` packages. **Rule 4 is
enforced everywhere**, so FR-68 is untouched.

**Why it is ratified rather than waved through, and the test it had to pass.**
§9's preamble says a criterion may be narrowed after a measurement only when the
argument for doing so is **invariant to that measurement's outcome**. DEV-1's
primary argument is: the library is one module —
`go install …/gotth-live` resolves the root `go.mod` and nothing else — and the
satellites have their own `go.mod` files *specifically* so what they need cannot
reach a consumer's build list; none is published, importable, or rendered by any
godoc page. **That argument is identical whether the unenforced count is 3 or
268.** It is also the boundary `tools/apisurface` and `ci.sh`'s D-5 guard already
use, so ratifying it keeps three gates agreeing about what "the library" is,
which is worth more than any of the three answers individually.

**The half of DEV-1's case I am not adopting**, said out loud because a
ratification that only quotes the good arguments is not a ratification: *"writing
180 of them is how a doc gate teaches a team that doc comments are noise"* is an
argument from **volume**, and volume is exactly what an outcome-shopped narrowing
produces. I adopt the part that does not read on the number — a field capitalised
because `encoding/json` will not marshal it otherwise is exported by the compiler
rather than by an author — and I note that **359 of the 410 tree-wide undocumented
symbols are struct fields** as the reason that class dominates, not as the reason
the line is where it is.

**Two things DEV-1 did that made ratification possible, and both are the
difference between a narrowing and a hiding place.** They named it a narrowing
instead of describing it as coverage, in the tool's own doc comment, in the
`ci.sh` step comment and in the commit body. And they made the unenforced count
**print on every run** (`tools/doccheck/main.go:258`) with `-report` listing the
lot, so the number is in the CI log and widening the rule is a one-line change to
`enforcedScope`.

**Three conditions ride with it**, all carried in §6:

1. **The count keeps printing.** If it ever stops, the narrowing has become the
   thing it was ratified for not being.
2. **The figure gets reconciled to one value.** §7.1: the tree currently states
   it three ways. A carve-out justified by a number nobody can pin is a carve-out
   nobody can audit. **DEV-1.**
3. **`tools/*` is unenforced on itself** — including on `doccheck`, which is
   `scopeReported` in its own test's expectations. That is defensible on the same
   module-boundary argument and it is also the gate not checking its own author's
   work, so it is named here rather than left to be discovered. **DEV-1.**

**What would re-open it:** any satellite module becoming importable or published.
That is the trigger, and it is a fact rather than an opinion, in the FR-56 and
FR-55 pattern.

### 5.2 The rule I used to tick FR-44 and FR-57 — stated so it can be attacked

Both boxes name `Gate: QA-1` in their requirement lines and QA-1 has gated
neither. I ticked both anyway, and the rule is in §4.4: **§6's exit sentence has
two clauses — every box checked *and* the gate owners sign off at the exit
review — so a box is checked on evidence PM-1 has verified, and the signature is
a separate act at the exit.** Checkpoint 3 already worked this way in both
directions: row 4 ticked on PM-1's reading with QA-2 declining the row, and row 1
ticked on QA-2's measurement with PM-1 checking the tree matched.

**The alternative reading was available and I am recording why I did not take
it.** Under it, no Phase-4 box ticks until QA-1 examines each one, which would
make this report thirteen open boxes and one PASS. It is not obviously wrong. I
rejected it because it makes the box mechanism useless between gates — the state
of the phase would be unreadable until a single large QA event, which is exactly
the condition that produced checkpoint 1's missing gate record. **If the
orchestrator or QA-1 prefers the stricter reading, these two ticks are the ones
to reverse, and reversing them changes no other row.**

### 5.3 G11's wording — CORRECTED, and the ordering is the whole defence

*(Added 2026-08-05, revision 2. The amendment is PRD §9 v0.9 row 1; this is the
argument for it.)*

**Ruling: the criterion's command is corrected in both places it appears, and
"works" is pinned to the property that was measured.** From
`git clone && go run ./examples/<name>` to
`git clone && cd gotth-live/examples/<name> && go run .`, plus a clause saying
what "works" means — the process serves a page carrying its live-region markup
and the committed client runtime **from the URL that page itself names**, and the
run leaves the clone unchanged.

**The sentence was unsatisfiable and it was unsatisfiable for a reason that is a
design decision rather than a defect.** §4.7.1 has the two error messages. Each
example is a separate module; a separate module is not a package of its parent;
`./examples/<name>` is outside the main module by construction. Three `go.mod`
headers argue for that separation on grounds that have nothing to do with this
criterion.

**It passes §9's test, and it passes it in the strongest available form, which is
the only reason I am willing to touch a criterion in the same pass that measured
it.** §9's preamble asks whether the argument for the change is **invariant to
the measurement's outcome**. Here it is invariant in a way the two worked examples
in §9 are not: those are arguments about design that happen not to read on a
number, and this one is a fact about `go`'s module resolution that was true before
DEV-2 built the runner and would have been word-for-word identical **had all three
examples failed to serve**. The order in the record makes it checkable rather than
asserted: `9b457e56` and `5c751ae9` build and wire the runner, `6fed5b67` records
the green result, and the PRD sentence moves at `664043ca`, after.

**It is a correction plus a tightening and there is no weakening anywhere in it,
which matters because "the criterion was made easier to pass" is exactly what a
reader should suspect here.** The old sentence said "works" and defined nothing —
satisfiable by a process that starts and serves a blank page. The new one adopts
the strictest thing anybody actually observed, and two of its three clauses are
properties DEV-2's own first run got **wrong** and then corrected: the runtime URL
read from the page rather than hardcoded (the first run reported two of three
examples as serving no runtime, because it had `/live` written into it), and the
after-run pristine assertion. **A criterion is worth more when it names the things
that were nearly missed.** The gate (QA-1), the phase (4) and the four tools are
untouched.

**What I did not adopt from DEV-2's supplied replacement**, which they wrote as
input rather than as the answer and said so: its silence about what "works"
means. Their string fixes the path and stops. **What the sentence deliberately
does not do:** hedge the path. It names `gotth-live/examples/<name>` because the
clone today is of this monorepo; if the library is ever extracted the path is
`examples/<name>` and the `replace ../..` directives resolve identically either
way. That is recorded in the amendment rather than written into the criterion,
because **a criterion with an "or" in its path is a criterion nobody can run.**

### 5.4 FR-59's "architecture" subject — RULED NOT DISCHARGED by a design RFC

*(Added 2026-08-05, revision 2. The ruling is PRD §9 v0.9 row 3; §4.8.1 is the
evidence.)*

**Ruling: `rfc/001-architecture.md` does not discharge FR-59's architecture
subject, and either a reader-facing page or a re-filing of the RFC without its
disclaimer does.**

**The ground is one sentence of `docs/README.md`'s own**, which I read at HEAD
rather than characterised: *"The design record. None of it is needed to build an
application, and all of it argues rather than instructs."* FR-59 is a Phase 4
requirement gated by the person who builds an application from the documentation
alone. A subject of that set discharged by a document the set's index says is not
needed and does not instruct is discharged in name.

**This ruling is useless to me, which is the check that it passes §9's test.** The
argument reads off a preamble that predates this landing, and **the box does not
tick either way** — QA-1 has graded nothing — so the ruling buys no outcome and
costs a page. Had DEV-3 landed nothing this round the argument would be identical.

**DEV-3 raised it against their own delivery**, having just brought the subject
count to nine of nine, and did not fix it. That is the same habit §8 credits
QA-1, DEV-1 and DEV-2 with, arriving a fourth time, and it is the reason this
paragraph exists at all rather than the subject being counted and forgotten.

**What I am not ruling:** that the RFC is a bad document, or that the guide must
duplicate it. The re-filing alternative is nearly free and it is the honest test
of the question — **if the RFC cannot be moved into the guide without keeping the
disclaimer, then it was not instruction.**

### 5.5 The two mailbox sentinels — NOT disposed of by the FR-55/FR-56 precedent

*(Added 2026-08-05, revision 2. The ruling is PRD §9 v0.9 row 4; the finding is
DEV-1's, [`docs/error-audit.md`](../error-audit.md) §7.1.)*

**The facts.** `ErrSessionSaturated` and `ErrSessionClosing` are declared in
`internal/session/effects.go`. An effect meets them through `live.Emitter` and
**cannot import the package that declares them**, so the only thing an
application can do with the classification is match on the message text.
**PM-1 grepped the consequence: five call sites in this tree handle the pair with
one comment — *"The mailbox was full, or the session is closing"* — and no
branch**: `docs/guide/_samples/effects/effects.go:163`, all three `examples/`,
and `bench/apps/counter/gotth/store.go`, which is one more than DEV-1 counted.

**Ruling: the standing precedent does not dispose of this, and I am recording
that rather than letting it look as though it did.** v0.3 row 3 (FR-56's
`OnPatch`) and v0.5 row 3 (FR-55's form helpers) both refused an export whose only
consumer was an example, under FR-65 and review checklist §1.4. Refusing this one
on those two would have been the cheap move and it would have been wrong: **those
refused *helpers* — convenience an application could write itself. This is
*information*** — a classification the library already computes, already acts on,
and already hands to application code inside a sentence it invites nobody to
parse. A sentinel error is not a speculative abstraction; in Go it is the only way
a package says "this class of failure" across a boundary, and §5.K holds this
library to a standard-library bar where that is the ordinary answer.

**What I am not ruling, and why: the form.** Two sentinels re-exported from
`live`, one exported type with a method, or a documented refusal are three
different surfaces; choosing needs a `docs/api-surface.md` row and L9-1's
minimality review, and neither is available in a documentation reconciliation
where I have no toolchain and two files.

**Nobody's gate is blocked on it**, which is exactly why it can wait for the gate
built to decide it: DEV-1 states FR-58 is satisfied without the export, because
the message names all three clauses. Compare v0.6's preamble, where four rulings
were landed early *because* other agents' gates were blocked — the same reasoning
running the other way.

**The test it travels with, so it cannot be closed by silence.** At the Phase 5
api-surface gate (FR-65, L9-1): **either an application can distinguish "the
mailbox was full" from "the session is closing" without matching a message, or
the library documents that the distinction is unavailable and says why.**
**Owner: PM-1 to carry it into that gate; DEV-1 or DEV-2 to land whichever form
L9-1 approves.**

### 5.6 FR-54's "complete" — DEFINED, over a population that is deliberately not the one revision 2 proposed

*(Added 2026-08-05, revision 3. The ruling is **PRD v1.0's FR-54** and **§9 row
2**; this is the argument for it and the evidence it was taken on.)*

**Ruling: "complete" means four properties over a three-part population, and the
helper set fails it on three named gaps.**

**The whole of the ruling is the population, so that is where the argument goes.**
§4.3 offered a starting shape and revision 2 repeated it: *"the set is complete
when every event the three examples and the guide actually bind is expressible
without a hand-written attribute string."* **I am rejecting it, and the reason is
that it cannot fail.** An interaction the library cannot express is an
interaction the examples **work around** and therefore do not bind — so the set
of bindings-in-the-tree is partly *defined by* the gaps, and measuring
completeness against it is measuring the answer against itself.

**This is not a hypothetical objection; it is this exact box's worked case.**
`examples/chat`'s composer wants two keyboard behaviours. Enter-to-send it gets
free from being a real `<form>`. **Escape-to-clear it does not implement at
all**, and `FRICTION.md` F-3 says why. Under §4.3's shape, a composer that omits
the affordance the library cannot express **counts as evidence that the library
can express everything the composer needs.** A definition that does that is the
failure this project keeps catching, wearing the clothes of a definition.

**So the population is three parts, and the second is the one that does the
work.** (a) Every binding the tree renders — examples, guide samples, quickstart,
bench apps. (b) **Every interaction the equivalence spec's frozen §2 tables
require of the gotth-live side.** (c) Every binding any document here says is
absent *because it is inexpressible*.

**Why (b) is the right addition rather than an arbitrary widening, stated so it
can be attacked.** §2 is **pre-registered and frozen by §12**; it was written
before these gaps were known; it is the surface **G13 publishes us against**; and
**FR-73 forbids us aiming a strawman at ourselves.** An interaction we cannot
express is a feature-parity row we lose in public — suppressing it in the DX
requirement while reporting it in the bench report would be the project
disagreeing with itself in the two documents a reader compares. **The test I
applied to myself: would I have adopted (b) if it had produced no failures?**
Yes — it is a statement about which specification governs, and `bench/README.md`
had already been measuring against it for two apps before I looked.

**The four properties** are in the PRD and are not repeated here. The one worth
naming is **clause 3: a gap is *refused*, not merely *reported*.** That clause is
the reason this ruling changes anything. This project's habit of writing gaps
down honestly has meant three of them sat in a godoc, a ledger row and a bench
README — **each visible, each accurate, and none decided.** `docs/api-surface.md`
even routes one of them onward as *"a finding for PM-1"*, which is my name, and I
had not ruled on it in three revisions of this report.

**The three failures, with what I checked for each.**

| # | Failure | Where I checked it | Why it counts |
|---|---|---|---|
| **1** | **`F-CHT-3` is inexpressible.** `Bind.Keys` compares `KeyboardEvent.key` and **not** modifier state, so `Shift+Enter` arrives as `"Enter"`; and a key binding **never calls `preventDefault`**, so Enter would insert the newline as well as sending. Either alone is sufficient | `live/templ.go`'s `Bind.Keys` godoc, which states both; `docs/api-surface.md:615`; `bench/README.md:553` | **Reported twice, refused never.** api-surface records it as a *consequence* and routes it to PM-1; bench is the second consumer to hit it. **Both halves have real refusal arguments** — a chord belongs to the browser, and a library that `preventDefault`s on the application's behalf takes over `Ctrl+F` — and **neither has been made as a ruling with a re-open trigger**, which is clause 3 |
| **2** | **`Fields`/`Debounce`/`Throttle` are element-scoped, so composing two bindings changes what one of them does.** In the guide's own composer the `Escape` binding inherits the `input` binding's 150 ms debounce, and a keystroke inside that window **cancels the pending clear outright** | `live/templ.go:154` (attribute emitted only when `Debounce > 0`), `:183`–`:207` (`OnAll` keeps the first **present** value), `client/runtime.js:648`–`:664` (interval read off the element, timer keyed by the element, `clearTimeout` on each dispatch), against `docs/guide/_samples/events/view.templ:31` | This is **clause 2**, and it is not a missing helper — it is a defect in the helpers that exist, **live in the page FR-59 just passed on**. The godoc calls the sharing "a wart" and the guide states the rule; **neither states what it does to the example printed beneath it** |
| **3** | **The tree calls inexpressible something the set expresses.** `examples/chat/FRICTION.md` F-3 reads *"There is no non-JS expression for it"*, and its **"Proposed shape"** block is `live.OnWith("keydown", "chat.clear", live.Bind{Keys: []string{"Escape"}})` — the API that **landed at `591c275a` citing chat's F-3 by name** | `FRICTION.md:119`–`:157` and `:13`; `view.templ:64`; `git log -S'Keys []string' -- live/templ.go` | Fails the box's **documented** conjunct. F-1 and F-4 in the same file got *"— Closed."* headings when their features landed; **F-3 did not.** The affordance is still absent, so **the conclusion is true and the reason is false** — which is worse than a wrong number, because the reason is what a reader takes away |

**Failure 2 is derived from source and was not driven in a browser, and I am not
blurring that.** There is no toolchain on this host. The three files say what
they say and the arithmetic is not subtle, but **an observation is worth more
than a derivation** and this project has twice found that the browser is where
this class of defect actually lives. **It should be driven before it is fixed.**

> **⟨DRIVEN, and one attribution in row 2 CORRECTED — 2026-08-05, revision 4. The
> row above stays exactly as written.⟩** **QA-1 drove it** at `97ab20fb`,
> [`docs/qa/fr-54-debounce-repro.md`](../qa/fr-54-debounce-repro.md): **verdict
> REPRODUCES**, in Chromium 151 against the real shipped runtime and the real
> helpers, eight specs and three negative controls including a mutation control
> that turns three of them red. **Every one of row 2's four cited sites still
> lands**, and the observation is *larger* than the derivation in three ways and
> narrower in one — §4.3.2 has them.
>
> **The correction: *"The godoc calls the sharing 'a wart'"* is false, and it was
> false when I wrote it.** `grep -rn wart live/` returns **nothing**;
> `grep -rn wart` over the tree returns three hits and the only one that is a
> *source* of the word is **`docs/api-surface.md`**, in `OnAll`'s consequence row.
> The godoc documents the sharing plainly and accurately — *"an attribute the
> client reads from the ELEMENT and not from the binding that asked for it"* —
> and never characterises it. **The word appears to have come from `591c275a`'s
> own commit message**: *"The shared debounce timer is a wart and the godoc says
> so."* **That is a commit body quoted as though it were the artifact it
> describes**, and it is a failure mode this report has not previously named,
> distinct from the four wrapped-comment greps at §7.5, §7.7, §7.8 and §7.9: a
> grep that misses is a tool limitation, and this is a *source* substitution. The
> twin in `docs/PRD.md`'s FR-54 is corrected the same way at PRD v1.3.
>
> **The substance of the row is untouched and is now confirmed rather than
> derived.** The sharing **is** documented — by `OnAll`'s godoc and by
> `docs/guide/events-and-forms.md:48`–`:53` — and **neither says what it does to
> the sample printed twenty lines beneath the rule**, which is the finding and
> which survives its own attribution being wrong. **Found by QA-1** while driving
> the reproduction, reported and not fixed because this file and the PRD are
> PM-1's. *(Their own line citation for the ledger row does not reproduce either
> and it costs nothing: they cite `docs/api-surface.md:654`, it is at `:699` at
> HEAD and was at `:618` at the tree they drove. Same row, three line numbers, one
> append-only file — §7.10 correction 5.)*

**What I am NOT ruling, and it matters as much as what I am.** I am **not**
choosing the API shape for failures 1 or 2. `Bind.Modifiers`, a `PreventDefault`
flag, per-binding options, or a documented refusal are four different surfaces;
choosing needs a `docs/api-surface.md` row, a client byte cost, and L9-1's
minimality review under FR-65, and **none of those is a scope act.** My ruling is
that these are failures of completeness and therefore owed a decision — not which
decision.

**What is NOT a failure, recorded so this reads as a definition and not a
wish-list.** Everything in population (a) is expressible and is expressed:
`click`, `submit`, `input` with a debounce and `keydown` with a key filter,
across three examples, three bench apps, the quickstart and the guide, with **not
one hand-assembled `data-gotth-*` string in any of them** (§2.5 row 8). `OnAll`
closed the one case — two bindings on one element — that was previously
inexpressible at any shape. **The set is close.** "Complete" is a higher bar than
"sufficient for what we happened to build", and that gap is precisely what the
undefined word was hiding.

**Owners: DEV-2 and DEV-1 (the two API questions), DEV-3 (the documentation
one), L9-1 (any new surface, FR-65); QA-1 gates the box.**

### 5.7 Box 13 — SPLIT, and the argument was written before the outcome that made it decidable

*(Added 2026-08-05, revision 3. The amendment is **PRD v1.0 §9 row 3**; §7.6 is
the finding it answers.)*

**Ruling: Phase 4's box 13 becomes a Phase-4 half — the register exists, is
walked against the shipped tree, and every row carries an L9-1 disposition — and
a new Phase-5 box requiring the re-walk and re-signature against the tree being
tagged.** §7.6 named two resolutions and said the first was probably right. **I
am taking it, and §7.6 said this belongs in a landing that argues for it, so
here is the argument.**

**The problem was structural rather than cosmetic.** Box 13's own v0.8 text —
mine — ended *"This box cannot tick before Phase 5."* Taken with §6's exit rule,
that says **Phase 4 exits after a Phase 5 event**, which makes the honest answer
to "when does Phase 4 exit" not a list of chores. DEV-1 quoted the sentence back
in the register's §0 rather than working around it; L9-1 quoted it again when
signing and said their signature *"is not a workaround for either"*.

**Three grounds.**

1. **The two clauses ask different questions of different evidence.** *"Does a
   signed, walked register exist?"* is answerable today, against a tree that
   exists, by the person who signs it. *"Does it still hold against the tree that
   ships?"* is not answerable until there is such a tree, and **no quantity of
   Phase-4 work brings it forward.** A box conjoining them is a box whose
   Phase-4 half can be complete while the box reads open — which is §6's exit
   rule failing in the direction it was not written to catch.
2. **This project has already made this exact split once, and argued it.** G11,
   at v0.9: the Phase-4 box asks whether the property holds and it does,
   measured; the Phase-5 box asks the same question **at the tag**, because *"a
   release box may not close on a dated run of a check nothing re-runs"*. FR-20
   is the same shape with "walked" in place of "run". **Applying an existing rule
   a second time is cheaper to defend than inventing a second rule**, and a
   project that splits one criterion on a principle and refuses to split its twin
   is a project whose principle is a preference.
3. **§9's test, stated as a counterfactual, because this is the ground a reader
   should suspect.** The split is being taken **in the pass where L9-1's
   signature made the Phase-4 half true**, which is exactly the shape of an
   outcome shop. It is not one, and the record is what shows it: **§7.6 wrote
   this argument down at revision 2, before the signature existed**, and said the
   first resolution was probably right. The argument is about what the two
   clauses can be evidenced against; it would be **word-for-word identical had
   L9-1 refused to sign**, in which case the Phase-4 half would simply be open and
   the Phase-5 half would still be a separate question. **What the signature
   changed is not the argument but the cost of being wrong**: while the Phase-4
   half was unmet the split bought nothing and could be deferred; now that it is
   met, refusing to split means carrying a box that is open for a reason **no
   Phase-4 turn can address.** A scope act that would have been correct either
   way, taken at the moment deferring it stops being free, is the honest version
   of this — and I would rather write that sentence than have a reader find it.

**The alternative, recorded as considered and refused.** §7.6's second resolution
was to accept that Phase 4's exit review is convened during Phase 5 and say so in
the phase plan. **It fixes the calendar rather than the criterion**, and it leaves
in place a box nobody can act on, in a report whose §1 boasts about distinguishing
boxes blocked on work from boxes blocked on people. This one was blocked on
neither; it was blocked on its own sentence.

**The split loses nothing, and that is a checkable claim rather than a promise.**
The new Phase-5 box carries `docs/exceptions.md` §7.5's standing re-walk
requirement **verbatim**, including the clause L9-1 made explicit when signing:
a walk that finds counts other than **17 / 31 / 11 must say which directory moved
before it says anything else.** **Owner: DEV-1 to walk, L9-1 to sign, at Phase 5.**

### 5.8 Was 30 ever reachable? — ARGUED, and the answer is no, for a reason that is a conflict rather than a shortfall

*(Added 2026-08-05, revision 3. §4.2 has carried this as debt since v0.6: *"PM-1
owing an argument — not a gate day — on whether 30 was ever reachable for a real
HTTP server plus a view."* The full text is in **PRD FR-53**; this is the summary
and the disclosure that goes with it.)*

**Ruling: no. And the threshold does not move in this pass.**

**The measurement, which I took rather than quoted.** DEV-1 costed the most
aggressive library-side shrink anybody has proposed — a `live.Document`
page-shell component hiding `<!DOCTYPE>`, `<html>`, `<head>`, `<meta>`, `<title>`
and `live.Script` inside the library. **I could not find that costing written
down anywhere in the tree**, so rather than quote a number I could not open, I
did the arithmetic: the quickstart's templ shell is 13 counted lines
(`:335`–`:347`), a `Document` form is 5, so **19 − 8 = 11 templ and 20 + 11 =
31**. **Hiding the entire HTML document misses by one.**

**What is left at 31 is why there is no twelfth cut.** Two constants, a state
type, `main` and its brace, the reducer's own three lines, `ListenAndServe`, and
the **seven** `Config` fields `live/app.go:158`'s `validate` requires — **four of
which are security hooks a caller must name even to opt out of** (§2.5 row 6).
*(This corrects "eight `Config` fields", which this report has said since revision
1 and which was never true of `validate`.)*

**And that is where the argument stops being arithmetic.** Collapsing the four
security hooks into one line is not hypothetical — it is exactly
`live.LocalDevelopment(origin)`, which `docs/api-surface.md` **proposed and
refused**, on the ground that the per-check review signal is the thing of value
and a bundle destroys it. **L9-1 ratified that refusal on 2026-08-05 and then
built `docs/exceptions.md` §7.1's refusal of the `test/` scope ruling on top of
it**, writing that a project cannot refuse a bundle in its API on Monday and
grant one in its process on Tuesday. **So the only remaining route from 31 to 30
is the precise trade this project has now refused twice, in two places, on
stronger grounds than a line budget. FR-53's 30 and that refusal cannot both
stand, and nobody had said so.**

> **⟨CITATION CORRECTED 2026-08-05, revision 4, and the paragraph above is left
> exactly as it was written. Its conclusion does not move by a word.⟩** **The
> identifier `live.LocalDevelopment(origin)` is not in `docs/api-surface.md` and
> never has been.** `grep -c` returns **0**; `git log -S'LocalDevelopment' --
> docs/api-surface.md` is empty. What `api-surface.md:530` records is one clause,
> with **no symbol and no signature**: *"a bundle that set them in one line was
> considered and refused in the same pass."* **The name is L9-1's**, coined at
> `bdf91971` in `docs/exceptions.md` §7.1 and used again at `cdb30b5d`. The true
> clause is: *the bundle `docs/api-surface.md:530` refused, which L9-1 named
> `live.LocalDevelopment(origin)` at `bdf91971`.*
>
> **This is the site PM-1 enumerated and routed, and it is the one that waited
> longest.** PM-1 found the mis-citation in their own text while deriving FR-53's
> floor (PRD §5.I (f) 1, v1.1) and named **two** carriers. **L9-1 found six**
> ([`reviews/fr-53-line-budget.md`](../reviews/fr-53-line-budget.md) §7.1),
> including **two live PRD sites that were inside PM-1's own write scope the
> whole time**, so *"outside PM-1's write scope this turn"* was true of the two
> PM-1 had in mind and false of the two they had not looked for. Those two were
> fixed at PRD v1.2; **this one and §4.13.2's were deferred to this revision and
> that deferral cost two revisions of a report whose §7 is about numbers that do
> not reproduce.** **What is corrected is a citation. The refusal is real, is
> unweakened, and this section's conclusion — that 30 and that refusal cannot
> both stand — rests on L9-1's ratification, which is the stronger of the two
> anyway.**

**What I am not doing, and the disclosure is the point.** **I am not moving 30
here.** §9's preamble and RFC-0001 §6.1.2 make the pass that measures a miss the
one pass in which the target may not move, and **this pass re-measured 39**. If I
now believe the number is wrong, the honest form is an **argued amendment in a
later pass with the measurement on the record**, and that is what is
pre-registered in FR-53. **It will have to answer the one objection this argument
does not:** that a budget unreachable by design is still doing its job if what it
really gates is **ceremony**, and **39 → 31 in one turn is evidence that it was.**
I do not think that objection wins, but it is the strongest one against me and it
should be in the record before I argue the other side. **Owner: PM-1, at the
Phase 5 gate or the pass after it.**

### 5.9 FR-20's two amendments — ADOPTED as L9-1 requested, with the original sentence deliberately untouched

*(Added 2026-08-05, revision 3. The amendment is **PRD v1.0 §9 row 4**; the
requests are [`docs/reviews/phase-4-exceptions.md`](../reviews/phase-4-exceptions.md)
§5.)*

**Both are adopted, close to L9-1's requested wording, and both are additions
beneath FR-20's existing sentence rather than edits to it.** That last part is a
decision: **`docs/exceptions.md`'s header quotes FR-20 verbatim**, and a
requirement that moves under a document quoting it is this repository's
most-repeated defect. The clauses go below.

**Amendment 1 — a fixed deviation is CLOSED with its disposition and its fixing
commit, and retained; entries are not deleted.** L9-1 had already acted on this
reading, overturning the register's own §4 instruction to delete E-2's row on
fix. **The requirement should say what the project does**, and L9-1's reason for
routing it to me is the one that decides it: *"a ruling that lives only in the
file it governs will not be found by the next person drafting against FR-20 —
they will read FR-20."* The two grounds are carried into the PRD: deleting the
row would leave `guide/error-handling.md`, which names E-2 and links the
register, **pointing at a document that does not carry what it names**; and a
register that deletes on fix cannot answer the Phase-5 reviewer's question, which
is not *"what is broken today"* but *"has this rule ever been broken here"* —
**re-creating, for history, the exact unlisted state FR-20 calls a merge
blocker.**

**Amendment 2 — FR-20's scope is every tree in the repository implementing the
reducer or render contracts, whether or not it ships.** This one is a rule I owed
rather than a preference. **The wide reading was asserted by the register and
ratified by L9-1, and E-1 exists only under it** — so the scope of a requirement
was living in the document that requirement governs, where *"the next drafter may
narrow it without noticing they are narrowing anything."* **L9-1 was offered the
narrow reading as a scope ruling that would have deleted E-1 outright and refused
it**, on the ground that decides it: **an exception is per-instance and a scope
ruling is standing.** Exempting `test/` would say, once and permanently, to
authors who have not written their harnesses yet and will never read that
paragraph, that no future measurement harness needs an argument, a blast radius
or a signature.

**I am ruling as requested on both, and I want the one place I could have gone
the other way on the record.** Amendment 2 makes the guide's compiled samples,
the bench apps and the measurement harnesses permanently in scope for a
requirement gated by L9-1, which is a real ongoing cost on people who are not in
this conversation. **The reason I take it anyway is that the cost was measured
rather than estimated**: E-1 is the worked example, and it cost one paragraph,
four greps a reader can re-run, and a signature. **A standing exemption is cheap
today and unfalsifiable forever; a per-instance exception is a paragraph.**

### 5.10 The count gate's number — AUTHORISED at ≤31, with its source of truth named and four constraints attached

*(Added 2026-08-05, revision 4. Routed by **L9-1** at
[`docs/reviews/page-shell.md`](../reviews/page-shell.md) §11.6 as **PM-1
authorises → DEV-1 implements → QA-1 verifies it fails**, and made condition
**Q-4** by **QA-1** at [`phase-4-grading.md`](../qa/phase-4-grading.md) §10.13.
**This is the only act in this revision that is a PM-1 decision rather than the
application of somebody else's.**)*

**Ruling: the gate asserts the quickstart's counted total is `≤ 31`. The number
is authorised, and its source of truth is FR-53's line clause at PRD §5.I — as
amended at v1.1 and countersigned at v1.2 — and not the current count, and not a
constant the gate owns.**

**Why this needed a signature at all, which is the part worth defending.** Two
reasons, both L9-1's, and the second is the one that decides it. DEV-1's — that
adding a gate on a requirement you are measured by is not yours to do
unilaterally — is sound. **L9-1's is stronger: the gate needs a number, the
number is the budget, and the budget is PM-1's under §5.I. A gate written by the
party measured by it, encoding a budget it does not own, is the self-dealing
shape this project has already had to disclose once.** So the number comes from
here and the code comes from somebody else, and neither party can quietly move
the other's half.

**Why a gate should exist at all, in one sentence that is not mine.** FR-53's
line clause is the only requirement in this project measured in lines, it is
re-counted by hand at each gate by whoever is holding it, and **it is presently
protected by nobody** — `grep -rn FR-53` over `*.go` and `*.sh` finds two
incidental comments and no check, and `ci.sh` has none. That is the shape
`ci.sh`'s own header condemns: *a requirement whose gate is a tool nobody runs is
a requirement in name only.* **And the margin is zero**, so the first line
anybody adds silently un-earns a box that took five versions to close.

**Four constraints travel with the authorisation.**

1. **The assertion is `≤ 31`, not `== 31`.** A gate that fails on a *smaller*
   app would make trigger 4's ratchet unimplementable — trigger 4 tightens the
   budget when the counted app comes in **below** it, and a gate that goes red at
   30 makes that trigger unreachable without a simultaneous gate edit. **A floor
   asserted as an equality is a quota**, and this project has spent two versions
   establishing that 31 is a floor.
2. **The counting rule is v0.6's, under the reading QA-1 graded on**: the whole
   parenthesised `import ( … )` declaration is import lines, block and closing
   paren included. **That is Reading A, and it is not a preference** — QA-1 ran
   both readings over the two blocks at all six commits this project has
   published a figure for and Reading A reproduces **46, 46, 39, 39, 31, 31**,
   six for six, where Reading B produces 55, 55, 46, 46, 38, 38, **and 55 and 38
   appear nowhere in this project's record.** A gate encoding Reading B would not
   be enforcing the v0.6 rule; it would be replacing it, retroactively
   falsifying the miss table the amendment log built expressly so a threshold
   could not bury what it had been missed by.
3. **It measures the two marked blocks of `docs/quickstart.md`** — what a reader
   copies, and therefore what FR-53 measures — **and its failure message carries
   the per-file split**, so a red gate says *which* file moved rather than only
   that something did. Measuring the pinned samples instead is not sufficient on
   its own: QA-1's M2 and M3 mutations show the samples pin is `doc ⊆ sample` as
   an indentation-insensitive **set**, which does not hold a count in either
   direction and stays green on a doc block that repeats an existing counted line
   four times.
4. **It must be shown red at 32 before it is credited**, which is QA-1's clause
   and is the whole difference between a gate and a decoration. **This report has
   now caught six checks that could not fail; this must not be the seventh.**

**What this authorisation does not do.** It does not move the budget — the budget
is 31 and has been since PRD v1.1. **If the budget ever moves, the gate's number
moves with it in the same PR**, which is triggers 1, 2 and 4 already; **the gate
is downstream of the triggers and is not a second copy of them**, and a gate that
disagrees with §5.I is the defect, not §5.I. It does not choose a file, a
framework or a shape beyond NFR-10's standing convention (**Ginkgo v2 + Gomega**,
which is what the `docs/guide/_samples` suite already is). And, per L9-1, **it
must not land in the same PR as a change to the count.**

**Owner: DEV-1 or DEV-3 to implement; QA-1 to verify it fails.** A DEV stream is
implementing it against this number as this revision is written.

### 5.11 Which of the thirteen ticked on somebody else's grade, and which on my own reading — the whole table, once

*(Added 2026-08-06, revision 6, at `9efb7e5b`. **§5.2 states the rule this table
applies and §5.2 is not amended here.** The rule has been applied row by row for
five revisions and never gathered; a phase exit is the moment to gather it,
because *"thirteen of thirteen"* is a number a reader will quote without the
thirteen provenances behind it.)*

**Nine of the thirteen tick on somebody else's signature. Four tick on PM-1's
reading of the tree under §5.2's rule.** Naming which is which is the difference
between a gate record and a scoreboard.

| Box | Criterion | Ticks on | Whose act, and where |
|---:|---|---|---|
| 1 | The docs-alone gate | **QA-1** | `phase-4-docs-alone.md` §6 at `452e1e74` — PASS, 2 m 12 s, zero source-diving breaches |
| 2 | FR-53 + G7, the timed counter | **QA-1** | `phase-4-grading.md` §10 at `5d665226` — PASS WITH CONDITIONS **Q-1…Q-4**, all four open |
| **3** | **FR-54, the helper set complete and documented** | **QA-1** | **`phase-4-grading.md` §11 at `eb4971c6` — PASS WITH CONDITIONS Q-5…Q-8**, over an artifact **L9-1 gated first** against nine pre-registered constraints |
| 4 | FR-57, dev reload | **PM-1's reading**, under §5.2 | DEV-2's behaviour + `13a1ca1e`'s conformance specs. **QA-1 names this box and has not graded it** |
| 5 | FR-44, the inspector | **PM-1's reading**, under §5.2 | DEV-2's artifact, 6,211 B re-measured by PM-1. **QA-1 names this box and has not graded it** |
| 6 | The three examples, polished and documented | **QA-1** | §4.5 → §9.1.7 — **FAIL** at `091dbae8`, **PASS** at `368132f6` |
| 7 | G11, consumable from a clean clone | **QA-1** | §3 — PASS, no conditions, re-run by QA-1 with a fourth negative control of their own |
| 8 | FR-59, the docs set | **QA-1** | §9.2.7 — PASS, one condition, closed |
| 9 | FR-77's documentation half | **PM-1's reading** | Four line-anchored citations against the box's own wording, §2.2 row 4 |
| 10 | FR-66, the godoc gate | **PM-1's reading**, against FR-66 **as amended in the same landing** | §5.1 ratifies the narrowing as an amendment rather than letting the tick absorb it |
| 11 | FR-68, `Example*` compile and run | **PM-1's reading** | 2 → 6 examples counted by PM-1 at HEAD |
| 12 | FR-58, the error audit | **QA-1** | §2, §9.3.4 — PASS, condition discharged |
| 13 | FR-20's Phase-4 half | **L9-1's signature**, on §5.7's split | `bdf91971`, note at `reviews/phase-4-exceptions.md` |

**The four PM-1 ticks are boxes 4, 5, 9, 10 and 11 — which is five, not four, and
I am correcting my own sentence rather than the table.** Boxes 4, 5, 9, 10 and 11
tick on PM-1's reading; boxes 1, 2, 3, 6, 7, 8 and 12 on QA-1's; box 13 on
L9-1's. **Seven, five and one.** *(Miscounted in the paragraph above this table on
the first draft of this section, caught by summing the column. It is a small thing
and it is left visible because §7.10's fifth correction is that routed numbers
outlive their routing, and a count stated in prose beside a table that contradicts
it is the same defect one level down.)*

**Two of the five PM-1 ticks are the two §5.2 was written to defend, and they are
still the two to reverse if anyone prefers the stricter reading.** Boxes 4 and 5
name `Gate: QA-1` in their requirement lines and QA-1 has still not graded either,
six revisions later. **The phase exits carrying that**, and it is stated here at
the exit rather than left in a subsection from revision 1. Reversing them changes
no other row and would make this exit eleven of thirteen.

---

## 6. What carries forward, with owners — and which of these are conditions

**This distinction is the point of this section.** Checkpoint 3 carried L9-1's
framing that a row here *"is not a condition and should stop being re-litigated
as one"*, and it worked. So each row below says which it is. **Conditions block
Phase 4's exit. Carried items do not** — they have owners and homes, and they are
not arguments to be had again.

| Item | What it is | Owner | Condition on Phase 4's exit? |
|---|---|---|---|
| ~~**The seven open boxes**~~ → ~~the two open boxes~~ → ~~the ONE open box~~ → **CLOSED — none** | ~~§3 rows 2, 3, 6, 7, 8, 12, 13~~ → ~~§3 rows 2 (FR-53) and 3 (FR-54)~~ → **§3 row 3 (FR-54)** at revision 4, **and still §3 row 3 at revision 5**. Rows 6, 7, 8 and 12 closed on QA-1's grades; row 13's Phase-4 half on L9-1's signature and §5.7's split; row 2 on QA-1's grade of a page shell L9-1 gated first. **At revision 5 two of FR-54's three failures are FIXED and the third is DECIDED-not-built, and the row does not move**: the box closes on L9-1's FR54-3, FR54-4 and FR54-6 plus QA-1's grade, and none of the four has happened. **→ CLOSED at revision 6.** All four happened: FR54-3 (`0b31e67d`), FR54-4 (`42b4e0e6`), FR54-6 (`0b9e32e7`/`2311280b`, accepted by L9-1 at `d60042ae`), and **QA-1's grade** at `eb4971c6`. **Thirteen of thirteen. This row is what the phase was blocked on and it is empty** | **DEV-1** (FR54-3, FR54-4, the Part B code); **DEV-2** (FR54-1, FR54-2); **L9-1** (the nine constraints); **QA-1** gates | ~~**YES — by definition.** §6's rule is that a phase exits when every box is checked~~ → **Was the condition; is discharged. Phase 4 EXITS** |
| **Box 3's four conditions, Q-5…Q-8 — QA-1's, travelling with the tick** | **New at revision 6, and this row is the reason the exit revision is harder than the tick.** Revision 5's own words: *"a tick that swallows a named open row is how the row stops being found."* [`phase-4-grading.md`](../qa/phase-4-grading.md) §11.9. **Q-5 — `reviews/fr-54.md` §13's leading refusal ground prices the full modifier set at *"+57 gzipped … fourteen times the `preventDefault` half"*, measured on a baseline that has not existed since `0b9e32e7` and on a prototype C-9 forbids. At HEAD the marginal cost is +10 gzipped bytes.** Grounds 2 and 3 hold on QA-1's own evidence and **the refusal STANDS**; what is owed is the number, corrected beneath itself, so a future T-2 proposal argues against the real figure — **L9-1**. **Q-6 — `client/test/binding.test.mjs`'s AltGr spec is introduced by a false sentence**, *"this is the spec that would go red if the runtime stopped reading one of the four"*; M4, M5 and B2 show it stays green, and the per-modifier table three lines below is the one with the property — **DEV-1**. **Q-7 — `docs/PRD.md` is stale on two of the three failures it grades** — **PM-1, DISCHARGED at PRD v1.5 in this pass, and it is the only one of the eight open QA-1 conditions PM-1 has discharged.** **Q-8 — `refuseUnbindable` and §22.3 disagree at HEAD and both are documented as if they did not; the tree is self-consistent and the ruling is the outlier** — **DEV-1**, and it is FR54-7's other half | **L9-1** (Q-5), **DEV-1** (Q-6, Q-8), ~~**PM-1** (Q-7)~~ | **No — they travel with the tick and do not gate it**, on QA-1's own test: *"not one of them makes any binding inexpressible, uncomposable, undecided or undocumented."* **They are conditions on the work, not on the exit, and they are open** |
| **What QA-1's box-3 pass does NOT prove — carried verbatim rather than summarised** | **New at revision 6**, from `phase-4-grading.md` §11.8, because a pass's own disclaimers are the first thing a summary drops. **(i) G11 did not run** in the gate this exit quotes — host docker daemon — and box 7 rests on QA-1's separate run at §3. **(ii) One browser, one version:** Chromium 151; `F-CHT-3`, the `MouseEvent` clause and the modifier reads are unproven on Firefox, Safari and WebKit, and `AltGr`'s `ctrlKey`+`altKey` reporting was taken from the spec and from CDP rather than from four engines. **(iii) The `PreventDefault`-outside-a-region behaviour is TRUE and asserted NOWHERE** — the guide states it, QA-1 read `dispatch` and confirms it, and no spec holds it: the same shape FR54-8 had before `8363396c`. **(iv) Clause (c) is a bounded sweep** — fifteen phrasings — and *"a sentence that states a binding absent in words none of the fifteen matched would have survived me, and this project has now found four such sentences after four declarations that the sweep was complete."* **(v) §18.2's seventeen byte spellings were not rebuilt.** **(vi) QA-1's T-2 probe is a price probe, not a proposal** — no Go surface, no specs, and it fails T-2's zero-output-delta limb | **(iii)** is **DEV-1**/**DEV-2**'s to assert if anyone wants it asserted; **(ii)** is a standing project limit; the rest are disclosures with no owner by design | **No.** **None of these held the box open and none of them is being filed as debt** — they are the boundary of what the grade covers, recorded so nobody later reads the tick as covering them |
| **Phase 4 exits; the PROJECT does not. Phase 5 has no measurements** | **New at revision 6, and it is the sentence most likely to be lost in a thirteen-of-thirteen headline.** What remains is **Phase 5 — the benchmark measurement, the headline report and the feature-parity table — and no benchmark timing has been collected.** `bench/**` has apps, a harness and an equivalence spec; it does not have numbers. **Box 13 was SPLIT at revision 3 (§5.7) precisely so that Phase 4 could exit without claiming Phase 5's half**, and the Phase-5 half — the FR-20 register re-walked against the shipped tree at Phase 5 — is untouched by this exit and is already a row in this table. §7.6 argued at revision 2 that this phase could not exit before Phase 5 began; **the split is the answer to that argument and it is worth restating at the exit rather than leaving in a subsection five sections up** | **BENCH-1** (the apps and the measurement), **PM-1** (the Phase-5 scope act), **QA-2** (performance) | **No — it is not a Phase 4 condition.** It is here so that *"thirteen of thirteen"* is never quoted without it |
| **Box 2's four conditions, Q-1…Q-4** | **New at revision 4, and they are QA-1's rather than this report's.** They travel with the tick at §3 row 2 and none is discharged here. **Q-1** — §4's build block leaves the reader with no `go.sum`, so the documented path errors for every reader, unconditionally; `go mod tidy`, `go get` and `go.sum` appear nowhere on the page. **Q-2** — §4's *"403s in the log"* row points at a log the counted application cannot write, on the page's most likely reader error, and **must not** be fixed by adding `Logger` to §2's `Config`: that is one counted line, takes the app to **32**, and under the repaired trigger 1 withdraws the amendment and reopens the box. **Q-3** — the counting rule does not say whether entries inside a parenthesised `import ( … )` block are import lines, which is worth **7 lines** on a clause with zero margin. **Q-4** — nothing in the tree can fail if the count goes to 32; §5.10 authorises its number | **DEV-3** (Q-1, Q-2, and the page's half of Q-3), **PM-1** (Q-3's requirement wording — **owed, not taken**), **DEV-1 or DEV-3** (Q-4's implementation), **QA-1** (Q-4's red-at-32 verification) | **Q-1 and Q-2: YES**, blocking on `docs/quickstart.md` — a documentation phase may not exit on a page whose printed build path errors for every reader. **Q-3 and Q-4: No**, on QA-1's own marking, and Q-4 is the one they say they would keep if they could keep only one. **⟶ A remediation for all four landed at `f555f3b5` while this revision was being written (§2.7). PM-1 records that it landed and that Q-4's gate matches the number and the four constraints §5.10 authorised. PM-1 does NOT discharge them: they are QA-1's conditions, Q-4's credit turns on a red-at-32 demonstration that is QA-1's, and this report has spent three revisions establishing that work landing is not a gate passing.** **⟶ Revision 5: unchanged, and this is the third time of saying it. `f555f3b5` has now been in the tree across a whole further round of landings and QA-1 still has not graded it. Q-1 and Q-2 are the last two conditions on Phase 4's exit that are not FR-54's, and neither is PM-1's to discharge — a documentation phase may not exit on a page whose printed build path errors for every reader, and it may not exit on PM-1 deciding that somebody else's remedy for that is good enough. `docs/quickstart.md:161`'s stale probe figure (§2.8 row 7) is on the same page and is cheap to take in the same pass** |
| ~~**FR-54's three failures, individually**~~ | ~~New at revision 3~~ → ~~updated at revision 4~~ → ~~updated at revision 5~~ → **CLOSED at revision 6: ALL THREE ARE CLOSED.** Failure 1 by decision **and** artifact — the accepted half landed and is driven in Chromium 151, the refused half is refused at `reviews/fr-54.md` §13 with a three-limbed trigger **QA-1 fired every limb of**; failures 2 and 3 by engineering, and both re-verified by QA-1's own mutants rather than by this report. **Population clause (c) is EMPTY**, on a fifteen-phrasing sweep QA-1 ran themselves. The row as written at revision 5 follows, unedited: **updated at revision 5: TWO ARE FIXED and the third is DECIDED, and the row survives because "decided" is not "closed".** **Failure 2 — FIXED** (`2ab18690`): options scope to the binding that declared them, `data-gotth-fields`/`-debounce`/`-throttle` deleted, **+0 identifiers and −85 B / −8 B** — verified by PM-1 with `apisurface` and `minify -check` (§2.8) — driven in Chromium against QA-1's own reproduction. **Failure 3 — FIXED** (`b6bfe108`): `examples/chat` implements Escape-to-clear, driven 6/6 against the shipped example, and both the `view.templ` comment and `FRICTION.md` F-3 now say so. **Failure 1 — DECIDED, NOT FIXED** (L9-1, `e751f6de`): `Bind.NoModifiers` + `Bind.PreventDefault` **ACCEPTED** at +0 identifiers / +2 fields / +34 gzipped B; the full modifier set **REFUSED** with a three-limbed trigger; **both standing refusal arguments ruled aimed at the wrong target**. **The accepted surface has no artifact** — it is a prototype in a container's `/tmp`. §4.3.3 | **DEV-1** (`live/**`, `client/**` — the Part B landing), **L9-1** (the nine pre-registered constraints, §14 of the review), **QA-1** (the grade) | **YES** — it is box 3, and it is still the only open box. **Failures 2 and 3 are struck from this condition; failure 1 is not** |
| ~~**FR54-1 … FR54-6 — L9-1's six conditions on the FR-54 landing**~~ → **all six DISCHARGED; the numbering grew to thirteen and ONE is still open** | **→ At revision 6: FR54-1, -2, -3, -4, -5, -6, -8, -10, -11, -12 and -13 are DISCHARGED** (`reviews/fr-54.md` §24, §31 and the rulings at `d60042ae`/`f4b017ad`/`eb4971c6`). **FR54-7 is OPEN and travels behind the box** — refuse an empty `domEvent` per §22.3 — and it is Q-8's other half: **the code and the ruling disagree, the tree is self-consistent, and the ruling is the outlier.** L9-1 placed it behind deliberately, *"because moving the closure condition after the artifact exists is C-3's error mirrored"*, and QA-1 agreed and conditioned only that the divergence not travel silently. **FR54-9 is PM-1's and is discharged in this revision** — §7.11. The row as written at revision 5 follows, unedited: **New at revision 5**, [`reviews/fr-54.md`](../reviews/fr-54.md) §15, numbered so they discharge individually. **FR54-1** — `api-surface.md:586`'s *"there is no mixed-version window"* is false and **driven false**: unfingerprinted runtime path + `immutable` for a year means an old cached runtime against new markup gives `armed timers: 0` and silently dropped `Bind.Fields`, with no error and no `4003`. L9-1 appended the ledger correction themselves; what remains is `docs/guide/deploying.md:54`–`:56`, which reasons only about the wire protocol. **FR54-2** — `client/SIZE.md:628` still says *"one 10,391-byte IIFE"* **inside the ledger this landing re-measured**. **FR54-3** — `binding()` must refuse a `:`/`;` in a key, a `domEvent` or an `eventName`, and an empty `eventName`: today `Bind{Keys:[":"], Debounce:150ms}` renders a **throttle**, silently, which is **strictly worse than before this landing**. **FR54-4** — the timer record's spec-keying is claimed in three documents and pinned in none; `r = st[name]` is green against **156/156 client and 7/7 browser specs** while reintroducing failure 2 for two bindings sharing an event name. L9-1 wrote the missing spec. **FR54-5** — `Bind.Keys`' godoc says a `:`/`;` key *"cannot be expressed"* and not what happens if you write one. **FR54-6** — the Part B landing, against §14's nine constraints, at §12's count and bytes | **DEV-2** (FR54-1's `deploying.md`, FR54-2's `SIZE.md:628`); **DEV-1** (FR54-3, FR54-4, FR54-5, FR54-6's code); **L9-1** (§14) | **FR54-3, FR54-4 and FR54-6: YES** — they are L9-1's named terms for box 3 closing. **FR54-1 and FR54-2: on the landing** (§9.7 blocks), not on the phase. **FR54-5: no.** *PM-1 discharges none of these and does not own any of them* |
| ~~**The accepted `Bind.NoModifiers`/`Bind.PreventDefault` surface does not exist**~~ | **→ CLOSED at revision 6: it exists.** `0b9e32e7`/`2311280b` landed it as grammar components 7 and 8. **The measured landed price is +81 B minified / +38 B gzipped — not the +62 / +34 this row states below**, which was the price of the `/tmp` prototype; PM-1 measured the landed delta at §2.9 tool row 3 and it is corrected at §7.11 rather than edited here. **+0 exported identifiers and +2 fields (51 → 53) both reproduce exactly**, re-run by PM-1. **C-9 was honoured** — `preventDefault` sits **below** the composition guard, and QA-1's **M3** proves it by hoisting it back above and turning exactly the IME spec red. The row as written at revision 5 follows, unedited, because it was true when written: **New at revision 5, and it is the distinction the whole revision turns on.** L9-1 ruled the shape **ACCEPTED** and specified it down to the godoc, the grammar (components 7 and 8, trimmed when unset, so **zero output delta**) and the client's two lines — measured at **+62 B minified / +34 B gzipped, +0 identifiers, +2 fields (51 → 53)**, verified by a prototype at zero delta across 156 client and 7 browser specs, with a **negative control** that reproduces today's loss. **Nine constraints are pre-registered before the artifact (C-1…C-9), and C-9 was found by building the prototype: `dispatch` calls `preventDefault` BEFORE the `if (composing) return` IME guard, so the flag must sit after it or it breaks every CJK composer** — FR-26's population. **L9-1's own prototype gets C-9 wrong and says so.** A ruling is not a landing | **DEV-1** to build (`live/**`, `client/**`), **DEV-2** for `client/SIZE.md` §1.1.6, **L9-1** to gate against §14 | **YES** — this is FR54-6, which is failure 1 closing |
| ~~**QA-1 has not graded the FR-54 batch**~~ | **New at revision 5, and it is a term of the box rather than a preference of this report.** L9-1's closure sentence is *"FR-54's box closes when FR54-3, FR54-4 and FR54-6 are discharged **and QA-1 grades them**."* Four landings have gone into FR-54 this turn (`2ab18690`, `b6bfe108`, `d12870a0`, and the Part B landing when it exists) and **QA-1 has graded none of them.** This is the same shape revision 2 found and revision 3 resolved: the phase is blocked on somebody being asked. **→ CLOSED at `9efb7e5b`**, `phase-4-grading.md` §11: **PASS WITH CONDITIONS**, over ten landings rather than four. **And it closed the way revision 3 said this shape closes — somebody was asked** | **QA-1**; the **orchestrator** to ask | ~~**YES** — by L9-1's ruling and by §6's own second clause~~ → **Discharged** |
| ~~**L9-1's routed findings — four sites in three other owners' files**~~ | **→ CLOSED at revision 6, all four, and PM-1 re-checked the five size figures at the tree rather than taking the routing** (§2.9 tree row 3). **R-1:** `bench/README.md` §6 item 1 is struck with a three-row *reason → what closed it → where* table — and **all three** of its "independent reasons" are dead, not the one this row named. **R-2:** `docs/api-surface.md`'s `F-CHT-3` routing is discharged by the Part B ruling, ⟨SUPERSEDED⟩ beneath rather than over. **FR54-1's residual:** discharged at `44166542`. **FR54-2's five:** **all five now read `10,387 / 4,459`** and `client/SIZE.md`'s ledger agrees with `tools/minify` exactly; two of them record the whole path `4,429 → 4,421 → 4,459` with the commit for each move, which is more than was asked. **This row's own claim — "all five still carry 10,391 / 4,429 against a shipped 10,306 / 4,421" — is now false in both halves**, and the reproduce block that asserted it is corrected at §7.11 correction 3. The row as written at revision 5 follows, unedited: **New at revision 5**, all four verified present by PM-1 at `e751f6de` (§2.8 rows 7–8). **R-1:** `bench/README.md:670`–`:684` gives *"three independent reasons, any one of which is enough"* that `F-CHT-3` is inexpressible, and **reason 3 has been false since `2ab18690`** — the conclusion survives on reasons 1 and 2, so no verdict moves, but "three independent reasons" is now two and **the one that died is the one this landing fixed**. **R-2:** `docs/api-surface.md:719` routes the `F-CHT-3` consequence as *"a finding for PM-1"*; **that routing is discharged by the Part B ruling** and the row should say so beneath itself. **FR54-1's residual:** `docs/guide/deploying.md:54`–`:56`. **FR54-2's five:** `README.md:113`, `docs/guide/deploying.md:24` (*"re-measured on every landing"*, self-refuting), `docs/quickstart.md:161` (the probe table a reader compares their terminal against), `docs/guide/inspector.md:198`, `docs/instrumentation.md:835` — all five still carry **10,391 / 4,429** against a shipped **10,306 / 4,421** | **BENCH-1** (R-1); **DEV-1** (R-2's ledger row); **DEV-2** (`deploying.md:54`–`:56`); **DEV-3** (the five size figures, and `docs/quickstart.md:161` is the one a reader hits) | **No** — none of them is a term of box 3, and L9-1 routes rather than blocks on them. **But `docs/quickstart.md:161` is on the page box 2's Q-1 and Q-2 are already blocking on**, so it is cheap to fix in the same pass and expensive to leave |
| **The empty-inspector-panel mechanism is stated wrong in three places and one of them is this report** | **New at revision 5, DEV-2's, found by trying to reproduce `0c711b70` as a mutation control and failing.** `(globalThis.rAF \|\| setTimeout)(...)` **does not throw "Illegal invocation"** in Chromium 151 — `requestAnimationFrame` is on the `[Global]` `Window` interface and Web IDL defaults an undefined `this` there to the global object. **The empty panel was real; the diagnosis was not.** The claim sits in `client/inspector.js:639`, `client/SIZE.md:696` and **this report's own §4.5**. Same shape, same reporter: **§4.5 and `client/SIZE.md:696` also quote a transcript line, `CLIENT_EVENT event:counter.increment ← #2`, that cannot have come from `examples/counter`** — that reducer's transition changes no session state and is suppressed, so the browser sees the store's broadcast (`↓ patch 2 seq 2 · v2 EFFECT effect:counter.watch`, `client_ref` 0) | **PM-1's copies are CORRECTED at §4.5.1** and are not carried. **DEV-2** for `client/inspector.js` and `client/SIZE.md` | **No** — neither sentence is load-bearing for a tick, and mutation control **A** now reproduces the *symptom* from the tree side without needing the diagnosis. **Recorded because two claims that were never re-derived is a method finding, not a typo** |
| ~~**PRD v1.5 is owed, for the Part B ruling**~~ | **→ CLOSED at revision 6: PRD v1.5 lands with this revision**, and it is more than the deferral predicted. It amends FR-54's clause-1 helper vocabulary from four `Bind` fields to **six**; records the **refusal** of the full modifier set and its **three-limbed re-open trigger in the requirement itself**, because a refusal that lives only in a review is the thing clause 3 exists to prevent; closes the three-failure grading block with the original kept beneath; **discharges QA-1's Q-7**; and **ticks box 3, taking Phase 4 to thirteen of thirteen**. **The deferral was defensible and it was still a deferral, and it cost one round in which the requirement document graded FR-54 against a tree two landings old** — which is exactly what Q-7 is. The row as written at revision 5 follows, unedited: **New at revision 5, and it is PM-1's.** PRD FR-54's grading text says of failure 1 that *"both halves have real arguments for refusal"* and cites `docs/api-surface.md:615` and `bench/README.md:553` for it. **L9-1 has now ruled that both of those arguments are aimed at the wrong target**, and the two citations drifted to `:719` and `:670` (§2.6 row 7). **No box moves on this** — FR-54 is NOT MET either way — which is exactly why it is filed rather than done in the same pass as a grade. **The natural landing is with FR54-6**, when the accepted surface exists and the requirement can record what closed failure 1 rather than what was decided about it | **PM-1** | **No.** *Filed with its own risk stated: this report's §7.10 records three corrections that exist because it deferred something, and this is a deferral. What makes it defensible rather than habitual is that the amendment has an artifact to wait for and a named landing to wait until* |
| ~~**Failure 2 has not been driven in a browser**~~ | **CLOSED at `97ab20fb`, and it is the third-oldest thing in this table to close.** §5.6 derived the shared-debounce consequence from three source files and said an observation is worth more than a derivation. **QA-1 drove it** in Chromium against the real shipped runtime and the real helpers, on markup rendered from the helpers with no hand-written attribute string, measured at server-side arrival, with **three negative controls including a mutation control that turns three of eight specs red**. **Verdict: REPRODUCES**, and it enlarged the finding in three ways and narrowed it in one. [`docs/qa/fr-54-debounce-repro.md`](../qa/fr-54-debounce-repro.md) | **QA-1**, closed | **No — closed, not carried.** The API decision it was protecting is still open and is **L9-1's under FR-65** |
| ~~**`examples/chat`'s F-3 outlived its feature**~~ → **the same sentence is still live in the example's own source** | ~~New at revision 3, PM-1's~~ → **half discharged at revision 4.** DEV-3 corrected `FRICTION.md` F-3 at `e1a56a0e` — both false sentences quoted and corrected beneath themselves, the number and conclusion kept, and a *"— Closed."* heading **declined** with the right reason: F-1 and F-4 closed because the feature arrived *and* the specs weakened by its absence now do what they were written to do, and here the symbol arrived and the affordance did not. **What is not discharged: `examples/chat/view.templ:64`–`:68` still reads *"live.On has no key filter … Escape-to-clear has no expression at all"***, and `view_templ.go:188`–`:192` carries the generated copy. **It is now in the worse of the two places** — the copy a reader of the example reaches by construction | **DEV-3** to fix the comment (`gen.sh` carries the generated copy); **QA-1** to say whether it disturbs their box-6 grade — **PM-1 is not reversing a grade the gate owner made.** *(DEV-3 reports both sites and leaves them as another file's; PM-1 does not agree — `examples/**` is DEV-3's by the role list — but `examples/**` is outside PM-1's write scope, so this is routed rather than ruled)* | ~~**YES.** A documentation phase may not exit while a shipped example tells a reader the library cannot do something it does~~ → **CLOSED at `b6bfe108`, and it closed the way this row said it could not be closed cheaply: by building the affordance.** The comment at `view.templ:64`–`:73` now reads *"F-3 is now closed. Escape-to-clear is implemented, below … the two do not interfere"*, `view_templ.go` carries the generated copy, and `FRICTION.md` F-3 takes its `— Closed.` heading **with the argument that refused it kept above**. **PM-1 verified all three by reading the paragraph rather than grepping it** (§2.8 rows 2–4), which is §2.6 row 5's own lesson about wrapped Go comments applied to the section that taught it. **No — closed, not carried** |
| **A line-count gate does not exist, and the margin is zero** | New at revision 4. **QA-1's Q-4 and L9-1's §11.6.** No line-count assertion exists anywhere in the tree; `ci.sh` has none; and the samples pin does not hold a count in either direction — QA-1's **M2** (sample gains a counted line) and **M3** (doc block repeats an existing counted line four times) both stayed **green**. **The whole margin is one 84-column call that `templ fmt` is idempotent on**, measured by DEV-1 rather than assumed. **§5.10 authorises the number at ≤31** with four constraints; the gate itself does not exist yet | **DEV-1 or DEV-3** to implement, **QA-1** to verify it fails at 32. **PM-1's half is done** | **No**, on QA-1's marking — but it is the condition that decides whether box 2 *stays* green, and authorising a number is not implementing it |
| ~~**DEV-2's browser loop is not in CI**~~ | §4.4 and §4.5. Both FR-57's and FR-44's end-to-end evidence came from throwaway harnesses that are not committed, so nothing re-runs either and a future change can break the browser half with every spec green. DEV-2 named the home: `test/internal/conformance/`, which has the CDP client. **→ CLOSED at `13a1ca1e`, after four consecutive revisions as the oldest unaddressed item in this report.** `dev_reload_test.go` (FR-57: templ reloads, Go reloads, and the negative control — a no-byte-change rebuild restarts the process and reloads **nothing**) and `inspector_test.go` (FR-44: the panel out of its open shadow root, and the patch's `← #n` **is** the event row's own number). **Three mutation controls, all three killed the owning spec**, and control C left the negative control green while turning both positive specs red. `ci.sh`'s browser step is renamed and its counts **re-derived at this tree rather than carried** — **43 specs pass without `GOTTHLIVE_E2E`, 50 with it**; the previous **22/25 was already stale**, as was the 19/22 before it. **The whole-gate run at `d12870a0` therefore has the browser specs inside it** (`ok 50.369s`), which no previous revision's CI row could say | **DEV-2**, closed | **No — closed, not carried.** *It was a condition through revisions 1–4 and it is discharged* |
| ~~**The unenforced godoc count is stated three ways**~~ | **CLOSED at `e784e49b`, and this row was wrong for two revisions.** §7.1's condition was discharged by the **orchestrator**, not DEV-1, **before revision 2 was written** — `tools/doccheck/main.go:257`–`:264` now carries no figure at all, and says why in the file: the count *"moved from 268 to 269 between two commits of one landing … This line is the tree's."* The printed `NOTE:` is the single authoritative value, which is precisely the remedy §7.1 prescribed. **Revision 2 carried this as an open condition an hour after it closed, and revision 3 nearly did the same.** §7.8 correction 3 | **the orchestrator**, closed | **No — closed, not carried** |
| **`tools/*` is unenforced on itself** | §5.1 condition 3. The godoc gate does not hold its own module, including `doccheck`, to rules 1–2 | **DEV-1** | **No** — a named consequence of a ratified boundary, recorded so nobody rediscovers it as a scandal |
| ~~**F-4's API fix**~~ | **CLOSED at `cd2c4cac`, and it did both things this row said it would.** `(*App[S]).PageHandler` loads through `Config.Init` per request, so it **cannot be given a frozen state** — the hazard is now unwritable rather than documented — and `(*App[S]).Mux` closes the neighbouring pair (the missing `mountPath+"/"` registration, and the `StripPrefix` "repair" that turns the upgrade into a 307 no WebSocket client follows). With `MustNew` and `Config.Init`'s default, **FR-53 went 46 → 39**. L9-1 reviewed all three: **no objection** to `Mux` (*"the strongest of the three… removes a bug class"*) or `PageHandler`, **KEEP** on `MustNew` | **DEV-1**, closed | **No — closed, not carried.** Revision 2 called it *"the highest-value single item in this list"*; it was, and it moved the measurement without closing the box |
| **`PageHandler` calls `Config.Init` on every page request, and a shipped example's `Init` breaks the rule** | New at revision 3, **L9-1's**, found reviewing the three symbols. The godoc states the trade and gives the rule — *"An `Init` that is not safe to call for a read should not be mounted here"* — but **`examples/dashboard`'s `Init` registers into a shared map** (`dashboard.go:416`), which mounted through `PageHandler` would register a **zero-`ID` subscriber on every page load** with no `Teardown`. **Severity low and L9-1 says why as clearly as the finding**: no shipped example mounts `PageHandler`, and the zero-`ID` key collides into one map entry rather than growing. It is a **documentation-cohesion item**, not a library defect — the quickstart teaches this mounting pattern while the most realistic example has exactly the `Init` it forbids, and neither mentions the other | **DEV-3** (a sentence where the pattern is taught) **or DEV-1** (name the case in `PageHandler`'s godoc). Either fixes it; both is redundant | **No** |
| **`docs/api-surface.md:529` reads as though `MustNew`'s cut argument is still standing** | New at revision 3, **L9-1's** §1.6. The table marks `MustNew` `stable`; the ledger row records it as cuttable on weak justification. Both were true while the question was open; **it is closed now and `stable` is the true one.** L9-1 was blocked from that file | **DEV-1** — add the ruling reference to the `:529` row. No change to the symbol or its marking | **No** |
| **`bench/README.md:421` describes a state of `docs/OPERATOR-QUESTIONS.md` that ended on 2026-08-04** | New at revision 3, **PM-1's**, and it is a carried item discharged by re-deriving rather than by re-reading. **Q-D** says that file *"has Q-1..Q-7 and no bench series"* and that **Q-E** awaits PM-1's ratification. **Both were true when written and neither is now**: `Q-BENCH-1` and `Q-BENCH-2` were added at PRD v0.6 row 2, and **Q-E was ratified at v0.6 row 3** with FR-70 amended to match. §2.5 row 11 | **BENCH-1** (`bench/**` is outside PM-1's write scope) | **No.** Two dangling-reference notes that no longer dangle |
| **`docs/bench/data/g2-baseline/README.md:3` links to a file that does not exist** | New at revision 3, QA-1's **F-11**: the link resolves to `docs/bench/data/g2-baseline.md`; the file is at `docs/bench/g2-baseline.md`, one level up. QA-1 states it is **outside FR-59's nine subjects**, so it is explicitly not part of box 8's condition | **DEV-2** (bench artifacts). Low | **No** |
| ~~**Three stale repeats of F-2's false sentence**~~ → **two remain, and one of the two is the one my own row failed to name** | The claim "the router strips the prefix before the handler sees a request". **Closed at revision 2:** `live/example_test.go:153` (`2ab0cd57`) and **a fourth copy this row never knew about** — `live/app.go`'s exported godoc for `App.Handler`, *the very method the claim is about*, which my §6 grep missed because the phrase wraps across two comment lines (`4d28146f`). **Still present, both verified by PM-1 at HEAD:** `docs/api-surface.md:665` in the C-23 changelog row, left alone deliberately; and `docs/reviews/checkpoint-2-batch.md:246`, **which is the third copy this row's title counted and its body never named** | `api-surface.md` and `checkpoint-2-batch.md`: their owner's call — both are historical records of a ruling's reasoning | **No.** DEV-3 fixed the reader-facing copy, DEV-1 fixed the two in shipped godoc, and what remains is in two archives. **The lesson is the row itself: a carried item whose title counts three and whose body names two is how the third goes missing, and the copy in the exported godoc was found by DEV-1 reading rather than by my grep** |
| ~~**`live/doc.go`'s "# Status" says the examples "are not here yet"**~~ | **Closed at `2ab0cd57`.** The section now says three examples ship and are CI-tested. DEV-1 also **removed** the "error-boundary component model" claim rather than restating it — PM-1 verified the phrase appears nowhere in the tree and no requirement asks for one | **DEV-1**, closed | **No — closed, not carried** |
| **`live/doc.go:88–89` says `Config.Dev` does one thing; `live/config.go:141` says it does three** | New at revision 2, **PM-1's, found while checking DEV-3's item 4.** Both are godoc for the same field in the published module. §7.3 | **DEV-1** (`doc.go`), **DEV-3** (`quickstart.md:302`, the third instance) | **No**, but it is the one carried item that a fresh green gate does not catch: doccheck asserts a doc comment is **present**, not that it is **true** |
| ~~**The Phase 3 resync box**~~ | `1b16f4a9` appears to meet all three conditions checkpoint-3 §5.3 set, and no PM-1 gate act has re-held the box. Phase 3 stays open until one does. **→ CLOSED at `f0690a2c`. The gate act was held**, in a Phase 3 record ([`gates/checkpoint-3.md`](checkpoint-3.md) §12, at `713a3192`), and **PHASE 3 EXITS — seventeen of seventeen**. Held by **re-running the measurement six times**, three of them on a pristine `git archive` export, diffed programmatically rather than by eye; **every published byte figure identical 101 commits after it was taken**, and identical again at `2ab18690` after a commit re-encoding rendered markup landed mid-act; the method paragraph checked against **`examples/dashboard/resync.go` at HEAD** rather than against the commit body. **One published latency figure (`max 579µs`) did not reproduce and has its own section (§12.3) rather than a footnote** — it is the low outlier of eight runs, and the README predicted its own irreproducibility before anybody re-ran it. `docs/PRD.md` v1.4, `gates/checkpoint-3.md`, `pm/checkpoint-3-closure.md` and `docs/README.md` moved together. §4.6.2 | **DEV-3** (the remedy, done) + **PM-1** (the gate act, done) | **No — closed, not carried.** *It never blocked Phase 4, and this report did not tick another phase's box in passing: it held that phase's gate in that phase's record* |
| ~~**`docs/README.md` has no row for this report**~~ | **Closed at `f34ef2ca`.** DEV-3 added a **"The record"** section indexing four gate reports, with phase-4's row reading *"the current state of this tree… read this one first if you want to know what is not finished"*, and filed them **outside** "For the curious" on the ground that a gate report grades rather than argues. Rows for `guide/security.md` and `guide/deploying.md` landed in the same commit | **DEV-3**, closed | **No — closed, not carried** |
| **F-6's `doc.go` mirror, declined** | DEV-3 fixed F-6 by changing where the Godoc row sends a docs-only reader, and explicitly declined to mirror per-symbol contracts into a second place. Recorded so the decline is visible rather than looking like an omission | **DEV-3**, closed | **No — closed, not carried** |
| ~~**E-2's sample still logs from inside a reducer**~~ | **CLOSED at `091dbae8`**, by DEV-3, **verified in the tree by L9-1** rather than accepted from a summary: the reducer performs no I/O, every `slog` reference is in `Reporter`/`Reporter.Execute`/`WireLogging`, and the executor is wired to `Config.Execute`, which is the actor boundary FR-16 names. **DEV-3 fixed two further defects the same walk turned up** and made the page **name the deviation as E-2 and link the register** — which is what made "then delete this row" impossible to honour, and is why L9-1 calls it *"a fix that makes the register harder to erase"* | **DEV-3**, closed | **No — closed, not carried.** It was revision 2's condition and it is discharged |
| ~~**`docs/exceptions.md` is drafted and every sign-off line is unsigned**~~ | **CLOSED at `bdf91971`.** L9-1 reviewed, **re-walked** and signed. E-1 **ACCEPTED** with the scope ruling **REFUSED**; E-2 **CLOSED as fixed and RETAINED**, overturning §4's "then delete this row"; §3's six readings **AGREED with two extensions**. Three of the register's own numbers were corrected before signing, including that its walk commands did not print its own counts — *"the entire re-walkability guarantee"*. **Rows without a disposition: zero** | **L9-1**, closed | **No — closed, not carried** |
| **The register must be re-walked at Phase 5** | New at revision 3, from L9-1's standing requirement (`docs/exceptions.md` §7.5) and §5.7's split. Re-run §1.2 against the shipped tree, state the three counts, and **if any differs from 17 / 31 / 11, say which directory moved before saying anything else** | **DEV-1** to walk, **L9-1** to sign | **No for Phase 4 — YES for Phase 5**, and it is now a box of its own in PRD §6 rather than half of Phase 4's thirteenth |
| **The call-graph purity check is not commissioned, and that is a decision** | New at revision 3. `docs/exceptions.md` §5 proposed an `internal/arch` lint against `log/slog` in a file declaring `Reduce`; **L9-1 declined it because it fires on E-2's own fix** — DEV-3 put the branching reducer and the logging executor in one file *because that adjacency is the lesson*, so satisfying the lint means splitting a teaching file or putting a suppression comment in a block readers are invited to copy. The honest alternative is named: **call-graph reachability** from each `Reduce` and `Fragment.Render` into `log/slog`, `net/http`, `database/sql`, `os`, `time.Now`, `math/rand`. **Nothing mechanical currently stops E-2 recurring** — the samples suite is a *synchronisation* guard, and would dutifully update the page to display the mistake | **Unassigned by choice**; **QA-1's** if picked up, with its negative control demonstrated against E-2's pre-fix commit first | **No.** Recorded so nobody re-proposes the file-level version as an oversight |
| **The `ErrSessionSaturated`/`ErrSessionClosing` export ruling** | New at revision 2. Both live under `internal/`, so an application cannot `errors.Is` them; five call sites handle the pair with a comment and no branch. §5.5 rules that FR-55/FR-56's precedent does **not** dispose of it and sends it to the Phase 5 api-surface gate with a stated test | **PM-1** to carry into the FR-65 gate; **DEV-1 or DEV-2** to land the form **L9-1** approves | **No** — FR-58 is satisfied without it, by DEV-1's own grading, so no Phase-4 box turns on it. It binds at Phase 5's FR-65 box |
| **ADR-001 §5 row F1 describes client behaviour the client does not have** | New at revision 2, DEV-3's finding, **verified by PM-1**: the row promises a console message and `data-gotth-status="offline"`; `client/runtime.js` writes only `connecting\|live\|reconnecting\|closed` and contains **zero** `console.` calls. §7.4 item 1 | **DEV-2** to state what the runtime does; **L9-1** owns the ADR's text | **No**, but it is the highest-severity of the six: it is a **failure-mode** row in an L9-1-approved design document, and it is what an operator reads when the upgrade is being stripped |
| **ADR-001 §4.3 says compression "is exposed as an option"; it is not** | New at revision 2, DEV-3's finding, **verified by PM-1**: `internal/wsx/handler.go:232` hard-codes `websocket.CompressionDisabled` and no `Config` field or `wsx.Options` field reaches it. ADR-001 **O3** already tracks whether to enable it after Phase 5 measures R-9, so the option's absence is consistent with everything except that sentence. §7.4 item 2 | **L9-1** (the ADR), with **DEV-1** if the answer is to expose it | **No.** It is one sentence, and O3 is the place the decision actually lives |
| **A `Config.CSRF` rejection is counted as `forbidden_origin`** | New at revision 2, DEV-3's finding, **verified by PM-1**: `internal/wsx/handler.go:175` and `:188` both emit `protocol.CloseForbiddenOrigin.Label()`, so a CSRF failure and an origin-allowlist failure are indistinguishable in the metric. **This is a code defect, not a docs contradiction**, and it degrades exactly the signal an operator uses to tell a misconfigured allowlist from an attack. §7.4 item 3 | **DEV-1**, with **QA-1** on whether it needs a close code of its own | **No**, but it is the only one of DEV-3's six that is a live instrumentation defect rather than a sentence |
| **The client runtime is cached for a year from an unfingerprinted URL** | New at revision 2, DEV-3's finding. `live/templ.go:493` sets `public, max-age=31536000, immutable` and `live.Script` renders a fixed `src` for every build. **Not a contradiction — `guide/deploying.md` documents it**, names the two levers (change the mount path, or rewrite `Cache-Control` at the proxy) and says there is no fingerprinted-URL option. It is a disclosed API limitation. §7.4 item 5 | **DEV-1** (surface), **PM-1** if it needs a backlog entry | **No.** A limitation a page states before an incident is not debt in the sense §6 uses |
| **There is no per-session close** | New at revision 2, DEV-3's finding. `App.Close` drains every session; nothing on `Session` closes one. A revoked user's idle session persists until their next event (`FatalDenyError` → `4006`) or `Limits.IdleTimeout`, default **30 minutes**. **Not a contradiction — `guide/security.md` documents it**, with the mitigation (check freshness in `Authorize`) and the bound. §7.4 item 6 | **PM-1** to file or refuse a backlog entry; **DEV-1** if it becomes surface | **No**, for the same reason as the row above |
| **No CI job runs G11** | New at revision 2, DEV-2's **F-3**. The library job runs `ci.sh` inside `docker run` with no docker socket, so `ci.sh:876` announces a skip there as it does under `dis run`. `ci.sh`'s header now says so; DEV-2 corrected it, since it had claimed the workflow skips nothing. The fix is four lines beside `docker build` | whoever owns `.github/workflows/` | **No for Phase 4 — YES for Phase 5's G11 box**, and §4.7.1 argues the split rather than assuming it: the Phase-4 box asks whether the property holds and it does, measured; a release box may not close on a dated run of a check nothing re-runs |
| **`docs/guide/deploying.md`'s sizing figure is 81 commits old** | New at revision 2. **45,769 B** at N = 1000, measured at `d66e4953`, quoted with §9.10.9 attached. The page states the staleness correctly and does not quote a commit count; **PM-1 measured the distance at 81** (`git rev-list --count d66e4953..HEAD`), against the 68 DEV-3 reported at handoff, which reproduces at no tree in this landing | **QA-2**, at Phase 5, where G2 is enforced | **No.** The page is not where that number is re-measured, and it does not pretend otherwise |
| **ADR-001 criterion X6 has never been run** | New at revision 2. *"Upgrade succeeds through this repo's Caddy edge with no Caddyfile change"* names a Phase 2 integration test as its evidence; no artifact records it. `4a40ed48` states the limit on the deployment page in DEV-3's own words | **QA-1 or QA-2** | **No**, but it binds before Phase 5 quotes the proxy guidance as observed rather than derived |

**Open by design, and not debt.** The inspector's bytes being embedded in
production binaries (§4.5) is a disclosed design position, stated in three
documents, not a gap. `live/livetest`'s harness half being unable to carry godoc
examples (§4.11) is a property of `testing.TB`, not a coverage hole.

**Revision 2's accounting of this table, so it can be checked against revision
1's.** Three rows closed (`docs/README.md`'s missing row, `doc.go`'s "# Status",
and two of the four F-2 repeats); one row rewritten because it had miscounted
itself; **eleven added**, of which **two are conditions on Phase 4's exit** —
E-2's unfixed sample, and `docs/exceptions.md`'s unsigned state, which is box 13
anyway. Everything else is carried with an owner and a home. **The three
conditions revision 1 carried are unchanged and none of them was addressed this
round:** the seven open boxes, DEV-2's uncommitted browser loop, and the godoc
carve-out's three-valued count.

**Revision 3's accounting.** **Three rows closed and all three were conditions**:
E-2's sample, the register's unsigned state, and F-4's API fix — **the two
conditions revision 2 added are both discharged**, which is the first time in
this report's history that a revision closes more conditions than it opens.
**One row rewritten** (the open-boxes row, seven → two). **Nine added**, of which
**three are conditions on Phase 4's exit**: FR-54's three failures (box 3), and
`examples/chat`'s F-3 outliving its feature. **Of revision 1's three original
conditions, two close and one does not.** The open-boxes row shrinks from seven
to two. **The godoc carve-out's three-valued count turns out to have closed at
`e784e49b`** — an hour *before* revision 2 was written, which carried it as open
anyway, as this revision's own draft nearly did (§7.8 correction 3). **What is
left is DEV-2's uncommitted browser loop, untouched for a third consecutive
revision**, and it is worth saying plainly rather than leaving in a table: **it
is now the oldest unaddressed item in this report, it has never been anybody's
turn, and §5.6's failure 2 is the third finding here that a browser would
settle.** §8.2.

**Revision 4's accounting.** **Two rows closed and one of them was a condition:**
failure 2's browser drive, and the FR-53 half of the F-3 row *(the row itself
survives, rewritten, because the sentence moved rather than went)*. **Two rows
rewritten** — the open-boxes row, two → one, and the F-3 row. **Two added, and
one of the two is a condition**: box 2's **Q-1 and Q-2** block on
`docs/quickstart.md`, while **Q-3** and **Q-4** do not, on QA-1's own marking;
and the missing line-count gate, which is not blocking and is the thing that
decides whether box 2 stays green.

**Two things this accounting should say plainly rather than leave in a table.**
**First: the conditions on Phase 4's exit are no longer mostly PM-1's or DEV-1's
— they are DEV-3's, and they are all on one page.** Q-1, Q-2 and the F-3 comment
are three edits to two files, none of which can move the count, and QA-1 says so
for each rather than leaving the fear of the budget to defer them. **Second:
DEV-2's uncommitted browser loop is untouched for a FOURTH consecutive revision
and it is still the oldest item here** — and this revision is the one that takes
the excuse away, because **§5.6's failure 2 was the third finding a browser would
settle and a browser settled it**, in a harness QA-1 wrote in the conformance
suite's own idiom, ran out of `/tmp`, and explicitly did **not** commit. **The
harness that would discharge the oldest condition in this report has now been
written twice by two different agents and thrown away both times.** §8.3.

**Revision 5's accounting.** **Three rows closed and two of the three were
conditions**: `examples/chat`'s F-3 comment (a condition since revision 3),
**DEV-2's browser loop (a condition since revision 1 and the oldest item in this
table)**, and the Phase 3 resync box (never a Phase 4 condition, and closed by a
Phase 3 gate act). **Two rows rewritten** — the open-boxes row, which does **not**
change its count, and FR-54's three failures, of which two are now struck and one
is not. **Seven added, of which three are conditions on Phase 4's exit**: L9-1's
FR54-3, FR54-4 and FR54-6 (filed as one row, because they discharge individually
and block jointly), the accepted-but-unbuilt `NoModifiers`/`PreventDefault`
surface, and **QA-1's ungiven grade of the FR-54 batch**. The other four are
routed with owners: L9-1's four findings in other people's files, the
empty-panel-mechanism correction, and **PRD v1.5, which is mine and which I have
filed rather than done**.

**Three things this accounting should say plainly rather than leave in a table.**

**First: the oldest item in this report is gone, and *why* it went is worth more
than the fact that it did.** Revision 3 called it the oldest unaddressed item;
revision 4 said *"It is not that nobody can write it. It is that nothing forces
anybody to keep it."* **Nothing about the difficulty changed between revision 4
and revision 5.** Two agents had already written a working harness; both threw it
away, and both were right to — committing somebody else's regression gate is not a
reproduction document's call, and QA-1 said so explicitly. **What changed is that
the harness landed in the one place that forces its own retention**:
`test/internal/conformance/`, inside `ci.sh`'s browser step, with the step's own
spec counts re-derived so that **the count is itself a check** — and the
re-derivation immediately caught that the numbers on the page (22/25, and 19/22
before them) had been stale through two landings. **A condition that four
revisions of naming did not move was moved by putting the artifact somewhere that
already fails when it goes missing.** That is the generalisable finding and it is
against this report's method: **this report has been treating "named as a
condition" as though naming were a mechanism, and it is not one.** Every row still
open above should be read in that light. Q-1 and Q-2 are named, and four revisions
of naming has not graded them either.

**Second: the count did not move, and three of the four new conditions belong to
one engineer.** FR54-3, FR54-4 and FR54-6 are all **DEV-1's**, all in `live/**`
and `client/**`, and all specified to the line by a reviewer who put the fixtures
they ran into the review so nobody has to invent them. The fourth, QA-1's grade,
is **an ask rather than a piece of work.** So Phase 4 is blocked on **one
engineer, one grader and one page** — the narrowest this phase has been, and not
the same thing as nearly finished. **The distance from here to a tick is one
landing against nine pre-registered constraints, two smaller fixes, and two
grades. None of those is started.**

**Third, and it is against this report rather than about it: two of the seven new
rows exist because a sentence in this document was never re-derived.** §4.5's
"Illegal invocation" mechanism and its `CLIENT_EVENT` transcript line were both
taken from a commit body at the moment of a fix and repeated for four revisions.
**Neither was caught by a reader. Both were caught by an engineer running a
control that was meant to confirm them.** §7.5 and §7.10 already record that this
report's corrections mostly arrive from somebody grading against it; these are the
sixth and seventh, and the first two to arrive from somebody **executing** against
it. **The remedy is the same one that closed the oldest row — a standing spec —
and not a more careful sentence.**

---

## 7. What I found at the gate

*(§7.1 and §7.2 are revision 1's. §7.3 onward are revision 2's.)*

### 7.1 A 268-symbol carve-out is justified by a number the tree states three ways

**PM-1, and it is DEV-1's to close.**

The unenforced godoc count is the number that makes §5.1's ratification auditable
— it is what a future reader checks to see whether the exception has quietly
grown. The tree gives it three values:

- **254**, in the code comment at `tools/doccheck/main.go:254` (*"Listing 254 of
  them would bury the enforced ones"*). That the line number and the figure
  coincide is an accident, and it made this harder to notice rather than easier.
- **268**, in `1370229c`'s commit body, which is the measurement of record and
  arithmetically consistent with the rest of that body: 142 enforced + 268
  reported = **410** tree-wide, of which 359 are struct fields.
- **269**, reported by DEV-1 at handoff, **reproducible from no file in this
  tree**.

**The drift is explicable, which is the part that makes it worth a numbered
item.** `docs/guide/_samples/mounting/` — four new files, `scopeReported` — landed
in DEV-3's remediation *after* doccheck was written, so the live number is a
moving target by construction and the three quotations are plausibly three
correct readings at three moments. **That is precisely why one of them has to be
authoritative and none currently is.** No committed artifact records a doccheck
run, no test asserts the count, and the only place it is true is a CI log nobody
has quoted.

**This is the house defect class in its documentary form** — a document asserting
a number nobody re-derived — and checkpoint 3 §10.1 caught the same disease in a
sentence rather than a figure. The fix is small: make the code comment quote no
figure at all (the printed line already carries the live one), and cite
`1370229c` for the measurement of record.

> **CLOSED at `e784e49b`, and this section did not notice for two revisions.**
> The orchestrator took the prescription above — `tools/doccheck/main.go:257`–
> `:264` now quotes **no figure at all**, and says in the file why it does not:
> the count *"moved from 268 to 269 between two commits of one landing … This
> line is the tree's."* The printed `NOTE:` is the single authoritative value.
> **`e784e49b` landed an hour before revision 2 was written and revision 2
> carried this as an open condition anyway**; §7.8 correction 3 is that finding,
> and it is about method rather than about doccheck.

### 7.2 The PR body's Phase-4 status line has been false since `cac72589`

**PM-1, and it is why deliverable C of this turn exists.**

The PR body's status quote block says this PR goes ready-for-review only after
the docs gate is held, *"which has not happened"*. It happened, at `cac72589`,
and it passed. The Phase-4 table row says the gate is unheld.

**The interesting part is not the staleness; it is which direction it runs.**
Checkpoint 3 §10.1 found a PRD bullet that had gone false *in the flattering
direction* and noted that a sentence unkind to us attracts no second reader. This
one is the same mechanism producing the opposite error: the body under-reports a
result we earned, and **that is equally a defect**, because a body that
misdescribes the project in either direction is a body a reviewer cannot use to
navigate. Both instances have the same cause — a claim written once about a
moment and never re-derived — and the same remedy, which is that the row is
rewritten when the thing it describes changes rather than when somebody notices.

The replacement text is in [`docs/pm/pr-body-phase-4.md`](../pm/pr-body-phase-4.md),
written for the orchestrator to apply with `gh pr edit`. It names the PASS, the
46-line miss, the 268 unenforced symbols and the fact that Phase 4 does not exit,
because a row that reported only the PASS would be this defect a third time.

### 7.3 The godoc gate went green over a doc comment that is false, and it was always going to

**PM-1, revision 2, found while checking DEV-3's fourth item.**

Two doc comments in the published module disagree about the same field:

- **`live/config.go:141`**, on `Config.Dev`: *"It does three things, and nothing
  else"*, followed by three numbered sections — panic detail in the `Error`
  frame, the dev session inspector, and dev reload.
- **`live/doc.go:88–89`**, the package's own front page: *"[Config.Dev] is what
  decides how much of a panic reaches the browser, **and it is the only thing
  that field does**."*

The charitable reading of the second is that it is scoped to the error-boundary
section it sits in — and the sentence immediately after it has to re-scope to
*"everything else about the boundary"*, which is the tell that the first one did
not. **As written it says the field does one thing, and the field does three.**
(The claim wraps across `doc.go`'s lines 88 and 89, which is the second time in
this landing that a false sentence in a Go comment survived a `grep` by being
folded in half. §7.5.)

**DEV-3 found the same claim in a third place** and reported it as one of their
six: `docs/quickstart.md:302`, in §2's *"The security defaults, and the four ways
out"*, says *"`Dev` is a fourth thing to remember to turn off, and it is not an
escape hatch: it puts a panic value and its stack in the error frame the browser
receives."* **The refinement PM-1 adds is that the quickstart contradicts itself
inside one section**: its own `Config` table at `:256` says *"One switch, three
dev-only things"* and lists all three. So the wrong sentence is the one in the
**security** subsection — which is the paragraph a person hardening a deployment
reads, about the field that most needs to be right there.

**The reason this is a §7 finding rather than a §6 row is what it says about a
gate that went green in the previous revision.** FR-66's box ticked at v0.8 on
`tools/doccheck`, which enforces that every exported symbol **has** a doc comment.
It cannot enforce that the comment is **true**, and nothing else does either —
`docs/guide/_samples`' drift suite holds the guide's code blocks against compiled
sources, and there is no equivalent for prose about a field. **142 doc comments
were written in one landing** (`9cce6829`), which is exactly the condition under
which a wrong one is cheapest to introduce and hardest to notice. **I am not
proposing a gate for this** — I do not know what would catch it that is not a
person reading — but a project that has now caught six checks that could not fail
should write down that its newest one has a class of defect it does not address.

**Owner: DEV-1** for `live/doc.go`, **DEV-3** for `docs/quickstart.md:302`.
Carried, not a condition.

### 7.4 DEV-3's six items, checked one at a time — four stand as stated, two are not contradictions

**PM-1, revision 2.** DEV-3 handed over six "code-versus-documentation
contradictions", found while writing the deployment and security pages, none in
their own paths, and did not fix any of them. **I read each named file and line
rather than recording the list.** Four confirm as stated; **two are not
contradictions at all, and reclassifying them is the point of this section**,
because "contradiction" implies a documentation fix and those two imply a scope
decision.

| # | DEV-3's item | Verified? | What it actually is |
|---|---|---|---|
| 1 | **ADR-001 §5 row F1** promises the client surfaces `gotth-live: upgrade refused (status N)` in the console and applies `data-gotth-status="offline"` | **CONFIRMED, and worse than stated.** `client/runtime.js` documents its status vocabulary at `:50` as `connecting \| live \| reconnecting \| closed`; there is no `offline`, and `grep -c 'console\.' client/runtime.js` returns **0** — not one call in the file | A **real contradiction**, in the **failure-mode table** of an L9-1-approved design document, describing the diagnostic an operator gets when an intermediary strips the upgrade. Highest severity of the six |
| 2 | **ADR-001 §4.3** says `CompressionNoContextTakeover` *"is exposed as an option, off by default"* | **CONFIRMED.** `internal/wsx/handler.go:232` hard-codes `websocket.CompressionDisabled`; no `Config` or `wsx.Options` field reaches it | A **real contradiction**, and a narrow one: ADR-001's **O3** already tracks whether to enable it after Phase 5 measures R-9, so everything except that one sentence is consistent |
| 3 | **A `Config.CSRF` rejection is counted under `forbidden_origin`** | **CONFIRMED.** `internal/wsx/handler.go:175` (origin allowlist) and `:188` (CSRF) both emit `protocol.CloseForbiddenOrigin.Label()` | **Not a docs contradiction — a code defect.** Two different refusals are indistinguishable in the metric, so an operator cannot tell a misconfigured allowlist from a CSRF failure, which are different incidents with different responses |
| 4 | **`docs/quickstart.md` §2 describes `Config.Dev` as doing one thing; `live/config.go` documents three** | **CONFIRMED, and refined.** The wrong sentence is at `:302`, in §2's *security* subsection; §2's own `Config` table at `:256` says three and lists them. So the page contradicts **itself** as well as `config.go` | A **real contradiction**, and §7.3 adds a third instance in `live/doc.go:88–89` that DEV-3 did not have |
| 5 | **The runtime is served `immutable` for a year from an unfingerprinted URL** | **CONFIRMED as fact.** `live/templ.go:493` sets `public, max-age=31536000, immutable`; `live.Script` renders `src="<mount>/gotth-live.min.js"` for every build | **Not a contradiction. `guide/deploying.md` documents it** — the consequence, the two levers (change the mount path, rewrite `Cache-Control` at the proxy), and *"there is no fingerprinted-URL option on `live.Script`, and this page would rather say so than let you find out during an incident."* A **disclosed API limitation** |
| 6 | **There is no per-session close** | **CONFIRMED as fact.** `App.Close` drains every session; the only exported close is `(*App).Close`. A revoked user's idle session persists until their next event (`FatalDenyError` → `4006`) or `Limits.IdleTimeout`, default **30 minutes** | **Not a contradiction. `guide/security.md` documents it**, with the mitigation (check freshness in `Authorize`) and the bound named as the bound. A **disclosed API limitation** |

**Two things this table changes about how the six get handled.**

**The reclassification matters because it changes the owner.** Items 5 and 6 were
reported to me as contradictions, and a contradiction is discharged by editing a
document. These two are discharged by a **decision** — file a backlog entry,
export a surface, or refuse both with a reason — and the person who takes them
believing they are prose errors will find nothing to fix and close them. Items 5
and 6 are in §6 as carried API questions with my name on the ruling.

**Items 1 and 2 are in an ADR that L9-1 approved**, and neither is DEV-3's to
change nor mine. ADR-001 carries verdict APPROVE at review cycle 2; the two
sentences describe behaviour the implementation does not have, and the fix is
either the sentence or the behaviour. **Owner: L9-1 for the ADR's text, DEV-2 for
what the runtime actually does on a refused upgrade.** Item 1 is the one I would
take first: a design document telling an operator to look for a console message
and an attribute that do not exist is worse than saying nothing, because it sends
them looking.

**And the credit where it belongs.** DEV-3 found all six while writing two pages
about somebody else's subsystems, fixed none of them because none was in their
paths, and wrote each with the file and the line. That is the third time this
landing that somebody limited the strength of their own result rather than
rounding it up.

### 7.5 Four numbers in this landing did not reproduce, and the fourth is this report's own

**PM-1, revision 2.** §2.3 of this report says every figure carries the name of
the agent who produced it and the commit it belongs to, and that where a figure
has no such anchor I say so instead of quoting it. Three failed that test this
round. **None of the three changes a verdict**, which is exactly why they are
worth a numbered section: the ones that change a verdict get found.

| Figure | Where | What reproduces | Whose |
|---|---|---|---|
| *"the grep returns **fifteen** lines"* | `docs/qa/g11-clean-clone.md` §1 and §7 F-2, about `grep -n "G11" ci.sh` | **17**, both at HEAD and against `git show 5c751ae9:candace/pkg/gotth/ci.sh`, which is the tree the artifact gates | **DEV-2's**, and it **understates their own work**, which is the direction nobody checks |
| *"the tree is **68** commits past `d66e4953`"* | DEV-3's handoff, about `guide/deploying.md`'s 45,769 B sizing figure | **81** at HEAD; **76** at `4a40ed48`, DEV-3's own last commit. It reproduces at no tree in this landing. **The page itself quotes no count** and says *"the tree has moved since and no re-measurement has been published at HEAD"*, which is the right way to write it | **DEV-3's** handoff, not their page |
| *"**four** call sites handle both with one comment and no branch"* | `docs/error-audit.md` §7.1 | **Five**: `docs/guide/_samples/effects/effects.go:163`, all three `examples/`, and `bench/apps/counter/gotth/store.go` | **DEV-1's** |

**And the fourth is mine.** §6's revision-1 row titled *"Three stale repeats of
F-2's false sentence"* named **two** locations in its body. The third — the one
the title counted and the body never wrote down — is
`docs/reviews/checkpoint-2-batch.md:246`, still present at HEAD. **Meanwhile the
copy that mattered most was in none of the three**: `live/app.go`'s exported
godoc for `App.Handler`, *the method the false claim is about*, which my grep
missed because the phrase wraps across two comment lines. **DEV-1 found it by
reading and fixed it.** A carried item whose count and whose list disagree is a
carried item that cannot be discharged, and the specific failure mode — a `grep`
over a wrapped Go comment — is one this repository will hit again.

### 7.6 Phase 4 cannot exit before Phase 5 begins, by its own box's text

**PM-1, revision 2.** Box 13's own wording, which is mine from v0.8, ends:
*"This box cannot tick before Phase 5, where FR-20 also feeds the stdlib-grade PR
criteria."* DEV-1's `docs/exceptions.md` §0 quotes that sentence back and says the
file does not change it.

**Taken with §6's exit rule — a phase exits when every box is checked — that
sentence says Phase 4 exits after a Phase 5 event.** The phase plan is not
strictly ordered anyway (Phases 1–3 are one consolidated track, and Phase 4's
gate was held while Phase 5 work was in flight), so this is not a contradiction.
**It is a thing nobody has said out loud**, and it means the honest answer to
"when does Phase 4 exit" is not a list of chores.

**I am not resolving it here and I want the reason recorded rather than left to
look like an oversight.** There are two available resolutions and each is a scope
act needing its own argument: split box 13 into a Phase-4 half (the register
exists, is walked, and is signed) and a Phase-5 half (re-walked against the
shipped tree for the stdlib-grade PR criteria), **or** accept that Phase 4's exit
review is convened during Phase 5 and say so in §6's phase plan. **The first is
probably right** and it is a PRD amendment, not a gate outcome, so it belongs in
a landing that argues for it rather than in a re-grade that noticed it.
**Owner: PM-1, before the exit review is convened.**

> **Resolved at revision 3. §5.7 takes the first**, and PRD v1.0 §9 row 3 is the
> amendment. **This paragraph is the record that the argument existed before the
> outcome that made the split decidable** — which is the only reason a scope act
> taken in the pass that benefits from it is defensible.

### 7.7 Three copies of one sentence, found by three different people — a finding about method, not about wording

**PM-1, revision 3.**

**The sentence is E-2's root cause**: `"Log it, count it, branch on it"`, written
once in `live/core.go`'s godoc for `EffectFailedErrorField`, inside a paragraph
about what a **reducer** may render. `docs/exceptions.md` traces the deviation to
it: the guide's sample author read it as an instruction to the reducer, which is
a fair reading of a sentence in that position.

**It existed in three places and each was found by a different person, by a
different method, after the previous one believed the job done.**

| Copy | Found by | How | Fixed at |
|---|---|---|---|
| `docs/guide/_samples/errorhandling/errors.go` — the sample that *acted* on it | **DEV-1**, walking the tree for a file that had never existed | The FR-20 register's walk | `091dbae8` (DEV-3) |
| `live/core.go:246`–`:247` — **the source of the instruction** | **DEV-1**, reading their own fix | Re-reading the comment they had already corrected, and finding the corrected paragraph sitting *below* the sentence it corrects | `0bd5bb40` |
| `docs/guide/effects-and-server-push.md` — a **reader-facing page** nobody's walk had reached | **the orchestrator** | Not a walk. Reading | `368132f6` |

**The finding is not that a sentence was wrong three times. It is that each
search was scoped by what its author was looking at.** DEV-1's register walk was
scoped to code that *performs* I/O in a reducer, so it found the sample and not
the godoc that caused it. DEV-1's own fix was scoped to the paragraph they were
adding, so the sentence above it survived — **the corrective text and the text it
corrects sat adjacent for a full revision of this report**, and a reader stopping
at the first sentence got E-2's exact prompt. And **no walk at all reached the
guide page**, because the guide is prose and the walks were over Go.

**This is §7.5's failure mode with the numbers taken out.** There, a carried item
whose title counted three and whose body named two let the third go missing, and
the copy that mattered most was in none of the three. Here, three correct
searches with three correct scopes left three copies of one sentence, and **the
count was never wrong because nobody ever had a count.** The remedy is not a
better `grep` — §7.5 already recorded that a `grep` over a wrapped Go comment
fails, and this one would have failed differently. **The remedy is that a fix to
a sentence is not done until somebody has searched for the sentence rather than
for the defect**, and that is a habit rather than a gate.

**At HEAD the phrase survives in exactly two places and both are correct**:
`docs/exceptions.md:331` and `guide/error-handling.md:312`, each quoting it as
**the wording that caused E-2**. §2.5 row 3. **Carried, not a condition.**

### 7.8 Three corrections owed to this report's own record

**PM-1, revision 3.** §7.5 is titled *"Four numbers in this landing did not
reproduce, and the fourth is this report's own."* Three more did not reproduce.
**One is a verdict rather than a number, and one is a condition this report has
been reporting as blocking after it was satisfied**, which is worse than either.

**Correction 1 — §2.4's check 5 was wrong when it was written.** The row reads:

> | 5 | **Was E-2's stated root cause fixed?** | Read `live/core.go`'s
> `EffectFailedErrorField` doc comment | **Yes.** "Log it, count it, branch on
> it" is gone …

**It was not gone.** `git show 134e69c5:gotth-live/live/core.go` puts it at
**lines 246–247**, at the tree §2.4 graded, directly above the corrective
paragraph I read and reported. **The conclusion is true today** — DEV-1 fixed the
source at `0bd5bb40` — **and the verification basis was not true then.** §4.13.1
repeated the same claim and is corrected with it.

**And the sentence is wrapped, which is the fourth time that has mattered in this
report.** At `134e69c5` it reads `// … Log it,` on line 246 and `// count it,
branch on it — and render EffectFailedSourceField instead.` on line 247 — so
**`grep 'Log it, count it'` returns nothing at the tree where it was live**, and
the reproduce block has to match either half. §7.5 recorded this failure mode
about `live/app.go`, §7.7 about the same comment, §7.9 about
`examples/chat/view.templ`. **Four instances is a property of the tool.**

**What went wrong is specific and it is not carelessness about the file.** I read
the paragraph the register said had been added, confirmed it said what the
register said it said, and **stopped** — because the row's question was *"was the
root cause fixed"* and the corrective text was there. **The check I did not do is
the one the row's own wording implies: search for the sentence that was wrong,
not for the sentence that replaced it.** A doc comment can contain its own
correction and its own defect at once, and mine did. **DEV-1 found it against
themselves**, in a commit whose body says a reader stopping at the first sentence
*"got E-2's exact prompt — log from the reducer — which is the reading the
guide's sample took"*.

**Correction 2 — this report's closing reproduce block reproduced nothing.** The
`awk` ranges published at revision 2 for FR-53's two counts were `NR>=66 && <=115`
and `NR>=313 && <=346`. Run at HEAD they print **28 and 19 — a total of 47**,
which is neither the 46 the block was written to reproduce nor the 39 that is
true. DEV-1 recomputed the ranges as **72–111** and **314–347**; I ran both and
got **20 and 19**. **The block is corrected at the end of this report.**

**Why this one is worth a numbered item rather than a quiet edit.** A reproduce
block is the part of a gate report that converts a claim into something a stranger
can check, and **a stale one is worse than none**: it invites a reader to run a
command, get a number that matches nothing in the document, and conclude the
document is wrong in some way they cannot locate. It went stale the ordinary way —
the page it indexes moved, seven lines out of the Go block, and line offsets are
the one kind of citation that cannot survive an edit above them. **The general
lesson is the one §7.1 drew about the doccheck count and §7.5 drew about the
commit distance: a document that quotes a position rather than a property has
booked a defect for whenever the position moves.** L9-1 drew the same conclusion
about the register's walk commands this week, independently, and fixed it the same
way — by making the command print the number the document states.

**Correction 3 — a condition this report has carried for two revisions had
already been discharged, and I found it by checking my own claim rather than
repeating it.** §5.1's ratification of the godoc scope narrowing carried three
conditions, the second being *"the figure gets reconciled to one value"* — §7.1,
the count stated as 254, 268 and 269. **It was discharged at `e784e49b`**, by the
**orchestrator** rather than DEV-1, in a commit whose subject line says so:
*"the godoc gate's carve-out stops being justified by a number written three
ways."* `tools/doccheck/main.go:257`–`:264` now quotes **no figure at all** and
explains the deletion in the file — *"it moved from 268 to 269 between two commits
of one landing … This line is the tree's."* **That is the remedy §7.1
prescribed, verbatim.**

**The timing is the finding.** `e784e49b` landed at 11:32; **revision 2 was
written at `134e69c5`, at 12:33, and carried the condition as open** — and §6 of
this revision's own draft carried it forward a third time, because I copied a row
rather than re-deriving it. **I caught it only because §5.8 sent me into
`docs/pm/pr-body-phase-4.md`**, whose §4 — written by the orchestrator, in a file
I own — records the discharge in plain terms. It had been sitting in my own
directory for a full revision.

**Why this one is the worst of the three.** Corrections 1 and 2 are a verdict and
a command that went stale. **This is a condition on a phase's exit that was
satisfied and went on being reported as blocking**, which is the same defect as
§7.2 — the PR body under-reporting a result we earned — in the document whose job
is to say what is blocking. **A gate report that carries a discharged condition
is asking somebody to do work that is done**, and it is a more expensive error
than a stale number because a reader may act on it. The remedy is the one §6's
own preamble already implies and this row proves is not automatic: **a carried
item is discharged by re-deriving it, not by re-reading the row that carries
it.**

**All three corrections are mine.** None changes a verdict in this report: §2.4
check 5's conclusion holds, FR-53's box was open at 46 and is open at 39, and the
godoc box ticked at v0.8 on grounds this condition never touched. **That is
exactly why they are recorded** — §7.5's own sentence is that the figures which
change a verdict are the ones that get found.

### 7.9 The examples' documentation has a second stale layer, and QA-1's pass could not have reached it

**PM-1, revision 3, found while gathering evidence for FR-54 rather than while
grading box 6.**

**QA-1 failed box 6 for six places where the tree described the world before
`livetest.Client` landed, then found a seventh themselves.** All seven belong to
one migration. **`examples/chat`'s F-3 is an eighth instance of the same class
belonging to a different migration** — `Bind.Keys` and `OnAll`, which landed at
`591c275a` at api-surface checkpoint 3, **for exactly this item**.

`FRICTION.md` F-3 reads *"Escape-to-clear is not implemented. There is no non-JS
expression for it"*, and its **"Proposed shape"** block is:

```go
live.OnWith("keydown", "chat.clear", live.Bind{Keys: []string{"Escape"}})
```

which is the shipping API, verbatim. `view.templ:64` repeats the claim in the
example's own source, and the file's summary at `:13` still counts F-3 among the
open items — while **F-1 and F-4 in the same file were given *"— Closed."*
headings** when their features landed, under a rule that file states itself: *"a
friction note documenting a missing feature must not outlive the feature."*

**The precise defect is worth stating precisely, because a hurried fix would get
it wrong.** The affordance **is** still absent from the chat example — nobody
bound Escape. So **the item's conclusion is true and its stated reason is false**,
and that is the harder shape to catch than a plain contradiction: a reader
checking whether the example does what it says will find that it does.

**Why this is a §7 finding and not a reversal of box 6.** QA-1 stated their
standard in advance, applied it, failed the box, and re-graded on remediation.
**A finding arriving after a grade is routed to the gate owner, not used to
overturn them by the person who does not hold the gate** — the same rule §5.2
states in the other direction. It is in §6 as a condition on Phase 4's exit with
**DEV-3** to fix and **QA-1** to say whether it disturbs their PASS.

**And the grep for it does not work, which I found by running the command I had
just published.** Both halves of the claim wrap across comment lines —
*"live.On has no / key filter"* and *"Escape-to-clear has no / expression at
all"* — so no phrase search reaches either. **That is the third time in this
report that a wrapped Go comment has defeated a `grep`**: §7.5 recorded DEV-1
finding `live/app.go`'s copy of F-2's false sentence *by reading*, after my grep
missed it for the same reason, and §7.7 records the corrective paragraph in
`live/core.go` sitting below the sentence it corrects. **Three instances is a
property of the tool, not a run of bad luck**, and the reproduce block at the end
of this report now says so where somebody will hit it. The workable form is to
match a fragment that lives on one line and then read the paragraph — which is
what "read, do not grep" has meant every time this project has said it.

**What it says about method, which is the reusable part.** Three passes have now
found instances of one defect class — documentation describing the library as it
was before an API landed — and **each pass found the instances belonging to the
migration it happened to be thinking about.** QA-1 was auditing the
`livetest.Client` migration and found seven. I was auditing the binding helpers
and found one, in a file QA-1 had read. **Nobody has swept for the class itself**,
and the cheapest form of that sweep is not a tool: it is that **the commit that
lands an API searches the tree for documents saying that API does not exist**,
which is what `591c275a` did not do and what `docs/api-surface.md`'s own ledger
row would have made easy, since it names the friction item by id.
### 7.10 Five corrections owed to this report's own record — and the two that survived longest are the two that were routed rather than made

**PM-1, revision 4.** §7.5 recorded four numbers that did not reproduce; §7.8
recorded three more. **Five more here**, and the pattern in them is not
carelessness about files — it is that **this report's own deferrals were the most
expensive kind of debt it carries.**

**Correction 1 — §8.2's prediction about box 2 was wrong.** It is the largest of
the five and it has §8.3 to itself.

**Correction 2 — the `live.LocalDevelopment` mis-citation at §5.8 and at
§4.13.2, routed at PRD v1.1 and paid at revision 4.** Both are corrected beneath
themselves and both paragraphs stand. **The finding was PM-1's own, made against
PM-1's own text**, which is the good half. **The bad half is the arithmetic of
the deferral**: PM-1 enumerated **two** carriers; L9-1 found **six**, of which
**two were live PRD text inside PM-1's own write scope the whole time**, so the
stated reason for not fixing them — *"outside PM-1's write scope this turn"* —
was true of the two PM-1 had in mind and false of the two they had not looked
for. **A footprint stated without being grepped is a footprint asserted**, which
is the house defect class of §7.1 applied to a set of files instead of to a
number. The two PRD sites were fixed at v1.2; **these two waited two more
revisions of a report whose §7.5 is titled after numbers that did not
reproduce.**

**Correction 3 — this report was the last document in the tree stating a stale
FR-53 count in live text, and QA-1 is the one who noticed.** §10.4 of their grade
checks `docs/README.md:24` against `docs/quickstart.md:7`, finds both at 20/11/31,
and concludes that **F-10 stays closed and "no document in the tree outside the
gate record states a stale count."** This report's §3 row 2, §6, §8.1 and §8.2
were all still reading **39 against 30** — and **the "30" had been stale for two
PRD versions on its own account**, since the budget moved to 31 at v1.1 and was
countersigned at v1.2 while this record sat at revision 3. **The row is re-graded
rather than patched**, and its criterion text now carries the amended budget with
a note saying it was stale.

**Why correction 3 is the one I would keep.** F-10 was a **condition on box 8**,
raised by QA-1, closed by DEV-3, and verified by me — a stale count in
`docs/README.md`. **The identical defect was sitting in the document that reports
on F-10**, for two revisions, while I verified somebody else's copy of it.
§7.8's third correction made the general point that a carried item is discharged
by re-deriving it; **this one makes the sharper one, that a report which grades
staleness elsewhere is not exempt from being re-derived itself**, and that the
person best placed to catch it is whoever is grading against it rather than
whoever wrote it.

**Correction 4 — §5.6's failure-2 row attributes to the library's godoc a word
the godoc does not contain.** *"The godoc calls the sharing 'a wart'"*:
`grep -rn wart live/` returns **nothing**, and the word is `docs/api-surface.md`'s.
**It appears to have come from `591c275a`'s own commit message** — *"The shared
debounce timer is a wart and the godoc says so"* — which makes this a **new**
failure mode for this report rather than a fifth instance of an old one. §7.5,
§7.7, §7.8 and §7.9 are all *"a grep missed a wrapped comment"*, which is a tool
limitation. **This is a source substitution: a commit body quoted as though it
were the artifact it describes**, by an author who had read the commit and not the
file. **The substance of the row survives intact and is now confirmed by
measurement**, which is the only reason this is a correction rather than a
retraction. Found by **QA-1** while driving the row, and reported to me rather
than fixed, because the file is mine.

**Correction 5 — three line-number citations that do not reproduce, all of them
into `docs/api-surface.md`, and one of them is not mine.** §5.6's failure-1 row
cites `:615`; DEV-3 routes it as `:651`; **it is at `:696`**. QA-1 cites `:654`
for the "wart" row; **it is at `:699` at HEAD and was at `:618` at the tree they
drove.** *(DEV-3's other routed drift does reproduce: `bench/README.md:553` →
`:670`.)* **Three agents, one append-only file, five different line numbers for
two rows.** §7.8's correction 2 drew the general lesson — *a document that quotes
a position rather than a property has booked a defect for whenever the position
moves* — and this is its fifth instance. **The remedy `api-surface.md` needs is
the one L9-1 applied to the register's walk commands and the orchestrator applied
to the doccheck count: cite the row, not the line.** Not routed as a task,
because it is a habit rather than a defect and three of these five citations are
in files nobody should edit for this.

**All five are corrections to text I own and none changes a verdict.** §5.8's
conclusion holds on L9-1's ratification; §5.6's failure-2 finding is now measured;
§3 row 2 was NOT MET at every count it stated and is MET at the one it now states;
and §8.2's wrong prediction changed nothing anybody did. **That is exactly why
they are recorded** — and the ratio worth noticing is that **three of the five
existed because this report deferred something, and only one because it got a
fact wrong.**

### 7.11 Four corrections owed to this report's own record — and the one nobody routed was found by running the block that publishes it

*(Added 2026-08-06, revision 6, at `9efb7e5b`. §7.5, §7.8 and §7.10 are the same
section at earlier revisions and are left standing. **This makes eleven corrections
to this report's own text across six revisions, and the count is published rather
than the ratio, because a report that stops finding its own errors has stopped
being run.**)*

**Correction 1 — the `+62 B minified / +34 B gzipped` price is the price of a
prototype, and this report published it as the price of a landing, six times.**
This is L9-1's **FR54-9**, and it is routed to PM-1 with the sites enumerated
(`reviews/fr-54.md` §18.4, §23): `:277`, `:617`, `:1130`, `:2746`, `:2748`,
`:3757` of this file, plus two in `docs/pm/pr-body-phase-4.md`. **The landed price
is +81 B minified / +38 B gzipped** — 10,306 → 10,387 and 4,421 → 4,459.

**I did not take that figure from the routing, and the reason is this report's own
rule.** *"A gate is what you ran."* I measured it: Go's `compress/gzip` at
`BestCompression`, which is what `tools/minify` uses, over the committed artifact
at `0b9e32e7~1` and at HEAD, in the container — **10,306 / 4,421 → 10,387 /
4,459** (§2.9 tool row 3). `0b9e32e7`'s parent **is** `42b4e0e6`, and `0b9e32e7`
is the only commit between Part A and HEAD that touches the artifact, so the delta
is the Part B shape's and nothing else's. **It agrees with L9-1's §18.4, with
`client/SIZE.md` §1.1.6 and with `docs/api-surface.md:581`, and I measured before
reading any of the three.**

**How the six sites are handled, and the distinction is the whole of this
correction.** **A dated record is not rewritten; a current-state claim must be true
now.**

| Site | Class | What happens |
|---|---|---|
| `§1.4` (`:277`) | **Dated record** — revision 5's own narrative, headed *"Added 2026-08-05, revision 5, at `e751f6de`"* | **Left exactly as written.** It was true of the ruling it described. Corrected here and at §1.5 |
| `§3` row 3 | **Live grading row** | The superseded verdict is **kept and struck**, with the corrected figure named inside the re-grade rather than substituted into the old text |
| `§4.3.3` (`:1130`) | **Dated record** — revision 5's subsection | **Left as written**, corrected here |
| `§6`, two rows | **Live carry-forward register** | Both rows **closed with the corrected figure stated in the closure**, the revision-5 text kept beneath unedited |
| `§8.4` (`:3757`) | **Dated record** — revision 5's exit statement, signed and dated | **Left as written.** §8.5 carries the correction |

**Correction 2 — and this one nobody routed, nobody could have read, and it is
this report's reproduce block.** The block at the end of this file publishes, as
what `go run ./apisurface` prints, `live 56/56  51/51  107/107`. **Run at this
tree it prints `live 56/56  53/53  109/109`.** The fields figure moved when the
Part B landing added two, which is the whole point of the landing this revision
grades — **so the reproduce block was falsified by the event the report exists to
record, and it was falsified in the direction that makes the report look
consistent.** It is corrected beneath itself in the block.

**This is the third time a published figure in this report's own reproduce block
has gone stale, and the pattern is now unambiguous.** Revision 3 found its line
ranges had drifted and were printing a number that was *"neither the old nor the
true one"* (§7.8). Revision 5's block asserted five stale size figures in other
people's files; **all five have since been fixed, so that assertion is now false
too** — correction 3. And this one. **Every one was found by executing the block.
None was found by reading it**, and this report has been read a great deal.

**Correction 3 — §6's row and the reproduce block both assert that five current
size figures in other owners' files are stale, and all five have been fixed.**
`README.md:113`, `docs/guide/deploying.md:24`, `docs/quickstart.md:161`,
`docs/guide/inspector.md:198` and `docs/instrumentation.md:835` **all now read
10,387 / 4,459**, and `client/SIZE.md`'s ledger agrees with `tools/minify`
exactly. I checked all six at the tree (§2.9 tree row 3). **The row closes and the
block's comment is corrected beneath itself.** Two of the five did better than the
correction asked for and recorded the whole path `4,429 → 4,421 → 4,459` with the
commit that caused each move — which is the shape this report keeps asking for and
should say so when it gets it.

**Correction 4 — a count stated in prose beside a table that contradicts it, in
§5.11, in this revision, caught before publication by summing the column.** The
paragraph introducing §5.11's table said *"four tick on PM-1's reading"*; the
table has **five**. It is corrected in place beneath the table rather than
silently fixed, because **it is §7.10's fifth correction one level down** — a
number carried in prose beside the evidence that refutes it — and a report that
polishes its own instance of a defect it is filing against others is doing the
thing it is filing against.

**Where these four differ from the previous seven, and it is worth one sentence.**
§7.10 noted that three of its five existed because this report *deferred*
something. **Three of these four exist because a figure this report published as
current stopped being current when the tree moved** — which is not deferral and is
not carelessness; it is the standing cost of publishing current-state numbers in a
document that outlives the tree it was written against. **The mechanism that
catches it is the reproduce block, and the mechanism only works when somebody runs
it.** Nobody has to; this revision did.

---

## 8. Exit statement

**Phase 4's gate has been held and passed. Phase 4 does not exit. Six boxes of
thirteen are ticked and seven are not.**

The gate box ticks on QA-1's PASS — a working counter from the documentation
alone in 2 m 12 s, zero source-diving breaches, no blocker — and it ticks
carrying QA-1's own caveat, which is that the gate measures a document that is
copy-paste-correct and not one that survives being deviated from. **Both
high-severity findings came from deliberately building the wrong variant, and in
both the page's own troubleshooting text pointed the wrong way.** That caveat is
inside the tick and not beside it, for the reason checkpoint 3 gave: a tick that
swallows a named open row is how the row stops being found.

**FR-53 is the box I am most confident about and it is the one that fails.** It
is a conjunction; 2 m 12 s passes and 46 lines misses. Three ways of ticking it
were available — count Go only and publish 27, read "≤30 lines of app code"
loosely enough that markup is something else, or move 30 — and all three were
pre-registered as unavailable in v0.6, *before* this measurement existed, which
is exactly what makes the pre-registration worth having. What is new is that the
remedy is specified for the first time: F-4's API fix deletes one of the 46 lines
and a documented hazard in the same change, and twelve more of them are the
`Config` fields `live.New` demands. **The number does not move; the app does.**

**I ratified one scope narrowing and I want the shape of that on the record.**
DEV-1 drew FR-66's line at the module boundary and left 268 symbols unenforced.
The argument is invariant to that count — the library is one module, the
satellites are unimportable by construction, and it is the same boundary two
other gates already use — so it passes §9's test, and it is ratified as an
**amendment to FR-66** rather than absorbed into a box's tick, because a tick
that silently swallows a 268-symbol carve-out is the failure §6's own preamble
names. **Two things DEV-1 did earned that ratification**: they called it a
narrowing instead of describing it as coverage, and they made the unenforced
count print on every run. **One thing they did not do is pin the number**, and
§7.1 is that finding: the tree states it three ways, and a carve-out justified by
a figure nobody can reproduce is a carve-out nobody can audit.

**What I am least comfortable signing** is not in the criteria, as at the last
two gates. Two of this round's three headline results — dev reload working end to
end, and the inspector painting a real causal chain — were established by browser
harnesses **that were thrown away**. Both features are genuinely demonstrated;
neither is guarded. The inspector's own history is the argument: the run that
proved it works is the run that found it painting nothing at all, while every
committed node spec passed. **The next change to either file has nothing standing
between it and a silent break**, and DEV-2 has already said where the standing
version belongs. It needs a CDP harness in `test/internal/conformance/` and an
afternoon, and it should not wait for somebody to discover it the hard way in
Phase 5.

**What went right, and it is a habit rather than a result.** QA-1 passed their
own gate and then wrote the paragraph explaining what the pass does not prove.
DEV-3 fixed seven of eight findings, declined the eighth with a reason, and
routed the one whose real fix is somebody else's API. DEV-1 landed a gate, tested
that the gate can fail on an empty walk, published the count their own scope line
does not cover, and corrected a claim of their own that they wrote and then
checked. DEV-2 published the byte figures beside the sentence saying the browser
evidence is not a gate. **None of those four is a review catching a mistake; all
four are somebody limiting the strength of their own result.** That is the habit
this project should be measured by and it is the one that made this report short
where it could have been an argument.

~~**Phase 4 exits when the seven open boxes close and QA-1 signs. The critical
path is one QA-1 pass covering FR-54, the examples, G11 and FR-59, plus three
pieces of work with owners: DEV-1's error audit and exceptions ledger, DEV-3's
deployment page, and DEV-2's conformance harness.**~~

### 8.1 Revision 2 — the exit statement, rewritten because four of those items are done

*(2026-08-05, at `134e69c5`. The sentence above is struck rather than deleted:
three of the four pieces of work it named landed in one turn, which is the
result, and a critical path that gets quietly replaced is one nobody can tell was
met.)*

**Six of thirteen boxes are ticked and seven are not, which is what revision 1
said. What is different is that revision 1's seven were mostly waiting on work
and revision 2's are mostly waiting on people.**

**Phase 4 exits when the seven open boxes close and the gate owners sign. Here is
each one, its owner, and what specifically closes it.**

| Box | State at `134e69c5` | Owner | What closes it |
|---|---|---|---|
| **12 — FR-58, the error audit** | **Delivered.** 117 sites, 25 graded failures, 25 fixed, 29 changes, three regression guards | **QA-1** | **One grading pass** over [`docs/error-audit.md`](../error-audit.md). Nothing else is owed. §4.12.1 |
| **7 — G11** | **Delivered and measured green** on all three examples, with the precondition asserted fatally and a negative control taken. The criterion's own sentence was unsatisfiable and is amended in PRD v0.9 row 1 | **QA-1** | **One grading pass** over [`docs/qa/g11-clean-clone.md`](../qa/g11-clean-clone.md). §4.7.1 |
| **8 — FR-59, the docs set** | **Delivered, nine subjects of nine by count.** One subject — architecture — ruled not discharged by a design RFC the index disclaims | **DEV-3**, then **QA-1** | **One page, or one re-filing** (§5.4), then one grading pass. §4.8.1 |
| **6 — the three examples** | Green in CI and now also proved to clone, build and serve from a clean tree. **"Polished and documented" is graded by nobody** | **DEV-3** to present, **QA-1** to grade | **One presentation and one grading pass.** DEV-2's F-5 says explicitly that the G11 run grades nothing about polish. §4.6 |
| **3 — FR-54, the templ helper set** | The helpers exist and are documented. **"Complete" is still undefined**, so the box is unticksable rather than unmet — untouched this round | **PM-1** to define, on **DEV-2's** and **DEV-3's** evidence; **QA-1** gates | **One definition, in the PRD, with an argument.** §4.3 states the likely shape. This is debt with my name on it and it did not move |
| **2 — FR-53, ≤15 min and ≤30 lines** | **The only box open on a measurement.** 2 m 12 s passes; 46 lines misses, re-counted unchanged at HEAD | **DEV-1** (the API) | **The app shrinks**, not the number. F-4's fix — a `live`-owned page handler taking the loader `Init` takes — deletes one of the 46 and a documented hazard together; twelve more are the `Config` fields `live.New` demands. §4.2 |
| **13 — FR-20, `docs/exceptions.md`** | **Drafted, walked, and entirely unsigned.** Two unrecorded deviations found, one of them live at HEAD | **L9-1** to sign or rule; **DEV-3** to fix E-2's sample | **One signature, one sample fix — and §7.6's split, because the box's own text says it cannot tick before Phase 5.** §4.13.1 |

**So the critical path is two signatures, two pages, one definition, one API
shrink and one sample fix**, and it is worth naming what it is **not**: it is not
four more turns of engineering. **Four deliverables are sitting in a queue in
front of QA-1** — the error audit, the G11 run, the docs set, and the examples'
polish clause — and nothing else has to happen before they can be graded. **The
single act that moves this phase furthest is asking QA-1 to grade.**

**What I refused to tick, in one place, so it can be argued with.** All four
re-graded boxes. Each has a real deliverable behind it and each names a gate
owner who is not me. §5.2's rule — a box is checked on evidence PM-1 verified,
and the signature is a separate act — is what let revision 1 tick FR-44 and
FR-57, and it does not stretch to cover a **judgement** the requirement assigns
by name. **The counterfactual is the check on that:** had I ticked these four,
Phase 4 would today read eleven of thirteen and be one QA-1 grading pass away
from a phase that nobody with merge-block authority had looked at. That is the
condition this project has already produced twice — a criterion passing for the
wrong reason — and it is worth more to me than a better-looking table.

**What I am least comfortable signing, and it has changed since revision 1.**
Revision 1's answer was the two uncommitted browser harnesses, and that is still
true and still nobody's turn. Revision 2's is smaller and closer: **E-2**. The
project publishes a page teaching failure handling, the compiled source behind it
breaks two of the three rules FR-20 exists to police, the root cause was our own
godoc telling the author to do it, and it has been unlisted since the page
landed. It is one edit. **A documentation phase that exits over that is a
documentation phase that did not read its own examples**, and DEV-1 found it by
walking the tree for a file that had never existed.

**What went right, and it is the same habit revision 1 named, arriving four more
times.** DEV-2 measured G11 green and then wrote that G11's own sentence is
unsatisfiable, refusing to average two findings into one word. DEV-3 brought the
docs set to nine of nine and immediately said which of the nine is weakest and
that they had not fixed it. DEV-1 corrected their own headline from 22 to 25
because their own tables said so, and found the copy of F-2's false sentence that
my grep had missed. And every one of the four routed the things outside their
ownership rather than reaching for them. **The record of this landing is four
agents making their own results harder to quote**, and that is still the property
this project should be measured by.

— PM-1, Product Manager, 2026-08-05 (revision 2)

### 8.2 Revision 3 — the queue emptied, and what is left is two decisions and one of them may be mine

*(2026-08-05, at `b04ba138`. §8.1's table is left standing; six of its seven rows
have moved and the seventh is the reason this section is short.)*

**Eleven of thirteen boxes are ticked. Phase 4 does not exit.**

**Revision 2 ended on a prediction and it was tested rather than repeated.** The
sentence was *"The single act that moves this phase furthest is asking QA-1 to
grade."* QA-1 was asked and graded four boxes; L9-1 was asked and signed the
register. **Five boxes moved in one turn, and not one moved because PM-1 read the
work.** That is §5.2's rule paying out: a box is checked on evidence PM-1 can
verify, the judgement belongs to whoever the requirement names, and **the way to
close a box blocked on the second clause is to ask, not to reinterpret.** Revision
2 refused four ticks to hold that line and the refusal cost one turn.

**What closes Phase 4, and it is two items rather than seven.**

| Box | State | Owner | What closes it |
|---|---|---|---|
| **2 — FR-53** | **39** against 30. The app shrank by seven and the miss is nine. The only box in this phase open on a measurement | **DEV-1** (API), then **PM-1** (the number) | Nothing available today. §5.8 argues **30 was never reachable**: the whole HTML document hidden in a library component lands at **31**, and the last line is a security-hook bundle refused twice. **This box most likely closes by amendment, in a later pass, not by engineering** ⟵ **WRONG. It closed by engineering, at revision 4, and this row is left standing rather than struck — §8.3** |
| **3 — FR-54** | **Defined and failing on three named gaps.** Was unticksable, is now arguable | **DEV-2**/**DEV-1** (two API questions), **DEV-3** (one documentation one), **L9-1** (FR-65), **QA-1** gates | Each of the three fixed **or refused with an argument and a re-open trigger**. §5.6 |

**Plus three conditions** in §6: FR-54's three failures are box 3; `examples/chat`'s
F-3 outliving its feature is one edit and one QA-1 disposition; and failure 2
should be driven in a browser before anybody changes an API on the strength of my
reading of three source files.

**The honest headline is that Phase 4's remaining work is two decisions, and one
of them is probably mine.** FR-53 has been open since v0.6 against a number I set
and pre-registered as immovable, and §5.8 is me concluding that the number was
never reachable. **I want the shape of that on the record without dressing it
up**: the pre-registration was right and it did its job — it stopped three
flattering re-readings and it forced the app to shrink by seven lines it would
otherwise still be carrying. **A budget can be both unreachable and useful, and
this one has been both.** What it cannot be is quietly adjusted by the person who
set it, in the pass that measured against it, which is why the amendment is
pre-registered rather than made.

**What I am least comfortable signing has not changed, and that is the finding.**
Revision 1 named DEV-2's two uncommitted browser harnesses; revision 2 named them
again and added E-2. **E-2 is fixed. The harnesses are not, for a third
consecutive revision**, and §6's accounting now shows them as **the oldest
unaddressed item in this report** — carried since revision 1, with a named owner,
and **it has never been anybody's turn.** *(The other item I was about to name
beside it, the godoc carve-out's three-valued count, turns out to have been
discharged at `e784e49b` before revision 2 was even written — §7.8 correction 3.
I found that by checking my own row instead of copying it, which is the only
reason this paragraph is not wrong twice.)* A condition that survives three
revisions is not
being deferred on its merits; it is being deferred because nothing forces it, and
§4.5's own history is the argument: the run that proved the inspector works is the
run that found it painting nothing at all, while every committed node spec passed.
**§5.6's failure 2 is the third finding in this report that a browser would settle
and nothing in CI enters one.**

**What went right, and it is the fourth revision running to say the same thing.**
**QA-1** passed four boxes and, in each, built the control that could have failed
them — including a `node` shim on `PATH` nobody asked for, and a vacuity control
on somebody else's new guard. **QA-1 also failed a box, recorded a miss inside
their own re-grade, and overturned their own prescription in favour of the person
they had graded.** **DEV-3** answered a ruling by taking the harder of two
alternatives and argued the choice on the ruling's own honest test. **DEV-1**
found the sentence their own fix had left standing and fixed it against
themselves. **L9-1** corrected three of the register's numbers *before* signing
it, refused the cleaner-looking scope ruling on a precedent they then wrote down,
and declined to resolve a scope question that was mine even though resolving it
would have tidied their own signature.

**And the one I am adding this round is about being wrong.** §7.8 records **three**
corrections to this report. A reproduce block that reproduced nothing. **A verdict
in my own §2.4 that was false when I wrote it** — I read the paragraph that fixed
a defect and did not search for the defect itself, which is the same error the
sample author made reading the same comment. And **a condition this report has
been reporting as blocking since an hour after it was discharged**, which I found
only because I went to check a claim in a file I own instead of restating the row
that carried it.

**That third one is the one I would keep if I could keep only one.** A stale
number misleads a reader; **a discharged condition reported as blocking asks
somebody to do work that is already done**, in the document whose entire job is
to say what is blocking. It survived two revisions of the most careful reading
this report has had, and it survived because a row in a table is exactly the kind
of thing that gets carried forward rather than re-derived. **§6's own preamble
says a carried item has an owner and a home; what it did not say, and now does,
is that a carried item is discharged by re-deriving it.**

**The habit this project should be measured by is not that its agents are
careful. It is that four different people this week made their own results harder
to quote — and that the three findings this revision is most uncomfortable about
are all mine, about myself.**

— PM-1, Product Manager, 2026-08-05 (revision 3)

### 8.3 Revision 4 — the box I said would close by amendment closed by engineering, and I was the one who said it

*(2026-08-05, at `5d665226`. §8, §8.1 and §8.2 are left standing. **§8.2's box-2
row is the sentence this section exists to pay**, and it is not struck, because a
prediction struck out of a record is a prediction the record can no longer be
measured by.)*

**Twelve of thirteen boxes are ticked. Phase 4 does not exit. The one box left is
FR-54.**

**Revision 3's §8.2 said, in a table, of box 2:**

> **This box most likely closes by amendment, in a later pass, not by
> engineering.**

**That prediction was mine and it was wrong.** Box 2 closed by **engineering**:
DEV-1 built a library-owned page shell, L9-1 gated it under FR-65 against nine
constraints written before it existed, DEV-1 discharged three conditions in code
at **+0 exported identifiers**, L9-1 accepted after killing seven mutants with
seven specs, and QA-1 counted **31** and graded **PASS**. **The amendment I
expected to close it had already been made, at PRD v1.1, and it moved the number
by one and closed nothing** — which is exactly what that amendment said about
itself and exactly what §5.8 said would happen. **So the pass that made me wrong
is a pass I had already written the argument for; what I got wrong was who would
close it and how.**

**Three things are worth saying about being wrong here rather than one.**

1. **The prediction was not idle — it was load-bearing, and it was quoted.** PRD
   §5.I (h) and [`docs/pm/fr-53-amendment.md`](../pm/fr-53-amendment.md) §9 both
   cite this sentence **by name, as a thing to be corrected**, from the day the
   amendment landed. It has been on the record as *known wrong* for two PRD
   versions before it was *shown wrong* today. **A prediction that everybody has
   agreed is false and that nobody has paid is a debt, not a disclosure**, and it
   took the box actually closing for it to be paid — which is a fact about this
   report's deferral habit and not about the prediction.
2. **What made it wrong is the thing I could not have priced: somebody built the
   component.** At revision 3 the `live.Document` shell was arithmetic over a
   symbol that did not exist — `grep -rn Document live/` returned nothing — and
   the honest form of my sentence was *"nobody has proposed building this."*
   **I wrote "most likely closes by amendment" instead, which converted an
   absence of work into a prediction about the work's nature.** That is the same
   move §5.6 rejects when it rejects the circular population: reading a gap in
   the tree as evidence about what the tree can do.
3. **And the counterfactual says the prediction was not merely unlucky.** Had the
   shell cost one line more, the app would have counted 32, **trigger 1 would
   have fired upward, the budget would not have moved at any cost, and the
   amendment would have been withdrawn and re-argued with the box open.** The
   route my prediction named — *closes by amendment* — **was closed by the
   ratchet in both directions before the shell landed**, and L9-1-C2 is what
   closed it. **There was no world in which box 2 closed by amendment**, and I
   said it would in the revision that carried the ratchet's own repair three
   sections earlier.

**What closes Phase 4 now, and it is one box and one page.**

| Box | State | Owner | What closes it |
|---|---|---|---|
| **3 — FR-54** | **Defined, failing on three named gaps, and two of the three moved this round without closing.** Failure 1 is undecided; failure 2 is **measured** rather than derived; failure 3's reason is corrected and its sentence has relocated into the example's own source | **DEV-2**/**DEV-1** (the API question failures 1 and 2 raise), **DEV-3** (the example's comment), **L9-1** (FR-65 on any new surface), **PM-1** (a refusal, if that is the answer); **QA-1** gates | Each of the three fixed **or refused with an argument and a re-open trigger**, per FR-54's clause 3. §4.3.2, §5.6 |

**Plus three conditions in §6**, and the shape of them has changed: **Q-1 and
Q-2 block on `docs/quickstart.md`** — a documented build path that errors for
every reader, and a troubleshooting row pointing at a log the counted application
cannot write — and **`examples/chat/view.templ:64` still tells a reader the
library cannot do something it does.** **All three are DEV-3's, all three are
prose, and none of them can move the count**, which QA-1 established for each
rather than leaving the fear of a zero-margin budget to defer them.

**And the first two moved while this section was being written**, which is the
right kind of problem to have. `f555f3b5` landed a remediation for **Q-1, Q-2,
Q-3's page half and Q-4** — §2.7. **I have re-derived the count at it (still
31, and the two counted sample files are byte-identical) and recorded that Q-4's
gate matches the number and the four constraints §5.10 authorised. I have not
discharged any of the four, and I am not going to.** They are QA-1's conditions;
Q-4's credit turns specifically on a **red-at-32 demonstration that is QA-1's**;
and **this report has spent three revisions on the sentence that work landing is
not a gate passing.** Applying it to a landing that makes my own act look
finished is the only version of that rule that costs anything.

**What I am least comfortable signing has not changed for a fourth revision, and
this is the revision that removes the last excuse.** DEV-2's browser loop is
still not in CI. Revision 1 named it, revision 2 named it again, revision 3
called it *"the oldest unaddressed item in this report"* and noted that §5.6's
failure 2 was the third finding here a browser would settle. **A browser settled
it** — QA-1 wrote eight specs in the conformance suite's own idiom, reusing that
suite's `launchChrome`, `serveLive` and `press`, ran them out of `/tmp` against a
copy of the tree, got `8 Passed | 0 Failed` and a mutation control that turns
three red, and **explicitly did not commit any of it**, correctly, because
choosing to add a regression gate is DEV-2's or QA-1's call after FR-65 and not a
reproduction document's. **So the harness that would discharge the oldest
condition in this report has now been written twice, by two different agents,
and thrown away both times.** It is not that nobody can write it. It is that
nothing forces anybody to keep it.

**What went right, and this round it is about gates rather than about people.**
**L9-1 pre-registered nine constraints before the artifact existed and then
failed the artifact on the one that mattered** — not on a byte count or a
signature, but on a *sentence*, made in five places, that the component's own
behaviour falsified; and they found it by running a probe rather than by reading
the disclosure that under-rated it. **DEV-1 took the more expensive of the two
offered remedies** and gave a better reason for it than the reviewer had given.
**L9-1 then tried to break the repair seven ways and could not.** **QA-1 refused
to treat four prior agreements at 31 as confirmation**, on the ground that no
count on this box can be blind because the number is printed in four documents a
grader has to read — and replaced blindness with the one check anchoring cannot
fake, running their method across six commits to show it returns 46 and 39 where
the record says 46 and 39. **And QA-1 drove a counter with a negative control
that reported a *dead* counter**, because a probe that has never said NO has not
been shown able to.

**The habit revision 3 named is intact and the finding this round is the same one
in a fifth form.** §7.10 records five corrections to this report. **Three of the
five exist because this report deferred something rather than because it got a
fact wrong** — two mis-citations routed at PRD v1.1 and paid two revisions later,
and a stale count that **QA-1 found while grading against this document.** The
one I would keep is that third: **F-10 was a condition on box 8 about a stale
count in `docs/README.md`, raised by QA-1, closed by DEV-3, verified by me — and
the identical defect was sitting in the report that verifies it, for two
revisions.** A gate report that grades staleness elsewhere is not exempt from
being re-derived itself, and the person best placed to catch it is whoever is
grading against it.

— PM-1, Product Manager, 2026-08-05 (revision 4)

### 8.4 Revision 5 — the revision in which a great deal happened and the number did not move

*(2026-08-05, at `e751f6de`. §8, §8.1, §8.2 and §8.3 are left standing. **§8.3's
closing sentence about the thrown-away harness is the one this section retires,
and it is the only thing in four revisions of this report to be retired by
somebody doing the work rather than by an argument.**)*

**Twelve of thirteen boxes are ticked. Phase 4 does not exit. The one box left is
FR-54, and the count is the same number revision 4 printed.**

**Four landings and a technical ruling arrived under this revision. None of them
ticks a box, and the honest report of that is the whole of §8.4.** A reader
skimming the revision table will see FR-54 failure 2 fixed, FR-54 failure 3 fixed,
the browser loop in CI, Phase 3 exiting at seventeen of seventeen, and a principal
engineer ruling on the question this report has carried undecided since revision
3 — and will conclude that Phase 4 is done. **It is not, and the reason is one
distinction: failure 1 is decided rather than fixed.**

**What "decided rather than fixed" means here, precisely.** L9-1 accepted
`Bind.NoModifiers` and `Bind.PreventDefault`, specified them to the godoc, priced
them at **+62 B minified / +34 B gzipped, +0 exported identifiers, +2 fields**,
proved zero output delta, drove `F-CHT-3` four ways under the accepted shape with
a negative control that reproduces today's loss, and refused the full modifier set
with a trigger a future proposal can be measured against without re-arguing
anything. **All of that was done against a prototype in a container's `/tmp`, and
the worktree is byte-identical to `d12870a0` at the end of that review by its
author's own statement.** The surface does not exist. **C-9 is the proof that this
distinction is not pedantry**: building the prototype is what found that
`dispatch` calls `preventDefault` *before* the IME composition guard, so the
accepted flag placed where the prototype places it **would break every CJK
composer** — a defect in the accepted design, found by construction, published by
the person who accepted it, in the same document. A ruling that has already found
one defect in itself is not a landing.

**And the box's closure condition is not this report's judgement.** L9-1 wrote it:
*"FR-54's box closes when FR54-3, FR54-4 and FR54-6 are discharged and QA-1 grades
them."* **FR54-3 is a regression this landing introduced** — an unbindable key
used to widen the filter and leave the debounce alone, and now it shifts every
later option one slot left, so a declared debounce silently becomes a throttle.
**FR54-4 is the landing's own motivating case left unpinned** — the mutant that
reintroduces failure 2 for two bindings sharing an event name is green against
156 client specs and 7 browser specs. **FR54-6 is the Part B landing.** Three
pieces of work and one grade, none started. **Twelve of thirteen.**

**Two of the three conditions §6 carried are discharged, and the one that is not
is the one I could have taken and did not.** `examples/chat`'s comment is fixed by
building the affordance rather than by rewording the sentence. The browser loop is
in CI. **Box 2's Q-1 and Q-2 have had a remediation sitting in the tree since
`f555f3b5` and QA-1 has still not graded it.** I have now declined to discharge
those four conditions in three consecutive revisions. **This is the revision where
declining costs something visible**: with two of three conditions gone, taking the
third would let this report say the phase is blocked on one box and nothing else,
and that sentence would be a PM-1 grading QA-1's own conditions in the pass that
received the remedy for them. **The rule this report has spent four revisions on
is that work landing is not a gate passing. Its converse arrived this round and is
the same rule: a decision is not work.** Both readings cost the same thing —
a count that stays where it is — and that is the only evidence that either is
being applied rather than quoted.

**What went right, and this round it is about mechanism rather than about people
or gates.**

**The oldest item in this report closed, and it closed for a reason worth more
than the closure.** Four revisions named DEV-2's uncommitted browser loop as a
condition. Two agents wrote the harness and threw it away, and **both were right
to**: QA-1's was a reproduction document's, DEV-2's original was a demonstration,
and committing somebody else's regression gate is not either of their calls.
Nothing about the difficulty changed. **What changed is where the artifact
landed** — inside `test/internal/conformance/`, inside `ci.sh`'s browser step,
with the step's spec counts **re-derived at the tree rather than carried**, which
immediately caught that the published counts had been stale through two landings
(22/25, and 19/22 before that). **So the fix for "nothing forces anybody to keep
it" was to put it where something already fails when it goes missing**, and that
is a finding about this report's method: **naming a thing as a condition is not a
mechanism, and this report has been using it as one.** Q-1 and Q-2 are named too.

**And the browser is now inside the gate rather than beside it.** The whole-gate
run this revision quotes is the orchestrator's at `d12870a0`, in
`dis-gotth-live-bench:latest` with `GOTTHLIVE_E2E=1` and `CHROME_BIN` set: **exit
0**, *"every gate this invocation could run is green"*, browser conformance **`ok
50.369s`**, **one step skipped — G11**, which by design needs a host docker daemon
and cannot run inside a container. `apisurface` and `doccheck` were re-run at
`e751f6de` after L9-1's commit and both exit 0. **Every previous revision of this
report had to quote the browser evidence from beside the gate. This one does
not**, and that sentence is `13a1ca1e`'s and not this report's.

**The Phase 3 act is the one thing here that is mine, and it is held the way this
report has been demanding other people hold theirs.** Six runs, three on a
pristine export, a programmatic diff rather than an eye, byte figures identical
101 commits later and identical again at a commit that re-encodes rendered markup
and landed mid-act, the method paragraph checked against the code at HEAD rather
than the commit body — **and the one figure that did not reproduce given its own
section at the same prominence as the ones that did.** `max 579µs` is the low
outlier of eight runs on this host, the README predicted its own irreproducibility
before anybody re-ran it, and the act says plainly what a reader who quotes that
number is quoting. **A gate act that publishes its own weakest number is the only
kind this project counts**, and the standard I am applying to FR-54's four
outstanding items is the one I just applied to myself.

**What I am least comfortable signing, for a fifth revision, and it is no longer
the browser loop.** It is that **this report now carries two corrections to its own
text that neither I nor any reader found** — §4.5's "Illegal invocation" mechanism
and its `CLIENT_EVENT` transcript line, both repeated for four revisions, both
disproved by an engineer running a control that was meant to confirm them. **The
first was the diagnosis of the defect that this report has used, in three separate
sections, as the argument for the condition that just closed.** The argument
survives — the browser is where that class of defect lives, and mutation control
**A** now reproduces the symptom without needing the diagnosis to be right — but
**the report was making a true argument out of a false fact for four revisions,
and the way it was caught was somebody trying to execute it.** §7.5, §7.10 and now
§4.5.1 all say the same thing in different words: this document is corrected by
people who run it, not by people who read it, and it should be written to be run.

— PM-1, Product Manager, 2026-08-05 (revision 5)

### 8.5 Revision 6 — the exit act, and the four rows the tick does not close

*(2026-08-06, at `9efb7e5b`. §8, §8.1, §8.2, §8.3 and §8.4 are left standing.
**§8.4's `+62 B minified / +34 B gzipped` is wrong — the landed price is +81 /
+38, measured at §2.9 tool row 3 — and it is corrected here rather than edited
there, because §8.4 is a signed and dated statement about a ruling that had no
artifact, and it was accurate about that.**)*

**THIRTEEN of thirteen boxes are ticked. PHASE 4 EXITS.**

**The thirteenth ticks on QA-1's signature and not on mine**, and that is the
sentence this report has been holding out for since revision 2 refused to tick
four boxes on its own reading. `docs/qa/phase-4-grading.md` §11, graded at
`eb4971c6`, committed at `9efb7e5b`: **PASS WITH CONDITIONS.** §5.11 says which of
all thirteen ticked on whose act — **seven QA-1, five PM-1, one L9-1** — and the
five that are mine are named there rather than left to be inferred from a total.

**QA-1's sentence, and it is theirs rather than mine:**

> **FR-54's Phase-4 exit box is graded PASS WITH CONDITIONS by QA-1 at
> `eb4971c6`: the helper set is complete under the PRD's four-clause definition
> across all three parts of its population — clause (c) is empty, and I swept it
> rather than inherited it; FR54-3, FR54-4 and FR54-6 are discharged on QA-1's
> own runs and not on the reviewer's, with the mutant that reintroduces failure 2
> turning exactly its owning spec red and the removed refusal turning 10 of 316
> `live` specs red; all nine of `reviews/fr-54.md` §14's constraints hold as
> corrected, and QA-1 accepts all three amendments — C-6's on evidence QA-1 drove
> itself, in node and in Chromium, showing that the constraint as written would
> have certified a runtime with a dropped `altKey` read. The four conditions are
> documentation, each has an owner, and not one of them makes any binding
> inexpressible, uncomposable, undecided or undocumented.**
>
> — **QA-1**, correctness gate, merge-blocking, 2026-08-05, at `eb4971c6`

**What the tick does not close, at the same prominence as the tick, because
revision 5 wrote the rule and this is the revision it costs something.** *"A tick
that swallows a named open row is how the row stops being found."*

1. **Q-5, Q-6 and Q-8 are open.** They are QA-1's, they travel with the tick, and
   **they are not mine to discharge** — Q-5 is L9-1's own refusal ground carrying
   a price that is off by roughly five; Q-6 is a false sentence sitting in the
   file that is C-6's evidence; Q-8 is a ruling and a runtime that disagree.
   **Q-7 was mine and is discharged at PRD v1.5**, in this pass, which makes one
   of eight open QA-1 conditions across two boxes. I have declined to discharge
   the other seven for other people in four consecutive revisions and I decline
   again here, in the revision where doing it would let this report say the phase
   exits clean.
2. **FR54-7 is open and travels behind the box.** `refuseUnbindable` refuses four
   things; L9-1's §22.3 rules a fifth; **the tree is self-consistent and the
   ruling is the outlier.**
3. **G11 did not run in the gate this exit quotes**, and one browser and one
   version is the whole of the browser evidence.
4. **Phase 4 exiting is not the project finishing, and no benchmark timing has
   been collected.** §3's closing paragraph and §6 both say so; it is repeated
   here because this is the paragraph that gets quoted.

**What went right, and this round it is a method failing honestly rather than a
method working.**

**L9-1 pre-registered nine constraints before the artifact existed, and three of
the nine were defective.** C-1 stated a client-spec count that was never right.
**C-3 set a byte budget no correct artifact could satisfy**, because it was priced
against a prototype that places `preventDefault` above the IME composition guard —
the shape C-9, five rows below it in the same table, forbids. **C-6 as written
would have certified a runtime with a dropped `altKey` read**, and the landing is
safe only because DEV-1 wrote a spec the constraint did not ask for. **All three
were caught by the people building against them. None was caught by their author
in advance, and their author published all three against themselves**, in the
document that carries the constraints, with the constraints left unedited — *"a
pre-registered constraint that gets quietly edited after the artifact exists is
worth nothing."* **QA-1 then re-drove the third independently, in node and in
Chromium, and confirmed it.**

**This report has praised pre-registration three times and it should now say what
this round actually shows.** Pre-registration did not prevent three defective
constraints from being written by a principal engineer in one sitting. **What it
did was make them findable, attributable and correctable before they graded
anything** — C-3 was caught by a builder pricing the real shape, C-6 by a builder
exceeding it and a grader mutating it, C-1 by a count somebody re-ran. That is a
smaller claim than *"constraints written before the artifact are the method that
works"*, and it is the one this round's evidence supports. **A constraint written
in advance is still somebody's guess. Its value is that it is a guess on the
record, with a name on it, before anyone knows which way it cuts.**

**And the finding I would rather this exit carried than the exit.** It is QA-1's,
and it is Q-5: they set out to prove T-2's re-open trigger was dead — that the
byte envelope had been tightened out of reach by the very landing that made it
matter — and **measured it alive instead**, then found that the number leading the
refusal it guards has been wrong since the landing it sits beside. **+10 gzipped
bytes at HEAD, not +57. Off by roughly five, in the sentence a future proposal
will be argued against.** The refusal survives on its other two grounds and QA-1
did not re-open it. **A control run to confirm a thing, which disconfirms it, and
is published as a finding rather than dropped as noise, is the only evidence this
project has that any of its gates are real** — and two of QA-1's fourteen did
exactly that in one pass.

**What I am least comfortable signing, and it is the same shape as revision 5's
and one level closer to home.** Revision 5 was least comfortable that two
corrections to its own text were found by an engineer running a control. **This
revision carries four more, and three of them are figures this report published as
current and which stopped being current when the tree moved underneath them.** One
was routed to me by L9-1 as FR54-9. **Two were not routed by anybody, could not
have been found by reading, and were found because I executed my own reproduce
block instead of trusting it** — an `apisurface` line falsified by the exact
landing this revision grades, and a five-site staleness claim that every one of
the five had since fixed. §7.5, §7.8, §7.10 and now §7.11 have said the same
sentence four times in four different words, and it is the sentence I want a
Phase-4 exit record to end on rather than a count: **this document is corrected by
people who run it, not by people who read it, and it should be written to be
run.**

**Phase 4 exits. The reproduce block below runs at `9efb7e5b`, and it is the part
of this report most likely to be true tomorrow.**

— PM-1, Product Manager, 2026-08-06 (revision 6 — Phase 4 exit act)

---

*Reproduce the checks in this report that need no toolchain:*

```
cd gotth-live

# §2.2 row 1 — FR-53's count, by the quickstart's own method.
#
# CORRECTED AT REVISION 3, and §7.8 is the finding. The ranges published here at
# revisions 1 and 2 were 66-115 and 313-346. They were right when written and the
# page moved under them at fde707f0; run today they print 28 and 19 — a total of
# 47, which is neither the 46 this block was written to reproduce nor the 39 that
# is true. A reproduce block that reproduces nothing is worse than none.
#
# At HEAD these print 20 and 19. Total 39, against FR-53's 30.
awk 'NR>=72  && NR<=111' docs/quickstart.md | grep -v '^\s*$' | grep -v '^\s*//' \
  | grep -v '^package ' | grep -v '^import' | grep -v '^\s*"' | grep -v '^)' | wc -l
awk 'NR>=314 && NR<=347' docs/quickstart.md | grep -v '^\s*$' | grep -v '^\s*//' \
  | grep -v '^package ' | grep -v '^import' | grep -v '^\s*"' | grep -v '^)' | wc -l

# ...and the ranges are the fragile part, so check them before trusting them:
# the Go block should open at "package main" and close at the "}" after
# log.Fatal, and the templ block should open at "package main" and close at the
# "}" after </html>.
sed -n '72p;111p;314p;347p' docs/quickstart.md

# §2.2 row 2 — FR-57 added no dependency
git diff --stat 452e1e74 8a06cb04 -- go.mod go.sum

# §2.1 — what the godoc green at 1e59bb04 covers at HEAD
git diff --stat 1e59bb04 8a06cb04 -- . ':!docs'

# §4.6 — the dashboard has not moved since its resync re-measurement
git diff --stat 1b16f4a9 8a06cb04 -- examples

# §6 — the three stale claims, verified present
grep -n 'router strips the prefix' live/example_test.go docs/api-surface.md
grep -n 'are not here yet' live/doc.go

# §4.13 — the file FR-20 requires
ls docs/exceptions.md

# §7.1 — the count, stated three ways
grep -n 'Listing 254' tools/doccheck/main.go
git log 1370229c -1 --format=%B | grep -n 'other 268'
```

*Revision 2's checks, at `134e69c5`:*

```
cd gotth-live

# §2.4 row 1 — the error census sums to the audit's 117
sed -n '/^var errorCensus/,/^}/p' internal/arch/errors_test.go

# §2.4 row 2 — and the audit's tables carry 25 graded failures
grep -c '\*\*was ' docs/error-audit.md

# §2.4 rows 4, 5 — E-2 is live at HEAD; its root cause in the godoc is fixed
grep -n 'slog.Warn' docs/guide/_samples/errorhandling/errors.go
grep -n 'Config.Execute' live/core.go

# §2.4 rows 7, 8 — the G11 step, and the two PRD sentences it corrected
grep -c 'G11' ci.sh                       # 17, not the artifact's fifteen
git show 5c751ae9:candace/pkg/gotth/ci.sh | grep -c 'G11'
grep -n 'cd gotth-live/examples' docs/PRD.md

# §2.4 rows 11, 12 — FR-53 and FR-68 undisturbed by the FR-58 remediation
git diff --stat 8a06cb04 HEAD -- docs/quickstart.md examples
grep -c '// Output:' live/example_test.go live/livetest/example_test.go

# §2.4 row 14 / §5.5 — five call sites, not four
grep -rn 'mailbox was full, or the session is closing' --include='*.go' .

# §7.3 — the same field documented two ways in one module
grep -n 'the only thing that field does' live/doc.go
grep -n 'It does three things' live/config.go

# §7.4 items 1, 2, 3 — verified against the code
grep -c 'console\.' client/runtime.js       # 0
grep -n 'data-gotth-status' client/runtime.js docs/adr/001-transport.md
grep -n 'CompressionDisabled' internal/wsx/handler.go
grep -n 'CloseForbiddenOrigin' internal/wsx/handler.go

# §7.5 — the numbers that did not reproduce
git rev-list --count d66e4953..HEAD          # 81, not 68
grep -rn 'router strips the prefix' docs/reviews/checkpoint-2-batch.md
```

*Revision 3's checks, at `b04ba138`. Nothing here needs a toolchain.*

```
cd gotth-live

# §7.8 correction 1 — the verdict in my own §2.4 row 5 that was false when written.
# The corrective paragraph WAS there. So was the sentence it corrects, above it.
git show 134e69c5:gotth-live/live/core.go | grep -n 'Log it\|count it'   # :246-:247
grep -n 'Log it, count it' live/core.go                                  # gone at HEAD

# §7.7 — three copies of one sentence, and what survives is quotation
grep -rn 'Log it, count it, branch on it' --include='*.go' --include='*.md' .
#   -> only docs/exceptions.md and guide/error-handling.md, each naming it as
#      the wording that CAUSED E-2

# §2.5 row 4 — box 8's condition (QA-1's F-10) is closed
sed -n '24p' docs/README.md          # "20 lines of Go, 19 of templ"
sed -n '7p'  docs/quickstart.md      # "20 lines of Go and 19 lines of templ markup"

# §2.5 row 5 — the register is signed, on every row
sed -n '13,17p' docs/exceptions.md   # three rows, each "L9-1, 2026-08-05"

# §2.5 row 6 / §5.8 — what live.New actually requires: SEVEN fields, four of
# them security hooks. (This report said "eight" from revision 1 to revision 2.)
sed -n '158,180p' live/app.go | grep 'Field:'

# §5.6 failure 1 — reported twice, refused never
grep -n 'Modifier state is not compared' docs/api-surface.md      # "a finding for PM-1"
grep -n "F-CHT-3's \"Enter sends" bench/README.md

# §5.6 failure 2 — element-scoped debounce, derived from three files.
# OnWith emits the attribute only when Debounce > 0; OnAll keeps the first
# PRESENT value; the runtime reads it off the element and keys the timer by it.
sed -n '148,160p' live/templ.go
sed -n '183,207p' live/templ.go
sed -n '645,665p' client/runtime.js
grep -n 'live.OnAll' -A 3 docs/guide/_samples/events/view.templ   # Escape + 150ms, one element

# §5.6 failure 3 / §7.9 — chat's F-3 outlived its feature
grep -n 'no non-JS expression' examples/chat/FRICTION.md           # :140
git log --oneline -S'Keys []string' -- gotth-live/live/templ.go    # 591c275a, citing F-3
#
# The claim in view.templ is at :64-:68 and CANNOT be grepped as a phrase: both
# "live.On has no / key filter" and "Escape-to-clear has no / expression at all"
# wrap across comment lines. That is the third time in this report a wrapped Go
# comment has defeated a grep (§7.5, §7.7), and the first two were mine too.
# Match a fragment that lives on one line, then read the paragraph:
grep -n 'key filter' examples/chat/view.templ                      # :65
sed -n '62,69p'      examples/chat/view.templ

# §2.5 row 11 — the item in my own brief that did not reproduce
grep -n 'Q-BENCH-1\|Q-BENCH-2' docs/OPERATOR-QUESTIONS.md          # both present since v0.6
grep -n 'no bench series' bench/README.md                          # the stale one
```

*Revision 4's checks, at `5d665226`. **Every one needs no toolchain** — `git`,
`grep`, `sed` and `python3` only — which is why they are here rather than in a
container recipe:*

```
cd gotth-live

# §2.6 row 1 — the count, 31, on FOUR artifacts and by classification rather
# than by a frozen awk. Bucket every physical line under exactly the four
# exclusions the v0.6 rule names (blank, comment, package, import DECLARATION —
# the parenthesised block and its closing paren included, which is the reading
# QA-1 graded on and the only one that reproduces this project's whole published
# history of this figure) and print the buckets so they can be checked by eye.
#
#   quickstart/main.go block   -> 43 physical = 7 blank + 10 comment
#                                 + 1 package + 5 import  -> COUNTED 20
#   quickstart/view.templ block-> 32 physical = 4 + 12 + 1 + 4 -> COUNTED 11
#   docs/guide/_samples/quickstart/main.go   -> 67 physical -> COUNTED 20
#   docs/guide/_samples/quickstart/view.templ-> 32 physical -> COUNTED 11
#   TOTAL 31 on the page's fences, 31 on the pinned samples.
#
# LOCATE THE BLOCKS BY THEIR MARKERS, NEVER BY LINE NUMBER. This is §7.8
# correction 2 and §7.10 correction 5 applied to this report's own block rather
# than described: the ranges QA-1's §10.3 cites, :75-:117 and :331-:362, were
# exact at 8be955e5 and are off by three at f555f3b5, because Q-1's and Q-2's
# remedies landed above them. The markers do not move:
awk '/^<!-- sample: quickstart\/main\.go -->/{f=1} f&&/^```/{n++; if(n==2) exit} \
     f&&n==1&&!/^```/{print NR": "$0}' docs/quickstart.md | head -3
grep -n '^<!-- sample: quickstart/' docs/quickstart.md
#
# And the counted blocks are UNCHANGED across Q-1's and Q-2's remedies, which is
# what QA-1 said would be true of any fix landing in a bash block or in prose:
git diff 5d665226 f555f3b5 -- docs/guide/_samples/quickstart/   # -> empty

# §2.6 row 2 / trigger 2 — validate still requires SEVEN fields, Init optional,
# four of them security hooks. Read the switch, not the count.
sed -n '/^func validate/,/^}/p' live/app.go | grep -c 'case cfg\.\|case len(cfg\.'

# §2.6 row 3 / L9-1-C2 — the repaired trigger 1 was in force BEFORE the shell.
# This is the single fact that decides whether box 2's PASS means anything.
git merge-base --is-ancestor 667d3db7 8680e8c5 && echo "in force before the shell"
git log --format='%h %ad %s' --date=format:'%H:%M' -1 667d3db7   # 16:19
git log --format='%h %ad %s' --date=format:'%H:%M' -1 8680e8c5   # 16:47

# §2.6 row 4 / §7.10 correction 4 — the godoc does NOT contain "wart".
grep -rn wart live/                                        # -> nothing
grep -rn wart --include='*.md' docs/api-surface.md docs/guide/  # -> the ledger row
git log -1 --format=%B 591c275a | grep -n 'wart'           # -> the commit message

# §2.6 row 5 — failure 3 is relocated, not closed. The claim in view.templ
# CANNOT be grepped as a phrase (it wraps); match a fragment on one line first.
grep -n 'key filter' examples/chat/view.templ        # :65, inside the :63-:68 paragraph
sed -n '63,68p'      examples/chat/view.templ
grep -n 'expression at all' examples/chat/view_templ.go

# §2.6 row 6 — population (b) does NOT catch Escape-to-clear. Read the frozen
# chat table in full rather than grepping for the word "Escape".
sed -n '212,220p' docs/bench/equivalence-spec.md     # F-CHT-1 .. F-CHT-9

# §2.6 rows 7, 8 / §7.10 correction 5 — three citations into one append-only
# file, five line numbers, two rows.
sed -n '696p' docs/api-surface.md    # failure 1's row (§5.6 says :615, DEV-3 :651)
sed -n '699p' docs/api-surface.md    # the "wart" row (QA-1 says :654)
git show 667d3db7:gotth-live/docs/api-surface.md | sed -n '618p'  # same row, then
sed -n '670p' bench/README.md        # DEV-3's other drift, which DOES reproduce

# §7.10 correction 3 — this report was the last carrier of a stale count.
sed -n '24p' docs/README.md          # "20 lines of Go, 11 of templ"
sed -n '7p'  docs/quickstart.md      # the same, on the page itself

# §5.10 — the gate this authorises does not exist yet, which is the point.
grep -rn 'FR-53' --include='*.go' --include='*.sh' . | grep -v '_test.go:.*//' | head
grep -n 'FR-53' ci.sh                # -> nothing

# ---------------------------------------------------------------- revision 5

# §2.8, tool rows 1 and 2 — the two numbers this revision quotes as numbers.
# THESE NEED A CONTAINER. `bash -c`, never `bash -lc`: a login shell strips the
# Go toolchain from PATH in the gotth-live dis images.
~/bin/dis run bash -c 'cd tools && go run ./apisurface'
#   -> live 56/56  51/51  107/107 ; "the surface matches the ledger"
#
#   CORRECTED AT REVISION 6, and §7.11 correction 2 is the finding. This line was
#   true at e751f6de and is FALSE at HEAD: the Part B landing added two struct
#   fields, which is the whole of what revision 6 grades, so the block was
#   falsified by the event the report exists to record. At HEAD it prints:
#   -> live 56/56  53/53  109/109 ; live/livetest 37/37 33/33 70/70
#      total 93 / 86 / 179 ; "the surface matches the ledger"
#   IDENTIFIERS ARE UNCHANGED AT 56. The accepted surface cost +0 exported names
#   and +2 fields, and that is checkable here rather than quotable from a review.

~/bin/dis run bash -c 'cd tools && go run ./minify -check'
#   -> Shipped gotth-live.min.js 10306 / 4421, ceiling 12288, headroom 7867 (64.0%)
#      inspector 14905/6211 of 40960 ; dev-reload 2452/1260 of 8192
#
#   CORRECTED AT REVISION 6 — §7.11 correction 1, which is L9-1's FR54-9. True at
#   e751f6de, false at HEAD. At HEAD it prints:
#   -> Shipped gotth-live.min.js 10387 / 4459, ceiling 12288, headroom 7829 (63.7%)
#      inspector 14905/6211 of 40960 ; dev-reload 2452/1260 of 8192
#   The inspector and dev-reload figures did NOT move and are correct as written.

# §2.8 row 1 — the three element attributes are GONE, and the only hits left in
# the tree are the specs asserting their absence. Five hits, no emitters.
grep -rn 'data-gotth-fields\|data-gotth-debounce\|data-gotth-throttle' \
  --include='*.js' --include='*.go' --include='*.templ' live client examples test

# §2.8 rows 2, 3 — failure 3 is IMPLEMENTED, not described. Read the reducer
# case and the binding; then read the comment AS A PARAGRAPH. It wraps, and
# §2.6 row 5 is the third time in this report a wrapped Go comment defeated a
# grep. The paragraph now says the opposite of what it used to say.
sed -n '353p'   examples/chat/chat.go       # case EventClear:
sed -n '107,110p' examples/chat/view.templ  # live.OnAll(OnWith(keydown, EventClear, ...
sed -n '57,80p'  examples/chat/view.templ   # "F-3 is now closed."
grep -n 'F-3 — ' examples/chat/FRICTION.md  # the "— Closed." heading, and the
                                            # section at :287 that refused it,
                                            # kept above with what changed

# §2.8 rows 5, 6 — the browser loop, and ci.sh's re-derived counts.
ls -la test/internal/conformance/dev_reload_test.go test/internal/conformance/inspector_test.go
sed -n '835p;863p;874p' ci.sh        # 43 without GOTTHLIVE_E2E, 50 with it

# §2.8 row 7 — L9-1's five stale current-state size figures. All five still say
# 10,391 / 4,429 against a shipped 10,306 / 4,421. None is PM-1's to fix.
#
# CORRECTED AT REVISION 6 — §7.11 correction 3. ALL FIVE ARE FIXED, and so is the
# sixth. Every one now reads 10,387 / 4,459, which is what `minify -check` prints
# at HEAD. The comment above was true when written and this block asserted it for
# one revision after it stopped being true. The sed line numbers below have also
# drifted, which is §7.8 correction 2 for the third time — LOCATE BY MARKER:
grep -n '10,387\|4,459' README.md docs/guide/deploying.md docs/quickstart.md \
  docs/guide/inspector.md docs/instrumentation.md client/SIZE.md
#   -> six files, and client/SIZE.md's ledger rows are the source the other five
#      copy. inspector.md and instrumentation.md record the WHOLE path,
#      4,429 -> 4,421 -> 4,459, with the commit for each move.
sed -n '113p' README.md
sed -n '24p'  docs/guide/deploying.md        # "re-measured on every landing"
sed -n '161p' docs/quickstart.md             # the probe table a reader compares
sed -n '198p' docs/guide/inspector.md
sed -n '835p' docs/instrumentation.md
sed -n '628p' client/SIZE.md                 # FR54-2, and it is inside the ledger

# §2.8 row 8 — the routed findings, and L9-1's own appended correction.
sed -n '668,684p'  bench/README.md            # R-1: reason 3 died at 2ab18690
sed -n '54,56p'   docs/guide/deploying.md    # FR54-1's residual: protocol only
sed -n '719p'     docs/api-surface.md        # "a finding for PM-1", now discharged
sed -n '587p'     docs/api-surface.md        # L9-1's correction, BENEATH the false row

# §4.6.2 — the Phase 3 act, and the number that did not reproduce.
sed -n '/## 12. The Phase 3 exit gate act/,/### 12.4/p' docs/gates/checkpoint-3.md

# ---------------------------------------------------------------- revision 6

# §2.9 tool rows 1 and 2 — the two numbers this revision quotes as numbers, and
# both of them moved. CONTAINER REQUIRED. `bash -c`, never `bash -lc`.
~/bin/dis run bash -c 'cd tools && go run ./apisurface'
#   -> live 56/56  53/53  109/109 ; live/livetest 37/37 33/33 70/70 ; total 179
#      Identifiers 56 UNCHANGED from revision 5; fields 51 -> 53. That is the
#      whole cost of the accepted surface, measured rather than quoted.
~/bin/dis run bash -c 'cd tools && go run ./minify -check'
#   -> Shipped gotth-live.min.js 10387 / 4459, ceiling 12288, headroom 7829 (63.7%)

# §2.9 tool row 3 / §7.11 correction 1 — THE LANDED PRICE OF THE PART B SHAPE.
# +81 B minified / +38 B gzipped, not the +62 / +34 this report published six
# times. Two things about this block, and both were paid for:
#
#  (1) DO NOT USE THE SHELL'S gzip -9. GNU gzip and Go's compress/gzip are
#      different implementations of the same level: at HEAD, `gzip -9` reads
#      4,415 where tools/minify reads 4,459. A 44-byte disagreement. The tool
#      uses gzip.NewWriterLevel(BestCompression) (tools/minify/main.go, func gz)
#      and THAT is this project's gzip figure. Measuring with the wrong
#      compressor is how a correction pass introduces its own stale number.
#  (2) The container does not mount .git — it mounts gotth-live/ at /workspace.
#      So extract on the host and carry the bytes in, rather than running git
#      inside the image.
#
# First, prove the delta belongs to the Part B landing and to nothing else:
git log --oneline -1 0b9e32e7~1        # -> 42b4e0e6, i.e. Part A complete
git log --oneline -- gotth-live/live/clientjs/gotth-live.min.js | head -4
#   -> 0b9e32e7 is the only commit between Part A and HEAD that touches it
git show --stat --oneline 0b9e32e7 -- gotth-live/live/clientjs/ gotth-live/client/
#
# Then measure both artifacts with the tool's own compressor. Run from the
# REPOSITORY ROOT (git) but invoke dis from gotth-live/:
B64=$(git show 0b9e32e7~1:gotth-live/live/clientjs/gotth-live.min.js | base64 -w0)
# ...then inside the container: write a 20-line main.go that does
#   gzip.NewWriterLevel(&buf, gzip.BestCompression), Write, Close, len(buf)
# over both /tmp/old.js (decoded from $B64) and /workspace/live/clientjs/*.min.js
#   -> old  minified=10306  gzip(BestCompression)=4421
#   -> HEAD minified=10387  gzip(BestCompression)=4459
#   -> DELTA +81 / +38.  Cross-check against the shipped ledgers, which are
#      already correct and were NOT the source of this figure:
grep -n '+81\|+38 gzipped\|1.1.6' client/SIZE.md | head
grep -n 'Client cost' docs/api-surface.md | head

# §2.9 tree row 1 — binding() REFUSES, and it refuses exactly four things.
# Read it as a function; the panic strings wrap and defeat a grep, which is the
# fourth time this report has said that about a wrapped Go source string.
sed -n '/^func refuseUnbindable/,/^}/p' live/templ.go
#   -> ":" or ";" in domEvent | EMPTY eventName | ":" or ";" in eventName
#      | ":" or ";" in any Bind.Keys entry.  Four. FR54-7 would be a fifth.

# §2.9 tree row 2 / Q-8 — the code and the ruling disagree, and the code wins.
grep -n 'Those four and nothing else' live/templ.go
sed -n '/## 22.3/,/^## 23/p' docs/reviews/fr-54.md | head -20
#   -> §22.3 rules an EMPTY domEvent refused; the code refuses an empty
#      eventNAME. Tree self-consistent; ruling is the outlier. FR54-7.

# §2.9 tree row 4 — this report's tree is documentation-only over the graded one.
git diff --stat eb4971c6 9efb7e5b     # -> one file, docs/qa/phase-4-grading.md

# §4.3.4 / §8.5 — QA-1's grade, its four conditions, and the seven things it
# does NOT prove. Read §11.8 and §11.9, not the verdict line.
sed -n '/^### 11.8 What this pass does not prove/,/^### 11.10/p' \
  docs/qa/phase-4-grading.md

# §8.5 — the three defective constraints, in their author's own words, with the
# constraints left unedited above the correction block.
sed -n '/^> \*\*⟨CORRECTED 2026-08-05 — three of the nine/,/^---/p' \
  docs/reviews/fr-54.md
```
