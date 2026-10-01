"""Compare the native metrics command with the unchanged archive calculator."""

import argparse
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest import mock


def load_module(name, path):
    specification = importlib.util.spec_from_file_location(name, path)
    module = importlib.util.module_from_spec(specification)
    sys.modules[name] = module
    specification.loader.exec_module(module)
    return module


class Parity(unittest.TestCase):
    """Use the oracle's results, including its errors, rather than fixture answers."""

    def __init__(self, native, oracle):
        super().__init__()
        self.native = native
        self.oracle = oracle
        self.original_calculate = oracle.calculate
        self.original_is_generated = oracle.is_generated
        self.maxDiff = None

    def invoke_native(self, root, manifest=None):
        arguments = [str(self.native), "--root", str(root), "metrics"]
        if manifest is not None:
            arguments.extend(["--manifest", str(manifest)])
        return subprocess.run(arguments, capture_output=True, text=True, timeout=30)

    def invoke_oracle(self, root, manifest=None):
        arguments = [sys.executable, str(self.oracle.__file__), "--root", str(root)]
        if manifest is not None:
            arguments.extend(["--manifest", str(manifest)])
        return subprocess.run(arguments, capture_output=True, text=True, timeout=30)

    def assert_rejected(self, result):
        self.assertNotEqual(result.returncode, 0, result.stdout)
        self.assertEqual(result.stdout, "")
        self.assertTrue(result.stderr.strip(), "rejection must explain the error")

    def assert_aliases(self, actual, expected, location):
        if "handwritten_lines" in expected:
            non_generated = expected["handwritten_lines"]
            total = expected["total_lines"]
            self.assertEqual(actual.get("non_generated_lines"), non_generated, location)
            self.assertIn("non_generated_percent", actual, location)
            self.assertEqual(actual["non_generated_percent"],
                             round(100 * non_generated / total, 2) if total else None,
                             location)
        if "handwritten_files" in expected:
            self.assertEqual(actual.get("non_generated_files"),
                             expected["handwritten_files"], location)

    def project_legacy(self, actual, expected, location="report"):
        if isinstance(expected, dict):
            self.assertIsInstance(actual, dict, location)
            self.assert_aliases(actual, expected, location)
            for key in expected:
                self.assertIn(key, actual, location)
            return {key: self.project_legacy(actual[key], value, f"{location}.{key}")
                    for key, value in expected.items()}
        if isinstance(expected, list):
            self.assertIsInstance(actual, list, location)
            self.assertEqual(len(actual), len(expected), location)
            return [self.project_legacy(value, expected[index], f"{location}[{index}]")
                    for index, value in enumerate(actual)]
        if expected is None:
            self.assertIsNone(actual, location)
        elif isinstance(expected, (str, bool, int)):
            self.assertIs(type(actual), type(expected), location)
        return actual

    def assert_report(self, result, expected):
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stderr, "")
        actual = json.loads(result.stdout)
        self.assertEqual(set(actual["by_language"]), set(expected["by_language"]))
        self.assertEqual(self.project_legacy(actual, expected), expected)
        return actual

    def calculate(self, root, records):
        with tempfile.TemporaryDirectory(prefix="metric-parity-receipt-") as directory:
            manifest = Path(directory) / "receipt.json"
            manifest.write_text(json.dumps({"files": records}), encoding="utf-8")
            result = self.invoke_native(root, manifest)
            try:
                # Direct legacy helper tests also use native metrics. Restore the
                # helper here so the oracle itself stays unchanged and independent.
                with mock.patch.object(self.oracle, "is_generated", self.original_is_generated):
                    expected = self.original_calculate(root, records)
            except ValueError:
                self.assert_rejected(result)
                raise
            self.assert_report(result, expected)
            return expected

    def is_generated(self, source):
        expected = self.original_is_generated(source)
        with tempfile.TemporaryDirectory(prefix="metric-parity-header-") as directory:
            root = Path(directory)
            content = source.encode("utf-8")
            (root / "header.go").write_bytes(content)
            report = self.calculate(root, [{"path": "header.go",
                                           "sha256": hashlib.sha256(content).hexdigest()}])
            self.assertEqual(report["files"][0]["generated"], expected)
        return expected

    def compare_cli(self, root, manifest=None):
        expected = self.invoke_oracle(root, manifest)
        actual = self.invoke_native(root, manifest)
        self.assertIn(expected.returncode, (0, 2),
                      f"oracle CLI failed unexpectedly:\n{expected.stderr}")
        if expected.returncode:
            self.assert_rejected(expected)
            self.assert_rejected(actual)
            return None
        self.assertEqual(expected.stderr, "")
        return self.assert_report(actual, json.loads(expected.stdout))


def additional_tests(parity, legacy):
    class NativeMetricTest(legacy.CodegenTest):
        def compare_raw_receipt(self, content):
            manifest = self.root / "receipt.json"
            manifest.write_text(content, encoding="utf-8")
            return parity.compare_cli(self.root, manifest)

        def test_cli_reads_default_receipt_without_git(self):
            try:
                super().test_cli_reads_default_receipt_without_git()
            except subprocess.CalledProcessError as error:
                self.fail(f"oracle CLI failed unexpectedly:\n{error.stderr}")
            parity.compare_cli(self.root)

        def test_cli_explicit_receipt_outside_root(self):
            records = [self.add("src with spaces/main.py", "value = 1\n"),
                       self.add("docs/説明.md", "# Documentation\n")]
            # A default receipt must not override the explicit receipt.
            (self.root / "SOURCE_RECEIPT.json").write_text("invalid", encoding="utf-8")
            with tempfile.TemporaryDirectory(prefix="metric-parity-manifest-") as directory:
                manifest = Path(directory) / "selected files.json"
                manifest.write_text(json.dumps({"files": records}), encoding="utf-8")
                parity.compare_cli(self.root, manifest)

        def test_cli_rejects_missing_and_invalid_manifest(self):
            parity.compare_cli(self.root)
            for content in (b"", b"{", b"null", b"[]", b"{}", b'{"files":null}',
                            b'{"files":{}}', b'{"files":"all"}', b'{"files":[null]}',
                            b'{"files":[false]}', b'{"files":[[]]}', b"\xff"):
                with self.subTest(content=content):
                    manifest = self.root / "receipt.json"
                    manifest.write_bytes(content)
                    parity.compare_cli(self.root, manifest)

        def test_cli_duplicate_members_use_the_last_value(self):
            record = self.add("main.py", "value = 1\n")
            files = json.dumps([record])
            for alternative in ("[]", "null", '"all"'):
                for first, last in ((files, alternative), (alternative, files)):
                    with self.subTest(first=first, last=last):
                        self.compare_raw_receipt(f'{{"files":{first},"files":{last}}}')

            good_fields = json.dumps(record)[1:-1]
            for key, alternative in (("path", "missing.py"), ("path", "../outside.py"),
                                     ("path", None), ("sha256", "bad"),
                                     ("sha256", None), ("sha256", "0" * 64)):
                other_field = json.dumps({key: alternative})[1:-1]
                for first, last in ((good_fields, other_field), (other_field, good_fields)):
                    with self.subTest(key=key, first=first, last=last):
                        self.compare_raw_receipt('{"files":[{' + first + ',' + last + '}]}')

        def test_cli_comments_are_rejected_only_outside_strings(self):
            for content in ('/* comment */{"files":[]}', '{"files":[]}/* comment */',
                            '// comment\n{"files":[]}', '{"files":[]}// comment',
                            '{"files":/* comment */[]}', '{"files":[/* comment */]}',
                            '{"files":[],// comment\n"note":null}'):
                with self.subTest(content=content):
                    self.compare_raw_receipt(content)
            record = self.add("marker/*generated*.py", "value = 1\n")
            for note in ('/* comment */', '// comment', 'quote " /* still a string */',
                         'escaped backslash \\ and quote " // still a string'):
                with self.subTest(note=note):
                    self.compare_raw_receipt(json.dumps({"files": [record], "note": note}))

        def test_cli_rejects_json_extensions(self):
            for value in ('(1,2)', '(1)', '<"Tag">', '<"Tag":1>', "'single quotes'"):
                with self.subTest(value=value):
                    self.compare_raw_receipt('{"files":[],"note":' + value + '}')
            for content in ('{files:[]}', '{"files":[],note:1}', '{"files":[],}',
                            '{"files":[],"note":[1,]}', '{"files":[]}{}'):
                with self.subTest(content=content):
                    self.compare_raw_receipt(content)

        def test_cli_nonfinite_values_follow_python_schema_validation(self):
            record = self.add("main.py", "value = 1\n")
            for value in ("NaN", "Infinity", "-Infinity", "1e400", "-1e400"):
                with self.subTest(value=value, field="unused"):
                    self.compare_raw_receipt('{"files":' + json.dumps([record])
                                             + ',"note":' + value + '}')
                with self.subTest(value=value, field="files"):
                    self.compare_raw_receipt('{"files":' + value + '}')
                for field in ("path", "sha256"):
                    valid_fields = json.dumps({key: item for key, item in record.items()
                                               if key != field})[1:-1]
                    with self.subTest(value=value, field=field):
                        self.compare_raw_receipt('{"files":[{' + valid_fields + ','
                                                 + json.dumps(field) + ':' + value + '}]}')

        def test_cli_raw_controls_and_escaped_controls(self):
            for character in ("\0", "\t", "\n", "\r", "\x1f"):
                with self.subTest(character=repr(character), escaped=False):
                    self.compare_raw_receipt('{"files":[],"note":"' + character + '"}')
                with self.subTest(character=repr(character), escaped=True):
                    self.compare_raw_receipt(json.dumps({"files": [], "note": character}))

        def test_cli_malformed_numbers_are_rejected(self):
            for value in ("01", "00", "-01", "+1", ".1", "1.", "1e", "1e+", "--1",
                          "0x1", "+Infinity", "-NaN", "nan", "infinity"):
                with self.subTest(value=value):
                    self.compare_raw_receipt('{"files":[],"note":' + value + '}')

        def test_cli_documents_stricter_high_surrogate_limit(self):
            record = self.add("unicode/\U0001f600.py", "value = 1\n")
            self.compare_raw_receipt(json.dumps({"files": [record], "note": "\U0001f600"}))
            manifest = self.root / "receipt.json"
            for escaped in (r"\ud800", r"\udbff", r"\ud800\ud800"):
                with self.subTest(escaped=escaped):
                    manifest.write_text('{"files":[],"note":"' + escaped + '"}', encoding="utf-8")
                    oracle = parity.invoke_oracle(self.root, manifest)
                    self.assertEqual(oracle.returncode, 0, oracle.stderr)
                    parity.assert_rejected(parity.invoke_native(self.root, manifest))

        def test_invalid_record_shapes(self):
            for record in (None, False, 17, "main.py", [], {}, {"path": None},
                           {"path": 12}, {"path": ["main.py"]}):
                with self.subTest(record=record), self.assertRaises(ValueError):
                    parity.calculate(self.root, [record])

        def test_unsafe_paths(self):
            for name in ("", "../outside.py", "/absolute.py", "./main.py", "a//b.py",
                         "a/../b.py", "a/./b.py", "a\\b.py", "main.py/"):
                with self.subTest(name=name), self.assertRaises(ValueError):
                    parity.calculate(self.root, [{"path": name, "sha256": "0" * 64}])

        def test_bad_hashes_are_rejected_even_for_excluded_files(self):
            for filename in ("main.py", "README.md", "data.bin"):
                record = self.add(filename, "content\n")
                invalid = [{"path": record["path"]}]
                invalid.extend({**record, "sha256": value}
                               for value in (None, 0, [], "", "a" * 63, "a" * 65, "g" * 64))
                for value in invalid:
                    with self.subTest(record=value), self.assertRaises(ValueError):
                        parity.calculate(self.root, [value])

        def test_uppercase_hashes_and_extensions(self):
            records = [self.add("MAIN.GO", "package main\n"),
                       self.add("DOC.MARKDOWN", "# Notes\n")]
            parity.calculate(self.root, [{**record, "sha256": record["sha256"].upper()}
                                         for record in records])

        def test_directory_and_symlink_ancestors_are_rejected(self):
            (self.root / "directory.py").mkdir()
            with self.assertRaises(ValueError):
                parity.calculate(self.root, [{"path": "directory.py", "sha256": "0" * 64}])
            record = self.add("real/main.py", "value = 1\n")
            (self.root / "linked").symlink_to(self.root / "real", target_is_directory=True)
            with self.assertRaises(ValueError):
                parity.calculate(self.root, [{**record, "path": "linked/main.py"}])
            with tempfile.TemporaryDirectory(prefix="metric-parity-link-") as directory:
                alias = Path(directory) / "root"
                alias.symlink_to(self.root, target_is_directory=True)
                with self.assertRaises(ValueError):
                    parity.calculate(alias, [record])
                with self.assertRaises(ValueError):
                    parity.calculate(alias / "real", [{**record, "path": "main.py"}])

        def test_selected_and_excluded_utf8_and_source_differences(self):
            for filename in ("main.py", "README.md"):
                for content in (b"\xff", b"\xc0\xaf", b"\xed\xa0\x80", b"\xe2\x82"):
                    with self.subTest(filename=filename, content=content):
                        record = self.add(filename, content)
                        with self.assertRaises(ValueError):
                            parity.calculate(self.root, [record])
            records = [self.add("main.py", "original\n"), self.add("README.md", "# Original\n"),
                       self.add("data.bin", b"\xff")]
            (self.root / "main.py").write_text("changed\n\n", encoding="utf-8")
            (self.root / "README.md").write_text("# Changed\n", encoding="utf-8")
            (self.root / "data.bin").write_bytes(b"\xfe")
            before = {record["path"]: (self.root / record["path"]).read_bytes() for record in records}
            report = parity.calculate(self.root, records)
            self.assertEqual(report["modified_files"], ["main.py", "README.md"])
            self.assertEqual(before, {name: (self.root / name).read_bytes() for name in before})

        def test_omitted_invalid_sources_are_never_read(self):
            records = [self.add("selected.py", "value = 1\n")]
            self.add("omitted.py", b"\xff")
            self.add("omitted.md", "<!-- csf:diagram unclosed -->\n")
            (self.root / "broken.py").symlink_to(self.root / "missing.py")
            parity.calculate(self.root, records)

        def test_empty_selected_files_and_empty_manifest(self):
            parity.calculate(self.root, [])
            parity.calculate(self.root, [self.add("empty.py", ""), self.add("empty.md", "")])

        def test_cli_ocaml_only_keeps_non_generated_aliases_empty(self):
            records = self.add_ocaml_sources()
            report = self.compare_raw_receipt(json.dumps({"files": records}))
            self.assertEqual(report["non_generated_files"], 0)
            for corpus in (report, report["documentation"]):
                self.assertEqual(corpus["non_generated_lines"], 0)
                self.assertIsNone(corpus["non_generated_percent"])

        def test_cli_code_exclusions_preserve_adjacent_code_and_nested_markdown(self):
            adjacent = self.add("candace/pkg/gotth-extra/main.go", "package main\n")
            markdown = self.add("candace/pkg/gotth/docs/README.md",
                                "<!-- Generated by a tool; do not edit. -->\n# Documentation\n")
            records = self.add_ocaml_sources() + self.add_gotth_code() + [adjacent, markdown]
            report = self.compare_raw_receipt(json.dumps({"files": records}))
            self.assertEqual([item["path"] for item in report["files"]], [adjacent["path"]])
            self.assertEqual([item["path"] for item in report["documentation"]["files"]],
                             [markdown["path"]])
            self.assertEqual(report["non_generated_files"], 1)

        def test_all_supported_extensions_are_counted(self):
            records = [self.add(f"source{extension}", "authored\n")
                       for extensions in parity.oracle.LANGUAGES.values()
                       for extension in sorted(extensions)]
            parity.calculate(self.root, records)

        def test_hidden_basenames_are_not_extensions(self):
            extensions = {extension for values in parity.oracle.LANGUAGES.values()
                          for extension in values} | {".md", ".markdown"}
            records = [self.add(f"hidden/{extension}", b"\xff")
                       for extension in sorted(extensions)]
            records.extend([self.add("file.", b"\xff"),
                            self.add(".config.go", "package config\n"),
                            self.add(".config.md", "# Configuration\n")])
            parity.calculate(self.root, records)

        def test_physical_unicode_splitlines(self):
            separators = ("\n", "\r", "\r\n", "\v", "\f", "\x1c", "\x1d", "\x1e",
                          "\x85", "\u2028", "\u2029")
            for separator in separators:
                for trailing in ("", separator):
                    with self.subTest(separator=repr(separator), trailing=bool(trailing)):
                        records = [
                            self.add("plain.py", separator.join(("first", "", "last")) + trailing),
                            self.add("generated.go", separator.join((
                                "// Code generated by a tool. DO NOT EDIT.", "", "package sample")) + trailing),
                            self.add("README.md", separator.join((
                                "# Notes", "<!-- csf:diagram graph -->", "generated", "",
                                "<!-- /csf:diagram graph -->", "authored")) + trailing),
                        ]
                        parity.calculate(self.root, records)

        def test_unicode_whitespace_and_header_boundaries(self):
            for whitespace in ("\t", "\u00a0", "\u1680", "\u2003", "\u202f", "\u3000"):
                with self.subTest(whitespace=repr(whitespace)):
                    parity.calculate(self.root, [
                        self.add("generated.py", whitespace + "\n" + whitespace
                                 + "# Code generated by Candacegen. DO NOT EDIT.\nvalue = 1\n"),
                        self.add("README.md", whitespace + "<!-- Generated by a tool; do not edit. -->"
                                 + whitespace + "\n# Notes\n"),
                    ])
            for suffix in ("Other", "é", "_", "2", "-tool", ".", ""):
                with self.subTest(suffix=suffix):
                    parity.is_generated(f"# Code generated by Candacegen{suffix}\nvalue = 1\n")

        def test_rounding_uses_each_denominator_independently(self):
            for generated, authored in ((1, 2), (1, 31), (9, 23), (17, 239), (3, 61)):
                with self.subTest(generated=generated, authored=authored):
                    records = [
                        self.add("generated.go", "// Code generated by a tool. DO NOT EDIT.\n"
                                 + "generated\n" * (generated - 1)),
                        self.add("authored.go", "authored\n" * authored),
                        self.add("README.md", "<!-- csf:diagram graph -->\n"
                                 + "generated\n" * generated + "<!-- /csf:diagram graph -->\n"
                                 + "authored\n" * authored),
                    ]
                    parity.calculate(self.root, records)

    return NativeMetricTest


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--native", type=Path, required=True)
    parser.add_argument("--oracle", type=Path, required=True)
    parser.add_argument("--legacy-tests", type=Path, required=True)
    arguments = parser.parse_args()
    # Paths are provided by Bazel, not inferred from a particular checkout or cwd.
    oracle_path = arguments.oracle.resolve(strict=True)
    sys.path.insert(0, str(oracle_path.parent))  # codegen imports its archive helper.
    oracle = load_module("codegen", oracle_path)
    legacy = load_module("legacy_codegen_tests", arguments.legacy_tests.resolve(strict=True))
    parity = Parity(arguments.native.resolve(strict=True), oracle)
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(additional_tests(parity, legacy))
    # Bazel sets PYTHONSAFEPATH, so child interpreters do not prepend the
    # script directory. Pass the declared oracle location to the unchanged
    # legacy CLI test as well as our own subprocesses.
    python_path = os.pathsep.join(filter(None, (str(oracle_path.parent),
                                               os.environ.get("PYTHONPATH", ""))))
    with mock.patch.object(oracle, "calculate", parity.calculate), \
            mock.patch.object(oracle, "is_generated", parity.is_generated), \
            mock.patch.dict(os.environ, {"PYTHONPATH": python_path}):
        result = unittest.TextTestRunner(verbosity=2).run(suite)
    return 0 if result.wasSuccessful() else 1


if __name__ == "__main__":
    sys.exit(main())
