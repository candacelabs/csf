// Copyright 2026 Candace Labs

package harness

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/io/ipc/proc"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/harness/session"
)

// The control plane's pull request operations: what they run and where.
const (
	programGitHub = "gh"
	programBash   = "bash"
	ghPullRequest = "pr"
	ghReady       = "ready"
	ghMerge       = "merge"
	ghSquash      = "--squash"
	// MergeScript is the repository's own merge path, relative to the
	// worktree; a repository without one is merged with gh's squash merge.
	MergeScript = "tools/merge-pr.sh"
	// MergeBazelDirectory is the merge path's Bazel output base under the
	// state directory, shared by every operator merge.
	MergeBazelDirectory = "merge-bazel"
	pullRequestPathMark = "/pull/"
)

var (
	// ErrNoPullRequest reports a ready or merge for a session that has not
	// opened a pull request.
	ErrNoPullRequest = fmt.Errorf("%w: the session has no pull request", csf.ErrConflict)
	// ErrNoLauncher reports a ready or merge on a host that granted the
	// service no process capability.
	ErrNoLauncher = errors.New("harness: this host grants no launcher for pull request operations")
)

// WithLauncher grants the process capability gh and the merge path start
// through. Without it Ready and Merge report [ErrNoLauncher].
func WithLauncher(launcher proc.ILauncher) AgentSessionServiceOption {
	return func(service *AgentSessionService) error {
		if launcher == nil {
			return fmt.Errorf("%w: nil launcher", ErrInvalidServiceOption)
		}
		service.launcher = launcher
		return nil
	}
}

// CheckAdmission reports the launch check the recipe would meet now, and why admission
// is held when it is, without admitting it.
func (service *AgentSessionService) CheckAdmission(ctx context.Context, request *harnessv1.CheckAgentSessionAdmissionRequest) (*harnessv1.CheckAgentSessionAdmissionResponse, error) {
	recipe := request.GetRecipe()
	if recipe == nil {
		return nil, fmt.Errorf("%w: a recipe is required", csf.ErrInvalidRequest)
	}
	if _, err := csf.PrepareAgentAssignment(recipe); err != nil {
		return nil, fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
	}
	if err := service.admitModel(recipe.GetModel()); err != nil {
		return nil, err
	}
	response := &harnessv1.CheckAgentSessionAdmissionResponse{}
	err := service.command(ctx, func(table *registry) error {
		if _, exists := table.sessions[recipe.GetAssignmentId()]; exists {
			return fmt.Errorf("%w: %s", ErrSessionExists, recipe.GetAssignmentId())
		}
		response.Held = table.held
		response.Check = launchCheck(service.measures, service.runner.StateDirectory(), uint32(table.running()), table.largestRun)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return response, nil
}

// Ready marks the session's draft pull request ready for review.
func (service *AgentSessionService) Ready(ctx context.Context, request *harnessv1.ReadyAgentSessionPullRequestRequest) (*harnessv1.ReadyAgentSessionPullRequestResponse, error) {
	if err := harnessv1.ValidateReadyAgentSessionPullRequestRequest(request); err != nil {
		return nil, fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
	}
	state, err := service.pullRequestOf(ctx, request.GetAssignmentId())
	if err != nil {
		return nil, err
	}
	_, err = service.launcher.Run(ctx, proc.Command{Executable: programGitHub, Arguments: []string{ghPullRequest, ghReady, state.GetPullRequestUrl()}, Directory: state.GetWorktree()})
	service.recordAction(ctx, request.GetAssignmentId(), session.ActionReady, err, slog.String(session.KeyPullRequestURL, state.GetPullRequestUrl()))
	if err != nil {
		return nil, fmt.Errorf("harness: mark %s ready: %w", state.GetPullRequestUrl(), err)
	}
	return &harnessv1.ReadyAgentSessionPullRequestResponse{Session: state}, nil
}

// Merge merges the session's pull request through the repository's merge
// path: the worktree's tools/merge-pr.sh, which merges the head with main,
// runs the merge checks and squash-merges only a passing result, or gh's
// squash merge where the repository has no such script. It waits for the
// path to finish; the report is what it printed.
//
// The merge runs on the service's own scope, not the caller's: a caller that
// goes away — a closed Workbench tab, an MCP client that timed out — stops
// waiting, and the merge carries on and records its outcome in the session's
// event log. It records merge_started first, so the log says a merge is in
// flight until the merge record that ends it.
func (service *AgentSessionService) Merge(ctx context.Context, request *harnessv1.MergeAgentSessionPullRequestRequest) (*harnessv1.MergeAgentSessionPullRequestResponse, error) {
	if err := harnessv1.ValidateMergeAgentSessionPullRequestRequest(request); err != nil {
		return nil, fmt.Errorf("%w: %w", csf.ErrInvalidRequest, err)
	}
	state, err := service.pullRequestOf(ctx, request.GetAssignmentId())
	if err != nil {
		return nil, err
	}
	command, err := service.mergeCommand(state)
	if err != nil {
		return nil, err
	}
	url := slog.String(session.KeyPullRequestURL, state.GetPullRequestUrl())
	service.recordAction(ctx, request.GetAssignmentId(), session.ActionMergeStarted, nil, url)
	type outcome struct {
		report string
		err    error
	}
	// Buffered, so the merge never blocks on a caller that stopped waiting.
	done := make(chan outcome, 1)
	err = service.command(ctx, func(table *registry) error {
		return table.scope.Go(func(scopeCtx context.Context) error {
			result, err := service.launcher.Run(scopeCtx, command)
			service.recordAction(scopeCtx, request.GetAssignmentId(), session.ActionMerge, err, url)
			done <- outcome{report: strings.TrimSpace(string(result.Stdout) + string(result.Stderr)), err: err}
			// A failed merge is the session's outcome, not the scope's.
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	select {
	case merged := <-done:
		if merged.err != nil {
			return nil, fmt.Errorf("harness: merge %s: %w\n%s", state.GetPullRequestUrl(), merged.err, merged.report)
		}
		return &harnessv1.MergeAgentSessionPullRequestResponse{Session: state, Report: merged.report}, nil
	case <-ctx.Done():
		return nil, fmt.Errorf("harness: merge %s continues on the host and records its outcome in the event log: %w", state.GetPullRequestUrl(), context.Cause(ctx))
	}
}

// mergeCommand is the merge path for the session's worktree.
func (service *AgentSessionService) mergeCommand(state *harnessv1.AgentSessionState) (proc.Command, error) {
	url := state.GetPullRequestUrl()
	if _, err := os.Stat(filepath.Join(state.GetWorktree(), MergeScript)); err != nil {
		return proc.Command{Executable: programGitHub, Arguments: []string{ghPullRequest, ghMerge, url, ghSquash}, Directory: state.GetWorktree()}, nil
	}
	_, number, found := strings.Cut(url, pullRequestPathMark)
	number = path.Base(number)
	if !found || number == "" {
		return proc.Command{}, fmt.Errorf("%w: %q names no pull request number", csf.ErrInvalidRequest, url)
	}
	stateDirectory := service.runner.StateDirectory()
	return proc.Command{
		Executable: programBash,
		Arguments:  []string{MergeScript, number},
		Directory:  state.GetWorktree(),
		ExtraEnvironment: []string{
			BazelOutputVariable + "=" + filepath.Join(stateDirectory, MergeBazelDirectory),
			BazelDiskCacheVariable + "=" + service.bazelDiskCache(),
			OCamlToolchainCacheVariable + "=" + filepath.Join(stateDirectory, OCamlToolchainCacheDirectory),
		},
	}, nil
}

// pullRequestOf is the session's state when it has a pull request and the
// host granted a launcher to act on it.
func (service *AgentSessionService) pullRequestOf(ctx context.Context, assignment string) (*harnessv1.AgentSessionState, error) {
	if service.launcher == nil {
		return nil, ErrNoLauncher
	}
	var state *harnessv1.AgentSessionState
	err := service.command(ctx, func(table *registry) error {
		record, err := table.lookup(assignment)
		if err != nil {
			return err
		}
		state = proto.Clone(record.state).(*harnessv1.AgentSessionState)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if state.GetPullRequestUrl() == "" {
		return nil, fmt.Errorf("%w: %s", ErrNoPullRequest, assignment)
	}
	return state, nil
}

// recordAction appends one control action record to the session's event log,
// the way a gate appends its decision: one line, written whole. A run with no
// run record yet has no log to write to, and a record that cannot be written
// is logged; neither changes the operation's outcome.
func (service *AgentSessionService) recordAction(ctx context.Context, assignment string, action string, failure error, attributes ...slog.Attr) {
	directory := session.RunDirectory(service.runner.StateDirectory(), assignment)
	state, err := session.ReadRunState(directory)
	if err != nil {
		return
	}
	log, err := session.OpenEventLog(directory, session.RunTrace(state))
	if err != nil {
		service.logger.Warn("harness: control action not recorded", "assignment", assignment, "action", action, "error", err)
		return
	}
	defer func() { _ = log.Close() }()
	attributes = append(attributes, slog.String(session.KeyAction, action))
	message := "control action: " + action
	if failure != nil {
		log.Failure(ctx, state.SessionID, state.Turns, session.EventTypeControlAction, message, failure, attributes...)
		return
	}
	log.Record(ctx, state.SessionID, state.Turns, session.EventTypeControlAction, message, attributes...)
}
