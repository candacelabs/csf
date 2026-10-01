#!/usr/bin/env node
/*
 * Start one bench app's PRODUCTION standalone server (§5.1), and the WebSocket
 * sidecar when the build is §5.4's secondary variant.
 *
 *   node scripts/start-app.mjs <app-dir>
 *
 * One script for all three apps, because three copies of this would drift and
 * a drift here is a difference in how one app is served — which is exactly the
 * kind of undeclared asymmetry E6 exists to forbid.
 *
 * -----------------------------------------------------------------------------
 * What this script guarantees, and why each guarantee is here
 *
 *   - NODE_ENV=production, always. §5.1: "NODE_ENV=production; React production
 *     build; no `next dev`". A standalone server started with anything else is
 *     not the configuration the spec pins, and the audit's production-React
 *     check would fail on it.
 *
 *   - PLAINTEXT ONLY. §3.6's TLS boundary is binding on both stacks: the
 *     measured container serves plaintext HTTP/WebSocket on its container port
 *     and TLS is terminated in the shared proxy container. This process holds
 *     no key, no certificate, and no TLS listener; harness/assert-no-tls.mjs
 *     proves that from outside before any D3 cell is recorded, and this script
 *     prints the same fact into its own log so the two agree.
 *
 *   - The standalone tree is completed before launch. `next build` writes
 *     .next/standalone WITHOUT public/ and WITHOUT .next/static (documented
 *     Next.js behaviour: those are expected to be served by a CDN). We are not
 *     a CDN, so they are copied in. Missing static chunks would show up as a
 *     Next.js hydration failure and be measured as a Next.js loss.
 */
import { spawn } from 'node:child_process';
import { cpSync, existsSync, readFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const benchRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const appDir = resolve(process.argv[2] ?? '.');

const pkg = JSON.parse(readFileSync(join(appDir, 'package.json'), 'utf8'));

const PORT = process.env.PORT ?? '3000';
/*
 * BENCH_HOST, not HOSTNAME.
 *
 * Next's standalone server binds process.env.HOSTNAME, and Docker sets
 * HOSTNAME to the container id in every container it starts. Honouring it
 * would bind the server to the container's own name — reachable from the proxy
 * but not from 127.0.0.1 inside the container, which is where the ws sidecar's
 * upstream and every health check look. The bind address is therefore an
 * explicit bench variable with an explicit default, and Docker's HOSTNAME is
 * deliberately not consulted.
 */
const HOSTNAME = process.env.BENCH_HOST ?? '0.0.0.0';
const VARIANT = process.env.BENCH_VARIANT ?? 'sse';

if (!['sse', 'ws', 'poll'].includes(VARIANT)) {
  console.error(`BENCH_VARIANT must be sse | ws | poll, got ${JSON.stringify(VARIANT)}`);
  process.exit(2);
}

/*
 * The standalone entrypoint sits at the app's path RELATIVE TO
 * outputFileTracingRoot, which next.config.ts pins to the bench root so the
 * hoisted workspace node_modules is traced. That makes the path
 * .next/standalone/apps/<app>/next/server.js rather than
 * .next/standalone/server.js.
 */
const rel = appDir.startsWith(benchRoot) ? appDir.slice(benchRoot.length + 1) : '';
const unwrapped = join(appDir, 'server.js');
const inTree = join(appDir, '.next', 'standalone', rel, 'server.js');

/*
 * Two layouts, one launcher.
 *
 * In a checkout, `next build` leaves the traced tree under the app's own
 * .next/standalone/<rel>/, and public/ and .next/static have to be copied in —
 * documented Next.js behaviour, since those are expected to be served by a CDN
 * and we are not a CDN.
 *
 * In the container (docker/next.Dockerfile) that tree has already been
 * unwrapped to the image root and the two directories are already beside it, so
 * server.js sits directly in the app directory and there is nothing to copy.
 * Detecting which one we are in beats maintaining two entrypoints that can
 * disagree about what "production" means.
 */
let entry;
if (existsSync(unwrapped)) {
  entry = unwrapped;
} else if (existsSync(inTree)) {
  entry = inTree;
  const standaloneApp = join(appDir, '.next', 'standalone', rel);
  for (const [from, to] of [
    [join(appDir, 'public'), join(standaloneApp, 'public')],
    [join(appDir, '.next', 'static'), join(standaloneApp, '.next', 'static')],
  ]) {
    if (existsSync(from)) cpSync(from, to, { recursive: true });
  }
} else {
  console.error(
    `no standalone build at ${unwrapped} or ${inTree}\n` +
      `run \`npm run build -w ${pkg.name}\` first (bench/README.md, "Building an app")`,
  );
  process.exit(2);
}

const children = [];

function launch(name, file, env) {
  const child = spawn(process.execPath, [file], {
    stdio: 'inherit',
    /*
     * NEXT_TELEMETRY_DISABLED: the measured container makes no network call it
     * was not asked to make. Next's anonymous telemetry is off in every bench
     * script for that reason, not because of what it reports.
     */
    env: { ...process.env, ...env, NODE_ENV: 'production', NEXT_TELEMETRY_DISABLED: '1' },
  });
  child.on('exit', (code, signal) => {
    console.error(`bench: ${name} exited code=${code} signal=${signal}`);
    shutdown(code ?? 1);
  });
  children.push(child);
  return child;
}

let shuttingDown = false;
function shutdown(code) {
  if (shuttingDown) return;
  shuttingDown = true;
  for (const child of children) {
    if (child.exitCode === null) child.kill('SIGTERM');
  }
  setTimeout(() => process.exit(code), 500).unref();
}

for (const signal of ['SIGTERM', 'SIGINT']) {
  process.on(signal, () => shutdown(0));
}

console.log(
  `bench: ${pkg.name} variant=${VARIANT} listening http://${HOSTNAME}:${PORT} ` +
    `tls=none (§3.6 boundary: TLS terminated in the proxy container)`,
);

/*
 * §2.5's committed fixture. Both servers read the same bytes; the path is
 * explicit so a wrong tree fails loudly at start rather than replaying nothing.
 */
const FIXTURE_DIR = process.env.BENCH_FIXTURE_DIR ?? join(benchRoot, 'fixtures');

launch('next', entry, { PORT, HOSTNAME, BENCH_FIXTURE_DIR: FIXTURE_DIR });

if (VARIANT === 'ws') {
  const relay = join(appDir, 'ws-server', 'relay.mjs');
  if (!existsSync(relay)) {
    console.error(`BENCH_VARIANT=ws but ${relay} does not exist`);
    process.exit(2);
  }
  /*
   * Second process, SAME container (§5.4). §3.6: "whatever processes the
   * idiomatic architecture requires are inside that one container and are all
   * counted" — this process's RSS is part of the ws variant's memory number.
   */
  launch('ws', relay, {
    BENCH_WS_PORT: process.env.BENCH_WS_PORT ?? '3101',
    BENCH_UPSTREAM: process.env.BENCH_UPSTREAM ?? `http://127.0.0.1:${PORT}`,
  });
}
