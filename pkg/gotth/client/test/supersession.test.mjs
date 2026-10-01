// The snapshot boundary the client is told about — H-13, protocol.md §4.3,
// and the two REV-INV findings that said nobody was reading it (U-1, U-2).
//
// DEV-ONLY and quarantined, like every file in this directory: never served,
// never bundled, reachable only from the bench image, which is the one image
// in the project with node in it (PRD FR-74).
//
// Run:
//   docker run --rm -v "$PWD:/w" -w /w/gotth-live dis-gotth-live-bench:latest \
//       bash -c 'node --test client/test/supersession.test.mjs'
//
// # What was missing
//
// H-13's enforcement column names `protocol.validateSnapshot` "on both the
// outbound boundary and THE CLIENT DECODER". The generated codec decoded
// `superseded_from_seq` and `superseded_through_seq` — fields 10 and 11 — and
// the runtime read neither: `onMessage` went straight to `applied(f.snapshot)`
// and `applied()` set `seq = p.server_seq` with no relation asserted to what
// the client actually held. So the normative table claimed an enforcement that
// shipped in one of the two places it named, and a Snapshot could move the
// client's high-water mark backwards or replace a range that did not meet the
// one the client stopped at, unnoticed either way.
//
// # Why the client checking is worth bytes at all
//
// The server is where enforcement is total, and these specs do not pretend
// otherwise. Two things make the client's copy pay for itself:
//
//  1. It names the error. A Snapshot that moves `seq` backwards makes the next
//     ack go backwards, and H-7 closes THAT as 4002 — the session ends either
//     way, but without this check it ends citing the client's ack instead of
//     the server frame that caused it, and an operator reads the wrong log.
//  2. It is the only place the range is checked against what the client
//     actually applied. §4.3 says the range exists to answer "which events
//     produced the DOM I am looking at"; only this side knows where the DOM
//     stopped. `validateSnapshot` checks the range internally — H-13's
//     `from <= through < server_seq` — and cannot check it against a client
//     state it does not hold. (REV-INV BR-9 is the server-side half: a `from`
//     clamped to `max(last_applied, acked) + 1`.)
//
// Every test drives the REAL runtime module through client/test/harness.mjs.
// The frames are built here rather than in the harness because they are the
// subject: a helper that could not express an illegal range would be a helper
// that could only test the happy path.

import test from "node:test";
import assert from "node:assert/strict";

import { OriginKind, PatchOp, ResyncReason } from "../codec.gen.js";
import { harness, panel, SESSION_A } from "./harness.mjs";

// snapshot is the frame `resync()` in internal/session builds: a Snapshot at
// `server_seq`, carrying the inclusive range it replaces and the RESYNC origin
// H-13 pairs with a non-zero range. `from`/`through` are written verbatim,
// including 0 — the encoder omits a zero varint, so a zero range arrives as an
// ABSENT field, which is exactly the shape a session's first Snapshot has and
// the one the runtime's `|| 0` reads have to survive.
function snapshot(seq, tick, from, through) {
  return {
    protocol_version: 1,
    session_id: SESSION_A,
    snapshot: {
      server_seq: seq,
      patch_id: seq,
      transition_id: seq,
      state_version: seq,
      origin: { kind: OriginKind.RESYNC, event_id: 11, client_ref: 11, source: "resync" },
      updates: [{ fragment_id: "rc.panel", op: PatchOp.MORPH, html: panel(tick) }],
      superseded_from_seq: from,
      superseded_through_seq: through,
    },
  };
}

// latched brings a session to the state every case below is about: two patches
// applied, so `seq` is 2 and the DOM reads "tick 2", then a gap that makes the
// client ask for the Snapshot these frames are answers to. It returns the
// state the assertions are written against so that no case has to restate it.
async function latched(t) {
  const h = await harness(t, { random: 0.5 });
  const sock = h.connect();
  sock.deliver(h.patch(SESSION_A, 2, 2));
  sock.deliver(h.patch(SESSION_A, 4, 4)); // 3 never arrived

  const asked = sock.kind("resync_request");
  assert.equal(asked.length, 1, "the harness failed to latch a gap");
  assert.equal(asked[0].resync_request.last_applied_seq, 2);
  assert.equal(asked[0].resync_request.reason, ResyncReason.GAP);

  return [h, sock];
}

// refused asserts the whole of the rejection: the close code and its reason,
// that the frame was NOT applied, and that nothing was acknowledged for it.
// The last one is the point — an ack for a frame the client refused is the
// H-7 violation this check exists to avoid causing.
function refused(h, sock, acksBefore) {
  assert.equal(h.el("#line").textContent, "tick 2", "a Snapshot the client rejected was applied anyway");
  assert.equal(sock.closedWith && sock.closedWith.code, 4002, "the rejected Snapshot did not close 4002");
  assert.equal(sock.kind("ack").length, acksBefore, "the client acknowledged a Snapshot it refused to apply");
  assert.equal(h.status(), "reconnecting", "a 4002 the client itself sent did not end the connection");
}

// ---------------------------------------------------------------------------
// U-1 — the range, against what the client holds
// ---------------------------------------------------------------------------

test("a resync Snapshot whose range begins where the client stopped is applied", async (t) => {
  const [h, sock] = await latched(t);
  const acks = sock.kind("ack").length;

  // seq is 2, so the range the server owes starts at 3; 4, 5 and 6 were
  // emitted and are being replaced; the Snapshot itself is 7, so
  // through < server_seq holds (H-13).
  sock.deliver(snapshot(7, 6, 3, 6));

  assert.equal(h.el("#line").textContent, "tick 6", "the legal resync Snapshot was not applied");
  assert.equal(sock.closedWith, null, "a legal supersession range was rejected");
  assert.equal(h.status(), "live");

  // The high-water mark moved to the Snapshot, and the ack names it. This is
  // the assertion that would fail if the check were written to reject
  // everything: applying is the behaviour, refusing is the exception.
  const acked = sock.kind("ack");
  assert.equal(acked.length, acks + 1);
  assert.equal(acked[acked.length - 1].ack.server_seq, 7);

  // And the detector re-arms behind it: the next contiguous patch is 8.
  sock.deliver(h.patch(SESSION_A, 8, 8));
  assert.equal(h.el("#line").textContent, "tick 8");
});

test("a range beginning past where the client stopped is a hole, and closes 4002", async (t) => {
  const [h, sock] = await latched(t);
  const acks = sock.kind("ack").length;

  // from = 4 with seq = 2 leaves sequence 3 neither applied by the client nor
  // superseded by the server: the DOM would have an unaccounted-for cause,
  // which is precisely what §4.3 says the edge exists to prevent.
  sock.deliver(snapshot(7, 6, 4, 6));

  refused(h, sock, acks);
  assert.match(sock.closedWith.reason, /^supersession 4-6 at 2$/, "the close reason does not name the range and the seq");
});

test("a range that overlaps state the client already applied closes 4002", async (t) => {
  const [h, sock] = await latched(t);
  const acks = sock.kind("ack").length;

  // from = 2 replaces sequence 2, which the client applied and acked. This is
  // P7's non-overlap failing on the wire, and it is REV-INV BR-9's reachable
  // shape: a resync answered twice, whose second Snapshot supersedes a range
  // the first one already replaced.
  sock.deliver(snapshot(7, 6, 2, 6));

  refused(h, sock, acks);
  assert.match(sock.closedWith.reason, /^supersession 2-6 at 2$/);
});

test("a range whose end precedes its start closes 4002", async (t) => {
  const [h, sock] = await latched(t);
  const acks = sock.kind("ack").length;

  sock.deliver(snapshot(7, 6, 3, 2)); // through < from
  refused(h, sock, acks);
});

test("a from with no through closes 4002 rather than reading as an open range", async (t) => {
  const [h, sock] = await latched(t);
  const acks = sock.kind("ack").length;

  // The encoder omits a zero varint, so this frame carries field 10 and not
  // field 11 and the decoder yields `undefined`. `undefined < from` is false,
  // so a check written without the `|| 0` normalisation would accept it: the
  // half-set frame is the one an encoder bug produces, and H-13 is stated as
  // "both 0 or both non-zero" for exactly this reason.
  sock.deliver(snapshot(7, 6, 3, 0));
  refused(h, sock, acks);
  assert.match(sock.closedWith.reason, /^supersession 3-0 at 2$/);
});

test("a through with no from closes 4002", async (t) => {
  const [h, sock] = await latched(t);
  const acks = sock.kind("ack").length;

  sock.deliver(snapshot(7, 6, 0, 6)); // the other half-set frame
  refused(h, sock, acks);
});

test("a range that reaches the Snapshot's own sequence closes 4002", async (t) => {
  const [h, sock] = await latched(t);
  const acks = sock.kind("ack").length;

  // H-13 is `through < server_seq`, strictly: a Snapshot cannot supersede
  // itself, and a range that reached its own sequence would make the client's
  // next expected patch ambiguous.
  sock.deliver(snapshot(7, 6, 3, 7));
  refused(h, sock, acks);
});

test("a session's first Snapshot carries no range at all, and is applied", async (t) => {
  // The both-zero arm, stated as its own case because it is the one every
  // other spec in this directory depends on: a check that demanded a range
  // would break every mount and every reconnect, and the frames those specs
  // deliver carry neither field.
  const h = await harness(t, { random: 0.5 });
  const sock = h.connect();

  assert.equal(sock.closedWith, null, "the mount Snapshot was rejected for carrying no supersession range");
  assert.equal(h.status(), "live");
  assert.equal(sock.kind("ack")[0].ack.server_seq, 1);
});

// ---------------------------------------------------------------------------
// U-2 — the sequence, which must go forward
// ---------------------------------------------------------------------------

test("a Snapshot at a sequence the client already passed closes 4002 instead of acking backwards", async (t) => {
  const [h, sock] = await latched(t);
  const acks = sock.kind("ack").length;

  // server_seq 1 against seq 2. Without the check this applies, sets seq = 1,
  // and the NEXT ack names 1 — which the server closes as 4002 under H-7, the
  // same code, citing the client's ack rather than the frame that caused it.
  sock.deliver(snapshot(1, 6, 0, 0));

  refused(h, sock, acks);
  assert.match(sock.closedWith.reason, /^server_seq 1 at 2$/, "the close reason does not name both sequences");
});

test("a Snapshot repeating the sequence the client holds closes 4002", async (t) => {
  const [h, sock] = await latched(t);
  const acks = sock.kind("ack").length;

  // Equality, not just less-than: `seq` is the highest CONTIGUOUS sequence
  // applied, so re-applying it would re-render markup the client already holds
  // and, on a resync, would make `from === seq + 1` unsatisfiable for the next
  // one.
  sock.deliver(snapshot(2, 6, 0, 0));
  refused(h, sock, acks);
});

test("the sequence check is not reachable from the patch path, which discards first", async (t) => {
  // Stated so the guard is not read as the patch path's protection and then
  // "simplified" on the grounds that onMessage already checks. onMessage
  // discards a non-contiguous patch BEFORE applied() sees it — and the two
  // behaviours are different on purpose: a backwards patch is a gap to be
  // resynced, a backwards Snapshot is a contradiction to be closed.
  const [h, sock] = await latched(t);

  sock.deliver(h.patch(SESSION_A, 1, 9)); // behind the client
  assert.equal(sock.closedWith, null, "a stale patch closed the connection instead of being discarded");
  assert.equal(h.el("#line").textContent, "tick 2", "a stale patch was applied");
  assert.equal(h.status(), "live");
});
