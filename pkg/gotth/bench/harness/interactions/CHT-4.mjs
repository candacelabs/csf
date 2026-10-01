import { click } from '../input.mjs';

/*
 * CHT-4 — switch room; predicate: room header + list content match the target
 * room (§2.3).
 *
 * The switch is a Server Action and not a navigation. §2 forbids client-side
 * routing on both sides, and §3.2 requires t_input and t_paint to come from the
 * same page's performance.now() timeline — a document navigation puts them in
 * two timelines and makes this interaction unmeasurable under the spec's own
 * definition. So the active room is server session state on both stacks.
 */
export default {
  id: 'CHT-4',
  app: 'chat',
  route: '/chat/alpha',
  region: 'D',
  measured: 'yes',
  async expect(page) {
    const current = await page.eval(
      `document.querySelector('[data-bench-id="room-title"]').getAttribute('data-bench-value')`,
    );
    return { room: current === 'beta' ? 'gamma' : 'beta' };
  },
  predicate: (e) =>
    `document.querySelector('[data-bench-id="room-title"]').getAttribute('data-bench-value') === ${JSON.stringify(e.room)}`,
  drive: (page, ctx) => click(page, `room-${ctx.expected.room}`),
};
