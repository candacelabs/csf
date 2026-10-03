// Copyright 2026 Candace Labs

package sessiongate

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/services/harness/session"
)

// Arguments of the git and gh children the commit gate starts.
const (
	gitExecutable = "git"
	ghExecutable  = "gh"
	gitDirectory  = "-C"
	gitRevList    = "rev-list"
	gitCount      = "--count"
	gitRange      = ".."
	gitHead       = "HEAD"
	gitPush       = "push"
	gitUpstream   = "--set-upstream"
	ghPullRequest = "pr"
	ghList        = "list"
	ghCreate      = "create"
	ghRepo        = "--repo"
	ghDraft       = "--draft"
	ghHead        = "--head"
	ghBase        = "--base"
	ghTitle       = "--title"
	ghBody        = "--body"
	ghState       = "--state"
	ghStateOpen   = "open"
	ghJSON        = "--json"
	ghURLField    = "url"
	ghJQ          = "--jq"
	ghFirstURL    = ".[0].url"
	noCommits     = "0"
)

// commit is the PostToolUse gate on Bash: after a command that ran git
// commit, the work branch is pushed and, if it has no open pull request on
// the repository it was pushed to, a draft one is opened there. Pushing an
// up-to-date branch and finding an existing pull request change nothing, so
// the gate is idempotent.
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
	if _, err := call.run(ctx, gitExecutable, gitDirectory, call.state.Worktree, gitPush, gitUpstream, session.PushRemote, call.state.Branch); err != nil {
		return call.failed(ctx, fmt.Errorf("%w: %w", ErrPush, err))
	}
	call.state.LastPushTime = time.Now().Unix()
	// gh is told the repository the branch was just pushed to. Left to resolve
	// one from the worktree, gh takes the clone's own default, which in a clone
	// with an upstream remote is the upstream, where the branch does not exist.
	repository, err := session.PushRemoteURL(ctx, call.gate.launcher, call.state.Worktree)
	if err != nil {
		return call.failed(ctx, fmt.Errorf("%w: %w", ErrPullRequest, err))
	}
	existing, err := call.run(ctx, ghExecutable, ghPullRequest, ghList, ghRepo, repository, ghHead, call.state.Branch, ghState, ghStateOpen, ghJSON, ghURLField, ghJQ, ghFirstURL)
	if err != nil {
		return call.failed(ctx, fmt.Errorf("%w: %w", ErrPullRequest, err))
	}
	if existing != "" {
		call.record(ctx, GateCommit, DecisionExists, slog.String(keyURL, existing))
		return call.told(fmt.Sprintf("Session gate: pushed %s; its pull request is %s.", call.state.Branch, existing)), nil
	}
	created, err := call.run(ctx, ghExecutable, ghPullRequest, ghCreate, ghRepo, repository, ghDraft,
		ghBase, call.state.BaseBranch, ghHead, call.state.Branch,
		ghTitle, call.state.PullRequestTitle, ghBody, pullRequestBody(call.state))
	if err != nil {
		return call.failed(ctx, fmt.Errorf("%w: %w", ErrPullRequest, err))
	}
	url := lastLine(created)
	call.record(ctx, GateCommit, DecisionOpened, slog.String(keyURL, url))
	return call.told(fmt.Sprintf("Session gate: pushed %s and opened draft pull request %s.", call.state.Branch, url)), nil
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

func lastLine(text string) string {
	text = strings.TrimSpace(text)
	return strings.TrimSpace(text[strings.LastIndexByte(text, '\n')+1:])
}
