[identifiers genericized for publication - measurements unmodified]

# Checkpoint 3 — QA-2, the chaos suite

**2026-08-04.** Against the shared worktree at `3bfbdb8c`
(`dev-/gotth-live-orchestrator-c3efc4`), in `dis-gotth-live:latest`
(image `e146d50d5de6`, Go 1.26.5, staticcheck 2025.1.1) on `node-b`.

The subject is PRD §6 "Phase 3 — Resilience", the eight minimum cases under
"QA-2 chaos suite green. **This is the gate.**", plus the three
equivalence-spec Appendix B measurements QA-2 owns — **QA3-1**, **QA3-2**,
**QA3-3** — and QA-1's **D-10**, which I was asked to verify rather than
believe.

The suite is `gotth-live/test/internal/chaos/`. It is the **server-and-wire**
half of resilience: the client runtime's own reconnect state machine landed
today in `80453b7c` and `client/test/reconnect.test.mjs` holds 35 specs for it,
so nothing here re-asserts backoff, jitter, visibility pausing or
terminal-versus-retried close codes. What is here is everything a browser
cannot decide alone.

---

## 1. Verdict

**PASS WITH TWO CONDITIONS.**

All eight PRD cases are implemented, all 36 specs are green under `-race`, and
every one of them was made to fail by mutating the library before it was
reported green (§6). Eight defects, **D-22** through **D-29**, all new, none of
them found by any existing suite.

**Two block checkpoint 3 on my authority:**

| | Defect | Why it blocks |
|---|---|---|
| **1** | **D-23 — three `Limits` fields are validated nowhere and kill every session at mount.** `HeartbeatInterval`, `MaxInboundFrameBytes` and `AckWindow` are copied verbatim into the mount `Snapshot`'s *refined* session parameters. Outside the protocol's ranges, `live.New` accepts the config and then every session on it dies at establishment with `Error{INTERNAL}` "the server could not encode an update", and the operator's log says the frame "was built by this library, so this is not a client problem" | This is D-14's defect class, in the same function that was extended to close it, three fields wider. It is a startup mistake that presents as a library bug at runtime, it is trivially reachable (`HeartbeatInterval: 500 * time.Millisecond` looks entirely reasonable), and it was the first thing this suite hit. **Owner: DEV-1** |
| **2** | **D-29 — a legitimate client refused by the resync budget freezes for about thirty seconds and is rescued only by the slow-client eviction.** The runtime latches the gap and clears it only on a Patch or Snapshot; a rate-limited resync is answered with `Error{RATE_LIMITED}` and *no render*, which is neither. The client then stops acknowledging, the outbound window fills, and RFC §7.4's eviction closes the connection after `slow_client_grace` — after which the reconnect recovers it | G9 is "survives bad networks". QA3-2 measures how often this reaches a real user: **20–25 % of legitimate resync requests are refused at 5–25 % patch loss** on a 53 Hz stream. The recovery exists, but it is the slow-client eviction doing a job nobody assigned it, and the user-visible cost of one refused resync is a stale page and a full re-mount. **Owner: DEV-2 (the re-arm) + PM-1/L9-1 (whether the eviction is the intended recovery path)** |

**Not blocking, recorded:** D-22, D-24, D-25, D-26, D-27, D-28. §5 states each
one's severity and owner, and each is held by a spec that goes red when the
behaviour changes, so none of them can quietly stop being true.

**On PRD case 8's second clause.** "Duplicate/replayed event frames → defined
semantics, no double state transition" is two requirements and this library
satisfies the first and not the second, *by design*. RFC §8.5 chooses
at-most-once, protocol.md **Q-P1** records that a fragment-scoped nonce is
therefore unnecessary, and
`test/internal/conformance/semantics_test.go` already asserts that a repeated
`client_ref` is two distinct events. Measured here: one `Event` frame's bytes
sent twice moved `state_version` 2 → 3 and ran the effect twice. **That is a
PM-1 decision, not a defect I am filing** — either the bullet loses its second
clause or Q-P1 is re-opened, and the second is a protocol change. §4.8.

**On D-10.** Closed, verified independently, and the closure is real. §3.

---

## 2. What I ran, and the host it ran on

Every number below is followed by the command that produced it. Nothing here is
quoted from another agent's report.

### 2.1 The suite

```bash
docker run --rm --cpuset-cpus=24-27 --memory=4g \
    -v "$PWD:/w" -w /w/gotth-live \
    -e GOFLAGS=-buildvcs=false -e GOTTHLIVE_SOAK=1 -e GOTTHLIVE_MEASURE=1 \
    dis-gotth-live:latest \
    bash -c 'go test -v -race -count=1 -timeout 35m ./test/internal/chaos/'
```

```
Ran 36 of 36 Specs in 400.864 seconds
SUCCESS! -- 36 Passed | 0 Failed | 0 Pending | 0 Skipped
```

Without the two environment variables — which is what CI runs — 30 specs run
and 6 are skipped: the 10,000-cycle churn, the SIGKILL restart, and the four
Appendix-B measurements.

### 2.2 Host state, and the contention label

`node-b`, 32 cores, and it is not an idle machine. `docker ps` at the start
of the measured runs listed 19 containers including **`gpu-desktop-steam-1` (up 5
days, healthy)**, the GPU streaming stack, plus `tenant-b-web-1`,
`tenant-c-web-1`, `tenant-e-web-1`, `tenant-h-chat-1` and others.

| when | `uptime` load average |
|---|---|
| before the first measured run | 9.87, 12.62, 11.54 |
| before the Appendix-B run | 6.12, 6.13, 7.97 |
| after the Appendix-B run | 6.41, 5.17, 6.91 |
| before the final full run | 2.59, 4.53, 5.77 |
| after the final full run | 1.74, 4.18, 5.45 |

**Every measured run in this document was taken on a contended host**, in a
container pinned to `--cpuset-cpus=24-27` with `--memory=4g`. That is a label,
not a disclaimer: per equivalence-spec §3.6 a contended run is publishable if it
says so, and an unlabelled one is not. The numbers that a reader should treat as
load-sensitive are called out where they appear — QA3-3's throughput delta is
the only one that moved materially between runs, and §7 gives both figures.

### 2.3 What did not run here

`ci.sh` was not run as part of this gate. It is DEV-3's file this round and its
`gofmt` and FR-7 steps were red on `examples/dashboard` when I started; I
confirmed that failure is not caused by any file of mine (`gofmt -l
./test/internal/chaos/` is empty, `go vet` and `staticcheck` are clean on the
package) and left it alone. The step text I want added is §9.

---

## 3. D-10 — verified, not believed

QA-1 left D-10 open as QA-2's Phase 3 item: *the leak test asserts goroutines
but not RSS*. `6f241373` claims to close it. I checked the claim rather than the
commit message.

`internal/wsx/wsx_test.go` now measures **two** signals after 10,000 clean
connect/disconnect cycles: `/gc/heap/live:bytes` after `debug.FreeOSMemory`, and
RSS from `/proc/self/statm`. Both budgets are derived in the source from stated
measurements, both are asserted, the RSS reader **fails rather than skips** when
`/proc` is absent, and the margins are published through `AddReportEntry` rather
than only asserted.

```bash
docker run --rm -v "$PWD:/w" -w /w/gotth-live -e GOFLAGS=-buildvcs=false \
    dis-gotth-live:latest bash -c 'go test -race -count=1 ./internal/wsx/'
```

Green. **D-10 is closed.** CP1-16 can be re-issued as met.

The one thing the closure does not cover, which is why case 7 exists as a
separate soak: those 10,000 cycles are all **clean** closes. §4.7.

---

## 4. The eight cases

### 4.1 Case 1 — connection dropped mid-patch → reconnect → resync → converges

**PASS.**

A TCP relay cuts both sockets with `SetLinger(0)` and no WebSocket close
handshake, in the middle of the patch stream, with effects still in flight.
Convergence is checked against a value the protocol did not produce: the
application's own ledger, rendered by the test rather than read off the wire.

```
40 interactions sent, 23 patched before the cut, 10 committed, 0 duplicated,
13 patched-but-never-committed (D-25), truth=10 and the reconnected Snapshot matched it
```

- The next session's `Snapshot` carries `server_seq = 1`, `OriginKind_MOUNT`,
  both supersession bounds zero — a reconnect is a new session, per RFC §8.1.
- `snapshotHTML(snapshot, "total")` equalled `renderTotal(ledger.total())`
  exactly.
- **0 duplicated application effects**, across every run.
- No commit that had settled before the cut was undone.
- A second, unrelated session on the same server kept being patched throughout.

The 13 patched-but-never-committed interactions are **D-25**, §5.

### 4.2 Case 2 — sequence gap → resync rather than out-of-order application

**PASS.**

The gap is injected by **loss**, not by a forged frame: H-9 makes the server
incapable of emitting a gap, so a server-produced one would be a different
defect. The client drops the third patch it receives and the fourth exposes the
gap.

```
gap at patch 3; superseded [4,5] of server_seq 6; 3 fragments re-rendered; truth=4
```

- Exactly one gap, exactly one `ResyncRequest{GAP}`.
- The answering `Snapshot` carries `OriginKind_RESYNC` with non-zero
  `event_id` and `client_ref` (H-6's second event-bearing arm) and
  `from <= through < server_seq` (H-13).
- All **three** registered fragments re-rendered, not only the one the missed
  patch carried.
- Markup equals the application's own truth.
- The applied sequence did not advance past the gap before the Snapshot.
- A resync describing **no** gap costs an `Ack` and no `Snapshot` (H-14's short
  circuit).

### 4.3 Case 3 — server restarted under load, within a stated bound

**PASS.**

The server is a **child process** and the restart is **SIGKILL**. Restarting the
`live.App` inside the test binary would have exercised `Mount` and `Snapshot`
and nothing else: the listener never closes, the accept queue never drains, no
socket is reset by the kernel, and the port is never rebound. Its ledger is a
file, because after a SIGKILL there is no in-process truth left to compare a
reconnected `Snapshot` against.

```
25 clients, SIGKILL restart; port rebound in 4ms;
slowest reconnect+resync 589ms over 1 attempts (bound 30s);
ledger 36 -> 294 distinct commits
```

**The stated bound is 30 s**, and it is derived rather than chosen: RFC §8.4
caps one backoff delay at 15 s, so 15 s of backoff plus the restart gap plus a
round trip is the worst case a healthy restart can produce. Measured worst case
across 25 clients: **589 ms**, one backoff attempt. The port was rebindable
4 ms after the kill.

The clients implement RFC §8.4's rule (full jitter, base 250 ms, cap 15 s)
themselves rather than being the shipped runtime, so that the client's
contribution to the recovery time is held at the documented values and what
remains in the number is the server's. That is stated as a limit of the case in
§8.

Three harness bugs found on the way, each of which would have made this case
pass for the wrong reason, and each now written where the next reader meets it:
`MaxSessionsPerIdentity` defaults to 20 so a 25-client fleet under one identity
silently loses five; `Event.seen_server_seq` is refined `this > 0` so an event
sent before the first `Snapshot` is refused at the parse boundary (which is why
`runtime.js` has `if (!seq) return`); and the mount `Snapshot` is consumed by
the dial, so a client that learns its sequence only from the read loop deadlocks
against itself.

### 4.4 Case 4 — slow client at a stated bandwidth

**PASS on FR-51. RFC §7.4's third stage does not fire — D-26.**

Stated bandwidth: **2,048 B/s** on the server→client direction, paced at a
byte-level TCP relay rather than by a client that reads slowly. That distinction
is what makes the case real — a client that does not call `Read` still drains
its kernel receive buffer, so the backpressure arrives late and in a lump.

```
2048 B/s throttle, 15s observed with grace 3 s: max window depth 16 of 16,
43 slow-client events, 302 coalesced patches, 30453 B delivered downstream (2030 B/s),
NOT evicted (D-26), other session still advancing, process still accepting
```

| FR-51 clause | verdict | the number |
|---|---|---|
| server queue bounded | **PASS** | `gotthlive_outbound_window_depth` peaked at exactly **16** of a configured 16 |
| server memory bounded | **PASS** | **169,792 B** of live heap retained over 15 s of continuous stalling, against a 4 MiB budget |
| other sessions unaffected | **PASS** | a second session's applied sequence kept advancing throughout, and a fresh connection was still served after |
| degraded **or** closed per a defined policy | **PASS (degraded)** | 43 synthesized `SlowClient` events, 302 coalesced patches |
| never the process | **PASS** | a new connection got `server_seq = 1` after the stall |

The heap figure is measured with the metric recorder **off**. With
`obstest.Metrics` attached the same run retained 7.9 MB, and essentially all of
it is the recorder retaining one observation per emission — a heap measurement
taken with it attached measures the harness. Both numbers are here so the next
reader does not have to rediscover that.

RFC §7.3's claim — "memory under backpressure is O(number of fragments), not
O(pending patches)" — holds, and 169,792 B over roughly 7,500 stalled updates
against three fragments is what holding looks like.

The eviction **is** reachable, and its bound is tight: with the client no longer
acknowledging at all,

```
2048 B/s throttle and no acknowledgements: closed with 4009 after 3.88s,
against a bound of 4s (grace 3 s + one 1 s tick)
```

Coalescing keeps its provenance:

```
CoalesceFlushAt=64, AckWindow=8, client never acknowledges:
flush fired with a union of 64, largest union over the run 64
```

No contributing list ever carried a zero identifier or a repeat, and none
exceeded H-4's ceiling.

### 4.5 Case 5 — event flood from a hostile client

**PASS on three clauses of four. The "defined close" is rate-dependent — D-24.**

- **Rate limit engages, typed error:** yes.
  `Error{RATE_LIMITED}` per refused frame, every time.
- **Defined close:** **reachable only above a rate.** The close needs
  `3 × EventBurst` *consecutive* denials and one allowed event resets the run,
  so it needs `3 × EventBurst` frames inside one token-refill interval — i.e. a
  flood faster than `3 × EventBurst × MaxEventsPerSecond`. At the documented
  defaults that is **15,000 frames/s**. Below it, the session is never closed:

  ```
  defaults (50/s, burst 100; close threshold 3x100x50 = 15,000 frames/s):
  10260 frames in 4.001s (2565 frames/s, 60x the limit), 9961 error frames back
  (9961 RATE_LIMITED), connection STILL OPEN
  ```

  A client flooding at **sixty times** the configured limit gets 9,961 typed
  errors and keeps its session. That is **D-24**. The close *is* reached when
  the flood outruns the refill, which the paired spec shows at a 75 frames/s
  threshold.
- **No unbounded allocation:** **PASS.** 2,254,384 B of live heap retained
  across the flood, against a 4 MiB budget — and the same figure appears
  whether 10,000 or 100,000 frames are sent, which is what "not per frame"
  looks like. Sixteen 1 MiB frames against a 4 KiB limit retained −3,544 B.

The mailbox bound refuses rather than blocks, with a typed error and no close.

Two findings ride along here: **D-22** (`gotthlive_sessions_active` reaches
**−50** after 50 rejected upgrades) and **D-28** (an oversize frame closes with
transport status **1009**, and the server records the close as `normal`).

### 4.6 Case 6 — network partition and half-open connection

**PASS. Reclamation is later than detection by the close handshake — D-27.**

The partition is a relay that keeps both sockets open, keeps **reading** from
both, and forwards nothing. That is the only construction that produces a
genuinely half-open connection: every write either side makes succeeds into a
live socket, nothing arrives, and no error is reported to anybody. A relay that
stopped reading instead would apply TCP backpressure and the server would see a
stalled write — a different failure, already covered by the write deadline.

```
half-open partition: session RECLAIMED after 7.942s, against a bound of 8.5s
(detection 3.5s = timeout 2.5s + interval 1s, plus a 5s close handshake
against a peer that cannot answer — D-27); goroutines 13 -> 8
```

- Detection is `HeartbeatTimeout + HeartbeatInterval`, because `onTick` is where
  the check lives and it runs on a ticker. **At the defaults that is 70 s.**
- Reclamation is one term further out. `Actor.Close` calls the transport's
  *graceful* close, which writes a close frame and waits for the peer to answer
  it; the peer of a half-open connection never will, so it runs to
  coder/websocket's five-second handshake timeout before the read pump returns
  and `Teardown` runs. **At the defaults reclamation is 75 s, not 70 s.** That
  is **D-27**.
- The close is recorded server-side as `heartbeat_timeout` in
  `gotthlive_connections_closed_total`. It cannot be read from the *client*, and
  the reason is the fault itself: a partition drops the close frame with
  everything else, so the client's socket ends with no WebSocket status at all.
- Goroutines returned to baseline; a second, healthy session kept being served
  throughout.
- Separately verified: an idle session with a heartbeat-healthy peer is evicted
  on `IdleTimeout` with **4011 SESSION_EVICTED** (FR-22).

### 4.7 Case 7 — 10,000-cycle churn, no goroutine/timer/heap leak

**PASS.**

`internal/wsx` already soaks 10,000 **clean** disconnects (§3). This soaks
10,000 **abnormal** ones: aborted with `CloseNow`, no close handshake, and half
of them aborted **while a transition is in flight**, so the actor is mid-render
when its connection dies. Those are different exit paths through
`Actor.shutdown`, and a resource that leaks on one of them leaks nowhere the
existing soak looks.

```
   300 abrupt cycles:   213ms wall; goroutines 7 -> 7;  live heap retained    880 B (2.9 B/cycle) against 281,344 B
10,000 abrupt cycles:  7.205s wall; goroutines 7 -> 7;  live heap retained   -272 B (-0.0 B/cycle) against 902,144 B
```

Three hundred connections aborted mid-handshake, before any upgrade completed,
left no goroutines and mounted no sessions.

**On the timer half of FR-22's sentence.** `runtime/metrics` exports no timer
count, so a leaked `*time.Ticker` is invisible *as a timer*. What it is not
invisible as is retained heap: the runtime timer holds the ticker and the ticker
holds its channel. The live-heap budget is therefore what stands behind "no
timer leak", and this is stated in the source rather than left for somebody to
assume a check exists that does not.

### 4.8 Case 8 — duplicate and replayed frames

**Defined semantics: PASS. "No double state transition": FAILS AS WRITTEN, by
design.**

```
one frame sent twice: state_version 2 -> 3, effect ran 2 times for ref 4242.
Defined semantics: YES (RFC §8.5, protocol.md Q-P1). No double state transition: NO.
```

The defined semantics is RFC §8.5's at-most-once plus protocol.md **Q-P1**,
which records that a fragment-scoped nonce is unnecessary *because* delivery is
at-most-once. A byte-identical `Event` frame sent twice is therefore two events,
which `semantics_test.go` already asserts as intended behaviour. The spec here
is a characterisation: it asserts the documented result and goes red the day
somebody adds deduplication without moving Q-P1.

**The PRD bullet's second clause is not satisfiable without a protocol change.**
Routed to PM-1: either the clause is amended, or Q-P1 re-opens and `Event` gains
a nonce. I am not filing it as a defect because both halves of the library are
doing what their own documents say.

The replay properties that **are** defences all hold:

| replayed frame | behaviour | verdict |
|---|---|---|
| an `Event` captured from **another** session | refused, connection closed **4002** (H-3); the victim's session untouched and the reducer never reached | PASS |
| the same `Ack`, three times | no-op; the session survives | PASS |
| an `Ack` that goes **backwards** | closed **4002** (H-7) | PASS |
| `ClientTelemetry` for a patch that has left the window, and for one that never existed | dropped and counted; connection survives (H-11) | PASS |
| 20 replayed `ResyncRequest`s | **3** snapshots, 9 `RATE_LIMITED` errors, closed **4008** (H-14) | PASS |

---

## 5. Defects

Numbering continues from D-21. Every one of these is held by a spec that goes
red when the behaviour changes — including the ones that assert current
behaviour, which are written so that *fixing* them turns the spec red and sends
the next reader to this section.

### D-22 — MEDIUM — `gotthlive_sessions_active` counts down on rejected handshakes

**Owner: DEV-1. Not merge-blocking.**

`wsx.Handler.ServeHTTP` calls `Metrics.ConnectionClosed` on the origin,
authentication and CSRF rejection paths. `Metrics.ConnectionOpened` is called
only from `Actor.mount`, which those paths never reach. `ConnectionClosed`
unconditionally does `sessionsActive.Add(ctx, -1)`, so every rejected upgrade
decrements a gauge that was never incremented.

```
50 rejected upgrades: gotthlive_sessions_active = -50,
gotthlive_connections_total = 0, gotthlive_connections_closed_total = 50
```

FR-34 lists the per-connection metric set as a Phase 3 gate. An operator
alerting on live sessions is alerting on a number that goes arbitrarily negative
under exactly the hostile traffic FR-51 is about. `gotthlive_connections_total`
minus `gotthlive_connections_closed_total` is wrong for the same reason.

The rejection paths should either not touch the gauge, or count into a separate
`gotthlive_handshakes_rejected_total{reason}`.

### D-23 — HIGH — three `Limits` fields are unvalidated and kill every session at mount

**Owner: DEV-1. MERGE-BLOCKING.**

`live.Limits.validate()` checks for negatives and `CoalesceFlushAt`'s ceiling.
Three more fields are copied verbatim into the mount `Snapshot`'s refined
session parameters:

| field | protocol predicate (protocol.md §3.3) |
|---|---|
| `HeartbeatInterval` → `heartbeat_interval_ms` | `this >= 1000 && this <= 300000` |
| `MaxInboundFrameBytes` → `max_inbound_frame_bytes` | `this >= 1024 && this <= 1048576` |
| `AckWindow` → `ack_window` | `this >= 1 && this <= 256` |

Outside any of those, `live.New` returns no error and **every session on that
configuration dies at establishment**:

```
the first frame was an Error rather than a Snapshot (H-10):
INTERNAL: the server could not encode an update
```

and the operator's log says *"refused to send a frame this library could not
validate: the frame was built by this library, so this is not a client
problem"* — which sends them to the wrong repository for a value they set
themselves.

This is D-14's defect class in the same function that was extended to close it.
`live/config.go`'s own comment reads *"Until D-14 this function inspected no
Limits field at all, which is how a `CoalesceFlushAt` above the protocol ceiling
reached the actor and turned the flush trigger into an emission failure."* Three
fields still do.

It blocks because `HeartbeatInterval: 500 * time.Millisecond` is a reasonable
thing for an operator to write, the failure is total, and the diagnosis points
away from the cause. It was the first thing this suite hit, at 300 ms.

Five table entries assert it. `live`'s own suite walks `Limits` by reflection
setting each numeric field **negative** — the range check is what nothing walks.

### D-24 — MEDIUM — FR-51's "defined close" is reachable only above 300× the rate limit

**Owner: DEV-1. Not merge-blocking.**

`Actor.ingressEvent` closes after `consecutiveDenialsBeforeClose × EventBurst`
= `3 × EventBurst` **consecutive** denials, and `a.eventDenied = 0` on every
allowed event. Tokens arrive at `MaxEventsPerSecond`, so the close needs
`3 × EventBurst` frames inside one refill interval — a flood rate above
`3 × EventBurst × MaxEventsPerSecond`, which at the documented defaults is
**15,000 frames/s**.

```
defaults (50/s, burst 100): 10260 frames in 4.001s (2565 frames/s, 60x the limit),
9961 error frames back (9961 RATE_LIMITED), connection STILL OPEN
```

FR-51 says exceeding a limit "MUST produce a typed error **and a defined
close**". A client at sixty times the limit gets the error and no close, for as
long as it cares to keep going. It is not a byte-amplification vector — the
error frames are smaller than the events that provoke them — but it is a
per-frame response the server is obliged to render and write, indefinitely, to a
client it has already decided is abusive.

The threshold is an emergent product of two constants that were each chosen for
another reason. A denial *rate* rather than a consecutive-denial *count* would
make the close reachable at any flood rate.

### D-25 — LOW — RFC §8.5 documents one direction of the at-most-once leak

**Owner: PM-1 + DEV-1 (RFC-0001 §8.5). Not merge-blocking. Documentation.**

RFC §8.5 states: *"An effect that already committed externally **stays
committed**, even though its patch never reached the client."* The opposite also
happens and is not stated: an effect's patch reaches the client and the effect
is then cancelled by the disconnect, so the browser showed a state the server
never committed.

```
40 interactions sent, 23 patched before the cut, 10 committed,
13 patched-but-never-committed
```

Resync corrects it — case 1's convergence assertion is the proof — so this is
eventual consistency working, not a correctness failure. It is a
*documentation* gap: §8.5 names the leak in the direction where the user sees
less than happened, and is silent about the direction where the user briefly
sees more. One sentence.

### D-26 — MEDIUM — RFC §7.4's eviction stage cannot fire against a client that acknowledges

**Owner: DEV-1 (implementation) + L9-1/PM-1 (whether the policy is what was
intended). Not merge-blocking.**

`window.noteFullness` clears `fullSince` the moment depth drops below the bound,
and `Actor.onAck` calls it on every acknowledgement. A client that acknowledges
at **any** nonzero rate therefore restarts the grace clock before it can expire.

```
2048 B/s throttle, 15s observed with grace 3 s: max window depth 16 of 16,
43 slow-client events, 302 coalesced patches, NOT evicted
```

versus the same client with acknowledgements switched off entirely:

```
closed with 4009 after 3.88s
```

RFC §7.4's third row reads *"window continuously full for `slow_client_grace` =
30 s"* → close `4009`. The implementation matches those words exactly; the words
describe a condition the canonical slow client does not meet. FR-51's own
disjunction — "degraded **or** closed" — is satisfied by the degrade stage, so
the requirement holds and this does not block. What does not hold is the
sentence in RFC §7.4 that says a slow client is eventually evicted.

The practical consequence is that a session pinned at a full window is held
open indefinitely, at the cost of its window, mailbox and actor, as long as it
keeps trickling acknowledgements. Whether that is the right answer is a design
question — it *is* making progress — but it should be the answer the RFC gives.

### D-27 — LOW — reclamation is five seconds later than detection

**Owner: DEV-1. Not merge-blocking.**

FR-12: *"Both sides MUST detect a dead peer within a configurable bound … The
server MUST reclaim session resources **on detection**."* Detection and
reclamation are not the same instant. `Actor.Close` → `conn.close` →
`ws.Close(...)` is coder/websocket's **graceful** close: it writes a close frame
and waits for the peer to answer. A half-open peer by definition never answers,
so it runs to the five-second close-handshake timeout before the read pump
returns, the actor's context cancels and `Teardown` runs.

```
half-open partition: session RECLAIMED after 7.942s
(detection 3.5s = timeout 2.5s + interval 1s, plus a 5s close handshake)
```

**At the defaults the reclamation bound is 50 + 20 + 5 = 75 s, not 70 s.** The
five seconds are not configurable through `live.Limits`. A close whose code is
`4010 HEARTBEAT_TIMEOUT` is by construction a close to a peer that cannot
answer, so `CloseNow` is available there at no cost to any correct client.

### D-28 — MEDIUM — close code 4007 `FRAME_TOO_LARGE` is unreachable

**Owner: DEV-1. Not merge-blocking.**

protocol.md §8.3 enumerates `4007 FRAME_TOO_LARGE` for H-5, and FR-8 says
"closed for unknown reason is a bug". The read limit is enforced by
`ws.SetReadLimit` — the transport's, applied before any library code sees the
frame — so coder/websocket closes with RFC 6455 status **1009**.
`conn.noteReadError` then returns early because `websocket.CloseStatus(err)` is
not `-1`, `finalCode()` falls back to `CloseNormal`, and the metric records the
close as `normal`.

```
16 × 1 MiB against a 4 KiB limit: client told close 1009
(protocol.md §8.3 enumerates 4007 for this),
server recorded gotthlive_connections_closed_total{code} = [normal]
```

So the client is told a status that is not in the protocol's enumeration, the
operator's dashboard is told `normal`, and `4007` is dead code. protocol.md
§8.3's own sentence — *"a test enumerates every `Close(` call site in the
library and asserts each names a constant from this table"* — passes, because
this close is not a `Close(` call site in the library at all.

Note that the *belt-and-braces* re-check after decode does produce `4007`: with
the transport limit removed the library's own rejection path runs and the client
sees `4007`, which is how this defect's falsifier works. It is unreachable
because the transport wins the race, not because it is unimplemented.

### D-29 — HIGH — a refused resync freezes the page until the slow-client eviction rescues it

**Owner: DEV-2 (the client re-arm) + PM-1/L9-1 (the intended recovery path).
MERGE-BLOCKING.**

One line of state on each side.

`client/runtime.js` latches the gap — `function resync() { if (gap || !seq)
return; gap = true; … }` — and clears it only in `applied()`, which runs on a
`Patch` or a `Snapshot`. The server answers a rate-limited resync with
`Error{RATE_LIMITED}` and **no render** (RFC §7.6), which is neither, and
`runtime.js`'s `f.error` branch dispatches a DOM `CustomEvent` and touches no
state.

So the latch stays set. Every subsequent patch fails the `server_seq === seq +
1` test and is discarded by the same early return, and the client stops
acknowledging as a consequence of having stopped applying. That last consequence
is what ends it, and it ends it by accident: with no acknowledgements the
outbound window fills, stays full, and RFC §7.4's eviction closes the connection
with `4009` after `slow_client_grace`, after which the client reconnects and
recovers.

The user-visible cost of one refused resync is therefore

```
(time for the outbound window to fill) + slow_client_grace
```

of a frozen page followed by a full re-mount — **about thirty seconds at the
defaults**, for a refusal RFC §7.6 describes as costing "no render".

```
one refused resync with grace at 5s: applied sequence frozen at 5,
resync requests frozen at 2, page stale for 6.002s, then closed 4009
```

It blocks because G9 is "survives bad networks" and QA3-2 measures the arrival
rate at a real workload: **20–25 % of legitimate resync requests refused at
5–25 % patch loss** on a 53 Hz stream, at an average request rate of only
0.2–0.5/s — the refusals come from losses *clustering* inside the one-second
minimum interval, not from the client asking too often.

Two independent fixes exist and either is sufficient: the client re-arms on
`Error{RATE_LIMITED}` and retries after a delay, or the server tells it when to
(a retry-after on the error). Both are small. What should not stand is the
current arrangement, in which the recovery is the slow-client eviction doing a
job nobody assigned it and which does not fire at all if the server has nothing
further to send.

---

## 6. Mutation evidence — every case was made to fail first

This project's recurring defect class is a check that cannot fail, caught four
separate times. So before any case was reported green, the library was mutated
so that the property the case asserts is false, and the case was watched going
red.

Every mutation was applied to a **pristine `git archive HEAD` export** under
`/tmp/qa2-mut/`, never to the shared worktree. Seventeen were run; sixteen
applied and all sixteen reddened their target. The seventeenth failed to apply
against the wrong file and was re-aimed and re-run.

```bash
# per mutation, from the repository root
rm -rf /tmp/qa2-mut/pristine && mkdir -p /tmp/qa2-mut/pristine
git archive HEAD | tar -x -C /tmp/qa2-mut/pristine
cp -a /tmp/qa2-mut/pristine /tmp/qa2-mut/<name>
( cd /tmp/qa2-mut/<name>/gotth-live && python3 <mutation>.py )
docker run --rm -v "/tmp/qa2-mut/<name>:/w" -w /w/gotth-live \
    -e GOFLAGS=-buildvcs=false -e GOTTHLIVE_SOAK=1 -e GOTTHLIVE_MEASURE=1 \
    dis-gotth-live:latest \
    bash -c 'go test -count=1 ./test/internal/chaos/ -args "-ginkgo.focus=<case>" -ginkgo.fail-on-empty'
```

| # | mutation | went red |
|---|---|---|
| M1 | `Actor.renderPass`: a snapshot renders only dirty fragments, not all | case 2 "superseding exactly the missed range" |
| M2 | `Actor.resync`: the no-op short circuit becomes unconditional | case 2 ×2 (the resync Snapshot, and the out-of-order guard) |
| M3 | `window.full()` returns false | case 4 ×3 (the queue bound, the eviction, the coalescing flush) |
| M4 | `ingressEvent`: the inbound bucket always allows | case 5 ×2 (the reachable close, and D-24) |
| M5 | `Actor.onTick`: the heartbeat-timeout close is deleted | case 6 ×2 (the partition, and the bystander) |
| M6 | `Handler.deregister`: the registry entry is never removed | case 7 ×2 (300 cycles and the 10,000-cycle soak) |
| M7 | `protocol.CheckSessionID` always returns nil | case 8 "refuses a frame replayed onto a different session (H-3)" |
| M8 | `window.ack`: the backwards-acknowledgement check is disabled | case 8 "closes on an acknowledgement that goes backwards (H-7)" |
| M9 | `Metrics.ConnectionClosed`: the `sessionsActive` decrement is removed | **D-22's spec** |
| M10 | `Actor.emitPatch`: `mustFlush` is always false | case 4 "flushes at the configured trigger rather than truncating" |
| M11 | `Limits.validate` gains the three range checks D-23 is about | **D-23's five table entries** |
| M12 | `Actor.resync`: a rate-limited request falls through and renders anyway | **D-29's spec** |
| M13 | `runEffects` executes each effect twice | case 1 "with no duplicated effect" |
| M14 | `ingressEvent`: one denial is enough to close | **D-24's spec** |
| M15 | `conn.serve`: the transport read limit is removed, so the library's own oversize rejection runs and 4007 reaches the client | **D-28's spec** |
| M16 | `conn.close`: `CloseNow` instead of the graceful close | case 6 ×2, including **D-27's "reclamation is later than detection"** |
| M17 | `Actor.renderPass`: a snapshot renders from a state that is not the session's | case 1 ×2 (convergence, and the bystander) |

M9, M11, M12, M14, M15 and M16 are the ones that matter most for this report's
honesty: five of the eight defects are recorded by specs that assert the
**current, wrong** behaviour, and a spec like that is worthless unless *fixing*
the defect turns it red. Each of those five was fixed in a mutant and each spec
went red, with a failure message that names the defect and says the spec should
now be rewritten as the requirement.

**The worktree was not modified.** `git status --porcelain` before and after the
mutation runs shows only other agents' untracked directories
(`bench/apps/chat/next/src/`, `bench/fixtures/`, `docs/guide/`) and nothing of
mine missing.

### 6.1 One spec that could not fail, found and fixed

The D-24 spec originally sent 100,000 frames as fast as the socket allowed. That
measured **10,124 frames/s** on a loaded host and crossed the 15,000 frames/s
close threshold on an idle one, so the characterisation flipped with the
machine — a check whose result is the hardware's. It now paces at a stated
**3,000 frames/s**, sixty times the configured limit and a fifth of the
threshold, and asserts the measured send rate stayed below the threshold before
it asserts anything about the close. Recorded here rather than quietly fixed,
because it is the same defect class this section exists to prevent.

---

## 7. The Appendix-B measurements

All three were taken. Command:

```bash
docker run --rm --cpuset-cpus=24-27 --memory=4g \
    -v "$PWD:/w" -w /w/gotth-live \
    -e GOFLAGS=-buildvcs=false -e GOTTHLIVE_MEASURE=1 \
    dis-gotth-live:latest \
    bash -c 'go test -v -count=1 -timeout 25m ./test/internal/chaos/ \
        -args "-ginkgo.label-filter=measure" -ginkgo.fail-on-empty'
```

Workload throughout: equivalence-spec §2.4's dashboard rate, **53 logical
updates/s**, server-initiated through an effect that emits into the session's own
mailbox. Two independent runs; the QA3-1 and QA3-2 tables below were identical
to within one frame across them.

### 7.1 QA3-1 — is `coalesce_flush_at = 512` the right value?

Client held behind by never acknowledging, 30 s window, `AckWindow = 16`.

| `CoalesceFlushAt` | frames/s (all kinds) | patches | flushes | flushes/s | largest union | **measured margin below H-4's 1024** |
|---:|---:|---:|---:|---:|---:|---:|
| 64 | 1.53 | 39 | 24 | 0.800 | 64 | **960** |
| 128 | 1.10 | 27 | 12 | 0.400 | 128 | **896** |
| 256 | 0.90 | 21 | 6 | 0.200 | 256 | **768** |
| **512 (default)** | **0.83** | **18** | **3** | **0.100** | **512** | **512** |
| 959 (`MaxCoalesceFlushAt`) | 0.77 | 16 | 1 | 0.033 | 959 | **65** |

**The answer: 512 is defensible and there is no measured reason to move it.**

- The flush rate is exactly `53 / flushAt` — the arithmetic RFC §7.4 predicts,
  confirmed to three significant figures across a 15× range of the parameter.
- The cost of the whole legal range is **under one extra frame per second**:
  0.033 flushes/s at 959, 0.800 at 64. Whatever else the trigger is, it is not
  expensive. RFC §7.4's "the cost of the flush is one extra frame against a
  client that is already behind" is correct, and at the default it is one extra
  frame every ten seconds.
- The margin at 512 is **512 identifiers**, measured. It is not the "half of
  H-4's ceiling" that the RFC's prose implies as a safety fraction — it is the
  exact distance, because the trigger counts the frame it is about to build and
  the union on the wire equalled the trigger exactly in every cell.
- At 959 the measured margin is **65**, which is precisely
  `MaxCoalesceFlushAt`'s own arithmetic (`1 + MaxEventContributing`) and
  therefore has *no* slack left for an application that fills
  `Event.Contributing`. The workload here contributes one identifier per event;
  an application contributing the legal maximum of 64 would put the 959 cell
  exactly on the ceiling. **512 has two-thirds of the ceiling in hand under any
  legal application behaviour; 959 has none.** That is the argument for 512 that
  the RFC did not have.

**A finding that changes what QA3-1 is about.** RFC §7.4 justifies the flush
trigger by saying *"at the dashboard workload (53 updates/s) with
`slow_client_grace` at 30 s, a single session can accumulate ~1,590 contributing
events before eviction"*. Measured at that exact workload:

```
53 updates/s for 30s against a client that never acknowledges:
0.73 frames/s total, 15 patches, largest contributing union 1, 0 flushes
```

**The union does not accumulate at all.** `deferPatch` folds the deferred
origin's own `event_id` and its contributing edges into the pending set; an
effect emission's origin carries `event_id = 0` — the origin *source* names its
cause instead — and its contributing list is exactly the one `scheduledBy` edge
the library adds, which is the same identifier on every emission and
deduplicates to a set of size one. So a purely server-initiated stream, which is
what FR-62's dashboard *is*, can never reach the trigger no matter how long the
client stalls.

The 1,590 figure describes a **client-event-driven** workload, or one whose
application sets `Event.Contributing` per emission — which is what the sweep
above had to do to make the trigger fire at all. RFC §7.4's justifying sentence
names the wrong workload. It does not change the value of the parameter and it
is not a defect in the code, but the number in the RFC is not measurable at the
workload the RFC attaches it to, and it should say so. **Owner: DEV-1 + L9-1
(RFC-0001 §7.4).**

**What this measurement is not.** DSH-8's 4× CPU throttle and §5.7's mobile
profile were not used: the client is held behind by withholding
acknowledgements, which reaches the same window state by a faster and more
deterministic route but does not reproduce a browser's own apply cost. §8.

### 7.2 QA3-2 — the rate at which a *legitimate* client is rate-limited

`MinResyncInterval = 1 s`, `ResyncBurst = 3`. The client behaves exactly as
FR-11 requires: one request per gap, latched until the answering `Snapshot`,
which is what `runtime.js` does. So every request it makes is legitimate and
every refusal is a false positive by construction.

20 s windows at 53 updates/s, loss applied to inbound sequenced frames:

| patch loss | patches seen | resync requests | request rate | answered | **refused** | **refusal rate** |
|---:|---:|---:|---:|---:|---:|---:|
| 1 % | 1059 | 10 | 0.50/s | 10 | 0 | **0 %** |
| 5 % | 115 | 5 | 0.25/s | 4 | 1 | **20 %** |
| 10 % | 55 | 4 | 0.20/s | 3 | 1 | **25 %** |
| 25 % | 31 | 4 | 0.20/s | 3 | 1 | **25 %** |

**The deciding number is 20–25 %, and the reason it is not zero is clustering,
not rate.** The average request rate never exceeded 0.5/s against a configured
minimum interval of 1 s — a fifth to a half of the budget. The refusals happen
because two losses land inside the same second after the burst is spent, which
is exactly how loss behaves on a real link.

Note the second column. At 1 % loss the client sees 1,059 patches in 20 s; at
25 % it sees 31. The client is not receiving a degraded stream, it is receiving
almost none of it — because it spends most of the window latched, waiting for a
resync it either has not been granted or has been refused. That is D-29, and
QA3-2 is how often D-29's trigger is pulled.

**Recommendation, for PM-1 and L9-1 rather than a change I am making.** The
amplification bound that `MinResyncInterval` exists to enforce is preserved by a
much smaller interval, because the expensive path is already short-circuited
when there is no gap (H-14) and the resync bucket is already independent of the
event bucket. If D-29 is fixed by having the client re-arm and retry, the
refusal rate stops mattering; if it is not, then 1 s / 3 refuses a quarter of
the recovery attempts of a client on a 5 % lossy link.

### 7.3 QA3-3 — provenance-log volume

instrumentation.md §4A.2/§4A.4 estimates **≈200 B/record** and hence
**≈10.6 KB/s/session** at the dashboard workload. Measured, counting exactly the
bytes `slog`'s JSON handler writes for records carrying
`logger=gotthlive.provenance` and nothing else:

```
53 updates/s for 20s: 1061 provenance records, 393,261 B total,
370.7 B/record, 19,663 B/s/session (19.66 KB/s)
```

| | estimate | **measured** | ratio |
|---|---:|---:|---:|
| per record | ≈200 B | **370.7 B** | **1.85×** |
| per session per second | ≈10.6 KB/s | **19.66 KB/s** | **1.85×** |
| at D3's active-heavy N = 1000 | ≈10.6 MB/s | **≈19.7 MB/s** | **1.85×** |

**The estimate is low by a factor of 1.85, uniformly.** The record count is
exactly the transition count (1,061 records for a 20 s window at 53/s), so the
discrepancy is entirely per-record size: a provenance row carries a session id,
seven causal identifiers, an origin kind and source, a fragment-id list and a
contributing list, and JSON key names and quoting are most of the difference
from a 200 B estimate.

That matters where Appendix B says it matters. Equivalence-spec **T-5** treats
19.7 MB/s out of a single container as a host-contention source at D3's
N = 1000, and the figure is now measured rather than assumed. Whether the log
path's buffers are also a line inside `M(x)` is not settled by this measurement
and is flagged in §8.

**The log path's share of the default-on-vs-off delta.** Throughput of
server-initiated transitions over a 12 s window, transitions counted from the
rendered value rather than from frames (coalescing collapses many transitions
into one patch, so a patch count measures the window and not the reducer):

| configuration | transitions | per second | cost vs `Logger` nil | per transition |
|---|---:|---:|---:|---:|
| provenance **off** (`Logger` nil) | 1,494,214 | 124,518/s | — | 8.03 µs |
| provenance on, discarding text handler | 856,594 | 71,383/s | **−42.7 %** | 14.01 µs (**+5.98 µs**) |
| provenance on, JSON to a counting sink | 190,140 | 15,845/s | **−87.3 %** | 63.11 µs (**+55.1 µs**) |

Read that as a **ceiling**, not as a per-request tax:

- At the dashboard's 53 updates/s per session, the provenance record costs
  **0.29 ms of CPU per second per session — 0.03 %**, which is two orders of
  magnitude inside NFR-1's ≤ 5 % budget. **The log path is not an NFR-1
  problem at realistic rates.**
- What the percentages describe is the *maximum transition rate a process can
  sustain*: 124,518/s with provenance off, **15,845/s with a JSON handler
  attached**. At D3's N = 1000 × 53/s = 53,000 transitions/s, provenance JSON
  encoding alone is **≈2.9 CPU-seconds per second — about three cores** spent on
  the log. That is the number the "sampled-in-production / full-in-soak" question
  in instrumentation **I6** should be decided against, and it is now a
  measurement.
- §4A.3 constrains the answer: G4 depends on the log being unsampled. This
  measurement does not settle I6; it supplies the figure I6 was waiting for.
  **The PM-1 half remains PM-1's.**

**Load sensitivity, labelled.** These three figures are the only numbers in this
document that moved materially between runs. Under `-race` and with the rest of
the suite in the same process the same measurement gave 10,311/s, 7,349/s and
6,840/s — the race detector's instrumentation dominates and the *ratio*
collapses, because it taxes everything equally. The table above is the unraced,
pinned, four-core run and is the one to quote; the raced figures are recorded
here so nobody re-derives a different answer from `ci.sh`'s output and thinks one
of us is wrong.

---

## 8. What I did not measure, and why

1. **DSH-8's 4× CPU throttle and §5.7's mobile network profile, for QA3-1.**
   Appendix B specifies the client be held behind by a browser CPU throttle and
   a mobile profile. I held it behind by withholding acknowledgements, which
   reaches the same window state deterministically and in seconds. What that
   does not reproduce is the browser's own morph and apply cost, which changes
   *when* the window fills and not *what happens when it does* — and the flush
   trigger is a function of the union's size, not of the reason the client is
   behind. I believe the substitution is sound for this parameter and I am
   naming it rather than assuming nobody would ask. Re-taking it under CDP would
   need `Emulation.setCPUThrottlingRate` and a network profile in the bench
   image, which is a browser harness this suite does not have.

2. **The DOM half of cases 1 and 2, in a real browser.** Convergence is asserted
   on the `Snapshot`'s markup against the application's own truth, not on a
   rendered DOM. The morph path from that markup to the DOM is
   `test/internal/conformance`'s browser suite and DEV-2's `morph.test.mjs`, and
   duplicating it here would be a second copy of somebody else's claim. What is
   *not* covered anywhere as a result: an end-to-end run in Chromium where a
   real gap is injected on the wire and the real runtime's DOM is inspected
   after the resync. That would need a WebSocket-message-level proxy in the
   browser harness. **Recommended for checkpoint 4; not done here.**

3. **The provenance log's contribution to `M(x)`.** QA3-3 measures bytes
   produced and CPU spent. Whether the log path's buffers land inside the G2
   memory figure is a question about the *handler an operator configures*, not
   about this library — a `slog` handler with a 4 MB buffered writer puts 4 MB in
   `M(x)` and a synchronous one puts nothing. I have no basis for choosing a
   handler on an operator's behalf, so I measured the volume and left the
   memory question to equivalence-spec §3.6's own configuration.

4. **Whether D-26's non-eviction is *wrong*.** I measured that RFC §7.4's third
   stage does not fire against a client that acknowledges, and that FR-51 is
   satisfied anyway by the degrade stage. Whether a session pinned at a full
   window should be evicted or held is a design decision with an argument on
   both sides, and it is not mine to make.

5. **TLS.** Every measurement here is over plaintext loopback. Nothing in this
   suite is a memory or byte figure that equivalence-spec §3.6's TLS-boundary
   rule applies to, so this is a statement of scope rather than a gap.

6. **`ci.sh` end to end.** §2.3.

7. **The 1,590-contributing-event figure at a client-event-driven workload.**
   §7.1 shows the union does not accumulate on a server-initiated stream and
   measures the trigger against an application that contributes one identifier
   per emission. I did not construct the client-event-driven workload RFC §7.4's
   figure actually describes — 53 *clicks* per second per session for 30 s is
   not a workload any of the three examples produces, and building one to
   validate a sentence in an RFC seemed the wrong order of operations. The
   sentence should be corrected to name the workload it means; the parameter is
   measured either way.

---

## 9. The `ci.sh` step I want added

**I have not edited `ci.sh`** — it is DEV-3's file this round. This is the step
text, in the shape of the existing `test/routers` step, for the orchestrator to
add and verify.

It belongs **after** the `tests, race detector (NFR-12)` step and before
`examples/counter`, because it is part of the main module and runs under the
same `go test` context.

```bash
# The checkpoint-3 chaos suite: PRD Phase 3's eight minimum cases, which are
# the gate that checkpoint 3 is. It runs inside `go test -race ./...` above as
# well — it is in this module, deliberately, because unlike test/routers and
# test/sampling it needs no dependency the library must not carry. So why a
# step of its own?
#
# Because six of its specs do not run there. Two are soak-class and four are
# Appendix-B measurements, and both classes gate on an environment variable so
# that a plain `go test ./...` stays seconds rather than minutes. A suite whose
# most expensive half is invisible to CI is the same defect one label out, so
# this step turns both on and names what it costs: about seven minutes.
#
# -ginkgo.fail-on-empty because a label filter matching nothing is a silent
# pass, which this repository has now caught four times.
step "chaos suite, soak and measurements (PRD Phase 3, the checkpoint-3 gate)"
if GOTTHLIVE_SOAK=1 GOTTHLIVE_MEASURE=1 go test -race -count=1 -timeout 35m \
    ./test/internal/chaos/ -args -ginkgo.fail-on-empty; then
  echo "clean"
else
  failures+=("chaos suite (PRD Phase 3 cases 1-8, QA3-1/2/3)")
fi
```

Verification that it is not vacuous, for whoever adds it: with
`GOTTHLIVE_SOAK` and `GOTTHLIVE_MEASURE` set, the suite reports `Ran 36 of 36
Specs`; without them, `Ran 30 of 36 Specs … 6 Skipped`. If the step ever prints
30, the environment variables are not reaching it.

**Reproducing everything in this document:**

```bash
# the whole suite, as the proposed CI step runs it
docker run --rm -v "$PWD:/w" -w /w/gotth-live \
    -e GOFLAGS=-buildvcs=false -e GOTTHLIVE_SOAK=1 -e GOTTHLIVE_MEASURE=1 \
    dis-gotth-live:latest \
    bash -c 'go test -v -race -count=1 -timeout 35m ./test/internal/chaos/'

# the Appendix-B measurements alone, pinned, as §7's numbers were taken
docker run --rm --cpuset-cpus=24-27 --memory=4g -v "$PWD:/w" -w /w/gotth-live \
    -e GOFLAGS=-buildvcs=false -e GOTTHLIVE_MEASURE=1 \
    dis-gotth-live:latest \
    bash -c 'go test -v -count=1 -timeout 25m ./test/internal/chaos/ \
        -args "-ginkgo.label-filter=measure" -ginkgo.fail-on-empty'

# one case
docker run --rm -v "$PWD:/w" -w /w/gotth-live -e GOFLAGS=-buildvcs=false \
    dis-gotth-live:latest \
    bash -c 'go test -v -count=1 ./test/internal/chaos/ -args "-ginkgo.focus=PRD case 4"'

# D-10, verified rather than believed
docker run --rm -v "$PWD:/w" -w /w/gotth-live -e GOFLAGS=-buildvcs=false \
    dis-gotth-live:latest bash -c 'go test -race -count=1 ./internal/wsx/'
```

---

## 10. PRD Phase 3 exit criteria, as they stand

| criterion | verdict |
|---|---|
| Case 1 — dropped mid-patch → reconnect → resync → converges, no duplicated or lost effect | **MET.** 0 duplicated; convergence against out-of-protocol truth. D-25 is a documentation gap in RFC §8.5, not a failure of this box |
| Case 2 — gap → resync rather than out-of-order (FR-11) | **MET** |
| Case 3 — server restarted under load, within a stated bound | **MET.** SIGKILL of a real process; bound 30 s, measured 589 ms |
| Case 4 — slow client at a stated bandwidth (FR-51) | **MET.** 2,048 B/s; queue at 16 of 16, heap 169,792 B, others unaffected, degraded, process alive. D-26 is against RFC §7.4's third row, not against FR-51 |
| Case 5 — event flood (FR-51) | **MET on three clauses of four.** D-24 is the "defined close" clause, and it is rate-dependent |
| Case 6 — partition and half-open (FR-12) | **MET.** Detection 3.5 s of a 3.5 s bound; reclamation 7.94 s, which is D-27 |
| Case 7 — 10k churn, no goroutine/timer/heap leak (FR-22) | **MET.** −0.0 B/cycle over 10,000 *abnormal* cycles, on top of D-10's clean-close soak |
| Case 8 — duplicate/replayed frames | **Defined semantics MET. "No double state transition" NOT MET, by design.** PM-1's call, §4.8 |
| Batching/debounce demonstrated, coalesced patch names every contributing event | **MET** by case 4 and QA3-1; the flush fires at the configured trigger and carries its whole union |
| Backpressure metrics exported (FR-34) | **MET for the queue set** — `gotthlive_outbound_window_depth`, `_patches_coalesced_total`, `_slow_client_events_total` all observed carrying real values. **D-22 is a defect in the connection set** |
| Live dashboard example (FR-62) | **DEV-3's, not assessed here** |
| Resync cost measured for the dashboard example | **Not this suite's.** QA3-1 and QA3-3 measure adjacent things; the dashboard resync byte/latency figure is DEV-3 + the FR-62 box |
| Client runtime ≤ 12 KB gzipped | **Not assessed here** — `tools/minify -check`, DEV-2 |
| G2 baseline exists, RFC §6.2 corrected | **Not this suite's.** D-10, the half that was mine, is closed and verified — §3 |

---

## 11. Verdict line

**QA-2 does not clear checkpoint 3 while D-23 and D-29 are open.** Every one of
the eight cases exists, runs, and can fail; six of them are green outright, case
5 is green on three clauses of four with D-24 named, and case 8's second clause
is a PRD/protocol conflict for PM-1 rather than a defect. The two blockers are
each a small, well-localised change with an owner: three range checks in
`live.Limits.validate` (DEV-1), and a re-arm on `Error{RATE_LIMITED}` in the
client runtime or a retry-after on the server's side of it (DEV-2). Neither
needs a design round. When both land and this suite is green against them, I
will re-issue this as a clear pass.

---
---

# Re-verification — 2026-08-04, against `ce52d2f9`

Everything above this line is the original report and is unchanged. This
section is the re-issue §11 promised, written after both blockers landed.

**2026-08-04, later the same day.** Against a **clean export**, not the shared
worktree: `git archive ce52d2f9559de915f0938218b0cc82961cc4883a | tar -x -C
/tmp/cp3-verify`. Same image as the original report — `dis-gotth-live:latest`,
`e146d50d5de6`, Go 1.26.5, staticcheck 2025.1.1 — on `node-b`.

The export is why the commit named here is `ce52d2f9` and not whatever `HEAD`
says when you read this. Two other agents were editing the same worktree
throughout, and one of them was changing library source under `live/` and
`internal/`; a verdict taken against a tree that moves while it is being
measured names nothing. `git rev-parse HEAD` was `b1641f4e` by the time this
section was written, two commits further on, and §R8 says which of those two
commits changes an answer here and how.

**The subject:** the two conditions §1 held checkpoint 3 on.

| | landed in | re-verified in |
|---|---|---|
| **D-23** — three `Limits` fields unvalidated, every session dead at mount | `8b428390` (the check), `7533a1bb` (this suite's table inverted) | §R3 |
| **D-29** — a refused resync freezes the page ~30 s | `c3a91af8` (the client re-arm) | §R4 |

Also landed and used here: `c11045a9`, which put the suite's expensive half into
`ci.sh` — §9's request, verbatim, in the place §9 asked for it — and `5179ff29`,
which closed both CI intermittents.

---

## R1. Verdict

**PASS. No conditions.**

Both blockers are closed, verified rather than believed, and the closures are
real:

- **D-23.** The three ranges are validated at `live.New`, they are the
  schema's own ranges compared verbatim against `frame.proto` and
  `docs/protocol.md` rather than re-invented, and the failure mode — accepted
  config, every session dead at establishment, an operator log blaming the
  library — is unreachable from `live.New`. §R3.
- **D-29.** The re-arm reaches the wire and **the server serves it**. The
  ~30 s freeze is now a **2.462 s / 2.515 s** longest stall over two independent runs, on the
  same server under the same fault, the recovery is the client's own retry, and RFC §7.4's
  eviction is back to being the last resort rather than the recovery. §R4.

**What this clears:** PRD §6's "QA-2 chaos suite green. **This is the gate.**"
All eight cases are MET as of this run — case 8's second clause was struck by
PM-1 on the strength of §4.8's escalation and replaced by a positive
requirement the existing spec already asserts (§R8). The suite is 40 of 40
green under `-race` with both cost classes on. It does **not** clear
checkpoint 3, which is the gate report's to write and needs L9-1 and the rows
in §R8 marked "not this suite's" as well as mine.

**Three new defects, none blocking:** **D-30** (HIGH, DEV-1), **D-31** (LOW,
PM-1 + DEV-2 + L9-1), and a correction to §5's own blanket claim about D-25
(§R5). §R6 states each one's severity, owner, and why it does not block where
D-23 did.

**Honesty note on scope.** I re-ran everything I quote. Nothing in this section
is carried over from the original report's numbers without a fresh run, and the
places where a re-run disagrees with the original are called out rather than
smoothed — §R5's D-25 row is the sharpest of them.

---

## R2. What I ran

### R2.1 The whole suite, as `ci.sh` runs it

```bash
rm -rf /tmp/cp3-verify && mkdir -p /tmp/cp3-verify
git archive ce52d2f9559de915f0938218b0cc82961cc4883a | tar -x -C /tmp/cp3-verify

docker run --rm --cpuset-cpus=24-27 --memory=4g \
    -v /tmp/cp3-verify:/w -w /w/gotth-live \
    -e GOFLAGS=-buildvcs=false -e GOTTHLIVE_SOAK=1 -e GOTTHLIVE_MEASURE=1 \
    dis-gotth-live:latest \
    bash -c 'go test -v -race -count=1 -timeout 35m ./test/internal/chaos/ \
        -args -ginkgo.fail-on-empty'
```

```
Will run 36 of 36 specs
Ran 36 of 36 Specs in 399.491 seconds
SUCCESS! -- 36 Passed | 0 Failed | 0 Pending | 0 Skipped
ok  github.com/candacelabs/candace/pkg/gotth/test/internal/chaos  400.565s
EXIT=0
```

`-ginkgo.fail-on-empty` was accepted and `Will run 36 of 36` is the line that
says the two environment variables reached the process — the check §9 asked
whoever added the CI step to make. **The step landed verbatim**, `ci.sh:141-161`,
after `tests, race detector (NFR-12)` and before `examples/counter`, which is
where §9 asked for it. I did not edit `ci.sh`; I read it.

That run is the one every re-verified number in §R5 comes from.

### R2.2 The Appendix-B measurements, unraced and pinned

§7's headline figures were taken without the race detector, because §7.3
records that `-race` collapses the provenance ratio by taxing everything
equally. Re-taken the same way:

```bash
docker run --rm --cpuset-cpus=24-27 --memory=4g -v /tmp/cp3-verify:/w \
    -w /w/gotth-live -e GOFLAGS=-buildvcs=false -e GOTTHLIVE_MEASURE=1 \
    dis-gotth-live:latest \
    bash -c 'go test -v -count=1 -timeout 25m ./test/internal/chaos/ \
        -args "-ginkgo.label-filter=measure" -ginkgo.fail-on-empty'
```

```
Ran 4 of 40 Specs in 318.017 seconds
SUCCESS! -- 4 Passed | 0 Failed | 0 Pending | 36 Skipped
```

### R2.3 The suite with this section's four new specs

40 specs, same invocation as R2.1. The result line is in §R7 with the mutation
evidence, because a spec added without a falsifier is not evidence of anything.

### R2.4 Host state, and the contention label again

`node-b`, 32 cores, 19-plus containers including `gpu-desktop-steam-1`, and
**more loaded than the original report's runs, not less**:

| when | `uptime` load average |
|---|---|
| before the export and the full run | 7.48, 6.36, 7.03 |
| during the new specs' first run | 15.60, 9.34, 7.63 |
| before the unraced measurement run | 6.18, 7.80, 7.22 |

Every measurement below is from a container pinned to `--cpuset-cpus=24-27`
with `--memory=4g` on a contended host. Per equivalence-spec §3.6 that is
publishable because it says so. The load-sensitive figures are §R5's throughput
row, and both the raced and unraced values are given there for the same reason
§7.3 gave both.

---

## R3. D-23 — verified, not believed, and then attacked

### R3.1 The ranges are the schema's, not new ones

The concern §1 named was a closure that invents its own numbers. It does not.
`internal/protocol/sessionparams.go` names each range once, carries the
predicate's source text with it, and `internal/protocol/sessionparams_test.go`
holds every range against the **generated** validator — accepted at `Min` and
`Max`, refused one past either end, `Field` and `Predicate` compared verbatim.

Compared by hand, three ways, at `ce52d2f9`:

| field | `proto/gotthlive/v1/frame.proto` | `docs/protocol.md` §3.3 | `internal/protocol` |
|---|---|---|---|
| `heartbeat_interval_ms` | `this >= 1000 && this <= 300000` (l. 218) | same (l. 219) | `Min 1000, Max 300000` |
| `max_inbound_frame_bytes` | `this >= 1024 && this <= 1048576` (l. 221) | same (l. 220) | `Min 1024, Max 1048576` |
| `ack_window` | `this >= 1 && this <= 256` (l. 223) | same (l. 221) | `Min 1, Max 256` |

Identical in all three places, and identical to the table §5's D-23 entry wrote
them into. **No new range was invented.**

### R3.2 The failure mode is gone

`live.New` now returns a `*ConfigError` naming the field, and the five entries
this suite carried were inverted by `7533a1bb` to assert exactly that — the
instruction the original table carried for this moment, executed. Green in
R2.1's run.

The closure is wider than the three entries. `live/snapshotparams_test.go` adds
eleven rejection entries (both endpoints of each field, a sub-millisecond
duration that narrows to zero, an order-of-magnitude overshoot), a spec that
refuses a value which is only in range once truncated to `uint32`, two specs on
the error's wording and its authority, nine acceptance entries that **mount a
real session at each endpoint and read the parameter back off the `Snapshot`**,
and a reflection property that mounts every configuration `New` accepts. That
last one is the part that closes the *class* rather than the three instances,
and it is the reason §R6's new defect had to be looked for somewhere else.

### R3.3 The adversarial case: valid field by field, incoherent together

Everything in R3.2 validates the three fields **one at a time**. Nothing
validates them against the fields they only make sense next to. One such pair
reproduces D-23's whole failure mode from values every one of those checks
admits, and it is reachable from the value D-23's own error message
recommends. That is **D-30**, §R6.1.

---

## R4. D-29 — the re-arm verified, and what one refused resync costs now

### R4.1 QA3-2 re-measured: the refusal rate did not move, and should not have

The refusal rate is a property of the **server's** budget, and `c3a91af8` is a
client change. Re-measured on the fixed tree, it reproduces §7.2 to the frame:

| patch loss | patches seen | requests | request rate | answered | refused | refusal rate |
|---:|---:|---:|---:|---:|---:|---:|
| 1 % | 1059 | 10 | 0.50/s | 10 | 0 | **0 %** |
| 5 % | 115 | 5 | 0.25/s | 4 | 1 | **20 %** |
| 10 % | 55 | 4 | 0.20/s | 3 | 1 | **25 %** |
| 25 % | 31 | 4 | 0.20/s | 3 | 1 | **25 %** |

Identical to §7.2's table in every cell. **20–25 % of legitimate resync requests
are still refused at 5–25 % patch loss**, and that is the correct outcome: the
fix did not make the refusals rarer, it made them cheap. §7.2's own
recommendation — that `MinResyncInterval` could be smaller — is therefore
**withdrawn as urgent**. Its own sentence said so: *"If D-29 is fixed by having
the client re-arm and retry, the refusal rate stops mattering."* It is, and it
does.

One thing this table is *not* any more, and the correction matters. QA3-2's
client is the **pre-fix** runtime model, deliberately: it latches and never
re-arms. So the second column — 1,059 patches at 1 % loss against 31 at 25 % —
still measures a client spending the window latched. On the fixed runtime it
would not. QA3-2 is now a measurement of the server's refusal rate and nothing
more, and I have said so in the spec rather than leaving the second column to be
misread.

### R4.2 Does the page still freeze for thirty seconds? No: about 2.5 seconds

The re-arm is in `client/runtime.js` and DEV-2's `client/test/resync.test.mjs`
holds fourteen specs for it — the schedule, the one-in-flight rule, the jitter
band, the acks, the interaction with visibility and with the reconnect. I did
not re-assert any of that; re-implementing somebody's client in Go and checking
it against itself proves nothing.

What no JS harness can answer is whether the **real server** admits that
schedule. Three separate pieces of this module have to cooperate for the fix to
work: the resync bucket has to grant the request the schedule puts in front of
it, the consecutive-denial counter has to not close `4008` on the retries, and
RFC §7.4's slow-client eviction has to not fire anyway. So the harness client
gained an opt-in transcription of `refused()`/`ask()` and of the patch branch's
ack, and the fixed client's behaviour was put in front of the real server:

```
run 1: defaults (MinResyncInterval 1s, ResyncBurst 3), 50% patch loss at 25 updates/s,
slow_client_grace 5s, observed 15.016s after the first refusal:
15 refusals, 14 armed retries, applied sequence advanced by 196 (11 -> 207),
18 resync Snapshots, longest stall 2.462s, closed=false code=-1

run 2 (independent, inside the 40-of-40 run of §R7.1):
15 refusals, 15 armed retries, applied sequence advanced by 221 (11 -> 232),
19 resync Snapshots, longest stall 2.515s, closed=false code=-1
```

against the **pre-fix client on the same server**, which is the control spec
still in `case2_gap_test.go` and still green in the same run:

```
one refused resync with grace at 5s: applied sequence frozen at 5,
resync requests frozen at 2, page stale for 6.002s, then closed 4009
```

| | pre-fix | **post-fix** |
|---|---|---|
| requests after a refusal | 0 | **14 retries** |
| applied sequence after the first refusal | frozen | **+196** |
| longest interval with no applied frame | to the eviction (6.002 s at a 5 s grace; ~30 s at the default) | **2.462 s / 2.515 s** over two runs |
| how it ends | closed `4009`, full re-mount | **it does not end; the session is still live** |

**Answer to §1's question: no.** One refused resync now costs a stall on the
order of the retry schedule's first two draws — `[500, 1000)` ms then
`[1000, 2000)` ms against a bucket refilling a token a second — and the page
recovers **on the same connection**, with no re-mount.

### R4.3 Is the slow-client eviction still doing a job nobody assigned it?

**No, and the safety net is still there.** The recovery path is now the client's
own retry, served by the resync bucket. RFC §7.4's eviction is what it was
always supposed to be: the last resort for a client that cannot be served at
all.

That is not an argument, it is the MR1 mutant. With the server's resync bucket
mutated to grant nothing, the **fixed** client is still evicted:

```
MR1 (a.resyncBucket.allow -> never): 4 refusals, 3 armed retries,
applied sequence advanced by 0 (2 -> 2), longest stall 6.9s, closed=true code=4009
```

So a client that retries and is refused for ever still ends up where D-29 said
it ended up — which is correct, because at that point it genuinely cannot be
served, and that is the case RFC §7.4's third row is about. The fix removed the
eviction from the *ordinary* path without removing it from the *last-resort*
path. Worth stating explicitly because the obvious way to write this fix — have
the client stop acknowledging differently, or have the server stop counting —
would have removed both.

The acks the fixed client sends while latched do not weaken it either.
`window.noteFullness` clears `fullSince` only when the window is **not** full,
and an ack at the sequence the client already holds does not drain the window,
so a genuinely stuck session still accumulates its grace. This is the one
interaction between the D-29 fix and **D-26** that could have made D-26 worse,
and it does not: verified by reading `internal/session/window.go:113-122` and
`actor.go:913-921`, and demonstrated by MR1's `4009`.

### R4.4 Does RFC §7.4 still describe reality? Yes — and RFC §7.6 no longer does

§7.4 is fine. Its third row describes the eviction, the eviction still does
exactly that, and D-26 (the eviction cannot fire against a client that
acknowledges into a *draining* window) is unchanged and still open, §R5.

**RFC §7.6 is now behind the code.** Line 800 of `docs/rfc/001-architecture.md`
still reads that a `ResyncRequest` arriving sooner than `MinResyncInterval` is
answered with `Error{RATE_LIMITED}` and **no render**, full stop. That is still
true of the frame and no longer true of the *interaction*: the client now
retries on a schedule, and the schedule is a protocol-visible behaviour that
exists only in `client/runtime.js`'s comments and `c3a91af8`'s commit body.
RFC §8.4 makes it worse by documenting **full jitter** as *the* client schedule
while there is now a second one, deliberately different — equal jitter, because
a refused resync has no herd to spread and a delay near zero is precisely the
request the server just declined. That reasoning is good and it is written
nowhere a protocol reader will find it. **D-31**, §R6.2.

### R4.5 What it cost

`client/SIZE.md` §1.1.3 records the re-arm at **+223 gzipped bytes**, and the
runtime totals **4,360 bytes gzipped against NFR-2's 12,288** ceiling. Read,
not re-measured — the ledger and the gate are DEV-2's, and §R8 marks that row
accordingly.

---

## R5. The six non-blocking defects, re-checked

§5 claimed that every one of these "is held by a spec that goes red when the
behaviour changes". I re-ran all six and checked the claim rather than the
sentence. **Five of six hold. The sixth does not, and the correction is mine.**

| | defect | severity | owner | re-run at `ce52d2f9` | status |
|---|---|---|---|---|---|
| **D-22** | `gotthlive_sessions_active` counts down on rejected handshakes | MEDIUM | DEV-1 | `50 rejected upgrades: gotthlive_sessions_active = -50, connections_total = 0, connections_closed_total = 50` | **OPEN, held by an assertion.** Unchanged |
| **D-24** | FR-51's "defined close" reachable only above 300× the rate limit | MEDIUM | DEV-1 | `defaults (50/s, burst 100; threshold 15,000 f/s): 10050 frames in 4.001s (2512 f/s, 60x the limit), 9751 RATE_LIMITED back, connection STILL OPEN` | **OPEN, held by an assertion.** The paced-send fix from §6.1 held: 2,512 f/s measured, a sixth of the threshold, and the spec asserts its own premise before its conclusion |
| **D-25** | RFC §8.5 documents one direction of the at-most-once leak | LOW | PM-1 + DEV-1 | `40 interactions sent, 10 patched before the cut, 10 committed, 0 duplicated, 0 patched-but-never-committed` | **OPEN, and NOT held by an assertion — see below** |
| **D-26** | RFC §7.4's eviction cannot fire against a client that acknowledges | MEDIUM | DEV-1 + L9-1/PM-1 | `2048 B/s, 15s observed with grace 3s: max window depth 16 of 16, 41 slow-client events, 302 coalesced patches, NOT evicted`; and with acks off, `closed with 4009 after 3.88s` | **OPEN, held by an assertion.** Both arms reproduce |
| **D-27** | reclamation is five seconds later than detection | LOW | DEV-1 | `half-open partition: session RECLAIMED after 7.944s, against a bound of 8.5s` (§4.6 measured 7.942 s) | **OPEN, held by an assertion** |
| **D-28** | close code `4007 FRAME_TOO_LARGE` is unreachable | MEDIUM | DEV-1 | `16 × 1 MiB against a 4 KiB limit: client told close 1009, server recorded gotthlive_connections_closed_total{code} = [normal]` | **OPEN, held by an assertion** |

None of the six has been fixed, none has regressed further, and all six specs
**ran** in R2.1's 36 — no label moved, nothing was skipped, and the
`fail-on-empty` guard was on. That was §5's real worry and it is satisfied.

### R5.1 The correction: D-25 is held by a printed number, not by a check

`case1_drop_test.go` computes `patchedButNotCommitted` and puts it in an
`AddReportEntry`. It is never asserted. The spec's assertions are that nothing
already committed was undone and that no effect ran twice — both correct, both
green, and neither of them about D-25.

In this run the number was **0**, where §4.1 and §5 recorded **13**. The
phenomenon is a race between the cut and the effect executor and it simply did
not occur; nothing noticed, because nothing was watching.

So §5's blanket sentence is wrong about one of its six, and I am correcting it
rather than quietly leaving it: **D-25's evidence is an observation, not a
check, and the "13" in the original report is not reproducible on demand.**

I am **not** adding an assertion. `len(patchedButNotCommitted) > 0` is a timing
race, and a flaky assertion is worse than an honest observation — it is this
project's own recurring defect class wearing the opposite mask. The right fix is
the one D-25 always asked for: **one sentence in RFC §8.5**, after which the
report entry becomes a curiosity rather than the only record. The defect keeps
its LOW severity and its PM-1 + DEV-1 owner, and it now also carries a note that
its number is opportunistic.

### R5.2 The Appendix-B numbers, re-measured

| | §7's figure | re-measured at `ce52d2f9` |
|---|---|---|
| QA3-1, flush rate at each `CoalesceFlushAt` | `53 / flushAt`, margins 960/896/768/512/65 | identical in every cell, margins 960/896/768/512/65 |
| QA3-1, server-initiated stream | `largest contributing union 1, 0 flushes` | `0.73 frames/s, 15 patches, largest contributing union 1, 0 flushes` — identical |
| QA3-2 | 0 / 20 / 25 / 25 % | identical (§R4.1) |
| QA3-3, per record | 370.7 B | **370.6 B** |
| QA3-3, per session per second | 19,663 B/s | **19,625 B/s** |
| QA3-3, transitions/s, provenance off / text / JSON (**unraced, pinned**) | 124,518 / 71,383 / 15,845 | **128,595 / 70,281 / 13,183** |
| QA3-3, the same three **under `-race`** | 10,311 / 7,349 / 6,840 | **10,420 / 7,911 / 6,983** |

§7.3's conclusions survive: the estimate is low by **1.85×** uniformly (370.6 /
200), and the on/off delta is −45.3 % for a discarding text handler and −89.7 %
for JSON to a counting sink, against §7.3's −42.7 % and −87.3 %. Same order, same
argument, and the difference is the host — which is why §7.3 labelled that row
load-sensitive and why both the raced and unraced values are printed here too.

---

## R6. New defects

Numbering continues from D-29.

### D-30 — HIGH — `HeartbeatInterval` and `HeartbeatTimeout` are each valid and fatal together

**Owner: DEV-1. Not merge-blocking — see "why this does not block" below.**

D-23's closure validates each of the three wire-carried fields against the
protocol's own refinement, one field at a time, and it validates them
thoroughly. Nothing validates any of them against the fields they only make
sense next to.

`internal/session/actor.go`'s `onTick` evaluates the liveness deadline and
writes the heartbeat that would reset it, **in that order, on the same tick**:

```go
if since := now.Sub(time.Unix(0, a.lastInboundNS.Load())); since > a.lim.HeartbeatTimeout {
    a.Close(protocol.CloseHeartbeatTimeout, "no frame from the client within the heartbeat timeout")
    return
}
...
frame := protocol.NewHeartbeat(a.peer.ID, a.hbNonce, uint32(a.lim.HeartbeatInterval/time.Millisecond))
```

and `internal/wsx/conn.go:79` drives it from
`time.NewTicker(h.opts.Limits.HeartbeatInterval)`. So the deadline is sampled on
a period of `HeartbeatInterval`, and when `HeartbeatInterval >=
HeartbeatTimeout` **no client can satisfy it**: the first tick finds
`since ≈ HeartbeatInterval > HeartbeatTimeout` and closes before sending the
solicitation the client would have echoed.

Measured, with both values inside every range D-23 checks:

```
HeartbeatInterval=2s, HeartbeatTimeout=1s (both accepted by live.New):
a client echoing every heartbeat was closed 4010 after 0 heartbeats.
```

Zero heartbeats is the mechanism, asserted rather than described.

**It is reachable from the value D-23's own error message recommends.** Set
`HeartbeatInterval` out of range and the library now says *"set it to between 1s
and 5m0s, or leave it zero for the default of 20s"*. An operator who takes the
top of that range and leaves `HeartbeatTimeout` alone gets:

```
live.New accepts HeartbeatInterval=5m0s (the ceiling its own D-23 message names)
with HeartbeatTimeout at its default of 50s. onTick runs on a ticker of
HeartbeatInterval and evaluates the timeout before it sends the heartbeat, so
the deadline is 4m10s past due on the first tick and no client can ever satisfy it.
```

It is reachable far below the ceiling too: `HeartbeatInterval: time.Minute`
against the default fifty-second timeout is an entirely ordinary thing to write.

The user-visible result is that every session with no other inbound traffic is
closed `4010 HEARTBEAT_TIMEOUT` on a `HeartbeatInterval` cycle, for ever, and
reconnects, and is closed again. `4010` is not in the client's terminal set, so
the page keeps working — through a permanent re-mount loop — and the operator's
`gotthlive_connections_closed_total{code="heartbeat_timeout"}` fills with
healthy clients while the close reason says *"no frame from the client within
the heartbeat timeout"*. That is D-23's diagnosis problem exactly: the config is
the cause and the evidence points at the peer.

**Why this does not block where D-23 did.** Three reasons, and they are the
honest ones rather than a preference for a clean verdict. The defaults (20 s
against 50 s) are coherent and correct, so an application that never mentions
`Limits` is unaffected, where D-23 was reachable at the first value an operator
would try. There is a recovery — the reconnect — where D-23's sessions died at
establishment with nothing behind them. And PRD Phase 3's gate is the eight
cases, all of which are MET. It is nonetheless **HIGH**, because the failure is
total for the sessions it touches and the diagnosis is actively misleading, and
because the fix is one comparison in the function that has now been extended
twice for this exact class.

**Suggested shape**, since the two previous instances of this class both took
it: refuse at construction in `Limits.validate`, name both fields, say which
ordering is required and why, and give the defaults. `HeartbeatTimeout` should
probably be required to be at least two intervals, so that a client gets one
solicitation it can lose.

Held by three specs in `test/internal/chaos/reverify_test.go` — the arithmetic
at the documented values with no clock in it, the timed observation, and a
control on the coherent ordering. Falsifiers in §R7.

### D-31 — LOW — the resync retry schedule is protocol-visible and documented nowhere a protocol reader looks

**Owner: PM-1 + L9-1 (RFC-0001 §7.6 and §8.4) + DEV-2 (the source of truth).
Not merge-blocking. Documentation.**

`c3a91af8` gave the client a second retry schedule: equal jitter,
`bound/2 + random(0, bound/2)` over `bound = min(15 s, 1000 ms × 2^n)`, one
request in flight per gap, the delay at least doubling per refusal. It is
correct and the reasoning behind choosing equal jitter over §8.4's full jitter
is good — a refused resync has no herd to spread, the bucket is per session, and
a delay near zero is precisely the request the server just declined.

That reasoning lives in `client/runtime.js`'s comments and in the commit body.
The two documents a protocol reader would consult say something else:

- **RFC §7.6** (line 800) still describes a request over budget as answered with
  `Error{RATE_LIMITED}` and **no render**, and stops. A reader implementing a
  second client from that table builds the pre-fix client and re-creates D-29.
- **RFC §8.4** (line 874) documents **full jitter** as *the* client schedule.
  There are now two, deliberately different, and nothing says so.

One paragraph in §7.6 and one sentence in §8.4. It is LOW because both halves of
the shipped system are correct and consistent; it is real because D-29 was a
defect that came from exactly this — one side's behaviour not being written
where the other side's author would read it.

### D-25's evidence — a correction, not a new defect

§R5.1. Recorded here so it is not lost between the two sections.

---

## R7. Mutation evidence for the specs added here

§6's rule applies to this section too: nothing added here was reported green
before it was made to fail. Same method — a pristine
`git archive ce52d2f9` export per mutation under `/tmp/qa2-remut/`, with only
this suite's own files overlaid, **never the shared worktree**.

```bash
rm -rf /tmp/qa2-remut/<name>
cp -a /tmp/qa2-remut/pristine /tmp/qa2-remut/<name>
python3 -c '<mutation>'
docker run --rm --cpuset-cpus=24-27 --memory=4g \
    -v "/tmp/qa2-remut/<name>:/w" -w /w/gotth-live -e GOFLAGS=-buildvcs=false \
    dis-gotth-live:latest \
    bash -c 'go test -count=1 ./test/internal/chaos/ -args "-ginkgo.focus=<case>" -ginkgo.fail-on-empty'
```

| # | mutation | went red |
|---|---|---|
| **MR1** | `Actor.resync`: the resync bucket grants nothing | D-29 re-verified — `applied sequence advanced by 0 (2 -> 2), longest stall 6.9s, closed=true code=4009`, which is the pre-fix signature reproduced exactly |
| **MR2** | `Actor.resync`: one denial reaches the `4008` close instead of `3 × ResyncBurst` | D-29 re-verified — the session was gone in 0.4 s, before the armed retry's first draw could fire, so it failed on the retry assertion rather than on the close one. Recorded as it happened |
| **MR3** | `Limits.validate` gains the `HeartbeatInterval >= HeartbeatTimeout` check — i.e. **D-30 fixed** | both D-30 specs, on the `ConfigError`. The coherent-ordering control stayed **green**, which is the second thing MR3 shows: the fix does not refuse the correct pairing |
| **MR4** | `Actor.onTick`: the heartbeat-timeout close is deleted | D-30's timed spec — `the session survived` |
| **MR5** | `wsx.conn`: the ticker period becomes an hour, so `onTick` never runs at the configured period | D-30's timed spec **and** its control — the control fails on its own premise, `no heartbeat was sent in three seconds at a one-second interval` |

Every one of the four specs added here has at least one falsifier, and the two
that assert **current, wrong** behaviour — D-30's pair — are reddened by MR3,
which is the mutation that *fixes* the defect. That is the property §6 exists to
enforce: a spec recording a defect is worthless unless closing the defect turns
it red and sends the next reader here.

**The worktree was not modified by any of this.** Every mutation ran in
`/tmp/qa2-remut/`; `git status --porcelain` over my file area was empty before
and after.

### R7.1 The suite with the four new specs

Same invocation as R2.1, against the export with this section's files in it:

```
Will run 40 of 40 specs
Ran 40 of 40 Specs in 419.895 seconds
SUCCESS! -- 40 Passed | 0 Failed | 0 Pending | 0 Skipped
EXIT=0
```

Twice, independently — 420.229 s and 419.895 s — because the first of the two
was compiled before §R7.2's prose edit to `case8_replay_test.go` landed, and a
run whose binary is not the files being committed is not evidence about them.
The second is the one this section reports. Both were 40 of 40.

### R7.2 Two specs re-worded rather than re-asserted

Both are in this suite's own files and neither changes an assertion.

- **`case2_gap_test.go`'s D-29 spec** now says in its own header that its client
  is the **pre-fix** runtime, kept deliberately as the control §R4.2 measures
  against. Its failure messages used to read "so D-29 is fixed", which was true
  when written and is now misleading: what they mean is "this harness client has
  stopped being a control". A reader who met that spec cold would otherwise have
  concluded D-29 was still open.
- **`case8_replay_test.go`** was written as a *characterisation* of behaviour the
  PRD asked not to have. PM-1's ruling inverted that (§R8), and the spec's
  assertions did not have to change by a line to become the requirement's:
  it already required the state version to advance and the effect to have run
  twice. Only the prose and the failure message moved.

---

## R8. PRD Phase 3 exit criteria, as they stand today

Rows that are not this suite's stay marked. Where I can see something about one
from here, it is stated as an observation with where I read it, not as an
assessment.

| criterion | verdict |
|---|---|
| Case 1 — dropped mid-patch → reconnect → resync → converges, no duplicated or lost effect | **MET.** Re-run green; 0 duplicated, convergence against out-of-protocol truth. D-25's *number* did not reproduce this run (§R5.1); the criterion does not depend on it |
| Case 2 — gap → resync rather than out-of-order (FR-11) | **MET.** Re-run green: `gap at patch 3; superseded [4,5] of server_seq 6; 3 fragments re-rendered; truth=4` |
| Case 3 — server restarted under load, within a stated bound | **MET.** SIGKILL of a real child process; bound 30 s, re-measured **615 ms** over one backoff attempt, port rebound in 6 ms |
| Case 4 — slow client at a stated bandwidth (FR-51) | **MET.** 2,048 B/s; window at 16 of 16, heap 169,536 B of a 4 MiB budget, others unaffected, degraded, process alive. **D-26** is against RFC §7.4's third row, not against FR-51, and is unchanged |
| Case 5 — event flood (FR-51) | **MET on three clauses of four.** **D-24** is the "defined close" clause and is rate-dependent; re-measured at 2,512 frames/s, 60× the limit, still open |
| Case 6 — partition and half-open (FR-12) | **MET.** Detection 3.5 s of a 3.5 s bound; reclamation **7.944 s**, which is **D-27** |
| Case 7 — 10k churn, no goroutine/timer/heap leak (FR-22) | **MET.** 10,000 *abnormal* cycles: goroutines 7 → 7, **1.2 B/cycle** against a 902,144 B budget, on top of D-10's clean-close soak |
| Case 8 — duplicate/replayed frames | **MET, both clauses as they now read.** The second clause was **struck by PM-1 on 2026-08-04** (PRD §9 v0.6 row 1; `docs/pm/checkpoint-3-scope.md` §1; protocol.md **Q-P1**'s closing note) in answer to §4.8's escalation, and replaced by a positive requirement: *two frames MUST produce two transitions and run the effect twice, and the library MUST NOT deduplicate.* The spec that measured the old conflict asserts the new requirement unchanged, and is green. **This is no longer a decision anyone owes.** PM-1 named the one trigger that re-opens it — an outbound retry queue in `client/runtime.js`, which would make a duplicate frame something the library itself could emit — and that trigger is recorded in the spec |
| Batching/debounce demonstrated, coalesced patch names every contributing event | **MET.** Case 4 and QA3-1 re-run: the flush fires at the configured trigger and carries its whole union, margins 960/896/768/512/65 across the legal range |
| Backpressure metrics exported (FR-34) | **MET for the queue set** — `gotthlive_outbound_window_depth`, `_patches_coalesced_total`, `_slow_client_events_total` all observed carrying real values. **D-22 is still a defect in the connection set** |
| Live dashboard example (FR-62) | **Not this suite's, owner DEV-3.** Observed from here: `examples/dashboard/` exists as its own module with `view.templ`, `feed.go`, `metrics.go`, `wire.go`, a Ginkgo suite and a `FRICTION.md`. I did not build or run it and make no claim about the plain-HTMX region |
| Resync cost measured for the dashboard example | **Not this suite's, owner DEV-3.** Observed: `examples/dashboard/resync.go` implements a `-resync-cost` flag and `README.md` §"The resync cost" publishes `min 97µs p50 163µs p90 259µs max 1.309ms (n=200)` with bytes cross-checked against the library's own `gotthlive_resync_bytes`. I read that page; I did not re-run it, so it is DEV-3's number and not mine |
| Client runtime ≤ 12 KB gzipped | **Not this suite's, owner DEV-2.** Observed: `client/SIZE.md` records **4,360 bytes** `gzip -9` against the 12,288 ceiling, and §1.1.3 attributes **+223** of it to the D-29 re-arm. Read from the ledger, not re-measured — the tool is `tools/minify -check`. **One number to reconcile before the gate report quotes it:** `docs/qa/ci-intermittents.md`'s `fe9b6772` gate-run line reads *"the NFR-2/NFR-3 size gate (10,178 B against a 12,288 B ceiling, 64.5% headroom)"*, and those two figures are not the same measurement — 10,178 B is the **minified** size and 64.5 % headroom is **4,360**'s (`1 - 4360/12288`). The gate itself is gzip and is met either way. Flagged rather than edited: that file is not mine |
| G2 baseline exists, RFC §6.2 corrected | **Not this suite's, owner DEV-1.** Observed: `docs/bench/g2-baseline.md` publishes **82,559 B per idle connection** at N = 1000 by equivalence-spec §3.6's unmodified method, states that it **misses the gate**, and records that the gate has not been moved and the method has not been changed. The half that was mine — **D-10** — is closed and was verified independently in §3 |

---

## R9. Verdict line

**PASS. QA-2 clears its half of checkpoint 3 at `ce52d2f9`.**

D-23 and D-29 are closed, and both closures were checked rather than taken:
D-23's ranges are the schema's own, compared verbatim in three places, and its
failure mode is unreachable from `live.New`; D-29's re-arm reaches the wire and
the real server serves it, turning a ~30 s freeze and a full re-mount into a
**2.5 s** stall on a live connection (2.462 s and 2.515 s over two runs), with RFC §7.4's eviction demonstrably
still in place as the last resort. All eight PRD cases are MET — case 8 by
PM-1's ruling, which struck the clause this suite escalated and left a
requirement the existing spec already asserts. The suite is 40 of 40 green under
`-race` with the soak and the measurements on, its expensive half now runs in
CI, and every spec added in this pass was made to fail first.

**Three things go forward, and none of them is a condition on this verdict:**

| | what | owner | what closes it |
|---|---|---|---|
| **D-30** | HIGH. `HeartbeatInterval >= HeartbeatTimeout` is accepted and kills every quiet session with a close code that blames the client | **DEV-1** | one comparison in `Limits.validate`, in the shape the last two instances of this class took. **[SUPERSEDED — closed in `985b5f61` the same day; §R10.]** |
| **D-31** | LOW. The resync retry schedule is protocol-visible and appears in neither RFC §7.6 nor §8.4 | **PM-1 + L9-1**, from **DEV-2**'s source | a paragraph in §7.6 and a sentence in §8.4 |
| **D-25** | LOW, and now with a correction: its evidence is a printed number, not a check, and the number did not reproduce | **PM-1 + DEV-1** | the one sentence in RFC §8.5 it always asked for |

D-22, D-24, D-26, D-27 and D-28 remain open at their original severities, all
five still held by assertions that go red if the behaviour changes, and none of
them is a Phase 3 exit criterion. The checkpoint-3 **gate report** is not this
document and still needs L9-1 and the four rows in §R8 marked "not this
suite's".

---
---

# Addendum — 2026-08-04, against `b7840fb8`: D-30 closed

Everything above is unchanged. §R9 listed **D-30** as one of three things going
forward; DEV-1 closed it in `985b5f61` the same day, which made this suite's two
D-30 specs fail by their own instruction. This section is that inversion, its
mutation evidence, and the three questions the orchestrator put to me alongside
it.

**Against a clean export again:** `git archive
b7840fb850b9d5984c0997f3356f8868f6de6214 | tar -x -C /tmp/cp3-verify2`. Same
image, `dis-gotth-live:latest` `e146d50d5de6`, Go 1.26.5, staticcheck 2025.1.1.
The worktree was dirty with three other agents' in-flight work while this ran —
`client/runtime.js`, `internal/session/window.go`, `internal/session/*_test.go`
and a new `client/test/supersession.test.mjs` — so, again, the verdict is about
`b7840fb8` and not about whatever `HEAD` says when you read this.

---

## R10. D-30 — closed, and the closure asserted on the wire

### R10.1 The check

`985b5f61` adds `Limits.validateHeartbeatPair`, called last from
`Limits.validate` so the pair it judges is one the protocol's ranges already
admit. `HeartbeatTimeout` must be at least `heartbeatTimeoutIntervals = 2`
whole `HeartbeatInterval`s, compared on **effective** values — the one place
this function departs from its "runs before Normalize" rule, and correctly,
because the entire reachable case is an operator who sets one field and never
mentions the other. That was my headline repro. The error names which of the
two came from the defaults, which is the part that makes it a diagnosis rather
than a number quoted back at somebody who did not type it.

I checked the two design choices rather than accepting them.

**Two intervals rather than one is not padding, and §R10.3's MR7 is the
measurement.** One interval is the bare correctness bound: a quiet session's
only inbound frame is the echo of the heartbeat a tick carries, so the deadline
is refreshed at most once per interval. With the constant mutated to one,
`live.New` admits `1s / 1s`, and on that pair:

```
MR7: a client echoing every heartbeat survived 5s and was sent 4 heartbeats
MR7: a client that lost the echo of heartbeat 2 was CLOSED
```

Satisfiable, and useless — exactly as `985b5f61`'s own comment claims, now
measured rather than argued. G9 is "survives bad networks", and one interval is
a bound that a single lost frame breaks.

**Naming `HeartbeatTimeout` of the two is right.** Raising the timeout is always
available; lowering the interval is not, because `HeartbeatInterval` leaves the
process as a refined session parameter in the mount `Snapshot` and is a
promise already made to every connected client.

### R10.2 The inverted specs

The previous version asserted the defect and carried the instruction for this
moment — *"if `validate()` now refuses the pair, D-30 is fixed and this spec
should assert the refusal"*. Executed. Five specs now, and deliberately **not**
a second copy of `live/limits_test.go`, which holds six refusal entries
including one a nanosecond inside the boundary and is the exhaustive home for
the check.

| spec | asserts |
|---|---|
| construction, entry 1 | `live.New` refuses `2s / 1s` — the pair **this file watched die on the wire** — with a `*ConfigError` naming `Limits.HeartbeatTimeout` |
| construction, entry 2 | it refuses the 5 m ceiling D-23's own message recommends against the 50 s default, **and says the timeout came from the defaults** |
| wire, entry 1 | at the tightest pair `live.New` still admits, a client echoing every heartbeat survives |
| wire, entry 2 | at that same pair, a client that **loses exactly one echo** survives |
| defaults | `live.New` still admits its own defaults, and 50 s still clears 2 × 20 s |

The two wire entries are this suite's actual contribution, and the pair they run
on is **discovered, not hard-coded**: the spec asks `live.New` what it will
accept in tenth-of-an-interval steps and takes the smallest. The step size is
the load-bearing detail — a search in whole intervals could never find a timeout
*below* one interval, which is precisely the region D-30 was about, and a spec
that cannot reach the dangerous values is this project's recurring defect
wearing a new hat.

```
HeartbeatInterval=1s; smallest HeartbeatTimeout live.New admits = 2s (2.0 intervals);
a client echoing every heartbeat survived 5s and was sent 4 heartbeats,
where the pre-fix pair closed a faithful client 4010 after 0.

HeartbeatInterval=1s; smallest HeartbeatTimeout live.New admits = 2s (2.0 intervals);
a client that lost the echo of heartbeat 2 survived 5s and was sent 4 heartbeats.
```

The harness gained one option for the second entry — `dropHeartbeatEchoAt`,
which skips the echo of the Nth heartbeat and echoes every other one. It is
distinct from `silent`, which drops every echo and is how a spec builds a peer
the server must conclude is dead.

### R10.3 Mutation evidence

Same method as §6 and §R7: a pristine `git archive b7840fb8` export per
mutation under `/tmp/qa2-remut2/`, with only this suite's own files overlaid,
never the shared worktree.

| # | mutation | went red |
|---|---|---|
| **MR6** | `validate` returns `nil` instead of calling `validateHeartbeatPair` — i.e. **D-30 un-fixed** | both construction entries. The wire entries stayed green, correctly: at `1s / 1s` a client that never loses anything survives, which is the whole reason MR7 exists |
| **MR7** | `heartbeatTimeoutIntervals` 2 → 1 | **the lost-echo entry only.** The faithful client survived; the client that lost one echo was closed. This is the spec that holds the constant's *reason* |
| **MR8** | `required := interval / 2`, so a sub-interval timeout is admitted | **both** wire entries and the `2s / 1s` construction entry |
| **MR9** | `heartbeatTimeoutIntervals` 2 → 3, strong enough to refuse the library's own defaults | the defaults spec — and its error fires `985b5f61`'s *"Both values are this library's defaults, which is a library bug: report it"* branch, whose source comment calls it unreachable while the defaults are coherent. That comment now has a spec holding it honest |

Every one of the five has a falsifier, and no two of them have the same one.

### R10.4 The `onTick` ordering: I agree with DEV-1, and the record now says so

The original D-30 write-up described the ordering — the deadline evaluated
before the solicitation is sent, on the same tick — as the mechanism. It is the
mechanism, and it is **not independently wrong**. Stated plainly so the record
does not imply a second open defect:

- The deadline is on **inbound** frames. No echo can arrive within the tick that
  solicits it, so reversing the two cannot change that tick's outcome. It would
  emit one more heartbeat on a session that closes anyway.
- Reversing it would in fact be *worse*: the outcome would then depend on a
  network round trip racing the remainder of a function body, which is a
  nondeterminism the current order does not have.
- The sampling it produces is already a specified and asserted property.
  `case6_partition_test.go` states and measures that dead-peer detection costs
  at most `HeartbeatTimeout + HeartbeatInterval` **because** the deadline is
  sampled on the tick — re-measured this run at 7.945 s against an 8.5 s bound.

D-30 was the missing constraint that made that property satisfiable at all, not
a defect in the sampling. **No new finding here.**

The natural follow-up, since the same tick also carries the idle timeout and the
slow-client grace: that quantisation is real and is already stated where each is
measured — case 4 asserts the eviction bound as *"grace plus one tick"*
(3.88 s against a 4 s bound). So neither is a further finding either.

### R10.5 Was anything else asserting the old behaviour?

**One thing was, and DEV-1 found it and fixed it in the same commit.** Nothing
else is.

`live/snapshotparams_test.go`'s "HeartbeatInterval at the ceiling" entry set the
5 m ceiling and nothing else and asserted that it **mounts**. It did — and then
closed every quiet session on it. D-30 was encoded inside D-23's own closure
spec, which is worth noting for its own sake: the reflection property that
"mounts every configuration `New` accepts" checks that the first frame is a
`Snapshot`, and D-30 kills the session *after* that frame. The entry now carries
a timeout that can be met and keeps its subject.

I checked the rest of the tree rather than assuming. Every site that sets either
field, audited against the new rule:

| where | pair | verdict |
|---|---|---|
| `internal/session/limits.go` defaults | 20 s / 50 s | clears 2× with 10 s to spare |
| `case6_partition_test.go` | 1 s / 2.5 s | clears by **500 ms** — the tightest configuration in the suite |
| `case4_slowclient_test.go` ×4 | 1 s / 120 s, 1 s / 5 m | clear |
| `case2_gap_test.go`, `measure_test.go` ×3, `reverify_test.go` D-29 | 1 s / 5 m, 5 s / window+120 s | clear |
| `case5_flood_test.go` D-23 entries | interval out of range, timeout unset | refused on **range** first, so they still name `Limits.HeartbeatInterval` — the ordering of the checks matters here and is correct |
| `live/snapshotparams_test.go` | fixed in `985b5f61` | — |

And empirically, because a grep is an argument and a run is evidence — every
satellite module that builds a live application, at `b7840fb8`:

```
test/routers        ok 0.015s      examples/chat        ok 9.234s
test/sampling       ok 0.142s      examples/dashboard   ok 9.005s
examples/counter    ok 1.159s      docs/guide/_samples  ok (samples 4.488s, apptest 0.005s)
```

None of them sets the pair at all; all take the coherent defaults. **Nothing
else in the repository asserted the old behaviour.**

**One maintenance note, since it is invisible until it bites.** Case 6 runs at
1 s / 2.5 s, half a second inside the new constraint. Raising
`heartbeatTimeoutIntervals` to 3 would break case 6 *and* the library's own
defaults — MR9 shows the second half of that. The constant is now load-bearing
for more than the check.

### R10.6 A latent race in this suite, found by the run that was meant to confirm the inversion

The first full 42-spec run at `b7840fb8` was **red**, and not on anything to do
with D-30:

```
[FAIL] Duplicate and replayed frames (PRD case 8) [It] charges a replayed
       ResyncRequest to the resync budget rather than re-rendering (H-14)
       failed to write msg: use of closed network connection
Ran 42 of 42 Specs in 426.412 seconds — 41 Passed | 1 Failed
```

The spec sent twenty replayed `ResyncRequest`s and required **every one** to be
written successfully. But the ninth consecutive denial closes the connection
with `4008` — which is the thing the spec is asserting — and that lands around
the twelfth send. Whether the client got all twenty out before the server acted
on the twelfth is a question about the scheduler, not about the library. It
passed **12 runs out of 12 in isolation** at this same commit and failed inside
the 42-spec `-race` run on a contended host.

That is §6.1's defect class one spec over: a check whose result is the
hardware's. It is mine, it was latent in every version of this suite including
the one the original report signed off, and my three full runs at `ce52d2f9` did
not hit it. **I have not attributed the change in timing to anything.** One
observed failure is not a rate, and `internal/wsx` gained a connection-hijack
path in the same window (`conn.go` +74, `handler.go` +64, a new `hijack.go`) —
which is a plausible influence on close timing and which I did not measure. Not
guessing is the honest answer.

Fixed the way §6.1 fixed D-24's: the loop now stops at the close it asserts, and
the number of requests that actually landed becomes the premise.

And then the fix was itself falsified, which turned up something worth writing
down. My first attempt guarded only with `sent >= 12`, and I described that as
catching a server that closes too early. **It does not**, and the mutation said
so: with the threshold cut to a single denial, all twenty writes still succeeded
— the client's writes are buffered, so they keep succeeding for a while after
the server has decided to close — and the spec passed. So the guard was doing
less than its comment claimed, which is the thing this suite exists to catch.

The spec now also requires the count of typed refusals to reach the documented
`consecutiveDenialsBeforeClose × ResyncBurst = 3 × 3 = 9`, and the comment says
which guard does what.

| # | mutation | went red |
|---|---|---|
| **MR10** | the resync flood close never fires | H-14, on `Eventually(w.isClosed)` |
| **MR11** | the **first** denial closes `4008` | H-14, on the refusal count — **and passed against the first version of the fix**, which is why the refusal count is there |

### R10.7 The suite

```bash
docker run --rm --cpuset-cpus=24-27 --memory=4g -v /tmp/cp3-verify2:/w \
    -w /w/gotth-live -e GOFLAGS=-buildvcs=false \
    -e GOTTHLIVE_SOAK=1 -e GOTTHLIVE_MEASURE=1 dis-gotth-live:latest \
    bash -c 'go test -v -race -count=1 -timeout 35m ./test/internal/chaos/ \
        -args -ginkgo.fail-on-empty'
```

42 specs — the 40 of §R7.1 plus the two D-30 gained by becoming a table. Run
**twice**, deliberately, because the defect §R10.6 records appeared once in
three runs and one green run would not have been evidence about it.

```
run 1:  Will run 42 of 42 specs
        Ran 42 of 42 Specs in 426.381 seconds
        SUCCESS! -- 42 Passed | 0 Failed | 0 Pending | 0 Skipped   EXIT=0

run 2:  Will run 42 of 42 specs
        Ran 42 of 42 Specs in 428.463 seconds
        SUCCESS! -- 42 Passed | 0 Failed | 0 Pending | 0 Skipped   EXIT=0
```

H-14, the spec §R10.6 is about, now reports what the arithmetic predicts rather
than what the scheduler allowed: `20 replays -> 3 snapshots, 9 RATE_LIMITED
errors, close 4008` — nine refusals, which is
`consecutiveDenialsBeforeClose × ResyncBurst` exactly.

D-29's re-measurement moved with the export and reproduces a third and fourth
time: longest stall **2.315 s** and **2.317 s** at `b7840fb8`, against 2.462 s
and 2.515 s at `ce52d2f9`. Four independent runs, four numbers inside 200 ms of
each other, against the ~30 s the defect cost.

**And once more at a moving HEAD.** `HEAD` advanced to **`281586c3`** while the
two runs above were in flight — DEV-1 landing G2 work in `live/`, `internal/`
and `docs/bench/`, and a `livetest` migration to `GinkgoTB()`. Nothing under
`test/internal/chaos/` changed in that window (`git log b7840fb8..281586c3 --
test/internal/chaos/` is empty), but "my files did not change" is not the same
claim as "my files still hold", so the suite was re-run against a clean
`git archive 281586c3` export with these files in it:

```
go test -race -count=1 -timeout 15m ./test/internal/chaos/ -args -ginkgo.fail-on-empty
ok  .../test/internal/chaos  98.163s
```

Without the two cost classes, so 36 of 42 — the six soak and measurement specs
are the ones the two full runs above cover, and re-taking a 400-second run to
chase a moving branch would have produced a third number about a fourth commit.
**The graded verdict is about `b7840fb8`; `281586c3` is a compile-and-green
confirmation on top of it.**

---

## R11. Verdict line, re-issued

**PASS stands, and one of the three things going forward has landed.**

D-30 is **CLOSED** — `985b5f61`, verified rather than believed: the constant's
value has a spec that measures its reason, the tightest admitted pair is
discovered rather than restated, and five specs with five different falsifiers
hold it. §R9's D-30 row is superseded.

Still open, unchanged, and still not conditions on this verdict:

| | what | owner | state |
|---|---|---|---|
| **D-31** | the resync retry schedule is protocol-visible and appears in neither RFC §7.6 nor §8.4 | **PM-1 + L9-1**, from DEV-2's source | with L9-1 in the checkpoint-3 batch review; a ruling is expected and no action is owed by me |
| **D-25** | RFC §8.5 documents one direction of the at-most-once leak, and its evidence here is a printed number rather than a check | **PM-1 + DEV-1** | open |

D-22, D-24, D-26, D-27 and D-28 remain open at their original severities. No new
library defect was found in this pass — §R10.6 is a defect in **this suite**,
found and fixed here, and the `onTick` ordering is explicitly **not** one
(§R10.4).

---
---

# Re-verification — 2026-08-05, against HEAD `1864cf92`

Everything above this line is the earlier record and is unchanged. This section
is the re-verification `docs/pm/checkpoint-3-closure.md` §8 item 3 and §2's row
**C-35(c)** ask for. It supersedes nothing above it except where it says so.

**Why it exists.** L9-1 AFFIRMED but NARROWED §R9's PASS: that PASS was earned
at `ce52d2f9`, three commits before the transport change, so no QA-2 sign-off
covered `5a2ca417` (review-checklist §8.6). The set has grown a great deal since
that narrowing, and the closure ledger enumerates it: **C-34** and **BR-8** in
`internal/wsx`, **BR-1…BR-9** in `internal/session`, **BR-3** and **U-6** in
`internal/render`, **D-4** and **U-5** in `internal/protocol`. §R13 takes each
one, says which of §R8's rows it *could* move and by what mechanism, and then
whether the re-run shows it did.

**Against a clean export, as every re-verification here has been:**

```bash
rm -rf /tmp/cp3-verify-head && mkdir -p /tmp/cp3-verify-head
git archive 1864cf92d1113ce4b8c8a68977ccafca2a744bcb | tar -x -C /tmp/cp3-verify-head
```

Same image throughout — `dis-gotth-live:latest`, `e146d50d5de6`, Go 1.26.5,
staticcheck 2025.1.1 — on `node-b`. The graded verdict is about
**`1864cf92`** and nothing else; other agents were writing in the shared
worktree while this ran, which is the whole reason the export exists.

`1864cf92` is two commits past the closure ledger's own pin of `d06101bb`, and
the difference cannot touch anything this suite measures:

```
git diff d06101bb 1864cf92 -- gotth-live/live gotth-live/internal \
    gotth-live/client gotth-live/proto     →  empty
```

Those two commits are the orchestrator's gate run and C-33(b), both under
`docs/` and `ci.sh`.

---

## R12. What I ran, and the host it ran on

### R12.1 The whole suite, as `ci.sh` runs it

```bash
docker run --rm --cpuset-cpus=24-27 --memory=4g \
    -v /tmp/cp3-verify-head:/w -w /w/gotth-live \
    -e GOFLAGS=-buildvcs=false -e GOTTHLIVE_SOAK=1 -e GOTTHLIVE_MEASURE=1 \
    dis-gotth-live:latest \
    bash -c 'go test -v -race -count=1 -timeout 35m ./test/internal/chaos/ \
        -args -ginkgo.fail-on-empty'
```

```
Will run 42 of 42 specs
Ran 42 of 42 Specs in 425.840 seconds
SUCCESS! -- 42 Passed | 0 Failed | 0 Pending | 0 Skipped
--- PASS: TestChaos (425.84s)
ok  github.com/candacelabs/candace/pkg/gotth/test/internal/chaos  426.905s
EXIT=0
```

`Will run 42 of 42` is the line that says both environment variables reached the
process; `-ginkgo.fail-on-empty` is what makes a run evidence rather than a
tautology. **Every re-verified number in §R13 comes from this run**, which is
the export *as `1864cf92` shipped it* — the two spec changes §R15 makes are not
in it. That ordering is deliberate: the movement §R13.5 reports was observed by
the **unchanged** specs, so it is a re-verification finding and not an artifact
of my own edit.

### R12.2 The Appendix-B measurements, unraced and pinned

§7.3 records that `-race` collapses the provenance ratio by taxing everything
equally, so the headline Appendix-B figures are taken without it, exactly as
§7's and §R2.2's were:

```bash
docker run --rm --cpuset-cpus=24-27 --memory=4g -v /tmp/cp3-verify-head:/w \
    -w /w/gotth-live -e GOFLAGS=-buildvcs=false -e GOTTHLIVE_MEASURE=1 \
    dis-gotth-live:latest \
    bash -c 'go test -v -count=1 -timeout 25m ./test/internal/chaos/ \
        -args "-ginkgo.label-filter=measure" -ginkgo.fail-on-empty'
```

```
Will run 4 of 42 specs
Ran 4 of 42 Specs in 317.636 seconds
SUCCESS! -- 4 Passed | 0 Failed | 0 Pending | 38 Skipped
--- PASS: TestChaos (317.64s)
ok  github.com/candacelabs/candace/pkg/gotth/test/internal/chaos  317.913s
EXIT=0
```

### R12.3 `internal/wsx`'s D-10 soak, re-run because C-34, BR-8 and the hijack path all landed there

§3 verified D-10's closure rather than believing it. Three of the named change
set's items are in that same package — C-34's registration and drain ordering,
BR-8's admission reservation, and `5a2ca417`'s new `hijack.go` — so the closure
was re-checked rather than carried:

```bash
docker run --rm --cpuset-cpus=24-27 --memory=4g -v /tmp/cp3-verify-head:/w \
    -w /w/gotth-live -e GOFLAGS=-buildvcs=false dis-gotth-live:latest \
    bash -c 'go test -v -race -count=1 ./internal/wsx/'
```

```
Ran 38 of 38 Specs in 9.852 seconds
SUCCESS! -- 38 Passed | 0 Failed | 0 Pending | 0 Skipped
ok  github.com/candacelabs/candace/pkg/gotth/internal/wsx  10.869s
EXIT=0
```

Both of D-10's signals, with their published margins:

```
   100 cycles: live heap  3,528 B ( 35.3 B/cycle) against    268,544 B
   100 cycles: RSS      524,288 B (5242.9 B/cycle) against  8,798,208 B
10,000 cycles: live heap  3,672 B (  0.4 B/cycle) against    902,144 B
10,000 cycles: RSS   17,698,816 B (1766.6 B/cycle) against 49,348,608 B
```

**D-10 stays closed at HEAD**, on both the heap and the RSS budget the closure
added, with the transport rewritten underneath it.

### R12.4 The suite with §R15's two reworked specs

Same invocation as §R12.1, against a pristine `1864cf92` export with only
`test/internal/chaos/case8_replay_test.go` overlaid — the file `34945818`
commits, and nothing else:

```
Will run 42 of 42 specs
Ran 42 of 42 Specs in 424.228 seconds
SUCCESS! -- 42 Passed | 0 Failed | 0 Pending | 0 Skipped
--- PASS: TestChaos (424.23s)
ok  github.com/candacelabs/candace/pkg/gotth/test/internal/chaos  425.334s
EXIT=0
```

Still **42**, because §R15 replaces one spec with one spec: the H-11 spec gained
two arms rather than becoming three `It`s, so that a reader who reaches it meets
all three statements about the ring in one place. Host after: load 6.68, 5.96,
5.85; `gpu-desktop-steam-1` 370.73 % / 518.9 MiB.

### R12.5 Host state, and the contention label

`node-b`, 32 cores, and it is not an idle machine. It is also serving a
**live Steam session**: `gpu-desktop-steam-1` sat within a few percent of four cores
for the entire window, sampled either side of every run.

| when | `uptime` load average | `gpu-desktop-steam-1` CPU / MEM |
|---|---|---|
| before the full raced run (07:18:20Z) | 2.40, 4.25, 4.93 | 374.26 % / 517.9 MiB |
| after the full raced run (07:25:52Z) | 4.11, 4.42, 4.74 | 380.01 % / 535.4 MiB |
| before the unraced measurement run (07:26:15Z) | 3.20, 4.17, 4.65 | 377.16 % / 518.7 MiB |
| after the unraced measurement run (07:31:47Z) | 3.85, 4.23, 4.52 | 378.69 % / 518.1 MiB |
| before `internal/wsx` (07:39:52Z) | 8.94, 6.76, 5.51 | — |
| across the §R14.3 A/B (07:35–07:39Z) | 4.55 → 7.89 → 8.98 → 5.95 | — |

**Every measurement below was taken on a contended host**, in a container pinned
to `--cpuset-cpus=24-27` with `--memory=4g`. Per equivalence-spec §3.6 that is
publishable **because it says so**, and an unlabelled run is not. That is the
same label §2.2 and §R2.4 carried, and it is worth adding what the earlier two
could not: this window is *less* loaded than either of theirs — §2.2's runs sat
at 9.87/12.62 and §R2.4's at 6.18–15.60, against 2.40–8.98 here. That difference
is not cosmetic. §R14.3 is a figure where it turned out to be the entire story,
and I measured that rather than asserting it.

---

## R13. The change set, and which of §R8's rows each item could move

The rule this section follows: for each named item, first the rows it **could**
move and the mechanism by which it could, then whether the re-run shows it
**did**. Cases 3, 4, 6 and 7 are review-checklist §8.6's named surface, and the
analysis does not stop there, because the row that actually moved is case 8's.

**The short answer.** Forty-two of forty-two specs are green and all eight cases
are MET. **One row's numbers moved — case 8's H-14 replay, 3 snapshots to 0 —
and BR-9's clamp is the cause, proven by mutation rather than argued.** One spec
was found asserting the reverse of its own title and unable to tell: **D-32**,
§R15. Everything else is inside the run-to-run spread of the same measurement,
and §R14.3 is a number that moved for a reason that is not the change set at
all.

### R13.1 `internal/wsx` — C-34 and BR-8

| | |
|---|---|
| **C-34** (`ed9f73b6`) | `Handler.Close` sets `draining` and snapshots `h.sessions` in one critical section; `register` takes that same lock and refuses while draining; `newConn` is split out of `serve` so registration happens on the `ServeHTTP` goroutine; `deregister` precedes `close(c.done)` |
| **could move** | **case 3** (a SIGKILLed process's successor accepting 25 mounts, and every standing server's own teardown), **case 7** (registration and deregistration run once per cycle, ten thousand times — §6's **M6** mutation, *"the registry entry is never removed"*, is the falsifier this suite already carries for exactly that), **case 4**'s "never the process" clause, **case 6**'s goroutine baseline |
| **did it** | **No.** Case 3: `25 clients, SIGKILL restart; port rebound in 4ms; slowest reconnect+resync 611ms over 1 attempts (bound 30s); ledger 29 -> 362 distinct commits`. Case 7: `goroutines 7 -> 7; live heap retained -928 B (-0.1 B/cycle) against 902144 B` over ten thousand abrupt cycles. Case 6: `goroutines 13 -> 8`. Case 4: a fresh connection was still served after the stall |

Coverage nobody has claimed, worth stating because it is free: `serve()`
registers `DeferCleanup(s.stop)` and `stop` calls `app.Close(ctx)`, so C-34's
changed drain path runs once per `serve()` — about forty-five times in one suite
run, including immediately after every case that leaves sessions in a hostile
state.

| | |
|---|---|
| **BR-8** (`fff99245`) | `admit` reserves **both** limits in one critical section: `perID` as before, plus a `pending` counter, with the process check reading `len(sessions) + pending` |
| **could move** | **case 3** (25 concurrent clients under one identity is the per-identity half; `cmd/chaossrv/main.go:166` sets `MaxSessionsPerIdentity: 200` deliberately, and §4.3 records why), **case 7** (admit and release run once per cycle, so a reservation leaked on any path would accumulate ten thousand times) |
| **did it** | **No.** Both green |

**And a limit of this suite, stated rather than left to be assumed.** BR-8's
*process* half is **not exercised here at all**. `live.DefaultLimits().MaxSessions`
is **0**, which `live/config.go:297` documents as unlimited, and no chaos
configuration sets it — so `handler.go:306`'s
`len(h.sessions)+h.pending >= h.opts.MaxSessions` sits behind a `MaxSessions > 0`
guard that never opens here, and a leaked `pending` would be invisible to case
7's ten thousand cycles. Where it *is* covered is `internal/wsx`'s own BR-8
spec, which the closure ledger records as deliberately concurrent because a
serial one passes against the defect. I ran that package (§R12.3); I did not
write a second copy of somebody else's spec. **Observation, with where I read
it — not a grade.**

### R13.2 `internal/session` — BR-1…BR-9

| | what landed | could move, and why | did it |
|---|---|---|---|
| **BR-1** | the acknowledgement stopped evicting; eviction is by age at `retentionSlots() = AckWindow + 1`; `trackedBytes` moves from `cap × 64` to `(cap+1) × 48` | **case 8**'s H-11 replay row is *directly* on it: "a telemetry report for a patch that has **left the window**" is a sentence whose meaning changed underneath. **case 4**: the ring now holds `cap+1` slots in steady state where a healthy session's used to sit near empty, so the heap under backpressure could move. **FR-34**: `depth()` is still `highest - acked` and untouched, so `gotthlive_outbound_window_depth` still means what it meant | **YES, and it is D-32.** The H-11 spec was asserting the *reverse* of what its title said and could not tell, because the only thing it checked was that the connection survived. §R15. Case 4 did **not** move: `max window depth 16 of 16`, heap `169,664 B` against §R8's 169,536 B and §4.4's 169,792 B — the same measurement's own spread, and three orders inside the 4 MiB budget |
| **BR-2** | `Config.Events` names and `EffectSource()` refused against `protocol.ValidOriginSource`, at `live.New` and before an effect runs | **case 1** (effects in flight across the cut), **case 4**, the batching row, and every throughput figure in §R14 — the check is a length compare plus a ≤64-byte scan **on the emission path**, per call and deliberately uncached | **No.** `chaos.commit` and `chaos.ticker` are both accepted; case 1 is green at `0 duplicated` with convergence against the ledger. Recorded because it makes the emission path *longer*: it is one of the two mechanisms I considered and rejected for §R14.3 |
| **BR-3** | `render()` installs nothing; `Renderer.Commit` is the only writer of `v.hashes` and runs after a successful write; `Discard` re-marks the updated fragments | **case 1** (the cut kills sends mid-patch), **case 2**'s "all three fragments re-rendered", **case 4**'s render under a full window | **No — and this suite does not reach the changed branch, which is worth saying rather than implying coverage.** `Commit`/`Discard` split on a **survivable** send failure, i.e. `protocol.InvalidFrameError`. The two reachable triggers for that were **D-23** (an out-of-range `Limits` field making a frame unencodable) and **BR-2** (an over-long `Origin.source`), and both are now refused at `live.New`. A cut socket is a *transport* error and takes `send`'s fatal path instead. Covered where it is reachable, in `internal/render/renderer_test.go` and `internal/session/actor_test.go`'s wide-app harness — read, not re-run by me. **Observation, not a grade** |
| **BR-4** | `redefer` on all three non-emitting exits, with `pendingIDs` capped at `CoalesceFlushCeiling` | **the batching/coalescing row** — P5's set equality is that row's second clause — plus **case 4**'s coalescing arm and **QA3-1** | **No.** Case 4: `CoalesceFlushAt=64, AckWindow=8, client never acknowledges: flush fired with a union of 64, largest union over the run 64`. QA3-1 identical to §R5.2 in every cell. The reason is that in this suite every coalescing emission *succeeds*, and `redefer` runs only where one does not; the remaining arm needs a **fully suppressed** render of a flushing transition, which the ticker workload cannot produce because every tick changes `Ticks` and therefore the markup |
| **BR-5** | `emitSnapshot` returns `(int, bool)`; a mount whose snapshot cannot be sent emits `Error{INTERNAL, fatal}` and closes rather than serving | **case 3** (25 mounts against a freshly restarted process), **case 1** (the reconnected mount), **case 5**'s D-23 entries | **No.** No mount snapshot failed, which is the expected outcome now that D-23 is closed: this suite has no reachable trigger left for it. Case 3's ledger went 29 → 362 across the restart and case 1's reconnected Snapshot matched the ledger exactly |
| **BR-6** | `resyncBucket.allow` moved **above** the no-op short circuit | **case 2**'s "a resync describing no gap costs an `Ack` and no `Snapshot`", **case 8**'s H-14 row, and both D-29 specs | **Partly — it is the second half of the one row that moved.** Case 2's no-op spec sends exactly one such request, which `ResyncBurst = 3` admits, so its Ack still arrives and its "no Snapshot" assertion still holds; it would move only for a spec sending more than the burst. Case 8 is where it shows: every one of the nine typed refusals that produce the `4008` is now the refusal of a request that renders **nothing**, which is precisely the frame kind BR-6 found charged to no bucket at all. Now pinned by an assertion rather than inferred — §R15, falsifier **MH5b** |
| **BR-7** | `sameState` reads `App.StateComparable()`, resolved once at construction | every case's transition path, and therefore **QA3-1**, **QA3-3** and the batching row | **No.** `board` is `struct{Total int; Ticks int; Note string}`, so `StateComparable()` is true and the predicate resolves to exactly what `reflect.Type.Comparable()` returned before; `app_test.go:107` already says the state is comparable on purpose. What it removes from the per-transition path is one `reflect.Type.Comparable()` call, which I considered and rejected as the mechanism for §R14.3 |
| **BR-8** | §R13.1 | | |
| **BR-9** | the supersession range's lower bound clamped at `max(win.ackedSeq(), lastSnapshotSeq)` before anything is derived from it | **case 2** (`superseded [4,5] of server_seq 6` **is** this arithmetic), **case 1** (reconnect → resync), **case 8**'s H-14 row, and the **D-29 post-fix** spec, whose retry-outruns-an-acknowledgement interleaving is the one `d3c06eb7`'s own message names as the reason the acked floor alone is insufficient | **YES — this is the row that moved.** Case 2 is **unmoved**, byte for byte: `gap at patch 3; superseded [4,5] of server_seq 6; 3 fragments re-rendered; truth=4`, because that client never understates what it has acknowledged. **Case 8 moved from 3 snapshots to 0**; §R13.5 is the mechanism and MH1 is the proof |

### R13.3 `internal/render` — U-6, and `internal/protocol` — D-4 and U-5

BR-3 is in §R13.2's table. The other three:

| | what landed | could move, and why | did it |
|---|---|---|---|
| **U-6** | fragments render through a `fragmentWriter`: `*bytes.Buffer` is unreachable and a write outside the call is refused with `errWriterEscaped` | **every case's markup**, and by cost **QA3-1** and **QA3-3**; **case 4**'s heap figure, since the commit claims one pointer per session rather than one wrapper per fragment | **No.** Case 4's heap is 169,664 B, unmoved within spread. Every markup assertion holds, and the strongest of them are case 1's and case 2's convergence against the *application's own ledger* rather than against anything the protocol produced — which is the best statement this suite can make that swapping the writer out did not corrupt a byte |
| **U-5** | `Framer.Encode` returns an opaque `protocol.Encoded`; `Write` takes it and cannot be handed bytes | **case 5**'s heap-per-frame figure and D-28's oversize close, **case 8**, **QA3-3** | **No.** Case 5's flood retained `2,214,592 B` against §4.5's 2,254,384 B of a 4 MiB budget; the oversize arm retained `-3,152 B` against §R8's −3,544 B; D-28 still tells the client `1009` and the operator `normal`. I read `actor.go`'s `send` to confirm that the `Encode`/`Write` split instrumentation §2.3 depends on survives, rather than inferring it from a number |
| **D-4** | `checkEnums` and `checkListBounds` become one `checkFieldInvariants` walk, on **both** hot boundaries | **case 5** (every refused frame is a parse — 10,260 of them in four seconds), **case 8** (every replayed frame is a parse), **QA3-3** (every emitted frame is an outbound validation), the FR-34 row | **No.** Case 5 measured `2,562 frames/s` of typed refusals against §R8's 2,512 and §4.5's 2,565, connection still open, `9,961 RATE_LIMITED` back. **One thing D-4 changes that nothing here pins, and I am not adding a spec for it:** with one walk the first violation **in field order** wins, where with two walks every enum violation was found before any list violation — so a frame violating both now carries the other rejection-reason metric label. No spec in this suite asserts a rejection *reason*, so nothing here would have caught it. `internal/protocol/outbound_test.go` gained 100 lines in the same commit and is where that belongs. **Observation for `internal/protocol`'s owner, not a defect** |

### R13.4 `5a2ca417` and `hijack.go` — the original C-35(c) subject, still in range

`ServeHTTP` returns at the upgrade, the session runs under
`context.WithoutCancel`, the session goroutine installs its own recover, and the
ResponseWriter is wrapped so the buffers the transport retains for the
connection's life are the ones this library sized.

- **Could move:** **case 7** (the goroutine topology and the per-cycle heap, ten thousand times), **case 3** (the accept path), **case 6** (the goroutine baseline), **case 4**'s "never the process".
- **Did it: no.** Case 7's ten-thousand-cycle arm — `goroutines 7 -> 7; live heap retained -928 B (-0.1 B/cycle) against 902144 B`. Case 6 — `goroutines 13 -> 8`. Case 3 — port rebound in 4 ms, slowest reconnect 611 ms of a 30 s bound.

One number here did move, and it is worth being precise about rather than quiet.
Case 7's **300**-cycle arm reads `9,776 B (32.6 B/cycle) against 281,344 B`
where §4.7 read `880 B (2.9 B/cycle)`. That budget is `256 KiB fixed +
64 B/cycle`, so **93 % of the 300-cycle budget is the fixed allowance**, and
"32.6 B/cycle" is 9.8 KB of one-time noise divided by 300 rather than a
per-cycle line. The arm with signal in it is the ten-thousand-cycle one and it
is at −0.1 B/cycle, against −0.0 at §4.7 and 1.2 at §R8. **I have not attributed
the 300-cycle difference to anything.** One observation is not a rate, and
§R10.6 is this document's own precedent for not guessing.

### R13.5 The row that moved: case 8's H-14 replay, 3 snapshots → 0

```
§R8, at ce52d2f9 and again at b7840fb8:
  20 replays -> 3 snapshots, 9 RATE_LIMITED errors, close 4008
at 1864cf92, same spec, unchanged:
  20 replays -> 0 snapshots, 9 RATE_LIMITED errors, close 4008
```

The spec replays one `ResyncRequest{last_applied_seq: 1}` twenty times, after
three commits, against a client running `ackAuto`. Two changes compose:

1. **BR-9's clamp.** `ackAuto` has already acknowledged past sequence 1, so
   `win.ackedSeq()` exceeds the replayed cursor. The clamp lifts `applied` to
   the acknowledged high-water mark, counts the contradiction under
   `understated_last_applied`, and the request then **describes no gap** — so it
   takes the no-op short circuit and is answered with an `Ack`.
2. **BR-6's ordering.** The budget is consulted *before* that short circuit, so
   those no-op answers are charged. Nine consecutive denials still reach `4008`.

Both halves are the fix working, and this is not a hypothesis: with BR-9's
snapshot floor removed and nothing else changed, the same spec produces **three**
again — `20 replays of ONE cursor produced 3 full re-renders` — which is
mutation **MH1** in §R16 and is what turns an attribution into a measurement.

What was wrong was this suite's own prose and one of its assertions. The
premise read *"three commits so there is a real gap to describe, and the resync
path is the expensive one rather than the no-op short circuit"*, which BR-9
falsified; and `produced <= 4` went from bounding amplification to being
satisfied for a reason unrelated to the budget.

**A stronger property is available and is now asserted.** Under BR-9 the bound
on a replayed *fixed* cursor is **structural, not budgeted**: the first
answering Snapshot moves `lastSnapshotSeq` past that cursor, so every later
replay of the same cursor describes no gap **no matter how much budget it is
given**. At most one Snapshot is reachable, ever. The spec now asserts `<= 1`,
and separately asserts that the allowed requests were **answered** at all —
which is the premise under the refusal count, because a budget guarding only
the expensive path would answer all twenty for free and close nothing. That is
exactly the state BR-6 found, and **MH5b** is its falsifier.

**An independent confirmation of the same mechanism, found while reading rather
than measured by me.** `c1338120` — *"the resync measurement asked with a cursor
it had already contradicted"* — is DEV-3 hitting this in `examples/dashboard`
and diagnosing it identically: the example asked with `last_applied_seq=1` from
a session that had acknowledged everything, was answered with a range covering
patches it had already applied, and *"the frame this measurement was timing is
one a browser would have hung up on"*. Two consumers, one mechanism, arrived at
separately. §R17 carries what that leaves owing on DEV-3's row.

---

## R14. The Appendix-B measurements, re-taken

### R14.1 QA3-1 — is `coalesce_flush_at = 512` the right value?

From the unraced pinned run of §R12.2:

| `CoalesceFlushAt` | frames/s | patches | flushes | flushes/s | largest union | **measured margin below H-4's 1024** | vs §R5.2 |
|---:|---:|---:|---:|---:|---:|---:|---|
| 64 | 1.50 | 39 | 24 | 0.800 | 64 | **960** | identical |
| 128 | 1.13 | 27 | 12 | 0.400 | 128 | **896** | identical |
| 256 | 0.90 | 21 | 6 | 0.200 | 256 | **768** | identical |
| **512 (default)** | **0.83** | **18** | **3** | **0.100** | **512** | **512** | identical |
| 959 (`MaxCoalesceFlushAt`) | 0.73 | 16 | 1 | 0.033 | 959 | **65** | identical |

```
53 updates/s for 30s against a client that never acknowledges:
0.73 frames/s total, 15 patches (0.50/s), largest contributing union 1, 0 flushes
```

**Nothing moved, and for QA3-1 that is the load-bearing result.** U-3 restated
H-4's headroom in one set of terms and added a spec that drives the union to
`CoalesceFlushCeiling` exactly; it did **not** move `MaxCoalesceFlushAt`, still
`CoalesceFlushCeiling - 1 - MaxEventContributing = 959`. The measured margin at
959 is still **65**, which is `1 + MaxEventContributing` and therefore still has
no slack for an application filling `Event.Contributing` — so §7.1's argument
for 512 over 959 stands word for word, and §7.1's separate finding about RFC
§7.4's 1,590 figure naming the wrong workload is unchanged and still owed to
DEV-1 + L9-1. BR-4's `redefer` and the `CoalesceFlushCeiling` cap it introduced
are not visible here, for the reason §R13.2's BR-4 row gives.

### R14.2 QA3-2 — the rate at which a *legitimate* client is rate-limited

| patch loss | patches seen | requests | request rate | answered | refused | refusal rate | vs §R4.1 |
|---:|---:|---:|---:|---:|---:|---:|---|
| 1 % | 1059 | 10 | 0.50/s | 10 | 0 | **0 %** | identical |
| 5 % | 115 | 5 | 0.25/s | 4 | 1 | **20 %** | identical |
| 10 % | 55 | 4 | 0.20/s | 3 | 1 | **25 %** | identical |
| 25 % | 31 | 4 | 0.20/s | 3 | 1 | **25 %** | identical |

Identical to §7.2 and §R4.1 in **every cell**, including the second column, and
that is the correct outcome twice over. The refusal rate is a property of the
*server's* budget, and neither BR-6 nor BR-9 changes what that budget grants a
client whose cursor is honest: QA3-2's client asks once per gap and never
understates, so BR-9's clamp is a no-op for it and BR-6's ordering never reaches
the short circuit. §R4.1's standing caveat also stands — this client is the
**pre-fix** runtime model, so the second column measures a client spending the
window latched, and the spec says so in its own text.

**20–25 % of legitimate resync requests are still refused at 5–25 % patch
loss**, still because of clustering rather than rate, and §R4.1's withdrawal of
that as *urgent* still holds for the reason it gave: D-29's re-arm makes the
refusals cheap rather than rare. Re-measured this run, the fixed client's cost
is `longest stall 2.792s` — a fifth independent measurement, against 2.462 s and
2.515 s at `ce52d2f9` and 2.315 s and 2.317 s at `b7840fb8`, and against the
~30 s the defect cost.

### R14.3 QA3-3 — provenance-log volume

**Volume: unmoved, to a tenth of a byte.**

```
53 updates/s for 20s: 1061 provenance records, 393,277 B total,
370.7 B/record, 19,664 B/s/session (19.66 KB/s)
```

| | estimate | §7.3 | §R5.2 | **at `1864cf92`** |
|---|---:|---:|---:|---:|
| per record | ≈200 B | 370.7 B | 370.6 B | **370.7 B** |
| per session per second | ≈10.6 KB/s | 19,663 B/s | 19,625 B/s | **19,664 B/s** |
| records for a 20 s window at 53/s | — | 1,061 | 1,061 | **1,061** |

The estimate is still low by **1.85×**, uniformly; the record count is still
exactly the transition count. The change set did not touch the record —
`Actor.provenance` is byte-identical across the range and still writes the same
fourteen fields — and equivalence-spec **T-5**'s ≈19.7 MB/s at D3's N = 1000
stands.

**Throughput: moved a long way, and the cause is the host. Measured, not
assumed.**

| configuration | §7.3 | §R5.2 | **§R12.2, at `1864cf92`** |
|---|---:|---:|---:|
| provenance **off** (`Logger` nil) | 124,518/s | 128,595/s | **127,023/s** |
| provenance on, discarding text handler | 71,383/s | 70,281/s | **89,313/s** |
| provenance on, JSON to a counting sink | 15,845/s | 13,183/s | **27,992/s** |
| implied delta, text / JSON | −42.7 % / −87.3 % | −45.3 % / −89.7 % | **−29.7 % / −78.0 %** |

The JSON arm doubling is far outside the run-to-run spread §7.3 labelled this
row with, so it needed a mechanism, and *"a figure that moved with no mechanism
named is a finding, not a number."* I looked for one in the change set first and
found only candidates pointing the **wrong way**: BR-2 adds a per-emission
`ValidOriginSource` scan, U-6 adds a bool test per fragment write, and BR-4's
`redefer` can only make the union it logs *larger*. BR-7 removes one
`reflect.Type.Comparable()` per transition, which is real but nowhere near a
factor of two. `internal/obs/log.go`'s only change in the range is REV-DEL's
deletion of `U64s` and `Strs`, which had **zero callers**.

So I measured it instead: the same spec — `measure_test.go` is **byte-identical**
across the two trees, verified with `diff` — against `git archive ce52d2f9` and
against `git archive 1864cf92`, **ABBA**, same host, same pinning, one window:

| # | tree | 1-min load at start | off | text handler | **JSON sink** |
|---|---|---:|---:|---:|---:|
| A1 | HEAD `1864cf92` | 4.55 | 126,969/s | 87,734/s | **25,532/s** |
| B1 | `ce52d2f9` | 7.89 | 124,947/s | 80,197/s | **25,614/s** |
| B2 | `ce52d2f9` | 8.98 | 123,034/s | 78,333/s | **21,740/s** |
| A2 | HEAD `1864cf92` | 5.95 | 129,536/s | 81,874/s | **26,936/s** |
| (§R12.2's own run) | HEAD `1864cf92` | 3.20 | 127,023/s | 89,313/s | **27,992/s** |

```bash
docker run --rm --cpuset-cpus=24-27 --memory=4g -v "$tree:/w" -w /w/gotth-live \
    -e GOFLAGS=-buildvcs=false -e GOTTHLIVE_MEASURE=1 dis-gotth-live:latest \
    bash -c 'go test -v -count=1 -timeout 10m ./test/internal/chaos/ \
        -args "-ginkgo.focus=QA3-3" -ginkgo.fail-on-empty'
```

`-v` because `go test` without it **discards a passing package's output**, which
is C-33(b)'s own lesson one directory over; the first attempt at this A/B was
silent for exactly that reason and was re-run. The `Ran 1 of 36` versus `Ran 1 of
42` in the two trees' output is a free check that the exports really are the two
different trees.

**`ce52d2f9` — the tree §R5.2's 13,183/s came from — produces 25,614/s and
21,740/s today.** The two trees are inside each other's spread on all three
arms, and both are roughly double what the same tree published yesterday. **The
change set did not move this row. The host did**, and the direction fits the
mechanism: `--cpuset-cpus` partitions cores, not last-level cache or memory
bandwidth, the JSON arm is by far the most allocation-heavy of the three, and
this window's whole-host load is roughly half §R5.2's. That is also why the
`off` arm — the least memory-hungry — is the one that barely moved in either
direction across all five samples.

**What that means for the number instrumentation I6 is waiting on.** At HEAD, on
this host, at §R12.2's load:

| | transitions/s | µs/transition | added by the log |
|---|---:|---:|---:|
| provenance off | 127,023 | 7.87 | — |
| discarding text handler | 89,313 | 11.20 | **+3.32 µs** |
| JSON to a counting sink | 27,992 | 35.73 | **+27.85 µs** |

- At the dashboard's 53 updates/s per session that is **1.48 ms of CPU per second per session, 0.15 %** — two orders of magnitude inside NFR-1's ≤ 5 %. §7.3's conclusion that the log path is not an NFR-1 problem at realistic rates is unchanged and is now more comfortable, not less.
- At D3's N = 1000 × 53/s = 53,000 transitions/s, provenance JSON encoding alone is **≈1.5 CPU-seconds per second — about one and a half cores**, where §7.3 measured ≈2.9 and called it three.
- **The honest form of that input to I6 is a range with its method attached, not a point.** The same tree measures at three cores on a loaded host and one and a half on a quieter one, and nothing about the library changed between them. **PM-1 and instrumentation I6 should be given both ends and the load at which each was taken**, which is what this table now does. The PM-1 half of I6 remains PM-1's.

---

## R15. Defects

Numbering continues from D-31.

### D-32 — MEDIUM — this suite's H-11 spec asserted the reverse of its own title, and could not tell

**Owner: QA-2 — mine. Found and fixed in this pass, in `34945818`. Not a library
defect and not merge-blocking.**

`case8_replay_test.go`'s H-11 spec was called *"drops a replayed telemetry
report for a patch that has left the window"*. Its comment stated the mechanism
in prose — *"Acknowledged patches leave the window"* — and its body sent exactly
one report about one patch that `ackAuto` had just acknowledged.

**BR-1 (`37df5537`) inverted that.** An acknowledgement no longer evicts;
eviction is by age at `retentionSlots() = AckWindow + 1`. So the single report
the spec sends is now precisely the report BR-1 exists to make **land**, and the
behaviour under the spec's own title reversed. The spec stayed green, because
the only thing it asserted was

```go
Consistently(w.isClosed, 1*time.Second, 100*time.Millisecond).Should(BeFalse())
```

and the connection survives whichever way the report is treated.

**The repro is executed and it is the whole finding.** The old spec, unmodified,
run against a pristine `1864cf92` export, and then against that same export with
BR-1 un-fixed (the acknowledgement evicting again, mutation MH2):

```
the OLD H-11 spec, against the FIXED library:
    Ran 1 of 42 Specs in 2.004 seconds
    SUCCESS! -- 1 Passed | 0 Failed | 0 Pending | 41 Skipped    EXIT=0

the OLD H-11 spec, against the library with BR-1 UN-FIXED:
    Ran 1 of 42 Specs in 2.005 seconds
    SUCCESS! -- 1 Passed | 0 Failed | 0 Pending | 41 Skipped    EXIT=0
```

**One spec, two libraries whose behaviour on that spec's own subject is exactly
opposite, two greens.** That is this repository's recurring defect class for the
sixth time, after C-21's unread `total`, D-19's `clean` printed without
`gofmt`, D-20's suite that was green because it never ran, C-33's skip that
never skipped, and the `Fixed1` table that asserted the bug. It is MEDIUM rather
than LOW because H-11 is a *defence* — the report is untrusted input used to
fabricate a span — and because BR-1 deliberately widened what the ring will
resolve, so this is exactly the spec that was supposed to say the widening did
not spend the defence.

**The fix** is three arms, each reading an instrument rather than the
connection's liveness, and each with its own falsifier:

1. a report about a patch the client has just acknowledged is **used** — one
   `gotthlive_client_morph_duration_seconds` observation, and the drop counter
   still zero. This is BR-1's whole point, on the wire rather than in
   `internal/session`'s in-process harness. Falsifier **MH2**.
2. a report naming a patch that never existed is **dropped and counted** —
   H-11's defence, which BR-1 was not allowed to spend. Falsifier **MH4**.
3. a report about a patch older than the retention bound is **dropped and
   counted** — the "left the window" case the title always claimed, now that
   leaving the window means age rather than acknowledgement. It is also the only
   thing here that says the ring is bounded at all, and an unbounded one is
   per-session memory a client decides the size of. Falsifier **MH3**.

What it now reports, from §R12.4's run:

```
acknowledged and inside the ring: USED (1 client-morph observation); forged: dropped;
older than retentionSlots = AckWindow + 1 = 17 after 19 further patches: dropped.
gotthlive_client_telemetry_dropped_total = 2
```

and, beside it, the H-14 spec now naming what §R13.5 measured:

```
20 replays -> 0 snapshots, 3 Acks, 9 RATE_LIMITED errors, close 4008.
The snapshot count was 3 before BR-9's clamp and is structurally at most 1 after it.
```

**Two things this defect is not.** It is not a regression in BR-1, which is
correct: `internal/session/actor_test.go:264`'s *"accepts the report the shipped
client sends, which acknowledges the patch first"* asserts the review's 40-of-40
repro directly, on the same two instruments this one reads. And it is not the
same finding as §R13.5's H-14 premise, which is a *comment* that went stale plus
an assertion that stopped binding — serious enough to fix in the same commit,
but the H-14 spec never asserted the opposite of its own subject, and its close
and refusal assertions held throughout.

**What this spec adds over that one, stated so it is not mistaken for a second
copy.** `actor_test.go`'s is in-process, against the session harness. This one
is over a real socket, with the real client ordering produced by `ackAuto`
rather than by a harness calling `sendAck` then `sendTelemetry`, and with the
retention bound driven by real emitted patches. That is the same distinction
§R4.2 drew for D-29: no in-process harness can say whether the **real server on
a real connection** admits the behaviour. Where it would be a duplicate is arm
2, which I kept because it is the arm that says BR-1's widening did not spend
H-11's defence, and dropping it would leave arms 1 and 3 with no statement
between them that a forged identifier still misses.

---

## R16. Mutation evidence

§6's rule applies here too: nothing in §R15 was reported green before it was
made to fail. Same method as §6, §R7 and §R10.3 — a pristine
`git archive 1864cf92` export per mutation under `/tmp/qa2-mut3/`, with only
this suite's own file overlaid, **never the shared worktree**. Every mutation is
of the **library**; a spec that has to be edited to go red is not a falsifier.

```bash
rm -rf /tmp/qa2-mut3/<name>
cp -a /tmp/qa2-mut3/pristine /tmp/qa2-mut3/<name>
python3 /tmp/qa2-mut3/mutate.py /tmp/qa2-mut3/<name> <name>
docker run --rm --cpuset-cpus=24-27 --memory=4g -v "/tmp/qa2-mut3/<name>:/w" \
    -w /w/gotth-live -e GOFLAGS=-buildvcs=false dis-gotth-live:latest \
    bash -c 'go test -v -count=1 -timeout 10m ./test/internal/chaos/ \
        -args "-ginkgo.focus=PRD case 8" -ginkgo.fail-on-empty'
```

Green first, so that a red means something:

```
the five case-8 specs against the pristine export, with this section's file:
Ran 5 of 42 Specs in 2.041 seconds
SUCCESS! -- 5 Passed | 0 Failed | 0 Pending | 37 Skipped    EXIT=0
```

| # | mutation | went red |
|---|---|---|
| **MH1** | `resync()`: BR-9's `applied = max(applied, a.lastSnapshotSeq)` floor removed | **H-14** — `20 replays of ONE cursor produced 3 full re-renders … Expected <int>: 3 to be <= <int>: 1`. This is also the measurement that **attributes** §R13.5's 3 → 0 rather than arguing it: the old number comes back when, and only when, the clamp goes away. The H-11 spec stayed green, correctly |
| **MH2** | `window.ack`: the eviction loop restored, i.e. **BR-1 un-fixed** | **H-11 arm 1 only** — `a report about a patch the client had just acknowledged was not used … Expected <int>: 0 to equal <int>: 1`. Also the second half of D-32's repro, above |
| **MH3** | `window.retentionSlots()` returns `1 << 30`, so the ring never ages anything out | **H-11 arm 3 only** — `a report about a patch pushed out of the retention bound was still resolved … Expected <float64>: 1 to be == <int>: 2` |
| **MH4** | `window.slotFor` resolves any identifier to the oldest retained slot | **H-11 arm 2 only** — `a forged patch identifier was resolved to a real slot rather than dropped and counted … Expected <float64>: 0 to be == <int>: 1` |
| **MH5** | `resync()`: the pre-BR-6 short circuit restored **verbatim**, on the raw cursor | **Nothing.** Recorded rather than dropped, as §6's seventeenth was. That circuit tests `m.lastAppliedSeq >= a.serverSeq`, and this spec's traffic is `last_applied_seq = 1` against `server_seq = 4`, so it never fires and the request reached the bucket anyway. A mis-aimed mutation: it un-fixed a code path this spec does not take |
| **MH5b** | `resync()`: the clamp **and** the no-op short circuit moved above `resyncBucket.allow` — which is what BR-6's defect *is*, once BR-9 is in the tree | **H-14** — `20 replayed resync requests did not reach the defined close … Expected <bool>: false to be true`. Twenty requests answered, none charged, no `4008` |

Each of the four arms added or re-pointed here has its own falsifier and **no
two arms share one**, which is the property that says the three H-11 arms are
three checks and not one check written three times. MH5's mis-aim is in the
table on purpose: a mutation that fails to redden anything is either a bad
mutation or a vacuous spec, and saying which is the work.

**The worktree was not modified by any of this.** Every mutation ran under
`/tmp/qa2-mut3/`; `git status --porcelain` over my file area held only
`case8_replay_test.go`, which is `34945818`.

---

## R17. PRD Phase 3 exit criteria at `1864cf92`

Rows that are not this suite's stay marked as such. Where I can see something
about one from here, it is an **observation with where I read it**, never a
grade — I may not grade another owner's row.

| criterion | verdict at `1864cf92` |
|---|---|
| Case 1 — dropped mid-patch → reconnect → resync → converges, no duplicated or lost effect | **MET.** `40 interactions sent, 24 patched before the cut, 10 committed, 0 duplicated, 14 patched-but-never-committed (D-25), truth=10 and the reconnected Snapshot matched it`. D-25's opportunistic number came back at **14** this run, against 13 at §4.1 and **0** at §R5.1 — which confirms §R5.1's correction rather than undoing it: the figure is an observation and not a check, and its value is a race |
| Case 2 — gap → resync rather than out-of-order (FR-11) | **MET, and unmoved despite BR-6 and BR-9 both landing on this exact path.** `gap at patch 3; superseded [4,5] of server_seq 6; 3 fragments re-rendered; truth=4` — byte for byte §R8's line. A no-op resync still costs an `Ack` and no `Snapshot`; what BR-6 added is that it now also costs a token |
| Case 3 — server restarted under load, within a stated bound | **MET.** SIGKILL of a real child process; bound 30 s, re-measured **611 ms** over one backoff attempt, port rebound in **4 ms**, ledger 29 → 362 distinct commits across the restart |
| Case 4 — slow client at a stated bandwidth (FR-51) | **MET.** 2,048 B/s; window at **16 of 16**, heap **169,664 B** of a 4 MiB budget, 42 slow-client events, 302 coalesced patches, 30,449 B downstream (2,030 B/s), other sessions unaffected, degraded, process alive. Eviction arm `closed with 4009 after 3.879s` against a 4 s bound. **D-26** unchanged and still open |
| Case 5 — event flood (FR-51) | **MET on three clauses of four.** **D-24** re-measured at **2,562 frames/s**, 60× the limit, `9,961 RATE_LIMITED` back, connection still open; heap 2,214,592 B of a 4 MiB budget. **D-22** at −50 and **D-28** at 1009/`normal`, both unchanged |
| Case 6 — partition and half-open (FR-12) | **MET.** Detection 3.5 s of a 3.5 s bound; reclamation **7.945 s** against an 8.5 s bound, which is **D-27**; goroutines 13 → 8; the bystander session kept being served |
| Case 7 — 10k churn, no goroutine/timer/heap leak (FR-22) | **MET.** Ten thousand *abnormal* cycles: goroutines 7 → 7, **−0.1 B/cycle** against a 902,144 B budget. On top of D-10's clean-close soak, **re-verified at HEAD in §R12.3** because C-34, BR-8 and `hijack.go` all landed in that package. The 300-cycle arm's 32.6 B/cycle is §R13.4 |
| Case 8 — duplicate/replayed frames | **MET, both clauses as they now read.** `one frame sent twice: state_version 2 -> 3, effect ran 2 times for ref 4242`. **The H-14 replay row's numbers moved, 3 snapshots → 0 (§R13.5), and the H-11 replay row was found asserting the reverse of its subject (D-32) and is now three assertions with three separate falsifiers.** PM-1's 2026-08-04 ruling on the second clause is untouched by any of this |
| Batching/debounce demonstrated, coalesced patch names every contributing event | **MET.** Case 4: `flush fired with a union of 64, largest union over the run 64`. QA3-1: margins **960/896/768/512/65** across the whole legal range, identical to §R5.2 in every cell. BR-4's `redefer` and U-3's restatement moved nothing here, for the reasons §R13.2 gives |
| Backpressure metrics exported (FR-34) | **MET for the queue set** — `gotthlive_outbound_window_depth` (16 of 16), `_patches_coalesced_total` (302) and `_slow_client_events_total` (42) all observed carrying real values. **D-22 is still a defect in the connection set.** Newly exercised from here: `gotthlive_client_telemetry_dropped_total` is now read in **both** directions by D-32's spec, and `gotthlive_client_morph_duration_seconds` is asserted to be emitted at all — which, before BR-1, it never was for any patch |
| Live dashboard example (FR-62) | **Not this suite's, owner DEV-3.** Observed: `examples/` was touched six times in this range — the `livetest.Client` migration, the bind-all Origin arm, REV-DUP §9's flake fix, `c1338120` and the `GinkgoTB()` migration. I did not build or run it and make no claim about it |
| Resync cost measured for the dashboard example | **Not this suite's, owner DEV-3 — and one thing to reconcile before the gate report quotes it.** `examples/dashboard/README.md`'s published `resync: min 97µs p50 163µs p90 259µs max 1.309ms (n=200)` is **byte-identical at `ce52d2f9` and at `1864cf92`**, while `examples/dashboard/resync.go` — the program that produces it — was rewritten by 141 lines in `c1338120` *because BR-9 made its old request unanswerable by a Snapshot*. That commit's own body says the measurement *"asked with a cursor it had already contradicted"* and that the frame it was timing *"is one a browser would have hung up on"*. So the published figure was produced by a request shape the fixed harness no longer sends, and it has not been re-taken. **Flagged for DEV-3, not graded and not edited: that file is not mine.** §R13.5 |
| Client runtime ≤ 12 KB gzipped | **Not this suite's, owner DEV-2.** Observed: `client/SIZE.md`'s gate row now reads **4,429 bytes** `gzip -9` against the 12,288 ceiling, up from the 4,360 §R8 quoted, with §1.1.4 attributing **+69** to the U-1/U-2 snapshot-boundary landing. The gate is met with more than 7 KB in hand. §R8's reconciliation note is now one step further out of date: `docs/qa/ci-intermittents.md`'s `fe9b6772` line still reads *"10,178 B against a 12,288 B ceiling, 64.5 % headroom"*, where 10,178 B is the **minified** size at the *resync re-arm* row and 64.5 % is `1 - 4360/12288`; at HEAD the gzip figure is 4,429 and the headroom is 64.0 %. Read from the ledger, not re-measured — the tool is `tools/minify -check`. **Still flagged rather than edited: not my file** |
| G2 baseline exists, RFC §6.2 corrected | **Not this suite's, owner DEV-1.** Observed: `docs/bench/g2-baseline.md` moved 625 lines inside this range and DEV-1's measurement campaign was running on this host while §R12's runs were taken. I read **no number** out of `docs/bench/`, following the same rule PM-1's closure ledger applied for the same reason. The half that was mine — **D-10** — is re-verified at HEAD in §R12.3, on both the heap and the RSS budget, with C-34, BR-8 and the hijack path underneath it |

---

## R18. Verdict line

**PASS. QA-2 clears the re-verification at `1864cf92`, and C-35(c) is
discharged.**

The eight Phase-3 cases were re-run at HEAD with `GOTTHLIVE_SOAK=1` and
`GOTTHLIVE_MEASURE=1`, on a clean export, and the suite is **42 of 42 green
under `-race` with both cost classes on**. All eight cases are MET. `internal/wsx`'s
D-10 soak was re-run because three of the named change-set items landed in that
package, and it is green on both the heap and the RSS budget. All three
Appendix-B measurements were re-taken by §7's own recipe.

**What the change set moved, of §R8's rows:** one. Case 8's H-14 replay row went
from **3 snapshots to 0**, and the cause is **BR-9's clamp** with **BR-6's
ordering** behind it — attributed by mutation (MH1 brings the 3 back and nothing
else does), not by argument, and independently corroborated by `c1338120`
diagnosing the same mechanism in `examples/dashboard`. Everything else that
could have moved did not, and §R13 says of each item which rows it could reach
and why it did not reach them. Three places where this suite **does not** cover
a change are stated as such rather than implied: BR-8's process-limit half
(`MaxSessions` is unbounded in every chaos configuration), BR-3's
`Commit`/`Discard` branch (its two reachable triggers were closed by D-23 and
BR-2), and D-4's rejection-reason ordering (no spec here asserts a reason label).

**One defect, and it is mine.** **D-32** — the H-11 spec asserted the reverse of
its own title after BR-1 and could not tell, demonstrated by the same unmodified
spec passing against both the library and its exact inverse. Fixed here in
`34945818` as three arms with three separate falsifiers, plus the H-14 spec's
stale premise and its no-longer-binding bound. **No new library defect was found
in this pass.**

**Still open, unchanged, and none of them a condition on this verdict:**

| | what | owner | state |
|---|---|---|---|
| **D-22** | `gotthlive_sessions_active` counts down on rejected handshakes | DEV-1 | MEDIUM, reproduced at −50 |
| **D-24** | FR-51's "defined close" reachable only above 300× the rate limit | DEV-1 | MEDIUM, reproduced at 2,562 frames/s |
| **D-25** | RFC §8.5 documents one direction of the at-most-once leak; its evidence here is a printed number | PM-1 + DEV-1 | LOW; the number came back at 14 this run, which is §R5.1's point, not a retraction of it |
| **D-26** | RFC §7.4's eviction cannot fire against a client that acknowledges | DEV-1 + L9-1/PM-1 | MEDIUM, both arms reproduce |
| **D-27** | reclamation is five seconds later than detection | DEV-1 | LOW, 7.945 s of an 8.5 s bound |
| **D-28** | close code `4007 FRAME_TOO_LARGE` is unreachable | DEV-1 | MEDIUM, 1009 to the client and `normal` to the operator |
| **D-31** | the resync retry schedule is protocol-visible and appears in neither RFC §7.6 nor §8.4 | PM-1 + L9-1, from DEV-2's source | LOW; the closure ledger carries it as **C-41** and L9-1 has taken it |

**Three things this section hands to other owners, none of which is mine to
close and none of which blocks:**

1. **DEV-3** — `examples/dashboard/README.md`'s resync-cost figure predates
   `c1338120`'s harness fix and was produced by a request shape the fixed
   harness no longer sends. §R17.
2. **PM-1 / instrumentation I6** — QA3-3's throughput row is host-dependent by a
   factor of two on the *same tree*, measured ABBA. The input I6 is waiting on
   is a range with its load attached (≈1.5 to ≈3 cores at D3's N = 1000), not
   the single figure §7.3 gave. §R14.3.
3. **`internal/protocol`'s owner (DEV-1)** — D-4's single walk makes the first
   violation *in field order* win, where two walks always found enum before
   list. Nothing in this suite asserts a rejection-reason label, so nothing here
   would catch a change in it. §R13.3.

**What this clears.** `docs/pm/checkpoint-3-closure.md` §8 item 3 and §2's row
**C-35(c)**: there is now a QA-2 sign-off covering `5a2ca417` and everything
after it, at `1864cf92`, naming which of §R8's rows the change set could move
and which one it did. It does **not** clear checkpoint 3, which is the gate
report's to write and needs the four rows in §R17 marked "not this suite's".

**And HEAD moved while this was being written, as it did in §R10.** `ead612c5`
landed L9-1's X3 ruling and ADR-002's acceptance — the closure ledger's §8 item
5 — and my own `34945818` sits on top of it. Neither touches anything this
section measures: `git diff 1864cf92 ead612c5 -- gotth-live/live
gotth-live/internal gotth-live/client gotth-live/proto gotth-live/test` is
**empty**, and that commit is four documents under `docs/adr/`, `docs/rfc/` and
`docs/reviews/`. **The graded verdict is about `1864cf92`.** With §8 item 5 now
ruled and item 3 discharged here, the three outstanding items the closure ledger
listed are down to none that a QA-2 or an L9-1 action closes.
