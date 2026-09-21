package core

import (
	"fmt"
	"time"
)

// FormatTimeOrNever renders a possibly-zero timestamp for human-readable
// output: the zero time becomes "never", anything else is formatted as UTC
// RFC 3339. Shared by alerting/reporting paths that describe when something
// was last observed.
func FormatTimeOrNever(t time.Time) string {
	if t.IsZero() {
		return "never"
	}
	return t.UTC().Format(time.RFC3339)
}

// FormatAgo renders an elapsed duration as the compact relative age an
// operator page shows beside a timestamp: "just now" below a minute, then
// whole minutes, whole hours, and whole days. Each tier truncates rather than
// rounds, so the label never claims more elapsed time than there is.
//
// A negative duration is clamped to zero and so reads as "just now". That is
// the shape clock skew takes between a machine that stamped an event and the
// machine rendering it, and a page that answers "in 3m" is reporting on the
// two clocks rather than on the event.
//
// What an absent or zero timestamp means stays with the caller, because the
// surfaces that share this ladder do not agree on it: a fleet page that has
// never heard from a node says "never", while one describing a snapshot it
// just took says "now". This function starts once there is a duration to
// render, which is the part they do agree on.
func FormatAgo(elapsed time.Duration) string {
	if elapsed < 0 {
		elapsed = 0
	}
	switch {
	case elapsed < time.Minute:
		return "just now"
	case elapsed < time.Hour:
		return fmt.Sprintf("%dm ago", int(elapsed.Minutes()))
	case elapsed < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(elapsed.Hours()))
	default:
		return fmt.Sprintf("%dd ago", int(elapsed.Hours()/24))
	}
}
