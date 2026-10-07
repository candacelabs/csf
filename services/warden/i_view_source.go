package warden

// IViewSource provides cluster view snapshots and change notifications.
// Implemented by the election manager; consumed by the watchdog,
// dashboard, and metrics packages.
type IViewSource interface {
	// View returns the current cluster view snapshot. Safe for
	// concurrent use; the returned value is a copy the caller may keep.
	View() ClusterView
	// Subscribe returns a channel that receives view snapshots after
	// state changes (role/term/leader changes, peer status transitions).
	// Delivery is best-effort: when the buffered channel is full,
	// intermediate updates are dropped, so consumers should treat a
	// receive as a change signal and may re-read View() for the latest
	// state. cancel unsubscribes and closes the channel.
	Subscribe(buf int) (ch <-chan ClusterView, cancel func())
}
