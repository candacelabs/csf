# ops view, the Workbench control plane

The [ops view](../../csf/docs/generated/ontology_cgen.md#term-ops_view) is the operator's live page of every harness session on this
machine: one card per session, read from the run directory the harness keeps
for it and updated the instant a line lands in its event log. Granted the CSF
[service](../../csf/docs/generated/ontology_cgen.md#term-service)'s operations it is the [Workbench control plane](../../csf/docs/generated/ontology_cgen.md#term-workbench): the same page, now able to
act on every session it shows. It stands beside the Copilot-era
[Workbench](../../csf/docs/generated/ontology_cgen.md#term-bench) Kanban and does not replace it.

## Run it

`csf serve` [mounts](../../csf/docs/generated/ontology_cgen.md#term-mount) the page at `/` on its own listen addresses, beside the
generated operations under `/api/harness/` and at `/mcp`, so the page, the
CLI and every [MCP](../../csf/docs/generated/ontology_cgen.md#term-mcp) client reach one process:

```bash
csf serve -listen 127.0.0.1:14120 -workbench-recipes <directory of recipe templates>
csf serve -listen 127.0.0.1:14120 -listen <tailnet address>:14120 -detach
```

Each listen address is accepted as a browser Origin; `-origin` adds another.
On a tailnet the operator opens `http://<tailnet address>:14120/` on a phone;
the page is a single column there and a grid on a wider screen. Nothing here
adds a public route, a proxy or a firewall rule: binding the tailnet address
is the whole of the exposure, and that address is a flag, never a file in
this tree. `csf view`, which served a read-only copy as a second process on
14121, is retired and says so.

Every address `csf serve` has served is in the
[endpoint registry](../../csf/docs/generated/ontology_cgen.md#term-endpoint_registry), and `csf serve` refuses to start
without serving each one that has no retirement record. An old address keeps
working as a redirect alias: `-redirect 127.0.0.1:14121` answers every
request there with a permanent redirect to the same path on the first listen
address's port. The page's endpoints panel lists every endpoint at each
address, how it is served and who uses it, and every retired address with
where it moved. Only the operator retires an address:

```bash
csf endpoint retire -address 127.0.0.1:14121 -moved-to 127.0.0.1:14120 -acknowledgement '<the operator's words>'
```

## What the operator can do

Every [action](../../csf/docs/generated/ontology_cgen.md#term-action) is one generated operation of the CSF [service](../../csf/docs/generated/ontology_cgen.md#term-service): the page calls it
in process, `csf <verb>` calls it over HTTP, and an [agent](../../csf/docs/generated/ontology_cgen.md#term-agent) calls it as the [MCP](../../csf/docs/generated/ontology_cgen.md#term-mcp)
tool of the same name at `/mcp`. No [action](../../csf/docs/generated/ontology_cgen.md#term-action) exists only in the page.

- Send a message from the card's composer: `SendAgentSessionMessage` with
  `operator_authored` set, so the harness vets the operator's terms and the
  [reply gate](../../csf/docs/generated/ontology_cgen.md#term-reply_gate) holds the reply to them (`csf send -operator`).
- Cancel: `CancelAgentSession` (`csf cancel`).
- Mark the pull request ready: `ReadyAgentSessionPullRequest` (`csf ready`).
- Merge it: `MergeAgentSessionPullRequest` (`csf merge`), which runs the
  worktree's `tools/merge-pr.sh` when there is one, so an operator merge takes
  the same merge path as every other, and gh's squash merge otherwise. It
  waits for the path's checks, so the card says "Merging" for minutes; the
  page must stay open until it reports. [Agent](../../csf/docs/generated/ontology_cgen.md#term-agent) self-merge rules are unchanged.
- Open the pull request and the ticket from the card's links.
- Launch a session: pick a ticket URL and a recipe template, see the
  admission check (`CheckAgentSessionAdmission`, `csf check`), then launch
  exactly the recipe that was checked (`SubmitAgentSession`, `csf submit`).

The harness records every one of these in the session's `events.jsonl` as a
typed `harness_control_action` record with its `action` (submit, send,
cancel, ready or merge), `operator_authored` and `turn_id` for a send, the
pull request for ready and merge, and `level` ERROR with the `error` when the
operation was refused. The card lists them among its recent events, whoever
the client was.

A template is a directory under `-workbench-recipes` holding `agent.json` and
the brief it names: the format `csf submit` reads. The launch form fills the
ticket URL, a fresh [assignment](../../csf/docs/generated/ontology_cgen.md#term-assignment) identifier and a branch of the template's own
with the [assignment](../../csf/docs/generated/ontology_cgen.md#term-assignment)'s first eight characters appended.

## The host panel

At the top of the page, below the strip, the host panel shows what runs on
the machine and controls it. CSF knows no repository's [services](../../csf/docs/generated/ontology_cgen.md#term-service) here: it
reads the host's containers through the container capability and the
profiles the operator defines.

- The gauges: one-minute load against [cores](../../csf/docs/generated/ontology_cgen.md#term-core), free [memory](../../csf/docs/generated/ontology_cgen.md#term-memory), and free disk on
  the state directory's filesystem.
- Every container: name, image, Compose project, state, cpu and [memory](../../csf/docs/generated/ontology_cgen.md#term-memory).
  Containers are grouped as CSF work or everything else. A container is CSF
  work when the harness's records claim it: it carries a session container's
  `csf.assignment` label naming a run directory, or it [mounts](../../csf/docs/generated/ontology_cgen.md#term-mount) a path inside
  one, which is how a session's build containers are found. Stopped
  containers are folded away under each group.
- Start, stop, pause and unpause, per container and per group, and a
  protect mark per container. A protected container is never stopped or
  paused by anything on the panel.
- The profiles, each a [host profile](../../csf/docs/generated/ontology_cgen.md#term-host_profile). The page starts with "only CSF work",
  which stops everything that is not CSF work; the operator adds others,
  such as "essentials", a named set to keep running, from the editor under
  the profiles. Applying one shows its diff before one confirm. The last
  applied profile is undone in one tap.
- The load guard: when the load passes 1.5 times the [cores](../../csf/docs/generated/ontology_cgen.md#term-core), the panel says so
  and offers "only CSF work" and a hold on new sessions (the [slice
  dispatcher](../../csf/docs/generated/ontology_cgen.md#term-slice_dispatcher)'s pause). It never acts on its own.

Every button on the panel is a [verified action](../../csf/docs/generated/ontology_cgen.md#term-verified_action). Its operation is dry-run against the
live host when the panel is first drawn and again on every refresh, five
seconds apart. A button whose check fails is drawn disabled with the reason.
A stop, a pause, a profile, an undo, or a change to more than one container
lists the containers it changes and asks for one confirm. The operation
plans again at the press and refuses if the host moved since the diff was
shown. After acting, it reads the host back and fails unless each container
reached the state its [action](../../csf/docs/generated/ontology_cgen.md#term-action) leaves.

Each control is one typed operation, served over HTTP under `/api/host/` and
as the [MCP](../../csf/docs/generated/ontology_cgen.md#term-mcp) tool of the same name at `/mcp`: `get_host`, `control_containers`,
`apply_host_profile`, `undo_host_profile`, `put_host_profile` and
`protect_containers`. Each change takes `dry_run`, and its `confirm` names
exactly what the dry run listed. The container capability the panel is
granted has no remove and no volume call, so nothing here can remove a
container or touch a volume.

Two files under the state directory hold the operator's records:
`host.json`, the profiles and protected names, and `host.jsonl`, one line per
applied profile or undo with exactly the changes made. A profile that changed
nothing is not recorded, so it never stands between the operator and the
undo of one that did.

## The first screen

Phone-first, top to bottom: a strip with the running sessions, the merges
the [control plane](../../csf/docs/generated/ontology_cgen.md#term-workbench) made today and the session spend today, with the
[fixers](../../csf/docs/generated/ontology_cgen.md#term-fixer)' spend against the [mining loop](../../csf/docs/generated/ontology_cgen.md#term-ouroboros)'s daily cap beside it; the launch form; the
cards, running sessions first; the loop panel; the [miners](../../csf/docs/generated/ontology_cgen.md#term-miner) panel; and the
dispatch queue. Today is the newest UTC day the page has read anything for,
so the strip needs no clock. A session's spend is folded from its result
records the way the loop folds a [fixer](../../csf/docs/generated/ontology_cgen.md#term-fixer)'s cost.

The dispatch queue panel reads `dispatch.json` under the state directory,
the snapshot the [slice dispatcher](../../csf/docs/generated/ontology_cgen.md#term-slice_dispatcher) in [`services/dispatch`](../dispatch)
writes on every pass: running slices, then the queue in dispatch order with
why each one waits, then the held ones, with the capacity and whether the
dispatcher is paused. Until that file exists the panel says so.

## What a card shows

Every field comes from two files under `<state>/<assignment id>/`:
`run.json`, the run record, and `events.jsonl`, the event log the harness and
its [turn executor](../../csf/docs/generated/ontology_cgen.md#term-turn_executor) append to.

| Field | Source |
|---|---|
| [agent](../../csf/docs/generated/ontology_cgen.md#term-agent), [assignment](../../csf/docs/generated/ontology_cgen.md#term-assignment), branch, ticket, turns | the run record |
| status: running, waiting, finished, [suspended](../../csf/docs/generated/ontology_cgen.md#term-session_suspend), failed, closed | the harness's own records: a turn requested, a turn finished, the session [suspended](../../csf/docs/generated/ontology_cgen.md#term-session_suspend), a turn failed, the [turn executor](../../csf/docs/generated/ontology_cgen.md#term-turn_executor) closed; waiting is a finished turn with background tasks running |
| resumes | the session resumed after a [suspend](../../csf/docs/generated/ontology_cgen.md#term-session_suspend); the resumed turn's record names its time to first token |
| background | the executor's latest `background_tasks_changed`; [background results](../../csf/docs/generated/ontology_cgen.md#term-background_result) |
| model | the first assistant message |
| executor | the run record; Claude Code for a run recorded without one |
| tool calls | every `tool_use` block in assistant messages |
| gate denials | [session gate](../../csf/docs/generated/ontology_cgen.md#term-session_gate) decisions that denied |
| pull request | the turn's receipt, or the executor's `code_change_published` |
| elapsed | first record to last record, at minute resolution |
| recent events | the last few of: tool calls, gate denials, the executor's own denials, the operator's messages, turn results, harness records; a card shows five and holds twenty, and "show earlier" is the one thing a browser may ask |

A waiting session is not stranded. Its turn has ended, but the executor still
runs background tasks, and the completion of each one starts a turn with no
Send. The harness records that turn as a [background result](../../csf/docs/generated/ontology_cgen.md#term-background_result) and counts it.
The card shows how many tasks are running and how many turns they woke.

Elapsed time is read from the log's own timestamps rather than a clock, so a
render stays a pure function of state; a running session logs every few
seconds, so the label is current to the minute.

## What the resident panel shows

Above the [miners](../../csf/docs/generated/ontology_cgen.md#term-miner), one panel for what the harness holds in [memory](../../csf/docs/generated/ontology_cgen.md#term-memory): the
harness's own resident set and the resident set of its executors and
everything they started, the open sessions, the executors alive, how many
sessions were resumed after a [suspend](../../csf/docs/generated/ontology_cgen.md#term-session_suspend), the latest resumed turn's time
to first token, and the derived idle bound with the quantile it sits at. The
source is `resident.jsonl` at the root of the state directory, which the
harness samples once a minute from the process table it is granted and
rewrites with the last day of samples. The panel follows the file the way the
[miners](../../csf/docs/generated/ontology_cgen.md#term-miner) panel follows its series. Until a sample exists the panel is empty.

## What the miners panel shows

Above the cards, one row per [Ouroboros](../../csf/docs/generated/ontology_cgen.md#term-ouroboros) [miner](../../csf/docs/generated/ontology_cgen.md#term-miner): its latest [mutation score](../../csf/docs/generated/ontology_cgen.md#term-mutation_score) and
every earlier one, as a series. The source is one file at the root of the
state directory, `mutation.jsonl`, one record per line (`Mutation` in
`services/ouroboros/contract/records.proto`). A [miner](../../csf/docs/generated/ontology_cgen.md#term-miner)'s `mutate --json` verb
prints the record; whoever runs it appends the line:

```bash
bazel-bin/services/ouroboros/miners/_template/miner.exe mutate --json \
  services/ouroboros/miners/_template/fixtures/labels.tsv \
  'services/ouroboros/miners/_template/fixtures/*/events.jsonl' >> <state>/mutation.jsonl
```

A point reads `killed/counted = score`, green when the gate accepted the [miner](../../csf/docs/generated/ontology_cgen.md#term-miner)
and red when it rejected it; a rejected point lists the surviving mutants. The
panel follows the file the way the cards follow their logs: a write re-reads
it and patches the panel alone. Until a record exists the panel is empty.

## The labeler panel

Above the board, one panel shows the [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler)'s run record, `<state>/labeler.json`,
which the harness binary's `label` verb replaces after every batch: the
model, the phase (labeling, yielding or finished), the ticket in hand, how
many tickets were labeled and how many gained a real instance, the labels
proposed, accepted and rejected, the precision of the proposed positives
under the acceptance rule, labels per hour, the GPU's utilization and [memory](../../csf/docs/generated/ontology_cgen.md#term-memory),
the keep-alive the last request carried with the two measurements it derives
from, and how often the [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler) yielded the GPU. The panel is its own
fragment, so a replaced record patches the panel and nothing else, and a
machine with no record shows the panel saying so.

## How it stays live

The view is a [service](../../csf/docs/generated/ontology_cgen.md#term-service) in the host [app](../../csf/docs/generated/ontology_cgen.md#term-app)'s [web layer](../../csf/docs/generated/ontology_cgen.md#term-web): it owns no listener and no
process. `csf serve` grants it two capabilities over the state directory,
the file capability to read and the watch capability to be told of changes,
the CSF [service](../../csf/docs/generated/ontology_cgen.md#term-service)'s operations and the template directory, and binds the
listener.

Each browser connection is one live UI session with one follow effect. The
effect reads every run directory once, then waits on the kernel's change
notification: a write to a session's event log re-reads that log from where
the last read stopped, folds the new lines into the session's card, and emits
the card as an internal event addressed to the card's own region. The card is
a [widget](../../csf/docs/generated/ontology_cgen.md#term-widget) in a keyed collection, so only that card's region is patched; the
board re-renders only when a session appears or the order changes. There is
no polling loop and no page refresh, and a browser cannot post a card of its
own: the event is internal, never registered.

## Proof

- `go test -race ./services/opsview/` runs the unit specs over real record
  shapes and the integration specs, which drive the page over the real wire
  with a gomock watcher: a card appears, only that card patches when a line
  lands, a run directory that appears later is adopted, a failing watch is
  reported on the page, a mutation record landing patches the [miners](../../csf/docs/generated/ontology_cgen.md#term-miner)
  panel alone, and a replaced [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler) record patches the [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler) panel alone.
- The browser conformance spec, labelled `browser`, runs headless Chromium in
  the [gotth-live](../../csf/docs/generated/ontology_cgen.md#term-gotth_live) bench image against a real listener, a real watch and a real
  file: the card's tool-call count moves from 1 to 3 on one appended line with
  no page reload.
- The host specs drive the host operations over a gomock [Engine](../../csf/docs/generated/ontology_cgen.md#term-engine) holding a
  table of containers. CSF work is grouped by label and by [mount](../../csf/docs/generated/ontology_cgen.md#term-mount). "only CSF
  work" applies only after the exact confirm and is undone from the ledger.
  A protected container refuses to stop and its button is drawn disabled
  with the reason. A confirm made before the host moved is refused. A
  satisfied profile is a no-op that records nothing. A change the [Engine](../../csf/docs/generated/ontology_cgen.md#term-engine)
  accepted but did not make fails the acceptance check.
- The host phone spec, labelled `browser`, applies "only CSF work" on a
  390x844 phone: the diff, the confirm, the containers stopping, then one tap
  to undo and the containers starting, with no page reload.
- The live run, the opt-in `acceptance` suite in `host_acceptance_test.go`,
  points the panel at the machine's own [Engine](../../csf/docs/generated/ontology_cgen.md#term-engine), process table and state
  directory. It applies the operator's "essentials" while the host already
  satisfies them. It passes no confirm, so a host that did not satisfy them
  would be refused, never changed.

## The loop panel

Above the cards sits the [mining loop](../../csf/docs/generated/ontology_cgen.md#term-ouroboros)'s panel: the [struggle rate](../../csf/docs/generated/ontology_cgen.md#term-struggle_rate) of the
week with its interval, the [compounding rate](../../csf/docs/generated/ontology_cgen.md#term-compounding_rate) per week with its
interval and the weeks it was fitted on beside the baseline to beat, the
per-day early read, the internal check's exponent with its week table, the
[miners](../../csf/docs/generated/ontology_cgen.md#term-miner), generic or tenant, the findings, the [fixers](../../csf/docs/generated/ontology_cgen.md#term-fixer) with their
spend against the daily budget and the cost of a ready pull request, the
[merge train](../../csf/docs/generated/ontology_cgen.md#term-merge_train)'s record, the [labeler](../../csf/docs/generated/ontology_cgen.md#term-labeler)'s queue, one row per [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) and the
last fourteen days. It is read from `ouroboros.json`, the snapshot
[`services/ouroboros`](../ouroboros) writes into the state directory on
every measure, and follows that file through the same watch the cards use;
a state directory with no snapshot shows the panel empty.

## Installed widgets

Between the panels and the cards sits every [widget](../../csf/docs/generated/ontology_cgen.md#term-widget) installed from the
`widgets` directory of the checkout `csf serve` runs from (`-widgets DIR`
names another), drawn over the loop's snapshot. The page watches that directory and
each definition in it through the watch capability: a definition that lands
is checked and drawn with no restart, one that is removed disappears, and one
that fails the check is shown refused with every reason and never drawn.
[`widgets/README.md`](../../widgets/README.md) is the format and the checks;
`operations.go` serves `list_widgets`, `check_widget` and `propose_widget`,
which `csf serve` exposes over [MCP](../../csf/docs/generated/ontology_cgen.md#term-mcp), and `recipe/` is the template an [agent](../../csf/docs/generated/ontology_cgen.md#term-agent)
session makes a [widget](../../csf/docs/generated/ontology_cgen.md#term-widget) from.

## Not done

Later slices: the fleet panel from Warden, the heap, accepting and rejecting
a [miner](../../csf/docs/generated/ontology_cgen.md#term-miner) from the page, the compounding chart over the series, and a ticket
picker that lists open tickets instead of taking a URL.
