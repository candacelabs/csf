package dashbored

import (
	"errors"
	"fmt"
	"time"
)

// The faults this engine reports, each a sentinel.
var (
	// ErrCollectorCount is a collector count outside [1, maxCollectors].
	ErrCollectorCount = errors.New("dashbored: the collector count is out of range")

	// ErrScrapeInterval is a scrape interval that is not positive.
	ErrScrapeInterval = errors.New("dashbored: the scrape interval is not positive")

	// ErrFlushInterval is a flush interval that is not positive, or one shorter
	// than a scrape — which reports a rate over a window nothing arrived in.
	ErrFlushInterval = errors.New("dashbored: the flush interval does not clear the scrape interval")

	// ErrReservoir is a reservoir size below one: a collector that samples
	// nothing is a collector that observes nothing.
	ErrReservoir = errors.New("dashbored: the reservoir size is below one")

	// ErrThreshold is a breach threshold outside (0, 1], the range an
	// observation can be in.
	ErrThreshold = errors.New("dashbored: the breach threshold is not an observable value")

	// ErrDebounce is a debounce below one, which fires an alert on a single
	// observation and resolves it on the next.
	ErrDebounce = errors.New("dashbored: the debounce is below one")

	// ErrAlertName is an empty alert name. The card interpolates it, and an
	// empty one renders as a sentence with a hole in it.
	ErrAlertName = errors.New("dashbored: the alert name is empty")

	// ErrRetention is a retention or query window that is not positive.
	ErrRetention = errors.New("dashbored: the retention or query window is not positive")
)

// maxCollectors is the widest fan-in this aggregator serves. The card draws
// three; the limit is stated and refused rather than left to spawn without
// bound.
const maxCollectors = 16

// Config is one telemetry pipeline's shape and pace. Every field is required.
type Config struct {
	// Collectors is how many probes feed the aggregator.
	Collectors int

	// ScrapeInterval is how often each collector observes.
	ScrapeInterval time.Duration

	// FlushInterval is how often the aggregator rolls up and reports a rate.
	FlushInterval time.Duration

	// ReservoirSize is how many observations a collector's reservoir holds.
	ReservoirSize int

	// BreachThreshold is the observation above which a sample is a breach, in
	// (0, 1].
	BreachThreshold float64

	// DebounceWindows is how many flush windows an alert must keep breaching
	// before it fires, and how many quiet ones before it resolves.
	DebounceWindows int

	// AlertName is what the card renders when something is firing.
	AlertName string

	// RetentionDays is how long the service says it keeps your data, and
	// QueryWindowHours is how much of that you can ask about. The two being
	// different is the product.
	RetentionDays    int
	QueryWindowHours int

	// Seed seeds the per-collector random streams.
	Seed int64
}

// DefaultConfig is the demo's own pace: three collectors, a scrape a second, a
// threshold high enough that a breach is news, and the retention numbers from
// the pricing page.
func DefaultConfig() Config {
	return Config{
		Collectors:       3,
		ScrapeInterval:   700 * time.Millisecond,
		FlushInterval:    2 * time.Second,
		ReservoirSize:    32,
		BreachThreshold:  0.92,
		DebounceWindows:  2,
		AlertName:        "p99_latency",
		RetentionDays:    395,
		QueryWindowHours: 2,
		Seed:             1,
	}
}

// Validate reports the first fault in a configuration.
func (config Config) Validate() error {
	switch {
	case config.Collectors < 1 || config.Collectors > maxCollectors:
		return fmt.Errorf("%w: %d, which is not in [1, %d]",
			ErrCollectorCount, config.Collectors, maxCollectors)
	case config.ScrapeInterval <= 0:
		return fmt.Errorf("%w: %s", ErrScrapeInterval, config.ScrapeInterval)
	case config.FlushInterval <= config.ScrapeInterval:
		return fmt.Errorf("%w: %s against a %s scrape",
			ErrFlushInterval, config.FlushInterval, config.ScrapeInterval)
	case config.ReservoirSize < 1:
		return fmt.Errorf("%w: %d", ErrReservoir, config.ReservoirSize)
	case config.BreachThreshold <= 0 || config.BreachThreshold > 1:
		return fmt.Errorf("%w: %v", ErrThreshold, config.BreachThreshold)
	case config.DebounceWindows < 1:
		return fmt.Errorf("%w: %d", ErrDebounce, config.DebounceWindows)
	case config.AlertName == "":
		return ErrAlertName
	case config.RetentionDays <= 0 || config.QueryWindowHours <= 0:
		return fmt.Errorf("%w: %d days retained, %d hours queryable",
			ErrRetention, config.RetentionDays, config.QueryWindowHours)
	}
	return nil
}
