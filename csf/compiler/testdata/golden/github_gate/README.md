# Golden fixtures: one gate, declared once, projected twice

This directory holds the **golden-first** fixtures for `github_gate`, the second
of two reverse-port examples (`example(2, gate, instances([github_gate]))`) that
pin what the `csfc` template should emit *before* the generator exists. The gate
it replaces is today's hand-written [`github.go`](../../../../../services/harness/sessiongate/github.go); the golden is
the shape that file should have once a `gate` block is its single source.

The point of a golden here is narrower than a normal test fixture. We are not
pinning generator output yet — there is no generator. We are pinning the
**ideal output**, written by hand, so an operator can read the exact Go a
generator should emit and reject it before a line of generator is written.

## One source, two projections

Every artifact below reads off the same syntax tree of `github_gate.csf`:

| File | Projection | Today's owner |
|---|---|---|
| `github_gate.csf` | **the source** — a `gate` block in the ontology language | none; `github.go` is hand-written |
| `github_shell.dl` | the Datalog program the block emits | none; the rule is prose in `github.go` |
| `github_gate.go` | the Go check the block emits | [`github.go`](../../../../../services/harness/sessiongate/github.go), 134 lines |

The paper (`docs/paper/draft.md` §3.4) states the target exactly: "declare
$C_1$ once as a `gate` block in the ontology language, emit the Datalog rule
above and this LaTeX from its syntax tree, and pin both with a golden test."

### `github_gate.csf` — the declaration

Modeled on the existing [`failure_code`](../../../../../csf/docs/generated/ontology_cgen.md#term-failure_code) block, the grammar's closest sibling. The
kind is `gate`, the instance `github_gate`; two relations (`word`, `replacement`)
and one rule (`github_shell`). The facts are data, one clause a line:

```
gate github_gate
  relation word "A gh noun the gate names: pr, issue or api."
  relation replacement "The CSF GitHub tool that replaces a gh noun and verb."
  rule github_shell "A gh pr, gh issue or gh api command."
{
  word pr; word issue; word api;
  replacement pr create pulls_create;
  …
}
```

### `github_shell.dl` — the Datalog

The `/* facts */` and `/* predicate */` blocks follow the [miner](../../../../../csf/docs/generated/ontology_cgen.md#term-miner) `rules.dl`
convention. The two clauses are the whole rule: a command whose noun the gate
names is a finding; a command's replacing tool is the `replacement` fact its
noun and verb name.

### `github_gate.go` — the Go

The style the generator must hold (from the brief's `style generated_go`):

- **generics over reflection, never `any`** for a typed field. The `replacement`
  relation is a `[]GithubReplacement` of typed facts, **not** the nested
  `map[string]map[string]string` that `github.go` carries today — the exact
  anti-pattern "we're doing a lot of generic things locally instead of exporting
  things out".
- **typed decoding.** `wordOf` and `verbOf` are generated switch decoders from
  spelling to the declared type; no `json.Unmarshal` into an anonymous struct.
- **the check is a function value, not a method.** `GitHubCheck` matches the
  [`Check`](../../../../../csf/docs/generated/ontology_cgen.md#term-check) shape so the gate registers it in data (CS-6), and `WithChecks` is the
  seam the harness wires it through.

## What is generated vs. the hand-written seam

Everything above the ruled line at the bottom of `github_gate.go` is **what the
generator emits**. The section below it — `type Check`, `WithChecks`, the
`Handle` loop sketch — is the **hand-written seam** the generated check plugs
into. It lives in `gate.go`, not in generated output; it is shown here only so
one file carries the whole shape for review. `WithChecks` does not exist yet:
this is its proposed signature.

## Method and scope

The brief's `golden_first` runs in five steps:

1. **declare** — the `gate` block, the `.dl`, and that `csfc` checks them.
2. **handwrite** — the exact Go the generator should emit, committed here. ← *this PR*
3. **review** — draft PR, **then stop**: the operator approves the golden before
   the generator starts. ← *this PR stops here*
4. **generate** — `csfc` emits the Go; output equals this file **byte for byte**
   under a golden test.
5. **replace** — the generated code replaces `github.go`; replaying its recorded
   inputs is identical.

Step 4 is in this pull request too: [`gate.ml`](../../../architecture/gate.ml) emits
`github_shell.dl` and `github_gate.go` from `github_gate.csf`, and
[`gate_test.ml`](../../../architecture/gate_test.ml) fails if either differs from
the files here by one byte. Step 5 (the generated check replaces `github.go`)
waits for your review of these files.

## Where the gate block's grammar lives

The `gate` production is [`csf/compiler/architecture/gate.ebnf`](../../../architecture/gate.ebnf),
beside the architecture grammar [`language.ebnf`](../../../architecture/language.ebnf);
the ontology grammar is [`csf/compiler/language/grammar.ebnf`](../../../language/grammar.ebnf).
Once kinds are declared in CSF (#487), `gate` becomes one declared kind
instead of a separate grammar file.

**Open under CS-20 (#497):** `relation replacement` and `relation word` name
no columns, so `replacement pr create pulls_create;` is a positional triple.
The fix declares the columns, for example
`relation replacement(noun: word, verb: verb, tool: github_tool)`, and the
generator reads them.

