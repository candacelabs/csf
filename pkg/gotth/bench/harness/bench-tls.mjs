/*
 * The bench proxy's certificate, trusted by the NODE half of the harness.
 *
 * §5.3 puts a locally generated self-signed certificate on the proxy and says it
 * is "trusted only by the harness". The BROWSER half has always honoured that:
 * every entry point that launches Chromium passes
 * `--ignore-certificate-errors-spki-list=$(cat docker/tls/bench.spki)`, so the
 * browser trusts exactly that one key and nothing else. The node half did not,
 * and the gap was invisible because it was split down the middle:
 *
 *   - `driver.mjs`'s two WebSocket constructions passed `rejectUnauthorized:
 *     false`, so they connected — with verification switched OFF;
 *   - every `fetch` beside them passed nothing at all, so they threw
 *     `DEPTH_ZERO_SELF_SIGNED_CERT` against the very same origin.
 *
 * Five call sites were on the failing side of that split: `GotthSession`'s and
 * `NextSession`'s document fetches, the SSE channel, the poll channel, and
 * §3.6's warm-up in `measure-memory.mjs`. Since `validate-driver.mjs` defaults
 * its origin to `https://127.0.0.1:18443` and runs the warm-up first, §3.6's
 * driver-validation gate would have thrown on its first HTTP request — the gate
 * that unblocks every 1k cell, dead on arrival, discoverable only in the one
 * window it can run in (a host with no GPU streaming session, Q-7).
 *
 * -----------------------------------------------------------------------------
 * What this does, and why it is not the obvious thing
 *
 * It ADDS the bench certificate to the process's default CA store, and changes
 * nothing else. Certificate verification stays on, hostname verification stays
 * on, the stock public roots stay in the store, and the only certificate that
 * newly verifies is the one `docker/gen-cert.sh` generated on this host. A
 * connection to the proxy under a name the certificate does not cover still
 * fails with `ERR_TLS_CERT_ALTNAME_INVALID`, which is the property that makes
 * this evidence rather than a bypass.
 *
 * Three alternatives were available and each is worse:
 *
 *   `rejectUnauthorized: false` per socket — what the two WebSockets already
 *       did. It connects to whatever answers. On a bench that exists to produce
 *       defensible numbers, "the driver talked to something on port 18443" is a
 *       weaker statement than "the driver talked to the proxy this tree
 *       generated a key for", and the two cost the same to write.
 *
 *   `NODE_TLS_REJECT_UNAUTHORIZED=0` — disables verification for every socket
 *       in the process, is set from outside the code that needs it, and is
 *       exactly the kind of environment variable that gets exported in a shell
 *       once and silently survives into a measured run. It is also what this
 *       defect was originally diagnosed with, which is the whole argument
 *       against shipping it.
 *
 *   undici's per-request `Agent` with `connect: { ca }` — the shape
 *       `GotthSession`'s constructor was written for, with an accepted-and-
 *       stored `dispatcher` field it never passed to anything. `undici` is not
 *       resolvable in this tree (`bench/node_modules` carries `undici-types`,
 *       which is types only), so taking it means a new runtime dependency and a
 *       `package-lock.json` change to make a bench harness trust a local
 *       certificate the platform can already be told about. `tls.setDefault-
 *       CACertificates()` is builtin from Node 22.15, this tree pins Node
 *       24.19.0, and `fetch`, `ws` and `tls.connect` all consult that one
 *       store — so a single call covers all five sites instead of five call
 *       sites each having to remember. The dead `dispatcher` field is removed
 *       rather than left looking like it works, because a parameter that is
 *       accepted, stored and never read is how this bug looked from the inside.
 *
 * -----------------------------------------------------------------------------
 * The SPKI cross-check
 *
 * `gen-cert.sh` writes `bench.crt` and `bench.spki` together, and the browser
 * trusts the run by the pin in the second file while the node half now trusts it
 * by the key in the first. Those are two files that can drift — regenerate the
 * certificate without re-reading the pin and the browser and the driver are
 * trusting different servers, on the same run, silently. So the pin is
 * recomputed from the certificate here and a mismatch is a startup failure. It
 * costs one hash and it makes "both halves of the harness trust the same
 * certificate" checkable instead of assumed.
 */
import { X509Certificate, createHash } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import tls from 'node:tls';
import { fileURLToPath } from 'node:url';

const HERE = dirname(fileURLToPath(import.meta.url));

export const DEFAULT_CERT_PATH = join(HERE, '..', 'docker', 'tls', 'bench.crt');
export const DEFAULT_SPKI_PATH = join(HERE, '..', 'docker', 'tls', 'bench.spki');

/**
 * Where the certificate is, with `BENCH_PROXY_CERT` / `BENCH_PROXY_SPKI` able to
 * move it.
 *
 * Read at call time rather than at import, which is what lets the suite point
 * the driver at a throwaway certificate and a real one-off HTTPS listener and
 * check that the driver installs its own anchor. That is the property this
 * module exists for and the one an options-shape assertion would not have
 * caught: the old code also worked if something else had already arranged
 * trust, so a test that arranges trust first tests nothing.
 */
export function certPaths() {
  return {
    certPath: process.env.BENCH_PROXY_CERT || DEFAULT_CERT_PATH,
    spkiPath: process.env.BENCH_PROXY_SPKI || DEFAULT_SPKI_PATH,
  };
}

/**
 * The SPKI pin, computed exactly as `docker/gen-cert.sh` computes it:
 * SHA-256 over the DER SubjectPublicKeyInfo, base64. The shell pipeline there is
 * `openssl x509 -pubkey | openssl pkey -pubin -outform der | openssl dgst
 * -sha256 -binary | openssl enc -base64`; this is the same four steps in one
 * expression, which is what lets the two be compared rather than trusted.
 */
export function spkiPin(pem) {
  const der = new X509Certificate(pem).publicKey.export({ type: 'spki', format: 'der' });
  return createHash('sha256').update(der).digest('base64');
}

/* Which certificate files have already been added, so this is idempotent
   without being once-only: a test needs to add a throwaway anchor after the
   bench one, and a driver that opens a thousand sessions must not add the same
   anchor a thousand times. */
const anchored = new Set();

/**
 * Add one PEM to the process's default CA store, additively.
 *
 * `tls.setDefaultCACertificates` REPLACES the store, so the current contents are
 * read back and passed through. Dropping them would narrow this process to
 * trusting the bench proxy alone — which would happen to be harmless here and
 * would be a surprising thing to have done to a process that may later fetch
 * something else.
 */
export function trustAnchor(pem) {
  const before = tls.getCACertificates('default');
  tls.setDefaultCACertificates([...before, pem]);
  return { before: before.length, after: tls.getCACertificates('default').length };
}

/**
 * Trust the bench proxy for this process, if the origin is one that needs it.
 *
 * Returns a record rather than nothing, so a caller that wants to put "the
 * driver trusted this certificate" in a manifest can, and so the tests can
 * assert on what happened instead of on what was logged.
 *
 * A plaintext origin is a no-op: the apps serve plaintext HTTP directly in
 * development (bench/README.md, "Building and running"), and requiring a
 * certificate to exist before `http://127.0.0.1:3000` can be driven would be a
 * new failure in exchange for nothing.
 *
 * An https origin with no certificate on disk THROWS, and deliberately. That is
 * the §3.6 topology with its `docker/gen-cert.sh` step skipped, and the failure
 * a reader wants there is the one that names the missing step — not a
 * `DEPTH_ZERO_SELF_SIGNED_CERT` forty lines into a session pool.
 */
export function ensureBenchTrust(origin, overrides = {}) {
  const { certPath, spkiPath } = { ...certPaths(), ...overrides };

  if (typeof origin !== 'string' || !origin.startsWith('https:')) {
    return { applied: false, reason: 'origin is not https; nothing to trust' };
  }

  if (anchored.has(certPath)) {
    return { applied: false, reason: 'already trusted in this process', certPath };
  }

  if (!existsSync(certPath)) {
    throw new Error(
      `the bench proxy's certificate is not at ${certPath}, and the origin ${origin} is ` +
        'https. §5.3 generates it locally and commits the script rather than the key ' +
        '(bench/README.md deviation D-2): run `sh docker/gen-cert.sh` from bench/. The ' +
        'driver will not fall back to an unverified connection — a measured run that ' +
        'trusted whatever answered on that port is not the §3.6 topology.',
    );
  }

  const pem = readFileSync(certPath, 'utf8');
  const pin = spkiPin(pem);

  /* The browser trusts this run by the pin in bench.spki. If the two files
     disagree, the two halves of the harness are trusting different servers. */
  if (existsSync(spkiPath)) {
    const recorded = readFileSync(spkiPath, 'utf8').trim();
    if (recorded !== pin) {
      throw new Error(
        `${spkiPath} records SPKI pin ${recorded}, but ${certPath} has ${pin}. These are ` +
          'written together by docker/gen-cert.sh, so a mismatch means the certificate ' +
          'was regenerated and the pin was not re-read — the browser half of the harness ' +
          '(--ignore-certificate-errors-spki-list) and the node half would be trusting ' +
          'different certificates on the same run. Re-run `sh docker/gen-cert.sh`.',
      );
    }
  }

  const store = trustAnchor(pem);
  anchored.add(certPath);
  return { applied: true, certPath, spkiPin: pin, store };
}
