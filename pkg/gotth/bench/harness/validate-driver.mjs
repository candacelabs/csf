#!/usr/bin/env node
/*
 * §3.6's DRIVER VALIDATION GATE — mandatory before any 1k number is quoted.
 *
 *   "Driver validation gate (mandatory before any 1k number is quoted):
 *    measure per-session memory with 10 real Chromium tabs and with 10
 *    synthetic sessions, on both stacks. If the per-session figures differ by
 *    more than 10 % on either stack, the driver misrepresents a browser and
 *    MUST be fixed before the 1k run. The validation numbers are published with
 *    the report. Without this, the 1k number is an assertion about a synthetic
 *    client, not about sessions."
 *
 *   node harness/validate-driver.mjs --app dashboard --variant sse \
 *     --operator-approved
 *
 * Four measured numbers, published as bench/data/driver-validation.json, which
 * is the file gate.mjs's G-DRIVER derives its verdict from. The artifact is
 * ALWAYS written — including when the run does not happen — because "not run,
 * and why" is a publishable answer under §7 and an estimate is not. There is no
 * path here that writes a figure it did not measure.
 *
 * -----------------------------------------------------------------------------
 * Why each half restarts the stack
 *
 * §5.7: "the proxy is started FRESH alongside the SUT for every run and
 * receives exactly the warm-up volume". The browser half and the synthetic half
 * are two runs, so they get two fresh stacks. Measuring the synthetic half
 * against a server that ten Chromium tabs had just finished with would compare
 * a warm allocator against a cold one and call the difference the driver's.
 *
 * -----------------------------------------------------------------------------
 * What this file will not do
 *
 * It will not derive one side from the other, it will not run while a GPU
 * streaming session is present (Q-7 — skipped, not degraded), and it will
 * not write `status: "run"` unless all four numbers came from four windows.
 */
import { execFileSync } from 'node:child_process';
import { existsSync, mkdirSync, writeFileSync } from 'node:fs';
import { join } from 'node:path';

import { assertTlsBoundary } from './assert-no-tls.mjs';
import { ensureBenchTrust } from './bench-tls.mjs';
import { NETWORK_PROFILES, launch, newPage } from './cdp.mjs';
import { SessionPool, expandCpuset, pinToCpuset, sessionFactory } from './driver.mjs';
import {
  DRIVER_TOLERANCE,
  DRIVER_VALIDATION_FILE,
  VALIDATED_STACKS,
  currentDriverSha256,
  operatorInvoked,
  withinTolerance,
} from './gate.mjs';
import { hostState, requireRunnableHost, steamBlockers } from './host-state.mjs';
import { BENCH_ROOT, DATA_DIR, openRun } from './manifest.mjs';
import {
  WARMUP_LOADS,
  readConstraints,
  readGcConfig,
  sampleCell,
  stackWiring,
  warmUp,
} from './measure-memory.mjs';
import { down, up } from './run.mjs';

/** §3.6: "10 real Chromium tabs ... and 10 synthetic sessions". */
export const VALIDATION_N = 10;

export const ARTIFACT_SCHEMA = 'gotth-live-bench/driver-validation/1';

/**
 * The artifact, in both of its forms.
 *
 * `status` is `run` or `not-run` and nothing else. G-DRIVER refuses anything
 * that is not `run`, so a partial result cannot be dressed up as a pass by
 * omitting a field.
 */
/**
 * Everything that has to be true before the first container starts, collected
 * ALL AT ONCE rather than one refusal at a time.
 *
 * The difference matters to whoever runs this next. A preflight that stops at
 * the first blocker sends an operator away to fix one thing, come back, and
 * find a second — and on a shared host each round trip is a wait for a GPU
 * session to end. The artifact therefore publishes the whole list, so "not run,
 * and why" is a work list rather than a first excuse.
 */
export function preflight({ args, host, images, benchRoot = BENCH_ROOT }) {
  const blockers = [];

  if (host.steamActive) {
    blockers.push({
      id: 'Q-7',
      blocker:
        'a GPU streaming container is running on this host. docs/OPERATOR-QUESTIONS.md ' +
        'Q-7: measured runs are SKIPPED, not degraded, while a GPU session is present. ' +
        'Waiting is the only permitted mitigation — do not stop, restart or reconfigure ' +
        'a co-tenant container to make a run cleaner. The check is deliberately ' +
        'conservative: a RUNNING container blocks whether or not somebody is streaming ' +
        'through it, because the harness cannot see a streaming session from outside and ' +
        'guessing in the permissive direction is how a contended run gets published as ' +
        'a clean one.',
      /* The same matcher host-state.mjs blocks on, imported rather than
         re-spelled: two copies of the pattern is two answers to "what counts as
         a GPU session", and the artifact's `observed` list would then disagree
         with the flag that produced it. */
      observed: steamBlockers(host.containers).map((c) => `${c.name} (${c.image})`),
    });
  }

  for (const [stack, image] of Object.entries(images)) {
    if (!image) {
      blockers.push({
        id: `image:${stack}`,
        blocker:
          `no image was named for the ${stack} stack (--${stack}Image). §3.6's gate is ` +
          '"on both stacks", so a run that could only measure one of them is not this gate.',
      });
      continue;
    }
    if (!imageExists(image)) {
      blockers.push({
        id: `image:${stack}`,
        blocker:
          `the ${stack} image ${image} is not present on this host. ` +
          (stack === 'gotth'
            ? 'Build it from the committed recipe, from bench/, with a context of `..` ' +
              'because the apps\' go.mod `replace` puts the library source in the build: ' +
              '`docker build -f docker/gotth.Dockerfile --build-arg APP=<counter|chat|' +
              'dashboard> -t gotth-live-bench/<app>-gotth:local ..`. §5.2 requires both ' +
              'sides behind the same proxy image by digest with identical constraints, so ' +
              'this half is measured through the same topology and never inferred from the ' +
              'other — bench/README.md, "The measured topology (§3.6)".'
            : 'Build it with the documented command in bench/README.md.'),
      });
    }
  }

  for (const [what, path] of [
    ['the proxy certificate', join(benchRoot, 'docker', 'tls', 'bench.crt')],
    ["compose's parameters", join(benchRoot, 'docker', '.env')],
  ]) {
    if (!existsSync(path)) {
      blockers.push({
        id: `missing:${path.replace(benchRoot, 'bench')}`,
        blocker:
          `${what} is absent at ${path.replace(benchRoot, 'bench')}. ` +
          'See bench/README.md, "The measured topology (§3.6)": `sh docker/gen-cert.sh` ' +
          'and `cp docker/.env.example docker/.env`, then set the four cpusets for THIS ' +
          'host (§5.2 requires them disjoint and their core counts stated).',
      });
    }
  }

  const cpuset = args.cpuset ?? process.env.BENCH_CPUSET_DRIVER ?? '';
  if (expandCpuset(cpuset).length === 0) {
    blockers.push({
      id: 'cpuset:driver',
      blocker:
        '§3.6 requires the synthetic session driver to be "pinned to CPUs disjoint from ' +
        'the server under test", and BENCH_CPUSET_DRIVER names none. docker/.env.example ' +
        "lists §5.2's four disjoint sets.",
    });
  }

  return blockers;
}

function imageExists(reference) {
  try {
    execFileSync('docker', ['image', 'inspect', reference], { stdio: 'ignore' });
    return true;
  } catch {
    return false;
  }
}

export function artifact({
  status,
  reason = null,
  blockers = [],
  app = null,
  variant = null,
  workload = null,
  stacks = {},
  host = null,
  runIds = [],
  /* §3.6: "It is pinned to CPUs disjoint from the server under test", with the
     core counts recorded. The counts belong with the numbers they qualify. */
  driverPin = null,
}) {
  return {
    schema: ARTIFACT_SCHEMA,
    spec: 'docs/bench/equivalence-spec.md §3.6, "Driver validation gate"',
    status,
    reason,
    /* Every reason this did not run, not just the first one. */
    blockers,
    at: new Date().toISOString(),
    n: VALIDATION_N,
    tolerance: DRIVER_TOLERANCE,
    app,
    variant,
    workload,
    /* The validation is a statement about ONE driver. gate.mjs compares this
       against the file on disk and calls a mismatch stale. */
    driverSha256: currentDriverSha256(),
    driverPin,
    stacks,
    host,
    runIds,
  };
}

export function writeArtifact(record, path = DRIVER_VALIDATION_FILE) {
  mkdirSync(DATA_DIR, { recursive: true });
  writeFileSync(path, `${JSON.stringify(record, null, 2)}\n`);
  return record;
}

/**
 * §3.6's comparison for one stack: the two figures and the verdict.
 *
 * Both cells are carried whole rather than reduced to a number, because §3.4
 * says a memory row without its CPU row is not a result — and that applies to
 * the validation numbers too, which are published with the report.
 */
export function compareHalves(stack, browserCell, syntheticCell, tolerance = DRIVER_TOLERANCE) {
  const verdict = withinTolerance(
    browserCell.memPerSessionBytes,
    syntheticCell.memPerSessionBytes,
    tolerance,
  );
  return {
    stack,
    browserPerSessionBytes: browserCell.memPerSessionBytes,
    syntheticPerSessionBytes: syntheticCell.memPerSessionBytes,
    relativeDifference: verdict.relative,
    within: verdict.within,
    reason: verdict.reason ?? null,
    browserCell,
    syntheticCell,
  };
}

/**
 * Ten real Chromium tabs, each loaded and each having reached §3.3's `ready`.
 *
 * "Established" means the same thing it means for the synthetic half: the
 * channel is open and the first message has been applied. A tab counted at
 * `load` would be a tab whose session the server has not built yet, and the
 * comparison would be between ten sessions and ten navigations.
 */
export async function establishTabs(browser, { origin, route, n = VALIDATION_N }) {
  const pages = [];
  for (let i = 0; i < n; i++) {
    const page = await newPage(browser, { networkProfile: NETWORK_PROFILES.lan });
    await page.goto(`${origin}${route}`);
    await page.eval(
      `Promise.race([
        window.__bench.whenReady(),
        new Promise((_, reject) => setTimeout(() => reject(new Error('window.__bench.ready never became true')), 30000)),
      ])`,
    );
    pages.push(page);
  }
  return pages;
}

async function measureHalf({
  label,
  stack,
  args,
  wiring,
  origin,
  sut,
  proxy,
  establish,
  teardown,
  settleMs,
  samples,
}) {
  const run = openRun({
    dimension: 'D3-validation',
    stack,
    app: wiring.app,
    variant: args.variant ?? 'sse',
    workload: args.workload ?? 'idle',
    concurrency: VALIDATION_N,
  });
  try {
    const tls = await assertTlsBoundary({
      sut,
      proxy,
      expectedProxyDigest: args.proxyDigest ?? null,
    });
    run.record({ tls, driverValidation: { half: label, n: VALIDATION_N } });
    if (!tls.pass) {
      run.close('aborted', `TLS boundary assertion failed: ${tls.findings.join('; ')}`);
      throw new Error(`${stack}/${label}: ${tls.findings.join('; ')}`);
    }

    run.record({
      gc: await readGcConfig(sut),
      containers: { sut, proxy },
      cpusets: { sut: await readConstraints(sut), proxy: await readConstraints(proxy) },
    });

    const cell = await sampleCell({
      sut,
      proxy,
      n: VALIDATION_N,
      workload: args.workload ?? 'idle',
      warmUpOnce: () =>
        warmUp({ origin, route: wiring.route, loads: Number(args.warmupLoads ?? WARMUP_LOADS) }),
      establish,
      teardown,
      settleMs,
      samples,
    });
    run.samples([{ half: label, ...flatten(cell) }], [
      'half',
      'n',
      'workload',
      'm0',
      'mN',
      'memPerSessionBytes',
      'cpuSecondsPerSessionPerMinute',
    ]);
    run.record({ cell });
    run.close('completed');
    return { cell, runId: run.runId };
  } catch (err) {
    if (run.manifest.outcome === 'started') run.close('aborted', err.message);
    throw err;
  }
}

function flatten(cell) {
  return {
    n: cell.n,
    workload: cell.workload,
    m0: cell.m0,
    mN: cell.mN,
    memPerSessionBytes: cell.memPerSessionBytes,
    cpuSecondsPerSessionPerMinute: cell.cpuSecondsPerSessionPerMinute,
  };
}

async function main() {
  const args = parse(process.argv.slice(2));

  /* §5.7. The gate runner is a measurement, so it is operator-initiated like
     every other one. */
  if (!operatorInvoked()) {
    console.error(
      'bench: refusing to start. §5.7 makes bench runs operator-initiated; pass ' +
        '--operator-approved to say that a person decided to run this.',
    );
    process.exit(2);
  }

  const wiring = stackWiring(args);
  const context = {
    app: wiring.app,
    variant: args.variant ?? 'sse',
    workload: args.workload ?? 'idle',
  };

  const images = { gotth: args.gotthImage ?? null, next: args.nextImage ?? null };

  /* Q-7 and everything else BEFORE anything is started, and the refusal is
     PUBLISHED rather than printed: an operator who runs this on a host with a
     GPU session gets an artifact that says the gate did not run and why, and
     G-DRIVER goes on refusing. That is the whole of §7's "not measured, and
     why". Note the ORDER: nothing has opened a run manifest yet, so a refused
     attempt consumes no run id and §6's contiguous sequence — and
     bench/data/'s "no run ids" claim — survive an attempt that was right to
     stop. */
  const host = hostState();
  const blockers = preflight({ args, host, images });
  if (blockers.length > 0) {
    writeArtifact(
      artifact({
        status: 'not-run',
        reason: blockers.map((b) => `[${b.id}] ${b.blocker}`).join('\n\n'),
        blockers,
        ...context,
        host,
      }),
    );
    for (const b of blockers) console.error(`bench: BLOCKED [${b.id}] ${b.blocker}\n`);
    console.error(
      `bench: wrote ${DRIVER_VALIDATION_FILE} with status "not-run" and ${blockers.length} ` +
        'blocker(s). G-DRIVER keeps refusing.',
    );
    process.exit(1);
  }

  /* Belt and braces: preflight already read the host, and this is the function
     the rest of the harness calls, so the two cannot drift apart. */
  requireRunnableHost();

  const driverPin = pinToCpuset(args.cpuset ?? process.env.BENCH_CPUSET_DRIVER ?? '');
  if (!driverPin.pinned) {
    const why =
      '§3.6 requires the synthetic session driver to be pinned to CPUs disjoint from ' +
      `the server under test, and it is not: ${driverPin.reason}.`;
    writeArtifact(artifact({ status: 'not-run', reason: why, ...context, host }));
    console.error(why);
    process.exit(1);
  }

  const sut = args.sut ?? 'bench-app';
  const proxy = args.proxy ?? 'bench-proxy';
  const origin = args.origin ?? 'https://127.0.0.1:18443';
  const settleMs = args.settle ? Number(args.settle) : undefined;
  const samples = args.samples ? Number(args.samples) : undefined;

  /* The node half of this gate talks to the proxy over the self-signed
     certificate §5.3 generates, and the browser half is handed the same
     certificate's SPKI pin below. Installing the anchor HERE, before the first
     container starts, is redundant in the happy path — warmUp() and every
     session do it too — and is the point in the unhappy one: a topology brought
     up without `sh docker/gen-cert.sh` fails now, naming the missing step,
     instead of forty lines into a session pool with a bare
     DEPTH_ZERO_SELF_SIGNED_CERT and ten containers already running. This gate
     gets one window on a shared host (Q-7); it should not spend it on a
     diagnosable error discovered late. */
  const trust = ensureBenchTrust(origin);
  if (trust.applied) {
    console.error(
      `bench: trusting the bench proxy certificate ${trust.certPath} (SPKI ${trust.spkiPin}), ` +
        `default CA store ${trust.store.before} -> ${trust.store.after}. Verification stays on.`,
    );
  }

  const stacks = {};
  const runIds = [];
  try {
    for (const stack of VALIDATED_STACKS) {
      if (!images[stack]) {
        throw new Error(
          `no image for the ${stack} stack: pass --${stack}Image. §5.2 requires both sides ` +
            'behind the same proxy image by digest, so both halves are measured through the ' +
            'same topology and neither is inferred from the other.',
        );
      }

      /* --- half one: ten real Chromium tabs -------------------------------- */
      await up({ BENCH_SUT_IMAGE: images[stack], BENCH_VARIANT: args.variant ?? 'sse' });
      let browser = await launch({
        extraFlags: args.spki ? [`--ignore-certificate-errors-spki-list=${args.spki}`] : [],
      });
      let tabs = [];
      const browserHalf = await measureHalf({
        label: 'browser',
        stack,
        args,
        wiring,
        origin,
        sut,
        proxy,
        settleMs,
        samples,
        establish: async (n) => {
          tabs = await establishTabs(browser, { origin, route: wiring.route, n });
          return { tabs: tabs.length, browser: (await browser.version()).product };
        },
        teardown: async () => {
          for (const page of tabs) await page.close().catch(() => {});
          tabs = [];
        },
      });
      await browser.close().catch(() => {});
      browser = null;
      await down().catch(() => {});

      /* --- half two: ten synthetic sessions -------------------------------- */
      await up({ BENCH_SUT_IMAGE: images[stack], BENCH_VARIANT: args.variant ?? 'sse' });
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
        workload: args.workload ?? 'idle',
      });
      const syntheticHalf = await measureHalf({
        label: 'synthetic',
        stack,
        args,
        wiring,
        origin,
        sut,
        proxy,
        settleMs,
        samples,
        establish: async (n) => {
          await pool.establish(n);
          return pool.stats();
        },
        teardown: () => pool.teardown(),
      });
      await down().catch(() => {});

      runIds.push(browserHalf.runId, syntheticHalf.runId);
      stacks[stack] = compareHalves(stack, browserHalf.cell, syntheticHalf.cell);
      console.log(
        `${stack}: ${stacks[stack].browserPerSessionBytes} B/session (10 tabs) vs ` +
          `${stacks[stack].syntheticPerSessionBytes} B/session (10 synthetic) — ` +
          `${stacks[stack].within ? 'within' : 'OUTSIDE'} ${DRIVER_TOLERANCE * 100} %`,
      );
    }
  } catch (err) {
    await down().catch(() => {});
    writeArtifact(
      artifact({
        status: 'not-run',
        reason: `the gate did not complete: ${err.message}`,
        ...context,
        stacks,
        host,
        runIds,
      }),
    );
    console.error(err.message);
    console.error(
      `\nbench: wrote ${DRIVER_VALIDATION_FILE} with status "not-run". G-DRIVER keeps refusing.`,
    );
    process.exit(1);
  }

  const record = writeArtifact(
    artifact({ status: 'run', ...context, stacks, host, runIds, driverPin }),
  );
  const failed = VALIDATED_STACKS.filter((s) => !record.stacks[s].within);
  if (failed.length > 0) {
    console.error(
      `\nbench: ${failed.join(' and ')} outside ${DRIVER_TOLERANCE * 100} %. §3.6: "the driver ` +
        'misrepresents a browser and MUST be fixed before the 1k run." G-DRIVER refuses.',
    );
    process.exit(1);
  }
  console.log(`\nbench: driver validation PASSED on both stacks; wrote ${DRIVER_VALIDATION_FILE}.`);
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
  main().catch(async (err) => {
    await down().catch(() => {});
    console.error(err.message);
    process.exit(1);
  });
}
