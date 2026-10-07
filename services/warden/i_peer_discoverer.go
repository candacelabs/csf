package warden

import "context"

// IPeerDiscoverer reports candidate cluster nodes. Discover returns a channel
// delivering roster snapshots until ctx ends (the implementation closes the
// channel when done and must send an initial snapshot promptly). Snapshots
// are advisory: the election manager's event loop consumes them, verifies
// candidates via ITransport.Identify, and only the LEADER turns stable,
// verified candidates into one-at-a-time membership changes. When the
// discovery source is unavailable, implementations should keep the channel
// open and simply not send (consumers fall back to the last known roster and
// the persisted membership — never to an empty set).
type IPeerDiscoverer interface {
	Discover(ctx context.Context) (<-chan Roster, error)
}
