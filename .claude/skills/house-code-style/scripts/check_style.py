#!/usr/bin/env python3
"""Report house-style violations in tracked, handwritten, first-party Go.

Six rules, all defined in `../references/go-rules.md`:

  CS-1  Interfaces carry an `I` prefix (`IStore`), repo-wide, including the
        published `candacelabs/candace` API. Operator ruling of 2026-09-02,
        recorded in `docs/widget_foundry.md`.
  CS-2  Every parameter in every signature is named — func declarations, func
        types, and interface methods alike. `go/CLAUDE.md` line 139 already
        mandated this for interfaces; nothing enforced it.
  CS-5  Bias against mutexes in favor of CSP. Operator ruling of 2026-09-02.
        Every declared `sync.Mutex`/`sync.RWMutex` is reported.
  CS-6  Composable function values over method-set accumulation. Operator
        ruling of 2026-09-02. A method whose body is nothing but three or more
        argument-less calls on its own receiver is reported: that is the
        hardcoded-dispatch-list shape a registry of function values replaces.
  CS-7  Generics at the boundary, erasure in one place. Operator ruling of
        2026-09-02. An exported interface method or exported struct field in a
        non-internal package whose type is bare `any` is reported: that is a
        public contract asking its author to hold an untyped value where a type
        parameter could have carried the type.
  CS-8  Return concrete implementations, only accept interfaces. Operator
        ruling of 2026-09-02. A `func` declaration with no receiver whose
        result list hands back an interface is reported: the caller could have
        held the concrete type, and where a config selects among
        implementations the selection belongs in the consumer.
  CS-9  Tests never hand-roll time. Operator ruling of 2026-09-02. A
        `time.Sleep` standing inside a `for` body in a `_test.go` file is
        reported: that is a wait-until-condition written as a timing loop, in
        a repository that already has a typed await over a polling library.
  CS-12 A constructor names what it builds: a receiverless `func New(` is
        reported. Report-only; operator directive of 2026-09-04, recorded in
        go-rules.md § CS-12 with a 27-file baseline. Detects exactly the name
        `New`; NewStore/MustNew are review questions, not lexer findings.

  CS-13 No magic strings. Operator directive of 2026-09-04, verbatim in
        go-rules.md § CS-13. A string literal standing as a VALUE inside a
        function body — compared against, assigned, passed, returned, used as
        a map key — is reported unless it is a `const` initializer, a
        separator, or human-facing text. Report-only: a locator, not a
        verdict, and it never blocks.

  CS-14 No hand-rolled twin of a library primitive. Operator directive of
        2026-09-04, verbatim in go-rules.md § CS-14. A receiverless function
        taking one value and returning one value, where one side is a
        `sql.Null*`/`uuid.NullUUID` and the other is the plain or pointer shape
        it wraps, is reported: `guregu/null` already ships that conversion and
        an sqlc `go_type` override emits the type directly, so the helper is a
        second spelling of something the tree already has. Report-only.

  CS-18-CROSSING Tests make no real crossings. Operator directive of
        2026-10-01, verbatim in go-rules.md § CS-18. A test file that listens,
        dials, serves, spawns a process, opens a PostgreSQL pool or imports
        testcontainers is reported unless its build constraint names the
        `acceptance` tag. Report-only; the native `CS-18-CROSSING` locator in
        tools/house_lint/test_layout.ml owns the CI report, and the CS-18
        mockgen and external-package parts have no legacy lane.

  CS-11 Test files dot-import ginkgo/v2 and gomega. Operator directive of
        2026-09-03. A `_test.go` file that imports `github.com/onsi/ginkgo/v2`
        or `github.com/onsi/gomega` plain, aliased or blank rather than with a
        dot is reported, so specs read Describe/It/Expect/Eventually unqualified
        as this tree's suites already do. The one exemption a lexer can see is
        structural: an in-package test whose own package declares an identifier
        the library also exports cannot dot-import it without a redeclaration
        compile error, so that import is forced to stay qualified. The blocking
        enforcement is `pkg/scripts/check-test-style.sh`, not this
        script; CS-11 is carried here so the eval corpus and the census measure
        the same rule the shell gate blocks on.

**Report-only by default: it prints findings and exits 0.** `--strict` exits 1
on any finding. Since slice P3 of the Widget Foundry program landed the
repo-wide rename and the named-parameter pass, `.github/workflows/house-lint.yml`
runs `--strict --rule CS-1 --rule CS-2 --rule CS-8` as a blocking step, so
those three rules now fail a pull request. CS-5, CS-6, CS-7 and CS-9 stay out
of that command permanently, for the reasons the next four paragraphs give.

`--rule` is repeatable and repeats accumulate. That is load-bearing rather than
a convenience: with a single-valued flag, `--rule CS-1 --rule CS-2` would parse
as `--rule CS-2` and the blocking job would silently stop checking CS-1 while
printing `0 CS-1 findings` — a gate green by never looking.

CS-5 is report-only for a second and stronger reason, and it points the other
way from CS-1 and CS-2. Those two under-report by design (`go_style_scan.py`
header): what they detect is mechanically a violation, so a false positive
would be a defect. CS-5 deliberately **over**-reports — it flags every mutex
declaration, including the leaf critical sections CS-5 explicitly permits —
because no lexer can tell a counter's lock from a state machine's lock. A
CS-5 line is a locator, not a verdict: it says "a human or an agent should
look at this one", and the honest answer is often "this mutex is correct".
That is why CS-5 findings would never be sound as a blocking gate, and why the
judgment half of the rule is tested by eval case `021-csp-judgment` rather than
by this script.

CS-6 sits in the same family and for the same reason: three ordered steps that
will never grow are honestly a method chain, and no lexer can tell "will never
grow" from "already a family". Its detector is deliberately narrow rather than
deliberately broad — a body with one argument, one `defer` or one `if` in it is
not reported at all — so it under-reports where CS-5 over-reports, but the
finding it does produce is still a locator and not a verdict, and CS-6 likewise
never flips to `--strict`. Its judgment half is eval case
`023-composability-judgment`, whose correct answer keeps a method chain.

CS-7 is the third of that family. Its detector is narrow like CS-6's — three
restrictions carry the rule's own scope, and each of them is the reason a
legitimate `any` goes unreported: only *exported* members of *exported* types,
only in *non-internal* packages (an `internal/` package cannot hold a public
contract, and erasing inside a library is what CS-7 asks for), and only *bare*
`any`, so `[]any`, `map[string]any` and a variadic `...any` are all left alone.
It resolves a package-local `type X any` alias, without which the rule's own
founding evidence — `type State any` threaded through seven interface methods —
would not be detected at all.

Measured at 9 findings over 674 corpus files, of which 6 are the founding
defect in one file and 3 are legitimate erasure that should stay. So CS-7 is a
locator like CS-5 and CS-6, it never flips to `--strict`, and its judgment half
is eval case `025-erasure-judgment`, whose correct answer keeps an `any`.

CS-8 breaks that run of three, and the reason is worth stating because the
three before it all failed the same test. CS-5, CS-6 and CS-7 each detect a
*question* a lexer cannot answer — is this mutex a leaf, is this list a
sequence, is this `any` an envelope — so each stays report-only forever. CS-8
detects a **violation**: a function with no receiver whose result is a declared
interface is one, and the five restrictions in `go_style_scan.py`'s header are
not softeners but the boundary of the shape, each removing a class where the
result type is not that declaration's to choose (a method satisfying an
interface, a sealed sum type, a hook implementation, a container, a
type-parameterised shell). What is left is decidable, which is why CS-8 is in
the blocking command rather than among the report-only three. It shipped
report-only with the corpus reading 9, the 9 were fixed, and `--rule CS-8` was
appended only in the commit after the count read 0 — a gate is never turned
red.

Corpus, and why each exclusion exists — the predicate is copied from
`tools/check-go-reuse.sh` (see `go_style_scan.py`, which owns the one
implementation both scripts share):

  * tracked files only (`git ls-files -- '*.go'`); scratch and build output are
    not house code.
  * `research/*` — frozen research artifacts, not our style surface.
  * `vendor/*`, `*/vendor/*` — third-party source.
  * `pkg/gotth/bench/*` — benchmarks are deliberately self-contained;
    shared helpers would distort what they measure.
  * `examples/gotth/*` — examples must compile as a single copied file.
  * `pkg/gotth/docs/guide/_samples/*` — guide samples are read in
    isolation on the page.
  * any file with a `// Code generated ... DO NOT EDIT.` line — generated files
    are projections, not owners (`AGENTS.md`); the finding belongs to
    the generator, and a hand edit is erased by the next regeneration.

`--root` scans a checkout other than the one this script ships in. It exists
for testability, and it is spelled the way `tools/check_operator_identifiers.py`
spells the same flag: the plumbing around the findings — `--strict`'s exit code,
the rule filter, the empty-corpus refusal — can only be asserted against a
corpus whose findings are known, and the real corpus is not that. It carries
hundreds of findings and they move with every commit, so a test that pinned them
would fail on unrelated work and be deleted. The monorepo's `tools/tests/test_house_style_gate.py`
points this flag at synthetic single-file corpora instead. The path is handed
straight to `git -C`, so a subdirectory scans that subtree with paths relative
to it, and a directory that is not a git checkout exits 2 rather than raising.
So does a host with no `git` on PATH: under `--strict`, exit 1 already means
"findings found", so every way this script can fail to look must exit 2.

CS-9 is the fourth, and its report-only posture is the strongest of the four
because a *majority* of what it finds is correct code. A sleep in a loop is a
wait when a test is asking "has it happened yet" and the subject of the test
when it is generating load, pacing a sender, throttling a link, or sampling a
window — and the two are byte-identical. Measured on this corpus after the
conversion: every remaining finding is a pacing loop or a best-effort quiesce,
which is 100% of them, and each is answered with a comment naming the
distinction rather than silenced. CS-9 is therefore a locator in the strongest
sense — a list of sleeps somebody should read — and it never flips to
`--strict`. Its judgment half is eval case `029-pacing-judgment`, whose
correct answer keeps the sleep.

`--fields` is a listing rather than a rule, and it exits 0 whatever it prints.
It reports every struct field whose declared type carries a repository-declared
interface — CS-8's amendment of 2026-09-03, *"returning a struct that carries
interface-typed fields is returning those interfaces"*. It is deliberately not
one of `--rule`'s values and can never reach the blocking command, because the
accept side of "only accept interfaces" legitimately dominates it: an
inward-flowing config struct holding an `IStore` is the pattern CS-8 asks for,
not a violation of it. The census carries the count as `interface_fields`, and
`bash tools/check-ifacereturn.sh` is the lane that can tell an accepted field from a
returned one.

The lane is sound only because CS-1 reads 0. Every interface in this corpus
carries the `I` prefix, so a lexer with no type checker can recognise one from
its name — the prefix rule was minted for readability and bought this for free
(operator ruling, 2026-09-03).

Usage:
    python3 check_style.py [--strict] [--fields]
                           [--rule CS-1|CS-2|CS-5|CS-6|CS-7|CS-8|CS-9|CS-11|CS-12|CS-13|CS-14|CS-18-CROSSING]
                           [--quiet] [--root REPO]
"""

from __future__ import annotations

import argparse
import subprocess
import sys
from typing import NamedTuple

import go_style_scan

# Every rule this gate reports, in rule-number order. `--rule` accepts these
# and the summary line counts them, so a new rule is wired in by extending
# this one tuple rather than by editing both places and forgetting the second.
# CS-10 has no line here on purpose — no lexer can see where a service's seam
# is — which is why the sequence steps straight from CS-9 to CS-11.
RULES = (
    "CS-1",
    "CS-2",
    "CS-5",
    "CS-6",
    "CS-7",
    "CS-8",
    "CS-9",
    "CS-11",
    "CS-12",
    "CS-13",
    "CS-14",
    "CS-18-CROSSING",
)

_SIGNATURE_LABELS = {
    "decl": "function declaration",
    "functype": "func type",
    "iface": "interface method",
}


class Finding(NamedTuple):
    rule: str
    path: str
    line: int
    message: str

    def render(self) -> str:
        return f"{self.path}:{self.line}: {self.rule}: {self.message}"


def _prefixed_name(declaration: go_style_scan.InterfaceDeclaration) -> str:
    """The CS-1 name this interface should carry, keeping its current visibility."""
    core = declaration.name.lstrip("_")
    if declaration.exported:
        return "I" + core
    return "i" + core[:1].upper() + core[1:]


def _interface_findings(go_file: go_style_scan.GoFile) -> list[Finding]:
    findings: list[Finding] = []
    for declaration in go_style_scan.interface_declarations(go_file):
        if declaration.i_prefixed:
            continue
        findings.append(
            Finding(
                rule="CS-1",
                path=go_file.path,
                line=go_file.line_of(declaration.offset),
                message=(
                    f"{'exported' if declaration.exported else 'unexported'} "
                    f"interface {declaration.name} lacks the house prefix; "
                    f"rename to {_prefixed_name(declaration)}"
                ),
            )
        )
    return findings


def _signature_findings(go_file: go_style_scan.GoFile) -> list[Finding]:
    findings: list[Finding] = []
    for signature in go_style_scan.signatures(go_file):
        if signature.named or signature.params == 0:
            continue
        label = _SIGNATURE_LABELS[signature.kind]
        subject = f"{label} {signature.name}" if signature.name else label
        plural = "s" if signature.params != 1 else ""
        findings.append(
            Finding(
                rule="CS-2",
                path=go_file.path,
                line=go_file.line_of(signature.offset),
                message=f"{subject} has {signature.params} unnamed parameter{plural}",
            )
        )
    return findings


_CS5_ADVICE = (
    "prefer CSP — a channel and one owning goroutine — unless this is a leaf "
    "critical section with no cross-goroutine protocol, which CS-5 permits"
)


def _mutex_subject(declaration: go_style_scan.MutexDeclaration) -> str:
    """How one mutex declaration is named in its finding line."""
    holder = f" in {declaration.holder}" if declaration.holder else ""
    if declaration.kind == "embedded":
        return f"embedded {declaration.type_name}{holder}"
    if declaration.kind == "field":
        return f"struct field {declaration.name} ({declaration.type_name}){holder}"
    return f"var {declaration.name} ({declaration.type_name})"


def _mutex_findings(go_file: go_style_scan.GoFile) -> list[Finding]:
    return [
        Finding(
            rule="CS-5",
            path=go_file.path,
            line=go_file.line_of(declaration.offset),
            message=f"{_mutex_subject(declaration)}: {_CS5_ADVICE}",
        )
        for declaration in go_style_scan.mutex_declarations(go_file)
    ]


_CS6_ADVICE = (
    "register these as named function values in a slice or map the code "
    "iterates, so the family is data a spec can enumerate — unless this is a "
    "short fixed sequence with real ordering dependencies that will never grow, "
    "which CS-6 permits"
)


def _dispatch_findings(go_file: go_style_scan.GoFile) -> list[Finding]:
    return [
        Finding(
            rule="CS-6",
            path=go_file.path,
            line=go_file.line_of(method.offset),
            message=(
                f"method {method.name} dispatches {len(method.calls)} sibling "
                f"methods and does nothing else "
                f"({', '.join(method.calls)}): {_CS6_ADVICE}"
            ),
        )
        for method in go_style_scan.dispatch_methods(go_file)
    ]


_CS7_ADVICE = (
    "carry the type in a type parameter instead — IThing[S any] — and where "
    "heterogeneity genuinely forces erasure, erase exactly once inside the "
    "library that owns the collection, behind a generic shell, so no "
    "author-facing or generated code ever holds an untyped value; unless this "
    "is a genuinely unknown shape (an encoding/json envelope, "
    "reflection-driven plumbing, a third-party signature being mirrored), "
    "which CS-7 permits when a comment says why"
)


def _erasure_subject(boundary: go_style_scan.ErasedBoundary) -> str:
    """How one erased boundary is named in its finding line."""
    spelled = (
        f"{boundary.type_text} (a package-local alias for any)"
        if boundary.via_alias
        else boundary.type_text
    )
    if boundary.kind == "field":
        return (
            f"exported struct field {boundary.holder}.{boundary.member} "
            f"is typed {spelled}"
        )
    return (
        f"exported interface method {boundary.holder}.{boundary.member} has a "
        f"{boundary.position} typed {spelled}"
    )


def _erasure_findings(go_file: go_style_scan.GoFile) -> list[Finding]:
    return [
        Finding(
            rule="CS-7",
            path=go_file.path,
            line=go_file.line_of(boundary.offset),
            message=f"{_erasure_subject(boundary)}: {_CS7_ADVICE}",
        )
        for boundary in go_style_scan.erased_boundaries(go_file)
    ]


_CS8_ADVICE = (
    "return the concrete implementation and let the consumer declare the "
    "interface variable — where a config selects among implementations, move "
    "the switch into the consumer and assign concrete constructors into a "
    "`var x IThing`, leaving it nil for the arm that wants no implementation; "
    "returning nil through an interface-returning factory is how a typed nil "
    "reaches a caller that checks for one"
)


def _interface_return_subject(returned: go_style_scan.InterfaceReturn) -> str:
    """How one interface-returning function is named in its finding line."""
    where = (
        f"result {returned.position} of {returned.arity}"
        if returned.arity > 1
        else "its result"
    )
    return (
        f"function {returned.name} returns the interface {returned.type_text} "
        f"as {where}"
    )


def _interface_return_findings(
    go_file: go_style_scan.GoFile, index: go_style_scan.InterfaceIndex
) -> list[Finding]:
    return [
        Finding(
            rule="CS-8",
            path=go_file.path,
            line=go_file.line_of(returned.offset),
            message=f"{_interface_return_subject(returned)}: {_CS8_ADVICE}",
        )
        for returned in go_style_scan.interface_returns(go_file, index)
        if returned.reported
    ]


_CS9_ADVICE = (
    "wait with the repository's typed await — pkg/eventually — which "
    "polls a typed value and fails with it, rather than re-deriving a "
    "deadline and a sleep; unless this sleep is the subject of the test "
    "rather than a wait for one (a load generator, a paced sender, a "
    "throttled link, a sampler, an observation window), which CS-9 permits "
    "when a comment names the distinction"
)


def _sleep_findings(go_file: go_style_scan.GoFile) -> list[Finding]:
    return [
        Finding(
            rule="CS-9",
            path=go_file.path,
            line=go_file.line_of(sleep.offset),
            message=(
                f"time.Sleep({sleep.argument}) stands inside the for at line "
                f"{go_file.line_of(sleep.loop_offset)}: {_CS9_ADVICE}"
            ),
        )
        for sleep in go_style_scan.sleep_loops(go_file)
    ]


_CS11_ADVICE = (
    'dot-import it in the test — `. "github.com/onsi/ginkgo/v2"`, '
    '`. "github.com/onsi/gomega"` — so specs read Describe/It/Expect/Eventually '
    "unqualified, the way this module's suites already do; unless an in-package "
    "test's own package declares an identifier the library also exports, in "
    "which case a dot import is a redeclaration and the qualified import is "
    "forced (eventually declares Consistently, redis declares Entry)"
)


def _assertion_import_subject(assertion: go_style_scan.AssertionImport) -> str:
    """How one non-dot assertion import is named in its finding line."""
    how = (
        "blank-imported"
        if assertion.local_name == "_"
        else f"imported as {assertion.local_name}"
    )
    return f"the test file has {assertion.path} {how} rather than dot-imported"


def _assertion_import_findings(
    go_file: go_style_scan.GoFile,
    facts_by_package: dict[tuple[str, str], go_style_scan.PackageFacts],
) -> list[Finding]:
    return [
        Finding(
            rule="CS-11",
            path=go_file.path,
            line=go_file.line_of(assertion.offset),
            message=f"{_assertion_import_subject(assertion)}: {_CS11_ADVICE}",
        )
        for assertion in go_style_scan.assertion_imports(go_file, facts_by_package)
        if assertion.reported
    ]


_CS12_ADVICE = (
    "name the constructor for the concrete type it returns — NewCopilotAdapter, "
    "NewPostgresStore — never bare New; the package qualifier is not part of "
    "the name at a call site. Generated constructors (sqlc, oapi-codegen, .pb.go) "
    "are outside the corpus and keep their generator's names"
)


def _constructor_findings(go_file: go_style_scan.GoFile) -> list[Finding]:
    return [
        Finding(
            rule="CS-12",
            path=go_file.path,
            line=go_file.line_of(constructor.offset),
            message=f"function {constructor.name} is a bare constructor: {_CS12_ADVICE}",
        )
        for constructor in go_style_scan.bare_constructors(go_file)
    ]


_CS13_ADVICE = (
    "declare it once, named, in the package that owns its meaning — a "
    "generated enum first (api.SessionStatusIdle, sqlc's, protoc's) because "
    "it cannot drift from the contract, otherwise a `const` — so the value "
    "the program dispatches on or persists has one spelling; unless this is "
    "human-facing text, a struct tag, an import path or a separator, which "
    "CS-13 permits"
)


def _magic_string_findings(go_file: go_style_scan.GoFile) -> list[Finding]:
    return [
        Finding(
            rule="CS-13",
            path=go_file.path,
            line=go_file.line_of(magic.offset),
            message=(
                f"the literal {_elided(magic.text)} stands in {magic.where} "
                f"as a value: {_CS13_ADVICE}"
            ),
        )
        for magic in go_style_scan.magic_strings(go_file)
    ]


_CS14_ADVICE = (
    "delete it and use the library the tree already has — guregu/null, emitted "
    "straight out of an sqlc `go_type` override (see "
    "go/services/copilot-adapter/store/sqlc.yaml), so a nullable column arrives "
    "as {replacement} and no conversion helper exists to keep in sync"
)


# `builds sql.NullTime FROM time.Time` and `reads sql.NullTime AS *time.Time`:
# the finding line says which way the conversion runs, because the two
# directions have different library answers (a `From` constructor and a `.Ptr()`
# accessor) and the reader is being told which one to write.
_CS14_PREPOSITION = {
    go_style_scan.TWIN_BUILDS: "from",
    go_style_scan.TWIN_READS: "as",
}


def _null_twin_findings(go_file: go_style_scan.GoFile) -> list[Finding]:
    return [
        Finding(
            rule="CS-14",
            path=go_file.path,
            line=go_file.line_of(twin.offset),
            message=(
                f"function {twin.name} {twin.direction} {twin.optional_type} "
                f"{_CS14_PREPOSITION[twin.direction]} {twin.plain_type} by hand: "
                + _CS14_ADVICE.format(replacement=twin.replacement)
            ),
        )
        for twin in go_style_scan.null_twins(go_file)
    ]


def _test_crossing_findings(go_file: go_style_scan.GoFile) -> list[Finding]:
    return [
        Finding(
            rule="CS-18-CROSSING",
            path=go_file.path,
            line=go_file.line_of(crossing.offset),
            message=(
                f"test crosses a real boundary through {crossing.what}: use a gomock "
                "mock of the exported interface, or pgmem as the database substrate; "
                "a real-infrastructure suite carries //go:build acceptance"
            ),
        )
        for crossing in go_style_scan.test_crossings(go_file)
    ]


_ELISION_WIDTH = 48


def _elided(text: str) -> str:
    """A literal short enough to read on one finding line."""
    flattened = text.replace("\n", "\\n")
    if len(flattened) <= _ELISION_WIDTH:
        return flattened
    return flattened[: _ELISION_WIDTH - 3] + "..."


def collect_findings(files: list[go_style_scan.GoFile]) -> list[Finding]:
    """Every CS-1 … CS-14 finding this gate reads, in file order.

    The `files` list is the whole corpus rather than one file at a time because
    two rules need it. CS-8 needs to know whether `warden.IPeerDiscoverer` is an
    interface, is sealed, or is a hook's own result type — all facts about the
    corpus, none of them declared in the file that commits the violation. CS-11
    needs to know what a test's own package declares in its *other* files, so
    the redeclaration that forces a qualified import (redis's `Entry`, in
    `cache.go`) is seen from `cache_test.go`.
    """
    index = go_style_scan.interface_index(files)
    facts_by_package = go_style_scan.package_facts(files)
    findings: list[Finding] = []
    for go_file in files:
        findings.extend(_interface_findings(go_file))
        findings.extend(_signature_findings(go_file))
        findings.extend(_mutex_findings(go_file))
        findings.extend(_dispatch_findings(go_file))
        findings.extend(_erasure_findings(go_file))
        findings.extend(_interface_return_findings(go_file, index))
        findings.extend(_sleep_findings(go_file))
        findings.extend(_assertion_import_findings(go_file, facts_by_package))
        findings.extend(_constructor_findings(go_file))
        findings.extend(_magic_string_findings(go_file))
        findings.extend(_null_twin_findings(go_file))
        findings.extend(_test_crossing_findings(go_file))
    findings.sort(key=lambda finding: (finding.path, finding.line, finding.rule))
    return findings


def count_by_rule(findings: list[Finding]) -> dict[str, int]:
    """Findings per rule, every rule present with a zero.

    `style_census.py` records exactly these numbers, so the census row and the
    gate's own summary line cannot disagree about how many findings there are.
    """
    return {
        rule: sum(1 for finding in findings if finding.rule == rule) for rule in RULES
    }


def _parse_arguments(argv: list[str]) -> argparse.Namespace:
    parser = argparse.ArgumentParser(
        description=(
            "Report CS-1 (I-prefix), CS-2 (named parameters), CS-5 (CSP over "
            "mutexes), CS-6 (composable function values), CS-7 (generics at "
            "the boundary), CS-8 (return concrete implementations) and CS-9 "
            "(tests never hand-roll time) findings."
        )
    )
    parser.add_argument(
        "--strict",
        action="store_true",
        help="exit 1 when any finding is reported (P3 turns this on in CI)",
    )
    parser.add_argument(
        "--rule",
        action="append",
        dest="rules",
        choices=RULES,
        help=(
            "report only this rule; repeatable, and repeats accumulate into a "
            "set rather than overwriting one another"
        ),
    )
    parser.add_argument(
        "--fields",
        action="store_true",
        help=(
            "list every struct field carrying a declared interface (CS-8's "
            "2026-09-03 amendment) and exit 0; a locator, never a verdict, and "
            "deliberately not a --rule value"
        ),
    )
    parser.add_argument(
        "--quiet",
        action="store_true",
        help="print the summary line only, not each finding",
    )
    parser.add_argument(
        "--root",
        help=(
            "scan this checkout instead of the one this script ships in "
            "(the gate's own tests point it at a synthetic corpus)"
        ),
    )
    return parser.parse_args(argv)


def _report_interface_fields(files: list[go_style_scan.GoFile]) -> int:
    """Print the interface-typed struct fields, and exit 0 whatever it printed.

    Always 0, including when it prints hundreds. This is CS-8's amendment
    reporting where an interface is CARRIED rather than where one is returned,
    and the accept side — a config struct holding the dependency a constructor
    was given — is the pattern the rule asks for. The number is a place to look,
    and `bash tools/check-ifacereturn.sh` is what says which side a given struct is on.
    """
    index = go_style_scan.interface_index(files)
    total = 0
    for go_file in files:
        for field in go_style_scan.interface_fields(go_file, index):
            total += 1
            member = field.member or "(embedded)"
            print(
                f"{go_file.path}:{go_file.line_of(field.offset)}: "
                f"{field.holder}.{member} is {field.type_text}, carrying the interface "
                f"{field.interface}"
            )
    print(
        f"check_style: {len(files)} files scanned, {total} interface-typed struct fields; "
        "a locator, not a verdict — an inward-flowing config struct is the accept side of CS-8"
    )
    return 0


def main(argv: list[str]) -> int:
    arguments = _parse_arguments(argv)
    try:
        root = arguments.root or go_style_scan.repo_root()
        files = go_style_scan.corpus(root)
    except subprocess.CalledProcessError:
        print(
            f"check_style: {arguments.root or '.'} is not a git checkout; "
            "nothing was scanned",
            file=sys.stderr,
        )
        return 2
    except OSError as error:
        # `git` missing from PATH, or the tree unreadable. Exit 2 for the same
        # reason the branch above does: under --strict an uncaught exception
        # exits 1, and 1 already means "findings found". A broken runner
        # reporting itself as a style violation is a blocking gate lying.
        print(
            f"check_style: could not run git in {arguments.root or '.'}: {error}",
            file=sys.stderr,
        )
        return 2
    if not files:
        print("check_style: corpus is empty; refusing a vacuous pass", file=sys.stderr)
        return 2

    if arguments.fields:
        return _report_interface_fields(files)

    findings = collect_findings(files)
    selected = arguments.rules
    if selected:
        findings = [finding for finding in findings if finding.rule in selected]

    if not arguments.quiet:
        for finding in findings:
            print(finding.render())

    by_rule = count_by_rule(findings)
    counted = ", ".join(f"{by_rule[rule]} {rule} findings" for rule in RULES)
    print(f"check_style: {len(files)} files scanned, {counted}")
    if selected:
        # A filtered rule counts zero in the line above because it was never
        # looked at, which reads identically to a rule that is clean. The
        # blocking CI job filters to CS-1 and CS-2, so say which rules the
        # verdict covers rather than leaving four zeros to be misread.
        print(
            "check_style: filtered to "
            + ", ".join(rule for rule in RULES if rule in selected)
            + "; the other rules were not evaluated"
        )
    if findings and not arguments.strict:
        print(
            "check_style: report-only by default; CS-1, CS-2 and CS-8 block in "
            "CI (.github/workflows/house-lint.yml) and CS-11 in "
            "check-test-style.sh (candace-go-checks.yml), "
            "CS-5/CS-6/CS-7/CS-9 never do"
        )
    return 1 if findings and arguments.strict else 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
