package candaceos

import (
	"fmt"
	"sort"

	"google.golang.org/protobuf/proto"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

// ClusterSnapshot is the minimum authoritative Warden state needed for a pure
// placement decision. ResolvePlacement never probes or mutates nodes.
type ClusterSnapshot struct {
	Nodes         []*candaceosv1.Node `json:"nodes" yaml:"nodes"`
	LeaderNodeID  string              `json:"leader_node_id,omitempty" yaml:"leader_node_id,omitempty"`
	Authoritative bool                `json:"authoritative" yaml:"authoritative"`
	HasQuorum     bool                `json:"has_quorum" yaml:"has_quorum"`
}

// Validate checks node identity uniqueness and any supplied leader identity.
func (snapshot ClusterSnapshot) Validate() error {
	seen := make(map[string]struct{}, len(snapshot.Nodes))
	for index, node := range snapshot.Nodes {
		if err := candaceosv1.ValidateNode(node); err != nil {
			return fmt.Errorf("%w: nodes[%d]: %w: %w", ErrInvalidClusterSnapshot, index, ErrInvalidNode, err)
		}
		if _, exists := seen[node.GetId()]; exists {
			return fmt.Errorf("%w: duplicate node id %q", ErrInvalidClusterSnapshot, node.GetId())
		}
		seen[node.GetId()] = struct{}{}
	}
	if snapshot.LeaderNodeID != "" {
		if err := validateID("leader_node_id", snapshot.LeaderNodeID); err != nil {
			return fmt.Errorf("%w: %w", ErrInvalidClusterSnapshot, err)
		}
	}
	return nil
}

// ResolvePlacement returns the complete target set for a deployment. Running
// placements fail closed without quorum and never return a partial replica
// set. Returned nodes are independent copies sorted by stable node ID.
func ResolvePlacement(deployment Deployment, snapshot ClusterSnapshot) ([]*candaceosv1.Node, error) {
	if err := deployment.Validate(); err != nil {
		return nil, err
	}
	if err := snapshot.Validate(); err != nil {
		return nil, err
	}
	if deployment.DesiredState == DesiredStateStopped {
		return []*candaceosv1.Node{}, nil
	}
	if !snapshot.Authoritative {
		return nil, ErrNotAuthoritative
	}
	if !snapshot.HasQuorum {
		return nil, ErrNoQuorum
	}

	alive := aliveNodesSorted(snapshot.Nodes)
	if snapshot.LeaderNodeID == "" {
		return nil, fmt.Errorf("%w: no leader is elected", ErrLeaderUnavailable)
	}
	var leader *candaceosv1.Node
	for _, node := range alive {
		if node.GetId() == snapshot.LeaderNodeID {
			leader = node
			break
		}
	}
	if leader == nil {
		return nil, fmt.Errorf("%w: elected node %q is missing or not alive", ErrLeaderUnavailable, snapshot.LeaderNodeID)
	}

	switch {
	case deployment.Placement.ExactNode != nil:
		nodeID := deployment.Placement.ExactNode.NodeID
		for _, node := range alive {
			if node.GetId() == nodeID {
				return []*candaceosv1.Node{node}, nil
			}
		}
		return nil, fmt.Errorf("%w: exact node %q is missing or not alive", ErrPlacementUnsatisfied, nodeID)

	case deployment.Placement.Leader != nil:
		return []*candaceosv1.Node{leader}, nil

	case deployment.Placement.MatchLabels != nil:
		policy := deployment.Placement.MatchLabels
		matches := make([]*candaceosv1.Node, 0, len(alive))
		for _, node := range alive {
			if hasExactLabels(node.GetLabels(), policy.Labels) {
				matches = append(matches, node)
			}
		}
		if len(matches) < policy.Replicas {
			return nil, fmt.Errorf("%w: match_labels needs %d replicas but only %d alive nodes match", ErrPlacementUnsatisfied, policy.Replicas, len(matches))
		}
		return matches[:policy.Replicas], nil
	default:
		// Deployment validation makes this unreachable, but retaining a guarded
		// return keeps this function fail-closed if the model changes.
		return nil, ErrInvalidPlacement
	}
}

func aliveNodesSorted(nodes []*candaceosv1.Node) []*candaceosv1.Node {
	alive := make([]*candaceosv1.Node, 0, len(nodes))
	for _, node := range nodes {
		if node.GetAlive() {
			alive = append(alive, proto.Clone(node).(*candaceosv1.Node))
		}
	}
	sort.Slice(alive, func(i, j int) bool {
		return alive[i].GetId() < alive[j].GetId()
	})
	return alive
}

func hasExactLabels(actual, required map[string]string) bool {
	for key, expected := range required {
		if actual[key] != expected {
			return false
		}
	}
	return true
}
