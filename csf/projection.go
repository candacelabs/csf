package csf

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

const (
	projectionWorkerCount  = 2
	projectionPollInterval = time.Second
	projectionTaskTimeout  = 25 * time.Second
	projectionStoreTimeout = 5 * time.Second
	projectionErrorField   = "error"
)

// ProjectionWorkers runs bounded, at-least-once projection work in its caller's
// process. PostgreSQL owns claims and retries; wakeups are only a latency hint.
type ProjectionWorkers struct {
	service  *Service
	cancel   context.CancelFunc
	finished sync.WaitGroup
	active   atomic.Int64
}

// StartProjectionWorkers starts one pool for this service. The caller closes it
// before closing the store, artifacts or shared OpenSearch client.
func (service *Service) StartProjectionWorkers(ctx context.Context) (*ProjectionWorkers, error) {
	if service.store == nil {
		return nil, nil
	}
	if !service.workersStarted.CompareAndSwap(false, true) {
		return nil, fmt.Errorf("projection workers already started")
	}
	workerContext, cancel := context.WithCancel(ctx)
	workers := &ProjectionWorkers{service: service, cancel: cancel}
	for range projectionWorkerCount {
		workers.finished.Go(func() { workers.consume(workerContext) })
	}
	return workers, nil
}

func (workers *ProjectionWorkers) Close() {
	if workers == nil {
		return
	}
	workers.cancel()
	workers.finished.Wait()
}

func (workers *ProjectionWorkers) Configured() int { return projectionWorkerCount }
func (workers *ProjectionWorkers) Active() int64   { return workers.active.Load() }
func (workers *ProjectionWorkers) Counts(ctx context.Context) ([]*pb.ProjectionCount, error) {
	return workers.service.store.CountProjections(ctx)
}

func (workers *ProjectionWorkers) consume(ctx context.Context) {
	ticker := time.NewTicker(projectionPollInterval)
	defer ticker.Stop()
	for ctx.Err() == nil {
		claimContext, cancel := context.WithTimeout(ctx, projectionStoreTimeout)
		task, err := workers.service.store.ClaimProjection(claimContext)
		cancel()
		if err != nil && ctx.Err() == nil {
			slog.Error("Cannot claim projection task", projectionErrorField, err)
		}
		if err == nil && task != nil {
			workers.project(ctx, task)
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-workers.service.wake:
		case <-ticker.C:
		}
	}
}

func (workers *ProjectionWorkers) project(ctx context.Context, task *pb.ProjectionTask) {
	workers.active.Add(1)
	defer workers.active.Add(-1)
	taskContext, cancel := context.WithTimeout(ctx, projectionTaskTimeout)
	err := workers.indexDocument(taskContext, task)
	cancel()
	// Cancellation leaves the lease intact for recovery after restart.
	if ctx.Err() != nil {
		return
	}
	settlementContext, finish := context.WithTimeout(ctx, projectionStoreTimeout)
	defer finish()
	var settlementError error
	if err != nil {
		settlementError = workers.service.store.FailProjection(settlementContext, task, err.Error())
	} else {
		settlementError = workers.service.store.CompleteProjection(settlementContext, task)
	}
	if settlementError != nil {
		slog.Error("Cannot settle projection lease; durable recovery remains pending", projectionErrorField, settlementError)
	}
}

func (workers *ProjectionWorkers) indexDocument(ctx context.Context, task *pb.ProjectionTask) error {
	document, err := workers.service.store.GetDocument(ctx, task.Document)
	if err != nil {
		return err
	}
	content, err := workers.service.artifacts.Get(document.ContentHash)
	if err != nil {
		return err
	}
	return workers.service.index.Index(ctx, document, string(content))
}
