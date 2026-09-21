# gotth-live instrumentation — metrics, traces, logs, and how they are audited

| | |
|---|---|
| **Status** | Draft for L9-1 review (Phase 0 deliverable) |
| **Date** | 2026-08-04 |
| **Author** | DEV-1 (Server Core / Go) |
| **Companions** | [RFC-0001](rfc/001-architecture.md) · [protocol.md](protocol.md) · [ADR-001](adr/001-transport.md) |
| **Satisfies** | PRD FR-34–FR-38, FR-41–FR-44, NFR-1, G4, G6; review checklist §4, §9.5 |

## 0. Why this is a Phase 0 document and not a Phase 3 chore

ADR-001 §4.5.4 records the honest cost of choosing WebSocket: **one connection is
one access-log line.** Everything per-event that an HTTP-per-interaction design
would get free from `otelhttp` and the access log, we must produce ourselves.

RFC 0000 §6.7 established why that is worth doing rather than merely
compensating for: in all four prior-art systems the dominant production failure
is a *degradation without a signal* — LiveView's silently-disabled change
tracking, Turbo's silently-missed broadcast, Datastar's silently-buffering proxy,
Blazor's silent long-poll fallback. The design rule that follows is stated once
here and applies to every subsystem:

> **Every fallback, coalesce, drop, suppression, rejection, and degradation
> increments a counter and emits a log record. A degradation that is not
> observable is a defect, not a tuning opportunity.**

---

## 1. Naming scheme (checklist §4.8)

| Kind | Convention |
|---|---|
| Metrics | `gotthlive_<subject>_<unit>` — Prometheus base units (`_seconds`, `_bytes`, `_total` for counters). Prefix `gotthlive_` is fixed and not configurable. |
| Spans | `gotthlive.<phase>` — dotted, lower-case |
| Span/log attributes | `gotthlive.<noun>.<field>` — e.g. `gotthlive.patch.id` |
| Log fields | `snake_case`, matching the span attribute's last two segments (`patch_id`) |

Any metric, span, or log field added later is documented in this file **in the
same PR** (checklist §9.5, §4.8).

---

## 2. Metrics (FR-34, FR-35, FR-38)

Enabled by exactly one field: **`Config.Metrics`**, a `metric.MeterProvider`
from `go.opentelemetry.io/otel/metric` (FR-38, api-surface §1.1). The consumer
writes no instrumentation code, registers no collectors, and names no metric.

**It is a `MeterProvider`, not a Prometheus registry.** L9-1 D1 settled OTel as
the single observability family, and dependencies.md §4 records why
`prometheus/client_golang` was rejected: a consumer who wants Prometheus uses
OTel's Prometheus exporter, which is *their* dependency rather than one we
impose on everybody. The `_total`/`_seconds`/`_bytes` naming below is Prometheus
*convention*, which the exporter maps cleanly; it is not a Prometheus
dependency.

### 2.1 Cardinality rules (FR-35, checklist §4.6) — read before adding a label

- **No causal ID is ever a metric label.** Not `session_id`, not `event_id`, not
  `patch_id`, not `transition_id`, not user identity. Those live in traces and
  logs. A PR that adds one is a block.
- **There is no per-session metric mode, and there is no switch for one.**
  Cycle 2 named a `live.WithPerSessionMetrics()` that appears in no ledger, and
  it was at odds with the bullet directly above it: a per-session label *is* a
  causal ID as a label. Per-session attribution is served by traces and by the
  §4A provenance log, both of which carry the full causal chain and neither of
  which multiplies a time series per connection. Re-introducing the knob would
  need an api-surface row and a named consumer (FR-65).
- **`event` label values are bounded by registration**: they take values from
  the identifiers the application registers with `Config`, fixed before the
  first connection, so their cardinality is knowable before deploy. The library
  logs the product at startup when it exceeds 1,000 series. **`fragment` was in
  this bullet and is no longer a metric label at all** — see §2.3's
  `gotthlive_render_duration_seconds` row and
  [rulings-review-wave.md](reviews/rulings-review-wave.md) §2. It survives as
  the `fragment.id` **span attribute** on `gotthlive.render.fragment` (§3.2),
  where it is per-span detail and not a time series, which is the split §2.1
  exists to draw. Note what the removal was *not* decided on: fragment IDs are
  registered, so this bullet's own bound applied and cardinality was never the
  objection.
- **`source` is *not* bounded by registration, because nothing registers an
  effect.** A `live.Effect` is built by the application and its `Source` is
  whatever the application put there; there is no registry to enumerate
  (protocol.md §3.3). Cardinality is therefore bounded **at the metric**: the
  label admits **64 distinct values per process**, after which further values
  collapse to `other` and `gotthlive_source_label_overflow_total` increments.
  Traces and the provenance log carry the full value; only the label is capped.
  This is a weaker guarantee than registration would give, and it is stated
  rather than assumed because the startup warning above cannot see it — a
  `source` explosion shows up as the overflow counter moving, at runtime, which
  is what that counter is for.

### 2.2 Counters

| Metric | Labels | Notes |
|---|---|---|
| `gotthlive_frames_received_total` | `kind` | 8 values, protocol.md §3.1 |
| `gotthlive_frames_sent_total` | `kind` | the framer is the only incrementer (checklist §4.4) |
| `gotthlive_frames_rejected_total` | `reason` | `refine_failed`, `oversize`, `unknown_kind`, `bad_version`, `session_mismatch`, `text_frame`, `enum_domain`, `list_bound`, **`ack_channel_full`** — one per protocol.md §6 enforcement site, plus the bounded ack channel of RFC §3.3, whose full-channel policy is drop-and-count |
| `gotthlive_outbound_validation_failed_total` | `kind` | a constructed frame failed `protocol.ValidateOutbound` (protocol.md §5.3). **Never a client's doing**: the frame was built here, from state this library owns, and it is dropped with an `Error` emitted in its place with the causal chain intact. Any non-zero value is actionable. It used to read *"a library bug, not a client problem"*, which sent the reader to the wrong repository: some of what a frame carries comes from the application — `Event.Contributing` above all — and until D-18 an over-long one reached the outbound validator and was counted here. C-31 moved that rejection to the emit path, where the error names the caller, so what still reaches this counter really is a defect in this library |
| `gotthlive_source_label_overflow_total` | — | an `Origin.source` value arrived after the `source` label reached its 64-value cap and was recorded as `other` (§2.1). It bounds a cardinality risk that registration cannot, because nothing registers an effect |
| `gotthlive_resync_requests_total` | `result` | `snapshot`, `noop`, `rate_limited` (RFC §7.6). A resync is the one client-triggered full re-render, so the split matters: `noop` is the free short circuit when there was no gap, `rate_limited` is the independent bucket refusing to amplify, and only `snapshot` costs a full render |
| `gotthlive_events_received_total` | `event` | registered names only |
| `gotthlive_events_rejected_total` | `reason` | `unauthorized`, `unknown_event`, `mailbox_full`, `rate_limited` |
| `gotthlive_transitions_total` | `result` | `applied`, `no_change`, `panicked` |
| `gotthlive_patches_sent_total` | `op` | `morph`, `append`, `prepend`, `remove` |
| `gotthlive_patches_suppressed_total` | — | identical-render suppression (RFC §5.4) |
| `gotthlive_patches_coalesced_total` | — | backpressure stage 1 (RFC §7.4) |
| `gotthlive_slow_client_events_total` | — | backpressure stage 2 |
| `gotthlive_wire_bytes_total` | `direction` | `in`, `out` |
| `gotthlive_effects_total` | `source`, `result` | `ok`, `error`, `cancelled`, `panicked` |
| `gotthlive_effects_abandoned_total` | — | drain-timeout expiry (RFC §3.6) |
| `gotthlive_panics_total` | `site` | `reduce`, `render`, `effect` |
| `gotthlive_connections_total` | — | opened |
| `gotthlive_connections_closed_total` | `code` | the **lower-case label names** in protocol.md §8.3's `code` column — `normal`, `going_away`, `protocol_violation`, `unsupported_version`, `unauthenticated`, `forbidden_origin`, `unauthorized`, `frame_too_large`, `rate_limited`, `slow_client`, `heartbeat_timeout`, `session_evicted`, `internal_error`, `resync_failed`. Never the numeric code, never the upper-case constant name (A-11) |
| `gotthlive_client_telemetry_dropped_total` | `reason` | `unknown_patch` (protocol.md H-11) — forged or stale client reports |

**Reconnects (FR-34), stated honestly.** Because a session does not outlive its
connection (RFC §8.1), the server cannot correlate a reconnect to its predecessor
without retaining exactly the state we chose not to keep. The reconnect signal is
therefore **derived**: `rate(gotthlive_connections_total)` against
`rate(gotthlive_connections_closed_total{code=~"heartbeat_timeout|normal|going_away"})`.
The library additionally exposes a client-side reconnect counter through the
Phase 4 inspector (FR-44). This derivation is documented rather than a
`gotthlive_reconnects_total` that would have to guess.

### 2.3 Histograms

| Metric | Labels | Notes |
|---|---|---|
| `gotthlive_reduce_duration_seconds` | `event` | |
| `gotthlive_render_duration_seconds` | — | **One observation per render pass**, covering every fragment the pass considered — not one per fragment. It carried a `fragment` label advertised here as *"opt-in; unlabelled by default"*; there was no opt-in to reach (`obs.Metrics.FragmentLabels` was set by nothing, in production or in test), and the label would have been wrong if there had been: the value passed was the **first** fragment in the update list, so a whole-pass duration wore one fragment's name. Field, attribute, branch and label row deleted together — [rulings-review-wave.md](reviews/rulings-review-wave.md) §2. Per-fragment attribution is `gotthlive.render.fragment`'s `fragment.id` span attribute (§3.2) until somebody splits this instrument, which is the pre-registered trigger for the label coming back |
| `gotthlive_encode_duration_seconds` | — | `ValidateOutbound` + `proto.Marshal`, and **not** the write |
| `gotthlive_send_duration_seconds` | — | time in `Conn.Write`, the write-stall signal. **Corrected 2026-08-04 while implementing FR-36's `gotthlive.send` span**: `Framer.Send` did validate, marshal and write in one call, so the actor timed one interval and recorded it into this histogram *and* the one above — two series equal by construction, and this one could not isolate the stall it is named for. `Framer` now exposes `Encode` and `Write` separately and `Send` is their composition, so the single-write-path property (protocol.md **P8**) is unchanged |
| `gotthlive_frame_bytes` | `direction` | |
| `gotthlive_mailbox_depth` | — | sampled per step |
| `gotthlive_outbound_window_depth` | — | RFC §7.4's detection signal — exported as a histogram so degradation is visible *before* eviction |
| `gotthlive_client_morph_duration_seconds` | — | **client-reported** (FR-29); see §2.5 |
| `gotthlive_client_apply_duration_seconds` | — | **client-reported** |
| `gotthlive_resync_bytes` | — | Phase 3 resync-cost criterion |

### 2.4 Gauges, and the honest limit on per-session memory

| Metric | Notes |
|---|---|
| `gotthlive_sessions_active` | |
| `gotthlive_goroutines` | library-owned goroutines; RFC §3.4 predicts `2 × sessions_active` + transient effects |
| `gotthlive_session_tracked_bytes` | **only** structures the library owns and can size exactly: window metadata, mailbox and ack backing arrays, fragment hashes, registry |
| `gotthlive_process_heap_bytes` | `runtime/metrics /memory/classes/heap/objects:bytes`, **undivided** |

**Go has no per-goroutine heap attribution, and this document does not pretend
otherwise.** FR-34 asks for "heap bytes attributable to the session"; the exact
number does not exist in Go. What is exported instead is a pair — an exact figure
for what the library owns, and the **undivided** process heap.

Cycle 1 exported a pre-divided `gotthlive_heap_bytes_per_session_mean`. **Dropped
in cycle 2 on L9-1's advisory A-12**, and the reasoning is right: the value is
derivable by any dashboard from two series we already export, and a pre-divided
series adds nothing except an invitation to the exact misreading the `_mean`
suffix existed to prevent. Export the parts; let the query do the division.
*(QA-2 owns I5; this is L9-1's input applied, not an override — if QA-2 wants the
derived series back it is one recording rule.)*

The authoritative memory number remains the external, out-of-process measurement
of RFC §6.3, which is also how it is audited (§5).

### 2.5 Client-reported metrics are untrusted input

`gotthlive_client_morph_duration_seconds` and `_apply_` come from
`ClientTelemetry` frames, which a hostile client can fabricate. Three defences,
all already in the protocol:

1. `patch_id` must name a patch actually sent to *this* session and still inside
   the ack window (protocol.md H-11); otherwise the report is dropped and counted.
2. The durations are refined `where this <= 60000000` µs, so a garbage value
   cannot skew a histogram to infinity.
3. Both metrics are named `client_*` so no dashboard confuses them with a
   server-measured value, and §5 cross-checks them against the harness's own
   CDP measurement.

---

## 3. Traces (FR-36, FR-38)

Enabled by exactly one field: **`Config.Tracer`**, a `trace.TracerProvider` from
`go.opentelemetry.io/otel/trace` (FR-38, api-surface §1.1). Taking the provider
explicitly rather than reading the OTel global is what lets the library depend
on the `otel/trace` submodule instead of the `otel` root — an architectural
constraint, not a style preference (§3.4).

### 3.1 Span tree per event

*(Amended 2026-08-04, condition **C-29**. The tree drawn here disagreed with the
tracer in three ways, each confirmed by running it. The diagram is the design's
own statement of FR-36's clause 3 enumeration, so a diagram that describes
something other than what ships is not a cosmetic error: it is the
requirement's surface pointing at the wrong graph.)*

*(Amended again 2026-08-04, **FR-36 clause 4 / condition C-30**. All eight
server-side spans now start, and the whole server-side path is one parent chain
under one root. The C-29 drawing is kept below the new one, because the change
it records is the interesting part.)*

This is the graph as **measured**, for one interaction that raises an event and
schedules an effect:

```
gotthlive.parse                        SERVER, ROOT — the read pump, once per
│   attrs: session.id, frame.bytes,    INBOUND FRAME of any kind
│          frame.kind                  proto.Unmarshal + Refine* + §6 invariants
│
└── gotthlive.authorize                started on the read pump, inside parse
    │   attrs: session.id, event.id, event.name
    │
    └── gotthlive.event                started on the ACTOR, and a true child
        │   attrs: session.id, event.id, event.name, event.client_ref,
        │          event.seen_server_seq, transition.id, state.version, result
        ├── gotthlive.reduce           the reducer and its panic guard
        │                              attrs: session.id, event.name,
        │                              transition.id
        ├── gotthlive.render           one render pass
        │   │                          attrs: session.id, transition.id
        │   └── gotthlive.render.fragment   one per fragment CONSIDERED
        │                              attrs: session.id, transition.id,
        │                              fragment.id, fragment.suppressed
        ├── gotthlive.encode           validate + proto.Marshal
        │   │                          attrs: session.id, transition.id,
        │   │                          patch.id, server_seq, frame.bytes,
        │   │                          window.depth
        │   └── gotthlive.send         time in the socket write, and nothing
        │                              else. same attrs as encode
        └── gotthlive.effect.<source>  CHILD, no link. Runs on its own
                                       goroutine and MAY END AFTER ITS PARENT.
                                       attrs: session.id, event.id,
                                       origin.source

gotthlive.client.morph                 SERVER, ROOT — a SECOND sampling
    └── LINK ─▶ gotthlive.encode       decision, deliberately (§3.5)
                                       attrs: session.id, patch.id, server_seq,
                                       morph.duration_us, apply.duration_us,
                                       timing.source

gotthlive.origin                       SERVER, ROOT — server-initiated
├── gotthlive.render                   transitions (mount, timer, pubsub,
│   └── gotthlive.render.fragment      effect result); §3.2, FR-42
└── gotthlive.encode                   A client-triggered RESYNC's origin span
    └── gotthlive.send                 is a CHILD of its authorize span, for
                                       clause 4's reason.
```

**The path is one parent chain, rooted at `gotthlive.parse`.** Until C-30 it was
three roots — `authorize`, `event`, `client.morph` — and the C-29 drawing above
this one recorded the first two as roots joined by a link:

```
gotthlive.authorize        parent=ROOT              links=0
gotthlive.event            parent=ROOT              links=1  link->gotthlive.authorize
gotthlive.effect.qa.probe  parent=gotthlive.event   links=0
gotthlive.encode           parent=gotthlive.event   links=0
gotthlive.client.morph     parent=ROOT              links=1  link->gotthlive.encode
gotthlive.origin           parent=ROOT              links=0
gotthlive.encode           parent=gotthlive.origin  links=0
```

The link's *shape* was defensible and its arithmetic was not: a link leaves both
ends sampler roots, `ParentBased` does not follow links, and at §3.5's own
default that produced **0 of 300** interactions recording `authorize` and
`event` together. FR-36 clause 4 is the ruling that followed, and the mechanism
is the one already in the code — the `obs.SpanRef` the ingress carries. An
**ended span is still a valid parent**: a parent-child edge asserts that the
parent's work caused the child's, not that the parent's clock encloses it. So
`obs.Tracer.StartChildOf` reconstructs the reference's `SpanContext`, puts it in
the context, and the transition descends from the authorization that admitted
it.

`gotthlive.parse` sits above `authorize` for the same reason and by the same
mechanism — lexically, this time, because both run on the read pump. It is
opened for **every inbound frame**, not only events: an ack and a heartbeat are
parsed too, and a parse span that appeared only for the frames that turned out
to be events would be a measurement of parsing that excludes most of it. Its
`gotthlive.frame.kind` attribute is what tells an interaction's trace from an
acknowledgement's.

**Link site 1 is gone, and clause 3's enumeration is now ONE site.** C-29 struck
the effect-span site and left two: `event ─▶ authorize` and
`client.morph ─▶ encode`. Clause 4 removed the first by making the edge a real
parent, which clause 3 calls *"always permitted"*. The remaining site is §3.3's,
and it is the last one.

**Effect spans are nested, not linked, and that is correct.** Cycle-1 text said
they are *"linked, not nested, because an effect may finish after the event span
closes"*. Both halves fail: measured, an effect span's parent is
`gotthlive.event` and it carries **zero links**; and the stated reason is not a
reason, because OpenTelemetry nowhere requires a child to end before its parent.
Measured mid-flight: `event.Ended=true effect.Ended=false` — a nested span
outliving its parent, observed rather than argued.

**`gotthlive.render.fragment` now has a constant.** C-29 left it undeclared on
purpose — *"a constant nothing starts is one more thing that reads as
implemented"* — and said whoever starts the span declares it in the same change.
`obs.SpanRenderFragment` is that declaration.

**How `gotthlive.render.fragment` gets a span without the render package
importing a tracer.** An architecture test forbids anything reachable from
`internal/render` from importing a clock, a logger or the outside world, and
`internal/obs` imports `log/slog` and `time`. So the renderer takes a
`render.FragmentObserver` — a function value, the same pattern
`protocol.WriteFunc` uses for the same reason — installed once per session by
the actor and left **nil** when tracing is off. A nil observer costs one branch
per fragment, which is what §4.2 requires.

**One span per fragment *considered*, not per fragment updated.** A suppressed
fragment (identical bytes, RFC §5.4) and a fragment whose render panicked both
get a span: the first cost the render anyway, and the second is the one an
operator is looking for. `gotthlive.fragment.suppressed` distinguishes them, and
a failed render records an error on the span while the panic value and stack
stay in the log record (§6.4).

**`gotthlive.send` is the write and nothing else, and starting it found a
second defect.** `protocol.Framer.Send` validated, marshalled and wrote in one
call, so the actor timed one interval and recorded it into **both**
`gotthlive_encode_duration_seconds` and `gotthlive_send_duration_seconds` — two
series equal by construction, one of them defined in §2.3 as *"time in
`Conn.Write`, the write-stall signal"*. The framer now exposes `Encode` and
`Write` separately; `Send` is their composition and is still the only write
path, so protocol.md **P8** is untouched.

Context is threaded throughout — no `context.Background()` mid-path
(checklist §4.3).

**How this was measured.** A recording `trace.TracerProvider`
(`internal/obstest`) attached to a live session, one `qa.increment` event driven
end to end, and the client's `ClientTelemetry` frame written back. Each span
printed with its parent's *name*:

```
gotthlive.origin           parent=ROOT                  (mount)
gotthlive.render           parent=gotthlive.origin
gotthlive.render.fragment  parent=gotthlive.render      fragment.id=count
gotthlive.render.fragment  parent=gotthlive.render      fragment.id=label
gotthlive.encode           parent=gotthlive.origin      patch.id=1
gotthlive.send             parent=gotthlive.encode
gotthlive.parse            parent=ROOT                  frame.kind=event
gotthlive.authorize        parent=gotthlive.parse
gotthlive.event            parent=gotthlive.authorize   links=0
gotthlive.reduce           parent=gotthlive.event
gotthlive.render           parent=gotthlive.event
gotthlive.render.fragment  parent=gotthlive.render      fragment.id=count
gotthlive.encode           parent=gotthlive.event       patch.id=2
gotthlive.send             parent=gotthlive.encode
gotthlive.parse            parent=ROOT                  frame.kind=client_telemetry
gotthlive.client.morph     parent=ROOT                  links=1 -> encode
```

Two specs hold that shape where it can fail: one in
`test/internal/conformance/trace_test.go` walks parent edges only and requires
the interaction to reach exactly one root, and one in
`live/instrumentation_test.go` requires `gotthlive.event`'s parent to be
`gotthlive.authorize` and its link set to be empty. Both go red when the child
edge is reverted to a link. The **rate** that clause 4 is really about is
`test/sampling`'s (§3.5).

### 3.2 Attributes (checklist §4.2)

Every span in the tree carries `gotthlive.session.id`. Every span from `reduce`
onward carries `gotthlive.transition.id`. Every span from `encode` onward carries
`gotthlive.patch.id` and `gotthlive.server_seq`. Patch-work spans without
`gotthlive.patch.id` are a review block, by checklist §4.2 and by this document.

**Measured 2026-08-04 by the §3.1 probe, and re-measured after clause 4.** C-29
found two of the three rules broken at the only spans they could then reach, and
left them *"for whoever implements the five missing spans"*. That is the change
this section is now amended by, so both are closed as attribute-site defects
rather than documentation ones:

| Rule | Spans it reaches | C-29 | Now |
|---|---|---|---|
| `session.id`, everywhere | all | present | present |
| `transition.id` from `reduce` onward | `reduce`, `render`, `render.fragment`, `encode`, `send` | **absent** at `encode`; present only on `event` | present on all five |
| `patch.id` + `server_seq` from `encode` onward | `encode`, `send` | present at `encode` | present on both |
| — same rule — | `client.morph` | `patch.id` present, `server_seq` **absent** | both present; the window slot already held the sequence beside the span reference and only the read was missing |

The two spans on the path that carry **no** `transition.id` are `parse` and
`authorize`, and that is not an omission: both run on the read pump before a
transition exists, which is the same fact clause 4's parent edge is about.
`gotthlive.parse` carries `gotthlive.frame.kind` instead — the one attribute
that tells an interaction's trace from an acknowledgement's, and the one a
falsifier has to read to know which traces are interactions at all.

Server-initiated transitions (timer, pubsub, effect result, mount, resync) get a
`gotthlive.origin` span carrying `gotthlive.origin.kind` and
`gotthlive.origin.source` — never `unknown` (FR-42, enforced by the
`len(this) > 0` refinement on `Origin.source`, protocol.md §3.3).

**A resync's origin span is a child, not a root** — the one place clause 4's
mechanism was applied beyond the box that asked for it, and it is worth saying
why. A resync request is authorized like any other event, as the distinguished
name `gotth.resync` on the read pump, so its span graph had the identical defect
C-30 measured one path over: `authorize` a root, `origin` a root, two
independent sampling decisions, and a client-triggered full re-render whose
authorization and whose render could never be recorded together. It descends
from the same `SpanRef` for the same reason. Mount, timer and effect-result
origins remain roots, correctly: no client frame authorized them and there is
nothing truthful for them to descend from.

### 3.3 Closing the loop to the client morph (FR-36's hard half)

**This is now the ONLY link site in FR-36 clause 3's enumeration.** C-29 struck
the effect-span site and left two; clause 4 removed `event ─▶ authorize` by
making it a real parent edge (§3.1), which clause 3 calls *"always permitted"*.
One remains, and the reason a parent edge would be false here is not that the
child ends late — §3.1 says plainly that a child outliving its parent is
permitted — but that the morph is recorded **in another process on a clock this
design refuses to synchronise**, so the span's start timestamp is derived and a
containment claim would be invented rather than observed.

The same reasoning rules out the parent that clause 4's own mechanism would
otherwise make available. The `ClientTelemetry` frame has a `gotthlive.parse`
span of its own, so the morph *could* descend from it at no cost — and it must
not, because the morph's derived start is **earlier than the frame that reported
it**. That would be the same invented enclosure reached by a different route,
and it would look more informative for being a parent edge, which is exactly
what makes it worse than a link.

The client's morph timing arrives *after* the server span closed, so it cannot be
nested. The mechanism:

1. The ack window (RFC §7.1) already holds `patch_id` per slot; it additionally
   holds a **compact 32-byte `spanRef`** for that patch's `gotthlive.encode`
   span: `TraceID [16]byte`, `SpanID [8]byte`, `traceFlags byte`, padded.

   **Not a `trace.SpanContext`.** Cycle 1 asserted 24 bytes for one; that was
   wrong — `TraceID` + `SpanID` is 24 before `traceFlags`, `remote`, and a
   `TraceState` carrying a slice header, so a real `SpanContext` is ~56–64 B on
   amd64. Storing `spanRef` and reconstructing with
   `trace.NewSpanContext(trace.SpanContextConfig{…})` at link time is exact for
   our use (we never populate `TraceState`) and halves the cost.

   **16 slots × 64 B total per slot (32 B ack metadata + 32 B `spanRef`) =
   1,024 B**, and this is now its own line in RFC §6.2's budget rather than being
   declared already covered by a line that was never written to include it.
2. On `ClientTelemetry{patch_id, morph_micros, apply_micros}`, the server emits a
   span `gotthlive.client.morph` with a **`trace.Link`** to that stored span
   context, and attributes `gotthlive.patch.id`, `gotthlive.morph.duration_us`,
   `gotthlive.apply.duration_us`, and `gotthlive.timing.source = "client_reported"`.
3. **Wall-clock alignment between browser and server is not attempted.** The
   *duration* is client-measured and authoritative; the span's start timestamp is
   derived (server receive time minus the reported duration) and is explicitly
   approximate. Saying so here is cheaper than a clock-sync protocol nobody asked
   for.

**No W3C `traceparent` is carried on the wire in v1.** The server is the trace
root, correlation is by `patch_id`, and a 55-byte traceparent per event buys
browser-side context propagation that no v1 requirement asks for. Revisit under
BL-17.

### 3.4 The OTel dependency problem — an open decision for L9-1

FR-36 requires OTel-compatible traces. The `go.opentelemetry.io/otel` API module
is modest, but a consumer who enables tracing needs the SDK, and the SDK's
transitive weight is not trivial for a library held to a stdlib-submission bar
(FR-69, checklist §10.3 Tier 1).

| Option | Consequence |
|---|---|
| **A.** Depend on the OTel **API only** in the core module; the consumer brings the SDK | **Chosen — L9-1 D1.** |
| **B.** Ship tracing in a separate module `gotth-live/otel` | Core `go.mod` stays minimal; costs a second module and makes FR-38's "one option" a lie for tracing |
| **C.** Define our own tiny tracer interface and adapt | Zero dependency; reinvents OTel badly and breaks FR-36's "OTel-compatible" in spirit |

**Settled: Option A (L9-1 D1).** B and C are rejected — C reinvents a standard
badly and is a checklist §1.4 violation on its face (a one-implementation
interface, the same thing the RFC refuses for transport), and B either makes
FR-38's "one option" false for tracing or forces a core interface, which is C
wearing a hat.

**The concrete form proposed, per D1's condition 1 ("the narrowest API module
that compiles"): the `go.opentelemetry.io/otel/trace` and
`go.opentelemetry.io/otel/metric` submodules, never the `otel` root.** The root
module declares eight requirements (`otel/metric`, `otel/trace`, `go-logr/logr`,
`go-logr/stdr`, `cespare/xxhash/v2`, `go.opentelemetry.io/auto/sdk`, …);
`otel/trace`'s own `go.mod` declares one runtime requirement. Because
`Config.Tracer`/`Config.Metrics` take the provider explicitly, **the library never
reads the OTel global**, which is what makes the narrow import possible.

D1's other two conditions: the measured `go list -m all` and binary-size deltas
are quoted in the PR that adds the dependency (condition 2), and the
**pre-registered fallback** is that if enabling tracing adds **more than 8
modules** to a consumer's build graph, we fall back to Option B (condition 3) —
fixed in advance so the choice is not made by whichever number is convenient.

### 3.5 Sampling — what it does to trace STRUCTURE, and then to overhead

Default sampler: `ParentBased(TraceIDRatioBased(0.05))`, overridable.

*(This section is amended 2026-08-04 on **C-30**. It used to open with "FR-36
constrains span structure, not sample rate" and go straight to overhead and
provenance. That sentence is true and it was the wrong thing to say first: it
reads as "structure and sampling do not interact", and they interact
completely. The omission is how three independent sampler roots on the event
path survived two reviews. Structure comes first now.)*

#### What the sampler does to the graph

`ParentBased` asks a **root sampler** at every span that has no parent, and
inherits at every span that has one. It does **not** look at links. Three facts
follow, and they are the whole of C-30:

1. **Every root is an independent coin toss.** A path made of *n* roots records
   all of it with probability *pⁿ*, not *p*.
2. **A link does not carry a decision.** Two spans joined only by a link sample
   independently even though an operator reads the link as one causal chain.
3. **Therefore an unreachable span and an unsampled span look identical**,
   which is precisely the distinction FR-36 clause 1 asserts is a defect rather
   than an artefact.

Measured by L9-1 over 300 real interactions at the default above, when the path
had three roots: `gotthlive.authorize` sampled 11/300, `gotthlive.event` 11/300,
`gotthlive.effect` 11/300, `gotthlive.origin` 19/300, `gotthlive.encode` 30/300
(= 19 + 11, exactly), and **both `authorize` and `event` together: 0 of 300**.
The arithmetic closing on itself is what says the measurement is sound. The
expected joint rate was 0.25 %, and 0/300 is what 0.25 % looks like.

#### What FR-36 clause 4 requires, and what it now measures

Clause 4 makes the whole server-side path — `parse`, `authorize`, `event`, and
everything descending from the transition — **one sampling decision**, taken
once at `gotthlive.parse` and inherited by parent edges the rest of the way
(§3.1). At rate *p*: *p* of interactions record the whole server-side graph,
1 − *p* record none of it, and **there are no partial graphs to misdiagnose**.

That is a falsifiable claim and it has a falsifier, in
`gotth-live/test/sampling` — a satellite module so that the OpenTelemetry SDK
never enters a consumer's build list (§3.4, dependencies.md §2.3), run by
`ci.sh`. Measured there, 300 interactions per rate against a real
`ParentBased(TraceIDRatioBased(p))`:

| *p* | interactions | complete server-side graphs | **partial** |
|---|---|---|---|
| 0.05 (the shipped default) | 300 | 12 (4.0 %) | **0** |
| 0.25 | 300 | 88 (29.3 %) | **0** |
| 0.5 | 300 | 156 (52.0 %) | **0** |

The spec carries a second assertion whose only job is that the first cannot pass
by recording nothing: in the same run, some interactions must be sampled and
some must not. And it has been **observed failing**: reverting the one line that
makes `gotthlive.event` a child of `gotthlive.authorize` turns the p=0.05 run
into **18 of 18 partial, 0 complete**, every recorded interaction holding
`{parse, authorize}` and nothing after it.

#### The morph is a second decision, and this is what that costs

`gotthlive.client.morph` is a root and stays one (§3.3, FR-36 clause 4's second
paragraph). So morph attribution is present for about *p* of the interactions
whose server-side graph was recorded — a joint rate of *p*², measured at
p = 0.5 over 200 interactions as 109 server-side graphs and 104 morph spans,
independently drawn.

**What that loses is attribution, not measurement.** The same duration arrives
as a `ClientTelemetry` frame and is recorded into
`gotthlive_client_morph_duration_seconds`, which is **unsampled** (FR-29,
FR-34). An operator loses the per-event link between one patch and one browser's
morph; they do not lose the latency distribution. That is the trade FR-36 books
explicitly, in preference to a parent edge asserting an enclosure over a derived
timestamp, or a 55-byte `traceparent` per event that BL-17 holds.

#### Then overhead, and provenance

PRD G4's provenance-totality guarantee is served by the frames and by the
provenance log — **specified in §4A**, and unsampled by construction — **not**
by traces, so sampling does not weaken it. L9-1 accepted the 5 % default *given*
that dependency, which is why §4A exists.

§4 states how this interacts with NFR-1, and why both sampled and unsampled
overhead are reported. **Not measured here:** what the five newly-started spans
cost against NFR-1's 5 % budget. Eight spans per interaction plus one per
fragment is more work than three, at both sampled and unsampled rates, and the
figure that decides it is QA-2's Phase 5 event→paint measurement rather than a
microbenchmark. It is named rather than estimated.

---

## 4. Overhead budget (NFR-1, G6) — ≤ 5 % of p50 event→paint

### 4.1 What is measured

Chat example, metrics **and** traces enabled versus fully disabled, p50
event→paint per equivalence-spec §3.2's definition, same hardware, same network
profile, percentiles with variance and sample counts (PRD FR-73).

**Two figures are reported, and this is deliberate:**

| Configuration | Role |
|---|---|
| default sampling (5 %) | **the NFR-1 gate** |
| 100 % sampling | **reported alongside, unsoftened** — otherwise the gate would be met by choosing a sample rate rather than by writing efficient code |

Reporting only the first would be exactly the kind of number FR-73 exists to
prevent.

### 4.2 The design decisions that make ≤ 5 % plausible

- **Pre-resolved label sets.** Each session holds its label slices, resolved
  once at open. No `map[string]string` construction and no label lookup on the
  hot path.
- **Counters are atomic adds**, not `WithLabelValues` per observation.
- **Attribute slices are reused** per session (`[]attribute.KeyValue` backed by a
  fixed array), so span creation does not allocate in steady state.
- **`gotthlive_mailbox_depth` and `_window_depth` are sampled**, not observed on
  every step.
- **Nothing is computed when disabled — this is a requirement, not a design
  note.** Metrics and tracing hooks MUST be nil-checked behind a single boolean
  per session, **not** behind a no-op interface, so a disabled configuration pays
  one predictable branch and nothing else. Stated as a requirement because it is
  the difference between NFR-1's 5 % gate being met by architecture and being met
  by sampling — the thing §4.1 and I3 are anxious about. A PR that routes a hot
  path through a no-op interface implementation fails this.

### 4.3 Falsifier

If the 5 % gate is met only at 5 % sampling and 100 % sampling exceeds 15 %, that
is a signal the hot path allocates, and it is fixed rather than sampled around.

---

## 4A. The provenance log

`provenance log` was load-bearing in cycle 1 — protocol.md P2/P4/P7, §3.5's
justification for trace sampling, §5.1's join target — and defined nowhere.
PRD G4 and RFC E4 rest on it. This section is the definition.

### 4A.1 What it is

**The provenance log is a structured log stream, not a library-owned store.** One
record per *transition*, emitted by the session actor, carrying the whole causal
row. The operator's existing log pipeline is the storage and the query engine —
in this monorepo, Loki (`docker-compose.admin.yaml`).

This is the cheapest correct answer. The alternative — a per-session in-memory
ring — cannot satisfy G4's 30-minute soak without holding ~95,400 records per
session at the dashboard workload, which is absurd against RFC §6's budget; and
it would not survive a restart, would need its own retention policy, and would be
a second store to secure.

### 4A.2 Format

One record per transition, at `Info`, on a dedicated logger name
`gotthlive.provenance`, with exactly these fields:

| Field | Source |
|---|---|
| `session_id` | RFC §3.1 |
| `event_id`, `client_ref` | 0 for server-initiated transitions |
| `transition_id`, `state_version` | protocol.md §4.1 |
| `patch_id`, `server_seq` | 0 if the transition emitted no patch (suppressed render, RFC §5.4) |
| `origin_kind`, `origin_source` | protocol.md §3.3 |
| `fragment_ids` | the fragments this transition patched |
| `contributing_event_ids` | present only on coalesced patches (RFC §7.4) |
| `superseded_from_seq`, `superseded_through_seq` | present only on resync snapshots (protocol.md §4.3) |

**A suppressed render still emits a record** with `patch_id = 0`. Without it P4
("`state_version` increases iff state changed") would be unverifiable, because
the transitions that produced no patch would be invisible.

Record size is ≈200 B serialized. At the dashboard workload that is
≈10.6 KB/s/session — a real number, stated so capacity planning is possible
rather than discovered.

### 4A.3 Configuration, and what is lost without it

Enabled whenever `Config.Logger` is non-nil, and **exempt from §6.3's `Info`
sampling** — a sampled provenance log cannot support a "100 %, zero unknown"
guarantee. There is no separate option: G4 depends on it, so it is not something
to forget to turn on.

If `Config.Logger` is nil, provenance records are not emitted and **FR-41's
reverse lookup is unavailable**. That is a documented consequence, not a silent
one: `livetest.Audit` and the G4 soak both fail closed if the logger is absent.

**What is *not* affected by any of this:** the causal chain itself, which rides
in the frames and is governed by FR-43. The distinction matters and is easy to
blur — **the frames always carry the chain; the provenance log is the
server-side index that makes the reverse lookup possible.** Disabling the log
costs queryability, never wire-level provenance.

### 4A.4 Cost and bounds

The library's contribution is bounded by construction: exactly one record per
transition, fixed field set, no accumulation in process. Retention and volume are
the operator's, and §4A.2 gives the numbers to plan with. Hot-path cost is one
`slog` record — measured under NFR-1's budget alongside metrics and traces, and
reported separately so it can be judged on its own.

### 4A.5 Independence from the metrics path — the property §4.5 needs

**The provenance log shares no code with the counters it is used to audit.**
Provenance records are emitted by the session actor in `step` (RFC §3.2), from
the same values that construct the frame. The metrics it is checked against
(`gotthlive_frames_sent_total`, `gotthlive_transitions_total`) are incremented in
the framer and the transport, on a different code path, into a different sink,
scraped by a different mechanism.

That is what makes §5.1's "reported vs externally observed" rows meaningful for
provenance. Were the log to read the framer's counters, or the framer to derive
its counts from the log, the audit would be checking a value against itself —
which is the failure checklist §4.5 exists to catch. **A PR that couples them is
a block.**

---

## 5. External auditability (checklist §4.5; QA-2's mandate)

> A metric that only the incrementing code can vouch for is not evidence.

Every library-reported signal has an **independent** confirmation path that does
not share code with the reporter. This table is the contract, and `livetest.Audit`
(§5.2) is its implementation.

### 5.1 The confirmation table

| Reported signal | Independent confirmation |
|---|---|
| `gotthlive_frames_sent_total` | count frames in a wire capture — this is protocol.md **P8**, and any drift means a second write path exists (checklist §4.4 automatic return) |
| `gotthlive_frames_received_total` | wire capture |
| `gotthlive_wire_bytes_total` | wire-capture byte count; cross-check against the container's network counters |
| `gotthlive_patches_sent_total{op}` | decode the capture and count by `FragmentUpdate.op` |
| `gotthlive_connections_closed_total{code}` | close frames in the capture carry the code |
| `gotthlive_sessions_active` | `ss -tn state established '( sport = :PORT )' \| wc -l`, from outside the process |
| `gotthlive_goroutines` | `runtime/metrics` is in-process, so audit against a **pprof goroutine dump** parsed externally |
| `gotthlive_process_heap_bytes` and `gotthlive_sessions_active` | RFC §6.3's out-of-process cgroup v2 measurement, compared against the two series undivided (A-12 dropped the pre-divided mean; the audit does its own arithmetic) |
| `gotthlive_reduce/render/encode_duration_seconds` | an independent CPU profile (`pprof`) over the same run, attributed by symbol; and span durations arriving at the collector by a path that shares no code with the metric |
| `gotthlive_client_morph_duration_seconds` | the harness's own CDP measurement per equivalence-spec §3.2 — the only fully external check on a client-reported number |
| `gotthlive_transitions_total{result}` | the provenance log's transition count (§4A.5 — a genuinely separate code path and sink), and the FR-15 determinism harness replaying the same event log |
| provenance-log completeness | every `patch_id` in the wire capture has a provenance record, and every record with `patch_id != 0` has a frame in the capture. Two-way, so neither a missing record nor a phantom one passes |
| `gotthlive_panics_total{site}` | injected-panic count from the Phase 2 fault-injection suite |
| `gotthlive_resync_requests_total{result}` | decode the capture and classify each `ResyncRequest` by what the server sent next on that session — a `Snapshot`, an `Ack`, or an `Error{RATE_LIMITED}`. Fully external and exact, and it is the same evidence exit criterion **E13** needs, so the amplification bound and the counter are checked by one mechanism |
| `gotthlive_source_label_overflow_total` | count the distinct `Origin.source` values in the independently decoded capture; everything past the sixty-fourth should have incremented it. Exact, and it is the only check that the cap is doing what §2.1 claims rather than silently discarding |
| `gotthlive_outbound_validation_failed_total{kind}` | injected-malformed-frame count from the Phase 2 fault-injection suite, **plus** a wire-side check that costs nothing extra: a dropped frame must leave no gap, so `server_seq` in the capture stays contiguous (P3) and an `Error` carrying the same causal chain appears in the dropped frame's place |
| `gotthlive_frames_rejected_total{reason="ack_channel_full"}` | count `Ack` frames in the capture against the high-water marks the outbound window is observed to advance through; the difference is the drops. The harness induces them by stalling the actor under a deliberate ack flood, so the expected count is known in advance rather than inferred from the number being audited |

### 5.2 `livetest.Audit` — the harness

A single test helper drives a scripted workload against a real server and asserts
each pair in §5.1 agrees within a stated tolerance:

1. capture the wire (both directions) for the run;
2. scrape `/metrics` before and after;
3. read cgroup v2 `memory.current`/`memory.stat`, `ss`, and a pprof goroutine
   dump from **outside** the process;
4. decode the capture independently of the library's framer — using the
   **generated client codec** (protocol.md §10.2), not the server's encoder, so a
   bug in the server's framing cannot hide itself;
5. assert equality, exact for counters and within tolerance for timings.

A failure means a metric lies, which is a merge block. This runs in CI on the
counter and chat examples and is the concrete answer to checklist §4.5.

---

## 6. Structured logs (FR-37, FR-58; checklist §4.7, §5.6)

### 6.1 `log/slog`, and the house-convention conflict

The library logs through **`log/slog`** and accepts a `*slog.Logger` from the
consumer. It never constructs a logger, never sets a global, and imposes **no**
logging dependency (FR-37).

**Settled by L9-1 D2**, which reads `go/CLAUDE.md`'s rule correctly and more
narrowly than cycle 1 did: "do not bypass `core.Logger`" is a rule about the
inside of the `go/` module, not a requirement that every Go artifact in this
repository depend on zerolog. gotth-live is a standalone module outside `go/`,
published at a stdlib-submission bar, and a library that puts zerolog in every
consumer's `go.mod` fails that bar.

The examples ship a ~40-line `slog.Handler` adapter binding library records to
this monorepo's `core.Logger`. **D2's binding condition: the adapter ships with a
test in the same PR** — one test that drives a library log record through it and
asserts the fields arrive on `core.Logger`. An untested adapter pasted into a doc
is how "nothing is lost on the inside" quietly becomes false.

### 6.2 Fields

Every library record carries `session_id`. Where applicable it also carries
`event_id`, `event_name`, `transition_id`, `patch_id`, `fragment_id`,
`server_seq`, `origin_kind`, `origin_source`, `close_code`, `duration_ms`.

FR-58 requires every library-produced error to name the session, the causal ID
where one exists, and the actionable next step. `"invalid frame"` without context
is a defect; the error text template is
`gotth-live: <what failed>: <why>: <what to do>` and a Phase 4 audit walks every
error construction site.

### 6.3 Levels — honest, and rate-limited (checklist §4.7)

| Level | Used for |
|---|---|
| `Debug` | per-event and per-patch records. **Off in production by default.** |
| `Info` | session open/close, resync issued. **Sampled 1:100 above 100 connections/s**, with the sampling stated in the record. |
| `Warn` | coalescing engaged, slow-client degrade, rate limit engaged, client telemetry dropped, identical-render suppression rate above threshold |
| `Error` | recovered panic, abandoned effect, protocol violation, fatal authorization denial, fragment-ID collision |

An operator should act on `Error`. Nothing routine reaches it.

### 6.4 Redaction at the boundary (checklist §5.6)

Never emitted at any level: session tokens, cookies, `Authorization` headers,
authorization-decision inputs, **full state snapshots**, and **raw frame payload
bodies**. The last two are the realistic leak vector once resync and frame logging
exist, and they are named explicitly for that reason.

Redaction happens at the logging boundary, not in callers: the library's log
helpers accept only field types that cannot carry a payload, and a test asserts
that no call site passes a `*Frame`, an application state value, or an `IIdentity`
into a log record.

---

## 7. Dev-mode provenance inspector (FR-44, NFR-8) — DELIVERED

Ships as a **separate opt-in file**, does not count against NFR-2's 12,288 bytes,
does not load in production builds, and stays ≤ 40 KB gzipped. It renders, for a
running session, the event stream, the resulting state versions, and the patches
each produced, joined by causal ID — the same joins §5 audits. It additionally
flags `hx-*` attributes inside an unpreserved live fragment (RFC §10.3), which is
where a developer will actually notice that mistake.

**Delivered at Phase 4** as `live/clientjs/gotth-live-inspector.min.js`, mounted
by `(*App[S]).InspectorScript`, documented at
[`guide/inspector.md`](guide/inspector.md). This section is no longer a pointer,
so the three numbers it fixed are now measurements rather than budgets:

| | fixed here | measured |
|---|---:|---:|
| inspector, gzip −9 | ≤ 40,960 B | **6,211 B** |
| shipped runtime, gzip −9 (NFR-2, must not move) | ≤ 12,288 B | **4,459 B** — and **unchanged by the inspector landing**, which is the claim this row makes |

**⟨CORRECTED 2026-08-05.⟩** The runtime row read **4,429 B, unchanged by this
landing**. The *"unchanged by this landing"* half is true and stays: the
inspector cost the shipped runtime zero bytes. The **figure** is not a property
of that landing, and carrying it as one made it stale twice over — FR-54's
per-binding options took it to 4,421 (`2ab18690`) and `Bind.NoModifiers` /
`Bind.PreventDefault` took it to **4,459** (`2311280b`). Re-measured with
`tools/minify -check` at HEAD; **7,829 B of headroom, 63.7 %**, so NFR-2 is met
with the same margin it has had all phase. `client/SIZE.md` §1.1 is the
per-landing attribution.

Both figures are gated by `tools/minify -check` from `ci.sh`, not asserted here.
One limit is worth stating where the budget was: `Config.Dev` gates *serving* the
artifact (404) and *rendering* the tag (zero bytes), which is what "does not load
in production builds" means and is what the specs assert. It does not gate
*embedding* — the bytes are `go:embed`ed like any other asset and `strings` will
find them in a production binary. Excluding them would need a build tag, and none
was added.

---

## 8. Open questions

| # | Question | Owner | Needed by |
|---|---|---|---|
| I6 | The provenance log's volume (§4A.2, ≈10.6 KB/s/session at the dashboard workload) is a real operational cost. Is a sampled-in-production / full-in-soak mode wanted, given §4A.3 makes G4 depend on it being unsampled? | QA-2 + PM-1 | Phase 3 |
| I3 | ~~Whether the default trace sample rate of 5 % is right, or whether NFR-1 should be measured only at 100 % sampling so the gate cannot be met by sampling~~ **RULED and closed** by PM-1 at the checkpoint-2 gate (PRD v0.5 §9 row 4, applied in NFR-1). The gate stays the figure at the **shipped default** — G6's claim is that observability is default-on, so gating a configuration nobody deploys measures nothing — and I3's worry is enforced instead by promoting **§4.3's own 15 % falsifier into the gate**: if the sampled figure passes and the 100 % figure exceeds 15 % of p50 event→paint, NFR-1 is **not met**. The shipped default is pre-registered and may not move between the start of the Phase 5 measurement and the report | Closed; QA-2 retains the measurement | Phase 5 |
| I4 | Histogram bucket boundaries for the duration metrics; the defaults should straddle the 50 ms/150 ms budget rather than Prometheus's generic set | DEV-1 | Phase 1 |
| I5 | **Partly resolved**: the pre-divided mean is dropped per A-12 (§2.4). What remains for QA-2 is whether `gotthlive_process_heap_bytes` earns its place beside the external cgroup measurement at all | QA-2 | Phase 3 |


---

## 9. Changelog

### Review wave, ruling 2 — 2026-08-04: the `fragment` render label, deleted rather than exported

L9-1 ruling on REV-DEL finding 3
([rulings-review-wave.md](reviews/rulings-review-wave.md) §2). This document
advertised a knob that did not exist; the fork was to build it or stop
advertising it, and the second is correct.

| Was | Is | Why it mattered |
|---|---|---|
| §2.3: `gotthlive_render_duration_seconds` carries `fragment`, *"opt-in label; unlabelled by default"* | No label, and the row says what the instrument actually measures: **one observation per render pass** | There was no opt-in to reach. `obs.Metrics.FragmentLabels` appeared exactly three times in the repository — its doc comment, its declaration, and the `if` that read it — and was set by nothing, in production or in test, so the branch was `false` on every render pass in every configuration that exists. An operator following this row went looking for a `Config` field that has never existed. |
| — | New, and it is the reason the fork was resolved by deleting rather than by adding the field | **The label was also wrong, not merely unreachable.** `RenderDuration` is called once around `renderPass`, which renders every dirty fragment; the value passed was `firstFragment(res.Updates)` — whichever fragment happened to be first in the update slice. Enabling it would have attributed a whole-pass duration to one fragment's name. A knob nobody can reach is a documentation defect; a knob that would misattribute if reached is a reason not to build it. |
| §2.1: *"`event` and `fragment` label values are bounded by registration"* | `event` only, with `fragment` explicitly relocated to the `fragment.id` **span attribute** on `gotthlive.render.fragment` | Stated so the next reader does not re-derive the cardinality question and conclude the label was refused on cardinality. It was not: fragment IDs come from `Config.Fragments` and are fixed before the first connection, so §2.1's own registration bound applied and §2.1's no-causal-ids rule never bit. The label was refused on FR-65 (an exported `Config` field with no named consumer) and on correctness. |
| — | Deleted with it: `obs.Metrics.FragmentLabels`, `fragmentAttr` and its initialiser, the branch, `session.firstFragment`, and `RenderDuration`'s third parameter, which becomes `(ctx, seconds)` | All internal. `tools/apisurface` reads `live 49/49` and `50/50` across the change, unmoved — the reviewable half of this item is the field that was *not* added. |

**Re-add trigger, pre-registered.** Move the timing inside the per-fragment
loop so a per-fragment observation exists, name the operator who needs the
series, and the `Config` field lands with that consumer in the PR. Until then
per-fragment attribution is the span attribute, which carries it at full
fidelity and costs no time series.

### Checkpoint 3 — 2026-08-04: FR-36 clause 4 (C-30) and the five spans that started nowhere

Code and documentation, in that order. Every figure below was produced by
running the tracer, and the sampling figures come from a spec that has been
observed failing before it was believed.

| Was | Is | Why it mattered |
|---|---|---|
| §3.1: `gotthlive.authorize`, `gotthlive.event` and `gotthlive.client.morph` were three **roots**, joined by two links | The server-side path is **one parent chain rooted at `gotthlive.parse`**; `gotthlive.client.morph` is the only remaining root and the only remaining link site | `ParentBased` asks a root sampler at every root and **does not follow links**, so three roots were three coin tosses. Measured by L9-1 at §3.5's own documented default: `authorize` 11/300, `event` 11/300, **both together 0 of 300**. FR-36 clause 1 says an unreachable span is *"a defect, not a sampling artefact"*, and at the default the two were indistinguishable. The mechanism is the `obs.SpanRef` the ingress already carried — an ended span is still a valid parent — so the fix cost one call, `obs.Tracer.StartChildOf`. |
| §3.1: five of the eight drawn spans were **started nowhere** — `parse`, `reduce`, `render`, `render.fragment`, `send` | All eight start, on the real path | Recorded as unmet since PRD v0.4 and unmoved by checkpoint 2, with the reason each time that a requirement narrowed to what shipped tests nothing. `gotthlive.render.fragment` also had no Go constant, deliberately; it has one now, in the change that starts it, which is what C-29 said the condition for declaring it was. |
| §2.3: `gotthlive_send_duration_seconds` was documented as *"time in `Conn.Write`"* and was **not** | The framer splits `Encode` from `Write`; the two histograms measure different intervals | Found by trying to start `gotthlive.send` and discovering there was nothing to wrap. `Framer.Send` validated, marshalled and wrote in one call, so encode-duration and send-duration were **equal by construction** and the write-stall signal could not detect a write stall. `Send` still exists as their composition, so protocol.md **P8**'s single write path is untouched. |
| §3.2: `transition.id` from `reduce` onward and `server_seq` on the morph were **measured absent** at C-29 and left for whoever implemented the missing spans | Both hold | C-29 was right to record rather than amend, and this is the change it was waiting for. The morph's `server_seq` was in the window slot beside the span reference the whole time; only the read was missing. |
| §3.5 stated what sampling does to **overhead and provenance** | §3.5 states what sampling does to **structure** first, then overhead and provenance | PM-1's C-30 ruling names this omission as *"how this survived two reviews"*. The section now opens with the three facts about `ParentBased` that make a link a broken chain, publishes the 0/300 that motivated clause 4, and publishes the table that replaces it. |
| — | New: a resync's `gotthlive.origin` span is a **child** of its authorize span | Found while applying clause 4: a resync is authorized on the read pump like any other event, so it had the identical two-root shape one path over. Mount, timer and effect-result origins stay roots, because no client frame authorized them. |

**The falsifier, and the fact that it fails.** `gotth-live/test/sampling` is a
satellite module — its own `go.mod`, so `go.opentelemetry.io/otel/sdk` never
enters a consumer's build list (§3.4's D1 conditions, dependencies.md §2.3) —
run by `ci.sh` next to `test/routers`. Measured: **0 partial server-side graphs**
at *p* = 0.05, 0.25 and 0.5 over 300 interactions each, with 12, 88 and 156
complete. Reverting the child edge turns *p* = 0.05 into **18 of 18 partial, 0
complete**, each recorded interaction holding `{parse, authorize}` and nothing
after it — C-30's shape, reproduced on demand. The two structural specs that can
hold this in `internal/obstest` (which stamps one `TraceID` on everything and
cannot express a decision — QA-1's **D-11**) go red on the same mutation.

**Not done here, and named rather than estimated:** what eight spans per
interaction plus one per fragment cost against NFR-1's ≤ 5 % budget. That figure
is QA-2's Phase 5 event→paint measurement and no microbenchmark substitutes for
it. §3.5 says so in the section that would otherwise be where an estimate went.

### Checkpoint 2 — 2026-08-04: condition C-29, §3 amended to the tracer that shipped

Documentation only. No code changed, no exported surface moved, no span was
added or removed. Every claim below was re-measured with a recording provider
before it was written; L9-1's findings were the starting point and not the
evidence.

| Was | Is | Why it mattered |
|---|---|---|
| §3.1 drew `gotthlive.authorize` as a **child** of `gotthlive.event` | `authorize` is a **root**, and the shipped edge is a **link running `event` ─▶ `authorize`** | The direction is the whole causal story: authorization runs on the read pump *before* the event reaches the actor, so there is no transition span for it to descend from. The call-site comment in `internal/session/ingress.go` said so correctly while the diagram contradicted it; the comment is now quoted in the section it was losing an argument to. |
| §3.1: effect spans are *"linked, not nested, because an effect may finish after the event span closes"* | Effect spans are **children of `gotthlive.event` with zero links**, and the sentence is struck | Both halves were wrong, and the reason was not a reason — OTel does not require a child to end before its parent, and a parent edge asserts causal containment rather than clock enclosure. **The code is right here.** Measured mid-flight: `event.Ended=true effect.Ended=false`. Consequence for FR-36 clause 3: the link enumeration is **two sites, not three**. |
| §3.1 drew eight spans as if all eight existed | Five — `parse`, `reduce`, `render`, `render.fragment`, `send` — are marked **drawn, started by nothing** | The boxes stay because PRD v0.4 §9 item 2 records FR-36 as **unmet** rather than narrowing it to what shipped, and an operator attributing latency inside one event needs them. `gotthlive.render.fragment` additionally has **no Go constant** and §3.1 now says it is unnamed, rather than a constant being declared for a span nothing starts. |

**Not done here, deliberately:** C-30 — making `gotthlive.event` a true child of
`gotthlive.authorize` — is a separate condition whose scope PM-1 has not ruled
on. §3.1 records the dependency and describes only what ships. **Newly recorded
in passing:** §3.2's `transition.id`-from-reduce-onward and
`server_seq`-from-encode-onward rules do not hold at the two spans they can
reach today; measured, tabulated in §3.2, and left for whoever implements the
five missing spans rather than amended by this condition.

### Phase 1, module init — 2026-08-04: conditions C-7, C-8 and C-9 closed

| Condition | Closure |
|---|---|
| **C-7** — the enabling API contradicted the API surface | §2 and §3 now name **`Config.Metrics`** (a `metric.MeterProvider`) and **`Config.Tracer`** (a `trace.TracerProvider`), which is what api-surface §1.1 ships; the three `WithX` functions this document named were among the ~14 the ledger cut. `reg` implied a Prometheus registry, so §2 states plainly why the field is a `MeterProvider` and why the Prometheus-shaped metric *names* are a convention the exporter maps rather than a dependency we impose. FR-38's "exactly one option" is satisfied by one field. **`WithPerSessionMetrics` is cut rather than ledgered**: it was an exported symbol in no ledger, with no named consumer (FR-65), and it contradicted the bullet immediately above it, since a per-session label *is* a causal ID as a label. Per-session attribution is traces and the §4A provenance log, which carry the whole chain without multiplying a series per connection. Re-adding the knob needs an api-surface row and a consumer. |
| **C-8** — four cycle-2 signals were named in the RFC and protocol.md and absent here | All four added to §2.2: `gotthlive_resync_requests_total{result}` with its three-value domain, `gotthlive_source_label_overflow_total`, `gotthlive_outbound_validation_failed_total{kind}` — flagged as a library bug rather than a client problem, since any non-zero value means we constructed a frame we could not validate — and the `ack_channel_full` value of `gotthlive_frames_rejected_total{reason}`. **Each also gets a §5.1 row**, because a signal with no independent confirmation is the thing §5 exists to forbid; two of the four (resync results, source overflow) are exactly checkable from a decoded capture, and the other two say what they lean on the fault-injection suite for. This was L9-1's "single most likely to rot", and the rot is in the mechanism as much as the list, which is why the audit rows landed with the metric rows. |
| **C-9** — §2.1 and protocol.md §3.3 contradicted each other on effect-source registration | **protocol.md is correct and this document was wrong.** An `Effect` is an application type and nothing registers it, so there is no registry for `source` values to be bounded by. §2.1 now splits the bullet: `event` and `fragment` *are* registration-bounded and the startup warning covers them; `source` is bounded **at the metric** at 64 values with an overflow counter, and the section says outright that this is the weaker guarantee and that a `source` explosion surfaces at runtime through that counter rather than at startup — because the startup warning cannot see it. |

**Non-blocking nit fixed in passing:** §5's confirmation table is referenced as
"§5.1" throughout and had no such heading. It has one now.

### Cycle 2 — 2026-08-04, in response to [L9-1 cycle-1 review](rfc/001-review-cycle-1.md)

**Verdict addressed: RETURN, 2 blocking + 3 advisory.** Both blockers fixed; all
three advisories applied; none declined.

| Objection | Change |
|---|---|
| **B-11** — the provenance log is load-bearing everywhere and specified nowhere | New **§4A**, answering every question the objection listed. It is a **structured log stream**, not a library-owned store: one record per transition, fixed field set (§4A.2), on a dedicated `gotthlive.provenance` logger, **exempt from `Info` sampling**, stored and queried by the operator's existing pipeline. §4A.1 argues against the in-memory-ring alternative (~95,400 records/session for G4's 30-minute soak). §4A.3 states what is lost when `Config.Logger` is nil, and draws the distinction that was being blurred: **the frames always carry the causal chain (FR-43); the log is the server-side index that makes the reverse lookup possible.** §4A.4 gives volume numbers (≈200 B/record, ≈10.6 KB/s/session at the dashboard workload) so capacity is planned rather than discovered. §4A.5 answers the checklist §4.5 question directly — the log is emitted by the actor in `step`, the counters it audits are incremented in the framer and transport, **different path, different sink**, and coupling them is a block. §3.5's sampling justification now points at a defined artifact, and §5.1 gains a two-way completeness row. New **I6** flags the volume as an operational question. |
| **B-12** — span-context accounting understated and double-counted | Both errors fixed, and the fix reduced the cost rather than inflating the number. Cycle 1's "24 bytes" was wrong — a real `trace.SpanContext` is ~56–64 B on amd64 once `traceFlags`, `remote`, and `TraceState`'s slice header are counted. §3.3 now stores a **compact 32-byte `spanRef`** (`TraceID`, `SpanID`, `traceFlags`) and reconstructs the `SpanContext` at link time, which is exact for our use because we never populate `TraceState`. And it is no longer "declared already covered": **RFC §6.2 carries it as part of an explicit 1,024 B window line** (16 slots × 64 B). |
| **A-11** — pin the `code` label domain | Applied. §2.2 enumerates the fourteen **lower-case** label values and forbids the numeric code and the upper-case constant name. protocol.md §8.3 gained a matching `code` column in the same cycle, so the enumeration, the dashboards, and the §5 audit have one source. |
| **A-12** — drop `gotthlive_heap_bytes_per_session_mean` | **Applied, not declined.** The series is derivable from two we already export and its only distinctive property was inviting the misreading its own suffix existed to prevent. §2.4 now exports the undivided `gotthlive_process_heap_bytes` and lets the query divide. Noted that QA-2 still owns I5 and this is L9-1's input applied, not an override. |
| **A-13** — state the nil-check as a requirement | Applied. §4.2's bullet is now MUST-phrased, names the no-op-interface anti-pattern explicitly, and says a PR that routes a hot path through one fails the requirement. |

**Governance incorporated:** **D1** — §3.4 records Option A as settled and
proposes the concrete narrow form (`otel/trace` + `otel/metric` submodules, never
the root; the library never reads the OTel global), with conditions 2 and 3
including the **8-module pre-registered fallback**. **D2** — §6.1 records
`log/slog` as settled with the binding condition that the `core.Logger` adapter
ships **tested** in the same PR. I1 and I2 are closed and removed.
