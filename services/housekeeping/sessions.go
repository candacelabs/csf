// Copyright 2026 Candace Labs

package housekeeping

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"strings"

	"github.com/candacelabs/csf/ipc/proc"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	cronservice "github.com/candacelabs/csf/services/cron"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/harness/session"
)

// Pull request states after which a session's branch is deleted.
const (
	pullRequestMerged = "MERGED"
	pullRequestClosed = "CLOSED"
)

// recordedRunFiles are a run directory's records: they stay.
var recordedRunFiles = map[string]bool{
	session.RunStateFile:         true,
	session.SettingsFile:         true,
	session.EventsFile:           true,
	session.WorktreeDirectory:    true,
	harness.BazelOutputDirectory: true,
}

// ended reports a session the harness records as finished.
func ended(phase harnessv1.AgentSessionPhase) bool {
	switch phase {
	case harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED,
		harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CLOSED,
		harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED:
		return true
	}
	return false
}

// census is the harness's sessions split by whether they ended.
type census struct {
	hostPID  int
	ended    []*harnessv1.AgentSessionState
	active   []*harnessv1.AgentSessionState
	recorded map[string]bool
}

func (housekeeper *Housekeeper) census(ctx context.Context) (census, error) {
	response, err := housekeeper.sessions(ctx)
	if err != nil {
		return census{}, fmt.Errorf("housekeeping: list sessions: %w", err)
	}
	result := census{hostPID: int(response.GetHostPid()), recorded: map[string]bool{}}
	for _, state := range response.GetSessions() {
		result.recorded[state.GetAssignmentId()] = true
		if ended(state.GetPhase()) {
			result.ended = append(result.ended, state)
			continue
		}
		result.active = append(result.active, state)
	}
	return result, nil
}

// Sessions reclaims what each ended session left: its leftover processes,
// its worktree, its Bazel output base and the branch of its merged or
// closed pull request. Everything else in the state directory, and every
// entry of a granted scratch directory, is reported as a finding.
func (housekeeper *Housekeeper) Sessions(ctx context.Context, occurrence cronservice.Occurrence) error {
	return housekeeper.reclaimSessions(ctx, passOf(occurrence))
}

func (housekeeper *Housekeeper) reclaimSessions(ctx context.Context, at pass) error {
	sessions, err := housekeeper.census(ctx)
	if err != nil {
		return err
	}
	table := housekeeper.readProcessTable()
	var failures []error
	for _, state := range sessions.ended {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := housekeeper.reclaimSession(ctx, at, state, sessions, table); err != nil {
			failures = append(failures, err)
		}
	}
	failures = append(failures, housekeeper.reportUnrecorded(ctx, at, sessions))
	return errors.Join(failures...)
}

func (housekeeper *Housekeeper) reclaimSession(ctx context.Context, at pass, state *harnessv1.AgentSessionState, sessions census, table processTable) error {
	id := state.GetAssignmentId()
	runDirectory := session.RunDirectory(housekeeper.stateDirectory, id)
	err := housekeeper.reapProcesses(ctx, at, id, runDirectory, sessions, table)
	survivors := table.holding(runDirectory)
	if len(survivors) > 0 {
		return errors.Join(err, housekeeper.finding(ctx, at, runDirectory,
			fmt.Sprintf("ended session %s, but process %d still works in it", id, survivors[0])))
	}
	worktreeRemoved, worktreeErr := housekeeper.reclaimWorktree(ctx, at, state, runDirectory)
	outputErr := housekeeper.reclaimOutputBase(ctx, at, id, runDirectory)
	var branchErr error
	if worktreeRemoved {
		branchErr = housekeeper.reclaimBranch(ctx, at, state, sessions)
	}
	return errors.Join(err, worktreeErr, outputErr, branchErr, housekeeper.reportRunExtras(ctx, at, id, runDirectory))
}

// reclaimWorktree removes the worktree the harness created for the session,
// unless it holds work that exists nowhere else.
func (housekeeper *Housekeeper) reclaimWorktree(ctx context.Context, at pass, state *harnessv1.AgentSessionState, runDirectory string) (bool, error) {
	id := state.GetAssignmentId()
	worktree := filepath.Join(runDirectory, session.WorktreeDirectory)
	if !housekeeper.exists(path.Join(id, session.WorktreeDirectory)) {
		return true, nil
	}
	if recorded := state.GetWorktree(); recorded != worktree {
		return false, housekeeper.finding(ctx, at, worktree, fmt.Sprintf("the session records its worktree as %q, not this path", recorded))
	}
	run, err := housekeeper.readRun(id)
	if err != nil {
		return false, housekeeper.finding(ctx, at, worktree, err.Error())
	}
	if reason := housekeeper.unsavedWork(ctx, worktree); reason != "" {
		return false, housekeeper.finding(ctx, at, worktree, reason)
	}
	err = housekeeper.delete(ctx, at, Record{Kind: KindWorktree, What: worktree, Session: id, Bytes: housekeeper.pathBytes(ctx, worktree)},
		func(ctx context.Context) (uint64, error) {
			size := housekeeper.pathBytes(ctx, worktree)
			if _, err := housekeeper.launcher.Run(ctx, proc.Command{
				Executable: programGit,
				Arguments:  []string{"-C", run.Repository, "worktree", "remove", "--force", worktree},
			}); err != nil {
				if removeErr := housekeeper.removeTree(ctx, worktree); removeErr != nil {
					return 0, errors.Join(err, removeErr)
				}
				_, _ = housekeeper.launcher.Run(ctx, proc.Command{Executable: programGit, Arguments: []string{"-C", run.Repository, "worktree", "prune"}})
			}
			return size, nil
		})
	return err == nil, err
}

// unsavedWork names work in the worktree that no remote holds: uncommitted
// changes, or commits on no remote branch.
func (housekeeper *Housekeeper) unsavedWork(ctx context.Context, worktree string) string {
	status, err := housekeeper.launcher.Run(ctx, proc.Command{Executable: programGit, Arguments: []string{"-C", worktree, "status", "--porcelain"}})
	if err != nil {
		return fmt.Sprintf("git status failed, so the worktree is kept: %v", err)
	}
	if strings.TrimSpace(string(status.Stdout)) != "" {
		return "the worktree has uncommitted changes"
	}
	unpushed, err := housekeeper.launcher.Run(ctx, proc.Command{
		Executable: programGit,
		Arguments:  []string{"-C", worktree, "log", "--oneline", "HEAD", "--not", "--remotes"},
	})
	if err != nil {
		return fmt.Sprintf("git log failed, so the worktree is kept: %v", err)
	}
	if strings.TrimSpace(string(unpushed.Stdout)) != "" {
		return "the worktree has commits on no remote branch"
	}
	return ""
}

// reclaimOutputBase removes the session's Bazel output base.
func (housekeeper *Housekeeper) reclaimOutputBase(ctx context.Context, at pass, id string, runDirectory string) error {
	if !housekeeper.exists(path.Join(id, harness.BazelOutputDirectory)) {
		return nil
	}
	outputBase := filepath.Join(runDirectory, harness.BazelOutputDirectory)
	return housekeeper.delete(ctx, at, Record{Kind: KindBazelOutputBase, What: outputBase, Session: id, Bytes: housekeeper.pathBytes(ctx, outputBase)},
		func(ctx context.Context) (uint64, error) {
			size := housekeeper.pathBytes(ctx, outputBase)
			return size, housekeeper.removeTree(ctx, outputBase)
		})
}

// reclaimBranch deletes the session's local branch once its pull request
// is merged or closed and no running session works on it.
func (housekeeper *Housekeeper) reclaimBranch(ctx context.Context, at pass, state *harnessv1.AgentSessionState, sessions census) error {
	branch, url := state.GetBranch(), state.GetPullRequestUrl()
	if branch == "" || url == "" {
		return nil
	}
	for _, active := range sessions.active {
		if active.GetBranch() == branch {
			return nil
		}
	}
	view, err := housekeeper.launcher.Run(ctx, proc.Command{Executable: programGitHub, Arguments: []string{"pr", "view", url, "--json", "state", "--jq", ".state"}})
	if err != nil {
		return fmt.Errorf("housekeeping: pull request %s: %w", url, err)
	}
	if prState := strings.TrimSpace(string(view.Stdout)); prState != pullRequestMerged && prState != pullRequestClosed {
		return nil
	}
	run, err := housekeeper.readRun(state.GetAssignmentId())
	if err != nil {
		return err
	}
	exists, _ := housekeeper.launcher.Run(ctx, proc.Command{Executable: programGit, Arguments: []string{"-C", run.Repository, "branch", "--list", branch}})
	if strings.TrimSpace(string(exists.Stdout)) == "" {
		return nil
	}
	return housekeeper.delete(ctx, at, Record{Kind: KindBranch, What: branch, Session: state.GetAssignmentId(), Detail: url},
		func(ctx context.Context) (uint64, error) {
			_, err := housekeeper.launcher.Run(ctx, proc.Command{Executable: programGit, Arguments: []string{"-C", run.Repository, "branch", "-D", branch}})
			return 0, err
		})
}

// reportRunExtras reports whatever an ended run directory holds beyond its
// records, its worktree and its output base: nothing records it as the
// session's, so it stays.
func (housekeeper *Housekeeper) reportRunExtras(ctx context.Context, at pass, id string, runDirectory string) error {
	entries, err := housekeeper.state.ReadDir(id)
	if err != nil {
		return nil
	}
	var failures []error
	for _, entry := range entries {
		if recordedRunFiles[entry.Name()] {
			continue
		}
		failures = append(failures, housekeeper.finding(ctx, at, filepath.Join(runDirectory, entry.Name()),
			fmt.Sprintf("in ended session %s's run directory, but not recorded as created by it", id)))
	}
	return errors.Join(failures...)
}

// reportUnrecorded reports the state directory's run directories this
// harness holds no session for, and every entry of the scratch directory.
func (housekeeper *Housekeeper) reportUnrecorded(ctx context.Context, at pass, sessions census) error {
	entries, err := housekeeper.state.ReadDir(".")
	if err != nil {
		return fmt.Errorf("housekeeping: list the state directory: %w", err)
	}
	var failures []error
	for _, entry := range entries {
		if !entry.IsDir() || entry.Name() == harness.BazelDiskCacheDirectory || sessions.recorded[entry.Name()] {
			continue
		}
		failures = append(failures, housekeeper.finding(ctx, at, filepath.Join(housekeeper.stateDirectory, entry.Name()),
			"a run directory this harness holds no session for"))
	}
	if housekeeper.scratch == nil {
		return errors.Join(failures...)
	}
	scratch, err := housekeeper.scratch.ReadDir(".")
	if err != nil {
		return errors.Join(append(failures, fmt.Errorf("housekeeping: list the scratch directory: %w", err))...)
	}
	for _, entry := range scratch {
		failures = append(failures, housekeeper.finding(ctx, at, filepath.Join(housekeeper.scratchDirectory, entry.Name()),
			"scratch no harness session records"))
	}
	return errors.Join(failures...)
}

func (housekeeper *Housekeeper) exists(name string) bool {
	_, err := housekeeper.state.ReadDir(name)
	return err == nil
}

func (housekeeper *Housekeeper) readRun(id string) (*session.RunState, error) {
	content, err := housekeeper.state.ReadFile(path.Join(id, session.RunStateFile))
	if err != nil {
		return nil, fmt.Errorf("housekeeping: read session %s's run: %w", id, err)
	}
	run := &session.RunState{}
	if err := json.Unmarshal(content, run); err != nil {
		return nil, fmt.Errorf("housekeeping: decode session %s's run: %w", id, err)
	}
	if run.Repository == "" {
		return nil, fmt.Errorf("housekeeping: session %s's run records no repository", id)
	}
	return run, nil
}
