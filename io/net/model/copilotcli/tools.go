// Copyright 2026 Candace Labs

package copilotcli

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// ErrUntranslatableTool reports a Claude Code tool rule that has no Copilot
// equivalent. A recipe carrying one cannot run on Copilot: dropping the rule
// would narrow the session and widening it would break the allowlist.
var ErrUntranslatableTool = errors.New("copilotcli executor: the tool rule has no Copilot equivalent")

// The Claude Code tool names a recipe's rules use, and the separators of a
// rule with a pattern ("Bash(git add:*)") and of an MCP tool
// ("mcp__csf__ListAgentSessions").
const (
	claudeBash      = "Bash"
	claudeRead      = "Read"
	claudeWrite     = "Write"
	claudeEdit      = "Edit"
	claudeMultiEdit = "MultiEdit"
	claudeGlob      = "Glob"
	claudeGrep      = "Grep"

	claudeMCPPrefix    = "mcp__"
	claudeMCPSeparator = "__"
	patternOpen        = "("
	patternClose       = ")"
)

// Copilot's tools and permission kinds, as copilot 1.0.90 names them. A
// permission kind is what --allow-tool grants; Copilot approves reads by
// itself.
const (
	copilotBash  = "bash"
	copilotView  = "view"
	copilotEdit  = "edit"
	copilotWrite = "create"
	copilotGlob  = "glob"
	copilotGrep  = "grep"

	permissionShell = "shell"
	permissionWrite = "write"

	copilotMCPSeparator = "-"
)

// copilotShellTools are the tools a shell grant makes available: the shell
// and the tools that read, write to and stop a shell it started.
var copilotShellTools = []string{copilotBash, "read_bash", "write_bash", "stop_bash", "list_bash"}

// toolTranslation is one Claude Code tool and what it is on Copilot: the
// tools it makes available and the permission it grants. A tool with no
// permission is one Copilot runs unasked.
type toolTranslation struct {
	claude     string
	copilot    []string
	permission string
}

// toolTranslations are the Claude Code tools a Copilot session can be given.
var toolTranslations = []toolTranslation{
	{claude: claudeBash, copilot: copilotShellTools, permission: permissionShell},
	{claude: claudeRead, copilot: []string{copilotView}},
	{claude: claudeWrite, copilot: []string{copilotWrite}, permission: permissionWrite},
	{claude: claudeEdit, copilot: []string{copilotEdit}, permission: permissionWrite},
	{claude: claudeMultiEdit, copilot: []string{copilotEdit}, permission: permissionWrite},
	{claude: claudeGlob, copilot: []string{copilotGlob}},
	{claude: claudeGrep, copilot: []string{copilotGrep}},
}

// ToolGrants is a recipe's tool rules as Copilot takes them: the tools the
// model is shown (--available-tools) and the permissions it holds without
// asking (--allow-tool). Nothing else is available, and a permission not
// granted is denied, since nobody answers a prompt. AllPaths is set when the
// shell is granted: Copilot also denies a shell command naming a path outside
// the working directory, which Claude Code's shell rules never do, and a
// session that may run the shell can reach any path through it anyway.
type ToolGrants struct {
	Available []string
	Allowed   []string
	AllPaths  bool
}

// TranslateToolRules translates Claude Code tool rules into Copilot's grants:
// "Bash" grants the shell, "Bash(pattern)" the shell for commands matching
// the pattern, "mcp__server__tool" one MCP tool, and Read, Write, Edit,
// MultiEdit, Glob and Grep their Copilot tools. Any other rule is
// [ErrUntranslatableTool].
func TranslateToolRules(rules []string) (ToolGrants, error) {
	grants := ToolGrants{Available: []string{}, Allowed: []string{}}
	for _, rule := range rules {
		available, allowed, err := translateRule(rule)
		if err != nil {
			return ToolGrants{}, err
		}
		grants.Available = appendNew(grants.Available, available...)
		grants.Allowed = appendNew(grants.Allowed, allowed...)
		grants.AllPaths = grants.AllPaths || slices.Contains(available, copilotBash)
	}
	return grants, nil
}

func translateRule(rule string) (available []string, allowed []string, err error) {
	if server, tool, ok := mcpRule(rule); ok {
		return []string{server + copilotMCPSeparator + tool}, []string{server + patternOpen + tool + patternClose}, nil
	}
	name, pattern, patterned := strings.Cut(rule, patternOpen)
	if patterned && !strings.HasSuffix(pattern, patternClose) {
		return nil, nil, fmt.Errorf("%w: %q", ErrUntranslatableTool, rule)
	}
	index := slices.IndexFunc(toolTranslations, func(translation toolTranslation) bool { return translation.claude == name })
	if index < 0 {
		return nil, nil, fmt.Errorf("%w: %q", ErrUntranslatableTool, rule)
	}
	translation := toolTranslations[index]
	switch {
	case patterned && translation.permission != permissionShell:
		return nil, nil, fmt.Errorf("%w: %q (only the shell takes a pattern)", ErrUntranslatableTool, rule)
	case patterned:
		return translation.copilot, []string{permissionShell + patternOpen + pattern}, nil
	case translation.permission == "":
		return translation.copilot, nil, nil
	}
	return translation.copilot, []string{translation.permission}, nil
}

// mcpRule splits "mcp__server__tool" into its server and tool.
func mcpRule(rule string) (server string, tool string, ok bool) {
	rest, found := strings.CutPrefix(rule, claudeMCPPrefix)
	if !found {
		return "", "", false
	}
	server, tool, found = strings.Cut(rest, claudeMCPSeparator)
	if !found || server == "" || tool == "" {
		return "", "", false
	}
	return server, tool, true
}

// claudeToolName is the name the harness reads a Copilot tool call by: the
// Claude Code tool it translates, "mcp__server__tool" for an MCP tool, or the
// Copilot name itself for a tool Claude Code has no counterpart of.
func claudeToolName(copilot string) string {
	if server, tool, found := strings.Cut(copilot, copilotMCPSeparator); found {
		return claudeMCPPrefix + server + claudeMCPSeparator + tool
	}
	switch copilot {
	case copilotBash:
		return claudeBash
	case copilotView:
		return claudeRead
	case copilotWrite:
		return claudeWrite
	case copilotEdit:
		return claudeEdit
	case copilotGlob:
		return claudeGlob
	case copilotGrep:
		return claudeGrep
	}
	return copilot
}

// appendNew appends the values list does not hold yet, in order.
func appendNew(list []string, values ...string) []string {
	for _, value := range values {
		if !slices.Contains(list, value) {
			list = append(list, value)
		}
	}
	return list
}
