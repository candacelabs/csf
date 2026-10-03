#!/usr/bin/env python3
"""Run the tier-1 eval cases and emit one ledger row per case.

Tier 1 is the mechanical half of the corpus: each case is a dirty/clean fixture
pair plus an `expected.json` naming the rule IDs that must fire on the dirty
fixture and must not fire on the clean one. This runner judges those fixtures
with `../scripts/check_style.py` itself — it imports the gate's own
`collect_findings` and the shared lexer in `go_style_scan`, and re-implements
nothing. A second implementation would drift, and then the eval would be
testing a scanner nobody runs.

The gate's `main()` is not reused because it scans the tracked repo corpus,
which the fixtures are deliberately outside of (`README.md` § "Why the fixtures
are not .go files"). Everything that decides whether a finding exists is
shared; only the file selection differs.

Assertions per tier-1 case, all three required:

  1. every rule in `dirty_must_fire` fires on the dirty fixtures, at least
     `dirty_min_findings[rule]` times (default 1);
  2. no rule *outside* `dirty_must_fire` fires on them — cases stay isolated to
     one rule, so a failure names one cause instead of a suspect list;
  3. nothing at all fires on the clean fixtures — a clean fixture is the
     exemplar an agent copies. `clean_must_not_fire` exists so the failure
     message names the rule the case was written about; a finding for an
     unlisted rule fails too, rather than passing on an omission.

Tier-2 cases (a `rubric.md` and no `expected.json`) are counted and named on
stderr, never scored here: their rows are appended by whoever judged them. This
runner will not emit a row it did not measure.

Case 000 is checked for a third thing, and it is not a verdict on the case.
`cases/000-originating-prompt/prompt.md` holds the operator message this whole
program came from, and `README.md` § "Case 000" says it is immutable. That was
an instruction with nothing behind it until this runner started recomputing the
sha256 of its fenced block against the digest stored beside it
(`prompt.sha256`). The block only: the prose around it is commentary on an
outcome that is still unfolding and is meant to grow, while the block is the
input the tool is scored against and may not move. A mismatch fails the run.

**It emits no row for that check**, deliberately. A row is `{kind, date,
skill_sha, case_id, tier, pass}` — a verdict on a case — and case 000's verdict
is tier 2's, awaiting a judge. "The file is unedited" is a precondition of that
judgement being about anything, not the judgement, and a runner that emitted a
`pass: true` row for it would be reporting a case it did not score
(`README.md` § "Rows, and the append-only doctrine").

Output: one JSON object per tier-1 case on stdout — `kind`, `date`,
`skill_sha`, `case_id`, `tier`, `pass` — for appending to the metrics ledger.
Failure detail goes to stderr. Exit 1 if any tier-1 case fails, or if the
originating prompt has changed.

Usage:
    python3 run_eval.py >> ../metrics/ledger.jsonl
"""

from __future__ import annotations

import datetime
import hashlib
import json
import subprocess
import sys
from pathlib import Path
from typing import NamedTuple

_EVALS_DIR = Path(__file__).resolve().parent
_SKILL_DIR = _EVALS_DIR.parent
_CASES_DIR = _EVALS_DIR / "cases"
_FIXTURE_SUFFIX = ".go.txt"

_PROVENANCE_CASE = "000-originating-prompt"
_PROMPT_FILE = "prompt.md"
_DIGEST_FILE = "prompt.sha256"
_FENCE = "```"

# What a reader is told when the originating prompt has moved. It names the
# doctrine rather than only the mismatch, because the repair is a `git
# checkout` and the tempting repair is a new digest.
_IMMUTABILITY_DOCTRINE = (
  f"cases/{_PROVENANCE_CASE}/{_PROMPT_FILE} is immutable "
  f"(evals/README.md, 'Case 000'; the file says so itself). It is the exact "
  "operator message that produced this skill, and the case exists because the "
  "prompt that created the tool is an input the tool is scored against — edit "
  "it and the eval measures a prompt nobody sent. A revised or follow-up "
  "prompt becomes a sibling case (000a-…), with a digest of its own. Restore "
  f"the block with `git checkout` rather than re-stamping {_DIGEST_FILE}."
)

sys.path.insert(0, str(_SKILL_DIR / "scripts"))

import check_style  # noqa: E402  (path is set immediately above)
import go_style_scan  # noqa: E402


def _skill_sha() -> str:
    """Last commit touching the skill — the version of the skill being judged.

    Not `HEAD`: a row should name what was measured, and two runs over an
    unchanged skill on different HEADs describe the same skill. Falls back to
    `HEAD` when the skill directory has no commit of its own yet.
    """
    for arguments in (
        ("log", "-1", "--format=%H", "--", str(_SKILL_DIR)),
        ("rev-parse", "HEAD"),
    ):
        try:
            completed = subprocess.run(
                ["git", "-C", str(_SKILL_DIR), *arguments],
                capture_output=True,
                text=True,
                check=True,
            )
        except (OSError, subprocess.CalledProcessError):
            continue
        sha = completed.stdout.strip()
        if sha:
            return sha
    return "unknown"


def _fixture_findings(directory: Path) -> tuple[list[check_style.Finding], int]:
    """Every CS-N finding across one fixture directory, and the files read."""
    findings: list[check_style.Finding] = []
    fixtures = sorted(path for path in directory.glob(f"*{_FIXTURE_SUFFIX}"))
    for path in fixtures:
        source = path.read_text(encoding="utf-8")
        # The path handed to the lexer is the one the fixture *stands for*,
        # with the `.txt` guard removed. Fixtures are held as `*.go.txt` so
        # they cannot pollute the census or manufacture a clone
        # (`README.md` § "Why the fixtures are not .go files"), and that is a
        # fact about where the bytes live rather than about the file they
        # depict. It became load-bearing with CS-9, whose scope is decided by
        # the file name: a rule about how a *test* waits cannot be exercised
        # by a fixture the scanner does not read as a test file.
        relative = str(path.relative_to(_CASES_DIR).with_suffix(""))
        findings.extend(
            check_style.collect_findings(
                [go_style_scan.go_file_from_source(relative, source)]
            )
        )
    return findings, len(fixtures)


def _counts_by_rule(findings: list[check_style.Finding]) -> dict[str, int]:
    counts: dict[str, int] = {}
    for finding in findings:
        counts[finding.rule] = counts.get(finding.rule, 0) + 1
    return counts


class CaseResult(NamedTuple):
    """Why a case failed, and the findings a reader needs to see to believe it."""

    failures: list[str]
    evidence: list[str]


def _check_tier_one(case_dir: Path, expected: dict) -> CaseResult:
    """Judge one tier-1 case. Empty `failures` means it passed."""
    failures: list[str] = []
    must_fire = list(expected.get("dirty_must_fire", []))
    minimums = dict(expected.get("dirty_min_findings", {}))
    must_not_fire = list(expected.get("clean_must_not_fire", []))

    if expected.get("case_id") != case_dir.name:
        failures.append(
            f"expected.json case_id {expected.get('case_id')!r} "
            f"does not match the directory name {case_dir.name!r}"
        )
    if not must_fire:
        failures.append("expected.json names no rule in dirty_must_fire")

    dirty_findings, dirty_count = _fixture_findings(case_dir / "dirty")
    clean_findings, clean_count = _fixture_findings(case_dir / "clean")
    if dirty_count == 0:
        failures.append(f"no dirty/*{_FIXTURE_SUFFIX} fixture found")
    if clean_count == 0:
        failures.append(f"no clean/*{_FIXTURE_SUFFIX} fixture found")

    dirty_counts = _counts_by_rule(dirty_findings)
    for rule in must_fire:
        seen = dirty_counts.get(rule, 0)
        wanted = minimums.get(rule, 1)
        if seen < wanted:
            failures.append(
                f"dirty: expected at least {wanted} {rule} finding(s), saw {seen}"
            )
    for rule, seen in sorted(dirty_counts.items()):
        if rule not in must_fire:
            failures.append(
                f"dirty: {rule} fired {seen} time(s) but the case tests "
                f"{', '.join(must_fire)} only — the fixture is no longer isolated"
            )

    for rule, seen in sorted(_counts_by_rule(clean_findings).items()):
        if rule in must_not_fire:
            failures.append(f"clean: {rule} fired {seen} time(s); it must not fire")
        else:
            failures.append(
                f"clean: {rule} fired {seen} time(s) and expected.json does not "
                "list it — the clean fixture is the exemplar an agent copies, so "
                "it must be clean of every rule, not only the listed ones"
            )

    evidence: list[str] = []
    if failures:
        for label, findings in (("dirty", dirty_findings), ("clean", clean_findings)):
            evidence.extend(f"{label}: {finding.render()}" for finding in findings)
        if not evidence:
            evidence.append("no findings were reported on either fixture")
    return CaseResult(failures=failures, evidence=evidence)


def _fenced_block(text: str) -> tuple[str | None, str | None]:
  """The content between the file's one pair of ``` fences, or why not.

  Exactly one pair. Two blocks would leave this check silently picking one of
  them, which is the same defect `tools/apisurface` was rewritten to stop
  making about a markdown table: a reader that consumes a prefix of a document
  whose shape it never checks reports agreement it did not verify.
  """
  lines = text.split("\n")
  fences = [index for index, line in enumerate(lines) if line.rstrip() == _FENCE]
  if len(fences) != 2:
    return None, (
      f"expected exactly one ``` fenced block, found {len(fences)} fence line(s). "
      "The verbatim prompt is that block, and this check will not guess which "
      "one it is"
    )
  return "\n".join(lines[fences[0] + 1 : fences[1]]) + "\n", None


def _stored_digest(path: Path) -> tuple[str | None, str | None]:
  """The digest recorded beside the case: first non-comment, non-blank line."""
  try:
    raw = path.read_text(encoding="utf-8")
  except OSError as error:
    return None, f"{path.name} is unreadable: {error}"
  for line in raw.splitlines():
    stripped = line.strip()
    if not stripped or stripped.startswith("#"):
      continue
    if len(stripped) != 64 or any(character not in "0123456789abcdef" for character in stripped):
      return None, f"{path.name}: {stripped!r} is not a lower-case sha256 digest"
    return stripped, None
  return None, f"{path.name} records no digest"


def _check_originating_prompt() -> list[str]:
  """Recompute case 000's prompt digest. Empty means it has not moved.

  Not a case verdict and not a ledger row: see this module's docstring. It is
  the precondition under which the tier-2 judgement of case 000 is a judgement
  about the message that was actually sent.
  """
  case_dir = _CASES_DIR / _PROVENANCE_CASE
  prompt_path = case_dir / _PROMPT_FILE
  digest_path = case_dir / _DIGEST_FILE

  if not prompt_path.exists():
    return [f"{prompt_path} is missing entirely", _IMMUTABILITY_DOCTRINE]
  if not digest_path.exists():
    return [
      f"{digest_path} is missing, so nothing pins the prompt. Recreate it with "
      "the digest of the fenced block, not with whatever the block says today",
      _IMMUTABILITY_DOCTRINE,
    ]

  block, error = _fenced_block(prompt_path.read_text(encoding="utf-8"))
  if error is not None:
    return [f"{prompt_path.name}: {error}", _IMMUTABILITY_DOCTRINE]

  expected, error = _stored_digest(digest_path)
  if error is not None:
    return [error, _IMMUTABILITY_DOCTRINE]

  measured = hashlib.sha256(block.encode("utf-8")).hexdigest()
  if measured != expected:
    return [
      f"the verbatim prompt block has changed: {measured} measured, "
      f"{expected} recorded in {_DIGEST_FILE}",
      _IMMUTABILITY_DOCTRINE,
    ]
  return []


def _load_expected(case_dir: Path) -> tuple[dict | None, str | None]:
    path = case_dir / "expected.json"
    if not path.exists():
        return None, None
    try:
        return json.loads(path.read_text(encoding="utf-8")), None
    except (OSError, json.JSONDecodeError) as error:
        return None, f"expected.json is unreadable: {error}"


def main() -> int:
    if not _CASES_DIR.is_dir():
        print(f"run_eval: no cases directory at {_CASES_DIR}", file=sys.stderr)
        return 2

    date = datetime.datetime.now(datetime.timezone.utc).isoformat(timespec="seconds")
    sha = _skill_sha()
    judged = 0
    passed = 0
    unscored: list[str] = []

    # First, and reported even when every case passes, because a run whose
    # provenance case has moved is a run over a corpus that no longer contains
    # what it claims to.
    prompt_failures = _check_originating_prompt()
    if prompt_failures:
        print(f"run_eval: FAIL {_PROVENANCE_CASE} (provenance)", file=sys.stderr)
        for reason in prompt_failures:
            print(f"  {reason}", file=sys.stderr)

    for case_dir in sorted(path for path in _CASES_DIR.iterdir() if path.is_dir()):
        expected, error = _load_expected(case_dir)
        if expected is None and error is None:
            if (case_dir / "rubric.md").exists():
                unscored.append(case_dir.name)
            else:
                print(
                    f"run_eval: {case_dir.name}: neither expected.json nor rubric.md; "
                    "it is not a case",
                    file=sys.stderr,
                )
            continue

        result = (
            CaseResult(failures=[error], evidence=[])
            if error
            else _check_tier_one(case_dir, expected)
        )
        judged += 1
        if result.failures:
            print(f"run_eval: FAIL {case_dir.name}", file=sys.stderr)
            for reason in result.failures:
                print(f"  {reason}", file=sys.stderr)
            for line in result.evidence:
                print(f"    {line}", file=sys.stderr)
        else:
            passed += 1
        print(
            json.dumps(
                {
                    "kind": "eval",
                    "date": date,
                    "skill_sha": sha,
                    "case_id": case_dir.name,
                    "tier": 1,
                    "pass": not result.failures,
                }
            )
        )

    if judged == 0:
        print("run_eval: no tier-1 cases found; refusing a vacuous pass", file=sys.stderr)
        return 2

    print(f"run_eval: tier 1: {passed}/{judged} cases passed", file=sys.stderr)
    if unscored:
        print(
            f"run_eval: tier 2: {len(unscored)} case(s) await a judge "
            f"({', '.join(unscored)}); see README.md for how their rows are recorded",
            file=sys.stderr,
        )
    print(
        "run_eval: the originating prompt is unchanged"
        if not prompt_failures
        else "run_eval: the originating prompt has CHANGED; see above",
        file=sys.stderr,
    )
    return 0 if passed == judged and not prompt_failures else 1


if __name__ == "__main__":
    sys.exit(main())
