import { click } from '../input.mjs';

/*
 * CHT-5 — send a 501-char body; predicate: inline error visible, composer
 * content intact (§2.3).
 *
 * F-CHT-4's validation is server-side on both stacks, deliberately: a
 * client-side length guard would make this a local paint here and a round trip
 * on the other stack, which is the category error §2.2 exists to keep out of
 * the tables. The "composer content intact" half is asserted too — an
 * implementation that clears the box has lost the feature, not won the row.
 */
const BODY = 'x'.repeat(501);

export default {
  id: 'CHT-5',
  app: 'chat',
  route: '/chat/alpha',
  region: 'B',
  measured: 'yes',
  async expect(page) {
    await page.eval(
      `(() => {
        const el = document.querySelector('[data-bench-id="composer"]');
        const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
        setter.call(el, ${JSON.stringify(BODY)});
        el.dispatchEvent(new Event('input', { bubbles: true }));
      })()`,
      { awaitPromise: false },
    );
    return { body: BODY };
  },
  predicate: (e) => `(() => {
    const err = document.querySelector('[data-bench-id="error"]');
    const composer = document.querySelector('[data-bench-id="composer"]');
    return !!err && err.textContent.trim() !== '' && composer.value === ${JSON.stringify(e.body)};
  })()`,
  drive: (page) => click(page, 'send'),
};
