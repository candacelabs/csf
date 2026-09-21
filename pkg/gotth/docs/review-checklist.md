# gotth-live review checklist

Owner: L9-1 (Principal Engineer / reviewer). This is the document a reviewer
**walks top to bottom for every PR**. Sections 1–10 are the code-PR pass;
section 11 is the separate pass for design docs (RFC/ADR). Every item is a
yes/no question. "I'll take their word for it" is not an answer — each check
names how to verify it.

Referenced documents (the checklist remains the operative floor if a document
ever drifts from the built tree):

| Document | Status |
|---|---|
| `docs/adr/001-transport.md` — WS vs SSE+fetch | approved; WebSocket |
| `docs/rfc/001-architecture.md` — budgets, memory ceiling | approved at Phase 0 review cycle 2 |
| `docs/instrumentation.md` — metrics/traces/logs spec | landed |
| `docs/protocol.md` + `proto/` — Liquid Proto schema | landed; current v1 mapping |
| `candace/pkg/liquidproto/` — same module as this library | landed, authoritative Liquid Proto runtime, schema, and generator |
| `go/CLAUDE.md` (repo root) — house Go + testing conventions | landed |

---

## Review philosophy

Review protects three things, in this order: **the functional core**, **the
provenance chain**, and **the budgets**. Everything else is negotiable in a
comment thread. Those three are not.

The bar is not "is this code good." The bar is "does this preserve the
properties that make the library worth building." A correct, well-tested,
beautifully factored change that makes one DOM patch untraceable is a return.

### Automatic return — stop reviewing, send it back

Do not continue down the checklist. Do not leave forty inline nits. Name the
violation, cite the section, return the PR.

1. **Purity violation.** A reducer that does I/O, reads a clock, reads
   randomness, starts a goroutine, or mutates state reachable from another
   session. A render function that is not a function of state alone.
2. **Provenance gap.** Any path that can change the DOM without producing a
   traceable patch record, or a frame that drops `event_id` / `transition_id` /
   `patch_id`. Stripping provenance "to save bytes" requires an ADR with
   measurements (§3, §11) — not a PR comment.
3. **Budget breach.** Client runtime over 12,288 bytes gzipped, or a measured
   regression past the latency budget, without an accompanying ADR that moves
   the budget.
4. **Diff > ~400 changed lines with no prior design note.** Size is measured
   as defined below. The remedy is a design note, or a split, not an
   explanation in the PR description.
5. **JSON or any non-liquid-proto side channel on the wire.** Including
   "just for debugging." The transport's own establishment and closure are
   carved out under §3.1 and are not a side channel; anything else is.
6. **Multi-node anything.** Clustering, session migration, CRDTs, gossip,
   shared session stores, plugin systems, custom build tooling. v1 is
   single-node. A PR that adds a seam "for when we cluster" is this violation.

### Verdict vocabulary

Use exactly one. Ambiguous verdicts produce re-review loops.

- **Return** — an automatic-return item fired, or the PR needs restructuring
  rather than edits. Not a judgment of the author; a routing decision.
- **Block** — a specific check failed; the PR is otherwise sound. Name the
  check ID (e.g. `§6.2`). QA-1 (correctness) and QA-2 (resilience/perf) hold
  independent merge-block authority; their block stands until they clear it,
  and the reviewer cannot override it.
- **Merge with nits** — all checks pass; remaining comments are
  non-blocking and explicitly labeled `nit:`.

### Fast pass — so process doesn't smother small fixes

A PR is fast-pass eligible only if **all** of the following hold:

- [ ] ≤ 25 changed lines, excluding generated files.
- [ ] No behavior change: typos, comments, godoc wording, log message text,
      formatting, test-name changes, dead-code deletion.
- [ ] Touches **none** of: `proto/`, the transport packages, the session actor,
      reducers, the render path, the client runtime, auth/authz, or CI budget
      gates.
- [ ] `go build ./...` and `go test ./...` are green.

Fast pass = read the diff, confirm the four boxes, approve. Skip §1–§11.

**Fast pass never applies to protocol, security, provenance, or the client
runtime, regardless of diff size.** A three-line change to the origin check
gets the full §5 pass. Small diffs in dangerous places are where the bugs are.

### How diff size is counted

- **Counts:** hand-written Go, templ sources, `.proto` schema, client runtime
  source, CI/budget config, docs prose.
- **Does not count:** generated output (`*.pb.go`, `*_refine.pb.go`,
  `*_templ.go`), golden/fixture files, `go.sum`.
- **But:** a large generated diff means the *schema* diff that produced it is
  the real change — review that at full weight. Generated volume is never a
  place to hide scope.
- Hand-edited generated files are an automatic return on their own: regenerate
  instead.
- 400 is a threshold for requiring a **prior** design note, not a hard cap. A
  700-line PR with a merged design note that predicted it is fine. A 410-line
  PR with a design note written after the code is not "prior."

---

## Gate 0 — triage (do this in five minutes, before reading code)

- [ ] **0.1** PR description links the work item and its acceptance criteria.
- [ ] **0.2** Diff size counted (per the rule above); if > ~400, a design note
      merged **before** the code exists and is linked.
- [ ] **0.3** No automatic-return item is visible from the file list alone
      (new `encoding/json` import in a transport package, new dependency in
      `go.mod`, client runtime file touched, `docker-compose*`/host config
      touched — this repo is live infrastructure and gotth-live changes belong
      under `gotth-live/` only).
- [ ] **0.4** CI is green, including the client size gate and `-race`. Red CI
      is the author's to fix before review time is spent.
- [ ] **0.5** If the PR touches protocol, security, or the client runtime,
      QA sign-off is present or explicitly pending (§8.6).

If 0.1–0.4 pass, continue. Otherwise return with the reason.

---

## 1. Scope & size

*Applies to: every PR.*

- [ ] **1.1** The diff is ≤ ~400 counted lines, **or** a prior merged design
      note covers it and is linked.
- [ ] **1.2** The linked work item states acceptance criteria, and the diff
      satisfies them.
- [ ] **1.3** No scope smuggling: every change in the diff traces to an
      acceptance criterion. Drive-by refactors, opportunistic renames, and
      "while I was in here" cleanups go to the backlog as separate items,
      however correct they are.
- [ ] **1.4** No speculative abstraction. Every new interface, generic type
      parameter, options struct, callback hook, or registry has **≥ 2 real
      call sites in this PR**, or a written justification in the PR
      description naming the concrete second consumer and when it lands.
      "Makes it testable" is a justification only when the test is in the diff.
- [ ] **1.5** Nothing multi-node: no clustering, session migration, CRDTs,
      distributed locks, external session stores, plugin systems, or custom
      build tooling. v1 is single-node and the code should read like it
      believes that.
- [ ] **1.6** No transport-abstraction hedging while ADR-001 is open. Either
      the PR does not touch transport, or it names the transport it assumes and
      cites the ADR state. A `Transport` interface with one implementation is
      subject to 1.4 like any other one-call-site abstraction — hedging an
      undecided ADR is not a justification.
- [ ] **1.7** Public API surface added is the minimum the criteria require.
      Exported symbols are permanent; unexported is the default.

---

## 2. Functional core

*Applies to: any PR touching reducers, state types, render, or effects.*

The rule: **pure reducers `(state, event) → (state, effects)`; render a pure
function of state; effects are data, executed only at the actor boundary.**

- [ ] **2.1** Reducers perform no I/O. Verify structurally, not by eyeballing:
      the reducer package's transitive import set is checked against an
      allowlist by a test (`go list -deps ./...` compared to the allowlist).
      If that test does not exist yet, the PR that adds the first reducer adds
      it. Banned in reducer packages: `net`, `net/http`, `os`, `database/*`,
      `time` (see 2.2), `math/rand`, `crypto/rand`, `log`, any client SDK.
- [ ] **2.2** No clocks. Reducers do not call `time.Now()`. Time enters as a
      field on the event, stamped at the actor boundary.
- [ ] **2.3** No randomness. IDs, tokens, and nonces are generated at the
      boundary and arrive on the event, never minted inside a reducer.
- [ ] **2.4** No goroutines, no channel operations, no `select`, no
      `sync.*` inside reducers.
- [ ] **2.5** No map-iteration nondeterminism reaching state or output. If a
      reducer ranges a map to build a slice, ordering is imposed (sorted key,
      or an explicit order field). Two runs on identical input produce
      byte-identical state and effects — assert this, don't assume it.
- [ ] **2.6** Reducers do not mutate their input state in place, and do not
      retain aliases into caller-owned slices/maps/`[]byte`. Copy on write or
      return new values. (Aliasing is the quiet way a "pure" reducer becomes
      impure — and note the Liquid Proto fine print: generated `Validate*`
      functions validate in place and do not copy `[]byte`. `ParseInbound`
      therefore copies immutable scalar snapshots after validation, and
      `Envelope` returns a deep clone; mutation tests must keep that boundary.)
- [ ] **2.7** Render is a pure function of state: same state → same HTML,
      no I/O, no clock, no session-external reads. Template helpers obey the
      same rule.
- [ ] **2.8** Effects are declared as **data** (values a test can assert on),
      not closures capturing live handles, and not performed inline. The
      reducer returns them; the actor executes them.
- [ ] **2.9** No shared mutable state across sessions. Package-level `var`s
      that are not immutable configuration are a return. No session's state is
      reachable from another session's goroutine.
- [ ] **2.10** State transitions are replayable, and a property test asserts
      it: `fold(initialState, events) == state` for the reducer under change.
      A new reducer without this property test is incomplete.
- [ ] **2.11** State is serializable for resync (see also §3.5). Any new state
      field either round-trips through the resync encoding or is explicitly
      documented as derived-and-recomputed-on-resync. Silent unserializable
      fields (funcs, channels, live handles) in state are a return.

---

## 3. Protocol (liquid proto)

*Applies to: any PR touching `proto/`, framing, encode/decode, or anything
that reaches the wire.*

- [ ] **3.1** Every wire interaction that carries application meaning — upstream
      events, downstream patches, heartbeat, resync, error — is a liquid proto
      frame. No exceptions for debugging.
      **Carve-out (amended 2026-08-04, L9-1):** the transport's own
      *establishment* and *closure* are not frames and cannot be — RFC 6455's
      opening handshake is HTTP and its close frame is a code plus a reason, on
      any transport. The carve-out is limited to those two, and it carries two
      obligations: anything application-meaningful the handshake negotiates
      (e.g. a protocol version) must be **re-asserted in-band** in the first
      frame and validated there, so the handshake is never the source of truth;
      and the wire audit must state how establishment and close bytes are
      accounted for, rather than letting "zero non-`Frame` bytes" quietly mean
      "zero except the ones we didn't count."
- [ ] **3.2** No JSON side channel. Verify: no `encoding/json` (or
      equivalent) import in transport/framing packages; no `JSON.parse` /
      `JSON.stringify` on wire payloads in the client runtime. Config files
      and non-wire tooling are unaffected.
- [ ] **3.3** Liquid Proto predicates are present on frame fields that carry an
      invariant, via `pkg/liquidproto`'s canonical `(candace.liquid.v1.field)` option
      (extension 51234). Concretely: identifier fields non-empty and length-capped,
      sequence numbers non-negative, payload sizes bounded, enums
      range-checked. A new frame field with an invariant expressed only in a
      comment is a block.
- [ ] **3.4** Inbound frames cross the generated `Validate*` boundary at
      ingress — handlers take the closed `protocol.IInbound` variants, never a
      decoded frame that has not passed validation. Generated validators do
      not recurse into nested messages or repeated message elements, so the
      reviewer confirms `ValidateFrame`, the matched payload's validator, and
      every repeated element validator all run at the edge. The corresponding
      outbound check is `ValidateOutbound`, immediately before encoding.
- [ ] **3.5** Causal IDs are present and propagated end to end:
      `event_id` → `transition_id` → `patch_id`. Every downstream patch frame
      names the transition that produced it; every transition names the event
      that caused it. A frame type that cannot carry the chain needs an ADR,
      not a `// TODO`.
- [ ] **3.6** Causal IDs survive every hop in this diff — decode, reducer
      dispatch, render, patch construction, encode, client apply. Grep the new
      code path for the point where the ID would be dropped; there usually is
      one.
- [ ] **3.7** Schema changes are versioned and backward-noted: field numbers
      never reused or renumbered, deleted fields `reserved`, the PR description
      states the compatibility impact (additive / breaking / wire-compatible),
      and a breaking change carries a version bump and a migration note in
      `docs/protocol.md`.
- [ ] **3.8** Conformance vectors updated when framing or semantics changed
      (see §8.5).
- [ ] **3.9** Generated code is regenerated by the committed generation script,
      not hand-edited, and the regeneration is reproducible (re-running
      produces no diff).

---

## 4. Observability & provenance

*Applies to: every PR that adds or changes a code path.*

Observability is a product feature here, not instrumentation debt. "Tests pass,
I'll add metrics later" is a block.

- [ ] **4.1** New code paths emit metrics, traces, and structured logs per
      `docs/instrumentation.md`. Until that spec lands, the floor is: every
      new frame type is counted; every new failure mode increments an error
      metric with a distinguishing label; every new blocking operation is
      timed.
- [ ] **4.2** Spans carry the causal IDs (`event_id`, `transition_id`,
      `patch_id`) as attributes, so a trace can be joined to a patch and back
      to the originating event. A span that describes patch work without the
      patch ID is a block.
- [ ] **4.3** Span hierarchy is intact: the event→transition→render→patch
      chain is a connected trace, not orphan spans. Context is threaded, not
      re-rooted (`context.Background()` mid-path is a block unless the PR
      explains the detachment).
- [ ] **4.4** **No code path can mutate the DOM without a traceable patch
      record.** On the client: the apply/morph function is the single DOM
      mutation entry point and takes a patch carrying its ID. On the server: no
      write reaches the socket except through the framer. A second write path,
      however small, is an automatic return.
- [ ] **4.5** Self-reported telemetry is externally verifiable — a counter's
      truth can be confirmed from outside the process (wire capture, log
      correlation, or a test that drives real traffic and asserts the counter),
      not only by the code that increments it. A metric that only the
      incrementing code can vouch for is not evidence.
- [ ] **4.6** Cardinality is bounded: no per-session, per-user, or per-event ID
      used as a **metric label**. Causal IDs belong in traces and logs, never
      in Prometheus label values.
- [ ] **4.7** Log levels are honest: per-event logging is debug or sampled;
      error level means an operator should act. New per-connection logs are
      rate-limited or sampled.
- [ ] **4.8** Metric/span names follow the existing naming scheme and are
      documented in the same PR (§9).

---

## 5. Security

*Applies to: every PR touching connection establishment, event ingress, auth,
or anything reachable before authentication.*

- [ ] **5.1** **Origin validated** on connection establishment against an
      explicit allowlist supplied by the embedding application. No wildcard
      default, no "reflect the Origin header," no empty-Origin pass. Verify a
      negative test exists (bad origin → rejected before any session state is
      allocated).
- [ ] **5.2** **Connection establishment is authenticated** before the session
      actor is spawned or any per-session memory is allocated. Authentication
      after the goroutine starts is both a security bug and a DoS vector.
- [ ] **5.3** **Per-event authorization hook is invoked on every event path,
      with no bypass.** Structural, not by convention: authz is invoked at the
      single mailbox ingress point, so a newly added event type cannot skip it.
      Verify by adding-an-event thought experiment — if a new event kind can be
      handled without passing the hook, the design is wrong.
- [ ] **5.4** Unknown/unhandled event kinds are **default-deny**, not
      default-ignore and not default-allow.
- [ ] **5.5** CSRF posture unchanged or improved, and the PR states which. The
      posture differs by transport (WS upgrade vs SSE + fetch POST), so the
      claim must name the transport and the mechanism (origin check,
      `SameSite`, token). "Not applicable" needs a sentence of why.
- [ ] **5.6** No secrets in logs or traces: no session tokens, cookies,
      `Authorization` headers, or auth-decision inputs. Specific to this
      system — **no full state snapshots and no raw frame payload bodies at
      default log levels**; state and payloads are the realistic leak vector
      once resync and frame logging exist. Redaction is applied at the logging
      boundary, not left to callers.
- [ ] **5.7** Untrusted input is bounded before allocation: frame size caps,
      mailbox depth caps, header/field length caps enforced at ingress (§3.3
      predicates are the preferred mechanism).
- [ ] **5.8** Rendered HTML escapes user-controlled values by default; any
      raw-HTML escape hatch is explicit, narrow, and justified in the PR.
- [ ] **5.9** Error responses to the client do not leak internals (stack
      traces, internal IDs beyond the causal chain, dependency errors).
- [ ] **5.10** No change to host firewall, network policy, or repo-root
      infrastructure files. gotth-live is a library; it does not touch this
      monorepo's live infrastructure.

---

## 6. Concurrency

*Applies to: any PR touching the session actor, mailbox, transport pumps, or
goroutine lifecycle.*

The rule: **one goroutine + mailbox per session. The actor is the lock.**

- [ ] **6.1** The mailbox is the **only** entry to session state. No exported
      method, callback, or test helper reads or writes session state from
      another goroutine.
- [ ] **6.2** No locks guarding session state. A `sync.Mutex` protecting
      session state means the actor model was abandoned — return. (Mutexes in
      genuinely shared, non-session infrastructure — a registry of sessions, a
      metrics collector — are fine and should be visibly scoped to that.)
- [ ] **6.3** Context cancellation is propagated: every blocking operation
      selects on `ctx.Done()`; no unbounded blocking send or receive; no
      `context.TODO()` in production paths.
- [ ] **6.4** Goroutine lifecycle is owned: every goroutine started in this
      diff has a named owner, a defined stop condition, and a place that waits
      for it. Fire-and-forget `go func()` is a block.
- [ ] **6.5** Leak test present for new goroutines — the test drives
      start→stop and asserts no goroutine remains. (If this needs a helper
      library, §10 applies.)
- [ ] **6.6** Backpressure behavior is **stated in the PR description** and
      implemented deliberately: mailbox bounded (with the bound named), and the
      full-mailbox policy explicit — block, drop-with-metric, or evict. Silent
      unbounded buffering is a block.
- [ ] **6.7** Slow-client behavior is defined for any change to the write
      path: write deadline, outbound queue bound, and eviction policy, with a
      metric on eviction. A slow client must not be able to grow server memory
      without bound.
- [ ] **6.8** Shutdown is graceful and ordered: in-flight effects settle or are
      cancelled, the connection is closed with a protocol close frame, and the
      session is deregistered exactly once.
- [ ] **6.9** `go test -race ./...` is clean (§8.4), and the race detector
      actually exercises the new concurrency (a race-clean run that never
      started the goroutine proves nothing).

---

## 7. Client runtime

*Applies to: any PR touching the client runtime source or its build/embed.*

- [ ] **7.1** **Size budget recorded in the PR: gzipped bytes of the shipped
      single file, with the number written down, not just "under budget."**
      Operative definition: ≤ **12,288 bytes** = `gzip -9` over the minified
      single-file bundle. CI's gate is the authority; a change to the
      definition or the number requires an ADR.
- [ ] **7.2** The delta versus `main` is reported when the PR adds client code,
      so budget consumption is visible before the ceiling is hit.
- [ ] **7.3** No `eval`, no `new Function`, no `setTimeout("string")`, no
      dynamic import of remote code. Grep, don't assume.
- [ ] **7.4** No runtime dependencies: nothing fetched from a CDN, no npm
      package shipped, no polyfill loader. The file is self-contained.
- [ ] **7.5** Still a single file, embedded and served by the library
      (`go:embed`), so users get no build step, no npm, no bundler config.
- [ ] **7.6** DOM mutation goes through the single patch-apply path (§4.4);
      no ad-hoc `innerHTML` writes elsewhere in the runtime.
- [ ] **7.7** Degrades sanely without JS where applicable: HTMX-interop and
      static views still render server-side; the page is not blank and does not
      throw. Progressive enhancement is claimed only where it's true.
- [ ] **7.8** Reconnect/resync behavior is stated for changes to the connection
      lifecycle: backoff policy, resume-vs-full-resync decision, and what the
      user sees while disconnected.
- [ ] **7.9** No new global namespace pollution; the runtime exposes one
      namespaced entry point.

---

## 8. Testing

*Applies to: every PR. House conventions: `go/CLAUDE.md` (repo root).*

- [ ] **8.1** Behavior tests use **Ginkgo v2 + Gomega**; interface mocks use
      **`go.uber.org/mock/gomock`**; concise **table-driven stdlib tests** are
      used where they express the case more directly than a spec. Mixed styles
      are fine when each is the clearer choice — arbitrary deviation is not.
- [ ] **8.2** Tests assert **behavior**, not implementation shape. A test that
      only re-states the code (asserting a mock was called with what the code
      obviously passes) is not coverage.
- [ ] **8.3** The new failure modes are tested, not just the happy path:
      rejection paths, cancellation, full mailbox, slow client, malformed
      frame, unauthorized event.
- [ ] **8.4** `go test -race ./...` clean, and the new code path is actually
      executed under it.
- [ ] **8.5** Conformance suite updated whenever the protocol was touched
      (§3): new/changed frames have vectors; encode→decode round-trip and
      causal-chain preservation are asserted.
- [ ] **8.6** QA sign-off present: **QA-1** for correctness, **QA-2** for
      resilience/perf, on PRs in their domain. Both hold merge-block authority;
      an unresolved QA block cannot be overridden by the reviewer.
- [ ] **8.7** Property test for replayability where reducers changed (§2.10).
- [ ] **8.8** Performance-sensitive changes carry a measurement, not an
      intuition. Hot-path changes state the effect on the event→paint budget
      (≤ 50 ms p50 LAN, ≤ 150 ms p99); per-session allocation changes state the
      effect on idle-connection memory against the RFC target (and where the
      RFC has not yet set it, record the measured current number so Phase 5 has
      a baseline).
- [ ] **8.9** No flaky-by-construction tests: no wall-clock sleeps as
      synchronization, no dependence on goroutine scheduling order, no reliance
      on map iteration order.

---

## 9. Docs

*Applies to: every PR. "Docs in a follow-up" is not accepted.*

- [ ] **9.1** Docs updated **in the same PR** as the behavior they describe.
- [ ] **9.2** Godoc on every new exported symbol: what it does, its
      preconditions, and its concurrency contract (safe to call from which
      goroutine?). For a library this is the API contract, not decoration.
- [ ] **9.3** Examples still build and, where they are runnable, still run
      (`go build ./...`, `go vet ./...`, example tests green).
- [ ] **9.4** Protocol changes reflected in `docs/protocol.md`, including the
      compatibility note from §3.7.
- [ ] **9.5** New metrics/spans/log fields documented in
      `docs/instrumentation.md`.
- [ ] **9.6** Security-relevant behavior (origin allowlist, authz hook
      contract, CSRF posture) documented where an integrator will find it —
      the hook's godoc and the security doc, not only the PR description.
- [ ] **9.7** Docs claim only what is true today. No "will support," no
      aspirational capability. (This monorepo already carries an aspirational
      k3s PRD that is not deployed; do not add a second one.)

---

## 10. Dependency review

*Applies to: any PR changing `go.mod`, `go.sum`, or the client runtime's
inputs. There is no purity rule — any dependency is admissible with an
adequate justification. The reviewer judges it; the bar scales with what lands
in users' `go.mod`.*

- [ ] **10.1** A **one-paragraph justification** is in the PR description
      covering all four: (a) what it buys, concretely; (b) maintenance health —
      release cadence, open-issue posture, bus factor; (c) transitive weight —
      module count and binary-size delta, measured, not guessed; (d) cost of
      owning the alternative — what writing/vendoring it ourselves would take
      and why that's the worse trade.
- [ ] **10.2** Measurements are real: `go list -m all` count delta and binary
      size delta before/after are quoted.
- [ ] **10.3** Tier applied correctly, and the bar with it:
      - **Tier 1 — lands in users' `go.mod`** (direct dependency of the public
        module). Highest bar: needs L9-1 explicit approval in the PR thread.
        Every transitive dependency we add is one our users cannot refuse.
      - **Tier 2 — test-only.** Justification required; reviewer approves.
      - **Tier 3 — tooling/CI, outside the module graph.** Note it in the PR;
        no ceremony.
- [ ] **10.4** License is compatible and recorded.
- [ ] **10.5** The dependency does not smuggle in a banned property: no
      client-side npm runtime dependency (§7.4), no JSON wire codec (§3.2), no
      clustering/coordination machinery (§1.5).
- [ ] **10.6** Version is pinned, and `go.sum` is complete and generated (not
      hand-edited).
- [ ] **10.7** For a dependency replacing existing code, the removed code is
      actually removed in the same PR.

---

## 11. Design-doc review (RFC / ADR)

*Applies to: PRs adding or amending a document under `docs/rfc/` or
`docs/adr/`. This is a different pass — do not run §1–§10 on prose.*

- [ ] **11.1** **The decision is stated**, in one sentence, near the top. A
      document that surveys options and ends without choosing is not an ADR;
      return it.
- [ ] **11.2** Status, date, author, and superseded-by are present.
- [ ] **11.3** Alternatives are analyzed with **real** trade-offs — the
      strongest form of each rejected option, including what it does *better*
      than the chosen one. A strawman alternative is a return: it means the
      decision was not actually tested.
- [ ] **11.4** Failure modes are analyzed: what breaks, how it is detected,
      how it degrades, how it recovers. Not "risks: some" — named failures
      with named responses.
- [ ] **11.5** **Observability and provenance impact is stated explicitly.**
      Does the decision preserve the `event_id → transition_id → patch_id`
      chain intact? If it trims provenance, the measurements justifying the
      trim are in the document (§3.5), not promised.
- [ ] **11.6** Exit criteria are **measurable**: numbers with units and a
      method — how it will be measured, on what hardware, and what result
      falsifies the decision. "Should be fast enough" is a return.
- [ ] **11.7** Budgets touched are named with numbers: event→paint p50/p99,
      client size, memory per idle connection.
- [ ] **11.8** Scope stays single-node v1 (§1.5), and the document does not
      quietly introduce a plugin system, build tool, or clustering seam.
- [ ] **11.9** **No deferral on the hard parts.** These may not be answered
      with "TBD," "in a later phase," or "out of scope for this doc" in any
      design document that touches them:
      1. **Transport (ADR-001, WS vs SSE+fetch)** — with actual arguments
         about intermediary behavior (proxies, load balancers, idle timeouts,
         buffering), HTTP/2 and HTTP/3 interaction and stream-count limits,
         reconnect and resume semantics, and **the upstream event path** —
         with SSE that is a separate fetch, and the doc must say how ordering,
         correlation, and CSRF work across two channels.
      2. **State serialization for resync** — what is serialized, what is
         recomputed, size, versioning across a server restart or deploy, and
         what the client sees during resync.
      3. **Per-event authorization** — where the hook sits, what it receives,
         what it can do, and why no event path can bypass it.
      4. **Origin validation and authenticated establishment** — mechanism and
         ordering relative to session allocation.
      5. **Memory ceiling per idle connection** — a target number, chosen with
         reasoning, plus how it will be measured in Phase 5.
      6. **Slow-client eviction** — detection signal, thresholds, policy, and
         the metric that makes it visible.
- [ ] **11.10** The document is honest about what it does not know, and marks
      those as open questions with owners — as distinct from deferring the six
      items above, which is not permitted.

---

## Appendix — reviewer's verdict block

Paste into the PR, filled in. A review that does not produce this block did not
happen.

```
Verdict: Return | Block | Merge with nits

Sections walked: 0, 1, ... (list; N/A sections named with reason)
Diff size (counted): NNN lines   Design note: <link | n/a>
Client runtime: NNNNN bytes gz (budget 12288)  delta vs main: +NNN | n/a
QA-1: pass | block <ref> | n/a      QA-2: pass | block <ref> | n/a

Blocking:
  - §X.Y — <what failed, in one line>

Non-blocking:
  - nit: ...
```
