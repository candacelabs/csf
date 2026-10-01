# The dashboard example

A simulated metrics feed pushing from the server twenty times a second, three
live regions patched independently, and two plain-HTMX regions on the same page.

It is the example PRD **FR-62** asks for and, with its Ginkgo suite, **FR-63**.
Phase 3's exit criteria are gated against it, so most of what is below is there
because a criterion names it — and §"[The five properties, and the spec that
proves each](#the-five-properties-and-the-spec-that-proves-each)" is the table a
gate reader wants.

Where the library was not enough, [`FRICTION.md`](FRICTION.md) says so, item by
item, and the code carries a comment naming the item.

---

## Run it

The host needs no Go. Build the library's own toolchain image once — `dis run`
is not the recipe any more, because the dis workspace is `pkg/gotth/` and this
example is that directory's sibling — then run from the export root, which is
this repository's root:

```bash
docker build -t dis-gotth-live:latest pkg/gotth/.dis
docker run --rm -v "$PWD:/workspace" -w /workspace/examples/gotth/dashboard \
    -p 8082:8082 dis-gotth-live:latest go run . -addr 0.0.0.0:8082
```

Or, if you have Go 1.26 locally:

```bash
cd examples/gotth/dashboard
go run .                        # http://127.0.0.1:8082
go run . -addr 127.0.0.1:9002
go run . -interval 200ms        # a slower feed, for reading the frames
go run . -provenance            # the causal row for every transition, as JSON
go run . -origin http://192.168.1.10:8082   # allow another browser Origin
go run . -resync-cost 200       # measure a full resync and exit
```

Run it **from this directory**: the vendored HTMX bundle is found by a relative
path (`-htmx` overrides it, and [F-3](FRICTION.md) says why it is like that).
There is no `npm install`, no bundler and no code generation step to run — the
client runtime is compiled into the binary and served by the same handler that
serves the WebSocket. The only generated file here is `view_templ.go` and it is
committed; regenerate it with `bash pkg/gotth/gen.sh` from the export root
after editing `view.templ` — that is the script FR-7's reproducibility gate
runs, and it lists this file among the outputs it compares.

While it runs, the FR-34 backpressure numbers are at
<http://127.0.0.1:8082/metrics.txt>.

## What to expect

1. The page renders with the feed's current reading already in it; the
   connection dot goes **connecting → live**. The snapshot that arrives a moment
   later morphs the page to bytes it already has, so there is no flash.
2. **The sample number climbs with nothing clicked.** That is the server
   pushing. The alert log and the control panel are not being re-rendered while
   it happens — see the fragment table below.
3. Press **Pause this tab**. The feed keeps running for every other tab, and
   resuming shows the *current* reading rather than replaying what you missed.
4. Open a second tab and press **Clear alerts** in one. The other tab's alert
   log empties without it asking.
5. Press **Load** in the deploys card and **Load operator notes** in the control
   panel. Both are plain HTMX over ordinary HTTP. The live regions keep updating
   underneath, and the swapped-in text survives every patch.
6. Watch `/metrics.txt` while you do all that.

**The feed is simulated and says so.** The values are a bounded random walk over
three made-up series, produced by a generator this process seeds — nothing reads
a real machine, and an example that scraped `/proc` would be measuring the VM it
runs on rather than demonstrating a library. What is real is everything the walk
feeds into: a source of change the server owns, at a rate no browser asked for,
delivered by a push over one connection.

## Where to look in the source

| File | What is in it |
|---|---|
| [`dashboard.go`](dashboard.go) | `State`, the pure `Reduce`, the render helpers, and the `live.Config` with its three fragments |
| [`feed.go`](feed.go) | The shared feed every session reads, the effects, and the subscription pump |
| [`view.templ`](view.templ) | Three live regions and two plain-HTMX regions, with `live.Region`, `live.On`, `live.Preserve` and `app.Document` — whose head extension is where this example's conditional HTMX `<script>` goes |
| [`main.go`](main.go) | Flags, routing, the HTMX digest check, the Origin allowlist, graceful shutdown |
| [`metrics.go`](metrics.go) | A `metric.MeterProvider` built from the OTel **API** alone, so the FR-34 numbers exist with no new dependency |
| [`resync.go`](resync.go) | The resync-cost measurement behind `-resync-cost` |
| [`wire.go`](wire.go) | A frame reader and writer over `protowire`, written from `frame.proto`, for the one measurement that runs outside a test |
| [`dashboard_test.go`](dashboard_test.go) | The reducer, determinism, the render helpers, the feed, and startup |
| [`wire_test.go`](wire_test.go) | FR-62's five properties, measured on the frames `livetest.Client` decodes off real dialled connections |
| [`FRICTION.md`](FRICTION.md) | Five things the library made harder than it should have, one of them closed and one half closed, and one observation |

## The three fragments, and why there are three

This is the whole design and everything else follows from it.

| Fragment | Whose it is | Re-renders when | Moves |
|---|---|---|---|
| `dashboard.meters` | everybody's | the reading or the history moved | 20×/second |
| `dashboard.alerts` | everybody's | a series crossed the threshold | minutes apart |
| `dashboard.controls` | **this session's alone** | **this session's** pause, backpressure or notice moved | when you act |

One fragment covering all three would repaint the alert log and the control
panel twenty times a second to deliver a number that changed in one of them.
That is what FR-62's "multiple independent live regions" is asking to be shown,
and it is asserted on the frames rather than on the declarations.

**How to break it, and where that shows up.** Widening the controls region's
`Dirty` to include the meters is legal, passes `livetest.AssertDirtyComplete`,
and **changes nothing on the wire** — the controls markup does not change when a
reading does, so identical-render suppression drops it before it reaches a
frame. Over-declaring costs a *render*, not a patch. The only place it is
visible is `gotthlive_patches_suppressed_total`, which is why both
independent-region specs assert that counter is zero. That was found by mutating
the code, not by reasoning about it; [O-1](FRICTION.md) records it.

## The loop, once through

The feed samples. Nothing on this path is specific to any one tab, which is why
every tab repaints for free.

```
feed ticker (application-owned)      each session's goroutine          browser
   │
   │ Sample(): advance the walk, take the reading under one lock
   ├──────────────► subscriber.offer(update)   [bounded queue, per session]
   │                       │
   │                       │ pump (a long-running effect)
   │                       ├──► emit(live.Event{Name: "dashboard.sample", …})
   │                       │        │
   │                       │        ├─► Reduce: fold the reading  →  new State
   │                       │        ├─► Dirty: meters yes, alerts no, controls no
   │                       │        ├─► render meters; suppress if the bytes match
   │                       │        └─► Patch{origin: effect:dashboard.subscribe}
   │                       │                    │
   │                       │                    └──────────────────► morph
   │                       │                                          the
   │                       │                                          meters
```

Pressing **Sample now** enters the same loop one step earlier: the reducer
returns a `ProbeEffect` carrying the event's identifier, the effect samples the
shared feed, and the reading comes back to *this* session with that identifier
on the emitted event's contributing list — and to every other session without
it, because an identifier is session-scoped and naming another session's event
is not a thing that can be true.

## Backpressure, and what "a patch is never dropped" means

The library's ladder has three stages, and this example demonstrates the first
two. `/metrics.txt` shows all of it.

| Stage | Trigger | What happens | Where you see it |
|---|---|---|---|
| **Coalesce** | half the outbound window unacknowledged | a transition stops emitting a frame of its own and collapses into the next one, **carrying its provenance with it** | `gotthlive_patches_coalesced_total` |
| **Degrade** | the window is full | nothing is emitted at all until an acknowledgement re-opens it; the application is told through a synthesized event | `gotthlive_slow_client_events_total`, and "falling behind" in the control panel |
| **Evict** | the window stays full past `SlowClientGrace` | the *session* is closed with `slow_client` | `gotthlive_connections_closed_total{code="slow_client"}` — QA-2's chaos suite, not here |

**A patch is never dropped.** It coalesces, then it defers, and then the session
is closed. Losing a patch while keeping the connection would leave the DOM
disagreeing with the server with nothing saying so, which is the one outcome the
protocol will not produce. The spec that holds this asserts the consequence
rather than the intent: server sequence numbers arriving at a stalled client are
**contiguous**, because a dropped patch would leave a hole.

That is also why FR-34's "patch drops" has no counter — see [F-5](FRICTION.md),
which is a note for PM-1 rather than a defect. The **coalesce ratio** is derived,
the way the reconnect signal already is:

```
gotthlive_patches_coalesced_total / gotthlive_frames_sent_total{kind="patch"}
```

Note the denominator. `gotthlive_patches_sent_total` counts fragment *updates*,
so a patch carrying three regions increments it three times, and using it would
make the ratio silently wrong in the safe-looking direction.

## HTMX on the same page, and the rule you have to know (D-16)

There are two plain-HTMX regions and they are placed differently on purpose.

**The deploys card is outside every live region.** It is not a fragment, it is
not in `Config.Fragments`, no patch can name it, and morph never touches it
(FR-31). Its button makes an ordinary `GET` to an ordinary `http.Handler` that
knows nothing about gotth-live.

**The operator-notes island is inside the controls region, behind
`live.Preserve()`.** That is the sanctioned way to host HTMX-owned DOM inside
server-owned DOM: the rule is innermost-declaration-wins, so the section is the
server's and this subtree is not, and morph leaves it and everything under it
alone (FR-27, FR-32).

> ### The rule
>
> **`hx-*` markup that a morph *inserts* is inert until `htmx.process` runs on
> it.**
>
> HTMX scans the document once, at load. An element the server starts rendering
> *mid-session* arrives in a patch, morph puts it in the DOM, and HTMX has never
> seen it: clicking it does nothing at all — no request, no error, no console
> message. Markup that HTMX already processed keeps working through any number
> of morphs, so the failure only affects the elements a patch **introduced**,
> which is what makes it easy to miss in development and certain to appear in
> production.
>
> This is defect **D-16**, measured in the browser at checkpoint 2 (see
> `docs/qa/checkpoint-2-browser.md` §D-16). It is **documented, not fixed**:
> `docs/gates/checkpoint-2.md` §8 carries the ruling, and the conformance suite
> holds a spec written to go red on the day the gap closes.
>
> **What to do about it.** Either of these, and this example does both:
>
> - put HTMX-owned markup **outside** every live region, or **inside** a
>   `live.Preserve()` subtree — in both cases HTMX processes it at page load and
>   morph never replaces it, so the situation cannot arise; or
> - if a live region genuinely must render `hx-*` markup mid-session, call
>   `htmx.process(el)` on the region after the patch applies. You will need your
>   own script to do it, and that script needs to survive a strict CSP.
>
> The first is what the library's design points at, and it is why FR-32 makes a
> live region server-owned in the first place: an HTMX swap into one would be
> overwritten by the next morph anyway.

`wire_test.go` carries the executable half of this: a spec that renders each
live region and **fails if any of them contains an `hx-` attribute outside a
`live.Preserve` subtree**, with the reason in the failure message. Adding an
`hx-get` to the meters region is a reasonable-looking thing to do, it works on
first load, and it stops working the first time the server re-renders that
region. The spec turns that into a red build.

## The five properties, and the spec that proves each

FR-62 names five properties. Each has a spec that asserts it on the frames, and
each spec was **watched go red** under a mutation that makes the property false.
A spec nobody has seen fail is a spec that has never been shown to test
anything.

| FR-62 property | Spec (`wire_test.go` unless noted) | Mutation that turned it red |
|---|---|---|
| **High-frequency server-initiated updates** | `patches a browser that has never sent an event, at the feed's rate` — 25 patches with `eventFrames() == 0`, every one attributed `effect:dashboard.subscribe` | `Config.Init` returns no `SubscribeEffect` → nothing is ever pushed |
| | `shows a rising sample number in the markup` — the rendered sample count goes 1…5 | *(covered by the same mutation)* |
| **Multiple independent live regions** | `puts only the meters on the wire when the feed samples, and renders nothing else at all` | controls `Dirty` widened to include `Meters` → suppression counter moves off zero |
| | `puts only the alert log on the wire when a series crosses the threshold` | meters `Dirty` widened to include `Alerts` → same |
| | `puts only this tab's controls on the wire when it pauses, and nothing on the other tab's` | *(the two above)* |
| **Batching / debounce, provenance intact** | `coalesces once the window fills, and the coalesced patch names every contributing event` — 12 probes, no acks, `AckWindow=4`; the union of every patch's contributing list must equal the 12 probe event IDs, resolved through the provenance log | `ProbeEffect` built without `Cause` → union empty, expected 12 |
| | `names no event it did not have, at a load where the ladder reaches its second stage` — 20 probes; every contributing ID must be a real event of this session | *(the same mutation)* |
| **Backpressure under a slow client** | `bounds the outbound queue and tells the application, then recovers when the client catches up` — 200 samples, ≤4 patches, one slow-client event, then the notice and the recovery | reducer ignores `live.SlowClientEvent` → the degraded notice never arrives |
| | `degrades the slow session and leaves every other session alone` | *(the same)* |
| | `never drops a patch: it coalesces, then defers` — contiguous sequence numbers | *(the same)* |
| **A plain-HTMX region on the same page** | `serves its fragments from ordinary handlers that never touch a live session`; `declares the deploys card outside every live region`; `keeps the HTMX island inside the controls region behind live.Preserve` | — |
| | **D-16 guard**: `renders no hx-* attribute inside a server-owned live region` | `hx-get` added to `MetersRegion` → red, with the D-16 explanation in the message |

Two more mutations, on code this example owns rather than on a property of the
library:

| Spec | Mutation that turned it red |
|---|---|
| `computes the coalesce ratio against patch frames rather than fragment updates` | denominator widened from `{kind="patch"}` to every frame kind |
| `answers every request with a snapshot and agrees with the library's own byte count` | `FrameBytes` records markup length instead of the frame's |

The browser half of the HTMX story — that a swap into the preserved island
actually survives a morph in a real DOM — is the conformance suite's
(`test/internal/conformance`, browser-labelled), not this module's. These specs
assert what the server sends and what the markup declares; they cannot assert
what Chromium does with it.

## The resync cost

`go run . -resync-cost 200` prints the measurement and its method. Below is the
program's own output, pasted rather than transcribed, at the example's defaults
(`-seed 1`, `-interval 50ms`):

```
state the snapshots rendered
  200 distinct state versions across 200 samples; last was version 236
  at the last snapshot: 5 alert rows, 30-sample sparkline

bytes on the wire, per snapshot
  frame: min 2220  p50 2378  p90 2661  max 2939  (n=200)
  markup: min 2079  p50 2231  p90 2512  max 2790  (n=200)
  protocol overhead (frame - markup, median): 147 B

  markup by region, last snapshot
    dashboard.alerts       925 B
    dashboard.controls     936 B
    dashboard.meters       929 B

  the library's own gotthlive_resync_bytes over the same run:
    n=200 mean=2368.1 B max=2939 B

latency, request written to snapshot read
  resync: min 91µs  p50 172µs  p90 256µs  max 579µs  (n=200)
```

**Where this was taken, because a latency figure without its host is a
decoration.** Commit `35d4e258`, in `dis-gotth-live:latest` (Go 1.26.5), on
`node-a` (a neutral name for the machine; every figure below is the one
observed) — 32 cores, and **not quiescent**: load average
4.06 / 5.24 / 4.92 at the start of the run, twenty containers up, `gpu-desktop-steam-1`
among them (healthy, GPU at 5 %, no streaming session in progress). Nothing in
this project pretends to have a quiet machine, and a number taken on a busy one
is worth more with that said than without it.

**The bytes are reproducible and the latency is not.** Two consecutive 200-sample
runs produced byte-for-byte identical byte figures — the feed's walk is seeded
and the markup is a function of the state it renders — and different latencies:
p50 172 µs then 189 µs, max 579 µs then 1.771 ms. Quote the byte figures; treat
the latency as the shape of a distribution taken on a contended host, which is
why it is printed as one rather than as a mean.

**Read the method before quoting the numbers.** One loopback WebSocket to the
same process, acknowledging every patch as a browser does. The measurement starts
once the session has folded 30 patches, which is where the sparkline reaches full
length and the state stops growing. Then, per sample: **one meters patch is read
and deliberately left unacknowledged**, so the cursor this client can honestly
claim is strictly behind what the server has emitted; the `ResyncRequest` names
**that** sequence — the one it has really applied — and nothing is acknowledged
while the resync is outstanding, because an `Ack` names the highest *contiguous*
sequence applied and a client with a hole below those frames has no such claim to
make. The Snapshot's own cumulative `Ack` repairs the gap and the next iteration
opens a fresh one. **Each snapshot therefore supersedes the tail since the last
acknowledgement, not the whole session.** One feed sample passes between
measurements, so the 200 samples cover 200 distinct state versions rather than
200 reads of one. The resync rate budget is relaxed from the library's `1s`/burst
3 to `1ms`/burst 208, which bounds how *often* a client may ask and not what one
resync costs.

**Why the request looks like that, and what it replaces.** This measurement used
to send `last_applied_seq=1` on every request from a session that had
acknowledged everything, and was answered with a Snapshot superseding the whole
session. `c1338120` ended that: the server now clamps a claimed cursor up to what
it already knows — the client's own acknowledged high-water mark and the sequence
of the last snapshot it sent — before deciding whether the request describes a
gap at all, so a caught-up client can no longer obtain a snapshot by understating
the field. That is not a limitation the harness works around. The old request's
answer superseded sequences this same client had already applied, and the shipped
runtime's `applied()` closes `4002` on exactly that overlap — so the old figure
was the cost of a frame a browser would have hung up on. The gap is now made real
instead of claimed, which is also the only state a browser ever asks from
(FR-11).

**What changed in the numbers, measured rather than argued.** `resync.go`
predicted the difference would be two varints, and it is: `p50` 2377 → **2378 B**,
`p90` 2660 → **2661**, `max` 2937 → **2939**, `min` unchanged at 2220 — the
supersession range's lower bound is now a three-digit sequence instead of the
literal 2, which is one more varint byte, occasionally two. The direction is
**up**, by 1–2 bytes on a ~2.4 KB frame. The old figure was not badly wrong; it
was produced by a program that no longer exists, described by a method paragraph
that described that program, and this project does not publish "probably still
roughly correct". Per-region markup moved by one byte on `dashboard.meters`
(928 → 929) because the run covers a different window of the walk; the library's
own histogram moved with the frames it counted (mean 2363.9 → **2368.1 B**).

Latency includes the loopback round trip and this process's scheduler and
excludes everything a browser does with the frame afterwards. Bytes are the
frame as it arrived; the library disables WebSocket compression, so a deployment
behind `permessage-deflate` sends fewer.

**The two byte figures are produced independently.** `gotthlive_resync_bytes` is
recorded server-side by code that has never seen `wire.go`; the frame lengths are
measured client-side by a decoder that has never seen the library's framer. They
agree exactly, and a spec asserts that they do — agreement is evidence, and
disagreement would be a defect report rather than a rounding difference.

**One number moved while this was being written**, and it is worth knowing why:
`dashboard.controls` was **1612 B** until an HTML comment was moved out of the
markup and into a Go comment. templ renders HTML comments, so 676 bytes of
explanation were in every controls patch — on the fragment this example
re-renders whenever anybody clicks anything.

## The suite

```bash
docker run --rm -v "$PWD:/workspace" -w /workspace/examples/gotth/dashboard \
    dis-gotth-live:latest go test -race -count=1 ./...
```

71 specs, about ten seconds, most of it deliberate waiting for silence: several
properties are "and then nothing else arrived", which cannot be asserted faster
than the idle period it is measured over. `ci.sh` runs it beside the counter and
chat suites, because an example CI does not run is a regression suite in name
only (FR-63).

The specs are split by what they can see. `dashboard_test.go` is the application
in isolation — the reducer, replay determinism through `livetest.ReplayN`,
`livetest.AssertDirtyComplete`, the render helpers, the feed's edge-triggered
alerts and bounded backlog, the Origin allowlist and the HTMX digest check.
`wire_test.go` drives real WebSockets through `livetest.Client` and asserts on
the frames it decodes, because three of FR-62's five properties are
unfalsifiable from inside the application.

Two things in the suite are worth copying:

- **The feed does not tick by default.** Specs drive `feed.Sample` themselves, so
  an assertion can be "this patch carried exactly these fragments" instead of
  "eventually something like this arrived". Only the high-frequency spec starts
  the ticker, because "patches arrive with nothing asking for them" is the one
  property that needs it.
- **The harness never acknowledges on its own.** "This client stopped
  acknowledging" is the condition the coalescing and backpressure specs are built
  on, and a helpful auto-ack in the harness would have made the whole
  backpressure ladder unreachable — a suite that passes because it never reached
  the code it names.

## Two things this example does deliberately differently from chat

**No identities.** `Authenticate` is `live.Anonymous`, `Authorize` is
`live.AllowAll`, `CSRF` is `live.NoCSRFCheck` — all three named so that
`grep -rn 'live\.Anonymous\|live\.AllowAll\|live\.NoCSRFCheck'` finds every
opt-out. A read-only operations demo has no accounts, so there is no identity to
derive and no per-event rule to apply. `examples/chat` is where that story is
told, and a real dashboard behind an SSO proxy would do what chat does.
`NoCSRFCheck` is only safe here because `Origins` is a real allowlist derived
from the listen address, which is the library's own stated condition.

**A real queue in the feed, not a latest-value-wins slot.** A gauge reading is
absolute, so a real dashboard would very likely collapse undelivered samples in
the application layer — the counter example's slot. This one deliberately does
not, because collapsing samples in the application would be doing the batching
FR-62 asks the *library* to do, and it would discard the causal edge a probe
carries before the library ever saw it. The coalescing this example measures
would then be partly `feed.go`'s, and the provenance assertion would be
asserting against a queue that had already thrown the evidence away.
