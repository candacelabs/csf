// Package adaptertest grants the copilot adapter's specs the capabilities a
// binary grants in production, without leaving the process: the adapter's
// store on pgmem with its real schema applied, and the adapter's HTTP surface
// served by the ipc/net/http listener over an in-memory connection.
package adaptertest

import (
	"context"
	"errors"
	"fmt"

	"github.com/candacelabs/csf/ipc/db/csfpg"
	"github.com/candacelabs/csf/pkg/pgmem"
	"github.com/candacelabs/csf/pkg/sqlmigrate"
	"github.com/candacelabs/csf/services/copilot-adapter/store"
)

// IReporter is the part of a spec runner the capabilities are opened
// against: GinkgoT() and *testing.T both satisfy it.
type IReporter interface {
	Helper()
	Errorf(format string, arguments ...any)
	Fatalf(format string, arguments ...any)
	Cleanup(cleanup func())
}

// MemoryStore is the adapter's store over one pgmem database of its own.
// Close releases the database.
type MemoryStore struct {
	*store.PostgresStore
	database   *pgmem.DB
	capability *pgmem.PGX
}

// OpenMemoryStore applies the adapter's schema to a new pgmem database and
// returns the store over it.
func OpenMemoryStore(ctx context.Context) (*MemoryStore, error) {
	database, err := pgmem.NewContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("adaptertest: open pgmem: %w", err)
	}
	handle := database.Open()
	err = sqlmigrate.Apply(ctx, handle, store.Migrations, store.MigrationsDirectory)
	err = errors.Join(err, handle.Close())
	if err != nil {
		return nil, errors.Join(fmt.Errorf("adaptertest: apply the adapter's schema: %w", err), database.Close())
	}
	capability := database.Public().OpenPGX()
	persistence, err := store.NewPostgresStore(capability)
	if err != nil {
		return nil, errors.Join(err, capability.Close(), database.Close())
	}
	return &MemoryStore{PostgresStore: persistence, database: database, capability: capability}, nil
}

// OpenStore opens a store for one spec and closes it when the spec ends; a
// store that cannot be opened fails the spec.
func OpenStore(reporter IReporter) *MemoryStore {
	reporter.Helper()
	opened, err := OpenMemoryStore(context.Background())
	if err != nil {
		reporter.Fatalf("adaptertest: %v", err)
	}
	reporter.Cleanup(func() {
		if err := opened.Close(); err != nil {
			reporter.Errorf("adaptertest: close: %v", err)
		}
	})
	return opened
}

// Database is the csfpg capability the store runs on, for a spec that reads
// the rows the store wrote or binds a second store to the same database.
func (opened *MemoryStore) Database() csfpg.IDB { return opened.capability }

// Close releases the pgmem database.
func (opened *MemoryStore) Close() error {
	return errors.Join(opened.capability.Close(), opened.database.Close())
}
