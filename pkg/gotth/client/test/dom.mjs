// A minimal DOM, so the morph algorithm can be specified without a browser.
//
// This is test scaffolding and it is deliberately small: it implements exactly
// the interface client/runtime.js's morph section touches, and nothing else.
// It is not a browser and does not pretend to be one — the full DOM
// conformance suite for focus, caret, scroll, IME composition and media
// position runs Playwright across NFR-7's browser matrix, and that is
// checkpoint 2's work (PRD FR-25, FR-26).
//
// What this buys is the half a browser cannot check cheaply: that the
// TRAVERSAL is right. Which old node is matched to which new node, what is
// removed, what is inserted, and — the property most of FR-25 actually rests
// on — which live node objects survive the morph rather than being replaced.
// Node identity is the mechanism behind focus, caret, scroll offset, media
// position and running transitions surviving a patch, so asserting identity
// here asserts the mechanism, and the browser suite then confirms the effect.
//
// No dependencies. Node's own test runner drives it.

let nextOrder = 0;

class Node {
  constructor(nodeType) {
    this.nodeType = nodeType;
    this.parentNode = null;
    this._kids = [];
    this._order = nextOrder++; // stable identity, for assertions
  }

  get childNodes() {
    return this._kids;
  }

  get firstChild() {
    return this._kids[0] || null;
  }

  get firstElementChild() {
    return this._kids.find((n) => n.nodeType === 1) || null;
  }

  get nextSibling() {
    if (!this.parentNode) return null;
    const k = this.parentNode._kids;
    return k[k.indexOf(this) + 1] || null;
  }

  get textContent() {
    return this.nodeType === 3 ? this.nodeValue : this._kids.map((n) => n.textContent).join("");
  }

  _detach() {
    if (this.parentNode) this.parentNode.removeChild(this);
  }

  appendChild(n) {
    n._detach();
    n.parentNode = this;
    this._kids.push(n);
    return n;
  }

  insertBefore(n, ref) {
    n._detach();
    n.parentNode = this;
    const i = ref ? this._kids.indexOf(ref) : -1;
    if (i < 0) this._kids.push(n);
    else this._kids.splice(i, 0, n);
    return n;
  }

  removeChild(n) {
    const i = this._kids.indexOf(n);
    if (i < 0) throw new Error("removeChild: not a child");
    this._kids.splice(i, 1);
    n.parentNode = null;
    return n;
  }

  replaceChild(n, old) {
    const i = this._kids.indexOf(old);
    if (i < 0) throw new Error("replaceChild: not a child");
    n._detach();
    n.parentNode = this;
    this._kids[i] = n;
    old.parentNode = null;
    return old;
  }

  replaceWith(n) {
    if (this.parentNode) this.parentNode.replaceChild(n, this);
  }

  remove() {
    this._detach();
  }

  contains(n) {
    for (let p = n; p; p = p.parentNode) if (p === this) return true;
    return false;
  }
}

class Text extends Node {
  constructor(value) {
    super(3);
    this.nodeValue = value;
  }
  get outerHTML() {
    return this.nodeValue;
  }
}

const VOID = new Set(["area", "base", "br", "col", "hr", "img", "input", "link", "meta", "source"]);

// selectorTerm compiles one term of a selector list into a predicate.
function selectorTerm(sel) {
  const m = /^\[([^\]=]+)(?:="([^"]*)")?\]$/.exec(sel);
  if (m) return (el) => el.hasAttribute(m[1]) && (m[2] === undefined || el.getAttribute(m[1]) === m[2]);
  if (/^[a-z][a-z0-9-]*$/i.test(sel)) {
    const tag = sel.toUpperCase();
    return (el) => el.tagName === tag;
  }
  throw new Error("querySelectorAll: unsupported selector " + sel);
}

class Element extends Node {
  constructor(tag) {
    super(1);
    this.tagName = tag.toUpperCase();
    this.attributes = [];
    // Live properties, distinct from the attributes that seed them. The whole
    // controlled/uncontrolled rule is about this distinction, so the shim has
    // to keep them apart or the specs would assert nothing.
    //
    // `open` is NOT one of them, and the difference is the whole of QA-1's
    // D-15. See the accessor below.
    this.value = "";
    this.checked = false;
    this.selected = false;
    this.open = false;
  }

  // `open` REFLECTS the content attribute, and `checked`/`selected` do not.
  //
  // That asymmetry is the HTML standard's, not this shim's. `input.checked` is
  // the element's checkedness — a piece of state distinct from the `checked`
  // attribute, which is only its default — and `option.selected` is the same
  // shape. `details.open`, by contrast, is a plain reflected IDL attribute: a
  // user opening a disclosure writes `open=""` into the DOM, and reading it
  // back cannot tell the user's own act apart from a server declaration.
  //
  // This shim modelled `open` as a plain property until 2026-08-04, and the
  // consequence was a green test that a real browser contradicts: the morph
  // rule keys on attribute PRESENCE meaning "the server is controlling this",
  // so in a browser an unrelated patch closes a <details> the user opened
  // (QA-1 D-15, docs/qa/checkpoint-2-browser.md §4). Modelling it accurately
  // is what makes the node-level suite capable of disagreeing with the runtime
  // about this at all.
  get open() {
    return this.hasAttribute("open");
  }

  set open(v) {
    if (v) this.setAttribute("open", "");
    else this.removeAttribute("open");
  }

  get id() {
    return this.getAttribute("id") || "";
  }

  get type() {
    return this.getAttribute("type") || "";
  }

  get name() {
    return this.getAttribute("name") || "";
  }

  getAttribute(n) {
    const a = this.attributes.find((x) => x.name === n);
    return a ? a.value : null;
  }

  hasAttribute(n) {
    return this.attributes.some((x) => x.name === n);
  }

  setAttribute(n, v) {
    v = String(v);
    const a = this.attributes.find((x) => x.name === n);
    if (a) a.value = v;
    else this.attributes.push({ name: n, value: v });
  }

  removeAttribute(n) {
    const i = this.attributes.findIndex((x) => x.name === n);
    if (i >= 0) this.attributes.splice(i, 1);
  }

  // querySelectorAll supports the forms the runtime uses: [attr],
  // [attr="value"], a bare tag name, and a comma-separated list of those —
  // bind() asks for "[data-gotth-on],details" in one walk. Anything else
  // throws, because a shim that silently matched nothing would turn a spec
  // green by doing less rather than by the runtime doing right.
  querySelectorAll(sel) {
    const terms = sel.split(",").map((s) => selectorTerm(s.trim()));
    const out = [];
    const walk = (n) => {
      for (const k of n._kids) {
        if (k.nodeType === 1) {
          if (terms.some((t) => t(k))) out.push(k);
          walk(k);
        }
      }
    };
    walk(this);
    return out;
  }

  // closest supports exactly the forms querySelectorAll does, through the same
  // compiler, because the runtime asks it for both: dispatch() walks up to
  // [data-gotth-on] and [data-gotth-region], and fields() asks for the
  // enclosing "form" by tag. A shim whose closest understood fewer selectors
  // than its own querySelectorAll would throw on the event path and make it
  // untestable here — which is how the reconnect suite found this.
  closest(sel) {
    const t = selectorTerm(sel);
    for (let p = this; p; p = p.parentNode) if (p.nodeType === 1 && t(p)) return p;
    return null;
  }

  get outerHTML() {
    const tag = this.tagName.toLowerCase();
    const attrs = this.attributes.map((a) => ` ${a.name}="${a.value}"`).join("");
    if (VOID.has(tag)) return `<${tag}${attrs}>`;
    return `<${tag}${attrs}>${this._kids.map((k) => k.outerHTML).join("")}</${tag}>`;
  }

  get innerHTML() {
    return this._kids.map((k) => k.outerHTML).join("");
  }
}

// parse handles the subset of HTML these specs are written in: elements,
// attributes with double-quoted or bare values, boolean attributes, void
// elements, and text. Anything it cannot parse throws rather than guessing,
// because a shim that silently mis-parses a fixture would make a passing spec
// meaningless.
export function parse(html) {
  const root = new Element("root");
  let cur = root;
  let i = 0;

  while (i < html.length) {
    const lt = html.indexOf("<", i);
    if (lt < 0) {
      addText(cur, html.slice(i));
      break;
    }
    if (lt > i) addText(cur, html.slice(i, lt));

    const gt = html.indexOf(">", lt);
    if (gt < 0) throw new Error("parse: unterminated tag");
    const inner = html.slice(lt + 1, gt).trim();
    i = gt + 1;

    if (inner.startsWith("/")) {
      const tag = inner.slice(1).trim().toUpperCase();
      if (cur.tagName !== tag) throw new Error(`parse: </${tag}> closes <${cur.tagName}>`);
      cur = cur.parentNode;
      continue;
    }

    const body = inner.endsWith("/") ? inner.slice(0, -1) : inner;
    const sp = body.search(/\s/);
    const tag = (sp < 0 ? body : body.slice(0, sp)).toLowerCase();
    const el = new Element(tag);
    if (sp >= 0) attrs(el, body.slice(sp));
    cur.appendChild(el);
    if (!inner.endsWith("/") && !VOID.has(tag)) cur = el;
  }

  if (cur !== root) throw new Error(`parse: unclosed <${cur.tagName}>`);
  return root;
}

function addText(parent, s) {
  if (s !== "") parent.appendChild(new Text(s));
}

function attrs(el, s) {
  const re = /([^\s=]+)(?:="([^"]*)"|=(\S+))?/g;
  let m;
  while ((m = re.exec(s)) !== null) {
    if (m[0].trim() === "") continue;
    const v = m[2] !== undefined ? m[2] : m[3] !== undefined ? m[3] : "";
    el.setAttribute(m[1], v);
    // Attributes that seed a live property do so once, at parse, exactly as a
    // browser does. Morph's job is then to decide whether to touch the
    // property afterwards.
    if (m[1] === "value") el.value = v;
    if (m[1] === "checked") el.checked = true;
    if (m[1] === "selected") el.selected = true;
    if (m[1] === "open") el.open = true;
  }
  // A textarea's declared value is its text content; the runtime reads
  // b.textContent for it, so nothing extra is needed here.
}

// one parses a fixture expected to hold a single root element.
export function one(html) {
  const r = parse(html);
  const el = r.firstElementChild;
  if (!el) throw new Error("one: no element in " + html);
  el.parentNode = null;
  return el;
}

export { Element, Text };
