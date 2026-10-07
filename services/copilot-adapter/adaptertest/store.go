// Copyright 2026 Candace Labs

// Package adaptertest opens the copilot-adapter store on pgmem, CSF's
// in-process substitute for PostgreSQL, with the adapter's real schema
// applied: the store a spec grants when it needs copilot-adapter state without
// a database.
package adaptertest

import (
	"context"
	"database/sql"

	"github.com/candacelabs/csf/pkg/pgmem"
	"github.com/candacelabs/csf/pkg/sqlmigrate"
	adapter "github.com/candacelabs/csf/services/copilot-adapter"
	"github.com/candacelabs/csf/services/copilot-adapter/store"
)

// IReporter is the part of a spec runner the store is opened against:
// GinkgoT() and *testing.T both satisfy it.
type IReporter interface {
	Helper()
	Errorf(format string, arguments ...any)
	Fatalf(format string, arguments ...any)
	Cleanup(cleanup func())
}

// applyAdapterSchema applies the very migrations the binary that owns the pool
// applies in production (store.Migrations). The adapter's tables are its own
// and none refers to a table of CSF's csfpg schema, so that schema is not
// applied here.
func applyAdapterSchema(ctx context.Context, database *sql.DB) error {
	return sqlmigrate.Apply(ctx, database, store.Migrations, store.MigrationsDirectory)
}

// OpenStore opens a store for one spec and closes it when the spec ends; a
// store that cannot be opened fails the spec.
func OpenStore(reporter IReporter) *pgmem.PostgresStoreOnPgmem[adapter.IStore] {
	reporter.Helper()
	wrapper, err := pgmem.OpenPostgresStoreOnPgmem(
		context.Background(),
		applyAdapterSchema,
		func(capability *pgmem.PGX) (adapter.IStore, error) {
			persistence, err := store.NewPostgresStore(capability)
			if err != nil {
				return nil, err
			}
			return persistence, nil
		},
	)
	if err != nil {
		reporter.Fatalf("adaptertest: %v", err)
	}
	reporter.Cleanup(func() {
		if err := wrapper.Close(); err != nil {
			reporter.Fatalf("adaptertest: close: %v", err)
		}
	})
	return wrapper
}
