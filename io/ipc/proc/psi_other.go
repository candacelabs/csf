// Copyright 2026 Candace Labs

//go:build !linux

package proc

import "errors"

// ErrPressureUnsupported reports a host whose kernel has no pressure files.
var ErrPressureUnsupported = errors.New("ipc/proc: pressure triggers need Linux")

// HostPressure is absent off Linux.
type HostPressure struct{ IPressureSource }

// OpenHostPressure refuses: only Linux reports pressure stalls.
func OpenHostPressure(resource PressureResource) (*HostPressure, error) {
	return nil, ErrPressureUnsupported
}
