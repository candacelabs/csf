/*
 * A minimal Chrome DevTools Protocol client, and the launcher for the browser
 * the whole of §4 is measured through.
 *
 * -----------------------------------------------------------------------------
 * Why this is not Playwright
 *
 * §4 says "Playwright driving headless Chromium over CDP, pinned version, same
 * browser binary, same flags, same viewport, same profile handling. No
 * stack-specific branch exists in the harness". Every property in that sentence
 * is a property of the HARNESS, and all of them hold here. What does not hold is
 * the library name, for one reason: Playwright ships its own browser build and
 * downloads it at install time from a Microsoft CDN, which is a network fetch
 * outside the npm registry and outside what this tree is permitted to do. The
 * bench container already carries a pinned Chromium at /usr/bin/chromium, and
 * §5.2 requires the browser binary to be pinned and recorded — which it is,
 * by digest of the image that contains it.
 *
 * §12 freezes §2, §3, §5, §7 and §8's row set. §4 is not in that list, so
 * choosing a CDP client is not an amendment; it is an implementation choice
 * inside a section the freeze leaves open. It is recorded in bench/README.md
 * and in the audit summary so QA-2 can overrule it rather than discover it.
 *
 * The parts of Playwright this deliberately does not reimplement — selector
 * engines, auto-waiting, retries — are parts the spec forbids anyway: every
 * wait in this harness is a §3.1 paint predicate or a §3.3 ready signal, and a
 * convenience retry would silently turn a missed paint into a slower one.
 */
import { spawn } from 'node:child_process';
import { mkdtempSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join } from 'node:path';

import WebSocket from 'ws';

/** §4: "same viewport (1440 x 900, DPR 1)". */
export const VIEWPORT = { width: 1440, height: 900, deviceScaleFactor: 1 };

/**
 * The browser binary. Pinned by the container image, not by this file — §5.2
 * pins base images by digest and the run manifest records which one ran.
 */
export const CHROMIUM = process.env.BENCH_CHROMIUM ?? '/usr/bin/chromium';

/**
 * Flags. Identical for both stacks by construction, because there is one list.
 *
 * --headless=new is the current headless implementation and the one whose
 * rendering path matches headful; the old one did not run the compositor the
 * same way, which would have made paint_present (§3.1) measure something else.
 *
 * Nothing here disables a feature that would flatter one stack: no
 * --disable-gpu-rasterization, no --disable-background-timer-throttling (a
 * backgrounded tab is not measured), no --disable-ipc-flooding-protection.
 * Additions to this list are a fairness question, not a convenience question.
 */
export const FLAGS = [
  '--headless=new',
  '--no-first-run',
  '--no-default-browser-check',
  '--disable-extensions',
  '--disable-background-networking',
  '--disable-component-update',
  '--disable-sync',
  '--metrics-recording-only',
  '--no-sandbox',
  '--disable-dev-shm-usage',
  `--window-size=${VIEWPORT.width},${VIEWPORT.height}`,
  '--force-device-scale-factor=1',
];

export class Cdp {
  #socket;
  #nextId = 1;
  #pending = new Map();
  #handlers = new Map();

  constructor(socket) {
    this.#socket = socket;
    socket.on('message', (raw) => this.#dispatch(JSON.parse(raw.toString())));
  }

  static async connect(url) {
    const socket = new WebSocket(url, { maxPayload: 256 * 1024 * 1024 });
    await new Promise((resolve, reject) => {
      socket.once('open', resolve);
      socket.once('error', reject);
    });
    return new Cdp(socket);
  }

  #dispatch(message) {
    if (message.id !== undefined) {
      const waiter = this.#pending.get(message.id);
      if (!waiter) return;
      this.#pending.delete(message.id);
      if (message.error) waiter.reject(new Error(`${message.error.message} (${message.error.code})`));
      else waiter.resolve(message.result);
      return;
    }
    const key = message.sessionId ? `${message.sessionId}:${message.method}` : message.method;
    for (const k of [key, message.method]) {
      const listeners = this.#handlers.get(k);
      if (!listeners) continue;
      for (const listener of [...listeners]) listener(message.params, message.sessionId);
    }
  }

  on(method, listener) {
    if (!this.#handlers.has(method)) this.#handlers.set(method, new Set());
    this.#handlers.get(method).add(listener);
    return () => this.#handlers.get(method)?.delete(listener);
  }

  send(method, params = {}, sessionId) {
    const id = this.#nextId++;
    const payload = { id, method, params };
    if (sessionId) payload.sessionId = sessionId;
    return new Promise((resolve, reject) => {
      this.#pending.set(id, { resolve, reject });
      this.#socket.send(JSON.stringify(payload));
    });
  }

  close() {
    this.#socket.close();
  }
}

/**
 * Launch Chromium with a FRESH profile directory.
 *
 * §3.3's cold definition is "fresh browser profile directory,
 * Network.clearBrowserCache + Network.clearBrowserCookies, new context, per
 * iteration". The profile directory is the part that cannot be undone from
 * inside a running browser, so it is per launch and the launcher owns it.
 */
export async function launch({ extraFlags = [] } = {}) {
  const profile = mkdtempSync(join(tmpdir(), 'bench-chromium-'));
  const child = spawn(
    CHROMIUM,
    [...FLAGS, ...extraFlags, `--user-data-dir=${profile}`, '--remote-debugging-port=0', 'about:blank'],
    { stdio: ['ignore', 'pipe', 'pipe'] },
  );

  const url = await new Promise((resolve, reject) => {
    let buffer = '';
    const timer = setTimeout(() => reject(new Error('chromium did not print a DevTools URL')), 30_000);
    const onData = (chunk) => {
      buffer += chunk.toString();
      const match = buffer.match(/ws:\/\/[^\s]+/);
      if (!match) return;
      clearTimeout(timer);
      child.stderr.off('data', onData);
      resolve(match[0]);
    };
    child.stderr.on('data', onData);
    child.once('exit', (code) => {
      clearTimeout(timer);
      reject(new Error(`chromium exited before listening (code ${code})\n${buffer}`));
    });
  });

  const cdp = await Cdp.connect(url);

  return {
    cdp,
    profile,
    async version() {
      return cdp.send('Browser.getVersion');
    },
    async close() {
      try {
        await cdp.send('Browser.close');
      } catch {
        child.kill('SIGKILL');
      }
      cdp.close();
      rmSync(profile, { recursive: true, force: true });
    },
  };
}

/**
 * A page in a FRESH browser context.
 *
 * One context per iteration is what makes "cold" mean cold: a context owns its
 * own cookie jar and HTTP cache, so a second navigation in a NEW context is a
 * cold load and a second navigation in the SAME context is §3.3's warm load.
 * Both are reported (FR-71.5) and the difference between them is exactly this
 * function being called again or not.
 */
export async function newPage(browser, { networkProfile = null } = {}) {
  const { cdp } = browser;
  const { browserContextId } = await cdp.send('Target.createBrowserContext', {
    disposeOnDetach: true,
  });
  const { targetId } = await cdp.send('Target.createTarget', {
    url: 'about:blank',
    browserContextId,
  });
  const { sessionId } = await cdp.send('Target.attachToTarget', { targetId, flatten: true });

  const send = (method, params) => cdp.send(method, params, sessionId);

  await send('Page.enable');
  await send('Runtime.enable');
  await send('Network.enable');
  await send('Performance.enable');
  await send('Emulation.setDeviceMetricsOverride', {
    ...VIEWPORT,
    mobile: false,
  });

  if (networkProfile) {
    /* §5.7's three profiles, applied by CDP identically to both stacks —
       browser-side, therefore symmetric by construction. */
    await send('Network.emulateNetworkConditions', {
      offline: false,
      latency: networkProfile.latencyMs,
      downloadThroughput: networkProfile.downKbps * 125, // kbit/s -> bytes/s
      uploadThroughput: networkProfile.upKbps * 125,
    });
  }

  return {
    sessionId,
    targetId,
    browserContextId,
    send,
    on: (method, listener) => cdp.on(`${sessionId}:${method}`, listener),

    async clearCache() {
      await send('Network.clearBrowserCache');
      await send('Network.clearBrowserCookies');
    },

    async goto(url) {
      const loaded = new Promise((resolve) => {
        const off = cdp.on(`${sessionId}:Page.loadEventFired`, () => {
          off();
          resolve();
        });
      });
      await send('Page.navigate', { url });
      await loaded;
    },

    async eval(expression, { awaitPromise = true } = {}) {
      const result = await send('Runtime.evaluate', {
        expression,
        awaitPromise,
        returnByValue: true,
      });
      if (result.exceptionDetails) {
        throw new Error(
          `page evaluate threw: ${result.exceptionDetails.exception?.description ?? result.exceptionDetails.text}`,
        );
      }
      return result.result.value;
    },

    async close() {
      await cdp.send('Target.closeTarget', { targetId });
      await cdp.send('Target.disposeBrowserContext', { browserContextId });
    },
  };
}

/** §5.7's network profiles. LAN is the profile G1 is stated against. */
export const NETWORK_PROFILES = {
  lan: null,
  broadband: { latencyMs: 25, downKbps: 20_000, upKbps: 5_000 },
  mobile: { latencyMs: 100, downKbps: 4_000, upKbps: 1_000 },
};
