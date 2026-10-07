# Shared Workbench composition

`NewWorkbench(ctx, db, WithBridge(...), WithRepository(...), WithLauncher(...), ...)`
composes the existing adapter, SQLC store, cron store, worktree manager and
terminal manager. The caller owns its database, Copilot bridge, listener and
process context: it grants the pool as a `csfpg.IDB` after applying
`store.Migrations` to it, and grants the `ipc/proc` launcher that Git and
terminal shells start through.
`MountUI(router, directory)` serves the existing built browser bundle at `/ui/`.

Call `Register(router)` to mount the generated API and `Restore(ctx)` to reconnect
persisted sessions. Until restoration succeeds, [Workbench](../../../csf/docs/generated/ontology_cgen.md#term-bench) API requests return
503 with `Retry-After`; sibling routes such as [MCP](../../../csf/docs/generated/ontology_cgen.md#term-mcp) remain available. Run
`Adapter.RunSchedules(ctx)` under the caller's lifecycle and call `Close(ctx)`
before closing the bridge and database. [Workbench](../../../csf/docs/generated/ontology_cgen.md#term-bench) closes its live board and
adapter; the caller retains the bridge and database. The public
[CSF host](../../../app/csf/cmd/main.go) uses this composition.

The UI build accepts `VITE_CSF_DASHBOARD_URL=/` for navigation to a cohosted board.
An omitted value leaves the standalone [Workbench](../../../csf/docs/generated/ontology_cgen.md#term-bench) navigation unchanged.
