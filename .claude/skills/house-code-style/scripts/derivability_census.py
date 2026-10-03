#!/usr/bin/env python3
"""Emit one machine-readable derivability row for the Widget Foundry ledger.

One JSON object on stdout, one line, no prose — appended to the append-only
metrics ledger and never edited afterwards, exactly like a `census` row.

**Derivability is a metric, not a rule.** It has no CS number, no gate and no
strict mode; nothing fails because of it. It answers one question:

    derivability = derived / (derived + derivable-but-hand-maintained)

This script computes the mechanical half of that — the **census level**, over
tracked first-party Go — and reads the **backlog level** from
`../metrics/derivable_backlog.json`, which is where the honest denominator
lives. Both halves are documented in `../metrics/README.md`; the third,
**bench** level is documented in `../evals/bench/csp-fanin-v1/README.md` and is
not computed by anything yet.

Why the denominator matters, stated here because this is the file someone
reads before changing the number: **a repo-wide generated-line percentage is
trivially gamed by generating more boilerplate.** Emit a bigger `.pb.go` and
the census level rises without one hand-maintained projection disappearing.
The backlog level is the counterweight — its ratio moves only when a generator
eats a *registered* projection — so a reader who quotes `derived_line_pct`
without the backlog counts beside it is quoting the gameable half alone.

Corpus and the generated marker come from `go_style_scan.py`, the same module
`check_style.py` and `style_census.py` use, so this row and a gate run can
never disagree about which files are derived. The corpus predicate — tracked,
first-party, no `research/`, no `vendor/`, no deliberately self-contained
benchmarks, examples or guide samples — is stated with each exclusion's reason
in that module's header. The one difference is what happens to a generated
file: `corpus()` drops it, and this script counts it, both through the same
`go_style_scan.is_generated()`.

**Schema 4** (2026-09-02). Schema numbers are ledger-wide and never reused;
version history is in `../metrics/README.md`. Fields:

    kind                        always "derivability", so a ledger reader can
                                filter rows
    schema                      ledger schema version; 4 since 2026-09-02
    date                        ISO-8601 UTC instant the row was measured
    git_sha                     HEAD the measurement describes
    generated_go_files          corpus files carrying the generated marker
    handwritten_go_files        corpus files without it — equal to the census
                                row's `corpus_files` at the same commit, by
                                construction, which is the drift check between
                                these two rows
    generated_go_lines          newline-counted lines in those generated files
    handwritten_go_lines        likewise for the handwritten ones
    derived_line_pct            generated_go_lines as a percentage of the two
                                summed, rounded to two places — the census-level
                                derivability score, and the gameable half
    backlog_open                registry entries with no derivation built
    backlog_derivation_built    entries whose derivation exists but whose
                                hand-maintained copy is still what ships
    backlog_derived             entries where the shipped code reads the
                                derivation and the hand-maintained copy is gone

`derived` is deliberately the hardest status to reach: a derivation that exists
and is not adopted has not removed any hand-maintenance, and counting it as
success is how a program congratulates itself for building a generator nobody
runs.

Usage:
    python3 derivability_census.py >> ../metrics/ledger.jsonl
"""

from __future__ import annotations

import argparse
import datetime
import json
import sys
from pathlib import Path

import go_style_scan

_LEDGER_SCHEMA = 4

_BACKLOG_PATH = Path(__file__).resolve().parent.parent / "metrics" / "derivable_backlog.json"

_STATUSES = ("open", "derivation-built", "derived")

_REQUIRED_ENTRY_KEYS = (
    "id",
    "projection",
    "canonical_source",
    "hand_maintained_at",
    "added",
    "status",
    "status_note",
)


class BacklogError(Exception):
    """The registry is missing, unparseable, or does not describe what it claims."""


def read_backlog(path: Path) -> list[dict[str, object]]:
    """Every registry entry, validated enough that a typo cannot silently shrink a count.

    Validation is deliberately shallow — this is a hand-edited registry, not a
    schema exercise — but a misspelled status must not vanish from all three
    counts and leave the row looking smaller and better.
    """
    try:
        with open(path, encoding="utf-8") as handle:
            document = json.load(handle)
    except OSError as error:
        raise BacklogError(f"cannot read {path}: {error}") from error
    except json.JSONDecodeError as error:
        raise BacklogError(f"{path} is not valid JSON: {error}") from error

    entries = document.get("entries")
    if not isinstance(entries, list):
        raise BacklogError(f"{path} has no `entries` list")

    seen_ids: set[str] = set()
    for entry in entries:
        if not isinstance(entry, dict):
            raise BacklogError(f"{path}: an entry is not an object")
        missing = [key for key in _REQUIRED_ENTRY_KEYS if key not in entry]
        if missing:
            raise BacklogError(
                f"{path}: entry {entry.get('id', '<unnamed>')!r} is missing {missing}"
            )
        if entry["status"] not in _STATUSES:
            raise BacklogError(
                f"{path}: entry {entry['id']!r} has status {entry['status']!r}, "
                f"which is not one of {list(_STATUSES)}"
            )
        if entry["id"] in seen_ids:
            raise BacklogError(f"{path}: duplicate entry id {entry['id']!r}")
        seen_ids.add(entry["id"])
    return entries


def count_by_status(entries: list[dict[str, object]]) -> dict[str, int]:
    """How many registry entries sit at each status, with every status present."""
    counts = {status: 0 for status in _STATUSES}
    for entry in entries:
        counts[str(entry["status"])] += 1
    return counts


def _derivability(
    generated_files: int,
    handwritten_files: int,
    generated_lines: int,
    handwritten_lines: int,
    backlog: dict[str, int],
    sha: str,
) -> dict[str, object]:
    total_lines = generated_lines + handwritten_lines
    derived_line_pct = round(100.0 * generated_lines / total_lines, 2) if total_lines else 0.0
    return {
        "kind": "derivability",
        "schema": _LEDGER_SCHEMA,
        "date": datetime.datetime.now(datetime.timezone.utc).isoformat(
            timespec="seconds"
        ),
        "git_sha": sha,
        "generated_go_files": generated_files,
        "handwritten_go_files": handwritten_files,
        "generated_go_lines": generated_lines,
        "handwritten_go_lines": handwritten_lines,
        "derived_line_pct": derived_line_pct,
        "backlog_open": backlog["open"],
        "backlog_derivation_built": backlog["derivation-built"],
        "backlog_derived": backlog["derived"],
    }


def _parse_arguments(argv: list[str]) -> argparse.Namespace:
    """Reject anything this script does not understand, rather than ignoring it.

    This was the first of the three scripts to do it. `style_census.py` took no
    arguments and silently discarded whatever it was given, which the P0 audit
    of the P3 style retrofit recorded as a defect — a mistyped flag there looked
    like it worked, and appended a ledger row. It parses its argv the same way
    since 2026-09-02, so all three scripts now exit 2 on an unknown flag, which
    is the behavior a caller can check.
    """
    parser = argparse.ArgumentParser(
        description=(
            "Emit one derivability row for the Widget Foundry metrics ledger: "
            "generated versus handwritten tracked Go, plus the derivable-backlog "
            "status counts."
        )
    )
    parser.add_argument(
        "--backlog",
        default=str(_BACKLOG_PATH),
        help="path to the derivable-backlog registry (default: the one this skill ships)",
    )
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    arguments = _parse_arguments(argv)
    try:
        entries = read_backlog(Path(arguments.backlog))
    except BacklogError as error:
        print(f"derivability_census: {error}", file=sys.stderr)
        return 2

    root = go_style_scan.repo_root()
    generated_files = 0
    handwritten_files = 0
    generated_lines = 0
    handwritten_lines = 0
    for _, source in go_style_scan.tracked_go_sources(root):
        lines = len(source.splitlines())
        if go_style_scan.is_generated(source):
            generated_files += 1
            generated_lines += lines
        else:
            handwritten_files += 1
            handwritten_lines += lines

    if generated_files + handwritten_files == 0:
        print(
            "derivability_census: corpus is empty; refusing a vacuous row",
            file=sys.stderr,
        )
        return 2

    row = _derivability(
        generated_files=generated_files,
        handwritten_files=handwritten_files,
        generated_lines=generated_lines,
        handwritten_lines=handwritten_lines,
        backlog=count_by_status(entries),
        sha=go_style_scan.head_sha(root),
    )
    print(json.dumps(row))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
