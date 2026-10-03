// Copyright 2026 Candace Labs

package jobs

import (
	"context"
	"errors"
	"fmt"

	"github.com/candacelabs/csf/ipc/db/csfpg"
	"github.com/jackc/pgx/v5"
)

// Projections export terminal jobs to trace and log systems. The ledger
// keeps their state; the consumer owns the projection itself.

// NextUntraced returns the newest terminal job of executor whose trace has
// not been exported and whose retry time has come. A managed job must have
// confirmed cleanup first. ok is false when none is waiting.
func (ledger *JobLedger[Spec]) NextUntraced(ctx context.Context, executor ExecutorName) (job Job[Spec], ok bool, err error) {
	return ledger.next(ledger.database.NextUntracedJob(ctx, csfpg.NextUntracedJobParams{Executor: string(executor), Kinds: ledger.kinds}))
}

// RecordTrace records a trace export result; it also delays the next retry.
func (ledger *JobLedger[Spec]) RecordTrace(ctx context.Context, jobID string, trace TraceProjection) error {
	return ledger.database.SetJobTrace(ctx, csfpg.SetJobTraceParams{JobID: jobID, TraceUrl: trace.URL, TraceExportError: trace.ExportError})
}

// NextUnarchived returns the newest terminal, cleaned-up job of executor
// whose logs have not been archived and whose retry time has come.
func (ledger *JobLedger[Spec]) NextUnarchived(ctx context.Context, executor ExecutorName) (job Job[Spec], ok bool, err error) {
	return ledger.next(ledger.database.NextUnarchivedJob(ctx, csfpg.NextUnarchivedJobParams{Executor: string(executor), Kinds: ledger.kinds}))
}

// RecordLogArchive records a log archival result; it also delays the next retry.
func (ledger *JobLedger[Spec]) RecordLogArchive(ctx context.Context, jobID string, archive LogArchive) error {
	return ledger.database.SetJobLogArchive(ctx, csfpg.SetJobLogArchiveParams{JobID: jobID, LogDocumentID: archive.DocumentID, LogProjectionError: archive.ProjectionError, LogIndexedAt: csfpg.Timestamp(archive.IndexedAt)})
}

func (ledger *JobLedger[Spec]) next(row csfpg.CsfJob, err error) (Job[Spec], bool, error) {
	if errors.Is(err, pgx.ErrNoRows) {
		return Job[Spec]{}, false, nil
	}
	if err != nil {
		return Job[Spec]{}, false, err
	}
	job, err := decodeJob[Spec](row)
	return job, err == nil, err
}

// DeliveryState is the state of one immutable trace delivery.
type DeliveryState string

// The delivery states, spelled by the generated csfpg enum.
const (
	DeliveryAttempted = DeliveryState(csfpg.CsfTraceDeliveryStateAttempted)
	DeliverySucceeded = DeliveryState(csfpg.CsfTraceDeliveryStateSucceeded)
	DeliveryAmbiguous = DeliveryState(csfpg.CsfTraceDeliveryStateAmbiguous)
)

// TraceDelivery names one trace sent to one destination for a job.
type TraceDelivery struct {
	Destination string
	TraceID     string
	JobID       string
}

// ClaimTraceDelivery records the attempt before anything is sent. claimed
// is false when the destination already has a delivery of this trace; read
// its state with TraceDeliveryState and never resend an unconfirmed one.
func (ledger *JobLedger[Spec]) ClaimTraceDelivery(ctx context.Context, delivery TraceDelivery) (claimed bool, err error) {
	_, err = ledger.database.ClaimJobTraceDelivery(ctx, csfpg.ClaimJobTraceDeliveryParams{Destination: delivery.Destination, JobID: delivery.JobID, TraceID: delivery.TraceID})
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// TraceDeliveryState reads a recorded delivery.
func (ledger *JobLedger[Spec]) TraceDeliveryState(ctx context.Context, delivery TraceDelivery) (DeliveryState, error) {
	row, err := ledger.database.GetJobTraceDelivery(ctx, csfpg.GetJobTraceDeliveryParams{Destination: delivery.Destination, TraceID: delivery.TraceID})
	if err != nil {
		return "", err
	}
	return DeliveryState(row.State), nil
}

// FinishTraceDelivery records the outcome of a claimed delivery: succeeded
// when failure is nil, otherwise ambiguous with the failure's text. Only an
// attempted delivery can finish.
func (ledger *JobLedger[Spec]) FinishTraceDelivery(ctx context.Context, delivery TraceDelivery, failure error) error {
	result := csfpg.FinishJobTraceDeliveryParams{Destination: delivery.Destination, TraceID: delivery.TraceID, State: csfpg.CsfTraceDeliveryStateSucceeded}
	if failure != nil {
		result.State, result.Error = csfpg.CsfTraceDeliveryStateAmbiguous, failure.Error()
	}
	_, err := ledger.database.FinishJobTraceDelivery(ctx, result)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("jobs: trace delivery %s to %s is not attempted", delivery.TraceID, delivery.Destination)
	}
	return err
}
