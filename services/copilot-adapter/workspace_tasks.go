package copilotadapter

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/candacelabs/csf/pkg/workcontinuity"
	workv1 "github.com/candacelabs/csf/proto/candace/work/v1"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

// workspaceTasks caches immutable, verified authority observations. The mutex
// only protects cache entries; source I/O happens outside the critical section.
type workspaceTasks struct {
	source *workcontinuity.Continuity
	mu     sync.Mutex
	cache  map[string]taskObservation
}

type taskObservation struct {
	generation uint64
	record     *workv1.ResumeRecord
}

// WithTaskContinuity supplies the issue authority used for shared planning.
// Without it, sessions still render, but task status is visibly unavailable.
func WithTaskContinuity(source *workcontinuity.Continuity) Option {
	return func(configuration *configuration) error {
		if source == nil {
			return errors.New("copilot-adapter: task continuity is nil")
		}
		configuration.taskContinuity = source
		return nil
	}
}

// WorkspaceTask returns a cloned observation, never mutable shared state.
// force is used at explicit ingestion boundaries after external issue changes.
func (adapter *CopilotAdapter) WorkspaceTask(ctx context.Context, taskURL string, force bool) *workv1.ResumeRecord {
	tasks := adapter.tasks
	tasks.mu.Lock()
	observation := tasks.cache[taskURL]
	if observation.record != nil && !force {
		tasks.mu.Unlock()
		return proto.Clone(observation.record).(*workv1.ResumeRecord)
	}
	observation.generation++
	tasks.cache[taskURL] = observation
	tasks.mu.Unlock()
	record := &workv1.ResumeRecord{Issue: &workv1.SourceIssue{HtmlUrl: taskURL}, Condition: workv1.ResumeCondition_RESUME_CONDITION_SOURCE_ERROR, CheckedAt: timestamppb.Now()}
	if tasks.source == nil {
		record.Findings = []string{"The host has not configured a task authority."}
	} else {
		loaded, err := tasks.source.Resume(ctx, taskURL, "")
		if err != nil {
			record.Findings = []string{err.Error()}
		} else {
			record = loaded
		}
	}
	tasks.mu.Lock()
	latest := tasks.cache[taskURL]
	if latest.generation == observation.generation && ctx.Err() == nil {
		latest.record = proto.Clone(record).(*workv1.ResumeRecord)
		tasks.cache[taskURL] = latest
	} else if latest.record != nil {
		// A slower source response cannot overwrite a newer refresh.
		record = proto.Clone(latest.record).(*workv1.ResumeRecord)
	}
	tasks.mu.Unlock()
	return record
}

// LinkWorkspaceTask retains only an explicit association. expectedGeneration
// belongs to that association, not to the checkpoint's Git revision.
func (adapter *CopilotAdapter) LinkWorkspaceTask(ctx context.Context, sessionID uuid.UUID, taskURL string, expectedGeneration int64) (api.WorkspaceTaskLink, error) {
	if err := workcontinuity.ValidateTaskURL(taskURL); err != nil || expectedGeneration < 0 {
		return api.WorkspaceTaskLink{}, fail(http.StatusBadRequest, errorCodeInvalidRequest, "a canonical task URL and nonnegative expected generation are required")
	}
	unlock := adapter.mutations.lock(sessionID)
	defer unlock()
	var row storedb.SessionTask
	err := adapter.store.Transact(ctx, func(queries storedb.Querier) error {
		if _, err := queries.GetSession(ctx, sessionID); err != nil {
			return err
		}
		var err error
		if expectedGeneration == 0 {
			row, err = queries.LinkSessionTask(ctx, storedb.LinkSessionTaskParams{SessionID: sessionID, TaskUrl: taskURL, ExpectedGeneration: expectedGeneration})
		} else {
			row, err = queries.ReplaceSessionTask(ctx, storedb.ReplaceSessionTaskParams{SessionID: sessionID, TaskUrl: taskURL, ExpectedGeneration: expectedGeneration})
		}
		return err
	})
	if errors.Is(err, sql.ErrNoRows) {
		return api.WorkspaceTaskLink{}, fail(http.StatusConflict, errorCodeInvalidRequest, "session missing or task association changed; refresh before retrying")
	}
	if err != nil {
		return api.WorkspaceTaskLink{}, storeFailure(err)
	}
	return views.WorkspaceTaskLink(row), nil
}

func (adapter *CopilotAdapter) LinkSessionTask(ctx context.Context, request api.LinkSessionTaskRequestObject) (api.LinkSessionTaskResponseObject, error) {
	if request.Body == nil {
		return nil, fail(http.StatusBadRequest, errorCodeInvalidRequest, "task association is required")
	}
	link, err := adapter.LinkWorkspaceTask(requestContext(ctx), request.SessionId, request.Body.TaskUrl, request.Body.ExpectedGeneration)
	if err != nil {
		return nil, err
	}
	return api.LinkSessionTask200JSONResponse(link), nil
}

func (adapter *CopilotAdapter) RefreshWorkspace(ctx context.Context, request api.RefreshWorkspaceRequestObject) (api.RefreshWorkspaceResponseObject, error) {
	links, err := adapter.store.ListSessionTasks(requestContext(ctx))
	if err != nil {
		return nil, storeFailure(err)
	}
	seen := map[string]bool{}
	for _, link := range links {
		if !seen[link.TaskUrl] {
			adapter.WorkspaceTask(ctx, link.TaskUrl, true)
			seen[link.TaskUrl] = true
		}
	}
	adapter.InvalidateWorkspace()
	return api.RefreshWorkspace204Response{}, nil
}

// MoveWorkspaceTask publishes a prepared checkpoint and verifies its retained
// receipt. The caller supplies the observed association and checkpoint tip.
// The existing session mutation owner serializes moves with relinking in this
// host. GitHub's stale/fork checks still apply; comments are not a remote CAS.
func (adapter *CopilotAdapter) MoveWorkspaceTask(ctx context.Context, link api.WorkspaceTaskLink, expectedID string, status workv1.WorkStatus, nextAction, reason string) (*workv1.ResumeRecord, error) {
	if adapter.tasks.source == nil {
		return nil, errors.New("the host has not configured a task authority")
	}
	unlock := adapter.mutations.lock(link.SessionId)
	defer unlock()
	retained, err := adapter.store.GetSessionTask(ctx, link.SessionId)
	if errors.Is(err, sql.ErrNoRows) || (err == nil && (retained.TaskUrl != link.TaskUrl || retained.Generation != link.Generation)) {
		adapter.InvalidateWorkspace()
		return nil, fmt.Errorf("%w: task association changed; refresh before moving it", workcontinuity.ErrStale)
	}
	if err != nil {
		return nil, storeFailure(err)
	}
	taskURL := retained.TaskUrl
	current := adapter.WorkspaceTask(ctx, taskURL, true)
	if current.Condition != workv1.ResumeCondition_RESUME_CONDITION_READY || expectedID == "" || current.GetCheckpoint().GetId() != expectedID {
		adapter.InvalidateWorkspace()
		return current, fmt.Errorf("%w: refresh the task before moving it", workcontinuity.ErrStale)
	}
	checkpoint := proto.Clone(current.Checkpoint).(*workv1.Checkpoint)
	checkpoint.Id, checkpoint.PredecessorId = uuid.NewString(), expectedID
	checkpoint.Status, checkpoint.NextAction = status, nextAction
	checkpoint.RecordedAt, checkpoint.TraceContext = timestamppb.New(time.Now().UTC()), nil
	if reason != "" {
		checkpoint.Blockers = []string{reason}
	} else {
		checkpoint.Blockers = nil
	}
	result, err := adapter.tasks.source.Checkpoint(ctx, taskURL, checkpoint)
	adapter.WorkspaceTask(ctx, taskURL, true)
	adapter.InvalidateWorkspace()
	return result, err
}
