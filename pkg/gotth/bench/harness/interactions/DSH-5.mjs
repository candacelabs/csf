import { click } from '../input.mjs';

/*
 * DSH-5 — pause, then resume; predicate: Region B frozen then converged to the
 * current tick (§2.4).
 *
 * Two assertions in one row, and the FIRST one is a negative: after the pause
 * lands, region B must not change for a period in which it certainly would have
 * (2 Hz churn, so 1500 ms is three missed updates). A pause that merely looks
 * paused because nothing happened to arrive is not a pause.
 *
 * Only the RESUME is timed, because a paint predicate needs a paint and
 * freezing is the absence of one. The pause half is a correctness gate on the
 * sample: a run whose freeze assertion fails does not contribute a resume
 * latency.
 */
export default {
  id: 'DSH-5',
  app: 'dashboard',
  route: '/dashboard',
  region: 'B',
  measured: 'yes',
  async setup(page) {
    const state = await page.eval(
      `document.querySelector('[data-bench-id="pause"]').getAttribute('data-bench-value')`,
    );
    if (state === 'paused') {
      await click(page, 'pause');
      await page.eval(`new Promise((r) => setTimeout(r, 400))`);
    }
    await click(page, 'pause');
    await page.eval(`new Promise((r) => setTimeout(r, 400))`);

    const frozenAt = await page.eval(
      `document.querySelector('[data-bench-id="tick"]').getAttribute('data-bench-value')`,
    );
    await page.eval(`new Promise((r) => setTimeout(r, 1500))`);
    const stillFrozen = await page.eval(
      `document.querySelector('[data-bench-id="tick"]').getAttribute('data-bench-value')`,
    );
    if (frozenAt !== stillFrozen) {
      throw new Error(`DSH-5: region B advanced while paused (${frozenAt} -> ${stillFrozen})`);
    }
    return { frozenAt: Number(frozenAt) };
  },
  async expect(page) {
    const tick = Number(
      await page.eval(`document.querySelector('[data-bench-id="tick"]').getAttribute('data-bench-value')`),
    );
    return { after: tick };
  },
  predicate: (e) =>
    `Number(document.querySelector('[data-bench-id="tick"]').getAttribute('data-bench-value')) > ${e.after}`,
  drive: (page) => click(page, 'pause'),
};
