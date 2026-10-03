// Copyright 2026 Candace Labs

//go:build acceptance

// The acceptance tier: opt-in specs against a real, disposable PostgreSQL,
// opened only through csfpg.OpenPool. Ordinary specs use gomock doubles of
// csfpg.IDB or the in-memory pgmem substrate; see go-rules.md CS-16.

package csfpg_test

import (
	"context"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/ipc/db/csfpg"
	"github.com/candacelabs/csf/pkg/eventually"
	"github.com/candacelabs/csf/runtime/config"
)

const (
	integrationDatabaseVariable = "CANDACE_CSF_TEST_DATABASE_URL"
	searchPathParameter         = "search_path"
)

var integrationReadyBudget = eventually.Budget{Within: 20 * time.Second}

var _ = Describe("PostgreSQL capability on PostgreSQL", func() {
	It("opens a pool that serves pgx and database/sql callers until the binary closes it", func() {
		ctx := context.Background()
		settings, err := csfpg.SettingsFromEnvironment(config.OSEnvironment(), integrationDatabaseVariable)
		if err != nil {
			Skip("set " + integrationDatabaseVariable + " to run PostgreSQL capability specs")
		}
		pool := eventually.Await(GinkgoT(), "PostgreSQL capability database", integrationReadyBudget, func() *csfpg.Pool {
			pool, err := csfpg.OpenPool(ctx, settings)
			if err != nil {
				return nil
			}
			return pool
		}, func(pool *csfpg.Pool) bool { return pool != nil })

		// The one thing pgmem cannot prove: CSF's schema applies on real
		// PostgreSQL, in a fresh schema, and a second run applies nothing.
		// The spec closes pool below, so the schema is dropped through a
		// pool of its own.
		admin, err := csfpg.OpenPool(ctx, settings)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(admin.Close)
		schema := "csfpg_acceptance_" + strings.ReplaceAll(uuid.NewString(), "-", "")
		_, err = admin.Exec(ctx, "CREATE SCHEMA "+pgx.Identifier{schema}.Sanitize())
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() {
			_, err := admin.Exec(context.Background(), "DROP SCHEMA "+pgx.Identifier{schema}.Sanitize()+" CASCADE")
			Expect(err).NotTo(HaveOccurred())
		})
		location, err := url.Parse(settings.URL)
		Expect(err).NotTo(HaveOccurred())
		query := location.Query()
		query.Set(searchPathParameter, schema)
		location.RawQuery = query.Encode()
		isolated, err := csfpg.OpenPool(ctx, csfpg.Settings{URL: location.String()})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(isolated.Close)
		schemaHandle := isolated.OpenSQL()
		DeferCleanup(schemaHandle.Close)
		Expect(csfpg.ApplySchema(ctx, schemaHandle)).To(Succeed())
		Expect(csfpg.ApplySchema(ctx, schemaHandle)).To(Succeed())

		var database csfpg.IDB = pool
		transaction, err := database.Begin(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(transaction.Rollback(ctx)).To(Succeed())
		view := pool.OpenSQL()
		Expect(view.PingContext(ctx)).To(Succeed())
		Expect(view.Close()).To(Succeed())

		pool.Close()
		Expect(database.Ping(ctx)).To(HaveOccurred())
	})
})
