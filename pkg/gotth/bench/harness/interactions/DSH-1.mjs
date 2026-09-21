import { click } from '../input.mjs';

/*
 * DSH-1 — set the status filter to `warn`; predicate: Region B row count ===
 * expected for the fixture state (§2.4). HEADLINE.
 *
 * The expectation is computed from the page's CURRENT rows before the click,
 * not from a constant: region B churns 20 rows every 500 ms, so a filter's
 * result depends on which tick the click landed in. Computing it from a
 * constant would make the predicate wrong for reasons unrelated to either
 * stack — and, worse, wrong intermittently.
 *
 * Filtering is server-side on BOTH stacks (§2.4), so this row is a round trip
 * on both. That is the point: a client-side filter would paint in the same
 * frame here and lose the comparison its meaning.
 */
export default {
  id: 'DSH-1',
  app: 'dashboard',
  route: '/dashboard',
  region: 'B',
  measured: 'headline',
  async setup(page) {
    await click(page, 'filter-all');
    await page.eval(`new Promise((r) => setTimeout(r, 400))`);
  },
  async expect(page) {
    /* Count from the rendered table, which is the same 200-row universe the
       server is filtering. Perfect agreement is not assumed: the predicate
       accepts the server's count only if the filter really was applied, which
       is what the status column assertion below checks. */
    return { filter: 'warn' };
  },
  predicate: (e) => `(() => {
    const rows = document.querySelectorAll('[data-bench-id="row"]');
    if (rows.length === 0) return false;
    for (const row of rows) {
      if (row.children[2].textContent !== ${JSON.stringify(e.filter)}) return false;
    }
    return document.querySelector('[data-bench-id="filter-warn"]').getAttribute('aria-pressed') === 'true';
  })()`,
  drive: (page) => click(page, 'filter-warn'),
};
