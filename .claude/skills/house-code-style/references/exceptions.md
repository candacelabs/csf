# Exceptions and standing rulings

Judgment calls are written down, not silently passed. Every entry names who
decided, when, what it costs, and where the cost gets paid — so a later reader
can tell a considered divergence from an accident.

---

## CS-1 `I`-prefix applies to the public API too

**Ruled:** operator, 2026-09-02, decision 2 of `docs/widget_foundry.md`.

**The ruling.** All exported interfaces take the `I` prefix (`IStore`,
`IReader`, …) **repo-wide, including the published `candacelabs/candace` API**,
the gotth api-surface ledger, and every mockgen directive, in lockstep.

**What it diverges from.** Mainstream Go convention, which names interfaces
after behavior (`Reader`, `Store`) and reserves prefixes for nothing. The
operator is aware of this and chose the prefix anyway; the argument for it is
legibility for agents reading a signature out of context, where `Store` and
`IStore` differ by exactly the fact you need. This is not a mistake to be
corrected by a future reviewer, and it is not open for re-litigation in a code
review. If it is ever reversed, it gets reversed the same way it was made — by
the operator, recorded here.

**What it costs.** 61 interfaces rename, 47 of them exported. Three things must
move in the same commit or the tree stops building; `go-rules.md` § CS-1 has
the measured detail, summarized here:

- `pkg/gotth/docs/api-surface.md` marks renamed rows (`Effect`,
  `Identity`, and others) as `stable`, i.e. "intended to survive to v1.0
  unchanged". The rename breaks that marking on purpose; the ledger rows and
  the commit message both have to say so.
- 13 `//go:generate ... mockgen` directives across 12 files. Six name interface
  types as **literal strings** the compiler never checks — 13 name mentions,
  eight of them in the single directive at
  `services/warden/generate.go`. A stale name there fails at
  `go generate` time, long after the rename looked finished.
- this tree is published whole, so the rename republishes with the next
  snapshot.

**Where it is paid — PAID, 2026-09-02.** Slice P3 of the Widget Foundry program
("Style retrofit: I-prefix + named-params repo-wide, gate goes blocking,
api-surface ledger and mockgen regenerated in lockstep") did all four parts.
CS-1 went 61 to 0 and CS-2 374 to 0 over the same 699-file corpus, the
api-surface ledger's type column and its §10 changelog moved with them, every
mockgen directive and generated double was regenerated rather than edited, and
`.github/workflows/house-lint.yml` now runs
`check_style.py --strict --rule CS-1 --rule CS-2` as a blocking step on every
pull request and every push to `main`. The cost is in
the rename entry (monorepo lab entry `2026-09-02-p3-cs1-rename`) and
the naming entry (monorepo lab entry `2026-09-02-p3-cs2-names`).

**Why report-only was the right posture and not cowardice.** 61 of 61
interfaces violated CS-1 while it held. A gate that fails every pull request for
work nobody has been asked to do is a gate people learn to ignore, and an
ignored gate is worse than none — it launders the debt as "known". Report-only
kept the number visible and moving (`../scripts/style_census.py` writes it to
the ledger every run) without spending the team's attention on it before P3.
The posture ended the way it was supposed to: the flip was made available by the
number reaching 0, not by anyone deciding the rule mattered more.

---

## Corpus exclusions inherited from the reuse gate

**Decided:** by adoption, 2026-09-02 — `check_style.py` reuses the exact
corpus predicate of `tools/check-go-reuse.sh` rather than inventing its own.

The exclusions and their reasons are stated in the header of
`../scripts/go_style_scan.py` and again in `../scripts/check_style.py`. The
judgment call worth recording is the *sharing*: two gates over the same
handwritten-Go corpus that disagree about what "the corpus" means would produce
two incompatible sets of numbers, and someone would eventually reconcile them
by picking the flattering one. They are kept identical on purpose. Changing one
means changing both, deliberately.

The generated-file exclusion in particular is not leniency — it follows
`AGENTS.md`'s "generated files are projections, not owners". A style
finding inside generated output is a finding about the generator, and it must
be fixed there.

---

## The CS-7 SDK refactor is queued behind the in-flight P2 merge — LANDED

**Closed:** 2026-09-02, stage p25s2 of P2.5. The work below shipped exactly as
described: `IWidget[S]` and `IDirtyDeclarer[S]`, `type State any` deleted,
`widget.Register[S]` as the generic shell over the unexported `erasedWidget[S]`
adapter, every generated widget regenerated and every handwritten adapter
retyped. `check_style.py` reports **0 CS-7 findings against `sdk.go`**, down
from 8; the repo total is 3, and all three are the legitimate erasures the rule
permits with a reason (`liquidproto.Error.Value`,
`deploy/operator.ApprovalRequest.Payload`, the mirrored go-redis signature).
The census row at the stage head carries the number.

Two things came out different from the plan, and both are recorded rather than
quietly done:

- `Registry.Register` and `MustRegister` became **functions** rather than
  methods, because a Go method cannot take a type parameter. The plan said
  `Register[S any](registry *Registry, widget IWidget[S]) error` and that is
  what shipped; the entry did not say it was a call-site change at every host.
- `Registry.Lookup` now returns the **registration** rather than the widget. A
  name is a string, so handing back a typed widget would need a second erasure
  site for a question the registration already answers. `LookupWidget[S]` is
  there for a caller that genuinely holds the type, and its assertion is
  explicitly *not* total — asking for the wrong `S` reports false.

The section below is kept verbatim as the record of what was promised.

---

**Decided:** 2026-09-02, when CS-7 was minted from `pkg/widget/sdk.go`.
This is a **planned-work** entry, not a permanent exemption: it records a
sequencing decision and the date it was made, so that "why is the founding
evidence of a rule still in the tree" has an answer other than drift.

**The work.** `type State any` becomes a type parameter: `IWidget[S any]` with
`Mount`, `Reduce`, `Render`, `Unmount` and `Snapshot` typed in `S`, and a
generic shell `Register[S any](registry *Registry, widget IWidget[S]) error`
wrapping a single unexported untyped adapter. That adapter is the one place a
state type is forgotten and the one place one is asserted — CS-7 § 2.

**Why it is not being done in the change that minted the rule.** Slice P2 is in
flight and its stages are building against the current contract. `IWidget` is
the interface every P2 stage implements and every generated widget satisfies, so
changing it underneath them breaks work already in progress — and the refactor
is not a signature edit in one file:

- every generated widget is regenerated, because the emitted per-instance code
  is exactly the code that stops asserting;
- the handwritten adapters are retyped;
- this tree is published whole, so the change republishes with the next snapshot.

Landing that concurrently with P2 would mean merging two rewrites of the same
interface. The refactor is **queued immediately after the P2 merge**, before the
next slice starts, so that it lands once against a settled contract.

**What it costs to wait.** CS-7 reports 6 findings against `sdk.go` until it
lands, and the census carries them (`erased_boundaries`, `cs7_findings`). That
is the intended posture: the gate is report-only, the number is published, and a
finding that stays visible with a written reason is the opposite of an
exclusion. Nothing here silences it, and the entry does not expire the rule — if
the refactor slips, the finding count is what says so.

*(Postscript: the count was 6 when this was written and 8 by the time the
refactor landed, because P2 added `IDirtyDeclarer`. The number moving while the
work waited is the mechanism working, and it is left uncorrected above for the
same reason a ledger row is never edited.)*

## CS-6: `build()` in the widget interpreter is a sequence, not a family

**Ruling (orchestrator, 2026-09-02, at the P2 merge).** The gate's one repo
finding — `pkg/widget/internal/validate/validator_build.go:24`, `build()`
dispatching the fourteen block readers plus `computeProjections` — is exempt
under CS-6's own counterweight. The dispatch order IS the dialect's canonical
block order: later blocks resolve against earlier declarations, and
`computeProjections` must run last because it consumes the resolved whole.
Reordering the calls changes the language's meaning, which is exactly what
"real ordering dependencies" means; the registry shape's payoff (a spec
enumerating an unordered family) does not exist here, because the order is the
semantics.

**The honest counter-argument, kept on the record.** The p2s0 stage noted the
list grows with every new block, so "will never grow" does not cleanly apply.
Accepted — the exemption rests on the ordering dependency, not on fixed size.
If a future dialect version makes block resolution order-independent, this
ruling dissolves with it.

**Posture.** The finding stays visible (report-only tripwire, `cs6_findings`
carries it in the census) rather than being pattern-listed away. A reader of
the number sees 1 and finds this entry; that is the intended pairing.

---

## What stays unfixed outside the corpus, after the P3 retrofit

**Decided:** stage p3s3 (the CS-2 pass), confirmed and re-measured by the p3s4
audit, 2026-09-02. Recorded rather than silenced: the findings below are real,
they are simply outside the tape measure, and CI now blocks on the half that is
inside it.

CS-1 and CS-2 both read **0 over the 699-file corpus**, and
`.github/workflows/house-lint.yml` blocks on that. Running the same scanner
over the tracked Go the corpus predicate *excludes* (94 files, 11 of them
generated) finds work the retrofit did not do:

| Excluded subtree | CS-2 unnamed signatures | CS-1 unprefixed interfaces |
|---|---:|---:|
| `examples/gotth/` | 21 | 0 |
| `pkg/gotth/bench/` | 21 | 0 |
| `pkg/gotth/docs/guide/_samples/` | 11 | 1 (`Provider`, `payments.go:201`) |
| `research/` | 3 | 2 (frozen artifacts) |
| **total** | **56** | **3** |

(The p3s3 lab entry reports the same total of 56 and attributes 13 of them to
`_samples`; re-measuring gives 11 there. The total is right, that sub-count is
not, and it is corrected here rather than in the dated entry.)

**The ruling: they stay, and the corpus boundary does not move to reach them.**

The reason is the boundary itself, not the findings. `check_style.py`'s corpus
predicate is deliberately identical to `tools/check-go-reuse.sh`'s — the section
above this one records why — and **widening it for one rule un-shares it**. Two
gates over the same handwritten Go that disagree about what "the corpus" means
produce two incompatible sets of numbers, every census row before the change
becomes incomparable with every row after it, and the ledger's whole claim
("is it getting better") stops being answerable across the seam. The gate that
just started blocking is not the moment to move its own denominator.

So widening is an **operator decision**, and it is a decision about the reuse
gate as much as this one. Nothing here is a judgment that the excluded code
should violate the rules.

**What it costs, stated plainly.** The tree now teaches CS-2 everywhere except
in the files written to be read as examples, and teaches CS-1 everywhere except
in one interface on a page of the published guide. `_samples` is the code a
reader of `pkg/gotth/docs/guide` copies, so it is the highest-leverage
11 signatures and 1 interface in the whole excluded set, and it is exactly
backwards for them to be the stale ones. That is the cost of the shared
boundary; it is being paid knowingly rather than discovered later.

**Why it did not resolve itself the way CS-1's rename mostly did.** A type
rename has a forcing function — `examples/gotth/` and `_samples` compile
in their own CI steps, so a stale reference to a renamed interface breaks a
build. A parameter name has none: adding or omitting one changes no type and
breaks nothing. Every one of the 56 would compile forever. `Provider` survived
for the mirror-image reason — it is a declaration nothing outside its own file
refers to, so no build ever had to notice it.

**How this entry is discharged, if it ever is.** Either an operator widens both
corpus predicates in one change and a retrofit pass follows, or the entry stays
and the numbers above get re-measured with it. What must not happen is the third
option: widening `check_style.py` alone because CS-1 and CS-2 are cheap to fix
there, leaving the reuse gate measuring a different tree.

---

## CS-8 does not reach method position, and that is a scope ruling, not a gap

**Decided:** 2026-09-02, minting CS-8 from the operator's *"RETURN CONCRETE
IMPLEMENTATIONS ONLY ACCEPT INTERFACES"*. Recorded because the ruling is
absolute and the gate is deliberately narrower than it, which is exactly the
shape of thing that gets mistaken for drift later.

**The ruling.** The gate reports a **receiverless** `func` declaration whose
result is an interface. A method is never reported.

**Why, measured.** A method's result type is fixed by whatever interface it
satisfies. `func (realClock) NewTimer(d time.Duration) ITimer` cannot return
`realTimer` instead: the moment it does, `realClock` stops satisfying `IClock`,
and `IClock`'s whole purpose is that `services/warden/testclock` can substitute
a simulated clock whose timers are simulated too. The decision lives in
`IClock`'s declaration, not in the method, and CS-8 is not a rule about
interface declarations.

That is not a hypothetical: of the **9** method-position interface results in
this corpus, **7** are implementations of `IClock` or `IPullSource` methods
(`realClock.NewTimer`, `realClock.NewTicker`, `testclock.Clock.NewTimer`,
`testclock.Clock.NewTicker`, `watchdog.fakeClock.NewTimer`,
`watchdog.fakeClock.NewTicker`, `jetStreamPullSource.Next`). Enforcing CS-8
there would produce seven findings whose only available "fix" is to delete a
working abstraction, and a gate whose findings cannot be fixed is the CS-5
failure mode with a blocking exit code attached.

**What it costs, stated plainly.** A factory *spelled as a method* —
`func (f *Factory) NewStore() IStore` — is missed. That is real, and it is the
one way to write the founding defect and pass the gate. It is accepted for the
same reason every other imprecision in `../scripts/go_style_scan.py` is: a miss
leaves a violation for the next reader to find, while a false positive fails a
correct pull request, and the second is how a blocking gate gets routed around.

**How it stays visible.** The census carries `interface_return_methods`
(9 at the baseline) next to `cs8_findings`, so the scope the rule declines is
published rather than merely omitted. **A corpus where
`interface_return_methods` climbs while `cs8_findings` holds at 0 is one where
factories moved into method sets to dodge the gate**, and that is the reading
the field exists for.

**The other two exemptions are in the rule text, not here**, because they are
structural rather than judgment calls: a sealed sum type (an exported interface
closed by an unexported method) is a tagged union rather than an abstraction,
and a function whose signature matches a declared `func` type is a hook
implementation whose result type is the hook's. Both are visible to the lexer,
both are exercised by `026-return-concrete`'s clean fixture, and neither is
reachable by writing a marker comment.

### Amendment, same day: the exemption is escapable, and every case was re-audited

**Amended:** 2026-09-02, hours after the ruling above, by the operator reading
the file the ruling was written about: *"WHY DOES CLOCK.GO RETURN INTERFACES
REEEEE"*.

The ruling above is intact — a method's result type is fixed by the interface
it satisfies — and it answered the wrong question. It reasoned from `IClock`
outward and never asked whether `ITimer` was an abstraction. It was not: a
channel and two function values, nothing dispatching on it and nothing able
to. **The exemption was defending an interface that should not have existed**,
and the tell was in the source: `clock.go` carried a three-line comment
arguing for its own interfaces. `lessons.md` has what that generalizes to.

So the exemption now carries a precondition, `go-rules.md` § CS-8 states it as
doctrine, and every method-position case in the corpus was re-audited against
it:

> Before recording a method-position exemption, apply **the data-shaped test**.
> Is the returned abstraction data-shaped — channels, function values, plain
> fields, no behaviour of its own? Then it is not an abstraction: make it a
> concrete struct, and there is nothing left to exempt. The exemption survives
> only for genuinely behavioural returns.

**Result: `interface_return_methods` 9 → 2 on 2026-09-02, and 2 → 1 the next
day.** Seven fixed, two kept, and then one of the two overruled — see the
amendment below the table.

| Site | Verdict |
|---|---|
| `RealClock.NewTimer` / `.NewTicker` | **fixed.** `ITimer`/`ITicker` deleted; `warden.Timer`/`warden.Ticker` are structs filled from `time.Timer`/`time.Ticker`'s own method values. |
| `testclock.Clock.NewTimer` / `.NewTicker` | **fixed.** Same structs, filled from the simulated waiter table and closures over the waiter id. `fakeTimer`/`fakeTicker` deleted. |
| `watchdog.fakeClock.NewTimer` / `.NewTicker` | **fixed.** Same structs; the test double's two private types deleted with them. |
| `election.cluster.route` | **fixed, and not by the data-shaped test.** `IRPCHandler` is three consensus state transitions and is genuinely behavioural — but the harness only ever holds `*Manager`, so the interface was a widening on the way out. This is precisely the "factory spelled as a method" the ruling above accepts as a miss, present in the corpus and found by reading. |

**The two that stay, with the reason per site.** Neither is data-shaped, and
in neither case does a concrete type exist for the declaration to return:

1. **`pkg/gotth/live/core.go` — `(Session).Identity() IIdentity`.**
   **OVERRULED 2026-09-03; kept here because the reasoning was good and the
   ruling still went the other way.** The operator's words:
   *"`func (s Session) Identity() IIdentity { return s.identity }` RETURN TYPE
   IS IIDENTITY FUCK YOU"*. Everything the paragraph below says is true — the
   concrete type IS in the caller's package and `live` genuinely cannot name
   it — and the conclusion drawn from it was wrong. **Go has no existential
   types; it has type parameters, and a type the callee cannot name is exactly
   what one is for.** `Session` is now `Session[I IIdentity]` and
   `Identity()` returns `I`. The finding that follows generalizes the
   data-shaped test of 2026-09-02: before recording an exemption because "the
   concrete type is the caller's", ask whether the caller could hand it over as
   a TYPE ARGUMENT. Where it can, the exemption evaporates the same way the
   data-shaped one does. Measured: the change deleted two library error
   messages and four specifications as unreachable
   (monorepo lab entry `2026-09-03-generic-identity`).

   The original reasoning, preserved:
   `IIdentity` is the *application's* type, not this repository's. It arrives
   through `Config.Authenticate`, which the application implements, and comes
   back out through this accessor so the application can assert it back:
   `sess.Identity().(Member)` in `examples/gotth/chat`, in `room.go`, and in
   the published guide's security sample. There is no concrete type `live` can
   name here — the concrete type is in the caller's package — so this is open
   polymorphism in the strict sense, and narrowing it is not a thing that can
   be spelled in Go. Its single method being an accessor (`Subject() string`)
   makes it *look* data-shaped; the test asks whether the repository could
   have returned a concrete type, and here it demonstrably could not.

2. **`go/pkg/messaging/consumer.go` — `(*JetStreamPullSource).Next(ctx) (IMessage, error)`.**
   Two reasons, either sufficient. `IMessage` is not data-shaped: `Ack`,
   `Nak` and `Term` are round-trips to the broker, and the value's identity to
   JetStream is what makes them mean anything. And the method's body is
   `return source.consumer.Next(...)`, whose static type is `jetstream.Msg` —
   itself a third-party *interface* (`var _ IMessage = (jetstream.Msg)(nil)`
   pins the relationship). There was never a concrete type available to
   return, which is the pass-through counterweight already in the rule text,
   arriving in method position.

**What this amendment does not do.** It does not make method position
reportable by the blocking gate; the scope ruling above stands, and
`interface_return_methods` is still how it stays visible. What changed is that
the number is now a number somebody is expected to *re-audit* rather than
merely watch — and, since the same day, a second CI lane
(`tools/check-ifacereturn.sh`) that reports every interface-typed result in
method position and everywhere else, so the next `cluster.route` is found by a
tool rather than by an operator's patience.

---

## CS-9: three sites that stay hand-rolled, and why converting them is a regression

**Ruling date:** 2026-09-02, landing CS-9.

CS-9 says a wait for a condition is `pkg/eventually`. These sites look
like the rule's target, are not, and will be "fixed" by a later reader unless
the reasons are written down here rather than only in a comment at the site.

### 1. `awaitRaft` stays on `livetest.Client.Await`

`examples/widget/live_test.go`. It is a named `await*` helper, which is
the exact silhouette of the nine the rule was minted against — and it has no
timing loop in it. `livetest.Client.Await` blocks on the frame channel, is the
typed await belonging to the library that owns the frame stream, and fails
naming **every frame it did see**. A generic poller taking one frame per tick
cannot report that, so converting it trades a diagnostic for a uniformity.

The general form, and CS-9 counterweight 4: **the timing loop belongs to
whatever owns the datum.** Reaching past an owning library's own typed await
for the repository-wide one is CS-3 over-applied — centralizing above the layer
that owns the semantics — and it costs whatever the owner knew that the generic
one does not.

### 2. The three best-effort quiesces are not awaits

`pkg/gotth/internal/session/provenance_test.go` (`stableGoroutines`),
`pkg/gotth/internal/wsx/wsx_test.go` (`settled`), and
`pkg/gotth/test/internal/chaos/case6_partition_test.go`
(`settledGoroutines`). Three copies of "sample until two readings agree, then
return whatever you have" — which reads as three authors writing one helper
again, which is the rule's founding complaint.

They stay, and the reason is mechanical rather than aesthetic: **each is itself
polled by an outer `Eventually`** (`provenance_test.go:233`,
`chaos/case7_churn_test.go:124`, `chaos/case6_partition_test.go:142`).
`eventually.Await` fails the test when its predicate never matches, and a fatal
failure inside a poll aborts the retry that is doing the actual waiting.
Converting them compiles, reads better, and breaks the specifications that
depend on them.

The second half of the reason generalizes: **a helper that cannot fail is not
an assertion.** These return their last reading on timeout by design, so there
is no condition for an await to own. They are sampling, feeding a measurement.

**What is *not* ruled here.** That the three are duplicated is a live CS-3
question, and it is deliberately left open: centralizing them means naming a
package for "the settled goroutine count", which is a decision about
`pkg/gotth`'s internals rather than about how tests wait. CS-9 does not
answer it, and a future change that does should say so rather than folding it
into an await conversion.

### The standing instruction

Answer a CS-9 finding at its site with a comment naming which kind of sleep it
is looking at. Never an exclusion list, never a narrowed heuristic, never a
marker comment the scanner honours — for the reasons this file already gives
for the `dupl` threshold and the CS-5 leaf mutex. All ten of CS-9's current
findings are correct code; a rule whose gate is entirely locator is the one
most likely to be silenced for convenience, and the one where silencing it
costs the most.

---

## OPEN: 129 standard-library `TestXxx` functions live outside every test-style walk

**Measured:** 2026-09-02, after the `tools` widening of
`pkg/scripts/check-test-style.sh`. **Not converted.** Whether to convert
any of it is an operator scope decision, and this entry exists so that decision
is taken against numbers rather than against an impression.

The trigger was one suite. `tools/ifacereturn`'s tests shipped as stock
`analysistest` with two plain `TestXxx` functions, under a header comment whose
justification was, verbatim, *"check-test-style.sh scopes the Ginkgo convention
to pkg, so this package is outside it by that script's own boundary
rather than by an exemption written here."* Operator, on finding it: **"IFACE
RETURN TEST IS NOT USING GINKGOGOMEGAGOMOCK HOLY FUCK"**. The suite is now
Ginkgo/Gomega and the walk now covers `tools`. The measurement below is
the other half: how much more of the tree that same sentence would have been
true about.

Counted with `check-test-style.sh`'s own two regexes, character for character,
over every tracked `*_test.go` outside `research/` and `vendor/`. A file
contributes `TestXxx declarations − RunSpecs bootstraps`, so a compliant Ginkgo
suite contributes 0.

| Area | test files | RunSpecs bootstraps | stdlib `TestXxx` |
|---|---:|---:|---:|
| `go/services/admin` | 17 | 1 | **96** |
| `pkg/gotth` (pruned by the walk) | 112 | 26 | **24** |
| `go/benchmarks/gc` (isolated Go 1.25 module) | 1 | 0 | **4** |
| `app/warden/e2e` | 3 | 1 | **3** |
| `bazel` | 1 | 0 | **2** |
| `pkg` (in the walk) | 65 | 22 | 0 |
| `tools` (joined the walk 2026-09-02) | 2 | 1 | 0 |
| `services` | 98 | 29 | 0 |
| `examples` | 26 | 9 | 0 |
| `app` (other) | 10 | 5 | 0 |
| `xetcas` | 2 | 1 | 0 |
| `github-runner` | 8 | 1 | 0 |
| `go/pkg`, `go/examples` | 19 | 10 | 0 |
| `go/services` (other) | 28 | 8 | 0 |
| **total** | **392** | **114** | **129** |

Three readings, and the second is the one that matters.

**Every area a walk reaches reads 0.** That is not a coincidence and it is not
evidence that the convention is self-sustaining; `pkg` and
`tools` are the only two subtrees any test-style gate walks, and both
are clean. Several unwalked areas are clean too — `services` at 98 test
files and 29 suites is the largest — so being outside a walk does not by itself
predict drift. It predicts that drift, when it happens, is invisible.

**`go/services/admin` is the founding measurement happening a second time.**
`go/CLAUDE.md` has said "use Ginkgo v2 with Gomega for all behavior tests; a
standard-library `TestXxx` function is allowed only as the `RunSpecs`
bootstrap" for as long as the file has existed. There is no `check-test-style.sh`
anywhere in `go/`. 96 of the repository's 129 stdlib `TestXxx` functions — 74% —
are in that one service, under that written rule, enforced by nothing. This
skill exists because 15 of 15 interface signatures in `go/` violated a rule that
file had mandated for a year; this is the same file, the same module, and the
same shape of result.

**The other 33 divide into two kinds, and only one of them is a question.**
`pkg/gotth`'s 24 are *pruned by a written exemption* with a stated
reason and its own gate (`pkg/gotth/ci.sh`) — a recorded ruling, not a gap, and
converting them is the deliberate content change that ruling declines.
`go/benchmarks/gc`'s 4 sit in an isolated Go 1.25 module that deliberately does
not share the fleet's dependency set. `bazel`'s 2 gate two `.bzl` files
against `MODULE.bazel`; the package contains no Go worth speaking of and its
"tests" are a version-pin comparison. That leaves `app/warden/e2e`'s 3
as the only genuinely open case in this tree.

**What is being asked of the operator, precisely:**

1. **`go/services/admin` (96).** Either widen a gate to `go/` — the private
   module has none — or amend `go/CLAUDE.md` to say the convention is aspirational
   there. Leaving a written rule with no gate is the one option this skill's own
   founding measurement rules out.
2. **`app/warden/e2e` (3).** An e2e suite that drives a built binary.
   Ginkgo would fit it (`eventually.Await(GinkgoTB(), …)` is already used in
   `cli_contract_test.go` beside them), so this is a cost question, not a
   feasibility one.
3. **`pkg/gotth` (24), `go/benchmarks/gc` (4), `bazel` (2).**
   Recommend leaving these; each already has a written reason. Recorded so the
   30 are not miscounted as debt.

Nothing here is converted in the change that measured it. A conversion that
nobody asked for, landed alongside a gate widening, is how a scope decision gets
made by an agent instead of by the operator.

**One methodology note, so the number can be trusted or corrected.** The gate's
regex requires the parameter to be spelled `t` (`^func Test[[:alnum:]_]*\(t
\*testing[.]T\)`). Re-counting with a regex that accepts any parameter name
gives **the same 129**, so on this corpus that literal costs nothing. It is
still a real blind spot in the gate, and a future author who writes
`func TestFoo(tb *testing.T)` walks straight past it.

---

## CS-10: the service audit

Every service directory in both modules, read on 2026-09-02 — the five this
change did not touch at `5840ab605`, and `go/services/foundry-board` at
`a3402ab93`, after the restructure that landed with the rule. Read, not
scripted: CS-10 has no line in `check_style.py` and the reasons are in
`go-rules.md`'s enforcement paragraph.

### `services/deploy` — compliant

Functional-option constructors throughout: `component/component.go:103`
(`New(name, options...)`, with `WithAssemble`, `WithStart`, `WithStop`,
`WithRequires`), `webui/webui.go:243`, `control/runtime.go:95`,
`httpapi/api.go:89`. No `package main` anywhere under `services/`, no
`cmd/`, no Dockerfile, no compose file; the only non-Go files are codegen inputs
and assets. The runnable composition is `app/deploy/cmd` and
`app/nodeexec/cmd` over
`app/deploy/bootstrap/bootstrap.go:149`, itself
`Run(version string, functionalOptions ...Option) error`. Containerization lives
at app level (`app/nodeexec/Dockerfile`), never under
`services/`. This is CS-10 as the taxonomy in `AGENTS.md:48-50` already
described it, complied with before the rule existed.

### `services/warden` — compliant, with a spelling nuance recorded so nobody "fixes" it

No `package main`, no Dockerfile, no compose file inside. Composition is
`app/warden/cmd/main.go`; deployment is `app/warden/Dockerfile`,
`app/warden/deploy/warden.service` and root `docker-compose.warden.yaml`. The
split is documented in-package at `services/warden/doc.go:40-41`.

**The nuance:** warden has **zero** `With*` options. Every constructor is
config-struct-plus-interfaces — `dashboard/dashboard.go:84`,
`election/config.go:149` (`NewManager(cfg, tr, st, clock)`),
`watchdog/watchdog.go:64`, `config/config.go:211`. It satisfies all four clauses
of the rule; it does not use the functional-option *spelling* the ruling names.

**Ruling: warden is not to be churned into options.** The ruling names
functional options because that is the shape that composes and extends; a
validated config struct at a seam nobody needs to extend is the same idea with
less ceremony. CS-10's test is "could a different binary use this, and could it
run with no container" — warden passes both. A future reader finding this
mismatch should read this paragraph, not open a refactor.

### `go/services/foundry-board` — compliant, and the founding violation

Before this change: every composition decision in `cmd/main.go`'s flag wiring,
no importable seam, a compose file the README documented first. After:
`foundryboard.New(options ...Option) (*Service, error)` with twelve `With*`
options, `Run(ctx)` under the caller's context, `Handler()` for a binary that
already owns a server, and a `cmd/` that is argv-to-options and nothing else.
The compose file stays, its header now says it is one deployment, and the
README documents the bare binary first. `candace board run` starts it with no
container on `127.0.0.1`.

### `go/services/candace-cloud` — was doc-gap-only; fixed here

Structure was already right: `cmd/candace-cloud/main.go` is 17 lines and calls
`bootstrap.Run`; the seam is
`internal/bootstrap/service.go:38 AssembleHomepage(configuration config.Config, version string)`
with an injectable `config.Load(lookup func(string) (string, bool))`. The whole
external contract is four environment variables, assets are embedded, generated
code is committed, and the compose file at the repository root injects nothing a
shell could not. **The bare run worked and was documented nowhere**: the
README's only run path was containerized, justified by "The host has no Go
toolchain" — a fact about one VM's operator presented as a fact about the
service, which is the CS-10 conflation in miniature. Repaired by adding the bare
path above the container path, and by saying which of the two that sentence is
about.

### `go/services/admin` — was doc-gap-only; fixed here

`cmd/main.go:351-402` composes real option-taking constructors
(`dashboard.New(provider, dashboard.WithWindow(...), WithRefreshInterval,
WithSnapshotTimeout, WithAdminGroup)`, `httpserver.New`, `logrelay.New`), all
under `internal/`, so nothing outside the service can import them — acceptable
for a service with one binary, and noted rather than charged. Bare run works:
`internal/config/config.go:125 LoadFromEnv` defaults to `:8080` and every knob
is an environment variable. **It was documented nowhere** — the Commands block
in `go/services/admin/CLAUDE.md` had `go build ./services/admin/cmd`, a build
and never a run. Repaired with one line naming the two things a local run needs
(`CANDACE_ADMIN_MODE=development`, because `Mode` defaults to `production` and
production requires a ≥32-character proxy-token file).

### RESOLVED (2026-10-01, slice S3): `go/services/ping` fails CS-10 structurally

Resolved: `go/services/ping` is now a library (`NewPingService`, `Register`,
`Start`) with no `cmd/` and no Dockerfile; `go/app/<host binary>` mounts it on a
second listener, the templates are embedded (`statictemplates.ParseTemplates`),
and the pastebin/007 sweeps run in the service's runtime scope. The record
below is kept as written.

`cmd/main.go:15-46` holds the engine, template loading, metrics, the route
registration for `secret007` and `pastebin`, and the `:8000` bind. There is no
library seam: `internal/handlers` exports two `gin.HandlerFunc` factories and
nothing that composes them.

The measured fact that makes this the rule's sharpest case: **the bare run is
documented, and documented first, in three separate files** — root `CLAUDE.md:246`,
`go/CLAUDE.md:69`, `go/services/ping/CLAUDE.md:17` (above the `docker build`
line at `:22`) — **and all three are broken as written.** `main.go:23` is
`template.ParseGlob("/root/web/statictemplates/*.html")` under the comment
"Path uses Dockerized relative path". `/root` exists only because the root
`docker-compose.yaml:20` mounts `./go` there; on a host that glob matches
nothing and `main.go:25` calls `core.Logger.Fatal()`. `candace ping dev` does
not rescue it either — it mounts `./go` at `/candace/go` against a one-line
image with no CMD.

**The repair is a code change** (embed the templates, or take a template root as
an option) and it is deliberately not in the change that wrote CS-10: the rule
landed with one restructure, and a second service rewritten in the same commit
would have made neither reviewable. Recorded here so the number is not lost.
`go/services/ping/CLAUDE.md:17` is additionally stale — it says `main.go`, which
now lives under `cmd/`.

### OPEN: `go/CLAUDE.md` defines a service as a binary, and CS-10 says it is not

`go/CLAUDE.md:47-65`, "Creating New Services", opens "When asked to create a new
service (**runnable binary**)" and lists `Dockerfile (containerization)` at
`:55` among the mandatory scaffold files, reinforced at `:98` ("Services (in
./services/*/cmd) ... complete runnable binaries"). Under CS-10 that is backwards
twice: it defines a service as a binary, and it makes a Dockerfile part of a
service's definition rather than a property of its operator's deployment.

**This is the file to correct, not the root container-first rule.** Root
`CLAUDE.md:32-35` is about how the fleet deploys binaries and is compatible with
CS-10 as written; the only ambiguity is that it says "services" while
`go/CLAUDE.md` defines "service" as the binary. Rewriting `go/CLAUDE.md`'s
scaffold section is a change to the conventions every new `go/` service is
generated from, and it belongs in a change that can be reviewed as that rather
than as a footnote to a rule.
