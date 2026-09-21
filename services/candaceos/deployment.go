package candaceos

import "fmt"

// DesiredState is the operator's intended deployment state.
type DesiredState string

const (
	DesiredStateRunning DesiredState = "running"
	DesiredStateStopped DesiredState = "stopped"
)

// Placement is a closed, user-facing choice. Exactly one variant must be set.
// Pointer variants make invalid combinations detectable at configuration
// boundaries without an interface-backed union that is awkward to encode.
type Placement struct {
	ExactNode   *ExactNodePlacement   `json:"exact_node,omitempty" yaml:"exact_node,omitempty"`
	Leader      *LeaderPlacement      `json:"leader,omitempty" yaml:"leader,omitempty"`
	MatchLabels *MatchLabelsPlacement `json:"match_labels,omitempty" yaml:"match_labels,omitempty"`
}

// ExactNodePlacement pins one deployment replica to one durable node identity.
type ExactNodePlacement struct {
	NodeID string `json:"node_id" yaml:"node_id"`
}

// LeaderPlacement follows the current authoritative cluster leader.
type LeaderPlacement struct{}

// MatchLabelsPlacement selects a deterministic number of alive nodes whose
// labels contain every exact key/value match.
type MatchLabelsPlacement struct {
	Labels   map[string]string `json:"labels" yaml:"labels"`
	Replicas int               `json:"replicas" yaml:"replicas"`
}

// Validate checks that exactly one complete placement variant is selected.
func (placement Placement) Validate() error {
	variants := 0
	if placement.ExactNode != nil {
		variants++
	}
	if placement.Leader != nil {
		variants++
	}
	if placement.MatchLabels != nil {
		variants++
	}
	if variants != 1 {
		return fmt.Errorf("%w: exactly one of exact_node, leader, or match_labels must be set", ErrInvalidPlacement)
	}

	if placement.ExactNode != nil {
		if err := validateID("exact_node.node_id", placement.ExactNode.NodeID); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidPlacement, err)
		}
	}
	if placement.MatchLabels != nil {
		if err := validateLabels("match_labels.labels", placement.MatchLabels.Labels, true); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidPlacement, err)
		}
		if placement.MatchLabels.Replicas < 1 {
			return fmt.Errorf("%w: match_labels.replicas must be at least 1", ErrInvalidPlacement)
		}
	}
	return nil
}

// Deployment is desired state. AppRevisionID points to one immutable
// AppRevision; changing source creates another revision instead of mutating it.
type Deployment struct {
	ID            string       `json:"id" yaml:"id"`
	AppRevisionID string       `json:"app_revision_id" yaml:"app_revision_id"`
	DesiredState  DesiredState `json:"desired_state" yaml:"desired_state"`
	Placement     Placement    `json:"placement" yaml:"placement"`
	Stateful      bool         `json:"stateful" yaml:"stateful"`
}

// Validate checks the desired-state contract. Stateful workloads must remain
// pinned to an exact node so leader changes or label ordering cannot move data.
func (deployment Deployment) Validate() error {
	if err := validateID("id", deployment.ID); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidDeployment, err)
	}
	if err := validateID("app_revision_id", deployment.AppRevisionID); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidDeployment, err)
	}
	switch deployment.DesiredState {
	case DesiredStateRunning, DesiredStateStopped:
	default:
		return fmt.Errorf("%w: desired_state must be %q or %q", ErrInvalidDeployment, DesiredStateRunning, DesiredStateStopped)
	}
	if err := deployment.Placement.Validate(); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidDeployment, err)
	}
	if deployment.Stateful && deployment.Placement.ExactNode == nil {
		return fmt.Errorf("%w: stateful workloads require exact_node placement", ErrInvalidDeployment)
	}
	return nil
}
