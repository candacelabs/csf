# Module-init increment — L9-1 review

| | |
|---|---|
| **Reviewer** | L9-1 (Principal Engineer) |
| **Date** | 2026-08-04 |
| **Reviewed** | `4bd45b0e`…`920aa61e` (nine commits), plus `d2902ac0` in `research/protobuf-refinement-types/` |
| **Reviewed against** | [review checklist](../review-checklist.md) §0–§10 |
| **Prior design note** | The Phase 0 package, merged at `174559f4` **before** the first code commit |
| **Verdict** | **APPROVE-WITH-CONDITIONS** — checklist vocabulary: **Merge with nits** |

```
Verdict: Merge with nits

Sections walked: 0, 1, 2 (N/A), 3, 4 (N/A), 5 (N/A), 6 (N/A), 7 (N/A), 8, 9, 10
Diff size (counted): ~1,898 lines   Generated, excluded: 3,943
Design note: Phase 0 package, merged 174559f4 — prior, and it predicted this layout
Client runtime: n/a — does not exist yet.  Size gate: n/a
QA-1: explicitly pending (§0.5) — see C-16    QA-2: n/a for this increment

Blocking:
  - none

Non-blocking: five conditions C-15…C-19, plus nits, below.
```

**Scope note.** The worktree carries uncommitted `go.mod`/`go.sum` edits from
DEV-1's parallel server-core work (adding templ, OTel and `coder/websocket`).
They are **out of scope** and are not reviewed here. Every measurement below was
taken against a pristine `git archive` export of `920aa61e` inside a scratch
`docker run` of `dis-gotth-live:latest`, precisely so an in-flight edit could
not contaminate it — an earlier measurement round was contaminated by exactly
that and was discarded.

---

## 1. Gate 0 and the automatic-return list

No automatic-return item fired. Walking the six explicitly, because this is the
first code increment and the list is what it is for:

1. **Purity** — n/a; no reducer or render code exists yet. The godoc commits to
   it in `internal/session` and `internal/render`, which is the right place.
2. **Provenance** — the schema *strengthens* it. `Origin.source` carries
   `len(this) > 0`, which makes an origin-less patch unconstructable rather than
   merely rejected; `Snapshot.superseded_from_seq`/`superseded_through_seq` (10,
   11) are present as B-7 required.
3. **Budget** — no client runtime, so no budget to breach.
4. **Diff > 400 with no prior design note** — ~1,898 counted lines, and the
   design note is prior and merged. §1.1's own example ("a 700-line PR with a
   merged design note that predicted it is fine") is the governing case, and
   this is a stronger form of it: RFC §14.2 predicted the package layout,
   protocol.md §3 predicted the schema field-for-field, and §5.4 predicted the
   plugin parameter down to "roughly four lines plus a test".
5. **JSON side channel** — none, and it is now asserted rather than promised:
   `internal/arch` walks `go list -deps` for `internal/protocol` and
   `internal/wsx` against `encoding/json`.
6. **Multi-node** — nothing.

---

## 2. What I verified by running it, not by reading it

All inside a scratch `docker run` of `dis-gotth-live:latest` against the
pristine export.

| Check | Result |
|---|---|
| `go build ./...`, `go vet ./...` | clean |
| `gofmt -l .` | empty |
| `go test ./...` | `internal/arch` ok, `internal/protocol` ok |
| `go test -race ./...` | clean, and the specs do execute under it |
| **Architecture test actually fails when violated** | Added `import _ "testing"` to package `live` in a copy: `[FAIL] the public package live does not link the testing machinery` at `imports_test.go:49`. The C-12 condition is a real assertion, not a tautology. |
| **`gen.sh` reproducibility** | Re-ran over the committed output: **byte-identical, no diff.** Ran it a **second** time: still no diff. Tests still green on the regenerated tree. The determinism claim in the header is true. |
| **Research plugin suites** | `expr`, `gen`, `example` all ok; `go vet` clean. |
| **Node in exactly one image** | `dis-gotth-live`: no node, no npm; protoc 35.1, protoc-gen-go, templ present; `refinec` and `protoc-gen-gorefine` correctly absent. `dis-gotth-live-bench`: node v24.19.0, npm 11.17.0. FR-74's quarantine is structural, as the Dockerfile comment claims. |

That reproducibility result is the one I most wanted and least expected to get
first time. It is also, per **C-15** below, currently unenforced.

---

## 3. The walk

### §1 Scope and size — pass

`§1.4` is clean: no new interface, no generic parameter, no options struct, no
callback, no registry. `§1.6` is clean and better than clean — RFC §3.5 now
records that FR-2 was *amended* rather than contradicted, and the isolation
property ships as an assertion (**C-1**, discharged; see §4).

`internal/cmd/gen-clientcodec` is a `main` that prints and exits 2. I looked at
it under §1.4 and let it stand: protocol.md §10.2 names it as the mechanism, its
godoc is the best specification of the client codec that exists anywhere in the
package, and a named placeholder is cheaper than the same reasoning arriving as
a PR comment in Phase 4.

### §3 Protocol — pass, one arithmetic correction

I spot-checked the `.proto` against protocol.md's catalogue field by field.

- **Field numbers** match §3.1 exactly: `event` 3, `ack` 4, `heartbeat` 5,
  `client_telemetry` 6, `resync_request` 7, `patch` 8, `snapshot` 9, `error` 10.
  The `.proto` groups the oneof by direction where §3.1 lists it by number;
  same numbers, and the `.proto`'s grouping is the more readable of the two.
- **`protocol_version`** is `this > 0` with no upper bound (B-6), and the
  comment carries the layering argument — *refinements reject what cannot be
  parsed; H-2 rejects what cannot be served*.
- **`Snapshot` 10/11** are present, deliberately **unpredicated**, with the
  reason stated inline: the all-zero case is legitimate, so H-13 carries it.
  That is the correct call and it is explained where the next reader will be.
- **Predicates that carry an invariant** (§3.3) are all present: identifiers
  length-capped and shape-checked, sequence numbers `> 0`, `html <= 1048576`,
  `Error.message <= 512`, telemetry durations bounded. The fields that carry
  *no* predicate — `Origin.event_id`/`client_ref`, `Error.event_id`/`client_ref`,
  every enum, every `repeated` — are exactly the set §2's plugin-limit table says
  cannot be refined, each with a named H-invariant. The schema is shaped by the
  type system rather than annotated after it, as §2 claims.
- **Refinement boundary** — `schema_test.go` proves the property that matters:
  a predicate violation is rejected identically whether constructed in Go or
  decoded from hostile bytes.

**One number does not reproduce.** protocol.md §3.1 and the header comment of
the normative `frame.proto` both state the envelope overhead is **21 bytes**.
The components they list sum to **22–23**: `session_id` 2+16 = 18,
`protocol_version` 2, payload tag+len 2–3. protocol.md §9's own provenance table
uses 22–23 and is internally consistent with it (18+2+2+8+28 = 58;
18+2+3+20+32 = 75). So 21 is wrong in two places, one of them the normative
schema file. → **C-17**.

### §8 Testing — pass, with the conformance mechanism owed

House conventions are followed exactly: Ginkgo v2 + Gomega for both suites,
`RunSpecs` bootstraps present, no arbitrary deviation. `gomock` is pinned
through a `tool` directive rather than a phantom import — the right call, and
the comment explaining why `go mod tidy` would otherwise prune it is the kind of
thing that stops a future PR from "cleaning it up".

`schema_test.go` is honest about being partial: its header says the descriptor
walk, the hand-checked invariants and the hostile corpus arrive with
`ParseInbound`. Accepted — vectors have nothing to run against until a decoder
exists — but the mechanism that stops a new frame kind from skipping the
boundary (§3.4, protocol.md §5.2) does not exist yet, and it is not optional.
→ **C-16**.

### §9 Docs — pass, and the godoc is at the bar

I read all seven doc.go files against the stdlib bar and would not send any of
them back. Three specifics worth recording so they survive:

- `live/doc.go`'s delivery-semantics section states the consequence most
  libraries leave implicit — *"an effect may have executed even though the user
  never saw its result"* — and then tells the caller what to do about it. That
  is the sentence an integrator needs and never gets.
- `internal/session/doc.go` explains *why* memory under a slow client is
  proportional to fragment count rather than pending patches. A godoc that
  explains why a bound holds is a godoc that stops someone removing the bound.
- `internal/wsx/doc.go` states the handshake ordering as the security property
  rather than as an implementation detail, which is checklist §5.2 written where
  the person about to reorder it will read it.

Concurrency contracts are present on the package docs (§9.2's substance). No
exported symbols exist yet, so §9.2's per-symbol obligation lands with the
server core.

### §10 Dependency review — pass; the measurements reproduce

`go.mod` adds `google.golang.org/protobuf` (Tier 1, approved in the Phase 0
ledger §1.2), plus ginkgo/gomega/mock (Tier 2, mandated). §10.6: `go.sum` is
generated — I regenerated the tree and it is consistent.

dependencies.md §5.1's headline numbers **reproduce**: consumer `go list -m all`
= 18, gaining 16 beyond itself and gotth-live; the empty-import binary floor is
1,821,519 B against the ledger's 1,821,535 B (the 16-byte difference is the
consumer module path length, not a discrepancy). Obligation 6 — the Tier 1 `go`
directives — is discharged. Obligation 2 is explicitly *not* discharged and says
so, which is the right way to leave an obligation open.

---

## 4. Conditions C-1…C-14 — closure walk

I walked each to the text, as in cycle 2. **All twelve DEV-1 conditions are
genuinely closed.** C-5 was closed by QA-2 at `eb6cf6dd`; C-6 remains PM-1's.

| # | Closed | Note |
|---|---|---|
| C-1 | ✓ | RFC §3.5 is now past-tense against the amended FR-2, and the heading says so. The changelog's claim is now true. |
| C-2 | ✓ | §7.1 restated as 64 B/slot in two halves × 16 = 1,024 B, with a sentence naming what cycle 1 undercounted. §7.1, §6.2 and instrumentation §3.3 now agree. |
| C-3 | ✓ | **Judgement call, and the right one.** C-3 offered two remedies; DEV-1 took a third — withdraw the `crypto/tls` line from §6.2 altogether. That is *better* than either option I named, because the reason is not bookkeeping: equivalence-spec §3.6 now requires the in-process figure to be independently measured, and a number derived from a composition budget cannot test a composition budget. Recording what the table's own method *would* have given (78,416 B) keeps the arithmetic auditable without publishing it as a result. Accepted. |
| C-4 | ✓ | Aligned on `live.AnyOrigin`, with a parenthetical recording the cut helper. |
| C-7 | ✓ | **Judgement call, and the right one.** I named `WithPerSessionMetrics` as "an exported symbol in no ledger"; cutting it rather than ledgering it is the stronger reading, because the knob contradicted the bullet directly above it — a per-session label *is* a causal ID as a label (§4.6). Re-adding it now needs an api-surface row and a named consumer. |
| C-8 | ✓ | All four signals in §2.2 — **and each also gets a §5.1 confirmation row**, which C-8 offered as "or state why it needs none". Two are exactly checkable from a decoded capture; the other two name what they lean on. The condition asked for a list; the answer supplied the mechanism, which is what stops the list rotting. |
| C-9 | ✓ | **Judgement call, and the right one.** Splitting the bullet is exactly the reconciliation asked for: `event`/`fragment` are registration-bounded and the startup warning covers them; `source` is metric-bounded at 64 with an overflow counter, and §2.1 now says outright that this is the weaker guarantee and surfaces at runtime because the startup warning cannot see it. protocol.md was correct and this document says so. |
| C-10 | ✓ | All three body sites plus api-surface A5's parenthetical. |
| C-11 | ✓ | Added as §5 obligation 6, with the episode that produced it stated. |
| C-12 | ✓ | All three: RFC §14.2 amended **in this PR**; `internal/arch` asserts `live` does not transitively import `testing` — and I confirmed by mutation that it fails when violated; the cap of two is recorded in both RFC §14.2 and api-surface §0.1, so a third package is refused by whichever document the author opens. |
| C-13 | ✓ (DEV-1 half) | api-surface §7.1 rewritten as a reconciliation and A2 struck. Records PM-1's FR-56 amendment in PRD v0.3; the PM-1 half is theirs to confirm. |
| C-14 | ✓ | ADR §7 makes X3's derived status binding with three consequences. Arithmetic checks: 4,096 + 2,000 + 8,192 + 500 = 14,788 against 16,384 = 9.7 % headroom. O2 correctly named as the live risk. |

`nit:` X3 maps to three whole §6.2 lines plus **half** of a fourth (the "2 ×
runtime `g`" line, of which X3 takes ≈500). The text says "its runtime `g`", so
it is unambiguous; "exactly four lines" is a shade generous. Fix only if you are
in the file.

---

## 5. The research-plugin diff (`d2902ac0`)

Minimal, additive, tested, and **default behaviour is unchanged** — verified by
the diff and by running the plugin's own suites on the committed tree.

- `DefaultRuntimeImport` preserves the old constant's value, and
  `TestRuntimeImportDefaults` asserts generated code still names it when nothing
  says otherwise. That test is the one that matters and it is present.
- `TestRuntimeImportOverride` asserts the *only* thing that moves is the import
  path — the emitted `*refine.Error` is unchanged. That is the claim the commit
  message makes ("packaging only"), turned into a test.
- Rejection cases are tested, including that a rejected value leaves the current
  setting untouched.
- 13 + 28 + 73 lines against protocol.md §5.4's prediction of "roughly four
  lines plus a test". The extra is validation and its tests, which is the right
  direction to overshoot in.

`nit:` `runtimePackage` is now process-global mutable state, so `gen.Generate`
is order-dependent on `SetRuntimeImport`. Harmless in a one-shot protoc plugin,
and the tests reset it via `t.Cleanup`; an options struct would be cleaner if
that file is ever reworked. `nit:` validation is a character blocklist rather
than a positive import-path check (`module.CheckImportPath`), so `..` and
`foo//bar` pass; proportionate here, since the value comes from `gen.sh` in the
same repo, but it is a blocklist and blocklists are the weaker shape.

---

## 6. Ruling on D7 — dependency ledger §5.1 / §7

**The operator's mandate — Ginkgo v2 + Gomega for behaviour specs, `gomock` for
interface mocks, table-driven stdlib only where clearly clearer — is BINDING and
is not in question here. Only packaging is.**

### 6.1 The measurement

Taken in the library dev container, Go 1.26.5, against the pristine `920aa61e`
export. gotth-live was **published through a real (file://) module proxy at a
version**, so nothing below is an artifact of a local `replace` directive; a
consumer is a module requiring gotth-live and importing `live`.

| Configuration | consumer `go list -m all` | consumer `go.sum` | consumer binary |
|---|---:|---:|---:|
| **as shipped** | **18** (+16 beyond itself and gotth-live) | 18 lines / 17 modules | 1,821,519 B |
| **option (b)** — suites *and* the `tool` directive in a sibling module | **3** (+1) | 3 lines / 2 modules | 1,821,519 B |
| **delta** | **−15 modules** | **−15 lines** | **0 bytes** |

Four findings decide this, and three of them are things I would have got wrong
from memory:

1. **The +16 does reach `go list -m all`.** Module-graph pruning does not save
   us. A consumer's direct requirement has its whole `go.mod` loaded, so every
   module gotth-live requires for its own tests enters the consumer's build
   list. Confirmed identically by the `replace` route and the proxy route.
2. **It also reaches `go.sum`** — 16 extra `/go.mod` hash lines. This is the
   finding a `replace`-based measurement *misses*: with a local replacement the
   consumer's `go.sum` is empty, and reading that as "zero go.sum impact" would
   have been wrong. Under a real `go get` the lines are there.
3. **It reaches nothing else.** The consumer builds with an **empty module cache
   and `GOPROXY=off`** — zero modules materialised. Nothing is fetched, verified
   or linked, and the binaries are byte-identical across the two configurations.
   "None of which is linked into anything they build" is measured, not asserted.
4. **Option (c), build tags, does not work — measured.** With every `_test.go`
   behind `//go:build conformance`, `go mod tidy` **still** records ginkgo and
   gomega as *direct* requirements, the consumer graph stays at 18, and it
   additionally acquires `stretchr/testify`. `go mod tidy` considers all build
   configurations. Meanwhile `go test ./...` then reports "no test files" for
   every package — the suites silently stop running by default. Option (c) costs
   G11's clean-clone property and buys **zero** module-graph reduction.

One more measured constraint on option (b): the `tool go.uber.org/mock/mockgen`
directive **on its own** holds 4 of the 15 modules (`mock`, `x/mod`, `x/sync`,
`x/tools`). Moving the suites without moving the tool directive gets a consumer
to 7, not 3. Any future implementation of (b) must move both.

### 6.2 Ruling

**Option (a): accept for v0.1, with disclosure, and with option (b)
pre-registered against a trigger fixed now.**

Reasoning, in order of weight:

1. **Option (c) is off the table on the measurement.** It is not a cheaper (b);
   it is the cost of (b) with none of the benefit.
2. **The measured cost of (a) is 15 graph rows and 15 `go.sum` lines, and
   nothing else.** Zero bytes linked, zero modules fetched, zero entries added
   to the consumer's own `go.mod`. That is a supply-chain-review and
   dependency-dashboard cost, which is real, and not a build or runtime cost,
   which is what NFR-9 is mostly about.
3. **The cost of (b) *today* is a property, not a row.** The suites live at
   `internal/arch/imports_test.go` and `internal/protocol/schema_test.go`.
   Moving them to a sibling module means `go test ./...` at the library root
   runs nothing — the same silent-no-tests failure that disqualifies (c),
   arriving by a different door, and against G11's own headline property. I am
   not trading a checked property for a cosmetic one at Phase 1, when the
   checked property is the entire reason `internal/arch` exists.
4. **(b) stays cheap later.** It is a directory move plus a `go.mod`. Nothing in
   this increment forecloses it, which is why deciding now costs nothing and
   deciding later costs nothing either.

**Disclosure obligations, binding on the ledger** (dependencies.md §2 and §5.1):
record the `go.sum` reach alongside `go list -m all`; record that the modules are
neither fetched nor linked, with the empty-cache/`GOPROXY=off` result as the
evidence; and record that option (c) was measured and rejected, so nobody
re-proposes build tags on the assumption they prune the graph. → **C-19**,
**C-18**.

**Pre-registered trigger for option (b)** — fixed now, so the choice is not made
after seeing a number:

1. **`playwright-go` must never enter the library module's `go.mod`.** The PR
   that adds the FR-25/FR-26 DOM conformance suite **creates
   `gotth-live/conformance/` with its own `go.mod`** and puts playwright there.
   The ledger currently believes a `//go:build browser` tag keeps it out of the
   graph; measured, it does not. This is the near-term action, and it also gives
   option (b) a first tenant that has no reason to live in the library module.
2. **Ceiling.** If a consumer's unlinked module count would exceed **20**, the
   remaining suites move to `gotth-live/conformance/` in the PR that would
   breach it — with the `tool` directive.
3. **Re-decide before v0.1 is published**, which is the deadline the ledger
   itself set for D7. Re-measure and bring me the number; if it is still 16 and
   still unlinked, (a) stands.

**D7 is closed.** Record it as closed in dependencies.md §7 with this ruling
cited, not carried.

---

## 7. Equivalence-spec freeze sign-off

**Signed off. L9-1's row in §12's sign-off table is GRANTED.**

I read v0.2 against RFC §6.1.1 and against C-3's measured-not-derived rule:

- **§3.6 carries RFC §6.1.1 verbatim.** I diffed the two block quotes
  mechanically: 11 lines, **byte-identical**. Same proxy image, separate
  container, same host, proxy excluded from `M(x)`, disqualifying **in either
  direction**, in-process TLS a labelled secondary and not a comparison row. C-5
  asked for a transplant needing no editing, and that is what happened.
- **C-3's rule is carried, and carried harder than I wrote it.** §3.6 requires
  the in-process figure to come from re-running the same procedure with the
  boundary moved, states that it may **not** be produced by adding an estimated
  `crypto/tls` line to RFC §6.2 and re-applying that table's GC method, and gives
  the reason — *"a secondary derived that way would confirm the estimate it
  exists to test."* If the run is not performed the row reads "not measured"; it
  is never inferred. RFC §6.2 withdrew the line on the same reasoning, so the two
  documents now agree by construction rather than by cross-reference.
- **The rule is asserted, not trusted.** Before any D3 cell is recorded the
  harness verifies no TLS listener in either measured container and equal proxy
  image digests, and writes both into the run manifest. New threats **T-21** and
  **T-22** name the two failure modes and their responses. A fairness rule
  nobody checks is a fairness rule that silently stops being true, and this one
  is checked.
- **§12's own discipline is respected.** The amendment log records A-1 with
  "measurement taken under old text: **No**", and the checkable form of that
  claim (`bench/data/` holds no run ids). The amendment moves a definition
  *before* any number exists, and it makes the contract harder for gotth-live,
  not easier — which is the only direction a pre-freeze amendment may move.

**Two residuals, neither mine and neither blocking my sign-off:** the freeze
itself still needs PM-1's row, and **C-6 remains open** — the PRD is still the
stale authority on a memory number this review approved, and Appendix A.4
correctly names it as the last place a reader finds the old TLS-in-process
framing presented as authoritative. C-6 gates the *recording* of the Phase 0
gate, as cycle 2 said; it does not gate this sign-off.

---

## 8. New conditions

Each is an obligation with an owner and a phase. **None blocks this merge.**

| # | Condition | Owner | Phase |
|---|---|---|---|
| **C-15** | **`gen.sh`'s reproducibility is a claim that nothing enforces.** I verified byte-identical regeneration twice, by hand, today; there is no CI in this module, so nothing keeps it true tomorrow, and protocol.md §10.2 already refers to "CI's byte-reproducibility check (FR-7)" as though it exists. Land a CI job running `go build`, `go vet`, `gofmt -l`, `go test -race ./...`, and the FR-7 check — re-run `gen.sh` in the library image and assert an empty `git diff`. Same principle as C-12: the argument becomes a test. | DEV-1 | Phase 1, before Checkpoint 1 |
| **C-16** | **The `ParseInbound` PR lands protocol.md §5.2's descriptor walk in the same PR** — the protoreflect walk over the `payload` oneof asserting every member has both a `ParseInbound` case and a generated `Refine*`, plus H-1's and H-4's descriptor-walk meta-tests. `schema_test.go` is deliberately partial and says so; until that walk exists, nothing structurally stops a new frame kind from skipping the refinement boundary, which is the whole of checklist §3.4. QA-1 sign-off (§0.5, §8.6) attaches to that PR, not this one. | DEV-1 + QA-1 | Phase 1 |
| **C-17** | **The envelope overhead is stated as 21 bytes in two places and its own components sum to 22–23.** protocol.md §3.1 and the header comment of `proto/gotthlive/v1/frame.proto` — the normative schema — both say 21; `session_id` 18 + `protocol_version` 2 + payload tag/len 2–3 is 22–23, and protocol.md §9's provenance table already uses 22–23 (58 and 75 both reproduce only with it). Correct both, and check §9 still reconciles. | DEV-1 | Phase 1 |
| **C-18** | **dependencies.md §2's playwright mitigation is wrong about the module graph.** A `//go:build browser` tag keeps the suite from *running* on a clean clone — that part is true and I verified it — but it does **not** keep the module out of `go.mod` or out of a consumer's `go list -m all`; `go mod tidy` considers all build configurations. Correct the text, and land `playwright-go` in `gotth-live/conformance/` per the D7 ruling rather than in the library module. | DEV-1 | with the FR-25/FR-26 suite |
| **C-19** | **Extend §5.1's disclosure to what was actually measured.** Add the `go.sum` reach (16 extra `/go.mod` lines under a real `go get`; zero under a local `replace`, which is why a replace-based measurement understates it); record that none of the 16 is fetched or linked, citing the empty-cache/`GOPROXY=off` build and the byte-identical binaries; record that option (c) was measured and rejected; and note that the "+1" row is true only while nothing reachable from `live` imports protobuf — measured, it becomes **+3** once `live` reaches `internal/protocol/gotthlivepb`, which is the next increment. | DEV-1 | Phase 1 |

**Non-blocking nits** (fix if you are in the file): X3's "exactly four lines" is
three lines plus half of a fourth (§4); `runtimePackage` is process-global
mutable state and `SetRuntimeImport` validates by blocklist (§5); the `.proto`
orders the oneof by direction where protocol.md §3.1 orders it by number — the
`.proto`'s is the better of the two, so change the doc if you change either.

---

## 9. Disposition

**APPROVE-WITH-CONDITIONS.** Merge.

The functional core is uncompromised, the provenance chain is stronger on the
wire than it was on paper, and the two structural claims this increment makes —
that `live` never links `testing`, and that the core never reaches the
transport — ship as assertions over the real build graph rather than as
intentions. The generated code regenerates byte-identically from the committed
script, twice. The one dependency-graph question worth having was raised by
DEV-1 rather than discovered by me, measured rather than estimated, and flagged
upward rather than decided unilaterally; that is the behaviour the ledger exists
to produce, and it is worth saying so.

D7 is ruled and closed. The equivalence spec has my freeze sign-off.

— L9-1, 2026-08-04

---

## Addendum — 2026-08-04: where the embedded client artifact lives

A ruling requested out of band on DEV-2's completed client runtime, so DEV-1 can
wire `live.Script()` as the last checkpoint-1 item. **This is a ruling on the
layout question only; the client runtime itself is not reviewed here and gets
its §7 pass with the checkpoint-1 package.**

### The premise is correct

RFC §14.2 says the shipped artifact lives in `client/` and is `go:embed`'d by
`live`. That is not implementable, and the reason is worth stating exactly so it
is not re-litigated: an `//go:embed` pattern may not contain `..` and may not
name a file outside its own package directory. `live` cannot reach `client/`.

Three facts about the tree as it stands, which is what makes this cheap:
`client/` holds `runtime.js`, the generated `codec.gen.js`, `predicates.manifest.txt`,
`SIZE.md`, the built `gotth-live.min.js`, and a node test suite under
`client/test/`; **there is no Go file anywhere under `client/`**; and `tools/`
is already its own module, so the minifier is outside the library's graph.

### Ruling: (b), with one correction — one copy of the artifact, not two

**The shipped artifact lives at `live/clientjs/gotth-live.min.js`.** The `tools/`
esbuild module **emits it there directly**. `client/` remains the source-of-truth
directory for the runtime *source* and its node tests, and remains a non-Go
directory.

The correction to (b) as proposed matters. (b) offered to keep the artifact in
`client/` and copy it into `live/`, with `-check` extended to assert the two
copies are identical. Do not create the second copy. A two-copy equality
invariant is a thing that can drift, and the answer to it is a checker somebody
has to keep running — where emitting to one location means there is nothing to
check. `-check` then does the job it has to do anyway: rebuild `client/`'s
sources and assert the result matches the committed
`live/clientjs/gotth-live.min.js`. That is the FR-7 staleness check, unchanged
in substance, now guarding one artifact instead of an equality between two.
This is the same principle as RFC §6.1.2 (removing the incentive beats
regulating it) and C-14 (a derived number stays derived): **an invariant you do
not create is better than one you check.**

Embed by **exact filename**, not a glob:

```go
//go:embed clientjs/gotth-live.min.js
var clientScript []byte
```

`clientjs/*` would silently adopt any file that later lands in that directory
into the module's shipped payload. Naming the file makes the embedded set exact
and makes adding to it a visible edit.

### Why not (a)

**(a) is refused.** It spends the C-12 cap on packaging trivia. `live/livetest`
was admitted on a *measured* cost — `testing`, `flag`, `regexp`,
`runtime/pprof` and `runtime/trace` in every consumer's production binary — and
C-12's third condition exists precisely so a third package is refused when the
grounds are convenience. These are convenience grounds: no consumer would ever
import `client`, `live` would import it so the bytes travel identically either
way, and the package would exist for no reason except to give `go:embed` a
directory to stand in. An exported package with no external call site is
checklist §1.4 and FR-65's rejection trigger; I am not going to enforce §1.4
against a `Transport` interface and then wave through a package whose entire job
is to be a directory.

### Why not (c)

**(c) is not wrong, it is worse.** Moving the whole client build tree under
`live/` puts node-dependent test sources and contributor artifacts
(`predicates.manifest.txt`, `SIZE.md`, `client/test/*.mjs`) inside the public
package directory, and it makes the embed patterns do work that an exact
filename in a dedicated data directory does for free. It also blurs FR-74's
"node lives in one directory" story further rather than leaving it legible.

### RFC §14.2 amendment wording

Replace the `client/` line in §14.2's tree and add the two `live/` lines:

```
  live/                        THE public package
  live/livetest/               test scaffolding (L9-1 ruling A1)
  live/clientjs/               the shipped client artifact, go:embed'd by live —
                               data only, never a Go package
  client/                      client runtime SOURCE + its node tests; not a Go
                               package, not the shipped bytes. Built by tools/
                               into live/clientjs/
```

and add, after §14.2's "Two exported packages, and two is the cap" paragraph:

> **Where the embedded client artifact lives** (amended 2026-08-04, L9-1, on
> the addendum to the module-init review). `//go:embed` may not contain `..`
> and may not name a file outside its own package directory, so `live` cannot
> embed from `client/` and the original wording was not implementable. The
> shipped artifact is emitted by the `tools/` esbuild module to
> `live/clientjs/gotth-live.min.js` and embedded by exact filename;
> `client/` keeps the runtime source, the generated codec, and the node tests.
> There is exactly one copy of the shipped bytes, so no two-copy equality
> invariant exists to drift, and `-check` compares a fresh build of `client/`
> against the committed artifact — the FR-7 staleness check either way.
>
> **This adds no exported package and the cap of two stands.**
> `live/clientjs/` holds no Go file and is therefore not a package. Per C-20
> the architecture test asserts the module's non-`internal` package set is
> exactly `live` and `live/livetest`, so the cap is enforced structurally
> rather than by vigilance.

### One new condition

| # | Condition | Owner | Phase |
|---|---|---|---|
| **C-20** | **Make the C-12 cap a test, now that there is a directory sitting next to it.** `internal/arch` gains an assertion that the module's non-`internal` package set is **exactly** `live` and `live/livetest` — via `go list ./...`, the same real-build-graph mechanism the other three assertions use. This enforces the cap structurally instead of by reviewer vigilance, and it is the assertion that catches `live/clientjs/` acquiring a `.go` file by accident. It is C-12 condition 2's own principle applied to C-12 condition 3: the entire argument for the cap is a claim, and an unverified claim is how it quietly becomes false. | DEV-1 | checkpoint 1, with `live.Script()` |

`nit:` two things I noticed while looking and am **not** ruling on, for the
checkpoint-1 package: `client/test/*.mjs` needs node, which is a second
directory requiring it against dependencies.md §3's "node exists in exactly one
container image and one directory" — either the claim or the layout wants
adjusting, and FR-74 is PM-1's to read. And `client/` ships in the module zip
(only `tools/` is excluded, having its own `go.mod`), so consumers `go get`
the runtime sources and node tests; that is true today regardless of this
ruling and is a size question, not a correctness one.

— L9-1, 2026-08-04
