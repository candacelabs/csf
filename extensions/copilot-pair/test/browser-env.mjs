/**
 * A minimal hand-rolled browser environment for testing public/app.js in Node
 * without third-party dependencies.
 *
 * It deliberately models the page's real deployment target: a plain-http LAN
 * origin, which is NOT a secure browser context. `crypto` therefore exposes
 * getRandomValues but NOT randomUUID — code that assumes secure-context-only
 * web APIs fails here the same way it fails on a phone opening the share link.
 */
import { randomFillSync } from "node:crypto";

class FakeElement {
  constructor(tagName) {
    this.tagName = tagName.toUpperCase();
    this.children = [];
    this.listeners = {};
    this.classNames = new Set();
    this.parentElement = undefined;
    this.replacedWith = undefined;
    this.id = "";
    this.value = "";
    this.textContent = "";
    this.placeholder = "";
    this.hidden = false;
    this.disabled = false;
  }

  get className() {
    return [...this.classNames].join(" ");
  }

  set className(value) {
    this.classNames = new Set(String(value).split(" ").filter(Boolean));
  }

  get classList() {
    return {
      add: (...names) => names.forEach((name) => this.classNames.add(name)),
      remove: (...names) => names.forEach((name) => this.classNames.delete(name)),
      contains: (name) => this.classNames.has(name),
      toggle: (name, force) => {
        const present = force ?? !this.classNames.has(name);
        if (present) {
          this.classNames.add(name);
        } else {
          this.classNames.delete(name);
        }
        return present;
      },
    };
  }

  get options() {
    return this.children.filter((child) => child.tagName === "OPTION");
  }

  addEventListener(name, handler) {
    (this.listeners[name] ??= []).push(handler);
  }

  dispatch(name, event = {}) {
    for (const handler of this.listeners[name] ?? []) {
      handler({ preventDefault() {}, ...event });
    }
  }

  append(...nodes) {
    for (const node of nodes) {
      this.children.push(node);
      if (node instanceof FakeElement) {
        node.parentElement = this;
      }
    }
  }

  replaceChildren(...nodes) {
    this.children = [];
    this.append(...nodes);
  }

  replaceWith(node) {
    this.replacedWith = node;
    if (this.parentElement) {
      const index = this.parentElement.children.indexOf(this);
      this.parentElement.children[index] = node;
    }
  }

  // Tag selectors ("pre") and single-class selectors (".stream-body"); the
  // page uses no others below the element level.
  querySelector(selector) {
    const matches = selector.startsWith(".")
      ? (element) => element.classNames.has(selector.slice(1))
      : (element) => element.tagName === selector.toUpperCase();
    const walk = (element) => {
      for (const child of element.children) {
        if (matches(child)) {
          return child;
        }
        const nested = walk(child);
        if (nested) {
          return nested;
        }
      }
      return undefined;
    };
    return walk(this);
  }

  cloneNode() {
    return new FakeElement(this.tagName);
  }

  scrollIntoView() {}
}

class FakeComment {
  constructor(data) {
    this.nodeType = 8;
    this.data = data;
    this.parentElement = undefined;
  }
}

class FakeDocument {
  constructor() {
    this.registry = new Map();
    this.created = [];
    // Nodes the page reaches for by name rather than by selector. They stay
    // out of the registry so querySelectorAll("button, input, ...") does not
    // hand them to disableControls.
    this.head = new FakeElement("head");
    this.body = new FakeElement("body");
    this.documentElement = new FakeElement("html");
    // The sticky-scroll check compares viewport against page height. Zero
    // keeps isNearBottom() true, which is the transcript's normal state and
    // the one the assertions describe.
    this.documentElement.scrollHeight = 0;
    for (const id of [
      "transcript", "streaming", "pending", "connection-dot",
      "connection-label", "session-id", "peer-count", "event-count",
      "action-status",
    ]) {
      this.register(id, new FakeElement("div"));
    }
    this.register("actor", new FakeElement("input"));
    this.register("model", new FakeElement("select"));
    this.register("prompt", new FakeElement("textarea"));
    for (const id of ["send", "steer", "abort", "change-model"]) {
      this.register(id, new FakeElement("button"));
    }
    const template = new FakeElement("template");
    template.content = new FakeElement("div");
    this.register("empty-template", template);
    // The streaming card wraps its Markdown body in a .stream-body div, which
    // is the node app.js writes the live assistant turn into.
    const streamBody = new FakeElement("div");
    streamBody.className = "md stream-body";
    this.registry.get("#streaming").append(streamBody);
  }

  register(id, element) {
    element.id = id;
    this.registry.set(`#${id}`, element);
  }

  querySelector(selector) {
    return this.registry.get(selector) ?? null;
  }

  querySelectorAll() {
    return [...this.registry.values(), ...this.created];
  }

  createElement(tagName) {
    const element = new FakeElement(tagName);
    this.created.push(element);
    return element;
  }

  createComment(data) {
    return new FakeComment(data);
  }
}

class FakeEventSource {
  static instances = [];

  constructor(url) {
    this.url = url;
    this.listeners = {};
    this.closed = false;
    FakeEventSource.instances.push(this);
  }

  addEventListener(name, handler) {
    (this.listeners[name] ??= []).push(handler);
  }

  close() {
    this.closed = true;
  }

  emit(name, payload) {
    const message = payload === undefined ? {} : { data: JSON.stringify(payload) };
    for (const handler of this.listeners[name] ?? []) {
      handler(message);
    }
  }
}

function define(key, value) {
  Object.defineProperty(globalThis, key, {
    value,
    configurable: true,
    writable: true,
  });
}

export function flush() {
  return new Promise((resolve) => setTimeout(resolve, 0));
}

let instance = 0;

export async function loadApp({ catalog, catalogStatus = 200 } = {}) {
  const document = new FakeDocument();
  const storage = new Map();
  const fetchCalls = [];
  FakeEventSource.instances = [];

  define("document", document);
  define("localStorage", {
    getItem: (key) => (storage.has(key) ? storage.get(key) : null),
    setItem: (key, value) => storage.set(key, String(value)),
    removeItem: (key) => storage.delete(key),
  });
  define("EventSource", FakeEventSource);
  define("fetch", async (url, options = {}) => {
    const call = { url: String(url), options };
    if (options.body) {
      call.body = JSON.parse(options.body);
    }
    fetchCalls.push(call);
    if (call.url === "/api/models") {
      if (catalogStatus !== 200) {
        return { ok: false, status: catalogStatus, json: async () => ({}) };
      }
      return { ok: true, status: 200, json: async () => catalog ?? { models: [] } };
    }
    return { ok: true, status: 200, json: async () => ({ ok: true }) };
  });
  // The page scrolls itself to the bottom as the transcript grows. The window
  // is short and already at the bottom, so scrollTo is a no-op recorder.
  define("window", {
    innerHeight: 800,
    scrollY: 0,
    scrollTo() {},
  });
  define("requestAnimationFrame", (callback) => setTimeout(() => callback(0), 0));
  // Insecure context: getRandomValues exists, randomUUID intentionally absent.
  define("crypto", {
    getRandomValues(array) {
      randomFillSync(array);
      return array;
    },
  });

  instance += 1;
  const moduleUrl = new URL("../public/app.js", import.meta.url);
  await import(`${moduleUrl.href}?instance=${instance}`);
  await flush();
  return {
    document,
    storage,
    fetchCalls,
    source: FakeEventSource.instances[0],
    flush,
  };
}
