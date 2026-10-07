// Copyright 2026 Candace Labs

package eventually

import (
	"context"
	"errors"
	"time"
)

// IClock is the time a wait outside a test reads and waits on: the current
// instant, and a channel that delivers once a duration has elapsed. The
// clock capability's system and manual clocks both satisfy it, so a binary
// grants the host's clock and a spec moves time itself.
type IClock interface {
	Now() time.Time
	After(d time.Duration) <-chan time.Time
}

// systemClock is the host's clock, for [Wait]: a primitive's own default,
// as package time is the standard library's.
type systemClock struct{}

func (systemClock) Now() time.Time                         { return time.Now() }
func (systemClock) After(d time.Duration) <-chan time.Time { return time.After(d) }

// ErrNoBudget reports a wait given no positive budget: a wait without a
// deadline is a watch, and nothing joins it.
var ErrNoBudget = errors.New("eventually: a wait needs a positive budget")

// Outcome is what a wait outside a test observed.
type Outcome[Value any] struct {
	// Met is whether match accepted a value before the budget ran out.
	Met bool
	// Final is whether the wait ended because the value can no longer be
	// accepted, as final judged it, rather than on the deadline.
	Final bool
	// Elapsed is the clock's time from the first poll to the last.
	Elapsed time.Duration
	// Polls counts the calls to poll.
	Polls int
	// Last is the last value poll returned: the accepted one when Met.
	Last Value
}

// Until is [Await] for code that is not a test: it polls until match
// accepts a value, final says no value ever will, the budget runs out on
// clock, or ctx ends, and reports what it saw rather than failing a test.
// The deadline is read on clock, so a spec that grants a manual clock decides
// every instant; poll itself is bounded by ctx, which the caller sizes to the
// budget. A nil final never ends a wait early.
//
// It returns an error only for a budget that is not positive or for ctx
// ending first, with the outcome so far.
func Until[Value any](
	ctx context.Context,
	clock IClock,
	budget Budget,
	poll func(ctx context.Context) Value,
	match func(value Value) bool,
	final func(value Value) bool,
) (Outcome[Value], error) {
	var outcome Outcome[Value]
	if budget.Within <= 0 {
		return outcome, ErrNoBudget
	}
	started := clock.Now()
	deadline := started.Add(budget.Within)
	for {
		outcome.Last = poll(ctx)
		outcome.Polls++
		now := clock.Now()
		outcome.Elapsed = now.Sub(started)
		switch {
		case match(outcome.Last):
			outcome.Met = true
			return outcome, nil
		case final != nil && final(outcome.Last):
			outcome.Final = true
			return outcome, nil
		case !now.Before(deadline):
			return outcome, nil
		}
		select {
		case <-ctx.Done():
			return outcome, context.Cause(ctx)
		case <-clock.After(min(budget.interval(), deadline.Sub(now))):
		}
	}
}
