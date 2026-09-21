package candaceos

import (
	"fmt"
	"time"
)

// ApprovalDecision captures both an open request and its terminal decision.
type ApprovalDecision string

const (
	ApprovalPending  ApprovalDecision = "pending"
	ApprovalApproved ApprovalDecision = "approved"
	ApprovalDenied   ApprovalDecision = "denied"
)

// Approval is a human decision about a named action in a run. Reason is
// optional operator context and is retained with either terminal decision.
type Approval struct {
	ID          string           `json:"id" yaml:"id"`
	RunID       string           `json:"run_id" yaml:"run_id"`
	Action      string           `json:"action" yaml:"action"`
	Decision    ApprovalDecision `json:"decision" yaml:"decision"`
	RequestedAt time.Time        `json:"requested_at" yaml:"requested_at"`
	DecidedAt   *time.Time       `json:"decided_at,omitempty" yaml:"decided_at,omitempty"`
	DecidedBy   string           `json:"decided_by,omitempty" yaml:"decided_by,omitempty"`
	Reason      string           `json:"reason,omitempty" yaml:"reason,omitempty"`
}

// Validate checks the request and ensures terminal decisions have complete,
// chronologically valid attribution.
func (approval Approval) Validate() error {
	if err := validateID("id", approval.ID); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidApproval, err)
	}
	if err := validateID("run_id", approval.RunID); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidApproval, err)
	}
	if approval.Action == "" {
		return fmt.Errorf("%w: action is required", ErrInvalidApproval)
	}
	if approval.RequestedAt.IsZero() {
		return fmt.Errorf("%w: requested_at is required", ErrInvalidApproval)
	}
	switch approval.Decision {
	case ApprovalPending:
		if approval.DecidedAt != nil || approval.DecidedBy != "" || approval.Reason != "" {
			return fmt.Errorf("%w: pending approvals cannot contain decision fields", ErrInvalidApproval)
		}
	case ApprovalApproved, ApprovalDenied:
		if approval.DecidedAt == nil {
			return fmt.Errorf("%w: decided_at is required for a terminal decision", ErrInvalidApproval)
		}
		if approval.DecidedAt.Before(approval.RequestedAt) {
			return fmt.Errorf("%w: decided_at cannot precede requested_at", ErrInvalidApproval)
		}
		if err := validateID("decided_by", approval.DecidedBy); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidApproval, err)
		}
	default:
		return fmt.Errorf("%w: unknown decision %q", ErrInvalidApproval, approval.Decision)
	}
	return nil
}
