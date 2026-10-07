// Copyright 2026 Candace Labs

package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/candacelabs/csf/pkg/atomicfile"
)

// The files of one run directory, <state directory>/<assignment id>.
const (
	// RunStateFile records the run for its gates and for a resumed turn.
	RunStateFile = "run.json"
	// SettingsFile is the Claude Code settings the session starts with.
	SettingsFile = "settings.json"
	// EventsFile is the run's event log, one JSON object per line.
	EventsFile = "events.jsonl"
	// WorktreeDirectory is the git worktree the session works in.
	WorktreeDirectory = "worktree"

	runDirectoryMode = 0o700
	runFileMode      = 0o600
)

// ErrNoRunState reports a run directory with no recorded run.
var ErrNoRunState = errors.New("harness session: no run is recorded in this directory")

// RunState is what the harness records about a run so its session gates and
// a later resumed turn act on the same session, trace and branch. Gates read
// it; only the runner writes it.
type RunState struct {
	AssignmentID     string `json:"assignment_id"`
	AgentID          string `json:"agent_id"`
	TicketURL        string `json:"ticket_url"`
	SessionID        string `json:"session_id"`
	TraceID          string `json:"trace_id"`
	SpanID           string `json:"span_id"`
	Repository       string `json:"repository"`
	Worktree         string `json:"worktree"`
	Branch           string `json:"branch"`
	BaseBranch       string `json:"base_branch"`
	PullRequestTitle string `json:"pull_request_title"`
	// WorkspaceMode is the workspace configuration: "normal" or "patch".
	WorkspaceMode string `json:"workspace_mode"`
	// Turns counts the turns started on the session, so a gate attributes its
	// decisions to the turn that is running.
	Turns int `json:"turns"`
	// LastPushTime records the Unix timestamp of the most recent push,
	// initialized when the session starts and updated by the commit gate.
	LastPushTime int64 `json:"last_push_time,omitempty"`
	// Model is the LLM model the session runs on, needed by the commit hook.
	Model string `json:"model,omitempty"`
	// Executor is the turn executor the session runs on. A run recorded
	// before executors were chosen has none and ran on Claude Code.
	Executor Executor `json:"executor,omitempty"`
	// BuildContainerID is the ID of the long-lived build container for this session.
	BuildContainerID string `json:"build_container_id,omitempty"`
}

// TurnExecutor is the turn executor the run is on: the recorded one, or
// Claude Code for a run recorded before executors were chosen.
func (state *RunState) TurnExecutor() Executor {
	if state.Executor == "" {
		return ExecutorClaudeCode
	}
	return state.Executor
}

// RunDirectory is the deterministic directory of an assignment's run.
func RunDirectory(stateDirectory string, assignmentID string) string {
	return filepath.Join(stateDirectory, assignmentID)
}

// ReadRunState reads the run recorded in directory.
func ReadRunState(directory string) (*RunState, error) {
	content, err := os.ReadFile(filepath.Join(directory, RunStateFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNoRunState, directory)
	}
	if err != nil {
		return nil, fmt.Errorf("harness session: read run state: %w", err)
	}
	state := &RunState{}
	if err := json.Unmarshal(content, state); err != nil {
		return nil, fmt.Errorf("harness session: decode run state: %w", err)
	}
	return state, nil
}

// WriteRunState replaces the run state atomically, so a gate never reads a
// half-written file. Gates and hooks use this to persist state changes like push times.
func WriteRunState(directory string, state *RunState) error {
	content, err := json.MarshalIndent(state, "", "  ")
	if err != nil {
		return fmt.Errorf("harness session: encode run state: %w", err)
	}
	return replaceFile(filepath.Join(directory, RunStateFile), content)
}

// replaceFile replaces target with content whole.
func replaceFile(target string, content []byte) error {
	if err := atomicfile.WriteFile(target, content, runFileMode); err != nil {
		return fmt.Errorf("harness session: replace %s: %w", filepath.Base(target), err)
	}
	return nil
}
