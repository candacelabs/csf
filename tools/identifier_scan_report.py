#!/usr/bin/env python3
"""Record what the operator-identifier gate found in a release, and what was done.

The gate (``tools/check_operator_identifiers.py``) fails a tree that carries an
operator identifier. When a release candidate fails it, the fix is to the
content, never to the pattern list. This script turns the gate's own output,
taken before and after the fix, into two public records:

* ``docs/release/identifier-scan-v<version>.json``: counts by kind, counts and
  the fix applied per file, and whether the pattern list changed; and
* an "Identifier scan" section of ``docs/release/notes-v<version>.md``,
  rendered from that JSON and nothing else.

**The records never contain the matched text.** They name files, counts and
fix rules only. Before writing, the script checks its own output two ways: no
substring the gate matched in the "before" output appears in it, and the gate's
own patterns, run over it, find nothing. A record that failed either check
would publish the identifier it reports, so the script refuses to write it.

Usage::

    # 1. the gate's output over the release candidate before any fix
    git worktree add --detach /tmp/before <release-candidate-commit>
    python3 tools/check_operator_identifiers.py --root /tmp/before 2> before.txt

    # 2. the gate's output over the fixed tree (staged files count: it reads
    #    ``git ls-files``)
    python3 tools/check_operator_identifiers.py > after.txt

    # 3. write the records
    python3 tools/identifier_scan_report.py --version 0.3.0 \\
        --before before.txt --after after.txt --base-ref <release-candidate-commit>

    # later, in CI or by hand: the notes section is the JSON's rendering and
    # neither file contains a match
    python3 tools/identifier_scan_report.py --version 0.3.0 --check

``before.txt`` holds the matched lines, so it is a working file: keep it out of
the repository.
"""

from __future__ import annotations

import argparse
import json
import re
import subprocess
import sys
from pathlib import Path
from typing import Mapping, NamedTuple, Sequence

TOOLS = Path(__file__).resolve().parent
sys.path.insert(0, str(TOOLS))

import check_operator_identifiers as gate  # noqa: E402

# The fix applied to every finding of a kind, as a sentence that names the rule
# and never the text it replaced.
KIND_RULES: Mapping[str, str] = {
  "non-candacelabs issue or pull-request reference": (
    "A fixture or example owner is replaced with the publishing organization's name"
    " (candacelabs). An upstream reference is reworded so it names the project and"
    " the number in words, with a link to the project or to a commit, never to a numbered"
    " issue or pull request."
  ),
  "Linux home-directory path naming an account": (
    "A path under a person's or machine's home directory becomes a neutral path"
    " (/srv, /var/lib), a path relative to the HOME variable where the code computes it, or,"
    " in a test of home-directory scrubbing, a path composed from its segments at run time."
  ),
}

# More specific fixes, by path prefix, applied before the kind's rule. The first
# prefix that matches a finding's path wins.
PATH_FIXES: tuple[tuple[str, str, str], ...] = (
  (".github/", "Linux home-directory path naming an account",
   "The shared Bazel task cache moved from beneath the Bazel home directory to a sibling"
   " directory; every writer and reader of the cache path changed together."),
  ("bazel/session_image/", "Linux home-directory path naming an account",
   "The session's home is composed from the run directory and the harness's home"
   " directory name instead of written out."),
  ("pkg/gotth/docs/", "Linux home-directory path naming an account",
   "Recorded evidence: the host directory holding the worktree was replaced with a"
   " neutral path, and the edit is stated beside the record."),
  ("pkg/gotth/docs/", "non-candacelabs issue or pull-request reference",
   "An upstream issue is cited by project and number in words, with a link to the project."),
  ("services/ouroboros/miners/", "Linux home-directory path naming an account",
   "Recorded fixtures: the recording host's home directory became the HOME variable, and the edit is"
   " stated in the labels file; the extracted shapes are unchanged."),
  ("xetcas/", "Linux home-directory path naming an account",
   "The demo workbench account's home moved to a state directory beside the server's own,"
   " in the image, the compose file and the demo transcript together."),
  ("csf/compiler/third_party/", "non-candacelabs issue or pull-request reference",
   "The upstream pull request is cited by project and number in words, with links to the"
   " project and the pinned commit."),
  ("csf/githubtools/", "non-candacelabs issue or pull-request reference",
   "The example repository's owner in the recorded GitHub API fixtures became candacelabs,"
   " in every fixture and in the one test that reads it."),
  ("tools/tests/", "Linux home-directory path naming an account",
   "The scanner test's positive fixtures are composed from their parts by helpers; every"
   " assertion is unchanged."),
  ("tools/tests/", "non-candacelabs issue or pull-request reference",
   "The scanner test's positive fixtures are composed from their parts by helpers, with"
   " neutral owners; every assertion is unchanged."),
  ("tools/", "Linux home-directory path naming an account",
   "A code comment that described the pattern with an example path was reworded; the"
   " pattern list is untouched."),
  ("services/opsview/", "Linux home-directory path naming an account",
   "A test of home-directory scrubbing composes its sample home path from segments; a"
   " documentation comment uses a neutral container path."),
  ("services/harness/", "Linux home-directory path naming an account",
   "The sample path of a memory-file write moved to a neutral directory; the gate under test"
   " does not read the directory."),
)

DRAFT_BANNER = (
  "> **Agent draft, unapproved.** Written by an agent from recorded evidence; the operator has"
  " not reviewed or approved it. Each fix lane adds its own section."
)

BEGIN_MARKER = "<!-- identifier-scan:begin -->"
END_MARKER = "<!-- identifier-scan:end -->"

HEADER = re.compile(r"^(?P<path>\S.*?):(?P<line>\d+): (?P<kind>\S.*)$")
SUMMARY = re.compile(r"^(?P<total>\d+) occurrence\(s\) in (?P<files>\d+) file\(s\)\.$")


class Finding(NamedTuple):
  path: str
  line: int
  kind: str


class Scan(NamedTuple):
  """One gate run: its findings, and the lines it matched on (working data)."""

  findings: tuple[Finding, ...]
  excerpts: tuple[str, ...]


class ReportError(RuntimeError):
  """The record could not be produced safely."""


def parse_gate_output(text: str) -> Scan:
  """Read the gate's output; refuse anything that is not a complete gate run."""
  lines = text.splitlines()
  if any(line.startswith("Operator-identifier gate passed") for line in lines):
    return Scan((), ())
  findings: list[Finding] = []
  excerpts: list[str] = []
  summary: re.Match[str] | None = None
  for line in lines:
    if line.startswith("    "):
      excerpts.append(line.strip())
      continue
    found = SUMMARY.match(line)
    if found:
      summary = found
      continue
    header = HEADER.match(line)
    if header:
      findings.append(Finding(header["path"], int(header["line"]), header["kind"]))
  if summary is None:
    raise ReportError("the gate output has neither a pass line nor a summary line")
  files = len({finding.path for finding in findings})
  if (len(findings), files) != (int(summary["total"]), int(summary["files"])):
    raise ReportError(
      f"the gate output reports {summary['total']} occurrence(s) in {summary['files']} file(s)"
      f" but lists {len(findings)} in {files}; the output is incomplete"
    )
  return Scan(tuple(findings), tuple(excerpts))


def summarize(scan: Scan) -> dict:
  by_kind: dict[str, int] = {}
  for finding in scan.findings:
    by_kind[finding.kind] = by_kind.get(finding.kind, 0) + 1
  return {
    "total": len(scan.findings),
    "files": len({finding.path for finding in scan.findings}),
    "by_kind": dict(sorted(by_kind.items())),
  }


def fix_for(path: str, kind: str) -> str:
  for prefix, fix_kind, fix in PATH_FIXES:
    if kind == fix_kind and path.startswith(prefix):
      return fix
  if kind in KIND_RULES:
    return KIND_RULES[kind]
  raise ReportError(f"no fix rule is recorded for the kind {kind!r}; add one to KIND_RULES")


def by_file(scan: Scan) -> list[dict]:
  counts: dict[tuple[str, str], int] = {}
  for finding in scan.findings:
    key = (finding.path, finding.kind)
    counts[key] = counts.get(key, 0) + 1
  return [
    {"path": path, "count": count, "kind": kind, "fix": fix_for(path, kind)}
    for (path, kind), count in sorted(counts.items())
  ]


def patterns_at(ref: str, root: Path) -> tuple[tuple[str, str], ...]:
  """The gate's pattern list as committed at ``ref``."""
  command = ["git", "-C", str(root), "show", f"{ref}:tools/check_operator_identifiers.py"]
  try:
    source = subprocess.run(command, capture_output=True, check=True).stdout.decode("utf-8")
  except subprocess.CalledProcessError as error:
    raise ReportError(f"cannot read the gate at {ref}: {error.stderr.decode().strip()}") from error
  namespace: dict = {"__name__": "gate_at_base"}
  exec(compile(source, f"{ref}:tools/check_operator_identifiers.py", "exec"), namespace)
  return tuple(namespace["PUBLIC_IDENTIFIER_PATTERNS"])


def build_record(version: str, before: Scan, after: Scan, base_patterns: Sequence[tuple[str, str]]) -> dict:
  return {
    "version": version,
    "before": summarize(before),
    "after": summarize(after),
    "by_file": by_file(before),
    "pattern_list_changed": tuple(base_patterns) != tuple(gate.PUBLIC_IDENTIFIER_PATTERNS),
  }


def matched_text(scan: Scan) -> set[str]:
  """Every substring the gate's patterns match in the lines it reported."""
  found: set[str] = set()
  for excerpt in scan.excerpts:
    raw = excerpt.encode("utf-8")
    for _, pattern in gate.compiled_patterns():
      for match in pattern.finditer(raw):
        found.add(match.group(0).decode("utf-8", errors="replace"))
  return found


def refuse_if_it_publishes_a_match(artifact: str, text: str, matches: set[str]) -> None:
  """Fail when ``text`` carries a matched substring or is itself a finding."""
  for match in sorted(matches):
    if match in text:
      raise ReportError(f"{artifact} would publish text the gate matched; refusing to write it")
  findings = gate.scan_bytes(text.encode("utf-8"), artifact, gate.compiled_patterns())
  if findings:
    raise ReportError(f"{artifact} would itself fail the gate at line {findings[0].line_number}")


def render_json(record: dict) -> str:
  return json.dumps(record, indent=2, ensure_ascii=False) + "\n"


def render_section(record: dict, json_name: str) -> str:
  before, after = record["before"], record["after"]
  rows = []
  for kind in sorted(set(before["by_kind"]) | set(after["by_kind"])):
    rule = KIND_RULES.get(kind, "")
    rows.append(f"| {kind} | {before['by_kind'].get(kind, 0)} | {after['by_kind'].get(kind, 0)} | {rule} |")
  changed = "changed" if record["pattern_list_changed"] else "unchanged"
  return "\n".join([
    BEGIN_MARKER,
    "## Identifier scan",
    "",
    f"The operator-identifier gate (`tools/check_operator_identifiers.py`) found **{before['total']}"
    f" occurrences in {before['files']} files** in the v{record['version']} release candidate."
    f" This release has **{after['total']}**: before {before['total']} → after {after['total']}.",
    f"The gate's pattern list is **{changed}**: the findings were fixed in the content, and no pattern"
    " was widened, narrowed, deleted or excluded.",
    "",
    "| Kind | Before | After | Fix rule |",
    "| --- | ---: | ---: | --- |",
    *rows,
    "",
    f"[{json_name}]({json_name}) lists every file with its count and the fix applied to it. It names"
    " files, counts and fix rules only; it never contains the text that matched. Evidence that was"
    " edited to remove an identifier says so beside the edit.",
    END_MARKER,
  ]) + "\n"


def splice_section(notes: str | None, version: str, section: str) -> str:
  """Put ``section`` in ``notes`` between the markers, creating the draft if absent."""
  if notes is None:
    return f"# Release notes, v{version} (draft)\n\n{DRAFT_BANNER}\n\n{section}"
  if BEGIN_MARKER in notes and END_MARKER in notes:
    start = notes.index(BEGIN_MARKER)
    end = notes.index(END_MARKER) + len(END_MARKER)
    tail = notes[end:]
    return notes[:start] + section.rstrip("\n") + (tail if tail.startswith("\n") else "\n" + tail)
  return notes.rstrip("\n") + "\n\n" + section


def section_of(notes: str) -> str | None:
  if BEGIN_MARKER not in notes or END_MARKER not in notes:
    return None
  start = notes.index(BEGIN_MARKER)
  end = notes.index(END_MARKER) + len(END_MARKER)
  return notes[start:end] + "\n"


def paths(root: Path, version: str) -> tuple[Path, Path]:
  directory = root / "docs" / "release"
  return directory / f"identifier-scan-v{version}.json", directory / f"notes-v{version}.md"


def check(root: Path, version: str) -> int:
  """Verify the committed records agree and publish no match."""
  json_path, notes_path = paths(root, version)
  try:
    record = json.loads(json_path.read_text(encoding="utf-8"))
    notes = notes_path.read_text(encoding="utf-8")
  except OSError as error:
    print(f"identifier scan records are missing: {error}", file=sys.stderr)
    return 1
  problems: list[str] = []
  if section_of(notes) != render_section(record, json_path.name):
    problems.append(f"{notes_path.name} does not carry the section rendered from {json_path.name}")
  if record.get("after", {}).get("total") != 0:
    problems.append("the record's after total is not zero")
  for artifact, text in ((json_path.name, json_path.read_text(encoding="utf-8")), (notes_path.name, notes)):
    findings = gate.scan_bytes(text.encode("utf-8"), artifact, gate.compiled_patterns())
    if findings:
      problems.append(f"{artifact} fails the gate at line {findings[0].line_number}")
  for problem in problems:
    print(problem, file=sys.stderr)
  if not problems:
    print(f"identifier scan records for v{version} are consistent and carry no match.")
  return 1 if problems else 0


def main(argv: Sequence[str] | None = None) -> int:
  parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
  parser.add_argument("--version", required=True, help="release version without the leading v, such as 0.3.0")
  parser.add_argument("--root", type=Path, default=TOOLS.parent, help="repository root (default: this checkout)")
  parser.add_argument("--before", type=Path, help="the gate's output over the release candidate before the fix")
  parser.add_argument("--after", type=Path, help="the gate's output over the fixed tree")
  parser.add_argument("--base-ref", help="the release-candidate commit whose pattern list the fix must not change")
  parser.add_argument("--check", action="store_true", help="verify the committed records instead of writing them")
  arguments = parser.parse_args(argv)
  root = arguments.root.resolve()

  if arguments.check:
    return check(root, arguments.version)
  if not (arguments.before and arguments.after and arguments.base_ref):
    parser.error("--before, --after and --base-ref are required unless --check is given")

  try:
    before = parse_gate_output(arguments.before.read_text(encoding="utf-8"))
    after = parse_gate_output(arguments.after.read_text(encoding="utf-8"))
    if after.findings:
      raise ReportError("the after output still lists findings; fix them before recording the release")
    record = build_record(arguments.version, before, after, patterns_at(arguments.base_ref, root))
    json_path, notes_path = paths(root, arguments.version)
    json_text = render_json(record)
    existing = notes_path.read_text(encoding="utf-8") if notes_path.exists() else None
    notes_text = splice_section(existing, arguments.version, render_section(record, json_path.name))
    matches = matched_text(before)
    refuse_if_it_publishes_a_match(json_path.name, json_text, matches)
    refuse_if_it_publishes_a_match(notes_path.name, notes_text, matches)
  except (ReportError, OSError) as error:
    print(f"identifier scan report: {error}", file=sys.stderr)
    return 1

  json_path.parent.mkdir(parents=True, exist_ok=True)
  json_path.write_text(json_text, encoding="utf-8")
  notes_path.write_text(notes_text, encoding="utf-8")
  print(
    f"wrote {json_path.relative_to(root)} and {notes_path.relative_to(root)}: "
    f"before {record['before']['total']} in {record['before']['files']} files, "
    f"after {record['after']['total']}, pattern list changed: {record['pattern_list_changed']}"
  )
  return 0


if __name__ == "__main__":
  raise SystemExit(main())
