# Cron: the trigger scheduler

`services/cron` is CSF's cron [service](../../csf/docs/generated/ontology_cgen.md#term-service): a durable in-process scheduler that
[mounts](../../csf/docs/generated/ontology_cgen.md#term-mount) into the host [runtime](../../csf/docs/generated/ontology_cgen.md#term-runtime), fires each declared trigger on its schedule and
records every occurrence through csfpg. The schedule grammar, the
human-readable declarations and their canonical five-field form, is the pure
library [`pkg/cron`](../../pkg/cron); this package is the behavior.

A trigger is a name, a schedule, a catch-up policy, an overlap policy and the
operation it invokes. Each firing is an occurrence, identified by the trigger
and its scheduled instant and run under a fenced lease. Execution is at least
once: a lease can expire after the operation produced an external effect but
before its end was recorded, so an operation uses the occurrence ID as its
idempotency key.

## Mounting

The binary opens the pool, applies CSF's schema and grants the [service](../../csf/docs/generated/ontology_cgen.md#term-service) its
store, its clock and its triggers; the host [runtime](../../csf/docs/generated/ontology_cgen.md#term-runtime) starts it, stops it and
joins it with every other [service](../../csf/docs/generated/ontology_cgen.md#term-service).

```go
import (
	"context"
	"time"

	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/io/ipc/db/csfpg"
	"github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/runtime"
	cronservice "github.com/candacelabs/csf/services/cron"
)

func run(ctx context.Context, settings csfpg.Settings) error {
	// The binary owns the pool and CSF's schema; the service borrows the pool.
	pool, err := csfpg.OpenPool(ctx, settings)
	if err != nil {
		return err
	}
	defer pool.Close()
	handle := pool.OpenSQL()
	err = csfpg.ApplySchema(ctx, handle)
	_ = handle.Close()
	if err != nil {
		return err
	}
	store, err := cronservice.NewStore(pool)
	if err != nil {
		return err
	}
	scheduler, err := cronservice.NewScheduler(
		cronservice.WithStore(store),
		cronservice.WithClock(clock.NewSystemClock()),
		cronservice.WithTrigger("daily-rollup", cron.Spec(cron.Daily(cron.At(3).AM())), buildDailyRollup,
			cronservice.WithCatchUp(cron.CatchUpLatest)),
		cronservice.WithTrigger("cache-refresh", cron.Spec(cron.Every(15*time.Minute)), refreshCache,
			cronservice.WithOverlap(cron.OverlapSkip)),
	)
	if err != nil {
		return err
	}
	host, err := runtime.NewHostRuntime(runtime.WithHostName("rollups"))
	if err != nil {
		return err
	}
	if err := host.Mount("cron", scheduler); err != nil {
		return err
	}
	// Run returns once the scheduler has stopped and the occurrence in
	// flight, canceled and recorded as such, has been joined.
	return host.Run(ctx)
}

func buildDailyRollup(ctx context.Context, occurrence cronservice.Occurrence) error {
	// occurrence.ID is the idempotency key for this scheduled instant.
	return nil
}
```

`Start` reconciles the declared triggers with the store, so a store that
cannot be read fails the [mount](../../csf/docs/generated/ontology_cgen.md#term-mount), then starts the scheduling [goroutine](../../csf/docs/generated/ontology_cgen.md#term-goroutine) on the
[scope](../../csf/docs/generated/ontology_cgen.md#term-scope) the [runtime](../../csf/docs/generated/ontology_cgen.md#term-runtime) hands it. Every occurrence runs on a [goroutine](../../csf/docs/generated/ontology_cgen.md#term-goroutine) of that same
[scope](../../csf/docs/generated/ontology_cgen.md#term-scope): canceling the [scope](../../csf/docs/generated/ontology_cgen.md#term-scope) cancels the operation in flight, and joining it
waits until that occurrence has recorded its end. A store that refuses a
record afterwards fails the [scope](../../csf/docs/generated/ontology_cgen.md#term-scope), and so the [runtime](../../csf/docs/generated/ontology_cgen.md#term-runtime), with the store's error
as the cause; an operation's own error or panic is recorded as a failed
occurrence and never stops the [service](../../csf/docs/generated/ontology_cgen.md#term-service).

## Policies and their defaults

Both defaults are the conservative choice, and both are per trigger:

| Trigger option | Values | Default | Effect |
|---|---|---|---|
| `WithCatchUp` | `CatchUpNone` · `CatchUpLatest` · `CatchUpAll` | `CatchUpNone` | What to do with occurrences missed while the process was down: skip past all of them (traditional cron), run only the most recent, or run every one up to the catch-up limit. |
| `WithOverlap` | `OverlapSkip` · `OverlapAllow` | `OverlapSkip` | Whether a second occurrence may run while another holds a live lease. Enforced by the store, so it holds across processes sharing one database, not just within one. |

Scheduler-wide options: `WithStore` (required), `WithClock` (the host's
clock by default), `WithLeaseDuration` (30s; a running occurrence renews
three times per duration), `WithCatchUpLimit` (1,000 due occurrences per
trigger per cycle), and `WithLeaseOwner` for a binary that already has a
stable replica identity; `NewScheduler` generates a random one otherwise.

`NewScheduler` rejects a duplicate trigger name, a name outside
`^[a-z][a-z0-9._/-]*$`, an invalid schedule, a nil operation, a missing store
and an empty trigger set before anything is [mounted](../../csf/docs/generated/ontology_cgen.md#term-mount).

## The store

[Cron](../../csf/docs/generated/ontology_cgen.md#term-cron) state is two tables of CSF's one migration,
[`ipc/db/csfpg/schema/001_init.sql`](../../ipc/db/csfpg/schema/001_init.sql):
`csf_cron_triggers`, one row per declared trigger with its schedule as
relational columns and its cursor, and `csf_cron_occurrences`, one row per
scheduled instant with its status, attempt and lease. The queries are
[`ipc/db/csfpg/cron.sql`](../../ipc/db/csfpg/cron.sql), generated by csfpg's
sqlc set; `NewStore` runs them over the `csfpg.IDB` capability the binary
grants. Every fenced write is one conditional statement, so the store takes no
row lock or advisory lock and the same SQL runs on pgmem.

`IStore` is the scheduler's boundary. `Claim` acquires an occurrence's lease
and advances the trigger's cursor in one transaction and is idempotent on the
occurrence ID; `Renew` and `Complete` fence on the lease token; `Skip` records
an occurrence a policy chose not to run; `Expired` lists abandoned leases for
the scheduler to reclaim; `Snapshot` is the active triggers and the 1,000
most recent occurrences. The Liquid Proto messages under `pkg/cron/v1` are
portable boundary contracts, never a storage format.

## Specs

Specs grant the [service](../../csf/docs/generated/ontology_cgen.md#term-service) a `clock.ManualClock` and the store over pgmem, and
move time themselves: `crontest.OpenStore(GinkgoT())` applies CSF's real
schema to a pgmem database and returns the store, closed when the spec ends.
The suite proves, without a sleep, that a trigger fires at its instant, that
an overlapping occurrence is skipped, that each catch-up policy does what it
says after downtime, and that a host [runtime](../../csf/docs/generated/ontology_cgen.md#term-runtime)'s shutdown joins the occurrence
in flight. pgmem keeps timestamps at second precision, so its specs use whole
seconds.

The opt-in acceptance tier runs the store's SQL on a disposable PostgreSQL,
opened only through `csfpg.OpenPool`:

```sh
CANDACE_CSF_TEST_DATABASE_URL='postgresql://csf:csf@localhost:5432/csf_test?sslmode=disable' \
  go test -race -tags acceptance ./services/cron/
```
