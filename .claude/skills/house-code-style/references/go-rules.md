# House Go rules

## Current enforcement — 2026-09-17

The [unified OCaml house checker](../../../../tools/house_lint/README.md) now
owns the CI lint verdict. Its [policy registry](../../../../tools/house_lint/policy.ml)
makes CS-1, CS-2, CS-3, CS-8, CS-11, TEST-BOOTSTRAP and DEPENDENCIES mandatory;
all other findings are advisory. Run from the repository root:

```bash
bash tools/check-house-lint.sh --test --summary house-lint-report.md
```

The single `house-lint.yml` job runs native Go AST rules, Python magic-string
checks, and invokes pinned
`dupl`, Go typed interface-return analysis and golangci-lint function length as
specialists. A failed scanner is always an error. `--report-only` makes findings
optional; `--native-only` skips the three specialists and is a partial check.

CS-4 now reports handwritten SQL literals, `Mock*` structs and unmarked generated
filenames. CS-10 now reports service process-ownership calls and service `cmd/`
and Dockerfile artifacts; Compose remains allowed. CS-12 covers `New`, `new`,
`NewStore`, `NewClient`, `NewService` and private variants. These are advisory
locators; architecture and semantic ownership still require review. Component
generator checks continue to own regeneration drift.

Additional advisory rules cover Python magic strings (`PY-MAGIC-STRING`) and
captured shared-state writes across Go goroutines (`GOROUTINE-SHARED-STATE`).
The latter recognizes bounded mutex and WaitGroup patterns; it is a review
locator, not a race-freedom proof. OCaml is excluded from magic-string linting.
The checker README defines both rules' exact scope.

The detailed sections below preserve rule rationale, counterweights, dated
measurements and descriptions of the earlier Python/shell enforcement. Their
historical commands and counts do not define current CI. Legacy Python census
and eval tools retain the old measurement semantics and do not measure the new
AST coverage; do not equate their results with the native report.

Numbered invariants, each naming its enforcement point. A rule with no
enforcement point is aspirational — see `lessons.md` for what that costs.

Measurements for CS-1 and CS-2 were taken on 2026-09-02 at `259164773` over a
585-file corpus (tracked, handwritten, first-party Go; the exclusion predicate
is `tools/check-go-reuse.sh`, restated in `../scripts/go_style_scan.py`). CS-5
and CS-6 arrived later the same day and each carries its own baseline, taken at
`3a639cba2` and `ce6ed02d2` respectively over 631 files of the same corpus, and
CS-7 later still at `5fb75ef2a` over 674 and CS-8 at `c596537d2` over 699 —
they are stamped in their
own sections rather than folded in here, because several counts from several
commits under one heading is how a reader ends up quoting the wrong one. Re-measure with `python3 ../scripts/style_census.py`
rather than trusting any of these numbers after main moves.

---

## CS-1 — Interfaces carry an `I` prefix, repo-wide, including the public API

`IStore`, `IReader`, `IIdentity`. Every interface, in every module, including
the published `candacelabs/candace` API surface. This is a deliberate
divergence from mainstream Go convention and it is the operator's call, made on
2026-09-02 and recorded as decision 2 of `docs/widget_foundry.md`. Do not
re-litigate it in a code review, and do not carve out the public module.

Unexported interfaces take the same shape with the visibility preserved:
`iStore`, not `IStore`.

**Enforcement:** `../scripts/check_style.py` (rule `CS-1`), **blocking in CI**
since slice P3 of the Widget Foundry program landed the rename on 2026-09-02.
`.github/workflows/house-lint.yml` runs
`check_style.py --strict --rule CS-1 --rule CS-2 --rule CS-8` on every pull
request and on every push to `main`, and exit 1 there fails the run. A default
run of the script is still report-only and exits 0, because it also reports
CS-5, CS-6 and CS-7, which never block.

Report-only was the posture until the rename, and it was the honest one rather
than cowardice: 61 of 61 interfaces violated the rule, and failing CI on work
nobody has been asked to do yet trains people to ignore the gate. What made the
flip available was the number reaching 0, not a decision that the rule had
become more important.

**Baseline:** 61 interface declarations, 47 exported, 0 already `I`-prefixed.
**Now:** 0 findings over 699 corpus files, 51 of 51 exported interfaces
prefixed — the census rows at `6e3ff1031` and `1f83b0420` bracket the change,
and the P3 rename entry (monorepo lab entry `2026-09-02-p3-cs1-rename`)
is what it cost.

**The P3 lockstep hazards, as measured beforehand.** The rename was not a
`sed`; three things had to move in the same commit or the tree stopped
building. All three were discharged by the rename stage, and it found three
more that this list did not have — a `-mock_names=` map, a generated file under
a leading-underscore directory that neither `go` nor Bazel can see, and a
`.proto` comment naming a Go type. The list is kept as written because the next
repo-wide rename needs the shape of it:

1. **`pkg/gotth/docs/api-surface.md`** — a hand-maintained ledger of
   the gotth public surface, in which `Effect` and `Identity` (among others)
   are marked `stable`, meaning "intended to survive to v1.0 unchanged". The
   rename contradicts that marking for every renamed row, so P3 must update the
   ledger rows *and* say in the commit message that the stability marking was
   broken deliberately by an operator ruling, not by drift.
2. **`//go:generate ... mockgen` directives that name interfaces as literal
   strings.** Measured: 13 mockgen directives across 12 files. Six of them pass
   interface names as literal arguments — 13 interface-name mentions in total —
   and every one is a string the compiler never checks, so a stale name fails
   at `go generate` time, not at build time. The worst is
   `services/warden/generate.go`, which names eight interfaces in a
   single directive (`Transport,Notifier,Store,Clock,PeerDiscoverer,ViewSource,
   IncidentLog,RPCHandler`). The other seven directives use `-source=` and
   follow a rename on their own, but their *generated* mock type names change
   (`MockStore` → `MockIStore`) and every spec that constructs one moves too.
3. **The exported-symbol churn crosses the export boundary.** This tree is
   published whole: a rename here republishes with the next snapshot.

---

## CS-2 — Every parameter in every signature is named

Function declarations, function types, and interface methods alike. Not just
interfaces; a `func(*http.Request) error` type and a `func(int, string)`
declaration are equally unreadable at the call site.

`go/CLAUDE.md` line 139 already says it for the interface half:

> Always name interface input parameters. Name return values only when that
> genuinely improves readability.

Return values keep that treatment: naming them is optional and driven by
readability, not by this rule.

**Enforcement:** `../scripts/check_style.py` (rule `CS-2`), **blocking in CI**
alongside CS-1 since slice P3 landed the named-parameter pass on 2026-09-02,
and alongside CS-8 since later the same day: the same one step of
`.github/workflows/house-lint.yml`,
`check_style.py --strict --rule CS-1 --rule CS-2 --rule CS-8`.

**Now:** 0 findings over 699 corpus files, from 374 — the census rows at
`1f83b0420` and `2c1eda90b` bracket it. Where the debt actually was is the
measurement worth carrying, and it is in `lessons.md`: 79% of it sat in func
types, not in the interfaces the rule was written about.

**The measured fact this rule exists for.** `go/CLAUDE.md` mandated named
interface parameters and nothing enforced it, so *every* interface in `go/`
violated it: **15 of 15** interface method signatures that take parameters had
none of them named (14/14 when the Widget Foundry baseline was first taken; the
ratio has never been anything but 100%). Repo-wide the same day: 36 of 100
interface method signatures with parameters were fully unnamed, and 558 of
7,774 parameters overall (7.18%) were unnamed.

Go forbids mixing named and unnamed parameters inside one list, so the
classification is per-signature, never per-parameter. A signature is either
compliant or it is not.

---

## CS-3 — Centralize a primitive at the lowest layer that owns its semantics

Do not restate the policy here; it is already written, and duplicating it is
how the two copies start disagreeing:

- **The rule:** the "Shared primitive reuse" section of the root `AGENTS.md`.
- **The gate:** `tools/check-go-reuse.sh` (pinned `dupl`, 100-token threshold
  on production source, 200-token on all handwritten code), run on every pull
  request.

Two counterweights travel with this rule and must be carried verbatim, because
CS-3 is the rule most often over-applied:

> Do not create catch-all `util`, `common`, or `core` packages.

> "Nothing qualifies" is a legitimate result.

When the gate reports a clone, centralize the primitive. Do not raise the
threshold, and document a narrowly scoped exclusion only when independent
copies are an intentional product constraint — the way `check-go-reuse.sh`
already excludes the gotth benchmarks, examples, and guide samples.

---

## CS-4 — Generate it before you write it

Before hand-writing a type, a client, a mock, a build file, or a config
projection, check whether a generator in this repo already owns that output.
**This rule is consolidation, not new policy** — every precedent below predates
it, and CS-4 exists so an agent does not have to rediscover them one at a time.

The precedents, each already load-bearing in CI or in a `go generate` chain:

| Surface | Owner |
|---|---|
| Refinement-typed contracts | `pkg/liquidproto` |
| Database access | `sqlc.yaml` (e.g. `pkg/cron/postgres/`, `services/deploy/store/`) |
| Protobuf bindings | the `generate.sh` chains (`pkg/proto/`, `services/warden/proto/`, `xetcas/proto/`, `go/proto/`, …) |
| Test doubles | the `//go:generate ... mockgen` sites |
| Bazel metadata | Gazelle, gated by `tools/check-bazel-metadata.sh` |

Two doctrine lines govern what you may then do with the output. Both are quoted
verbatim from where they already live:

> **Generated files are projections, not owners** — `AGENTS.md`

A hand edit to generated output is erased by the next regeneration, so a
finding in a generated file is a finding about its generator. This is also why
`../scripts/check_style.py` excludes every file carrying a
`// Code generated ... DO NOT EDIT.` line.

> Never unit-test checked-in generated code directly. Test the handwritten
> generator and the handwritten production boundary that consumes generated
> code, and use deterministic regeneration checks to detect output drift.
> — `go/CLAUDE.md`

`tools/check-bazel-metadata.sh` is the model for what a deterministic
regeneration check looks like: it runs both generators and fails if either
would change anything.

**Enforcement:** per-generator. `tools/check-bazel-metadata.sh` gates
the Bazel projection; the `generate.sh` chains and `sqlc` configs are run by
`go generate`. There is no single repo-wide "did you regenerate everything"
gate, and pretending otherwise would be worse than saying so: when you add a
generator, add its regeneration check with it.

---

## CS-5 — Bias against mutexes in favor of CSP

The operator's words, verbatim, 2026-09-02:

> bias against mutexes in favor of csp

Prefer communicating sequential processes: **channels, one owning goroutine per
datum, message passing**. A datum with exactly one owner needs no lock, and the
state machine that used to be spread across three goroutines becomes control
flow inside one, readable top to bottom.

The lineage is the standard library's own, and CS-5 is not a divergence from
mainstream Go the way CS-1 is:

> Do not communicate by sharing memory; instead, share memory by
> communicating. — the Go proverb, and `sync`'s own framing

**Where the line falls.** A lock is the honest primitive for a
[leaf critical section](../../../../csf/docs/generated/ontology_cgen.md#term-leaf),
and the ontology term is the test: its three conditions are defined there, not
here, and failing any one of them makes the section a protocol. Reach for a
lock there without apologizing. A leaf is still
[in-process io](../../../../csf/docs/generated/ontology_cgen.md#term-in_process):
two goroutines communicating through shared memory. Once slice X2 (#264) lands,
leaf sections use the `io/inproc` leaf primitives and raw `sync` appears only in
`io/inproc` and the runtime; until then a `sync.Mutex`/`sync.RWMutex` at the
site is correct.

What CS-5 exists to name is the other shape: **a mutex guarding a state machine
that multiple goroutines negotiate over.** When the rules start reading "the
watcher may not start a reload if the handler already did", "the timer backs
off when the state is failed", "a late result must not clobber a newer
generation" — that is a protocol, and the lock has been drafted in to stand in
for one. Give the state one owner and send it messages.

**Three counterweights travel with this rule.** They are not softeners; without
them CS-5 is a licence to make things worse, and it will be applied that way:

1. **Do not convert an honest leaf mutex into channel ceremony.** A reply
   channel per read, allocated and selected on, is a worse counter than
   `mu.Lock(); n++; mu.Unlock()`, and putting a 40k/sec read path behind one
   owner goroutine converts parallel reads into a queue. Removing a lock is not
   the goal; removing the *negotiation* is.
2. **`-race`-clean is necessary under either style and sufficient under
   neither.** Channels are not a correctness proof — a CSP design deadlocks,
   drops messages on a full buffer, and leaks goroutines in ways a mutex design
   cannot. Run `go test -race` and keep it green whichever way you wrote it.
3. **A third option exists and is often the best one.** An immutable snapshot
   published by pointer (`atomic.Pointer[T]`, copy-on-write on write) removes
   the lock *and* the message passing. It satisfies CS-5 by removing the
   sharing, not by adding a protocol — do not label it "the CSP version".

**Enforcement:** `../scripts/check_style.py` (rule `CS-5`), report-only,
alongside CS-1 and CS-2 — plus eval case `020-csp-over-mutexes` for the shape
and `021-csp-judgment` for the judgment.

The heuristic flags **every** declared `sync.Mutex`/`sync.RWMutex` — struct
fields, embedded fields, `var` specs — including the leaf ones the rule
explicitly permits. That over-reporting is deliberate and it runs opposite to
CS-1 and CS-2, which under-report on purpose. Those two detect something that
is mechanically a violation; CS-5 detects something that is mechanically a
*question*, because no lexer can tell a counter's lock from a state machine's
lock. **A CS-5 line is a locator, not a verdict.** The right response to one is
often "this mutex is correct, here is why" — and never an exclusion list, a
narrowed heuristic, or a marker comment that makes it disappear. CS-5 will
therefore not flip to `--strict` at slice P3 with CS-1 and CS-2; a rule whose
findings are legitimately dismissible cannot be a blocking gate.

**Baseline:** measured at `3a639cba2` over 631 corpus files — **107 mutex
declarations**: 91 struct fields, 16 `var` specs; 93 `sync.Mutex`, 14
`sync.RWMutex`; 48 in production source, 59 in `_test.go` files (test fakes
recording calls are the single most common shape). Zero are embedded. This
number is data about a real corpus, not a defect count to be driven to zero,
and the census row carries it (`mutex_fields`, `mutex_vars`, `mutex_rwmutex`,
`cs5_findings`) so the composition stays visible as it moves.

---

## CS-6 — Composable function values over method-set accumulation

The operator's words, verbatim, 2026-09-02, reading live P1 interpreter code:

> why couldn't this be functional options pattern

> dude stuff like this man

The code being read was `pkg/widget/internal/validate/validator_invariants.go`
line 17, on branch `foundry/p1-widget-sdk` (not this branch — do not go and
edit it; it is evidence, and P1 owns it):

```go
func (documentValidator *validator) checkInvariants() {
	documentValidator.checkStateFieldWriters()
	documentValidator.checkUnreferencedDeclarations()
	documentValidator.checkPredicateCycles()
	documentValidator.checkBindingReachability()
	documentValidator.checkSceneCardinality()
	documentValidator.checkDescriptionIsBound()
}
```

Six behaviors with one signature, accumulated as methods on a mutable struct
and wired together by a call list that only a human can enumerate.

**The rule.** When a family of behaviors shares one signature — checks, rules,
stages, handlers, migration steps — model each as a **named function value
registered in data**: a slice or map the code iterates. Not as another method on
a mutable struct, wired into a hardcoded call list.

The payoff is not aesthetic. A registry is a value, so it can be *inspected*:

- It carries metadata. A check can be registered next to the error classes it
  owns, the stage it belongs to, whether it is skippable — none of which a
  method has anywhere to put.
- **A test can assert completeness over it.** Iterate the registry, assert every
  declared error class has exactly one owner, assert no two checks claim the
  same one, assert the count matches the spec. **A method set can never be
  enumerated by a spec** — nothing in the language lets a test ask "is every
  invariant wired in?", so the answer is whatever the last person to edit the
  call list remembered.

That last point is the whole rule. The dispatch list above is correct today and
nothing will ever tell you when it stops being.

**Constructor configuration is the same principle at construction time.**
Functional options — `New(x, WithY(...))` — are function values registered in a
variadic list instead of fields set on a half-built struct. This is not a
foreign idiom being imported: `pkg/cron/options.go` already does it,
with `Option func(config *serviceConfig) error` and
`JobOption func(config *jobConfig) error`,
and `New` validating the complete option set before anything starts. Match it.

**Prefer pure function values over shared mutable accumulation.** A registered
check takes its inputs explicitly and returns its findings; the registry loop
does the appending. The dispatch-list version instead has each method reach
through the receiver for its input and call `report(...)` for its output, which
is why the family had to be methods in the first place — the shared mutable
receiver *is* the coupling. Take the input as a parameter, return the result,
and the coupling is gone along with the reason for the method set.

**Two counterweights travel with this rule**, and they are not softeners — an
unqualified CS-6 is gameable, and it will be gamed:

1. **A short fixed sequence with real ordering dependencies is honestly a
   method chain, or plain statements.** Three ordered steps that will never grow
   — parse, then validate, then emit, where step two cannot run before step one
   — do not want a registry. Iterating a slice to run three things in a fixed
   order adds an indirection, hides the ordering constraint that was previously
   visible as line order, and buys nothing, because a registry's value is
   inspection and there is nothing worth inspecting about three. The test is not
   "how many calls" but **"is this a family, or a sequence?"** — a family grows
   and its members are interchangeable; a sequence has an order that means
   something.
2. **CS-2 applies inside every function type and every registry entry.**
   `type Check func(*Document) []Finding` is a CS-2 violation dressed as a CS-6
   compliance; it is `type Check func(document *Document) []Finding`. Converting
   a method set into function values *multiplies* the number of func types in
   the tree, so this is the rule most likely to introduce CS-2 debt while
   satisfying another rule.

**Enforcement:** `../scripts/check_style.py` (rule `CS-6`), report-only,
alongside CS-1, CS-2 and CS-5 — plus eval case `022-composable-checks` for the
shape and `023-composability-judgment` for the judgment.

The heuristic flags one shape only: **a method whose body consists solely of
three or more argument-less calls on its own receiver.** One statement that is
anything else — an argument, a `defer`, a `return`, an `if`, a call through a
field — disqualifies the whole body and the method is not reported. That is
deliberately narrow, and it runs opposite to CS-5's deliberate over-reporting:
CS-5 flags every mutex and lets a human dismiss most of them, while CS-6 stays
quiet unless it is looking at the exact shape the founding evidence had.

Narrow was chosen over broad because the alternative was unreliable rather than
merely noisy. A broader detector — "a method that mostly calls its own methods",
"a method with a high sibling-call ratio" — has no defensible threshold, would
fire on ordinary composition all over the tree, and **a gate that misfires
teaches authors to dodge the gate**, which is a worse outcome than a gate that
misses. What shipped is a locator for one unambiguous shape.

CS-6 still cannot become a blocking gate, for counterweight 1's reason: the
detector cannot tell a family from a sequence, and a legitimate three-step
ordered chain fires it. **A CS-6 line is a locator, not a verdict**, exactly as
a CS-5 line is. It does not flip to `--strict` at slice P3 with CS-1 and CS-2.

**Baseline:** measured at `ce6ed02d2` over 631 corpus files — **0 findings**,
recorded in the census row of that commit.
Zero is the honest reading and not a broken detector, and it was verified in
both directions before the number was recorded:

- **Positive:** run against the founding evidence
  (`pkg/widget/internal/validate/validator_invariants.go` on
  `foundry/p1-widget-sdk`), the heuristic reports exactly one finding naming all
  six sibling calls. The shape it is named after, it detects.
- **Negative:** lowering the threshold from three to **one** still reports 0
  over the same 631 files. No method in this corpus has a body made only of
  argument-less receiver calls, at any length. The shape is genuinely absent
  here; it is not being filtered out by the threshold.

So CS-6 is the first rule to launch with a clean corpus, which changes what its
number means. CS-1's 61 and CS-5's 107 are baselines to watch move. CS-6's 0 is
a **tripwire**: it exists to catch the shape entering the tree, and the census
row (`dispatch_methods`, `dispatch_calls_total`, `dispatch_calls_max`,
`cs6_findings`) is what makes the first arrival visible.

**The tripwire has since fired.** At `5fb75ef2a` on
`feat/widget-foundry`, which merged the P1 interpreter, CS-6 reports **2**
findings: `pkg/widget/internal/validate/validator_invariants.go:17` — the six-way
dispatch list that founded the rule, now inside the measured corpus — and
`pkg/widget/internal/validate/validator_build.go:24`, a **15**-call dispatch list
that did not exist when the rule was written. That second one is what
`dispatch_calls_max` was added for. Neither is fixed here; they belong to P1,
and this is the report, not the repair.


---

## CS-7 — Generics at the boundary, erasure in one place

The operator's words, verbatim, 2026-09-02, reading the widget SDK's `State`
contract:

> are you guys an idiot we literally have generics

The code being read was `pkg/widget/sdk.go` line 24, on branch
`feat/widget-foundry`:

```go
type State any
```

and the interface at lines 120–134 that threads it through every phase:

```go
type IWidget interface {
	Register() Registration
	Mount(ctx context.Context, session live.Session) (State, []live.Effect, error)
	Reduce(state State, event live.Event) (State, []live.Effect)
	...
}
```

**Start with what the SDK got right**, because the rule is not what a first
reading suggests. `sdk.go`'s own comment argues:

> A registry holds widgets of different shapes in one sequence, and Go has no
> way to say "a slice of values each of a different type known to its own
> owner" — so the host carries the value and the widget asserts it back on the
> way in.

That analysis is **correct**. Go has no existential types. A `Registry` holding
`[]IWidget[S]` for a different `S` per element cannot be spelled, and any design
that stores differently-parameterized values in one collection erases somewhere.
Nothing in CS-7 disputes it, and a response that "fixes" the erasure by
pretending Go can type a heterogeneous registry is wrong on the language.

**The defect is that the erasure was made the public contract instead of an
internal detail.** The analysis proved that erasure must exist *somewhere*; it
was then applied at the widest possible surface — the exported interface every
widget author implements and every generated widget satisfies — when the same
argument permits it at exactly one unexported adapter inside the package that
owns the collection.

**The rule.**

1. **A public contract never asks its author to hold `any` where a type
   parameter can carry the type.** If the value's type is known to whoever
   writes the code on both sides of the call, the signature says so.
2. **Where heterogeneity genuinely forces erasure, erase exactly once**, inside
   the library that owns the collection, behind a generic shell:

   ```go
   type IWidget[S any] interface {
       Mount(ctx context.Context, session live.Session) (S, []live.Effect, error)
       Reduce(state S, event live.Event) (S, []live.Effect)
       ...
   }

   // Register is the generic shell. It is the only place a widget's state type
   // is forgotten, and the adapter it builds is the only place one is asserted.
   func Register[S any](registry *Registry, widget IWidget[S]) error {
       return registry.add(&erasedWidget[S]{widget: widget})
   }
   ```

   `erasedWidget[S]` is unexported, implements the untyped interface the
   registry stores, and holds every assertion in the package. **The assertion
   lives at that one audited site.** Author-facing code and generated
   per-instance code never see `any` and never assert.
3. What that buys is not aesthetics. An assertion at one reviewed site is a
   fact about the library; an assertion in every generated widget is a fact
   about every widget, and the compiler stops checking the thing the design
   depended on.

**Three counterweights travel with this rule**, and as with CS-5 and CS-6 they
are not softeners — an unqualified CS-7 is a licence to sprinkle type parameters
across a codebase that did not ask for them:

1. **Do not generify internals no author touches, and a type parameter that
   only ever binds one type is ceremony.** CS-7 is about *boundaries*: the
   signature someone outside the package has to satisfy. An unexported helper
   whose only caller is three lines up gains nothing from `[T any]`, and a
   `Store[T]` instantiated exactly once as `Store[Session]` is a rename of
   `SessionStore` with extra syntax. The test is whether a second caller with a
   different type exists or is coming.
2. **Code that genuinely operates on unknown shapes stays `any`, and says why
   in a comment.** An `encoding/json` envelope, reflection-driven plumbing, a
   value being hashed or formatted rather than used, and a third-party
   signature being mirrored are all real. A type parameter there would be a
   parameter nobody can instantiate. The comment is the requirement: an `any`
   with a reason is a decision, an `any` without one is a default.
3. **CS-2 applies inside every generic signature.** `Register[S any](*Registry,
   IWidget[S]) error` is a CS-2 violation wearing CS-7 compliance; it is
   `Register[S any](registry *Registry, widget IWidget[S]) error`. Adding type
   parameters rewrites signatures wholesale, which makes this the second rule —
   after CS-6 — whose compliance work is a common way to introduce CS-2 debt.

**Enforcement:** `../scripts/check_style.py` (rule `CS-7`), report-only,
alongside CS-1, CS-2, CS-5 and CS-6 — plus eval case
`024-generics-at-the-boundary` for the shape and `025-erasure-judgment` for the
judgment.

The heuristic reports an **exported** interface method parameter or result, or
an **exported** struct field, whose type is bare `any`, in a **non-internal**
package. Three restrictions, each carrying the rule's own scope rather than
trimming noise:

- **Non-internal only.** CS-7 governs a public contract. Go's `internal/` rule
  already makes such a package unreachable from outside its subtree, and
  erasing inside a library is precisely what the rule asks for. This is not a
  hypothetical distinction here: `pkg/gotth/internal/session/types.go`
  carries `App.Init`/`Reduce`/`Teardown` in exactly the `any`-threaded shape
  IWidget has, and CS-7 deliberately says nothing about it, because that
  erasure is already where the rule wants it.
- **Bare only.** `[]any`, `map[string]any`, `*any` and a variadic `...any` are
  not reported. A container of dynamic elements is not a contract erasing one
  value's own type.
- **Package-local `any` aliases are resolved.** `type State any` makes `State`
  erased for the rest of that file. Without this the detector reports 3
  findings, **all three of them legitimate**, and misses the founding evidence
  entirely — the rule's own defect is spelled `State`, not `any`.

**A CS-7 line is a locator, not a verdict**, exactly as a CS-5 and a CS-6 line
are, and for the reason counterweight 2 gives: no lexer can tell a JSON
envelope from an erased contract. It does not flip to `--strict` at slice P3.

**Baseline:** measured at `5fb75ef2a` over 674 corpus files — **9
findings across 4 exported types**, and 1 package-local `any` alias. The
composition is the number that matters, and it was reviewed finding by finding
rather than reported as a total:

| Site | Verdict |
|---|---|
| `pkg/widget/sdk.go` (6 findings, `IWidget` × 5 methods) | **The defect.** The founding evidence. |
| `pkg/liquidproto/error.go:19` — `Error.Value any` | **Legitimate.** A refinement error holds the rejected value in its base Go type; `Error[V any]` would break `errors.As`, which needs one concrete type. |
| `services/deploy/operator/approvals.go:80` — `ApprovalRequest.Payload any` | **Legitimate.** Arbitrary tool arguments, hashed and never stored. Genuinely unknown shape. |
| `go/pkg/foundation/storage/redis/cache.go:41` — `Redis.Set` | **Legitimate.** The interface mirrors `*goredis.Client`, asserted at compile time two lines below. The `interface{}` is the third party's, and a type parameter there would break the assertion. |

So **6 of 9 findings are the defect and 3 are erasure that stays** — a rate that
made the heuristic worth shipping rather than judging by hand. The three
legitimate sites are not exclusions and never become any: they are answered, the
way a CS-5 leaf mutex is answered. Two of them do satisfy counterweight 2 with an
existing comment; `ApprovalRequest.Payload` explains itself in the type's doc
comment rather than at the field, which is the weakest of the three and is
reported here rather than fixed, since nothing in this task owns that file.

---

### Amendment (operator, 2026-10-01): a generic API instantiated with any

Operator, reading `relay.NewMessenger[any]`, `Envelope[any]` and an MCP input
field `Body any` in agent messaging, verbatim: "why do we need any if we have
parameterizable types". A generic type exists to carry a type; instantiating it
with `any` at a public boundary erases exactly what it carries, which is the
CS-7 defect with extra steps. Declare the contract (CS-4: generate it first)
and instantiate the generic with it. Agent messaging now does: the relay is
`Relay[Body]`, one relay carries one contract, and CSF's carries
`*agentv1.AgentMessage` from `proto/candace/agent/v1/agent.proto`.

The CS-7 locator reports this shape too: an exported struct field, an exported
interface method's parameter or result, or an exported function's parameter or
result whose type is a generic instantiation with `any` or `interface{}` as a
type argument. A type parameter constrained by `any` (`[Body any]`) is a
constraint, not an erasure, and is not reported.

## CS-8 — Return concrete implementations; only accept interfaces

The operator's words, verbatim, 2026-09-02:

> RETURN CONCRETE IMPLEMENTATIONS ONLY ACCEPT INTERFACES

**The rule.** A function or method never *returns* an interface type.
Interfaces live in two places and no third:

1. **Parameter position** — a function accepts an interface, because a caller
   with any implementation can then call it. This half of the ruling is
   permissive and is not gated; nothing in CS-8 asks you to narrow a parameter.
2. **A variable declaration at the consuming seam** — `var store IStore` in the
   code that is about to use one, or a struct field the consumer holds. That is
   where the abstraction is needed and where it is visible.

A function returning an interface takes a fact away from its caller — the
concrete type — that the function itself knows and the caller often needs, and
the caller cannot get it back except by an assertion the compiler stops
checking.

**Where runtime polymorphism selects among implementations, the selection moves
into the consumer.** The shape CS-8 is minted against is the config-selected
factory: a `switch` over a mode string, returning a different implementation per
arm. The fix is not to delete the switch. It is to move it to where the
interface variable is declared — declare the variable at the seam that *accepts*
the interface, and assign concrete constructors into it in the arms:

```go
// Before: the factory chooses, and hands back an interface.
func buildDiscoverer(cfg config.Config) warden.IPeerDiscoverer {
	switch cfg.Discovery.Mode {
	case config.DiscoveryModeTailscale:
		return discovery.NewTailscale(...)
	case config.DiscoveryModeFile:
		return discovery.NewFile(...)
	default: // "static"
		return nil
	}
}

// After: the consumer declares the interface and the switch assigns concretes.
var discoverer warden.IPeerDiscoverer
switch cfg.Discovery.Mode {
case config.DiscoveryModeTailscale:
	discoverer = discovery.NewTailscale(...)
case config.DiscoveryModeFile:
	discoverer = discovery.NewFile(...)
default: // "static": no discoverer, and the variable stays untyped nil.
}
```

Each constructor still returns its own concrete type; the one place that erases
them into a common interface is the assignment, which is the seam, which is
where the reader is already looking.

**The untyped-nil seam is preserved by leaving the variable nil, never by
returning nil through an interface-returning factory.** This is the trap, and it
is the reason CS-8 is worth a gate rather than a preference:

> A concrete nil pointer wrapped in an interface is **not** a nil interface.
> `var p *Tailscale = nil; var d IPeerDiscoverer = p; d != nil` is **true**.

So a factory whose signature is `IPeerDiscoverer` and whose "no implementation"
arm returns a concrete typed nil silently hands the consumer a non-nil interface
holding a nil pointer, and every `if discoverer != nil` downstream takes the
wrong branch. The worked example is warden's static fleet: a nil `Discoverer`
selects the election manager's static semantics — membership mirrors the config
peer list exactly and persisted membership is ignored — and a typed nil there
flips a static fleet into dynamic semantics, where a persisted roster overrides
config edits. Nothing fails loudly; the fleet just stops obeying its own config
file. With the switch in the consumer the arm assigns nothing at all, and
"nothing" is the one value that cannot be a typed nil.

The same trap is what makes the naive reading of CS-8 dangerous. "Return the
concrete type" applied to a factory whose real job is *selection* produces
`func buildDiscoverer(cfg config.Config) *discovery.Tailscale`, which is both
wrong (it cannot express the other modes) and, on the static arm, the typed-nil
bug itself. Move the selection; do not narrow the return type in place.

**One structural exemption, and it is stated once: `error`.** Go's own contract,
implemented by everything, matched on by `errors.Is` and `errors.As`, and
returned by roughly every function in this tree. It is conveniently not
`I`-prefixed — CS-1's house rule does not reach the standard library — so the
heuristic never sees it and no exclusion list is needed to keep it.

**Exempt by construction, because the callee has no concrete type to return:**
a result carried by a **type parameter the caller instantiated**. Both shapes
are in this corpus:

- `live.zeroInit[S any](ctx, session) (S, []Effect, error)` returns a bare `S`.
  That is the caller's type, not an interface the callee chose, and the
  heuristic never sees it because `S` is not an interface name.
- `widget.LookupWidget[S any](registry, name) (IWidget[S], bool)` returns an
  *instantiated* interface. CS-7 required that shell to erase the widget's state
  type at one audited adapter, so by the time `LookupWidget` runs there is no
  concrete type left to hand back — the erasure is the design. The detector
  reports only an un-instantiated name, so `IWidget[S]` is out of scope.

**One counterweight travels with this rule, and it is a scope statement rather
than a softener: a pass-through of somebody else's contract is not an erasure
choice.** Ruled by the orchestrator, 2026-09-02, under the operator's mandate.

When a function returns an interface **because an upstream or THIRD-PARTY API
demands that shape at that position** — `templ.Component`, `http.Handler`, a
library's own `Msg` — it is handing back the contract it was given. There was
never a concrete type available for it to return instead, so there is nothing
for CS-8 to relocate. This is the same class as the `error` exemption and as
CS-7's counterweight about a mirrored third-party signature.

> **Third-party ONLY.** Amended 2026-09-03 by operator ruling, because the
> exemption had quietly grown a second meaning. A repo-owned FRAMEWORK's
> contract is this repository's choice and is in scope — every one of its
> signatures is one somebody here wrote and can change today. "The library
> requires this shape" is an exemption when the library belongs to somebody
> else and an admission when it belongs to us. The site that forced the
> amendment is in this repository's own UI framework:
>
> ```go
> func retryWatch(ev live.Event) []live.IEffect { // holy shit i hate you
> ```
>
> `live` is ours. `live.IEffect` is gone, `live.Effect` is a struct, and
> `Config.Execute` — the hook that existed to type-switch on the interface —
> went with it. See monorepo lab entry `2026-09-03-effects-concrete`.

**CS-8 reaches interface types this repository owns and could have made
concrete.** That is the whole test, and it is worth stating as a question:
*could this function have returned a concrete type?* A config-selected factory
could (three constructors already do). A function whose result type is
`templ.Component` because `IWidget.Render` is declared to return one could not.

**What the gate does about it, measured rather than promised.** The
declared-interface cross-check already *is* the third-party filter, and it is a
better one than a package-qualifier rule would be, because it needs no import
resolution: a result type is reported only when the corpus declares an
interface of that name. `Component`, `Handler` and `Msg` are not declared here,
so `templ.Component` is silent whether it is returned bare or in a slice. The
two sites the ruling names are each out of scope twice over:

| Site | Why the gate is silent |
|---|---|
| `hosting.Regions() []templ.Component` | The corpus declares no interface named `Component`, so it is third-party and out of scope. `[]T` stopped being a reason on 2026-09-03; this row survives on the first half alone. |
| `widgettest.Card.Apply(...) []live.Effect` | It is a **method**, so its result is fixed by the interface it satisfies — and there is no interface left in it to report. |

**Amendment, 2026-09-03: a slice of interfaces is a slice of interfaces.** The
detector used to report only a BARE result, and `[]IEffect`,
`map[string]IThing` and a variadic `...IThing` were read as "a container, not
this function handing back one implementation". That reading is what let a
repo-owned framework's reducer contract sit at 0 findings for a month. A caller
that receives a slice of interfaces receives interfaces. Slices, arrays, maps
and variadics of an I-prefixed declared interface are **in scope**; a pointer
(`*IStore`) and a map KEY still are not, and both stay out for the usual
under-reporting reason rather than because they are correct.

Measured across the amendment, by running the tools rather than by estimate:
the extended detector found **4** CS-8 findings on the pre-sweep tree — one
each in `live/app.go`, `live/livetest/replay.go`, `widget/registry.go` and
`foundry-board`'s `retryWatch` — with 25 further composite-result sites newly
visible as measurements (exempt or method-position). After the sweep, **0**.

**Amendment, 2026-09-03: a struct that carries interfaces returns them.**
Operator ruling: *"returning a struct that carries interface-typed fields is
returning those interfaces; the wide lane sees through the wrapper; inward-
flowing config structs are the accept side and are fine"*. Two lanes report it
and **neither blocks**:

- the text lane, `check_style.py --fields`, lists every struct field whose type
  carries a declared interface, and the census carries the count as
  `interface_fields` (**83** on landing, **73** after the identity change). It is deliberately not a `--rule`
  value and can never reach the blocking command.
- the typed lane, `bash tools/check-ifacereturn.sh`, walks a RESULT's struct fields
  transitively and names the path (`NewView returns View.Store, which is the
  interface subject.IStore`). It is direction-aware by construction: only
  results are walked, so a parameter is unreachable from it. Measured across
  the six first-party modules: **407 → 1,956**.

The accept side dominates both counts and that is correct rather than a defect.
A config struct holding an `IStore` a constructor was handed is the pattern
CS-8 asks for; what the lanes are for is the other direction, and only the
typed lane can tell them apart. **No fix wave followed the measurement**: the
numbers are published and the judgments come later.

**Why a lexer may do this at all, which is the finding underneath the
amendment.** Operator ruling, 2026-09-03, on naming as instrumentation: the
text lane can recognise an interface from its NAME, with no type checker, only
because CS-1 reads 0 — every interface in this corpus is `I`-prefixed. A rule
minted for readability bought a stdlib-only Python script the ability to answer
a question `go/types` would otherwise be required for. That is the strongest
argument the `I` prefix has, and it was not the argument it was adopted on.

**What the gate cannot tell, stated plainly.** Two residual cases, neither
present in the corpus today:

1. A third-party interface whose name collides with one this repository
   declares. Package qualifiers are dropped, so `otherlib.IStore` would be
   reported if we declared an `IStore` of our own. Measured: no such collision
   exists (`ID` is the corpus's only I-prefixed name that is not an interface,
   and nothing declares an interface by it).
2. A receiverless helper returning a bare repo-owned interface purely to feed an
   upstream contract — the pass-through shape without a method or a slice to
   make it visible.

For both, **the counterweight is the manual answer and the finding is answered
in the review, not silenced.** No marker comment, no exclusion list, no
narrowed heuristic — the reasons `lessons.md` and `../evals/README.md` reject
those for CS-5 apply unchanged here. If case 1 ever arrives, the fix is to
report it and rule on it, not to widen the detector's blind spot.

**The exemption is escapable, and the escape is the doctrine.** Amended
2026-09-02, from the operator reading `clock.go`: *"WHY DOES CLOCK.GO RETURN
INTERFACES REEEEE"*.

Every exemption above says the same thing — *this position's type is not the
declaration's to choose*. The method exemption says it about `IClock`: a
simulated clock has to hand back a simulated timer, so the result type belongs
to the interface. That was true, and it answered the wrong question. The prior
question is **whether the returned interface is an abstraction at all**, and
`ITimer` was not one. It declared `C() <-chan time.Time`, `Stop() bool` and
`Reset(d time.Duration) bool`: a channel and two function values. Nothing
dispatched on it, nothing could — a timer holds no state of its own — and the
two implementations differed only in which channel they closed over.

So, before recording any exemption, apply **the data-shaped test**:

> Is the returned abstraction data-shaped — channels, function values and
> plain fields, with no behaviour of its own? Then it is not an abstraction.
> Make it a concrete struct, and the exemption evaporates because there is no
> longer an interface to exempt.

`ITimer` and `ITicker` became `warden.Timer{C; Stop func() bool; Reset func(d
time.Duration) bool}` and `warden.Ticker{C; Stop func()}`. `RealClock` fills
them from `time.Timer`'s and `time.Ticker`'s own method values; `testclock`
fills them from its waiter table and closures over the waiter id. Four types
that existed only to re-expose a channel are gone, `C` reads as a field
everywhere, and CS-6 is the reason this works rather than a coincidence: a
struct of function values *is* the composable form, and the "swappable
implementation" the interface was modelling is just which closures got stored.

**The exemption survives only for genuinely behavioural returns**, and what
survives is recorded per site in `exceptions.md` rather than assumed. The
re-audit of every method-position case in the corpus took
`interface_return_methods` from **9 to 2**: six were the clock, one
(`cluster.route`) was a factory spelled as a method — the evasion the
"no receiver" scope leaves open, found by looking rather than by the gate —
and two are behavioural and stay.

**Enforcement, in two lanes.** Operator directive, 2026-09-02: *"i want a ci
lint check that checks to see if there are any interface return types and flags
them"* — **any**, which is wider than the gate above and is meant to be. The
two lanes are not a staging sequence; both are permanent, and they answer
different questions.

| Lane | What runs | Scope | Verdict |
|---|---|---|---|
| **blocking-narrow** | `../scripts/check_style.py --strict --rule CS-1 --rule CS-2 --rule CS-8`, in `.github/workflows/house-lint.yml` on every pull request and every push to `main` | lexical; the five restrictions above | **fails the build**, because every finding in scope has a fix |
| **flagging-exact** | `tools/check-ifacereturn.sh`, running the `go/analysis` analyzer at `tools/ifacereturn` over every first-party module | type-aware; **no** restrictions, `error` the only exemption | **never fails the build** (`continue-on-error`); prints every finding plus a by-interface tally in the step summary |

The wide lane is type-aware because `go/types` answers *"is this an
interface"* exactly where a lexer can only guess: it sees stdlib and
third-party interfaces the corpus never declares, `any` however it is spelled,
and method position. It therefore reports code this rule has already ruled
correct — sealed sums, hook implementations, library pass-throughs, generated
protobuf and `templ` output, and the two method-position returns that survived
the data-shaped re-audit. **That is the lane's job, not its defect.** Keeping
the ruled cases visible is why it exists, which is also why it has no
exclusion list and no marker comment, and why it flags instead of blocking: a
gate whose findings have no fix is one people learn to route around.

One correctness note, because it is not obvious: `types.IsInterface` reports
**true for a type parameter**, whose underlying type is its constraint. The
analyzer excludes type parameters explicitly, or `func f[S any]() S` — the
caller's own type, the shape CS-7's counterweight already exempts — would be
reported for every generic function in the tree.

Measured on landing: **407** interface-typed results across six modules (348
`candace`, 35 `go`, 11 `github-runner/autoscaler`, 8
`go/examples/liquid-openapi-sqlc`, 5 `go/services/candace-cloud`, 0
`go/benchmarks/gc`). Over half are generated protobuf and `templ` output; the
tally makes that legible without an exclusion list. **No census field carries
this number**, deliberately: `../scripts/style_census.py` is stdlib-only
Python that runs on hosts with no Go and no Docker, and a field that needed a
toolchain would make the census unrunnable exactly where it is most often run.
The number lives in the CI step summary and in the lab entry.

Both lanes are exercised by eval case `026-return-concrete` for the shape and
`027-factory-judgment` for the judgment, and the analyzer has its own
`analysistest` suite covering repo-owned, stdlib and third-party interfaces,
the `error` exemption, concrete and generic silence, and both method and
function position.

CS-8 is the first rule since CS-2 headed for a blocking gate, and the first of
the four minted from a live operator reaction to get there. CS-5, CS-6 and CS-7
all detect a *question* — is this mutex a leaf, is this list a sequence, is this
`any` an envelope — and a question is not gate-shaped; each of those three is a
locator, not a verdict, and none of them ever blocks. CS-8 detects a
**violation**: a receiverless function whose declared result is a declared
interface is one, on sight, and the five restrictions below are the boundary of
that shape rather than softeners of it.

It shipped report-only anyway, for exactly one commit, because the corpus read
9 and `exceptions.md` has the standing reason: a gate that fails a pull request
for work nobody has been asked to do is a gate people learn to ignore. The flip
to `--rule CS-8` landed in the commit *after* those 9 were fixed and the count
read 0. **A gate is never turned red.**

**What the detector reads, and the five restrictions that are its scope.** Each
one removes a class where the result type is *not the declaration's to choose*,
which is the same test the rule itself applies — and each was forced by a real
site rather than added to quiet the number:

| Restriction | Why, and what it costs |
|---|---|
| **No receiver** | A method's result type is fixed by the interface it satisfies; changing it stops the type satisfying that interface, so the decision lives in the interface — *if the interface deserves to exist*, which is what the data-shaped test above now asks first. Measured: 9 method-position interface results at the baseline, **2 after the 2026-09-02 re-audit** deleted `ITimer`/`ITicker` and narrowed `cluster.route`. **Cost: a factory spelled as a method is missed** — `cluster.route` was exactly that, and it was found by reading rather than by the gate, which is why the flagging lane below now reports method position too. |
| **Composite results are IN scope since 2026-09-03** | `[]IStore`, `[N]IStore`, `map[string]IStore` and a variadic `...IStore` are reported: a caller that receives a slice of interfaces receives interfaces. Still out: `*IStore` (a pointer to an interface is a different mistake), a map KEY, a func value's own signature, and the instantiated `IWidget[S]` — the type argument is the caller's, and a shell that erased the concrete type on the way in by design (CS-7) has none to return. |
| **A declared interface name only** | `^[Ii][A-Z]` alone reads `session.ID` — `type ID [16]byte` — as an interface: 4 false positives. The detector cross-checks the base name against every interface the corpus declares, which removes all four. Package qualifiers are dropped (imports are not resolved), so two packages sharing one name where only one is an interface would false positive; measured, `ID` is the only I-prefixed non-interface name in the corpus and nothing declares an interface by that name. |
| **A sealed sum type is not an abstraction** | An **exported** interface with an **unexported** method cannot be implemented outside its package, so its variants are closed and the package owns all of them. That is how Go spells a tagged union — `protocol.IInbound`'s `isInbound()` — and `ParseInbound` returning one returns a union whose variant is a property of the input bytes, not a choice it made. *Exported* is load-bearing: an unexported interface with unexported methods is an ordinary package-local abstraction, and the corpus contains one whose factory is a CS-8 violation of the purest kind (`iWorkloadRunner`). |
| **A signature a contract already fixes** | A function whose parameter and result types match a `func` type declared anywhere in the corpus is an implementation of that hook, not a factory: `live.Anonymous(request *http.Request) (IIdentity, error)` exists to be assigned to `Config.Authenticate func(request *http.Request) (IIdentity, error)`, and it cannot narrow its result without ceasing to fit. The interface there is the *field's* — a variable declaration at the consuming seam, which is exactly where CS-8 puts one. |

**Baseline:** measured at `c596537d2` over 699 corpus files — **13 receiverless
interface-returning functions, 4 of them exempt, so 9 findings**, plus 9
method-position results the rule does not enforce. The census carries all four
numbers (`interface_return_funcs`, `interface_return_exempt`,
`interface_return_methods`, `cs8_findings`) so the scope CS-8 declines stays
visible: a corpus where `interface_return_methods` climbs while `cs8_findings`
holds at 0 is one where factories moved into method sets. The field paid for
itself on 2026-09-02 — it is what made "9 method-position results" a number
somebody could re-audit, which is how six of them turned out not to be
abstractions at all.

**Now:** 0 over the same 699-file corpus. All 9 were fixed in one commit, and
the worklist is small enough to state in full:

| Site | What it was, and what it became |
|---|---|
| `app/warden/cmd/main.go` — `buildDiscoverer` | The founding site. The switch moved into `run`, which declares `var discoverer warden.IPeerDiscoverer` and assigns `discovery.NewTailscale(...)` / `discovery.NewFile(...)` — both of which already returned their own concrete types. The static arm assigns nothing. |
| `app/warden/cmd/main.go` — `buildNotifier` | The same, one seam over: the watchdog's `warden.INotifier`, three concrete constructors, and a default arm that refuses to start instead of returning an error nobody could act on differently. |
| `go/benchmarks/gc/cmd/gcbench/main.go` — `newWorkload` | The founding shape a second time. Became a **registry** (CS-6): `workloads` maps a name to the concrete constructor and the shape it reports, `main` looks up and assigns into one `var runner iWorkloadRunner`, `validateConfig` derives its accepted names from the same table, and the test enumerates it — which the old switch made impossible. |
| `services/warden/clock.go` — `NewRealClock` | `realClock` became the exported `RealClock` and the constructor returns it. Its `NewTimer`/`NewTicker` returned `ITimer`/`ITicker` for one day, as the worked example of why CS-8 governs functions and not methods — until the operator read the file and the data-shaped test above deleted both interfaces. They now return the concrete `Timer`/`Ticker` structs, and the worked example is the amendment instead. |
| `go/pkg/messaging/consumer.go` — `NewJetStreamPullSource` | `jetStreamPullSource` became the exported `JetStreamPullSource`; `Consumer` declares the `IPullSource` it needs. |
| 4 warden test doubles — `mockView` ×2, `mockIncidents`, `stubTransport` | Each built one gomock double and widened it on the way out. Each now returns the `*mocks.MockI…` its generated constructor already produced: mockgen's own contract is concrete, and these were undoing it. |

**The static-nil semantics were proven preserved, not asserted.**
`services/warden/election/ledger_test.go`'s "ignores a persisted Membership in
static mode" — the spec that pins a nil `Discoverer` selecting static
membership — was run green before the change and after it. A second spec was
added beside it, because the first one's meaning depends on a fact nothing
tested: **"treats a typed-nil discoverer as discovery mode, not as static"**
constructs a manager with a nil `*fakeDiscoverer` in the interface and asserts
it adopts the persisted roster, ghost voter and all. Writing it turned up one
more thing worth carrying:

> Gomega's `BeNil()` reports a typed nil as **nil**. `cfg.Discoverer != nil` —
> the expression `election.NewManager` actually evaluates — reports it as
> **not nil**.

So a spec written the idiomatic way, `Expect(cfg.Discoverer).To(BeNil())`,
would have agreed with the wrong one and passed while the fleet went dynamic.
The spec is spelled as the language spells it, with that reason in a comment.

---

## CS-9 — Tests never hand-roll time

The operator's words, verbatim, 2026-09-02, reading the test tree:

> we keep rewriting the same await test helper function holy shit

**The measurement behind it.** Four named await helpers, each private to its
own file and each a re-derivation of the same loop:

| Helper | Where |
|---|---|
| `awaitRaft` | `examples/widget/live_test.go` |
| `awaitReload` | `pkg/gotth/test/internal/conformance/dev_reload_test.go` |
| `waitUntil` | `services/warden/watchdog/run_test.go` |
| `awaitOutput` | `app/warden/e2e/cli_contract_test.go` |

plus **five more inside one file** — `awaitConsistentLeader`,
`awaitPeerStatus`, `awaitAllAlive`, `awaitAlert`, `awaitAlertAnywhere` in
`app/warden/e2e/e2e_test.go`, each spelling out a deadline, a poll, a
`dumpLogs()` and a `Fatalf` — and two more of the same shape written inline in
that file's specs. Against **89 test files that already called gomega's
`Eventually`**. Nobody was missing a library. What was missing was a *typed*
way to call it, so nine authors each wrote a private one.

### The rule

1. **A wait for a condition is `pkg/eventually`.**
   `eventually.Await[Value](reporter, what, budget, poll, match)` returns the
   value that satisfied `match`. `eventually.Consistently[Value]` is the same for
   an absence.
2. **A hand-rolled helper wrapping a timing loop is deleted where the loop was
   all it had, and reimplemented on the primitive where it carries real domain
   logic.** `awaitReload`'s "a read that lands inside the reload is the event,
   not a failure" is domain logic and survives; the `for` around it was not.
3. **A budget is named and generous** (counterweight 3).

**Why not just "call `Eventually`".** Because that trades one debt for another,
and the operator named the trade while the rule was being written: *"why can't
we use parameterized types, we literally have generics"*. `Eventually` is a
pre-generics reflection API — it takes `any`, matches through `reflect`, and
asks every call site to hold an untyped value. Prescribing it everywhere is
CS-7's founding defect applied 89 times. So CS-9's answer is CS-7's answer:
**the erasure is real and stays, and it happens exactly once, inside the
package that owns it.** `eventually` is that package. Its signature is typed, its
engine is gomega, and no caller sees `any`.

The typed shell pays for itself in failure messages, which is the part a reader
meets on a bad afternoon:

```go
Eventually(func() bool { return len(rec.Sent()) == 1 }).Should(BeTrue())
// Expected <bool>: false to be true
```

```go
eventually.Await(GinkgoTB(), "the dead peer's one notification", schedulerBudget,
    rec.Sent, func(sent []warden.Incident) bool { return len(sent) == 1 })
// ...the last value poll returned: [{PeerDead node-a ...} {PeerDead node-b ...}]
```

The first says a boolean was false. The second says two notifications arrived
where one was expected, and names them.

### Four counterweights travel with this rule

They are not softeners. CS-9 unqualified is a licence to delete experiments,
and it will be applied that way — a *majority* of what its gate reports is
correct code.

1. **A sleep that generates load or paces something is the subject of the test,
   not a wait for one, and it stays.** A load generator's rate
   (`case5_flood_test.go`), a link throttled to N bytes per second
   (`relay_test.go` — the slow client *is* the sleep), a sampler running a
   fixed window at a fixed resolution (`reverify_test.go`), an observation
   window a measurement is per-unit-of (`measure_test.go`), sends spaced so
   they cannot coalesce (`livetest/client_test.go`), a reconnect backoff
   (`watch_e2e_test.go`), the stimulus a shutdown race is scheduled by
   (`feed_shutdown_test.go`). Converting one of those to a poll deletes the
   experiment. **Say which it is in a comment**, because the next reader has
   the gate's finding in front of them and nothing else.

   A fifth shape turned up by doing the work rather than by planning it: a
   **best-effort quiesce** — `stableGoroutines`, `settled`, `settledGoroutines`,
   three copies of "sample until two readings agree, then return whatever you
   have". Those look exactly like awaits and are not, for a mechanical reason:
   each is *itself polled by an outer `Eventually`*, so it must return rather
   than fail, and a fatal await inside a poll aborts the retry that is doing the
   actual waiting. A helper that cannot fail is not an assertion.

2. **`Consistently` is the negative-space twin, and a bare sleep followed by
   one read is usually reaching for it.** "No CSP violation was raised while
   booting" was a two-second sleep and one read
   (`conformance/browser_test.go`): a violation at 200ms and a violation at
   1.9s are the same failure, and only the second survived to be read. The same
   two seconds spent asserting throughout is strictly more evidence for the
   same wall clock.

3. **Budgets are stated generously and named.** The costs are not symmetric: a
   budget too large makes a *failing* test slow, a budget too small makes a
   *correct* test red on a loaded machine. The warden CLI contract suite is the
   worked example and the reason this clause is numbered — `Eventually(p.output,
   "5s", "20ms")`, inline, for a real process to start, bind a port, load a
   config and win an election. It flaked under host load, the flake was handed
   back on the program board, and the repair is a named `bootBudget` of one
   minute. A duration inline in an argument list is a number nobody argues
   with; a duration with a name is a claim somebody can.

4. **The timing loop belongs to whatever owns the datum, and sometimes that is
   not `eventually`.** `livetest.Client.Await` blocks on the frame channel, is
   the typed await of the library that owns the frame stream, and fails naming
   every frame it did see — which a generic poller taking one frame per tick
   could not. `awaitRaft` stays on it. Reaching past an owning library's own
   typed await for the generic one is CS-3 over-applied, and it costs a
   diagnostic.

   The same judgement runs the other way for **Ginkgo suites**: the framework is
   mandated there (`pkg/scripts/check-test-style.sh`) and native
   `Eventually` is the idiom, so a suite where matcher composition genuinely
   earns it may keep it. But a Ginkgo spec that only polls a **bool** should
   prefer the primitive anyway — a typed poll turns "false is not true" into
   the value. `conformance/waitLive` is the counter-example kept on purpose:
   both of its polls genuinely produce a bool, so the shell would carry a bool
   and print `false`, which is what `BeTrue` already prints.

**Enforcement:** `../scripts/check_style.py` (rule `CS-9`), **report-only
permanently**, alongside CS-5, CS-6 and CS-7 — plus eval case
`028-await-not-sleep` for the shape and `029-pacing-judgment` for the judgment.

The heuristic reports **a `time.Sleep` standing inside a `for` body, in a file
whose name ends `_test.go`**. The file-name restriction is the rule's scope and
not a convenience: the same shape in production code is a retry, a backoff or a
rate limiter, and CS-9 is about how a *test* waits.

**A CS-9 line is a locator, not a verdict**, exactly as a CS-5, CS-6 and CS-7
line is — and it is the strongest case of the four, because after this branch
**100% of its findings are correct code**. It will not flip to `--strict`, for
the reason counterweight 1 gives: a lexer cannot tell a wait from a throttle,
and the honest response to most CS-9 findings is a comment naming which one it
is looking at.

**Baseline:** measured at `69c491187` over 699 corpus files — **17 findings**,
plus the four named helpers and the two inline loops the heuristic could not
see because they were spelled with `Eventually` rather than a sleep.
**Now:** 10 findings over 702 files, all of them keeps, and each answered at its site: 6
pacing or observation loops, 1 reconnect backoff, 3 best-effort quiesces. Zero
await-shaped findings remain. The census carries the composition
(`test_sleep_loops`, `cs9_findings`) so a new one is visible the day it lands.

---

## CS-10 — A service is conceptual; it never owns the process

The operator's words, verbatim, 2026-09-02, typo included:

> SERVICES SHOULD NOT REQUIRE CONTAINERIZATION SERVICES ARE CONSIDERED
> CONCEPTULA AND ARE INTENDED TO GO INTO SOME BINARY VIA A FUNCTIONAL OPTION

### The rule

1. **A service is a library package.** Composable business logic that a caller
   imports. Its entry point is a constructor taking functional options —
   `New(options ...Option) (*Service, error)`, `WithBoardPath(path string)`,
   `WithListenAddress(address string)` — in the shape `pkg/cron/options.go`
   already uses. The constructor validates the complete option set before it
   builds anything, so a configuration mistake is a construction failure naming
   the fault.
2. **It never owns the process.** No `func main`, no `flag.Parse`, no
   environment read, no signal handler, no compose file as its *definition*, and
   no container requirement. A context arrives from the caller; a listener is
   opened when the caller asks for one, or never, because the caller mounted the
   handler into a server it already had.
3. **Runnable composition is an app/cmd concern.** The binary is where flags,
   environment, signals and process lifetime live. `AGENTS.md` already
   spells this taxonomy out for the public module — `pkg/` primitives,
   `services/` composable business logic, `app/` runnable compositions each
   owning a `cmd/` — and CS-10 is that taxonomy stated as a rule that applies to
   both modules.
4. **Deployment is a property of the binary and its operator.** A container, a
   compose project, a systemd unit and a bare `go run` are four ways to start
   the same binary. Which one this fleet uses is the operator's decision under
   the monorepo's container-first rule; none of them is what the service
   *is*.

The test to apply to a service directory is one question: **could a different
binary use this, and could this run with no container at all?** If the honest
answer is no, the composition seam is in the wrong place.

### Container-first and CS-10 do not conflict, and the reconciliation is explicit

The monorepo's root `CLAUDE.md` opens with a container-first infrastructure rule:
"Default to implementing services, configuration, routing, authentication,
health checks, and application lifecycle in containers and Compose." Read
carelessly beside CS-10, that looks like a contradiction. It is not, and the
two clauses are about different nouns:

| | Governs | Says |
|---|---|---|
| **Container-first** (the monorepo's root `CLAUDE.md`) | how **binaries** are deployed on this fleet | put the deployment in a container and Compose rather than on the host — do not reach for a systemd unit, a host package or a firewall rule when a container will do |
| **CS-10** (here) | what a **service package** is | a library with a functional-options constructor, importable, runnable without a container, owning no process |

**They compose.** CS-10 says the board is a library and `services/foundry-board/cmd`
is a binary over it; container-first says that binary is deployed on this fleet
as a Compose project rather than as a host service. Both are true of the same
service at the same time, and neither weakens the other. What CS-10 forbids is
letting the *deployment* answer become the *definition* — a service you cannot
construct, test or mount without bringing a container up.

The one place the wording genuinely collides is `go/CLAUDE.md`, whose "Creating
New Services" section defines a service as a **runnable binary** and lists a
Dockerfile among its mandatory scaffold files. That is the file to correct, not
the root rule, and it is recorded as open in `exceptions.md`.

### Three counterweights travel with this rule

Without them CS-10 is a licence to delete every compose file in the repository
and shuffle directories for a week, and it will be applied that way.

1. **A thin `cmd/` inside a service directory is the acceptable runnable
   shell.** The `go/` side puts `cmd/` under `services/<name>/` and has since
   long before this rule; this tree puts it under `app/<name>/`. Both layouts
   satisfy CS-10 as long as the shell is *thin* — argv to options, options to
   `New`, `New` to `Run`, and no decision of its own. The layout tension is
   real and is recorded rather than resolved: **CS-10 does not mandate a
   repo-wide move of `go/services/*/cmd` into a `go/app/`**, because that is a
   directory reshuffle with a merge cost and no behavioural payoff, and because
   the rule is about where the *seam* is, not about which directory holds the
   `main`.
2. **A compose file may live beside a service, as one documented deployment
   option.** Two conditions, and both are load-bearing: the bare-binary path
   must *work*, and it must be documented **first**. Documentation order is not
   cosmetic here — a README that opens with `docker compose up` and puts "bare
   metal, for development" thirty lines below has told the reader the container
   is the thing and the binary is a convenience, which is precisely the belief
   this rule exists to correct.
3. **Infrastructure that *is* containerization is out of scope.** The edge
   Caddy, Authelia, the Compose stacks in `docs/runbooks/`: these are not
   services in the CS-10 sense, they are deployments, and there is no library
   underneath them to extract. Do not apply this rule to them, and do not let
   its existence become an argument for un-containerizing the edge.

### Current CS-10 enforcement

The native OCaml advisory detector reports main/configuration/environment,
signal, listener and Gin-engine calls under service trees, plus tracked service
`cmd/` and Dockerfile artifacts. The 2026-09-04 amendment puts those artifacts
with the binary; a Compose deployment may still sit beside the service. A clean
syntax scan does not prove that another binary can construct and mount the
library. Review the actual seam and retain the judgment eval.

### Historical enforcement at adoption — 2026-09-02

**Enforcement:** eval case `031-compose-stays-judgment`, plus the per-directory
audit recorded in `exceptions.md`. `../scripts/check_style.py` has **no CS-10
rule**, and this is the first rule in this skill with no line in that script at
all.

That is a real cost and it is stated here rather than buried, because this
skill's own opening premise is that a rule enforced by nothing is not a rule.
CS-10 gets three things instead of a lexer:

- **A measured baseline over every service directory in both modules**, below,
  so a later re-audit compares against a number rather than a memory.
- **A judgment eval case** whose correct answer *keeps* a compose file, so the
  rule cannot be passed by deleting containers.
- **The restructure itself**, landed in the same change as the rule. CS-10 is
  the first rule here to arrive with its founding violation already repaired,
  which is the only form of enforcement available to a rule a script cannot see.

**The mechanical check that was considered and rejected.** The candidate was: a
`*/services/*` package whose README documents no bare-binary run path, or whose
only run instructions are compose. It was rejected on two grounds. It is a
*documentation* scanner, and `check_style.py` is a Go lexer that shares one
module with the census and the eval runner precisely so that a gate run and a
census row cannot disagree about what they counted — bolting a Markdown reader
onto it makes that guarantee meaningless. And its false-positive rate would be
structural rather than incidental: it cannot tell "this README omits the bare
run because the service requires a container" from "this README omits it
because the service has no README", and it would fire on `services/*`,
which is compliant by construction and has no per-service README at all.

The precedent is CS-5's, one step further along. CS-5 will not block because a
lexer cannot tell a counter's lock from a state machine's lock, so its findings
are legitimately dismissible. CS-10's findings are not even legitimately
*locatable*: "requires containerization" is a property of the relationship
between a package, its documentation and its deployment, and no one of those
three files carries it. A gate that misfires teaches authors to dodge the gate,
and a gate over a corpus this small — six directories — buys less than reading
the six.

### Baseline: every service directory in both modules, 2026-09-02

Measured by reading each directory, not by running a script: the five this
change did not touch at `5840ab605`, and `go/services/foundry-board` at `a3402ab93`,
after the restructure that landed with this rule.
Per-directory evidence is in `exceptions.md` § "CS-10: the service audit".

| Directory | Library seam | `main`/`cmd` inside | compose inside | bare run works | documented first | Verdict |
|---|---|---|---|---|---|---|
| `services/deploy` | `New(name, options...)`, `With*` throughout | no | no | n/a — library | n/a | **compliant** |
| `services/warden` | `New(config, deps)`, **no** `With*` | no | no | n/a — library | n/a | **compliant**, one nuance |
| `go/services/foundry-board` | `New(options...)`, 12 `With*` | thin `cmd/` | yes | yes | yes | **compliant** — *this change* |
| `go/services/candace-cloud` | `AssembleHomepage(config)` under `internal/` | thin `cmd/` | no (root) | yes | yes | **compliant** — *doc fixed here* |
| `go/services/admin` | `dashboard.New(provider, With*)` under `internal/` | thin `cmd/` | no (root) | yes | yes | **compliant** — *doc fixed here* |
| `go/services/ping` | **none** | `cmd/` with all wiring | no (root) | **no** | — | **structural gap, OPEN** |

Four of six were already compliant or one sentence away from it, which is the
measurement worth carrying: the taxonomy in `AGENTS.md` has been holding
on the public side without a gate, and the `go/` side's gaps are two missing
sentences and one hardcoded container path.

The two that were not:

- **`go/services/foundry-board` was the founding violation**, and it was the
  board this program built to watch itself. Every composition decision lived in
  `cmd/main.go`'s flag wiring; nothing outside a container could construct one;
  no specification could reach the wiring. It is now `foundryboard.New(options...)`
  with a thin shell over it, and two of its specifications serve a real request
  through a board that no process, flag, environment variable or container ever
  touched.
- **`go/services/ping` fails structurally, not editorially.** Its bare run
  *is* documented, first, in three separate files — and all three are broken as
  written, because `cmd/main.go` parses its templates from `/root/web/...`, a
  path that exists only because the root compose file mounts `./go` at `/root`.
  Adding a sentence fixes nothing. That is CS-10's sharpest single measurement:
  a service that documents a bare-binary path it cannot actually take. The
  repair is a code change to ping and is **out of scope for the change that
  wrote this rule**; it is recorded as OPEN in `exceptions.md`.

`services/warden`'s nuance is worth stating so a future reader does not
"fix" it: it uses config-struct-plus-interfaces constructors and has **zero**
`With*` options. It satisfies clauses 1–4 — it is a library, it owns no process,
its composition is `app/warden/cmd`, and its Dockerfile and systemd unit
live with the app — but if CS-10 is read as literally mandating the functional
option *spelling*, warden is the one place in `services/*` that does not
use it. The ruling names functional options because that is the shape that
composes; a validated config struct at a seam nobody needs to extend is the same
idea with less ceremony, and warden is not to be churned into options to satisfy
a spelling.

---

### Amendment (operator, 2026-09-04): no listener, no cmd/, no Dockerfile in a service directory

Operator, 2026-09-04, reviewing the copilot adapter, verbatim: "recall all
services are meant to be options that can be slid into a pre-existing gin
binary it's never a service's job to start a new server process", and "there
should NOT be a cmd/ dir nor a dockerfile i don't understand why those would
exist since those are EXCLUSIVELY ONE TO ONE TO A BINARY".

Counterweight 1 above — a thin `cmd/` inside a service directory — is
**withdrawn**. A service package exposes `New<Thing>(...Option)` and
`Register(router gin.IRouter)`; it never opens a listener, never builds its
own engine, never carries a `Run`, a `cmd/`, or a Dockerfile. The binary that
mounts it lives in `go/app/` (private) or `app/` (public), owns the
engine (`go/pkg/httpserver.NewEngine`), the listener, flags, signals and its
own Dockerfile. `go/services/copilot-adapter` was brought to this shape in
the same change; the `services/*/cmd` directories the 2026-09-02 audit
accepted are the retrofit backlog. Counterweight 2 (a compose file beside a
service as one deployment option) was not addressed by the ruling and stands
until it is.

## CS-11 — Test files dot-import ginkgo/v2 and gomega

The operator's directive, 2026-09-03:

> a linter for ginkgo/gomega imported WITHOUT the dot — the house convention is
> `. "github.com/onsi/ginkgo/v2"` and `. "github.com/onsi/gomega"` (dot import)
> in tests; a plain or aliased import of those two packages is the violation.

### The rule

In a `_test.go` file, `github.com/onsi/ginkgo/v2` and `github.com/onsi/gomega`
are **dot-imported**, so a spec reads `Describe`, `It`, `Expect` and `Eventually`
unqualified — the way this tree's suites already do. A **plain, aliased, or
blank** (`_`) import of either of those two exact packages in a test is the
violation. This was not a new preference imposed on the corpus; it was a
convention already followed **709 times** and unenforced. The rule exists to
keep it that way, which is this skill's founding argument applied to a rule that
had almost won on its own.

### Two counterweights travel with this rule, and both were forced by real code

**1. Non-test code that depends on gomega or ginkgo as a *library* imports it
QUALIFIED.** This is the exemption the rule exists around, not a softener.
`pkg/eventually` is production code whose engine is gomega — it wraps
`gomega.NewWithT(...).Eventually(...)` behind a typed shell so the rest of the
tree waits through `eventually.Await` instead of re-deriving a poll. Dot-importing
gomega there would pour an assertion vocabulary — `Expect`, `Eventually`, every
matcher — into a *production* package's namespace, which is the anti-pattern the
exemption is written for. `eventually.go` imports gomega qualified on purpose, and
carries a comment saying so. The rule's scope is the file name: a `_test.go`
dot-imports, a production file does not, and the two are decided by what the file
*is* rather than by what it imports.

**2. The gomega and ginkgo SUB-packages stay QUALIFIED.** `gomega/gstruct`,
`gomega/gexec`, `gomega/gleak`, `gomega/gbytes`, `gomega/gmeasure`,
`gomega/types`, and the `ginkgo/v2/...` helper packages are namespaced helpers,
not the DSL, so they read `gstruct.MatchFields`, `gexec.Start`. The rule targets
**exactly** the two quote-bounded paths `github.com/onsi/ginkgo/v2` and
`github.com/onsi/gomega`, and nothing else. A test that dot-imports the two and
qualifies `gstruct` is fully compliant.

### The structural exemption the fix-wave uncovered: a forced qualification

The rule as first written assumed every test could take the dot. Driving the
corpus to zero found that **two in-package tests cannot** — a dot import there is
not a style choice but a *redeclaration compile error* — and the two failed for
different reasons, which is why this exemption is a rule rather than a note:

- **redis, the explicit collision.** `go/pkg/foundation/storage/redis` declares
  the exported type `Entry` — the value `Cache.Get` returns — and ginkgo's
  `DescribeTable` helper is also named `Entry`. `cache_test.go` is an in-package
  test (`package redis`) that must use `DescribeTable`/`Entry`, so a dot import
  of ginkgo would land ginkgo's `Entry` in the file block beside the package's
  own `Entry`, and *"Entry already declared through dot-import"* is a compile
  error, not a lint. The test uses unexported internals (`cache.messageType`,
  `cacheKey`), so it cannot move to an external package to escape the collision.
  ginkgo stays **aliased**; gomega, which collides with nothing, is dot-imported.
- **eventually, the silent collision.** `pkg/eventually` declares
  `Consistently`, mirroring gomega's own, so *any* in-package test of it
  redeclares `Consistently` if it dot-imports gomega — even a test that never
  writes `gomega.Consistently`. Here the honest fix was different: eventually's
  suite has an idiomatic escape the redis one does not, so its specs moved to an
  external `package eventually_test` (dot-importing both cleanly, reaching the one
  unexported method through an `export_test.go` seam) rather than carrying a
  forced qualifier. The collision was removed instead of exempted, because it
  could be.

The rule that comes out of the two: **an in-package test whose own package
declares a top-level identifier the library also exports keeps that library's
import qualified, because a dot import would not compile.** Where the test can
become an external `_test` package cleanly, that is the better fix and the
collision disappears; where it cannot (unexported internals, an exported type
whose name is the collision), the qualified import is correct and stays.

### Enforcement: a blocking mechanical gate, in the test-convention script

**Enforcement:** `pkg/scripts/check-test-style.sh` (repo-wide over every
tracked `*_test.go` in every first-party module), **blocking in CI** through
`.github/workflows/candace-go-checks.yml`, which already runs that script. This
is high-confidence and trivially safe — a plain import of one of two exact paths
in a test is a violation on sight — so unlike CS-5/CS-6/CS-7/CS-9 it is a
verdict, not a locator, and it blocks the way CS-1/CS-2/CS-8 do. As with those,
**blocking landed only after the fix-wave drove the corpus to zero**; a gate is
never turned red.

The structural exemption is mechanical, not a marker comment or an exclusion
list: the gate treats a qualified import as forced when a dot import would
redeclare one of the package's own top-level names — evidenced by the package
referencing `<qualifier>.Name` for a `Name` it declares. That is the same kind
of structural exemption CS-8's sealed-sum and hook cases are, and it is the only
one; production files are out of scope entirely because CS-11 reads test files.

CS-11 is **also** carried by `../scripts/check_style.py` (rule `CS-11`,
report-only there) so the tier-1 eval corpus and the census measure the same
rule the shell gate blocks on — the two-lane shape CS-8 established, with the
blocking lane in the shell this time. Eval case `032-dot-import-assertions`
exercises the mechanical shape and `033-keep-qualified-judgment` the judgment,
whose correct answer keeps a qualified gomega import in production code.

**Baseline:** measured 2026-09-03. **709 dot-imports, 5 non-dot.** Of the five,
one is `eventually.go` (production, the counterweight-1 exemption, stays), two are
eventually's in-package test files (removed by the external-package move, now
dot-imported), and two are redis's (the `Entry` collision, forced to stay
qualified). After the fix: **0 CS-11 findings** — the corpus that was already
99.3% compliant is now enforced there. The census carries the composition
(`dot_assertion_test_imports`, `nondot_assertion_test_imports`,
`assertion_import_exempt`, `cs11_findings`) so the forced qualifications stay
visible as a number rather than disappearing into a green gate.

## CS-12 — A constructor names what it builds

Operator, 2026-09-04, reading `copilotadapter.New`, `postgres.NewStore` and
`copilotbridge.New` in the copilot adapter diff, verbatim: "can we please stop
just naming shit "new" and instead name shit like NewXyzThingThatItIs".

### The rule

A constructor is named for the concrete type it returns: `NewCopilotAdapter`,
`NewPostgresStore`, `NewCopilotBridge`. Never bare `New`, and never a generic
noun that only the package qualifier disambiguates (`NewStore`, `NewClient`,
`NewService`). The package name is not part of the name: at a call site the
import is aliased, or the call sits among ten others, and `New(` says nothing.
The same holds for unexported constructors — `newSessionRegistry`,
`newResolutionRegistry`, `newEventTranslator` were already right.

This is CS-8's instinct applied to the name instead of the type: the caller
should be able to read what concrete thing it is holding without opening the
package.

### Counterweights

1. Generated code keeps its generator's names. sqlc's `storedb.New`,
   oapi-codegen's `NewClient`/`NewClientWithResponses`/`NewStrictHandler`, and
   `.pb.go` constructors are projections; renaming them means editing output,
   which CS-4 forbids. The consuming handwritten code is where the rule bites.
2. A third-party constructor being mirrored keeps the mirrored name, and says
   so in a comment.
3. The rename is not a licence to reshuffle receivers or types beyond the
   constructor and the type it names. `New` → `NewCopilotAdapter` may pull
   `Service` → `CopilotAdapter` with it, because the name is now visibly wrong;
   it does not pull anything else.

### Current CS-12 enforcement and historical baseline

The native OCaml AST detector reports receiverless constructors named `New`,
`new`, `NewStore`, `NewClient`, `NewService`, `newStore`, `newClient` and
`newService`. It remains advisory in the registry and excludes marked generated
code. A mirrored upstream constructor still requires the documented judgment.

At adoption, 2026-09-04, the proposed first detector only targeted bare `New`;
the wider native detector supersedes that plan. The baseline on 2026-09-04,
outside generated code and tests: 27 files declare a bare `func New(` across
`go/` and this tree (`pkg/pgmem.New` and `MustNew` among them). The
copilot adapter was renamed in the same change as this rule; the 27 are the
retrofit backlog, not this rule's excuse to stay report-only forever.

## CS-13 — No magic strings

Operator, 2026-09-04, reading `go/services/copilot-adapter/handlers.go` and
`sessions.go`, where status values like `"idle"`, `"running"`, `"queued"` and
`"ended"`, error codes like `"store_error"` and `"session_not_found"`, and the
event kinds beside them were spelled inline, verbatim: "have an agent write a
fucking lint check for magic strings i'm tired of this. if a string isn't
defined as a constant somewhere, then it's a fucking magic string i think".

### The rule

A string literal that is a **value** — compared against, assigned into a field,
passed as an argument, returned, used as a map key, put in a slice — is a magic
string unless it is the initializer of a `const` or of a package-level `var`.

Values the program dispatches on or persists are the class this is about:
status names, kinds, error codes, header names, query-parameter names, column
and enum values, environment variable names, flag names. Each must be declared
once, named, and in the package that owns its meaning.

**A generated enum is the preferred source**, ahead of a hand-written constant,
because it cannot drift from the contract: `api.SessionStatusIdle` and
`api.TurnStatusQueued` are projected from `openapi.yaml`, sqlc's column
constants from the migrations, protoc's from the `.proto`. A second hand-written
spelling of a contract value is a second place for the contract to be wrong.
Where the value must be persisted or compared as a plain string, convert at the
seam — `string(api.SessionStatusIdle)` — rather than re-spelling it.

The defect is not verbosity. `"idle"` written in six places is six independent
decisions that happen to agree today, and nothing fails when the seventh
disagrees; a compiler that has never seen the value cannot help. Naming it once
turns a runtime mismatch into a missing identifier.

### Counterweights

1. **Human-facing text is not a value.** Error messages, log messages, format
   strings — the first argument of a `Printf`-family call and its `%` verbs —
   documentation strings and panic messages stay inline. Naming a sentence
   moves it away from the code that explains it and buys nothing: nothing
   dispatches on prose.
2. **Struct tags, import paths, `//go:generate` and `//go:embed` lines, and
   build constraints are syntax, not values.** A `json:"status"` tag is part of
   the type declaration, and `//go:embed migrations` cannot reference a
   constant even where one exists beside it.
3. **`_test.go` files are out of scope.** A spec spelling `"idle"` is asserting
   the wire value on purpose; replacing it with the constant under test makes
   the assertion tautological. This is the same file-name scope CS-9 and CS-11
   carry, and for the same kind of reason.
4. **Generated files are outside the corpus already**, by the `DO NOT EDIT`
   marker every consumer of `go_style_scan.py` shares.
5. **An empty string and single-character separators are not magic** — `""`,
   `" "`, `","`, `"/"`, `"\n"`. There is no meaning a name could add.
6. **A declaration is not a use.** A literal that initializes a `const`, or a
   package-level `var`, or that is an element of a package-level composite
   literal — a registry table, CS-6's shape — *is* the single named spelling
   the rule is asking for.

### Enforcement: report-only, and a locator rather than a verdict

`scripts/check_style.py` carries it as rule `CS-13`, and it never blocks. The
detector reads a string literal standing in a **function body** in an
expression position, which is counterweights (2) and (6) enforced by scope
rather than by a list: a package-level `const`, `var`, struct tag, import path,
`//go:generate` line and registry table are all outside a body and are never
looked at.

What the lexer cannot see is stated with the six imprecisions already in
`../scripts/go_style_scan.py`'s header, and every one of them under-reports:

* **Human-facing text is recognised by two proxies, not understood.** A literal
  in the message position of a call the detector knows (`errors.New`,
  `fmt.Errorf`, the `Print`/`Fprint` families, `panic`, and any method call
  whose final identifier is a log or test-report name), a literal assigned into
  a `Message:`-shaped composite key, and **a literal containing a space** are
  read as prose. The last is the broad one: a value the program dispatches on
  is identifier-shaped, and a sentence is not. A persisted value that genuinely
  contains a space is therefore missed — and none of the founding evidence
  does.
* **Every argument of a logging call is exempt**, structured logging's key
  strings included, because no lexer can tell the key in
  `slog.String("status", "idle")` from its value.
* **A function-local `const` exempts the literal that initializes it**, which
  the rule as written permits, even though a value declared inside one function
  is not "declared once in the package that owns its meaning". That is a
  review question, not a lexer finding.
* **A callee is matched on the text as written**, so a `fmt` imported under an
  alias is not recognised as the format family.

Baseline on 2026-09-04, over the 807-file corpus: **2856 findings**. That is
the retrofit backlog, and it is far too large for a blocking gate — CS-13 is a
locator in CS-5's sense, a list of places somebody should read. The rule bit
where it was minted: `go/services/copilot-adapter`'s non-test, non-generated Go
read **70 findings across 8 files** before this change and **0** after, with
the status and kind values taken from the generated `api` enums and the error
codes declared once as an `errorCode*` `const` block naming the contract's
`Error.code` field.

Judged mechanically by eval case `035-magic-strings`.

## CS-14 — No hand-rolled twin of a library primitive

Operator, 2026-09-04, reading `optionalTime`, `nullableString` and
`optionalUUID` in the copilot adapter — with `nullTime`, `valueTime`,
`nullString`, `valueString` and `nullInt16` already living in
`pkg/cron/postgres/store.go`, written months earlier by somebody
solving the same problem — verbatim: "WHY ARE YOU WRITING MORONIC FUNCTIONS
LIKE OPTIONALTIME WE ALREADY HAVE A NULLS PACKAGE THAT WE USE FOR THAT HOLY
SHIT WRITE A LINTER FOR THAT", and, asked which package: "it's guregu null".

### The rule

A receiverless function that converts between a library's optional type and
the plain or pointer value it wraps is a **twin**: a second spelling of
something the tree already has, which drifts from the first the moment either
one is touched. The optional types this rule reads are `sql.NullString`,
`sql.NullTime`, `sql.NullBool`, `sql.NullInt16`, `sql.NullInt32`,
`sql.NullInt64`, `sql.NullFloat64` and `uuid.NullUUID`.

The house answer is **`github.com/guregu/null/v5`, emitted by sqlc `go_type`
overrides** — `go/services/copilot-adapter/store/sqlc.yaml` is the reference
declaration: nullable `text` becomes `null.String`, nullable `timestamptz`
becomes `null.Time`, nullable `uuid` becomes `*uuid.UUID`. The generated row
type then carries the optional shape the caller wants, and the conversion is
the library's own: `null.TimeFrom(v)` and `null.NewString(v, ok)` going in,
`.Ptr()` and `.ValueOrZero()` coming out. **No conversion helper exists**, so
there is nothing to keep in sync.

This is CS-3 and CS-4 pointed at one recurring instance. CS-3 says centralize a
primitive at the layer that owns its semantics; CS-14 says the layer that owns
*this* one is a library somebody already imported, and the correct number of
copies of it in this repository is zero — not one shared `nulls` package of our
own, which would be a third spelling. CS-4 says generate before you write, and
the generator here is sqlc, which will emit the type directly if the config
asks it to.

### Counterweights

1. **Generated code and tests are outside the scope.** Generated files are
   projections and leave the corpus by the `DO NOT EDIT` marker every consumer
   of `go_style_scan.py` shares; `_test.go` files are excluded by file name,
   the same scope CS-9, CS-11 and CS-13 carry, because a spec may write out the
   conversion it is asserting about.
2. **Carrying an optional through is not converting it.** A store method that
   takes a `sql.NullTime` as an argument in order to write it, a constructor
   that puts one in a struct, a query wrapper that passes one down — these keep
   the value in the library's own type on both sides, and there is no second
   spelling to drift. The detector encodes this as a shape rather than a list:
   it reads a receiverless function whose **entire parameter list is one
   value** and whose **single result is the other shape**, one side the optional
   and the other side the plain or pointer value. `func endedRow(endedAt
   sql.NullTime) *SessionRow` is silent, and so is `func normalize(value
   sql.NullTime) sql.NullTime`, which converts between two library types and
   not between a library type and ours.
3. **There is no exempt shared package, and that is the ruling rather than an
   oversight.** A `From`/`New` constructor implemented once in a shared `pkg/`
   would be exactly the kind of centralization CS-3 asks for — and it is *not*
   what was ruled. The operator named the library. `guregu/null` already ships
   those constructors, sqlc already emits its types, and a house package
   wrapping them would be a twin of the twin, one layer up. So nothing is
   exempt today, and if a future ruling does mint such a package, this
   counterweight is the place to record it.

### Enforcement: report-only, and it under-reports

`scripts/check_style.py` carries it as rule `CS-14`, and it never blocks. The
detector reads the same `function_heads` walk CS-2, CS-8 and CS-12 use, plus a
scan of the one parameter's and the one result's types.

Header imprecisions, in the usual register, all of them under-reporting:

* **The type is matched on the text as written**, so `database/sql` imported
  under an alias, or a dot-imported `NullTime`, is not recognised. Same
  limitation, same direction, as CS-13's callee matching.
* **The integer widths are read as a family.** `func nullInt16(v int)
  sql.NullInt16` converts an `int`, not an `int16` — the twin is written
  against the width the caller happens to hold — so `int`, `int8`, `int16`,
  `int32` and `int64` are all accepted as the plain shape of an `sql.NullInt*`.
  A conversion through an unrelated named type (`type Retries int` in, a
  `sql.NullInt16` out) is missed.
* **Exactly one parameter and exactly one result.** A twin that also takes a
  `*testing.T`, a `context.Context`, or a second value, or that returns an
  `error` beside the converted value, is not read. That restriction is what
  buys counterweight (2), and it is the reason the detector fires on 5 things
  in an 809-file corpus and on nothing else.
* **A method is out of scope**, for CS-8's reason: a receiver puts the choice
  of result type in whatever the type satisfies rather than in the declaration.
  A twin spelled as a method is missed.
* **Only these eight optional types.** `sql.NullByte`, `pgtype.Timestamptz`, a
  `null.Time` being re-wrapped, or any other library's optional is not read
  until the evidence says it should be. The list is where a future ruling
  extends the rule.

Baseline on 2026-09-04, over the 809-file corpus: **5 findings at
introduction**, and all five in one file — `pkg/cron/postgres/store.go`'s
`nullTime`, `nullString`, `nullInt16`, `valueTime` and `valueString`. Zero false
positives anywhere else. The copilot adapter, where the rule was minted, read
**0** already: its `optionalTime`, `nullableString` and `optionalUUID` were
deleted when the sqlc overrides landed, before the lint existed to name them.

**The corpus reads 0 today.** The cron store was migrated onto guregu/null in
the same change that minted the rule — its `sqlc.yaml` gained the same
overrides, the five helpers were deleted, and the callers read `.Ptr()` and
write `null.TimeFrom`. So CS-14 has no retrofit backlog, unlike CS-12's 26 and
CS-13's 2841, and **any finding it reports is new code**. It stays report-only
regardless: the rule is one day old, its detector reads one narrow shape, and a
rule that has never yet been wrong about anything has not been tested enough to
block a pull request. Promoting it is a later decision with evidence behind it.

Judged mechanically by eval case `038-null-twins`.

### Amendment (operator, 2026-10-01): the first-error latch is a twin too

Operator, reading `runtime/host.go:122` on PR #269, verbatim: "is this
really idiomatic in go for async loop shit". The code was a buffered
`failures := make(chan error, 1)` filled through a `report` closure doing a
non-blocking `select { case failures <- err: default: }`, and a two-arm select
waiting on either the root context or that channel. That is a hand-rolled
first-error latch, and the standard library already owns it:
`ctx, cancel := context.WithCancelCause(ctx)`, pass `cancel` as the failure
reporter, wait on `<-ctx.Done()` and read `context.Cause(ctx)`. The first cause
wins by construction. CS-14 is not only about optional values: it is any
second spelling of a library primitive.

Counterweight: errgroup and conc are NOT the answer at that site. They join
everything at once, and the host runtime's contract is ordered reverse
shutdown, so the runtime keeps per-service scopes and only the failure latch
becomes a context.

Enforcement: the advisory CS-14 locator in `tools/house_lint/advisory.ml`
(`first_error_latches`) reports, in non-test Go, a `select` with a `default`
case whose send targets a variable bound to `make(chan error, 1)`. Blocking
sends, other capacities and other element types are not reported.

---

## CS-15 — Whoever starts a goroutine owns its cleanup

Operator, 2026-10-01, correcting the first wording of this rule (which said
goroutines start only in `runtime/`), verbatim:
"go routines do not start only in runtime services can start goroutines, they just have to be responsible for cleanup and, if you have inter-service things, then YOU'RE responsible for lifecycle management".

### The rule

Any code may start a goroutine, a service included. Starting one makes the
starter responsible for it: something must be able to stop it (a context it
watches, a channel it drains) and something must wait for it to finish (a
WaitGroup, an errgroup, a conc pool, a `runtime.Scope`, or a receive on a
channel it signals). When goroutines coordinate across services, the code that
wires those services together owns that shared lifecycle; it does not fall to
whichever service happened to start first.

`runtime.Scope` is the easiest way to satisfy the rule: a service
mounted into the `HostRuntime` is handed a scope, and `scope.Go` /
`scope.GoOwner` give it cancellation and a join for free. It is a convenience,
not a monopoly.

### Tests are not exempt

Operator, 2026-10-01, verbatim: "WHY THE FUCK DOES THE GATE EXEMPT TESTS".
A goroutine a spec starts needs an owner like any other: a join (`Wait`, a
receive, a channel handed to `Eventually`, a scope) or a context-driven exit,
with goleak proving it at the end. CS-15 and the network part of CS-16 scan
`_test.go` files too; a spec reaches a socket through `ipc/net` like
any other caller. (The pgx part of CS-16 drops its exemption separately.)

### Counterweights

1. Generated code is outside the corpus already.
2. Goroutines a library starts inside its own call (grpc, net/http,
   `signal.NotifyContext`, `context.AfterFunc`) are not `go` statements in
   this tree; the code holding the call owns them.

### Enforcement

Advisory native locator `CS-15` in `tools/house_lint/placement.ml`. It reports a
`go` statement in selected non-test Go when its enclosing function or method
shows no owner:

* no join there: no `.Wait()` call, and no receive (`<-ch`, in or out of a
  `select`) or `range` over a channel the goroutine sends on or closes;
* and no context-driven exit: the goroutine's call mentions no `.Done()` or
  `.Err()` selector and no context value (an identifier `ctx`, or one ending in
  `ctx`, `Ctx` or `Context`).

It is syntax, so it is a locator: an owner can live in another function (a
struct's `Close` that waits, a loop that drains a channel elsewhere), and a
finding is answered at its site. Baseline when this wording landed
(2026-10-01, 1245 handwritten files): **17 findings**, down from 99 under the
withdrawn "only in runtime/" wording, which reported every `go` statement
whether or not it was owned. With tests in scope and function literals
(spec bodies) treated as enclosing functions, the same day: **45** (28 in
`_test.go`); after the S1 slice's own specs were fixed and a channel returned
to the caller counted as a join, **41** (24 in tests). CS-16's network part,
tests included: **42** (31 in tests), then **39** (28). The native report owns
the current count.

---

## CS-16 — Boundary crossings only under `ipc/`

Operator-approved ontology, 2026-10-01 (slice S1). Gate wording: **CS-16
boundary crossings only under ipc/ (os/exec only ipc/proc; net dial/listen +
grpc.NewClient + os file APIs only ipc/; pgxpool/pgx.Connect only
ipc/db/csfpg).**

### The rule

Crossing out of the process's shared memory — a socket, a subprocess, a file, a
database pool — happens only through a capability under `ipc/`, granted
by the binary to a constructor as an I-prefixed interface. The three tiers are
in-process (shared memory, channels, direct `ServeHTTP`: no capability), kernel
I/O (sockets including loopback, files, pipes: `ipc/net`, `ipc/fs`) and the
process boundary (fork/exec: `ipc/proc`, the sole gateway).

The capabilities that exist today are `ipc/net` (`IListener`,
`IDialer` over `HostNetwork`), `ipc/net/http` (`HTTPListener`, a
mountable service), `ipc/net/grpc` (`IClientConnector`) and
`ipc/db/csfpg` (`IDB`, opened as a `Pool` by `OpenPool`). The
homepage's Warden watcher is the reference consumer of the network part: it
used to call `grpc.NewClient` itself; it now takes an
`ipcgrpc.IClientConnector`. The database part's reference consumers are the
CSF knowledge store and the Deploy control store: `csf.NewPostgres` and
`store.NewControlStore` take a `csfpg.IDB` and never close it, while
`app/csf` and `app/deploy` open the pool with
`csfpg.OpenPool` and close it after the services that borrowed it stop.

Slice S5 added the process boundary: `ipc/proc` (`ILauncher` over
`HostLauncher`, returning a concrete `*Process`) owns launch, standard streams,
cancellation (the child's whole process group, a terminal's whole session),
waiting and reaping, and is the only non-test importer of `os/exec` in
this tree. Git, gh, Compose, `go build`, templ and interactive shells all start
through it, granted to the constructor that needs them. `ipc/docker`
(`ContainerHost` over the Docker Engine SDK client) is the container
capability, with a sandboxed run-to-completion that always removes its
container.

### Tests cross boundaries the same way

Operator, 2026-10-01, verbatim, on finding that the gate skipped `_test.go`:
"WHY THE FUCK DOES THE GATE EXEMPT TESTS". And, on how a spec should reach a
database instead: "TESTS COULD JUST USE GOMOCK" / "OR USE PGMEM".

A spec never opens a real database, socket or process of its own. Code that
uses the database takes `csfpg.IDB`, and its specs use the generated double
`mocks.MockIDB` (`ipc/db/csfpg/mocks`, regenerated by the package's
`go:generate mockgen` directive). A spec that needs the schema itself runs the
real migration file on `pkg/pgmem`, with no DDL in Go. Counterweight:
a gomock double and pgmem are not crossings, so CS-16 never reports them.

The one labelled exception is the opt-in **acceptance tier**, the exception
CS-18 allows: files behind the `acceptance` build tag that run against a
disposable PostgreSQL named by an environment variable and open pools only
through `csfpg.OpenPool`. It holds exactly what pgmem cannot yet prove:
`csfpg`'s pool and `ApplySchema` on real PostgreSQL, and the Deploy store,
control, reconcile and Core lifecycle suites, whose SQL uses PL/pgSQL
triggers, `LEFT JOIN LATERAL`, arrays and `DISTINCT ON` views. The tier
shrinks as pgmem learns those; it is a backlog, not a home.

### Enforcement: CS-16 (network and process) and CS-16-DB (PostgreSQL)

Advisory native locator `CS-16` in `tools/house_lint/placement.ml`, scoped in
S1 to the network: outside `ipc/`, in selected Go (tests included), a call to
`net.Listen*`/`net.Dial*`, the package-level `net/http` `ListenAndServe`,
`ListenAndServeTLS`, `Serve` and `ServeTLS`, `grpc.NewClient`/`Dial`/
`DialContext`, or a `net.Dialer`/`net.ListenConfig` composite literal. Calls
are resolved through the file's imports, so an alias is followed and a
different package named `net` is not. An unaliased import binds its last path
element with a module major-version suffix skipped, as Go does, so
`github.com/jackc/pgx/v5` binds `pgx`.

The PostgreSQL part is its own rule, `CS-16-DB`, and it is **mandatory**. It
is narrower than `ipc` as a whole: outside
`ipc/db/csfpg/`, a call to `pgxpool.New`, `pgxpool.NewWithConfig`,
`pgx.Connect` or `pgx.ConnectConfig` is reported. Parsing a configuration
(`pgxpool.ParseConfig`) and using a pool someone else opened are not crossings
and are not reported. Baseline: 2 production sites at introduction
(`app/csf/cmd/main.go`, `services/deploy/store/store.go`),
both migrated in S4. Removing the test exemption then found 8 more, all
administrative pools in Deploy test files; those suites moved to the
acceptance tier on `csfpg.OpenPool`, the count read 0 with tests included, and
the part was split out as blocking `CS-16-DB`. `database/sql`'s
`sql.Open("pgx", ...)` is not resolved by this locator; the one production
site, the CSF Workbench database, was moved onto `csfpg.OpenPool` and
`Pool.OpenSQL` in the same slice.

The `os` file API part is a later slice; method calls such as
`server.ListenAndServe()` cannot be resolved syntactically and are not
reported. Both parts scan `_test.go` files. The network part stays advisory
while its test-file findings (sockets opened directly by specs) move to
in-process transports or `ipc/net`.

The process part (S5): outside `ipc/proc/`, an import of `os/exec` or
`github.com/creack/pty`, or a call to `os.StartProcess`, `syscall.Exec`,
`syscall.ForkExec` or `syscall.StartProcess`. The import is the finding,
because every use of the package (`exec.Cmd`, `LookPath`, `ExitError`) is part
of the crossing. Other `ipc/` packages are not exempt from it: the
container capability reaches the Engine over its socket, not by forking.
Tests are NOT exempt from the process part (operator, 2026-10-01: "tests are
not exempt from the capability gates"; unit tests stay in the package,
integration tests sit outside it and double the exported interfaces with
gomock): a consumer's spec doubles `proc.ILauncher`, and only
`ipc/proc`'s own unit specs start real children. At S5's merge the
non-test count inside this tree is 0, so the non-test half is ready to block
here; the remaining findings are `go/` sites and test fixtures that
still fork Git or a server directly, the retrofit backlog.

---

## CS-17 — Config is a capability

Operator-approved ontology, 2026-10-01 (slice S6): `runtime/config` is
"the only reader of the process environment". Gate wording: **CS-17 the
process environment is read only by the config capability.** Operator,
2026-10-01, verbatim: "Tests are not exempt from the capability gates".

### The rule

`os.Getenv`, `os.LookupEnv` and `os.Environ` appear only in
`runtime/config` (the capability) and in a binary's own
`go/app/<name>/config` package. The binary's `main` obtains
`runtimeconfig.OSEnvironment()` once and passes it to its config package's
`Load`; everything else — services, libraries, the binary's assembly — receives
parsed values, or the `Environment` itself, through its constructor. A service
never reads the environment (CS-10 already says so); CS-17 extends the same
line to every library.

The binary DECLARES its names: each one is a constant in its config package,
and its default is the fallback argument of the capability call
(`environment.String(envAddress, defaultAddress)`), not an `if value == ""`
after a `Raw` read. That declaration is what makes configuration statically
checkable: the monorepo's Compose environment check compares every Compose service built from
this repository's Dockerfiles with the names its binary's import closure reads
and reports names set but never read (**dead**) and names read without a
declared default but never set (**missing**). A default applied by code after
`Raw` is invisible to it and shows up as missing — move the default into the
call. `go/app/<host binary>/config` is the reference consumer.

### Counterweights

1. Tests are in scope. A spec hands the code under test
   `runtimeconfig.NewEnvironment` over a map, which is also the faster and
   more isolated fixture; it sets real variables with `GinkgoT().Setenv`, a
   write that the rule does not report. The capability's own specs and a
   binary config package's specs are inside the allowed directories.
2. `os.Setenv`/`os.Unsetenv` write and are not reads. A binary preparing a
   subprocess environment through `exec.Cmd.Env` is a CS-16 `ipc/proc`
   concern, not this one.
3. Names consumed by the Go runtime or the container (`PATH`, `HOME`, `TZ`,
   `SSL_CERT_FILE`, `GOMEMLIMIT`, ...) are read by the runtime, not by code in
   this tree; envcheck lists them explicitly rather than calling them dead.

### Enforcement

Advisory native locator `CS-17` in `tools/house_lint/placement.ml`:
import-resolved `os.Getenv`/`os.LookupEnv`/`os.Environ` calls in selected Go,
`_test.go` included, outside the two allowed locations. An aliased `os` import
is followed and a different package named `os` is not. It is a locator, not a
verdict, carrying the retrofit backlog of every binary and library written
before the capability existed; the native report owns the current count. The
introduction baseline (S6, 2026-10-01, measured on the S6 branch rebased
after S4): 59 findings, 29 of them in `_test.go` files; 19 in `go/services`, 11 in
`pkg`, 10 in `services`, 9 in `app`; 0 in
`go/app/<host binary>`, the reference consumer.

The static reachability check lives in `tools/env_reachability` and runs as
the monorepo's Compose environment check (`--strict` fails on dead, missing or unresolved
services). It reads checked-in files only and starts nothing.

---

## Directory names are ontology terms (`ONTOLOGY-DIRS`)

Operator-approved ontology, 2026-10-01: "Directory names must be ontology
terms. `internal/` is a transparent visibility marker; its children are still
named by ontology terms. Nothing is named "engine"."

Advisory native locator `ONTOLOGY-DIRS` in `tools/house_lint/placement.ml`. It
reads the term identifiers from `csf/compiler/language/architecture.csf`
itself, so the rule cannot drift from the dictionary it enforces; a directory
matches a term by its identifier or the identifier's plural (`services`,
`widgets`). It checks role positions only: the first directory under
the repository root, and every directory below the layered roots `ipc`, `runtime` and
`web`. A service, package or app below its root is named by what it does.
`internal` is skipped wherever it appears. Catch-all names (`engine`, `util`,
`utils`, `common`, `core`, `helpers`, `misc`) are reported at any depth. One
finding per offending directory, at the first offending segment.

---

## CS-18 — The test layout: unit in the package, integration outside it on gomock

The operator, 2026-10-01, verbatim:

> TESTS COULD JUST USE GOMOCK

> OR USE PGMEM

> like if you have an exported interface, you have a reason to do gomock

> unit tests remain in the package, "integration tests" are outside the package
> and test the client usability and defined and undefined usage patterns thanks
> to gomock

> WHY THE FUCK DOES THE GATE EXEMPT TESTS

### The rule

1. **Unit tests are in-package.** A `_test.go` file declaring `package foo`
   tests foo's internals: unexported helpers, state transitions, the pieces a
   client never sees.
2. **Integration tests are the external `package foo_test` specs.** They use
   only foo's exported API — the way a client would — and every dependency
   foo takes is a gomock mock of an exported interface. They cover the
   defined usage *and* the undefined usage: calls in the wrong order, nil
   arguments, use after `Close`, a second `Start`. Each misuse asserts the
   defined error foo returns for it (`MatchError(foo.ErrClosed)`), not merely
   that nothing panicked. This is what "integration test" means in this
   repository; the word does not mean "talks to real infrastructure".
3. **Tests make no real crossings.** No real database pool, no listening or
   dialing socket (loopback included, so `httptest.NewServer` is a crossing
   and `httptest.NewRecorder` is not), no subprocess, no container. A
   dependency is a gomock mock; the database substrate is pgmem
   (`pkg/pgmem`). When pgmem lacks a feature a test needs, extend
   pgmem — that is the rule's instruction, not a reason to reach for a real
   server.
4. **Every exported interface has a mock.** "If you have an exported
   interface, you have a reason to do gomock": an exported I-interface carries
   a `//go:generate` mockgen directive — beside it, or in the consumer test
   package that uses it — and the generated mock is committed. A hand-written
   `MockIFoo` is not a mock under this rule (CS-4 already locates those).
5. **House gates do not exempt test files.** A gate decides its scope by what
   the rule is about. CS-18's three locators read test files because tests
   are the subject; a gate that skips `_test.go` to make its number smaller is
   the failure the last quote names. CS-15 and the network part of CS-16
   already scan tests on the same instruction; the pgx part of CS-16 still
   exempts them at this rule's introduction and drops that exemption in its
   own slice.

**How CS-18 and CS-16 meet in a test.** CS-16 says *where* a crossing may
live (only under `ipc`); CS-18 says a unit or integration test makes
*none*. A spec that opens a real socket through `ipc/net` satisfies
CS-16 and still crosses: under CS-18 the spec takes a gomock mock of
`ipcnet.IListener`/`IDialer` instead, and only a labelled acceptance suite
reaches the real `HostNetwork`. The `CS-18-CROSSING` locator resolves direct
standard-library and pgx calls, so a real crossing made through an `ipc`
capability is not located by it; that gap is review's, not the rule's. A
direct `net.Listen` in a test outside `ipc` is reported by both
locators, once per rule, because it breaks both.

### Counterweights, written down honestly

1. **pgmem and generated mocks are not crossings.** pgmem is a
   process-local PostgreSQL emulator: SQL is translated for an embedded
   execution engine, with no server, no socket and no pool to anything. A generated gomock mock is a value in the test's own
   memory. Neither is what rule 3 forbids, and neither is reported.
2. **A named, opt-in acceptance suite may cross, outside both layers.** A
   suite whose *subject* is real infrastructure — migrations against the
   PostgreSQL version production runs, a feature pgmem does not yet implement,
   a CLI contract against the built binary — is legitimate if it is labelled
   as such. **The label is a build constraint naming the `acceptance` tag**,
   above the package clause: `//go:build acceptance` (combinations such as
   `//go:build acceptance && linux` count; `!acceptance` does not). The tag
   makes the suite opt-in — `go test ./...` never runs it; a job that wants it
   passes `-tags acceptance` — and makes it visible to the gate, which leaves
   such files out of `CS-18-CROSSING`. An acceptance suite is not the
   package's integration layer and does not satisfy rule 2: the package still
   owes external gomock specs. The label is honest only when the real
   infrastructure is what the suite is about; labelling a test of the
   package's own code to silence the locator is gaming, and eval case
   `041-acceptance-judgment` grades exactly that distinction.
3. **The `integration` build tag is a misnomer under this rule.** At
   introduction the tree's real-infrastructure suites were tagged
   `//go:build integration` (nine files) and `archiveintegration` (one).
   They are opt-in, which is the substance of counterweight 2, but the tag
   claims the integration layer, which rule 2 reserves for the external gomock
   specs. The gate does not accept those tags as the label. All ten were
   retagged `//go:build acceptance` in the change that introduced this rule,
   together with every invocation that selected them: the `-tags` flags in
   `brain-spine-composition.yml` and `copilot-adapter-checks.yml`, and the
   coverage script's tagged shard (`tools/go-coverage.sh`, now
   `candace-acceptance`). From then on "integration" in this repository means
   only the external gomock specs.
4. **Not every package has an exported interface, and not every test needs a
   mock.** A pure package (a parser, a value type) has no dependency to mock;
   its external specs drive the exported API directly. Rule 4 attaches to the
   interface, not to the package. Do not add a meaningless mock or a
   placeholder external file to clear a locator
   (`docs/go_integration_tests.md` makes the same point for the PR notice).
5. **Interfaces that cannot be mocked are out of scope.** A type-set
   constraint (`interface{ ~int | ~string }`) and an empty interface have no
   method set to mock; package `main` exports nothing importable. The
   `CS-18-MOCKGEN` locator skips all three by construction.

### Enforcement: three advisory locators, one per part

Native, in `tools/house_lint/test_layout.ml`, each its own policy ID so a part
can be flipped to mandatory the day it reads zero without waiting for the
others:

| ID | Locates | Anti-gaming built in |
|---|---|---|
| `CS-18-MOCKGEN` | An exported, mockable interface in selected non-test Go that no `//go:generate` mockgen directive covers, or whose directive's `-destination` is not a tracked file carrying the `// Code generated by MockGen. DO NOT EDIT.` header. Source mode (`-source=`, resolved relative to the directive's directory, so `../seams.go` from a consumer counts) covers every interface in that file; package mode (`importpath IFoo,IBar`, resolved through the tracked `go.mod` module table, `.` meaning the directive's own package) covers the named interfaces. | A directive with no committed output, an output without the MockGen header, a plain `// go:generate` comment, and a directive naming `IStoreReader` for `IStore` all leave the interface reported. |
| `CS-18-CROSSING` | In every `_test.go` file — in-package and external — a call resolved through the file's imports to `net.Listen*`/`Dial*`, `net.Dialer`/`ListenConfig` literals, package-level `net/http` `ListenAndServe`/`Serve*`, `httptest.NewServer`/`NewTLSServer`/`NewUnstartedServer`, `os/exec.Command`/`CommandContext`, `pgxpool.New`/`NewWithConfig`, `pgx.Connect`/`ConnectConfig`, `database/sql.Open` with the `pgx`/`postgres` driver, and any import of `testcontainers-go`. Files labelled `//go:build acceptance` are out of scope. | `!acceptance`, a comment that merely says "acceptance", and the `integration` tag do not exempt; aliased imports are followed; `httptest.NewRecorder`, `pgxpool.ParseConfig`, `net.ParseIP` and `sql.Open("pgmem", …)` are not crossings. |
| `CS-18-EXTERNAL` | A package (directory plus package name, excluding `main`) whose selected non-test files declare exported API — a function, method, type, variable or constant — with no `<name>_test` package in the same directory holding a spec (`Test*`/`Example*`/`Fuzz*` function or a Ginkgo `Describe`/`DescribeTable`). Reported once, at the package clause of the first file that exports API. | An external file with no spec, an external package in another directory, and in-package tests alone do not count. |

The mandatory/undefined-usage half of rule 2 — that the external specs
actually exercise misuse and assert defined errors — is not mechanically
decidable and stays with review and the judgment eval. The existing advisory
PR notice (`tools/check_go_integration_tests.py`,
`docs/go_integration_tests.md`) remains the per-PR prompt for changed
packages and additionally looks for `.EXPECT()` calls; `CS-18-EXTERNAL` is the
repository-wide count of the same gap in the house report.

`CS-18-CROSSING` also has a legacy lane in `../scripts/check_style.py` (rule
`CS-18-CROSSING`, report-only), the two-lane shape CS-11 established, so the
tier-1 eval `040-test-crossings` and the census measure the rule the native
report locates. The mockgen and external-package parts are native-only.

**Baseline**, measured 2026-10-01 at `218966c7b` by
`bash tools/check-house-lint.sh --test --summary house-lint-report.md`:
**35 `CS-18-MOCKGEN`**, **231 `CS-18-CROSSING`** and
**60 `CS-18-EXTERNAL`**. The legacy census row at the same commit
records 231 `cs18_findings` (the same 231, with the same per-call breakdown) across 110 test files
(`test_crossing_files`) and 0 labelled acceptance suites. The crossings are
mostly loopback HTTP: 143 `httptest` servers (136 `NewServer`), 35 `os/exec`
subprocesses, 31 `net` listens/dials, 22 PostgreSQL pools (14 `sql.Open` with
the pgx driver, 8 `pgxpool.New`), no testcontainers. Rebased onto
`ab56463c3` (main, the same day) the native report reads 35 / 228 / 60.
After the ten real-infrastructure suites were retagged `acceptance` (see
counterweight 3) and the branch was rebased onto `f181da7d1`, it reads
35 / **219** / 60: the labelled suites left the crossing count, which is the
label working, not a fix. All
three parts are advisory: each is a retrofit backlog, and per this skill's
standing practice a part becomes mandatory only after a fix wave drives it to
zero — never by turning a gate red. The native report owns the current
counts.


## CS-20 — Every compound type is named

The operator, 2026-10-06, verbatim:

> WE'RE NOT GOING TO CREATE A 4TUPLE TYPE WITHOUT NAMING IT

### The rule

A compound value that crosses a boundary — several Go results, an anonymous
struct, an OCaml tuple of three or more elements, an inline CSF field, an
undeclared Datalog relation — carries no name, so no reader can reason about
what it is. The fix is always the same: declare a named type and use it.

1. **A Go function or method returns at most two values**, and only in the
   sanctioned shapes `(value, error)` and `(value, bool)`. Three or more
   results are a compound without a name; declare a struct type and return it.
2. **A Go struct type literal has a name.** A struct written inline — an
   anonymous field type or a local variable — is a compound without a name;
   declare `type Foo struct { ... }` and use `Foo`. The empty `struct{}` is the
   set idiom (`map[K]struct{}`), declares no field, and is not a compound.
3. **An OCaml tuple of arity three or more is a named record.** `(start, stop,
   prose)` names nothing; declare a record with one field per element.
4. **A CSF kind field names its type.** A field written inline as a bare
   `record` or a `[...]` list carries no name; declare the kind and refer to it
   by name.
5. **A Datalog relation is declared before it is read.** A rule body that reads
   a relation no declaration or clause head in the file introduces is a
   relation the reader cannot look up.

### Counterweights, written down honestly

- **`(value, error)` and `(value, bool)` are two results**, and never fire.
- **An OCaml pair is the allowed shape.**
- **Generated files are exempt** — their compounds belong to the generator.

### Enforcement

Advisory (relaxed from mandatory by operator ruling 2026-10-06; see
`tools/house_lint/policy.ml`), in `tools/house_lint/cs20.ml`, five native locators:

- `go_results_3_or_more_unnamed` — a function or method whose result list holds
  three or more types.
- `go_anonymous_struct_type` — a `struct_type` node that is not a declared
  type's body and declares a field.
- `ocaml_tuple_arity_3_or_more` — a text scanner over `.ml`/`.mli` counting
  top-level commas inside parentheses (a pair or a code group never fires).
- `csf_inline_compound_field` — a `.csf` field `:` before `[`, `{`, or a bare
  `record`.
- `datalog_relation_undeclared` — a `.dl` body term with no declaration, clause
  head, or engine built-in.

## CS-21 — Code of one type lives together

The operator, 2026-10-06, verbatim:

> PUT CODE OF SIMILAR TYPES TOGETHER IT MAKES IT EASIER TO REASON ABOUT THE
> RUNTIME

### The rule

Code of one type belongs in one place: a file named for the type, or the file
that declares it. A file named for a role (`options.go`, `metrics.go`,
`errors.go`, ...) collects unrelated types that share only their role, and a
method living away from its receiver's file forces a reader to hold several
files in mind at once.

1. **A file is named for its type.** `<snake(type)>.go`, or a concern split
   still sorted beside it: `<snake(type)>_<concern>.go`.
2. **A role-named file declares at most one type.** `options.go` holding two
   unrelated capabilities' options is the failure the ticket names; give each
   type its own file.
3. **A method lives beside its receiver.** A method whose receiver type is
   declared in another file moves into the receiver's file (or a file named for
   it).

### Counterweights, written down honestly

- **Generated files are exempt only with a declared layout.** A generated
  role-named file proves a generator emitted a layout by role; the generator
  must declare that layout (`Layout:` in its config), and that declaration,
  not the file, is what exempts it.

### Enforcement

Advisory (relaxed from mandatory by operator ruling 2026-10-06; see
`tools/house_lint/policy.ml`), in `tools/house_lint/cs21.ml`, three native locators:

- `role_named_file_mixing_types` — a role-named file declaring two or more
  top-level types.
- `method_outside_its_type_files` — a method whose receiver type is declared in
  another selected file the method's file is not named for.
- `generated_layout_by_role` — a generated role-named file whose generator
  declares no layout.
