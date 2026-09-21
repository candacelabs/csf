import { click } from '../input.mjs';

/*
 * DSH-3 — toggle sort on metric_1; predicate: first row's data-bench-value ===
 * expected (§2.4).
 *
 * "expected" is not precomputable: the sort happens on the server against rows
 * that churn at 2 Hz, so the id that ends up first depends on the tick. The
 * predicate therefore asserts the ORDERING PROPERTY the sort claims — metric_1
 * ascending, ties broken by id, which is exactly the comparator the server
 * uses — plus the control's own state. That is a stronger assertion than a
 * remembered id and it cannot go stale.
 */
export default {
  id: 'DSH-3',
  app: 'dashboard',
  route: '/dashboard',
  region: 'B',
  measured: 'yes',
  async setup(page) {
    const mode = await page.eval(
      `document.querySelector('[data-bench-id="sort-m1"]').getAttribute('data-bench-value')`,
    );
    if (mode === 'off') return;
    /* Cycle back to off so the measured click is always off -> asc. */
    for (let i = 0; i < 3; i++) {
      const now = await page.eval(
        `document.querySelector('[data-bench-id="sort-m1"]').getAttribute('data-bench-value')`,
      );
      if (now === 'off') break;
      await click(page, 'sort-m1');
      await page.eval(`new Promise((r) => setTimeout(r, 300))`);
    }
  },
  expect: async () => ({ mode: 'asc' }),
  predicate: (e) => `(() => {
    const control = document.querySelector('[data-bench-id="sort-m1"]');
    if (control.getAttribute('data-bench-value') !== ${JSON.stringify(e.mode)}) return false;
    const rows = [...document.querySelectorAll('[data-bench-id="row"]')];
    if (rows.length < 2) return false;
    for (let i = 1; i < rows.length; i++) {
      const a = Number(rows[i - 1].children[3].textContent);
      const b = Number(rows[i].children[3].textContent);
      if (a > b) return false;
      if (a === b && Number(rows[i - 1].children[0].textContent) > Number(rows[i].children[0].textContent)) return false;
    }
    return true;
  })()`,
  drive: (page) => click(page, 'sort-m1'),
};
