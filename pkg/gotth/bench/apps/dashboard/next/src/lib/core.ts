/*
 * The live dashboard's shared vocabulary and its pure derivations (§2.4).
 *
 * §2.4 is the demanding app and the source of the headline push-latency number,
 * the heavy memory workload and the wire-byte comparison. Every bound it states
 * is encoded here as a constant rather than left implicit, so a change that
 * breaks E5 ("bounded DOM") breaks a named constant a reviewer can find.
 */

/** §2.4 region A: "8 tiles: label, value, delta %, inline SVG sparkline of 60 points". */
export const KPI_COUNT = 8;
export const SPARK_POINTS = 60;

/** §2.4 region B: "200 rows x 8 cols". */
export const ROW_COUNT = 200;

/** §2.4 region C: "2 series x 120 points, shift-one-point". */
export const SERIES_POINTS = 120;

/** §2.4 region D: "append-only, capped 50 entries". */
export const LOG_CAP = 50;

/** §2.4 controls: "Text search ... debounced 150 ms on both stacks". */
export const SEARCH_DEBOUNCE_MS = 150;

export type Status = 'ok' | 'warn' | 'error';
export const STATUSES: readonly Status[] = ['ok', 'warn', 'error'];

export type StatusFilter = 'all' | Status;
export const STATUS_FILTERS: readonly StatusFilter[] = ['all', 'ok', 'warn', 'error'];

export type SortMode = 'off' | 'asc' | 'desc';

/** §2.4 controls: "Rows per page | select 50 / 100 / 200". */
export const PER_PAGE_CHOICES = [50, 100, 200] as const;
export type PerPage = (typeof PER_PAGE_CHOICES)[number];

/** One row of region B. Eight columns, exactly as §2.4 lists them. */
export interface Row {
  id: number;
  name: string;
  status: Status;
  m1: number;
  m2: number;
  m3: number;
  /** Milliseconds since T0, so the rendered clock is a server number. */
  ts: number;
}

export interface Kpi {
  label: string;
  value: number;
  /** Percent change against the previous 1 Hz sample, one decimal place. */
  delta: number;
  /** SPARK_POINTS values, oldest first. */
  spark: number[];
}

export interface LogEntry {
  seq: number;
  text: string;
  ts: number;
}

/** The six controls of §2.4, all server-authoritative (E4). */
export interface Controls {
  filter: StatusFilter;
  search: string;
  sort: SortMode;
  perPage: PerPage;
  paused: boolean;
}

export const DEFAULT_CONTROLS: Controls = {
  filter: 'all',
  search: '',
  sort: 'off',
  /*
   * 200 by default, which is the concurrency §2.4's DOM bound is stated
   * against ("200 x 10 = 2000") and the state DSH-7's passive push row is
   * measured in. DSH-4 drives 50 -> 200, so the driver sets 50 first; making
   * 50 the default instead would mean every other interaction was measured on
   * a quarter of the table the spec sizes.
   */
  perPage: 200,
  paused: false,
};

/** Region E — "a small panel refreshed by an explicit button press". */
export interface Panel {
  text: string;
  /** Refresh count, so a repaint is provable rather than plausible. */
  seq: number;
  ts: number;
}

/** Everything one session renders. The SSR payload and the poll payload. */
export interface DashView {
  version: number;
  /** The fixture tick this view reflects (§2.5, §3.2). */
  tick: number;
  nowMs: number;
  kpis: Kpi[];
  series: [number[], number[]];
  /** The visible page: already filtered, sorted and paged ON THE SERVER. */
  rows: Row[];
  /** Rows matching the filter before paging, for the "n of m" line. */
  total: number;
  log: LogEntry[];
  controls: Controls;
  panel: Panel;
}

/**
 * An incremental update (§4.6 is a wire-byte comparison, so this matters).
 *
 * A full DashView is ~14 KB at perPage 200. Pushing one twice a second would
 * be 28 KB/s/session of which ~90 % is unchanged bytes, and the wire-byte row
 * would then be measuring an author's choice rather than a framework's. So the
 * push channel carries patches: region A and C are small enough to send whole
 * at their 1 Hz rate, region B sends the 20 changed rows plus an id list only
 * when the visible ORDER changes, and region D appends.
 *
 * This is what a perf-minded Next.js team ships, and it is the like-for-like
 * counterpart to gotth-live's per-fragment patches.
 */
export interface Patch {
  version: number;
  tick: number;
  nowMs: number;
  kpis?: Kpi[];
  series?: [number[], number[]];
  /** New visible order, by row id. Absent when the order did not change. */
  rowIds?: number[];
  /** Full data for rows that changed or newly became visible. */
  rowUpd?: Row[];
  total?: number;
  logAdd?: LogEntry[];
  controls?: Controls;
  panel?: Panel;
  /** A resume sends the whole view rather than a patch onto a stale one. */
  full?: DashView;
}

/* ------------------------------------------------------------ derived ---- */

/** F: the delta badge's class, so a repaint is not a single text node. */
export function deltaBand(delta: number): 'up' | 'down' | 'flat' {
  if (delta > 0.05) return 'up';
  if (delta < -0.05) return 'down';
  return 'flat';
}

export function formatDelta(delta: number): string {
  const sign = delta > 0 ? '+' : '';
  return `${sign}${delta.toFixed(1)}%`;
}

/**
 * The absolute timestamp a row and a log entry render.
 *
 * Formatted from milliseconds-since-T0 with explicit arithmetic rather than
 * toLocaleTimeString, because the container's locale and timezone are not part
 * of the equivalence contract.
 */
export function stamp(ms: number): string {
  const total = Math.floor(ms / 1000);
  const m = Math.floor(total / 60) % 60;
  const s = total % 60;
  const cs = Math.floor((ms % 1000) / 10);
  return `${pad(m)}:${pad(s)}.${pad(cs)}`;
}

function pad(n: number): string {
  return n < 10 ? `0${n}` : String(n);
}

/**
 * Sparkline geometry: SPARK_POINTS bars, one element each.
 *
 * §2.4 sizes region A at "8 x ~70 nodes = 560", which is only reachable if each
 * sparkline point is its own element — a single <polyline> would be one node
 * and the region would be an order of magnitude cheaper than the spec sizes it.
 * So the bars are individual elements on both stacks, and the SVG node budget
 * (<= 800 for the whole document) is 8 x 60 + 2 x 120 = 720.
 */
export function barGeometry(values: number[], index: number): { x: number; y: number; h: number } {
  const v = values[index] ?? 0;
  const max = 1000;
  const h = Math.max(1, Math.round((v / max) * 20));
  return { x: index, y: 20 - h, h };
}

/** Region C point geometry: 2 series x SERIES_POINTS, one element per point. */
export function pointGeometry(values: number[], index: number): { cx: number; cy: number } {
  const v = values[index] ?? 0;
  return { cx: index, cy: 100 - Math.round((v / 1000) * 100) };
}

/** The filter/search/sort/page pipeline, applied SERVER-SIDE on both stacks. */
export function visible(rows: Row[], controls: Controls): { page: Row[]; total: number } {
  let out = rows;

  if (controls.filter !== 'all') {
    out = out.filter((r) => r.status === controls.filter);
  }
  if (controls.search !== '') {
    const needle = controls.search.toLowerCase();
    out = out.filter((r) => r.name.toLowerCase().includes(needle));
  }

  const total = out.length;

  if (controls.sort !== 'off') {
    /*
     * Copy before sorting: `rows` is the authority's own array and sorting it
     * in place would reorder every other session's table as a side effect of
     * one session pressing a button. "stable sort by id unless user sorts"
     * (§2.4) is the base order, so the tie-break is id.
     */
    const dir = controls.sort === 'asc' ? 1 : -1;
    out = [...out].sort((a, b) => (a.m1 - b.m1) * dir || a.id - b.id);
  }

  return { page: out.slice(0, controls.perPage), total };
}
