// The dev inspector's model and its HTMX audit (PRD FR-44, NFR-8).
//
// These specs drive client/inspector.js the way the tap drives it in a
// browser: frames are built as objects, ENCODED and DECODED with the same
// generated codec the runtime uses, and handed to record(). Going through the
// wire matters — a spec that passed a hand-written object would be asserting
// against a shape the codec might not produce, and the fields this file is
// about (absent varints reading as undefined, packed repeated ids, a 16-byte
// session id) are exactly the ones that differ between the two.
//
// Importing the module runs no boot code: inspector.js guards its WebSocket
// wrap and its panel behind `typeof document !== "undefined"`, the same way
// runtime.js guards its own, so under node there is nothing to mount and
// nothing to wrap.
//
// What is NOT specified here, stated so the coverage is not overread: the
// panel's rendering. It builds DOM through createElement/textContent against a
// shadow root and a constructed stylesheet, and client/test/dom.mjs implements
// the interface morph touches rather than either of those. The properties that
// matter about the view — no innerHTML, no eval, no inline style — are asserted
// over the BUILT ARTIFACT by bundle.test.mjs, which is a stronger check than a
// shim could give: it reads the bytes the library actually serves.

import test from "node:test";
import assert from "node:assert/strict";

import { decodeFrame, encodeFrame, OriginKind, PatchOp, ResyncReason } from "../codec.gen.js";
import { audit, note, record, reset, state } from "../inspector.js";
import { parse } from "./dom.mjs";

const SESSION_A = new Uint8Array(16).fill(0xa1);
const SESSION_B = new Uint8Array(16).fill(0xb2);

// wire round-trips a frame and folds it in, which is what attach() does with
// every frame that crosses the socket.
function wire(payload, dir, session) {
  const full = { protocol_version: 1, session_id: session || SESSION_A, ...payload };
  const bytes = encodeFrame(full);
  return record(decodeFrame(bytes), dir, bytes.length);
}

function sent(payload, session) {
  return wire(payload, 1, session);
}

function received(payload, session) {
  return wire(payload, 0, session);
}

// snapshot is the frame every session starts with; nothing joins until one has
// arrived, because the session is not known until then.
function snapshot(extra) {
  return {
    snapshot: {
      server_seq: 1,
      patch_id: 1,
      transition_id: 1,
      state_version: 1,
      origin: { kind: OriginKind.MOUNT, source: "mount" },
      heartbeat_interval_ms: 15000,
      max_inbound_frame_bytes: 65536,
      ack_window: 32,
      updates: [{ fragment_id: "counter", op: PatchOp.MORPH, html: "<b>0</b>" }],
      ...extra,
    },
  };
}

test.beforeEach(() => reset());

test("an event and the patch that answers it are one chain, joined by client_ref", () => {
  received(snapshot());

  const ev = sent({
    event: { client_ref: 7, name: "counter.increment", fragment_id: "counter", seen_server_seq: 1, fields: [{ key: "by", value: "2" }] },
  });
  assert.equal(ev.kind, "event");
  assert.equal(ev.eventId, 0, "the client cannot know the server's event id before a patch carries it");
  assert.deepEqual(ev.fields, [{ key: "by", value: "2" }]);

  const patch = received({
    patch: {
      server_seq: 2,
      patch_id: 5,
      transition_id: 33,
      state_version: 18,
      origin: { kind: OriginKind.CLIENT_EVENT, event_id: 41, client_ref: 7, source: "event:counter.increment" },
      updates: [{ fragment_id: "counter", op: PatchOp.MORPH, html: "<b>2</b>" }],
    },
  });

  // The patch names its cause, and the event has learned everything the
  // server minted for it: protocol.md §4.2's chain, end to end.
  assert.equal(patch.cause, ev.n);
  assert.equal(patch.originKind, "CLIENT_EVENT");
  assert.equal(patch.originSource, "event:counter.increment");
  assert.equal(ev.eventId, 41);
  assert.equal(ev.transitionId, 33);
  assert.equal(ev.stateVersion, 18);
  assert.deepEqual(ev.patches, [patch.n]);

  assert.deepEqual(patch.updates, [
    { fragmentId: "counter", op: "MORPH", bytes: 8, html: "<b>2</b>", truncated: false },
  ]);
  assert.equal(state().session.seq, 2);
  assert.equal(state().session.stateVersion, 18);
});

test("client telemetry joins back to the patch it reports on, by patch_id", () => {
  received(snapshot());
  const patch = received({
    patch: {
      server_seq: 2,
      patch_id: 5,
      transition_id: 2,
      state_version: 2,
      origin: { kind: OriginKind.TIMER, source: "timer:tick" },
      updates: [{ fragment_id: "counter", op: PatchOp.MORPH, html: "<b>1</b>" }],
    },
  });

  // The runtime sends this after every applied frame; it is where the morph
  // cost on screen comes from, and it is the client's own measurement.
  assert.equal(sent({ client_telemetry: { patch_id: 5, morph_micros: 2400, apply_micros: 3100 } }), null,
    "telemetry is folded into a patch row, not listed as one of its own");
  assert.equal(patch.morphMicros, 2400);
  assert.equal(patch.applyMicros, 3100);
});

test("a server-initiated patch resolves the events that contributed, and admits the ones it cannot", () => {
  received(snapshot());

  // Event 7 gets a patch of its own, so its event_id (41) becomes known here.
  const ev = sent({ event: { client_ref: 7, name: "chat.send", fragment_id: "log", seen_server_seq: 1 } });
  received({
    patch: {
      server_seq: 2,
      patch_id: 2,
      transition_id: 2,
      state_version: 2,
      origin: { kind: OriginKind.CLIENT_EVENT, event_id: 41, client_ref: 7, source: "event:chat.send" },
      updates: [{ fragment_id: "log", op: PatchOp.APPEND, html: "<li>hi</li>" }],
    },
  });

  const effect = received({
    patch: {
      server_seq: 3,
      patch_id: 3,
      transition_id: 3,
      state_version: 3,
      origin: {
        kind: OriginKind.EFFECT,
        source: "effect:chat.broadcast",
        contributing_event_ids: [41, 99],
      },
      updates: [{ fragment_id: "log", op: PatchOp.APPEND, html: "<li>there</li>" }],
    },
  });

  assert.equal(effect.originKind, "EFFECT");
  assert.equal(effect.cause, null, "an effect patch has no client_ref, so it is a root of its own");
  assert.deepEqual(effect.contributing, [
    { eventId: 41, row: ev.n, name: "chat.send" },
    // 99 is an event this browser never saw announced: its transition produced
    // no patch, so no frame ever carried the pair. The panel shows the number
    // rather than inventing a name for it; the server-side provenance log is
    // where that lookup lives (instrumentation §4A).
    { eventId: 99, row: 0, name: "" },
  ]);
});

test("an error naming an event lands on that event's row; one naming nothing gets its own", () => {
  received(snapshot());
  const ev = sent({ event: { client_ref: 3, name: "counter.increment", fragment_id: "counter", seen_server_seq: 1 } });

  const onEvent = received({ error: { code: 4, message: "unknown event", event_id: 12, client_ref: 3 } });
  assert.equal(onEvent, ev, "the error is that event's outcome, not a second step in the chain");
  assert.equal(ev.error.code, "UNKNOWN_EVENT");
  assert.equal(ev.error.fatal, false);

  const orphan = received({ error: { code: 6, message: "rate limited", event_id: 77, fatal: false } });
  assert.equal(orphan.kind, "error");
  assert.equal(orphan.error.code, "RATE_LIMITED");
  assert.equal(state().counts.errors, 2);
});

test("a resync request is listed and its reason is named", () => {
  received(snapshot());
  const row = sent({ resync_request: { last_applied_seq: 4, reason: ResyncReason.GAP } });

  assert.equal(row.kind, "resync");
  assert.equal(row.lastAppliedSeq, 4);
  assert.equal(row.reason, "GAP");
});

test("a resync snapshot carries its supersession edge and stays in the same session", () => {
  received(snapshot());
  const before = state().session;

  const resync = received(
    snapshot({
      server_seq: 9,
      patch_id: 9,
      transition_id: 9,
      state_version: 9,
      origin: { kind: OriginKind.RESYNC, event_id: 50, client_ref: 4, source: "resync" },
      superseded_from_seq: 2,
      superseded_through_seq: 8,
    }),
  );

  assert.equal(resync.supersededFrom, 2);
  assert.equal(resync.supersededThrough, 8);
  assert.equal(resync.originKind, "RESYNC");
  assert.equal(state().session, before, "a resync is the same session (RFC-0001 §8.1), so nothing resets");
});

test("a reconnect is a new session, and nothing joins across the boundary", () => {
  received(snapshot());
  sent({ event: { client_ref: 7, name: "counter.increment", fragment_id: "counter", seen_server_seq: 1 } });

  // RFC-0001 §8.1: a reconnect is a DIFFERENT session whose ids start again.
  // A client_ref of 7 in the new session is a different interaction, and
  // joining it to the old event would be a chain that reads right and is
  // fiction.
  received(snapshot(), SESSION_B);
  const patch = received(
    {
      patch: {
        server_seq: 2,
        patch_id: 2,
        transition_id: 2,
        state_version: 2,
        origin: { kind: OriginKind.CLIENT_EVENT, event_id: 3, client_ref: 7, source: "event:counter.increment" },
        updates: [{ fragment_id: "counter", op: PatchOp.MORPH, html: "<b>1</b>" }],
      },
    },
    SESSION_B,
  );

  assert.equal(patch.cause, null);
  assert.equal(state().session.id, "b2".repeat(16));
  assert.equal(state().session.seq, 2);
});

test("the log is a bounded ring, and the counts are not", () => {
  received(snapshot());
  for (let i = 0; i < 700; i++) {
    sent({ event: { client_ref: i + 1, name: "counter.increment", fragment_id: "counter", seen_server_seq: 1 } });
  }

  const s = state();
  assert.equal(s.rows.length, 500, "MAX_ROWS bounds what is held");
  assert.equal(s.rows[s.rows.length - 1].clientRef, 700, "the newest row is kept");
  assert.equal(s.counts.out, 700, "the counters keep counting past the window");
  assert.ok(s.counts.bytesOut > 0, "wire bytes are recorded, and no frame field carries them");
});

test("markup larger than the preview is truncated, and the byte count still tells the truth", () => {
  received(snapshot());
  const big = "<i>" + "x".repeat(9000) + "</i>";
  const patch = received({
    patch: {
      server_seq: 2,
      patch_id: 2,
      transition_id: 2,
      state_version: 2,
      origin: { kind: OriginKind.MOUNT, source: "mount" },
      updates: [{ fragment_id: "counter", op: PatchOp.MORPH, html: big }],
    },
  });

  assert.equal(patch.updates[0].bytes, big.length);
  assert.equal(patch.updates[0].html.length, 4096);
  assert.equal(patch.updates[0].truncated, true);
});

test("acks and heartbeats are counted and not listed", () => {
  received(snapshot());
  const before = state().rows.length;

  assert.equal(sent({ ack: { server_seq: 1 } }), null);
  assert.equal(received({ heartbeat: { nonce: 4, interval_ms: 15000 } }), null);

  assert.equal(state().rows.length, before);
  assert.equal(state().counts.out, 1);
  assert.equal(state().counts.in, 2);
});

test("notes collapse duplicates rather than repeating them", () => {
  note("hx", "same");
  note("hx", "same");
  note("hx", "other");

  assert.deepEqual(
    state().notes.map((n) => [n.text, n.count]),
    [
      ["same", 2],
      ["other", 1],
    ],
  );
});

// --- the HTMX ownership audit (RFC-0001 §10.3) ------------------------------

test("hx-* inside an unpreserved live fragment is reported", () => {
  const doc = parse(
    '<main data-gotth-region="panel">' +
      '<div id="feed" hx-get="/more" hx-trigger="revealed">…</div>' +
      "</main>",
  );

  assert.deepEqual(audit(doc), [{ fragment: "panel", tag: "div", id: "feed", attribute: "hx-get" }]);
});

test("hx-* under data-gotth-preserve is the sanctioned arrangement, and is not reported", () => {
  const doc = parse(
    '<main data-gotth-region="panel">' +
      '<section data-gotth-preserve><div id="feed" hx-get="/more">…</div></section>' +
      "</main>",
  );

  assert.deepEqual(audit(doc), [], "data-gotth-preserve is exactly the opt-out morph honours");
});

test("hx-* outside every live fragment is none of the inspector's business", () => {
  const doc = parse('<div hx-get="/elsewhere">…</div><main data-gotth-region="panel"><b>0</b></main>');

  assert.deepEqual(audit(doc), []);
});

test("the fragment root itself is audited, and an element is reported once however many hx-* it carries", () => {
  const doc = parse('<main data-gotth-region="panel" hx-get="/x" hx-post="/y"><b>0</b></main>');

  assert.deepEqual(audit(doc), [{ fragment: "panel", tag: "main", id: "", attribute: "hx-get" }]);
});
