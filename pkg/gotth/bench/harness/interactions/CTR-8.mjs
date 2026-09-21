/*
 * CTR-8 — reload; value preserved. CORRECTNESS ASSERTION ONLY (§2.1).
 *
 * It is in the interaction set because F-CTR-4 ("value survives a full page
 * reload, server-authoritative") is the feature that makes the counter an
 * equivalent-semantics comparison rather than a category error. It produces no
 * latency sample and no row; it produces a pass or a fail, and a fail voids the
 * counter's cells because the app under test was not the app the spec
 * describes.
 */
export default {
  id: 'CTR-8',
  app: 'counter',
  route: '/counter',
  region: 'A',
  measured: 'correctness',
  async assert(page, { origin, route }) {
    const before = await page.eval(`window.__bench.value('value')`);
    await page.goto(`${origin}${route}`);
    await page.eval(`window.__bench.whenReady()`);
    const after = await page.eval(`window.__bench.value('value')`);
    return { pass: before === after, before, after };
  },
};
