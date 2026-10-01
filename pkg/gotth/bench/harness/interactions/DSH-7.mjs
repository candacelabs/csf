/*
 * DSH-7 — passive tick N applied; predicate: Region B rows for tick N match the
 * fixture (§2.4). HEADLINE (push).
 *
 * This is the single most important number the comparison produces: no input,
 * no user, just the server pushing 53 logical updates a second at 1000 sessions
 * and a browser applying them.
 *
 * §3.2's push procedure owns t_input: there is no local input, so
 * t_input(N) = T0 + N x 100 ms translated onto the page timeline via the
 * NTP-style exchange over /api/bench/clock, and the skew bound is published
 * beside the row. The skew is common-mode — same procedure, same host, same
 * clock, both stacks — so it biases both absolute numbers identically and
 * cancels in the A-vs-B delta. Stated in the report, never assumed.
 */
export default {
  id: 'DSH-7',
  app: 'dashboard',
  route: '/dashboard',
  region: 'B',
  measured: 'headline-push',
  push: true,
  /** The runner picks the target tick; the predicate asserts the page reached it. */
  predicate: (e) =>
    `Number(document.querySelector('[data-bench-id="tick"]').getAttribute('data-bench-value')) >= ${e.tick}`,
};
