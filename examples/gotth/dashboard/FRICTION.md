# Friction, from building the dashboard example

Where the library was insufficient for something PRD **FR-62** requires, or for
something a live dashboard is simply expected to do. Each item says what was
needed, what the library offers, the workaround taken, the shape I would
propose, and what it blocks.

Nothing here was fixed by editing the library at the time it was written. Each
workaround still open is marked in the code with a comment naming its item.

**One is now closed, and one is closed by half.** F-1 asked the library to
export the names it synthesizes when the outbound window fills and drains; it
does, in `ec4ef65d`. F-2 asked for an exported way to read a frame; `livetest`
now has one, so this example's **specs** no longer decode anything themselves —
what is left of the item is the non-test measurement, which cannot use a helper
that takes a `testing.TB`, and the subprotocol constant the library still does
not export. Both headings are kept so that the numbering does not move and a
reader arriving from a document that cites either lands somewhere.

**Summary: nothing here blocks a Phase 3 exit criterion outright.** One was a
vocabulary gap that a spec had to stand in for and was the item most likely to
bite somebody (F-1, now closed). Two are the second application of a tax `examples/chat`
already recorded, and are therefore evidence rather than news (F-2 is chat's
F-1, since closed on the test path; F-4 is chat's F-5). One is about what an
example that wants HTMX has to do today (F-3). One is a metric the requirement
names and the library does not export, where the library is right and the
requirement's wording is what should move (F-5) — that one is PM-1's.

**One is not friction at all** and is filed as an observation, because it is a
property of the library that this example measured rather than a thing that
made the example harder: O-1, on where over-declaring a fragment actually costs
something.

---

## F-1 — *Resolved.* The library's backpressure events have exported names

Landed in **`ec4ef65d`**, in the shape this item proposed and with one change to
it worth reading before writing the next friction note.

The item reported that FR-51's *defined* degradation is a reducer branching on
the names the library synthesizes into a session's own mailbox, that those names
lived in `internal/protocol/frames.go` where an application cannot reach them,
and that `dashboard.go` therefore spelled `"timer:slow_client"` and
`"timer:client_recovered"` out as literals. It argued that this was not a small
thing: it is exactly the shape that produced `live.EffectFailedEvent`, and the
accident is silent, because a reducer matching the wrong string falls through
its `switch` to the default and the dashboard never tells anybody it is falling
behind.

`live.SlowClientEvent` and `live.ClientRecoveredEvent` now exist, `dashboard.go`
branches on them, and `docs/guide/events-and-forms.md`'s table names all three
synthesized events as constants instead of two of them as strings.

**The change to the proposal.** This item wrote the two constants as literals,
following `EffectFailedEvent`. They shipped as `= protocol.SourceSlowClient` and
`= protocol.SourceClientRecovered` instead — one spelling of each string in the
module rather than two that agree until somebody edits one. The cost is that
godoc prints a name from a package the reader cannot import, which is the reason
`EffectFailedEvent` duplicates its literal; it is paid back by quoting both
values in the doc comment.

**What replaced the workaround's spec, and why it is not the same spec.** The
workaround here was `wire_test.go`'s backpressure spec, and the item's own
complaint about it stands — a spec in an example is a poor substitute for a
constant, because the next application will not have the spec. The library now
carries its own: `live_test.go`'s *"backpressure vocabulary"* drives a real
session into the ladder's second stage and asserts on the name the reducer was
handed. It had to be behavioural rather than an equality between two constants,
because a constant declared as another constant agrees with itself by
construction — which is the second-order consequence of taking the single
source of truth, and is worth knowing in advance the next time that trade comes
up.

---

## F-2 — *Closed for the specs, open for the measurement.* Reading a frame outside a test

**Needed.** Three of FR-62's five properties are claims about what is on the
wire: which fragments a patch carries, which events a coalesced patch names, and
how many patches a client that stops acknowledging can be sent. None of them is
checkable from inside the application. The resync-cost criterion needs the same
thing for a different reason — "bytes for a full resync" is a length of what the
server sent, which the server's own state does not have.

**What has closed.** When this item was written, `live/livetest`'s
documentation described a `Client` that "lands with the benchmark harness" and
it was not there, so nothing exported decoded a frame at all. `livetest.Client`
is built now. `wire_test.go` dropped its driver and its decoder onto it and
proves all three wire properties with the library's own second exported package
— which is what chat's F-1 asked for, and that item is closed.

**What has not.** `MeasureResync` cannot follow the specs, and the reason is
structural rather than a matter of time: `livetest.NewClient` takes a
`testing.TB` first, deliberately, and `MeasureResync` runs from
`go run . -resync-cost 200` in the example binary — because a measurement whose
command is "run this test with these flags" is a measurement nobody re-runs.
Reaching it would mean linking `testing` into an example binary or fabricating a
`testing.TB` in `main`, and the whole argument for a separate `livetest` package
is that neither should be easy. So `wire.go` survives, in a **non-test** file,
and that is the sharper statement of the original gap: it is not only tests that
need to read a frame.

It deliberately does **not** import
`pkg/gotth/internal/protocol/gotthlivepb`, even though Go's lexical internal
rule would permit it from this path: a consumer's module is not under that
prefix and never could, so an example that measured with the library's private
codec would be measuring with a tool no reader of the example can pick up.

**The subprotocol name is still not exported.** The handshake requires
`Sec-WebSocket-Protocol: gotth-live.v1`. The client runtime sends it, the server
checks it, and anything else that dials — a measurement, an operator with a
debugging client — has to hard-code the string; `internal/protocol.Subprotocol`
is where the library keeps it, which an application cannot reach. `wire.go`
declares its own copy.

**Proposed shape, for the part that is left.** One line in `live`, independent
of `livetest` and much cheaper than it:

```go
// Subprotocol is the WebSocket subprotocol a gotth-live handshake requires.
const Subprotocol = "gotth-live.v1"
```

A second, larger shape would close the rest: a decoder that does not require a
`testing.TB` — `livetest.Client` minus the spec harness — so that a measurement
in an application binary can read what it received. This item does not propose
it, because a library exporting a codec is a bigger decision than exporting a
constant and it is not this example's to make.

**Blocks.** Nothing. It cost this example ~380 lines, which was the second time
that bill was paid; `wire.go` is down to what one non-test measurement needs.

---

## F-3 — An example that wants HTMX has to find HTMX

**Needed.** FR-62 requires "a plain-HTMX region on the same page". A plain-HTMX
region needs HTMX in the browser.

**What the library offers.** Nothing, correctly — gotth-live does not depend on
HTMX and must not ship a copy of it. But this repository already has exactly one
copy, vendored for the conformance suite at
`test/internal/conformance/testdata/htmx-2.0.10.min.js`, with its SHA-256
recorded beside it.

**Workaround.** `main.go` reads that file by relative path, verifies the digest
at startup, and serves it from the example's own handler. The digest check is
not decoration: this example serves somebody else's JavaScript to a browser, and
"the file at this path" is not provenance. When the file is not reachable the
page says so in the markup and the live half still works, because an HTMX region
that silently does nothing is the failure this example is partly about.

The relative path is the ugly part. It resolves when the example is run the way
its README says to run it — from its own directory — and `-htmx` exists for
every other case.

**Proposed shape.** Nothing in `live`. This is a repository-layout question:
`test/internal/conformance/testdata/` is a strange place for the one artifact
two unrelated things need. A `third_party/htmx/` directory with the bundle, its
digest, and its licence, referenced by both, would remove the relative path and
the surprise.

**Blocks.** Nothing.

---

## F-4 — Every application re-implements the fan-out pump

**Needed.** A source of change the server owns, delivered to every subscribed
session, with a bounded backlog per session and a policy for a session that
cannot keep up. That is the whole of FR-62's first property and half of its
fourth.

**What the library offers.** `Config.Execute` is handed a `live.Emitter`, and a
long-running effect is the only place an application can inject events into a
session. Everything above that — the subscriber registry, the per-session queue,
the offer-and-drop, the retry cadence, the refusal budget — is the
application's.

**Workaround.** `feed.go` implements it: 90 lines of subscriber registry, offer,
pump, and refusal counting. It is the **third** independent implementation of
the same shape in this repository, after `examples/counter` and
`examples/chat`, and the three agreed on `retryDelay` and `maxRefusals` without
consulting each other, which is the part worth recording. Chat's F-5 called
this out with two data points; this is the third.

**Proposed shape.** Not a pubsub — the library should not own a broker. What
recurs is narrower:

```go
// package live
//
// Pump delivers values from ch to the session, retrying a full mailbox with
// the library's own backoff and returning a Retryable error when the session
// refuses more than budget in a row.
func Pump[T any](ctx context.Context, ch <-chan T, emit Emitter, to func(T) Event) error
```

Every one of the three applications would collapse to a channel and a mapping
function, and the retry cadence would be the library's business rather than
three copies of a constant.

**Blocks.** Nothing. It costs each example the same 90 lines, and the third
occurrence is what turns "an example needed this" into a pattern.

---

## F-5 — FR-34 names a metric the library does not export, and should not

**Needed.** PRD Phase 3: "Backpressure metrics exported: queue depth, drops,
coalesce ratio (FR-34)."

**What the library offers.**

- **Queue depth** — yes, twice: `gotthlive_outbound_window_depth` and
  `gotthlive_mailbox_depth`, both histograms.
- **Coalesce ratio** — the numerator only, as
  `gotthlive_patches_coalesced_total`. A ratio is not a metric, it is a
  recording rule, and `instrumentation.md` §2.2 already makes that argument for
  reconnects. The denominator is `gotthlive_frames_sent_total{kind="patch"}`.
  Note which one: `gotthlive_patches_sent_total` counts fragment **updates**, so
  a patch carrying three regions increments it three times and using it would
  make the ratio silently wrong in the safe-looking direction.
- **Drops** — **there is no patch-drop counter, and there should not be one.**
  On this design a patch is never dropped: under pressure it coalesces into the
  next one with its provenance intact, then it is deferred entirely, and if the
  client still does not acknowledge, the *session* is closed with `slow_client`.
  Dropping a patch while keeping the connection would leave the DOM disagreeing
  with the server with nothing saying so, which is the one outcome the protocol
  will not produce. `wire_test.go` asserts the consequence directly: server
  sequence numbers arriving at a stalled client are contiguous, because a
  dropped patch would leave a hole.

**Workaround.** `metrics.go` derives the ratio and `Meters.Report` prints the
drops that are real, each of a different thing — suppressed renders, events
refused for a full mailbox, acknowledgement frames refused for a full ack
channel — under a heading that says a patch itself is never dropped and why.

**Proposed shape.** This is **PM-1's**, not DEV-1's. FR-34's list should say
`patch drops` → `patches coalesced and the slow-client close`, or the Phase 3
box should say "coalesce ratio, derived" the way the reconnect signal is already
documented as derived. The requirement is currently asking for a number whose
existence would be a design defect.

**Blocks.** Nothing here. It would block a literal reading of the Phase 3 box,
which is why it is written down.

---

## O-1 — Observation: over-declaring a fragment is invisible on the wire

Not friction — a property of the library this example measured, recorded here
because it changed a spec and would change anybody else's.

FR-62's second property is "multiple independent live regions", and the obvious
way to falsify a claim about it is to widen a fragment's `Dirty` function and
watch a patch grow. That does not happen. Widening the controls region's `Dirty`
to include the meters leaves every wire assertion green, because the controls
markup does not change when a reading does and identical-render **suppression**
drops it before it reaches a frame.

So over-declaring costs a **render**, not a patch — and at twenty samples a
second, per session, the render is the cost that matters. It is visible in
exactly one place: `gotthlive_patches_suppressed_total`, which counts fragments
that declared themselves dirty, were rendered, and produced bytes the client
already had.

Both of this example's independent-region specs therefore assert two things: the
patch carried one region, and the suppression counter is **zero**. The first
alone is green under the mutation; the pair is red. `livetest.AssertDirtyComplete`
does not help here and says so in its own documentation — it catches
under-declaring, which is the mistake that produces a stale region, and treats
over-declaring as free. It is free in correctness and it is not free in CPU.

Worth a sentence in the fragment documentation: **the suppression counter is how
you find an over-declared `Dirty`.**
