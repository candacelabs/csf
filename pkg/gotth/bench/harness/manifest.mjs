/*
 * The run manifest (§6), and the two gates that stand between the harness and
 * a recorded cell.
 *
 * §6: "The harness writes a manifest for every run it starts, including aborted
 * and failed runs, with the abort reason. The report's raw-data directory
 * contains every run id the harness ever emitted for the final report; a gap in
 * the id sequence is an audit failure."
 *
 * That sentence is the whole design of this module. A manifest is opened FIRST,
 * before anything can fail, and closed with an outcome whatever happens. There
 * is no path through this harness that produces samples without a manifest, and
 * no path that abandons a manifest silently — because "no selective re-running"
 * (§6) is only checkable if the id sequence is contiguous.
 */
import { execFileSync } from 'node:child_process';
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, readdirSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

import { hostState } from './host-state.mjs';

export const BENCH_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
export const DATA_DIR = join(BENCH_ROOT, 'data');

function git(args) {
  try {
    return execFileSync('git', args, { cwd: BENCH_ROOT, encoding: 'utf8' }).trim();
  } catch {
    return '';
  }
}

/**
 * Run ids are a contiguous zero-padded sequence, deliberately.
 *
 * A timestamp or a uuid would make a gap invisible — you cannot see that
 * `run-1785873261` is missing. §6 makes a gap an audit failure, so the ids have
 * to be countable by a reader who was not there.
 */
export function nextRunId() {
  mkdirSync(DATA_DIR, { recursive: true });
  const used = readdirSync(DATA_DIR)
    .map((name) => /^run-(\d{5})$/.exec(name))
    .filter(Boolean)
    .map((m) => Number(m[1]));
  const next = used.length === 0 ? 1 : Math.max(...used) + 1;
  return `run-${String(next).padStart(5, '0')}`;
}

/** SHA-256 of a file on disk, for the fixture and the shim (§2.0, §2.5, §6). */
export function sha256File(path) {
  if (!existsSync(path)) return null;
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}

/**
 * Open a manifest. Everything §6 lists is filled in here or by close().
 *
 * The caller supplies what only it knows (which stack, which app, which
 * variant, which network profile, the cgroup limits it applied); the rest is
 * read from the host and the tree so it cannot be misreported by a caller in a
 * hurry.
 */
export function openRun(spec) {
  const runId = spec.runId ?? nextRunId();
  const dir = join(DATA_DIR, runId);
  mkdirSync(dir, { recursive: true });

  const manifest = {
    runId,
    startedAt: new Date().toISOString(),
    outcome: 'started',
    abortReason: null,

    /* What is being measured. */
    dimension: spec.dimension ?? null,
    stack: spec.stack ?? null,
    app: spec.app ?? null,
    variant: spec.variant ?? null,
    workload: spec.workload ?? null,
    concurrency: spec.concurrency ?? null,
    networkProfile: spec.networkProfile ?? 'lan',

    /* §5.2 — host, and the contended flag. */
    host: hostState({ benchProject: spec.benchProject }),

    /* §6 — versions and provenance. */
    git: {
      sha: git(['rev-parse', 'HEAD']),
      dirty: git(['status', '--porcelain']) !== '',
      describe: git(['describe', '--always', '--dirty']),
    },
    versionsLock: sha256File(join(BENCH_ROOT, 'versions.lock.md')),
    shimSha256: sha256File(join(BENCH_ROOT, 'harness', 'shim.js')),
    fixtureSha256: spec.app
      ? sha256File(join(BENCH_ROOT, 'fixtures', spec.app, 'ticks.jsonl'))
      : null,

    /* §5.2 — container constraints, recorded from docker inspect by the caller. */
    containers: spec.containers ?? null,
    cpusets: spec.cpusets ?? null,

    /* §3.6 — the TLS boundary, its assertion result, and the proxy digest. */
    tls: spec.tls ?? {
      boundary: 'outside',
      asserted: false,
      note: 'assertTlsBoundary() has not run for this manifest yet',
    },

    /* §3.7 — set when the discovered rate is within 20 % of the proxy's own
       saturation rate, in which case the number is a bound on the harness. */
    proxyLimited: null,

    /* §3.6 / §5.1 — GC configuration in force, pinned and disclosed. */
    gc: spec.gc ?? null,

    /* §5.6 + Appendix B — what the gotth-live side was configured with. A
       Next.js run records these as null, which is itself the §8 parity row:
       there is no equivalent default instrumentation to configure. */
    observability: spec.observability ?? {
      provenanceLogger: null,
      provenanceSink: null,
      coalesceFlushAt: null,
      minResyncInterval: null,
      resyncBurst: null,
    },

    /* §3.1 — the timer resolution the browser actually applied. */
    timerResolution: spec.timerResolution ?? null,

    /* §3.6 — the 10-tab gate's state at the moment this run started. */
    driverValidation: spec.driverValidation ?? null,

    /* §3.2 — the push-clock skew bound, published beside every push row. */
    clockSkew: spec.clockSkew ?? null,
  };

  write(dir, manifest);

  return {
    runId,
    dir,
    manifest,
    /** Merge additional facts in as they become known. */
    record(patch) {
      Object.assign(manifest, patch);
      write(dir, manifest);
      return manifest;
    },
    /** Append one sample row. §6: raw data is CSV, one row per sample. */
    samples(rows, header) {
      const path = join(dir, 'samples.csv');
      const lines = [];
      if (!existsSync(path)) lines.push(header.join(','));
      for (const row of rows) lines.push(header.map((h) => csv(row[h])).join(','));
      writeFileSync(path, `${lines.join('\n')}\n`, { flag: 'a' });
    },
    close(outcome, abortReason = null) {
      manifest.outcome = outcome;
      manifest.abortReason = abortReason;
      manifest.finishedAt = new Date().toISOString();
      write(dir, manifest);
      return manifest;
    },
  };
}

function csv(value) {
  if (value === null || value === undefined) return '';
  const text = String(value);
  return /[",\n]/.test(text) ? `"${text.replace(/"/g, '""')}"` : text;
}

function write(dir, manifest) {
  writeFileSync(join(dir, 'manifest.json'), `${JSON.stringify(manifest, null, 2)}\n`);
}

/** Every run id the harness has ever emitted, and whether the sequence is intact. */
export function auditRunIds() {
  if (!existsSync(DATA_DIR)) return { ids: [], contiguous: true, gaps: [] };
  const ids = readdirSync(DATA_DIR)
    .map((name) => /^run-(\d{5})$/.exec(name))
    .filter(Boolean)
    .map((m) => Number(m[1]))
    .sort((a, b) => a - b);
  const gaps = [];
  for (let i = 1; i < ids.length; i++) {
    for (let n = ids[i - 1] + 1; n < ids[i]; n++) gaps.push(n);
  }
  return { ids, contiguous: gaps.length === 0, gaps };
}
