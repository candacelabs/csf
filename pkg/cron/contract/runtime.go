package contract

import (
	"fmt"
	"sort"
	"time"

	cron "github.com/candacelabs/csf/pkg/cron"
	cronv1 "github.com/candacelabs/csf/pkg/cron/v1"
	"github.com/candacelabs/csf/pkg/liquidproto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// NewJobDefinitionCodec returns a validating Liquid Proto codec for portable
// desired job definitions.
func NewJobDefinitionCodec() (*liquidproto.Codec[*cronv1.JobDefinition], error) {
	return liquidproto.NewCodec(
		func() *cronv1.JobDefinition { return new(cronv1.JobDefinition) },
		ValidateJobDefinition,
	)
}

// NewStatusSnapshotCodec returns a validating Liquid Proto codec for read-only
// runtime status snapshots.
func NewStatusSnapshotCodec() (*liquidproto.Codec[*cronv1.StatusSnapshot], error) {
	return liquidproto.NewCodec(
		func() *cronv1.StatusSnapshot { return new(cronv1.StatusSnapshot) },
		ValidateStatusSnapshot,
	)
}

// JobDefinitionToProto maps a persistence-neutral domain definition to its
// portable boundary representation.
func JobDefinitionToProto(definition cron.JobDefinition) (*cronv1.JobDefinition, error) {
	schedule, err := cron.ScheduleFromDefinition(definition.Schedule)
	if err != nil {
		return nil, fmt.Errorf("%w: job %q schedule: %v", ErrInvalid, definition.Name, err)
	}
	scheduleMessage, err := ScheduleToProto(schedule)
	if err != nil {
		return nil, err
	}
	catchUp, err := catchUpToProto(definition.CatchUp)
	if err != nil {
		return nil, err
	}
	overlap, err := overlapToProto(definition.Overlap)
	if err != nil {
		return nil, err
	}
	message := &cronv1.JobDefinition{
		Name:          definition.Name,
		Schedule:      scheduleMessage,
		CatchUpPolicy: catchUp,
		OverlapPolicy: overlap,
	}
	if err := ValidateJobDefinition(message); err != nil {
		return nil, err
	}
	return message, nil
}

// JobDefinitionFromProto validates and maps a boundary job definition into
// the domain without introducing a wire-shaped persistence model.
func JobDefinitionFromProto(message *cronv1.JobDefinition) (cron.JobDefinition, error) {
	if message == nil {
		return cron.JobDefinition{}, fmt.Errorf("%w: job definition is required", ErrInvalid)
	}
	if err := cronv1.ValidateJobDefinition(message); err != nil {
		return cron.JobDefinition{}, fmt.Errorf("%w: job scalar refinements: %w", ErrInvalid, err)
	}
	schedule, err := ScheduleFromProto(message.GetSchedule())
	if err != nil {
		return cron.JobDefinition{}, err
	}
	scheduleDefinition, err := schedule.Definition()
	if err != nil {
		return cron.JobDefinition{}, fmt.Errorf("%w: normalized schedule: %v", ErrInvalid, err)
	}
	catchUp, err := catchUpFromProto(message.GetCatchUpPolicy())
	if err != nil {
		return cron.JobDefinition{}, err
	}
	overlap, err := overlapFromProto(message.GetOverlapPolicy())
	if err != nil {
		return cron.JobDefinition{}, err
	}
	return cron.JobDefinition{
		Name:     message.GetName(),
		Schedule: scheduleDefinition,
		CatchUp:  catchUp,
		Overlap:  overlap,
	}, nil
}

// ValidateJobDefinition completes generated scalar validation with nested
// schedule and policy semantics.
func ValidateJobDefinition(message *cronv1.JobDefinition) error {
	_, err := JobDefinitionFromProto(message)
	return err
}

// OccurrenceToProto maps one durable execution record to a status boundary.
func OccurrenceToProto(record cron.OccurrenceRecord) (*cronv1.RunSummary, error) {
	state, err := runStateToProto(record.Status)
	if err != nil {
		return nil, err
	}
	invocation := &cronv1.Invocation{
		OccurrenceId: record.ID,
		JobName:      record.JobName,
		ScheduledAt:  timestamppb.New(record.ScheduledAt),
		Attempt:      record.Attempt,
	}
	if !record.StartedAt.IsZero() {
		invocation.StartedAt = timestamppb.New(record.StartedAt)
	}
	message := &cronv1.RunSummary{
		Invocation:   invocation,
		State:        state,
		WorkerId:     record.LeaseOwner,
		ErrorSummary: record.Error,
		SkipReason:   record.SkipReason,
	}
	if !record.FinishedAt.IsZero() {
		message.FinishedAt = timestamppb.New(record.FinishedAt)
	}
	if err := ValidateRunSummary(message); err != nil {
		return nil, err
	}
	return message, nil
}

// ValidateInvocation validates a handler invocation boundary. A standalone
// invocation always represents an actual attempt, so attempt must be nonzero.
func ValidateInvocation(message *cronv1.Invocation) error {
	return validateInvocation(message, false)
}

// ValidateRunSummary applies generated refinements and state-dependent
// timestamp, attempt, error, and skip semantics.
func ValidateRunSummary(message *cronv1.RunSummary) error {
	if message == nil {
		return fmt.Errorf("%w: run summary is required", ErrInvalid)
	}
	if err := cronv1.ValidateRunSummary(message); err != nil {
		return fmt.Errorf("%w: run scalar refinements: %w", ErrInvalid, err)
	}
	state := message.GetState()
	if state == cronv1.RunState_RUN_STATE_UNSPECIFIED {
		return fmt.Errorf("%w: run state is required", ErrInvalid)
	}
	allowUnattempted := state == cronv1.RunState_RUN_STATE_SKIPPED || state == cronv1.RunState_RUN_STATE_PENDING
	if err := validateInvocation(message.GetInvocation(), allowUnattempted); err != nil {
		return err
	}

	finished := message.GetFinishedAt()
	switch state {
	case cronv1.RunState_RUN_STATE_PENDING:
		if message.GetInvocation().GetStartedAt() != nil || finished != nil {
			return fmt.Errorf("%w: pending run cannot have execution timestamps", ErrInvalid)
		}
	case cronv1.RunState_RUN_STATE_RUNNING:
		if finished != nil || message.GetWorkerId() == "" {
			return fmt.Errorf("%w: running run requires worker_id and no finished_at", ErrInvalid)
		}
	case cronv1.RunState_RUN_STATE_SUCCEEDED,
		cronv1.RunState_RUN_STATE_FAILED,
		cronv1.RunState_RUN_STATE_CANCELED:
		if finished == nil {
			return fmt.Errorf("%w: terminal run requires finished_at", ErrInvalid)
		}
	case cronv1.RunState_RUN_STATE_SKIPPED:
		if finished == nil || message.GetSkipReason() == "" {
			return fmt.Errorf("%w: skipped run requires reason and finished_at", ErrInvalid)
		}
	default:
		return fmt.Errorf("%w: unsupported run state %d", ErrInvalid, state)
	}
	if finished != nil {
		if err := finished.CheckValid(); err != nil {
			return fmt.Errorf("%w: finished_at: %v", ErrInvalid, err)
		}
	}
	if state != cronv1.RunState_RUN_STATE_FAILED && state != cronv1.RunState_RUN_STATE_CANCELED && message.GetErrorSummary() != "" {
		return fmt.Errorf("%w: only failed or canceled runs may have error_summary", ErrInvalid)
	}
	if state != cronv1.RunState_RUN_STATE_SKIPPED && message.GetSkipReason() != "" {
		return fmt.Errorf("%w: only skipped runs may have skip_reason", ErrInvalid)
	}
	return nil
}

// StoreSnapshotToProto builds a stable, read-only boundary projection. It
// derives active counts and the latest run rather than treating protobuf as
// scheduler state.
func StoreSnapshotToProto(snapshot cron.StoreSnapshot, observedAt time.Time) (*cronv1.StatusSnapshot, error) {
	if observedAt.IsZero() {
		return nil, fmt.Errorf("%w: observed_at is required", ErrInvalid)
	}
	occurrences := append([]cron.OccurrenceRecord(nil), snapshot.Occurrences...)
	sort.Slice(occurrences, func(left, right int) bool {
		if occurrences[left].ScheduledAt.Equal(occurrences[right].ScheduledAt) {
			return occurrences[left].ID < occurrences[right].ID
		}
		return occurrences[left].ScheduledAt.Before(occurrences[right].ScheduledAt)
	})

	message := &cronv1.StatusSnapshot{ObservedAt: timestamppb.New(observedAt)}
	jobs := append([]cron.JobState(nil), snapshot.Jobs...)
	sort.Slice(jobs, func(left, right int) bool {
		return jobs[left].Definition.Name < jobs[right].Definition.Name
	})
	for _, state := range jobs {
		definition, err := JobDefinitionToProto(state.Definition)
		if err != nil {
			return nil, err
		}
		status := &cronv1.JobStatus{
			Definition: definition,
			NextRunAt:  timestamppb.New(state.NextRunAt),
		}
		if state.Definition.Schedule.HasAnchor {
			status.IntervalAnchor = timestamppb.New(state.Definition.Schedule.Anchor)
		}
		for _, occurrence := range occurrences {
			if occurrence.JobName != state.Definition.Name {
				continue
			}
			if occurrence.Status == cron.OccurrenceRunning {
				status.ActiveRuns++
			}
			run, err := OccurrenceToProto(occurrence)
			if err != nil {
				return nil, err
			}
			status.LastRun = run
		}
		message.Jobs = append(message.Jobs, status)
	}
	if err := ValidateStatusSnapshot(message); err != nil {
		return nil, err
	}
	return message, nil
}

// ValidateStatusSnapshot validates a complete read-only status projection.
func ValidateStatusSnapshot(message *cronv1.StatusSnapshot) error {
	if message == nil {
		return fmt.Errorf("%w: status snapshot is required", ErrInvalid)
	}
	if message.GetObservedAt() == nil {
		return fmt.Errorf("%w: observed_at is required", ErrInvalid)
	}
	if err := message.GetObservedAt().CheckValid(); err != nil {
		return fmt.Errorf("%w: observed_at: %v", ErrInvalid, err)
	}
	seen := make(map[string]struct{}, len(message.GetJobs()))
	for _, status := range message.GetJobs() {
		if status == nil {
			return fmt.Errorf("%w: nil job status", ErrInvalid)
		}
		if err := cronv1.ValidateJobStatus(status); err != nil {
			return fmt.Errorf("%w: job status scalar refinements: %w", ErrInvalid, err)
		}
		if err := ValidateJobDefinition(status.GetDefinition()); err != nil {
			return err
		}
		name := status.GetDefinition().GetName()
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("%w: duplicate job status %q", ErrInvalid, name)
		}
		seen[name] = struct{}{}
		if status.GetNextRunAt() == nil {
			return fmt.Errorf("%w: job %q next_run_at is required", ErrInvalid, name)
		}
		if err := status.GetNextRunAt().CheckValid(); err != nil {
			return fmt.Errorf("%w: job %q next_run_at: %v", ErrInvalid, name, err)
		}
		if status.GetIntervalAnchor() != nil {
			if err := status.GetIntervalAnchor().CheckValid(); err != nil {
				return fmt.Errorf("%w: job %q interval_anchor: %v", ErrInvalid, name, err)
			}
		}
		if status.GetLastRun() != nil {
			if err := ValidateRunSummary(status.GetLastRun()); err != nil {
				return err
			}
			if status.GetLastRun().GetInvocation().GetJobName() != name {
				return fmt.Errorf("%w: job status/run name mismatch", ErrInvalid)
			}
		}
	}
	return nil
}

func validateInvocation(message *cronv1.Invocation, allowUnattempted bool) error {
	if message == nil {
		return fmt.Errorf("%w: invocation is required", ErrInvalid)
	}
	if err := cronv1.ValidateInvocation(message); err != nil {
		return fmt.Errorf("%w: invocation scalar refinements: %w", ErrInvalid, err)
	}
	if message.GetScheduledAt() == nil {
		return fmt.Errorf("%w: scheduled_at is required", ErrInvalid)
	}
	if err := message.GetScheduledAt().CheckValid(); err != nil {
		return fmt.Errorf("%w: scheduled_at: %v", ErrInvalid, err)
	}
	if message.GetOccurrenceId() != cron.OccurrenceID(message.GetJobName(), message.GetScheduledAt().AsTime()) {
		return fmt.Errorf("%w: occurrence_id does not match job_name and scheduled_at", ErrInvalid)
	}
	if message.GetAttempt() == 0 && !allowUnattempted {
		return fmt.Errorf("%w: attempted invocation requires a nonzero attempt", ErrInvalid)
	}
	if message.GetAttempt() > 0 {
		if message.GetStartedAt() == nil {
			return fmt.Errorf("%w: attempted invocation requires started_at", ErrInvalid)
		}
		if err := message.GetStartedAt().CheckValid(); err != nil {
			return fmt.Errorf("%w: started_at: %v", ErrInvalid, err)
		}
	}
	return nil
}

func catchUpToProto(policy cron.CatchUpPolicy) (cronv1.CatchUpPolicy, error) {
	switch policy {
	case cron.CatchUpNone:
		return cronv1.CatchUpPolicy_CATCH_UP_POLICY_NONE, nil
	case cron.CatchUpLatest:
		return cronv1.CatchUpPolicy_CATCH_UP_POLICY_LATEST, nil
	case cron.CatchUpAll:
		return cronv1.CatchUpPolicy_CATCH_UP_POLICY_ALL, nil
	default:
		return 0, fmt.Errorf("%w: unknown catch-up policy %q", ErrInvalid, policy)
	}
}

func catchUpFromProto(policy cronv1.CatchUpPolicy) (cron.CatchUpPolicy, error) {
	switch policy {
	case cronv1.CatchUpPolicy_CATCH_UP_POLICY_NONE:
		return cron.CatchUpNone, nil
	case cronv1.CatchUpPolicy_CATCH_UP_POLICY_LATEST:
		return cron.CatchUpLatest, nil
	case cronv1.CatchUpPolicy_CATCH_UP_POLICY_ALL:
		return cron.CatchUpAll, nil
	default:
		return "", fmt.Errorf("%w: catch-up policy is required", ErrInvalid)
	}
}

func overlapToProto(policy cron.OverlapPolicy) (cronv1.OverlapPolicy, error) {
	switch policy {
	case cron.OverlapSkip:
		return cronv1.OverlapPolicy_OVERLAP_POLICY_SKIP, nil
	case cron.OverlapAllow:
		return cronv1.OverlapPolicy_OVERLAP_POLICY_ALLOW, nil
	default:
		return 0, fmt.Errorf("%w: unknown overlap policy %q", ErrInvalid, policy)
	}
}

func overlapFromProto(policy cronv1.OverlapPolicy) (cron.OverlapPolicy, error) {
	switch policy {
	case cronv1.OverlapPolicy_OVERLAP_POLICY_SKIP:
		return cron.OverlapSkip, nil
	case cronv1.OverlapPolicy_OVERLAP_POLICY_ALLOW:
		return cron.OverlapAllow, nil
	default:
		return "", fmt.Errorf("%w: overlap policy is required", ErrInvalid)
	}
}

func runStateToProto(status cron.OccurrenceStatus) (cronv1.RunState, error) {
	switch status {
	case cron.OccurrenceRunning:
		return cronv1.RunState_RUN_STATE_RUNNING, nil
	case cron.OccurrenceSucceeded:
		return cronv1.RunState_RUN_STATE_SUCCEEDED, nil
	case cron.OccurrenceFailed:
		return cronv1.RunState_RUN_STATE_FAILED, nil
	case cron.OccurrenceCanceled:
		return cronv1.RunState_RUN_STATE_CANCELED, nil
	case cron.OccurrenceSkipped:
		return cronv1.RunState_RUN_STATE_SKIPPED, nil
	default:
		return 0, fmt.Errorf("%w: unknown occurrence status %q", ErrInvalid, status)
	}
}
