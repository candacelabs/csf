/*
 * node --test. The §3.6 half: the synthetic session driver, the 10 % comparison
 * that decides whether it may be used, the TLS-boundary verdict, and G-DRIVER's
 * refusals.
 *
 *   node --test harness/
 *
 * Dependency-light for the same reason harness.test.mjs is: the bench tree is
 * quarantined (FR-74) and every dependency it adds is one the audit has to
 * justify.
 *
 * What is tested here is what a reviewer can check by hand. What is NOT tested
 * here is anything needing a container or a browser — the driver's behaviour
 * against a live server was exercised by running it against
 * bench/apps/dashboard/gotth and gotth-live-bench/dashboard-next:sse, which is
 * construction verification and recorded no timings.
 */
import assert from 'node:assert/strict';
import test from 'node:test';

import { decodeFrame, encodeFrame } from '../../client/codec.gen.js';
import { evaluateBoundary, speaksTls } from './assert-no-tls.mjs';
import {
  GotthSession,
  PROTOCOL_VERSION,
  SUBPROTOCOL,
  WORKLOADS,
  ackFrame,
  countSseFrames,
  eventFrame,
  expandCpuset,
  heartbeatEcho,
  micros,
  resyncFrame,
  sessionFactory,
  sessionKeyFromDocument,
  telemetryFrame,
} from './driver.mjs';
import {
  DRIVER_TOLERANCE,
  VALIDATED_STACKS,
  evaluateDriverValidation,
  requireGates,
  withinTolerance,
} from './gate.mjs';
import { disjoint, warmUpsMatch } from './measure-memory.mjs';
import {
  ARTIFACT_SCHEMA,
  VALIDATION_N,
  artifact,
  compareHalves,
  preflight,
} from './validate-driver.mjs';

const SID = new Uint8Array(16).fill(7);

/* -------------------------------------------------------------------------- */
/* The driver's frames, against the codec the browser actually runs.           */
/* -------------------------------------------------------------------------- */

test('the driver encodes against the SHIPPED codec, not a re-implementation', () => {
  /* The point of the import is that this test cannot pass against a guessed
     frame layout: encodeFrame/decodeFrame here are the same two functions
     client/runtime.js calls, generated from the same FileDescriptorSet as the
     Go side. §3.6's 10-tab gate exists to catch a driver that misrepresents a
     browser; a mis-encoded frame would fail it for a reason that is not the
     stack, and this is the check that removes that reason. */
  const round = (frame) => decodeFrame(encodeFrame(frame));

  const ack = round(ackFrame(SID, 42));
  assert.equal(ack.protocol_version, PROTOCOL_VERSION);
  assert.deepEqual([...ack.session_id], [...SID]);
  assert.equal(ack.ack.server_seq, 42);

  const hb = round(heartbeatEcho(SID, { nonce: 9, interval_ms: 30_000 }));
  assert.equal(hb.heartbeat.nonce, 9, 'both fields are echoed verbatim (protocol §3.4)');
  assert.equal(hb.heartbeat.interval_ms, 30_000);

  const telemetry = round(telemetryFrame(SID, { patchId: 3, morphMs: 1.5, applyMs: 2.25 }));
  assert.equal(telemetry.client_telemetry.patch_id, 3);
  assert.equal(telemetry.client_telemetry.morph_micros, 1500);
  assert.equal(telemetry.client_telemetry.apply_micros, 2250);

  const resync = round(resyncFrame(SID, 11));
  assert.equal(resync.resync_request.last_applied_seq, 11);
  assert.equal(resync.resync_request.reason, 1, 'ResyncReason.GAP');
});

test('an event round-trips with its fields, its fragment and its causation edge', () => {
  const decoded = decodeFrame(
    encodeFrame(
      eventFrame(SID, {
        clientRef: 4,
        name: 'dash.filter',
        fragmentId: 'dash.controls',
        seenServerSeq: 88,
        fields: { v: 'degraded' },
      }),
    ),
  );
  assert.equal(decoded.event.name, 'dash.filter');
  assert.equal(decoded.event.fragment_id, 'dash.controls');
  assert.equal(decoded.event.client_ref, 4);
  /* seen_server_seq is the causation edge H-10 requires: it must name a patch
     the client actually saw, which is why the driver refuses to send before its
     first Snapshot. */
  assert.equal(decoded.event.seen_server_seq, 88);
  assert.deepEqual(decoded.event.fields, [{ key: 'v', value: 'degraded' }]);
});

test('an absent fragment_id is refused here rather than by the server', () => {
  /* Learned the hard way, against the real server: `Event.fragment_id` carries
     a 1:64 length predicate, an empty string encodes as an ABSENT field, and
     bench/apps/dashboard/gotth answered the driver's first event with
     Error{INVALID_FRAME} "event violates its schema". A workload whose events
     are all refused is the idle workload wearing a label. */
  const empty = decodeFrame(
    encodeFrame(
      eventFrame(SID, { clientRef: 1, name: 'x', fragmentId: '', seenServerSeq: 1, fields: {} }),
    ),
  );
  assert.equal(empty.event.fragment_id, undefined, 'an empty string is an absent field on the wire');

  const session = new GotthSession({ origin: 'http://x', route: '/r', mountPath: '/r/live' });
  session.seq = 5;
  session.sessionId = SID;
  assert.throws(() => session.sendEvent('dash.filter', { fragmentId: '' }), /bounded 1:64/);
});

test('micros clamps exactly as client/runtime.js does', () => {
  assert.equal(micros(-1), 0);
  assert.equal(micros(0.5), 500);
  assert.equal(micros(1e9), 60_000_000, 'the schema bounds it at 60 s');
});

test('the workload table names events the apps actually allowlist', () => {
  /* Config.Events is default-deny on both gotth-live apps, so a name this table
     got wrong would be refused with UNKNOWN_EVENT and §3.4s "active" workload
     would silently be the idle one. */
  assert.deepEqual(Object.keys(WORKLOADS).sort(), ['active-heavy', 'active-light', 'idle']);
  assert.equal(WORKLOADS.idle.event, null, '§3.4 idle: heartbeats only');
  assert.equal(WORKLOADS['active-light'].interactionEveryMs, 10_000, '§3.4: one +1 every 10 s');
  assert.equal(WORKLOADS['active-heavy'].interactionEveryMs, 30_000, '§3.4: one control every 30 s');
  for (const name of ['active-light', 'active-heavy']) {
    assert.match(WORKLOADS[name].event.fragmentId, /^[a-z]+\.[a-z]+$/);
  }
});

test('the subprotocol is the servers, not a string this driver invented', () => {
  assert.equal(SUBPROTOCOL, 'gotth-live.v1', 'internal/protocol/limits.go Subprotocol');
  assert.equal(PROTOCOL_VERSION, 1, 'client/runtime.js VERSION');
});

test('--stack is told, not sniffed, and an unknown value is refused', () => {
  assert.throws(() => sessionFactory({ stack: 'nextjs', origin: 'http://x' }), /unknown stack/);
  for (const stack of VALIDATED_STACKS) {
    assert.equal(typeof sessionFactory({ stack, origin: 'http://x', route: '/r' }), 'function');
  }
});

test('the Next.js session key is read from the document, never minted', () => {
  const html = 'self.__next_f.push([1,"...\\"sessionKey\\":\\"48885e61e398433e9f4c7b70397f872d\\"}]"])';
  assert.equal(sessionKeyFromDocument(html), '48885e61e398433e9f4c7b70397f872d');
  /* A channel opened on a key no page rendered is not §3.4's session, so the
     absence is a null the caller turns into a refusal rather than a fallback. */
  assert.equal(sessionKeyFromDocument('<html>no key here</html>'), null);
});

test('SSE frames are counted by their terminator, heartbeats included', () => {
  const bytes = (s) => new TextEncoder().encode(s);
  assert.equal(countSseFrames(bytes('data: {"a":1}\n\n')), 1);
  assert.equal(countSseFrames(bytes('data: {"a":1}\n\ndata: {"a":2}\n\n')), 2);
  assert.equal(countSseFrames(bytes(': hb\n\n')), 1, 'a comment heartbeat is a frame the server sent');
  assert.equal(countSseFrames(bytes('data: partial\n')), 0);
});

test('cpusets expand to core LISTS, because §5.2 asks for the counts', () => {
  assert.deepEqual(expandCpuset('10-13'), [10, 11, 12, 13]);
  assert.deepEqual(expandCpuset('0,2,4-5'), [0, 2, 4, 5]);
  assert.deepEqual(expandCpuset(''), []);
});

test('§3.6: the driver must be pinned DISJOINT from the server under test', () => {
  assert.equal(disjoint('0-7', '10-17'), true);
  assert.equal(disjoint('0-7', '7-9'), false, 'one shared core is not disjoint');
  assert.equal(disjoint('', '10-17'), false, 'an unset cpuset is not evidence of disjointness');
  assert.equal(disjoint('0-7', ''), false);
});

test('§3.6: M(0) and M(N) must follow the SAME warm-up', () => {
  const a = { loads: 50, elapsedMs: 10_000 };
  assert.equal(warmUpsMatch(a, { loads: 50, elapsedMs: 10_400 }).match, true);
  assert.equal(warmUpsMatch(a, { loads: 40, elapsedMs: 10_000 }).match, false, 'same NUMBER of loads');
  assert.equal(warmUpsMatch(a, { loads: 50, elapsedMs: 30_000 }).match, false, 'same elapsed time');
  /* Both elapsed times are published: a clause that reads "the same elapsed
     time" is not discharged by a harness that only promises it. */
  assert.deepEqual(warmUpsMatch(a, { loads: 50, elapsedMs: 10_400 }).elapsedMs, [10_000, 10_400]);
});

/* -------------------------------------------------------------------------- */
/* §3.6's 10 % comparison — including the failing side.                        */
/* -------------------------------------------------------------------------- */

test('the 10 % comparison passes a driver that represents a browser', () => {
  const v = withinTolerance(100_000, 105_000);
  assert.equal(v.within, true);
  assert.ok(Math.abs(v.relative - 0.05) < 1e-12);
  assert.equal(v.deltaBytes, 5_000);
});

test('the 10 % comparison FAILS the driver in both directions', () => {
  /* The failing side is the whole point: a driver that under-represents a
     browser makes the stack look cheaper per session, and one that
     over-represents it makes the stack look dearer. §3.6 disqualifies both. */
  assert.equal(withinTolerance(100_000, 111_000).within, false, 'synthetic too heavy');
  assert.equal(withinTolerance(100_000, 89_000).within, false, 'synthetic too light');
  assert.equal(withinTolerance(100_000, 110_000).within, true, 'exactly 10 % is within');
  assert.equal(withinTolerance(100_000, 90_000).within, true);
});

test('an undefined comparison is a refusal, not a pass', () => {
  assert.equal(withinTolerance(0, 0).within, false, 'a cell whose M(N) did not exceed M(0)');
  assert.equal(withinTolerance(-5, -5).within, false);
  assert.equal(withinTolerance(null, 100).within, false);
  assert.equal(withinTolerance(100, undefined).within, false);
});

test('the denominator is the BROWSER figure, which is the stricter reading', () => {
  /* browser=100k, synthetic=110.5k: 10.5 % against the browser, 9.5 % against
     the larger of the two. §12 says take the reading least favourable to
     gotth-live when a clause is ambiguous; refusing here is that reading. */
  const v = withinTolerance(100_000, 110_500);
  assert.equal(v.within, false);
  assert.ok(v.relative > 0.1 && Math.abs(110_500 - 100_000) / 110_500 < 0.1);
});

test('compareHalves carries both cells, because a memory row needs its CPU row', () => {
  const cell = (bytes) => ({ memPerSessionBytes: bytes, cpuSecondsPerSessionPerMinute: 0.5 });
  const c = compareHalves('gotth', cell(100_000), cell(104_000));
  assert.equal(c.within, true);
  assert.equal(c.browserPerSessionBytes, 100_000);
  assert.equal(c.syntheticPerSessionBytes, 104_000);
  assert.ok(c.browserCell.cpuSecondsPerSessionPerMinute !== undefined, '§3.4: never memory alone');
  assert.ok(c.syntheticCell.cpuSecondsPerSessionPerMinute !== undefined);
});

/* -------------------------------------------------------------------------- */
/* §3.6's boundary assertion.                                                  */
/* -------------------------------------------------------------------------- */

test('the boundary assertion REFUSES when the measured container speaks TLS', () => {
  const verdict = evaluateBoundary({
    sut: 'bench-app',
    proxy: 'bench-proxy',
    ports: [3000, 8443],
    tlsPorts: [8443],
    proxyImage: { imageId: 'sha256:x', repoDigests: ['caddy@sha256:abc'] },
  });
  assert.equal(verdict.pass, false);
  assert.match(verdict.findings.join(' '), /TLS handshake on 8443/);
  /* The clause, not a generic error: the asymmetry is worth ~18,000 B/session
     and is disqualifying in EITHER direction (T-21, amendment A-1). */
  assert.match(verdict.findings.join(' '), /either direction/);
});

test('the boundary assertion refuses a published port and an unequal proxy digest', () => {
  const published = evaluateBoundary({
    sut: 'bench-app',
    proxy: 'bench-proxy',
    sutPublished: [{ port: '3000/tcp', bindings: [{ HostPort: '3000' }] }],
    ports: [3000],
    tlsPorts: [],
    proxyImage: { repoDigests: ['caddy@sha256:abc'] },
  });
  assert.equal(published.pass, false);
  assert.match(published.findings.join(' '), /only container that publishes a port/);

  const mismatched = evaluateBoundary({
    sut: 'bench-app',
    proxy: 'bench-proxy',
    ports: [3000],
    tlsPorts: [],
    proxyImage: { repoDigests: ['caddy@sha256:abc'] },
    expectedProxyDigest: 'caddy@sha256:def',
  });
  assert.equal(mismatched.pass, false);
  assert.match(mismatched.findings.join(' '), /void, not corrected after the fact/);
});

test('a clean topology passes, and says what it checked', () => {
  const verdict = evaluateBoundary({
    sut: 'bench-app',
    proxy: 'bench-proxy',
    ports: [3000, 3101],
    tlsPorts: [],
    proxyImage: { repoDigests: ['caddy@sha256:abc'] },
    expectedProxyDigest: 'caddy@sha256:abc',
  });
  assert.equal(verdict.pass, true);
  assert.deepEqual(verdict.sutTlsPorts, []);
  assert.deepEqual(verdict.sutListeningPorts, [3000, 3101]);
  assert.equal(verdict.boundary, 'outside');
});

test('a plaintext listener answers no ClientHello, which is positive evidence', async () => {
  /* The third check is the one that cannot be talked around, so it is exercised
     against a real socket rather than mocked: an HTTP server receiving a
     ClientHello closes, times out or answers with an error, and none of those
     is a secureConnect. */
  const { createServer } = await import('node:http');
  const server = createServer((_, res) => res.end('ok'));
  await new Promise((r) => server.listen(0, '127.0.0.1', r));
  const port = server.address().port;
  try {
    assert.equal(await speaksTls('127.0.0.1', port, 1500), false);
  } finally {
    server.close();
  }
});

/* -------------------------------------------------------------------------- */
/* G-DRIVER.                                                                   */
/* -------------------------------------------------------------------------- */

const passing = (over = {}) =>
  artifact({
    status: 'run',
    app: 'dashboard',
    variant: 'sse',
    workload: 'idle',
    stacks: {
      gotth: { browserPerSessionBytes: 100_000, syntheticPerSessionBytes: 103_000 },
      next: { browserPerSessionBytes: 200_000, syntheticPerSessionBytes: 195_000 },
    },
    ...over,
  });

test('G-DRIVER passes only when four measured numbers say it may', () => {
  const gate = evaluateDriverValidation(passing(), { app: 'dashboard', variant: 'sse' });
  assert.equal(gate.pass, true);
  assert.match(gate.note, /gotth 100000 vs 103000/);
});

test('G-DRIVER refuses when the artifact is ABSENT', () => {
  const gate = evaluateDriverValidation(null, { app: 'dashboard' });
  assert.equal(gate.pass, false);
  assert.match(gate.note, /10 real Chromium tabs/, 'the refusal quotes the clause');
  assert.match(gate.note, /no artifact at/);
});

test('G-DRIVER refuses an artifact that says the gate did not run', () => {
  const gate = evaluateDriverValidation(
    artifact({
      status: 'not-run',
      reason: 'a GPU streaming container is running',
      app: 'dashboard',
    }),
    { app: 'dashboard' },
  );
  assert.equal(gate.pass, false);
  assert.match(gate.note, /GPU streaming container/);
});

test('G-DRIVER refuses a STALE artifact: a different driver, or a different app', () => {
  /* A validation is a statement about one driver. Editing harness/driver.mjs
     retires it without anybody having to remember to, which is the only form of
     that rule that survives contact with a busy Phase 5. */
  const staleDriver = evaluateDriverValidation(
    { ...passing(), driverSha256: 'a'.repeat(64) },
    { app: 'dashboard', driverSha256: 'b'.repeat(64) },
  );
  assert.equal(staleDriver.pass, false);
  assert.match(staleDriver.note, /DIFFERENT harness\/driver\.mjs/);

  const otherApp = evaluateDriverValidation(passing(), { app: 'chat' });
  assert.equal(otherApp.pass, false);
  assert.match(otherApp.note, /per app\/stack pair/);

  const otherVariant = evaluateDriverValidation(passing(), { app: 'dashboard', variant: 'ws' });
  assert.equal(otherVariant.pass, false);
  assert.match(otherVariant.note, /validated variant/);
});

test('G-DRIVER refuses when one stack is missing, because §3.6 says BOTH', () => {
  const gate = evaluateDriverValidation(
    passing({
      stacks: { next: { browserPerSessionBytes: 1, syntheticPerSessionBytes: 1 } },
    }),
    { app: 'dashboard' },
  );
  assert.equal(gate.pass, false);
  assert.match(gate.note, /no figures for gotth/);
});

test('G-DRIVER refuses when EITHER stack is outside 10 %', () => {
  const gotthFails = evaluateDriverValidation(
    passing({
      stacks: {
        gotth: { browserPerSessionBytes: 100_000, syntheticPerSessionBytes: 140_000 },
        next: { browserPerSessionBytes: 200_000, syntheticPerSessionBytes: 195_000 },
      },
    }),
    { app: 'dashboard' },
  );
  assert.equal(gotthFails.pass, false);
  assert.match(gotthFails.note, /misrepresents a browser and MUST be fixed/);
  assert.match(gotthFails.note, /gotth: 100000 B\/session with 10 real tabs/);

  const nextFails = evaluateDriverValidation(
    passing({
      stacks: {
        gotth: { browserPerSessionBytes: 100_000, syntheticPerSessionBytes: 103_000 },
        next: { browserPerSessionBytes: 200_000, syntheticPerSessionBytes: 100_000 },
      },
    }),
    { app: 'dashboard' },
  );
  assert.equal(nextFails.pass, false);
  assert.match(nextFails.note, /next: 200000/);
});

test('G-DRIVER cannot be asserted by hand in gates.json', () => {
  /* Every other gate is a boolean somebody writes down. This one is derived
     from four measured numbers AFTER the merge, so a hand-written
     {"driverValidation":{"pass":true}} is overwritten by whatever the artifact
     says — which, in this tree today, is that there is no artifact. */
  assert.throws(
    () => requireGates(['driverValidation'], { app: 'dashboard' }),
    /10 real Chromium tabs|no artifact at/,
  );
});

test('the preflight collects EVERY blocker, not the first one', () => {
  /* On a shared host each round trip is a wait for somebody else's GPU session
     to end, so "not run, and why" has to be a work list rather than a first
     excuse. */
  const host = {
    steamActive: true,
    containers: [{ name: 'gpu-desktop-steam-1', image: 'steam-desktop:abc', project: 'gpu-desktop' }],
  };
  const blockers = preflight({
    args: {},
    host,
    images: { gotth: null, next: null },
    benchRoot: '/nowhere-that-exists',
  });
  const ids = blockers.map((b) => b.id);
  assert.ok(ids.includes('Q-7'));
  assert.ok(ids.includes('image:gotth'));
  assert.ok(ids.includes('image:next'));
  assert.ok(ids.includes('cpuset:driver'));
  assert.ok(ids.some((id) => id.startsWith('missing:')));
  assert.match(
    blockers.find((b) => b.id === 'Q-7').blocker,
    /SKIPPED, not degraded/,
    'the refusal quotes Q-7 rather than failing generically',
  );
  /* And the conservative reading is stated where an operator reads it: a
     running container blocks whether or not somebody is streaming. */
  assert.match(blockers.find((b) => b.id === 'Q-7').blocker, /whether or not somebody is streaming/);
});

test('a named-but-absent gotth image sends the operator to the recipe, not to a dead end', () => {
  /* D-9 is closed. This refusal used to read "there is no committed recipe that
     builds a gotth-live SUT container", which was true when it was written and
     is the kind of sentence that outlives its truth quietly — an operator would
     go looking for the missing half of the work and find it already done.

     It is asserted here rather than trusted to prose because the two details
     that make the command work are exactly the two a reader gets wrong: it is
     run from bench/, and its context is `..` (the apps' go.mod `replace` puts
     the library source in the build, so the context cannot stop at bench/). A
     blocker naming a command nobody can run is the same dead end as no command.

     Note the arm this exercises: the test above passes null images and reaches
     "no image was named". This one names an image that is not on the machine —
     `gotth-live-bench/nothing-here:absent` is not an image anybody builds — so
     the docker `image inspect` fails and the recipe arm is the one that runs. */
  const blockers = preflight({
    args: { cpuset: '10-17' },
    host: { steamActive: false, containers: [] },
    images: { gotth: 'gotth-live-bench/nothing-here:absent', next: 'gotth-live-bench/nothing-here:absent' },
    benchRoot: '/nowhere-that-exists',
  });
  const gotth = blockers.find((b) => b.id === 'image:gotth').blocker;
  assert.match(gotth, /docker build -f docker\/gotth\.Dockerfile/);
  assert.match(gotth, /--build-arg APP=/);
  assert.match(gotth, /\s\.\.`/, 'the context is the gotth-live root, not bench/');
  assert.doesNotMatch(
    gotth,
    /no committed recipe|no gotth counterpart/,
    'D-9 is resolved; the refusal must not still claim the recipe is missing',
  );
});

test('a quiescent host with everything in place yields no blockers of its own', () => {
  const blockers = preflight({
    args: { cpuset: '10-17' },
    host: { steamActive: false, containers: [] },
    images: { gotth: 'x', next: 'y' },
    benchRoot: '/nowhere-that-exists',
  });
  /* The two images are absent from this test machine and the two files are
     absent from /nowhere; what must NOT appear is Q-7 or the cpuset. */
  assert.equal(blockers.some((b) => b.id === 'Q-7'), false);
  assert.equal(blockers.some((b) => b.id === 'cpuset:driver'), false);
});

test('the artifact records what a reader needs to re-derive the verdict', () => {
  const record = passing();
  assert.equal(record.schema, ARTIFACT_SCHEMA);
  assert.equal(record.n, VALIDATION_N, '§3.6 fixes N at 10 for this gate');
  assert.equal(record.tolerance, DRIVER_TOLERANCE);
  assert.equal(typeof record.driverSha256, 'string');
  assert.match(record.spec, /§3\.6/);
});
