// Copyright 2026 Candace Labs

// Package crontest opens the cron store on pgmem, CSF's in-process
// substitute for PostgreSQL, with CSF's real schema applied: the store a
// spec grants when it needs cron state without a database.
package crontest

import (
	"context"
	"errors"
	"fmt"

	"github.com/candacelabs/csf/ipc/db/csfpg"
	"github.com/candacelabs/csf/pkg/pgmem"
	cron "github.com/candacelabs/csf/services/cron"
)

// MemoryStore is the cron store over one pgmem database of its own. Close
// releases the database.
type MemoryStore struct {
	*cron.Store
	database   *pgmem.DB
	capability *pgmem.PGX
}

// OpenMemoryStore applies CSF's schema to a new pgmem database and returns
// the cron store over it.
func OpenMemoryStore(ctx context.Context) (*MemoryStore, error) {
	database, err := pgmem.NewContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("crontest: open pgmem: %w", err)
	}
	handle := database.Open()
	err = csfpg.ApplySchema(ctx, handle)
	err = errors.Join(err, handle.Close())
	if err != nil {
		return nil, errors.Join(fmt.Errorf("crontest: apply CSF's schema: %w", err), database.Close())
	}
	capability := database.Public().OpenPGX()
	store, err := cron.NewStore(capability)
	if err != nil {
		return nil, errors.Join(err, capability.Close(), database.Close())
	}
	return &MemoryStore{Store: store, database: database, capability: capability}, nil
}

// IReporter is the part of a spec runner the store is opened against:
// GinkgoT() and *testing.T both satisfy it.
type IReporter interface {
	Helper()
	Fatalf(format string, arguments ...any)
	Cleanup(cleanup func())
}

// OpenStore opens a store for one spec and closes it when the spec ends; a
// store that cannot be opened fails the spec.
func OpenStore(reporter IReporter) *MemoryStore {
	reporter.Helper()
	store, err := OpenMemoryStore(context.Background())
	if err != nil {
		reporter.Fatalf("crontest: %v", err)
	}
	reporter.Cleanup(func() {
		if err := store.Close(); err != nil {
			reporter.Fatalf("crontest: close: %v", err)
		}
	})
	return store
}

// Database is the csfpg capability the store runs on, for a spec that reads
// the rows the store wrote.
func (store *MemoryStore) Database() csfpg.IDB { return store.capability }

// Close releases the pgmem database.
func (store *MemoryStore) Close() error {
	return errors.Join(store.capability.Close(), store.database.Close())
}
