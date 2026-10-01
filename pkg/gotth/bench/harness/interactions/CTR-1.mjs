import { click } from '../input.mjs';

/*
 * CTR-1 — click [data-bench-id=inc]; predicate: [data-bench-id=value]
 * textContent === expected (§2.1). HEADLINE.
 *
 * The cleanest isolation of round-trip cost in the whole suite: one click, one
 * server-authoritative integer, one text node plus two derived nodes repainted.
 */
export default {
  id: 'CTR-1',
  app: 'counter',
  route: '/counter',
  region: 'A',
  measured: 'headline',
  async expect(page) {
    const value = Number(await page.eval(`window.__bench.value('value')`));
    return { value: value + 1 };
  },
  predicate: (e) => `window.__bench.value('value') === ${JSON.stringify(String(e.value))}`,
  drive: (page) => click(page, 'inc'),
};
