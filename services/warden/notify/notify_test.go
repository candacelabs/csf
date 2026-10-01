package notify

import (
	"time"

	"github.com/candacelabs/csf/services/warden"
)

var testTime = time.Date(2026, 7, 20, 5, 4, 3, 0, time.UTC)

// deadIncident builds a representative peer_dead incident for tests.
func deadIncident(id string) warden.Incident {
	peer := warden.Node{ID: warden.NodeID(id), Addr: "203.0.113.11:7717"}
	at := testTime
	return warden.Incident{
		ID:         warden.NewIncidentID(warden.IncidentPeerDead, peer.ID, at),
		Type:       warden.IncidentPeerDead,
		Peer:       peer,
		Term:       7,
		ReportedBy: "node-c",
		DetectedAt: at,
		LastSeen:   at.Add(-time.Minute),
		Message:    "peer " + id + " (203.0.113.11:7717) declared dead by leader node-c (term 7)",
	}
}

func recoveryIncident(id string) warden.Incident {
	inc := deadIncident(id)
	inc.Type = warden.IncidentPeerRecovered
	inc.ID = warden.NewIncidentID(warden.IncidentPeerRecovered, inc.Peer.ID, testTime)
	inc.Message = "peer " + id + " recovered; outage lasted 2m0s"
	return inc
}
