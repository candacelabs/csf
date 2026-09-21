import { clock, fixtureSha } from '@/lib/store';

/*
 * §3.2's control channel for push interactions.
 *
 * "There is no local input; the causal start is the server's emission of tick
 * N. Both servers emit tick N at T0 + N x 100 ms on CLOCK_MONOTONIC. At run
 * start, estimate the offset between the server's CLOCK_MONOTONIC and the
 * page's performance.now() origin with 100 NTP-style exchanges over the
 * harness's control channel; take the sample with minimum round-trip and record
 * the estimated skew bound."
 *
 * This route is that channel. It is not part of the app's product surface, it
 * is never fetched by the page, and the harness calls it before and after a run
 * — so its cost is outside the measured interaction set on both stacks, and the
 * gotth-live side must expose the identical shape for the same code to drive
 * both (§4: no per-stack branch in the harness).
 *
 * `monotonicNs` is process.hrtime.bigint(), which is CLOCK_MONOTONIC on Linux.
 * It is published as a string because JSON has no 64-bit integer.
 */
export const dynamic = 'force-dynamic';
export const runtime = 'nodejs';

export async function GET(): Promise<Response> {
  const c = clock();
  return new Response(
    JSON.stringify({
      ...c,
      monotonicNs: process.hrtime.bigint().toString(),
      fixtureSha256: fixtureSha(),
      pid: process.pid,
    }),
    {
      headers: {
        'Content-Type': 'application/json; charset=utf-8',
        'Cache-Control': 'no-store',
      },
    },
  );
}
