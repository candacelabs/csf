/*
 * CHT-6 — scroll the list 20 screens. No paint predicate; CORRECTNESS AND JANK
 * ONLY (§2.3).
 *
 * It produces no latency row. What it produces is a long-task trace from
 * CDP Tracing over the scroll, which is §4.5's "long-task time during the
 * interaction set" — cheap, informative, and a place either stack may win.
 * F-CHT-1 forbids virtualization on both sides, so both are scrolling the same
 * 200 real nodes.
 */
export default {
  id: 'CHT-6',
  app: 'chat',
  route: '/chat/alpha',
  region: 'A',
  measured: 'jank',
  async drive(page) {
    const box = await page.eval(`(() => {
      const el = document.querySelector('[data-bench-id="messages"]');
      const r = el.getBoundingClientRect();
      return { x: r.x + r.width / 2, y: r.y + r.height / 2, h: r.height };
    })()`);
    for (let i = 0; i < 20; i++) {
      await page.send('Input.dispatchMouseEvent', {
        type: 'mouseWheel',
        x: box.x,
        y: box.y,
        deltaX: 0,
        deltaY: box.h,
      });
      await page.eval(`new Promise((r) => setTimeout(r, 32))`);
    }
  },
};
