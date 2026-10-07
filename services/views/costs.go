// Copyright 2026 Candace Labs

package views

import (
	"io/fs"
	"strings"
	"time"

	"github.com/candacelabs/csf/services/harness/costs"
)

const (
	// CostRefresh is how often the hypervisor's cost events are derived
	// again: the derivation reads every run whole, so it runs on a slower
	// cadence than the fold, and the cost model changes over hours.
	CostRefresh = 15 * time.Minute
	// operationPrefix is the proto enum prefix an operation's label drops.
	operationPrefix = "HYPERVISOR_OPERATION_"
)

// CostEvent is one hypervisor cost event (#353), as the cost families count
// and price it.
type CostEvent struct {
	Operation string
	Agent     string
	Model     string
	At        time.Time
	USD       float64
}

// CostKey is the label set of the cost families.
type CostKey struct {
	Operation string
	Agent     string
	Model     string
}

// CostTally is what a set of cost events adds up to.
type CostTally struct {
	Events int64
	USD    float64
}

// DeriveCosts derives the hypervisor's cost events from the runs in state,
// as #353's cost report does: the cache lifetime and the per-model prices
// are fitted from the same record.
func DeriveCosts(state fs.FS) ([]CostEvent, error) {
	runs, err := costs.ReadRuns(state)
	if err != nil {
		return nil, err
	}
	lifetime, _ := costs.CacheLifetime(runs)
	derived := costs.Derive(runs, lifetime, costs.Prices(costs.FitPrices(runs)), 0)
	events := make([]CostEvent, 0, len(derived))
	for _, event := range derived {
		events = append(events, CostEvent{
			Operation: strings.ToLower(strings.TrimPrefix(event.GetOperation().String(), operationPrefix)),
			Agent:     event.GetAgent(), Model: event.GetModel(), At: event.GetAt().AsTime().UTC(), USD: event.GetUsd(),
		})
	}
	return events, nil
}

// CostsThrough adds up the cost events at or before at, by label set.
func CostsThrough(events []CostEvent, at time.Time) map[CostKey]CostTally {
	tallies := map[CostKey]CostTally{}
	for _, event := range events {
		if event.At.After(at) {
			continue
		}
		key := CostKey{Operation: event.Operation, Agent: event.Agent, Model: event.Model}
		tally := tallies[key]
		tally.Events++
		tally.USD += event.USD
		tallies[key] = tally
	}
	return tallies
}
