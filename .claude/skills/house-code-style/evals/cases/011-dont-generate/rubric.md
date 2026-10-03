# Rubric — case 011, don't build that generator yet

Tier 2, **negative**. The correct answer is no. Scoring procedure and row
format are in `../../README.md`.

**Why this case exists.** CS-4 says *check whether a generator already owns
this output before hand-writing it*. Read carelessly — and the originating
prompt (`../000-originating-prompt/prompt.md`) leans hard in this direction —
it becomes "generate everything", which is how a repo acquires a bespoke
compiler with one consumer, no regeneration check, and a schema that moved
again on Thursday. This case is the counterweight; without it the corpus scores
"write the compiler" as a win.

---

## R11-1 — The verdict is no, for now (required)

The top-line answer is: hand-write the config struct today. Do not build the
compiler this week.

**Fails if** the response starts designing the generator, or agrees with a
caveat ("let's build a minimal version first"), or splits the difference by
writing a partial generator. Everything below is scored only if this passes.

## R11-2 — The reason is schema instability plus a single consumer

A generator amortizes a *stable* schema across *many* consumers. This schema
has one consumer and is changing daily, which inverts both terms: every change
costs a spec edit, a generator edit, and a regeneration, to produce one struct
a person could have edited in a minute.

**Fails if** the reason is "generators are complicated" or effort-estimate
skepticism ("it's more than a day") without naming stability and consumer
count as the actual criteria.

## R11-3 — The full cost of a generator is named, including its gate

CS-4 requires a deterministic regeneration check with every generator, and the
generated output is then a projection nobody may hand-edit. So the real
deliverable is not "a compiler": it is a schema, a generator, a regeneration
check wired into CI, and a rule that the emitted file is now read-only to
humans.

**Fails if** the response prices only the compiler, or forgets that the
regeneration check ships with it.

## R11-4 — Existing owners are checked before a new one is proposed

CS-4's first move is to ask whether something already owns this output. A
passing response checks: `candace/pkg/liquidproto` owns refinement-typed
contracts, the sqlc configs own database access, the proto chains own bindings.
If one of those already covers config projection, the answer is to use it, not
to write a second generator beside it.

**Fails if** the response proposes a new generator without ever asking what
already exists — which is the same mistake as hand-writing something a
generator already owns, made in the other direction.

## R11-5 — It says what would flip the answer

Name the trigger: the schema stops moving *and* there are several consumers —
at which point the ontology work at slice P1 of `docs/widget_foundry.md` is the
place this belongs, rather than a one-off compiler for config structs. Until
then, the hand-written struct **is** the cheaper artifact.

**Fails if** the refusal has no re-entry condition, or if it ignores that the
program already plans a generator for a neighbouring surface and lets the two
efforts collide later.

## R11-6 — "ast bashing is cheap" is answered, not ignored

The operator's premise is half right and the response should say which half:
writing the transform is genuinely cheap now; *owning* it is not. The recurring
cost is the schema contract, the regeneration check, the drift failures at
`go generate` time, and the reviewers who now read generated diffs.

**Fails if** the response either accepts the premise wholesale or dismisses it
without engaging — the operator is right that the cost curve moved, and a
refusal that pretends otherwise will be ignored, correctly.

---

## Judge's note

This case and case 010 fail the same way: a response that pattern-matches the
rule's headline and skips its conditions. Watch also for the reverse
over-correction — a blanket "we don't do code generation here" contradicts CS-4
and the five generator chains this repo already runs, and should fail R11-4 and
R11-5 even though its verdict happens to be "no".
