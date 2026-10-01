package coldstart

import (
	"errors"
	"fmt"
	"time"
)

// The faults this engine reports, each a sentinel.
var (
	// ErrRuntimeName is an empty runtime name. The card interpolates it into a
	// stat line, and an empty one renders as a sentence with a hole in it.
	ErrRuntimeName = errors.New("coldstart: the runtime name is empty")

	// ErrArrivalRate is an arrival interval that is not positive.
	ErrArrivalRate = errors.New("coldstart: the arrival interval is not positive")

	// ErrStartupBudget is a start-up budget that is not positive. Scaling to
	// zero instantly and back up instantly is a different product.
	ErrStartupBudget = errors.New("coldstart: the start-up budget is not positive")

	// ErrWorkDuration is an invocation duration that is not positive.
	ErrWorkDuration = errors.New("coldstart: the invocation duration is not positive")

	// ErrMaxInstances is a pool ceiling outside [1, maxInstances].
	ErrMaxInstances = errors.New("coldstart: the pool ceiling is out of range")

	// ErrBacklogCeiling is a backlog ceiling below one: a dispatcher that
	// cannot queue anything drops the invocation that caused it to spawn.
	ErrBacklogCeiling = errors.New("coldstart: the backlog ceiling is below one")

	// ErrIdleSweeps is an idle tolerance below one, which reaps an instance in
	// the same sweep it warmed in.
	ErrIdleSweeps = errors.New("coldstart: the idle tolerance is below one")

	// ErrReapInterval is a reap interval that is not positive. Without one
	// nothing ever scales to zero, which is the other half of the product.
	ErrReapInterval = errors.New("coldstart: the reap interval is not positive")

	// ErrCallPatience is a caller patience that does not clear a cold start
	// plus an invocation, so every first call times out.
	ErrCallPatience = errors.New("coldstart: the caller's patience does not clear a cold start")
)

// maxInstances is the largest pool this dispatcher runs. The card draws two;
// the limit is stated and refused rather than left to spawn without bound.
const maxInstances = 16

// Config is one runtime's shape and pace. Every field is required.
type Config struct {
	// RuntimeName is the runtime the card interpolates into a stat line.
	RuntimeName string

	// ArrivalInterval is how often the caller invokes.
	ArrivalInterval time.Duration

	// StartupBudget is what an instance pays before it can serve anything, and
	// is the number the dashboard reports and the customer is billed for.
	StartupBudget time.Duration

	// WorkDuration is how long a warm instance takes to answer.
	WorkDuration time.Duration

	// MaxInstances is the pool ceiling.
	MaxInstances int

	// BacklogCeiling is how many invocations may wait on a start-up before the
	// dispatcher stops accepting and starts dropping the oldest.
	BacklogCeiling int

	// IdleSweeps is how many reap sweeps an instance may sit idle through
	// before its channel is closed and its goroutine returns.
	IdleSweeps int

	// ReapInterval is how often the dispatcher looks for instances to freeze.
	ReapInterval time.Duration

	// CallPatience is how long a caller waits for its own reply channel before
	// giving up on it.
	CallPatience time.Duration

	// WarmFloor is how many instances survive every sweep. Zero is legal and is
	// what "scales to zero" means; the prewarm control raises it to one, which
	// the pricing page calls Serverful.
	WarmFloor int
}

// DefaultConfig is the demo's own pace: a call every second and a bit, a
// start-up long enough to watch being paid, and a reaper patient enough that an
// instance is not gone before anybody saw it.
func DefaultConfig() Config {
	return Config{
		RuntimeName:     "candace/go1.26",
		ArrivalInterval: 1200 * time.Millisecond,
		StartupBudget:   800 * time.Millisecond,
		WorkDuration:    150 * time.Millisecond,
		MaxInstances:    2,
		BacklogCeiling:  4,
		IdleSweeps:      3,
		ReapInterval:    2 * time.Second,
		CallPatience:    5 * time.Second,
		WarmFloor:       0,
	}
}

// Validate reports the first fault in a configuration.
func (config Config) Validate() error {
	switch {
	case config.RuntimeName == "":
		return ErrRuntimeName
	case config.ArrivalInterval <= 0:
		return fmt.Errorf("%w: %s", ErrArrivalRate, config.ArrivalInterval)
	case config.StartupBudget <= 0:
		return fmt.Errorf("%w: %s", ErrStartupBudget, config.StartupBudget)
	case config.WorkDuration <= 0:
		return fmt.Errorf("%w: %s", ErrWorkDuration, config.WorkDuration)
	case config.MaxInstances < 1 || config.MaxInstances > maxInstances:
		return fmt.Errorf("%w: %d, which is not in [1, %d]",
			ErrMaxInstances, config.MaxInstances, maxInstances)
	case config.BacklogCeiling < 1:
		return fmt.Errorf("%w: %d", ErrBacklogCeiling, config.BacklogCeiling)
	case config.IdleSweeps < 1:
		return fmt.Errorf("%w: %d", ErrIdleSweeps, config.IdleSweeps)
	case config.ReapInterval <= 0:
		return fmt.Errorf("%w: %s", ErrReapInterval, config.ReapInterval)
	case config.CallPatience <= config.StartupBudget+config.WorkDuration:
		return fmt.Errorf("%w: %s against a %s start-up and a %s invocation",
			ErrCallPatience, config.CallPatience, config.StartupBudget, config.WorkDuration)
	case config.WarmFloor < 0 || config.WarmFloor > config.MaxInstances:
		return fmt.Errorf("%w: a warm floor of %d above a ceiling of %d",
			ErrMaxInstances, config.WarmFloor, config.MaxInstances)
	}
	return nil
}
