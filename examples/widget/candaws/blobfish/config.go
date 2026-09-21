package blobfish

import (
	"errors"
	"fmt"
	"time"
)

// The faults this engine reports, each a sentinel.
var (
	// ErrStorageClass is an empty storage class. The card interpolates it into
	// a stat line, and an empty one renders as a sentence with a hole in it.
	ErrStorageClass = errors.New("blobfish: the storage class is empty")

	// ErrCadence is a put cadence that is not positive.
	ErrCadence = errors.New("blobfish: the put cadence is not positive")

	// ErrReplicaDelay is a zone latency that is not positive, or a jitter
	// spread that is negative.
	ErrReplicaDelay = errors.New("blobfish: the zone latency is not positive")

	// ErrPatience is a coordinator patience that does not clear the fast
	// zones' worst case. A coordinator that gives up before a healthy zone can
	// answer never reaches a quorum, which looks like an outage rather than
	// like a configuration fault.
	ErrPatience = errors.New("blobfish: the coordinator's patience does not clear the zone latency")

	// ErrSlowZone is a slow zone outside [-1, zoneCount).
	ErrSlowZone = errors.New("blobfish: the slow zone is not a zone")

	// ErrSlowFactor is a slow factor below one: a slow zone that is not slower.
	ErrSlowFactor = errors.New("blobfish: the slow factor is below one")

	// ErrRepairInterval is a repair interval that is not positive. Without one
	// the third copy never catches up, and "eventually consistent" becomes
	// "consistent at no point".
	ErrRepairInterval = errors.New("blobfish: the repair interval is not positive")
)

// Config is one store's shape and pace. Every field is required.
type Config struct {
	// StorageClass is the tier this store's objects are in, and is the text the
	// card interpolates. Glacial is cheaper because retrieving from it is
	// billed separately.
	StorageClass string

	// Cadence is how often the bucket puts an object.
	Cadence time.Duration

	// ReplicaDelay is the base latency of a zone, and DelaySpread is the width
	// of the per-zone jitter added to it.
	ReplicaDelay time.Duration
	DelaySpread  time.Duration

	// SlowZone is the index of the zone the quorum does not wait for, or -1 for
	// a store whose zones are all the same speed. It is the one the repair
	// channel runs to, and the one the pricing page calls eventually
	// consistent so the other two do not have to be.
	SlowZone int

	// SlowFactor multiplies the slow zone's latency.
	SlowFactor int

	// Patience is how long the coordinator waits for a quorum before giving
	// up. It must clear a fast zone's worst case, or no write ever lands.
	Patience time.Duration

	// RepairInterval is how often the repairer probes every zone and issues a
	// repair write to whichever is behind.
	RepairInterval time.Duration

	// Seed seeds the per-zone jitter streams.
	Seed int64
}

// DefaultConfig is the demo's own pace: three zones, one of them six times
// slower than the other two, and a repair sweep often enough that the third
// copy visibly catches up.
func DefaultConfig() Config {
	return Config{
		StorageClass:   "Glacial",
		Cadence:        1200 * time.Millisecond,
		ReplicaDelay:   80 * time.Millisecond,
		DelaySpread:    60 * time.Millisecond,
		SlowZone:       zoneCount - 1,
		SlowFactor:     6,
		Patience:       500 * time.Millisecond,
		RepairInterval: 2 * time.Second,
		Seed:           1,
	}
}

// Validate reports the first fault in a configuration.
func (config Config) Validate() error {
	switch {
	case config.StorageClass == "":
		return ErrStorageClass
	case config.Cadence <= 0:
		return fmt.Errorf("%w: %s", ErrCadence, config.Cadence)
	case config.ReplicaDelay <= 0 || config.DelaySpread < 0:
		return fmt.Errorf("%w: %s with a spread of %s",
			ErrReplicaDelay, config.ReplicaDelay, config.DelaySpread)
	case config.SlowZone < -1 || config.SlowZone >= zoneCount:
		return fmt.Errorf("%w: %d, in a store of %d zones",
			ErrSlowZone, config.SlowZone, zoneCount)
	case config.SlowFactor < 1:
		return fmt.Errorf("%w: %d", ErrSlowFactor, config.SlowFactor)
	case config.Patience <= config.ReplicaDelay+config.DelaySpread:
		return fmt.Errorf("%w: %s against a zone that can take %s",
			ErrPatience, config.Patience, config.ReplicaDelay+config.DelaySpread)
	case config.RepairInterval <= 0:
		return fmt.Errorf("%w: %s", ErrRepairInterval, config.RepairInterval)
	}
	return nil
}

// zoneDelay is one zone's base latency: the configured one, multiplied for the
// zone the quorum does not wait for.
func (config Config) zoneDelay(zone int) time.Duration {
	if zone != config.SlowZone {
		return config.ReplicaDelay
	}
	return config.ReplicaDelay * time.Duration(config.SlowFactor)
}
