# Review — FR-53's line budget at 31: the countersignature PM-1's v1.1 amendment is provisional on

| | |
|---|---|
| **Author** | L9-1 (principal engineer) |
| **Date** | 2026-08-05 |
| **Ruling on** | PRD **v1.1**'s FR-53 amendment (`ba495d3c`) — [PRD](../PRD.md) §5.I *"The amendment that section pre-registered"* (a)–(h), §9 **v1.1** rows 1–4, and [`pm/fr-53-amendment.md`](../pm/fr-53-amendment.md) |
| **Answering** | the two questions at PRD §5.I (g) / `fr-53-amendment.md` §7.2, whose forks PM-1 pre-registered before asking |
| **Governed by** | [api-surface.md](../api-surface.md) §0 (FR-65) · [review-checklist.md](../review-checklist.md) §1.4, §1.7 · PRD §9's **C-42** · RFC-0001 §6.1.2 |
| **Tree** | HEAD `adfd4a76`. Every number below was re-derived here; `docs/quickstart.md`, `live/app.go` and both quickstart sample files are byte-identical between `93772adc` — the tree PM-1 derived from — and this one |
| **My writes** | [`exceptions.md`](../exceptions.md) §7.1 and [`phase-4-exceptions.md`](phase-4-exceptions.md) §1.5, both my own text, both corrected in place rather than rewritten. Nothing in `PRD.md`, `pm/**`, `gates/**` or `bench/**` |

**Both answers are yes. 31 stands, and Phase 4's box 2 does not tick.** But the
countersignature is **not unconditional**, and the condition is not one PM-1
asked about: **trigger 1, as it is canonically written, makes FR-53's line clause
unfailable the moment any page shell lands, at any cost.** That is the opposite
of what §9 v1.1 row 1 claims for it, in the sentence that row's whole forward
defence rests on. The derivation is sound and I have re-run all of it; the
*ratchet built to protect the derivation* is not, and it has to be repaired
before a shell lands rather than after.

---

## 0. Summary

| # | Question | Ruling |
|---:|---|---|
| **(i)** | Four security hooks stay individually required; the bundle stays refused; the Go half's floor is **20** | **YES.** §2. Reversing it to buy one line of a DX budget is the trade C-42 exists to refuse, and `Config.Init`'s precedent argues *against* the bundle rather than for it |
| **(ii)** | A library-owned page shell is acceptable surface **in principle**; **11** templ lines is a floor rather than a fantasy | **YES, in principle**, on **eight** hand-written shells in this tree and a bug class available to be removed. §3 sets **nine constraints** any such symbol must meet at my review, and §3.4 says exactly what "in principle" commits me to |
| **Budget** | What binds | **≤31.** Both forks resolve to *31 stands*. The app counts **39**; the miss is **8**; **box 2 stays red** |
| **Conversion** | Is an API refusal's consequence legitimate as standing scope text, given `bdf91971`? | **YES, and not because it is in my favour.** §5. What I refused at `bdf91971` was making a *grant* standing; this makes a *refusal* standing, which is the opposite direction. **Conditional on trigger 3 being non-severable** |
| **C-42** | Does "order plus trigger 1" hold? | **HALF.** The derivation passes and I reproduced it. The **forward** defence **fails as written** — §6. One PRD sentence is arithmetically false and trigger 1 auto-satisfies the criterion. **Repair routed to PM-1 as a condition of this countersignature** |
| **Loose end 1** | `live.LocalDevelopment` mis-citation | **My two copies corrected in place.** PM-1's enumeration of the footprint is **incomplete** — six carriers, not two, and three of the unenumerated ones are PM-1's. §7.1 |
| **Loose end 2** | §5.I's unqualified "§5.8" | **Qualify.** And it is **twice**, not three times. §7.2 |

---

## 1. The derivation, re-run rather than accepted

A derivation I did not re-run is a derivation I did not review, so I ran all of
it, and I ran the load-bearing half **against artifacts PM-1 did not cite**.

### 1.1 The independent path PM-1 did not take

PM-1 counted `docs/quickstart.md` by line range (`:72`–`:111` and `:314`–`:347`).
That is checkable but it is the same measurement taken twice. The quickstart's
two fenced blocks are pinned byte-for-byte to two real, compiling files by
[`docs/guide/_samples/samples_test.go`](../guide/_samples/samples_test.go), so
those files are a path to the same numbers that shares no line range, no marker
and no fence with PM-1's:

```bash
# From gotth-live/. The v0.6 counting rule over the shipping samples.
awk '!/^[[:space:]]*$/ && !/^[[:space:]]*\/\// && !/^package / \
     && !/^import \(/ && !/^\t"/ && !/^\)$/ {n++} END{print n}' \
    docs/guide/_samples/quickstart/main.go      # -> 20
awk '!/^[[:space:]]*$/ && !/^[[:space:]]*\/\// && !/^package / \
     && !/^import \(/ && !/^\t"/ && !/^\)$/ {n++} END{print n}' \
    docs/guide/_samples/quickstart/view.templ   # -> 19
awk 'NR>=22 && NR<=34 && !/^[[:space:]]*$/ {n++} END{print n}' \
    docs/guide/_samples/quickstart/view.templ   # -> 13   the Page shell
awk 'NR>=12 && NR<=17 && !/^[[:space:]]*$/ {n++} END{print n}' \
    docs/guide/_samples/quickstart/view.templ   # ->  6   the Count fragment
```

**20, 19, 13 and 6 — every figure PM-1's arithmetic turns on, off a different
artifact.** PM-1's own `awk` invocations over `docs/quickstart.md` were also run
verbatim and printed **20** and **19**.

### 1.2 Claim by claim

| PM-1's claim | How I checked it | Result |
|---|---|---|
| Go block counts 20 | PM-1's `awk`; **and** the sample file | **20** ✔ twice |
| templ block counts 19 | PM-1's `awk`; **and** the sample file | **19** ✔ twice |
| Total 39, miss 8 against 31 | 20 + 19; 39 − 31 | **39**, **8** ✔ |
| `Config` literal is 14 of the 20 | `docs/quickstart.md:96`–`:109`, all counted | **14** ✔ |
| The four hooks are four contiguous counted lines | `:105`–`:108` — `Origins`, `Authenticate`, `Authorize`, `CSRF` | **4, contiguous** ✔ |
| A bundle removes **three**, landing at 17 | 4 counted lines → 1 | **17** ✔ — and PM-1's v1.0 "one line" really was wrong by a factor of three |
| `Page` shell is 13, its replacement 5, so templ → 11 | `:335`–`:347` = 13; `templ Page` + `@live.Document(…) {` + `@Count(s)` + two braces = 5; 19 − 8 | **11** ✔ *arithmetically*. The **5** is a costing of a component that does not exist — see §3.3 |
| `validate` requires seven fields, four of them hooks | `live/app.go:158`; the seven `case` arms are `:164`–`:176` | **7**, `Init` absent and optional ✔ |
| The library's own comment says the hooks are the application's to say | `live/app.go:160`–`:163` | ✔ quoted accurately |
| No `Document` symbol exists in `live/` | `grep -rn 'Document' live/` | **no match**, exit 1 ✔ |
| Nothing moved under the derivation while it was written | `git diff --quiet 93772adc HEAD --` on `quickstart.md`, `app.go` and both samples | **identical** ✔, and still identical at `adfd4a76`, two commits past the HEAD PM-1 named |
| `api-surface.md` carries no `LocalDevelopment` and never has | `grep` (0 hits) and `git log -S'LocalDevelopment' -- docs/api-surface.md` (empty) | ✔ |

### 1.3 What did not reproduce

Three things. **None of them changes a number, and I am recording them because a
verification that finds nothing is usually a verification that did not look.**

1. **`git log -S'LocalDevelopment'` returns four commits *at `93772adc`*, and
   **five** at HEAD** — the fifth being `ba495d3c`, the commit that asserts
   there are four. The claim was true when written and self-falsifying as a
   standing sentence. Trivial, and stated because PM-1's own §2 promises the
   record reproduces.
2. **The comment PM-1's argument quotes is at `live/app.go:160`–`:163`, not
   `:159`–`:162`.** No document in the tree actually miscites it — the PRD and
   `fr-53-amendment.md` both cite only `:158` for `validate`, which is exactly
   right. I note the true range here so the next reader who goes looking finds
   it: `:158` is `func validate`, `:159` is `switch {`, and the comment is the
   four lines after that.
3. **PM-1's enumeration of the mis-citation's footprint is incomplete** — six
   carriers, not two. §7.1.

**Everything the two answers below turn on reproduced.** The arithmetic is
right, including the correction PM-1 made against their own v1.0 text.

---

## 2. Question (i) — YES. The hooks stay individually required.

**Ruling: the four security hooks stay individually required, a bundle that sets
them in one line stays refused, and the Go half's floor is 20 counted lines.
31 stands on this leg.**

I ratified this refusal at `bdf91971` and again at `cdb30b5d` §1.5, and I am not
reversing it to buy one line of a documentation budget. Three grounds, the third
of which has not been stated before and is the one I would defend first.

**(a) The library already states the argument, and it is correct.**
`live/app.go:160`–`:163`, in the switch that refuses a `Config`:

> *Everything below is a field with no defensible default — a reducer, a region,
> an event name and the four security hooks are all things only the application
> can say.*

That is not a style preference. It is a claim about who holds the information,
and it is true: no value the library could pick for `Origins` is right for any
deployment, and `Authenticate`, `Authorize` and `CSRF` have opt-outs
(`live.Anonymous`, `live.AllowAll`, `live.NoCSRFCheck`) precisely so that opting
out is an act somebody performed and a reviewer can see.

**(b) The refusal's value is the per-check review signal, and a bundle destroys
exactly that.** `api-surface.md:530` records the refusal in one clause; I
ratified it and then built `exceptions.md` §7.1's refusal of FR-20's `test/`
scope ruling on top of it. A project cannot refuse a bundle in its API on Monday
and grant one in its process on Tuesday — and it cannot grant one on Wednesday
because a line budget it set itself came out one over.

**(c) The `Config.Init` precedent argues against the bundle, not for it — and
this is the ground PM-1 leaves on the table.** A reader could reasonably ask why
`Init` was allowed to become optional at `fde707f0` and the four hooks were not.
`api-surface.md:530` answers it and the answer is decisive: `Init` has a
**total, side-effect-free** meaning for "unwritten" (the zero value of `S`), and
**forgetting it is visible on the first run** — the sessions start empty and,
through `PageHandler`, so does the page. For the four hooks there is no nil that
means "off", and forgetting them is invisible: a guessed `Origins` produces a
page that works on the author's laptop and a cross-origin upgrade nobody
observes. **The one shrink this project did take was taken on the ground that its
failure is loud. A bundle would be taken on the ground that its failure is
quiet.** They are not the same act and the first is not precedent for the second.

**What "yes" costs me, stated so it is not free.** It costs a line the project
would otherwise have. I am accepting that, and trigger 3 is the price of ever
changing my mind: if the refusal is overturned the budget **must** drop to 28 in
the same PR. I countersign that trigger explicitly. It is what stops a security
reversal from quietly buying DX slack, and §5 below makes it a condition.

---

## 3. Question (ii) — YES in principle, with nine constraints

**Ruling: a `live.Document`-shaped page shell is acceptable exported surface *in
principle*. 11 templ lines is a floor and not a fantasy. 31 stands on this leg
too.**

This is my question, FR-65 is my gate, and the answer has to be argued at the bar
I hold everything else to — which this project has twice used to *refuse* surface
(`live.Field` at FR-55, `Config.OnPatch` at FR-56, both on the same clause).

### 3.1 The consumer test, which is the one that decides it

Review checklist **§1.4** is the binding text: *every new interface, generic type
parameter, options struct, callback hook, or registry has ≥ 2 real call sites in
this PR*. FR-55 and FR-56 both died on it — *"exported surface whose only consumer
is an example"*, *"an exported symbol with no named call site"*.

A page shell does not die on it, and it is not close:

```bash
grep -rc 'DOCTYPE' --include='*.templ' .
```

**Eight hand-written page shells across seven files**, every one of them a real
consumer that exists today:

| Shell | Head content beyond the quickstart's |
|---|---|
| `docs/guide/_samples/quickstart/view.templ:23` | — (this is the counted one) |
| `examples/counter/view.templ:86` | viewport, stylesheet, **`@dev`** (`DevReloadScript`) |
| `examples/chat/view.templ:147` | viewport, stylesheet |
| `examples/chat/view.templ:215` (`LoginPage`) | viewport, stylesheet, **and no `live.Script` at all** |
| `examples/dashboard/view.templ:224` | viewport, stylesheet, **conditional HTMX `<script>`** |
| `bench/apps/counter/gotth/view.templ:93` | viewport, stylesheet, **shim `<script>` above and ready `<script>` below** `live.Script` |
| `bench/apps/chat/gotth/view.templ:172` | viewport, stylesheet |
| `bench/apps/dashboard/gotth/view.templ:313` | viewport, stylesheet |

`test/memory/cmd/memsrv/main.go:327` hand-writes a ninth in Go. **Seven of the
eight emit `live.Script`.** This is the strongest consumer case any symbol in
this library has had, and it is nine times `MustNew`'s.

### 3.2 There is a bug class available, which is what separates the two admissions I have already made

`Mux` is the symbol I called the strongest of DEV-1's three (`phase-4-exceptions.md`
§2) because it made two silent mounting failures *inexpressible*. `MustNew` is the
weakest because it removes no bug class and buys three lines. A page shell can be
either, and which one it is depends on a decision nobody has taken yet:

`api-surface.md:272` states an ordering invariant — `InspectorScript` *"belongs
above `Script`'s tag: both are deferred, and the inspector must wrap the
WebSocket constructor before the runtime opens a socket."* **Today that invariant
is documented and not enforced**, and getting it backwards produces an inspector
that silently sees nothing. A shell that owns the `<head>` can make it
inexpressible. **That is a `Mux`-class argument, and it is a better reason to
build this component than FR-53 is.** It is also the constraint most likely to
cost a line — see 5 below.

### 3.3 The nine constraints. These are what I will gate on under FR-65.

"Acceptable in principle" is not a pre-approval of a signature. Any
`live.Document`-shaped symbol must meet all nine or I refuse it:

1. **≥ 2 real call sites in the landing PR, at least one of them not the
   quickstart.** Checklist §1.4, literally. A shell that can only serve the
   counted sample is `live.Field` with better arithmetic, and I will refuse it
   whatever it does to FR-53.
2. **Head extension must exist, and must cost the quickstart nothing when
   unused.** Seven of the eight shells carry head content the counted one does
   not. A component that cannot carry a `<link rel="stylesheet">` and a
   `<meta name="viewport">` fails constraint 1 on the same day it ships. And if
   the extension costs the counted app a line, **the floor is not 31**, and
   trigger 1 fires before the symbol lands rather than after.
3. **`lang` and the `<html>` element's attributes stay the application's.** A
   live-connection library does not choose a document's language. This is FR-55's
   own ruling one surface over — *the accessibility attributes it would want to
   own are markup decisions that belong to the application's design system*. A
   hardcoded `lang="en"` is a rejection, not a default.
4. **`<title>` is application content and is a parameter, never a default.**
5. **The `InspectorScript`/`DevReloadScript` ordering invariant is preserved or
   made inexpressible — and the design must say which.** This is the hard one:
   both are methods on `*App[S]` and a package-level `live.Document` cannot reach
   them, while `PageHandler(page func(S) templ.Component)` gives the page
   function no route to the `App` either. A shell that emits `Script` and leaves
   the app to place the dev tags **relative to a tag it can no longer see** is a
   regression on today's hand-written shells and I will refuse it. I am not
   prescribing the resolution; I am pre-registering that "we did not think about
   it" is not one.
6. **The runtime tag must be omissible.** `examples/chat`'s `LoginPage` is a
   non-live page in a live application and deliberately carries no `live.Script`.
   A shell that always emits the runtime either cannot serve it or puts a socket
   on a page with no regions.
7. **`PageHandler`'s buffered-render contract survives.** `live/page.go:122`–`:132`
   renders to a buffer so a mid-render failure is a 500 rather than a truncated
   200, and names `live.Script` as *"the failure this actually catches"*. A shell
   that swallows `Script`'s error, or that writes before the buffer, deletes that
   guarantee and takes FR-58 with it.
8. **No `any`/`interface{}` in the signature, and no accessor-heavy options
   struct.** FR-65 names both as rejection triggers by name.
9. **An `api-surface.md` row in the same PR**, naming its FR and its stability
   marking, with the identifier-count delta reported (FR-65, §0's *"a symbol with
   no FR is a symbol to cut"*).

### 3.4 Is "acceptable in principle" something I am willing to have quoted back at me?

**Yes, and here is exactly what it commits me to, because a budget of 31 will
quote it back at me and I would rather be precise now than aggrieved later.**

**It commits me to this:** I will not refuse a page shell **on the ground that a
library should not own a page shell**. That argument is spent. I have looked at
it, the consumer case is eight real shells, and there is a bug class in the
neighbourhood. If DEV-1 lands a shell meeting §3.3 and I then refuse it because
hiding `<html>` feels like too much library, I will be moving a criterion after a
measurement, which is C-42 pointed at me.

**It does not commit me to:** any particular signature, or to 5 counted lines.
Constraints 1–9 are pre-registered grounds on which I may still refuse, and they
are written before the artifact exists for exactly the reason PM-1 fixed 31
before it exists.

**And the honest disclosure, in the same form PM-1 used.** The 5-line invocation
is DEV-1's costing, reproduced by PM-1 and now by me — as *arithmetic*, not as a
measurement. Constraints 2 and 5 are each capable of making it 6. **If they do,
the floor is 32, and I will say so rather than let the budget absorb it** — which
is precisely the failure mode §6 is about.

---

## 4. The budget

**≤31 binds.** Both forks resolve to *31 stands*, so the amendment holds on its
own pre-registration and I am not inventing a third option; the fork is genuine
and it is exhausted.

**The box does not tick.** The app counts **39**. 39 > 31. The line clause of
FR-53 fails by **8**. Nothing in this note moves Phase 4's box 2, and I have
graded nothing.

I also affirm the two parts of PM-1's ruling that were not put to me but which my
countersignature would otherwise be read as covering:

- **The counting rule is untouched** and stays v0.6's. I was not asked to move it
  and I am not moving it.
- **FR-53's 15-minute clause is untouched.**

---

## 5. The standing-vs-per-instance conversion — countersigned, with one condition

PM-1 flags, correctly and without being asked, that setting 31 *encodes L9-1's
per-instance refusal of the security bundle into standing scope text* — the exact
conversion I refused for FR-20's `test/` scope at `bdf91971` — and adds that doing
it in my favour is *"less noticeable, not more legitimate."* That last clause is
right and is the reason this needed an answer rather than a nod.

**Ruling: the conversion is legitimate here. PM-1's analogy is half right, and
the half that is wrong is the half that decides it.**

What I refused at `bdf91971` was converting a **grant** from per-instance to
standing. E-1 was permission to deviate; making it a scope ruling would have said
*"once and permanently, that no future measurement harness needs an argument, a
blast radius, or a signature"* — and, in my own words there, **"the record of
what a measurement binary is allowed to do would then be an absence."**

The general principle is therefore not *a per-instance ruling may never become
standing text*. If it were, C-42 itself would be illegitimate, and so would
RFC-0001 §6.1.2's ratchet, and this project is largely built out of such
conversions. The principle is narrower and sharper:

> **A per-instance ruling may not become standing text in the direction that
> removes future review.** A standing *grant* makes the next author's obligation
> an absence. A standing *refusal* makes it a visible act somebody has to
> perform in the open.

31 moves in the second direction, and **trigger 3 is what makes that true rather
than rhetorical**: overturning the bundle refusal *forces* the budget to 28 in
the same PR. The consequence of reversal is priced, in advance, in the document
that grades it. Nobody can reverse my refusal and pocket the line quietly. That
is the opposite of an absence.

**The condition, and it is a real one.** This countersignature depends entirely
on trigger 3 surviving. If trigger 3 is ever dropped, softened, or moved to a
document that does not gate, then 31 becomes a standing number whose security
premise has been detached from it — and at that moment it *is* the conversion I
refused at `bdf91971`, because the refusal it was priced against would no longer
be visible from the number.

> **Condition L9-1-C1 — trigger 3 is non-severable.** PRD §5.I (e) trigger 3 may
> not be struck, narrowed or moved except in the same act that strikes, narrows
> or moves the security-bundle refusal it prices. Any amendment touching trigger
> 3 alone requires L9-1's signature. **Owner: PM-1** to carry into §5.I (e) as a
> clause under the trigger table.

**And one thing I am refusing to do, because it would be the quiet version of
this.** I hold the pen on `docs/api-surface.md` and could add the name
`live.LocalDevelopment(origin)` to the `:530` row, which would retroactively make
six documents' citations true. **I decline.** The ledger records symbols that
exist or are proposed; back-filling a name onto a refusal so that later prose
about it reads better is retrofitting history to fit a citation, which is the
inverse of the rule this project applies to its own wrong sentences. §7.1 fixes
the citations instead.

---

## 6. C-42 — the derivation passes, the forward defence does not, and the repair is one clause

C-42 is my condition; PM-1 self-tested against it and asked me to check the
self-test. This is the part of the review with a finding in it.

### 6.1 What passes, and I checked it rather than agreeing with it

**The invariance half passes.** The derivation has three inputs — `validate`'s
seven required fields, the minimum shape of an HTML document, and arithmetic over
the second. I re-derived all three (§1). **None reads on 39.** Had the page
counted 33, 41 or 46, every figure in §3 of `fr-53-amendment.md` would be
identical. That is C-42 satisfied on its own words.

**The discipline half passes, and it passes on a hash.** The argument was written
at §9 **v1.0 row 5** and at the end of §5.I's *"Was 30 ever reachable?"*, in the
pass that *measured* the miss and explicitly declined to move the number. This
pass took no measurement — verified: the two files under the derivation are
byte-identical between `93772adc` and `adfd4a76`, so there was nothing to
measure differently.

**The backward counterfactual passes.** A count at or below 30 is arithmetically
impossible under the derivation, so its only arrival is a wrong floor, which
withdraws rather than defends the amendment. Correct.

**And the move is +1 where shopping would have moved to 39.** I weigh that. It is
the cheapest available move in the direction of the author's convenience, taken
in a pass that graded nothing, with the disclosure written in the first person.
On the record as it stands, PM-1 has been harder on themselves than I would have
had to be.

### 6.2 What fails: the forward defence, and it fails on arithmetic

§9 v1.1 row 1 concedes the forward counterfactual openly — *"there is exactly one
count at which this amendment changes a grade, and it is 31"* — and then defends
it:

> **Its defence is not neutrality, it is order**: 31 was fixed *before* the
> artifact that could satisfy it exists, from `validate` and from HTML, and
> trigger 1 forces it re-derived **in the same PR** as any shell that lands —
> **so a shell costing one line more moves the budget to 32 and the box stays
> red.**

**The bolded clause is false.** FR-53 reads *"≤31 lines"* (PRD `:926`). If a shell
costs one line more, the app counts 32 and the budget becomes 32. **32 ≤ 32. The
box goes green.** The sentence that is supposed to show the amendment cannot
grade itself into a pass is the sentence that shows it can.

This is not a slip in one document. It is a **conclusion-level disagreement
between the two documents that carry this ruling**, and the amendment's own
precedence rule notices it without being able to resolve it:

| Document | What it says a 6-line shell does |
|---|---|
| PRD §9 v1.1 row 1 | *"moves the budget to 32 and **the box stays red**"* |
| `fr-53-amendment.md` §6.2 | the same sentence, verbatim |
| `fr-53-amendment.md` §9 | *"if it costs 6 the budget is 32, the app is 32, and **it ticks there**"* |

`fr-53-amendment.md`'s preamble says *"where this document goes further it goes
further in detail, never in conclusion. If the two ever disagree, the PRD is the
one in force and this file is the defect."* **Here the file's §9 is right and the
PRD is wrong**, so the rule as written puts the false statement in force.

### 6.3 The consequence is larger than one sentence

Trigger 1's canonical text (PRD §5.I (e), and I diffed it against
`fr-53-amendment.md` §8 — **byte-identical**, so no drift) reads:

> *A library-owned page shell lands and the counted total is **not 31** → The
> floor is re-derived **in the same PR** and the budget moves to it, **up or
> down**, naming the line that moved.*

Applied literally: the budget tracks the tree. If the shell costs 5, the app is
31 and the box ticks. If it costs 6, the app is 32, the budget is 32, and the box
ticks. If it costs 9, the app is 35, the budget is 35, and the box ticks.
**Under trigger 1 as written, FR-53's line clause cannot fail once any page shell
lands, at any cost.**

That is RFC-0001 §6.1.2's own condemned shape — *a target that cannot ratchet down
is a target that stops constraining* — arriving from the direction §6.1.2 did not
anticipate, and it arrives inside the very ratchet quoted in the amendment's
defence. `fr-53-amendment.md` §9's third clause (*"if it costs more, neither moves
far enough and the box stays open"*) is not reachable from trigger 1's text
either; nothing in the trigger caps the re-derivation.

**The repair already exists and is not in force.** `fr-53-amendment.md` §8 carries
a paragraph the PRD does not:

> **Withdrawal, as distinct from movement.** Trigger 1 firing *upward* — a shell
> that lands above 31 — does not merely move the number; it **falsifies the
> premise this amendment was granted on**, and the correct act is to say so in the
> amendment log rather than to quietly re-baseline.

`grep -n -i 'withdraw' docs/PRD.md` returns two hits, both in *prose about*
trigger 1, and **none in trigger 1**. The trigger table is the canonical copy by
both documents' own statement; the withdrawal paragraph is in the non-canonical
copy only; so the clause that would make the defence true is the one clause that
does not bind.

### 6.4 Ruling

**PM-1's C-42 self-test passes on the derivation and on order, and fails on the
forward defence. I am not withdrawing the amendment for it — 31 stands, because
the defect is in the ratchet and not in the floor — but the repair is a condition
of this countersignature, and it must land *before* any page shell does.**

> **Condition L9-1-C2 — trigger 1 may not re-baseline upward.** PRD §5.I (e)
> trigger 1 takes the withdrawal clause that today lives only in
> `pm/fr-53-amendment.md` §8, in force: **a shell whose landed floor is above 31
> falsifies the premise this amendment was granted on. The budget does not move
> up to meet it. The amendment is withdrawn and re-argued in the amendment log,
> with the box open.** Downward movement is unaffected — that half is the ratchet
> and it is correct.
>
> **Condition L9-1-C3 — the false sentence is corrected where it was made.**
> PRD §9 v1.1 row 1's *"a shell costing one line more moves the budget to 32 and
> the box stays red"* is arithmetically false against FR-53's `≤`. Under this
> project's own rule the wrong sentence stays and is corrected beneath, dated —
> the same treatment PM-1 gave their own three v1.0 statements at §5.I (f).
> `pm/fr-53-amendment.md` §6.2 carries the same sentence and needs the same
> treatment. **Owner: PM-1**, both.

**Why this must land before DEV-1's shell and not with it.** Trigger 1 fires *in
the same PR* as the shell. If the shell lands first, the trigger's current text is
the text that governs its landing, and box 2 closes by re-baselining — the
outcome §9's preamble forbids and the one PM-1's §9 explicitly says is not
available. **The repair is therefore a prerequisite of box 2's engineering route,
not a tidy-up after it.**

**One thing I want on the record in PM-1's favour.** This defect is only findable
*because* PM-1 wrote the forward counterfactual down instead of resting on "39 >
31, this changes no grade." A pass that had claimed neutrality would have hidden
it. The disclosure is what made the error reviewable, which is the argument for
disclosure working exactly as intended.

---

## 7. The two loose ends

### 7.1 The `live.LocalDevelopment` mis-citation — mine corrected, and the footprint is three times what PM-1 enumerated

**Verified first.** `docs/api-surface.md` contains the string zero times
(`grep -c` → 0) and never has (`git log -S'LocalDevelopment' -- docs/api-surface.md`
→ empty). `api-surface.md:530` records the refusal in one clause with **no symbol
and no signature**. `git log -S'LocalDevelopment'` returns four commits at
`93772adc` — `bdf91971`, `cdb30b5d`, `e5063267`, `ab00e7dc` — none touching
`api-surface.md`. **PM-1's finding is correct: I coined the name, in
`docs/exceptions.md` §7.1, and PM-1 quoted it back as though the ledger had
written it.**

**But the footprint is six sites, not two.** PM-1's §5.I (f) 1 says *"the two
documents that carry the mis-citation"*. `grep -rn 'LocalDevelopment'` finds:

| Site | Owner | Status |
|---|---|---|
| `docs/exceptions.md:591` | **L9-1** | **Corrected in place in this commit** |
| `docs/reviews/phase-4-exceptions.md:90` | **L9-1** | **Corrected in place in this commit** |
| `docs/gates/phase-4.md:1644` (§5.8) | PM-1 | Enumerated and routed by PM-1 ✔ |
| `docs/gates/phase-4.md:1247` (§5.9) | PM-1 | **Not enumerated.** Routed below |
| `docs/PRD.md:506` — FR-20's **live** scope clause 2 | PM-1 | **Not enumerated, and inside PM-1's own write scope.** Routed below |
| `docs/PRD.md:2360` — Phase 4 **exit box 2**'s parenthetical | PM-1 | **Not enumerated, and inside PM-1's own write scope.** Routed below |

`docs/PRD.md:1049` and `:3314` are the dated v1.0 statements PM-1 corrected at
§5.I (f) and deliberately left standing. Those are history and I agree they stay.
**The other two PRD sites are not history**: `:506` is a live clause of FR-20's
scope — the sentence the next person drafting against FR-20 will read — and
`:2360` is inside the exit box that grades this very requirement. Neither was
enumerated, and PM-1's stated reason for not editing (*"outside PM-1's write
scope this turn"*) does not reach either of them.

**What I changed, in my own text, and how.** I applied the rule I set at
`bdf91971` when I refused to let E-2's row be deleted, and that PM-1 applied to
their own three sentences: **the wrong sentence stays where it was made and a
dated correction goes beneath it.** A page that quietly corrects itself teaches
the fix and hides the failure mode. So `exceptions.md` §7.1's paragraph is
unedited and now carries a dated L9-1 correction after it, and
`phase-4-exceptions.md` §1.5 the same. **Neither ruling changes. The name stays
in use — it is a good name and six documents now depend on it — but its
load-bearing citation is this ratification, not the ledger's aside.**

**Routed to PM-1, three sites:** `docs/PRD.md:506`, `docs/PRD.md:2360`, and
`docs/gates/phase-4.md:1247`, plus the §5.8 site PM-1 already routed. All four
say the ledger refused a *named* bundle; the ledger refused an *unnamed* one. The
minimal fix at each is to attribute the name — *"the bundle `docs/api-surface.md:530`
refused, which L9-1 named `live.LocalDevelopment(origin)` at `bdf91971`"* — which
is one clause and is true.

### 7.2 §5.I's unqualified "§5.8" — qualify it, and it is twice, not three times

**Verified.** `grep -n '5\.8' docs/PRD.md` returns three hits. Two are in §5.I's
v1.1 prose and are unqualified — `:1101` (*"Which corrects §5.8's summary of
itself"*) and `:1120` (*"§5.8 named the strongest argument"*). The third, at
`:3292` in §9 v1.1 row 2, is **already qualified** as
"`docs/gates/phase-4.md` §5.8" and needs nothing.

**Ruling: qualify the two.** Leaving them is not defensible, for a reason
specific to this document rather than a general preference for precision: the
PRD's §5 subsections are **lettered** (§5.A … §5.L), so "§5.8" has no referent in
the document a reader is holding. It resolves to `docs/gates/phase-4.md` §5.8
*"Was 30 ever reachable? — ARGUED"* — which is the gate record's twin of the PRD's
own *"Was 30 ever reachable?"* subsection, so a reader who guesses that §5.8 means
"the section above" gets the right *argument* by accident and the wrong
*document*, and then cannot find the sentence being corrected. That is worse than
a dangling reference: it is one that appears to resolve.

The fix is three words at each site. **The referent is genuinely ambiguous
between two documents that both contain the argument**, so the qualification
should also say which text (f) is correcting — the gate record's, the PRD's, or
both. Per §9 v1.1 row 2 it is *both*, since (f) lists §5.I's own v1.0 subsection
and the gate record's §5.8 side by side. **Owner: PM-1.**

---

## 8. What I edited, what I routed, and to whom

**Edited, in files I own — two, both my own text, both corrections in place:**

| File | Change |
|---|---|
| `docs/exceptions.md` §7.1 | Dated correction after the mis-citing paragraph. **The paragraph and the ruling are unchanged.** |
| `docs/reviews/phase-4-exceptions.md` §1.5 | Dated correction after the mis-citing sentence. **The ratification is unchanged.** |

**Deliberately not edited:** `docs/api-surface.md`. Adding the symbol name to the
`:530` row would make six citations retroactively true and is the wrong act (§5,
last paragraph). Adding a ledger row for `live.Document` before it exists is the
failure FR-65 names and that this project already refused for `livetest.Audit` and
`Report`. §3.3's constraints live here, in a review note, until there is a symbol.

**Routed, with owners:**

| # | Item | Owner | Blocking? |
|---:|---|---|---|
| 1 | **L9-1-C2** — trigger 1 takes the withdrawal clause; no upward re-baselining. §6.4 | **PM-1** | **Yes — before any page shell lands.** Under the current text the first shell closes box 2 by re-baselining |
| 2 | **L9-1-C3** — §9 v1.1 row 1's *"the box stays red"* corrected beneath itself, dated; same at `fr-53-amendment.md` §6.2. §6.4 | **PM-1** | Yes, same act as 1 |
| 3 | **L9-1-C1** — trigger 3 declared non-severable. §5 | **PM-1** | **Yes — this countersignature is conditional on it** |
| 4 | Mis-citation at `docs/PRD.md:506` and `:2360` — live text, not history | **PM-1** | No |
| 5 | Mis-citation at `docs/gates/phase-4.md:1247` (§5.9), alongside the §5.8 site PM-1 already routed | **PM-1**, at revision 4 | No |
| 6 | The two unqualified "§5.8" citations at `docs/PRD.md:1101` and `:1120`. §7.2 | **PM-1** | No |
| 7 | A `live.Document` page shell built to §3.3's nine constraints | **DEV-1** to build, **L9-1** to gate under FR-65, **QA-1** to re-count | It is box 2's only engineering route |

**Not routed, and recorded so nobody looks for it:** I have graded no Phase-4 box
and reversed no QA-1 grade. Revision 4 of `docs/gates/phase-4.md` remains PM-1's
and I have not written into that file.

---

## 9. What Phase 4 box 2 needs now

**Box 2 reads *"First working counter in ≤15 minutes and ≤31 lines of app code,
timed (FR-53, G7)"*. The app counts 39. It does not tick, and nothing in this
note ticks it.**

The routes are the three PM-1 named, with one addition that changes their
ordering:

1. **Engineering.** A page shell meeting §3.3's nine constraints, landing the app
   at ≤ the re-derived floor. **DEV-1** builds, **L9-1** gates it as new surface
   under FR-65, **QA-1** re-counts and grades. **Prerequisite: L9-1-C2 must be in
   force first** — otherwise the shell closes the box by moving the budget to
   meet itself, which is not a pass.
2. **A disclosed waiver**, argued on its own merits as a descope with a reason and
   an owner. **PM-1** writes, **L9-1** countersigns. I am not pre-judging it; I
   note only that it must argue for shipping a 39-line quickstart, not for a
   number.
3. **Not at all.** Phase 4 exits with box 2 open, or does not exit.

**What does not close it: a further amendment.** The number is at its floor, both
legs of the floor are now countersigned, and the only remaining move is the one
§2 refuses — which lands at 28, further from 39 rather than nearer.

— L9-1, 2026-08-05, against `adfd4a76`
