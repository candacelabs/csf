#!/usr/bin/env node
/*
 * The run driver: A/B/A/B interleaving, one manifest per started run, and the
 * host checks that can stop one (§5.2, §5.7, §6).
 *
 *   node harness/run.mjs --dimension D2 --app dashboard --variant sse \
 *     --operator-approved
 *
 * -----------------------------------------------------------------------------
 * Why interleaving is the design and not a detail
 *
 * §5.2: "Runs are interleaved A/B/A/B at the run level. We cannot pin the host
 * CPU governor (host changes are out of bounds for this project — repo policy),
 * so thermal and frequency drift is made common-mode by interleaving instead of
 * controlled away."
 *
 * That sentence is doing a lot of work on a host that docs/OPERATOR-QUESTIONS.md
 * Q-2 records as shared and non-quiescent. Five runs of A followed by five runs
 * of B would attribute an hour of somebody else's load to whichever stack
 * happened to be second. Interleaving does not make the host quiet; it makes the
 * noise land on both stacks equally, which is the only thing available without
 * touching the host — and touching the host is not available.
 *
 * §5.7: the proxy is started FRESH alongside the SUT for every run and receives
 * exactly the warm-up volume, so its connection pools, session cache and
 * keep-alive state to the upstream are in the same condition on both sides. "A
 * proxy carried warm across an A/B pair, or reused across the two stacks, is a
 * method error of the same kind as forcing GC on one side only."
 *
 * -----------------------------------------------------------------------------
 * STATUS: skeleton. It sequences, gates, and writes manifests; it does not yet
 * bring stacks up, because bench/apps/<app>/gotth/ does not exist yet and there
 * is no B side to interleave with. Every place that needs the gotth-live half
 * is marked and none of them is guessed at.
 */
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';

import { assertTlsBoundary, reachable } from './assert-no-tls.mjs';
import { operatorInvoked, requireGates } from './gate.mjs';
import { requireRunnableHost } from './host-state.mjs';
import { openRun } from './manifest.mjs';

const exec = promisify(execFile);

/** §6: 5 independent runs per cell, each with a fresh start of BOTH containers. */
export const RUNS_PER_CELL = 5;

export const STACKS = ['gotth', 'next'];

/**
 * The A/B/A/B order §5.2 requires, starting from alternating feet across cells
 * so the first-run position is not always the same stack's.
 */
export function interleave(runsPerCell = RUNS_PER_CELL, startWith = 0) {
  const order = [];
  for (let i = 0; i < runsPerCell; i++) {
    order.push(STACKS[(i + startWith) % 2], STACKS[(i + startWith + 1) % 2]);
  }
  return order;
}

/**
 * Bring the topology up for ONE stack, fresh (§5.7).
 *
 * `--force-recreate` is not an optimisation to be removed later: it is what
 * makes "the proxy is started fresh alongside the SUT for every run" true. A
 * `docker compose up` that finds the proxy already running leaves it warm, and
 * the second stack measured is then measured against a proxy the first stack
 * warmed.
 */
export async function up(env) {
  await exec('docker', ['compose', '-f', 'docker/compose.yaml', 'up', '-d', '--force-recreate'], {
    env: { ...process.env, ...env },
    cwd: new URL('..', import.meta.url).pathname,
  });
}

export async function down() {
  await exec('docker', ['compose', '-f', 'docker/compose.yaml', 'down', '-v', '--remove-orphans'], {
    cwd: new URL('..', import.meta.url).pathname,
  });
}

async function main() {
  const args = parse(process.argv.slice(2));

  /* §5.7: "Bench runs are operator-initiated. The harness refuses to start
     unless explicitly invoked, and records host state." */
  if (!operatorInvoked()) {
    console.error(
      'bench: refusing to start. §5.7 makes bench runs operator-initiated; pass ' +
        '--operator-approved to say that a person decided to run this.\n' +
        'Construction and single-tab smoke need no approval: node harness/smoke.mjs.',
    );
    process.exit(2);
  }

  /* Q-7: skipped, not degraded, while a GPU session is present. Throws. */
  const host = requireRunnableHost();
  if (host.contended) {
    console.error(
      `bench: host has ${host.coTenants.length} co-tenant container(s); runs will be ` +
        'marked CONTENDED in the manifest and excluded from the headline set ' +
        '(§5.2). They are still published. This is expected on this host ' +
        '(docs/OPERATOR-QUESTIONS.md Q-2) and is spec threat T-5, not a solved problem.',
    );
  }

  requireGates(['driverValidation', 'conformance', 'phase3']);

  const order = interleave(Number(args.runs ?? RUNS_PER_CELL));
  console.log(`bench: A/B order for this cell: ${order.join(' ')}`);

  for (const stack of order) {
    const run = openRun({
      dimension: args.dimension ?? 'D2',
      stack,
      app: args.app ?? 'dashboard',
      variant: args.variant ?? 'sse',
      networkProfile: args.profile ?? 'lan',
    });
    try {
      if (stack === 'gotth' && !args.gotthImage) {
        /* This used to read "bench/apps/<app>/gotth/ does not exist yet". All
           three are built now, and so is the synthetic session driver
           (harness/driver.mjs), so the message was describing a state that had
           stopped being true — which is worse than no message, because the next
           reader would go looking for the wrong missing thing. What is actually
           missing is one level up: bench/docker/ carries next.Dockerfile and no
           gotth counterpart, so there is no image to point BENCH_SUT_IMAGE at
           for the A side. bench/README.md deviation D-9. */
        throw new Error(
          'there is no gotth-live SUT image to interleave with: bench/docker/ carries ' +
            'next.Dockerfile and no counterpart, so BENCH_SUT_IMAGE can be pointed at ' +
            'only one of the two stacks. §5.2 requires both sides behind the same proxy ' +
            'image by digest with identical constraints and cpuset, so this is a blocker ' +
            'for every D3 and D4 number and not only for the A/B order. Pass ' +
            '--gotthImage once one exists. See bench/README.md, deviation D-9.',
        );
      }

      const image =
        stack === 'gotth'
          ? args.gotthImage
          : (args.nextImage ?? args.image ?? 'gotth-live-bench/dashboard-next:sse');
      await up({ BENCH_SUT_IMAGE: image });
      const port = process.env.BENCH_PROXY_PORT ?? '18443';
      for (let i = 0; i < 60 && !(await reachable('127.0.0.1', Number(port))); i++) {
        await new Promise((r) => setTimeout(r, 500));
      }

      const tls = await assertTlsBoundary({
        sut: 'bench-app',
        proxy: 'bench-proxy',
        expectedProxyDigest: args.proxyDigest ?? null,
      });
      run.record({ tls });
      if (!tls.pass) {
        run.close('aborted', `TLS boundary assertion failed: ${tls.findings.join('; ')}`);
        continue;
      }

      throw new Error('dimension execution is wired per-dimension in measure-*.mjs');
    } catch (err) {
      /* §6: a manifest for EVERY run the harness starts, including aborted and
         failed ones, with the abort reason. A gap in the id sequence is an
         audit failure, so there is no early return that skips this. */
      run.close('aborted', err.message);
      console.error(`  ${run.runId} [${stack}] aborted: ${err.message}`);
    } finally {
      await down().catch(() => {});
    }
  }
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
