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
 * A BUILD-time choice, not a runtime one: next.config.ts resolves @transport to
 * exactly one module, so the measured bundle provably contains one transport
 * and D1 cannot charge Next.js for two it never opens.
 *
 * The file is duplicated in all three apps rather than shared, because each
 * app's transport modules are typed against that app's own payload and a shared
 * copy would need a generic seam whose only purpose is to avoid three short
 * files. The three copies are identical apart from this paragraph.
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

export const VARIANT: BenchVariant = parseVariant(process.env.BENCH_VARIANT);

/**
 * Polling interval for the `poll` variant, in milliseconds.
 *
 * §5.4 names the mechanism (`SWR refreshInterval`) but not the interval, and
 * the interval is the whole memory-vs-CPU trade the polling column exists to
 * show. 1000 ms is the default taken, matching the other two apps so the three
 * polling columns are comparable with each other. Recorded in bench/README.md
 * under "Defaults this tree took that the spec left open".
 */
export const POLL_INTERVAL_MS: number = Number(process.env.BENCH_POLL_INTERVAL_MS ?? 1000);
