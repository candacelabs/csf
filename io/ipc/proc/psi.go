// Copyright 2026 Candace Labs

package proc

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=psi.go -destination=mocks/mock_psi.go -package=mocks

// PressureResource names one of the kernel's pressure stall files.
type PressureResource string

// The resources the kernel reports pressure for, under /proc/pressure.
const (
	PressureCPU    PressureResource = "cpu"
	PressureMemory PressureResource = "memory"
	PressureIO     PressureResource = "io"
)

// pressureDirectory is the resources' directory within the process table.
const pressureDirectory = "pressure"

// PressureKind is a pressure line: some tasks stalled, or all of them.
type PressureKind string

const (
	PressureSome PressureKind = "some"
	PressureFull PressureKind = "full"
)

// The kernel's bounds on a trigger's window (Documentation/accounting/psi.rst).
const (
	minimumTriggerWindow = 500 * time.Millisecond
	maximumTriggerWindow = 10 * time.Second
)

var (
	// ErrUnknownPressure reports a resource the kernel has no pressure file for.
	ErrUnknownPressure = errors.New("ipc/proc: unknown pressure resource")
	// ErrPressureFormat reports a pressure file this parser cannot read.
	ErrPressureFormat = errors.New("ipc/proc: unreadable pressure file")
	// ErrInvalidTrigger reports a trigger outside the kernel's bounds, or one
	// the kernel refused.
	ErrInvalidTrigger = errors.New("ipc/proc: invalid pressure trigger")
)

// PressureLine is one line of a pressure file: the stalled share of time,
// in percent, over the last 10, 60 and 300 seconds, and the total stall.
type PressureLine struct {
	Avg10  float64
	Avg60  float64
	Avg300 float64
	Total  time.Duration
}

// Pressure is one resource's pressure: some tasks stalled, and all of them.
// The kernel reports no full line for CPU before 5.13; it then reads zero.
type Pressure struct {
	Some PressureLine
	Full PressureLine
}

func knownResource(resource PressureResource) bool {
	return resource == PressureCPU || resource == PressureMemory || resource == PressureIO
}

// ReadPressure reads one resource's pressure from the process table granted
// as /proc.
func ReadPressure(processes iofs.IFiles, resource PressureResource) (Pressure, error) {
	var pressure Pressure
	if !knownResource(resource) {
		return pressure, fmt.Errorf("%w: %q", ErrUnknownPressure, resource)
	}
	content, err := processes.ReadFile(pressureDirectory + "/" + string(resource))
	if err != nil {
		return pressure, err
	}
	return ParsePressure(string(content))
}

// ParsePressure reads the text of a pressure file.
func ParsePressure(text string) (Pressure, error) {
	var pressure Pressure
	seen := false
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 5 {
			return pressure, fmt.Errorf("%w: %q", ErrPressureFormat, line)
		}
		var parsed PressureLine
		for _, field := range fields[1:] {
			name, value, found := strings.Cut(field, "=")
			if !found {
				return pressure, fmt.Errorf("%w: %q", ErrPressureFormat, line)
			}
			var err error
			switch name {
			case "avg10":
				parsed.Avg10, err = strconv.ParseFloat(value, 64)
			case "avg60":
				parsed.Avg60, err = strconv.ParseFloat(value, 64)
			case "avg300":
				parsed.Avg300, err = strconv.ParseFloat(value, 64)
			case "total":
				var micros uint64
				micros, err = strconv.ParseUint(value, 10, 64)
				parsed.Total = time.Duration(micros) * time.Microsecond
			default:
				err = errors.New("unknown field")
			}
			if err != nil {
				return pressure, fmt.Errorf("%w: %q: %w", ErrPressureFormat, line, err)
			}
		}
		switch PressureKind(fields[0]) {
		case PressureSome:
			pressure.Some, seen = parsed, true
		case PressureFull:
			pressure.Full = parsed
		default:
			return pressure, fmt.Errorf("%w: %q", ErrPressureFormat, line)
		}
	}
	if !seen {
		return pressure, fmt.Errorf("%w: no some line", ErrPressureFormat)
	}
	return pressure, nil
}

// PressureTrigger is a kernel pressure trigger: an event whenever the kind
// of stall reaches Stall within any Window.
type PressureTrigger struct {
	Kind   PressureKind
	Stall  time.Duration
	Window time.Duration
}

// String is the trigger as the kernel reads it: "some 150000 1000000".
func (trigger PressureTrigger) String() string {
	return fmt.Sprintf("%s %d %d", trigger.Kind, trigger.Stall.Microseconds(), trigger.Window.Microseconds())
}

// Validate refuses a trigger the kernel would: an unknown kind, a window
// outside 500 ms to 10 s, or a stall that is not positive or exceeds the
// window. An unprivileged process may also be held to a window that is a
// multiple of 2 s; the kernel says so when the trigger is armed.
func (trigger PressureTrigger) Validate() error {
	switch {
	case trigger.Kind != PressureSome && trigger.Kind != PressureFull:
		return fmt.Errorf("%w: kind %q is neither some nor full", ErrInvalidTrigger, trigger.Kind)
	case trigger.Window < minimumTriggerWindow || trigger.Window > maximumTriggerWindow:
		return fmt.Errorf("%w: window %s is outside %s to %s", ErrInvalidTrigger, trigger.Window, minimumTriggerWindow, maximumTriggerWindow)
	case trigger.Stall <= 0 || trigger.Stall > trigger.Window:
		return fmt.Errorf("%w: stall %s must be positive and at most the window %s", ErrInvalidTrigger, trigger.Stall, trigger.Window)
	}
	return nil
}

// IPressureSource is one opened pressure file a trigger is armed on: the
// kernel file on the host, a double in a spec.
type IPressureSource interface {
	// Arm writes the trigger; a refused write is ErrInvalidTrigger.
	Arm(trigger string) error
	// Wait blocks until the trigger fires, ctx ends (its error), or the
	// file fails.
	Wait(ctx context.Context) error
	Close() error
}

// WatchPressureSource arms trigger on source and delivers one value per
// event the kernel raises, dropping an event while the previous one is
// undelivered. The channel closes, and source is closed, when ctx ends or
// the source fails; the caller reads the reading it wants with
// [ReadPressure].
func WatchPressureSource(ctx context.Context, source IPressureSource, trigger PressureTrigger) (<-chan struct{}, error) {
	if err := trigger.Validate(); err != nil {
		return nil, errors.Join(err, source.Close())
	}
	if err := source.Arm(trigger.String()); err != nil {
		return nil, errors.Join(err, source.Close())
	}
	events := make(chan struct{}, 1)
	// Owned by the watch: it ends when ctx does or the source fails, and the
	// closed channel is its join.
	go func() {
		defer close(events)
		defer func() { _ = source.Close() }()
		for source.Wait(ctx) == nil {
			select {
			case events <- struct{}{}:
			default:
			}
		}
	}()
	return events, nil
}

// WatchPressure opens the host's pressure file for resource and watches it
// with trigger, as [WatchPressureSource] does. It needs no privilege beyond
// the kernel's limit on an unprivileged trigger's window.
func WatchPressure(ctx context.Context, resource PressureResource, trigger PressureTrigger) (<-chan struct{}, error) {
	if !knownResource(resource) {
		return nil, fmt.Errorf("%w: %q", ErrUnknownPressure, resource)
	}
	source, err := OpenHostPressure(resource)
	if err != nil {
		return nil, err
	}
	return WatchPressureSource(ctx, source, trigger)
}
