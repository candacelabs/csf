// Copyright 2026 Candace Labs

package upgrade

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/pkg/eventually"
)

const (
	// TailLines is how many of the new host's last log lines a restart
	// prints when the host exits or never answers.
	TailLines = 20
	// DefaultTick is how often a restart says it is still waiting: often
	// enough that an operator never watches a silent terminal for long, rare
	// enough that a 30 s start is a handful of lines.
	DefaultTick  = 5 * time.Second
	tailPrefix   = "  | "
	noLogYet     = "(no log line yet)"
	elapsedRound = 100 * time.Millisecond
)

var (
	// ErrHostExited reports a new host that exited before it answered.
	ErrHostExited = errors.New("upgrade: the new host exited before it answered")
	// ErrHostStuck reports a host that did not exit, or a new host that did
	// not answer, within its budget.
	ErrHostStuck = errors.New("upgrade: the host did not change state within its budget")
)

// The two waits of a restart. A stop ends every executor and the HTTP
// listeners, which took under 2 s on this host; a start brings up the owned
// database and resumes every open run before it answers, which took under
// 5 s. Both budgets are generous multiples of that, so a loaded machine does
// not fail a good release.
var (
	DefaultStopBudget  = eventually.Budget{Within: 2 * time.Minute, Interval: 250 * time.Millisecond}
	DefaultReadyBudget = eventually.Budget{Within: 5 * time.Minute, Interval: 500 * time.Millisecond}
)

// ExitStatus is how a started host's process ended.
type ExitStatus struct {
	Code int
	Err  error
}

// StartedHost is a host process a restart started.
type StartedHost struct {
	PID int
	// Log is where its output goes.
	Log string
	// Exited delivers once, when the process exits.
	Exited <-chan ExitStatus
}

// HostControl is what a restart needs from the binary that runs it, each a
// function value the binary fills from its capabilities and a spec from
// fakes.
type HostControl struct {
	// Current is the host the record names and whether its process runs.
	Current func() (pid int, running bool, err error)
	// Stop asks the host to stop and returns without waiting for it.
	Stop func(ctx context.Context, pid int) error
	// Alive is whether pid's process has not exited.
	Alive func(pid int) bool
	// Start starts the new host.
	Start func(ctx context.Context) (StartedHost, error)
	// Ready is whether the host started as pid answers, and what it saw.
	Ready func(ctx context.Context, pid int) (bool, string)
	// LogTail is the last lines of the new host's log, oldest first.
	LogTail func(lines int) []string
}

// HostRestarter stops the running host and starts the new one, saying what
// it does as it does it.
type HostRestarter struct {
	control  HostControl
	progress Progress
	clock    clock.IClock
	stop     eventually.Budget
	ready    eventually.Budget
	tick     time.Duration
}

// RestarterOption configures a [HostRestarter].
type RestarterOption func(restarter *HostRestarter) error

// WithRestartProgress grants the line every step is reported through.
func WithRestartProgress(progress Progress) RestarterOption {
	return func(restarter *HostRestarter) error {
		if progress == nil {
			return fmt.Errorf("%w: nil progress", ErrInvalidOption)
		}
		restarter.progress = progress
		return nil
	}
}

// WithRestartClock grants the clock elapsed times and ticks are read on.
func WithRestartClock(source clock.IClock) RestarterOption {
	return func(restarter *HostRestarter) error {
		if source == nil {
			return fmt.Errorf("%w: nil clock", ErrInvalidOption)
		}
		restarter.clock = source
		return nil
	}
}

// WithBudgets replaces the stop and ready budgets.
func WithBudgets(stop eventually.Budget, ready eventually.Budget) RestarterOption {
	return func(restarter *HostRestarter) error {
		if stop.Within <= 0 || ready.Within <= 0 {
			return fmt.Errorf("%w: budgets must be positive", ErrInvalidOption)
		}
		restarter.stop, restarter.ready = stop, ready
		return nil
	}
}

// WithTick replaces how often a restart says it is still waiting.
func WithTick(tick time.Duration) RestarterOption {
	return func(restarter *HostRestarter) error {
		if tick < 0 {
			return fmt.Errorf("%w: negative tick", ErrInvalidOption)
		}
		restarter.tick = tick
		return nil
	}
}

// NewHostRestarter validates the control and the whole option set before
// building the restarter.
func NewHostRestarter(control HostControl, options ...RestarterOption) (*HostRestarter, error) {
	if control.Current == nil || control.Stop == nil || control.Alive == nil || control.Start == nil ||
		control.Ready == nil || control.LogTail == nil {
		return nil, fmt.Errorf("%w: every host control is required", ErrInvalidOption)
	}
	restarter := &HostRestarter{control: control, progress: silent, clock: clock.NewSystemClock(),
		stop: DefaultStopBudget, ready: DefaultReadyBudget, tick: DefaultTick}
	for _, option := range options {
		if err := option(restarter); err != nil {
			return nil, err
		}
	}
	return restarter, nil
}

// waitReading is one poll of the new host while a restart waits for it.
type waitReading struct {
	ready  bool
	exited *ExitStatus
	detail string
}

// Restart stops the running host, waits for its process to exit, starts the
// new host and returns once it answers. A new host that exits is reported at
// once, with its exit status and its last log lines, not at the deadline.
func (restarter *HostRestarter) Restart(ctx context.Context) error {
	pid, running, err := restarter.control.Current()
	if err != nil {
		return err
	}
	if running {
		if err := restarter.stopHost(ctx, pid); err != nil {
			return err
		}
	} else {
		restarter.say("no host is running; starting one")
	}
	began := restarter.clock.Now()
	started, err := restarter.control.Start(ctx)
	if err != nil {
		return err
	}
	restarter.say("started pid %d, log %s", started.PID, started.Log)
	lastTick := began
	seen, err := eventually.Wait(fmt.Sprintf("pid %d to answer", started.PID), restarter.ready,
		func() waitReading {
			select {
			case status := <-started.Exited:
				return waitReading{exited: &status}
			default:
			}
			ready, detail := restarter.control.Ready(ctx, started.PID)
			if now := restarter.clock.Now(); !ready && now.Sub(lastTick) >= restarter.tick {
				lastTick = now
				restarter.say("waiting %s: %s", restarter.since(began), restarter.latestLine())
			}
			return waitReading{ready: ready, detail: detail}
		},
		func(reading waitReading) bool { return reading.ready || reading.exited != nil })
	if seen.exited != nil {
		restarter.say("pid %d exited with status %d after %s; its last log lines:", started.PID, seen.exited.Code, restarter.since(began))
		restarter.sayTail()
		return fmt.Errorf("%w: pid %d, status %d: %s", ErrHostExited, started.PID, seen.exited.Code, restarter.latestLine())
	}
	if err != nil {
		restarter.say("pid %d did not answer after %s; its last log lines:", started.PID, restarter.since(began))
		restarter.sayTail()
		return fmt.Errorf("%w: pid %d did not answer: %w", ErrHostStuck, started.PID, err)
	}
	restarter.say("pid %d answered after %s: %s", started.PID, restarter.since(began), seen.detail)
	return nil
}

// stopHost asks pid to stop and waits for its process to exit.
func (restarter *HostRestarter) stopHost(ctx context.Context, pid int) error {
	restarter.say("stopping pid %d", pid)
	began := restarter.clock.Now()
	if err := restarter.control.Stop(ctx, pid); err != nil {
		return fmt.Errorf("stop pid %d: %w", pid, err)
	}
	if _, err := eventually.Wait(fmt.Sprintf("pid %d to exit", pid), restarter.stop,
		func() bool { return restarter.control.Alive(pid) },
		func(alive bool) bool { return !alive }); err != nil {
		return fmt.Errorf("%w: pid %d did not exit: %w", ErrHostStuck, pid, err)
	}
	restarter.say("pid %d exited after %s", pid, restarter.since(began))
	return nil
}

func (restarter *HostRestarter) latestLine() string {
	if tail := restarter.control.LogTail(1); len(tail) > 0 {
		return tail[len(tail)-1]
	}
	return noLogYet
}

func (restarter *HostRestarter) sayTail() {
	for _, line := range restarter.control.LogTail(TailLines) {
		restarter.progress(tailPrefix + line)
	}
}

func (restarter *HostRestarter) since(began time.Time) time.Duration {
	return restarter.clock.Now().Sub(began).Round(elapsedRound)
}

func (restarter *HostRestarter) say(format string, arguments ...any) {
	restarter.progress(fmt.Sprintf(format, arguments...))
}
