package sqlmigrate_test

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/patience"

	"github.com/candacelabs/csf/pkg/sqlmigrate"
)

const sqlmigrateTestDatabaseURLEnv = "CANDACE_SQLMIGRATE_TEST_DATABASE_URL"

const concurrentMigrationBudget = 20 * time.Second
const concurrentMigrationSpecTimeout = 30 * time.Second

var claimWaitBudget = patience.Budget{Within: 20 * time.Second}

type synchronizedMigrationFS struct {
	fstest.MapFS
	target   string
	arrivals chan<- struct{}
	release  <-chan struct{}
	ctx      context.Context
}

type bootstrapCoordinator struct {
	winnerReady chan<- struct{}
	allowCommit <-chan struct{}
}

type bootstrapBarrierConnector struct {
	driver.Connector
	coordinator bootstrapCoordinator
}

type bootstrapBarrierConnection struct {
	*stdlib.Conn
	coordinator bootstrapCoordinator
	intercepted bool
}

func (files synchronizedMigrationFS) ReadFile(name string) ([]byte, error) {
	if name == files.target {
		select {
		case files.arrivals <- struct{}{}:
		case <-files.ctx.Done():
			return nil, files.ctx.Err()
		}
		select {
		case <-files.release:
		case <-files.ctx.Done():
			return nil, files.ctx.Err()
		}
	}
	return fs.ReadFile(files.MapFS, name)
}

func (connector bootstrapBarrierConnector) Connect(ctx context.Context) (driver.Conn, error) {
	connection, err := connector.Connector.Connect(ctx)
	if err != nil {
		return nil, err
	}
	postgresConnection, ok := connection.(*stdlib.Conn)
	if !ok {
		_ = connection.Close()
		return nil, fmt.Errorf("sqlmigrate test connector received %T, want *stdlib.Conn", connection)
	}
	return &bootstrapBarrierConnection{
		Conn:        postgresConnection,
		coordinator: connector.coordinator,
	}, nil
}

func (connection *bootstrapBarrierConnection) ExecContext(
	ctx context.Context,
	statement string,
	arguments []driver.NamedValue,
) (driver.Result, error) {
	if connection.intercepted ||
		len(arguments) != 0 ||
		!strings.HasPrefix(strings.TrimSpace(statement), "CREATE TABLE IF NOT EXISTS schema_migrations") {
		return connection.Conn.ExecContext(ctx, statement, arguments)
	}
	connection.intercepted = true

	transaction, err := connection.Conn.Conn().Begin(ctx)
	if err != nil {
		return nil, err
	}
	command, err := transaction.Exec(ctx, statement)
	if err != nil {
		_ = transaction.Rollback(ctx)
		return nil, err
	}
	select {
	case connection.coordinator.winnerReady <- struct{}{}:
	case <-ctx.Done():
		_ = transaction.Rollback(context.Background())
		return nil, ctx.Err()
	}
	select {
	case <-connection.coordinator.allowCommit:
	case <-ctx.Done():
		_ = transaction.Rollback(context.Background())
		return nil, ctx.Err()
	}
	if err := transaction.Commit(ctx); err != nil {
		return nil, err
	}
	return driver.RowsAffected(command.RowsAffected()), nil
}

var _ = Describe("PostgreSQL migration ownership", func() {
	var (
		databaseURL string
		isolatedURL string
		schemaName  string
		admin       *sql.DB
		db          *sql.DB
	)

	BeforeEach(func(ctx SpecContext) {
		databaseURL = os.Getenv(sqlmigrateTestDatabaseURLEnv)
		if databaseURL == "" {
			Skip("set " + sqlmigrateTestDatabaseURLEnv + " to run PostgreSQL integration specs")
		}

		schemaName = fmt.Sprintf("sqlmigrate_test_%d_%d", os.Getpid(), time.Now().UnixNano())
		var err error
		isolatedURL, err = databaseURLWithSearchPath(databaseURL, schemaName, "")
		Expect(err).NotTo(HaveOccurred())

		admin, err = sql.Open("pgx", databaseURL)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(admin.Close)
		Expect(admin.PingContext(ctx)).To(Succeed())
		_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+pgx.Identifier{schemaName}.Sanitize())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func(cleanupCtx SpecContext) {
			_, dropErr := admin.ExecContext(cleanupCtx, "DROP SCHEMA "+pgx.Identifier{schemaName}.Sanitize()+" CASCADE")
			Expect(dropErr).NotTo(HaveOccurred())
		})

		db, err = sql.Open("pgx", isolatedURL)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)
		db.SetMaxOpenConns(4)
		Expect(db.PingContext(ctx)).To(Succeed())
	})

	It("bootstraps a fresh schema across two database handles", func(ctx SpecContext) {
		applicationPrefix := fmt.Sprintf("sqlmigrate_bootstrap_%d_%d_", os.Getpid(), time.Now().UnixNano())
		winnerReady := make(chan struct{}, 1)
		allowCommit := make(chan struct{})
		var allowCommitOnce sync.Once
		releaseWinner := func() {
			allowCommitOnce.Do(func() { close(allowCommit) })
		}
		DeferCleanup(releaseWinner)
		coordinator := bootstrapCoordinator{
			winnerReady: winnerReady,
			allowCommit: allowCommit,
		}

		runners := make([]*sql.DB, 0, 2)
		for runner := 0; runner < 2; runner++ {
			runnerURL, err := databaseURLWithSearchPath(
				databaseURL,
				schemaName,
				fmt.Sprintf("%s%d", applicationPrefix, runner),
			)
			Expect(err).NotTo(HaveOccurred())
			runnerDB, err := openBootstrapBarrierDatabase(runnerURL, coordinator)
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(runnerDB.Close)
			Expect(runnerDB.PingContext(ctx)).To(Succeed())
			runners = append(runners, runnerDB)
		}

		files := fstest.MapFS{
			"migrations/000001_fresh.up.sql": {Data: []byte(`CREATE TABLE fresh_bootstrap_effects (id INTEGER PRIMARY KEY);
INSERT INTO fresh_bootstrap_effects (id) VALUES (1);`)},
		}
		results := make(chan error, len(runners))
		for _, runnerDB := range runners {
			go func() {
				results <- sqlmigrate.Apply(ctx, runnerDB, files, "migrations")
			}()
		}
		Eventually(winnerReady).WithTimeout(concurrentMigrationBudget).Should(Receive())
		patience.Await(GinkgoTB(), "the second ledger bootstrap to wait on PostgreSQL's catalog", claimWaitBudget,
			func() bool {
				var waiting bool
				queryErr := admin.QueryRowContext(ctx, `SELECT EXISTS (
    SELECT 1
    FROM pg_stat_activity
    WHERE application_name LIKE $1
      AND wait_event_type = 'Lock'
      AND query LIKE 'CREATE TABLE IF NOT EXISTS schema_migrations%'
)`, applicationPrefix+"%").Scan(&waiting)
				return queryErr == nil && waiting
			},
			func(waiting bool) bool { return waiting },
		)
		releaseWinner()
		runnerResults := make([]error, 0, len(runners))
		for range runners {
			var result error
			Eventually(results).WithTimeout(concurrentMigrationBudget).Should(Receive(&result))
			runnerResults = append(runnerResults, result)
		}
		for _, result := range runnerResults {
			Expect(result).To(Succeed())
		}

		var ledgerTables int
		Expect(admin.QueryRowContext(ctx, `SELECT COUNT(*)
FROM information_schema.tables
WHERE table_schema = $1 AND table_name = 'schema_migrations'`, schemaName).Scan(&ledgerTables)).To(Succeed())
		Expect(ledgerTables).To(Equal(1))
		var effects int
		Expect(db.QueryRowContext(ctx, "SELECT COUNT(*) FROM fresh_bootstrap_effects").Scan(&effects)).To(Succeed())
		Expect(effects).To(Equal(1))
		var receipts int
		Expect(db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM schema_migrations WHERE name = $1",
			"000001_fresh.up.sql",
		).Scan(&receipts)).To(Succeed())
		Expect(receipts).To(Equal(1))
	}, SpecTimeout(concurrentMigrationSpecTimeout))

	It("lets two concurrent runners install one unapplied migration exactly once", func(ctx SpecContext) {
		bootstrapMigrationLedger(ctx, db)
		migrationName := "migrations/000001_concurrent.up.sql"
		arrivals := make(chan struct{}, 2)
		release := make(chan struct{})
		var releaseOnce sync.Once
		releaseReaders := func() {
			releaseOnce.Do(func() { close(release) })
		}
		DeferCleanup(releaseReaders)
		files := synchronizedMigrationFS{
			MapFS: fstest.MapFS{
				migrationName: {Data: []byte(`CREATE TABLE concurrent_migration_effects (id INTEGER PRIMARY KEY);
INSERT INTO concurrent_migration_effects (id) VALUES (1);`)},
			},
			target:   migrationName,
			arrivals: arrivals,
			release:  release,
			ctx:      ctx,
		}

		results := make(chan error, 2)
		for runner := 0; runner < 2; runner++ {
			go func() {
				results <- sqlmigrate.Apply(ctx, db, files, "migrations")
			}()
		}
		Eventually(arrivals).WithTimeout(concurrentMigrationBudget).Should(Receive())
		Eventually(arrivals).WithTimeout(concurrentMigrationBudget).Should(Receive())
		releaseReaders()
		Eventually(results).WithTimeout(concurrentMigrationBudget).Should(Receive(Succeed()))
		Eventually(results).WithTimeout(concurrentMigrationBudget).Should(Receive(Succeed()))

		var effects int
		Expect(db.QueryRowContext(ctx, "SELECT COUNT(*) FROM concurrent_migration_effects").Scan(&effects)).To(Succeed())
		Expect(effects).To(Equal(1))
		var receipts int
		Expect(db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM schema_migrations WHERE name = $1",
			"000001_concurrent.up.sql",
		).Scan(&receipts)).To(Succeed())
		Expect(receipts).To(Equal(1))
	}, SpecTimeout(concurrentMigrationSpecTimeout))

	It("honors cancellation while a concurrent owner holds the receipt claim", func(ctx SpecContext) {
		bootstrapMigrationLedger(ctx, db)
		migrationName := "migrations/000002_canceled.up.sql"
		receiptName := "000002_canceled.up.sql"
		blocker, err := db.BeginTx(ctx, nil)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = blocker.Rollback() })
		_, err = blocker.ExecContext(ctx, `INSERT INTO schema_migrations (name, applied_at)
VALUES ($1, CURRENT_TIMESTAMP)`, receiptName)
		Expect(err).NotTo(HaveOccurred())

		applicationName := fmt.Sprintf("sqlmigrate_cancel_%d_%d", os.Getpid(), time.Now().UnixNano())
		contenderURL, err := databaseURLWithSearchPath(databaseURL, schemaName, applicationName)
		Expect(err).NotTo(HaveOccurred())
		contender, err := sql.Open("pgx", contenderURL)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(contender.Close)
		Expect(contender.PingContext(ctx)).To(Succeed())

		arrivals := make(chan struct{}, 1)
		release := make(chan struct{})
		claimCtx, cancelClaim := context.WithCancel(ctx)
		DeferCleanup(cancelClaim)
		files := synchronizedMigrationFS{
			MapFS: fstest.MapFS{
				migrationName: {Data: []byte("CREATE TABLE canceled_migration_effects (id INTEGER PRIMARY KEY);")},
			},
			target:   migrationName,
			arrivals: arrivals,
			release:  release,
			ctx:      claimCtx,
		}
		result := make(chan error, 1)
		go func() {
			result <- sqlmigrate.Apply(claimCtx, contender, files, "migrations")
		}()
		Eventually(arrivals).WithTimeout(concurrentMigrationBudget).Should(Receive())
		close(release)

		patience.Await(GinkgoTB(), "the contender to wait on the receipt owner", claimWaitBudget,
			func() bool {
				var waiting bool
				queryErr := admin.QueryRowContext(ctx, `SELECT EXISTS (
    SELECT 1
    FROM pg_stat_activity
    WHERE application_name = $1 AND wait_event_type = 'Lock'
)`, applicationName).Scan(&waiting)
				return queryErr == nil && waiting
			},
			func(waiting bool) bool { return waiting },
		)
		cancelClaim()
		Eventually(result).WithTimeout(concurrentMigrationBudget).Should(Receive(MatchError(ContainSubstring("context canceled"))))
		Expect(blocker.Rollback()).To(Succeed())

		var receipts int
		Expect(db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM schema_migrations WHERE name = $1", receiptName,
		).Scan(&receipts)).To(Succeed())
		Expect(receipts).To(BeZero())

		plainFiles := fstest.MapFS{
			migrationName: {Data: []byte("CREATE TABLE canceled_migration_effects (id INTEGER PRIMARY KEY);")},
		}
		Expect(sqlmigrate.Apply(ctx, db, plainFiles, "migrations")).To(Succeed())
		Expect(db.QueryRowContext(ctx,
			"SELECT COUNT(*) FROM schema_migrations WHERE name = $1", receiptName,
		).Scan(&receipts)).To(Succeed())
		Expect(receipts).To(Equal(1))
	}, SpecTimeout(concurrentMigrationSpecTimeout))
})

func openBootstrapBarrierDatabase(databaseURL string, coordinator bootstrapCoordinator) (*sql.DB, error) {
	config, err := pgx.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("parse bootstrap barrier database URL: %w", err)
	}
	connector := bootstrapBarrierConnector{
		Connector:   stdlib.GetConnector(*config),
		coordinator: coordinator,
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}

func bootstrapMigrationLedger(ctx context.Context, db *sql.DB) {
	files := fstest.MapFS{
		"migrations/README.md": {Data: []byte("no migrations")},
	}
	Expect(sqlmigrate.Apply(ctx, db, files, "migrations")).To(Succeed())
}

func databaseURLWithSearchPath(databaseURL string, schemaName string, applicationName string) (string, error) {
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		return "", fmt.Errorf("parse sqlmigrate integration database URL: %w", err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		return "", fmt.Errorf("sqlmigrate integration database URL must use postgres or postgresql")
	}
	databaseName, err := url.PathUnescape(strings.TrimPrefix(parsed.EscapedPath(), "/"))
	if err != nil {
		return "", fmt.Errorf("decode sqlmigrate integration database name: %w", err)
	}
	if !strings.HasSuffix(databaseName, "_test") {
		return "", fmt.Errorf("sqlmigrate integration database name must end in _test, got %q", databaseName)
	}
	query := parsed.Query()
	query.Set("search_path", schemaName)
	if applicationName != "" {
		query.Set("application_name", applicationName)
	}
	parsed.RawQuery = query.Encode()
	return parsed.String(), nil
}
