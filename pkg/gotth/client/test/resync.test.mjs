// The gap latch and its retry — QA-2's D-29, FR-11, RFC-0001 §7.6 and §8.4.
//
// DEV-ONLY and quarantined, like every file in this directory: never served,
// never bundled, reachable only from the bench image, which is the one image
// in the project with node in it (PRD FR-74).
//
// Run:
//   docker run --rm -v "$PWD:/w" -w /w/gotth-live dis-gotth-live-bench:latest \
//       bash -c 'node --test client/test/resync.test.mjs'
//
// # The defect these specs hold closed
//
// A gap latches the client: it stops applying and asks for a Snapshot. The
// latch was cleared in exactly one place — applied() — and RFC §7.6 gives the
// server an answer that goes through neither Patch nor Snapshot: a request
// over the resync budget is refused with Error{RATE_LIMITED} and no render. So
// the client stayed latched, discarded every later patch, and stopped
// acknowledging as a side effect of having stopped applying; the server's
// outbound window filled and §7.4's slow-client eviction closed the connection
// about thirty seconds later. The page was stale for those thirty seconds and
// the recovery was the eviction doing a job nobody assigned it.
//
// QA-2 measured the arrival rate: 20–25 % of LEGITIMATE resync requests are
// refused at 5–25 % patch loss on a 53 Hz stream, because losses cluster
// inside the one-second minimum interval (checkpoint-3-chaos.md §7.2).
//
// # What a browser cannot check, which is why these are here
//
// The same split as the reconnect suite. A browser spec can watch a real page
// recover, but it cannot observe the SCHEDULE without asserting on jitter and
// waiting out fifteen-second delays, and it cannot easily make a cooperating
// server refuse a request that is not abusive. Here: the delays as ARGUMENTS
// to setTimeout against a stubbed Math.random, the frames that do and do not
// leave the client while it is latched, and the two schedules — resync retry
// and reconnect backoff — proved not to disturb each other.
//
// Every test drives the REAL runtime module through the shared environment in
// client/test/harness.mjs. Nothing here re-implements the schedule: a test
// that computed its expected delay from its own copy of the formula would pass
// against any implementation that had a formula.

import test from "node:test";
import assert from "node:assert/strict";

import { ErrorCode, PatchOp, ResyncReason } from "../codec.gen.js";
import { harness, panel, SESSION_A, SESSION_B } from "./harness.mjs";

// refusal is what RFC §7.6 says the server sends: Error{RATE_LIMITED}, not
// fatal, no render. The causal ids are the ones the server really uses — a
// ResyncRequest carries no client_ref of its own, so the actor stands its
// server-minted event id in for both (internal/session.ingressResync), which
// is exactly why the client cannot correlate this frame to its own request and
// keys off its own latch instead.
function refusal(session, eventID) {
  return {
    protocol_version: 1,
    session_id: session,
    error: {
      code: ErrorCode.RATE_LIMITED,
      message: "resync requests are rate limited: wait before requesting another",
      event_id: eventID === undefined ? 7 : eventID,
      client_ref: eventID === undefined ? 7 : eventID,
      fatal: false,
    },
  };
}

// granted is the answering Snapshot, carrying the supersession range protocol
// H-13 requires of one: both fields non-zero, from <= through < server_seq.
function granted(session, seq, tick, from) {
  return {
    protocol_version: 1,
    session_id: session,
    snapshot: {
      server_seq: seq,
      patch_id: seq,
      transition_id: seq,
      state_version: seq,
      superseded_from_seq: from,
      superseded_through_seq: seq - 1,
      updates: [{ fragment_id: "rc.panel", op: PatchOp.MORPH, html: panel(tick) }],
    },
  };
}

// latched brings a session to the state every case here is about: one patch
// applied, a gap detected, one request out, and nothing yet answering it.
async function latched(t, opts) {
  const h = await harness(t, opts);
  const sock = h.connect();
  sock.deliver(h.patch(SESSION_A, 2, 2)); // applied; seq = 2
  sock.deliver(h.patch(SESSION_A, 4, 4)); // 3 never arrived
  assert.equal(sock.kind("resync_request").length, 1, "the fixture did not produce a gap");
  assert.equal(h.el("#line").textContent, "tick 2", "the fixture applied a patch from beyond the gap");
  return [h, sock];
}

// ---------------------------------------------------------------------------
// The refusal is answered by a retry, and not by the slow-client eviction
// ---------------------------------------------------------------------------

test("a refused resync is retried, and the page recovers on the same connection", async (t) => {
  // The whole of D-29 in one case. Before the fix the refusal armed nothing:
  // the client stayed latched until the server's outbound window filled and
  // the eviction closed the socket, and the page was stale for that whole
  // time. The recovery asserted here uses neither the close nor a reconnect.
  const [h, sock] = await latched(t, { random: 0 });

  sock.deliver(refusal(SESSION_A));

  assert.equal(h.clock.count(), 1, "a refused resync armed no retry: the client is waiting to be evicted");
  h.clock.fire();

  const asked = sock.kind("resync_request");
  assert.equal(asked.length, 2, "the retry did not send a second request");
  assert.equal(asked[1].resync_request.last_applied_seq, 2, "the retry named a sequence the client has not applied");
  assert.equal(asked[1].resync_request.reason, ResyncReason.GAP);

  // The server grants this one: seq 3–5 are superseded by a snapshot at 6.
  sock.deliver(granted(SESSION_A, 6, 6, 3));

  assert.equal(h.el("#line").textContent, "tick 6", "the answering Snapshot was not applied");
  assert.equal(sock.kind("ack").pop().ack.server_seq, 6, "the client did not acknowledge the Snapshot");
  assert.equal(h.status(), "live");

  // And the recovery was not the eviction path: no close, one socket, no
  // re-mount. This is the assertion that distinguishes the fix from what the
  // library already did.
  assert.equal(sock.closedWith, null, "the client closed the connection to recover");
  assert.equal(h.sockets().length, 1, "the recovery went through a reconnect, which is the eviction path");
  assert.ok(
    !h.trail.includes("reconnecting") && !h.trail.includes("closed"),
    "the user was shown a disconnection to recover from a refusal that cost no render: " + h.trail.join(" → "),
  );
});

test("the refusal re-arms the request, not the detector: patches are still discarded until the Snapshot", async (t) => {
  // Re-arming must not be mistaken for recovery. The client is still missing
  // seq 3, so it must still refuse to apply anything past it — otherwise the
  // fix for a stale page is a DOM state no server render ever produced.
  const [h, sock] = await latched(t, { random: 0 });

  sock.deliver(refusal(SESSION_A));
  sock.deliver(h.patch(SESSION_A, 5, 5));
  sock.deliver(h.patch(SESSION_A, 6, 6));

  assert.equal(h.el("#line").textContent, "tick 2", "the client applied past the gap after the refusal");
  assert.equal(sock.kind("resync_request").length, 1, "a patch arriving while a retry is armed sent a second request");
  assert.equal(h.clock.count(), 1, "the armed retry was replaced by a second one");

  h.clock.fire();
  const asked = sock.kind("resync_request");
  assert.equal(asked.length, 2);
  assert.equal(asked[1].resync_request.last_applied_seq, 2, "the retry moved on without applying anything");
});

// ---------------------------------------------------------------------------
// Acknowledgements keep flowing while the client is latched
// ---------------------------------------------------------------------------

test("a patch discarded because of the gap is still acknowledged, at the sequence the client holds", async (t) => {
  // The client stopped acknowledging as a CONSEQUENCE of having stopped
  // applying, because the ack was written by applied() and nothing else. That
  // is what made a latched client indistinguishable from a dead one.
  const [h, sock] = await latched(t, { random: 0 });

  sock.deliver(refusal(SESSION_A));
  sock.deliver(h.patch(SESSION_A, 5, 5));
  sock.deliver(h.patch(SESSION_A, 6, 6));

  // The whole session's acks: the mount Snapshot at 1, the patch at 2, then
  // one for each of the three patches that were received and discarded — the
  // gap frame itself and the two after the refusal — every one of them naming
  // the sequence the client actually holds.
  assert.deepEqual(
    sock.kind("ack").map((f) => f.ack.server_seq),
    [1, 2, 2, 2, 2],
    "a latched client went silent: it owes one ack per patch received, naming what it applied",
  );
});

test("the acks a latched client sends are legal: never backwards, never a patch it did not apply", async (t) => {
  // protocol.md H-7 closes the connection on an acknowledgement that goes
  // backwards or names a sequence the server never emitted, so "keep
  // acknowledging" has to mean the high-water mark and nothing else.
  const [h, sock] = await latched(t, { random: 0 });
  const telemetry = sock.kind("client_telemetry").length;
  const before = sock.kind("ack").length;

  for (const seq of [5, 6, 7, 8]) sock.deliver(h.patch(SESSION_A, seq, seq));

  const seqs = sock.kind("ack").map((f) => f.ack.server_seq);
  // Stated first, because "every ack is legal" is true of a client that sends
  // none, and a check that cannot fail is this project's recurring defect.
  assert.ok(seqs.length > before, "the legality assertion below is vacuous: the latched client sent no acks at all");
  assert.ok(
    seqs.every((s, i) => s > 0 && s <= 2 && (i === 0 || s >= seqs[i - 1])),
    "an ack went backwards, named a patch the client never applied, or was zero: " + seqs.join(","),
  );
  assert.equal(
    sock.kind("client_telemetry").length,
    telemetry,
    "the client reported a morph duration for a patch it discarded",
  );
});

// ---------------------------------------------------------------------------
// The schedule is bounded — the server refused for a reason
// ---------------------------------------------------------------------------

test("the retry schedule has a floor: the unluckiest draw still waits", async (t) => {
  // The one place this runtime's two schedules deliberately disagree. §8.4's
  // reconnect uses FULL jitter, drawing from zero, to spread a herd of tabs
  // that one server-side event disconnected together. A refused resync has no
  // herd — the resync bucket is per session — and a delay of zero is exactly
  // the request the server has just declined. A draw of 0 must therefore still
  // produce a wait.
  const [h, sock] = await latched(t, { random: 0 });

  sock.deliver(refusal(SESSION_A));

  assert.equal(h.clock.delays()[0], 500, "the unluckiest draw retried immediately into the budget that refused it");
});

test("the delay at least doubles per refusal and stops at the reconnect schedule's own cap", async (t) => {
  const [h, sock] = await latched(t, { random: 0 });

  const seen = [];
  for (let i = 0; i < 7; i++) {
    sock.deliver(refusal(SESSION_A));
    assert.equal(h.clock.count(), 1, "refusal " + i + " left more than one retry armed");
    seen.push(h.clock.delays()[0]);
    h.clock.fire();
  }

  assert.deepEqual(
    seen,
    [500, 1000, 2000, 4000, 7500, 7500, 7500],
    "the floor sequence is not min(15000, 1000·2^n)/2, capped where §8.4's reconnect caps",
  );
  assert.ok(
    seen.every((d, i) => i === 0 || d >= seen[i - 1]),
    "a later refusal was retried sooner than an earlier one",
  );
});

test("n refusals cost exactly n retries: one request in flight per gap, ever", async (t) => {
  // The bound that matters to the server. Nine consecutive denials close the
  // connection 4008 (3 × ResyncBurst, internal/session.resync), so a client
  // that answered a refusal by shouting would turn a rate limit into a close.
  const [h, sock] = await latched(t, { random: 0 });

  for (let i = 0; i < 6; i++) {
    sock.deliver(refusal(SESSION_A));
    // A second refusal arriving before the armed retry fires — the server
    // answering a request the client has not made yet cannot happen, but a
    // duplicated frame can, and it must not double the rate.
    sock.deliver(refusal(SESSION_A));
    assert.equal(h.clock.count(), 1, "two refusals armed two retries");
    h.clock.fire();
  }

  assert.equal(sock.kind("resync_request").length, 7, "the client sent more requests than it was refused");
  assert.equal(h.clock.count(), 0, "a retry that has fired is still armed");
});

test("the jitter spreads the retry across the upper half of the interval", async (t) => {
  // Equal jitter: bound/2 + random(0, bound/2). The two ends are asserted
  // together, because a schedule that ignored Math.random entirely would pass
  // either one alone.
  const [low, lowSock] = await latched(t, { random: 0 });
  lowSock.deliver(refusal(SESSION_A));
  assert.equal(low.clock.delays()[0], 500);

  const [high, highSock] = await latched(t, { random: 0.99 });
  highSock.deliver(refusal(SESSION_A));
  assert.equal(high.clock.delays()[0], 995, "the delay does not scale across the upper half of the interval");
});

test("an answered resync resets the schedule, so a later gap starts at the base again", async (t) => {
  // Without this a long-lived session that was refused four times in the
  // morning would wait 7.5 s before its first retry in the afternoon.
  const [h, sock] = await latched(t, { random: 0 });

  sock.deliver(refusal(SESSION_A));
  h.clock.fire();
  sock.deliver(refusal(SESSION_A));
  assert.equal(h.clock.delays()[0], 1000, "the schedule did not grow across two refusals");
  h.clock.fire();

  sock.deliver(granted(SESSION_A, 6, 6, 3));

  sock.deliver(h.patch(SESSION_A, 9, 9)); // a fresh gap, later in the same session
  sock.deliver(refusal(SESSION_A));
  assert.equal(h.clock.delays()[0], 500, "a new gap inherited the previous gap's backoff step");
});

test("a Snapshot that lands while a retry is armed disarms it", async (t) => {
  // The interleaving is constructed: today's actor answers one request with
  // one frame, so a Snapshot cannot overtake the refusal that armed this
  // timer. The invariant is the client's own and worth holding anyway —
  // never ask for a gap that is closed — and holding it in a spec is what
  // stops the disarm in applied() from being a line that could be deleted
  // with nothing going red, which is exactly what QA-1's D-21 was.
  const [h, sock] = await latched(t, { random: 0 });

  sock.deliver(refusal(SESSION_A));
  assert.equal(h.clock.count(), 1, "the fixture armed no retry");

  sock.deliver(granted(SESSION_A, 6, 6, 3));

  assert.equal(h.clock.count(), 0, "a retry stayed armed for a gap the Snapshot had already closed");
  assert.equal(h.el("#line").textContent, "tick 6");
  assert.equal(sock.kind("resync_request").length, 1, "a request went out after the gap was closed");
});

// ---------------------------------------------------------------------------
// Narrowness: what the client does NOT act on
// ---------------------------------------------------------------------------

test("RATE_LIMITED with no gap outstanding does nothing: an event flood cannot become a resync flood", async (t) => {
  // The other frame this error answers is an ordinary Event over the event
  // bucket, and its ids are indistinguishable from a refused resync's. A
  // client that acted on the code alone would answer "you are sending too
  // much" by sending the most expensive frame in the protocol.
  const h = await harness(t, { random: 0 });
  const sock = h.connect();
  sock.deliver(h.patch(SESSION_A, 2, 2));

  sock.deliver(refusal(SESSION_A, 1));
  sock.deliver(refusal(SESSION_A, 2));

  assert.equal(h.clock.count(), 0, "a rate-limited event armed a resync retry");
  assert.deepEqual(sock.kind("resync_request"), [], "a rate-limited event produced a resync request");
});

test("an error that is not RATE_LIMITED arms nothing, and every error still reaches the application", async (t) => {
  // RATE_LIMITED is the one code that means "not now" rather than "not ever".
  // The CustomEvent contract is unchanged for all of them, which is the half
  // an embedding application can see.
  const [h, sock] = await latched(t, { random: 0 });

  sock.deliver({
    protocol_version: 1,
    session_id: SESSION_A,
    error: { code: ErrorCode.UNKNOWN_FRAGMENT, message: "no such fragment", event_id: 4, client_ref: 4, fatal: false },
  });
  assert.equal(h.clock.count(), 0, "an unrelated error armed a resync retry");

  sock.deliver(refusal(SESSION_A));
  assert.equal(h.clock.count(), 1);

  assert.deepEqual(
    h.dispatched.map((e) => e.type + ":" + e.detail.code),
    ["gotth-live:error:" + ErrorCode.UNKNOWN_FRAGMENT, "gotth-live:error:" + ErrorCode.RATE_LIMITED],
    "the runtime stopped raising gotth-live:error, or raised it for only some codes",
  );
});

// ---------------------------------------------------------------------------
// Composition with §8.4 — the reconnect machinery is not disturbed
// ---------------------------------------------------------------------------

test("an armed resync retry does not disturb the reconnect backoff, and does not survive the reconnect", async (t) => {
  const h = await harness(t, { random: 0.5 });
  const first = h.connect();
  first.deliver(h.patch(SESSION_A, 3, 3)); // gap
  first.deliver(refusal(SESSION_A));

  assert.deepEqual(h.clock.delays(), [750], "the resync retry is not armed at bound/2 + random(0, bound/2)");

  first.drop();
  assert.deepEqual(
    h.clock.delays(),
    [750, 125],
    "the reconnect draw is not §8.4's own random(0, min(15000, 250·2^n)) with both schedules live",
  );

  // The reconnect timer is the one that matters now; fire until it opens.
  while (h.sockets().length < 2) h.clock.fire();
  assert.equal(h.clock.count(), 0, "a retry armed for the old session survived into the new one");

  const second = h.live().accept();
  second.deliver(h.snapshot(SESSION_B, 0));
  second.deliver(h.patch(SESSION_B, 2, 1));

  assert.deepEqual(second.kind("resync_request"), [], "the new session was asked to resync a gap it never had");
  assert.equal(h.status(), "live");
});

test("becoming visible pulls an armed retry forward; a tab switched with nothing armed sends nothing", async (t) => {
  // §8.4's resume, applied to the schedule that can use it. The reconnect
  // timer is cancelled while hidden because firing it would OPEN a socket into
  // a backgrounded page; this one writes a small frame on a socket that is
  // already open, so it stays armed and is merely pulled forward. The second
  // half is the flood guard: visibility can accelerate a retry, never invent
  // one, so alt-tabbing is not a way to bypass the schedule.
  const [h, sock] = await latched(t, { random: 0 });
  sock.deliver(refusal(SESSION_A));

  h.visibility("hidden");
  assert.equal(h.clock.count(), 1, "hiding the tab cancelled the retry, leaving nothing to recover the page");

  h.visibility("visible");
  assert.equal(sock.kind("resync_request").length, 2, "becoming visible did not pull the armed retry forward");
  assert.equal(h.clock.count(), 0, "the retry fired and stayed armed");

  h.visibility("hidden");
  h.visibility("visible");
  h.visibility("hidden");
  h.visibility("visible");
  assert.equal(sock.kind("resync_request").length, 2, "a visibility flap with nothing armed sent a request anyway");
});
