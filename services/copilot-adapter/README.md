# copilot-adapter

A REST surface over one host's GitHub Copilot CLI, so a browser UI can start
sessions, prompt or steer them, watch the transcript stream, and answer the
requests the CLI blocks on. It is the [Copilot Pair](../../extensions/copilot-pair/README.md)
feature list expressed as a typed HTTP contract instead of an extension.

The service is a **library package** (house rule CS-10): its entry point is
`NewCopilotAdapter(options ...Option) (*CopilotAdapter, error)`, it reads no
environment, parses no flags, installs no signal handler and requires no
container. There is no `cmd/` and no Dockerfile here: those are one-to-one with
a binary and live in `app/`.

## Contract first

Nothing here is hand-written twice. Two files own the design, in this order:

| File | What it owns |
|---|---|
| [`application.yaml`](application.yaml) | Structured **intent**: resources, fields, invariants, capabilities. No routes, no SQL, no wire format. The document you argue about. |
| [`openapi.yaml`](openapi.yaml) | The **wire contract**: OpenAPI 3.0.3, one operation per capability, one shared `Error` schema on every 4xx/5xx, UUID ids, RFC 3339 UTC timestamps, enums for every status and kind, `limit`+`cursor` pagination. |

`openapi.yaml` is the generator input; `gen/api/api.gen.go` (models, client,
gin server, strict server, embedded spec) and the UI's TypeScript client types
are **projections** of it. Never hand-edit a projection: change the spec and
regenerate.

`Register` installs the shared
[`httpserver.ValidateOpenAPIRequests`](../../pkg/httpserver/README.md) adapter
on the generated routes. It uses kin-openapi's legacy schema router and
`openapi3filter`; Gin still owns HTTP routing. Validation failures use the
contract's `Error` response, and unrelated routes on the same engine remain
unaffected. Authentication remains a caller-owned policy: this adapter's
current loopback composition uses no-op schema authentication.

Resources: `Session` (id, displayName, model, workingDirectory, status of
`starting|idle|running|ended|failed`, createdAt, updatedAt, lastTurnAt?,
turnCount), `Turn`, `TranscriptItem` (sequence-numbered, assistant deltas
collapsed), `SessionRequest` (exact-identity tool permission) and the
read-only `Model` list the CLI reports.

### Streaming transports

`GET /v1/sessions/{sessionId}/events` is Server-Sent Events. The generated
strict server cannot produce a streaming body, so its transport handler is
written by hand — against the `SessionEvent` schema declared in the same spec,
so its envelope `{seq, sessionId, kind, occurredAt, payload}` is still
generated into Go and TypeScript. Clients resume with `Last-Event-ID`.
Redocly reports `SessionEvent` as an unused component for exactly this reason;
that warning is expected and correct.

The live Kanban uses gotth-live's WebSocket handler at `/v1/kanban/live` and
server-rendered markup at `/v1/kanban/view`. Both mount on the existing host
router. Cards are keyed widget instances; committed adapter writes notify them
in memory, without a polling timer. Task columns come from continuity
checkpoints, independently of whether the associated agent session is idle.

The [widget integration guide](widget-ui/README.md) explains task associations,
checkpoint requirements and explicit refresh of external issue changes. Given
an existing adapter and router, mounting and shutdown look like:

```go
board, err := kanban.NewBoard(adapter, []string{"https://workbench.example.invalid"}, logger)
if err != nil {
    return err
}
board.Register(router)
// At host shutdown, after stopping new requests:
return board.Close(shutdownContext)
```

## Regenerate

```bash
# From this directory; both commands use pinned generator containers.
bash generate.sh write
bash generate.sh check
```

The [shared generator image](../../tools/oapi-codegen/Dockerfile) pins
oapi-codegen v2.8.0 on Go 1.26.5. `check` regenerates into a temporary directory
and compares the output without changing the checked-in bindings.

Validate the spec before regenerating:

```bash
docker run --rm --user "$(id -u):$(id -g)" -e HOME=/tmp \
  -e npm_config_cache=/tmp/npm -v "$PWD:/spec" -w /spec node:22-bookworm \
  bash -c 'npx --yes @redocly/cli@1.34.3 lint openapi.yaml'
```

## Run it as a plain binary

The adapter has no `cmd/` or flags of its own. The checked-in
[`app/csf/cmd/main.go`](../../app/csf/cmd/main.go) is the
complete, compile-checked composition: it owns configuration, applies
the adapter and cron migrations, constructs every required bridge, store,
worktree, terminal and schedule dependency, mounts the adapter into Gin, and
owns the listener and signal lifecycle.

Use Go 1.26.5, Node 22, an available PostgreSQL database, an authenticated
Copilot CLI, and a Git checkout for sessions. Keep a database configuration file
outside the checkout with this shape, replacing the example credentials:

```json
{"url":"postgres://user:password@127.0.0.1:5432/copilot_adapter?sslmode=disable"}
```

From the public module root containing `go.mod`, replace the absolute paths and
run:

```bash
(cd services/copilot-adapter/ui && npm ci && VITE_CSF_DASHBOARD_URL=/ npm run build)
go run ./app/csf/cmd serve \
  --workbench-database-config /absolute/path/to/workbench-database.json \
  --workbench-repository /absolute/path/to/repository \
  --workbench-ui ./services/copilot-adapter/ui/dist
```

Open `http://127.0.0.1:14111/ui/`; `/healthz` is the health endpoint. The
composition mounts the live board. Its `--listen` and `--origin` defaults are
`127.0.0.1:14111` and `http://127.0.0.1:14111`; when changing the address or using
a proxy, set `--origin` to the actual browser origin. An optional
`--workbench-token-file` supplies a token to the Copilot bridge and the HTTP
GitHub task source. A consumer embedding the Workbench supplies its own
`workbench.WithTaskContinuity(...)` for checkpoint-backed moves.

A container is one **deployment option** for that binary, never the definition
of the service.

## Implementation and tests

<!-- BEGIN BACKEND STAGE -->
### Backend

Generated first, in this order, each with a `check` mode that regenerates into a
temp dir and diffs: `generate.sh` (oapi-codegen v2.8.0 → `gen/api/api.gen.go`:
models, Gin router, strict server, client, embedded spec), `store/generate.sh`
(sqlc 1.31.1 over `store/migrations` + `store/queries.sql` → `storedb`,
database/sql flavour so `pkg/pgmem` can back it in tests), the
mockgen `//go:generate` sites for the two seams in `seams.go`, and goverter
(`views.go` → `views_gen.go`: the projection from sqlc's rows to the contract's
models, so the mapping between two generated types is generated too).

Handwritten, and only policy (every row→model mapping is generated; every
error answer goes through one typed failure and one strict middleware; nullable
columns are guregu/null values by sqlc override, so no Null* helper exists): `service.go`/`options.go` (the CS-10
library: `NewCopilotAdapter(options...)` and `Register(gin.IRouter)`, its only
mount point), `handlers.go` (the strict
interface), `sessions.go` (one owning goroutine for live CLI handles and one per
session projecting bridge events into `session_events`, `transcript_items`,
`pending_requests` and turn state), `sse.go` (the event stream over gin's own `Stream` loop and
`gin-contrib/sse` framing; the generated `text/event-stream` visitor cannot flush per frame),
`copilotbridge` (the SDK seam; needs a Copilot CLI on the host),
and `store/migrate.go` (the embedded migrations, the only schema source, applied by `pkg/sqlmigrate`). The `IStore` seam is sqlc's generated `Querier`, so it cannot drift from the queries.

Mount it into a binary's existing engine — a service never opens a listener,
never has a `cmd/`, and never ships a Dockerfile; those are one-to-one with a
binary and live in `app/`:

[`app/csf/cmd/main.go`](../../app/csf/cmd/main.go) is the
maintained reference composition. It supplies every required option and is
compiled in CI, so consumers should follow that source instead of copying a
partial constructor example that can drift out of date.

Migrations: `store.ApplyMigrations(ctx, db)` (the shared `pkg/sqlmigrate`
over the embedded `.sql` files) before `Register`.

Tests: `go test -race ./services/copilot-adapter/...` — unit specs in-package
over gomock seams through the generated client; integration specs under
`integration/` on `pgmem` with the real migrations and a mocked CLI.
<!-- END BACKEND STAGE -->

<!-- BEGIN UI STAGE -->
The browser front end is [`ui/`](ui/README.md): Vite 6 + React 18 + Mantine 8 +
TypeScript strict. It is a static bundle with no container
of its own — `npm run dev` serves it and proxies `/v1` and `/healthz` at this
host on `http://127.0.0.1:8090`; `npm run build` emits `dist/`, which
`workbench.MountUI` serves from the host's origin.

`ui/src/api/schema.d.ts` is generated from `openapi.yaml` by openapi-typescript
7.4.4 (`npm run gen`), checked for drift by `npm run gen:check`, and consumed
through one `openapi-fetch` client in `ui/src/api/client.ts`. The SSE stream is
folded by the pure reducer in `ui/src/transcript.ts`, which collapses assistant
deltas into one growing message and a toolCall/toolResult pair into one card,
and carries the `Last-Event-ID` resume point.
<!-- END UI STAGE -->
