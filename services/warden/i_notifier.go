package warden

import "context"

// INotifier delivers a watchdog Incident to the operator. Production uses
// SMTP email; tests use a recording mock; the e2e harness uses a file
// sink. Implementations must be safe for concurrent use. Notify should
// return an error only on delivery failure; the watchdog logs failures
// and retries on the next evaluation (dedup still prevents duplicate
// notifications once one delivery succeeds).
type INotifier interface {
	Notify(ctx context.Context, inc Incident) error
}
