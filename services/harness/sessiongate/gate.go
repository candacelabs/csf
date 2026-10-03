// Copyright 2026 Candace Labs

// Package sessiongate holds the session gates the agent harness installs into
// every session it runs: structural checks Claude Code calls as hooks around
// a tool call. The wait gate rejects a Bash command that would wait on
// nothing (a shell poll loop, pgrep -f, a foreground sleep) and names the
// replacement; the ready gate runs tools/check-merge.sh in the worktree
// before a command marks a pull request ready and denies it on any
// consistency regression; the commit gate pushes the work branch after a git commit and
// opens its draft pull request, on the repository it pushed to, if none
// exists. Every decision is written to the run's event log under the run's
// trace.
//
// A gate is a library. The binary Claude Code calls reads the hook's input,
// grants the process capability and writes the returned output.
package sessiongate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/services/harness/session"
)

var (
	// ErrInvalidOption reports a nil option or an option value the gate
	// cannot use.
	ErrInvalidOption = errors.New("session gate: invalid option")
	// ErrNoLauncher reports a gate built without the process capability.
	ErrNoLauncher = errors.New("session gate: a process launcher is required")
	// ErrNoRunDirectory reports a gate built without its run directory.
	ErrNoRunDirectory = errors.New("session gate: a run directory is required")
	// ErrMalformedHookInput reports hook input that is not the JSON object
	// Claude Code sends.
	ErrMalformedHookInput = errors.New("session gate: malformed hook input")
	// ErrUnknownEvent reports a hook event the harness installs no gate on.
	ErrUnknownEvent = errors.New("session gate: no gate for this hook event")
	// ErrPush reports a work branch git could not push.
	ErrPush = errors.New("session gate: could not push the work branch")
	// ErrPullRequest reports a draft pull request gh could not find or open.
	ErrPullRequest = errors.New("session gate: could not open the draft pull request")
)

// The gates and their decisions, as the event log records them.
const (
	GateWait   = "wait"
	GateReady  = "ready"
	GateCommit = "commit"

	DecisionAllow     = "allow"
	DecisionDeny      = "deny"
	DecisionSkip      = "skip"
	DecisionMalformed = "malformed"
	DecisionOpened    = "opened"
	DecisionExists    = "exists"
	DecisionFailed    = "failed"

	keyGate      = "gate"
	keyDecision  = "decision"
	keyHookEvent = "hook_event"
	keyToolUseID = "tool_use_id"
	keyCommand   = "command"
	keyRules     = "rules"
	keyReason    = "reason"
	keyURL       = "pull_request_url"

	permissionDeny = "deny"
)

// HookInput is the part of a command hook's standard input the gates read.
type HookInput struct {
	SessionID     string `json:"session_id"`
	HookEventName string `json:"hook_event_name"`
	ToolName      string `json:"tool_name"`
	ToolUseID     string `json:"tool_use_id"`
	ToolInput     struct {
		Command         string `json:"command"`
		RunInBackground bool   `json:"run_in_background"`
	} `json:"tool_input"`
}

// HookOutput is a command hook's standard output.
type HookOutput struct {
	HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

// HookSpecificOutput is the event-specific decision Claude Code reads.
type HookSpecificOutput struct {
	HookEventName            string `json:"hookEventName"`
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

// SessionGate answers the gated hooks of one run.
type SessionGate struct {
	launcher  proc.ILauncher
	directory string
}

// SessionGateOption configures a [SessionGate].
type SessionGateOption func(gate *SessionGate) error

// WithLauncher grants the process capability git and gh start through.
// Required.
func WithLauncher(launcher proc.ILauncher) SessionGateOption {
	return func(gate *SessionGate) error {
		if launcher == nil {
			return ErrNoLauncher
		}
		gate.launcher = launcher
		return nil
	}
}

// WithRunDirectory names the run the gate answers for: the directory the
// harness recorded it in. Required.
func WithRunDirectory(directory string) SessionGateOption {
	return func(gate *SessionGate) error {
		if directory == "" {
			return ErrNoRunDirectory
		}
		gate.directory = directory
		return nil
	}
}

// NewSessionGate validates the whole option set before building the gate.
func NewSessionGate(options ...SessionGateOption) (*SessionGate, error) {
	gate := &SessionGate{}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(gate); err != nil {
			return nil, err
		}
	}
	switch {
	case gate.launcher == nil:
		return nil, ErrNoLauncher
	case gate.directory == "":
		return nil, ErrNoRunDirectory
	}
	return gate, nil
}

// Handle answers one hook call: event is the hook event the harness
// installed the call on and input is the hook's standard input. A nil output
// means the call proceeds without a word from the gate. A gate that could
// not finish its work returns both the output telling the agent and the
// error.
func (gate *SessionGate) Handle(ctx context.Context, event string, input []byte) (*HookOutput, error) {
	state, err := session.ReadRunState(gate.directory)
	if err != nil {
		return nil, err
	}
	log, err := session.OpenEventLog(gate.directory, session.RunTrace(state))
	if err != nil {
		return nil, err
	}
	defer func() { _ = log.Close() }()
	ctx, err = log.Context(ctx)
	if err != nil {
		return nil, err
	}
	call := &gateCall{gate: gate, log: log, state: state, event: event}
	hook := HookInput{}
	if err := json.Unmarshal(input, &hook); err != nil {
		malformed := fmt.Errorf("%w: %w", ErrMalformedHookInput, err)
		call.failure(ctx, gateFor(event), DecisionMalformed, malformed)
		return nil, malformed
	}
	call.hook = hook
	switch event {
	case session.HookPreToolUse:
		if denied := call.wait(ctx); denied != nil {
			return denied, nil
		}
		return call.ready(ctx), nil
	case session.HookPostToolUse:
		return call.commit(ctx)
	}
	unknown := fmt.Errorf("%w: %q", ErrUnknownEvent, event)
	call.failure(ctx, gateFor(event), DecisionFailed, unknown)
	return nil, unknown
}

func gateFor(event string) string {
	if event == session.HookPostToolUse {
		return GateCommit
	}
	return GateWait
}

// gateCall is one hook call being answered.
type gateCall struct {
	gate  *SessionGate
	log   *session.EventLog
	state *session.RunState
	event string
	hook  HookInput
}

func (call *gateCall) attributes(gate string, decision string, extra ...slog.Attr) []slog.Attr {
	return append([]slog.Attr{
		slog.String(keyGate, gate),
		slog.String(keyDecision, decision),
		slog.String(keyHookEvent, call.event),
		slog.String(keyToolUseID, call.hook.ToolUseID),
		slog.String(keyCommand, call.hook.ToolInput.Command),
	}, extra...)
}

func (call *gateCall) record(ctx context.Context, gate string, decision string, extra ...slog.Attr) {
	call.log.Record(ctx, call.sessionID(), call.state.Turns, session.EventTypeGateDecision,
		"session gate decision", call.attributes(gate, decision, extra...)...)
}

func (call *gateCall) failure(ctx context.Context, gate string, decision string, err error) {
	call.log.Failure(ctx, call.sessionID(), call.state.Turns, session.EventTypeGateDecision,
		"session gate decision", err, call.attributes(gate, decision)...)
}

// sessionID is the session the hook reports, or the recorded one when the
// input carried none.
func (call *gateCall) sessionID() string {
	if call.hook.SessionID != "" {
		return call.hook.SessionID
	}
	return call.state.SessionID
}

// wait is the PreToolUse gate on Bash: a command that waits on nothing is
// denied with the replacement named. A command the shell parser cannot read
// is allowed and logged: Bash itself will refuse it.
func (call *gateCall) wait(ctx context.Context) *HookOutput {
	if call.hook.ToolName != session.ToolBash {
		call.record(ctx, GateWait, DecisionSkip)
		return nil
	}
	findings, err := FindShellWaits(call.hook.ToolInput.Command)
	if err != nil {
		call.record(ctx, GateWait, DecisionAllow, slog.String(keyReason, err.Error()))
		return nil
	}
	if call.hook.ToolInput.RunInBackground {
		findings = slices.DeleteFunc(findings, isForegroundSleep)
	}
	if len(findings) == 0 {
		call.record(ctx, GateWait, DecisionAllow)
		return nil
	}
	messages := make([]string, 0, len(findings))
	rules := make([]string, 0, len(findings))
	for _, finding := range findings {
		messages = append(messages, finding.Message())
		rules = append(rules, string(finding.Rule))
	}
	reason := "Rejected by the CSF session gate. " + strings.Join(messages, "; ")
	call.record(ctx, GateWait, DecisionDeny, slog.Any(keyRules, rules), slog.String(keyReason, reason))
	return &HookOutput{HookSpecificOutput: &HookSpecificOutput{
		HookEventName:            session.HookPreToolUse,
		PermissionDecision:       permissionDeny,
		PermissionDecisionReason: reason,
	}}
}

// isForegroundSleep reports a finding that does not apply to a command the
// turn does not wait for.
func isForegroundSleep(finding ShellFinding) bool { return finding.Rule == RuleForegroundSleep }
