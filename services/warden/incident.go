package warden

import (
	"fmt"
	"time"
)

// Incident is a single watchdog event that (subject to dedup/cooldown)
// results in exactly one operator notification.
type Incident struct {
	// ID uniquely identifies the incident, e.g. "peer_dead/node-a/1721433600".
	ID   string       `json:"id"`
	Type IncidentType `json:"type"`
	// Peer is the affected node.
	Peer Node `json:"peer"`
	// Term is the reporting leader's term when the incident was detected.
	Term Term `json:"term"`
	// ReportedBy is the leader that detected the incident.
	ReportedBy NodeID `json:"reported_by"`
	// DetectedAt is when the leader detected the transition.
	DetectedAt time.Time `json:"detected_at"`
	// LastSeen is the leader's last successful contact with the peer
	// before the incident (zero if never seen).
	LastSeen time.Time `json:"last_seen"`
	// Message is a human-readable summary suitable for an email body line.
	Message string `json:"message"`
}

// NewIncidentID builds the canonical incident ID.
func NewIncidentID(t IncidentType, peer NodeID, at time.Time) string {
	return fmt.Sprintf("%s/%s/%d", t, peer, at.Unix())
}
