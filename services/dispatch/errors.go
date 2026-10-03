// Copyright 2026 Candace Labs

package dispatch

import (
	"errors"
	"fmt"

	"github.com/candacelabs/csf/csf"
)

var (
	// ErrNoSessions reports a service built without a session host.
	ErrNoSessions = errors.New("dispatch: a session host is required")
	// ErrInvalidOption reports a nil option or a value the service cannot use.
	ErrInvalidOption = errors.New("dispatch: invalid option")
	// ErrNotStarted reports an operation before the service was mounted and
	// started, or after it stopped.
	ErrNotStarted = errors.New("dispatch: the service is not running")
	// ErrInvalidSlice reports a slice the service cannot enqueue.
	ErrInvalidSlice = fmt.Errorf("%w: invalid slice", csf.ErrInvalidRequest)
	// ErrInvalidIntent reports an intent the service cannot use.
	ErrInvalidIntent = fmt.Errorf("%w: invalid intent", csf.ErrInvalidRequest)
	// ErrSliceExists reports a second Enqueue of a slice identifier.
	ErrSliceExists = fmt.Errorf("%w: the slice is already enqueued", csf.ErrConflict)
	// ErrUnknownSlice reports an edge end or a slice this service holds no
	// node for.
	ErrUnknownSlice = fmt.Errorf("%w: no such slice", csf.ErrNotFound)
	// ErrUnknownIntent reports a Reprioritize of an intent never declared.
	ErrUnknownIntent = fmt.Errorf("%w: no such intent", csf.ErrNotFound)
	// ErrCycle reports depends_on edges that would close a cycle.
	ErrCycle = fmt.Errorf("%w: depends_on would close a cycle", csf.ErrInvalidRequest)
	// ErrSliceFinished reports a change to a merged or canceled slice.
	ErrSliceFinished = fmt.Errorf("%w: the slice has finished", csf.ErrConflict)
)
