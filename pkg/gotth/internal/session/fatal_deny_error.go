package session

import "fmt"

// FatalDenyError rejects an event and closes the connection.
type FatalDenyError struct {
	// Reason is operator-facing, as for DenyError.
	Reason string
}

// Error renders the reason and says the connection is going with it, so a log
// line distinguishes this from the survivable denial without the reader having
// to know which type produced it.
func (e *FatalDenyError) Error() string {
	return fmt.Sprintf("gotth-live: event denied, closing the connection: %s", e.Reason)
}
