---
name: writing-new-tests
description: >-
  Probe-first procedure for writing or extending tests in this monorepo: run
  the code under test once and copy the observed output into the assertions,
  confirm every prerequisite the test needs, run only the new test first, stop
  after two failures of the same test and re-probe, and check the known
  pitfalls list (nil versus empty, ordering, wall-clock dates, duplicate test
  basenames, cancellation semantics, browser secure-context and CSP traps).
  Load it before writing a new test file, adding cases to an existing suite,
  writing a fixture or golden file, or changing an expected value after a test
  went red — in Go (Ginkgo/Gomega), Python (pytest) or browser specs. It does
  not restate the Go test conventions; it links CS-9, CS-11 and
  pkg/eventually.
---

# Writing new tests

Mined session traces show the same loop again and again: an assertion written
from assumed behavior, a red first run, then several edit-and-rerun rounds that
end at the value a single probe would have printed. 49 such episodes over 10
sessions were measured on 2026-10-01
(lab entry (monorepo lab entry `2026-10-01-harness-operating-rules`)).
This skill front-loads the probe.

Go test style is owned elsewhere — read, do not re-derive:

- CS-11 (Ginkgo/Gomega dot-imports) and CS-9 (no hand-rolled waits) in
  [house-code-style](../house-code-style/references/go-rules.md).
- [pkg/eventually](../../../pkg/eventually) is the one typed await
  primitive. `eventually.Await` / `eventually.Consistently`, never a sleep loop.
- In-memory Postgres for tests is `pkg/pgmem`; schema comes from the real
  migration files, never DDL literals in the test.
- HTTP service tests follow the shape of `services/copilot-adapter`'s specs:
  generated clients against the exported API, gomock doubles underneath.

## 1. Probe before you assert

Run the code under test once, the way the test will: a scratch call, the real
CLI, the real gate on the real fixture. Copy what it prints into the expected
values. Never hand-compute expected findings, messages, counts or orderings.

If the probe output looks wrong, that is a finding about the code — report it;
do not encode your belief about what it should have said.

## 2. Pre-flight checklist

Before writing the first assertion:

| Check | How |
|---|---|
| Prerequisites exist or are created in setup | the database, service, directory or file the test reads; a test that depends on host state skips with a reason instead of failing |
| No duplicate test basename | `git ls-files` filtered for the basename; pytest without packages cannot collect two modules with one name |
| Deterministic time | inject a clock; no assertion on today's date or a hard-coded future date that will pass |
| Deterministic order | sort results before comparing; never assert map, goroutine or directory-listing order |
| Nil vs empty on purpose | decide whether the contract returns `nil` or an empty slice/map/list, and assert that one |
| Waits are typed and bounded | eventually or `Eventually` with a named budget; never a sleep |
| Hangs are diagnosable | Go: `-race -timeout <budget>` so a hang prints goroutines |

## 3. Run narrow, then wide

Run only the new test (`go test -run`, `pytest path::name`, a focused spec)
until it is green, then the package, then the affected scope from
[change-impact-sweep](../change-impact-sweep/SKILL.md).

## 4. Iteration cap

If the same test fails twice, stop editing assertions. Re-probe the actual
behavior (step 1) and re-read the prerequisites (step 2). A third blind edit
is guessing.

## 5. Known pitfalls

Generic traps already paid for once. Add a line when you hit a new one.

- Cancelling the context of a blocking websocket read can close the whole
  connection, not just that read; keep reads in one background pump and stop it
  deliberately.
- Browser secure-context APIs (random UUIDs, clipboard, some crypto) are absent
  when a page is served over plain HTTP from a non-loopback address.
- Script evaluated through a devtools protocol bypasses Content-Security-Policy,
  so it cannot prove a CSP blocks anything; load the content the way a page
  would.
- Two test modules with the same basename collide in pytest unless each sits in
  a package.
- Text asserted against generated or documented output drifts when the source
  changes; prefer asserting structure, or regenerate and re-probe.
- A fixture date in the future becomes the past; derive dates from the injected
  clock.
