// Copyright 2026 Candace Labs

package pgmem

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/candacelabs/csf/io/ipc/db/csfpg"
)

// PostgresStoreOnPgmem wraps a store and the pgmem database it runs on,
// for specs that need to access both the store and the underlying capability
// for verification queries.
type PostgresStoreOnPgmem[S any] struct {
	Store      S
	database   *DB
	capability *PGX
}

// Database returns the csfpg capability the store runs on, for tests that
// verify the rows the store wrote.
func (wrapper *PostgresStoreOnPgmem[S]) Database() csfpg.IDB {
	return wrapper.capability
}

// Close releases the pgmem database.
func (wrapper *PostgresStoreOnPgmem[S]) Close() error {
	return errors.Join(wrapper.capability.Close(), wrapper.database.Close())
}

// OpenPostgresStoreOnPgmem applies migrations to a new pgmem database and
// creates a store over it, returning the wrapped store that holds both the
// store and database access. When Close is called on the wrapper, it closes
// both the capability and the database.
//
// The newStore callback receives the one csfpg capability the wrapper owns;
// callers pass it to their store constructor and must not close it, since the
// wrapper's Close does. A capability the callback opened for itself would
// outlive the wrapper, with its pool's connection-opener goroutine, and
// nothing would release it. When newStore fails, the capability and the
// database are already closed.
func OpenPostgresStoreOnPgmem[S any](
	ctx context.Context,
	applySchema func(ctx context.Context, database *sql.DB) error,
	newStore func(capability *PGX) (S, error),
) (*PostgresStoreOnPgmem[S], error) {
	database, err := NewContext(ctx)
	if err != nil {
		return nil, fmt.Errorf("pgmem test: open database: %w", err)
	}
	handle := database.Open()
	err = applySchema(ctx, handle)
	err = errors.Join(err, handle.Close())
	if err != nil {
		return nil, errors.Join(fmt.Errorf("pgmem test: apply schema: %w", err), database.Close())
	}
	capability := database.Public().OpenPGX()
	store, err := newStore(capability)
	if err != nil {
		return nil, errors.Join(err, capability.Close(), database.Close())
	}
	return &PostgresStoreOnPgmem[S]{Store: store, database: database, capability: capability}, nil
}
