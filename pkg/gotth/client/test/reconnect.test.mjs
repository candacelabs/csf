// The reconnect state machine — RFC-0001 §8.4, §8.1, §8.2, §8.3 and §8.5.
//
// DEV-ONLY and quarantined, like every file in this directory: never served,
// never bundled, reachable only from the bench image, which is the one image
// in the project with node in it (PRD FR-74).
//
// Run:
//   docker run --rm -v "$PWD:/w" -w /w/gotth-live dis-gotth-live-bench:latest \
//       bash -c 'node --test client/test/reconnect.test.mjs'
//
// # What this file can check that a browser cannot, and why it is here
//
// The browser spec (test/internal/conformance/reconnect_test.go) cuts a real
// socket and watches a real page recover. It is the evidence that the whole
// thing works. What it cannot do is observe the SCHEDULE: a spec that measured
// wall-clock delays would be asserting on jitter, which is random by design,
// and it would take a quarter of an hour to reach the cap once. Nor can it
// easily reach the close codes a cooperating server never sends.
//
// So the two halves are split deliberately. Here: the arithmetic of §8.4's
// backoff against a stubbed Math.random and a fake clock, the visibility
// pause, every close code in and out of the terminal set, and the frames that
// do and do not leave the client across the boundary. There: focus, caret,
// <details> and an actual TCP cut.
//
// Every test drives the REAL runtime module. Nothing here re-implements the
// schedule — a test that computed its own expected delay from its own copy of
// the formula would pass against any implementation that had a formula.
//
// The environment — the fake clock, the fake socket and the document shim — is
// client/test/harness.mjs, shared with the resync suite so that two files
// cannot drift into testing two different systems.

import test from "node:test";
import assert from "node:assert/strict";

import { ResyncReason } from "../codec.gen.js";
import { harness, SESSION_A, SESSION_B } from "./harness.mjs";

// ---------------------------------------------------------------------------
// §8.4 — the schedule
// ---------------------------------------------------------------------------

test("the backoff is exponential with the base and cap RFC-0001 §8.4 fixes", async (t) => {
  // A fixed draw of exactly half the interval, so the sequence of BOUNDS is
  // readable straight off the delays: base·2^n, doubling, capped at 15 s.
  const h = await harness(t, { random: 0.5 });
  h.connect();

  const seen = [];
  for (let i = 0; i < 9; i++) {
    h.live().drop();
    assert.equal(h.clock.count(), 1, "a dropped connection armed no retry");
    seen.push(h.clock.delays()[0]);
    h.clock.fire(); // the attempt runs, the socket never opens, and it drops again
    h.live().drop();
    // Undo the extra attempt this loop step made, so `seen` reads as one
    // delay per attempt: the second drop above armed the next one already.
    seen.push(h.clock.delays()[0]);
    h.clock.fire();
  }

  const bounds = seen.map((d) => d * 2);
  assert.deepEqual(
    bounds.slice(0, 8),
    [250, 500, 1000, 2000, 4000, 8000, 15000, 15000],
    "the backoff bound sequence is not min(15000, 250·2^n)",
  );
  assert.ok(
    bounds.slice(8).every((b) => b === 15000),
    "the bound left the 15 s cap after eight attempts",
  );
});

test("the jitter is FULL: the whole interval is drawn from, starting at zero", async (t) => {
  // This is the assertion that tells full jitter from the two forms it is
  // most often confused with. Equal jitter is b/2 + random(0, b/2) and
  // decorrelated jitter is random(base, previous·3): both keep a FLOOR under
  // the delay, and the floor is exactly what puts a thousand reconnecting
  // tabs in a narrow band instead of spread across the window — which is the
  // remount storm §8.4 cites LiveView's RELOAD_JITTER for.
  const zero = await harness(t, { random: 0 });
  zero.connect();
  zero.live().drop();
  assert.equal(zero.clock.delays()[0], 0, "a draw of 0 must produce a delay of 0, not a floor");

  const most = await harness(t, { random: 0.99 });
  most.connect();
  most.live().drop();
  assert.equal(most.clock.delays()[0], 247.5, "the delay does not scale linearly across the whole interval");
});

test("reaching live resets the backoff; opening without a Snapshot does not", async (t) => {
  const h = await harness(t, { random: 0.5 });
  h.connect();

  // Three failures that never reach a Snapshot. A server that accepts the
  // connection and immediately closes it — a crash loop, a draining
  // instance — must not be handed 250 ms retries for ever.
  const growing = [];
  for (let i = 0; i < 3; i++) {
    h.live().drop();
    growing.push(h.clock.delays()[0]);
    h.clock.fire();
    h.live().accept(); // the handshake completes...
  }
  assert.deepEqual(growing, [125, 250, 500], "the schedule reset on a connection that never went live");

  // Now one that does reach live.
  h.live().deliver(h.snapshot(SESSION_B, 0));
  assert.equal(h.status(), "live");

  h.live().drop();
  assert.equal(h.clock.delays()[0], 125, "a session that reached live did not reset the schedule");
});

// ---------------------------------------------------------------------------
// §8.4 — paused while hidden, resumed immediately on becoming visible
// ---------------------------------------------------------------------------

test("a hidden tab holds no retry timer at all", async (t) => {
  const h = await harness(t, { random: 0.5 });
  h.connect();

  h.doc.visibilityState = "hidden";
  h.live().drop();

  assert.equal(h.status(), "reconnecting", "the status must still report the truth about the connection");
  assert.equal(h.clock.count(), 0, "a hidden tab armed a retry timer that would fire into a backgrounded page");
  assert.equal(h.sockets().length, 1, "a hidden tab opened a socket");
});

test("becoming visible reconnects immediately, not at the next scheduled tick", async (t) => {
  const h = await harness(t, { random: 0.5 });
  h.connect();

  h.doc.visibilityState = "hidden";
  h.live().drop();
  assert.equal(h.clock.count(), 0);

  h.visibility("visible");

  assert.equal(h.sockets().length, 2, "becoming visible did not connect");
  assert.equal(h.clock.count(), 0, "the resume went through a timer rather than connecting immediately");

  // And it is a real, usable connection, not a socket nobody wired up.
  h.live().accept();
  h.live().deliver(h.snapshot(SESSION_B, 0));
  assert.equal(h.status(), "live");
});

test("going hidden cancels a retry that was already armed", async (t) => {
  const h = await harness(t, { random: 0.5 });
  h.connect();

  h.live().drop();
  assert.equal(h.clock.count(), 1, "a visible tab must arm a retry");

  h.visibility("hidden");

  assert.equal(h.clock.count(), 0, "the armed retry survived the tab being hidden");
  assert.equal(h.sockets().length, 1);

  // And the tab still recovers when it comes back, from a state where the
  // only thing that could have reconnected it has been cancelled.
  h.visibility("visible");
  assert.equal(h.sockets().length, 2, "the tab could not recover after its retry was cancelled");
});

test("a visible tab that is already connected is not disturbed by a visibility change", async (t) => {
  const h = await harness(t, { random: 0.5 });
  const first = h.connect();

  h.visibility("hidden");
  h.visibility("visible");

  assert.equal(h.sockets().length, 1, "a live connection was replaced by a visibility change");
  assert.equal(h.live(), first);
  assert.equal(h.status(), "live");
});

// ---------------------------------------------------------------------------
// §8.4 — terminal close codes are never retried
// ---------------------------------------------------------------------------

// The two arms are tables rather than specs, because the interesting content
// is the membership of the list and nothing else: one assertion, many codes.
// Every code is from docs/protocol.md §8.3 plus the two the BROWSER produces
// on its own, which no server-side enumeration contains.
const terminal = [
  [4000, "NORMAL — the session ended cleanly and coming back would be a new one nobody asked for"],
  [4003, "UNSUPPORTED_VERSION — the same client will fail the same negotiation"],
  [4004, "UNAUTHENTICATED — the identity hook already said no"],
  [4005, "FORBIDDEN_ORIGIN — the origin allowlist will not change between attempts"],
  [4006, "UNAUTHORIZED — a fatal denial is fatal"],
];

for (const [code, why] of terminal) {
  test("close " + code + " is terminal and nothing revives it (" + why + ")", async (t) => {
    const h = await harness(t, { random: 0.5 });
    h.connect();

    h.live().drop(code);

    assert.equal(h.status(), "closed");
    assert.equal(h.clock.count(), 0, "a terminal close armed a retry");
    assert.equal(h.sockets().length, 1, "a terminal close reconnected");

    // Not by a visibility change either, which is the other way in.
    h.visibility("hidden");
    h.visibility("visible");
    assert.equal(h.sockets().length, 1, "a tab switch re-dialled a session the server refused");
    assert.equal(h.status(), "closed");
    assert.deepEqual(h.trail, ["connecting", "live", "closed"]);
  });
}

const retried = [
  [1006, "the abnormal close a browser reports for a connection that vanished"],
  [1001, "going away — the tab's own navigation, or a proxy shutting down"],
  [4001, "GOING_AWAY — the server is draining, and will come back"],
  [4002, "PROTOCOL_VIOLATION — a new session gets a new parse of everything"],
  [4007, "FRAME_TOO_LARGE"],
  [4008, "RATE_LIMITED — the budget refills"],
  [4009, "SLOW_CLIENT — the window is per session"],
  [4010, "HEARTBEAT_TIMEOUT — exactly the case reconnect exists for"],
  [4011, "SESSION_EVICTED — the idle timeout, and a new session is the remedy"],
  [4012, "INTERNAL_ERROR — a contained panic; the next session may be fine"],
  [4013, "RESYNC_FAILED"],
];

for (const [code, why] of retried) {
  test("close " + code + " is retried (" + why + ")", async (t) => {
    const h = await harness(t, { random: 0.5 });
    h.connect();

    h.live().drop(code);

    assert.equal(h.status(), "reconnecting");
    assert.equal(h.clock.count(), 1, "a recoverable close armed no retry");
    h.clock.fire();
    assert.equal(h.sockets().length, 2, "the retry did not open a connection");
  });
}

// ---------------------------------------------------------------------------
// §8.2 — what the user sees
// ---------------------------------------------------------------------------

test("data-gotth-status goes live → reconnecting → live across a drop", async (t) => {
  const h = await harness(t, { random: 0.5 });
  h.connect();

  h.live().drop();
  assert.equal(h.status(), "reconnecting");

  h.clock.fire();
  h.live().accept();
  h.live().deliver(h.snapshot(SESSION_B, 0));

  assert.deepEqual(h.trail, ["connecting", "live", "reconnecting", "live"], "§8.2's transition sequence is wrong");
});

test("a retry attempt does not flicker the status back to connecting", async (t) => {
  const h = await harness(t, { random: 0.5 });
  h.connect();

  for (let i = 0; i < 3; i++) {
    h.live().drop();
    h.clock.fire();
  }

  assert.equal(h.status(), "reconnecting");
  assert.deepEqual(h.trail, ["connecting", "live", "reconnecting", "reconnecting", "reconnecting"]);
  assert.ok(!h.trail.slice(2).includes("connecting"), "the user saw the page flicker back to connecting on a retry");
});

test("the DOM is frozen at the last applied patch while reconnecting, and nothing is disabled", async (t) => {
  const h = await harness(t, { random: 0.5 });
  h.connect();
  h.live().deliver(h.patch(SESSION_A, 2, 7));

  const before = h.el('[data-gotth-region="rc.panel"]').outerHTML;
  assert.ok(before.includes("tick 7"), "the fixture never applied the patch it is meant to freeze");

  h.live().drop();
  h.clock.fire(); // an attempt that does not complete: the worst case for the DOM

  assert.equal(
    h.el('[data-gotth-region="rc.panel"]').outerHTML,
    before,
    "the DOM changed while the connection was down",
  );

  // §8.2: "remains fully interactive for HTMX and native controls. Live
  // controls are not disabled by the library." This asserts the negative
  // directly, because it is the change somebody makes with the best of
  // intentions — an application that WANTS a disabled look styles off the
  // status attribute, which is why the attribute is there.
  assert.equal(h.root.querySelectorAll("[disabled]").length, 0, "the library disabled a control while reconnecting");
  assert.equal(h.root.querySelectorAll("[aria-disabled]").length, 0);
  assert.equal(h.root.querySelectorAll("[inert]").length, 0);

  // And the delegated listener is still attached, so the page is not merely
  // undisabled but actually live to input.
  h.emit("click", h.el("#inc"));
  assert.equal(h.status(), "reconnecting", "a click during reconnect changed the connection state");
});

// ---------------------------------------------------------------------------
// §8.1 — a reconnect is a NEW SESSION
// ---------------------------------------------------------------------------

test("nothing at all is sent on a new connection before its first Snapshot", async (t) => {
  const h = await harness(t, { random: 0.5 });
  h.connect();

  h.live().drop();
  h.clock.fire();
  const second = h.live().accept();

  // A heartbeat before the Snapshot. The runtime echoes heartbeats, and an
  // echo written with the PREVIOUS session's id is protocol.md H-3 — the
  // server answers it by closing 4002, so a client that carried its old id
  // over would fail every reconnect at the first heartbeat.
  second.deliver({ protocol_version: 1, session_id: SESSION_A, heartbeat: { nonce: 9, interval_ms: 1000 } });

  assert.deepEqual(second.sent, [], "the client wrote to a connection whose session it does not yet know (H-10)");

  // Once the Snapshot names the new session, the echo works again.
  second.deliver(h.snapshot(SESSION_B, 0));
  second.deliver({ protocol_version: 1, session_id: SESSION_B, heartbeat: { nonce: 9, interval_ms: 1000 } });

  const beats = second.kind("heartbeat");
  assert.equal(beats.length, 1);
  assert.deepEqual([...beats[0].session_id], [...SESSION_B], "an echo named the wrong session");
});

test("an event raised between the socket opening and the Snapshot is not sent", async (t) => {
  const h = await harness(t, { random: 0.5 });
  h.connect();
  h.live().deliver(h.patch(SESSION_A, 2, 1)); // the old session reached server_seq 2

  h.live().drop();
  h.clock.fire();
  const second = h.live().accept();

  h.emit("click", h.el("#inc"));

  assert.deepEqual(
    second.kind("event"),
    [],
    "an event went out citing a seen_server_seq from a session that no longer exists",
  );
});

test("client_ref restarts at 1 on the new connection (protocol.md §4.1)", async (t) => {
  const h = await harness(t, { random: 0.5 });
  const first = h.connect();

  h.emit("click", h.el("#inc"));
  h.emit("click", h.el("#inc"));
  assert.deepEqual(
    first.kind("event").map((f) => f.event.client_ref),
    [1, 2],
  );

  h.live().drop();
  h.clock.fire();
  const second = h.live().accept();
  second.deliver(h.snapshot(SESSION_B, 0));

  h.emit("click", h.el("#inc"));

  const ev = second.kind("event");
  assert.equal(ev.length, 1);
  assert.equal(ev[0].event.client_ref, 1, "client_ref is monotonic from 1 PER CONNECTION, and it carried over");
  assert.equal(ev[0].event.seen_server_seq, 1, "the event cites a server_seq the new session never reached");
  assert.deepEqual([...ev[0].session_id], [...SESSION_B]);
});

test("the new session's server_seq restarting at 1 is not read as a gap", async (t) => {
  const h = await harness(t, { random: 0.5 });
  h.connect();
  for (const seq of [2, 3, 4, 5]) h.live().deliver(h.patch(SESSION_A, seq, seq));
  assert.equal(h.live().kind("ack").pop().ack.server_seq, 5);

  h.live().drop();
  h.clock.fire();
  const second = h.live().accept();
  second.deliver(h.snapshot(SESSION_B, 0));
  second.deliver(h.patch(SESSION_B, 2, 1));

  assert.deepEqual(second.kind("resync_request"), [], "the client asked for a resync across a clean reconnect");
  assert.equal(h.status(), "live");
  assert.equal(second.kind("ack").pop().ack.server_seq, 2, "the new session's patches were not applied");
});

test("a gap outstanding when the socket dropped does not latch across the reconnect", async (t) => {
  const h = await harness(t, { random: 0.5 });
  const first = h.connect();

  // A gap on the old connection: seq 1 was applied, seq 3 arrives.
  first.deliver(h.patch(SESSION_A, 3, 3));
  assert.equal(first.kind("resync_request").length, 1, "the fixture did not produce a gap");

  // ...and the connection dies before the server can answer it.
  first.drop();
  h.clock.fire();
  const second = h.live().accept();
  second.deliver(h.snapshot(SESSION_B, 0));

  // A gap on the NEW connection, where a live actor can answer it.
  second.deliver(h.patch(SESSION_B, 3, 9));

  const asked = second.kind("resync_request");
  assert.equal(asked.length, 1, "a gap on the new connection was detected and never reported: the flag latched");
  assert.equal(asked[0].resync_request.last_applied_seq, 1);
  assert.equal(asked[0].resync_request.reason, ResyncReason.GAP);
});

test("the reconnect Snapshot is morphed into the frozen DOM, not replacing it (§8.3)", async (t) => {
  const h = await harness(t, { random: 0.5 });
  h.connect();
  h.live().deliver(h.patch(SESSION_A, 2, 4));

  // The state that only survives if the node survives. Node identity is the
  // mechanism behind every case in FR-25 — focus, caret, scroll, media
  // position — so asserting identity here asserts the mechanism, and the
  // browser spec confirms the effect.
  const line = h.el("#line");
  const draft = h.el("#draft");
  draft.value = "half-typed";
  line.__qaMark = "before the drop";

  h.live().drop();
  h.clock.fire();
  const second = h.live().accept();

  // The new session Mounts from scratch, so it renders tick 0 — the DOM says
  // 4. The morph therefore really does have to change this subtree.
  second.deliver(h.snapshot(SESSION_B, 0));

  assert.equal(h.el("#line").textContent, "tick 0", "the reconnect Snapshot was not applied");
  assert.equal(h.el("#line"), line, "the Snapshot REPLACED the live node instead of morphing it");
  assert.equal(h.el("#line").__qaMark, "before the drop");
  assert.equal(h.el("#draft"), draft);
  assert.equal(h.el("#draft").value, "half-typed", "an uncontrolled input lost what the user typed across a reconnect");
});

// ---------------------------------------------------------------------------
// §8.5 — events are at-most-once
// ---------------------------------------------------------------------------

test("an event attempted while disconnected is dropped, never queued for the new session", async (t) => {
  // This is the spec that exists to go red when somebody adds a retry queue,
  // which is the single most likely well-intentioned change to this file.
  // §8.5 is explicit: the client does NOT retry an event that was
  // unacknowledged when the connection dropped. A click during a network
  // failure may be lost, the user sees server truth after the resync, and the
  // alternative pushes idempotency into every application reducer (R-12).
  const h = await harness(t, { random: 0.5 });
  const first = h.connect();

  h.emit("click", h.el("#inc")); // this one goes out
  assert.equal(first.kind("event").length, 1);

  first.drop();

  // Three clicks with nowhere to go.
  h.emit("click", h.el("#inc"));
  h.emit("click", h.el("#inc"));
  h.emit("click", h.el("#inc"));

  h.clock.fire();
  const second = h.live().accept();
  second.deliver(h.snapshot(SESSION_B, 0));

  assert.deepEqual(second.kind("event"), [], "an event raised while disconnected was replayed onto the new session");

  // Nothing arrives later, either: no queue drains on the next patch.
  second.deliver(h.patch(SESSION_B, 2, 1));
  assert.deepEqual(second.kind("event"), []);

  // ...and none rides out on the back of the next real click, which is the
  // other place a queue would naturally drain. Exactly one event, and its
  // client_ref is 1: a drained queue would make this the fourth.
  h.emit("click", h.el("#inc"));
  const after = second.kind("event");
  assert.equal(after.length, 1, "the clicks raised while disconnected drained behind the next real one");
  assert.equal(after[0].event.client_ref, 1, "client_ref counted the events that were never sent");
});

test("an event that was in flight when the socket dropped is not re-sent", async (t) => {
  const h = await harness(t, { random: 0.5 });
  const first = h.connect();

  h.emit("click", h.el("#inc"));
  const inFlight = first.kind("event")[0];
  assert.equal(inFlight.event.client_ref, 1);

  // The drop happens before any patch acknowledges it. The server may or may
  // not have reduced it; §8.5 says the client does not find out and does not
  // ask again.
  first.drop();
  h.clock.fire();
  const second = h.live().accept();
  second.deliver(h.snapshot(SESSION_B, 0));

  assert.deepEqual(second.kind("event"), [], "the unacknowledged event was re-sent, making events at-least-once");
});

// ---------------------------------------------------------------------------
// FR-11 — the gap trigger, which reconnect must not have disturbed
// ---------------------------------------------------------------------------
//
// §8.3's two triggers are one code path, and the reconnect work is the second
// one. These are the first: they had no node-level spec before this file, and
// without them "unchanged" would be an assertion about a path nothing here
// executes.

test("a gap stops the client applying and produces one ResyncRequest naming the last applied seq", async (t) => {
  const h = await harness(t, { random: 0.5 });
  const sock = h.connect();
  sock.deliver(h.patch(SESSION_A, 2, 2));

  const frozen = h.el("#line").textContent;

  sock.deliver(h.patch(SESSION_A, 4, 4)); // 3 never arrived

  assert.equal(h.el("#line").textContent, frozen, "the client applied a patch from beyond a gap");

  const asked = sock.kind("resync_request");
  assert.equal(asked.length, 1);
  assert.equal(asked[0].resync_request.last_applied_seq, 2);
  assert.equal(asked[0].resync_request.reason, ResyncReason.GAP);

  // One request per gap, not one per frame: the server's resync budget is
  // three per second with a one-second minimum interval (protocol.md H-14).
  sock.deliver(h.patch(SESSION_A, 5, 5));
  sock.deliver(h.patch(SESSION_A, 6, 6));
  assert.equal(sock.kind("resync_request").length, 1, "a second gap frame produced a second request");

  // The answering Snapshot re-arms the detector, which is what makes the gap
  // path usable more than once per connection.
  sock.deliver(h.snapshot(SESSION_A, 6, 7));
  assert.equal(h.el("#line").textContent, "tick 6");
  sock.deliver(h.patch(SESSION_A, 9, 9));
  assert.equal(sock.kind("resync_request").length, 2, "the detector did not re-arm after the resync Snapshot");
});
