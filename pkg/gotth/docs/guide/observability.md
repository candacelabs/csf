# Observability

At the end of this page you can turn on the library's metrics, traces and
provenance log with three fields, and know what each one buys you at three in
the morning.

Compiled source: [`_samples/observability`](_samples/observability).

---

## Three fields

<!-- sample: observability/observability.go -->
```go
func Instrument[S any, I live.IIdentity](cfg live.Config[S, I], logger *slog.Logger, mp metric.MeterProvider, tp trace.TracerProvider) live.Config[S, I] {
	cfg.Logger = logger
	cfg.Metrics = mp
	cfg.Tracer = tp
	return cfg
}
```

| Field | Type | Turns on |
|---|---|---|
| `Logger` | `*log/slog.Logger` | the library's structured logs **and the provenance log** |
| `Metrics` | `metric.MeterProvider`, from `go.opentelemetry.io/otel/metric` | every `gotthlive_*` metric |
| `Tracer` | `trace.TracerProvider`, from `go.opentelemetry.io/otel/trace` | every `gotthlive.*` span |

You write no instrumentation code, register no collectors, and name no metric.
A nil provider is legal for all three and costs one predictable branch per call
site.

**The providers are yours.** The library never constructs a logger, never sets a
global, and never reads the OpenTelemetry global — the provider is taken
explicitly, which is what lets it depend on the OTel *API* submodules
(`otel/metric`, `otel/trace`) rather than on the OTel root.

**It takes a `MeterProvider`, not a Prometheus registry.** If you want
Prometheus, use OTel's Prometheus exporter: that is your dependency rather than
one this library imposes on everybody. The `_total` / `_seconds` / `_bytes`
naming below is Prometheus *convention*, which the exporter maps cleanly.

Constructing the providers is OpenTelemetry's business and not this library's —
`go.opentelemetry.io/otel/sdk/metric` and `.../sdk/trace`, wired to whatever
exporter you use. Nothing about that setup is gotth-live-specific.

---

## What is measured

The full catalogue is [`docs/instrumentation.md`](../instrumentation.md). The
ones worth putting on a dashboard on day one:

| Metric | Labels | Reads as |
|---|---|---|
| `gotthlive_sessions_active` | — | how many live connections there are |
| `gotthlive_connections_closed_total` | `code` | why they end. The label is the **lower-case name** from the close-code table — `normal`, `slow_client`, `rate_limited` — never the number. |
| `gotthlive_events_rejected_total` | `reason` | `unauthorized`, `unknown_event`, `mailbox_full`, `rate_limited`. A rising `unknown_event` is usually a binding naming an event you forgot to register. |
| `gotthlive_transitions_total` | `result` | `applied`, `no_change`, `panicked` |
| `gotthlive_patches_sent_total` | `op` | `morph`, `append`, `prepend`, `remove` |
| `gotthlive_patches_suppressed_total` | — | fragments that declared themselves dirty and rendered bytes the client already had. **This is how you find an over-declared `Dirty`** — see [fragments-and-dirty-tracking.md](fragments-and-dirty-tracking.md). |
| `gotthlive_patches_coalesced_total` | — | backpressure stage 1: the client is falling behind |
| `gotthlive_slow_client_events_total` | — | backpressure stage 2 |
| `gotthlive_outbound_window_depth` | — | a histogram, so degradation is visible *before* an eviction |
| `gotthlive_effects_total` | `source`, `result` | `ok`, `error`, `cancelled`, `panicked`, by your `EffectSource()` |
| `gotthlive_panics_total` | `site` | `reduce`, `render`, `effect` |
| `gotthlive_reduce_duration_seconds` | `event` | |
| `gotthlive_render_duration_seconds` | `fragment` | opt-in label; unlabelled by default |

Three cardinality facts to design around:

- **No causal identifier is ever a metric label.** Not `session_id`, not
  `event_id`, not `patch_id`. They are span attributes and log fields instead.
- **The `event` label is bounded by `Config.Events`**, which is fixed before the
  first connection. That is one of the reasons the registration list exists.
- **The `source` label is capped at 64 distinct values per process**, because
  nothing registers an effect source. Past the cap, values collapse to `other`
  and `gotthlive_source_label_overflow_total` increments. Traces and the
  provenance log carry the full value either way.

**There is no `gotthlive_reconnects_total`.** A session does not outlive its
connection, so the server cannot correlate a reconnect to its predecessor
without retaining exactly the state it chose not to keep. Derive it:
`rate(gotthlive_connections_total)` against
`rate(gotthlive_connections_closed_total{code=~"heartbeat_timeout|normal|going_away"})`.

Two of the histograms — `gotthlive_client_morph_duration_seconds` and
`gotthlive_client_apply_duration_seconds` — are **client-reported and therefore
untrusted input**. They are bounded on the wire and dropped unless they name a
patch actually sent to that session and still inside the ack window; they are
named `client_*` so no dashboard mistakes them for a server measurement.

---

## The provenance log

Setting `Config.Logger` also turns on one JSON record per **transition**, at
`Info`, on the logger name `gotthlive.provenance`:

<!-- sample: observability/observability.go -->
```go
func ProvenanceLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
```

Each record carries `session_id`, `event_id`, `client_ref`, `transition_id`,
`state_version`, `patch_id`, `server_seq`, `origin_kind`, `origin_source` and
`fragment_ids`, plus `contributing_event_ids` on a coalesced patch and the
supersession pair on a resync snapshot. About **200 bytes** serialized.

It is a **structured log stream, not a library-owned store**. Your existing log
pipeline is the storage and the query engine; the library holds nothing in
process and accumulates nothing. There is no separate switch — a reverse lookup
you have to remember to enable is one that is off when you need it.

A transition that emitted **no** patch still gets a record, with `patch_id` 0.
Without it the transitions that produced nothing would be invisible, and "the
state version rises exactly when state changed" would be unverifiable.

### What it looks like

Two records, one click. This is the whole design visible in a log:

```json
{"msg":"transition","logger":"gotthlive.provenance",
 "session_id":"a9a9904e3445571d376c68f5864b74d1",
 "event_id":11,"client_ref":11,"transition_id":23,"state_version":14,
 "patch_id":0,"server_seq":0,
 "origin_kind":"CLIENT_EVENT","origin_source":"event:counter.increment",
 "fragment_ids":null}

{"msg":"transition","logger":"gotthlive.provenance",
 "session_id":"a9a9904e3445571d376c68f5864b74d1",
 "event_id":0,"client_ref":0,"transition_id":24,"state_version":15,
 "patch_id":13,"server_seq":13,
 "origin_kind":"EFFECT","origin_source":"effect:counter.watch",
 "fragment_ids":["counter.value"]}
```

The first is the click: `origin_kind` is `CLIENT_EVENT`, `client_ref: 11` ties
it to the eleventh interaction that browser sent, and `patch_id` is **0**
because the reducer returned an effect and changed no state. The second is the
consequence: the subscription pump delivered a value, and `fragment_ids` names
the one region whose markup moved.

### What "every patch is traceable" buys during an incident

The question that shows up on a pager is *"a user has a screenshot of a wrong
number — where did it come from?"* With the provenance log the answer is a log
query rather than a reconstruction:

1. Take the `patch_id` off the frame the browser captured — or off the span, or
   off the client's own report.
2. Find the record with that `patch_id` in that `session_id`.
3. Read `origin_source`. If it is `event:<name>`, you have the interaction. If
   it is `effect:<name>`, find the `CLIENT_EVENT` record whose `transition_id`
   precedes it in the same session, or read `contributing_event_ids` if the
   application named one.
4. `state_version` tells you whether state actually changed, and `fragment_ids`
   tells you which region rendered.

Two properties make that answer trustworthy. **A second tab's records are
identical except for `session_id`** — every session is patched from its own
transition, there is no shared render and no broadcast frame — so a per-session
number means what it says. And **the provenance log shares no code with the
counters it is used to check**: records are emitted by the session actor from
the values that construct the frame, while the metrics are incremented in the
framer and the transport, into a different sink. Checking one against the other
is evidence rather than a value compared with itself.

### The cost of leaving `Config.Logger` nil

Provenance records are not emitted and the **reverse lookup is unavailable**.

What is *not* lost is the causal chain itself, which rides in the frames. The
distinction is easy to blur and worth keeping sharp: **the frames always carry
the chain; the provenance log is the server-side index that makes the reverse
lookup a query.** Disabling it costs queryability, never wire-level provenance.

---

## Logs

The library logs through `log/slog` and accepts your `*slog.Logger`. It never
constructs one, never sets a global, and imposes no logging dependency.

Log fields are `snake_case` and match the last two segments of the
corresponding span attribute — `patch_id` against `gotthlive.patch.id` — so a
log line and a span join without a translation table. Every library-produced
error names the session, the causal identifier where one exists, and the
actionable next step.

---

## Traces

Spans are `gotthlive.<phase>`, dotted and lower-case, and attributes are
`gotthlive.<noun>.<field>`. One span tree per event covers the boundary, the
reducer, each render, the encode and the send — with `gotthlive.encode` and
`gotthlive.send` separated deliberately, so a write stall is visible on its own
rather than folded into encoding time.

Sampling changes trace *structure*, not just volume: a dropped parent takes its
children with it. That is a reason the provenance log is exempt from sampling
and the traces are not — a sampled provenance log could not support a
"100 %, zero unknown" claim, and a sampled trace can still answer "what does a
slow event look like".

### Active browser connections

`app.ActiveConnections()` reads that application's connection registry directly.
A connection remains counted until its cleanup deregisters it; refused upgrades
never enter the count. Every widget on the same connection shares that registration,
regardless of how many effect goroutines it starts.

`gotthlive_sessions_active` follows the same registration and removal boundaries
when a MeterProvider is configured. The historical `gotthlive_connections_closed_total`
series also records origin, authentication and CSRF refusals; subtracting it from
`gotthlive_connections_total` does not compute active connections.
