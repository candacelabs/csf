package workcontinuity

import (
	"context"
	"fmt"
	"time"

	"github.com/candacelabs/csf/pkg/telemetry"
	telemetryv1 "github.com/candacelabs/csf/proto/candace/telemetry/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	workv1 "github.com/candacelabs/csf/proto/candace/work/v1"
)

const (
	eventCheckpointIntent = "work.checkpoint.intent"
	eventCheckpointStored = "work.checkpoint.stored"
	eventResumed          = "work.resumed"
	attributeTask         = "task_url"
	attributeCheckpoint   = "checkpoint_id"
	attributeRevision     = "revision"
)

// Continuity validates and records handoffs against an authoritative issue source.
type Continuity struct {
	source ISource
	logger *telemetry.JSONLLogger
	maxAge time.Duration
}

type Option func(service *Continuity)

func WithSource(source ISource) Option { return func(service *Continuity) { service.source = source } }
func WithLogger(logger *telemetry.JSONLLogger) Option {
	return func(service *Continuity) { service.logger = logger }
}
func WithMaxAge(maxAge time.Duration) Option {
	return func(service *Continuity) { service.maxAge = maxAge }
}

// NewContinuity validates the source, trace sink and freshness policy before use.
func NewContinuity(options ...Option) (*Continuity, error) {
	service := &Continuity{maxAge: DefaultMaxAge}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("work continuity option is nil")
		}
		option(service)
	}
	if service.source == nil || service.logger == nil || service.maxAge <= 0 {
		return nil, fmt.Errorf("work continuity requires source, logger and positive max age")
	}
	return service, nil
}

func (service *Continuity) Resume(ctx context.Context, taskURL, revision string) (*workv1.ResumeRecord, error) {
	snapshot, err := service.source.Load(ctx, taskURL)
	if err != nil {
		return nil, err
	}
	record := Resume(snapshot, revision, time.Now(), service.maxAge)
	if err := service.log(ctx, eventResumed, record.Checkpoint, taskURL); err != nil {
		return record, err
	}
	return record, nil
}

// Checkpoint appends a validated receipt, verifies it by rereading the source,
// and rejects concurrent forks. It does not claim a GitHub transaction or lease.
func (service *Continuity) Checkpoint(ctx context.Context, taskURL string, input *workv1.Checkpoint) (*workv1.ResumeRecord, error) {
	if input == nil {
		return nil, fmt.Errorf("%w: missing input", ErrInvalid)
	}
	snapshot, err := service.source.Load(ctx, taskURL)
	if err != nil {
		return nil, err
	}
	chain, err := history(snapshot)
	if err != nil {
		return nil, err
	}
	checkpoint := proto.Clone(input).(*workv1.Checkpoint)
	var existing *workv1.Checkpoint
	for _, item := range chain {
		if item.checkpoint.Id == checkpoint.Id {
			existing = item.checkpoint
		}
	}
	if checkpoint.SchemaVersion == 0 {
		checkpoint.SchemaVersion = SchemaVersion
	}
	if checkpoint.TaskUrl == "" {
		checkpoint.TaskUrl = taskURL
	}
	if checkpoint.TaskUrl != taskURL {
		return nil, fmt.Errorf("%w: task URL mismatch", ErrInvalid)
	}
	if existing != nil {
		if checkpoint.RecordedAt == nil {
			checkpoint.RecordedAt = existing.RecordedAt
		}
		if checkpoint.TraceContext == nil {
			checkpoint.TraceContext = existing.TraceContext
		}
		if checkpoint.TaskFingerprint == "" {
			checkpoint.TaskFingerprint = existing.TaskFingerprint
		}
		if checkpoint.PredecessorId == "" {
			checkpoint.PredecessorId = existing.PredecessorId
		}
		if !proto.Equal(checkpoint, existing) {
			return nil, fmt.Errorf("%w: reused checkpoint ID differs", ErrConflict)
		}
		return Resume(snapshot, checkpoint.Revision, time.Now(), service.maxAge), nil
	}
	if checkpoint.RecordedAt == nil {
		checkpoint.RecordedAt = timestamppb.Now()
	}
	if checkpoint.TraceContext == nil {
		checkpoint.TraceContext, err = telemetry.NewTraceContext(telemetry.TraceFlagsSampled)
		if err != nil {
			return nil, err
		}
	}
	if checkpoint.TaskFingerprint == "" {
		checkpoint.TaskFingerprint = Fingerprint(snapshot.Issue)
	}
	if checkpoint.TaskFingerprint != Fingerprint(snapshot.Issue) {
		return nil, ErrStale
	}
	parent := ""
	if len(chain) > 0 {
		parent = chain[len(chain)-1].checkpoint.Id
	}
	if checkpoint.PredecessorId == "" {
		checkpoint.PredecessorId = parent
	}
	if checkpoint.PredecessorId != parent {
		return nil, fmt.Errorf("%w: predecessor is not current tip", ErrStale)
	}
	body, err := EncodeCheckpoint(checkpoint)
	if err != nil {
		return nil, err
	}
	if !checkpointFresh(checkpoint, time.Now(), service.maxAge) {
		return nil, fmt.Errorf("%w: timestamp is in the future or outside the freshness window", ErrStale)
	}
	if err := service.log(ctx, eventCheckpointIntent, checkpoint, taskURL); err != nil {
		return nil, err
	}
	stored, err := service.source.Append(ctx, taskURL, body)
	if err != nil {
		return nil, fmt.Errorf("append may be ambiguous; retry the same ID after inspecting source: %w", err)
	}
	snapshot, err = service.source.Load(ctx, taskURL)
	if err != nil {
		return nil, fmt.Errorf("stored at %s but reread failed: %w", stored.HtmlUrl, err)
	}
	record := Resume(snapshot, checkpoint.Revision, time.Now(), service.maxAge)
	if record.Condition != workv1.ResumeCondition_RESUME_CONDITION_READY || record.Checkpoint.GetId() != checkpoint.Id {
		return record, fmt.Errorf("stored at %s but verification requires reconciliation: %s", stored.HtmlUrl, record.Condition)
	}
	if err := service.log(ctx, eventCheckpointStored, checkpoint, taskURL); err != nil {
		return record, fmt.Errorf("stored at %s but trace write failed: %w", stored.HtmlUrl, err)
	}
	return record, nil
}

func (service *Continuity) log(ctx context.Context, event string, checkpoint *workv1.Checkpoint, taskURL string) error {
	if checkpoint.GetTraceContext() != nil {
		var err error
		ctx, err = telemetry.ContextWithTrace(ctx, checkpoint.TraceContext)
		if err != nil {
			return err
		}
		ctx, _, err = telemetry.ContextWithChildSpan(ctx)
		if err != nil {
			return err
		}
	}
	return service.logger.Log(ctx, telemetryv1.Severity_SEVERITY_INFO, event, event, map[string]string{
		attributeTask: taskURL, attributeCheckpoint: checkpoint.GetId(), attributeRevision: checkpoint.GetRevision(),
	})
}
