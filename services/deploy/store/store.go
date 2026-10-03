// Package store is Deploy's durable control-plane state.
//
// The package owns the embedded migrations that define the control schema and
// the append-only activity receipts that make operator work auditable. It does
// not own a connection pool: the binary opens one through
// candace/ipc/db/csfpg, hands it to NewControlStore as the database
// capability and closes it after the store's users stop. The SQL files under
// migrations/ are the only source of schema; no Go code here or anywhere else
// in Core declares DDL.
//
// Callers may rely on NewControlStore returning only after the database
// answers and every embedded migration has been applied exactly once under an
// advisory lock, on WithTx running its function inside a single transaction,
// and on IsTransactionCommitError marking the one outcome that must not be
// reported as a failure without a durable read-back, because PostgreSQL may
// have committed after all.
//
// The generated query layer these migrations produce lives behind
// services/deploy/internal/storedb and is deliberately not part of this
// repository's public API: those relational shapes change whenever a migration
// does. Embedders reach durable state through Store.
package store

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/candacelabs/csf/ipc/db/csfpg"
	"github.com/candacelabs/csf/services/deploy/internal/storedb"
)

const transactionRollbackTimeout = 5 * time.Second

// TransactionCommitError means PostgreSQL may have committed even though the
// client did not receive a successful commit acknowledgement.
type TransactionCommitError struct {
	Cause error
}

// Error implements error.
func (e *TransactionCommitError) Error() string {
	return "committing Deploy transaction: " + e.Cause.Error()
}

// Unwrap returns the underlying commit failure.
func (e *TransactionCommitError) Unwrap() error { return e.Cause }

// IsTransactionCommitError reports whether a write needs durable read-back
// before its outcome can safely be called failed.
func IsTransactionCommitError(err error) bool {
	var commitError *TransactionCommitError
	return errors.As(err, &commitError)
}

//go:embed migrations/*.sql
var migrations embed.FS

// Store is the durable control-plane store. Queries is the root query set,
// outside any transaction; use WithTx for writes that must be atomic.
type Store struct {
	database csfpg.IDB
	Queries  *storedb.Queries
}

// StartupRecovery summarizes durable work terminalized before a new operator
// controller starts. ReceiptIDs contains one append-only receipt per recovered
// run or approval.
type StartupRecovery struct {
	InterruptedRuns           int
	InterruptedDeploymentRuns int
	ExpiredApprovals          int
	ReceiptIDs                []int64
}

// NewControlStore verifies PostgreSQL readiness through the database
// capability, applies embedded migrations, and returns the durable
// control-plane store. The store never closes the database it was given.
func NewControlStore(ctx context.Context, database csfpg.IDB) (*Store, error) {
	if database == nil {
		return nil, errors.New("opening Deploy store: a database capability is required")
	}
	if err := database.Ping(ctx); err != nil {
		return nil, fmt.Errorf("pinging Deploy database: %w", err)
	}
	if err := applyMigrations(ctx, database); err != nil {
		return nil, err
	}
	return &Store{database: database, Queries: storedb.New(database)}, nil
}

// applyMigrations runs every pending migration inside one transaction that
// holds the migration advisory lock until it ends, so concurrent Core
// processes serialize and a process that dies mid-upgrade releases the lock
// with its connection. Each file runs in its own savepoint and records its
// version there; the pending set commits together or not at all.
func applyMigrations(ctx context.Context, database csfpg.IDB) error {
	upgrade, err := database.Begin(ctx)
	if err != nil {
		return fmt.Errorf("starting Deploy migrations: %w", err)
	}
	defer func() {
		rollbackContext, cancel := context.WithTimeout(context.Background(), transactionRollbackTimeout)
		defer cancel()
		_ = upgrade.Rollback(rollbackContext)
	}()
	queries := storedb.New(upgrade)
	if err := queries.AcquireMigrationLock(ctx); err != nil {
		return fmt.Errorf("acquiring Deploy migration lock: %w", err)
	}

	registry, err := migrations.ReadFile("migrations/000_registry.sql")
	if err != nil {
		return fmt.Errorf("reading Deploy migration registry: %w", err)
	}
	if _, err := upgrade.Exec(ctx, string(registry)); err != nil {
		return fmt.Errorf("creating Deploy migration registry: %w", err)
	}
	appliedVersions, err := queries.ListAppliedMigrationVersions(ctx)
	if err != nil {
		return fmt.Errorf("listing Deploy migrations: %w", err)
	}
	applied := make(map[int32]struct{}, len(appliedVersions))
	for _, version := range appliedVersions {
		applied[version] = struct{}{}
	}
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return fmt.Errorf("listing embedded Deploy migrations: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == "000_registry.sql" || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		prefix, _, ok := strings.Cut(entry.Name(), "_")
		version, parseErr := strconv.ParseInt(prefix, 10, 32)
		if !ok || parseErr != nil || version < 1 {
			return fmt.Errorf("invalid Deploy migration filename %q", entry.Name())
		}
		if _, exists := applied[int32(version)]; exists {
			continue
		}
		contents, err := migrations.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return fmt.Errorf("reading Deploy migration %q: %w", entry.Name(), err)
		}
		tx, err := upgrade.Begin(ctx)
		if err != nil {
			return fmt.Errorf("starting Deploy migration %q: %w", entry.Name(), err)
		}
		if _, err := tx.Exec(ctx, string(contents)); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("applying Deploy migration %q: %w", entry.Name(), err)
		}
		if err := queries.WithTx(tx).RecordMigration(ctx, storedb.RecordMigrationParams{
			Version: int32(version), Name: entry.Name(),
			AppliedAt: pgtype.Timestamptz{Time: time.Now().UTC(), Valid: true},
		}); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("recording Deploy migration %q: %w", entry.Name(), err)
		}
		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("committing Deploy migration %q: %w", entry.Name(), err)
		}
	}
	if err := upgrade.Commit(ctx); err != nil {
		return fmt.Errorf("committing Deploy migrations: %w", err)
	}
	return nil
}

// Ping verifies that the durable control-plane store is reachable.
func (s *Store) Ping(ctx context.Context) error {
	if s == nil || s.database == nil {
		return fmt.Errorf("pinging Deploy database: store is closed")
	}
	if err := s.database.Ping(ctx); err != nil {
		return fmt.Errorf("pinging Deploy database: %w", err)
	}
	return nil
}

// WithTx runs one SQLC-only transaction. Keeping the callback typed prevents
// callers from embedding ad-hoc SQL in handwritten Go.
func (s *Store) WithTx(ctx context.Context, fn func(queries *storedb.Queries) error) error {
	if s == nil || s.database == nil {
		return fmt.Errorf("starting Deploy transaction: store is closed")
	}
	tx, err := s.database.BeginTx(ctx, pgx.TxOptions{})
	if err != nil {
		return fmt.Errorf("starting Deploy transaction: %w", err)
	}
	defer func() {
		rollbackContext, cancel := context.WithTimeout(context.Background(), transactionRollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackContext)
	}()
	if err := fn(s.Queries.WithTx(tx)); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return &TransactionCommitError{Cause: err}
	}
	return nil
}

// RecoverInterruptedOperatorWork atomically terminalizes nonterminal work left
// by an earlier Core process. It must run before a new controller can create
// work, making every open record at this boundary stale by definition.
func (s *Store) RecoverInterruptedOperatorWork(ctx context.Context, interruptedAt time.Time) (StartupRecovery, error) {
	if interruptedAt.IsZero() {
		return StartupRecovery{}, fmt.Errorf("recovering interrupted operator work: interruption time is required")
	}
	interruptedAt = interruptedAt.UTC()
	result := StartupRecovery{}
	err := s.WithTx(ctx, func(queries *storedb.Queries) error {
		stamp := pgtype.Timestamptz{Time: interruptedAt, Valid: true}
		runIDs, err := queries.FailInterruptedOperatorRuns(ctx, stamp)
		if err != nil {
			return fmt.Errorf("failing interrupted operator runs: %w", err)
		}
		sort.Strings(runIDs)
		deploymentRunIDs, err := queries.FailInterruptedDeploymentRuns(ctx, stamp)
		if err != nil {
			return fmt.Errorf("failing interrupted deployment runs: %w", err)
		}
		sort.Strings(deploymentRunIDs)
		approvals, err := queries.ExpirePendingApprovalsOnRestart(ctx, stamp)
		if err != nil {
			return fmt.Errorf("expiring interrupted approvals: %w", err)
		}
		sort.Slice(approvals, func(i, j int) bool { return approvals[i].ApprovalID < approvals[j].ApprovalID })

		result.InterruptedRuns = len(runIDs)
		result.InterruptedDeploymentRuns = len(deploymentRunIDs)
		result.ExpiredApprovals = len(approvals)
		result.ReceiptIDs = make([]int64, 0, len(runIDs)+len(deploymentRunIDs)+len(approvals))
		for _, runID := range runIDs {
			receiptID, err := AppendReceipt(
				ctx, queries, "operator_run", runID, "run.interrupted",
				"Agent run interrupted by Deploy restart", "", interruptedAt,
			)
			if err != nil {
				return fmt.Errorf("recording interrupted run %q: %w", runID, err)
			}
			result.ReceiptIDs = append(result.ReceiptIDs, receiptID)
		}
		for _, runID := range deploymentRunIDs {
			receiptID, err := AppendReceipt(
				ctx, queries, "deployment_run", runID, "deployment.interrupted",
				"Node outcome was unknown when Deploy restarted", "", interruptedAt,
			)
			if err != nil {
				return fmt.Errorf("recording interrupted deployment run %q: %w", runID, err)
			}
			result.ReceiptIDs = append(result.ReceiptIDs, receiptID)
		}
		for _, approval := range approvals {
			receiptID, err := AppendReceipt(
				ctx, queries, "approval", approval.ApprovalID, "approval.expired",
				"Pending approval expired during Deploy restart",
				approval.PayloadSha256, interruptedAt,
			)
			if err != nil {
				return fmt.Errorf("recording expired approval %q: %w", approval.ApprovalID, err)
			}
			result.ReceiptIDs = append(result.ReceiptIDs, receiptID)
		}
		return nil
	})
	if err != nil {
		return StartupRecovery{}, fmt.Errorf("recovering interrupted operator work: %w", err)
	}
	return result, nil
}

// AppendReceipt appends one activity receipt outside a transaction. Use the
// package-level AppendReceipt to append inside one.
func (s *Store) AppendReceipt(
	ctx context.Context,
	entityType string,
	entityID string,
	kind string,
	summary string,
	payloadSHA256 string,
	at time.Time,
) (int64, error) {
	return AppendReceipt(ctx, s.Queries, entityType, entityID, kind, summary, payloadSHA256, at)
}

// AppendReceipt appends through either the root query set or a transaction.
func AppendReceipt(
	ctx context.Context,
	queries *storedb.Queries,
	entityType string,
	entityID string,
	kind string,
	summary string,
	payloadSHA256 string,
	at time.Time,
) (int64, error) {
	return queries.AppendReceipt(ctx, storedb.AppendReceiptParams{
		EntityType:    entityType,
		EntityID:      entityID,
		Kind:          kind,
		Summary:       summary,
		PayloadSha256: optionalText(payloadSHA256),
		OccurredAt:    pgtype.Timestamptz{Time: at.UTC(), Valid: true},
	})
}

func optionalText(value string) pgtype.Text {
	return pgtype.Text{String: value, Valid: value != ""}
}
