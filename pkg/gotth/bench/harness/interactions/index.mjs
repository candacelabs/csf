/*
 * The interaction registry, and the check that it matches §2's tables.
 *
 * E2 — "Every interaction ID in §2 exists in both and is driven by the
 * identical harness script against identical data-bench-id hooks" — is only
 * true if the set of files in this directory is the set of IDs in the spec. The
 * lists below are transcribed from §2.1, §2.3 and §2.4 and the loader asserts
 * that every one of them loaded and that nothing else did. A missing
 * interaction is then a startup failure rather than a quietly shorter table.
 */
import CTR1 from './CTR-1.mjs';
import CTR2 from './CTR-2.mjs';
import CTR3 from './CTR-3.mjs';
import CTR4 from './CTR-4.mjs';
import CTR5 from './CTR-5.mjs';
import CTR6 from './CTR-6.mjs';
import CTR7 from './CTR-7.mjs';
import CTR8 from './CTR-8.mjs';
import CHT1 from './CHT-1.mjs';
import CHT2 from './CHT-2.mjs';
import CHT2b from './CHT-2b.mjs';
import CHT3 from './CHT-3.mjs';
import CHT4 from './CHT-4.mjs';
import CHT5 from './CHT-5.mjs';
import CHT6 from './CHT-6.mjs';
import CHT7 from './CHT-7.mjs';
import CHT8 from './CHT-8.mjs';
import DSH1 from './DSH-1.mjs';
import DSH2 from './DSH-2.mjs';
import DSH3 from './DSH-3.mjs';
import DSH4 from './DSH-4.mjs';
import DSH5 from './DSH-5.mjs';
import DSH6 from './DSH-6.mjs';
import DSH7 from './DSH-7.mjs';
import DSH8 from './DSH-8.mjs';

/** Exactly the IDs §2.1, §2.3 and §2.4 name. Changing this list is a §12 matter. */
export const SPEC_IDS = [
  'CTR-1', 'CTR-2', 'CTR-3', 'CTR-4', 'CTR-5', 'CTR-6', 'CTR-7', 'CTR-8',
  'CHT-1', 'CHT-2', 'CHT-2b', 'CHT-3', 'CHT-4', 'CHT-5', 'CHT-6', 'CHT-7', 'CHT-8',
  'DSH-1', 'DSH-2', 'DSH-3', 'DSH-4', 'DSH-5', 'DSH-6', 'DSH-7', 'DSH-8',
];

const loaded = [
  CTR1, CTR2, CTR3, CTR4, CTR5, CTR6, CTR7, CTR8,
  CHT1, CHT2, CHT2b, CHT3, CHT4, CHT5, CHT6, CHT7, CHT8,
  DSH1, DSH2, DSH3, DSH4, DSH5, DSH6, DSH7, DSH8,
];

export const INTERACTIONS = new Map(loaded.map((i) => [i.id, i]));

const missing = SPEC_IDS.filter((id) => !INTERACTIONS.has(id));
const extra = [...INTERACTIONS.keys()].filter((id) => !SPEC_IDS.includes(id));
if (missing.length > 0 || extra.length > 0) {
  throw new Error(
    `interaction registry does not match equivalence-spec §2 (E2).\n` +
      `  missing: ${missing.join(', ') || '(none)'}\n` +
      `  extra:   ${extra.join(', ') || '(none)'}`,
  );
}

export function forApp(app) {
  return [...INTERACTIONS.values()].filter((i) => i.app === app);
}

/** The rows §2 marks headline; they are what a reader looks at first. */
export function headlines() {
  return [...INTERACTIONS.values()].filter((i) => i.measured.startsWith('headline'));
}

/** Interactions that produce a latency sample, as opposed to a pass/fail. */
export function timed() {
  return [...INTERACTIONS.values()].filter(
    (i) => i.measured !== 'correctness' && i.measured !== 'jank',
  );
}
