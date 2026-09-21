[identifiers genericized for publication - measurements unmodified]

# Review — the page shell under FR-65: `(*App[S]).Document` and `NoRuntime` against the nine pre-registered constraints

| | |
|---|---|
| **Author** | L9-1 (principal engineer) |
| **Date** | 2026-08-05 |
| **Reviewed at** | `679e6695`, the tip of the landing. The artifact is `8680e8c5` (the symbol, the specs, the quickstart), `3c66cc04` (the three example modules and two guide pages) and `679e6695` (the FR-58 census and two number corrections) |
| **Gating against** | [`fr-53-line-budget.md`](fr-53-line-budget.md) **§3.3** — *"these are what I will gate on under FR-65"* — read with **§3.2** (the bug class) and **§3.4** (what "acceptable in principle" does and does not commit me to) |
| **Also binding** | [api-surface.md](../api-surface.md) §0 (FR-65) · [review-checklist.md](../review-checklist.md), §1.4, §1.7, §8.1, §9.7 · PRD §5.I (e) |
| **My writes** | This file only. Nothing in `PRD.md`, `gates/**`, `qa/**`, `pm/**`, `api-surface.md`, `bench/**`, no code, and `fr-53-line-budget.md` untouched |

**Verdict: ACCEPT WITH CONDITIONS.** In the checklist's own vocabulary this is a
**Block on §9.7** — *"docs claim only what is true today"* — cleared by condition
**PS-1** alone; the other two conditions are non-blocking. **The surface is
accepted as designed: two identifiers, this signature, `NoRuntime` as a named
sentinel.** I am asking for no change to the shape of the API, no change to the
call sites, and **no change that can move the count**.

What is blocked is one sentence, made in five places, that the artifact's own
behaviour falsifies: **that no argument to this component can put the inspector
below the runtime.** One can. I ran it, and the bytes are below. The component is
still a large improvement on the eight hand-written shells it replaces and I am
not refusing it for this — but the sentence is the whole of the `Mux`-class
argument the ledger spends an identifier on, and a claim that survives only the
arguments the tests happen to pass is the failure class §3.2 exists to name.

---

## 1. How I checked, and with what

No Go and no node on this host, so everything below ran in `dis-gotth-live:latest`
from `gotth-live/` via `~/bin/dis run bash -c …`. Where I needed behaviour the
committed specs do not exercise, I built a **throwaway probe module** in the
container's `/tmp` with a `replace` onto the mounted worktree, so that nothing
was written into the tree and every probe used only the exported API an
application has. Probe output is quoted verbatim and unabridged.

```
~/bin/dis run go test ./live/...
  ok  github.com/candacelabs/candace/pkg/gotth/live           0.465s
  ok  github.com/candacelabs/candace/pkg/gotth/live/livetest  1.368s

~/bin/dis run bash -c 'cd tools && go run ./apisurface'
  live            56/56  51/51  107/107   (measured/ledger)
  live/livetest   37/37  33/33   70/70
  the surface matches the ledger                       APISURFACE-EXIT=0

~/bin/dis run bash -c 'cd tools && go run ./doccheck'
  every exported symbol in the library carries a doc comment; every example
  checks its output                                    DOCCHECK-EXIT=0
```

`~/bin/dis run bash ci.sh` was also run in full from this review rather than
taken from the commit message; §7 records what it said.

---

## 2. The nine constraints, one row each

Each row is *what I checked*, not what the landing claimed.

| # | Constraint (§3.3, abbreviated) | What I checked | Ruling |
|---:|---|---|---|
| **1** | ≥ 2 real call sites in the landing PR, ≥ 1 not the quickstart | `grep -rn 'app.Document(' --include='*.templ' .` → **five**: `docs/guide/_samples/quickstart/view.templ:29`, `examples/chat/view.templ:147` and `:225`, `examples/dashboard/view.templ:224`, `examples/counter/view.templ:97`. **Four are not the quickstart**, in three modules, and all five pages existed the day before with a hand-written shell — none is an example written to justify the symbol. `grep -rc 'DOCTYPE' --include='*.templ' .` now returns three files (`bench/apps/*/gotth`), plus `test/memory/cmd/memsrv/main.go` in Go: eight shells became four, and the four that remain belong to other owners and were not touched | **PASS**, and by the widest margin any symbol in this library has had. Checklist §1.4 wanted two |
| **2** | Head extension exists, and costs the quickstart nothing when unused | The parameter is variadic, so the counted call passes no head argument at all. Spec `live/document_test.go:136` renders with and without an explicit `templ.NopComponent` and asserts byte equality. I re-counted the counted file myself under the v0.6 rule: **11**, and the shell's invocation is 5 lines where the hand-written one was 13 — §1.1's own arithmetic, reproduced. Three of the four non-quickstart sites carry real head content (viewport, stylesheet, and `examples/dashboard`'s conditional third-party HTMX `<script>`) | **PASS.** The floor is not moved by this parameter, which is what §3.3.2 made the condition |
| **3** | `lang` and `<html>`'s attributes stay the application's | Probe with `htmlAttrs` nil emits `<!doctype html><html><head>` — **no attributes at all, no invented `lang`**. Spec `:98` asserts the same and `:107` asserts escaping and stable order. The renderer is `templ.RenderAttributes`, not a hand-rolled writer; `templ@v0.3.1020/runtime.go:426` — *"Returns the items of the attributes map in key sorted order"* — so one call is one byte sequence. Key **and** value are escaped (`runtime.go:466`); the residual that an attribute *key* containing a space can spell a second attribute is templ's own spread behaviour, identical for the five helpers this package already returns into, and is not new surface | **PASS** |
| **4** | `<title>` is a parameter, never a default | `errNoTitle` (`live/document.go:208`) is returned **before any write**; spec `:84` asserts both the message and that the buffer is empty. Escaping checked with `Bob & "Alice" <live>` at `:74`. *Nit only:* a whitespace-only title is accepted — probe rendered `<title> </title>` with no error. That is "refuse rather than repair" applied consistently and I am not asking for a trim | **PASS** |
| **5** | The `InspectorScript`/`Script` ordering invariant is preserved **or made inexpressible — and the design must say which** | The design says *made inexpressible*. **It is not, quite, and the difference is the whole of §3** below. For every spelling that does not itself emit a runtime tag the invariant now holds by construction, which is a real improvement on all eight prior shells; but a head extension containing `live.Script` puts a runtime tag **above** the inspector, and so does a `Document` nested in a head extension | **PASS on the refusal clause** (this is not a shell that emits `Script` and leaves the app to place the dev tags — that is what §3.3.5 said I would refuse, and it was not built). **FAIL on the claim.** Condition **PS-1** |
| **6** | The runtime tag must be omissible | `examples/chat`'s `LoginPage` is the named consumer and passes `live.NoRuntime`; spec `:215` asserts a complete document with **no `<script` at all**, and `:230` asserts `live.Script(live.NoRuntime)` errors, so the sentinel cannot be smuggled into a real mount. The value is `"no-runtime"`, which has no leading `/`, so every other mount-taking call refuses it — checked against `normalizeMountFor` (`live/templ.go:416`) rather than assumed | **PASS** |
| **7** | `PageHandler`'s buffered-render contract survives | `live/page.go:126`–`:131` is unchanged and still renders into a `bytes.Buffer` before a byte reaches the client. `Document` **strengthens** it: mount and title are validated before the first `io.WriteString`, so a refusal writes zero bytes even without the buffer. Spec `:249` drives a bad mount through `PageHandler` and asserts `500` with no `<html` in the body; spec `:157` asserts a failing head component's error is returned unchanged rather than swallowed. Errors from the three tags and from the children are returned unchanged — read line by line, no `_ =` anywhere in the component | **PASS** |
| **8** | No `any`/`interface{}` in the signature, no accessor-heavy options struct | The signature is four ordinary parameters, no new type, no new struct field, no options struct. `templ.Attributes` **is** `map[string]any` (`templ/runtime.go:422`) and DEV-1 recorded that in the ledger rather than leaving me to find it. I rule it inside the constraint: it is the type this package's five attribute helpers already return (`live/templ.go:90, 101, 148, 183, 249`) and the only argument `templ.RenderAttributes` accepts, so it is the existing vocabulary of the templ integration; `map[string]string` would be a *new* vocabulary that cannot carry a boolean attribute and cannot accept this library's own helpers | **PASS**, on the disclosure. The letter is brushed; the spirit — untyped surface where a typed one was available — is met |
| **9** | An `api-surface.md` row in the same PR, with FR, stability marking and identifier delta | Rows at `:274` (`Document`) and `:275` (`NoRuntime`), both marked **experimental**, both naming their FRs; §0's count moved 54 → 56 and §10 gained a dated changelog section (`:548`). `tools/apisurface` reports **56/56 identifiers, 51/51 fields — the surface matches the ledger**, exit 0 | **PASS.** One citation is wrong and is condition **PS-3**, which does not disturb the row |

**Eight pass. One passes on its refusal clause and fails on its claim.**

---

## 3. Constraint 5, at length, because it is the one the symbol exists for

### 3.1 What the artifact claims, in five places

> **The ordering invariant is not preserved here, it is made inexpressible**:
> there is no argument to this component, and no order of arguments, that
> produces an inspector tag below the runtime tag.
> — `live/document.go:62`–`:64`

The same claim, in the ledger I hold the pen on and did not write here:

- `docs/api-surface.md:272` — the `InspectorScript` row now says that ordering is
  *"a rule this surface enforces rather than documents"*;
- `:274` — the `Document` row says it *"makes the … invariant **inexpressible**
  rather than documented"*;
- `:299`–`:300` — *"there is no argument, and no order of arguments, that puts the
  inspector below the runtime"*;
- `docs/guide/inspector.md:26`–`:31` — *"If your page uses `app.Document`, you are
  already done … nothing to add and nothing to order."*

And the residual, disclosed rather than hidden, at `live/document.go:70`–`:74`
and `api-surface.md:305`: an application that puts its own `Script` in the head
extension gets two runtime tags, which is *"a different mistake with a different
shape."*

### 3.2 What it does

Probe, using only exported API, `Dev: true`:

```go
app.Document("/live", "t", nil, live.Script("/live"))
```

```
<!doctype html><html><head><meta charset="utf-8"><title>t</title>
<script src="/live/gotth-live.min.js" data-gotth-url="/live" defer></script>
<script src="/live/gotth-live-inspector.min.js" defer></script>
<script src="/live/gotth-live.min.js" data-gotth-url="/live" defer></script>
<script src="/live/gotth-live-dev-reload.min.js" data-gotth-dev-url="/live"
        data-gotth-dev-build="74891347b114845d9f88c553" defer></script>
</head><body></body></html>
```

(one line in reality; wrapped here). A second spelling reaches the same place —
`app.Document("/live", "outer", nil, app.Document("/live", "inner", nil))` nests
a whole document in the head and puts *its* runtime tag above the outer
inspector. So this is a class, not a typo.

**That is not "a different mistake with a different shape". It is this mistake.**
`api-surface.md:272`'s invariant is stated in terms of document order for a
reason it states itself: *both are deferred, and the inspector must wrap the
`WebSocket` constructor before the runtime opens a socket.* Deferred scripts run
in document order. In the bytes above the runtime runs **first**, opens its
socket, and only then does the inspector wrap `WebSocket` — the inspector sees
nothing, silently, which is the exact failure §3.2 of my prior note called *"an
inspector that silently shows nothing."* The duplicate tag is a second, separate
problem riding along: `client/runtime.js:1157`'s `boot()` has no idempotence
guard of any kind, so nothing in the runtime treats a second instance as a
no-op.

**Three things I want on the record in DEV-1's favour, because they bound how far
this goes.**

1. **It is not a regression.** Every one of the eight hand-written shells could
   spell this and worse, and four still can. The component removes the bug class
   for every application that does not hand it a runtime tag, which is all five
   call sites in this landing and every application that follows the docs.
2. **It was disclosed, not hidden.** DEV-1 wrote the residual into the godoc and
   the ledger unprompted. What is wrong is the *characterisation* of it, and a
   disclosure that under-rates its own finding is still the behaviour I want.
3. **The design decision it defends is correct.** Making this a method to reach
   `InspectorScript` and `DevReloadScript` is the right call and is the reason
   the symbol is worth its identifier at all. I am not disturbing it.

### 3.3 Why it is nonetheless a condition and not a nit

Because the sentence is load-bearing three times over. It is the ledger's whole
argument for spending an identifier (`§10`, `:552`: *"This makes it inexpressible,
which is `Mux`'s argument rather than `MustNew`'s"*); it is what `api-surface.md:272`
now tells a reader the surface *enforces*; and it is what `guide/inspector.md`
tells an application author they no longer have to think about. A future reviewer
deciding whether this symbol survives to v1.0 will read those sentences and not
this probe. Checklist §9.7 exists for exactly this.

I also note that `guide/inspector.md`'s new paragraph warns about the harmless
duplicate — *"a second inspector"* — and says nothing about the harmful one.

---

## 4. Ruling on each disclosed residual, and on the changed spec

| Residual, as DEV-1 disclosed it | My ruling |
|---|---|
| **Two runtime tags** from a caller who puts `live.Script` in the head extension | **Accepted as a residual, refused as a characterisation.** See §3. Condition **PS-1**. The hole itself stays open — closing it in code is optional, and no *documentation* of it can close it |
| **`NoRuntime` suppresses dev-reload too**; the escape is passing `app.DevReloadScript(mount)` as head content | **ACCEPTED.** The escape is real — I ran it, and it renders the dev-reload tag on a document with no runtime and no inspector — and it is safe on evidence rather than assertion: `DevReloadScript`'s godoc has carried *"Order does not matter here … this tag may go anywhere"* since `7cff113a`, the commit that created the tag. **But the escape has no spec**, and a documented escape hatch with no spec is a hatch that closes silently. Condition **PS-2** |
| **`<body>` takes no attributes** | **ACCEPTED.** I checked every remaining hand-written `<body>` in the tree — `bench/apps/{counter,chat,dashboard}/gotth/view.templ` and `test/memory/cmd/memsrv/main.go:322` — and all four are bare, so this blocks nobody today. The symbol is marked `experimental`, so the repair when somebody needs it is a signature change that is permitted rather than a second method. DEV-1's godoc already says the argument must be made rather than worked around, which is the right instruction |
| **`templ.Attributes` is `map[string]any` underneath** | **ACCEPTED**, on the disclosure, for the reasons in constraint 8's row. Recording it in the ledger against the constraint it brushes is precisely the behaviour that makes a disclosure worth having |

### The changed `examples/counter` spec — a test following a documented indifference

**Ruling: correct, and the replacement is strictly stronger than what it
replaced.** This is a spec that was asserting an incidental of the hand-written
order, not a contract.

The evidence is `DevReloadScript`'s own godoc, and it is dispositive: *"Unlike
`(*App[S]).InspectorScript`, this tag may go anywhere: it wraps nothing, reads
nothing the runtime owns, and talks only to its own route over HTTP … only the
inspector's position in it is load-bearing."* I checked that this predates the
change rather than accompanying it —
`git log -S"Order does not matter here" -- live/devreload.go` returns exactly one
commit, `7cff113a`, the FR-57 landing, and `git merge-base --is-ancestor
7cff113a 8680e8c5` confirms it. The old assertion (dev-reload above the runtime)
was therefore never a contract; the new one (inspector above the runtime) is the
only ordering the library has ever claimed.

And the spec did not shrink to fit: it **gained** an assertion that the inspector
tag is present with `Dev` true, and another that it is absent with `Dev` false.
A test bent to fit new code loses coverage. This one added it.

---

## 5. FR-65 minimality — is `+2` the smallest surface that buys this?

**Yes, and I have looked at the alternatives rather than asserting it.**

**Could `Document` have been smaller?** Not without failing my own constraints.
Drop `title` and let the application render its own into the head extension —
that is constraint 4, which requires it be a parameter. Drop `htmlAttrs` and the
component emits a bare `<html>` that no application can ever put `lang` on — that
is worse than constraint 3's hardcoded default, because it removes the
application's ability rather than guessing for it. Make `head` a single
`templ.Component` instead of variadic — same identifier count, and every page
without head content pays a `nil`. **Four parameters, no new type, no new struct
field, no options struct: this is the floor for what the component must know.**

**Could it have been a package-level function, +1 identifier?** No, and this is
the one place the artifact's argument is exactly right: a package-level
`live.Document` can reach `Script` but not `InspectorScript` or
`DevReloadScript`, so it would emit the runtime and leave the application to
place the inspector *relative to a tag it can no longer see*. §3.3.5 pre-registered
that shape as a refusal. It was not built.

**`NoRuntime` as a sentinel const — the judgement call, made.** **I accept it.**
The alternatives are each worse in a way this project has already ruled on:

- *Empty string means no runtime.* This is the absence-as-intent spelling the
  library refuses everywhere — `normalizeMountFor` already rejects `""` with a
  sentence about the author forgetting, and a page that loads perfectly and does
  nothing is the silent failure `Script` refuses a default to prevent.
- *A `bool` parameter* would be **+1 identifier instead of +2**, so it is
  genuinely smaller by FR-65's metric — and I refuse it anyway. It puts a bare
  `false` at four of the five call sites, which is unreadable and ungreppable,
  and it is the exact trade this library has already made four times in the other
  direction: `AnyOrigin`, `Anonymous`, `AllowAll` and `NoCSRFCheck` are all one
  identifier spent so that an opt-out is *a thing somebody wrote down*. Being the
  fifth instance of a four-instance precedent is worth one identifier.
- *A second method* (`StaticDocument`) is also +1 identifier and leaves two
  bodies to keep in sync.

**And the honest note:** constraint 6 — *"the runtime tag must be omissible"* — is
mine. `NoRuntime` exists because I required it. If a future pass wants the
identifier back, the thing to reopen is my constraint, not DEV-1's spelling of it.

---

## 6. The count, which I do not grade

**I re-derived it and I grade nothing.** QA-1 re-counts and grades box 2; PM-1
fires trigger 1.

Two independent paths, both v0.6's frozen `awk`, the same two paths §1.1 of my
prior note established:

```
docs/guide/_samples/quickstart/main.go       -> 20
docs/guide/_samples/quickstart/view.templ    -> 11
docs/quickstart.md, the Go fenced block      -> 20
docs/quickstart.md, the templ fenced block   -> 11
```

**20 + 11 = 31, off two artifacts that share no line range and no fence.** The
templ half moved 19 → 11 because the 13-line hand-written shell became a 5-line
invocation: exactly the arithmetic pre-registered at §1.2 as *"a costing of a
component that does not exist"*. It exists now and it costs what it was costed at.

**Does anything here make 31 depend on something I would refuse? No — and I
looked hard, because a number that arrives exactly on its target is the number to
be most suspicious of.** Specifically:

1. **The route back to the `App` is genuinely free.** `app :=` inside `main`
   became `var app =` above it: 20 counted lines either way, which I verified by
   running the rule over both revisions of the file rather than by reading the
   diff. And the sanction predates the landing — `MustNew`'s godoc has said *"It
   is for main and for **package-level initialisation**"* since `MustNew` shipped
   (`live/app.go:118`). Nor is the count hostage to it: an inline closure
   (`app.PageHandler(func(s State) templ.Component { return Page(app, s) })`)
   also counts 20. **The number does not rest on the package-level var.**
2. **Constraint 2 held, which is where the floor could have moved.** The head
   extension is variadic and costs the counted app nothing; had it cost a line I
   would be reporting a floor of 32 here, as §3.4 said I would.
3. **Nothing was removed from the document to buy a line.** The built quickstart
   emits the same bytes as the hand-written shell — I checked this against the
   *old generated file* rather than against the claim:
   `git show 8680e8c5^:…/view_templ.go` writes
   `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>gotth-live quickstart</title>`,
   which is byte-for-byte what `Document` emits for the same arguments. The page
   did not get smaller; the source did.

**Two observations for whoever re-counts, offered as facts and not as grades.**

- **The whole margin is one unwrapped line.** `view.templ:29` is 84 columns and
  carries four arguments; the file's comments wrap at 79. `gen.sh` runs `templ
  generate` and no `templ fmt --check`, so nothing in CI will ever reformat it —
  but a human who wraps that call makes the count 32.
- **31 is the one arrival at which neither half of the repaired trigger fires.**
  Trigger 1's down-branch needs a floor *below* 31; trigger 4 needs the app
  *below* the budget; a floor *above* 31 withdraws the amendment. At exactly 31
  the budget does not move in either direction, which is the single arithmetic
  point where the amendment is neither tightened nor withdrawn. That is not
  evidence of shopping — I traced the route above and it is the pre-registered
  one — but it is the arrival a grader should re-derive rather than accept.

**And the prerequisite I made blocking is satisfied.** §6.4 of my prior note said
L9-1-C2 had to be *in force before* DEV-1's shell, not with it. It is:
`docs/PRD.md:1222` now reads *"Above 31: the budget does not move up, at any
cost … the amendment is withdrawn and re-argued"*, and that text landed at
`667d3db7`, an ancestor of `8680e8c5`. **The shell landed under the repaired
trigger.** I record that because if it had not, my countersignature would have
been the thing making a re-baseline available.

**None of the three conditions below can move the count.** PS-1's code option
adds no exported identifier and touches no counted line; PS-2 adds a spec; PS-3
edits a citation.

---

## 7. `ci.sh`, held to its own claim

Run from this review as `~/bin/dis run bash ci.sh`, not taken from the commit
message. **`CI-EXIT=0`**, verdict line *"every gate this invocation could run is
green"*. `gofmt`, `vet` and `staticcheck` clean; the race-detector step green
across every package including `live`, `internal/arch` and
`test/internal/conformance`; all three example modules green
(`examples/{counter,chat,dashboard}`), and `docs/guide/_samples` green — which is
the pin suite that holds the fenced blocks in `docs/quickstart.md` and
`README.md` to the compiled sample, and therefore the reason the two counting
paths in §6 are measurements of the same shipping bytes rather than of a stale
copy. The two gates that are mine ran separately and are quoted in §1: **FR-65
exact, 56/56 and 51/51**, and **FR-66/FR-68 clean**. Five steps skip inside the
container for want of a nested docker daemon and of node — FR-7 `gen.sh --check`,
the NFR-4 client suite, the bench harness suite, the browser conformance specs
and G11 — which is the pre-existing D-19/D-20 arrangement and not this landing's
doing; DEV-1 reports having run three of them from the host, and I did not re-run
those three.
**The FR-58 census is real, not decorative** — `internal/arch/errors_test.go`
moved `live` 38 → 39 in the same commit as the grading, `docs/error-audit.md`
§3.3.2 grades the one new error against all three clauses with `S` and `C`
recorded `n/a (page request)` for the same reason §3.3.1 gives, and the audit
explains where `Document`'s *mount-path* refusals are graded instead of
pretending they are new. `internal/arch` is green under `-race`, which is the
check that would have failed had 39 been wrong.

Testing conventions, checklist §8.1: **18 specs in `live/document_test.go`, all
Ginkgo v2 + Gomega**, no table-driven stdlib test, no `gomock` — correctly, since
there is no interface here to mock. The example modules' new helpers build real
`*live.App` values for the same reason. **Conforming.**

---

## 8. Things nobody routed to me

1. **`NoRuntime`'s ledger row cites NFR-5**, which is *"No npm at runtime, no
   build step imposed"* (`PRD.md:853`) — about the consumer needing no node,
   bundler or lockfile. NFR-5 is satisfied identically whether or not this symbol
   exists. Condition **PS-3**; §0's rule that a symbol's FR column is what keeps
   it honest is the reason this is worth a line at all.
2. **`README.md:93` says the library took "three pieces of the ceremony" and then
   names four** (`MustNew`, `App.Mux`, `App.PageHandler`, optional `Config.Init`).
   This predates the landing — it is in the file at `8680e8c5^` — but DEV-1
   corrected *eleven → twelve* in the same sentence, which I verified is right (the
   seven `Config` fields occupy 12 counted lines, six of them `Reduce`'s body), and
   left the three. **nit:** while you are in that sentence.
3. **A whitespace-only `<title>` is accepted.** `Document("/live", " ", nil)`
   renders `<title> </title>` and returns nil. **nit**, and arguably correct:
   trimming would be a repair, and this library refuses rather than repairs.
4. **There is an empty, root-owned directory tree at
   `gotth-live/gotth-live/{docs/guide/_samples,examples/chat,tools}`** in this
   worktree — no files, untracked, `mtime` 2026-08-04, so it predates these three
   commits. It is what a container command run with a doubled `gotth-live/` path
   prefix leaves behind (the image's working directory is already the module
   root). Harmless to every build I ran, but it is owned by `root` inside a
   worktree owned by the unprivileged checkout user, so a later `git clean -fd`
   will fail on it. **Routed
   to the orchestrator as housekeeping**, not to DEV-1.
5. **`docs/qa/fr-53-timed-r2.md` was present and untracked while this review
   ran**, and landed at `dab16364` between the last check above and this file's
   commit. That is QA-1's work and I read nothing into it, reviewed nothing
   against it, and did not touch it. Noted so that a reader who finds it tracked
   at `HEAD` knows it was not part of what I gated.

---

## 9. Conditions

| # | Condition | What discharges it | Owner | Blocking |
|---:|---|---|---|---|
| **PS-1** | **The inexpressibility claim is false as written and must be made true or made accurate.** A head extension containing a runtime tag — `live.Script`, or a nested `Document` — renders that tag **above** the inspector, which is the ordering failure `api-surface.md:272` describes and not merely a duplicate tag. Sites: `live/document.go:62`–`:64` and `:70`–`:74`; `docs/api-surface.md:272`, `:274`, `:299`–`:300`, `:305`, and §10's row at `:552`; `docs/guide/inspector.md:26`–`:31` | **Either (a)** `Document` refuses a runtime tag rendered from its head extension — a context marker set around the head loop and read by `Script`, no new exported identifier, turning a silently blind inspector into `PageHandler`'s 500 with a named reason — **or (b)** all seven sites are restated to say precisely what remains expressible, that it is the ordering failure itself, and that the guide's *"nothing to order"* holds only for a head extension that emits no runtime tag. **Either way a Ginkgo spec pins the actual byte order for the `live.Script`-in-head case**, so the claim cannot drift back. (a) is what I would take, and my §3.3.5 said I would not prescribe the resolution, so (b) discharges it | **DEV-1** | **Yes** — checklist §9.7 |
| **PS-2** | **The documented `NoRuntime` escape has no spec.** `NoRuntime`'s godoc (`live/document.go:30`–`:33`) names `(*App).DevReloadScript(mountPath)` as head content as the way to keep dev-reload on a non-live page. I ran it and it works. Nothing asserts it | One `It` in `live/document_test.go`: `Document(NoRuntime, title, nil, app.DevReloadScript(mount))` with `Dev` true renders the dev-reload tag and still no runtime and no inspector | **DEV-1** | No |
| **PS-3** | **`NoRuntime`'s ledger row cites NFR-5**, which is about npm and the embedded `<script src>` and is satisfied identically with or without this symbol | Drop the citation or replace it with the requirement the symbol actually serves. FR-53 and §3.3 constraint 6, both already in the row, carry it | **DEV-1** | No |

**Not conditions, recorded so nobody treats them as such:** the `<body>`
attribute residual, the `templ.Attributes` element type, the whitespace title,
and the `README` "three pieces". All are accepted or are nits.

---

## 10. What this review does not do

I have **graded no Phase 4 box**, moved no number, and written into no file but
this one. The count of record is QA-1's and the trigger is PM-1's; §6 above
reports 31 because I re-derived it and because §3.4 obliged me to say so if the
floor had moved, and it reports it as a measurement rather than as a pass. I have
not touched `fr-53-line-budget.md`, which three documents cite at a fixed shape;
this file is the gate that note pre-registered, and it stands beside it rather
than inside it.

**On my own §3.4, in case it is quoted back at me:** I have not refused this on
the ground that a library should not own a page shell. I do not think that, I
said so before the artifact existed, and the artifact is better than the eight
shells it replaces. The one thing I am blocking is a sentence about what the
artifact guarantees, which is §3.3 constraint 5 applied exactly as it was
written — *preserved or made inexpressible, and the design must say which* — to a
design that says the second and delivers something narrower.

— L9-1, 2026-08-05, against `679e6695`

---

# 11. Verification of the discharge — 2026-08-05, against `8be955e5`

| | |
|---|---|
| **Reviewed at** | `8be955e5`, the tip of the discharge. `cbad05d8` (the refusal, the specs, six prose sites, the census), `e7d47de6` (`quickstart.md` §3), `8be955e5` (`gofmt` on the spec file) |
| **Verdict** | **ACCEPT.** PS-1, PS-2 and PS-3 all **DISCHARGED**. No condition stands. The Block on §9.7 is cleared |

**PS-1 was discharged by route (a) — the claim was made true in code — and I
accept the reasoning for taking it over route (b).** DEV-1's ground is better
than the one I gave: I said (a) was what I would take; they said (b) would have
discharged the condition by demoting the symbol's justification to
*"inexpressible unless you hand it a runtime tag"*, which is close to no claim at
all, and the ledger at §10 is what makes that justification load-bearing. That is
the correct reading of why the sentence mattered, and it is a better argument
than mine.

## 11.1 PS-1 — DISCHARGED

**The mechanism, read rather than taken.** `Document` derives `headCtx` from
`ctx` and marks it **only** when `withRuntime`; the head loop renders under
`headCtx`; the three tags and the children render under the unmarked `ctx` —
which is necessary, because one of the three *is* `Script`. The key is an
unexported empty struct in `live/document.go`, set in one place and read in one
place. `Script` checks the mark **before** the mount path, which is the right
order: a caller who has made both mistakes should be told the structural one.

**My own probes, rebuilt from scratch against the repaired tree**, in a
throwaway module in the container's `/tmp` with a `replace` onto the worktree,
using only exported API:

| Probe | Result |
|---|---|
| **P1** — `Document("/live","t",nil, live.Script("/live"))`, my original falsifying case | **Refused.** Error names the component, the mechanism and `live.NoRuntime`; the buffer holds `…<title>t</title>` and **no runtime tag** |
| **P2** — a whole `Document` nested in the head | **Refused**, same error, after the inner document's inspector tag and before any runtime tag |
| **P3** — the ordinary dev document | `<!doctype html><html><head><meta charset="utf-8"><title>t</title>` + **inspector, runtime, dev-reload** — **byte-identical to what I captured before the repair**. The fix is inert on every correct page |
| **P4** — `Script` **one level down**, inside a wrapper component that renders it | **Refused.** DEV-1 did not enumerate this one; it works because context values inherit, so the mark reaches any depth of composition. This is the case that makes the refusal a property rather than a special case |
| **P5** — a **hand-written** `<script src="/live/gotth-live.min.js" …>` in head content, no API call | **Renders**, above the inspector. See the ruling below |
| **P6** — a `NoRuntime` document nested in a live head (emits no `Script`) | **No error.** The refusal is not over-broad: it fires on a runtime tag, not on a nested document |

**The specs, mutated rather than read.** PS-1 required a spec that pins the byte
order, and a spec that pins bytes is worth exactly what its ability to fail is
worth. I copied `go.mod`, `go.sum`, `live/`, `internal/` and `proto/` into
`/tmp/mutant` inside the container — **no write of any kind into the worktree** —
confirmed the copy green, then broke the component seven ways and ran
`go test ./live/ -count=1` against each:

| # | Mutation | Result |
|---:|---|---|
| **M1** | the mark is never set (`headCtx = ctx`) | **KILLED** — 3 specs, incl. *"is refused, so a blind inspector becomes an error instead"* |
| **M2** | `Script` emitted **before** `InspectorScript` | **KILLED** — *"pins the head's byte order in dev: charset, title, inspector, runtime, dev-reload"* |
| **M3** | head content rendered **below** the three tags | **KILLED** — 2 specs |
| **M4** | the mark set only when `Config.Dev` | **KILLED** — *"is refused in production too, where there is no inspector to blind"* |
| **M5** | the mark widened to the **children** | **KILLED** — *"does not refuse live.Script anywhere else"* |
| **M6** | the mark set even for `NoRuntime` | **KILLED** — *"lets a page that declared itself not live place its own runtime tag"* |
| **M7** | head content dropped on a `NoRuntime` page | **KILLED** — 3 specs, incl. PS-2's *"still lets the dev-reload tag be placed by hand, which its godoc promises"* |

**Seven mutants, seven killed**, each by the spec that owns that behaviour and —
in every case — by *only* those specs, out of 274 in the package. That is a
targeted suite rather than a broad one, and M2 in particular is the mutant the
**old** assertion would have survived: three `strings.Index` comparisons pass on
any document that contains the three substrings somewhere. **The spec now fails
when the bytes move, and the two deliberate boundaries are pinned by specs rather
than described** — M5 and M6 are those boundaries, and both are caught, which
means nobody can move the line without moving a spec.

**One residual I found, and it is a nit rather than a condition — P5.** A head
component that **hand-writes** `<script src="/live/gotth-live.min.js" …>` still
lands above the inspector, so the absolute sentence *"no argument to this
component, in any order"* is still, strictly, falsifiable. **I am not reopening
anything on it, and the distinction is principled rather than tired:**

- The spelling I blocked was **`live.Script` — an exported function of this
  library that the guide told authors to place.** The claim was false about the
  library's own vocabulary, which is the only place a compositional guarantee can
  live.
- The spelling that remains is **not in the library's vocabulary at all.** It
  requires hand-typing the minified asset's URL, and it is the same category as
  hand-writing `data-gotth-region` instead of `live.Region` — no guarantee in
  this library, or in any templ library, survives an author who writes the
  library's private output by hand. If the sentence had to survive *that*, no
  compositional claim could ever be made.
- **And closing it is a change I would refuse.** The only way is to buffer every
  head component's output and scan it for the runtime's URL — a cost on every
  page render, and a second, quieter parser of exactly the kind
  `normalizeMount`'s own godoc declines to become.

**nit:** one word of scope in the godoc and at `api-surface.md:299` would make
the sentence exactly true — *"no **call to this package's API**, in any order,
can put the inspector below a runtime tag."* Worth taking at the next touch of
either file. **Not a condition, no spec needed, and it does not gate anything.**

## 11.2 PS-2 — DISCHARGED

The escape has a spec (`live/document_test.go:331`), and it asserts the whole of
it: the dev-reload tag present with its build identity, **no** runtime, **no**
inspector, children in the body. Its converse is specced too — a `NoRuntime`
page may place its own runtime tag — which I did not ask for and which is what
makes the boundary legible. **M7 killed the escape spec** and **M6 killed its
converse**, so both have demonstrated failure ability rather than assumed it.

## 11.3 PS-3 — DISCHARGED

`NoRuntime`'s live row at `api-surface.md:275` now cites **FR-53, FR-61**.
`FR-61` is *"Chat (Phase 2)"* at `PRD.md:1620`, and `examples/chat`'s `LoginPage`
is the symbol's named consumer — so the citation now points at the requirement
the symbol actually serves. The **dated §10 row still cites NFR-5 and is left
standing**, which is the correct half of this project's rule.

## 11.4 The three boundaries — my rulings

**1. `Script` among the children still renders. This is where I want the line,
and DEV-1's reason is sound. AFFIRMED.** Four grounds, the first of which is the
one I would defend:

- **The failure being refused is the quiet one; the failure left open is
  visible.** A blind inspector is invisible: the page loads, every tag is there,
  nothing logs. Two runtime tags in the body are two `<script>` elements a
  developer sees in view-source, and the inspector — if it is on at all — works
  and shows both sockets. That is precisely the criterion §2(c) of my
  countersignature used to let `Config.Init` shrink while refusing the security
  bundle: **the one shrink this project took was taken because its failure is
  loud.** The same test decides this boundary the same way.
- **The invariant this symbol owns is *ordering*, and in the body the ordering
  holds.** Refusing there would be refusing a different defect under the
  ordering rule's authority — which is how a rule stops meaning anything.
- **A refusal keyed on an invisible context value should be exactly as wide as
  the failure it prevents.** That is DEV-1's argument and I endorse it: action at
  a distance is tolerable in proportion to how tightly it is scoped, and widening
  it buys no guarantee while making the surprise larger.
- **The over-refusal would be concrete**, not theoretical: a body-wide mark also
  refuses a `Document` composed into the children, whose error would be answering
  a question nobody asked.

It is now pinned by M5, so moving this line requires moving a spec — which is the
right amount of friction for a boundary that is a judgement rather than a fact.

**2. `NoRuntime` sets no mark. AFFIRMED, and it is the only defensible
choice.** With `NoRuntime` there is no inspector on the page, so there is nothing
for a hand-placed runtime tag to be misordered *against*; refusing it would break
a page that works, for a rule that does not apply to it. Pinned by M6.

**3. The refusal is keyed on a context value — a new failure-condition shape for
this module. ACCEPTED, with its blast radius audited rather than asserted.** What
makes it tolerable is that all four of these are true and I checked each: the key
is an unexported empty struct, so no other package can name it; it is set in
exactly one place and read in exactly one; **a context that has never been
through `Document`'s head content cannot carry it** — verified by probe (`Script`
outside any `Document` renders, P3 and the spec at `:289`) and empirically by the
four hand-written shells' modules still passing in `ci.sh`; and the failure it
produces is `PageHandler`'s buffered 500 with a named reason rather than
anything silent.

**And `error-audit.md` revision 5 does the thing I would otherwise have had to
ask for.** It records that this is the first graded error in the published module
whose *condition is a context value rather than an argument*, and says **§6
should be read as gaining a fifth weakness, because nothing in that document's
method would have found it by reading a call site.** A document disclosing the
limits of its own method, unprompted, is the behaviour that makes the rest of its
grades worth reading. The census moved `live` 39 → 40 in the same commit and the
new error is graded against all three clauses; `internal/arch` is green under
`-race`, which is the check that would have failed had 40 been wrong.

## 11.5 The documentation treatment — correct, and it volunteers more than I asked

**The treatment is the one this project uses.** §10's dated row for the original
landing is **not** rewritten: it still says *"makes it inexpressible"* and still
cites NFR-5, as the record of what was claimed that day. A new dated §10 section
sits **above** it, quotes my probe, and says plainly that the row beneath was
false when written. That is the rule PM-1 applied to their own three v1.0
sentences and that I applied to my own `live.LocalDevelopment` mis-citation, and
it is applied here in the same shape.

**I checked the ledger diff for quiet repairs and found none.** Three hunks:
four §5.2 rows, the §5.2 prose block, and the new §10 section. Nothing else in
`api-surface.md` moved.

**And one row is there because DEV-1 went looking for what their own correction
implied.** The original landing's *"Nothing existing changes meaning — `Script`
… untouched"* is no longer true of this one: **an exported symbol gained a
failure mode.** The new section says so, against checklist §1.7 and BL-30, and
`Script`'s own §5.2 row now names the refusal. I did not ask for that and would
have had to.

`docs/guide/inspector.md` now names both mistakes in increasing order of what
they cost, where before it warned about the harmless duplicate and was silent on
the harmful one. `docs/quickstart.md` §3's bullet list said the head extension
may carry *"your own `<script>`"*, which read as permission for the exact call
that is now an error; it names the boundary and points at the guide, in prose,
outside the counted blocks.

## 11.6 The count gate — ruling

**My `templ fmt --check` nit is withdrawn, on DEV-1's measurement rather than on
their argument.** `templ fmt` is idempotent on the 84-column line *and* on a
hand-wrapped four-line version of the same call that counts 15 templ lines — so
the gate would be green in both states and protects nothing. That is the right
way to kill a reviewer's suggestion: measure it.

**Should a gate on the count itself exist? Yes.** FR-53's line clause is the only
requirement in this project measured in lines, and it is presently protected by
nobody — it is re-counted by hand at each gate, by whoever is holding it. That is
the exact shape `ci.sh`'s own header condemns: *a requirement whose gate is a
tool nobody runs is a requirement in name only.*

**DEV-1 was right not to write it, and there is a second reason they did not
give.** Theirs — that adding a gate on a requirement you are measured by is not
yours to do unilaterally — is sound. The stronger one is that **the gate needs a
number, and the number is the budget, which is PM-1's under §5.I.** A gate
written by the party measured by it, encoding a budget it does not own, is the
self-dealing shape this project has already had to disclose once.

**Routing: PM-1 authorises the number and names its source of truth; DEV-1
implements it in `ci.sh` over v0.6's rule and the two sample files; QA-1 verifies
it fails when the count exceeds the budget. It must not land in the same PR as a
change to the count.** This is a **recommendation and deliberately not a
condition of this review** — QA-1 is grading FR-53's box as I write, and
inserting a new gate into that critical path would be me adding scope to a
grading window, which is the thing I would refuse from anybody else.

## 11.7 The count, and `ci.sh`

**The count did not move, and I re-derived it rather than accepting the report:**

```
docs/guide/_samples/quickstart/main.go     -> 20      docs/quickstart.md Go block    -> 20
docs/guide/_samples/quickstart/view.templ  -> 11      docs/quickstart.md templ block -> 11
```

**20 + 11 = 31**, unchanged on both paths. Nothing in the discharge touches a
counted line — a context key, an error value, prose and specs.
`tools/apisurface` reports **56/56 identifiers and 51/51 fields**: the repair
buys the guarantee at **+0 exported surface**, which is what makes route (a)
cheaper than the sentence it replaced.

`~/bin/dis run bash ci.sh` re-run in full from this review: **`CI-EXIT=0`**, the
same five container-scoped skips as before.

**On the `gofmt` failure in the run before this one:** the trap DEV-1 names is
real — `gofmt -l` reports by printing, not by exiting, so `gofmt -l live/ && echo
OK` cannot fail — but it was in an uncommitted pre-flight, not in the repository.
Every committed use gets it right and captures the output instead
(`ci.sh:168`, `ci.sh:393`, `gen.sh:331`), and `ci.sh:115` records having been
bitten by a related version of this before. **The correct reading is that the
gate worked**: a step caught a formatting defect that a hand-rolled check had
waved through, which is the entire argument for having the step. Nothing to
route.

## 11.8 Final verdict

**ACCEPT.** All three conditions discharged; no condition stands; nothing is
blocked. The surface is unchanged at 56 identifiers, the count is unchanged at
31, and `(*App[S]).Document`'s central claim — the one the ledger spends an
identifier on — is now **true of every call this library's API can express**,
enforced in code, and pinned by a spec suite I broke seven ways and could not get
past.

Box 2 remains QA-1's to grade and trigger 1 remains PM-1's to fire. **I have
graded nothing here either.**

— L9-1, 2026-08-05, against `8be955e5`
