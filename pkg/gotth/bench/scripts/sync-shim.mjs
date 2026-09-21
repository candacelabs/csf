#!/usr/bin/env node
/*
 * Copy harness/shim.js into an app's public/ tree, and prove the copy is
 * byte-identical to the source.
 *
 * equivalence-spec §2.0 requires ONE shim file, byte-identical, served by both
 * stacks, whose transfer bytes are subtracted from both stacks' client-JS
 * figures. "Byte-identical" has to be checkable rather than promised, so:
 *
 *   node scripts/sync-shim.mjs <app-dir>   copies and prints the SHA-256
 *   node scripts/sync-shim.mjs --verify    checks every app's copy, exits 1 on drift
 *
 * The copy is gitignored. The source is committed. The Next.js side serves the
 * copy from public/bench/shim.js; the gotth-live side serves the same bytes from
 * its own handler, and the two SHA-256s are recorded in the run manifest.
 */
import { createHash } from 'node:crypto';
import { mkdirSync, readFileSync, writeFileSync, existsSync } from 'node:fs';
import { dirname, join, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const benchRoot = resolve(dirname(fileURLToPath(import.meta.url)), '..');
const source = join(benchRoot, 'harness', 'shim.js');
const apps = ['counter', 'chat', 'dashboard'];

const bytes = readFileSync(source);
const sha = createHash('sha256').update(bytes).digest('hex');

const args = process.argv.slice(2);
const verify = args.includes('--verify');

function targetFor(appDir) {
  return join(appDir, 'public', 'bench', 'shim.js');
}

if (verify) {
  let bad = 0;
  for (const app of apps) {
    const target = targetFor(join(benchRoot, 'apps', app, 'next'));
    if (!existsSync(target)) {
      console.error(`MISSING  ${target}`);
      bad++;
      continue;
    }
    const got = createHash('sha256').update(readFileSync(target)).digest('hex');
    if (got !== sha) {
      console.error(`DRIFTED  ${target}\n  want ${sha}\n  got  ${got}`);
      bad++;
      continue;
    }
    console.log(`ok       ${target}`);
  }
  console.log(`shim.js  ${bytes.length} bytes  sha256:${sha}`);
  process.exit(bad === 0 ? 0 : 1);
}

const appDir = resolve(args.find((a) => !a.startsWith('-')) ?? '.');
const target = targetFor(appDir);
mkdirSync(dirname(target), { recursive: true });
writeFileSync(target, bytes);
console.log(`shim.js -> ${target}  ${bytes.length} bytes  sha256:${sha}`);
