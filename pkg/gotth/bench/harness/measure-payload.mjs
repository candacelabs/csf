#!/usr/bin/env node
/*
 * D1 — client JS payload (§3.5, §4's D1 row).
 *
 * Two figures per FR-71.1: transfer bytes (Network.loadingFinished's
 * encodedDataLength, the bytes on the wire as served) and decoded bytes
 * (Network.getResponseBody length after decompression).
 *
 * Two windows per §3.5, and BOTH are reported:
 *   (a) navigation start -> t_ready (first interactive load)
 *   (b) navigation start -> completion of the app's full measured interaction
 *       set. This exists because Next.js code-splits and lazily fetches chunks
 *       on interaction; measuring only (a) would understate its payload, which
 *       is not a win we want to take (T-14).
 *
 * Accounting rules that are easy to get silently wrong, so they are here rather
 * than in a spreadsheet later:
 *
 *   - shim.js is subtracted from BOTH stacks, and the subtracted amount is
 *     stated (§2.0, §3.5). It is byte-identical on both sides, so leaving it in
 *     would inflate both figures by the same constant and flatter whichever
 *     stack has the larger total.
 *   - RSC / Flight payloads (text/x-component) are NOT JS and are NOT in the JS
 *     figure — but they are reported as their own line beside gotth-live's
 *     HTML-fragment wire bytes, because "excluding it silently would be a
 *     hidden asymmetry" (§3.5, T-15).
 *   - Total transfer bytes to t_ready, all content types, is reported as the
 *     un-gameable aggregate.
 */
import { NETWORK_PROFILES, launch, newPage } from './cdp.mjs';
import { runInteraction } from './input.mjs';
import { operatorInvoked, requireGates } from './gate.mjs';
import { forApp, INTERACTIONS } from './interactions/index.mjs';
import { BENCH_ROOT, openRun, sha256File } from './manifest.mjs';
import { join } from 'node:path';
import { statSync } from 'node:fs';

const JS_MIME = [
  'application/javascript',
  'text/javascript',
  'application/ecmascript',
  'application/x-javascript',
];

const FLIGHT_MIME = ['text/x-component'];

/** Collects §3.5's accounting from a page's whole network history. */
export function accountant(page) {
  const responses = new Map();
  const finished = [];

  page.on('Network.responseReceived', (e) => {
    responses.set(e.requestId, {
      url: e.response.url,
      mimeType: (e.response.mimeType ?? '').toLowerCase(),
      status: e.response.status,
      contentEncoding: e.response.headers?.['content-encoding'] ?? '',
    });
  });

  page.on('Network.loadingFinished', (e) => {
    const meta = responses.get(e.requestId);
    if (!meta) return;
    finished.push({
      ...meta,
      requestId: e.requestId,
      encodedDataLength: e.encodedDataLength,
      at: e.timestamp,
    });
  });

  return {
    all: () => finished,
    /** Decoded lengths need a round trip per response, so they are fetched on
     *  demand at the end of a window rather than per response. */
    async decode(page) {
      for (const entry of finished) {
        if (entry.decodedLength !== undefined) continue;
        try {
          const body = await page.send('Network.getResponseBody', { requestId: entry.requestId });
          entry.decodedLength = body.base64Encoded
            ? Buffer.from(body.body, 'base64').length
            : Buffer.byteLength(body.body, 'utf8');
        } catch {
          /* The body was evicted from the CDP buffer. Recorded as null rather
             than guessed; a decoded figure that is partly estimated is not a
             figure §3.5 permits. */
          entry.decodedLength = null;
        }
      }
      return finished;
    },
    summarize(shimBytes) {
      const js = finished.filter((e) => JS_MIME.includes(e.mimeType));
      const flight = finished.filter((e) => FLIGHT_MIME.includes(e.mimeType));
      const shim = js.filter((e) => e.url.endsWith('/bench/shim.js'));
      const sum = (list, key) => list.reduce((n, e) => n + (e[key] ?? 0), 0);
      return {
        js: {
          requests: js.length,
          transferBytes: sum(js, 'encodedDataLength'),
          decodedBytes: sum(js, 'decodedLength'),
          /* §3.5: subtracted from both, with the subtracted amount stated. */
          shimTransferBytes: sum(shim, 'encodedDataLength'),
          shimSourceBytes: shimBytes,
          transferBytesExShim: sum(js, 'encodedDataLength') - sum(shim, 'encodedDataLength'),
        },
        /* Its own line, never folded into the JS figure (T-15). */
        flight: {
          requests: flight.length,
          transferBytes: sum(flight, 'encodedDataLength'),
          decodedBytes: sum(flight, 'decodedLength'),
        },
        /* The un-gameable aggregate (§3.5). */
        total: {
          requests: finished.length,
          transferBytes: sum(finished, 'encodedDataLength'),
        },
        encodings: [...new Set(finished.map((e) => e.contentEncoding).filter(Boolean))],
      };
    },
  };
}

async function main() {
  const args = parse(process.argv.slice(2));
  requireGates(operatorInvoked() ? ['driverValidation', 'conformance', 'phase3'] : undefined);

  const app = args.app ?? 'dashboard';
  const origin = args.origin ?? 'https://127.0.0.1:18443';
  const interactions = forApp(app).filter((i) => i.drive);
  const route = interactions[0].route;
  const shimPath = join(BENCH_ROOT, 'harness', 'shim.js');

  const run = openRun({
    dimension: 'D1',
    stack: args.stack ?? 'next',
    app,
    variant: args.variant ?? 'sse',
    networkProfile: 'lan',
  });

  const browser = await launch({
    extraFlags: args.spki ? [`--ignore-certificate-errors-spki-list=${args.spki}`] : [],
  });

  try {
    const loads = [];
    /* §4's D1 sample plan: 20 loads per app per stack, cold context each. */
    for (let i = 0; i < 20; i++) {
      const page = await newPage(browser, { networkProfile: NETWORK_PROFILES.lan });
      const acc = accountant(page);
      await page.clearCache();
      await page.goto(`${origin}${route}`);
      await page.eval(`window.__bench.whenReady()`);

      await acc.decode(page);
      const atReady = acc.summarize(statSync(shimPath).size);

      /* Window (b): through the full measured interaction set. */
      for (const interaction of interactions) {
        if (interaction.setup) await interaction.setup(page, { origin, route });
        if (interaction.predicate) await runInteraction(page, interaction, { origin, route });
        else await interaction.drive(page, { origin, route });
      }
      await acc.decode(page);
      const afterInteractions = acc.summarize(statSync(shimPath).size);

      loads.push({ load: i, atReady, afterInteractions });
      await page.close();
    }

    run.record({
      shimSha256: sha256File(shimPath),
      loads,
      note:
        'Both §3.5 windows are reported. shim.js bytes are subtracted from both ' +
        'stacks and the subtracted amount is stated. RSC/Flight bytes are a ' +
        'separate line and are never folded into the JS figure.',
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
