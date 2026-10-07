package warden

// IncidentType classifies watchdog incidents.
type IncidentType string

const (
	// IncidentPeerDead is raised by the leader when a peer transitions
	// to StatusDead, or when a newly elected leader first observes an
	// already-dead peer.
	IncidentPeerDead IncidentType = "peer_dead"
	// IncidentPeerRecovered is raised by the leader when a peer that had
	// a peer_dead incident becomes StatusAlive again.
	IncidentPeerRecovered IncidentType = "peer_recovered"
)
