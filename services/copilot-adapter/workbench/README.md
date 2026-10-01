# Shared Workbench composition

`NewWorkbench(ctx, db, WithBridge(...), WithRepository(...), ...)` composes the
existing adapter, SQLC store, cron store, worktree manager and terminal manager.
The caller owns its database, Copilot bridge, listener and process context.
`MountUI(router, directory)` serves the existing built browser bundle at `/ui/`.

Call `Register(router)` to mount the generated API and `Restore(ctx)` to reconnect
persisted sessions. Until restoration succeeds, Workbench API requests return
503 with `Retry-After`; sibling routes such as MCP remain available. Run
`Adapter.RunSchedules(ctx)` under the caller's lifecycle and call `Close(ctx)`
before closing the bridge and database. Workbench closes its live board and
adapter; the caller retains the bridge and database. The public
[CSF host](../../../app/csf/cmd/main.go) uses this composition.

The UI build accepts `VITE_CSF_DASHBOARD_URL=/` for navigation to a cohosted board.
An omitted value leaves the standalone Workbench navigation unchanged.
