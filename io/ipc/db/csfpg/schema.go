// Copyright 2026 Candace Labs

package csfpg

import (
	"context"
	"database/sql"
	"embed"

	"github.com/candacelabs/csf/pkg/sqlmigrate"
)

// Schema holds CSF's migrations, one file per change: schema/001_init.sql
// assumes a clean database, and each later file adds what came after. They are
// applied in name order. sqlc generates this package's queries from the same
// files.
//
//go:embed schema/*.sql
var Schema embed.FS

// SchemaDirectory is the directory of Schema that holds the migrations.
const SchemaDirectory = "schema"

// SchemaVersionTable records which of CSF's migrations a database holds. An
// application that embeds CSF keeps its own migrations in its own version
// table, applied after CSF's with any tool; the two are numbered
// independently and never share a table.
const SchemaVersionTable = "csf_schema_version"

// ApplySchema brings database up to CSF's schema. It is idempotent: a file the
// version table records is never applied again. Pass a pool's OpenSQL handle.
func ApplySchema(ctx context.Context, database *sql.DB) error {
	return sqlmigrate.ApplyVersioned(ctx, database, Schema, SchemaDirectory, SchemaVersionTable)
}
