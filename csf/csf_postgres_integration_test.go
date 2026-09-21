//go:build integration

package csf_test

import (
	"context"
	"os"
	"strings"
	"time"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/pkg/patience"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const csfPostgresTestDatabaseURLEnvironment = "CANDACE_CSF_TEST_DATABASE_URL"

var csfPostgresDatabaseBudget = patience.Budget{Within: 20 * time.Second}

type csfPostgresFixture struct {
	config *pgxpool.Config
	pool   *pgxpool.Pool
	store  *csf.Postgres
}

// buildCSFPostgresFixture gives each integration spec an isolated database
// schema while preserving the caller-selected disposable PostgreSQL instance.
func buildCSFPostgresFixture(ctx context.Context) *csfPostgresFixture {
	databaseURL := os.Getenv(csfPostgresTestDatabaseURLEnvironment)
	Expect(databaseURL).NotTo(BeEmpty(), "set "+csfPostgresTestDatabaseURLEnvironment+" to run PostgreSQL integration specs")
	config, err := pgxpool.ParseConfig(databaseURL)
	Expect(err).NotTo(HaveOccurred())
	admin, err := pgxpool.NewWithConfig(ctx, config.Copy())
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(admin.Close)
	Expect(patience.Await(GinkgoT(), "CSF integration PostgreSQL", csfPostgresDatabaseBudget, func() error {
		return admin.Ping(ctx)
	}, func(err error) bool {
		return err == nil
	})).To(Succeed())
	schema := "csf_integration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		_, err := admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		Expect(err).NotTo(HaveOccurred())
	})
	config.ConnConfig.RuntimeParams["search_path"] = schema
	pool, err := pgxpool.NewWithConfig(ctx, config)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(pool.Close)
	store, err := csf.NewPostgres(pool)
	Expect(err).NotTo(HaveOccurred())
	Expect(store.Initialize(ctx)).To(Succeed())
	return &csfPostgresFixture{config: config, pool: pool, store: store}
}
