// Copyright 2026 Candace Labs

// Package sessiongate holds the session gates the agent harness installs into
// every session it runs: structural checks Claude Code calls as hooks around
// a tool call and at the end of a turn. The wait gate rejects a Bash command
// that would wait on nothing (a shell poll loop, pgrep -f, a foreground
// sleep) and names the replacement; the ready gate runs tools/check-merge.sh
// in the worktree before a command marks a pull request ready and denies it
// on any consistency regression; the commit gate pushes the work branch after
// a git commit and opens its draft pull request, on the repository it pushed
// to, if none exists; the reply gate reads the turn's reply back from the
// event log and refuses one that answers unvetted operator terms without a
// research check per term, commits the agent's future behaviour with no
// enforcing artifact in the turn, or waits on a background result while no
// background task runs, so the turn continues from the typed reason; the
// endpoint gate refuses a command that would stop serving an operator-facing
// endpoint of the endpoint registry without the operator's retirement
// record. The question gate judges every question put to the operator, the
// ones a reply ends on and the ones an AskUserQuestion call asks before they
// are shown: a question names its operator class (meaning, trust boundary,
// class membership) or is the agent's to resolve, and no question or offered
// alternative may name what a recorded ruling rules out. A reply that reports
// a number to an operator who asked for a measurement names the quantity the
// operator asked for. The search gate denies the third grep or find call in a
// row of a turn and names csf search, which asks the knowledge index. Every
// decision is written to the run's event log under the run's
// trace. Given a directory with no run record, the gate answers in operator
// mode, for a session the harness does not run, such as the orchestrator's:
// the endpoint gate alone, unlogged.
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

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/net/github"
	"github.com/candacelabs/csf/services/harness/endpoint"
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
	// ErrPullRequest reports a draft pull request the gate could not find or
	// open.
	ErrPullRequest = errors.New("session gate: could not open the draft pull request")
	// ErrNoGitHub reports a gate the binary granted no GitHub protocol client:
	// no token was found.
	ErrNoGitHub = errors.New("no GitHub token: set GITHUB_TOKEN or GH_TOKEN, or log gh in with its token in hosts.yml")
)

// The gates and their decisions, as the event log records them.
const (
	GateWait   = "wait"
	GateReady  = "ready"
	GateCommit = "commit"
	GateReply  = "reply"
	// GateQuestion is the PreToolUse gate on AskUserQuestion; the questions
	// a reply ends on are the reply gate's.
	GateQuestion = "question"
	// GateEndpoint is the PreToolUse gate on commands that stop serving a
	// registered endpoint.
	GateEndpoint = "endpoint"
	// GateSearch is the PreToolUse gate on a chain of grep and find calls.
	GateSearch = "search"

	DecisionAllow     = "allow"
	DecisionDeny      = "deny"
	DecisionSkip      = "skip"
	DecisionMalformed = "malformed"
	DecisionOpened    = "opened"
	DecisionExists    = "exists"
	DecisionFailed    = "failed"
	// DecisionLimit records a reply the reply gate would refuse but passes,
	// the turn having been refused its limit of times already.
	DecisionLimit = "limit"

	keyGate      = "gate"
	keyDecision  = "decision"
	keyHookEvent = "hook_event"
	keyToolUseID = "tool_use_id"
	keyCommand   = "command"
	keyRules     = "rules"
	keyReason    = "reason"
	keyURL       = "pull_request_url"

	// KeyQuestions is the verdicts on the questions a reply or an
	// AskUserQuestion call put to the operator, as the decision records them.
	KeyQuestions = "questions"

	permissionDeny = "deny"
)

// HookInput is the part of a command hook's standard input the gates read.
type HookInput struct {
	SessionID     string    `json:"session_id"`
	HookEventName string    `json:"hook_event_name"`
	ToolName      string    `json:"tool_name"`
	ToolUseID     string    `json:"tool_use_id"`
	ToolInput     ToolInput `json:"tool_input"`
}

// ToolInput is the part of a gated tool's input the gates read: Bash's
// command and whether it runs in the background, and the pattern and path of
// a Grep or Glob call.
type ToolInput struct {
	Command         string `json:"command"`
	RunInBackground bool   `json:"run_in_background"`
	Pattern         string `json:"pattern"`
	Path            string `json:"path"`
}

// HookOutput is a command hook's standard output. A tool gate answers in
// HookSpecificOutput; the Stop gate answers with Decision and Reason, which
// is how a Stop hook makes the turn continue.
type HookOutput struct {
	Decision           string              `json:"decision,omitempty"`
	Reason             string              `json:"reason,omitempty"`
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
	github    *github.GitHubClient
	directory string
	registry  endpoint.Registry
	citations CitationCheck
}

// CitationCheck is the proof check on a pull request's body: an improvement
// it claims must cite a recorded evaluation suite result (#416).
type CitationCheck func(ctx context.Context, body string) error

// WithCitationCheck grants the proof check the ready gate runs on the pull
// request's body. Without it no body is checked.
func WithCitationCheck(check CitationCheck) SessionGateOption {
	return func(gate *SessionGate) error {
		if check == nil {
			return fmt.Errorf("%w: nil citation check", ErrInvalidOption)
		}
		gate.citations = check
		return nil
	}
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

// WithGitHub grants the GitHub protocol client the commit gate opens draft
// pull requests through. Without it the commit gate pushes and reports
// [ErrNoGitHub].
func WithGitHub(client *github.GitHubClient) SessionGateOption {
	return func(gate *SessionGate) error {
		if client == nil {
			return fmt.Errorf("%w: nil GitHub client", ErrInvalidOption)
		}
		gate.github = client
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

// WithEndpointRegistry grants the endpoint registry the endpoint gate
// protects. Without it the gate protects nothing.
func WithEndpointRegistry(registry endpoint.Registry) SessionGateOption {
	return func(gate *SessionGate) error {
		gate.registry = registry
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
	if errors.Is(err, session.ErrNoRunState) {
		return gate.operatorMode(event, input)
	}
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
	call.input = input
	switch event {
	case session.HookPreToolUse:
		if hook.ToolName == session.ToolAskUserQuestion {
			return call.question(ctx), nil
		}
		if denied := call.mcp(ctx); denied != nil {
			return denied, nil
		}
		if denied := call.wait(ctx); denied != nil {
			return denied, nil
		}
		if denied := call.search(ctx); denied != nil {
			return denied, nil
		}
		if denied := call.endpoint(ctx); denied != nil {
			return denied, nil
		}
		if denied := call.github(ctx); denied != nil {
			return denied, nil
		}
		return call.ready(ctx), nil
	case session.HookPostToolUse:
		return call.commit(ctx)
	case session.HookStop:
		return call.reply(ctx), nil
	}
	unknown := fmt.Errorf("%w: %q", ErrUnknownEvent, event)
	call.failure(ctx, gateFor(event), DecisionFailed, unknown)
	return nil, unknown
}

// gateFor names the gate a failure on event is recorded under.
func gateFor(event string) string {
	switch event {
	case session.HookPostToolUse:
		return GateCommit
	case session.HookStop:
		return GateReply
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
	// input is the hook's standard input as it arrived.
	input []byte
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
	judgement := judgeWait(call.hook)
	switch {
	case judgement.output != nil:
		call.record(ctx, GateWait, judgement.decision, slog.Any(keyRules, judgement.rules), slog.String(keyReason, judgement.reason))
	case judgement.reason != "":
		call.record(ctx, GateWait, judgement.decision, slog.String(keyReason, judgement.reason))
	default:
		call.record(ctx, GateWait, judgement.decision)
	}
	return judgement.output
}

// waitJudgement is the wait gate's verdict on one tool call: the decision,
// why, the rules a denial broke and the output telling the agent.
type waitJudgement struct {
	decision string
	reason   string
	rules    []string
	output   *HookOutput
}

// judgeWait applies the wait gate's rules to one tool call.
func judgeWait(hook HookInput) waitJudgement {
	if hook.ToolName != session.ToolBash {
		return waitJudgement{decision: DecisionSkip}
	}
	findings, err := FindShellWaits(hook.ToolInput.Command)
	if err != nil {
		return waitJudgement{decision: DecisionAllow, reason: err.Error()}
	}
	if hook.ToolInput.RunInBackground {
		findings = slices.DeleteFunc(findings, isForegroundSleep)
	}
	if len(findings) == 0 {
		return waitJudgement{decision: DecisionAllow}
	}
	messages := make([]string, 0, len(findings))
	rules := make([]string, 0, len(findings))
	for _, finding := range findings {
		messages = append(messages, finding.Message())
		rules = append(rules, string(finding.Rule))
	}
	reason := sessionGateRejection + strings.Join(messages, "; ")
	return waitJudgement{decision: DecisionDeny, reason: reason, rules: rules, output: &HookOutput{HookSpecificOutput: &HookSpecificOutput{
		HookEventName:            session.HookPreToolUse,
		PermissionDecision:       permissionDeny,
		PermissionDecisionReason: reason,
	}}}
}

// isForegroundSleep reports a finding that does not apply to a command the
// turn does not wait for.
func isForegroundSleep(finding ShellFinding) bool { return finding.Rule == RuleForegroundSleep }

// endpoint is the PreToolUse gate on Bash that keeps every registered
// endpoint served: a command that would stop serving one without a
// retirement record is denied, naming each endpoint and its users, and the
// denial is logged. A command that stops nothing is not this gate's, and one
// the shell parser cannot read the wait gate has logged already.
func (call *gateCall) endpoint(ctx context.Context) *HookOutput {
	if call.hook.ToolName != session.ToolBash {
		return nil
	}
	denied, err := endpointDecision(call.hook.ToolInput.Command, call.gate.registry)
	if err != nil || denied == nil {
		return nil
	}
	call.record(ctx, GateEndpoint, DecisionDeny, slog.String(keyReason, denied.HookSpecificOutput.PermissionDecisionReason))
	return denied
}

// operatorMode answers a hook for a session the harness does not run: only
// the endpoint gate, on PreToolUse of Bash, with nothing logged, since there
// is no run to log under.
func (gate *SessionGate) operatorMode(event string, input []byte) (*HookOutput, error) {
	if event != session.HookPreToolUse {
		return nil, nil
	}
	hook := HookInput{}
	if err := json.Unmarshal(input, &hook); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedHookInput, err)
	}
	if hook.ToolName != session.ToolBash {
		return nil, nil
	}
	denied, err := endpointDecision(hook.ToolInput.Command, gate.registry)
	if err != nil {
		return nil, nil
	}
	return denied, nil
}

// endpointDecision is the endpoint gate's answer to one command: a denial
// naming every finding, or nil.
func endpointDecision(command string, registry endpoint.Registry) (*HookOutput, error) {
	findings, err := FindEndpointStops(command, registry)
	if err != nil || len(findings) == 0 {
		return nil, err
	}
	messages := make([]string, 0, len(findings))
	for _, finding := range findings {
		messages = append(messages, finding.Message())
	}
	return &HookOutput{HookSpecificOutput: &HookSpecificOutput{
		HookEventName:            session.HookPreToolUse,
		PermissionDecision:       permissionDeny,
		PermissionDecisionReason: "Rejected by the CSF endpoint gate. " + strings.Join(messages, "; "),
	}}, nil
}
