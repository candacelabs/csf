# Deploying

At the end of this page you can ship a gotth-live application: what the binary
contains, what a reverse proxy in front of it has to be told, what a deploy does
to the sessions that are open, and what happens the moment you run a second
replica — which is the part of this page most likely to change your
architecture.

Compiled source: [`_samples/deploying`](_samples/deploying).

---

## One binary, and what that buys

`go build` produces one file. The client runtime is `//go:embed`ed into the
`live` package and served by the same handler that serves the connection, so
there is no CDN origin to configure, no `npm` in the image, no asset pipeline,
and nothing to publish anywhere.

Two numbers, both measured, both with their method:

| | |
|---|---|
| The shipped client runtime | **10,387 B** minified, **4,459 B** gzipped, against the NFR-2 ceiling of 12,288 — 7,829 B headroom, 63.7 %. Measured by `candace/pkg/gotth/tools/minify` over the artifact `live` actually embeds; **`client/SIZE.md` is the ledger and this row is a copy of it** — [`client/SIZE.md`](../../client/SIZE.md) |
| A complete linked application | **16,171,201 B** for `examples/counter`, **+14,349,666 B** over an empty Go module's floor. Attributed line by line in [`docs/dependencies.md`](../dependencies.md) §5.3, where the largest single contributor turns out to be dead-code elimination ceasing to apply once something calls `live.New`, not any dependency |

> **⟨CORRECTED 2026-08-05 — the runtime row said *"re-measured on every
> landing"*, and that clause was the finding.⟩** It read **10,391 B / 4,429 B**,
> which was last true at `2ab18690^` — two landings and three days stale, while
> claiming in the same sentence to be re-measured on every one of them. A page
> that advertises its own freshness policy and does not keep it is worse than one
> that says nothing, because it tells a deployer not to check.
> `reviews/fr-54.md` §8 (L9-1) found it and called the clause self-refuting; the
> figures here are `tools/minify -check` at HEAD.
> **The clause is removed rather than repaired.** This row is a *copy*, and the
> honest thing for a copy to do is name its source and admit it can lag —
> `client/SIZE.md` §1 is the ledger NFR-2 is gated on, it is what `ci.sh`
> enforces, and it is what to read if this row and it ever disagree again.

A consumer's `go.mod` gains **one** module — `google.golang.org/protobuf`.
That is also measured, in the same document.

**`templ` is a build-time tool, not a runtime one.** You need `templ generate`
in the pipeline that produces your `*_templ.go`, or you commit the generated
files as this repository does; the running binary needs neither `templ` nor
`protoc` nor a generator. The three examples are `git clone && go run .` with no
node, npm, protoc or generator installed, because their generated code is
committed.

### The runtime is served immutable, from a path that does not change

The handler answers `<mount>/gotth-live.min.js` with a strong `ETag`,
`X-Content-Type-Options: nosniff`, and:

```text
Cache-Control: public, max-age=31536000, immutable
```

That is right for the common case — a page that reconnects a thousand times
fetches the runtime once, and a conditional request gets a `304` — and it has a
consequence worth knowing before an upgrade rather than after. **The URL carries
no build fingerprint.** `live.Script` renders `src="<mount>/gotth-live.min.js"`
for every build of every version, so a browser holding a cached response will
not revalidate it for a year, and `immutable` tells it not to revalidate on an
ordinary reload either.

Within a protocol major version that is usually harmless: an old runtime and a
new server still speak `gotth-live.v1`, and the version check that would close
the session (`4003 UNSUPPORTED_VERSION`) only fires on a **major** mismatch. If
you need a runtime change to reach browsers sooner than the cache expires, the
levers available today are outside the library: change the mount path, or have
your proxy rewrite the `Cache-Control` on that one path. There is no
fingerprinted-URL option on `live.Script`, and this page would rather say so
than let you find out during an incident.

> **⟨CORRECTED 2026-08-05 — *"within a protocol major version that is usually
> harmless"* is false, and it was driven rather than argued.
> `reviews/fr-54.md` §4, condition FR54-1 (L9-1).⟩**
>
> The paragraph above reasons about **one** contract. There are **two**, and the
> version check only covers the first.
>
> | | The contract | Versioned? | What closes the session when it breaks |
> |---|---|---|---|
> | 1 | **The wire protocol** — the frames, `gotth-live.v1` | **Yes**, and negotiated on the subprotocol | `4003 UNSUPPORTED_VERSION`, on a major mismatch |
> | 2 | **The attribute grammar** — the `data-gotth-*` markup the helpers emit and the runtime parses (`client/SIZE.md` §7) | **No.** It has no version, no negotiation and no handshake | **Nothing.** There is no check to fail |
>
> A cached runtime is only *"usually harmless"* with respect to contract 1. When
> a landing changes contract 2 — as FR-54's per-binding options did, and as
> `Bind.NoModifiers` and `Bind.PreventDefault` did after it — an old runtime
> parses new markup with an **older grammar**, reads the components it knows,
> and **silently ignores the rest**. Both artifacts ship in one binary; **they do
> not arrive at one browser.**
>
> **Driven, not derived.** L9-1 ran `client/runtime.js` from before the
> per-binding-options landing against markup rendered by HEAD's helpers:
>
> ```
> OLD runtime + NEW markup
>   armed timers: 0  | events on the wire: ["c.draft"]   <- the declared 150 ms debounce is gone
>   event: f.one     | fields delivered: []              <- Bind.Fields{room:"alpha"} is gone
> ```
>
> **No error. No console warning. No `4003`. No close code at all.** A page that
> asked for a 150 ms debounce sends a frame per keystroke, and an event that
> declared a static field arrives with *no fields* — which changes what your
> reducer computes, not merely how often it runs.
>
> **What to do about it, since that is what this page is for.** Treat *any*
> release whose notes mention the binding grammar — `Bind` gaining a field,
> `On`/`OnWith`/`OnAll` changing what they emit, `client/SIZE.md` §7 moving — as
> a release that **needs the cache broken**, at the same priority you would give
> a protocol major bump. The two levers are the ones named above and they are
> unchanged: **change the mount path** so the URL is new, or **have your proxy
> rewrite `Cache-Control` on that one path** for the duration of the rollout.
> Neither is inside the library.
>
> **How you would find out otherwise**, in rough order of how long it takes:
> `data-gotth-status` still reads `live` and the connection is healthy, so
> nothing on the status path will tell you; the symptom is traffic volume and
> missing payload fields, so **compare the frames a real browser sends against
> what the markup declares** — the dev inspector
> ([`inspector.md`](inspector.md)) shows exactly that, and it is the fastest
> check available. **An ordinary reload does not clear it** — that is precisely
> what `immutable` buys. A *hard* reload (`Ctrl`/`Cmd`+`Shift`+`R`) does bypass
> the cache and will, which makes it a fine way for **you** to confirm the
> diagnosis and no way at all to fix a deployment: you cannot ask every user to
> perform one, and the users who need it most are the ones who will never see
> the instruction.
>
> **This is a documentation condition, not a design one.** No fingerprinted-URL
> option is being asked for here, and the disclosure two paragraphs up — that
> the library has no such lever — is correct and is why this is worth saying
> plainly rather than fixing quietly. `docs/api-surface.md` §10 carries the
> other half of this correction, against the ledger row that claimed *"there is
> no mixed-version window."*

---

## The reverse proxy

This is where a working application most often becomes a broken deployment, and
almost all of it comes from one fact: **the library holds one long-lived
WebSocket per browser tab.** Everything below follows from that and from the
transport, which is specified in [`adr/001-transport.md`](../adr/001-transport.md)
and [`protocol.md`](../protocol.md).

**Where this section's authority comes from, stated before you rely on it.**
Every claim below is read out of this repository's own code and wire
specification, and the sample's specs assert the ones that are behaviour. **None
of it has been measured end to end through a real reverse proxy.** ADR-001's
criterion **X6** — "upgrade succeeds through this repo's Caddy edge with no
Caddyfile change" — is written down with a Phase 2 integration test named as its
evidence, and no artifact in this tree records that test having run. So read
this as "what the transport requires", not as "what has been observed through
nginx, Caddy, an ALB or an ingress controller", and verify the first deployment
rather than assuming it.

### What actually goes over the wire

One WebSocket message is exactly one encoded `gotthlive.v1.Frame`, sent as a
**binary** frame (opcode `0x2`). There is no JSON, no text framing, and no
debug escape hatch — a received text frame is a protocol violation and closes
the session with `4002`.

The handshake is an ordinary HTTP/1.1 upgrade:

```text
GET /live HTTP/1.1
Host: app.example.com
Connection: Upgrade
Upgrade: websocket
Origin: https://app.example.com
Sec-WebSocket-Version: 13
Sec-WebSocket-Key: ...
Sec-WebSocket-Protocol: gotth-live.v1
```

### Response buffering: a non-issue, unlike SSE

You do **not** need `proxy_buffering off`, and you do **not** need
`X-Accel-Buffering: no`. A WebSocket is not a buffered HTTP response body; once
the `101` is answered the proxy is relaying frames. This is the one place a
gotth-live deployment is easier than the server-sent-events design it was
weighed against, and it is worth stating precisely so nobody copies an SSE
runbook onto it.

### Idle timeouts: the one number the network makes your business

This is the setting that breaks deployments.

**The rule: `Limits.HeartbeatInterval` must be shorter than the shortest idle
timeout anywhere in the path** — the load balancer's, the reverse proxy's, any
NAT, any service mesh. The defaults are an interval of **20 s** and a peer-dead
timeout of **50 s**.

Two details that decide whether the rule works:

- **The heartbeat is an application-level `Heartbeat` frame, not an RFC 6455
  ping.** It is real traffic on the connection in both directions, so an
  intermediary that measures idleness in bytes sees it. An intermediary that
  measures something else will not be fooled by it either way.
- **`HeartbeatTimeout` must span at least two intervals**, and `live.New`
  refuses the pair otherwise, naming `Limits.HeartbeatTimeout`. One interval is
  the bare correctness bound — a quiet session's only inbound frame is the echo
  of the heartbeat a tick carried — and two is what lets a healthy client lose
  one solicitation without being closed.

`Production` derives both from the one number you know:

<!-- sample: deploying/deploying.go -->
```go
func Production[S any, I live.IIdentity](cfg live.Config[S, I], proxyIdle time.Duration, maxSessions int) live.Config[S, I] {
	cfg.Dev = false
	cfg.DevBuildID = ""

	interval := proxyIdle / 3
	if interval > MaxHeartbeatInterval {
		interval = MaxHeartbeatInterval
	}
	cfg.Limits.HeartbeatInterval = interval
	cfg.Limits.HeartbeatTimeout = interval * 5 / 2

	cfg.Limits.MaxSessions = maxSessions
	return cfg
}
```

A third of the idle timeout leaves room for one lost solicitation inside the
window, and the 5:2 ratio between timeout and interval is the one the library's
own defaults use. The clamp is the protocol's: `heartbeat_interval_ms` is a
refined field of the mount snapshot and is admitted only between **1 s** and
**5 min**, so a path that idles out in under three seconds produces a Config
`live.New` refuses rather than a server every session of which dies at
establishment. That refusal is asserted in the sample's spec, along with the
whole table of realistic idle timeouts the derivation is run against.

**When you get it wrong**, the symptom is
`gotthlive_connections_closed_total{code="heartbeat_timeout"}` rising and pages
that flap to `reconnecting` on a fixed cycle. That metric is the diagnosis; the
close code is `4010`.

### Upgrade headers

An intermediary that strips or rewrites `Connection: Upgrade` and
`Upgrade: websocket` breaks this library completely, and there is no fallback
transport in v0.1 — the ADR chose WebSocket knowing this and says so.

**The failure is quiet, and you should know what it looks like.** The client
runtime writes exactly four values to `data-gotth-status` on `<html>`:
`connecting`, `live`, `reconnecting`, `closed`. There is no `offline` value and
the runtime writes nothing to the console at all. So a stripped `Upgrade`, a
refused `Origin`, and a proxy that answers the upgrade path with an HTML error
page all present identically: `data-gotth-status="reconnecting"`, a full-jitter
retry that never gives up, and a page whose live regions are frozen at the last
patch while the rest of it stays interactive. **The diagnosis is the handshake's
HTTP status in your access log, not the browser.**

| Status on the upgrade | Meaning |
|---|---|
| `101` | it worked |
| `403` | the origin allowlist refused it — or `Config.CSRF` did, which is counted under the same metric label |
| `401` | `Config.Authenticate` refused it |
| `426` | the request did not offer the `gotth-live.v1` subprotocol; usually an intermediary rewriting headers |
| `503` | a session limit — `Limits.MaxSessions` or `Limits.MaxSessionsPerIdentity` |
| anything else, or an HTML error page | something in the path is answering instead of your process |

### One access-log line per session, not per interaction

`App.Handler()` **returns at the upgrade**. The session then runs on a goroutine
the library owns, for as long as the connection lasts. Two operational
consequences:

- Middleware wrapping the handler completes at the handshake. A request-scoped
  logger, timer, or duration histogram records a handshake, not a connection —
  which is what lets a request timeout mean what it says, and what makes your
  p99 request duration meaningless as a measure of session health.
- Every per-event number has to come from this library's own instrumentation.
  That is the honest cost of one connection per tab, and it is why
  [observability.md](observability.md) is a page rather than a footnote.

Do not put a request-duration limit or an HTTP-level timeout on the mount path
expecting it to bound a session. It bounds a handshake.

### `http.Server` fields

<!-- sample: none — net/http's own struct, not this library's API -->
```go
srv := &http.Server{
	Handler:           mux,
	ReadHeaderTimeout: 5 * time.Second,
	// No WriteTimeout.
}
```

Leave `WriteTimeout` zero. It is a deadline set on the underlying connection
before the handler runs, and a live session is a hijacked connection that
inherits it — so a non-zero value cuts sessions off mid-life on a fixed
schedule. The per-write bound that belongs to this library is
`Limits.WriteDeadline` (default **five seconds**), which is applied per write
and feeds the slow-client eviction rather than killing a healthy connection.

---

## TLS terminates outside the process

The library speaks plain HTTP and never touches TLS. Terminate at your load
balancer, ingress, or reverse proxy, and give it a `wss://`-capable listener;
nothing in the configuration changes on this side except that your
`Config.Origins` entries are `https://` and the browser's WebSocket URL becomes
`wss://`. The client derives that itself from the page's own scheme.

Two things follow, and both are stated rather than assumed:

- **The measured per-session memory figure excludes TLS**, deliberately, because
  the measurement terminates TLS outside the measured container. If you
  terminate in-process instead, the per-connection cost is yours to measure and
  the number below does not cover it.
- **A private hop behind an authenticating edge is an ordinary design here.**
  Public TLS and authentication at the proxy, followed by a private connection
  to this process, is what `Config.Origins`, `Config.Authenticate` and
  `Config.CSRF` are configured against — see [security.md](security.md) for
  which of them is doing what in that shape.

---

## Which `Config` knobs are production-relevant

**`Config.Dev` must be false in production.** It is not an escape hatch and it
is not a log-level: it gates three separate things, and each of them is a
developer tool in front of the public.

| `Dev` turns on | What it exposes |
|---|---|
| Panic detail in the `Error` frame | the panic value and its stack, truncated to the 512-byte message cap, delivered to the browser. Production sends a fixed generic message and the causal identifiers |
| The session inspector | `<mount>/gotth-live-inspector.min.js` is served, and `(*App).InspectorScript` renders the tag. With `Dev` false the route is **404** and the component writes nothing |
| Dev reload | `<mount>/gotth-live-dev-reload.min.js` and `<mount>/gotth-live-dev-build` are served, and `(*App).DevReloadScript` renders the tag. Both **404** with `Dev` false |

`DevBuildID` is read only when `Dev` is set, so it is cleared alongside it. How
to *verify* all of this on a deployed process, rather than trust it, is in
[security.md](security.md#the-dev-only-routes-and-how-to-check-they-are-off).

The rest, sorted by whether a deployment has to think about them:

| Field | Default | Set it in production? |
|---|---|---|
| `Limits.MaxSessions` | **0 — unlimited** | **Yes.** This is the one default an operator should not keep: it is the difference between a memory ceiling and a memory bill |
| `Limits.HeartbeatInterval` / `HeartbeatTimeout` | 20 s / 50 s | **Yes, if anything in the path idles out sooner than 50 s.** See above |
| `Limits.MaxSessionsPerIdentity` | 20 | Only if one subject legitimately opens more tabs than that, or you want it tighter |
| `Logger`, `Metrics`, `Tracer` | nil | **Yes** — nil disables the provenance log and with it the reverse lookup from a captured patch back to its cause. [observability.md](observability.md) |
| `Limits.IdleTimeout` | 30 min | Only to trade session eviction against memory |
| `Limits.MaxInboundFrameBytes` | 65,536 | Only if a form legitimately carries more. It must stay between 1,024 and 1,048,576 |
| `Limits.EffectDrainTimeout` | 5 s | Only if your effects are slower than that; it bounds what `App.Close` waits for |
| `Limits.WriteDeadline`, `SlowClientGrace`, `AckWindow`, `MailboxDepth`, `AckChannelDepth`, `CoalesceFlushAt`, `EventBurst`, `MaxEventsPerSecond`, `MinResyncInterval`, `ResyncBurst`, `PanicBudget` | see [error-handling.md](error-handling.md) | Rarely. They are safe by default and every one of them is validated at `live.New` rather than clamped |

Every `Limits` field is **refused** rather than clamped when it is out of range,
and the error names the field. A limit that silently becomes a different limit
is not a limit an operator can reason about.

---

## Graceful shutdown, and what a deploy does to a live session

There are two shutdowns to perform and they are not interchangeable.
`http.Server.Shutdown` **does not close and does not wait for hijacked
connections**, and every live session is one — so `Shutdown` returns with every
session still open. `App.Close` is what drains them.

<!-- sample: deploying/deploying.go -->
```go
func (d *Deployment) Run(ctx context.Context) error {
	serving := make(chan error, 1)
	go func() { serving <- d.Server.Serve(d.Listener) }()

	select {
	case err := <-serving:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}

	d.Ready.Drain()

	shutdown, cancel := context.WithTimeout(context.Background(), d.Grace)
	defer cancel()

	if err := d.Server.Shutdown(shutdown); err != nil {
		return fmt.Errorf("gotth-live: the HTTP server did not shut down within the grace period: %w", err)
	}
	return d.DrainSessions(shutdown)
}
```

`DrainSessions` is `(*live.App[S, I]).Close`, whose signature that is. The order is
the whole content of the function, and the sample's spec asserts it: readiness
fails **before** the sessions are drained, and the drain happens exactly once.

**What `App.Close` guarantees.** Every session is closed with **4001
`GOING_AWAY`**, `Teardown` runs for each, and in-flight effects are waited for
up to the context's deadline. "Every session" is exact and is held by a spec
rather than by that sentence: a connection admitted but not yet registered when
`Close` begins is **refused** and closed with the same code, so there is no
interval in which `Close` returns `nil` over a session it did not touch. After
`Close` the handler refuses new upgrades and the `App` is not reusable.

**What every browser then does.** `4001` is not in the client's terminal set, so
each page goes to `data-gotth-status="reconnecting"` and retries with a
full-jitter backoff — `delay = random(0, min(15 s, 250 ms · 2ⁿ))`, unlimited
attempts, and no timer at all while the tab is hidden. The jitter is there
specifically so a deploy does not produce a remount storm. When a reconnect
succeeds it is a **new session**: fresh `Init`, fresh `Snapshot`, fresh identity
binding, new `session_id`.

**And what is lost.** Everything the process was holding. There is no durable
session state across a restart: any state your `Init` cannot rebuild from your
own store is gone, and the user's live regions repaint from the new snapshot.
The DOM stays exactly as the last patch left it in the meantime — frozen,
scrollable, focusable, and fully interactive, because nothing in the runtime
disables a control.

Size the grace period above `Limits.EffectDrainTimeout` (default five seconds),
and above whatever your own effects need. If the deadline passes with effects
still in flight, `App.Close` returns an error and the effects are abandoned and
counted.

---

## Health checks

The library ships no health endpoint, and it exposes no session count on `App`.
That is deliberate: how many sessions are open is
`gotthlive_sessions_active`, which is a metric, and a health check that reports
a gauge is a health check that flaps.

What a gotth-live process specifically needs is a **readiness** signal that can
be failed *before* the process stops accepting, because a session that lands on
a draining replica gets a connection, a snapshot, and a close a moment later —
and the user sees a page that connected and reconnected for no reason they can
observe.

<!-- sample: deploying/deploying.go -->
```go
type Readiness struct{ draining atomic.Bool }

func (r *Readiness) Drain() { r.draining.Store(true) }

func (r *Readiness) ServeHTTP(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	if r.draining.Load() {
		http.Error(w, "draining", http.StatusServiceUnavailable)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, "ok\n")
}
```

Mount it on a plain HTTP path — it is an ordinary handler and has nothing to do
with the live mount — and point the platform's readiness probe at it. A liveness
probe can be the same handler ignoring the flag, or nothing at all: a Go process
that has stopped answering HTTP is a process the platform will notice anyway.

```yaml
readinessProbe:
  httpGet: { path: /readyz, port: 8080 }
  periodSeconds: 2
livenessProbe:
  httpGet: { path: /healthz, port: 8080 }
  periodSeconds: 10
```

Set the pod's `terminationGracePeriodSeconds` above the `Grace` you passed to
`Run`, or the platform kills the process in the middle of the drain it asked
for.

**Do not probe the live mount.** A probe that opens a WebSocket allocates a
session, runs `Init`, and mints a `session_id` every few seconds, which shows up
as a permanent floor under `gotthlive_sessions_active` and as churn in the
provenance log. A `GET` to the mount without upgrade headers is not free either:
it reaches the origin check first and answers `403`, which will look like an
attack in your logs.

---

## Horizontal scale, and exactly what breaks

**One process owns one session, and there is no second process that can help.**
This is the load-bearing consequence of the design, not an implementation gap:
the actor model, the resync story and the failure modes all assume it.
Multi-node — session migration, cross-node pubsub, sticky routing — is backlog
item **BL-1**, and PRD risk **R-14** records the consequence in the PRD's own
words: *any later multi-node work is a redesign, not an extension.*

Read the following as three separate claims, because they are usually confused
with each other.

**1. A second replica does nothing to an existing session, and that is the good
news.** A session lives entirely inside the process that accepted its upgrade,
over a connection that was established once. Scaling out does not migrate it,
disturb it, or split its state. There is no distributed state to become
inconsistent, because there is no distributed state.

**2. A second replica does not share application state, and that is the bad
news.** Two tabs of the same page that land on different replicas see different
worlds unless *your* application is what keeps them in step. The library's
sharing story is `Config.Execute` reaching an application-owned store, and if
that store is a Go map in the process — which is what all three examples ship —
then it is per-replica. The moment you run two, that map has to become something
both replicas can reach: Postgres, Redis, NATS, whatever you already run. The
library does not provide one, does not wrap one, and does not know you have
added one.

**3. Session affinity is not required for correctness, and is worth having
anyway.** A session cannot outlive its connection, so there is no "session on
the wrong node" state to be in: whichever replica accepts the upgrade owns that
session completely, and a reconnect after a close is a brand-new session that is
free to land anywhere. So you do not need sticky sessions to be *correct*. What
you get from them is a page whose first-paint replica and live-session replica
agree — worth having when the two disagree about state your store has not
converged on yet — and, in front of a proxy that rebalances aggressively, fewer
gratuitous reconnects.

**What a rolling deploy therefore costs.** Every replica that restarts closes
every session it holds with `4001`, and every one of those browsers reconnects
against whichever replica the balancer gives it. Because that path is identical
to the one a network blip takes, it is exercised constantly rather than only on
deploy days — but the cost is real: one full `Init` and one full `Snapshot` per
open tab, at once, per replica. Roll one replica at a time and let the jitter do
its work.

**And the capacity model.** Capacity here is a vertical question. If "add
another instance behind the load balancer" is your plan for a single page whose
concurrent session count is growing, that plan works only as far as your shared
store does, and the per-session memory below is what you multiply. The honest
form of all of this, including the cases where the answer is to use something
else, is [when-not-to-use-this.md](when-not-to-use-this.md).

---

## Resource sizing

**The number, with the method attached, because it is worth nothing without
it.**

| | |
|---|---|
| Measured | **45,769 B of steady-state memory per idle connection** |
| Conditions | N = 1000, Idle workload, observability **on**, TLS terminated outside the measured container, pooled over **five** runs with a 5.5 % spread |
| Method | [`docs/bench/equivalence-spec.md`](../bench/equivalence-spec.md) §3.6, unmodified; harness [`test/memory/`](../../test/memory), its own module; every run published in [`docs/bench/g2-baseline.md`](../bench/g2-baseline.md) §9.10.5 |
| Tree | commit `d66e4953`. **The tree has moved since and no re-measurement has been published at HEAD** |
| Against the gate | PRD goal **G2** is ≤ 46,080 B (45 KiB). This is **0.993×** it — under by 311 B, or 0.68 % |

**Read the last row with §9.10.9 attached, which the baseline document
insists on and so does this page.** A margin of 0.68 % is smaller than the
cell's own 5.5 % spread, and **two of the five runs were individually over the
gate** (46,145 B and 47,292 B). The honest statement is that the shipping tree
measures *at* the gate, not that it has cleared it.

**Two further caveats, neither of them small:**

- **The driver-validation gate has never been run.** Equivalence-spec §3.6
  requires 10 real browser tabs measured against 10 synthetic sessions before
  any 1,000-session figure is quoted. No campaign has run it. So every figure
  above is, in its own document's words, an assertion about a synthetic client
  rather than about browsers.
- **That figure is the library's memory, not your application's.** Every session
  folds its own copy of the shared data it renders — the bench chat keeps all
  three rooms' logs per session, the bench dashboard its 200 rows — because
  `live.Event.Fields` is `map[string]string` and a reducer cannot be handed a
  pointer into a shared store and stay pure. `bench/README.md` records this as
  deviation **G-3**, states that it is a real per-session cost, and says it
  **has not been measured**. If your live pages are large, shared, and nearly
  identical across many sessions, model that before you commit.

For orientation only, from the same campaign and the same method: with
`Logger`, `Metrics` and `Tracer` all nil the figure is **42,086 B** over **two**
runs — so default-on observability costs **≈3,682 B/session, 8.0 % of the
headline**. It is reported as two runs because it was two.

**What each session costs in goroutines is settled and small: exactly two**, at
both measured cells. Sizing a process is therefore memory arithmetic plus your
own effect concurrency, not a goroutine-count question.

---

## A deployment checklist

- [ ] `Config.Dev` is false, and the three dev routes answer `404` on the
      deployed process — verified, not assumed
- [ ] `Config.Origins` lists the exact origins the browser sends, with no
      trailing slash — [security.md](security.md)
- [ ] `Limits.MaxSessions` is set to something
- [ ] `Limits.HeartbeatInterval` is below the shortest idle timeout in the path
- [ ] `Logger`, `Metrics` and `Tracer` are wired
- [ ] `http.Server.WriteTimeout` is zero and `ReadHeaderTimeout` is not
- [ ] Shutdown fails readiness, then calls `Shutdown`, then calls `App.Close`
- [ ] The grace period exceeds `Limits.EffectDrainTimeout`, and the platform's
      termination grace exceeds the grace period
- [ ] Any state two replicas must agree on lives outside both of them
- [ ] `gotthlive_connections_closed_total{code="heartbeat_timeout"}` is on a
      dashboard, because it is how the proxy tells you it disagrees with you
