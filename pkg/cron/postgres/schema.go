package postgres

import "embed"

const migrationsDirectory = "migrations"

// MigrationSource is the embedded relational schema owned by the cron
// PostgreSQL adapter. An application can feed it to its migration runner
// without copying cron's table definitions into the application.
type MigrationSource struct {
	Files     embed.FS
	Directory string
}

//go:embed migrations/*.up.sql
var migrationFiles embed.FS

// EmbeddedMigrations returns cron's canonical PostgreSQL schema source.
func EmbeddedMigrations() MigrationSource {
	return MigrationSource{Files: migrationFiles, Directory: migrationsDirectory}
}
