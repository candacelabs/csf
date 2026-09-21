package metrics

// Exported names of the Prometheus surface warden exposes at /metrics. These
// are the single source of truth for every metric family name and label name:
// metrics.go builds its collectors from them, and the contract specs reference
// them for their value lookups. The frozen HELP/TYPE/family-list goldens in
// metrics_contract_test.go stay literal on purpose — they are the independent
// side that pins these constants' values, so a rename here surfaces there.
//
// Operators' dashboards and alerts are wired to these exact names; changing a
// value is a breaking change to the metrics contract.
const (
	MetricIsLeader          = "warden_is_leader"
	MetricTerm              = "warden_term"
	MetricElectionsTotal    = "warden_elections_total"
	MetricViewAuthoritative = "warden_view_authoritative"
	MetricMembershipVersion = "warden_membership_version"
	MetricVoters            = "warden_voters"
	MetricObservers         = "warden_observers"
	MetricDiscovered        = "warden_discovered"
	MetricPeerUp            = "warden_peer_up"
	MetricPeerStatus        = "warden_peer_status"
	MetricPeerLatencyMS     = "warden_peer_latency_ms"
	MetricPeerMember        = "warden_peer_member"
)

// Prometheus label names attached to the metric families above.
const (
	LabelNode   = "node"   // node-scoped scalar metrics (is_leader, term, elections)
	LabelPeer   = "peer"   // per-peer metrics (peer_up, peer_status, peer_latency_ms, peer_member)
	LabelStatus = "status" // one-hot liveness dimension of warden_peer_status
	LabelMember = "member" // one-hot membership-kind dimension of warden_peer_member
)
