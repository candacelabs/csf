// Copyright 2026 Candace Labs

// Package clock is the clock capability: the kernel I/O tier's time source.
// Reading the clock and waiting for an instant are system calls whose far
// side is the kernel, so a service that schedules by time does both through
// a capability granted by the binary that owns the process, never through
// package time directly. The binary grants [SystemClock]; a spec grants a
// [ManualClock] and moves time itself, so a wait for the next occurrence is
// proven without a sleep.
package clock

import "time"

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=clock.go -destination=mocks/mock_clock.go -package=mocks

// IClock is the time source a service is granted: the current instant, and
// a wait that delivers once a duration has elapsed.
type IClock interface {
	// Now is the current instant.
	Now() time.Time
	// After delivers the instant at which d elapsed; a zero or negative d
	// delivers at once. The channel is buffered to one and never closed, as
	// time.After's is.
	After(d time.Duration) <-chan time.Time
}

// SystemClock is the host's clock: package time behind the capability.
type SystemClock struct{}

// NewSystemClock grants the host's clock as its own concrete type; a caller
// that wants the abstraction assigns it into an [IClock] (CS-8).
func NewSystemClock() SystemClock { return SystemClock{} }

// Now is time.Now.
func (SystemClock) Now() time.Time { return time.Now() }

// After is time.After.
func (SystemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }
