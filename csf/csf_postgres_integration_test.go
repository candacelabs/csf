//go:build acceptance

package csf_test

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/ipc/db/csfpg"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/runtime/config"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

const csfPostgresTestDatabaseURLEnvironment = "CANDACE_CSF_TEST_DATABASE_URL"

var csfPostgresDatabaseBudget = eventually.Budget{Within: 20 * time.Second}

const csfPostgresSearchPathParameter = "search_path"

type csfPostgresFixture struct {
	settings csfpg.Settings
	pool     *csfpg.Pool
	store    *csf.Postgres
}

// buildCSFPostgresFixture gives each integration spec an isolated database
// schema while preserving the caller-selected disposable PostgreSQL instance.
// The spec owns the pools, as a binary would: it opens them through
// ipc/db/csfpg and closes them when the spec ends.
func buildCSFPostgresFixture(ctx context.Context) *csfPostgresFixture {
	settings, err := csfpg.SettingsFromEnvironment(config.OSEnvironment(), csfPostgresTestDatabaseURLEnvironment)
	Expect(err).NotTo(HaveOccurred(), "set "+csfPostgresTestDatabaseURLEnvironment+" to run PostgreSQL integration specs")
	admin := eventually.Await(GinkgoT(), "CSF integration PostgreSQL", csfPostgresDatabaseBudget, func() *csfpg.Pool {
		pool, err := csfpg.OpenPool(ctx, settings)
		if err != nil {
			return nil
		}
		return pool
	}, func(pool *csfpg.Pool) bool {
		return pool != nil
	})
	DeferCleanup(admin.Close)
	schema := "csf_integration_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(func() {
		_, err := admin.Exec(ctx, "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
		Expect(err).NotTo(HaveOccurred())
	})
	location, err := url.Parse(settings.URL)
	Expect(err).NotTo(HaveOccurred())
	query := location.Query()
	query.Set(csfPostgresSearchPathParameter, schema)
	location.RawQuery = query.Encode()
	settings = csfpg.Settings{URL: location.String()}
	pool, err := csfpg.OpenPool(ctx, settings)
	Expect(err).NotTo(HaveOccurred())
	DeferCleanup(pool.Close)
	schemaHandle := pool.OpenSQL()
	DeferCleanup(schemaHandle.Close)
	Expect(csfpg.ApplySchema(ctx, schemaHandle)).To(Succeed())
	store, err := csf.NewPostgres(pool)
	Expect(err).NotTo(HaveOccurred())
	return &csfPostgresFixture{settings: settings, pool: pool, store: store}
}
