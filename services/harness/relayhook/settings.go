// Copyright 2026 Candace Labs

package relayhook

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/candacelabs/csf/pkg/atomicfile"
)

// HookConfig describes one hook in Claude Code's settings.
type HookConfig struct {
	Name    string   `json:"name"`
	Event   string   `json:"event"`
	Command string   `json:"command"`
	Args    []string `json:"args,omitempty"`
}

// UserSettings is the structure of Claude Code's user settings.
type UserSettings struct {
	Hooks []HookConfig `json:"hooks,omitempty"`
}

// InstallRelayHook writes a relay hook into the operator's Claude Code settings.
// settingsPath should point to the user-level settings.json file.
func InstallRelayHook(settingsPath string, orchestrator string) error {
	if settingsPath == "" {
		return errors.New("settings path is required")
	}
	if orchestrator == "" {
		return errors.New("orchestrator ID is required")
	}

	// Ensure the directory exists.
	dir := filepath.Dir(settingsPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create settings directory: %w", err)
	}

	// Read existing settings or create empty ones.
	var settings UserSettings
	if content, err := os.ReadFile(settingsPath); err == nil {
		if err := json.Unmarshal(content, &settings); err != nil {
			return fmt.Errorf("decode settings: %w", err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("read settings: %w", err)
	}

	// Remove any existing relay hook to avoid duplicates.
	var newHooks []HookConfig
	for _, h := range settings.Hooks {
		if h.Name != "relay" || h.Event != "UserPromptSubmit" {
			newHooks = append(newHooks, h)
		}
	}

	// Add the new relay hook.
	newHooks = append(newHooks, HookConfig{
		Name:    "relay",
		Event:   "UserPromptSubmit",
		Command: "harness",
		Args:    []string{"gate", "UserPromptSubmit"},
	})

	settings.Hooks = newHooks

	// Marshal, then replace the settings file atomically through the one
	// primitive that writes beside the target, syncs and renames.
	content, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal settings: %w", err)
	}

	if err := atomicfile.WriteFile(settingsPath, content, 0o600); err != nil {
		return fmt.Errorf("install settings: %w", err)
	}

	return nil
}
