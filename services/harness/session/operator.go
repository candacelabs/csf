// Copyright 2026 Candace Labs

package session

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Where each executor reads an operator's own hooks, under the home
// directory: Claude Code's user settings, which the harness merges its hook
// into, and a hooks file of Copilot's own, which the harness owns whole.
const (
	claudeUserSettings  = ".claude/settings.json"
	copilotUserHooks    = ".copilot/hooks/csf-gates.json"
	userDirectoryMode   = 0o700
	keyHooks            = "hooks"
	settingsIndent      = "  "
	operatorGateTimeout = gateTimeoutSeconds
)

// ErrUserSettings reports a user settings file the harness cannot merge its
// hook into: not a JSON object, or hooks not an object of event arrays.
var ErrUserSettings = errors.New("harness session: the user settings cannot take the gate hook")

// operatorMatcher is the one hook an operator's own session gets: the gate
// on every Bash call, gateCommand with the event and home appended. home
// holds no run, so the gate answers in operator mode, with the gates that
// mode runs.
func operatorMatcher(gateCommand []string, home string) (hookMatcher, error) {
	command, err := shellCommand(append(append([]string{}, gateCommand...), HookPreToolUse, home))
	if err != nil {
		return hookMatcher{}, err
	}
	return hookMatcher{Matcher: ToolBash, Hooks: []hookCommand{{Type: hookTypeCommand, Command: command, Timeout: operatorGateTimeout}}}, nil
}

// InstallOperatorGates hooks the operator's own Claude Code and Copilot
// sessions under home up to the wait gate, so a shell command that waits on
// nothing is refused there as it is in a harness session. Claude Code's user
// settings keep every other setting and hook, and a second install changes
// nothing; Copilot's hooks file is the harness's own. It returns the files it
// wrote.
func InstallOperatorGates(home string, gateCommand []string) ([]string, error) {
	if !filepath.IsAbs(home) {
		return nil, fmt.Errorf("%w: home %q is not absolute", ErrInvalidOption, home)
	}
	if len(gateCommand) == 0 || gateCommand[0] == "" {
		return nil, ErrNoGateCommand
	}
	matcher, err := operatorMatcher(gateCommand, home)
	if err != nil {
		return nil, err
	}
	claude := filepath.Join(home, claudeUserSettings)
	if err := mergeClaudeHook(claude, matcher); err != nil {
		return nil, err
	}
	copilot := filepath.Join(home, copilotUserHooks)
	hooks, err := json.MarshalIndent(claudeSettings{Hooks: map[string][]hookMatcher{HookPreToolUse: {matcher}}}, "", settingsIndent)
	if err != nil {
		return nil, err
	}
	if err := writeUserFile(copilot, hooks); err != nil {
		return nil, err
	}
	return []string{claude, copilot}, nil
}

// mergeClaudeHook adds matcher to the PreToolUse hooks of the settings at
// path, unless an entry running the same command is there already.
func mergeClaudeHook(path string, matcher hookMatcher) error {
	// The settings are Claude Code's whole user configuration, of a shape the
	// harness does not own: every key but hooks is carried through untouched.
	settings := map[string]json.RawMessage{}
	content, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return fmt.Errorf("harness session: read %s: %w", path, err)
	default:
		if err := json.Unmarshal(content, &settings); err != nil {
			return fmt.Errorf("%w: %s: %w", ErrUserSettings, path, err)
		}
	}
	hooks := map[string][]json.RawMessage{}
	if raw, found := settings[keyHooks]; found {
		if err := json.Unmarshal(raw, &hooks); err != nil {
			return fmt.Errorf("%w: %s: hooks: %w", ErrUserSettings, path, err)
		}
	}
	command, err := json.Marshal(matcher.Hooks[0].Command)
	if err != nil {
		return err
	}
	for _, entry := range hooks[HookPreToolUse] {
		if bytes.Contains(entry, command) {
			return nil
		}
	}
	entry, err := json.Marshal(matcher)
	if err != nil {
		return err
	}
	hooks[HookPreToolUse] = append(hooks[HookPreToolUse], entry)
	if settings[keyHooks], err = json.Marshal(hooks); err != nil {
		return err
	}
	merged, err := json.MarshalIndent(settings, "", settingsIndent)
	if err != nil {
		return err
	}
	return writeUserFile(path, merged)
}

// writeUserFile replaces path with content, creating its directory.
func writeUserFile(path string, content []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), userDirectoryMode); err != nil {
		return fmt.Errorf("harness session: create %s: %w", filepath.Dir(path), err)
	}
	return replaceFile(path, append(content, '\n'))
}
