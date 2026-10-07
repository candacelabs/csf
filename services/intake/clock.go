// Copyright 2026 Candace Labs

package intake

import "time"

// Clock is the time source the poller waits on. It is data, not behaviour:
// [SystemClock] fills it from package time, and a spec fills it with a fixed
// time and an After that records what it was asked to wait.
type Clock struct {
	Now   func() time.Time
	After func(duration time.Duration) <-chan time.Time
}

// SystemClock is the wall clock.
func SystemClock() Clock { return Clock{Now: time.Now, After: time.After} }
