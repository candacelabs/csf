// Copyright 2026 Candace Labs

package clock

import (
	"sort"
	"sync"
	"time"
)

// ManualClock is a clock that moves only when its owner moves it: the
// in-process substitute a spec grants instead of [SystemClock]. Every After
// arms a wait at the current instant plus its duration; [ManualClock.Advance]
// fires the waits the new instant has reached, earliest first, delivering
// as time.After does, into a one-slot buffer without blocking.
//
// The zero value is not usable; construct it with [NewManualClock]. Its
// methods are safe to call from the goroutine under test and the spec at
// once.
type ManualClock struct {
	// mu guards now and the armed waits: one map of single-step reads and
	// writes with nothing waited for under it, a leaf critical section
	// (CS-5). Deliveries happen after it is released.
	mu     sync.Mutex
	now    time.Time
	nextID uint64
	waits  map[uint64]wait
}

type wait struct {
	id       uint64
	deadline time.Time
	channel  chan time.Time
}

// NewManualClock returns a clock reading start.
func NewManualClock(start time.Time) *ManualClock {
	return &ManualClock{now: start, waits: map[uint64]wait{}}
}

// Now is the instant the clock was last moved to.
func (clock *ManualClock) Now() time.Time {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return clock.now
}

// After arms a wait that fires once Advance has moved the clock to its
// deadline. A deadline not after the current instant fires at once, as
// time.After(0) does.
func (clock *ManualClock) After(d time.Duration) <-chan time.Time {
	channel := make(chan time.Time, 1)
	clock.mu.Lock()
	defer clock.mu.Unlock()
	deadline := clock.now.Add(d)
	if !deadline.After(clock.now) {
		channel <- clock.now
		return channel
	}
	clock.nextID++
	clock.waits[clock.nextID] = wait{id: clock.nextID, deadline: deadline, channel: channel}
	return channel
}

// Advance moves the clock forward by d and fires every armed wait whose
// deadline the new instant has reached, earliest first, each with its own
// deadline. A wait nobody has drained since it last fired is dropped, as
// time.After's would be.
func (clock *ManualClock) Advance(d time.Duration) {
	clock.mu.Lock()
	target := clock.now.Add(d)
	due := make([]wait, 0)
	for id, armed := range clock.waits {
		if !armed.deadline.After(target) {
			due = append(due, armed)
			delete(clock.waits, id)
		}
	}
	clock.now = target
	clock.mu.Unlock()
	sort.Slice(due, func(left, right int) bool {
		if due[left].deadline.Equal(due[right].deadline) {
			return due[left].id < due[right].id
		}
		return due[left].deadline.Before(due[right].deadline)
	})
	for _, fired := range due {
		select {
		case fired.channel <- fired.deadline:
		default:
		}
	}
}

// Waiting reports how many waits are armed and not yet fired, so a spec can
// let the code under test arm its wait before advancing past it.
func (clock *ManualClock) Waiting() int {
	clock.mu.Lock()
	defer clock.mu.Unlock()
	return len(clock.waits)
}
