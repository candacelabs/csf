# Golden fixtures: one view, declared once, projected twice

This directory holds the **golden-first** fixtures for `observe_sessions`, the
first reverse-port example (`example(1, view, instances([observe_sessions]))`)
that pins what the `csfc` template should emit *before* the generator exists.
The behavior it replaces is today's hand-written
[`progress.go`](../../progress.go), [`read.go`](../../read.go) and
[`work.go`](../../work.go), and the two `csf` verbs they feed; the golden is the
shape that code should have once a `view` block is its single source. The `view`
block itself lives at
[`csf/compiler/testdata/golden/view/observe_sessions.csf`](../../../../../csf/compiler/testdata/golden/view/observe_sessions.csf)
— a CSF source cannot live under `services/`.

The point of a golden here is narrower than a normal test fixture. We are not
pinning generator output yet — there is no generator. We are pinning the
**ideal output**, written by hand, so an operator can read the exact Go a
generator should emit and reject it before a line of generator is written.

## One source, two projections

Every artifact below reads off the same syntax tree of `observe_sessions.csf`:

| File | Projection | Today's owner |
|---|---|---|
| [`observe_sessions.csf`](../../../../../csf/compiler/testdata/golden/view/observe_sessions.csf) | **the source** — a `view` block in the ontology language | none; the projection is hand-written |
| `observe_sessions.dl` | the Datalog acceptance program the block emits | none; the brief is prose |
| `progress.go` | the Go projection the block emits | [`progress.go`](../../progress.go), 326 lines |

The brief states the target exactly: one command shows every session as a
`session_progress` row, one command tails them all, there is no polling and no
JSON on screen.

### `observe_sessions.csf` — the declaration

Modeled on the existing [`failure_code`](../../../../../csf/docs/generated/ontology_cgen.md#term-failure_code) block, the grammar's closest sibling, and on the
sibling `gate` block in
[`sessiongate`](https://github.com/candacelabs/csf_staging/pull/483) (under review). The kind
is `view`, the instance `observe_sessions`; one relation (`session_progress`),
two verbs (`status`, `tail`), one line render, and the resolution of a virtual
session through `services/harness/routing`. The fields and the line kinds are
data, one clause a line:

```
view observe_sessions
  relation session_progress "One row per session: …"
  verb status "Every session as one session_progress row, as a table." -> table session_progress
  verb tail "Every session's folded lines, readably, labelled by agent, never as JSON." -> stream line
  render line "{time} {agent} {kind} {text}"
  resolve virtual through "services/harness/routing"
{
  field assignment "assignment_id" "…";  field agent "agent_id" "…";
  kind turn "…";  kind tool "…";  …
}
```

### `observe_sessions.dl` — the Datalog

The `/* facts */` and `/* predicate */` blocks follow the [miner](../../../../../csf/docs/generated/ontology_cgen.md#term-miner) `rules.dl`
convention. The two clauses are the brief's own acceptance and offense rules: a
brief is done when one command answers each question with no polling and no
JSON on screen, and a session that wrote a script to answer a question a verb
already answers is the proxy the brief mines.

### `progress.go` — the Go

The style the generator must hold:

- **a typed field per declared field**, and a typed `Kind` with a **generated
  switch decoder** (`kindOf`) from spelling to the declared type — the shape
  [`wordOf`](https://github.com/candacelabs/csf_staging/pull/483) and `verbOf`
  hold for the `gate` block, never a loop over a string slice.
- **the render is one method.** `Line.String()` is the block's
  `render line "{time} {agent} {kind} {text}"` and the only place the format
  lives; no caller re-spells it.
- **the two verbs are function values, not methods.** `Status` and `Tail` take
  the projection and return what the command prints, so the `csf` command
  registers them in data.
- **the fold is pure.** `Fold` applies one [`session`](../../../../../csf/docs/generated/ontology_cgen.md#term-session) record to a row and returns the row
  and its lines, so a replay of the recorded [evidence](../../../../../csf/docs/generated/ontology_cgen.md#term-evidence) folds identically.

## What is generated vs. the hand-written seam

Everything above the ruled line at the bottom of `progress.go` is **what the
generator emits**. The section below it — reading the state directory into
records, and filling the two git fields through a `proc.ILauncher` — is the
**hand-written seam** the projection plugs into. It lives in `read.go` and
`work.go`, not in generated output; it is shown here only so one file carries
the whole shape for review.

## Method and scope

The brief's `golden_first` runs in five steps:

1. **declare** — the `view` block, the `.dl`, and that `csfc` checks them.
2. **handwrite** — the exact Go the generator should emit, committed here. ← *this change*
3. **review** — the operator reads the golden and redirects it before the
   generator starts. It happens **after the fact, on `main`**: the operator's
   standing ruling is that work merges rather than stopping at a draft and
   waiting for approval.
4. **generate** — `csfc` emits the Go; output equals this file **byte for byte**
   under a golden test.
5. **replace** — the generated code replaces `progress.go`; replaying its
   recorded inputs is identical.

Steps 4 and 5 are **out of [scope](../../../../../csf/docs/generated/ontology_cgen.md#term-scope)** here. This change is steps 1–2, merged for the
after-the-fact review that is step 3.

## Where the view block's grammar lives

The CSF language has two EBNF grammars:
[`csf/compiler/language/grammar.ebnf`](../../../../../csf/compiler/language/grammar.ebnf)
(the ontology language, where [`failure_code`](../../../../../csf/docs/generated/ontology_cgen.md#term-failure_code)
is declared) and
[`csf/compiler/architecture/language.ebnf`](../../../../../csf/compiler/architecture/language.ebnf)
(the architecture language: `process`, `scope`, `component`). Neither has a `view` production yet. The
`view` block above follows that term in `grammar.ebnf`, so the next step is a
`view` production there, decoded by the same compiler; the `.dl` and `.go` are
consequences of that block.

The first thing to approve or redirect is that block's shape — in particular
whether a read-only projection is a `view`, a `verb` or a `relation`, and
whether `render`/`resolve` belong in the block.

