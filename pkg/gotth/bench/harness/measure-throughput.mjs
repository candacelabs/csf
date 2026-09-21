#!/usr/bin/env node
/*
 * D4 — SSR / full-page render throughput (§3.7, §4's D4 row).
 *
 * The definition, in full, because "throughput" without it is not a number:
 * the highest sustained OFFERED request rate at which, over a 60 s measurement
 * window following a 30 s warm-up, p99 response latency <= 200 ms and error
 * rate = 0.
 *
 * Four properties of the load model that are the whole difference between a
 * number and a press release:
 *
 *   OPEN MODEL, NOT CLOSED. Constant arrival rate. "A closed-loop generator
 *   suffers coordinated omission and reports optimistic latency" — it stops
 *   offering load exactly when the server slows down, which is when the
 *   interesting latency happens.
 *
 *   THE GENERATOR ADDRESSES THE PROXY, not the measured container, on both
 *   sides. The client->proxy leg is TLS and the proxy->app leg is plaintext,
 *   identically for both stacks, so the extra hop is common-mode and cancels in
 *   the A-vs-B delta (§3.6, §3.7).
 *
 *   THE PROXY CAN BE THE CEILING. Its own saturation rate is measured once
 *   against a static response served by the proxy itself — the :8444 listener
 *   in docker/Caddyfile, which has no upstream at all. Any discovered rate
 *   within 20 % of that is marked proxy-limited in the manifest and reported as
 *   a bound on the HARNESS, not as a stack's throughput.
 *
 *   FRESH SESSION COOKIE PER REQUEST. This is the SSR path, not the live path,
 *   so every request is a cold session. It is also why both per-session stores
 *   in this tree evict: without eviction the RPS ceiling would be measuring an
 *   unbounded map.
 *
 * The cached variant is measured SEPARATELY and published as an explicit
 * Next.js-advantage row (§4.5, AS-6). §5.5 forbids it on the measured route
 * because the equivalent gotth-live route is dynamic and cannot be cached —
 * that is a fairness constraint on the comparison, not a claim that Next.js
 * cannot cache, and giving Next.js the win in a labelled row is the point.
 *
 * -----------------------------------------------------------------------------
 * STATUS: this file assembles and prints the commands; it does not run them.
 *
 * The generator (oha or vegeta, choice pinned in versions.lock.md) is not in
 * the bench container, and installing a load generator is a decision for the
 * Phase 5 turn rather than a side effect of construction. The rate-discovery
 * loop, the confirmation runs and the proxy-ceiling probe are all expressed
 * below as the exact invocations, so the Phase 5 turn runs a documented command
 * (FR-75) rather than inventing one.
 */
import { operatorInvoked, requireGates } from './gate.mjs';
import { openRun } from './manifest.mjs';

/** §3.7's ceiling. Both halves; neither alone is the definition. */
export const CEILING = { p99LatencyMs: 200, errorRate: 0 };

/** §3.7: ">= 8 probe points, 20 s each, then a full 60 s confirmation run". */
export const PROBE_SECONDS = 20;
export const PROBE_POINTS = 8;
export const CONFIRM_SECONDS = 60;
export const WARMUP_SECONDS = 30;

/**
 * The generator invocation for one offered rate.
 *
 * `-z` is a duration and `-q` a constant query rate: oha's open-model mode.
 * `--no-tui`, `--json` make it scriptable. `-H` pins Accept and
 * Accept-Encoding identically for both stacks, HTTP/1.1 and keep-alive on both
 * (§3.7).
 */
export function ohaCommand({ url, rate, seconds, cpuset, insecure = true }) {
  return [
    'docker', 'run', '--rm', '--network', 'gotth-live-bench',
    '--cpuset-cpus', cpuset,
    'ghcr.io/hatoo/oha:PINNED-IN-versions.lock.md',
    '--no-tui', '--json',
    '-z', `${seconds}s`,
    '-q', String(rate),
    '--http-version', '1.1',
    '-H', 'Accept: text/html,application/xhtml+xml',
    '-H', 'Accept-Encoding: gzip',
    ...(insecure ? ['--insecure'] : []),
    /* §3.7: "Fresh session cookie per request (cold session)". No cookie jar is
       carried, which is oha's default — stated because it is load-bearing. */
    '--disable-keepalive=false',
    url,
  ];
}

/**
 * §3.7's rate discovery: binary search over offered rate, >= 8 probe points of
 * 20 s each, then a 60 s confirmation at the discovered rate AND at rate x 1.1
 * — "which must fail the ceiling, proving the ceiling is the ceiling".
 *
 * The x1.1 run is not optional and not a formality. Without it a "discovered
 * rate" is just the highest rate that happened to be probed.
 */
export function plan({ url, cpuset, lo = 100, hi = 20_000 }) {
  const probes = [];
  let a = lo;
  let b = hi;
  for (let i = 0; i < PROBE_POINTS; i++) {
    const mid = Math.round((a + b) / 2);
    probes.push({ rate: mid, seconds: PROBE_SECONDS, command: ohaCommand({ url, rate: mid, seconds: PROBE_SECONDS, cpuset }) });
    /* The real search updates a or b from the probe's measured p99; the plan
       printed here shows the shape and the Phase 5 driver walks it for real. */
    b = mid;
  }
  return {
    warmup: { seconds: WARMUP_SECONDS, note: '§5.7: 30 s at the offered rate, discarded' },
    probes,
    confirm: {
      seconds: CONFIRM_SECONDS,
      note: 'at the discovered rate, and at rate x 1.1 which MUST fail the ceiling',
    },
    proxyCeiling: {
      note:
        '§3.7: measured once against a static response served by the proxy ' +
        'itself (docker/Caddyfile :8444, no upstream). A discovered rate within ' +
        '20 % of it is marked proxy-limited in the manifest and reported as a ' +
        'bound on the harness, not as a stack\'s throughput.',
      command: ohaCommand({
        url: 'https://proxy:8444/',
        rate: 50_000,
        seconds: PROBE_SECONDS,
        cpuset,
      }),
    },
    ceiling: CEILING,
  };
}

function main() {
  const args = parse(process.argv.slice(2));
  requireGates(operatorInvoked() ? ['driverValidation', 'conformance', 'phase3'] : undefined);

  const run = openRun({
    dimension: 'D4',
    stack: args.stack ?? 'next',
    app: args.app ?? 'dashboard',
    variant: args.variant ?? 'sse',
  });

  const printed = plan({
    url: args.url ?? 'https://proxy:8443/dashboard',
    cpuset: args.cpuset ?? '10-17',
  });
  run.record({ plan: printed });
  console.log(JSON.stringify(printed, null, 2));
  run.close(
    'aborted',
    'D4 is planned, not executed: the load generator (oha/vegeta, pinned in ' +
      'versions.lock.md) is not installed in this tree. Installing one is a ' +
      'Phase 5 decision, not a side effect of construction.',
  );
  process.exit(1);
}

function parse(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i++) {
    if (!argv[i].startsWith('--')) continue;
    const key = argv[i].slice(2);
    const value = argv[i + 1] && !argv[i + 1].startsWith('--') ? argv[++i] : 'true';
    out[key] = value;
  }
  return out;
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main();
}
