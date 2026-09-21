# Candace workbench UI

The production browser workbench for the Copilot adapter. It is a Vite 6,
React 18 and strict TypeScript bundle mounted by the Go service at `/ui/`.
There is no independent frontend server in production.

## Product surface

- `/ui/` is the user entry point. Home shows observed task/worktree counts,
  model availability and searchable tasks with their actual session status.
  Refresh overview reads a new snapshot; failed refreshes mark the prior
  snapshot and never turn unavailable counts into zero.
  **Refresh overview** also refreshes the model catalog. The Home sidebar link
  returns here from any chat, without changing existing session URLs.
- `/ui/#/kanban` embeds the server-rendered task board inside the existing
  React shell. One gotth WebSocket updates keyed card regions. Explicit task
  links connect sessions to authoritative checkpoints; session runtime status
  remains visible separately. Task checkpoints are last observed; **Refresh task
  checkpoints** reads external GitHub changes. There is no GitHub webhook or
  background board polling. Drag a grip between task-status columns, or use
  the keyboard-accessible **Move task** form. A drop submits the observed
  checkpoint and target status; the card stays in its previous column until
  the server confirms the move. Blocked/operator moves require a reason.
  Unlinked sessions stay in Unassigned and cannot be moved until a current
  checkpoint is available. Task, checkpoint and evidence links use server data.
- `/ui/#/simulations` uses the same shell to inspect recorded runs, simulation
  steps, camera images, measurements, artifacts, trace URLs and bounded logs.
  Its browser types are generated from the existing CSF OpenAPI contract.
  Run links include the selected ID. Read endpoints refresh while visible;
  operator controls remain available through the existing simulation page.
- `/ui/#/release` links to the installation's configured consumer guide through
  `VITE_CSF_RELEASE_URL`. It carries no embedded deployment or release receipts.
- The Workbench reads shared custom CSS and refreshes it every five seconds
  while the tab is visible, including when focus returns. A failed refresh
  leaves the last successful CSS in place. Visit `/ui/?theme=default` to bypass
  shared CSS in that tab and recover from a broken override.
- A persistent sidebar groups durable chats under the worktrees returned by the
  adapter. New tasks create an isolated worktree by default. Reusing either the
  configured checkout or an existing managed worktree is an explicit choice;
  the browser sends a server-issued repository/worktree ID and never a path.
- The central chat combines the replayed transcript, one typed SSE stream,
  pending exact-identity permission requests, queue-or-steer composition, abort, and
  model switching.
- A collapsible dock can sit on the right or bottom and remembers its position.
  Its tabs are Terminal, Changes, Worktree, Schedules, and Subagents.
  Mantine owns tab selection, arrow-key navigation and panel associations.
  Labels remain readable on small screens with horizontal scrolling; collapsing
  the dock returns the available width to the conversation.
- Terminal uses xterm.js against the adapter-owned PTY API, including replay,
  input, resize, lifecycle state, and stop. The heavy emulator is loaded only
  when the terminal pane opens. Input sends immediately and combines keystrokes
  that arrive while a request is pending, within a bounded buffer. Uncertain
  writes are never retried. PTY output wakes the Go stream through an atomic
  replay notification, with no polling delay before flushing.
- Changes and Worktree show the adapter's bounded diff and current git metadata.
- Schedules provide create, edit, pause, resume and confirmed delete over the
  adapter's `pkg/cron`-backed API.
- Subagents show active/completed delegated work from the real API and live SSE
  data. Selecting a row opens its chronological activity and tool output.
- Tool cards expand into decoded commands, descriptions, output and recorded
  exit codes. The nested **Raw tool data** inspector keeps the original payloads
  and call ID. Unknown result formats remain visible; a completed turn without
  a tool result is labelled **no result**, never a successful tool execution.
  Long outputs scroll within a keyboard-focusable block. Retained partial replies
  in idle sessions do not show an active thinking animation.
- Model pickers use `/v1/models`, show display names and IDs, and can refresh the
  catalog independently of task loading. A session keeps its actual current
  model visible even when it is absent from that catalog. New tasks require a
  choice when several models are available; a single available model is
  preselected. An empty or failed catalog disables selection and task creation
  rather than suggesting unverified model IDs.

`src/sessionEvents.ts` is the exhaustive reducer for the generated
`SessionEvent` discriminated union. Transcript, requests, session metadata,
subagent identity and subagent activity share one EventSource and resume
watermark. Adding a new server event kind breaks the TypeScript build until the
UI deliberately handles it.

## Run and verify

From the public module root containing `go.mod`, the container path avoids a
host Node installation:

```bash
docker run --rm --user "$(id -u):$(id -g)" \
  -e npm_config_cache=/tmp/npm \
  -v "$PWD:/workspace" -w /workspace/services/copilot-adapter/ui \
  node:22-bookworm bash -lc \
  'npm ci && npm run gen:check && npm test -- --run && npm run build'
```

For local development with Node 22, run `npm run dev` from this directory.
Vite serves the UI below `/ui/` and proxies `/v1` and `/healthz` to
`http://127.0.0.1:8090`. Start the [public host](../README.md#run-it-as-a-plain-binary)
with `--listen 127.0.0.1:8090` and `--origin` set to the Vite browser origin.
The development proxy covers the Workbench routes; use the built bundle on the
CSF host to exercise the CSF HTTP routes from the same origin.

For a Workbench bundled with CSF, set `VITE_CSF_DASHBOARD_URL=/` at build time
to add a **CSF dashboard** link to the sidebar. An absolute dashboard URL also
works. Leave the variable unset for a standalone Workbench; no dashboard link
is rendered by default. This setting supplies the origin for the CSF API;
Copilot session requests remain on the Workbench origin.
Home and the sidebar share the same destination registry. Optionally set
`VITE_CSF_RELEASE_URL` to the deployment's reviewed guide or receipt URL to add
**Release & evidence** to both. That page links to the configured guide; it
does not embed release receipts or a retained deployment dataset.
The progress board at `/` remains available; `/ui/` is the common entry point.

## Contract projection

`../openapi.yaml` owns Copilot requests, responses and SSE shapes.
`csf/tools/codegen/generated/openapi/adapter.openapi.json`, relative to the public
module root, owns the separate
CSF HTTP operations, projected into `src/api/csf-schema.d.ts`.
`src/api/schema.d.ts` is generated and must not be edited:

```bash
npm run gen
npm run gen:check
```

The tests cover transcript replay/delta folding, the exhaustive live reducer,
request decisions, safe Markdown, the grouped sidebar, default and existing
worktree creation, schedule transport and confirmation, and the Subagents
list-to-live-activity interaction.

## Kanban island ownership

`src/kanbanTemplates.tsx` is the build-time source for the Go templates in
`../kanban/templates/`. `npm run gen:kanban` renders the existing Mantine
components/theme with React `renderToStaticMarkup`; `npm run gen:kanban:check`
checks deterministic output and runs during both `gen:check` and `build`.
Edit the TSX source, never the `_cgen.html` outputs. Go `html/template` escapes
all runtime fields. The server supplies the generated WorkStatus options and
marks only its already-rendered card markup as trusted HTML.

React owns one empty ref host. The initial same-origin HTML fills it once;
gotth owns descendant patches. SortableJS 1.15.6 owns temporary drag placement
and restores it before submitting the existing form. It never persists a local
board order. Its MutationObserver attaches new columns and destroys removed
ones without polling. The route cleanup destroys Sortable instances, cancels
its fetch, stops the gotth connection and removes its descendants.

```ts
const runtime = await loadGotthRuntime();
const response = await fetch("/v1/kanban/view", { signal });
host.innerHTML = await response.text(); // trusted same-origin server markup
runtime.start("/v1/kanban/live", host);
// Route cleanup (after disposing Sortable):
runtime.stop();
host.replaceChildren();
```

The live handler also serves `/v1/kanban/live/gotth-live.min.js`. Loading that
script without `data-gotth-url` opens no connection; `start` owns all listeners.
`stop` closes the socket, cancels reconnect/resync/debounce timers and removes
listeners. A later `start` creates a fresh session. Vite proxies `/v1` including
WebSocket upgrades for the same lifecycle in development.

[SortableJS documentation](https://github.com/SortableJS/Sortable) owns drag
behavior; the application bridge uses `Sortable.create(column, { handle,
onStart, onMove, onEnd })` and `sortable.destroy()` at route cleanup.
