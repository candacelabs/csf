// Copyright 2026 Candace Labs

package jobs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"time"

	"github.com/candacelabs/csf/io/ipc/db/csfpg"
)

// Kind names what a job does, such as "simulation.carla". The consumer
// chooses it; the ledger stores and groups by it.
type Kind string

// ExecutorName names the executor a job is admitted to, such as "local".
type ExecutorName string

// State is a job's durable lifecycle state.
type State string

// The job states, spelled by the generated csfpg enum.
const (
	StatePending           = State(csfpg.CsfJobStatePending)
	StateSubmitting        = State(csfpg.CsfJobStateSubmitting)
	StateQueued            = State(csfpg.CsfJobStateQueued)
	StateRunning           = State(csfpg.CsfJobStateRunning)
	StateSucceeded         = State(csfpg.CsfJobStateSucceeded)
	StateFailed            = State(csfpg.CsfJobStateFailed)
	StateCancelling        = State(csfpg.CsfJobStateCancelling)
	StateCancelled         = State(csfpg.CsfJobStateCancelled)
	StateSubmissionUnknown = State(csfpg.CsfJobStateSubmissionUnknown)
)

// Terminal reports whether no executor will change the state again.
func (state State) Terminal() bool {
	return state == StateSucceeded || state == StateFailed || state == StateCancelled
}

// The identity rules the schema's CHECK constraints also enforce.
var (
	jobIdentity    = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,79}$`)
	kindName       = regexp.MustCompile(`^[a-z][a-z0-9_.-]{0,79}$`)
	executorName   = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,39}$`)
	budgetAccount  = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.-]{0,79}$`)
	evidenceSHA256 = regexp.MustCompile(`^[0-9a-f]{64}$`)
)

// maxTimeout is the largest timeout the INTEGER seconds column holds.
const maxTimeout = (1<<31 - 1) * time.Second

// ValidJobID reports whether identity can name a job.
func ValidJobID(identity string) bool { return jobIdentity.MatchString(identity) }

// Budget is one immutable spending account that admissions reserve from.
type Budget struct {
	Account        string
	LimitUSDMicros int64
}

// BudgetUsage is an account's limit and the reservations it holds.
type BudgetUsage struct {
	Budget
	ReservedUSDMicros int64
}

// Admission is a request to record a job for an executor.
type Admission[Spec any] struct {
	ID       string
	Kind     Kind
	Executor ExecutorName
	Spec     Spec
	// TotalUnits is the admitted amount of work, at least one; progress and
	// measurement steps are bounded by it.
	TotalUnits int64
	// Managed jobs are driven by an executor registered with the ledger,
	// which owns the external resource and confirms its cleanup. Unmanaged
	// jobs are run by someone else and report through Record.
	Managed              bool
	Target               string
	Image                string
	ArtifactURI          string
	ReservationUSDMicros int64
	Timeout              time.Duration
}

// TraceProjection is the job's trace export state.
type TraceProjection struct {
	URL         string
	ExportError string
}

// LogArchive is the job's log archival state.
type LogArchive struct {
	DocumentID      string
	ProjectionError string
	IndexedAt       time.Time
}

// Job is one recorded job with its decoded request.
type Job[Spec any] struct {
	ID                    string
	Kind                  Kind
	Executor              ExecutorName
	BudgetAccount         string
	Spec                  Spec
	RequestSHA256         string
	State                 State
	TotalUnits            int64
	CompletedUnits        int64
	Managed               bool
	Target                string
	Image                 string
	ExternalID            string
	ArtifactURI           string
	ReservationUSDMicros  int64
	Timeout               time.Duration
	Reason                string
	LogStream             string
	LogCursor             string
	InspectionError       string
	CancellationRequested bool
	CleanupConfirmed      bool
	Trace                 TraceProjection
	LogArchive            LogArchive
	CreatedAt             time.Time
	UpdatedAt             time.Time
}

// Execution is the executor-owned part of a job: what an executor reports
// after observing or driving the external resource.
type Execution struct {
	State            State
	ExternalID       string
	Reason           string
	LogStream        string
	CleanupConfirmed bool
}

// Execution returns the job's current executor-owned state, the starting
// point an executor changes and returns.
func (job Job[Spec]) Execution() Execution {
	return Execution{State: job.State, ExternalID: job.ExternalID, Reason: job.Reason, LogStream: job.LogStream, CleanupConfirmed: job.CleanupConfirmed}
}

// encodeRequest is the one place a Spec becomes stored JSON.
func encodeRequest[Spec any](spec Spec) ([]byte, string, error) {
	encoded, err := json.Marshal(spec)
	if err != nil {
		return nil, "", fmt.Errorf("%w: encode request: %v", ErrInvalidAdmission, err)
	}
	digest := sha256.Sum256(encoded)
	return encoded, hex.EncodeToString(digest[:]), nil
}

// decodeJob is the one place a stored row becomes a typed Job.
func decodeJob[Spec any](row csfpg.CsfJob) (Job[Spec], error) {
	var spec Spec
	if err := json.Unmarshal(row.Request, &spec); err != nil {
		return Job[Spec]{}, fmt.Errorf("jobs: decode request of %s: %w", row.JobID, err)
	}
	return Job[Spec]{
		ID: row.JobID, Kind: Kind(row.Kind), Executor: ExecutorName(row.Executor), BudgetAccount: row.BudgetAccount,
		Spec: spec, RequestSHA256: row.RequestSha256, State: State(row.State),
		TotalUnits: row.TotalUnits, CompletedUnits: row.CompletedUnits, Managed: row.Managed,
		Target: row.ExecutorTarget, Image: row.ExecutorImage, ExternalID: row.ExternalID, ArtifactURI: row.ArtifactUri,
		ReservationUSDMicros: row.ReservationUsdMicros, Timeout: time.Duration(row.TimeoutSeconds) * time.Second,
		Reason: row.Reason, LogStream: row.LogStream, LogCursor: row.LogCursor, InspectionError: row.InspectionError,
		CancellationRequested: row.CancellationRequested, CleanupConfirmed: row.CleanupConfirmed,
		Trace:      TraceProjection{URL: row.TraceUrl, ExportError: row.TraceExportError},
		LogArchive: LogArchive{DocumentID: row.LogDocumentID, ProjectionError: row.LogProjectionError, IndexedAt: csfpg.Time(row.LogIndexedAt)},
		CreatedAt:  csfpg.Time(row.CreatedAt), UpdatedAt: csfpg.Time(row.UpdatedAt),
	}, nil
}

func decodeJobs[Spec any](rows []csfpg.CsfJob) ([]Job[Spec], error) {
	result := make([]Job[Spec], 0, len(rows))
	for _, row := range rows {
		job, err := decodeJob[Spec](row)
		if err != nil {
			return nil, err
		}
		result = append(result, job)
	}
	return result, nil
}
