# Rubric — case 025, one finding, two answers, three verdicts

Tier 2, **split verdict with a third answer**. Neither "make them both generic"
nor "leave them both alone" is correct, and on the sink half neither of those
two verdicts is correct *either* — the answer there is a third thing. Scoring
procedure and row format are in `../../README.md`.

**Why this case exists.** CS-7 says *a public contract takes a type parameter,
not `any`*, which is gameable in the obvious direction: answer "add a type
parameter" to everything and score full marks on any corpus of positive cases,
then go write `Envelope[T any]` for a decoder that cannot know `T`. The corpus
is gameable from the other side too — `010-dont-extract`, `011-dont-generate`
and the restraint halves of `021` and `023` mean a model that answers *don't*
scores without reading a rule.

**And it is now gameable from a third side, which is what this case is built
against.** With `021` and `023` both in the corpus, "give a split verdict and
justify it by substance rather than syntax" has itself become the extractable
pattern — a model can produce a well-shaped split without reading CS-7 at all.
So the sink half is constructed so that a *binary* split is still wrong: the
available verdicts are "generify it" and "leave it", and the correct answer is
neither. The erasure is real, it is forced by the language, and it moves.
A response that has only learned the shape of the previous judged cases lands
on "the sink should be generic" and stops, which fails R25-1 and R25-2 both.

---

## R25-1 — The verdict is split, and the sink half is *relocate*, not *eliminate* (required)

The response separates the two and gives a different answer to each:

- **`ISink` — relocate the erasure.** `ISink[E any]` typed in the sink's own
  payload, an unexported untyped view the registry stores, an unexported
  adapter holding the single assertion, and a generic shell
  `Register[E any](registry *Registry, sink ISink[E]) error` as the only door
  in. The registry still holds heterogeneous sinks; the erasure still exists;
  it now exists **once**, in a package-internal adapter no author implements.
- **`Envelope.Payload` — keep the `any`.** The shape is genuinely unknown, the
  code never reads a field off it, and there is no type for a parameter to
  carry.

**Fails if** the response gives one verdict for both in either direction —
including "generify both for consistency" and "both are fine, the gate is
report-only". **It also fails if the sink half is answered as a plain
"make it generic" with no relocated erasure site**, because that answer is
either incomplete or, if taken literally, impossible: see R25-2. Everything
below is scored only if this passes.

## R25-2 — The language constraint is stated correctly, and not wished away

Go has no existential types. `[]ISink[E]` for a different `E` per element
cannot be spelled, so a heterogeneous registry **must** erase somewhere. The
widget SDK's own comment makes this argument and it is correct; CS-7's content
is that the erasure belongs at one unexported adapter, not in the exported
contract.

Three specific errors fail this criterion:

- claiming or implying that generics let the registry hold typed sinks with no
  erasure at all;
- proposing `[]ISink[any]` as the typed registry — a real instantiation that
  does not unify with `ISink[PageEvent]`, since Go generics are not variant, so
  this is the same erasure with more syntax;
- silently changing the problem to a homogeneous registry (`Registry[E any]`
  holding one payload type), which abandons a requirement the prompt states
  rather than satisfying it.

**Fails if** any of those appear, or if the response asserts the erasure can be
removed without saying where it went.

## R25-3 — The erasure lands at exactly one named site, and generated code stops asserting

"Move the erasure" is not a design. A passing response names:

- **the typed contract**, e.g. `type ISink[E any] interface { Deliver(payload E) error ... }`;
- **the untyped view the registry stores**, unexported, which no sink author
  implements;
- **the adapter**, e.g. `erasedSink[E]`, unexported, holding *the* assertion —
  and says why that assertion is total rather than defensive: the only value
  that can reach it is one this same adapter produced from the same `E`.

The prompt states that half the sinks are generated and that **the generator
emits the assertion too**. The fix is therefore a change to the generator
(CS-4: the finding in generated output is a finding about its generator), after
which the emitted per-instance code contains no assertion at all.

**Fails if** the recommendation stops at "add a type parameter to ISink" with
no adapter and no single erasure site, or if it never mentions that the
generated sinks are regenerated and stop asserting.

## R25-4 — The envelope is kept for the unknown shape, not for convenience

The `any` stays because the shape is genuinely unknown: a third party owns the
schema, it changes without notice, and nothing in the code reads a field off
the value. That is CS-7's second counterweight, and the rule requires the
reason to be **written in a comment** — an `any` with a reason is a decision,
an `any` without one is a default.

**Fails if** the envelope half is justified by churn, by risk, by "if it ain't
broke", or by a category error such as "CS-7 only governs interfaces, not
struct fields" (it governs both). It also fails if the response keeps the `any`
and never says a comment belongs there.

## R25-5 — `Envelope[T any]` is rejected, specifically

The fluent wrong answer to the second half is a type parameter: `Envelope[T
any]` with `Payload T`. It reads as CS-7 compliance and it cannot work — the
decoder is handed bytes from an unknown sender and has nothing to instantiate
`T` with, so the type parameter would be chosen at a call site that does not
know the answer, and every caller would write `Envelope[any]`.

**Fails if** the response proposes a type-parameterized envelope, or accepts
one as a reasonable alternative without naming why the decode site cannot
supply the type.

## R25-6 — CS-2 is applied to every generic signature it writes

Adding type parameters rewrites signatures wholesale.
`Register[E any](*Registry, ISink[E]) error` is a CS-2 violation wearing CS-7
compliance; it is
`Register[E any](registry *Registry, sink ISink[E]) error`. The same applies
inside `ISink[E]`'s own methods and the adapter's.

**Fails if** the response introduces any signature, function type, or interface
method with an unnamed parameter. Scored on the code the response actually
writes, not on whether it mentions CS-2.

## R25-7 — The surviving gate finding is treated as a locator, not a defect

`check_style.py` will keep reporting `Envelope.Payload` forever, because no
lexer can tell a JSON envelope from an erased contract. A passing response says
so plainly and does not propose to make the finding go away.

**Fails if** the response proposes silencing it — an exclusion list, a narrowed
heuristic, a marker comment, a threshold — or treats the surviving finding as
something to clear. It also fails if it converts the envelope *in order to* get
a clean report, which is the gate driving the design instead of the reverse.

## R25-8 — CS-7 is not over-applied to the internals

Counterweight 1: do not generify internals no author touches, and a type
parameter that only ever binds one type is ceremony. The registry's own storage
— the slice of untyped sinks, the parallel state — is exactly the internal that
should stay untyped, and that is not a compromise but the design.

**Fails if** the response adds type parameters to unexported helpers for
consistency, or proposes a type parameter that every call site instantiates
with the same type.

## R25-9 — It says what would flip the envelope answer

Name the trigger: the schema becoming ours, subscribers needing typed access to
fields, a finite known set of sources making a discriminated union possible, or
any code starting to read a field off the payload. The moment something in our
code needs to know what is in there, the shape is no longer unknown and CS-7
bites. A verdict with no re-entry condition is a preference, not a rule.

**Fails if** the "keep it" half is unconditional.

---

## Judge's note

Four plausible wrong answers, and the case is built to catch all four.

The first is fluent CS-7 maximalism: *"Per CS-7, parameterize both."* It cites
the rule and produces `Envelope[T any]`, a type parameter no decode site can
instantiate, which every caller will write as `Envelope[any]` — the original
erasure with extra ceremony and a worse name.

The second is the corpus-shaped answer. A model that has read `010`, `011` and
the restraint halves of `021` and `023` has learned that judged cases reward
saying no, and answers *"both of these are fine, CS-7 has counterweights"* —
right about the envelope, wrong about the sink, and arrived at without reading
either.

The third is the **shape-matched split**, and it is the one this case was built
for. `021` and `023` have taught the corpus that the winning move is a split
justified by substance. A model can now produce *"generify the sink, keep the
envelope"* — a correct-looking split, delivered fluently, that never notices
the sink half is impossible as stated. R25-1 and R25-2 both fail it, and they
fail it for the reason that matters: the response does not know where the
erasure went, which is the entire content of the rule.

The fourth is subtler and worth watching for: a response that **relocates the
erasure but forgets the generator**. The prompt says the assertion is emitted
into generated code, so a fix applied only to the handwritten sinks leaves the
unchecked cast in exactly the code nobody reads. Grade R25-3 independently of
R25-1 — a response can get the design right and leave most of the instances of
the defect in the tree.
