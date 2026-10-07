// Copyright 2026 Candace Labs

// Package crontest opens the cron store on pgmem, CSF's in-process
// substitute for PostgreSQL, with CSF's real schema applied: the store a
// spec grants when it needs cron state without a database.
package crontest

import (
	"context"

	"github.com/candacelabs/csf/io/ipc/db/csfpg"
	"github.com/candacelabs/csf/pkg/pgmem"
	cron "github.com/candacelabs/csf/services/cron"
)

// IReporter is the part of a spec runner the store is opened against:
// GinkgoT() and *testing.T both satisfy it.
type IReporter interface {
	Helper()
	Fatalf(format string, arguments ...any)
	Cleanup(cleanup func())
}

// OpenStore opens a store for one spec and closes it when the spec ends; a
// store that cannot be opened fails the spec.
func OpenStore(reporter IReporter) *pgmem.PostgresStoreOnPgmem[*cron.Store] {
	reporter.Helper()
	wrapper, err := pgmem.OpenPostgresStoreOnPgmem(
		context.Background(),
		csfpg.ApplySchema,
		func(capability *pgmem.PGX) (*cron.Store, error) { return cron.NewStore(capability) },
	)
	if err != nil {
		reporter.Fatalf("crontest: %v", err)
	}
	reporter.Cleanup(func() {
		if err := wrapper.Close(); err != nil {
			reporter.Fatalf("crontest: close: %v", err)
		}
	})
	return wrapper
}
