[identifiers genericized for publication - measurements unmodified]

# Phase 4 grading pass — three boxes, graded by QA-1

**Date:** 2026-08-05
**Tree graded:** `091dbae878b545c1f42df1b51f1584f5c79fc01c` (branch
`dev-/gotth-live-orchestrator-c3efc4`). The branch moved to `d0d4cf4e` while
this pass was running; §6 says exactly what that changed and what it did not,
and one finding below is *about* `d0d4cf4e`.
**Graded by:** QA-1, merge-blocking authority.
**Requested by:** [`docs/gates/phase-4.md`](../gates/phase-4.md) §8.1 —
*"Four deliverables are sitting in a queue in front of QA-1 … The single act
that moves this phase furthest is asking QA-1 to grade."*

---

## 1. Verdicts

| Phase-4 box | Criterion | Verdict |
|---|---|---|
| **12** | **FR-58** — every library-produced error names the session, the causal ID where one exists, and the actionable next step. Artifact: [`docs/error-audit.md`](../error-audit.md) | **PASS**, with one condition (§2.6) |
| **7** | **G11** — consumable from a clean clone. Artifact: [`docs/qa/g11-clean-clone.md`](g11-clean-clone.md), runner `tools/g11/{run,inside}.sh`, `ci.sh:876` | **PASS**, no conditions |
| **6** | **FR-60…FR-63** — the three examples *polished, documented, and green in CI end to end*. The **"polished and documented"** conjunct, which is the half nobody had graded | **FAIL** — on the *documented* conjunct only, in six specific places; §4 |

**Three boxes, not four.** FR-59's docs set (box 8) is **deliberately not
graded here.** DEV-3 is landing the architecture page — the ninth subject
`docs/gates/phase-4.md` §5.4 ruled not discharged by a design RFC — in a
parallel stream *right now*: `docs/guide/architecture.md` and
`docs/guide/_samples/architecture/` arrived at `d0d4cf4e` in the middle of this
pass. Grading a docs set while its disputed subject is being written would be
grading a tree that no longer exists by the time the grade is read, and the
resulting verdict would be unattributable to any commit. It stays open with
DEV-3's name on it and QA-1's grade owed after.

**I graded box 6 without DEV-3's presentation.** §8.1 records the owner line as
*"DEV-3 to present, QA-1 to grade"* and no presentation has happened. I graded
what is in the tree, as a reader meets it. §4.1 states the standard I applied
before I applied it, so that DEV-3 can contest the standard rather than only
the verdict; and §4.6 says which of the six findings a presentation could
plausibly have changed my mind about (none of them — all six are the tree
disagreeing with itself).

---

## 2. Box 12 — FR-58, the error audit. **PASS**, with one condition

### 2.1 The standard

An audit passes when four things are true, and I checked each rather than
assuming any: **(a)** its enumeration is reproducible by somebody who did not
write it and reproduces to the same number; **(b)** the messages it says ship
are the messages that ship; **(c)** the guards it puts up can actually go red;
**(d)** its own account of where it is weak is checkable rather than
decorative. PM-1 already checked that the census map sums to 117 and that the
"was …" rows count 25 (§4.12.1), so I did not re-derive those two — I did the
four above.

### 2.2 (a) The enumeration reproduces — from the document's prose, not the
document's code

I did **not** just re-run DEV-1's walk. I re-implemented §2.1's stated rule from
the prose in a scratch program, ran it against the tree, and only then compared
it to `internal/arch/errors_test.go`'s numbers. Per-package, at `091dbae8`, on
a **pristine `git clone`** of that commit rather than on this dirty worktree:

```
PER-PACKAGE COUNTS (all packages under live/ and internal/):
  internal/clientcodec                13
  internal/cmd/gotth-live-dev          3
  internal/obs                         4
  internal/protocol                   40
  internal/protocol/gotthlivepb       45
  internal/render                      8
  internal/session                     8
  internal/wsx                        10
  live                                37
  live/livetest                        7
  TOTAL (all packages)               175
  TOTAL (audit's 8 in-scope packages) 117
```

Every one of the eight in-scope numbers is the census map's number, and the sum
is **117**. Two of the out-of-scope figures the audit states in prose also fall
out of the same walk and match: `gotthlivepb`'s **45** (§2.3, *"Its 45
messages"*) and `clientcodec`'s **13** (§2.3, *"Its 13 messages"*). Those two
were not asserted anywhere by a machine and they are right.

The committed walk itself is green on the same pristine tree:

```
ok  github.com/candacelabs/candace/pkg/gotth/internal/arch    0.974s
ok  github.com/candacelabs/candace/pkg/gotth/live             0.469s
ok  github.com/candacelabs/candace/pkg/gotth/internal/session 6.395s
```

**And the 25 reproduces by section, not just in total.** §4's arithmetic claims
6 + 5 + 1 + 4 + 6 + 3. Counting §3's rows myself, keyed by the §4 subsection
each cites:

```
{'4.1': 6, '4.2': 5, '4.3': 1, '4.4': 4, '4.5': 6, '4.6': 3}  total 25
```

That is a stronger check than counting 25 rows, because it would catch a row
filed under the wrong remediation. Note for a future reader: 24 of the 25 rows
carry the literal string `**was `; the twenty-fifth is `live/app.go:539`, which
reads `**the fragment was unnamed**`. A grep for `**was ` returns 25 *including*
the headline sentence in §1, which is a coincidence, not the count.

### 2.3 (b) The rewritten messages are the ones a caller sees

I spot-checked **17 of the 25** graded failures against the shipping source,
across all six remediation subsections, and read the *call sites* for the two
that were graded as "constructed and then discarded" — because for those, the
message being right in the source proves nothing.

| Audit row | What the code at `091dbae8` says | Matches? |
|---|---|---|
| §4.1 `live/app.go:356/362/380/388` | the four Emitter refusals, all four built from `emissionContext(p, scheduledBy)` = `"session %s: an event emitted by an effect scheduled by event %d"` | yes |
| §4.1 `internal/session/effects.go:27/33/45` | both sentinels carry their next step; `emissionRefused` composes `"gotth-live: session %s: the event emitted by effect %q (%s) was dropped: %w"` | yes |
| §4.2 `internal/wsx/handler.go:339/344/350` | draining / process limit / identity limit, each with the next step | yes |
| §4.2 `handler.go:411` (`register`) | names the session — **and `handler.go:273` now logs it** at `Warn` with `session_id`, `subject`, `err`. It was discarded; it is not any more | yes |
| §4.2 `handler.go:479` (`mintID`) | **and `handler.go:214` now logs it** at `Error` with `subject` and `err` before the 500 | yes |
| §4.3 `internal/session/actor.go:253` | *"session `<id>` closed before it was established: … read this session's earlier Error record …"* | yes |
| §4.4 `internal/render/registry.go:129/139`, `internal/protocol/outbound.go:80`, `live/app.go:539` | all four carry the next-step clause §3 quotes | yes |
| §4.5 `live/livetest/client.go:295/312`, `frame.go:258` | the three long diagnostics, verbatim | yes |
| §4.6 `internal/cmd/gotth-live-dev/main.go:80/87`, `watch.go:197` | the three watcher messages, including the `*os.PathError` replacement | yes |

Nothing in the sample was overstated. The two "discarded" fixes are the ones
that could most easily have been a document-only change, and both are real code
at their call sites.

### 2.4 (c) The three guards go red — six mutations, on a throwaway copy

This is the check the project exists to do, and none of it had been done. I
copied the module to `/tmp`, broke one property at a time, and ran the guard
that claims to hold it. **Baseline on the untouched copy: all three green.**

| # | Mutation | Guard | Result |
|---|---|---|---|
| M1 | add one `errors.New` to package `live` | `internal/arch/errors_test.go` | **RED** — *"package live authors 38 error messages and docs/error-audit.md enumerates 37. An error was added, removed or moved…"* |
| M2 | add `internal/qa1probe` authoring one error | same | **RED** — *"these packages … appear on neither list … : [internal/qa1probe]"* |
| M3 | delete `internal/cmd/gen-clientcodec` (an out-of-scope entry nothing imports) | same | **RED** — *"outOfScope names internal/cmd/gen-clientcodec and no such package is in the tree: delete the entry rather than leaving an exclusion nobody can evaluate"* |
| M4 | strip the session out of `emissionContext` | `live/fr58_test.go` | **RED** ×4 — *"FR-58 clause 1: the error names no session, or names one that is not this one"* |
| M4b | name a **plausible but wrong** 32-hex session | same | **RED** ×4, same message — the file's claim that it asserts the *right* session is true |
| M5 | make `ConfigError{Field:"Init"}` name a session | same | **RED** — *"a construction-time error named a session. None exists at New: if this starts passing, the audit's per-row 'no session exists here' reason has stopped being true"* |
| M6 | `%w` → `%v` in `emissionRefused` | `internal/session/emission_internal_test.go` | **RED** ×4 — *"errors.Is does not reach the sentinel through the wrapper"* |
| M7 | delete the next step from `ErrSessionSaturated` | same | **RED** ×2 — clause 3, naming the missing string |
| M8 | drop the session from `emissionRefused` | same | **RED** ×4 — clause 1 |

All three arms of the census fire, both directions of `fr58_test.go`'s session
assertion fire (present-and-right, and absent-where-it-must-be-absent), and the
sentinel guard fires on wrapping, on the next step and on the session
separately. **These are not guards that cannot fail.**

**And one of them has now fired in anger**, which is worth more than all nine
mutations: see §6, where the census is **red at `d0d4cf4e`** on a commit that
landed two hours after the audit was graded.

### 2.5 (d) §6 "Where this document is weakest" is checkable, and checks out

The load-bearing claim is §6.4 / §8: *"this audit graded the 16 `Error`-level
log records and did not tabulate the 18 at lower levels"*, with the grep printed
in the document. Run at `091dbae8`:

```
Error  16      Warn   16      Info    1      Debug   1
16 (graded)  +  18 (not tabulated)  =  34 = every non-test log record by that pattern
```

Both numbers are exact and the two partition the whole set. A weakness section
that publishes a falsifiable number and gets it right is the opposite of
decorative. §7.3's stray root-owned `gotth-live/gotth-live/{docs,examples,tools}`
is also real and still present; §6.1, §6.2 and §6.5 are statements about
fragility rather than about facts, and §7.2 turns §6.1 into a request with an
owner, which is the correct disposal.

### 2.6 The one defect, and the condition

**§3.4 over-states the composition claim on five `live/livetest` rows, and one
of them is on an exported symbol.** Five rows grade FR-58's *session* clause as
*"↑ via `Client.where()`, which prefixes **every** failure with the client's
name and `(session <hex>)`"*. `where()` is called at exactly four places, all of
them `tb.Fatalf` paths (`client.go:324, 354, 490, 495`). The exported
`(*Client).NextErr` — a documented symbol, `docs/api-surface.md:330`, FR-63 —
returns those errors **bare**. Driven, not read:

```
the session this client holds: 11ea8a97654bbaf4c65b0ea14e83887b
NextErr, the value a caller holds:
  "no frame arrived within 300ms: the session is open and quiet, so either the
   transition you expected produced no patch — an identical render is
   suppressed — or the outbound window is full and nothing was acknowledged"
  names the session?  false
```

That is the exact defect §4.5 says `where()` was introduced to fix — *"a spec
driving two clients failed with 'livetest: b: no frame arrived within 2s'"* —
surviving on the path that hands the value to the caller instead of failing the
spec. On the `NextErr` path the message names neither the session nor the
client. Nothing in `live/fr58_test.go` covers `livetest`, so nothing catches it.

**This does not undo the audit** — it is one grade in one row family, it is the
shape §6.1 already names as the document's weakest, and the remaining 112 rows
are unaffected. It is a condition rather than a FAIL because the audit's method,
arithmetic, remediation and guards are all sound and independently reproduced.

> **Condition on box 12:** the five `live/livetest` rows in §3.4
> (`client.go:215`, `:295`, `:312`, `frame.go:258`, `:263`) are re-graded — either
> the errors carry `where()` on the `NextErr` path, or the rows say *"↑ via
> `where()` **on the `Next`/`Await` paths**; bare from `NextErr`"* and the `S`
> column is graded accordingly. **Owner: DEV-1.** F-1 below.

**Verdict: PASS.** The audit is what FR-58 asked for: a reproducible
enumeration, a grading somebody signed, 29 real changes in code, and three
guards that measurably cannot pass by accident.

---

## 3. Box 7 — G11, consumable from a clean clone. **PASS**

G11's gate is QA-1 by name, so this section is a re-run rather than a reading.
Everything below I executed on `node-b`, outside every project image.

### 3.1 The run, at HEAD, by me

```
$ bash tools/g11/run.sh          # exit 0
tree:   091dbae878b545c1f42df1b51f1584f5c79fc01c
image:  golang:1.25-bookworm golang@sha256:ea341baa…d51c9d58
date:   2026-08-05T13:14:01Z
as worded  (go run ./examples/<name>):        impossible
as documented (cd examples/counter   && go run .): PASS
as documented (cd examples/chat      && go run .): PASS
as documented (cd examples/dashboard && go run .): PASS
```

counter served after 7 s having downloaded 7 modules; chat and dashboard after
2 s having downloaded none. All three returned a page with their
`data-gotth-region` markup and **10,391 bytes** of `gotth-live.min.js` from the
URL the page itself named (`/live`, `/chat/live`, `/dashboard/live`).

### 3.2 The precondition, checked by me and not by the runner

A skip that never skips is this project's signature failure, so I did not take
the runner's word for the image. Directly, in `golang:1.25-bookworm`:

```
node ABSENT   npm ABSENT   npx ABSENT   protoc ABSENT   refinec ABSENT
templ ABSENT  buf ABSENT   protoc-gen-go ABSENT  yarn ABSENT  pnpm ABSENT
go: go1.25.12
any node binary anywhere on disk:  (none)
```

The last line is the one worth having: `find / -name node -type f -perm -u+x`
returns nothing, so this is not "off `PATH`", it is "not in the image".

### 3.3 The clone is a clean clone, checked by me

Ran `--keep` and inspected the result myself rather than reading the runner's
assertion:

```
alternates file: (does not exist)      -> shares no object store with the checkout
clone HEAD  == source HEAD             091dbae8…
shallow: true    remote: file:///home/dev/…/gotth-live-orchestrator-c3efc4
node_modules dirs:      0
built example binaries: 0
git status --porcelain --ignored: 0 lines
untracked:                        0 lines
all 7 generated files present and TRACKED
gotth-live.min.js: 10391 bytes, byte-identical to the worktree's committed file
```

10,391 is `client/SIZE.md:45`'s figure for the shipped runtime. The bytes three
examples served out of a container with no node in it are the committed file,
compared byte-for-byte, not by length.

### 3.4 Three negative controls — two of DEV-2's and one of mine

| Control | Expected | Got |
|---|---|---|
| `run.sh --deadline 1` (counter needs 7 s) | red | **exit 1**, `FAILED: examples/{counter,chat,dashboard} did not serve within 1s`, and the `ci.sh` step goes red |
| **Mine:** `run.sh --image qa1-g11-hasnode` — an image built `FROM golang:1.25-bookworm` whose only difference is a `node` shim on `PATH` | red on the **precondition**, in an otherwise-valid image | **exit 1**, `PRESENT: node -> /usr/local/bin/node (this is not a G11 environment)`, `FAILED: one of node/npm/protoc/refinec is installed: the run cannot answer G11` |
| **Mine:** the post-run "the run changed nothing in the clone" assertion — planted a modified `view_templ.go` and an untracked file in the kept clone | detected | the assertion's own command reports **2 lines**; on a pristine clone it reports 0 |

The second is the one the artifact most needed and did not have: it shows the
precondition check is load-bearing rather than a printout, in an image that is
otherwise exactly the right one. The third shows the read-only claim is a
measurement.

### 3.5 The amended criterion is the one the run establishes

PRD v0.9 row 1 rewrote G11 to: *"`git clone && cd gotth-live/examples/<name> &&
go run .` works for all three examples with no node, npm, protoc, or refinec
installed — where **works** means the process serves a page carrying its
live-region markup and the committed client runtime from the URL that page
itself names, and the run leaves the clone unchanged."* Three clauses, and the
run establishes each: markup (§3.1, region IDs printed per example), runtime
from the page's own URL (§3.1 and §3.3, byte-compared), clone unchanged (§3.3
and §3.4). PM-1 tightened DEV-2's suggested string rather than adopting it, and
the tightening is the part I would otherwise have had to ask for: DEV-2's
version would have been satisfiable by a process that starts and serves a blank
page.

I also confirmed the discrepancy is a fact about `go` and not about the tree:
`go run ./examples/counter` fails from the repository root (*"cannot find main
module"*) and from `candace/pkg/gotth/` (*"main module … does not contain package
…/examples/counter"*), and each example's `go.mod` carries its own `replace …
=> ../..`. The ordering PM-1 relies on holds: the property was green before the
sentence moved, and the sentence was unsatisfiable before the property was
measured.

### 3.6 The `ci.sh` step, and the standing gate

`grep -n "G11" ci.sh` returns **17** lines with the step at `ci.sh:876`. Its
exit-code handling is honest: `0` passes, `2` becomes an announced `SKIPPED`
recorded in `skipped+=`, and only `1` becomes a failure. **I concur with PM-1's
placement of the missing CI job (`docs/qa/g11-clean-clone.md` §7 F-3) as a
Phase 5 condition and I am not re-litigating it**: the box asks whether the
property holds, it does, measured by me today; and the file that would fix it,
`.github/workflows/`, is owned by nobody on this team. It binds at the Phase 5
box that asks the same question at the tag.

**Verdict: PASS, no conditions.** Two cosmetic defects routed (F-2, F-3); neither
touches the measurement.

---

## 4. Box 6 — the three examples, "polished and documented". **FAIL**

Green in CI is real and already verified (`ci.sh:295`, `:302`, `:322`), and I
re-ran all three suites green with `-race`. That conjunct is not in dispute.
This section grades the other two words.

### 4.1 The standard, stated before it is applied

An example set is **polished** when it is clean by the project's own tools —
`gofmt`, `go vet`, the race detector, the committed generated code regenerating
byte-identically — and when it builds and runs the way a stranger would run it.

An example set is **documented** when: every command the README gives works as
written; every factual claim the README makes about the code is true of the code
that ships; every number the README quotes is the number a reader would get; and
the example's own record of its limitations is current.

I graded the second definition strictly, on the same rule PM-1 applied to
FR-53: **a conjunction fails on its weakest conjunct**, and "documented" is not
"has documentation".

### 4.2 Polished — passes, and it is not close

| Check | counter | chat | dashboard |
|---|---|---|---|
| `gofmt -l .` | clean | clean | clean |
| `go vet ./...` | clean | clean | clean |
| `go test -race ./...` | ok 2.2 s | ok 10.4 s | ok 10.6 s |
| `templ generate` reproduces the committed `view_templ.go` | **byte-identical** | **byte-identical** | **byte-identical** |
| clones, builds and serves from a clean clone with no toolchain | PASS (§3) | PASS | PASS |

The code reads well. The three `Where to look in the source` tables are
accurate; every file they list exists and holds what they say it holds
(including `examples/dashboard/wire.go`, which is still live — `resync.go`
decodes with it, so that row is not stale even though `wire_test.go` has moved
to `livetest.Client`). The design essays — counter's *"the reducer never changes
the value"*, chat's three-fragment argument, dashboard's D-16 callout and its
backpressure ladder — are the best documentation in this repository and I want
that on the record beside the FAIL.

### 4.3 Documented — the commands work

Every documented invocation, run by me:

| Command | Result |
|---|---|
| `cd examples/<name> && go run .` ×3 | serves; §3.1 |
| `-addr`, `-origin`, `-provenance` (all three examples) | declared and parsed; `-interval`, `-htmx`, `-resync-cost` on dashboard |
| `dis run bash -c 'cd examples/<name> && go test -race ./...'` | green ×3 |
| `dis run bash -c 'cd examples/<name> && templ generate'` | exit 0, output byte-identical ×3 |
| `go run . -resync-cost 200` | ran; see below |

**The dashboard's pasted measurement reproduces to the byte.** README §"The
resync cost" pastes a block taken at commit `35d4e258` on
`node-a`. I ran the same command on a different host, in a
different image, at `091dbae8`:

```
                      README (35d4e258)        QA-1 (091dbae8, different host)
frame   min/p50/p90/max   2220/2378/2661/2939   2220/2378/2661/2939   identical
markup  min/p50/p90/max   2079/2231/2512/2790   2079/2231/2512/2790   identical
protocol overhead median  147 B                 147 B                 identical
markup by region          925 / 936 / 929       925 / 936 / 929       identical
gotthlive_resync_bytes    n=200 mean 2368.1 max 2939   same            identical
latency p50 / max         172µs / 579µs         183µs / 1.882ms       differs
```

The README says *"the bytes are reproducible and the latency is not"* and *"quote
the byte figures; treat the latency as the shape of a distribution taken on a
contended host"*. That is a prediction, and it came true on a machine the author
never touched. It is the single best-documented number in this repository.

**Counter's provenance section is true, driven rather than read.** No spec in
the counter module asserts it, so I wrote a throwaway spec into a copy of the
clone that drives one `counter.increment` through `livetest.Client` with
`-provenance`'s exact logger, and parsed the records:

```
[0] MOUNT        mount                     transition_id=1 patch_id=1 fragment_ids=[counter.value counter.controls]
[1] CLIENT_EVENT event:counter.increment   event_id=1 client_ref=1 transition_id=2 patch_id=0 fragment_ids=[]
[2] EFFECT       effect:counter.watch      transition_id=3 state_version=2 patch_id=2 fragment_ids=[counter.value]
```

Every clause of the README's narrative holds: the click's record carries
`patch_id: 0` *"because the reducer returned an effect and changed no state"*;
`client_ref` ties back to the interaction the client sent; the consequence
record names `effect:counter.watch` and exactly `["counter.value"]`. **"One click
produces two records" is accurate** — record [0] is the mount, which precedes the
click, and I am recording it here only so that nobody re-runs this and thinks
they have found a third.

### 4.4 Documented — the six places the tree disagrees with itself

**D-1. All three READMEs quote the wrong number of specs.** Measured with
`go test -race -count=1 -v ./...` and Ginkgo's own summary line:

| Example | README says | Actually runs | Off by |
|---|---|---|---|
| counter | **42 specs** (`README.md:63` and `:195`, twice) | **53** | 11 (26 %) |
| chat | **151 specs** (`README.md:232`) | **155** | 4 |
| dashboard | **70 specs** (`README.md:370`) | **72** | 2 |

Counter's is quoted twice, including in the "Where to look in the source" table,
and is out by a quarter.

**D-2. `examples/chat/README.md:242–243` states the opposite of the code beside
it.** The README's suite section reads:

> `wire_test.go` … decodes the frames with a reader written over
> `google.golang.org/protobuf/encoding/protowire` … `livetest.Client` is the
> supported answer and **is not implemented yet** — FRICTION.md item **F-1**.

`examples/chat/wire_test.go:42`, in the same directory, reads: *"livetest.Client
is the fix, and **it is now built**."* The file imports `livetest`, calls
`livetest.NewClient` at `:127`, and contains no protowire decoder at all — the
package is not imported by any non-comment line in the chat module. A reader
following this README is sent to write ~260 lines of hand-rolled codec that the
example itself deleted, and told the library's supported testing API does not
exist. This is the most serious of the six: it is wrong about the library, on
the subject FR-63 exists to serve, in the example the project points a reader at
to learn how to test.

**D-3. Both FRICTION files carry an item that is no longer true, in files that
establish their own convention for saying so.**

- `examples/chat/FRICTION.md` F-1 — *"There is no exported way to read a frame"*
  … *"It is not there today."* False at `091dbae8`. The same file marks F-4
  ***"Closed."*** and explains that the heading is kept so citations land; the
  convention exists and was not applied.
- `examples/dashboard/FRICTION.md` F-2 — *"There is **still** no exported way to
  read a frame, and now no exported way to dial one either"* … *"a `Client` that
  'lands with the benchmark harness', and it is not there."* Also false; that
  module's own `wire_test.go:216` says *"livetest.Client is the library's
  supported answer and it is now built"* and uses it 25 times. The same file
  marks F-1 ***"Resolved."***

**D-4. `examples/chat/go.mod` declares two direct requirements the module does
not use.** `go mod tidy -diff` moves `github.com/coder/websocket` and
`google.golang.org/protobuf` from the direct block to `// indirect` — the
mechanical residue of the same `livetest.Client` migration that D-2 and D-3 are
the prose residue of. This matters more here than in an ordinary module:
`docs/dependencies.md` §5 makes the examples the consumer-side dependency
measurement, so chat currently over-reports its direct dependency count by two.
(counter and dashboard tidy clean apart from a `go.sum` bump of an indirect test
dependency, `rogpeppe/go-internal` 1.13.1 → 1.14.1, which is not a defect.)

**D-5. `examples/counter/README.md:230–236` cites the wrong criterion in its own
last sentence.** *"Keyboard `+`/`-`, which C-B lists as **F-CTR-6** … but it is
not what **CTR-5** measures."* `docs/bench/equivalence-spec.md:160` is F-CTR-6
(keyboard); `:159` is F-CTR-5 (two tabs both repaint). The first citation is
right and the second is off by one, in a paragraph whose whole job is to be
precise about an unimplemented feature.

**D-6 (context, not a separate charge).** D-1 through D-4 are one event: the
migration from three hand-rolled protowire codecs to `livetest.Client` updated
the code and the test files, and left the READMEs, both FRICTION files and one
`go.mod` describing the world before it. That is why the verdict is FAIL rather
than five nits — it is a documentation set that stopped tracking its own code,
and the project has now caught the same shape in `docs/README.md`, in the PR
body, in a godoc comment and in three benchmark numbers.

### 4.5 Verdict

**FAIL, on the "documented" conjunct.** *Polished* passes and I would sign it
alone. *Green in CI end to end* passes and is already signed. *Documented* does
not: one README paragraph is flatly false about the library's testing API, two
FRICTION items are false in files that have a convention for marking exactly
that, three spec counts are wrong, one criterion citation is wrong, and one
`go.mod` disagrees with its own imports.

**What turns this box green: six edits, none of them in code.** Correct three
spec counts, rewrite `chat/README.md`'s suite paragraph, mark `chat` F-1 and
`dashboard` F-2 closed the way each file already marks one item closed, run
`go mod tidy` in `examples/chat`, and fix one `CTR-5` → `CTR-6`. **Owner: DEV-3.**
I will re-grade on request; no presentation is needed for a re-grade either.

---

## 5. Defects, routed

| # | Defect | Where | Owner |
|---|---|---|---|
| **F-1** | Five §3.4 rows grade FR-58's session clause as satisfied *"↑ via `Client.where()`, which prefixes **every** failure"*. `where()` is applied only on the `tb.Fatalf` paths; the exported `(*Client).NextErr` (`docs/api-surface.md:330`) returns them bare, with no session and no client name. Driven output in §2.6 | `docs/error-audit.md` §3.4; `live/livetest/client.go:258–324` | **DEV-1** (library/API). **Condition on box 12** |
| **F-2** | **The census is red at `d0d4cf4e`**: `package live authors 36 error messages and docs/error-audit.md enumerates 37`. `Config.Init` became optional in that commit, deleting the `*ConfigError` the audit enumerates at §3.1 `live/app.go:107`. `go test ./internal/arch/` fails, so `ci.sh` fails. The other two FR-58 guards are green — `fr58_test.go` was updated in the same commit and the census map was not | `internal/arch/errors_test.go` `errorCensus["live"]`; `docs/error-audit.md` §3.1 | **DEV-1**. Merge-blocking now, not at Phase 5 |
| **F-3** | `docs/qa/g11-clean-clone.md` §1 and §7 F-2 both say `grep -n "G11" ci.sh` *"returns fifteen lines"*. It returns **17**, both at HEAD and at `5c751ae9`, the artifact's own tree. PM-1's §4.7.1 says 17 and is right | `docs/qa/g11-clean-clone.md` §1, §7 F-2 | **DEV-2** (client/G11 runner) |
| **F-4** | `docs/gates/phase-4.md` §4.7.1 item 1 lists seven tools and ends *"A run in which **any** is present fails immediately."* Only the four G11 names are fatal; `templ`, `buf`, `protoc-gen-go` and `go-bindata` are reported advisorily. DEV-2's §3.1 and `docs/PRD.md:1960` are both precise; only the gate record's sentence is loose | `docs/gates/phase-4.md` §4.7.1 | **PM-1** (scope/record) |
| **F-5** | Three stale spec counts: counter 42→**53** (twice), chat 151→**155**, dashboard 70→**72** | `examples/*/README.md` | **DEV-3** |
| **F-6** | `examples/chat/README.md:242–243` says `livetest.Client` *"is not implemented yet"* and describes a protowire decoder `wire_test.go` no longer contains. The file next to it says the opposite and uses `livetest.NewClient` | `examples/chat/README.md` | **DEV-3** |
| **F-7** | `examples/chat/FRICTION.md` F-1 and `examples/dashboard/FRICTION.md` F-2 are both false at HEAD; each file already marks a different item *"Closed."* / *"Resolved."* | both FRICTION files | **DEV-3** |
| **F-8** | `examples/chat/go.mod` declares `github.com/coder/websocket` and `google.golang.org/protobuf` direct; nothing in the module imports either. Distorts `docs/dependencies.md` §5's consumer-side measurement | `examples/chat/go.mod` | **DEV-3** |
| **F-9** | `examples/counter/README.md:236` cites **CTR-5** where the subject is **CTR-6** | `examples/counter/README.md` | **DEV-3** |

**Nothing is routed to L9-1 from this pass.** F-1 is a re-grade of five rows,
not a deviation from FR-14/16/18, so it does not need an `exceptions.md` entry.

**Not touched by me, by rule:** `docs/gates/phase-4.md`, `docs/error-audit.md`,
`docs/exceptions.md`, `docs/PRD.md`, `docs/README.md`, `docs/quickstart.md`,
`docs/guide/**`, `examples/**`, `live/**`, `ci.sh`. Every fix above is stated
with enough precision that its owner does not have to re-derive it.

---

## 6. The tree moved under this pass, and what that changes

The branch was at `b7a03d34` when I started, `091dbae8` when I ran the
measurements, and `d0d4cf4e` when I wrote this. **All three verdicts are stated
at `091dbae8`**, and G11's run is anchored there by a `git clone` of that exact
commit rather than by this worktree.

`091dbae8` (E-2's fix) touched `docs/README.md`, `docs/guide/error-handling.md`
and one `_samples` file: no effect on any of the three boxes.

`d0d4cf4e` (DEV-1's F-4 page handler, plus DEV-3's architecture page) touched
`live/{app,config,page,templ}.go` and `docs/guide/architecture.md`. Two
consequences:

1. **F-2 above** — the census is now red. I checked the other two FR-58 guards
   at `d0d4cf4e` and both are green, and I identified the missing site precisely
   (one `&ConfigError{}` literal in `live/app.go`; the kind histogram for that
   file goes 13 → 12 `&ConfigError{}` and is unchanged in every other kind) so
   that DEV-1 does not have to bisect it. **This is the guard working in
   production rather than under my mutation**, one commit after the audit
   landed, and it is the strongest single piece of evidence for box 12's PASS.
2. **FR-59's architecture page arrived**, which is why box 8 is out of scope for
   this pass (§1).

I did not re-grade boxes 7 or 6 at `d0d4cf4e`; `examples/**` is byte-identical
across `091dbae8..d0d4cf4e`, so box 6's six findings stand unchanged, and G11's
property is about the committed file layout, which those commits did not alter.

---

## 7. What I could not check, and why

* **`ci.sh` end to end.** I ran the pieces that bear on these three boxes — the
  three example suites with `-race`, the three FR-58 guards, `gofmt`, `go vet`,
  the G11 step's runner — and not the whole gate. A full `ci.sh` was not needed
  for any of the three verdicts and would not have added evidence to any of
  them.
* **`docs/error-audit.md`'s eighteen non-`Error` log records.** §6.4 declares
  them read-but-not-tabulated. I verified the **count** is exactly right (§2.5)
  and did **not** grade the eighteen. Widening that is DEV-1's Phase 5 carry and
  I concur with PM-1 that it is not a Phase 4 condition.
* **The remaining 8 of the 25 graded failures.** I checked 17 by reading the
  shipping source and 2 of those by reading their call sites. The sample covers
  all six remediation subsections; I did not exhaustively re-verify all 25.
* **Any example in a browser.** G11 speaks HTTP; box 6's polish grade is over
  code, READMEs and commands. Whether counter's connection dot goes
  *connecting → live* in Chromium is the conformance suite's browser-labelled
  specs, not this pass. The three READMEs' "What to expect" walkthroughs are
  therefore **ungraded** by me — they are the one part of box 6 I could not
  falsify from a terminal, and a presentation from DEV-3 is exactly what would
  have covered them.
* **`-provenance` on chat and dashboard.** Driven on counter only (§4.3).
  Dashboard's `wire_test.go` asserts on provenance rows in its own suite; chat's
  `wire_test.go:544` has a `Describe("Provenance")`. Both are green in the runs
  above; I did not drive either by hand.
* **G11 offline.** The run downloads seven modules. DEV-2 records this and does
  not claim otherwise; G11 says nothing about being offline; I did not test it.
* **FR-59.** Deliberately, §1.

---

## 8. Statement

Two of the three boxes pass. FR-58's audit reproduces from its own stated
method, its remediation is in the shipping code, its three guards go red under
nine separate mutations and have now gone red once in production, and its
weakness section publishes numbers that are exactly right — one composition
grade over-states itself on an exported path, which is a condition and not a
failure. G11's property holds, measured by me from a real clone in an image I
verified myself contains no node anywhere on disk, with a negative control I
built for its own precondition; the criterion's amended text is the one that run
establishes, and the standing CI job stays where PM-1 put it.

The examples' code is the best in this repository and their documentation has
stopped tracking it in six places, one of which tells a reader that the
library's supported testing API does not exist while the file beside it uses
that API twenty-five times. **A documentation phase does not exit over a README
that contradicts the test file in its own directory**, and six edits — none in
code — close it.

— **QA-1**, correctness gate, 2026-08-05, at `091dbae8`.

---

*Reproduce this report.*

```bash
cd gotth-live

# ---- §2.2 the census, from a pristine clone of the graded commit ----
git clone --depth 1 "file://$(git rev-parse --show-toplevel)" /tmp/qa1-clone
docker run --rm -v /tmp/qa1-clone:/w -w /w/gotth-live -e GOFLAGS=-buildvcs=false \
  dis-gotth-live:latest bash -c 'go test ./internal/arch/ ./live/ ./internal/session/ -count=1'

# ---- §2.2 the 25, by section, with no toolchain ----
python3 - <<'PY'
import re; from collections import Counter
t=open('docs/error-audit.md').read().split('\n'); c=Counter()
for i,l in enumerate(t,1):
    if i==19: continue                      # the headline sentence, not a row
    if '**was ' in l or 'was unnamed**' in l:
        m=re.search(r'§(4\.\d)',l); c[m.group(1) if m else '?']+=1
print(dict(sorted(c.items())), 'total', sum(c.values()))   # -> 6,5,1,4,6,3 = 25
PY

# ---- §2.4 one of the nine mutations: the census must go red ----
mkdir -p /tmp/qa1-mut/gotth-live && cp -a go.mod go.sum live internal /tmp/qa1-mut/gotth-live/
printf 'package live\n\nimport "errors"\n\nvar p = errors.New("x")\n' \
  > /tmp/qa1-mut/gotth-live/live/qa1_probe.go
docker run --rm -v /tmp/qa1-mut/gotth-live:/w -w /w dis-gotth-live:latest \
  bash -c 'go test ./internal/arch/ -count=1'     # -> "package live authors 38 … enumerates 37"

# ---- §2.5 the weakness section's own numbers ----
grep -rn 'log\.Error(ctx\|Logger\.Error(ctx' internal/ live/ | grep -v _test | wc -l   # 16
grep -rn 'log\.\(Warn\|Info\|Debug\)(ctx\|Logger\.\(Warn\|Info\|Debug\)(ctx' \
  internal/ live/ | grep -v _test | wc -l                                              # 18

# ---- §3 G11, on a host with docker, outside every project image ----
bash tools/g11/run.sh                 # exit 0
bash tools/g11/run.sh --deadline 1    # exit 1 — the timing negative control
bash tools/g11/run.sh --keep          # then inspect the clone yourself:
#   ls <clone>/.git/objects/info/alternates      -> absent: no shared object store
#   git -C <clone> status --porcelain --ignored  -> empty
#   cmp <clone>/gotth-live/live/clientjs/gotth-live.min.js live/clientjs/gotth-live.min.js

# §3.2 the image, checked directly rather than through the runner
docker run --rm --entrypoint bash golang:1.25-bookworm -c \
  'for t in node npm protoc refinec templ buf; do printf "%-9s %s\n" "$t" "$(command -v $t || echo ABSENT)"; done
   find / -name node -type f -perm -u+x 2>/dev/null | head'

# §3.4 the precondition negative control (mine)
printf 'FROM golang:1.25-bookworm\nRUN printf "#!/bin/sh\\necho v24\\n" > /usr/local/bin/node && chmod +x /usr/local/bin/node\n' \
  | docker build -q -t qa1-g11-hasnode:latest -
bash tools/g11/run.sh --image qa1-g11-hasnode:latest   # exit 1, PRESENT: node

# ---- §4.2/§4.4 the examples ----
docker run --rm -v "$PWD/..":/w -w /w/gotth-live -e GOFLAGS=-buildvcs=false \
  dis-gotth-live:latest bash -c '
    for ex in counter chat dashboard; do
      cd /w/gotth-live/examples/$ex
      printf "%-10s " "$ex"; go test -race -count=1 -v ./... 2>&1 | grep -oE "Ran [0-9]+ of [0-9]+ Specs" | tail -1
      gofmt -l . ; go vet ./... ; go mod tidy -diff | head -20
      cp view_templ.go /tmp/b.go && templ generate && cmp view_templ.go /tmp/b.go && echo "  templ: byte-identical"
    done'
#   -> counter 53 (README says 42), chat 155 (151), dashboard 72 (70)
#   -> chat's tidy diff moves coder/websocket and protobuf to // indirect

# §4.3 the dashboard's pasted measurement, on any host
docker run --rm -v "$PWD/..":/w -w /w/gotth-live/examples/dashboard \
  -e GOFLAGS=-buildvcs=false dis-gotth-live:latest bash -c 'go run . -resync-cost 200'
#   -> the byte figures reproduce exactly; the latency figures do not, as documented

# ---- §4.4 D-2, with no toolchain at all ----
grep -n "not implemented yet" examples/chat/README.md
grep -n "it is now built"     examples/chat/wire_test.go
grep -c "livetest"            examples/chat/wire_test.go examples/dashboard/wire_test.go
grep -rn "protowire"          examples/chat/*.go          # only a stale comment

# ---- §6 / F-2: the census at the moving HEAD ----
docker run --rm -v "$PWD/..":/w -w /w/gotth-live -e GOFLAGS=-buildvcs=false \
  dis-gotth-live:latest bash -c 'go test ./internal/arch/ -count=1'
#   at d0d4cf4e -> "package live authors 36 error messages and docs/error-audit.md enumerates 37"
```

---

## 9. Re-verification, 2026-08-05, against `368132f6`

**Everything §5 routed has been worked.** This section re-grades box 6, grades
box 8 for the first time, and disposes of box 12's condition. §1–§8 above are
left exactly as they were written at `091dbae8`: the FAIL in §4.5 is the verdict
that was correct against that tree, and §9.1 is a second verdict against a
different one, not a correction of the first.

**Tree graded:** `368132f6cbbad2fe09cc6c1a6b9739c8947b3f32`, stable for the whole
of this pass (checked at the start and at the end). Everything I ran is at that
commit unless it names another.

**Method note.** No remediation is graded by reading the commit that claims it.
Every check below is a re-run, a mutation I wrote, or a value I drove out of the
shipping code and printed. Where a report told me something was held by a test,
I broke the thing and watched the test go red myself.

---

### 9.1 Box 6 re-graded — the examples' "polished and documented". **PASS**

#### 9.1.1 Polished — re-run, unchanged

`gofmt -l .` clean, `go vet ./...` clean, `go test -race -count=1 ./...` green in
all three modules (counter 2.2 s, chat 10.4 s, dashboard 10.6 s). §4.2 stands.

#### 9.1.2 The three counts, re-measured rather than taken

Ginkgo's own summary line, `-race -count=1 -v`, at HEAD:

```
counter    Ran 53 of 53 Specs   53 Passed | 0 Failed | 0 Pending | 0 Skipped
chat       Ran 155 of 155 Specs
dashboard  Ran 72 of 72 Specs
```

`counter/README.md:63` and `:195` say 53, `chat:232` says 155,
`dashboard:370` says 72. **F-5 closed**, and the numbers agree with DEV-3's
independent re-derivation and with §4.4's table.

#### 9.1.3 The `ReportAfterSuite` check — audited, not accepted

DEV-3 put the counts under a report node in each suite file. The question I was
asked is whether it can go red, and whether it can pass vacuously. **Five
controls, all mine, all on a copy of the tree in `/tmp`, none of them run by
DEV-3:**

| Control | What I did | Result |
|---|---|---|
| **A — a wrong number** | counter README `53 specs` → `54`, **on the second of its two claims only** | red, exit **1**: *"README.md says 54 specs and this suite has 53"*. It checks **every** occurrence, not the first |
| **B — vacuity** | rewrote both claims to *"fifty-three specs"*, leaving no `N specs` anywhere | red, exit **1**: *"README.md no longer makes an \"N specs\" claim, so this check is checking nothing: restore the sentence or delete this node rather than leaving a guard that cannot fail"* |
| **C — the artifact removed** | `mv README.md` away | red, exit **1**: *"cannot read README.md to check the spec count it publishes"* |
| **D — the drift it exists for** | added one trivial spec, left the README at 53 | red, exit **1**: *"README.md says 53 specs and this suite has 54"* |
| **E — the false-positive it must not produce** | `-ginkgo.focus` matching nothing, README untouched | **green**, exit 0, `Ran 0 of 53` |

**The vacuity question is answered in the strongest form available**: the guard
does not merely fail to notice a removed claim, it treats a removed claim as its
own failure and says so in a sentence that tells the next author which of the two
repairs to make. Control D is the case the check exists for and it is the one I
most expected to find unheld; it holds. Control E confirms `TotalSpecs` rather
than `SpecsThatWillRun` is the right side of the comparison — under focus it
still compared 53 against 54 rather than 53 against 1.

In every red case the failure is a **suite** failure with a non-zero `go test`
exit status, and all 53/155/72 specs passed alongside it — so the guard is not
piggy-backing on some other failure. It is in CI unfocused: `ci.sh:320`, `:327`,
`:347`.

**One fragility, not a defect, recorded for whoever edits these READMEs next:**
the regexp is `(\d+) specs\b` over the whole file, so a *second* unrelated
sentence of the form "adds 2 specs" would turn the suite red spuriously. That is
the safe direction for this check to be wrong in, and no README has such a
sentence today.

#### 9.1.4 F-6, F-8, F-9 — closed, checked at the source

- **F-6.** `examples/chat/README.md`'s suite section now describes what
  `wire_test.go` does — drives real dialled connections through
  `live/livetest`'s `Client` — and keeps the internal-import argument that made
  the deleted decoder necessary rather than lazy, filed in the past tense. The
  phrase *"is not implemented yet"* appears nowhere in `examples/` at HEAD. **Not
  a rewrite that deleted the history**, which was the risk: the paragraph that
  replaces it says *"It was not always so"* and names the item that filed it.
- **F-8.** `go mod tidy -diff` in `examples/chat` is now **empty**. The direct
  block is four requirements, `coder/websocket` and `google.golang.org/protobuf`
  are `// indirect`. I also checked the collateral DEV-3 said was not there:
  `go list -m all | wc -l` in `examples/chat` is **62**, which is the figure
  `docs/dependencies.md:529` quotes, so that measurement is undisturbed.
  (counter and dashboard still show a `go.sum`-only diff — `rogpeppe/go-internal`
  1.13.1 → 1.14.1 and one `check.v1` `h1:` line — which §4.4 already recorded as
  not a defect.)
- **F-9.** `counter/README.md:236` now reads *"it is not what CTR-6 measures"*,
  and `docs/bench/equivalence-spec.md:160` is F-CTR-6, the keyboard row. Both
  citations in that paragraph are now right.

#### 9.1.5 The seventh instance, which I missed

DEV-3 found one more copy of the same false sentence that §4.4's D-2 is about,
and it was **in code, not in a README**: `examples/dashboard/wire.go`'s header
said `live/livetest` *"documents a Client that would be the supported answer and
it is not implemented yet"* twenty lines above the paragraph explaining why the
file survives. My §4.2 read that file and graded its "Where to look in the
source" row as accurate without reading its header. **That is a miss in my own
pass**, it is the same defect class I failed the box for, and it is recorded here
rather than folded silently into a PASS.

#### 9.1.6 The F-2 adjudication — **DEV-3 is right, my remedy was wrong**

This is the one place DEV-3 declined to do what §5 asked. F-7 told them to mark
`examples/dashboard/FRICTION.md` F-2 *"Closed."* the way that file already marks
F-1 *"Resolved."* They graded it **"Closed for the specs, open for the
measurement"** instead. I checked their two grounds against the tree rather than
against their report:

| Claim in the item | What HEAD says | True? |
|---|---|---|
| `wire.go` survives as a **non-test** file because `MeasureResync` cannot use a helper that takes a `testing.TB` | `live/livetest/client.go:109` — `NewClient(tb testing.TB, …)`. `examples/dashboard/resync.go:147` `MeasureResync`, reached from `main.go:155` under `go run . -resync-cost N`, decodes with `wire.go`; `wire_test.go` no longer does | **yes** |
| the library still exports no subprotocol constant | the only `Subprotocol` in the module is `internal/protocol/limits.go:18`, which an application cannot import; `live` exports none; `examples/dashboard/wire.go:57` declares its own copy and `resync.go:186` dials with it | **yes** |

**Adjudication.** My finding was right and my prescription was wrong. What was
false at `091dbae8` was the item's *framing* — *"there is **still** no exported
way to read a frame, and now no exported way to dial one either"*, and *"a
`Client` that 'lands with the benchmark harness', and it is not there"* — and
that framing is gone. Two of the item's substantive claims were true then and are
true now. **Had DEV-3 done what I asked, the file would state something false**,
which is the defect class this box failed for; instructing a half-true item to be
marked wholly closed was me applying the convention mechanically instead of
reading what was under it. The half-close heading, the file's own preamble
(*"One is now closed, and one is closed by half"*), and the dashboard README row
(*"one of them closed and one half closed"*) are all consistent with each other
and with the tree. **F-7 is closed on DEV-3's disposal, over mine.**

I note for the record that DEV-3 raised this against an instruction from the
merge-blocking gate, with the evidence attached, rather than complying quietly.
That is the behaviour this project should want from the person who is right.

#### 9.1.7 Verdict

**PASS.** All six findings of §4.4 are closed, one of them on a better remedy
than the one I specified; a seventh instance I missed is closed too; the polish
grade is unchanged; and the number that went stale is now held by a check whose
red and green paths I drove myself, including the vacuous-pass path a lesser
guard would have left open. §4.5's *"six edits, none of them in code"* turned out
to be five edits and two code changes, which is more than was asked for.

---

### 9.2 Box 8 — FR-59, the docs set. **PASS**, with one condition

This box was deliberately left ungraded above (§1). It is graded here for the
first time.

#### 9.2.1 The standard, stated before it is applied

FR-59 names nine subjects. A docs set discharges it when **(a)** each of the nine
has a page a reader can find and build from; **(b)** the disputed subject meets
the ruling `docs/gates/phase-4.md` §5.4 made about it; and **(c)** the pages'
claims are true of the code that ships — because a documentation requirement
whose pages are false is not a documentation requirement that has been met. (c)
is the same standard §4.1 applied to the examples, applied to the library's own
guide.

#### 9.2.2 (a) Nine of nine, each indexed

| FR-59 subject | Page | Indexed at |
|---|---|---|
| quickstart | `docs/quickstart.md` | `docs/README.md:24` |
| architecture | `docs/guide/architecture.md` | `:32` |
| protocol reference | `docs/protocol.md` | `:58` |
| observability setup | `docs/guide/observability.md` | `:37` |
| security configuration | `docs/guide/security.md` | `:43` |
| HTMX interop | `docs/guide/htmx-interop.md` | `:38` |
| forms and validation (FR-55's pattern) | `docs/guide/events-and-forms.md` | `:33` |
| deployment | `docs/guide/deploying.md` | `:44` |
| "when not to use this" | `docs/guide/when-not-to-use-this.md` | `:45` |

All fourteen files in `docs/guide/` have an index row; there is no page a reader
cannot reach. Every relative link in `docs/guide/**` and `docs/quickstart.md`
resolves to a file that exists, and the two anchors the architecture page targets
in other pages (`deploying.md#resource-sizing`,
`lifecycle-hooks.md#what-runs-where`) are real headings.

#### 9.2.3 (b) The architecture page against PM-1's ruling

§5.4 ruled that `rfc/001-architecture.md` does not discharge the subject, and
named two alternatives. DEV-3 took the first. Against the ruling's own terms:

- **It is reader-facing and it instructs.** It is filed under **Guide**, not
  under *"For the curious"*, and it tells the reader what to do
  (*"keep the hook a decision over data you already have. If it needs a database,
  cache the answer at `Init` and re-check it in `Execute`"*).
- **It carries no disclaimer** — nothing in it says it is not needed to build an
  application, which is the sentence the ruling read the RFC's filing off.
- **The RFC's own index row now points at it** as *"the page to build from"*, so
  the set no longer has two architecture documents with no stated relation.
- **It answers the ruling's honest test rather than dodging it.** §5.4 said: *"if
  the RFC cannot be moved into the guide without keeping the disclaimer, then it
  was not instruction."* The page's closing section gives the reason the re-filing
  was not taken — the RFC is 1,805 lines (checked: `wc -l` = 1805) addressed to a
  reviewer holding a checklist, dated `2026-08-04` with status *"Draft for L9-1
  approval (Phase 0 gate)"*, i.e. before the library existed (checked: both in the
  RFC's own header table). That is an argument that the RFC **was** design record
  rather than instruction, which is the answer the test was designed to extract,
  not an evasion of it.
- **It is held by machinery, not by care.** Its Go blocks carry
  `<!-- sample: architecture/architecture.go -->` markers and are governed by
  `docs/guide/_samples/samples_test.go`. I mutated one line of one block in the
  page (`s.Heard++` → `s.Heard += 2`) on a copy: **red**, 151 passed / 1 failed,
  *"../architecture.md:109 still matches architecture/architecture.go"*. Restored,
  green.

**The ruling is met.**

#### 9.2.4 (c) The load-bearing claim, driven — with a control

DEV-3 reports that writing the page found a real defect: `lifecycle-hooks.md`
said `Authorize` runs on the session's actor goroutine; it runs on the
connection's read pump, and a slow one self-closes with 4010
`HEARTBEAT_TIMEOUT`. I verified all three parts.

**The defect was real.** `git show 22a47a6b~1:…/lifecycle-hooks.md:166` reads
*"| `Authorize`, `Reduce` | the session's actor goroutine |"*. At HEAD the row is
split and reads *"the connection's read pump, before the event reaches the
mailbox"*. So a page in the docs set did assert the false thing, and it was found
by writing the page that is now being graded.

**The correction is true, statically:** `internal/wsx/conn.go:251` calls
`c.actor.Ingress(parseCtx, in)` synchronously inside `readPump`, and
`internal/session/ingress.go:173` calls `a.app.Authorize` from that call's
descendant. Nothing crosses a goroutine boundary in between.

**The correction is true, driven.** The sample's spec observes two different
goroutine identifiers for `Init` and `Authorize`; that spec is DEV-3's, so I
wrote my own and asked the harder question — the **4010** consequence, which no
spec in the tree asserts. A probe and a control, same limits, same client
traffic, on a copy:

```
PROBE   Authorize sleeps 9s on the first event; HeartbeatInterval 1s,
        HeartbeatTimeout 2s; three further events written in the first 1.2s
  -> livetest: … (session 6c26ca9d462726c78f16b109155b9e99): failed to get
     reader: received close frame: status = StatusCode(4010) and reason =
     "no frame from the client within the heartbeat timeout"

CONTROL identical limits, identical traffic, Authorize returns immediately
  -> no error for 8 s; the session stays open
```

The control is what makes the probe mean anything: `livetest.Client` does not
echo heartbeats, so a *quiet* session on those limits would close 4010 whatever
`Authorize` did. With traffic flowing the tight configuration alone does not
close it, and only the stall does. **The page's sharpest and least checkable
sentence is true, and it is true for the stated reason** — the frames written
during the stall are never read, so `Ingress` never refreshes `lastInboundNS`
(`internal/session/ingress.go:45`) and `onTick` closes on the deadline
(`internal/session/actor.go:1093`).

#### 9.2.5 (c) The rest of the page, and the rest of the set

| Check | Result |
|---|---|
| Every default in the page's two tables against `internal/session/limits.go`'s `DefaultLimits()` | **every one exact** — fifteen of that struct's sixteen fields (it names `WriteDeadline` as a concept and quotes no value for it): 65536, 50/100, 64, 32, 16, 512, 1 s/3, 20 s/50 s, 30 min, 3, 5 s, 30 s grace |
| Every close code the page names against `internal/protocol/closecode.go` | **all exact**: 4002, 4006, 4007, 4008, 4009, 4010, 4011, 4012 |
| Every close code named anywhere in the guide + quickstart + `protocol.md` | 4000–4013, **all fourteen defined** |
| *"exactly three kinds skip `Authorize`"* | **true, and it is the non-obvious reading**: a resync request does **not** skip it — `ingressResync` calls `a.authorize` (`ingress.go:214`). Ack, telemetry and heartbeat are the three |
| *"exactly 2 goroutines per session"* | `docs/bench/g2-baseline.md:323`, `:365`, `:493` — 2,007 against 7 at N=1000 and 207 against 7 at N=100, every run |
| Every `live.X` / `livetest.X` symbol referenced anywhere in `docs/guide/**`, `docs/quickstart.md`, `docs/protocol.md` (43 distinct) | **42 resolve in the shipping source.** The one that does not, `livetest.Report`, is the one the set itself labels *"not yet implemented. Ledgered; no consumer has written their shape yet"* (`testing-your-app.md:26`) — and `tools/apisurface` is green at `live` 54/54 + 51/51, `livetest` 37/37 + 33/33 |
| `docs/guide/_samples` suite at HEAD | **green**, every sample package builds and every marked block still matches its file |
| `deploying.md` §Resource sizing, which `architecture.md` defers the memory figure to | present, with its method, its commit, **and** the two caveats that make it honest — that two of five runs were over the gate and that the driver-validation gate has never been run |
| `events-and-forms.md`'s absent-vs-empty claim, which is FR-55's owed pattern | `Fields.Get` and `Fields.Lookup` exist at `live/core.go:156`/`:162` with exactly the documented semantics |

#### 9.2.6 The one defect

**`docs/README.md:24` quotes a measurement the page it indexes has replaced.**
The index row says the quickstart is *"27 lines of Go, 19 of templ"*.
`docs/quickstart.md:7` says **20 and 19**, and says the total is *"39 against the
≤30"*. I counted the shipping sample myself under the quickstart's own stated
rule — every line that is not blank, not a comment, and not `package`/`import`:

```
docs/guide/_samples/quickstart/main.go     20
docs/guide/_samples/quickstart/view.templ  19
```

The quickstart is right and its index row is wrong. It went stale at `fde707f0`,
DEV-1's FR-53 shrink, earlier in this same turn, and nothing holds it: the
samples suite governs `docs/README.md`'s **code blocks** and that file has none,
so its prose numbers are unheld.

**Why this is a condition and not a second FAIL, stated so the two boxes are
graded on one standard.** §4.5 failed box 6 for a documentation set that had
**stopped tracking its own code across a whole migration** — six places, one of
them telling a reader that the library's supported testing API did not exist
while the file beside it used it twenty-five times. This is one number, in an
index row, lagging one commit from the same turn, on a page that states the
correct figure twice and volunteers that it misses its own budget by nine. It
misleads nobody into writing wrong code. Every subject page's claims are true.
The distinction I am drawing is the one §4.4's D-6 already drew: a set that has
stopped tracking its code, versus a row that lags a commit.

> **Condition on box 8:** `docs/README.md:24` is corrected to the quickstart's
> own figure — 20 lines of Go, 19 of templ, 39 against FR-53's 30 — or the row
> stops restating a number the page it links to holds. **Owner: DEV-3.** F-10
> below. It is a one-line edit and it does not need a re-grade.

#### 9.2.7 Verdict

**PASS, with one condition.** Nine subjects of nine, each with a page and an
index row. The disputed subject has a page that meets §5.4's ruling on every
term of it, including the honest test, and that page's sharpest claim is true
when driven with a control rather than merely asserted. The set's factual
surface — every default, every close code, every exported symbol it names —
checks out mechanically against the shipping source, with the single unimplemented
symbol labelled as such on the page that names it. Writing the disputed page
found and fixed a false claim in a neighbouring page, which is the strongest
available evidence that the page was not decorative.

---

### 9.3 Box 12's condition — **discharged**

#### 9.3.1 The value a caller holds, driven again

§2.6's defect was that the exported `(*Client).NextErr` returned the five §3.4
messages bare. I re-ran the same driver at HEAD:

```
QA1-SESSION: 2e0940ee8771a38e5d1840bfdfeed637
QA1-NEXTERR: "livetest: QA1 driver … (session 2e0940ee8771a38e5d1840bfdfeed637):
  no frame arrived within 300ms: the session is open and quiet, so either the
  transition you expected produced no patch — an identical render is suppressed
  — or the outbound window is full and nothing was acknowledged"
```

The hex is the value `SessionID()` returns on the same client, the client's name
is there, and the next-step clause survived the prefix rather than being replaced
by it. §2.6's *"names the session? false"* is now true. The transport arm carries
it too — §9.2.4's probe, which is a completely unrelated spec, printed a
`StatusCode(4010)` close **already wrapped with its session**, which is the arm
DEV-1 called the one most in need of it.

#### 9.3.2 The specs go red when the fix is removed

DEV-1 reports three Ginkgo specs holding it, mutation-driven. I did the mutation:
`return nil, fmt.Errorf("livetest: %s: %w", c.where(), err)` → `return nil, err`,
on a copy. **All three go red** — the timeout arm loses the session, the
transport arm loses it, and the third fails on *"Next must not re-apply a prefix
`NextErr` already carries"* because the count of `"livetest: "` went to zero. My
driver printed the bare string in the same run, which is §2.6's output verbatim.
Restored: `./internal/arch/`, `./live/` and `./live/livetest/` all green.

#### 9.3.3 The five rows

The condition offered a disjunction; DEV-1 did **both** halves. The five rows are
now `client.go:220`, `:323`, `:340`, `frame.go:258`, `:263` — the same five sites,
line numbers moved by the wrap, each verified by me to be the site the row quotes
— and each says *"↑ via the wrap above, on every path: the `tb.Fatalf` failures
and the value `NextErr` returns. **Clause 1 failed on the returned value until
revision 3**"*. A sixth row, `client.go:302`, grades the wrap itself and is marked
*"new at revision 3"* rather than carrying a "was", so it does not disturb the 25.

#### 9.3.4 Does my §2 reproduction still mean what it said?

I graded revision 1. The document is now at revision 3. So I re-ran my own walk —
the AST re-implementation of §2.1's prose, written independently of
`internal/arch/errors_test.go` — at **both** trees:

```
                                    091dbae8 (§2.2)   368132f6 (HEAD)
  internal/cmd/gotth-live-dev              3                 3
  internal/obs                             4                 4
  internal/protocol                       40                40
  internal/render                          8                 8
  internal/session                         8                 8
  internal/wsx                            10                10
  live                                    37                38
  live/livetest                            7                 8
  TOTAL (audit's 8 in-scope packages)    117               119
```

**§2.2's numbers reproduce at `091dbae8` to the package**, three revisions later,
so the evidence my PASS rested on is intact and still says what it said. At HEAD
the walk returns **119**, which is revision 3's headline, and both moved packages
are accounted for by the document: `live` 37 → 38 (§3.1's `app.go:107` row struck,
`live/page.go`'s two added at §3.3.1) and `livetest` 7 → 8 (the wrap). The
committed census agrees — `errorCensus` carries `live: 38` and `live/livetest: 8`
with the reason written beside each — and `go test ./internal/arch/` is green.

The 25 is unmoved: counting §3's graded-failure rows by the §4 subsection each
cites gives **{4.1: 6, 4.2: 5, 4.3: 1, 4.4: 4, 4.5: 6, 4.6: 3} = 25**, exactly
§2.2's figures. `grep -c '**was '` returns 25 for the same coincidental reason
§2.2 recorded — 24 rows plus the §1 headline, with `app.go:539`'s
*"the fragment was unnamed"* as the twenty-fifth row.

**Condition discharged.** Box 12's **PASS** stands, unconditional. The census has
now fired twice in production on real edits, which is worth more than any
mutation I could write for it.

---

### 9.4 Defects, routed from this pass

| # | Defect | Where | Owner |
|---|---|---|---|
| **F-10** | `docs/README.md:24` indexes the quickstart as *"27 lines of Go, 19 of templ"*. The page says **20 and 19**, total 39, and the shipping sample counts 20/19 under the page's own stated rule. Stale since `fde707f0`; the samples suite governs code blocks and this file has none, so nothing holds it | `docs/README.md:24` | **DEV-3.** Condition on box 8 (§9.2.6) |
| **F-11** | `docs/bench/data/g2-baseline/README.md:3` links to `../g2-baseline.md`, which resolves to `docs/bench/data/g2-baseline.md` and does not exist; the file is at `docs/bench/g2-baseline.md`, one level further up. Outside FR-59's nine subjects, so it is **not** part of box 8's condition | `docs/bench/data/g2-baseline/README.md` | **DEV-2** (bench artifacts). Low |

**F-1 through F-9 are closed** (§9.1, §9.3). F-3 and F-4 were routed to DEV-2 and
PM-1 in §5 and are theirs; I did not re-check them in this pass and they are not
conditions on any box I have graded.

---

### 9.5 What I could not check in this pass, and why

* **A full `ci.sh`.** The orchestrator was running one in a container while this
  pass ran; running a second would have contended for the same tree without
  adding evidence. I ran the narrower steps each verdict needs: the three example
  suites with `-race`, `./internal/arch/`, `./live/`, `./live/livetest/`, the
  `docs/guide/_samples` suite, and `tools/apisurface`. Every one green at HEAD.
* **The examples in a browser.** Unchanged from §7: the three READMEs' "What to
  expect" walkthroughs remain the one part of box 6 I cannot falsify from a
  terminal, and box 6's PASS is over code, READMEs, commands and numbers.
* **The docs set read end to end for prose quality.** I graded FR-59 on coverage,
  on §5.4's ruling, and on whether the claims are true of the code — the three
  things the requirement and the gate actually ask. I did **not** grade whether
  the fourteen guide pages are well written, and a reader-alone build from them
  is `docs/qa/phase-4-docs-alone.md`'s question, not this one.
* **The remaining 8 of the audit's 25 graded failures.** Unchanged from §7; I
  spot-checked 17 in §2.3 and did not widen the sample.
* **Boxes 7 and 13.** Box 7 is graded above and nothing in this landing touched
  the committed file layout its property is about. Box 13 (FR-20) is L9-1's gate,
  signed at `bdf91971`, and is not mine to grade.

---

### 9.6 Status — what QA-1 has now graded in Phase 4

| Box | Criterion | Verdict | Where |
|---|---|---|---|
| **6** | The three examples *polished and documented* | **FAIL** at `091dbae8` → **PASS** at `368132f6` | §4.5, re-graded §9.1.7 |
| **7** | G11 — consumable from a clean clone | **PASS**, no conditions | §3 |
| **8** | FR-59 — the docs set, nine subjects | **PASS**, with one condition (F-10) | §9.2.7 |
| **12** | FR-58 — the error audit | **PASS**, condition **discharged** | §2, §9.3.4 |

**Four of Phase 4's thirteen boxes now carry a QA-1 grade, and all four are
passes.** One condition is open, it is a one-line edit to an index row, and it
does not need a re-grade to close. Boxes QA-1 gates and has **not** graded in
either pass: FR-53's conjunction, FR-54, FR-57's dev reload, and FR-66/FR-68's
godoc boxes — none of which was routed to me. Box 13 is L9-1's.

— **QA-1**, correctness gate, 2026-08-05, at `368132f6`.

---

*Reproduce §9.*

```bash
cd gotth-live
D='docker run --rm -v '"$PWD"'/..:/w -w /w/gotth-live -e GOFLAGS=-buildvcs=false dis-gotth-live:latest bash -c'

# ---- §9.1.2 the three counts, from Ginkgo's own summary ----
$D 'for e in counter chat dashboard; do cd /w/gotth-live/examples/$e;
      printf "%-10s " $e; go test -race -count=1 -v ./... 2>/dev/null |
      grep -oE "Ran [0-9]+ of [0-9]+ Specs" | tail -1; done'

# ---- §9.1.3 the five controls, on a COPY (never on examples/) ----
cp -a ../gotth-live /tmp/qa1-neg/            # then, in /tmp/qa1-neg/gotth-live:
#  A  sed the SECOND "53 specs" in counter/README.md to 54   -> red, exit 1
#  B  rewrite every "N specs" to words                       -> red: "checking nothing"
#  C  mv counter/README.md away                              -> red: "cannot read README.md"
#  D  add a one-line Ginkgo spec, leave the README           -> red: "says 53 … has 54"
#  E  go test -ginkgo.focus="matches nothing", README intact -> GREEN, Ran 0 of 53

# ---- §9.1.6 the F-2 adjudication, with no toolchain ----
grep -rn "Subprotocol" live/ internal/protocol/limits.go | grep -v _test
grep -n "func NewClient" live/livetest/client.go          # takes testing.TB first
grep -n "MeasureResync" examples/dashboard/main.go examples/dashboard/resync.go

# ---- §9.2.4 the 4010 claim: probe + control, on the COPY ----
#  a spec in docs/guide/_samples/architecture/ with HeartbeatInterval 1s,
#  HeartbeatTimeout 2s, Authorize sleeping 9s -> NextErr carries StatusCode(4010);
#  the same limits and traffic without the stall -> no error for 8s.
grep -n "actor.Ingress" internal/wsx/conn.go               # :251, on the read pump
grep -n "app.Authorize" internal/session/ingress.go        # :173, under it
sed -n '1093,1094p' internal/session/actor.go              # the 4010 close

# ---- §9.2.3 the drift check bites the new page ----
#  on the COPY: change s.Heard++ to s.Heard += 2 in docs/guide/architecture.md
$D 'cd docs/guide/_samples && go test . -count=1'   # -> "../architecture.md:109 still matches"

# ---- §9.2.5 the mechanical claim sweep ----
sed -n '/^func DefaultLimits/,/^}/p' internal/session/limits.go   # vs the page's tables
sed -n '15,32p' internal/protocol/closecode.go                    # vs every 40xx in the docs
$D 'cd tools && go run ./apisurface'                              # live 54/54 51/51, livetest 37/37 33/33

# ---- §9.2.6 the one defect ----
sed -n '24p' docs/README.md      # "27 lines of Go"
sed -n '7p'  docs/quickstart.md  # "20 lines of Go and 19 lines of templ markup"

# ---- §9.3 the condition ----
$D 'go test ./live/livetest/ -count=1 -v -ginkgo.focus="FR-58 on the error NextErr returns"'
#  mutate live/livetest/client.go:302 to "return nil, err" on a COPY -> all three red
$D 'go test ./internal/arch/ ./live/ ./live/livetest/ -count=1'    # green at HEAD

# ---- §9.3.4 the walk, at both trees ----
#  an AST program implementing §2.1's prose (errors.New | fmt.Errorf | *Error
#  composite literal | protocol.reject), skipping _test.go, over live/ and internal/:
#    091dbae8 -> 117 in scope (live 37, livetest 7)   == §2.2, unchanged
#    368132f6 -> 119 in scope (live 38, livetest 8)   == revision 3's headline
```

---

## 10. Box 2 — FR-53 and G7, the timed counter. Graded 2026-08-05 at `8be955e5`

**Tree graded:** `8be955e5d67df0397f01d82f7830d5e0c4d5e610`, HEAD of
`dev-/gotth-live-orchestrator-c3efc4`. The tree did not move under this pass.
**Requested by:** the orchestrator, after DEV-1's page shell (`8680e8c5`) and
L9-1's gate on it ([`docs/reviews/page-shell.md`](../reviews/page-shell.md),
`af4585b4`). **PM-1 evaluates FR-53's re-open triggers after this grade; §10.14
says what I found for them and it is not what the brief anticipated.**

**Box 2 reads:** *"First working counter in ≤15 minutes and ≤31 lines of app
code, timed (FR-53, G7)."* It has been open since v0.6 and has been missed by
16, then 9, then 8.

### 10.1 Verdicts

| Clause | Verdict | On what |
|---|---|---|
| **≤15 minutes** | **PASS** | 2 m 29 s, [`docs/qa/fr-53-timed-r2.md`](fr-53-timed-r2.md) §1, credited on §10.8's checks. One stop, self-repairing from the error text — §10.6 |
| **≤31 lines of application code** | **PASS**, at exactly **31**, margin **zero** | My own count, §10.3, on both counting paths, sequence-identical, under the rule as it has been applied at every measurement since v0.6 — §10.5 |
| **G7** — *DX is real: QA-1 builds a working small app from the docs alone, no source reading, ≤15 min to first working counter* | **DISCHARGED** by the same evidence, and it also closes a qualification box 1 has carried since v0.8 — §10.10 |
| **BOX 2 AS A WHOLE** | **PASS WITH CONDITIONS** — four, numbered **Q-1** to **Q-4** at §10.13. None reopens this grade; **Q-4 exists because nothing in the tree can fail if the count goes to 32** | |

**Box 2 goes green. It is the first time it has, and it goes green with no
margin at all on the clause that kept it red.** Every condition below is about
keeping it green, or about the page rather than the measurement.

### 10.2 The standard, stated before it is applied

**What FR-53 says, in full:** *"A developer following the quickstart from zero
MUST reach a working, live counter in ≤15 minutes and ≤31 lines of application
code. Measured by QA-1 with a timer, from docs alone, without reading library
source."*

**The counting rule, fixed at v0.6 by PM-1 ruling, PRD §5.I. It is not mine to
move and I have not moved it:**

- **Scope:** *"every line of application code the developer authors, in every
  file, whatever its extension"* — for the quickstart, `main.go` **plus**
  `view.templ`.
- **Counted:** *"every line that is not blank, not a comment, and not a
  `package` or `import` line."*
- **Not counted:** *"generated files the developer does not write
  (`*_templ.go`), `go.mod`, and shell commands."*

**Three things that are consequently already settled and that I am not being
asked to decide.** (i) `go.mod` **does not count** — the r2 record asks for a
ruling on this and asks correctly, because it could not see the PRD, but the
rule already carves it out by name. The 15 lines it worried about are not in
scope and never were. (ii) Shell commands do not count, which is why every
remedy proposed at §10.13 for Q-1 is free. (iii) `view_templ.go` does not count.

**What is genuinely open and is mine:** whether entries inside a parenthesised
`import ( … )` block are "import lines". §10.5 rules it.

**Not mine, and untouched:** the budget of 31 (PM-1's, countersigned by L9-1),
the triggers, L9-1's page-shell conditions PS-1 to PS-3, and box 1's tick.

### 10.3 My count — method shown, per file, line by line

**Method, derived from the rule rather than inherited from anyone's `awk`.** I
classify every physical line of each counted artifact under exactly four
exclusions — blank, comment, `package`, `import` — and print the classification
so it can be checked by eye rather than trusted. I compute **both** readings of
the import clause in the same pass (§10.5), so that adopting one is a stated
choice and not a property of my script. `python3` on the host; no Go and no node
was needed to count.

**Path A — the quickstart's own fenced blocks at HEAD.** `docs/quickstart.md`
lines **75–117** (the `go` block, marked `<!-- sample: quickstart/main.go -->`)
and **331–362** (the `templ` block, marked `<!-- sample: quickstart/view.templ
-->`), taken as the fences delimit them.

| Artifact | Physical | Blank | Comment | `package` | `import` decl | **Counted** |
|---|---:|---:|---:|---:|---:|---:|
| `docs/quickstart.md` :75–117 (`main.go`) | 43 | 7 | 10 | 1 | 5 | **20** |
| `docs/quickstart.md` :331–362 (`view.templ`) | 32 | 4 | 12 | 1 | 4 | **11** |
| | | | | | | **31** |

The 20 counted Go lines are `const MountPath`, `const EventInc`, `type State`,
the 14 lines of the `live.MustNew(live.Config[State]{…})` literal, and the three
lines of `func main`. The 11 counted templ lines are `templ Count` and its 5,
and `templ Page` and its 4. **Twelve of the 20 are `Config` fields `validate`
requires**, which is the shape PM-1's derivation has claimed since v1.1 and
which my classification reproduces independently.

**Path B — the shipping sample files at HEAD**, `docs/guide/_samples/quickstart/main.go`
and `view.templ`. Same method, applied to whole files rather than to fence
ranges:

| Artifact | Physical | **Counted** |
|---|---:|---:|
| `docs/guide/_samples/quickstart/main.go` | 67 | **20** |
| `docs/guide/_samples/quickstart/view.templ` | 32 | **11** |
| | | **31** |

**My count is 31.** It agrees with the page's claim, with r2's, with L9-1's
re-derivation and with PM-1's costing — and §10.11 is where I say why agreement
with four prior figures is the weakest part of that and what I did about it.

**The method can produce numbers other than 31**, which is the first thing to
establish about any counter that returns its target. Run over the same two
blocks across the six commits the record names, it returns 46, 46, 39, 39, 31 —
and 55, 55, 46, 46, 38 under the other reading. It is not a script that says 31.

### 10.4 The two counting paths cross-checked — and the pin that is credited with holding them together does not hold a count

**The two paths agree line for line, not merely in total.** I compared the
*sequences* of counted lines, not the counts: `main.go` 20 = 20 with an identical
ordered sequence, `view.templ` 11 = 11 likewise. The sample file carries 24 more
physical lines than the fenced block — a command doc comment, a `State` doc
comment, three extra paragraphs on `app` — and every one of them is a comment.
**Two artifacts, sharing no line range and no fence, carrying the same 31
counted lines.**

**A third statement of the same figure, checked because F-10 taught this file to
check it:** `docs/README.md:24` reads *"20 lines of Go, 11 of templ"*. It
tracked `8680e8c5`. F-10 stays closed and no document in the tree outside the
gate record (PM-1's, revision 3, already owed a revision 4) states a stale count.

**Now the part that is not confirmation.** L9-1's §7 credits
`docs/guide/_samples/samples_test.go` with being *"the reason the two counting
paths in §6 are measurements of the same shipping bytes rather than of a stale
copy."* **That overstates what the suite enforces, in the direction that
matters.** The pin builds a **set** of `TrimSpace`d lines from the sample file
and asserts each non-blank line of the doc block is **in** it. That is
`doc ⊆ sample`, as a set, indentation-insensitive. It is not an equality and it
is not a count.

I mutation-tested it on a throwaway copy of the module in the container's `/tmp`
(nothing written into the tree; baseline green first):

| Mutation | Would it move the count? | Pin |
|---|---|---|
| **M1** — doc block gains a counted line the sample lacks | doc +1 | **RED — caught** |
| **M2** — sample gains a counted line the doc block lacks | sample +1 | **GREEN — not caught** |
| **M3** — doc block repeats an existing counted line 4× | **doc +4** | **GREEN — not caught** |
| **M4** — doc block re-indented, tabs → spaces | no | GREEN (correctly indifferent) |

**M3 is the one that matters**, because the doc block is what a reader copies and
therefore what FR-53 measures: the counted block can gain lines and stay green so
long as each is already present once in the sample — a second `Fragments:` entry,
a repeated closing brace from a nested call, a duplicated `}`. **The agreement at
31 is a fact I measured about today's tree, not a property the suite maintains.**
That is Q-4.

### 10.5 The parenthesised-import ambiguity — ruled on the record, not on preference

**The two readings, and they are 7 lines apart on a clause with zero margin:**

| Reading | `main.go` | `view.templ` | Total | vs ≤31 |
|---|---:|---:|---:|---|
| **A** — the whole `import` declaration is import lines, block and closing paren included | 20 | 11 | **31** | **PASS** |
| **B** — only lines beginning `import` are import lines | 24 | 14 | **38** | FAIL by 7 |

**I graded under Reading A.** Three reasons, in the order of their weight, and
the first is the one that makes this a finding of fact rather than a preference.

**(a) Reading A reproduces the entire published history of this measurement.
Reading B has never produced a figure this project has recorded.** The v0.6
ruling did not invent a method; it says it adopts *"the quickstart's own stated
method … unchanged"* and rules *"only its scope"*. So the method is fixed by how
it has been applied, and it has been applied six times at six commits by three
different agents. I ran both readings over the two blocks at each:

| Commit | Time | Reading A | Reading B | The record says |
|---|---|---:|---:|---|
| `8a06cb04` | 11:05 | 27 + 19 = **46** | 33 + 22 = 55 | **46** (v0.8) |
| `134e69c5` | 12:33 | 27 + 19 = **46** | 33 + 22 = 55 | **46** (v0.9) |
| `fde707f0` | 13:27 | 20 + 19 = **39** | 24 + 22 = 46 | **39** (v1.0) |
| `93772adc` | 15:12 | 20 + 19 = **39** | 24 + 22 = 46 | **39** (v1.1) |
| `679e6695` | 17:08 | 20 + 11 = **31** | 24 + 14 = 38 | **31** (r2, L9-1) |
| `8be955e5` | HEAD | 20 + 11 = **31** | 24 + 14 = 38 | this grade |

**Reading A is 6 for 6. Reading B produces 55 and 46 and 38, and 55 and 38
appear nowhere in this project's record.** A grader adopting B today would not be
applying the v0.6 rule to a new tree; they would be replacing it, retroactively
falsifying FR-53's own miss table — the table the amendment log built expressly
so that moving a threshold could not bury what it had been missed by — and doing
so on the one tree where the substitution flips a verdict. **That is the version
of "the grader settles the rule at the gate" that the v0.6 ruling exists to
prevent, and it points the other way from the one a suspicious reader expects.**

**(b) Reading B is internally incoherent on the rule's own rationale.** Under B
the count depends on whether imports are written as a parenthesised block or as
separate `import "x"` lines — a purely cosmetic choice, made by tooling, that
changes no program. `main.go`'s three imports cost 4 under B as a block and 0
written out singly. The v0.6 scope ruling refuses exactly this shape of thing:
*"Exempting it would let the count be met by moving code across a file
boundary."* A reading under which `gofmt`'s grouping is worth 7 lines is not a
stricter reading of the rule; it is a different and worse rule.

**(c) It is the reading the page's own arithmetic states.** `docs/quickstart.md:7`
publishes 20 and 11 per file, and only Reading A yields that split.

**This does not make the rule's text adequate, and the repair is PM-1's.** A rule
whose two plain readings differ by 7 on a clause with zero margin should say
which it means, in the requirement, before the next grader has to re-derive what
I re-derived. **I am routing the wording and not writing it** — Q-3, §10.14.
**My verdict does not depend on the repair**; the repair stops a future grader
reaching 38 on a document that has meant 31 all along.

### 10.6 The documented build path does not work — and it is a docs defect, not a box-2 failure

**Reproduced, independently, at HEAD, and it is exactly as r2 describes it.** I
ran §1 verbatim (`go mod init`, the hand-written `require` block, `go mod edit
-replace`), copied §2 and §3 from the fences, then §4 verbatim:

```
templ generate     -> exit 0
go run .           -> 9 errors, exit 1
   view_templ.go:8:8: missing go.sum entry for module providing package
       github.com/a-h/templ (imported by example.com/counter); to add:
           go get example.com/counter
   … 7 more, at internal/obs/metrics.go, internal/obs/trace.go,
     internal/protocol/refinepb/refine.pb.go, internal/protocol/inbound.go,
     internal/wsx/conn.go
```

`go mod tidy`, `go get` and `go.sum` appear **nowhere** on the page — `grep` over
`docs/quickstart.md` returns no match for any of the three. **The stop is
unconditional: it is what §1 followed by §4 produces, every time, for every
reader.**

**And it is not §1's `require` block that causes it.** I ran the counterfactual —
skip §1's block entirely, keep only `go mod init` and the `replace` — and the
build still fails, with `no required module provides package …`. **The page's
defect is not a wrong instruction; it is a missing one.** Nothing on the page
ever writes `go.sum`.

**One correction to the evidence, and it changes the severity rather than the
finding.** r2 says the Go tool's own printed remedy *"is the wrong shape of
advice (it names the main module) … A reader who trusts the error text over
their instincts is sent sideways."* **I ran it, and it is not wrong: it works.**
`go get example.com/counter`, copy-pasted from the first error, exits 0, writes a
14-line `go.sum`, and `go build ./...` then exits 0. The resulting `go.mod` is
byte-identical to what `go mod tidy` produces. (r2 is right about the *second*
remedy: `go mod download github.com/a-h/templ` alone only moves the error along
to `go.opentelemetry.io/otel`.) **So the worst-case reader — the one new to
modules who trusts the tool over instinct — is fixed by one copy-paste from the
error they are already looking at.** The "several minutes" r2 prices this at is
an over-estimate, and that matters to §10.9.

**The ruling, and the argument, because a later reader will want it.**

**A documented path that always errors is a defect against the document and does
not fail this box.** FR-53's clause is *"reach a working, live counter in ≤15
minutes"*. Every term is met: the reader reaches a working live counter, from
docs alone, and the stop is **inside** the measured time with 12.5 minutes to
spare. The requirement bounds a **cost** and attaches its own instrument — a
timer — to measure it. A clause that meant "the printed commands succeed on the
first attempt" would need no timer.

**The contrary reading, stated fairly:** *"following the quickstart from zero"*
can be read as a warranty that the page's instructions execute as printed, and
under that reading §4 does not, and box 2 fails. **I reject it, and not because
it is inconvenient.** (i) It makes box 2 a docs-quality box, which is **box 1** —
the docs-alone gate, already held and passed, and passed *with eight findings
against the page*. This project has already placed "the page has defects" and
"the timed traversal succeeded" in different boxes, and collapsing them would
retroactively unfail nothing and refail everything. (ii) Under the warranty
reading every one of round 1's eight findings and r2's six would fail this box,
which is not a standard anybody could hold a document to. (iii) The severity, now
that I have run it, is a fifteen-second copy-paste whose remedy the failure
prints.

**What would change my answer**, said now so the ruling is falsifiable: if the
stop were **not** self-diagnosing — if it produced a silent wrong result, or an
error whose printed remedy did not work, or a failure that recurs after the
remedy — I would fail this box, because then the documented path would not lead
to a working counter at all and the time clause would be measuring a path the
reader is not on. It is none of those. **It is one missing line in a `bash`
block, which the counting rule does not count, so the fix is free in every sense
that matters here.** It is **Q-1**, and it is blocking on the page.

### 10.7 `Config.Logger` is unset — confirmed with a control, and the obvious fix fails this box

**Confirmed at the source, then driven.** `Config.Logger` is `nil` in §2's
application; `live/app.go:92` passes it to `obs.NewLogger`, which returns a nil
`*obs.Logger` (`internal/obs/log.go:81`–`:84`); every emit method tests the nil
receiver and returns (`:139`). **The process writes nothing.**

The sharper form of the finding, which is worth having: the library *does* carry
the right message on exactly this path.
`internal/wsx/handler.go:176` reads

```go
h.opts.Logger.Warn(ctx, "gotth-live: refused an upgrade from a disallowed origin: add it to Config.Origins if it is yours",
    obs.Str("origin", r.Header.Get("Origin")))
```

— an FR-58-shaped message naming the field to fix. **The quickstart's `Config` is
precisely the configuration under which it is unreachable.**

**Driven, with the failure the page's troubleshooting row is written about.** I
built the counted application unmodified and, in a second container, built it
again with the origin allowlist changed so every upgrade is refused — the
`localhost`-instead-of-`127.0.0.1` reader error, in its permanent form. Across
a successful `101`, a `403 forbidden origin`, a `426`, and then a whole browser
session **reconnecting on a rejected origin for the length of a driven run**:

```
PROCESS OUTPUT:  stdout = 0 bytes    stderr = 0 bytes
```

**Zero bytes, in the exact scenario §4's row describes — *"a page reconnecting
every few seconds with 403s in the log"*.** The diagnosis in the row is correct;
the place it sends the reader does not exist. It lands on the page's most likely
reader error, which is the one thing that makes it worth a condition rather than
a note.

**And the interaction that no one has flagged, which is the reason this is
routed with a constraint attached rather than as a bare docs fix.** r2's third
suggested remedy is *"or add `Logger` to the §2 `Config` while debugging."*
**That remedy fails box 2.** A `Logger:` field in the `Config` literal is one
**counted** line — the `"log/slog"` import is free under Reading A, the field is
not — taking `main.go` to 21 and the app to **32**. Under FR-53's trigger 1 as
repaired at v1.2, a counted total above 31 does not move the budget up: **it
withdraws the amendment and reopens this box.** The one-line convenience fix for
Q-2 costs the project the box it just closed.

**So Q-2 must be discharged the other two ways** — point the row at the network
tab, which §4 already does two paragraphs earlier, and name the
`localhost`/`127.0.0.1` trap at the row. Both are prose. Neither costs a line.

**One further r2 finding I re-derived while I had the server up**, since it is
cheap and it is about the same table: an allowlisted `Origin` with **no**
`Sec-WebSocket-Protocol` header at all returns **`426`**. So the row's gloss —
*"subprotocol mismatch — the page and the binary are different versions"* —
states one cause where it means a condition. r2 is right, it is minor, no browser
reader hits it, and it rides along on Q-2 rather than earning its own condition.

### 10.8 Is the timed run a real docs-alone run? Every checkable claim, checked

r2 is evidence, not a grade. I checked what can be checked and I say what cannot.

| r2's claim | How I checked it | Result |
|---|---|---|
| Held at `679e6695` | `git log` timestamps: `679e6695` committed 17:08:39, next commit `dab16364` at 17:33:36; the run window is 17:26:44–17:29:13 | **Consistent.** The window sits wholly inside that commit's tenure as HEAD, and nothing landed during it |
| §7's two files are *"byte-identical to the quickstart's §2 and §3 blocks"* | Extracted r2's §7 fenced blocks and the quickstart's at `679e6695`; SHA-256 compare | **True, both files.** *"I changed nothing and added nothing"* holds mechanically |
| The 9 `missing go.sum entry` errors | Ran §1 → §4 myself at HEAD | **Reproduced.** Same nine, same files, same `line:column` — `internal/obs/metrics.go:9:2`, `internal/wsx/conn.go:12:2` and the rest — differing only in mount point (`/src` vs my `/workspace`) |
| §7's `go.mod`, incl. the six `// indirect` pins | `go mod tidy` in a fresh module against the worktree | **Identical**, line for line, but for the `replace` target |
| The served page bytes, quoted verbatim | `curl -s` against my own build of the same blocks | **Byte-identical**, including `lang="en"`, the charset, the title and the `defer`ed script tag |
| `Content-Length: 10391`, `Content-Type: text/javascript` | `curl -sI` | **Both reproduced exactly** |
| Elapsed 2 m 29 s | 17:26:44 → 17:29:13 | Arithmetic checks |
| *"I read exactly one file in this repository"* | — | **Not verifiable by anyone**, and I say so rather than credit it silently. What I can say: every checkable claim above checks out, and its quoted compiler paths are ones only a real build produces |

**One detail of its method I could not match**, recorded because a later reader
comparing the two records will notice it: r2 mounted the checkout at `/src`,
where the project's `~/bin/dis` mounts at `/workspace`. r2 therefore used a
hand-rolled `docker run` of the same images rather than `~/bin/dis`, which is
consistent with its own friction #1 and is not a defect.

**I credit the run.** Its verifiable surface is large, it is entirely correct,
and the one thing it got wrong (§10.6, the tool's remedy) it got wrong in the
direction that made its own findings sound *worse*, which is the direction a
manufactured record does not err in.

**The anchoring disclosure, and what it does to the evidence.** r2 discloses that
`docs/quickstart.md:7` states `20/11/31` in the page's first paragraph, so its
recount was not blind. **That is true, and it is true of mine too, and it cannot
be fixed:** the number is printed in the quickstart, in the PRD, in L9-1's review
and in r2 itself, and a grader has to read all four. **No count taken on this box
by anybody can be blind, and treating r2's 31 as independent confirmation of the
page's 31 would be double-counting a single number.** What replaces blindness is
(i) mechanical classification printed line by line so a reader checks rather than
trusts, (ii) two artifacts that share no fence and no line range, and (iii)
**the historical reproduction at §10.5(a)** — the one check anchoring cannot
fake, because a grader biased toward 31 would still have to produce 46 and 39 at
the older commits under the same method, and Reading A does and Reading B does
not.

### 10.9 The 15-minute clause, and how I am reading "a developer"

**2 m 29 s is an AI agent's wall clock and r2 says so itself**, calling it a
floor rather than a human estimate. FR-53's clause says *"a developer"*.

**My reading: the clause is satisfied, and the reading is forced by the
requirement's own second sentence.** FR-53 says *"Measured by QA-1 with a timer,
from docs alone"*. On this project QA-1 is an agent by construction. A
requirement that both names an agent as its instrument and means a human's wall
clock would be unmeasurable by the only party it authorises to measure it. So
what the clock is a proxy for is what r2 correctly identifies: **how many times
the documented path stops you.** It stops once.

**Three things make me comfortable that the human reading also holds**, which I
state because the two readings should not be allowed to quietly diverge:

1. **The margin is 6×.** 2 m 29 s against 15 minutes. For a human to fail, the
   same path would have to cost 12.5 minutes more than it cost here.
2. **The single stop is self-repairing from the error text** — §10.6, which I
   established by running the tool's own printed remedy rather than by assuming
   it was wrong. This is the specific finding that retires r2's own worry that
   *"a reader new to modules could lose several minutes here."*
3. **Neither pre-flagged stumble is live on the page** (`templ.Handler`'s frozen
   zero state, the `http.Handle` lines). r2 gives the reason and it is checkable
   and I checked it: §2's block is complete and correct and contains
   `app.PageHandler(Page)` and `app.Mux(...)` already, so a reader who copies
   cannot make either mistake.

**What I am not claiming:** that a human would take 2 m 29 s. Nobody has measured
that and this record does not.

### 10.10 G7

**G7:** *"DX is real — QA-1 builds a working small app from the docs alone, no
source reading, ≤15 min to first working counter."* `Gate: QA-1, Phase 4`.

**Discharged by the same evidence, and I do not think it needs its own.** G7's
five terms are a strict subset of what r2 establishes — QA-1 built it, it is a
working small app, from docs alone, with no source reading, in ≤15 minutes — and
**G7 carries no line clause**, so the part of box 2 that is delicate is not
G7's part. The box cites G7 beside FR-53 because FR-53's timing half *is* G7's
whole criterion.

**One thing worth recording, because it is a debt closing rather than a box
moving.** Box 1's tick at v0.8 carries qualification (c) in its own words: the
gate was held at `452e1e74`, *"DEV-3 has since rewritten the quickstart in seven
places … but not re-run docs-alone by QA-1"*, and *"a re-run against the
remediated page is not owed by this box and is not claimed here."* **r2 is that
re-run, against a page rewritten a second time since — the whole page shell.**
So G7's discharge today is *stronger* than the one box 1 was ticked on, and
qualification (c) is now satisfied as a by-product. **I am not re-grading box 1**;
it is ticked and it stays ticked. I am recording that the caveat inside its tick
no longer describes the tree.

**What I could not do for G7, and it is structural:** I cannot supply its
evidence myself. I have read the PRD, the library source, L9-1's review and r2.
G7 requires an agent that has not, and r2 is that agent. This is the one box on
which the grader is necessarily downstream of somebody else's run, which is why
§10.8 is as long as it is.

### 10.11 A number that arrives exactly on its target

31 against ≤31, margin zero. **This is the figure to be most suspicious of and I
treated it that way.** Three ways it could have been manufactured; none was.

1. **It was costed before the artifact existed, and the artifact matched the
   costing.** PRD §5.I (a), at v1.1 — *before* any `Document` symbol was in
   `live/` — costs a `live.Document` page shell at **20 Go + 11 templ = 31**,
   with the arithmetic printed: a shell that is *"`templ Page`, an
   `@live.Document` invocation, the `@Count(s)` child and two closing braces"* is
   **5**; 13 − 5 = 8; 19 − 8 = **11**. **The built `templ Page` is exactly those
   5 lines and the built file is exactly 11.** An artifact landing on a number
   published before it existed is the opposite of shopping for a number.
2. **Nothing was removed from the page to buy a line.** I checked this against
   the pre-shell **generated** file rather than against anyone's claim: I decoded
   the `WriteString` literals of `git show 8680e8c5^:…/view_templ.go` and the old
   hand-written shell emitted
   `<!doctype html><html lang="en"><head><meta charset="utf-8"><title>gotth-live quickstart</title></head><body>`.
   HEAD's `Document` emits that **plus** the runtime tag (which, in the old file,
   came from a `live.Script` component call and so is absent from the literals).
   **The document did not get smaller. The source did.**
3. **The obvious objection — "the count shrank by moving code across a
   boundary", which is the exact failure the v0.6 scope ruling exists to
   prevent — does not apply, and the distinction is worth being explicit
   about.** v0.6's concern is moving code between *the developer's own files*,
   Go ↔ templ, which is why the rule binds *"every file, whatever its
   extension"*. Moving ceremony **into the library** is not that evasion; it is
   the only route FR-53 has ever named. Box 2's own text says what closes it:
   *"the app shrinking — a library-owned page shell, DEV-1 to build, L9-1 to gate
   as new surface, QA-1 to re-count."* That is what happened, in that order, with
   the gate held before the recount.

**One thing I took from L9-1 by inspection rather than by running it:** that the
count does not rest on `app` being a package-level `var` — the alternative
inline closure `app.PageHandler(func(s State) templ.Component { return Page(app, s) })`
is also one line. That is true on its face and I did not build it.

**And the fact that makes the zero margin a condition rather than a shrug:**
L9-1 records that `view.templ`'s `@app.Document(…)` line is 84 columns where the
file's comments wrap at 79, and that `gen.sh` runs `templ generate` and no
`templ fmt --check`. **A human who wraps that call makes the count 32.** Add to
that §10.4's M2 and M3: the pin will not catch it if the wrap lands in both
artifacts, and will not catch a sample-side addition at all. **Nothing in this
tree can fail if this number moves.** That is Q-4, and at a margin of zero on a
box that took five versions to close, it is the condition I would keep if I had
to drop the other three.

### 10.12 The counter is live — my own drive, with a negative control

r2 observed the counter and reported it honestly. **The one thing static evidence
cannot give me is that the number changes without a page load**, so I drove it
myself, with my own CDP script, in `dis-gotth-live-bench:latest` — and, unlike
r2, **with a negative control**, because a probe that has never reported a dead
counter has not been shown to be able to.

Built from the **same fence ranges I counted** (`:75`–`:117`, `:331`–`:362`),
unmodified, so the counted artifact and the driven artifact are one artifact.
Trusted mouse input via `Input.dispatchMouseEvent` on the button's real box, not
`element.click()`.

| Probe | **LIVE** | **CONTROL** (same app, allowlist changed so every upgrade is refused) |
|---|---|---|
| `data-gotth-status` after load | `live` | `reconnecting` |
| `<output>` after load | `0` | `0` |
| navigation entries before | 1 | 1 |
| **`<output>` after clicks 1 / 2 / 3** | **`1` / `2` / `3`** | **`0` / `0` / `0`** |
| navigation entries after | **1** | 1 |
| `window.__qa1_sentinel`, set before click 1 | **survived all three** | survived |
| console errors | **0** | 0 |
| region HTML at the end | `<p data-gotth-region="count"><output>3</output> …` | `…<output>0</output>…` |
| after `Page.navigate` reload | `0`, sentinel **`GONE`** | `0`, sentinel **`GONE`** |

**The control is the whole point of the table.** One character of `Config` and
the identical probe reports a counter that does not count — while the navigation
entry stays at 1 and the sentinel survives, i.e. the clicks were dispatched and
the page did not reload; the number simply did not move. **So the probe can say
NO, and it said NO.** And the reload row is the control on the sentinel itself:
it dies on a document load, which is what licenses "it survived three clicks" to
mean "there was no document load."

**Second confirmation of §10.7 from the same run:** the control server spent the
whole session refusing upgrades and reconnecting, and wrote **0 bytes** to stdout
and stderr. That is §4's troubleshooting row's own scenario, producing no log at
all.

### 10.13 Verdict on box 2 — **PASS WITH CONDITIONS**

**≤15 minutes: PASS. ≤31 lines: PASS at exactly 31. G7: discharged. Box 2 goes
green.** The miss table's final row is **31 / 31 / 0**, after 16, 16, 16, 9, 8.

**None of the four conditions reopens this grade** — each is discharged by an
edit that cannot move the count, and I have checked that for each one.

| # | Condition | What discharges it | Owner | Blocking |
|---:|---|---|---|---|
| **Q-1** | **§4's build block leaves the reader with no `go.sum`, so the documented path errors for every reader, unconditionally** (§10.6). `go mod tidy`, `go get` and `go.sum` appear nowhere on the page | `go mod tidy` (or `go get .`) added to §4's block, or to §1 with a sentence saying why. **The fix lands in a `bash`/`text` block, which the v0.6 rule excludes as shell commands, so it cannot move the count** — I state that so the fix is not deferred out of fear of the budget | **DEV-3** | **Yes**, on the page. Not on this grade |
| **Q-2** | **§4's *"403s in the log"* points at a log the counted application cannot write** (§10.7), on the page's most likely reader error (`localhost` vs `127.0.0.1`) | Point the row at the network tab, as §4 already does two paragraphs earlier, **and** name the `localhost`/`127.0.0.1` trap at the row. **NOT by adding `Logger` to §2's `Config`: that is one counted line, takes the app to 32, and under trigger 1 as repaired it withdraws the amendment and reopens this box.** The `426` row's gloss (a cause stated where a condition is meant) rides along here | **DEV-3** | **Yes**, on the page. Not on this grade |
| **Q-3** | **The counting rule does not say whether entries inside a parenthesised `import ( … )` block are import lines** — 7 lines, on a clause with zero margin (§10.5) | FR-53's counted-lines bullet says what it has always meant: *"…not a `package` line, and not part of an `import` declaration, including the parenthesised block and its closing paren."* **This is a clarification of the rule as applied at all six measurements, not an amendment**, and §10.5(a) is the evidence for that. The quickstart's §-0 statement of its own rule should also carry the `go.mod` and `*_templ.go` exclusions the PRD has and the page does not | **PM-1** (requirement text) and **DEV-3** (the page's restatement of it) | No. **I ruled it; the condition stops the next grader having to** |
| **Q-4** | **Nothing in the tree can fail if the count goes to 32.** No line-count assertion exists anywhere (`grep -rn FR-53` over `*.go`/`*.sh` finds two incidental comments and no check; `ci.sh` has none), and the samples pin does not hold a count in either direction — my **M2** and **M3** mutations stayed green (§10.4). The whole margin is one 84-column line that no formatter will ever touch | One `It` in `docs/guide/_samples/samples_test.go` — the file that already reads the markdown — asserting the counted total of the quickstart's two marked blocks is **≤31**, by the §10.3 method, with the per-file split in the failure message. **Ginkgo v2 + Gomega, per project standard**, which is what that suite already is. It must be shown to go red at 32 before it is credited | **DEV-3** (or whoever owns `ci.sh`) | No. **The one I would keep if I had to keep only one** |

**Why none of these is a FAIL.** Q-1 and Q-2 are defects in a document, and the
box that grades this document's quality is box 1, which was held, passed, and
passed with eight findings against the page. Q-3 is a wording debt on a rule I
applied as it has always been applied. Q-4 is about the future of a number, not
about the number.

**Why the box is not an unconditional PASS.** Because a pass with zero margin,
held by no executable check, on a document whose printed build path does not
work, is a pass that the next commit can undo silently — and saying so in
conditions is the only instrument I have that survives this record being closed.

### 10.14 Routed

| # | To | Item |
|---|---|---|
| **Q-1**, **Q-2** | **DEV-3** | §10.13. Both blocking on `docs/quickstart.md`. **Q-2 carries a constraint**: the `Logger`-in-`Config` remedy costs a counted line and reopens box 2 |
| **Q-3** | **PM-1** (FR-53's text), **DEV-3** (the page's restatement) | §10.5. Clarification, not amendment. **I did not write it and it is not mine to write** |
| **Q-4** | **DEV-3** | §10.4, §10.13. Ginkgo v2 + Gomega, in the existing samples suite; must be shown red at 32 |
| **T-1** | **PM-1**, and it is not what the brief anticipated | **Trigger 1 does not fire.** Its condition is *"a library-owned page shell lands and the counted total is **not 31**"*. **The counted total is 31.** Trigger 4 needs the app *below* the budget; 31 is not below 31. Trigger 5 needs a change *"for a reason other than a library shrink"*; this was a library shrink. **No trigger fires and the budget does not move in either direction.** What PM-1 owes is a record that trigger 1 was **evaluated and its condition was not met** — not a firing. L9-1 anticipated this at their §6: 31 is the single arithmetic point at which neither branch fires |
| **T-2** | **PM-1** | **L9-1-C2's sequencing held, and I verified it rather than took it.** `git merge-base --is-ancestor 667d3db7 8680e8c5` → **yes**: the repaired trigger 1 was in force *before* DEV-1's shell, not in the same PR. **This is load-bearing on my grade**: under the pre-repair text, trigger 1 would have moved the budget up to whatever the shell cost and this box could not have failed at any cost, which would have made a PASS here worthless. It could have failed and it did not |
| **T-3** | **PM-1** | FR-53's miss table gains its final row — **budget 31, counted 31, miss 0** — and §5.I's *"Consequence, stated rather than avoided: FR-53 is NOT met today"* is now false and needs correcting beneath itself in this project's usual way. `docs/gates/phase-4.md` revision 4, already owed two corrections, now owes a third and a green box 2 |
| **F-12** | **DEV-1**, low | `live/page_test.go:396` still describes FR-53 as *"≤30 lines"*, two amendments stale. It is a comment on a spec that counts nothing, so it misleads no measurement — but it is in the file whose specs exist to hold the quickstart's shape |

**Not routed, because they are not mine:** L9-1's **PS-1** (DEV-1 remediated it
at `cbad05d8`; L9-1 is verifying in parallel), **PS-2**, **PS-3**. I checked only
the one thing box 2 needs from that review — §10.15.

### 10.15 What I checked that I could have taken on trust, and what I did not run

**Taken from nobody:**

* **The count.** Both artifacts, both readings, printed line by line, and across
  six commits. Four prior figures agreed with mine; I did not use any of them to
  reach mine, and §10.5(a) is the reproduction that anchoring cannot fake.
* **That the two counting paths are actually two.** A 43-line fenced block and a
  67-line file, sharing no line range, carrying an **identical ordered sequence**
  of 20 counted lines — checked as sequences, not as totals.
* **That the counted blocks did not move under the four commits between r2's
  `679e6695` and HEAD.** SHA-256 on both blocks: identical. One of those commits
  (`e7d47de6`) edited `docs/quickstart.md` — in prose at `:405`–`:413`, outside
  both fences. So r2's measurement covers the tree I graded.
* **That the pin credited with holding the two paths together does so.** It does
  not, in two of the four directions that move a count (§10.4, M2 and M3). This
  is a correction to L9-1's §7 and it is the origin of Q-4.
* **That the build genuinely fails from §1 → §4**, at HEAD, nine errors — *and*
  the counterfactual without §1's `require` block, which also fails, which is
  what shows the defect is a missing instruction rather than a wrong one.
* **That the Go tool's own printed remedy works.** It does. This contradicts r2
  and it lowers the severity of its own finding (§10.6).
* **That the counter is live**, driven by my own script, **with a negative
  control that reported a dead counter** — because a probe that has never failed
  has not been shown able to (§10.12).
* **That the process writes nothing**, across `101`, `403`, `426` and a full
  reconnect-storm session: 0 bytes, twice (§10.7).
* **That `L9-1-C2` preceded the shell**, by git ancestry rather than by reading
  the claim (§10.14, T-2).
* **That nothing was dropped from the page to buy a line**, by decoding the
  pre-shell generated file's own string literals (§10.11).

**Not run, and why:**

* **A full `ci.sh`.** Another agent was running one in the shared
  `dis-gotth-live` container while this pass ran (`bash ci.sh`, 18:25, with the
  chaos suite live). A second would have contended for the same container and
  would have told me nothing about box 2. L9-1 ran it in full at `679e6695`:
  `CI-EXIT=0`. **I confirmed instead the one thing my box needs from it** — that
  the `docs/guide/_samples` suite is green at HEAD, which I did on a throwaway
  copy while establishing the M1–M4 baseline.
* **A second timed run.** FR-53 names **one** timed measurement and r2 is it. A
  stopwatch held by an agent that has read the answers, the PRD and the library
  measures nothing about a developer following the page from zero, and producing
  one would have been theatre.
* **The docs-alone gate.** I cannot hold it. I read the PRD, the library source,
  L9-1's review and r2 before starting. That is why §10.8 checks r2's claims
  instead of replacing them.
* **L9-1's PS-1/PS-2/PS-3, and DEV-1's `cbad05d8` remediation of PS-1.** That
  review is open and is L9-1's; L9-1 states the resolution *"cannot move the
  count"*. I checked exactly that and no more: the counted blocks are
  byte-identical across `cbad05d8`, and the counted application still builds and
  still runs live at HEAD, which I established by building and running it rather
  than by reading the diff.
* **`app` as a package-level `var` versus the inline closure.** Taken from
  L9-1 by inspection; the alternative is one line either way on its face and I
  did not build it (§10.11).
* **Anything about a human's wall clock.** Nobody has measured it and §10.9 does
  not claim it.
* **`docs/gates/phase-4.md` revision 4, the PRD miss table, and FR-53's text.**
  Not my files. Routed at §10.14.

### 10.16 Status — what QA-1 has now graded in Phase 4

| Box | Criterion | Verdict | Where |
|---|---|---|---|
| **2** | **FR-53 + G7 — the timed counter, ≤15 min and ≤31 lines** | **PASS with four conditions** (Q-1…Q-4) | **§10** |
| **6** | The three examples *polished and documented* | **FAIL** at `091dbae8` → **PASS** at `368132f6` | §4.5, §9.1.7 |
| **7** | G11 — consumable from a clean clone | **PASS**, no conditions | §3 |
| **8** | FR-59 — the docs set, nine subjects | **PASS**, condition **F-10 closed** | §9.2.7, §10.4 |
| **12** | FR-58 — the error audit | **PASS**, condition **discharged** | §2, §9.3.4 |

**Five of Phase 4's thirteen boxes now carry a QA-1 grade and all five pass.**
**Box 2 closed on engineering, which is what its own text said was the only route
left** — a library-owned page shell, built by DEV-1, gated by L9-1, recounted
here — and not on an amendment. Boxes QA-1 gates and has still **not** graded:
**FR-54**, FR-57's dev reload, and FR-66/FR-68's godoc boxes. Box 13 is L9-1's.

— **QA-1**, correctness gate, merge-blocking, 2026-08-05, at `8be955e5`.

---

*Reproduce §10.*

```bash
cd gotth-live
D='docker run --rm -v '"$PWD"':/workspace:ro dis-gotth-live:latest bash -c'

# ---- §10.3 the count, both artifacts, both readings ----
#  classify each physical line under exactly four exclusions (blank, comment,
#  package, import) and print the classification; Reading A treats the whole
#  `import ( … )` declaration as import lines, Reading B only lines starting
#  with `import`.
#    docs/quickstart.md :75-117   -> A=20 B=24      docs/…/quickstart/main.go   -> A=20 B=24
#    docs/quickstart.md :331-362  -> A=11 B=14      docs/…/quickstart/view.templ-> A=11 B=14
#    TOTAL                           A=31 B=38

# ---- §10.5(a) the reading that reproduces the record ----
#  same method over the two blocks at each commit the record names:
#    8a06cb04 A=46 B=55 | 134e69c5 A=46 B=55 | fde707f0 A=39 B=46
#    93772adc A=39 B=46 | 679e6695 A=31 B=38 | HEAD     A=31 B=38
#  the record says 46, 46, 39, 39, 31. Reading A is 6 for 6; B is 0 for 6.

# ---- §10.4 the pin's four mutations, on a throwaway copy in the container ----
#  copy docs/guide/_samples + quickstart.md + docs/README.md + README.md to /tmp,
#  repoint the replace at /workspace, baseline green, then:
#    M1 doc block +1 counted line   -> RED    M3 doc block repeats a line 4x -> GREEN
#    M2 sample    +1 counted line   -> GREEN  M4 doc block re-indented       -> GREEN

# ---- §10.6 the documented path, verbatim ----
$D 'mkdir -p /tmp/c && cd /tmp/c && go mod init example.com/counter
    printf "\nrequire (\n\tgithub.com/a-h/templ v0.3.1020\n\tgithub.com/candacelabs/candace/pkg/gotth v0.1.0\n)\n" >> go.mod
    go mod edit -replace github.com/candacelabs/candace/pkg/gotth=/workspace
    sed -n "75,117p"  /workspace/docs/quickstart.md > main.go
    sed -n "331,362p" /workspace/docs/quickstart.md > view.templ
    templ generate && go run .'          # -> 9x "missing go.sum entry", exit 1
$D '… ; go get example.com/counter && go build ./...'   # the tool's own remedy: exit 0
grep -c "go mod tidy\|go get\|go.sum" docs/quickstart.md              # -> 0

# ---- §10.7 the log that is not written ----
sed -n '176p' internal/wsx/handler.go       # the Warn the row means
sed -n '81,84p;139p' internal/obs/log.go    # nil Logger -> nil *obs.Logger -> emit returns
#  build the counted app, drive a 101, a 403 (Origin: localhost) and a 426
#  (no Sec-WebSocket-Protocol):  stdout=0 bytes  stderr=0 bytes

# ---- §10.12 the drive, with the control ----
#  dis-gotth-live-bench:latest (go + node 24 + chromium), own CDP driver,
#  Input.dispatchMouseEvent on the button's box:
#    LIVE     status=live          clicks 1,2,3   nav 1->1  sentinel survives  console 0
#    CONTROL  status=reconnecting  clicks 0,0,0   nav 1->1  sentinel survives  console 0
#    (control = same app, Origins changed to an address the browser is not on)
#    reload on both: output 0, sentinel GONE  <- the control on the sentinel

# ---- §10.14 T-2, the sequencing that makes this grade mean anything ----
git merge-base --is-ancestor 667d3db7 8680e8c5 && echo "L9-1-C2 in force before the shell"
```

---

## 11. Box 3 — FR-54, the templ helper set *complete and documented*. Graded 2026-08-05 at `eb4971c6`

**Verdict: PASS WITH CONDITIONS. Phase 4's box 3 is MET, and the phase exits on
this grade.** Four conditions — **Q-5 … Q-8** — travel with the tick. All four
are documentation, each has a named owner, and **not one of them makes any
binding in the population inexpressible, uncomposable, undecided or
undocumented**, which is the only test that could have held the box open.

### 11.1 The standard, stated before it is applied

I grade against three things and nothing else.

1. **`docs/PRD.md`'s FR-54 definition** — the four clauses, quantified over the
   three-part population (a)/(b)/(c). That definition, not `gates/phase-4.md`
   §4.3's earlier "every event the examples bind", which PM-1 rejected as
   circular and which I am not reviving.
2. **L9-1's closure sentence**, `reviews/fr-54.md` §15: *"FR-54's box closes when
   FR54-3, FR54-4 and FR54-6 are discharged and QA-1 grades them."* L9-1's §24
   and §31 discharge is a **technical sign-off**. It is not a correctness grade
   and I did not treat it as one: every discharge below carries a run of mine.
3. **§14's nine pre-registered constraints, as corrected at §14's correction
   block and §24's closing note.** §11.4 says which of the three amendments I
   accept and why, and what a landing compliant with the *uncorrected* text
   would have had to look like.

And one procedural standard, which is this file's own: **a gate is what you ran.**
Every clause below names a command and a result. Every claim I could turn into a
mutation, I did — **fourteen controls**, §11.6. Two of them were built to
disconfirm something and disconfirmed the opposite thing instead, and those two
are the findings.

**What I did not treat as in scope.** `docs/reviews/**`, `docs/gates/**`,
`docs/qa/**` and `docs/pm/**` are dated records of other trees and other owners,
per `reviews/fr-54.md` §8's own boundary — **except where such a file makes a
current-state claim in a status line**, which `gates/phase-4.md` §7.2 already
established is a live claim rather than a record. Q-7 is that carve-out applied
once, to `docs/PRD.md`.

### 11.2 The gate steps, at HEAD, run by me

```
~/bin/dis run bash -c 'cd tools && go run ./apisurface'
  live 56/56 · 53/53 · 109/109 ; live/livetest 37/37 · 33/33 · 70/70   exit 0
~/bin/dis run bash -c 'cd tools && go run ./minify -check'
  Shipped gotth-live.min.js  10387 / 4459 ; ceiling 12288, headroom 7829 (63.7%)  exit 0
~/bin/dis run bash -c 'cd tools && go run ./doccheck'    every exported symbol documented, exit 0
~/bin/dis run bash -c 'go test ./... -count=1'           exit 0
go test ./live/ -v                 SUCCESS! 316 Passed | 0 Failed
node --test client/test/*.test.mjs 179 pass / 0 fail, counted per suite at runtime
  (binding 23, bundle 9, codec 34, dev-reload 18, inspector 15, morph 20,
   reconnect 35, resync 14, supersession 11)
browser conformance, Chromium 151  SUCCESS! 61 Passed | 0 Failed | 133 Skipped
gen.sh --check (full-root copy in /tmp)  "the committed output is byte-identical
                                          to a fresh generation"
docs/guide/_samples (its own module)     doc-block suite 159/159 ; keychords 15/15
```

I did **not** run `ci.sh`. The orchestrator's full gate at HEAD in
`dis-gotth-live-bench:latest` with `GOTTHLIVE_E2E=1` is exit 0 with **one step
skipped — G11**, which needs a host docker daemon; I cite it and re-ran, myself,
every step in it that carries a number this grade rests on. **Every mutation
below went to a copy under a container's `/tmp` with the worktree mounted `:ro`.
The worktree is byte-identical to `eb4971c6` at the end of this grade**, and the
only file I have written is this one.

### 11.3 Clause by clause, against the population

#### Clause 1 — Expressible. **MET.**

**(a) Every binding the tree renders.** I enumerated the call sites rather than
sampling them: `examples/{counter,chat,dashboard}` (4 clicks; a `submit`, an
`OnAll` of `keydown:Escape` + debounced `input`, a click; 4 clicks),
`docs/guide/_samples/{quickstart,events,htmxinterop,keychords}`,
`docs/quickstart.md`, and `bench/apps/{counter,chat,dashboard}/gotth/`. **Every
one is written through `Region`/`On`/`OnWith`/`OnAll`/`Preserve`/`Script`.**
`grep -rn 'data-gotth-'` over exactly those paths returns CSS selectors, test
assertions, prose and `bench/ready.js`'s status mirror — **no hand-assembled
attribute string in any rendered markup**, which is clause 1's second conjunct
and is the one a reader would assume rather than check. `gen.sh --check` is
byte-identical, so what I read is what is served.

**(b) The frozen `docs/bench/equivalence-spec.md` §2 tables.** `F-CTR-1…7`,
`F-CHT-1…9`, `F-DSH` regions A–E and the six controls, read against the gotth
side. The only member of (b) that was ever inexpressible is **`F-CHT-3`**, and it
is expressible now **and adopted by the measured artifact**:
`bench/apps/chat/gotth/bindings.go` renders
`keydown:chat.send:Enter::::1:1;input:chat.draft::150`, and the browser
conformance drives it. Two neighbours I checked because they look like gaps and
are not: **DSH-4**'s *"select 50/100/200"* is rendered as buttons for a stated
measurement reason (`dashboard.go:90`–`:95`), not because a `change` binding is
unavailable — `live.On("change", …)` takes any DOM event; and **F-CHT-9**'s
missing visible denial is `bench/README.md` G-8, a `Config.Authorize` render
question, not a binding one.

**(c) Every binding a document states is absent *because it cannot be
expressed*. This set is now EMPTY, and it is the clause I spent the most of this
pass on**, because the brief is right that it is where four agents found four
different instances. I swept the tree on fifteen phrasings — *cannot be
expressed*, *not expressible*, *inexpressible*, *no expression*, *has no way to
say*, *cannot express*, *is simply absent*, *is therefore absent*, *no key
filter*, *never calls `preventDefault`*, *modifier state is not compared*,
*binds Send to the button only*, *no non-JS expression*, *half-met*, *hand-written
JS* — and **read every hit** outside the four record families. Ten sites carried
such a sentence and **all ten now carry it corrected beneath itself, none of them
deleted**:

| site | state at HEAD |
|---|---|
| `docs/guide/when-not-to-use-this.md:155` | the whole gap **row** is quoted in a blockquote as history, with all three reasons named and closed beneath it, and the residual restated as a **refusal** with §13's trigger |
| `bench/README.md` §6 item 1 | struck, not deleted, with a three-row *reason → what closed it → where* table and the adopted markup |
| `bench/harness/interactions/CTR-5.mjs:19`–`:23` | corrected — and it is the **second** correction in that file, which the file says of itself |
| `bench/apps/counter/gotth/bindings.go:31`–`:35` | corrected, and better than the condition asked: it adds why **neither** option belongs on the counter (`+` **is** Shift+`=`) |
| `client/SIZE.md:753`–`:766` §5 | corrected beneath, with the surviving half (a filter *alone*) named |
| `docs/guide/events-and-forms.md:154`, `:170` items 4 and 5 | corrected beneath, each naming which sentence survives |
| `examples/counter/README.md:230`–`:236` | corrected beneath, with the bench counter's own `OnAll` printed as the refutation |
| `test/internal/conformance/keybinding_test.go:441` | corrected beneath, under §26's comment-only lift |
| `docs/api-surface.md:740`, `:744` | ⟨SUPERSEDED⟩ rows beneath, not over |
| `examples/chat/FRICTION.md` F-3 + `examples/chat/view.templ:64`–`:71` | closed; the affordance is implemented and the comment corrected |

**One thing in the brief did not reproduce as a finding and I want it on the
record because it cost me an hour and would have been a wrong FAIL.**
`examples/chat/FRICTION.md:202`–`:206` still reads, in the present tense,
*"`view.templ:62`–`:68`'s composer comment **still says** … 'Escape-to-clear has
no expression at all'"*, and that is false at HEAD. It is **not** a clause-4
violation, because F-3's own reading instructions at `:127`–`:138` name that
paragraph specifically: *"sentences below saying the affordance is absent, that
the clear is destroyed, that no shape has been chosen, or that `view.templ`'s
composer comment still carries the false reason were true when written and are
**not** true now."* The item is layered and it says so before a reader reaches
the layer. **Checked, not a defect.**

#### Clause 2 — Composable without silent loss. **MET.**

The clause's own sentence is *"several bindings on one element behave as each was
written"*, and at HEAD every `Bind` option is a component of the binding that
declared it. What I ran, rather than read:

- **`r = st[name]`** (M1) — the mutant that survived everything before FR54-4 —
  turns **exactly one** spec red, *"two bindings that share an event name hold
  independent timers"*, 22 pass / 1 fail. **FR54-4 discharged on my run.**
- **`s[7]` read off the element instead of the matched spec** (M2, C-5's own named
  control) — **exactly one** red, *"PreventDefault belongs to the binding that
  declared it and not to its sibling"*.
- **Deleting the `!s[6]` test** (M6) — **five** red, including the fall-through
  spec the two-binding `F-CHT-3` shape depends on.
- The guide's own composer, `docs/guide/_samples/events/view.templ:38`–`:41` — the
  element QA-1 drove in `fr-54-debounce-repro.md` and the one that made failure 2
  real — renders `keydown:c.clear:Escape;input:c.draft::150` with **no
  element-level option attribute at all**, and `examples/chat/chat_test.go:1390`
  asserts the absence rather than trusting it.

#### Clause 3 — Any gap refused, with an argument that outlives the example and a named re-open trigger. **MET, and the trigger is live rather than decorative — I fired every limb at it.**

Failure 1's two halves are **decided**: `Bind.NoModifiers` and
`Bind.PreventDefault` accepted (§12), the full modifier set **REFUSED** (§13).
The brief asks whether §13 is a real refusal with a real trigger and whether its
three limbs are checkable. **They are checkable, and I checked all three:**

- **T-1 — a second consumer. Count is still zero, and I counted rather than
  quoted it.** Grepped `docs/bench/equivalence-spec.md`, `examples/**`,
  `docs/guide/**` and `bench/**` for a required modifier state other than "none
  held": every hit is `Shift+Enter` (served), `Ctrl`/`Shift`+click on
  `PlainClick` (served), an `AltGr` **caveat** whose stated remedy is *"name such
  a key without this option"* (expressible), or the refusal itself. §2's frozen
  tables contain no `Ctrl`/`Meta`/`Alt` row. **T-1 does not fire.**
- **T-2 — the ≤ 4,475 B gzipped envelope. I built shapes and measured them, and
  this is where §13 has a defect.** See §11.5; the limb is satisfiable, which
  means the trigger is a real door and not a wall — and the price §13 argues from
  is not the price.
- **T-3 — L9-1's own evidence proving insufficient.** Driven, not read: the
  browser-labelled conformance set is **61 Passed | 0 Failed** at HEAD, and it
  contains `F-CHT-3` end to end through Chromium 151.
  **T-3's two clauses — `Shift+Enter` reaching the server, or the newline not
  being inserted — are both unmet. The refusal STANDS on my run as well as on
  L9-1's.**

**The refusal's argument outlives the example**, which is the harder half of the
clause. Ground 2 — that the surface cannot be two-valued, because a default of
"none held" breaks `F-CTR-6` since `+` **is** Shift+`=` — is a statement about
`KeyboardEvent.key` and about the frozen counter requirement, not about the chat
composer, and **I confirmed it by construction**: to get a working three-valued
encoding into my T-2 probe at all I had to introduce a sentinel (`""` = don't
care, `"0"` = exactly none), which is precisely the *"rule with one unpredictable
exception"* §13 refuses on and §3 ratified. Ground 3 is T-1's count. **Two of the
three grounds hold on my own evidence. The first does not — Q-5.**

#### Clause 4 — Documented, and the documentation is true of the tree. **MET, both halves.**

**First half — every helper and every option on
`docs/guide/events-and-forms.md` with its attribute spelling.** The vocabulary
table carries `Region`, `On`, `OnWith`, `OnAll` with the full eight-component
grammar, and a second table carries `Preserve` and `Script`; `Bind`'s **six**
fields are enumerated with the two new ones' component numbers and their `1`/absent
spelling (`:202`–`:203`). I checked the completeness claim rather than accepting
it: `grep '^func [A-Z].*templ\.'` over `live/*.go` returns exactly those six
package-level helpers, so *"two more helpers complete the templ surface"* is
true. **And the first half is mechanically pinned, which I proved with two
controls** (D1, D2): drifting the guide's printed fence away from
`docs/guide/_samples/keychords/keychords.go` turns the doc-block suite red naming
the line, and drifting `keychords.go` away from the attribute string turns the
keychords suite red naming the spelling. The new compiled sample module is 15
Ginkgo specs, `Expect`/`Gomega`, no mocks — correct under NFR-10.

**Second half — no document states as absent something the set expresses.**
That is the clause (c) sweep above, plus the two sites §26 and §27.2 opened:
`keybinding_test.go:441`'s *"and for nothing else"* is corrected beneath itself,
and **C-4's freeze holds under §26's scoped mechanical form** —

```
git diff d12870a0 -- test/internal/conformance/keybinding_test.go \
  | grep -E '^[+-]' | grep -vE '^(\+\+\+|---)' | grep -vE '^[+-][[:space:]]*//'
```

— **empty**, against `d12870a0` *and* against `42b4e0e6`, while the raw diff is
**38 insertions, every one a comment line**. `examples/counter/README.md` is
corrected beneath itself with the bench counter's `OnAll` printed as proof.
**One residual, and it is in the requirement document rather than in the library
— Q-7.**

### 11.4 The nine constraints, as corrected — and whether I accept the three amendments

| # | Verdict | What I ran |
|---:|---|---|
| **C-1** | **PASS** | `gen.sh --check` byte-identical on a full-root `/tmp` copy; the browser keybinding specs green with `keybinding_test.go` **executably unmodified** (empty scoped diff against both trees); **179** client specs green, counted per suite at runtime by me |
| **C-2** | **PASS** | `apisurface` → `live 56/56 · 53/53 · 109/109`, exit 0. I read `binding()` rather than inferring: the two booleans are `noMods, prevent := "", ""` inline, **no new identifier of any kind** |
| **C-3** | **PASS as amended** | `minify -check` reads **10,387 / 4,459** — *exactly* the amended ceiling. See below |
| **C-4** | **PASS** | The §26 mechanical check is empty against both trees; *"does not take the key away from the browser"* green inside the 61/61 browser run |
| **C-5** | **PASS** | **M2**, the control C-5 names by name: exactly one red and it is the owning spec |
| **C-6** | **PASS as amended, and I proved the amendment myself, in both harnesses** | **M4/M5/B2** below |
| **C-7** | **PASS** | 61 browser specs green at HEAD, `F-CHT-3` among them |
| **C-8** | **Routed, never blocking** | `bench/apps/chat/gotth` has adopted it; `bench/README.md` §6 item 1 and §8's row move beneath themselves. Not graded by me — `bench/**` is another owner's |
| **C-9** | **PASS** | **M3**: restoring the §12.1 prototype's placement turns exactly one spec red, *"PreventDefault does not fire mid-composition"* |

**I accept all three amendments. Here is why, and what I would have required
otherwise.**

**C-1's count — accept.** I did not take 179 on anyone's word; I ran the nine
suites and summed them per file. The correction moves the finding in the
strengthening direction (165 green under the FR54-4 mutant is a stronger
statement than 156), so nothing downstream of it moves.

**C-3's budget — accept the amendment to ≤ 10,387 / ≤ 4,459, and record what the
amended constraint now is.** Holding `≤ 4,455` as written would have required
shipping the §12.1 prototype, which places `preventDefault` **above** the
composition guard; **M3 proves that shape turns a committed spec red**, and it
would take the commit key from every IME composer, which is the population FR-26
exists for. **A landing compliant with C-3-as-written would have had to be a
landing that breaks CJK input.** That settles it. But I am recording the cost of
the amendment plainly, because L9-1 did not: **C-3-as-amended is a record, not a
gate.** Its number is exactly what `minify -check` reads at HEAD, so it cannot
fail. What is actually gating the bytes here is (i) NFR-2's 12,288 B ceiling,
untouched, with **63.7 % headroom**, and (ii) §19's merit argument for spending
one gzipped byte rather than duplicating the composition condition. I verified
the shipped figure and the direction of §18.2's table; **I did not rebuild all
seventeen spellings**, and §11.7 says so.

**C-6 requires the per-modifier table — accept, and this is the amendment I am
most confident in, because I drove it twice and neither run was L9-1's.**
Deleting `e.altKey` from the match condition leaves the **AltGr spec green** in
node (M4) *and* in Chromium (B2), and turns red only the per-modifier table DEV-1
wrote beyond the constraint. Deleting `e.metaKey` (M5) behaves the same.
**C-6-as-written would have certified a runtime with a dropped modifier read**,
and the landing is safe because DEV-1 wrote a spec the constraint did not ask
for. **A landing compliant with C-6-as-written would have been a landing whose
own evidence could not fail.**

**And that is exactly the sentence one comment in the landing gets wrong — Q-6.**
`client/test/binding.test.mjs`'s AltGr spec is introduced by *"this is the spec
that would go red if the runtime stopped reading one of the four."* **M4 and M5
show it does not.** That is a false sentence sitting in the file that is C-6's
evidence, three lines above the spec that actually has the property — the *reason
outliving its constraint* class this review found three times, found a fourth
time, in the landing rather than in the documentation. Its browser twin at
`keybinding_modifiers_test.go:475`–`:480` does **not** make the claim and is
correct as written.

### 11.5 The T-2 probe — I set out to show the trigger was dead and measured it alive, and the measurement is against §13

This is the one place I disagree with the record on substance, so I am showing
the numbers.

**The hypothesis I went in with.** §18.3 holds T-2's envelope at ≤ 4,475 B
gzipped *shipped*, on the ground that *"it was priced against the shipped
artifact of its day and nothing about C-9 touches it."* But T-2 is an **absolute**
ceiling on the whole artifact, and the artifact moved from 4,421 to 4,459
underneath it. That leaves **16 B of envelope where there had been 54 B** — and
§11's own table prices the modifier half at **+57**. On those numbers T-2 is
C-3's defect a second time and the refusal has a dead limb.

**So I built shapes and measured them with `tools/minify`, on a `/tmp` copy,
worktree `:ro`.** Each is HEAD's runtime with only the component-7 test replaced
by a three-valued modifier comparison:

| shape | minified | gzip | inside T-2's 4,475? |
|---|---:|---:|---|
| **baseline — HEAD, two-valued `NoModifiers`** | 10,387 | **4,459** | — |
| `+s[6] === e.shiftKey + 2*e.ctrlKey + 4*e.altKey + 8*e.metaKey` | 10,395 | **4,469** | **yes**, 6 B to spare |
| `+s[6] === (e.shiftKey?1:0) + (e.ctrlKey?2:0) + (e.altKey?4:0) + (e.metaKey?8:0)` | 10,412 | **4,473** | **yes**, 2 B |
| the `"scam"` string spelling with a `"0"` sentinel | 10,431 | 4,477 | no, by 2 B |
| **control — restored** | 10,387 | **4,459** | — |

**My hypothesis is refuted and the trigger is alive: T-2's byte limb is reachable
at HEAD by two spellings I constructed in an afternoon.** Good news for the
refusal — a re-open trigger nobody can satisfy is not a trigger.

**But it falsifies §13's first ground.** Ground 1 reads: *"**Price, measured.**
+57 gzipped bytes for the modifier half alone, against +34 for both accepted
fields together. **Fourteen times** the `preventDefault` half."* At HEAD, where
component 7 and the four `*Key` reads already exist, **the marginal cost of going
from "none held" to the full modifier set is +10 gzipped bytes** — 0.13 % of the
7,829 B headroom, not fourteen times anything. §11's `+57` prices the machinery
from a baseline that has not existed since `0b9e32e7`, and it prices it on a
prototype whose `preventDefault` placement C-9 forbids. **This is the third
instance of the exact defect §18.3 confessed — a number measured once and never
re-priced — and it survives in the same document that confessed it, in the
sentence that leads the refusal.**

**The refusal is not unseated and I am not re-opening it.** Grounds 2 and 3 are
independent of the price and both hold on my evidence: my own probe needed a
sentinel to be three-valued, which is ground 2 confirmed by construction, and
T-1's count is zero. **What is owed is the number, corrected beneath itself, so
that a future proposer is arguing against the real figure — Q-5.**

**And I am explicit about what my probe is not.** It is a **price probe, not a
proposal**: there is no `Bind.Modifiers` on the Go side, no specs, and — decisive
— **it does not satisfy T-2's zero-output-delta limb**, because a bitmask reading
of component 7 changes the meaning of the `"1"` that `bench/apps/chat/gotth` and
`docs/guide/_samples/keychords` render today. **T-2 has not fired.** What I have
shown is that its byte limb is reachable, and therefore that ground 1's headline
is not the price at which the question is being decided.

### 11.6 The three original failures

| | Verdict | On what |
|---|---|---|
| **Failure 1** — `F-CHT-3` inexpressible | **CLOSED, both halves, and by decision *and* artifact.** The expressible half landed (`0b9e32e7`) and is driven in Chromium; the refused half is refused under clause 3 with a trigger whose three limbs I fired myself | 61/61 browser; T-1 counted; T-2 measured; T-3 does not fire |
| **Failure 2** — element-scoped options | **FIXED** at `2ab18690` and **pinned** at `42b4e0e6`. The property three documents claimed is now the mutant M1 kills, and the guide's own composer renders no element-level option attribute | M1, M2, M6 |
| **Failure 3** — the tree calls inexpressible what the set expresses | **FIXED.** The affordance is implemented in `examples/chat`, F-3 carries its `— Closed.` heading with the refusal that preceded it kept above, and the relocated copy in `view.templ` is corrected. Clause (c) is empty | the §11.3(c) sweep |

**FR54-3, FR54-4 and FR54-6 are discharged on my runs**, which is the closure
sentence's second half: FR54-3 by **M8** (removing the `refuseUnbindable` call
turns **10 of 316** `live` specs red — L9-1's figure reproduces exactly, so the
refusals can say NO); FR54-4 by **M1**; FR54-6 by C-1…C-9 above.

### 11.7 My controls, and what each did

All on `/tmp` copies; the worktree was mounted `:ro` throughout.

| # | Mutation | Result |
|---|---|---|
| **M1** | `r = st[name]` instead of `st[specs[i]]` | **1 red** — *"two bindings that share an event name hold independent timers"* (22/1) |
| **M2** | read `s[7]` off the element, not the matched spec | **1 red** — *"PreventDefault belongs to the binding that declared it and not to its sibling"* |
| **M3** | hoist the `s[7]` `preventDefault` **above** the composition guard | **1 red** — *"PreventDefault does not fire mid-composition: Enter still commits the candidate"* |
| **M4** | delete `e.altKey` from the match condition | **1 red** — the **per-modifier table**. The **AltGr spec stays GREEN** |
| **M5** | delete `e.metaKey` | **1 red** — the same table. AltGr green again |
| **M6** | delete the whole `!s[6]` test | **5 red**, including the fall-through spec |
| **M7** | delete the `s[7]` `preventDefault` entirely | **3 red** |
| **M8** | remove `refuseUnbindable(…)` from `binding()` | **306 Passed / 10 Failed** of 316 `live` specs |
| **B1** | skip the modifier test for `click` only — falsifies the `MouseEvent` clause | **all 179 node specs GREEN**; browser **60/1**, and the one red is FR54-8's own spec. L9-1's §31 claim reproduces exactly |
| **B2** | delete `e.altKey`, re-minified, **in Chromium** | **60/1**, and the red is the `DescribeTable` entry **Alt**. **The browser AltGr spec stays green too** |
| **D1** | drift the guide's fence away from `keychords.go` | doc-block suite red: *"../events-and-forms.md:211 still matches keychords/keychords.go"* |
| **D2** | drift `keychords.go` away from the asserted spelling | keychords suite red: *"renders NoModifiers on a click binding as the page says it does"* |
| **P1–P3** | three full-modifier-set spellings, `tools/minify` | 4,469 / 4,473 / 4,477 against a 4,459 baseline; restored control back to 4,459 |

**Nothing turned red that should not have.** Baseline before every mutation and
after every restore: 179 node, 316 `live`, 61 browser, all green.

**The two that disconfirmed what they were built to confirm** — and they are the
two findings: **B2/M4/M5**, run to confirm C-6's amendment, showed that the
spec's *own comment* claims the property it lacks (Q-6); and **P1–P3**, run to
show T-2's envelope had been tightened out of reach, showed it reachable and
showed §13's leading price wrong instead (Q-5).

### 11.8 What this pass does not prove

- **G11 did not run.** The orchestrator's gate skipped it (host docker daemon)
  and so did I. Box 7 is separately graded at §3; nothing here re-establishes it.
- **`ci.sh` is cited, not re-run by me.** Every step in it that carries a number
  this grade rests on, I ran individually.
- **One browser, one version.** Chromium 151. `F-CHT-3`, the `MouseEvent` clause
  and the modifier reads are unproven on Firefox, Safari and WebKit, and
  `AltGr`'s `ctrlKey`+`altKey` reporting is a browser behaviour I took from the
  spec and from CDP rather than from four engines.
- **I did not rebuild §18.2's seventeen spellings.** I verified the shipped
  figure, the direction of the table, and three spellings of my own. If that
  table is wrong somewhere I did not touch, this grade does not catch it.
- **My T-2 probe is not a proposal** — no Go surface, no specs, and it fails
  T-2's zero-output-delta limb. It establishes a price, not a shape.
- **I did not verify the ten-pass Chromium drive `bench/README.md` publishes for
  its own chat app**, and I did not re-run any benchmark. `bench/**` is another
  owner's and C-8 was never blocking.
- **The `PreventDefault`-outside-a-region disclosure is untested.** The guide
  states, truthfully (I read `dispatch`), that a `PreventDefault` binding outside
  every `live.Region` suppresses the browser's default and sends nothing. Nothing
  asserts it. It is a true sentence with no spec — the same shape as FR54-8
  before `8363396c`, and I am recording it rather than conditioning on it.
- **Clause (c) is a sweep, and a sweep is a bounded claim.** Fifteen phrasings,
  every hit read outside the four record families. A sentence that states a
  binding absent in words none of the fifteen matched would have survived me, and
  this project has now found four such sentences after four declarations that the
  sweep was complete.

### 11.9 Conditions — QA-1's, travelling with the tick

None of these gates the box; each has a named owner; each is a correction beneath
itself in this project's house style.

| # | Condition | Owner |
|---|---|---|
| **Q-5** | **`reviews/fr-54.md` §13's ground 1 states a price that is no longer the price.** *"+57 gzipped bytes for the modifier half alone … fourteen times the `preventDefault` half"* is measured on the pre-`0b9e32e7` baseline and on a prototype C-9 forbids. **Measured at HEAD, the marginal cost of the full modifier set is +10 gzipped bytes** (§11.5). Grounds 2 and 3 are untouched and the refusal stands; the number should be corrected beneath itself so a future T-2 proposal argues against the real figure. §18.3's *"nothing about C-9 touches [T-2]"* is the sentence that carried it | **L9-1** |
| **Q-6** | **`client/test/binding.test.mjs`'s AltGr spec is introduced by a false sentence:** *"this is the spec that would go red if the runtime stopped reading one of the four."* **M4, M5 and B2 show it stays green** when `altKey` or `metaKey` is dropped; the per-modifier spec three lines below is the one with the property. This is the vacuity §14's C-6 correction is about, asserted as its opposite inside C-6's own evidence file. *(Nit in the same area: `keybinding_modifiers_test.go`'s `DescribeTable` uses the literal `4` for Meta where the other three entries use named `mod*` constants, and no `modMeta` exists.)* | **DEV-1** |
| **Q-7** | **`docs/PRD.md` — the document that owns this box — is stale on two of the three failures it grades.** Its header **Status** row still reads *"failure 2 is **measured** … and failure 3's false reason is corrected in place … **while the affordance stays absent**"*; failure 2 was **fixed** at `2ab18690` and the affordance landed at `b6bfe108`. FR-54's failure-3 block still closes *"**Until that comment moves**, the tree still states as inexpressible something the set expresses, so failure 3 is *not* closed, it is *relocated*"* — the comment moved. This is FR54-9's class in the requirement rather than in the gate record, it is on nobody's list, and by §7.2's own precedent a status line is a live claim | **PM-1** |
| **Q-8** | **`refuseUnbindable` and §22.3 disagree at HEAD, and both are documented as if they did not.** §22.3 **RULES** an empty `domEvent` REFUSED; the code refuses four things and its godoc says *"Those four and nothing else"*, and `docs/guide/events-and-forms.md`'s refusal table says *"Nothing else is refused"*. The **tree is self-consistent and the ruling is the outlier**. I agree with L9-1's placement — FR54-7 travels behind, because moving the closure condition after the artifact exists is C-3's error mirrored — and I am conditioning only that the divergence not travel silently: either land the fifth refusal, or note in `refuseUnbindable`'s godoc that a fifth is ruled and pending | **DEV-1** |

### 11.10 Verdict, and the sentence PM-1 may quote

**Box 3 — FR-54, "templ helper set complete and documented" — is MET.
PASS WITH CONDITIONS Q-5 … Q-8. Phase 4's thirteenth box ticks and the phase
exits.**

> **FR-54's Phase-4 exit box is graded PASS WITH CONDITIONS by QA-1 at
> `eb4971c6`: the helper set is complete under the PRD's four-clause definition
> across all three parts of its population — clause (c) is empty, and I swept it
> rather than inherited it; FR54-3, FR54-4 and FR54-6 are discharged on QA-1's
> own runs and not on the reviewer's, with the mutant that reintroduces failure 2
> turning exactly its owning spec red and the removed refusal turning 10 of 316
> `live` specs red; all nine of `reviews/fr-54.md` §14's constraints hold as
> corrected, and QA-1 accepts all three amendments — C-6's on evidence QA-1 drove
> itself, in node and in Chromium, showing that the constraint as written would
> have certified a runtime with a dropped `altKey` read. The four conditions are
> documentation, each has an owner, and not one of them makes any binding
> inexpressible, uncomposable, undecided or undocumented.**

**And the finding I would rather this row carried than the pass.** §13's leading
refusal ground prices the full modifier set at a figure that has been wrong since
the landing it refuses beside: **+10 gzipped bytes at HEAD, not +57**. I went
looking for a dead re-open trigger and found a live one attached to a dead
number. The refusal survives on its other two grounds — but *"fourteen times the
`preventDefault` half"* is the sentence a future proposal will be argued against,
and it is off by roughly five. **Q-5, and it belongs at the top of this section
rather than the bottom of the table.**

— **QA-1**, correctness gate, merge-blocking, 2026-08-05, at `eb4971c6`.

### 11.11 Status — what QA-1 has now graded in Phase 4

| Box | Criterion | Verdict | Where |
|---|---|---|---|
| **2** | FR-53 + G7 — the timed counter | **PASS** with Q-1…Q-4 | §10 |
| **3** | **FR-54 — the templ helper set, complete and documented** | **PASS** with **Q-5…Q-8** | **§11** |
| **6** | The three examples, *polished and documented* | **FAIL** at `091dbae8` → **PASS** at `368132f6` | §4.5, §9.1.7 |
| **7** | G11 — consumable from a clean clone | **PASS**, no conditions | §3 |
| **8** | FR-59 — the docs set, nine subjects | **PASS**, condition closed | §9.2.7, §10.4 |
| **12** | FR-58 — the error audit | **PASS**, condition discharged | §2, §9.3.4 |

**Six of Phase 4's thirteen boxes now carry a QA-1 grade and all six pass.**
Boxes QA-1 gates and has still **not** graded: FR-57's dev reload and
FR-66/FR-68's godoc boxes. Box 13 is L9-1's.

---

*Reproduce §11.*

```bash
cd gotth-live
# from the repository ROOT for the docker lines
RO='docker run --rm -v '"$PWD"':/workspace:ro -w /workspace/candace/pkg/gotth dis-gotth-live-bench:latest bash -c'

# ---- §11.2 the gate steps ----
~/bin/dis run bash -c 'cd tools && go run ./apisurface'        # live 56/56 · 53/53 · 109/109
~/bin/dis run bash -c 'cd tools && go run ./minify -check'     # 10387 / 4459, headroom 7829 (63.7%)
~/bin/dis run bash -c 'cd tools && go run ./doccheck'          # exit 0
~/bin/dis run bash -c 'go test ./... -count=1'                 # exit 0
$RO 'go test ./live/ -count=1 -v' | grep -E "SUCCESS!|FAIL!"   # 316 Passed | 0 Failed
$RO 'for f in client/test/*.test.mjs; do node --test "$f"; done'   # sums to 179
$RO 'GOTTHLIVE_E2E=1 CHROME_BIN=/usr/bin/chromium go test ./test/internal/conformance/ \
     -count=1 -v -args -ginkgo.label-filter=browser -ginkgo.fail-on-empty'  # 61 | 0 | 133 skipped

# gen.sh regenerates in place: copy the WHOLE repo root, never the worktree
docker run --rm -v "$PWD:/workspace:ro" dis-gotth-live-bench:latest \
  bash -c 'cp -r /workspace /tmp/root && cd /tmp/root/gotth-live && ./gen.sh --check'
#   -> "the committed output is byte-identical to a fresh generation"

# ---- §11.3 clause 4 / C-4, the §26 mechanical form ----
git diff d12870a0 -- test/internal/conformance/keybinding_test.go \
  | grep -E '^[+-]' | grep -vE '^(\+\+\+|---)' | grep -vE '^[+-][[:space:]]*//'   # empty
git diff --stat d12870a0 -- test/internal/conformance/keybinding_test.go          # 38 insertions

# ---- §11.7 the mutations: copy to /tmp, edit the copy, never the worktree ----
#   M1 s/r = st[specs[i]] || (st[specs[i]] = {})/r = st[name] || (st[name] = {})/  -> 1 red
#   M2 s/if (s[7]) e.preventDefault();/…split(":")[7]…/                            -> 1 red
#   M3 move `if (s[7]) e.preventDefault();` above `if (composing) return;`          -> 1 red
#   M4 drop e.altKey  from the four-boolean test  -> 1 red: the per-modifier table
#   M5 drop e.metaKey from the four-boolean test  -> 1 red: the per-modifier table
#      (in BOTH: the AltGr spec stays GREEN — this is C-6's vacuity, and Q-6)
#   M6 replace the whole (!s[6] || !(…)) clause with `true`                         -> 5 red
#   M7 delete `if (s[7]) e.preventDefault();`                                       -> 3 red
#   M8 replace `refuseUnbindable(domEvent, eventName, b.Keys)` with `_ = refuseUnbindable`
#      go test ./live/ -v   -> FAIL! 306 Passed | 10 Failed
#   B1 (!s[6] || e.type === "click" || !(…))   -> node 179 GREEN, browser 60|1,
#      the red is "filters a Ctrl+click and a Shift+click … (FR54-8)"
#   B2 M4 re-minified and run in Chromium      -> browser 60|1, the red is
#      "reads all four modifier booleans, not a subset  [It] Alt"; AltGr green

# ---- §11.4 clause 4 first half is pinned: the two drift controls ----
#   D1 edit the guide fence in docs/guide/events-and-forms.md so it no longer matches
#      docs/guide/_samples/keychords/keychords.go
#      cd docs/guide/_samples && go test . -> RED "…:211 still matches keychords/keychords.go"
#   D2 edit keychords.go's PlainClick to live.Bind{}
#      go test ./keychords/ -> RED "renders NoModifiers on a click binding as the page says"

# ---- §11.5 the T-2 price probe, tools/minify on a /tmp copy ----
#   replace `(!s[6] || !(e.shiftKey || e.ctrlKey || e.altKey || e.metaKey))` with a
#   three-valued modifier comparison, then: cd tools && go run ./minify
#     baseline (HEAD)                                            10387 / 4459
#     +s[6] === e.shiftKey + 2*e.ctrlKey + 4*e.altKey + 8*e.metaKey  10395 / 4469  <= 4475
#     +s[6] === (e.shiftKey?1:0)+(e.ctrlKey?2:0)+(e.altKey?4:0)+(e.metaKey?8:0)
#                                                                10412 / 4473  <= 4475
#     the "scam" string spelling with a "0" sentinel             10431 / 4477  > 4475
#     restored control                                           10387 / 4459
#   NOT a proposal: no Go surface, no specs, and it fails T-2's zero-output-delta limb.

# ---- §11.3 clause (c), the sweep ----
grep -rniE "cannot be expressed|not expressible|inexpressible|no expression|cannot express|\
has no way to|no way to say|is simply absent|is therefore absent|no key filter|\
never calls preventDefault|modifier state is not|binds Send to the button|half-met|\
no non-JS|hand-written (JS|JavaScript)" \
  --include=*.go --include=*.templ --include=*.md --include=*.js --include=*.mjs . \
  | grep -vE "^\./docs/(reviews|gates|qa|pm)/"
#   -> ten sites, every one corrected beneath itself; read, not counted.
```
