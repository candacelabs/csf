// Copyright 2026 Candace Labs

// Package csfpgconsumer is a copyable example of an application's own
// migrations stacked on CSF's schema in one PostgreSQL database. The binary
// applies CSF's schema first, under csf_schema_version, then its own, under
// its own version table:
//
//	handle := pool.OpenSQL()
//	defer handle.Close()
//	if err := csfpg.ApplySchema(ctx, handle); err != nil { ... }
//	if err := sqlmigrate.ApplyVersioned(ctx, handle, csfpgconsumer.Migrations,
//		csfpgconsumer.MigrationDirectory, csfpgconsumer.VersionTable); err != nil { ... }
//
// Any migration tool works for the second call, provided it keeps its own
// version table. csfpg's specs load this exact directory, so the example
// stays runnable.
package csfpgconsumer

import "embed"

// Migrations holds the example consumer's migrations.
//
//go:embed migrations/*.sql
var Migrations embed.FS

// MigrationDirectory is the directory of Migrations holding the files.
const MigrationDirectory = "migrations"

// VersionTable is the example consumer's own version table.
const VersionTable = "csfpg_consumer_schema_version"
