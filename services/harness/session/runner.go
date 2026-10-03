// Copyright 2026 Candace Labs

// Package session is the agent harness's session runner: it runs an agent's
// session for an assignment on a turn executor, in a git worktree of its own,
// with the session gates installed and every event logged under the run's
// trace.
//
// The runner is a library. It owns no process: the binary that mounts it
// grants the process capability every git, gh and Claude Code child starts
// through, names the state directory and supplies the command Claude Code
// calls for each gate.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/ipc/model"
	"github.com/candacelabs/csf/ipc/model/claudecode"
	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/pkg/telemetry"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mocks/mock_turn_executor.go -package=mocks github.com/candacelabs/csf/services/harness/session ITurnExecutor,IOpenTurnExecutor,IRouter

var (
	// ErrInvalidOption reports a nil option or an option value the runner
	// cannot use.
	ErrInvalidOption = errors.New("harness session: invalid option")
	// ErrNoLauncher reports a runner built without the process capability.
	ErrNoLauncher = errors.New("harness session: a process launcher is required")
	// ErrNoStateDirectory reports a runner built without a state directory.
	ErrNoStateDirectory = errors.New("harness session: an absolute state directory is required")
	// ErrNoGateCommand reports a runner built without the gate command.
	ErrNoGateCommand = errors.New("harness session: a gate command is required")
	// ErrInvalidRecipe reports a recipe the harness cannot run.
	ErrInvalidRecipe = errors.New("harness session: invalid recipe")
	// ErrSessionExists reports a run already started for the assignment; a
	// follow-up turn is a Resume.
	ErrSessionExists = errors.New("harness session: the assignment already has a session; resume it")
	// ErrWorktreeCollision reports a worktree path already occupied by
	// something the harness did not record.
	ErrWorktreeCollision = errors.New("harness session: the worktree path is already occupied")
	// ErrWorktree reports a worktree git could not create.
	ErrWorktree = errors.New("harness session: could not create the worktree")
	// ErrRunMismatch reports a recorded run that belongs to another recipe.
	ErrRunMismatch = errors.New("harness session: the recorded run belongs to a different recipe")
	// ErrNoPrompt reports a resumed turn with nothing to say.
	ErrNoPrompt = errors.New("harness session: a resumed turn needs a prompt")
	// ErrTurnFailed reports a turn the turn executor did not complete.
	ErrTurnFailed = errors.New("harness session: the turn failed")
	// ErrPushRemote reports a worktree whose push remote git could not name.
	ErrPushRemote = errors.New("harness session: could not read the push remote's URL")
)

// PushRemote is the remote the harness pushes a work branch to and opens its
// pull request on.
const PushRemote = "origin"

// Executables and arguments of the children the runner starts.
const (
	gitExecutable = "git"
	ghExecutable  = "gh"
	gitDirectory  = "-C"
	gitWorktree   = "worktree"
	gitAdd        = "add"
	gitNewBranch  = "-b"
	gitRemote     = "remote"
	gitGetURL     = "get-url"
	ghPullRequest = "pr"
	ghList        = "list"
	ghRepo        = "--repo"
	ghHead        = "--head"
	ghState       = "--state"
	ghStateAll    = "all"
	ghJSON        = "--json"
	ghURLField    = "url"
	ghJQ          = "--jq"
	ghFirstURL    = ".[0].url"

	gitConfig           = "config"
	gitSetCoreHooksPath = "core.hooksPath"
	gitCommit           = "commit"
	gitAllowEmptyCommit = "--allow-empty"

	flagSettings           = "--settings"
	flagModel              = "--model"
	flagPermissionMode     = "--permission-mode"
	flagAllowedTools       = "--allowedTools"
	flagAppendSystemPrompt = "--append-system-prompt"
	// permissionDontAsk denies, without asking, every tool call the allow
	// list and the gates do not approve: nobody answers a prompt in print
	// mode.
	permissionDontAsk = "dontAsk"
	toolSeparator     = ","

	messageTypeUser = "user"
	roleUser        = "user"

	hookScript = `#!/bin/bash
set -uo pipefail

# prepare-commit-msg hook: appends CSF-Session, CSF-Turn, and CSF-Model trailers
# to every commit in the worktree.

COMMIT_MSG_FILE=$1
# Git runs a hook from the root of the working tree, which is the run
# directory's worktree, so run.json sits beside it.
RUN_STATE_FILE="$PWD/../run.json"

if [[ ! -f "$RUN_STATE_FILE" ]]; then
	exit 0
fi

# Extract values from run.json using grep and sed. Pattern allows optional
# whitespace after the colon, as produced by json.MarshalIndent.
ASSIGNMENT_ID=$(grep -o '"assignment_id": "[^"]*"' "$RUN_STATE_FILE" | sed 's/"assignment_id": "\([^"]*\)"/\1/')
MODEL=$(grep -o '"model": "[^"]*"' "$RUN_STATE_FILE" | sed 's/"model": "\([^"]*\)"/\1/')
TURNS=$(grep -o '"turns": [0-9]*' "$RUN_STATE_FILE" | sed 's/"turns": \([0-9]*\)/\1/')

if [[ -z "$ASSIGNMENT_ID" ]] || [[ -z "$MODEL" ]] || [[ -z "$TURNS" ]]; then
	exit 0
fi

# Append trailers if they don't already exist.
if ! grep -q "^CSF-Session:" "$COMMIT_MSG_FILE"; then
	echo "" >> "$COMMIT_MSG_FILE"
	echo "CSF-Session: $ASSIGNMENT_ID" >> "$COMMIT_MSG_FILE"
fi
if ! grep -q "^CSF-Turn:" "$COMMIT_MSG_FILE"; then
	echo "CSF-Turn: $TURNS" >> "$COMMIT_MSG_FILE"
fi
if ! grep -q "^CSF-Model:" "$COMMIT_MSG_FILE"; then
	echo "CSF-Model: $MODEL" >> "$COMMIT_MSG_FILE"
fi

exit 0
`
)

// ITurnExecutor carries out one turn of a session: the Claude Code turn
// executor in production, a double in specs.
type ITurnExecutor interface {
	Propose(ctx context.Context, turn *claudecode.Turn) (*model.Proposal[claudecode.Event], error)
}

// TurnExecutorSpec is what the runner asks of a turn executor.
type TurnExecutorSpec struct {
	Session   uuid.UUID
	Directory string
	// Arguments are the turn executor's extra arguments: the settings with
	// the gates, the model, the permission mode and the allowed tools.
	Arguments []string
	Logger    *slog.Logger
	// Resume is true when the session already exists.
	Resume bool
	// Environment is appended, as NAME=value, to the environment the turn
	// executor's process inherits: the Bazel cache locations, for one.
	Environment []string
}

// TurnExecutorFactory builds the turn executor for one turn.
type TurnExecutorFactory func(spec TurnExecutorSpec) (ITurnExecutor, error)

// ClaudeCodeTurnExecutors builds Claude Code turn executors that start
// executable through launcher.
func ClaudeCodeTurnExecutors(launcher proc.ILauncher, executable string) TurnExecutorFactory {
	return func(spec TurnExecutorSpec) (ITurnExecutor, error) {
		options := []claudecode.ClaudeCodeBrainOption{
			claudecode.WithExecutable(executable),
			claudecode.WithDirectory(spec.Directory),
			claudecode.WithArguments(spec.Arguments...),
			claudecode.WithLogger(spec.Logger),
			claudecode.WithEnvironment(spec.Environment...),
		}
		if spec.Resume {
			options = append(options, claudecode.WithResumedSession())
		}
		return claudecode.NewClaudeCodeBrain(launcher, spec.Session, options...)
	}
}

// AgentSessionRunner runs agent sessions for prepared assignments.
type AgentSessionRunner struct {
	launcher       proc.ILauncher
	stateDirectory string
	gateCommand    []string
	executors      TurnExecutorFactory
	openExecutors  OpenTurnExecutorFactory
	claude         string
	router         IRouter
}

// AgentSessionRunnerOption configures an [AgentSessionRunner].
type AgentSessionRunnerOption func(runner *AgentSessionRunner) error

// WithLauncher grants the process capability git, gh and the turn executor
// start through. Required.
func WithLauncher(launcher proc.ILauncher) AgentSessionRunnerOption {
	return func(runner *AgentSessionRunner) error {
		if launcher == nil {
			return ErrNoLauncher
		}
		runner.launcher = launcher
		return nil
	}
}

// WithStateDirectory keeps each run under directory/<assignment id>.
// Required; the directory must be absolute.
func WithStateDirectory(directory string) AgentSessionRunnerOption {
	return func(runner *AgentSessionRunner) error {
		if !filepath.IsAbs(directory) {
			return fmt.Errorf("%w: %q", ErrNoStateDirectory, directory)
		}
		runner.stateDirectory = filepath.Clean(directory)
		return nil
	}
}

// WithGateCommand is the command Claude Code calls for every gated hook,
// with the hook event and the run directory appended. Required.
func WithGateCommand(arguments ...string) AgentSessionRunnerOption {
	return func(runner *AgentSessionRunner) error {
		if len(arguments) == 0 || arguments[0] == "" {
			return ErrNoGateCommand
		}
		runner.gateCommand = append([]string{}, arguments...)
		return nil
	}
}

// WithTurnExecutors replaces the Claude Code turn executor.
func WithTurnExecutors(factory TurnExecutorFactory) AgentSessionRunnerOption {
	return func(runner *AgentSessionRunner) error {
		if factory == nil {
			return fmt.Errorf("%w: nil turn executor factory", ErrInvalidOption)
		}
		runner.executors = factory
		return nil
	}
}

// WithClaudeExecutable runs Claude Code from executable instead of the name
// claude on PATH.
func WithClaudeExecutable(executable string) AgentSessionRunnerOption {
	return func(runner *AgentSessionRunner) error {
		if executable == "" {
			return fmt.Errorf("%w: empty Claude Code executable", ErrInvalidOption)
		}
		runner.claude = executable
		return nil
	}
}

// NewAgentSessionRunner validates the whole option set before building the
// runner.
func NewAgentSessionRunner(options ...AgentSessionRunnerOption) (*AgentSessionRunner, error) {
	runner := &AgentSessionRunner{claude: claudecode.DefaultExecutable}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(runner); err != nil {
			return nil, err
		}
	}
	switch {
	case runner.launcher == nil:
		return nil, ErrNoLauncher
	case runner.stateDirectory == "":
		return nil, ErrNoStateDirectory
	case len(runner.gateCommand) == 0:
		return nil, ErrNoGateCommand
	}
	if runner.executors == nil {
		runner.executors = ClaudeCodeTurnExecutors(runner.launcher, runner.claude)
	}
	if runner.openExecutors == nil {
		runner.openExecutors = ClaudeCodeOpenTurnExecutors(runner.launcher, runner.claude)
	}
	return runner, nil
}

// StateDirectory is the directory every run lives under, as <assignment id>/.
func (runner *AgentSessionRunner) StateDirectory() string { return runner.stateDirectory }

// Run starts the assignment's session: it prepares the recipe, creates the
// worktree, installs the gates and runs the first turn on the task. The
// receipt is returned whenever the session was started, also when the turn
// failed, so its links are never lost with the error.
func (runner *AgentSessionRunner) Run(ctx context.Context, recipe *pb.AgentAssignmentRecipe) (*pb.AgentAssignmentReceipt, error) {
	ctx, plan, state, log, err := runner.openRun(ctx, recipe)
	if err != nil {
		return nil, err
	}
	defer func() { _ = log.Close() }()
	return runner.turn(ctx, log, plan, state, firstPrompt(plan, state), false)
}

// openRun prepares a new run: it freezes the recipe, refuses an assignment
// that already has a run, creates the run directory, the trace, the event log
// and the worktree, and returns ctx carrying the run's trace. The caller owns
// the returned log.
func (runner *AgentSessionRunner) openRun(ctx context.Context, recipe *pb.AgentAssignmentRecipe) (context.Context, *pb.AgentAssignmentPlan, *RunState, *EventLog, error) {
	plan, err := prepare(recipe)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	directory := RunDirectory(runner.stateDirectory, plan.GetRecipe().GetAssignmentId())
	_, err = ReadRunState(directory)
	if err == nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: %s", ErrSessionExists, directory)
	}
	if !errors.Is(err, ErrNoRunState) {
		return nil, nil, nil, nil, err
	}
	worktree := filepath.Join(directory, WorktreeDirectory)
	if _, err := os.Lstat(worktree); err == nil {
		return nil, nil, nil, nil, fmt.Errorf("%w: %s", ErrWorktreeCollision, worktree)
	}
	if err := os.MkdirAll(directory, runDirectoryMode); err != nil {
		return nil, nil, nil, nil, fmt.Errorf("harness session: create run directory: %w", err)
	}
	trace, err := telemetry.NewTraceContext(telemetry.TraceFlagsSampled)
	if err != nil {
		return nil, nil, nil, nil, fmt.Errorf("harness session: run trace: %w", err)
	}
	state := newRunState(plan, trace.GetTraceId(), trace.GetSpanId(), worktree)
	log, err := OpenEventLog(directory, trace)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	ctx, err = log.Context(ctx)
	if err != nil {
		_ = log.Close()
		return nil, nil, nil, nil, err
	}
	log.Record(ctx, state.SessionID, 0, EventTypeRunStarted, "harness run started",
		slog.String(keyAssignmentID, state.AssignmentID), slog.String(keyAgentID, state.AgentID),
		slog.String(keyRecipeSHA256, plan.GetRecipeSha256()), slog.String(keyWorktree, worktree), slog.String(keyBranch, state.Branch))
	if err := runner.createWorktree(ctx, state); err != nil {
		log.Failure(ctx, state.SessionID, 0, EventTypeWorktreeReady, "worktree not created", err)
		_ = log.Close()
		return nil, nil, nil, nil, err
	}
	log.Record(ctx, state.SessionID, 0, EventTypeWorktreeReady, "worktree created", slog.String(keyWorktree, worktree))
	return ctx, plan, state, log, nil
}

// Resume runs a follow-up turn on the assignment's existing session, in the
// same worktree and under the same trace.
func (runner *AgentSessionRunner) Resume(ctx context.Context, recipe *pb.AgentAssignmentRecipe, prompt string) (*pb.AgentAssignmentReceipt, error) {
	if strings.TrimSpace(prompt) == "" {
		return nil, ErrNoPrompt
	}
	plan, err := prepare(recipe)
	if err != nil {
		return nil, err
	}
	directory := RunDirectory(runner.stateDirectory, plan.GetRecipe().GetAssignmentId())
	state, err := ReadRunState(directory)
	if err != nil {
		return nil, err
	}
	if state.SessionID != plan.GetSessionKey() || state.Branch != plan.GetRecipe().GetWorkspace().GetBranch() {
		return nil, fmt.Errorf("%w: %s", ErrRunMismatch, directory)
	}
	log, err := OpenEventLog(directory, RunTrace(state))
	if err != nil {
		return nil, err
	}
	defer func() { _ = log.Close() }()
	ctx, err = log.Context(ctx)
	if err != nil {
		return nil, err
	}
	return runner.turn(ctx, log, plan, state, prompt, true)
}

// turn runs one turn and returns the receipt.
func (runner *AgentSessionRunner) turn(ctx context.Context, log *EventLog, plan *pb.AgentAssignmentPlan, state *RunState, prompt string, resume bool) (*pb.AgentAssignmentReceipt, error) {
	directory := RunDirectory(runner.stateDirectory, state.AssignmentID)
	settings, err := writeSettings(directory, runner.gateCommand)
	if err != nil {
		return nil, err
	}
	state.Turns++
	if err := WriteRunState(directory, state); err != nil {
		return nil, err
	}
	session := uuid.MustParse(state.SessionID)
	executor, err := runner.executors(TurnExecutorSpec{
		Session:   session,
		Directory: state.Worktree,
		Arguments: turnArguments(plan, settings),
		Logger:    log.Logger(),
		Resume:    resume,
	})
	if err != nil {
		return nil, fmt.Errorf("harness session: turn executor: %w", err)
	}
	return runner.executeTurn(ctx, log, plan, state, executor, prompt, resume)
}

// executeTurn runs prompt as one turn on executor, records it and returns the
// receipt. The caller has already counted the turn in state and written it.
func (runner *AgentSessionRunner) executeTurn(ctx context.Context, log *EventLog, plan *pb.AgentAssignmentPlan, state *RunState, executor ITurnExecutor, prompt string, resume bool) (*pb.AgentAssignmentReceipt, error) {
	message, err := userMessage(prompt)
	if err != nil {
		return nil, err
	}
	log.Record(ctx, state.SessionID, state.Turns, EventTypeTurnRequested, "turn requested", slog.Bool(keyResume, resume))
	_, turnErr := executor.Propose(ctx, &claudecode.Turn{Messages: []json.RawMessage{message}})
	receipt := &pb.AgentAssignmentReceipt{
		Plan:           plan,
		SessionId:      state.SessionID,
		TurnId:         strconv.Itoa(state.Turns),
		WorktreeId:     state.Worktree,
		Branch:         state.Branch,
		PullRequestUrl: runner.pullRequestURL(ctx, log, state),
		TraceId:        state.TraceID,
	}
	if turnErr != nil {
		log.Failure(ctx, state.SessionID, state.Turns, EventTypeRunFinished, "turn failed", turnErr)
		return receipt, fmt.Errorf("%w: %w", ErrTurnFailed, turnErr)
	}
	log.Record(ctx, state.SessionID, state.Turns, EventTypeRunFinished, "turn finished",
		slog.String(keyPullRequestURL, receipt.GetPullRequestUrl()))
	return receipt, nil
}

// prepare freezes the recipe and checks the workspace the harness needs.
func prepare(recipe *pb.AgentAssignmentRecipe) (*pb.AgentAssignmentPlan, error) {
	if recipe.GetWorkspace() == nil {
		return nil, fmt.Errorf("%w: the recipe has no workspace", ErrInvalidRecipe)
	}
	plan, err := csf.PrepareAgentAssignment(recipe)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidRecipe, err)
	}
	workspace := plan.GetRecipe().GetWorkspace()
	if len(workspace.GetAllowedTools()) == 0 {
		return nil, fmt.Errorf("%w: workspace.allowed_tools is empty", ErrInvalidRecipe)
	}
	if index := slices.IndexFunc(workspace.GetAllowedTools(), isInvalidTool); index >= 0 {
		return nil, fmt.Errorf("%w: workspace.allowed_tools entry %q", ErrInvalidRecipe, workspace.GetAllowedTools()[index])
	}
	if workspace.GetBranch() == workspace.GetBaseBranch() {
		return nil, fmt.Errorf("%w: workspace.branch must differ from base_branch", ErrInvalidRecipe)
	}
	return plan, nil
}

// isInvalidTool reports an allowed-tools entry Claude Code cannot read as one
// rule: blank, or several joined by the list separator.
func isInvalidTool(tool string) bool {
	return strings.TrimSpace(tool) == "" || strings.Contains(tool, toolSeparator)
}

func newRunState(plan *pb.AgentAssignmentPlan, traceID string, spanID string, worktree string) *RunState {
	recipe := plan.GetRecipe()
	workspace := recipe.GetWorkspace()
	return &RunState{
		AssignmentID:     recipe.GetAssignmentId(),
		AgentID:          recipe.GetAgent().GetId(),
		TicketURL:        recipe.GetTicketUrl(),
		SessionID:        plan.GetSessionKey(),
		TraceID:          traceID,
		SpanID:           spanID,
		Repository:       workspace.GetRepositoryPath(),
		Worktree:         worktree,
		Branch:           workspace.GetBranch(),
		BaseBranch:       workspace.GetBaseBranch(),
		PullRequestTitle: workspace.GetPullRequestTitle(),
		Model:            recipe.GetModel(),
		LastPushTime:     0,
	}
}

// createWorktree checks the work branch out from the base into the run's
// worktree path, installs the prepare-commit-msg hook, and creates an initial
// empty commit.
func (runner *AgentSessionRunner) createWorktree(ctx context.Context, state *RunState) error {
	_, err := runner.launcher.Run(ctx, proc.Command{
		Executable: gitExecutable,
		Arguments:  []string{gitDirectory, state.Repository, gitWorktree, gitAdd, gitNewBranch, state.Branch, state.Worktree, state.BaseBranch},
		Directory:  state.Repository,
	})
	if err != nil {
		return fmt.Errorf("%w: %w", ErrWorktree, err)
	}
	if err := runner.installCommitMsgHook(ctx, state); err != nil {
		return err
	}
	if err := runner.createInitialCommit(ctx, state); err != nil {
		return err
	}
	state.LastPushTime = int64(time.Now().Unix())
	return nil
}

// installCommitMsgHook installs the prepare-commit-msg hook in the run
// directory and points git's core.hooksPath at it. The worktree's .git is a
// file, not a directory, so the hook cannot live under it.
func (runner *AgentSessionRunner) installCommitMsgHook(ctx context.Context, state *RunState) error {
	hooksDir := filepath.Join(filepath.Dir(state.Worktree), "hooks")
	if err := os.MkdirAll(hooksDir, 0o700); err != nil {
		return fmt.Errorf("harness session: create hooks directory: %w", err)
	}
	hookPath := filepath.Join(hooksDir, "prepare-commit-msg")
	hookContent := []byte(hookScript)
	if err := os.WriteFile(hookPath, hookContent, 0o755); err != nil {
		return fmt.Errorf("harness session: write prepare-commit-msg hook: %w", err)
	}
	_, err := runner.launcher.Run(ctx, proc.Command{
		Executable: gitExecutable,
		Arguments:  []string{gitDirectory, state.Worktree, gitConfig, gitSetCoreHooksPath, hooksDir},
		Directory:  state.Worktree,
	})
	if err != nil {
		return fmt.Errorf("harness session: configure hooks path: %w", err)
	}
	return nil
}

// createInitialCommit creates an empty initial commit with the session started
// message. This enables the session gates to push and open a PR.
func (runner *AgentSessionRunner) createInitialCommit(ctx context.Context, state *RunState) error {
	_, err := runner.launcher.Run(ctx, proc.Command{
		Executable: gitExecutable,
		Arguments:  []string{gitDirectory, state.Worktree, gitCommit, gitAllowEmptyCommit, "-m", fmt.Sprintf("wip(%s): session started", state.AgentID)},
		Directory:  state.Worktree,
	})
	if err != nil {
		return fmt.Errorf("harness session: create initial commit: %w", err)
	}
	return nil
}

// PushRemoteURL is the URL of the worktree's push remote, as git records it:
// the repository gh is told to act on, so a clone whose gh default is another
// remote still gets its pull request where the branch was pushed.
func PushRemoteURL(ctx context.Context, launcher proc.ILauncher, worktree string) (string, error) {
	result, err := launcher.Run(ctx, proc.Command{
		Executable: gitExecutable,
		Arguments:  []string{gitDirectory, worktree, gitRemote, gitGetURL, PushRemote},
		Directory:  worktree,
	})
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrPushRemote, err)
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

// pullRequestURL is the branch's pull request on the push remote's
// repository, if one exists. Not finding one is not a failure of the turn:
// the commit gate opens it after the first commit, and a turn may commit
// nothing.
func (runner *AgentSessionRunner) pullRequestURL(ctx context.Context, log *EventLog, state *RunState) string {
	repository, err := PushRemoteURL(ctx, runner.launcher, state.Worktree)
	if err != nil {
		log.Failure(ctx, state.SessionID, state.Turns, EventTypeReceiptWritten, "pull request lookup failed", err)
		return ""
	}
	result, err := runner.launcher.Run(ctx, proc.Command{
		Executable: ghExecutable,
		Arguments:  []string{ghPullRequest, ghList, ghRepo, repository, ghHead, state.Branch, ghState, ghStateAll, ghJSON, ghURLField, ghJQ, ghFirstURL},
		Directory:  state.Worktree,
	})
	if err != nil {
		log.Failure(ctx, state.SessionID, state.Turns, EventTypeReceiptWritten, "pull request lookup failed", err)
		return ""
	}
	return strings.TrimSpace(string(result.Stdout))
}

// turnArguments are the turn executor's extra Claude Code arguments.
func turnArguments(plan *pb.AgentAssignmentPlan, settings string) []string {
	recipe := plan.GetRecipe()
	return []string{
		flagSettings, settings,
		flagModel, recipe.GetModel(),
		flagPermissionMode, permissionDontAsk,
		flagAppendSystemPrompt, recipe.GetAgent().GetInstructions(),
		flagAllowedTools, strings.Join(recipe.GetWorkspace().GetAllowedTools(), toolSeparator),
	}
}

// firstPrompt is the task as the agent first reads it, with the links that
// identify the work.
func firstPrompt(plan *pb.AgentAssignmentPlan, state *RunState) string {
	recipe := plan.GetRecipe()
	return fmt.Sprintf("Agent: %s (revision %d)\nAssignment: %s\nRecipe SHA-256: %s\nTicket: %s\nWorktree: %s (branch %s from %s)\n\n%s",
		recipe.GetAgent().GetId(), recipe.GetAgent().GetRevision(), recipe.GetAssignmentId(), plan.GetRecipeSha256(),
		recipe.GetTicketUrl(), state.Worktree, state.Branch, state.BaseBranch, recipe.GetTask())
}

// userMessage is prompt as one stream-json user message.
func userMessage(prompt string) (json.RawMessage, error) {
	type content struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type message struct {
		Type    string  `json:"type"`
		Message content `json:"message"`
	}
	encoded, err := json.Marshal(message{Type: messageTypeUser, Message: content{Role: roleUser, Content: prompt}})
	if err != nil {
		return nil, fmt.Errorf("harness session: encode prompt: %w", err)
	}
	return encoded, nil
}
