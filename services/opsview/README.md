# ops view

The [ops view](../../csf/docs/generated/ontology_cgen.md#term-ops_view) is the operator's live page of every harness session on this
machine: one card per session, read from the run directory the harness keeps
for it and updated the instant a line lands in its event log. It is the first
live version of the view the [Workbench](../../csf/docs/generated/ontology_cgen.md#term-bench) will grow into; the name is provisional
until the view term lands.

## Run it

The harness binary hosts it as a second process, separate from `harness serve`
so the view starts, stops and restarts without touching running sessions. It
reads the same state directory serve writes.

```bash
harness view                                   # http://127.0.0.1:14121/ for development
harness view -listen 127.0.0.1:14121 -listen <tailnet address>:14121 -detach
harness view -stop                             # ends the one recorded in <state>/view.json
```

Port 14121 is the view's: the next port after the harness's own 14120. Each
listen address is accepted as a browser Origin; `-origin` adds another. On a
tailnet the operator opens `http://<tailnet address>:14121/` on a phone; the
page is a single column there and a grid on a wider screen. Nothing here adds
a public route, a proxy or a firewall rule: binding the tailnet address is the
whole of the exposure, and that address is a flag, never a file in this tree.

## What a card shows

Every field comes from two files under `<state>/<assignment id>/`:
`run.json`, the run record, and `events.jsonl`, the event log the harness and
its [turn executor](../../csf/docs/generated/ontology_cgen.md#term-turn_executor) append to.

| Field | Source |
|---|---|
| [agent](../../csf/docs/generated/ontology_cgen.md#term-agent), [assignment](../../csf/docs/generated/ontology_cgen.md#term-assignment), branch, ticket, turns | the run record |
| status: running, finished, failed, closed | the harness's own records: a turn requested, a turn finished, a turn failed, the [turn executor](../../csf/docs/generated/ontology_cgen.md#term-turn_executor) closed |
| model | the first assistant message |
| tool calls | every `tool_use` block in assistant messages |
| gate denials | [session gate](../../csf/docs/generated/ontology_cgen.md#term-session_gate) decisions that denied |
| pull request | the turn's receipt, or the executor's `code_change_published` |
| elapsed | first record to last record, at minute resolution |
| recent events | the last few of: tool calls, gate denials, the executor's own denials, the operator's messages, turn results, harness records; a card shows five and holds twenty, and "show earlier" is the one thing a browser may ask |

Elapsed time is read from the log's own timestamps rather than a clock, so a
render stays a pure function of state; a running session logs every few
seconds, so the label is current to the minute.

## How it stays live

The view is a [service](../../csf/docs/generated/ontology_cgen.md#term-service) in the host [app](../../csf/docs/generated/ontology_cgen.md#term-app)'s [web layer](../../csf/docs/generated/ontology_cgen.md#term-web): it owns no listener and no
process. `harness view` grants it two capabilities over the state directory,
the file capability to read and the watch capability to be told of changes,
and binds the listener.

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
  lands, a run directory that appears later is adopted, and a failing watch is
  reported on the page.
- The browser conformance spec, labelled `browser`, runs headless Chromium in
  the [gotth-live](../../csf/docs/generated/ontology_cgen.md#term-gotth_live) bench image against a real listener, a real watch and a real
  file: the card's tool-call count moves from 1 to 3 on one appended line with
  no page reload.

## Not done

Later slices: the fleet panel from Warden, the ouroboros ring, the heap,
operator actions (accept and reject), the compounding chart.
