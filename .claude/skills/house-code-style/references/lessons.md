# Lessons

Append-only. Each entry is a thing this repo learned the expensive way, stated
so the next agent does not have to learn it again. Add an entry when a rule
changes because of evidence — not when it changes because of taste.

---

## A rule without a gate is aspirational

**Learned:** 2026-09-02, taking the Widget Foundry style baseline.

`go/CLAUDE.md` line 139 has said "Always name interface input parameters" for
as long as the file has existed. Nothing checked it. When the baseline was
measured, **every** interface method signature in `go/` that takes parameters
violated it — 15 of 15 at `259164773`, 14 of 14 when the number was first
taken. Not most. All of them.

The rule was clearly written, in the right file, in a file agents are
instructed to read. That was not enough, and the failure was total rather than
partial — which is the interesting part. A rule enforced by nothing does not
degrade gradually into partial compliance; it is simply not a rule, and code
lands at whatever the surrounding code already does.

**What follows from it, and what this skill does about it:**

- Every rule in `go-rules.md` names its enforcement point. A rule that cannot
  name one is written down as an aspiration, explicitly, so nobody mistakes it
  for a constraint.
- `../scripts/check_style.py` exists so CS-1 and CS-2 have a real one, and
  `../scripts/style_census.py` writes the number to the ledger on every run, so
  "is it getting better" is answerable rather than felt.
- Report-only is a legitimate posture (see `exceptions.md`), but only when the
  number is *published*. Report-only with nobody reading the report is the same
  failure with more machinery.
- The corollary for briefs: restating a rule in a subagent brief
  (`brief-snippet.md`) is not enforcement either. It raises compliance; it does
  not guarantee it. Verify the diff.

---

## A rule enters through the framework, or it is a note

**Learned:** 2026-09-02, landing CS-5 ("bias against mutexes in favor of csp"),
the first rule added after the framework existed.

CS-1 through CS-4 were written and *then* given gates. CS-5 was the test of
whether the machinery is a workflow or a monument, and the answer is that a new
rule now arrives whole, in one change: **rule text with its counterweights, a
gate heuristic, a tier-1 fixture pair, and a tier-2 rubric** — plus the census
field that carries its number, so the rule is measured from its first commit
rather than from whenever someone gets around to it. The cost was hours, not
days, and the rule was falsifiable the same afternoon: 107 mutex declarations
in the corpus, which is a fact about the code rather than a defect count.

Two things that only showed up by going through the framework, and would not
have shown up by writing the rule down:

- **A rule can be true and still not be gate-shaped.** CS-5's exemption (a leaf
  mutex is honest) is invisible to a lexer, so the heuristic over-reports on
  purpose and can never block. Writing the fixture pair is what forced that
  admission: the clean fixture *could not contain* the very thing the rule
  permits (`../evals/cases/020-csp-over-mutexes/expected.json`). A rule that
  had only been written down would have claimed an enforcement point it does
  not have.
- **The eval corpus can be gamed by its own history.** With two negative cases
  already in it, "answer don't" became a winning strategy, so the new judged
  case was built with a split verdict — keep one lock, convert the other
  (`../evals/cases/021-csp-judgment/rubric.md`). Balance is a property of the
  corpus, not of each case.

---

## A rule is minted the moment taste is violated, not when it is planned

**Learned:** 2026-09-02, landing CS-6 ("composable function values over method-set
accumulation").

CS-6 arrived from the operator reading a live diff — a six-way dispatch list in
P1 interpreter code — and reacting: *"why couldn't this be functional options
pattern"*, *"dude stuff like this man"*. That reaction is the trigger this whole
framework exists to catch, and the cost of catching it is that the rule gets
written, gated, fixtured and measured **in the same commit as the reaction**,
while the offending code is still on screen and the taste behind it is still
articulable. A rule deferred to a planning pass is a rule reconstructed from
memory, and what survives that reconstruction is the headline without the
counterweights.

---

## The gap between a correct analysis and a correct API is itself rule-shaped

**Learned:** 2026-09-02, landing CS-7 ("generics at the boundary, erasure in one
place") — the third rule minted in one day from the operator reading a live
diff, after CS-5 and CS-6.

The widget SDK's `type State any` came with a comment that argues its own case
correctly: Go has no existential types, a registry of differently-parameterized
values cannot be spelled, so something must erase. Every word of that is true.
The operator read it and said *"are you guys an idiot we literally have
generics"* anyway — and was right, because the argument proves erasure must
happen *somewhere* and the code had applied it *everywhere*: at the exported
interface each author implements, rather than at one unexported adapter inside
the package that owns the collection.

**A defect can therefore sit entirely in the distance between a sound argument
and where its conclusion was applied**, and that distance is invisible to
review-by-reasoning. Reading the comment, agreeing with it, and approving the
code is the natural path, and it is how this contract shipped. The check that
catches it is not "is this justified" but "is the justification load-bearing at
*this* surface" — which is a rule-shaped question, so it became a rule.

Two consequences worth carrying:

- **A rule can be minted against code whose author reasoned well.** CS-5 and
  CS-6 were minted against code nobody had argued for. CS-7 is the first minted
  against a documented, defended decision, and the rule text has to open by
  saying the analysis is correct — otherwise the fix is "pretend Go can type a
  heterogeneous registry", which is a worse design than the one it replaces.
  Relocate, do not eliminate.
- **The detector had to resolve the alias to see the defect at all.** The
  erasure was spelled `State`, not `any`. A bare-`any` heuristic reports 3
  findings on this corpus, all three of them legitimate — 100% noise — and
  misses the founding evidence entirely. The rule and its gate were built in the
  same change, which is the only reason that was discovered before shipping
  rather than after.

---

## A gate that reads counts cannot see a spelling

**Learned:** 2026-09-02, twice in one afternoon, doing the CS-1 rename and then
the CS-2 named-parameter pass.

`pkg/gotth/tools/apisurface` is the gate that fails CI when the gotth
public surface drifts from the hand-maintained ledger at
`pkg/gotth/docs/api-surface.md`. It compares **counts** — exported
identifiers and exported struct fields, per package — against two rows of a
markdown table. Renaming `Effect` to `IEffect` changes no count. Naming a
parameter changes no count. So across both stages the tool printed the identical
verdict:

```
  live                56/56       53/53      109/109     (measured/ledger)
  live/livetest       37/37       33/33       70/70      (measured/ledger)
  the surface matches the ledger
```

The enforced ledger would have passed with **every symbol row in it stale**,
twice, and nothing would have said so. Both times the ledger was corrected by
hand and by reading, and a §10 changelog entry recorded that the counts do not
move.

This is not an argument that the tool is wrong. Its own §0 says it "holds
measurements and nothing derived", and that shape exists because of a real past
defect in which a derived struct-field count was miscounted for months. The
tradeoff is sound and this is its price, paid twice inside one day.

**What follows from it, and it generalizes past this one tool:** a gate's
enforcement surface is exactly the projection it compares, and every property
outside that projection is unguarded no matter how alarming the gate's name is.
Before trusting a gate to catch a class of change, ask what function of the tree
it actually compares — then ask whether your change moves that function. A
rename does not move a count. `check_style.py` is the gate that *did* see both
retrofits, because the function it compares is the spelling.

---

## The rule was written about interfaces; 79% of its debt was in func types

**Learned:** 2026-09-02, doing the repo-wide CS-2 pass.

CS-2's founding measurement is about interfaces — `go/CLAUDE.md` mandated named
interface parameters, nothing enforced it, 15 of 15 violated it. When the 374
repo-wide findings were classified by the kind of signature they sit in:

| Kind | Findings | Share |
|---|---:|---:|
| func type (including every function literal) | 297 | 79.4% |
| function declaration | 41 | 11.0% |
| **interface method** | **36** | **9.6%** |

**The half of the rule that had a written mandate and no enforcement was the
smallest half by a factor of eight.** The interfaces are where the rule's
authors were already looking, so that is where compliance was least bad. The 297
func types are where nobody was: hook fields on configuration structs
(`Authenticate func(*http.Request) (IIdentity, error)`) and the test literals
that satisfy them.

Two things to carry:

- **A rule's debt is not where its author was looking.** Attention is itself a
  weak enforcement mechanism, and it concentrates exactly where the rule text
  points — which means the measured distribution of a rule's violations is
  information the rule text does not contain. Measure before assuming the
  worklist looks like the rule.
- **This is CS-6's counterweight 2 with a number on it.** CS-6 already warns
  that converting a method set into function values *multiplies* the func types
  in the tree, making it the rule most likely to introduce CS-2 debt while
  satisfying another rule. The tree's CS-2 debt was already 79% func types
  **before anyone acted on CS-6 at all**. That is the multiplication warning
  quantified against a corpus that had not yet been through the transformation,
  and it says the warning is about the dominant shape rather than a corner case.

---

## The `I` prefix made return-position interfaces loud enough to interrogate

**Learned:** 2026-09-02, minting CS-8 ("return concrete implementations, only
accept interfaces") from `app/warden/cmd/main.go`.

The founding site was **defensible-looking, and had in fact been defended.**
`buildDiscoverer` is a config-selected factory: a switch over the discovery
mode returning a Tailscale discoverer, a file discoverer, or `nil`. It carries a
nine-line comment explaining precisely why the static arm returns `nil` rather
than a `NewStatic` — a nil `Discoverer` selects the election manager's static
membership semantics, and passing an implementation there would flip a static
fleet into dynamic ones. Nothing about it reads as a defect. It is the shape
every Go tutorial teaches, and the comment proves somebody thought about the
hard part.

What made it get read at all was CS-1. `warden.IPeerDiscoverer` in result
position is *typographically loud* in a way `warden.PeerDiscoverer` is not: the
prefix the operator imposed for one reason — legibility for an agent reading a
signature out of context — turned every return-position interface in the tree
into something a reader's eye stops on. The rule that came out of the stop was
not the one CS-1 was written for.

The operator then ruled the **default** the other way, in five words:

> RETURN CONCRETE IMPLEMENTATIONS ONLY ACCEPT INTERFACES

Two things follow, and the second is the more useful:

- **A rule can be minted against code whose comment is correct.** CS-7 was the
  first minted against a documented, defended decision (`type State any`);
  CS-8 is the second, and the pattern is now worth naming. In both, the defence
  is sound about the *hard* part and silent about the part the rule governs.
  `buildDiscoverer`'s comment is right that the static arm must produce a nil —
  and says nothing about the fact that returning nil *through an
  interface-typed result* is the exact construct that makes a nil unreliable.
  Move one arm to `return (*discovery.Tailscale)(nil)` and the comment is still
  true, the code still compiles, and every `if discoverer != nil` downstream is
  now wrong. Reading the comment, agreeing with it and approving the code is the
  natural path, twice over.
- **One convention's cost paid another rule's discovery.** CS-1 cost 61 renames
  across a public export boundary and is a deliberate divergence from mainstream
  Go. Its return on that, unplanned, is that the *next* rule's evidence became
  greppable — and its gate became precise, because `^[Ii][A-Z]` cross-checked
  against the declared interface list is a decidable test where "does this
  result type happen to be an interface" would have needed a type checker this
  stdlib-only lexer does not have. A convention that makes a class of thing
  visible is worth more than the thing it was adopted for.

---

## The primitive was in the dependency tree the whole time

**Learned:** 2026-09-02, landing CS-9 ("tests never hand-roll time") — the
sixth rule minted from the operator reading live code, and the first one where
the fix required writing no new capability at all.

The catch was *"we keep rewriting the same await test helper function holy
shit"*. The measurement behind it: nine private await helpers — four named
across four files, five more inside one end-to-end suite — plus a dozen waits
spelled as a sleep in a `for`. And **89 test files that already imported
gomega and already called `Eventually`**. Every module in the tree had the
polling engine as a direct dependency, in `go.mod`, resolved, vendored into
the build. Nobody was missing a library.

**What was missing was a typed way to call the one they had.** `Eventually` is
a pre-generics reflection API: it takes `any`, it matches through `reflect`,
and it hands back nothing. So an author who wanted a value out, or a predicate
the compiler checked, wrote a wrapper — and since the wrapper is four lines,
each of the nine wrote their own rather than looking for someone else's. The
duplication was not ignorance of the library. It was the library's signature
being one step away from what the call site wanted, nine times over.

Three things follow, and the third is the one that generalizes past this rule:

- **Check what you already depend on before writing a helper.** The house
  already says *prefer libraries over hand-rolled* (`docs/`, and the operator's
  own standing note). CS-9 is that doctrine's failure mode with a number on it:
  the doctrine was followed at the *module* level — the library is a
  dependency — and violated at every *call site*, which is where it was
  supposed to bite. "Do we depend on something that does this" is a question
  with a mechanical answer, and it was never asked.

- **The answer to a duplicated wrapper is one wrapper, not zero.** The tempting
  correction was "stop wrapping, call `Eventually` directly", and the operator
  rejected it while the rule was being written: *"why can't we use
  parameterized types, we literally have generics"*. Prescribing an `any`-typed
  reflection API at 89 call sites is CS-7's founding defect applied 89 times.
  So CS-9's answer is CS-7's: the erasure is real, it stays, and it happens
  **once**, inside `pkg/eventually`, behind a typed shell. Nine private
  wrappers became one public one — which is CS-3 arriving at the same place
  from the other direction, and the reason the two rules do not conflict here.

- **A rule can be minted against code where nobody wrote anything wrong.**
  CS-5 and CS-6 were minted against code nobody had argued for; CS-7 against a
  documented, defended decision. CS-9 is the first minted against nine authors
  who each did the locally correct thing. Every one of those helpers is good
  code in isolation, and the defect exists only in the *ninth* copy — which is
  a property no code review of any single diff can see, and therefore has to
  be a rule with a census behind it rather than a thing reviewers are asked to
  notice.

**And the counterweight was found by doing the work, not by planning it.** The
plan said "convert the sleep loops". Reading them found that a *majority* were
not waits at all: a load generator's rate, a throttled link, a sampler, an
observation window, a reconnect backoff, and — the one nobody predicted —
three **best-effort quiesces** that are themselves polled by an outer
`Eventually` and therefore must return rather than fail, because a fatal await
inside a poll aborts the retry doing the actual waiting. Converting those would
have deleted three experiments and broken two more. CS-9 ships report-only with
100% of its findings correct code, which is the highest of the four
report-only rules and the clearest statement yet of what "a locator, not a
verdict" means.
- The force-add-files-not-directories convention failed twice in one day (P0
  stage 3's .pyc, then the CS-9 landing's five). The operator caught the
  second in review; the fix is the fix this skill prescribes for everything:
  a gate (the monorepo's tools/tests/test_no_bytecode_tracked.py) plus root .gitignore
  entries, not a third restatement of the convention.
## A comment arguing for an interface is a sign the interface is arguable
`services/warden/clock.go` carried this, above three one-line methods:
> The three methods below return interfaces because `IClock` declares them that
> way: a simulated clock has to be able to hand back a simulated timer, so the
> result type belongs to the interface and not to this implementation. That is
> why CS-8 governs functions rather than methods.
Every sentence is true. It was written the same day CS-8 was minted, by
somebody who had just read the rule, and it was carried straight into
`exceptions.md` as the ruling's worked example. It survived a rule launch, a
census, a nine-site fix and a blocking-gate flip.
It did not survive the operator opening the file: *"WHY DOES CLOCK.GO RETURN
INTERFACES REEEEE"*.
**The comment was answering a question nobody had asked, which is the tell.**
Its whole argument is about *where the decision lives* — in `IClock`, not in
`RealClock` — and it is correct about that. It never asks whether `ITimer`
should exist. `ITimer` was `C() <-chan time.Time`, `Stop() bool` and
`Reset(d time.Duration) bool`: a channel and two function values, with nothing
dispatching on it and nothing able to. As a `struct` it needed no defence at
all, and the four private types that existed only to re-expose a channel all
disappeared with it.
Three things generalize:
- **A defensive comment is a code smell with a specific shape.** Not "this is
  subtle, here is why" — that is documentation. This one is *"here is why the
  rule does not apply to me"*, addressed to a reviewer rather than to a
  reader, and it is the artifact of a decision that felt like it needed
  permission. Grep for the rule's own name in a comment: a citation next to a
  violation is somebody arguing, and an argument means there is a question.
- **A rule's first exemption is written by whoever is most convinced by it**,
  and that is the worst moment to write one. CS-8 was hours old; the exemption
  and the code it exempted were authored in the same session by the same
  reader of the same rule, and the exemption was reasoned from the rule
  outward rather than from the code inward. The correction is procedural and
  it is now in `exceptions.md`: an exemption has to survive a test applied to
  the *thing being exempted* — is this abstraction data-shaped? — before it is
  reasoned about in terms of the rule.
- **The census field is what made the re-audit possible.** `interface_return_methods`
  existed only because the scope CS-8 declined had to stay visible. It said 9.
  Nine is small enough to read one by one, and reading them one by one is how
  six of them turned out not to be abstractions and a seventh turned out to be
  the factory-spelled-as-a-method the ruling had accepted as a permanent miss.
  **Publishing the number you are not enforcing is not bookkeeping; it is the
  only thing that makes the un-enforced scope auditable at all.** The rule's
  own text predicted the field would catch factories *migrating* into method
  sets. What it actually caught was factories that had been sitting there the
  whole time.

## The convention held exactly as far as its gate walked, and not one directory further
`pkg/scripts/check-test-style.sh` walked `pkg`. Measured on
2026-09-02 with that script's own regexes: **0** stdlib `TestXxx` functions
inside the walk, **129** outside it. The `ifacereturn` analyzer's suite was one
of them, and its header comment said so in as many words — *"this package is
outside it by that script's own boundary rather than by an exemption written
here"* — which is a correct reading of the gate and the reason the operator's
reaction was *"IFACE RETURN TEST IS NOT USING GINKGOGOMEGAGOMOCK HOLY FUCK"*.
**Scope IS the rule.** A walk is not a description of where a convention
applies; it is the whole of where it applies, and an author who reads it
accurately builds outside it in good faith. So a gate is widened rather than a
convention narrowed to fit the gate — and the 129 are recorded in
`exceptions.md` as an open ruling with their measured counts, not converted,
because 96 of them sit in `go/services/admin` under a `go/CLAUDE.md` rule
enforced by nothing at all, which is this skill's founding measurement occurring
a second time in the same module.
- **The exemption was also factually wrong, and nobody checked.** The header
  claimed `analysistest.Run` takes a `*testing.T`. It takes
  `analysistest.Testing`, which is `Errorf(format string, args ...any)` — one
  method, satisfied by `GinkgoT()` since the interface existed. A false
  technical premise inside a true scope argument survived review because the
  scope argument was load-bearing and the premise was not. Read the signature.

## The rule arrived while the fleet was demonstrating the violation, and the violation was ours
CS-10 — *"SERVICES SHOULD NOT REQUIRE CONTAINERIZATION SERVICES ARE CONSIDERED
CONCEPTULA AND ARE INTENDED TO GO INTO SOME BINARY VIA A FUNCTIONAL OPTION"* —
was ruled while the orchestration board was up on the GPU host, and the board
was the violation. Of the six service directories in this repository, exactly
one had its entire composition seam inside `cmd/main.go`'s flag wiring, could
not be constructed by any other binary, and shipped a compose file its README
documented first: **`go/services/foundry-board`, the board this program built to
watch itself.**

Three things follow, and only the first is about the board.

- **The instrument was the specimen.** Every other rule in this skill was
  minted against code the program was *reading* — the widget interpreter, the
  warden clock, the test tree. CS-10 was minted against the program's own
  tooling, written days earlier by the same process that writes the rules, and
  it had drifted the way everything drifts: `main.go` was where the wiring was
  easiest to put, the container was how the thing actually ran, so the container
  became the definition and nothing objected. **Tooling is not exempt from the
  rules it enforces, and it is the code least likely to be reviewed against
  them.**
- **Four of six were already compliant, and none of them had a gate.**
  `services/*` complies by construction because `AGENTS.md`
  states the `pkg`/`services`/`app` taxonomy plainly and the module was built to
  it; `go/services/{admin,candace-cloud}` were each one missing sentence away.
  That is the inverse of this skill's founding measurement, where a documented
  rule with no gate read 15 of 15 violations. The difference is not enforcement:
  it is that AGENTS.md's taxonomy is *structural* — it tells you which directory
  a file goes in — while `go/CLAUDE.md`'s "a service is a runnable binary, and
  here is the Dockerfile it needs" is a definition you can satisfy while
  violating the intent. **A rule you can obey by putting a file somewhere holds
  better than a rule you can obey by agreeing with it.**
- **The one structural failure documents a bare-binary path it cannot take.**
  `go/services/ping` tells a reader `go run main.go` in three separate files,
  first, before any container instruction — and all three are broken, because
  the code parses its templates from `/root/web/...`, a path that exists only
  because the root compose file mounts `./go` at `/root`. It is the sharpest
  single measurement CS-10 produced: **documentation-order compliance is not
  compliance.** The counterweight that says "document the bare path first"
  assumes the bare path works, and ping is the case that shows the assumption
  has to be checked rather than read.

CS-10 is also the first rule here that `check_style.py` cannot see at all, and
the first to arrive with its founding violation already repaired in the same
change. Those two facts are the same fact. A rule with no lexer has no way to
make a claim about the corpus except by fixing the corpus, so the restructure is
not a follow-up to the rule — it is the rule's only available evidence.

## The convention was already followed 709 times; the 5 exceptions were the rule

**Learned:** 2026-09-03, landing CS-11 ("test files dot-import ginkgo/v2 and
gomega").

The measurement that opened this was not a debt count but its opposite:
**709 dot-imports against 5 non-dot** — a convention followed 99.3% of the time
and enforced by nothing. That is the mirror image of this skill's founding
measurement, where a documented rule read 15 of 15 *violations*. A convention
this near-universal does not need to be argued for; it needs a gate before the
5 become 50, because new code lands at whatever the surrounding code does and
the surrounding code was already right.

The lesson is in the 5, not the 709. The tempting reading was that they were
laziness to be swept up. Every one turned out to be forced:

- **One was production code.** `pkg/eventually` wraps gomega as its await
  engine; dot-importing an assertion vocabulary — `Expect`, `Eventually`, every
  matcher — into a non-test package is the pollution the rule is *for*. The
  scope of CS-11 is the file name, and `eventually.go` is not a test. This is the
  exemption that makes the rule correct rather than mechanical: a linter that
  flagged every non-dot gomega import would flag the one file that most needs to
  keep it.
- **Four were in-package tests that could not compile with the dot** — and they
  failed for two different reasons, which is what made the exemption a rule.
  redis declares the exported type `Entry`, which ginkgo's `DescribeTable`
  helper is also named, so a dot import of ginkgo into `package redis` is
  *"Entry already declared through dot-import"* — a compile error, not a style
  nit. eventually declares `Consistently`, which gomega exports, so any in-package
  test of it redeclares `Consistently` even when it never writes
  `gomega.Consistently`.

Three things generalize:

- **A rule minted against a near-compliant corpus is minted against its own
  exceptions.** With CS-1 the work was the 61 renames; with CS-11 the work was
  understanding why 5 files were not part of the 709. The exceptions were not
  noise around the rule — they *were* the rule, because a mechanical
  dot-everything linter breaks all five and a correct rule exempts all five.
- **Two shapes of the same collision want opposite fixes.** redis's collision is
  explicit (the test writes `ginkgo.Entry`) and the package resists
  externalizing (unexported internals, an exported type whose name is the
  collision), so the qualified import is correct and stays — exempted. eventually's
  is silent (no test writes `gomega.Consistently`) but the suite had an idiomatic
  escape redis did not, so its specs moved to an external `package eventually_test`
  and the collision *disappeared*. Remove a forced qualification where you can;
  exempt it where you cannot. The gate has to see both, so its exemption is a
  package fact — a dot import redeclares when the package declares a name the
  library exports — rather than a per-file guess.
- **The detector could not have been designed before the fix-wave found the
  second shape.** A first cut exempted redis by "this file writes `ginkgo.Entry`
  and the package declares `Entry`", and it *missed* redis's own
  `cache_integration_test.go`, which has the identical collision and writes none
  of it. The collision is a property of the package, felt in a file that shows
  no sign of it — the same lesson CS-8's cross-file index taught, arriving from
  the import side.
