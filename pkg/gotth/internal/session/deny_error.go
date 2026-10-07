package session

import "fmt"

// DenyError rejects one event without closing the connection.
type DenyError struct {
	// Reason is operator-facing. The client is told a generic denial, because
	// the reason an event was refused describes the rule that refused it.
	Reason string
}

// Error renders that operator-facing reason, for the log.
func (e *DenyError) Error() string {
	return fmt.Sprintf("gotth-live: event denied: %s", e.Reason)
}
