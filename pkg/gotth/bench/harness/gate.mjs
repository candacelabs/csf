/*
 * The gates. Nothing in this tree records a measured cell until all of them
 * pass, and each one refuses in the spec's own words rather than with a
 * generic error, because the person who hits the refusal is the person who
 * needs to read the clause.
 *
 * G-OPERATOR   §5.7 — "Bench runs are operator-initiated. The harness refuses
 *                     to start unless explicitly invoked."
 * G-DRIVER     §3.6 — the 10-real-tabs vs 10-synthetic validation, mandatory
 *                     before any 1k number is quoted. T-9.
 * G-CONFORM    §2.5 — the fixture conformance test: both servers must emit the
 *                     same logical state for tick N. "This test gates the
 *                     measurement: it must pass before any run counts."
 * G-TLS        §3.6 — no TLS listener in the measured container, equal proxy
 *                     image digests. T-21.
 * G-PHASE3     Appendix B — QA3-1/2/3 are safety-chosen defaults, not measured.
 *                     A Phase 3 re-tune landing after Phase 5 measurement has
 *                     begun forces full re-collection of the affected cells, so
 *                     the cheap way to avoid that is to finish Phase 3 first.
 *
 * -----------------------------------------------------------------------------
 * G-DRIVER is the one gate that is not a boolean somebody writes down
 *
 * Every other gate here reads `data/gates.json`, which is a file a person
 * edits. That is fine for a check whose evidence lives elsewhere, and it is not
 * fine for this one: §3.6 makes the 10-tab comparison the thing that decides
 * whether the 1k number is "an assertion about a synthetic client, not about
 * sessions", so the gate reads the four measured numbers and re-derives the
 * verdict itself. `gates.json` cannot open it — the derived value is applied
 * AFTER the merge, so a hand-written `{"driverValidation":{"pass":true}}` is
 * overwritten by whatever the artifact actually says.
 *
 * Three ways it refuses, all of them the same refusal text plus a reason:
 *
 *   absent   no bench/data/driver-validation.json, or one that says "not run";
 *   stale    the artifact is for a different app, or for a DIFFERENT DRIVER —
 *            it records the sha256 of harness/driver.mjs, so editing the driver
 *            invalidates its own validation without anybody remembering to;
 *   failing  either stack's two per-session figures differ by more than 10 %.
 */
import { createHash } from 'node:crypto';
import { existsSync, readFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';

import { BENCH_ROOT, DATA_DIR } from './manifest.mjs';

const HERE = dirname(fileURLToPath(import.meta.url));

export const GATE_FILE = join(DATA_DIR, 'gates.json');

/** §3.6's published artifact: the four numbers, or the reason there are none. */
export const DRIVER_VALIDATION_FILE = join(DATA_DIR, 'driver-validation.json');

/** §3.6: "If the per-session figures differ by more than 10 % on either stack". */
export const DRIVER_TOLERANCE = 0.1;

/** The stacks the gate requires a pair of figures for. §3.6: "on both stacks". */
export const VALIDATED_STACKS = ['gotth', 'next'];

const DRIVER_REFUSAL =
  '§3.6 driver validation has not been run: per-session memory with 10 real ' +
  'Chromium tabs vs 10 synthetic sessions, on both stacks, within 10 %. ' +
  'Without it the 1k number is an assertion about a synthetic client, not ' +
  'about sessions (T-9).';

const DEFAULTS = {
  operator: { pass: false, note: 'no operator invocation recorded' },
  driverValidation: { pass: false, note: DRIVER_REFUSAL },
  conformance: {
    pass: false,
    note:
      '§2.5 conformance test has not been run: both servers must render the same ' +
      'DOM at a fixed tick under a paused clock.',
  },
  phase3: {
    pass: false,
    note:
      'Appendix B: QA3-1 (coalesce_flush_at), QA3-2 (MinResyncInterval / ' +
      'ResyncBurst) and QA3-3 (provenance-log volume) are still safety-chosen ' +
      'defaults. Two of them move numbers this spec publishes, so a re-tune after ' +
      'Phase 5 has begun forces full re-collection under §12.',
  },
};

/**
 * §3.6's 10 % comparison.
 *
 * The denominator is the BROWSER figure, not the mean and not the larger of the
 * two, because the browser is the quantity the driver is claiming to represent:
 * "if the per-session figures differ by more than 10 %, the driver
 * misrepresents a browser". Dividing by the browser figure is also the stricter
 * of the two readings whenever the synthetic side is the larger one, which is
 * the direction that would flatter a stack by inventing per-session memory that
 * a real tab does not cost. §12's "pick the reading least favourable to
 * gotth-live" is discharged by taking the stricter one in both directions.
 *
 * A browser figure of zero or below makes the ratio undefined; that is a
 * refusal, not a pass, because a cell whose M(N) did not exceed its M(0) is a
 * cell that measured nothing.
 */
export function withinTolerance(browserBytes, syntheticBytes, tolerance = DRIVER_TOLERANCE) {
  if (typeof browserBytes !== 'number' || typeof syntheticBytes !== 'number') {
    return { within: false, relative: null, reason: 'a figure is missing' };
  }
  if (!(browserBytes > 0)) {
    return {
      within: false,
      relative: null,
      reason:
        `the 10-real-tab figure is ${browserBytes} B/session; a non-positive baseline ` +
        'makes the comparison undefined and a cell whose M(N) did not exceed its ' +
        'M(0) measured nothing',
    };
  }
  const relative = Math.abs(syntheticBytes - browserBytes) / browserBytes;
  return {
    within: relative <= tolerance,
    relative,
    tolerance,
    browserBytes,
    syntheticBytes,
    deltaBytes: syntheticBytes - browserBytes,
  };
}

/** The sha256 of the driver a validation result is a statement about. */
export function currentDriverSha256() {
  const path = join(HERE, 'driver.mjs');
  if (!existsSync(path)) return null;
  return createHash('sha256').update(readFileSync(path)).digest('hex');
}

export function readDriverValidation(path = DRIVER_VALIDATION_FILE) {
  if (!existsSync(path)) return null;
  try {
    return JSON.parse(readFileSync(path, 'utf8'));
  } catch {
    return null;
  }
}

/**
 * G-DRIVER's verdict, as a pure function of the artifact and the run's context.
 *
 * Returns the same `{ pass, note }` shape every other gate has, so requireGates
 * does not need to know that this one is derived.
 */
export function evaluateDriverValidation(
  artifact,
  { app = null, variant = null, driverSha256 = currentDriverSha256() } = {},
) {
  const refuse = (why) => ({ pass: false, note: `${DRIVER_REFUSAL}\n    ${why}`, detail: why });

  if (artifact === null) {
    return refuse(`no artifact at ${DRIVER_VALIDATION_FILE.replace(BENCH_ROOT, 'bench')}.`);
  }
  if (artifact.status !== 'run') {
    return refuse(
      `the artifact records status ${JSON.stringify(artifact.status ?? null)}: ` +
        `${artifact.reason ?? 'no reason recorded'}`,
    );
  }
  if (driverSha256 && artifact.driverSha256 && artifact.driverSha256 !== driverSha256) {
    return refuse(
      'the artifact validated a DIFFERENT harness/driver.mjs ' +
        `(${artifact.driverSha256.slice(0, 12)} vs ${driverSha256.slice(0, 12)}). ` +
        'A validation is a statement about one driver; editing the driver retires it.',
    );
  }
  if (app !== null && artifact.app !== app) {
    return refuse(
      `the artifact validated app ${JSON.stringify(artifact.app ?? null)}, and this run is ` +
        `${JSON.stringify(app)}. §3.6's gate is per app/stack pair.`,
    );
  }
  if (variant !== null && artifact.variant != null && artifact.variant !== variant) {
    return refuse(
      `the artifact validated variant ${JSON.stringify(artifact.variant)}, and this run is ` +
        `${JSON.stringify(variant)}.`,
    );
  }

  const missing = VALIDATED_STACKS.filter((s) => !artifact.stacks?.[s]);
  if (missing.length > 0) {
    return refuse(`the artifact has no figures for ${missing.join(', ')}; §3.6 says "on both stacks".`);
  }

  const failures = [];
  for (const stack of VALIDATED_STACKS) {
    const s = artifact.stacks[stack];
    const verdict = withinTolerance(
      s.browserPerSessionBytes,
      s.syntheticPerSessionBytes,
      artifact.tolerance ?? DRIVER_TOLERANCE,
    );
    if (!verdict.within) {
      failures.push(
        `${stack}: ${s.browserPerSessionBytes} B/session with 10 real tabs vs ` +
          `${s.syntheticPerSessionBytes} B/session with 10 synthetic ` +
          `(${verdict.relative === null ? verdict.reason : `${(verdict.relative * 100).toFixed(1)} % apart`})`,
      );
    }
  }
  if (failures.length > 0) {
    return refuse(
      'the driver misrepresents a browser and MUST be fixed before the 1k run — ' +
        failures.join('; '),
    );
  }

  return {
    pass: true,
    note:
      `validated on ${artifact.at ?? 'an unrecorded date'} for app ${artifact.app}: ` +
      VALIDATED_STACKS.map(
        (s) =>
          `${s} ${artifact.stacks[s].browserPerSessionBytes} vs ` +
          `${artifact.stacks[s].syntheticPerSessionBytes} B/session`,
      ).join(', '),
  };
}

/**
 * The gate set in force.
 *
 * `context` names the app and variant of the run asking, because G-DRIVER is
 * per app/stack pair and a validation of the counter says nothing about the
 * dashboard.
 */
export function readGates(context = {}) {
  let gates = { ...DEFAULTS };
  if (existsSync(GATE_FILE)) {
    try {
      gates = { ...DEFAULTS, ...JSON.parse(readFileSync(GATE_FILE, 'utf8')) };
    } catch {
      gates = { ...DEFAULTS };
    }
  }
  /* AFTER the merge, deliberately: this gate's evidence is four measured
     numbers, so it is derived from them and is not a boolean gates.json can
     assert. */
  gates.driverValidation = evaluateDriverValidation(readDriverValidation(), context);
  return gates;
}

/**
 * The check every measurement entrypoint calls first.
 *
 * It throws. That is deliberate: a warning would be read once and then not
 * read, and the failure mode this guards against — a number quoted from a run
 * whose gates never passed — is exactly the failure mode §12 says this whole
 * document exists to prevent.
 */
export function requireGates(
  needed = ['operator', 'driverValidation', 'conformance', 'phase3'],
  context = {},
) {
  const gates = readGates(context);
  const failed = needed.filter((name) => !gates[name]?.pass);
  if (failed.length === 0) return gates;

  const lines = failed.map((name) => `  ${name}: ${gates[name]?.note ?? 'not recorded'}`);
  throw new Error(
    `bench: refusing to record a measured cell. ${failed.length} gate(s) not passed:\n` +
      `${lines.join('\n')}\n\n` +
      `Gates are recorded in ${GATE_FILE.replace(BENCH_ROOT, 'bench')} once each check has ` +
      'actually been performed, except driverValidation, which is derived from ' +
      `${DRIVER_VALIDATION_FILE.replace(BENCH_ROOT, 'bench')} and cannot be asserted by hand. ` +
      'Construction and single-tab smoke do not need them; ' +
      'see bench/README.md, "Measurements are operator-initiated".',
  );
}

/**
 * §5.7's operator gate, in the only form that means anything: the process was
 * started with the flag, in this invocation, by somebody who typed it.
 */
export function operatorInvoked(argv = process.argv) {
  return argv.includes('--operator-approved');
}
