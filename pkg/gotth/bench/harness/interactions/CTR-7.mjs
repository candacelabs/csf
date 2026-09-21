/*
 * CTR-7 — `+1` in tab A, repaint in tab B (§2.1). HEADLINE (push).
 *
 * §3.2: "For CTR-7 (cross-tab), t_input is instead tab A's native input
 * timestamp, and t_paint is tab B's; both tabs run in the same browser process
 * group and share a performance time origin only if same-origin — enforced,
 * since both tabs load the same origin."
 *
 * Sharing a time origin is not automatic: performance.timeOrigin is per
 * document, so tab A's t_input and tab B's t_paint are on two timelines whose
 * origins differ by however long elapsed between the two navigations. The
 * offset is measured once per pair, from Date.now()/performance.now() in each
 * tab, and subtracted. It is recorded with the sample so the correction is
 * auditable rather than invisible.
 *
 * This interaction therefore takes TWO pages and is driven by
 * measure-latency.mjs's cross-tab path rather than by runInteraction().
 */
export default {
  id: 'CTR-7',
  app: 'counter',
  route: '/counter',
  region: 'A',
  measured: 'headline-push',
  crossTab: true,
  driveIn: 'inc',
  predicate: (e) => `window.__bench.value('value') === ${JSON.stringify(String(e.value))}`,
  async expect(pageB) {
    const value = Number(await pageB.eval(`window.__bench.value('value')`));
    return { value: value + 1 };
  },
};
