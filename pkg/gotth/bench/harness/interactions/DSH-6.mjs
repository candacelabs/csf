import { click } from '../input.mjs';

/*
 * DSH-6 — Region E refresh; predicate: panel content === expected (§2.4).
 *
 * AS-3: the mechanism differs by design — plain HTMX on the gotth-live side per
 * FR-62, a Server Action form here — and the visible behaviour is the same.
 * Both mechanisms ship inside both apps and both are counted in both bundle
 * measurements (§3.5): HTMX's gzipped bytes count against gotth-live, and this
 * form's action bytes count against Next.js.
 *
 * The panel's seq is what the predicate keys on rather than its text, because
 * the text is derived from live state and could coincidentally repeat.
 */
export default {
  id: 'DSH-6',
  app: 'dashboard',
  route: '/dashboard',
  region: 'E',
  measured: 'yes',
  async expect(page) {
    const seq = Number(
      await page.eval(`document.querySelector('[data-bench-id="panel"]').getAttribute('data-bench-value')`),
    );
    return { seq: seq + 1 };
  },
  predicate: (e) =>
    `document.querySelector('[data-bench-id="panel"]').getAttribute('data-bench-value') === ${JSON.stringify(String(e.seq))}`,
  drive: (page) => click(page, 'refresh'),
};
