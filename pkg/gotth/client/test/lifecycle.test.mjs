import test from "node:test";
import assert from "node:assert/strict";
import { ErrorCode } from "../codec.gen.js";
import { harness, SESSION_A, SESSION_B } from "./harness.mjs";

test("stop releases a connecting socket and detached callbacks cannot reconnect", async (t) => {
  const h = await harness(t);
  h.rt.start("/live", h.root);
  const previous = h.live();
  h.rt.stop();
  assert.equal(previous.closedWith.code, 1000);
  assert.equal(previous.onmessage, null);
  assert.equal(previous.onclose, null);
  assert.equal(h.status(), "closed");
  h.visibility("hidden");
  h.visibility("visible");
  assert.equal(h.clock.count(), 0);
  assert.equal(h.sockets().length, 1);
});

test("stop cancels reconnect and per-binding debounce work", async (t) => {
  const h = await harness(t);
  h.connect();
  const button = h.el("#inc");
  button.setAttribute("data-gotth-on", "click:rc.inc::100");
  h.emit("click", button);
  assert.equal(h.clock.count(), 1);
  h.live().drop();
  assert.equal(h.clock.count(), 2);
  h.rt.stop();
  assert.equal(h.clock.count(), 0);
  h.emit("click", button);
  assert.equal(h.clock.count(), 0, "delegated listener survived stop");
  h.rt.stop();
  assert.equal(h.status(), "closed", "stop is idempotent");
});

test("remount binds new controls and resets the connection without duplicate dispatch", async (t) => {
  const h = await harness(t);
  h.connect();
  const previous = h.live();
  h.rt.stop();
  h.rt.start("/board/live", h.root);
  const current = h.live();
  current.accept().deliver(h.snapshot(SESSION_B, 2));
  h.emit("click", h.el("#inc"));
  const events = current.kind("event");
  assert.equal(events.length, 1);
  assert.equal(events[0].event.client_ref, 1);
  assert.deepEqual(events[0].session_id, SESSION_B);
  assert.equal(previous.kind("event").length, 0);
  assert.equal(current.url, "ws://app.test/board/live");
});

test("starting another island replaces the current connection", async (t) => {
  const h = await harness(t);
  h.connect(SESSION_A);
  const previous = h.live();
  h.rt.start("/next/live", h.root);
  assert.equal(previous.closedWith.code, 1000);
  assert.equal(h.sockets().length, 2);
  h.live().accept().deliver(h.snapshot(SESSION_B, 0));
  h.emit("click", h.el("#inc"));
  assert.equal(h.live().kind("event").length, 1);
});

test("stop cancels an outstanding resync retry", async (t) => {
  const h = await harness(t);
  h.connect();
  h.live().deliver(h.patch(SESSION_A, 3, 3));
  h.live().deliver({ protocol_version: 1, session_id: SESSION_A, error: { code: ErrorCode.RATE_LIMITED, message: "try later" } });
  assert.equal(h.clock.count(), 1);
  h.rt.stop();
  assert.equal(h.clock.count(), 0);
});
