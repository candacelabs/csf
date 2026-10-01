import { click } from '../input.mjs';

/*
 * CHT-2 — click Send, SERVER-CONFIRMED message appears (§2.3). HEADLINE.
 *
 * Predicate: "last message node's data-bench-value === sent body AND its
 * data-bench-state === confirmed".
 *
 * §2.0 describes data-bench-value as marking the element whose textContent is
 * the predicate's subject, while §2.3 reads it as an attribute holding the
 * body. Both readings are satisfied by the markup: the <li> carries
 * data-bench-value="<body>" and the body span's textContent is the same string.
 * Flagged in bench/README.md as a spec wording ambiguity, resolved in the
 * direction that makes both true rather than by choosing one.
 */
export default {
  id: 'CHT-2',
  app: 'chat',
  route: '/chat/alpha',
  region: 'A',
  measured: 'headline',
  async expect(page, ctx) {
    const body = ctx.body ?? `bench ${Date.now()}`;
    await page.eval(
      `(() => {
        const el = document.querySelector('[data-bench-id="composer"]');
        const setter = Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, 'value').set;
        setter.call(el, ${JSON.stringify(body)});
        el.dispatchEvent(new Event('input', { bubbles: true }));
      })()`,
      { awaitPromise: false },
    );
    return { body };
  },
  predicate: (e) => `(() => {
    const nodes = document.querySelectorAll('[data-bench-id="message"]');
    const last = nodes[nodes.length - 1];
    return !!last
      && last.getAttribute('data-bench-value') === ${JSON.stringify(e.body)}
      && last.getAttribute('data-bench-state') === 'confirmed';
  })()`,
  drive: (page) => click(page, 'send'),
};
