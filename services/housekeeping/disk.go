// Copyright 2026 Candace Labs

package housekeeping

import (
	"context"
	"errors"
	"fmt"
	"slices"

	cronservice "github.com/candacelabs/csf/services/cron"
	"github.com/candacelabs/csf/services/harness/session"
)

// diskFloor is the free space the host must keep, and how it was derived.
type diskFloor struct {
	bytes      uint64
	derivation string
}

// deriveFloor derives the floor from what the sessions measure now: the
// largest run directory any recorded session has is the per-session peak,
// and the floor is room for every running session to reach that peak plus
// one more peak of headroom, so the next admitted session fits too.
func (housekeeper *Housekeeper) deriveFloor(ctx context.Context, sessions census) diskFloor {
	var peak uint64
	peakSession := ""
	for _, state := range slices.Concat(sessions.active, sessions.ended) {
		size := housekeeper.pathBytes(ctx, session.RunDirectory(housekeeper.stateDirectory, state.GetAssignmentId()))
		if size > peak {
			peak, peakSession = size, state.GetAssignmentId()
		}
	}
	running := uint64(len(sessions.active))
	return diskFloor{
		bytes: peak * (running + 1),
		derivation: fmt.Sprintf("per-session peak %d bytes (run directory of %s) x (%d running + 1 headroom) = %d bytes",
			peak, peakSession, running, peak*(running+1)),
	}
}

// DiskFloor measures free disk against the derived floor. Below it, it
// holds the harness's admission and runs the reclaim steps in order (the
// sessions, Docker, the shared cache), measuring again after each, until
// free disk is above the floor; it releases admission once it is.
func (housekeeper *Housekeeper) DiskFloor(ctx context.Context, occurrence cronservice.Occurrence) error {
	at := passOf(occurrence)
	sessions, err := housekeeper.census(ctx)
	if err != nil {
		return err
	}
	floor := housekeeper.deriveFloor(ctx, sessions)
	free, err := housekeeper.freeBytes(ctx)
	if err != nil {
		return err
	}
	if err := housekeeper.record(at, Record{Type: RecordFloor, What: housekeeper.stateDirectory, Bytes: free, Detail: floor.derivation}); err != nil {
		return err
	}
	if free >= floor.bytes {
		housekeeper.releaseAdmission()
		return nil
	}
	housekeeper.holdAdmission(fmt.Sprintf("%d free bytes are below the housekeeping floor: %s", free, floor.derivation))
	steps := []func(ctx context.Context, at pass) error{
		housekeeper.reclaimSessions,
		housekeeper.reclaimDocker,
		housekeeper.trimSharedCache,
	}
	var failures []error
	for _, step := range steps {
		failures = append(failures, step(ctx, at))
		if free, err = housekeeper.freeBytes(ctx); err != nil {
			return errors.Join(append(failures, err)...)
		}
		if free >= floor.bytes {
			housekeeper.releaseAdmission()
			return errors.Join(failures...)
		}
	}
	return errors.Join(append(failures, fmt.Errorf("%w: %d free, floor %d", ErrBelowFloor, free, floor.bytes))...)
}

func (housekeeper *Housekeeper) holdAdmission(reason string) {
	if housekeeper.admission != nil && !housekeeper.dryRun {
		housekeeper.admission.HoldAdmission(reason)
	}
}

func (housekeeper *Housekeeper) releaseAdmission() {
	if housekeeper.admission != nil && !housekeeper.dryRun {
		housekeeper.admission.ReleaseAdmission()
	}
}
