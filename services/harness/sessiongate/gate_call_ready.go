// Copyright 2026 Candace Labs

package sessiongate

import (
	"context"
	"strings"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/harness/session"
)

const (
	bashExecutable = "bash"
	// checkMergeScript is the consistency check tools/merge-pr.sh runs on
	// every merge, relative to the worktree.
	checkMergeScript = "tools/check-merge.sh"
	// reportLines is how much of the check's report the denial quotes: the
	// per-signal table and the refusal line sit at its end.
	reportLines = 40
	// The gh program and arguments the proof check reads a pull request's
	// body with.
	ghExecutable  = "gh"
	ghPullRequest = "pr"
	ghView        = "view"
	ghRepo        = "--repo"
	ghJSON        = "--json"
	ghJQ          = "--jq"
	ghBodyField   = "body"
	ghBodyJQ      = ".body"
)

// ready is the PreToolUse gate on the GitHub tool that marks a pull request
// ready for review: before it runs, the merge path's consistency checks run
// on the worktree, and a regression denies the call with the report's tail.
// The shell gh pr ready it once watched is refused by the GitHub gate.
func (call *gateCall) ready(ctx context.Context) *HookOutput {
	if call.hook.ToolName != session.ToolReadyPullRequest {
		return nil
	}
	if denied := call.citations(ctx); denied != nil {
		return denied
	}
	result, err := call.gate.launcher.Run(ctx, proc.Command{
		Executable: bashExecutable, Arguments: []string{checkMergeScript}, Directory: call.state.Worktree,
	})
	if err == nil {
		call.record(ctx, GateReady, DecisionAllow)
		return nil
	}
	reason := "Rejected by the CSF session gate: tools/check-merge.sh found a consistency regression, so this pull request " +
		"cannot be marked ready. Fix it, commit, and mark it ready again.\n" + tail(string(result.Stdout)+string(result.Stderr), reportLines)
	call.failure(ctx, GateReady, DecisionDeny, err)
	return &HookOutput{HookSpecificOutput: &HookSpecificOutput{
		HookEventName:            session.HookPreToolUse,
		PermissionDecision:       permissionDeny,
		PermissionDecisionReason: reason,
	}}
}

// citations is the proof check: the pull request's body is read from the
// repository the branch was pushed to, and an improvement it claims without
// a citation matching a recorded evaluation suite result denies the command.
// A body that cannot be read is not judged; check-merge still runs.
func (call *gateCall) citations(ctx context.Context) *HookOutput {
	if call.gate.citations == nil {
		return nil
	}
	repository, err := session.PushRemoteURL(ctx, call.gate.launcher, call.state.Worktree)
	if err != nil {
		return nil
	}
	result, err := call.gate.launcher.Run(ctx, proc.Command{
		Executable: ghExecutable, Arguments: []string{ghPullRequest, ghView, call.state.Branch, ghRepo, repository, ghJSON, ghBodyField, ghJQ, ghBodyJQ},
		Directory: call.state.Worktree,
	})
	if err != nil {
		return nil
	}
	failure := call.gate.citations(ctx, string(result.Stdout))
	if failure == nil {
		return nil
	}
	call.failure(ctx, GateReady, DecisionDeny, failure)
	return &HookOutput{HookSpecificOutput: &HookSpecificOutput{
		HookEventName:      session.HookPreToolUse,
		PermissionDecision: permissionDeny,
		PermissionDecisionReason: "Rejected by the CSF session gate: the pull request claims an improvement without an evaluation suite citation " +
			"that matches a recorded score, so it cannot be marked ready. Cite the line `csf eval show -build <sha12>` prints, " +
			"or say the claim is unscored and drop the improvement wording from the Verdict and Now lines.\n" + failure.Error(),
	}}
}

// tail is the last count lines of text.
func tail(text string, count int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "\n")
}
