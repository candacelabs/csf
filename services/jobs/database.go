// Copyright 2026 Candace Labs

package jobs

import (
	"context"

	"github.com/jackc/pgx/v5"

	"github.com/candacelabs/csf/ipc/db/csfpg"
)

// IJobQueries is the part of csfpg's generated queries the ledger runs.
// *csfpg.Queries satisfies it; the SQL is owned by ipc/db/csfpg/jobs.sql.
type IJobQueries interface {
	EnsureJobBudget(ctx context.Context, arg csfpg.EnsureJobBudgetParams) (csfpg.CsfJobBudget, error)
	LockJobBudget(ctx context.Context, account string) (csfpg.CsfJobBudget, error)
	GetJobBudget(ctx context.Context, account string) (csfpg.CsfJobBudget, error)
	ReserveJobBudget(ctx context.Context, arg csfpg.ReserveJobBudgetParams) (int64, error)
	InsertJob(ctx context.Context, arg csfpg.InsertJobParams) (csfpg.CsfJob, error)
	GetJob(ctx context.Context, arg csfpg.GetJobParams) (csfpg.CsfJob, error)
	LockJob(ctx context.Context, arg csfpg.LockJobParams) (csfpg.CsfJob, error)
	ListJobs(ctx context.Context, arg csfpg.ListJobsParams) ([]csfpg.CsfJob, error)
	RequestJobCancellation(ctx context.Context, arg csfpg.RequestJobCancellationParams) (csfpg.CsfJob, error)
	ListJobMetricDefinitions(ctx context.Context, jobID string) ([]csfpg.CsfJobMetricDefinition, error)
	InsertJobMetricDefinition(ctx context.Context, arg csfpg.InsertJobMetricDefinitionParams) (int64, error)
	InsertJobMeasurement(ctx context.Context, arg csfpg.InsertJobMeasurementParams) (int64, error)
	LatestJobMeasurements(ctx context.Context, jobID string) ([]csfpg.CsfJobMeasurement, error)
	RecordJobProgress(ctx context.Context, arg csfpg.RecordJobProgressParams) error
	ClaimJobSubmission(ctx context.Context, arg csfpg.ClaimJobSubmissionParams) (csfpg.CsfJob, error)
	MarkInterruptedJobSubmissions(ctx context.Context, arg csfpg.MarkInterruptedJobSubmissionsParams) error
	FinishJobSubmission(ctx context.Context, arg csfpg.FinishJobSubmissionParams) (int64, error)
	PollableJobs(ctx context.Context, arg csfpg.PollableJobsParams) ([]csfpg.CsfJob, error)
	UpdateJobExecution(ctx context.Context, arg csfpg.UpdateJobExecutionParams) error
	SetJobInspectionError(ctx context.Context, arg csfpg.SetJobInspectionErrorParams) error
	SetJobLogCursor(ctx context.Context, arg csfpg.SetJobLogCursorParams) error
	LockJobExecutor(ctx context.Context, executor string) (bool, error)
	NextHostJob(ctx context.Context, arg csfpg.NextHostJobParams) (csfpg.CsfJob, error)
	NextUntracedJob(ctx context.Context, arg csfpg.NextUntracedJobParams) (csfpg.CsfJob, error)
	SetJobTrace(ctx context.Context, arg csfpg.SetJobTraceParams) error
	NextUnarchivedJob(ctx context.Context, arg csfpg.NextUnarchivedJobParams) (csfpg.CsfJob, error)
	SetJobLogArchive(ctx context.Context, arg csfpg.SetJobLogArchiveParams) error
	CountJobStates(ctx context.Context, kinds []string) ([]csfpg.CountJobStatesRow, error)
	LatestJobProgress(ctx context.Context, kinds []string) ([]csfpg.LatestJobProgressRow, error)
	ClaimJobTraceDelivery(ctx context.Context, arg csfpg.ClaimJobTraceDeliveryParams) (string, error)
	GetJobTraceDelivery(ctx context.Context, arg csfpg.GetJobTraceDeliveryParams) (csfpg.CsfJobTraceDelivery, error)
	FinishJobTraceDelivery(ctx context.Context, arg csfpg.FinishJobTraceDeliveryParams) (string, error)
}

// IJobDatabase is the ledger's database: the queries outside a transaction,
// and Transact for work that must commit or roll back as one unit.
type IJobDatabase interface {
	IJobQueries
	// Transact runs work inside one transaction and commits it when work
	// returns nil; any error rolls the transaction back and is returned.
	Transact(ctx context.Context, work func(queries IJobQueries) error) error
}

// PostgresJobDatabase is the IJobDatabase over the PostgreSQL capability.
// It borrows the capability and never closes it.
type PostgresJobDatabase struct {
	*csfpg.Queries
	database csfpg.IDB
}

// NewPostgresJobDatabase returns the job database over a pool or connection
// the binary opened through ipc/db/csfpg.
func NewPostgresJobDatabase(database csfpg.IDB) (*PostgresJobDatabase, error) {
	if database == nil {
		return nil, ErrDatabaseRequired
	}
	return &PostgresJobDatabase{Queries: csfpg.New(database), database: database}, nil
}

// Transact runs work in one READ COMMITTED transaction.
func (database *PostgresJobDatabase) Transact(ctx context.Context, work func(queries IJobQueries) error) error {
	return csfpg.Transact(ctx, database.database, func(tx pgx.Tx) error {
		return work(database.Queries.WithTx(tx))
	})
}
