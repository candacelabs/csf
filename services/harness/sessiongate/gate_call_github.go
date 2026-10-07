// Copyright 2026 Candace Labs

package sessiongate

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"mvdan.cc/sh/v3/syntax"

	"github.com/candacelabs/csf/csf/githubtools"
	"github.com/candacelabs/csf/services/harness/session"
)

const (
	// GateGitHub is the PreToolUse gate on Bash that refuses a gh command
	// reaching GitHub's issues, pull requests or API: a session reaches
	// GitHub through CSF's typed GitHub tools, which record every call.
	GateGitHub = "github"
	// RuleGitHubShell is a gh pr, gh issue or gh api command.
	RuleGitHubShell Rule = "github_shell"

	ghNounAPI     = "api"
	mcpToolPrefix = "mcp__" + session.MCPServerName + "__"
	toolList      = "csf github -list"
)

// ghReplacements is the tool that replaces each gh noun and verb.
var ghReplacements = map[string]map[string]string{
	ghNounPR: {
		ghCreate: githubtools.ToolPullsCreate, ghVerbReady: githubtools.ToolMarkPullRequestReady, ghMerge: githubtools.ToolPullsMerge,
		ghList: githubtools.ToolPullsList, ghVerbView: githubtools.ToolPullsGet, ghVerbChecks: githubtools.ToolChecksListForRef,
		ghVerbEdit: githubtools.ToolPullsUpdate, ghVerbClose: githubtools.ToolPullsUpdate, ghVerbComment: githubtools.ToolIssuesCreateComment,
	},
	ghNounIssue: {
		ghCreate: githubtools.ToolIssuesCreate, ghVerbComment: githubtools.ToolIssuesCreateComment, ghVerbEdit: githubtools.ToolIssuesUpdate,
		ghVerbClose: githubtools.ToolIssuesUpdate, ghVerbReopen: githubtools.ToolIssuesUpdate, ghList: githubtools.ToolIssuesListForRepo,
		ghVerbView: githubtools.ToolIssuesGet,
	},
}

const (
	ghList        = "list"
	ghCreate      = "create"
	ghMerge       = "merge"
	ghVerbView    = "view"
	ghVerbChecks  = "checks"
	ghVerbClose   = "close"
	ghVerbReopen  = "reopen"
	ghVerbComment = "comment"
)

// GitHubFinding is one gh command a session may not run, and the tool that
// replaces it.
type GitHubFinding struct {
	Snippet string
	// Tool is the replacing tool's name; empty when no one tool replaces
	// the command, as for gh api.
	Tool string
}

// Message is the refusal the agent reads.
func (finding GitHubFinding) Message() string {
	replacement := "the CSF GitHub tools (" + toolList + " names them; each is the MCP tool " + mcpToolPrefix + "<name>)"
	if finding.Tool != "" {
		replacement = fmt.Sprintf("the MCP tool %s%s (csf github %s from a script)", mcpToolPrefix, finding.Tool, finding.Tool)
	}
	return fmt.Sprintf("%s: %q is refused in a CSF session; GitHub is reached through CSF's typed GitHub tools, which record every call. Use %s", RuleGitHubShell, finding.Snippet, replacement)
}

// FindGitHubCommands parses command as bash and returns every gh pr, gh
// issue and gh api command in it, including inside sh -c, in source order.
func FindGitHubCommands(command string) ([]GitHubFinding, error) {
	file, err := parse(command)
	if err != nil {
		return nil, err
	}
	var findings []GitHubFinding
	syntax.Walk(file, func(node syntax.Node) bool {
		call, ok := node.(*syntax.CallExpr)
		if !ok {
			return true
		}
		name, arguments := commandWords(call)
		switch {
		case name == commandGh && len(arguments) > 0 && (arguments[0] == ghNounAPI || ghReplacements[arguments[0]] != nil):
			finding := GitHubFinding{Snippet: snippetOf(command, call)}
			if len(arguments) > 1 {
				finding.Tool = ghReplacements[arguments[0]][arguments[1]]
			}
			findings = append(findings, finding)
		case shells[name]:
			if script, ok := shellScript(arguments); ok {
				nested, _ := FindGitHubCommands(script)
				findings = append(findings, nested...)
			}
		}
		return true
	})
	return findings, nil
}

func snippetOf(source string, node syntax.Node) string {
	start, end := int(node.Pos().Offset()), int(node.End().Offset())
	if start < 0 || end > len(source) || start > end {
		return ""
	}
	return strings.TrimSpace(source[start:end])
}

// github is the PreToolUse gate on Bash that refuses gh commands reaching
// GitHub, naming the tool that replaces each.
func (call *gateCall) github(ctx context.Context) *HookOutput {
	if call.hook.ToolName != session.ToolBash {
		return nil
	}
	findings, err := FindGitHubCommands(call.hook.ToolInput.Command)
	if err != nil || len(findings) == 0 {
		return nil
	}
	messages := make([]string, 0, len(findings))
	for _, finding := range findings {
		messages = append(messages, finding.Message())
	}
	reason := "Rejected by the CSF session gate. " + strings.Join(messages, "; ")
	call.record(ctx, GateGitHub, DecisionDeny, slog.Any(keyRules, []string{string(RuleGitHubShell)}), slog.String(keyReason, reason))
	return &HookOutput{HookSpecificOutput: &HookSpecificOutput{
		HookEventName:            session.HookPreToolUse,
		PermissionDecision:       permissionDeny,
		PermissionDecisionReason: reason,
	}}
}
