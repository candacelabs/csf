package warden

import "time"

// IClock abstracts time so the election and watchdog state machines can be
// tested deterministically with a simulated clock (services/warden/testclock).
// Production code uses NewRealClock.
type IClock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
	NewTimer(d time.Duration) Timer
	NewTicker(d time.Duration) Ticker
}

// Timer is one armed one-shot, handed to the caller as data: the channel it
// will fire on, and the two operations that cancel or re-arm it. Nothing here
// is polymorphic — a Timer holds no state of its own and dispatches to
// nothing — so it is a struct rather than an interface, and every clock fills
// it from whatever machinery it already has (the real one from *time.Timer,
// the simulated one from its own waiter table).
//
// The zero Timer is not usable; obtain one from IClock.NewTimer.
type Timer struct {
	// C receives the time at which the timer fired, once. It is buffered to
	// one and never closed, exactly as time.Timer.C is.
	C <-chan time.Time
	// Stop prevents the timer from firing and reports whether it stopped a
	// pending fire: the same semantics as time.Timer.Stop, including that a
	// false return may mean the value is already buffered in C, which is why
	// the drain-then-Reset dance in election.Manager is spelled out.
	Stop func() bool
	// Reset re-arms the timer with duration d and reports whether it was
	// still armed: the same semantics as time.Timer.Reset, including that it
	// is only safe to call on a stopped or drained timer.
	Reset func(d time.Duration) bool
}

// Ticker is one armed repeating source, handed to the caller as data for the
// same reason Timer is: a channel plus the one operation that ends it.
//
// The zero Ticker is not usable; obtain one from IClock.NewTicker.
type Ticker struct {
	// C receives the time of each tick. It is buffered to one and delivery is
	// non-blocking, so a tick a receiver has not drained is dropped rather
	// than queued — exactly what time.Ticker.C does.
	C <-chan time.Time
	// Stop halts delivery. It does not close C, matching time.Ticker.Stop, so
	// a receiver parked on C after Stop simply never wakes.
	Stop func()
}

// RealClock is the production IClock, backed by the time package. The
// simulated one is services/warden/testclock.
type RealClock struct{}

// NewRealClock returns the production clock as its own concrete type. A caller
// that wants the abstraction declares `var clock warden.IClock` and assigns
// this into it; a caller that does not, keeps the type (house rule CS-8).
func NewRealClock() RealClock { return RealClock{} }

func (RealClock) Now() time.Time                         { return time.Now() }
func (RealClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// NewTimer fills a Timer from time.NewTimer. The two function fields are the
// standard library's own method values, so the semantics documented on Timer
// are time.Timer's rather than a re-implementation of them.
func (RealClock) NewTimer(d time.Duration) Timer {
	timer := time.NewTimer(d)
	return Timer{C: timer.C, Stop: timer.Stop, Reset: timer.Reset}
}

// NewTicker fills a Ticker from time.NewTicker, and panics on a non-positive
// d because time.NewTicker does.
func (RealClock) NewTicker(d time.Duration) Ticker {
	ticker := time.NewTicker(d)
	return Ticker{C: ticker.C, Stop: ticker.Stop}
}
