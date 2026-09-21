// gotth-live dev reload — the browser half of FR-57.
//
// This file is NOT part of the shipped runtime. It is built to its own
// artifact, live/clientjs/gotth-live-dev-reload.min.js, served only when
// live.Config.Dev is true, and referenced only by the tag
// (*live.App).DevReloadScript renders — which renders nothing at all when Dev
// is false. It is the same two-gates-on-one-switch arrangement the inspector
// uses, for the same reason, and it costs the NFR-2 bundle exactly zero bytes:
// nothing in client/runtime.js knows this file exists, and nothing here
// imports it.
//
// ---------------------------------------------------------------------------
// What problem this solves, and what the runtime already solves
// ---------------------------------------------------------------------------
//
// A Go change and a templ change are the same event: templ generates Go, so
// both mean the process is rebuilt and restarted. The socket drops, and the
// client runtime's own reconnect-and-resync state machine brings the page back
// by itself — that part needs nothing from this file, and it is why a restart
// of the SAME binary is not a reload here.
//
// What resync cannot do is repaint anything outside a live fragment. The page
// shell, the <head>, the classes on the <body>, a fragment whose markup
// changed while its state did not (Dirty says clean, so no patch is sent) —
// all of that was rendered once, by the process that is now gone. After a
// rebuild the browser is showing markup from a build that no longer exists,
// with a live connection into a build that renders it differently. Only a
// document reload fixes that, and FR-57 says the developer must not be the one
// to ask for it.
//
// So this file answers one question, once a second: is the process on the
// other end still the build that rendered this page?
//
// ---------------------------------------------------------------------------
// Build identity, and why it is on the script tag
// ---------------------------------------------------------------------------
//
// The server exposes a build identity at <mount>/gotth-live-dev-build, in dev
// only. By default it is a hash of the running executable, so it changes when
// and only when the code changes: an identical rebuild, or a plain restart of
// the same binary, keeps it — and this file then does nothing at all, leaving
// the runtime's reconnect to restore the session.
//
// The baseline comes from the SCRIPT TAG, not from a first fetch at boot. That
// is not a micro-optimisation, it is the correctness of the whole thing: the
// server renders the id of the build that produced THIS document into the tag,
// so a rebuild that lands between the page render and the first poll is still
// caught. Adopting the first fetched value as the baseline would silently
// accept the new build as "what this page is showing", which is exactly the
// case a developer hits when they save a file while the page is loading.
//
// ---------------------------------------------------------------------------
// What is exported, and why
// ---------------------------------------------------------------------------
//
// The decision — reload, or keep waiting, or carry on — is pure, and it is
// exported so client/test/dev-reload.test.mjs can drive it directly rather
// than through a browser. check() is exported for the same reason one layer
// out: the mapping from "what fetch did" to "what that means" is where a
// non-2xx answer, a network error and a body of the wrong shape have to be
// told apart, and every one of those is a real thing a half-restarted server
// does. Importing this module under node runs no boot code, the same way
// runtime.js and inspector.js guard theirs.

//#region model

// The poll cadence, in milliseconds.
//
// STEADY is the healthy case: the server answered and the build matched. One
// request a second per open dev tab is the cost of the feature, and it is why
// polling stops while the tab is hidden.
//
// REBUILDING is the cadence while the server is not answering, which is what a
// rebuild-and-restart looks like from here. It is four times a second so the
// reload lands as soon as the new process is listening rather than up to a
// second later.
//
// The two decays after that exist because "not answering" also describes a
// server the developer stopped an hour ago, and hammering a dead port at 4 Hz
// until the tab is closed is rude to nothing except the log, but it is still
// wrong. After ten seconds of silence the cadence drops to STEADY, and after
// about a minute and a half to ABANDONED.
var STEADY = 1000;
var REBUILDING = 250;
var ABANDONED = 5000;

// The miss counts at which the cadence decays. Named rather than inlined
// because client/test/dev-reload.test.mjs asserts the boundaries by number,
// and a table whose numbers are written twice is a table that disagrees.
var REBUILDING_UNTIL = 40; // 40 × 250 ms = 10 s
var STEADY_UNTIL = 100; // then 60 × 1000 ms = 1 min more

// A build identity is a short opaque token. The bound is here rather than only
// on the server because this file must decide what to do with a body it did
// not expect, and "reload the page" is the wrong answer to a proxy's HTML
// error document that happened to arrive with a 200.
var MAX_BUILD_BYTES = 128;

var cfg = {
  // build is the identity of the build that rendered this document, read off
  // the script tag. Null means the tag carried none, which is a server that
  // is not actually in dev mode; nothing is polled in that case.
  build: null,
  // endpoint is the absolute-path URL of the build-identity route.
  endpoint: null,
};

var misses = 0;
var reloading = false;

// configure sets the baseline and the endpoint. It is called once at boot from
// the script tag's attributes, and directly by the specs.
export function configure(options) {
  cfg.build = options && typeof options.build === "string" && options.build !== "" ? options.build : null;
  cfg.endpoint = (options && options.endpoint) || null;
  misses = 0;
  reloading = false;
}

// reset returns the module to its unconfigured state. Specs only.
export function reset() {
  configure(null);
}

// state is the model the specs read, and what the badge renders from.
export function state() {
  return { build: cfg.build, endpoint: cfg.endpoint, misses: misses, reloading: reloading };
}

// observe folds one poll result into the model and returns what to do about
// it. It is the whole decision, and it is pure.
//
// The three answers:
//
//	"linked"    the build on the other end is the one that rendered this page.
//	            Nothing to do. This is the answer on every poll of a healthy
//	            session, and also the answer while the process is restarting
//	            into the SAME binary — that case is the runtime's reconnect to
//	            handle, not this file's.
//	"waiting"   nobody answered, or what answered was not a build identity.
//	            Between a rebuild's `kill` and its `listen` this is the answer
//	            for as many polls as the build takes.
//	"reload"    a DIFFERENT build answered. The document in front of the
//	            developer was rendered by a process that no longer exists.
//
// Once "reload" has been returned it is returned forever: location.reload() is
// asynchronous, several polls can land before the document goes away, and
// reloading a page that is already reloading is how a dev tool turns a rebuild
// into a loop.
export function observe(result) {
  if (reloading) return "reload";
  if (!result || result.ok !== true || typeof result.build !== "string") {
    misses++;
    return "waiting";
  }
  var build = result.build;
  if (build === "" || build.length > MAX_BUILD_BYTES) {
    // A 200 carrying something that is not a build identity is not evidence
    // that the build changed. It is evidence that something else answered —
    // a proxy, a login page, a router that swallowed the path — and the safe
    // reading of "I do not know" is "do not reload".
    misses++;
    return "waiting";
  }
  if (cfg.build === null) {
    // No baseline was rendered into the tag, so there is nothing to compare
    // against and adopting this one would make the first rebuild invisible.
    // Recorded, and nothing else.
    cfg.build = build;
    misses = 0;
    return "linked";
  }
  if (build !== cfg.build) {
    reloading = true;
    return "reload";
  }
  misses = 0;
  return "linked";
}

// nextDelay is how long to wait before polling again, given how many
// consecutive polls have failed. See STEADY/REBUILDING/ABANDONED above.
export function nextDelay() {
  if (misses === 0) return STEADY;
  if (misses <= REBUILDING_UNTIL) return REBUILDING;
  if (misses <= STEADY_UNTIL) return STEADY;
  return ABANDONED;
}

// buildURL turns the mount the server rendered into the tag into the path this
// file polls.
//
// The root mount is the one case: "/" already ends in the separator, and
// joining it again would spell "//" — which a browser reads as the beginning
// of an authority, so the poll would go to a host called
// "gotth-live-dev-build". live.normalizeMount refuses that spelling on the
// server for the same reason, and this is the client's half of it.
export function buildURL(mount) {
  return (mount === "/" ? "/" : mount + "/") + "gotth-live-dev-build";
}

//#endregion

//#region poll

// check performs one poll and folds the answer in.
//
// Everything a half-restarted server can do arrives here as an exception or as
// a response, and the mapping between those and observe()'s vocabulary is the
// part worth specifying: a refused connection throws, a listening socket
// belonging to something else answers 404 or 502, and a server that is up
// answers 200 with a short token. Only the last of those is evidence about the
// build.
//
// The fetch is `cache: "no-store"`: the answer is by definition the one thing
// on the page that must never be served from a cache, and the route sets
// no-store as well. Both, because either alone has been enough to make this
// class of feature quietly stop working.
export async function check(fetchImpl) {
  if (!cfg.endpoint) return observe(null);
  try {
    var response = await fetchImpl(cfg.endpoint, { cache: "no-store", credentials: "same-origin" });
    if (!response || !response.ok) return observe(null);
    var body = await response.text();
    return observe({ ok: true, build: body.trim() });
  } catch (err) {
    return observe(null);
  }
}

//#endregion

//#region badge

// The badge is the one piece of UI here: a small fixed marker that appears
// while the server is not answering and goes away when it comes back. It
// exists because the difference between "my rebuild is taking eight seconds"
// and "my app crashed and nothing is coming" is otherwise invisible — the page
// just sits there looking fine, which is the failure mode this project cares
// about most.
//
// It is built the way the inspector's panel is built, and for the same reason:
// CSSOM property writes and a constructed stylesheet, never a <style> element
// and never a style attribute, so it opens under a strict CSP with no
// 'unsafe-inline' and no nonce to offer. Nothing here is ever given server
// markup, so there is no innerHTML anywhere in this file either.
var CSS =
  ".b{position:fixed;right:12px;bottom:12px;z-index:2147483647;" +
  "font:12px/1.4 ui-monospace,SFMono-Regular,Menlo,monospace;" +
  "background:#1c1917;color:#fafaf9;border:1px solid #44403c;border-radius:6px;" +
  "padding:6px 10px;box-shadow:0 2px 8px rgba(0,0,0,.35);opacity:.92}" +
  ".d{display:inline-block;width:7px;height:7px;border-radius:50%;" +
  "background:#f59e0b;margin-right:7px;vertical-align:middle}";

var host = null;
var label = null;

function badge() {
  if (host) return;
  host = document.createElement("div");
  host.id = "gotth-live-dev-reload";
  var shadow = host.attachShadow ? host.attachShadow({ mode: "open" }) : host;
  try {
    var sheet = new CSSStyleSheet();
    sheet.replaceSync(CSS);
    shadow.adoptedStyleSheets = [sheet];
  } catch (x) {
    // An unstyled marker is still a marker. A dev tool that needs the page's
    // CSP relaxed to show one line of text is worse than a plain one.
  }
  var box = document.createElement("div");
  box.className = "b";
  var dot = document.createElement("span");
  dot.className = "d";
  label = document.createElement("span");
  box.appendChild(dot);
  box.appendChild(label);
  shadow.appendChild(box);
  document.body.appendChild(host);
}

// paint shows, hides or relabels the badge for one action. It is separate from
// observe() so that the decision has no DOM in it at all, which is what lets
// the specs run under node.
function paint(action) {
  if (action === "linked") {
    if (host && host.parentNode) host.parentNode.removeChild(host);
    host = null;
    label = null;
    return;
  }
  if (!document.body) return;
  badge();
  label.textContent =
    action === "reload" ? "gotth-live: new build — reloading" : "gotth-live: waiting for the server";
}

//#endregion

//#region bootstrap

// The guard is what lets the specs import this module under node, where there
// is no document to read a script tag off and no timer worth starting — the
// same arrangement runtime.js and inspector.js use for the same reason.
if (typeof document !== "undefined") {
  var script = document.currentScript;
  var mount = script && script.getAttribute("data-gotth-dev-url");
  var stamped = script && script.getAttribute("data-gotth-dev-build");

  if (mount) {
    configure({ build: stamped, endpoint: buildURL(mount) });

    // One timer and one request in flight, ever. Both guards are here because
    // the visibilitychange listener below deliberately polls out of turn, and
    // a listener that starts a second loop every time the developer alt-tabs
    // is a dev tool that gets slower the longer it is open.
    var timer = 0;
    var busy = false;

    var schedule = function (ms) {
      if (timer) clearTimeout(timer);
      timer = setTimeout(tick, ms);
    };

    var tick = function () {
      timer = 0;
      // Polling stops while the tab is hidden. A background tab is not
      // watching for a reload, and browsers throttle its timers anyway; the
      // visibilitychange listener is what makes the reload land the moment
      // the developer comes back to it.
      if (document.hidden || busy) return void schedule(STEADY);
      busy = true;
      check(globalThis.fetch).then(function (action) {
        busy = false;
        paint(action);
        if (action === "reload") return void globalThis.location.reload();
        schedule(nextDelay());
      });
    };

    document.addEventListener("visibilitychange", function () {
      if (!document.hidden && !state().reloading) schedule(0);
    });

    schedule(STEADY);
  }
}

//#endregion
