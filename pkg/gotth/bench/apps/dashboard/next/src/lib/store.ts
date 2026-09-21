import 'server-only';

import {
  DEFAULT_CONTROLS,
  KPI_COUNT,
  LOG_CAP,
  SERIES_POINTS,
  SPARK_POINTS,
  visible,
  type Controls,
  type DashView,
  type Kpi,
  type LogEntry,
  type Panel,
  type Patch,
  type Row,
  type Status,
} from './core';
import { Replay, TICK_MS, load, type FixtureTick } from './fixture';

/*
 * The dashboard's authority: the five regions' state, the per-session control
 * state the regions are rendered through, and the fixture replay that drives
 * all of it (§2.4, §2.5).
 *
 * Keyed off a global Symbol for the same reason the counter's and chat's stores
 * are: Next bundles route handlers and Server Actions into separate server
 * chunks, so a module-level singleton can be instantiated more than once and
 * the bug looks like "the push channel is broken".
 */

type DashEvent =
  | { k: 'kpi'; v: number[] }
  | { k: 'series'; v: number[] }
  | { k: 'rows'; r: Array<{ id: number; status: Status; m1: number; m2: number; m3: number; ts: number }> }
  | { k: 'log'; seq: number; text: string };

interface FixtureBase {
  kpiLabels: string[];
  rows: Row[];
  kpi: number[];
  spark: number[][];
  series: number[][];
}

/** One subscriber: a browser tab holding this app's push channel (§3.4). */
interface Session {
  id: string;
  controls: Controls;
  panel: Panel;
  listener: ((patch: Patch) => void) | null;
  /** Ids currently rendered by this session, so a patch can be incremental. */
  sentIds: number[];
  /** The last log seq this session has been told about. */
  logMark: number;
  pollExpiry: number;
  createdAtMs: number;
}

interface Shape {
  labels: string[];
  rows: Row[];
  byId: Map<number, Row>;
  kpiValues: number[];
  kpiPrev: number[];
  spark: number[][];
  series: [number[], number[]];
  log: LogEntry[];
  version: number;
  tick: number;
  sessions: Map<string, Session>;
  replay: Replay<DashEvent> | null;
  sha256: string;
}

/** See chat's store for why this exists; D4 mints a session per request. */
const SESSION_GRACE_MS = 30_000;

const KEY = Symbol.for('gotth-live-bench.dashboard.store.v1');

function shape(): Shape {
  const g = globalThis as unknown as Record<symbol, Shape | undefined>;
  let s = g[KEY];
  if (s) return s;

  const fixture = load<FixtureBase, DashEvent>('dashboard');
  const rows = fixture.base.rows.map((r) => ({ ...r }));

  s = {
    labels: fixture.base.kpiLabels,
    rows,
    byId: new Map(rows.map((r) => [r.id, r])),
    kpiValues: fixture.base.kpi.slice(),
    kpiPrev: fixture.base.kpi.slice(),
    spark: fixture.base.spark.map((a) => a.slice()),
    series: [fixture.base.series[0].slice(), fixture.base.series[1].slice()],
    log: [],
    version: 0,
    tick: 0,
    sessions: new Map(),
    replay: null,
    sha256: fixture.sha256,
  };
  g[KEY] = s;

  s.replay = new Replay<DashEvent>(fixture.ticks, (tick) => applyTick(s!, tick), TICK_MS);
  return s;
}

/* ------------------------------------------------------------- replay ---- */

function applyTick(s: Shape, tick: FixtureTick<DashEvent>): void {
  s.tick = tick.n;
  const now = tick.n * TICK_MS;

  let kpiChanged = false;
  let seriesChanged = false;
  const rowsChanged = new Set<number>();
  const logAdded: LogEntry[] = [];

  for (const event of tick.e) {
    switch (event.k) {
      case 'kpi': {
        /* Region A, 1 Hz. The previous sample is kept so the delta % is a
           server derivation and not a client one (E4). */
        s.kpiPrev = s.kpiValues;
        s.kpiValues = event.v.slice(0, KPI_COUNT);
        for (let i = 0; i < KPI_COUNT; i++) {
          s.spark[i].push(s.kpiValues[i]);
          if (s.spark[i].length > SPARK_POINTS) s.spark[i].shift();
        }
        kpiChanged = true;
        break;
      }
      case 'series': {
        /* Region C, "shift-one-point": 2 new points in, 2 dropped. */
        for (let i = 0; i < 2; i++) {
          s.series[i].push(event.v[i]);
          if (s.series[i].length > SERIES_POINTS) s.series[i].shift();
        }
        seriesChanged = true;
        break;
      }
      case 'rows': {
        /* Region B, 2 Hz, 20 rows changed per tick (10 % churn). */
        for (const upd of event.r) {
          const row = s.byId.get(upd.id);
          if (!row) continue;
          row.status = upd.status;
          row.m1 = upd.m1;
          row.m2 = upd.m2;
          row.m3 = upd.m3;
          row.ts = upd.ts;
          rowsChanged.add(upd.id);
        }
        break;
      }
      case 'log': {
        /* Region D, 5 Hz, append-only, capped 50. */
        const entry: LogEntry = { seq: event.seq, text: event.text, ts: now };
        s.log.push(entry);
        if (s.log.length > LOG_CAP) s.log.shift();
        logAdded.push(entry);
        break;
      }
    }
  }

  if (tick.n % 10 === 0) sweepSessions(s, Date.now());

  s.version += 1;

  for (const session of s.sessions.values()) {
    if (!session.listener) continue;
    /*
     * DSH-5 — "Pause / resume | halts application of live updates
     * (client-visible), stream continues server-side".
     *
     * Server-authoritative, matching E4 and matching what the gotth-live
     * dashboard does: the feed keeps running for every other session, and a
     * resume shows the CURRENT tick rather than replaying what was missed. A
     * client-side pause would make DSH-5 a local paint on this stack and a
     * round trip on the other, which is the category error §2.2 exists to keep
     * out of the tables.
     */
    if (session.controls.paused) continue;
    const patch = patchFor(s, session, { kpiChanged, seriesChanged, rowsChanged, logAdded });
    if (patch) session.listener(patch);
  }
}

function sweepSessions(s: Shape, now: number): void {
  for (const [id, session] of s.sessions) {
    if (session.listener !== null) continue;
    if (session.pollExpiry > now) continue;
    if (now - Math.max(session.createdAtMs, session.pollExpiry) < SESSION_GRACE_MS) continue;
    s.sessions.delete(id);
  }
}

/* -------------------------------------------------------------- views ---- */

function kpis(s: Shape): Kpi[] {
  const out: Kpi[] = [];
  for (let i = 0; i < KPI_COUNT; i++) {
    const prev = s.kpiPrev[i] || 1;
    out.push({
      label: s.labels[i],
      value: s.kpiValues[i],
      delta: ((s.kpiValues[i] - prev) / prev) * 100,
      spark: s.spark[i],
    });
  }
  return out;
}

export function viewFor(session: Session, s: Shape = shape()): DashView {
  const { page, total } = visible(s.rows, session.controls);
  session.sentIds = page.map((r) => r.id);
  session.logMark = s.log.length > 0 ? s.log[s.log.length - 1].seq : 0;
  return {
    version: s.version,
    tick: s.tick,
    nowMs: Date.now(),
    kpis: kpis(s),
    series: [s.series[0], s.series[1]],
    rows: page,
    total,
    log: s.log,
    controls: { ...session.controls },
    panel: session.panel,
  };
}

interface TickDelta {
  kpiChanged: boolean;
  seriesChanged: boolean;
  rowsChanged: Set<number>;
  logAdded: LogEntry[];
}

/**
 * The incremental push for one session (see core.ts's Patch for why).
 *
 * Region B is the interesting half. The visible order is recomputed every tick
 * because churn can move a row in or out of a filter, and the id list is sent
 * only when it actually changed. Rows are sent whole when they changed OR when
 * they newly became visible — a client cannot render an id it has never been
 * given the fields for, and that is the bug an id-list-only patch would have.
 */
function patchFor(s: Shape, session: Session, delta: TickDelta): Patch | null {
  const { page, total } = visible(s.rows, session.controls);
  const ids = page.map((r) => r.id);

  const orderChanged =
    ids.length !== session.sentIds.length || ids.some((id, i) => id !== session.sentIds[i]);
  const known = new Set(session.sentIds);

  const rowUpd: Row[] = [];
  for (const row of page) {
    if (delta.rowsChanged.has(row.id) || !known.has(row.id)) rowUpd.push({ ...row });
  }

  const logAdd = delta.logAdded;

  if (!delta.kpiChanged && !delta.seriesChanged && !orderChanged && rowUpd.length === 0 && logAdd.length === 0) {
    return null;
  }

  session.sentIds = ids;
  if (logAdd.length > 0) session.logMark = logAdd[logAdd.length - 1].seq;

  const patch: Patch = { version: s.version, tick: s.tick, nowMs: Date.now() };
  if (delta.kpiChanged) patch.kpis = kpis(s);
  if (delta.seriesChanged) patch.series = [s.series[0], s.series[1]];
  if (orderChanged) {
    patch.rowIds = ids;
    patch.total = total;
  }
  if (rowUpd.length > 0) patch.rowUpd = rowUpd;
  if (logAdd.length > 0) patch.logAdd = logAdd;
  return patch;
}

/* ---------------------------------------------------------- lifecycle ---- */

function ensure(s: Shape, id: string): Session {
  let session = s.sessions.get(id);
  if (session) return session;
  session = {
    id,
    controls: { ...DEFAULT_CONTROLS },
    panel: { text: 'Press refresh to load the operator panel.', seq: 0, ts: 0 },
    listener: null,
    sentIds: [],
    logMark: 0,
    pollExpiry: 0,
    createdAtMs: Date.now(),
  };
  s.sessions.set(id, session);
  return session;
}

export function snapshot(id: string): DashView {
  const s = shape();
  return viewFor(ensure(s, id), s);
}

export function subscribe(id: string, listener: (patch: Patch) => void): () => void {
  const s = shape();
  const session = ensure(s, id);
  session.listener = listener;
  /* The first frame is the whole view, which is §3.3's "first message
     applied": a client that connects between two ticks is correct at once. */
  listener({ version: s.version, tick: s.tick, nowMs: Date.now(), full: viewFor(session, s) });
  return () => {
    if (session.listener === listener) session.listener = null;
    s.sessions.delete(id);
  };
}

export function touchPoller(id: string, ttlMs: number): void {
  const s = shape();
  ensure(s, id).pollExpiry = Date.now() + ttlMs;
}

/* ----------------------------------------------------------- controls ---- */

/**
 * Every control is a server round trip on both stacks (§2.4: the filter and
 * the search "filter Region B server-side on both stacks").
 *
 * The whole view is pushed back rather than a patch, because a control change
 * can replace the entire visible set and a patch onto a set the client is about
 * to stop rendering is more bytes, not fewer.
 */
export function setControls(id: string, next: Partial<Controls>): void {
  const s = shape();
  const session = ensure(s, id);
  const wasPaused = session.controls.paused;
  session.controls = { ...session.controls, ...next };

  if (!session.listener) return;

  /* A resume sends the current state, not the backlog: "resuming shows the
     current reading rather than replaying what you missed". */
  if (wasPaused && !session.controls.paused) {
    session.sentIds = [];
    session.listener({
      version: s.version,
      tick: s.tick,
      nowMs: Date.now(),
      full: viewFor(session, s),
    });
    return;
  }

  if (session.controls.paused) {
    /* The pause itself is visible immediately; nothing after it is. */
    session.listener({
      version: s.version,
      tick: s.tick,
      nowMs: Date.now(),
      controls: { ...session.controls },
    });
    return;
  }

  session.sentIds = [];
  session.listener({
    version: s.version,
    tick: s.tick,
    nowMs: Date.now(),
    full: viewFor(session, s),
  });
}

/** Region E — the manual panel, refreshed by an explicit button press. */
export function refreshPanel(id: string): Panel {
  const s = shape();
  const session = ensure(s, id);
  session.panel = {
    seq: session.panel.seq + 1,
    text: `${s.rows.filter((r) => r.status === 'error').length} rows in error at tick ${s.tick}`,
    ts: s.tick * TICK_MS,
  };
  return session.panel;
}

export function controlsOf(id: string): Controls {
  return { ...ensure(shape(), id).controls };
}

/* -------------------------------------------------------------- bench ---- */

/** §3.2's control channel: T0 and the replay position, for the skew estimate. */
export function clock(): { t0Ms: number; nowMs: number; tick: number; tickMs: number } {
  const s = shape();
  return {
    t0Ms: s.replay?.t0Ms ?? 0,
    nowMs: Date.now(),
    tick: s.replay?.tickNow() ?? 0,
    tickMs: TICK_MS,
  };
}

export function fixtureSha(): string {
  return shape().sha256;
}
