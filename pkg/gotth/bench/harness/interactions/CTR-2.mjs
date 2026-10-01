import { click } from '../input.mjs';

/* CTR-2 — click [data-bench-id=dec] (§2.1). */
export default {
  id: 'CTR-2',
  app: 'counter',
  route: '/counter',
  region: 'A',
  measured: 'yes',
  async expect(page) {
    const value = Number(await page.eval(`window.__bench.value('value')`));
    return { value: value - 1 };
  },
  predicate: (e) => `window.__bench.value('value') === ${JSON.stringify(String(e.value))}`,
  drive: (page) => click(page, 'dec'),
};
