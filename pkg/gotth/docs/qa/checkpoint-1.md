# Checkpoint 1 — QA-1 correctness gate report

| | |
|---|---|
| **Author** | QA-1 (Correctness) |
| **Date** | 2026-08-04 |
| **Gate instrument** | [PRD](../PRD.md) §6 Phase 1 exit criteria |
| **Also held against** | [protocol.md](../protocol.md) §6 (H-1…H-14), §7 (P1…P8); [review-checklist.md](../review-checklist.md) §8; [instrumentation.md](../instrumentation.md) §4A; [reviews/module-init.md](../reviews/module-init.md) C-15…C-20 |
| **Reviewed** | `165f5b25`…`cd1d4170`, plus QA-1's own commits |
| **Verdict** | ~~**BLOCK** — 12 of 19 criteria pass, 3 fail, 4 are partial~~ **superseded** |
| **Re-verified** | 2026-08-04, against `44f87764`. **Verdict now PASS** — see [§7](#7-re-verification-2026-08-04-against-44f87764) |

> **§1–§6 are the original gate report and are left as written.** They are the
> record of what was true when the gate was first run, and editing them to
> match the outcome would delete the evidence that the process worked. Every
> verdict, defect and number below was superseded by the re-verification in
> §7; read that for current status.

**The shape of this result, stated up front so the count is not misread.** The
core loop is in good condition. Every criterion about the protocol boundary,
the actor, provenance, determinism and the security hooks passes, and passes
against 120 adversarial specs QA-1 wrote from the specification documents
rather than from the implementation. Nothing blocking below is a defect in that
loop.

What blocks is **evidence infrastructure**: there is no CI in this module, two
named exit criteria (metrics flowing, traces flowing) have no verifying test at
all, and `staticcheck` — which the criteria name — has never been run and is
not clean. Those are cheap to fix and they are exactly the things that stop
being true quietly, which is why L9-1 raised C-15 and why it is not
discretionary.

---

## 1. What QA-1 re-ran, and what came back

Every command below was executed by QA-1 in `dis-gotth-live:latest` (or the
bench image where noted) against this working tree. Nothing is quoted from a
developer's report.

| Check | Command | Result |
|---|---|---|
| Build | `go build ./...` | clean |
| Vet | `go vet ./...` | clean |
| Format | `gofmt -l .` | empty |
| Tests, race | `go test -race -count=1 ./...` | **all packages ok** (14 packages, incl. QA-1's suite at 13.3 s) |
| Soak label | `GOTTHLIVE_SOAK=1 go test -race ./test/... -ginkgo.label-filter=soak` | ok, 14.9 s |
| Codegen reproducibility | `gen.sh --check` (root-mounted `docker run`) | **byte-identical to a fresh generation** |
| Client staleness gate | `tools/ … go run ./minify -check` | ok — **3,874 B gzip of 12,288** (68.5 % headroom) |
| Node client suite | bench image, `node --test client/test/*.test.mjs` | **51 pass, 0 fail** (bundle 2, codec 34, morph 15) |
| Counter example | `examples/counter`: build, `gofmt -l`, `vet`, `go test -race` | all clean |
| **staticcheck** | `staticcheck ./...` | **2 findings — not clean** (see D-5) |

`staticcheck` is **not present in `dis-gotth-live:latest`**. QA-1 installed it
(`honnef.co/go/tools/cmd/staticcheck@2025.1.1`) inside a container to run it at
all. That absence is itself part of D-2: a tool the exit criteria name, which
the project's own image cannot run, is a criterion nobody was ever going to
check.

### 1.1 QA-1's own suite

`gotth-live/test/internal/conformance/` — **120 specs**, Ginkgo v2 + Gomega,
green under `-race`, running in `go test ./...` by default except the
soak-, latency- and e2e-labelled specs.

| File | What it holds |
|---|---|
| `hostile_wire_test.go` | the parse boundary against bytes no encoder produces; every predicate probed at N and N+1 |
| `provenance_test.go` | P1…P8 re-implemented from protocol.md §7 |
| `limits_test.go` | event flood, mailbox at its bound, H-14 resync amplification, hostile acknowledgements, H-11 telemetry forgery |
| `determinism_test.go` | FR-19 across sessions and interleavings; FR-15's helper mutation-tested |
| `semantics_test.go` | handshake rejection, FR-47 no-bypass, double submission, H-8 stale views, FR-3 re-encode |
| `counter_e2e_test.go` | the counter binary as its own process, driven over a real WebSocket |
| `latency_test.go` | protocol-level event→patch percentiles |

**Why it lives under `test/internal/` and not `test/`.** See D-7. The short
version: `internal/arch`'s C-20 assertion requires the module's non-internal
package set to be exactly `{live, live/livetest}`, and `go list ./...` counts a
directory of `_test.go` files as a package. DEV-3 hit the same wall and solved
it by giving `examples/counter` its own `go.mod`. Nesting under `test/internal/`
is the same resolution.

---

## 2. Verdict per acceptance criterion

The PRD's Phase 1 exit criteria are unnumbered checkboxes; they are numbered
here **CP1-01…CP1-19 in document order** for citation, with the FR each carries.

| # | Criterion (abbreviated) | FR | Verdict |
|---|---|---|---|
| CP1-01 | Counter works end to end **in a real browser** | — | **PARTIAL** |
| CP1-02 | Connection lifecycle: handshake, auth, origin, heartbeat, enumerated close | FR-8/45/46 | **PASS** |
| CP1-03 | Wire audit: 100 % parses as `Frame`, zero non-proto bytes | FR-3, G5 | **PASS** |
| CP1-04 | Hostile-wire-data suite, typed errors, zero partial application | FR-5, FR-13 | **PASS** |
| CP1-05 | Session actor passes `-race` under concurrent event injection | FR-17 | **PASS** |
| CP1-06 | Reducer determinism helper exists and the counter uses it | FR-15 | **PASS** |
| CP1-07 | Repeated-render byte-equality test passes | FR-19 | **PASS** |
| CP1-08 | Event→**paint** latency measured and published, **on LAN**, p50/p95/p99 | — | **PARTIAL** |
| CP1-09 | Metrics flowing: FR-34 core set visible in Prometheus | FR-34 | **FAIL** |
| CP1-10 | Traces flowing: one trace spans receive→…→client morph | FR-36 | **FAIL** |
| CP1-11 | Provenance: a captured patch resolves; 100 %, zero unknown | FR-41, G4 | **PASS** |
| CP1-12 | Client ≤ 12 KB gzip with per-subsystem ledger | NFR-2/3 | **PASS** |
| CP1-13 | No-eval scan green; runtime verified **under strict CSP** | NFR-4, FR-49 | **PARTIAL** |
| CP1-14 | Authorization hook before every reducer; no frame kind bypasses | FR-47 | **PASS** |
| CP1-15 | Cross-origin attack test fails to establish or inject | FR-48 | **PASS** |
| CP1-16 | Leak test: 10k cycles return **goroutines and RSS** to baseline | FR-22 | **PARTIAL** |
| CP1-17 | Pre-generated proto checked in; clean-machine build | FR-7 | **PASS** |
| CP1-18 | `api-surface.md` current; **CI reports the count delta per PR** | FR-65 | **FAIL** |
| CP1-19 | Toolchain clean: `gofmt`, `vet`, **`staticcheck`**, `-race` | NFR-12 | **FAIL** |

### The passes, with what actually established them

- **CP1-02.** Handshake ordering (origin → identity → CSRF) verified from the
  outside in `semantics_test.go`, including the two cases a permissive
  implementation gets wrong: a request with **no** `Origin` header, and an
  origin that merely has an allowed one as a **prefix**. Both are refused 403.
  Heartbeat send and heartbeat-timeout close are covered by
  `internal/session/actor_test.go`. Close codes are a closed enumeration and
  `outbound_test.go` walks every `Close(` call site.
- **CP1-03.** Every payload captured in a driven session re-encodes
  byte-identically to the bytes it arrived as; the harness's read pump fails the
  spec if any non-binary message or non-`Frame` payload arrives at all;
  `internal/arch` holds that no first-party code on the wire path imports
  `encoding/json`.
- **CP1-04.** Beyond the committed vectors: truncated and never-terminating
  varints, a ten-byte all-continuation varint, field number zero, wire type 6, a
  proto3 group tag, a length prefix longer than the bytes that follow, a
  **four-gigabyte length prefix** (asserted to allocate < 8 MB, so the prefix is
  not trusted), duplicate `session_id` where the *second* is invalid (merged
  value must lose the frame), **two oneof members in one frame** where the kept
  one is server-only, and every bound probed at N and N+1 — name 64/65,
  fragment 64/65, key 128/129, value 8192/8193, field list 64/65, morph
  60000000/60000001, heartbeat 300000/300001, session id 0/15/16/17/36. All
  rejected as `*protocol.RejectError`, none yielding a payload.
- **CP1-06.** The counter uses `livetest.ReplayN` and
  `livetest.AssertDirtyComplete`. QA-1 additionally **mutation-tested the helper
  itself**: it must fail a reducer that reads a clock, fail one whose effects
  differ between replays, and refuse to certify a single replay or an empty log.
  It does all four. A determinism helper that passes everything is decoration,
  and this one is not.
- **CP1-11.** P1…P8 re-implemented independently. The mechanism is a join
  between the wire capture (written by the framer) and the provenance log
  (written by the actor in `step`) — different code, different sink, per
  instrumentation.md §4A.5 — so agreement is evidence. Both arms of G4's
  disjunction are exercised: a `CLIENT_EVENT` patch resolved from its bytes
  alone, and a `MOUNT` snapshot resolved to its named source. Zero unknown
  origins across the default run and the soak. **See D-1**: this criterion
  passes on G4's stated disjunction while the product claim behind it is
  weaker than it reads.
- **CP1-14.** Asserted over the **reducer**, which is the only definition of
  "bypass" that means anything: with `Authorize` denying everything, an
  `Event`, a `ResyncRequest`, an `Ack`, a `Heartbeat` and a `ClientTelemetry`
  were all sent, and `Reduce` ran zero times. The resync is confirmed to reach
  the hook under its reserved name `gotth.resync`.
- **CP1-17.** `gen.sh --check` reports the committed generated code is
  byte-identical to a fresh generation. `refinec` and `protoc-gen-gorefine` are
  absent from the image that builds the library.

---

## 3. Defects

Severity is QA-1's, against the checkpoint the defect lands in.

### D-1 — HIGH — an effect-emitted patch drops the event that scheduled it

**FR-42, protocol.md §4.2. Gate phase: 2. Does not block checkpoint 1;
pre-registered as a checkpoint-2 block.**

FR-42: a server-effect patch "MUST carry a synthetic origin naming the effect
source **and, where one exists, the upstream event that scheduled it**."
protocol.md §4.2 says that id belongs in `contributing_event_ids`.

In the counter — the flagship example, the FR-60 latency subject and the FR-71
comparison subject — the **only user-visible interaction** produces a patch with
no such edge.

**Minimal repro.** `GOTTHLIVE_E2E=1 go test ./test/... -args -ginkgo.label-filter=e2e -ginkgo.v`,
and read the report entry. Verbatim, from a click with `client_ref 4242`:

```
click:  client_ref 4242 → event_id 1, transition 2, patch_id 0 (suppressed, state unchanged)
patch:  transition 3, state_version 2, patch_id 2, server_seq 2,
        origin EFFECT/"effect:counter.watch", contributing []
```

The click's own transition changes no session state (the reducer delegates to a
shared store) so its render is suppressed and it emits no patch. The visible
patch is the store broadcast's, and it carries `event_id 0`, `client_ref 0` and
an **empty** `contributing_event_ids`. An operator holding the patch that
changed the number can reach `effect:counter.watch` and cannot reach the click.

**This is a library defect, not an example defect**, and two layers independently
discard the edge:

- `internal/session/effects.go:110` — `emitter(source)` closes over the effect's
  source alone. The scheduling event's id is in scope at the `runEffects` call
  site in `transition` and is never passed down. The constructed origin is
  `protocol.Origin{Kind: EFFECT, Source: …}` with no `Contributing`.
- `live/app.go:198` — the adapter builds `session.Event{Name, FragmentID,
  Fields}` and **drops `Event.ID`**, so an application cannot supply the edge
  either. `Actor.emitter` then zeroes it regardless.

The mechanism already exists — `Origin.Contributing` is populated on the
coalescing path — so the fix is to thread the scheduling event's id through
`execute`/`emitter` and union it in.

Held as a **pending spec** at `counter_e2e_test.go` (`PIt`, "names the click
among the contributing events of the patch it caused"), so checkpoint 2
inherits an executable statement of the requirement rather than a note.

### D-2 — HIGH — there is no CI for this module — **BLOCKING**

**Condition C-15 (owner DEV-1, due "Phase 1, before Checkpoint 1") is not
discharged.**

`.github/workflows/` contains only `blog-deployment-checks.yml` and
`sync-blog-submodule.yml`. Nothing references `gotth-live`. There is no
workflow file anywhere in or under `candace/pkg/gotth/`.

C-15 asked for a job running `go build`, `go vet`, `gofmt -l`,
`go test -race ./...` and the FR-7 regeneration check. None of it runs
anywhere. The consequence is not hypothetical — it is **five criteria whose
stated gate is `CI` and which nothing enforces**:

| Requirement | Gate | Enforced today |
|---|---|---|
| FR-7 — byte-reproducible codegen | CI | no — `gen.sh --check` exists and nothing calls it |
| NFR-2 — client ≤ 12,288 B, "CI MUST fail on exceedance" | CI | no |
| NFR-3 — per-PR gzip size delta by subsystem | CI | no |
| NFR-4 — no-eval static scan "in CI, not by convention" | CI | no — the assertion exists in `client/test/bundle.test.mjs`, unrun |
| NFR-12 — `gofmt`/`vet`/`staticcheck`/`-race` | CI | no |
| FR-65 — CI reports the exported-identifier count delta | CI | no (this is CP1-18) |

protocol.md §10.2 already refers to "CI's byte-reproducibility check (FR-7)" as
though it exists. L9-1 flagged exactly this and wrote the remedy; the remedy has
not landed.

**Repro:** `ls .github/workflows/` and `grep -rl gotth-live .github/`.

### D-3 — MEDIUM — CP1-09 and CP1-10 have no verifying test — **BLOCKING**

Two named checkpoint-1 exit criteria — "**Metrics flowing:** the FR-34 core set
visible in Prometheus with one option enabled" and "**Traces flowing:** a single
trace spans receive → reduce → render → send → client morph" — have **zero**
covering evidence.

Every test that touches instrumentation uses a **no-op provider**:
`internal/session/harness_test.go:385` uses `metricnoop.NewMeterProvider()` and
`:404` uses `tracenoop.NewTracerProvider()`. That exercises the call sites and
asserts nothing about what is emitted.

**Repro:**

```
grep -rn 'sdk/metric\|sdkmetric\|ManualReader\|metricdata' --include='*.go' .   # no matches
grep -rn 'SpanRecorder\|tracetest\|RecordedSpans'          --include='*.go' .   # no matches
```

No metric reader and no span recorder exist in the module, so no test can
distinguish "the counter was incremented with the right name and labels" from
"a method was called". The FR-36 claim in particular — that **one** trace spans
the whole path *including the client morph attached by causal id* — is the
non-obvious one, and it is entirely unasserted.

QA-1 did not build the instrument: `go.opentelemetry.io/otel/sdk/metric` is not
a dependency of this module and adding one to `go.mod` is DEV-1's decision, not
QA's.

### D-4 — MEDIUM — the documented Coalesce backpressure stage is not wired

RFC-0001 §7.4 (line 599) specifies the ladder's first stage:

| Stage | Signal | Threshold |
|---|---|---|
| **Coalesce** | `unacked_depth` | **≥ `ack_window/2` = 8** |

`internal/session/window.go:54` implements exactly that:

```go
func (w *window) coalescing() bool { return w.depth() >= w.cap/2 }
```

**Nothing calls it.** `Actor.emitPatch` (`actor.go:377`) branches on
`a.win.full()` — depth ≥ 16 — instead. So coalescing engages only when the
window is *completely* full, never at half, and the three-stage ladder collapses
to two.

Consequence: a client falling behind gets no early relief, the window fills
completely before any collapsing happens, the wire is burstier than designed,
and `gotthlive_patches_coalesced_total` under-reports against the documented
model. The backpressure ladder gates at checkpoint 3, so this is not a
checkpoint-1 block — but it was found only because `staticcheck` flags the dead
method, which is the argument for D-2 in one line.

**Repro:** `grep -rn 'coalescing()' --include='*.go' .` → one definition, zero
call sites.

### D-5 — LOW — `staticcheck` is not clean, so CP1-19 fails as written

```
internal/session/window.go:54:18:      func (*window).coalescing is unused (U1000)
internal/session/harness_test.go:280:16: func (*sink).failWith is unused (U1000)
```

The first is D-4's substance. The second is an unused test helper. NFR-12 and
CP1-19 both say `staticcheck` **clean**, and `staticcheck` is additionally not
installed in the project's own image (§1).

### D-6 — LOW — an unreachable branch in `ParseInbound` whose comment claims otherwise

`internal/protocol/inbound.go:178` — the payload switch's `default:` arm carries:

> `// Reached when the frame carries no payload at all.`

It is not. `KindOf` reports a payload-less frame as `KindUnknown`, and the
`!kind.ClientToServer()` guard at `:111` rejects it first, with reason
`unknown_kind` and close `4002`. The `default:` arm is dead and its message
("frame carries no payload") can never be emitted.

Behaviour is correct; the comment describes a route that does not exist, and a
reader relying on it would mis-model the boundary. QA-1's spec pins the **real**
route so a future edit to the guard cannot silently drop the rejection.

**Repro:** encode `{protocol_version: 1, session_id: <16 B>}` with no payload;
the error is `rejected inbound frame (unknown_kind): payload kind "unknown" is
server-to-client only…`, not the `default:` arm's text.

### D-7 — LOW — the reserved QA directory collides with the C-20 arch cap

`internal/arch/imports_test.go:96` asserts the module's non-`internal` package
set is `ConsistOf("live", "live/livetest")`. `go list ./...` reports a directory
containing only `_test.go` files as a package, so **a suite placed at
`gotth-live/test/` — the directory PRD delivery reserves for QA — fails that
assertion on arrival.**

Verified directly: adding a single `_test.go` file at `gotth-live/test/` makes
`go list` emit `…/gotth-live/test` and the arch spec red.

This is a genuine tension, not a mistake: C-20's own text says "exactly `live`
and `live/livetest`", and DEV-1 implemented it faithfully. DEV-3 hit the same
wall and resolved it by giving `examples/counter` its own `go.mod` (documented
in that file's header). QA-1 resolved it by nesting under `test/internal/`,
which the walk skips.

**No fix is requested.** It is recorded so the next person to reach for
`gotth-live/test/` finds the answer instead of the failure. If L9-1 prefers, the
assertion could exempt packages with no non-test Go files — those cannot be
imported by a consumer, which is what the cap is actually protecting.

### D-8 — INFO — `live.Event.ID` is settable and silently discarded

`live/core.go:69` exports `Event.ID` with the doc "the server-minted causal
identifier". An application constructing an `Event` to pass to `Emitter` can set
it, and it is dropped twice: `live/app.go:198` omits it when building the
internal event, and `Actor.emitter` assigns `ev.ID = 0` unconditionally.

Harmless today because nothing depends on it. It becomes the obvious wrong fix
for D-1 — a reader who finds the field will assume setting it works.

### D-9 — LOW — no CSP verification exists anywhere (part of CP1-13)

FR-49 requires the runtime to function under `script-src 'self'; object-src
'none'`, and CP1-13 requires it "verified under strict CSP".

```
grep -rn "Content-Security-Policy\|script-src" --include='*.go' --include='*.js' --include='*.mjs' .
```

returns **nothing** — not in the library, not in the client runtime, not in the
counter example, not in any test. The no-eval half of CP1-13 is genuinely
covered (`client/test/bundle.test.mjs:30` asserts
`/\beval\s*\(|new Function/` does not match the shipped bundle, and it passes);
the CSP half has no evidence and no mechanism. Verifying it needs a browser,
which is D-10's problem too.

### D-10 — LOW — the leak test asserts goroutines but not RSS (part of CP1-16)

CP1-16 requires 10k cycles to "return **goroutine count and RSS** to baseline
within stated tolerance". `internal/wsx/wsx_test.go:466` asserts
`settled() <= baseline+4` goroutines and `handler.Sessions` reaching zero. RSS
is never sampled. FR-22's gate is QA-2, so closing this is QA-2's; it is
recorded here because the criterion sits in checkpoint 1's list.

---

## 4. Latency — measured, published, and labelled

**These numbers are NOT PRD G1 and must not be quoted as it.** G1 is event→paint
on a LAN, gated at ≤ 50 ms p50 / ≤ 150 ms p99, owned by QA-2, measured in Phase
5 against equivalence-spec §3.6.

### 4.1 What was measured

Protocol-level **event→patch-received**: from the moment the client hands an
encoded `Event` frame to the socket, until the corresponding `Patch` frame has
been read and decoded client-side. It spans refine → authorize → reduce →
render → encode → send → decode, over a real WebSocket through the real
handler.

| | |
|---|---:|
| samples | 300 (after 30 discarded warm-up interactions) |
| min | **21.82 µs** |
| **p50** | **91.86 µs** |
| **p95** | **171.15 µs** |
| **p99** | **463.00 µs** |
| max | **642.93 µs** |
| mean | 103.22 µs |

Hardware and toolchain: `linux/amd64`, 32 CPUs, `go1.26.5`, inside
`dis-gotth-live:latest`. Client and server in one process over loopback.

Reproduce:

```
GOTTHLIVE_SOAK=1 go test ./test/... -v -args -ginkgo.label-filter=latency
```

### 4.2 What it excludes, and why the criterion is still PARTIAL

- **Browser morph and paint.** The other half of "event→paint". Not measured.
- **Any network.** Loopback, one process. G1 says LAN.
- **The counter example.** Measured against QA-1's two-fragment harness app, not
  `examples/counter`, which landed while this was being written.

So the figure is a **floor**: the real event→paint number is this plus a network
round trip plus paint. It is published because checkpoint 1 asks for a measured
number with a method rather than an estimate, and this is a measured number with
a method.

**Disclosure:** the inbound rate limit was lifted for the run
(`MaxEventsPerSecond`/`EventBurst` raised). At the default 50/s the measurement
loop outruns the bucket after the burst and measures the limiter. The token is
taken at ingress, *before* the timed interval — a refused event produces an
`Error`, not a slow `Patch` — so removing it shortens nothing reported here.
Every other limit was left at its default.

### 4.3 Why there is no browser number

No browser is present in either project image, and neither ships one:
`dis-gotth-live` has no node by design, and `dis-gotth-live-bench` adds node and
nothing else. Obtaining one requires editing `.dis/Dockerfile.bench`, which is
not QA-1's file, or a per-run download that nobody could reproduce from the
committed tree.

Recorded for whoever does the checkpoint-2 DOM conformance suite: **chromium
is one apt line away** on the bench image's base —
`Debian 13 (trixie)`, `chromium 151.0.7922.71-1~deb13u1` from
`deb.debian.org/debian-security trixie-security/main`. That is the cheap path to
CP1-01's browser half, FR-25/FR-26's matrix, and D-9's CSP check at once.

Note also that today's morph evidence is 15 node specs against
`client/test/dom.mjs`, a hand-written DOM shim. That file is **honest about
it** — its header says it is not a browser, explains that it exists to check
traversal and node identity, and defers focus, caret, scroll, IME and media to
checkpoint 2's Playwright matrix. That is a reasonable split and is not a
defect; it does mean CP1-01's "in a real browser" clause has no evidence yet.

---

## 5. C-15…C-20 — discharge walk

| # | Owner | Due | Status |
|---|---|---|---|
| **C-15** | DEV-1 | Phase 1, **before checkpoint 1** | **NOT DISCHARGED** — see D-2. No CI exists. `gen.sh --check` is implemented and works, which is half of what C-15 asked for; the job that runs it does not exist. |
| **C-16** | DEV-1 + QA-1 | Phase 1 | **DISCHARGED.** `internal/protocol/conformance_test.go` walks the `Frame` descriptor over the `payload` oneof and asserts every member has both a `ParseInbound` case and a generated `Refine*`; `schema_test.go` carries H-1's and H-4's descriptor walks, and `ListBound` is exported so the table is held complete. QA-1 independently confirmed the H-1 and H-4 walks reject an undeclared enum number and an unbounded repeated field. QA-1 sign-off attaches here. |
| **C-17** | DEV-1 | Phase 1 | **DISCHARGED.** protocol.md §3.1 and `proto/gotthlive/v1/frame.proto` both read 22–23 bytes; §9's table reconciles (18 + 2 + 2–3). |
| **C-18** | DEV-1 | with the FR-25/FR-26 suite | **NOT DUE.** No `playwright-go` in `go.mod`, so nothing is wrong yet; it comes due with checkpoint 2's DOM suite. |
| **C-19** | DEV-1 | Phase 1 | **DISCHARGED.** dependencies.md §5.1 carries the `go.sum` reach, the empty-cache/`GOPROXY=off` evidence, the rejected option (c), and the +1→+3 correction. |
| **C-20** | DEV-1 | checkpoint 1, with `live.Script()` | **DISCHARGED**, and it works — it is what caught QA-1's own directory (D-7). `internal/arch/imports_test.go:96` asserts the exported set via `go list`. |

---

## 6. Sign-off

**QA-1 BLOCKS checkpoint 1.** Merge-block authority is exercised on three
items. None of them is a defect in the core loop; all three are missing
evidence for criteria the PRD names.

**Blocking, must clear before checkpoint 1 signs:**

1. **D-2 — land CI.** Condition C-15, already overdue by its own terms. The job
   C-15 specifies, plus the client size gate (NFR-2), the size ledger (NFR-3),
   the no-eval scan (NFR-4), the exported-identifier count delta (FR-65/CP1-18)
   and `staticcheck` (NFR-12). Add `staticcheck` to `.dis/Dockerfile` while
   there — a criterion the image cannot run is not a criterion.
2. **D-3 — give CP1-09 and CP1-10 a test.** A metric reader and a span recorder,
   and an assertion that the FR-34 core set is emitted with the right names and
   that one trace spans receive → reduce → render → send → client morph joined
   by causal id. Two exit criteria currently rest on no-op providers.
3. **D-5 / CP1-19 — make `staticcheck` clean**, which means resolving D-4 rather
   than deleting the method: either wire `window.coalescing()` at
   `ack_window/2` as RFC §7.4 specifies, or amend §7.4 and say why the ladder is
   two stages.

**Requires a PM-1/L9-1 decision, not a fix (QA-1 does not descope):**

4. **CP1-01 and CP1-08's browser halves, and D-9's CSP check.** All three need a
   browser that no project image has. Either add chromium to the bench image
   (§4.3 gives the exact package) and do them, or amend the checkpoint-1
   criteria to say the browser half lands at checkpoint 2 with the DOM
   conformance matrix. Silently leaving them unchecked is the one option QA-1
   will not sign.

**Pre-registered for checkpoint 2:** **D-1**. QA-1 will block checkpoint 2 on
it. The pending spec is already committed and will go green when the edge is
carried.

**Recorded, not blocking:** D-6, D-7, D-8, D-10.

**What QA-1 affirms.** The protocol boundary, the actor, the provenance chain,
render determinism and the security hooks are in good shape and were tested
adversarially rather than confirmed. 120 independent specs pass under `-race`,
including every H-invariant reachable from a client and every P-property in
protocol.md §7. The codegen is byte-reproducible on demand. The client is 3,874
bytes of a 12,288-byte budget. Those are real results and they are not what
this report blocks on.

---

*Reproduce this report's checks:*

```bash
# from gotth-live/, inside dis-gotth-live:latest
go build ./... && go vet ./... && gofmt -l . && go test -race -count=1 ./...
GOTTHLIVE_SOAK=1 go test -race ./test/... -args -ginkgo.label-filter=soak
GOTTHLIVE_SOAK=1 go test ./test/... -v -args -ginkgo.label-filter=latency
GOTTHLIVE_E2E=1  go test ./test/... -v -args -ginkgo.label-filter=e2e -ginkgo.v
(cd tools && go run ./minify -check)

# from the repository root
docker run --rm -v "$PWD:/workspace" -w /workspace dis-gotth-live:latest \
    bash candace/pkg/gotth/gen.sh --check

# bench image, for the client suite
docker run --rm -v "$PWD:/workspace" -w /workspace dis-gotth-live-bench:latest \
    bash -c 'for f in client/test/*.test.mjs; do node --test "$f"; done'
```

---

## 7. Re-verification, 2026-08-04, against `44f87764`

| | |
|---|---|
| **Trigger** | DEV-1's remediation of the §6 block — five commits, `d554cfed`…`44f87764` |
| **Re-run by** | QA-1, independently; nothing below is quoted from a remediation commit message |
| **Verdict** | **PASS.** 17 criteria pass outright, 1 passes with a question escalated (CP1-10), 1 stays partial and non-blocking by QA-1's judgement (CP1-16) |

### 7.1 The gate now has a single entry point, and it fails when it should

`candace/pkg/gotth/ci.sh` exists, `.github/workflows/gotth-live-checks.yml` runs it in
three contexts, and `staticcheck` is pinned into `.dis/Dockerfile`. QA-1 ran the
script from the repository root and it **exits 0** with one loudly-announced
skip (the node suite, which the library image has no node for, and which the
workflow runs in the bench image).

A green script proves nothing on its own, so each of the six CI-gated
requirements was **mutation-tested** — the mutation applied to a throwaway copy,
the script run, and the named step required to be the one that fails:

| Requirement | Mutation applied | Result |
|---|---|---|
| NFR-12 `staticcheck` | added an unused func to `internal/session` | exit 1, `FAILED: staticcheck (NFR-12)` |
| FR-65 exported surface | added an unledgered exported func to `live` | exit 1, `FAILED: exported surface (FR-65)` |
| FR-7 codegen | edited a committed `.pb.go` header | exit 1, `is not what this generator produces` |
| NFR-4 no-eval | added `eval(…)` to the shipped bundle, creating no new global so only the regex could catch it | exit 1, `PRD NFR-4: no eval, ever` |
| NFR-2 / NFR-3 client size | appended real code to `client/runtime.js`, leaving the artifact stale | exit 1, `gotth-live.min.js is stale` |

One mutation deserves recording because it *correctly* did **not** fail: a
comment-only edit to `runtime.js` left the minified artifact byte-identical and
the check passed. That is right — the gate guards the shipped bytes, not the
source — and it is noted so the result is not mistaken for a gap.

**D-2 and D-5 are closed.**

### 7.2 D-1 — closed, and the edge that must not exist is now asserted too

The fix is two-sided and correct. The library threads the scheduling event
through `execute`/`emitter`; a new `live.Event.Contributing` lets the
application supply the edge only it knows. `live.Event.ID` is now **rejected**
rather than silently dropped, which closes **D-8** by the better of the two
available fixes.

QA-1 verified both directions against the real binary:

- **Positive.** The patch a click causes now carries the click's own event id.
  Asserted as `ContainElement(1)` — the session's first inbound event — rather
  than merely non-empty.
- **Negative, and the one that matters more.** A causal id is session-scoped, so
  event 1 in one session and event 1 in another are different events sharing a
  number. Two tabs were opened against one counter; one clicked. The watching
  tab's fan-out patches are asserted to carry **no** `event_id` and **no**
  `contributing_event_ids` at all. They do not. The example scopes the edge with
  `snap.ChangedBy == to`, which is exactly right.

An invented edge would make provenance confidently wrong, which is worse than
the empty field the defect started as. It is not invented. **D-1 is closed.**

### 7.3 The three browser criteria — answered

Chromium 151.0.7922.71 is in the bench image. QA-1 drives it through a **minimal
CDP client written against the WebSocket library the module already depends
on** — no puppeteer, no npm, no lockfile, no post-install download, so FR-74's
quarantine is untouched.

**CP1-01 — PASS.** Against the real counter binary in a real browser: the value
goes 1 → 2 on a click. Morph rather than replacement is asserted by **node
identity** — an expando set on the live value element before the click is still
there afterwards, which cannot survive an `innerHTML` replace and is the
mechanism every FR-25 case rests on. Focus on the clicked button survives the
patch.

**CP1-13 — PASS.** The counter is fronted by a proxy adding:

```
default-src 'none'; script-src 'self'; style-src 'self'; connect-src 'self';
img-src 'self' data:; object-src 'none'; base-uri 'none'; frame-ancestors 'none'
```

with no `unsafe-inline` and no `unsafe-eval`. The runtime loads, opens its
WebSocket and patches the DOM with **zero** `securitypolicyviolation` events.

Two corrections were needed before that result meant anything, and both are
recorded because each would have produced a false pass:

1. The first draft probed enforcement with `new Function()` through
   `Runtime.evaluate`. CDP evaluation runs in a privileged world that **bypasses
   CSP entirely**, so that probe reports success under any policy. Enforcement
   is now proven by injecting an inline `<script>` through the DOM and asserting
   it was blocked and raised a violation.
2. The proxy changes the browser's `Origin`, and the counter's allowlist is
   derived from its own listen address — so the handshake was refused. That was
   the origin check working. The fix is to start the counter with the proxy's
   origin, not to weaken the check.

**CP1-08 — PASS.** Event→**paint**, 220 interactions:

| | |
|---|---:|
| samples | 220 |
| browser | Chrome/151.0.7922.71 |
| host | linux/amd64, 32 CPU, go1.26.5 |
| min | **1.50 ms** |
| **p50** | **3.20 ms** |
| **p95** | **3.60 ms** |
| **p99** | **4.80 ms** |
| max | 16.10 ms |
| mean | 3.18 ms |

*Method.* `performance.now()` immediately before dispatching a real click on the
+1 button, to `performance.now()` in the **second** `requestAnimationFrame`
callback after a `MutationObserver` observed the counter's text change. rAF runs
before the frame is painted, so the second one is the first callback that can
only run after the frame carrying the change was presented. Spans the whole
loop: click → event frame → refine → authorize → reduce → render → patch frame →
client morph → paint. 30 ms of pacing sits **between** iterations, outside the
timed interval, to stay under the library's default 50 events/s budget. 20
warm-up interactions discarded.

*Labelling.* **Loopback, one host, headless. NOT PRD G1**, which is event→paint
on a **LAN**, gated at ≤50 ms p50 / ≤150 ms p99, owned by QA-2 in Phase 5. This
is checkpoint 1's measured number under the exit criterion that says Phase 1
measures and records while Phase 5 enforces. Run-to-run variance across two runs
was p50 3.20/3.20, p95 3.60/3.80, p99 4.80/4.50 ms.

The protocol-level figure from §4 still stands as the server-side floor and was
re-measured: p50 90.9 µs, p95 153.8 µs, p99 536.8 µs over 300 samples. **D-9 is
closed.**

### 7.4 D-3 — metrics genuinely closed; traces closed with a caveat

`internal/obstest` supplies hand-rolled recording providers and
`live/instrumentation_test.go` asserts against them. QA-1 mutation-tested both
halves rather than reading them:

- **Metrics — real.** Removing the `a.m.EventReceived(ctx, name)` increment
  fails `counts an interaction end to end, with the labels the catalogue
  names`. **CP1-09 passes.**
- **Traces — partly vacuous.** Reparenting the encode span onto
  `context.Background()` — which detaches it and the client morph from the
  transition entirely — leaves the whole `live` suite **green**.

The cause is **D-11**: `obstest` stamps every span it records with the same
`TraceID` unconditionally, so the suite's `Expect(ids).To(HaveLen(1))` — its
"one trace, not four" line — has cardinality one whatever the library does. It
cannot fail. That is worse than a missing assertion, because it is counted as
evidence for the criterion it cannot test.

QA-1 supplied the missing evidence instead of only reporting it
(`test/internal/conformance/trace_test.go`), asserting over the edges `obstest`
records faithfully and nobody read — the parent pointer and the link. Both new
specs fail under the mutation the existing suite survives.

### 7.5 D-12 — a question for PM-1 and L9-1, not a QA verdict

Measured, and published because the FR-36 wording turns on it and nobody had the
number: **the event path is 4 OpenTelemetry traces**, not one —
`gotthlive.origin`, `gotthlive.authorize`, `gotthlive.event`,
`gotthlive.client.morph` are each a root span.

They are **connected and walkable**: QA-1 asserts that every span on the path is
reachable from the transition span by following parents and links, and that the
encode span is a true descendant of the transition. But links join traces; they
do not merge them.

The design is defensible and deliberate — the code says so at the call site.
Two edges *cannot* be parent edges: authorization runs on the read pump before a
transition span exists, and the morph happens in a browser. And **FR-36's own
sentence** asks for "one trace per event" and, in the same breath, for "the
client morph attached via the causal ID carried in the frame" — which is a link,
which is a second trace. The requirement contradicts itself.

QA-1 does not resolve requirement ambiguity by fiat, so **CP1-10 passes** on its
substance — traces flow across the whole path, joined, walkable, with the morph
attached exactly as FR-36's second clause describes — and the wording question
is escalated. Either FR-36 is satisfied as designed and its first clause should
say "one connected trace graph", or the read-pump boundary must become a real
parent (feasible: the span context could ride the mailbox message) and the
morph exception stated explicitly.

### 7.6 D-10 — QA-1's call: open, non-blocking, QA-2's to close

CP1-16 asks 10k connect/disconnect cycles to return **goroutines and RSS** to
baseline. The goroutine half runs and passes at 10k cycles. RSS is still never
sampled.

**QA-1 judges this non-blocking for checkpoint 1** and hands it to QA-2 as a
Phase 3 item. The reasoning, so the call is reviewable rather than a shrug:
FR-22's own gate is **QA-2**, not QA-1; the leak class this criterion exists to
catch — goroutines, timers, retained sessions — *is* covered and passing; and
RSS-to-baseline is a memory-measurement discipline that belongs with G2's
`equivalence-spec §3.6` method, which is QA-2's instrument and Phase 5's number.
Adding a second, weaker RSS measurement here would produce a figure nobody would
trust against the one that matters.

### 7.7 Verdict per criterion, re-issued

| # | Criterion | FR | Was | **Now** |
|---|---|---|---|---|
| CP1-01 | Counter end to end **in a real browser** | — | PARTIAL | **PASS** |
| CP1-02 | Lifecycle: handshake, auth, origin, heartbeat, close codes | FR-8/45/46 | PASS | **PASS** |
| CP1-03 | Wire audit: 100 % parses as `Frame` | FR-3, G5 | PASS | **PASS** |
| CP1-04 | Hostile wire data, typed errors, no partial application | FR-5/13 | PASS | **PASS** |
| CP1-05 | Actor `-race` under concurrent injection | FR-17 | PASS | **PASS** |
| CP1-06 | Determinism helper exists; counter uses it | FR-15 | PASS | **PASS** |
| CP1-07 | Repeated-render byte equality | FR-19 | PASS | **PASS** |
| CP1-08 | Event→paint measured and published | — | PARTIAL | **PASS** |
| CP1-09 | Metrics flowing | FR-34 | FAIL | **PASS** |
| CP1-10 | Traces flowing across the path | FR-36 | FAIL | **PASS** (D-12 escalated) |
| CP1-11 | Provenance resolves; 100 %, zero unknown | FR-41, G4 | PASS | **PASS** (strengthened by D-1) |
| CP1-12 | Client ≤ 12 KB gzip with ledger | NFR-2/3 | PASS | **PASS** (3,874 B, unchanged) |
| CP1-13 | No-eval green; strict CSP verified | NFR-4, FR-49 | PARTIAL | **PASS** |
| CP1-14 | Authorization before every reducer | FR-47 | PASS | **PASS** |
| CP1-15 | Cross-origin attack test | FR-48 | PASS | **PASS** |
| CP1-16 | 10k cycles: goroutines **and RSS** | FR-22 | PARTIAL | **PARTIAL** — non-blocking, QA-2 Phase 3 (D-10) |
| CP1-17 | Pre-generated proto; clean-machine build | FR-7 | PASS | **PASS** |
| CP1-18 | api-surface current; CI reports count delta | FR-65 | FAIL | **PASS** |
| CP1-19 | Toolchain clean incl. `staticcheck` | NFR-12 | FAIL | **PASS** |

### 7.8 Defect ledger, re-issued

| ID | Sev | Status | Note |
|---|---|---|---|
| D-1 | HIGH | **CLOSED** | Fixed two-sidedly; positive and negative arms both asserted against the real binary |
| D-2 | HIGH | **CLOSED** | `ci.sh` + workflow + staticcheck in the image; all six gates mutation-verified |
| D-3 | MED | **CLOSED** | Metrics mutation-verified; traces evidenced, see D-11 |
| D-4 | MED | **CLOSED** | `window.coalescing()` now called at `actor.go:410` and `:683`; the ladder has its first stage back |
| D-5 | LOW | **CLOSED** | `staticcheck ./...` clean, and installed in the image |
| D-6 | LOW | **CLOSED** | The arm is kept deliberately with a corrected comment saying what it is for — a better fix than deleting it |
| D-7 | LOW | **OPEN, no fix requested** | The `test/` vs C-20 collision. Resolved by convention (`test/internal/`), recorded so the next person finds the answer |
| D-8 | INFO | **CLOSED** | `Event.ID` and `Event.At` are now rejected rather than silently dropped |
| D-9 | LOW | **CLOSED** | CSP verified in a real browser under a real policy |
| D-10 | LOW | **OPEN, non-blocking** | RSS in the leak test → QA-2, Phase 3. §7.6 states the reasoning |
| **D-11** | **MED** | **NEW, non-blocking** | `obstest` assigns one `TraceID` to every span, making the existing "one trace, not four" assertion incapable of failing. Repro: reparent the encode span onto `context.Background()` — `go test ./live/` stays green, `./test/... -ginkgo.focus=FR-36` fails. **Owner DEV-1**: assert over `ParentID`, which `obstest` already records |
| **D-12** | **—** | **NEW, question** | FR-36 asks for "one trace per event" and for the morph "attached via the causal ID", which are different things. Measured: the path is 4 traces joined by links. **Owner PM-1 + L9-1**: amend the wording, or make the read-pump boundary a real parent |
| **D-13** | **MED** | **CLOSED** (DEV-1) | Fixed: one `goList(dir, args...)` helper with `Output()` and an explicit `cmd.Stderr` buffer, surfaced only when the command fails. Two specs, both mutation-checked against the old shape — one builds a scratch module with an npm-shaped `node_modules` symlink, one points the helper at a malformed `go.mod`. `bash ci.sh` is green with `bench/` present. The original report follows. `internal/arch` reads `go list` with `CombinedOutput()` and treats every line as a package path. `go list` writes warnings to **stderr**, so a `node_modules` tree with symlinks under the module turns each warning into a phantom exported package and fails C-20 — accusing the author of adding a third exported package. Phase 5's Next.js app guarantees such a tree. §7.11 has the repro. **Owner DEV-1**: `Output()` instead of `CombinedOutput()` in `packages`, `deps` and `firstPartyImports` |

### 7.9 Re-run outputs

| Check | Result |
|---|---|
| `ci.sh` from the repository root | **exit 0**, one announced skip (node suite → bench image) |
| Six CI-gated requirements, mutation-tested | all five mutations caught by the correct named step |
| `go test -race -count=1 ./...` (library image) | all packages ok |
| QA-1 suite, every label, `-race`, bench image | **127 passed, 0 failed, 0 pending, 0 skipped** (47.8 s) |
| QA-1 suite, default labels | ok, 12.3 s |
| node client suite | 3 files, 0 failures |
| `examples/counter` own module | build, vet, `-race` clean |
| `gen.sh --check` | byte-identical |
| client size | 3,874 B gzip of 12,288 (68.5 % headroom) |

### 7.11 D-13 — a latent false failure the re-run caught by accident

The final `ci.sh` run for this sign-off **failed**, and the cause is worth
recording precisely because the committed tree is fine.

While QA-1 was re-verifying, QA-2's Phase 5 bench scaffolding appeared in the
shared worktree as untracked files, including `bench/node_modules` with symlinks.
The `-race` step then failed — not on a test, but on C-20:

```
[FAILED] a third exported package needs a ruling, not a directory.
Expected <[]string | len:8>: [
    "warning: ignoring symlink /workspace/candace/pkg/gotth/bench/node_modules/@gotth-live-bench/chat-next",
    ... (five more warnings) ...
    "live", "live/livetest",
] to consist of <[]string | len:2>: ["live", "live/livetest"]
```

`internal/arch`'s `packages()` runs `go list` with **`CombinedOutput()`** and
treats every non-empty line as a package path. `go list` writes
`warning: ignoring symlink …` to **stderr**, which `CombinedOutput` merges into
stdout, so each warning becomes a phantom exported package. `deps()` and
`firstPartyImports()` have the same shape.

Confirmed both ways in a scratch copy: with `bench/` removed the arch suite is
`ok`, and re-running `go list ./... 2>/dev/null` returns a clean package list.

**The committed tree passes** — `bench/` is untracked, and every gate in §7.9
was run against the tree as committed. But Phase 5's Next.js comparison app
lands a `node_modules` under this module by design (FR-74), so this will fire in
CI, and the message it produces accuses the author of something they did not do.
One-line fix per helper: `Output()` rather than `CombinedOutput()`, surfacing
stderr only on error.

Recorded as **D-13**, MEDIUM, non-blocking for checkpoint 1.

### 7.10 Sign-off

**QA-1 signs off checkpoint 1. The block is lifted.**

The remediation did the harder thing in three places rather than the cheap one:
D-8 was closed by rejecting the field instead of quietly honouring it, D-6 by
keeping the unreachable arm with a correct explanation instead of deleting it,
and D-1 by splitting the edge between the party that knows the scheduling event
and the party that knows the fan-out — which is why the negative arm passes.

Two defects are carried forward and neither blocks:

- **D-11** (DEV-1) — a vacuous assertion in the trace suite. QA-1 has supplied
  non-vacuous coverage in the meantime, so the criterion is evidenced either
  way; the existing assertion should still be fixed rather than left to be
  cited as proof of something it cannot test.
- **D-12** (PM-1 + L9-1) — FR-36's own sentence is self-contradictory. Needs a
  ruling before Phase 5 quotes trace topology in the bench report.

**D-13** is carried too, and is the one to fix soonest despite being MEDIUM:
it is dormant today and becomes a confusing CI failure the moment Phase 5's
bench app lands its `node_modules`. §7.11.

**D-10** stays open as QA-2's Phase 3 item by QA-1's judgement, recorded in
§7.6.

**Pre-registered for checkpoint 2**, unchanged: the FR-25 DOM conformance matrix
across NFR-7's browser set, IME composition (FR-26), and `data-gotth-preserve`
(FR-27). The CDP harness added here is the place they plug in — it already
drives a real browser against a real server with no npm in the path.
