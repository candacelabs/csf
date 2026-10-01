/*
 * §5.2's host record, and the two host conditions that can stop a run.
 *
 * "The harness records hostname, kernel, CPU model, core count, RAM, Docker
 * version, and cgroup limits into every run manifest, and records whether any
 * other Compose project is running on the host at run start. A run with
 * co-tenancy is marked contended in the manifest and is excluded from the
 * headline set but still published."
 *
 * Two things follow from docs/OPERATOR-QUESTIONS.md and are enforced here
 * rather than remembered:
 *
 *   Q-2  the bench host is `node-a`, a SHARED host that is not quiescent.
 *        Non-quiescence is disclosed, not claimed away, so every run carries
 *        load average, free memory and the container list at run start.
 *
 *   Q-7  "measured runs are skipped, not degraded, while an active GPU
 *        streaming session is present, and the check is part of the harness
 *        rather than a habit." A GPU session on this host is the single largest
 *        contention source. Waiting is the only permitted mitigation: this
 *        module never stops, restarts or reconfigures anything.
 */
import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { cpus, freemem, hostname, loadavg, totalmem } from 'node:os';

function run(cmd, args) {
  try {
    return execFileSync(cmd, args, { encoding: 'utf8', timeout: 15_000 }).trim();
  } catch {
    return '';
  }
}

function readOr(path, fallback = '') {
  try {
    return readFileSync(path, 'utf8').trim();
  } catch {
    return fallback;
  }
}

/** Containers running on the host right now, with their compose project. */
export function containers() {
  const raw = run('docker', [
    'ps',
    '--format',
    '{{.Names}}\t{{.Image}}\t{{.Label "com.docker.compose.project"}}',
  ]);
  if (raw === '') return [];
  return raw.split('\n').map((line) => {
    const [name, image, project] = line.split('\t');
    return { name, image, project: project || '' };
  });
}

/**
 * The substrings that identify a GPU streaming container, lowercased.
 *
 * WHICH container is the GPU session is a property of the deployment, not of
 * this library, so it is configuration: BENCH_GPU_SESSION_MATCH is a
 * comma-separated list of substrings matched against each container's name and
 * its image. A deployment whose streaming container is called something else
 * names it there rather than editing this file.
 *
 * An empty or unset value falls back to the default rather than to an empty
 * list, because an empty match list is a Q-7 gate that blocks nothing — the
 * fail-open shape this whole module exists to refuse. Substrings rather than
 * prefixes, so a Compose-prefixed name (`<project>-steam-1`) is caught by the
 * default too.
 */
export const DEFAULT_GPU_SESSION_MATCH = ['steam', 'selkies'];

export function gpuSessionMatch(env = process.env) {
  const configured = (env.BENCH_GPU_SESSION_MATCH ?? '')
    .split(',')
    .map((s) => s.trim().toLowerCase())
    .filter((s) => s !== '');
  return configured.length > 0 ? configured : DEFAULT_GPU_SESSION_MATCH;
}

/**
 * Q-7's gate. Returns the blocking containers, or an empty array.
 *
 * "Active" is taken conservatively: a running GPU streaming container blocks,
 * whether or not somebody is currently streaming through it, because the
 * harness cannot see a streaming session from outside and guessing in the
 * permissive direction is how a contended run gets published as a clean one.
 */
export function steamBlockers(list = containers(), env = process.env) {
  const needles = gpuSessionMatch(env);
  return list.filter((c) => {
    const name = (c.name ?? '').toLowerCase();
    const image = (c.image ?? '').toLowerCase();
    return needles.some((n) => name.includes(n) || image.includes(n));
  });
}

/**
 * §5.2's co-tenancy flag.
 *
 * The bench's own project is excluded; everything else counts. On this host
 * that is never empty (Q-2), so `contended` is expected to be true and the
 * report says so in its body — the flag exists to make the condition visible in
 * the data, not to pretend it can be avoided.
 */
export function coTenants(benchProject, list = containers()) {
  return list.filter((c) => c.project !== benchProject);
}

export function hostState({ benchProject = 'gotth-live-bench' } = {}) {
  const list = containers();
  const others = coTenants(benchProject, list);
  return {
    hostname: hostname(),
    kernel: run('uname', ['-r']),
    cpuModel: cpus()[0]?.model ?? 'unknown',
    cores: cpus().length,
    ramBytes: totalmem(),
    freeBytes: freemem(),
    loadavg: loadavg(),
    dockerVersion: run('docker', ['version', '--format', '{{.Server.Version}}']),
    cgroupVersion: readOr('/sys/fs/cgroup/cgroup.controllers') !== '' ? 'v2' : 'v1',
    containers: list,
    coTenants: others.map((c) => `${c.name} (${c.project || 'no project'})`),
    /* §5.2: a run with co-tenancy is marked contended, excluded from the
       headline set, and still published. */
    contended: others.length > 0,
    steamActive: steamBlockers(list).length > 0,
    at: new Date().toISOString(),
  };
}

/**
 * The pre-run check. Throws on the one condition Q-7 says must stop a run;
 * returns the state otherwise, contended flag and all.
 */
export function requireRunnableHost(options = {}) {
  const state = hostState(options);
  if (state.steamActive) {
    throw new Error(
      'bench: a GPU streaming container is running on this host.\n' +
        'docs/OPERATOR-QUESTIONS.md Q-7: measured runs are SKIPPED, not degraded, ' +
        'while a GPU session is present. Waiting is the only permitted mitigation — ' +
        'do not stop, restart or reconfigure a co-tenant container to make a run cleaner.',
    );
  }
  return state;
}
