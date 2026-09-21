[identifiers genericized for publication - measurements unmodified]

# `bench/data/` — deliberately empty

This directory holds raw measurement output: one `run-NNNNN/` per run, each with
`samples.csv` (one row per sample) and `manifest.json` (§6).

**It contains no run ids, and that is a checkable claim rather than a promise.**
The equivalence spec's own amendment log says so in those terms: the reason
amendment A-1 could move a definition safely is that no number existed yet, and
"`bench/data/` contains no run ids" is the form of that claim a reader can
verify. Deleting this file would not change the claim; measuring would.

## What will land here, and the rules it lands under

- **A manifest for every run the harness starts** — including aborted and failed
  ones, with the abort reason (§6). There is no path through `harness/` that
  produces samples without a manifest and none that abandons a manifest
  silently.
- **Contiguous, zero-padded run ids.** `run-00001`, `run-00002`, … A gap in the
  sequence is an audit failure (§6, T-19), which is only detectable if the ids
  are countable by somebody who was not there — a timestamp or a uuid would make
  a missing run invisible.
- **`gates.json`**, written once each gate has actually been checked. Until then
  `harness/gate.mjs` refuses to record a cell and says which clause it is
  refusing under.

## The one file here that is not a run

**`driver-validation.json`** — §3.6's driver-validation gate: per-session memory
with 10 real Chromium tabs and with 10 synthetic sessions, on both stacks,
within 10 %. It is not a run and it consumes no run id, and it is the file
`harness/gate.mjs`'s `G-DRIVER` derives its verdict from rather than reading a
boolean somebody wrote down. `status` is `run` or `not-run` and nothing else, so
a partial result cannot be dressed up as a pass by omitting a field.

**Today it says `not-run`, for one reason.** `harness/validate-driver.mjs` was
invoked on `node-a` and refused, and the artifact records the
host state and every blocker rather than a number. It listed four earlier on
2026-08-05; three of them were work and have been done — the gotth-live SUT
image and the committed `docker/gotth.Dockerfile` that builds it (deviation
D-9), `docker/.env`, and `BENCH_CPUSET_DRIVER`. What remains is **Q-7**:
a GPU streaming container is running, runs are skipped and not degraded while it
is, and waiting is the only permitted mitigation — nothing in this tree stops,
restarts or reconfigures a co-tenant container to make a run cleaner. The refusal
happens **before** a manifest is opened, which is why the "no run ids" claim
above is still true after an attempt, and it is still true after this one.

The verdict did not move: `status` is `not-run` either way and `G-DRIVER` goes
on refusing. What changed is that the artifact is now a work list of one, and
the one item on it is a wait rather than a task.

`G-DRIVER` also treats the artifact as **stale** when it names a different app or
variant, or when `driverSha256` no longer matches `harness/driver.mjs` — a
validation is a statement about one driver, and editing the driver retires it
without anybody having to remember to.

## Why it is still empty

Phase 3's tuning is unfinished. `coalesce_flush_at`, `MinResyncInterval` /
`ResyncBurst` and the provenance-log volume are safety-chosen defaults that have
never been measured (Appendix B, QA3-1/2/3), and two of the three move numbers
this spec publishes. A re-tune landing after Phase 5 measurement has begun
forces full re-collection of the affected cells under §12 — so finishing Phase 3
first is the cheap path, and the gate that enforces it is code rather than a
note.
