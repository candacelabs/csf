# Rubric — 029 pacing judgment

Every criterion is required. The case passes only if all of them pass.

The verdicts are **keep, convert, keep** — and the case is not passed by
getting that pattern right. Two of the three keeps rest on different reasons,
and a response that gives one reason for both has answered a two-way question
with a three-way shape.

---

## R29-1 — Finding 1 stays, because the sleep *is* the rate

The answer keeps the flood's sleep and says that it is not a wait at all: it is
what makes the load generator generate load at a chosen rate, and the rate is
the independent variable the assertion below it is stated against
(`sent/10 < 15000`). Replacing it with a poll does not slow the sender down; it
deletes the experiment and leaves an assertion about a number nothing controls.

**Fails if** the answer converts it, "modernizes" it into
`eventually.Await`/`Eventually`/`Consistently`, replaces it with a ticker as
though the shape were the problem, or keeps it with a reason that would apply
to any sleep ("sleeps are sometimes fine", "this one is short").

## R29-2 — Finding 2 converts, and to a poll of the close **code**, not the bool

The answer replaces `awaitEvicted` with the repository's typed await. It should
notice that `wire` offers both `isClosed() bool` and `code()`, and poll the
code: a poll that returns a bool fails with `false`, while a poll that returns
the close code fails with the code the peer actually sent — which is the
difference the typed shell exists for, and the reason CS-9's answer is not
"call `Eventually`".

**Fails if** the answer keeps the hand-rolled loop; if it keeps a private
`awaitEvicted` wrapper that still owns a deadline and a sleep; or if it
converts but says nothing about which of the two available polls it chose. An
answer that polls `isClosed` **and gives a reason** for preferring it does not
fail this criterion — it fails only on silence, because the point is that the
choice was seen.

## R29-3 — Finding 3 stays, and *not* because it is pacing

The answer keeps `settledGoroutines` and gives the mechanical reason: it is
polled by an `Eventually` in one of its own call sites, so it must **return**
rather than fail. `eventually.Await` fails the test when its predicate never
matches, and a fatal failure inside a poll aborts the retry that is doing the
actual waiting — so converting this one would break the spec that depends on it
even though the code would compile and read better.

The second half of the reason is that it asserts nothing. A helper that cannot
fail is not an assertion, so there is no condition here for an await to own; it
is a best-effort quiesce feeding a measurement.

**Fails if** finding 3 is kept with finding 1's reason — "it paces", "the sleep
is the subject", "it is generating load" — which is the fluent wrong answer and
is false: nothing here is generating anything, and the sleep is not what the
test is about. It also fails if the answer converts it, whether or not it also
proposes rewriting the outer `Eventually` to accommodate the conversion.

## R29-4 — the budget in the converted site is named

The one conversion introduces a wall clock, and the answer gives it a name in
the suite rather than writing `20*time.Second` inline in the call — and says
why the number can be raised freely: too large costs a slow failure on a test
that was going to fail, too small costs a red build on a correct one, and the
two are not the same price.

**Fails if** the converted call carries an inline duration with no name, or if
the answer shortens the budget "to keep the suite fast".

## R29-5 — nothing is silenced

No answer proposes an exclusion list, a narrowed heuristic, a `//nolint`-style
marker, or a rule change that makes findings 1 and 3 stop being reported. The
correct response to a CS-9 finding that is correct code is a comment at the
site naming which kind of sleep it is.

**Fails if** the answer suggests teaching the scanner to skip these shapes, or
proposes moving the two kept sleeps out of a `for` purely to dodge the
heuristic.

---

## Judge's note — the plausible wrong answer

The fluent failure is **a clean two-versus-one split with one reason for the
two.**

By this point the judged corpus has taught a strong pattern: `021`, `023`,
`025` and `027` all reward a split verdict justified by substance rather than
by shape. A model that has read those and nothing else will split this case
correctly — convert 2, keep 1 and 3 — and justify the keeps together: *"both
are sleeps that serve the test rather than wait for it."* That sentence is true
of finding 1 and **false of finding 3**, where the sleep serves nothing and the
reason for keeping it is that the function is polled by something else and
must not be able to fail.

So the verdict pattern is deliberately not the discriminator. R29-1 and R29-3
are graded on their reasons, and an answer can produce the right diff on all
three findings and still fail, because the third keep was reached by
association. That is the shape of the actual mistake this rule was written
against: the plan for CS-9 said "convert the sleep loops", and reading them
found that a *majority* were not waits — three of them for a reason nobody had
predicted, which only became visible by asking who calls this.

Two other failures to watch for, both older strategies resurfacing:

- **Blanket convert.** "All three are CS-9 findings, put them all on the
  primitive." This is what the rule's headline sentence produces without its
  counterweights, and it breaks two specifications.
- **Blanket keep.** "Sleeps in tests are usually deliberate; leave them and
  add comments." The corpus has rewarded *don't* twice (`010`, `011`), which
  is exactly why every judged case since has been built so that a blanket
  answer cannot win. It fails R29-2.
