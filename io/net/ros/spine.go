// Copyright 2026 Candace Labs

// Package ros is CSF's boundary to the low-level spine controller. The spine
// is the fast controller that drives hardware; it is external and ROS-side,
// not part of CSF. CSF reaches it over the network, so the boundary is an ipc
// capability: a caller is granted an [ISpine] through its constructor and
// never interprets a controller program in-process.
//
// The seam is the shared contract proto/candace/brainspine/v1: CSF submits an
// Action and observes an Observation. No ROS transport ships yet; the
// [DisconnectedSpine] answers every call with a [NotConnectedError] so callers
// render "no spine connected" instead of failing.
package ros

import (
	"context"
	"errors"
	"fmt"

	brainspinev1 "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

// NotConnectedStatus is the human-facing status a caller renders when no
// spine is connected. It is a stable string: views and tests match it.
const NotConnectedStatus = "no spine connected"

// NotConnectedReason is the machine-facing reason a fallback Action carries
// when no spine is connected.
const NotConnectedReason = "no_spine_connected"

// Operation names a call across the spine boundary.
type Operation string

const (
	// OperationSubmit hands the spine one proposed action.
	OperationSubmit Operation = "submit"
	// OperationObserve reads the spine's latest observation.
	OperationObserve Operation = "observe"
)

// ISpine is the low-level controller as CSF sees it. A submitted action is a
// proposal for the spine to admit; the spine owns admission, actuation and
// its own fail-safe behavior.
type ISpine interface {
	// Submit proposes one action to the spine.
	Submit(ctx context.Context, action *brainspinev1.Action) error
	// Observe returns the spine's latest observation.
	Observe(ctx context.Context) (*brainspinev1.Observation, error)
}

// NotConnectedError reports that no spine is connected for an operation.
type NotConnectedError struct {
	// Operation is the call that found no spine.
	Operation Operation
}

func (failure *NotConnectedError) Error() string {
	return fmt.Sprintf("%s: %s requires the external ROS-side controller", NotConnectedStatus, failure.Operation)
}

// IsNotConnected reports whether err, or any error it wraps, is a
// [NotConnectedError].
func IsNotConnected(err error) bool {
	var failure *NotConnectedError
	return errors.As(err, &failure)
}

// DisconnectedSpine is the spine stub: no transport and no state. Every call
// returns a [NotConnectedError], or the context's error once ctx is done. It
// is immutable, so one value is safe to share between goroutines.
type DisconnectedSpine struct{}

var _ ISpine = (*DisconnectedSpine)(nil)

// NewDisconnectedSpine returns the stub spine a CSF host is granted until a
// ROS transport is configured.
func NewDisconnectedSpine() *DisconnectedSpine { return &DisconnectedSpine{} }

// Submit never reaches a controller.
func (spine *DisconnectedSpine) Submit(ctx context.Context, action *brainspinev1.Action) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return &NotConnectedError{Operation: OperationSubmit}
}

// Observe never reaches a controller.
func (spine *DisconnectedSpine) Observe(ctx context.Context) (*brainspinev1.Observation, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return nil, &NotConnectedError{Operation: OperationObserve}
}

// Status is the human-facing problem a view renders for spine, or the empty
// string when an observation succeeds. A nil spine, or one whose observation
// reports [NotConnectedError], renders [NotConnectedStatus]; any other
// observation failure is rendered with its cause rather than hidden.
func Status(ctx context.Context, spine ISpine) string {
	if spine == nil {
		return NotConnectedStatus
	}
	_, err := spine.Observe(ctx)
	switch {
	case err == nil:
		return ""
	case IsNotConnected(err):
		return NotConnectedStatus
	default:
		return fmt.Sprintf("spine observation failed: %v", err)
	}
}
