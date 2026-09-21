// A smoke test of the SHIPPED artifacts, not of the sources.
//
// Everything else in this directory exercises client/runtime.js,
// client/codec.gen.js and client/inspector.js. That is the right level for
// behaviour, but it leaves one gap: live/clientjs/gotth-live.min.js and
// live/clientjs/gotth-live-inspector.min.js are committed build products, and
// a committed build product can go stale or be broken by the bundler while
// every source spec still passes. This file reaches across to the exact bytes
// the library embeds and serves, and asserts they parse, execute, and install
// exactly one global — none at all, in the inspector's case.
//
// live/clientjs/ holds that one file and no Go file, so it is a data directory
// rather than a package; the `go:embed` in live names it exactly (L9-1
// addendum to docs/reviews/module-init.md, 2026-08-04).
//
// It is loaded through createRequire rather than an eval-family API on
// purpose. The runtime's own no-eval rule (PRD NFR-4) is about the shipped
// file, but a test that reached for eval to check it would make the CI scan
// that enforces the rule harder to write and easier to get wrong.

import test from "node:test";
import assert from "node:assert/strict";
import { createRequire } from "node:module";
import { readFileSync } from "node:fs";

const source = readFileSync(new URL("../../live/clientjs/gotth-live.min.js", import.meta.url), "utf8");
const inspector = readFileSync(new URL("../../live/clientjs/gotth-live-inspector.min.js", import.meta.url), "utf8");
const devReload = readFileSync(new URL("../../live/clientjs/gotth-live-dev-reload.min.js", import.meta.url), "utf8");

test("the shipped bundle is self-contained: no import, no require, no remote fetch", () => {
  assert.doesNotMatch(source, /\brequire\s*\(/, "the bundle must not require anything");
  assert.doesNotMatch(source, /\bimport\s*[({]/, "the bundle must be a single self-contained file");
  assert.doesNotMatch(source, /\beval\s*\(|new Function/, "PRD NFR-4: no eval, ever");
  assert.doesNotMatch(source, /https?:\/\//, "PRD NFR-6: nothing is fetched from a third-party origin");
});

// NFR-4 and NFR-6 are about SHIPPED BYTES, and the inspector is shipped bytes:
// it is served by the library, from the same handler, to a real browser. That
// it is dev-only lowers the consequence of an eval in it and does not make one
// permitted, so the scan is the same scan.
test("the shipped inspector is self-contained: no import, no require, no remote fetch", () => {
  assert.doesNotMatch(inspector, /\brequire\s*\(/, "the inspector must not require anything");
  assert.doesNotMatch(inspector, /\bimport\s*[({]/, "the inspector must be a single self-contained file");
  assert.doesNotMatch(inspector, /\beval\s*\(|new Function/, "PRD NFR-4: no eval, ever");
  assert.doesNotMatch(inspector, /https?:\/\//, "PRD NFR-6: nothing is fetched from a third-party origin");
});

// Checkpoint-1 CP1-13: the inspector must open under a strict CSP, with no
// 'unsafe-inline' and no nonce to offer.
//
// Two spellings would break that, and neither is caught by reading the source
// once: a <style> element (style-src governs it however it was created) and a
// style="..." attribute written into markup. Element.style property writes and
// a constructed CSSStyleSheet are CSSOM, which style-src does not govern, and
// they are what the panel uses.
//
// innerHTML is scanned for a different reason. The inspector displays markup
// the server rendered — the one thing on its screen an attacker who has got
// this far controls — and it displays it through textContent. A dev tool that
// parsed it would execute it.
test("the shipped inspector is strict-CSP compatible and never parses server markup", () => {
  assert.doesNotMatch(inspector, /createElement\(\s*["'`]style/i, "a <style> element needs 'unsafe-inline' or a nonce");
  assert.doesNotMatch(inspector, /setAttribute\(\s*["'`]style["'`]/i, "an inline style attribute is rejected by a strict CSP");
  assert.doesNotMatch(inspector, /\.innerHTML\s*=|\.outerHTML\s*=|insertAdjacentHTML/,
    "server markup is shown as text, never parsed");
});

// The same two scans over the FR-57 dev-reload client, for the same reason:
// it is served by the library, from the same handler, to a real browser.
test("the shipped dev-reload client is self-contained: no import, no require, no remote fetch", () => {
  assert.doesNotMatch(devReload, /\brequire\s*\(/, "the dev-reload client must not require anything");
  assert.doesNotMatch(devReload, /\bimport\s*[({]/, "the dev-reload client must be a single self-contained file");
  assert.doesNotMatch(devReload, /\beval\s*\(|new Function/, "PRD NFR-4: no eval, ever");
  assert.doesNotMatch(devReload, /https?:\/\//, "PRD NFR-6: nothing is fetched from a third-party origin");
});

// The dev-reload badge is a fixed marker, built the way the inspector's panel
// is built and held to the same CSP rule. It never receives server markup at
// all — the only text it shows is two constant strings — so the innerHTML scan
// here is about the shape of the file rather than about an injection path.
test("the shipped dev-reload client is strict-CSP compatible", () => {
  assert.doesNotMatch(devReload, /createElement\(\s*["'`]style/i, "a <style> element needs 'unsafe-inline' or a nonce");
  assert.doesNotMatch(devReload, /setAttribute\(\s*["'`]style["'`]/i, "an inline style attribute is rejected by a strict CSP");
  assert.doesNotMatch(devReload, /\.innerHTML\s*=|\.outerHTML\s*=|insertAdjacentHTML/, "nothing here parses markup");
});

// The NFR-8 clause that no other check can see, and the FR-57 clause that
// inherits it: the shipped runtime does not know either dev artifact exists.
// There is no callback, no registry and no name in common, which is what makes
// "the inspector does not count against NFR-2" — and "dev reload costs the
// shipped runtime nothing" — true by construction rather than by a measurement
// that could drift.
test("the shipped runtime contains no inspector seam and no dev-reload seam", () => {
  assert.doesNotMatch(source, /inspect/i, "PRD NFR-8: the inspector costs the NFR-2 bundle zero bytes");
  assert.doesNotMatch(source, /dev-reload|devReload|gotth-live-dev/i, "FR-57 costs the NFR-2 bundle zero bytes");
});

test("the shipped bundle boots against a document and installs one global", () => {
  const listeners = [];
  const html = { setAttribute() {} };

  globalThis.document = {
    readyState: "complete",
    documentElement: html,
    currentScript: null, // no data-gotth-url, so boot() wires up but does not connect
    addEventListener: (t) => listeners.push(t),
    querySelectorAll: () => [],
  };

  const before = new Set(Object.keys(globalThis));
  createRequire(import.meta.url)("../../live/clientjs/gotth-live.min.js");

  const added = Object.keys(globalThis).filter((k) => !before.has(k));
  assert.deepEqual(added, ["gotthLive"], "exactly one new global (review-checklist §7.9)");

  assert.equal(globalThis.gotthLive.version, 1);
  assert.equal(typeof globalThis.gotthLive.start, "function");
  assert.equal(globalThis.gotthLive.status(), "");

  // Embedding shells may finish loading after unmount; start owns listeners.
  assert.deepEqual(listeners, []);
  assert.equal(typeof globalThis.gotthLive.stop, "function");

  delete globalThis.document;
  delete globalThis.gotthLive;
});

// The inspector's boot, over the shipped bytes.
//
// It has no seam in the runtime to attach to (that is the point — see
// client/inspector.js), so what it does at load is replace the WebSocket
// constructor. That is the whole tap, it is the one thing about this file that
// touches a global anybody else owns, and it is asserted here against the
// artifact rather than the source, because the artifact is what a browser
// runs.
test("the shipped inspector wraps the gotth-live socket and adds no global of its own", () => {
  class FakeSocket {
    constructor(url, protocols) {
      this.url = url;
      this.protocols = protocols;
      this.sent = [];
      this.listeners = [];
    }
    send(data) {
      this.sent.push(data);
    }
    addEventListener(type) {
      this.listeners.push(type);
    }
  }
  FakeSocket.OPEN = 1;

  globalThis.document = {
    readyState: "complete",
    body: null, // so boot registers for DOMContentLoaded instead of mounting
    addEventListener: () => {},
  };
  const native = (globalThis.WebSocket = FakeSocket);

  const before = new Set(Object.keys(globalThis));
  createRequire(import.meta.url)("../../live/clientjs/gotth-live-inspector.min.js");

  assert.deepEqual(
    Object.keys(globalThis).filter((k) => !before.has(k)),
    [],
    "the inspector installs no global of its own; it replaces one that exists",
  );
  assert.notEqual(globalThis.WebSocket, native, "the constructor was not wrapped, so nothing is being watched");
  assert.equal(globalThis.WebSocket.OPEN, FakeSocket.OPEN, "the constructor's statics must survive the wrap");

  // A real socket comes back — the wrapper is a function returning the native
  // instance, not a proxy in front of it — and it is tapped.
  const live = new globalThis.WebSocket("wss://app.example/live", "gotth-live.v1");
  assert.ok(live instanceof FakeSocket);
  assert.notEqual(live.send, FakeSocket.prototype.send, "the gotth-live socket's send is not tapped");
  assert.deepEqual(live.listeners, ["message", "close"]);

  // Every other socket on the page is left exactly alone. A dev tool that
  // read an application's unrelated WebSocket traffic would be a surprise
  // nobody opted into.
  const other = new globalThis.WebSocket("wss://app.example/chat", "something-else");
  assert.equal(other.send, FakeSocket.prototype.send, "an unrelated socket was tapped");
  assert.deepEqual(other.listeners, []);

  // Frames go through: a tapped send still reaches the underlying socket, and
  // an undecodable one does not throw into the application's call stack.
  live.send(new Uint8Array([8, 1]));
  assert.equal(live.sent.length, 1);
  live.send(new Uint8Array([255, 255, 255]));
  assert.equal(live.sent.length, 2);

  delete globalThis.document;
  delete globalThis.WebSocket;
});

// The dev-reload client's boot, over the shipped bytes.
//
// It reads two attributes off its own script tag and starts one timer. The
// property worth asserting against the ARTIFACT rather than the source is the
// negative one: with no data-gotth-dev-url — which is what a page rendered by
// anything other than DevReloadScript looks like — it starts no timer, makes
// no request, and adds nothing to the global object. A dev tool that polled a
// URL it guessed would be a dev tool that polls production.
test("the shipped dev-reload client does nothing on a page that did not ask for it", () => {
  const timers = [];
  const nativeSetTimeout = globalThis.setTimeout;
  globalThis.setTimeout = (fn, ms) => {
    timers.push(ms);
    return 0;
  };
  globalThis.document = {
    hidden: false,
    body: null,
    currentScript: { getAttribute: () => null },
    addEventListener: () => {},
    createElement: () => {
      throw new Error("nothing should be built");
    },
  };
  globalThis.fetch = () => {
    throw new Error("nothing should be fetched");
  };

  const before = new Set(Object.keys(globalThis));
  createRequire(import.meta.url)("../../live/clientjs/gotth-live-dev-reload.min.js");

  assert.deepEqual(
    Object.keys(globalThis).filter((k) => !before.has(k)),
    [],
    "the dev-reload client installs no global of its own",
  );
  assert.deepEqual(timers, [], "a page with no dev-reload tag started a polling timer anyway");

  globalThis.setTimeout = nativeSetTimeout;
  delete globalThis.document;
  delete globalThis.fetch;
});
