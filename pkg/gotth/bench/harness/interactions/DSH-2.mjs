import { focus, pressKey } from '../input.mjs';

/*
 * DSH-2 — type one character into search; predicate: Region B row set matches
 * after the debounce (§2.4).
 *
 * §2.4: "debounced 150 ms on both stacks with identical debounce
 * implementation semantics". Those semantics, written down so the gotth-live
 * side can match rather than approximate: TRAILING edge, timer reset on every
 * keystroke, one request fired 150 ms after the last one, no leading call and
 * no maximum wait.
 *
 * The debounce is INSIDE the measured interval on both stacks, which makes this
 * row about the round trip plus a constant that is equal on both sides. Timing
 * from the end of the debounce instead would be timing a quantity no user
 * experiences.
 */
export default {
  id: 'DSH-2',
  app: 'dashboard',
  route: '/dashboard',
  region: 'B',
  measured: 'yes',
  timeoutMs: 15000,
  async setup(page) {
    await focus(page, 'search');
  },
  expect: async () => ({ needle: 'a' }),
  predicate: (e) => `(() => {
    const rows = document.querySelectorAll('[data-bench-id="row"]');
    if (rows.length === 0) return false;
    for (const row of rows) {
      if (!row.children[1].textContent.toLowerCase().includes(${JSON.stringify(e.needle)})) return false;
    }
    return true;
  })()`,
  drive: (page, ctx) => pressKey(page, ctx.expected.needle),
};
