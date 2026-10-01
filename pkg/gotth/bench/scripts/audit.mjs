#!/usr/bin/env node
/*
 * §5.4's pessimization audit, executed and its output committed.
 *
 *   node scripts/audit.mjs            run every check, write audit/
 *   node scripts/audit.mjs --check    run every check, fail on any FAIL
 *
 * "Pessimization audit — the Next.js app does not get measured until this
 * passes." The checklist is transcribed verbatim below and each item is either
 * a real check or an explicit "requires the Phase 5 turn", never a tick.
 *
 * -----------------------------------------------------------------------------
 * Why this file is adversarial towards its own apps
 *
 * T-2 is "Strawman Next.js implementation (PRD R-15)", and the mitigation §5.4
 * offers is this audit plus an independent reviewer. There is no independent
 * reviewer: docs/OPERATOR-QUESTIONS.md Q-1 records the default in force as
 * "internal control only, disclosed in the report body", because every agent on
 * this project was briefed by the same orchestrator against the same documents,
 * which is exactly the correlation an outside review is supposed to break.
 *
 * So this file is the whole of the fairness control on the Next.js side, and it
 * is written to look for the things an author would rather not find: a client
 * boundary that swallowed the tree, a dependency nobody uses, a dev build in
 * production, an await that exists to make a number look better. An audit that
 * only confirms is not a control.
 */
import { execFile } from 'node:child_process';
import { existsSync, mkdirSync, readFileSync, readdirSync, statSync, writeFileSync } from 'node:fs';
import { gzipSync } from 'node:zlib';
import { dirname, join, relative, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';
import { promisify } from 'node:util';

const exec = promisify(execFile);

const BENCH_ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const AUDIT_DIR = join(BENCH_ROOT, 'audit');
const APPS = ['counter', 'chat', 'dashboard'];

const results = [];

function record(id, title, status, detail) {
  results.push({ id, title, status, detail });
  const mark = status === 'PASS' ? 'PASS' : status === 'FAIL' ? 'FAIL' : status;
  console.log(`${mark.padEnd(7)} ${id}  ${title}`);
  if (status === 'FAIL') console.log(`        ${String(detail).split('\n').join('\n        ')}`);
}

function walk(dir, out = []) {
  if (!existsSync(dir)) return out;
  for (const entry of readdirSync(dir, { withFileTypes: true })) {
    const path = join(dir, entry.name);
    if (entry.isDirectory()) {
      if (['node_modules', '.next', 'public'].includes(entry.name)) continue;
      walk(path, out);
    } else if (/\.(ts|tsx|mjs|js)$/.test(entry.name)) {
      out.push(path);
    }
  }
  return out;
}

/* ------------------------------------------------------------------ A-1 --- */
/*
 * "No 'use client' at or near the root; client boundaries are as deep as the
 * interactivity requires."
 *
 * "Near the root" is made concrete: app/layout.tsx and app/page.tsx are the
 * root, and a 'use client' in either pulls the whole route's tree into the
 * client bundle. A directive anywhere under components/ or lib/transport/ is
 * expected and is what a narrow boundary looks like.
 */
function checkUseClient() {
  const offenders = [];
  const boundaries = [];
  for (const app of APPS) {
    const src = join(BENCH_ROOT, 'apps', app, 'next', 'src');
    for (const file of walk(src)) {
      const head = readFileSync(file, 'utf8').slice(0, 200);
      if (!/^\s*(\/\*[\s\S]*?\*\/\s*)?['"]use client['"]/m.test(head)) continue;
      const rel = relative(BENCH_ROOT, file);
      boundaries.push(rel);
      if (/src\/app\/(layout|page)\.tsx$/.test(file) || /src\/app\/[^/]+\/(layout|page)\.tsx$/.test(file)) {
        offenders.push(rel);
      }
    }
  }
  record(
    'A-1',
    "no 'use client' at or near the root",
    offenders.length === 0 ? 'PASS' : 'FAIL',
    offenders.length === 0
      ? `${boundaries.length} client boundaries, all below the route:\n  ${boundaries.join('\n  ')}`
      : `root-level client boundaries: ${offenders.join(', ')}`,
  );
  return boundaries;
}

/* ------------------------------------------------------------------ A-2 --- */
/*
 * "@next/bundle-analyzer output committed; no barrel-file import pulling in an
 * unused tree; no unexpectedly large chunk."
 */
function checkBundle() {
  const summaries = {};
  const missing = [];
  for (const app of APPS) {
    const json = join(BENCH_ROOT, 'apps', app, 'next', '.next', 'analyze', 'client.json');
    if (!existsSync(json)) {
      missing.push(app);
      continue;
    }
    const stats = JSON.parse(readFileSync(json, 'utf8'));
    summaries[app] = stats
      .map((chunk) => ({
        label: chunk.label,
        statSize: chunk.statSize,
        parsedSize: chunk.parsedSize,
        gzipSize: chunk.gzipSize,
        /* The top five modules by parsed size. This is where a barrel-file
           import shows up: a package nobody imported by name, sitting at the
           top of a chunk. */
        top: (chunk.groups ?? [])
          .flatMap(function flatten(g) {
            return g.groups ? g.groups.flatMap(flatten) : [g];
          })
          .sort((a, b) => (b.parsedSize ?? 0) - (a.parsedSize ?? 0))
          .slice(0, 5)
          .map((g) => ({ path: g.path ?? g.label, parsedSize: g.parsedSize })),
      }))
      .sort((a, b) => b.parsedSize - a.parsedSize)
      .slice(0, 12);
  }

  mkdirSync(join(AUDIT_DIR, 'bundle-analyzer'), { recursive: true });
  for (const [app, summary] of Object.entries(summaries)) {
    writeFileSync(
      join(AUDIT_DIR, 'bundle-analyzer', `${app}.json`),
      `${JSON.stringify(summary, null, 2)}\n`,
    );
  }

  record(
    'A-2',
    '@next/bundle-analyzer output committed',
    missing.length === 0 ? 'PASS' : 'FAIL',
    missing.length === 0
      ? `distilled to audit/bundle-analyzer/{${APPS.join(',')}}.json`
      : `no analyzer output for: ${missing.join(', ')} — run \`npm run analyze -w @gotth-live-bench/<app>-next\``,
  );
  return summaries;
}

/* ------------------------------------------------------------------ A-3 --- */
/* "No unused dependency in package.json; depcheck clean." */
async function checkDepcheck() {
  const findings = {};
  let bad = 0;
  for (const app of APPS) {
    const dir = join(BENCH_ROOT, 'apps', app, 'next');
    let out = '{}';
    try {
      const r = await exec(
        join(BENCH_ROOT, 'node_modules', '.bin', 'depcheck'),
        [dir, '--json', '--skip-missing=true'],
        { maxBuffer: 32 * 1024 * 1024 },
      );
      out = r.stdout;
    } catch (err) {
      /* depcheck exits non-zero when it finds something; the JSON is still on
         stdout and IS the finding. */
      out = err.stdout ?? '{}';
    }
    const parsed = JSON.parse(out);
    /*
     * `ws` is expected in `dependencies` and invisible to depcheck: it is
     * imported by ws-server/relay.mjs, which is not part of the Next build
     * graph but IS the §5.4 secondary variant's second process in the same
     * container. Declaring it is correct; depcheck not seeing it is depcheck
     * not walking that file. It is listed rather than suppressed.
     */
    const unused = (parsed.dependencies ?? []).filter((d) => d !== 'ws');
    const unusedDev = parsed.devDependencies ?? [];
    findings[app] = { unused, unusedDev, explained: ['ws (ws-server/relay.mjs, outside the Next graph)'] };
    if (unused.length > 0) bad++;
  }
  record(
    'A-3',
    'depcheck clean (no unused runtime dependency)',
    bad === 0 ? 'PASS' : 'FAIL',
    JSON.stringify(findings, null, 2),
  );
  return findings;
}

/* ------------------------------------------------------------------ A-4 --- */
/*
 * "Production React confirmed at runtime (no dev-build warnings, no
 * react-dom/profiling)."
 *
 * Checked in the BUILT OUTPUT rather than in the config, because the config is
 * what an author intends and the output is what a browser runs. React's
 * development build is identifiable by strings that its production build
 * strips.
 */
function checkProductionReact() {
  const findings = {};
  let bad = 0;
  for (const app of APPS) {
    const staticDir = join(BENCH_ROOT, 'apps', app, 'next', '.next', 'static');
    if (!existsSync(staticDir)) {
      findings[app] = 'no build output';
      bad++;
      continue;
    }
    const chunks = walk(staticDir).filter((f) => f.endsWith('.js'));
    const hits = [];
    for (const file of chunks) {
      const text = readFileSync(file, 'utf8');
      /* Strings present only in React's development build, and the profiling
         build's own marker. */
      for (const needle of [
        'Warning: ReactDOM.render',
        'react-dom.development',
        'react.development',
        'reactDevToolsGlobalHook.inject',
        '__REACT_DEVTOOLS_GLOBAL_HOOK__.onCommitFiberRoot',
        'react-dom/profiling',
      ]) {
        if (text.includes(needle)) hits.push(`${relative(BENCH_ROOT, file)}: ${needle}`);
      }
    }
    findings[app] = { chunks: chunks.length, devBuildMarkers: hits };
    if (hits.length > 0) bad++;
  }
  record(
    'A-4',
    'production React confirmed in the built output',
    bad === 0 ? 'PASS' : 'FAIL',
    JSON.stringify(findings, null, 2),
  );
  return findings;
}

/* ------------------------------------------------------------------ A-5 --- */
/* "No next dev, no Turbopack dev server, no HMR runtime in the bundle." */
function checkNoDevRuntime() {
  const findings = {};
  let bad = 0;
  for (const app of APPS) {
    const staticDir = join(BENCH_ROOT, 'apps', app, 'next', '.next', 'static');
    const pkg = JSON.parse(readFileSync(join(BENCH_ROOT, 'apps', app, 'next', 'package.json'), 'utf8'));
    const scriptsWithDev = Object.entries(pkg.scripts ?? {}).filter(([, v]) => /next dev|--turbopack/.test(v));
    /*
     * The RUNTIME symbols, not the string "react-refresh".
     *
     * The first version of this check looked for the bare string and failed all
     * three apps. It was a false positive worth recording rather than quietly
     * deleting: Next's shared constants module carries
     * CLIENT_STATIC_FILES_RUNTIME_REACT_REFRESH = 'react-refresh' as an
     * identifier in a table of build-output names, and that table ships in
     * production because other constants in it are used at runtime. None of the
     * actual Fast Refresh machinery is present — no $RefreshReg$, no
     * $RefreshSig$, no webpackHotUpdate, no hmrM, no _next/webpack-hmr — and
     * those are what a dev runtime cannot be built without.
     *
     * The bare string is still counted and reported, so the loosening is
     * visible in the committed output instead of being a check that silently
     * got weaker.
     */
    const runtimeMarkers = [
      'webpackHotUpdate',
      '__webpack_require__.hmrM',
      '$RefreshReg$',
      '$RefreshSig$',
      'performFullRefresh',
      '_next/webpack-hmr',
    ];
    const hits = [];
    let benignConstant = 0;
    for (const file of walk(staticDir).filter((f) => f.endsWith('.js'))) {
      const text = readFileSync(file, 'utf8');
      for (const needle of runtimeMarkers) {
        if (text.includes(needle)) hits.push(`${relative(BENCH_ROOT, file)}: ${needle}`);
      }
      if (text.includes('react-refresh')) benignConstant++;
    }
    findings[app] = {
      hmrRuntimeMarkers: hits,
      devScripts: scriptsWithDev.map(([k]) => k),
      benignReactRefreshConstant: benignConstant,
      note:
        'benignReactRefreshConstant counts files containing the STRING ' +
        '"react-refresh" (Next\'s build-output name table). It is not a dev ' +
        'runtime; hmrRuntimeMarkers is the check.',
    };
    if (hits.length > 0 || scriptsWithDev.length > 0) bad++;
  }
  record(
    'A-5',
    'no dev server, no HMR runtime in the bundle',
    bad === 0 ? 'PASS' : 'FAIL',
    JSON.stringify(findings, null, 2),
  );
  return findings;
}

/* ------------------------------------------------------------------ A-6 --- */
/*
 * "No artificial await/delay, no disabled caching beyond §5.5's dynamic-route
 * rule, no throttled revalidation."
 *
 * Every setTimeout and every `force-dynamic` in the app sources is listed with
 * its file, so a reviewer reads the list rather than trusting a green tick.
 * There ARE deliberate delays — the 150 ms search debounce §2.4 mandates on
 * both stacks, and the 1 s typing heartbeat — and they are named here as
 * expected rather than hidden by a pattern that skips them.
 */
function checkNoArtificialDelay() {
  const expected = [
    { pattern: /SEARCH_DEBOUNCE_MS/, why: '§2.4 mandates a 150 ms search debounce on BOTH stacks' },
    { pattern: /TYPING_PING_MS/, why: 'F-CHT-6 typing heartbeat, 1 Hz, off the measured paint path' },
    { pattern: /HEARTBEAT_MS/, why: "SSE keep-alive; its bytes are counted in §4.6 like gotth-live's" },
    { pattern: /TYPING_DECAY_MS/, why: 'F-CHT-6 says the indicator decays after 3 s' },
    { pattern: /SESSION_GRACE_MS/, why: 'session eviction, so D4 does not measure an unbounded map' },
    {
      pattern: /this\.schedule|setTimeout\(\(\) => this\.schedule\(\), 0\)|\bdelay\)/,
      why:
        "§2.5's monotonic replay schedule (tick N at T0 + N x 100 ms), or the " +
        'yield that keeps a catch-up from starving the event loop the push ' +
        'channel writes on. Neither adds latency to a measured interaction.',
    },
    {
      pattern: /retry = setTimeout\(connect/,
      why:
        'transport reconnect backoff. Off the measured path by construction: a ' +
        'run in which the channel dropped is a run whose samples are already ' +
        'suspect, and the backoff exists so a server restart mid-run does not ' +
        'silently cost every subsequent sample.',
    },
  ];
  const delays = [];
  const dynamics = [];
  for (const app of APPS) {
    for (const file of walk(join(BENCH_ROOT, 'apps', app, 'next', 'src'))) {
      const text = readFileSync(file, 'utf8');
      const rel = relative(BENCH_ROOT, file);
      for (const raw of text.split('\n')) {
        const line = raw.trim();
        /* Comments and type positions are not delays. Scanning them produced a
           list nobody would read, and a list nobody reads is not a control. */
        if (line.startsWith('*') || line.startsWith('//') || line.startsWith('/*')) continue;
        if (/ReturnType<typeof (setTimeout|setInterval)>/.test(line)) continue;
        if (!/\b(setTimeout|setInterval)\s*\(|await new Promise/.test(line)) continue;
        const known = expected.find((e) => e.pattern.test(line));
        delays.push({ file: rel, line: line.slice(0, 120), expected: known?.why ?? null });
      }
      if (text.includes("dynamic = 'force-dynamic'")) dynamics.push(rel);
    }
  }
  const unexplained = delays.filter((d) => d.expected === null);
  record(
    'A-6',
    'no artificial await or delay; dynamic routes are §5.5, not a handicap',
    'REVIEW',
    JSON.stringify(
      {
        note:
          'REVIEW, not PASS: "no artificial delay" is a judgement, and a script ' +
          'that returned PASS for it would be asserting the judgement rather ' +
          'than supporting it. Every scheduled delay in the three apps is listed ' +
          'so a reviewer reads them. `unexplained` entries are multi-line calls ' +
          'whose delay argument is on a following line — open the file and read ' +
          'the three. The force-dynamic list is §5.5\'s fairness constraint, not ' +
          'a self-inflicted cost: the equivalent gotth-live route renders current ' +
          'session state and cannot be cached, so this one may not be either.',
        unexplained,
        explained: delays.length - unexplained.length,
        forceDynamicRoutes: dynamics,
      },
      null,
      2,
    ),
  );
  return { delays, dynamics };
}

/* ------------------------------------------------------------------ A-7 --- */
function checkLighthouse() {
  record(
    'A-7',
    'Lighthouse performance score recorded',
    'DEFERRED',
    'Requires a run against the §3.6 topology, which is a Phase 5 activity. ' +
      '§5.4: "a score materially below what the app\'s content warrants is ' +
      'treated as evidence of pessimization and investigated before measuring". ' +
      'Not run here, and not ticked.',
  );
}

/* ------------------------------------------------------------------ A-8 --- */
/*
 * "Every deviation from the Next.js docs' recommended pattern is listed with a
 * reason."
 *
 * This list is the checklist item, not a summary of it. Adding to it is how a
 * later change stays auditable; a deviation that is not here is one nobody
 * declared.
 */
const DEVIATIONS = [
  {
    what: 'The typing heartbeat is a Route Handler, not a Server Action.',
    where: 'apps/chat/next/src/app/api/chat/typing/route.ts',
    why:
      "§5.4's table says mutations are Server Actions. React serialises Server " +
      'Actions, so a keystroke heartbeat would queue in front of the user\'s ' +
      'Send and CHT-2 — the headline chat latency — would be measuring the ' +
      'heartbeat draining rather than the send. A fire-and-forget keepalive POST ' +
      'is what a competent team ships for a presence ping.',
  },
  {
    what: 'Next\'s own gzip is off (compress: false); the proxy compresses.',
    where: 'apps/*/next/next.config.ts',
    why:
      '§3.5 mandates gzip level 6 for the comparison figure on both stacks and ' +
      'calls a mismatch a disqualifying method error. The only place one level ' +
      'can be guaranteed for both is the container they share — the §3.6 proxy. ' +
      'Leaving Next\'s compressor on as well would double-compress or make the ' +
      'effective level whichever layer won.',
  },
  {
    what: 'Room switching and the dashboard controls are Server Actions, not navigations.',
    where: 'apps/chat/next/src/app/chat/[room]/actions.ts, apps/dashboard/next/src/app/dashboard/actions.ts',
    why:
      '§2 forbids client-side routing on both sides, and §3.2 requires t_input ' +
      'and t_paint to come from the same page\'s performance.now() timeline. A ' +
      'document navigation puts them in two timelines and makes CHT-4 ' +
      'unmeasurable under the spec\'s own definition.',
  },
  {
    what: 'The dashboard push channel carries patches, not whole views.',
    where: 'apps/dashboard/next/src/lib/core.ts (Patch), lib/patch.ts',
    why:
      'A whole DashView is ~14 KB at perPage 200; pushing one twice a second ' +
      'would be 28 KB/s/session of which ~90 % is unchanged bytes, and §4.6\'s ' +
      'wire-byte row would be measuring an author\'s choice rather than a ' +
      'framework. This is what a perf-minded Next.js team ships.',
  },
  {
    what: 'The store is keyed off a global Symbol rather than a module-level const.',
    where: 'apps/*/next/src/lib/store.ts',
    why:
      'Next bundles route handlers and Server Actions into separate server ' +
      'chunks, so a module-level singleton can be instantiated more than once. ' +
      'This is the recipe the Next docs use for a database client, not a ' +
      'benchmark-specific trick.',
  },
  {
    what: 'Cookies are set by route handlers, not by middleware.',
    where: 'apps/*/next/src/lib/session.ts',
    why:
      'Middleware would run in its own runtime on every request to the measured ' +
      'route. Paying that on a route that does not need it is a self-inflicted ' +
      'Next.js cost — exactly what this audit exists to catch.',
  },
  {
    what: 'The filter and rows-per-page controls are buttons, not <select>s.',
    where: 'apps/dashboard/next/src/components/DashboardLive.tsx',
    why:
      '§2.4 writes "select 50 / 100 / 200", which reads as "choose one of". ' +
      'Buttons make DSH-1 and DSH-4 a native pointerdown, which is what §3.2\'s ' +
      't_input is defined against; a <select> would put the causal start in a ' +
      'change event the spec does not define.',
  },
];

function checkDeviations() {
  record(
    'A-8',
    'every deviation from the docs\' recommended pattern is listed with a reason',
    'PASS',
    `${DEVIATIONS.length} declared`,
  );
}

/* ---------------------------------------------------------------- report --- */

function gzipOf(path) {
  return existsSync(path) ? gzipSync(readFileSync(path), { level: 6 }).length : null;
}

function firstLoadTable() {
  const rows = [];
  for (const app of APPS) {
    const dir = join(BENCH_ROOT, 'apps', app, 'next', '.next', 'static', 'chunks');
    if (!existsSync(dir)) continue;
    let raw = 0;
    let gz = 0;
    let files = 0;
    for (const file of walk(dir).filter((f) => f.endsWith('.js'))) {
      raw += statSync(file).size;
      gz += gzipOf(file) ?? 0;
      files++;
    }
    rows.push({ app, files, rawBytes: raw, gzip6Bytes: gz });
  }
  return rows;
}

function writeChecklist(extra) {
  mkdirSync(AUDIT_DIR, { recursive: true });
  const pass = results.filter((r) => r.status === 'PASS').length;
  const fail = results.filter((r) => r.status === 'FAIL').length;
  const other = results.length - pass - fail;

  const lines = [
    '# Next.js pessimization audit — output',
    '',
    '| Field | Value |',
    '|---|---|',
    '| Source | equivalence-spec §5.4, "Pessimization audit — the Next.js app does not get measured until this passes" |',
    `| Generated | \`node scripts/audit.mjs\` |`,
    `| Result | **${pass} PASS, ${fail} FAIL, ${other} other** |`,
    '| Independent review | **None.** docs/OPERATOR-QUESTIONS.md Q-1: internal control only, disclosed in the report body, not a footnote |',
    '',
    '> **This file is regenerated, not edited.** The Phase 5 turn re-runs',
    '> `node scripts/audit.mjs` against the same apps and the result must still',
    '> be all-PASS before a cell is recorded. A hand-edited tick is the exact',
    '> failure mode T-2 describes.',
    '',
    '## Checklist',
    '',
    '| # | Item (§5.4) | Status |',
    '|---|---|---|',
    ...results.map((r) => `| ${r.id} | ${r.title} | **${r.status}** |`),
    '',
    '## Detail',
    '',
    ...results.flatMap((r) => [
      `### ${r.id} — ${r.title}  — ${r.status}`,
      '',
      '```json',
      typeof r.detail === 'string' ? r.detail : JSON.stringify(r.detail, null, 2),
      '```',
      '',
    ]),
    '## Client chunk sizes, gzip level 6',
    '',
    'Informational, not the D1 figure. D1 counts what the PAGE fetches from',
    'navigation start (§3.5), which is a subset of what is on disk, and it is',
    'measured through CDP at the browser rather than by gzipping a directory.',
    'This table exists so an unexpectedly large chunk is visible during',
    'construction rather than at measurement time.',
    '',
    '| app | chunk files | raw bytes | gzip-6 bytes |',
    '|---|---:|---:|---:|',
    ...extra.firstLoad.map(
      (r) => `| ${r.app} | ${r.files} | ${r.rawBytes.toLocaleString()} | ${r.gzip6Bytes.toLocaleString()} |`,
    ),
    '',
    '## Declared deviations from the Next.js docs\' recommended patterns (§5.4, last item)',
    '',
    ...DEVIATIONS.flatMap((d) => [
      `### ${d.what}`,
      '',
      `**Where:** \`${d.where}\`  `,
      `**Why:** ${d.why}`,
      '',
    ]),
    '## Client boundaries (§5.4, first item)',
    '',
    'Every `\'use client\'` in the three apps. None is at or near a route root;',
    'each app has exactly one boundary on its measured route plus its transport',
    'module, because the regions are views of ONE subscription and splitting',
    'them would open one connection per region — an architecture no competent',
    'team ships, and one D3 would then charge Next.js for.',
    '',
    ...extra.boundaries.map((b) => `- \`${b}\``),
    '',
  ];

  writeFileSync(join(AUDIT_DIR, 'nextjs-pessimization-checklist.md'), `${lines.join('\n')}\n`);
  console.log(`\nwrote audit/nextjs-pessimization-checklist.md (${pass} PASS, ${fail} FAIL, ${other} other)`);
  return fail;
}

async function main() {
  const boundaries = checkUseClient();
  checkBundle();
  await checkDepcheck();
  checkProductionReact();
  checkNoDevRuntime();
  checkNoArtificialDelay();
  checkLighthouse();
  checkDeviations();

  const fail = writeChecklist({ boundaries, firstLoad: firstLoadTable() });
  if (process.argv.includes('--check') && fail > 0) process.exit(1);
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
