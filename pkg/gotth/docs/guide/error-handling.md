# Error handling

At the end of this page you can diagnose a configuration that will not start, a
connection that closes, an event that is refused, and an effect that fails — and
you will know which of those a user can see.

Compiled source: [`_samples/errorhandling`](_samples/errorhandling).

---

## Startup: `ConfigError`

`live.New` validates the whole `Config` and returns a `*live.ConfigError` naming
the field at fault and what to set it to. There is one error type and no
sentinels, because the text is more actionable than an `errors.Is` target.

<!-- sample: errorhandling/errors.go -->
```go
func Start[S any, I live.IIdentity](cfg live.Config[S, I]) (*live.App[S, I], error) {
	app, err := live.New(cfg)
	if err == nil {
		return app, nil
	}

	var cfgErr *live.ConfigError
	if errors.As(err, &cfgErr) {
		return nil, fmt.Errorf("live config field %q: %s", cfgErr.Field, cfgErr.Detail)
	}
	return nil, err
}
```

What it refuses:

| `Field` | Because |
|---|---|
| `Init`, `Reduce` | missing hook |
| `Fragments` | empty, or a duplicate/invalid ID |
| `Events` | empty, or an entry that is the empty string |
| `Origins` | empty and no `live.AnyOrigin` |
| `Authenticate`, `Authorize`, `CSRF` | missing hook and no named escape hatch |
| `Limits.<name>` | negative, or `CoalesceFlushAt` above **959** |
| `Metrics` | the meter provider could not be used |

**Every one of those is a startup mistake, and finding it at startup is the
difference between a failed deploy and a session that misbehaves in
production.** A `Limits` field is refused rather than clamped, on the same
ground: a limit that silently becomes a different limit is not a limit an
operator can reason about.

---

## Before the WebSocket exists: the handshake

The order below is the security property — authenticate before allocating any
per-session memory — and its consequence is that **the three checks most likely
to reject you are HTTP statuses, not close codes**:

| Step | On failure |
|---|---|
| origin allowlist | `403 forbidden origin` |
| `Authenticate` | `401 unauthenticated` |
| `CSRF` | `403 forbidden` |
| subprotocol `gotth-live.v1` | `426` |
| accept `101` | — the session identifier is minted **only now**, and the actor spawned |

The client runtime cannot distinguish those. A handshake that fails surfaces to
it as an abnormal close, which is not in its terminal set, so **it retries
forever with backoff**. A page that reconnects every few seconds while your log
fills with 403s is the origin allowlist, every time.

`gotthlive_connections_closed_total` still counts these, under the labels
`forbidden_origin` and `unauthenticated` — which is why those names appear both
here and in the close-code table below.

---

## The close codes a developer sees

The private range **4000–4999**, for a session that was established. Every
`Close` call site in the library names one of these, and a test enumerates the
call sites to keep that true.

| Code | Name | Means | Usually |
|---|---|---|---|
| 4000 | `NORMAL` | closed cleanly | you called `Close`, or the tab went away |
| 4001 | `GOING_AWAY` | server shutting down or draining | `App.Close` |
| 4002 | `PROTOCOL_VIOLATION` | a text frame, non-`Frame` bytes, or a broken invariant | something between you and the browser is rewriting frames |
| 4003 | `UNSUPPORTED_VERSION` | major protocol version mismatch | a stale page against a new binary |
| 4004 | `UNAUTHENTICATED` | the identity hook failed | mostly a **metric label** — see the handshake section above |
| 4005 | `FORBIDDEN_ORIGIN` | the origin allowlist refused it | likewise; on the wire this is a `403` on the upgrade |
| 4006 | `UNAUTHORIZED` | `Authorize` returned a `*FatalDenyError` | |
| 4007 | `FRAME_TOO_LARGE` | over `Limits.MaxInboundFrameBytes` (default **65536**) | a form with a very large field |
| 4008 | `RATE_LIMITED` | the inbound bucket, or the independent resync bucket | over `MaxEventsPerSecond` (**50**) / `EventBurst` (**100**), or `MinResyncInterval` (**1 s**) / `ResyncBurst` (**3**) |
| 4009 | `SLOW_CLIENT` | the outbound window stayed full past `SlowClientGrace` (**30 s**) | the browser cannot keep up, or the network is gone |
| 4010 | `HEARTBEAT_TIMEOUT` | no heartbeat within `HeartbeatTimeout` (**50 s**) | a proxy idled the connection out; check it against `HeartbeatInterval` (**20 s**) |
| 4011 | `SESSION_EVICTED` | no inbound frame for `IdleTimeout` (**30 min**) | working as intended |
| 4012 | `INTERNAL_ERROR` | a contained panic that could not be recovered into the session, or `Limits.PanicBudget` (**3**) exhausted at one site | |
| 4013 | `RESYNC_FAILED` | a resync could not produce a consistent snapshot | |

**Five of them are terminal to the client runtime**: 4000, 4003, 4004, 4005 and
4006. The runtime does not retry those, because an unsupported version, a
rejected origin, or a failed identity check will fail again identically —
retrying would be a loop rather than a recovery. `data-gotth-status` on `<html>`
goes to `closed` and stays there; no timer is armed and no visibility change
revives it.

**Everything else reconnects**, with `data-gotth-status="reconnecting"` and a
full-jitter backoff — `delay = random(0, min(15 s, 250 ms · 2ⁿ))`, unlimited
attempts, and no timer at all while the tab is hidden. The DOM stays exactly as
the last applied patch left it: frozen, scrollable, focusable, and fully
interactive. Nothing in the runtime disables a control, deliberately — the HTMX
regions, links and native inputs on the page are yours, and freezing the live
regions is not a licence to take the page away from the user.

A reconnect is a **new session**: fresh `Init`, fresh snapshot, fresh identity
binding.

---

## Error frames: refusals that do not close the connection

An `Error` frame carries a code, a message capped at **512 bytes**, the causal
identifiers, and a `fatal` flag. The codes are `UNSUPPORTED_VERSION`,
`UNAUTHORIZED`, `INVALID_FRAME`, `UNKNOWN_EVENT`, `UNKNOWN_FRAGMENT`,
`RATE_LIMITED`, `INTERNAL` and `RESYNC_FAILED`.

The client dispatches every one of them as a DOM event you can listen for:

```js
document.addEventListener("gotth-live:error", (e) => console.warn(e.detail));
```

The two you will meet while building:

- **`UNKNOWN_EVENT`** — a binding names an event that is not in
  `Config.Events`. Default-deny: refused, counted in
  `gotthlive_events_rejected_total{reason="unknown_event"}`, never dispatched
  and never ignored. Almost always a typo, or a constant used on one side and a
  literal on the other.
- **`UNAUTHORIZED`, non-fatal** — `Authorize` returned a `*live.DenyError`. The
  event is rejected, no state changes, the session continues.

**`DenyError.Reason` is operator-facing.** A generic message reaches the client
in production, because an authorization reason is an authorization input.

---

## Panics are contained, per session

A panic in a reducer, in a fragment's `Render`, or in a fragment's `Dirty` is
recovered, contained to the session it happened in, logged at error level with
the causal identifiers and the stack, counted in
`gotthlive_panics_total{site}`, and answered with an `Error` frame naming the
event that caused it.

- A **reducer** panic leaves the pre-transition state intact and correct — that
  is what the no-mutation rule buys — and emits no patch.
- A **render** panic leaves one region stale and lets every other fragment in
  the same transition patch normally.
- Neither closes the session on its own. A site that panics
  `Limits.PanicBudget` times in one session does, with **4012
  `INTERNAL_ERROR`**. No other session is affected either way.

One `Error` frame is emitted per render pass, not per broken fragment: in
production the message is a fixed string, so repeating it once per fragment
would add no information. The per-fragment record is the log line and the
metric.

`Config.Dev` is what decides how much of a panic reaches the browser, and **it
is the only thing that field does**. In production the frame carries a fixed
generic message and the causal identifiers; with `Dev` set, it also carries the
panic value and its stack, truncated to the 512-byte message cap. The full stack
goes to `Logger` at error level in both modes.

---

## Failed effects reach the reducer

A panicking effect is the deliberate exception to the paragraph above: it
becomes an `EffectFailedEvent` rather than an `Error` frame, because a failure
the reducer can see is replayable and one that only reaches the wire is not.

<!-- sample: errorhandling/errors.go -->
```go
func Reducer(reporter *Reporter) live.Reducer[State, live.AnonymousIdentity] {
	return func(s State, ev live.Event) (State, []live.Effect[live.AnonymousIdentity]) {
		if ev.Name != live.EffectFailedEvent {
			return s, nil
		}

		source := ev.Fields.Get(live.EffectFailedSourceField)

		s.Notice = "could not refresh " + source

		retryable, _ := strconv.ParseBool(ev.Fields.Get(live.EffectFailedRetryableField))

		if retryable && source == SourceFetch && s.Attempts < 3 {
			s.Attempts++
			return s, []live.Effect[live.AnonymousIdentity]{reporter.FetchEffect()}
		}
		return s, nil
	}
}
```

Three things that reducer does **not** do are each worth a sentence, because
each was wrong on this page until 2026-08-05:

- **It does not log.** That rule has a section of its own below.
- **It does not retry an unclassified failure.** `retryable` is parsed rather
  than assumed, and an absent or unparseable value is `false` — see
  [Classification](#classification).
- **It counts retries in `State`, not in a counter.** `Attempts` is state, so
  replaying the event log reaches the same number; a metric incremented from
  inside the reducer would not.

`live.EffectFailedEvent` is an exported constant — **do not type the string.**
Before the constant existed, this library's own counter example hard-coded a
name nothing emits, and its tests passed, because the reducer's default branch
does nothing. A failure path that has never run is the one you are relying on.

The event is not in `Config.Events` and must not be: registration is what makes
a name sendable by a browser, and this one is minted by the library.

### The disclosure rule, stated once

> **Render `EffectFailedSourceField`. Never render `EffectFailedErrorField`.**

`EffectFailedErrorField` carries the error's own message, or the panic value,
**verbatim, in production, unredacted, and ungated by `Config.Dev`**. That is
right for a reducer, which is server code and needs the detail to be actionable.
It also means the string is whatever an upstream library chose to put in an
error: a connection string, a query, an internal hostname, a stack-shaped panic
value.

Error frames carry a fixed generic message in production for exactly this
reason. The failure event is a second path to the same disclosure and carries no
such discipline, because only your application knows what its own effects put in
their errors. `EffectFailedSourceField` is a name you chose, so it is the value
that is safe to show.

### The logging rule, and why the reducer is the wrong place

> **Do not log from inside the reducer.** Log the failure from the effect's own
> `Run`, which is already at the actor boundary.

This is not a style preference. FR-16 names *"logging of application data"* as
I/O and requires it to run on the session actor **after** the reducer returns,
"never inside it"; FR-14 says a reducer performs no I/O at all. The reason is
replay: a reducer that logs turns the same event log into a different sequence
of records on every run, and determinism is the property the reducer is written
for — the property [testing-your-app.md](testing-your-app.md) holds it to.

So the three fields split by what a reducer is allowed to do with them:

| Field | In the reducer | Where the log line goes |
|---|---|---|
| `EffectFailedSourceField` | render it, branch on it | — |
| `EffectFailedErrorField` | **never render it, never log it** | the effect's own `Run`, or the `slog.Handler` you give `Config.Logger` |
| `EffectFailedRetryableField` | branch on it | — |

The effect holds the error **value**, which is strictly more than the event
carries: it can classify it, unwrap it, or pull structured fields off it with
`errors.As`, none of which survives the flattening into the event's string.

<!-- sample: errorhandling/errors.go -->
```go
func (r *Reporter) FetchEffect() live.Effect[live.AnonymousIdentity] {
	return live.Effect[live.AnonymousIdentity]{
		Source: SourceFetch,
		Run: func(ctx context.Context, sess live.Session[live.AnonymousIdentity], emit live.Emitter) error {
			err := r.Fetch(ctx)
			if err == nil {
				return nil
			}

			r.Log.ErrorContext(ctx, "effect failed",
				slog.String("session", sess.ID().String()),
				slog.String("source", SourceFetch),
				slog.String("error", err.Error()),
				slog.Bool("retryable", live.IsRetryable(err)))
			return err
		},
	}
}
```

**The library writes no record of an error your effect returns.** It turns the
error into the failure event and counts it in
`gotthlive_effects_total{result="error"}` when `Config.Metrics` is set — that is
all. The line above is the only one there will be, which is the concrete reason
it belongs in the effect rather than in the reducer that reads the event a
moment later.

**A panicking effect is the other half, and it has a different logger.** It
never reaches that `return err`: the library recovers it, logs it at error level
to `Config.Logger` with the session, the effect source, the event that scheduled
it and the stack, counts `gotthlive_panics_total{site="effect"}`, and
synthesizes the same failure event classified **terminal**. Logging from your
effects and leaving `Logger` nil therefore logs one half of your effect failures
and drops the other half silently:

<!-- sample: errorhandling/errors.go -->
```go
func WireLogging(cfg live.Config[State, live.AnonymousIdentity], r *Reporter, logger *slog.Logger) live.Config[State, live.AnonymousIdentity] {
	cfg.Reduce = Reducer(r)
	cfg.Logger = logger
	return cfg
}
```

> This page taught the opposite until 2026-08-05: the sample above used to call
> `slog.Warn` with all three fields **from inside `Reduce`**, and the library's
> own godoc on `EffectFailedErrorField` said "Log it, count it, branch on it" in
> a paragraph about what a reducer may render. Both are fixed. The deviation is
> recorded as **E-2** in [`../exceptions.md`](../exceptions.md), which is the
> register FR-20 requires — a page that quietly corrects itself teaches the fix
> and hides the failure mode, and the failure mode here was a plausible reading
> of the library's own documentation.

### Classification

<!-- sample: errorhandling/errors.go -->
```go
func ExecuteWithClassification(transient bool) error {
	if transient {
		return live.Retryable(fmt.Errorf("report.fetch: upstream timed out"))
	}
	return fmt.Errorf("report.fetch: no such report")
}
```

`live.IsRetryable` reads the mark back off an error you already hold — an
executor deciding between its own retry and handing the decision up, and the
spec that checks it decided correctly. It works through `errors.As`, so the mark
survives arbitrary `%w` wrapping in either direction:

<!-- sample: errorhandling/errors.go -->
```go
func WasTransient(err error) bool { return live.IsRetryable(fmt.Errorf("wrapped: %w", err)) }
```

Do not assert on "the error wraps something" as a stand-in. That is an assertion
about wrapping standing in for one about classification, and it passes for any
error that happens to wrap — including one where the mark was deleted.

The reader a **reducer** wants is still the field on the event, because what a
reducer holds is an event.

---

## A short diagnosis table

| Symptom | Look at |
|---|---|
| page loads, nothing is ever live | the `<script>` tag's `src` — a 404 means the mount path and `live.Script` disagree |
| status flips between `connecting` and `reconnecting` forever | the handshake response, not the console. 403 is the origin allowlist; 401 is your `Authenticate` |
| status goes to `closed` and stays | a terminal close code: 4000, 4003, 4004, 4005 or 4006 |
| a click does nothing, no frame is sent | the control is not inside a `data-gotth-region` element |
| a click sends a frame and gets `UNKNOWN_EVENT` | the name is not in `Config.Events` |
| one region stops updating, the rest are fine | an under-declared `Dirty`; run `livetest.AssertDirtyComplete` |
| everything updates on every event | `State` is not comparable — a slice or map field, or a `time.Time` |
| status flaps to `reconnecting` every ~50 s | `HeartbeatInterval` is above an idle timeout somewhere in the path |
| `SLOW_CLIENT` under load | watch `gotthlive_outbound_window_depth` and `gotthlive_patches_coalesced_total`; they move first |
| an effect's failure is invisible | the reducer has no `live.EffectFailedEvent` case |
| effects fail and nothing is logged | your executor returns the error without logging it — the library logs only the panicking half, and only if `Config.Logger` is set |
| replaying one event log produces different logs each run | something inside the reducer is doing I/O; a `slog` call is the usual one |

Every library-produced error names the session, the causal identifier where one
exists, and the actionable next step. An error that does not is a defect worth
reporting.
