package candaceos

import (
	"fmt"
	"time"
)

// RunStatus is the lifecycle of one concrete deployment attempt on one node.
type RunStatus string

const (
	RunStatusQueued           RunStatus = "queued"
	RunStatusAwaitingApproval RunStatus = "awaiting_approval"
	RunStatusRunning          RunStatus = "running"
	RunStatusSucceeded        RunStatus = "succeeded"
	RunStatusFailed           RunStatus = "failed"
	RunStatusCanceled         RunStatus = "canceled"
)

// Run captures one attempt against the exact application revision selected
// when it was queued, so later desired-state edits cannot rewrite history.
type Run struct {
	ID            string     `json:"id" yaml:"id"`
	DeploymentID  string     `json:"deployment_id" yaml:"deployment_id"`
	AppRevisionID string     `json:"app_revision_id" yaml:"app_revision_id"`
	NodeID        string     `json:"node_id" yaml:"node_id"`
	Status        RunStatus  `json:"status" yaml:"status"`
	RequestedAt   time.Time  `json:"requested_at" yaml:"requested_at"`
	StartedAt     *time.Time `json:"started_at,omitempty" yaml:"started_at,omitempty"`
	FinishedAt    *time.Time `json:"finished_at,omitempty" yaml:"finished_at,omitempty"`
}

// Validate checks identity, lifecycle fields, and timestamp ordering.
func (run Run) Validate() error {
	identities := []struct {
		field string
		value string
	}{
		{field: "id", value: run.ID},
		{field: "deployment_id", value: run.DeploymentID},
		{field: "app_revision_id", value: run.AppRevisionID},
		{field: "node_id", value: run.NodeID},
	}
	for _, identity := range identities {
		if err := validateID(identity.field, identity.value); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidRun, err)
		}
	}
	if run.RequestedAt.IsZero() {
		return fmt.Errorf("%w: requested_at is required", ErrInvalidRun)
	}
	if run.StartedAt != nil && run.StartedAt.Before(run.RequestedAt) {
		return fmt.Errorf("%w: started_at cannot precede requested_at", ErrInvalidRun)
	}
	if run.FinishedAt != nil {
		if run.FinishedAt.Before(run.RequestedAt) {
			return fmt.Errorf("%w: finished_at cannot precede requested_at", ErrInvalidRun)
		}
		if run.StartedAt != nil && run.FinishedAt.Before(*run.StartedAt) {
			return fmt.Errorf("%w: finished_at cannot precede started_at", ErrInvalidRun)
		}
	}

	switch run.Status {
	case RunStatusQueued, RunStatusAwaitingApproval:
		if run.StartedAt != nil || run.FinishedAt != nil {
			return fmt.Errorf("%w: %s runs cannot have started_at or finished_at", ErrInvalidRun, run.Status)
		}
	case RunStatusRunning:
		if run.StartedAt == nil || run.FinishedAt != nil {
			return fmt.Errorf("%w: running runs require started_at and forbid finished_at", ErrInvalidRun)
		}
	case RunStatusSucceeded, RunStatusFailed:
		if run.StartedAt == nil || run.FinishedAt == nil {
			return fmt.Errorf("%w: %s runs require started_at and finished_at", ErrInvalidRun, run.Status)
		}
	case RunStatusCanceled:
		if run.FinishedAt == nil {
			return fmt.Errorf("%w: canceled runs require finished_at", ErrInvalidRun)
		}
	default:
		return fmt.Errorf("%w: unknown status %q", ErrInvalidRun, run.Status)
	}
	return nil
}
