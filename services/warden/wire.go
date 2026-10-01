package warden

// Retired wire protocol, kept for legacy compatibility only.
//
// The node-to-node cluster RPCs (Vote/Heartbeat/Identify, plus the
// server-streaming WatchCluster) no longer travel as HTTP/JSON: they were
// migrated to gRPC over the candacenet.warden.v1.WardenService, defined in
// services/warden/proto/warden/v1/warden.proto — that schema, not this file, is now the
// source of truth for the node-to-node wire. The types below (VoteRequest,
// HeartbeatRequest, etc.) still exist because every other package is written
// against them (services/warden/wireconv converts to/from the proto messages at the
// gRPC/persistence boundary) and because their encoding/json form is still
// the legacy on-disk PersistentState format the store's read path must
// tolerate for a lossless upgrade (see services/warden/store). Their exact JSON is
// frozen by wire_contract_test.go as that legacy/compat surface, not as a
// live wire contract.

// PathVote/PathHeartbeat/PathIdentify are retired: no HTTP handler registers
// them (those RPCs are gRPC methods now). They and the JSON message types
// above are kept only because wire_contract_test.go freezes them as the
// legacy-compat encoding; PathAPIStatus, PathMetrics, PathDashboard, and
// PathClusterPartial remain live — they still name the current HTTP surface.
const (
	PathVote      = "/warden/v1/vote"
	PathHeartbeat = "/warden/v1/heartbeat"
	PathIdentify  = "/warden/v1/identify"
	PathAPIStatus = "/api/status"
	PathMetrics   = "/metrics"
	PathDashboard = "/"
	// PathClusterPartial is the HTMX partial the dashboard polls to
	// refresh the cluster table without a full page reload.
	PathClusterPartial = "/partials/cluster"
)

// VoteRequest asks a peer for its vote in CandidateID's election for Term.
type VoteRequest struct {
	Term        Term   `json:"term"`
	CandidateID NodeID `json:"candidate_id"`
}

// VoteResponse is the reply to a VoteRequest. Term is the voter's current
// term after processing the request (a candidate seeing a higher term must
// step down to follower and adopt it).
type VoteResponse struct {
	Term    Term   `json:"term"`
	Granted bool   `json:"granted"`
	VoterID NodeID `json:"voter_id"`
}

// HeartbeatRequest is sent by the leader to every peer at the configured
// heartbeat interval. It asserts leadership for Term and carries the
// leader's authoritative ClusterView for follower dashboards.
type HeartbeatRequest struct {
	Term     Term   `json:"term"`
	LeaderID NodeID `json:"leader_id"`
	// View is the leader's current authoritative cluster view. May be nil
	// (followers then keep their previous cached view).
	View *ClusterView `json:"view,omitempty"`
	// Membership is the leader's effective voting configuration. Receivers
	// with a lower local version persist-then-adopt it (only from the
	// leader they currently accept). May be nil (no change conveyed).
	Membership *Membership `json:"membership,omitempty"`
}

// IdentifyResponse answers the GET PathIdentify handshake. Discovery treats a
// node as a same-cluster warden observer candidate only when ClusterID
// matches the local configuration.
type IdentifyResponse struct {
	ClusterID string `json:"cluster_id"`
	NodeID    NodeID `json:"node_id"`
	Version   string `json:"version"`
}

// HeartbeatResponse acknowledges a heartbeat. OK is false when the receiver
// rejects the sender as leader (stale term; Term then tells the stale leader
// the newer term so it can step down), OR when the heartbeat carried a
// membership change (discovery mode) that this node failed to durably
// persist — the leader's one-at-a-time settle accounting (election/membership.go)
// must never count such a response as an ack, since doing so would let a
// change be declared committed before a real quorum has stored it.
//
// OK is NOT a liveness signal: any response that arrives at all (regardless
// of OK) proves the sender is reachable, and the leader's peer-liveness
// bookkeeping (lastContact/latencyMS, election/heartbeat.go) updates on
// receipt independent of OK. Only the absence of a response (an RPC error)
// is evidence of non-liveness. A reachable follower that merely fails to
// persist a membership change must never be misclassified as dead.
type HeartbeatResponse struct {
	Term   Term   `json:"term"`
	OK     bool   `json:"ok"`
	NodeID NodeID `json:"node_id"`
}
