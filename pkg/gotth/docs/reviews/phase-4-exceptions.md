# Review — FR-20's exceptions register, and the three additive symbols

**L9-1, 2026-08-05, against the tree at `29348a5a`.**

This note carries the rulings that do not belong in `docs/exceptions.md` and the
defects found while verifying it. The register's own rulings — E-1, E-2, §3's
readings, the re-walk requirement — are in `docs/exceptions.md` §7, because that
is where the next reader looks for them.

**Everything below was checked in the tree.** Where I am repeating something
another agent told me, I say so and say what I checked instead.

---

## 1. `MustNew` — RULING: KEEP

DEV-1 landed `MustNew[S](Config[S]) *App[S]` (`live/app.go:135`) and flagged it
against themselves: it removes no bug class, it buys three lines, and its
`docs/api-surface.md` row (`:529`) states the weak justification plainly so it
can be cut on that ground. That is the right way to add a symbol to a library
held to a standard-library bar, and it deserves an answer rather than a nod.

**Ruling: it stays. Permanently, not provisionally.**

### 1.1 The economy is real, and I measured it rather than accepting it

The row says it "buys three lines of a four-line idiom in `main`". That is
checkable, so I checked it. `docs/guide/_samples/quickstart/main.go:39` — the
artifact **FR-53 is measured against** — reads `app := live.MustNew(...)`.
Without the symbol it is:

```go
app, err := live.New(live.Config[State]{ ... })
if err != nil {
	log.Fatal(err)
}
```

Under FR-53's counting rule (PRD `:893` — every line that is not blank, not a
comment, not `package` or `import`), that is **+3 counted lines**: the `if`, the
`log.Fatal`, and the closing brace. The `app, err :=` line replaces the
`app :=` line and costs nothing.

**So the economy is not rhetorical. It is three lines off a requirement that is
measured in lines, in the one file that requirement measures.** FR-53 stands at
39 against a budget of 30 (`fde707f0`); `MustNew` is 3 of the 9 that remain to
find. A symbol that moves a failing measured requirement a third of the way to
its budget, for one identifier and six lines of implementation, is not a symbol
with "only" an economy argument. It is a symbol whose argument happens to be the
one the project chose to measure.

### 1.2 The stdlib precedent is exact in shape and in risk, not merely in name

`regexp.MustCompile`/`regexp.Compile` is the same twin: same argument, same
return minus the error, `Must` prefix. `template.Must` panics with the error
value itself, which is what `MustNew` does (`app.go:138`), so a reader sees the
`*ConfigError` naming the field and what to set it to. `netip.MustParseAddr` is
the third. **This is not a helper the standard library would find novel.**

### 1.3 The one misuse that would matter is fenced by the godoc

The failure mode for a `Must` constructor is a caller who builds the argument
from *runtime configuration* — an env var, a flag, a config file — and thereby
turns an operator's typo into a panic in production. `live/app.go:132–134` draws
exactly that line: *"Use New anywhere that choice exists: a server composing
applications, a test that expects a rejection, anything building a Config out of
configuration rather than out of source."* The godoc names the misuse, not just
the use, which is the part most `Must` documentation omits.

### 1.4 The argument for cutting, and why it loses

The strongest cut argument is not the one in the ledger row. It is: **`MustNew`
does not close FR-53's gap.** 39 with it, 42 without; either way the requirement
fails, so the economy buys a smaller failure rather than a pass, and a permanent
public symbol is a high price for a smaller failure.

It loses because **the alternative is not "keep the three lines and find the nine
elsewhere"** — the remaining nine are in the `Config` literal and the templ view,
where every candidate for removal is a security hook or the event binding this
library exists to provide. `MustNew` is the cheapest three lines available and
they are the only three with stdlib precedent behind them. Cutting it makes the
gap 12, removes nothing dangerous, and leaves the quickstart teaching a
four-line error dance for a failure that is a typo in a literal three lines
above it.

**A symbol earns its place by being the right thing at the call site it was
written for.** In `main`, with a `Config` literal in the source, the right thing
is to panic with the error, and this spells it in one line.

### 1.5 The refused `live.LocalDevelopment(origin)` bundle — RATIFIED

Recorded in the same row group as refused. **The refusal is correct and I have
built on it**: it would have collapsed three security opt-outs into one line and
destroyed the per-check review signal, and that reasoning is what I used to
refuse the `test/memory` scope ruling in `docs/exceptions.md` §7.1. The two
decisions are the same trade at two levels and a project cannot take it both
ways in one week.

> **Citation corrected 2026-08-05 — L9-1, and the heading and paragraph above
> stand as written.** Both attach the name `live.LocalDevelopment(origin)` to
> `docs/api-surface.md`'s row group. **That file has never contained the
> identifier**: `grep -c` returns 0 and `git log -S'LocalDevelopment' --
> docs/api-surface.md` is empty; `:530` records the refusal with no symbol and no
> signature. **I coined the name myself** at `bdf91971` in `docs/exceptions.md`
> §7.1 and reused it here as though it were the ledger's. Found by **PM-1**
> (PRD §9 v1.1 row 2). **The ratification is unchanged and so is what it was used
> for** — only the citation moves, from the ledger's aside to my own §7.1, which
> is the stronger of the two. Corrected beneath rather than in place, per §7.2's
> own rule. See [`fr-53-line-budget.md`](fr-53-line-budget.md) §7.1.

### 1.6 One item routed to DEV-1 from this ruling

**`docs/api-surface.md`'s stability column and its ledger row disagree about
`MustNew`.** The table (`:76`) marks it `stable`, which `:17` defines as
"intended to survive to v1.0 unchanged". The ledger row (`:529`) records it as
cuttable on the ground of weak justification. Both were true while the question
was open; **it is closed now and `stable` is the true one.**

*Routed to **DEV-1**: add the ruling reference to the `:529` row so the cut
argument reads as settled rather than as standing. I am blocked from editing
`docs/api-surface.md` this turn.* No change to the `stable` marking or to the
symbol.

---

## 2. `PageHandler` and `Mux` — no objection, and one trap worth naming

I reviewed both while I was in the file, since they are permanent surface landing
in the same commit as the symbol I was asked about.

**`(*App[S]).Mux` (`live/page.go:208`) — no objection, and it is the strongest
of the three.** It makes two silent failures unexpressible rather than
documented: the missing `mountPath+"/"` registration, whose only evidence is one
`SyntaxError` in a browser console, and the `http.StripPrefix` repair that turns
the upgrade into a 307 a WebSocket client cannot follow. It panics on a bad
mount path on `http.ServeMux`'s own precedent, and it refuses `"/"` with a
message that says what to do instead. That is a symbol that removes a bug class,
which is the bar the ledger asks for.

**`(*App[S]).PageHandler` (`live/page.go:83`) — no objection.** Buffering the
render before writing any byte (`:127`) is the detail that makes it correct: a
render that fails half way is a 500 rather than a 200 carrying a truncated
document. 401 on `Authenticate`'s refusal is right and the godoc gives the
reason — a page whose socket will be refused is a page that cannot work.

**The trap, which is documented but not connected.** `PageHandler` calls
`Config.Init` on **every page request** and discards its effects. The godoc
(`:43–53`) states the trade and gives the rule: *"An Init that is not safe to
call for a read should not be mounted here."* The rule is right. What is missing
is that **the library ships an example whose `Init` breaks it**:
`examples/dashboard/dashboard.go:416` calls `feed.Join(s.ID())`, a registration
into a shared `map[live.ID]*subscriber`, not a read. Mounted through
`PageHandler` it would register a **zero-`ID`** subscriber on every page load,
with no `Teardown` to remove it.

**Severity: low, and I want the reason for that stated as clearly as the
finding.** No shipped example actually mounts `PageHandler` — only `README.md`,
`docs/quickstart.md` and the quickstart sample use it, and their `Init` is nil.
The zero-`ID` key collides into a single map entry rather than growing without
bound. **This is a documentation-cohesion item, not a library defect**, and it
is here because the quickstart teaches `app.Mux(MountPath, app.PageHandler(Page))`
as *the* way to mount, while the most realistic shipped example has exactly the
kind of `Init` that pattern forbids, and nothing in either place mentions the
other.

*Routed to **DEV-3** (guide/examples): a sentence where the mounting pattern is
taught, or in `examples/dashboard`'s README, connecting the two. **DEV-1** may
prefer instead to name the registering-`Init` case in `PageHandler`'s godoc,
which already has the paragraph for it. Either fixes it; both is redundant.*

---

## 3. What I found verifying the register that the register did not carry

Three of these are now fixed in `docs/exceptions.md` and are listed so the owners
know what changed under them. The fourth is routed.

### 3.1 E-1's blast radius overstated the deviation — CORRECTED before signing

The draft said, flatly, that the probe's mutex means "the memory figures this
binary produces carry its cost". **False of every measured run.** `probe` is nil
unless `-probe` is passed (`test/memory/cmd/memsrv/main.go:119–121`, default
`false`), `stackProbe.note` returns at its nil-receiver guard
(`probe.go:154–156`) before touching the pool, `runtime.Stack` or the mutex, and
**`test/memory/measure.sh` — the harness that produces G2's cells — never passes
the flag**. Only `diag.sh:144`'s `on-probe` diagnostic cell does, and the flag's
own help says it "must never be enabled during a measured window".

The row still stands: the call is unconditional in the source and the render
closes over `probe`, so whether that render is a pure function of state is
decided by a flag rather than by the render. **But a blast radius that overstates
is not a thing to sign**, and the correction is part of why E-1 was cheap to
accept rather than worth a scope exemption.

*No owner action. Corrected in place.*

### 3.2 The register's own walk commands did not produce the register's own numbers — CORRECTED

This is the finding with consequences, because it is the entire re-walkability
guarantee. At the drafted tree `2ab0cd57`:

| Stated | Its own command prints | Cause |
|---|---|---|
| 16 reducers | **24** | The pattern also matches every `Reduce: Reduce` wiring line |
| 30 renders | **29** | `Render: *func` cannot match `quickstart/main.go`'s `Render: Count` |

**Both stated numbers were right.** DEV-1 read what the grep could not match and
counted correctly — §1.2's "read, not just matched" was load-bearing. What was
wrong is the claim that the *commands* produced them, and a Phase-5 walker
re-running them would have got two disagreements plus a third from the tree
genuinely moving, with no way to separate them.

Commands, counts and the per-tree table are corrected and pinned to `29348a5a`.
Current truth: **17 reducers, 31 fragment renders, 11 templ files.**

### 3.3 The register was walked against a tree that had already moved — RE-WALKED

`docs/guide/_samples/architecture/` landed at `22a47a6b`, after DEV-1's walk,
adding a reducer and a render the register never saw. I walked it: `Reduce`
(`architecture.go:88`) is pure, the render closes over nothing, `Room.Execute`
takes its mutex at the actor boundary where it belongs, and `Room.Join` in `Init`
is cleared by §3.1's existing reading. **No new deviation.**

**The general point, for whoever walks next:** a register signed against the
commit it was drafted at is a register that is already stale, in a worktree three
agents are committing to. Sign against HEAD and pin the SHA.

### 3.4 §5's proposed lint fires on the fixed tree — NOT COMMISSIONED

`docs/exceptions.md` §5 proposes an `internal/arch` check that no package under
`examples/` or `docs/guide/_samples/` imports `log/slog` from a file that also
declares `Reduce`, and hands the decision to me.

**It would fire on E-2's own fix.** `docs/guide/_samples/errorhandling/errors.go`
imports `log/slog` (`:9`) and declares `func Reduce` (`:63`), and it does so
**on purpose**: DEV-3's fix puts the branching reducer and the logging
`Reporter.Execute` side by side because that adjacency is the lesson. Satisfying
the lint means splitting a teaching file or putting a suppression comment in a
code block readers are invited to copy. Both are worse than the gap.

Declined, with the honest alternative named in `docs/exceptions.md` §7.6: a
**call-graph reachability** assertion from each `Reduce` and `Fragment.Render`
into `log/slog`, `net/http`, `database/sql`, `os`, `time.Now`, `math/rand`.
*Unassigned by choice; **QA-1's** if it is picked up, and its negative control
must be demonstrated against E-2's pre-fix commit before anyone trusts it.*

### 3.5 E-2's page/sample guard is a synchronisation guard, not a purity guard

Worth stating because it is easy to read the green suite as protection.
`docs/guide/_samples/samples_test.go` holds the page's fenced blocks against the
compiled sample, so page and source cannot drift. **It does not stop the
violation coming back**: re-introducing `slog.Warn` into that reducer keeps the
suite green and dutifully updates the page to display the mistake. Nothing
mechanical prevents E-2 from recurring. That is why §5's sentence still stands
and why 3.4's declined lint is a real gap rather than a solved one.

---

## 4. E-2's fix — verified, and DEV-3's landing is what a fix should look like

I verified `091dbae8` in the tree rather than accepting the summary. The reducer
performs no I/O; every `slog` reference in the file is in `Reporter`,
`Reporter.Execute` (`:121`) or `WireLogging` (`:149`); the executor is wired to
`Config.Execute`, which is the actor boundary FR-16 names. The page gained the
rule with its reason (`error-handling.md:241`), a per-field table (`:255`) and a
block at `:308` saying it taught the opposite until today.

**Two things DEV-3 did that were not asked for and that I want on the record.**
The commit fixed two further defects the walk turned up in the same reducer — it
retried without reading `EffectFailedRetryableField`, contradicting the page's
own Classification section, and its retry count is in `State`, the only counter
that survives replay. And the page **names the deviation as E-2 and links the
register** rather than quietly correcting itself. That link is what made
`docs/exceptions.md` §4's "then delete this row" impossible to honour: deleting
it would leave a published page pointing at a register that does not carry what
it names. **A fix that makes the register harder to erase is a better fix than
one that just moves the line.**

*Samples module green at `29348a5a`: `go build`, `go vet`, `go test ./...`
across all fifteen packages, run by me. `go build ./...` and `go test ./live/...`
green in the root module.*

---

## 5. Routed, in one place

| To | Item | Why it is theirs |
|---|---|---|
| **PM-1** | **PRD amendment 1** — FR-20 is silent on what happens when a recorded deviation is fixed. §4's "then delete this row" was a fair reading of that silence and I overturned it. Requested: *a fixed deviation is CLOSED in the register with its disposition and the fixing commit, and retained; entries are not deleted.* | A ruling that lives only in the file it governs will not be found by the next person drafting against FR-20 |
| **PM-1** | **PRD amendment 2** — FR-20's scope over non-shipped trees (guide samples, bench apps, measurement and chaos harnesses) is asserted by `docs/exceptions.md` §1.1, not stated by the requirement. I ratified it and built E-1's refusal on it. Requested: *FR-20 names every tree in the repository that implements the reducer or render contracts, whether or not it ships.* | E-1 exists only under the wide reading, and a scope living in the register can be narrowed by a drafter who does not notice they are narrowing it |
| **PM-1** | **Not touched:** `docs/gates/phase-4.md` §7.6's box-13 split. Nothing in my signature chooses either resolution | It is a scope act and it is PM-1's |
| **DEV-1** | `docs/api-surface.md:529` — record the `MustNew` ruling so the cut argument reads as settled. No change to the symbol or its `stable` marking | §1.6; I am blocked from that file this turn |
| **DEV-1** *or* **DEV-3** | The registering-`Init` trap under `PageHandler`, §2. One sentence in either the godoc or the mounting guidance | Low severity, documented-but-unconnected |
| **QA-1** | The call-graph purity check, §3.4 — **unassigned, not commissioned.** If picked up it needs its negative control shown against E-2's pre-fix commit | It is a correctness guard and the file-level version is a false positive away from being turned off |
| **DEV-1** | Walk the register again at Phase 5, per `docs/exceptions.md` §7.5's standing requirement: re-run §1.2, state the three counts, and if any differs from **17 / 31 / 11** say which directory moved before saying anything else | §4's row, made explicit and now cheap because the commands were fixed |

---

## 6. On the register itself

**Signed.** E-1 accepted as a named exception with the scope ruling refused; E-2
closed as fixed and retained rather than deleted; §3's six readings agreed with
two extensions. `docs/exceptions.md` §7 carries all of it with the arguments.

**Would it survive a Phase-5 re-walk?** As handed to me, no — it would have been
rebuilt, for the reason in §3.2. **As it stands now, yes**, and the fix was to
the instrument rather than to the measurement: the substance was sound
throughout, the two deviations are correct, and the step-4 result DEV-1 asked a
reader to doubt held everywhere I pushed on it.

**And the box still does not tick.** PRD §9's box 13 says it cannot before
Phase 5 and PM-1 has not resolved what that means for the phase. My signature is
not a workaround for either. What is true today is that FR-20 has a register, it
has been walked against the shipped tree by two people independently, and the
deviations in it are named, argued and signed.

— **L9-1**, 2026-08-05, at `29348a5a`.
