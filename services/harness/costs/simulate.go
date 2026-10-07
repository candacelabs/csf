// Copyright 2026 Candace Labs

package costs

import (
	"math"
	"time"
)

// The simulator replays the recorded runs under a policy: each decision is
// a pure function from one recorded unit (an idle gap, a bind) to what the
// policy would have paid for it. Nothing is sampled, so a replay of the same
// record is the same number.

// Gap is one stretch a real session sat idle: between two of its turns, or
// after its last turn until the run's record ends (Returned false).
type Gap struct {
	// Day is the UTC day the gap ended.
	Day      string
	Agent    string
	Model    string
	Length   time.Duration
	Returned bool
	// Prefix is the conversation's size at the gap's start: what the next
	// turn reads, or writes again once the cache has expired.
	Prefix int64
}

// dayFormat names a UTC day.
const dayFormat = time.DateOnly

// Gaps is every idle stretch of every real session in the record.
func Gaps(runs []Run) []Gap {
	gaps := []Gap{}
	for _, run := range runs {
		var end time.Time
		for _, span := range run.Open {
			if span.To.After(end) {
				end = span.To
			}
		}
		last := map[string]Turn{}
		order := []string{}
		for _, turn := range run.Turns {
			if previous, seen := last[turn.Real]; seen {
				gaps = append(gaps, Gap{Day: turn.Start.UTC().Format(dayFormat), Agent: run.Agent, Model: previous.Model,
					Length: max(0, turn.Start.Sub(previous.End)), Returned: true, Prefix: previous.Last.Input() + previous.Last.Output})
			} else {
				order = append(order, turn.Real)
			}
			last[turn.Real] = turn
		}
		for _, real := range order {
			previous := last[real]
			if end.After(previous.End) {
				gaps = append(gaps, Gap{Day: end.UTC().Format(dayFormat), Agent: run.Agent, Model: previous.Model,
					Length: end.Sub(previous.End), Prefix: previous.Last.Input() + previous.Last.Output})
			}
		}
	}
	return gaps
}

// KeepAlive prices keeping a conversation's prompt cache warm through a gap
// with requests that read the whole prefix just before each expiry, against
// letting it expire and writing the prefix again on return. It is ski
// rental (Karlin, Manasse, Rudolph and Sleator): each request is a day's
// rent, the rebuild is the purchase.
type KeepAlive struct {
	Lifetime time.Duration
	Prices   map[string]Price
}

// Requests is how many keep-alive requests bridge the gap: one just before
// each expiry that falls inside it.
func (keepAlive KeepAlive) Requests(gap Gap) int {
	if gap.Length <= keepAlive.Lifetime {
		return 0
	}
	return int(math.Ceil(float64(gap.Length)/float64(keepAlive.Lifetime))) - 1
}

// Request is one keep-alive request's price: the prefix read and one output
// token.
func (keepAlive KeepAlive) Request(gap Gap) float64 {
	price := keepAlive.Prices[gap.Model]
	return float64(gap.Prefix)*price.Read + price.Output
}

// Rebuild is the premium a turn pays after expiry: the prefix written
// instead of read.
func (keepAlive KeepAlive) Rebuild(gap Gap) float64 {
	price := keepAlive.Prices[gap.Model]
	return float64(gap.Prefix) * (price.Write - price.Read)
}

// Cost is what a gap costs when at most limit keep-alive requests are sent:
// the requests sent, and the rebuild when they ran out before the return.
func (keepAlive KeepAlive) Cost(gap Gap, limit int) float64 {
	needed := keepAlive.Requests(gap)
	if !gap.Returned {
		return float64(min(needed, limit)) * keepAlive.Request(gap)
	}
	if needed <= limit {
		return float64(needed) * keepAlive.Request(gap)
	}
	return float64(limit)*keepAlive.Request(gap) + keepAlive.Rebuild(gap)
}

// BreakEven is the deterministic ski-rental rule's limit for the gap: keep
// alive until the requests sent would equal the rebuild. It is 2-competitive.
func (keepAlive KeepAlive) BreakEven(gap Gap) int {
	request := keepAlive.Request(gap)
	if request <= 0 {
		return 0
	}
	return int(math.Floor(keepAlive.Rebuild(gap) / request))
}

// Offline is what an oracle that knew the gap's length would pay: the
// cheaper of bridging it and rebuilding, and nothing for a gap with no
// return. No policy can do better.
func (keepAlive KeepAlive) Offline(gap Gap) float64 {
	if !gap.Returned {
		return 0
	}
	return min(float64(keepAlive.Requests(gap))*keepAlive.Request(gap), keepAlive.Rebuild(gap))
}

// IdleClose prices closing a turn executor after it sat idle for a
// threshold: the memory it holds until then, and on a return after the
// threshold the reopen, which costs the measured cache premium while the
// cache is warm (past the lifetime the cache is lost either way) and the
// measured added time to first token. Memory and latency have no price in
// the record, so they stay in their own units.
type IdleClose struct {
	Lifetime      time.Duration
	ResidentBytes float64
	ReopenPremium float64
	ReopenLatency time.Duration
}

// IdleCost is one gap's cost under one threshold, in the three units.
type IdleCost struct {
	USD             float64
	ResidentGBHours float64
	Latency         time.Duration
}

// Cost is the gap's cost when the executor closes after threshold; a
// negative threshold never closes.
func (idle IdleClose) Cost(gap Gap, threshold time.Duration) IdleCost {
	held := gap.Length
	if threshold >= 0 {
		held = min(gap.Length, threshold)
	}
	cost := IdleCost{ResidentGBHours: idle.ResidentBytes / 1e9 * held.Hours()}
	if threshold >= 0 && gap.Returned && gap.Length > threshold {
		cost.Latency = idle.ReopenLatency
		if gap.Length <= idle.Lifetime {
			cost.USD = idle.ReopenPremium
		}
	}
	return cost
}

// Bind is one virtual session's first turn on a new real session.
type Bind struct {
	Day   string
	Model string
	// First is its first model call: what the endpoint already held of the
	// prefix (read) and what it wrote.
	First Tokens
}

// Binds is every bind in the record.
func Binds(runs []Run) []Bind {
	binds := []Bind{}
	for _, run := range runs {
		seen := map[string]bool{}
		for _, turn := range run.Turns {
			if seen[turn.Real] || turn.Model == "" {
				continue
			}
			seen[turn.Real] = true
			binds = append(binds, Bind{Day: turn.Start.UTC().Format(dayFormat), Model: turn.Model, First: turn.First})
		}
	}
	return binds
}

// Placement prices prefix-aware grouping: binds that share a prefix are
// placed so the shared part is still cached when the next one starts. The
// shareable prefix of a model is the longest prefix the endpoint ever found
// cached on a bind's first call; under grouping every bind reads that much
// instead of writing it.
type Placement struct {
	Shared map[string]int64
	Prices map[string]Price
}

// NewPlacement measures the shareable prefix of every model.
func NewPlacement(binds []Bind, prices map[string]Price) Placement {
	shared := map[string]int64{}
	for _, bind := range binds {
		shared[bind.Model] = max(shared[bind.Model], bind.First.Read)
	}
	return Placement{Shared: shared, Prices: prices}
}

// Shift is the tokens grouping moves from written to read on the bind.
func (placement Placement) Shift(bind Bind) int64 {
	return min(bind.First.Write, max(0, placement.Shared[bind.Model]-bind.First.Read))
}

// Saving is what grouping saves on the bind.
func (placement Placement) Saving(bind Bind) float64 {
	price := placement.Prices[bind.Model]
	return float64(placement.Shift(bind)) * (price.Write - price.Read)
}
