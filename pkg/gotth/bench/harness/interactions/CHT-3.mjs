/*
 * CHT-3 — a peer message arrives; predicate: new last message node matches the
 * fixture entry (§2.3). HEADLINE (push).
 *
 * There is no local input, so §3.2's push procedure applies: t_input(N) =
 * T0 + N x 100 ms translated onto the page timeline, with the offset estimated
 * by 100 NTP-style exchanges over /api/bench/clock and the skew bound published
 * beside every push row. measure-latency.mjs owns that translation; this file
 * owns the predicate and the fixture lookup.
 */
export default {
  id: 'CHT-3',
  app: 'chat',
  route: '/chat/alpha',
  region: 'A',
  measured: 'headline-push',
  push: true,
  /** The tick the harness waits for is chosen by the runner; the body comes
   *  from the committed fixture, so the predicate is about the DATA and not
   *  merely about "something appeared". */
  predicate: (e) => `(() => {
    const nodes = document.querySelectorAll('[data-bench-id="message"]');
    const last = nodes[nodes.length - 1];
    return !!last && last.getAttribute('data-bench-value') === ${JSON.stringify(e.body)};
  })()`,
};
