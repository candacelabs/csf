# The error-message audit — gotth-live

> **Historical evidence notice (2026-08-11).** This audit is intentionally
> pinned to the 2026-08-05 revisions named below. Its references to the former
> `internal/refine`, `internal/protocol/refinepb`, and
> `protoc-gen-gorefine` layout describe that audited tree, not the current
> architecture. Current generated validators live in `gotthlivepb`, import
> `github.com/candacelabs/csf/pkg/liquidproto`, and are produced by
> `protoc-gen-liquidproto`.

**FR-58:** *"Every library-produced error MUST name the session, the causal ID
where one exists, and the actionable next step. `'invalid frame'` without
context is a defect."* `Phase: 2` `Gate: QA-1`

**Produced by DEV-1**, 2026-08-05, against the tree at `2ab0cd57`. This is the
Phase 4 exit box PRD §9 assigns as *"DEV-1 to enumerate, QA-1 to grade"*. What
follows is the enumeration and DEV-1's own grading; **QA-1's grade is the one
that ticks the box**, and this document is the artifact it should be taken
against rather than a substitute for it.

**Headline, so it can be checked before anything else here is read.**

| | |
|---|---|
| Error-authoring sites enumerated | **118** at revision 7, **120** at revision 6, **121** at revision 5, **120** at revision 4, **119** at revision 3, **118** at revision 2, **117** as originally walked — in 8 packages of the published module. Every number below except this row and §3.1's, §3.3.1's, §3.3.2's, §3.4's and §5's is the walk's own and is left alone; see the revision notes |
| Sites out of scope, each with a reason | **8 packages** — §2.3 |
| Enumerated sites that failed at least one applicable clause | **25** — the rows marked "**was …**" in §3 |
| Fixed in code | **25**, all of them, plus **4** defects this walk found that are not error-authoring sites: three log records dropping a causal identifier they were holding, and one error path that logged nothing at all. **29 changes**, at `ba5ce082` (27) and `4d28146f` (2) |
| Rows where a clause is genuinely inapplicable | **stated per row, with the reason**; §3's preamble defines each reason once |
| Under a regression guard | §5 — four files, and what each one can and cannot hold |

**Which tree each count is of, so a grader is not comparing two different
walks.** QA-1's grading pass reproduced this document's enumeration independently
and got **117** (`docs/qa/phase-4-grading.md` §2.1, per-package, on a pristine
clone). **That is revision 1's number and it is right**: the walk was run against
the tree at `091dbae8`, before FR-53's shrink landed. 118 is revision 2 (the tree
from `cd2c4cac`) and 119 is revision 3 (this one). The walk QA-1 ran is unchanged
and still returns 117 at `091dbae8`. **No verdict QA-1 checked has been reversed
in either revision.** Revision 3 does rewrite the `S` column of five §3.4 rows,
and the reason is QA-1's own F-1: the code moved so that what those cells claim
is true of every path, and the cells now say which path each clause is met on.

**Revision 7 — 2026-09-03, the second removal of the same day, and the one with
the sharper argument.** `live.Session` became generic in the application's
identity type (operator ruling: *"`func (s Session) Identity() IIdentity` RETURN
TYPE IS IIDENTITY FUCK YOU"*), so `Config.Authenticate` returns the
APPLICATION's own type and "returned no identity and no error" stopped being a
result it can produce. **Two** errors answered that shape and both are gone:
`live/app.go`'s authenticate adapter, deleted with the adapter, and
`live/page.go`'s `errNoIdentity`. **The count is 120 − 2 = 118**, the census
moves 39 → 37 for `live`, and §3.1 loses one row while §3.2's adapter row goes
with the adapter. A type parameter deleting two error messages is the strongest
form of the argument for it: the failure is not handled better, it is
unreachable.

**Revision 6 — 2026-09-03, and it is the census firing on a REMOVAL, which it
had not done before.** `live.Config.Execute` was deleted when `live.Effect`
became a concrete struct carrying its own `Run` (operator ruling, 2026-09-03;
`docs/lab/2026-09-03-effects-concrete/entry.md`), and the one error that hook
authored — *"a reducer returned an effect but Config.Execute is nil"* — went
with it. **The count is 121 − 1 = 120**, `internal/arch/errors_test.go`'s census
moves 40 → 39 for `live` in the same commit, and §3.2 records what left and why
rather than dropping the row silently. **The fifth time the guard has fired**,
and the first on a deletion: the census is symmetric, which is the property that
makes it a census rather than a floor. The failure that error described did not
disappear — it moved to `internal/session`, which refuses an effect whose `Run`
is nil with a deterministic failure event, and that site was already enumerated.

**Revision 5 — 2026-08-05, DEV-1, and the new error is the repair for a review
finding rather than a new feature.** L9-1's gate on the page shell
([`reviews/page-shell.md`](reviews/page-shell.md), condition **PS-1**) found that
`live.Script` rendered inside `(*App).Document`'s head content emits a runtime
tag *above* the inspector's and silently blinds it. The repair refuses that
composition, and a refusal is a sentence somebody has to read: **the count is
120 + 1 = 121**, `internal/arch/errors_test.go`'s census moved 39 → 40 for
`live` in the same commit, and §3.3.2 gains the row. **The fourth time the guard
has fired.** Nothing else changed: no verdict, no "was ✗" annotation, and no row
of §3.1 through §3.4. Note for a future grader that this is the first graded
error in the published module whose *condition is a context value* rather than
an argument or a field — §6 should be read as gaining a fifth weakness there,
because nothing in this document's method would have found it by reading a call
site.

**Revision 4 — 2026-08-05, DEV-1, and it is the census firing on an ordinary
addition, which is the case it was built for.** `(*App).Document`, the
library-owned page shell FR-53's engineering route needed, refuses a document
with an empty title, and that refusal is one new error-authoring site in `live`.
**The count is 119 + 1 = 120**, `internal/arch/errors_test.go`'s census moved
38 → 39 for `live` in the same commit, and the new error is graded in **§3.3.2**
by the same method as everything above it. **The third time the guard has
fired**, and the first on an addition that was neither a defect nor a refactor —
a symbol was added, an error came with it, and the census would not let it ship
ungraded. Nothing else in this document changed: no verdict, no "was ✗"
annotation, and no row of §3.1 through §3.4.

**Revision 3 — 2026-08-05, DEV-1, and it closes QA-1's F-1 rather than
answering it.** F-1 is the standing condition on Phase 4's box 12: five §3.4 rows
graded FR-58's session clause as satisfied *"↑ via `Client.where()`"*, and
`where()` was applied on the `tb.Fatalf` paths alone, so the exported
`(*Client).NextErr` — the one method that hands the value to a caller — returned
those messages bare. QA-1 drove it and printed the string. **The code is what
changed**, not the grade: `NextErr` now wraps whatever ended the wait in
`livetest: <name> (session <hex>): …`, once, at one place; `Next` prints that
value instead of composing a second prefix; and `NewClient`'s mount-snapshot
failure composes with it. That wrap is itself an error-authoring site by §2.1's
rule, so **the count is 118 + 1 = 119**, `internal/arch/errors_test.go`'s census
moved 7 → 8 for `live/livetest` in the same commit — **the second time it has
fired**, and the first time on a defect a grader found rather than on a
refactor — and the property is now under a spec, §5's fourth row.

**Revision 2 — 2026-08-05, DEV-1, and it is an edit made *under* QA-1's grading
pass rather than beside it.** FR-53's shrink landed three additions to `live`
and made `Config.Init` optional, which moved this document's subject in three
ways: one enumerated error **no longer exists** (`Config.Init`'s missing-hook
`ConfigError` — the field is optional now, so there is nothing to refuse), two
**new** ones were authored in `live/page.go`, and the `live/*.go` line numbers
in §3.1 through §3.3 all shifted. **The count is therefore 117 − 1 + 2 = 118**,
and `internal/arch/errors_test.go`'s census was updated in the same commit,
which is the rule §5 states and this is the first time it has fired.

**What was NOT touched:** every verdict, every "was ✗" annotation, §4's list of
25, and the prose in §1, §2, §4, §6 and §7, all of which are about the walk at
`2ab0cd57` and stay true of it. The two new rows are §3.3.1, graded by the same
method and marked as revision 2's, so a grader can see exactly which two rows
did not exist when the pass began.

---

## 1. What this document is, and what the gate report said it was not

`docs/gates/phase-4.md` §4.12 records this box as **NOT MET — not started**, and
is specific about two things that had been mistaken for it:

> `guide/error-handling.md` exists and is a good reader-facing page. `9cce6829`
> wrote doc comments on error types that say which reader each string is for […]
> **Neither is the audit.**

Both statements stand. `guide/error-handling.md` tells an application author how
to handle failures; it does not enumerate the library's own error set.
`9cce6829` documented the *types*; it graded no *message*. The missing artifact
was the enumeration — every library-produced error, checked against FR-58's
three clauses, one row each — and the changes that enumeration implies.

**A document that grades and changes nothing is not an audit.** 25 of the 117
sites failed a clause that applied to them, and all 25 were rewritten in code
before this file was written, so §3's "message as it reads today" column
describes the tree you have rather than the one that was walked. §4 lists them
with what each one was, together with four more defects the same walk turned up
that are not sites in the §2.1 sense — a log record is not an error-authoring
site, and three of them were dropping a causal identifier the call site was
already holding.

---

## 2. Method

### 2.1 How the enumeration was produced, so it can be re-run

The set is not a grep for the word "error". It is a walk of the module's Go
syntax trees for the places **a human wrote a sentence that becomes an error
message**, which is the only thing FR-58 has an opinion about. A function that
returns another function's error verbatim authors nothing and is not a site.

A site is one of:

1. a call to `errors.New` or `fmt.Errorf`;
2. a composite literal of a type whose name ends in `Error` — `ConfigError`,
   `RejectError`, `InvalidFrameError`, `DenyError`, `FatalDenyError`,
   `RetryableError`, and (deliberately, see §3.9) two protobuf message types
   that also end in the word;
3. a call to `internal/protocol`'s `reject` helper, which is the one function in
   this module that takes a message and returns an error constructed elsewhere.
   Every inbound rejection a client or an operator ever reads is written at one
   of those 15 call sites and nowhere else, so omitting them would have left the
   whole client-facing half of the surface uncounted.

The walk is committed, and it is the same rule the regression guard applies:
[`internal/arch/errors_test.go`](../internal/arch/errors_test.go). To reproduce
the count:

```bash
# from gotth-live/ , in the project's toolchain container
docker run --rm -v "$PWD/..":/w -w /w/gotth-live dis-gotth-live:latest \
  bash -c 'go test ./internal/arch/ -count=1 -run TestArchitecture'
```

It passes when the tree's site count per package equals the census, and its
failure message names the package and both numbers. To see the sites themselves
rather than the count, the same AST rule over `live/` and `internal/`, skipping
`_test.go`, is what produced §3's rows; the greps below reach a superset of it
and were used as the cross-check that the walk had not missed a construction
shape:

```bash
grep -rn 'errors\.New\|fmt\.Errorf' --include='*.go' live/ internal/ | grep -v '_test.go'
grep -rn ') Error() string' --include='*.go' live/ internal/ | grep -v '_test.go'
grep -rn 'Error{' --include='*.go' live/ internal/ | grep -v '_test.go'
```

**Line numbers in §3 are as of `2ab0cd57` and will drift.** That is why the
guard counts per package rather than pinning lines: a census that fails on every
edit is a census somebody turns off.

### 2.2 What counts as "library-produced"

Every package of the **published module** — the one a consumer's `go.mod`
resolves — that can produce an error a consumer's process can reach:

`live`, `live/livetest`, `internal/cmd/gotth-live-dev`, `internal/obs`,
`internal/protocol`, `internal/render`, `internal/session`, `internal/wsx`.

`internal/cmd/gotth-live-dev` is in scope despite `internal/`, because its own
package comment documents `go run …/internal/cmd/gotth-live-dev` as the
supported way to use it: Go's internal rule governs imports, and a `main`
package named on a command line is not imported.

`live/livetest` is in scope for the same reason it is published: an application
author's specs run against it, and its failure messages are the only output it
has.

`test/`, `examples/`, `bench/`, `tools/` and `docs/guide/_samples/` are separate
modules or separate trees. None is library code a consumer links, and none is
enumerated here.

### 2.3 What is out of scope, and why — one reason per package

These eight are asserted as an exact set by the guard, so a new directory under
`internal/` cannot appear and quietly avoid being graded.

| Package | Why it is out |
|---|---|
| `internal/protocol/gotthlivepb` | Generated by protoc and protoc-gen-gorefine, marked `DO NOT EDIT`. Its 45 messages are all `Refine<T>: cannot refine a nil *<T>` and refinement violations. **Every one of them reaches a reader wrapped** — by `RejectError` inbound, by `InvalidFrameError` outbound — and the wrapper is what §3.9 grades. |
| `internal/protocol/refinepb` | Generated, as above. |
| `internal/refine` | Vendored verbatim from the research tree by `gen.sh` and marked `DO NOT EDIT`. Its `*Error` is the payload inside a `RejectError`; the wrapper carries the session context and the next step. |
| `internal/clientcodec` | Build-time code generator, run by `gen.sh`. Its 13 messages are read by whoever regenerates the client codec; the package is never linked into a consumer's binary. |
| `internal/cmd/gen-clientcodec` | That generator's `main`, out for the same reason. |
| `internal/obstest` | Test support. Not linked into a consumer's binary. |
| `internal/livebridge` | Declares one variable and one interface, and constructs no error. |
| `internal/arch` | Contains no non-test code at all. |

**The generated-code exclusion is the one worth arguing with**, and the argument
is in §7.2: it is defensible only because *every* path out of the generated
boundary is wrapped, and that is a property somebody could break without
noticing.

---

## 3. The enumeration

**Reading the grade columns.** `S` is FR-58's session clause, `C` its causal-ID
clause, `N` its actionable-next-step clause.

- **✓** — the clause is satisfied, in the words the row's message shows.
- **n/a (reason)** — the clause is **inapplicable**, not waived. Each reason is
  defined once below and used verbatim, so a row can be checked against the
  definition rather than trusted:
  - `n/a (construction)` — the error is produced by `live.New`, `wsx.NewHandler`
    or `obs.NewMetrics`, before any connection exists. There is no session, and
    no causal identifier, because nothing has happened yet. A message that
    invented either would be lying. `live/fr58_test.go` asserts the *absence* of
    a session clause on these, so the reason is falsifiable rather than assumed.
  - `n/a (page render)` — `live.Script` and `App.InspectorScript` render on the
    page request, before the WebSocket exists.
  - `n/a (pre-session)` — the upgrade is being refused, and `mintID` has not
    run. The identity is named instead, because it is the only handle the
    refused request has.
  - `n/a (parse)` — the frame is being decoded and has not been trusted yet.
    Server-minted causal identifiers are minted at the actor boundary, which
    this has not reached; the client's own `client_ref` is untrusted input and
    naming it as a causal ID would be repeating a claim the library has just
    refused.
  - `n/a (pure)` — a pure validation predicate over a proto message, holding no
    session and no connection. Reached only through a wrapper, named in the row.
  - `n/a (not an error)` — the census rule is deliberately over-inclusive; the
    site is not an `error`. §3.9.
- **↑ via `X`** — the clause is satisfied **in composition**, by the wrapper
  named. The row is graded on the string a reader actually meets, which is the
  composed one, and the wrapper is named so the claim can be checked.
- **✗** — failed. Every ✗ in this document is historical: it names what was
  wrong and links to §4. Nothing in the tree at `2ab0cd57` fails an applicable
  clause.

### 3.1 `live` — construction (`live/app.go`, `config.go`, `devreload.go`)

All 19 are `*ConfigError`, rendering as `gotth-live: Config.<Field> is invalid:
<Detail>`. `S` and `C` are `n/a (construction)` on every row, for the reason
defined above.

*Line numbers are revision 2's, at HEAD. The walk's own were 20 rows and are
listed in the revision note above; the only row that changed is the deleted one.*

| Site | Field / message | S | C | N | Verdict |
|---|---|---|---|---|---|
| `live/app.go:78` | `Fragments` — wraps `render.NewRegistry`'s message (§3.7) | n/a (construction) | n/a (construction) | ↑ via the wrapped registry message | PASS |
| `live/app.go:88` | `Metrics` — wraps `obs.NewMetrics` (§3.5) | n/a (construction) | n/a (construction) | ↑ via the wrapped metrics message | PASS |
| `live/app.go:108` | `Origins` — wraps `wsx.NewHandler` (§3.8) | n/a (construction) | n/a (construction) | ↑ via the wrapped handler message | PASS |
| ~~`live/app.go:107`~~ | ~~`Init`: *"set a mount hook returning the session's initial state"*~~ — **DELETED at revision 2.** `Config.Init` became optional for FR-53, so a nil one is filled in with the zero value rather than refused, and there is no error here to grade. The row is struck rather than removed so the count moves visibly | — | — | — | **n/a — the site is gone** |
| `live/app.go:165` | `Reduce`: *"set the reducer that advances state"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `live/app.go:167` | `Fragments`: *"declare at least one live region"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `live/app.go:169` | `Events`: *"declare the event names this application accepts; unknown names are refused"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `live/app.go:171` | `Origins`: *"list the allowed Origin values, or set live.AnyOrigin for local development"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `live/app.go:173` | `Authenticate`: *"set an authentication hook, or live.Anonymous to opt out"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `live/app.go:175` | `Authorize`: *"set a per-event authorization hook, or live.AllowAll to opt out"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `live/app.go:177` | `CSRF`: *"set a CSRF hook, or live.NoCSRFCheck to opt out"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `live/app.go:181` | `Events`: *"Events[i] is empty; every event needs a name"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `live/app.go:198` | `Events`: the name is too long to namespace onto an origin — states the bound, the pattern, and *"so this name is at most N bytes long: shorten it"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `live/config.go:487` | `Limits.<field>`: *"must not be negative; leave it zero to take the documented default"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `live/config.go:495` | `Limits.CoalesceFlushAt`: says the flush would build a frame the protocol refuses, then *"set it to at most N, or leave it zero for the default of M"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `live/config.go:509` | `Limits.<snapshot param>`: names the predicate the mount snapshot is refined to, then *"set it to between X and Y, or leave it zero for the default of Z"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `live/config.go:618` | `Limits.HeartbeatTimeout`: the unreachable-timeout message, ending *"Set HeartbeatTimeout to at least X … or lower HeartbeatInterval; the defaults are …"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `live/devreload.go:247` | `DevBuildID` over 128 bytes — *"a short opaque token such as a commit hash is what fits"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `live/devreload.go:257` | `DevBuildID` control byte — says the body is compared verbatim in the browser | n/a (construction) | n/a (construction) | ✓ | PASS |
| `live/devreload.go:266` | `DevBuildID` whitespace — *"this identity can never equal itself — trim it here"* | n/a (construction) | n/a (construction) | ✓ | PASS |

**Result: 19 PASS, 0 FAIL** (20 PASS at the walk, less the deleted `Init` row).
This is the best-served corner of the library and
it is worth saying why: `docs/api-surface.md:458` records that six error
sentinels were collapsed into one `ConfigError` with `Field` and `Detail`
precisely because *"the error text is more actionable (FR-58) than an
`errors.Is` target"*. That decision is the reason there is nothing to fix here.

### 3.2 `live` — the Emitter, and the denial types (`live/app.go`)

| Site | Message as it reads today | S | C | N | Verdict |
|---|---|---|---|---|---|
| `live/app.go:379` | `&session.FatalDenyError{Reason: …}` — translation of the application's own `FatalDenyError` across the adapter boundary. Renders through `internal/session`'s copy (§3.6) | ↑ via `ingress.go:182`'s `Error` record: `session_id`, `subject`, `event_name`, `event_id` | ↑ via the same record | ✓ *"event denied, closing the connection: <reason>"* | PASS |
| `live/app.go:383` | `&session.DenyError{Reason: …}` — same, survivable | ↑ via `ingress.go:192`'s record, which is `Warn` rather than `Error` because a survivable denial is the authorization hook working | ↑ via the same record: `event_name`, `event_id` | ✓ | PASS |
| `live/app.go:388` | `&session.DenyError{Reason: err.Error()}` — an authorization hook that returned an unrecognised error shape is treated as a denial, so a hook cannot fail open | ↑ via the same `Warn` record | ↑ via the same record | ✓ — the hook's own message is carried through verbatim as the reason | PASS |
| `live/app.go:412` | *"gotth-live: session `<id>`: an event emitted by an effect scheduled by event N set Event.ID to M: causal identifiers are minted by the server, so leave it zero…"* | ✓ | ✓ | ✓ | PASS — **was ✗ on S and C**, §4.1 |
| `live/app.go:418` | *"…set Event.At: the actor boundary stamps it, so leave it zero"* | ✓ | ✓ | ✓ | PASS — **was ✗ on S and C**, §4.1 |
| `live/app.go:436` | *"…listed N identifiers in Event.Contributing, above the limit of 64: name the events whose state changes this event carries, not every event the session has seen"* | ✓ | ✓ | ✓ | PASS — **was ✗ on S and C**, §4.1 |
| `live/app.go:444` | *"…listed 0 in Event.Contributing: list the identifiers of real events, or leave the field nil"* | ✓ | ✓ | ✓ | PASS — **was ✗ on S and C**, §4.1 |
| `live/app.go:595` | *"gotth-live: fragment %q rendered no component: return a templ component rather than nil — an empty one for the state that has nothing to show"* | ↑ via the actor's render-failure record, which carries `session_id`, `fragment_id`, `event_id` and `transition_id` | ↑ via the same record | ✓ | PASS — **the fragment was unnamed**, §4.4 |

**One row left this section on 2026-09-03, and it is worth recording rather
than deleting silently.** `live/app.go`'s *"a reducer returned an effect but
Config.Execute is nil"* was the one row where "no session" was a judgement
rather than an observation — a session existed when it fired, but the fact
reported was a property of the Config and identical on every session in the
process. It is gone because `Config.Execute` is gone: since the effect became a
concrete struct carrying its own `Run`, a reducer cannot return an effect with
no executor, and the failure that error described is now
`internal/session`'s refusal of an effect whose `Run` is nil (§3.7). `live`'s
census moves **40 → 39** with it.

### 3.3 `live` — `Script`'s mount-path refusals (`live/templ.go`)

`S`, `C`: `n/a (page render)` on all six.

| Site | The clause it refuses, and what it says the browser does | N | Verdict |
|---|---|---|---|
| `live/templ.go:418` | empty mount — *"such as `/live`: the client runtime is served by that handler, so an empty mount cannot address it"* | ✓ | PASS |
| `live/templ.go:424` | not absolute — *"the browser resolves this against the page's own URL, so it must begin with `/`"* | ✓ | PASS |
| `live/templ.go:432` | contains `//` — names the authority the browser would parse, and where the WebSocket would go | ✓ | PASS |
| `live/templ.go:440` | contains `\` — states the WHATWG rule that makes one backslash and two behave identically | ✓ | PASS |
| `live/templ.go:448` | contains `?` or `#` — says the runtime filename lands inside the query and is never fetched | ✓ | PASS |
| `live/templ.go:457` | control byte — says browsers strip tab/CR/LF before parsing, so the requested path is not the written one | ✓ | PASS |

**6 PASS.** Each says what a browser will do, which is a stronger next step than
"invalid path" and is why none needed changing.

**These six now serve two callers, and the subject is a parameter because of
it** (revision 2). `(*App).Mux` routes with a mount path and validates it by the
same predicate, so the messages take the naming caller — `live.Script` or
`(*live.App).Mux` — rather than blaming `live.Script` for a string handed to
`Mux`. Naming the wrong function is an FR-58 next-step failure of the quietest
kind: the sentence is actionable and it points at the wrong line. The six
clauses themselves are byte-identical.

### 3.3.1 `live` — the page handler (`live/page.go`), added at revision 2

Two sites, both new, neither existing when the walk was made. `S` is `n/a (page
request)` on both, and the reason is sharper than "no session yet": a page
request **cannot** have one, because a session is minted at the WebSocket
handshake and this is a different request — which is exactly why
`PageHandler` hands `Config.Init` the zero session id. `C` is `n/a` for the same
reason: no event, no transition, nothing to be caused by.

| Site | Message as it reads today | S | C | N | Verdict |
|---|---|---|---|---|---|
| `live/page.go:155` | *"gotth-live: the page function returned no component on a page request: return a templ component rather than nil — an empty one for the state that has nothing to show"* | n/a (page request) | n/a (page request) | ✓ — and it answers the follow-up question, which is what to return when there is nothing to show | PASS |

**2 PASS, 0 FAIL.** Both are `errors.New` rather than a private string type
*specifically so that §5's census counts them*: the first draft of this handler
used a named string type, the census did not see it, and a message a human wrote
would have shipped ungraded. That is the coverage failure §5 exists to prevent,
found by §5 firing.

**What is deliberately not in this table**, so that a grader does not have to
guess whether it was missed: the four `http.Error` bodies `PageHandler` writes
(`"unauthenticated"`, `"cannot render the page"`) are not error-authoring sites
by §2.1's rule — they are HTTP status text, and the graded sentence is the one
that reaches `Config.Logger`. The two `panic` strings in `PageHandler` and `Mux`
are not errors either; they are startup assertions on a literal, in the shape
`http.ServeMux.Handle` already uses, and §2.1's census does not cover panics.
Both exclusions follow the existing rule rather than widening it, and **§6 should
be read as gaining a fourth weakness here**: a `panic` carrying a sentence is a
message an operator reads, and this document has no opinion about any of them.

### 3.3.2 `live` — the page shell (`live/document.go`), added at revision 4, second row at revision 5

Two sites, both new, and neither existed when the walk was made. `S` and `C` are
`n/a (page request)` for exactly the reason §3.3.1 gives: a page render has no
session and cannot have one, and no event caused it.

| Site | Message as it reads today | S | C | N | Verdict |
|---|---|---|---|---|---|
| `live/document.go:275` | *"gotth-live: (\*live.App).Document was given an empty title: a document's title is application content and this component has no default for it — pass the page's own title as the second argument"* | n/a (page request) | n/a (page request) | ✓ — names the parameter by position, and says why there is no default rather than only that there is none | PASS |
| `live/document.go:258` | *"gotth-live: live.Script was rendered inside the head content of (\*live.App).Document, which renders the runtime's script tag itself and renders it BELOW the dev inspector's: a second tag from here lands above the inspector, both are deferred, deferred scripts run in document order, and a runtime that opens its socket before the inspector wraps WebSocket leaves the inspector showing nothing at all. Delete this call — Document emits the runtime, the inspector and the dev-reload tag in the one order that works. A page that must place its own runtime tag is a page that is not using Document's: pass live.NoRuntime as the mount path, which makes Document emit none of the three"* | n/a (page request) | n/a (page request) | ✓ — **two** next steps, and they are different acts rather than one restated: delete the call, or, if the page genuinely owns its runtime tag, change `Document`'s mount path to `NoRuntime`. Added at revision 5 | PASS |

**2 PASS, 0 FAIL.** Both are `errors.New` at package level for the two reasons
§3.3.1's pair are: one sentence wherever it surfaces, and a message a human
wrote is a message §5's census counts. They reach an application author as a
value through `templ.Component.Render`, and through `PageHandler` they reach an
operator as a 500 and a log line, so they are graded on the harder of the two
paths — the one where the library is not the last reader.

**The second one is unusually long and that is deliberate**, on the same rule
§4.4 applied to the four next-step clauses it fixed: the reader is holding a
page that did not render, the cause is a *composition* they may not think of as
a mistake at all, and the message has to say why the obvious repair (moving the
tag) is not the repair. It is also the only error in this package whose
condition is a context value rather than an argument, so nothing about the call
site itself would have told them.

**What is deliberately not in this table.** `Document`'s mount-path refusals are
not new sites: it calls `normalizeMountFor`, the same function `Script` and
`Mux` already call, so the six clauses §3.3 grades are the six clauses it emits,
with `(*live.App).Document` substituted for `live.Script` as the subject. That
substitution is what the `who` parameter added at §3.3's revision was for, and
the error is graded there rather than twice.

### 3.4 `live/livetest` — the test harness (8 at revision 3; 7 at the walk)

`livetest`'s entire product is diagnostics, so FR-58 bites hardest here. All
three clauses apply: a `Client` holds the session identifier the server bound to
it.

**The `S` column of this table is revision 3's, and the reason is QA-1's F-1.**
As first graded, the five `Client` rows read *"↑ via `Client.where()`, which
prefixes **every** failure"*. `where()` was called at four places, all of them
`tb.Fatalf`, so the claim held for `Next`, `Await` and both arms of `write` and
**failed for `(*Client).NextErr`**, the exported method that returns the value
instead of failing the spec with it — the one path on which this package is not
the last reader. The grade was right about what FR-58 requires and wrong about
where the tree met it. **Line numbers below are as of revision 3**, unlike the
rest of §3.

| Site | Message as it reads today | S | C | N | Verdict |
|---|---|---|---|---|---|
| `live/livetest/client.go:302` | *"livetest: `<name>` (session `<hex>`): `<what ended the wait>`"* — the wrap `NextErr` applies to every error it returns, added at revision 3 | ✓ — it **is** the session clause for this package: `Client.where()` names the client and the session the server bound to it, and it is now applied on the returned value and not only on the failures | n/a — a retrieval that produced no frame is caused by no event | ✓ — inherited: the wrap adds the subject and leaves the wrapped sentence's next step intact, which is asserted rather than assumed | PASS — new at revision 3 |
| `live/livetest/client.go:220` | *"a `<type>` message arrived and every payload on this protocol is binary: … look for a proxy or a middleware in the handler under test that is writing to the socket"* | ↑ via the wrap above, on every path: the `tb.Fatalf` failures and the value `NextErr` returns. **Clause 1 failed on the returned value until revision 3** | n/a (parse) | ✓ | PASS — **was ✗ on N**, §4.5 |
| `live/livetest/client.go:323` | *"the connection closed with no error of its own: the server closed it, so read the close code in the server's records — a session evicted or refused looks exactly like this from here"* | ↑ same | n/a (parse) | ✓ | PASS — **was *"the connection closed"***, §4.5 |
| `live/livetest/client.go:340` | *"no frame arrived within `<d>`: the session is open and quiet, so either the transition you expected produced no patch — an identical render is suppressed — or the outbound window is full and nothing was acknowledged"* | ↑ same | n/a (parse) | ✓ | PASS — **was *"no frame arrived within 5s"***, §4.5 |
| `live/livetest/frame.go:258` | *"decoding a frame of N bytes: …: the bytes are not an encoded gotthlive.v1.Frame, so either something other than this library wrote to the socket or a spec called WriteRaw and the server answered in kind"* | ↑ same — `decodeFrame` runs on the read pump and its error reaches a caller only through `NextErr` | n/a (parse) | ✓ | PASS — **was ✗ on N**, §4.5 |
| `live/livetest/frame.go:263` | *"a frame arrived with protocol version N and this package speaks 1: livetest is compiled from the same module as the server under test, so a mismatch here means the frame came from somewhere else"* | ↑ same | n/a (parse) | ✓ | PASS — **was ✗ on N**, §4.5 |
| `live/livetest/frame.go:300` | `&Error{…}` — livetest's decoded **view** of an inbound Error frame | n/a (not an error) | n/a (not an error) | n/a (not an error) | §3.9 |
| `live/livetest/replay.go:123` | *"fragment %q rendered no component: its Render returned nil, and a live region has to return a templ component for every state — return an empty one rather than nil for the state that has nothing to show"* | n/a — `ReplayN` and `AssertDirtyComplete` drive a reducer and a render directly, with no session | n/a — same | ✓ | PASS — **was ✗ on N**, §4.5 |

**The wrap covers an error this package did not author**, which is the arm that
most needed it: when the server closes on a protocol violation, the value that
ends the stream is `coder/websocket`'s (*"received close frame: status =
StatusCode(4002)"*), and nothing in it names the connection it belongs to. It
carries the session now because the wrap is applied to whatever ended the wait
rather than to this package's own sentences.

**`livetest`'s `testing.TB` failure messages are graded too, and they are not in
the census.** It counts error *values*; a `tb.Fatalf` produces a failure string,
not an `error`. But it is the same reader meeting the same class of sentence, and
excluding them would have been the letter of FR-58 against its point. Six carried
no session and no next step and were rewritten in the same commit: `client.go`'s
`Next`, `Await`, and both arms of `write`, plus `NewClient`'s dial and
mount-snapshot failures. §4.5. Two of the six changed shape again at revision 3
and neither lost anything: `Next` prints the value `NextErr` returned rather than
composing a second prefix around it, and `NewClient`'s mount-snapshot failure
appends its paragraph to that value instead of nesting it. One prefix, applied
once, is what stops the two paths drifting apart again — and it is the property
the spec in §5 holds.

### 3.5 `internal/obs` (4)

| Site | Message | S | C | N | Verdict |
|---|---|---|---|---|---|
| `internal/obs/metrics.go:213`, `:223`, `:230`, `:237` | *"gotth-live: could not create the metric %s: %w: check the meter provider"* | n/a (construction) | n/a (construction) | ✓ | PASS ×4 |

### 3.6 `internal/session` (8)

| Site | Message as it reads today | S | C | N | Verdict |
|---|---|---|---|---|---|
| `internal/session/actor.go:253` | *"gotth-live: session `<id>` closed before it was established: the mount transition did not produce a snapshot, so read this session's earlier Error record for the mount failure rather than retrying here"* | ✓ | n/a — nothing this session did has an identifier yet; the mount transition is the causal root and it is what failed | ✓ | PASS — **was *"gotth-live: session closed before it was established"***, §4.3 |
| `internal/session/actor.go:496` | `span.RecordError("gotth-live: the reducer panicked: <value>")` | ↑ the span carries `session_id` as an attribute, set at `Start` | ↑ the span carries `event_id` and `transition_id` | ✓ — the paired log record says *"the transition was not applied and the session survives"* | PASS |
| `internal/session/actor.go:741` | `errFragmentRender`, a span sentinel: *"the fragment could not be rendered: that region is stale and the log record for this transition carries the panic"* | ↑ span attribute | ↑ span carries `transition_id` and `fragment_id` | ✓ — it points at the record that holds the panic value, which §6.4 keeps off the span | PASS |
| `internal/session/effects.go:27` | `ErrSessionSaturated`: *"the session mailbox is full: back off and emit again, or raise Config.Limits.MailboxDepth"* | ✓ via the wrapper at `:45`, which is the only way it is returned | ✓ via the wrapper | ✓ | PASS — **was ✗ on all three**, §4.1 |
| `internal/session/effects.go:33` | `ErrSessionClosing`: *"the session is closing and will accept no further events: return from the effect rather than retrying, because nothing this session emits from here reaches a reducer"* | ✓ via the wrapper | ✓ via the wrapper | ✓ | PASS — **was ✗ on all three**, §4.1 |
| `internal/session/effects.go:45` | `emissionRefused`: *"gotth-live: session `<id>`: the event emitted by effect %q (scheduled by event N) was dropped: `<sentinel>`"* | ✓ | ✓ | ✓ via the wrapped sentinel | PASS — **new at `ba5ce082`**, §4.1 |
| `internal/session/window.go:115` | *"gotth-live: acknowledged sequence N was never emitted (highest is M): acknowledge only patches this session sent"* | ↑ via `onAck`'s `Error` record, which carries `session_id` and `server_seq` | ✓ — `server_seq` is the causal identifier of an acknowledgement; no event exists | ✓ | PASS |
| `internal/session/window.go:120` | *"gotth-live: acknowledged sequence N is below the high-water mark M: an acknowledgement is cumulative and never goes backwards"* | ↑ via the same record | ✓ | ✓ | PASS |

### 3.7 `internal/render` (13; 8 at the original walk)

Keyed collections added two authoring sites on 2026-09-17: the startup namespace
refusal and one combined child-projection refusal, taking the count from 8 to 10.
On 2026-09-18 the collection renderer split that combined refusal into four
specific messages: invalid child namespace, duplicate child ID, missing Render,
and unsupported nested Children. The current count is **10 − 1 + 4 = 13**;
the historical totals above remain dated. The five collection-related sites
are graded individually below.

The startup refusal names both overlapping region IDs; no session exists at
construction. Each child refusal names the parent and child and identifies the
required correction. `collectionChildren` carries it in a `Failure` to
`Actor.noteRenderFailures`, whose log record supplies `session_id`,
`fragment_id`, `event_id` and `transition_id`. Production frames retain the
generic render error; the detailed correction is in that operator log.


| Site | Message as it reads today | S | C | N | Verdict |
|---|---|---|---|---|---|
| `internal/render/registry.go:95` | *"gotth-live: an application declares no fragments: declare at least one live region"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `:101` | *"gotth-live: fragments[i] declares no ID: every live region needs a stable identity"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `:104` | *"gotth-live: fragments[i] (%q): %w"* — the wrapper that gives `:129` and `:139` their index and identity | n/a (construction) | n/a (construction) | ↑ via the wrapped message | PASS |
| `:107` | *"gotth-live: fragments[i] (%q) declares no Render: a live region must render"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `:110` | *"gotth-live: fragments[i] and fragments[j] both declare the fragment ID %q: give each live region a distinct identity"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `:129` | *"a fragment ID is at most 64 bytes and this one is N: shorten it — the bound is the wire schema's, so a longer identity is a patch this library builds and then refuses to send"* | n/a (construction) | n/a (construction) | ✓ | PASS — **was *"a fragment ID is at most 64 bytes, this one is N"***, §4.4 |
| `:139` | *"a fragment ID may hold only letters, digits and `_:.-` , not %q: remove that byte — the charset is the wire schema's, and a patch naming this region could not be sent"* | n/a (construction) | n/a (construction) | ✓ | PASS — **was ✗ on N**, §4.4 |
| `internal/render/renderer.go:147` | `errWriterEscaped`: *"gotth-live: a fragment wrote to its io.Writer after Render returned: … so build the markup during the call rather than retaining the writer"* | ↑ `callRender` turns it into that fragment's `Failure`, and the actor's record carries `session_id`, `fragment_id`, `event_id` | ↑ via the same record | ✓ | PASS |
| `internal/render/registry.go:127` | *"gotth-live: fragment %q overlaps the child namespace of %q: use disjoint region identities"* | n/a (construction) | n/a (construction) | ✓ — choose disjoint identities | PASS |
| `internal/render/collection.go:76` | *"gotth-live: child %q must have a nonempty ID suffix inside %q's colon namespace"* | ↑ via `Actor.noteRenderFailures` | ↑ via the same record | ✓ — use the parent's colon prefix and a nonempty suffix | PASS |
| `:78` | *"gotth-live: child %q is repeated in collection %q: child IDs must be unique"* | ↑ via `Actor.noteRenderFailures` | ↑ via the same record | ✓ — give each child a distinct ID | PASS |
| `:80` | *"gotth-live: child %q of %q must declare Render"* | ↑ via `Actor.noteRenderFailures` | ↑ via the same record | ✓ — supply the child's renderer | PASS |
| `:82` | *"gotth-live: child %q of %q cannot declare Children: nested collections are not supported"* | ↑ via `Actor.noteRenderFailures` | ↑ via the same record | ✓ — remove the nested collection declaration | PASS |

### 3.8 `internal/wsx` (10)

| Site | Message as it reads today | S | C | N | Verdict |
|---|---|---|---|---|---|
| `internal/wsx/handler.go:104` | *"gotth-live: no authentication hook: set one, or opt out explicitly"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `:106` | *"gotth-live: no CSRF hook: set one, or opt out explicitly"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `:108` | *"gotth-live: no application: this is a library bug"* | n/a (construction) | n/a (construction) | ✓ — "this is a library bug" is the actionable step: report it, and stop looking at your own Config | PASS |
| `:110` | *"gotth-live: no allowed origins: set them, or opt out explicitly"* | n/a (construction) | n/a (construction) | ✓ | PASS |
| `:339` | *"gotth-live: the server is draining and is accepting no new sessions: this is Handler.Close in progress, so the client should reconnect to another instance rather than retry this one"* | n/a (pre-session) | n/a (pre-session) | ✓ | PASS — **was *"the server is draining"***, §4.2 |
| `:344` | *"gotth-live: the process is at its session limit of N: raise Config.Limits.MaxSessions, or add capacity — every slot is a live connection, not a queued one"* | n/a (pre-session) | n/a (pre-session) | ✓ | PASS — **was ✗ on N**, §4.2 |
| `:350` | *"gotth-live: identity %q is at its session limit of N: raise Config.Limits.MaxSessionsPerIdentity … a browser that reconnects without closing the old socket reaches this legitimately"* | n/a (pre-session) — the identity is named instead, and it is the only handle a refused upgrade has | n/a (pre-session) | ✓ | PASS — **was ✗ on N**, §4.2 |
| `:411` | *"gotth-live: session `<id>` lost the race with Handler.Close and was not registered: the connection is closed with going-away and the client reconnects; no application state existed yet, so nothing was lost"* | ✓ — this one **can** name it: `mintID` has run | n/a — no frame has been exchanged | ✓ | PASS — **was *"the server is draining"*, and was discarded without being logged**, §4.2 |
| `:461` | *"gotth-live: %d sessions had not finished draining: raise the deadline or investigate a stuck effect"* | n/a — the subject is the process, not one session; the count is the fact | n/a | ✓ | PASS |
| `:479` | *"gotth-live: could not mint a session identifier: %w: the system's random source is unavailable, which is a host problem rather than a library or client one — no session can be created until it is back"* | n/a (pre-session) — the identifier is what failed to exist | n/a (pre-session) | ✓ | PASS — **was ✗ on N, and was discarded without being logged**, §4.2 |

### 3.9 `internal/protocol` (40)

The largest group, and the one where the Class-A/Class-B distinction does the
most work. Nothing in this package holds a session: `ParseInbound` is a function
of bytes and limits, and `ValidateOutbound` is a function of a frame. The
session is added by the two callers, and both add it.

**The 15 `reject()` sites** (`inbound.go:111` … `:247`) each author the `Detail`
of a `RejectError`. Reached by a reader through `wsx/conn.go`'s `rejected`,
which logs at `Warn` with `session_id`, `reason` and the error, and answers the
client with an `Error` frame carrying the same `Detail`.

| Site | Detail | S | C | N | Verdict |
|---|---|---|---|---|---|
| `inbound.go:111` | *"frame of N bytes exceeds the M byte inbound limit: lower the payload or raise Limits.MaxInboundFrameBytes"* | ↑ via `conn.rejected`'s record | n/a (parse) | ✓ | PASS |
| `:118` | *"payload is not an encoded gotthlive.v1.Frame: send only binary frames carrying one encoded Frame"* | ↑ | n/a (parse) | ✓ | PASS |
| `:124` | *"frame envelope violates its schema: correct the offending field"* — the offending field is named by the wrapped `*refine.Error` | ↑ | n/a (parse) | ✓ | PASS |
| `:134` | *"payload kind %q is server-to-client only: a client may send event, ack, heartbeat, client_telemetry or resync_request"* | ↑ | n/a (parse) | ✓ | PASS |
| `:144` | *"enum field is outside its declared domain: send a declared, non-zero value"* | ↑ | n/a (parse) | ✓ | PASS |
| `:147` | *"repeated field exceeds its cardinality bound: send fewer elements"* | ↑ | n/a (parse) | ✓ | PASS |
| `:157` | *"event violates its schema: correct the offending field"* | ↑ | n/a (parse) | ✓ | PASS |
| `:164` | *"event field violates its schema: correct the offending key or value"* | ↑ | n/a (parse) | ✓ | PASS |
| `:174` | *"ack violates its schema: server_seq must be positive"* | ↑ | n/a (parse) | ✓ | PASS |
| `:182` | *"heartbeat violates its schema: echo the server's nonce and interval_ms verbatim"* | ↑ | n/a (parse) | ✓ | PASS |
| `:190` | *"client telemetry violates its schema: report a real patch_id and durations under 60 seconds"* | ↑ | n/a (parse) | ✓ | PASS |
| `:198` | *"resync request violates its schema: last_applied_seq must be positive"* | ↑ | n/a (parse) | ✓ | PASS |
| `:215` | *"payload kind %q has no parse case: this is a library bug, not a client problem"* | ↑ | n/a (parse) | ✓ | PASS |
| `:230` | *"protocol version N is not supported by this server, which speaks version 1: upgrade the client runtime"* | ↑ | n/a (parse) | ✓ | PASS |
| `:247` | *"frame names a session that is not the one bound to this connection: send the session_id from the first Snapshot"* | ↑ | n/a (parse) | ✓ | PASS |

**`RejectError` itself** — `reject.go:65`, the one literal — renders as
*"gotth-live: rejected inbound frame (`<reason>`): `<detail>`: `<cause>`"*. Its
doc comment has cited FR-58 since it was written. **PASS.**

**The 11 `invariants.go` and 2 `limits.go` predicates.** These are pure
functions over a proto message: `n/a (pure)` on `S` and `C`, and none carries a
next step of its own. They are reached in exactly two ways, and each supplies
one:

- inbound, through `ParseInbound` → `reject(…)` at `:144`/`:147`, whose Detail
  is *"send a declared, non-zero value"* / *"send fewer elements"*;
- outbound, through `ValidateOutbound` → `Framer.Encode` → `InvalidFrameError`,
  which renders *"gotth-live: refusing to send an invalid `<kind>` frame: `<this
  message>`: this frame was built by the server, so it is not a client
  problem"*.

That closing clause is the next step for the operator who meets it: stop
investigating the client, and report it. **All 13 PASS ↑ via their wrapper**, and
§7.2 says what would have to change for that to stop being true.

| Site | Message | Verdict |
|---|---|---|
| `invariants.go:42` | *"%s: map fields are not permitted in this schema"* | PASS ↑ |
| `:111` | *"%s: the unspecified enum value 0 is never valid on the wire"* | PASS ↑ |
| `:114` | *"%s: %d is not a declared value of %s"* | PASS ↑ |
| `:139` | *"gotthlive.v1.Origin: a patch without an origin is an orphan patch"* | PASS ↑ |
| `:143`, `:147` | *"gotthlive.v1.Origin: kind %s requires event_id/client_ref %s, got %d"* | PASS ↑ ×2 |
| `:167` | *"gotthlive.v1.Error: nil payload"* | PASS ↑ |
| `:170` | *"gotthlive.v1.Error: event_id and client_ref must both be set or both be zero, got %d and %d"* | PASS ↑ |
| `:184` | *"gotthlive.v1.Snapshot: nil payload"* | PASS ↑ |
| `:190`, `:195`, `:203`, `:206` | the four supersession-range invariants, each stating the rule and the values it saw | PASS ↑ ×4 |
| `limits.go:86` | *"%s: repeated field has no H-4 cardinality bound"* | PASS ↑ |
| `limits.go:89` | *"%s: %d elements exceeds the bound of %d"* | PASS ↑ |

**`outbound.go`, 7 sites.**

| Site | Message as it reads today | S | C | N | Verdict |
|---|---|---|---|---|---|
| `outbound.go:27` | *"gotth-live: outbound frame is nil: construct a frame before sending it"* | ↑ via the actor's `Error` record, which carries `session_id`, `patch_id`, `transition_id` | ↑ via the same record | ✓ | PASS |
| `:77` | *"gotth-live: outbound frame carries no payload: set exactly one member of the payload oneof"* | ↑ | ↑ | ✓ | PASS |
| `:80` | *"gotth-live: outbound frame carries payload kind %q, which the server never sends: this frame was built by the server, so it is a library bug — report it with the kind above; a client cannot cause this"* | ↑ | ↑ | ✓ | PASS — **was ✗ on N**, §4.4 |
| `:225`, `:232`, `:249` | the three `&InvalidFrameError{}` literals — *"gotth-live: refusing to send an invalid %s frame: %v: this frame was built by the server, so it is not a client problem"* | ↑ | ↑ | ✓ | PASS ×3 |
| `:251` | *"gotth-live: an encoded frame with no bytes: obtain one from Framer.Encode rather than constructing it"* | ↑ | ↑ | ✓ | PASS |

**`frames.go:232` — two sites, and they are not errors.** `NewError` builds the
`Error` **frame** a client receives, so the census's "type name ends in Error"
rule catches `&pb.Frame_Error{}` and `&pb.Error{}`. Kept as rows rather than
special-cased out, because a rule that is over-inclusive costs two documented
lines and a rule that is under-inclusive costs a missed error.

They are worth a paragraph anyway, because the *frame's* message is the one
thing a browser ever reads and it is governed by a different rule than FR-58's:
`instrumentation.md` §6.4 forbids putting a panic value or application state on
the wire, so `Actor.devMessage` sends a fixed generic string in production and
the detail only in `Config.Dev`. **FR-58 governs the server-side error; §6.4
governs what the client is told.** They are not in tension — the operator's copy
is complete either way — and `live/core.go`'s doc comment on
`EffectFailedErrorField` is where that trade is written down for applications.

### 3.10 `internal/cmd/gotth-live-dev` (3)

| Site | Message as it reads today | S | C | N | Verdict |
|---|---|---|---|---|---|
| `main.go:80` | *"-dir %q could not be resolved to an absolute path: …: pass the directory of the module you want watched, or leave -dir unset to watch the current one"* | n/a — a watcher process, no session anywhere | n/a | ✓ | PASS — **was *"-dir: %w"***, §4.6 |
| `main.go:87` | *"could not create the scratch directory the rebuilt binary is written to: …: check TMPDIR and the space behind it"* | n/a | n/a | ✓ | PASS — **was a bare returned `os.MkdirTemp` error**, §4.6 |
| `watch.go:197` | *"%s is a file and this watches a directory: point -dir at the module directory holding your application, not at one of its files"* | n/a | n/a | ✓ | PASS — **was `&os.PathError{Op: "watch"}`, rendering as *"watch `<path>`: invalid argument"***, §4.6 |

---

## 4. The 25 that failed, and the four defects the same walk found

**29 changes**, landed at **`ba5ce082`** (27) and **`4d28146f`** (2, found while
writing the census walk).

The arithmetic, because §3's tables and this section count different things: **25
enumerated sites** (6 in §4.1, 5 in §4.2, 1 in §4.3, 4 in §4.4, 6 in §4.5, 3 in
§4.6) plus **4 non-sites** — the three log records in §4.3 and the silent
rejection arm at the end of §4.2. A log record authors no error value, so it is
not in the 117; it is where an error the library already built reaches a reader,
which is what FR-58 is about.

### 4.1 Six errors handed to application code that named no session — the worst of them

The `Emitter`'s four refusals and the two mailbox sentinels are the **entire**
error surface an effect can hold. None of the six named the session, and none
named the event the effect descended from. An effect fanning out across sessions
learned *"gotth-live: the session mailbox is full: the emitted event was
dropped"* and had no handle on which session that was.

- The two sentinels stay sentinels and are **wrapped rather than replaced**, so
  `errors.Is` still reaches them. Their own text now carries the next step, which
  is the half that is the same every time; `Actor.emissionRefused` adds the
  session, the effect source and the causal clause.
- `live`'s four gain the same subject clause, built once in `emissionContext`.

**This needed the causal identifier, and it was not at the site.** The errors are
raised *before* the emitted event has an identifier of its own — which is the
literal subject of the first of the four. The identifier that exists is the
event whose transition returned the effect, and `internal/session`'s
`App.Execute` now takes it as a parameter, because nothing at that boundary can
re-derive it. `App` is an internal interface with one implementation, so no
exported symbol changed and `docs/api-surface.md` has no row to add.

### 4.2 Five in the transport, three of them silent

- `admit`'s three refusals (`the server is draining` / `the process is at its
  session limit of N` / `this identity is at its session limit of N`) had no
  `gotth-live:` prefix and no next step. All three now say what to change or why
  not to.
- **`register`'s refusal was constructed and then discarded at the call site.**
  An operator saw a connection close with going-away and had nothing to join it
  to. It is now logged, and it names the session, which it can: `mintID` has run
  by then.
- **`mintID`'s error was discarded behind a bare 500.** Also now logged, at
  `Error`, because `crypto/rand` does not fail on a healthy machine.

A sixth, adjacent and in the same commit — and the fourth non-site:
`conn.rejected`'s non-`RejectError` arm closed the connection in silence. It is unreachable while `ParseInbound`
returns only `*RejectError` — a conformance spec holds it there — but the
alternative to logging in that arm is a library error that reaches no reader,
which is precisely what FR-58 names.

### 4.3 Three log records holding a causal identifier and dropping it

This is `8fb6ade9`'s shape — checkpoint 2's own FR-58 fix — three more times:

- **`noteRenderFailures`** takes `eventID`, puts it on the `Error` frame, and
  left it off the record an operator reads first. A stale region could be read
  off the log and the click behind it could not.
- **`emitError`** did the same with the interaction whose failure never reached
  the client — the one case where the server-side record is the *only* surviving
  copy of that edge, because the frame carrying it is what failed to go out.
- **`resync`**'s unanswered-request line named no sequence at all, so a reader
  could not tell a client re-asking from the same cursor from one making
  progress.

Plus one that IS an enumerated site: `Actor.Ready`'s *"gotth-live: session closed
before it was established"*, which named neither the session nor what to read
instead. It is the 1 this subsection contributes to the 25; the three records
above it are three of the 4 non-sites.

### 4.4 Four next-step clauses on messages that had "what" and "why" and stopped

The two fragment-ID rules in `internal/render`; the outbound payload kind the
server never sends; and `renderAdapter`'s nil-component error, which said *"a
fragment"* on a page that may declare nine and now names which — because that
value travels through `Config`'s own render hook and can be wrapped by an
application before the log record carrying the identity is written.

### 4.5 `live/livetest` — six error values and six `testing.TB` messages

`Client.where()` is new: it prefixes every failure with the client's name and
`(session <hex>)`. Before it, a spec driving two clients failed with *"livetest:
b: no frame arrived within 2s"*, and an operator reading the server's records
beside it had no way to say which of the two sessions in them was `b`.

The six `testing.TB` messages rewritten alongside are `Next`, `Await`, both arms
of `write`, and `NewClient`'s dial and mount-snapshot failures. They are not in
the 117 — the census counts error values — and they are graded because it is the
same reader meeting the same sentence.

**Revision 3's addendum, and it is the part this subsection got wrong.** *"Every
failure"* meant every `tb.Fatalf`. The exported `(*Client).NextErr` returned the
same sentences with no prefix at all, so the value a caller held — the path where
a spec can store it, wrap it, or log it beside the server's own records — was
exactly the diagnostic this remediation says it replaced. §3.4 carries the
correction and the count; what belongs here is the shape of the mistake, because
it is a shape rather than an incident: a helper introduced on the failure paths,
graded by reading the helper, and never followed to the path that returns.

### 4.6 Three in the dev watcher

Including `&os.PathError{Op: "watch"}`, which renders as *"watch `<path>`:
invalid argument"* — a true sentence that tells the person who typed `-dir`
nothing at all.

---

## 5. What is enforced from here, and what is not

Four files, and the split between them is the argument.

| File | What it holds | What it cannot hold |
|---|---|---|
| [`live/fr58_test.go`](../live/fr58_test.go) | The errors that reach application code **as values**, driven through the paths that build them: the Emitter's four refusals, 11 `ConfigError` shapes, both denial types, `Script`'s six mount refusals. Where a session is named it asserts it is the **right** session — the identifier the client on the other end holds — and the `ConfigError` rows assert the **opposite**, that no session is named, so §3's `n/a (construction)` is falsifiable rather than assumed | Anything about whether a next step is *good*. It asserts the specific instruction is still present, per row |
| [`internal/session/emission_internal_test.go`](../internal/session/emission_internal_test.go) | The two mailbox sentinels at the seam, including that `errors.Is` still reaches them through the wrapper. **A table-driven standard-library test, not Ginkgo**, and the file says why: the package clause is `package session`, what is under test is a pure function, and `union_internal_test.go` is the same shape for the same reason | The behaviour that reaches it — that is `backpressure_test.go`'s subject |
| [`live/livetest/client_test.go`](../live/livetest/client_test.go) | **The harness's own session clause**, which nothing held until QA-1 filed F-1. Three specs under *"Client: FR-58 on the error NextErr returns"* drive a real session and assert that the **returned** error names the session **this client holds** — `(session %x)` built from `SessionID()`, not a hex run — on the timeout arm and on the transport's read error, and that `Next`'s failure string is byte-identical to the value `NextErr` returned with `livetest: ` in it exactly once. Mutated by dropping the wrap: all three go red | The judgement. It holds that the prefix is applied and that the wrapped sentence's next step survives it, not that either is well written |
| [`internal/arch/errors_test.go`](../internal/arch/errors_test.go) | **Coverage.** 121 sites across 8 packages by the §2.1 rule (117 at the walk, 118 at revision 2, 119 at revision 3, 120 at revision 4; see the revision notes), plus the 8 out-of-scope packages as an exact set. Adding an error fails it, with a message that says to grade the new one and give it a row here. **It has now fired four times** — on FR-53's landing, which produced §3.3.1; on revision 3's wrap, which produced §3.4's first row; on the page shell, which produced §3.3.2; and on PS-1's repair, which added §3.3.2's second row | Any grade at all. It counts |

**Why the census counts instead of grading.** "The actionable next step" is a
judgement about whether a sentence tells a reader what to do. Every automatic
proxy — count the colons, look for an imperative verb, require a minimum length
— is a rule a bad message passes and a good one fails. Writing one would have
produced a gate that is green because it is weak, which is worse than no gate
because it looks like one. The grading is here, done by a person, with the
method above it.

**No `ci.sh` step is owed.** All four run inside the `go test ./...` steps that
already exist.

---

## 6. Where this document is weakest

Stated so a reviewer can go straight to it.

1. **The `↑ via` grades rest on a claim about composition** — that the wrapper
   named in the row is the only way that message reaches a reader. That is true
   at `2ab0cd57` and nothing enforces it. A future caller of
   `protocol.ValidateOutbound` that logs the raw error would silently turn a
   dozen PASS rows into failures. See §7.2. **This weakness has now been paid
   out once, and in the weaker direction: the claim was not merely unenforced,
   it was untrue when written.** §3.4's `↑ via Client.where()` described the
   `tb.Fatalf` paths and not `NextErr`, which is the second way that message
   reached a reader — QA-1's F-1, corrected in code at revision 3 and now under
   §5's fourth guard. A row's `↑` is a claim about *every* path out, and it is
   worth checking the returning one first, because that is the path on which the
   library is not the last reader.
2. **The generated-code exclusion has the same shape.** 45 messages in
   `gotthlivepb` are excluded because every path out of the refinement boundary
   is wrapped. That is a property of the current call graph, not of the
   generator.
3. **`live/app.go:342` is a judgement call** and §3.2 says so in the row.
4. **Sixteen `Error`-level log records were graded, and the other 18 records
   were not.** The 18 `Warn`, `Info` and `Debug` records were read but not
   tabulated,
   on the rule that FR-58 is about errors and `instrumentation.md` §6.3 makes
   `Error` the level an operator acts on. Two `Warn` records were fixed anyway
   because they were dropping an error entirely (§4.2). **This is the largest
   thing a reviewer could reasonably ask to be widened.**
5. **The line numbers rot.** §2.1 says why the guard counts per package instead.

---

## 7. Findings that belong to somebody else

### 7.1 An application cannot `errors.Is` the two mailbox refusals

`ErrSessionSaturated` and `ErrSessionClosing` live in `internal/session`. An
effect receives them through `live.Emitter` and **cannot import the package that
declares them**, so the only thing it can do with the classification is match on
the string — which the library's own doc comments elsewhere call a defect
waiting for its first refactor.

`docs/guide/_samples/effects/effects.go:163` and three examples all handle this
with a comment reading *"The mailbox was full, or the session is closing"* and
no branch, which is the workaround the missing symbols force.

FR-58 is satisfied without them: the message names all three clauses. But an
error an application can read and cannot classify is an API question, and
exporting two sentinels from `live` is an exported-surface change needing a
`docs/api-surface.md` row and a ruling. **Owner: PM-1 to rule, then DEV-1 or
DEV-2 to land.** Not done here, because scope creep inside an audit is how an
audit stops landing.

### 7.2 Nothing enforces that the wrapped messages stay wrapped

§6.1 and §6.2, restated as a request. The cheapest guard is an `internal/arch`
assertion that `protocol.ValidateOutbound` and `protocol.checkFieldInvariants`
have exactly the callers they have today, in the shape of the existing
import-graph assertions in that package. **Owner: DEV-1**, and it is not in this
landing because it is a new architectural claim rather than an audit finding.

### 7.3 A stray root-owned empty directory in the worktree

`gotth-live/gotth-live/{docs,examples,tools}` exists, is owned by `root`, and is
empty. It is a container-mount artifact rather than anything the repository
tracks — `git status` does not see it — but it makes `find` and `grep -r` from
the worktree root report doubled paths, which is how it was noticed. **Owner:
whoever runs the container mounts.** Not deleted here: it is `root`-owned and
outside anything this landing touches.

---

## 8. Statement

Every library-produced error in the published module is enumerated in §3, by the
method in §2, which is committed and re-runnable. 25 failed a clause that applied
to them and 25 were fixed, alongside four defects that are not enumerated sites.
Every clause marked inapplicable carries the reason it
is inapplicable, and the two most common of those reasons are asserted rather
than asserted-about: `live/fr58_test.go` fails if a construction-time error
starts naming a session, and `internal/arch/errors_test.go` fails if the set
being graded stops being the whole set.

**§6 is the honest part of this document and should be read before §8 is
believed.** In particular, this audit graded the 16 `Error`-level log records and did
not tabulate the 18 at lower levels (counted by
`grep -rn 'log\.Error(ctx\|Logger\.Error(ctx' internal/ live/ | grep -v _test`
and its Warn/Info/Debug counterpart), and roughly a quarter of the PASS verdicts
rest on composition rather than on the message's own text.

— **DEV-1**, 2026-08-05, at `2ab0cd57`. Awaiting QA-1's grade per FR-58's gate
line.
