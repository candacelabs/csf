#!/usr/bin/env node
/*
 * D3 — server memory per active session, and the CPU figure that must be
 * published beside it (§3.4, §3.6, §4's D3 row).
 *
 *   mem_per_session = ( M(N) - M(0) ) / N
 *
 * M(x) = the median of 60 samples taken at 1 Hz over the last 60 s of a
 * 5-minute steady-state window, where M(x) is cgroup v2 `memory.current` minus
 * `memory.stat`'s `file` — anonymous + kernel memory attributable to the
 * workload, read from OUTSIDE the process.
 *
 * -----------------------------------------------------------------------------
 * The three rules this dimension exists to obey, and where each lives in code
 *
 * 1. TLS OUTSIDE, BOTH STACKS (§3.6, amendment A-1, T-21). The proxy container
 *    is excluded from M(x) and from the paired CPU figure, and its own cpu.stat
 *    delta is published as its own line. assertTlsBoundary() runs BEFORE any
 *    cell is recorded and its result goes in the manifest. The asymmetry is
 *    worth ~18,000 B/session and is disqualifying in EITHER direction — the
 *    pre-A-1 spec bound gotth-live alone and the asymmetry ran against it.
 *
 * 2. NO MEMORY ROW WITHOUT A CPU ROW (§3.4). "Memory without CPU is not a
 *    result under this spec." A polling stack holds ~no per-connection memory
 *    and pays in CPU instead; reporting only memory would flatter it. So
 *    sampleCell() returns both or neither.
 *
 * 3. NO 1k FIGURE WITHOUT THE 10-TAB GATE (§3.6, T-9). "measure per-session
 *    memory with 10 real Chromium tabs and with 10 synthetic sessions, on both
 *    stacks. If the per-session figures differ by more than 10 % on either
 *    stack, the driver misrepresents a browser and MUST be fixed before the 1k
 *    run." Without it the 1k number is an assertion about a synthetic client,
 *    not about sessions. That gate is validate-driver.mjs; this file cannot
 *    reach a cell until gate.mjs says it passed.
 *
 * -----------------------------------------------------------------------------
 * STATUS
 *
 * The orchestration below is complete: the synthetic session driver is
 * driver.mjs, which speaks each stack's actual protocol and imports the SHIPPED
 * client codec rather than re-implementing the frame layout. What still refuses
 * is the GATES — Phase 3's tuning is unfinished (Appendix B, QA3-1/2/3), the
 * §2.5 conformance test has not run, and the 10-tab driver validation has not
 * run. `requireGates` throws before a manifest is opened, so a refused
 * invocation leaves no run id behind and `bench/data/` stays honest.
 */
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

import { assertTlsBoundary } from './assert-no-tls.mjs';
import { ensureBenchTrust } from './bench-tls.mjs';
import {
  SessionPool,
  WORKLOADS,
  expandCpuset,
  pinToCpuset,
  sessionFactory,
  sleep,
} from './driver.mjs';
import { operatorInvoked, requireGates } from './gate.mjs';
import { requireRunnableHost } from './host-state.mjs';
import { openRun } from './manifest.mjs';

const exec = promisify(execFile);

/** §3.6's window: 5 minutes of steady state, the last 60 s sampled at 1 Hz. */
export const SETTLE_MS = 300_000;
export const SAMPLES = 60;

/**
 * §3.6's warm-up: "the same number of full-page loads and the same elapsed
 * time" before M(0) as before M(N).
 *
 * 50 is this tree's number, not the spec's — §3.6 fixes only that the two
 * warm-ups match. It is recorded in the manifest and is identical on both
 * stacks, which is the property the clause is actually about.
 */
export const WARMUP_LOADS = 50;

/** One cgroup v2 reading for a container, from outside the process (§3.6). */
export async function readCgroup(container) {
  const { stdout } = await exec('docker', [
    'exec',
    container,
    'sh',
    '-c',
    'cat /sys/fs/cgroup/memory.current; echo ---; cat /sys/fs/cgroup/memory.stat; echo ---; cat /sys/fs/cgroup/cpu.stat',
  ]);
  const [currentRaw, statRaw, cpuRaw] = stdout.split('---');
  const stat = Object.fromEntries(
    statRaw
      .trim()
      .split('\n')
      .map((line) => line.split(/\s+/))
      .map(([k, v]) => [k, Number(v)]),
  );
  const cpu = Object.fromEntries(
    cpuRaw
      .trim()
      .split('\n')
      .map((line) => line.split(/\s+/))
      .map(([k, v]) => [k, Number(v)]),
  );
  const current = Number(currentRaw.trim());
  return {
    at: Date.now(),
    memoryCurrent: current,
    file: stat.file ?? 0,
    /* THE figure. Page cache is excluded because it is not attributable to the
       workload and it is the single easiest way to make one stack look worse
       for a reason that is the kernel's. */
    m: current - (stat.file ?? 0),
    cpuUsageUsec: cpu.usage_usec ?? 0,
    stat,
  };
}

/**
 * §3.6's sampling window: 5 minutes of steady state, then the median of 60
 * samples at 1 Hz over the last 60 s.
 *
 * The settle is not padding. Node's V8 and Go's GC both reach a different
 * steady state minutes in than seconds in, and the headline is deliberately
 * UNFORCED steady state "because that is what a deployment sees" (§3.6). A
 * shorter window would report whichever allocator happened to be between
 * collections.
 */
export async function sampleWindow(container, { settleMs = SETTLE_MS, samples = SAMPLES } = {}) {
  const first = await readCgroup(container);
  await sleep(Math.max(0, settleMs - samples * 1000));
  const readings = [];
  for (let i = 0; i < samples; i++) {
    readings.push(await readCgroup(container));
    await sleep(1000);
  }
  const last = readings[readings.length - 1];
  const sortedM = readings.map((r) => r.m).sort((a, b) => a - b);
  const durationSec = (last.at - first.at) / 1000;
  return {
    /* Median, not mean (§6: percentiles, never means). */
    m: sortedM[Math.floor(sortedM.length / 2)],
    readings,
    /* §3.4's paired CPU figure: cpu.stat usage_usec delta / duration / N. The
       caller divides by N; this returns seconds of CPU over the window. */
    cpuSeconds: (last.cpuUsageUsec - first.cpuUsageUsec) / 1e6,
    durationSec,
  };
}

/**
 * §3.6's warm-up, one function so M(0) and M(N) cannot get different ones.
 *
 * Full-page loads of the measured route through the PROXY, on both stacks,
 * sequentially — a warm-up that ran concurrently would be a small load test and
 * would leave the server in a different place than the one the clause asks for
 * ("JIT-warmed code and lazily compiled routes ... in the baseline on both
 * sides"). The response body is read and dropped so the server actually
 * finishes writing it.
 */
export async function warmUp({ origin, route, loads = WARMUP_LOADS }) {
  /* This is the FIRST request §3.6's driver-validation gate makes — before a
     session pool exists, before a browser is launched — so it is also the first
     place a missing trust anchor would have thrown. It is the reason the gate
     was dead on arrival rather than merely degraded. */
  ensureBenchTrust(origin);
  const startedAt = Date.now();
  let bytes = 0;
  let ok = 0;
  for (let i = 0; i < loads; i++) {
    const res = await fetch(`${origin}${route}`, {
      headers: { Accept: 'text/html', 'Accept-Encoding': 'gzip' },
    });
    const body = await res.arrayBuffer();
    bytes += body.byteLength;
    if (res.ok) ok += 1;
  }
  return { loads, ok, bytes, elapsedMs: Date.now() - startedAt, startedAt };
}

/**
 * §3.6's "same warm-up" clause, asserted rather than assumed.
 *
 * Equal load counts are exact. Elapsed time cannot be, so the comparison is a
 * tolerance and the two elapsed times are BOTH published — a clause that reads
 * "the same elapsed time" is not discharged by a harness that only promises it.
 */
export function warmUpsMatch(a, b, tolerance = 0.25) {
  const slower = Math.max(a.elapsedMs, b.elapsedMs);
  const faster = Math.min(a.elapsedMs, b.elapsedMs);
  const drift = slower === 0 ? 0 : (slower - faster) / slower;
  return {
    equalLoads: a.loads === b.loads,
    elapsedMs: [a.elapsedMs, b.elapsedMs],
    drift,
    tolerance,
    match: a.loads === b.loads && drift <= tolerance,
  };
}

/** §3.6/§5.1: the GC configuration in force, read from the container, not from .env. */
export async function readGcConfig(container) {
  const { stdout } = await exec('docker', ['inspect', '-f', '{{json .Config.Env}}', container]);
  const env = Object.fromEntries(
    JSON.parse(stdout || '[]').map((line) => {
      const eq = line.indexOf('=');
      return [line.slice(0, eq), line.slice(eq + 1)];
    }),
  );
  const nodeOptions = env.NODE_OPTIONS ?? '';
  const maxOldSpace = /--max-old-space-size=(\d+)/.exec(nodeOptions);
  return {
    /* Go side. */
    GOGC: env.GOGC || null,
    GOMEMLIMIT: env.GOMEMLIMIT || null,
    /* Node side, "set equal to the container memory limit" (§3.6). */
    NODE_OPTIONS: nodeOptions || null,
    maxOldSpaceSizeMb: maxOldSpace ? Number(maxOldSpace[1]) : null,
  };
}

/** §5.2's container constraints and the cpusets, with the core COUNTS stated. */
export async function readConstraints(container) {
  const { stdout } = await exec('docker', [
    'inspect',
    '-f',
    '{{json .HostConfig}}',
    container,
  ]);
  const host = JSON.parse(stdout || '{}');
  return {
    cpuset: host.CpusetCpus || null,
    cores: expandCpuset(host.CpusetCpus || '').length,
    memLimitBytes: host.Memory ?? null,
    memSwapLimitBytes: host.MemorySwap ?? null,
    pidsLimit: host.PidsLimit ?? null,
  };
}

/**
 * §3.6's secondary figures, and the symmetry rule that governs them.
 *
 * | | gotth-live (Go) | Next.js (Node) |
 * | Runtime-internal | runtime/metrics ... | process.memoryUsage() ... |
 * | Post-forced-GC floor | debug.FreeOSMemory() | --expose-gc + global.gc() |
 *
 * "The forced-GC floor is a secondary, labelled number on both sides or on
 * neither. Forcing GC on only one stack — in either direction — is a method
 * error."
 *
 * Both figures need the measured process to expose them, and neither bench
 * image does today: there is no runtime-introspection route on either stack and
 * no --expose-gc on the Node side. Rather than take the one that happens to be
 * reachable, this returns "not measured" for BOTH with the reason, which is
 * what §7 requires and what the symmetry rule requires. It is never inferred
 * from the headline.
 *
 * The probes are parameters so that adding the two routes — one per stack, in
 * the same landing, or neither — turns this on without touching the rule.
 */
export async function secondaryFigures({ stack, probe = null, forceGcProbe = null } = {}) {
  const unavailable = (which) => ({
    available: false,
    stack,
    reason:
      `${which} is not measured: it needs an introspection route inside the measured ` +
      'container on BOTH stacks (Go runtime/metrics and debug.FreeOSMemory; Node ' +
      'process.memoryUsage/v8.getHeapStatistics and --expose-gc + global.gc), and ' +
      'neither bench image carries one. §3.6 makes these symmetric on both sides or ' +
      'on neither, so taking whichever is reachable would be the method error the ' +
      'clause names. Reported as "not measured" per §7; never derived.',
  });

  return {
    runtimeInternal: probe ? await probe() : unavailable('the runtime-internal secondary'),
    postForcedGcFloor: forceGcProbe
      ? await forceGcProbe()
      : unavailable('the post-forced-GC floor'),
    symmetric: Boolean(probe) === Boolean(forceGcProbe),
  };
}

/**
 * A cell: M(0), M(N), the per-session figure, and the paired CPU figures —
 * for the SUT and, as its own line, for the proxy.
 *
 * Returns both or neither, because §3.4 says memory without CPU is not a
 * result. There is no code path here that produces a memory number alone.
 *
 * The ORDER is §3.6's, not a convenience:
 *
 *   warm-up -> M(0) -> establish N -> the SAME warm-up -> M(N) -> teardown
 *
 * so that "M(0) is measured after the same warm-up as M(N)" is a property of
 * the sequence rather than a note in a report. The two warm-up records are both
 * returned and their agreement is asserted.
 */
export async function sampleCell({
  sut,
  proxy,
  establish,
  teardown,
  n,
  workload,
  warmUpOnce,
  settleMs = SETTLE_MS,
  samples = SAMPLES,
  secondaries = null,
}) {
  const warmZero = await warmUpOnce();
  const zero = await sampleWindow(sut, { settleMs, samples });
  const zeroProxy = await sampleWindow(proxy, { settleMs: 1000, samples: 1 });

  const established = await establish(n);
  const warmFull = await warmUpOnce();
  const full = await sampleWindow(sut, { settleMs, samples });
  const fullProxy = await sampleWindow(proxy, { settleMs: 1000, samples: 1 });
  const secondary = secondaries ? await secondaries() : null;
  await teardown();

  return {
    n,
    workload,
    m0: zero.m,
    mN: full.m,
    memPerSessionBytes: (full.m - zero.m) / n,
    /* §3.4: "mean server CPU seconds per session per minute". */
    cpuSecondsPerSessionPerMinute:
      ((full.cpuSeconds - zero.cpuSeconds) / n) * (60 / full.durationSec),
    /* §3.6's warm-up clause, evidenced. */
    warmUp: { zero: warmZero, full: warmFull, agreement: warmUpsMatch(warmZero, warmFull) },
    /* What the driver actually achieved, so a cell with dead sessions in it is
       visible instead of quietly dividing by an N that was never established. */
    driver: established ?? null,
    secondary,
    window: { settleMs, samples, m0Readings: zero.readings.length, mNReadings: full.readings.length },
    /* §3.4/§3.6: the proxy is excluded from M(x) and from the paired figure,
       and its own cpu.stat delta is published as its own line. The polling
       variant shifts work into the proxy (connection churn, TLS handshakes)
       rather than into the application server, and hiding the proxy's CPU would
       let it look free by a second route. */
    proxy: {
      excludedFromM: true,
      cpuSecondsIdle: zeroProxy.cpuSeconds,
      cpuSecondsLoaded: fullProxy.cpuSeconds,
    },
  };
}

/** The per-stack wiring: everything that differs between an A run and a B run. */
export function stackWiring(args) {
  const app = args.app ?? 'dashboard';
  const routes = {
    counter: { route: '/counter', mount: '/counter/live', stream: '/api/counter/stream' },
    chat: { route: '/chat/alpha', mount: '/chat/live', stream: '/api/chat/stream' },
    dashboard: { route: '/dashboard', mount: '/dashboard/live', stream: '/api/dashboard/stream' },
  };
  const r = routes[app];
  if (!r) throw new Error(`unknown app ${JSON.stringify(app)}; §2 names counter, chat, dashboard`);
  return {
    app,
    route: args.route ?? r.route,
    mountPath: args.mount ?? r.mount,
    streamPath: args.stream ?? (args.variant === 'poll' ? r.stream.replace(/stream$/, 'snapshot') : r.stream),
    wsPath: args.wsPath ?? '/ws',
  };
}

async function main() {
  const args = parse(process.argv.slice(2));

  /* §5.7 first, and §3.6/§2.5/Appendix B behind it. This THROWS before a
     manifest is opened, deliberately: a refused invocation must not consume a
     run id, because §6 makes a gap in the id sequence an audit failure and
     "bench/data/ contains no run ids" is a claim a reader checks by counting. */
  requireGates(operatorInvoked() ? ['driverValidation', 'conformance', 'phase3'] : undefined, {
    app: args.app ?? 'dashboard',
    variant: args.variant ?? 'sse',
  });

  /* Q-7: skipped, not degraded, while a GPU streaming session is present. */
  requireRunnableHost();

  const wiring = stackWiring(args);
  const stack = args.stack ?? 'next';
  const n = Number(args.n ?? 100);
  const workload = args.workload ?? 'idle';
  if (!WORKLOADS[workload]) {
    throw new Error(`unknown workload ${JSON.stringify(workload)}; §3.4 names ${Object.keys(WORKLOADS).join(', ')}`);
  }

  const run = openRun({
    dimension: 'D3',
    stack,
    app: wiring.app,
    variant: args.variant ?? 'sse',
    workload,
    concurrency: n,
  });

  try {
    const sut = args.sut ?? 'bench-app';
    const proxy = args.proxy ?? 'bench-proxy';

    /* §3.6, and §12's amendment A-1 note 3: "It adds an assertion, not just a
       rule." Before ANY cell is recorded. */
    const tls = await assertTlsBoundary({
      sut,
      proxy,
      expectedProxyDigest: args.proxyDigest ?? null,
    });
    run.record({ tls });
    if (!tls.pass) {
      run.close('aborted', `TLS boundary assertion failed: ${tls.findings.join('; ')}`);
      process.exit(1);
    }

    /* §3.6's pinned-and-disclosed GC configuration, §5.2's constraints and the
       four cpusets — all read from the containers, all in the manifest BEFORE
       the first window opens. */
    const gc = await readGcConfig(sut);
    const sutConstraints = await readConstraints(sut);
    const proxyConstraints = await readConstraints(proxy);
    const driverPin = pinToCpuset(args.cpuset ?? process.env.BENCH_CPUSET_DRIVER ?? '');
    run.record({
      gc,
      containers: { sut, proxy },
      cpusets: {
        sut: sutConstraints,
        proxy: proxyConstraints,
        /* §3.6: "It is pinned to CPUs disjoint from the server under test."
           Disjointness is asserted, not assumed: overlapping sets would make
           the driver a co-tenant of the thing it is measuring. */
        driver: driverPin,
        disjoint: disjoint(sutConstraints.cpuset, driverPin.cpuset),
      },
    });
    if (!driverPin.pinned) {
      run.close(
        'aborted',
        `§3.6 requires the session driver to be pinned to CPUs disjoint from the SUT: ${driverPin.reason}`,
      );
      process.exit(1);
    }
    if (!disjoint(sutConstraints.cpuset, driverPin.cpuset)) {
      run.close(
        'aborted',
        `driver cpuset ${driverPin.cpuset} overlaps the SUT's ${sutConstraints.cpuset}; §3.6 requires them disjoint`,
      );
      process.exit(1);
    }

    const origin = args.origin ?? 'https://127.0.0.1:18443';
    const pool = new SessionPool({
      make: sessionFactory({
        stack,
        origin,
        route: wiring.route,
        mountPath: wiring.mountPath,
        variant: args.variant ?? 'sse',
        streamPath: wiring.streamPath,
        wsPath: wiring.wsPath,
      }),
      workload,
    });

    const cell = await sampleCell({
      sut,
      proxy,
      n,
      workload,
      warmUpOnce: () =>
        warmUp({ origin, route: wiring.route, loads: Number(args.warmupLoads ?? WARMUP_LOADS) }),
      establish: async (count) => {
        await pool.establish(count);
        return pool.stats();
      },
      teardown: () => pool.teardown(),
      secondaries: () => secondaryFigures({ stack }),
    });

    run.samples(
      [
        {
          n: cell.n,
          workload: cell.workload,
          m0: cell.m0,
          mN: cell.mN,
          memPerSessionBytes: cell.memPerSessionBytes,
          cpuSecondsPerSessionPerMinute: cell.cpuSecondsPerSessionPerMinute,
          proxyCpuSecondsIdle: cell.proxy.cpuSecondsIdle,
          proxyCpuSecondsLoaded: cell.proxy.cpuSecondsLoaded,
        },
      ],
      [
        'n',
        'workload',
        'm0',
        'mN',
        'memPerSessionBytes',
        'cpuSecondsPerSessionPerMinute',
        'proxyCpuSecondsIdle',
        'proxyCpuSecondsLoaded',
      ],
    );
    run.record({ cell });
    run.close('completed');
    console.log(JSON.stringify(cell, null, 2));
  } catch (err) {
    run.close('aborted', err.message);
    console.error(err.message);
    process.exit(1);
  }
}

/** §3.6/§5.2: the driver's cores and the SUT's cores must not intersect. */
export function disjoint(a, b) {
  const left = new Set(expandCpuset(a ?? ''));
  if (left.size === 0) return false;
  const right = expandCpuset(b ?? '');
  if (right.length === 0) return false;
  return right.every((core) => !left.has(core));
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
  main().catch((err) => {
    console.error(err.message);
    process.exit(1);
  });
}
