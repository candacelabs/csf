#!/usr/bin/env python3
"""Corpus selection and Go signature scanning shared by the house-code-style scripts.

Three consumers import this module: `check_style.py` (the
CS-1/CS-2/CS-5/CS-6/CS-7/CS-8 gate), `style_census.py` (the style-census ledger row) and
`derivability_census.py` (the derivability ledger row). It exists so they can
never disagree about what "the corpus", "an unnamed parameter" or "a generated
file" means; no script re-implements any of it.

The generated marker is the load-bearing case of that. `corpus()` uses it to
*drop* generated files and `derivability_census.py` uses it to *partition*
them, which is two opposite uses of one predicate — so `is_generated()` is a
named function both call rather than a regex each spells out. A second copy of
that regex would be a metric and a gate silently disagreeing about which files
are derived, which is precisely the number the derivability metric reports.

Corpus predicate — deliberately the same one as `tools/check-go-reuse.sh`, so
the style gate and the reuse gate scan exactly the same tracked, handwritten,
first-party Go. Each exclusion and its reason:

  * `git ls-files -- '*.go'`            — tracked files only; untracked scratch
                                          and build output are not house code.
  * drop `research/*`                   — frozen research artifacts; reproducing
                                          a paper's code is not writing ours.
  * drop `vendor/*` and `*/vendor/*`    — third-party source, not first-party.
  * drop `pkg/gotth/bench/*`    — benchmarks are deliberately
                                          self-contained; sharing code across
                                          them would distort what they measure.
  * drop `examples/gotth/*`     — examples are deliberately
                                          self-contained so a reader can copy
                                          one file and have it compile.
  * drop `pkg/gotth/docs/guide/_samples/*`
                                        — same reason: guide samples are read
                                          in isolation, on the page.
  * drop any file with a line matching `^// Code generated .* DO NOT EDIT\\.$`
                                        — generated files are projections, not
                                          owners (`AGENTS.md`); a
                                          finding there is a finding about the
                                          generator, and editing the output is
                                          erased by the next regeneration.

  * CS-14 null-twin detection reads a receiverless declaration with exactly
    one parameter and exactly one result, where one side is a `sql.Null*` or
    `uuid.NullUUID` written with that exact text and the other is the plain or
    pointer value it wraps. Everything else is missed on purpose: a method (the
    receiver puts the choice elsewhere, CS-8's reason), a twin that also takes
    a context or returns an error, a `database/sql` under an import alias, and
    a conversion through a named type of the caller's own. The one-parameter,
    one-result shape is what buys the rule its counterweight — a function
    CARRYING a `sql.NullTime` through to a query is not converting it — and it
    is why the detector reads 5 things in an 809-file corpus and nothing else.
    The integer widths are read as a family (`int` in, `sql.NullInt16` out is
    the founding offender), which is the one place it over-reaches rather than
    under-reports, and it over-reaches onto exactly the shape the rule is about.

Parsing is a lexer, not a Go parser: this module runs on hosts with no Go
toolchain (`go/ast` is not available to us), and it must stay stdlib-only. It
blanks comments and string/rune literals first, then walks balanced brackets.
Two consequences are documented rather than hidden:

  * Function *declarations* are found by `^func` at column 0. That is what
    gofmt produces for every top-level declaration; a hand-indented top-level
    `func` would be missed. Everything gofmt formats is covered.
  * `I`-prefix detection is `^I[A-Z]`, so an interface legitimately named
    `IOThing` reads as already-prefixed. Accepted: the false-negative direction
    is the safe one for a report-only gate.
  * CS-5 mutex detection reads *declarations* — struct fields (embedded
    included) and `var` specs whose type is exactly `sync.Mutex`,
    `sync.RWMutex`, or a pointer to one. A mutex reached through a container
    (`map[string]*sync.Mutex`), aliased behind `sync.Locker`, or created by
    `:=` inside a function body is not counted. Same under-reporting direction
    as the two above, for the same reason.
  * CS-6 dispatch detection recognises exactly one shape: a method whose body
    consists *solely* of `DISPATCH_MINIMUM` or more argument-less calls on its
    own receiver. One statement that is anything else — an argument, a `defer`,
    a `return`, an `if`, a call through a field (`worker.store.Flush()`) —
    disqualifies the whole body. A dispatch list spelled any other way is
    missed, which is again the safe direction: what survives the filter is
    unambiguously the hardcoded-call-list shape.
  * CS-7 erasure detection reads *exported members of exported named types in
    non-internal packages*: an exported interface method whose parameter or
    result type is bare `any`, and an exported struct field of bare `any`.
    Three restrictions carry the rule's own scope rather than being
    conveniences, and each is the reason a legitimate `any` is not reported:

      - **Non-internal only.** CS-7 governs a *public contract*. Go's own
        `internal/` rule already makes those packages unreachable from outside,
        and erasing inside a library is precisely what CS-7 permits, so an
        `internal/` package cannot commit the defect by definition.
      - **Bare only.** `[]any`, `map[string]any` and `*any` are not reported. A
        container of dynamic elements is not a contract erasing one value's own
        type, which is what the rule is about.
      - **Package-local `any` aliases are resolved.** A declaration of the form
        `type State any` (or `= any`, or `interface{}`) makes `State` an erased
        type for the rest of that *file*, so a contract that hides its erasure
        behind a name is still seen. Resolution is per-file rather than
        per-package because every consumer scans one file at a time; an alias
        used across a package boundary is missed, which is the safe direction
        again.

    A variadic `...any` is not reported either: that is the `fmt.Println` shape,
    a genuine unknown-shape boundary rather than a type a parameter could have
    carried.
  * CS-8 interface-return detection reads the *result list of a `func`
    declaration with no receiver*, and reports a result whose type is a bare
    I-prefixed name the corpus declares as an interface. It is the first
    detector here that is not per-file: `interface_index()` collects what the
    whole corpus declares, because the founding site returns
    `warden.IPeerDiscoverer` from a `main` package that declares no interfaces
    at all. Scanning one file alone therefore sees only what that file
    declares, which is what makes an eval fixture self-contained.

    Five restrictions carry the rule's own scope. Each is the reason a result
    that is not this declaration's to choose goes unreported:

      - **No receiver.** A method's result type is fixed by the interface it
        satisfies; changing it unilaterally stops the type satisfying that
        interface, so the decision lives in the interface declaration and not
        in the method. Measured on this corpus: 9 method-position interface
        results, 7 of them implementations of `IClock`/`IPullSource` methods.
        A factory *spelled* as a method is therefore missed — the same
        under-reporting direction as every imprecision above.
      - **Composites are IN scope since 2026-09-03; a pointer and a func type
        are not.** A slice, an array, a variadic and a map's VALUE are peeled
        (`interface_result_name`), because a caller that receives a slice of
        interfaces receives interfaces — reading one as "a container, not this
        function handing back one implementation" is what let this repository's
        own framework contract sit at 0 findings for a month. `*IStore` stays
        out: a pointer to an interface is a different mistake with a different
        fix. A map KEY stays out: the value position is the one a returned
        implementation travels in. A func value's own signature stays out: it
        is a contract rather than a handover. `IWidget[S]` is not reported for
        a sharper reason than any of those — the type argument is the caller's,
        and a shell that erased the concrete type on the way in by design
        (CS-7) has none to return (`pkg/widget/registry.go`'s
        `LookupWidget`).
      - **A declared interface name only.** `^[Ii][A-Z]` alone reads
        `session.ID` — `type ID [16]byte` — as an interface, which is 4 false
        positives on this corpus. Cross-checking the base name against every
        interface the corpus declares removes all four. It also *is* CS-8's
        third-party filter, and a better one than a package-qualifier rule
        would be, because it needs no import resolution: CS-8 reaches interface
        types this repository owns, and a result type the corpus does not
        declare — `templ.Component`, `http.Handler`, a library's `Msg` — is a
        pass-through of somebody else's contract and is never reported. A
        package qualifier is dropped, so a third-party interface whose name
        collided with one of ours would be reported; measured today, `ID` is
        the only I-prefixed non-interface name in the corpus and no interface
        anywhere shares it.
      - **A sealed sum type is not an abstraction.** See
        `sealed_interface_names()`.
      - **A signature a contract already fixes.** See `hook_signatures()`.

    `error` needs no rule: it is Go's own contract, it is the one interface the
    house does not prefix, and `^[Ii][A-Z]` cannot see it.
  * Interface-typed STRUCT FIELD detection (`interface_fields`) is CS-8's
    2026-09-03 amendment and is a locator rather than a rule: it has no CS
    number in the gate, it is reachable only through `check_style.py --fields`,
    and the census publishes its count. It peels one wrapper more than the
    result lane — a pointer and a channel included — and looks inside a
    func-typed field's own signature, which is where a variadic `...IThing`
    lives. It reads unexported types as well as exported ones, because a
    private struct holding an injected dependency is most of what it counts.
    It is sound only because CS-1 reads 0: every interface in this corpus
    carries the `I` prefix, so a name is enough to recognise one without a type
    checker.
  * CS-9 sleep-loop detection reads a `time.Sleep(` call that stands inside a
    `for` statement's body, in a file whose name ends `_test.go`. It is the
    only detector here that looks at a file name, and deliberately: a sleep
    inside a loop in production code is a retry, a backoff or a rate limiter,
    and CS-9 is about how a *test* waits.

    A brace is read as a loop body when the text between the start of its line
    and the brace itself contains the `for` keyword, which is what gofmt
    produces for every `for` statement it formats. Three consequences, all
    under-reporting:

      - **A `for` header split across lines is missed.** gofmt does not
        produce one.
      - **A sleep reached through a helper the loop calls is missed.** The
        detector sees one function at a time and does not follow calls, so
        `for { step() }` with the sleep inside `step` is invisible. That is
        the same limit every heuristic here has and the same safe direction.
      - **A closure defined inside a loop counts as inside it**, whether or
        not it runs there. The alternative is tracking where a function value
        escapes to, which a lexer cannot do, and the founding evidence — a
        goroutine spawned to pace sends inside a loop — is exactly this shape.

    What the detector cannot see at all is the distinction the rule turns on:
    a sleep that *paces* — a load generator, a throttled relay, a sampler, an
    observation window — is the subject of its test rather than a wait for
    one, and looks identical. That is why CS-9 never blocks. See
    `../references/go-rules.md` § CS-9.
  * CS-11 assertion-import detection reads the import specs of a `_test.go`
    file whose path is exactly `github.com/onsi/ginkgo/v2` or
    `github.com/onsi/gomega`, and reports one imported with a name other than
    `.` (a plain, aliased, or blank import). The import *path* is a string
    literal, so this is the one detector that reads `GoFile.source` rather than
    the blanked `code`; everything else — the qualifier uses and the package's
    own declarations — is read from the blanked code, where a `pkg.Name` inside
    a comment cannot masquerade as a real reference.

    The one exemption is a fact about a whole package, not one file, so CS-11
    is a per-corpus detector like CS-8: `package_facts` records, per (directory,
    package), whether a dot import of either library would redeclare one of the
    package's own top-level names. The evidence that the library exports a name
    is the corpus itself writing `qualifier.Name`; the evidence the package
    declares it is a top-level type or receiverless-func of that name. Two
    consequences, both under-reporting the way the rest of this module does:

      - **The library's exports are known only from what the corpus references.**
        A package that declares `Entry` but whose tests never write
        `ginkgo.Entry` would not be recognised as colliding, and the forced
        qualification would read as a violation. No such package exists; the one
        that collides (redis) references it, so the collision is seen.
      - **`var`/`const` declarations are not collected**, only types and
        receiverless funcs. A ginkgo/gomega export colliding with a first-party
        `var` of the same exported name is not a shape that occurs.

    Detection reads a name, and the blocking enforcement is elsewhere:
    `pkg/scripts/check-test-style.sh` is the gate that fails CI, and
    CS-11 is carried here so the eval corpus and the census measure the same
    rule. See `../references/go-rules.md` § CS-11.
"""

from __future__ import annotations

import bisect
import re
import subprocess
from pathlib import Path
from typing import Iterator, NamedTuple

# --- corpus ---------------------------------------------------------------

_EXCLUDED_PREFIXES = (
    "research/",
    "vendor/",
    "pkg/gotth/bench/",
    "examples/gotth/",
    "pkg/gotth/docs/guide/_samples/",
)

_GENERATED_MARKER = re.compile(r"^// Code generated .* DO NOT EDIT\.$", re.MULTILINE)


class GoFile(NamedTuple):
    """One corpus file: its repo-relative path and its comment/string-blanked source.

    `code` is the source with comments and string/rune literals blanked to
    spaces, which is what every bracket walk reads. `source` is the raw bytes,
    kept for the one rule that needs a string literal's content — CS-11 reads
    the import path, which the blanking would have erased.
    """

    path: str
    code: str
    line_starts: tuple[int, ...]
    source: str = ""

    def line_of(self, offset: int) -> int:
        """1-indexed line number containing `offset`."""
        return bisect.bisect_right(self.line_starts, offset)


def repo_root() -> str:
    """Absolute path of the checkout holding this script, whatever the cwd is.

    Resolved from the script's own location rather than the working directory,
    the way the `candace` CLI derives its checkout root, so the gate scans the
    tree it ships with even when invoked from elsewhere.
    """
    return _git("-C", str(Path(__file__).resolve().parent), "rev-parse", "--show-toplevel")


def head_sha(root: str) -> str:
    """Full commit sha of HEAD in `root`."""
    return _git("-C", root, "rev-parse", "HEAD")


def _git(*args: str) -> str:
    completed = subprocess.run(
        ["git", *args], capture_output=True, text=True, check=True
    )
    return completed.stdout.strip()


def _is_excluded_path(relative_path: str) -> bool:
    if relative_path.startswith(_EXCLUDED_PREFIXES):
        return True
    return "/vendor/" in relative_path


def go_file_from_source(relative_path: str, source: str) -> GoFile:
    """One `GoFile` from source text that is already in hand.

    `corpus()` builds every file this way, and so does the eval runner
    (`../evals/run_eval.py`) for its fixtures — a fixture lexed by a second
    implementation would stop testing the gate the repo actually runs.
    """
    code = blank_noncode(source)
    starts = tuple([0] + [index + 1 for index, char in enumerate(code) if char == "\n"])
    return GoFile(path=relative_path, code=code, line_starts=starts, source=source)


def is_generated(source: str) -> bool:
    """Whether `source` carries the conventional `DO NOT EDIT` generated marker.

    The one implementation of that test. `corpus()` calls it to exclude
    generated files — a finding there belongs to the generator, and a hand edit
    is erased by the next regeneration — and `derivability_census.py` calls it
    to count them, because "how much of this tree is derived" is the question
    the same marker answers from the other side.
    """
    return _GENERATED_MARKER.search(source) is not None


def tracked_go_sources(root: str) -> Iterator[tuple[str, str]]:
    """Every tracked first-party Go file as `(relative_path, source)`, in git order.

    Generated files are **included** here: this is the corpus predicate without
    the generated filter, so a caller can either drop them (`corpus()`) or
    count them (the derivability census). Files that cannot be read or decoded
    are skipped, the way `corpus()` has always skipped them.
    """
    listing = subprocess.run(
        ["git", "-C", root, "ls-files", "-z", "--", "*.go"],
        capture_output=True,
        text=True,
        check=True,
    ).stdout
    for relative_path in listing.split("\0"):
        if not relative_path or _is_excluded_path(relative_path):
            continue
        try:
            with open(f"{root}/{relative_path}", encoding="utf-8") as handle:
                source = handle.read()
        except (OSError, UnicodeDecodeError):
            continue
        yield relative_path, source


def corpus(root: str) -> list[GoFile]:
    """Every tracked, handwritten, first-party Go file, comment/string-blanked."""
    return [
        go_file_from_source(relative_path, source)
        for relative_path, source in tracked_go_sources(root)
        if not is_generated(source)
    ]


# --- lexing ---------------------------------------------------------------


def blank_noncode(source: str) -> str:
    """Replace comment and literal bytes with spaces, preserving every newline.

    Offsets and line numbers survive unchanged, so a brace inside a raw string
    or a `//` in a URL can never confuse the bracket walk below.
    """
    out = list(source)
    index = 0
    length = len(source)
    while index < length:
        char = source[index]
        if char == "/" and source.startswith("//", index):
            end = source.find("\n", index)
            end = length if end == -1 else end
            _blank_span(out, index, end)
            index = end
        elif char == "/" and source.startswith("/*", index):
            end = source.find("*/", index + 2)
            end = length if end == -1 else end + 2
            _blank_span(out, index, end)
            index = end
        elif char in "\"`'":
            index = _blank_literal(source, out, index, quote=char)
        else:
            index += 1
    return "".join(out)


def _blank_span(out: list[str], start: int, end: int) -> None:
    for position in range(start, end):
        if out[position] != "\n":
            out[position] = " "


def literal_end(source: str, start: int, quote: str) -> int:
    """Offset one past the literal opening at `start`, clamped to the source.

    The one walk of Go's literal grammar. Two consumers read it and neither
    re-spells it: `_blank_literal` blanks the span it returns, and
    `string_literals` (CS-13) yields the span so the rule can read the text
    inside. A second implementation would be two answers to "where does this
    literal end", and the two rules would disagree about which bytes are code.
    """
    length = len(source)
    cursor = start + 1
    while cursor < length:
        char = source[cursor]
        if quote != "`" and char == "\\":
            cursor += 2
            continue
        if char == quote:
            cursor += 1
            break
        if quote != "`" and char == "\n":
            break  # unterminated literal; do not swallow the rest of the file
        cursor += 1
    return max(min(cursor, length), start + 1)


def _blank_literal(source: str, out: list[str], start: int, quote: str) -> int:
    end = literal_end(source, start, quote)
    _blank_span(out, start, end)
    return end


_BRACKET_PAIRS = {"(": ")", "[": "]", "{": "}"}


def matching_bracket(code: str, opening: int) -> int:
    """Index of the bracket closing the one at `opening`, or -1 if unbalanced."""
    stack: list[str] = []
    for index in range(opening, len(code)):
        char = code[index]
        if char in _BRACKET_PAIRS:
            stack.append(char)
        elif char in ")]}":
            if not stack or _BRACKET_PAIRS[stack.pop()] != char:
                return -1
            if not stack:
                return index
    return -1


def split_top_level(text: str, separators: str = ",") -> list[str]:
    """Split `text` on `separators` that sit outside every bracket pair.

    Separators are dropped and each part keeps its original length, so a caller
    tracking offsets can advance by `len(part) + 1` per separator.
    """
    parts: list[str] = []
    current: list[str] = []
    depth = 0
    for char in text:
        if char in _BRACKET_PAIRS:
            depth += 1
        elif char in ")]}":
            depth -= 1
        if depth == 0 and char in separators:
            parts.append("".join(current))
            current = []
        else:
            current.append(char)
    parts.append("".join(current))
    return parts


def _skip_space(code: str, index: int) -> int:
    while index < len(code) and code[index] in " \t\n\r":
        index += 1
    return index


_IDENTIFIER = re.compile(r"[A-Za-z_]\w*")

# An identifier that opens a type rather than naming a parameter. `map[K]V`,
# `chan T`, `func(...)`, `interface{...}` and `struct{...}` are the only Go
# types whose first token is a keyword, so this list is closed.
_TYPE_KEYWORDS = frozenset({"map", "chan", "func", "interface", "struct"})

_NAMED_ELEMENT = re.compile(r"^([A-Za-z_]\w*)\s+\S")


def _element_is_named(element: str) -> bool:
    stripped = element.strip()
    if not stripped:
        return False
    match = _NAMED_ELEMENT.match(stripped)
    if match is None:
        return False
    return match.group(1) not in _TYPE_KEYWORDS


# --- signatures -----------------------------------------------------------


class Signature(NamedTuple):
    """One parameter list found in the source, classified as a whole.

    Go forbids mixing named and unnamed parameters within a single list, so
    `named` is a property of the list, not of an individual parameter, and
    `unnamed_params` is either 0 or `params`.
    """

    kind: str  # "decl" | "functype" | "iface"
    offset: int
    name: str | None
    params: int
    named: bool

    @property
    def unnamed_params(self) -> int:
        return 0 if self.named else self.params


def _classify(kind: str, offset: int, name: str | None, params_text: str) -> Signature:
    elements = [
        element for element in split_top_level(params_text) if element.strip()
    ]
    named = any(_element_is_named(element) for element in elements)
    return Signature(
        kind=kind,
        offset=offset,
        name=name,
        params=len(elements),
        named=named or not elements,
    )


_FUNC_DECLARATION = re.compile(r"(?m)^func\b")
_FUNC_TYPE = re.compile(r"\bfunc\s*\(")
_INTERFACE_BODY = re.compile(r"\binterface\s*\{")
_TYPE_KEYWORD = re.compile(r"\btype\b")
_METHOD_HEAD = re.compile(r"([A-Za-z_]\w*)\s*\(")


class FunctionHead(NamedTuple):
    """One top-level `func` declaration, located but not yet classified.

    The one walk of the `func` grammar. Three consumers read it — the CS-2
    signature pass, the CS-6 dispatch pass and the CS-8 result pass — and a
    second walk would be a second answer to "what was declared here", which is
    the defect the shared `_spec_name_and_type_start` already exists to prevent
    on the `type` side.

    `receiver` is the receiver list's source text, or `None` for a plain
    function; that distinction is what CS-6 filters *for* and CS-8 filters
    *against*.
    """

    keyword_offset: int
    name: str
    receiver: str | None
    params_open: int
    params_close: int


def function_heads(code: str) -> Iterator[FunctionHead]:
    """Every top-level `func` declaration in one file, in source order.

    A declaration whose brackets do not balance is skipped rather than guessed
    at: the lexer has already blanked comments and literals, so an unbalanced
    bracket here means the file does not parse, and a partial reading of it
    would be a finding about nothing.
    """
    for match in _FUNC_DECLARATION.finditer(code):
        cursor = _skip_space(code, match.end())
        receiver: str | None = None
        if cursor < len(code) and code[cursor] == "(":  # method receiver
            closing = matching_bracket(code, cursor)
            if closing == -1:
                continue
            receiver = code[cursor + 1 : closing]
            cursor = _skip_space(code, closing + 1)
        name_match = _IDENTIFIER.match(code, cursor)
        if name_match is None:
            continue
        cursor = _skip_space(code, name_match.end())
        if cursor < len(code) and code[cursor] == "[":  # type parameters
            closing = matching_bracket(code, cursor)
            if closing == -1:
                continue
            cursor = _skip_space(code, closing + 1)
        if cursor >= len(code) or code[cursor] != "(":
            continue
        params_close = matching_bracket(code, cursor)
        if params_close == -1:
            continue
        yield FunctionHead(
            keyword_offset=match.start(),
            name=name_match.group(0),
            receiver=receiver,
            params_open=cursor,
            params_close=params_close,
        )


def _declaration_signatures(code: str) -> tuple[list[Signature], set[int]]:
    """Top-level `func` declarations, plus the offsets of their `func` keywords.

    The offsets let the func-type pass skip a method's receiver list, which
    would otherwise read as an anonymous func type.
    """
    signatures: list[Signature] = []
    keyword_offsets: set[int] = set()
    for head in function_heads(code):
        keyword_offsets.add(head.keyword_offset)
        signatures.append(
            _classify(
                "decl",
                head.keyword_offset,
                head.name,
                code[head.params_open + 1 : head.params_close],
            )
        )
    return signatures, keyword_offsets


def _func_type_signatures(code: str, declaration_offsets: set[int]) -> list[Signature]:
    """Anonymous func types and func literals: `type H func(...)`, fields, closures."""
    signatures: list[Signature] = []
    for match in _FUNC_TYPE.finditer(code):
        if match.start() in declaration_offsets:
            continue
        opening = match.end() - 1
        closing = matching_bracket(code, opening)
        if closing == -1:
            continue
        signatures.append(
            _classify("functype", match.start(), None, code[opening + 1 : closing])
        )
    return signatures


def _interface_method_signatures(code: str) -> list[Signature]:
    """Method signatures inside every interface body, named or anonymous.

    Embedded interfaces (`io.Reader`) and constraint elements (`~int | ~string`)
    do not match the method head and are skipped.
    """
    signatures: list[Signature] = []
    for match in _INTERFACE_BODY.finditer(code):
        opening = match.end() - 1
        closing = matching_bracket(code, opening)
        if closing == -1:
            continue
        base = opening + 1
        offset = 0
        for element in split_top_level(code[base:closing], separators="\n;"):
            stripped = element.lstrip()
            head = _METHOD_HEAD.match(stripped)
            if head is not None:
                lead = len(element) - len(stripped)
                params_open = base + offset + lead + head.end() - 1
                params_close = matching_bracket(code, params_open)
                if params_close != -1:
                    signatures.append(
                        _classify(
                            "iface",
                            base + offset + lead,
                            head.group(1),
                            code[params_open + 1 : params_close],
                        )
                    )
            offset += len(element) + 1
    return signatures


def signatures(go_file: GoFile) -> Iterator[Signature]:
    """Every parameter list in one file, classified."""
    declarations, keyword_offsets = _declaration_signatures(go_file.code)
    yield from declarations
    yield from _func_type_signatures(go_file.code, keyword_offsets)
    yield from _interface_method_signatures(go_file.code)


# --- interface declarations ----------------------------------------------


class InterfaceDeclaration(NamedTuple):
    """One named `type X interface { ... }`, single or inside a `type (...)` group."""

    offset: int
    name: str

    @property
    def exported(self) -> bool:
        return self.name[:1].isupper()

    @property
    def i_prefixed(self) -> bool:
        return re.match(r"^[Ii][A-Z]", self.name) is not None


def _spec_name_and_type_start(code: str, index: int) -> tuple[str, int] | None:
    """The name declared at `index` and the offset where its type expression starts.

    Type parameters and the `=` of an alias are stepped over, so `type Store[T
    any] interface {` and `type State = any` both report the offset of the type
    itself. Shared by every consumer that reads a type spec — the CS-1
    interface scan and the CS-7 erasure scan — because two walks of the same
    grammar would be two answers to "what was declared here".
    """
    name_match = _IDENTIFIER.match(code, index)
    if name_match is None:
        return None
    cursor = _skip_space(code, name_match.end())
    if cursor < len(code) and code[cursor] == "[":  # type parameters
        closing = matching_bracket(code, cursor)
        if closing == -1:
            return None
        cursor = _skip_space(code, closing + 1)
    if code.startswith("=", cursor):  # type alias
        cursor = _skip_space(code, cursor + 1)
    return name_match.group(0), cursor


def _interface_spec_name(code: str, index: int) -> str | None:
    spec = _spec_name_and_type_start(code, index)
    if spec is None:
        return None
    name, cursor = spec
    return name if _INTERFACE_BODY.match(code, cursor) is not None else None


def _type_spec_offsets(code: str) -> list[int]:
    """Offset of every type spec's name: single declarations and `type (...)` groups.

    One walk of the `type` keyword, shared rather than repeated per consumer.
    """
    offsets: list[int] = []
    for match in _TYPE_KEYWORD.finditer(code):
        if match.start() > 0 and code[match.start() - 1] == "(":
            continue  # `switch x := y.(type)`, not a declaration
        cursor = _skip_space(code, match.end())
        if cursor < len(code) and code[cursor] == "(":
            closing = matching_bracket(code, cursor)
            if closing == -1:
                continue
            base = cursor + 1
            offset = 0
            for spec in split_top_level(code[base:closing], separators="\n;"):
                lead = len(spec) - len(spec.lstrip())
                offsets.append(base + offset + lead)
                offset += len(spec) + 1
            continue
        offsets.append(cursor)
    return offsets


def interface_declarations(go_file: GoFile) -> list[InterfaceDeclaration]:
    """Every named interface declared in one file."""
    code = go_file.code
    found: list[InterfaceDeclaration] = []
    for start in _type_spec_offsets(code):
        name = _interface_spec_name(code, start)
        if name is not None:
            found.append(InterfaceDeclaration(offset=start, name=name))
    return found


# --- mutex declarations ---------------------------------------------------


class MutexDeclaration(NamedTuple):
    """One declared `sync.Mutex`/`sync.RWMutex`: a struct field or a `var` spec.

    CS-5 is about shared-memory locking as a *design*, so what is counted is
    the declaration of the lock, not the `Lock()`/`Unlock()` calls that follow
    from it. `holder` names the struct type when the declaration is a field and
    the type name is recoverable, so a finding can say what the lock guards.
    """

    offset: int
    name: str | None  # None for an embedded field: `struct { sync.Mutex }`
    type_name: str  # "sync.Mutex" or "sync.RWMutex"
    kind: str  # "field" | "embedded" | "var"
    holder: str | None  # enclosing named struct type, when one was found


_MUTEX_TYPE = r"sync\.(?:RW)?Mutex"
_STRUCT_BODY = re.compile(r"\bstruct\s*\{")
_VAR_KEYWORD = re.compile(r"\bvar\b")
_NAME_LIST = r"[A-Za-z_]\w*(?:\s*,\s*[A-Za-z_]\w*)*"

# A struct field or var spec whose type *is* a mutex. The trailing `\s*$`
# matters: struct tags and comments are already blanked to spaces, so anything
# else left on the line means the type is something other than a bare mutex.
_MUTEX_FIELD = re.compile(
    rf"^(?:(?P<names>{_NAME_LIST})\s+)?\*?(?P<type>{_MUTEX_TYPE})\s*$"
)
_MUTEX_VAR = re.compile(rf"^(?P<names>{_NAME_LIST})\s+\*?(?P<type>{_MUTEX_TYPE})\b")
_MUTEX_VAR_LITERAL = re.compile(
    rf"^(?P<names>{_NAME_LIST})\s*=\s*[&*]?(?P<type>{_MUTEX_TYPE})\s*\{{"
)
# `type worker struct {` / `worker struct {` inside a `type (...)` group.
_STRUCT_HOLDER = re.compile(r"([A-Za-z_]\w*)(?:\s*\[[^\]]*\])?\s+struct\s*$")


def _mutex_type(match: re.Match[str]) -> str:
    return match.group("type").replace(" ", "")


def _first_name(match: re.Match[str]) -> str | None:
    names = match.groupdict().get("names")
    if not names:
        return None
    return names.split(",")[0].strip()


def _holder_of(code: str, struct_keyword: int) -> str | None:
    """The type name a `struct {` at `struct_keyword` belongs to, if any."""
    line_start = code.rfind("\n", 0, struct_keyword) + 1
    holder = _STRUCT_HOLDER.search(code[line_start:struct_keyword] + "struct")
    return holder.group(1) if holder else None


def _struct_field_mutexes(code: str) -> list[MutexDeclaration]:
    """Mutex-typed fields in every struct body, nested bodies included.

    Nested structs are covered because each `struct {` is visited in its own
    right; the outer body's top-level split keeps a nested body in one element,
    which then simply fails to match a field pattern.
    """
    found: list[MutexDeclaration] = []
    for match in _STRUCT_BODY.finditer(code):
        opening = match.end() - 1
        closing = matching_bracket(code, opening)
        if closing == -1:
            continue
        holder = _holder_of(code, match.start())
        base = opening + 1
        offset = 0
        for element in split_top_level(code[base:closing], separators="\n;"):
            field = _MUTEX_FIELD.match(element.strip())
            if field is not None:
                name = _first_name(field)
                lead = len(element) - len(element.lstrip())
                found.append(
                    MutexDeclaration(
                        offset=base + offset + lead,
                        name=name,
                        type_name=_mutex_type(field),
                        kind="field" if name else "embedded",
                        holder=holder,
                    )
                )
            offset += len(element) + 1
    return found


def _var_mutexes(code: str) -> list[MutexDeclaration]:
    """Mutex-typed `var` specs, single or inside a `var (...)` group."""
    found: list[MutexDeclaration] = []
    for match in _VAR_KEYWORD.finditer(code):
        cursor = _skip_space(code, match.end())
        specs: list[tuple[int, str]] = []
        if cursor < len(code) and code[cursor] == "(":
            closing = matching_bracket(code, cursor)
            if closing == -1:
                continue
            base = cursor + 1
            offset = 0
            for spec in split_top_level(code[base:closing], separators="\n;"):
                lead = len(spec) - len(spec.lstrip())
                specs.append((base + offset + lead, spec.strip()))
                offset += len(spec) + 1
        else:
            end = code.find("\n", cursor)
            end = len(code) if end == -1 else end
            specs.append((cursor, code[cursor:end].strip()))
        for offset, spec in specs:
            declaration = _MUTEX_VAR.match(spec) or _MUTEX_VAR_LITERAL.match(spec)
            if declaration is None:
                continue
            found.append(
                MutexDeclaration(
                    offset=offset,
                    name=_first_name(declaration),
                    type_name=_mutex_type(declaration),
                    kind="var",
                    holder=None,
                )
            )
    return found


def mutex_declarations(go_file: GoFile) -> list[MutexDeclaration]:
    """Every declared `sync.Mutex`/`sync.RWMutex` in one file, in source order."""
    declarations = _struct_field_mutexes(go_file.code) + _var_mutexes(go_file.code)
    declarations.sort(key=lambda declaration: declaration.offset)
    return declarations


# --- dispatch methods -----------------------------------------------------

# How many sibling calls make a body a dispatch *list* rather than a sequence.
# Three is the point at which the counterweight in `go-rules.md` § CS-6 stops
# applying on its own: two ordered steps are a sequence by anyone's reading.
DISPATCH_MINIMUM = 3


class DispatchMethod(NamedTuple):
    """One method whose body is nothing but argument-less sibling calls.

    This is the shape CS-6 is named after: a family of same-signature behaviors
    accumulated as methods on one struct and wired together by a hardcoded call
    list, where a registry of function values would have been data the code
    could iterate, count, and assert completeness over.

    `calls` holds the sibling method names in source order, so a finding can say
    how long the list is — which is the whole argument, since a list that only a
    human can enumerate is a list a spec cannot.
    """

    offset: int
    name: str
    receiver: str
    receiver_type: str | None
    calls: tuple[str, ...]


# `documentValidator *validator`, `pool WorkerPool`, `store *Store[T]`. The
# receiver name is what every dispatched call must be spelled against.
_RECEIVER = re.compile(r"^\s*(?P<name>[A-Za-z_]\w*)\s+\*?\s*(?P<type>[A-Za-z_]\w*)")


def _body_span(code: str, params_close: int) -> tuple[int, int]:
    """Offsets bracketing a function body, or `(-1, -1)` if none is found.

    The body brace is the first `{` at parenthesis/bracket depth zero after the
    parameter list. A result list is crossed on the way; a result type that is
    itself brace-delimited (`interface{}`, an anonymous `struct{...}`) fools
    this into reading the type's brace as the body, which then yields no
    statements and no finding. Under-reporting, deliberately.
    """
    depth = 0
    for index in range(params_close + 1, len(code)):
        char = code[index]
        if char in "([":
            depth += 1
        elif char in ")]":
            depth -= 1
        elif depth == 0 and char == "{":
            closing = matching_bracket(code, index)
            return (index, closing) if closing != -1 else (-1, -1)
        elif depth == 0 and char == "\n":
            break  # an interface method or a declaration with no body
    return (-1, -1)


def _sibling_calls(body: str, receiver_name: str) -> tuple[str, ...] | None:
    """Sibling call names if the body is *only* those calls, else `None`.

    A single statement of any other kind returns `None` rather than being
    skipped. That strictness is the detector's whole precision budget: the
    bodies that survive it cannot be anything but a dispatch list.
    """
    call = re.compile(rf"^{re.escape(receiver_name)}\.([A-Za-z_]\w*)\(\)$")
    names: list[str] = []
    for element in split_top_level(body, separators="\n;"):
        stripped = element.strip()
        if not stripped:
            continue
        match = call.match(stripped)
        if match is None:
            return None
        names.append(match.group(1))
    return tuple(names)


def dispatch_methods(
    go_file: GoFile, minimum: int = DISPATCH_MINIMUM
) -> list[DispatchMethod]:
    """Every hardcoded-dispatch-list method in one file, in source order."""
    code = go_file.code
    found: list[DispatchMethod] = []
    for head in function_heads(code):
        if head.receiver is None:
            continue  # a plain function, not a method
        receiver = _RECEIVER.match(head.receiver)
        if receiver is None or receiver.group("name") == "_":
            continue  # an unnamed receiver dispatches to nothing
        body_open, body_close = _body_span(code, head.params_close)
        if body_open == -1:
            continue
        calls = _sibling_calls(code[body_open + 1 : body_close], receiver.group("name"))
        if calls is None or len(calls) < minimum:
            continue
        found.append(
            DispatchMethod(
                offset=head.keyword_offset,
                name=head.name,
                receiver=receiver.group("name"),
                receiver_type=receiver.group("type"),
                calls=calls,
            )
        )
    return found


# --- erased boundaries ----------------------------------------------------

# The one spelling of "a value whose type this contract refuses to know" that
# CS-7 reports. `any` is an alias for `interface{}` and both are written in this
# tree, so both are recognised — but only *bare*. `[]any`, `map[string]any`,
# `*any` and `...any` are containers of dynamic elements or the `fmt.Println`
# shape, not a contract erasing one value's own type, which is what the rule is
# about.
_ANY_LITERAL = "any"
_EMPTY_INTERFACE = re.compile(r"^interface\s*\{\s*\}$")
_STRUCT_BODY_TYPE = re.compile(r"\bstruct\s*\{")

# A struct field: one or more names, then the type. Struct tags are string
# literals and are already blanked to spaces, so the type runs to end of element.
_STRUCT_FIELD = re.compile(rf"^(?P<names>{_NAME_LIST})\s+(?P<type>\S.*)$")


class ErasedBoundary(NamedTuple):
    """One exported member of a public contract whose type is bare `any`.

    CS-7 is about *where* erasure lives, not about whether it happens at all: a
    heterogeneous registry has to erase somewhere, and the rule is that it
    erases once, inside the library owning the collection, behind a generic
    shell. So what is counted is the erasure an author is asked to hold — an
    exported interface method or exported struct field in a non-internal
    package — and never the unexported adapter that is the correct place for it.

    `via_alias` records that the type was written as a package-local name
    declared `type X any`, which is how the founding evidence spelled it
    (`type State any`). Without alias resolution the rule's own founding
    evidence would not be detected at all.
    """

    offset: int
    kind: str  # "method" | "field"
    holder: str  # the exported interface or struct type
    member: str  # the exported method or field
    position: str  # "parameter" | "result" | "field type"
    type_text: str  # the type exactly as written
    via_alias: bool


def in_internal_package(relative_path: str) -> bool:
    """Whether a repo-relative path sits under a Go `internal/` package.

    CS-7's scope restriction, and a load-bearing one rather than a convenience:
    Go's own `internal/` rule already makes such a package unimportable from
    outside its subtree, so it cannot carry a public contract, and erasing
    inside a library is exactly what CS-7 permits.
    """
    return relative_path.startswith("internal/") or "/internal/" in relative_path


def _is_erased_type(type_text: str, aliases: frozenset[str]) -> bool:
    stripped = " ".join(type_text.split())
    if stripped == _ANY_LITERAL or _EMPTY_INTERFACE.match(stripped) is not None:
        return True
    return stripped in aliases


def _element_type(element: str) -> str:
    """The type half of one parameter, result or field element.

    Elements arrive already split on top-level commas, so each is either
    `name Type` or a bare `Type`.
    """
    stripped = element.strip()
    if not _element_is_named(stripped):
        return stripped
    name_match = _IDENTIFIER.match(stripped)
    return stripped[name_match.end() :].strip() if name_match else stripped


# A `{` that opens a type rather than a function body. `interface{}` and an
# anonymous `struct{...}` are the only result types written with a brace, so
# this test is closed the way `_TYPE_KEYWORDS` is.
_TYPE_BRACE = re.compile(r"\b(?:interface|struct)\s*$")


def result_elements(code: str, params_close: int) -> list[str]:
    """The result types of a signature whose parameter list closes at `params_close`.

    One reader for every signature the module walks: an interface method and a
    func type, which have no body, and a top-level declaration, which does.
    The single-result walk therefore has to stop at a body brace without
    stopping at `interface{}` or `struct{...}` written *as* the result — the
    difference `_TYPE_BRACE` decides, and the reason this is one function
    rather than one per caller.
    """
    cursor = _skip_space(code, params_close + 1)
    if cursor >= len(code):
        return []
    if code[cursor] == "(":
        closing = matching_bracket(code, cursor)
        if closing == -1:
            return []
        return [
            element
            for element in split_top_level(code[cursor + 1 : closing])
            if element.strip()
        ]
    end = cursor
    depth = 0
    while end < len(code):
        char = code[end]
        if char in "([":
            depth += 1
        elif char == "{":
            if depth == 0 and _TYPE_BRACE.search(code[cursor:end]) is None:
                break  # the function body
            depth += 1
        elif char in ")]}":
            if depth == 0:
                break
            depth -= 1
        elif depth == 0 and char in "\n;,":
            break
        end += 1
    single = code[cursor:end].strip()
    return [single] if single else []


def erased_type_names(go_file: GoFile) -> frozenset[str]:
    """Package-local names declared `type X any` (or `= any`, or `interface{}`).

    Resolved per *file*, because every consumer of this module scans one file at
    a time. An alias used across a package boundary is therefore missed — the
    same under-reporting direction as every other imprecision here.
    """
    code = go_file.code
    names: set[str] = set()
    for start in _type_spec_offsets(code):
        spec = _spec_name_and_type_start(code, start)
        if spec is None:
            continue
        name, cursor = spec
        end = code.find("\n", cursor)
        end = len(code) if end == -1 else end
        if _is_erased_type(code[cursor:end], frozenset()):
            names.add(name)
    return frozenset(names)


def _named_type_bodies(
    code: str, body_pattern: re.Pattern[str], exported_only: bool = True
) -> list[tuple[str, int, int]]:
    """`(name, body_open, body_close)` for each named type with that body.

    Exported only by default, because CS-7's two readers ask about a public
    contract. The interface-field lane passes False: a private struct holding an
    interface is exactly the injected-dependency shape that lane exists to
    locate, and dropping it would hide most of what it counts.
    """
    bodies: list[tuple[str, int, int]] = []
    for start in _type_spec_offsets(code):
        spec = _spec_name_and_type_start(code, start)
        if spec is None:
            continue
        name, cursor = spec
        if exported_only and not name[:1].isupper():
            continue  # an unexported type is not a public contract
        body = body_pattern.match(code, cursor)
        if body is None:
            continue
        opening = body.end() - 1
        closing = matching_bracket(code, opening)
        if closing != -1:
            bodies.append((name, opening, closing))
    return bodies


def _interface_erasures(
    code: str, go_file: GoFile, aliases: frozenset[str]
) -> list[ErasedBoundary]:
    found: list[ErasedBoundary] = []
    for holder, opening, closing in _named_type_bodies(code, _INTERFACE_BODY):
        base = opening + 1
        offset = 0
        for element in split_top_level(code[base:closing], separators="\n;"):
            stripped = element.lstrip()
            head = _METHOD_HEAD.match(stripped)
            if head is None or not head.group(1)[:1].isupper():
                offset += len(element) + 1
                continue
            lead = len(element) - len(stripped)
            method_offset = base + offset + lead
            params_open = method_offset + head.end() - 1
            params_close = matching_bracket(code, params_open)
            if params_close != -1:
                positioned = [
                    ("parameter", part)
                    for part in split_top_level(code[params_open + 1 : params_close])
                    if part.strip()
                ] + [
                    ("result", part)
                    for part in result_elements(code, params_close)
                ]
                for position, part in positioned:
                    type_text = _element_type(part)
                    if _is_erased_type(type_text, aliases):
                        found.append(
                            ErasedBoundary(
                                offset=method_offset,
                                kind="method",
                                holder=holder,
                                member=head.group(1),
                                position=position,
                                type_text=" ".join(type_text.split()),
                                via_alias=" ".join(type_text.split()) in aliases,
                            )
                        )
            offset += len(element) + 1
    return found


def _field_erasures(
    code: str, go_file: GoFile, aliases: frozenset[str]
) -> list[ErasedBoundary]:
    found: list[ErasedBoundary] = []
    for holder, opening, closing in _named_type_bodies(code, _STRUCT_BODY_TYPE):
        base = opening + 1
        offset = 0
        for element in split_top_level(code[base:closing], separators="\n;"):
            field = _STRUCT_FIELD.match(element.strip())
            if field is not None:
                name = field.group("names").split(",")[0].strip()
                type_text = field.group("type").strip()
                if name[:1].isupper() and _is_erased_type(type_text, aliases):
                    lead = len(element) - len(element.lstrip())
                    found.append(
                        ErasedBoundary(
                            offset=base + offset + lead,
                            kind="field",
                            holder=holder,
                            member=name,
                            position="field type",
                            type_text=" ".join(type_text.split()),
                            via_alias=" ".join(type_text.split()) in aliases,
                        )
                    )
            offset += len(element) + 1
    return found


def erased_boundaries(go_file: GoFile) -> list[ErasedBoundary]:
    """Every CS-7 erased public boundary in one file, in source order.

    Empty for a file under an `internal/` package: that is not a public
    contract, and internal erasure is what the rule asks for.
    """
    if in_internal_package(go_file.path):
        return []
    aliases = erased_type_names(go_file)
    code = go_file.code
    boundaries = _interface_erasures(code, go_file, aliases) + _field_erasures(
        code, go_file, aliases
    )
    boundaries.sort(key=lambda boundary: boundary.offset)
    return boundaries


# --- interface returns ----------------------------------------------------

# A result type that is a bare interface: an optional package qualifier and an
# I-prefixed name, nothing else. `*IStore`, `func(...) IIdentity` and the
# instantiated `IWidget[S]` all fail to match, and each of those exclusions
# carries a piece of CS-8's own scope rather than trimming noise — the module
# header states which.
_QUALIFIED_INTERFACE = re.compile(
    r"^(?:(?P<package>[A-Za-z_]\w*)\.)?(?P<name>[Ii][A-Z]\w*)$"
)

# The composite wrappers a result may be built out of and still be this
# declaration's own choice of interface: a slice, an array of any length, a
# variadic, and a map's value. Peeled left to right until a bare type remains,
# so `[]IEffect`, `[]map[string]IEffect` and `...IEffect` all reach the same
# name `[]IEffect` used to hide.
#
# Operator ruling, 2026-09-03: `func retryWatch(ev live.Event) []live.IEffect`
# is the shape the rule was written about, and reading it as "a container, not
# this function handing back one implementation" was the reading that let a
# repo-owned framework contract stay interface-valued for a month. A caller
# receiving a slice of interfaces receives interfaces.
#
# A pointer stays out: `*IStore` is a pointer to an interface, which is a
# different mistake with a different fix. A map *key* stays out too — the value
# position is the one a returned implementation travels in — and both are the
# usual under-reporting direction.
_COMPOSITE_PREFIX = re.compile(r"^(?:\.\.\.|\[\s*\]|\[[^\[\]]*\]|map\[[^\[\]]*\])\s*")


def interface_result_name(type_text: str) -> str | None:
    """The interface name a result type hands back, or None if it hands back none.

    One reader for the bare case and the composite one, so that
    `interface_returns` asks a single question and the eval fixtures can pin
    both shapes against the same function.
    """
    remainder = type_text
    while True:
        match = _QUALIFIED_INTERFACE.match(remainder)
        if match is not None:
            return match.group("name")
        prefix = _COMPOSITE_PREFIX.match(remainder)
        if prefix is None:
            return None
        remainder = remainder[prefix.end() :].strip()

# Why a reported interface result is not reported after all. Both are cases
# where the result type is not this declaration's to choose; neither is an
# exclusion list, and neither can be reached by writing a marker comment.
EXEMPT_SEALED = "sealed sum type"
EXEMPT_CONTRACT = "contract-fixed signature"


class InterfaceReturn(NamedTuple):
    """One `func` declaration whose result list hands back an interface.

    CS-8 is about what a caller receives where it could have received a
    concrete type, so what is counted is the *result* — never a parameter,
    which is the position the rule explicitly wants an interface in.

    `receiver_type` is the method receiver's type when the declaration is a
    method, and `None` for a plain function. Only a plain function is reported:
    a method's result type is fixed by whatever interface it satisfies, and the
    decision lives in that interface, not here.

    `exempt` names why an otherwise-reportable result is not this declaration's
    choice either — `EXEMPT_SEALED` for a closed union, `EXEMPT_CONTRACT` for a
    hook implementation — or is `None` when the finding stands.
    """

    offset: int
    name: str
    receiver_type: str | None
    position: int  # 1-indexed position in the result list
    arity: int  # how many results the list has
    type_text: str  # the interface type exactly as written
    exempt: str | None

    @property
    def reported(self) -> bool:
        """Whether this is a CS-8 finding rather than a measurement."""
        return self.receiver_type is None and self.exempt is None


class InterfaceIndex(NamedTuple):
    """What the whole corpus knows that one file cannot answer on its own.

    CS-8 is the first rule whose detector is not per-file, and it has to be:
    the founding site returns `warden.IPeerDiscoverer` from a `main` package
    that declares no interfaces at all, so a per-file detector would miss the
    rule's own evidence. Scanning one file alone therefore reports only what
    that file declares — which is what makes an eval fixture self-contained.
    """

    names: frozenset[str]  # every named interface declared in the corpus
    sealed: frozenset[str]  # of those, the exported ones closed by an unexported method
    hooks: frozenset[str]  # normalized signatures of every declared func type


def sealed_interface_names(go_file: GoFile) -> frozenset[str]:
    """Exported interfaces carrying an unexported method: Go's closed sum type.

    An unexported method cannot be implemented outside the declaring package,
    so the set of variants is closed and the package owns all of them. That is
    how Go spells a tagged union — `protocol.IInbound`'s `isInbound()` is the
    idiom exactly — and a function returning one is returning a union whose
    variant is a property of its input, not a factory hiding a choice.

    *Exported* is load-bearing: an unexported interface with unexported methods
    is an ordinary package-local abstraction, not a sealed union, and the
    corpus contains one whose factory is a CS-8 violation of the purest kind
    (`go/benchmarks/gc/cmd/gcbench/main.go`'s `iWorkloadRunner`).
    """
    code = go_file.code
    sealed: set[str] = set()
    for holder, opening, closing in _named_type_bodies(code, _INTERFACE_BODY):
        for element in split_top_level(code[opening + 1 : closing], separators="\n;"):
            head = _METHOD_HEAD.match(element.lstrip())
            if head is not None and head.group(1)[:1].islower():
                sealed.add(holder)
                break
    return frozenset(sealed)


def _join_types(types: list[str]) -> str:
    return ", ".join(" ".join(one.split()) for one in types)


def normalized_signature(code: str, params_open: int, params_close: int) -> str:
    """A signature reduced to its types, so a declaration and a hook can be compared.

    Parameter names are dropped because a hook type and the function written to
    satisfy it need not agree on them, and CS-2 guarantees both have some. What
    survives is `(paramTypes) (resultTypes)`, which is exactly the part Go's own
    assignability rule compares.
    """
    parameters = [
        _element_type(element)
        for element in split_top_level(code[params_open + 1 : params_close])
        if element.strip()
    ]
    results = [_element_type(element) for element in result_elements(code, params_close)]
    return f"({_join_types(parameters)}) ({_join_types(results)})"


def hook_signatures(go_file: GoFile) -> frozenset[str]:
    """Normalized signatures of every `func` type written in one file.

    A named `type Authenticate func(...)`, a struct field
    `Authenticate func(request *http.Request) (IIdentity, error)`, a func-typed
    parameter and a func literal all count: each is a contract some function is
    written to satisfy, and a function that satisfies one did not choose its own
    result type. Method receivers are skipped the same way the CS-2 func-type
    pass skips them, by offset.
    """
    code = go_file.code
    _, declaration_offsets = _declaration_signatures(code)
    found: set[str] = set()
    for match in _FUNC_TYPE.finditer(code):
        if match.start() in declaration_offsets:
            continue  # a method receiver, not a func type
        params_open = match.end() - 1
        params_close = matching_bracket(code, params_open)
        if params_close == -1:
            continue
        found.add(normalized_signature(code, params_open, params_close))
    return frozenset(found)


def interface_index(files: list[GoFile]) -> InterfaceIndex:
    """The corpus-wide facts CS-8's per-file pass consults."""
    names: set[str] = set()
    sealed: set[str] = set()
    hooks: set[str] = set()
    for go_file in files:
        names.update(
            declaration.name for declaration in interface_declarations(go_file)
        )
        sealed.update(sealed_interface_names(go_file))
        hooks.update(hook_signatures(go_file))
    return InterfaceIndex(
        names=frozenset(names), sealed=frozenset(sealed), hooks=frozenset(hooks)
    )


def _receiver_type(receiver: str) -> str:
    """The type a receiver list binds, named or not.

    `_RECEIVER` wants `name Type`, which a receiver written type-only —
    `func (realClock) NewTimer(...)`, three of which are in this corpus — does
    not have. Methodhood is decided by the receiver list *existing*, never by
    this parse succeeding: reading an unnamed receiver as "no receiver" would
    have reported three interface-satisfying methods as factories.
    """
    match = _RECEIVER.match(receiver)
    if match is not None:
        return match.group("type")
    stripped = receiver.strip().lstrip("*").strip()
    return stripped.split("[")[0] or "?"


def interface_returns(go_file: GoFile, index: InterfaceIndex) -> list[InterfaceReturn]:
    """Every declaration in one file whose result list hands back an interface.

    Methods and exempt results are included: `reported` is what separates a
    CS-8 finding from a measurement, and the census carries both, so the scope
    the rule does not enforce stays visible instead of being invisible.
    """
    code = go_file.code
    found: list[InterfaceReturn] = []
    for head in function_heads(code):
        elements = result_elements(code, head.params_close)
        receiver_type = (
            None if head.receiver is None else _receiver_type(head.receiver)
        )
        for position, element in enumerate(elements, start=1):
            type_text = " ".join(_element_type(element).split())
            interface_name = interface_result_name(type_text)
            if interface_name is None or interface_name not in index.names:
                continue
            exempt: str | None = None
            if interface_name in index.sealed:
                exempt = EXEMPT_SEALED
            elif (
                normalized_signature(code, head.params_open, head.params_close)
                in index.hooks
            ):
                exempt = EXEMPT_CONTRACT
            found.append(
                InterfaceReturn(
                    offset=head.keyword_offset,
                    name=head.name,
                    receiver_type=receiver_type,
                    position=position,
                    arity=len(elements),
                    type_text=type_text,
                    exempt=exempt,
                )
            )
    return found


# --- interface-typed struct fields ----------------------------------------

# The wrappers a FIELD may be built out of and still be holding an interface.
# Wider than the result lane's: a pointer and a channel are peeled here, because
# the question this lane asks is "does this struct carry an interface", and
# `*IStore` and `chan IThing` both answer yes.
_FIELD_WRAPPER = re.compile(
    r"^(?:\*|\.\.\.|\[\s*\]|\[[^\[\]]*\]|map\[[^\[\]]*\]|(?:<-\s*)?chan(?:\s*<-)?\s)\s*"
)

_FIELD_FUNC_TYPE = re.compile(r"^func\s*\(")


class InterfaceField(NamedTuple):
    """One struct field whose declared type carries an interface the corpus names.

    Operator ruling, 2026-09-03: *"returning a struct that carries interface-typed
    fields is returning those interfaces"*. This is the textual half of seeing
    through that wrapper, and it is sound only because CS-1 reads 0 — every
    interface in this corpus is `I`-prefixed, so a name is enough to recognise
    one without a type checker. That is naming-as-instrumentation: the prefix
    rule was minted for readability and it bought a lexer the ability to answer a
    question `go/types` would otherwise be needed for.

    **It is a locator, not a verdict, and it never blocks.** Config-struct
    injection is the blessed accept-interfaces pattern — warden's ruling stands —
    so the accept side legitimately dominates this count. What the number is for
    is the other side: a struct that is RETURNED and carries an interface is
    handing that interface to its caller through a wrapper, and the wide
    `ifacereturn` lane is what sees which of those two a given struct is.
    """

    offset: int
    holder: str  # the struct type's name
    member: str  # the field's name, or "" for an embedded field
    type_text: str  # the field type exactly as written
    interface: str  # the interface name reached through it


def field_interface_name(type_text: str, names: frozenset[str]) -> str | None:
    """The interface a field type carries, or None.

    Peels the wrappers above and, for a func-typed field, looks inside its own
    parameter and result lists — which is where a variadic `...IThing` lives, the
    one shape the operator called out by name.
    """
    remainder = type_text.strip()
    while True:
        match = _QUALIFIED_INTERFACE.match(remainder)
        if match is not None:
            return match.group("name") if match.group("name") in names else None
        if _FIELD_FUNC_TYPE.match(remainder) is not None:
            return _func_type_interface(remainder, names)
        wrapper = _FIELD_WRAPPER.match(remainder)
        if wrapper is None:
            return None
        remainder = remainder[wrapper.end() :].strip()


def _func_type_interface(type_text: str, names: frozenset[str]) -> str | None:
    """The first interface in a func type's parameters or results."""
    params_open = type_text.index("(")
    params_close = matching_bracket(type_text, params_open)
    if params_close == -1:
        return None
    parts = [
        part for part in split_top_level(type_text[params_open + 1 : params_close]) if part.strip()
    ] + result_elements(type_text, params_close)
    for part in parts:
        found = field_interface_name(_element_type(part), names)
        if found is not None:
            return found
    return None


def interface_fields(go_file: GoFile, index: InterfaceIndex) -> list[InterfaceField]:
    """Every struct field in one file whose type carries a declared interface."""
    code = go_file.code
    found: list[InterfaceField] = []
    for holder, opening, closing in _named_type_bodies(
        code, _STRUCT_BODY_TYPE, exported_only=False
    ):
        base = opening + 1
        offset = 0
        for element in split_top_level(code[base:closing], separators="\n;"):
            stripped = element.strip()
            if stripped:
                field = _STRUCT_FIELD.match(stripped)
                if field is None:
                    member, type_text = "", stripped  # an embedded field
                else:
                    member = field.group("names").split(",")[0].strip()
                    type_text = field.group("type").strip()
                interface = field_interface_name(type_text, index.names)
                if interface is not None:
                    lead = len(element) - len(element.lstrip())
                    found.append(
                        InterfaceField(
                            offset=base + offset + lead,
                            holder=holder,
                            member=member,
                            type_text=" ".join(type_text.split()),
                            interface=interface,
                        )
                    )
            offset += len(element) + 1
    return found


# --- sleep loops ----------------------------------------------------------


class SleepLoop(NamedTuple):
    """One `time.Sleep` standing inside a `for` body, in a test file.

    This is the shape CS-9 is named after: a wait-until-condition written as a
    hand-rolled timing loop, in a repository whose test modules already carry a
    polling library and, since 2026-09-02, a typed await over it
    (`pkg/eventually`).

    `argument` is the sleep's argument exactly as written, so a finding can say
    whether the loop paces at a named interval or at a literal, and
    `loop_offset` is the enclosing `for`, so a reader is pointed at the loop
    rather than only at the line that sleeps.
    """

    offset: int
    loop_offset: int
    argument: str


_SLEEP_CALL = re.compile(r"\btime\.Sleep\s*\(")
_LOOP_KEYWORD = re.compile(r"\bfor\b")

_TEST_FILE_SUFFIX = "_test.go"


def is_test_file(relative_path: str) -> bool:
    """Whether a repo-relative path is a Go test file.

    CS-9's scope restriction, and a load-bearing one rather than a
    convenience: a sleep inside a loop in production code is a retry, a
    backoff or a rate limiter — all legitimate, none of them a test waiting
    for a condition — so the rule would be reporting a different thing
    entirely outside the test files.
    """
    return relative_path.endswith(_TEST_FILE_SUFFIX)


def sleep_loops(go_file: GoFile) -> list[SleepLoop]:
    """Every `time.Sleep` inside a `for` body in one test file, in source order.

    Empty for anything that is not a `_test.go` file.
    """
    if not is_test_file(go_file.path):
        return []
    code = go_file.code
    sleeps = {match.start(): match for match in _SLEEP_CALL.finditer(code)}
    if not sleeps:
        return []

    found: list[SleepLoop] = []
    # Each open brace, paired with whether the line it closes declares a loop.
    # Comments and literals are already blanked, so a `for` seen here is the
    # keyword and never a word inside a string or a sentence.
    braces: list[tuple[int, bool]] = []
    for index, char in enumerate(code):
        if char == "{":
            line_start = code.rfind("\n", 0, index) + 1
            braces.append(
                (index, _LOOP_KEYWORD.search(code[line_start:index]) is not None)
            )
            continue
        if char == "}":
            if braces:
                braces.pop()
            continue
        match = sleeps.get(index)
        if match is None:
            continue
        enclosing = next(
            (offset for offset, is_loop in reversed(braces) if is_loop), None
        )
        if enclosing is None:
            continue
        opening = match.end() - 1
        closing = matching_bracket(code, opening)
        argument = "" if closing == -1 else " ".join(code[opening + 1 : closing].split())
        found.append(
            SleepLoop(offset=index, loop_offset=enclosing, argument=argument)
        )
    return found


# --- assertion-library imports (CS-11) ------------------------------------

# The two packages CS-11 governs, quote-bounded and exact. Only these two: the
# gomega and ginkgo SUB-packages (gomega/gstruct, gomega/gexec, gomega/types,
# ginkgo/v2/... helpers) are namespaced helpers that stay qualified, so a path
# is matched only when it is exactly one of these — the alternation below is
# anchored by the closing quote, so `.../gomega/gstruct` never matches.
_GINKGO_PATH = "github.com/onsi/ginkgo/v2"
_GOMEGA_PATH = "github.com/onsi/gomega"

# The name a plain import binds: ginkgo/v2's package name is `ginkgo` (the v2 is
# a module-path version, not the package name), and gomega's is `gomega`.
_ASSERTION_DEFAULT_NAME = {_GINKGO_PATH: "ginkgo", _GOMEGA_PATH: "gomega"}

# One import spec of one of the two packages: an optional local name (".", "_",
# or an alias), then the exact quoted path. `import ` is optional so a
# single-line `import . "..."` matches as well as a spec inside an `import (...)`
# block, and the path alternation is quote-bounded so nothing but these two
# exact paths is ever seen. Read from the raw source, because the path is a
# string literal the lexer would have blanked.
_ASSERTION_IMPORT = re.compile(
    r"(?m)^[ \t]*(?:import[ \t]+)?"
    r"(?:(?P<name>\.|_|[A-Za-z]\w*)[ \t]+)?"
    r'"(?P<path>github\.com/onsi/(?:ginkgo/v2|gomega))"'
)

_PACKAGE_CLAUSE = re.compile(r"(?m)^package[ \t]+(?P<name>\w+)")

# Why a non-dot assertion import is not a finding after all: dropping the
# qualifier would redeclare one of the package's own top-level names. This is
# not an exclusion list and cannot be reached by a marker comment — it is a
# property of the Go the file would become, the same kind of structural
# exemption CS-8's sealed-sum and hook cases are.
EXEMPT_REDECLARE = "a dot import would redeclare a package identifier"


class AssertionImport(NamedTuple):
    """One import of ginkgo/v2 or gomega in a test file, classified for CS-11.

    CS-11 is the house convention that a *test* dot-imports these two so a spec
    reads Describe/It/Expect/Eventually unqualified, as this tree's suites
    already do hundreds of times over. `local_name` is what the import binds:
    "." is the dot import the rule wants; an alias, a plain package name, or "_"
    is the shape it flags.

    `exempt` names the one case a dot import cannot be written — an in-package
    test whose own package declares a top-level identifier the library also
    exports, so a dot import would be a redeclaration compile error — and is
    None when the finding stands.
    """

    offset: int
    path: str
    local_name: str
    exempt: str | None

    @property
    def is_dot(self) -> bool:
        return self.local_name == "."

    @property
    def reported(self) -> bool:
        """Whether this is a CS-11 finding rather than a dot import or an exemption."""
        return not self.is_dot and self.exempt is None


def package_name(go_file: GoFile) -> str | None:
    """The package clause of one file, or None if it declares none."""
    match = _PACKAGE_CLAUSE.search(go_file.source)
    return match.group("name") if match else None


def _directory_of(relative_path: str) -> str:
    return relative_path.rsplit("/", 1)[0] if "/" in relative_path else ""


def top_level_declared_names(go_file: GoFile) -> frozenset[str]:
    """Every top-level type and receiverless-function name declared in one file.

    These are the package-block identifiers a dot import can collide with: a
    dot-imported package's exported names land in the importing file's file
    block, and Go forbids one identifier in both the file and package block.
    Method names are not package-block identifiers and are skipped. Types
    (redis's `Entry`) and functions (a typed await shell's `Consistently`) are
    the two declaration kinds a ginkgo/gomega export actually collides with; a
    `var`/`const` of such a name is not a shape that occurs, and leaving it out
    keeps this to the two walks the module already has.
    """
    code = go_file.code
    names: set[str] = set()
    for start in _type_spec_offsets(code):
        spec = _spec_name_and_type_start(code, start)
        if spec is not None:
            names.add(spec[0])
    for head in function_heads(code):
        if head.receiver is None:
            names.add(head.name)
    return frozenset(names)


class PackageFacts(NamedTuple):
    """What one (directory, package) tells CS-11 that a single file cannot.

    `redeclares_ginkgo`/`redeclares_gomega` say a dot import of that library
    anywhere in the package would be a redeclaration compile error, because the
    package declares a top-level name the library exports. That is a fact about
    the whole package, not one file: redis's `Entry` is declared in `cache.go`,
    the proof that ginkgo exports an `Entry` too is `cache_test.go` writing
    `ginkgo.Entry`, and the file that must stay qualified because of it —
    `cache_integration_test.go` — does neither. All three are one package, so
    the collision is stored against the package and every qualified import in it
    is exempt.
    """

    redeclares_ginkgo: bool
    redeclares_gomega: bool


class _RawImport(NamedTuple):
    offset: int
    path: str
    local_name: str


def _raw_assertion_imports(go_file: GoFile) -> list[_RawImport]:
    """Every ginkgo/v2 or gomega import spec in one file, path and local name only.

    The low-level parse both `package_facts` and `assertion_imports` build on, so
    the import block is read once per file and the two never disagree about what
    was imported or under what name.
    """
    found: list[_RawImport] = []
    for match in _ASSERTION_IMPORT.finditer(go_file.source):
        name = match.group("name")
        path = match.group("path")
        local = name if name is not None else _ASSERTION_DEFAULT_NAME[path]
        found.append(_RawImport(offset=match.start(), path=path, local_name=local))
    return found


def _qualified_uses(code: str, local_name: str) -> set[str]:
    """Exported names N referenced as `local_name.N` in the blanked code.

    A `local_name.N` reference is proof the imported library exports N: you
    cannot write it otherwise. The qualifier survives blanking; a `local_name.N`
    inside a comment or a string does not, so this reads the blanked code.
    """
    return set(re.findall(rf"\b{re.escape(local_name)}\.([A-Z]\w*)", code))


def package_facts(files: list[GoFile]) -> dict[tuple[str, str], PackageFacts]:
    """Per-(directory, package) redeclaration facts, across the corpus.

    Two passes over the package's files: the first gathers every top-level name
    the package declares and, from every file that imports ginkgo or gomega
    qualified, the exported names it references through that qualifier — which is
    the corpus's own evidence of what those libraries export. A dot import
    redeclares when those two sets intersect. Keyed by (directory, package) so a
    single-file eval fixture sees only its own package, the same property CS-8's
    per-corpus index preserves.
    """
    declared: dict[tuple[str, str], set[str]] = {}
    ginkgo_used: dict[tuple[str, str], set[str]] = {}
    gomega_used: dict[tuple[str, str], set[str]] = {}
    for go_file in files:
        pkg = package_name(go_file)
        if pkg is None:
            continue
        key = (_directory_of(go_file.path), pkg)
        declared.setdefault(key, set()).update(top_level_declared_names(go_file))
        ginkgo_used.setdefault(key, set())
        gomega_used.setdefault(key, set())
        for raw in _raw_assertion_imports(go_file):
            if raw.local_name in (".", "_"):
                continue
            bucket = ginkgo_used if raw.path == _GINKGO_PATH else gomega_used
            bucket[key].update(_qualified_uses(go_file.code, raw.local_name))
    return {
        key: PackageFacts(
            redeclares_ginkgo=bool(names & ginkgo_used.get(key, set())),
            redeclares_gomega=bool(names & gomega_used.get(key, set())),
        )
        for key, names in declared.items()
    }


def assertion_imports(
    go_file: GoFile, facts_by_package: dict[tuple[str, str], PackageFacts]
) -> list[AssertionImport]:
    """Every ginkgo/v2 or gomega import in one test file, classified for CS-11.

    Empty for anything that is not a `_test.go` file: CS-11 is a convention
    about how a *test* reads, so production code that depends on gomega as a
    library (`pkg/eventually`) is out of scope and imports it qualified on
    purpose. `facts_by_package` is what `package_facts` built over the corpus;
    for a lone fixture it holds only that fixture's own package.
    """
    if not is_test_file(go_file.path):
        return []
    pkg = package_name(go_file)
    facts = (
        facts_by_package.get((_directory_of(go_file.path), pkg))
        if pkg is not None
        else None
    )
    found: list[AssertionImport] = []
    for raw in _raw_assertion_imports(go_file):
        redeclares = facts is not None and (
            facts.redeclares_ginkgo
            if raw.path == _GINKGO_PATH
            else facts.redeclares_gomega
        )
        exempt = (
            EXEMPT_REDECLARE
            if raw.local_name not in (".", "_") and redeclares
            else None
        )
        found.append(
            AssertionImport(
                offset=raw.offset,
                path=raw.path,
                local_name=raw.local_name,
                exempt=exempt,
            )
        )
    return found


class BareConstructor(NamedTuple):
    """One receiverless `func New(` declaration — CS-12's mechanical shape."""

    offset: int
    name: str


_BARE_CONSTRUCTOR_NAMES = frozenset({"New"})


def bare_constructors(go_file: GoFile) -> list[BareConstructor]:
    """Every top-level function named exactly `New` in one file.

    CS-12 says a constructor names what it builds; `New` names nothing. The
    detector reads one syntactic shape and nothing else: a receiverless
    declaration whose identifier is `New`. `NewStore`, `NewClient` and
    `MustNew` are the same defect and are deliberately not read here, because
    a lexer cannot tell `NewClient` in a package with one client from
    `NewStore` in a package with three; those are answered in review, and the
    under-reporting is recorded in the header with the other imprecisions.
    """
    return [
        BareConstructor(offset=head.keyword_offset, name=head.name)
        for head in function_heads(go_file.code)
        if head.receiver is None and head.name in _BARE_CONSTRUCTOR_NAMES
    ]


# --- CS-13: magic strings -------------------------------------------------


class MagicString(NamedTuple):
    """One string literal standing in a function body as a value."""

    offset: int
    text: str
    where: str


# A call whose FIRST argument is human-facing text rather than a value: the
# message an error carries, a panic's explanation, a format string and its `%`
# verbs. CS-13 counterweight (1). The callee is matched on the dotted text as
# written, so a package renamed at its import site is missed — the safe
# direction, and the same one every detector above takes.
_MESSAGE_FIRST_ARGUMENT = frozenset(
    {
        "panic",
        "errors.New",
        "fmt.Errorf",
        "fmt.Print",
        "fmt.Printf",
        "fmt.Println",
        "fmt.Sprint",
        "fmt.Sprintf",
        "fmt.Sprintln",
        "log.Fatal",
        "log.Fatalf",
        "log.Fatalln",
        "log.Panic",
        "log.Panicf",
        "log.Print",
        "log.Printf",
        "log.Println",
    }
)

# A method call whose arguments are a log or test record: `logger.Info(...)`,
# `slog.Warn(...)`, `t.Fatalf(...)`. EVERY argument is exempt, structured
# logging's key strings included, because a lexer cannot tell the key in
# `slog.String("status", "idle")` from its value and CS-13 errs toward
# under-reporting. A receiver is required, so a first-party `Info(...)` called
# unqualified in its own package is still read.
_MESSAGE_METHOD_NAMES = frozenset(
    {
        "Debug",
        "DebugContext",
        "Debugf",
        "Error",
        "ErrorContext",
        "Errorf",
        "Fatal",
        "Fatalf",
        "Info",
        "InfoContext",
        "Infof",
        "Log",
        "Logf",
        "Panic",
        "Panicf",
        "Print",
        "Printf",
        "Println",
        "Skip",
        "Skipf",
        "Warn",
        "WarnContext",
        "Warnf",
    }
)

# The `Fprint` family writes to a writer, so the message is the SECOND
# argument. Spelling that out is the difference between exempting a format
# string and exempting the writer beside it.
_MESSAGE_SECOND_ARGUMENT = frozenset(
    {
        "fmt.Fprint",
        "fmt.Fprintf",
        "fmt.Fprintln",
    }
)

# A composite-literal key whose value is human-facing text rather than a value
# the program dispatches on: `Finding{Class: ..., Message: "document declares no
# name"}`. CS-13 counterweight (1) reaches an error message wherever it is
# written, and a message assigned into a field is the one shape the
# message-family call test cannot see. Deliberately short: `Name`, `Title` and
# `Summary` are NOT here, because those are values often enough that exempting
# them would hide the defect the rule was minted for.
_MESSAGE_FIELD_KEYS = frozenset({"Message", "Msg", "Detail", "Description", "Reason"})

_MESSAGE_FIELD_ASSIGNMENT = re.compile(
    r"\b(?P<key>[A-Za-z_]\w*)\s*:\s*$"
)

_GO_KEYWORDS_BEFORE_PAREN = frozenset(
    {"if", "for", "switch", "return", "go", "defer", "case", "select", "func"}
)

# `""`, a one-character separator, and a one-escape separator (`"\n"`, `"\t"`).
# CS-13 counterweight (5): these carry no meaning a name could add.
_SEPARATOR_LITERAL = re.compile(r"^(?:.|\\.)?$", re.DOTALL)

# Prose. CS-13 counterweight (1) reaches human-facing text wherever it is
# written, and the message-family call list can only see the messages that
# travel through a call it knows. A value the program dispatches on or persists
# — a status name, an error code, a header name, a query parameter, an enum
# value, an environment variable, a flag — is identifier-shaped and has no
# space in it; a literal that does is a sentence. Under-reporting is the
# deliberate direction: a persisted value that genuinely contains a space
# ("United States") is missed, and the rule's founding evidence — "idle",
# "store_error", "session_not_found", "Last-Event-ID", "lastEventId" — is not.
_PROSE_SPACE = " "

_CONST_KEYWORD = re.compile(r"\bconst\b")
_CALLEE_TAIL = re.compile(r"[A-Za-z_][\w.]*$")


def string_literals(source: str) -> Iterator[tuple[int, int, str]]:
    """Every string literal in `source` as `(start, end, inner text)`.

    Comments and rune literals are walked over rather than yielded, so a `"` in
    a comment cannot open a literal and `'"'` cannot swallow the file. This is
    the second detector that reads raw source rather than the blanked `code`,
    and for CS-11's reason: the text inside the quotes *is* the subject of the
    rule, and the blanking erased it.
    """
    index = 0
    length = len(source)
    while index < length:
        char = source[index]
        if char == "/" and source.startswith("//", index):
            end = source.find("\n", index)
            index = length if end == -1 else end
        elif char == "/" and source.startswith("/*", index):
            end = source.find("*/", index + 2)
            index = length if end == -1 else end + 2
        elif char == "'":
            index = literal_end(source, index, quote="'")
        elif char in "\"`":
            end = literal_end(source, index, quote=char)
            closed = end - index >= 2 and source[end - 1] == char
            yield index, end, source[index + 1 : end - 1 if closed else end]
            index = end
        else:
            index += 1


def _function_bodies(code: str) -> list[tuple[int, int]]:
    """The `{...}` span of every top-level `func` declaration that has one."""
    spans: list[tuple[int, int]] = []
    for head in function_heads(code):
        body_open, body_close = _body_span(code, head.params_close)
        if body_open != -1:
            spans.append((body_open, body_close))
    return spans


def _const_spans(code: str, body: tuple[int, int]) -> list[tuple[int, int]]:
    """Spans inside one body covered by a `const` declaration or `const (...)` group.

    CS-13's counterweight (6) read from the inside: a literal that IS the
    declaration is not a use of one. Package-level declarations need no span —
    the rule's scope is a function body, so a package-level `const`, a
    package-level `var`, a struct tag, an import path, a `//go:generate` line
    and a registry table are all out by construction.
    """
    spans: list[tuple[int, int]] = []
    for match in _CONST_KEYWORD.finditer(code, body[0], body[1]):
        cursor = _skip_space(code, match.end())
        if cursor < len(code) and code[cursor] == "(":
            closing = matching_bracket(code, cursor)
            if closing != -1:
                spans.append((match.start(), closing + 1))
                continue
        line_end = code.find("\n", match.end())
        spans.append((match.start(), len(code) if line_end == -1 else line_end))
    return spans


def _enclosing_call(code: str, offset: int, floor: int) -> tuple[str | None, int]:
    """The callee of the call this offset sits in, and the argument's index.

    Walks back to the nearest unclosed bracket. A `(` yields the identifier
    chain written before it; a `{` or `[` — a composite literal, an index
    expression — has no callee, and neither does a keyword's parenthesis.
    `w.Header().Set(` reads as `Set`, because the walk back over `[A-Za-z_.]`
    stops at the `)`; that is enough for the message-family test, which asks
    for a receiver.
    """
    depth = 0
    argument = 0
    index = offset - 1
    while index >= floor:
        char = code[index]
        if char in ")]}":
            depth += 1
        elif char in "([{":
            if depth == 0:
                if char != "(":
                    return None, argument
                callee = _CALLEE_TAIL.search(code[max(floor, index - 120) : index])
                if callee is None:
                    return None, argument
                name = callee.group(0)
                if name.split(".")[-1] in _GO_KEYWORDS_BEFORE_PAREN:
                    return None, argument
                return name, argument
            depth -= 1
        elif char == "," and depth == 0:
            argument += 1
        index -= 1
    return None, argument


def _is_message_field(code: str, offset: int, floor: int) -> bool:
    """Whether this literal is the value of a `Message:`-shaped composite key."""
    match = _MESSAGE_FIELD_ASSIGNMENT.search(code[max(floor, offset - 60) : offset])
    return match is not None and match.group("key") in _MESSAGE_FIELD_KEYS


def _is_message_text(callee: str | None, argument: int) -> bool:
    """Whether this literal is human-facing text CS-13 leaves inline."""
    if callee is None:
        return False
    if argument == 0 and callee in _MESSAGE_FIRST_ARGUMENT:
        return True
    if argument == 1 and callee in _MESSAGE_SECOND_ARGUMENT:
        return True
    return "." in callee and callee.rsplit(".", 1)[1] in _MESSAGE_METHOD_NAMES


def magic_strings(go_file: GoFile) -> list[MagicString]:
    """Every string literal standing as a VALUE inside a function body.

    CS-13. Scope, exemptions and their reasons are in this module's header and
    in `../references/go-rules.md` § CS-13; the short form is: function bodies
    only, no `_test.go` file, no `const` initializer, no separator, and no
    human-facing message.
    """
    if is_test_file(go_file.path):
        return []
    bodies = _function_bodies(go_file.code)
    if not bodies:
        return []
    exempt = [span for body in bodies for span in _const_spans(go_file.code, body)]
    found: list[MagicString] = []
    for start, end, inner in string_literals(go_file.source):
        body = next((span for span in bodies if span[0] < start < span[1]), None)
        if body is None:
            continue
        if _SEPARATOR_LITERAL.match(inner) or _PROSE_SPACE in inner:
            continue
        if any(span[0] <= start < span[1] for span in exempt):
            continue
        if _is_message_field(go_file.code, start, body[0]):
            continue
        callee, argument = _enclosing_call(go_file.code, start, body[0])
        if _is_message_text(callee, argument):
            continue
        where = (
            f"argument {argument + 1} of {callee}(...)"
            if callee is not None
            else "an expression"
        )
        found.append(
            MagicString(offset=start, text=go_file.source[start:end], where=where)
        )
    return found


# --- CS-14: hand-rolled twins of a library's optional type ----------------


class NullTwin(NamedTuple):
    """One receiverless function converting a library optional to a plain shape."""

    offset: int
    name: str
    direction: str
    optional_type: str
    plain_type: str
    replacement: str


# Every optional type this rule reads, and the value it wraps. The value column
# is a *set* because the twin is usually written against the width the caller
# happens to hold — `func nullInt16(v int) sql.NullInt16` in
# `pkg/cron/postgres/store.go` converts an `int`, not an `int16`, and
# reading only the exact width would miss the founding offender.
_OPTIONAL_VALUES: dict[str, frozenset[str]] = {
    "sql.NullString": frozenset({"string"}),
    "sql.NullTime": frozenset({"time.Time"}),
    "sql.NullBool": frozenset({"bool"}),
    "sql.NullInt16": frozenset({"int", "int8", "int16"}),
    "sql.NullInt32": frozenset({"int", "int16", "int32"}),
    "sql.NullInt64": frozenset({"int", "int32", "int64"}),
    "sql.NullFloat64": frozenset({"float32", "float64"}),
    "uuid.NullUUID": frozenset({"uuid.UUID"}),
}

# The library form that replaces each twin, named in the finding so the reader
# does not have to go looking for what to write instead.
_LIBRARY_FORM = {
    "sql.NullString": "null.String — null.StringFrom(v), .Ptr(), .ValueOrZero()",
    "sql.NullTime": "null.Time — null.TimeFrom(v), .Ptr(), .ValueOrZero()",
    "sql.NullBool": "null.Bool — null.BoolFrom(v), .Ptr(), .ValueOrZero()",
    "sql.NullInt16": "null.Int16 — null.Int16From(v), .Ptr(), .ValueOrZero()",
    "sql.NullInt32": "null.Int32 — null.Int32From(v), .Ptr(), .ValueOrZero()",
    "sql.NullInt64": "null.Int64 — null.Int64From(v), .Ptr(), .ValueOrZero()",
    "sql.NullFloat64": "null.Float — null.FloatFrom(v), .Ptr(), .ValueOrZero()",
    "uuid.NullUUID": "a `*uuid.UUID` sqlc go_type override, read and written directly",
}

_POINTER_PREFIX = "*"

TWIN_READS = "reads"
TWIN_BUILDS = "builds"


def _dereferenced(type_text: str) -> str:
    """`type_text` with one leading `*` peeled: `*time.Time` is `time.Time`."""
    if type_text.startswith(_POINTER_PREFIX):
        return type_text[1:].strip()
    return type_text


def _optional_shape(type_text: str) -> str | None:
    """`type_text` if it is one of the library optional types CS-14 reads."""
    core = _dereferenced(type_text)
    return core if core in _OPTIONAL_VALUES else None


def _plain_shape(type_text: str, optional_type: str) -> str | None:
    """`type_text` if it is the plain or pointer shape `optional_type` wraps."""
    if _dereferenced(type_text) in _OPTIONAL_VALUES[optional_type]:
        return type_text
    return None


def _single_element(elements: list[str]) -> str | None:
    """The one type in a list of exactly one parameter or result, else None."""
    present = [element for element in elements if element.strip()]
    if len(present) != 1:
        return None
    return _element_type(present[0])


def _twin_of(parameter: str, result: str) -> tuple[str, str, str] | None:
    """`(direction, optional type, plain type)` if this pair is a conversion.

    Exactly one side is the optional. A signature whose parameter AND result
    are both optionals is a normalisation between two library types rather than
    the hand-rolled twin of one, and reads as neither direction.
    """
    from_parameter = _optional_shape(parameter)
    from_result = _optional_shape(result)
    if (from_parameter is None) == (from_result is None):
        return None
    if from_parameter is not None:
        plain = _plain_shape(result, from_parameter)
        return None if plain is None else (TWIN_READS, from_parameter, plain)
    plain = _plain_shape(parameter, from_result)
    return None if plain is None else (TWIN_BUILDS, from_result, plain)


def null_twins(go_file: GoFile) -> list[NullTwin]:
    """Every receiverless function that converts a library optional by hand.

    CS-14. The shape is deliberately narrow: one parameter in, one result out,
    one side a `sql.Null*`/`uuid.NullUUID` and the other the plain or pointer
    value it wraps. That is exactly the twin `guregu/null` already ships and an
    sqlc `go_type` override already emits, and it is exactly the shape that
    does NOT catch a store method carrying a `sql.NullTime` through to a query
    — carrying an optional is not converting one.
    """
    if is_test_file(go_file.path):
        return []
    found: list[NullTwin] = []
    for head in function_heads(go_file.code):
        if head.receiver is not None:
            continue
        parameter = _single_element(
            split_top_level(go_file.code[head.params_open + 1 : head.params_close])
        )
        result = _single_element(result_elements(go_file.code, head.params_close))
        if parameter is None or result is None:
            continue
        twin = _twin_of(parameter, result)
        if twin is None:
            continue
        direction, optional_type, plain_type = twin
        found.append(
            NullTwin(
                offset=head.keyword_offset,
                name=head.name,
                direction=direction,
                optional_type=optional_type,
                plain_type=plain_type,
                replacement=_LIBRARY_FORM[optional_type],
            )
        )
    return found


# --- CS-18: tests make no real crossings ----------------------------------
#
# The legacy lane of the native `CS-18-CROSSING` locator
# (`tools/house_lint/test_layout.ml`), kept so the tier-1 eval corpus and the
# census measure the rule the native report locates. The tables below restate
# the native ones; a change to either is a change to both. Import specs are
# read line by line from the raw source, so a spec-shaped line inside a raw
# string literal would be read as an import: an over-report, never a miss.


class TestCrossing(NamedTuple):
    """One real crossing (socket, subprocess, database pool, container) in a test."""

    offset: int
    what: str


_CROSSING_CALLS = {
    "net": frozenset({
        "Listen", "ListenPacket", "ListenTCP", "ListenUDP", "ListenUnix",
        "ListenUnixgram", "ListenIP", "ListenMulticastUDP", "Dial", "DialTimeout",
        "DialTCP", "DialUDP", "DialUnix", "DialIP",
    }),
    "net/http": frozenset({"ListenAndServe", "ListenAndServeTLS", "Serve", "ServeTLS"}),
    "net/http/httptest": frozenset({"NewServer", "NewTLSServer", "NewUnstartedServer"}),
    "os/exec": frozenset({"Command", "CommandContext"}),
    "github.com/jackc/pgx/v5/pgxpool": frozenset({"New", "NewWithConfig"}),
    "github.com/jackc/pgx/v5": frozenset({"Connect", "ConnectConfig"}),
}
_CROSSING_TYPES = {"net": frozenset({"Dialer", "ListenConfig"})}
_NETWORK_DRIVERS = frozenset({"pgx", "postgres", "pgx/v5"})
_CONTAINER_MODULE = "github.com/testcontainers/testcontainers-go"

_IMPORT_SPEC = re.compile(
    r"(?m)^[ \t]*(?:import[ \t]+)?(?:(?P<name>\.|_|[A-Za-z]\w*)[ \t]+)?"
    r'"(?P<path>[^"\n]+)"[ \t]*(?://[^\n]*)?$'
)
_BUILD_CONSTRAINT = re.compile(r"(?m)^//go:build[ \t]+(?P<expression>[^\n]*)$")
_MAJOR_VERSION = re.compile(r"v\d+")


def _import_binding(path: str) -> str:
    """The name an unaliased import binds: the last element, skipping /vN."""
    parts = path.split("/")
    if len(parts) > 1 and _MAJOR_VERSION.fullmatch(parts[-1]):
        return parts[-2]
    return parts[-1]


def acceptance_suite(go_file: GoFile) -> bool:
    """Whether the file is the labelled opt-in acceptance suite.

    The label is a `//go:build` constraint above the package clause in which
    `acceptance` appears as a positive tag; `!acceptance` is not the label.
    """
    clause = _PACKAGE_CLAUSE.search(go_file.source)
    header = go_file.source[: clause.start()] if clause else go_file.source
    return any(
        "acceptance" in re.split(r"[\s()&|]+", match.group("expression"))
        for match in _BUILD_CONSTRAINT.finditer(header)
    )


def _driver_crossing(go_file: GoFile, after: int) -> str | None:
    driver = re.match(r'\s*"([^"\n]*)"', go_file.source[after:])
    if driver and driver.group(1) in _NETWORK_DRIVERS:
        return f"database/sql.Open with the {driver.group(1)} driver"
    return None


def test_crossings(go_file: GoFile) -> list[TestCrossing]:
    """Every real crossing a test file makes, in source order.

    CS-18 says tests make no real crossings: dependencies are gomock mocks of
    exported interfaces and the database substrate is pgmem. A call is resolved
    through the file's own imports, so an alias is followed and a different
    package that happens to be named `net` is not reported. The labelled
    acceptance suite is out of scope by its build constraint, not by a list.
    """
    if not is_test_file(go_file.path) or acceptance_suite(go_file):
        return []
    clause = _PACKAGE_CLAUSE.search(go_file.source)
    if clause is None:
        return []
    found: list[TestCrossing] = []
    bindings: dict[str, str] = {}
    for match in _IMPORT_SPEC.finditer(go_file.source, clause.end()):
        path = match.group("path")
        bindings[match.group("name") or _import_binding(path)] = path
        if path == _CONTAINER_MODULE or path.startswith(_CONTAINER_MODULE + "/"):
            found.append(TestCrossing(offset=match.start("path"), what=path))
    for name, path in bindings.items():
        for use in re.finditer(rf"(?<![\w.]){re.escape(name)}\.([A-Z]\w*)\s*([({{])", go_file.code):
            member, opener = use.group(1), use.group(2)
            what: str | None = None
            if opener == "(" and member in _CROSSING_CALLS.get(path, ()):
                what = f"{path}.{member}"
            elif opener == "{" and member in _CROSSING_TYPES.get(path, ()):
                what = f"{path}.{member} literal"
            elif opener == "(" and path == "database/sql" and member == "Open":
                what = _driver_crossing(go_file, use.end())
            if what is not None:
                found.append(TestCrossing(offset=use.start(), what=what))
    found.sort(key=lambda crossing: crossing.offset)
    return found
