// Copyright 2026 Candace Labs

package session

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"mvdan.cc/sh/v3/syntax"
)

// The Claude Code hook events the harness installs gates on, spelled as
// Claude Code's settings and hook input spell them.
const (
	HookPreToolUse  = "PreToolUse"
	HookPostToolUse = "PostToolUse"
	// ToolBash is the tool both gates watch.
	ToolBash = "Bash"

	hookTypeCommand = "command"
	// gateTimeoutSeconds bounds one PostToolUse gate call; the commit gate
	// pushes and opens a pull request, so it gets a network's worth of time.
	gateTimeoutSeconds = 120
	// readyGateTimeoutSeconds bounds one PreToolUse gate call. The wait gate
	// answers at once; the ready gate runs the merge path's checks (Bazel
	// builds, two ontology measurements, the full house lint) before a pull
	// request is marked ready.
	readyGateTimeoutSeconds = 3600
)

// gateTimeouts is each gated event's hook timeout.
var gateTimeouts = map[string]int{HookPreToolUse: readyGateTimeoutSeconds, HookPostToolUse: gateTimeoutSeconds}

// claudeSettings is the part of the Claude Code settings schema the harness
// writes: one command hook per gated event.
type claudeSettings struct {
	Hooks map[string][]hookMatcher `json:"hooks"`
}

type hookMatcher struct {
	Matcher string        `json:"matcher"`
	Hooks   []hookCommand `json:"hooks"`
}

type hookCommand struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout"`
}

// sessionSettings is the settings document for a run: every gated event
// calls gateCommand with the event name and the run directory appended.
func sessionSettings(gateCommand []string, runDirectory string) ([]byte, error) {
	settings := claudeSettings{Hooks: map[string][]hookMatcher{}}
	for _, event := range []string{HookPreToolUse, HookPostToolUse} {
		command, err := shellCommand(append(append([]string{}, gateCommand...), event, runDirectory))
		if err != nil {
			return nil, err
		}
		settings.Hooks[event] = []hookMatcher{{
			Matcher: ToolBash,
			Hooks:   []hookCommand{{Type: hookTypeCommand, Command: command, Timeout: gateTimeouts[event]}},
		}}
	}
	content, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("harness session: encode settings: %w", err)
	}
	return content, nil
}

// shellCommand quotes an argument vector into the one command line a hook
// setting holds; Claude Code runs it through a shell.
func shellCommand(arguments []string) (string, error) {
	quoted := make([]string, 0, len(arguments))
	for _, argument := range arguments {
		word, err := syntax.Quote(argument, syntax.LangBash)
		if err != nil {
			return "", fmt.Errorf("harness session: quote gate argument %q: %w", argument, err)
		}
		quoted = append(quoted, word)
	}
	return strings.Join(quoted, " "), nil
}

// writeSettings writes the run's settings file and returns its path.
func writeSettings(runDirectory string, gateCommand []string) (string, error) {
	content, err := sessionSettings(gateCommand, runDirectory)
	if err != nil {
		return "", err
	}
	path := filepath.Join(runDirectory, SettingsFile)
	return path, replaceFile(path, content)
}
