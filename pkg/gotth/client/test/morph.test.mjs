// Morph algorithm specs — DOM-less, run under node's built-in test runner.
//
// DEV-ONLY and quarantined: this file is never served, never bundled, and
// never reaches a consumer. It runs in the bench image, which is the only
// image in the project with node in it (PRD FR-74). A clean clone with no node
// still builds, serves and tests the library — the Go half of the codec
// round-trip runs with `go test` and no node at all — which is the property
// G11 is about.
//
// Run:
//   docker run --rm -v "$PWD:/w" -w /w/gotth-live dis-gotth-live-bench:latest \
//       node --test client/test/

import test from "node:test";
import assert from "node:assert/strict";

import { bind, morphElement } from "../runtime.js";
import { one } from "./dom.mjs";

// morph applies b's markup onto a's tree and returns the live root, which is
// always the same object it was handed — the region anchor never moves.
function morph(fromHTML, toHTML) {
  const a = one(fromHTML);
  morphElement(a, one(toHTML));
  return a;
}

test("text content is updated in place", () => {
  const a = one("<div><b>0</b></div>");
  const b = a.firstChild;
  const t = b.firstChild;

  morphElement(a, one("<div><b>1</b></div>"));

  assert.equal(a.innerHTML, "<b>1</b>");
  // The <b> and its text node are the SAME objects. This is the mechanism
  // behind focus, caret, scroll and media position surviving a patch.
  assert.equal(a.firstChild, b);
  assert.equal(b.firstChild, t);
});

test("attributes are added, changed and removed", () => {
  const a = morph('<div class="a" data-x="1">t</div>', '<div class="b" title="z">t</div>');
  assert.equal(a.getAttribute("class"), "b");
  assert.equal(a.getAttribute("title"), "z");
  assert.equal(a.hasAttribute("data-x"), false);
});

test("a tag change replaces the node rather than mutating it", () => {
  const a = one("<div><span>x</span></div>");
  const span = a.firstChild;

  morphElement(a, one("<div><em>x</em></div>"));

  assert.equal(a.innerHTML, "<em>x</em>");
  assert.notEqual(a.firstChild, span);
});

// An id match with a changed tag is the one route into morphNode's replaceWith
// path — without an id, match() refuses a different tag and the new node is
// inserted beside the old one instead. That path detaches the node it
// replaces, and morphChildren used to read its nextSibling AFTERWARDS to
// advance the cursor. A detached node's nextSibling is null, so the walk ended
// there and every remaining new child was appended beside the old one it
// should have matched.
//
// Measured before the fix: four siblings after the tag change came back
// duplicated, six children where there should be four, and the identity of
// every one of them lost — which is FR-24 and most of FR-25 for the whole
// second half of a fragment, on a patch where a server did nothing more exotic
// than render <article id="card"> where it used to render <div id="card">.
test("a tag change does not corrupt the siblings after it", () => {
  const a = one('<div><p id="x">1</p><div id="w">old</div><p id="y">2</p><p id="z">3</p></div>');
  const [x, w, y, z] = a.childNodes.slice();

  morphElement(a, one('<div><p id="x">1</p><section id="w">new</section><p id="y">2!</p><p id="z">3!</p></div>'));

  assert.equal(a.childNodes.length, 4, "the siblings after the replaced node were duplicated");
  assert.equal(a.innerHTML, '<p id="x">1</p><section id="w">new</section><p id="y">2!</p><p id="z">3!</p>');

  // The tag change is the only replacement. Everything around it is morphed,
  // which is what carries focus, caret, scroll and media position through.
  assert.equal(a.childNodes[0], x);
  assert.notEqual(a.childNodes[1], w, "the changed tag must be a replacement, or this asserts nothing");
  assert.equal(a.childNodes[2], y, "the sibling after a replaced node lost its identity");
  assert.equal(a.childNodes[3], z);
});

test("identified children survive a reorder", () => {
  const a = one('<ul><li id="a">A</li><li id="b">B</li><li id="c">C</li></ul>');
  const [la, lb, lc] = a.childNodes.slice();

  morphElement(a, one('<ul><li id="c">C</li><li id="a">A!</li></ul>'));

  assert.equal(a.childNodes.length, 2);
  // Both surviving nodes are the ORIGINAL objects, in the new order. An
  // implementation that matched positionally would have rebuilt both.
  assert.equal(a.childNodes[0], lc);
  assert.equal(a.childNodes[1], la);
  assert.equal(la.textContent, "A!");
  assert.equal(lb.parentNode, null); // B is gone
});

test("new children are inserted and stale trailing children are removed", () => {
  const a = morph("<ul><li>1</li><li>2</li><li>3</li></ul>", "<ul><li>1</li></ul>");
  assert.equal(a.innerHTML, "<li>1</li>");

  const b = morph("<ul><li>1</li></ul>", "<ul><li>1</li><li>2</li></ul>");
  assert.equal(b.innerHTML, "<li>1</li><li>2</li>");
});

test("a nested subtree is morphed, not rebuilt", () => {
  const a = one('<div><section id="s"><p>old</p></section></div>');
  const s = a.firstChild;
  const p = s.firstChild;

  morphElement(a, one('<div><section id="s"><p>new</p></section></div>'));

  assert.equal(a.firstChild, s);
  assert.equal(s.firstChild, p);
  assert.equal(p.textContent, "new");
});

// --------------------------------------------------------------------------
// FR-27 — the preserve opt-out, which is what makes an HTMX-owned region
// inside a live fragment safe.
// --------------------------------------------------------------------------

test("a preserved subtree is never touched, even when the server renders it differently", () => {
  const a = one('<div><div id="w" data-gotth-preserve><span hx-get="/x">owned</span></div><p>a</p></div>');
  const w = a.firstChild;
  const owned = w.firstChild;

  morphElement(a, one('<div><div id="w" data-gotth-preserve><b>replaced</b></div><p>b</p></div>'));

  assert.equal(a.firstChild, w);
  assert.equal(w.firstChild, owned);
  assert.equal(w.innerHTML, '<span hx-get="/x">owned</span>');
  // The rest of the fragment is still server-owned and still morphs.
  assert.equal(a.childNodes[1].textContent, "b");
});

test("a preserved node the server no longer renders is not removed", () => {
  const a = one('<div><p>a</p><div data-gotth-preserve>keep</div></div>');
  const keep = a.childNodes[1];

  morphElement(a, one("<div><p>a</p></div>"));

  assert.equal(keep.parentNode, a);
  assert.equal(keep.textContent, "keep");
});

test("a preserved fragment root morphs to nothing at all", () => {
  const a = one('<div data-gotth-preserve><p>a</p></div>');
  morphElement(a, one("<div><p>b</p></div>"));
  assert.equal(a.innerHTML, "<p>a</p>");
});

// --------------------------------------------------------------------------
// FR-25 — the controlled/uncontrolled rule. An attribute PRESENT in the
// incoming markup means server-controlled; ABSENT means the value is the
// user's.
// --------------------------------------------------------------------------

test("an uncontrolled input keeps what the user typed", () => {
  const a = one('<div><input id="q" name="q"></div>');
  const input = a.firstChild;
  input.value = "half-typed";

  morphElement(a, one('<div><input id="q" name="q" placeholder="search"></div>'));

  assert.equal(a.firstChild, input);
  assert.equal(input.value, "half-typed");
  assert.equal(input.getAttribute("placeholder"), "search");
});

test("a controlled input is overwritten, including to empty", () => {
  const a = one('<div><input id="q" value="one"></div>');
  const input = a.firstChild;
  input.value = "user edit";

  morphElement(a, one('<div><input id="q" value="two"></div>'));
  assert.equal(input.value, "two");

  morphElement(a, one('<div><input id="q" value=""></div>'));
  assert.equal(input.value, "", "a server that renders value=\"\" must be able to clear the box");
});

test("checkbox state follows the presence of the checked attribute", () => {
  const a = one('<div><input id="c" type="checkbox"></div>');
  const box = a.firstChild;
  box.checked = true; // the user ticked it

  // The server still renders it unchecked and did not change its mind, so the
  // attribute is absent in both markups and the user's tick stands.
  morphElement(a, one('<div><input id="c" type="checkbox"></div>'));
  assert.equal(box.checked, true);

  // Now the server declares it checked.
  morphElement(a, one('<div><input id="c" type="checkbox" checked></div>'));
  assert.equal(box.checked, true);

  // And declares it unchecked again: the attribute was there and is not.
  morphElement(a, one('<div><input id="c" type="checkbox"></div>'));
  assert.equal(box.checked, false);
});

test("details follows the server when the server declares its state", () => {
  const a = one("<div><details><summary>s</summary>body</details></div>");
  const d = a.firstChild;
  bind(a); // first paint: the server's word is closed

  // The server opens it.
  morphElement(a, one("<div><details open><summary>s</summary>body</details></div>"));
  assert.equal(d.open, true);
  assert.equal(a.firstChild, d, "the element was replaced rather than morphed");

  // And closes it again.
  morphElement(a, one("<div><details><summary>s</summary>body</details></div>"));
  assert.equal(d.open, false);
});

// QA-1 D-15, fixed. This is the FR-25 clause "<details> open state" as
// written, and it is the case a browser disagreed with the shim about until
// commit 3c9a9a2d made `open` reflect the content attribute here as the HTML
// standard requires.
//
// The rule that makes it hold is in runtime.js above `declared`: morph
// compares against what the SERVER last said, recorded by bind() at the two
// moments the live attribute is the server's word and nothing else, rather
// than against the live attribute — which for a reflected attribute has two
// authors and one bit. bind() here is the first paint, exactly as it is on the
// document at boot in a browser.
test("details the user opened survives a patch that does not mention it (FR-25, D-15)", () => {
  const a = one("<div><details><summary>s</summary>body</details></div>");
  const d = a.firstChild;
  bind(a);

  d.open = true; // the user opened it; the browser writes open="" for this

  // The server re-renders the fragment for an unrelated reason and says
  // nothing about the disclosure.
  morphElement(a, one("<div><details><summary>s</summary>body</details></div>"));

  assert.equal(a.firstChild, d, "the element was replaced rather than morphed");
  assert.equal(d.open, true, "an unrelated patch closed a <details> the user opened");
  assert.equal(d.hasAttribute("open"), true, "syncAttrs removed the attribute the user's open state reflects to");

  // And it keeps holding: the record must not be re-seeded from the live
  // attribute after a patch, or the disclosure survives one patch and dies on
  // the next.
  morphElement(a, one("<div><details><summary>s</summary>body</details></div>"));
  assert.equal(d.open, true, "the disclosure survived one patch and was closed by the next");
});

// The other direction, and the one that stops the fix from being "never touch
// <details>". A server that CHANGES its declaration is still authoritative,
// which is the same bargain the checkbox rule strikes.
test("a server that changes its mind still closes a <details> the user opened", () => {
  const a = one("<div><details><summary>s</summary>body</details></div>");
  const d = a.firstChild;
  bind(a);

  d.open = true; // the user opened it

  // The server now declares it open too. Nothing visible changes.
  morphElement(a, one("<div><details open><summary>s</summary>body</details></div>"));
  assert.equal(d.open, true);

  // And now withdraws the declaration. That is a change in the server's word,
  // so it wins.
  morphElement(a, one("<div><details><summary>s</summary>body</details></div>"));
  assert.equal(d.open, false, "the server changed its declaration and morph did not follow it");
  assert.equal(a.firstChild, d, "the element was replaced rather than morphed");
});

// The mirror of D-15: a repeated declaration is not a change, so it does not
// re-open what the user closed. This is the arm that catches syncAttrs copying
// the attribute back through after syncProps has decided not to.
test("a repeated declaration does not reopen a <details> the user closed", () => {
  const a = one("<div><details open><summary>s</summary>body</details></div>");
  const d = a.firstChild;
  bind(a);
  assert.equal(d.open, true);

  d.open = false; // the user closes what the server opened

  morphElement(a, one("<div><details open><summary>s</summary>body</details></div>"));
  assert.equal(d.open, false, "a patch repeating open= reopened a disclosure the user had closed");
  assert.equal(d.hasAttribute("open"), false);
});

// A <details> a patch INSERTS carries the server's declaration in its own
// markup, and bind() runs on the region after every patch — so the record for
// a node nobody has seen before comes from the markup that introduced it, not
// from a live attribute anyone could have written.
test("a <details> the morph inserted takes its baseline from the markup that introduced it", () => {
  const a = one("<div><p>a</p></div>");
  bind(a);

  morphElement(a, one('<div><p>a</p><details id="d" open><summary>s</summary>body</details></div>'));
  const d = a.childNodes[1];
  assert.equal(d.open, true);
  bind(a); // what apply() does after every patch

  d.open = false; // the user closes it

  morphElement(a, one('<div><p>b</p><details id="d" open><summary>s</summary>body</details></div>'));
  assert.equal(a.childNodes[1], d, "the inserted element was replaced rather than morphed");
  assert.equal(d.open, false, "the user's close was reverted by a patch that repeated the declaration");
  assert.equal(a.childNodes[0].textContent, "b", "the rest of the fragment stopped morphing");
});

test("a textarea's value is taken from its declared text, and left alone when there is none", () => {
  const a = one("<div><textarea></textarea></div>");
  const ta = a.firstChild;
  ta.value = "draft";

  morphElement(a, one("<div><textarea></textarea></div>"));
  assert.equal(ta.value, "draft");

  morphElement(a, one("<div><textarea>server text</textarea></div>"));
  assert.equal(ta.value, "server text");
});

test("an input is treated as a leaf", () => {
  // Descending into an input would be meaningless and, for a textarea, would
  // fight the value rule. The node survives and its attributes still sync.
  const a = one('<div><input id="q" type="text"></div>');
  const input = a.firstChild;
  morphElement(a, one('<div><input id="q" type="text" disabled></div>'));
  assert.equal(a.firstChild, input);
  assert.equal(input.hasAttribute("disabled"), true);
});
