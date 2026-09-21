// The environment the node-side client specs drive the real runtime in.
//
// DEV-ONLY and quarantined, like every file in this directory: never served,
// never bundled, reachable only from the bench image, which is the one image
// in the project with node in it (PRD FR-74).
//
// This file holds no assertions. It is here because two suites need the same
// environment — client/test/reconnect.test.mjs and client/test/resync.test.mjs
// — and a second copy of a fake socket is a second definition of what the
// server does, which is the way two suites quietly stop testing the same
// system. It is a plain .mjs and not a .test.mjs on purpose: ci.sh runs every
// client/test/*.test.mjs as its own node --test process, so a helper that
// matched that glob would be a suite with no tests in it, and a spec file
// importing another spec file would run that file's specs twice.
//
// The runtime reads document, WebSocket, setTimeout, Math.random, location and
// performance off the global object at CALL time, so a test can replace all of
// them before importing it and observe everything the runtime does to the
// outside world. No seam was added to the runtime for testability, and none
// should be: a seam is a byte cost on every page and a second code path
// nothing in production takes.

import assert from "node:assert/strict";

import { decodeFrame, encodeFrame, PatchOp } from "../codec.gen.js";
import { parse, Element } from "./dom.mjs";

export const SESSION_A = new Uint8Array(16).fill(0xa1);
export const SESSION_B = new Uint8Array(16).fill(0xb2);

// panel is the one fragment every case in these suites uses. It holds a bound
// control, an uncontrolled input and an identified line, which is the least
// that lets a spec tell "morphed" from "replaced" and "frozen" from "wiped".
export function panel(tick, extra) {
  return (
    '<main data-gotth-region="rc.panel">' +
    '<p id="line">tick ' +
    tick +
    "</p>" +
    '<input id="draft" name="draft">' +
    '<button id="inc" data-gotth-on="click:rc.inc">+</button>' +
    (extra || "") +
    "</main>"
  );
}

// fakeClock replaces the global timer functions with a queue a spec can
// inspect. Delays are recorded exactly as the runtime asked for them, so the
// backoff assertions are on the ARGUMENT to setTimeout rather than on elapsed
// time — there is no sleeping anywhere in these suites (review-checklist §8.9).
export function fakeClock() {
  let next = 0;
  const armed = new Map();
  const real = { setTimeout: globalThis.setTimeout, clearTimeout: globalThis.clearTimeout };

  // Handles start at 1. The runtime stores the handle in a variable it also
  // uses as "is a retry armed", so a falsy handle would read as no timer.
  globalThis.setTimeout = (fn, delay) => {
    armed.set(++next, { fn, delay });
    return next;
  };
  globalThis.clearTimeout = (h) => {
    armed.delete(h);
  };

  return {
    delays: () => [...armed.values()].map((t) => t.delay),
    count: () => armed.size,
    // fire runs the SOONEST-DUE armed timer, tie-broken by arming order. The
    // runtime can hold two — a reconnect retry and a resync retry — and this
    // clock has no notion of now, so "soonest due" means "as if both were
    // armed at the same instant". That is true to within the microseconds a
    // case here takes, and it is the difference between a spec that observes
    // the schedule and one that observes the order two setTimeout calls
    // happened to be written in. It removes the timer first, so a callback
    // that arms another is not confused with the one that ran.
    fire() {
      let pick = null;
      for (const [handle, timer] of armed) if (!pick || timer.delay < pick[1].delay) pick = [handle, timer];
      armed.delete(pick[0]);
      pick[1].fn();
    },
    restore() {
      globalThis.setTimeout = real.setTimeout;
      globalThis.clearTimeout = real.clearTimeout;
    },
  };
}

// fakeSocket is a WebSocket the test drives from the server side.
//
// It records what the runtime sent, DECODED, so an assertion reads as the
// protocol rather than as bytes — and decoding is itself a check, because a
// frame the shipped decoder cannot read is a frame the server would reject.
export class fakeSocket {
  constructor(url, protocol) {
    this.url = url;
    this.protocol = protocol;
    this.readyState = 0; // CONNECTING
    this.sent = [];
    this.closedWith = null;
    fakeSocket.all.push(this);
  }

  send(bytes) {
    this.sent.push(decodeFrame(bytes));
  }

  // close is the runtime closing us; a real endpoint answers with a close
  // event carrying that code, and so do we.
  close(code, reason) {
    this.closedWith = { code, reason };
    this.drop(code);
  }

  // --- the server side of the socket -------------------------------------

  accept() {
    this.readyState = 1;
    return this;
  }

  deliver(frame) {
    const bytes = encodeFrame(frame);
    // binaryType is "arraybuffer", so the runtime receives an ArrayBuffer.
    this.onmessage?.({ data: bytes.buffer });
    return this;
  }

  // drop closes the socket from the far side. 1006 is what a browser reports
  // for a connection that vanished without a close frame, which is what a cut
  // cable, a killed server and a proxy timeout all look like.
  drop(code) {
    if (this.readyState === 3) {
      this.onclose?.({ code: code === undefined ? 1006 : code });
      return this;
    }
    this.readyState = 3;
    this.onclose?.({ code: code === undefined ? 1006 : code });
    return this;
  }

  // frames of a kind, for the assertions below.
  kind(name) {
    return this.sent.filter((f) => f[name] !== undefined);
  }
}
fakeSocket.all = [];

// harness installs the environment, imports a FRESH runtime module, and
// returns everything a spec needs to drive it.
//
// The import is cache-busted per case because the runtime holds module-level
// connection state — a second test importing the same instance would inherit
// the first test's session, sequence number and status.
let caseNo = 0;
export async function harness(t, opts) {
  opts = opts || {};

  const saved = {
    document: globalThis.document,
    WebSocket: globalThis.WebSocket,
    location: globalThis.location,
    random: Math.random,
    gotthLive: globalThis.gotthLive,
  };
  const clock = fakeClock();
  fakeSocket.all = [];

  const root = parse(panel(0));
  const html = new Element("html");
  const trail = [];
  const setAttr = html.setAttribute.bind(html);
  html.setAttribute = (n, v) => {
    if (n === "data-gotth-status") trail.push(v);
    setAttr(n, v);
  };

  const listeners = new Map();
  const dispatched = [];
  const doc = {
    readyState: "complete",
    visibilityState: "visible",
    currentScript: null, // boot() wires up but does not connect; start() is explicit
    documentElement: html,
    activeElement: null,
    addEventListener(type, fn) {
      if (!listeners.has(type)) listeners.set(type, []);
      if (!listeners.get(type).includes(fn)) listeners.get(type).push(fn);
    },
    removeEventListener(type, fn) {
      listeners.set(type, (listeners.get(type) || []).filter((listener) => listener !== fn));
    },
    querySelectorAll: (sel) => root.querySelectorAll(sel),
    querySelector: (sel) => root.querySelectorAll(sel)[0] || null,
    getElementById: (id) => root.querySelectorAll("[id]").find((e) => e.id === id) || null,
    createElement(tag) {
      if (tag !== "template") throw new Error("the runtime only creates <template>; got " + tag);
      return {
        content: null,
        set innerHTML(h) {
          this.content = parse(h);
        },
      };
    },
    // Recorded rather than ignored: the CustomEvent the runtime raises on an
    // Error frame is a documented contract with the embedding application, so
    // a spec has to be able to assert it is still raised.
    dispatchEvent(e) {
      dispatched.push(e);
    },
  };

  globalThis.document = doc;
  globalThis.WebSocket = fakeSocket;
  globalThis.location = { href: "http://app.test/page" };
  // A number fixes every draw; a function is the sequence of draws, which is
  // how a spec says "the unluckiest run" and "the luckiest run" in one case.
  if (typeof opts.random === "function") Math.random = opts.random;
  else if (opts.random !== undefined) Math.random = () => opts.random;

  const rt = await import("../runtime.js?case=" + ++caseNo);

  t.after(() => {
    clock.restore();
    Math.random = saved.random;
    globalThis.document = saved.document;
    globalThis.WebSocket = saved.WebSocket;
    globalThis.location = saved.location;
    globalThis.gotthLive = saved.gotthLive;
    if (saved.document === undefined) delete globalThis.document;
    if (saved.WebSocket === undefined) delete globalThis.WebSocket;
    if (saved.location === undefined) delete globalThis.location;
    if (saved.gotthLive === undefined) delete globalThis.gotthLive;
  });

  const h = {
    rt,
    clock,
    doc,
    root,
    trail,
    dispatched,
    status: () => html.getAttribute("data-gotth-status"),
    sockets: () => fakeSocket.all,
    live: () => fakeSocket.all[fakeSocket.all.length - 1],
    // el reads a node back out of the LIVE document every time, never from a
    // reference a spec captured: a detached node keeps its properties, so
    // reading one off a stale reference reports success for a node the patch
    // threw away. "#id" is spelled for the spec's benefit — dom.mjs supports
    // exactly the selector forms the runtime itself uses, and an id selector
    // is not one of them, which is a property of the shim worth keeping.
    el: (sel) => doc.querySelector(sel[0] === "#" ? '[id="' + sel.slice(1) + '"]' : sel),
    // emit fires a delegated DOM event the way a browser would: at the
    // document, in the capture phase, with the target the user acted on.
    //
    // extra is merged onto the event object, which is how a spec gives a
    // keydown its `key`. It is an argument rather than a fixed field because
    // the runtime reads `e.key` on every dispatch and `undefined === "Escape"`
    // is false: a harness that always supplied one would make an unfiltered
    // binding indistinguishable from a filtered one that happened to match.
    emit(type, target, extra) {
      const e = Object.assign({ type, target, preventDefault() {} }, extra);
      (listeners.get(type) || []).forEach((fn) => fn(e));
    },
    visibility(state) {
      doc.visibilityState = state;
      (listeners.get("visibilitychange") || []).forEach((fn) => fn({ type: "visibilitychange" }));
    },
    // snapshot is what the server sends on every connection: a new session,
    // server_seq starting again at 1 (§8.1).
    snapshot(session, tick, seq) {
      return {
        protocol_version: 1,
        session_id: session,
        snapshot: {
          server_seq: seq === undefined ? 1 : seq,
          patch_id: 1,
          transition_id: 1,
          state_version: 1,
          updates: [{ fragment_id: "rc.panel", op: PatchOp.MORPH, html: panel(tick) }],
        },
      };
    },
    patch(session, seq, tick) {
      return {
        protocol_version: 1,
        session_id: session,
        patch: {
          server_seq: seq,
          patch_id: seq,
          transition_id: seq,
          state_version: seq,
          updates: [{ fragment_id: "rc.panel", op: PatchOp.MORPH, html: panel(tick) }],
        },
      };
    },
  };

  // connect brings a session all the way up, which most specs need before
  // they can be about a DISconnection.
  h.connect = (session) => {
    rt.start("/live");
    h.live().accept();
    h.live().deliver(h.snapshot(session || SESSION_A, 0));
    assert.equal(h.status(), "live", "the harness failed to establish a session");
    return h.live();
  };

  return h;
}
