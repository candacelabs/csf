// Package store carries the adapter's schema. The .sql files are the only
// schema source; nothing in Go declares a domain table, and the runner that
// applies them is the shared candace/pkg/sqlmigrate.
package store

import (
	"context"
	"database/sql"
	"embed"

	"github.com/candacelabs/csf/pkg/sqlmigrate"
)

//go:embed migrations/*.up.sql
var migrationFiles embed.FS

// migrationsDirectory is the path the embedded schema lives at inside
// migrationFiles; the //go:embed pattern above names the same directory.
const migrationsDirectory = "migrations"

// ApplyMigrations brings db up to the embedded schema. Tests apply the very
// same bytes production does.
func ApplyMigrations(ctx context.Context, db *sql.DB) error {
	return sqlmigrate.Apply(ctx, db, migrationFiles, migrationsDirectory)
}
