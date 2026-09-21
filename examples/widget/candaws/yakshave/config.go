package yakshave

import (
	"errors"
	"fmt"
	"time"
)

// The faults this engine reports. Each is a sentinel because a caller that
// wants to tell "your cadence is not positive" from "your failure rate is not a
// probability" should not have to read an English sentence to do it.
var (
	// ErrCadence is a cadence that is not positive: a pipeline that starts a
	// run never is a pipeline that hangs rather than one that errors.
	ErrCadence = errors.New("yakshave: the cadence is not positive")

	// ErrStageDuration is a stage duration that is not positive, or one long
	// enough that the whole chain cannot finish inside one cadence — which is a
	// pipeline permanently behind its own schedule.
	ErrStageDuration = errors.New("yakshave: the stage duration does not fit the cadence")

	// ErrQuota is a quota that is not positive. Zero would mean a pipeline that
	// refuses its own first run, which looks exactly like one that is broken.
	ErrQuota = errors.New("yakshave: the quota is not positive")

	// ErrFailureRate is a failure rate outside [0, 1].
	ErrFailureRate = errors.New("yakshave: the failure rate is not a probability")

	// ErrRetryCeiling is a negative retry ceiling. Zero is legal and means a
	// red build is never retried automatically.
	ErrRetryCeiling = errors.New("yakshave: the retry ceiling is negative")
)

// Config is one pipeline's pace and luck. Every field is required: there is no
// zero value that means "work it out".
type Config struct {
	// Cadence is how often a run starts, and how often the meter bills a
	// minute. One cadence is one billed minute, which is the joke's unit.
	Cadence time.Duration

	// StageDuration is how long one stage holds an artifact. The whole chain
	// must fit inside one cadence, or every run queues behind the one before it.
	StageDuration time.Duration

	// QuotaMinutes is the billing window's budget. A run may not start on a
	// zero balance, and a retry is charged the same as a first attempt.
	QuotaMinutes int

	// RetryCeiling is how many times a red build is retried automatically
	// before the quota, or the ceiling, arrives.
	RetryCeiling int

	// FailureRate is how much trouble the chain is in, in [0, 1]. Each stage
	// weights it by its own share; zero is a pipeline that never fails and one
	// is a pipeline that never ships.
	FailureRate float64

	// Seed seeds the per-stage random streams. Equal seeds give equal draws;
	// they do not give an equal scheduler, so a run is reproducible in which
	// stages fail and not in exactly when.
	Seed int64
}

// DefaultConfig is the demo's own pace: a run every four seconds through a
// chain that takes about half of that, a quota generous enough to watch, and
// enough trouble that a retry happens while somebody is looking.
func DefaultConfig() Config {
	return Config{
		Cadence:       4 * time.Second,
		StageDuration: 420 * time.Millisecond,
		QuotaMinutes:  90,
		RetryCeiling:  2,
		FailureRate:   0.35,
		Seed:          1,
	}
}

// Validate reports the first fault in a configuration.
//
// It is called by [NewPipeline], so a pipeline that exists is one whose pace can
// actually ship something — and the checks are the ones whose violation is a
// pipeline that runs and never finishes, which looks like a hang rather than
// like an error.
func (config Config) Validate() error {
	switch {
	case config.Cadence <= 0:
		return fmt.Errorf("%w: %s", ErrCadence, config.Cadence)
	case config.StageDuration <= 0 ||
		config.StageDuration*time.Duration(stageCount) >= config.Cadence:
		return fmt.Errorf("%w: %s across %d stages against a %s cadence",
			ErrStageDuration, config.StageDuration, stageCount, config.Cadence)
	case config.QuotaMinutes <= 0:
		return fmt.Errorf("%w: %d", ErrQuota, config.QuotaMinutes)
	case config.RetryCeiling < 0:
		return fmt.Errorf("%w: %d", ErrRetryCeiling, config.RetryCeiling)
	case config.FailureRate < 0 || config.FailureRate > 1:
		return fmt.Errorf("%w: %v", ErrFailureRate, config.FailureRate)
	}
	return nil
}
