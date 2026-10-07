// Copyright 2026 Candace Labs

// Package proc is the process boundary: the one subprocess gateway in CSF.
// Starting another program crosses into another address space through fork
// and exec, so it happens only through a capability granted by the binary that
// owns the process, and this is the only package in candace/ that imports
// os/exec.
//
// A binary constructs one [HostLauncher] and passes it, as an [ILauncher], to
// whatever needs to run a program; a service receives the capability in its
// constructor and never builds an exec.Cmd itself. The gateway owns the whole
// life of a child:
//
//   - launch: an argument vector, never a shell string, in an explicit
//     directory and environment;
//   - input and output: caller-supplied readers and writers, or captured
//     output returned as a structured [Result];
//   - cancellation: the context passed to [HostLauncher.Run] or
//     [HostLauncher.Start] bounds the child, and canceling it kills the
//     child's whole process group, so a shell's own children die with it;
//   - waiting and cleanup: [HostLauncher.Run] always reaps its child, and a
//     started [Process] is reaped by [Process.Wait], so no child is left a
//     zombie.
//
// The package starts no goroutines of its own. The copying goroutines
// os/exec runs for non-file writers, and the one that watches a context, are
// owned by the call: they start in Run or Start and are joined by the Wait
// that completes it.
package proc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"

	"github.com/candacelabs/csf/pkg/boundedbuffer"
	boundedbufferv1 "github.com/candacelabs/csf/pkg/boundedbuffer/v1"
	runtimeconfig "github.com/candacelabs/csf/runtime/config"
)

const (
	// DefaultWaitDelay bounds how long Wait keeps reading a canceled child's
	// output pipes after the kill: a grandchild that escaped the group must
	// not hold the caller forever.
	DefaultWaitDelay = 5 * time.Second
	// DefaultDiagnosticBytes bounds captured standard error.
	DefaultDiagnosticBytes = 64 << 10
)

var (
	// ErrTerminalRun is returned when a terminal is requested from Run: an
	// interactive terminal belongs to a started [Process].
	ErrTerminalRun = errors.New("ipc/proc: a terminal command must be started, not run")
	// ErrStreamConflict is returned for a command whose stream fields
	// contradict each other.
	ErrStreamConflict = errors.New("ipc/proc: conflicting standard stream configuration")
	// ErrExecutableRequired is returned for a command with no executable.
	ErrExecutableRequired = errors.New("ipc/proc: executable is required")
	// ErrInvalidOption is returned by NewHostLauncher for a nil or
	// out-of-range option.
	ErrInvalidOption = errors.New("ipc/proc: invalid launcher option")
	// ErrNoTerminal is returned by Resize on a process started without one.
	ErrNoTerminal = errors.New("ipc/proc: process has no terminal")
)

// ILauncher runs programs. It is the process-boundary capability.
type ILauncher interface {
	// Run starts command, waits for it and returns its structured result.
	Run(ctx context.Context, command Command) (Result, error)
	// Start launches command and returns the running process, which the
	// caller must Wait for.
	Start(ctx context.Context, command Command) (*Process, error)
	// LookPath resolves an executable name the way Run would.
	LookPath(name string) (string, error)
}

// Command is one program invocation.
type Command struct {
	// Executable is a path, or a name resolved on PATH.
	Executable string
	// Arguments follow the executable in the argument vector.
	Arguments []string
	// Directory is the working directory; empty means the caller's.
	Directory string
	// Environment replaces the inherited environment when non-nil.
	Environment []string
	// ExtraEnvironment is appended to the environment the child receives,
	// inherited or replaced, so a caller adds a variable without reading the
	// process environment itself.
	ExtraEnvironment []string
	// Stdin is the child's standard input; nil means none.
	Stdin io.Reader
	// Stdout receives standard output. When nil it is captured into
	// [Result.Stdout] (unbounded, like exec.Cmd.Output), unless StdoutPipe
	// is set on a started process.
	Stdout io.Writer
	// Stderr receives standard error. When nil it is captured, bounded by the
	// launcher's diagnostic limit, into [Result.Stderr] and [ExitError],
	// whose message then carries it. A caller whose child's diagnostics may
	// hold secrets passes io.Discard to keep them out of every error.
	Stderr io.Writer
	// StdoutPipe exposes standard output as [Process.Stdout] on a started
	// process. Read it to the end before Wait.
	StdoutPipe bool
	// Terminal attaches the child to a new pseudo-terminal of this size, in
	// its own session. Stdin, Stdout and Stderr must be nil: the terminal is
	// all three, read and written through [Process.Terminal].
	Terminal *TerminalSize
}

// TerminalSize is a pseudo-terminal window in character cells.
type TerminalSize struct {
	Rows    uint16
	Columns uint16
}

// Result is what a finished child left behind.
type Result struct {
	// ExitCode is the child's exit status, or -1 if it was killed by a
	// signal or never ran.
	ExitCode int
	// Stdout is captured standard output when Command.Stdout was nil.
	Stdout []byte
	// Stderr is captured standard error when Command.Stderr was nil.
	Stderr []byte
	// StderrTruncated reports that captured standard error exceeded the
	// diagnostic limit and only its prefix was kept.
	StderrTruncated bool
}

// ExitError reports a child that ran and exited unsuccessfully.
type ExitError struct {
	Executable string
	Code       int
	Stderr     []byte
	cause      *exec.ExitError
}

// Error names the program, its status and, when captured, its diagnostics.
func (exitError *ExitError) Error() string {
	message := fmt.Sprintf("ipc/proc: %s: %v", exitError.Executable, exitError.cause)
	if diagnostic := strings.TrimSpace(string(exitError.Stderr)); diagnostic != "" {
		message += ": " + diagnostic
	}
	return message
}

// Unwrap exposes the operating system's report.
func (exitError *ExitError) Unwrap() error { return exitError.cause }

// HostLauncher is this host's process table, granted as a capability.
type HostLauncher struct {
	waitDelay       time.Duration
	diagnosticBytes int64
}

// HostLauncherOption configures a [HostLauncher].
type HostLauncherOption func(launcher *HostLauncher) error

// WithWaitDelay bounds how long a canceled child's pipes are drained.
func WithWaitDelay(delay time.Duration) HostLauncherOption {
	return func(launcher *HostLauncher) error {
		if delay <= 0 {
			return fmt.Errorf("%w: wait delay must be positive, got %s", ErrInvalidOption, delay)
		}
		launcher.waitDelay = delay
		return nil
	}
}

// WithDiagnosticBytes bounds captured standard error.
func WithDiagnosticBytes(limit int64) HostLauncherOption {
	return func(launcher *HostLauncher) error {
		if limit <= 0 {
			return fmt.Errorf("%w: diagnostic limit must be positive, got %d", ErrInvalidOption, limit)
		}
		launcher.diagnosticBytes = limit
		return nil
	}
}

// NewHostLauncher returns the host's process capability.
func NewHostLauncher(options ...HostLauncherOption) (*HostLauncher, error) {
	launcher := &HostLauncher{waitDelay: DefaultWaitDelay, diagnosticBytes: DefaultDiagnosticBytes}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(launcher); err != nil {
			return nil, err
		}
	}
	return launcher, nil
}

var _ ILauncher = (*HostLauncher)(nil)

// LookPath resolves name on PATH.
func (launcher *HostLauncher) LookPath(name string) (string, error) {
	resolved, err := exec.LookPath(name)
	if err != nil {
		return "", fmt.Errorf("ipc/proc: %w", err)
	}
	return resolved, nil
}

// Run starts command, waits for it, and returns its result. A child that
// exits unsuccessfully returns an [*ExitError]; a canceled context kills the
// child's process group and returns the context's cause. The result carries
// whatever output was captured in every case.
func (launcher *HostLauncher) Run(ctx context.Context, command Command) (Result, error) {
	if command.Terminal != nil {
		return Result{ExitCode: -1}, ErrTerminalRun
	}
	if command.StdoutPipe {
		return Result{ExitCode: -1}, fmt.Errorf("%w: a piped standard output belongs to a started process", ErrStreamConflict)
	}
	process, err := launcher.Start(ctx, command)
	if err != nil {
		return Result{ExitCode: -1}, err
	}
	return process.Wait()
}

// Start launches command. The returned process must be waited for; the
// context bounds it exactly as it bounds Run, so a child meant to outlive a
// request is started with a context that does not end with the request.
func (launcher *HostLauncher) Start(ctx context.Context, command Command) (*Process, error) {
	if command.Executable == "" {
		return nil, ErrExecutableRequired
	}
	child := exec.CommandContext(ctx, command.Executable, command.Arguments...)
	child.Dir = command.Directory
	child.Env = environment(command)
	child.WaitDelay = launcher.waitDelay
	process := &Process{ctx: ctx, command: child, executable: command.Executable}
	if command.Terminal != nil {
		if command.Stdin != nil || command.Stdout != nil || command.Stderr != nil || command.StdoutPipe {
			return nil, fmt.Errorf("%w: a terminal owns all three streams", ErrStreamConflict)
		}
		if err := startTerminal(process, *command.Terminal); err != nil {
			return nil, fmt.Errorf("ipc/proc: start %s on a terminal: %w", command.Executable, err)
		}
		return process, nil
	}
	if err := launcher.attachStreams(process, command); err != nil {
		return nil, err
	}
	isolateGroup(child)
	child.Cancel = func() error { return killGroup(child.Process) }
	if err := child.Start(); err != nil {
		return nil, fmt.Errorf("ipc/proc: start %s: %w", command.Executable, err)
	}
	return process, nil
}

func (launcher *HostLauncher) attachStreams(process *Process, command Command) error {
	child := process.command
	child.Stdin = command.Stdin
	child.Stdout = command.Stdout
	child.Stderr = command.Stderr
	switch {
	case command.StdoutPipe:
		if command.Stdout != nil {
			return fmt.Errorf("%w: Stdout and StdoutPipe are exclusive", ErrStreamConflict)
		}
		pipe, err := child.StdoutPipe()
		if err != nil {
			return fmt.Errorf("ipc/proc: stdout pipe for %s: %w", command.Executable, err)
		}
		process.stdout = pipe
	case command.Stdout == nil:
		process.capturedStdout = &bytes.Buffer{}
		child.Stdout = process.capturedStdout
	}
	if command.Stderr == nil {
		diagnostics, err := boundedbuffer.New(&boundedbufferv1.Retention{MaxBytes: launcher.diagnosticBytes})
		if err != nil {
			return fmt.Errorf("ipc/proc: diagnostic buffer: %w", err)
		}
		process.capturedStderr = diagnostics
		child.Stderr = diagnostics
	}
	return nil
}

// environment is the child's environment. Inheriting is the default; to extend
// it the gateway takes the inherited environment from the config capability,
// the only reader of the process environment (CS-17).
func environment(command Command) []string {
	if command.Environment == nil && command.ExtraEnvironment == nil {
		return nil
	}
	base := command.Environment
	if base == nil {
		base = runtimeconfig.InheritedEnvironment()
	}
	// Never nil here: exec reads a nil environment as "inherit".
	child := make([]string, 0, len(base)+len(command.ExtraEnvironment))
	return append(append(child, base...), command.ExtraEnvironment...)
}
