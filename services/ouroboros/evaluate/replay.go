// Copyright 2026 Candace Labs

package evaluate

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdfs "io/fs"
	"log/slog"
	"path"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/kernel/clock"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/ouroboros"
)

const (
	// replayBranchFormat names a replay's work branch: build, suite version
	// and the ticket's position in the suite, so two builds' replays of one
	// ticket never collide and the branch, which session listings print,
	// does not name the held-out ticket.
	replayBranchFormat = "eval/%s/v%d/%02d"
	gitExecutable      = "git"
	gitDirectory       = "-C"
	gitDiff            = "diff"
	gitNameOnly        = "--name-only"
	worktreeName       = "worktree"
)

var (
	// ErrInvalidOption reports a nil option or one the replayer cannot use.
	ErrInvalidOption = errors.New("evaluate: invalid option")
	// ErrNoOriginal reports a suite ticket whose original run left no recipe.
	ErrNoOriginal = errors.New("evaluate: the ticket's original run has no recipe to replay")
)

// Job is one suite ticket ready to replay: the ticket and the replay
// recipe, encoded as protojson so a job crosses to a burst node as data.
type Job struct {
	Ticket Ticket          `json:"ticket"`
	Recipe json.RawMessage `json:"recipe"`
}

// Jobs builds the replay of every suite ticket from its original run's
// recipe: the same agent and task, on the suite's model, from the commit its
// pull request started from, in repository (a clone whose pushes never
// reach the real one), on a branch named for the build.
func Jobs(suite Suite, corpus iofs.IFiles, build string, repository string) ([]Job, error) {
	jobs := make([]Job, 0, len(suite.Tickets))
	for position, ticket := range suite.Tickets {
		content, err := corpus.ReadFile(path.Join(ticket.Assignment, session.RecipeFile))
		if errors.Is(err, stdfs.ErrNotExist) {
			return nil, fmt.Errorf("%w: #%d (%s)", ErrNoOriginal, ticket.Number, ticket.Assignment)
		}
		if err != nil {
			return nil, err
		}
		var original pb.AgentAssignmentRecipe
		if err := (protojson.UnmarshalOptions{DiscardUnknown: true}).Unmarshal(content, &original); err != nil {
			return nil, fmt.Errorf("evaluate: decode the recipe of #%d: %w", ticket.Number, err)
		}
		replay := proto.Clone(&original).(*pb.AgentAssignmentRecipe)
		replay.AssignmentId = uuid.NewString()
		replay.Model = suite.Model
		workspace := replay.GetWorkspace()
		if workspace == nil {
			return nil, fmt.Errorf("%w: #%d's recipe has no workspace", ErrNoOriginal, ticket.Number)
		}
		workspace.RepositoryPath = repository
		workspace.AllowedTools = slices.Clone(suite.tools())
		// The stored recipe carries the brief inline as its task.
		workspace.BriefPath = ""
		workspace.BaseBranch = ticket.BaseCommit
		workspace.Branch = fmt.Sprintf(replayBranchFormat, build, suite.Version, position+1)
		encoded, err := protojson.Marshal(replay)
		if err != nil {
			return nil, err
		}
		jobs = append(jobs, Job{Ticket: ticket, Recipe: encoded})
	}
	return jobs, nil
}

// Relocate points every job's replay at repository: the clone a node other
// than the one that built the jobs replays from.
func Relocate(jobs []Job, repository string) ([]Job, error) {
	moved := make([]Job, 0, len(jobs))
	for _, job := range jobs {
		var recipe pb.AgentAssignmentRecipe
		if err := protojson.Unmarshal(job.Recipe, &recipe); err != nil {
			return nil, fmt.Errorf("evaluate: decode the replay recipe of #%d: %w", job.Ticket.Number, err)
		}
		recipe.GetWorkspace().RepositoryPath = repository
		encoded, err := protojson.Marshal(&recipe)
		if err != nil {
			return nil, err
		}
		moved = append(moved, Job{Ticket: job.Ticket, Recipe: encoded})
	}
	return moved, nil
}

// Host is the harness host a replay runs on, as the four operations the
// replayer calls: the generated client's methods, or the in-process
// service's.
type Host struct {
	Admit  func(ctx context.Context, request *harnessv1.CheckAgentSessionAdmissionRequest) (*harnessv1.CheckAgentSessionAdmissionResponse, error)
	Submit func(ctx context.Context, request *harnessv1.SubmitAgentSessionRequest) (*harnessv1.SubmitAgentSessionResponse, error)
	Get    func(ctx context.Context, request *harnessv1.GetAgentSessionRequest) (*harnessv1.GetAgentSessionResponse, error)
	Cancel func(ctx context.Context, request *harnessv1.CancelAgentSessionRequest) (*harnessv1.CancelAgentSessionResponse, error)
}

// Replayer runs replays on one harness host: it submits each job, reads the
// replay's event log as it grows, cancels the session once the suite's
// budget of tool calls is spent, and records what the replay read.
type Replayer struct {
	sessions  Host
	launcher  proc.ILauncher
	state     iofs.IFiles
	directory string
	clock     clock.IClock
	poll      time.Duration
	parallel  int
	logger    *slog.Logger
}

// ReplayerOption configures a [Replayer].
type ReplayerOption func(replayer *Replayer) error

// WithHost grants the harness host the replays run on. Required.
func WithHost(host Host) ReplayerOption {
	return func(replayer *Replayer) error {
		if host.Admit == nil || host.Submit == nil || host.Get == nil || host.Cancel == nil {
			return fmt.Errorf("%w: the host needs all four operations", ErrInvalidOption)
		}
		replayer.sessions = host
		return nil
	}
}

// WithLauncher grants the process capability git reads a replay's change
// through. Required.
func WithLauncher(launcher proc.ILauncher) ReplayerOption {
	return func(replayer *Replayer) error {
		if launcher == nil {
			return fmt.Errorf("%w: nil launcher", ErrInvalidOption)
		}
		replayer.launcher = launcher
		return nil
	}
}

// WithState grants the host's state directory, where each replay's run
// directory and event log appear. Required.
func WithState(directory string, files iofs.IFiles) ReplayerOption {
	return func(replayer *Replayer) error {
		if directory == "" || files == nil {
			return fmt.Errorf("%w: the state directory needs a path and its files", ErrInvalidOption)
		}
		replayer.directory, replayer.state = directory, files
		return nil
	}
}

// WithClock grants the clock the replayer paces its reads by. Required.
func WithClock(source clock.IClock, poll time.Duration) ReplayerOption {
	return func(replayer *Replayer) error {
		if source == nil || poll <= 0 {
			return fmt.Errorf("%w: the clock needs a value and a positive poll", ErrInvalidOption)
		}
		replayer.clock, replayer.poll = source, poll
		return nil
	}
}

// WithParallel bounds the replays running at once on the host; the host's
// own admission bounds them further.
func WithParallel(parallel int) ReplayerOption {
	return func(replayer *Replayer) error {
		if parallel < 1 {
			return fmt.Errorf("%w: parallel must be at least 1", ErrInvalidOption)
		}
		replayer.parallel = parallel
		return nil
	}
}

// WithLogger grants the logger progress goes to.
func WithLogger(logger *slog.Logger) ReplayerOption {
	return func(replayer *Replayer) error {
		if logger == nil {
			return fmt.Errorf("%w: nil logger", ErrInvalidOption)
		}
		replayer.logger = logger
		return nil
	}
}

// NewReplayer validates the whole option set before building the replayer.
func NewReplayer(options ...ReplayerOption) (*Replayer, error) {
	replayer := &Replayer{parallel: 1, logger: slog.New(slog.DiscardHandler)}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(replayer); err != nil {
			return nil, err
		}
	}
	if replayer.sessions.Submit == nil || replayer.launcher == nil || replayer.state == nil || replayer.clock == nil {
		return nil, fmt.Errorf("%w: sessions, launcher, state and clock are required", ErrInvalidOption)
	}
	return replayer, nil
}

// running is one submitted replay.
type running struct {
	job        Job
	assignment string
	started    time.Time
}

// Run replays jobs on the host for build and suite at the suite's budget and
// hands each finished replay to record, tagged with node.
func (replayer *Replayer) Run(ctx context.Context, build string, suite Suite, node string, jobs []Job, record func(ctx context.Context, replay Replay) error) error {
	queue := jobs
	active := map[string]running{}
	for len(queue) > 0 || len(active) > 0 {
		for len(queue) > 0 && len(active) < replayer.parallel {
			started, admitted, err := replayer.submit(ctx, queue[0])
			if err != nil {
				return err
			}
			if !admitted {
				break
			}
			active[started.assignment] = started
			queue = queue[1:]
			replayer.logger.Info("evaluate: replay started", "ticket", started.job.Ticket.Number, "assignment", started.assignment, "queued", len(queue))
		}
		for assignment, replay := range active {
			done, err := replayer.check(ctx, build, suite, node, replay, record)
			if err != nil {
				return err
			}
			if done {
				delete(active, assignment)
			}
		}
		if len(queue) == 0 && len(active) == 0 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-replayer.clock.After(replayer.poll):
		}
	}
	return nil
}

// submit starts one replay when the host admits it now; admitted is false
// when the host holds new sessions, and the job waits for the next read.
func (replayer *Replayer) submit(ctx context.Context, job Job) (started running, admitted bool, err error) {
	var recipe pb.AgentAssignmentRecipe
	if err := protojson.Unmarshal(job.Recipe, &recipe); err != nil {
		return running{}, false, fmt.Errorf("evaluate: decode the replay recipe of #%d: %w", job.Ticket.Number, err)
	}
	admission, err := replayer.sessions.Admit(ctx, &harnessv1.CheckAgentSessionAdmissionRequest{Recipe: &recipe})
	if err != nil {
		return running{}, false, err
	}
	check := admission.GetCheck()
	if admission.GetHeld() != "" || !(check.GetAdmitted() || check.GetReportOnly()) {
		return running{}, false, nil
	}
	if _, err := replayer.sessions.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: &recipe}); err != nil {
		return running{}, false, fmt.Errorf("evaluate: submit the replay of #%d: %w", job.Ticket.Number, err)
	}
	return running{job: job, assignment: recipe.GetAssignmentId(), started: replayer.clock.Now()}, true, nil
}

// check reads one replay: once its budget is spent it is canceled, and once
// it is no longer running it is read, its change compared with the
// reference and the replay recorded.
func (replayer *Replayer) check(ctx context.Context, build string, suite Suite, node string, replay running, record func(ctx context.Context, replay Replay) error) (bool, error) {
	events, err := replayer.state.ReadFile(path.Join(replay.assignment, session.EventsFile))
	if err != nil && !errors.Is(err, stdfs.ErrNotExist) {
		return false, err
	}
	calls, _ := Read(events, 0)
	got, err := replayer.sessions.Get(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: replay.assignment})
	if err != nil {
		return false, err
	}
	phase := got.GetSession().GetPhase()
	live := phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_STARTING || phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING
	if live && calls < int64(suite.Derivation.Budget) {
		return false, nil
	}
	if phase != harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED && phase != harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED {
		if _, err := replayer.sessions.Cancel(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: replay.assignment}); err != nil {
			replayer.logger.Warn("evaluate: replay not canceled", "assignment", replay.assignment, "error", err)
		}
	}
	toolCalls, episodes := Read(events, suite.Derivation.Budget)
	usage := ouroboros.FoldUsage(events)
	touched, err := replayer.touched(ctx, replay.assignment, replay.job.Ticket.BaseCommit)
	if err != nil {
		replayer.logger.Warn("evaluate: replay change not read", "assignment", replay.assignment, "error", err)
	}
	result := Replay{
		Build: build, SuiteVersion: suite.Version, Ticket: replay.job.Ticket.Number, Node: node, Assignment: replay.assignment,
		ToolCalls: toolCalls, Episodes: episodes, Recall: Recall(replay.job.Ticket.Files, touched),
		CostUSDMicros: usage.CostUSDMicros, Seconds: int64(replayer.clock.Now().Sub(replay.started) / time.Second),
		RecordedAt: replayer.clock.Now(),
	}
	replayer.logger.Info("evaluate: replay read", "ticket", result.Ticket, "tool_calls", toolCalls, "episodes", episodes, "recall", result.Recall)
	return true, record(ctx, result)
}

// touched lists the files the replay's worktree changed against its base,
// committed or not.
func (replayer *Replayer) touched(ctx context.Context, assignment string, base string) ([]string, error) {
	result, err := replayer.launcher.Run(ctx, proc.Command{
		Executable: gitExecutable,
		Arguments:  []string{gitDirectory, filepath.Join(replayer.directory, assignment, worktreeName), gitDiff, gitNameOnly, base},
	})
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(string(result.Stdout)), "\n") {
		if line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}
