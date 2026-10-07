# Release notes, v0.3.0 (draft)

> **Agent draft, unapproved.** Written by an agent from recorded evidence; the operator has not reviewed or approved it. Each fix lane adds its own section.

CSF v0.3.0 is a developer preview. CSF is one Go process that holds the tools, records, schedules and experiments around agents, programs that work a software ticket on their own. Its compiler, `csfc`, checks the code against a written declaration of the system and generates what used to be hand-written; its harness runs agent sessions behind gates, checks on what an agent runs and says. This release carries 212 changes since v0.2.9.

## What changed

### Agents run end to end inside CSF

- CSF runs its own durable PostgreSQL database; the scheduler, the dispatcher and the self-improvement loop keep their state in it.
- The slice graph dispatches itself. A slice is one unit of work with the slices it depends on; ready slices launch in critical-path order, within limits measured from the host, the daily budget and the provider's rate limit.
- Each agent session runs inside a pinned build container and its own sandbox.
- The recipe chooses the executor, the external coding agent that carries out a turn: Claude Code or GitHub Copilot, with the same tools and gates. The operator's allowed-models ruling refuses any other model.
- A model router can be declared in `providers.json` (any Anthropic-compatible endpoint), and rate limits apply to the provider a session actually used.
- Idle sessions suspend and resume on their recorded conversation; a restarted host resumes its sessions through admission, staggered by the worker cap.
- Agents have inboxes: `csf send` returns a receipt and delivers at the next safe point. Waiting (`csf await`) is a primitive, and CSF hears GitHub events as they happen, with no polling.
- CSF has a GitHub capability with typed GitHub tools over MCP.
- Every merge publishes a checksummed `csf` binary; `csf upgrade` moves a host to it and `-rollback` moves it back.

### Gates

- The reply gate checks what an agent says, not only what it runs. The question gate checks an agent's questions and offered options against the operator's rulings, and every ruling is a typed record naming the gate that enforces it.
- The merge tool refuses a change that lowers the golden metrics, among them the share of the tree reproduced from its declarations, measured by regenerating it.
- House rules CS-19 to CS-21: type parameters over reflection, named compound values, and one file per declared thing.
- The operator-identifier gate gained class patterns, and this release was scanned to zero (see Identifier scan).

### The self-improvement loop

Ouroboros, CSF's self-improvement loop, mines recorded agent sessions for recurring failures and proposes fixes as pull requests.

- Miners and fixers run all the time inside CSF. A miner is an extractor and a Datalog rule with labeled fixtures; its test must kill its own mutants.
- New miners find self-merged pull requests, routing outside the two allowed arms, and hand-rolled waits; a detector flags unusually long turns.
- An always-on dreamer keeps the slice graph full of the work most likely to lower the struggle rate.
- An idle-GPU labeler proposes real instances for mining tickets at no model cost. Failure codes and operator corrections are typed labels, and every build is scored on held-out tickets before it goes live.

### Language and compiler

- CSF declares its own kinds in its meta language. The language, the decision tree, the directory tree and the CLI come from one tree.
- Every grammar decision is one pick from at most 16 options, a typed question that JEV, a typed decision model, can answer. `csf decide` answers typed questions from a local decision model.
- `csfc` generates every service from its declaration, and the bootstrap loop runs to a fixed point.
- The decision tree's invariants are proved in Lean, and CI checks the proof.
- Every tracked directory declares its allowed file types, and the I/O directories are named by the tier they cross.
- `in_process`, `kernel`, `ipc` and `net` are now reserved words in architecture declarations (directory tiers), so they can no longer name a component.
- SCIP code indexes are mined into a fixed fact schema.
- The CSF guide gained diagrams for the bootstrap job, the code workflow and concurrency.

### Workbench and observability

The Workbench is the browser control plane inside `csf serve`.

- Live transcripts, the value stream and who is working where, a host panel, a cost panel (`csf costs`) and cloud burst sessions.
- `csf serve` exports its measurements with a Grafana dashboard; `csf scoreboard` shows every program meter with the consistency headline; `csf status` and `csf tail` read one session projection.
- Operator-facing addresses are a typed registry, and a checked widget definition goes live without a restart.

### Release hygiene

- 122 identifier findings in 51 files were fixed in the content (see Identifier scan); flaky specs were fixed; housekeeping and the loop embed the time zone database.

## Known issues

- A session launched through a declared router can fail to resume when an earlier attempt of the same ticket ran under another provider: the earlier conversation is held in a different provider's configuration directory.
- `csf` has 41 top-level commands; the planned limit is 16 (see What's coming).
- Many components are declared but not yet built. The diagrams mark them as planned.

## What's coming

Planned, not promised: the direction as of this release.

- **State in PostgreSQL.** Every tool call, message and decision becomes a row, and every change a serializable transaction, so concurrent agents always leave the system in a state that satisfies its rules. The concurrency diagram in the CSF guide shows the design.
- **At most 16 commands.** The CLI shrinks to a fixed set of verbs: find, read, edit, ask, send, wait, run, stop, set, serve, gate and go. Each is one SQL operation and one typed decision, served the same way over the CLI, HTTP and MCP.
- **CSF runs the agent loop.** The conversation lives in PostgreSQL and resumes on any model provider, and the external executor becomes optional.
- **Jobs on River**, the PostgreSQL job queue for Go.
- **Search from the database:** text search and symbol search (definitions and references) over the tree, with no file read from disk.
- **Rules as SQL:** rules written in a restricted SQL fragment, compiled to Go with sqlc and to Lean proofs.

<!-- identifier-scan:begin -->
## Identifier scan

The operator-identifier gate (`tools/check_operator_identifiers.py`) found **122 occurrences in 51 files** in the v0.3.0 release candidate. This release has **0**: before 122 → after 0.
The gate's pattern list is **unchanged**: the findings were fixed in the content, and no pattern was widened, narrowed, deleted or excluded.

| Kind | Before | After | Fix rule |
| --- | ---: | ---: | --- |
| Linux home-directory path naming an account | 45 | 0 | A path under a person's or machine's home directory becomes a neutral path (/srv, /var/lib), a path relative to the HOME variable where the code computes it, or, in a test of home-directory scrubbing, a path composed from its segments at run time. |
| non-candacelabs issue or pull-request reference | 77 | 0 | A fixture or example owner is replaced with the publishing organization's name (candacelabs). An upstream reference is reworded so it names the project and the number in words, with a link to the project or to a commit, never to a numbered issue or pull request. |

[identifier-scan-v0.3.0.json](identifier-scan-v0.3.0.json) lists every file with its count and the fix applied to it. It names files, counts and fix rules only; it never contains the text that matched. Evidence that was edited to remove an identifier says so beside the edit.
<!-- identifier-scan:end -->
