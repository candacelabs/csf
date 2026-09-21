# RFC 0000 — Prior-art teardown: server-driven UI over a persistent connection

| | |
|---|---|
| **Status** | Informational (input to RFC 0001 and ADR-001) |
| **Author** | DEV-1 (Server Core / Go) |
| **Date** | 2026-08-04 |
| **Supersedes** | — |
| **Feeds** | RFC 0001 (gotth-live architecture), ADR-001 (transport), liquid-proto mapping spec |

## 0. Purpose, scope, and how to read the numbers

gotth-live proposes: all state and rendering on the server (Go + templ + Tailwind), one
long-lived connection per session, events up, re-rendered HTML fragments down, a thin
client runtime that morphs them into the DOM. That is not a new idea. Four production
systems already occupy adjacent points in the design space, and three of them have
years of operational scar tissue we can read for free.

This document tears down each, then says precisely what gotth-live is doing differently
and why. It is deliberately unflattering to gotth-live where the prior art is better —
if we cannot articulate what BEAM does better than Go for this workload, we have not
finished thinking.

### Method and epistemic hygiene

Everything version-specific below was checked on **2026-08-04**. Three tiers of claim
appear, and they are marked:

- **Verified** — read from primary source: upstream source code, an official doc page,
  the GitHub/hex.pm release APIs, or a measurement I ran (bundle sizes). Cited inline.
- **Reported** — an official doc or vendor states a number I could not independently
  reproduce (e.g. Microsoft's per-circuit memory figure). Attributed, not endorsed.
- **Unverified** — flagged explicitly as such. I have not invented a single number to
  fill a gap; where a framework publishes nothing, this document says so.

Version state as of 2026-08-04, from the GitHub Releases API and the hex.pm API:

| Project | Current | Released | Notes |
|---|---|---|---|
| Phoenix LiveView | **1.2.8** | 2026-07-27 | 1.0.0 → 2024-12-03; 1.1.0 → 2025-07-30; 1.2.0 → 2026-06-10 |
| Datastar | **1.0.2** | 2026-06-02 | 1.0.0 → 2026-04-16 (i.e. 1.0 is ~4 months old) |
| Datastar Go SDK | **v1.2.x** | 2026 | moved to its own module `github.com/starfederation/datastar-go` |
| Hotwire Turbo | **8.0.23** | 2026-01-29 | no Turbo 9; the 8.0.x line has been maintenance-only for ~2 years |
| Blazor Server | **.NET 10** | 2025-11 | .NET 11 previews exist in docs monikers; 10 is current LTS-track |
| htmx | **2.0.9** stable | 2026-04-20 | 4.0.0-beta6 (2026-07-23) is the in-flight major |
| idiomorph | **0.7.4** | 2025-09-29 | vendored by Turbo; Datastar re-implements the algorithm |

Adjacent-tool state for our own stack: Go 1.26.5 / 1.25.12 are current upstream (repo
baseline per `CLAUDE.md` is Go 1.24 on the VMs); templ is at v0.3.1020 (2026-05-10).

---

## 1. Phoenix LiveView (Elixir / BEAM)

LiveView is the closest cousin and the most serious prior art. It has been production
software since 2019, hit 1.0 in December 2024, and shipped two feature majors since.

### 1.1 Architecture

The lifecycle is two-phase and this is load-bearing:

1. **Dead render.** An ordinary HTTP request runs `mount/3` with `connected?(socket) ==
   false`, renders the full HTML, and embeds a *signed* session token plus a static
   token in the root element (`data-phx-session`, `data-phx-static` — verified in
   [`constants.ts`](https://github.com/phoenixframework/phoenix_live_view/blob/v1.2.8/assets/js/phoenix_live_view/constants.ts)).
   The page is useful with JS off. First paint is SSR.
2. **Live render.** The client opens a WebSocket, joins a Phoenix Channel with that
   signed token, and the server spawns a **dedicated GenServer process per connected
   LiveView**. `mount/3` runs again, now `connected?` — so mount is *always* executed
   twice and must be idempotent. The process holds `socket.assigns` for the connection's
   lifetime. Nested LiveViews get their own processes; `LiveComponent`s do **not** — they
   run inside the parent process and are a state-partitioning device, not a concurrency
   device. ([process model](https://hexdocs.pm/phoenix_live_view/Phoenix.LiveView.html))

The unit of failure isolation is therefore the LiveView process, not the connection. A
crash in one nested LiveView does not take the parent's socket down.

### 1.2 Transport and its semantics

WebSocket via Phoenix Channels, multiplexed: one socket carries every LiveView, live
upload channel, and user channel for that tab. Upstream and downstream share the same
ordered, framed, bidirectional pipe — an event and the diff it caused are causally
adjacent on one connection.

An **opt-in long-poll fallback** exists (`longPollFallbackMs` on the `LiveSocket`
constructor) for corporate proxies that eat WebSocket upgrades. Note the shape: the
*fallback* is long-poll, not SSE, because the upstream path also has to work.

Client bundle reality, **measured 2026-08-04** (`gzip -9`, jsDelivr artifacts):

| Bundle | Raw | gzip |
|---|---:|---:|
| `phoenix_live_view@1.2.8/priv/static/phoenix_live_view.min.js` | 119,633 B | **36,980 B** |
| `phoenix@latest/priv/static/phoenix.min.js` (required separately) | 25,025 B | **7,787 B** |
| **LiveView client total** | ~144 KB | **~44.8 KB** |

I confirmed the LiveView bundle does *not* embed the Phoenix socket client (no
`phx_join`, no `LongPoll` strings in the minified artifact), so both are shipped. That
is ~3.7× our ≤12 KB budget, and it is the number to beat.

### 1.3 State model

All application state is server-resident in `socket.assigns`. The client holds only the
DOM and the signed session token. Two escape hatches keep the resident set small:

- **`temporary_assigns`** — reset to their initial value after every render, so
  append-only feeds do not accumulate.
- **`streams`** — collections that are *never* held server-side at all; the server emits
  insert/delete instructions against DOM ids inside a `phx-update="stream"` container,
  with an optional `:limit`. This is LiveView admitting that "all state on the server" is
  a memory bill and selling you an opt-out.

### 1.4 Diff / patch strategy — the genuinely clever part

HEEx templates are compiled, at build time, into a `Rendered` structure of **statics**
(the literal HTML chunks) and **dynamics** (the interpolated expressions), plus a
fingerprint. On first render both go over the wire. On every subsequent render, the
compiler-generated change tracking evaluates *only* the dynamics whose backing assigns
changed, and the server sends a sparse nested map of just those.

The wire keys are single characters. Verified from
[`constants.ts` @ v1.2.8](https://github.com/phoenixframework/phoenix_live_view/blob/v1.2.8/assets/js/phoenix_live_view/constants.ts):

```
s   statics          r   root / reply     c   components
k   keyed (comprehension)   kc  keyed count   km  keyed moved
e   events           t   title            p   templates      stream  stream ops
```

Note `k`/`kc`/`km`: the comprehension representation changed in 1.1 when
[keyed comprehensions](https://www.phoenixframework.org/blog/phoenix-liveview-1-1-released)
landed (`<li :for={i <- @items} :key={i.id}>`), so an insert at the head of a list no
longer marks every subsequent element dirty. Before that, positional indices meant
prepending to a list re-sent the whole list.

**This is a structural diff of the render tree, not a DOM diff.** The server never
compares HTML strings; it compares assigns and reassembles. The client reconstitutes
HTML from statics + received dynamics, then morphs.

The morph is **morphdom** — specifically a *fork*: `package.json` at v1.2.8 pins
`"morphdom": "github:SteffenDE/morphdom#sd-keyed-root"`
([verified](https://github.com/phoenixframework/phoenix_live_view/blob/v1.2.8/package.json)).
Not idiomorph. LiveView layers a large amount of its own bookkeeping on top
(`dom_patch.ts`, `element_ref.ts`, `dom_post_morph_restorer.ts`) for focus retention,
`phx-ref` locking during in-flight events, portals, and stream containers.

**Cost of the cleverness:** change tracking is *fragile by design*. Introducing a local
variable in a template silently disables tracking for that subtree; mutating assigns with
`Map.put/3` instead of `assign/3` silently bypasses it; touching the `assigns` map
directly instead of `@` does the same
([docs](https://phoenix-live-view.hexdocs.pm/assigns-eex.html)). These are *silent
performance regressions*, not errors. This is the single most important lesson in this
document for gotth-live: **a diff engine that can be defeated invisibly by ordinary code
is an observability problem, not just a performance problem.**

### 1.5 Reconnect and resync

There is no state replay. On reconnect the client rejoins with the signed session token;
the server spawns a *new* process and re-runs `mount/3` and `handle_params/3` from
scratch. Whatever was in assigns and not derivable from session + params is gone. The
server then pushes a full render, and the client morphs it over the existing DOM — which
is why unsent form input and scroll position usually survive even though server state did
not. The client exposes `phx-connected` / `phx-loading` / `phx-error` CSS classes and
hook `disconnected`/`reconnected` callbacks so the app can gray out during the gap.

The corollary is good news operationally: **LiveView does not require sticky sessions.**
A reconnect landing on a different node just remounts. Compare Blazor (§4.5).

The bad news is deploys. `phx-track-static` hashes static assets; when a new deploy
changes them the client force-reloads the page, and the client carries
`MAX_RELOADS = 10` with `RELOAD_JITTER_MIN/MAX = 5000/10000` ms
([constants.ts](https://github.com/phoenixframework/phoenix_live_view/blob/v1.2.8/assets/js/phoenix_live_view/constants.ts))
specifically to keep a bad deploy from becoming a reload loop. A rolling deploy is a
synchronised remount storm: every connected session re-runs `mount/3`, which usually
means every session re-runs its database queries, at once. The jitter constants exist
because this bit people.

### 1.6 Backpressure and slow clients

**There is no application-level backpressure API.** Diffs are pushed unconditionally.
BEAM process mailboxes are unbounded, so a slow consumer manifests as mailbox growth in
the LiveView process and the transport process, i.e. as memory, until the VM is under
pressure. The documented mitigations are all *input-side rate limiting* or *payload
reduction*, not flow control:

- `phx-debounce` / `phx-throttle` on bindings (limits the client's event rate)
- `streams` with `:limit` (bounds what the DOM and the wire carry)
- `temporary_assigns` (bounds the resident set)
- `hibernate_after`, default **15,000 ms** — the process compresses its own heap after
  idle ([docs](https://phoenix-live-view.hexdocs.pm/Phoenix.LiveView.html))

What LiveView does *not* have: sequence numbers on diffs, per-connection send-window
accounting, an ack from the client that a patch was applied, or a documented drop/coalesce
policy for a client that cannot keep up. Compare Blazor (§4.6), which has all of those.
This is a genuine gap and a design opportunity for gotth-live.

### 1.7 Memory per connection

**LiveView publishes no per-connection memory figure.** I looked; the docs give you
`hibernate_after` as the knob and no number as the baseline. The number widely quoted in
blog posts — "2 million connections per node" — comes from
[The Road to 2 Million WebSocket Connections in Phoenix](https://www.phoenixframework.org/blog/the-road-to-2-million-websocket-connections)
(2015), which benchmarked **Phoenix Channels with no LiveView and no per-connection
application state**. It is not a LiveView number and should never be cited as one. A
LiveView process holds the assigns, the last `Rendered` tree (for fingerprint/diff
comparison), and its mailbox; the honest statement is that the footprint is
**proportional to your assigns plus the rendered tree, and unbounded by the framework**.

Secondary figures circulating (40 KB/channel, 3 MB active / 150 KB hibernated) trace to
low-quality secondary sources and I could not corroborate them against anything primary.
**Treat as unverified.** If gotth-live wants a credible comparison we will have to
benchmark LiveView ourselves.

### 1.8 Observability and provenance

`:telemetry` events, span-shaped (`:start` / `:stop` / `:exception`), for `mount`,
`handle_params`, `handle_event`, `render`, plus `live_component` update/handle_event/
destroyed ([telemetry docs](https://phoenix-live-view.hexdocs.pm/telemetry.html)).
Measurements are `system_time` / `duration`; metadata carries the `socket`.

What you do **not** get:

- No per-connection resource metrics (bytes sent, diff size, patch count) as
  first-class telemetry. `render` gives you duration, not payload size.
- **No causal identifier linking an inbound event to the diff it produced.** The
  `handle_event` span and the subsequent `render` span are separate telemetry events with
  no shared correlation id in the emitted metadata. If a user reports "the wrong number
  appeared in the cart badge," there is no id in the frame that lets you retrieve the
  event, the state transition, and the emitted patch as one unit.
- **No OpenTelemetry emission from LiveView itself.** You bridge `:telemetry` to OTel by
  hand. `opentelemetry_phoenix` instruments HTTP and Channels, not the LiveView diff
  pipeline.
- LiveDashboard gives you excellent *runtime* introspection (per-process memory, mailbox
  length, reductions) — genuinely better than anything Go ships out of the box — but it is
  VM observability, not application-semantic observability.

LiveView 1.1/1.2 added HEEx **debug annotations** (`debug_heex_annotations`, module-level
config in 1.2) which emit HTML comments identifying the component that rendered a subtree.
That is real provenance — but it is a *development* feature, string-embedded in HTML, and
it tells you which template rendered a node, not which event caused it to change.

### 1.9 Known failure modes and operational pain

- **Change tracking silently disabled** by benign-looking template refactors (§1.4).
- **Deploy remount storms** (§1.5) — every session re-mounts and re-queries simultaneously.
- **WebSocket upgrade blocked** by corporate proxies / break-and-inspect middleboxes →
  long-poll fallback, with the latency and connection-churn that implies.
- **Idle timeouts** on intermediary LBs killing connections; the Channel heartbeat is what
  keeps them alive, and it must be tuned below the shortest idle timeout in the path.
- **Mobile networks**: every app backgrounding is a disconnect and a remount. On a flaky
  cellular link, mount cost is paid repeatedly, which is exactly when the user's device
  and your database can least afford it.
- **`check_origin` / CSRF** misconfiguration behind a reverse proxy is a common
  "everything 403s in production only" failure — relevant to us, since this monorepo
  fronts everything with Caddy.
- **Latency is the UX.** Every keystroke-driven interaction is a round trip. LiveView's
  answer is `JS` commands (client-side DOM effects without a round trip) and hooks; a
  server-driven framework without a local-effect escape hatch is unusable at >100 ms RTT.

---

## 2. Datastar

The most Go-relevant prior art, and the youngest: **1.0.0 shipped 2026-04-16**, current
1.0.2 on 2026-06-02 (GitHub Releases API). Anything written about Datastar before mid-2026
describes a release-candidate API that changed under people.

### 2.1 Architecture

Datastar is htmx + Alpine collapsed into one ~13 KB file. Reactivity is declared in
`data-*` attributes; **signals** are client-side reactive values; **actions** (`@get`,
`@post`, …) issue `fetch` requests; responses may be `text/html` (ordinary) or
`text/event-stream` (a stream of patch events). The server writes SSE events; the client
applies them.

There is **no session object and no server-side connection actor**. The server side is a
plain `http.Handler`. That is the entire architecture, and its plainness is its best
property.

### 2.2 Transport: SSE down, `fetch` up — and what that costs

This is the split-transport model ADR-001 has to evaluate, so it deserves precision.

- **Downstream**: SSE. Any handler can be a stream; `datastar.NewSSE(w, r)` upgrades the
  response writer and the stream lives until the handler returns or the context is
  cancelled. Each event is **flushed immediately** — no inter-event buffering. The Go SDK
  exposes `IsClosed()` and a `Send()` that is safe for concurrent use, with optional
  gzip/deflate/brotli/zstd compression
  ([`datastar-go` package docs](https://pkg.go.dev/github.com/starfederation/datastar-go/datastar)).
- **Upstream**: separate `fetch` requests. Signals ride along — **GET puts the JSON
  signal snapshot in a `datastar` query parameter; every other method puts it in the JSON
  body** (`ReadSignals(r, &sig)` handles both). So the upstream path is stateless HTTP and
  the client re-uploads its state view on every interaction.

The consequences are structural, not incidental:

1. **No ordering guarantee between the two paths.** An event posted at T and a patch
   arriving on the long-lived stream at T+ε have no defined relative order. There is no
   sequence number tying a response patch to the request that caused it.
2. **HTTP/1.1's six-connection-per-origin cap.** A long-lived SSE stream permanently
   occupies one of six. Two streams plus a slow upload and the tab starts blocking. HTTP/2
   multiplexing (~100 streams) fixes it, but you cannot assume HTTP/2 end-to-end —
   break-and-inspect proxies routinely downgrade. This is the single most-cited Datastar
   production concern
   ([community production-considerations doc](https://alvarolm.github.io/datastar-resources/docs/considerations.html)).
3. **Proxy buffering kills SSE silently.** nginx needs `proxy_buffering off` or
   `X-Accel-Buffering: no`; the failure mode is "it works locally, and in production the
   UI updates in bursts every 4 KB." Notably, Microsoft's Blazor-behind-nginx guidance
   tells you to *remove* the `proxy_buffering off` line because Blazor doesn't use SSE —
   an amusing illustration that the two transports have inverted proxy requirements.
4. **SSE cannot carry binary.** `text/event-stream` is UTF-8 line-oriented. A liquid-proto
   binary frame over SSE requires base64 (+33% and an encode/decode hop) or a
   protobuf-JSON mapping. **This is the sharpest input to ADR-001 in this entire
   document.**

### 2.3 State model

Datastar markets itself as backend-driven state, and the marketing is misleading. Signals
live **in the browser**. The backend *drives* them (`datastar-patch-signals`) and *reads*
them (`ReadSignals`), but between requests the authoritative copy of `$count` is in the
DOM. Any server-resident state is something you built yourself in your own Go structures
with your own lifecycle.

That is the opposite of gotth-live's thesis, and it is worth stating plainly: **Datastar
is not a competitor to the state-actor model; it is a competitor to the wire format and
the client runtime.**

### 2.4 Diff / patch strategy

No diff. The server sends whole HTML fragments and a mode; the client applies them.
Verified from
[`patchElements.ts`](https://github.com/starfederation/datastar/blob/main/library/src/plugins/watchers/patchElements.ts):

- Events: `datastar-patch-elements` (fields `elements`, `selector`, `mode`, `namespace`,
  `useViewTransition`, `viewTransitionSelector`) and `datastar-patch-signals` (fields
  `signals`, `onlyIfMissing`).
- Modes: `outer` (default), `inner`, `replace`, `prepend`, `append`, `before`, `after`,
  `remove`. With no `selector`, `outer`/`replace` target by matching the top-level
  element's `id`.
- The morph is an **Idiomorph-derived algorithm inlined into the source**, not an npm
  dependency — the `pantry` node, persistent-id set, and id/tagName maps in
  `patchElements.ts` are Idiomorph's algorithm re-implemented. There is a
  `data-ignore-morph` opt-out and `<script>` re-execution bookkeeping via a `WeakSet`.

Because there is no server-side diff, wire volume is proportional to *fragment size*, not
*change size*. For a 200-row table where one cell changed, LiveView sends the cell and
Datastar sends the row (or the table, depending on how you sliced your handlers). The
tradeoff is bought back in simplicity: there is no change-tracking model to silently
defeat.

### 2.5 Reconnect and resync

EventSource semantics: the browser reconnects automatically, and the SSE `retry:` field
sets the interval (`DefaultSseRetryDuration` = 1000 ms in the Go SDK). `Last-Event-ID`
exists in the SSE spec but Datastar does not build a resumable event log on top of it, so
**patches emitted while disconnected are lost with no gap detection**. The reconnecting
client simply re-runs whatever handler its `data-on-load` action points at.

The community production-considerations document reports a sharper version of this:
signals revert to their *initial* values on SSE reconnect, losing interactions made during
the gap, with the workaround being manual handling of `data-on:datastar-fetch` events. I
could not confirm this against the 1.0.2 source in the time available — **treat as
reported, not verified** — but it is the expected consequence of client-resident state
plus a stateless reconnect.

### 2.6 Backpressure and slow clients

Datastar has no backpressure mechanism, but — uniquely among the four — **it does not need
one at the framework layer, because you own the goroutine.** The handler is yours: you can
select on `ctx.Done()`, coalesce, drop, buffer with a bounded channel, or check
`IsClosed()` before doing expensive work. A blocked `Flush()` blocks *your* goroutine and
nothing else.

This is the single most important thing to steal. Go's model puts the flow-control
decision exactly where the application knowledge is.

### 2.7 Memory per connection

**No published figure, and the question is slightly malformed** — the framework holds
nothing. The cost is: one goroutine blocked in the handler (Go's initial goroutine stack
is 8 KB, growing as needed), the `http.ResponseWriter` and its transport buffers, and
whatever you allocate. For a typical Go HTTP server, a few tens of KB per idle SSE stream
is a defensible order-of-magnitude estimate, but **I am not going to state it as a
measured number, because I did not measure it.** gotth-live should.

Measured client bundle, **2026-08-04**: `datastar@v1.0.2/bundles/datastar.js` = 34,083 B
raw, **13,277 B gzip -9**. The project homepage advertises 11.76 KiB, which is presumably
brotli; my gzip figure and their figure are consistent with that reading. There is also a
smaller `datastar-core.js` (10,254 B raw).

### 2.8 Observability and provenance

Nothing built in — no metrics, no traces, no connection registry, no event log.

And yet this is arguably the *best* observability story of the four, because it is a
`net/http` handler: `otelhttp`, standard middleware, `promhttp`, and this monorepo's
zerolog conventions all just work, with request-scoped context propagation you already
understand. Nothing has to be reverse-engineered out of a framework's internal event bus.

What is still missing, and cannot be added from outside: there is no id on a patch that
ties it to the event that produced it, because the two travel on different HTTP requests
with no shared identifier. **Provenance is not merely absent in Datastar; the split
transport makes it structurally awkward.**

### 2.9 Known failure modes and operational pain

- HTTP/1.1 six-connection cap; HTTP/2 assumption not safe end-to-end (§2.2).
- Proxy buffering breaking streaming, silently, in production only.
- API churn: 1.0 is four months old and the RC line broke syntax repeatedly (the
  `data-on-click` → `data-on:click` delimiter change is the notorious one). The Go SDK
  moved modules (`starfederation/datastar/sdk/go` v0.21.4 is abandoned at 2024-12;
  current is `starfederation/datastar-go`). Anyone's Datastar knowledge from 2025 is stale.
- CSP: the community doc reports Datastar requires `unsafe-eval` and does no automatic
  escaping, putting XSS entirely on the developer. **Reported, not verified** — I did not
  find `new Function` in the 1.0.2 bundle by string search, but the bundle is minified and
  that is not dispositive. Flagged for our own security review since we would face the
  same question with any expression-evaluating client.
- Plugin API is used ecosystem-wide but undocumented and unsupported (same source).

---

## 3. Hotwire / Turbo Streams (Rails)

Current: **Turbo 8.0.23, 2026-01-29**. Turbo 8 landed in Feb 2024 with morphing; the 8.0.x
line has been maintenance since. No Turbo 9 exists.

### 3.1 Architecture

Three separable pieces:

- **Turbo Drive** — intercepts navigation and form submission, swaps `<body>`, keeps the
  page instance alive. A PJAX descendant.
- **Turbo Frames** — `<turbo-frame id>` scopes a navigation to a subtree; the server
  returns a full page and Turbo extracts the matching frame.
- **Turbo Streams** — the reactive part: a list of imperative DOM operations.

There is no session actor, no server-resident view state, and no persistent connection in
the base case. The persistent connection appears only when you opt into broadcasts.

### 3.2 Transport

Three independent delivery paths, which is unusual and instructive
([handbook](https://turbo.hotwired.dev/handbook/streams)):

1. **HTTP response** to a form submission, content type
   `text/vnd.turbo-stream.html`. This is the default and covers most interactions. Fully
   stateless, no connection.
2. **WebSocket** via Action Cable — `turbo_stream_from @thing` in the template subscribes
   the tab to a channel; server-side `broadcast_replace_to` pushes rendered HTML.
3. **SSE** via `<turbo-stream-source src="…">` (or Mercure).

Upstream is always ordinary HTTP. The WebSocket/SSE path is **downstream-only broadcast**;
it is not a session channel. This is the cleanest separation of the four: the
request/response path and the push path are acknowledged as different things with
different semantics rather than pretending to be one connection.

Measured client bundle, **2026-08-04**: `@hotwired/turbo@8.0.23/dist/turbo.es2017-umd.js`
= 217,020 B raw, **45,764 B gzip -9**. Note that Turbo ships **only unminified** bundles
(`turbo.es2017-esm.js` and `turbo.es2017-umd.js`, per the jsDelivr package listing), so
this gzip figure is for the unminified artifact; a minifying build would improve it, but
the importmap-based Rails default serves it as-is.

### 3.3 State model

None on the server. Every interaction re-derives from the database. Turbo's entire
position is that a persistent per-user server object is a liability, and for a large class
of apps it is right.

The cost is that "the current state of this user's view" is not a thing that exists
anywhere, which means it cannot be inspected, traced, or reasoned about as a unit.

### 3.4 Diff / patch strategy

**No diff whatsoever, by design.** Eight imperative actions —
`append`, `prepend`, `replace`, `update`, `remove`, `before`, `after`, and (Turbo 8)
`refresh` — each with a `target` (DOM id) or `targets` (CSS selector). The server decides
*what to do*, not *what changed*.

Turbo 8 added morphing on top, as a **method** rather than an action:
`<meta name="turbo-refresh-method" content="morph">` makes a page refresh morph the body
rather than replace it, and `method="morph"` (with `scroll="preserve|reset"`) is available
on `replace`/`update`. The implementation is **Idiomorph, vendored into the Turbo bundle**
(verified: `src/core/morphing.js` in the repo, 17 `idiomorph` occurrences and Idiomorph's
`pantry` in the shipped dist; the package declares no runtime dependencies).

The `refresh` broadcast action is the interesting one architecturally: instead of
broadcasting *content*, the server broadcasts "something changed, go re-fetch," and the
client issues a normal request and morphs the result. That is a deliberate trade of
bandwidth and server CPU for correctness and per-user personalization — because the
content-broadcast path renders **once** and sends the identical HTML to every subscriber,
which means it fundamentally cannot personalize.

### 3.5 Reconnect and resync

Action Cable reconnects. **Broadcasts emitted during the gap are gone.** There are no
sequence numbers, no `Last-Event-ID` replay, and no gap detection — the client cannot even
know it missed something. The recovery story is "the user refreshes," or you adopt the
`refresh` action so that reconnection triggers a re-fetch of ground truth.

For gotth-live this is the clearest possible statement of what a sequence-numbered frame
protocol buys: **the ability to detect that you are stale.** Turbo cannot.

### 3.6 Backpressure and slow clients

None. Action Cable fan-out is fire-and-forget through the pub/sub adapter (Redis in any
real deployment). A slow subscriber's frames queue in the server's WebSocket write buffer.
There is no coalescing, so a hot record broadcasting 50 updates/sec sends 50 frames to
every subscriber. The `_later` variants (`broadcast_replace_later_to`) move *rendering*
into a background job, which bounds request latency, not wire volume.

### 3.7 Memory per connection

No server-side view state, so the per-connection cost is an Action Cable connection object
and its subscription set. **Rails publishes no per-connection figure** and I found none I
would stand behind. The practical scaling limit people hit is Action Cable connection
count per process and Redis pub/sub fan-out, not per-user memory.

### 3.8 Observability and provenance

Client-side: a rich DOM event vocabulary (`turbo:before-stream-render`,
`turbo:before-morph-element`, `turbo:submit-end`, …) which is genuinely useful for
debugging morph surprises. Server-side: ordinary Rails `ActiveSupport::Notifications` for
the request that produced the stream.

Provenance: **the weakest of the four.** A broadcast frame contains an action, a target,
and HTML. It does not identify the model change that caused it, the request that triggered
that change, or the user who initiated it — the same bytes go to everyone. Correlating "a
row changed in the UI" with "this HTTP request mutated this record" is manual archaeology
across two unrelated log streams.

### 3.9 Known failure modes and operational pain

- **Morph surprises.** Idiomorph preserves what it can, but focus, scroll, `<details>`
  open state, third-party-widget DOM, and anything a JS library mutated get clobbered
  unless you annotate `data-turbo-permanent` or stable `id`s. This was the dominant
  complaint after Turbo 8; the fix is discipline, and discipline does not scale across a
  team.
- **Broadcast fan-out cost.** Rendering per-broadcast is cheap; the Redis fan-out and the
  N WebSocket writes are not.
- **No personalization on the broadcast path** (§3.4) — a real functional limit that
  pushes people onto the `refresh` action and back to N requests.
- **Turbo Drive's cache preview flicker** — the restored cached page renders before the
  fresh one arrives, and JS state initialized on the cached copy has to be torn down.
- **Idle timeouts and mobile backgrounding** drop the Action Cable connection; with no
  resync, the tab is silently stale until the user acts.

---

## 4. Blazor Server (.NET, SignalR circuits)

The most operationally mature of the four in terms of published knobs and defaults, and —
after .NET 10 — the best instrumented. It is also the one whose failure modes are the most
severe, because its coupling is the tightest.

### 4.1 Architecture

A **circuit** is a server-side object holding the live component instances and their
**render tree** (a C# object graph mirroring the DOM). The browser holds no application
logic; `blazor.server.js` receives binary render batches and applies DOM edits, and ships
DOM events back. Every `@onclick` is a network round trip.

### 4.2 Transport

SignalR hub. **WebSockets preferred; Long Polling is the only fallback** — Microsoft's
docs state SSE is "not relevant to Blazor app client-server interactions," which is
correct given the bidirectional requirement, and the client logs a console warning when it
falls back
([host & deploy](https://learn.microsoft.com/en-us/aspnet/core/blazor/host-and-deploy/server/?view=aspnetcore-10.0)).
WebSocket compression is on by default since .NET 9.

Documented defaults ([SignalR guidance](https://learn.microsoft.com/en-us/aspnet/core/blazor/fundamentals/signalr?view=aspnetcore-10.0)):

| Setting | Default |
|---|---|
| `MaximumReceiveMessageSize` (inbound) | 32 KB |
| `KeepAliveInterval` | 15 s |
| `ClientTimeoutInterval` / client `withServerTimeout` | 30 s |
| `HandshakeTimeout` | 15 s |
| `JSInteropDefaultCallTimeout` | 1 min |
| client reconnect `maxRetries` | 8 |

Microsoft's own UX guidance: **"For a reasonable UI experience, we recommend a sustained
UI latency of 250 ms or less."** That is the honest cost of putting the event loop on the
other side of the internet, stated by the vendor.

### 4.3 State model

Everything — component instances, fields, the render tree — lives in the circuit on the
server. Strictly more state-resident than LiveView (which keeps assigns, not a component
object graph).

### 4.4 Diff / patch strategy

`Renderer` produces a `RenderTreeDiff` (a sequence of `RenderTreeEdit`s) per component,
batched into a **`RenderBatch`**, serialized to a **compact binary format**, and pushed
over SignalR. The client applies edits positionally against its mirror of the render tree —
**no morphing, no HTML string diffing, no DOM matching heuristics.** Because both sides
hold the same tree structure, edits are exact.

This is meaningfully different from every other system here and worth respecting: Blazor's
patches are the smallest and the least ambiguous. The price is that the client is not
"thin" in any sense, the wire format is opaque to humans and to proxies, and the two sides
must remain in lockstep — which is why §4.5 is hard.

### 4.5 Reconnect and resync

Verified from
[`CircuitOptions.cs`](https://github.com/dotnet/aspnetcore/blob/main/src/Components/Server/src/CircuitOptions.cs):

| Option | Default |
|---|---|
| `DisconnectedCircuitRetentionPeriod` | 3 minutes |
| `DisconnectedCircuitMaxRetained` | 100 |
| `PersistedCircuitInMemoryRetentionPeriod` (.NET 10) | 2 hours |
| `PersistedCircuitInMemoryMaxRetained` (.NET 10) | 1000 |
| `PersistedCircuitDistributedRetentionPeriod` (.NET 10) | 8 hours |

Within the retention window a reconnect **resumes the same circuit** and replays
unacknowledged render batches — genuine session continuity, which neither LiveView nor
Turbo nor Datastar offers. Outside it, the circuit is gone and the client's only recourse
is a full page reload; the reconnection UI exposes `show`/`hide`/`failed`/`rejected`/
`paused`/`retrying` states so the app can drive that.

.NET 10 added **circuit state persistence**: properties marked `[PersistentState]` are
serialized on `OnPersisting` just before a disconnected circuit is evicted, encrypted with
ASP.NET Data Protection, and rehydrated into a *new* circuit on reconnect — optionally via
HybridCache so it survives a process restart. This is a direct, explicit acknowledgement
by Microsoft that "state lives in a server process" is a durability problem. **We should
read that as a warning about our own design, not as a feature to envy.**

Because the circuit is pinned to a process, **sticky sessions are mandatory**: ARR affinity
on Azure App Service / IIS, `nginx.ingress.kubernetes.io/affinity: cookie` on Kubernetes,
Container Apps sticky sessions, plus a **shared Data Protection key ring** (Azure Blob +
Key Vault in Microsoft's reference config) so any instance can deserialize component state.
That is a substantial amount of infrastructure policy imported into your deployment by a
UI framework choice — precisely the kind of coupling this monorepo's container-first rule
exists to resist.

### 4.6 Backpressure and slow clients — the one to steal

Blazor is the only system of the four with **real, bounded, acknowledged downstream flow
control**, and it is worth reading the code. From
[`RemoteRenderer.cs`](https://github.com/dotnet/aspnetcore/blob/main/src/Components/Server/src/Circuits/RemoteRenderer.cs):

- a `ConcurrentQueue<UnacknowledgedRenderBatch> _unacknowledgedRenderBatches`
- batches carry a monotonically increasing **`BatchId`** (a sequence number)
- the renderer stalls when `_unacknowledgedRenderBatches.Count >=
  _options.MaxBufferedUnacknowledgedRenderBatches` — **default 10**
- the client calls back `OnRenderCompletedAsync(incomingBatchId, errorMessageOrNull)`,
  which dequeues everything up to and including that id
- on reconnect, all still-unacknowledged batches are rewritten to the new connection

So: sequence-numbered frames, a bounded in-flight window, explicit client acknowledgement,
and replay on reconnect. **That is the flow-control design gotth-live should adopt**, and
liquid proto's refinement types are a natural fit for expressing its invariants (Appendix A).

Inbound is bounded separately by `MaximumReceiveMessageSize` (32 KB) — with an explicit
docs warning that raising it increases DoS exposure, and that large prerendered state
blowing this limit is a known circuit-initialization failure.

### 4.7 Memory per connection

The only framework here that publishes a number, and it publishes it clearly
([host & deploy](https://learn.microsoft.com/en-us/aspnet/core/blazor/host-and-deploy/server/?view=aspnetcore-10.0)):

> Each circuit uses approximately **250 KB** of memory for a minimal *Hello World*-style
> app. […] If you expect your app to support 5,000 concurrent users, consider budgeting at
> least **1.3 GB** of server memory to the app (or ~273 KB per user).

**Reported, not independently verified**, but it is a vendor number with a stated
methodology caveat ("measure resource demands during development for your app"), which is
more than the other three offer. It is also a *floor*: the render tree grows with component
count, so a real dashboard is multiples of this.

I could not measure `blazor.server.js`'s size — it is not published to a public CDN or npm
in a form I could fetch. **Unverified.**

### 4.8 Observability and provenance — the current best of the four

.NET 10 shipped first-class OpenTelemetry instrumentation, and the names are specific
([performance docs](https://learn.microsoft.com/en-us/aspnet/core/blazor/performance/?view=aspnetcore-10.0)):

**Meters**
- `Microsoft.AspNetCore.Components`: `aspnetcore.components.navigate`,
  `aspnetcore.components.handle_event.duration`
- `Microsoft.AspNetCore.Components.Lifecycle`:
  `aspnetcore.components.update_parameters.duration`,
  **`aspnetcore.components.render_diff.duration`**, **`aspnetcore.components.render_diff.size`**
- `Microsoft.AspNetCore.Components.Server.Circuits`: `aspnetcore.components.circuit.active`,
  `aspnetcore.components.circuit.connected`, `aspnetcore.components.circuit.duration`

**Activities** (`ActivitySource`s `Microsoft.AspNetCore.Components` and
`…Server.Circuits`)
- `…StartCircuit` — `Circuit {circuitId}`, tagged `aspnetcore.components.circuit.id`,
  **linked to the HTTP and SignalR traces**
- `…Navigate` — `Route {route} -> {componentType}`
- `…HandleEvent` — `Event {attributeName} -> {componentType}.{methodName}`, tagged with the
  handler's `code.function.name`, **linked to the circuit and route activities**

This is a real provenance story and we must not pretend otherwise. Microsoft's own stated
use case — *"click to which component caused exception and on which page? in which linked
circuit and with what HTTP context?"* — is close to what gotth-live wants.

Where it stops: `render_diff.size` and `render_diff.duration` are **histograms**, not
per-patch records. There is no id carried *in the frame* that lets you take a specific
DOM patch observed in a specific browser and retrieve the event and state transition that
produced it. The `BatchId` exists on the wire (§4.6) but is a flow-control sequence number,
not a trace correlator — it is not surfaced as a span attribute or a metric dimension. The
gap between Blazor and gotth-live's ambition is therefore narrow but real: **Blazor traces
the server's work; gotth-live proposes to make the frame itself the trace carrier.**

### 4.9 Known failure modes and operational pain

- **Sticky sessions mandatory** + shared Data Protection key ring (§4.5). Non-negotiable
  infrastructure coupling.
- **Latency is the product** — ≤250 ms sustained, per Microsoft. Intercontinental users
  need a regional deployment or Azure SignalR Service.
- **Long-poll fallback** behind VPNs/proxies, with a console warning as the only signal.
- **Deploys evict every circuit.** With .NET 10 persistence, in-memory persisted state is
  also lost on restart unless you wired up a distributed cache.
- **`MaximumReceiveMessageSize` (32 KB)** silently caps JS interop payloads and prerendered
  state; the remedy is chunking or streaming interop, not raising the limit.
- **Circuit retention as a DoS surface** — `DisconnectedCircuitMaxRetained = 100` and
  `PersistedCircuitInMemoryMaxRetained = 1000` are bounded for exactly this reason, which
  means a busy app evicts legitimate users' state under churn.
- A live .NET 10 bug ([aspnetcore#64607](https://github.com/dotnet/aspnetcore/issues/64607))
  reports the app entering a bugged state on circuit resume after
  `PersistedCircuitInMemoryRetentionPeriod` elapses — resume-after-eviction is a genuinely
  hard state machine and it is not fully settled even for Microsoft.

---

## 5. Cross-cutting comparison

### 5.1 The design space, on one page

| | LiveView 1.2 | Datastar 1.0 | Turbo 8 | Blazor Server (.NET 10) |
|---|---|---|---|---|
| **Server session object** | GenServer per LiveView | none | none | Circuit per tab |
| **Downstream transport** | WebSocket (Channels) | SSE | HTTP resp / WS / SSE | WebSocket (SignalR) |
| **Upstream transport** | same WebSocket | separate `fetch` | ordinary HTTP | same WebSocket |
| **Fallback** | long-poll (opt-in) | n/a | n/a | long-poll (auto) |
| **Wire encoding** | JSON, 1-char keys | SSE text + HTML | HTML | **binary** render batch |
| **Diff unit** | template dynamics | none (whole fragment) | none (imperative op) | render-tree edits |
| **Client apply** | forked morphdom | inlined Idiomorph | vendored Idiomorph | positional tree edits |
| **Seq numbers on frames** | ❌ | ❌ | ❌ | ✅ `BatchId` |
| **Client ack / send window** | ❌ | n/a | ❌ | ✅ window = 10 |
| **Reconnect = state survives** | ❌ remount | ❌ | ❌ | ✅ within 3 min (+ .NET 10 persistence) |
| **Missed-frame detection** | n/a (remount) | ❌ | ❌ | ✅ |
| **Sticky sessions required** | ❌ | ❌ | ❌ | ✅ |
| **Published mem/conn** | ❌ none | ❌ n/a | ❌ none | ✅ ~250 KB (vendor) |
| **First-class OTel** | ❌ (`:telemetry` only) | ❌ (but stdlib works) | ❌ | ✅ .NET 10 meters + activities |
| **Event→patch causal id** | ❌ | ❌ | ❌ | ❌ (traces linked, frames not tagged) |

### 5.2 Client runtime size — measured 2026-08-04

`gzip -9` over the artifact each project ships. Reproduce with:

```
curl -sSL -o f "<jsdelivr url>" && stat -c%s f && gzip -9 -c f | wc -c
```

| Runtime | Raw | gzip | vs. our ≤12 KB budget |
|---|---:|---:|---|
| **gotth-live target** | — | **≤12,288 B** | — |
| Datastar 1.0.2 (`bundles/datastar.js`) | 34,083 | **13,277** | 1.08× |
| htmx 2.x (`htmx.min.js`) | 51,238 | **16,539** | 1.35× |
| LiveView 1.2.8 (`phoenix_live_view.min.js`) | 119,633 | 36,980 | — |
| + `phoenix.min.js` (required) | 25,025 | 7,787 | — |
| **LiveView total** | ~144,658 | **44,767** | 3.64× |
| Turbo 8.0.23 (`turbo.es2017-umd.js`, unminified) | 217,020 | **45,764** | 3.72× |
| `blazor.server.js` | — | — | **unverified — not publicly fetchable** |

**Finding for the orchestrator:** ≤12 KB gzip is *below* the smallest shipped competitor,
and that competitor does no server-side diffing and no binary framing. Our budget must
absorb a morph algorithm (Idiomorph-class, ~5–7 KB gzip on its own), a connection/reconnect
state machine, an event binder, *and* a liquid-proto frame decoder. A general protobuf-JS
runtime is out of the question; a hand-written decoder for our fixed frame schema
(~1–3 KB) is the only viable path, and even then 12 KB is tight. This is an early-risk item
for RFC 0001, not a detail.

---

## 6. Synthesis: what gotth-live does differently, and why

### 6.1 The four things nobody has together

Reading across §5.1, every column has a hole:

1. **Nobody has flow control *and* a morphing client.** Blazor has the flow control and a
   lockstep tree client; the three HTML-morphing systems all push unconditionally.
2. **Nobody has provenance in the frame.** Blazor comes closest and stops at linked
   server-side traces.
3. **Nobody treats per-connection observability as a product surface.** LiveView gives you
   BEAM introspection; Blazor gives you aggregate histograms; the other two give you
   nothing.
4. **Nobody has a typed, machine-checkable wire contract.** LiveView's format is
   single-character JSON keys documented only by reading `constants.ts`; Turbo's is HTML
   with attributes; Datastar's is SSE field names; Blazor's is an internal binary format
   whose own API docs say *"types in `Microsoft.AspNetCore.Components.RenderTree` are not
   recommended for use outside of the Blazor framework and will change."*

gotth-live's thesis is that these four holes are the same hole, and that a typed frame with
causal identity closes all of them at once.

### 6.2 Why Go's concurrency model is the structural advantage

The state-actor model — one goroutine + mailbox per session, pure reducers
`(state, event) → (state, effects)`, pure render of state, effects only at the actor
boundary — is not an arbitrary aesthetic. Each property buys something the prior art
lacks.

**Cheap, first-class concurrency without a VM.** A goroutine costs ~8 KB of initial stack
and a scheduler slot. Unlike Blazor's circuit — a heap object graph serviced by a thread
pool, with all the "which thread am I on" hazards that implies — a gotth-live session is a
`for { select { … } }` loop that reads top to bottom. Unlike Datastar's per-request handler,
it survives between requests and can hold real state.

**`select` is a flow-control primitive, and it is the missing piece.** This is the crux.
Blazor had to *build* an unacknowledged-batch queue with a bounded window inside its
renderer. In Go, that design is idiomatic and local: a bounded outbound channel, a
`select` with `ctx.Done()` and a `default:` branch that coalesces instead of blocking, and
the actor is the natural place to decide whether a slow client should be throttled,
coalesced, or dropped — because the actor is the only thing that knows what the pending
patches *mean*. LiveView cannot express this, because a BEAM mailbox is unbounded by
construction and `receive` has no "the queue is full" branch. Go's bounded channels make
backpressure the default expressible thing rather than something you bolt on.

**Contexts give cancellation a shape.** `context.Context` propagating from the connection
through the actor into every effect means connection teardown, request deadlines, and
shutdown drain are one mechanism, and it is the same mechanism `otelhttp`, `database/sql`,
and every library in this monorepo already speaks. LiveView's equivalent is process linking
and monitors — more powerful, but a separate vocabulary from everything else.

**Pure reducers make the causal chain a value, not an inference.** If a state transition is
a function call with an event in and effects out, then the tuple (event id, prior state
version, new state version, emitted patches) exists as data at a single point in the
program. That is the entire provenance feature, and it falls out of the architecture rather
than being retrofitted. LiveView's `handle_event/3` is a reducer too — but it returns a
socket, effects are performed inline, and the render is a separate later step, so nothing
ever holds the whole tuple.

**Structured concurrency at the boundary.** Effects run in goroutines spawned by the actor
and report back into the mailbox. This means the actor loop never blocks on I/O, and every
in-flight effect is attributable to the event that spawned it — which is exactly the
provenance edge Blazor's `HandleEvent` activity gives you and LiveView does not.

### 6.3 Honest accounting: what BEAM does better

BEAM is the closest cousin and it beats Go on several axes that matter for this workload.
Pretending otherwise would make the RFC worse.

- **Preemptive scheduling.** BEAM preempts on reduction count. A LiveView that computes a
  pathological render cannot starve its neighbours. Go's scheduler is preemptive since 1.14
  (async signal-based) but at coarser granularity, and a goroutine in a tight non-preemptible
  region — or holding a lock the runtime cares about — still degrades the whole process.
  **Mitigation for us:** treat reducers as a budgeted operation, instrument reducer duration
  as a first-class metric (which we are doing anyway), and set an explicit "reducer must not
  block" invariant enforced in review and in tests.
- **True process isolation and per-process heaps.** A BEAM process crash is contained: its
  heap is freed, its links fire, and nothing else is corrupted. A Go panic in a goroutine
  takes the **entire process** down unless recovered, and shared heap means a memory leak in
  one session is indistinguishable from a leak anywhere else. **Mitigation:** a
  `recover()` at the actor loop boundary, converted into a session-fault frame + a metric,
  is mandatory, not optional — it is our only approximation of a supervision tree. We should
  say so in the RFC as a hard requirement.
- **Per-process GC.** BEAM collects each process's heap independently; a LiveView's
  garbage never causes a global pause. Go's GC is process-global. For 10k sessions with
  churning render trees this is a real difference, and it is the reason this monorepo
  already has a controlled GC A/B benchmark
  (`go/benchmarks/gc/` in the canonical monorepo, added in `e3f606e5`; it is
  outside this export) —
  we should reuse its methodology rather than inventing one. **Mitigation:** aggressive
  reuse of render buffers (`sync.Pool`),
  avoiding per-frame allocation in the hot path, and measuring GC pause attribution as part
  of the observability surface rather than discovering it in production.
- **Supervision trees and OTP.** Restart strategies, `:hibernate`, distributed process
  registries, and hot code loading are decades-deep. We are single-node v1, so most of this
  is out of scope — but LiveView's `hibernate_after` has no Go equivalent at all, and idle
  sessions will cost us more than they cost LiveView.
- **The `Rendered` compile-time diff.** LiveView's static/dynamic split is done by the
  Elixir *compiler* via macros. templ generates Go code but does not, today, give us a
  statics/dynamics decomposition with change tracking. Achieving LiveView-class wire
  efficiency will require either a templ-level facility or accepting fragment-granularity
  patches (Datastar/Turbo-class) — **an explicit open question for RFC 0001, and the one
  place where BEAM's metaprogramming is a genuine structural advantage, not just an
  operational one.**

Where Go wins outright for *this* product: a single static binary, `net/http` as the
universal substrate (so `otelhttp`/`promhttp`/zerolog compose without adapters), bounded
channels as a native backpressure primitive, protobuf as a first-class citizen with real
codegen, and a hiring/operating story that fits this monorepo's existing Go-everywhere
posture.

### 6.4 Versus Blazor's circuits, specifically

Blazor's circuit is the design gotth-live most resembles in *state residency* and least
resembles in *coupling*. Three deliberate divergences:

1. **We send HTML, they send tree edits.** Their patches are smaller and unambiguous; ours
   are inspectable, debuggable in DevTools, proxy-transparent, and degrade gracefully
   through a morph algorithm when the client's DOM has drifted (third-party scripts,
   browser extensions, autofill). Blazor's positional edits assume a mirror that is never
   wrong; every real browser eventually makes that assumption false.
2. **We must not require sticky sessions.** Circuit-pinning drags ARR affinity, ingress
   cookie annotations, and a shared Data Protection key ring into the deployment. For this
   monorepo — Caddy at the edge, backends over Tailscale, container-first policy — importing
   load-balancer policy from a UI library is exactly the coupling `CLAUDE.md` forbids by
   default. v1 is single-node, so we get this for free; the RFC should nonetheless state
   **"a reconnect landing anywhere must be correct"** as a design invariant so we do not
   accidentally build a circuit.
3. **We take their flow control.** Sequence-numbered frames, a bounded unacknowledged
   window, client acks, replay on reconnect. This is the best idea in the prior art and it
   is under-copied because it lives in `RemoteRenderer.cs` rather than in a blog post.

### 6.5 Versus Datastar's per-request model

Datastar is right that a plain `http.Handler` is the best observability substrate in Go, and
wrong (for our purposes) that client-resident signals are sufficient. Its per-request model
means: no server-side view state to inspect, no place to attribute a patch to, and — because
upstream and downstream are different HTTP requests — no way to correlate them even in
principle.

gotth-live's answer is to keep Datastar's substrate and add the actor: **one long-lived
connection whose handler is still an ordinary `http.Handler`**, so `otelhttp` wraps it and
context propagates, but whose lifetime is a session rather than a request, so there is a
`sessionID` every metric, log line, span, and frame can carry. We inherit Datastar's
composability without inheriting its statelessness.

Concretely, the split-transport question Datastar answers with "SSE down, fetch up" is
ADR-001's subject, and this teardown produces three inputs to it: (a) SSE cannot carry
binary without base64, which is a direct tax on liquid-proto framing; (b) split transports
make request↔patch ordering undefined, which undermines provenance at the protocol level;
(c) SSE burns one of six HTTP/1.1 connections per origin. All three favour WebSocket. The
counter-arguments — SSE's trivial proxy story, automatic browser reconnection, and
`Last-Event-ID` resumption for free — are real and ADR-001 must weigh them rather than
assume the conclusion.

### 6.6 Why liquid proto with causal IDs gives provenance none of these have

Every frame in gotth-live carries identity. The sketch (details in the mapping spec, not
here):

- an **event frame** carries `event_id`, `session_id`, `client_seq`, and a `causation_id`
  naming the patch frame the user was looking at when they acted;
- a **patch frame** carries `patch_id`, `server_seq`, the `event_id` that caused it, the
  state version before and after, and the trace/span context.

Two properties follow that no system in §5.1 has:

**Bidirectional traceability.** From a DOM node you can name the patch; from the patch, the
state transition; from the transition, the event; from the event, the trace; from the trace,
the HTTP request, the database queries, and the effects. And backwards: from a log line
about a failed effect you can name every DOM patch that user saw as a consequence. Blazor
gets partway via linked activities but the *frame* carries no id, so the chain breaks at the
network boundary — you cannot start from something the user observed. LiveView, Turbo, and
Datastar have no chain at all.

**The `causation_id` on the way up is the piece nobody has.** It makes stale-view conflicts
detectable rather than silent: if a click was made against patch #41 and the server is at
#44, the reducer *knows* the user acted on stale information, and can reject, re-render, or
merge deliberately. Turbo cannot detect that it missed a broadcast (§3.5); LiveView remounts
and hopes; Datastar reverts signals. This is a correctness feature wearing an observability
feature's clothes.

**Why *liquid* proto rather than plain proto.** A causal ID that can be empty is not a causal
ID; a sequence number that can be zero or negative destroys the ordering invariant the whole
flow-control window depends on. Refinement types move those from "documented in a comment
and validated in a handler somebody forgot to call" to "the type has no invalid inhabitants
and the parse boundary rejects hostile wire data." The protobuf refinement-types study in
this repo demonstrated exactly that guarantee, including rejection of hand-crafted hostile
varints at the `Refine*` boundary. See Appendix A.

### 6.7 Why observability is a differentiator, not garnish

The prior art establishes the argument empirically. In all four systems the dominant
production failure is *invisible*:

- LiveView: change tracking silently disabled → payloads quietly 100× larger, no metric fires.
- Turbo: a missed broadcast → the UI is silently stale, and the client cannot know.
- Datastar: proxy buffering → updates arrive in bursts, and nothing reports it.
- Blazor: long-poll fallback → a console warning nobody reads, and 5× the latency.

These are not bugs; they are *degradations without a signal*. A server-driven UI framework
puts the render loop on the far side of a network, and the user-visible symptom ("it feels
slow", "the number is wrong") is many layers removed from the cause. Aggregate histograms do
not close that gap — you need to take one user's complaint and walk it to one line of code.

So the design commitments are:

1. **Per-connection metrics as a first-class surface** — frames in/out, bytes, patch size
   distribution, reducer duration, render duration, queue depth, coalesce count, drop count,
   ack latency, reconnect count — labelled by session, exported via the collector this
   monorepo already runs (`docker-compose.admin.yaml`, Prometheus/Loki on node-b).
2. **OTel spans that span the boundary** — the event frame carries trace context so the
   browser interaction and the server reducer are one trace, not two.
3. **Structured logs that carry the same ids** — via `core.Logger` per `go/CLAUDE.md`, with
   `session_id` / `event_id` / `patch_id` as fields, so a Loki query on a `patch_id` pulled
   out of a browser returns the whole causal chain.
4. **A degradation is always a signal.** Every fallback path — coalescing, dropping,
   transport downgrade, morph mismatch — increments a counter and emits a log. LiveView's
   silent change-tracking loss is the anti-pattern we are explicitly designing against.

That is not garnish. It is the only thing that makes a server-driven architecture
operable at all, and the fact that four mature systems ship without it is the actual
market gap.

### 6.8 What we should copy without shame

- **LiveView**: the dead-render/live-render two-phase mount (SSR first paint, progressive
  enhancement, works with JS off); `streams` as the answer to unbounded collections;
  `phx-debounce`/`phx-throttle` as input-side rate limiting; keyed comprehensions as a
  prerequisite for list diffing that does not degrade to O(n) on prepend.
- **Blazor**: sequence numbers, the bounded unacknowledged window (§4.6), the bounded
  inbound message size, and the honesty of publishing a memory-per-connection figure and a
  latency budget.
- **Turbo**: the `refresh` action — broadcasting "you are stale, re-fetch" instead of
  content is the correct answer for fan-out and personalization, and it composes with
  sequence numbers beautifully (a client that detects a gap re-fetches ground truth rather
  than trying to replay).
- **Datastar**: the `http.Handler` substrate, immediate flush semantics, and a client small
  enough to argue about.

---

## 7. Open questions carried into RFC 0001 / ADR-001

1. **Transport.** WebSocket vs SSE+fetch. This teardown's evidence leans WebSocket (binary
   frames, single ordered path, no six-connection cap, symmetric acks) but SSE's proxy and
   auto-reconnect story is genuinely better and `Last-Event-ID` gives resumption for free.
   ADR-001's job, not this document's.
2. **Diff granularity.** templ does not give us LiveView's compile-time statics/dynamics
   split. Do we (a) accept fragment-granularity patches like Datastar/Turbo, (b) build a
   templ-level change-tracking facility, or (c) diff rendered output server-side and send
   the delta? Each has a different wire-volume and CPU profile, and (c) is the only one that
   needs no templ changes. **Biggest unresolved architectural question.**
3. **Client budget.** ≤12 KB gzip is below every shipped competitor (§5.2) while doing
   strictly more. Is the budget a requirement or an aspiration? A hand-written liquid-proto
   decoder is mandatory either way.
4. **Panic containment.** Go has no supervision tree. The actor-boundary `recover()` +
   session-fault frame + metric is our substitute; the RFC should specify its semantics
   (does the session restart? does the client get a re-render? is client-side form state
   preserved?).
5. **Idle session cost.** LiveView has `hibernate_after`; Go has nothing analogous. What is
   an idle gotth-live session's footprint, and do we evict/serialize idle sessions?
6. **Backpressure policy.** The window and ack mechanism are settled (copy Blazor). The
   *policy* when the window is full — block the reducer, coalesce patches, drop and mark
   stale, or disconnect — is a product decision that must be per-application configurable
   and must always emit a signal.
7. **Benchmark obligation.** Nobody publishes a comparable memory-per-connection figure for
   LiveView. If we want to claim an advantage we must measure LiveView, Blazor, and
   gotth-live ourselves on the same workload. Until then we make no comparative memory
   claims.

---

## Appendix A — How the wire protocol leans on liquid proto

*(Sketch only. The full mapping is a separate spec.)*

The refinement-types study in
`research/protobuf-refinement-types/` — a tree of the canonical monorepo, outside
this export — established two usable layers, and gotth-live wants both:

- **Layer A — annotation + `protoc-gen-gorefine`.** A `(candace.refine.field)` option
  carrying a predicate; the plugin generates a *distinct opaque Go type* per refined field
  whose only non-zero inhabitants passed a checked constructor, with the predicate compiled
  to native Go (no reflection, no interpreter). Crucially, invalid **wire data** is rejected
  at a generated `Refine*` parse boundary — "parse, don't validate" at deserialization,
  proven against hand-crafted hostile varints in the plugin's 187-test suite.
- **Layer B — `refinec`.** The forked protocompile frontend accepting
  `uint64 server_seq = 3 where this > 0;` directly, desugaring to a **byte-identical**
  descriptor, so Layer A's plugin runs unmodified.

The intended use, concretely:

```proto
message PatchFrame {
  uint64 server_seq  = 1 where this > 0;              // monotonic; 0 is not a frame
  string patch_id    = 2 where len(this) > 0;         // provenance anchor, never empty
  string session_id  = 3 where len(this) > 0;
  string causing_event_id = 4 where len(this) > 0;    // the causal edge, structurally required
  uint64 state_version_before = 5;
  uint64 state_version_after  = 6 where this > 0;
}

message EventFrame {
  uint64 client_seq  = 1 where this > 0;
  string event_id    = 2 where len(this) > 0;
  string causation_id = 3 where len(this) > 0;        // which patch the user acted on
}
```

Three properties this buys the protocol, none of which a comment or a hand-written
validator provides:

1. **The causal chain cannot be silently broken.** A patch frame with an empty
   `causing_event_id` is unconstructable in Go and unparseable from the wire. Provenance
   stops being a convention that erodes and becomes a type-level invariant.
2. **The flow-control window's ordering invariant is enforced at the boundary.** Blazor's
   `BatchId` is an ordinary `long`; ours cannot be zero, so "unset" and "first frame" are
   distinguishable by construction, and the ack path cannot be confused by a zero-valued
   default.
3. **Hostile or corrupt frames die at parse, not at use.** The plugin's `Refine*` boundary
   re-checks every field on deserialization, which is exactly where an untrusted browser's
   bytes enter the actor.

Fine print carried over from
that study's `plugin/IMPLEMENTATION.md`
and applicable here: Go cannot forbid an opaque type's zero value (closed at the `Refine*`
boundary instead), `reflect`/`unsafe` defeat anything, `[]byte` values are aliased, and
nested messages are **not** recursively refined. That last one matters — our frame types
must be flat or the guarantee thins out, which is itself a useful design constraint on the
protocol.

Two open items for the mapping spec: (a) if ADR-001 selects SSE, binary proto needs base64
or a protobuf-JSON mapping, and the refinement guarantees must survive that encoding hop;
(b) the client-side decoder must enforce the same predicates or the invariants are
server-only — hand-writing ~1–3 KB of decoder means hand-writing the predicate checks too,
and they should be **generated** from the same descriptors rather than transcribed.

---

## Appendix B — Verification log

**Verified by primary source or direct measurement**

- Version/date table (§0): GitHub Releases API (`gh api repos/{owner}/{repo}/releases`) for
  Datastar, Turbo, LiveView, htmx, idiomorph; hex.pm package API for LiveView release dates;
  `https://go.dev/dl/?mode=json` for Go.
- LiveView diff wire keys (§1.4): `assets/js/phoenix_live_view/constants.ts` @ v1.2.8.
- LiveView morphdom fork (§1.4): `package.json` @ v1.2.8 →
  `"morphdom": "github:SteffenDE/morphdom#sd-keyed-root"`.
- LiveView reload constants (§1.5): `constants.ts` @ v1.2.8 (`MAX_RELOADS = 10`,
  `RELOAD_JITTER_MIN/MAX`).
- LiveView bundle excludes phoenix.js (§1.2): string search of the minified artifact for
  `phx_join` / `LongPoll` → 0 hits.
- Datastar patch modes, event fields, and inlined Idiomorph-derived morph (§2.4):
  `library/src/plugins/watchers/patchElements.ts`.
- Datastar Go SDK API and flush semantics (§2.2, §2.6): pkg.go.dev for
  `github.com/starfederation/datastar-go/datastar`.
- Turbo vendors Idiomorph (§3.4): `src/core/morphing.js` in-repo + 17 `idiomorph`
  occurrences and Idiomorph's `pantry` in the shipped dist; `package.json` declares no
  runtime dependencies.
- Blazor flow control (§4.6): `src/Components/Server/src/Circuits/RemoteRenderer.cs` —
  `_unacknowledgedRenderBatches`, `MaxBufferedUnacknowledgedRenderBatches`,
  `OnRenderCompletedAsync(long incomingBatchId, …)`.
- Blazor defaults (§4.5): `src/Components/Server/src/CircuitOptions.cs` —
  `DisconnectedCircuitMaxRetained = 100`, `DisconnectedCircuitRetentionPeriod = 3 min`,
  `MaxBufferedUnacknowledgedRenderBatches = 10`, `PersistedCircuitInMemory*`,
  `JSInteropDefaultCallTimeout = 1 min`.
- Blazor SignalR/transport/latency guidance (§4.2, §4.9) and OTel names (§4.8): Microsoft
  Learn, `?view=aspnetcore-10.0`.
- Bundle sizes (§5.2): downloaded from jsDelivr and measured with `gzip -9` on 2026-08-04.

**Reported by the vendor/community, not independently verified**

- Blazor ~250 KB per circuit and ~273 KB/user at 5,000 users — Microsoft Learn.
- Datastar homepage's 11.76 KiB figure (my gzip measurement is 13,277 B; the difference is
  consistent with brotli).
- Datastar signal-revert-on-reconnect, `unsafe-eval` CSP requirement, and undocumented
  plugin API — community "production considerations" document.

**Explicitly unverified — do not cite as fact**

- Any per-connection memory number for Phoenix LiveView. The "2 million connections"
  figure is a 2015 **Phoenix Channels** benchmark with no LiveView state and must not be
  presented as a LiveView number. The 40 KB / 3 MB / 150 KB figures circulating in blog
  posts could not be corroborated against any primary source.
- Any per-connection memory number for Turbo/Action Cable or Datastar.
- `blazor.server.js` bundle size — not fetchable from a public CDN or npm.
- Whether Datastar 1.0.2 actually requires `unsafe-eval` (string search of the minified
  bundle for `new Function` returned 0, which is suggestive but not dispositive).

## Sources

- [Phoenix LiveView docs — Phoenix.LiveView module](https://phoenix-live-view.hexdocs.pm/Phoenix.LiveView.html)
- [Phoenix LiveView docs — Assigns and HEEx templates](https://phoenix-live-view.hexdocs.pm/assigns-eex.html)
- [Phoenix LiveView docs — Telemetry](https://phoenix-live-view.hexdocs.pm/telemetry.html)
- [Phoenix LiveView docs — JavaScript interoperability](https://phoenix-live-view.hexdocs.pm/js-interop.html)
- [Phoenix LiveView 1.1 released](https://www.phoenixframework.org/blog/phoenix-liveview-1-1-released)
- [Phoenix LiveView 1.2 released](https://phoenixframework.org/blog/phoenix-liveview-1-2-released)
- [The Road to 2 Million WebSocket Connections in Phoenix (2015, Channels — not LiveView)](https://www.phoenixframework.org/blog/the-road-to-2-million-websocket-connections)
- [phoenix_live_view source @ v1.2.8](https://github.com/phoenixframework/phoenix_live_view/tree/v1.2.8)
- [Datastar](https://data-star.dev/)
- [Datastar — SSE events reference](https://data-star.dev/reference/sse_events)
- [Datastar releases](https://github.com/starfederation/datastar/releases)
- [Datastar `patchElements.ts`](https://github.com/starfederation/datastar/blob/main/library/src/plugins/watchers/patchElements.ts)
- [datastar-go SDK docs](https://pkg.go.dev/github.com/starfederation/datastar-go/datastar)
- [Datastar production considerations (community)](https://alvarolm.github.io/datastar-resources/docs/considerations.html)
- [Turbo Handbook — Streams](https://turbo.hotwired.dev/handbook/streams)
- [Turbo Handbook — Page refreshes with morphing](https://turbo.hotwired.dev/handbook/page_refreshes)
- [hotwired/turbo](https://github.com/hotwired/turbo)
- [Host and deploy ASP.NET Core server-side Blazor apps](https://learn.microsoft.com/en-us/aspnet/core/blazor/host-and-deploy/server/?view=aspnetcore-10.0)
- [ASP.NET Core Blazor SignalR guidance](https://learn.microsoft.com/en-us/aspnet/core/blazor/fundamentals/signalr?view=aspnetcore-10.0)
- [ASP.NET Core Blazor performance best practices (metrics and tracing)](https://learn.microsoft.com/en-us/aspnet/core/blazor/performance/?view=aspnetcore-10.0)
- [Blazor `RemoteRenderer.cs`](https://github.com/dotnet/aspnetcore/blob/main/src/Components/Server/src/Circuits/RemoteRenderer.cs)
- [Blazor `CircuitOptions.cs`](https://github.com/dotnet/aspnetcore/blob/main/src/Components/Server/src/CircuitOptions.cs)
- [dotnet/aspnetcore#64607 — circuit resume after retention period](https://github.com/dotnet/aspnetcore/issues/64607)
- Refinement types for Protocol Buffers — `research/protobuf-refinement-types/`
  in the canonical monorepo, outside this export
