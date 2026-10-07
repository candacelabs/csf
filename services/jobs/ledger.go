// Copyright 2026 Candace Labs

package jobs

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/candacelabs/csf/io/ipc/db/csfpg"
	"github.com/jackc/pgx/v5"
)

// defaultMetricLimit bounds how many metrics one job may declare.
const defaultMetricLimit = 128

// JobLedger records and drives the jobs of one request type.
type JobLedger[Spec any] struct {
	database  IJobDatabase
	budget    Budget
	kinds     []string
	rules     observationRules
	executors []executorBinding[Spec]
}

// LedgerOption configures a JobLedger at construction.
type LedgerOption[Spec any] func(ledger *JobLedger[Spec]) error

// WithProgressMetric names the metric whose value is the completed units.
// Without it, progress comes only from executors.
func WithProgressMetric[Spec any](metric string) LedgerOption[Spec] {
	return func(ledger *JobLedger[Spec]) error {
		if metric == "" {
			return fmt.Errorf("%w: empty progress metric", ErrInvalidObservation)
		}
		ledger.rules.progressMetric = metric
		return nil
	}
}

// WithMetricLimit bounds how many metrics one job may declare.
func WithMetricLimit[Spec any](limit int) LedgerOption[Spec] {
	return func(ledger *JobLedger[Spec]) error {
		if limit < 1 {
			return fmt.Errorf("%w: metric limit %d", ErrInvalidObservation, limit)
		}
		ledger.rules.metricLimit = limit
		return nil
	}
}

// NewJobLedger returns the ledger for the given kinds, reserving admissions
// from budget. Options register executors; jobs admitted to an executor
// that is not registered are recorded but never driven.
func NewJobLedger[Spec any](database IJobDatabase, budget Budget, kinds []Kind, options ...LedgerOption[Spec]) (*JobLedger[Spec], error) {
	if database == nil {
		return nil, ErrDatabaseRequired
	}
	if !budgetAccount.MatchString(budget.Account) || budget.LimitUSDMicros < 0 {
		return nil, ErrInvalidBudget
	}
	ledger := &JobLedger[Spec]{database: database, budget: budget, rules: observationRules{metricLimit: defaultMetricLimit}}
	for _, kind := range kinds {
		if !kindName.MatchString(string(kind)) || slices.Contains(ledger.kinds, string(kind)) {
			return nil, ErrKindsRequired
		}
		ledger.kinds = append(ledger.kinds, string(kind))
	}
	if len(ledger.kinds) == 0 {
		return nil, ErrKindsRequired
	}
	for _, option := range options {
		if err := option(ledger); err != nil {
			return nil, err
		}
	}
	return ledger, nil
}

func (ledger *JobLedger[Spec]) serves(kind Kind) bool {
	return slices.Contains(ledger.kinds, string(kind))
}

// Submit admits a job: it reserves the budget and records the job pending,
// in one transaction. Repeating an identical admission returns the recorded
// job without reserving again.
func (ledger *JobLedger[Spec]) Submit(ctx context.Context, admission Admission[Spec]) (Job[Spec], error) {
	if err := ledger.validAdmission(admission); err != nil {
		return Job[Spec]{}, err
	}
	request, digest, err := encodeRequest(admission.Spec)
	if err != nil {
		return Job[Spec]{}, err
	}
	var recorded csfpg.CsfJob
	err = ledger.database.Transact(ctx, func(queries IJobQueries) error {
		_, err := queries.EnsureJobBudget(ctx, csfpg.EnsureJobBudgetParams{Account: ledger.budget.Account, LimitUsdMicros: ledger.budget.LimitUSDMicros})
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrBudgetChanged
		}
		if err != nil {
			return err
		}
		// The account row serializes admissions, including concurrent retries.
		if _, err = queries.LockJobBudget(ctx, ledger.budget.Account); err != nil {
			return err
		}
		existing, err := queries.GetJob(ctx, csfpg.GetJobParams{JobID: admission.ID, Kinds: ledger.kinds})
		if err == nil {
			if existing.Kind != string(admission.Kind) || existing.Executor != string(admission.Executor) || existing.TotalUnits != admission.TotalUnits || existing.RequestSha256 != digest {
				return ErrIdentityReused
			}
			recorded = existing
			return nil
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			return err
		}
		reserved, err := queries.ReserveJobBudget(ctx, csfpg.ReserveJobBudgetParams{Account: ledger.budget.Account, Amount: admission.ReservationUSDMicros})
		if err != nil {
			return err
		}
		if reserved != 1 {
			return ErrBudgetExhausted
		}
		recorded, err = queries.InsertJob(ctx, csfpg.InsertJobParams{
			JobID: admission.ID, Kind: string(admission.Kind), Executor: string(admission.Executor), BudgetAccount: ledger.budget.Account,
			Request: request, RequestSha256: digest, TotalUnits: admission.TotalUnits, Managed: admission.Managed,
			ExecutorTarget: admission.Target, ExecutorImage: admission.Image, ArtifactUri: admission.ArtifactURI,
			ReservationUsdMicros: admission.ReservationUSDMicros, TimeoutSeconds: int32(admission.Timeout.Seconds()),
		})
		return err
	})
	if err != nil {
		return Job[Spec]{}, err
	}
	return decodeJob[Spec](recorded)
}

func (ledger *JobLedger[Spec]) validAdmission(admission Admission[Spec]) error {
	switch {
	case !jobIdentity.MatchString(admission.ID):
		return fmt.Errorf("%w: job identity %q", ErrInvalidAdmission, admission.ID)
	case !ledger.serves(admission.Kind):
		return fmt.Errorf("%w: %q", ErrUnknownKind, admission.Kind)
	case !executorName.MatchString(string(admission.Executor)):
		return fmt.Errorf("%w: executor %q", ErrInvalidAdmission, admission.Executor)
	case admission.TotalUnits < 1:
		return fmt.Errorf("%w: at least one unit of work is required", ErrInvalidAdmission)
	case admission.ReservationUSDMicros < 0:
		return fmt.Errorf("%w: negative reservation", ErrInvalidAdmission)
	case admission.Timeout < time.Second || admission.Timeout > maxTimeout || admission.Timeout%time.Second != 0:
		return fmt.Errorf("%w: timeout must be whole seconds", ErrInvalidAdmission)
	}
	return nil
}

// Get returns one job of this ledger's kinds.
func (ledger *JobLedger[Spec]) Get(ctx context.Context, jobID string) (Job[Spec], error) {
	row, err := ledger.database.GetJob(ctx, csfpg.GetJobParams{JobID: jobID, Kinds: ledger.kinds})
	if errors.Is(err, pgx.ErrNoRows) {
		return Job[Spec]{}, fmt.Errorf("%w: %s", ErrJobNotFound, jobID)
	}
	if err != nil {
		return Job[Spec]{}, err
	}
	return decodeJob[Spec](row)
}

// List returns up to limit jobs, newest first.
func (ledger *JobLedger[Spec]) List(ctx context.Context, limit int32) ([]Job[Spec], error) {
	rows, err := ledger.database.ListJobs(ctx, csfpg.ListJobsParams{Kinds: ledger.kinds, RowLimit: limit})
	if err != nil {
		return nil, err
	}
	return decodeJobs[Spec](rows)
}

// Cancel records a cancellation request. A pending job is cancelled at
// once; a managed pending job has no external resource, so its cleanup is
// confirmed too. Other jobs are cancelled by their executor.
func (ledger *JobLedger[Spec]) Cancel(ctx context.Context, jobID string) (Job[Spec], error) {
	row, err := ledger.database.RequestJobCancellation(ctx, csfpg.RequestJobCancellationParams{JobID: jobID, Kinds: ledger.kinds})
	if errors.Is(err, pgx.ErrNoRows) {
		return Job[Spec]{}, fmt.Errorf("%w: %s", ErrJobNotFound, jobID)
	}
	if err != nil {
		return Job[Spec]{}, err
	}
	return decodeJob[Spec](row)
}

// Record applies an observation atomically under the job's row lock.
func (ledger *JobLedger[Spec]) Record(ctx context.Context, jobID string, observation Observation) error {
	return ledger.database.Transact(ctx, func(queries IJobQueries) error {
		return ledger.record(ctx, queries, jobID, observation)
	})
}

func (ledger *JobLedger[Spec]) record(ctx context.Context, queries IJobQueries, jobID string, observation Observation) error {
	row, err := queries.LockJob(ctx, csfpg.LockJobParams{JobID: jobID, Kinds: ledger.kinds})
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("%w: %s", ErrJobNotFound, jobID)
	}
	if err != nil {
		return err
	}
	job, err := decodeJob[Spec](row)
	if err != nil {
		return err
	}
	stored, err := queries.ListJobMetricDefinitions(ctx, jobID)
	if err != nil {
		return err
	}
	declared := make(map[string]MetricDefinition, len(stored))
	for _, definition := range stored {
		declared[definition.Name] = MetricDefinition{Name: definition.Name, Unit: definition.Unit, Description: definition.Description}
	}
	plan, err := planObservation(ledger.rules, job, declared, observation)
	if err != nil {
		return err
	}
	for _, definition := range plan.definitions {
		count, err := queries.InsertJobMetricDefinition(ctx, csfpg.InsertJobMetricDefinitionParams{JobID: jobID, Name: definition.Name, Unit: definition.Unit, Description: definition.Description})
		if err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("%w: %s", ErrMetricRedefined, definition.Name)
		}
	}
	for _, measurement := range plan.measurements {
		count, err := queries.InsertJobMeasurement(ctx, csfpg.InsertJobMeasurementParams{JobID: jobID, Metric: measurement.Metric, Step: int64(measurement.Step), Value: measurement.Value, RecordedAt: csfpg.Timestamp(measurement.RecordedAt), EvidenceHash: observation.EvidenceHash})
		if err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("%w: %s step %d", ErrConflictingMeasurement, measurement.Metric, measurement.Step)
		}
	}
	return queries.RecordJobProgress(ctx, csfpg.RecordJobProgressParams{JobID: jobID, CompletedUnits: plan.completedUnits, State: csfpg.CsfJobState(plan.state), Reason: plan.reason})
}

// Measurements returns the latest measurement of every metric of a job.
func (ledger *JobLedger[Spec]) Measurements(ctx context.Context, jobID string) ([]RecordedMeasurement, error) {
	rows, err := ledger.database.LatestJobMeasurements(ctx, jobID)
	if err != nil {
		return nil, err
	}
	result := make([]RecordedMeasurement, 0, len(rows))
	for _, row := range rows {
		result = append(result, RecordedMeasurement{Measurement: Measurement{Metric: row.Metric, Step: uint64(row.Step), Value: row.Value, RecordedAt: csfpg.Time(row.RecordedAt)}, EvidenceHash: row.EvidenceHash})
	}
	return result, nil
}

// StateCount is how many jobs of one kind and executor are in one state.
type StateCount struct {
	Kind     Kind
	Executor ExecutorName
	State    State
	Jobs     int64
}

// States counts this ledger's jobs by kind, executor and state.
func (ledger *JobLedger[Spec]) States(ctx context.Context) ([]StateCount, error) {
	rows, err := ledger.database.CountJobStates(ctx, ledger.kinds)
	if err != nil {
		return nil, err
	}
	result := make([]StateCount, 0, len(rows))
	for _, row := range rows {
		result = append(result, StateCount{Kind: Kind(row.Kind), Executor: ExecutorName(row.Executor), State: State(row.State), Jobs: row.Jobs})
	}
	return result, nil
}

// LatestProgress is the progress of the newest job of a kind and executor.
type LatestProgress struct {
	Kind           Kind
	Executor       ExecutorName
	TotalUnits     int64
	CompletedUnits int64
	UpdatedAt      time.Time
}

// LatestProgress reports the newest job's progress per kind and executor.
func (ledger *JobLedger[Spec]) LatestProgress(ctx context.Context) ([]LatestProgress, error) {
	rows, err := ledger.database.LatestJobProgress(ctx, ledger.kinds)
	if err != nil {
		return nil, err
	}
	result := make([]LatestProgress, 0, len(rows))
	for _, row := range rows {
		result = append(result, LatestProgress{Kind: Kind(row.Kind), Executor: ExecutorName(row.Executor), TotalUnits: row.TotalUnits, CompletedUnits: row.CompletedUnits, UpdatedAt: csfpg.Time(row.UpdatedAt)})
	}
	return result, nil
}

// BudgetUsage reads the ledger's account. It returns ErrBudgetNotOpened
// before the first admission creates the account.
func (ledger *JobLedger[Spec]) BudgetUsage(ctx context.Context) (BudgetUsage, error) {
	row, err := ledger.database.GetJobBudget(ctx, ledger.budget.Account)
	if errors.Is(err, pgx.ErrNoRows) {
		return BudgetUsage{}, ErrBudgetNotOpened
	}
	if err != nil {
		return BudgetUsage{}, err
	}
	return BudgetUsage{Budget: Budget{Account: row.Account, LimitUSDMicros: row.LimitUsdMicros}, ReservedUSDMicros: row.ReservedUsdMicros}, nil
}
