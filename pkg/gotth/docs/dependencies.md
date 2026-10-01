# gotth-live dependency ledger

| | |
|---|---|
| **Status** | Current ledger; dated measurement sections remain point-in-time evidence |
| **Date** | 2026-08-11 |
| **Author** | DEV-1 (Server Core / Go) |
| **Satisfies** | PRD NFR-9, FR-69; review checklist §10 |
| **Governed by** | [RFC-0001 §14.3](rfc/001-architecture.md) |

## 0. How to read this ledger

There is **no purity rule**. Any dependency is admissible with an adequate
justification; L9-1 judges, and the bar scales with what lands in a consumer's
`go.mod` (NFR-9). Every entry carries the four-part justification checklist §10.1
requires — **(a)** what it buys concretely, **(b)** maintenance health,
**(c)** transitive weight, **(d)** cost of owning the alternative — plus, for
Tier 1, the two extra clauses FR-69 adds at the stdlib-submission bar: **why the
standard library cannot do it**, and **the removal cost if upstream is
abandoned**.

| Tier | Meaning | Bar |
|---|---|---|
| **1** | Direct dependency of the public module — lands in users' `go.mod` | Highest. Needs L9-1's explicit approval in the PR thread. Every transitive dependency we add is one our users cannot refuse. |
| **2** | Test-only | Justification required; reviewer approves |
| **3** | Tooling / CI / bench, outside the library's module graph | Note it; no ceremony |

**On measurements (checklist §10.2).** The ledger must quote a real
`go list -m all` count delta and a real binary-size delta. Both are measured
and quoted for every Tier 1 entry: §5.1 at module init, §5.2 at checkpoint 1,
where the three remaining Tier 1 dependencies actually landed, and §5.3 against
`examples/counter` — the binary obligation 2 names — once it existed. §5 is the
standing obligation, and every PR that changes `go.mod` re-quotes it. The Go
1.26 + Liquid Proto centralization on 2026-08-11 changed the current dependency
shape; older checkpoint tables below remain labeled with the date and revision
they measured. Everything else below — versions, licences, release
dates, declared requirements, issue counts, contributor distribution — was read
from upstream on **2026-08-04** and is quoted, not estimated.

### Standing correction — one module, 2026-08-27

The single-module fold landed after most of this ledger was written, and it
moves one fact several sections argue from: there is now **exactly one `go.mod`
in this tree's world, at the export root**, and it is
`github.com/candacelabs/csf`. gotth-live is that module's `pkg/gotth`
package. The satellites this ledger calls modules — `tools/`,
`test/{routers,sampling,memory}`, `docs/guide/_samples`, the three example
applications (which also left this tree, for `examples/gotth/`) and the three
benchmark applications under `bench/apps/` — are packages of it.

This correction supersedes the **module count, and the "declared by a `go.mod`
nothing a consumer builds" mechanism**, wherever they appear below. It does not
rewrite the dated measurements, which were true of the tree they measured:
§2.2's `chi` and `gin`, §2.3's `otel/sdk`, §3's `esbuild` row and §3's
benchmark-applications row each name a satellite `go.mod` that is gone,
and their requirements are declared by the one `go.mod` a consumer does resolve.

Four things survive the fold, and they are what a reader should hold the tree to
now:

- **No new name.** Every requirement those satellites carried was already a
  Tier 1 or Tier 2 entry of this ledger, or a transitive of one. That is the
  claim that always did the work; the module boundary was how it used to be
  enforced, not what it meant.
- **No nested module.** `ci.sh`'s D-5 step fails if any `go.mod` exists below
  the export root other than the root's own. It is the invariant the export
  itself depends on, and it is stronger than the walk it replaced.
- **The node quarantine is untouched.** FR-74's other half was never about
  `go.mod`: node and npm exist in `bench/` and in `.dis/Dockerfile.bench` and
  nowhere else, and that is still checked.
- **Tier 3 is drawn by kind, not by module.** A tool, a benchmark application,
  or a CI harness is Tier 3 because of what it is — which is what §3's own
  heading says, and what makes the tier survive losing its module boundary.

---

## 1. Tier 1 — lands in users' `go.mod`

### 1.1 `github.com/coder/websocket` — the transport

| | |
|---|---|
| Version | **v1.8.15** (2026-06-15) |
| Licence | **ISC** — permissive, compatible |
| Declared requirements | **zero** (`go.mod` has no `require` block) |
| Go directive | `go 1.23` — below our `go 1.26` floor (§1.6), so it imposes nothing |
| Release cadence | v1.8.12 (2024-08) → v1.8.13 (2025-03) → v1.8.14 (2025-09) → v1.8.15 (2026-06) |
| Open issues | 69 · 5,372 stars · last push 2026-06-15 |
| `go list -m all` delta | **+1**, measured at checkpoint 1 (§5.2) — the expectation held exactly |
| Binary-size delta | **+3,091,256 B** in-module (§5.2), **+2,856,203 B** from a consumer's module (§5.3). Larger than templ and OTel together, and not what §1.1 expected. §5.3 attributes it: only **52,897 B** is this library's own compiled code, and the other 98 % is the transitive `crypto/*` and `net/http` the RFC 6455 handshake requires |

**(a) What it buys.** A correct, Autobahn-tested RFC 6455 server implementation
with a **context-aware API** — `Conn.Read(ctx)`, `Conn.Write(ctx, …)`,
`Conn.SetReadLimit(n)` — which maps one-to-one onto review-checklist §6.3 ("every
blocking operation selects on `ctx.Done()`") and onto ADR-001 §4.2's requirement
that the inbound size cap apply *before* payload allocation. The alternative API
style (deadlines) would force us to translate contexts into deadlines at every
call site, which is exactly the kind of hand-maintained correspondence that rots.

**(b) Maintenance health.** Four releases across two years, steady rather than
busy, which is appropriate for a protocol implementation of a frozen RFC. The
honest bus-factor note: commit history is dominated by the original author
(`nhooyr`, ~700 of ~740 commits), but the project was **transferred to Coder**, a
funded company that depends on it in its own product. So the *history* is
single-author and the *stewardship* is corporate — a better position than either
signal alone suggests, and better than `gorilla/websocket`'s (§4.1).

**(c) Transitive weight.** Zero declared requirements — verified by reading
upstream's `go.mod`. This is the single strongest argument for it: a Tier 1
dependency that adds exactly one module to a consumer's graph.

**(d) Cost of owning the alternative.** ADR-001 §3.4 costs out a hand-rolled
server-side subset: ~600–900 lines covering masking, fragmentation reassembly,
the close handshake, control-frame interleaving, and payload-length edge cases —
plus ownership of Autobahn conformance in perpetuity. Poor value against a
zero-transitive-dependency library.

**Why the stdlib cannot do it (FR-69).** Go's standard library has no WebSocket
implementation. `golang.org/x/net/websocket` is explicitly deprecated by its own
package documentation and does not implement RFC 6455 correctly.

**Removal cost if abandoned (FR-69).** Bounded and pre-planned. First fallback is
`gorilla/websocket` (§4.1), also zero-dependency, behind a shim of roughly 150
lines translating context to deadlines. Second fallback is the in-house subset
above. Because `internal/wsx` is the **only** package that imports it (RFC §3.5,
enforced by an architecture test), the blast radius of either move is one package.

---

### 1.2 `google.golang.org/protobuf` — the wire format

| | |
|---|---|
| Version | **v1.36.11** (2025-12-12) |
| Licence | **BSD-3-Clause** |
| Declared requirements | 2: `github.com/golang/protobuf` v1.5.0, `github.com/google/go-cmp` v0.7.0 |
| Go directive | `go 1.23` |
| Open issues | 13 · 3,341 stars · last push 2026-01-20 |
| `go list -m all` delta | **+3** for a consumer once `live` imports the generated package (§5.1's D7 disclosure); +1 while it did not |
| Binary-size delta | **+5,514,522 B** (§5.2) — the largest single contributor, as expected, and it is the reflection runtime |

**(a) What it buys.** The runtime that `protoc-gen-go` and `protoc-gen-liquidproto`
output requires. PRD FR-3 makes liquid proto the only thing on the wire; nothing
else can produce or consume it. It also supplies `protoreflect`, which
protocol.md §5.2 and §6 (H-1, H-4) use to make the enum-domain and
list-cardinality checks *descriptor walks* rather than per-field code — the
mechanism that keeps those invariants from rotting as the schema grows.

**(b) Maintenance health.** Maintained by Google as part of the protobuf project.
13 open issues on a project of this reach is a strong signal. Release cadence is
regular (v1.36.9 → .10 → .11 across Sep–Dec 2025). Bus factor is
organisational, not personal.

**(c) Transitive weight.** Two declared requirements, both benign: the legacy
`github.com/golang/protobuf` shim and `go-cmp` (test-facing). Module-count impact
is small; **binary** impact is the one to watch, because the protobuf runtime
carries reflection machinery. Measured at module init.

**(d) Cost of owning the alternative.** Hand-writing a proto codec for our fixed
eight-message schema is genuinely feasible on the *encode/decode* axis — we are
already doing exactly that for the client (protocol.md §10.2). But we would lose
`protoreflect`, and with it the descriptor-walk enforcement of H-1/H-4 and the
conformance test that proves every oneof member crosses its `Validate*` case. We would
also lose unknown-field preservation (FR-10), which the in-place Liquid Proto
validation boundary preserves. Not worth it.

**Why the stdlib cannot do it.** No protobuf support in the standard library.

**Removal cost if abandoned.** Effectively unremovable, and this is accepted
deliberately: the PRD's central differentiator *is* protobuf-based. If the module
were abandoned it would be forked, not replaced. This is the one Tier 1
dependency whose removal cost we do not claim is bounded, and saying so is more
useful than pretending otherwise.

---

### 1.3 `github.com/a-h/templ` — the render path

| | |
|---|---|
| Version | **v0.3.1020** (2026-05-10) |
| Licence | **MIT** |
| Declared requirements | **14 direct + 6 indirect** |
| Go directive | `go 1.25.0` — below gotth-live's current Go 1.26 floor |
| Open issues | 42 · 10,432 stars · last push 2026-07-24 |
| Release cadence | v0.3.977 (2025-12) → v0.3.1001 (2026-02) → v0.3.1020 (2026-05) |
| Bus factor | **`a-h` 709 commits, `joerdav` 69, then a long tail** — single-maintainer-dominant |
| `go list -m all` delta | **+11** (§5.2) — the largest of any Tier 1 entry, as expected, and all of it the CLI and LSP requirements |
| Binary-size delta | **+152,893 B** (§5.2) — small, as expected: a consumer links the runtime, not the CLI |

**(a) What it buys.** PRD §4 makes templ the **only** v1 authoring path (BL-5
backlogs alternatives), so this is not a preference. It buys compile-time-checked
components, contextual escaping (FR-50), and `templ.Component` as the render
contract that `Fragment[S].Render` returns.

**(b) Maintenance health.** Active and popular — three releases in six months,
42 open issues against 10.4k stars. **But the bus factor is the weakest of any
Tier 1 entry**: one author holds ~85 % of commits. This is stated plainly because
it is the ledger's job to state it, and because PRD §4's exclusion of alternative
template engines means we have no in-tree fallback.

**(c) Transitive weight — the finding that matters.** templ's `go.mod` declares
14 direct requirements (`fsnotify`, `cli/browser`, `fatih/color`, `rs/cors`,
`golang.org/x/tools`, `stretchr/testify`, …). Those exist for the **CLI and
LSP**, which live in the same module. I read upstream's root package: the
runtime (`runtime.go`) imports **only the standard library plus templ's own
`safehtml` subpackage**. So:

- **Compiled into a consumer's binary:** the templ runtime only. Binary impact
  should be small.
- **Visible in `go list -m all`:** templ's declared requirements, because Go
  resolves requirements at module granularity. This is the number checklist §10.2
  wants, and it will look worse than the binary number.

Both are measured at module init and **both are reported**, because quoting only
the flattering one is exactly what FR-73 exists to prevent elsewhere in this
project. If the module-graph number is unacceptable to L9-1, the mitigation is an
upstream request to split the CLI into its own module — not a workaround on our
side.

**Go directive — historical source of the floor, no longer the current one.**
templ declares `go 1.25.0`, and a dependency's directive raises a consumer's
toolchain requirement whether or not the design needs it. Cycle 2 therefore
accepted Go 1.25 rather than pinning templ backwards. The 2026-08-11
centralization moved gotth-live and the Liquid Proto toolchain to **Go 1.26**
explicitly; the module they are both packages of declares `go 1.26.0`, and CI
pins Go 1.26.5.

**This entry is why §5 gained its sixth obligation.** The floor moved because a
Tier-1 dependency changed a consumer-visible number silently, and it was caught
while writing this ledger rather than at review time. §5 now requires every
`go.mod`-changing PR to quote each Tier-1 dependency's `go` directive alongside
the module-count and binary deltas, so the next occurrence is caught by the
process instead of by luck.

**(d) Cost of owning the alternative.** Writing a template engine is not on the
table. The realistic alternative is `html/template` (stdlib), which PRD §4
excludes for v1 and BL-5 backlogs; it would cost the compile-time checking and
the component model the whole DX story rests on.

**Why the stdlib cannot do it.** `html/template` exists and is capable, but it is
runtime-parsed and produces no typed component values, so `Fragment[S].Render`
would return `func(io.Writer) error` and every render error would be a runtime
error. That is a deliberate product decision recorded in PRD §4, not a technical
impossibility — stated honestly.

**Removal cost if abandoned.** High and not fully bounded. `Fragment[S].Render`'s
signature mentions `templ.Component`, so a replacement changes the public API
(api-surface.md §2.1). BL-5's render adapter is the pre-planned escape hatch and
would need to land first.

---

### 1.4 `go.opentelemetry.io/otel/trace` and `…/metric` — **ADMITTED (L9-1 D1, Option A)**

| | |
|---|---|
| Version | **v1.45.0** (2026-08-03) |
| Licence | **Apache-2.0** |
| Declared requirements (`otel/trace` submodule) | 1 runtime: `go.opentelemetry.io/otel`; plus go-cmp/testify (test) |
| Declared requirements (`otel` root module) | 8, incl. `otel/metric`, `otel/trace`, `go-logr/logr`, `go-logr/stdr`, `cespare/xxhash/v2`, `go.opentelemetry.io/auto/sdk` |
| Open issues | 197 · 6,502 stars · last push 2026-08-03 |
| `go list -m all` / binary delta | **+6 modules, +232,809 B** (§5.2). The pre-registered fallback triggers above 8; it does not trigger |
| **Required by `gotth-live/go.mod` as of `d66e4953`** | **three modules, all direct**: `go.opentelemetry.io/otel/trace`, `go.opentelemetry.io/otel/metric` **and `go.opentelemetry.io/otel` itself** — §5.4 |

**Approved by L9-1 as decision D1: Option A — the core module depends on the
OTel *API* only; the consumer brings the SDK.** Options B and C were rejected: C
reinvents a standard and is a checklist §1.4 violation on its face (a
one-implementation interface, the same thing the RFC refuses for transport), and
B either makes FR-38's "one option" false for tracing or forces a core interface,
which is C wearing a hat. Supporting evidence that this is ecosystem-ubiquitous:
this monorepo's own `go/go.sum` already resolves `otel`, `otel/trace`,
`otel/metric`, `otel/sdk`, `otel/sdk/metric` and `auto/sdk` transitively, with
nothing in `go/` importing OTel directly.

**Three binding conditions attach, all recorded here as required by D1:**

1. **Narrowest API module that compiles.** The proposal is to import
   `go.opentelemetry.io/otel/trace` and `go.opentelemetry.io/otel/metric` — the
   **submodules** — and **never** `go.opentelemetry.io/otel` itself. The root
   declares eight requirements; `otel/trace`'s own `go.mod` declares one runtime
   requirement. This is possible only because `Config.Tracer`/`Config.Metrics`
   take providers explicitly, so **the library never reads the OTel global**;
   that is now an architectural constraint, not a style preference. If
   `otel/attribute` proves unavoidable for span attributes it is added here with
   a stated reason, per the condition.
2. **Measured graph delta quoted in the adding PR** — `go list -m all`
   before/after plus binary-size delta (checklist §10.2). Cannot be produced now:
   the module does not exist and the Phase 0 host has no Go toolchain.
3. **Pre-registered fallback.** If enabling tracing adds **more than 8 modules**
   to a consumer's build graph, fall back to Option B (a `gotth-live/otel`
   submodule). Fixed in advance so the choice is not made by whichever number is
   more convenient.

> **MARKED 2026-08-05 by PM-1; the measurement landed the same day and the row's
> requirement line above is corrected. The condition itself is still L9-1's and
> is still open.** Condition 1 says the library imports the submodules and
> **never** `go.opentelemetry.io/otel` itself, *"if `otel/attribute` proves
> unavoidable for span attributes it is added here with a stated reason"*.
>
> **It proved unavoidable, and the stated reason condition 1 asks for is
> §5.2's**, written at checkpoint 1 and reproduced here so this row carries it
> rather than pointing at it: every option constructor in both APIs
> (`metric.WithAttributes`, `trace.WithAttributes`) takes an
> `attribute.KeyValue`, so there is no way to record a span or metric attribute
> without importing `go.opentelemetry.io/otel/attribute` — a package of the
> **root** module. The root has therefore been a build-list requirement all
> along, at **no additional graph cost**, because it is already `otel/trace`'s
> single declared requirement and §5.2's `+6` was measured with it. What
> `d66e4953` changed is not the dependency but its **declaration**: the root
> moved from `// indirect` to a direct `require`, which is what `go mod tidy`
> produces for a module whose packages the library imports, and which is the
> honest record of what was already true.
>
> **What remains open, and it is a question rather than a defect.** Condition 1's
> sentence *"never `go.opentelemetry.io/otel` itself"* was written about a
> **requirement line**, and there now is one. Two readings are available — that
> the condition was always about the *submodule-only import surface* and is
> satisfied, the `attribute` exception being exactly what it pre-registered; or
> that a declared direct requirement on the root is the thing it forbade, in
> which case it needs restating to say so. **PM-1 does not choose between them:
> D1 is L9-1's decision and its conditions are L9-1's to discharge.** Filed as a
> carried item — **L9-1** (the condition's wording), **DEV-1** (condition 3's
> re-quote: the +6 / +232,809 B tracing delta against the 8-module fallback has
> not been re-measured since `d66e4953` dropped `auto/sdk` and `go-logr/stdr`
> from the pruned graph, which may have moved it in our favour). Recorded in
> [`docs/pm/checkpoint-3-closure.md`](pm/checkpoint-3-closure.md) §7.5 and in
> the [checkpoint-3 gate report](gates/checkpoint-3.md) §7.

**(a) What it buys.** PRD FR-36 requires OTel-compatible traces spanning
receive → refine → authorize → reduce → render → encode → send → client morph,
and FR-38 requires it to be one option. api-surface.md §1.1 also proposes
`Config.Metrics metric.MeterProvider`, which would make OTel carry metrics too.

**(b) Maintenance health.** CNCF project, very active (v1.45.0 released the day
before this ledger was written), organisational bus factor. 197 open issues is
high in absolute terms but low relative to the project's surface.

**(c) Transitive weight — the finding that shaped D1's condition 1.** Depending
on the **`otel` root module** pulls eight requirements including `auto/sdk` and
two logr modules. Depending on the **`otel/trace` and `otel/metric` submodules**
(API only) pulls essentially just `otel` itself. This is a materially smaller ask
than "take OTel", and it was not visible in RFC §3.4 when that section was
written. It is now the concrete form of Option A, subject to L9-1's confirmation
in cycle 2.

**(d) Cost of owning the alternative.** RFC §3.4 option C — define a small tracer
interface of our own — costs zero dependencies and reinvents OTel badly; FR-36
says "OTel-compatible", and a bespoke interface satisfies that only by adapter,
which the consumer then writes. Option B (a `gotth-live/otel` submodule) keeps
the core `go.mod` minimal at the cost of making FR-38's "one option" untrue for
tracing.

**Why the stdlib cannot do it.** `log/slog` covers logs; there is no stdlib
tracing or metrics API.

**Removal cost if abandoned.** Low. Instrumentation is confined to one internal
package and a nil-checked boolean per session (instrumentation.md §4.2); the
public surface exposure is two `Config` fields.

**Consequence for api-surface.md.** If L9-1 picks option B, `Config.Metrics` and
`Config.Tracer` move to the submodule and the exported field count drops by two.

---

### 1.5 Logging — **stdlib `log/slog`, no dependency. SETTLED (L9-1 D2)**

PRD FR-37 requires structured logging through a standard interface and forbids
imposing a concrete logging dependency on consumers. `log/slog` satisfies both at
zero cost, so **there is no dependency to justify here** — this row exists so the
ledger is complete and the conflict is visible.

**Settled by L9-1 D2**, which reads the conflict more narrowly and correctly
than cycle 1 did: `go/CLAUDE.md`'s rule is "do not bypass `core.Logger` *inside
the `go/` module*", not "every Go artifact in this repository depends on
zerolog". gotth-live is a standalone module outside `go/` at a
stdlib-submission bar, and a library that puts zerolog in every consumer's
`go.mod` fails that bar.

**D2's binding condition, recorded here because it is a ledger obligation:** the
~40-line `slog.Handler` adapter binding library records to `core.Logger` ships
**with a test in the same PR** — one test driving a library log record through
the adapter and asserting the fields arrive on `core.Logger`. An untested adapter
is how "nothing is lost on the inside" quietly becomes false
(instrumentation.md §6.1).

---

### 1.6 `pkg/liquidproto` — canonical Liquid Proto runtime, in this module

**No longer a dependency in the sense the rest of this section means.** It was
one: the runtime lived in a module of its own and arrived here through
`go.mod`. It is now a package of the SAME module as the library,
`github.com/candacelabs/csf`, so nothing in `go.mod` names it and no
consumer can be on a different version of it than of gotth-live. The entry is
kept, at its original number, because what it buys and what it would cost to
replace are unchanged and are the reason the fold was safe.

| | |
|---|---|
| Version | **not versioned separately** — one module, one version, one revision |
| Licence | **Apache-2.0** |
| Declared requirements | `google.golang.org/protobuf` v1.36.11, already required by the library |
| Go directive | `go 1.26.0`, the module's own — this and templ set the current floor |
| Where linked | generated `internal/protocol/gotthlivepb/frame_liquid.pb.go` imports only `github.com/candacelabs/csf/pkg/liquidproto` |

**(a) What it buys.** One published owner for Liquid Proto's runtime,
annotation schema, and generator. Generated `Validate*` functions return an
inspectable `*liquidproto.Error`; its string form redacts rejected strings and
bytes. gotth-live no longer carries a copied runtime, annotation binding, or
generator implementation.

**(b) Maintenance health.** `pkg/liquidproto` is maintained in this tree,
beside its consumer, and publishes with it. The monorepo remains canonical and
the generated public repository is read-only.

**(c) Transitive weight.** The runtime adds no dependency beyond protobuf,
which gotth-live already requires. The annotation schema and
`protoc-gen-liquidproto` are contributor-time tools and are not linked into a
consumer binary. The migration PR must re-quote the module-count and binary
measurements before this entry can claim a numeric zero-delta.

**(d) Cost of owning the alternative.** The previous alternative duplicated
the runtime and kept the schema and generator under an experimental research
tree, giving one protocol three owners. Reintroducing that split is small in
lines and expensive in drift; replacing `pkg/liquidproto` would require
regenerating the validators to point at a different runtime.

**Why the stdlib cannot do it.** The Go standard library has no protobuf
runtime, descriptor API, or schema-specific code generator. The stable shared
primitive is the Liquid Proto validation contract, not a replacement for Go's
own serialization packages.

**Removal cost if abandoned.** Bounded: preserve the generated `Validate*`
API, move the small runtime and generator together to another published module,
regenerate, and change one module requirement. The protobuf dependency remains
regardless.

**Bootstrap publication state.** Until `candacelabs/csf` has its first
exported version, a local consumer needs ONE replacement —
`replace github.com/candacelabs/csf => /path/to/the/checkout/candace` —
which brings the library and this runtime together because they are the same
module. It used to take two, one per module, and that was the whole of what the
fold removed. The remaining replacement is transitional and goes away when the
exported version is pinned.

---

## 2. Tier 2 — test-only

Tier 2 splits in two, and the split is the whole of what §2.2 has to say. Being
test-only decides whether a dependency is *linked*; it does not decide whether
it is *visible*. Which `go.mod` declares it decides that, and the two questions
have been conflated here before.

### 2.1 Test-only, in the library's own `go.mod` — visible in a consumer's graph

**None of these is linked into a consumer's binary. All three are nonetheless
visible in a consumer's `go list -m all`, and that is measured below rather than
waved away.** Cycle 2 said flatly "none of these reach a consumer's `go.mod`",
which is true of the *build* and false of the *module graph*: Go resolves
requirements at module granularity, so a module we require for our own tests
enters the build list of anybody who requires us. §5 quotes the number.

**Justification anchor.** The three are mandated: the repository's testing
convention and review checklist §8.1 require Ginkgo v2 with Gomega for
behaviour-focused specs and `go.uber.org/mock` for expectation-based interface
mocks, and the operator restated that as binding on this project on 2026-08-04,
with a narrow carve-out for a small table-driven standard-library test where it
is clearly clearer. That is why all three are pinned in `go.mod` at module init
rather than arriving with the first suite that needs them: the alternative is a
ledger that churns on every early test PR.

| Module | Version | Licence | Health | Why |
|---|---|---|---|---|
| `github.com/onsi/ginkgo/v2` | v2.32.0 (2026-06-22) | MIT | very active (3 releases in June 2026); 126 open issues, 9,035 stars; 10 direct + 12 indirect requires | **House convention, restated as an operator directive** — PRD NFR-10 and checklist §8.1 mandate Ginkgo v2 for behaviour specs. Not a free choice. In use since module init: `internal/arch` and `internal/protocol`. |
| `github.com/onsi/gomega` | v1.42.1 (2026-06-23) | MIT | very active; 48 open issues | Same mandate; Ginkgo's matcher library. |
| `go.uber.org/mock` | v0.6.0 (2025-08-18) | Apache-2.0 | **quieter — last push 2025-12-17, ~8 months** | Same mandate (checklist §8.1) for expectation-based interface mocks. The quiet cadence is noted; it is a stable, feature-complete fork of `golang/mock` and test-only, so the risk is contained. No suite mocks an interface yet, so it is pinned through a **`tool go.uber.org/mock/mockgen`** directive — which is also how the generator gets pinned — rather than through an import that does not exist. Without that, `go mod tidy` would prune it and the first mock-based PR would have to re-litigate the version. |

**A fourth row stood here until the checkpoint-2 gate, and striking it is worth
more than the row was (PM-1 gate report §9.1).** The row justified
`github.com/playwright-community/playwright-go` v0.6100.0 as driving the
FR-25/FR-26 DOM conformance suite, with the sentence *"there is no other way to
assert focus, caret, IME composition, and `<details>` state survive a morph"*,
and it was followed by a long passage on condition C-18 — that a `//go:build
browser` tag stops the suite from running but not the module from entering
`go.mod`, because Go resolves requirements at module granularity, so the
mitigation had to be structural.

**Measured at the checkpoint-2 gate: `playwright-go` is in no `go.mod`, in no
`go.sum`, and imported by no `.go` file in this repository.** The suite it
claimed to drive is driven by `test/internal/conformance/cdp_test.go`, a
hand-rolled Chrome DevTools Protocol client written over
`github.com/coder/websocket` — a module §1.1 already admits as Tier 1 for the
transport. So the sentence "there is no other way" was disproved by the person
who found the other way, and the row described a design that a better one
superseded. It is recorded in §4 as the decision it actually is, because *"we
considered a browser-automation library and wrote 200 lines instead"* is a
better ledger entry than any dependency justification would have been.

Two things survive the strike rather than going with it. **C-18's mitigation was
right and is what shipped** — not for playwright, which never arrived, but as
the pattern, and it is now used **six** times rather than three: `test/routers`
(§2.2), `test/sampling` (§2.3) and `test/memory` carry test-only requirements out
of the library's graph, and `bench/apps/{counter,chat,dashboard}/gotth` (§3.1)
carry the benchmark applications' out of it under FR-74. All six are a separate
module with their own `go.mod`, and the pattern was pre-registered here before
any of them needed it. *(The count read three until 2026-08-05 and the three
bench modules were in no section of this ledger at all — L9-1's **C-44**. The
lesson is §2.3(e)'s, one section over: a count of separate modules is a
measurement of the tree — `find . -name go.mod` prints **twelve**, one root and
eleven satellites — and not a memory.)* And **C-18's reasoning about build tags is
still correct** and is the reason the pattern is separate modules rather than
tags; it is restated in §2.2 and §2.3, which is where a reader now meets it.

The failure mode is this project's own recurring one, running in the flattering
direction — a document asserting something nobody re-derived, and the assertion
was that we had spent a dependency we had not. That direction is exactly why
nobody noticed for four checkpoints, and `docs/dependencies.md` is an
L9-1-gated Phase 5 deliverable (NFR-9, FR-69), so a phantom row is a phantom row
in a gate artifact.

**(d) for all three:** owning the alternative means writing a spec runner, a
matcher library, or a mock generator. None is defensible; all three are
test-only, so the Tier 1 bar does not apply. (Owning the fourth alternative — a
browser-automation protocol client — turned out to be entirely defensible, and
we own it: §4.)

**"Test-only" is a property of the import graph, and one import would end it.**
D7's disclosure rests on the sentence *"they do not get the module content
hashes, because nothing in their build imports those packages"*, and that stays
true only while no shipped `.go` file imports one of the three. `live/livetest`
is where the pressure is: it is an exported package whose every helper takes a
`testing.TB`, Ginkgo's `GinkgoT()` deliberately is not one, and the obvious fix
is a Ginkgo import. §4 records why that import was refused and what it was
measured to cost. **No import, and no adapter either**: Ginkgo's `GinkgoTB()`
*is* a `testing.TB` and is called from the consumer's own test file, so nothing
in `livetest` has to reach for the framework at all (api-surface §6,
[rulings-review-wave.md](reviews/rulings-review-wave.md) §1 — which withdrew
the `livetest.NewTB` this paragraph used to name here). Nothing in §2.1 changes
as a result, which is the point of writing it down: the row is unchanged
*because* a decision was made, not because nothing happened.

### 2.2 Test-only, in a separate module — not in a consumer's graph at all

These are the first Tier 2 entries for which §2.1's correction does **not**
apply, and the reason is the structural mitigation §2.1 pre-registered under
condition C-18: they are declared by `gotth-live/test/routers/go.mod`, not by
`gotth-live/go.mod`. Nothing a consumer builds requires that module, so nothing
about it reaches their build list. They are Tier 2 rather than Tier 3 because
the tier boundary is drawn by *kind* — tests, not tooling — and §0's table asks
for a justification either way; the graph question is answered separately, and
here it is answered "no".

| Module | Version | Released | Licence | Declared requirements | Where |
|---|---|---|---|---|---|
| `github.com/go-chi/chi/v5` | **v5.3.1** | 2026-07-05 | MIT | **zero** — its `go.mod` has no `require` block | `test/routers` only |
| `github.com/gin-gonic/gin` | **v1.12.0** | 2026-02-28 | MIT | **15 direct + 20 indirect** | `test/routers` only |

**(a) What they buy.** PRD **FR-33** does not merely say the library is
router-agnostic; it names its own verification — "verified by mounting … under
`net/http`, `chi`, and `gin` in the test suite" — and L9-1's **C-23** added the
condition that makes it a test rather than a formality: three *distinct*
prefixes, at least one not `/live`. Those two router names are the requirement's
own text. There is no substitute that proves the same thing, because the
property under test is precisely "this works under a router we did not write".

**(b) Maintenance health.** chi: MIT, no dependencies at all, and a `go`
directive policy stated in its own `go.mod` ("supports the four most recent
major versions of Go"). gin: MIT, the most widely deployed Go HTTP framework,
and its 35 declared requirements are the honest cost of a framework that ships
binding, validation, rendering, and an HTTP/3 path. Neither is load-bearing for
anything gotth-live ships; a break in either breaks one test module.

**(c) Transitive weight — measured, and the reason the module is separate.**
This is the entry's whole argument, so it is a number rather than a claim.
Against the root module at **61** modules in `go list -m all`, **measured
2026-08-04 at checkpoint 1 (§5.2)**:

| Configuration | `go list -m all` | Delta |
|---|---|---|
| `gotth-live/go.mod` as shipped | **61** | — |
| …if it required chi | 62 | **+1** |
| …if it required gin | 94 | **+33** |
| …if it required both | **95** | **+34** |
| `gotth-live/go.mod` as shipped, *with* `test/routers` in the repository | **61** | **0** |

> **The baseline has since moved to 62 and these four deltas were not
> re-measured (§5.4, 2026-08-05).** They are left as measured rather than
> renumbered, because a table whose baseline is re-measured and whose derived
> rows are adjusted by arithmetic stops being a measurement. What the move does
> **not** touch is this entry's argument: the last row is the one that carries
> it, and `test/routers` costs a consumer **0** at either baseline, which is a
> property of the module boundary and not of the count. Re-running the four
> counterfactuals is DEV-1's, with §5's obligation 1, and it is not owed by this
> entry.

Thirty-four modules — a JSON codec, a YAML parser, a validator, an assembler,
a MongoDB driver, and a QUIC stack — is what one requirement's own verification
would have cost every consumer of this library, for two imports that appear in
one `_test.go` file. `test/routers/go.mod` is what makes the last row zero, and
it is the same mechanism §2.1 pre-registered for `gotth-live/conformance/`,
applied a checkpoint earlier than expected because FR-33 arrived first.

For scale rather than for the decision: the `test/routers` module's own build
list is **96**, against `examples/chat`'s **62** for the same direct set minus
these two. The 34-module difference lives entirely inside a module nothing
outside this repository requires.

**Obligation 2 (binary size) is not applicable and is not skipped quietly.**
`test/routers` builds no binary — the package is empty by construction so that
chi and gin are imported only by tests — and a test binary's size is not what
§5's obligation measures. Obligation 1 is discharged by the table above.
`gotth-live/go.mod` did not change in the PR that added this module, which is
the point of it.

**(d) Cost of owning the alternative.** Hand-rolling a router to stand in for
chi or gin would test a router this project wrote, which is the one thing FR-33
is not asking about. Dropping to `net/http` alone would satisfy the sentence and
abandon the requirement. Neither is a real option; the real choice was *where
the `require` lines live*, and that is what the separate module decides.

### 2.3 `go.opentelemetry.io/otel/sdk` — FR-36 clause 4's falsifier, in a separate module

| Module | Version | Released | Licence | Declared requirements | Where |
|---|---|---|---|---|---|
| `go.opentelemetry.io/otel/sdk` | **v1.45.0** | 2026-08-03 (`go list -m -json`) | Apache-2.0 | 10 direct + 6 indirect in its own `go.mod`; 5 of the direct ones are its own test dependencies (`go-cmp`, `testify`, `goleak`) or already here (`otel`, `otel/metric`, `otel/trace`) | `test/sampling` **and** `test/memory` |
| `go.opentelemetry.io/otel/sdk/metric` | **v1.45.0** | 2026-08-03 | Apache-2.0 | same release train | `test/memory` (direct); `test/sampling` (transitive) |

**This is a test-only satellite module with zero consumer impact**, by the same
mechanism `test/routers` uses and declared in
`gotth-live/test/sampling/go.mod`. Nothing a consumer builds requires that
module, so nothing about it reaches their build list.

**(a) What it buys.** FR-36 clause 4 says the server-side event path MUST be
exactly one sampling decision, and PM-1's ruling attaches a falsifier that is a
spec: over N interactions at any 0 < *p* < 1, the number of *partial*
server-side graphs must be 0. A sampling decision is made by an SDK sampler and
by nothing else. `internal/obstest` — the recording provider the conformance
suite uses — stamps one hard-coded `TraceID` on every span and never declines
to record (QA-1 defect **D-11**), so it can assert the *structure* that makes
one decision possible and cannot assert the decision. There is no substitute
for `ParentBased(TraceIDRatioBased(p))` here, because that expression is
literally what instrumentation §3.5 documents as the default and literally what
L9-1 ran to find C-30.

**(b) Why it may not enter `gotth-live/go.mod`.** §1.4 admits the OTel **API**
submodules and records L9-1 D1's condition that the library depend on
`otel/trace` and `otel/metric` and never the root — which is possible only
because `Config.Tracer` takes a provider explicitly and the library never reads
the OTel global. Go resolves requirements at module granularity, so a test-only
import of the SDK is not a test-only module: it would land in the build list of
every consumer, and it would spend D1's **pre-registered 8-module fallback
budget** on a test rather than on a feature. That is the same correction §2.1
had to make about a build tag.

**(c) Transitive weight — measured.** Against the root module at **61**,
**measured 2026-08-04** when this entry landed:

| Configuration | `go list -m all` | Delta |
|---|---|---|
| `gotth-live/go.mod` as shipped | **61** | — |
| `gotth-live/go.mod` as shipped, *with* `test/sampling` in the repository | **61** | **0** |
| the `test/sampling` module's own build list | **66** | +5 |

> **The root baseline is now 62 (§5.4, 2026-08-05), and the satellite's own list
> was not re-taken.** The row that carries this entry's argument is the middle
> one — a satellite module costs a consumer **0** — and that row is a statement
> about the module boundary, not about either count: it reads 0 at 61 and 0 at
> 62. Left as measured rather than renumbered, for §2.2(c)'s reason.

The five, taken as a set difference of the two `go list -m all` outputs, are
`go.opentelemetry.io/otel/sdk`, `go.opentelemetry.io/otel/sdk/metric`,
`github.com/google/uuid`, `go.uber.org/goleak`, and the satellite module
itself. Four would have been four every consumer never asked for; as shipped
they are zero. The number is small — this is not chi and gin's +34 — and the
argument does not rest on its size: §1.4's fallback budget is a count, and a
count spent on a test is spent.

**(d) Maintenance health.** Apache-2.0, the OpenTelemetry project's own Go SDK,
released in lockstep with the API submodules §1.4 already admits — same version
number, same day — so it is pinned to a release this ledger already tracks
rather than to a fourth cadence. Nothing gotth-live ships links it; a break in
it breaks one test module.

**Obligation 2 (binary size) is not applicable and is not skipped quietly**, for
the same reason `test/routers` records: the package builds no binary and a test
binary's size is not what §5's obligation measures. `gotth-live/go.mod` did not
change in the PR that added this module, so obligations 3–6 do not move either.

**(e) A second consumer, found by re-deriving the row rather than reading it.**
This section was written for `test/sampling` and said *"`test/sampling` only"*.
`test/memory` — G2's baseline harness, landed in the same checkpoint by a
different hand — requires `otel/sdk` **and** `otel/sdk/metric` directly, because
`cmd/memsrv` has to stand up a real SDK to measure what default-on observability
costs per session (`docs/bench/g2-baseline.md`'s observability-off cell). The
"Where" column above is corrected and `sdk/metric` gets its own row; it was
already inside `test/sampling`'s +5 as a transitive, and it is a *direct*
requirement of `test/memory`.

**Nothing about the argument changes, and that is the point of checking.** Both
are satellite modules with their own `go.mod`; the root module is still **61**;
two consumers of a module that reaches no consumer of the library is still zero
reachable modules. What would have been wrong is the sentence, and this ledger
had a phantom row struck in this same landing (§8) for exactly that — a
statement nobody re-derived. The rule this produces is worth stating: **the
"Where" column is a measurement, not a memory**, and a new satellite module
means re-running the check across all of them, not appending a row.

---

## 3. Tier 3 — tooling, CI, and bench (outside the library's module graph)

| Tool | Version | Licence | Where it lives | Why |
|---|---|---|---|---|
| `protoc` | 35.1 | BSD-3-Clause | `.dis/Dockerfile` | Compiles `proto/` (RFC §14.4). Contributor workflow only — **FR-7** guarantees consumers never need it. |
| `protoc-gen-go` | v1.36.11 | BSD-3-Clause | `.dis/Dockerfile` | Base Go codegen. |
| `protoc-gen-liquidproto` | this module's own source at the checked-out revision | Apache-2.0 | built by `gen.sh` from `pkg/liquidproto/cmd/protoc-gen-liquidproto`; deliberately not baked into the image | Liquid Proto validation codegen. Building from the canonical checked-out source keeps the generated validators, schema, and runtime on one revision. |
| `templ` CLI | v0.3.1020 | MIT | `.dis/Dockerfile` | Generates `*_templ.go`. Same module as the Tier 1 runtime but a different binary; the CLI never enters the library's build. |
| `github.com/evanw/esbuild` | v0.28.1 | MIT | **`candace/pkg/gotth/tools/` — its own module** | Minifies the client runtime for the NFR-2 gzip gate, and — since 2026-08-05 — the dev session inspector for NFR-8's separate 40,960-byte gate. Two entry points, two artifacts, two ceilings, one invocation. Kept in a separate module specifically so it stays out of the library's `go.mod`; 634 open issues against 40k stars reflects scale, not neglect (last push 2026-06-12). |
| node / npm, Next.js, and the bench app's lockfile | pinned in `bench/versions.lock.md` | various | **`bench/` only**, and `.dis/Dockerfile.bench` only | PRD **FR-74** quarantine. Node exists in exactly one container image and one directory. |
| `oha` or `vegeta` | pinned in `bench/versions.lock.md` | MIT | `bench/` | Open-model load generation for the equivalence spec's §3.7 RPS figure. |
| The three benchmark applications — `bench/apps/{counter,chat,dashboard}/gotth` | `templ` v0.3.1020, `ginkgo/v2` v2.32.0, `gomega` v1.42.1, plus the library itself | MIT (all three) | **three packages of the one module**, under `pkg/gotth/bench/apps/` — three separate modules with their own `go.mod` until the single-module fold | The gotth-live side of the Phase 5 comparison (equivalence-spec §2, §10). Tier 3 by **kind**: benchmark applications and their suites, not library code. They add no requirement *name* the module did not already carry — each of their directs is a Tier 1 or Tier 2 entry of this ledger — and PRD **FR-74**'s quarantine is untouched in the half that was never about `go.mod`: node lives in `bench/` and nowhere else. Added at condition **C-44**; the module claim is superseded by §0's standing correction. |

### 3.1 The three benchmark applications — what C-44 asked for, and what the fold changed

`af9057d1` put `bench/apps/{counter,chat,dashboard}/gotth` into the gate, and
this register did not know they existed. L9-1's **C-44** is that omission, and
the reason it is a finding rather than a tidy-up is §9's own erratum: §2.3's
"Where" column *"was wrong within hours of being written"* because two modules
landed concurrently and neither author read the other's `go.mod`. Three more
landed the same way.

**What C-44 asked for was a measurement; the fold changed the instrument, not
the answer.** When this section was written the three applications were three
modules and the evidence was read out of three `go.mod` files: an identical
direct set —

```
github.com/a-h/templ                                  v0.3.1020
github.com/candacelabs/csf/pkg/gotth       v0.0.0   (replace ../../../..)
github.com/onsi/ginkgo/v2                             v2.32.0
github.com/onsi/gomega                                v1.42.1
```

— plus 18 indirect requirements each, every one of them already a Tier 1 or
Tier 2 entry of this ledger or a transitive of one. The conclusion that did the
work was that **the applications add no *name* the library's own graph has not
already justified**, and the single-module fold preserved it exactly: `templ`,
`ginkgo/v2` and `gomega` are direct requirements of the one `go.mod` for the
library's own reasons, and the `replace` is gone because there is no longer a
separate module to point anywhere. The question is now answered by reading one
file instead of four.

They are Tier 3 rather than Tier 2 for a reason the fold could not touch,
because the tier boundary is drawn by **kind**: these are benchmark applications
and their suites, not the library's tests.

**Why Tier 3's "note it; no ceremony" is the right bar, and what the note has to
say to earn it.** Tier 3 used to mean *outside the library's module graph*, and
two one-line checks kept that honest: `grep -c 'bench' go.mod` returning **0**
at the library root, and a `replace ... => ../../../..` line in each of the
three that a consumer could not have. Both measured a boundary that no longer
exists, and pretending otherwise is exactly the failure §2.3(e) named. What
replaces them is one check and it is stronger: `ci.sh`'s D-5 step fails if
**any** `go.mod` exists below the export root other than the root's own, so
neither these applications nor anything else can quietly re-acquire a module —
and, being an invariant of the export rather than of this tree, it is enforced
on every publish as well as every run. FR-74's operational meaning survives in
the half that was never about `go.mod`: node and npm live in `bench/` and in
`.dis/Dockerfile.bench`, and nowhere else.

**They are in the gate and that is a separate fact from being in this register.**
`ci.sh`'s tree list names all three, and its D-5 guard is red if an entry names
a directory that is gone or an entry no step reads — so the CI list and the tree
cannot silently disagree about the trees the list names. What the fold cost that
guard is stated in `ci.sh` itself rather than hidden here: it used to walk for
`go.mod` files, so a new satellite was red by construction, and a new suite in a
new directory is now caught by nothing there. Nothing checked the *ledger*
against the tree either, which is how three modules were invisible here while
being visible there. §2.1's enumeration is corrected in the same landing for the
same reason.

**`refinec` is deliberately not installed.** protocol.md §3 uses the option form
of the refinement annotation, which stock `protoc` compiles, keeping the forked
compiler frontend out of CI's byte-reproducibility path. The research proved both
forms produce byte-identical descriptors, so this costs nothing.

---

## 4. Considered and rejected

A dependency ledger that lists only what we took is half a review artifact.

| Candidate | Rejected because |
|---|---|
| **`github.com/gorilla/websocket`** v1.5.3 | Also zero-dependency and perfectly serviceable, but last released **2024-06** and deadline-based rather than context-based, which fights checklist §6.3 at every call site. **Retained as the documented fallback** (§1.1). |
| **`github.com/prometheus/client_golang`** | Would make Prometheus a second Tier 1 observability dependency family alongside OTel. api-surface.md §1.1 takes `metric.MeterProvider` instead; consumers who want Prometheus use OTel's Prometheus exporter, which is *their* dependency, not ours. Collapsing two families into one is worth the indirection. |
| **`github.com/google/uuid`** | Not needed. `session_id` is 16 bytes from `crypto/rand`; the causal IDs are session-scoped `uint64` counters (protocol.md §4.1), chosen partly *because* they need no ID library on either side and cost 1–3 varint bytes instead of 18. |
| **A protobuf JavaScript runtime** (`protobufjs`, `google-protobuf`) | Would violate NFR-5 (no npm at runtime) and blow NFR-2 on its own — the entire Datastar framework is 13,277 B gzip (teardown §5.2). The client codec is generated for our fixed schema instead (protocol.md §10.2). |
| **`idiomorph` / `morphdom` as vendored client JS** | Measured at 3,350 B and 3,063 B gzip respectively — affordable, but RFC §10.1 needs the FR-25 preservation contract, the FR-27 opt-out, the FR-26 IME rule, and the fragment-ownership boundary *inside* the traversal, which is why LiveView forks morphdom and Datastar inlined its own. We implement the algorithm rather than wrap a library. |
| **`chi`, `gin`, or any router** | FR-33 requires plain `http.Handler` and no router requirement. The examples mount under `net/http`, `chi`, and `gin` in tests precisely to prove no coupling exists. |
| **Any clustering, session-store, or coordination library** | Automatic return under checklist §1.5 / §6 of the automatic-return list. v1 is single-node (PRD R-14). |
| **`golang.org/x/time/rate`** | Under consideration for FR-51's token bucket; a ~40-line in-house bucket avoids a Tier 1 module for one small algorithm. **Decision deferred to the PR that implements FR-51**, with the default being in-house. |
| **`github.com/onsi/ginkgo/v2` as an import of `live/livetest`** — the shipped Ginkgo `testing.TB` adapter | Ginkgo is already Tier 2 (§2.1) and already visible in a consumer's module graph, so this looks free and is not: §2.1's claim is that nothing a consumer builds *imports* it, and a non-test file of an exported package importing it ends that for everybody. **Measured**, on a scratch consumer module requiring gotth-live through a `replace` and importing `live/livetest`: as shipped, **11,073,189 B**, **42** modules in the consumer's build list, **0** Ginkgo packages linked. With one `ginkgo` import in `livetest/tb.go`: **14,557,205 B** (**+3,484,016**), **59** modules (**+17**), **11** Ginkgo packages linked, and **seven** modules whose *content* the consumer must now fetch and verify — `onsi/ginkgo/v2`, `Masterminds/semver/v3`, `go-logr/logr`, `golang.org/x/tools`, `x/mod`, `x/sync`, `x/sys` — against the metadata-only reach D7 was accepted on. That is what a project whose suites are plain `go test` would pay for an adapter it will not call. **A `live/livetest/ginkgotb` leaf package** would confine it to the people who want it and is not available either: api-surface.md §0.1 caps the surface at two exported packages pending an L9-1 ruling, and `internal/arch` asserts the cap. **The measurement stands and the rejection stands; what changed is the alternative.** This row used to end by naming `livetest.NewTB`, the handler-taking adapter — *"zero modules, zero bytes, one exported identifier"*. [Review-wave ruling 1](reviews/rulings-review-wave.md) withdrew it: Ginkgo ships `GinkgoTB()`, a real `testing.TB`, and a consumer calls it **from their own test file**, so `livetest` imports nothing either way and the answer now costs zero exported identifiers as well. The 17 modules above are the cost of a `livetest` **import**, which neither answer ever paid — so this row's number was never the argument for the adapter, and its removal does not weaken the refusal. If the leaf package is ever ruled in, this row is where the argument re-opens |
| **`playwright-go`, `chromedp`, `rod`, or any browser-automation library** | The FR-25/FR-26/FR-28 browser evidence needs four verbs — launch, attach to a page, evaluate JavaScript, read back a JSON value — and `test/internal/conformance/cdp_test.go` implements exactly those, in ~200 lines of Chrome DevTools Protocol over `github.com/coder/websocket`, which §1.1 already admits for the transport. Its own header gives the reason, and it is **FR-74 rather than pride**: *"every browser-automation library on offer arrives through npm with a lockfile and a post-install download, and the one property the bench quarantine exists to protect is that none of that can reach a consumer… the browser evidence costs zero new dependencies in any `go.mod` and zero npm anywhere."* `playwright-go` specifically also downloads browser binaries on first use, which breaks G11's "clean clone, no node, `go test`" property. **This is a decision to own 200 lines, and it is bounded**: the client implements what the criteria need and *"is not a general automation library and should not grow into one."* If a second engine ever lands (BL-31, WebDriver BiDi), that is where this decision gets re-opened — BiDi is a different protocol, not more of this one. |

---

## 5. Standing measurement obligations (checklist §10.2)

The PR that creates `gotth-live/go.mod` MUST fill in, and every PR that changes
`go.mod` MUST re-quote:

1. `go list -m all | wc -l` — before and after, per dependency added.
2. Binary size of `examples/counter` — before and after, per dependency added.
3. For templ specifically (§1.3): **both** the module-graph count and the
   binary-size delta, since they tell different stories and only reporting the
   smaller one would be dishonest.
4. `go.sum` complete and generated, never hand-edited (checklist §10.6).
5. Every version pinned; no `latest`, no pseudo-versions except where upstream
   has no tag.
6. **The `go` directive of every Tier 1 dependency**, quoted alongside the two
   deltas above. This obligation exists because of §1.3: a Tier-1 dependency
   moved a consumer-visible toolchain floor from 1.24 to 1.25 without anybody
   noticing until this ledger was written. A directive is as much a thing a
   consumer cannot refuse as a transitive module is, and it was the only one of
   the two this list did not ask for.

**Current-floor note (2026-08-11).** gotth-live and the Liquid Proto toolchain
are packages of one module, which declares `go 1.26.0`, and CI pins Go 1.26.5. Sections 5.1–5.5 are dated measurements of
earlier revisions; any statement there that the module declared Go 1.25 records
that checkpoint and does not describe the current tree.

### 5.1 Module-init measurements — 2026-08-04

Discharging obligations 1 and 2 for the PR that creates `go.mod` (§7 D6), on
Go 1.26.5 in the library dev container.

**Obligation 1 — `go list -m all`.** The number that matters is not ours, it is
a consumer's, so both are quoted. A consumer here is a module requiring
gotth-live and importing `live`.

| Configuration | gotth-live's own `go list -m all` | a consumer's | modules the consumer gains beyond itself and gotth-live |
|---|---:|---:|---:|
| empty module (the floor) | 1 | — | — |
| **library dependencies only** — `google.golang.org/protobuf` v1.36.11 | 4 | 3 | **+1** |
| **as shipped**, adding the mandated Tier 2 test frameworks | 43 | 18 | **+16** |

Read both rows. **Protobuf costs a consumer exactly one module**, which is the
strongest number in this ledger and the one FR-69 cares about: `protobuf`'s two
declared requirements (`golang/protobuf`, `go-cmp`) do not enter a consumer's
build list at all. **The Tier 2 mandate costs a consumer fifteen more** —
ginkgo, gomega, `go.uber.org/mock`, `google/pprof`, `Masterminds/semver`,
`go-logr/logr`, `slim-sprig`, `go-cmp`, `go.yaml.in/yaml/v3`, and six
`golang.org/x` modules (`mod`, `net`, `sync`, `sys`, `text`, `tools`) — **none
of which is linked into anything they build.**

That is the honest form of the Tier 2 claim, and it is reported rather than
softened because FR-73's rule cuts inward as well as outward. It is a
consequence of the testing convention, not a dependency decision this ledger is
free to make, so it is recorded as a **measured cost of a mandate** and flagged
to L9-1 as new **D7** rather than acted on unilaterally.

#### D7's disclosure, in full (condition C-19)

L9-1 closed D7 as **accept-with-disclosure**, and a disclosure that stops at the
module count is not the whole thing being disclosed. Four additions:

**How far it reaches into a consumer's `go.sum`.** A consumer requiring
gotth-live gets **16 `/go.mod` lines** in their own `go.sum` from the Tier 2
mandate — the hash of each mandated module's `go.mod`, which the module graph
needs in order to be computed at all. They do **not** get the module content
hashes, because nothing in their build imports those packages. So the reach is
metadata, and it is metadata they must fetch and verify.

**The evidence that nothing is fetched or linked.** Two checks, both cheap and
both repeatable:

- a build with an empty module cache and `GOFLAGS=-mod=mod GOPROXY=off`
  succeeds for a consumer importing `live`, which it could not do if any Tier 2
  module's *content* were needed;
- the binary a consumer produces is **byte-identical** whether or not the Tier 2
  modules are present in the module cache, because none of their packages is in
  the import graph.

The claim "unlinked" is therefore checkable rather than asserted, which is the
distinction this ledger keeps making about everything else.

**Option (c), and why it was not taken.** A third remedy existed beside the two
named above: drop the mandated frameworks and write the suites against
`testing` alone. It is rejected, and not on taste — the convention is an
operator directive restated as binding on this project, and a ledger does not
get to overturn one by preferring a smaller number. The cost of the mandate is
reported; the mandate stands.

**The count grows to +3 for the library's own dependencies, not +1.** §5.1's
headline "protobuf costs a consumer exactly one module" was measured when
`live` was a stub importing nothing. Once `live` imports
`internal/protocol/gotthlivepb`, protobuf's own two declared requirements enter
the consumer's build list with it, so the honest figure for the library's
dependency alone becomes **+3**. §5.2 measures the shipped graph.

**Obligation 2 — binary size.** `examples/counter` does not exist yet, so the
obligation cannot be discharged as written and is **not** quietly substituted
for. What can be measured now is the floor: a `package main` importing `live`
and doing nothing links at **1,821,535 B**, which is an empty Go binary,
because `live` is currently a stub with no imports. The first real figure comes
with the counter example in Checkpoint 1, and every dependency added before then
re-quotes this line.

**Obligation 6 — Tier 1 `go` directives, as shipped.** `coder/websocket` 1.23
(not yet required), `google.golang.org/protobuf` 1.23, `a-h/templ` **1.25.0**
(not yet required — this is the one that set the floor at that checkpoint),
`otel/trace` and `otel/metric` (not yet required). At that checkpoint,
gotth-live declared **`go 1.25.0`**; the current floor is Go 1.26 (note above).

### 5.2 Checkpoint-1 measurements — 2026-08-04

The server core adds the three remaining Tier 1 dependencies, so this section
re-quotes every obligation for them. Go 1.26.5, library dev container.

**Obligation 1 — module graph, per dependency.** Measured by adding each to the
module-init `go.mod` and taking `go list -m all | wc -l`. The baseline is 43.

| Added | `go list -m all` | delta | what arrives |
|---|---:|---:|---|
| baseline (module init) | 43 | — | — |
| `otel/trace` + `otel/metric` v1.45.0 | 49 | **+6** | `otel`, `otel/trace`, `otel/metric`, `auto/sdk`, `cespare/xxhash/v2`, `go-logr/stdr`, `stretchr/testify`, and a `go-logr/logr` upgrade v1.4.3 → v1.4.4 |
| `coder/websocket` v1.8.15 | +1 | **+1** | itself; its `go.mod` declares no requirements, as §1.1 claimed |
| `a-h/templ` v0.3.1020 | +11 | **+11** | itself plus `a-h/parse`, `andybalholm/brotli`, `cenkalti/backoff/v4`, `cli/browser`, `fatih/color`, `fsnotify`, `mattn/go-colorable`, `mattn/go-isatty`, `natefinch/atomic`, `rs/cors` — the CLI and LSP requirements §1.3 predicted would look worse than the binary number |
| **as shipped, all three** | **61** | **+18** | |

**D1 condition 3 is discharged and does not trigger.** The pre-registered
fallback was: if enabling tracing adds **more than 8** modules to a consumer's
build graph, fall back to Option B and move `Config.Tracer`/`Config.Metrics`
into a `gotth-live/otel` submodule. The measured figure is **+6**. Option A
stands, and it stands on a number fixed before the number was known.

**D1 condition 1, with the exception it anticipated.** The library imports
`go.opentelemetry.io/otel/trace`, `.../otel/metric`, and
`go.opentelemetry.io/otel/attribute`. The third is a package of the **root**
module, which §1.4 said never to import. It is unavoidable and the condition
allowed for exactly this: every option constructor in the metric and trace APIs
(`metric.WithAttributes`, `trace.WithAttributes`) takes `attribute.KeyValue`,
so there is no way to record an attribute without it. It costs **nothing
additional**, because the root module is already in the graph as `otel/trace`'s
single declared requirement — the +6 above is measured with it. The library
still never reads the OTel global, which is the property that made the narrow
import possible in the first place.

**Obligation 2 — binary size, per dependency.** `examples/counter` still does
not exist (it is DEV-3's), so the obligation is measured against the nearest
honest stand-in and the substitution is stated rather than hidden: a
`package main` inside this module, importing the named packages and doing
nothing. It links the same code a consumer's binary would.

| Configuration | bytes | delta |
|---|---:|---:|
| empty `func main() {}` | 1,821,567 | — |
| `+ internal/protocol` (protobuf runtime) | 7,336,089 | **+5,514,522** |
| `+ otel/trace` + `otel/metric` | 7,568,898 | **+232,809** |
| `+ coder/websocket` | 10,660,154 | **+3,091,256** |
| `+ a-h/templ` | 10,813,047 | **+152,893** |

Two of these deserve a sentence. **The OTel API costs 233 KB**, which is what
Option A was chosen for: the SDK is the consumer's and is not in this number.
**`coder/websocket` costs 3.1 MB**, which is more than templ and more than OTel
together, and is not something §1.1 predicted — it is `compress/flate` and the
`crypto/*` machinery the RFC 6455 handshake pulls in rather than the library's
own code. It is recorded because the ledger's job is to report the number that
surprises it, and it does not change the decision: the alternative is owning an
RFC 6455 implementation.

**Obligation 3 — templ, both numbers.** Module graph **+11**, binary
**+152,893 B**. §1.3 predicted exactly this shape: the module-graph number looks
bad because templ's CLI and LSP live in the same module, while the runtime a
consumer actually links imports only the standard library and templ's own
`safehtml`. Both are reported, because reporting only the flattering one is
what FR-73 exists to prevent.

**Obligation 6 — Tier 1 `go` directives, as shipped.** `coder/websocket` **1.23**,
`google.golang.org/protobuf` **1.23**, `a-h/templ` **1.25.0**, `otel/trace` and
`otel/metric` **1.23**. At checkpoint 1, the highest was templ's and the module
declared **`go 1.25.0`**. No dependency added in that checkpoint moved it; the
current floor is Go 1.26 (note above).

**D5 is closed: the token bucket is in-house.** §4 deferred the
`golang.org/x/time/rate` decision to the PR implementing FR-51, with the default
being in-house. That PR is this checkpoint, and the default held: the bucket is
23 lines in `internal/session`, takes its clock from the actor's injected one
so a spec can drive a thirty-minute idle timeout without waiting, and adds no
module. `x/time/rate` would have added one for an algorithm this size, and its
own clock is not injectable in the way these tests need.

### 5.3 The counter example — 2026-08-04: obligation 2 discharged as written

`examples/counter` now exists, so the obligation this ledger declined to
substitute for can be measured against the binary it actually names. Go 1.26.5,
library dev container, `go build` with no linker flags — the same conditions
§5.1 measured the floor under, confirmed by the floor reproducing to within
8 bytes of build-id noise.

**The number.**

| | Bytes |
|---|---:|
| empty `func main() {}` (§5.1's floor) | 1,821,535 |
| **`examples/counter`, the shipped application** | **16,171,201** |
| **delta** | **+14,349,666** |

`-trimpath` takes 16,798 B off it and nothing else changes.

**The ladder, measured from a consumer's module.** `examples/counter` is a
separate module requiring gotth-live through a `replace`, so unlike §5.2's
in-module stand-in these figures are what a consumer's build produces. Each row
adds its import to the row above.

| Configuration | bytes | delta |
|---|---:|---:|
| empty `func main() {}` | 1,821,543 | — |
| `+ google.golang.org/protobuf` (`proto.Marshal`) | 2,873,971 | **+1,052,428** |
| `+ otel/trace` + `otel/metric` | 3,033,156 | **+159,185** |
| `+ coder/websocket` | 5,889,359 | **+2,856,203** |
| `+ a-h/templ` | 6,409,113 | **+519,754** |
| `+ gotth-live/live`, blank import | 10,990,304 | **+4,581,191** |
| a minimal application that *calls* `live.New` and serves | 15,796,596 | **+4,806,292** |
| **`examples/counter`** | **16,171,201** | **+374,605** |

**§5.2's prediction is confirmed, and now attributed.** `coder/websocket` costs
**+2,856,203 B**, against the 3,091,256 B §5.2 measured in-module — the same
number to within 8 %, arrived at from the other side of a module boundary. §5.2
guessed at the cause; this measures it. `go tool nm` puts **52,897 B** of
`github.com/coder/websocket` symbols in the finished binary, so **98 % of the
2.86 MB is transitive standard library** — the `crypto/*` machinery and
`net/http` the RFC 6455 handshake requires, not the library's own code. The
decision is unchanged and the reason is now the measured one: what is being
bought at 2.86 MB is not a WebSocket implementation, it is the standard
library's TLS and HTTP stack, which almost any server links anyway.

**The biggest contributor is not a dependency.** It is **dead-code elimination
ceasing to apply**: a blank import of `live` links 10,990,304 B, and an
application that actually calls `live.New` links **+4,806,292 B** more. Nothing
was added between those two rows. What changed is reachability — chiefly the
generated frame types' protobuf reflection and registry tables, which the
linker can discard while nothing encodes a frame and cannot once something
does. Any figure quoted for this library against an unused import is therefore
about 30 % low, and this ledger will not quote one.

**The example's own code is +374,605 B**, or 2.3 % of the binary: two templ
components, the reducer, the store, `flag`, `log/slog`, `os/signal` and an
embedded stylesheet. An application is not what makes a gotth-live binary
large.

**One trap, recorded so the next person does not lose an hour to it.**
`go tool nm -size` reports `crypto/internal/fips140/drbg.memory` at
**33,554,432 B** — twice the whole binary. It is a **bss** symbol: a 32 MiB
zero-page reservation that costs nothing on disk and is resident memory, not
binary size. Any per-package attribution that sums `nm` sizes without excluding
bss produces a total larger than the file it is describing.

**Obligations 1, 3 and 6 for this PR.** The module graph is unchanged: the
example adds no requirement the library did not already have, and `go list -m
all` inside `examples/counter` is 62 — 61 as §5.2 measured plus the example
module itself. No `go` directive moved in that checkpoint; its floor was still
templ's `1.25.0`. The current floor is Go 1.26 (note above).

### 5.4 `d66e4953` — 2026-08-05: the root `go.mod` changed and obligation 1 is re-quoted

**Why this section exists.** §5's standing obligation is that *every* PR
changing `go.mod` re-quotes obligations 1–6. `d66e4953` changed it and nothing
re-quoted anything; PM-1's closure ledger §7.5 found the gap and could not close
it without a toolchain. This is the re-quote for obligation 1. **It is partial,
and §5.4.2 says which parts are still owed rather than leaving the section to
look complete.**

**What `d66e4953` did**, from its diff rather than its subject: five modules the
library imports from non-test code moved from `// indirect` to a direct
`require` (`a-h/templ`, `coder/websocket`, `go.opentelemetry.io/otel`,
`otel/metric`, `otel/trace`); `go.opentelemetry.io/auto/sdk` and
`go-logr/stdr` left; `github.com/rogpeppe/go-internal` and `gopkg.in/check.v1`
arrived as indirects. Its own body records that `go build ./...` is green either
side and that a fresh `go mod tidy` reproduces the committed file byte for byte.

#### 5.4.1 Obligation 1 — the root build list, measured

```
docker run --rm -v "$EXPORT:/w" -w /w/gotth-live -e GOFLAGS=-buildvcs=false \
    dis-gotth-live:latest bash -c 'go list -m all | wc -l'
```

| | modules in `go list -m all` |
|---|---:|
| root module at checkpoint 1 (§5.2, 2026-08-04) | **61** |
| **root module at `d66e4953`, measured 2026-08-05** | **62** |
| **delta** | **+1** |

**Whose measurement this is.** The orchestrator ran it, in
`dis-gotth-live:latest` against a `git archive` export rather than the shared
worktree, and reported it to PM-1; PM-1 has no Go toolchain on this host and did
not re-run it. It is quoted as theirs, on the same rule
`docs/qa/checkpoint-3-chaos.md` §R8 applies to the rows that are not QA-2's.

**The direct-require set as measured, which is the part that answers §1.4:**

```
github.com/candacelabs/csf/pkg/gotth   (the module itself)
github.com/a-h/templ
github.com/coder/websocket
github.com/onsi/ginkgo/v2
github.com/onsi/gomega
go.opentelemetry.io/otel              <- §1.4 condition 1's subject
go.opentelemetry.io/otel/metric
go.opentelemetry.io/otel/trace
google.golang.org/protobuf
```

Every name is an existing entry in this ledger — §1.1 `coder/websocket`, §1.2
`protobuf`, §1.3 `templ`, §1.4 the three OTel modules, §2.1 Ginkgo and Gomega.
**No dependency is admitted by this re-quote and no tier moves.** What moved is
one declaration and one count.

#### 5.4.2 What is NOT re-quoted here, and who owes it

Obligation 1 asks for the count *"before and after, per dependency added"*. No
dependency was added, so the per-dependency half has no subject — but three
things are genuinely owed and are named rather than skipped quietly:

| Owed | Why it matters | Owner |
|---|---|---|
| **Attribution of the +1.** Which module the build list gained is not derivable from the `require` blocks: two indirects left and two arrived, netting zero there, so the +1 is in the graph and not in the file. A `go list -m all` diff between the two trees settles it in one command | A count that moved by one with no name attached is the kind of number this ledger exists to stop carrying | **DEV-1** |
| **Obligation 2 (binary size), before and after.** Not measured. §5.3's `examples/counter` figure of 16,171,201 B predates this commit | The declaration change should be a no-op on the linked binary and nobody has shown that it is | **DEV-1** |
| **§1.4 condition 3's re-quote.** The +6 modules / +232,809 B tracing delta has not been re-taken since `auto/sdk` and `go-logr/stdr` left the pruned graph, and the pre-registered fallback threshold is a **count** | The threshold was fixed in advance precisely so it could not be argued later; re-measuring it now, while nothing turns on the answer, is when that is cheapest | **DEV-1**, with **L9-1** on the condition |

**Obligations 4, 5 and 6, checked at the file rather than assumed.** Obligation
4: `go.sum` is generated and not hand-edited — the commit's own body records that
a fresh `go mod tidy` in `dis-gotth-live:latest` reproduces the committed file
byte for byte, which is the checkable form of that claim. Obligation 6: **no
Tier 1 `go` directive moved in that checkpoint**; its floor was templ's `1.25.0`
and the module declared `go 1.25.0`, read from that revision's `go.mod` line 3.
The current floor is Go 1.26 (note above). Obligation 5: **every
requirement in the file is a pinned version, and exactly two are
pseudo-versions** — `github.com/google/pprof v0.0.0-20260402051712-545e8a4df936`
and `gopkg.in/check.v1 v1.0.0-20201130134442-10cb98267c6c`, both `// indirect`,
neither new in this commit's *kind* although `check.v1` is new in this commit.
Obligation 5's exception is for upstreams that publish no tag, and **whether each
of these two qualifies is a fact about the upstream that PM-1 did not check** —
it needs a network the gate host does not give a container. Named as unverified
rather than waved through; **DEV-1**, with the attribution above.

### 5.5 FR-57 dev reload — 2026-08-05: no dependency, and the binary figure re-taken

**Obligation 1 — module count: unchanged, and nothing was added.** Dev reload
adds no `require` to any module in this tree. The watcher
(`internal/cmd/gotth-live-dev`) polls modification times through
`filepath.WalkDir` rather than reaching for `fsnotify`; the server half uses
`crypto/sha256`, `crypto/rand` and `os`, all standard library and the first of
those already imported by `live/templ.go`. `tools/go.mod` is untouched: the new
artifact is a third entry point for the esbuild bundler that is already there.

The alternative was considered and is recorded so it is not re-proposed by
default. `fsnotify` would replace a 250 ms poll with inotify, which is the right
trade for a watcher over a large tree — and it is a **direct dependency in the
consumer-reachable module** for a development convenience, which NFR-9's
stdlib-submission bar does not carry. Measured on the tree it would serve:
`examples/counter` is **8 source files**, and one full scan of it is not a cost
anybody can perceive. The re-proposal condition is a real one: a tree where a
scan at 4 Hz shows up in a profile, or a platform where `WalkDir` misses an
edit. Neither has been observed here.

**Obligation 2 — binary size, before and after, measured.** Go 1.26.5,
`dis-gotth-live:latest`, `go build` with no linker flags, from
`examples/counter`, which is a separate module requiring gotth-live through a
`replace` — §5.3's conditions exactly.

| | Bytes |
|---|---:|
| `examples/counter` at `452e1e74` (immediately before FR-57) | 16,513,414 |
| **`examples/counter` with dev reload** | **16,536,630** |
| **delta** | **+23,216 (+0.14 %)** |

That is the embedded 2,474-byte artifact, the `live` package's dev-reload
routes and build-identity derivation, and whatever `crypto/rand` and `os`
reachability the latter adds on top of what `net/http` already links. It is
paid by every consumer's binary whether or not they set `Config.Dev`, exactly
as the inspector's 14,905 bytes are, and for the same reason: embedding is
unconditional and only serving is gated. `live/devreload.go`'s godoc says so at
the `//go:embed` rather than implying it away.

**And it re-takes a figure §5.4.2 flagged as owed.** That table records
*"§5.3's `examples/counter` figure of 16,171,201 B predates this commit"* and
assigns the re-measurement to DEV-1. The current tree measures **16,536,630 B**,
so the ledger's standing figure was **365,429 B** stale — of which **23,216** is
this landing and the remaining **342,213** belongs to everything between `d66e4953`
and `452e1e74`, chiefly FR-44's inspector artifact. **Attributing that remainder
is still owed and is still DEV-1's**; what is discharged here is only the
before-and-after for FR-57, against a baseline built from `git archive
452e1e74` rather than against a number carried in a document.

---

## 6. Banned-property check (checklist §10.5)

| Banned property | Status |
|---|---|
| Client-side npm runtime dependency (§7.4) | **Clear.** Node exists only in `bench/` and `.dis/Dockerfile.bench` (FR-74). The client runtime is one embedded, self-contained file. |
| JSON wire codec (§3.2) | **Clear.** No dependency here provides or requires one. The architecture test asserts no `encoding/json` import in `internal/protocol` or `internal/wsx`. |
| Clustering / coordination machinery (§1.5) | **Clear.** Nothing in this ledger provides it; §4 records the rejection. |

---

## 7. Open questions

| # | Question | Owner | Needed by |
|---|---|---|---|
| D1 | *Was closed at checkpoint 1 (§5.2); **condition 1 is re-opened as a question at the checkpoint-3 gate**, 2026-08-05.* Option A and the decision itself are untouched: the measured tracing delta is **+6 modules** against a pre-registered fallback threshold of more than 8, fixed before the number was known. What re-opens is narrower. `d66e4953` records `go.opentelemetry.io/otel` as a **direct** `require`, because the library imports `otel/attribute` — a package of the root module — which condition 1 pre-registered as the one exception and §5.2 recorded with its reason. So the *dependency* is unchanged and the *declaration* is new, and condition 1's sentence "never `go.opentelemetry.io/otel` itself" now admits two readings. **PM-1 does not choose**; see §1.4's block and §5.4 | **L9-1** (the wording), DEV-1 (condition 3's re-quote, §5.4.2) | before Phase 5, where this ledger is an L9-1-gated deliverable |
| D2 | *Closed, then superseded.* Cycle 2 accepted Go 1.25 rather than pin templ backwards. The 2026-08-11 Liquid Proto centralization moved gotth-live's explicit floor to **Go 1.26**; no dependency pin was rolled back | — | done |
| D3 | *Closed — L9-1 D2 settled `log/slog`.* Residue: the adapter's test is a shipping obligation, not an open question (§1.5) | DEV-1 | Phase 1 |
| D4 | templ's single-maintainer bus factor (§1.3) against PRD §4's exclusion of alternative template engines. Is BL-5's render adapter worth pulling forward as insurance? | PM-1 + L9-1 | not blocking Phase 1 |
| D5 | *Closed — the default held.* The FR-51 bucket is 23 in-house lines taking the actor's injected clock; `x/time/rate` would have added a module for an algorithm that size and a clock these specs cannot drive (§5.2) | — | done |
| D6 | *Closed — §5.3 discharges obligation 2 against the binary it names.* `examples/counter` links **16,171,201 B**, **+14,349,666 B** over §5.1's floor. §5.2's `coder/websocket` figure is confirmed from a consumer's module (+2,856,203 B, within 8 %) and attributed: 52,897 B is the library's own code and the rest is transitive `crypto/*` and `net/http`. The largest contributor turns out not to be a dependency but dead-code elimination ceasing to apply once something actually calls `live.New` | — | done |
| D7 | *Closed — L9-1 accepted with disclosure.* §5.1's disclosure is extended to the `go.sum` reach (16 `/go.mod` lines), the two checks that make "not fetched, not linked" verifiable rather than asserted, why option (c) was refused, and the correction of "+1" to "+3" once `live` imports the generated package | — | done |


---

## 8. Changelog

### Phase 3, the checkpoint-3 gate — 2026-08-05: the root build list is 61 → 62, and §1.4's condition has a stated reason and an open question

| Change | Source |
|---|---|
| **New §5.4 — obligation 1 re-quoted for `d66e4953`.** `go list -m all` at the shipping tree is **62**, against §5.2's 61: **+1**. Method named and quoted (`dis-gotth-live:latest`, `git archive` export, `GOFLAGS=-buildvcs=false`), and **attributed to the orchestrator who ran it** rather than to this ledger — PM-1 has no Go toolchain on this host. The nine-line direct-require set is published with it, and every name in it is an existing entry here: no dependency is admitted and no tier moves | §5 obligation **1**, owed since `d66e4953` and found by [`docs/pm/checkpoint-3-closure.md`](pm/checkpoint-3-closure.md) §7.5 |
| **§5.4.2 names three things the re-quote does NOT cover**, so the section cannot be read as complete: which module the +1 is (it is in the graph, not in the `require` blocks — two indirects left and two arrived), obligation 2's binary size before and after, and §1.4 condition 3's +6/+232,809 B tracing delta, unre-measured since `auto/sdk` and `go-logr/stdr` left the pruned graph. All three DEV-1's; the last also L9-1's | FR-73's rule applied inward: "not measured, and why" |
| **§1.4's requirement line is corrected and its condition-1 marker is rewritten from "owed" to "answered in part".** The stated reason condition 1 pre-registered is §5.2's and is now reproduced *in* §1.4 rather than pointed at: every option constructor in both APIs takes an `attribute.KeyValue`, so `go.opentelemetry.io/otel/attribute` — a package of the root module — is unavoidable, at no additional graph cost, because the root is already `otel/trace`'s single declared requirement. `d66e4953` changed the **declaration**, not the dependency | **D1** condition 1 |
| **The condition itself is filed as an open question with L9-1 named, and PM-1 does not choose between its two readings.** Either condition 1 was always about the submodule-only *import surface* and the `attribute` exception is exactly what it pre-registered, or a declared direct `require` on the root is the thing it forbade and the wording needs restating. D1 is L9-1's decision | PM-1 gate report [§7](gates/checkpoint-3.md), **not decided here** |
| **§2.2(c) and §2.3(c) keep their measured tables and gain a dated note that the baseline has moved.** The four chi/gin counterfactuals and the satellite's own 66 are **not** renumbered by arithmetic: a table whose baseline is re-measured and whose derived rows are adjusted stops being a measurement. Both entries' arguments rest on the row that reads **0** at either baseline, which is a property of the module boundary | §0, and §2.3(e)'s own rule that a count is a measurement and not a memory |

### Phase 3, checkpoint 3 — 2026-08-05: C-44, the three benchmark Go modules enter the register

| Change | Source |
|---|---|
| **New §3.1 and a Tier 3 row: `bench/apps/{counter,chat,dashboard}/gotth`.** `af9057d1` added three Go modules to the gate — each with its own `go.mod` requiring `templ` v0.3.1020, Ginkgo v2.32.0, Gomega v1.42.1 and the library through a `replace` — and `grep -n 'bench/apps' docs/dependencies.md` returned nothing. **Tier 3, because the tier boundary is FR-74's quarantine and that is exactly what these modules are for**: the root `go.mod` requires none of them (`grep -c 'bench' go.mod` = 0), and each carries the `replace ../../../..` a consumer cannot have | L9-1 condition **C-44** |
| **§2.1's enumeration of the C-18 pattern reads six, not three.** The pattern is now `test/routers`, `test/sampling`, `test/memory` **and** the three bench modules. The parenthetical states why the number was wrong, in §2.3(e)'s terms: a count of separate modules is a measurement of the tree, not a memory. `find . -name go.mod` prints twelve — one root, eleven satellites, all eleven declared in `ci.sh`'s `ci_modules`, verified against the tree on 2026-08-05 | **C-44**'s falsifier, second clause |
| **No dependency is admitted and no tier moves.** Each module declares 22 requirements — 4 direct, 18 indirect, identical across the three — and every one is already a Tier 1 or Tier 2 entry here or a transitive of one; the modules add no *name* this ledger had not already justified. What they added was three `go.mod` files this register did not know about, which is a different defect and the one C-44 names | §0 |
| **§1.4 gains a marker rather than an edit.** D1 condition 1's pre-registered trigger — *"if `otel/attribute` proves unavoidable … it is added here with a stated reason"* — has fired: library code imports `go.opentelemetry.io/otel/attribute`, a package of the **root** module the condition says is never imported, and `d66e4953` recorded the root as direct. The dependency did not change and the sentence about it did. Marked and assigned to DEV-1 + L9-1, not patched, because the condition is L9-1's | **D1** condition 1, found by re-deriving rather than reading |
| **Obligations 1–6 do not fire for this entry and one of them is owed elsewhere.** These modules changed no `go.mod` in this landing. `d66e4953` **did** change the root `go.mod` — five modules moved from indirect to direct, `go.opentelemetry.io/auto/sdk` and `go-logr/stdr` left, `github.com/rogpeppe/go-internal` and `gopkg.in/check.v1` arrived as indirects — and §5's standing obligation says every PR that changes `go.mod` re-quotes it. **That re-quote is owed and is not this entry's**; §2.2(c) and §2.3(c) both quote a root build list of **61** and neither has been re-measured since. Owner: DEV-1 | §5, obligation **1** |

### Phase 3, checkpoint 3 — 2026-08-04: a dependency refused, and the number it was refused on

| Change | Source |
|---|---|
| **New §4 row: Ginkgo as an import of `live/livetest`, rejected.** The library now ships the `testing.TB` adapter five files in this repository were hand-rolling (`livetest.NewTB`), and the shape of it is a dependency decision rather than an API one. Measured on a scratch consumer module importing `live/livetest`: **11,073,189 B / 42 modules / 0 Ginkgo packages** as shipped, against **14,557,205 B / 59 modules / 11 Ginkgo packages** with one `ginkgo` import in `livetest` — **+3,484,016 B, +17 modules**, and seven modules whose *content* a consumer must fetch, for an adapter a plain-`go test` project will never call | api-surface.md §6, **FR-15**, **NFR-9** |
| **§2.1 gains the sentence its own disclosure depends on.** D7 was accepted on *"they do not get the module content hashes, because nothing in their build imports those packages"* — a claim about the import graph that one import in one exported package would end. `live/livetest` is where that pressure is, and the row now says so and points at §4 rather than leaving the reader to notice | **D7**, condition C-19 |
| **No `go.mod` change, so obligations 1–6 do not fire**, and that is the finding rather than an omission: the cheapest form of this entry is a dependency that was not added. The measurement above was taken *because* nothing changed — a counterfactual is the only way to price a refusal | §5 |
| **The alternative was ruled out by a gate rather than by taste.** A `live/livetest/ginkgotb` leaf package would confine the cost to Ginkgo users, and api-surface.md §0.1 caps this surface at two exported packages pending an L9-1 ruling, with `internal/arch` asserting the cap. §4's row names where the argument re-opens if that ruling ever comes | api-surface.md §0.1 |

### Phase 3, checkpoint 3 — 2026-08-04: one module admitted, one struck for never having existed

| Change | Source |
|---|---|
| **New §2.3.** `go.opentelemetry.io/otel/sdk` **v1.45.0**, Apache-2.0, declared by `gotth-live/test/sampling/go.mod` and by nothing a consumer builds. FR-36 clause 4's falsifier needs a real `ParentBased(TraceIDRatioBased(p))` because a sampling decision is made by an SDK sampler and by nothing else; `internal/obstest` stamps one hard-coded `TraceID` and never declines to record (**D-11**), so it can assert the structure and not the decision. Root module stays at **61**; the satellite's own list is 66, and the five-module difference is a set difference of two `go list -m all` outputs, not an estimate | **C-30**, PRD Phase 3 box 2 |
| **§2.1's fourth row is struck.** `playwright-go` is in no `go.mod`, no `go.sum`, and imported by no `.go` file — and the row's claim that *"there is no other way"* to assert focus, caret, IME and `<details>` survive a morph was disproved by the person who found the other way. The C-18 passage goes with it; what survives is the mitigation it pre-registered, which is now what three satellite modules do, and its reasoning about build tags, restated where a reader meets it | PM-1 gate report **§9.1** |
| **§4 gains the decision the struck row was hiding.** Browser-automation libraries — playwright-go, chromedp, rod — considered and rejected for ~200 lines of CDP over `coder/websocket`, quoting `cdp_test.go`'s own reason (FR-74, and no npm anywhere) and its own bound (*"not a general automation library and should not grow into one"*), with BL-31's WebDriver BiDi named as where the decision re-opens | §9.1 |
| **§2.3's "Where" column was wrong within hours of being written**, and the re-check above is what found it. `test/memory` requires `otel/sdk` and `otel/sdk/metric` directly; the row said `test/sampling` only. Corrected, with `sdk/metric` given its own row and a new §2.3(e). The two modules landed concurrently in this checkpoint, so neither author could have read the other's `go.mod` — which is precisely the case a re-derivation catches and a review of the diff does not | this pass |

**Why a strike is a bigger entry than an admission.** The phantom row ran in the
**flattering** direction — it said we had spent a dependency we had not — which
is exactly why it survived four checkpoints and two reviews of this file. It is
the same defect class this project keeps catching (C-21's unread `total` column,
D-19's `clean` printed without `gofmt`, D-20's suite that was green because it
never ran): **a document asserting something nobody re-derived.** This ledger is
an L9-1-gated Phase 5 deliverable (NFR-9, FR-69), so the row would have reached
a gate as evidence. Every remaining Tier 1 and Tier 2 row in §1 and §2 was
re-checked against `go.mod`, `go.sum` and the import graph in the same pass;
they are all real.

### Phase 2, the FR-33 three-router suite — 2026-08-04: chi and gin admitted, at zero graph cost

| Change | Source |
|---|---|
| **§2 is split.** Being test-only decides whether a dependency is linked; which `go.mod` declares it decides whether it is visible. §2.1 is the existing content unchanged — three modules a consumer can see — and **§2.2 is new**: `chi` **v5.3.1** and `gin` **v1.12.0**, both MIT, declared by `gotth-live/test/routers/go.mod` and by nothing a consumer builds | **FR-33**, L9-1 **C-23** |
| **The separation is quoted rather than asserted.** Root module **61** modules; **+1** for chi, **+33** for gin, **+34** for both, and **0** as actually shipped. Thirty-four modules — a JSON codec, a YAML parser, a validator, an assembler, a MongoDB driver, a QUIC stack — is what FR-33's own named verification would have cost every consumer, for two imports in one `_test.go` file | obligation **1** |
| **The playwright mitigation is used a checkpoint early.** §2.1 pre-registered "separate module with its own `go.mod`" as the structural answer for `gotth-live/conformance/`. FR-33 needed it first, and `test/routers` is the same mechanism. That it was pre-registered rather than invented on demand is the part worth recording | condition **C-18**'s residue |
| **Obligation 2 is declared not applicable, with the reason.** `test/routers` builds no binary: its package is empty by construction so chi and gin are imported by tests only, and a test binary's size is not what the obligation measures. `gotth-live/go.mod` did not change, so obligations 3–6 do not move either | §5 |

**What this entry does not claim.** Nothing here says chi or gin is safe to
depend on at the Tier 1 bar; the question was never asked, because the answer to
"where do the `require` lines live" made it unnecessary. If a future suite wants
either of them inside `gotth-live/go.mod`, the 61 → 95 measurement above is the
number that argument has to beat.

### Phase 1, the counter example — 2026-08-04: D6 closed

| Change | Source |
|---|---|
| **New §5.3** measures obligation 2 against `examples/counter` rather than a stand-in. **16,171,201 B**, **+14,349,666 B** over the empty-import floor, with a seven-row ladder taken from a consumer's module rather than from inside this one | obligation **2**, **D6** |
| **§5.2's surprise is confirmed and, for the first time, attributed.** `coder/websocket` costs **+2,856,203 B** measured from the other side of a module boundary — the same number as §5.2's 3,091,256 B to within 8 %. `go tool nm` puts **52,897 B** of its symbols in the binary, so **98 % of the cost is transitive standard library**: the `crypto/*` and `net/http` the RFC 6455 handshake requires. §5.2 suspected this; §5.3 measures it, and the decision is unchanged | obligation **3**'s "report both numbers" rule, applied to a cause rather than a total |
| **A finding this ledger did not go looking for.** The largest contributor is not a dependency. A blank import of `live` links 10,990,304 B; an application that *calls* `live.New` links **+4,806,292 B** more, with nothing added between the two — the generated frame types' protobuf reflection becomes reachable and the linker can no longer discard it. Any size quoted for this library against an unused import is about 30 % low, and §5.3 says so rather than quoting the flattering figure | FR-73's rule, cutting inward |
| **A measurement trap recorded rather than left to be rediscovered.** `go tool nm -size` reports `crypto/internal/fips140/drbg.memory` at 33,554,432 B — twice the binary. It is bss: a 32 MiB zero-page reservation that is resident memory, not file size. Any attribution that sums `nm` sizes without excluding bss produces a total larger than the file | §5.3 |
| D6 closed. Obligations 1, 3 and 6 re-quoted for this PR and unchanged: the example adds no requirement the library did not already have, and no `go` directive moved | §5 |

### Phase 1, checkpoint 1 — 2026-08-04: conditions C-18 and C-19 closed, D1/D5/D7 discharged

| Change | Source |
|---|---|
| **§2's playwright mitigation is corrected rather than softened.** A `//go:build browser` tag stops the suite running; it does not stop the module entering `go.mod`, because Go resolves requirements at module granularity. The mitigation is now the pre-registered `gotth-live/conformance/` separate module, and the row is marked as a plan rather than a shipped dependency, since `playwright-go` is not in `go.mod` today | condition **C-18** |
| **§5.1 gains D7's full disclosure**: the `go.sum` reach (16 `/go.mod` lines, metadata and not content), the two checks that make "not fetched, not linked" verifiable — an empty-cache `GOPROXY=off` build and a byte-identical binary with and without the modules cached — why option (c) was refused, and the correction of the headline "+1" to **+3** once `live` imports the generated package | condition **C-19** |
| **New §5.2** measures both obligations per dependency for the three Tier 1 additions the server core makes, and fills the measured figures into §1.1–§1.4 in place of the "to be measured" placeholders | obligations 1, 2, 3, 6 |
| **D1 closed.** Tracing costs **+6** modules against a pre-registered fallback threshold of more than 8, so Option A stands on a number fixed before it was known. `otel/attribute` is imported — a root-module package — which condition 1 explicitly allowed for, at no additional graph cost, because the root is already there as `otel/trace`'s single requirement | **L9-1 D1** conditions 2 and 3 |
| **D5 closed.** The FR-51 token bucket is in-house, as §4's default said it would be: 23 lines taking the actor's injected clock, and no module | §4 |
| **D7 closed** as accept-with-disclosure, with the disclosure extended per C-19 | **L9-1** |

**The one number that surprised this ledger is recorded as such.**
`coder/websocket` costs **3.1 MB** of binary — more than templ and the OTel API
together — and §1.1 predicted nothing of the sort, having reasoned only about
its zero declared requirements. It is `compress/flate` and the handshake's
crypto machinery. It does not change the decision, because the alternative is
owning an RFC 6455 implementation, but a ledger that only reports the numbers
it guessed correctly is not doing its job.

### Phase 1, module init — 2026-08-04: conditions C-10 and C-11 closed

| Condition | Closure |
|---|---|
| **C-10** — the superseded `go 1.24` floor survived in the body after the changelog closed the question at `go 1.25` | Corrected in all three places. §1.1's directive row now reads "below our `go 1.25` floor"; §1.3's reads "this is what sets gotth-live's floor"; and §1.3's "Go-directive conflict" paragraph, which still presented two open options and asked L9-1 to pick, is rewritten as the resolution it became — accept `go 1.25`, with the reason and the approval cited. api-surface A5's parenthetical is corrected in the same sweep. |
| **C-11** — the `go` directive was missing from §5's standing obligations | Added as obligation **6**, with the episode that produced it stated in §1.3: a Tier-1 dependency moved a consumer-visible toolchain floor and it was caught while writing this ledger rather than at review time. A directive is as much a thing a consumer cannot refuse as a transitive module is, and it was the only one of the two the list did not ask for. |

**§5.1 is new and discharges the module-init half of D6.** `go list -m all` is
measured in three configurations, because the number that matters is a
consumer's rather than ours: the library's own dependency costs a consumer
**one** module, and the mandated test frameworks cost **fifteen more**, none of
them linked. The binary-size obligation is explicitly *not* discharged —
`examples/counter` does not exist, and substituting a different binary for the
one the obligation names would be the kind of quiet swap §5 exists to prevent.

**§2 is corrected rather than merely expanded.** "None of these reach a
consumer's `go.mod`" was true of the build and false of the module graph. The
measured version replaces it, the mandate is recorded as the justification
anchor, and the residual question — whether fifteen unlinked modules is
acceptable at a stdlib-submission bar — is raised as **D7** for L9-1 and PM-1
rather than answered here.

### Cycle 2 — 2026-08-04

Not reviewed in L9-1's cycle 1 (written after it landed), but updated for that
cycle's settled decisions.

| Change | Source |
|---|---|
| §1.4 OTel promoted from **L9-PENDING** to **ADMITTED**, with all three of D1's binding conditions recorded: narrowest API module (the `otel/trace` + `otel/metric` **submodules**, never the root), measured graph delta in the adding PR, and the **8-module pre-registered fallback** to Option B. Records that the library never reads the OTel global — now an architectural constraint, since it is what makes the narrow import possible | **L9-1 D1** |
| §1.5 logging promoted from **L9-PENDING** to **SETTLED**, with D2's binding condition (the `core.Logger` adapter ships **tested** in the same PR) recorded as a ledger obligation | **L9-1 D2** |
| §7 D1, D2, D3 closed; the residue is implementation obligations, not open questions | above, plus RFC §14.1 |
| The templ `go 1.25.0` conflict (§1.3) is resolved: gotth-live's floor is **`go 1.25`** | RFC §14.1 cycle 2 |

D4 (templ's single-maintainer bus factor), D5 (`x/time/rate` vs in-house), and
D6 (the measurement obligations) are unchanged and remain open with their
existing owners.
