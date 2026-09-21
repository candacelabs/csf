# Workbench widgets and WDL

WDL means **Widget Definition Language**: the existing Widget Foundry dialect,
currently pinned to `dialect 0`. Its parser, validator, resolved representation
and generator are Go libraries. Keeping those libraries in Go permits a host to
validate agent-authored definitions in its own process.

Widgets exist within gotth-live; they are not independent application components.
The current Kanban uses gotth-live-owned generated widget instances. Each session is
one keyed instance of the definition in `kanban-card.widget`. The host subscribes
to committed adapter changes and loads retained session/task associations; one
gotth connection per browser patches only changed cards. Membership or task-column
changes replace the board region. No timer polls the Kanban and no card owns a
process, listener, socket or worker.

| Owner | Responsibility |
|---|---|
| `kanban-card.widget` | Typed card state, source event fields and widget registration. |
| `../kanban/generate.go` | Reproducible Go/templ generation from WDL. |
| `../ui/src/kanbanTemplates.tsx` | Mantine card and column presentation, emitted as Go HTML templates. |
| `../kanban` | Load authority observations, route actions, render keyed instances and close subscriptions. |
| `../openapi.yaml` and `../store` | Generated workspace API and durable session-to-task associations. |
| `copilot-adapter-pulse.widget` | Earlier aggregate-status example; not the Kanban composition. |

WDL and Mantine remain separate inputs here: the current WDL generator does not
express the whole form layout. A new WDL definition currently requires generation
and a Go build. Live definition installation and rendering without a rebuild is
planned; it is not
an implemented capability of this Kanban.

## Mount the existing library

The caller owns the configured adapter, Gin router, allowed origin and shutdown
context. The [mounting implementation](../kanban/board.go) is used like this:

```go
board, err := kanban.NewBoard(adapter, []string{"https://workbench.example.invalid"}, logger)
if err != nil {
    return err
}
board.Register(router)
// During host shutdown, after stopping new requests:
return board.Close(shutdownContext)
```

`NewBoard` constructs handlers. `Register` mounts `/v1/kanban/view` and
`/v1/kanban/live` on the supplied router. The host retains its authentication
policy. `Close` drains sessions and their subscriptions; the caller continues to
own the adapter and database. A Workbench composition can instead set
`workbench.WithKanbanOrigins(...)` and call `Workbench.Close(ctx)`.

## Associate a session with authoritative work

The [generated HTTP client](../gen/api/api.gen.go) retains explicit associations.
Generation zero means no association exists yet; subsequent writes supply the
last observed generation and reject stale updates:

```go
linked, err := client.LinkSessionTaskWithResponse(ctx, sessionID,
    api.LinkSessionTaskJSONRequestBody{
        TaskUrl: "https://github.com/example/project/issues/123",
        ExpectedGeneration: 0,
    })
if err != nil {
    return err
}
if linked.StatusCode() != http.StatusOK {
    return fmt.Errorf("link task: HTTP %d", linked.StatusCode())
}
```

The card shows the last observed continuity checkpoint, with task, checkpoint,
evidence and session links. Task status is distinct from session runtime status:
idle or ended does not imply done. Moving a card publishes and rereads a new
checkpoint against its observed predecessor; stale or competing source tips
remain visible errors. GitHub comments are not a transactional lease.

An unlinked session, missing checkpoint or unavailable authority stays in
**Needs task checkpoint**. Configure `workbench.WithTaskContinuity(...)`, link the
task and publish its initial checkpoint through the continuity API before moving
it. `POST /v1/workspace/refresh`, also available through the board's refresh
button, explicitly ingests external issue changes. There is no configured GitHub
webhook in this slice. Direct database writers must call
`adapter.InvalidateWorkspace()` after commit; adapter transactions notify
subscribers themselves.

## Reproduce the card

From the public module root containing `go.mod`, with its pinned Go toolchain:

```sh
go generate ./services/copilot-adapter/kanban
```

From `services/copilot-adapter/ui`, using the pinned Node dependencies:

```sh
npm ci
npm run gen
npm run gen:check
npm run build
```

The [consumer integration specs](../integration/workspace_test.go) exercise the
real migrations, generated HTTP client, shared adapter and actual gotth sockets:

```go
first := livetest.NewClient(GinkgoTB(), router,
    livetest.ClientOptions{Path: kanban.LivePath, Origin: allowedOrigin})
second := livetest.NewClient(GinkgoTB(), router,
    livetest.ClientOptions{Path: kanban.LivePath, Origin: allowedOrigin})
// A committed task association must reach both clients without a polling tick.
```
