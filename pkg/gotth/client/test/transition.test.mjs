// View transitions around a region's patches: data-gotth-transition.
//
// DEV-ONLY and quarantined, like every file in this directory: never served,
// never bundled, reachable only from the bench image, which is the one image
// in the project with node in it (PRD FR-74).
//
// Run:
//   docker run --rm -v "$PWD:/w" -w /w/pkg/gotth dis-gotth-live-bench:latest \
//       bash -c 'node --test client/test/transition.test.mjs'
//
// A fragment root carrying data-gotth-transition has the patches that morph it
// applied inside document.startViewTransition, so the browser animates what
// moved. The update callback runs after the browser captured the old view, so
// these specs hold the callback and prove three things: nothing is applied
// before it runs, a frame that arrives meanwhile waits and is applied after it
// in order, and reduced motion, or a browser without the API, applies at once.
//
// The browser API is stubbed on the harness's document: the runtime reads
// document.startViewTransition and matchMedia at call time, so no seam was
// added for this spec.

import test from "node:test";
import assert from "node:assert/strict";

import { harness, SESSION_A } from "./harness.mjs";

const TRANSITION = "data-gotth-transition";

// marked connects a session and marks the panel for transitions, with the
// motion preference given and a startViewTransition that holds its update.
async function marked(t, reduce) {
  const h = await harness(t, { random: 0.5 });
  const sock = h.connect();
  h.el('[data-gotth-region="rc.panel"]').setAttribute(TRANSITION, "");
  const held = { update: null, done: null, count: 0 };
  h.doc.startViewTransition = (update) => {
    held.count++;
    held.update = update;
    return { updateCallbackDone: new Promise((resolve) => (held.done = resolve)) };
  };
  const saved = globalThis.matchMedia;
  globalThis.matchMedia = () => ({ matches: reduce });
  t.after(() => {
    if (saved === undefined) delete globalThis.matchMedia;
    else globalThis.matchMedia = saved;
  });
  return [h, sock, held];
}

test("a marked region's patch is applied inside a view transition, and a frame that arrives meanwhile waits its turn", async (t) => {
  const [h, sock, held] = await marked(t, false);
  const acks = sock.kind("ack").length;

  sock.deliver(h.patch(SESSION_A, 2, 2));
  assert.equal(held.count, 1, "the patch did not start a view transition");
  assert.equal(h.el("#line").textContent, "tick 0", "the patch was applied before the browser captured the old view");

  sock.deliver(h.patch(SESSION_A, 3, 3));
  assert.equal(h.el("#line").textContent, "tick 0", "a frame was applied while the transition held the page");
  assert.equal(sock.closedWith, null, "the held frame was read as a gap");

  held.update();
  assert.equal(h.el("#line").textContent, "tick 2", "the transition's update did not apply its patch");
  held.done();
  await new Promise((resolve) => setImmediate(resolve));
  assert.equal(h.el("#line").textContent, "tick 3", "the held frame was not applied after the transition");
  assert.deepEqual(
    sock.kind("ack").slice(acks).map((frame) => frame.ack.server_seq),
    [2, 3],
    "the frames were not acknowledged in order",
  );
});

test("reduced motion applies a marked region's patch at once, with no transition", async (t) => {
  const [h, sock, held] = await marked(t, true);
  sock.deliver(h.patch(SESSION_A, 2, 2));
  assert.equal(held.count, 0, "a view transition ran although the user asked for reduced motion");
  assert.equal(h.el("#line").textContent, "tick 2");
});

test("a browser without view transitions applies a marked region's patch at once", async (t) => {
  const [h, sock, held] = await marked(t, false);
  delete h.doc.startViewTransition;
  sock.deliver(h.patch(SESSION_A, 2, 2));
  assert.equal(held.count, 0);
  assert.equal(h.el("#line").textContent, "tick 2");
});

test("an unmarked region's patch never waits for a transition", async (t) => {
  const [h, sock, held] = await marked(t, false);
  h.el('[data-gotth-region="rc.panel"]').removeAttribute(TRANSITION);
  sock.deliver(h.patch(SESSION_A, 2, 2));
  assert.equal(held.count, 0);
  assert.equal(h.el("#line").textContent, "tick 2");
});
