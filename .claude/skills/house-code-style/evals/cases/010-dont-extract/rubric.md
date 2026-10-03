# Rubric — case 010, don't extract it yet

Tier 2, **negative**. The correct answer is no. Scoring procedure and row
format are in `../../README.md`.

**Why this case exists.** A skill whose CS-3 says "centralize primitives" is
trivially gamed: an agent that answers "yes, extract it" to every question of
this shape scores well on any corpus made only of positive cases, and produces
exactly the `util` package the root `CLAUDE.md` forbids. The counterweights in
CS-3 — *do not create catch-all `util`, `common`, or `core` packages*, and
*"nothing qualifies" is a legitimate result* — are the rule's real content, and
this is the case that tests them.

---

## R10-1 — The verdict is no (required)

The top-line answer is: keep the helper local to the heartbeat widget for now.
Not "yes but keep it small", not "let's extract it behind an interface", not
"sure, and we can always move it later".

**Fails if** the response extracts, or proposes extracting, on this evidence —
including a hedged yes. Everything below is scored only if this one passes.

## R10-2 — The reason is the caller count, stated as a threshold

CS-3 keeps unsettled or service-specific behavior with its owner until at least
two callers need the *same semantics*. One caller is one caller; a hypothetical
future widget is not a second caller, and "the next widget will probably want
it" is the argument the threshold exists to refuse.

**Fails if** the reason given is only aesthetic ("12 lines is too small to
move") without naming the threshold, or if it invents a different one.

## R10-3 — The proposed name is rejected as a category error

`timeutil` and `uiutil` are exactly the catch-all buckets CS-3 bans, and the
response has to say so rather than quietly picking the less-bad of the two. The
ban is about the destination being defined by *what kind of thing it is* rather
than by which layer owns the semantics.

**Fails if** the response accepts either name, or counter-proposes another
catch-all (`shared`, `helpers`, `misc`, `pkg/util`).

## R10-4 — It says what would change the answer

A refusal that cannot be revisited is just as unhelpful as a premature
extraction. The response names the trigger: a second caller needing the same
semantics — at which point the destination is the lowest layer that owns those
semantics (formatting a relative timestamp for a widget belongs with the widget
rendering layer, not in a bucket of "time things"), and `check-go-reuse.sh`
will report the clone if the duplicate is large enough to matter.

**Fails if** the answer is a flat no with no re-entry condition, or if it
defers to "we'll see" without naming what to look for.

## R10-5 — No gate is misrepresented as demanding this

`tools/check-go-reuse.sh` is green here and cannot be otherwise: it detects
duplication, and there is one copy. A response may not cite the reuse gate as a
reason to extract.

**Fails if** the response claims or implies CI wants this change.

---

## Judge's note

The tempting wrong answer is fluent, cites CS-3 by name, and is wrong anyway:
*"Per CS-3, centralize stable primitives at the lowest layer that owns their
semantics — I'll move this into a shared `timeutil` package."* It quotes the
rule and violates both of its counterweights in one sentence. Citation is not
compliance.
