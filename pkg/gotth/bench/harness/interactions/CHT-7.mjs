import { focus, pressKey } from '../input.mjs';

/*
 * CHT-7 — type continuously while peer messages arrive at 2/s for 30 s.
 * CORRECTNESS ONLY (FR-25, FR-55) (§2.3).
 *
 * The assertion is "zero dropped keystrokes, caret position stable". This is
 * the one interaction whose failure mode is invisible in a latency
 * distribution: an implementation that re-renders the composer from the server
 * on every peer message loses characters and moves the caret, and every one of
 * its latency numbers would still look fine.
 */
export default {
  id: 'CHT-7',
  app: 'chat',
  route: '/chat/alpha',
  region: 'B',
  measured: 'correctness',
  async assert(page) {
    /*
     * Start from an empty composer with the caret at the end.
     *
     * Without this the assertion is unsound rather than merely awkward: an
     * earlier interaction may have left a body in the box, and a caret at
     * position 0 means every keystroke is inserted at the FRONT — so the
     * assertion would compare the tail of the value against the characters
     * typed and fail for a reason that has nothing to do with dropped input.
     */
    await page.eval(
      `(() => {
        const el = document.querySelector('[data-bench-id="composer"]');
        const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
        setter.call(el, '');
        el.dispatchEvent(new Event('input', { bubbles: true }));
      })()`,
      { awaitPromise: false },
    );
    await focus(page, 'composer');
    await page.eval(
      `(() => {
        const el = document.querySelector('[data-bench-id="composer"]');
        el.setSelectionRange(el.value.length, el.value.length);
      })()`,
      { awaitPromise: false },
    );
    const typed = [];
    for (let i = 0; i < 60; i++) {
      const ch = String.fromCharCode(97 + (i % 26));
      typed.push(ch);
      await pressKey(page, ch);
      await page.eval(`new Promise((r) => setTimeout(r, 500))`);
    }
    const state = await page.eval(`(() => {
      const el = document.querySelector('[data-bench-id="composer"]');
      return { value: el.value, start: el.selectionStart, end: el.selectionEnd };
    })()`);
    const want = typed.join('');
    return {
      pass: state.value.endsWith(want) && state.start === state.value.length,
      dropped: want.length - countTail(state.value, want),
      caret: state.start,
      length: state.value.length,
    };
  },
};

function countTail(value, want) {
  let n = 0;
  for (let i = 0; i < want.length; i++) {
    if (value[value.length - want.length + i] === want[i]) n++;
  }
  return n;
}
