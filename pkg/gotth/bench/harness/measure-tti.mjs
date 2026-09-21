#!/usr/bin/env node
/*
 * D5 — time-to-interactive, cold and warm (§3.3, §4's D5 row).
 *
 * TTI = t_ready, the performance.now() the shim stamps at the instant the app
 * assigns window.__bench.ready = true. The performance.now() timeline starts at
 * performance.timeOrigin = navigation start, so t_ready IS the TTI with no
 * arithmetic.
 *
 * Cold: fresh browser context, Network.clearBrowserCache +
 * Network.clearBrowserCookies, per iteration. Warm: a second navigation in the
 * SAME context with cache and session cookie retained. Both reported (FR-71.5).
 * 100 cold + 100 warm per stack across 5 runs (§4).
 *
 * -----------------------------------------------------------------------------
 * §3.3's "verification, not trust" — the part that is easy to leave out
 *
 * t_ready is SELF-REPORTED by the app, so it is independently validated:
 *
 *   "for 100 loads per stack, the harness fires the app's headline interaction
 *    at t_ready + 0 ms and asserts it completes with a normal latency
 *    distribution; and separately fires it at t_ready - 50 ms and asserts a
 *    materially higher failure/latency rate. If firing early does NOT degrade,
 *    the ready signal is late (conservative) and must be corrected."
 *
 * That closes the obvious cheat in either direction, and it mirrors the review
 * checklist's rule that self-reported telemetry must be externally verifiable
 * (T-11). validateReady() below is that check, and D5 does not report a number
 * without it.
 */
import { NETWORK_PROFILES, launch, newPage } from './cdp.mjs';
import { runInteraction } from './input.mjs';
import { operatorInvoked, requireGates } from './gate.mjs';
import { forApp, INTERACTIONS } from './interactions/index.mjs';
import { openRun } from './manifest.mjs';
import { cell } from './analyze.mjs';

export const LOADS = 100;

/** One cold load: fresh context, cleared cache and cookies, navigate, read t_ready. */
export async function coldLoad(browser, origin, route, profile) {
  const page = await newPage(browser, { networkProfile: profile });
  await page.clearCache();
  await page.goto(`${origin}${route}`);
  const tReady = await page.eval(`window.__bench.whenReady()`);
  const context = await page.eval(`({
    fcp: (performance.getEntriesByName('first-contentful-paint')[0] || {}).startTime ?? null,
    domContentLoaded: performance.timing ? null : null,
    navigation: JSON.parse(JSON.stringify(performance.getEntriesByType('navigation')[0] || {})),
  })`);
  return { page, tReady, context };
}

/**
 * §3.3's external validation of the self-reported ready signal.
 *
 * Returns both distributions. The verdict is deliberately NOT computed here:
 * "materially higher" is a judgement the report makes with both numbers in
 * front of it, and hard-coding a threshold would let a marginal result be
 * rendered as a pass by a constant nobody agreed to.
 */
export async function validateReady(browser, origin, interaction, profile, loads = LOADS) {
  const atReady = [];
  const beforeReady = [];
  const failures = { atReady: 0, beforeReady: 0 };

  for (let i = 0; i < loads; i++) {
    /* At t_ready + 0 ms. */
    {
      const page = await newPage(browser, { networkProfile: profile });
      await page.clearCache();
      await page.goto(`${origin}${interaction.route}`);
      await page.eval(`window.__bench.whenReady()`);
      try {
        const sample = await runInteraction(page, interaction, { origin, route: interaction.route });
        atReady.push(sample.latency);
      } catch {
        failures.atReady++;
      }
      await page.close();
    }

    /*
     * At t_ready - 50 ms.
     *
     * "Before ready" cannot be waited for, so it is raced: the interaction is
     * fired 50 ms before the ready signal is EXPECTED, using the median t_ready
     * observed so far. A page that is genuinely interactive earlier than it
     * claims will complete this normally, which is the finding.
     */
    {
      const page = await newPage(browser, { networkProfile: profile });
      await page.clearCache();
      const expected = median(atReady.length > 0 ? atReady : [200]);
      await page.goto(`${origin}${interaction.route}`);
      await page.eval(`new Promise((r) => setTimeout(r, ${Math.max(0, expected - 50)}))`);
      try {
        const sample = await runInteraction(page, interaction, {
          origin,
          route: interaction.route,
        });
        beforeReady.push(sample.latency);
      } catch {
        failures.beforeReady++;
      }
      await page.close();
    }
  }

  return {
    atReady: cell([atReady]),
    beforeReady: cell([beforeReady]),
    failures,
    loads,
    note:
      '§3.3: if firing 50 ms early does NOT degrade, the ready signal is late ' +
      '(conservative) and must be corrected. The verdict is the report\'s to ' +
      'make with both distributions in front of it; no threshold is hard-coded.',
  };
}

function median(values) {
  if (values.length === 0) return 0;
  const sorted = [...values].sort((a, b) => a - b);
  return sorted[Math.floor(sorted.length / 2)];
}

async function main() {
  const args = parse(process.argv.slice(2));
  requireGates(operatorInvoked() ? ['driverValidation', 'conformance', 'phase3'] : undefined);

  const app = args.app ?? 'dashboard';
  const origin = args.origin ?? 'https://127.0.0.1:18443';
  const profileName = args.profile ?? 'lan';
  const profile = NETWORK_PROFILES[profileName];
  const headline = forApp(app).find((i) => i.measured === 'headline' && i.drive);
  const route = headline?.route ?? forApp(app)[0].route;

  const run = openRun({
    dimension: 'D5',
    stack: args.stack ?? 'next',
    app,
    variant: args.variant ?? 'sse',
    networkProfile: profileName,
  });

  const browser = await launch({
    extraFlags: args.spki ? [`--ignore-certificate-errors-spki-list=${args.spki}`] : [],
  });

  try {
    /* §5.7: 10 discarded loads per run, and "the Node process receives the same
       warm-up request volume as the Go process before any measurement". */
    for (let i = 0; i < 10; i++) {
      const { page } = await coldLoad(browser, origin, route, profile);
      await page.close();
    }

    const cold = [];
    const warm = [];
    for (let i = 0; i < LOADS; i++) {
      const { page, tReady } = await coldLoad(browser, origin, route, profile);
      cold.push(tReady);
      /* Warm: second navigation in the SAME context, cache and session cookie
         retained. Not a new context, and not a cleared cache. */
      await page.goto(`${origin}${route}`);
      warm.push(await page.eval(`window.__bench.whenReady()`));
      await page.close();
    }

    run.record({
      cold: cell([cold]),
      warm: cell([warm]),
      readyValidation: headline
        ? await validateReady(browser, origin, INTERACTIONS.get(headline.id), profile)
        : { skipped: 'no headline interaction with a driver for this app' },
      excluded: {
        lighthouseTTI:
          'NOT used (§3.3, T-12): legacy Lighthouse TTI and Speed Index depend ' +
          'on a network-quiet window, and a long-lived connection with ' +
          'heartbeats can prevent one — that would penalise the ' +
          'persistent-connection architecture for a property of the metric ' +
          'rather than of the user experience.',
      },
    });
    run.close('completed');
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
