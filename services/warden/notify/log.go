package notify

import (
	"context"

	"github.com/candacelabs/csf/pkg/core"
	"github.com/candacelabs/csf/services/warden"
)

// LogNotifier delivers incidents by writing a structured log line via
// core.Logger. It is the default notifier when SMTP is not configured. It
// holds no mutable state and core.Logger is safe for concurrent use, so no
// synchronization is needed.
type LogNotifier struct{}

var _ warden.INotifier = (*LogNotifier)(nil)

// NewLogNotifier returns a LogNotifier.
func NewLogNotifier() *LogNotifier { return &LogNotifier{} }

// Notify logs the incident and always returns nil.
func (n *LogNotifier) Notify(ctx context.Context, inc warden.Incident) error {
	core.Logger.Warn().
		Str("incident_id", inc.ID).
		Str("type", string(inc.Type)).
		Str("peer_id", string(inc.Peer.ID)).
		Str("peer_addr", inc.Peer.Addr).
		Uint64("term", uint64(inc.Term)).
		Str("reported_by", string(inc.ReportedBy)).
		Time("detected_at", inc.DetectedAt).
		Time("last_seen", inc.LastSeen).
		Str("message", inc.Message).
		Msg("warden incident")
	return nil
}
