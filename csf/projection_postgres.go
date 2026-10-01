package csf

import (
	"context"
	"errors"

	db "github.com/candacelabs/csf/csf/internal/brainspinedb"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/jackc/pgx/v5"
)

const (
	projectionLeaseSeconds     = 60
	projectionMaxAttempts      = 5
	projectionRetryBaseSeconds = 2
	projectionRetryMaxSeconds  = 60
)

var projectionStates = map[db.BrainspineProjectionStatus]pb.ProjectionState{
	db.BrainspineProjectionStatusPending:   pb.ProjectionState_PROJECTION_STATE_PENDING,
	db.BrainspineProjectionStatusRunning:   pb.ProjectionState_PROJECTION_STATE_RUNNING,
	db.BrainspineProjectionStatusSucceeded: pb.ProjectionState_PROJECTION_STATE_SUCCEEDED,
	db.BrainspineProjectionStatusFailed:    pb.ProjectionState_PROJECTION_STATE_FAILED,
}

func (store *Postgres) ClaimProjection(ctx context.Context) (*pb.ProjectionTask, error) {
	task, err := store.queries.ClaimProjectionTask(ctx, db.ClaimProjectionTaskParams{LeaseSeconds: projectionLeaseSeconds, MaxAttempts: projectionMaxAttempts})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return projectionMessage(db.BrainspineProjectionTask(task)), nil
}
func (store *Postgres) CompleteProjection(ctx context.Context, task *pb.ProjectionTask) error {
	_, err := store.queries.CompleteProjectionTask(ctx, db.CompleteProjectionTaskParams{SourceID: task.Document.SourceId, Revision: task.Document.Revision, LeaseGeneration: task.LeaseGeneration})
	return err
}
func (store *Postgres) FailProjection(ctx context.Context, task *pb.ProjectionTask, problem string) error {
	_, err := store.queries.FailProjectionTask(ctx, db.FailProjectionTaskParams{SourceID: task.Document.SourceId, Revision: task.Document.Revision, LeaseGeneration: task.LeaseGeneration, MaxAttempts: projectionMaxAttempts, RetryBaseSeconds: projectionRetryBaseSeconds, RetryMaxSeconds: projectionRetryMaxSeconds, LastError: problem})
	return err
}
func (store *Postgres) GetProjection(ctx context.Context, request *pb.DocumentRequest) (*pb.ProjectionTask, error) {
	task, err := store.queries.GetProjectionTask(ctx, db.GetProjectionTaskParams{SourceID: request.SourceId, Revision: request.Revision})
	if err != nil {
		return nil, err
	}
	return projectionMessage(task), nil
}
func (store *Postgres) CountProjections(ctx context.Context) ([]*pb.ProjectionCount, error) {
	counts, err := store.queries.CountProjectionTasks(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]*pb.ProjectionCount, 0, len(counts))
	for _, count := range counts {
		result = append(result, &pb.ProjectionCount{State: projectionStates[count.Status], Count: count.Count})
	}
	return result, nil
}
func projectionMessage(task db.BrainspineProjectionTask) *pb.ProjectionTask {
	return &pb.ProjectionTask{Document: &pb.DocumentRequest{SourceId: task.SourceID, Revision: task.Revision}, State: projectionStates[task.Status], Attempts: task.Attempts, LeaseGeneration: task.LeaseGeneration, LastError: task.LastError}
}
