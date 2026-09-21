import { click } from '../input.mjs';

/* CTR-3 — click [data-bench-id=inc10] (§2.1). */
export default {
  id: 'CTR-3',
  app: 'counter',
  route: '/counter',
  region: 'A',
  measured: 'yes',
  async expect(page) {
    const value = Number(await page.eval(`window.__bench.value('value')`));
    return { value: value + 10 };
  },
  predicate: (e) => `window.__bench.value('value') === ${JSON.stringify(String(e.value))}`,
  drive: (page) => click(page, 'inc10'),
};
