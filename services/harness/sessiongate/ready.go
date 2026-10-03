// Copyright 2026 Candace Labs

package sessiongate

import (
	"context"
	"strings"

	"github.com/candacelabs/csf/ipc/proc"
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
)

// ready is the PreToolUse gate on Bash: before a command marks a pull
// request ready for review, the merge path's consistency checks run on the
// worktree, and a regression denies the command with the report's tail. A
// command that does not mark a pull request ready is not this gate's.
func (call *gateCall) ready(ctx context.Context) *HookOutput {
	if call.hook.ToolName != session.ToolBash {
		return nil
	}
	if marks, err := RunsPullRequestReady(call.hook.ToolInput.Command); err != nil || !marks {
		return nil
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

// tail is the last count lines of text.
func tail(text string, count int) string {
	lines := strings.Split(strings.TrimRight(text, "\n"), "\n")
	if len(lines) > count {
		lines = lines[len(lines)-count:]
	}
	return strings.Join(lines, "\n")
}
