// Package testclock provides a deterministic, manually-advanced
// implementation of warden.IClock for tests. Time only moves when Advance is
// called; timers and tickers fire from Advance in chronological order. This
// lets the election and watchdog state machines be exercised without real
// sleeps, so tests are fast and non-flaky.
//
// The zero value is not usable; construct with New. A single *Clock can be
// shared by an entire simulated cluster so every node observes the same
// global time.
//
// This is a supported test double, not an internal fixture: anything built on
// warden.IClock — including code outside this repository — can drive it the same
// way warden's own suites do. Callers may rely on Advance being the only thing
// that moves time, and on the waiters due within one Advance firing in
// deadline order, ties broken by creation order, so a test observes one fixed
// interleaving rather than a scheduler-dependent one. Delivery matches the real
// time package rather than improving on it: each channel is buffered to one and
// the send is non-blocking, so a tick an unread receiver has not drained is
// dropped exactly as time.Ticker would drop it. BlockUntilTimers is how a test
// waits for the code under test to arm its timers before advancing. It never
// returns wall-clock time and has no place in a production wiring.
package testclock

import (
	"runtime"
	"sync"
	"time"

	"github.com/candacelabs/csf/services/warden"
)

// Clock is a deterministic fake clock implementing warden.IClock. All methods
// are safe for concurrent use.
type Clock struct {
	mu       sync.Mutex
	now      time.Time
	nextID   int
	waiters  map[int]*waiter
	blockers []*blocker
}

// waiter is a single armed timer or ticker.
type waiter struct {
	id       int
	deadline time.Time
	period   time.Duration // 0 for one-shot timers/After; >0 for tickers
	ch       chan time.Time
}

// blocker records a goroutine parked in BlockUntilTimers waiting for the
// number of armed waiters to reach n.
type blocker struct {
	n  int
	ch chan struct{}
}

// New returns a Clock whose current time is start.
func New(start time.Time) *Clock {
	return &Clock{
		now:     start,
		waiters: make(map[int]*waiter),
	}
}

// Now returns the current simulated time.
func (c *Clock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// After implements warden.IClock. It returns a channel that receives the time
// once d has elapsed (via Advance).
func (c *Clock) After(d time.Duration) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.addWaiterLocked(d, 0).ch
}

// NewTimer implements warden.IClock. The returned warden.Timer is filled from
// this clock's own machinery: the waiter's channel, and closures over the
// waiter id that reach back into stop and reset.
func (c *Clock) NewTimer(d time.Duration) warden.Timer {
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.addWaiterLocked(d, 0)
	id, ch := w.id, w.ch
	return warden.Timer{
		C:     ch,
		Stop:  func() bool { return c.stop(id) },
		Reset: func(d time.Duration) bool { return c.reset(id, ch, d) },
	}
}

// NewTicker implements warden.IClock. It panics if d <= 0, mirroring
// time.NewTicker.
func (c *Clock) NewTicker(d time.Duration) warden.Ticker {
	if d <= 0 {
		panic("testclock: non-positive interval for NewTicker")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	w := c.addWaiterLocked(d, d)
	id, ch := w.id, w.ch
	return warden.Ticker{
		C:    ch,
		Stop: func() { c.stop(id) },
	}
}

// addWaiterLocked registers a new waiter. Caller must hold c.mu.
func (c *Clock) addWaiterLocked(d time.Duration, period time.Duration) *waiter {
	c.nextID++
	w := &waiter{
		id:       c.nextID,
		deadline: c.now.Add(d),
		period:   period,
		ch:       make(chan time.Time, 1),
	}
	c.waiters[w.id] = w
	c.notifyBlockersLocked()
	return w
}

// Advance moves simulated time forward by d, firing every timer and ticker
// whose deadline falls within the new interval, in chronological order.
// Tickers reschedule and may fire multiple times. After each fire the lock is
// released and the scheduler is yielded so a woken goroutine gets a chance to
// run (and arm follow-on timers that Advance will then also fire if due),
// keeping multi-goroutine tests deterministic when combined with an explicit
// settle barrier.
func (c *Clock) Advance(d time.Duration) {
	c.mu.Lock()
	target := c.now.Add(d)
	for {
		next := c.earliestDueLocked(target)
		if next == nil {
			c.now = target
			break
		}
		c.now = next.deadline
		fireAt := next.deadline
		if next.period > 0 {
			next.deadline = next.deadline.Add(next.period)
		} else {
			delete(c.waiters, next.id)
		}
		// Non-blocking send matches real time.Timer/Ticker semantics: the
		// buffered (size 1) channel drops the tick if the receiver has not
		// drained the previous one.
		select {
		case next.ch <- fireAt:
		default:
		}
		c.mu.Unlock()
		runtime.Gosched()
		c.mu.Lock()
	}
	c.notifyBlockersLocked()
	c.mu.Unlock()
}

// earliestDueLocked returns the active waiter with the smallest deadline <=
// target, breaking ties by id for determinism. Caller must hold c.mu.
func (c *Clock) earliestDueLocked(target time.Time) *waiter {
	var next *waiter
	for _, w := range c.waiters {
		if w.deadline.After(target) {
			continue
		}
		if next == nil ||
			w.deadline.Before(next.deadline) ||
			(w.deadline.Equal(next.deadline) && w.id < next.id) {
			next = w
		}
	}
	return next
}

// BlockUntilTimers blocks until at least n timers/tickers are armed. It is the
// recommended synchronization point for tests that arm timers on background
// goroutines: wait for the expected number to be registered before advancing.
func (c *Clock) BlockUntilTimers(n int) {
	c.mu.Lock()
	if len(c.waiters) >= n {
		c.mu.Unlock()
		return
	}
	b := &blocker{n: n, ch: make(chan struct{})}
	c.blockers = append(c.blockers, b)
	c.mu.Unlock()
	<-b.ch
}

// ArmedTimers returns the number of currently armed timers/tickers.
func (c *Clock) ArmedTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.waiters)
}

// notifyBlockersLocked wakes any blockers whose threshold is now met. Caller
// must hold c.mu.
func (c *Clock) notifyBlockersLocked() {
	if len(c.blockers) == 0 {
		return
	}
	n := len(c.waiters)
	remaining := c.blockers[:0]
	for _, b := range c.blockers {
		if n >= b.n {
			close(b.ch)
		} else {
			remaining = append(remaining, b)
		}
	}
	c.blockers = remaining
}

// stop removes a waiter, reporting whether it was still armed (matching
// time.Timer.Stop semantics: true if it prevented a fire).
func (c *Clock) stop(id int) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, ok := c.waiters[id]
	if ok {
		delete(c.waiters, id)
	}
	return ok
}

// reset re-arms a one-shot waiter with duration d, reporting whether it was
// armed before (matching time.Timer.Reset semantics).
func (c *Clock) reset(id int, ch chan time.Time, d time.Duration) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	w, active := c.waiters[id]
	if active {
		w.deadline = c.now.Add(d)
	} else {
		c.waiters[id] = &waiter{id: id, deadline: c.now.Add(d), period: 0, ch: ch}
	}
	c.notifyBlockersLocked()
	return active
}
