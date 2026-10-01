import { click } from '../input.mjs';

/*
 * CHT-2b — click Send, OPTIMISTIC LOCAL message appears. Next.js only,
 * labelled row (§2.3, AS-2).
 *
 * "Optimistic UI is idiomatic App Router practice and a genuine Next.js
 * capability. It is measured with optimistic UI on (CHT-2b, the local paint)
 * and the same interaction is measured to server confirmation (CHT-2) on both
 * stacks. Both rows ship."
 *
 * Reporting it is the point: it quantifies a v1 gotth-live exclusion (BL-4),
 * and suppressing it would be the strawman FR-73 forbids. The gotth-live column
 * for this row reads "no equivalent", never a blank and never a slower number.
 */
export default {
  id: 'CHT-2b',
  app: 'chat',
  route: '/chat/alpha',
  region: 'A',
  measured: 'nextjs-only',
  nextOnly: true,
  expect: (await import('./CHT-2.mjs')).default.expect,
  predicate: (e) => `(() => {
    const nodes = document.querySelectorAll('[data-bench-id="message"]');
    const last = nodes[nodes.length - 1];
    return !!last
      && last.getAttribute('data-bench-value') === ${JSON.stringify(e.body)}
      && last.getAttribute('data-bench-state') === 'pending';
  })()`,
  drive: (page) => click(page, 'send'),
};
