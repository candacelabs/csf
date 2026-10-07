// Copyright 2026 Candace Labs

package main

import (
	"context"
	"os"
	"path/filepath"

	"github.com/candacelabs/csf/csf/githubtools"
	"github.com/candacelabs/csf/csf/prod"
	"github.com/candacelabs/csf/io/ipc/proc"
)

const (
	// The git worktree subcommands the golden measurer runs in the loop's
	// checkout: a detached worktree at one revision, removed once it is read.
	gitWorktree       = "worktree"
	gitWorktreeAdd    = "add"
	gitWorktreeDetach = "--detach"
	gitWorktreeRemove = "remove"
	gitWorktreeForce  = "--force"
	// goldenWorktreePrefix names the temporary parent each measured worktree is
	// created under; goldenWorktreeName is the worktree inside it.
	goldenWorktreePrefix = "csf-golden-"
	goldenWorktreeName   = "worktree"
)

// goldenRead reads one checked-out worktree into the reading the merge quality
// gate compares: the per-directory chief counts of the six chief rows
// (csf/prod). It is the seam [newGoldenMeasurer] reads through, so a test
// scripts a reading with no repository on disk.
type goldenRead func(ctx context.Context, worktree string) (prod.Reading, error)

// newGoldenMeasurer is the golden measurer the merge quality gate runs. It
// checks out one revision of the loop's checkout (the repository the host was
// given with -ouroboros-repository) into a detached worktree through the
// launcher, reads it, and removes the worktree, so no part of the measured
// revision is left on disk. A revision that cannot be checked out or read
// fails, and the merge fails closed.
func newGoldenMeasurer(launcher proc.ILauncher, repository string, read goldenRead) githubtools.GoldenMeasurer {
	return func(ctx context.Context, _, _, revision string) (prod.Reading, error) {
		parent, err := os.MkdirTemp("", goldenWorktreePrefix)
		if err != nil {
			return prod.Reading{}, err
		}
		defer os.RemoveAll(parent)
		worktree := filepath.Join(parent, goldenWorktreeName)
		if err := runWorktree(ctx, launcher, repository, gitWorktreeAdd, gitWorktreeDetach, worktree, revision); err != nil {
			return prod.Reading{}, err
		}
		// Remove even when the read fails or the context is canceled: the
		// measured revision may not outlive the measurement.
		defer runWorktree(context.WithoutCancel(ctx), launcher, repository, gitWorktreeRemove, gitWorktreeForce, worktree)
		return read(ctx, worktree)
	}
}

// runWorktree runs one git worktree subcommand in the repository through the
// launcher.
func runWorktree(ctx context.Context, launcher proc.ILauncher, repository string, arguments ...string) error {
	command := append([]string{gitDirectory, repository, gitWorktree}, arguments...)
	_, err := launcher.Run(ctx, proc.Command{Executable: gitExecutable, Arguments: command})
	return err
}
