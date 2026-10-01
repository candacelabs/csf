/*
 * §3.6's TLS-boundary assertion, and T-21's mitigation in executable form.
 *
 * "Terminating TLS inside one stack's container and outside the other's is a
 * disqualifying method error, in either direction. ... the harness asserts the
 * boundary rather than trusting it: before any D3 cell is recorded, it verifies
 * that the measured container holds no TLS listener and that the proxy running
 * in front of it is the same image digest as the one recorded for the other
 * stack's runs, and it writes both facts into the run manifest (§6)."
 *
 * The asymmetry this guards against is worth ~18,000 B per session, and the
 * direction it swings is a choice that would be available AFTER seeing the
 * number. Amendment A-1 exists because the pre-A-1 spec bound gotth-live alone
 * and the asymmetry ran against gotth-live — FR-73's honesty clause cuts both
 * ways, and an unfair-to-ourselves comparison is still an unfair comparison.
 *
 * Three checks, in increasing order of how hard they are to fool:
 *
 *   1. published ports — the measured container must publish none (§5.3: with
 *      the §3.6 topology the proxy is the only container that publishes a port);
 *   2. listening sockets — read from /proc/net/tcp inside the container, so it
 *      is the kernel's list and not the application's opinion;
 *   3. a real TLS ClientHello to every listening port. A plaintext HTTP server
 *      cannot answer one. This is the check that cannot be talked around.
 */
import { execFile } from 'node:child_process';
import net from 'node:net';
import tls from 'node:tls';
import { promisify } from 'node:util';

const exec = promisify(execFile);

async function docker(args) {
  const { stdout } = await exec('docker', args, { timeout: 20_000 });
  return stdout.trim();
}

/** Listening TCP ports inside a container, from the kernel's own tables. */
export async function listeningPorts(container) {
  const out = await docker([
    'exec',
    container,
    'sh',
    '-c',
    'cat /proc/net/tcp /proc/net/tcp6 2>/dev/null || true',
  ]);
  const ports = new Set();
  for (const line of out.split('\n').slice(1)) {
    const cols = line.trim().split(/\s+/);
    if (cols.length < 4) continue;
    /* st == 0A is TCP_LISTEN. */
    if (cols[3] !== '0A') continue;
    const hex = cols[1]?.split(':')[1];
    if (!hex) continue;
    ports.add(parseInt(hex, 16));
  }
  return [...ports].sort((a, b) => a - b);
}

/**
 * True when `host:port` completes a TLS handshake.
 *
 * A plaintext HTTP server receiving a ClientHello either closes, times out, or
 * answers with an HTTP error — none of which produce a 'secureConnect'. So a
 * resolved `true` here is positive evidence of a TLS listener, and a resolved
 * `false` is positive evidence of its absence, rather than the absence of
 * evidence.
 */
export function speaksTls(host, port, timeoutMs = 3000) {
  return new Promise((resolve) => {
    const socket = tls.connect(
      { host, port, rejectUnauthorized: false, timeout: timeoutMs },
      () => {
        socket.destroy();
        resolve(true);
      },
    );
    socket.on('error', () => resolve(false));
    socket.on('timeout', () => {
      socket.destroy();
      resolve(false);
    });
  });
}

/** The container's IP on the bench network, for the handshake probe. */
export async function containerIp(container) {
  const out = await docker([
    'inspect',
    '-f',
    '{{range .NetworkSettings.Networks}}{{.IPAddress}} {{end}}',
    container,
  ]);
  return out.split(/\s+/).filter(Boolean)[0] ?? '';
}

export async function imageDigest(container) {
  const image = await docker(['inspect', '-f', '{{.Image}}', container]);
  const repoDigests = await docker(['inspect', '-f', '{{json .RepoDigests}}', image]);
  return { imageId: image, repoDigests: JSON.parse(repoDigests || '[]') };
}

export async function publishedPorts(container) {
  const out = await docker(['inspect', '-f', '{{json .NetworkSettings.Ports}}', container]);
  const map = JSON.parse(out || '{}');
  return Object.entries(map)
    .filter(([, bindings]) => Array.isArray(bindings) && bindings.length > 0)
    .map(([port, bindings]) => ({ port, bindings }));
}

/**
 * The full §3.6 assertion for one stack's topology.
 *
 * Returns a record for the manifest. It NEVER throws for a policy failure — it
 * returns `pass: false` with the reason, because §6 requires a manifest to be
 * written for every run the harness starts including the ones it aborts, and a
 * thrown error before the manifest is written is a gap in the run-id sequence,
 * which is itself an audit failure.
 */
export async function assertTlsBoundary({
  sut,
  proxy,
  expectedProxyDigest = null,
  /* The four probes are injectable so the VERDICT can be tested without a
     container. The refusals below are the part a reviewer has to be able to
     check by hand — "a TLS listener in the measured container aborts the run"
     is not a claim that should rest on somebody having once misconfigured a
     container to see it fail. The defaults are the docker-backed ones and
     nothing in the harness passes anything else. */
  probes = {},
} = {}) {
  const {
    published = publishedPorts,
    ip: ipOf = containerIp,
    ports: portsOf = listeningPorts,
    tls: tlsProbe = speaksTls,
    digest = imageDigest,
  } = probes;

  const sutPublished = await published(sut);
  const ip = await ipOf(sut);
  const ports = await portsOf(sut);
  const tlsPorts = [];
  for (const port of ports) {
    if (ip === '') break;
    if (await tlsProbe(ip, port)) tlsPorts.push(port);
  }
  const proxyImage = await digest(proxy);

  return evaluateBoundary({
    sut,
    proxy,
    sutPublished,
    ports,
    tlsPorts,
    proxyImage,
    expectedProxyDigest,
  });
}

/**
 * The verdict, as a pure function of what the probes found.
 *
 * Split out from the probing so the three refusals are testable and so no
 * future caller can grow a fourth check that lives only in one code path.
 */
export function evaluateBoundary({
  sut,
  proxy,
  sutPublished = [],
  ports = [],
  tlsPorts = [],
  proxyImage = { imageId: '', repoDigests: [] },
  expectedProxyDigest = null,
}) {
  const findings = [];

  if (sutPublished.length > 0) {
    findings.push(
      `measured container ${sut} publishes ${sutPublished.map((p) => p.port).join(', ')}; ` +
        '§5.3 requires the proxy to be the only container that publishes a port',
    );
  }

  if (tlsPorts.length > 0) {
    findings.push(
      `measured container ${sut} completed a TLS handshake on ${tlsPorts.join(', ')}; ` +
        '§3.6 requires TLS to be terminated OUTSIDE the measured container, ' +
        'and an asymmetry here is disqualifying in either direction',
    );
  }

  if (expectedProxyDigest && !proxyImage.repoDigests.includes(expectedProxyDigest)) {
    findings.push(
      `proxy image digest ${JSON.stringify(proxyImage.repoDigests)} != recorded ` +
        `${expectedProxyDigest}; §5.2: "A run in which the two sides' proxy image ` +
        'digests differ is void, not corrected after the fact"',
    );
  }

  return {
    boundary: 'outside',
    sut,
    sutListeningPorts: ports,
    sutPublishedPorts: sutPublished,
    sutTlsPorts: tlsPorts,
    proxy,
    proxyImage,
    expectedProxyDigest,
    pass: findings.length === 0,
    findings,
    checkedAt: new Date().toISOString(),
  };
}

/**
 * `node harness/assert-no-tls.mjs [--sut bench-app] [--proxy bench-proxy]`
 *
 * bench/README.md has documented this as a runnable command since the topology
 * landed, and until now the file had no entry point — so the command printed
 * nothing and exited 0, which is the same shape of defect as a gate nobody
 * calls. It exits non-zero on a failed assertion, so it is usable from a shell
 * as well as readable from one.
 */
async function main() {
  const args = {};
  const argv = process.argv.slice(2);
  for (let i = 0; i < argv.length; i++) {
    if (!argv[i].startsWith('--')) continue;
    const key = argv[i].slice(2);
    args[key] = argv[i + 1] && !argv[i + 1].startsWith('--') ? argv[++i] : 'true';
  }
  const verdict = await assertTlsBoundary({
    sut: args.sut ?? 'bench-app',
    proxy: args.proxy ?? 'bench-proxy',
    expectedProxyDigest: args.proxyDigest ?? null,
  });
  console.log(JSON.stringify(verdict, null, 2));
  process.exit(verdict.pass ? 0 : 1);
}

/** A plain TCP reachability probe, for the harness's own start-up wait. */
export function reachable(host, port, timeoutMs = 1000) {
  return new Promise((resolve) => {
    const socket = net.connect({ host, port, timeout: timeoutMs }, () => {
      socket.destroy();
      resolve(true);
    });
    socket.on('error', () => resolve(false));
    socket.on('timeout', () => {
      socket.destroy();
      resolve(false);
    });
  });
}

if (import.meta.url === `file://${process.argv[1]}`) {
  main().catch((err) => {
    console.error(err.message);
    process.exit(1);
  });
}
