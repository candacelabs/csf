# A tour of CSF

This tour walks the whole repository in nine stops, in the order the pieces depend
on each other. Each stop says what the piece is, where it lives, one thing to run,
and what you should see. Every command runs from the repository root. You need
Docker for the Bazel and kit stops and Go 1.26 for the `go run` stops; nothing on
the tour needs a model account, a GPU or a database.

New words link to the [dictionary](../csf/docs/generated/ontology_cgen.md) or the
[glossary](GLOSSARY.md) on first use.

| stop | piece | where it lives | time |
|---|---|---|---|
| 1 | [The idea: LITHE on CPU 0](#1-the-idea-lithe-on-cpu-0) | [README](../README.md#what-is-csf) | 2 min |
| 2 | [The language](#2-the-language-architecturecsf) | [`csf/compiler/language/architecture.csf`](../csf/compiler/language/architecture.csf) | 3 min |
| 3 | [The compiler](#3-the-compiler-csfc) | [`csf/compiler/`](../csf/compiler/README.md) | 10 min |
| 4 | [The runtime library](#4-the-runtime-library) | [`csf/`](../csf/README.md), [`examples/`](../examples) | 3 min |
| 5 | [gotth-live](#5-gotth-live-live-ui-from-go) | [`pkg/gotth/`](../pkg/gotth) | 2 min |
| 6 | [The agent harness](#6-the-agent-harness) | [`app/harness/`](../app/harness), [`services/harness/`](../services/harness) | 10 min |
| 7 | [Miners](#7-miners-ouroboros) | [`csf/docs/generated/ontology_cgen.md`](../csf/docs/generated/ontology_cgen.md#term-ouroboros) | 2 min |
| 8 | [Deploy and Warden](#8-deploy-and-warden) | [`services/deploy/`](../services/deploy), [`services/warden/`](../services/warden), [`infra/deploy-kit/`](../infra/deploy-kit) | 10 min |
| 9 | [xetcas and the primitives](#9-xetcas-and-the-primitives) | [`xetcas/`](../xetcas), [`pkg/`](../pkg) | 10 min |

<p align="center">
  <img src="assets/csf-cpu0-mapping.svg" width="900" alt="CSF drawn inside LITHE's CPU 0 as one Go process containing typed tools, sessions, schedules, knowledge, observation and bounded workers; LITHE's Brain, Spine and Transport cores and the external protocol boundaries are drawn outside it.">
</p>

## 1. The idea: LITHE on CPU 0

LITHE ([Lim and Clites, 2026](../README.md#ref-lithe)) runs a robot's whole control
stack on one partitioned computer: a best-effort
[Brain](../csf/docs/generated/ontology_cgen.md#term-brain) proposes, a real-time
[Spine](../csf/docs/generated/ontology_cgen.md#term-spine) executes, and each gets its
own [core](../csf/docs/generated/ontology_cgen.md#term-core). CPU 0 is the
[housekeeping](GLOSSARY.md#lit-housekeeping) core: it absorbs jitter and prepares the
next controller.

CSF is that philosophy applied to CPU 0. Everything an [agent](../csf/docs/generated/ontology_cgen.md#term-agent)
system needs around its decisions (typed tools, sessions, schedules,
[knowledge](../csf/docs/generated/ontology_cgen.md#term-knowledge), observation) runs as
[services](../csf/docs/generated/ontology_cgen.md#term-service) inside one Go process,
so handoffs between them are function calls, not sockets. The diagram above is that
mapping.

**See it:** read [Why CSF: no IPC inside CPU 0](../README.md#2-why-csf-no-ipc-inside-cpu-0)
in the README, which shows LITHE's four cores and where CSF sits.

## 2. The language: architecture.csf

One file declares what the system is: every term, what contains it, what it
depends on, and the paper each borrowed word comes from. Code, docs and diagrams
are checked against it.

**Try it:**

```bash
grep -n '^term widget ' csf/compiler/language/architecture.csf
```

**You should see** the declaration of a [widget](../csf/docs/generated/ontology_cgen.md#term-widget):
its name, its one-line definition and where it belongs. The same definition
appears, generated, in the [dictionary](../csf/docs/generated/ontology_cgen.md#term-widget).

## 3. The compiler: csfc

[`csfc`](../csf/docs/generated/ontology_cgen.md#term-compiler) checks the code against
the declaration and generates what used to be hand-written: the glossary, the
dictionary, the README's diagrams and its north star table.

**Try it** (Bazel comes from a pinned container; Docker is the only prerequisite):

```bash
bash tools/bazel.sh test //csf/compiler/architecture:all //csf/compiler:csfc --nobuild_tests_only --lockfile_mode=error
bazel-bin/csf/compiler/bin/csfc check
```

**You should see** the compiler's tests pass and `csfc check` exit 0. Then follow
the [runnable walkthrough](../csf/compiler/architecture/WALKTHROUGH.md), which
changes the declaration and shows the check catching the code that no longer
matches.

## 4. The runtime library

[`csf`](../csf/README.md) is a Go library. You build your own binary and
[mount](../csf/docs/generated/ontology_cgen.md#term-mount) CSF's services into it with
functional options; CSF registers routes and hands back a handler, and your
process stays yours. Every operation is generated once and served as HTTP, CLI and
[MCP](../csf/docs/generated/ontology_cgen.md#term-mcp).

**Try it:**

```bash
go run ./examples/csf-theme --listen 127.0.0.1:8089 --theme-dir ./examples/csf-theme
```

**You should see** a server on `http://127.0.0.1:8089` with the generated API and an
MCP endpoint at `/mcp`. Point an agent at that endpoint and say *"Learn about
CSF"*: the `LearnAboutCSF` operation explains the version you pinned. To build your
own binary, start from the [consumer example](../examples/csf-consumer/main.go); the
[extension guide](extending.md) covers the four compile-time seams.

## 5. gotth-live: live UI from Go

[gotth-live](../csf/docs/generated/ontology_cgen.md#term-gotth_live) is CSF's
[web layer](../csf/docs/generated/ontology_cgen.md#term-web): state and rendering stay
in your Go process, and one WebSocket per tab carries events up and re-rendered
fragments down. No npm, no CDN, no code generation.

**Try it:**

```bash
go run ./examples/gotth/counter
```

**You should see** `counter: http://127.0.0.1:8080`. Open it in two tabs and click
in one: the other repaints, and reloading either keeps the count, because the
number lives only in the Go process. The
[counter walkthrough](../examples/gotth/counter/README.md) follows one click through
every file it touches.

## 6. The agent harness

One process per machine runs every agent session. The `csf` binary is both that
process and its client. Every session gets its own git worktree, a
[gate](../csf/docs/generated/ontology_cgen.md#term-session_gate) on every shell command
and commit, and a draft pull request at its first commit. Each session has a chat
page in the browser.

**Try it:** install `csf` once per machine with the
[kit](../tools/kit/README.md), then from the root of any repository of yours:

```bash
csf init
csf submit -recipe .csf/assignments/sample/agent.json
csf events -assignment <id>
csf chat -assignment <id>
```

**You should see** `csf init` write a sample
[assignment](../csf/docs/generated/ontology_cgen.md#term-assignment) under `.csf/`,
`csf submit` start a session and print its id, `csf events` stream the session's
work until it opens a draft pull request, and `csf chat` print the address of its
chat page. Messages typed there join the session's queue as new turns.

## 7. Miners: ouroboros

[Ouroboros](../csf/docs/generated/ontology_cgen.md#term-ouroboros) closes the loop. Miners
read the record of what the operator asked for and what agents did, find where
intent and code diverge, and turn each divergence into the next gate or change.
Every change still lands as an operator-approved pull request, under gates the
miners may not change.

**Today:** session mining runs outside this repository; the CSF service is
milestone 6 of the [north star](../README.md#north-star). This stop has nothing to
run yet.

## 8. Deploy and Warden

The [deploy service](../services/deploy) is an agent-operated deployment system: an
agent harness proposes a change, the deploy service approves and fences it, the
node executor reconciles Compose [applications](../csf/docs/generated/ontology_cgen.md#term-application),
and the operator UI watches. [Warden](../services/warden) is the fleet watchdog it
fences against: Raft-style leader election ([Ongaro and Ousterhout, 2014](../README.md#ref-raft))
over a static peer set, liveness, incidents, and the authoritative view every
mutation is checked against.

**Try it:** the default install is deliberately harmless: a simulated harness, a
dry-run executor, and no Docker socket mounted anywhere.

```bash
./infra/deploy-kit/install.sh
./infra/deploy-kit/status.sh
./infra/deploy-kit/uninstall.sh
```

**You should see** the operator UI at `http://<host>:7780` after the install, and
`status.sh` report every service healthy. The deploy service has no built-in
authentication; keep it on a trusted network or behind your own authenticating
proxy. The [deploy kit manual](../infra/deploy-kit/README.md) covers operations,
and its [trust model](../infra/deploy-kit/AGENTS.md) lists eight invariants with
their enforcement points.

## 9. xetcas and the primitives

[xetcas](../xetcas) is a self-hosted Xet ([Hugging Face](../README.md#ref-xet))
content-addressable storage server with a Git LFS ([Git LFS](../README.md#ref-git-lfs))
front door. Re-pushing a 48 MiB model after editing 2% of it costs about 1 MiB.

**Try it:**

```bash
cd xetcas && bash demo/demo.sh
```

**You should see** the containerized demo push a model, edit it, push again and
report how little was uploaded the second time. The demo exits non-zero if any
step fails, so it doubles as the acceptance test.

The primitives under [`pkg/`](../pkg) import nothing from services or apps, so each
is usable on its own:

| package | what it gives you |
|---|---|
| [`pgmem`](../pkg/pgmem) | a process-local PostgreSQL ([Stonebraker and Rowe, 1986](../README.md#ref-postgres)) emulator for fast tests: real PostgreSQL AST, no server |
| [`liquidproto`](../pkg/liquidproto) | protobuf with [refinement types](GLOSSARY.md#lit-refinement_types) ([Freeman and Pfenning, 1991](../README.md#ref-refinement-types)) compiled into the generated Go |
| [`mailbox`](../pkg/mailbox) | ownership of a mutable value serialized onto one [goroutine](../csf/docs/generated/ontology_cgen.md#term-goroutine), so no field needs a lock |
| [`eventually`](../pkg/eventually) | the one typed await for tests |
| [`cron`](../pkg/cron), [`config`](../pkg/config), [`redact`](../pkg/redact), [`telemetry`](../pkg/telemetry) | schedules, configuration parsing, log redaction and trace propagation |

**Try it:**

```bash
go test ./pkg/pgmem/...
```

**You should see** PostgreSQL queries run against an in-process emulator, with no
database server started.

## Where next

- **Build everything:** [Build it](../README.md#8-build-it) in the README.
- **Use CSF from your own module:** [Consume it](../README.md#6-consume-it).
- **Extend it:** [the four compile-time seams](extending.md).
- **Look up a word:** the [glossary](GLOSSARY.md) and the
  [dictionary](../csf/docs/generated/ontology_cgen.md).
- **Cite it:** [Citation](../README.md#10-citation).
