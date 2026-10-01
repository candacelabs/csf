#!/usr/bin/env node
/*
 * Copy harness/ready.js into each bench app's gotth/bench/ tree.
 *
 * The other half of D-6 (docs/reviews/deduplication.md §6): §3.3's ready signal
 * was three byte-identical tracked copies with no source, no sync and nothing
 * that failed when they drifted. This is the sync; the verify is
 * scripts/verify-ready.sh, and it is a shell script rather than a --verify flag
 * here for a reason that is spelled out below and in bench/README.md.
 *
 *   node scripts/sync-ready.mjs             rewrite all three copies
 *   node scripts/sync-ready.mjs <app-dir>   rewrite one, e.g. apps/chat/gotth
 *   sh scripts/verify-ready.sh              check them; exits 1 on drift
 *
 * -----------------------------------------------------------------------------
 * Why this does NOT mirror sync-shim.mjs's gitignore-the-copies model
 *
 * shim.js's copies land in apps/<app>/next/public/ and are read by Node at run
 * time, so gitignoring them costs nothing: the stack that serves them is a
 * stack that had to run `npm` to exist at all. ready.js's copies are consumed
 * by `//go:embed bench/ready.js`, and go:embed is resolved by the COMPILER
 * against files that must be on disk before any build step runs. Gitignoring
 * them makes a clean checkout fail to compile:
 *
 *   bench.go:42:12: pattern bench/ready.js: no matching files found
 *
 * — in dis-gotth-live:latest, which has no node and therefore cannot run this
 * script to repair itself, and which is the image ci.sh builds all three bench
 * modules in (`bench/apps/<app>/gotth`). So the copies stay tracked and the
 * verification, not the gitignore, is what makes them honest.
 */
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const benchRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const source = join(benchRoot, 'harness', 'ready.js');
const apps = ['counter', 'chat', 'dashboard'];

const bytes = readFileSync(source);
const sha = createHash('sha256').update(bytes).digest('hex');

function targetFor(appDir) {
  return join(appDir, 'bench', 'ready.js');
}

const args = process.argv.slice(2);
const named = args.find((a) => !a.startsWith('-'));
const targets = named
  ? [targetFor(resolve(named))]
  : apps.map((app) => targetFor(join(benchRoot, 'apps', app, 'gotth')));

for (const target of targets) {
  mkdirSync(dirname(target), { recursive: true });
  writeFileSync(target, bytes);
  console.log(`ready.js -> ${target}`);
}
console.log(`ready.js  ${bytes.length} bytes  sha256:${sha}`);
