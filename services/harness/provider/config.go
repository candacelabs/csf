// Copyright 2026 Candace Labs

package provider

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The modes the configuration directory and its entries are created with.
const (
	configDirectoryMode os.FileMode = 0o700
	configFileMode      os.FileMode = 0o600
)

// CredentialsFile is the executor's own provider login. The harness-owned
// configuration directory must not carry one: a login overrides the API key,
// so a session would reach the subscription the host switched away from.
const CredentialsFile = ".credentials.json"

// ErrLoginPresent reports a configuration directory carrying the executor's
// own login, which would override the router's key.
var ErrLoginPresent = errors.New("harness provider: the executor configuration directory carries a login, which overrides the router's key")

// PrepareConfigDirectory creates the harness-owned executor configuration
// directory and links its projects/ to history, the real conversation history,
// so a conversation started before the switch resumes on the provider. It
// refuses a directory carrying the executor's own login, since that login
// would override the router's key and send the turn to the wrong provider.
//
// It is idempotent: an existing directory whose projects/ already points at
// history is left as it is, and a projects/ pointing elsewhere is relinked.
func (router *Router) PrepareConfigDirectory(history string) error {
	directory := router.ConfigDirectoryPath()
	if err := os.MkdirAll(directory, configDirectoryMode); err != nil {
		return fmt.Errorf("harness provider: create the executor configuration directory: %w", err)
	}
	_, err := os.Lstat(filepath.Join(directory, CredentialsFile))
	if err == nil {
		return fmt.Errorf("%w: %s", ErrLoginPresent, CredentialsFile)
	}
	if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("harness provider: check the executor configuration directory: %w", err)
	}
	return linkProjects(filepath.Join(directory, ProjectsDirectory), history)
}

// linkProjects points link at history, a symlink so the conversations the
// executor writes land in the real history and the ones already there resume.
// A link already pointing at history is kept; any other link is replaced. A
// real directory there is left alone: it holds conversations nobody asked the
// harness to move.
func linkProjects(link string, history string) error {
	if history == "" {
		return nil
	}
	info, err := os.Lstat(link)
	switch {
	case err == nil && info.Mode()&os.ModeSymlink != 0:
		target, readErr := os.Readlink(link)
		if readErr == nil && target == history {
			return nil
		}
		if err := os.Remove(link); err != nil {
			return fmt.Errorf("harness provider: replace the conversation history link: %w", err)
		}
	case err == nil:
		// A real directory of conversations: left as the operator left it.
		return nil
	case !errors.Is(err, os.ErrNotExist):
		return fmt.Errorf("harness provider: check the conversation history link: %w", err)
	}
	if err := os.MkdirAll(history, configDirectoryMode); err != nil {
		return fmt.Errorf("harness provider: create the conversation history: %w", err)
	}
	if err := os.Symlink(history, link); err != nil {
		return fmt.Errorf("harness provider: link the conversation history: %w", err)
	}
	return nil
}
