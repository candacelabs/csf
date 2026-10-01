import { click } from '../input.mjs';

/*
 * CHT-8 — the `readonly` participant sends; error visible, no message appended.
 * CORRECTNESS ONLY (FR-47) (§2.3).
 *
 * Identity is a cookie the harness sets before the assertion; the refusal is
 * server-side on both stacks (F-CHT-9). A disabled Send button would prove
 * nothing — the thing under test is that the SERVER says no, so the composer
 * stays usable and the attempt is really made.
 */
export default {
  id: 'CHT-8',
  app: 'chat',
  route: '/chat/alpha',
  region: 'B',
  measured: 'correctness',
  async assert(page, { origin, route }) {
    await page.send('Network.setCookie', {
      name: 'bench_who',
      value: 'readonly',
      url: origin,
      path: '/',
    });
    await page.goto(`${origin}${route}`);
    await page.eval(`window.__bench.whenReady()`);

    /*
     * "No message appended" means NO MESSAGE OF OURS, not "the list did not
     * grow".
     *
     * The fixture is replaying peer traffic at 2 msg/s the whole time (§2.3),
     * so a count comparison fails whenever a peer speaks during the wait — and
     * it would fail for a reason that has nothing to do with F-CHT-9. The body
     * is unique to this assertion, so its absence is the exact claim.
     */
    const body = `refused-${Date.now()}`;
    await page.eval(
      `(() => {
        const el = document.querySelector('[data-bench-id="composer"]');
        const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
        setter.call(el, ${JSON.stringify(body)});
        el.dispatchEvent(new Event('input', { bubbles: true }));
      })()`,
      { awaitPromise: false },
    );
    await click(page, 'send');
    /* Generous, because this is a correctness assertion and not a timing: the
       cost of waiting too long is a slower suite, and the cost of waiting too
       little is a false "the server did not refuse". */
    await page.eval(`new Promise((r) => setTimeout(r, 2500))`);

    const after = await page.eval(`(() => ({
      mine: [...document.querySelectorAll('[data-bench-id="message"]')]
        .filter((n) => n.getAttribute('data-bench-value') === ${JSON.stringify(body)}).length,
      total: document.querySelectorAll('[data-bench-id="message"]').length,
      error: (document.querySelector('[data-bench-id="error"]') || {}).textContent || '',
    }))()`);

    return { pass: after.error.trim() !== '' && after.mine === 0, body, ...after };
  },
};
