#!/usr/bin/env python3
"""Fail when an operator identifier reaches this repository.

This tree is published: every tracked byte of it is public. There is no
exclusion hatch, so the only place a private identifier can be kept out of the
public record is the content itself. This gate is the standing check that it
stayed out, and it runs in two places:

* in the private monorepo this module is exported from, where a thin wrapper
  (``tools/check_operator_identifiers.py`` there) adds the operator's own
  literal machine names and addresses to the patterns below and scans the
  export root before a snapshot is ever produced; and
* here, in the published repository, over the whole tracked tree.

**Why the literals are not in this file.** The private wrapper's extra patterns
name specific machines, accounts, and tailnet addresses. Publishing that list
would publish exactly the facts this gate exists to withhold — a single
greppable file handing over every identifier at once. So the literals stay in
the private repository and this file carries only *classes* of identifier:
patterns that describe the shape of a private address, mailbox, or home path
without naming one. The class patterns are strong enough to catch the literals
anyway (a tailnet address is a tailnet address whether or not the gate has been
told which one), which is what makes the split cost nothing in detection.

It reads nothing but the tracked tree — no network, no Git history, no
dependencies outside the standard library — so it runs identically in CI, in a
pre-push hook, and at publish time.

Usage::

    python3 tools/check_operator_identifiers.py [--root REPO] [PATH ...]

Exit status is ``0`` when the scanned tree is clean, ``1`` when an identifier
was found, and ``2`` when the scan could not be performed at all (which is a
failure too: an unrun gate must never read as a pass).
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from pathlib import Path
from typing import Iterable, NamedTuple, Sequence


# The tracked subtrees this gate is responsible for. In the published
# repository the export root *is* the repository, so the whole tree is scanned;
# the private wrapper narrows this to the export root it owns.
DEFAULT_SCAN_PATHS: tuple[str, ...] = (".",)

# APPEND-ONLY BY POLICY.
#
# A pattern in this list may be added, and its description may be reworded, but
# a pattern is never narrowed, widened to a weaker form, or deleted. When the
# gate fires, the finding is fixed in the content — genericize the identifier
# to a documentation-range address (203.0.113.0/24, 198.51.100.0/24), a neutral
# node name (node-a, node-b), an example.invalid mailbox, or a neutral path.
# Loosening a pattern to make a file pass is the one repair that is not allowed:
# it retires the evidence that the identifier was ever there.
#
# Every pattern names a *class* of private identifier rather than any specific
# one, and is anchored so that this project's own vocabulary — the product
# names, the tailnet CIDR that is part of the shipped origin-validation
# contract, the 100.64.0.0/24 fixture addresses, the documentation ranges, and
# the container home directories — cannot match.
PUBLIC_IDENTIFIER_PATTERNS: tuple[tuple[str, str], ...] = (
  # A literal address inside the Tailscale/CGNAT range 100.64.0.0/10 is a real
  # node on somebody's tailnet. Two things inside that range are deliberately
  # not matched: the range itself (``100.64.0.0/10`` appears in the shipped
  # origin-validation contract) and 100.64.0.0/24, which is the synthetic
  # address block every fixture and example here draws from.
  (
    "tailnet address outside the fixture block",
    r"\b100\.(?:64\.(?!0\.)\d{1,3}|6[5-9]\.\d{1,3}|[7-9]\d\.\d{1,3}"
    r"|1[01]\d\.\d{1,3}|12[0-7]\.\d{1,3})\.\d{1,3}\b",
  ),
  # A MagicDNS name identifies a machine and the tailnet it belongs to just as
  # precisely as its address does.
  ("Tailscale MagicDNS hostname", r"(?i)\b[a-z0-9][a-z0-9-]*\.ts\.net\b"),
  # Personal mailboxes. Consumer mail providers only: the fixtures use
  # example.com / example.invalid / example.test mailboxes deliberately, and
  # role addresses at documentation domains are fine.
  (
    "personal mailbox",
    r"(?i)\b[a-z0-9._%+-]+@(?:gmail|googlemail|yahoo|ymail|hotmail|outlook"
    r"|live|msn|icloud|aol|protonmail|proton|gmx|zoho|fastmail)\.[a-z]{2,}\b",
  ),
  # A macOS home directory names the account that owns the machine. The Linux
  # equivalent is deliberately absent: /home/dev and /home/xet here are
  # container accounts this project creates in its own images, and a pattern
  # that fired on them would be a pattern people learn to route around.
  ("macOS home-directory path naming an account", r"(?i)/Users/[a-z0-9._-]+/"),
)


class ScanError(RuntimeError):
  """The scan could not be performed, so its result means nothing."""


class Finding(NamedTuple):
  """One occurrence of one pattern on one line."""

  path: str
  line_number: int
  description: str
  line: str

  def render(self) -> str:
    excerpt = self.line.strip()
    if len(excerpt) > 160:
      excerpt = excerpt[:157] + "..."
    return f"{self.path}:{self.line_number}: {self.description}\n    {excerpt}"


def compiled_patterns(
    patterns: Sequence[tuple[str, str]] = PUBLIC_IDENTIFIER_PATTERNS,
) -> tuple[tuple[str, "re.Pattern[bytes]"], ...]:
  """Compile a policy list once, against bytes.

  Scanning bytes rather than text means a file with an unexpected encoding, a
  Git LFS pointer, or embedded binary content is still scanned instead of
  silently skipped.
  """
  return tuple(
    (description, re.compile(pattern.encode("utf-8")))
    for description, pattern in patterns
  )


def tracked_files(root: Path, scan_paths: Sequence[str]) -> tuple[str, ...]:
  """Return every tracked path under ``scan_paths``, repository-relative."""
  command = ["git", "-C", str(root), "ls-files", "-z", "--", *scan_paths]
  try:
    completed = subprocess.run(command, capture_output=True, check=True)
  except FileNotFoundError as error:  # pragma: no cover - environment failure
    raise ScanError("git is required to enumerate the tracked tree") from error
  except subprocess.CalledProcessError as error:
    detail = error.stderr.decode("utf-8", errors="replace").strip()
    raise ScanError(f"git ls-files failed: {detail}") from error
  return tuple(entry for entry in completed.stdout.decode("utf-8").split("\0") if entry)


def scan_bytes(
    data: bytes,
    path: str,
    patterns: Iterable[tuple[str, "re.Pattern[bytes]"]],
) -> list[Finding]:
  """Return every finding in ``data``, one per matching pattern per line."""
  findings: list[Finding] = []
  for index, raw_line in enumerate(data.split(b"\n"), start=1):
    for description, pattern in patterns:
      if pattern.search(raw_line):
        findings.append(
          Finding(
            path=path,
            line_number=index,
            description=description,
            line=raw_line.decode("utf-8", errors="replace"),
          )
        )
  return findings


def scan_tree(
    root: Path,
    scan_paths: Sequence[str],
    patterns: Iterable[tuple[str, "re.Pattern[bytes]"]] | None = None,
) -> tuple[list[Finding], int]:
  """Scan every tracked file under ``scan_paths``; return findings and a count."""
  compiled = tuple(patterns) if patterns is not None else compiled_patterns()
  paths = tracked_files(root, scan_paths)
  if not paths:
    raise ScanError(
      "no tracked files found under " + ", ".join(scan_paths) + "; refusing to report a pass"
    )

  findings: list[Finding] = []
  scanned = 0
  for path in paths:
    absolute = root / path
    if absolute.is_symlink() or not absolute.is_file():
      # Submodule gitlinks and symlinks carry no publishable bytes of their
      # own. Export roots are required to be free of nested gitlinks anyway.
      continue
    findings.extend(scan_bytes(absolute.read_bytes(), path, compiled))
    scanned += 1
  return findings, scanned


def default_root() -> Path:
  return Path(__file__).resolve().parents[1]


def main(
    argv: Sequence[str] | None = None,
    *,
    patterns: Sequence[tuple[str, str]] = PUBLIC_IDENTIFIER_PATTERNS,
    root: Path | None = None,
    scan_paths: Sequence[str] = DEFAULT_SCAN_PATHS,
) -> int:
  parser = argparse.ArgumentParser(description=__doc__.splitlines()[0])
  parser.add_argument(
    "--root",
    type=Path,
    default=root if root is not None else default_root(),
    help="repository root to scan (default: the checkout containing this script)",
  )
  parser.add_argument(
    "paths",
    nargs="*",
    default=None,
    help="repository-relative subtrees to scan (default: the export roots)",
  )
  arguments = parser.parse_args(argv)
  requested = tuple(arguments.paths) if arguments.paths else tuple(scan_paths)

  try:
    findings, scanned = scan_tree(
      arguments.root.resolve(), requested, compiled_patterns(patterns)
    )
  except ScanError as error:
    print(f"operator-identifier gate could not run: {error}", file=sys.stderr)
    return 2

  if findings:
    print("Operator identifiers found in the public tree:\n", file=sys.stderr)
    for finding in findings:
      print(finding.render(), file=sys.stderr)
    print(
      f"\n{len(findings)} occurrence(s) in {len({f.path for f in findings})} file(s)."
      "\nFix the content: genericize to a documentation-range address"
      " (203.0.113.x / 198.51.100.x), a neutral node name, an example.invalid"
      "\nmailbox, or a neutral path. The pattern list is append-only —"
      " never widen or delete a pattern to make this pass.",
      file=sys.stderr,
    )
    return 1

  print(
    f"Operator-identifier gate passed: {scanned} tracked file(s) under "
    + ", ".join(requested)
    + f" checked against {len(patterns)} patterns."
  )
  return 0


if __name__ == "__main__":
  raise SystemExit(main())
