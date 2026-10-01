/*
 * §3.6's synthetic session driver.
 *
 * "1000 real Chromium tabs is not feasible on one host. A synthetic driver
 * speaks each stack's actual protocol — gotth-live: liquid proto over the
 * ADR-001 transport, real handshake, real events; Next.js: the same document
 * fetch plus the same SSE/WS channel — and consumes and discards pushed
 * payloads at the rate a browser would (no artificial backpressure). It is
 * pinned to CPUs disjoint from the server under test."
 *
 * -----------------------------------------------------------------------------
 * The frame layout is IMPORTED, never re-implemented
 *
 * measure-memory.mjs used to carry a note saying a driver "written against a
 * guessed frame layout would fail the 10-tab validation gate for a reason that
 * is not the stack". The remedy is the one import below: `../../client/codec.gen.js`
 * is the very file the browser runs, generated from the same FileDescriptorSet
 * as the Go side, so this driver and a real tab cannot disagree about a field
 * number, a wire type or a length bound. FR-74(a) permits JavaScript under
 * `bench/` to reach the library by RELATIVE PATH; it does not permit a
 * package.json dependency on it, and there is none.
 *
 * What that import buys is narrow and worth stating: it removes *encoding* as a
 * cause of a 10-tab failure. It does not remove *behaviour* as one, so the
 * behaviour below is transcribed from client/runtime.js rather than invented:
 *
 *   - the upgrade offers the `gotth-live.v1` subprotocol and an Origin the
 *     server's allowlist accepts (internal/wsx/handler.go denies an absent
 *     Origin, so a driver that omitted it would measure 403s);
 *   - the document is fetched FIRST and its cookies are carried into the
 *     upgrade, because that is where each bench app mints the per-page-load
 *     bench session id its Config.Init reads;
 *   - every applied Patch or Snapshot is followed by an Ack AND a
 *     ClientTelemetry frame, exactly as runtime.js's applied() sends them. A
 *     driver that skipped them would understate inbound frame handling by two
 *     frames per patch — at the dashboard's rate, ~106 frames/s/session of
 *     traffic the server would never see;
 *   - a heartbeat is echoed with both fields verbatim (protocol §3.4);
 *   - a sequence gap latches, sends a ResyncRequest and acks at the sequence
 *     actually held (FR-11), instead of applying past the gap.
 *
 * -----------------------------------------------------------------------------
 * Where this driver is NOT a browser, stated rather than hidden
 *
 *   1. There is no DOM, so ClientTelemetry's morph_micros / apply_micros carry
 *      this driver's own decode-and-discard timings rather than a real morph.
 *      The frame is sent, at the same rate, with the same shape and the same
 *      uint32 clamp; only the two values inside it differ. They are inputs to
 *      the server's telemetry, not to its session state.
 *   2. The Next.js half fetches the document and opens the channel, which is
 *      what §3.6 asks of it, and does not fetch sub-resources. A browser also
 *      pulls the JS chunks and the stylesheet; those are static-file reads that
 *      end before the steady-state window opens.
 *   3. The Next.js half cannot dispatch a Server Action, so §3.4's
 *      active-light and active-heavy CLIENT->server halves are refused on that
 *      stack rather than approximated. See sendMutation() below for the whole
 *      reason and bench/README.md deviation D-7 for its consequence.
 *
 * All three are exactly the kind of thing the 10-tab validation gate exists to
 * bound, which is why that gate is mandatory and why nothing here claims to
 * have discharged it.
 */
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import WebSocket from 'ws';

import { decodeFrame, encodeFrame, ResyncReason } from '../../client/codec.gen.js';
import { ensureBenchTrust } from './bench-tls.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));

/** internal/protocol/limits.go's Subprotocol, and client/runtime.js's VERSION. */
export const SUBPROTOCOL = 'gotth-live.v1';
export const PROTOCOL_VERSION = 1;

/**
 * The driver's own source digest, recorded in the validation artifact.
 *
 * A validation result is a statement about a particular driver. Changing this
 * file invalidates it, and the gate has to be able to see that without being
 * told — see gate.mjs's staleness check.
 */
export function driverSha256() {
  return createHash('sha256').update(readFileSync(join(HERE, 'driver.mjs'))).digest('hex');
}

/* -------------------------------------------------------------------------- */
/* Frame construction — the half a test can check against the real codec.      */
/* -------------------------------------------------------------------------- */

/**
 * client/runtime.js's send(): every client frame carries the negotiated version
 * and the session id the first Snapshot handed out. Both are re-asserted in
 * band on every frame, which is the protocol's own rule (docs/protocol.md §1).
 */
export function clientFrame(sessionId, body) {
  return { protocol_version: PROTOCOL_VERSION, session_id: sessionId, ...body };
}

export function ackFrame(sessionId, serverSeq) {
  return clientFrame(sessionId, { ack: { server_seq: serverSeq } });
}

/** Both fields echoed verbatim (docs/protocol.md §3.4). */
export function heartbeatEcho(sessionId, heartbeat) {
  return clientFrame(sessionId, {
    heartbeat: { nonce: heartbeat.nonce, interval_ms: heartbeat.interval_ms },
  });
}

export function telemetryFrame(sessionId, { patchId, morphMs, applyMs }) {
  return clientFrame(sessionId, {
    client_telemetry: {
      patch_id: patchId,
      morph_micros: micros(morphMs),
      apply_micros: micros(applyMs),
    },
  });
}

export function eventFrame(sessionId, { clientRef, name, fragmentId, seenServerSeq, fields }) {
  return clientFrame(sessionId, {
    event: {
      client_ref: clientRef,
      name,
      fragment_id: fragmentId,
      seen_server_seq: seenServerSeq,
      fields: Object.entries(fields ?? {}).map(([key, value]) => ({ key, value })),
    },
  });
}

export function resyncFrame(sessionId, lastAppliedSeq, reason = ResyncReason.GAP) {
  return clientFrame(sessionId, {
    resync_request: { last_applied_seq: lastAppliedSeq, reason },
  });
}

/** client/runtime.js's us(): a millisecond duration clamped to the schema's uint32. */
export function micros(ms) {
  const v = Math.round(ms * 1000);
  return v < 0 ? 0 : v > 60_000_000 ? 60_000_000 : v;
}

/* -------------------------------------------------------------------------- */
/* Cookies — a browser carries the document's jar into the upgrade.            */
/* -------------------------------------------------------------------------- */

export function parseSetCookie(headers) {
  const jar = new Map();
  const raw = typeof headers.getSetCookie === 'function' ? headers.getSetCookie() : [];
  for (const line of raw) {
    const pair = line.split(';')[0];
    const eq = pair.indexOf('=');
    if (eq === -1) continue;
    jar.set(pair.slice(0, eq).trim(), pair.slice(eq + 1).trim());
  }
  return jar;
}

export function cookieHeader(jar) {
  return [...jar.entries()].map(([k, v]) => `${k}=${v}`).join('; ');
}

/* -------------------------------------------------------------------------- */
/* The gotth-live session.                                                     */
/* -------------------------------------------------------------------------- */

/**
 * One gotth-live session: document fetch, real upgrade, real frames.
 *
 * `origin` is the origin the page was served from and the value sent in the
 * Origin header, because internal/wsx/handler.go's allowlist denies an absent
 * Origin ("a request with no Origin is not a request from an allowed one") and
 * a driver measuring 403s would measure nothing at all.
 */
export class GotthSession {
  /* There is no `dispatcher` here any more. There used to be one — accepted,
     stored, and passed to nothing — which is how this class managed to look
     like it had solved TLS trust while `fetchDocument()` below used a bare
     global `fetch`. Trust is now the process-wide additive anchor in
     bench-tls.mjs, which `fetch`, `ws` and `tls.connect` all consult; see that
     file for why a per-request dispatcher was not the shape taken. */
  constructor({ origin, route, mountPath }) {
    this.origin = origin;
    this.route = route;
    this.mountPath = mountPath;
    this.jar = new Map();
    this.sessionId = null;
    this.seq = 0;
    this.ref = 0;
    this.gap = false;
    this.status = 'connecting';
    this.received = 0;
    this.applied = 0;
    this.bytesDown = 0;
    this.bytesUp = 0;
    this.sent = 0;
    this.errors = [];
    this.socket = null;
    this.closed = false;
  }

  /** The document fetch a tab makes before the runtime ever opens a socket. */
  async fetchDocument() {
    /* Before the first byte, not at import time: the §3.6 topology's proxy holds
       a self-signed certificate and this process has to be told about it, once,
       additively. A no-op on a plaintext origin and on every call after the
       first. */
    ensureBenchTrust(this.origin);
    const res = await fetch(`${this.origin}${this.route}`, {
      headers: { Accept: 'text/html', 'Accept-Encoding': 'gzip' },
    });
    for (const [k, v] of parseSetCookie(res.headers)) this.jar.set(k, v);
    const body = await res.text();
    this.documentBytes = body.length;
    if (!res.ok) throw new Error(`document ${this.route} -> ${res.status}`);
    return body;
  }

  async open({ timeoutMs = 30_000 } = {}) {
    await this.fetchDocument();

    const url = new URL(this.mountPath, this.origin);
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';

    const headers = { Origin: this.origin };
    const cookie = cookieHeader(this.jar);
    if (cookie !== '') headers.Cookie = cookie;

    const socket = new WebSocket(url.href, [SUBPROTOCOL], {
      headers,
      /* No `rejectUnauthorized: false`. This used to say a node client cannot
         pin, which was wrong: the bench certificate is in this process's
         default CA store by the time fetchDocument() above has returned, so
         this connection is fully verified — chain and hostname both — against
         exactly the certificate docker/gen-cert.sh generated. See
         bench-tls.mjs. */
      perMessageDeflate: false,
    });
    this.socket = socket;

    await new Promise((ok, fail) => {
      const timer = setTimeout(() => fail(new Error('gotth upgrade timed out')), timeoutMs);
      socket.once('open', () => {
        clearTimeout(timer);
        ok();
      });
      socket.once('error', (err) => {
        clearTimeout(timer);
        fail(err);
      });
    });

    socket.on('message', (data) => this.#onMessage(data));
    socket.on('close', (code, reason) => {
      this.status = this.closed ? 'closed' : 'reconnecting';
      if (!this.closed && code !== 1000) {
        this.errors.push(`socket closed ${code} ${reason?.toString?.() ?? ''}`.trim());
      }
    });

    /* A session is not established until the first Snapshot: H-10 says the
       client sends nothing before it, and §3.4 says an active session has
       "exchanged at least one frame". Resolving `open()` at the TCP upgrade
       would count sessions the server has not built state for yet. */
    await this.live(timeoutMs);
    return this;
  }

  live(timeoutMs = 30_000) {
    if (this.status === 'live') return Promise.resolve(this);
    return new Promise((ok, fail) => {
      const timer = setTimeout(
        () => fail(new Error('no Snapshot within the handshake window')),
        timeoutMs,
      );
      this.onLive = () => {
        clearTimeout(timer);
        ok(this);
      };
      this.onFail = (err) => {
        clearTimeout(timer);
        fail(err);
      };
    });
  }

  #send(frame) {
    if (!this.socket || this.socket.readyState !== 1 || !this.sessionId) return false;
    const bytes = encodeFrame(frame);
    this.socket.send(bytes);
    this.bytesUp += bytes.length;
    this.sent += 1;
    return true;
  }

  #onMessage(data) {
    /* No queue, no accumulation: the payload is decoded, counted and dropped on
       the same turn of the loop it arrived on. That is what §3.6's "consumes
       and discards ... at the rate a browser would (no artificial
       backpressure)" means on this side of the wire. */
    const t0 = performance.now();
    const bytes = data instanceof Buffer ? new Uint8Array(data) : new Uint8Array(Buffer.from(data));
    this.bytesDown += bytes.length;
    this.received += 1;

    let frame;
    try {
      frame = decodeFrame(bytes);
    } catch (err) {
      this.errors.push(`undecodable frame: ${err.message}`);
      this.socket.close(4002, 'undecodable frame');
      return;
    }

    if (frame.snapshot) {
      if (frame.protocol_version !== PROTOCOL_VERSION) {
        this.errors.push(`protocol version ${frame.protocol_version}`);
        this.socket.close(4003, 'protocol version');
        this.onFail?.(new Error(`protocol version ${frame.protocol_version}`));
        return;
      }
      this.sessionId = frame.session_id;
      this.heartbeatIntervalMs = frame.snapshot.heartbeat_interval_ms ?? null;
      this.status = 'live';
      this.#applied(frame.snapshot, t0);
      this.onLive?.();
      return;
    }

    if (frame.patch) {
      /* FR-11: applying past a gap would put the DOM in a state no server
         render ever produced. Latch, ask, and ack at the sequence held. */
      if (frame.patch.server_seq !== this.seq + 1) {
        if (!this.gap && this.seq) {
          this.gap = true;
          this.#send(resyncFrame(this.sessionId, this.seq));
        }
        this.#send(ackFrame(this.sessionId, this.seq));
        return;
      }
      this.#applied(frame.patch, t0);
      return;
    }

    if (frame.heartbeat) {
      this.#send(heartbeatEcho(this.sessionId, frame.heartbeat));
      return;
    }

    if (frame.error) {
      this.errors.push(`server error code ${frame.error.code}: ${frame.error.message ?? ''}`);
    }
  }

  #applied(p, t0) {
    this.seq = p.server_seq;
    this.gap = false;
    this.applied += 1;
    const t1 = performance.now();
    this.#send(ackFrame(this.sessionId, this.seq));
    this.#send(
      telemetryFrame(this.sessionId, {
        patchId: p.patch_id,
        /* No DOM: these are this driver's decode-and-discard timings. The
           frame, its rate and its clamp are the browser's; the two values are
           not. Declared at the top of this file and in bench/README.md. */
        morphMs: t1 - t0,
        applyMs: t1 - t0,
      }),
    );
  }

  /**
   * §3.4's active workloads, as REAL events over the real transport.
   *
   * `fragmentId` is required and is not defaulted to the empty string. That is
   * not defensiveness: `Event.fragment_id` carries a 1:64 length predicate, an
   * empty string encodes as an ABSENT field, and the server answers an absent
   * one with `Error{INVALID_FRAME}` — "event violates its schema: correct the
   * offending field", which this driver was observed to earn on its first run
   * against bench/apps/dashboard/gotth. A real binding always names the region
   * its element sits in (client/runtime.js's sendEvent takes `fid` from the
   * bound element), so this one must too.
   */
  sendEvent(name, { fragmentId, fields = {} } = {}) {
    if (!this.seq) return false; // H-10: nothing before the first Snapshot
    if (typeof fragmentId !== 'string' || fragmentId === '') {
      throw new Error(
        `sendEvent(${JSON.stringify(name)}) needs the fragment id of the region the ` +
          'control lives in: Event.fragment_id is bounded 1:64 and an absent one is ' +
          'refused with INVALID_FRAME before the reducer runs.',
      );
    }
    return this.#send(
      eventFrame(this.sessionId, {
        clientRef: ++this.ref,
        name,
        fragmentId,
        seenServerSeq: this.seq,
        fields,
      }),
    );
  }

  async close() {
    this.closed = true;
    if (this.socket && this.socket.readyState <= 1) {
      await new Promise((ok) => {
        this.socket.once('close', ok);
        this.socket.close(1000, 'bench driver closing');
        setTimeout(ok, 2000);
      });
    }
  }
}

/* -------------------------------------------------------------------------- */
/* The Next.js session.                                                        */
/* -------------------------------------------------------------------------- */

/**
 * The session key the Next.js page mints server-side, per page load.
 *
 * It is a prop on a client component, so it reaches the browser inside the
 * Flight payload the document embeds — which is where the browser reads it
 * from too. Minting one here instead would open a channel for a key no page
 * ever rendered, and §3.4's Next.js session is "one browser tab ... holding the
 * app's SSE stream or WebSocket ... plus its session cookie".
 */
export function sessionKeyFromDocument(html) {
  const match = /sessionKey\\?"\s*:\s*\\?"([0-9a-f]{16,64})\\?"/.exec(html);
  return match ? match[1] : null;
}

export class NextSession {
  constructor({ origin, route, variant = 'sse', streamPath, wsPath = '/ws', pollIntervalMs = 1000 }) {
    this.origin = origin;
    this.route = route;
    this.variant = variant;
    this.streamPath = streamPath;
    this.wsPath = wsPath;
    this.pollIntervalMs = pollIntervalMs;
    this.jar = new Map();
    this.sessionKey = null;
    this.status = 'connecting';
    this.received = 0;
    this.bytesDown = 0;
    this.errors = [];
    this.closed = false;
    this.abort = new AbortController();
  }

  async fetchDocument() {
    /* Same anchor, same reason, same place in the sequence as the gotth-live
       half: the document is the first request either session makes, and the
       three channels below (SSE, WS, poll) all inherit the trust it installs.
       §3.6's gate measures both stacks through the same proxy, so a fix on one
       side only would have moved the failure rather than removed it. */
    ensureBenchTrust(this.origin);
    const res = await fetch(`${this.origin}${this.route}`, {
      headers: {
        Accept: 'text/html',
        'Accept-Encoding': 'gzip',
        ...(this.jar.size ? { Cookie: cookieHeader(this.jar) } : {}),
      },
    });
    for (const [k, v] of parseSetCookie(res.headers)) this.jar.set(k, v);
    const html = await res.text();
    if (!res.ok) throw new Error(`document ${this.route} -> ${res.status}`);
    this.sessionKey = sessionKeyFromDocument(html);
    if (this.sessionKey === null) {
      throw new Error(
        `could not read the server-minted sessionKey out of ${this.route}. The driver ` +
          'will not invent one: a channel opened on a key no page rendered is not the ' +
          "§3.4 session the spec defines. If the app's prop name changed, this " +
          'extraction changed with it and the 10-tab gate is the thing that would ' +
          'have caught the difference.',
      );
    }
    return html;
  }

  async open() {
    await this.fetchDocument();
    if (this.variant === 'sse') return this.#openSse();
    if (this.variant === 'ws') return this.#openWs();
    if (this.variant === 'poll') return this.#openPoll();
    throw new Error(`unknown Next.js variant ${JSON.stringify(this.variant)}`);
  }

  async #openSse() {
    const url = `${this.origin}${this.streamPath}?k=${encodeURIComponent(this.sessionKey)}`;
    const res = await fetch(url, {
      headers: {
        Accept: 'text/event-stream',
        'Cache-Control': 'no-cache',
        ...(this.jar.size ? { Cookie: cookieHeader(this.jar) } : {}),
      },
      signal: this.abort.signal,
    });
    for (const [k, v] of parseSetCookie(res.headers)) this.jar.set(k, v);
    if (!res.ok || res.body === null) throw new Error(`stream ${this.streamPath} -> ${res.status}`);
    this.status = 'live';

    /* Read-and-drop. The reader is pulled in a loop with nothing held between
       turns, which is the no-backpressure requirement: a driver that stopped
       reading would apply TCP backpressure a browser never applies and would
       measure the server's send buffers instead of its sessions. */
    this.pump = (async () => {
      const reader = res.body.getReader();
      try {
        for (;;) {
          const { done, value } = await reader.read();
          if (done) break;
          this.bytesDown += value.byteLength;
          /* Frames are delimited by a blank line; counting them costs one
             pass over bytes that are dropped immediately afterwards. */
          this.received += countSseFrames(value);
        }
      } catch (err) {
        if (!this.closed) this.errors.push(`sse read: ${err.message}`);
      }
      this.status = this.closed ? 'closed' : 'reconnecting';
    })();
    return this;
  }

  async #openWs() {
    const url = new URL(this.wsPath, this.origin);
    url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:';
    url.searchParams.set('k', this.sessionKey);
    const socket = new WebSocket(url.href, {
      headers: {
        Origin: this.origin,
        ...(this.jar.size ? { Cookie: cookieHeader(this.jar) } : {}),
      },
      /* Verified, not waived — see the same note on the gotth-live socket. */
      perMessageDeflate: false,
    });
    this.socket = socket;
    await new Promise((ok, fail) => {
      socket.once('open', ok);
      socket.once('error', fail);
    });
    this.status = 'live';
    socket.on('message', (data) => {
      this.bytesDown += data.length ?? 0;
      this.received += 1;
    });
    socket.on('close', () => {
      this.status = this.closed ? 'closed' : 'reconnecting';
    });
    return this;
  }

  async #openPoll() {
    const url =
      `${this.origin}${this.streamPath}?k=${encodeURIComponent(this.sessionKey)}` +
      `&ttl=${this.pollIntervalMs * 2}`;
    this.status = 'live';
    const tick = async () => {
      if (this.closed) return;
      try {
        const res = await fetch(url, {
          cache: 'no-store',
          headers: this.jar.size ? { Cookie: cookieHeader(this.jar) } : {},
          signal: this.abort.signal,
        });
        const body = await res.arrayBuffer();
        this.bytesDown += body.byteLength;
        this.received += 1;
      } catch (err) {
        if (!this.closed) this.errors.push(`poll: ${err.message}`);
      }
    };
    this.timer = setInterval(tick, this.pollIntervalMs);
    await tick();
    return this;
  }

  /**
   * §3.4's active workloads need a client->server mutation, and on this stack
   * §5.4 makes that a Server Action.
   *
   * A Server Action is dispatched by POSTing to the route with a `Next-Action`
   * header whose value is a build-time id, and a body in React's Flight reply
   * encoding. Both are artefacts of the build, not of the protocol. Writing
   * this driver against a guessed encoding is precisely the failure mode §3.6's
   * 10-tab gate exists to catch — "a driver written against a guessed frame
   * layout would fail the validation gate for a reason that is not the stack" —
   * and the alternative (a Route Handler the app does not use) would measure a
   * mechanism §5.4 forbids the app from using.
   *
   * So it refuses. The consequence is that §3.4's active-light and active-heavy
   * rows for Next.js read "not measured, and why" per §7 until this is built
   * against the real Flight encoding, and bench/README.md deviation D-7 says so
   * where QA-2 reads it. It is never inferred from the idle row.
   */
  sendMutation() {
    throw new Error(
      "the synthetic driver cannot dispatch a Next.js Server Action: its id and its " +
        'body encoding are build artefacts, and a guessed encoding is the exact ' +
        'failure §3.6\'s 10-tab gate exists to catch. §3.4 active-light and ' +
        'active-heavy for the Next.js stack therefore read "not measured" per §7 ' +
        '(bench/README.md, deviation D-7). They are never inferred from the idle row.',
    );
  }

  async close() {
    this.closed = true;
    this.status = 'closed';
    if (this.timer) clearInterval(this.timer);
    if (this.socket && this.socket.readyState <= 1) this.socket.close(1000, 'bench driver closing');
    this.abort.abort();
    await this.pump?.catch(() => {});
  }
}

/**
 * SSE frames in one chunk, counted by their "\n\n" terminator.
 *
 * It counts terminators and not `data:` prefixes, so the 15 s comment
 * heartbeat (": hb\n\n") counts too — which is right for §4.6's frame
 * accounting, where a heartbeat is a frame the server sent. A frame split
 * across two chunks is counted once, at the chunk its terminator lands in.
 */
export function countSseFrames(chunk) {
  let count = 0;
  for (let i = 1; i < chunk.length; i++) {
    if (chunk[i] === 0x0a && chunk[i - 1] === 0x0a) count += 1;
  }
  return count;
}

/* -------------------------------------------------------------------------- */
/* §3.4's workloads.                                                           */
/* -------------------------------------------------------------------------- */

/**
 * §3.4's three workloads.
 *
 * The event names and fragment ids are the apps' own constants
 * (bench/apps/counter/gotth/counter.go, bench/apps/dashboard/gotth/dashboard.go)
 * rather than strings invented here — `Config.Events` is default-deny on both,
 * so a name this table got wrong would be refused with UNKNOWN_EVENT and the
 * "active" workload would quietly be the idle one.
 */
export const WORKLOADS = {
  /* "connected, no application events, heartbeats only". */
  idle: { app: null, interactionEveryMs: null, event: null },
  /* "counter workload: one +1 every 10 s per session". */
  'active-light': {
    app: 'counter',
    interactionEveryMs: 10_000,
    event: { name: 'counter.increment', fragmentId: 'counter.controls', fields: {} },
  },
  /* "full 53 updates/s push per session, one control interaction every 30 s".
     The push rate is the app's, not the driver's; the driver's half of the row
     is the control interaction. `v` is dashboard.go's fieldValue and "all" is a
     member of StatusFilters, because a value outside the closed list is dropped
     by the reducer and would make this workload idle too. */
  'active-heavy': {
    app: 'dashboard',
    interactionEveryMs: 30_000,
    event: { name: 'dash.filter', fragmentId: 'dash.controls', fields: { v: 'all' } },
  },
};

/**
 * A pool of established sessions, and the workload timer that keeps them
 * active.
 *
 * Sessions are opened in bounded parallel batches rather than all at once: a
 * thousand simultaneous upgrades measures the accept queue, and §3.6's window
 * is a STEADY state reached after establishment, not during it.
 */
export class SessionPool {
  constructor({ make, workload = 'idle', batch = 25, batchPauseMs = 50 }) {
    this.make = make;
    this.workload = workload;
    this.batch = batch;
    this.batchPauseMs = batchPauseMs;
    this.sessions = [];
    this.timers = [];
  }

  async establish(n) {
    const spec = WORKLOADS[this.workload];
    if (!spec) throw new Error(`unknown workload ${JSON.stringify(this.workload)}`);
    for (let i = 0; i < n; i += this.batch) {
      const slice = [];
      for (let j = i; j < Math.min(n, i + this.batch); j++) slice.push(this.make(j));
      const opened = await Promise.all(slice.map((s) => s.open().then(() => s)));
      this.sessions.push(...opened);
      if (i + this.batch < n) await sleep(this.batchPauseMs);
    }

    if (spec.interactionEveryMs !== null) {
      /* Spread the interactions across the interval instead of firing all N at
         once: a thousand simultaneous clicks is a thundering herd no session
         count produces in life, and it would put a burst in the middle of the
         steady-state window. */
      for (const [index, session] of this.sessions.entries()) {
        const offset = Math.round((index / this.sessions.length) * spec.interactionEveryMs);
        const timer = setTimeout(() => {
          const repeat = setInterval(() => {
            try {
              if (typeof session.sendEvent === 'function') {
                session.sendEvent(spec.event.name, {
                  fragmentId: spec.event.fragmentId,
                  fields: spec.event.fields,
                });
              } else {
                session.sendMutation();
              }
            } catch (err) {
              session.errors.push(err.message);
              clearInterval(repeat);
            }
          }, spec.interactionEveryMs);
          this.timers.push(repeat);
        }, offset);
        this.timers.push(timer);
      }
    }
    return this.sessions;
  }

  stats() {
    return {
      sessions: this.sessions.length,
      live: this.sessions.filter((s) => s.status === 'live').length,
      framesReceived: this.sessions.reduce((a, s) => a + s.received, 0),
      bytesDown: this.sessions.reduce((a, s) => a + s.bytesDown, 0),
      bytesUp: this.sessions.reduce((a, s) => a + (s.bytesUp ?? 0), 0),
      errors: this.sessions.flatMap((s) => s.errors),
    };
  }

  async teardown() {
    for (const t of this.timers) {
      clearTimeout(t);
      clearInterval(t);
    }
    this.timers = [];
    await Promise.all(this.sessions.map((s) => s.close().catch(() => {})));
    this.sessions = [];
  }
}

/** The factory the harness hands SessionPool, one per stack. */
export function sessionFactory({ stack, origin, route, mountPath, variant, streamPath, wsPath }) {
  if (stack === 'gotth') {
    return () => new GotthSession({ origin, route, mountPath });
  }
  if (stack === 'next') {
    return () => new NextSession({ origin, route, variant, streamPath, wsPath });
  }
  throw new Error(
    `unknown stack ${JSON.stringify(stack)}: --stack is told, not sniffed, and an ` +
      'unrecognised value is refused rather than treated as "not gotth"',
  );
}

/**
 * §3.6: "It is pinned to CPUs disjoint from the server under test."
 *
 * The driver runs in this process, so the pinning is this process's. taskset is
 * used rather than a cgroup because the harness must not create or modify a
 * cgroup on a host it is not allowed to reconfigure; the core list and the core
 * COUNT both go into the manifest, because §3.7 and §5.2 ask for the counts to
 * be stated and a cpuset string alone does not state one.
 */
export function pinToCpuset(cpuset = process.env.BENCH_CPUSET_DRIVER ?? '') {
  const cores = expandCpuset(cpuset);
  if (cores.length === 0) {
    return { pinned: false, cpuset, cores: 0, reason: 'BENCH_CPUSET_DRIVER is not set' };
  }
  try {
    execFileSync('taskset', ['-cp', cpuset, String(process.pid)], { stdio: 'ignore' });
    return { pinned: true, cpuset, cores: cores.length };
  } catch (err) {
    return { pinned: false, cpuset, cores: cores.length, reason: err.message };
  }
}

/**
 * "0,2,4-5" -> [0, 2, 4, 5].
 *
 * A single core has no "-", so its upper bound is `undefined` and not `NaN` —
 * `Number.isNaN(undefined)` is false, so an `isNaN` guard alone silently drops
 * every single-core entry and reports a smaller cpuset than the one in force.
 * That is exactly the shape of error §5.2's "core assignments stated" exists to
 * prevent, so the guard tests for both.
 */
export function expandCpuset(spec) {
  const cores = [];
  for (const part of String(spec).split(',')) {
    if (part.trim() === '') continue;
    const [lo, hi] = part.split('-').map(Number);
    if (!Number.isFinite(lo)) continue;
    const upper = Number.isFinite(hi) ? hi : lo;
    for (let i = lo; i <= upper; i++) cores.push(i);
  }
  return [...new Set(cores)].sort((a, b) => a - b);
}

export function sleep(ms) {
  return new Promise((resolve) => setTimeout(resolve, ms));
}
