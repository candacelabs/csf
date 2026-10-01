import { click } from '../input.mjs';

/*
 * CTR-4 — click reset; predicate: textContent === "0" (§2.1).
 *
 * The one CTR predicate with a literal rather than a computed expectation, and
 * therefore the one that needs a setup: resetting an already-zero counter
 * paints nothing and the observer would time out. The setup is not measured.
 */
export default {
  id: 'CTR-4',
  app: 'counter',
  route: '/counter',
  region: 'A',
  measured: 'yes',
  async setup(page) {
    const value = Number(await page.eval(`window.__bench.value('value')`));
    if (value !== 0) return;
    await click(page, 'inc');
    await page.eval(`new Promise((r) => setTimeout(r, 250))`);
  },
  predicate: () => `window.__bench.value('value') === "0"`,
  drive: (page) => click(page, 'reset'),
};
