package fleet

import "time"

const nodeRoleLabel = "role"

// ConfiguredNode combines fixed deployment configuration with Warden's
// observed membership state. Leadership remains a property of the containing
// snapshot and is never inferred from Role.
type ConfiguredNode struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Role     string            `json:"role"`
	Labels   map[string]string `json:"labels"`
	Status   string            `json:"status"`
	Address  string            `json:"address"`
	LastSeen time.Time         `json:"last_seen"`
}

// ConfiguredSnapshot is the operator-facing fleet status. LeaderID and Term
// are dynamic Warden observations; node Role and Labels are fixed Core
// configuration used by placement.
type ConfiguredSnapshot struct {
	Self          string           `json:"self"`
	LeaderID      string           `json:"leader_id"`
	Term          uint64           `json:"term"`
	Authoritative bool             `json:"authoritative"`
	HasQuorum     bool             `json:"has_quorum"`
	Online        int              `json:"online"`
	Required      int              `json:"required"`
	Nodes         []ConfiguredNode `json:"nodes"`
	UpdatedAt     time.Time        `json:"updated_at"`
	Error         string           `json:"error,omitempty"`
}

// WithConfiguration projects configured labels onto a Warden snapshot without
// mutating either input.
func WithConfiguration(snapshot Snapshot, configured map[string]map[string]string) ConfiguredSnapshot {
	result := ConfiguredSnapshot{
		Self: snapshot.Self, LeaderID: snapshot.LeaderID, Term: snapshot.Term,
		Authoritative: snapshot.Authoritative, HasQuorum: snapshot.HasQuorum,
		Online: snapshot.Online, Required: snapshot.Required,
		Nodes:     make([]ConfiguredNode, 0, len(snapshot.Nodes)),
		UpdatedAt: snapshot.UpdatedAt, Error: snapshot.Error,
	}
	for _, observed := range snapshot.Nodes {
		labels := cloneLabels(configured[observed.ID])
		result.Nodes = append(result.Nodes, ConfiguredNode{
			ID: observed.ID, Name: observed.Name,
			Role: deploymentRole(labels), Labels: labels,
			Status: observed.Status, Address: observed.Address, LastSeen: observed.LastSeen,
		})
	}
	return result
}

func deploymentRole(labels map[string]string) string {
	if role := labels[nodeRoleLabel]; role != "" {
		return role
	}
	return "worker"
}

func cloneLabels(source map[string]string) map[string]string {
	result := make(map[string]string, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
