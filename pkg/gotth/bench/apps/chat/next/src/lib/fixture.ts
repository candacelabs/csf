import 'server-only';

import { createHash } from 'node:crypto';
import { readFileSync } from 'node:fs';
import { join } from 'node:path';

/*
 * The committed fixture, and the monotonic schedule both servers replay it on
 * (§2.5).
 *
 * "Both servers replay the fixture against a monotonic schedule: tick N is
 * emitted at T0 + N x 100 ms, where T0 is recorded per run. Neither server
 * generates data; both read the same bytes."
 *
 * T0 is exported so /api/bench/clock can publish it. §3.2 needs it: for push
 * interactions there is no local input, so `t_input(N) = T0 + N x 100 ms`
 * translated onto the page timeline, with the offset estimated by 100 NTP-style
 * exchanges over the harness's control channel. That control channel is the
 * clock route, and it is identical in all three apps.
 */

export interface FixtureTick<E> {
  n: number;
  e: E[];
}

export interface Fixture<B, E> {
  base: B;
  ticks: FixtureTick<E>[];
  /** Bytes as read, so the run manifest can record the SHA-256 (§6). */
  sha256: string;
}

/**
 * Replay interval.
 *
 * 100 ms is §2.5's schedule and the default. The chat stress row (§2.3, peer
 * traffic at 20 msg/s instead of 2) replays the SAME committed bytes with this
 * divided by 10 — one fixture, one SHA-256, two rates, and the rate in force
 * recorded in the manifest. Baking a second rate into a second fixture would
 * mean two files to keep in step for no gain.
 */
export const TICK_MS = Number(process.env.BENCH_TICK_MS ?? 100);

export function load<B, E>(app: string): Fixture<B, E> {
  /*
   * BENCH_FIXTURE_DIR is set by scripts/start-app.mjs (and by the container's
   * entrypoint) to bench/fixtures. It is not derived from import.meta.url,
   * because in the standalone build this module has been bundled into a server
   * chunk whose location is not the source layout — and it is not derived from
   * cwd either, because cwd for the standalone server is the traced app
   * directory several levels below the bench root. An explicit variable with a
   * loud failure is better than a relative path that silently resolves to the
   * wrong tree and replays an empty fixture.
   */
  const dir = process.env.BENCH_FIXTURE_DIR;
  if (!dir) {
    throw new Error(
      'BENCH_FIXTURE_DIR is unset. Start the app with scripts/start-app.mjs, ' +
        'or set it to bench/fixtures (equivalence-spec §2.5).',
    );
  }
  const text = readFileSync(join(dir, app, 'ticks.jsonl'), 'utf8');

  const lines = text.split('\n');
  const base = (JSON.parse(lines[0]) as { base: B }).base;
  const ticks: FixtureTick<E>[] = [];
  for (let i = 1; i < lines.length; i++) {
    if (lines[i] === '') continue;
    ticks.push(JSON.parse(lines[i]) as FixtureTick<E>);
  }

  return { base, ticks, sha256: sha256(text) };
}

function sha256(text: string): string {
  return createHash('sha256').update(text).digest('hex');
}

/**
 * Drives a fixture at the monotonic schedule and calls `apply` once per tick.
 *
 * The schedule is absolute, not cumulative: each timer is set against
 * `T0 + n x TICK_MS`, so a late tick does not push every subsequent tick later.
 * A cumulative `setInterval` would let scheduler jitter accumulate into a drift
 * of seconds over the fixture's hour, and the push-latency rows would then be
 * measuring the drift.
 *
 * Ticks the process is already late for are applied without waiting, so a
 * server that stalls catches up to wall-clock position rather than replaying
 * history slowly. That matters for `M(x)`: the memory window is five minutes
 * in, and a stack that fell behind would be sampled at the wrong tick.
 */
export class Replay<E> {
  readonly t0Ms: number;
  private cursor = 0;
  private timer: ReturnType<typeof setTimeout> | null = null;
  private stopped = false;

  constructor(
    private readonly ticks: FixtureTick<E>[],
    private readonly apply: (tick: FixtureTick<E>) => void,
    private readonly intervalMs: number = TICK_MS,
  ) {
    this.t0Ms = Date.now();
    this.schedule();
  }

  stop(): void {
    this.stopped = true;
    if (this.timer !== null) clearTimeout(this.timer);
  }

  /** The tick number the schedule says should have been emitted by now. */
  tickNow(): number {
    return Math.floor((Date.now() - this.t0Ms) / this.intervalMs);
  }

  private schedule(): void {
    if (this.stopped) return;
    if (this.cursor >= this.ticks.length) {
      // The fixture is an hour long and a run is minutes. Reaching the end is
      // therefore a bug or a very long soak; looping would replay old
      // timestamps and make the conformance test (§2.5) ambiguous, so it stops
      // and says so.
      console.warn('bench: fixture exhausted; no further ticks will be emitted');
      return;
    }

    const next = this.ticks[this.cursor];
    const due = this.t0Ms + next.n * this.intervalMs;
    const delay = due - Date.now();

    if (delay <= 0) {
      this.cursor++;
      this.apply(next);
      // Yield rather than recursing, so a long catch-up cannot blow the stack
      // or starve the event loop the push channel writes on.
      this.timer = setTimeout(() => this.schedule(), 0);
      return;
    }

    this.timer = setTimeout(() => {
      this.cursor++;
      this.apply(next);
      this.schedule();
    }, delay);
  }
}
