// Copyright 2026 Candace Labs

// Package listsched is list scheduling (Graham, 1969): whenever a machine is
// free, start the highest-priority ready task that may run beside what is
// running. It is parametric in the priority, so a caller ranks by critical
// path, urgency or anything else, and it adds one constraint Graham did
// not have: a conflict relation, under which two tasks never run at once.
//
// [Pick] is the one decision the scheduler makes, shared by the simulator
// here and by any service that dispatches work the same way, so a service's
// dispatch order is the simulator's prediction by construction. [Simulate]
// runs a whole plan to its makespan; [LowerBound] and [GrahamBound] turn a
// measured makespan into a verdict against the optimum without computing it.
package listsched

import (
	"errors"
	"fmt"
	"slices"
	"sort"
	"time"

	"github.com/candacelabs/csf/io/inproc"
)

var (
	// ErrInvalidPlan reports a plan with no order, no priority or a capacity
	// below one.
	ErrInvalidPlan = errors.New("listsched: invalid plan")
	// ErrStuck reports a plan whose conflicts leave ready tasks that can never
	// start, which cannot happen for a conflict relation that is irreflexive.
	ErrStuck = errors.New("listsched: ready tasks can never start")
)

// Plan is what the scheduler works from: the precedence order, each task's
// expected duration, the conflict relation, the capacity and the priority.
type Plan[ID comparable, Kind comparable] struct {
	// Order is the graph holding the precedence relation; every task is one
	// of its nodes.
	Order *inproc.Graph[ID, Kind]
	// Precedes is the directed acyclic kind of Order that orders tasks.
	Precedes Kind
	// Duration is a task's expected duration; a task with none takes
	// [DefaultDuration].
	Duration func(id ID) time.Duration
	// Conflicts reports whether a and b must not run at once. It is
	// symmetric and irreflexive; nil means no conflicts.
	Conflicts func(a ID, b ID) bool
	// Capacity is how many tasks may run at once.
	Capacity int
	// Less reports whether a should start before b when both are ready.
	Less func(a ID, b ID) bool
}

// DefaultDuration is the duration of a task the plan does not time.
const DefaultDuration = time.Second

// Interval is one task's run in a schedule.
type Interval[ID comparable] struct {
	Task  ID
	Start time.Duration
	End   time.Duration
}

// Sample is the concurrency at one instant of a schedule.
type Sample struct {
	At      time.Duration
	Running int
}

// Schedule is the result of simulating a plan.
type Schedule[ID comparable] struct {
	// Intervals in start order, the dispatch order.
	Intervals []Interval[ID]
	Makespan  time.Duration
	// Concurrency over time: one sample at every start or end.
	Concurrency []Sample
	// Peak is the most tasks that ran at once.
	Peak int
}

// Order is the task identifiers in start order.
func (schedule Schedule[ID]) Order() []ID {
	order := make([]ID, 0, len(schedule.Intervals))
	for _, interval := range schedule.Intervals {
		order = append(order, interval.Task)
	}
	return order
}

// validate checks the plan's fixed parts.
func (plan Plan[ID, Kind]) validate() error {
	switch {
	case plan.Order == nil:
		return fmt.Errorf("%w: no order", ErrInvalidPlan)
	case plan.Less == nil:
		return fmt.Errorf("%w: no priority", ErrInvalidPlan)
	case plan.Capacity < 1:
		return fmt.Errorf("%w: capacity %d", ErrInvalidPlan, plan.Capacity)
	}
	return nil
}

func (plan Plan[ID, Kind]) duration(id ID) time.Duration {
	if plan.Duration == nil {
		return DefaultDuration
	}
	if duration := plan.Duration(id); duration > 0 {
		return duration
	}
	return DefaultDuration
}

func (plan Plan[ID, Kind]) conflicts(a ID, b ID) bool {
	return plan.Conflicts != nil && plan.Conflicts(a, b)
}

// Pick chooses which ready tasks start now: in priority order, each task that
// conflicts with nothing running and nothing picked before it, until the
// capacity is full. Lower-priority tasks are not held back by a blocked
// higher-priority one; that is what keeps the machines busy, and it is the
// bucketed order of Δ-stepping rather than a strict serial one.
func Pick[ID comparable](ready []ID, running []ID, capacity int, conflicts func(a ID, b ID) bool, less func(a ID, b ID) bool) []ID {
	free := capacity - len(running)
	if free <= 0 || len(ready) == 0 {
		return nil
	}
	candidates := slices.Clone(ready)
	sort.SliceStable(candidates, func(i int, j int) bool { return less(candidates[i], candidates[j]) })
	picked := make([]ID, 0, free)
	for _, candidate := range candidates {
		if len(picked) == free {
			break
		}
		if conflictsWithAny(candidate, running, conflicts) || conflictsWithAny(candidate, picked, conflicts) {
			continue
		}
		picked = append(picked, candidate)
	}
	return picked
}

func conflictsWithAny[ID comparable](candidate ID, others []ID, conflicts func(a ID, b ID) bool) bool {
	if conflicts == nil {
		return false
	}
	for _, other := range others {
		if conflicts(candidate, other) {
			return true
		}
	}
	return false
}

// Simulate runs the plan: at time zero and at every completion it picks what
// starts next, until every task has run. Durations are the plan's expected
// ones, so the result is the prediction a dispatcher using [Pick] with the
// same priority realizes when its tasks take as long as expected.
func Simulate[ID comparable, Kind comparable](plan Plan[ID, Kind]) (Schedule[ID], error) {
	if err := plan.validate(); err != nil {
		return Schedule[ID]{}, err
	}
	finished := map[ID]bool{}
	ends := map[ID]time.Duration{}
	var running []ID
	var schedule Schedule[ID]
	now := time.Duration(0)
	total := plan.Order.Len()
	for len(finished) < total {
		done := func(id ID) bool { return finished[id] }
		ready := plan.Order.Frontier(plan.Precedes, done)
		ready = slices.DeleteFunc(ready, func(id ID) bool { return slices.Contains(running, id) })
		for _, id := range Pick(ready, running, plan.Capacity, plan.conflicts, plan.Less) {
			running = append(running, id)
			ends[id] = now + plan.duration(id)
			schedule.Intervals = append(schedule.Intervals, Interval[ID]{Task: id, Start: now, End: ends[id]})
		}
		if len(running) == 0 {
			return Schedule[ID]{}, fmt.Errorf("%w: %d ready, %d finished of %d", ErrStuck, len(ready), len(finished), total)
		}
		schedule.Concurrency = append(schedule.Concurrency, Sample{At: now, Running: len(running)})
		schedule.Peak = max(schedule.Peak, len(running))
		// Time advances to the earliest completion; everything ending then
		// finishes together.
		next := ends[running[0]]
		for _, id := range running[1:] {
			next = min(next, ends[id])
		}
		now = next
		running = slices.DeleteFunc(running, func(id ID) bool {
			if ends[id] == now {
				finished[id] = true
				return true
			}
			return false
		})
	}
	schedule.Concurrency = append(schedule.Concurrency, Sample{At: now, Running: 0})
	schedule.Makespan = now
	return schedule, nil
}

// LowerBound is a bound no schedule of the plan can beat: the longer of the
// critical path (the heaviest chain of dependent tasks, which cannot overlap
// itself) and the total work spread over every machine.
func LowerBound[ID comparable, Kind comparable](plan Plan[ID, Kind]) (time.Duration, error) {
	if err := plan.validate(); err != nil {
		return 0, err
	}
	seconds := func(id ID) float64 { return plan.duration(id).Seconds() }
	longest := 0.0
	for _, rank := range plan.Order.UpwardRank(plan.Precedes, seconds, nil) {
		longest = max(longest, rank)
	}
	total := time.Duration(0)
	for _, id := range plan.Order.Nodes() {
		total += plan.duration(id)
	}
	spread := time.Duration(float64(total) / float64(plan.Capacity))
	critical := time.Duration(longest * float64(time.Second))
	return max(critical, spread), nil
}

// GrahamBound is the makespan a list schedule on capacity machines never
// exceeds: (2 - 1/m) times the optimum (Graham, 1969). Given a lower bound
// on the optimum instead of the optimum itself the result is still a valid
// test: a makespan within the bound of the lower bound is within it of the
// optimum.
func GrahamBound(capacity int, optimum time.Duration) time.Duration {
	if capacity < 1 {
		return optimum
	}
	return time.Duration(float64(optimum) * (2 - 1/float64(capacity)))
}
