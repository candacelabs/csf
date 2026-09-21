import DSH7 from './DSH-7.mjs';

/*
 * DSH-8 — as DSH-7 under 4x CPU throttle. REPORTED, NOT GATED — it is a
 * degradation shape, not a pass/fail (§2.4).
 *
 * Emulation.setCPUThrottlingRate is applied by the harness identically to both
 * stacks, so whatever it costs is common-mode. It is the one degradation
 * measurement in the suite: §7 records that behaviour under adversarial or slow
 * clients is otherwise not measured on the Next.js side, because no equivalent
 * to gotth-live's chaos suite exists and building one credibly is out of scope.
 */
export default {
  ...DSH7,
  id: 'DSH-8',
  measured: 'reported',
  cpuThrottlingRate: 4,
};
