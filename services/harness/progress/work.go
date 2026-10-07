// Copyright 2026 Candace Labs

package progress

import (
	"context"
	"strconv"
	"strings"

	"github.com/candacelabs/csf/io/ipc/proc"
)

// The git children [Work] starts. git is git's own vocabulary, not candace's.
const (
	gitExecutable = "git"
	gitDirectory  = "-C"
	gitRevList    = "rev-list"
	gitCount      = "--count"
	gitRange      = ".."
	gitHead       = "HEAD"
	gitStatus     = "status"
	gitPorcelain  = "--porcelain"
)

// Work fills the fields of a row that need the session's working tree rather
// than its run directory: the commits its branch is ahead of its base and the
// files it has changed but not committed. A session with no worktree yet, or
// one whose git commands fail, keeps zero counts, so status shows a session it
// cannot read rather than failing.
func Work(ctx context.Context, launcher proc.ILauncher, row *Progress) {
	if row == nil || row.worktree == "" || launcher == nil {
		return
	}
	row.CommitsAhead = commitsAhead(ctx, launcher, row.worktree, row.baseBranch)
	row.ChangedFiles = changedFiles(ctx, launcher, row.worktree)
}

// commitsAhead is how many commits the worktree's branch is ahead of base, and
// 0 when base is unknown or git cannot read the tree.
func commitsAhead(ctx context.Context, launcher proc.ILauncher, worktree, base string) int {
	if base == "" {
		return 0
	}
	result, err := launcher.Run(ctx, proc.Command{
		Executable: gitExecutable,
		Arguments:  []string{gitDirectory, worktree, gitRevList, gitCount, base + gitRange + gitHead},
		Directory:  worktree,
	})
	if err != nil {
		return 0
	}
	count, err := strconv.Atoi(strings.TrimSpace(string(result.Stdout)))
	if err != nil {
		return 0
	}
	return count
}

// changedFiles is how many files the worktree reports changed but not
// committed, and 0 when git cannot read the tree.
func changedFiles(ctx context.Context, launcher proc.ILauncher, worktree string) int {
	result, err := launcher.Run(ctx, proc.Command{
		Executable: gitExecutable,
		Arguments:  []string{gitDirectory, worktree, gitStatus, gitPorcelain},
		Directory:  worktree,
	})
	if err != nil {
		return 0
	}
	text := strings.TrimSpace(string(result.Stdout))
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}
