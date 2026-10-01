# Candace cron

`cron` is a durable in-process scheduler for Candace Go services. It runs as
part of the service lifecycle; it is not a separate daemon. An `IStore` is
required, and PostgreSQL is the production implementation.

```go
import (
	"context"
	"database/sql"
	"time"

	cron "github.com/candacelabs/csf/pkg/cron"
	cronpostgres "github.com/candacelabs/csf/pkg/cron/postgres"
	"github.com/gin-gonic/gin"
	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/sync/errgroup"
)

func run(ctx context.Context, databaseURL string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return err
	}
	defer db.Close()

	store, err := cronpostgres.NewStore(db)
	if err != nil {
		return err
	}
	scheduler, err := cron.New(
		cron.WithStore(store),
		cron.WithJob(
			"daily-rollup",
			cron.Spec(cron.Daily(cron.At(3).AM())),
			buildDailyRollup,
			cron.WithCatchUp(cron.CatchUpAll),
			cron.WithOverlap(cron.OverlapAllow),
		),
		cron.WithJob(
			"cache-refresh",
			cron.Spec(cron.Every(15*time.Minute)),
			refreshCache,
		),
	)
	if err != nil {
		return err
	}

	router := gin.New()
	scheduler.Register(router.Group("/internal")) // GET /internal/cron only

	group, groupContext := errgroup.WithContext(ctx)
	group.Go(func() error { return scheduler.Run(groupContext) })
	group.Go(func() error { return serveHTTP(groupContext, router) })
	return group.Wait()
}

func buildDailyRollup(ctx context.Context, invocation cron.Invocation) error {
	// invocation.ID is the stable idempotency key for this scheduled instant.
	return nil
}
```

## Declaring a schedule

Schedules are typed builders rather than strings. Each one normalizes to a
canonical five-field cron form, and that string — not the builder call — is
what a `JobDefinition` persists and the status route reports. These pairs are
pinned by `schedule_test.go`:

| Declaration | `Canonical()` |
|---|---|
| `cron.Spec(cron.Daily(cron.At(3).PM()))` | `0 15 * * *` |
| `cron.Spec(cron.Weekly(time.Monday, cron.At(8, 30).AM()))` | `30 8 * * 1` |
| `cron.Spec(cron.Monthly(1, cron.Noon()))` | `0 12 1 * *` |
| `cron.Spec(cron.LastDayOfMonth(cron.Midnight()))` | `0 0 L * *` |
| `cron.Spec(cron.Every(15 * time.Minute))` | `@every 15m0s` |
| `cron.Spec(cron.Raw("*/15 2-3 * * 1,3"))` | `*/15 2-3 * * 1,3` |

`Schedule.String()` is the human rendering of the same declaration: the first
row reads `daily at 3:00 PM (UTC)`.

`At` returns a `MeridiemTime`, not a `TimeOfDay`, so a twelve-hour clock
declaration cannot reach a schedule until it says which half of the day it
means — the ambiguous spelling is a compile error rather than a job that fires
twelve hours off:

```go
cron.Daily(cron.At(3))        // does not compile: MeridiemTime is not a TimeOfDay
cron.Daily(cron.At(3).PM())   // 15:00
cron.Daily(cron.At24(15))     // the same instant on a 24-hour clock
```

`Spec` schedules in UTC. `Schedule.In(location)` returns a copy in another
location, and neither builder panics: an invalid declaration is carried until
`Validate`, `Canonical`, or `Next` reports it.

## Policies and their defaults

Both defaults are the conservative choice, and both are per job:

| Job option | Values | Default | Effect |
|---|---|---|---|
| `WithCatchUp` | `CatchUpNone` · `CatchUpLatest` · `CatchUpAll` | `CatchUpNone` | What to do with occurrences missed while the process was down: skip past all of them (traditional cron), run only the most recent, or run every one up to the catch-up limit. |
| `WithOverlap` | `OverlapSkip` · `OverlapAllow` | `OverlapSkip` | Whether a second occurrence may run while another holds a live lease. Enforced by the `IStore`, so it holds across processes sharing one database, not just within one. |

Service-wide options: `WithStore` (required, no implicit default),
`WithLeaseDuration` (30s; active jobs renew three times per duration),
`WithCatchUpLimit` (1,000 due occurrences per job per cycle), and
`WithLeaseOwner` for callers that already have a stable replica identity —
`New` generates a random one otherwise.

Apply the relational migration in
`postgres/migrations/000001_create_cron_jobs_and_runs.up.sql` through the
owning service's normal migration runner. Queries are generated and checked in
with:

```sh
./pkg/cron/postgres/generate.sh write
./pkg/cron/postgres/generate.sh check
```

The PostgreSQL integration suite is opt-in locally. Point it at a disposable
database whose name ends in `_test`, then run the complete cron suite from the
`candace` module root:

```sh
CANDACE_CRON_TEST_DATABASE_URL='postgresql://cron:cron@localhost:5432/candace_cron_test?sslmode=disable' \
  go test -race ./pkg/cron/...
```

The integration harness rejects database names without the `_test` suffix and
runs each suite in a unique schema that it removes afterward. It never reuses
the scheduler tables in `public`.

The PostgreSQL model is ordinary typed relational state: job definitions,
schedule columns, cursors, occurrences, attempts, and fenced leases. The
Liquid Proto messages under `cron/v1` are only portable HTTP or
messaging contracts; protobuf wire bytes are never stored in the database.

Execution is at least once. A lease can expire after the handler produced an
external side effect but before completion was recorded, so handlers must use
`Invocation.ID` as an idempotency key. `CatchUpNone`, `CatchUpLatest`, and
`CatchUpAll` control missed occurrences; `OverlapSkip` is conservative by
default, while `OverlapAllow` permits concurrent attempts. Handlers must honor
context cancellation; `Run` drains cooperative handlers before returning.

The status snapshot includes active jobs and the 1,000 most recent durable
occurrences, keeping the read-only Gin endpoint bounded as history grows.

For tests or deliberately disposable processes, opt into memory explicitly:

```go
scheduler, err := cron.New(
	cron.WithStore(cron.NewMemoryStore()),
	cron.WithJob("test-job", cron.Spec(cron.Daily(cron.Noon())), handler),
)
```

`MemoryStore` never pretends to be durable and is never selected implicitly.
