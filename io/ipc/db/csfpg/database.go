// Copyright 2026 Candace Labs

// Package csfpg is CSF's PostgreSQL backend: the one owner of database
// connection pools in a CSF process, CSF's schema and its migrations, and the
// queries sqlc generates against that schema. It is one of the ipc/db
// packages, one per database backend CSF reaches, each named csf<backend>;
// the stores that use it live with their services.
//
// A connection to PostgreSQL crosses the kernel I/O tier, so it is opened only
// here, by the binary that owns the process. The binary calls [OpenPool] once with [Settings] it read from its own
// configuration, hands the resulting [Pool] to each service as an [IDB], and
// closes the pool after those services stop. A service receives the [IDB] in
// its constructor and never opens or closes a pool itself; pgxpool.New and
// pgx.Connect appear nowhere else.
package csfpg

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/candacelabs/csf/runtime/config"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=database.go -destination=mocks/mock_database.go -package=mocks
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mocks/mock_pgx.go -package=mocks github.com/jackc/pgx/v5 Tx

// IDB is the database capability a service receives. Its first three methods
// are sqlc's generated DBTX, so a service hands it straight to its generated
// queries; Begin and BeginTx open transactions and Ping reports readiness.
// *pgxpool.Pool, and therefore [Pool], satisfies it.
type IDB interface {
	Exec(ctx context.Context, sql string, arguments ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, arguments ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, arguments ...any) pgx.Row
	Begin(ctx context.Context) (pgx.Tx, error)
	BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error)
	Ping(ctx context.Context) error
}

// Settings selects the database a binary opens. The JSON shape is the CSF
// database configuration file's.
type Settings struct {
	// URL is a PostgreSQL connection string or URL as pgx parses it.
	URL string `json:"url"`
}

// ErrMissingURL means the settings name no database.
var ErrMissingURL = errors.New("ipc/db/csfpg: a database URL is required")

// SettingsFromEnvironment reads the database URL from the named variable of
// the config capability. The binary declares the name.
func SettingsFromEnvironment(environment config.Environment, name string) (Settings, error) {
	settings := Settings{URL: environment.Raw(name)}
	if settings.URL == "" {
		return Settings{}, fmt.Errorf("%s: %w", name, ErrMissingURL)
	}
	return settings, nil
}

// Pool is a PostgreSQL connection pool owned by the binary that opened it.
// It embeds the pgxpool.Pool, so it satisfies [IDB]; the binary alone calls
// Close, after every service it handed the pool to has stopped.
type Pool struct {
	*pgxpool.Pool
}

// OpenPool opens a pool for settings and verifies that the database answers
// before returning it. A pool that cannot reach the database is closed and
// not returned.
func OpenPool(ctx context.Context, settings Settings) (*Pool, error) {
	if settings.URL == "" {
		return nil, ErrMissingURL
	}
	parsed, err := pgxpool.ParseConfig(settings.URL)
	if err != nil {
		// The URL may carry a password; name the failure, never the input.
		return nil, errors.New("ipc/db/csfpg: invalid database URL")
	}
	pool, err := pgxpool.NewWithConfig(ctx, parsed)
	if err != nil {
		return nil, fmt.Errorf("ipc/db/csfpg: open pool: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("ipc/db/csfpg: database did not answer: %w", err)
	}
	return &Pool{Pool: pool}, nil
}

// Transact runs work inside one transaction of database and commits it when
// work returns nil; any error rolls the transaction back and is returned. It
// is the one transaction shape every store over an [IDB] uses.
func Transact(ctx context.Context, database IDB, work func(tx pgx.Tx) error) error {
	tx, err := database.Begin(ctx)
	if err != nil {
		return fmt.Errorf("ipc/db/csfpg: begin: %w", err)
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if err := work(tx); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("ipc/db/csfpg: commit: %w", err)
	}
	return nil
}

// OpenSQL returns a database/sql handle over the same connections, for the
// consumers built on database/sql, such as the shared migration runner
// candace/pkg/sqlmigrate. The caller closes the handle before the pool.
func (pool *Pool) OpenSQL() *sql.DB {
	return stdlib.OpenDBFromPool(pool.Pool)
}
