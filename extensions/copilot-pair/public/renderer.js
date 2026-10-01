/*
 * Rich-text rendering for the pair transcript, built on standard libraries
 * instead of a hand-rolled parser: marked (Markdown), KaTeX with
 * marked-katex-extension (math), DOMPurify (sanitization), and highlight.js
 * (code). They load lazily from jsDelivr — pinned versions with subresource
 * integrity — so the extension still requires no install step. Until they
 * arrive, or offline, messages render as plain text.
 */

const CDN_ASSETS = [
  {
    kind: "style",
    href: "https://cdn.jsdelivr.net/npm/katex@0.16.21/dist/katex.min.css",
    integrity: "sha384-zh0CIslj+VczCZtlzBcjt5ppRcsAmDnRem7ESsYwWwg3m/OaJ2l4x7YBZl9Kxxib",
  },
  {
    kind: "style",
    href: "https://cdn.jsdelivr.net/npm/@highlightjs/cdn-assets@11.11.1/styles/github-dark.min.css",
    integrity: "sha384-wH75j6z1lH97ZOpMOInqhgKzFkAInZPPSPlZpYKYTOqsaizPvhQZmAtLcPKXpLyH",
  },
  // Scripts execute in this order (async=false): the KaTeX global must exist
  // before marked-katex-extension's UMD wrapper captures it at load time.
  {
    kind: "script",
    src: "https://cdn.jsdelivr.net/npm/katex@0.16.21/dist/katex.min.js",
    integrity: "sha384-Rma6DA2IPUwhNxmrB/7S3Tno0YY7sFu9WSYMCuulLhIqYSGZ2gKCJWIqhBWqMQfh",
  },
  {
    kind: "script",
    src: "https://cdn.jsdelivr.net/npm/marked@18.0.9/lib/marked.umd.js",
    integrity: "sha384-kyb7rY2xnKgiRpRdAeNJxQ8e2SiGZy1m+FK9yME6M0eGJpg7EFaGGvf7+JSirgqE",
  },
  {
    kind: "script",
    src: "https://cdn.jsdelivr.net/npm/marked-katex-extension@5.1.10/lib/index.umd.js",
    integrity: "sha384-XxGEZv9F7hWupeJBnhRjriQyXeFcyOlXo3zlrowvdAwLwShaBaRBTAOUaFCxfO7J",
  },
  {
    kind: "script",
    src: "https://cdn.jsdelivr.net/npm/dompurify@3.4.13/dist/purify.min.js",
    integrity: "sha384-ZuC+DIACqSIZTsp+7YF57cR5Y+6qXa7YFbEKdA/EHA/R0T+41dtorqucYl71Zp+t",
  },
  {
    kind: "script",
    src: "https://cdn.jsdelivr.net/npm/@highlightjs/cdn-assets@11.11.1/highlight.min.js",
    integrity: "sha384-RH2xi4eIQ/gjtbs9fUXM68sLSi99C7ZWBRX1vDrVv6GQXRibxXLbwO2NGZB74MbU",
  },
];

let ready = false;
let started = false;
const readyCallbacks = [];

export function rendererReady() {
  return ready;
}

export function onRendererReady(callback) {
  readyCallbacks.push(callback);
}

export function ensureRenderer() {
  if (started) {
    return;
  }
  started = true;
  let remainingScripts = CDN_ASSETS.filter((asset) => asset.kind === "script").length;
  for (const asset of CDN_ASSETS) {
    let element;
    if (asset.kind === "style") {
      element = document.createElement("link");
      element.rel = "stylesheet";
      element.href = asset.href;
    } else {
      element = document.createElement("script");
      element.src = asset.src;
      element.async = false;
      element.addEventListener("load", () => {
        remainingScripts -= 1;
        if (remainingScripts === 0) {
          configure();
        }
      });
      // A failed script leaves remainingScripts above zero: the page simply
      // stays in plain-text mode instead of half-working.
    }
    element.integrity = asset.integrity;
    element.crossOrigin = "anonymous";
    document.head.append(element);
  }
}

function configure() {
  globalThis.marked.use(
    { gfm: true, breaks: true },
    globalThis.markedKatex({ throwOnError: false }),
  );
  globalThis.DOMPurify.addHook("afterSanitizeAttributes", (node) => {
    if (node.tagName === "A") {
      node.setAttribute("target", "_blank");
      node.setAttribute("rel", "noopener noreferrer");
    }
  });
  ready = true;
  for (const callback of readyCallbacks.splice(0)) {
    callback();
  }
}

export function renderRichText(element, text) {
  element.classList.toggle("plain", !ready);
  if (!ready) {
    element.textContent = text;
    return;
  }
  const html = globalThis.marked.parse(normalizeMathDelimiters(text));
  element.innerHTML = globalThis.DOMPurify.sanitize(html);
  for (const code of element.querySelectorAll("pre code")) {
    globalThis.hljs.highlightElement(code);
  }
  attachCopyButtons(element);
}

// marked-katex-extension understands $…$ and $$…$$; models frequently emit
// \(…\) and \[…\] instead, so translate those outside of code spans/fences.
function normalizeMathDelimiters(text) {
  return text.split(/(```[\s\S]*?(?:```|$)|`[^`\n]*`)/).map((segment, index) => {
    if (index % 2 === 1) {
      return segment;
    }
    return segment
      .replaceAll(/\\\[([\s\S]+?)\\\]/g, (_match, tex) => `\n$$\n${tex}\n$$\n`)
      .replaceAll(/\\\(([\s\S]+?)\\\)/g, (_match, tex) => `$${tex}$`);
  }).join("");
}

function attachCopyButtons(root) {
  for (const pre of root.querySelectorAll("pre")) {
    const button = document.createElement("button");
    button.type = "button";
    button.className = "copy-btn";
    button.textContent = "Copy";
    button.addEventListener("click", () => {
      void copyText(pre.querySelector("code")?.innerText ?? pre.innerText).then(() => {
        button.textContent = "Copied";
        setTimeout(() => {
          button.textContent = "Copy";
        }, 1400);
      });
    });
    pre.append(button);
  }
}

// The share URL is plain http on a LAN, so the async clipboard API is often
// unavailable (no secure context); fall back to a transient selection.
function copyText(text) {
  if (navigator.clipboard?.writeText) {
    return navigator.clipboard.writeText(text).catch(() => copyTextFallback(text));
  }
  return Promise.resolve(copyTextFallback(text));
}

function copyTextFallback(text) {
  const area = document.createElement("textarea");
  area.value = text;
  area.style.position = "fixed";
  area.style.opacity = "0";
  document.body.append(area);
  area.select();
  try {
    document.execCommand("copy");
  } finally {
    area.remove();
  }
}
