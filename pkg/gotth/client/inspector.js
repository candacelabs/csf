// gotth-live dev inspector — the causal chain of a running session (FR-44).
//
// This file is NOT part of the shipped runtime. It is built to its own
// artifact, live/clientjs/gotth-live-inspector.min.js, served only when
// live.Config.Dev is true, and referenced only by the tag
// (*live.App).InspectorScript renders — which renders nothing at all when Dev
// is false. NFR-8's four clauses map onto four mechanisms:
//
//	separate opt-in file    its own entry point, its own artifact, its own
//	                        ceiling in tools/minify. Nothing imports it from
//	                        runtime.js and nothing in runtime.js names it.
//	not counted by NFR-2    it costs the shipped bundle ZERO bytes. See "Why
//	                        no seam in the runtime" below — this is the whole
//	                        reason this file taps the socket instead of asking
//	                        the runtime for a callback.
//	not loaded in prod      two independent gates in Go, both on Config.Dev:
//	                        the route 404s and the script tag is not written.
//	≤ 40 KB gzipped         tools/minify measures this artifact against its own
//	                        ceiling on every CI run, beside NFR-2's.
//
// ---------------------------------------------------------------------------
// Why no seam in the runtime
// ---------------------------------------------------------------------------
//
// The obvious design is a callback the runtime offers — `gotthLive.inspect(fn)`
// — called once per frame. It is three lines, it is order-independent, and it
// is what a library with no byte budget would do.
//
// It also puts bytes that exist only for the inspector inside the artifact
// NFR-2 gates, and NFR-8 says in as many words that the inspector must not
// count against NFR-2. Measured on the tree this landed against, the seam was
// ~70 gzipped bytes out of 7,859 of headroom, so this is not a capacity
// argument — it is that a requirement written as MUST NOT is not something to
// spend a little of. client/test/harness.mjs already states the same rule from
// the testing side: "No seam was added to the runtime for testability, and
// none should be: a seam is a byte cost on every page and a second code path
// nothing in production takes."
//
// So the inspector reads the wire instead. It replaces globalThis.WebSocket
// with a function that returns a real socket with `send` wrapped and a
// `message` listener attached, filtered to the "gotth-live.v1" subprotocol so
// no other socket on the page is touched. The runtime reads WebSocket off the
// global at call time (client/test/harness.mjs relies on the same property),
// so nothing in runtime.js changes, and a production page contains no code
// path that so much as mentions this file.
//
// The cost of that choice is ORDER: the runtime opens its socket during its
// own script execution, so this file must execute first. Both tags are
// `defer`, which executes them in document order, so "first" means "written
// above live.Script". Getting it wrong is a silent no-op — the failure mode
// this codebase least tolerates — so it is checked and reported IN THE PANEL:
// if globalThis.gotthLive already exists when this module runs, the runtime
// booted first and the panel opens with that message rather than with an empty
// list somebody has to explain.
//
// ---------------------------------------------------------------------------
// What it shows, and where every field comes from
// ---------------------------------------------------------------------------
//
// Nothing here is derived from the runtime's internal state. Every value on
// screen is a field of a frame that crossed the socket, decoded with the same
// generated codec the runtime uses (client/codec.gen.js), so the panel cannot
// show a chain the wire did not carry. protocol.md §4.2 is the chain:
//
//	Event{client_ref, seen_server_seq}          what the browser sent
//	   -> server mints event_id
//	   -> transition_id -> state_version -> patch_id -> server_seq
//	   -> Origin{kind, event_id, client_ref, source, contributing_event_ids}
//
// The join is Origin.client_ref: it names the outbound Event this patch
// answers, and it is the only field that closes the loop locally, because
// event_id is minted server-side and no frame tells the client about it until
// a patch carries both. That has a consequence worth stating rather than
// hiding: `contributing_event_ids` on an EFFECT patch (FR-42, RFC §7.4) can be
// resolved to a named local event ONLY IF that event previously got a patch of
// its own carrying the same event_id. An event whose transition produced no
// patch — a suppressed render, RFC §5.4 — is never announced to the client at
// all, so its id shows as a bare number. The server-side provenance log
// (instrumentation §4A) has the record this client cannot have; §4A.3's
// distinction is exactly this one, and the panel says so where it happens
// instead of inventing a name.
//
// Timings are the client's own: the runtime sends ClientTelemetry{patch_id,
// morph_micros, apply_micros} after every applied frame, so the outbound half
// of the tap carries the morph cost and it is joined back by patch_id.
//
// It additionally flags `hx-*` attributes inside an unpreserved live fragment
// (RFC-0001 §10.3, instrumentation §7). The server deliberately does not scan
// rendered HTML for those, so this is the only place that mistake is caught,
// and it is caught where the developer will see it.
//
// ---------------------------------------------------------------------------
// Constraints this file is written against
// ---------------------------------------------------------------------------
//
// Strict CSP (checkpoint-1 CP1-13). No inline script: this is an external
// file. No `<style>` element and no `style="..."` attribute in markup: the
// panel's styling is a constructed CSSStyleSheet adopted by a shadow root, and
// its positioning is set through element.style properties. Both are CSSOM,
// which `style-src` does not govern; a `<style>` element would need
// 'unsafe-inline' or a nonce, and this file has neither to offer. Where
// constructed stylesheets are unavailable the panel still works and simply
// looks plain — see mount().
//
// No eval and no new Function (NFR-4), asserted over the built artifact by
// client/test/bundle.test.mjs, the same scan that guards the runtime.
//
// No innerHTML anywhere. Fragment HTML from the server is shown as TEXT in a
// <pre>, through textContent. A patch's markup is the one thing on screen that
// an attacker who has reached this far controls, and a dev tool that executed
// it would be a nicer exploit than the bug it was opened to find.
//
// Bounded memory. A session at the dashboard workload emits tens of frames a
// second and a developer leaves this open for hours, so the log is a ring of
// MAX_ROWS entries, fragment markup is truncated at HTML_KEEP characters, and
// nothing else accumulates. It is a window on a stream, not a recording; the
// server-side provenance log is the recording (instrumentation §4A.1 rejects
// the in-memory ring for the same reason at a larger scale).
//
// No npm, no CDN, no build step for the consumer (FR-74, NFR-6): one relative
// import of the generated codec, bundled into the artifact by the Go minifier.

import { decodeFrame, ErrorCode, OriginKind, PatchOp, ResyncReason } from "./codec.gen.js";

// The subprotocol the runtime opens with. A page may hold other sockets; this
// is what tells them apart, and it is compared rather than guessed at because
// wrapping an unrelated socket would be both a privacy problem and a bug.
var PROTOCOL = "gotth-live.v1";

// MAX_ROWS bounds the log. 500 rows is ~10 s of the dashboard example's 53 Hz
// stream and several minutes of a hand-driven counter, which is the window a
// developer is actually reading.
var MAX_ROWS = 500;

// HTML_KEEP bounds what is retained of a FragmentUpdate's markup. The wire
// permits 1 MiB per update (protocol.md §3.3); the panel shows the head of it
// and always reports the true byte length beside it.
var HTML_KEEP = 4096;

// ---------------------------------------------------------------------------
// The model
// ---------------------------------------------------------------------------
//
// Exported so client/test/inspector.test.mjs can drive it with frames and read
// the chain back without a DOM. The IIFE the minifier emits has no exports, so
// these cost the artifact nothing but their bodies.

var log = [], // the ring, oldest first
  rowNo = 0, // monotonic row number, for display and for stable keys
  byRef = {}, // client_ref  -> event row      (this connection)
  byEventId = {}, // event_id -> event row     (this connection)
  byPatchId = {}, // patch_id -> patch row     (this connection)
  sess = null, // the current session summary, null before the first Snapshot
  notes = [], // inspector-level problems: load order, hx-* ownership
  counts = { in: 0, out: 0, bytesIn: 0, bytesOut: 0, errors: 0 };

function push(row) {
  row.n = ++rowNo;
  row.t = Date.now();
  log.push(row);
  if (log.length > MAX_ROWS) log.shift();
  return row;
}

// connection resets everything scoped to ONE CONNECTION, for the same reason
// the runtime's newSession() does: RFC-0001 §8.1 makes a reconnect a different
// session, its ids start again from 1, and a client_ref carried across would
// join this session's patch to the previous session's event — a chain that
// looks right and is fiction.
function connection(id) {
  byRef = {};
  byEventId = {};
  byPatchId = {};
  sess = {
    id: id,
    status: "live",
    seq: 0,
    stateVersion: 0,
    heartbeatMs: 0,
    maxInboundBytes: 0,
    ackWindow: 0,
    since: Date.now(),
  };
}

function hex(u8) {
  if (!u8) return "";
  var s = "";
  for (var i = 0; i < u8.length; i++) s += (u8[i] < 16 ? "0" : "") + u8[i].toString(16);
  return s;
}

// name inverts one of the generated enum objects. The codec exports them as
// {NAME: number}; every id on the wire needs the other direction, and an id
// the enum does not know is shown as its number rather than as "unknown",
// because a newer server sending a value this build has no name for is
// FR-10's forward compatibility working, not an error.
function name(enumeration, id) {
  if (!id) return "";
  for (var k in enumeration) if (enumeration[k] === id) return k;
  return String(id);
}

// fieldsOf flattens Event.fields into an array of {key, value} that survives
// JSON.stringify. Values are the application's own form data, so they are
// never interpreted here, only displayed.
function fieldsOf(ev) {
  var out = [];
  for (var i = 0; ev.fields && i < ev.fields.length; i++) {
    out.push({ key: ev.fields[i].key || "", value: ev.fields[i].value || "" });
  }
  return out;
}

function updatesOf(p) {
  var out = [];
  for (var i = 0; p.updates && i < p.updates.length; i++) {
    var u = p.updates[i],
      html = u.html || "";
    out.push({
      fragmentId: u.fragment_id || "",
      op: name(PatchOp, u.op) || "MORPH",
      // The byte length is the wire's, not the retained string's: a truncated
      // preview must never be able to under-report what actually arrived.
      bytes: html.length,
      html: html.length > HTML_KEEP ? html.slice(0, HTML_KEEP) : html,
      truncated: html.length > HTML_KEEP,
    });
  }
  return out;
}

// patchRow builds the row for a Patch or a Snapshot and performs the join.
function patchRow(p, kind) {
  var o = p.origin || {},
    row = push({
      kind: kind, // "patch" | "snapshot"
      dir: 0,
      serverSeq: p.server_seq || 0,
      patchId: p.patch_id || 0,
      transitionId: p.transition_id || 0,
      stateVersion: p.state_version || 0,
      originKind: name(OriginKind, o.kind),
      originSource: o.source || "",
      eventId: o.event_id || 0,
      clientRef: o.client_ref || 0,
      contributing: [],
      updates: updatesOf(p),
      supersededFrom: p.superseded_from_seq || 0,
      supersededThrough: p.superseded_through_seq || 0,
      cause: null, // the row number of the event this answers, once joined
      morphMicros: 0,
      applyMicros: 0,
      error: null,
    });

  // The join. Origin.client_ref names an event this browser sent, so it is the
  // one edge the client can close on its own; learning event_id from the same
  // Origin is what later lets contributing_event_ids resolve to a name.
  var ev = row.clientRef ? byRef[row.clientRef] : null;
  if (ev) {
    ev.eventId = row.eventId;
    ev.patches.push(row.n);
    ev.stateVersion = row.stateVersion;
    ev.transitionId = row.transitionId;
    row.cause = ev.n;
    if (row.eventId) byEventId[row.eventId] = ev;
  }

  // FR-42's second half: a coalesced or effect-originated patch names the
  // events that contributed. Each is resolved to a local event row where one
  // is known, and left as a bare id where it is not — see the header.
  for (var i = 0; o.contributing_event_ids && i < o.contributing_event_ids.length; i++) {
    var id = o.contributing_event_ids[i],
      src = byEventId[id];
    row.contributing.push({ eventId: id, row: src ? src.n : 0, name: src ? src.name : "" });
  }

  if (row.patchId) byPatchId[row.patchId] = row;
  if (sess) {
    sess.seq = row.serverSeq;
    sess.stateVersion = row.stateVersion;
  }
  return row;
}

// reset clears the whole model. The panel's "clear" button and the specs use
// it; nothing else does.
export function reset() {
  log = [];
  rowNo = 0;
  byRef = {};
  byEventId = {};
  byPatchId = {};
  sess = null;
  notes = [];
  counts = { in: 0, out: 0, bytesIn: 0, bytesOut: 0, errors: 0 };
}

// note records an inspector-level problem — something wrong with the page
// rather than with the session. Duplicates are collapsed, because the hx-*
// audit re-runs after every patch and would otherwise report the same element
// once per frame for as long as the page is open.
//
// It repaints, so every caller is spared remembering to; render() is a no-op
// before the panel is mounted and under node, where there is no panel at all.
export function note(kind, text) {
  for (var i = 0; i < notes.length; i++) {
    if (notes[i].kind === kind && notes[i].text === text) {
      notes[i].count++;
      render();
      return notes[i];
    }
  }
  var n = { kind: kind, text: text, count: 1 };
  notes.push(n);
  render();
  return n;
}

// record takes one decoded frame and folds it into the model.
//
// dir is 0 for a frame the browser received and 1 for one it sent. bytes is
// the frame's length on the wire, which the panel reports and which no field
// of the frame itself carries.
//
// It returns the row it created, or null for a frame that is counted but not
// listed (Ack, Heartbeat, ClientTelemetry — the last of which is folded into
// the patch row it reports on rather than shown on its own).
export function record(frame, dir, bytes) {
  if (dir) {
    counts.out++;
    counts.bytesOut += bytes || 0;
  } else {
    counts.in++;
    counts.bytesIn += bytes || 0;
  }

  if (frame.snapshot) {
    var s = frame.snapshot,
      id = hex(frame.session_id);
    // A Snapshot with a session id this panel has not seen is a new
    // connection (RFC §8.1). A resync Snapshot on the SAME session is not:
    // it carries the supersession edge and belongs in the existing chain.
    if (!sess || sess.id !== id) connection(id);
    sess.heartbeatMs = s.heartbeat_interval_ms || 0;
    sess.maxInboundBytes = s.max_inbound_frame_bytes || 0;
    sess.ackWindow = s.ack_window || 0;
    return patchRow(s, "snapshot");
  }

  if (frame.patch) return patchRow(frame.patch, "patch");

  if (frame.event) {
    var e = frame.event,
      row = push({
        kind: "event",
        dir: 1,
        clientRef: e.client_ref || 0,
        name: e.name || "",
        fragmentId: e.fragment_id || "",
        seenServerSeq: e.seen_server_seq || 0,
        fields: fieldsOf(e),
        eventId: 0, // learned from the Origin of the patch that answers it
        transitionId: 0,
        stateVersion: 0,
        patches: [],
        error: null,
      });
    if (row.clientRef) byRef[row.clientRef] = row;
    return row;
  }

  if (frame.error) {
    var er = frame.error;
    counts.errors++;
    var target = (er.client_ref && byRef[er.client_ref]) || (er.event_id && byEventId[er.event_id]);
    var detail = {
      code: name(ErrorCode, er.code) || String(er.code || 0),
      message: er.message || "",
      fatal: !!er.fatal,
      eventId: er.event_id || 0,
      clientRef: er.client_ref || 0,
    };
    // An error that names an event this browser sent belongs ON that event's
    // row: it is that event's outcome, and a separate row would split one
    // causal step across two lines. An error that names nothing the client
    // knows — a refused ResyncRequest carries a server-minted event_id
    // (runtime.js's refused() explains why it is indistinguishable) — gets its
    // own row, because it has no chain to attach to.
    if (target) {
      target.error = detail;
      return target;
    }
    return push({ kind: "error", dir: 0, error: detail });
  }

  if (frame.resync_request) {
    return push({
      kind: "resync",
      dir: 1,
      lastAppliedSeq: frame.resync_request.last_applied_seq || 0,
      reason: name(ResyncReason, frame.resync_request.reason) || "",
    });
  }

  if (frame.client_telemetry) {
    var ct = frame.client_telemetry,
      p = byPatchId[ct.patch_id];
    if (p) {
      p.morphMicros = ct.morph_micros || 0;
      p.applyMicros = ct.apply_micros || 0;
    }
    return null;
  }

  // Ack and Heartbeat are counted and not listed. They are the two frame kinds
  // with no causal content: an ack is a high-water mark the panel already
  // shows as `seq`, and a heartbeat echo says only that the socket is alive,
  // which the status line says better.
  return null;
}

// state is the whole model, for the view and for the specs.
export function state() {
  return { session: sess, rows: log, notes: notes, counts: counts };
}

// ---------------------------------------------------------------------------
// The HTMX ownership audit (RFC-0001 §10.3)
// ---------------------------------------------------------------------------

// audit walks every live fragment under root and reports `hx-*` elements that
// morph will overwrite.
//
// The rule it enforces is RFC §10.3's precedence rule, from the side the
// server declined to check: inside a declared fragment, an element carrying
// `data-gotth-preserve` and its subtree are morph's business no longer, so an
// hx-* element there is the sanctioned arrangement. An hx-* element anywhere
// else inside the fragment is server-owned — the next patch overwrites it, and
// any HTMX swap into it is reverted — which is a mistake with no symptom until
// a patch arrives, sometimes minutes later.
//
// It walks attributes rather than running a selector so it needs nothing from
// the DOM beyond childNodes, attributes and getAttribute — which is what lets
// it be specified against client/test/dom.mjs with no browser.
export function audit(root) {
  var found = [],
    regions = root.querySelectorAll("[data-gotth-region]");
  for (var i = 0; i < regions.length; i++) walk(regions[i], regions[i].getAttribute("data-gotth-region"), found);
  return found;
}

function walk(el, fragment, found) {
  // Preserved subtrees are exactly what the opt-out is for, so the walk stops
  // rather than reporting what it finds under one.
  if (el.hasAttribute && el.hasAttribute("data-gotth-preserve")) return;
  var attrs = el.attributes || [];
  for (var i = 0; i < attrs.length; i++) {
    if (attrs[i].name.indexOf("hx-") === 0) {
      found.push({
        fragment: fragment,
        tag: (el.tagName || "").toLowerCase(),
        id: (el.getAttribute && el.getAttribute("id")) || "",
        attribute: attrs[i].name,
      });
      break; // one report per element, not one per hx-* attribute on it
    }
  }
  var kids = el.childNodes || [];
  for (var j = 0; j < kids.length; j++) if (kids[j].nodeType === 1) walk(kids[j], fragment, found);
}

// ---------------------------------------------------------------------------
// The wire tap
// ---------------------------------------------------------------------------

var paused = false;

// attach wraps one socket. Both halves record AFTER the underlying call, so a
// send that throws is not reported as a frame that went out.
function attach(ws) {
  var send = ws.send;
  ws.send = function (data) {
    var r = send.call(ws, data);
    if (!paused) {
      try {
        // encodeFrame produces a Uint8Array, which decodeFrame takes as-is.
        take(decodeFrame(data), 1, data.byteLength);
      } catch (x) {
        note("decode", "an outbound frame did not decode: " + x.message);
      }
    }
    return r;
  };
  ws.addEventListener("message", function (e) {
    if (paused) return;
    if (typeof e.data === "string") return void note("wire", "a text frame arrived; every payload is a binary Frame (protocol.md §1)");
    try {
      take(decodeFrame(new Uint8Array(e.data)), 0, e.data.byteLength);
    } catch (x) {
      note("decode", "an inbound frame did not decode: " + x.message);
    }
  });
  ws.addEventListener("close", function (e) {
    if (sess) sess.status = "closed " + e.code;
    render();
  });
}

// take folds a frame in and schedules the two things that follow from it: a
// repaint, and — for a frame that changed the DOM — a re-audit once the
// runtime's morph has run.
//
// The audit is deferred with setTimeout rather than a microtask on purpose.
// This listener and the runtime's own run from the same event dispatch, and a
// microtask queued here can run between the two listeners rather than after
// both, which would audit the DOM the patch has not been applied to yet.
function take(frame, dir, bytes) {
  record(frame, dir, bytes);
  if (!dir && (frame.patch || frame.snapshot)) scheduleAudit();
  render();
}

var auditTimer = 0;
function scheduleAudit() {
  if (auditTimer) return;
  auditTimer = setTimeout(function () {
    auditTimer = 0;
    var found = audit(document);
    for (var i = 0; i < found.length; i++) {
      note(
        "hx",
        "<" +
          found[i].tag +
          (found[i].id ? "#" + found[i].id : "") +
          "> carries " +
          found[i].attribute +
          " inside fragment “" +
          found[i].fragment +
          "” and is not marked data-gotth-preserve: morph owns it, so the next patch overwrites it (RFC-0001 §10.3)",
      );
    }
  }, 0);
}

// ---------------------------------------------------------------------------
// The panel
// ---------------------------------------------------------------------------

var host = null,
  shadow = null,
  head = null,
  body = null,
  repaint = 0, // the pending animation-frame handle, 0 when none
  collapsed = false;

var CSS =
  ":host{all:initial}" +
  "*{box-sizing:border-box;font:12px/1.45 ui-monospace,SFMono-Regular,Menlo,Consolas,monospace}" +
  ".p{display:flex;flex-direction:column;max-height:60vh;width:min(46rem,96vw);" +
  "background:#12141a;color:#dfe3ea;border:1px solid #2b3040;border-radius:6px 6px 0 0;box-shadow:0 0 24px #0008;overflow:hidden}" +
  ".h{display:flex;gap:.6em;align-items:center;padding:.45em .7em;background:#1b1f29;border-bottom:1px solid #2b3040;flex:0 0 auto}" +
  ".h b{color:#8ab4ff;font-weight:600}" +
  ".h .sp{flex:1 1 auto}" +
  ".h button{font:inherit;background:#262b38;color:#dfe3ea;border:1px solid #394054;border-radius:3px;padding:.1em .5em;cursor:pointer}" +
  ".h button[aria-pressed=true]{background:#3a4256;border-color:#5a6478}" +
  ".b{overflow:auto;flex:1 1 auto}" +
  ".r{border-bottom:1px solid #20242e;padding:.3em .7em}" +
  ".r>summary{cursor:pointer;list-style:none;white-space:nowrap;overflow:hidden;text-overflow:ellipsis}" +
  ".r>summary::-webkit-details-marker{display:none}" +
  ".up{color:#7fd1a0}.dn{color:#8ab4ff}" +
  ".k{color:#9aa3b4}" +
  ".w{color:#ffc46b}.e{color:#ff8a8a}" +
  ".d{padding:.35em 0 .2em 1.4em;color:#c3c9d6}" +
  ".d div{white-space:pre-wrap;word-break:break-word}" +
  "pre{margin:.3em 0;padding:.4em;background:#0d0f14;border:1px solid #232838;border-radius:3px;max-height:12em;overflow:auto;white-space:pre-wrap;word-break:break-word}" +
  ".n{padding:.35em .7em;background:#2a2113;color:#ffc46b;border-bottom:1px solid #3a2f1a}" +
  ".empty{padding:1em .7em;color:#9aa3b4}";

function el(tag, cls, text) {
  var n = document.createElement(tag);
  if (cls) n.className = cls;
  if (text !== undefined) n.textContent = text;
  return n;
}

function mount() {
  host = el("div");
  host.id = "gotth-live-inspector";
  // Positioning through CSSOM properties rather than a style attribute: a
  // strict CSP rejects the attribute and does not govern these.
  host.style.position = "fixed";
  host.style.right = "0";
  host.style.bottom = "0";
  host.style.zIndex = "2147483647";
  shadow = host.attachShadow ? host.attachShadow({ mode: "open" }) : host;

  // Constructed stylesheets are CSSOM and are not subject to style-src. A
  // <style> element would be, and would need 'unsafe-inline' or a nonce this
  // file cannot supply. Where they are unavailable the panel is unstyled and
  // still readable, which is the honest degradation: it is a dev tool, and a
  // plain one beats one that needs the page's CSP relaxed to open.
  try {
    var sheet = new CSSStyleSheet();
    sheet.replaceSync(CSS);
    shadow.adoptedStyleSheets = [sheet];
  } catch (x) {
    note("css", "constructed stylesheets are unavailable in this browser, so the panel is unstyled");
  }

  var panel = el("div", "p");
  head = el("div", "h");
  body = el("div", "b");
  panel.appendChild(head);
  panel.appendChild(body);
  shadow.appendChild(panel);
  document.body.appendChild(host);
  render();
}

function button(label, title, onClick, pressed) {
  var b = el("button", null, label);
  b.type = "button";
  b.title = title;
  if (pressed !== undefined) b.setAttribute("aria-pressed", pressed ? "true" : "false");
  b.addEventListener("click", onClick);
  return b;
}

// render repaints the whole panel on the next frame. Whole, rather than
// incrementally: the log is bounded at MAX_ROWS, one repaint per animation
// frame is at most 60 a second however fast the stream is, and a diffing dev
// tool is a second morph implementation to get wrong.
function render() {
  if (!head || repaint) return;
  // Called as a bare global, NOT through `(globalThis.rAF || setTimeout)(...)`.
  // That expression form calls the function with no receiver, and
  // requestAnimationFrame is a Window method: it throws "Illegal invocation",
  // from inside mount(), leaving a panel that is mounted, styled and
  // permanently empty. Found by opening the thing in a real browser, which is
  // the only place it could have been found.
  repaint = globalThis.requestAnimationFrame ? requestAnimationFrame(due) : setTimeout(due, 16);
}

function due() {
  repaint = 0;
  paint();
}

function paint() {
  var s = sess;
  head.textContent = "";
  head.appendChild(el("b", null, "gotth-live"));
  head.appendChild(el("span", "k", s ? s.id.slice(0, 8) : "no session"));
  if (s) {
    head.appendChild(el("span", null, s.status));
    head.appendChild(el("span", "k", "seq " + s.seq));
    head.appendChild(el("span", "k", "v" + s.stateVersion));
  }
  head.appendChild(el("span", "k", counts.in + "↓ " + counts.out + "↑"));
  if (counts.errors) head.appendChild(el("span", "e", counts.errors + " err"));
  head.appendChild(el("span", "sp"));
  head.appendChild(
    button(paused ? "resume" : "pause", "stop or resume recording frames", function () {
      paused = !paused;
      paint();
    }, paused),
  );
  head.appendChild(
    button("clear", "discard the recorded chain", function () {
      reset();
      paint();
    }),
  );
  head.appendChild(button("copy", "copy the recorded chain as JSON", copy));
  head.appendChild(
    button(collapsed ? "▲" : "▼", "collapse or expand the panel", function () {
      collapsed = !collapsed;
      paint();
    }, collapsed),
  );

  body.textContent = "";
  if (collapsed) {
    body.style.display = "none";
    return;
  }
  body.style.display = "";

  for (var i = 0; i < notes.length; i++) {
    body.appendChild(el("div", "n", (notes[i].count > 1 ? "×" + notes[i].count + "  " : "") + notes[i].text));
  }

  if (!log.length) {
    body.appendChild(
      el("div", "empty", "No frames yet. Interact with the page, or check that this script is loaded before live.Script."),
    );
    return;
  }
  // Newest first: the row a developer wants is the one that just happened.
  for (var j = log.length - 1; j >= 0; j--) body.appendChild(row(log[j]));
}

function copy() {
  var text = JSON.stringify(state(), null, 2);
  if (!globalThis.navigator || !navigator.clipboard) return void note("clipboard", "this browser exposes no clipboard API, so copy is unavailable");
  navigator.clipboard.writeText(text).then(null, function (x) {
    note("clipboard", "copy failed: " + x.message);
    paint();
  });
}

// row renders one entry as a <details> whose summary is the causal one-liner
// and whose body is every field behind it.
function row(r) {
  var d = document.createElement("details"),
    sum = document.createElement("summary");
  d.className = "r";
  sum.appendChild(el("span", "k", "#" + r.n + " "));

  if (r.kind === "event") {
    sum.appendChild(el("span", "up", "↑ " + (r.name || "(unnamed)")));
    sum.appendChild(el("span", "k", " ref " + r.clientRef + (r.fragmentId ? " · " + r.fragmentId : "")));
    sum.appendChild(
      el(
        "span",
        "k",
        r.eventId
          ? " → event " + r.eventId + " · v" + r.stateVersion + " · " + r.patches.length + " patch"
          : " → no patch yet",
      ),
    );
    if (r.error) sum.appendChild(el("span", "e", " · " + r.error.code));
    detail(d, [
      ["client_ref", r.clientRef],
      ["seen_server_seq", r.seenServerSeq],
      ["event_id", r.eventId || "(not yet announced by any patch)"],
      ["transition_id", r.transitionId || "—"],
      ["state_version", r.stateVersion || "—"],
      ["patch rows", r.patches.join(", ") || "—"],
    ]);
    for (var i = 0; i < r.fields.length; i++) field(d, "field " + r.fields[i].key, r.fields[i].value);
    if (r.error) errorDetail(d, r.error);
  } else if (r.kind === "patch" || r.kind === "snapshot") {
    sum.appendChild(el("span", "dn", "↓ " + r.kind + " " + r.patchId));
    sum.appendChild(el("span", "k", " seq " + r.serverSeq + " · v" + r.stateVersion));
    sum.appendChild(el("span", null, " " + (r.originKind || "—") + (r.originSource ? " " + r.originSource : "")));
    if (r.cause) sum.appendChild(el("span", "up", " ← #" + r.cause));
    var ops = [];
    for (var k = 0; k < r.updates.length; k++) ops.push(r.updates[k].op + " " + r.updates[k].fragmentId);
    if (ops.length) sum.appendChild(el("span", "k", " · " + ops.join(", ")));
    if (r.morphMicros) sum.appendChild(el("span", "k", " · " + (r.morphMicros / 1000).toFixed(1) + " ms"));

    var pairs = [
      ["server_seq", r.serverSeq],
      ["patch_id", r.patchId],
      ["transition_id", r.transitionId],
      ["state_version", r.stateVersion],
      ["origin.kind", r.originKind || "—"],
      ["origin.source", r.originSource || "—"],
      ["origin.event_id", r.eventId || "—"],
      ["origin.client_ref", r.clientRef || "—"],
    ];
    if (r.cause) pairs.push(["caused by", "row #" + r.cause]);
    if (r.supersededFrom) pairs.push(["supersedes", r.supersededFrom + "–" + r.supersededThrough]);
    if (r.morphMicros || r.applyMicros) {
      pairs.push(["client morph", (r.morphMicros / 1000).toFixed(3) + " ms"]);
      pairs.push(["client apply", (r.applyMicros / 1000).toFixed(3) + " ms"]);
    }
    detail(d, pairs);

    if (r.contributing.length) {
      var c = [];
      for (var m = 0; m < r.contributing.length; m++) {
        var e = r.contributing[m];
        c.push(e.row ? "event " + e.eventId + " = #" + e.row + " " + e.name : "event " + e.eventId + " (not seen by this client)");
      }
      field(d, "contributing_event_ids", c.join("\n"));
    }
    for (var u = 0; u < r.updates.length; u++) {
      var up = r.updates[u];
      field(
        d,
        up.op + " " + up.fragmentId + " · " + up.bytes + " B" + (up.truncated ? " (preview truncated)" : ""),
        up.html,
        true,
      );
    }
  } else if (r.kind === "resync") {
    sum.appendChild(el("span", "up", "↑ resync request"));
    sum.appendChild(el("span", "k", " last_applied_seq " + r.lastAppliedSeq + " · " + r.reason));
    detail(d, [
      ["last_applied_seq", r.lastAppliedSeq],
      ["reason", r.reason],
    ]);
  } else {
    sum.appendChild(el("span", "e", "↓ error " + r.error.code));
    sum.appendChild(el("span", "k", " " + r.error.message));
    errorDetail(d, r.error);
  }

  d.insertBefore(sum, d.firstChild);
  return d;
}

function detail(d, pairs) {
  var box = el("div", "d");
  for (var i = 0; i < pairs.length; i++) {
    var line = el("div");
    line.appendChild(el("span", "k", pairs[i][0] + ": "));
    line.appendChild(el("span", null, String(pairs[i][1])));
    box.appendChild(line);
  }
  d.appendChild(box);
}

function errorDetail(d, e) {
  detail(d, [
    ["code", e.code],
    ["message", e.message],
    ["fatal", e.fatal],
    ["event_id", e.eventId || "—"],
    ["client_ref", e.clientRef || "—"],
  ]);
}

// field renders a labelled value. Server-rendered markup goes through
// textContent into a <pre>: it is shown, never parsed and never executed.
function field(d, label, value, pre) {
  var box = el("div", "d");
  box.appendChild(el("span", "k", label + ": "));
  if (pre) box.appendChild(el("pre", null, value));
  else box.appendChild(el("span", null, value));
  d.appendChild(box);
}

// ---------------------------------------------------------------------------
// Boot
// ---------------------------------------------------------------------------

// The guard is what lets the specs import this module under node, where there
// is no document, no WebSocket and nothing to wrap — the same arrangement
// runtime.js uses for the same reason.
if (typeof document !== "undefined") {
  if (globalThis.gotthLive) {
    note(
      "order",
      "The gotth-live runtime booted before this inspector, so the socket it already opened is not being watched. " +
        "Render the inspector's script tag ABOVE live.Script's: both are deferred, and deferred scripts run in document order.",
    );
  }

  var Native = globalThis.WebSocket;
  if (Native) {
    // A function, not a class: `new` on a function returning an object yields
    // that object, so the runtime gets a genuine WebSocket with no proxy in
    // front of it and every property, event and constant behaves natively.
    var Wrapped = function (url, protocols) {
      var ws = new Native(url, protocols);
      if (protocols === PROTOCOL) attach(ws);
      return ws;
    };
    Wrapped.prototype = Native.prototype;
    for (var key in Native) Wrapped[key] = Native[key];
    Wrapped.CONNECTING = Native.CONNECTING;
    Wrapped.OPEN = Native.OPEN;
    Wrapped.CLOSING = Native.CLOSING;
    Wrapped.CLOSED = Native.CLOSED;
    globalThis.WebSocket = Wrapped;
  } else {
    note("wire", "this browser has no WebSocket, so there is nothing to inspect");
  }

  if (document.body) mount();
  else document.addEventListener("DOMContentLoaded", mount);
}
