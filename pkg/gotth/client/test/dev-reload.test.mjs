// The dev-reload decision (PRD FR-57).
//
// What is specified here is the whole of what this file DECIDES: given what
// the last poll did, does the page reload, wait, or carry on, and how long
// until the next one. That decision is pure and it is the entire feature —
// everything else in client/dev-reload.js is a timer, a fetch and eleven lines
// of badge.
//
// Importing the module runs no boot code: dev-reload.js guards its script-tag
// read and its polling loop behind `typeof document !== "undefined"`, the same
// way runtime.js and inspector.js guard theirs, so under node there is nothing
// to configure itself from and no timer to start.
//
// What is NOT specified here, stated so the coverage is not overread: the
// badge's DOM, and the loop that calls check() on a timer. The badge is built
// through createElement/textContent against a shadow root and a constructed
// stylesheet, and the properties that matter about it — no innerHTML, no
// <style> element, no style attribute — are asserted over the BUILT ARTIFACT
// by bundle.test.mjs, which reads the bytes the library actually serves. The
// loop is asserted end to end, in a real browser, against a real rebuild;
// docs/guide/dev-reload.md records that run.

import test from "node:test";
import assert from "node:assert/strict";

import { buildURL, check, configure, nextDelay, observe, reset, state } from "../dev-reload.js";

const BASELINE = "9e1f0c3a4b5d";

function ok(build) {
  return { ok: true, build };
}

// answering builds a fetch stand-in. It is deliberately not a mock library:
// what is being specified is how this file reads a Response, and the shapes
// that matter are a resolved non-ok response and a rejected promise — both of
// which are two lines to write and neither of which a mock would make clearer.
function answering(...turns) {
  let i = 0;
  return async () => {
    const turn = turns[Math.min(i++, turns.length - 1)];
    if (turn instanceof Error) throw turn;
    return turn;
  };
}

function response(status, body) {
  return { ok: status >= 200 && status < 300, status, text: async () => body };
}

test.beforeEach(() => {
  reset();
  configure({ build: BASELINE, endpoint: "/live/gotth-live-dev-build" });
});

// --- the decision -----------------------------------------------------------

test("the same build is nothing to do", () => {
  assert.equal(observe(ok(BASELINE)), "linked");
  assert.equal(state().misses, 0);
});

test("a different build reloads", () => {
  assert.equal(observe(ok("0000deadbeef")), "reload");
  assert.equal(state().reloading, true);
});

// location.reload() is asynchronous and several polls can land before the
// document goes away. A dev tool that reloaded again on each of them turns one
// rebuild into a loop.
test("once it has decided to reload it never decides anything else", () => {
  assert.equal(observe(ok("0000deadbeef")), "reload");
  assert.equal(observe(ok(BASELINE)), "reload", "it went back to the old build and stayed reloading");
  assert.equal(observe(null), "reload");
  assert.equal(observe(ok("0000deadbeef")), "reload");
});

// This is the whole of "without losing the session where state permits". The
// build identity does not move when the code does not, so a restart of the
// same binary — a crash loop, a container restart, a rebuild of source that
// did not really change — is answered "linked", and the client runtime's own
// reconnect brings the live regions back with no reload at all.
test("a restart into the same build is a reconnect, not a reload", () => {
  assert.equal(observe(null), "waiting");
  assert.equal(observe(null), "waiting");
  assert.equal(observe(ok(BASELINE)), "linked");
  assert.equal(state().reloading, false);
  assert.equal(state().misses, 0, "the miss counter did not clear, so the cadence stays fast forever");
});

test("nobody answering is waiting, and counts", () => {
  assert.equal(observe(null), "waiting");
  assert.equal(observe({ ok: false }), "waiting");
  assert.equal(observe(undefined), "waiting");
  assert.equal(state().misses, 3);
});

// A 200 carrying something that is not a build identity is not evidence that
// the build changed — it is evidence that something ELSE answered. A proxy's
// error page, a login redirect, a router that swallowed the path. The safe
// reading of "I do not know" is "do not reload".
test("a 200 that is not a build identity is not evidence about the build", () => {
  assert.equal(observe(ok("")), "waiting");
  assert.equal(observe(ok("x".repeat(129))), "waiting");
  assert.equal(observe({ ok: true, build: 17 }), "waiting");
  assert.equal(state().reloading, false);
});

test("128 bytes is accepted and 129 is not, which is the server's own bound", () => {
  configure({ build: "x".repeat(128), endpoint: "/e" });
  assert.equal(observe(ok("x".repeat(128))), "linked");
  configure({ build: BASELINE, endpoint: "/e" });
  assert.equal(observe(ok("x".repeat(128))), "reload");
});

// A tag with no build identity means a server that is not actually in dev
// mode, or a page rendered by something other than DevReloadScript. Adopting
// the first fetched value as the baseline would be worse than doing nothing:
// it would make the FIRST rebuild — the one the developer is watching for —
// the invisible one.
test("with no baseline on the tag it records what it sees and reloads nothing", () => {
  configure({ endpoint: "/e" });
  assert.equal(state().build, null);

  assert.equal(observe(ok("first")), "linked");
  assert.equal(state().build, "first");
  assert.equal(observe(ok("second")), "reload");
});

// --- the cadence ------------------------------------------------------------

test("a healthy session polls once a second", () => {
  observe(ok(BASELINE));
  assert.equal(nextDelay(), 1000);
});

test("a rebuild is polled for four times a second, then decays twice", () => {
  const cadence = [];
  for (let i = 1; i <= 101; i++) {
    observe(null);
    cadence.push([i, nextDelay()]);
  }
  const at = (n) => cadence.find(([i]) => i === n)[1];

  assert.equal(at(1), 250, "the first miss is a rebuild in progress");
  assert.equal(at(40), 250, "ten seconds of misses is still a rebuild in progress");
  assert.equal(at(41), 1000, "after ten seconds it is not a rebuild any more");
  assert.equal(at(100), 1000);
  assert.equal(at(101), 5000, "after about a minute and a half it is not coming back soon");
});

// --- reading what fetch did -------------------------------------------------

test("a 200 with a short token is a build identity, trimmed", async () => {
  assert.equal(await check(answering(response(200, "  " + BASELINE + "\n"))), "linked");
});

test("a refused connection is waiting, not an exception into the page", async () => {
  assert.equal(await check(answering(new TypeError("fetch failed"))), "waiting");
  assert.equal(state().misses, 1);
});

test("a non-2xx is waiting, whoever is answering the port", async () => {
  assert.equal(await check(answering(response(404, "not found"))), "waiting");
  assert.equal(await check(answering(response(502, "<html>bad gateway</html>"))), "waiting");
  assert.equal(state().reloading, false);
});

test("a new build over a healthy connection reloads", async () => {
  assert.equal(await check(answering(response(200, "0000deadbeef"))), "reload");
});

test("with no endpoint configured nothing is fetched and nothing reloads", async () => {
  reset();
  let called = false;
  assert.equal(
    await check(async () => {
      called = true;
    }),
    "waiting",
  );
  assert.equal(called, false);
});

// The poll must never be served from a cache. The route sets no-store too;
// both, because either alone has been enough to make this class of feature
// quietly stop working.
test("the poll asks for no cached copy and sends the page's own credentials", async () => {
  const seen = [];
  await check(async (url, init) => {
    seen.push([url, init]);
    return response(200, BASELINE);
  });

  assert.equal(seen[0][0], "/live/gotth-live-dev-build");
  assert.equal(seen[0][1].cache, "no-store");
  assert.equal(seen[0][1].credentials, "same-origin");
});

// --- the URL ----------------------------------------------------------------

test("the poll URL is the mount and nothing else", () => {
  assert.equal(buildURL("/live"), "/live/gotth-live-dev-build");
  assert.equal(buildURL("/app/live"), "/app/live/gotth-live-dev-build");
});

// "//" begins an authority, so a root mount joined the ordinary way would poll
// a HOST called gotth-live-dev-build. live.normalizeMount refuses that
// spelling on the server for the same reason.
test("a root mount does not become an authority", () => {
  assert.equal(buildURL("/"), "/gotth-live-dev-build");
});
