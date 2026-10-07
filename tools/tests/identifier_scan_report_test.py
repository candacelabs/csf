#!/usr/bin/env python3
"""Tests for the identifier scan report.

The synthetic gate output below has to contain text the gate matches, because
the report's job is to read such output and keep that text out of its records.
The matched text is joined from its parts here, so this file is itself clean.
"""

import json
import sys
import tempfile
from pathlib import Path

sys.path.insert(0, str(Path(__file__).parent.parent))

import check_operator_identifiers as gate
import identifier_scan_report as report

HOME_KIND = "Linux home-directory path naming an account"
REFERENCE_KIND = "non-candacelabs issue or pull-request reference"


def matched_home() -> str:
    return "/".join(("", "home", "someaccount", ""))


def matched_reference() -> str:
    return f"some-owner/some-repo#{42}"


def gate_output(*findings: tuple[str, int, str, str]) -> str:
    lines = ["Operator identifiers found in the public tree:", ""]
    for path, line, kind, excerpt in findings:
        lines += [f"{path}:{line}: {kind}", f"    {excerpt}"]
    files = len({finding[0] for finding in findings})
    lines += ["", f"{len(findings)} occurrence(s) in {files} file(s).", "Fix the content: ..."]
    return "\n".join(lines) + "\n"


def before_scan() -> report.Scan:
    return report.parse_gate_output(
        gate_output(
            ("pkg/a/one.go", 3, HOME_KIND, f"mkdir {matched_home()}x"),
            ("pkg/a/one.go", 9, HOME_KIND, f"cd {matched_home()}y"),
            ("services/b/two.go", 5, REFERENCE_KIND, f"see {matched_reference()}"),
        )
    )


PASSED = "Operator-identifier gate passed: 10 tracked file(s) under . checked against 6 patterns.\n"


def test_parse_reads_paths_kinds_and_working_excerpts():
    scan = before_scan()
    assert [(f.path, f.line, f.kind) for f in scan.findings] == [
        ("pkg/a/one.go", 3, HOME_KIND),
        ("pkg/a/one.go", 9, HOME_KIND),
        ("services/b/two.go", 5, REFERENCE_KIND),
    ]
    assert len(scan.excerpts) == 3


def test_parse_reads_a_pass_as_no_findings():
    assert report.parse_gate_output(PASSED).findings == ()


def test_parse_refuses_output_whose_summary_disagrees():
    truncated = gate_output(("pkg/a/one.go", 3, HOME_KIND, "x")).replace("1 occurrence(s) in 1", "2 occurrence(s) in 1")
    try:
        report.parse_gate_output(truncated)
    except report.ReportError:
        return
    raise AssertionError("an incomplete gate output must be refused")


def test_parse_refuses_output_that_is_not_a_gate_run():
    try:
        report.parse_gate_output("operator-identifier gate could not run: no tracked files\n")
    except report.ReportError:
        return
    raise AssertionError("a failed gate run must not read as a result")


def test_record_counts_by_kind_and_file_and_names_a_fix_for_each():
    base = gate.PUBLIC_IDENTIFIER_PATTERNS
    record = report.build_record("9.9.9", before_scan(), report.Scan((), ()), base)
    assert record["before"] == {"total": 3, "files": 2, "by_kind": {HOME_KIND: 2, REFERENCE_KIND: 1}}
    assert record["after"] == {"total": 0, "files": 0, "by_kind": {}}
    assert [(entry["path"], entry["count"], entry["kind"]) for entry in record["by_file"]] == [
        ("pkg/a/one.go", 2, HOME_KIND),
        ("services/b/two.go", 1, REFERENCE_KIND),
    ]
    assert all(entry["fix"] for entry in record["by_file"])
    assert record["pattern_list_changed"] is False


def test_record_notices_a_changed_pattern_list():
    narrowed = gate.PUBLIC_IDENTIFIER_PATTERNS[:-1]
    record = report.build_record("9.9.9", before_scan(), report.Scan((), ()), narrowed)
    assert record["pattern_list_changed"] is True


def test_the_record_and_the_section_carry_no_matched_text():
    scan = before_scan()
    record = report.build_record("9.9.9", scan, report.Scan((), ()), gate.PUBLIC_IDENTIFIER_PATTERNS)
    text = report.render_json(record) + report.render_section(record, "identifier-scan-v9.9.9.json")
    matches = report.matched_text(scan)
    assert matched_home() in matches and matched_reference() in matches
    assert not any(match in text for match in matches)
    assert gate.scan_bytes(text.encode(), "record", gate.compiled_patterns()) == []


def test_a_record_that_would_publish_a_match_is_refused():
    matches = report.matched_text(before_scan())
    for leaked in (f"fixed {matched_home()}", f"fixed {matched_reference()}"):
        try:
            report.refuse_if_it_publishes_a_match("record", leaked, matches)
        except report.ReportError:
            continue
        raise AssertionError("a record carrying matched text must be refused")
    report.refuse_if_it_publishes_a_match("record", "fixed a path", matches)


def test_splice_creates_replaces_and_keeps_other_sections():
    record = report.build_record("9.9.9", before_scan(), report.Scan((), ()), gate.PUBLIC_IDENTIFIER_PATTERNS)
    section = report.render_section(record, "identifier-scan-v9.9.9.json")
    created = report.splice_section(None, "9.9.9", section)
    assert report.section_of(created) == section
    edited = created.replace("## Identifier scan", "## Identifier scan (stale)")
    other = "# Notes\n\nIntro.\n\n" + edited + "\n## Another section\n\nBody.\n"
    spliced = report.splice_section(other, "9.9.9", section)
    assert report.section_of(spliced) == section
    assert "Intro." in spliced and "## Another section" in spliced and "(stale)" not in spliced
    assert report.splice_section(spliced, "9.9.9", section) == spliced


def test_check_accepts_matching_records_and_rejects_a_stale_section():
    record = report.build_record("9.9.9", before_scan(), report.Scan((), ()), gate.PUBLIC_IDENTIFIER_PATTERNS)
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory)
        json_path, notes_path = report.paths(root, "9.9.9")
        json_path.parent.mkdir(parents=True)
        json_path.write_text(report.render_json(record), encoding="utf-8")
        notes_path.write_text(
            report.splice_section(None, "9.9.9", report.render_section(record, json_path.name)), encoding="utf-8"
        )
        assert report.check(root, "9.9.9") == 0
        notes_path.write_text(notes_path.read_text(encoding="utf-8").replace("**3 occurrences", "**4 occurrences"), encoding="utf-8")
        assert report.check(root, "9.9.9") == 1


def test_rendered_json_has_the_documented_shape():
    record = report.build_record("9.9.9", before_scan(), report.Scan((), ()), gate.PUBLIC_IDENTIFIER_PATTERNS)
    loaded = json.loads(report.render_json(record))
    assert list(loaded) == ["version", "before", "after", "by_file", "pattern_list_changed"]
    assert set(loaded["by_file"][0]) == {"path", "count", "kind", "fix"}


if __name__ == "__main__":
    for name, function in sorted(globals().items()):
        if name.startswith("test_") and callable(function):
            function()
            print(f"✓ {name} passed")
    print("All tests passed!")
