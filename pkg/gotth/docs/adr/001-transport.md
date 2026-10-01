# ADR-001 — Transport: WebSocket

| | |
|---|---|
| **Status** | Accepted (pending L9-1 approval of RFC-0001) |
| **Date** | 2026-08-04 |
| **Author** | DEV-1 (Server Core / Go) |
| **Supersedes** | — |
| **Superseded by** | — |
| **Decides** | PRD §7.2 Q1, PRD R-4 |
| **Evidence base** | [RFC 0000 — prior-art teardown](../rfc/0000-prior-art-teardown.md) |

## Decision

**gotth-live uses a single WebSocket (RFC 6455) per browser tab, carrying binary
liquid proto frames in both directions; SSE + `fetch` is rejected.** The code
names WebSocket directly — there is no `Transport` interface in v1 (review
checklist §1.6, §1.4).

---

## 1. Context

PRD FR-1 requires exactly one long-lived connection per tab. PRD FR-3 requires
every byte **in both directions** to be a liquid proto `Frame`. PRD FR-4 fixes
the frame vocabulary, and it already includes three **client→server** frame
kinds beyond `Event`: `ResyncRequest`, `Ack`, and `ClientTelemetry`. PRD FR-11
requires server-assigned monotonic sequence numbers with client-detected gaps.

That set of requirements is the context, and it is most of the argument.

---

## 2. The decision, argued

### 2.1 FR-3 + FR-4 already presuppose a bidirectional frame channel

SSE is a server→client medium. Under "SSE + fetch", the client→server frames
(`Event`, `Ack`, `ResyncRequest`, `ClientTelemetry`) travel as HTTP request
bodies. Two consequences:

1. **FR-3 is satisfied only by a technicality.** The bytes would still be proto,
   but they would be proto *inside an HTTP request envelope negotiated per
   event*, not frames on the connection FR-1 describes. "One long-lived
   connection" becomes one long-lived *half* connection plus an unbounded number
   of short ones.
2. **`Ack` becomes a request.** The acknowledged-window design this project
   adopts from Blazor (RFC-0001 §7) requires the client to acknowledge a
   sequence number roughly once per render batch. On SSE that is an HTTP POST
   per ack — a request whose entire purpose is flow control, paid for with a
   request round trip.

This is not a preference. A bidirectional protocol on a unidirectional
transport requires a second transport, and the PRD's own frame vocabulary is
bidirectional.

### 2.2 Binary framing without a tax (PRD R-4)

WebSocket carries opcode `0x2` binary payloads natively. `text/event-stream` is
a UTF-8, line-oriented format: a `data:` field cannot contain a raw `0x00`, a
lone `\r`, or a `\n`. Liquid proto over SSE therefore requires either:

- **base64**, costing **+33.3 % on every downstream byte** — and downstream is
  the hot direction, carrying rendered HTML fragments; or
- **protobuf-JSON**, which inflates further, loses the byte-identical re-encode
  property FR-3's wire audit depends on, and puts a JSON codec in the client
  runtime in direct tension with review-checklist §3.2.

There is no third option that keeps FR-3 honest.

### 2.3 The connection budget — verified, and larger than expected

Chromium keeps **two separate socket pools** with **different per-host limits**
([`net/socket/client_socket_pool_manager.cc`](https://source.chromium.org/chromium/chromium/src/+/main:net/socket/client_socket_pool_manager.cc)):

```c
g_max_sockets_per_group = {
    6,    // kNormal      — ordinary HTTP/1.1 requests
    255   // kWebSocket
};
```

with the in-source comment: *"WebSocket connections are long-lived, and should
be treated differently than normal other connections. Use a limit of 255…
Also note that Firefox uses a limit of 200."*

This matters more for gotth-live than for a general framework, because PRD
FR-30/FR-31 require coexistence with plain-HTMX pages that issue ordinary
XHRs. An SSE stream **permanently consumes one of the six** connections the
application's own HTMX requests are competing for; a WebSocket consumes one of
a separate 255. On a page with two live regions and an HTMX-driven table, the
SSE design spends a third of the user's request concurrency on itself.

HTTP/2 multiplexing largely dissolves this (streams, not connections), but
HTTP/2 cannot be assumed end to end: break-and-inspect proxies routinely
downgrade, and this repository's own edge is one hop among several.

### 2.4 The upstream path, ordering, and correlation (checklist §11.9.1)

Under SSE + fetch there are two independent channels, and the protocol has no
defined order between them. Concretely, with an event posted to `POST /live/ev`
and patches arriving on `GET /live/stream`:

- The patch caused by event *E* may arrive on the stream **before** the HTTP
  response to *E* completes, after it, or interleaved with a patch caused by an
  unrelated server effect. Nothing in either protocol orders them.
- FR-11's per-session monotonic sequence would have to be **reconstructed
  across two channels**, and the client's gap detector would have to reason
  about a sequence space it observes through one channel while writing into
  another.
- Correlation requires the client to carry a session identifier in every POST.
  That identifier is an **ambient credential on a state-mutating request** —
  precisely the shape CSRF exploits.

With a single WebSocket, ordering is the transport's guarantee (RFC 6455
delivers messages on a connection in order), the sequence space is one channel,
and correlation is positional.

### 2.5 CSRF and authenticated establishment (FR-46, FR-48, checklist §5.5)

- **WebSocket:** one authenticated establishment. The `Upgrade` request carries
  the application's cookies once; the server validates `Origin` against the
  allowlist (FR-45), validates a handshake token bound to the authenticated
  session (FR-48), binds identity, and only then allocates session state
  (checklist §5.2). After that, **no further ambient-credential request exists**
  — the open connection is the capability. A cross-origin page cannot open a
  WebSocket to us and inherit authority, because `Origin` is set by the browser
  on the upgrade and is not forgeable from script.
- **SSE + fetch:** every event POST is a fresh credentialed request. The posture
  needs `Origin`/`Sec-Fetch-Site` checks **plus** `SameSite` cookie discipline
  **plus** a CSRF token, on every request, forever. Three mechanisms, each of
  which can be got wrong independently, versus one.

This is the strongest security argument in the decision and it is structural,
not incidental.

---

## 3. Alternatives, in their strongest form

### 3.1 SSE (downstream) + `fetch` (upstream) — the serious alternative

**What it does better than WebSocket — honestly:**

1. **Intermediary tolerance.** An SSE stream is an ordinary HTTP response that
   never ends. It traverses proxies, WAFs, and corporate break-and-inspect
   middleboxes that block or strip the `Upgrade` header. This is a real
   deployment advantage, and it is the reason Datastar chose it.
2. **Free reconnection.** `EventSource` reconnects on its own, honours the
   server's `retry:` field, and resends `Last-Event-ID`. gotth-live must
   hand-write reconnect, backoff, and jitter — and pay for it out of the 12,288-byte
   client budget (NFR-2).
3. **Free resumption primitive.** `Last-Event-ID` is a standardised,
   server-controlled resume cursor. Our replacement (ack + gap detection + a
   full `Snapshot`) is more capable but is ours to build and ours to get wrong.
   **It is not a replay window and this line used to say it was**: the outbound
   window retains acknowledgement metadata, never frame bytes, and re-render is
   the only recovery path (`internal/session/window.go:21-30`). PM-1's closure
   ledger §7 item 11, blessed DELETE-NOW, discharged here because the C-35 edit
   pass is in this file.
4. **Native HTTP/2 and HTTP/3.** SSE is just a response body; it multiplexes on
   h2 and h3 with no negotiation. WebSocket over HTTP/2 requires RFC 8441
   Extended CONNECT and over HTTP/3 requires RFC 9220; **support for both is
   patchy across browsers and reverse proxies as of 2026** (nginx has an open
   ticket, and neither is universally deployed). In practice our upgrade lands
   on HTTP/1.1.
5. **Observability by default.** An SSE handler is an ordinary `http.Handler`,
   so `otelhttp`, `promhttp`, access logs, and this monorepo's existing edge
   logging see every event as a request, with no bespoke instrumentation. Our
   WebSocket collapses a session's whole traffic into one access-log line, and
   we must rebuild per-event visibility ourselves (see §5).

**Why it still loses:** §2.1 (the frame vocabulary is bidirectional), §2.2 (the
base64 tax on the hot direction), §2.4 (two channels destroy protocol-level
ordering, which is the substrate provenance sits on), and §2.5 (three CSRF
mechanisms instead of one). Advantages 2, 3, and 5 are things we must *build*;
disadvantages 2.1–2.5 are things we could not *fix*.

### 3.2 Long-polling

**What it does better:** traverses literally everything; no idle-timeout
tuning; trivially load-balanced. It is why Phoenix keeps it as a fallback and
why SignalR falls back to it automatically.

**Why it loses:** a patch's latency floor becomes a poll interval; FR-11's
ordering must be reconstructed across independent requests; per-interaction
request overhead is the dominant cost at dashboard update rates (FR-62's
workload is 53 updates/s per session, per the equivalence spec §3.4). It is a
fallback, not a design. **Not implemented in v1** — see §6, and note that
adding it later is BL-13, not a seam we build now (checklist §1.5).

### 3.3 WebTransport / HTTP/3 datagrams

**What it does better:** true multiplexing, unreliable-datagram option, no
head-of-line blocking, designed for this decade.

**Why it loses:** browser and infrastructure support is not universal in 2026,
and PRD §4 excludes it explicitly (BL-13). Revisit at v1.0, not v0.1.

### 3.4 Hand-rolled RFC 6455 on `http.Hijacker` (no dependency)

**What it does better:** zero Tier-1 dependency in the consumer's `go.mod`
(checklist §10.3), and we only need a strict subset — server side, binary
frames, no extensions, no compression.

**Why it loses:** the subset is still masking, fragmentation reassembly, the
close handshake, control-frame interleaving rules, and payload-length edge
cases — a correctness surface with a published conformance suite (Autobahn)
that we would then owe. That is a poor trade against a zero-transitive-
dependency library (§4.1). **Re-evaluation trigger:** if the chosen library
gains transitive dependencies or is abandoned, in-housing the subset is the
documented exit, and the cost estimate above is the NFR-9 removal cost.

---

## 4. Consequences

### 4.1 Dependency (NFR-9 / FR-69, Tier 1 — lands in users' `go.mod`)

**`github.com/coder/websocket` v1.8.15** (2026-06-15) is the intended choice.

- **What it buys:** a correct, Autobahn-tested RFC 6455 server implementation
  with a **context-aware API** (`Conn.Read(ctx)`, `Conn.Write(ctx, …)`), which
  maps directly onto review-checklist §6.3 ("every blocking operation selects on
  `ctx.Done()`"). The gorilla-style deadline API would force us to translate
  contexts into deadlines by hand at every call site.
- **Maintenance health:** actively released (v1.8.12 → v1.8.15 across 2024–2026);
  it is the maintained continuation of `nhooyr.io/websocket`.
- **Transitive weight:** its `go.mod` declares **zero requirements**. Module
  count delta = 1. Binary-size delta to be measured and quoted in the PR that
  adds it (checklist §10.2).
- **Alternative considered:** `github.com/gorilla/websocket` v1.5.3 — also
  zero-dependency, but last released 2024-06 and deadline-based. Kept as the
  documented fallback if `coder/websocket` regresses.
- **Cost of owning the alternative:** §3.4.

Final Tier-1 approval is L9-1's, in the PR that adds the dependency. This ADR
records the intent and the reasoning, not the approval.

### 4.2 Frame carriage

- One WebSocket message = one liquid proto `Frame`, opcode `0x2` (binary).
  No message concatenates or splits frames; the framing is the transport's.
- No text frames are ever sent. A received text frame is a protocol error
  (close code `PROTOCOL_VIOLATION`, `docs/protocol.md` §7).
- Maximum inbound message size is set at the library level (FR-13) and enforced
  by the transport *before* payload allocation via `Conn.SetReadLimit`.

### 4.3 Compression: disabled by default, with evidence

`permessage-deflate` (RFC 7692) is **off by default**. The reason is a memory
fact, not a preference: `coder/websocket` documents `CompressionContextTakeover`
as costing *"a fixed 32 KB sliding window, a fixed **1.2 MB** `flate.Writer` and
a `sync.Pool` of 40 KB `flate.Reader`s"* per connection
([`compress.go`](https://github.com/coder/websocket/blob/master/compress.go)).
That is **≈26× the entire 45 KiB idle-connection gate** RFC-0001 §6.1 sets.

`CompressionNoContextTakeover` pools the writer (per-message, not
per-connection) and is exposed as an option, off by default. It is the
configuration Phase 5 measures for the PRD R-9 provenance-byte question. This
answers PRD §7.2 Q9.

### 4.4 Intermediaries, idle timeouts, and this repository's edge

- **Caddy** (this monorepo's edge, `caddy/Caddyfile`) proxies WebSocket
  transparently in v2; no directive is required. gotth-live adds **no** edge
  configuration and no firewall or network-policy change (checklist §5.10, and
  the repo-root `CLAUDE.md` trust-model rule).
- **The heartbeat rule:** the `Heartbeat` interval MUST be shorter than the
  shortest idle timeout in the path. Default interval **20 s**, peer-dead
  timeout **50 s** (2.5×), both configurable (FR-12). The docs state the rule
  so an operator behind a 30 s-idle load balancer knows what to change.
- **Buffering is a non-issue**, unlike SSE: no `proxy_buffering off`, no
  `X-Accel-Buffering: no`. This is a small but real operational win, and it is
  the mirror image of SSE's advantage in §3.1.1.
- **HTTP/2 / HTTP/3:** the upgrade lands on HTTP/1.1 in practice. Per §2.3 this
  costs us nothing, because the connection is drawn from the browser's separate
  WebSocket pool.

### 4.5 What we deliberately give up

Stated plainly, because §3.1 is not a strawman:

1. **Middlebox tolerance.** Environments that strip `Upgrade` will not work.
   There is no fallback in v1 (§3.2). The failure is detected and reported
   (§5.1), not silently degraded.
2. **Free reconnect.** We hand-write backoff and jitter, and pay client bytes
   for it (budget: RFC-0001 §10.4's size ledger, "transport" line).
3. **`Last-Event-ID` resumption.** Replaced by the acknowledged-window design
   (RFC-0001 §7), which is strictly more capable — it detects gaps in both
   directions and bounds server memory — but is ours to build and test.
4. **Per-event HTTP observability for free.** One connection is one access-log
   line. Everything per-event must come from our own instrumentation, which is
   why `docs/instrumentation.md` is a Phase-0 deliverable and not a Phase-3
   chore. This is the honest cost of the decision, and it converts an
   externality into a product requirement.

---

## 5. Failure modes (checklist §11.4)

| # | Failure | Detection | Degradation | Recovery |
|---|---|---|---|---|
| F1 | `Upgrade` stripped/blocked by an intermediary | The upgrade request returns a non-101 status; the client runtime surfaces `gotth-live: upgrade refused (status N)` in the console and applies the `data-gotth-status="offline"` attribute on the document element | Page remains fully server-rendered and usable; live regions are static; HTMX regions unaffected (FR-30/31, checklist §7.7) | Retry with backoff. No transport fallback in v1; the operator sees the status and fixes the path |
| F2 | Idle timeout in a proxy silently closes the connection | Heartbeat miss at 50 s (§4.4) on both sides; server reclaims the session (FR-12), client sees a close | Reconnect banner; DOM frozen at last patch | Reconnect → new session → `Snapshot` (RFC-0001 §8). If it recurs, the metric `gotthlive_connections_closed_total{code="heartbeat_timeout"}` rises and the operator shortens the interval |
| F3 | Client is slow; the outbound window fills | Unacked-window depth ≥ high-water mark; write deadline exceeded | Coalesce → synthesize a `SlowClient` event → evict, in that order (RFC-0001 §7.4) | Client reconnects and resyncs. Server memory is bounded at every stage |
| F4 | Sequence gap (a frame lost by a broken intermediary) | Client's next expected `server_seq` ≠ received (FR-11) | Client stops applying patches immediately — it does **not** apply out of order | Client sends `ResyncRequest`; server replies with `Snapshot` at the current state version |
| F5 | Oversize or malformed inbound frame | `SetReadLimit` (pre-allocation) or the `Refine*` boundary | Frame rejected, counted, never partially applied (FR-5) | Typed `Error` frame; repeated violations trip the per-connection rate limit and close (FR-51) |
| F6 | Text frame or non-`Frame` bytes received | Opcode check; proto parse failure | Immediate close, no session state touched | Close code `PROTOCOL_VIOLATION`; counted with a distinguishing label |
| F7 | Server restart / deploy | All connections close | Every client reconnects simultaneously | Reconnect backoff is jittered (RFC-0001 §8.4) specifically to avoid LiveView's documented remount-storm problem (`MAX_RELOADS`/`RELOAD_JITTER` in the teardown §1.5) |

---

## 6. Observability and provenance impact (checklist §11.5)

**The decision strengthens the causal chain rather than trimming it.** One
ordered channel means `event_id → transition_id → patch_id` is carried on a
single sequence space with a single writer (the session actor), so:

- No cross-channel correlation is needed to reconstruct the chain (contrast
  §2.4).
- The `Ack` frame lets the server record **client-confirmed delivery** per
  sequence number — a provenance property none of the four systems in the
  teardown has.
- `ClientTelemetry` carries the client-measured morph duration keyed by
  `patch_id` (FR-29), closing the event→paint span server-side (FR-36) without a
  second HTTP request.

**Nothing in this ADR trims provenance**, so FR-43's ADR-with-measurements
requirement does not apply.

The cost, per §4.5.4, is that per-event HTTP-layer observability is not free.
`docs/instrumentation.md` is the compensating deliverable.

---

## 7. Exit criteria — measurable, with falsification (checklist §11.6, §11.7)

| # | Criterion | Method | Falsifies the decision if |
|---|---|---|---|
| X1 | Event→paint p50 ≤ 50 ms, p99 ≤ 150 ms on LAN, counter + chat | Equivalence spec §3.2, Phase 5 (recorded from Phase 1) | p99 > 150 ms attributable to transport framing rather than reduce/render |
| X2 | Transport's share of the client bundle ≤ **1,600 bytes gzipped** | NFR-3 subsystem ledger, `gzip -9` over the minified single file, per-subsystem attribution by build flag | > 1,600 B and no other subsystem under budget |
| X3 | Transport's share of **retained** idle memory ≤ **13,759 B/connection**, excluding TLS. **Adopted 2026-08-05 by L9-1 — §7.2 is the ruling, §7.1 the derivation it adopts.** **Maps to exactly five lines of RFC-0001 §6.2**: WebSocket read buffer (**512**) + WebSocket **write** buffer (**1,024**) + WebSocket conn struct (**2,370**, measured) + the **conn read-pump** goroutine stack (**8,192**, the one line still an estimate, and bounded above rather than settled — §7.2.3) + its runtime `g` (**410**, measured) = **12,508 B**, plus §6.1.2's 10 % ⇒ **13,759 B, 9.1 % headroom**. The session actor's stack is *not* transport and is not counted here | Equivalence spec §3.6, Idle, N=1000, with the library's own allocation isolated by a no-op-session harness, read off §3.6's **secondaries** (`/gc/heap/live:bytes`, `/memory/classes/heap/stacks:bytes`, `runtime.malg`) or RFC §6.3's per-component heap profile — **not** the unforced steady-state cgroup figure, which is a different quantity (§7.2.4) | > 13,759 B of **retained** per-connection transport state, i.e. the library allocates per-connection transport state we cannot pool |
| X4 | Zero non-`Frame` bytes on the wire, both directions | FR-3 wire audit over the full example suite | any byte that is not a `Frame` |
| X5 | Cross-origin page cannot establish a session or inject an event | FR-48 attack test | a session is established from a disallowed origin |
| X6 | Upgrade succeeds through this repo's Caddy edge with no Caddyfile change | Phase 2 integration test against a local Caddy with the repo's common snippets | a Caddyfile change is required |

X3's isolation method is the part most likely to be argued with; RFC-0001 §6.3
defines it.

**X3 is a derived number, and the binding rule is that it stays one** (condition
C-14). It is not an independently chosen ceiling that happens to sit near a
composition estimate: it is 12,508 B — the sum of five named RFC-0001 §6.2
lines — plus §6.1.2's 10 %, and it acquired its previous value (16,384 B) because
advisory A-7 forced the mapping and the mapping showed the 12 KB ceiling before
that had already been breached by the design it was meant to bound. A ceiling
that is quietly false is worse than a looser one that is true, which is why it
was raised then and why it is **lowered now** — the same rule, run in the other
direction, on the lines the transport actually pays.

Three consequences follow, and they are the condition:

1. **If any of the five lines moves, X3 and §6.2 change in the same PR.** The
   live risk was **O2**: if `coder/websocket` allocated a per-connection
   goroutine beyond our own two, that would be another ~8,192 B of stack plus a
   runtime `g`, which breaches X3 outright and moves §6.2's non-heap subtotal
   with it. **O2 is now closed by measurement and it closed in our favour** — §9.
2. **X3 never becomes the looser of the two.** If the measurement lands under
   the estimate, X3 ratchets down with §6.1.2's rule rather than being left
   generous — a transport ceiling with slack in it stops constraining the
   transport.
3. **A change to X3 quotes the arithmetic**, not just the new figure, because
   the whole value of a derived number is that a reader can check it. The
   current arithmetic is **512 + 1,024 + 2,370 + 8,192 + 410 = 12,508**, against
   **13,759** — five lines, of which two are code constants, two are measured and
   one is an estimate that §7.2.3 bounds from above rather than settles.

### 7.1 X3 re-derived — 2026-08-04/05, condition C-14 discharged

> **ADOPTED 2026-08-05. The ruling is §7.2 and this section is the derivation it
> adopts, kept as DEV-1 wrote it** — including the sentence that says it awaited
> a ruling, because what a proposal claimed before it was ruled on is part of the
> record. Two of its figures are re-checked against the settled campaign in
> §7.2.2 and one of its supporting numbers moved; the arithmetic did not.

`5a2ca417` moved the first of X3's four named lines — the WebSocket read buffer,
from net/http's 4,096 B to this library's 512 B — and neither X3 nor RFC-0001
§6.2 moved with it. That is C-14(1) breached, and L9-1 found it as **C-35**. The
arithmetic, restated with every line at its current value and every line's basis
named:

| Line | Was | Now | Basis |
|---|---:|---:|---|
| WebSocket **read** buffer | 4,096 | **512** | `internal/wsx/hijack.go: readBufferBytes`, `5a2ca417`. A code constant, not an estimate |
| WebSocket **write** buffer | *no line* | **1,024** | `writeBufferBytes`, same commit. **X3 never had this line and neither did §6.2** — the transport retains net/http's `bufio.Writer` as well as its reader (`conn.bw`), which the G2 baseline found at §5.2 and which was 4,096 B for the entire life of this criterion |
| WebSocket conn struct | 2,000 | **2,370** | **MEASURED** — `ce52d2f9`'s per-component heap profile, `websocket.Accept`/`newConn`. This closes ADR §9's **O2**-adjacent open line and RFC §16 **O7**'s conn-struct estimate, and it closes it slightly *against* us |
| conn read-pump goroutine stack | 8,192 | **8,192** | **STILL AN ESTIMATE, and the only one left.** See below |
| its runtime `g` | ≈500 | **410** | **MEASURED** — `runtime.malg`, 820 B for the two descriptors, halved |
| **derived total** | **14,788** | **12,508** | |

**The one line that is still an estimate, and why it was not measured here.**
§3.6's secondary measures `/memory/classes/heap/stacks:bytes`, which is *both*
goroutines together plus the stack allocator's span accounting: 25,215 B/session
before `9f88d75e`, 12,812 B after, and 12,780 B after `5a2ca417`. X3 counts the
read pump's stack and explicitly excludes the actor's, and that class cannot be
split. Splitting it needs the per-goroutine stack probe `70abe339` built
(`memsrv -probe`), which this campaign did not run per arm. **It is named as
owed rather than guessed at**, and it is the line most likely to come *down*:
12,780 B for two goroutines is below 2 × 8,192, so at least one of the two is
already under this estimate.

**The ratchet, C-14(2), applied and shown.** 12,508 B under a 16,384 B ceiling
is **23.7 % slack**, against the 9.7 % this condition exists to hold — "a
transport ceiling with slack in it stops constraining the transport". C-14(2)
sends the ratchet to §6.1.2's rule, which is *the measured value plus 10 %*:

```
12,508 × 1.1 = 13,758.8  ⇒  X3 = 13,759 B/connection
```

**Where this differs from L9-1's arithmetic in C-35, stated rather than
quietly substituted.** C-35 computes `512 + 2,000 + 8,192 + 500 = 11,204`. This
computes 12,508. The whole difference is three lines and each is a deliberate
choice:

- **+1,024**, the write buffer. C-35 restated X3's *four* named lines. But the
  transport has always retained a writer as well as a reader, X3 has always been
  "the transport's share of idle memory", and a criterion that omits a real
  transport line is the "quietly false ceiling" §7 says is worse than a loose
  one. Adding it makes X3 **tighter in truth and larger in arithmetic**, which
  is the direction that costs us.
- **+370**, the conn struct at its measured 2,370 rather than its 2,000
  estimate. C-14(3) says the value of a derived number is that a reader can
  check it; using a measurement in preference to the estimate it replaced is
  that rule working.
- **−90**, the runtime `g` at its measured 410 rather than ≈500.

Taking C-35's 11,204 would set a ceiling below a line the transport actually
pays, which would be a *false* ratchet — the failure mode §7 already names.

**RFC-0001 §6.2 moves in the same landing**, per C-14(1): §6.2.4 carries the
composition as of `5a2ca417`. §6.2.2's estimate table is deliberately **not**
edited — its own preamble says it is "kept whole and unedited, because a
corrected number with the wrong one deleted tells a reader nothing about how far
off it was" — so the current lines live in a new dated subsection and §6.2.2
gains a pointer to it. That is a departure from C-35's falsifier, which asks for
§6.2.2's row to read 512, and it is argued rather than taken silently.

### 7.2 The ruling on §7.1 — L9-1, 2026-08-05 (C-14 / C-35(a))

**ADOPTED at 13,759 B/connection**, with the write buffer as X3's fifth line, the
§6.2.2 departure upheld, one clarification of what X3 bounds that makes it
tighter, and two conditions.

| Question put to me | Ruling |
|---|---|
| Adopt or refuse **13,759 B** | **ADOPT.** X3 is 13,759 B/connection. 16,384 B is withdrawn and §7's row and closing arithmetic are edited in this landing |
| The **write buffer** as X3's fifth line | **ADOPTED, and §7.1 is right for the reason it gives.** `writeBufferBytes = 1,024` (`internal/wsx/hijack.go:64`, read at HEAD, not carried from a commit subject) is retained by `websocket.Conn` for the connection's life. A criterion called "the transport's share of idle memory" that omits a buffer the transport holds is the quietly-false ceiling §7 names. My own C-35 arithmetic of `512 + 2,000 + 8,192 + 500 = 11,204` **omitted it, and I was wrong to restate four lines when the transport pays five.** Adopting it costs us 1,024 B of ceiling, which is the direction that tells you the correction is honest |
| Editing **RFC §6.2.2** vs pointing at it | **DEPARTURE UPHELD**, with one addition. §6.2.2's preamble is a rule I approved and it is a good one; overwriting the estimate destroys the only record of how far off it was. But a reader of §6.2.2 alone could see the marked read-buffer row and still not learn that the table is **missing a line entirely**. RFC §6.2.2 therefore gains one sentence *below* the table saying so and pointing at §6.2.4/§6.2.5; the table is untouched. C-35's falsifier asked for §6.2.2's row to read 512 and is **discharged in substance rather than to the letter**, and this sentence is why |
| Rule against the **settled campaign**, not `ae61f325`'s snapshot (PM-1 §8 item 5) | **Done — §7.2.2.** One of §7.1's supporting figures moved and is restated. Neither of its two measured lines moved, for a reason worth stating precisely: the campaign did not re-measure them |

#### 7.2.1 The adopted arithmetic, with every line's basis and date

| Line | B | Basis, checked at HEAD (`d66e4953` + docs) |
|---|---:|---|
| WebSocket **read** buffer | **512** | `internal/wsx/hijack.go:63`, `readBufferBytes`. Code constant, read in the tree |
| WebSocket **write** buffer | **1,024** | `internal/wsx/hijack.go:64`, `writeBufferBytes`. Code constant, read in the tree |
| WebSocket conn struct | **2,370** | **MEASURED**, `ce52d2f9`, RFC §6.3 per-component heap profile (g2-baseline §7.5), `websocket.Accept`/`newConn`. **Not re-measured at the shipping tree** — §7.2.2 |
| conn read-pump goroutine stack | **8,192** | **ESTIMATE.** Bounded above rather than settled — §7.2.3 — and the bound is what makes adopting it safe |
| its runtime `g` | **410** | **MEASURED**, `ce52d2f9`, same profile: `runtime.malg` 820 B for two descriptors, halved. **Not re-measured** — §7.2.2 |
| **composition** | **12,508** | |
| **X3, = composition × 1.1 per §6.1.2's ratchet** | **13,759** | 12,508 × 1.1 = 13,758.8 |

#### 7.2.2 What the settled campaign did and did not move

PM-1's ledger §8 item 5 required me to rule against the settled campaign rather
than against the snapshot §7.1 was written from. Checked, line by line:

- **The conn struct (2,370) and the runtime `g` (410) stand, and the campaign
  did not touch them.** Both come from `ce52d2f9`'s per-component heap profile.
  g2-baseline **§9.10.11.3** states that RFC §6.3's per-component profile was
  **not re-run at `d66e4953`** and that nothing is estimated from its absence. So
  the correct statement is not "they survived the re-measurement" — it is that
  **no re-measurement of these two lines exists**, they are quoted at the tree
  and date they were taken, and re-deriving them at the shipping tree is part of
  what §9.10.11.3 already owes.
- **One supporting figure in §7.1 did move.** §7.1 argues from the combined
  goroutine-stack class at 12,780 B/session (`5a2ca417`, campaign `c2`). The
  settled campaign measures that class at the shipping tree: **12,943 B/session**
  (obs on, 5 runs, §9.10.6) and **13,681 B/session** (obs off, 2 runs, §9.10.10).
  §7.1's argument survives the move — both are still below 2 × 8,192 — and
  §7.2.3 uses the settled figures rather than the superseded one.
- **O2 is closed by the same campaign**, and it closed favourably: exactly
  **2.0** goroutines per session in all five obs-on runs and both obs-off runs at
  `d66e4953` (`(2007−7)/1000` and `(2006−6)/1000`, recomputed by me from the
  published `introspect-m0/mn.json`). `coder/websocket` allocates no third
  per-connection goroutine. See §9.

#### 7.2.3 The last estimate: bounded from above, which is what a ceiling needs

§7.1 leaves the read-pump stack an estimate and names `memsrv -probe` as what
would settle it. **Adopting a ceiling whose largest term is unmeasured needs one
property and only one: that the term cannot be larger than the estimate.** That
property is derivable from the settled campaign's own published readings, so the
ruling does not wait on a new run.

Recomputed by me from `docs/bench/data/g2-baseline/remeasure-2026-08-05/`, as
`(mn − m0)/N` over `/memory/classes/heap/stacks:bytes`, N = 1000 — the same
arithmetic §3.6 uses for its secondaries, and it reproduces DEV-1's published
medians exactly (12,943 and 13,681), which is the provenance check:

| Cell at `d66e4953` | per-session stacks, by run | median |
|---|---|---:|
| obs on, 5 runs | 12,845.1 · 12,877.8 · 12,943.4 · 13,008.9 · 13,041.7 | **12,943.4** |
| obs off, 2 runs | 13,336.6 · 14,024.7 | **13,680.7** |
| `M(0)` stack reserve, whole process | 557,056 – 655,360 B over 6–7 goroutines | — |

Go allocates goroutine stacks in powers of two from a 2,048 B minimum, and there
are exactly two per session (§7.2.2). The largest per-session reading anywhere in
either cell is **14,024.7 B**; adding back the *entire* `M(0)` stack reserve as
if all of it had been consumed and none replenished — 655.4 B/session, the most
the subtraction could hide — gives **≤ 14,680 B for the two stacks together**.

- Two stacks at 8,192 B would be **16,384 B**. Excluded, in every run of both
  cells. **At most one of the two goroutines is at 8,192 B and the other is at
  most 4,096 B.**
- One stack at 16,384 B with the other at Go's 2,048 B minimum would be
  **18,432 B**. Excluded by a wider margin. **No per-session goroutine exceeds
  8,192 B.**

**So 8,192 B is a true upper bound on the read pump's stack at the shipping
tree**, whichever of the two goroutines is the deep one. X3 at 13,759 B cannot be
false on account of this line; it can only be *loose* on account of it, and
C-14(2) is what collects that later. The exact value is still unmeasured and I am
not rounding it away: if the read pump is the 4,096 B one, the composition is
8,412 B and X3 ratchets to 9,253 B.

> **C-45 — the read-pump stack line. Owner: DEV-1. Not blocking this gate; owed
> before Phase 5 quotes X3.** Run `memsrv -probe` (built at `70abe339`;
> `diag.sh --cells on-probe`) at the shipping tree and report, per goroutine, the
> observed relocation count and the used-bytes lower bound, with the deepest
> frame list. **Falsifier, stated honestly about the instrument:** if the read
> pump's used-bytes lower bound exceeds 4,096 B the line is 8,192 B and X3 is
> confirmed at 13,759; if it does not, *the probe as built cannot settle it* —
> it reports lower bounds and relocations, not allocated sizes — and a second
> instrument is owed rather than an inference. Either way X3 and RFC §6.2 move in
> the same PR, per C-14(1), and the arithmetic is quoted, per C-14(3).

#### 7.2.4 What X3 bounds — the clarification that makes it falsifiable

Advisory A-7 raised X3's "units and boundaries" and the answer given then was the
mapping to §6.2's lines. Ratcheting the ceiling from 16,384 B to 13,759 B exposes
the half of A-7 that the mapping alone did not settle, and leaving it would make
the tighter ceiling *unfalsifiable-or-false* rather than binding:

**X3's five lines are retained bytes. §3.6's headline is not.** Three of the five
— 512 + 1,024 + 2,370 = **3,906 B** — are GC heap, and equivalence-spec §3.6's
headline is unforced steady state, which under `GOGC=100` carries up to one
further copy of every heap line (RFC §6.2.2 models exactly this, as a separate
derived line). A no-op-session harness measured §3.6's headline way would
therefore be expected to read up to **16,414 B** for a transport that is paying
exactly its budget — 2,655 B *over* a 13,759 B ceiling, with nothing wrong.

So X3's method column now names the quantity: **retained** per-connection
transport bytes, read from §3.6's own secondaries (`/gc/heap/live:bytes`,
`/memory/classes/heap/stacks:bytes`, `runtime.malg`) under the no-op-session
harness, or from RFC §6.3's per-component heap profile, which is the instrument
that produced two of the five lines in the first place.

**This is not a benchmark-method change and §6.1.2 is not engaged.** §6.1.2
governs the 46,080 B G2 gate, which this does not touch; no X3 measurement
exists, so nothing is being fitted to a number; equivalence-spec §3.6 is not
amended, extended or reinterpreted — X3 reads two of the secondaries §3.6 already
requires to be reported alongside. And the clarification makes X3 **tighter**: a
ceiling on retained bytes is strictly harder to satisfy than the same figure
against a number inflated by GC headroom.

#### 7.2.5 The line the composition still omits, and the check that adopting anyway is safe

§7.1's own argument — that a ceiling below a line the transport actually pays is
a false ratchet — has to be run against §7.1. `ce52d2f9`'s profile carries
**`context.WithCancel` × 2 ≈ 1,200 B/session**, a line §6.2 does not have. One of
those two is the transport's: `internal/wsx/conn.go:78` derives a cancellable
context per connection and holds it for the session's life. X3's five lines do
not include it.

The split between the transport's context and the actor's is **not measured** —
the profile grouped them — so rather than assume a half, take the worst case
against myself and give the transport **both**:

```
12,508 + 1,200 = 13,708  ≤  13,759
```

**The adopted ceiling survives the strongest form of its own objection, by 51 B.**
That is thin, and stating it is the point: the *next* per-connection transport
line found unbudgeted breaks X3, and C-14(1) then requires a re-derivation rather
than a quiet exceedance.

> **C-46 — the context line. Owner: DEV-1. Not blocking this gate.** At the next
> X3 re-derivation (C-45's PR, or the next `internal/wsx` change that moves a
> line), the per-connection `context.WithCancel` enters X3's composition and
> RFC §6.2 with its own measured value, split from the actor's rather than
> estimated. **Falsifier:** X3's arithmetic has six lines, the sixth cites a
> measurement at the tree that produced it, and RFC §6.2 carries the same line.

#### 7.2.6 What I refused

**Refused: 11,204 B.** It is my own C-35 arithmetic and §7.1 is right to decline
it. It omits the write buffer, so it would set the ceiling below a line the
transport pays — the exact failure §7 names, committed by the reviewer who wrote
the rule. Recorded here rather than dropped, because a review condition that
turns out to be wrong is worth more on the record than off it.

**Refused: waiting for the probe before adopting.** The tree currently carries a
ceiling marked stale, which is the state C-14 exists to prevent; §7.2.3 supplies
the bound that makes adoption safe without it; and a ruling deferred to a
measurement nobody has scheduled is how 16,384 B survived a landing that moved
one of its lines.

---

## 8. Scope (checklist §11.8)

Single-node v1. This ADR introduces no clustering seam, no session store, no
build tool, and — per checklist §1.6 — **no `Transport` interface**. FR-2 asked
for one; **PM-1 amended it in PRD v0.2** to require the isolation property
instead, verified by the same architecture test, which is what RFC-0001 §3.5
delivers and `internal/arch` checks. The interface itself is BL-13, for when a
second transport exists and can say what shape it should be.

---

## 9. Open questions (checklist §11.10)

Distinct from the §11.9 hard parts, all of which are answered above or in
RFC-0001.

| # | Question | Owner | Needed by |
|---|---|---|---|
| O1 | Exact binary-size delta of `coder/websocket` (checklist §10.2 requires a measured number, and the host running this analysis has no Go toolchain) | DEV-1 | the PR that adds the dependency |
| O2 | ~~Whether `coder/websocket` allocates a per-connection goroutine beyond our own two~~ **CLOSED by measurement, 2026-08-05, and it closed in our favour: it does not.** Exactly **2.0** goroutines per session in all five obs-on runs and both obs-off runs at `d66e4953` (g2-baseline §9.10.6, §9.10.10; recomputed from the published `introspect-m0/mn.json` as `(2007−7)/1000` and `(2006−6)/1000`), and in all eleven runs of §5.5's three cells before that. C-14(1)'s named live risk is retired | DEV-1 | ~~Phase 1 memory baseline~~ — done |
| O3 | Whether to enable `CompressionNoContextTakeover` by default after Phase 5 measures the provenance-byte cost (PRD R-9) | QA-2 → ADR-002 if it changes | Phase 5 |
| O4 | Chromium's 255-per-host WebSocket pool is verified from source; Firefox's 200 is stated only in Chromium's comment, and Safari's limit is unverified | DEV-1 | not blocking — no design depends on the exact number, only on the pools being separate |

---

## Changelog

### Checkpoint 3 — 2026-08-05: X3 adopted at 13,759 B (L9-1 ruling, C-14 / C-35(a))

| Condition | Closure |
|---|---|
| **C-35(a)** — `readBufferBytes = 512` moved one of X3's four named lines and neither X3 nor RFC §6.2 moved with it | **CLOSED by ruling.** §7.1 (DEV-1's re-derivation) is **ADOPTED**: X3 = **13,759 B/connection**, from `512 + 1,024 + 2,370 + 8,192 + 410 = 12,508` plus §6.1.2's 10 %. §7's X3 row, its "four lines" framing and its closing arithmetic are edited in this landing rather than left marked stale; RFC §6.2 moves with it (§6.2.5), per C-14(1). The ruling, its two departures and its two new conditions are **§7.2** |
| **C-14** — X3 stays derived, ratchets down, and quotes its arithmetic | **Exercised, in the ratchet direction.** 16,384 B → 13,759 B. C-14(2) is what forced it: 12,508 B under 16,384 B is 31 % slack against the 9.7 % this condition exists to hold |

**Three things ruled on, and one refused:** the **write buffer** enters X3 as its
fifth line (my own C-35 arithmetic omitted it and was wrong); **RFC §6.2.2 is
pointed at rather than edited**, upheld, with one added sentence below its table
so a reader learns the table is missing a line entirely; **X3's quantity is
stated** — retained bytes, not §3.6's unforced steady state, which under
`GOGC=100` carries up to one further copy of X3's 3,906 B of heap lines (§7.2.4).
Refused: **11,204 B**, and waiting for the per-goroutine probe before adopting.

**New conditions: C-45** (the read-pump stack, `memsrv -probe`, DEV-1) and
**C-46** (the per-connection `context.WithCancel` line, DEV-1). Neither blocks
this gate; both bind the next re-derivation.

**Also in this pass:** **O2 is closed by measurement** — no third per-connection
goroutine, exactly 2.0 per session in seven runs at `d66e4953` — and §3.1.3's
*"ack + replay window"* is corrected to what the code does (PM-1 closure ledger
§7 item 11).

### Phase 1, module init — 2026-08-04: condition C-14 closed

| Condition | Closure |
|---|---|
| **C-14** — X3 rose 12 KB → 16,384 B under advisory A-7, accepted, on condition that its derived status is binding | §7 gains the note that makes it binding. X3 is stated as **derived** — 4,096 + 2,000 + 8,192 + 500 = 14,788, plus 9.7 % headroom — with three consequences written down: any of the four §6.2 lines moving changes X3 and §6.2 **in the same PR**, X3 **never becomes the looser of the two** and ratchets down under §6.1.2 if the measurement comes in under the estimate, and a change to X3 quotes the arithmetic rather than only the new figure. **O2 is named as the live risk**: a third per-connection goroutine in `coder/websocket` would breach X3 outright and move §6.2's non-heap subtotal with it. |

**Also corrected in the same pass**, being the same stale framing C-1 fixed in
RFC §3.5: §8 said FR-2 "asks for a transport interface" and called the conflict
an open item for L9-1. PM-1 amended FR-2 in PRD v0.2 and the architecture test
now ships, so §8 states the delivered position and points at BL-13.

### Cycle 2 — 2026-08-04, in response to [L9-1 cycle-1 review](../rfc/001-review-cycle-1.md)

ADR-001 was **APPROVED** with three advisories. All three are applied; none is
declined.

| Objection | Change |
|---|---|
| **A-5** — stale cross-reference | §4.5.2 now cites RFC-0001 **§10.4**'s size ledger, not §9 (which is panic recovery). |
| **A-6** — singular metric name | §5 F2 now uses `gotthlive_connections_closed_total` (plural), matching RFC §7.4 and instrumentation §2.2. |
| **A-7** — X3's units and boundaries | §7 X3 now names the exact four RFC §6.2 lines it maps to and states the estimated total. **Applying the advisory changed the number**: read buffer + conn struct + read-pump stack + its `g` is 14,788 B, so the original 12 KB ceiling was already breached by the design it was meant to bound. X3 is raised to **16,384 B** with 9.7 % headroom, and the session actor's stack is explicitly excluded as not-transport. O2 updated to match. |

Also updated, following from RFC-0001's **B-3** TLS decision: §4.3's compression
comparison now reads "≈26× the entire 45 KiB idle-connection gate" rather than
"≈19× … 64 KB", because the gate figure changed (RFC §6.1). The compression
decision itself is unchanged and unaffected — 1.2 MB per connection is
disqualifying against any budget in this range.
