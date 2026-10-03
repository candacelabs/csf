// Copyright 2026 Candace Labs

package session

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/candacelabs/csf/ipc/model/claudecode"
	"github.com/candacelabs/csf/ipc/proc"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

// ErrSessionClosed reports a turn on an open session after Close.
var ErrSessionClosed = errors.New("harness session: the session is closed")

// EventTypeSessionClosed records that an open session's turn executor was
// closed.
const EventTypeSessionClosed = "harness_session_closed"

// EventTypeRunResumed records that a run was reopened on its recorded
// conversation, after the harness restarted.
const EventTypeRunResumed = "harness_run_resumed"

// RecipeFile holds the run's frozen recipe, so a restarted harness can reopen
// the run without being handed it again.
const RecipeFile = "recipe.json"

// IOpenTurnExecutor is a turn executor kept open across turns: the Claude Code
// process whose stream-json input stays open in production, a double in
// specs. Interrupt asks it to end the running turn at its next safepoint;
// Close ends it and joins what it started.
type IOpenTurnExecutor interface {
	ITurnExecutor
	Interrupt(ctx context.Context) error
	Close(ctx context.Context) error
}

// OpenTurnExecutorFactory opens the turn executor an [OpenSession] holds.
type OpenTurnExecutorFactory func(ctx context.Context, spec TurnExecutorSpec) (IOpenTurnExecutor, error)

// ClaudeCodeOpenTurnExecutors opens one Claude Code process per session
// through launcher and keeps it across turns.
func ClaudeCodeOpenTurnExecutors(launcher proc.ILauncher, executable string) OpenTurnExecutorFactory {
	return func(ctx context.Context, spec TurnExecutorSpec) (IOpenTurnExecutor, error) {
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
		brain, err := claudecode.NewClaudeCodeBrain(launcher, spec.Session, options...)
		if err != nil {
			return nil, err
		}
		return brain.Open(ctx)
	}
}

// RouteRequest is what a router knows about a virtual session (one
// assignment) before it opens.
type RouteRequest struct {
	Virtual string
	// Session is the conversation the virtual session opens when no
	// existing one fits.
	Session string
	Model   string
	// Agent is the agent's identity: its id and a hash of its instructions.
	// A resumed conversation keeps the system prompt it first recorded, so
	// only a conversation with the same identity may be attached.
	Agent string
	// Key is the topic key: the assignment's ticket.
	Key string
	// Summary is the assignment in a line: its pull request title.
	Summary string
}

// IRouter decides which real session, a Claude Code conversation, a virtual
// session runs on: an existing one it fits well in, which it resumes, or
// request.Session, a new one.
type IRouter interface {
	Route(ctx context.Context, request RouteRequest) (string, error)
}

// WithRouter routes every opened session: without one, each session opens a
// new conversation.
func WithRouter(router IRouter) AgentSessionRunnerOption {
	return func(runner *AgentSessionRunner) error {
		if router == nil {
			return fmt.Errorf("%w: nil router", ErrInvalidOption)
		}
		runner.router = router
		return nil
	}
}

// WithOpenTurnExecutors replaces the open Claude Code turn executor.
func WithOpenTurnExecutors(factory OpenTurnExecutorFactory) AgentSessionRunnerOption {
	return func(runner *AgentSessionRunner) error {
		if factory == nil {
			return fmt.Errorf("%w: nil open turn executor factory", ErrInvalidOption)
		}
		runner.openExecutors = factory
		return nil
	}
}

// OpenSession is one assignment's session held open: its run directory,
// event log and worktree exist, its turn executor is open, and no turn has
// run until the caller asks for one. One goroutine drives it; turns never
// overlap.
type OpenSession struct {
	runner   *AgentSessionRunner
	ctx      context.Context
	plan     *pb.AgentAssignmentPlan
	state    *RunState
	log      *EventLog
	executor IOpenTurnExecutor
	closed   bool
}

// Open prepares the assignment's session exactly as Run does, up to the first
// turn: the recipe is frozen, the worktree created, the gates installed and
// the turn executor opened on the session. The first turn is the caller's to
// run, with [OpenSession.FirstPrompt]. The worktree creation is bounded by
// ctx; the executor's process is bounded by the session, which Close ends.
func (runner *AgentSessionRunner) Open(ctx context.Context, recipe *pb.AgentAssignmentRecipe, environment ...string) (*OpenSession, error) {
	ctx, plan, state, log, err := runner.openRun(ctx, recipe)
	if err != nil {
		return nil, err
	}
	directory := RunDirectory(runner.stateDirectory, state.AssignmentID)
	settings, err := writeSettings(directory, runner.gateCommand)
	if err != nil {
		_ = log.Close()
		return nil, err
	}
	attached := runner.route(ctx, log, plan, state)
	if err := WriteRunState(directory, state); err != nil {
		_ = log.Close()
		return nil, err
	}
	if err := writeRecipe(directory, plan.GetRecipe()); err != nil {
		_ = log.Close()
		return nil, err
	}
	executor, err := runner.openExecutors(ctx, TurnExecutorSpec{
		Session:     uuid.MustParse(state.SessionID),
		Directory:   state.Worktree,
		Arguments:   turnArguments(plan, settings),
		Logger:      log.Logger(),
		Environment: environment,
		Resume:      attached,
	})
	if err != nil {
		log.Failure(ctx, state.SessionID, 0, EventTypeTurnRequested, "turn executor not opened", err)
		_ = log.Close()
		return nil, fmt.Errorf("harness session: open turn executor: %w", err)
	}
	return &OpenSession{runner: runner, ctx: ctx, plan: plan, state: state, log: log, executor: executor}, nil
}

// Reopen reopens a run a previous harness process left open: the same
// assignment, worktree, branch and trace, and the turn executor opened on the
// recorded conversation, resumed when a turn has run on it. No worktree is
// created and nothing is replayed.
func (runner *AgentSessionRunner) Reopen(ctx context.Context, recipe *pb.AgentAssignmentRecipe, environment ...string) (*OpenSession, error) {
	plan, err := prepare(recipe)
	if err != nil {
		return nil, err
	}
	directory := RunDirectory(runner.stateDirectory, plan.GetRecipe().GetAssignmentId())
	state, err := ReadRunState(directory)
	if err != nil {
		return nil, err
	}
	if state.Branch != plan.GetRecipe().GetWorkspace().GetBranch() {
		return nil, fmt.Errorf("%w: %s", ErrRunMismatch, directory)
	}
	log, err := OpenEventLog(directory, RunTrace(state))
	if err != nil {
		return nil, err
	}
	ctx, err = log.Context(ctx)
	if err != nil {
		_ = log.Close()
		return nil, err
	}
	settings, err := writeSettings(directory, runner.gateCommand)
	if err == nil {
		err = writeRecipe(directory, plan.GetRecipe())
	}
	if err != nil {
		_ = log.Close()
		return nil, err
	}
	log.Record(ctx, state.SessionID, state.Turns, EventTypeRunResumed, "harness run resumed", slog.Int(KeyTurn, state.Turns))
	executor, err := runner.openExecutors(ctx, TurnExecutorSpec{
		Session:     uuid.MustParse(state.SessionID),
		Directory:   state.Worktree,
		Arguments:   turnArguments(plan, settings),
		Logger:      log.Logger(),
		Environment: environment,
		Resume:      state.Turns > 0,
	})
	if err != nil {
		log.Failure(ctx, state.SessionID, state.Turns, EventTypeRunResumed, "turn executor not reopened", err)
		_ = log.Close()
		return nil, fmt.Errorf("harness session: reopen turn executor: %w", err)
	}
	return &OpenSession{runner: runner, ctx: ctx, plan: plan, state: state, log: log, executor: executor}, nil
}

// ReadRecipe reads the recipe a run recorded when it opened.
func ReadRecipe(directory string) (*pb.AgentAssignmentRecipe, error) {
	content, err := os.ReadFile(filepath.Join(directory, RecipeFile))
	if err != nil {
		return nil, fmt.Errorf("harness session: read recipe: %w", err)
	}
	recipe := &pb.AgentAssignmentRecipe{}
	if err := protojson.Unmarshal(content, recipe); err != nil {
		return nil, fmt.Errorf("harness session: decode recipe: %w", err)
	}
	return recipe, nil
}

func writeRecipe(directory string, recipe *pb.AgentAssignmentRecipe) error {
	content, err := protojson.Marshal(recipe)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(directory, RecipeFile), content, runFileMode); err != nil {
		return fmt.Errorf("harness session: write recipe: %w", err)
	}
	return nil
}

// route asks the router which real session the virtual session runs on and
// records it as the run's session. It reports whether that is an existing
// conversation, to be resumed. A router that fails, or answers with an
// identifier that is not a conversation's, leaves the new one in place.
func (runner *AgentSessionRunner) route(ctx context.Context, log *EventLog, plan *pb.AgentAssignmentPlan, state *RunState) bool {
	if runner.router == nil {
		return false
	}
	real, err := runner.router.Route(ctx, RouteRequest{
		Virtual: state.AssignmentID, Session: state.SessionID, Model: plan.GetRecipe().GetModel(), Agent: agentIdentity(plan),
		Key: plan.GetRecipe().GetTicketUrl(), Summary: plan.GetRecipe().GetWorkspace().GetPullRequestTitle(),
	})
	if err == nil {
		_, err = uuid.Parse(real)
	}
	if err != nil {
		log.Failure(ctx, state.SessionID, 0, EventTypeTurnRequested, "routing failed; opening a new conversation", err)
		return false
	}
	attached := real != state.SessionID
	state.SessionID = real
	return attached
}

// agentIdentity is the agent's id and the first twelve hex digits of the
// SHA-256 of its instructions.
func agentIdentity(plan *pb.AgentAssignmentPlan) string {
	sum := sha256.Sum256([]byte(plan.GetRecipe().GetAgent().GetInstructions()))
	return plan.GetRecipe().GetAgent().GetId() + "/" + hex.EncodeToString(sum[:6])
}

// FirstPrompt is the task as the agent first reads it.
func (open *OpenSession) FirstPrompt() string { return firstPrompt(open.plan, open.state) }

// State is a copy of the run's record as last written.
func (open *OpenSession) State() RunState { return *open.state }

// Plan is the frozen recipe the session runs.
func (open *OpenSession) Plan() *pb.AgentAssignmentPlan { return open.plan }

// Directory is the run directory: events.jsonl, run.json, settings.json and
// the worktree live there.
func (open *OpenSession) Directory() string {
	return RunDirectory(open.runner.stateDirectory, open.state.AssignmentID)
}

// Turn runs prompt as the session's next turn on the open executor and
// returns the receipt. ctx bounds the wait for the turn's result; the
// executor's process outlives it.
func (open *OpenSession) Turn(ctx context.Context, prompt string) (*pb.AgentAssignmentReceipt, error) {
	if open.closed {
		return nil, ErrSessionClosed
	}
	if prompt == "" {
		return nil, ErrNoPrompt
	}
	traced, err := open.log.Context(ctx)
	if err != nil {
		return nil, err
	}
	open.state.Turns++
	if err := WriteRunState(open.Directory(), open.state); err != nil {
		return nil, err
	}
	return open.runner.executeTurn(traced, open.log, open.plan, open.state, open.executor, prompt, open.state.Turns > 1)
}

// Interrupt asks the running turn to end at the executor's next safepoint.
func (open *OpenSession) Interrupt(ctx context.Context) error {
	if open.closed {
		return ErrSessionClosed
	}
	return open.executor.Interrupt(ctx)
}

// Close ends the turn executor, bounded by ctx, and closes the event log. It
// is idempotent.
func (open *OpenSession) Close(ctx context.Context) error {
	if open.closed {
		return nil
	}
	open.closed = true
	err := open.executor.Close(ctx)
	if err != nil {
		open.log.Failure(open.ctx, open.state.SessionID, open.state.Turns, EventTypeSessionClosed, "turn executor closed", err)
	} else {
		open.log.Record(open.ctx, open.state.SessionID, open.state.Turns, EventTypeSessionClosed, "turn executor closed",
			slog.Int(KeyTurn, open.state.Turns))
	}
	return errors.Join(err, open.log.Close())
}
