# Duplication review — REV-DUP

| | |
|---|---|
| **Reviewer** | REV-DUP (principal engineer, single lens: duplication) |
| **Date** | 2026-08-04 |
| **Lens** | Code that exists in ≥ 2 places and should be one shared library |
| **Reviewed** | the whole `candace/pkg/gotth/` tree at `af9057d1`, excluding `bench/apps/*/next/.next/` and `node_modules/` (build artefacts) |
| **Governed by** | [review checklist](../review-checklist.md) §1.3, §1.4 · [api-surface.md](../api-surface.md) §0.1 · [dependencies.md](../dependencies.md) §2, §4 |
| **Verdict** | One extraction landed. **Nine specifications** left for owners. **Nine duplications ruled DELIBERATE** and certified to stay. |

**The governing rule.** Checklist §1.4 requires an abstraction to have ≥ 2 real
call sites. This review is the other half of that rule: finding the ≥ 2 call
sites that already exist. Its constraint is that **module separation is
intentional** (FR-74 / D7 quarantine), so a duplication spanning two modules is
not automatically a defect — the question is always whether a *reachable*
shared home exists, and for most of what follows the honest answer is no.

**Scope discipline.** `internal/session`, `internal/protocol`, `live/` core and
the driver's in-flight files were report-only throughout. The driver committed
`945d55b8`, `9892c991` and `af9057d1` during this review and opened new work in
`live/config.go`; nothing here touches any of it.

---

## 0. Summary

| # | Duplication | Copies | Disposition |
|---|---|---:|---|
| **D-1** | `allowedOrigins`, and a bind-all arm three of six copies never got | 6 | **EXTRACTED** — `af485014` |
| **D-2** | `livetest.NewTB` duplicates `ginkgo.GinkgoTB()`, which Ginkgo ships for this exact purpose | 2 idioms, 33 sites | **EXTRACTED** — `281586c3`. Symbol and suite removed by L9-1's ruling 1; the 16 Go call sites across 7 modules and the guide's four fenced blocks migrated in one commit |
| **D-3** | `livetest.Client` is ledgered and unimplemented, so four suites hand-roll a session driver | 4 | **EXTRACTED (partial)** — `8756b861` builds `Client`; `d853ed83` and `8725629e` retire `examples/chat` and `examples/dashboard` onto it, −961 lines. `test/routers` still **SPECIFIED**. *"No new exported symbol"* was wrong — see §3 |
| **D-4** | `checkListBounds` and `checkEnums` are the same descriptor walk, twice, on the hot parse path | 2 | **SPECIFIED** — owner of `internal/protocol` |
| **D-5** | The module list: 6 unrolled blocks in `ci.sh`, a second projection in `gen.sh`, no single source | 2 + 6 | **EXTRACTED** — `451c88ca`, as a guard rather than a unification, per the constraint this review stated. `ci_modules` is checked three ways against the tree: a `go.mod` with no entry, an entry with no `go.mod`, and an entry named nowhere else in `ci.sh`. The steps are deliberately NOT collapsed — the citations argument held — so the array-to-step coupling is a self-grep of the script's own text, and that check's weakness (it proves a path is *named*, not *run*) is documented at the site rather than left to be discovered |
| **D-6** | `bench/ready.js` — three byte-identical *tracked* copies, no sync script, no verify target | 3 | **EXTRACTED** — `a313e433`, deviating from this section's specification in one respect: **the copies stay git-tracked rather than gitignored**. `//go:embed bench/ready.js` is resolved by the compiler, so an ignored copy makes a clean checkout fail with `pattern bench/ready.js: no matching files found` in `dis-gotth-live:latest` — the node-free image `ci.sh` builds all three bench modules in, which cannot run `sync-ready.mjs` to repair itself. Measured, not argued. The verification was what the finding actually needed, because **a drifted copy still compiles**: `verify-ready.sh` is `sh` + `cmp` with two callers (`npm run verify:ready` and a `ci.sh` step) rather than a `--verify` flag reachable only through npm |
| **D-7** | `LoadShim` / `serveScript` / `serveCSS` across the three bench modules | 3 | **SPECIFIED** (low priority) |
| **D-8** | The "walk up to the enclosing `go.mod`" loop in `gen.sh`, one copy missing its `/` sentinel | 2 | **EXTRACTED** — `f95b86c1`. One `enclosing_module_dir()`, sentinel intact, both call sites on it; the first site's error message survives verbatim for both. Driven directly under `timeout 10`: outside every module it now prints and exits non-zero instead of spinning |
| **D-9** | `step`/`say`/`die`/`usage`/arg-parsing across four bash scripts | 3–4 each | **SPECIFIED** (low priority) |
| **D-10** | The `":PORT"` residual, in all six `allowedOrigins` copies | 6 | **SPECIFIED** — deliberately *not* fixed in D-1 |
| **R-1**…**R-9** | Nine duplications examined and ruled correct as they stand | — | **DELIBERATE** |

---

## 1. D-1 — EXTRACTED: the bind-all Origin arm (`af485014`)

`allowedOrigins(addr, extra string) []string` is written **six times**:
`examples/{counter,chat,dashboard}/main.go` and
`bench/apps/{counter,chat,dashboard}/gotth/main.go`. Measured:

- the three example copies are **byte-identical** to each other (19 lines);
- the three bench copies are identical to each other modulo one `gofmt` line
  wrap (`bench/apps/counter` wraps a two-argument `append`);
- example vs bench differ by **exactly one `switch` arm**:

```go
case "0.0.0.0":
    origins = append(origins, "http://127.0.0.1:"+port, "http://localhost:"+port)
```

Six separate modules, so there is no package to share and no extraction to
perform. What there is instead is **one-directional drift**: the bench copies
were written later, hit the bug, fixed it, and nobody told the examples.

**The drift was a live defect, not a style difference.** All three example
READMEs give the containerised invocation as `-addr 0.0.0.0:PORT`
(`examples/chat/README.md:19`, `examples/counter/README.md:18`,
`examples/dashboard/README.md:22`). `0.0.0.0` is a bind address and no browser
ever sends it as an `Origin`, so following the documented instructions built an
allowlist of exactly `["http://0.0.0.0:PORT"]` — which nothing can match. Every
upgrade from a browser at `http://localhost:PORT` was refused with 403. **The
documented way to run all three examples did not work.**

**Why the existing spec did not catch it.** `examples/dashboard` had an
allowlist spec that *named* `0.0.0.0` and passed anyway
(`dashboard_test.go:423-427`): it asserted only that the result is not
`live.AnyOrigin`. An allowlist that allows nobody is not the wildcard. That is
the shape of assertion the fix replaces.

**Landed.** Each example gains an `It` naming the loopback spellings for the
bind-all address; `counter` and `chat` gain the Origin-allowlist `Describe`
they never had. **Mutated:** with the arm removed, the counter spec fails on
`ContainElements` — so it is not passing vacuously.

**Converged to the bench spelling exactly rather than improved past it**, because
a seventh variant is the disease. See D-10 for what that leaves.

> Verified in `dis-gotth-live:latest`: `go build`, `go vet`, `gofmt -l` and
> `go test -race -count=1` green in `examples/counter`, `examples/chat` and
> `examples/dashboard`. Net **+94 lines** — this one costs lines rather than
> saving them, because the duplication was unremovable and only the divergence
> could be closed.

---

## 2. D-2 — SPECIFIED: `livetest.NewTB` duplicates a symbol Ginkgo already ships

**This is the largest finding, and it is an exported symbol, so it is a ruling
rather than an edit.**

`live/livetest/tb.go` ships `NewTB(fail, out) testing.TB`, a 40-line adapter
that embeds a nil `testing.TB`. Its stated premise, in the godoc, in
`doc.go:11-16`, in `api-surface.md` §6, and in `dependencies.md` §4, is:

> Every helper below takes a `testing.TB`, and **a Ginkgo suite has no way to
> produce one** — `testing.TB` carries an unexported method.

That is true of `GinkgoT()`. It is **false of `GinkgoTB()`**, which Ginkgo ships
for precisely this purpose. Measured in-container against the version this
module already requires (`ginkgo/v2 v2.32.0`):

```
$ go doc github.com/onsi/ginkgo/v2 GinkgoTB
func GinkgoTB(optionalOffset ...int) *GinkgoTBWrapper
    GinkgoTB() implements a wrapper that exactly matches the testing.TB interface.
    ...
    This wrapper satisfies the testing.TB interface and intended to be used as a
    drop-in replacement with third party libraries that accept testing.TB.
```

**The repository already agrees with the upstream answer, in both directions at
once.** Two idioms are live for the identical construct:

| Idiom | Call sites |
|---|---:|
| `livetest.NewTB(Fail, GinkgoWriter)` | 23 |
| `ginkgo.GinkgoTB()` | 10 |

and **five files use both** — `examples/chat/chat_test.go`,
`examples/counter/counter_test.go`, `bench/apps/chat/gotth/chat_test.go`,
`bench/apps/counter/gotth/counter_test.go`, and `live/livetest/session_test.go`,
the package's own suite. The sharpest instance is one line apart in sibling
modules, for the same call:

```go
// bench/apps/counter/gotth/counter_test.go:367
return livetest.NewSession(GinkgoTB(), id, anonymous{})
// bench/apps/dashboard/gotth/dashboard_test.go:726
return livetest.NewSession(livetest.NewTB(Fail, GinkgoWriter), tabA, identity)
```

**The dependency argument does not rescue it.** `NewTB` takes a handler so that
`livetest` need not import Ginkgo — and `GinkgoTB()` is called *from the
consumer's own test file*, so `livetest` imports nothing either way. The
measured 17-module, 3.48 MB cost in `dependencies.md` §4 is the cost of
`livetest` importing Ginkgo, which neither option pays. **Zero dependency
difference.** That §4 row stays correct and becomes moot.

**The residual justification has no call site.** What `NewTB` buys over
`GinkgoTB()` is that it works for a non-Ginkgo framework. Checklist §1.4 wants
≥ 2 real call sites for an abstraction; there is exactly **one** spec framework
in this repository and the operator mandates it. The generality has zero
consumers, which is the condition §1.4 exists to reject.

**Argued honestly against myself:** `NewTB`'s nil-embedded `TB` panics on any
method it does not implement, a property `tb_test.go` proves and the ledger
calls "the failure mode to want" — if a helper grows a `tb.Cleanup` call, the
suite stops there. `GinkgoTB()` *implements* `Cleanup` correctly instead, which
is strictly better than stopping. The property is not lost; it is obsoleted.

**Specification (owner: L9-1 for the ruling, DEV-1 for the edit).** Remove
`livetest.NewTB`; rewrite the 23 call sites to `GinkgoTB()`; delete `tb.go` and
`tb_test.go`; correct `doc.go:11-16`, `api-surface.md` §6 and
`docs/guide/testing-your-app.md` §"Ginkgo: `livetest.NewTB` is the adapter",
all of which state the false premise as fact. Drafted ledger row for §10:

> ### Checkpoint 3, REV-DUP — `livetest.NewTB` withdrawn in favour of `GinkgoTB()`
>
> | Change | Source |
> |---|---|
> | **`livetest.NewTB` removed**, `live/livetest` ceiling **10 → 9** identifiers; struct fields unchanged at 6. Ginkgo ships `GinkgoTB()` — *"a wrapper that exactly matches the testing.TB interface … intended to be used as a drop-in replacement with third party libraries that accept testing.TB"* — and this module already requires `v2.32.0` and already called it at **10** sites while calling `NewTB` at 23. The adapter's premise, *"a Ginkgo suite has no way to produce one"*, is true of `GinkgoT()` and false of `GinkgoTB()` | measured, `go doc github.com/onsi/ginkgo/v2 GinkgoTB` |
> | **No dependency moves.** `NewTB` took a handler so `livetest` need not import Ginkgo; `GinkgoTB()` is called from the consumer's test file, so `livetest` imports nothing either way. dependencies.md §4's measured 17-module cost is the cost of a *`livetest` import of Ginkgo*, which neither option pays | dependencies.md §4, unchanged and now moot |
> | **The nil-`TB` panic property is obsoleted rather than lost.** `NewTB` panicked on an unimplemented `testing.TB` method so a helper growing a `tb.Cleanup` call would stop loudly; `GinkgoTB()` implements `Cleanup`, which is the better answer to the same question | `tb_test.go`, deleted with the symbol |
> | **The five files that spelled it both ways stop disagreeing.** `livetest.NewTB` and `GinkgoTB()` were live in the same file in `examples/{chat,counter}`, `bench/apps/{chat,counter}/gotth` and `livetest`'s own suite | REV-DUP §2 |

**If L9-1 rules to keep `NewTB`**, the finding does not evaporate: the 10
`GinkgoTB()` sites must then be converted *to* it, because one construct with
two spellings in one file is the duplication either way.

---

## 3. D-3 — SPECIFIED: the unimplemented `livetest.Client`, and the four drivers filling the gap

`api-surface.md` §6 ledgers `Client`, `Audit` and `Report`; `live/livetest/doc.go:43-46`
records them as not yet built. In their absence **four suites hand-roll a
WebSocket session driver**, and three of those additionally hand-roll a protobuf
decoder:

| Module | File | Lines | Codec | Driver type |
|---|---|---:|---|---|
| `examples/chat` | `wire_test.go` | 1,309 | hand-rolled `protowire` | `browser` |
| `examples/dashboard` | `wire.go` + `wire_test.go` | 428 + 1,408 | hand-rolled `protowire` | `browser` |
| `test/routers` | `wire_test.go` + `harness_test.go` | 290 + 408 | hand-rolled `protowire` | `browser` |
| root | `test/internal/conformance/harness_test.go` | 548 | `pb` types | `driven` |
| root | `test/internal/chaos/wire_test.go` | 749 | `pb` types | *(driver WIP — report-only)* |
| `test/sampling` | `harness_test.go` | 292 | `pb` types | `driven` |

The codec halves are not merely similar. `eachField` — the 38-line
`protowire` tag loop — is **byte-identical** across `examples/chat/wire_test.go:137-174`,
`examples/dashboard/wire.go:173-210` and `test/routers/wire_test.go:121-158`.
Each is followed by its own `decodeFrame` / `decodePatch` / `decodeOrigin` /
`decodeUpdate` / `decodeError`, differing only in identifier case. The driver
halves repeat `pump` / `next` / `await` / `settle` / `send` / `ack` six times
with the same control flow and different error prefixes.

**Why this is not simply a defect.** Each copy has a real reason to exist where
it is, and for `test/routers` the reason is written down and is a good one
(`test/routers/go.mod:18-26`): the module *could* import
`gotth-live/internal/...` — Go's internal rule is import-path-prefix based, and
`test/sampling` demonstrates this by doing exactly that — but it deliberately
does not, because *"a mounting test that proved 'the session works' with the
library's own private codec would be proving it with a tool no reader can pick
up."* The examples have the same constraint from `measures like a consumer`.

**But `livetest` is a package a reader can pick up.** That is the whole point of
the second exported package, and it dissolves the objection: a `livetest.Client`
satisfies "a consumer could do this" in a way `internal/protocol` never can.
So the duplication is the *symptom*, and the cure is already designed, already
ledgered, and simply not built.

**Specification (owner: DEV-1).** Implement `livetest.Client` per
`api-surface.md` §6 — `Send`, `WaitFor`, `Close`, already ledgered, **no new
exported symbol and therefore no L9-1 ruling**. Then retire the hand-rolled
codecs in `examples/chat`, `examples/dashboard` and `test/routers` onto it. The
reachable saving is on the order of **2,400 lines**, and the safety property is
better than the saving: five copies of a protocol decoder are five places for
the wire format to be understood slightly wrong.

### 3.1 Disposition — EXTRACTED for two of the three, and what the third is waiting on

`livetest.Client` is built (`8756b861`) and two of the three named suites are on
it. Measured, against the line counts in the table above:

| Suite | Before | After | Δ |
|---|---:|---:|---:|
| `examples/chat/wire_test.go` | 1,309 | 853 | **−456** |
| `examples/dashboard/wire_test.go` | 1,408 | 1,303 | −105 |
| `examples/dashboard/wire.go` | 428 | 394 | −34 |
| `test/routers` (`wire_test.go` + `harness_test.go`) | 698 | 698 | — |

Every touched suite is green under `-race` with the same spec count it had
before: chat 155, dashboard 71.

**The honest arithmetic, because this finding's headline was a line count.**
−595 in the examples, against **+739** of new non-test code in `livetest`
(`client.go` 493, `frame.go` 246) and +373 of specs for it. So **the repository
is not yet smaller, and this review's *"on the order of 2,400 lines"* is a
figure about the finished migration rather than about this commit.** Two of the
five drivers are retired; the estimate needs `test/routers` and the two
`test/internal` harnesses to come off before the arithmetic turns, and one copy
(`examples/dashboard/wire.go`) will never come off at all. The number to hold
this work to is therefore the one below it: **one decoder instead of five**, and
the next suite that needs frame-level assertions writes none.

**This finding's *"no new exported symbol"* did not survive implementation, and
that is the correction rather than an override.** It was read off §6's four
`Client` rows. Two things are only visible from inside the work: `Client` had
**no constructor** — the only ledgered function returning one was `Audit`'s
callback parameter — and `WaitFor(fragmentID, func(html string) bool)` cannot
express what the named consumers assert, which is *which* fragments a patch
carried, *which* events a coalesced patch names, how many patches an
unacknowledging client is sent, and how many **bytes** a resync cost. A `Client`
at the ledgered surface would have retired none of the three decoders, which is
the outcome this finding exists to prevent. The four rows are 28, `live/livetest`
moves 9/6 → 37/33, `live` is unmoved, and the argument for each row is in
api-surface.md §6 and §10.

**`examples/dashboard/wire.go` survives on purpose, and the reason is
structural.** `MeasureResync` is in a non-test file — `go run . -resync-cost 200`
is the point of it — and `livetest.Client` takes a `testing.TB` first,
deliberately. Reaching it from an example binary means linking `testing` into
that binary or fabricating a `testing.TB` in `main`, and the argument for a
separate `livetest` package is that neither should be easy. So one `eachField`
copy remains, held by a consumer that cannot use the cure. The file's header now
says so, which is the difference between a residual and a defect.

**`test/routers` stays SPECIFIED**, unclaimed by this wave rather than blocked:
its `go.mod:18-26` objection is fully answered by `livetest` being an exported
package, and its `browser` is the same six methods. It is the smallest of the
three and inherits a proven recipe.

**One deletion fell out rather than being sought:**
`examples/dashboard`'s `EncodeHeartbeatFrame` had no caller before this work
either.

**Sequencing note.** `test/internal/chaos` was the driver's live WIP throughout
this review and `examples/chat/FRICTION.md:60` already sketches a
`NewClient(tb, h, o)` signature. Whoever implements this should read that
FRICTION entry first; it is the consumer report `Client`'s shape was waiting on.

---

## 4. D-4 — SPECIFIED: two descriptor walks where one visitor belongs

`internal/protocol/limits.go:82-119` (`checkListBounds`) and
`internal/protocol/invariants.go:20-55` (`checkEnums`) are **the same traversal**:
both take a `protoreflect.Message`, iterate `m.Descriptor().Fields()`, branch on
`IsList()` / `MessageKind`, recurse into singular submessages guarded by
`m.Has(fd)`, and recurse into list elements by index. ~35 lines each. They
differ only in the leaf action — a cardinality-bound lookup versus an enum-value
check — and in that `checkListBounds` additionally rejects map fields.

They are in the **same package and the same module**, so unlike everything else
in this review the extraction is unobstructed. Both are called from
`ParseInbound` (`inbound.go:121`), so **every inbound frame is walked twice**;
one visitor with two leaf callbacks removes a full traversal from the parse path
as well as the duplicate code.

Report-only for me by scope (`internal/protocol` is the driver's turf).
**Specification:** a single `walkMessage(m protoreflect.Message, visit func(protoreflect.FieldDescriptor, protoreflect.Value) error) error`
in `internal/protocol`, with `checkListBounds` and `checkEnums` reduced to
their leaf predicates. No exported surface moves; `internal/protocol` is
internal, so `tools/apisurface` reads `live 49/49` and `50/50` across it.

---

## 5. D-5, D-8, D-9 — SPECIFIED: the build scripts

Five shell scripts, 1,527 lines total: `ci.sh` (451), `gen.sh` (403),
`test/memory/measure.sh` (469), `test/memory/diag.sh` (158),
`bench/docker/gen-cert.sh` (46, POSIX `sh` — shares no idiom, leave out of any
bash-library extraction).

**D-5 — the module list exists twice and in neither place as a list.** There
are 12 `go.mod` files. `ci.sh` enumerates modules as **six remaining unrolled
near-identical blocks** (`examples/counter` 169, `examples/chat` 176,
`examples/dashboard` 196, `test/routers` 263, `test/sampling` 282,
`test/memory` 302), each five lines of `step` + `cd && go build && go vet &&
go test -race` + `failures+=`, plus two undocumented variants: `tools` (353) has
**no `go build` and no `-race`**, and `docs/guide/_samples` (226-251) is a
26-line superset adding `staticcheck` and a `gofmt` assertion. `gen.sh:100-120`
holds a *second projection* of the same set — the seven modules containing
`.templ` files — and re-derives module roots from it at 318-322.

**The cost of that was a real gap, and the driver closed it mid-review.** Until
`af9057d1`, `bench/apps/*/gotth` — three modules with Ginkgo suites — were run
by nothing in CI. I verified independently that all three pass
(`ok` in 1.089 s / 1.229 s / 6.289 s), so the gap was latent rather than
masking failures. The driver's fix is a **loop** over the three, which is the
right shape and is the argument for doing the same to the other six.

The asymmetry worth noting: `gen.sh:147-158` validates its list against a
filesystem walk, so it cannot silently go stale. **`ci.sh`'s list is held honest
by nothing** — which is exactly how the bench gap survived.

**Specification (owner: build).** One array, validated against
`find . -name go.mod` the way `gen.sh` already validates `templ_sources`, with
per-module opt-outs for the two documented variants. Two constraints on the
implementer: `ci.sh` deliberately does **not** use `set -e` (line 72, it
accumulates into `failures`) where `gen.sh:41` does, so a shared library must
assume neither; and documentation quotes `ci.sh` by step name, by output string
and in one case **by line range** — `docs/qa/checkpoint-3-chaos.md:1168` asserts
"the step landed verbatim, `ci.sh:141-161`" — so a behaviour-preserving
refactor still invalidates a citation. That coupling is why I specified this
rather than extracting it.

**D-8 — a latent infinite loop in the duplicated copy.** The "walk up to the
enclosing `go.mod`" loop appears twice in `gen.sh`, and the copies are not the
same:

```bash
# gen.sh:174-176 — correct
while [ ! -f "${mod_dir}/go.mod" ] && [ "${mod_dir}" != "/" ]; do
# gen.sh:320 — sentinel dropped
while [ ! -f "${mod_dir}/go.mod" ]; do mod_dir="$(dirname "${mod_dir}")"; done
```

`dirname /` returns `/` forever. Unreachable today because 174-188 runs first
and exits 1, but it is a trap waiting for the order to change — and it exists
only because the loop was copied rather than shared.

**D-9 — helpers, low priority.** `ci.sh:86-88` `step()` and
`measure.sh:154` `say()` are the same `printf '\n\033[1m==> %s\033[0m\n'`
under two names, and `ci.sh:434` inlines it a third time; `gen.sh` uses the same
`==>` convention as ten un-coloured `echo`s. Tool-presence assertions are
hand-rolled six ways, and `gofmt` is asserted two different ways within `ci.sh`
alone (113-117 and 236-239). `measure.sh` and `diag.sh` are ~70 % clones at the
top (`usage` heredoc, `while`/`case` arg loop, `IMAGE` default) and are the
natural first `lib.sh`. Two live divergences are worth fixing regardless of any
extraction: `diag.sh` asserts **no** tools despite requiring `docker`, and
`measure.sh` mounts **no module cache**, so its build step re-downloads
dependencies on every run where `diag.sh` does not.

Also observed: `ci.sh` prints three `docker run` recipes (347, 384, 426) that
**disagree about the working directory** — 347 and 426 require `$PWD` = repo
root, 384 requires `$PWD` = `candace/pkg/gotth/`. The workflow is uniform
(`-w /workspace/candace/pkg/gotth`, three jobs), so 384 contradicts CI and its own
sibling. A stray root-owned empty `gotth-live/gotth-live/{docs/guide/_samples,tools}`
tree in the checkout is the fossil of someone running one of them.

---

## 6. D-6, D-7 — SPECIFIED: the bench applications

**D-6 — `ready.js`, three byte-identical tracked copies with no keeper.**

```
551140696133a954915d57fb477c9419  bench/apps/chat/gotth/bench/ready.js
551140696133a954915d57fb477c9419  bench/apps/counter/gotth/bench/ready.js
551140696133a954915d57fb477c9419  bench/apps/dashboard/gotth/bench/ready.js
```

88 lines each, all three **git-tracked**, embedded via `//go:embed bench/ready.js`.
`go:embed` cannot reference a path outside its own package directory, so a
single shared file is genuinely impossible — a copy step is the only mechanism.

**The repository already has that mechanism and did not point it at this file.**
`bench/harness/shim.js` faces the identical constraint and is handled properly:
copies are gitignored (`bench/.gitignore:16-18`), generated by
`bench/scripts/sync-shim.mjs`, and SHA-256 verified by `npm run verify:shim`
(`bench/package.json:20`). `ready.js` got none of it — three tracked copies, no
sync, no verify, nothing that fails when they drift.

**Specification (owner: BENCH).** A `sync-ready.mjs` mirroring `sync-shim.mjs`,
one source at `bench/harness/ready.js`, the three copies gitignored, and a
`verify:ready` target joined to `verify:shim`. This is the one case in the
review where the *shape* of the fix is already written and only needs aiming.

**D-7 — `LoadShim` / `serveScript` / `serveCSS`, low priority.** ~25 near-identical
lines in each of `bench/apps/{counter,chat,dashboard}/gotth/bench.go`, differing
only in an error prefix (`"counter-bench:"` vs `"chat-gotth:"`) and comment
placement; `serveCSS` is byte-identical across all three. Three separate
modules, so sharing needs a fourth module that all three require — which is the
quarantine the bench `go.mod`s exist to hold. **Cost exceeds benefit at 25
lines; recorded so the next reader does not re-derive it.** Revisit only if a
fourth bench app lands.

---

## 7. D-10 — SPECIFIED: what D-1 deliberately did not fix

All six `allowedOrigins` copies, including the ones I corrected, mishandle the
`":PORT"` form. `net.SplitHostPort(":8080")` yields `host == ""`, so
`origins[0]` becomes the meaningless `"http://:8080"`, and the `case "127.0.0.1", ""`
arm adds `localhost` but **never `127.0.0.1`**. A user running `-addr :8080` and
browsing to `http://127.0.0.1:8080` is refused.

I left it. D-1's whole argument is that a seventh variant is worse than a shared
sixfold bug, and fixing this in three copies would have re-created exactly the
divergence I was closing. **Specification:** one change, applied to all six
copies in one commit, adding `127.0.0.1` to the empty-host arm — or, better,
folded into whatever resolves D-3, since a `livetest`-adjacent helper for
"derive a dev allowlist from a listen address" would have ≥ 6 call sites and is
the only member of this review that clears §1.4 on its own.

---

## 8. Ruled DELIBERATE — nine duplications that should stay

| # | Duplication | Why it stays |
|---|---|---|
| **R-1** | `test/routers` hand-rolls a `protowire` codec it could import from `internal/` | `test/routers/go.mod:18-26` argues it at length and is right: the suite's subject is *what a consumer can do from outside*, and proving it with the library's private codec proves it with a tool no reader can pick up. Note the module **could** import `internal/` — Go's internal rule is path-prefix based, and `test/sampling` does exactly that — so this is a choice, correctly made and correctly written down. |
| **R-2** | `examples/{chat,dashboard}` hand-roll the same codec | Same argument plus a stronger one: each example's `go.mod` says it *measures like a consumer*, and importing `internal/` would falsify the dependency measurement the module exists to produce. |
| **R-3** | Three `Kind()`/`IsList()` switches in `internal/clientcodec` (`schema.go:204-241`, `generator_test.go:252-269`, `golden.go:290-311`) | `generator_test.go:250-251` states it: *"derived independently of the generator so the spec is a second opinion rather than an echo."* Deduplicating a test against the code it tests deletes the test. Same for the `generator_test.go:86-104` / `schema.go:140-167` walk pair and `conformance_test.go:137-160` / `limits.go:82-119`. |
| **R-4** | `bench/harness/shim.js` copied into three `next/public/bench/` trees | Managed duplication: gitignored, generated by `sync-shim.mjs`, SHA-256 verified by `npm run verify:shim`. equivalence-spec §2.0 *requires* one byte-identical file served by both stacks. This is the model D-6 should copy. |
| **R-5** | `internal/refine/refine.go` is a byte copy of the research plugin's `refine.go` | The generated header states the trade: the research module is a vanity path (`example.invalid/research/protorefine`) with no resolver behind it, so importing it would put an unfetchable module in every consumer's graph. |
| **R-6** | The refinement-predicate grammar parsed twice — three regexes in `internal/clientcodec/schema.go:102-106` vs a full `go/ast` type-checker in the research plugin | Not dedupable at any reasonable cost, for R-5's reason one level up: the authoritative parser lives outside `candace/pkg/gotth/` in an unfetchable module. Five lines of regex is the cheapest available answer to a grammar of three builtins. |
| **R-7** | `client/test/golden.json` read by both a Go and a JS suite | Not duplication — one generated artefact from `internal/clientcodec/golden.go:116-264`, single-sourced, with both consumers documenting the split. The only restatement is a 4-line unknown-tag byte sequence in `codec.test.mjs:102-107`, which exists for a property the fixture cannot express. |
| **R-8** | Every doc sample re-scaffolds its own server (`Config`, origins, `http.Handle` pair, `Script` mount) | Pedagogically load-bearing. `docs/guide/_samples/quickstart/main.go` is 59 lines and self-contained; a reader copying it must get a working server, not an import of a helper that hides the four decisions the page is teaching. **Judged per-lead as the brief asked: repetition here is the product.** |
| **R-9** | `client/test/bundle.test.mjs:38-44` builds a small `document` stub the harness also builds | ~7 lines, and `bundle.test.mjs:1-18` documents why it cannot reuse `harness.mjs`: it tests the *minified shipped artefact* through `createRequire`, not the sources. |

Two leads from the brief were **investigated and found empty**, recorded so
nobody re-opens them: there is **no** DOM-shim duplication between
`bench/harness/` and `client/test/` (nothing under `bench/` runs
`client/runtime.js` under node at all — `bench/harness/shim.js` is a
*browser-side measurement* shim and shares zero lines with the node fake-DOM in
`client/test/dom.mjs`); and there is **no** paint-predicate or codec-fixture
duplication across that boundary either.

---

## 9. Incidental — one flake, outside this lens

`examples/dashboard/wire_test.go:1316` failed once in eight runs during
verification (`Expect(found).NotTo(BeNil(), "patch %d appears in no provenance
row")`), then passed 3/3 and 4/4 on re-run. The spec never calls
`allowedOrigins`, so it is unrelated to `af485014`; it reads the provenance log
after awaiting a patch off the wire, and the log row is written asynchronously
relative to the frame. It is **not** listed in `docs/qa/ci-intermittents.md`.
Flagged for the dashboard owner, not diagnosed here.

**CLOSED — `7ff41b56`.** The mechanism above was right and the *width* was the
part nobody had; the width is what decides the fix. `Actor.emitPatch` writes the
socket before the causal row deliberately — the failed-send branch emits no row
at all, because a frame that never reached the transport must not be logged as
delivered — so a patch off the wire is not a receipt for its own row, and the
spec was racing a ~50 µs tail. Swept by injecting a sleep between the two
writes: 0/20 red at no stall, 19/20 at 50 µs, 20/20 at 100 µs and above. It also
reproduced naturally, 1 in 40, only under cgroup throttling (`--cpus=0.2` with
`GOMAXPROCS` forced — Go 1.26 derives `GOMAXPROCS` from the cgroup quota, so a
bare `--cpus=1` *reduces* concurrency instead of stressing it), and not at all
in 160 unthrottled runs on this host.

A **test** defect, not a library one, and the row that distinguishes them is the
mutation: with no stall at all, deleting the provenance call makes the *new*
spec go red, so the library's contract is intact and the spec was asserting an
ordering the library never offered. Nothing in `internal/` changed. The fix
carries no wall-clock term — one session's transitions are applied in sequence
by a single actor goroutine, so a *second* patch on the socket proves the first
patch's row was already written, and the spec now awaits the later patch before
reading the earlier one's row. Evidence, the tallies, and the 1 ms control that
shows the defect was this one spec's rather than the suite's are in
`docs/qa/ci-intermittents.md`, which now lists it.

---

## 10. What I changed

| Commit | Modules touched | Verification |
|---|---|---|
| `af485014` | `examples/counter`, `examples/chat`, `examples/dashboard` | `go build`, `go vet`, `gofmt -l`, `go test -race -count=1` green in all three, in `dis-gotth-live:latest`; mutation-checked |

Nothing else was modified. `go.sum` was touched by a `go mod download all`
during investigation and **reverted** — the extra hashes were transitive
tool dependencies and had no business in the ledger.
