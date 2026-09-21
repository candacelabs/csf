import { LOG_CAP, type DashView, type Patch, type Row } from './core';

/*
 * Applying a push patch to the view the page is rendering.
 *
 * This is the only piece of state-reconciliation logic in the dashboard's
 * client, and it is written once here rather than three times in the three
 * transports — the transports differ in how bytes arrive, never in what the
 * bytes mean. A per-transport copy would let the sse and ws variants drift into
 * measuring different applications.
 *
 * It is deliberately total and deliberately dull: no merging heuristics, no
 * "if it looks stale, refetch". A patch older than the view it lands on is
 * dropped by the caller on `version`, and anything this function cannot resolve
 * (an id in rowIds whose fields were never sent) is dropped rather than
 * rendered blank, because a blank row would satisfy a row-count predicate while
 * showing nothing — a paint the harness would count and a person would not.
 */
export function applyPatch(view: DashView, patch: Patch): DashView {
  if (patch.full) return patch.full;

  const next: DashView = {
    ...view,
    version: patch.version,
    tick: patch.tick,
    nowMs: patch.nowMs,
  };

  if (patch.kpis) next.kpis = patch.kpis;
  if (patch.series) next.series = patch.series;
  if (patch.controls) next.controls = patch.controls;
  if (patch.panel) next.panel = patch.panel;
  if (typeof patch.total === 'number') next.total = patch.total;

  if (patch.rowUpd || patch.rowIds) {
    const byId = new Map<number, Row>();
    for (const row of view.rows) byId.set(row.id, row);
    if (patch.rowUpd) {
      for (const row of patch.rowUpd) byId.set(row.id, row);
    }
    const order = patch.rowIds ?? view.rows.map((r) => r.id);
    const rows: Row[] = [];
    for (const id of order) {
      const row = byId.get(id);
      if (row) rows.push(row);
    }
    next.rows = rows;
  }

  if (patch.logAdd && patch.logAdd.length > 0) {
    const log = view.log.concat(patch.logAdd);
    next.log = log.length > LOG_CAP ? log.slice(log.length - LOG_CAP) : log;
  }

  return next;
}
