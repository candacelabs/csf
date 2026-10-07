// Copyright 2026 Candace Labs

package claudecode

import (
	"encoding/json"
	"fmt"
	"time"
)

// The system event subtypes Claude Code writes for a task it runs: a Bash
// command or an agent, started in the foreground or the background.
const (
	subtypeTaskStarted      = "task_started"
	subtypeTaskNotification = "task_notification"
)

// TaskNotification is Claude Code's report that a task it started has ended,
// read from the task_notification event an open session writes between turns.
// Between turns that event is what wakes the session: Claude Code starts a turn
// of its own on it, with no input from the harness.
type TaskNotification struct {
	TaskID    string `json:"task_id"`
	ToolUseID string `json:"tool_use_id"`
	// Status is how the task ended, as Claude Code spells it: completed,
	// failed or stopped.
	Status  string `json:"status"`
	Summary string `json:"summary"`
	// OutputFile is where Claude Code wrote the task's output; empty when it
	// kept none.
	OutputFile string `json:"output_file"`
	// Elapsed is the time from the task's task_started event to this one, as
	// the open session saw them; zero when it did not see the start.
	Elapsed time.Duration `json:"-"`
}

// WithTaskNotifications hands notify every task notification an open session
// reads between turns, in order. notify runs on the session's stream owner,
// which reads every output line: it must return promptly and never block.
func WithTaskNotifications(notify func(notification TaskNotification)) ClaudeCodeBrainOption {
	return func(brain *ClaudeCodeBrain) error {
		if notify == nil {
			return fmt.Errorf("%w: nil task notification receiver", ErrInvalidOption)
		}
		brain.notifyTasks = notify
		return nil
	}
}

// task reads the task events of one output line: a start is remembered for
// the elapsed time, and an end read between turns is handed to the receiver.
func (state *streamState) task(header eventHeader, raw json.RawMessage) {
	if header.Type != EventTypeSystem || state.notify == nil {
		return
	}
	var notification TaskNotification
	switch header.Subtype {
	case subtypeTaskStarted:
		if json.Unmarshal(raw, &notification) == nil && notification.TaskID != "" {
			state.started[notification.TaskID] = state.now()
		}
	case subtypeTaskNotification:
		if json.Unmarshal(raw, &notification) != nil {
			return
		}
		if started, seen := state.started[notification.TaskID]; seen {
			notification.Elapsed = state.now().Sub(started)
			delete(state.started, notification.TaskID)
		}
		if state.reply == nil {
			state.notify(notification)
		}
	}
}
