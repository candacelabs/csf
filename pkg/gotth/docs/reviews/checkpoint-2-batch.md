# Checkpoint-2 batch — L9-1 rulings

| | |
|---|---|
| **Reviewer** | L9-1 (Principal Engineer) |
| **Date** | 2026-08-04 |
| **Ruled on** | five items referred for decision, against `820752f6` |
| **Code reviewed on its merits** | `c8f1aea2`, `820752f6` (item 4) |
| **Ruled against** | [PRD](../PRD.md) FR-15, FR-16, FR-23, FR-33, FR-65 · [RFC-0001](../rfc/001-architecture.md) · [protocol.md](../protocol.md) · [api-surface.md](../api-surface.md) · [review checklist](../review-checklist.md) |
| **Prior rulings** | [module-init review](module-init.md) (C-15…C-19, addendum C-20) |
| **Disposition** | **five rulings, six conditions C-21…C-26, none blocking; two documents amended here** |

```
Rulings: 5 issued, 0 deferred
  1. api-surface §0 counts       CHANGE  — strike the derived cells, keep the measurements
  2. protocol.md H-6             AMEND   — done here, plus P2 and P6, its twins
  3. live.Script()               CHANGE  — Script(mountPath string); no new identifier
  4. RFC vs. effect-failure      AMEND   — done here; the change is accepted on its merits
  5. livetest session ctor       ADMIT   — one symbol, guarded by testing.TB and internal/

Conditions opened: C-21…C-26          Blocking: none
Documents amended in this batch: docs/protocol.md §6, §7, §12
                                 docs/rfc/001-architecture.md §9, §12, §17
Files I did not touch, by rule: any .go, .js, .templ, docs/api-surface.md,
                                docs/PRD.md, docs/qa/, gotth-live/bench/
```

**Commit-reference correction, for the record.** The referral named
`358f91de`, `c8f1aea2` and `820752f6` as the effect-failure work. `358f91de` is
QA-1's D-13 fix — `go list` stdout versus stderr in `internal/arch` — and has
nothing to do with the effect boundary. The effect-failure contract is
`c8f1aea2` (the `Session` parameter) and `820752f6` (the contract itself). Item 4
is ruled against those two.

---

## 1. What I verified by running it, not by reading it

Everything below was run in `dis-gotth-live:latest` against this worktree, or
against a pristine `git archive HEAD` export where a mutation was involved.

| Check | Command | Result |
|---|---|---|
| The whole gate | `bash ci.sh` | **exit 0**, "every gate this invocation could run is green", one announced skip (the node client suite the library image does not have by design). Build, vet, gofmt, staticcheck, `-race` across the module, `examples/counter`'s own module, apisurface, the client size gate (9,143 B minified / **3,874 B gzipped** against NFR-2's 12,288 B gzip ceiling — 8,414 B headroom, 68.5 %), and FR-7 byte-reproducibility all green. Re-run after this batch's two document edits: still exit 0. |
| The measured surface | `go run ./apisurface` | `live` **45/45** identifiers, **49/49** fields. `live/livetest` **2/8** and **0/6**. Printed measured total **47 / 49 / 96**. |
| **Item 1 — the `total` column is checked by nothing** | mutated a copy of the ledger to `9001` and `7` in the `total` column, ran `apisurface -root` against it | **exit 0, "the surface matches the ledger".** The claim is confirmed by mutation, not by reading `readLedger`. |
| **Item 3 — the script tag 404s off the default mount** | a scratch consumer module: `mux.Handle("/app/", http.StripPrefix("/app", app.Handler()))` | `Script()` renders `<script src="/live/gotth-live.min.js" data-gotth-url="/live" defer>`; `GET /app/gotth-live.min.js` → **200**; `GET /live/gotth-live.min.js` → **404**. No server-side error on either side of the failure. |
| **Item 4 — the name-drift spec bites** | `live.EffectFailedEvent` set back to the historical wrong `"gotthlive.effect_failed"` | **3 failures in `./live`**: the drift spec at `live_test.go:39` and both end-to-end specs. Reproduces DEV-1's commit message exactly. |
| **Item 4 — the terminal default is asserted** | `session.retryable` forced to `return true` | `./internal/session` fails, and `./live` fails on the end-to-end reducer. The failing assertion's message is *"an unclassified failure must not invite a retry"* (`actor_test.go:560`); the commit message quotes that message as if it were the spec name, which it is not — the spec is *"reports a failing effect to the reducer with its source and error"*. Immaterial, recorded for accuracy. |
| **Item 5 — what is actually unconstructable** | a scratch consumer module | `var s live.Session` and `live.Session{}` **do** compile outside `live` — an empty composite literal names no field. `s.ID()` is all-zero and `s.Identity()` is **nil**. What is impossible is a `Session` with a chosen identity, which is the only kind worth passing to a hook. The premise is right; its wording needed sharpening, and §6 rules on the sharpened version. |
| **Outside the five — `Config.Dev`** | `grep -rn '\.Dev\b\|Dev:' --include='*.go'` over the module | exactly **one** hit: `examples/counter/main.go:96`, which *sets* it. Nothing in the library reads it. See §8. |

**Not measured, and why.** I did not run the bench image's node suite or the
browser matrix: neither bears on any of the five items, and `bench/` is untracked
WIP that is not mine to touch. I did not measure the consumer module-graph
effect of the `livetest` addition in §6, because it adds no dependency — the
bridge is an existing-module `internal/` package and `livetest` already imports
`testing`; C-19's next re-measurement is the right place for a number.

---

## 2. Ruling 1 — `docs/api-surface.md` §0: strike what is derived, keep what is measured

**Ruling: CHANGE. §0's counts table holds measurements and nothing else. The
`total` column and the `Total … incl. fields` row are struck. The two remaining
columns state, in the table, that they mean different things. The five erratum
paragraphs are compressed to one sentence; §10 already holds the narrative.**

### 2.1 What §0's counts section is for

One thing: **it is the baseline `tools/apisurface` reads, and the FR-65 gate
enforces.** It is a machine input that a human must be able to check. That is the
whole job. Everything else §0 has accumulated — a derived column, a derived row,
five paragraphs of history — is a second changelog living in a section whose
correctness a program depends on, and none of it is read by the program.

### 2.2 The three findings, ruled

**(a) The `total` column is unchecked, and I proved it.** `readLedger` consumes
the first two numeric cells of each of two rows and ignores everything else.
With the column set to `9001` and `7` the tool exits 0 and prints *"the surface
matches the ledger"*. That is precisely the failure mode the tool was written to
end — §0's own erratum says a hand-maintained count *"is accurate until the first
time nobody re-derives it"* — reproduced inside the fix. The same is true of the
third **row**: `94` and `14` are derived from the two rows above and are matched
by no regex.

**(b) The column is also incoherent, independently of being unchecked.** `live`'s
column is a measurement held to exact equality in both directions.
`live/livetest`'s is a **v0.1 target treated as a ceiling** — measured 2 against
a ledgered 8 — because most of its symbols wait on the bench harness that fixes
their shape. `53` is their sum, so it is a measurement plus an aspiration
presented as one number, and it is not the module's exported surface (that is 47)
nor a bound anyone checks. Worse, the tool prints its own `total 47 / 49 / 96`
two lines under the ledger's `53 / 55 / 108`, so CI's output already shows a
reader two different totals and explains neither.

**(c) The errata go, save one sentence.** Five paragraphs narrate: the
`41/48` → `40/49` correction, `Config.Events`, `NewFields`,
`Event.Contributing`, the `Limits` fields, and the effect boundary. Every one of
those has a **§10 changelog entry with its source column and its argument** —
longer, better, and in the section whose job that is. Keeping them in §0 means
each future surface change must be written twice, and the §0 copy is the one
nobody will maintain. One sentence survives, because it is not history but the
*rationale for the check being a program*: the original off-by-one lived in the
identifier column for months and each edit carried it forward because the number
being checked was the total.

### 2.3 The precedent this follows

The module-init addendum refused a second copy of the client artifact on the
ground that *"an invariant you do not create is better than one you check"*, and
C-14 held that a derived number stays derived. A `total` column is a derived
number promoted to a stored one. The consistent ruling is to delete it, not to
teach the tool to check it — deleting it removes the failure mode, checking it
adds code whose only job is to defend a cell nobody needs.

### 2.4 What §0 becomes — normative

Replace the `**Counts**` line and the three-column table with:

> **Counts.** This table is the FR-65 baseline `tools/apisurface` reads and CI
> enforces. It holds measurements and nothing derived. **`live` must match
> exactly**, in both directions — a difference either way fails. **`live/livetest`
> is a v0.1 target and a ceiling**: most of its ledgered symbols are
> unimplemented, waiting on the bench harness that fixes their shape, so measured
> may be below this row and may never exceed it. The two columns therefore mean
> different things and are deliberately **not summed here**; the tool prints
> every derived total, including the module's true measured surface.
>
> | | `live` (exact) | `live/livetest` (ceiling) |
> |---|---:|---:|
> | Exported identifiers (types, funcs, methods, consts, vars) | **45** | 8 |
> | Exported struct fields | **49** | 6 |
>
> *The `live` split was corrected from 41/48 to 40/49 when `tools/apisurface`
> first measured it: one struct field had been counted in the identifier column
> since before checkpoint 1, and every subsequent edit carried the error forward
> because the number being checked was the total. That is the argument for this
> table holding only what a program reads, and for the derived totals living in
> the program's output. Surface changes are recorded in §10.*

The other four erratum paragraphs are struck. §8's FR-65 bullet stops restating
`53`/`108` and points at the table instead.

**Owner: DEV-1. Phase: checkpoint 2.** → **C-21**.

---

## 3. Ruling 2 — `protocol.md` §6: H-6 is stale, the code is right, and the fix is applied here

**Ruling: AMEND, and amended in this batch. The correct reading is the wider
one — `event_id` and `client_ref` are non-zero exactly when the origin kind is
*event-bearing*, which is `CLIENT_EVENT` or `RESYNC`. No implementation
changes.**

### 3.1 Why the code wins

This is not a close call, and it is worth saying why so nobody re-opens it.
Cycle 2's **B-7** made `ResyncRequest` mint an `event_id`, made the resulting
`Snapshot` carry `Origin{kind: RESYNC, event_id, client_ref}`, and explicitly
*"removed `RESYNC` from §4.2's `event_id = 0` list"* — protocol.md §12 says so in
its own changelog. §4.2 was rewritten to match, in bold, with the reason (*"a
resync is the one server-initiated frame with a specific, nameable client
cause"*). `frame.proto`'s `Origin.event_id` comment was rewritten to match.
`validateOrigin`/`eventBearing` implement it. `outbound_test.go`'s H-6 table has
both `RESYNC` arms, and the conformance suite's wire-observable H-6 spec uses the
same two-kind definition.

**Six sites moved and one sentence did not.** The one that did not is in the
table that R-13 exists to make normative, which is the worst place for it to be:
a reader who trusts §6 gets a rule the wire does not obey, and the only
reconciliation is a comment in a Go file they have no reason to open. Reverting
the code to §6's narrower sentence would delete provenance the server already
holds, for nothing.

### 3.2 The twins — and one of them is not cosmetic

Fixing H-6 alone would have left two stale sentences in §7, where the properties
QA-1 checks live.

**P2 was under-inclusive, and that is a real gap.** It read *"For every patch
with `kind == CLIENT_EVENT`, `origin.event_id` names an `Event` received on this
session"*. Under the corrected H-6 a resync `Snapshot` also carries an
`event_id` — and P2 was the only property that **joins** that identifier back to
an inbound frame. I confirmed the consequence in the code rather than inferring
it: `test/internal/conformance/provenance_test.go:157` does
`if c.origin.GetKind() != pb.OriginKind_CLIENT_EVENT { continue }`, so the
`RESYNC` arm's identifiers are never resolved against the provenance log. The
identifier B-7 added *in order to preserve provenance* is the one identifier no
conformance property checks. P2 is restated over the event-bearing kinds, naming
what each resolves to — an `Event` for `CLIENT_EVENT`, the `ResyncRequest` for
`RESYNC` — and its check column now fails a run that produced no resync snapshot
rather than passing it vacuously.

**P6's parenthetical was simply false, and it cited §4.2 for it.** It read
*"server-initiated patches carry `event_id = 0` by design, §4.2"*. §4.2 says the
opposite for exactly one kind, which is the kind §4.2 is mostly about. Corrected
to distinguish a server-initiated patch that no client frame caused from the
resync `Snapshot` that does name one, and its test column now names the `RESYNC`
snapshot as resolving through the **first** arm.

Nothing else needed it. H-13 already says *"a resync snapshot has
`Origin.kind == RESYNC` iff they are non-zero"* and is consistent. H-12 is about
`Error` and is untouched. `frame.proto`'s comment is already correct — I checked
it rather than assuming, and it is the one document that never drifted.

### 3.3 What I changed

- **§6, H-6** — restated as *event-bearing*, with an inline amendment marker.
- **§6, new note under the table** — records the amendment, fixes *event-bearing*
  as the vocabulary, and states that a third event-bearing kind must move H-6,
  P2, P6 and `eventBearing` **together**. That last sentence is the point: this
  defect was one sentence out of six moving, and naming the set is how the next
  one is caught.
- **§7, P2 and P6** — as above.
- **§12** — a changelog subsection recording all four, in the table format §12
  already uses.

The doc half is done. The half a document cannot do — extending P2's conformance
spec to the `RESYNC` arm, and removing the now-inverted comment in
`invariants.go` that explains H-6's sentence is older than §4.2 — lands as
**C-22**. **Owner: DEV-1 + QA-1. Phase: checkpoint 2.**

---

## 4. Ruling 3 — `live.Script()` takes the mount path

**Ruling: CHANGE. `Script(mountPath string) templ.Component`. The unexported
`defaultMountPath` is deleted. Not a second function, not a `Config` field, not
a runtime check, and not "document harder".**

### 4.1 The failure, measured

Mounted at `/app/`, the handler serves `/app/gotth-live.min.js` with a 200 and
the rendered tag points at `/live/gotth-live.min.js`, which 404s. There is no
server-side error and no log line: the page loads, the script does not, and
nothing is live. That is the same class of silent failure the attribute-constant
comment at the top of `live/templ.go` exists to prevent — *"a disagreement
between the two is a silent no-op in the browser rather than an error
anywhere"* — reproduced eighty lines below the comment.

### 4.2 Why the caller must state it, and why that settles the shape

The mount path is knowledge only the caller has. `App.Handler()` is an
`http.Handler`; the router strips the prefix before the handler ever sees a
request, and `Script()` renders on a *different* request entirely — the page. So
no runtime check inside the library can observe the mismatch, which removes
option (d) on a fact rather than a preference. There are exactly two places the
path can come from: the `Script` call site, or `Config`.

**Against a second function (`ScriptAt`):** +1 exported identifier, and it leaves
the broken default reachable. The 404 survives, now with a sibling that would
have avoided it. This is the worst of the five options and I am ruling it out
explicitly so it is not proposed again.

**Against `Config.Mount` plus `App.Script()`:** +1 field, +1 method, and it does
not remove the failure — it relocates it. `Config.Mount: "/live"` next to
`mux.Handle("/app/", …)` produces exactly the same 404, and now the library
stores a string it never uses for routing, which is the shape FR-65 flags. It
also splits the templ helper vocabulary: `Region`, `On`, `OnWith` and `Preserve`
are package-level funcs, and a fifth reachable only as a method on a *generic*
type is a worse thing to teach in a quickstart than one extra argument.

**For a parameter:** **zero net exported identifiers** — decisive under FR-65,
where every other option spends surface to fix a defect. After the change there
is no way to render the tag without naming the mount, so the wrong thing is
unwritable rather than merely detectable — the same principle as the addendum's
one-copy artifact ruling. The mount string then sits in the same file as the
router line it must match, where a shared `const` is the obvious next step.
`Script` is marked **experimental** in the ledger and v0.1 makes no compatibility
commitment (BL-30), so the signature change costs a changelog line and the
counter's one call site.

### 4.3 The spec, so nobody needs a second ruling

```go
// Script renders the script tag for the embedded client runtime, for an
// application whose handler is mounted at mountPath.
func Script(mountPath string) templ.Component
```

1. `mountPath` is the prefix the handler is reachable at as the **browser** sees
   it — `"/live"`, `"/app/live"`. A single trailing `/` is trimmed, so `"/live"`
   and `"/live/"` render identically.
2. Emits `src="<mount>/gotth-live.min.js"` and `data-gotth-url="<mount>"`,
   unchanged in every other respect.
3. **A mount that is empty or does not begin with `/` makes `Render` return an
   error.** This is the server-side error the current design has nowhere to put:
   it lands on the page request, where a handler already has an error path. It
   costs no exported identifier and it turns the one remaining way to get this
   wrong into a 500 instead of a blank page.
4. `defaultMountPath` is deleted. `Script`'s doc paragraph telling the reader to
   hand-write their own tag is deleted with it — it was the workaround, and a
   workaround must not outlive its defect. `App.Handler()`'s doc drops *"beyond
   the one Script documents"*: after this the handler holds no routing assumption
   at all, which is FR-33 stated without a caveat for the first time.
5. Ledger §5.2's `Script()` row is updated in the same PR. The identifier count
   does not move (45 stays 45); only a signature changes.

**Specs required** (Ginkgo v2 + Gomega, per the standing convention):
`Script("/x")` emits both attributes against `/x`; `Script("/x/")` renders
byte-identically to `Script("/x")`; `Script("")` and `Script("x")` return a
render error rather than a tag. And in the **FR-33 three-router test**: mount the
counter under `net/http`, `chi` and `gin` at **three distinct prefixes, at least
one of which is not `/live`**, and for each assert that the `src` in the rendered
tag returns **200** from that router. Mounting all three at `/live` would satisfy
PRD Phase 2's "mounts unchanged" criterion literally while testing nothing about
prefixes, and that is the test that would have caught this defect before I did.

**Owner: DEV-1** (`live/templ.go`, `live/app.go` doc, the specs), **DEV-3** (the
counter's call site and README). **Phase: checkpoint 2, and before the FR-33
three-router test is written**, not after. → **C-23**.

---

## 5. Ruling 4 — the effect-failure contract: accepted, and the RFC amended here

**Ruling on the change: ACCEPT on its merits. Ruling on the disagreement: AMEND
the RFC, done in this batch, in the open, following C-13's precedent rather than
a footnote. Ruling on the default: "unclassified is terminal" is right. Ruling
on the cut: shipping `Retryable` without `IsRetryable` is the right cut.**

### 5.1 The change, on its merits

`c8f1aea2` gives `Config.Execute` a `Session`. The argument is correct and the
proof is the right kind: the counter's `WatchEffect` **loses** its `Session`
field and becomes an empty struct, because that field existed only to smuggle an
identifier past a signature that dropped it. A change that makes an application
type smaller is a change that was fixing something real. An explicit parameter
over a context value is right for the reason the doc comment gives — a context
value makes the identity optional at the type level and absent by mistake at
runtime.

`820752f6` makes the failure event reachable and classified. Both defects it
names are real, and the first is worse than it sounds: `examples/counter`
hard-coded `"gotthlive.effect_failed"`, which nothing emits, and its spec passed
because the reducer's default branch does nothing. **A failure-handling path
shipped in the flagship example having never once executed.** That is the exact
sibling of QA-1's D-13 class — a test that cannot fail — and the commit is
honest enough to record that the counter *stays green* under the name mutation
because its table builds the event from the same constant the reducer matches.
Naming your own remaining blind spot in the commit message is the behaviour I
want to see repeated.

I re-ran both mutations rather than trusting them (§1). Both bite, both in the
places claimed.

Two judgement calls I looked at and let stand. **Duplicating the literal rather
than aliasing** `internal/session.EffectFailedEvent`: an alias would make drift
structurally impossible, which is normally the stronger shape, but it would print
`= session.EffectFailedEvent` in the godoc of a package whose readers cannot
import that package. Godoc legibility for the reader who has no other option
wins, and the drift is held by a spec that I confirmed fails. **The panic arm
passing `false` explicitly** rather than falling through the default: correct,
and the comment says why — re-running a panicking effect re-runs the bug, and
the panic budget would then close the session on a loop the library scheduled for
itself. Terminal on its own merits, not by default.

### 5.2 The RFC disagreement — amended, not footnoted

§9 and §12 described `EffectFailed{source, err}`, a typed variant. That shape
never shipped and could not have: a reducer receives an ordinary `live.Event`,
so the contract is necessarily a name plus field keys. The RFC was not made
wrong by this session's work — it was **always** a description of something that
did not exist, and the session's work is what made the gap visible by giving the
real contract exported names. C-13's precedent governs: a design document and a
shipped surface that disagree silently is how the next reviewer gets misled.
Amended in §9 (the row, plus a note carrying the four constants, the terminal
default and the `IsRetryable` cut), in §12's failure table, and recorded in §17.

### 5.3 "Unclassified is terminal" — the right default

**Affirmed, and it is the only defensible direction.** The asymmetry is not
aesthetic. An effect may have committed externally before it failed — the message
published, the row written — so retrying a failure nobody classified risks
duplicating data somebody else owns, invisibly. Not retrying costs a change that
visibly does not happen and shows up as a session that stops updating. Between a
**visible omission** and an **invisible duplicate**, the default belongs on the
omission. It is also the only direction the library is entitled to take:
retrying is an assertion about idempotence, and only the code that performed the
effect can make it — the library asserting it on that code's behalf would be
inventing a guarantee.

Two details make the default hold under misuse rather than only under correct
use, and both are right. The classification travels as a **field**, not a second
event name, so a reducer that does not care matches one name and a reducer that
misreads the field reads something that is not `"true"` — the terminal answer.
And the godoc tells the reader to parse it with `strconv.ParseBool` and take the
error as false. An unreadable classification is an unclassified one. That is
default-deny (checklist §5.4) applied to failure, and it degrades in the safe
direction.

### 5.4 No `IsRetryable` — the right cut

**Affirmed. No change.** The mark is *set* by the executor and *read* by the
reducer, and what a reducer holds is the event, not the error — so the
symmetric-looking predicate has no call site, which FR-65 makes a rejection. I
looked for the consumer before affirming, as I did for A2, and did not find one:
nothing in `live`, `internal/session` or `examples/counter` inspects an error's
mark outside the one internal `errors.As`. §7 of the ledger already records the
re-add trigger — *something needing to inspect an error it did not itself
produce* — which is a real trigger and not a hedge. It is re-addable in one PR
if the chat example finds it.

The one composition hazard, recorded so it is not discovered as a surprise:
`live.Retryable` returns an error whose concrete type is `internal/`, so an
application that wants to *test* a mark on an error travelling through its own
helpers must track that itself. That is the cut, stated. If it bites in Phase 2,
the trigger has already fired.

### 5.5 The one thing the change should have documented and did not

`EffectFailedErrorField` carries `err.Error()`, or `sprint(r)` for a panic,
**unconditionally and in production**, with no relation to `Config.Dev`. That is
defensible — the reducer is server code — but the moment a reducer renders that
field into a fragment, internal error text and raw panic values reach the
browser, and nothing in the godoc says so. The library is careful about this for
`Error` *frames* (checklist §5.9); the failure *event* is a second path to the
same disclosure and is not covered by the same discipline. Documentation, not a
design change. → **C-24**.

---

## 6. Ruling 5 — `livetest` gains a session constructor: one symbol, two guards

**Ruling: ADMIT, into `live/livetest`, as exactly one exported identifier.
`live` gains none. This does not touch C-12's cap: the cap counts exported
*packages*, and this adds none.**

### 6.1 The premise, sharpened by measurement

"An application cannot construct a `live.Session`" is not quite true and the
difference matters. `live.Session{}` and `var s live.Session` **do** compile
outside `live` — an empty composite literal names no field. What comes back is a
zero `ID()` and a **nil** `Identity()`, so any hook that reads the identity — and
identity is the reason `Authorize`, `Init` and now `Execute` take a `Session` at
all — gets nothing, or panics. So the accurate statement is: **an application
can construct a useless `Session` and cannot construct a useful one.** A helper
that handed back another identity-less value would add nothing; this one must
take an identity.

The cost is already visible and already paid once. `examples/counter/store.go`
carries a `Execute`/`execute` split whose doc comment is a defect report:
*"a `live.Session` has unexported fields and no constructor, so an application
cannot build one, and a `Config` hook that takes one is testable only through a
running server."* Every application that unit-tests a hook will invent that same
split. FR-15 obliges the library to ship test scaffolding, `livetest` is where it
lives, and its ledger column is a v0.1 target with room in it.

### 6.2 The shape

```go
// package live/livetest
func NewSession(tb testing.TB, id live.ID, identity live.Identity) live.Session
```

- **Both values are the caller's.** `live.ID` is `[16]byte` and an application
  can already build one. Deriving the ID from the subject was tempting and is
  wrong: `MaxSessionsPerIdentity` exists precisely because one subject holds many
  sessions, and checkpoint 2's chat example — two tabs, one user — is the first
  test that needs two distinct sessions for one identity.
- **A nil `identity` is `tb.Fatalf`.** A `Session` whose `Identity()` is nil is
  exactly the trap the zero value already sets; a helper that reproduces it is
  not scaffolding.
- **One symbol, not two.** No `NewSessionWithID`, no options struct, no
  `Anonymous` convenience overload. `livetest.NewSession(t, live.ID{1},
  someIdentity)` is already one line.

### 6.3 The mechanism, and what stops it forging identity in production

`livetest` cannot construct a `live.Session` either — it is a different package
and the fields are unexported — so `live` must expose *something*. It must not be
an exported symbol: `live.NewSession` would be an identity constructor sitting in
the production package, reachable from any handler, which is a materially worse
trade than the problem it solves.

**A module-internal bridge.** A small `internal/` package holding a var that
`live` sets at init and `livetest` reads. `gotth-live/internal/...` is importable
by `gotth-live/live` and `gotth-live/live/livetest` and by nothing outside the
module, so the constructor is unreachable to any consumer except through
`livetest`. DEV-1 may pick a cheaper mechanism with the same two properties —
**zero new exported identifiers in `live`**, and **unreachable from outside the
module** — and should say which in the commit message if it differs.

Three things stop it becoming a forgery route, and they are cumulative:

1. **`testing.TB` is the first parameter**, matching `ReplayN` and
   `AssertDirtyComplete` exactly. Calling it from production code means
   fabricating a `testing.TB` — a visible, absurd act, not an accident. This is
   the `httptest`/`fstest` guard and the house shape in one.
2. **The bridge is `internal/`.** Nothing outside this module can reach the
   constructor except through `livetest`.
3. **Importing `livetest` is already visible.** It links `testing`, and with it
   `flag`, `regexp`, `runtime/pprof` and `runtime/trace`, into a production
   binary — the measured cost §0.1 cites as the entire reason the second package
   exists. That property is asserted for `live` by `internal/arch` today.

Add one assertion to `internal/arch`: **the bridge package's importers are
exactly `live` and `live/livetest`**, via the same `go list` mechanism the other
four assertions use. The argument for the bridge's safety is a claim about who
imports it, and by C-12 condition 2's own principle an unverified claim is how it
quietly becomes false.

### 6.4 The consequences, all in the same PR

- `docs/api-surface.md` §6 gains a row; `live/livetest`'s identifier ceiling goes
  **8 → 9**. Measured goes 2 → 3, so the tool passes either way — which is
  exactly why the row must be written by hand and reviewed, not left to CI.
- **Land it after C-21**, or the struck totals have to be moved as well.
- `examples/counter/store.go`'s `Execute`/`execute` split collapses back into one
  method and the comment goes with it. A comment that documents a missing library
  feature must not outlive the feature landing — that is how a fixed defect gets
  re-reported. **DEV-3.**

**Owner: DEV-1** (the symbol, the bridge, the arch assertion, the ledger row),
**DEV-3** (the counter). **Phase: checkpoint 2.** → **C-25**.

---

## 7. Conditions

Each is an obligation with an owner and a phase. **None blocks anything
currently in flight.**

| # | Condition | Owner | Phase |
|---|---|---|---|
| **C-21** | **§0's counts table holds measurements and nothing derived, and the tool refuses to ignore anything else.** Strike the `total` column and the `Total … incl. fields` row; label the two remaining columns *exact* and *ceiling* in the table itself; compress the five erratum paragraphs to the one sentence in §2.4 and let §10 carry the rest; stop §8 restating `53`/`108`. In the same PR, `tools/apisurface` must **fail** on a counts table containing a cell or a row it does not read, rather than ignoring it — today an unread column can hold any number at all, which I proved by setting it to 9001 and watching CI pass. Prove the fix by mutation, in the commit message: re-add a `total` column with a wrong number and show the FR-65 step goes red. | DEV-1 | checkpoint 2 |
| **C-22** | **Extend P2's conformance spec to the `RESYNC` arm, and remove the comment the amendment inverted.** `test/internal/conformance/provenance_test.go:157` skips every kind but `CLIENT_EVENT`, so a resync snapshot's `event_id` — the identifier B-7 introduced *to preserve provenance* — is joined against the provenance log by nothing. Extend the join to the event-bearing kinds, resolving a `RESYNC` origin to its `ResyncRequest`, and fail the spec when the run contains no resync snapshot rather than passing vacuously. Then delete the now-stale sentence in `internal/protocol/invariants.go`'s `eventBearing` comment that says H-6's wording predates §4.2 — H-6 now agrees with the code, and a comment explaining a contradiction that no longer exists will be read as evidence one does. | DEV-1 + QA-1 | checkpoint 2 |
| **C-23** | **`Script` takes the mount path**, per §4.3: `Script(mountPath string) templ.Component`, a trailing `/` trimmed, a render error for an empty or non-absolute path, `defaultMountPath` and the hand-write-your-own-tag paragraph both deleted, `App.Handler()`'s doc caveat dropped, ledger §5.2's row updated, identifier count unchanged. Specs for all four behaviours, and the **FR-33 three-router test mounts at three distinct prefixes, at least one of which is not `/live`**, asserting each rendered `src` returns 200 from its own router. Land this **before** that test is written. | DEV-1 (library) + DEV-3 (counter, README) | checkpoint 2, ahead of the FR-33 suite |
| **C-24** | **Say that the failure event's error text is not redacted.** `EffectFailedErrorField` carries `err.Error()` or a raw panic value, in production, ungated by `Config.Dev`. Add to that constant's doc comment and to `live/doc.go`'s delivery-semantics section: this is operator-facing detail, it is not redacted, and rendering it into a fragment publishes internal error text and panic values to the browser — name `EffectFailedSourceField` as the value that is safe to render. The library is careful about this for `Error` frames (checklist §5.9); the failure event is a second path to the same disclosure and currently carries none of the same warnings. | DEV-1 | checkpoint 2 |
| **C-25** | **`livetest.NewSession(testing.TB, live.ID, live.Identity) live.Session`**, per §6: one exported identifier in `livetest`, **none** in `live`, built over a module-`internal/` bridge; `tb.Fatalf` on a nil identity; `internal/arch` asserts the bridge's importers are exactly `live` and `live/livetest`; api-surface §6 gains a row and `live/livetest`'s ceiling goes 8 → 9. Land after **C-21**. `examples/counter/store.go`'s `Execute`/`execute` split and its comment come out in the same PR — a comment documenting a missing library feature must not outlive the feature. | DEV-1 + DEV-3 | checkpoint 2 |
| **C-26** | **`Config.Dev` is read by nothing, and FR-23's dev/prod split does not exist.** Measured: `grep -rn '\.Dev\b\|Dev:' --include='*.go'` over the module returns exactly one hit — `examples/counter/main.go:96`, which *sets* it. No library code reads it; every `emitError` message is a fixed generic string, so no stack reaches an `Error` frame in either mode. FR-23 requires both; the ledger marks `Dev` **stable** citing FR-23 and checklist §5.9. **A `stable` field that does nothing is worse than an absent one**: it reads as a shipped safety control. Either implement it or cut it, in the same PR, and if cut take the count down. Separately, and in the same area: a **render** panic emits no `Error` frame (`noteRenderFailure` logs and counts only), where RFC §9 says *"Every recovery: … and an `Error` frame carrying the causal ID"* — reducer panics do emit one (`actor.go:325`), and effect panics deliberately become the failure event instead. Phase 2's exit criterion already pre-registers all three sites, so this comes due at checkpoint 2 either way; the effect site wants the criterion amended to the event, not an `Error` frame bolted on. | DEV-1 | checkpoint 2 (Phase 2 exit criterion) |

---

## 8. What I found outside the five, for routing

- **C-26 above is the material one.** It is not one of the five items; it was
  found while checking whether `Config.Dev` gated the failure event's error text
  for item 4, and it turns out `Config.Dev` gates nothing at all. It touches a
  Phase 2 exit criterion and a `stable`-marked security-facing field, so it is
  the one thing here I would want scheduled rather than queued.
- **QA-1's D-12 is still open and addressed to me.** *"FR-36 asks for 'one trace
  per event' and for the morph 'attached via the causal ID', which are different
  things; measured, the path is 4 traces joined by links. Owner PM-1 + L9-1."*
  It is not in this batch's five and I am **not** ruling on it here, because the
  ruling depends on which of the two readings PM-1 intends FR-36 to carry, and
  that half has not come back. Route it to PM-1 first; I will rule in the same
  pass as the checkpoint-2 gate report.
- **The counter's name-mutation blind spot is worth keeping visible.**
  `820752f6` records that `examples/counter` stays green when
  `live.EffectFailedEvent` is mutated, because its table builds the event from
  the same constant the reducer matches. That is a correct and honest analysis,
  and it generalises: **any** example spec that constructs its input from the
  same constant its code matches on is testing the branch and not the name. The
  library's drift spec covers this one. When DEV-3 writes chat, the same pattern
  will appear again for whatever constants chat introduces, and the answer is the
  same — one drift spec in `live`, not a stronger table in the example.
- **`readLedger` is positional and silent about it.** It takes the first two
  numeric cells of the first row matching each of two labels, and a `continue` on
  the second match. After C-21 the anchor makes shape changes loud; until then, a
  second table anywhere in `api-surface.md` beginning with those labels would be
  ignored rather than flagged. Noted, not conditioned — C-21 subsumes it.

**Nits** (fix only if you are in the file): `820752f6`'s commit message quotes an
assertion message as a spec name (§1); `live/templ.go:37`'s doc comment for
`defaultMountPath` begins "DefaultMountPath is where…" against an unexported
identifier, which C-23 deletes anyway; `session.RetryableError{Err: nil}` is
constructible inside the module and its `Error()` would panic — `live.Retryable`
guards the exported path, so this is a latent internal edge and nothing more.

---

## 9. Disposition

**Five rulings issued, none deferred. Six conditions, none blocking.** Two
documents amended in this batch, in the open, with the amendment recorded in each
document's own changelog: `protocol.md` §6/§7/§12 and
`rfc/001-architecture.md` §9/§12/§17.

Three of the five items were a document lagging behind working code, and in all
three the code was right — H-6's sentence, the RFC's `EffectFailed{source, err}`,
and §0's derived counts. That is a healthy direction to drift in and a cheap one
to fix, and the fix in each case is to move the document rather than to add a
checker. The two that were not — `Script()`'s hardcoded mount and the
unconstructable `Session` — are both the same shape: a caller-side workaround
documented as the intended path, where the workaround's own doc comment is the
defect report. Both are cheap now and get expensive the moment a second example
copies them, which is what checkpoint 2 is about to do.

The one thing I would ask the team to take from this batch beyond its rulings:
the two defects I could only find by **running** — that the `total` column
accepts 9001, and that the script tag 404s off `/live` — were both invisible to
reading, and both sat behind a green CI. Every round so far has produced one of
these. Keep budgeting for it.

— L9-1, 2026-08-04

---

## Orchestrator log — 2026-08-04, after the batch

Not part of the ruling. This records what implementing C-21…C-25 turned up, so
that the next session picks it up from the document rather than from a commit
message nobody re-reads.

**All five conditions are implemented and pushed** (`9338e0ae`, `1b9f0743`,
`97939abd`, `ae983a43`, `f22b689b`), each with mutation evidence in its commit
message, `ci.sh` green in `dis-gotth-live:latest` and the client suite green in
`dis-gotth-live-bench:latest`. Four things are open behind them:

| Item | What it is | Owner |
|---|---|---|
| **C-23 residue** | `Script("//cdn.example/live")` is accepted. §4.3's validation is "empty or does not begin with `/`", implemented exactly as ruled — but a scheme-relative URL begins with `/` and points the runtime at another origin, which is the CDN this library does not have. DEV-1 declined to widen a ruled validation unilaterally, correctly. Needs a one-line amendment or an explicit "no". | L9-1, then DEV-1 |
| **C-22 residue** | The code half is done; P6's resync clause, P5's coalescing set-equality, and the soak-labelled run still execute over `exercise(...)`, which sends no resync, so those clauses are unexercised for the arm C-22 exists to cover. | QA-1 |
| **C-26** | `Config.Dev` is inert and a render panic emits no `Error` frame. Deliberately not folded into this batch: it is the checkpoint-2 error-boundary work and a Phase 2 exit criterion (FR-23), and it wants a suite rather than a patch. | DEV-1 |
| **D-12** | QA-1's FR-36 question — "one trace per event" versus the four traces joined by links that were measured. L9-1 declined to rule until PM-1 says which reading FR-36 carries. **PM-1 goes first**, then L9-1 rules in the checkpoint-2 gate pass. | PM-1, then L9-1 |

---

# Addendum — L9-1, 2026-08-04: the C-23 residue, ruled

**This is my addendum, not part of the batch above.** It rules on the first row
of the orchestrator log — `Script("//cdn.example/live")` is accepted — and
nothing else. §4 and the C-23 row stand as issued; what follows corrects one
clause inside them and opens **C-27**.

| | |
|---|---|
| **Ruled against** | `0d48b92b`, the tree as shipped after `97939abd` |
| **Measured in** | `dis-gotth-live:latest` (Go 1.26.5) and `dis-gotth-live-bench:latest` (**Chromium 151.0.7922.71**, Debian 13) |
| **Ruling** | **CHANGE** — one condition, **C-27**, DEV-1, checkpoint 2, non-blocking |
| **Cost under FR-65** | **zero** new exported identifiers; `live` stays at 45/45 |

**Measurement hygiene.** The worktree does not compile right now — DEV-1's
in-flight `internal/session` edits are mid-flight in a shared checkout — so
every number below was taken against a pristine `git archive HEAD` export of
`0d48b92b`, mounted read-only, exactly as the module-init review did when the
same thing happened. Nothing here was measured against an in-flight edit.

---

## A.1 What I verified by running it, not by reading it

Two harnesses. The first renders `Script` over a battery of mount paths in Go.
The second is the one that matters: **two real HTTP origins on the loopback
interface** — `127.0.0.1:8080` is "the application", `127.0.0.1:8081` is "the
other origin" — a real Chromium loading a real page containing the bytes
`Script` actually emits, and **the real shipped `gotth-live.min.js` (9,143 B)
served by the other origin**. The evidence is the other origin's request log. If
it records a hit, the browser went there.

| # | Check | Result |
|---|---|---|
| **1** | `Script("//127.0.0.1:8081/live")`, no CSP | `<script src="//127.0.0.1:8081/live/gotth-live.min.js" data-gotth-url="//127.0.0.1:8081/live" defer>`. Other origin's log: **`GET /live/gotth-live.min.js`**, then **`GET /live  [WEBSOCKET UPGRADE, subprotocol gotth-live.v1]`**. `<html data-gotth-status="reconnecting">`. The runtime was fetched from the other origin **and opened its live session there**. Not a broken tag — a working one, pointed elsewhere. |
| **2** | `Script("///127.0.0.1:8081/live")` | **Same two log lines.** Go's `net/url` resolves this to `https://app.example///…` — *same origin*. Chromium resolves it to `http://127.0.0.1:8081/live/…` — *other origin*. The two parsers disagree and the browser is the one that matters. |
| **3** | `Script("/\\127.0.0.1:8081/live")` | **Same two log lines**, and this one survives the current `%q` writer doubling the backslash into `\\`. WHATWG's *special-authority-ignore-slashes* state skips every `/` and `\` before the host, so one backslash and two behave identically. `net/url` said `%5C%5C…`, same origin. Wrong again. |
| **4** | The non-adversarial route: `"/" + prefix + "/live"` with `prefix == ""` | `mount == "//live"`, `Render` returns **nil**, and the browser resolves `src` to **`http://live/gotth-live.min.js`** with WebSocket host **`live`**. A string concatenation with one empty variable turns a path segment into a **hostname**, silently. Nobody has to be hostile for this. |
| **5** | **The strict CSP**, CP1-13's exact policy, over check 1 | **`OTHER ORIGIN : no request`.** `script-src 'self'` stops the fetch outright, so the runtime never loads and no WebSocket is attempted. The CSP defeats this completely. |
| **6** | `%q` is not HTML escaping — `Script("/live\" onerror='fetch(\`//127.0.0.1:8081/XSS-EXECUTED\`)' x=\"")` | Chromium's parsed DOM: **`data-tag-attrs="src,onerror,x,data-gotth-url,defer"`** and `onerror="fetch(\`//127.0.0.1:8081/XSS-EXECUTED\`)"` — a real, separate, syntactically clean event-handler attribute on the `<script>` element. `%q` renders `"` as `\"`; the backslash stays in the `src` value and the quote closes the attribute. |
| **7** | `Script("/reports&sect;ion/live")` — a path made **only** of characters the current validation permits | Browser-resolved `src`: **`http://127.0.0.1:8080/reports%C2%A7ion/live/…`**. The unescaped `&sect;` was decoded to `§`. The path the browser requests is not the path the caller mounted, and no character in that input is one anybody would think to reject. |
| **8** | `Script("/live#f")` | `data-ws-ctor="THREW: SyntaxError: Failed to construct 'WebSocket': The URL contains a fragment identifier ('f'). Fragment identifiers are not allowed in WebSocket URLs."` Verbatim, from the browser. |
| **9** | `Script("/live?x=1")` | `src` resolves to `/live` with `?x=1/gotth-live.min.js` as the **query**; the runtime file is never requested, and `<html>` carries **no** `data-gotth-status` at all — the runtime never ran. The C-23 silent no-op, reached through a character instead of a wrong prefix. |
| **10** | `Script("/live\n/x")`, `"/li\tve"`, `"/live\r/x"` | All render, all resolve to a path the caller did not write (`/live/n/x`, `/li/tve`, `/live/r/x` today; browsers additionally *strip* raw tab/CR/LF from URLs before parsing, so the answer changes again once the escaping is fixed). Three spellings, three different wrong paths, no error. |
| **11** | `Script("/live//")` | `src="/live/gotth-live.min.js"` but `data-gotth-url="/live/"` — **the two attributes disagree**, because `Script` trims the trailing slash a second time for `src` and not for the URL. |
| **12** | Inputs I checked and am **not** conditioning on | `"//"` alone trims to `/` and is same-origin — safe. `/%2f%2f127.0.0.1:8081` stays same-origin — percent-encoding is not a bypass. `/app/../live` is normalised by the browser to `/live` — confusing, harmless. No clause below rejects these, deliberately. |
| **13** | FR-65 cost | `go run ./apisurface -root ..` on the pristine export: `live` **45/45** identifiers, **49/49** fields, *"the surface matches the ledger"*. Everything C-27 touches is unexported or a doc comment. |
| **14** | Baseline | `go test ./live/...` on the pristine export: **ok**, both packages. C-23's own specs are green and stay green; nothing below invalidates an existing expected-byte assertion (checked: `/live`, `/app/live`, `/`, `/ui` all render byte-identically under the new writer). |

**Not measured, and why.** I did not re-run the full `ci.sh` or the node client
suite: nothing in this ruling touches the client runtime, the wire, or the
generated code, and the worktree cannot compile right now anyway. The
`connect-src 'self'` half of the CSP was never exercised in isolation, because
under check 5 `script-src` already stopped the runtime from loading — there is
no reachable configuration in which the WebSocket leg is blocked and the script
leg is not, since both attributes come from the same mount string.

---

## A.2 The referral understated it, in two directions

The orchestrator log names one input. Measured, **three** distinct spellings all
begin with `/`, all pass the shipped validation, and all put the runtime *and
the live session* on another origin: `//host`, `///host`, `/\host`. And the
consequence is not "the script comes from a CDN" — it is that
`data-gotth-url` travels with it, so the WebSocket carrying **every event and
every patch for that session** opens against the other origin too. Check 1's
second log line is the whole finding in one line of evidence.

**Why the check is wrong is more interesting than that it is wrong.** I wrote
`begins with "/"` as shorthand for *"a same-origin absolute path"*, in Go,
against RFC 3986. Browsers do not parse URLs with RFC 3986; they parse them with
the WHATWG URL Standard, in which `//`, `///` and `/\` all begin an **authority**.
My own Go probe — `net/url`, the right library, the right RFC, read carefully —
reported checks 2 and 3 as **same-origin and safe**. They are not. A validation
of a string that only a browser will ever parse, written and tested against a
parser that is not a browser's, tests the wrong thing. That is the generalisable
part and it is why the spec below is a positive rule with the browser as its
oracle, rather than a list of bad prefixes.

**And the caller does not have to be hostile.** Check 4 is the case I would lead
with if I were only allowed one: `"/" + prefix + "/live"` with an empty `prefix`
yields `//live`, and the browser turns `live` into a hostname. That is a
one-variable mistake, in ordinary code, producing exactly the silent blank page
C-23 exists to abolish — except that this time the page also tries to hand the
session to a host that does not exist. The realistic input here is a **mistake**,
not an attack, and that is the ground the ruling stands on.

**Second finding, same function.** `Script` builds its tag with
`fmt.Fprintf(w, "…src=%q…")`. `%q` is **Go** quoting, not HTML attribute
quoting. Check 6 proves the consequence in the parsed DOM — `onerror` and `x`
arrive as genuine attributes on the `<script>` element — and check 7 proves it
is not merely a hostile-input concern: `/reports&sect;ion/live` contains nothing
any validation below would reject, and the browser silently fetches
`/reports§ion/live/…` instead. This is the module's one hand-rolled HTML writer.
`Region`, `On`, `OnWith` and `Preserve` all return `templ.Attributes` and are
escaped by templ; `Script` is the single place that writes markup itself, and it
is the single place that does not escape. Checklist §5.8 and FR-50 both say
rendered values are escaped by default, and this is the one exception, unmarked.

---

## A.3 The strict CSP — measured, and what it is and is not worth

Check 5 is unambiguous and I want it recorded plainly rather than hedged: under
CP1-13's exact policy the other origin receives **nothing**. `script-src 'self'`
blocks the fetch, so the runtime never loads, so no WebSocket is attempted. The
inline-handler injection of check 6 is blocked by the same directive for the
same reason. For a deployment that ships that header, every finding in A.2 is
inert and the residual symptom is a blank page plus a console violation.

That materially lowers the severity, and it is why **C-27 is not blocking and is
not a security finding**. It does not lower it to zero, for one reason that is
not a judgement call:

**FR-49 requires the runtime to _function under_ a strict CSP. Nothing requires
a consumer to _send_ one, and the library does not emit one.** CP1-13's evidence
is a header added by QA-1's test proxy, not a header `App.Handler()` sets. The
library ships identical bytes to the consumer who sets that policy and the
consumer who does not, and the second consumer is the common case for a v0.1
library being tried out. A defence that lives in someone else's HTTP response
header is a defence I am not entitled to count as the library's. So: the CSP
demotes this from a security defect to a correctness defect with a security
tail, and a correctness defect inside a validator that already exists is worth
three clauses.

---

## A.4 Ruling

**Ruling: CHANGE.** `normalizeMount` rejects a mount path that is not a *path* —
no authority, no query, no fragment, no byte a URL parser removes or
reinterprets — and `Script` writes **HTML-escaped** attribute values instead of
Go-quoted ones. Zero new exported identifiers. No new concept, no new mechanism,
no second function, no `Config` field.

**This is not a widening of C-23, and the distinction matters.** §4.3(1) already
defines `mountPath` as *"the prefix the handler is reachable at as the **browser**
sees it"*, and the shipped godoc repeats it. `//evil.example/live` is not a
prefix of anything any handler is reachable at. `begins with "/"` was my spelling
of §4.3(1)'s sentence, not a second and looser rule sitting beside it — and the
spelling was wrong, because I spelled it in the wrong parser. Correcting a
predicate so that it admits what its own specification admits is not scope
growth; leaving it is a validator whose doc comment and whose behaviour disagree,
which is the exact shape §9 of this batch says this project keeps producing.

Three things settle the shape, in order of weight:

1. **The mechanism is already ruled and already built.** C-23 decided that a
   mount that cannot address the handler is a **render error**, landing on the
   page request where a handler has an error path. C-27 changes which strings are
   in that set. There is no principled line at which `""` and `"live"` are worth
   rejecting but `"//live"` is not — and check 4 shows `"//live"` is the one a
   real caller actually produces by accident.
2. **A positive rule, not a blocklist.** I am not going to enumerate hostile
   prefixes against a parser this project does not own; checks 2 and 3 are two
   spellings of one idea and there will be a third. The rule below states what a
   mount path *is*, and every clause names the browser behaviour it exists to
   prevent, so the next reader can re-derive it. This is the module-init review's
   own nit — *"validation is a character blocklist rather than a positive
   check… blocklists are the weaker shape"* — applied where it now costs
   something.
3. **Escaping removes the coupling, not just the character.** After clauses 1–5
   no accepted mount can contain `"` or `\`, so `%q` would be *safe* — but only
   because a different function forbids the input, with nothing saying so. Check
   7 shows it is not even safe: `&sect;` is legal under every clause and is
   silently decoded. Escaping properly makes the writer correct on its own terms
   and stops the day someone relaxes clause 3 from silently reopening check 6.
   Same principle as the addendum's one-copy artifact ruling, in the other
   direction: **do not create an invariant between two functions when one of them
   can just be right.**

**DEV-1 was right to bring this back rather than fix it.** A ruled validation is
not a place for unilateral widening, and the discipline of implementing exactly
what was ruled and reporting the residue is what put checks 1–11 in front of me
at all. That is the behaviour I want repeated, and it costs one round trip.

---

## A.5 The alternative I rejected

**"A hostile mount path is out of the threat model. Recorded, closed."**

I take the premise. The mount path is application-supplied, sitting next to
`mux.Handle`, and this library's threat model is *the client is hostile, the
embedding application is trusted*. An application that interpolates untrusted
input into `live.Script` has a larger problem than this function. I am stating
that normatively so nobody re-opens **that** question: **a mount path chosen by
an attacker is out of gotth-live's threat model, and C-27 is not a fix for one.**

I reject it as the *ruling* for three reasons:

- **It answers a question nobody asked.** The reachable input is check 4 — an
  empty variable in a concatenation — not an adversary. Rejecting the fix on
  threat-model grounds would be refusing to fix a bug because the bug is not
  also an exploit.
- **It leaves the predicate disagreeing with its own doc comment**, and the
  godoc is the artifact a consumer reads. "Out of the threat model" does not
  make `begins with "/"` a correct rendering of "the prefix the handler is
  reachable at as the browser sees it."
- **It costs nothing to fix.** Zero exported identifiers, one unexported
  function, one writer line, five table entries. The threat-model argument is
  the right answer to a request for a *new mechanism*; there is no new mechanism
  here.

I also considered and rejected the true minimum — reject a leading `//` and
`/\`, stop there. It is the blocklist shape (reason 2 above), and it would leave
checks 8, 9 and 10 in the tree, all of which are the same silent-no-op class
C-23 exists to end and all of which I have browser output for. One more clause
in a function already doing this job is not a trade worth making.

---

## A.6 The spec, so nobody needs a third ruling

`Script`'s signature does not change. `normalizeMount` gains clauses; `Script`
gains an escaping call. **`live` stays at 45/45 identifiers and 49/49 fields.**

### A.6.1 What a mount path is

> `mountPath` is a **path-only, same-origin reference**: the prefix the handler
> is reachable at as the browser sees it, and nothing else. `Script` emits it
> unchanged apart from trimming at most one trailing `/`.

`Render` returns an error, and emits no tag, when `mountPath`:

| | Clause | The browser behaviour it exists to prevent |
|---|---|---|
| 1 | is empty, or does not begin with `/` | *unchanged from C-23 §4.3(3)* |
| 2 | contains `//` anywhere | a leading `//` starts an **authority** — `"//live"` makes `live` a hostname (check 4) — and an inner or trailing one is an empty segment no router registered (checks 2, 11) |
| 3 | contains `\` | for a special scheme the WHATWG parser treats `\` as `/`, so `/\host` is an authority too, and one backslash and two behave identically (check 3) |
| 4 | contains `?` or `#` | either ends the path, so `src` no longer names the runtime file; `?` makes the filename part of the query and the runtime never loads (check 9), and `#` additionally makes the **WebSocket constructor throw** (check 8) |
| 5 | contains a byte `< 0x20` or `0x7F` | browsers remove tab, CR and LF from URLs before parsing, so the path the browser requests is not the path the caller wrote (check 10) |

`"/"` remains valid and renders as it does today. Clause 2 is checked against
the string **as given**, before the trailing-slash trim, or `"//"` trims to `"/"`
and passes.

Deliberately **not** rejected, and say so in the code so it is not added later:
percent-encoding (check 12 — `%2f` is not a bypass), `..` segments (the browser
normalises them and the result is the caller's business), and spaces. Clauses
2–5 are the set for which I have browser evidence; do not extend them without
some.

### A.6.2 The writer

Emit HTML-escaped attribute values rather than `%q`. `templ.EscapeString` is
already in the module graph and `templ` is already imported by this file; if
DEV-1 prefers stdlib `html.EscapeString`, take it and say which in the commit
message. Write the quotes explicitly rather than letting a verb supply them.

Measured (check 14): for `/live`, `/app/live`, `/`, `/ui` — every path in the
existing specs — the output is **byte-identical**, so no expected-byte assertion
moves and FR-7 is untouched. It differs only where the path contains an HTML
metacharacter, and there the escaped form is the correct one (check 7).

While in the function: derive **both** attributes from the one normalised
string. Today `src` is trimmed a second time and `data-gotth-url` is not, which
is why check 11 renders two attributes that disagree. Clause 2 makes that input
unreachable, but the second trim is still doing load-bearing work for the root
mount, so make it one normalisation with two uses rather than two trims.

### A.6.3 Specs required

Ginkgo v2 + Gomega, per the standing convention, extending the tables that
`97939abd` already added to `live/live_test.go` rather than opening new ones.

1. **Five entries on the existing `"refuses a mount path that cannot address the
   handler"` table**, one per clause: `"//127.0.0.1:9/live"`, `"///x/live"`,
   `"/\\x/live"`, `"/live?x=1"`, `"/live#f"`, and one control character. Each
   `Entry` asserts a render error **and** an empty buffer, as the two existing
   entries do. Name in each entry's comment what the browser does with it —
   these are the cases where the reason is not visible from the string.
2. **One spec that the two attributes agree**: for every accepted mount, the
   `src` is exactly `data-gotth-url` (root-normalised) plus
   `"/gotth-live.min.js"`. That is the invariant check 11 broke and the one a
   future normalisation change will break again.
3. **One escaping spec, through a mount path clauses 1–5 accept**:
   `Script("/reports&sect;ion/live")` renders `&amp;sect;`, not `&sect;`. This is
   the spec that fails today and the reason A.6.2 is not merely belt-and-braces.
   Assert the rendered bytes; the browser half is check 7 and does not need to be
   re-run in CI.
4. **Tighten the FR-33 assertion.** `live/live_test.go`'s
   `"renders a src the mounted handler actually answers"` table asserts
   `Expect(src).To(HavePrefix("/"))` — which **passes** for
   `//evil.example/live/gotth-live.min.js`. The test guarding this property
   currently asserts the very predicate that is wrong. Assert instead that the
   `src` has no authority: `HavePrefix("/")` **and** not `HavePrefix("//")`.

### A.6.4 Documents

- `Script`'s godoc: replace *"empty or does not begin with `/`"* with A.6.1's
  sentence plus the clause list, short. Keep the existing paragraph about why
  the parameter exists; it is good and it is the reason this ruling is narrow.
- `docs/api-surface.md` §5.2's `Script()` row: its description still reads
  *"an empty or non-absolute path makes `Render` return an error"*. Restate as
  *path-only, same-origin*. **The count does not move** — 45 stays 45.
- No PRD change. No RFC change. FR-33 and FR-65 are satisfied as written and
  BL-30 covers the behaviour change: `Script` is marked **experimental** and
  v0.1 makes no compatibility commitment, so tightening an error case costs a
  changelog line. No call site in the repo is affected — `examples/counter`
  mounts at the `MountPath = "/live"` constant, which every clause accepts.

---

## A.7 Condition

| # | Condition | Owner | Phase |
|---|---|---|---|
| **C-27** | **`Script` accepts a path and only a path, and writes escaped HTML.** Per A.6: `normalizeMount` additionally rejects a mount path containing `//` anywhere, `\`, `?`, `#`, or a byte `< 0x20` or `0x7F`, with clause 2 checked before the trailing-slash trim; `Script` emits HTML-escaped attribute values rather than `%q`, and derives both attributes from one normalisation. Measured, the shipped code renders `//host`, `///host` and `/\host` into a tag that makes a real Chromium fetch the runtime from another origin **and open the `gotth-live.v1` WebSocket there**, and renders `"/" + "" + "/live"` into a tag whose host is `live`. Zero new exported identifiers; `live` stays 45/45. Specs per A.6.3, including the escaping spec through `/reports&sect;ion/live` — which fails today — and the FR-33 table's `HavePrefix("/")` assertion tightened, since it passes for `//evil.example/…`. Update `Script`'s godoc and api-surface §5.2's row; the count does not move. **Prove it by the same route I did, in the commit message: render the three authority spellings and show `Render` returns an error where it previously returned a tag.** Not blocking, and not a security fix — a strict CSP defeats every case, measured (A.3) — but the library does not ship that CSP and cannot require it. | DEV-1 | checkpoint 2 |

---

## A.8 What I am routing rather than ruling

- **QA-1 owns nothing here.** All four specs live in `live/live_test.go`
  alongside the tables `97939abd` added. I am deliberately not splitting this
  across two agents for four table entries.
- **The conformance-suite question this raises is not C-27's.** Every check
  above is a *render*-time property and belongs in `live`'s own specs. If the
  FR-25/FR-26 browser suite ever wants an "the runtime only ever talks to its own
  origin" property, that is a conformance property and it should be proposed as
  one, with the harness that exists by then — not bolted onto this condition.
- **The `%q`-as-HTML pattern, elsewhere.** I checked: `Script` is the only
  place in `live` that writes markup by hand. `Region`, `On`, `OnWith` and
  `Preserve` all return `templ.Attributes` and are escaped by templ. There is
  nothing else to sweep, and I looked before saying so.

`nit:` `normalizeMount`'s doc comment explains why it trims *one* trailing slash
and not more — *"silently rewriting more of it… would make this function a
second, quieter router"* — which is right, and is exactly the argument for
clauses 2–5 **rejecting** rather than rewriting. Worth one sentence there when
the clauses land, because the next reader's instinct will be to normalise `//`
away rather than refuse it, and refusing is the whole point.

**The thing to take from this one.** §9 of the batch above asked the team to keep
budgeting for defects that only running finds. This is the third, and it is the
first where a *careful* reading would still have missed it: I checked checks 2
and 3 in Go first, with `net/url`, and Go told me both were same-origin and safe.
The browser told me neither was. When the value under test is a string a browser
will parse, the oracle is a browser — not the standard library, not the RFC, and
not the reviewer who wrote the rule.

— L9-1, 2026-08-04

---

## Orchestrator log — 2026-08-04, the checkpoint-2 work round

Not part of any ruling. Same purpose as the log above: what this round closed,
and what it opened, recorded where the next session will look rather than in a
commit message nobody re-reads.

**Closed and pushed** (`5b6f086e`…`892c4dee`, `ci.sh` exit 0 on the combined
tree, plus the two gates that script announces as skipped — `gen.sh --check`
with the repository root mounted, and the node client suite in the bench image):

| | | |
|---|---|---|
| **C-26** | `Config.Dev` is read by something and a render panic emits an `Error` frame | `87bf5647` |
| **C-22 residue** | P5, P6 and the soak run now execute over a resync, non-vacuously | `0031377b` |
| **D-12** | PM-1 ruled: FR-36 carries the *connected trace graph* reading. PRD is v0.4 | `5b6f086e` |
| **C-27** | `Script` accepts a path and only a path, and escapes what it writes | `5364bae8` (ruling), `d67438f1` (implementation) |
| **FR-61** | the chat example, its suite, and an honest list of what it could not prove | `892c4dee` |

Two findings from the round are worth carrying forward on their own account.
`87bf5647` reached a latent bug that dev mode made live: `truncateMessage` cut
an `Error` frame's message on a **byte**, which was unreachable while every
message was a fixed ASCII literal and drops the whole frame the moment an
application's panic value is appended. And L9-1's C-27 is the third defect this
project has found only by running — the first where careful reading would still
have missed it, because Go's `net/url` calls `///host` and `/\host` same-origin
and a browser does not.

### Open, with owners

| Item | What it is | Owner |
|---|---|---|
| **D-14** | QA-1's find: `live.Limits.CoalesceFlushAt` is unvalidated against H-4's `CoalesceFlushCeiling`. Above it the flush trigger becomes an emission failure and P5's provenance set is lost — measured 8 on the wire against 1,385 swallowed. MEDIUM: unreachable on defaults. Held as a `PIt`; write-up in `docs/qa/checkpoint-2-conformance.md` §4 | DEV-1 |
| **D-12, second half** | PM-1 ruled the reading; L9-1 said they would rule in the same pass as the checkpoint-2 gate report and has **not** read `5b6f086e`. Comes due at that gate | L9-1 |
| **instrumentation.md §3 vs the tracer** | PM-1 found three disagreements: `authorize` drawn as a child but shipped as a root; effect spans called "linked, not nested" but the context is passed through so they nest; eight spans drawn and five (`parse`, `reduce`, `render`, `render.fragment`, `send`) started nowhere. FR-36 as amended permits the current shape only *while enumerated* | DEV-1 + L9-1 |
| **FR-58 gap** | the effect-panic log omits `scheduledBy`, a causal ID it already holds at the call site. One field | DEV-1 |
| **G2 has no baseline** | RFC §6.2 says Phase 1 owed one; the 46,080 B gate still rests on a 42,416 B estimate with two estimated lines. Blocks a Phase 5 number being quotable. Related to C-5 | DEV-1 + QA-2 |
| **I3** | whether NFR-1's *gate* is the sampled or the unsampled figure. PM-1 recorded the two-figure reporting rule, not the ruling | QA-2 + PM-1, Phase 5 |
| **F-1** | `livetest.Client` is documented and unimplemented, so DEV-3 hand-rolled ~260 lines of `protowire` frame reader to test chat off the wire. The ledger's `livetest` ceiling is 9 and measured is 3; this is what the gap costs an application | DEV-1 |
| **F-3** | `live.On("keydown", …)` has no key filter. Re-confirmed with a cost this round, and **the bench equivalence spec's F-CTR-6 needs it** | DEV-2 |
| **F-4** | L9-1 §5.4's re-add trigger for `IsRetryable` has fired at one remove — chat found it | L9-1 |
| **F-6** | DEV-3 asks what FR-55's "first-class" forms means before anyone ships a helper. Not a proposal, a scope question | PM-1 |
| **O-1** | FR-7's `gen.sh --check` enumerates four protobuf files and **does not cover `_templ.go`**, which `templ generate` writes by hand. A generated-output gate that misses half the generated output | DEV-1 + QA-1 |

### What checkpoint 2 still needs before its gate

The chat example is built and its own suite is green, which is the *subject* of
the gate, not the gate. Unbuilt: the DOM-preservation conformance suite across
NFR-7's browser matrix (FR-25, FR-26, FR-27, FR-28), HTMX coexistence (FR-30,
FR-31, FR-32, G8), and the FR-33 three-router mount test — which C-23 requires
be written at **three distinct prefixes, at least one not `/live`**, and which
C-27 §A.6.3(4) has already tightened the assertion for. Then QA-1 gates chat,
L9-1 reviews, PM-1 writes the gate report.

---

## Orchestrator log — 2026-08-04, the three-suite round

Not part of any ruling. Same purpose as the logs above.

**The three suites the section directly above called unbuilt are built**, and
four of the eleven open items are closed. Three streams ran concurrently in the
shared worktree with disjoint file ownership; `ci.sh` is exit 0 on the combined
tree with the repository root mounted, so the FR-7 gate **ran** rather than
skipping, and the only announced skip is the node suite the library image does
not have by design.

| | | |
|---|---|---|
| **FR-33** | one application, three routers (`net/http`, chi, gin), three distinct prefixes — `/live`, `/app/live`, `/ui/gotth` — as a separate module, so chi and gin cannot reach a consumer | `ee476ed4`, `8aea799b` |
| **FR-25/26/27/28** | DOM preservation in a real Chromium: focus, caret, uncontrolled value, element and document scroll, checkbox, radio, `<select>`, media position, an in-flight CSS transition, IME composition, `Preserve` subtree identity | `4de374d8`, `a9416e69`, `3c9a9a2d` |
| **FR-30/31/32, G8** | HTMX coexistence against vendored HTMX 2.0.10, RFC §10.3's precedence rule executed both ways | `c4f937ed`, `3269a6aa` |
| **D-14** | `Limits` has a validated range, rejected at construction rather than clamped | `2dfed02d` |
| **FR-58** | the effect-panic log names the event that scheduled the effect | `8fb6ade9` |
| **O-1** | `gen.sh` generates and checks the templ outputs it was ignoring | `84fed635` |

**Evidence, run on the combined tree, not taken from the reports.** `ci.sh` exit
0 with FR-7 running; `live` 45/45 identifiers and 49/49 fields against the
ledger; client runtime 9,143 B minified / 3,874 B gzip against NFR-2's 12,288 B;
the node suite exit 0 in the bench image; and the browser suite **19 of 154
specs, 19 passed, 0 failed, 1 pending** in `dis-gotth-live-bench:latest`.

Three things from the round are worth carrying on their own account.

**D-14's bound is 1023, not the round number.** QA-1's write-up pointed at
`CoalesceFlushCeiling = 1024`; measured, the flush trigger counts *deferred*
transitions and the frame it forces carries one identifier more, because
`takePending` folds in the origin of the transition being emitted at the time.
A fix written against 1024 would have shipped the defect one value narrower. It
is the fourth defect this project has found only by running.

**The FR-33 suite cost 34 modules that the library did not pay.** Measured:
the root module's `go list -m all` is 61, chi adds 1, gin adds 33, and as
shipped it is still 61 — because the suite is its own module. That number is the
argument for the separation, and it is now in the ledger rather than in a commit
message.

**The DOM-preservation suite caught its own vacuity before it caught anything
else.** The scroll spec originally read its identity tag off a *captured* node
reference, which survives detachment, so it passed under the mutation that
replaces every node instead of morphing it. Seven mutations were run, six of
them rebuilt through `tools/minify` into the served bundle; that one was found
by mutation M1 and fixed. This is the class of test the project has now shipped
twice and caught twice.

### Closed from the eleven

`D-14`, `FR-58 gap`, `O-1`. `F-3` (the `keydown` key filter) and the rest are
untouched and still open with their existing owners.

### Newly open, with owners

| Item | What it is | Owner |
|---|---|---|
| **D-15** | MEDIUM. FR-25 names `<details>` open state and it fails in a browser: `details.open` *reflects* the content attribute, so a user's disclosure is indistinguishable from a server declaration and the next unrelated patch reverts it twice over — `syncProps` closes it, `syncAttrs` removes the attribute. Node identity passes on the line before, so it is the rule and not the traversal. Held as a `PIt` plus a node `todo`; the node DOM shim had hidden it by modelling `open` as a plain property, which `3c9a9a2d` corrects. Write-up in `docs/qa/checkpoint-2-browser.md` §4 | DEV-2 |
| **D-16** | LOW-MEDIUM, documentation gap. `hx-*` markup that a morph **inserts** is inert — 0 HTMX requests on first click, works after `htmx.process`. Measured alongside the good half: a control that already existed keeps working through two morphs with no processing at all. Needs either a documented rule or a runtime option | DEV-3 (docs), DEV-2 (runtime option) |
| **D-17** | LOW. `live/app.go:153` routes the embedded runtime by `strings.HasSuffix(r.URL.Path, clientRuntimeFile)`, so `/live/a/b/c/gotth-live.min.js`, `/live/not-really-gotth-live.min.js` and `/live/xgotth-live.min.js` all serve the artifact with 200. Not a disclosure — the file is public and immutable — but the `ETag` plus `max-age=31536000, immutable` design assumes one URL per artifact, and a caching proxy in front of the app sees an unbounded key space for one 9 KB body. Found by DEV-1 while building the FR-33 suite; numbered here, and QA-1 may renumber at the gate | DEV-1 |
| **D-18** | An application-supplied `live.Event.Contributing` is unbounded and reaches the same H-4 emission failure as D-14 on **default** limits, with no coalescing involved: one event carrying 1,200 identifiers yields `patches=0 errors=1`, a non-fatal `Error{INTERNAL}`, and a state change the client never sees. D-14's `+1` arithmetic holds for the edges the *library* adds and not for the ones an application adds. Not fixed: bounding it changes an exported contract, and whether the bound belongs on the emit path, the flush trigger, or both is a design question. Write-up in `docs/qa/checkpoint-2-conformance.md` §8.4 | DEV-1, then L9-1 |
| **NFR-7 is 1 of 8** | The suites are built; the matrix is not. Measured: Chromium 151.0.7922.71. Not measured, with reasons and no estimates — Chrome previous stable (no 150 build in any image, and the images must not be rebuilt this round); Firefox ×2 (**measured obstruction**: `firefox-esr 140.13.0esr` in a throwaway container speaks WebDriver BiDi and answers `GET /json/version` with 404, while `cdp_test.go` speaks CDP only); Safari macOS ×2 and iOS ×2 (no Safari for Linux, no WebKit in any image, no macOS host). The checkpoint-2 exit criterion is therefore **partially met**, and closing it is a scope decision: add a BiDi harness plus Firefox to the bench image, or amend NFR-7 to state what CI can verify | PM-1, then L9-1 |
| **F-1, paid twice** | `test/routers/wire_test.go` is a trimmed copy of `examples/chat`'s hand-rolled `protowire` frame reader, because a separate module cannot reach `internal/` and sharing would mean a fourth module existing only to be imported by two test modules. Second module to pay it. It goes away when `live/livetest` grows the `Client` it already documents | DEV-1 |
| **`ci.sh` FR-7 residual** | `ci.sh` skips the whole FR-7 step when `../research/protobuf-refinement-types/plugin` is absent, so in the plain library image the templ half is skipped along with the protobuf half even though it needs neither protoc nor the research tree. The GitHub workflow runs the root-mounted context, so nothing is skipped there. Splitting it wants a third `gen.sh` mode; judged not worth it this round, recorded rather than dropped | DEV-1 |

### What checkpoint 2 still needs before its gate, restated

Every suite the gate is *about* now exists and is green. What is left is the
gate itself: QA-1 gates the chat example and the two new browser suites, L9-1
reviews this round's six landings (and owes the second half of D-12, which comes
due in the same pass), and PM-1 writes the checkpoint-2 gate report — which must
rule on whether NFR-7 at 1 of 8 cells satisfies the exit criterion or amends it.
