// Copyright 2026 Candace Labs

package sessiongate

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/candacelabs/csf/services/harness/session"
)

const (
	// GateMCP is the PreToolUse gate that refuses a tool served by any MCP
	// server but csf's own: csf is the only MCP server (#654), and every
	// other service is reached through a csf proxy, which records the call
	// and holds the credential.
	GateMCP = "mcp"
	// RuleForeignMCP is a call to a tool of an MCP server other than csf.
	RuleForeignMCP Rule = "foreign_mcp"

	// mcpPrefix starts every MCP tool name: mcp__<server>__<tool>.
	mcpPrefix = "mcp__"
)

// ForeignMCPServer names the MCP server a tool belongs to when it is not
// csf's own; ok is false for csf's tools and for every non-MCP tool.
func ForeignMCPServer(tool string) (server string, ok bool) {
	if !strings.HasPrefix(tool, mcpPrefix) || strings.HasPrefix(tool, mcpToolPrefix) {
		return "", false
	}
	server, _, _ = strings.Cut(strings.TrimPrefix(tool, mcpPrefix), "__")
	return server, true
}

// mcp refuses a tool of a foreign MCP server, naming csf's proxy instead.
func (call *gateCall) mcp(ctx context.Context) *HookOutput {
	server, foreign := ForeignMCPServer(call.hook.ToolName)
	if !foreign {
		return nil
	}
	reason := fmt.Sprintf("Rejected by the CSF session gate. %s: %s is served by the MCP server %q; csf is the only MCP server, and every other service is reached through it (%s<tool>; %s names the GitHub ones).",
		RuleForeignMCP, call.hook.ToolName, server, mcpToolPrefix, toolList)
	call.record(ctx, GateMCP, DecisionDeny, slog.Any(keyRules, []string{string(RuleForeignMCP)}), slog.String(keyReason, reason))
	return &HookOutput{HookSpecificOutput: &HookSpecificOutput{
		HookEventName:            session.HookPreToolUse,
		PermissionDecision:       permissionDeny,
		PermissionDecisionReason: reason,
	}}
}
