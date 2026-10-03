#!/usr/bin/env python3
"""Emit one machine-readable style-census row for the Widget Foundry ledger.

One JSON object on stdout, one line, no prose — a row is appended to the
append-only metrics ledger and never edited afterwards. Rows are how the
program shows the style debt shrinking; a row is never deleted to improve a
trend (`docs/widget_foundry.md`, operator decision 4).

Corpus and parsing come from `go_style_scan.py`, and the finding counts come
from `check_style.py`'s own `collect_findings`, so a census row and a gate run
can never disagree about what they counted or how many findings there were.
That module's header states every exclusion and its reason.

**Schema 12** (2026-10-01, CS-18) adds `test_crossings`, `test_crossing_files`,
`acceptance_suites` and `cs18_findings`.
**Schema 11** (2026-09-04, CS-14) adds `null_twins` and `cs14_findings`.
**Schema 10** (2026-09-04, CS-13) adds `magic_strings` and `cs13_findings`.
**Schema 9** (2026-09-04, CS-12) adds `cs12_findings`.
**Schema 8** (2026-09-03, CS-11). Rows carry an explicit `schema` field since
schema 2; a row without one is schema 1 and has no `cs*_findings` or `mutex_*`
keys. Schema 2 exists because of `docs/widget_foundry.md` amendment 7: a
schema-1 row records structural counts but not the gate's finding counts, so
the number `check_style.py` prints could not be reconstructed from the ledger.
It now is. Structural counts are kept alongside the finding counts rather than
replaced by them — they say what the debt is *made of*, which is what tells a
reader whether a number moved because code changed or because a rule did.
Schema 3 added CS-6's three fields on the same principle, schema 5 added
CS-7's three, schema 6 CS-8's three, schema 7 CS-9's four, and schema 8 adds
CS-11's four and `interface_fields` for CS-8's struct-field amendment, both
landed 2026-09-03. Schema numbers are ledger-wide and never reused, which is why
this census skips 4: that number belongs to the derivability row. The version
history is in `../metrics/README.md`; old rows are never back-filled.

Field meanings — `*_unnamed` fields count **parameters**, not signatures,
because Go forbids mixing named and unnamed parameters inside one list:

    kind                    always "census", so a ledger reader can filter rows
    schema                  census schema version; 3 since 2026-09-02
    date                    ISO-8601 UTC instant the row was measured
    git_sha                 HEAD the measurement describes
    corpus_files            tracked handwritten first-party Go files scanned
    interfaces_total        named interface declarations (exported + unexported)
    interfaces_exported     of those, the ones with an upper-case initial
    i_prefixed              of the exported ones, those already matching ^I[A-Z]
    iface_sigs_with_params  interface method signatures that take >= 1 parameter
    iface_unnamed           unnamed parameters in interface method signatures
    decl_unnamed            unnamed parameters in func declarations
    functype_unnamed        unnamed parameters in func types and func literals
    unnamed_total           the three above, summed
    unnamed_pct             unnamed_total as a percentage of all parameters,
                            rounded to two places
    mutex_fields            sync.Mutex/RWMutex struct fields, embedded included
    mutex_vars              sync.Mutex/RWMutex `var` declarations
    mutex_rwmutex           of those two, how many are RWMutex rather than Mutex
    dispatch_methods        methods whose body is nothing but >= 3 argument-less
                            calls on their own receiver (the CS-6 shape)
    dispatch_calls_total    sibling calls summed over those methods
    dispatch_calls_max      the longest single dispatch list, or 0 for none —
                            "how bad is the worst one", which a count alone
                            cannot say
    erased_boundaries       exported interface-method parameters/results and
                            exported struct fields typed bare `any` in
                            non-internal packages (the CS-7 shape)
    erased_boundary_types   distinct exported types carrying that erasure — a
                            count alone cannot say whether it is one contract
                            threaded through every method or many unrelated
                            sites, and those want opposite responses
    erased_aliases          package-local `type X any` declarations in
                            non-internal packages: erasure hidden behind a name,
                            which is how the founding evidence spelled it
    cs1_findings            CS-1 findings, exactly as check_style.py prints them
    cs2_findings            CS-2 findings, likewise (signatures, not parameters:
                            one finding per unnamed list, so this is smaller
                            than unnamed_total and the two are not comparable)
    cs5_findings            CS-5 findings, likewise. Equal to
                            mutex_fields + mutex_vars by construction — one
                            finding per declaration — so a row where they differ
                            means the gate and the scanner have drifted apart.
    cs6_findings            CS-6 findings, likewise. Equal to dispatch_methods
                            by construction, for the same reason and with the
                            same drift check.
    cs7_findings            CS-7 findings, likewise. Equal to
                            erased_boundaries by construction, with the same
                            drift check.
    interface_return_funcs  receiverless func declarations whose result list
                            hands back a declared interface (the CS-8 shape),
                            counted before the rule's two exemptions
    interface_return_exempt of those, the ones whose result type is not the
                            declaration's to choose: a sealed sum type, or a
                            signature a declared func type already fixes
    interface_return_methods
                            the same shape in method position, which CS-8 does
                            not enforce because a method's result is fixed by
                            the interface it satisfies. Carried so the scope
                            the rule declines stays visible: a corpus where
                            this rises while cs8_findings stays 0 is one where
                            factories moved into method sets.
    cs8_findings            CS-8 findings, likewise. Equal to
                            interface_return_funcs - interface_return_exempt by
                            construction, with the same drift check.
    interface_fields        struct fields whose declared type carries a
                            declared interface — bare, behind a pointer, in a
                            slice, array or map, or in a func-typed field's own
                            signature. CS-8's amendment of 2026-09-03: a struct
                            that is RETURNED and carries an interface is
                            returning that interface through a wrapper.
                            **A locator, never a verdict, and nothing blocks on
                            it**: inward-flowing config structs are the accept
                            side of "only accept interfaces", and they
                            legitimately dominate this count. It is sound at
                            all only because cs1_findings is 0 — every
                            interface here is I-prefixed, so a name is enough
                            to recognise one without a type checker.
    test_files              corpus files whose name ends _test.go — the
                            denominator CS-9 is about, and the one that says
                            whether a moving sleep count is the tree growing
                            or the tests getting worse
    test_sleep_loops        time.Sleep calls standing inside a for body in
                            those files (the CS-9 shape)
    test_sleep_files        distinct test files carrying at least one. The
                            composition matters here more than anywhere else:
                            nine in one chaos suite is a pacing corpus, nine
                            across nine packages is nine authors writing the
                            same await helper again
    cs9_findings            CS-9 findings, likewise. Equal to test_sleep_loops
                            by construction — one finding per sleep — with the
                            same drift check.
    dot_assertion_test_imports
                            ginkgo/v2 and gomega imports in test files that are
                            dot-imported (the CS-11 convention) — the denominator
                            that says how near-universal the convention already
                            was
    nondot_assertion_test_imports
                            ginkgo/v2 and gomega imports in test files that are
                            plain, aliased or blank rather than dot-imported (the
                            CS-11 shape), counted before the rule's one exemption
    assertion_import_exempt of those, the ones a dot import cannot fix: an
                            in-package test whose own package declares a name the
                            library also exports, so a dot import would be a
                            redeclaration compile error. Carried so the scope the
                            rule cannot enforce stays visible — a corpus where
                            this rises is gaining packages that mirror the
                            assertion vocabulary, not getting worse
    cs11_findings           CS-11 findings, likewise. Equal to
                            nondot_assertion_test_imports - assertion_import_exempt
    cs12_findings           CS-12 findings: receiverless functions named exactly
                            New. Report-only; the retrofit backlog CS-12 was
                            minted against (27 files on 2026-09-04)
                            by construction, with the same drift check.
    magic_strings           string literals standing as values inside function
                            bodies — the CS-13 shape, counted structurally the
                            way the other rules' structural fields are
    cs13_findings           CS-13 findings, which equal `magic_strings`: the
                            rule's exemptions are applied by the detector
                            rather than subtracted afterwards, so the two move
                            together and a divergence would be a defect.
                            Report-only; a locator, never a verdict
    null_twins              receiverless functions converting a library optional
                            (sql.Null*, uuid.NullUUID) to or from the plain or
                            pointer value it wraps — the CS-14 shape, counted
                            structurally like the fields above
    cs14_findings           CS-14 findings, which equal `null_twins` for the
                            same reason cs13_findings equals magic_strings: the
                            detector applies the rule's scope itself, so the two
                            move together. Report-only
    test_crossings          real crossings (listen, dial, serve, subprocess,
                            PostgreSQL pool, testcontainers) in test files
                            outside a labelled acceptance suite — the CS-18
                            crossing shape, counted structurally
    test_crossing_files     test files holding at least one of those: the
                            composition field, because nine crossings in one
                            harness and nine in nine suites want different
                            responses
    acceptance_suites       test files whose build constraint names the
                            `acceptance` tag: the labelled opt-in suites CS-18
                            leaves out of scope. A rise here with a fall in
                            test_crossings is suites being labelled, not mocked
    cs18_findings           CS-18-CROSSING findings, which equal
                            `test_crossings` by construction. Report-only. The
                            mockgen and external-package parts of CS-18 are
                            native-only and have no census field

`--root` measures a checkout other than the one this script ships in, the same
flag `check_style.py` and `tools/check_operator_identifiers.py` carry and for
the same reason: a row's fields can only be asserted against a corpus whose
contents are known, and the real corpus moves with every commit.

Usage:
    python3 style_census.py [--root REPO] >> ../metrics/ledger.jsonl
"""

from __future__ import annotations

import argparse
import datetime
import json
import subprocess
import sys

import check_style
import go_style_scan

_CENSUS_SCHEMA = 12


def _census(files: list[go_style_scan.GoFile], sha: str) -> dict[str, object]:
    interfaces_total = 0
    interfaces_exported = 0
    i_prefixed = 0
    iface_sigs_with_params = 0
    unnamed_by_kind = {"iface": 0, "decl": 0, "functype": 0}
    parameters_total = 0
    mutex_fields = 0
    mutex_vars = 0
    mutex_rwmutex = 0
    dispatch_methods = 0
    dispatch_calls_total = 0
    dispatch_calls_max = 0
    erased_boundaries = 0
    erased_holders: set[tuple[str, str]] = set()
    erased_aliases = 0
    interface_return_funcs = 0
    interface_return_exempt = 0
    interface_return_methods = 0
    interface_fields = 0
    magic_strings = 0
    null_twins = 0
    test_crossings = 0
    test_crossing_files = 0
    acceptance_suites = 0
    test_files = 0
    test_sleep_loops = 0
    test_sleep_files = 0
    dot_assertion_test_imports = 0
    nondot_assertion_test_imports = 0
    assertion_import_exempt = 0
    index = go_style_scan.interface_index(files)
    facts_by_package = go_style_scan.package_facts(files)

    for go_file in files:
        for declaration in go_style_scan.interface_declarations(go_file):
            interfaces_total += 1
            if declaration.exported:
                interfaces_exported += 1
                if declaration.i_prefixed:
                    i_prefixed += 1
        for signature in go_style_scan.signatures(go_file):
            parameters_total += signature.params
            if signature.kind == "iface" and signature.params > 0:
                iface_sigs_with_params += 1
            unnamed_by_kind[signature.kind] += signature.unnamed_params
        for declaration in go_style_scan.mutex_declarations(go_file):
            if declaration.kind == "var":
                mutex_vars += 1
            else:
                mutex_fields += 1
            if declaration.type_name == "sync.RWMutex":
                mutex_rwmutex += 1
        for method in go_style_scan.dispatch_methods(go_file):
            dispatch_methods += 1
            dispatch_calls_total += len(method.calls)
            dispatch_calls_max = max(dispatch_calls_max, len(method.calls))
        for boundary in go_style_scan.erased_boundaries(go_file):
            erased_boundaries += 1
            erased_holders.add((go_file.path, boundary.holder))
        if not go_style_scan.in_internal_package(go_file.path):
            erased_aliases += len(go_style_scan.erased_type_names(go_file))
        for returned in go_style_scan.interface_returns(go_file, index):
            if returned.receiver_type is not None:
                interface_return_methods += 1
                continue
            interface_return_funcs += 1
            if returned.exempt is not None:
                interface_return_exempt += 1
        interface_fields += len(go_style_scan.interface_fields(go_file, index))
        magic_strings += len(go_style_scan.magic_strings(go_file))
        null_twins += len(go_style_scan.null_twins(go_file))
        if go_style_scan.is_test_file(go_file.path):
            test_files += 1
            sleeps = len(go_style_scan.sleep_loops(go_file))
            test_sleep_loops += sleeps
            if sleeps:
                test_sleep_files += 1
            if go_style_scan.acceptance_suite(go_file):
                acceptance_suites += 1
            crossings = len(go_style_scan.test_crossings(go_file))
            test_crossings += crossings
            if crossings:
                test_crossing_files += 1
        for assertion in go_style_scan.assertion_imports(go_file, facts_by_package):
            if assertion.is_dot:
                dot_assertion_test_imports += 1
            else:
                nondot_assertion_test_imports += 1
                if assertion.exempt is not None:
                    assertion_import_exempt += 1

    unnamed_total = sum(unnamed_by_kind.values())
    unnamed_pct = (
        round(100.0 * unnamed_total / parameters_total, 2) if parameters_total else 0.0
    )
    findings = check_style.count_by_rule(check_style.collect_findings(files))
    return {
        "kind": "census",
        "schema": _CENSUS_SCHEMA,
        "date": datetime.datetime.now(datetime.timezone.utc).isoformat(
            timespec="seconds"
        ),
        "git_sha": sha,
        "corpus_files": len(files),
        "interfaces_total": interfaces_total,
        "interfaces_exported": interfaces_exported,
        "i_prefixed": i_prefixed,
        "iface_sigs_with_params": iface_sigs_with_params,
        "iface_unnamed": unnamed_by_kind["iface"],
        "decl_unnamed": unnamed_by_kind["decl"],
        "functype_unnamed": unnamed_by_kind["functype"],
        "unnamed_total": unnamed_total,
        "unnamed_pct": unnamed_pct,
        "mutex_fields": mutex_fields,
        "mutex_vars": mutex_vars,
        "mutex_rwmutex": mutex_rwmutex,
        "dispatch_methods": dispatch_methods,
        "dispatch_calls_total": dispatch_calls_total,
        "dispatch_calls_max": dispatch_calls_max,
        "erased_boundaries": erased_boundaries,
        "erased_boundary_types": len(erased_holders),
        "erased_aliases": erased_aliases,
        "cs1_findings": findings["CS-1"],
        "cs2_findings": findings["CS-2"],
        "cs5_findings": findings["CS-5"],
        "cs6_findings": findings["CS-6"],
        "cs7_findings": findings["CS-7"],
        "interface_return_funcs": interface_return_funcs,
        "interface_return_exempt": interface_return_exempt,
        "interface_return_methods": interface_return_methods,
        "cs8_findings": findings["CS-8"],
        "interface_fields": interface_fields,
        "test_files": test_files,
        "test_sleep_loops": test_sleep_loops,
        "test_sleep_files": test_sleep_files,
        "cs9_findings": findings["CS-9"],
        "dot_assertion_test_imports": dot_assertion_test_imports,
        "nondot_assertion_test_imports": nondot_assertion_test_imports,
        "assertion_import_exempt": assertion_import_exempt,
        "cs11_findings": findings["CS-11"],
        "cs12_findings": findings["CS-12"],
        "magic_strings": magic_strings,
        "cs13_findings": findings["CS-13"],
        "null_twins": null_twins,
        "cs14_findings": findings["CS-14"],
        "test_crossings": test_crossings,
        "test_crossing_files": test_crossing_files,
        "acceptance_suites": acceptance_suites,
        "cs18_findings": findings["CS-18-CROSSING"],
    }


def _parse_arguments(argv: list[str]) -> argparse.Namespace:
    """Reject anything this script does not understand, rather than ignoring it.

    Until 2026-09-02 `main()` took no argv at all and whatever it was handed was
    discarded, so `style_census.py --stirct` appended a row and looked like it
    had worked. The P0 audit of the P3 style retrofit recorded that as a defect,
    and it is a sharper one here than in most scripts: the ledger is append-only,
    so a row written by a mistyped invocation cannot be taken back and cannot be
    told apart from an intended one. `argparse` exits 2 on an unknown flag, which
    is a result a caller can check. `derivability_census.py` already did this;
    the two now match.
    """
    parser = argparse.ArgumentParser(
        description=(
            "Emit one style-census row for the Widget Foundry metrics ledger: "
            "the structural counts of the CS-1, CS-2, CS-5, CS-6, CS-7, CS-8, "
            "CS-9 and CS-11 debt and the gate's own finding counts."
        )
    )
    parser.add_argument(
        "--root",
        help=(
            "measure this checkout instead of the one this script ships in "
            "(the census's own tests point it at a synthetic corpus)"
        ),
    )
    return parser.parse_args(argv)


def main(argv: list[str]) -> int:
    arguments = _parse_arguments(argv)
    try:
        root = arguments.root or go_style_scan.repo_root()
        files = go_style_scan.corpus(root)
        sha = go_style_scan.head_sha(root)
    except subprocess.CalledProcessError:
        print(
            f"style_census: {arguments.root or '.'} is not a git checkout with a "
            "commit; refusing a row it cannot stamp",
            file=sys.stderr,
        )
        return 2
    except OSError as error:
        # `git` missing from PATH, or the tree unreadable. Same exit as above:
        # a row this script cannot stamp is a row it must not print.
        print(
            f"style_census: could not run git in {arguments.root or '.'}: {error}",
            file=sys.stderr,
        )
        return 2
    if not files:
        print("style_census: corpus is empty; refusing a vacuous row", file=sys.stderr)
        return 2
    print(json.dumps(_census(files, sha)))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
