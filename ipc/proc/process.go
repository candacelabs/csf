// Copyright 2026 Candace Labs

package proc

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	"github.com/creack/pty"

	"github.com/candacelabs/csf/pkg/boundedbuffer"
)

// Process is one started child. Exactly one goroutine calls [Process.Wait];
// [Process.Kill], [Process.Signal] and [Process.Resize] may be called from any
// goroutine before or during that Wait.
type Process struct {
	ctx            context.Context
	command        *exec.Cmd
	executable     string
	stdout         io.ReadCloser
	terminal       *os.File
	capturedStdout *bytes.Buffer
	capturedStderr *boundedbuffer.Buffer
}

// Pid is the child's process identifier; for a terminal it is also the
// session and process-group identifier.
func (process *Process) Pid() int { return process.command.Process.Pid }

// Stdout is standard output when the command asked for StdoutPipe, else nil.
// Read it to the end before Wait.
func (process *Process) Stdout() io.Reader { return process.stdout }

// Terminal is the controlling side of a terminal command's pseudo-terminal,
// else nil. The process owns it: Wait closes it.
func (process *Process) Terminal() *os.File { return process.terminal }

// Resize changes a terminal command's window size.
func (process *Process) Resize(size TerminalSize) error {
	if process.terminal == nil {
		return ErrNoTerminal
	}
	return pty.Setsize(process.terminal, &pty.Winsize{Rows: size.Rows, Cols: size.Columns})
}

// Signal delivers signal to the child alone, the way an interrupt asks a
// program to run its own shutdown.
func (process *Process) Signal(signal os.Signal) error {
	return process.command.Process.Signal(signal)
}

// Kill ends the child and everything it started: its process group, and for a
// terminal every process in its session, including background jobs a shell
// moved into groups of their own. It does not wait; Wait reaps the child.
func (process *Process) Kill() error {
	if process.terminal != nil {
		return killSession(process.command.Process)
	}
	return killGroup(process.command.Process)
}

// Wait blocks until the child exits, reaps it and returns its result, with the
// same error semantics as [HostLauncher.Run].
func (process *Process) Wait() (Result, error) {
	waitErr := process.command.Wait()
	if process.terminal != nil {
		_ = process.terminal.Close()
	}
	result := Result{ExitCode: -1}
	if state := process.command.ProcessState; state != nil {
		result.ExitCode = state.ExitCode()
	}
	if process.capturedStdout != nil {
		result.Stdout = process.capturedStdout.Bytes()
	}
	if process.capturedStderr != nil {
		result.Stderr = append([]byte(nil), process.capturedStderr.Bytes()...)
		result.StderrTruncated = process.capturedStderr.Truncated()
	}
	if waitErr == nil {
		return result, nil
	}
	if err := process.ctx.Err(); err != nil {
		return result, fmt.Errorf("ipc/proc: %s: %w", process.executable, context.Cause(process.ctx))
	}
	var exitError *exec.ExitError
	if errors.As(waitErr, &exitError) {
		return result, &ExitError{Executable: process.executable, Code: result.ExitCode, Stderr: result.Stderr, cause: exitError}
	}
	return result, fmt.Errorf("ipc/proc: %s: %w", process.executable, waitErr)
}

// startTerminal starts the child in a new session whose controlling terminal
// is a fresh pseudo-terminal. The session leader is its own group leader, so
// cancellation kills the session the same way Kill does.
func startTerminal(process *Process, size TerminalSize) error {
	child := process.command
	child.Cancel = func() error { return killSession(child.Process) }
	terminal, err := pty.StartWithSize(child, &pty.Winsize{Rows: size.Rows, Cols: size.Columns})
	if err != nil {
		return err
	}
	process.terminal = terminal
	return nil
}
