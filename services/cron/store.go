// Copyright 2026 Candace Labs

package cron

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/candacelabs/csf/io/ipc/db/csfpg"
	grammar "github.com/candacelabs/csf/pkg/cron"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=store.go -destination=mocks/mock_store.go -package=mocks

var (
	// ErrDatabaseRequired reports a store built without the database
	// capability.
	ErrDatabaseRequired = errors.New("cron: a database is required")
	// ErrTriggerNotFound reports a store operation for a trigger absent from
	// the latest reconciliation.
	ErrTriggerNotFound = errors.New("cron: trigger not found")
	// ErrLeaseLost reports a stale or expired lease token. It is a fencing
	// error: the caller must stop acting as the occurrence's owner.
	ErrLeaseLost = errors.New("cron: occurrence lease lost")
	// ErrOccurrenceConflict reports an occurrence identity reused with
	// different trigger or scheduled-time data, or a cursor the request does
	// not follow.
	ErrOccurrenceConflict = errors.New("cron: occurrence identity conflict")
	// ErrOccurrenceRunning reports an attempt to skip an occurrence that is
	// executing under a live lease.
	ErrOccurrenceRunning = errors.New("cron: occurrence is running")
)

const (
	maxLeaseTokenBytes = 256
	maxStoreTextBytes  = 4096
	// SnapshotOccurrenceLimit bounds the recent occurrence history a
	// snapshot carries.
	SnapshotOccurrenceLimit = 1_000
	skipReasonOverlap       = "overlap"
)

// ClaimDisposition is the durable outcome of a Claim.
type ClaimDisposition string

const (
	ClaimAcquired        ClaimDisposition = "acquired"
	ClaimAlreadyTerminal ClaimDisposition = "already_terminal"
	ClaimLeaseHeld       ClaimDisposition = "lease_held"
	ClaimSkippedOverlap  ClaimDisposition = "skipped_overlap"
)

// ClaimRequest atomically advances the trigger's cursor and acquires the
// occurrence's fenced lease.
type ClaimRequest struct {
	OccurrenceID string
	TriggerName  string
	ScheduledAt  time.Time
	NextRunAt    time.Time
	LeaseOwner   string
	LeaseToken   string
	ClaimedAt    time.Time
	LeaseUntil   time.Time
}

// ClaimResult is idempotent for a deterministic occurrence ID.
type ClaimResult struct {
	Disposition ClaimDisposition
	Occurrence  grammar.OccurrenceRecord
}

// LeaseRenewal extends a live lease while keeping its fencing token.
type LeaseRenewal struct {
	OccurrenceID string
	LeaseToken   string
	RenewedAt    time.Time
	LeaseUntil   time.Time
}

// Completion records the end of one acquired occurrence.
type Completion struct {
	OccurrenceID string
	LeaseToken   string
	Status       grammar.OccurrenceStatus
	FinishedAt   time.Time
	Error        string
}

// SkipRequest records an occurrence deliberately not invoked and advances
// its trigger's cursor past it.
type SkipRequest struct {
	OccurrenceID string
	TriggerName  string
	ScheduledAt  time.Time
	NextRunAt    time.Time
	SkippedAt    time.Time
	Reason       string
}

// IStore is the scheduler's durable boundary: the out-of-process store the
// triggers' cursors and occurrences live in. Claim and Skip advance the
// cursor atomically with the record they write; Complete and Renew fence on
// the lease token; Reconcile replaces the active declarations, keeps an
// established interval anchor, and leaves abandoned occurrences for
// Expired without rewinding a cursor.
type IStore interface {
	Reconcile(ctx context.Context, definitions []grammar.TriggerDefinition, now time.Time) ([]grammar.TriggerState, error)
	Claim(ctx context.Context, request ClaimRequest) (ClaimResult, error)
	Renew(ctx context.Context, renewal LeaseRenewal) error
	Complete(ctx context.Context, completion Completion) error
	Skip(ctx context.Context, request SkipRequest) error
	Expired(ctx context.Context, now time.Time, limit int) ([]grammar.OccurrenceRecord, error)
	Snapshot(ctx context.Context) (grammar.StoreSnapshot, error)
}

// Store is the IStore over CSF's PostgreSQL schema, reached through the
// csfpg capability: the csf_cron_triggers and csf_cron_occurrences tables and
// the queries csfpg generates for them. Every fenced write is one
// conditional statement, so it takes no row lock and runs unchanged on
// pgmem. It borrows the capability and never closes it.
type Store struct {
	database csfpg.IDB
	queries  *csfpg.Queries
}

var _ IStore = (*Store)(nil)

// NewStore returns the store over a pool the binary opened through
// ipc/db/csfpg, or over pgmem's IDB in a spec.
func NewStore(database csfpg.IDB) (*Store, error) {
	if database == nil {
		return nil, ErrDatabaseRequired
	}
	return &Store{database: database, queries: csfpg.New(database)}, nil
}

// transact runs work in one transaction, committed when work returns nil.
func (store *Store) transact(ctx context.Context, work func(queries *csfpg.Queries) error) error {
	transaction, err := store.database.Begin(ctx)
	if err != nil {
		return fmt.Errorf("cron: begin: %w", err)
	}
	defer func() { _ = transaction.Rollback(context.WithoutCancel(ctx)) }()
	if err := work(store.queries.WithTx(transaction)); err != nil {
		return err
	}
	if err := transaction.Commit(ctx); err != nil {
		return fmt.Errorf("cron: commit: %w", err)
	}
	return nil
}

// Reconcile makes definitions the active set: a new trigger starts at its
// first occurrence after now, a changed schedule restarts from now, a
// changed policy keeps its cursor, and a trigger no longer declared is
// disabled with its history kept.
func (store *Store) Reconcile(ctx context.Context, definitions []grammar.TriggerDefinition, now time.Time) ([]grammar.TriggerState, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if now.IsZero() {
		return nil, fmt.Errorf("%w: reconciliation time is required", ErrInvalidConfiguration)
	}
	now = now.UTC()
	var states []grammar.TriggerState
	err := store.transact(ctx, func(queries *csfpg.Queries) error {
		rows, err := queries.ListCronTriggers(ctx)
		if err != nil {
			return err
		}
		existing := make(map[string]csfpg.CsfCronTrigger, len(rows))
		for _, row := range rows {
			existing[row.TriggerName] = row
		}
		seen := make(map[string]bool, len(definitions))
		for _, incoming := range definitions {
			if seen[incoming.Name] {
				return fmt.Errorf("%w: duplicate trigger %q", ErrInvalidConfiguration, incoming.Name)
			}
			seen[incoming.Name] = true
			if err := reconcileTrigger(ctx, queries, existing, incoming, now); err != nil {
				return err
			}
		}
		for name, row := range existing {
			if row.Enabled && !seen[name] {
				if err := queries.DisableCronTrigger(ctx, csfpg.DisableCronTriggerParams{TriggerName: name, UpdatedAt: csfpg.Timestamp(now)}); err != nil {
					return err
				}
			}
		}
		final, err := queries.ListCronTriggers(ctx)
		if err != nil {
			return err
		}
		states, err = activeStates(final)
		return err
	})
	if err != nil {
		return nil, err
	}
	return states, nil
}

func reconcileTrigger(ctx context.Context, queries *csfpg.Queries, existing map[string]csfpg.CsfCronTrigger, incoming grammar.TriggerDefinition, now time.Time) error {
	row, had := existing[incoming.Name]
	active := had && row.Enabled
	var persisted grammar.TriggerDefinition
	if active {
		var err error
		persisted, err = triggerDefinition(row)
		if err != nil {
			return err
		}
		incoming = grammar.PreserveIntervalAnchor(incoming, persisted)
	}
	incoming, schedule, err := grammar.NormalizeTriggerDefinition(incoming, now)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidConfiguration, err)
	}
	scheduleChanged := !active || !reflect.DeepEqual(incoming.Schedule, persisted.Schedule)
	if active && !scheduleChanged && reflect.DeepEqual(incoming, persisted) {
		return nil
	}
	next := csfpg.Time(row.NextRunAt)
	if scheduleChanged {
		next, err = schedule.Next(now)
		if err != nil {
			return fmt.Errorf("cron: trigger %q first occurrence: %w", incoming.Name, err)
		}
	}
	_, err = queries.UpsertCronTrigger(ctx, upsertParameters(incoming, next, now))
	return err
}

// Claim acquires the occurrence's lease and advances the trigger's cursor
// in one transaction. It is idempotent on the occurrence ID: a terminal
// occurrence, a live lease and an overlap are reported, not repeated.
func (store *Store) Claim(ctx context.Context, request ClaimRequest) (ClaimResult, error) {
	if err := contextError(ctx); err != nil {
		return ClaimResult{}, err
	}
	if err := validateClaim(request); err != nil {
		return ClaimResult{}, err
	}
	var result ClaimResult
	err := store.transact(ctx, func(queries *csfpg.Queries) error {
		trigger, err := activeTriggerRow(ctx, queries, request.TriggerName)
		if err != nil {
			return err
		}
		if err := validateScheduleAdvance(trigger, request.ScheduledAt, request.NextRunAt); err != nil {
			return err
		}
		row, err := queries.GetCronOccurrence(ctx, request.OccurrenceID)
		switch {
		case err == nil:
			result, err = reclaimOccurrence(ctx, queries, trigger, row, request)
			return err
		case errors.Is(err, pgx.ErrNoRows):
			result, err = claimNewOccurrence(ctx, queries, trigger, request)
			return err
		default:
			return err
		}
	})
	if err != nil {
		return ClaimResult{}, err
	}
	return result, nil
}

func reclaimOccurrence(ctx context.Context, queries *csfpg.Queries, trigger csfpg.CsfCronTrigger, row csfpg.CsfCronOccurrence, request ClaimRequest) (ClaimResult, error) {
	if row.TriggerName != request.TriggerName || !csfpg.Time(row.ScheduledAt).Equal(request.ScheduledAt.UTC()) {
		return ClaimResult{}, ErrOccurrenceConflict
	}
	record := occurrenceRecord(row)
	if record.Status != grammar.OccurrenceRunning {
		return ClaimResult{Disposition: ClaimAlreadyTerminal, Occurrence: record}, advanceCursor(ctx, queries, request)
	}
	if durablyAfter(record.LeaseUntil, request.ClaimedAt) {
		return ClaimResult{Disposition: ClaimLeaseHeld, Occurrence: record}, advanceCursor(ctx, queries, request)
	}
	if trigger.OverlapPolicy == string(grammar.OverlapSkip) {
		live, err := liveOccurrence(ctx, queries, request)
		if err != nil {
			return ClaimResult{}, err
		}
		if live {
			return ClaimResult{Disposition: ClaimLeaseHeld, Occurrence: record}, nil
		}
	}
	if err := advanceCursor(ctx, queries, request); err != nil {
		return ClaimResult{}, err
	}
	acquired, err := queries.AcquireExpiredCronOccurrence(ctx, csfpg.AcquireExpiredCronOccurrenceParams{
		LeaseOwner: text(request.LeaseOwner), LeaseToken: text(request.LeaseToken),
		LeaseUntil: csfpg.Timestamp(request.LeaseUntil), ClaimedAt: csfpg.Timestamp(request.ClaimedAt),
		OccurrenceID: request.OccurrenceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return ClaimResult{Disposition: ClaimLeaseHeld, Occurrence: record}, nil
	}
	if err != nil {
		return ClaimResult{}, err
	}
	return ClaimResult{Disposition: ClaimAcquired, Occurrence: occurrenceRecord(acquired)}, nil
}

func claimNewOccurrence(ctx context.Context, queries *csfpg.Queries, trigger csfpg.CsfCronTrigger, request ClaimRequest) (ClaimResult, error) {
	if !csfpg.Time(trigger.NextRunAt).Equal(request.ScheduledAt.UTC()) {
		return ClaimResult{}, fmt.Errorf("%w: claim scheduled time does not match durable cursor", ErrOccurrenceConflict)
	}
	if trigger.OverlapPolicy == string(grammar.OverlapSkip) {
		live, err := liveOccurrence(ctx, queries, request)
		if err != nil {
			return ClaimResult{}, err
		}
		if live {
			skipped, err := queries.InsertSkippedCronOccurrence(ctx, csfpg.InsertSkippedCronOccurrenceParams{
				OccurrenceID: request.OccurrenceID, TriggerName: request.TriggerName,
				ScheduledAt: csfpg.Timestamp(request.ScheduledAt), SkippedAt: csfpg.Timestamp(request.ClaimedAt),
				SkipReason: text(skipReasonOverlap),
			})
			if err != nil {
				return ClaimResult{}, err
			}
			return ClaimResult{Disposition: ClaimSkippedOverlap, Occurrence: occurrenceRecord(skipped)}, advanceCursor(ctx, queries, request)
		}
	}
	running, err := queries.InsertRunningCronOccurrence(ctx, csfpg.InsertRunningCronOccurrenceParams{
		OccurrenceID: request.OccurrenceID, TriggerName: request.TriggerName,
		ScheduledAt: csfpg.Timestamp(request.ScheduledAt), LeaseOwner: text(request.LeaseOwner),
		LeaseToken: text(request.LeaseToken), LeaseUntil: csfpg.Timestamp(request.LeaseUntil),
		ClaimedAt: csfpg.Timestamp(request.ClaimedAt),
	})
	if err != nil {
		return ClaimResult{}, err
	}
	return ClaimResult{Disposition: ClaimAcquired, Occurrence: occurrenceRecord(running)}, advanceCursor(ctx, queries, request)
}

// Renew extends a live lease; a stale token or an expired lease is lost.
func (store *Store) Renew(ctx context.Context, renewal LeaseRenewal) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if renewal.OccurrenceID == "" || renewal.LeaseToken == "" || renewal.RenewedAt.IsZero() ||
		len(renewal.LeaseToken) > maxLeaseTokenBytes || renewal.LeaseUntil.IsZero() ||
		!durablyAfter(renewal.LeaseUntil, renewal.RenewedAt) {
		return fmt.Errorf("%w: invalid lease renewal", ErrInvalidConfiguration)
	}
	renewed, err := store.queries.RenewCronOccurrenceLease(ctx, csfpg.RenewCronOccurrenceLeaseParams{
		LeaseUntil: csfpg.Timestamp(renewal.LeaseUntil), RenewedAt: csfpg.Timestamp(renewal.RenewedAt),
		OccurrenceID: renewal.OccurrenceID, LeaseToken: text(renewal.LeaseToken),
	})
	if err != nil {
		return err
	}
	if renewed != 1 {
		return ErrLeaseLost
	}
	return nil
}

// Complete records the end of an acquired occurrence under its live lease.
func (store *Store) Complete(ctx context.Context, completion Completion) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if completion.OccurrenceID == "" || completion.LeaseToken == "" || completion.FinishedAt.IsZero() ||
		len(completion.LeaseToken) > maxLeaseTokenBytes || !completion.Status.Completion() ||
		len(completion.Error) > maxStoreTextBytes ||
		(completion.Status == grammar.OccurrenceSucceeded && completion.Error != "") {
		return fmt.Errorf("%w: invalid completion", ErrInvalidConfiguration)
	}
	finished, err := store.queries.FinishCronOccurrence(ctx, csfpg.FinishCronOccurrenceParams{
		Status: string(completion.Status), FinishedAt: csfpg.Timestamp(completion.FinishedAt),
		ErrorSummary: text(completion.Error), OccurrenceID: completion.OccurrenceID, LeaseToken: text(completion.LeaseToken),
	})
	if err != nil {
		return err
	}
	if finished != 1 {
		return ErrLeaseLost
	}
	return nil
}

// Skip records an occurrence that is deliberately not invoked and advances
// the cursor past it. A replay of a terminal skip changes nothing, and an
// occurrence under a live lease is refused.
func (store *Store) Skip(ctx context.Context, request SkipRequest) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := validateSkip(request); err != nil {
		return err
	}
	return store.transact(ctx, func(queries *csfpg.Queries) error {
		trigger, err := activeTriggerRow(ctx, queries, request.TriggerName)
		if err != nil {
			return err
		}
		row, err := queries.GetCronOccurrence(ctx, request.OccurrenceID)
		switch {
		case err == nil:
			if row.TriggerName != request.TriggerName || !csfpg.Time(row.ScheduledAt).Equal(request.ScheduledAt.UTC()) {
				return ErrOccurrenceConflict
			}
			if row.Status != string(grammar.OccurrenceRunning) {
				return nil
			}
			if err := validateScheduleAdvance(trigger, request.ScheduledAt, request.NextRunAt); err != nil {
				return err
			}
			if durablyAfter(csfpg.Time(row.LeaseUntil), request.SkippedAt) {
				return ErrOccurrenceRunning
			}
			if _, err := queries.SkipExpiredCronOccurrence(ctx, csfpg.SkipExpiredCronOccurrenceParams{
				SkippedAt: csfpg.Timestamp(request.SkippedAt), SkipReason: text(request.Reason), OccurrenceID: request.OccurrenceID,
			}); err != nil {
				return err
			}
		case errors.Is(err, pgx.ErrNoRows):
			if err := validateScheduleAdvance(trigger, request.ScheduledAt, request.NextRunAt); err != nil {
				return err
			}
			if !csfpg.Time(trigger.NextRunAt).Equal(request.ScheduledAt.UTC()) {
				return fmt.Errorf("%w: skip scheduled time does not match durable cursor", ErrOccurrenceConflict)
			}
			if _, err := queries.InsertSkippedCronOccurrence(ctx, csfpg.InsertSkippedCronOccurrenceParams{
				OccurrenceID: request.OccurrenceID, TriggerName: request.TriggerName,
				ScheduledAt: csfpg.Timestamp(request.ScheduledAt), SkippedAt: csfpg.Timestamp(request.SkippedAt),
				SkipReason: text(request.Reason),
			}); err != nil {
				return err
			}
		default:
			return err
		}
		return advanceCursor(ctx, queries, ClaimRequest{
			TriggerName: request.TriggerName, NextRunAt: request.NextRunAt, ClaimedAt: request.SkippedAt,
		})
	})
}

// Expired lists the abandoned running occurrences of active triggers,
// oldest expiry first, without moving a cursor; Claim reclaims each.
func (store *Store) Expired(ctx context.Context, now time.Time, limit int) ([]grammar.OccurrenceRecord, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if now.IsZero() || limit <= 0 || limit > maxCatchUpLimit {
		return nil, fmt.Errorf("%w: expired query requires a time and positive limit", ErrInvalidConfiguration)
	}
	rows, err := store.queries.ListExpiredCronOccurrences(ctx, csfpg.ListExpiredCronOccurrencesParams{
		ExpiredAt: csfpg.Timestamp(now), RowLimit: int32(limit),
	})
	if err != nil {
		return nil, err
	}
	return occurrenceRecords(rows), nil
}

// Snapshot is the active triggers and the newest SnapshotOccurrenceLimit
// occurrences, in a stable order.
func (store *Store) Snapshot(ctx context.Context) (grammar.StoreSnapshot, error) {
	if err := contextError(ctx); err != nil {
		return grammar.StoreSnapshot{}, err
	}
	var snapshot grammar.StoreSnapshot
	err := store.transact(ctx, func(queries *csfpg.Queries) error {
		rows, err := queries.ListCronTriggers(ctx)
		if err != nil {
			return err
		}
		snapshot.Triggers, err = activeStates(rows)
		if err != nil {
			return err
		}
		recent, err := queries.ListRecentCronOccurrences(ctx, SnapshotOccurrenceLimit)
		if err != nil {
			return err
		}
		slices.Reverse(recent)
		snapshot.Occurrences = occurrenceRecords(recent)
		return nil
	})
	if err != nil {
		return grammar.StoreSnapshot{}, err
	}
	return snapshot, nil
}

func activeTriggerRow(ctx context.Context, queries *csfpg.Queries, name string) (csfpg.CsfCronTrigger, error) {
	trigger, err := queries.GetCronTrigger(ctx, name)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && !trigger.Enabled) {
		return csfpg.CsfCronTrigger{}, fmt.Errorf("%w: %q", ErrTriggerNotFound, name)
	}
	return trigger, err
}

func liveOccurrence(ctx context.Context, queries *csfpg.Queries, request ClaimRequest) (bool, error) {
	_, err := queries.LiveCronOccurrence(ctx, csfpg.LiveCronOccurrenceParams{
		TriggerName: request.TriggerName, At: csfpg.Timestamp(request.ClaimedAt), ExcludedOccurrenceID: request.OccurrenceID,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

func advanceCursor(ctx context.Context, queries *csfpg.Queries, request ClaimRequest) error {
	advanced, err := queries.AdvanceCronTrigger(ctx, csfpg.AdvanceCronTriggerParams{
		NextRunAt: csfpg.Timestamp(request.NextRunAt), UpdatedAt: csfpg.Timestamp(request.ClaimedAt), TriggerName: request.TriggerName,
	})
	if err != nil {
		return err
	}
	if advanced != 1 {
		return fmt.Errorf("%w: %q", ErrTriggerNotFound, request.TriggerName)
	}
	return nil
}

func activeStates(rows []csfpg.CsfCronTrigger) ([]grammar.TriggerState, error) {
	states := make([]grammar.TriggerState, 0, len(rows))
	for _, row := range rows {
		if !row.Enabled {
			continue
		}
		definition, err := triggerDefinition(row)
		if err != nil {
			return nil, err
		}
		states = append(states, grammar.TriggerState{
			Definition: definition, NextRunAt: csfpg.Time(row.NextRunAt),
			CreatedAt: csfpg.Time(row.CreatedAt), UpdatedAt: csfpg.Time(row.UpdatedAt),
		})
	}
	return states, nil
}

func validateScheduleAdvance(trigger csfpg.CsfCronTrigger, scheduledAt, nextRunAt time.Time) error {
	persisted, err := triggerDefinition(trigger)
	if err != nil {
		return err
	}
	schedule, err := grammar.ScheduleFromDefinition(persisted.Schedule)
	if err != nil {
		return fmt.Errorf("%w: durable schedule: %w", ErrInvalidConfiguration, err)
	}
	expected, err := schedule.Next(scheduledAt)
	if err != nil {
		return fmt.Errorf("%w: durable next occurrence: %w", ErrInvalidConfiguration, err)
	}
	if !expected.Equal(nextRunAt) {
		return fmt.Errorf("%w: next run does not follow durable schedule", ErrOccurrenceConflict)
	}
	return nil
}

func validateClaim(request ClaimRequest) error {
	if request.OccurrenceID == "" || request.TriggerName == "" || request.LeaseOwner == "" || request.LeaseToken == "" ||
		len(request.LeaseOwner) > maxLeaseOwnerBytes || len(request.LeaseToken) > maxLeaseTokenBytes ||
		request.ScheduledAt.IsZero() || request.NextRunAt.IsZero() || request.ClaimedAt.IsZero() || request.LeaseUntil.IsZero() ||
		!request.NextRunAt.After(request.ScheduledAt) || !durablyAfter(request.LeaseUntil, request.ClaimedAt) {
		return fmt.Errorf("%w: invalid occurrence claim", ErrInvalidConfiguration)
	}
	if request.OccurrenceID != grammar.OccurrenceID(request.TriggerName, request.ScheduledAt) {
		return fmt.Errorf("%w: claim ID is not deterministic", ErrOccurrenceConflict)
	}
	return nil
}

func validateSkip(request SkipRequest) error {
	if request.OccurrenceID == "" || request.TriggerName == "" || request.ScheduledAt.IsZero() ||
		request.NextRunAt.IsZero() || request.SkippedAt.IsZero() || len(request.Reason) == 0 ||
		len(request.Reason) > maxStoreTextBytes || !request.NextRunAt.After(request.ScheduledAt) {
		return fmt.Errorf("%w: invalid occurrence skip", ErrInvalidConfiguration)
	}
	if request.OccurrenceID != grammar.OccurrenceID(request.TriggerName, request.ScheduledAt) {
		return fmt.Errorf("%w: skip ID is not deterministic", ErrOccurrenceConflict)
	}
	return nil
}

func upsertParameters(definition grammar.TriggerDefinition, next, now time.Time) csfpg.UpsertCronTriggerParams {
	parameters := csfpg.UpsertCronTriggerParams{
		TriggerName: definition.Name, ScheduleKind: string(definition.Schedule.Kind), Timezone: definition.Schedule.Timezone,
		NextRunAt: csfpg.Timestamp(next), CatchUpPolicy: string(definition.CatchUp), OverlapPolicy: string(definition.Overlap),
		UpdatedAt: csfpg.Timestamp(now),
	}
	switch definition.Schedule.Kind {
	case grammar.ScheduleKindRaw:
		parameters.RawExpression = text(definition.Schedule.Canonical)
	case grammar.ScheduleKindDaily, grammar.ScheduleKindWeekly, grammar.ScheduleKindMonthly, grammar.ScheduleKindLastDayOfMonth:
		parameters.LocalHour = smallint(definition.Schedule.Hour)
		parameters.LocalMinute = smallint(definition.Schedule.Minute)
	}
	if definition.Schedule.Kind == grammar.ScheduleKindWeekly {
		parameters.Weekday = smallint(int(definition.Schedule.Weekday))
	}
	if definition.Schedule.Kind == grammar.ScheduleKindMonthly {
		parameters.MonthDay = smallint(definition.Schedule.MonthDay)
	}
	if definition.Schedule.Kind == grammar.ScheduleKindEvery {
		nanoseconds := int64(definition.Schedule.Interval)
		parameters.IntervalNanoseconds = &nanoseconds
		parameters.IntervalAnchorAt = csfpg.Timestamp(definition.Schedule.Anchor)
	}
	return parameters
}

func triggerDefinition(row csfpg.CsfCronTrigger) (grammar.TriggerDefinition, error) {
	definition := grammar.TriggerDefinition{
		Name: row.TriggerName, CatchUp: grammar.CatchUpPolicy(row.CatchUpPolicy), Overlap: grammar.OverlapPolicy(row.OverlapPolicy),
		Schedule: grammar.ScheduleDefinition{Kind: grammar.ScheduleKind(row.ScheduleKind), Timezone: row.Timezone},
	}
	if row.LocalHour != nil && row.LocalMinute != nil {
		definition.Schedule.Hour = int(*row.LocalHour)
		definition.Schedule.Minute = int(*row.LocalMinute)
	}
	if row.Weekday != nil {
		definition.Schedule.Weekday = time.Weekday(*row.Weekday)
	}
	if row.MonthDay != nil {
		definition.Schedule.MonthDay = int(*row.MonthDay)
	}
	if row.IntervalNanoseconds != nil {
		definition.Schedule.Interval = time.Duration(*row.IntervalNanoseconds)
		definition.Schedule.Anchor = csfpg.Time(row.IntervalAnchorAt)
		definition.Schedule.HasAnchor = row.IntervalAnchorAt.Valid
	}
	if row.RawExpression != nil {
		definition.Schedule.Canonical = *row.RawExpression
	}
	schedule, err := grammar.ScheduleFromDefinition(definition.Schedule)
	if err != nil {
		return grammar.TriggerDefinition{}, fmt.Errorf("%w: stored trigger %q: %w", ErrInvalidConfiguration, row.TriggerName, err)
	}
	definition.Schedule, err = schedule.Definition()
	if err != nil {
		return grammar.TriggerDefinition{}, fmt.Errorf("%w: stored trigger %q: %w", ErrInvalidConfiguration, row.TriggerName, err)
	}
	return definition, nil
}

func occurrenceRecord(row csfpg.CsfCronOccurrence) grammar.OccurrenceRecord {
	return grammar.OccurrenceRecord{
		ID: row.OccurrenceID, TriggerName: row.TriggerName, ScheduledAt: csfpg.Time(row.ScheduledAt),
		Status: grammar.OccurrenceStatus(row.Status), Attempt: uint32(row.Attempt),
		StartedAt: csfpg.Time(row.StartedAt), FinishedAt: csfpg.Time(row.FinishedAt),
		LeaseOwner: value(row.LeaseOwner), LeaseToken: value(row.LeaseToken), LeaseUntil: csfpg.Time(row.LeaseUntil),
		Error: value(row.ErrorSummary), SkipReason: value(row.SkipReason), LastModified: csfpg.Time(row.UpdatedAt),
	}
}

func occurrenceRecords(rows []csfpg.CsfCronOccurrence) []grammar.OccurrenceRecord {
	records := make([]grammar.OccurrenceRecord, len(rows))
	for index, row := range rows {
		records[index] = occurrenceRecord(row)
	}
	return records
}

// text is the nullable column value of a string: empty is NULL.
func text(value string) *string {
	if value == "" {
		return nil
	}
	return &value
}

func value(column *string) string {
	if column == nil {
		return ""
	}
	return *column
}

func smallint(number int) *int16 {
	converted := int16(number)
	return &converted
}

func durablyAfter(later, earlier time.Time) bool {
	return later.UTC().Truncate(time.Microsecond).After(earlier.UTC().Truncate(time.Microsecond))
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidConfiguration)
	}
	return context.Cause(ctx)
}
