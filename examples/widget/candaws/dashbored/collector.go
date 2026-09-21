package dashbored

import (
	"context"
	"math/rand/v2"
	"time"
)

// A collector at runtime is a goroutine owning one reservoir. The reservoir
// never leaves the goroutine, and the only thing a collector ever says is a
// sample — plus, exactly once, that it has gone.

// sample is one timestamped observation from one collector.
//
// It is sent exactly once and read by exactly one aggregator, and it is never
// mutated after being sent, which is what a value on a channel gets for free.
type sample struct {
	// Collector is which goroutine observed it.
	Collector int

	// Series is what it is an observation of.
	Series string

	// Value is the observation.
	Value float64
}

// breach is one observation past the threshold, on its way to the alerter.
type breach struct {
	// Alert is the alert's name, which is the text the card renders.
	Alert string

	// Value is what breached.
	Value float64
}

// reservoirAdd folds one observation into a reservoir of fixed size.
//
// It is Algorithm R and it is a pure function of the reservoir, the observation,
// how many have been seen and one draw — which is what lets the sampling be
// specified without starting a goroutine or reading a clock. Until the reservoir
// is full every observation is kept; after that each one replaces a uniformly
// chosen entry with probability size/seen, so every observation the collector
// ever made has the same chance of being in it.
func reservoirAdd(
	reservoir []float64, value float64, seen int, draw func(bound int) int,
) []float64 {
	if len(reservoir) < cap(reservoir) {
		return append(reservoir, value)
	}
	if cap(reservoir) == 0 || seen <= 0 {
		return reservoir
	}
	chosen := draw(seen)
	if chosen < len(reservoir) {
		reservoir[chosen] = value
	}
	return reservoir
}

// collector is one probe: one goroutine, one reservoir, one series.
type collector struct {
	// id is this collector's index, and what its departure notice carries.
	id int

	// series is what this collector observes.
	series string

	// interval is how often it scrapes.
	interval time.Duration

	// size is how many observations its reservoir holds.
	size int

	// ingest is the one channel every collector sends on. It is shared, and it
	// is the only thing in this engine that is.
	ingest chan<- sample

	// departures is where a collector says, exactly once, that it has gone.
	//
	// It is a message rather than a shared counter for the reason the whole
	// package is built on: "have all the producers finished" is a question with
	// one right answer and no safe place to keep it, so it is not kept.
	departures chan<- int

	// retire is closed when the fleet asks its collectors to leave.
	retire <-chan struct{}

	// jitter is this collector's own random stream: the observation it makes
	// and the reservoir slot it replaces both come out of it, so two collectors
	// in one process never touch one source.
	jitter *rand.Rand
}

// run is the collector's whole life: scrape, sample, emit, and on the way out
// say so exactly once.
func (probe *collector) run(ctx context.Context) {
	reservoir := make([]float64, 0, probe.size)
	seen := 0

	scrapes := time.NewTicker(probe.interval)
	defer scrapes.Stop()

	for {
		select {
		case <-ctx.Done():
			// A cancelled context is the process ending rather than a
			// collector leaving, and nothing is waiting to hear about it.
			return

		case <-probe.retire:
			select {
			case probe.departures <- probe.id:
			case <-ctx.Done():
			}
			return

		case <-scrapes.C:
			value := probe.jitter.Float64()
			seen++
			reservoir = reservoirAdd(reservoir, value, seen, probe.jitter.IntN)
			select {
			case probe.ingest <- sample{Collector: probe.id, Series: probe.series, Value: value}:
			case <-ctx.Done():
				return
			}
		}
	}
}
