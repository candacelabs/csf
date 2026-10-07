# csfpg: CSF's PostgreSQL backend

`candace/ipc/db/csfpg` is CSF's PostgreSQL backend: the only place a CSF
process opens a PostgreSQL pool, the owner of CSF's schema and its migrations,
and the home of the queries sqlc generates against that schema. Migrations are
the only schema source. It follows the `ipc/db` rule: one package per database
backend CSF reaches, named `csf<backend>`; the stores that use it live in
`services/`.

| Exported | Role |
|---|---|
| `IDB` | The capability a [service](../../../csf/docs/generated/ontology_cgen.md#term-service) receives: sqlc's `DBTX` plus `Begin`, `BeginTx` and `Ping`. |
| `Settings`, `SettingsFromEnvironment` | What the binary opens, read from its own configuration. |
| `OpenPool`, `Pool`, `Pool.OpenSQL` | The binary opens one pool, hands it out as an `IDB`, and closes it after its [services](../../../csf/docs/generated/ontology_cgen.md#term-service) stop. |
| `ApplySchema`, `SchemaVersionTable` | CSF's one migration, its [schema](../../../csf/docs/generated/ontology_cgen.md#term-schema), applied under `csf_schema_version`. |
| `New`, `Queries` and the `Csf*` models | sqlc-generated from `queries.sql`, `jobs.sql`, `cron.sql` and `agent_configurations.sql` against the schema; a [service](../../../csf/docs/generated/ontology_cgen.md#term-service) passes its `IDB` to `New`. The query contract is in [QUERIES.md](QUERIES.md). |
| `mocks.MockIDB`, `mocks.MockTx` | Generated gomock doubles (`go generate ./ipc/db/csfpg`) for specs. |

## Schema

CSF ships exactly one migration,
[`schema/001_init.sql`](schema/001_init.sql). It assumes a clean database and
is edited in place while CSF's schema evolves. `candace/pkg/sqlmigrate`
applies it and records it in CSF's own version table, `csf_schema_version`;
`sqlc.yaml` generates this package's queries from the same file
(see [QUERIES.md](QUERIES.md)).

An [application](../../../csf/docs/generated/ontology_cgen.md#term-application) that embeds CSF keeps its own migrations, numbered
independently and recorded in its own version table. It may use any migration
tool for them. With `sqlmigrate`, the binary makes two calls on one database:

```go
handle := pool.OpenSQL()
defer handle.Close()
// CSF's schema, under csf_schema_version.
if err := csfpg.ApplySchema(ctx, handle); err != nil {
	return err
}
// The application's own migrations, under its own version table.
if err := sqlmigrate.ApplyVersioned(ctx, handle, migrations, "migrations", "myapp_schema_version"); err != nil {
	return err
}
```

[`candace/examples/csfpg-consumer/migrations/001_example_notes.sql`](../../../examples/csfpg-consumer/migrations/001_example_notes.sql)
is a copyable starting point. `schema_test.go` loads that exact directory and
stacks it on CSF's schema in one database, so the example cannot rot.

## Tests

Specs never open a real database. A unit of code that uses the database takes
`csfpg.IDB`, and its specs use `mocks.MockIDB`. Specs that need the schema
itself run it on [pgmem](../../../pkg/pgmem/README.md), loading the real
`001_init.sql` with no DDL in Go. The generated queries run on pgmem too:
`pgmem.Schema.OpenPGX` serves the pgx surface and satisfies `IDB`, so
`queries_test.go` passes it to `New` and runs one query file per spec.

The labelled exception is the opt-in **acceptance tier**: specs behind the
`acceptance` build tag that run against a disposable PostgreSQL named by
`CANDACE_CSF_TEST_DATABASE_URL` (the deploy [service](../../../csf/docs/generated/ontology_cgen.md#term-service)'s specs use
`CANDACEOS_STORE_TEST_DATABASE_URL`), opening pools only through
`csfpg.OpenPool`. It holds only what pgmem cannot yet prove: this package's
pool and `ApplySchema` on real PostgreSQL, and the deploy [service](../../../csf/docs/generated/ontology_cgen.md#term-service)'s suites under `services/deploy`, whose SQL
uses PL/pgSQL triggers, `LEFT JOIN LATERAL`, arrays and `DISTINCT ON` views.

```sh
go test -race -tags acceptance ./ipc/db/csfpg/ ./services/deploy/... ./app/deploy/...
```
