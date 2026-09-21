#!/usr/bin/env node
/*
 * The smoke check. NOT a measurement, and it is written so that it cannot
 * become one by accident.
 *
 *   node harness/smoke.mjs --app dashboard --origin http://127.0.0.1:3000
 *   node harness/smoke.mjs --app chat --stack gotth --origin http://127.0.0.1:8080
 *
 * What it does: opens ONE tab, waits for §3.3's ready signal, and drives each
 * of the app's interactions once, asserting that the paint predicate becomes
 * true. What it deliberately does not do: record a latency, write anything into
 * bench/data/, or run more than one tab. A duration IS produced by the shim —
 * awaitPaint resolves with one — and it is dropped here rather than printed,
 * because a number printed by a smoke run is a number somebody will quote.
 *
 * §12 and the Phase 3 sequencing are the reason. Phase 3's tuning is not
 * finished (Appendix B: QA3-1, QA3-2, QA3-3 are all still safety-chosen
 * defaults), two of the three move numbers this spec publishes, and a re-tune
 * landing after measurement has begun forces full re-collection under §12. So
 * there are no quotable measurements from this tree yet, and this file is the
 * one that would otherwise be the leak.
 */
import { NETWORK_PROFILES, launch, newPage } from './cdp.mjs';
import { runInteraction } from './input.mjs';
import { forApp } from './interactions/index.mjs';
import { STACKS } from './run.mjs';

/**
 * Why a row is not driven in a smoke tab, or null to drive it.
 *
 * Exported and pure so `node --test` can check the categorisation without a
 * browser: the two callers of a skip rule are this file and the suite that
 * proves it, and a rule only reachable by launching Chromium is a rule nobody
 * checks.
 *
 * `stack` is the stack the origin is serving, and it is a REQUIRED argument
 * rather than something sniffed from the page. A wrong guess here does not
 * report a wrong guess, it reports a pass — the whole risk of the nextOnly arm
 * below is that it silently swallows a real Next.js failure — so the harness is
 * told which stack it is pointed at, exactly as measure-*.mjs are told.
 */
export function skipReason(interaction, stack) {
  if (interaction.push || interaction.crossTab) {
    return 'push/cross-tab: needs the run driver, not a smoke tab';
  }
  /* AS-2: CHT-2b is the optimistic-send row and gotth-live has no optimistic UI
     by construction (BL-4), so its predicate cannot become true on this stack
     and the row times out BY DESIGN. Driving it here made `--app chat` exit
     non-zero against gotth-live for a reason that is not a defect.

     It is skipped only when the stack under test cannot express it. Skipping it
     on the Next.js side would hide a real Next.js regression in the one
     capability §2.3 exists to credit that stack with, which is a worse failure
     than the exit code this arm fixes. */
  if (interaction.nextOnly && stack !== 'next') {
    return `nextOnly: Next.js-only row (AS-2); the ${stack} column reads "no equivalent", never a failure`;
  }
  return null;
}

function parseArgs(argv) {
  const out = {};
  for (let i = 0; i < argv.length; i++) {
    if (!argv[i].startsWith('--')) continue;
    const key = argv[i].slice(2);
    const value = argv[i + 1] && !argv[i + 1].startsWith('--') ? argv[++i] : 'true';
    out[key] = value;
  }
  return out;
}

async function main() {
  const args = parseArgs(process.argv.slice(2));
  const app = args.app ?? 'counter';
  const origin = args.origin ?? 'http://127.0.0.1:3000';

  /* Defaulted to 'next' because every measure-*.mjs defaults `--stack` to
     'next', and a second convention for the same word would be the per-stack
     branch §3 forbids, wearing a different hat. An unrecognised value is
     refused rather than treated as "not gotth": a typo that silently ran the
     nextOnly rows against gotth-live would put the exit code back, and a typo
     that silently skipped them on Next.js would be worse. */
  const stack = args.stack ?? 'next';
  if (!STACKS.includes(stack)) {
    console.error(`--stack must be one of ${STACKS.join(', ')}; got ${JSON.stringify(stack)}`);
    process.exit(2);
  }

  const interactions = forApp(app);
  if (interactions.length === 0) {
    console.error(`no interactions registered for app ${JSON.stringify(app)}`);
    process.exit(2);
  }

  const route = interactions[0].route;
  const browser = await launch({
    extraFlags: args.spki ? [`--ignore-certificate-errors-spki-list=${args.spki}`] : [],
  });
  const version = await browser.version();
  console.log(`smoke: ${version.product} against ${origin}${route}  [stack: ${stack}]`);
  console.log('smoke: SINGLE TAB, NO TIMINGS RECORDED (construction only, spec §12 / Appendix B)\n');

  const page = await newPage(browser, { networkProfile: NETWORK_PROFILES.lan });
  const results = [];

  try {
    await page.clearCache();
    await page.goto(`${origin}${route}`);

    /* §3.3's ready signal, which the app sets exactly once and the shim stamps.
       Waiting on it rather than on a sleep is the whole point of the signal. */
    await page.eval(
      `Promise.race([
        window.__bench.whenReady(),
        new Promise((_, reject) => setTimeout(() => reject(new Error('window.__bench.ready never became true')), 20000)),
      ])`,
    );
    console.log('  ok   window.__bench.ready  (§3.3 hydration + channel open + first message applied)');

    for (const interaction of interactions) {
      const skip = skipReason(interaction, stack);
      if (skip) {
        console.log(`  skip ${interaction.id}  (${skip})`);
        results.push({ id: interaction.id, status: 'skipped' });
        continue;
      }

      try {
        if (interaction.setup) await interaction.setup(page, { origin, route });

        if (interaction.assert) {
          const verdict = await interaction.assert(page, { origin, route });
          console.log(
            `  ${verdict.pass ? 'ok  ' : 'FAIL'} ${interaction.id}  ${JSON.stringify(verdict)}`,
          );
          results.push({ id: interaction.id, status: verdict.pass ? 'pass' : 'fail' });
          continue;
        }

        if (!interaction.drive) {
          results.push({ id: interaction.id, status: 'skipped' });
          continue;
        }

        if (!interaction.predicate) {
          /* CHT-6 is the case: "scroll the list 20 screens | none | correctness
             /jank only". There is no paint to await, so it is driven and its
             completion is the assertion. The long-task trace §4.5 wants comes
             from the run driver, not from a smoke tab. */
          await interaction.drive(page, { origin, route });
          console.log(`  ok   ${interaction.id}  driven (no paint predicate: jank/correctness row)`);
          results.push({ id: interaction.id, status: 'pass' });
          continue;
        }

        /* The sample is taken and then thrown away, on purpose. */
        await runInteraction(page, interaction, { origin, route });
        console.log(`  ok   ${interaction.id}  paint predicate became true`);
        results.push({ id: interaction.id, status: 'pass' });
      } catch (err) {
        console.log(`  FAIL ${interaction.id}  ${err.message}`);
        results.push({ id: interaction.id, status: 'fail', error: err.message });
      }

      /* §5.7's inter-interaction gap is U(400, 600) ms in a real run; the smoke
         uses its midpoint, because a smoke check is not sampling anything. */
      await page.eval(`new Promise((r) => setTimeout(r, 500))`);
    }

    /* E5 — bounded DOM. A smoke run is the cheapest place to catch a document
       that has grown past the bound the spec sizes the comparison against. */
    const dom = await page.eval(`({
      elements: document.getElementsByTagName('*').length,
      svg: document.querySelectorAll('svg, svg *').length,
    })`);
    console.log(`\n  DOM  ${dom.elements} elements, ${dom.svg} inline SVG nodes (E5 bounds are per app, §2)`);
  } finally {
    await page.close();
    await browser.close();
  }

  const failed = results.filter((r) => r.status === 'fail');
  console.log(
    `\nsmoke: ${results.filter((r) => r.status === 'pass').length} passed, ` +
      `${failed.length} failed, ${results.filter((r) => r.status === 'skipped').length} skipped`,
  );
  process.exit(failed.length === 0 ? 0 : 1);
}

/* Guarded so the suite can import skipReason without launching Chromium — the
   idiom run.mjs already uses for a module that is also a script. */
if (import.meta.url === `file://${process.argv[1]}`) {
  main().catch((err) => {
    console.error(err);
    process.exit(1);
  });
}
