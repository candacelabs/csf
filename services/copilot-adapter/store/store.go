package store

import (
	"context"
	"database/sql"
	"fmt"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

// PostgresStore is the SQLC query set plus the transaction boundary the
// adapter needs. The database pool remains owned by the mounting binary.
type PostgresStore struct {
	*storedb.Queries
	database *sql.DB
}

var _ copilotadapter.IStore = (*PostgresStore)(nil)

// NewPostgresStore binds generated queries to database.
func NewPostgresStore(database *sql.DB) (*PostgresStore, error) {
	if database == nil {
		return nil, fmt.Errorf("copilot-adapter store: nil database")
	}
	return &PostgresStore{Queries: storedb.New(database), database: database}, nil
}

// Transact runs operation against one transaction and commits only when the
// callback succeeds.
func (store *PostgresStore) Transact(ctx context.Context, operation copilotadapter.StoreTransaction) error {
	if operation == nil {
		return fmt.Errorf("copilot-adapter store: nil transaction operation")
	}
	transaction, err := store.database.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = transaction.Rollback() }()
	if err := operation(storedb.New(transaction)); err != nil {
		return err
	}
	return transaction.Commit()
}
