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
	"slices"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/affect"
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

// The session hypervisor's suspend and resume records. The router reads none
// of them as a close of the conversation: a suspended session still holds it
// and resumes on it.
const (
	// EventTypeSessionSuspended records that an open session was suspended: its
	// turn executor closed, the session open on its recorded conversation.
	EventTypeSessionSuspended = "harness_session_suspended"
	// EventTypeSessionResumed records that a suspended session was resumed: its
	// turn executor opened again on the recorded conversation for the next turn.
	EventTypeSessionResumed = "harness_session_resumed"
	// EventTypeTurnResumed records, after the first turn on a resumed session,
	// what the resume cost: the time to the turn's first token.
	EventTypeTurnResumed = "harness_turn_resumed"
	// KeyIdleSeconds is how long the session had been between turns when its
	// session was suspended.
	KeyIdleSeconds = "idle_seconds"
	// KeySuspendedSeconds is how long the session was suspended before it resumed.
	KeySuspendedSeconds = "suspended_seconds"
	// KeyTimeToFirstToken is the resumed turn's time to its first assistant
	// event, in milliseconds.
	KeyTimeToFirstToken = "time_to_first_token_ms"
)

// ErrSuspended reports a turn asked of a suspended session; the caller
// resumes it first.
var ErrSuspended = errors.New("harness session: the session is suspended; resume it before the turn")

// RecipeFile holds the run's frozen recipe, so a restarted harness can reopen
// the run without being handed it again.
const RecipeFile = "recipe.json"

// IOpenTurnExecutor is a turn executor kept open across turns: the Claude Code
// process whose stream-json input stays open in production, a double in
// specs. Interrupt asks it to end the running turn at its next safepoint;
// Inject writes a message to the executor's input; Close ends it and joins
// what it started.
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
		if len(spec.LaunchPrefix) > 0 {
			options = append(options, claudecode.WithLaunchPrefix(spec.LaunchPrefix...))
		}
		if spec.Resume {
			options = append(options, claudecode.WithResumedSession())
		}
		if spec.TaskNotifications != nil {
			options = append(options, claudecode.WithTaskNotifications(spec.TaskNotifications))
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
	runner *AgentSessionRunner
	ctx    context.Context
	plan   *pb.AgentAssignmentPlan
	state  *RunState
	log    *EventLog
	// executor is nil while the session is suspended: the session stays open
	// on its conversation and Resume opens the executor again on spec.
	executor IOpenTurnExecutor
	spec     TurnExecutorSpec
	// resumed is set from a resume until the next turn reports it on its
	// receipt.
	resumed *pb.ResumedTurn
	// confined is set when the session runs sandboxed: its cgroup and Docker
	// proxy are torn down on Close.
	confined *confinement
	// contained is set when the session runs in a container, which Close
	// removes.
	contained *containment
	// background carries the executor's between-turn task notifications to
	// the goroutine driving the session.
	background chan claudecode.TaskNotification
	// settings is the Claude Code settings file the turn executor was opened
	// with, kept so a provider fallback reopens it on the same gates.
	settings string
	// attempt is the provider spelling the executor is currently on, counted
	// from zero: a fallback reopens the executor on attempt+1.
	attempt int
	closed  bool
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
	opened, err := runner.openExecutor(ctx, state, plan, settings, directory, environment, attached, log)
	if err != nil {
		log.Failure(ctx, state.SessionID, 0, EventTypeTurnRequested, "turn executor not opened", err)
		_ = log.Close()
		return nil, fmt.Errorf("harness session: open turn executor: %w", err)
	}
	opened.runner, opened.ctx, opened.plan, opened.state, opened.log = runner, ctx, plan, state, log
	return opened, nil
}

// openExecutor opens the turn executor: inside the session's own container
// when the runner is granted container sessions, or confined in the session
// sandbox when it is granted one. Either becomes the executor's argv prefix.
// A failed open tears down what it created. The returned session carries only
// the executor and its container or confinement.
func (runner *AgentSessionRunner) openExecutor(ctx context.Context, state *RunState, plan *pb.AgentAssignmentPlan,
	settings string, directory string, environment []string, resume bool, log *EventLog) (*OpenSession, error) {
	arguments, executorEnvironment, err := runner.launch(plan, directory, settings)
	if err != nil {
		return nil, err
	}
	runner.recordProvider(ctx, log, state, plan, 0)
	// The executor's own environment reaches it inside a container only
	// through the attach prefix, so the container is given it too.
	environment = slices.Concat(environment, executorEnvironment)
	opened := &OpenSession{background: make(chan claudecode.TaskNotification, backgroundBuffer)}
	var launchPrefix, sandboxEnvironment []string
	switch {
	case runner.containers != nil:
		contained, err := runner.contain(ctx, state, directory, environment, log)
		if err != nil {
			return nil, err
		}
		opened.contained, launchPrefix = contained, contained.launchPrefix
	case runner.sandbox != nil:
		confined, err := runner.confine(state, directory, environment, log.Logger())
		if err != nil {
			return nil, err
		}
		opened.confined, launchPrefix, sandboxEnvironment = confined, confined.launchPrefix, confined.environment
	}
	spec := TurnExecutorSpec{
		Session:           uuid.MustParse(state.SessionID),
		Directory:         state.Worktree,
		Arguments:         arguments,
		Logger:            log.Logger(),
		Environment:       slices.Concat(environment, sandboxEnvironment),
		LaunchPrefix:      launchPrefix,
		Resume:            resume,
		TaskNotifications: opened.deliverBackground,
		Executor:          ExecutorOf(plan.GetRecipe()),
		RunDirectory:      directory,
	}
	executor, err := runner.openExecutors(ctx, spec)
	if err != nil {
		opened.release(ctx, log, state.SessionID, 0)
		return nil, err
	}
	opened.executor, opened.spec = executor, spec
	opened.settings, opened.attempt = settings, 0
	return opened, nil
}

// release ends the session's container or confinement and records its launch
// receipt: a host session's says it ran on the host.
func (open *OpenSession) release(ctx context.Context, log *EventLog, sessionID string, turns int) {
	if open.contained != nil {
		open.contained.close(ctx, log, sessionID, turns)
		return
	}
	if open.confined != nil {
		open.confined.close(ctx, log, sessionID, turns)
	}
	log.Record(ctx, sessionID, turns, EventTypeLaunchReceipt, "session launch receipt", slog.String(KeyLaunch, LaunchHost))
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
	opened, err := runner.openExecutor(ctx, state, plan, settings, directory, environment, state.Turns > 0, log)
	if err != nil {
		log.Failure(ctx, state.SessionID, state.Turns, EventTypeRunResumed, "turn executor not reopened", err)
		_ = log.Close()
		return nil, fmt.Errorf("harness session: reopen turn executor: %w", err)
	}
	opened.runner, opened.ctx, opened.plan, opened.state, opened.log = runner, ctx, plan, state, log
	return opened, nil
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
// SHA-256 of its instructions. A Copilot session's identity also names the
// executor, so a conversation is only attached by the executor that holds
// it; a Claude Code identity is spelled as before executors were chosen, so
// recorded conversations still attach.
func agentIdentity(plan *pb.AgentAssignmentPlan) string {
	sum := sha256.Sum256([]byte(plan.GetRecipe().GetAgent().GetInstructions()))
	identity := plan.GetRecipe().GetAgent().GetId() + "/" + hex.EncodeToString(sum[:6])
	if executor := ExecutorOf(plan.GetRecipe()); executor != ExecutorClaudeCode {
		identity = string(executor) + ":" + identity
	}
	return identity
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

// TurnOption describes the message a turn carries.
type TurnOption func(input *turnInput)

// OperatorAuthored marks the turn's message as the operator's own words: a
// chat turn the operator sent, or a relayed message that quotes the operator
// verbatim. The harness computes the message's unvetted terms against the
// operator's vocabulary, records them and puts them into the turn, and the
// reply gate holds the reply to them. It records the message's operator
// affect too, and when the strain reads high it says so in the turn and the
// reply gate bounds the reply's length.
func OperatorAuthored() TurnOption {
	return func(input *turnInput) { input.operator = true }
}

// Turn runs prompt as the session's next turn on the open executor and
// returns the receipt. ctx bounds the wait for the turn's result; the
// executor's process outlives it.
func (open *OpenSession) Turn(ctx context.Context, prompt string, options ...TurnOption) (*pb.AgentAssignmentReceipt, error) {
	if open.closed {
		return nil, ErrSessionClosed
	}
	if open.executor == nil {
		return nil, ErrSuspended
	}
	if prompt == "" {
		return nil, ErrNoPrompt
	}
	input := turnInput{prompt: prompt}
	for _, option := range options {
		option(&input)
	}
	traced, err := open.log.Context(ctx)
	if err != nil {
		return nil, err
	}
	if input.operator {
		input.affect = affect.Read(input.prompt)
		if err := input.vetAgainst(open.runner.stateDirectory); err != nil {
			return nil, err
		}
		if input.affect.Strain == affect.StrainHigh {
			input.prompt += strainNotice()
		}
	}
	open.state.Turns++
	if err := WriteRunState(open.Directory(), open.state); err != nil {
		return nil, err
	}
	input.resume = open.state.Turns > 1
	receipt, err := open.runner.executeTurn(traced, open.log, open.plan, open.state, open.executor, input)
	if errors.Is(err, ErrProviderUnavailable) {
		retried, fallbackErr := open.fallBack(traced, err)
		switch {
		case fallbackErr != nil:
			return receipt, errors.Join(err, fallbackErr)
		case retried:
			receipt, err = open.runner.executeTurn(traced, open.log, open.plan, open.state, open.executor, input)
		}
	}
	if receipt != nil && open.resumed != nil {
		receipt.Resumed = open.resumed
		open.resumed = nil
		receipt.Resumed.TimeToFirstTokenMs = timeToFirstToken(open.Directory())
		open.log.Record(traced, open.state.SessionID, open.state.Turns, EventTypeTurnResumed, "turn on a resumed session",
			slog.Float64(KeyTimeToFirstToken, receipt.Resumed.GetTimeToFirstTokenMs()),
			slog.Float64(KeySuspendedSeconds, receipt.Resumed.GetSuspendedSeconds()))
	}
	return receipt, err
}

// timeToFirstToken is the running turn's time from its request to the
// executor's first assistant event, in milliseconds, read from the event log;
// zero when the turn reported no assistant event.
func timeToFirstToken(directory string) float64 {
	records, err := ReadTurnRecords(directory)
	if err != nil || len(records) == 0 {
		return 0
	}
	requested := records[0].Time
	for _, record := range records[1:] {
		if record.EventType == EventTypeAssistant && record.Direction == DirectionOut {
			return float64(record.Time.Sub(requested).Microseconds()) / 1000
		}
	}
	return 0
}

// Suspended reports whether the session is suspended: its turn executor closed.
func (open *OpenSession) Suspended() bool { return open.executor == nil }

// Suspend is the session hypervisor's suspend: it ends the turn executor's
// process, bounded by ctx, and keeps the session open on its recorded
// conversation: the event log stays open, the worktree and run record stay,
// and [OpenSession.Resume] opens the executor again. idle is how long the
// session had been between turns. A suspended session is left as it is.
func (open *OpenSession) Suspend(ctx context.Context, idle time.Duration) error {
	if open.closed {
		return ErrSessionClosed
	}
	if open.executor == nil {
		return nil
	}
	err := open.executor.Close(ctx)
	open.executor = nil
	if err != nil {
		open.log.Failure(open.ctx, open.state.SessionID, open.state.Turns, EventTypeSessionSuspended, "session suspended: turn executor closed", err,
			slog.Float64(KeyIdleSeconds, idle.Seconds()))
		return err
	}
	open.log.Record(open.ctx, open.state.SessionID, open.state.Turns, EventTypeSessionSuspended, "session suspended: turn executor closed",
		slog.Int(KeyTurn, open.state.Turns), slog.Float64(KeyIdleSeconds, idle.Seconds()))
	return nil
}

// Resume is the session hypervisor's resume: it opens the turn executor again
// on the recorded conversation after a suspend, on the spec it was first
// opened with and resumed, exactly as a restarted harness reopens a run.
// suspended is how long the session was suspended; the next turn's receipt
// carries it with the time to first token. A session whose executor is open is
// left as it is.
func (open *OpenSession) Resume(ctx context.Context, suspended time.Duration) error {
	if open.closed {
		return ErrSessionClosed
	}
	if open.executor != nil {
		return nil
	}
	spec := open.spec
	spec.Resume = open.state.Turns > 0
	executor, err := open.runner.openExecutors(ctx, spec)
	if err != nil {
		open.log.Failure(open.ctx, open.state.SessionID, open.state.Turns, EventTypeSessionResumed, "session not resumed: turn executor not opened", err,
			slog.Float64(KeySuspendedSeconds, suspended.Seconds()))
		return fmt.Errorf("harness session: resume turn executor: %w", err)
	}
	open.executor = executor
	open.resumed = &pb.ResumedTurn{SuspendedSeconds: suspended.Seconds()}
	open.log.Record(open.ctx, open.state.SessionID, open.state.Turns, EventTypeSessionResumed, "session resumed: turn executor opened",
		slog.Int(KeyTurn, open.state.Turns), slog.Float64(KeySuspendedSeconds, suspended.Seconds()))
	return nil
}

// Interrupt asks the running turn to end at the executor's next safepoint. An
// suspended session runs no turn, so there is nothing to interrupt.
func (open *OpenSession) Interrupt(ctx context.Context) error {
	if open.closed {
		return ErrSessionClosed
	}
	if open.executor == nil {
		return nil
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
	var err error
	if open.executor != nil {
		err = open.executor.Close(ctx)
	}
	if err != nil {
		open.log.Failure(open.ctx, open.state.SessionID, open.state.Turns, EventTypeSessionClosed, "turn executor closed", err)
	} else {
		open.log.Record(open.ctx, open.state.SessionID, open.state.Turns, EventTypeSessionClosed, "turn executor closed",
			slog.Int(KeyTurn, open.state.Turns))
	}
	open.release(open.ctx, open.log, open.state.SessionID, open.state.Turns)
	return errors.Join(err, open.log.Close())
}
