// Package sqlmigrate applies a service's embedded migration files to a
// database/sql handle, so a service's store directory holds its .sql files and
// nothing else: the files are the only schema source, and this is the one
// runner that reads them.
package sqlmigrate

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"
)

const sqlLineCommentPrefix = "--"

const schemaMigrationsDDL = `CREATE TABLE IF NOT EXISTS schema_migrations (
    name TEXT PRIMARY KEY,
    applied_at TIMESTAMPTZ NOT NULL
)`

const sqlStateUniqueViolation = "23505"
const sqlStateDuplicateTable = "42P07"

const claimMigrationSQL = `INSERT INTO schema_migrations (name, applied_at)
VALUES ($1, CURRENT_TIMESTAMP)
ON CONFLICT (name) DO NOTHING`

type iSQLStateError interface {
	SQLState() string
}

// Apply brings db up to every *.up.sql file under dir in files, in name
// order, skipping the ones schema_migrations already records. Each file is
// executed one statement at a time so a driver that runs one statement per
// call (candace/pkg/pgmem in tests) applies the same bytes production does.
func Apply(ctx context.Context, db *sql.DB, files fs.FS, dir string) error {
	return ApplyPrefixed(ctx, db, files, dir, "")
}

// ApplyPrefixed applies a component schema while namespacing its migration
// receipts in a database shared with an owning application. Prefix is only a
// ledger namespace; it never changes the embedded filename or execution order.
func ApplyPrefixed(ctx context.Context, db *sql.DB, files fs.FS, dir string, prefix string) error {
	if db == nil {
		return fmt.Errorf("sqlmigrate: a database handle is required")
	}
	if err := createMigrationLedger(ctx, db); err != nil {
		return err
	}
	entries, err := fs.ReadDir(files, dir)
	if err != nil {
		return fmt.Errorf("sqlmigrate: read %s: %w", dir, err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		if strings.HasSuffix(entry.Name(), ".up.sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)
	for _, name := range names {
		receipt := name
		if prefix != "" {
			receipt = prefix + ":" + name
		}
		applied, err := recorded(ctx, db, receipt)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		body, err := fs.ReadFile(files, dir+"/"+name)
		if err != nil {
			return fmt.Errorf("sqlmigrate: read %s: %w", name, err)
		}
		if err := applyOne(ctx, db, name, receipt, Statements(string(body))); err != nil {
			return err
		}
	}
	return nil
}

func createMigrationLedger(ctx context.Context, db *sql.DB) error {
	if _, err := db.ExecContext(ctx, schemaMigrationsDDL); err == nil {
		return nil
	} else if !isConcurrentBootstrapConflict(err) {
		return fmt.Errorf("sqlmigrate: create schema_migrations: %w", err)
	}

	// PostgreSQL's IF NOT EXISTS check can race before either transaction's
	// catalog row is visible. The losing CREATE reports the resolved catalog
	// conflict, so one immediate retry observes the winner's committed table.
	if _, err := db.ExecContext(ctx, schemaMigrationsDDL); err != nil {
		return fmt.Errorf("sqlmigrate: create schema_migrations after concurrent bootstrap: %w", err)
	}
	return nil
}

func isConcurrentBootstrapConflict(err error) bool {
	var sqlState iSQLStateError
	if !errors.As(err, &sqlState) {
		return false
	}
	switch sqlState.SQLState() {
	case sqlStateUniqueViolation, sqlStateDuplicateTable:
		return true
	default:
		return false
	}
}

// applyOne claims one receipt, then runs that migration file's statements in a
// SINGLE transaction. The receipt primary key is the database-owned migration
// lock: a concurrent claimant waits for the owner to commit or roll back, then
// either skips the committed migration or becomes the new owner. The claim,
// statements, and receipt therefore land together or not at all.
func applyOne(ctx context.Context, db *sql.DB, name string, receipt string, statements []string) error {
	transaction, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlmigrate: begin %s: %w", name, err)
	}
	defer func() {
		// Undoing an already-finished transaction is a no-op, so this only
		// takes effect on the error paths below.
		_ = transaction.Rollback()
	}()
	claim, err := transaction.ExecContext(ctx, claimMigrationSQL, receipt)
	if err != nil {
		return fmt.Errorf("sqlmigrate: claim %s: %w", name, err)
	}
	claimed, err := claim.RowsAffected()
	if err != nil {
		return fmt.Errorf("sqlmigrate: inspect claim for %s: %w", name, err)
	}
	if claimed == 0 {
		return nil
	}
	for _, statement := range statements {
		if _, err := transaction.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("sqlmigrate: apply %s: %w", name, err)
		}
	}
	if err := transaction.Commit(); err != nil {
		return fmt.Errorf("sqlmigrate: finish %s: %w", name, err)
	}
	return nil
}

func recorded(ctx context.Context, db *sql.DB, name string) (bool, error) {
	var count int64
	row := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations WHERE name = $1", name)
	if err := row.Scan(&count); err != nil {
		return false, fmt.Errorf("sqlmigrate: look up %s: %w", name, err)
	}
	return count > 0, nil
}

// Statements splits one migration file into the statements it holds. The
// files hold plain DDL with no semicolons inside literals. Full-line --
// comments are removed before semicolons are interpreted as delimiters.
func Statements(body string) []string {
	lines := make([]string, 0, strings.Count(body, "\n")+1)
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(strings.TrimSpace(line), sqlLineCommentPrefix) {
			lines = append(lines, line)
		}
	}
	statements := []string{}
	for _, candidate := range strings.Split(strings.Join(lines, "\n"), ";") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		statements = append(statements, candidate)
	}
	return statements
}
