'use client';

import {
  useActionState,
  useCallback,
  useEffect,
  useRef,
  useState,
  useTransition,
} from 'react';

import { useDashboardLive } from '@transport';
import {
  PER_PAGE_CHOICES,
  SEARCH_DEBOUNCE_MS,
  SERIES_POINTS,
  SPARK_POINTS,
  STATUS_FILTERS,
  barGeometry,
  deltaBand,
  formatDelta,
  pointGeometry,
  stamp,
  type DashView,
  type Kpi,
  type LogEntry,
  type PerPage,
  type Row,
  type StatusFilter,
} from '@/lib/core';
import {
  refreshPanelAction,
  setFilter,
  setPaused,
  setPerPage,
  setSearch,
  setSort,
} from '@/app/dashboard/actions';

/*
 * The whole interactive surface of the dashboard, and the only 'use client' on
 * the measured route.
 *
 * Why one boundary and not six: regions A..E and the control bar are six views
 * of ONE subscription. Splitting them into separate client components would
 * give each its own EventSource/WebSocket — six connections per tab where
 * gotth-live opens one — and D3 would then charge Next.js for an architecture
 * no competent team would ship. The static shell stays in the Server Component
 * that renders this one (§5.4: client boundaries as deep as the interactivity
 * requires, and no deeper than it permits).
 *
 * Everything the six controls do is a Server Action round trip, because §2.4
 * says the filter and the search filter region B server-side ON BOTH STACKS and
 * E4 says a feature that is server-authoritative in one is server-authoritative
 * in the other. There is no client-side filtering here, and its absence is the
 * measurement.
 */

export interface DashboardLiveProps {
  initial: DashView;
  sessionKey: string;
}

export default function DashboardLive({ initial, sessionKey }: DashboardLiveProps) {
  const { view, status, received } = useDashboardLive(sessionKey, initial);
  const [, startTransition] = useTransition();
  const [search, setSearchText] = useState(initial.controls.search);
  const readySignalled = useRef(false);
  const debounce = useRef<ReturnType<typeof setTimeout> | null>(null);

  /* Region E — a Server Action form (AS-3). Its content is returned by the
     action, not pushed: it is the one thing on this page that refreshes only
     when a person presses the button. */
  const [panel, refresh, refreshing] = useActionState(refreshPanelAction, initial.panel);

  useEffect(() => {
    document.documentElement.setAttribute('data-bench-status', status);
  }, [status]);

  /* §3.3 — hydration complete for the interactive region, channel open, first
     message applied. Set exactly once. */
  useEffect(() => {
    if (readySignalled.current) return;
    if (status !== 'live' || received === 0) return;
    readySignalled.current = true;
    const bench = (window as unknown as { __bench?: { ready: boolean } }).__bench;
    if (bench) bench.ready = true;
  }, [status, received]);

  /*
   * DSH-2 — "type one character into search | Region B row set matches
   * predicate after debounce".
   *
   * §2.4: "debounced 150 ms on both stacks with identical debounce
   * implementation semantics". Those semantics, stated so the gotth-live side
   * can be written to match rather than to approximate: TRAILING edge, timer
   * reset on every keystroke, one request fired SEARCH_DEBOUNCE_MS after the
   * last one, no leading call and no maximum wait. The input's own value is
   * local state and paints immediately — the debounce delays the server round
   * trip, never the character.
   */
  const onSearch = useCallback(
    (value: string) => {
      setSearchText(value);
      if (debounce.current !== null) clearTimeout(debounce.current);
      debounce.current = setTimeout(() => {
        debounce.current = null;
        startTransition(async () => {
          await setSearch(sessionKey, value);
        });
      }, SEARCH_DEBOUNCE_MS);
    },
    [sessionKey],
  );

  useEffect(() => {
    return () => {
      if (debounce.current !== null) clearTimeout(debounce.current);
    };
  }, []);

  const act = useCallback((run: () => Promise<void>) => {
    startTransition(async () => {
      await run();
    });
  }, []);

  const nextSort = view.controls.sort === 'off' ? 'asc' : view.controls.sort === 'asc' ? 'desc' : 'off';

  return (
    <>
      {/* ------------------------------------------------------ region A -- */}
      <section className="card kpis" data-bench-region="A">
        <h2>KPIs</h2>
        <ul className="kpi-strip" data-bench-id="kpis">
          {view.kpis.map((kpi, i) => (
            <KpiTile key={kpi.label} kpi={kpi} index={i} />
          ))}
        </ul>
      </section>

      {/* --------------------------------------------------- the controls -- */}
      <section className="card controls" data-bench-id="controls">
        <div className="control-group" role="group" aria-label="Status filter">
          {STATUS_FILTERS.map((f) => (
            <button
              key={f}
              type="button"
              className={view.controls.filter === f ? 'chip current' : 'chip'}
              data-bench-id={`filter-${f}`}
              aria-pressed={view.controls.filter === f}
              onClick={() => act(() => setFilter(sessionKey, f as StatusFilter))}
            >
              {f}
            </button>
          ))}
        </div>

        <input
          type="search"
          className="search"
          data-bench-id="search"
          placeholder="search name"
          value={search}
          onChange={(e) => onSearch(e.target.value)}
        />

        <button
          type="button"
          className="chip"
          data-bench-id="sort-m1"
          data-bench-value={view.controls.sort}
          onClick={() => act(() => setSort(sessionKey, nextSort))}
        >
          metric_1 {view.controls.sort}
        </button>

        <div className="control-group" role="group" aria-label="Rows per page">
          {PER_PAGE_CHOICES.map((n) => (
            <button
              key={n}
              type="button"
              className={view.controls.perPage === n ? 'chip current' : 'chip'}
              data-bench-id={`per-page-${n}`}
              aria-pressed={view.controls.perPage === n}
              onClick={() => act(() => setPerPage(sessionKey, n as PerPage))}
            >
              {n}
            </button>
          ))}
        </div>

        <button
          type="button"
          className="chip"
          data-bench-id="pause"
          data-bench-value={view.controls.paused ? 'paused' : 'running'}
          onClick={() => act(() => setPaused(sessionKey, !view.controls.paused))}
        >
          {view.controls.paused ? 'Resume' : 'Pause'}
        </button>
      </section>

      {/* ------------------------------------------------------ region B -- */}
      <section className="card table" data-bench-region="B">
        <h2>
          Live table
          <span className="count" data-bench-id="row-count" data-bench-value={view.rows.length}>
            {view.rows.length} of {view.total}
          </span>
          <span className="tick" data-bench-id="tick" data-bench-value={view.tick}>
            tick {view.tick}
          </span>
        </h2>
        <table>
          <thead>
            <tr>
              <th>id</th>
              <th>name</th>
              <th>status</th>
              <th>metric_1</th>
              <th>metric_2</th>
              <th>metric_3</th>
              <th>updated</th>
              <th>badge</th>
            </tr>
          </thead>
          <tbody data-bench-id="rows">
            {view.rows.map((row) => (
              <TableRow key={row.id} row={row} />
            ))}
          </tbody>
        </table>
      </section>

      {/* ------------------------------------------------------ region C -- */}
      <section className="card series" data-bench-region="C">
        <h2>Time series</h2>
        <svg
          className="chart"
          viewBox={`0 0 ${SERIES_POINTS} 100`}
          preserveAspectRatio="none"
          data-bench-id="series"
          data-bench-value={view.series[0][view.series[0].length - 1] ?? 0}
          aria-hidden="true"
        >
          {view.series.map((points, s) =>
            points.map((_, i) => {
              const { cx, cy } = pointGeometry(points, i);
              return <circle key={`${s}-${i}`} className={`p s${s}`} cx={cx} cy={cy} r={0.9} />;
            }),
          )}
        </svg>
      </section>

      {/* ------------------------------------------------------ region D -- */}
      <section className="card log" data-bench-region="D">
        <h2>
          Event log
          <span className="count" data-bench-id="log-count" data-bench-value={view.log.length}>
            {view.log.length}
          </span>
        </h2>
        <ol className="entries" data-bench-id="log">
          {view.log.map((entry) => (
            <LogRow key={entry.seq} entry={entry} />
          ))}
        </ol>
      </section>

      {/* ------------------------------------------------------ region E -- */}
      <section className="card panel" data-bench-region="E">
        <h2>Operator panel</h2>
        <form action={refresh}>
          <input type="hidden" name="k" value={sessionKey} />
          <button type="submit" data-bench-id="refresh" disabled={refreshing}>
            Refresh
          </button>
        </form>
        <p className="panel-text" data-bench-id="panel" data-bench-value={panel.seq}>
          {panel.text}
        </p>
      </section>
    </>
  );
}

/*
 * §2.4 sizes region A at "8 x ~70 nodes = 560", which is only reachable if each
 * sparkline point is its own element. A single <polyline> would be one node and
 * the region would be an order of magnitude cheaper than the spec sizes it, so
 * the bars are individual elements — on both stacks — and the document's SVG
 * budget (<= 800 nodes) is 8 x 60 here plus 2 x 120 in region C.
 */
function KpiTile({ kpi, index }: { kpi: Kpi; index: number }) {
  return (
    <li className="kpi" data-bench-id={`kpi-${index}`}>
      <span className="kpi-label">{kpi.label}</span>
      <span className="kpi-value" data-bench-id={`kpi-value-${index}`} data-bench-value={kpi.value}>
        {kpi.value}
      </span>
      <span className={`kpi-delta ${deltaBand(kpi.delta)}`}>{formatDelta(kpi.delta)}</span>
      <svg className="spark" viewBox={`0 0 ${SPARK_POINTS} 20`} preserveAspectRatio="none" aria-hidden="true">
        {kpi.spark.map((_, i) => {
          const { x, y, h } = barGeometry(kpi.spark, i);
          return <rect key={i} x={x} y={y} width={0.8} height={h} />;
        })}
      </svg>
    </li>
  );
}

/* Eight columns exactly as §2.4 lists them, plus the badge span: ten nodes per
   row, so 200 rows is the spec's 2000. */
function TableRow({ row }: { row: Row }) {
  return (
    <tr data-bench-id="row" data-bench-value={row.id}>
      <td>{row.id}</td>
      <td>{row.name}</td>
      <td>{row.status}</td>
      <td>{row.m1}</td>
      <td>{row.m2}</td>
      <td>{row.m3}</td>
      <td>{stamp(row.ts)}</td>
      <td>
        <span className={`badge ${row.status}`}>{row.status}</span>
      </td>
    </tr>
  );
}

function LogRow({ entry }: { entry: LogEntry }) {
  return (
    <li className="entry" data-bench-id="log-entry" data-bench-value={entry.seq}>
      <span className="at">{stamp(entry.ts)}</span>
      <span className="text">{entry.text}</span>
    </li>
  );
}
