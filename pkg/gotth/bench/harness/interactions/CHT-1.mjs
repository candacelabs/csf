import { focus, pressKey } from '../input.mjs';

/*
 * CHT-1 — type one character into the composer; predicate: composer value
 * updated (§2.3).
 *
 * "MUST NOT round-trip on either side; a round-trip here is an implementation
 * defect, not a result." So this row exists to FAIL loudly rather than to
 * produce a number worth reading: a p50 in the tens of milliseconds is the
 * finding, and it is a finding about the implementation.
 *
 * The region is B (the composer), not A: the ROI for the paint_present
 * cross-check must be the pixels that changed, and the message list did not.
 */
export default {
  id: 'CHT-1',
  app: 'chat',
  route: '/chat/alpha',
  region: 'B',
  measured: 'yes',
  async setup(page) {
    await focus(page, 'composer');
  },
  async expect(page) {
    const value = await page.eval(
      `document.querySelector('[data-bench-id="composer"]').value`,
    );
    return { value: `${value}x` };
  },
  predicate: (e) =>
    `document.querySelector('[data-bench-id="composer"]').value === ${JSON.stringify(e.value)}`,
  drive: (page) => pressKey(page, 'x'),
};
