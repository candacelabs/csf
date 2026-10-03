# Rubric — 033 keep-qualified judgment

Every criterion is required. The case passes only if all of them pass.

The verdicts are **keep File 1 qualified, convert File 2** — and the case is not
passed by getting that pair right. The point is *why* File 1 stays: not because
its qualifier reads nicely, but because CS-11 governs test files and File 1 is
production code that depends on gomega as a library.

---

## R33-1 — File 2 converts to a dot import

The answer dot-imports gomega in `run_test.go` and drops the `gomega.` qualifiers
(`gomega.RegisterFailHandler` → `RegisterFailHandler`, `gomega.Expect` →
`Expect`, `gomega.Equal` → `Equal`), so the suite reads unqualified the way the
convention wants. File 2 is a `_test.go` file, its package declares nothing
gomega exports, and there is no reason for the qualifier.

**Fails if** the answer leaves File 2's gomega qualified, or claims the rule
does not apply to it.

## R33-2 — File 1 keeps its qualified import

The answer does **not** dot-import gomega in `eventually.go`. It stays
`"github.com/onsi/gomega"`, qualified.

**Fails if** the answer dot-imports gomega in File 1, drops its `gomega.`
qualifiers, or calls the proposed change correct as written. This is the
"dot-import everywhere" failure the case is built against.

## R33-3 — the reason is scope, not taste

The answer keeps File 1 qualified for the right reason: **CS-11 is a convention
about how a *test* reads, and `eventually.go` is production code** — a library
that wraps gomega as its engine. Dot-importing an assertion vocabulary
(`Expect`, `Eventually`, every matcher) into a non-test package pollutes that
package's namespace, which is the opposite of what the rule is for. A reader
should be able to point at the file name and the package role, not at a
readability preference.

**Fails if** the reason given is only aesthetic ("the qualifier reads better",
"it's clearer to see where gomega calls are") without naming that File 1 is
outside CS-11's scope because it is not a test. An answer that keeps File 1 for
a reason that would apply equally to File 2 has not understood the split.

## R33-4 — nothing is silenced, and the rule is not weakened

The answer does not propose exempting File 1 with a marker comment, an exclusion
list, or a change to the gate that makes it skip production files by some
fragile signal. The gate already scopes itself to `_test.go` files by
construction; File 1 is simply not in scope, and saying so is the whole answer.

**Fails if** the answer reaches for a `//nolint`-style marker, an allow-list, or
proposes teaching the scanner a new exception it does not need.

---

## Judge's note — the plausible wrong answer

Two fluent failures.

The first is **"apply the convention uniformly"**: the proposed change is
internally consistent — dot-import both packages everywhere — and a model that
has read the headline of CS-11 and nothing else approves it, dot-importing
File 1 along with File 2. That is the failure R33-2 exists for, and it is the
one the case is named against.

The second is subtler and is the one the escalating judged corpus has trained a
model toward: **split correctly, justify by taste.** By now `021`, `023`, `025`,
`027` and `029` all reward a split verdict justified by substance, so a model
will happily keep File 1 and convert File 2 — and then defend keeping File 1
with *"the explicit `gomega.` prefix makes the calls easier to follow."* That
sentence is a readability claim, and it is true of File 2 as much as File 1, so
it cannot be the reason one converts and the other does not. R33-3 fails it. The
real discriminator is not present in any earlier case: it is **scope** — the
rule is about test files, and one of these two is not a test.

There is also a fact a strong answer may notice but is not required to: because
`eventually` declares `Consistently` itself and gomega also exports `Consistently`,
a dot import of gomega into `eventually.go` would not even compile — it would
redeclare the name. That is a real second reason File 1 keeps its qualifier, and
an answer that cites it is correct, but the primary reason graded here is scope:
File 1 is production code, and CS-11 is a test convention.
