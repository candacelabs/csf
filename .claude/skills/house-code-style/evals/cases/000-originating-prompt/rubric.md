# Rubric — case 000, the originating prompt

Tier 2. Judged by the operator or an LLM judge against the criteria below;
scoring procedure and row format are in `../../README.md`.

**What is being judged.** The response an orchestrating agent gives to
`prompt.md` *before any code is written* — the questions it asks, the goal it
proposes, and the shape of the program it lays out. Not the code that later
came out of it.

**All ten criteria are required.** The case passes only if every one passes; a
row records which ones failed. Nine of ten is a fail with a written reason,
because the failures this case exists to catch are individually fatal: an agent
that skips R0-2 has already spawned a team against a goal nobody agreed to.

---

## R0-1 — Look the unfamiliar term up before answering

The prompt says "ontological thinking … you should look up and figure out what
that measn". A passing response actually researches the term and reports what
it found, including that the phrase is used loosely, and then says what it will
mean *here*: designing the vocabulary — the set of primitives, their names and
their relations — before generating anything from it.

**Fails if:** the term is silently dropped; or it is defined confidently from
memory with no indication that anything was checked; or the answer is an
etymology lecture that never lands on what changes in this repo.

## R0-2 — Propose a goal and stop; deploy nothing until sign-off

The prompt is explicit: "don't deploy agents until i sign off on the goal and
THEN name the branch and worktree". A passing response ends its first turn with
a proposed end state and open questions, having launched no subagent, created
no branch, and created no worktree.

**Fails if:** any agent is dispatched, or a branch or worktree is named, before
the operator answers. This is the single most-likely failure and it is not
recoverable by good work afterwards.

## R0-3 — Ask few, high-leverage, decidable questions

The operator asked for "whatever questions you need". A passing response asks a
small number of questions that are genuinely load-bearing — each with the
options spelled out and a recommendation attached — so answering is a decision,
not an essay.

**Fails if:** the response asks an open-ended survey ("what do you want the
components to look like?"), or asks nothing and assumes, or buries a real fork
in the road inside an assumption.

## R0-4 — Turn the preferences into an enforceable skill, not a style guide

"a skill for all my super particular preferences" has to come out as numbered
rules, each naming an enforcement point, plus a script that measures compliance
and a way to get the rules in front of a subagent that reads only its brief.

**Fails if:** the deliverable is prose — a document restating the preferences
with nothing that checks them. The repo already ran that experiment: the named
-parameter rule sat in `go/CLAUDE.md` unchecked and compliance was 0 of 15
(`../../../references/lessons.md`).

## R0-5 — Measure before claiming

A passing response takes a baseline: how many interfaces exist, how many carry
the prefix, how many signatures have unnamed parameters. Numbers, at a named
commit, before any of the work starts.

**Fails if:** it asserts the current state from impression, or proposes metrics
that are only collectible after the fact, or claims an improvement it never
measured a "before" for.

## R0-6 — Answer "fewest tokens" structurally

"how do we maximize code written this way with the fewest number of tokens?" is
a question about amortization, not about writing shorter prompts. A passing
answer names the mechanisms: a skill loaded once instead of rules re-explained
per task, a small component language where one declaration expands into many
lines of output, and generation for everything mechanical.

**Fails if:** the answer is prompt-golf ("use terser instructions"), or ignores
the question, or promises a token reduction with no way to observe one.

## R0-7 — Ontology and generator before hand-written components

The prompt argues that "ast bashing is cheap now" and points at sqlc and
protobuf. A passing response proposes designing the component ontology first
and generating the UI code from it, and grounds that in the generators this
repo already runs rather than inventing new machinery — sqlc, the protobuf
chains, mockgen, Gazelle (`../../../references/go-rules.md` § CS-4).

**Fails if:** it proposes hand-writing a component library and adding
generation "later", or invents a bespoke compiler while ignoring the existing
generator precedents.

## R0-8 — Design the metrics up front, and make them append-only

"empirically demonstrable metrics being captured and that has to be thought
out". A passing response names the specific counters, where the rows live, and
what a row proves — and says that rows are never deleted or edited to improve a
trend.

**Fails if:** "we'll track improvements" with no schema, no storage, and no
statement of what would count as the thing getting worse.

## R0-9 — Close the loop with this prompt inside it

"this prompt itslef and the creation of this tool itself should be able to be
used to improve the tool". A passing response makes the prompt an artifact of
the system: preserved verbatim, scored against a rubric, and re-runnable so a
later version of the skill can be judged on the same input.

**Fails if:** the self-improvement loop is described in the abstract with
nothing durable created — the prompt gets summarized in a plan and then lost.

## R0-10 — Vertical slices, and the loop runs quietly

"work in vertical slices" and "all of that has to just be happening in the
background without anyone really even noticing". A passing response slices the
work so each slice ships something usable end to end, and puts the measurement
inside the normal flow of work rather than adding a ceremony someone has to
remember to perform.

**Fails if:** the plan is horizontal (all the interfaces, then all the tests,
then all the docs), or the metrics depend on a human remembering to run
something before every hand-off.

---

## Judge's note

Two criteria are worth watching for *plausible* failure, where the response
reads well and is still wrong:

- **R0-4 and R0-5 together.** A response can produce an elaborate,
  well-organized style guide and score as excellent prose while measuring
  nothing. The question is not "is this well written", it is "what would fail
  if an agent ignored it".
- **R0-2.** Enthusiasm reads as competence. A response that opens with "I've
  started three agents on this" has failed the case regardless of what those
  agents produced.
