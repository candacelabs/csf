// Copyright 2026 Candace Labs

package sessiongate

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/net/github"
	"github.com/candacelabs/csf/services/harness/session"
)

// Arguments of the git children the commit gate starts.
const (
	gitExecutable = "git"
	gitDirectory  = "-C"
	gitRevList    = "rev-list"
	gitCount      = "--count"
	gitRange      = ".."
	gitHead       = "HEAD"
	gitPush       = "push"
	gitUpstream   = "--set-upstream"
	noCommits     = "0"
	// headSeparator joins a head branch to its owner, as GitHub filters
	// pull requests by head.
	headSeparator = ":"
	pathSeparator = "/"
)

// commit is the PostToolUse gate on Bash: after a command that ran git
// commit, the work branch is pushed and, if it has no open pull request on
// the repository it was pushed to, a draft one is opened there. In patch mode,
// the commit is not pushed; the agent must propose it via csf propose.
// Pushing an up-to-date branch and finding an existing pull request change
// nothing, so the gate is idempotent.
func (call *gateCall) commit(ctx context.Context) (*HookOutput, error) {
	if call.hook.ToolName != session.ToolBash {
		call.record(ctx, GateCommit, DecisionSkip)
		return nil, nil
	}
	committed, err := RunsGitCommit(call.hook.ToolInput.Command)
	if err != nil || !committed {
		call.record(ctx, GateCommit, DecisionSkip)
		return nil, nil
	}
	ahead, err := call.run(ctx, gitExecutable, gitDirectory, call.state.Worktree, gitRevList, gitCount, call.state.BaseBranch+gitRange+gitHead)
	if err != nil || ahead == noCommits {
		call.record(ctx, GateCommit, DecisionSkip, slog.String(keyReason, "no commits beyond the base branch"))
		return nil, nil
	}
	// In patch mode, the commit is not pushed; the agent must propose it.
	if call.state.WorkspaceMode == "patch" {
		call.record(ctx, GateCommit, DecisionSkip, slog.String(keyReason, "workspace mode is patch; use csf propose"))
		return call.told("Session gate: commits in patch mode must be proposed via csf propose."), nil
	}
	url, opened, err := PublishBranch(ctx, call.gate.launcher, call.gate.github, call.state)
	if err != nil {
		return call.failed(ctx, err)
	}
	if !opened {
		call.record(ctx, GateCommit, DecisionExists, slog.String(keyURL, url))
		return call.told(fmt.Sprintf("Session gate: pushed %s; its pull request is %s.", call.state.Branch, url)), nil
	}
	call.record(ctx, GateCommit, DecisionOpened, slog.String(keyURL, url))
	return call.told(fmt.Sprintf("Session gate: pushed %s and opened draft pull request %s.", call.state.Branch, url)), nil
}

// PublishBranch pushes a run's work branch and returns its open pull request
// on the repository it was pushed to, opening a draft one when it has none;
// opened reports which. It records the push on state. It is the harness's one
// way a session's work becomes a pull request: the commit gate calls it after
// a commit, and so does anything else that commits in a session's worktree.
// GitHub is reached through the protocol client, never a gh child.
func PublishBranch(ctx context.Context, launcher proc.ILauncher, client *github.GitHubClient, state *session.RunState) (url string, opened bool, err error) {
	if client == nil {
		return "", false, fmt.Errorf("%w: %w", ErrPullRequest, ErrNoGitHub)
	}
	if _, err := launcher.Run(ctx, proc.Command{
		Executable: gitExecutable, Arguments: []string{gitDirectory, state.Worktree, gitPush, gitUpstream, session.PushRemote, state.Branch}, Directory: state.Worktree,
	}); err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrPush, err)
	}
	state.LastPushTime = time.Now().Unix()
	// The pull request is opened on the repository the branch was just pushed
	// to, never on one a clone's upstream default would name.
	remote, err := session.PushRemoteURL(ctx, launcher, state.Worktree)
	if err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrPullRequest, err)
	}
	repository, err := github.ParseRemote(remote)
	if err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrPullRequest, err)
	}
	open := github.PullslistParamsStateOpen
	head := repository.Owner + headSeparator + state.Branch
	listed, err := client.PullslistWithResponse(ctx, repository.Owner, repository.Name, &github.PullslistParams{State: &open, Head: &head})
	if err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrPullRequest, err)
	}
	if listed.JSON200 == nil {
		return "", false, fmt.Errorf("%w: %w", ErrPullRequest, &github.StatusError{Status: listed.StatusCode(), Message: string(listed.Body)})
	}
	if pulls := *listed.JSON200; len(pulls) > 0 {
		return pulls[0].HtmlUrl, false, nil
	}
	draft := true
	title, body := state.PullRequestTitle, pullRequestBody(state)
	created, err := client.PullscreateWithResponse(ctx, repository.Owner, repository.Name, github.PullscreateJSONRequestBody{
		Title: &title, Body: &body, Head: state.Branch, Base: strings.TrimPrefix(state.BaseBranch, session.PushRemote+pathSeparator), Draft: &draft,
	})
	if err != nil {
		return "", false, fmt.Errorf("%w: %w", ErrPullRequest, err)
	}
	if created.JSON201 == nil {
		return "", false, fmt.Errorf("%w: %w", ErrPullRequest, &github.StatusError{Status: created.StatusCode(), Message: string(created.Body)})
	}
	return created.JSON201.HtmlUrl, true, nil
}

// failed logs a gate that could not finish and tells the agent what is left
// to do.
func (call *gateCall) failed(ctx context.Context, err error) (*HookOutput, error) {
	call.failure(ctx, GateCommit, DecisionFailed, err)
	return call.told(fmt.Sprintf("Session gate: %v. Push %s and open its draft pull request yourself.", err, call.state.Branch)), err
}

func (call *gateCall) told(text string) *HookOutput {
	return &HookOutput{HookSpecificOutput: &HookSpecificOutput{HookEventName: session.HookPostToolUse, AdditionalContext: text}}
}

// run starts one program in the worktree and returns its trimmed output.
func (call *gateCall) run(ctx context.Context, executable string, arguments ...string) (string, error) {
	result, err := call.gate.launcher.Run(ctx, proc.Command{Executable: executable, Arguments: arguments, Directory: call.state.Worktree})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(result.Stdout)), nil
}

func pullRequestBody(state *session.RunState) string {
	return fmt.Sprintf("Opened by the CSF harness session gate after the first commit.\n\n"+
		"| Link | Value |\n|---|---|\n| Agent | %s |\n| Assignment | %s |\n| Ticket | %s |\n| Session | %s |\n| Trace | %s |\n",
		state.AgentID, state.AssignmentID, state.TicketURL, state.SessionID, state.TraceID)
}
