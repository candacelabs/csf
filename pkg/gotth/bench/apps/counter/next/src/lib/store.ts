import 'server-only';

import { applyOp, EMPTY_SNAPSHOT, type CounterOp, type Snapshot } from './core';

/*
 * The server-side authority.
 *
 * F-CTR-1 and F-CTR-4: the integer lives here and nowhere else, so a reload
 * throws the browser's copy away and rebuilds it from this. F-CTR-5: a change
 * made by one tab is broadcast to every subscriber, which is what forces this
 * app to own a real push channel rather than a useState.
 *
 * ---------------------------------------------------------------------------
 * Why globalThis rather than a module-level `const`
 *
 * Next.js bundles route handlers and Server Actions into separate server
 * chunks. A module-level singleton can therefore be instantiated more than
 * once — the Server Action would increment one store while the SSE route
 * streamed another, and the bug would look like "the push channel is broken".
 * Keying the instance off a global Symbol is the standard self-hosted Next
 * idiom for this (it is what the Prisma/Redis client recipes in the Next docs
 * do) and is not a benchmark-specific trick.
 *
 * ---------------------------------------------------------------------------
 * Rooms, and the one place this app reads more literally than the spec
 *
 * §2.1 F-CTR-1 says the counter is "server state, per session", while the
 * gotth-live counter this app must match (examples/counter) keeps ONE counter
 * shared by every connection — its README's "2 tabs sharing this counter" is
 * that sharing made visible. Under E1/E3/E4 the app that gets measured is the
 * app that exists, so the default here is `global`, matching Go. The `session`
 * scope is implemented too, so that if QA-2 amends §2.1 toward the literal
 * reading the Next.js side is not the thing blocking it. See
 * docs/OPERATOR-QUESTIONS.md Q-BENCH-1.
 */

export type CounterScope = 'global' | 'session';

export const SCOPE: CounterScope =
  process.env.BENCH_COUNTER_SCOPE === 'session' ? 'session' : 'global';

/** A subscriber is one tab holding the app's push channel (§3.4). */
type Listener = (snapshot: Snapshot) => void;

interface Room {
  value: number;
  version: number;
  changedAtMs: number;
  changedBy: string;
  /** Tabs holding a push channel served by THIS process (SSE variant). */
  local: Map<string, Listener>;
  /**
   * Subscribers that receive every snapshot but are not tabs.
   *
   * Exactly one thing is ever in here: the WebSocket sidecar's upstream
   * consumer. It has to see the same stream a browser sees, and it must not be
   * counted as a browser, or the ws variant would report one session more than
   * exists and F-CTR-5's tab count would be visibly wrong on the page.
   */
  relays: Set<Listener>;
  /**
   * Tabs holding a push channel served by the WebSocket sidecar, as most
   * recently reported by it (§5.4's secondary variant runs the ws server as a
   * second process in the same container, so this process cannot count them).
   */
  external: number;
  /** Polling clients, with a TTL: a poller holds no connection to count. */
  polling: Map<string, number>;
}

interface StoreShape {
  rooms: Map<string, Room>;
}

const KEY = Symbol.for('gotth-live-bench.counter.store.v1');

function shape(): StoreShape {
  const g = globalThis as unknown as Record<symbol, StoreShape | undefined>;
  let s = g[KEY];
  if (!s) {
    s = { rooms: new Map() };
    g[KEY] = s;
  }
  return s;
}

function room(id: string): Room {
  const s = shape();
  let r = s.rooms.get(id);
  if (!r) {
    r = {
      value: 0,
      version: 0,
      changedAtMs: 0,
      changedBy: '',
      local: new Map(),
      relays: new Set(),
      external: 0,
      polling: new Map(),
    };
    s.rooms.set(id, r);
  }
  return r;
}

/** Drops polling clients that have not been seen for two intervals. */
function sweep(r: Room, now: number): void {
  for (const [tab, expiry] of r.polling) {
    if (expiry <= now) r.polling.delete(tab);
  }
}

function tabs(r: Room, now: number): number {
  sweep(r, now);
  return r.local.size + r.external + r.polling.size;
}

function snapshotOf(r: Room, now: number): Snapshot {
  return {
    value: r.value,
    version: r.version,
    tabs: tabs(r, now),
    changedAtMs: r.changedAtMs,
    changedBy: r.changedBy,
    nowMs: now,
  };
}

function broadcast(id: string): void {
  const r = room(id);
  const now = Date.now();
  const snap = snapshotOf(r, now);
  for (const listener of r.local.values()) {
    listener(snap);
  }
  for (const listener of r.relays) {
    listener(snap);
  }
}

export function snapshot(id: string): Snapshot {
  if (!id) return { ...EMPTY_SNAPSHOT, nowMs: Date.now() };
  return snapshotOf(room(id), Date.now());
}

/**
 * Apply an operation and tell every subscriber.
 *
 * The version counter exists for the same reason it does in Go: a snapshot
 * older than the one a client already holds is dropped by the client, so
 * out-of-order delivery repairs itself instead of leaving a tab permanently
 * wrong.
 */
export function apply(id: string, op: CounterOp, by: string): Snapshot {
  const r = room(id);
  r.value = applyOp(r.value, op);
  r.version += 1;
  r.changedAtMs = Date.now();
  r.changedBy = by;
  broadcast(id);
  return snapshotOf(r, r.changedAtMs);
}

/** Registers a push subscriber (SSE). Returns the unsubscribe. */
export function subscribe(id: string, tab: string, listener: Listener): () => void {
  const r = room(id);
  r.local.set(tab, listener);
  broadcast(id); // F-CTR-5: every tab repaints when a tab merely connects.
  return () => {
    if (r.local.get(tab) === listener) {
      r.local.delete(tab);
      broadcast(id);
    }
  };
}

/**
 * Registers the WebSocket sidecar's upstream consumer.
 *
 * Same snapshots, no effect on the tab count, and no broadcast on
 * subscribe — a relay attaching is not a tab arriving and must not repaint
 * anybody's page.
 */
export function subscribeRelay(id: string, listener: Listener): () => void {
  const r = room(id);
  r.relays.add(listener);
  return () => {
    r.relays.delete(listener);
  };
}

/** The WebSocket sidecar reporting how many browsers it is holding. */
export function setExternalTabs(id: string, n: number): void {
  const r = room(id);
  if (r.external === n) return;
  r.external = n;
  broadcast(id);
}

/** A polling client checking in. `ttlMs` should be ~2x the poll interval. */
export function touchPoller(id: string, tab: string, ttlMs: number): void {
  const r = room(id);
  r.polling.set(tab, Date.now() + ttlMs);
}

/** Test/inspection helper; never called on a measured path. */
export function reset(id: string): void {
  shape().rooms.delete(id);
}
