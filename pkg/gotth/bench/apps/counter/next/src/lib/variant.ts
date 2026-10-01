/**
 * The live-data variant this build ships (§5.4).
 *
 *   sse   primary   — SSE from a streaming Route Handler, consumed with
 *                     useSWRSubscription. Everything inside Next.js.
 *   ws    secondary — a dedicated `ws` server beside the standalone Next
 *                     server, in the same container.
 *   poll  third     — SWR refreshInterval. Measured for D3/D4 only, where it
 *                     changes the memory-vs-CPU trade fundamentally (§3.4).
 *
 * This is a BUILD-TIME choice, not a runtime one. Selecting the transport at
 * runtime would ship all three implementations in the client bundle, and D1
 * would then charge Next.js for two transports it is not using. next.config.ts
 * resolves the `@transport` alias to exactly one module, so the bundle
 * provably contains one.
 */
export type BenchVariant = 'sse' | 'ws' | 'poll';

export const VARIANTS: readonly BenchVariant[] = ['sse', 'ws', 'poll'];

export function parseVariant(value: string | undefined): BenchVariant {
  if (value === 'ws' || value === 'poll' || value === 'sse') return value;
  if (value === undefined || value === '') return 'sse';
  throw new Error(
    `BENCH_VARIANT must be one of ${VARIANTS.join(' | ')}, got ${JSON.stringify(value)}`,
  );
}

/** Server-side view of the variant. */
export const VARIANT: BenchVariant = parseVariant(process.env.BENCH_VARIANT);

/**
 * Polling interval for the `poll` variant, in milliseconds.
 *
 * §5.4 names the mechanism (`SWR refreshInterval`) but not the interval, and
 * the interval is the whole memory-vs-CPU trade the polling column exists to
 * show. 1000 ms is the default taken here: it is the rate at which the
 * dashboard's slowest region updates (§2.4 region A, 1 Hz), so a polling
 * client is not asked to be slower than the app's own slowest live region.
 * Recorded in docs/OPERATOR-QUESTIONS.md Q-BENCH-2 and overridable, because
 * D4 should sweep it rather than assume it.
 */
export const POLL_INTERVAL_MS: number = Number(process.env.BENCH_POLL_INTERVAL_MS ?? 1000);
