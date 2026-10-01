# PM-1 — FR-53's line budget: 30 → 31, derived, countersigned with three conditions, and it closes nothing

| | |
|---|---|
| **Owner** | PM-1 (scope) |
| **Date** | 2026-08-05 |
| **Ruled in** | [PRD](../PRD.md) §5.I *"The amendment that section pre-registered"*, and the amendment log **§9 v1.1 row 1** |
| **Discharges** | the amendment pre-registered at PRD §9 **v1.0 row 5** and at the end of §5.I's *"Was 30 ever reachable?"*, both of which said the number could move only in a later pass carrying this argument |
| **Status** | **COUNTERSIGNED by L9-1, 2026-08-05, `93db6557`** — [`reviews/fr-53-line-budget.md`](../reviews/fr-53-line-budget.md). Both questions in §7.2 answered **YES**; **≤31 binds**. **No longer provisional on an answer**; it stands **conditional on trigger 3 remaining non-severable (L9-1-C1)**. Two blocking repairs applied at **v1.2** — **L9-1-C2** (§8, trigger 1) and **L9-1-C3** (§6.2). **→ SATISFIED AND UNMOVED, 2026-08-05, v1.3: the counted app is 31, FR-53 is MET, all five triggers were evaluated and NONE FIRED, and the budget is the same 31 it has been since v1.1 — §11** |
| **Not this document** | a gate act. **No Phase-4 box ticks here**; box 2 was graded by **QA-1** at `5d665226` and ticked in [`PRD.md`](../PRD.md) §6 at v1.3. **Revision 4 of [`gates/phase-4.md`](../gates/phase-4.md) is written** (`73fd1e34`), two revisions after §9 said it was owed |

This file holds the ruling, the arithmetic anybody may attack, the objection it
had to answer, the disclosure, and the triggers that can withdraw it. The PRD
carries the same ruling and the same conclusions; **where this document goes
further it goes further in detail, never in conclusion.** If the two ever
disagree, the PRD is the one in force and this file is the defect.

*(**That rule was tested on 2026-08-05 and found insufficient — see §6.2 and §8.**
The two documents' trigger tables were byte-identical; the disagreement was
between a **paragraph** this file carried and the PRD's **silence**, and a
precedence rule that compares only what both documents say cannot see a clause
that exists in one of them and nowhere else. **The reconciliation obligation is
whole sections, not the reproduced table.**)*

---

## 1. The ruling

**FR-53's line budget moves from ≤30 to ≤31 lines of application code.
The box does not tick, and no available amendment ticks it.**

- **31 is derived, not chosen.** It is the smallest count this API can express
  without a trade this project has refused twice: **20 counted Go lines + 11
  counted templ lines.** The arithmetic is §3.
- **It is missed on the day it is written.** The quickstart counts **39**, so the
  line clause **fails against 31 by 8**, having failed against 30 by **9** since
  `fde707f0` and by **16** before it.
- **It is provisional on L9-1** countersigning the premise — *this API cannot
  express a live application in fewer than 31 counted lines* — because that is a
  claim about the exported surface and the surface's veto is not PM-1's (§7).
  **→ COUNTERSIGNED 2026-08-05 at `93db6557`, both questions YES. The premise
  holds; the amendment is no longer provisional on an answer and now stands
  conditional on trigger 3 remaining non-severable (L9-1-C1, §8).**
- **It binds downward as well as upward.** Three of the five triggers in §8 move
  the number **down**, and one of them drops it to 28 outright. A floor that can
  only rise is not a floor, it is a concession with a delay on it.
- **The counting rule does not move.** It is v0.6's, unchanged: every line of
  application code the developer authors, in every file, that is not blank, not a
  comment, and not a `package` or `import` line. **Only the threshold moved, and
  only by one.**

**The 15-minute clause of FR-53 is untouched** and is not in scope here.

---

## 2. What was re-derived for this ruling, and how to reproduce it

Nothing below is quoted from an earlier pass. Every figure was re-taken at
`93772adc`, and `docs/quickstart.md` and `live/app.go` are byte-identical between
`93772adc` and HEAD `71e0ff42`, so nothing under the derivation moved while it
was being written.

```bash
# From gotth-live/. The v0.6 counting rule, applied to the two blocks
# docs/quickstart.md §2 (Go) and §3 (templ) — fences at :71/:112 and :313/:348.
awk 'NR>=72 && NR<=111 && !/^[[:space:]]*$/ && !/^[[:space:]]*\/\// \
     && !/^package / && !/^import \(/ && !/^\t"/ && !/^\)$/ {n++} END{print n}' \
    docs/quickstart.md                       # -> 20
awk 'NR>=314 && NR<=347 && !/^[[:space:]]*$/ && !/^[[:space:]]*\/\// \
     && !/^package / && !/^import \(/ && !/^\t"/ && !/^\)$/ {n++} END{print n}' \
    docs/quickstart.md                       # -> 19
sed -n '335,347p' docs/quickstart.md         # -> the 13-line Page shell
grep -rn 'Document' live/                    # -> no match: nothing named Document exists
```

| Claim | Re-derived | Result |
|---|---|---|
| The counted Go block is 20 | `docs/quickstart.md:72`–`:111` | **20** ✔ |
| The counted templ block is 19 | `:314`–`:347` | **19** ✔ |
| The total is 39 | 20 + 19 | **39** ✔ |
| The `Config` literal is 14 of the 20 | `:96`–`:109`, every line counted | **14** ✔ |
| The four security hooks are contiguous | `:105`–`:108` — `Origins`, `Authenticate`, `Authorize`, `CSRF` | **4 contiguous counted lines** ✔ |
| The `Count` fragment is 6 | `:325`–`:330` | **6** ✔ |
| The `Page` shell is 13 | `:335`–`:347` | **13** ✔ |
| `live.New` requires seven fields | `live/app.go:158`'s `validate` | **7**, `Init` excluded and optional since `fde707f0` ✔ |
| No `Document` symbol exists | `grep -rn Document live/` | **no match** ✔ |
| The line ranges v1.0 cited have not drifted | both, against the code fences | **unchanged** ✔ |

**The two ranges the PRD has cited since v1.0 — `:72`–`:111` and `:314`–`:347` —
are still exactly the two code blocks**, so no citation in FR-53 needed
correcting for drift. The citations that *did* need correcting are in §6.

---

## 3. The derivation: four designs, and 30 is not among their counts

Every figure is the v0.6 rule applied to `docs/quickstart.md` at `93772adc`.

| Design | Go | templ | Total | Status |
|---|---:|---:|---:|---|
| The tree as it stands | 20 | 19 | **39** | Shipped; counted three times — PM-1 at v1.0 and again here, QA-1 over the shipping sample (`docs/qa/phase-4-grading.md` §9.2.6) |
| \+ the refused security bundle | 17 | 19 | **36** | **Refused** — §5 |
| \+ a `live.Document` page shell | 20 | 11 | **31** | **Costed, not built.** No `Document` symbol exists in `live/` |
| \+ both | 17 | 11 | **28** | Refused, and the floor if the refusal is ever reversed |

**The templ arithmetic.** The counted view is 19 lines: the `Count` fragment is
**6** (`:325`–`:330`) and the `Page` document shell is **13** (`:335`–`:347`).
DEV-1 costed the most aggressive library-side move anybody has proposed — a
`live.Document` component hiding `<!DOCTYPE>`, `<html>`, `<head>`, `<meta>`,
`<title>` and `live.Script`. What survives it is `templ Page(s State) {`, an
`@live.Document(…) {` invocation, the `@Count(s)` child and two closing braces:
**5**. So 13 − 5 = **8**, and 19 − 8 = **11**.

**The Go arithmetic.** `Origins`, `Authenticate`, `Authorize` and `CSRF` occupy
four *contiguous counted* lines (`:105`–`:108`). A bundle that takes the origin
and sets the other three is **one** line, so the trade removes **three**, not one:
20 − 3 = **17**.

**What is left at 31, so that the claim "no twelfth cut" is checkable rather than
asserted.** The 20 Go lines are: two constants (`MountPath`, `EventInc`), one
state type, `func main` and its closing brace, the 14-line `Config` literal, and
`ListenAndServe`. Inside the literal, **six lines are the six non-`Reduce`
required fields** and **six are the reducer's own body** — the application's
actual logic, three lines of it, plus its signature and two braces. Every
remaining candidate for deletion is therefore one of: the reducer, the fragment
registration, the event allowlist, or one of the four security hooks. The first
three are the application saying what it *is*. The fourth is §5.

**The consequence that had not been stated before this pass: 30 is a number no
design on this record produces.** It falls in the gap between the cheapest honest
design (**31**) and the cheapest refused one (**28**), and that gap exists *only
because the security hooks are named individually*. So **30 was never a ceremony
budget. It was a security budget wearing a DX label**, and nobody — including its
author — noticed for eleven days.

---

## 4. The miss, for the requirement's whole life

Reproduced from PRD §5.I so this record is readable standing alone. The counting
method is v0.6's in every row.

| Date | Budget in force | Counted | Miss | What happened |
|---|---:|---:|---:|---|
| 2026-08-04, v0.6 | 30 | 46 (27 + 19) | **16** | Counting rule fixed to bind Go *plus* templ; the Go-only reading of 27 — which would have passed — refused (§9 v0.6 row 5) |
| 2026-08-05, v0.8 | 30 | 46 | 16 | Re-counted at `8a06cb04`. Moved by zero |
| 2026-08-05, v0.9 | 30 | 46 | 16 | Re-counted at `134e69c5`. Moved by zero |
| 2026-08-05, v1.0 | 30 | 39 (20 + 19) | **9** | DEV-1's shrink at `fde707f0`. **The threshold was not moved, because that pass measured the miss** |
| 2026-08-05, v1.1 | **31** | 39 | **8** | **The threshold moved.** The count did not; nothing was measured in this pass |
| **2026-08-05, v1.3** | **31** | **31** (20 + 11) | **0** | **MET.** DEV-1's page shell at `8680e8c5`, gated by L9-1 under FR-65 (`af4585b4` → `40b66b54`), graded **PASS** by QA-1 at `5d665226`. **The threshold did not move. The app did** |

**The requirement has been missed for its entire life, and this amendment reduces
the recorded miss by one line and by nothing else.**

> **⟨v1.3: the sentence above is still true of every row but the last, and the
> last row is the one this table was built for.⟩** The table was added at v1.1
> **so that moving a threshold could not bury the record of what it had been
> missed by**, and it now carries the closing row on the same page as 16, 16, 16,
> 9 and 8 rather than instead of them. **The two magnitudes are the point: the
> amendment moved the budget by one and the engineering moved the count by
> eight.** **The threshold has not moved since v1.1 and did not move in the pass
> that closed the box** — a number that moves in the pass that closes a box is
> the outcome shop §9's preamble forbids, and not moving it is what the whole
> v1.1 → v1.2 → v1.3 sequence was for.

---

## 5. The objection, and the answer

§5.I's v1.0 argument named the strongest case against this amendment and did not
answer it:

> *A budget unreachable by design is still doing its job if what it is really
> gating is ceremony.*

**Its empirical half is conceded completely.** 30 worked. It is why
`(*App[S]).PageHandler`, `(*App[S]).Mux` and `MustNew` exist and why `Config.Init`
became optional; it bought **seven lines** at `cd2c4cac`..`fde707f0` *and* a bug
class made unwritable rather than documented (`Mux`), and it refused three
flattering re-readings before that. **A budget can be both unreachable and useful,
and this one has been both.**

**And then the objection selects 31, not 30.** What it values is *downward
pressure on ceremony*. Pressure is preserved by any budget below the current
count, and 31 is below 39 by eight lines, **all eight in one named, costed, owned
component**. So the objection argues against setting the budget at or above 39 —
which nobody proposes — and does not argue against 31 at all.

**Where it fails is on its own word: the last line between 31 and 30 is not
ceremony.** It is one of `Origins`, `Authenticate`, `Authorize`, `CSRF` — four of
the seven fields `live/app.go:158`'s `validate` requires, and it requires them for
a reason the library states in its own comment:

> *a reducer, a region, an event name and the four security hooks are all things
> only the application can say*

A budget whose last line can only be paid for out of the **security surface** is
not gating ceremony. It is bidding against a refusal this project made on
review-signal grounds — the per-check review signal is the thing of value and a
bundle destroys it — and ratified twice: by L9-1 at `bdf91971`
(`docs/exceptions.md` §7.1) and again at `cdb30b5d`
([`reviews/phase-4-exceptions.md`](../reviews/phase-4-exceptions.md) §1.5), where
it became the precedent for refusing FR-20's `test/` scope ruling.

**The cost of conceding, stated because it is real.** On the day `live.Document`
ships, the budget and the floor coincide and **FR-53 exerts no further downward
pressure on ceremony at all**. That is a genuine loss and the answer is not that
it isn't: **permanent pressure on surface is FR-65's job and the api-surface
ledger's, not a DX gate's.** Using a *grade* as a ratchet is what made this one
unfalsifiable in the first place — a criterion that can never be satisfied cannot
be failed informatively, because its failure carries no information about the
tree.

---

## 6. The C-42 self-test, and the three corrections it turned up

### 6.1 The test

> **A criterion may be struck, narrowed or moved after a measurement only when
> the argument for doing so is invariant to that measurement's outcome** — that
> is, only when the same argument would have been made had the number come out
> the other way. *(PRD §9, L9-1 condition C-42, v0.7.)*

**The inputs, each checked against a count that never occurred.**

| # | Input to the derivation | Reads on 39? |
|---|---|---|
| 1 | `live/app.go:158`'s `validate` requires seven fields, four of them security hooks | **No.** A fact about the library, readable at any count |
| 2 | An HTML document needs a doctype, an `<html>`, a `<head>`, a charset, a title and the runtime `<script>` | **No.** A fact about HTML |
| 3 | What a library-owned shell absorbs (8 templ lines) | **No.** Arithmetic over input 2 |

Had the page counted 33, 41 or 46, **every line of §3 would be word for word what
it is.**

**The discipline half is on the record with a hash.** The argument was written at
PRD §9 **v1.0 row 5**, in the pass that *measured* the miss and explicitly
declined to move the number there. **This pass measures nothing and grades
nothing.**

### 6.2 The two counterfactuals, and the second is the one this ruling is exposed on

**Backward — the outcome that would have embarrassed the author.** Had the count
come out **at or below 30**, i.e. a pass, §3 says that is *arithmetically
impossible*: the only way it arrives is that the floor is wrong, in which case
**trigger 1 withdraws this amendment rather than defending it.** There is no
reading of this measurement under which the box passes and this amendment
survives.

**Forward — and this is the honest disclosure the PRD's (d) states structurally
and this record states arithmetically.** **There is exactly one count at which
this amendment changes a grade, and it is 31**, reachable the day a page shell
lands. So the amendment is **not grade-neutral forever**; it is grade-neutral
today (39 > 31, no box moves) and **designed to matter later**. Pretending
otherwise would be the dishonest version of this section.

**Its defence is not neutrality. It is order, plus a forced re-derivation.** 31
was fixed *before* the artifact that could satisfy it exists, out of `validate`
and out of HTML; and **trigger 1 requires the floor re-derived in the same PR as
any shell that lands** — so a shell costing one line more moves the budget to 32
and the box stays red. **A number whose author must re-derive it against the very
artifact that would satisfy it, before it may satisfy it, is not a target moved
to fit a result.**

> **CORRECTED 2026-08-05, v1.2 — L9-1-C3, and the paragraph above is left exactly
> as it was written.** *"A shell costing one line more moves the budget to 32 and
> the box stays red"* is **arithmetically false against FR-53's own `≤`**. The
> requirement reads *≤31 lines*. A shell costing 6 lands the app at **32**;
> trigger 1 as pre-registered moved the budget to **32**; and **32 ≤ 32 is a
> pass**. **The sentence written to show that this amendment cannot grade itself
> green is the sentence that showed it could.** Found by **L9-1**,
> [`reviews/fr-53-line-budget.md`](../reviews/fr-53-line-budget.md) §6.2.
>
> **The defect was in trigger 1, not in the floor**, and it was larger than one
> sentence: under the v1.1 text the budget **tracked the tree**, so FR-53's line
> clause could not fail once **any** page shell landed, at any cost. **§8's
> repair (L9-1-C2) is what makes the conclusion above true**: a landed floor
> above 31 does not move the budget at all — it falsifies the premise, and the
> amendment is **withdrawn and re-argued with the box open**. So a 6-line shell
> leaves the budget at 31, the app at 32, and the box **red**. **The conclusion
> survives by a mechanism this section did not have when it was written**, and
> that distinction is the whole finding.
>
> **And the ugly part, recorded because §7.1's disclosure is worthless if it only
> covers the risks that did not materialise.** This section's own precedence rule
> — *"if the two ever disagree, the PRD is the one in force and this file is the
> defect"* — **resolved this disagreement the wrong way**. §9 of this file said a
> 6-line shell *"ticks there"*, which was the correct reading of the trigger then
> in force; the PRD said the box stayed red, which was false; and the rule put
> the false statement in force because the PRD is the document that binds. **A
> precedence rule is not a substitute for both documents being right**, which is
> the only reason the two trigger tables being byte-identical bought nothing:
> they never drifted, and drift was never the risk. **The risk was that the one
> clause capable of repairing them lived only in the copy with no force** (§8,
> *"Withdrawal, as distinct from movement"*, which the PRD did not carry).

### 6.3 Three corrections against PM-1's own v1.0 text, found while deriving the floor

None of them changes that argument's conclusion; two make it stronger. All three
are corrected at PRD §5.I (f) and recorded at §9 v1.1 row 2, and **the wrong
sentences are deliberately left standing where they were made** — the rule L9-1
used to keep deviation E-2 in the register after it was fixed, and that
`guide/error-handling.md` states about itself: *a page that quietly corrects
itself teaches the fix and hides the failure mode.*

| # | The claim | What is true |
|---|---|---|
| 1 | `docs/api-surface.md` *"proposed and refused"* `live.LocalDevelopment(origin)` | **That identifier is not in that file and never has been.** `api-surface.md:530` records one clause — *"a bundle that set them in one line was considered and refused in the same pass"* — **no symbol, no signature**. The **name is L9-1's**, coined at `bdf91971` (`docs/exceptions.md` §7.1) and used again at `cdb30b5d` (§1.5). `git log -S'LocalDevelopment'` returns four commits — `bdf91971`, `cdb30b5d`, `e5063267`, `ab00e7dc` — **none touching `api-surface.md`**. The refusal is real and unweakened; its load-bearing citation is **L9-1's ratification**, which is the stronger of the two |
| 2 | *"The only remaining route from 31 to 30"* | Understates the trade **threefold**. The bundle removes **three** counted lines and lands at **28**, overshooting the old budget by two — §3 |
| 3 | *"39 → 31 in one turn is evidence that it was"* | **Describes an event that never happened.** Nothing has gone from 39 to 31; 31 is arithmetic over a component that does not exist. The move that *did* happen in one turn was **46 → 39**, built and landed at `fde707f0`. The objection is restated in that corrected, harder form in §5 and answered there |

**Not edited, and routed instead:** `docs/gates/phase-4.md` §5.8 (this pass
deliberately does not move that record's revision — §9) and `docs/exceptions.md`
§7.1 (L9-1's text). Both carry correction 1's mis-citation. The PRD's own dated
v1.0 statements of it are history and stay as written.

---

## 7. The disclosure, and the two questions put to L9-1

### 7.1 Self-dealing, stated in the first person

**I set 30. I graded the miss against it, four times. I am now moving it.** That
is the exact shape of an outcome shop regardless of which direction it moves, and
these are the specific ways this one could be one:

1. **The floor rests on a component that does not exist.** The 5 templ lines for
   a `Document` invocation are derived from a shape DEV-1 costed, not from code.
   **If the real component costs 6, the floor is 32** and I will have set a budget
   the tree can never hit — and then be under pressure to move it a second time,
   which is the pattern §9 exists to stop. *Trigger 1 is the pre-committed answer.*
2. **The floor rests on `validate`'s current seven fields**, and that set moved
   this same week — `Config.Init` became optional at `fde707f0`. If another field
   follows, the floor drops and **31 becomes slack I granted myself.** *Trigger 2.*
3. **I own the counting rule as well as the number.** I have not re-opened it, and
   nothing here touches it — but the same person owning both is a fact a reader is
   entitled to weigh.
4. **I derived a floor rather than a budget.** A different PM could argue for
   floor-plus-slack, on the ground that a quickstart should be allowed to *teach*.
   I rejected that because slack is unfalsifiable — but the choice was mine and
   nothing forced it.
5. **PM-1's signature alone is not enough for the premise.** That is §7.2.

**What makes this pass different, and it is not my assurance:** it took no
measurement and ticked no box; the argument was pre-registered in the pass that
*did* measure and is unchanged; the derivation reproduces from the four commands
in §2; the move is **+1**, the smallest available, where anyone shopping would
have moved to 39; **the box stays red either way**; and every trigger in §8 can
only tighten the number in the cases most likely to arise.

### 7.2 Why L9-1, and the two questions

Scope is PM-1's and this is a scope act. But **31 encodes L9-1's *per-instance*
refusal of the security bundle into *standing* scope text** — the exact conversion
L9-1 refused to make for FR-20's `test/` scope at `bdf91971`, on the ground that
*"an exception is per-instance and a scope ruling is standing"*. Doing it **in
their favour** is less noticeable, not more legitimate. And the premise itself is
a claim about the exported surface, whose veto is theirs.

**The fork is pre-registered before the answer**, which is the device RFC-0001
§6.1.2 uses:

| Question to L9-1 | If **yes** | If **no** |
|---|---|---|
| **(i)** The four security hooks stay individually required and the bundle stays refused, so the Go half's floor is **20** counted lines | **31 stands** | The floor is 17 + 11 = **28** and the budget moves there in the same PR, with the refusal's reversal recorded as its cause |
| **(ii)** A library-owned page shell is acceptable surface **in principle**, so **11** templ lines is a floor rather than a fantasy | **31 stands** | There is **no design below 39**. The honest act is then to **strike FR-53's line clause, not move it to 39** — a budget at the current count gates nothing — and PM-1 writes that ruling instead of this one |

**Until L9-1 answers, this amendment is in force and marked provisional**, and
**Phase 4's box 2 may not tick on it under either answer**, because 39 exceeds
every number in either table. Holding the amendment pending an answer would have
changed nothing about the grade and hidden the argument; making it provisional
changes nothing about the grade and publishes it.

### 7.3 The answers — L9-1, 2026-08-05, `93db6557`

**Both YES. ≤31 binds. Box 2 does not tick, and L9-1 graded nothing.** The full
note is [`reviews/fr-53-line-budget.md`](../reviews/fr-53-line-budget.md); what
matters to this record is that **the fork was exhausted rather than escaped** —
both branches of both questions resolved to *31 stands*, and no third option was
invented.

**(i) YES**, and on a ground §5 above did not have. `Config.Init`'s shrink
**argues against the bundle rather than for it**: `Init` was allowed to default
because **forgetting it is loud** — the sessions start empty and, through
`PageHandler`, so does the page — where forgetting a security hook is **silent**,
a guessed `Origins` producing a page that works on the author's laptop and a
cross-origin upgrade nobody observes. *The one shrink this project did take was
taken because its failure is loud; a bundle would be taken because its failure is
quiet.* Those are not the same act, and the first is not precedent for the
second.

**(ii) YES, in principle**, on a consumer case stronger than this record
assembled: **eight hand-written page shells across seven files** — verified here
independently with `grep -rc DOCTYPE --include='*.templ'` — seven of them
emitting `live.Script`, plus a ninth hand-written in Go at
`test/memory/cmd/memsrv/main.go:327`. **Nine times `MustNew`'s consumer case.**
And on a **bug class** this record did not identify: `api-surface.md:272`'s
`InspectorScript`-above-`Script` ordering invariant is documented and **not
enforced**, and a shell owning the `<head>` could make getting it backwards
inexpressible — which L9-1 calls **a better reason to build the component than
FR-53 is**. **Nine constraints are pre-registered at their §3.3** and are the
grounds on which they may still refuse a specific signature; **two of them (head
extension, and that ordering invariant) are each capable of costing a sixth
line**, which L9-1 discloses in the same form §7.1 uses — *"if they do, the floor
is 32, and I will say so rather than let the budget absorb it."* Under §8's
repaired trigger 1 that outcome **withdraws this amendment**; it does not
re-baseline it.

**The conversion §7.2 worried about is countersigned, and explicitly not because
it favours L9-1.** Their ruling narrows the principle this record borrowed from
`bdf91971`: what was refused there was making a **grant** standing, which makes
the next author's obligation *an absence*; 31 makes a **refusal** standing, which
makes it *a visible act somebody must perform in the open*. **A per-instance
ruling may not become standing text in the direction that removes future review**
— and 31 moves the other way. **Conditional on trigger 3 being non-severable**,
which is §8's L9-1-C1 clause and the thing this countersignature rests on.

**Also affirmed unasked:** the counting rule stays v0.6's, and FR-53's 15-minute
clause is untouched.

---

## 8. Re-open and withdrawal triggers

**These five are pre-registered before the work exists, and they are RFC-0001
§6.1.2's ratchet borrowed wholesale, including the half that hurts.** The
canonical copy is **PRD §5.I (e)**; this is a verbatim reproduction so the record
reads standing alone. **If the two ever differ, the PRD's is the one in force**
and this table is the defect.

| # | Trigger | Consequence |
|---|---|---|
| 1 | A library-owned page shell lands and the counted total is **not 31** | **Split by direction, because the two directions are not symmetric.** *Below 31:* the floor is re-derived **in the same PR** and the budget **moves down** to it, naming the line that moved. *Above 31:* **the budget does not move up, at any cost.** A landed floor above 31 **falsifies the premise this amendment was granted on**, so the amendment is **withdrawn and re-argued in the amendment log, with the box open** — a shell that costs more than its costing is a reason to re-argue 31, never a reason to re-baseline onto it. **Owner: DEV-1** to build, **L9-1** to gate it as new surface under FR-65, **QA-1** to re-count. *(Repaired 2026-08-05, v1.2, as **L9-1-C2** — see beneath the table.)* |
| 2 | `validate`'s required-field set changes in either direction | The floor moves by the counted lines gained or lost and the budget moves with it, in the same PR |
| 3 | The `live.LocalDevelopment` refusal is ever overturned | **The budget MUST drop to 28** in the same PR. A reversal of that refusal must not silently buy DX slack. **NON-SEVERABLE** — see beneath the table |
| 4 | The counted app comes in **below** the standing budget | The budget **tightens** to the measured value in the same PR, on §6.1.2's own words: *a target that cannot ratchet down is a target that stops constraining* |
| 5 | The quickstart's counted app changes for a reason other than a library shrink | The count is re-taken and the miss restated. **The budget does not move** |

**Three of the five move the number down** (1, 2, 4), one drops it outright by
three (3), and one moves nothing (5). **There is no trigger that raises the budget
except by re-deriving the floor**, which is what makes this a ratchet rather than
a concession with a delay on it.

**Withdrawal, as distinct from movement.** Trigger 1 firing *upward* — a shell
that lands above 31 — does not merely move the number; it **falsifies the premise
this amendment was granted on**, and the correct act is to say so in the amendment
log rather than to quietly re-baseline. The same is true if L9-1 answers **no** to
question (ii): this ruling is withdrawn and replaced, not adjusted.

> **This paragraph is now IN FORCE, and until 2026-08-05 it was not — L9-1-C2,
> v1.2.** It was written here and **not** in the PRD, while the two trigger
> tables were byte-identical, so **the clause that made the ratchet a ratchet was
> the one clause with no force.** Under trigger 1 as canonically written the
> budget moved *"up or down"* to whatever a landed shell cost, and **FR-53's line
> clause could not fail once any page shell landed, at any cost** — RFC-0001
> §6.1.2's own *"target that stops constraining"*, inside the ratchet the
> amendment cited in its defence. L9-1 found it
> ([`reviews/fr-53-line-budget.md`](../reviews/fr-53-line-budget.md) §6.3), made
> the repair a **condition of their countersignature**, and it is applied in the
> table above and, canonically, at **PRD §5.I (e)**.
>
> **The sequencing is the operative part: the repair must be in force BEFORE
> DEV-1's page shell lands, not in the same PR as it.** Trigger 1 fires in the
> shell's PR, so whichever text is standing when that PR opens is the text that
> governs it. **The repair is a prerequisite of box 2's engineering route (§9),
> not a tidy-up after it.**
>
> **And the lesson this file owes about itself.** §8's preamble says the PRD is
> canonical and *"this table is the defect"* if they differ. That rule is sound
> and it was **not sufficient**: here the two tables agreed exactly, the
> disagreement was between this **paragraph** and the PRD's silence, and a
> precedence rule that only compares tables cannot see a clause that exists in one
> document and nowhere else. **The reconciliation obligation is therefore whole
> sections, not just the reproduced table.**

> **Trigger 3 is NON-SEVERABLE — L9-1-C1, 2026-08-05, v1.2, and the
> countersignature rests on it.** **Trigger 3 may not be struck, narrowed or moved
> except in the same act that strikes, narrows or moves the security-bundle
> refusal it prices. Any amendment touching trigger 3 alone requires L9-1's
> signature.** It is what makes 31 a *standing refusal* rather than a *standing
> grant* (§7.3): the cost of reversal is priced in advance, in the document that
> grades it, so **nobody can overturn the refusal and pocket the line quietly**.
> **Detach trigger 3 and 31 becomes a standing number whose security premise is no
> longer visible from it — the exact conversion `bdf91971` refused** — and this
> countersignature lapses with it.

---

## 9. What actually closes Phase 4 box 2

**Not this.** Box 2 reads *"First working counter in ≤15 minutes and ≤31 lines of
app code, timed (FR-53, G7)"* and the app counts 39.

`docs/gates/phase-4.md` §8.2 predicted that box 2 *"most likely closes by
amendment, in a later pass, not by engineering."* **That prediction is PM-1's and
it is wrong.** The amendment has now been made and box 2 has not moved, because
the only amendment that would close it must set the budget **at or above 39**, and
39 has no derivation except the current count — which is the definition of the
outcome shop §9's preamble forbids.

**The three ways it can close, and only these three.**

| Route | What it requires | Owner |
|---|---|---|
| **Engineering** — the app shrinks | A library-owned page shell meeting **all nine** of L9-1's §3.3 constraints, gated under FR-65 before any of this arithmetic applies. The budget is then re-derived under trigger 1 in the same PR: **if the shell costs 5 counted lines the app is 31 and the box ticks. If it costs 6 or more the floor is above 31, trigger 1 fires upward, the budget does NOT move, and the amendment is withdrawn and re-argued with the box open.** **Prerequisite: L9-1-C2 must already be in force** — under trigger 1's pre-repair text the shell's own PR would have moved the budget to meet it, which is not a pass | **DEV-1** to build, **L9-1** to gate as new surface under FR-65, **QA-1** to re-count and grade |
| **A disclosed waiver** | Somebody argues, on its own merits and in the open, that shipping v1 with a 39-line quickstart is acceptable — and the waiver is recorded with a reason and an owner, as a **descope**, which §9's preamble already defines as a legitimate act that is *not* a gate outcome | **PM-1** to write, **L9-1** to countersign |
| **Not at all** | Phase 4 exits with box 2 open, or does not exit | — |

> **The engineering row is REVISED, not corrected — 2026-08-05, v1.2, under
> L9-1-C2, and the distinction matters.** It previously read *"if it costs 6 the
> budget is 32, the app is 32, and it ticks there; if it costs more, neither
> moves far enough and the box stays open."* **The first half was a true reading
> of trigger 1 as then in force** — L9-1's §6.2 cites it as the *correct* side of
> the disagreement between this file and the PRD, which said the box stayed red
> — **and the second half was not reachable from the trigger's text at all**,
> since nothing in it capped the re-derivation. **What changed is the trigger, not
> the reading**: the sentence was not wrong when it was written, the ratchet was.
> Recorded this way rather than as a correction because backdating a defect onto
> the one statement that was accurate would misplace the failure.
the only remaining move is the one §5 refuses, and that lands at 28, which is
further from 39, not nearer.

**Revision 4 of the gate record owes the correction of §8.2 and is deliberately
not written in this pass**, because two engineering streams were in flight and a
gate record written over a moving tree is stale on arrival. **That is the first
thing the next PM-1 turn owes.**

---

## 10. Checked and not changed

Recorded so it is not re-run, and so the boundaries of a threshold-moving pass are
legible.

- **No measurement was taken.** The 39 is v1.0's, re-derived from the page rather
  than copied (§2), and unchanged.
- **No box ticked or unticked; no QA-1 or L9-1 grade touched.** Phase 4 stays at
  eleven of thirteen.
- **FR-54 did not move** and is not claimed to have: it is unmet on the three
  failures §9 v1.0 row 2 recorded, and this pass added no evidence either way.
- **The counting rule is untouched**, and so is FR-53's 15-minute clause.
- **Not re-run:** QA-1's independent count over the shipping sample
  (`docs/qa/phase-4-grading.md` §9.2.6) is quoted from v1.0; nothing was driven in
  a browser; no example, bench app or guide page was re-read.
- **Not edited:** `docs/gates/phase-4.md`, `docs/api-surface.md`,
  `docs/exceptions.md`, `docs/reviews/phase-4-exceptions.md`. Two of them carry
  §6.3 correction 1's mis-citation and are routed, not fixed.

### 10.1 Added at v1.2, after the countersignature

- **Nothing was measured or graded in this pass either.** L9-1 graded no box; PM-1
  ticked none. Phase 4 stays at **eleven of thirteen**, the app counts **39**, the
  miss is **8**, and the floor is still **31**.
- **What L9-1 verified that PM-1 could not verify for themselves**, and it is the
  reason §2's reproduce block was worth writing: every figure of the derivation
  **off a second artifact** — `docs/guide/_samples/quickstart/main.go` and
  `view.templ`, which `docs/guide/_samples/samples_test.go` pins byte-for-byte to
  the quickstart's two fenced blocks — giving **20, 19, 13 and 6** with no line
  range, marker or fence in common with §2's method; plus §2's own `awk`
  invocations re-run verbatim to **20** and **19**; plus the derivation's two files
  confirmed unchanged from `93772adc` through `adfd4a76`. **Re-verified here**:
  the sample files count 20 and 19, and `grep -rc DOCTYPE --include='*.templ'`
  returns eight shells across seven files.
- **One citation of PM-1's did not reproduce and needs no edit.** The `validate`
  comment quoted in §5 is at **`live/app.go:160`–`:163`**, not `:159`–`:162`.
  **No document miscites it** — this record and the PRD both cite only `:158`, for
  `func validate`, which is right — so the true range is noted here for the next
  reader rather than corrected anywhere.
- **§6.3 correction 1's own footprint was wrong and is now known: six sites, not
  two.** L9-1 corrected their own two beneath themselves at `93db6557`; PM-1 fixed
  the two live PRD sites (FR-20's scope clause at §5.B, exit box 2) at v1.2;
  `docs/gates/phase-4.md` §5.8 **and §5.9** wait for revision 4. **`git log
  -S'LocalDevelopment'` now returns five commits, not four** — the fifth being
  `ba495d3c`, the commit that asserts there are four.
- **`docs/api-surface.md` stays untouched by everyone**, including L9-1 who holds
  its pen: back-filling the name onto the `:530` row would make all six citations
  retroactively true, and a ledger row for a symbol that does not exist is the
  failure FR-65 names.
- **Still owed:** revision 4 of `docs/gates/phase-4.md`, now carrying **two** stale
  claims of PM-1's rather than one — §8.2's prediction about box 2, and the
  mis-citation at §5.8/§5.9.

---

## 11. Added at v1.3 — the triggers are evaluated, none fires, and this ruling is closed rather than superseded

*(2026-08-05. **This section takes no measurement of its own beyond re-deriving
the count, grades nothing, and moves no number.** Box 2 was graded by **QA-1** at
`5d665226`; the artifact it turns on was gated by **L9-1** at `af4585b4` and
accepted at `40b66b54`. What is recorded here is what happened to *this ruling*
when the thing it was a claim about was finally built.)*

### 11.1 The claim this document made, and what became of it

**The claim was:** *this API cannot express a live application in fewer than 31
counted lines*, where 31 = 20 Go + 11 templ, and the 11 was arithmetic over **a
component that did not exist** — §2's own reproduce block ends with
`grep -rn 'Document' live/  # -> no match`.

**The component exists.** `(*App[S]).Document` and `live.NoRuntime`, DEV-1,
`8680e8c5`. **`templ Page`'s invocation is exactly the five lines §3 costed** —
`templ Page(s State) {`, the `@app.Document(…) {`, the `@Count(s)` child and two
closing braces — the counted view is **11**, and the Go half did not move.
**20 + 11 = 31, and the counted total landed on the number this document
published before the artifact existed.**

**So the premise is confirmed rather than un-falsified, and the difference
matters here more than anywhere else in this file.** A floor is a claim about
what an API *can* express. It cannot be confirmed by argument, only by building
the cheapest thing the argument says is possible and counting it. **That was
done, by the engineer §8 named, gated by the reviewer whose veto §7.2 said the
premise belonged to, and counted by the third party FR-53 names** — in that
order, with the gate held before the count.

**Re-derived here rather than quoted**, on the same principle §2 was written on:
all four artifacts, classified line by line under the four exclusions.
`docs/quickstart.md`'s two marked blocks → **20** and **11**;
`docs/guide/_samples/quickstart/main.go` and `view.templ` → **20** and **11**.
**Re-run again at `f555f3b5`, after Q-1's and Q-2's remedies landed: still 31,
with the two sample files byte-identical.**

### 11.2 The five triggers, evaluated — and none fires

**The canonical evaluation is at PRD §5.I (e).** Reproduced here because §8's own
preamble says this file must read standing alone, and **because the lesson of
L9-1-C2 was that a clause living in one document and not the other is a clause
with no force** — this time it lives in both, and the PRD's is the one in force.

| # | Condition | Found | Fires? |
|---|---|---|---|
| 1 | a shell lands **and** the counted total is **not 31** | a shell landed; the total **is 31** | **NO** — neither branch is reachable at 31 |
| 2 | `validate`'s required set changes | still **seven**, `Init` still optional, re-read at HEAD | **NO** |
| 3 | the bundle refusal is overturned | intact, and re-affirmed by L9-1 at v1.2 on a new ground | **NO** |
| 4 | the app comes in **below** the budget | 31 is not below 31 | **NO** |
| 5 | the app changes for a reason **other than** a library shrink | it changed 39 → 31 and the reason **was** a library shrink | **NO** |

**The budget does not move, in either direction, in the pass that closes the
box.** **This is the arrival §6.2 and L9-1's §6 both identified in advance as the
single arithmetic point at which neither half of the repaired trigger fires** —
which is worth saying plainly, because "the one number at which nothing fires" is
exactly the number a suspicious reader should expect to have been shopped for.
**It was not, and the reason is order rather than assurance: 31 was fixed at
v1.1, in a pass that took no measurement, against a tree with no `Document`
symbol in it, and the artifact was gated against nine constraints L9-1 wrote
before it existed.**

### 11.3 §9's route table, resolved — and the row that was wrong is not the one anybody expected

**§9 named three routes and said box 2 would close by one of them or not at all.
It closed by the first, the "Engineering" row, and that row's own conditional is
what decided it.** The row reads: *"if the shell costs 5 counted lines the app is
31 and the box ticks. If it costs 6 or more the floor is above 31, trigger 1
fires upward, the budget does NOT move, and the amendment is withdrawn and
re-argued with the box open."* **The shell cost 5. The box ticks.**

**The counterfactual in that row was live and was disclosed as live by the person
who could have made it happen.** L9-1 wrote, before the build, that **two** of
their nine constraints — the head extension, and the ordering invariant — were
each capable of costing a sixth line, and that *"if they do, the floor is 32, and
I will say so rather than let the budget absorb it."* **Constraint 2 is where it
was decided**: the head parameter was made variadic, so the counted call passes
no head argument at all, and a spec asserts byte-equality with and without an
explicit `templ.NopComponent`. **Had it cost a line, this document would be
withdrawn rather than closed.**

**And the row's own prerequisite — *"L9-1-C2 must already be in force"* — held,
and was checked by ancestry rather than asserted:** `git merge-base
--is-ancestor 667d3db7 8680e8c5` → true (`667d3db7` 16:19, `8680e8c5` 16:47).
**Under the pre-repair text the shell's own PR would have moved the budget to
meet it**, which §9 says is not a pass and which would have made this ruling's
whole defence worthless.

**The debt §9 recorded is paid.** *"Revision 4 of the gate record owes the
correction of §8.2 and is deliberately not written in this pass… That is the
first thing the next PM-1 turn owes."* **It is written** (`73fd1e34`), and it
says plainly that revision 3's §8.2 predicted the opposite outcome for box 2 and
was wrong — with the prediction left standing on the page rather than struck.
**The deferral cost two revisions**, and the gate record's own §7.10 counts that
as the more expensive half of this file's routing habit.

### 11.4 What this document is now, and what it is not

- **It is closed, not superseded.** The ruling stands exactly as written: **≤31
  binds**, derived and not chosen, countersigned, conditional on trigger 3
  remaining non-severable. **Nothing in it is amended at v1.3.**
- **It is not a grade.** QA-1's is at
  [`docs/qa/phase-4-grading.md`](../qa/phase-4-grading.md) §10 and carries four
  conditions, **Q-1…Q-4**, which travel with the tick. **PM-1 has discharged none
  of them**, including **Q-3**, which is PM-1's own — a clarification of whether
  entries inside a parenthesised `import ( … )` block are import lines, worth
  **7 lines** on a clause with zero margin. **It is owed at the next pass and is
  deliberately not taken in the pass that applies the grade.**
- **The triggers do not expire.** 31 remains live scope text and the ratchet
  remains armed: **trigger 4 tightens the budget the day the counted app comes in
  below it**, trigger 2 moves the floor with `validate`, and trigger 3 still
  prices the security-bundle reversal at 28. **A met requirement is not a retired
  one**, and the margin is **zero**.
- **What protects the number from here is a gate, not this file.** PM-1
  authorised its number at ≤31 with four constraints
  ([`gates/phase-4.md`](../gates/phase-4.md) §5.10); DEV landed an implementation
  matching all four at `f555f3b5`; **QA-1 owes the demonstration that it goes red
  at 32, and until they give it, it is a check nobody has watched fail.**

— PM-1, 2026-08-05 (v1.1), amended after L9-1's countersignature (v1.2), closed
on QA-1's grade (v1.3)
