import { click } from '../input.mjs';

/*
 * CTR-6 — 20 clicks on inc within 1000 ms; predicate: start+20, FINAL
 * CONVERGENCE ONLY (§2.1). Reported as convergence latency, not per-click.
 *
 * t_input is the FIRST click's timestamp, not the last: the quantity is "how
 * long until the twentieth result is on screen", and the shim's default
 * t_input would be the most recent input. It is passed explicitly for that
 * reason.
 *
 * On the Next.js side React serialises Server Actions, so this is 20 sequential
 * round trips under the idiomatic pattern §5.4 mandates. That is real Next.js
 * behaviour, not a handicap introduced by the harness, and the alternative a
 * latency-chasing team would reach for is written down in bench/README.md so
 * the §5.4 audit can rule on it.
 */
export default {
  id: 'CTR-6',
  app: 'counter',
  route: '/counter',
  region: 'A',
  measured: 'convergence',
  timeoutMs: 30000,
  async expect(page) {
    const value = Number(await page.eval(`window.__bench.value('value')`));
    return { value: value + 20 };
  },
  predicate: (e) => `window.__bench.value('value') === ${JSON.stringify(String(e.value))}`,
  async drive(page) {
    for (let i = 0; i < 20; i++) {
      await click(page, 'inc');
      if (i === 0) {
        await page.eval(`window.__benchFirstInput = window.__bench.t_input;`, {
          awaitPromise: false,
        });
      }
      await page.eval(`new Promise((r) => setTimeout(r, 45))`);
    }
    await page.eval(`window.__bench.t_input = window.__benchFirstInput;`, { awaitPromise: false });
  },
};
