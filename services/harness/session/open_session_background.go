// Copyright 2026 Candace Labs

package session

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"regexp"
	"strconv"
	"strings"

	"github.com/candacelabs/csf/io/net/model/claudecode"
)

// EventTypeBackgroundResult records a turn the turn executor started on its
// own, between turns, because a task it ran in the background ended: the
// harness counts it as the session's next turn, and the record carries what
// woke it.
const EventTypeBackgroundResult = "harness_background_result"

// The fields of a background result record.
const (
	KeyTaskID        = "task_id"
	KeyToolUseID     = "tool_use_id"
	KeyTaskStatus    = "status"
	KeyTaskSummary   = "summary"
	KeyTaskElapsed   = "task_elapsed_ms"
	KeyTaskExitCode  = "exit_code"
	KeyTaskOutputEnd = "output_tail"

	// OutputTailBytes bounds the output a background result carries: the end
	// of the task's output, where a command's verdict and exit status are. It
	// is a size bound for the record, not a measured threshold.
	OutputTailBytes = 2048
	// backgroundBuffer is how many notifications wait for the goroutine
	// driving the session. The executor reads them only between turns, when
	// that goroutine is waiting for them, so the buffer covers the moment a
	// turn starts; a notification past it is dropped rather than stall the
	// executor's output, and its turn then reads as an executor event only.
	backgroundBuffer = 64
)

// exitCodes are where Claude Code names a Bash task's exit code: the line it
// ends the task's output with, or, from 2.1.286, the notification's summary.
var exitCodes = []struct {
	pattern *regexp.Regexp
	from    func(notification claudecode.TaskNotification, tail string) string
}{
	{regexp.MustCompile(`\[exited with code (-?\d+)\]\s*$`), func(_ claudecode.TaskNotification, tail string) string { return tail }},
	{regexp.MustCompile(`\(exit code (-?\d+)\)`), func(notification claudecode.TaskNotification, _ string) string { return notification.Summary }},
}

// exitCode is the task's exit code, when its output or summary names one.
func exitCode(notification claudecode.TaskNotification, tail string) (int, bool) {
	for _, source := range exitCodes {
		if match := source.pattern.FindStringSubmatch(source.from(notification, tail)); match != nil {
			if code, err := strconv.Atoi(match[1]); err == nil {
				return code, true
			}
		}
	}
	return 0, false
}

// deliverBackground hands one notification to the goroutine driving the
// session. It runs on the executor's stream owner and never blocks.
func (open *OpenSession) deliverBackground(notification claudecode.TaskNotification) {
	select {
	case open.background <- notification:
	default:
	}
}

// BackgroundResults delivers the task notifications the turn executor read
// between turns, each of which started a turn of the executor's own. The
// goroutine driving the session records each with [OpenSession.BackgroundResult].
func (open *OpenSession) BackgroundResults() <-chan claudecode.TaskNotification {
	return open.background
}

// BackgroundResult counts the turn the executor started on notification as
// the session's next turn and records it as a typed background result: the
// task, how it ended, its exit code when its output names one, how long it
// ran and the bounded tail of its output. Nothing is sent to the executor; the
// turn is already running.
func (open *OpenSession) BackgroundResult(ctx context.Context, notification claudecode.TaskNotification) error {
	if open.closed {
		return ErrSessionClosed
	}
	traced, err := open.log.Context(ctx)
	if err != nil {
		return err
	}
	open.state.Turns++
	if err := WriteRunState(open.Directory(), open.state); err != nil {
		return err
	}
	attributes := []slog.Attr{
		slog.String(KeyTaskID, notification.TaskID),
		slog.String(KeyToolUseID, notification.ToolUseID),
		slog.String(KeyTaskStatus, notification.Status),
		slog.String(KeyTaskSummary, notification.Summary),
		slog.Float64(KeyTaskElapsed, float64(notification.Elapsed.Microseconds())/1000),
	}
	tail, err := outputTail(notification.OutputFile)
	if err != nil {
		attributes = append(attributes, slog.String(KeyError, err.Error()))
	}
	if code, named := exitCode(notification, tail); named {
		attributes = append(attributes, slog.Int(KeyTaskExitCode, code))
	}
	attributes = append(attributes, slog.String(KeyTaskOutputEnd, tail))
	open.log.Record(traced, open.state.SessionID, open.state.Turns, EventTypeBackgroundResult, "background result", attributes...)
	return nil
}

// outputTail is the last OutputTailBytes of the file at path, cut to whole
// UTF-8 characters. No path is no output; the session's private tmp, where
// the executor writes it, is the same path inside and outside a container.
func outputTail(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return "", err
	}
	if _, err := file.Seek(max(0, info.Size()-OutputTailBytes), io.SeekStart); err != nil {
		return "", err
	}
	content, err := io.ReadAll(io.LimitReader(file, OutputTailBytes))
	if err != nil {
		return "", err
	}
	return strings.ToValidUTF8(string(content), ""), nil
}
