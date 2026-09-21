import { click } from '../input.mjs';

/*
 * DSH-4 — rows-per-page 50 -> 200; predicate: row count === 200 (§2.4).
 *
 * §2.4 writes the control as "select 50 / 100 / 200", which reads as "choose
 * one of". It is rendered as buttons rather than a <select> so the input is a
 * native pointerdown, which is what §3.2's t_input is defined against; a
 * <select> would put the causal start in a change event the spec does not
 * define. Recorded in bench/README.md.
 */
export default {
  id: 'DSH-4',
  app: 'dashboard',
  route: '/dashboard',
  region: 'B',
  measured: 'yes',
  /*
   * The precondition is the UNFILTERED table at 50 rows, and it is established
   * rather than assumed: the filter and the search are per-session server state
   * that an earlier interaction may have left set, and 200 rows is not
   * reachable through a `warn` filter. Establishing preconditions in setup is
   * why setup exists — a run that inherits the previous interaction's state is
   * measuring an unstated one.
   */
  async setup(page) {
    await click(page, 'filter-all');
    await page.eval(
      `(() => {
        const el = document.querySelector('[data-bench-id="search"]');
        if (el.value === '') return;
        const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set;
        setter.call(el, '');
        el.dispatchEvent(new Event('input', { bubbles: true }));
      })()`,
      { awaitPromise: false },
    );
    await page.eval(`new Promise((r) => setTimeout(r, 600))`);
    await click(page, 'per-page-50');
    await page.eval(`new Promise((r) => setTimeout(r, 400))`);
  },
  expect: async () => ({ rows: 200 }),
  predicate: (e) => `document.querySelectorAll('[data-bench-id="row"]').length === ${e.rows}`,
  drive: (page) => click(page, 'per-page-200'),
};
