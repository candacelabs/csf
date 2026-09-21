/*
 * node --test. The node half of the harness against a REAL self-signed TLS
 * listener.
 *
 *   node --test harness/bench-tls.test.mjs
 *
 * -----------------------------------------------------------------------------
 * Why this file exists, and what shape of test would not have worked
 *
 * The defect it locks down: `GotthSession.fetchDocument()`, `NextSession`'s
 * document/SSE/poll fetches and §3.6's `warmUp()` all called a bare global
 * `fetch` against an origin defaulting to `https://127.0.0.1:18443`, where the
 * §5.3 proxy holds a self-signed certificate. Every one of them threw
 * `DEPTH_ZERO_SELF_SIGNED_CERT`. The two WebSockets beside them passed
 * `rejectUnauthorized: false` and connected, which is what kept the split
 * invisible — the sockets worked, so the transport "worked".
 *
 * A test asserting on the shape of an options object would not have caught it,
 * and would not catch a regression either: the old code ALSO succeeded whenever
 * something else had already arranged trust for the process. So the assertion
 * that matters is behavioural and has to be made in the right order —
 * `fetchDocument()` succeeds against a genuinely untrusted listener that NOTHING
 * ELSE HAS TRUSTED FIRST, because the driver installs its own anchor. That is
 * why `BENCH_PROXY_CERT` is set before the driver is called and never after.
 *
 * -----------------------------------------------------------------------------
 * What it proves and what it does not
 *
 * PROVES: a real `node:https` listener, a real openssl-generated self-signed
 * certificate, a real TLS handshake. The unanchored request really is rejected
 * by node's verifier; the anchored one really does complete; hostname
 * verification really is still enforced afterwards; and the SPKI pin this tree
 * computes in JavaScript really is the one `docker/gen-cert.sh` computes in
 * openssl.
 *
 * DOES NOT PROVE: anything about the bench proxy itself. There is no Caddy here,
 * no gzip, no h2, no WebSocket upgrade and no container — those belong to the
 * live topology and are checked there (bench/README.md, "The measured topology").
 * This file is about one question only: does the node half of the harness trust
 * the certificate §5.3 generates, without disabling verification to do it.
 *
 * It skips, rather than fails, when `openssl` is absent, because the assertion
 * needs a certificate and this tree will not commit a private key (D-2). The
 * bench image carries openssl 3.5.6, so in the environment `ci.sh` runs it in,
 * it runs.
 */
import assert from 'node:assert/strict';
import { execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync, writeFileSync } from 'node:fs';
import { createServer } from 'node:https';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import test from 'node:test';
import tls from 'node:tls';

import {
  DEFAULT_CERT_PATH,
  certPaths,
  ensureBenchTrust,
  spkiPin,
  trustAnchor,
} from './bench-tls.mjs';
import { GotthSession, NextSession } from './driver.mjs';

function haveOpenssl() {
  try {
    execFileSync('openssl', ['version'], { stdio: 'ignore' });
    return true;
  } catch {
    return false;
  }
}

/**
 * A throwaway certificate, generated the same way docker/gen-cert.sh generates
 * the real one — same key type, same SANs, same self-signed shape — so what is
 * exercised below is the case the bench proxy actually presents.
 */
function selfSigned(dir, cn = 'bench.localhost') {
  const key = join(dir, 't.key');
  const crt = join(dir, 't.crt');
  execFileSync('openssl', [
    'req', '-x509', '-newkey', 'rsa:2048', '-nodes',
    '-keyout', key, '-out', crt,
    '-days', '1', '-subj', `/CN=${cn}`,
    '-addext', 'subjectAltName=DNS:bench.localhost,DNS:localhost,IP:127.0.0.1',
  ], { stdio: 'ignore' });
  return { key, crt };
}

/** The §2.0 document shape each session expects to read something out of. */
const DOCUMENT =
  '<!doctype html><html><body><div data-bench-id="root"></div>' +
  '<script>window.__NEXT_DATA__={"props":{"sessionKey":"0123456789abcdef"}}</script>' +
  '</body></html>';

async function listener(certs) {
  const server = createServer(
    { key: readFileSync(certs.key), cert: readFileSync(certs.crt) },
    (req, res) => {
      res.setHeader('Content-Type', 'text/html; charset=utf-8');
      res.setHeader('Set-Cookie', 'bench_sid=deadbeef; Path=/; HttpOnly');
      res.end(DOCUMENT);
    },
  );
  await new Promise((ok) => server.listen(0, '127.0.0.1', ok));
  return { server, origin: `https://127.0.0.1:${server.address().port}` };
}

/* setDefaultCACertificates replaces a process-global, so every test that
   installs an anchor puts the store back. Without this the first test to run
   would silently satisfy the ones after it — which is the exact failure mode
   this file exists to rule out. */
function withRestoredStore(fn) {
  return async (t) => {
    const saved = tls.getCACertificates('default');
    const savedEnv = process.env.BENCH_PROXY_CERT;
    const savedSpki = process.env.BENCH_PROXY_SPKI;
    try {
      await fn(t);
    } finally {
      tls.setDefaultCACertificates(saved);
      if (savedEnv === undefined) delete process.env.BENCH_PROXY_CERT;
      else process.env.BENCH_PROXY_CERT = savedEnv;
      if (savedSpki === undefined) delete process.env.BENCH_PROXY_SPKI;
      else process.env.BENCH_PROXY_SPKI = savedSpki;
    }
  };
}

test(
  'the driver fetches its document over a self-signed listener BY INSTALLING ITS OWN ANCHOR',
  { skip: haveOpenssl() ? false : 'openssl is not on PATH; the fixture needs a certificate' },
  withRestoredStore(async () => {
    const dir = mkdtempSync(join(tmpdir(), 'bench-tls-'));
    const certs = selfSigned(dir);
    const { server, origin } = await listener(certs);

    try {
      /* 1. The fixture is genuinely untrusted. If this ever stops rejecting,
            every assertion below is meaningless and the test has to fail here
            rather than pass quietly further down. */
      await assert.rejects(
        () => fetch(`${origin}/dashboard`),
        (err) => {
          assert.match(
            String(err.cause?.code ?? err.code ?? err.message),
            /SELF_SIGNED|UNABLE_TO_VERIFY/,
            'the fixture must present a certificate node does not already trust',
          );
          return true;
        },
      );

      /* 2. Nothing has anchored anything. The driver is pointed at the
            throwaway certificate the way the topology points it at the bench
            one, and is then asked to do the thing that used to throw.

            THIS is the regression assertion. Before the fix, fetchDocument()
            called a bare global fetch and this line threw
            DEPTH_ZERO_SELF_SIGNED_CERT — on both session classes, and in
            warmUp() before either of them. */
      process.env.BENCH_PROXY_CERT = certs.crt;
      process.env.BENCH_PROXY_SPKI = join(dir, 'nonexistent.spki');

      const gotth = new GotthSession({
        origin,
        route: '/dashboard',
        mountPath: '/dashboard/live',
      });
      const html = await gotth.fetchDocument();
      assert.match(html, /data-bench-id="root"/);
      assert.equal(gotth.jar.get('bench_sid'), 'deadbeef', 'the cookie the upgrade carries');

      /* And the Next.js half, because §3.6's gate measures both stacks through
         the same proxy: a fix on one side would have moved the failure. */
      const next = new NextSession({ origin, route: '/dashboard', variant: 'sse' });
      await next.fetchDocument();
      assert.equal(next.sessionKey, '0123456789abcdef');

      /* 3. Verification was not disabled to achieve any of that. A name the
            certificate does not cover is still refused, which is the whole
            difference between this and `rejectUnauthorized: false`. */
      await new Promise((done) => {
        const socket = tls.connect(
          { host: '127.0.0.1', port: new URL(origin).port, servername: 'not-in-cert.example' },
          () => {
            socket.end();
            done(assert.fail('hostname verification is off: an uncovered name connected'));
          },
        );
        socket.on('error', (err) => {
          assert.equal(err.code, 'ERR_TLS_CERT_ALTNAME_INVALID');
          done();
        });
      });
    } finally {
      server.close();
      rmSync(dir, { recursive: true, force: true });
    }
  }),
);

test(
  'the anchor is additive: the public roots survive it',
  { skip: haveOpenssl() ? false : 'openssl is not on PATH' },
  withRestoredStore(async () => {
    const dir = mkdtempSync(join(tmpdir(), 'bench-tls-'));
    try {
      const certs = selfSigned(dir);
      const before = tls.getCACertificates('default').length;
      const { after } = trustAnchor(readFileSync(certs.crt, 'utf8'));
      assert.equal(after, before + 1, 'exactly one anchor added, none replaced');
      assert.ok(before > 1, 'the stock store was non-trivial to begin with');
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  }),
);

test(
  'the SPKI pin computed here is the one gen-cert.sh computes in openssl',
  { skip: haveOpenssl() ? false : 'openssl is not on PATH' },
  withRestoredStore(async () => {
    const dir = mkdtempSync(join(tmpdir(), 'bench-tls-'));
    try {
      const certs = selfSigned(dir);
      /* gen-cert.sh's exact pipeline, run as a pipeline, against the same file.
         Two implementations of one number is the only way to know the browser's
         --ignore-certificate-errors-spki-list and this module agree. */
      const viaOpenssl = execFileSync(
        'sh',
        [
          '-c',
          `openssl x509 -in "${certs.crt}" -pubkey -noout ` +
            '| openssl pkey -pubin -outform der ' +
            '| openssl dgst -sha256 -binary | openssl enc -base64',
        ],
        { encoding: 'utf8' },
      ).trim();
      assert.equal(spkiPin(readFileSync(certs.crt, 'utf8')), viaOpenssl);
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  }),
);

test(
  'a certificate and a pin that disagree are a startup failure, not a silent split',
  { skip: haveOpenssl() ? false : 'openssl is not on PATH' },
  withRestoredStore(async () => {
    const dir = mkdtempSync(join(tmpdir(), 'bench-tls-'));
    try {
      const certs = selfSigned(dir);
      const stale = join(dir, 'stale.spki');
      writeFileSync(stale, 'AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=\n');
      /* The browser trusts the run by the pin file and the node half by the
         certificate. Letting those drift means two halves of one harness
         talking to two different servers and no symptom until the numbers
         disagree for a reason nobody can name. */
      assert.throws(
        () => ensureBenchTrust('https://127.0.0.1:1', { certPath: certs.crt, spkiPath: stale }),
        /records SPKI pin .* but .* has .*gen-cert\.sh/s,
      );
    } finally {
      rmSync(dir, { recursive: true, force: true });
    }
  }),
);

test('an https origin with no certificate on disk names the missing step', () => {
  assert.throws(
    () => ensureBenchTrust('https://127.0.0.1:18443', { certPath: '/nowhere/bench.crt' }),
    /gen-cert\.sh/,
    'the failure a reader wants is the missing step, not DEPTH_ZERO_SELF_SIGNED_CERT',
  );
  /* And it does NOT quietly fall back to an unverified connection, which is the
     failure mode that would put an unauthenticated peer inside a measured run. */
  assert.throws(
    () => ensureBenchTrust('https://127.0.0.1:18443', { certPath: '/nowhere/bench.crt' }),
    /will not fall back/,
  );
});

test('a plaintext origin is a no-op, so `go run .` needs no certificate', () => {
  /* bench/README.md, "Building and running": each app serves plaintext HTTP
     directly in development. Demanding a proxy certificate before that can be
     driven would be a new failure in exchange for nothing. */
  const r = ensureBenchTrust('http://127.0.0.1:3000');
  assert.equal(r.applied, false);
  assert.match(r.reason, /not https/);
  assert.equal(ensureBenchTrust(undefined).applied, false, 'no origin is also not https');
});

test('the default certificate path is the one docker/gen-cert.sh writes', () => {
  /* A path this module got wrong would fail closed — every https run refusing
     with "run gen-cert.sh" while the file sits where it always did — so it is
     asserted rather than left to a reader to notice. */
  assert.match(DEFAULT_CERT_PATH, /docker[/\\]tls[/\\]bench\.crt$/);
  assert.equal(certPaths().certPath, DEFAULT_CERT_PATH, 'unset env means the committed layout');
  process.env.BENCH_PROXY_CERT = '/tmp/elsewhere.crt';
  try {
    assert.equal(certPaths().certPath, '/tmp/elsewhere.crt');
  } finally {
    delete process.env.BENCH_PROXY_CERT;
  }
});
