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

// Bounds of one reconciliation pass. A remote provider gets its own deadline
// per call; a host pass holds one transaction for its whole iteration.
const (
	remoteSubmitDeadline   = 30 * time.Second
	remoteObserveDeadline  = 20 * time.Second
	hostReconcileDeadline  = 25 * time.Second
	interruptedGrace       = 120
	pollableJobsPerPass    = 32
	interruptedSubmission  = "Submission interrupted; inspect the executor by the job identity before retrying"
	executorKindRemote     = "remote"
	executorKindHost       = "host"
	errorRecordingDeadline = 5 * time.Second
)

// LogPage is one page of an external job's log: what it observed and the
// cursor to resume from.
type LogPage struct {
	Observation Observation
	NextCursor  string
}

// IRemoteExecutor runs jobs on a provider without an idempotency key. The
// ledger calls Submit at most once per job, after committing the claim.
type IRemoteExecutor[Spec any] interface {
	// Submit starts the external job and returns its provider identity. An
	// error leaves the job in submission_unknown; it is never retried.
	Submit(ctx context.Context, job Job[Spec]) (externalID string, err error)
	// Observe reads the external job, requests termination when the job's
	// cancellation was requested, and reports the execution state.
	Observe(ctx context.Context, job Job[Spec]) (Execution, error)
	// ReadLog reads the next page of the job's log stream from its cursor.
	ReadLog(ctx context.Context, job Job[Spec]) (LogPage, error)
}

// IHostExecutor drives jobs on this host through operations that are
// idempotent by a durable name.
type IHostExecutor[Spec any] interface {
	// Reconcile moves the job one step toward a terminal state with
	// confirmed cleanup. The returned execution is always recorded; an error
	// is recorded as the job's inspection error and retried next pass.
	Reconcile(ctx context.Context, job Job[Spec]) (Execution, error)
}

// executorBinding is one registered executor; exactly one field is set.
type executorBinding[Spec any] struct {
	name   ExecutorName
	remote IRemoteExecutor[Spec]
	host   IHostExecutor[Spec]
}

// WithRemoteExecutor registers a remote executor under name.
func WithRemoteExecutor[Spec any](name ExecutorName, executor IRemoteExecutor[Spec]) LedgerOption[Spec] {
	return func(ledger *JobLedger[Spec]) error {
		if executor == nil {
			return fmt.Errorf("%w: %s %q", ErrInvalidExecutor, executorKindRemote, name)
		}
		return ledger.bind(executorBinding[Spec]{name: name, remote: executor})
	}
}

// WithHostExecutor registers a host executor under name.
func WithHostExecutor[Spec any](name ExecutorName, executor IHostExecutor[Spec]) LedgerOption[Spec] {
	return func(ledger *JobLedger[Spec]) error {
		if executor == nil {
			return fmt.Errorf("%w: %s %q", ErrInvalidExecutor, executorKindHost, name)
		}
		return ledger.bind(executorBinding[Spec]{name: name, host: executor})
	}
}

func (ledger *JobLedger[Spec]) bind(binding executorBinding[Spec]) error {
	if !executorName.MatchString(string(binding.name)) || slices.ContainsFunc(ledger.executors, func(existing executorBinding[Spec]) bool { return existing.name == binding.name }) {
		return fmt.Errorf("%w: %q", ErrInvalidExecutor, binding.name)
	}
	ledger.executors = append(ledger.executors, binding)
	return nil
}

// Reconcile makes one bounded pass over every registered executor, in
// registration order. Per-job executor failures are recorded on the job;
// only database failures are returned.
func (ledger *JobLedger[Spec]) Reconcile(ctx context.Context) error {
	for _, binding := range ledger.executors {
		var err error
		if binding.remote != nil {
			err = ledger.reconcileRemote(ctx, binding.name, binding.remote)
		} else {
			err = ledger.reconcileHost(ctx, binding.name, binding.host)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (ledger *JobLedger[Spec]) reconcileRemote(ctx context.Context, name ExecutorName, executor IRemoteExecutor[Spec]) error {
	if err := ledger.database.MarkInterruptedJobSubmissions(ctx, csfpg.MarkInterruptedJobSubmissionsParams{Executor: string(name), Reason: interruptedSubmission, GraceSeconds: interruptedGrace}); err != nil {
		return err
	}
	row, err := ledger.database.ClaimJobSubmission(ctx, csfpg.ClaimJobSubmissionParams{Executor: string(name), Kinds: ledger.kinds})
	switch {
	case err == nil:
		if err := ledger.submitRemote(ctx, executor, row); err != nil {
			return err
		}
	case !errors.Is(err, pgx.ErrNoRows):
		return err
	}
	rows, err := ledger.database.PollableJobs(ctx, csfpg.PollableJobsParams{Executor: string(name), Kinds: ledger.kinds, RowLimit: pollableJobsPerPass})
	if err != nil {
		return err
	}
	for _, row := range rows {
		job, err := decodeJob[Spec](row)
		if err == nil {
			deadline, cancel := context.WithTimeout(ctx, remoteObserveDeadline)
			err = ledger.observeRemote(deadline, executor, job)
			cancel()
		}
		if err != nil {
			if err := ledger.recordProblem(ctx, ledger.database, row.JobID, err); err != nil {
				return err
			}
		}
	}
	return nil
}

func (ledger *JobLedger[Spec]) submitRemote(ctx context.Context, executor IRemoteExecutor[Spec], row csfpg.CsfJob) error {
	state, externalID, reason := StateQueued, "", ""
	job, problem := decodeJob[Spec](row)
	if problem == nil {
		deadline, cancel := context.WithTimeout(ctx, remoteSubmitDeadline)
		externalID, problem = executor.Submit(deadline, job)
		cancel()
		if problem == nil && externalID == "" {
			problem = ErrNoExternalIdentity
		}
	}
	if problem != nil {
		state, externalID, reason = StateSubmissionUnknown, "", problem.Error()
	}
	_, err := ledger.database.FinishJobSubmission(ctx, csfpg.FinishJobSubmissionParams{JobID: row.JobID, State: csfpg.CsfJobState(state), ExternalID: externalID, Reason: reason})
	return err
}

func (ledger *JobLedger[Spec]) observeRemote(ctx context.Context, executor IRemoteExecutor[Spec], job Job[Spec]) error {
	execution, err := executor.Observe(ctx, job)
	if err != nil {
		return err
	}
	if err := ledger.updateExecution(ctx, ledger.database, job, execution); err != nil {
		return err
	}
	if execution.LogStream == "" {
		return nil
	}
	job.LogStream = execution.LogStream
	page, err := executor.ReadLog(ctx, job)
	if err != nil {
		return err
	}
	if len(page.Observation.Entries) > 0 {
		if err := ledger.Record(ctx, job.ID, page.Observation); err != nil {
			return err
		}
	}
	return ledger.database.SetJobLogCursor(ctx, csfpg.SetJobLogCursorParams{JobID: job.ID, LogCursor: page.NextCursor})
}

// A transaction-scoped advisory lock serializes host reconciliation, even
// during overlapping deploys, and the row lock keeps observations out while
// the executor acts.
func (ledger *JobLedger[Spec]) reconcileHost(ctx context.Context, name ExecutorName, executor IHostExecutor[Spec]) error {
	deadline, cancel := context.WithTimeout(ctx, hostReconcileDeadline)
	defer cancel()
	return ledger.database.Transact(deadline, func(queries IJobQueries) error {
		acquired, err := queries.LockJobExecutor(deadline, string(name))
		if err != nil || !acquired {
			return err
		}
		row, err := queries.NextHostJob(deadline, csfpg.NextHostJobParams{Executor: string(name), Kinds: ledger.kinds})
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		job, err := decodeJob[Spec](row)
		if err != nil {
			return ledger.recordProblem(deadline, queries, row.JobID, err)
		}
		execution, problem := executor.Reconcile(deadline, job)
		if err := ledger.updateExecution(deadline, queries, job, execution); err != nil {
			return err
		}
		if problem != nil {
			return ledger.recordProblem(deadline, queries, job.ID, problem)
		}
		return nil
	})
}

func (ledger *JobLedger[Spec]) updateExecution(ctx context.Context, queries IJobQueries, job Job[Spec], execution Execution) error {
	if execution.ExternalID == "" {
		execution.ExternalID = job.ExternalID
	}
	return queries.UpdateJobExecution(ctx, csfpg.UpdateJobExecutionParams{JobID: job.ID, State: csfpg.CsfJobState(execution.State), ExternalID: execution.ExternalID, Reason: execution.Reason, LogStream: execution.LogStream, CleanupConfirmed: execution.CleanupConfirmed})
}

func (ledger *JobLedger[Spec]) recordProblem(ctx context.Context, queries IJobQueries, jobID string, problem error) error {
	deadline, cancel := context.WithTimeout(context.WithoutCancel(ctx), errorRecordingDeadline)
	defer cancel()
	return queries.SetJobInspectionError(deadline, csfpg.SetJobInspectionErrorParams{JobID: jobID, InspectionError: problem.Error()})
}
