package queuecumber

import (
	"errors"
	"fmt"
	"time"
)

// The faults this engine reports, each a sentinel so a caller can tell them
// apart without reading an English sentence.
var (
	// ErrWorkerCount is a worker count outside [1, maxWorkers].
	ErrWorkerCount = errors.New("queuecumber: the worker count is out of range")

	// ErrCadence is a submission cadence that is not positive.
	ErrCadence = errors.New("queuecumber: the submission cadence is not positive")

	// ErrSweep is a sweep interval that is not positive. Without one no lease
	// ever expires, and at-least-once quietly becomes at-most-once.
	ErrSweep = errors.New("queuecumber: the sweep interval is not positive")

	// ErrVisibility is a visibility timeout of fewer than two sweeps. One sweep
	// is a lease that can expire while the worker is still holding it, which
	// turns every delivery into a redelivery.
	ErrVisibility = errors.New("queuecumber: the visibility timeout is under two sweeps")

	// ErrCapacity is a queue capacity that is not positive: a queue that can
	// hold nothing refuses its own first message.
	ErrCapacity = errors.New("queuecumber: the queue capacity is not positive")

	// ErrAttemptCeiling is an attempt ceiling below one. A message that may be
	// tried zero times is dead-lettered before anybody looks at it.
	ErrAttemptCeiling = errors.New("queuecumber: the attempt ceiling is below one")

	// ErrDropRate is a drop rate outside [0, 1].
	ErrDropRate = errors.New("queuecumber: the drop rate is not a probability")
)

// maxWorkers is the largest worker pool this broker runs.
//
// The ceiling is the width of the "which workers are leasing" set, which is
// tracked as a fixed array so the broker's locals stay a copyable value. The
// card draws three; the limit is stated and refused rather than left to run off
// the end of an array.
const maxWorkers = 8

// Config is one broker's shape and pace. Every field is required.
type Config struct {
	// Workers is how many worker goroutines lease from this broker.
	Workers int

	// Cadence is how often the producer submits a message.
	Cadence time.Duration

	// WorkDuration is how long a worker holds a message before acknowledging
	// it, or not.
	WorkDuration time.Duration

	// SweepInterval is how often the expiry goroutine ages every live lease by
	// one. It is the only clock on that side of the engine.
	SweepInterval time.Duration

	// VisibilitySweeps is how many sweeps a lease survives before it expires.
	// Two is the floor: one would let a lease expire while its worker was
	// still working, which turns every delivery into a redelivery.
	VisibilitySweeps int

	// Capacity is how many ready messages the queue accepts before it stops
	// accepting. A full queue is what closes the intake, and closing the intake
	// is the whole of what this bounds.
	//
	// It is an admission bound and not a ceiling on depth. A lease that expires
	// and a redrive both push onto the ready queue without consulting it, by
	// design: refusing a message the queue has already accepted would drop it,
	// and a queue that drops what it accepted is not delivering at least once.
	// So depth can stand above capacity, and what capacity promises is the
	// narrower thing — a *submission* arriving at a full queue is counted as
	// refused rather than quietly queued.
	Capacity int

	// AttemptCeiling is how many times a message is leased before it is moved
	// to the dead-letter store rather than made ready again.
	AttemptCeiling int

	// DropRate is how often a worker finishes with a message and says nothing,
	// in [0, 1]. It is the whole of at-least-once: a message nobody
	// acknowledged is a message that comes back.
	DropRate float64

	// Seed seeds the per-worker random streams.
	Seed int64
}

// DefaultConfig is the demo's own pace: three workers, a message every second
// and a half, and enough dropped acknowledgements that a redelivery happens
// while somebody is watching.
func DefaultConfig() Config {
	return Config{
		Workers:          3,
		Cadence:          1500 * time.Millisecond,
		WorkDuration:     700 * time.Millisecond,
		SweepInterval:    600 * time.Millisecond,
		VisibilitySweeps: 3,
		Capacity:         12,
		AttemptCeiling:   3,
		DropRate:         0.3,
		Seed:             1,
	}
}

// Validate reports the first fault in a configuration. It is called by
// [NewBroker], so a broker that exists is one whose pace can actually deliver.
func (config Config) Validate() error {
	switch {
	case config.Workers < 1 || config.Workers > maxWorkers:
		return fmt.Errorf("%w: %d, which is not in [1, %d]", ErrWorkerCount, config.Workers, maxWorkers)
	case config.Cadence <= 0:
		return fmt.Errorf("%w: %s", ErrCadence, config.Cadence)
	case config.SweepInterval <= 0:
		return fmt.Errorf("%w: %s", ErrSweep, config.SweepInterval)
	case config.VisibilitySweeps < 2:
		return fmt.Errorf("%w: %d", ErrVisibility, config.VisibilitySweeps)
	case config.WorkDuration <= 0:
		return fmt.Errorf("%w: %s", ErrCadence, config.WorkDuration)
	case config.Capacity <= 0:
		return fmt.Errorf("%w: %d", ErrCapacity, config.Capacity)
	case config.AttemptCeiling < 1:
		return fmt.Errorf("%w: %d", ErrAttemptCeiling, config.AttemptCeiling)
	case config.DropRate < 0 || config.DropRate > 1:
		return fmt.Errorf("%w: %v", ErrDropRate, config.DropRate)
	}
	return nil
}
