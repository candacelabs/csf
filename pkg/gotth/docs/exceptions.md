# Named exceptions to the functional-discipline rules — gotth-live

**FR-20 — Named exceptions only.** *"Any deviation from FR-14/16/18 MUST be
recorded in `gotth-live/docs/exceptions.md` with the reason, the blast radius,
and an L9-1 sign-off line. Unlisted deviations are merge blockers."*
`Phase: 1 onward` `Gate: L9-1`

**Drafted by DEV-1**, 2026-08-05, against the tree at `2ab0cd57`.
**Reviewed, re-walked and signed by L9-1**, 2026-08-05, against the tree at
`29348a5a`. §7 carries the rulings, the corrections L9-1 made before signing, and
what a Phase-5 re-walk should expect.

| Row | Disposition | Signed |
|---|---|---|
| **E-1** — `test/memory`'s render writes to a shared stack probe | **ACCEPTED as a named exception.** The scope ruling DEV-1 offered — `test/memory` out of FR-20 entirely — is **REFUSED**, on this project's own precedent. §7.1 | **L9-1, 2026-08-05** |
| **E-2** — the guide sample's reducer logged | **CLOSED — FIXED at `091dbae8`**, verified in the tree by L9-1, not accepted as a deviation. The row is **retained as closed history**; §4's "then delete this row" is **overturned**. §7.2 | **L9-1, 2026-08-05** |
| **§3's six readings** | **AGREED**, with two extensions L9-1 made rather than asked for. §7.3 | **L9-1, 2026-08-05** |

**What this signature is on, and what it is not.** It is on the register as it
stands against `29348a5a`: two deviations, one accepted and one closed, and a
walk that reaches the whole tree. It is **not** a tick of PRD §9's box 13, which
by its own text cannot tick before Phase 5 — see §0 and §7.5.

---

## 0. Why this file did not exist until today, and what that means

FR-20 has been in force since Phase 1. `docs/gates/phase-4.md` §4.13 records the
consequence in one line — *"`ls gotth-live/docs/exceptions.md` → no such file"* —
and states the two readings it left open:

> Either there has never been a deviation — in which case the file should exist
> and say so, because "no exceptions" is a claim somebody has to sign, and an
> absent file makes it unfalsifiable — or there has been one and it went
> unrecorded, which the requirement itself calls a merge blocker. **Nothing in
> the tree distinguishes the two, and that is the finding.**

The walk in §1 distinguishes them. **The second reading is the true one.** Two
deviations exist in the tree today and neither was recorded; both are below, as
E-1 and E-2. Both are **merge blockers by FR-20's own text**, and this document
is where they stop being unlisted — not where they stop being deviations. §4
says what each one needs and who owns it.

**What this document does not close.** PRD §9's Phase 4 entry for this box says
it *"cannot tick before Phase 5, where FR-20 also feeds the stdlib-grade PR
criteria."* That is a statement about the box, and this file does not change it.
What exists now is a drafted, walked, unsigned register. It becomes a signed one
when L9-1 signs it, and the box becomes tickable when Phase 5's own use of FR-20
is satisfied as well. **Nothing here should be read as ticking anything.**

> **L9-1, 2026-08-05.** It is a signed register now, and the second half of that
> paragraph is unchanged by the signature: **the box still does not tick.** One
> of the two deviations was fixed rather than accepted — E-2, at `091dbae8` —
> so the sentence above that says two exist is true of `2ab0cd57` and not of
> `29348a5a`. **One live accepted deviation remains, E-1.** §7 is the disposition
> of every row, and §7.5 answers the question this paragraph raises next: whether
> the register would survive being re-walked at Phase 5 or would have to be
> rebuilt.

---

## 1. The walk — the method, so "no deviations found" can be checked

FR-20's subject is the three functional-discipline rules, quoted from PRD §5.B:

- **FR-14 — Pure reducer.** *"Application state transitions MUST be expressed as
  a pure function `(state, event) → (state, []Effect)`. Reducers MUST NOT
  perform I/O, read clocks, read randomness, or mutate the input state."*
- **FR-16 — Effects at the actor boundary only.** *"All I/O — DB, HTTP, timers,
  pubsub, **logging of application data** — MUST be executed by the session
  actor after the reducer returns, never inside it."*
- **FR-18 — Pure render.** *"`render(state) → fragments` MUST be a pure function
  of state. Renders MUST NOT mutate state or perform I/O."*

### 1.1 What was walked

Everything in the repository that is a gotth-live *application* or that
implements the reducer/render contracts, which is wider than the published
module: FR-20 says "any deviation", and a sample a reader copies is a place a
deviation does the most damage.

| Tree | What it holds | Reducers | Renders |
|---|---|---|---|
| `live/`, `internal/` | the library itself | the adapter and the actor that call them | the renderer that drives them |
| `examples/counter`, `examples/chat`, `examples/dashboard` | the three shipped examples | 3 | 8 fragments |
| `docs/guide/_samples/*` | the compiled sources behind the guide pages | 9 | 9 fragments |
| `bench/apps/{counter,chat,dashboard}/gotth` | the Phase 5 comparison applications | 3 | 11 fragments |
| `test/memory/cmd/memsrv`, `test/internal/chaos/cmd/chaossrv` | measurement and chaos harness applications | 2 | 3 fragments |

*(Counts are at `29348a5a`. The guide-samples row was 8 and 8 at the drafted
tree; `docs/guide/_samples/architecture/` landed at `22a47a6b` in between.
**17 and 31** is the total, and §1.2's correction says why the draft's 16 and 30
were right for the tree they were taken against.)*

**The scope this table asserts is ratified, and it is wider than the published
module on purpose.** FR-20 says "any deviation", and the trees that are not
shipped are exactly the trees where a deviation is cheapest to leave in place.
**L9-1 ratifies it as the reading of FR-20 this project uses**; §7.4 routes the
requirement text to PM-1, because a scope this file asserts is weaker than a
scope FR-20 states.

### 1.2 How, exactly

1. **Enumerate every reducer and every render.** Every `Reduce` field or
   function assigned to a `live.Config`, and every `Fragment.Render`:

   ```bash
   # reducers. The pattern matches a declaration AND its `Reduce: Reduce`
   # wiring, so hits exceed reducers; the second form counts each reducer once.
   grep -rn 'Reduce: *func\|Reduce:\|^func Reduce' --include='*.go' \
     live/ internal/ examples/ docs/guide/_samples/ bench/apps/ test/ | grep -v '_test.go'
   grep -rn 'Reduce: *func\|^func Reduce' --include='*.go' \
     examples/ docs/guide/_samples/ bench/apps/ test/ | grep -v '_test.go'
   # renders. NOT 'Render: *func': a Fragment may name a function instead of
   # writing a literal, and one does. See the correction below.
   grep -rn 'Render:' --include='*.go' \
     examples/ docs/guide/_samples/ bench/apps/ test/ | grep -v '_test.go' \
     | grep -v 'CmdPanicRender'
   ```

   **At `29348a5a`: 17 reducers and 31 fragment renders.** Each was read, not
   just matched, along with every helper it calls — an `apply*` function called
   only from a reducer is part of that reducer.

   > **L9-1 correction, 2026-08-05.** The commands above are not DEV-1's, and the
   > numbers are not DEV-1's either. **The draft's were 16 and 30 "counted by
   > those two commands", and neither number is what those commands print.**
   > Re-run at the drafted tree `2ab0cd57`, DEV-1's reducer command prints **24**
   > and DEV-1's render command prints **29**.
   >
   > - **16 was right and the command was wrong.** 16 is the count of *distinct*
   >   reducers; the pattern also matches each `Reduce: Reduce` wiring line, so
   >   it prints 24. The second command above is the one that prints 16 there.
   > - **30 was right and the command was wrong.** `Render: *func` cannot match
   >   `docs/guide/_samples/quickstart/main.go`'s `Render: Count`, which names a
   >   function instead of writing a literal. DEV-1 found it by reading — §1.2's
   >   "read, not just matched" is load-bearing and was not decoration — and
   >   counted 29 + 1. The corrected command prints 30 there and 31 here.
   > - **And the tree moved under the register between drafting and signing.**
   >   `docs/guide/_samples/architecture/` landed at `22a47a6b`, after the walk,
   >   adding **a 17th reducer and a 31st render the walk never saw**. L9-1
   >   walked that sample; it is clean, and §7.3 says on what.
   >
   > This is the difference between a register that can be re-walked and one that
   > has to be rebuilt. A Phase-5 re-walker running the draft's commands would
   > have got three numbers that disagreed with the file, two of them for the
   > file's own reasons, and no way to tell that from a tree that had moved.

2. **Grep the same trees for each forbidden act**, then read every hit in
   context rather than trusting the pattern:

   ```bash
   # clocks and randomness (FR-14)
   grep -rn 'time\.Now()\|time\.Since\|rand\.' --include='*.go' --include='*.templ' \
     examples/ docs/guide/_samples/ bench/apps/ test/ live/ internal/
   # I/O and logging (FR-14, FR-16, FR-18)
   grep -rn 'slog\.\|log\.\|os\.\|http\.\|fmt\.Print\|sql\.\|\.Exec(\|\.Query(' \
     --include='*.go' --include='*.templ' examples/ docs/guide/_samples/ bench/apps/ test/
   # in-place mutation of a value reached through the input state (FR-14, FR-18)
   grep -rn 'sort\.\|slices\.Sort\|append(s\.\|append(state\.\|append(st\.' \
     --include='*.go' --include='*.templ' examples/ docs/guide/_samples/ bench/apps/ test/
   ```

3. **Read every `.templ` file and its generated `_templ.go`**, because a render
   is written in templ and the Go escape is where an impure call would hide. 11
   templ files.

4. **Read every helper method reachable from those 30 renders** — the `with`
   builders and the read accessors on `State`, `Log`, `History`, `AlertLog`,
   `Reading`, `Table` and `Row` — for in-place mutation of a value the input
   state also reaches. No count is given for these because the set is defined by
   what the renders call rather than by a pattern: each render was followed into
   its helpers until it reached only field reads and allocations. This is the
   step a grep cannot do, because `append` to a slice held behind a pointer in
   the input state mutates the caller's backing array whenever capacity allows,
   and it looks like ordinary Go.

### 1.3 What the walk found

**Two deviations, §2.** Both were found by step 2 and confirmed by reading.

**And a body of evidence that the rules are otherwise held on purpose rather
than by luck**, which is worth stating because it is what makes the two
exceptions believable as exceptions:

- Every fold in the three examples replaces a value wholesale rather than
  appending in place. `History.with` (`examples/dashboard/dashboard.go:112`),
  `AlertLog.with` (`examples/dashboard/feed.go:184`) and `Log.with`
  (`examples/chat/chat.go:192`) each `make` a fresh slice, copy the tail they
  keep into it, and return a new pointer. The `dashboard.State` doc comment says
  why, in the type: *"Both are replaced wholesale rather than appended to in
  place: a reducer must not mutate the state it was given."*
- The bench chat's shared room sorts its rosters **before** the event is
  emitted, and its own comment gives the reason: *"Sorting here rather than at
  render is what keeps the render a pure function of state without every viewer
  re-sorting the same eight names."*
- `examples/counter`'s reducer needs a relative timestamp and takes it from
  `ev.At` — stamped at the actor boundary — rather than from a clock. That is
  the pattern FR-14 prescribes, used in the one place a reducer would obviously
  reach for `time.Now()`.
- **30 of the 31 renders close over nothing at all** — each is a
  `func(State) templ.Component` reading only its argument, with no clock, no
  package-level variable and no shared store. The thirty-first is E-1.
  *(29 of 30 as drafted; re-counted by L9-1 at `29348a5a`, where the
  `architecture` sample's render is the one added and it closes over nothing
  either.)*

**Step 4 found nothing**, and that is the result most worth doubting, because it
is the one a reader cannot re-derive from a grep. The check was: does any method
reachable from a render `append` to, sort, or assign into a slice or map that the
*input* state also reaches? The answer was no everywhere, and the reason is
uniform — every builder allocates a new slice and copies into it, and every
accessor only reads. `History.with` is the clearest instance and
`bench/apps/dashboard`'s `Visible` the least obvious: it sorts, but it sorts a
freshly allocated `[]*Row` and leaves the table it filtered untouched.

---

## 2. The deviations

### E-1 — `test/memory`'s fragment render writes to a shared stack probe

**The deviation.** FR-18: *"Renders MUST NOT mutate state or perform I/O."* The
fragment render declared at
`test/memory/cmd/memsrv/main.go:148` calls `probe.note("app.Render",
stackAddr())` at `main.go:152`, before writing its markup.
`stackProbe.note` (`test/memory/cmd/memsrv/probe.go:153`) calls
`runtime.Stack`, takes a `sync.Mutex`, and mutates a shared map keyed by
goroutine ID. That is mutation of shared process state, performed inside a
render, on the actor goroutine.

`Config.Init` (`main.go:137`) and `Config.Authorize` (`main.go:172`) call it too.
Neither of those is a deviation, and they are listed here only so a reader who
greps for `probe.note` and finds three hits knows all three were considered:
neither hook is a reducer or a render, and §3.1 gives the reading of FR-16 that
clears them. `Authorize` additionally does not run on the actor goroutine at all
— it runs on the read pump, at the mailbox ingress — so it is outside every one
of the three rules' subjects.

**Why it was accepted.** This binary exists to answer G2 — steady-state memory
per idle connection — and the specific question it answers is how far the
session actor's goroutine stack extends. The render is the deepest point on that
goroutine that the *application* can reach, so it is the only place a probe
observes the extent the measurement is about. Instrumenting the library instead
would measure a stack the library holds rather than the one a real application
produces, which is the figure PRD §3 wants. `note` is written not to disturb
what it measures — the dump buffer is pooled precisely so an 8 KiB local array
does not force the growth being observed, and its own comment says so.

**Blast radius.** Bounded to `test/memory`, a separate module built and run only
by `ci.sh`'s memory step and never linked into the library, an example, or a
consumer's binary. Within that module the consequences are real and worth
naming:

- **The render is no longer replayable**, so `livetest.ReplayN` and
  `AssertDirtyComplete` are meaningless against this application. Neither is
  used there.
- **It is a mutex acquisition on the render path** *whenever the probe is
  installed*, so the figures a probed run produces carry its cost. G2 is a
  memory measurement rather than a latency one, so this affects the number
  `memsrv` is *not* used for; **no latency figure anywhere in this project may
  be taken from this binary**, and none is.

  > **L9-1 correction before signing, 2026-08-05.** The draft said "the memory
  > figures this binary produces carry its cost", flatly. **That is false of
  > every measured run, and a blast radius that overstates is not a thing to
  > sign.** `probe` is nil unless `-probe` is passed (`main.go:119–121`,
  > default `false`), and `note` returns at its nil-receiver guard
  > (`probe.go:154–156`) before it touches the pool, `runtime.Stack` or the
  > mutex. **`test/memory/measure.sh` — the harness that produces G2's cells —
  > never passes `-probe`**; only `diag.sh`'s `on-probe` cell does
  > (`diag.sh:144`), and the flag's own help calls itself DIAGNOSTIC and says
  > it "must never be enabled during a measured window".
  >
  > **This does not clear the row, and the reason is worth stating.** In the
  > default configuration the render mutates nothing — but the *call is
  > unconditional in the source* and the render *closes over `probe`*, so
  > whether this render is a pure function of state is decided by a flag rather
  > than by the render. That is the deviation. What the correction changes is
  > its size, not its existence: it makes E-1 cheap to accept, which is part of
  > why §7.1 accepts it rather than ruling the tree out of scope.
- **It cannot leak into the library's own measurements**, because the probe map
  is capped (`p.cap`) and the binary is not what the bench apps use.

**What would remove it.** Nothing cheap, which is why it is an exception rather
than a fix. The alternative is a runtime hook the library exposes for stack
probing, which would be library surface added for one measurement — a worse
trade than one deviation in one test binary.

**`L9-1 sign-off:` ACCEPTED as a named exception. — L9-1, 2026-08-05, at
`29348a5a`.** Verified in the tree, not read off the draft: the render at
`main.go:148`, the `note` call at `:152`, `stackProbe.note` at `probe.go:153`
with its mutex and its map, the `-probe` gate at `main.go:119`, `measure.sh`'s
silence about that flag, and `test/memory/go.mod` making this its own module
that `ci.sh:452` builds separately. **The scope ruling DEV-1 offered instead is
refused; §7.1 is the argument, and it is the reason this row exists rather than
a paragraph exempting a directory.**

---

### E-2 — the error-handling guide's sample reducer logged from inside the reducer

> **CLOSED — FIXED at `091dbae8`, 2026-08-05. Verified in the tree by L9-1.
> Never accepted as a deviation, and the row is retained rather than deleted.**
> The closure record, the verification, and the ruling that overturns §4's
> "then delete this row" are at the end of this row and in §7.2.
>
> **The row below is left in the past tense it was written in and is otherwise
> unedited.** It is the record of a live merge blocker that shipped in a
> documentation phase, and rewriting it into "there was once a problem" would
> destroy the only thing it is now good for.

**The deviation.** FR-16 names *"logging of application data"* as I/O that must
happen at the actor boundary *"never inside"* the reducer, and FR-14 says
reducers MUST NOT perform I/O. `docs/guide/_samples/errorhandling/errors.go:71`
calls `slog.Warn("effect failed", …)` inside `Reduce`, with three fields read off
the event — including `live.EffectFailedErrorField`, which is application data by
any reading.

**This is the one that matters more than E-1**, and the reason is where it is.
`test/memory` is a measurement binary nobody copies. This file is the compiled
source behind `docs/guide/error-handling.md`, held by CI so that the page's code
blocks are real — which means it is code the project shows a reader and invites
them to imitate, teaching the exact mistake FR-14 and FR-16 exist to prevent, on
the page about doing failure handling correctly.

**Why it happened, which is not the same as why it was accepted.** The sample is
following the library's own advice. `live/core.go`'s doc comment on
`EffectFailedErrorField` said, and had said since `9cce6829`:

> Log it, count it, branch on it — and render `EffectFailedSourceField` instead.

Read in the context it appears in — a paragraph about what a *reducer* may
render — "log it" reads as an instruction to the reducer. The sample took it that
way. **The guidance was ambiguous and the sample resolved it wrongly, so the root
cause is the library's godoc rather than the sample author's care.** That half is
fixed at the commit that lands this file: the comment now says to branch on it in
the reducer and to log it from `Config.Execute` or from the `slog.Handler` given
to `Config.Logger`, and says why.

**Why it is recorded as an exception rather than simply fixed.**
`docs/guide/**` is outside this landing's ownership. Recording it here is what
FR-20 asks for — *"unlisted deviations are merge blockers"*, and this one has
been unlisted since the page landed — and the fix is routed in §4 rather than
taken. **This exception is expected to be short-lived**, and it is the one row in
this file that should not still be here at Phase 5.

**Blast radius.**

- **The sample application itself**: its reducer is not replayable, so the same
  event log produces a different sequence of log records on each run. Nothing
  asserts determinism over it, so nothing currently fails.
- **Readers**, which is the real radius and is unbounded. The page is
  `guide/error-handling.md`, FR-59 names it as one of the docs set's nine
  subjects, and a reader copying the pattern into an application with a
  determinism spec gets a reducer that fails FR-15's replay for a reason the
  page never mentions.
- **Not the library**: `live/` and `internal/` are unaffected, and no shipped
  example does this. `examples/chat`'s and `examples/dashboard`'s `applyFailure`
  both handle the same event and both only *branch*.

**`L9-1 closure:` FIXED — not accepted. — L9-1, 2026-08-05, at `29348a5a`.**

This line is deliberately not the sign-off line the draft left blank. **An
accepted exception and a fixed deviation need different signatures**, because
they say different things to the next reader: one says a rule is being broken on
purpose and here is the argument, the other says a rule was broken and is not
being broken now. Signing E-2 as an exception would have recorded a permission
nobody wants and nobody needs.

**What was verified in the tree, rather than accepted from the fix's summary:**

- `docs/guide/_samples/errorhandling/errors.go`'s `Reduce` (now `:63`) performs
  no I/O. It reads `EffectFailedSourceField` into `s.Notice`, parses
  `EffectFailedRetryableField`, and returns an effect. **No `slog` call reaches
  it**: every `slog` reference in the file is in `Reporter`, `Reporter.Execute`
  (`:121`) or `WireLogging` (`:149`).
- **The move is to the actor boundary and not merely to another function.**
  `Reporter.Execute` is wired to `Config.Execute`, which the library runs after
  the reducer returns — the exact placement FR-16's *"executed by the session
  actor after the reducer returns"* names.
- **The page was rewritten, not patched.** `docs/guide/error-handling.md` gained
  *"The logging rule, and why the reducer is the wrong place"* (`:241`), which
  states the rule, cites FR-16 and FR-14, and gives replay as the reason rather
  than tidiness; a per-field table saying what the reducer may do with each of
  the three fields and where its log line goes (`:255`); and a block at `:308`
  saying the page **taught the opposite until today** and naming the godoc
  sentence that caused it.
- **The root cause is fixed too**, at `live/core.go`'s `EffectFailedErrorField`,
  which is what stops the next sample author from making the same reading.
- **The samples module is green** at `29348a5a` — `go build`, `go vet` and
  `go test ./...` across all fifteen sample packages, run by L9-1.

**Two things the closure does not claim.** The page's code blocks are held
against the compiled sample by `docs/guide/_samples/samples_test.go`, so page and
source cannot drift — **but that is a synchronisation guard, not a purity
guard.** Re-introducing a `slog` call into that reducer would keep the suite
green and update the page to show the mistake. Nothing mechanical prevents E-2
from happening again; §5 is still the honest state, and §7.6 is what L9-1 did
about it.

---

## 3. Considered and cleared — the categories a reader will ask about

Not deviations. Each is here because a reasonable reviewer would ask, and
"we looked and here is the reading" is worth more than silence. **None of these
needs a sign-off**; if L9-1 disagrees with any reading, it becomes an E-row.

> **`L9-1:` all six readings AGREED, none becomes an E-row. — 2026-08-05, at
> `29348a5a`.** Each was checked against the code and not against the prose:
> `counter.go:288` / `bench chat:632` / `bench dashboard:924` for §3.1,
> `renderer.go:100,153,294` for §3.2, the fragment spans in
> `session/actor.go:689` for §3.3, `render/registry.go:120` for §3.4,
> `session/effects.go:232,296` and `actor.go:1051` for §3.5, and
> `livetest/replay.go:23,74` for §3.6. **Two of the six are extended in §7.3
> rather than merely agreed to** — §3.3's and the walk's coverage — because
> agreeing with a reading that does not reach far enough is how the next walk
> inherits a hole.

### 3.1 `Config.Init` reads clocks and joins shared stores

`examples/counter:288`, `bench/apps/chat/gotth/chat.go:632` and
`bench/apps/dashboard/gotth/dashboard.go:924` all read `time.Now()` in `Init`;
every example's `Init` calls a shared store's `Join`, which takes a lock and
registers the session.

**Cleared, on the text.** FR-14's subject is *reducers*; FR-18's is *renders*.
FR-16 requires I/O to be *"executed by the session actor … never inside"* the
reducer — and `Init` **is** the session actor, running the mount transition on
its own goroutine before the first snapshot. `Config.Init`'s documented contract
is to produce initial state and startup effects, which is not achievable without
reading something.

**The honest consequence, stated rather than hidden:** a session's mount state is
therefore not reproducible from its event log alone. FR-15's replay harness
starts from an `initial S` the caller supplies, so it never depended on mount
being reproducible; but anybody who reads FR-14 as "the whole session is a pure
fold" should know that the fold's seed is not.

### 3.2 The library's `Renderer` mutates itself during a render pass

`internal/render/renderer.go` sets `v.rendering`, writes into `v.buf`, and clears
dirty bits while a render pass runs.

**Cleared.** FR-18 says renders must not mutate *state*, and "state" throughout
PRD §5.B is the application's state value. The renderer's scratch is the
library's own, per-session and single-goroutine. Notably the mutation exists to
*enforce* purity rather than to break it: `v.rendering` is what makes a fragment
that retains its `io.Writer` and writes after `Render` returns fail with an error
instead of landing bytes in another fragment's markup.

### 3.3 The library opens a span around each fragment render

`internal/session/actor.go`'s fragment observer starts an OpenTelemetry span per
fragment while the render pass is running.

**Cleared.** It is around the application's render, not inside it: the
application's `Render` closure is called between `Start` and the finish
callback, and it is handed nothing that reaches the tracer. The span is library
instrumentation of a render rather than I/O performed by one, and with tracing
disabled — the default — `obs.Tracer` is nil-safe and does nothing.

### 3.4 `render.NewRegistry` reads randomness

`maphash.MakeSeed()`, once, at construction.

**Cleared, and worth one sentence because it is randomness on the render path.**
The seed is fixed for a process and is used only to hash a fragment's rendered
markup against its own previous markup, within one session, to suppress an
identical render. It never leaves the process, never reaches the wire, and never
changes the HTML — so FR-19's byte-identical-across-processes clause is
untouched. Two processes reach the same suppression decisions for the same
reason; only the intermediate hash differs.

### 3.5 The actor stamps `Event.At` from a clock

`internal/session/actor.go` reads `a.now()` at the mailbox boundary.

**Cleared — this is the mechanism FR-14 requires, not an exception to it.** The
clock is read once, at the boundary, and the value travels on the event, which is
exactly what lets a reducer be a pure function of `(state, event)` and what makes
an event log replayable at all.

### 3.6 `live/livetest`'s harness calls renders and reducers directly

`ReplayN` folds a reducer N times; `AssertDirtyComplete` renders each fragment
twice per event.

**Cleared.** Calling a pure function repeatedly is what a purity check *is*.
Nothing in the harness makes the functions impure, and `ReplayN`'s whole premise
is that N runs agree.

---

## 4. What each row needs, and who owns it

*(This table was DEV-1's request for rulings. The **Disposition** column is
L9-1's answer, 2026-08-05, and it is what is in force. Nothing here is
outstanding.)*

| Row | Needed | Owner | Disposition |
|---|---|---|---|
| **E-1** | L9-1's signature, or a ruling that `test/memory` is out of FR-20's scope entirely — which would be a cleaner outcome than a signed exception, and is L9-1's call rather than DEV-1's | **L9-1** | **SIGNED as an exception; the scope ruling is REFUSED.** §7.1 |
| **E-2**, root cause | **Done** — `live/core.go`'s `EffectFailedErrorField` comment now says where to log and why. Landed with this file | **DEV-1**, closed | **Verified closed** by L9-1 |
| **E-2**, the sample | Move the `slog.Warn` out of `Reduce` into `Config.Execute` or a handler, and say on the page why it is not in the reducer — the page is about failure handling, so the rule is on-topic rather than a digression. **`docs/guide/**` is not this landing's to edit** | **DEV-3** (guide owner), ~~then delete this row~~ | **DONE at `091dbae8`**, verified by L9-1. **"Then delete this row" is OVERTURNED** — the row is retained as closed history. §7.2 |
| **§3's six readings** | L9-1's agreement, or an E-row for any it disagrees with | **L9-1** | **AGREED**, with two extensions. §7.3 |
| **The register itself** | Re-walked at Phase 5, per PRD §9's note that FR-20 feeds the stdlib-grade PR criteria. §5 says what would make that cheap | **L9-1** to require it; **DEV-1** to walk | **REQUIRED**, and §1.2's commands and counts were corrected today so the re-walk is a re-run. §7.5 |
| §5's proposed `internal/arch` check | An owner, "if L9-1 wants it" | **L9-1** to assign | **NOT commissioned**, and §7.6 says why that is a decision rather than a shrug |

---

## 5. What is NOT enforced, and what it would take

**Nothing in CI checks this file against the tree.** FR-58's audit got a census
guard (`internal/arch/errors_test.go`) because "how many error messages exist" is
a countable property. "Does this reducer perform I/O" is not: a call three
helpers deep into a package that opens a connection is a whole-program analysis,
and a name-based heuristic — flag `slog.`, flag `time.Now` — would have caught
E-2 and would also flag every legitimate use in `Config.Execute`, so it would be
turned off within a month.

**This register is therefore only as good as the next walk.** §1.2's commands are
written out so the next walk is a re-run rather than a re-invention, and the
numbers it produced — **16 reducers, 30 fragment renders, 11 templ files** — are
stated so a later walk that finds different ones knows something moved.

The one mechanical check that would be worth its weight, and is not written here
because it is a new architectural claim rather than an audit finding: an
`internal/arch`-style assertion that no package under `examples/` or
`docs/guide/_samples/` imports `log/slog` **from a file that also declares a
`Reduce` function**. It is crude, it would have caught E-2, and it is a false
positive away from being useless — which is the argument for it being a
proposal in this section rather than a commit. **Owner: whoever L9-1 assigns, if
L9-1 wants it.**

---

## 6. Statement — DEV-1's, at drafting

*(Left as written. It was true of `2ab0cd57` and it is superseded by §7, which
says which parts of it stopped being true and when. A statement that gets
rewritten when it is answered is a statement nobody can hold anyone to.)*

Two deviations from FR-14/16/18 exist in this tree. Both are recorded above with
a reason, a blast radius and an unsigned sign-off line. The walk that establishes
that there are no others is §1, with its commands, its counts, and — in §1.3 —
the one result it asks a reader to doubt.

**No sign-off line in this document is signed.** FR-20's gate is L9-1, and DEV-1
drafting a register does not make it a signed one. Until L9-1 signs, E-1 and E-2
are recorded deviations rather than accepted ones, which is a different thing and
is the honest state.

— **DEV-1**, 2026-08-05, at `2ab0cd57`.

---

## 7. L9-1's rulings

*(2026-08-05, against the tree at `29348a5a`. Every claim below was checked in
the tree by the person signing it; where a claim in the draft did not survive
that check, the correction is written where the claim was and is named here.)*

### 7.1 E-1 — accepted as a named exception. The scope ruling is refused.

DEV-1 offered a cleaner-looking outcome and was careful to say it was not theirs
to take: **rule `test/memory` out of FR-20's scope entirely**, and the file
carries no E-1 at all. It is a real option and it is refused. The argument
matters more than the verdict, because what is being chosen is what the *next*
measurement harness costs.

**The case for the scope ruling, put at its strongest.** FR-14/16/18 are the
product's contract. `memsrv` is not a product; it is an instrument, in its own
module, built by one `ci.sh` step, linked into nothing. Its render exists to be
*stood on* — the probe's whole purpose is that the render is the deepest point
on the actor goroutine an application can reach. Calling that a "deviation from
the pure-render rule" arguably mistakes a measuring device for a thing being
measured, and a register cluttered with instrument rows is a register people stop
reading.

**Why it loses.** A scope ruling and an exception differ in exactly one way, and
it is the way that matters: **an exception is per-instance and a scope ruling is
standing.** Exempting `test/` says, once and permanently, that no future
deviation in any measurement harness needs an argument, a blast radius, or a
signature — and it says it to authors who have not written those harnesses yet
and will never read this paragraph. The record of what a measurement binary is
allowed to do would then be an absence.

**This project has already decided this exact question, in the other direction,
in the same week.** `docs/api-surface.md` records a refused
`live.LocalDevelopment(origin)` bundle: it would have collapsed three security
opt-outs into one line, and it was refused because **the per-check review signal
is the thing of value and a bundle destroys it**. Exempting a directory from
FR-20 is that same trade at the register level — three deviations' worth of
future review signal collapsed into one line of scope text. **I ratify that
refusal and I am applying its reasoning here.** A project cannot refuse a bundle
in its API on Monday and grant one in its process on Tuesday.

> **Citation corrected 2026-08-05 — L9-1, and the sentence above is left exactly
> as it was written.** The paragraph says `docs/api-surface.md` records the bundle
> under the name `live.LocalDevelopment(origin)`. **It does not, and never has.**
> `api-surface.md:530` carries the refusal in one clause — *"a bundle that set
> them in one line was considered and refused in the same pass"* — with **no
> symbol and no signature**. `grep -c 'LocalDevelopment' docs/api-surface.md`
> returns **0**, and `git log -S'LocalDevelopment' -- docs/api-surface.md` is
> **empty**. **The name is mine, coined in this paragraph**, and I then quoted it
> as though I were citing it. Found by **PM-1** while deriving FR-53's floor
> (PRD §9 v1.1 row 2, `docs/pm/fr-53-amendment.md` §6.3).
>
> **Nothing in the ruling moves.** The refusal is real, it is recorded at
> `:530`, its ground is the one stated above, and the reasoning I applied to
> FR-20's `test/` scope is unaffected. What moves is the load-bearing citation:
> it is **this ratification**, not the ledger's aside. The name stays in use —
> six documents now depend on it — and it is **not** being back-filled into the
> ledger to make the citation true, because retrofitting history to fit a
> citation is the inverse of the rule this section applies to E-2. The correction
> goes beneath the sentence rather than into it for the same reason: a page that
> quietly corrects itself teaches the fix and hides the failure mode.
> Countersignature and the rest of the FR-53 ruling:
> [`reviews/fr-53-line-budget.md`](reviews/fr-53-line-budget.md) §7.1.

**Two facts found while verifying make the exception cheap, which is the rest of
the answer.** First, the deviation is **gated on a diagnostic flag that no
measured run passes** — the correction inside E-1's blast radius has the
evidence, and it means accepting E-1 concedes almost nothing in practice. Second,
**this row costs one paragraph and is falsifiable**: a reader can run four greps
and check every sentence of it, which is not true of a scope sentence.

**The standing rule this sets, so the next harness author knows the cost up
front:** a measurement or chaos harness that needs to break FR-14/16/18 gets an
E-row — a reason, a blast radius, an L9-1 signature. **It is not exempt and it is
not hard.** E-1 is the worked example of how long that takes.

### 7.2 E-2 — closed as fixed, and the row is kept. Registers do not delete history.

The fix is real and I verified it rather than taking the summary; the evidence is
in E-2's closure block. What was actually put to me is the harder half: §4 said
*"then delete this row"*, and I am overturning that.

**The argument for deleting.** A register of *current* deviations that carries
fixed ones is a register whose length stops meaning anything. FR-20's job is to
make live deviations visible; a row that no longer describes the tree is noise
competing with the rows that do.

**Why it loses, and the first reason is not a matter of taste.**
`docs/guide/error-handling.md` now contains — at `:313` — a block naming **E-2**
by identifier and linking `../exceptions.md`, as the register FR-20 requires.
**Deleting the row makes a published page point at a document that does not
carry the thing it names.** The page's own sentence for why it says this at all
is *"a page that quietly corrects itself teaches the fix and hides the failure
mode"*. A register that quietly deletes its rows does precisely that, one level
up, and would make a liar of the page in the same motion.

**The second reason is what the register is for.** At Phase 5 this file feeds the
stdlib-grade PR criteria, and the question a reviewer at that bar asks is not
"what is broken today" — CI answers that — but **"has this rule ever been broken
here, and where, and what did you do about it?"** A register that deletes on fix
cannot answer it. It re-creates, for history, the exact unlisted state FR-20
calls a merge blocker: the deviation happened, it is nowhere, and nothing in the
tree distinguishes "never happened" from "happened and was erased" — which is
`docs/gates/phase-4.md` §4.13's original finding, reinvented by the register that
was written to close it.

**And the register's own §0 already made this argument**: this document is
*"where they stop being unlisted — not where they stop being deviations."* A row
whose deviation is fixed has not stopped being a thing that happened.

**The noise objection is answered by disposition, not by deletion.** E-2 carries
its verdict in a block at the top of the row, the header table carries it in one
line, and nobody reading either can mistake it for live. That is the whole cost.

**Standing rule for this file, in force from today:** **rows are closed, never
deleted.** A closed row keeps its original text in the tense it was written in,
gains a disposition block naming the fixing commit and what was verified, and
stays. **The count that matters is not the number of rows; it is the number of
rows without a disposition.** Today that number is one, and it is E-1.

### 7.3 §3's six readings — agreed, and two of them extended

All six clearings are correct on the requirement text and I am not opening an
E-row against any. §3.1's is the one that earns its place: it clears `Config.Init`
and then states the consequence — a session's mount state is not reproducible
from its event log — instead of banking the clearing and moving on. That is what
a cleared category should look like.

**Two extensions, which I made rather than requested, because a reading that does
not reach far enough leaves the next walk a hole:**

1. **§3.3 covers the spans; it did not cover the timers.** A reader who greps
   `a.now()` in `internal/session/actor.go` finds **five** hits, not the one
   §3.5 describes. Two of them — `actor.go:570` and `:572` — bracket the render
   pass to feed `RenderDuration`. **They are cleared on §3.3's own reading and
   not on §3.5's**: the library timing its own render pass is instrumentation
   *around* the application's render, the same relationship the fragment span
   has, and the application's `Render` closure is handed nothing that reaches
   either. §3.5's subject is narrower than its greppable footprint, and this
   sentence is what stops the next walk re-deriving it.
2. **The walk now reaches `docs/guide/_samples/architecture/`**, which landed at
   `22a47a6b` after the draft and which the draft therefore never saw. I walked
   it: `Reduce` (`architecture.go:88`) branches on the event, reads
   `ev.Fields`, writes two fields of a copied state and returns an effect — no
   clock, no I/O, no mutation of the input; its render closes over nothing;
   `Room.Execute` takes the mutex at the actor boundary, where it belongs; and
   `Room.Join` in `Init` is cleared by §3.1 for the reason §3.1 gives.
   **No new deviation. The count is 17 and 31, and the two E-rows are still the
   whole set.**

### 7.4 What I am routing to PM-1 — two PRD amendments, and one thing I am not touching

**I am not resolving `docs/gates/phase-4.md` §7.6's box-13 split.** It is PM-1's,
it is a scope act, and §7.6 already says the first of its two resolutions is
probably right. Nothing in this file should be read as having chosen one.

**Amendment 1 — FR-20 is silent on what happens when a deviation is fixed, and
that silence produced a real disagreement today.** §4's "then delete this row"
was a fair reading of a requirement that says only that deviations MUST be
*recorded*. I ruled the other way in §7.2, and **a ruling that lives only in the
file it governs will not be found by the next person drafting against FR-20 —
they will read FR-20.** Requested text, PM-1's to word: *a recorded deviation
that is fixed is CLOSED in the register with its disposition and the commit that
fixed it, and is retained; entries are not deleted.*

**Amendment 2 — FR-20's scope is asserted by this file rather than stated by the
requirement.** FR-20 says "any deviation" and names FR-14/16/18. It does not say
whether "any" reaches trees that are not published: the guide's compiled samples,
the bench comparison apps, the measurement and chaos harnesses. §1.1 asserts that
it does, I have ratified that in §1.1 and built §7.1's refusal on top of it —
**but a scope that lives in the register is a scope the next drafter may narrow
without noticing they are narrowing anything**, and E-1 exists only under the
wide reading. Requested: *FR-20 names the trees it covers — every tree in the
repository that implements the reducer or render contracts, whether or not it
ships.*

Both are amendments and not gate outcomes, so they belong in a landing that
argues for them. **Owner: PM-1.**

### 7.5 Would this register survive a Phase-5 re-walk, or would it be rebuilt?

I was asked to answer this plainly and the honest answer has two halves.

**As DEV-1 handed it to me: it would have been rebuilt, not re-walked.** §5's
claim is that §1.2's commands are written out "so the next walk is a re-run
rather than a re-invention", and that the counts are stated "so a later walk that
finds different ones knows something moved". Run today, **the draft's commands
disagree with the draft's numbers in three places, and only one of the three is
the tree moving**:

| What the draft said | What its own command prints | Why |
|---|---|---|
| 16 reducers, "counted by those two commands" | **24** at `2ab0cd57` | The pattern matches each `Reduce: Reduce` wiring line as well as its declaration. 16 was the right count of reducers and the wrong count of hits |
| 30 renders, same claim | **29** at `2ab0cd57` | `Render: *func` cannot see `quickstart/main.go`'s `Render: Count`. DEV-1 found it **by reading** and added it; the command cannot |
| 16 and 30, at HEAD | **17 and 31** | The tree genuinely moved: `docs/guide/_samples/architecture/` at `22a47a6b` |

A Phase-5 re-walker would have hit all three at once and been unable to tell the
one real signal from the two artefacts — which is the failure mode a stated count
exists to prevent, arriving through the count itself. **Note what this is not: it
is not sloppiness in the walk.** Both numbers were *right*; DEV-1 read what the
grep could not match and counted correctly. What was wrong was the claim that the
commands produced them, and that claim is the entire re-walkability guarantee.

**As it stands after today: yes, it is re-walkable, and it should be re-walked
rather than rebuilt.** §1.2's commands now print the numbers §1.2 states, the
numbers are pinned to `29348a5a` rather than to "today", the reducer command that
counts each reducer once is given beside the one that over-counts, and §1.1
carries the per-tree breakdown so a delta localises to a directory instead of to
a total. **The substance was always sound** — the two E-rows are correct, the
step-4 result I most wanted to doubt held everywhere I checked it, and the
clearings are right on the text. It was the instrument that needed calibrating,
not the measurement.

**One standing requirement for that re-walk, which is §4's row made explicit:**
it re-runs §1.2 against the shipped tree, states the three counts it gets, and
**if any differs from 17 / 31 / 11 it says which directory moved before it says
anything else.** DEV-1 walks; L9-1 signs.

### 7.6 §5's proposed lint — not commissioned, and that is a decision

§5 proposes an `internal/arch` assertion that no package under `examples/` or
`docs/guide/_samples/` imports `log/slog` from a file that also declares a
`Reduce`, and hands the decision to me. **I am not commissioning it**, and I want
the reason recorded so nobody re-proposes it as an oversight.

It would have caught E-2 — the strongest thing that can be said for a lint. But
E-2's fix puts `Reporter` and `Reduce` **in the same file**, `errors.go`, and the
page's whole pedagogical point is that a reader sees the reducer that branches
and the executor that logs *side by side*. **The check as specified fires on the
fixed tree.** Making it not fire means either splitting a teaching file to satisfy
a linter — moving code across a file boundary to change a measurement, which is
the exact move FR-53's counting rule was written to forbid — or a suppression
comment in the sample, which is a marker in a code block a reader is invited to
copy. Both are worse than the disease.

**What I want instead, and it is a different check.** The property worth
enforcing is not "this file imports `slog`"; it is "this *function* performs
I/O", and the cheapest honest approximation is a **call-graph** assertion over
each `Reduce` and each `Fragment.Render` — reachability from those roots into
`log/slog`, `net/http`, `database/sql`, `os`, `time.Now` or `math/rand` — rather
than a file-level import grep. That is a real piece of work and it is not free,
so it is a Phase-5 candidate with a named argument rather than a chore.
**Owner: unassigned by choice. If it is picked up, it is QA-1's**, as a
correctness guard, and it needs its negative control demonstrated on E-2's
pre-fix commit before anybody trusts it.

Until then, §5's sentence stands as the honest state, and this register is the
enforcement.

### 7.7 What I signed

Two deviations from FR-14/16/18 were found in this tree by a walk I re-ran rather
than read. **One is accepted with an argument and my name on it; one was fixed
before it reached me and is closed with my name on that instead.** Six categories
are cleared on readings I checked and extended twice. Three of the register's own
numbers were wrong about their own method and are corrected. **No row in this
document is now without a disposition.**

**And the box does not tick.** PRD §9's box 13 says it cannot before Phase 5, and
`docs/gates/phase-4.md` §7.6 records that PM-1 has not resolved what that means
for the phase. **My signature is not a workaround for either.** What is true
today is that FR-20 has a register, the register has been walked against the
shipped tree, and the deviations in it are named, argued and signed — which is
everything FR-20 asks for and is not, by itself, the box.

— **L9-1**, 2026-08-05, at `29348a5a`.
