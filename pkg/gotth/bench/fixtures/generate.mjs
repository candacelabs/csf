#!/usr/bin/env node
/*
 * The shared fixture generator (equivalence-spec §2.5).
 *
 *   node fixtures/generate.mjs            regenerate every app's ticks.jsonl
 *   node fixtures/generate.mjs --verify   regenerate into memory, compare to
 *                                         the committed .sha256, exit 1 on drift
 *   node fixtures/generate.mjs chat       one app only
 *
 * -----------------------------------------------------------------------------
 * Why this file exists at all
 *
 * §2.5: "Reimplementing a data generator twice invites accidental asymmetry."
 * So neither server generates data. This generator runs once, its output is
 * committed as a SHA-256 (the bytes themselves are large and derived, so the
 * .sha256 is the committed artefact and .gitignore keeps the JSONL out), and
 * both servers read the same bytes and replay them against the same monotonic
 * schedule: tick N is emitted at T0 + N x 100 ms.
 *
 * The fixture is a BENCH INPUT FILE, never wire traffic (§2.5). gotth-live
 * reads it server-side and emits liquid proto frames; this side reads it
 * server-side and pushes its own frames. Nothing about the file's format
 * reaches either wire.
 *
 * -----------------------------------------------------------------------------
 * The seed
 *
 * §2.5 names the dashboard seed `0xG07TH11VE`, which is a mnemonic and not a
 * hex literal — G, T and H are not hex digits. It is used here as the literal
 * ASCII string it is written as, hashed to 32 bits with FNV-1a. That is
 * deterministic, reproducible from the spec text alone, and recorded here so a
 * reader who expects an integer knows exactly what was done with the token
 * instead of guessing which digits were dropped.
 */
import { createHash } from 'node:crypto';
import { existsSync, mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const here = dirname(fileURLToPath(import.meta.url));

/** The spec's mnemonic seed, as the ASCII string it is written as. */
export const SEED_TOKEN = '0xG07TH11VE';

/** §2.5: "tick N is emitted at T0 + N x 100 ms". */
export const TICK_MS = 100;

/** §2.5: "36 000 ticks = 1 hour at 10 Hz". Both apps use the same horizon. */
export const TICKS = 36_000;

function fnv1a(text) {
  let h = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

/**
 * splitmix32 — small, fast, and deterministic across engines because every
 * operation is a 32-bit integer op. Math.random() is obviously unusable; so is
 * anything whose float rounding could differ between the machine that
 * generated the fixture and the machine that verifies it.
 */
function rng(seed) {
  let s = seed >>> 0;
  return function next() {
    s = (s + 0x9e3779b9) >>> 0;
    let z = s;
    z = Math.imul(z ^ (z >>> 16), 0x21f0aaad) >>> 0;
    z = Math.imul(z ^ (z >>> 15), 0x735a2d97) >>> 0;
    return ((z ^ (z >>> 15)) >>> 0) / 4294967296;
  };
}

function pick(r, list) {
  return list[Math.floor(r() * list.length) % list.length];
}

function intBetween(r, lo, hi) {
  return lo + Math.floor(r() * (hi - lo + 1));
}

/* --------------------------------------------------------------- chat ---- */

/** §2.3: "8 simulated peers". */
const PEERS = ['ana', 'bo', 'cy', 'dee', 'eli', 'fen', 'gus', 'hana'];

const WORDS = (
  'the deploy went out clean rollback ready staging is green latency looks flat ' +
  'cache hit rate climbed a bit after the change queue depth is nothing to worry ' +
  'about yet i pinned the version and rebuilt the image logs are quiet since noon ' +
  'that alert was a threshold not an incident somebody restarted the collector ' +
  'again memory is steady across all four replicas the proxy is doing the tls now ' +
  'so the app container is plaintext which is what we wanted numbers later'
).split(' ');

/**
 * Body length: lognormal, clamped to §2.3's 1..500, parameters chosen so the
 * empirical mean lands on the spec's 62. The generator prints the achieved mean
 * so the claim is checked rather than asserted.
 */
function bodyLength(r) {
  // Box-Muller from two uniforms, then exp().
  const u1 = Math.max(r(), 1e-9);
  const u2 = r();
  const n = Math.sqrt(-2 * Math.log(u1)) * Math.cos(2 * Math.PI * u2);
  const len = Math.round(Math.exp(3.807 + 0.8 * n));
  return Math.min(500, Math.max(1, len));
}

function body(r) {
  const want = bodyLength(r);
  let out = '';
  while (out.length < want) {
    out += (out ? ' ' : '') + pick(r, WORDS);
  }
  return out.slice(0, want);
}

/*
 * Rates, straight from §2.3: "Peer traffic replayed from the fixture at
 * 2 msg/s aggregate for latency runs and 20 msg/s for the stress row.
 * Typing-indicator events at 4/s."
 *
 * The fixture is generated ONCE, at the latency rate. The stress row replays
 * the SAME committed bytes with the tick interval divided by 10, which is how
 * one fixture and one SHA-256 serve both rows — §2.5 requires both servers to
 * read the same bytes, not that a rate be baked into them. The replay interval
 * in force is recorded in the run manifest.
 */
function chatTicks() {
  const r = rng(fnv1a(`${SEED_TOKEN}:chat`));
  const rooms = ['alpha', 'beta', 'gamma'];
  const ticks = [];

  // Everybody is present from tick 0; joins and leaves churn a minority of the
  // roster so F-CHT-5 has something live to show without the list ever being
  // empty (an empty roster would make CHT-3's predicate ambiguous).
  const present = new Set(PEERS);
  const base = { presence: [...PEERS], rooms };

  let lengths = 0;
  let messages = 0;

  for (let n = 0; n < TICKS; n++) {
    const events = [];

    // 2 msg/s aggregate = one message every 5th tick at 10 Hz.
    if (n % 5 === 0) {
      const text = body(r);
      lengths += text.length;
      messages++;
      events.push({ k: 'msg', room: pick(r, rooms), author: pick(r, PEERS), body: text });
    }

    // 4 typing events/s = two every 5th tick, offset from the messages so a
    // typing repaint and a message repaint are separable in the samples.
    if (n % 5 === 2) {
      events.push({ k: 'typing', room: pick(r, rooms), author: pick(r, PEERS) });
      events.push({ k: 'typing', room: pick(r, rooms), author: pick(r, PEERS) });
    }

    // A join or a leave every 30 s, never emptying the roster.
    if (n % 300 === 150) {
      const who = pick(r, PEERS);
      if (present.has(who) && present.size > 5) {
        present.delete(who);
        events.push({ k: 'leave', room: pick(r, rooms), author: who });
      } else if (!present.has(who)) {
        present.add(who);
        events.push({ k: 'join', room: pick(r, rooms), author: who });
      }
    }

    if (events.length > 0) ticks.push({ n, e: events });
  }

  return { base, ticks, stats: { messages, meanBodyLength: lengths / messages } };
}

/* ---------------------------------------------------------- dashboard ---- */

/** §2.4 region A: 8 KPI tiles. */
const KPIS = ['requests', 'errors', 'p50 ms', 'p99 ms', 'queue', 'workers', 'cache %', 'rps'];

/** §2.4 region B: 200 rows x 8 cols, "stable sort by id unless user sorts". */
const ROWS = 200;
const STATUSES = ['ok', 'warn', 'error'];

const NOUNS = (
  'ingest replay shard router relay warden ledger beacon anvil cinder drift ember ' +
  'flint glint harbor iris jetty kiln lumen mesa nomad onyx pylon quartz ridge ' +
  'slate talon umber vault willow xenon yarrow zephyr'
).split(' ');

function rowName(r, i) {
  return `${pick(r, NOUNS)}-${String(i).padStart(3, '0')}`;
}

/** §2.4 region D: append-only event log, capped 50, 5 Hz. */
const LOG_VERBS = ['scaled', 'drained', 'restarted', 'promoted', 'evicted', 'rebalanced'];

function dashboardTicks() {
  const r = rng(fnv1a(`${SEED_TOKEN}:dashboard`));

  /*
   * The initial 200-row table and the 8 KPI seeds go in the fixture's base
   * record rather than being computed by each server: E3 says both servers
   * render identical information at identical times, and "both run the same
   * generator" is a weaker guarantee than "both read the same bytes".
   */
  const rows = [];
  for (let i = 0; i < ROWS; i++) {
    rows.push({
      id: i + 1,
      name: rowName(r, i + 1),
      status: pick(r, STATUSES),
      m1: intBetween(r, 0, 1000),
      m2: intBetween(r, 0, 1000),
      m3: intBetween(r, 0, 1000),
      ts: i * 137,
    });
  }

  const kpi = KPIS.map(() => intBetween(r, 100, 900));
  /* 60 sparkline points of history, so region A is full at tick 0. */
  const spark = KPIS.map((_, i) => {
    const out = [];
    let v = kpi[i];
    for (let j = 0; j < 60; j++) {
      v = Math.max(0, Math.min(1000, v + intBetween(r, -40, 40)));
      out.push(v);
    }
    return out;
  });
  /* 120 points x 2 series of history for region C. */
  const series = [0, 1].map(() => {
    const out = [];
    let v = intBetween(r, 200, 800);
    for (let j = 0; j < 120; j++) {
      v = Math.max(0, Math.min(1000, v + intBetween(r, -25, 25)));
      out.push(v);
    }
    return out;
  });

  const base = { kpiLabels: KPIS, rows, kpi, spark, series };

  const ticks = [];
  const last = kpi.slice();
  const lastSeries = series.map((s) => s[s.length - 1]);
  let logSeq = 0;

  for (let n = 0; n < TICKS; n++) {
    const events = [];

    /* Region A + C, 1 Hz -> every 10th tick. */
    if (n % 10 === 0) {
      const v = last.map((x) => Math.max(0, Math.min(1000, x + intBetween(r, -40, 40))));
      for (let i = 0; i < v.length; i++) last[i] = v[i];
      events.push({ k: 'kpi', v });

      const s = lastSeries.map((x) => Math.max(0, Math.min(1000, x + intBetween(r, -25, 25))));
      for (let i = 0; i < s.length; i++) lastSeries[i] = s[i];
      events.push({ k: 'series', v: s });
    }

    /* Region B, 2 Hz, 20 rows changed per tick (10 % churn) -> every 5th tick. */
    if (n % 5 === 0) {
      const changed = [];
      const seen = new Set();
      while (changed.length < 20) {
        const id = intBetween(r, 1, ROWS);
        if (seen.has(id)) continue;
        seen.add(id);
        changed.push({
          id,
          status: pick(r, STATUSES),
          m1: intBetween(r, 0, 1000),
          m2: intBetween(r, 0, 1000),
          m3: intBetween(r, 0, 1000),
          ts: n * TICK_MS,
        });
      }
      changed.sort((a, b) => a.id - b.id);
      events.push({ k: 'rows', r: changed });
    }

    /* Region D, 5 Hz -> every 2nd tick. */
    if (n % 2 === 0) {
      logSeq++;
      events.push({
        k: 'log',
        seq: logSeq,
        text: `${pick(r, NOUNS)}-${String(intBetween(r, 1, ROWS)).padStart(3, '0')} ${pick(r, LOG_VERBS)}`,
      });
    }

    if (events.length > 0) ticks.push({ n, e: events });
  }

  return {
    base,
    ticks,
    stats: {
      logicalUpdatesPerSecond:
        KPIS.length * 1 /* region A values */ +
        20 * 2 /* region B rows */ +
        2 * 1 /* region C points */ +
        5 /* region D entries */,
    },
  };
}

/* ------------------------------------------------------------- driver ---- */

const APPS = {
  chat: chatTicks,
  dashboard: dashboardTicks,
};

function serialize({ base, ticks }) {
  /*
   * Line 0 is the base record; lines 1..N are ticks in ascending order. One
   * file per app so there is one SHA-256 per app, which is what the run
   * manifest records (§6).
   */
  const lines = [JSON.stringify({ base })];
  for (const tick of ticks) lines.push(JSON.stringify(tick));
  return `${lines.join('\n')}\n`;
}

function main() {
  const args = process.argv.slice(2);
  const verify = args.includes('--verify');
  const only = args.filter((a) => !a.startsWith('-'));
  const names = only.length > 0 ? only : Object.keys(APPS);

  let failed = 0;

  for (const name of names) {
    const build = APPS[name];
    if (!build) {
      console.error(`unknown app ${name}; known: ${Object.keys(APPS).join(', ')}`);
      process.exit(2);
    }

    const built = build();
    const text = serialize(built);
    const sha = createHash('sha256').update(text).digest('hex');
    const dir = join(here, name);
    const jsonl = join(dir, 'ticks.jsonl');
    const shaFile = join(dir, 'ticks.jsonl.sha256');

    if (verify) {
      if (!existsSync(shaFile)) {
        console.error(`MISSING  ${shaFile}`);
        failed++;
        continue;
      }
      const want = readFileSync(shaFile, 'utf8').trim().split(/\s+/)[0];
      if (want !== sha) {
        console.error(`DRIFTED  ${name}\n  committed ${want}\n  generated ${sha}`);
        failed++;
        continue;
      }
      console.log(`ok       ${name}  ${built.ticks.length} ticks  sha256:${sha}`);
      continue;
    }

    mkdirSync(dir, { recursive: true });
    writeFileSync(jsonl, text);
    writeFileSync(shaFile, `${sha}  ticks.jsonl\n`);
    console.log(
      `wrote    ${jsonl}  ${built.ticks.length} ticks  ${text.length} bytes  sha256:${sha}`,
    );
    if (built.stats) console.log(`         ${JSON.stringify(built.stats)}`);
  }

  process.exit(failed === 0 ? 0 : 1);
}

if (resolve(process.argv[1] ?? '') === resolve(fileURLToPath(import.meta.url))) {
  main();
}
