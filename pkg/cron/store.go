package cron

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"sync"
	"time"
)

var (
	// ErrInvalidConfiguration reports a constructor or persisted-definition
	// value that cannot form a safe scheduler.
	ErrInvalidConfiguration = errors.New("cron: invalid configuration")
	// ErrStoreRequired reports a Service constructed without an explicit IStore.
	ErrStoreRequired = errors.New("cron: store is required")
	// ErrNoJobs reports a Service constructed without any static jobs.
	ErrNoJobs = errors.New("cron: at least one job is required")
	// ErrJobNotFound reports a store operation for a job absent from the latest
	// static reconciliation.
	ErrJobNotFound = errors.New("cron: job not found")
	// ErrLeaseLost reports a stale or expired lease token. It is a fencing error:
	// the caller must not continue acting as the occurrence owner.
	ErrLeaseLost = errors.New("cron: occurrence lease lost")
	// ErrOccurrenceConflict reports reused occurrence identity with different
	// job or scheduled-time data.
	ErrOccurrenceConflict = errors.New("cron: occurrence identity conflict")
	// ErrOccurrenceRunning reports an attempt to skip an occurrence that is
	// already executing.
	ErrOccurrenceRunning = errors.New("cron: occurrence is running")
	// ErrAlreadyRunning reports concurrent Run calls on one Service.
	ErrAlreadyRunning = errors.New("cron: service is already running")
)

const (
	maxLeaseOwnerBytes = 128
	maxLeaseTokenBytes = 256
	maxStoreTextBytes  = 4096

	// SnapshotOccurrenceLimit bounds the recent occurrence history returned by
	// IStore.Snapshot and the read-only status route.
	SnapshotOccurrenceLimit = 1_000
)

// JobDefinition is the static, persistence-neutral declaration reconciled at
// startup. Adapters map it to their own SQLC or wire types at the boundary.
type JobDefinition struct {
	Name     string             `json:"name"`
	Schedule ScheduleDefinition `json:"schedule"`
	CatchUp  CatchUpPolicy      `json:"catch_up"`
	Overlap  OverlapPolicy      `json:"overlap"`
}

// JobState is the durable scheduling cursor for one active definition.
type JobState struct {
	Definition JobDefinition `json:"definition"`
	NextRunAt  time.Time     `json:"next_run_at"`
	CreatedAt  time.Time     `json:"created_at"`
	UpdatedAt  time.Time     `json:"updated_at"`
}

// OccurrenceStatus is the durable terminal or running state of an occurrence.
type OccurrenceStatus string

const (
	OccurrenceRunning   OccurrenceStatus = "running"
	OccurrenceSucceeded OccurrenceStatus = "succeeded"
	OccurrenceFailed    OccurrenceStatus = "failed"
	OccurrenceCanceled  OccurrenceStatus = "canceled"
	OccurrenceSkipped   OccurrenceStatus = "skipped"
)

// OccurrenceRecord is the durable execution record for one scheduled instant.
// LeaseToken is a fencing secret used only by IStore implementations and is
// deliberately omitted from JSON snapshots.
type OccurrenceRecord struct {
	ID           string           `json:"id"`
	JobName      string           `json:"job_name"`
	ScheduledAt  time.Time        `json:"scheduled_at"`
	Status       OccurrenceStatus `json:"status"`
	Attempt      uint32           `json:"attempt"`
	StartedAt    time.Time        `json:"started_at,omitempty"`
	FinishedAt   time.Time        `json:"finished_at,omitempty"`
	LeaseOwner   string           `json:"lease_owner,omitempty"`
	LeaseToken   string           `json:"-"`
	LeaseUntil   time.Time        `json:"lease_until,omitempty"`
	Error        string           `json:"error,omitempty"`
	SkipReason   string           `json:"skip_reason,omitempty"`
	LastModified time.Time        `json:"last_modified"`
}

// ClaimDisposition explains the durable outcome of Claim.
type ClaimDisposition string

const (
	ClaimAcquired        ClaimDisposition = "acquired"
	ClaimAlreadyTerminal ClaimDisposition = "already_terminal"
	ClaimLeaseHeld       ClaimDisposition = "lease_held"
	ClaimSkippedOverlap  ClaimDisposition = "skipped_overlap"
)

// ClaimRequest atomically advances the job cursor and attempts to acquire the
// occurrence's fenced lease.
type ClaimRequest struct {
	OccurrenceID string
	JobName      string
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
	Occurrence  OccurrenceRecord
}

// LeaseRenewal extends a live lease while preserving its fencing token.
type LeaseRenewal struct {
	OccurrenceID string
	LeaseToken   string
	RenewedAt    time.Time
	LeaseUntil   time.Time
}

// Completion terminally records one acquired invocation.
type Completion struct {
	OccurrenceID string
	LeaseToken   string
	Status       OccurrenceStatus
	FinishedAt   time.Time
	Error        string
}

// SkipRequest terminally records a deliberately uninvoked occurrence and
// atomically advances its job cursor.
type SkipRequest struct {
	OccurrenceID string
	JobName      string
	ScheduledAt  time.Time
	NextRunAt    time.Time
	SkippedAt    time.Time
	Reason       string
}

// StoreSnapshot is a point-in-time copy of active jobs and at most the most
// recent SnapshotOccurrenceLimit durable occurrences.
type StoreSnapshot struct {
	Jobs        []JobState         `json:"jobs"`
	Occurrences []OccurrenceRecord `json:"occurrences"`
}

// IStore is the durable scheduler boundary. Implementations must make Claim and
// Skip atomic with the associated NextRunAt advance. Complete and Renew must
// fence on LeaseToken. Reconcile must replace the active static definition set,
// preserve an already established interval anchor, and leave abandoned runs
// discoverable through Expired without rewinding the normal job cursor.
type IStore interface {
	Reconcile(ctx context.Context, definitions []JobDefinition, now time.Time) ([]JobState, error)
	Claim(ctx context.Context, request ClaimRequest) (ClaimResult, error)
	Renew(ctx context.Context, renewal LeaseRenewal) error
	Complete(ctx context.Context, completion Completion) error
	Skip(ctx context.Context, request SkipRequest) error
	Expired(ctx context.Context, now time.Time, limit int) ([]OccurrenceRecord, error)
	Snapshot(ctx context.Context) (StoreSnapshot, error)
}

// MemoryStore is an explicit process-local IStore for tests and disposable
// services. It implements the same lease and reconciliation semantics as a
// durable adapter, but intentionally does not survive process restart.
type MemoryStore struct {
	mu          sync.RWMutex
	jobs        map[string]JobState
	occurrences map[string]OccurrenceRecord
}

// NewMemoryStore returns an empty process-local store.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		jobs:        make(map[string]JobState),
		occurrences: make(map[string]OccurrenceRecord),
	}
}

// OccurrenceID returns the stable identity for one job's scheduled instant.
func OccurrenceID(jobName string, scheduledAt time.Time) string {
	digest := sha256.Sum256([]byte(jobName + "\x00" + scheduledAt.UTC().Format(time.RFC3339Nano)))
	return "occ_" + hex.EncodeToString(digest[:])
}

// Reconcile implements static startup reconciliation for MemoryStore.
func (store *MemoryStore) Reconcile(
	ctx context.Context,
	definitions []JobDefinition,
	now time.Time,
) ([]JobState, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	now = now.UTC()
	if now.IsZero() {
		return nil, fmt.Errorf("%w: reconciliation time is required", ErrInvalidConfiguration)
	}

	store.mu.Lock()
	defer store.mu.Unlock()

	active := make(map[string]JobState, len(definitions))
	seen := make(map[string]struct{}, len(definitions))
	for _, incoming := range definitions {
		if _, duplicate := seen[incoming.Name]; duplicate {
			return nil, fmt.Errorf("%w: duplicate job %q", ErrInvalidConfiguration, incoming.Name)
		}
		seen[incoming.Name] = struct{}{}

		existing, exists := store.jobs[incoming.Name]
		if exists {
			incoming = preserveIntervalAnchor(incoming, existing.Definition)
		}
		incoming, schedule, err := normalizedDefinition(incoming, now)
		if err != nil {
			return nil, err
		}

		if !exists {
			next, err := schedule.Next(now)
			if err != nil {
				return nil, fmt.Errorf("cron: job %q initial occurrence: %w", incoming.Name, err)
			}
			active[incoming.Name] = JobState{
				Definition: incoming,
				NextRunAt:  next.UTC(),
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			continue
		}

		state := existing
		scheduleChanged := !reflect.DeepEqual(incoming.Schedule, existing.Definition.Schedule)
		definitionChanged := !reflect.DeepEqual(incoming, existing.Definition)
		state.Definition = incoming
		if scheduleChanged || state.NextRunAt.IsZero() {
			next, err := schedule.Next(now)
			if err != nil {
				return nil, fmt.Errorf("cron: job %q reconciled occurrence: %w", incoming.Name, err)
			}
			state.NextRunAt = next.UTC()
		}
		if definitionChanged || scheduleChanged {
			state.UpdatedAt = now
		}
		active[incoming.Name] = state
	}

	store.jobs = active
	return store.jobStatesLocked(), nil
}

// Claim implements atomically fenced occurrence acquisition for MemoryStore.
func (store *MemoryStore) Claim(ctx context.Context, request ClaimRequest) (ClaimResult, error) {
	if err := contextError(ctx); err != nil {
		return ClaimResult{}, err
	}
	if err := validateClaim(request); err != nil {
		return ClaimResult{}, err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	state, exists := store.jobs[request.JobName]
	if !exists {
		return ClaimResult{}, fmt.Errorf("%w: %q", ErrJobNotFound, request.JobName)
	}
	if err := validateScheduleAdvance(state, request.ScheduledAt, request.NextRunAt); err != nil {
		return ClaimResult{}, err
	}
	if existing, exists := store.occurrences[request.OccurrenceID]; exists {
		if err := sameOccurrence(existing, request.JobName, request.ScheduledAt); err != nil {
			return ClaimResult{}, err
		}
		if existing.Status != OccurrenceRunning {
			store.advanceLocked(request.JobName, request.NextRunAt, request.ClaimedAt)
			return ClaimResult{Disposition: ClaimAlreadyTerminal, Occurrence: existing}, nil
		}
		if durablyAfter(existing.LeaseUntil, request.ClaimedAt) {
			store.advanceLocked(request.JobName, request.NextRunAt, request.ClaimedAt)
			return ClaimResult{Disposition: ClaimLeaseHeld, Occurrence: existing}, nil
		}
		if state.Definition.Overlap == OverlapSkip && store.hasLiveOverlapLocked(request.JobName, request.OccurrenceID, request.ClaimedAt) {
			return ClaimResult{Disposition: ClaimLeaseHeld, Occurrence: existing}, nil
		}
		existing.Attempt++
		existing.StartedAt = request.ClaimedAt.UTC()
		existing.FinishedAt = time.Time{}
		existing.LeaseOwner = request.LeaseOwner
		existing.LeaseToken = request.LeaseToken
		existing.LeaseUntil = request.LeaseUntil.UTC()
		existing.Error = ""
		existing.SkipReason = ""
		existing.LastModified = request.ClaimedAt.UTC()
		store.occurrences[request.OccurrenceID] = existing
		store.advanceLocked(request.JobName, request.NextRunAt, request.ClaimedAt)
		return ClaimResult{Disposition: ClaimAcquired, Occurrence: existing}, nil
	}
	if !state.NextRunAt.Equal(request.ScheduledAt) {
		return ClaimResult{}, fmt.Errorf("%w: claim scheduled time does not match durable cursor", ErrOccurrenceConflict)
	}

	if state.Definition.Overlap == OverlapSkip {
		for _, occurrence := range store.occurrences {
			if occurrence.JobName == request.JobName &&
				occurrence.Status == OccurrenceRunning &&
				durablyAfter(occurrence.LeaseUntil, request.ClaimedAt) {
				skipped := OccurrenceRecord{
					ID:           request.OccurrenceID,
					JobName:      request.JobName,
					ScheduledAt:  request.ScheduledAt.UTC(),
					Status:       OccurrenceSkipped,
					FinishedAt:   request.ClaimedAt.UTC(),
					SkipReason:   "overlap",
					LastModified: request.ClaimedAt.UTC(),
				}
				store.occurrences[request.OccurrenceID] = skipped
				store.advanceLocked(request.JobName, request.NextRunAt, request.ClaimedAt)
				return ClaimResult{Disposition: ClaimSkippedOverlap, Occurrence: skipped}, nil
			}
		}
	}

	record := OccurrenceRecord{
		ID:           request.OccurrenceID,
		JobName:      request.JobName,
		ScheduledAt:  request.ScheduledAt.UTC(),
		Status:       OccurrenceRunning,
		Attempt:      1,
		StartedAt:    request.ClaimedAt.UTC(),
		LeaseOwner:   request.LeaseOwner,
		LeaseToken:   request.LeaseToken,
		LeaseUntil:   request.LeaseUntil.UTC(),
		LastModified: request.ClaimedAt.UTC(),
	}
	store.occurrences[request.OccurrenceID] = record
	store.advanceLocked(request.JobName, request.NextRunAt, request.ClaimedAt)
	return ClaimResult{Disposition: ClaimAcquired, Occurrence: record}, nil
}

// Renew implements fenced lease renewal for MemoryStore.
func (store *MemoryStore) Renew(ctx context.Context, renewal LeaseRenewal) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if renewal.OccurrenceID == "" || renewal.LeaseToken == "" || renewal.RenewedAt.IsZero() ||
		len(renewal.LeaseToken) > maxLeaseTokenBytes || renewal.LeaseUntil.IsZero() ||
		!durablyAfter(renewal.LeaseUntil, renewal.RenewedAt) {
		return fmt.Errorf("%w: invalid lease renewal", ErrInvalidConfiguration)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	record, exists := store.occurrences[renewal.OccurrenceID]
	if !exists || record.Status != OccurrenceRunning || record.LeaseToken != renewal.LeaseToken ||
		!durablyAfter(record.LeaseUntil, renewal.RenewedAt) {
		return ErrLeaseLost
	}
	record.LeaseUntil = renewal.LeaseUntil.UTC()
	record.LastModified = renewal.RenewedAt.UTC()
	store.occurrences[renewal.OccurrenceID] = record
	return nil
}

// Complete implements fenced terminal recording for MemoryStore.
func (store *MemoryStore) Complete(ctx context.Context, completion Completion) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if completion.OccurrenceID == "" || completion.LeaseToken == "" || completion.FinishedAt.IsZero() ||
		len(completion.LeaseToken) > maxLeaseTokenBytes || !completion.Status.terminalCompletion() ||
		len(completion.Error) > maxStoreTextBytes ||
		(completion.Status == OccurrenceSucceeded && completion.Error != "") {
		return fmt.Errorf("%w: invalid completion", ErrInvalidConfiguration)
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	record, exists := store.occurrences[completion.OccurrenceID]
	if !exists || record.Status != OccurrenceRunning || record.LeaseToken != completion.LeaseToken ||
		!durablyAfter(record.LeaseUntil, completion.FinishedAt) {
		return ErrLeaseLost
	}
	record.Status = completion.Status
	record.FinishedAt = completion.FinishedAt.UTC()
	record.LeaseOwner = ""
	record.LeaseToken = ""
	record.LeaseUntil = time.Time{}
	record.Error = completion.Error
	record.LastModified = completion.FinishedAt.UTC()
	store.occurrences[completion.OccurrenceID] = record
	return nil
}

// Skip implements idempotent terminal skip recording for MemoryStore.
func (store *MemoryStore) Skip(ctx context.Context, request SkipRequest) error {
	if err := contextError(ctx); err != nil {
		return err
	}
	if err := validateSkip(request); err != nil {
		return err
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	state, exists := store.jobs[request.JobName]
	if !exists {
		return fmt.Errorf("%w: %q", ErrJobNotFound, request.JobName)
	}
	if existing, exists := store.occurrences[request.OccurrenceID]; exists {
		if err := sameOccurrence(existing, request.JobName, request.ScheduledAt); err != nil {
			return err
		}
		if existing.Status != OccurrenceRunning {
			return nil
		}
		if err := validateScheduleAdvance(state, request.ScheduledAt, request.NextRunAt); err != nil {
			return err
		}
		if durablyAfter(existing.LeaseUntil, request.SkippedAt) {
			return ErrOccurrenceRunning
		}
		existing.Status = OccurrenceSkipped
		existing.FinishedAt = request.SkippedAt.UTC()
		existing.LeaseOwner = ""
		existing.LeaseToken = ""
		existing.LeaseUntil = time.Time{}
		existing.Error = ""
		existing.SkipReason = request.Reason
		existing.LastModified = request.SkippedAt.UTC()
		store.occurrences[request.OccurrenceID] = existing
		store.advanceLocked(request.JobName, request.NextRunAt, request.SkippedAt)
		return nil
	}
	if err := validateScheduleAdvance(state, request.ScheduledAt, request.NextRunAt); err != nil {
		return err
	}
	if !state.NextRunAt.Equal(request.ScheduledAt) {
		return fmt.Errorf("%w: skip scheduled time does not match durable cursor", ErrOccurrenceConflict)
	}
	store.occurrences[request.OccurrenceID] = OccurrenceRecord{
		ID:           request.OccurrenceID,
		JobName:      request.JobName,
		ScheduledAt:  request.ScheduledAt.UTC(),
		Status:       OccurrenceSkipped,
		FinishedAt:   request.SkippedAt.UTC(),
		SkipReason:   request.Reason,
		LastModified: request.SkippedAt.UTC(),
	}
	store.advanceLocked(request.JobName, request.NextRunAt, request.SkippedAt)
	return nil
}

// Expired returns a bounded stable-order view of abandoned running
// occurrences for active jobs. It does not mutate leases or scheduling
// cursors; callers recover records through Claim's normal fencing path.
func (store *MemoryStore) Expired(ctx context.Context, now time.Time, limit int) ([]OccurrenceRecord, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if now.IsZero() || limit <= 0 || limit > maxCatchUpLimit {
		return nil, fmt.Errorf("%w: expired query requires a time and positive limit", ErrInvalidConfiguration)
	}
	now = now.UTC()
	store.mu.RLock()
	defer store.mu.RUnlock()
	records := make([]OccurrenceRecord, 0)
	for _, occurrence := range store.occurrences {
		if occurrence.Status != OccurrenceRunning || durablyAfter(occurrence.LeaseUntil, now) {
			continue
		}
		if _, active := store.jobs[occurrence.JobName]; !active {
			continue
		}
		records = append(records, occurrence)
	}
	sort.Slice(records, func(left, right int) bool {
		if records[left].LeaseUntil.Equal(records[right].LeaseUntil) {
			if records[left].ScheduledAt.Equal(records[right].ScheduledAt) {
				return records[left].ID < records[right].ID
			}
			return records[left].ScheduledAt.Before(records[right].ScheduledAt)
		}
		return records[left].LeaseUntil.Before(records[right].LeaseUntil)
	})
	if len(records) > limit {
		records = records[:limit]
	}
	return records, nil
}

// Snapshot returns a defensive, stable-order copy of MemoryStore state.
func (store *MemoryStore) Snapshot(ctx context.Context) (StoreSnapshot, error) {
	if err := contextError(ctx); err != nil {
		return StoreSnapshot{}, err
	}
	store.mu.RLock()
	defer store.mu.RUnlock()
	snapshot := StoreSnapshot{Jobs: store.jobStatesLocked()}
	snapshot.Occurrences = make([]OccurrenceRecord, 0, len(store.occurrences))
	for _, occurrence := range store.occurrences {
		snapshot.Occurrences = append(snapshot.Occurrences, occurrence)
	}
	sort.Slice(snapshot.Occurrences, func(left, right int) bool {
		if snapshot.Occurrences[left].ScheduledAt.Equal(snapshot.Occurrences[right].ScheduledAt) {
			return snapshot.Occurrences[left].ID < snapshot.Occurrences[right].ID
		}
		return snapshot.Occurrences[left].ScheduledAt.Before(snapshot.Occurrences[right].ScheduledAt)
	})
	if len(snapshot.Occurrences) > SnapshotOccurrenceLimit {
		snapshot.Occurrences = snapshot.Occurrences[len(snapshot.Occurrences)-SnapshotOccurrenceLimit:]
	}
	return snapshot, nil
}

func normalizedDefinition(definition JobDefinition, now time.Time) (JobDefinition, Schedule, error) {
	if err := validateJobName(definition.Name); err != nil {
		return JobDefinition{}, Schedule{}, err
	}
	if !definition.CatchUp.valid() || !definition.Overlap.valid() {
		return JobDefinition{}, Schedule{}, fmt.Errorf("%w: job %q has invalid policies", ErrInvalidConfiguration, definition.Name)
	}
	schedule, err := ScheduleFromDefinition(definition.Schedule)
	if err != nil {
		return JobDefinition{}, Schedule{}, fmt.Errorf("%w: job %q schedule definition: %w", ErrInvalidConfiguration, definition.Name, err)
	}
	if definition.Schedule.Kind == ScheduleKindEvery && !definition.Schedule.HasAnchor {
		schedule = schedule.Anchor(now)
	}
	definition.Schedule, err = schedule.Definition()
	if err != nil {
		return JobDefinition{}, Schedule{}, fmt.Errorf("%w: job %q normalized schedule: %w", ErrInvalidConfiguration, definition.Name, err)
	}
	return definition, schedule, nil
}

func preserveIntervalAnchor(incoming, existing JobDefinition) JobDefinition {
	if incoming.Schedule.Kind != ScheduleKindEvery || incoming.Schedule.HasAnchor ||
		existing.Schedule.Kind != ScheduleKindEvery || !existing.Schedule.HasAnchor {
		return incoming
	}
	if incoming.Schedule.Interval == existing.Schedule.Interval &&
		incoming.Schedule.Timezone == existing.Schedule.Timezone &&
		incoming.Schedule.Canonical == existing.Schedule.Canonical {
		incoming.Schedule.Anchor = existing.Schedule.Anchor
		incoming.Schedule.HasAnchor = true
	}
	return incoming
}

func validateClaim(request ClaimRequest) error {
	if request.OccurrenceID == "" || request.JobName == "" || request.LeaseOwner == "" || request.LeaseToken == "" ||
		len(request.LeaseOwner) > maxLeaseOwnerBytes || len(request.LeaseToken) > maxLeaseTokenBytes ||
		request.ScheduledAt.IsZero() || request.NextRunAt.IsZero() || request.ClaimedAt.IsZero() || request.LeaseUntil.IsZero() ||
		!request.NextRunAt.After(request.ScheduledAt) || !durablyAfter(request.LeaseUntil, request.ClaimedAt) {
		return fmt.Errorf("%w: invalid occurrence claim", ErrInvalidConfiguration)
	}
	if request.OccurrenceID != OccurrenceID(request.JobName, request.ScheduledAt) {
		return fmt.Errorf("%w: claim ID is not deterministic", ErrOccurrenceConflict)
	}
	return nil
}

func validateSkip(request SkipRequest) error {
	if request.OccurrenceID == "" || request.JobName == "" || request.ScheduledAt.IsZero() ||
		request.NextRunAt.IsZero() || request.SkippedAt.IsZero() || len(request.Reason) == 0 ||
		len(request.Reason) > maxStoreTextBytes || !request.NextRunAt.After(request.ScheduledAt) {
		return fmt.Errorf("%w: invalid occurrence skip", ErrInvalidConfiguration)
	}
	if request.OccurrenceID != OccurrenceID(request.JobName, request.ScheduledAt) {
		return fmt.Errorf("%w: skip ID is not deterministic", ErrOccurrenceConflict)
	}
	return nil
}

func sameOccurrence(record OccurrenceRecord, jobName string, scheduledAt time.Time) error {
	if record.JobName != jobName || !record.ScheduledAt.Equal(scheduledAt) {
		return ErrOccurrenceConflict
	}
	return nil
}

func validateScheduleAdvance(state JobState, scheduledAt, nextRunAt time.Time) error {
	schedule, err := ScheduleFromDefinition(state.Definition.Schedule)
	if err != nil {
		return fmt.Errorf("%w: durable schedule: %v", ErrInvalidConfiguration, err)
	}
	expected, err := schedule.Next(scheduledAt)
	if err != nil {
		return fmt.Errorf("%w: durable next occurrence: %v", ErrInvalidConfiguration, err)
	}
	if !expected.Equal(nextRunAt) {
		return fmt.Errorf("%w: next run does not follow durable schedule", ErrOccurrenceConflict)
	}
	return nil
}

func (store *MemoryStore) advanceLocked(jobName string, next, updatedAt time.Time) {
	state := store.jobs[jobName]
	if state.NextRunAt.Before(next) {
		state.NextRunAt = next.UTC()
	}
	state.UpdatedAt = updatedAt.UTC()
	store.jobs[jobName] = state
}

func (store *MemoryStore) hasLiveOverlapLocked(jobName, exceptID string, now time.Time) bool {
	for _, occurrence := range store.occurrences {
		if occurrence.ID != exceptID && occurrence.JobName == jobName &&
			occurrence.Status == OccurrenceRunning && durablyAfter(occurrence.LeaseUntil, now) {
			return true
		}
	}
	return false
}

func (store *MemoryStore) jobStatesLocked() []JobState {
	states := make([]JobState, 0, len(store.jobs))
	for _, state := range store.jobs {
		states = append(states, state)
	}
	sort.Slice(states, func(left, right int) bool {
		return states[left].Definition.Name < states[right].Definition.Name
	})
	return states
}

func (status OccurrenceStatus) terminalCompletion() bool {
	switch status {
	case OccurrenceSucceeded, OccurrenceFailed, OccurrenceCanceled:
		return true
	default:
		return false
	}
}

func contextError(ctx context.Context) error {
	if ctx == nil {
		return fmt.Errorf("%w: nil context", ErrInvalidConfiguration)
	}
	return context.Cause(ctx)
}

func durablyAfter(later, earlier time.Time) bool {
	return later.UTC().Truncate(time.Microsecond).After(earlier.UTC().Truncate(time.Microsecond))
}
