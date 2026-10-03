# Rubric — case 023, one shape, two answers

Tier 2, **split verdict**. Neither "make them both registries" nor "leave them
both alone" is the answer. Scoring procedure and row format are in
`../../README.md`.

**Why this case exists.** CS-6 says *prefer composable function values in a
registry over method-set accumulation*, which is trivially gamed in one
direction — answer "register them" to everything and score full marks on any
corpus of positive cases, then go convert a three-step ordered pipeline into a
slice that hides its own ordering constraint. The corpus is gameable from the
other side too: with `010-dont-extract`, `011-dont-generate` and the "keep the
lock" half of `021-csp-judgment` already in it, a model that answers *don't* to
every judged prompt scores well without reading a rule. So this case is
constructed so that **both blanket answers fail R23-1**.

The prompt deliberately gives both halves the same *syntax* — a method whose
body is nothing but sibling calls — and different *substance*. CS-6's content
is that distinction, not the syntax, and a response that cannot make it has not
understood the rule regardless of which verdict it landed on.

---

## R23-1 — The verdict is split, and it is the top-line answer (required)

The response separates the two and gives a different answer to each:

- **`build()` — keep the method chain** (or flatten it to plain statements).
  Three steps with real ordering dependencies that will never grow. CS-6's own
  text calls this shape honest.
- **`checkAll()` — this is the one CS-6 is about.** Eleven same-signature
  behaviors, growing (three added last month), interchangeable in order, each
  reaching through a shared mutable receiver for input and output. That is a
  family, and a family belongs in a registry.

**Fails if** the response gives one verdict for both in either direction —
including "register both, consistency matters" and "both are fine, the gate is
report-only". Everything below is scored only if this passes.

## R23-2 — The criterion is family-versus-sequence, not the number of calls

Both methods are dispatch lists by syntax; three calls versus eleven is the
most available difference and the wrong one. A response that splits on size has
adopted a threshold, and a threshold licences converting the builder the moment
a fourth step appears, and leaving a five-check family alone because five is
small.

The distinction that must be named: a **family** grows and its members are
interchangeable — order carries no meaning, so the list is data. A **sequence**
has an order that *is* the logic, so the list is control flow.

**Fails if** the split is justified only by how many calls each method makes,
or only by "eleven is a lot to read". Naming size as corroborating evidence is
fine; resting the verdict on it is not.

## R23-3 — The reason for keeping the chain is what a registry would destroy

`parse → resolve → emit` has its ordering dependency encoded as line order,
where it is visible for free. Put those three in a slice and the constraint
becomes a convention about the slice's element order — unenforced, undocumented
at the call site, and newly breakable by anyone who reorders the registrations
or runs them concurrently. The registry also buys nothing in exchange, because
its value is inspection and there is nothing worth inspecting about three fixed
steps.

**Fails if** the "keep it" half is justified only by brevity or by "if it ain't
broke", with no account of what converting it would cost.

## R23-4 — The diagnostics rewrite is described concretely enough to be wrong

"Make it a registry" is not a design. A passing response says:

- **the function type**, e.g. `type Check func(document *Document) []Finding`;
- **what a registry entry carries** — at minimum the check and the diagnostic
  classes it owns, which is the metadata a method had nowhere to put;
- **that the checks become pure** — document in, findings out — instead of
  reading the document off the receiver and appending to `reporter.findings`;
  the registry loop does the appending, and that is what removes the coupling
  which forced them to be methods.

**Fails if** the recommendation stops at "put them in a slice and range over
it" with no account of the signature, the metadata, or the shared receiver.

## R23-5 — The reported symptom is connected to the completeness assertion

The prompt contains one actual bug, stated twice: a check gets written, never
wired into `checkAll`, and nobody finds out until a bug report. That is not a
discipline problem, it is the direct consequence of a method set being
unenumerable — **no test can iterate a method set**, so no test could have
caught it.

A passing response names the fix in those terms: once the family is data, a
spec iterates the registry and asserts every declared diagnostic class has
exactly one owner, no two checks claim the same class, and the count matches
the contract. This is the rule's payoff and the reason this half is a
conversion rather than a preference.

**Fails if** the response converts the diagnostics half without connecting it
to the missed-wiring bug, or proposes to fix that bug with a code-review
checklist, a comment, or "remember to add it".

## R23-6 — CS-2 is applied to the function type it just introduced

Converting a method set multiplies the func types in the tree, and each is a
signature CS-2 governs. `type Check func(*Document) []Finding` is a CS-2
violation wearing CS-6 compliance.

**Fails if** the response introduces any function type or function-typed field
with an unnamed parameter. This is scored on the code the response actually
writes, not on whether it mentions CS-2.

## R23-7 — The gate finding is treated as a locator, not a defect

`check_style.py` reports both methods and will keep reporting `build()`
forever, because the heuristic cannot tell a family from a sequence. A passing
response says so plainly and does not propose to make the finding go away.

**Fails if** the response treats the surviving finding as something to clear,
or proposes silencing it — an exclusion list, a narrowed heuristic, a marker
comment, a threshold. It also fails if it converts `build()` *in order to* get
a clean report, which is the gate driving the design instead of the other way
around.

## R23-8 — Registry and functional options are not confused

The operator's founding words for CS-6 were "why couldn't this be functional
options pattern", and a response that has pattern-matched on that phrase will
reach for `WithCheck(...)` constructor options for the diagnostics family. They
are the same principle at different times: **functional options configure one
constructed thing at construction time; a registry is a family of behaviors the
code iterates.** The diagnostics problem is the second. A response may note that
options would be the right shape if callers needed to assemble their own
reporter, but it must not substitute one for the other.

**Fails if** the diagnostics answer is delivered as constructor options with no
registry the code can enumerate — since options accumulated into a struct are
just as unenumerable as the method set was, and would not have caught the bug.

## R23-9 — It says what would flip the builder answer

Name the trigger: a fourth step that is conditional or skippable, steps that
callers need to select or reorder, a caller that needs to ask "what stages are
there", or plugins contributing stages. Any of those makes it a family and CS-6
bites there too. A verdict with no re-entry condition is a preference, not a
rule.

**Fails if** the "keep it" half is unconditional.

---

## Judge's note

Three plausible wrong answers, and the case is built to catch all three.

The first is fluent CS-6 maximalism: *"Per CS-6, model both as registries of
function values."* It cites the rule, it is internally consistent, and it
converts a three-step ordered pipeline into a slice whose element order is now
load-bearing and unchecked.

The second is the corpus-shaped answer. A model that has seen `010`, `011` and
the "keep the lock" half of `021` learns that judged cases reward restraint, and
answers *"both of these are fine, CS-6 has counterweights, the gate is
report-only"* — right about the builder, wrong about the diagnostics, and
arrived at without reading either.

The third is subtler and worth watching for: a **correct split reached by call
count**. "Three is small, eleven is a lot" gets R23-1 right and R23-2 wrong,
and it is the answer most likely to look good on a skim. Grade R23-2
independently of R23-1 — a response can pass the verdict and fail the reasoning,
and the reasoning is what transfers to the next diff.
