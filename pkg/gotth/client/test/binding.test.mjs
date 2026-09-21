// The reading half of FR-54 failure 2: an option a binding declares belongs to
// THAT binding.
//
// docs/gates/phase-4.md §5.6 row 2, driven by QA-1 in Chromium
// (docs/qa/fr-54-debounce-repro.md, verdict REPRODUCES): Fields, Debounce and
// Throttle used to be attributes of the ELEMENT. dispatch() read the interval
// off the element and keyed the debounce timer by the element, so on the
// guide's own composer — an Escape binding composed with a 150 ms input
// binding — the Escape inherited an interval it never asked for AND a
// keystroke inside that window cleared the pending clear outright. Not delayed,
// not reordered: gone, with no error, no console warning and no frame on the
// wire. Symmetrically, an Escape inside the window destroyed a pending draft,
// so the server never learned what was typed while the browser went on showing
// it.
//
// The fixtures below are written as literal markup because that is what the
// contract is: `client/SIZE.md` §7 fixes the attribute vocabulary and
// `live/binding_test.go` pins the same strings on the emitting side. A
// disagreement between the two is a silent no-op in the browser rather than an
// error anywhere, which is why both halves assert the bytes rather than each
// other.
//
// DEV-ONLY and quarantined, like every file in this directory.

import test from "node:test";
import assert from "node:assert/strict";

import { PatchOp } from "../codec.gen.js";
import { harness, panel, SESSION_A } from "./harness.mjs";

// The guide's composer, in the shape the server now emits: the Escape binding
// carries no options, the input binding carries its own 150 ms, and there is
// no element-level attribute for either of them to share.
const COMPOSER = '<input id="composer" name="body" value="hi" data-gotth-on="keydown:c.clear:Escape;input:c.draft::150">';

// The shape the server emitted BEFORE this landed, kept as a control on the
// removal rather than as a vocabulary: nothing renders it any more, and the
// point of the spec that uses it is that the runtime no longer reads it. A
// stale binary, a hand-written attribute or a cached page cannot bring the
// shared timer back.
const LEGACY =
  '<input id="legacy" name="body" value="hi" data-gotth-on="keydown:l.clear:Escape;input:l.draft"' +
  ' data-gotth-debounce="150" data-gotth-fields="room=alpha">';

// Two bindings that BOTH declare a debounce. This is the case that separates
// "the interval is per binding" from "the timer is per binding": with one
// interval per element the first fix alone would already make the composer
// above behave, and this element would still lose an event.
const TWO = '<input id="two" name="body" data-gotth-on="keydown:t.slow:Escape:300;input:t.fast::150">';

// A throttled binding beside an unthrottled one. Throttle had the same
// element-scoped record as Debounce — st.l, keyed by the element — so it is
// fixed the same way and asserted the same way.
const THROTTLED = '<input id="thr" name="body" data-gotth-on="keydown:th.guard:Escape::1000;input:th.free">';

// Two bindings with DIFFERENT static fields. Under the old first-wins merge
// this element carried one data-gotth-fields and both bindings sent it, so the
// second binding's payload was the first binding's.
const FIELDED = '<button id="fld" data-gotth-on="click:f.one::::a=1;dblclick:f.two::::b=2">+</button>';

// Two bindings on one element that share an EVENT NAME and differ in key and
// in fields. Every spec above separates its bindings by DOM event or by event
// name, so every one of them is also green if the timer record is keyed by the
// event name instead of by the spec string — which is the mutation
// docs/reviews/fr-54.md §7.2 found survived all 165 committed client specs and
// all 7 browser conformance specs.
//
// It is not a cosmetic key. THIS is the shape Bind.Fields moved per-binding for
// (that review §3): two keys raising ONE event with different payloads is the
// ordinary way to write the benchmark counter's "+ and - apply +1 and -1", and
// the debounce is what a key-repeat guard wants. Keyed by name, the second press
// calls clearTimeout on the first press's pending send: one event arrives where
// two were raised, and it carries dir=down. That is FR-54 failure 2 exactly —
// one binding destroying another's pending send, silently — reintroduced by a
// one-token change.
//
// The markup is what the server emits for
//
//   live.OnAll(
//     live.OnWith("keydown", "c.step", live.Bind{Keys: []string{"+"},
//       Fields: map[string]string{"dir": "up"},   Debounce: 100 * time.Millisecond}),
//     live.OnWith("keydown", "c.step", live.Bind{Keys: []string{"-"},
//       Fields: map[string]string{"dir": "down"}, Debounce: 100 * time.Millisecond}),
//   )
//
// and live/binding_test.go pins that emission, so the literal below cannot
// drift away from the markup a page actually carries.
const SHARED =
  '<div id="shared" tabindex="0" data-gotth-on="keydown:c.step:+:100::dir=up;keydown:c.step:-:100::dir=down"></div>';

// deliver morphs the region to a panel carrying `extra` and returns the socket.
// The bound elements arrive by PATCH rather than in the first document so that
// the specs also exercise bind()'s discovery of a new event type, which is how
// a keydown listener comes to exist at all.
function paint(h, extra) {
  h.live().deliver({
    protocol_version: 1,
    session_id: SESSION_A,
    patch: {
      server_seq: 2,
      patch_id: 2,
      transition_id: 2,
      state_version: 2,
      updates: [{ fragment_id: "rc.panel", op: PatchOp.MORPH, html: panel(1, extra) }],
    },
  });
}

// names is what the SERVER would see, in arrival order. Every assertion here is
// about the events that reached the wire, not about the DOM: "this event was
// never raised" is the whole finding, and it is invisible in the document.
function names(h) {
  return h
    .live()
    .kind("event")
    .map((f) => f.event.name);
}

function fieldsOf(h, name) {
  const f = h
    .live()
    .kind("event")
    .find((x) => x.event.name === name);
  assert.ok(f, name + " never reached the wire");
  return (f.event.fields || []).map((kv) => kv.key + "=" + (kv.value === undefined ? "" : kv.value));
}

test("a binding's own debounce is in force for that binding", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, COMPOSER);

  h.emit("input", h.el("#composer"));

  assert.deepEqual(names(h), [], "the draft binding asked for 150 ms and was sent immediately");
  assert.equal(h.clock.count(), 1);
  assert.deepEqual(h.clock.delays(), [150]);

  h.clock.fire();
  assert.deepEqual(names(h), ["c.draft"]);
});

test("a sibling's debounce does not delay a binding that asked for none", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, COMPOSER);

  h.emit("keydown", h.el("#composer"), { key: "Escape" });

  assert.deepEqual(names(h), ["c.clear"], "the Escape binding declared no interval and must not wait for one");
  assert.equal(h.clock.count(), 0, "no timer was armed, so there is nothing for a later event to cancel");
});

test("a keystroke inside the window does not destroy the pending clear", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, COMPOSER);

  // §5.6's sequence: Escape, then a printable character 3 ms later. The
  // browser routes the keydown AND the input event the character causes.
  h.emit("keydown", h.el("#composer"), { key: "Escape" });
  h.emit("input", h.el("#composer"));
  h.clock.fire();

  assert.deepEqual(names(h), ["c.clear", "c.draft"]);
});

test("an Escape inside the window does not destroy the pending draft", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, COMPOSER);

  // The symmetric direction, and the one that loses SERVER state rather than a
  // keystroke: the draft the member typed never arrives and the browser goes
  // on showing it.
  h.emit("input", h.el("#composer"));
  h.emit("keydown", h.el("#composer"), { key: "Escape" });
  h.clock.fire();

  assert.deepEqual(names(h), ["c.clear", "c.draft"]);
});

test("two debounced bindings on one element hold independent timers", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, TWO);

  h.emit("keydown", h.el("#two"), { key: "Escape" });
  assert.deepEqual(h.clock.delays(), [300]);

  h.emit("input", h.el("#two"));
  assert.deepEqual(
    h.clock.delays().sort((a, b) => a - b),
    [150, 300],
    "the input's timer replaced the keydown's, which is the element-keyed WeakMap slot",
  );

  h.clock.fire();
  h.clock.fire();
  assert.deepEqual(names(h).sort(), ["t.fast", "t.slow"]);
});

test("a throttled binding does not throttle its sibling", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, THROTTLED);

  h.emit("keydown", h.el("#thr"), { key: "Escape" });
  h.emit("keydown", h.el("#thr"), { key: "Escape" });
  assert.deepEqual(names(h), ["th.guard"], "the second press is inside the throttled binding's own interval");

  h.emit("input", h.el("#thr"));
  h.emit("input", h.el("#thr"));
  assert.deepEqual(names(h), ["th.guard", "th.free", "th.free"], "the input binding declared no throttle");
});

test("each binding sends the static fields it declared", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, FIELDED);

  h.emit("click", h.el("#fld"));
  h.emit("dblclick", h.el("#fld"));

  assert.deepEqual(names(h), ["f.one", "f.two"]);
  assert.deepEqual(fieldsOf(h, "f.one"), ["a=1"]);
  assert.deepEqual(fieldsOf(h, "f.two"), ["b=2"], "the second binding sent the first binding's fields");
});

test("the element-level option attributes are not read and are not a fallback", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, LEGACY);

  // This is QA-1's spec 3 verbatim, against the markup that used to produce it.
  // Before this landed the Escape armed a 150 ms timer read off the element and
  // the input event 3 ms later cleared it: ONE event reached the server for the
  // pair, and it was the draft.
  h.emit("keydown", h.el("#legacy"), { key: "Escape" });
  h.emit("input", h.el("#legacy"));

  assert.deepEqual(names(h), ["l.clear", "l.draft"]);
  assert.equal(h.clock.count(), 0, "data-gotth-debounce is no longer a debounce");
  assert.deepEqual(fieldsOf(h, "l.draft"), ["body=hi"], "data-gotth-fields is no longer a field source");
});

test("two bindings that share an event name hold independent timers", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, SHARED);

  // "+" then "-" inside the 100 ms window. Two bindings, one event name, one
  // element, two presses the member made and two the server is owed.
  h.emit("keydown", h.el("#shared"), { key: "+" });
  assert.deepEqual(h.clock.delays(), [100]);

  h.emit("keydown", h.el("#shared"), { key: "-" });
  assert.deepEqual(
    h.clock.delays(),
    [100, 100],
    "the second press cancelled the first one's pending send: the record is keyed by the event name, not by the spec",
  );
  assert.equal(h.clock.count(), 2, "two bindings armed two timers");

  h.clock.fire();
  h.clock.fire();

  assert.deepEqual(names(h), ["c.step", "c.step"], "both presses must reach the server");
  assert.deepEqual(
    h
      .live()
      .kind("event")
      .map((f) => (f.event.fields || []).map((kv) => kv.key + "=" + kv.value).join(",")),
    ["dir=up", "dir=down"],
    "each binding sends its own payload, in the order the keys were pressed",
  );
});

test("an unfiltered binding with no options is unchanged", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, "");

  // panel()'s own button, bound "click:rc.inc" since checkpoint 1. It is the
  // positive control: this suite would be worth nothing if the grammar change
  // had broken the shape every page already uses.
  h.emit("click", h.el("#inc"));

  assert.deepEqual(names(h), ["rc.inc"]);
  assert.equal(h.clock.count(), 0);
});

// ---------------------------------------------------------------------------
// FR-54 failure 1 — components 7 and 8, the reading half.
//
// docs/reviews/fr-54.md §12 accepts Bind.NoModifiers and Bind.PreventDefault
// and REFUSES the full modifier set (§13). The requirement is F-CHT-3, "Enter
// sends and Shift+Enter inserts a newline", which a key filter alone cannot
// express because a filter chooses which keys raise events and does not take a
// key away from the page.
//
// These are the node half, and they are here rather than only in the browser
// because the properties below are about what dispatch() DOES with the four
// modifier booleans and with the composition guard, not about what value a
// physical key produces — which is the browser's business and is asserted in
// test/internal/conformance/keybinding_modifiers_test.go through Chromium.
// ---------------------------------------------------------------------------

// F-CHT-3's composer as live/binding_test.go pins the server emitting it, with
// the send binding carrying both new components and the draft binding beside it
// carrying neither. The literal is the contract; a disagreement between this
// file and that one is a silent no-op in the browser.
const CHT3 =
  '<textarea id="cht" name="body" data-gotth-on="keydown:chat.send:Enter::::1:1;input:chat.draft::150"></textarea>';

// The same composer WITHOUT the two components — today's grammar, and the
// negative control that reproduces today's loss. It is what makes the specs
// above it worth something: they would all be green against a runtime that
// ignored s[6] and s[7] entirely if this one did not go the other way.
const CHT3_TODAY =
  '<textarea id="old" name="body" data-gotth-on="keydown:chat.send:Enter;input:chat.draft::150"></textarea>';

// Two bindings for ONE key, distinguished only by the modifier component. The
// first is filtered out by a held modifier and does NOT break dispatch's match
// loop, so the second gets its turn — which is how one element raises two
// different events for Enter and Shift+Enter.
const FALLTHROUGH =
  '<div id="ft" tabindex="0" data-gotth-on="keydown:f.plain:Enter::::1;keydown:f.chord:Enter"></div>';

// C-6's AltGr case. "@" needs AltGr on many European layouts, and AltGr sets
// ctrlKey AND altKey — so a NoModifiers binding that names the key the member
// is actually typing does not fire. A silent non-firing is this requirement's
// own failure mode, so it is asserted rather than left in a godoc.
const ALTGR =
  '<div id="ag" tabindex="0" data-gotth-on="keydown:a.strict:@::::1;keydown:a.loose:@"></div>';

// pd emits an event carrying a preventDefault spy, and returns the call count.
// The harness's own event object supplies a no-op preventDefault; `extra` wins
// under Object.assign, which is what lets a spec count the calls.
function pd(h, type, el, extra) {
  let calls = 0;
  h.emit(type, el, Object.assign({ preventDefault: () => calls++ }, extra));
  return () => calls;
}

test("a binding that asked for it suppresses the browser's default", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, CHT3);

  const calls = pd(h, "keydown", h.el("#cht"), { key: "Enter" });

  assert.deepEqual(names(h), ["chat.send"], "Enter did not reach the binding that names it");
  assert.equal(calls(), 1, "the newline was left to the browser: F-CHT-3 sends AND newlines");
});

test("a modifier held is a press the NoModifiers binding does not match", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, CHT3);

  const calls = pd(h, "keydown", h.el("#cht"), { key: "Enter", shiftKey: true });

  assert.deepEqual(names(h), [], "Shift+Enter reached the send binding: the message sends on a newline");
  assert.equal(calls(), 0, "a binding that does not match must suppress nothing — the browser owes a line break");
});

test("NEGATIVE CONTROL: without the two components Shift+Enter sends and Enter newlines", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, CHT3_TODAY);

  const shift = pd(h, "keydown", h.el("#old"), { key: "Enter", shiftKey: true });
  assert.deepEqual(names(h), ["chat.send"], "this is the defect F-CHT-3 names, and it must reproduce");
  assert.equal(shift(), 0);

  const plain = pd(h, "keydown", h.el("#old"), { key: "Enter" });
  assert.equal(plain(), 0, "and the newline lands beside the send");
});

// C-5. The composer is bound keydown:send WITH PreventDefault and input:draft
// WITHOUT it, and the mutation control is "read s[7] off the element instead" —
// which turns exactly this spec red and nothing else.
test("PreventDefault belongs to the binding that declared it and not to its sibling", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, CHT3);

  const key = pd(h, "keydown", h.el("#cht"), { key: "Enter" });
  const input = pd(h, "input", h.el("#cht"));

  assert.equal(key(), 1, "the keydown binding declared PreventDefault");
  assert.equal(input(), 0, "the input binding beside it declared none and must not inherit one");
  h.clock.fire();
  assert.deepEqual(names(h), ["chat.send", "chat.draft"], "both bindings still reach the server");
});

// C-9, and the defect L9-1 found by building the shape rather than accepting
// it: docs/reviews/fr-54.md §12.1's prototype folds s[7] into the preventDefault
// line ABOVE the composition guard.
//
// Enter during an IME composition COMMITS the candidate. Suppressing it takes
// the commit key away from every composer that uses one — the population FR-26's
// guard exists for — and it does so silently, on a keyboard most of this
// project's reviewers do not have. So the suppression sits below the guard, and
// this spec is what fails if it moves back up.
test("PreventDefault does not fire mid-composition: Enter still commits the candidate", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, CHT3);

  h.emit("compositionstart", h.el("#cht"));
  const calls = pd(h, "keydown", h.el("#cht"), { key: "Enter" });

  assert.equal(calls(), 0, "the IME's commit key was suppressed: every CJK composer on this page is broken");
  assert.deepEqual(names(h), [], "FR-26: nothing is sent mid-composition either");
});

test("the same Enter sends and suppresses once the composition has ended", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, CHT3);

  h.emit("compositionstart", h.el("#cht"));
  h.emit("compositionend", h.el("#cht"));
  const calls = pd(h, "keydown", h.el("#cht"), { key: "Enter" });

  assert.equal(calls(), 1, "the binding must work normally after the commit");
  assert.deepEqual(names(h), ["chat.send"]);
});

// A submit or an anchor click has had its default suppressed since checkpoint 1,
// and that line is NOT the one that moved. Pinned here because the obvious way
// to satisfy C-9 is to fold s[7] into it and push the whole thing below the
// guard, which would let a form navigate for real mid-composition — a second
// defect wearing the first one's fix.
test("a submit still has its default suppressed mid-composition", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, '<form id="f" data-gotth-on="submit:f.go"><input name="q" value="x"></form>');

  h.emit("compositionstart", h.el("#f"));
  const calls = pd(h, "submit", h.el("#f"));

  assert.equal(calls(), 1, "the page would have navigated away mid-composition");
  assert.deepEqual(names(h), [], "and it is still not sent");
});

// A binding filtered out by a modifier does not end the match loop. This is the
// property that makes F-CHT-3 expressible as two bindings rather than as a
// three-valued modifier field — the shape §13 refuses.
test("a modifier-filtered binding falls through to the next one for the same event", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, FALLTHROUGH);

  h.emit("keydown", h.el("#ft"), { key: "Enter" });
  assert.deepEqual(names(h), ["f.plain"], "the unmodified press must reach the first binding");

  h.emit("keydown", h.el("#ft"), { key: "Enter", shiftKey: true });
  assert.deepEqual(names(h), ["f.plain", "f.chord"], "the chord must fall through to the binding behind it");
});

// C-6, asserted. AltGr sets ctrlKey AND altKey, so a printable key that needs
// it does not match a NoModifiers binding — while the member types exactly the
// character the binding named. live.Bind.NoModifiers says so; this is the spec
// that would go red if the runtime stopped reading one of the four.
test("AltGr sets ctrlKey AND altKey, so an AltGr key misses a NoModifiers binding", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, ALTGR);

  h.emit("keydown", h.el("#ag"), { key: "@", ctrlKey: true, altKey: true });
  assert.deepEqual(names(h), ["a.loose"], "AltGr+@ matched the strict binding: it must not, and the godoc says so");

  h.emit("keydown", h.el("#ag"), { key: "@" });
  assert.deepEqual(names(h), ["a.loose", "a.strict"], "and the same key with nothing held must match it");
});

test("each of the four modifier booleans is read, one spec per key", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, ALTGR);

  for (const mod of ["shiftKey", "ctrlKey", "altKey", "metaKey"]) {
    h.emit("keydown", h.el("#ag"), { key: "@", [mod]: true });
  }
  assert.deepEqual(
    names(h),
    ["a.loose", "a.loose", "a.loose", "a.loose"],
    "one of the four modifiers is not read, so a NoModifiers binding fires with it held",
  );
});

// CapsLock and NumLock are lock states rather than held modifiers: they set none
// of the four booleans and a filtered binding fires with either on. Stated in
// the godoc, asserted here, because "it reads the modifier state" is exactly the
// sentence a reader would over-apply.
test("CapsLock and NumLock are not modifiers and do not filter anything out", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, ALTGR);

  h.emit("keydown", h.el("#ag"), { key: "@", getModifierState: () => true });
  assert.deepEqual(names(h), ["a.strict"], "a lock state must not filter a NoModifiers binding");
});

// A binding with no key filter and NoModifiers set is a filter, not a no-op —
// the review's §12.1 godoc claimed the opposite and it is corrected in
// live.Bind. An unfiltered keydown binding with the component raises its event
// for every UNMODIFIED key and for no modified one.
test("NoModifiers applies to a binding that names no key at all", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, '<div id="u" tabindex="0" data-gotth-on="keydown:u.any:::::1"></div>');

  h.emit("keydown", h.el("#u"), { key: "a" });
  h.emit("keydown", h.el("#u"), { key: "b", ctrlKey: true });
  h.emit("keydown", h.el("#u"), { key: "c" });

  assert.deepEqual(names(h), ["u.any", "u.any"], "the Ctrl chord must not reach an unfiltered NoModifiers binding");
});

// C-1 on the reading side. A spec with fewer than seven components leaves s[6]
// and s[7] undefined, and !undefined is true — so a binding written before these
// components existed costs no extra test and behaves exactly as it did. The
// specs above this line all use the new grammar; this one uses the old one.
test("a binding with neither component behaves exactly as it did before they existed", async (t) => {
  const h = await harness(t);
  h.connect();
  paint(h, COMPOSER);

  const esc = pd(h, "keydown", h.el("#composer"), { key: "Escape", shiftKey: true, ctrlKey: true });
  assert.deepEqual(names(h), ["c.clear"], "a modifier must not filter a binding that never asked about one");
  assert.equal(esc(), 0, "and nothing may be taken away from the browser");
});
