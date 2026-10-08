# The CSF kit: install CSF into another repository and use it there

This guide takes you from a fresh clone of this repository to an [agent](../../csf/docs/generated/ontology_cgen.md#term-agent)
session running on a repository of your own, with nothing but Docker on your
machine. Every step says which machine you are on, the exact command to type,
what it does, what success looks like, when to stop, and how to undo it.

Three words you will see throughout, in CSF's vocabulary:

- The **harness** is one process that runs every [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) session on a machine.
  The `csf` program is both the harness and its client; `csf init` starts the
  harness when none is running, and it keeps running.
- An **[assignment](../../csf/docs/generated/ontology_cgen.md#term-assignment)** is one unit of work for an [agent](../../csf/docs/generated/ontology_cgen.md#term-agent), described by a
  **recipe** (a small JSON file) and a **brief** (a Markdown file with the
  task). Submitting a recipe starts a **session**.
- A **[miner](../../csf/docs/generated/ontology_cgen.md#term-miner)** is a program that reads what sessions did and reports on it.

Everything below runs on one Linux machine, in a terminal, from the directory
where you cloned this repository. Commands you type are in boxes. Replace the
parts in angle brackets, such as `<repo>`, with your own values.

## 1. Prerequisites

You need these on the machine. Check each one by typing its command; success
is a version line or an account name, not an error.

| What | Check | Why the kit needs it |
|---|---|---|
| Docker | `docker run --rm hello-world` | The kit builds its two programs inside pinned containers. No Go or Rust is installed on your machine. |
| git, with your name set | `git config --global user.name` | Sessions commit to a git branch. If this prints nothing, set it: `git config --global user.name "Your Name"` and `git config --global user.email "you@example.com"`. |
| gh, logged in | `gh auth status` | The commit gate opens a draft pull request for every session with `gh`. |
| The `claude` program | `claude --version` | Sessions are run by Claude Code, which the harness starts for each [assignment](../../csf/docs/generated/ontology_cgen.md#term-assignment). |
| python3 | `python3 --version` | Only `tools/kit/test-install.sh` uses it, to read the sample recipe back. |

You also need a repository of your own to work in: a git clone with a GitHub
remote, on a branch (not a detached checkout). Below it is called `<repo>`,
such as `$HOME/src/my-project`.

Stop here if any check fails. Nothing has been changed yet.

## 2. Install `csf`, once per machine

Type this from the root of your clone of this repository:

```bash
tools/kit/install.sh
```

Add `--prefix <dir>` to install somewhere other than `$HOME/.local/bin`, and
`--repo <repo>` to run section 3 in `<repo>` straight away.

What it does, in order:

1. Builds `csf` from `app/csf/cmd` inside the `golang:1.26.5-bookworm`
   container, running as you (not root), with its Go module cache under
   `<prefix>/.csf-kit/go`. Installs it as `<prefix>/csf`.
2. Clones the public repository `candacelabs/rrsi` at the pinned revision
   `e741cf3` under `<prefix>/.csf-kit/src` and builds its `rrsi-mine` [miner](../../csf/docs/generated/ontology_cgen.md#term-miner)
   inside the `rust:1.91-bookworm` container, with its Cargo home under
   `<prefix>/.csf-kit/cargo`. Installs it as `<prefix>/rrsi-mine`.

Success looks like two `[PASS]` lines. The first install takes a few minutes
(it downloads and compiles); a second install reuses the caches and is faster.
If `<prefix>` is not on your `PATH`, the installer says so; add it, or call
`<prefix>/csf` by its full path.

The installer refuses, and changes nothing, when `<prefix>/csf` or
`<prefix>/rrsi-mine` already exists and was not installed by this kit. The
message starts with `Refusing to replace`. Pick another `--prefix` or move the
file yourself.

`csf` was called `harness`. Invoked by that name it still works and prints a
deprecation note.

## 3. `csf init` in your repository

This is the one command for every repository you want CSF in. From the root
of `<repo>`:

```bash
csf init
```

What it does, in order:

1. Writes a sample [assignment](../../csf/docs/generated/ontology_cgen.md#term-assignment) into your repository:
   `<repo>/.csf/assignments/sample/agent.json` (the recipe, filled in for
   your repository and its current branch) and `brief.md` (the task).
   Section 8 explains every field. Running it again writes a fresh sample with
   a new [assignment](../../csf/docs/generated/ontology_cgen.md#term-assignment) id.
2. Finds the harness for this machine. If `$HOME/.local/state/csf/harness/harness.json`
   records one that answers, `csf init` registers the repository with it;
   otherwise it starts one in the background on 127.0.0.1:14120 and waits
   until it answers. One harness per machine, never two.
3. Prints the next commands, filled in.

Success: `[PASS] Wrote ...`, then `[PASS] Registered with the running host at
http://127.0.0.1:14120.` or `[PASS] Started the host at http://127.0.0.1:14120.`
The host's log is `$HOME/.local/state/csf/harness/harness.log`.

Stop and read the message if it refuses: it must run at the root of a git
repository that is on a branch.

The sample brief has no gate settings of its own: the harness installs the
[session gates](../../csf/docs/generated/ontology_cgen.md#term-session_gate) (section 7) into every session it runs.

## 4. Submit the sample assignment

```bash
csf submit -recipe .csf/assignments/sample/agent.json
```

What it does: sends the recipe and its brief to the running harness. The
harness creates a git worktree of `<repo>` on a new branch (`csf/sample-<id>`,
starting from the branch `<repo>` was on when you installed), starts a Claude
Code session in it with only the tools the recipe allows, and gives it the
brief.

Success: a JSON reply with `"assignment_id"` (copy it; the next commands need
it), a `"session"` whose `"phase"` is `AGENT_SESSION_PHASE_STARTING` or
`AGENT_SESSION_PHASE_RUNNING`, and a `"check"` with the machine's measured
capacity.

Stop and read the message if the reply is an error: `no harness is recorded
as running` means section 3 was skipped or the harness has stopped; `invalid recipe` names the field to
fix in `agent.json`.

The sample brief asks the [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) to do three things: run `sleep 3` (which the
[session gate](../../csf/docs/generated/ontology_cgen.md#term-session_gate) rejects, on purpose, so you can see a rejection), add one file
named `CSF_SAMPLE.md`, and commit it. The commit gate then pushes the branch
and opens a draft pull request. The whole session takes about a minute.

## 5. Follow the session

```bash
csf events -assignment <assignment id>
```

What it does: prints the session's event log, one JSON object per line, as
it happens, and exits when the session's latest turn finishes (the session
stays open for your next message, section 6). Press Ctrl-C to stop watching
earlier; the session keeps running.

Success looks like, in this order:

1. `"event_type": "harness_run_started"` and then `"harness_worktree_ready"`.
2. A record with `"gate": "wait"` and `"decision": "deny"`: the `sleep 3`
   was rejected. Its `"command"` field shows the command.
3. A record with `"gate": "commit"` and `"decision": "opened"` whose
   `"pull_request_url"` is the draft pull request on GitHub.
4. `"event_type": "harness_run_finished"` and the command exits.

The same log is the file `<state>/<assignment id>/events.jsonl`.

To see the session's state at any time:

```bash
csf get -assignment <assignment id>
```

Success: JSON with `"phase"` (`RUNNING` while the [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) works, `OPEN` when it
has finished its turn and waits for a message), `"branch"`,
`"pull_request_url"` once the commit gate opened one, and `"turns"`.

`csf list` shows every session the harness knows.

To watch and drive every session as a live card instead, open the
[Workbench control plane](../../csf/docs/generated/ontology_cgen.md#term-workbench) the harness serves on its own address,
`http://127.0.0.1:14120/`: send a session a message, cancel it, mark its pull
request ready, merge it, or launch a new one from a ticket.

## 6. Chat with the session

Print the session's chat address and open it in a browser on the same machine:

```bash
csf chat -assignment <assignment id>
```

It prints `http://127.0.0.1:14120/chat/<assignment id>`.

What it does: shows the session's events live and lets you type a message to
the [agent](../../csf/docs/generated/ontology_cgen.md#term-agent). A message starts its next turn in the same worktree, on the same
branch. From the terminal the same thing is:

```bash
csf send -assignment <assignment id> -message "Also add a second line to CSF_SAMPLE.md and commit."
```

Success: the page shows the session; after a message, new events appear and
`csf get` shows `"turns"` increased by one.

## 7. What the session gates do

The harness installs these checks, called [session gates](../../csf/docs/generated/ontology_cgen.md#term-session_gate), into every session it
runs. You do not configure them; the event log shows each decision.

- **The wait gate** looks at every shell command before it runs. It rejects
  three ways of waiting that never end on their own: a loop that sleeps while
  polling (`while ...; do sleep ...`), `pgrep -f` (its pattern matches the
  shell running it), and a foreground `sleep`. The [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) is told what to do
  instead: start the command in the background and act when it finishes. In
  the log: `"gate": "wait"`, `"decision": "deny"` with the rule
  (`poll_loop`, `pgrep_full` or `foreground_sleep`). Ordinary commands are
  `"decision": "allow"`.
- **The commit gate** runs after every shell command that made a git commit.
  It pushes the work branch to `origin` and, if the branch has no open pull
  request on that repository, opens a draft one there titled from the recipe;
  a clone whose `gh` default is another remote still gets its pull request
  where the branch was pushed. In the log: `"gate": "commit"`
  with `"decision": "opened"` (and the URL), `"exists"` (a pull request was
  already open), `"skip"` (the command made no commit) or `"failed"` (with
  the reason, for example `gh` not logged in).
- **The [reply gate](../../csf/docs/generated/ontology_cgen.md#term-reply_gate)**
  reads the reply at the end of every turn. A message sent as the
  operator's own words (the chat, or `csf send -operator`) has its
  [unvetted terms](../../csf/docs/generated/ontology_cgen.md#term-unvetted_term)
  computed first: words the operator never used in an earlier message, after
  code, identifiers and common English are set aside. They are recorded
  (`"event_type": "unvetted_terms"`) and named in the turn, and the reply must
  carry one [research check](../../csf/docs/generated/ontology_cgen.md#term-research_check)
  per term, a fenced block tagged
  `research-check` holding `{"term", "what_it_is", "what_it_does_not_do",
  "fits_goal": yes|no|partly, "why", "likely_source": agent output|paper|
  person|unknown}`. A reply that promises future behaviour ("I'll", "from now
  on", "going forward", "until then I will", "every time") without a commit, a
  gate or [hook](../../csf/docs/generated/ontology_cgen.md#term-hook) change, or a ticket item with an owner in the same turn is
  refused too. In the log: `"gate": "reply"` with `"decision": "deny"`, the
  rules that fired and the reason the turn continues from. After two refusals
  in one turn the reply passes as `"decision": "limit"`.
- **The endpoint gate** keeps every address in the
  [endpoint registry](../../csf/docs/generated/ontology_cgen.md#term-endpoint_registry)
  served. It rejects a shell command that would stop serving one with no
  retirement record: `csf stop` without a `csf serve` in the same command, a `kill` of the recorded `csf serve` process, `pkill` or
  `killall` naming `csf`, and `docker stop`, `rm` or `kill` of a container an
  endpoint names. The reason names each endpoint, its addresses and its users.
  It also rejects `csf endpoint retire`, which records the operator's own
  acknowledgement and so is theirs to run. In the log: `"gate": "endpoint"`,
  `"decision": "deny"`; a command it lets through is not logged. Run as
  `csf gate PreToolUse <state directory>` from a session the harness does not
  run, such as the orchestrator's, it applies this gate alone.

The [agent](../../csf/docs/generated/ontology_cgen.md#term-agent)'s first commit therefore always appears on GitHub as a draft pull
request without the [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) asking.

## 8. The recipe, field by field

`<repo>/.csf/assignments/sample/agent.json` is an `AgentAssignmentRecipe`, the
message defined in `proto/candace/brainspine/v1/brainspine.proto`. The
`csf init` fills the templates in `app/csf/verbs/recipe/`, which are built into `csf`; edit the copy in your
repository to make your own [assignment](../../csf/docs/generated/ontology_cgen.md#term-assignment). One rule throughout: `branch` must
differ from `base_branch`, and `allowed_tools` must not be empty.

| Field | What it is | The sample's value |
|---|---|---|
| `assignment_id` | A fresh UUID naming this [assignment](../../csf/docs/generated/ontology_cgen.md#term-assignment); the harness keeps the run under `<state>/<assignment_id>/` | generated by `csf init` |
| `agent.id` | Short name of the [agent](../../csf/docs/generated/ontology_cgen.md#term-agent), lowercase letters, digits, `_` or `-` | `sample` |
| `agent.revision` | A positive number you raise when you change the [agent](../../csf/docs/generated/ontology_cgen.md#term-agent)'s instructions | `1` |
| `agent.display_name` | The [agent](../../csf/docs/generated/ontology_cgen.md#term-agent)'s name as people see it | `Sample` |
| `agent.instructions` | Standing instructions the [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) reads before the brief | how to behave in the worktree |
| `ticket_url` | The issue or page this [assignment](../../csf/docs/generated/ontology_cgen.md#term-assignment) comes from; shown to the [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) | this guide |
| `model` | The Claude model that runs the session | `claude-haiku-4-5-20251001` |
| `repository_id` | A short name for the repository, used in records | the directory name of `<repo>` |
| `workspace.repository_path` | Absolute path of the repository the worktree is created from | `<repo>` |
| `workspace.base_branch` | The branch the work branch starts from and the pull request targets | the branch `<repo>` was on at install |
| `workspace.branch` | The work branch the worktree checks out; created from `base_branch` | `csf/sample-<first 8 characters of the id>` |
| `workspace.brief_path` | The file holding the task, relative to the recipe; replaces an inline `task` | `brief.md` |
| `workspace.allowed_tools` | The Claude Code tools the session may use without asking; everything else is denied | `Bash`, `Read`, `Write`, `Edit` |
| `workspace.pull_request_title` | Title of the draft pull request the commit gate opens | `CSF sample assignment: add CSF_SAMPLE.md` |

To make a second [assignment](../../csf/docs/generated/ontology_cgen.md#term-assignment), copy the `sample` directory to a new name under
`.csf/assignments/`, give it a new `assignment_id` (`cat /proc/sys/kernel/random/uuid`
prints one), a new `branch`, and your own `brief.md`.

## 9. The miners

`rrsi-mine` reads repositories and session records and prints what it finds.
Three commands matter here. Each prints JSON; success is JSON, failure is an
error line.

```bash
<prefix>/rrsi-mine miners
```

Lists every registered [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) with its name, inputs and the records it
writes: `git-history`, `traces`, `handoffs`, `slices` and `pr-gap`.

```bash
<prefix>/rrsi-mine csf detect --repo <repo>
```

Says whether the repository is CSF-instrumented and why, and exits 0 either
way. The first line is `instrumented: yes` or `instrumented: no`, followed by
each signal it found: `go_mod_is_csf` (the repository is CSF itself),
`go_mod_requires_csf` (a `go.mod` requires `github.com/candacelabs/csf`),
`bazel_module_csf` (a `MODULE.bazel` depends on `csf`), `csf_file` (an
`architecture.csf` file) and `architecture_model` (the CSF compiler produced a
model), and a `csfc:` line saying whether it found the CSF compiler. A plain
repository such as the sample prints `instrumented: no` and `csfc: none`,
which is the expected answer. Add `--json` for the full detection.

```bash
<prefix>/rrsi-mine pr-gap --out <dir outside any git work tree>
```

Measures the "active [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) without a pull request" gap over the Claude Code
transcripts on this machine (under `$HOME/.claude/projects` unless you pass
`--root`): per run it counts commits, pushes and pull requests and fires the
signals `never_pushed`, `slow_push`, `pushed_no_pr`, `slow_pr`,
`brief_defers_pr` and `operator_pr_correction`. It writes `episodes.jsonl`,
`runs.jsonl`, `pr-gap-summary.json`, `pr-gap-task.json`,
`pr-gap-exam-candidates.jsonl` and `pr-gap-state.json` into `--out` and
prints the summary. The output directory must be outside every git work tree,
as rrsi requires; `$HOME/csf-mine` is a fine choice, `<repo>/mine` is not.

## 10. Stop the harness

```bash
csf stop
```

What it does: asks the running harness to shut down. Sessions still running
are closed in order; their worktrees and event logs stay on disk.

Success: JSON with `"sessions_running"` (how many were still open), and
`<state>/harness.json` disappears.

To stop one session without stopping the harness:

```bash
csf cancel -assignment <assignment id>
```

Success: JSON whose `"phase"` is `AGENT_SESSION_PHASE_CANCELING` or
`AGENT_SESSION_PHASE_CANCELED`.

## 11. Undo everything

In this order, each a plain removal; nothing else on the machine was changed.

1. Stop the harness (section 10) if it is running.
2. Remove the two programs and the kit's caches:
   `rm -f <prefix>/csf <prefix>/rrsi-mine && rm -rf <prefix>/.csf-kit`
3. Remove the sample from your repository: `rm -rf <repo>/.csf`. The
   worktrees and branches a session made are under `<state>` and in `<repo>`'s
   branch list; `git -C <repo> worktree prune` and `git -C <repo> branch -D csf/sample-<id>`
   remove them, and the draft pull request is closed on GitHub like any other.
4. Remove the harness state: `rm -rf <state>` (the directory you gave to
   `-state`, `$HOME/.local/state/csf/harness` above).

## 12. Check the kit itself

```bash
tools/kit/test-install.sh
```

Runs the installer with `--prefix` pointing at a temporary directory, runs
`csf init` in a temporary repository against a private state directory and
port, then checks that `csf` prints its usage, the host `csf init` started
answers, `rrsi-mine miners` lists `pr-gap`, and the written sample recipe
names the temporary repository. Success is one `[PASS]` line; the host is
stopped and the temporary directories are removed afterwards. It needs the prerequisites in section 1 and takes as long as an
install.
