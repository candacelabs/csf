import LocalCounter from '@/components/LocalCounter';

/*
 * C-A — the structural floor (§2.2, AS-1).
 *
 * "an additional useState counter, measured once, reported in the latency
 * table as a separate, clearly labelled row: 'Next.js, client-local state — no
 * gotth-live equivalent.' It is not averaged with C-B, not omitted, and not
 * buried."
 *
 * Reporting it is the point. It quantifies the ceiling client-side state buys,
 * which gotth-live cannot reach by construction (PRD BL-3/BL-4), and
 * suppressing it would be the strawman FR-73 forbids.
 *
 * It lives on its own route rather than on /counter so that its bytes and its
 * elements cannot leak into the C-B measurement: D1 counts the JS the measured
 * route fetches, and a second counter sitting in the same bundle would charge
 * Next.js for code C-B never runs.
 */
export const dynamic = 'force-dynamic';

export default function LocalCounterPage() {
  return (
    <main>
      <h1>Next.js counter — client-local state (C-A)</h1>
      <p className="lede">
        This counter is <code>useState</code>. It paints in the same frame as
        the click and it is not shared, not persisted and not server-authoritative.
        gotth-live has no equivalent; that is what this page measures.
      </p>
      <LocalCounter />
    </main>
  );
}
