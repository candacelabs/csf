package brainspinedb

import "embed"

// Schema is the explicit fresh-database bootstrap input. It is not silently
// reapplied or treated as an upgrade migration for an existing installation.
//
//go:embed schema.sql
var Schema string

// Migrations is the same additive schema input consumed by SQLC.
//
//go:embed migrations/*.up.sql
var Migrations embed.FS
