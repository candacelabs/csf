// Copyright 2026 Candace Labs

package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/io/net/model/copilotcli"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
)

// Executor names the turn executor a session runs on, as a recipe's executor
// field spells it.
type Executor string

// The turn executors a recipe can choose. An empty executor field is Claude
// Code.
const (
	ExecutorClaudeCode Executor = "claude-code"
	ExecutorCopilot    Executor = "copilot"
)

// ExecutorOf is the turn executor the recipe chooses.
func ExecutorOf(recipe *pb.AgentAssignmentRecipe) Executor {
	if Executor(recipe.GetExecutor()) == ExecutorCopilot {
		return ExecutorCopilot
	}
	return ExecutorClaudeCode
}

// The files of a Copilot session under its run directory: the plugin holding
// the session gates, the agent's instructions and the usage file each turn
// writes.
const (
	copilotDirectory      = "copilot"
	copilotPlugin         = "plugin"
	copilotPluginManifest = ".claude-plugin/plugin.json"
	copilotPluginHooks    = "hooks/hooks.json"
	copilotInstructions   = "instructions"
	// copilotInstructionsFile is where Copilot reads instructions from a
	// directory named in COPILOT_CUSTOM_INSTRUCTIONS_DIRS: a path-scoped
	// instructions file applying to every path. Copilot ignores AGENTS.md and
	// .github/copilot-instructions.md in such a directory.
	copilotInstructionsFile = ".github/instructions/agent.instructions.md"
	copilotUsageFile        = "usage.json"

	copilotPluginName        = "csf-session-gates"
	copilotPluginDescription = "The CSF harness session gates"
	copilotPluginVersion     = "1.0.0"
	copilotInstructionsHead  = "---\napplyTo: \"**\"\n---\n"

	envCopilotInstructions = "COPILOT_CUSTOM_INSTRUCTIONS_DIRS"
)

// The Copilot arguments the runner adds to every turn.
const (
	flagCopilotModel          = "--model"
	flagCopilotPlugin         = "--plugin-dir"
	flagCopilotAvailableTools = "--available-tools"
	flagCopilotAllowTool      = "--allow-tool"
	flagCopilotMCPConfig      = "--additional-mcp-config"
	// flagCopilotNoAskUser removes the tool that asks the operator a
	// question: nobody answers one in a session.
	flagCopilotNoAskUser = "--no-ask-user"
	// flagCopilotNoBuiltinMCPs removes Copilot's own GitHub MCP servers: a
	// session has the tools its recipe allows and the host's MCP server.
	flagCopilotNoBuiltinMCPs = "--disable-builtin-mcps"
	// flagCopilotAllPaths turns off Copilot's own path verification, for a
	// session granted the shell; see copilotcli.ToolGrants.
	flagCopilotAllPaths = "--allow-all-paths"
	mcpAllTools         = "*"
)

// copilotMCPConfig is Copilot's --additional-mcp-config document: Claude
// Code's, with the tools each server exposes, which the recipe's allowlist
// then narrows.
type copilotMCPConfig struct {
	Servers map[string]copilotMCPServer `json:"mcpServers"`
}

type copilotMCPServer struct {
	Type    string            `json:"type"`
	URL     string            `json:"url"`
	Headers map[string]string `json:"headers,omitempty"`
	Tools   []string          `json:"tools"`
}

// copilotPluginManifest is the Claude-format plugin manifest Copilot loads
// from a --plugin-dir; its hooks file is the session settings' hooks, which
// Copilot reads in Claude Code's shape and calls with Claude Code's payloads.
type copilotPluginDocument struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Version     string `json:"version"`
}

// TurnExecutorsByRecipe builds the turn executor each recipe chooses: Claude
// Code from claude, or the Copilot CLI from copilot.
func TurnExecutorsByRecipe(launcher proc.ILauncher, claude string, copilot string) TurnExecutorFactory {
	claudeCode := ClaudeCodeTurnExecutors(launcher, claude)
	return func(spec TurnExecutorSpec) (ITurnExecutor, error) {
		if spec.Executor == ExecutorCopilot {
			return newCopilotTurnExecutor(launcher, copilot, spec)
		}
		return claudeCode(spec)
	}
}

// OpenTurnExecutorsByRecipe opens the turn executor each recipe chooses:
// Claude Code from claude, or the Copilot CLI from copilot.
func OpenTurnExecutorsByRecipe(launcher proc.ILauncher, claude string, copilot string) OpenTurnExecutorFactory {
	claudeCode := ClaudeCodeOpenTurnExecutors(launcher, claude)
	return func(ctx context.Context, spec TurnExecutorSpec) (IOpenTurnExecutor, error) {
		if spec.Executor == ExecutorCopilot {
			return newCopilotTurnExecutor(launcher, copilot, spec)
		}
		return claudeCode(ctx, spec)
	}
}

// copilotTurnExecutor adapts the Copilot CLI turn executor to the runner's
// turn shape: the runner hands every executor one stream-json user message,
// and Copilot takes the prompt it carries. Each turn is one Copilot process
// on the session, so the same executor serves a Run, a Resume and an open
// session.
// ErrCopilotInjectUnsupported means the Copilot CLI turn executor runs one
// process per turn and cannot take a message in the middle of one; queued
// messages arrive with the next turn instead.
var ErrCopilotInjectUnsupported = errors.New("session: the copilot turn executor cannot inject a message mid-turn")

type copilotTurnExecutor struct {
	executor *copilotcli.CopilotCLIExecutor
}

// Inject reports that a Copilot turn cannot take a message mid-turn.
func (executor *copilotTurnExecutor) Inject(ctx context.Context, message string) error {
	return ErrCopilotInjectUnsupported
}

func newCopilotTurnExecutor(launcher proc.ILauncher, executable string, spec TurnExecutorSpec) (*copilotTurnExecutor, error) {
	options := []copilotcli.CopilotCLIExecutorOption{
		copilotcli.WithExecutable(executable),
		copilotcli.WithDirectory(spec.Directory),
		copilotcli.WithArguments(spec.Arguments...),
		copilotcli.WithLogger(spec.Logger),
		copilotcli.WithEnvironment(spec.Environment...),
		copilotcli.WithUsageFile(filepath.Join(spec.RunDirectory, copilotDirectory, copilotUsageFile)),
	}
	if len(spec.LaunchPrefix) > 0 {
		options = append(options, copilotcli.WithLaunchPrefix(spec.LaunchPrefix...))
	}
	executor, err := copilotcli.NewCopilotCLIExecutor(launcher, spec.Session, options...)
	if err != nil {
		return nil, err
	}
	return &copilotTurnExecutor{executor: executor}, nil
}

// Propose runs the turn's prompt on Copilot and returns its events.
func (adapter *copilotTurnExecutor) Propose(ctx context.Context, turn *claudecode.Turn) (*model.Proposal[claudecode.Event], error) {
	prompt, err := promptOf(turn)
	if err != nil {
		return nil, err
	}
	proposal, err := adapter.executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})
	if err != nil {
		return nil, err
	}
	events := make([]claudecode.Event, 0, len(proposal.Actions))
	for _, record := range proposal.Actions {
		events = append(events, claudecode.Event{Type: record.Type, Raw: record.Raw})
	}
	return &model.Proposal[claudecode.Event]{Provider: proposal.Provider, Actions: events}, nil
}

// Interrupt stops the running turn's Copilot process: Copilot has no
// safepoint to end a turn at, so the turn ends failed.
func (adapter *copilotTurnExecutor) Interrupt(ctx context.Context) error {
	return adapter.executor.Interrupt(ctx)
}

// Close stops the running turn and refuses later ones.
func (adapter *copilotTurnExecutor) Close(ctx context.Context) error {
	return adapter.executor.Close(ctx)
}

// promptOf is the prompt the runner's single stream-json user message carries.
func promptOf(turn *claudecode.Turn) (string, error) {
	if turn == nil || len(turn.Messages) != 1 {
		return "", fmt.Errorf("%w: a Copilot turn takes exactly one user message", ErrNoPrompt)
	}
	var message struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	if err := json.Unmarshal(turn.Messages[0], &message); err != nil {
		return "", fmt.Errorf("harness session: decode prompt: %w", err)
	}
	return message.Message.Content, nil
}

// copilotLaunch writes a Copilot session's plugin and instructions under the
// run directory and returns the turn executor's extra arguments and
// environment: the model, the gates, the recipe's tools as Copilot's grants
// and the host's MCP server.
func (runner *AgentSessionRunner) copilotLaunch(plan *pb.AgentAssignmentPlan, runDirectory string) ([]string, []string, error) {
	recipe := plan.GetRecipe()
	grants, err := copilotcli.TranslateToolRules(recipe.GetWorkspace().GetAllowedTools())
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrInvalidRecipe, err)
	}
	plugin, instructions, err := writeCopilotFiles(runDirectory, runner.gateCommand, recipe.GetAgent().GetInstructions())
	if err != nil {
		return nil, nil, err
	}
	arguments := []string{flagCopilotModel, recipe.GetModel(), flagCopilotPlugin, plugin, flagCopilotNoAskUser, flagCopilotNoBuiltinMCPs}
	if runner.mcpURL != "" {
		config, err := json.Marshal(copilotMCPConfig{Servers: map[string]copilotMCPServer{
			MCPServerName: {Type: mcpTransportHTTP, URL: runner.mcpURL, Headers: map[string]string{AssignmentHeader: recipe.GetAssignmentId()}, Tools: []string{mcpAllTools}},
		}})
		if err != nil {
			return nil, nil, err
		}
		arguments = append(arguments, flagCopilotMCPConfig, string(config))
	}
	if grants.AllPaths {
		arguments = append(arguments, flagCopilotAllPaths)
	}
	arguments = append(append(arguments, flagCopilotAvailableTools), grants.Available...)
	if len(grants.Allowed) > 0 {
		arguments = append(append(arguments, flagCopilotAllowTool), grants.Allowed...)
	}
	return arguments, []string{envCopilotInstructions + "=" + instructions}, nil
}

// writeCopilotFiles writes the Copilot plugin whose hooks are the session
// gates, exactly as the Claude Code settings hold them, and the directory
// holding the agent's instructions. It returns both directories.
func writeCopilotFiles(runDirectory string, gateCommand []string, instructions string) (string, string, error) {
	hooks, err := sessionSettings(gateCommand, runDirectory)
	if err != nil {
		return "", "", err
	}
	manifest, err := json.MarshalIndent(copilotPluginDocument{
		Name: copilotPluginName, Description: copilotPluginDescription, Version: copilotPluginVersion,
	}, "", "  ")
	if err != nil {
		return "", "", fmt.Errorf("harness session: encode Copilot plugin: %w", err)
	}
	plugin := filepath.Join(runDirectory, copilotDirectory, copilotPlugin)
	instructionsDirectory := filepath.Join(runDirectory, copilotDirectory, copilotInstructions)
	files := []struct {
		path    string
		content []byte
	}{
		{path: filepath.Join(plugin, copilotPluginManifest), content: manifest},
		{path: filepath.Join(plugin, copilotPluginHooks), content: hooks},
		{path: filepath.Join(instructionsDirectory, copilotInstructionsFile), content: []byte(copilotInstructionsHead + instructions + "\n")},
	}
	for _, file := range files {
		if err := os.MkdirAll(filepath.Dir(file.path), runDirectoryMode); err != nil {
			return "", "", fmt.Errorf("harness session: create Copilot directory: %w", err)
		}
		if err := replaceFile(file.path, file.content); err != nil {
			return "", "", err
		}
	}
	return plugin, instructionsDirectory, nil
}
