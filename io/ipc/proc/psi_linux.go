// Copyright 2026 Candace Labs

//go:build linux

package proc

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"os"

	"golang.org/x/sys/unix"
)

// hostPressureRoot is the kernel's pressure directory.
const hostPressureRoot = "/proc/pressure/"

// HostPressure is the kernel's pressure file for one resource, opened for a
// trigger, and an eventfd that wakes a Wait when its context ends, so a
// waiting goroutine blocks in poll(2) with no timeout.
type HostPressure struct {
	file *os.File
	wake int
}

var _ IPressureSource = (*HostPressure)(nil)

// ErrPressureClosed reports a wait on a pressure file the kernel closed or
// failed.
var ErrPressureClosed = errors.New("ipc/proc: pressure file failed")

// OpenHostPressure opens the kernel's pressure file for resource, read and
// write, as a trigger needs.
func OpenHostPressure(resource PressureResource) (*HostPressure, error) {
	file, err := os.OpenFile(hostPressureRoot+string(resource), os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("ipc/proc: open %s pressure: %w", resource, err)
	}
	wake, err := unix.Eventfd(0, unix.EFD_CLOEXEC|unix.EFD_NONBLOCK)
	if err != nil {
		return nil, errors.Join(fmt.Errorf("ipc/proc: eventfd: %w", err), file.Close())
	}
	return &HostPressure{file: file, wake: wake}, nil
}

// Arm writes the trigger, with its terminating NUL as the kernel asks.
func (pressure *HostPressure) Arm(trigger string) error {
	if _, err := pressure.file.Write(append([]byte(trigger), 0)); err != nil {
		return fmt.Errorf("%w: %q: %w", ErrInvalidTrigger, trigger, err)
	}
	return nil
}

// Wait blocks in poll(2) until the kernel raises POLLPRI, ctx ends or the
// file fails.
func (pressure *HostPressure) Wait(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() {
		var one [8]byte
		binary.NativeEndian.PutUint64(one[:], 1)
		_, _ = unix.Write(pressure.wake, one[:])
	})
	defer stop()
	descriptors := []unix.PollFd{
		{Fd: int32(pressure.file.Fd()), Events: unix.POLLPRI},
		{Fd: int32(pressure.wake), Events: unix.POLLIN},
	}
	for {
		if _, err := unix.Poll(descriptors, -1); err != nil {
			if errors.Is(err, unix.EINTR) {
				continue
			}
			return fmt.Errorf("%w: %w", ErrPressureClosed, err)
		}
		switch {
		case descriptors[1].Revents != 0:
			return ctx.Err()
		case descriptors[0].Revents&(unix.POLLERR|unix.POLLNVAL) != 0:
			return ErrPressureClosed
		case descriptors[0].Revents&unix.POLLPRI != 0:
			return nil
		}
	}
}

// Close closes the file, which removes its trigger, and the eventfd.
func (pressure *HostPressure) Close() error {
	return errors.Join(pressure.file.Close(), unix.Close(pressure.wake))
}
