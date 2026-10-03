// Package store carries the adapter's schema. The .sql files are the only
// schema source; nothing in Go declares a domain table. The binary that owns
// the pool applies them with the shared candace/pkg/sqlmigrate, beside CSF's
// own schema:
//
//	handle := pool.OpenSQL()
//	defer handle.Close()
//	err := sqlmigrate.Apply(ctx, handle, store.Migrations, store.MigrationsDirectory)
package store

import "embed"

// Migrations holds the adapter's schema. Specs apply the very same bytes
// production does.
//
//go:embed migrations/*.up.sql
var Migrations embed.FS

// MigrationsDirectory is the path the schema lives at inside Migrations; the
// //go:embed pattern above names the same directory.
const MigrationsDirectory = "migrations"
