// gotth-live browser runtime.
//
// One WebSocket carries encoded gotthlive.v1.Frame messages in both
// directions. Events go up, patches come down, and rendered HTML fragments are
// morphed into the live DOM rather than replacing it. Everything the browser
// needs is here and in the generated codec beside it: no dependencies, no CDN,
// no build step for the consumer, and no eval in any form (PRD NFR-4/5/6).
//
// The file is written compactly on purpose. The whole runtime is held to
// 12,288 bytes gzipped over the minified bundle (PRD NFR-2), roughly a third
// of what a general protobuf runtime alone would cost, so byte count is a
// design input here rather than an afterthought. client/SIZE.md carries the
// measured per-subsystem ledger, and the //#region markers below are what the
// measurement is taken against — they are load-bearing, not decoration.
//
// ---------------------------------------------------------------------------
// Attribute vocabulary — the contract with the server's templ helpers
// ---------------------------------------------------------------------------
//
// docs/api-surface.md §5.2 names the helpers; it does not fix their attribute
// spellings, so they are fixed here and the server side must match. The
// data-gotth-* family is used throughout, following the two spellings the
// design documents already commit to (data-gotth-preserve, data-gotth-status).
//
//	data-gotth-region="<fragment_id>"   Region(id). Marks a fragment root.
//	                                    Morph never touches anything outside
//	                                    one (RFC-0001 §10.1's ownership rule,
//	                                    which is what makes FR-31 safe by
//	                                    construction rather than by care).
//	data-gotth-on="<dom>:<name>[:<key>[:<debounceMs>[:<throttleMs>[:<fields>]]]][;...]"
//	                                    On(domEvent, eventName), and everything
//	                                    after the name is OnWith -> Bind.
//	                                    Several bindings on one element, e.g.
//	                                    "input:filter;change:commit", matched in
//	                                    order with the first match winning.
//	                                    Trailing empty components are trimmed, an
//	                                    empty one means "not set", and the key
//	                                    component is ONE KeyboardEvent.key value
//	                                    the event must carry — so a list of keys
//	                                    is one binding per key:
//	                                    "keydown:up:+;keydown:down:-".
//	                                    <fields> is URL-encoded, so
//	                                    URLSearchParams parses it for free, and
//	                                    the encoding escapes ":" and ";" so it
//	                                    cannot split the grammar it sits in.
//	                                    EVERY option is read off the matched
//	                                    binding and never off the element, which
//	                                    is FR-54 failure 2 — see dispatch().
//	data-gotth-preserve                 Preserve(). This element and its
//	                                    subtree are never morphed, removed or
//	                                    reordered — the sanctioned way to host
//	                                    HTMX- or third-party-owned DOM inside a
//	                                    live region (FR-27, FR-32).
//	data-gotth-status                   Written on <html> by the runtime:
//	                                    connecting | live | reconnecting |
//	                                    closed (RFC-0001 §8.2).
//
// FragmentUpdate.html means: for MORPH, the complete markup of the fragment
// root element including its data-gotth-region attribute; for APPEND and
// PREPEND, child markup to add inside the region.
//
// ---------------------------------------------------------------------------
// Scope, stated so nothing here reads as more than it is
// ---------------------------------------------------------------------------
//
// Implemented: connect and in-band version negotiation, heartbeat echo, the
// frame codec, event capture with its per-binding key filter and event
// serialization carrying causal ids, morph with
// the FR-25 preservation rules, acks, client telemetry, sequence-gap detection
// driving a ResyncRequest — retried on its own bounded schedule when the
// server's resync budget refuses it, and acknowledging what the client has
// while it waits — close-code classification, and — since checkpoint 3
// — RFC-0001 §8.4's reconnect state machine: exponential backoff with full
// jitter, paused while the tab is hidden and resumed the moment it is visible,
// with terminal close codes never retried. A reconnect is a NEW SESSION
// (§8.1), so the connection-scoped identifiers reset and the Snapshot that
// follows is morphed rather than replacing anything (§8.3).
//
// NOT implemented, deliberately: numeric and `matches` predicate enforcement
// on the client (see predicates.manifest.txt).
//
// NOT HERE, and not by omission: the dev-mode provenance inspector (FR-44).
// It is a separate opt-in file, client/inspector.js, built to its own artifact
// with its own ceiling, and it does not count against NFR-2 — which is why
// there is no hook for it anywhere in this file and there must not be one. It
// reads the session's frames off the WebSocket instead, by replacing the
// constructor before this file uses it. Nothing here knows it exists;
// client/SIZE.md §2.1 is the argument for keeping it that way.

// The one import, and the only file this one joins. It sits outside every
// //#region on purpose: the size tool measures a subsystem by deleting its
// region and re-bundling, and a region that owned the import would measure the
// codec's disappearance rather than its own.
import { decodeFrame, encodeFrame, ErrorCode, PatchOp, ResyncReason } from "./codec.gen.js";

//#region bootstrap
var VERSION = 1;

// Three constants used to sit here — A_FIELDS, A_DEBOUNCE and A_THROTTLE — and
// their absence is FR-54 failure 2. An attribute is read from the ELEMENT, and
// an element carries several bindings; those three options belong to one
// binding each and are now components of it. dispatch() says what that cost.
var A_REGION = "data-gotth-region",
  A_ON = "data-gotth-on",
  A_PRESERVE = "data-gotth-preserve";

var status = "";

function now() {
  return performance.now();
}

function setStatus(s) {
  status = s;
  document.documentElement.setAttribute("data-gotth-status", s);
}
//#endregion

//#region morph
// Idiomorph-class: id-based matching first, then a same-tag soft match at the
// cursor. Ours rather than vendored because the FR-25 preservation contract,
// the FR-27 opt-out and the fragment-ownership boundary all have to live
// inside the traversal — which is exactly why LiveView forks morphdom and
// Datastar inlined its own (RFC-0001 §10.1).
//
// Nothing between here and save() touches a global. It walks nodes through the
// DOM interface only, which is what lets the algorithm be specified against a
// shim with no browser at all (client/test/dom.mjs).

// composing holds the element with an active IME composition, if any. Morph
// never writes the value of that element (FR-26).
var composing = null;

function preserved(n) {
  return n.nodeType === 1 && n.hasAttribute(A_PRESERVE);
}

function idOf(n) {
  return n.nodeType === 1 && n.id ? n.id : null;
}

// pantry holds identified nodes that have been detached but may still be
// claimed by a later new child — the persistent-ID pantry RFC-0001 §10.1
// names. Without it, moving a list item from last position to first would
// destroy and rebuild every node it passed, taking their focus, scroll and
// media state with them.
//
// It is scoped to one top-level morph — a node may legitimately move between
// parents inside a single patch, but a node left over from an earlier patch
// must never be claimed by a later one. morphElement establishes that scope
// on entry rather than counting recursion depth, so an exception mid-traversal
// cannot leave a stale pantry behind for the next patch to draw from.
var pantry = null;

// match finds the old node that should become nc, scanning forward from cur.
//
// An id is strong evidence, so an id match is searched for through the whole
// remaining sibling list. Without one, only the node at the cursor is
// eligible, and only if it is the same kind, the same tag, and does not itself
// carry an id that some later new node may still claim.
//
// A preserved node is matched like any other. It has to be: skipping it would
// leave the new node with nothing to match, and the new node would then be
// inserted BESIDE the preserved one rather than reconciled with it, quietly
// duplicating the region. morphNode is where preservation is enforced, and
// enforcing it in one place is why that works.
//
// cur is non-null: morphChildren, the only caller, writes
// `m = cur ? match(cur, nc) : null` and so never calls with an exhausted
// cursor. That guard is the one worth keeping — it also skips the call — so
// this function does not repeat it (REV-DEL 2.10).
function match(cur, nc) {
  var id = idOf(nc),
    n;
  if (id) {
    for (n = cur; n; n = n.nextSibling) if (idOf(n) === id) return n;
    return null;
  }
  return cur.nodeType === nc.nodeType && (cur.nodeType !== 1 || (cur.tagName === nc.tagName && !cur.id)) ? cur : null;
}

// declared records what the SERVER last said about each <details> open state,
// and it is the only piece of DOM state this runtime keeps outside the DOM.
// The reason it has to is the whole of QA-1's D-15, so it is written down here
// rather than left to be rediscovered.
//
// The controlled/uncontrolled rule below reads attribute presence on the LIVE
// node as "what the server last declared". That reading is sound for
// checked, selected and value because the user never writes those attributes:
// an input's checkedness and an option's selectedness are separate pieces of
// state that the attribute only seeds, and typing sets the dirty-value flag
// rather than the value attribute. `details.open` is not like them. It is a
// plain REFLECTED IDL attribute, so opening a disclosure — by clicking the
// summary or by el.open = true — writes open="" into the DOM itself. From then
// on the live attribute has two authors and one bit, and the server's word is
// not recoverable from it. Morph read the user's own disclosure as a server
// declaration and reverted it, twice over: syncProps closed it and syncAttrs
// then removed the attribute.
//
// So the server's word is kept where the user cannot write it. Every sync
// records it; bind() seeds it from markup at first paint and for anything a
// patch inserted, which are exactly the two moments the live attribute IS the
// server's word and nothing else. The rule then reads like the checkbox rule
// and means the same thing: follow the server when the server CHANGES ITS
// MIND, and only then.
//
// It carries the same limitation as the checkbox rule, stated here rather than
// discovered later: a server whose declaration has not changed cannot override
// the user. Rendering <details open> and then <details> is how a server closes
// a disclosure, the same way rendering value="" is how it clears a box.
//
// The general shape, which FR-25 does not currently name: this is unsound for
// ANY attribute a browser reflects from user state, not for <details> alone.
// <dialog open> has the same shape, as does any custom element that reflects
// internal state. Only <details> is wired here, because only <details> is a
// case FR-25 names and every entry costs NFR-2 bytes; the record is keyed by
// element rather than by tag so that adding one is a branch, not a mechanism.
var declared = new WeakMap();

// syncProps applies the controlled/uncontrolled rule, and must run before
// attributes are synced because it reads the element's pre-sync state.
//
// The rule, which is the honest reading of FR-25's "uncontrolled input
// values": an attribute PRESENT in the incoming markup means the server is
// controlling that property, so the live property is set from it; an attribute
// ABSENT means the value is the user's, so it is left alone. That is why
// typing in a box survives a patch caused by somebody else's event, and why a
// server that wants to clear the box can still do so by rendering value="".
function syncProps(a, b, t) {
  if (t === "INPUT") {
    var ty = a.type;
    if (ty === "checkbox" || ty === "radio") {
      if (b.hasAttribute("checked") !== a.hasAttribute("checked")) a.checked = b.hasAttribute("checked");
    } else if (b.hasAttribute("value") && a !== composing) {
      a.value = b.getAttribute("value");
    }
  } else if (t === "TEXTAREA") {
    // A textarea's declared value is its text content, not an attribute.
    if (b.textContent !== "" && a !== composing) a.value = b.textContent;
  } else if (t === "OPTION") {
    if (b.hasAttribute("selected") !== a.hasAttribute("selected")) a.selected = b.hasAttribute("selected");
  } else if (t === "DETAILS") {
    // Compared against the server's last word, never against the live
    // attribute — see declared, above. The fallback is for an element no
    // bind() has seen, where the live attribute is still the only record
    // there is; bind() runs at first paint and after every patch, so it is
    // reached only by a caller driving morph directly.
    var o = b.hasAttribute("open");
    if (o !== (declared.has(a) ? declared.get(a) : a.hasAttribute("open"))) a.open = o;
    declared.set(a, o);
  }
}

// syncAttrs makes the live attributes equal the incoming ones, with exactly
// one exclusion: <details open>. syncProps has already ruled on that bit under
// the rule above, and because open REFLECTS, copying the attribute through
// here would silently overrule it. That second write is the other half of
// D-15: the user's disclosure was reverted once by each function, so fixing
// either one alone fixes nothing.
function syncAttrs(a, b, t) {
  var i,
    at = a.attributes,
    bt = b.attributes,
    n,
    skip = t === "DETAILS" ? "open" : "";
  for (i = bt.length - 1; i >= 0; i--) {
    n = bt[i];
    if (n.name !== skip && a.getAttribute(n.name) !== n.value) a.setAttribute(n.name, n.value);
  }
  for (i = at.length - 1; i >= 0; i--) {
    n = at[i];
    if (n.name !== skip && !b.hasAttribute(n.name)) a.removeAttribute(n.name);
  }
}

// morphNode reconciles one node in place. In place is the whole point: a node
// that is never replaced keeps its focus, its caret, its scroll offset, its
// media playback position and its running CSS transitions for free, which is
// most of FR-25 obtained by not doing something rather than by restoring it
// afterwards.
function morphNode(a, b) {
  if (preserved(a)) return;
  if (a.nodeType !== 1) {
    if (a.nodeValue !== b.nodeValue) a.nodeValue = b.nodeValue;
    return;
  }
  if (a.tagName !== b.tagName) {
    a.replaceWith(b);
    return;
  }
  morphEl(a, b);
}

// morphElement reconciles two elements known to share a tag. It is also the
// fragment-root entry point: a MORPH patch calls it with the live region
// element and the newly rendered one, so the region anchor itself survives.
export function morphElement(a, b) {
  pantry = {};
  morphEl(a, b);
  pantry = null;
}

function morphEl(a, b) {
  if (preserved(a)) return;
  var t = a.tagName;
  syncProps(a, b, t);
  syncAttrs(a, b, t);
  // An input has no children, and a textarea's children ARE its value, which
  // syncProps has already handled under the controlled/uncontrolled rule.
  if (t !== "INPUT" && t !== "TEXTAREA") morphChildren(a, b);
}

// drop detaches an old node the server no longer renders at this position. A
// preserved node is never detached at all; an identified one is put in the
// pantry rather than discarded, because a later new child may still claim it.
function drop(op, d) {
  if (preserved(d)) return;
  op.removeChild(d);
  var id = idOf(d);
  if (id) pantry[id] = d;
}

function morphChildren(op, np) {
  var cur = op.firstChild,
    nc = np.firstChild,
    nn,
    m,
    d,
    id,
    mn;
  while (nc) {
    nn = nc.nextSibling;
    m = cur ? match(cur, nc) : null;
    if (!m && (id = idOf(nc)) && pantry[id]) {
      // Claimed from the pantry: this node moved rather than being replaced.
      m = pantry[id];
      delete pantry[id];
      op.insertBefore(m, cur);
      morphNode(m, nc);
    } else if (m) {
      while (cur !== m) {
        d = cur;
        cur = cur.nextSibling;
        drop(op, d);
      }
      // The cursor advances to what came AFTER m before m was reconciled, and
      // it has to be read first. An id match with a changed tag sends
      // morphNode down the replaceWith path, which detaches m — and a detached
      // node's nextSibling is null, so reading it afterwards would end the
      // walk early and append every remaining new child beside the old ones it
      // should have matched. Measured, before the read was hoisted: an
      // id-matched <div> becoming a <section> duplicated all four siblings
      // after it and lost their node identity. Reading it first is identical
      // in the ordinary case, because morphNode only ever touches m's own
      // subtree.
      mn = m.nextSibling;
      morphNode(m, nc);
      cur = mn;
    } else {
      op.insertBefore(nc, cur);
    }
    nc = nn;
  }
  while (cur) {
    d = cur;
    cur = cur.nextSibling;
    drop(op, d);
  }
}

// save captures the state that survives only if it is restored: focus and
// caret when the focused node did get replaced, and scroll offsets of
// identified descendants. Scoped to the fragment being patched, so the cost is
// proportional to what changed rather than to the page.
//
// # When this pair is the only thing standing between FR-25 and a regression
//
// Almost never, and that is the point of writing it down. Morph keeps focus,
// caret, scroll, media position and running transitions by not replacing the
// node that holds them, so on the overwhelming majority of patches save() and
// restore() capture state that was never in danger and restore nothing.
//
// They earn their bytes in exactly one situation: the node holding the state
// was REPLACED rather than reconciled. In this runtime that means an
// id-matched element whose tag changed — morphNode's replaceWith path, which a
// server hits by rendering <div id="card"> for one state and <article
// id="card"> for another — and it means the subtree under such an element,
// which comes back as fresh server markup with the same ids and none of the
// state. Focus, caret and scroll are recoverable there because they are keyed
// by id; nothing else in FR-25's list is, which is why nothing else is tried.
//
// QA-1 measured this pair at 617 minified bytes with no test in the repository
// that failed when it was removed (D-21). It is not dead code — the tag-change
// case above is real and reachable — it was untested, so the tag-change case
// is now a spec: test/internal/conformance/dom_preservation_test.go, "restores
// focus, the caret and scroll across a patch that REPLACED the node holding
// them". That spec asserts the replacement actually happened before it asserts
// anything was restored, so it cannot quietly become a second test of morph.
function save(root) {
  var ae = document.activeElement,
    s = { a: null, sc: [] },
    els,
    i,
    e;
  if (ae && ae.id && root.contains(ae)) {
    s.a = { id: ae.id, n: ae, s: null, e: null };
    try {
      s.a.s = ae.selectionStart;
      s.a.e = ae.selectionEnd;
    } catch (x) {
      /* not a text control; selection does not apply */
    }
  }
  els = root.querySelectorAll("[id]");
  for (i = 0; i < els.length; i++) {
    e = els[i];
    if (e.scrollTop || e.scrollLeft) s.sc.push([e.id, e.scrollTop, e.scrollLeft]);
  }
  return s;
}

function restore(s) {
  var a = s.a,
    i,
    e;
  if (a && document.activeElement !== a.n) {
    e = document.getElementById(a.id);
    if (e) {
      e.focus();
      if (a.s !== null) {
        try {
          e.setSelectionRange(a.s, a.e);
        } catch (x) {
          /* the replacement is not a text control */
        }
      }
    }
  }
  for (i = 0; i < s.sc.length; i++) {
    e = document.getElementById(s.sc[i][0]);
    if (e) {
      e.scrollTop = s.sc[i][1];
      e.scrollLeft = s.sc[i][2];
    }
  }
}

// ---------------------------------------------------------------------------
// The single DOM mutation entry point (review-checklist §4.4, §7.6)
//
// apply is the ONLY function in this runtime that mutates application DOM, and
// it takes the patch that authorises the mutation. There is no second path and
// no ad-hoc innerHTML write anywhere else; grep for innerHTML and it appears
// once, in parse() below.
//
// One carve-out, named rather than hidden: setStatus writes data-gotth-status
// on <html>. That is connection state on the document element, outside every
// declared fragment, and RFC-0001 §8.2 requires it.
// ---------------------------------------------------------------------------

function region(id) {
  return document.querySelector("[" + A_REGION + '="' + id + '"]');
}

function parse(html) {
  var t = document.createElement("template");
  t.innerHTML = html;
  return t.content;
}

function apply(p) {
  var t0 = now(),
    mt = 0,
    ups = p.updates || [],
    i,
    u,
    el,
    frag,
    t1,
    st,
    root;
  for (i = 0; i < ups.length; i++) {
    u = ups[i];
    el = region(u.fragment_id);
    if (!el) continue; // an unknown fragment id is the server's problem, not a crash
    if (u.op === PatchOp.REMOVE) {
      el.remove();
      continue;
    }
    frag = parse(u.html || "");
    t1 = now();
    if (u.op === PatchOp.APPEND) el.appendChild(frag);
    else if (u.op === PatchOp.PREPEND) el.insertBefore(frag, el.firstChild);
    else {
      st = save(el);
      root = frag.firstElementChild;
      if (root) morphElement(el, root);
      restore(st);
    }
    mt += now() - t1;
    bind(el);
  }
  return [mt, now() - t0];
}
//#endregion

//#region events
// One delegated listener per bound DOM event type, at the document, in the
// capture phase so that events which do not bubble (focus, blur) arrive too.
// No per-node handler exists, so a morphed subtree is interactive with no
// re-binding step and morph cannot destroy a binding (FR-28).
//
// bind() attaches nothing to the elements themselves. It does two things to a
// piece of freshly arrived server markup: discovers which event TYPES to
// listen for, and records the server's word about the reflected state morph
// cannot otherwise recover (the declared WeakMap in the morph region, and the
// note above it, which is the rule).
//
// Both jobs want the same walk at the same two moments — first paint, and
// every region after every patch — so they share one querySelectorAll rather
// than taking one each.
//
// The record is written only where there is none. After a patch the live open
// attribute may be the user's rather than the server's, since syncAttrs
// deliberately leaves it alone, and re-reading it here would hand morph the
// user's own disclosure back as if the server had declared it. That is a way
// of fixing D-15 that reintroduces it one patch later.
//
// Exported on the same footing as morphElement, and for the same reason: the
// node suite has to be able to establish the first-paint record the way the
// runtime does, or its D-15 spec would be asserting against a different rule
// from the one that ships.

var listening = {},
  timers = new WeakMap(),
  pending = new Set();

export function bind(root) {
  var els = root.querySelectorAll("[" + A_ON + "],details"),
    i,
    j,
    specs,
    t,
    e,
    on;
  for (i = 0; i < els.length; i++) {
    e = els[i];
    if (e.tagName === "DETAILS" && !declared.has(e)) declared.set(e, e.hasAttribute("open"));
    on = e.getAttribute(A_ON);
    if (!on) continue;
    specs = on.split(";");
    for (j = 0; j < specs.length; j++) {
      t = specs[j].split(":")[0];
      if (t && !listening[t]) {
        listening[t] = 1;
        document.addEventListener(t, dispatch, true);
      }
    }
  }
}

// fields serializes form state. Forms and single controls take the SAME path,
// which is the point of api-surface.md's one On() covering submit and input
// alike: if there is a form, FormData is the answer, and it already gets the
// hard case right — an unchecked checkbox is ABSENT rather than empty, which
// is exactly the distinction live.Fields.Lookup exists to report.
//
// `st` is the matched binding's own static fields, passed in rather than read
// off the element. What the element contributes — its form, or its own name and
// value — is genuinely the element's and is the same for every binding on it;
// what Bind.Fields declares is one binding's payload, and two bindings on one
// element are entitled to different ones. Two keys raising the same event with
// different `dir` values is the case that makes this obvious, and it is the
// same shape as the benchmark counter's two-keys-one-element requirement.
function fields(el, st) {
  var out = [],
    form = el.form || (el.tagName === "FORM" ? el : el.closest("form"));
  if (form) {
    new FormData(form).forEach(function (v, k) {
      if (typeof v === "string") out.push({ key: k, value: v });
    });
  } else if (el.name) {
    if (el.type !== "checkbox" && el.type !== "radio") out.push({ key: el.name, value: el.value });
    else if (el.checked) out.push({ key: el.name, value: el.value || "on" });
  }
  if (st) {
    new URLSearchParams(st).forEach(function (v, k) {
      out.push({ key: k, value: v });
    });
  }
  return out;
}

// EVERY option a binding declares is a component of that binding, and dispatch
// reads all of them off the spec it matched. Nothing here reads an option off
// the element.
//
// A binding's optional third component is a key filter, and it is what makes
// keydown expressible at all. Without one, data-gotth-on="keydown:chat.send"
// raises an event on EVERY key including Tab, Shift and the arrows: a frame per
// keystroke, and a message sent the first time somebody moves the caret
// (examples/chat FRICTION.md F-3).
//
// It lives inside the binding rather than in an attribute of its own, which is
// the shape that was proposed, because an attribute is read from the ELEMENT
// and an element carries several bindings. A composer bound
// "input:chat.draft;keydown:chat.clear" with a key attribute beside it would
// filter the INPUT binding by a key an input event does not have, and the draft
// would silently stop being sent. Per binding the two cannot interfere, and two
// keys can raise two DIFFERENT events from one focused element — which is
// exactly what the benchmark counter's "+ and - apply +1 and -1" needs and what
// a per-element filter cannot express at all.
//
// THAT ARGUMENT WAS WON HERE AND NOT CARRIED ACROSS TO THE THREE OPTIONS SITTING
// BESIDE IT, which is FR-54 failure 2 and the reason components four, five and
// six exist. Debounce, Throttle and Fields were attributes of the element for
// one release longer, and the paragraph above turned out to describe them
// exactly: the guide's own composer put an Escape binding beside a 150 ms input
// binding, the Escape read the element's interval and armed the element's one
// timer, and the input event three milliseconds later called clearTimeout on it.
// The clear was not delayed and not reordered — it was destroyed, with no error,
// no console warning and no frame on the wire. Symmetrically, an Escape inside
// the window destroyed a pending draft, so the server never learned what was
// typed while the browser went on showing it. QA-1 drove all of it in Chromium:
// docs/qa/fr-54-debounce-repro.md, verdict REPRODUCES.
//
// The interval and the timer SLOT are two defects with one cause, and fixing
// either alone leaves the other. A per-binding timer with a per-element interval
// still delays a key binding by 150 ms for a reason its author never wrote down;
// a per-binding interval with a per-element timer still loses an event whenever
// two bindings on one element both debounce. Both are per binding below, and
// client/test/binding.test.mjs has a spec for each.
//
// ONE key per binding, so a list is several bindings and there is no second
// separator to reserve. A comma is the obvious choice and a comma is a legal
// e.key value; ":" and ";" are already spent on this grammar, and a key that is
// one of those two being unbindable is the price. live.Bind.Keys says so. The
// three components added after the key spend nothing further: they are more
// fields of a ":" split that was happening anyway, so the set of characters a
// key may not be is exactly what it was.
//
// It compares e.key, and reads a modifier only where the binding asked it to,
// which is a decision rather than an omission:
//
//   - a printable key already carries its modifiers. Shift and "=" arrive as
//     "+", which is why a requirement can say "+" and not "Shift+Equal". That
//     is also why component 7 is "no modifier held" and NOT a modifier set: a
//     default of "none held" would stop "+" matching anything at all.
//   - a modifier pressed ALONE is "Shift"/"Control"/"Alt"/"Meta" and matches no
//     filter, so F-3's frame-per-keystroke is fixed for those too.
//   - Ctrl and Meta chords belong to the browser and to the operating system.
//     Nothing here expresses one, and a filter that matched one would raise an
//     event while the browser did its own thing anyway. The full modifier set
//     is REFUSED (docs/reviews/fr-54.md §13) rather than unimplemented.
//
// Components 7 and 8 are FR-54 failure 1, and each is one subscript into the
// split that was already happening:
//
//   - s[6], "no modifier held": the four *Key booleans, any of which held is a
//     press this binding does not match. It does not break the loop, so the
//     NEXT binding for the same DOM event gets its turn — which is what makes
//     "Enter sends, Shift+Enter does not" one element and two bindings
//     (F-CHT-3). AltGr sets ctrlKey AND altKey; CapsLock and NumLock set none
//     of the four. live.Bind.NoModifiers documents both.
//   - s[7], preventDefault for THIS binding. Its PLACEMENT is the whole of it:
//     it is below the composition guard, not beside the submit and anchor
//     cases above it. Enter during an IME composition commits the candidate,
//     so suppressing it there would take the commit key away from every
//     composer that uses one — the population FR-26 exists for. The submit and
//     anchor cases stay where they are, because moving THEM below the guard
//     would let a form submit for real mid-composition, which is a second
//     defect rather than a fix for the first.
//
// An event with no key property never matches a filtered binding: a filter
// filters, so a key filter on a click fires never rather than always. Matching
// is exact and case-sensitive against the UI Events value — "Escape" not "Esc",
// "ArrowUp" not "Up", " " not "Space" — and nothing here normalises, because
// "a" and "A" are different keys and the name set belongs to the UI Events
// specification rather than to this project.
function dispatch(e) {
  var el = e.target && e.target.closest ? e.target.closest("[" + A_ON + "]") : null;
  if (!el) return;

  var name = null,
    specs = el.getAttribute(A_ON).split(";"),
    i,
    s;
  for (i = 0; i < specs.length; i++) {
    s = specs[i].split(":");
    if (
      s[0] === e.type &&
      (!s[2] || s[2] === e.key) &&
      (!s[6] || !(e.shiftKey || e.ctrlKey || e.altKey || e.metaKey))
    ) {
      name = s[1];
      break;
    }
  }
  // Not ours. Do not touch it — hx-* behaviour on this page is untouched
  // (FR-31), and that promise is kept by this line.
  if (!name) return;

  if (e.type === "submit" || (e.type === "click" && el.tagName === "A")) e.preventDefault();
  if (composing) return; // never send mid-composition (FR-26)
  // BELOW the guard, and the two lines are not interchangeable: see the note
  // on s[7] above. A key suppressed mid-composition is a broken IME.
  if (s[7]) e.preventDefault();

  var frag = el.closest("[" + A_REGION + "]");
  if (!frag) return;
  // s is the spec the loop above matched, and every option comes off it. An
  // absent component is undefined, +undefined is NaN, and NaN || 0 is 0 — so a
  // trimmed binding costs no extra test.
  var fid = frag.getAttribute(A_REGION),
    fs = fields(el, s[5]),
    d = +s[3] || 0,
    th = +s[4] || 0,
    st,
    r;

  // The record is per ELEMENT and then per BINDING within it, keyed by the spec
  // string. timers stays a WeakMap on the element so a removed node still takes
  // its timers with it; the inner object is what keeps one binding's pending
  // send out of another's reach. Two bindings whose specs are byte-identical
  // are the same binding twice and correctly share one record.
  //
  // Nothing is stored for a binding that neither debounces nor throttles, which
  // is most of them: an element that only clicks never enters the map at all.
  if (d || th) {
    st = timers.get(el);
    if (!st) timers.set(el, (st = {}));
    r = st[specs[i]] || (st[specs[i]] = {});
  }
  if (th) {
    if (r.l && now() - r.l < th) return;
    r.l = now();
  }
  if (d) {
    clearTimeout(r.t);
    pending.delete(r.t);
    r.t = setTimeout(function () {
      pending.delete(r.t);
      sendEvent(name, fid, fs);
    }, d);
    pending.add(r.t);
    return;
  }
  sendEvent(name, fid, fs);
}
//#endregion

//#region provenance
// The causal identifiers, the ack window's client half, and the telemetry the
// server correlates a morph duration against.

var sid = null, // the 16-byte session id, learned from the first Snapshot
  seq = 0, // highest CONTIGUOUS server_seq applied; also what we ack
  ref = 0, // client_ref, monotonic from 1 per connection (protocol.md §4.1)
  gap = false, // a resync is outstanding: patches are discarded until it lands
  gapTries = 0, // consecutive refusals, the n in the retry schedule below
  gapTimer = 0; // the armed retry, 0 when none — one at a time, like retry

// newSession clears everything scoped to one CONNECTION, and open() calls it
// on every attempt rather than only the first.
//
// RFC-0001 §8.1: session lifetime is exactly connection lifetime. There is no
// resume and there must not be one, so a reconnect is not this session
// continuing over a new socket — it is a different session that the server
// will Mount from scratch and describe with a Snapshot whose server_seq starts
// again at 1. Every identifier below belongs to the connection that is gone,
// and each one has its own way of being wrong if it survives:
//
//	sid   send() refuses to write anything without it, so clearing it is what
//	      makes "the client sends nothing before this connection's first
//	      Snapshot" (H-10) true PER CONNECTION rather than per page. A frame
//	      carrying the previous session's id is protocol.md H-3, and the
//	      server answers it by closing 4002.
//	seq   seen_server_seq is the causation edge: it must name a patch the user
//	      saw IN THIS SESSION. Carried over, the first event of the new
//	      session would cite a server_seq the new actor has not reached.
//	ref   client_ref is monotonic from 1 per connection (protocol.md §4.1).
//	gap   a resync outstanding when the socket dropped must not latch:
//	      resync() returns early while the flag is set, so a later gap would
//	      be detected and never reported.
//	gapTimer
//	      a retry armed against the OLD session must not fire into the new
//	      one. It would ask for a resync the new session has no gap for,
//	      which the server answers with the no-op Ack — harmless, and still a
//	      frame nobody asked for, sent for a connection that no longer exists.
//	      gapTries goes with it: the schedule is per gap, and the new session
//	      has not had one.
//
// Two of the four have a spec that fails when they are deleted (sid, ref) and
// TWO DO NOT, which is worth writing down rather than leaving for somebody to
// discover by deleting a line and watching nothing go red:
//
//	seq   is dominated by sid. Nothing can be sent before the Snapshot
//	      arrives, and the Snapshot sets seq on its way through applied().
//	gap   is dominated by applied(), which clears it for every frame it
//	      applies — including the Snapshot that ends the reconnect.
//
// They stay because they are the same piece of state as the other two, and
// because both dominations are orderings between other functions rather than
// properties of this one: the day the session id is learned from the handshake
// instead of from the first Snapshot, the stale seq becomes an H-10 violation
// with nothing to catch it. Resetting two of four fields and relying on
// somebody else's order for the rest is the sort of invariant that holds until
// it silently does not.
function newSession() {
  sid = null;
  seq = 0;
  ref = 0;
  gap = false;
  gapTries = 0;
  clearTimeout(gapTimer);
  gapTimer = 0;
}

function sendEvent(name, fid, fs) {
  // H-10: the client sends nothing before the first Snapshot. seen_server_seq
  // is the causation edge, so it must name a patch the user actually saw.
  if (!seq) return;
  send({
    event: {
      client_ref: ++ref,
      name: name,
      fragment_id: fid,
      seen_server_seq: seq,
      fields: fs,
    },
  });
}

// us clamps a millisecond duration to the microsecond uint32 the schema bounds
// at 60 s. A hostile value cannot originate here, but a clock anomaly can.
function us(ms) {
  var v = Math.round(ms * 1000);
  return v < 0 ? 0 : v > 60000000 ? 60000000 : v;
}

// applied is the one place `seq` moves, so it is the one place the two things
// that could move it wrongly are checked (REV-INV U-1, U-2).
//
// # The sequence must go forward (U-2)
//
// `seq` is the client's high-water mark and the number every ack, every
// `seen_server_seq` and every `last_applied_seq` is drawn from. A frame that
// moved it BACKWARDS would make the next ack go backwards too, and the server
// closes that as 4002 under H-7 — so the failure already ended the session,
// it just ended it naming the client's ack rather than the server's frame.
// Checking here costs one comparison and turns a confusing eviction into a
// close code that says what actually happened.
//
// The patch path cannot reach this: onMessage discards anything that is not
// `seq + 1` before it gets here. The SNAPSHOT path can, and had no check of
// any kind — a Snapshot was applied at whatever `server_seq` it carried, from
// zero relation to what the client held.
//
// # The supersession range must meet what the client actually holds (U-1)
//
// H-13 names its enforcement as `validateSnapshot` "on both the outbound
// boundary and THE CLIENT DECODER". The client decoder decoded fields 10 and
// 11 and read neither, so the normative table claimed an enforcement that
// shipped in one of the two places it named. This is that second place.
//
// The rule, from protocol.md §4.3 and the H-13 row: both fields are 0 (a
// session's first Snapshot) or both are non-zero with
// `from <= through < server_seq` (a resync Snapshot), and the range is the
// inclusive `server_seq` span this Snapshot replaces — everything the server
// emitted after the `last_applied_seq` the client asked with. `ask()` sends
// `seq`, and `seq` cannot move while a gap is latched, so on arrival the range
// must begin at exactly `seq + 1`. Anything else is one of two errors and both
// are worth a close rather than a silent apply:
//
//	from > seq + 1   a hole: (seq, from) is neither applied nor superseded, so
//	                 the DOM the user is looking at has no accounted-for cause.
//	from <= seq      an overlap: the range covers sequences already applied,
//	                 which is P7's non-overlap failing on the wire. REV-INV
//	                 BR-9 is the server-side half of this — a `from` clamped to
//	                 `max(last_applied, acked) + 1` makes the two sides agree by
//	                 construction, and until it lands the reachable case is a
//	                 resync answered twice, whose second Snapshot supersedes a
//	                 range the first one already replaced.
//
// A Patch carries neither field, so both read as 0 and the check is the
// both-zero arm — the same arm a session's first Snapshot takes.
//
// H-13's third clause — `Origin.kind == RESYNC` iff the fields are non-zero —
// stays on the outbound boundary only, and that is a size decision rather than
// an oversight: `OriginKind` is not imported here, and the generated enum is
// one object, so importing it to compare one member ships all six the way
// `ErrorCode` ships all eight (SIZE.md §1.1.3 measured that at 126 gzipped
// bytes). The range clause is the half that constrains what this client does
// next; the kind clause only labels the frame.
function applied(p) {
  if (p.server_seq <= seq) return close(4002, "server_seq " + p.server_seq + " at " + seq);
  // Absent varints decode as undefined, so both are read through `|| 0`: a
  // `through` left undefined against a non-zero `from` has to compare as 0 and
  // fail, and `undefined < from` is false, which would have let it through.
  var from = p.superseded_from_seq || 0,
    through = p.superseded_through_seq || 0;
  if (from ? from !== seq + 1 || through < from || through >= p.server_seq : through)
    return close(4002, "supersession " + from + "-" + through + " at " + seq);
  var t = apply(p);
  seq = p.server_seq;
  // A Patch or a Snapshot is the only thing that ends a gap, and applying one
  // ends it: the latch, and any retry armed for it. Leaving the timer behind
  // would send a request for a gap that has just been closed.
  //
  // gapTries is deliberately NOT reset here, and the omission is measured
  // rather than assumed: resync() zeroes it when it latches the next gap, so a
  // reset here is a line no spec can turn red — it was written, found
  // unfalsifiable by mutation, and removed. Disarming the timer is the
  // opposite: "a Snapshot that lands while a retry is armed disarms it" fails
  // without the two lines below, and it is the invariant that keeps a request
  // from going out for a gap that is already closed.
  gap = false;
  clearTimeout(gapTimer);
  gapTimer = 0;
  // The ack is a cumulative high-water mark, so one per applied frame is safe
  // and a dropped one is harmless: the next supersedes it (RFC-0001 §7.1).
  send({ ack: { server_seq: seq } });
  send({ client_telemetry: { patch_id: p.patch_id, morph_micros: us(t[0]), apply_micros: us(t[1]) } });
}

// ---------------------------------------------------------------------------
// The gap latch and its retry — QA-2's D-29
// ---------------------------------------------------------------------------
//
// A gap latches: the client stops applying and asks for a Snapshot, and until
// one arrives every later patch is discarded, because applying past a gap
// would put the DOM in a state no server render ever produced (FR-11).
//
// The latch used to be cleared in exactly one place, applied(), and the server
// has an answer that goes through neither Patch nor Snapshot: RFC-0001 §7.6
// gives ResyncRequest its own budget — MinResyncInterval 1 s, ResyncBurst 3 —
// and a request over it is answered with Error{RATE_LIMITED} and NO RENDER. So
// a client that was refused stayed latched for ever. It discarded every
// subsequent patch, and — because the ack is written by applied() — it stopped
// acknowledging as a side effect of having stopped applying, so the server's
// outbound window filled, stayed full, and RFC §7.4's slow-client eviction
// closed the connection after slow_client_grace. Recovery was a reconnect and
// a full re-mount about thirty seconds later, for a refusal §7.6 describes as
// costing "no render". QA-2 measured the refusal rate on a 53 Hz stream at
// 20–25 % of LEGITIMATE requests at 5–25 % patch loss (checkpoint-3-chaos.md
// §7.2): the refusals come from losses clustering inside one second, not from
// a client asking too often.
//
// Two things follow, and they are separate.
//
// # 1. A refusal re-arms the request, not the detector
//
// refused() schedules another request. It does NOT clear `gap`: the client is
// still missing a patch, so it must still discard what it cannot apply. What
// the refusal changes is that there is now a request pending again rather than
// a request that will never be repeated.
//
// # 2. The client keeps acknowledging what it HAS
//
// onMessage acks a discarded patch at the sequence the client actually holds.
// It is honest — an ack is the highest CONTIGUOUS sequence applied (§3.2), and
// while latched that number does not move — and it is legal: the server's H-7
// check refuses an ack that goes backwards or names a patch never sent, and
// this one does neither. The rate is not a flood either: it is one ack per
// patch received, exactly the rate the client was already sending before the
// gap. What it buys is that a latched client is no longer INDISTINGUISHABLE
// FROM A DEAD ONE — the window depth keeps being reported (instrumentation
// §2.3), and the client's high-water mark is on the wire rather than inferred
// from silence. It does not, and cannot, drain the window: only a Snapshot
// moves that mark, which is why the retry above is the recovery and this is
// not.
//
// # The schedule, and the number the wire does not carry
//
// RESYNC_BASE is 1 s because RFC §7.6's DEFAULT MinResyncInterval is 1 s, and
// that default is every scrap of information the client has: Error carries a
// code, a message, the causal ids and `fatal` (protocol.md §3.3) and no
// retry-after, and the Snapshot's session parameters carry the heartbeat
// interval, the frame cap and the ack window but not the resync budget. So an
// operator who lengthens MinResyncInterval tells the client nothing, and the
// schedule below is a guess that grows until it stops being wrong. Closing
// that would be a wire change; it is filed, not smuggled in here.
//
// The delay is bound/2 + random(0, bound/2) over bound = min(CAP, base·2^n) —
// EQUAL jitter, and deliberately not the FULL jitter §8.4 uses for reconnect,
// which is the one place this file has two schedules that disagree. Full
// jitter draws from zero upwards to spread a herd of tabs that were all
// disconnected by the same event; the floor is what would land them in a band.
// A refused resync has no herd: the resync bucket is per session, this client
// alone was refused, and a delay near zero is precisely the request the server
// has just declined — spent, counted against the same bucket, and one step
// closer to the sustained-abuse close. So here the floor is the point. With
// the defaults the first retry lands in [500, 1000) ms against a bucket that
// refills a token a second, so a legitimate client is served on its first or
// second retry and the server sees at most one extra refusal.
//
// It is bounded in three ways, which together are why a client the server
// refused cannot answer by shouting. At most one timer is armed, so at most
// one request is in flight per gap. The delay at least doubles per refusal, so
// n refusals cost n requests spread over a geometrically growing window
// (Math.pow overflows to Infinity long before n could, and Math.min then
// yields the cap, so the schedule is total without a clamp). And the ceiling
// is CAP — transport's, shared rather than copied, because a second constant
// would be a second policy.
//
// If the server refuses everything anyway — a MinResyncInterval far above the
// default, or an actor that has stopped granting — the schedule reaches the
// server's own sustained-abuse close (3 × ResyncBurst consecutive denials,
// 4008) and that close is in the RETRIED set, so the outer reconnect loop
// takes over. The pathological case degrades to what happened before this
// existed; the ordinary case no longer reaches it.
var RESYNC_BASE = 1000;

// ask writes the outstanding request. It is the only place a ResyncRequest is
// constructed, so "one request per gap, naming the sequence the client really
// has" is a property of this function rather than of its three callers.
function ask() {
  gapTimer = 0;
  send({ resync_request: { last_applied_seq: seq, reason: ResyncReason.GAP } });
}

function resync() {
  if (gap || !seq) return;
  gap = true;
  gapTries = 0;
  ask();
}

// refused re-arms after Error{RATE_LIMITED}.
//
// The guard is `gap` — the client's own state — and not the error's causal
// ids, because the ids cannot answer the question. A refused resync's
// Error names a SERVER-minted event_id (a ResyncRequest carries no client_ref
// of its own, so the server stands one in), which is indistinguishable from
// the client_ref on an Error refusing an ordinary event. Conditioning on the
// latch is sound in both directions: while latched the only way forward is a
// Snapshot, so asking again is right whichever frame the server was refusing,
// and while not latched there is nothing to ask for and this does nothing at
// all. An event flood therefore cannot be turned into a resync flood.
function refused() {
  if (!gap || gapTimer) return;
  var b = Math.min(CAP, RESYNC_BASE * Math.pow(2, gapTries++)) / 2;
  gapTimer = setTimeout(ask, b + Math.random() * b);
}
//#endregion

//#region transport
var sock = null,
  // endpoint is the resolved ws:/wss: URL. start() learns it once, so every
  // later attempt is open() with no arguments and there is exactly one place
  // that constructs a socket.
  endpoint = null,
  // attempt is n in §8.4's delay = random(0, min(cap, base·2^n)). It counts
  // CONSECUTIVE failures, and it returns to zero only when a connection
  // reaches "live" — not when the socket opens. That is deliberate and it is
  // the case backoff is actually for: a server that accepts the TCP
  // connection and then closes it, which is what a crash-looping or draining
  // server looks like from here. Resetting on open would hand exactly that
  // server 250 ms retries for ever, from every tab at once.
  attempt = 0,
  // retry is the pending timer handle, 0 when none is armed. A hidden tab
  // holds none at all — see vis().
  retry = 0;

// RFC-0001 §8.4's constants. The shape of the jitter is not decoration:
// LiveView ships RELOAD_JITTER_MIN/MAX because synchronised remount storms
// after a deploy are a real production failure (teardown §1.5). FULL jitter
// draws from the whole interval starting at zero; equal jitter (b/2 +
// random(0, b/2)) and decorrelated jitter both keep a floor under the delay,
// and a floor is precisely what lands a thousand reconnecting tabs in a narrow
// band instead of spread across the window. Full jitter is also the cheaper
// and better-studied form, so there is nothing to trade off.
var BASE = 250,
  CAP = 15000;

function send(payload) {
  if (!sock || sock.readyState !== 1 || !sid) return false;
  payload.protocol_version = VERSION;
  payload.session_id = sid;
  sock.send(encodeFrame(payload));
  return true;
}

function onMessage(e) {
  // Opcode 0x1 is a protocol violation: every payload is a binary Frame
  // (docs/protocol.md §1).
  if (typeof e.data === "string") return close(4002, "text frame");

  var f;
  try {
    f = decodeFrame(new Uint8Array(e.data));
  } catch (x) {
    return close(4002, "undecodable frame");
  }

  if (f.snapshot) {
    // Version negotiation's in-band half. The subprotocol token is a fast
    // reject, not the source of truth: the negotiated version is re-asserted
    // here and checked here (docs/protocol.md §1, §8.2).
    if (f.protocol_version !== VERSION) return close(4003, "protocol version " + f.protocol_version);
    sid = f.session_id;
    // Reaching "live" is what ends a backoff sequence — see attempt, above.
    // This is also the only transition into "live", so §8.2's
    // live → reconnecting → live is one line here and one line in onClose.
    attempt = 0;
    setStatus("live");
    applied(f.snapshot);
  } else if (f.patch) {
    // FR-11 gap detection: stop applying immediately and ask for a snapshot.
    // Applying past a gap would put the DOM in a state no server render ever
    // produced, which is worse than a visible pause.
    //
    // The discarded patch is still acknowledged, at the sequence the client
    // actually holds rather than the one it just refused to apply — see the
    // gap latch's note. resync() is the no-op it has always been while a
    // request is already outstanding; the ack is not.
    if (f.patch.server_seq !== seq + 1) {
      resync();
      return send({ ack: { server_seq: seq } });
    }
    applied(f.patch);
  } else if (f.heartbeat) {
    // Echo both fields verbatim: it keeps the interval_ms predicate total in
    // both directions and doubles as an acknowledgement that the client
    // honoured the interval (docs/protocol.md §3.4).
    send({ heartbeat: { nonce: f.heartbeat.nonce, interval_ms: f.heartbeat.interval_ms } });
  } else if (f.error) {
    // D-29. RATE_LIMITED is the one error the client acts on, because it is
    // the one that means "not now" rather than "not ever": every other code
    // describes something a repeat of the same request would hit again. The
    // event is dispatched either way and carries the whole frame, so an
    // application that wants to show a badge sees no change here.
    if (f.error.code === ErrorCode.RATE_LIMITED) refused();
    document.dispatchEvent(new CustomEvent("gotth-live:error", { detail: f.error }));
  }
}

function close(code, reason) {
  if (sock) sock.close(code, reason);
}

// Close codes that mean "do not come back": an unsupported version, a rejected
// origin, or a failed identity or authorization check will fail again
// identically, so retrying is a loop rather than a recovery
// (docs/protocol.md §8.3).
var TERMINAL = [4000, 4003, 4004, 4005, 4006];

// onClose is the whole of §8.2's user-visible half.
//
// A terminal close ends the page's live session and nothing revives it: not a
// timer, because none is armed, and not a visibility change, because vis()
// acts only on "reconnecting". Anything else is "reconnecting", and the DOM
// stays exactly as the last applied patch left it — frozen, and fully
// interactive. Nothing in this runtime disables a control, and nothing in it
// should: HTMX regions, links, forms and native inputs on the page belong to
// the application, and freezing the live regions is not a licence to take the
// page away from the user (§8.2, FR-31).
function onClose(e) {
  sock = null;
  if (TERMINAL.indexOf(e.code) >= 0) return setStatus("closed");
  setStatus("reconnecting");
  schedule();
}

function hidden() {
  return document.visibilityState === "hidden";
}

// schedule arms the next attempt, per §8.4:
//
//	delay = random(0, min(cap, base·2^n)),  base = 250 ms, cap = 15 s
//
// Unlimited attempts, and no timer at all while the tab is hidden. Math.pow
// overflows to Infinity long before attempt could, and Math.min then yields
// the cap, so the schedule is total for every n without a clamp.
function schedule() {
  if (retry || hidden()) return;
  retry = setTimeout(open, Math.random() * Math.min(CAP, BASE * Math.pow(2, attempt++)));
}

// vis is §8.4's pause and resume, and the resume is immediate BY
// CONSTRUCTION rather than by racing the schedule: a hidden tab holds no timer
// to wait for, so becoming visible connects now instead of at the next tick a
// backgrounded page would have serviced late anyway. Timers in a hidden tab
// are throttled to once a minute in every engine we support, so an armed
// timer there is not a schedule — it is a schedule the browser rewrites.
//
// The guard is what keeps a terminal close terminal: "closed" is not
// "reconnecting", so no amount of tab switching re-dials a session the server
// refused.
//
// The resync retry is treated differently from the reconnect timer, and the
// difference is the socket. A reconnect timer that fired in a hidden tab would
// OPEN one, and nothing else could wake the page, so it is cancelled and the
// resume connects immediately. An armed resync retry writes one small frame on
// a socket that is already open, so it is left armed: a hidden tab that stays
// latched still recovers on its own, at whatever rate the engine's once-a-
// minute timer throttle allows. Becoming visible only PULLS THAT RETRY
// FORWARD. It cannot invent one — a tab switched twice with nothing armed
// sends nothing, which is what keeps rapid visibility changes from becoming
// the flood the schedule exists to prevent.
function vis() {
  clearTimeout(retry);
  retry = 0;
  if (hidden()) return;
  if (!sock && status === "reconnecting") open();
  else if (gapTimer) {
    clearTimeout(gapTimer);
    ask();
  }
}

// open is the only place a socket is constructed, on the first connect and on
// every retry alike, so "a reconnect is a new session" cannot be true on one
// path and false on the other.
function open() {
  retry = 0;
  if (sock) return;
  newSession();
  sock = new WebSocket(endpoint, "gotth-live.v1");
  sock.binaryType = "arraybuffer";
  sock.onmessage = onMessage;
  sock.onclose = onClose;
}

// A page has one live connection. Starting a new island releases the previous
// island first; stop is also safe before a socket reaches its first Snapshot.
export function stop() {
  clearTimeout(retry);
  retry = 0;
  pending.forEach(clearTimeout);
  pending.clear();
  timers = new WeakMap();
  newSession();
  document.removeEventListener("visibilitychange", vis);
  document.removeEventListener("compositionstart", compositionStart);
  document.removeEventListener("compositionend", compositionEnd);
  document.removeEventListener("DOMContentLoaded", bootReady);
  for (var type in listening) document.removeEventListener(type, dispatch, true);
  listening = {};
  composing = null;
  declared = new WeakMap();
  if (sock) {
    sock.onmessage = sock.onclose = null;
    sock.close(1000, "island unmounted");
    sock = null;
  }
  endpoint = null;
  attempt = 0;
  setStatus("closed");
}

export function start(url, root) {
  if (endpoint) stop();
  bind(root || document);
  document.addEventListener("compositionstart", compositionStart);
  document.addEventListener("compositionend", compositionEnd);
  var u = new URL(url, location.href);
  u.protocol = u.protocol === "https:" ? "wss:" : u.protocol === "http:" ? "ws:" : u.protocol;
  endpoint = u.href;
  document.addEventListener("visibilitychange", vis);
  // The only status this function sets. Every later attempt leaves the
  // attribute on "reconnecting", because §8.2 promises the user
  // live → reconnecting → live and not a flicker per attempt.
  setStatus("connecting");
  open();
}
//#endregion

//#region bootstrap
var bootURL;
function compositionStart(e) {
  composing = e.target;
}
function compositionEnd() {
  composing = null;
}
function bootReady() {
  if (bootURL) start(bootURL);
}
function boot() {
  var s = document.currentScript;
  bootURL = s && s.getAttribute("data-gotth-url");
  // An embedding shell may load the script after its island was unmounted.
  // Without an explicit URL, loading alone owns no listeners or connection.
  if (!bootURL) return;
  if (document.readyState === "loading") document.addEventListener("DOMContentLoaded", bootReady, { once: true });
  else bootReady();
}

// One namespaced global and nothing else on window (review-checklist §7.9).
// The guard is what lets the specs import this module under node, where the
// morph algorithm is exercised against a DOM shim and there is no document.
if (typeof document !== "undefined") {
  globalThis.gotthLive = {
    version: VERSION,
    start: start,
    stop: stop,
    status: function () {
      return status;
    },
  };
  boot();
}
//#endregion
