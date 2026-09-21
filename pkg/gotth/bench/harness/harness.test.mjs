/*
 * node --test. Dependency-light on purpose: the bench tree is quarantined
 * (FR-74) and every dependency it adds is a dependency the audit has to
 * justify, so the harness's own tests use the runtime's test runner and nothing
 * else.
 *
 *   node --test harness/
 *
 * What is tested here is the part of the harness that has an answer a reviewer
 * can check by hand: the §6 statistics, the §2 interaction registry, and the
 * refusals. What is NOT tested here is anything that needs a browser or a
 * container — those are exercised by harness/smoke.mjs, which runs for real.
 */
import assert from 'node:assert/strict';
import test from 'node:test';

import { bootstrapCI, cell, compare, instability, percentile, summarize } from './analyze.mjs';
import { INTERACTIONS, SPEC_IDS, forApp, headlines, timed } from './interactions/index.mjs';
import { operatorInvoked, readGates, requireGates } from './gate.mjs';
import { DEFAULT_GPU_SESSION_MATCH, coTenants, steamBlockers } from './host-state.mjs';
import { STACKS } from './run.mjs';
import { skipReason } from './smoke.mjs';

test('percentile is nearest-rank, so every reported value was observed', () => {
  const sorted = [1, 2, 3, 4, 5, 6, 7, 8, 9, 10];
  assert.equal(percentile(sorted, 50), 5);
  assert.equal(percentile(sorted, 99), 10);
  assert.equal(percentile(sorted, 100), 10);
  /* No interpolation: a p95 of a latency distribution should be a latency that
     actually happened, not an average of two that did. */
  assert.ok(sorted.includes(percentile(sorted, 95)));
});

test('summarize reports percentiles and never a mean (FR-73)', () => {
  const s = summarize([5, 1, 3, 2, 4]);
  assert.equal(s.n, 5);
  assert.equal(s.p50, 3);
  assert.equal(s.min, 1);
  assert.equal(s.max, 5);
  assert.ok(!('mean' in s), 'FR-73: percentiles, never means, for latency');
});

test('bootstrap CI is deterministic, so a published interval is reproducible', () => {
  const values = Array.from({ length: 200 }, (_, i) => i + 1);
  const a = bootstrapCI(values, 50, { resamples: 500, seed: 7 });
  const b = bootstrapCI(values, 50, { resamples: 500, seed: 7 });
  assert.deepEqual(a, b);
  const c = bootstrapCI(values, 50, { resamples: 500, seed: 8 });
  assert.notDeepEqual(a.lo === c.lo && a.hi === c.hi, true, 'a different seed should move the interval');
  assert.ok(a.lo <= a.hi);
});

test('§6 instability rule flags a cell whose per-run p50s spread past 20 %', () => {
  assert.equal(instability([100, 102, 98, 101, 99], 100).unstable, false);
  const bad = instability([100, 100, 100, 100, 140], 100);
  assert.equal(bad.unstable, true);
  assert.equal(bad.threshold, 0.2);
});

test('§6: overlapping confidence intervals are NOT a win', () => {
  const runsA = [Array.from({ length: 100 }, (_, i) => 100 + (i % 10))];
  const runsB = [Array.from({ length: 100 }, (_, i) => 101 + (i % 10))];
  const verdict = compare(cell(runsA), cell(runsB), { labelA: 'gotth', labelB: 'next' });
  assert.equal(verdict.verdict, 'no measured difference');
  assert.equal(verdict.winner, undefined, 'an overlapping pair must not name a winner');
});

test('§6: separated confidence intervals name a winner and a ratio', () => {
  const fast = [Array.from({ length: 100 }, () => 10)];
  const slow = [Array.from({ length: 100 }, () => 100)];
  const verdict = compare(cell(fast), cell(slow), { labelA: 'gotth', labelB: 'next' });
  assert.equal(verdict.winner, 'gotth');
  assert.equal(verdict.ratio, 10);
});

test('E2: the interaction registry is exactly §2s tables, no more and no less', () => {
  assert.deepEqual([...INTERACTIONS.keys()].sort(), [...SPEC_IDS].sort());
  for (const id of SPEC_IDS) {
    const i = INTERACTIONS.get(id);
    assert.equal(i.id, id, 'the file name and the id must agree');
    assert.ok(i.app, `${id} names no app`);
    assert.ok(i.route, `${id} names no route`);
    assert.ok(i.region, `${id} names no data-bench-region (§3.1 needs the ROI)`);
    assert.ok(i.measured, `${id} does not say whether it is measured`);
  }
});

test('every app has the interactions §2 gives it', () => {
  assert.equal(forApp('counter').length, 8, '§2.1 lists CTR-1..8');
  assert.equal(forApp('chat').length, 9, '§2.3 lists CHT-1..8 plus CHT-2b');
  assert.equal(forApp('dashboard').length, 8, '§2.4 lists DSH-1..8');
});

test('the headline rows are the ones §2 marks headline', () => {
  assert.deepEqual(
    headlines().map((i) => i.id).sort(),
    ['CHT-2', 'CHT-3', 'CTR-1', 'CTR-7', 'DSH-1', 'DSH-7'],
  );
});

test('correctness-only rows produce no latency sample', () => {
  const timedIds = timed().map((i) => i.id);
  for (const id of ['CTR-8', 'CHT-7', 'CHT-8']) {
    assert.ok(!timedIds.includes(id), `${id} is a correctness assertion, not a timing`);
  }
});

test('every timed interaction has a paint predicate and a driver', () => {
  for (const i of timed()) {
    if (i.push || i.crossTab) continue; // driven by the run driver, not runInteraction
    assert.equal(typeof i.predicate, 'function', `${i.id} has no paint predicate`);
    assert.equal(typeof i.drive, 'function', `${i.id} has no driver`);
  }
});

test('paint predicates are strings evaluated IN THE PAGE, not host closures', () => {
  /* This is what keeps the predicate text living with the interaction ID and
     out of the harness — §3 forbids a per-stack branch in the harness, and a
     predicate that closed over harness state would be one place such a branch
     could hide. */
  const src = INTERACTIONS.get('CTR-1').predicate({ value: 42 });
  assert.equal(typeof src, 'string');
  assert.match(src, /window\.__bench\.value/);
});

test('a nextOnly row is skipped on gotth-live and DRIVEN on Next.js', () => {
  const cht2b = INTERACTIONS.get('CHT-2b');
  assert.equal(cht2b.nextOnly, true, 'AS-2: CHT-2b is the Next.js-only optimistic-send row');

  /* The bug this covers: smoke.mjs skipped only push/crossTab, so CHT-2b was
     driven against gotth-live, timed out by design (no optimistic UI, BL-4),
     and made `npm run smoke -- --app chat` exit non-zero for a non-defect. */
  assert.match(skipReason(cht2b, 'gotth'), /nextOnly/);

  /* And the bug the fix could have introduced, which is the worse one: a skip
     that also fired on Next.js would hide a regression in the one capability
     §2.3 credits that stack with. */
  assert.equal(skipReason(cht2b, 'next'), null, 'a nextOnly row must run on Next.js');
});

test('the skip line names its category, for every category', () => {
  assert.match(skipReason(INTERACTIONS.get('CHT-3'), 'next'), /push\/cross-tab/);
  assert.match(skipReason(INTERACTIONS.get('CTR-7'), 'gotth'), /push\/cross-tab/);
  /* An ordinary row is driven on both stacks; only the three flags excuse one. */
  for (const stack of STACKS) {
    assert.equal(skipReason(INTERACTIONS.get('CTR-1'), stack), null);
    assert.equal(skipReason(INTERACTIONS.get('CHT-2'), stack), null);
  }
});

test('nextOnly and measured:nextjs-only travel together, or neither does', () => {
  /* Two spellings of one fact: `measured` drives the report tables and
     `nextOnly` drives the driver. A future row that set one and forgot the
     other would be published as Next-only and still driven against gotth-live,
     which is exactly the state this fix found CHT-2b in. */
  for (const i of INTERACTIONS.values()) {
    assert.equal(
      Boolean(i.nextOnly),
      i.measured === 'nextjs-only',
      `${i.id}: nextOnly=${Boolean(i.nextOnly)} but measured=${JSON.stringify(i.measured)}`,
    );
  }
  assert.deepEqual(
    [...INTERACTIONS.values()].filter((i) => i.nextOnly).map((i) => i.id),
    ['CHT-2b'],
    'CHT-2b is the only Next.js-only row in §2; a new one is a §12 matter',
  );
});

test('§5.7: the operator gate is about this invocation, not a config file', () => {
  assert.equal(operatorInvoked(['node', 'x']), false);
  assert.equal(operatorInvoked(['node', 'x', '--operator-approved']), true);
});

test('gates refuse by default, and say which clause they are refusing under', () => {
  const gates = readGates();
  for (const name of ['driverValidation', 'conformance', 'phase3']) {
    assert.equal(gates[name].pass, false, `${name} must default to closed`);
  }
  assert.throws(
    () => requireGates(['driverValidation']),
    /10 real Chromium tabs|driverValidation/,
    'the refusal must quote the clause, not just fail',
  );
  assert.throws(() => requireGates(['phase3']), /QA3-1|Appendix B/);
});

/*
 * Q-7's gate, whose match list became configuration when the container it was
 * written against stopped being nameable in a published tree. The three
 * properties below are the whole of what "conservative" meant when the pattern
 * was a literal, so they are asserted rather than left to the comment.
 */
test('Q-7 blocks on a GPU streaming container whatever the deployment calls it', () => {
  const list = [
    { name: 'bench-app', image: 'gotth-live-bench/dashboard-gotth:local', project: 'gotth-live-bench' },
    { name: 'ops-steam-1', image: 'steam-desktop:abc', project: 'ops' },
  ];
  assert.deepEqual(
    steamBlockers(list, {}).map((c) => c.name),
    ['ops-steam-1'],
    'a Compose-prefixed name must match: the default is a substring, not a prefix',
  );
  assert.deepEqual(
    steamBlockers([{ name: 'app', image: 'ghcr.io/example/selkies-desktop:v1', project: '' }], {}).map(
      (c) => c.name,
    ),
    ['app'],
    'the image is matched as well as the name, because a renamed container still runs the stack',
  );
  assert.deepEqual(steamBlockers(list, { BENCH_GPU_SESSION_MATCH: 'gpu-runtime' }).map((c) => c.name), []);
  assert.deepEqual(
    steamBlockers([{ name: 'x-gpu-runtime-1', image: 'i', project: 'p' }], {
      BENCH_GPU_SESSION_MATCH: 'gpu-runtime',
    }).map((c) => c.name),
    ['x-gpu-runtime-1'],
  );
});

test('an empty BENCH_GPU_SESSION_MATCH falls back to the default rather than to no gate', () => {
  /* The fail-open shape this gate exists to refuse: a blank value in a .env
     would otherwise leave the Q-7 check matching nothing and every run
     reporting a quiescent host. */
  const gpu = [{ name: 'ops-steam-1', image: 'steam-desktop:abc', project: 'ops' }];
  for (const value of ['', '   ', ',,']) {
    assert.equal(steamBlockers(gpu, { BENCH_GPU_SESSION_MATCH: value }).length, 1, JSON.stringify(value));
  }
  assert.ok(DEFAULT_GPU_SESSION_MATCH.length > 0);
});

test('§5.2 co-tenancy counts every project but the bench\u2019s own', () => {
  const list = [
    { name: 'bench-app', image: 'i', project: 'gotth-live-bench' },
    { name: 'bench-proxy', image: 'i', project: 'gotth-live-bench' },
    { name: 'node-a-tenant-1', image: 'i', project: 'other' },
    { name: 'loose', image: 'i', project: '' },
  ];
  assert.deepEqual(
    coTenants('gotth-live-bench', list).map((c) => c.name),
    ['node-a-tenant-1', 'loose'],
  );
});
