#!/usr/bin/env node
/*
 * D2 — event->paint latency (§3.1, §3.2, §4's D2 row).
 *
 *   node harness/measure-latency.mjs --stack next --app dashboard \
 *     --variant sse --profile lan --origin https://127.0.0.1:18443 \
 *     --operator-approved
 *
 * Sample plan (§4): 200 samples/run x 5 runs = 1000 per interaction ID per
 * stack per network profile, inter-interaction gap U(400, 600) ms. Warm-up
 * (§5.7): 200 discarded interactions per run per interaction ID.
 *
 * -----------------------------------------------------------------------------
 * THIS FILE DOES NOT RUN YET, AND THAT IS THE POINT
 *
 * requireGates() refuses until §5.7's operator gate, §3.6's driver-validation
 * gate, §2.5's conformance gate and Appendix B's Phase-3 gate have all actually
 * passed. Phase 3's tuning is unfinished: QA3-1 (coalesce_flush_at), QA3-2
 * (MinResyncInterval/ResyncBurst) and QA3-3 (provenance-log volume) are
 * safety-chosen defaults, and two of the three move numbers this dimension
 * publishes. A re-tune landing after measurement has begun forces full
 * re-collection under §12, so the cheap way to avoid that is to finish Phase 3
 * first — which is the sequencing QA-2 committed to and the reason the gate is
 * code rather than a note.
 */
import { NETWORK_PROFILES, launch, newPage } from './cdp.mjs';
import { runInteraction } from './input.mjs';
import { operatorInvoked, requireGates } from './gate.mjs';
import { forApp, INTERACTIONS } from './interactions/index.mjs';
import { openRun } from './manifest.mjs';
import { cell } from './analyze.mjs';

/** §4: "inter-interaction gap U(400, 600) ms". */
function gap() {
  return 400 + Math.random() * 200;
}

/** §5.7: 200 discarded interactions per run per interaction ID. */
export const WARMUP_ITERATIONS = 200;
/** §4: 200 samples per run, 5 runs. */
export const SAMPLES_PER_RUN = 200;
export const RUNS = 5;

/**
 * §3.1's timer resolution, measured rather than assumed.
 *
 * "Timer resolution is performance.now()'s, which Chrome coarsens based on
 * cross-origin isolation state — 100 us without, 5 us with. Both apps are
 * served with identical COOP/COEP headers so both get the identical clamp; the
 * effective resolution is recorded in the run manifest."
 *
 * The proxy sets those headers for both stacks, so the clamp is identical by
 * construction — but "identical by construction" is a claim, and this is the
 * check. It reads the smallest non-zero gap between consecutive samples, which
 * is the clamp.
 */
export async function timerResolution(page) {
  return page.eval(`(() => {
    let min = Infinity;
    let last = performance.now();
    for (let i = 0; i < 200000; i++) {
      const now = performance.now();
      const d = now - last;
      if (d > 0 && d < min) min = d;
      last = now;
    }
    return { resolutionMs: min === Infinity ? null : min, crossOriginIsolated: self.crossOriginIsolated === true };
  })()`);
}

/**
 * §3.2's push-clock exchange: 100 NTP-style round trips over the app's
 * /api/bench/clock control channel, keeping the sample with the minimum RTT.
 *
 * The skew is common-mode — same procedure, same host, same clock, both
 * stacks — so it biases both absolute numbers identically and cancels in the
 * A-vs-B delta. It is published beside every push row rather than assumed away.
 */
export async function estimateClockSkew(page, origin, exchanges = 100) {
  return page.eval(
    `(async () => {
      let best = null;
      for (let i = 0; i < ${exchanges}; i++) {
        const t0 = performance.now();
        const res = await fetch(${JSON.stringify(`${origin}/api/bench/clock`)}, { cache: 'no-store' });
        const body = await res.json();
        const t1 = performance.now();
        const rtt = t1 - t0;
        if (best === null || rtt < best.rtt) {
          // The server's answer is assumed to have been produced at the
          // midpoint of the round trip, which is the standard NTP estimator and
          // is exact when the path is symmetric. The residual is bounded by
          // rtt/2 and that bound is what gets published.
          best = {
            rtt,
            skewMs: body.nowMs - (performance.timeOrigin + t0 + rtt / 2),
            t0Ms: body.t0Ms,
            tickMs: body.tickMs,
            serverTick: body.tick,
            fixtureSha256: body.fixtureSha256,
          };
        }
      }
      return { ...best, boundMs: best.rtt / 2, exchanges: ${exchanges} };
    })()`,
  );
}

async function main() {
  const args = parse(process.argv.slice(2));
  requireGates(
    operatorInvoked() ? ['driverValidation', 'conformance', 'phase3'] : undefined,
  );

  const app = args.app ?? 'dashboard';
  const stack = args.stack ?? 'next';
  const profile = args.profile ?? 'lan';
  const origin = args.origin ?? 'https://127.0.0.1:18443';
  const ids = args.ids ? args.ids.split(',') : forApp(app).filter((i) => i.drive).map((i) => i.id);

  const run = openRun({
    dimension: 'D2',
    stack,
    app,
    variant: args.variant ?? 'sse',
    networkProfile: profile,
  });

  const browser = await launch({
    extraFlags: args.spki ? [`--ignore-certificate-errors-spki-list=${args.spki}`] : [],
  });

  try {
    const page = await newPage(browser, { networkProfile: NETWORK_PROFILES[profile] });
    const route = INTERACTIONS.get(ids[0]).route;
    await page.goto(`${origin}${route}`);
    await page.eval(`window.__bench.whenReady()`);

    run.record({
      timerResolution: await timerResolution(page),
      clockSkew: await estimateClockSkew(page, origin),
      browser: await browser.version(),
    });

    const byId = new Map();

    for (const id of ids) {
      const interaction = INTERACTIONS.get(id);
      if (interaction.cpuThrottlingRate) {
        await page.send('Emulation.setCPUThrottlingRate', { rate: interaction.cpuThrottlingRate });
      }

      const perRun = [];
      for (let r = 0; r < RUNS; r++) {
        const samples = [];
        const total = WARMUP_ITERATIONS + SAMPLES_PER_RUN;
        for (let i = 0; i < total; i++) {
          if (interaction.setup) await interaction.setup(page, { origin, route });
          const sample = await runInteraction(page, interaction, { origin, route });
          /* The first WARMUP_ITERATIONS are discarded (§5.7). They are taken
             identically, not skipped — a warm-up that runs a different code
             path warms the wrong thing. */
          if (i >= WARMUP_ITERATIONS) samples.push(sample.latency);
          run.samples(
            [
              {
                id,
                run: r,
                iteration: i,
                warmup: i < WARMUP_ITERATIONS ? 1 : 0,
                t_input: sample.t_input,
                t_paint: sample.t_paint,
                latency_ms: sample.latency,
                mutations: sample.mutations,
                predicate_evals: sample.predicateEvals,
              },
            ],
            [
              'id', 'run', 'iteration', 'warmup', 't_input', 't_paint',
              'latency_ms', 'mutations', 'predicate_evals',
            ],
          );
          await page.eval(`new Promise((r) => setTimeout(r, ${gap()}))`);
        }
        perRun.push(samples);
      }

      if (interaction.cpuThrottlingRate) {
        await page.send('Emulation.setCPUThrottlingRate', { rate: 1 });
      }

      byId.set(id, cell(perRun));
    }

    run.record({ cells: Object.fromEntries(byId) });
    run.close('completed');
    await page.close();
  } catch (err) {
    run.close('aborted', err.message);
    throw err;
  } finally {
    await browser.close();
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
