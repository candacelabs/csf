// Copyright 2026 Candace Labs

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/candacelabs/csf/services/harness/relayhook"
)

const (
	verbRelay            = "relay"
	verbRelayInstall     = "install"
	orchestratorFlag     = "assignment"
	settingsPathFlag     = "settings"
	claudeConfigDirFn    = ".claude"
	relayHarnessPort     = "14120"
	relayEndpointEnv     = "HARNESS_ENDPOINT"
	relayOrchestratorEnv = "RELAY_ORCHESTRATOR"
)

func relay(arguments []string, output io.Writer) error {
	if len(arguments) == 0 {
		return fmt.Errorf("relay: choose %s or another subcommand", verbRelayInstall)
	}

	subcommand, rest := arguments[0], arguments[1:]

	switch subcommand {
	case verbRelayInstall:
		return relayInstall(rest, output)
	}

	return fmt.Errorf("relay: unknown subcommand %q", subcommand)
}

func relayGate(ctx context.Context, input io.Reader, output io.Writer, diagnostics io.Writer) int {
	hookInput, err := io.ReadAll(io.LimitReader(input, 64<<10))
	if err != nil {
		fmt.Fprintf(diagnostics, "harness gate: read hook input: %v\n", err)
		return exitBlocked
	}

	// Get orchestrator from environment if set.
	orchestrator := os.Getenv(relayOrchestratorEnv)

	// Get harness endpoint from environment or use default.
	endpoint := os.Getenv(relayEndpointEnv)
	if endpoint == "" {
		endpoint = "http://127.0.0.1:" + relayHarnessPort
	}

	// Create the relay hook.
	var sender relayhook.ISender
	if orchestrator != "" {
		s, err := relayhook.NewClientSender(endpoint, 30*time.Second)
		if err != nil {
			fmt.Fprintf(diagnostics, "harness gate: create sender: %v\n", err)
			return exitBlocked
		}
		sender = s
	} else {
		// If no orchestrator is configured, just return a deny without forwarding.
		// This allows testing the hook infrastructure without a full setup.
		sender = &noopSender{}
	}

	hook, err := relayhook.New(
		relayhook.WithOrchestrator(orchestrator),
		relayhook.WithSender(sender),
	)
	if err != nil {
		fmt.Fprintf(diagnostics, "harness gate: create hook: %v\n", err)
		// If we can't create the hook, still return a deny to block the prompt.
		decision := &relayhook.HookOutput{
			HookSpecificOutput: &relayhook.HookSpecificOutput{
				HookEventName:      "UserPromptSubmit",
				PermissionDecision: "deny",
				AdditionalContext:  fmt.Sprintf("relay hook error: %v", err),
			},
		}
		content, _ := json.Marshal(decision)
		fmt.Fprintln(output, string(content))
		return 0
	}

	// Handle the hook.
	decision, err := hook.Handle(hookInput)
	if err != nil {
		fmt.Fprintf(diagnostics, "harness gate: handle: %v\n", err)
		return exitBlocked
	}

	if decision != nil {
		content, err := json.Marshal(decision)
		if err != nil {
			fmt.Fprintf(diagnostics, "harness gate: marshal decision: %v\n", err)
			return exitBlocked
		}
		fmt.Fprintln(output, string(content))
	}

	return 0
}

// noopSender is a sender that does nothing - used when no orchestrator is configured.
type noopSender struct{}

func (n *noopSender) Send(orchestratorID string, message string) (string, error) {
	// Just return empty string - the hook will block the prompt anyway.
	return "", nil
}

func (n *noopSender) Get(orchestratorID string) (string, error) {
	return "", nil
}

func (n *noopSender) Cancel(orchestratorID string) error {
	return nil
}

func relayInstall(arguments []string, output io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbRelay+" "+verbRelayInstall, flag.ContinueOnError)
	orchestrator := flags.String(orchestratorFlag, "", "orchestrator assignment ID")
	settingsPath := flags.String(settingsPathFlag, "", "Claude Code settings.json path (default: user-level)")

	if err := flags.Parse(arguments); err != nil {
		return err
	}

	if flags.NArg() != 0 {
		return errUsage
	}

	if *orchestrator == "" {
		return fmt.Errorf("-assignment is required")
	}

	// If no settings path is given, use the user-level Claude Code settings.
	if *settingsPath == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("determine home directory: %w", err)
		}
		*settingsPath = filepath.Join(home, claudeConfigDirFn, "settings.json")
	}

	if err := relayhook.InstallRelayHook(*settingsPath, *orchestrator); err != nil {
		return fmt.Errorf("install relay hook: %w", err)
	}

	fmt.Fprintf(output, "relay hook installed: %s\n", *settingsPath)
	fmt.Fprintf(output, "orchestrator assignment: %s\n", *orchestrator)

	return nil
}
