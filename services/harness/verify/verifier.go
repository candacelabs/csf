// Copyright 2026 Candace Labs

package verify

import (
	"bytes"
	"context"
	"fmt"

	"github.com/candacelabs/csf/io/ipc/proc"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

// PatchVerifier applies proposed patches to a worktree. It spawns git through
// the process boundary's capability, never os/exec directly.
type PatchVerifier struct {
	worktreePath string
	launcher     proc.ILauncher
}

// NewPatchVerifier creates a verifier for the given worktree, running git
// through launcher.
func NewPatchVerifier(worktreePath string, launcher proc.ILauncher) *PatchVerifier {
	return &PatchVerifier{worktreePath: worktreePath, launcher: launcher}
}

// Apply applies a proposed patch to the worktree, returning a rejection if it
// fails or nil on success.
func (v *PatchVerifier) Apply(ctx context.Context, proposal *harnessv1.Proposal) *harnessv1.Rejection {
	if err := validateProposal(proposal); err != nil {
		return &harnessv1.Rejection{
			Code:    harnessv1.Rejection_CODE_VALIDATION_FAILED,
			Details: err.Error(),
		}
	}

	if err := v.applyPatch(ctx, proposal.Diff); err != nil {
		return &harnessv1.Rejection{
			Code:    harnessv1.Rejection_CODE_APPLY_FAILED,
			Details: err.Error(),
		}
	}

	return nil
}

// runGit runs git with the given arguments in the worktree, feeding stdin on
// standard input when non-empty, and returns its combined output.
func (v *PatchVerifier) runGit(ctx context.Context, arguments []string, stdin string) ([]byte, error) {
	command := proc.Command{Executable: "git", Arguments: arguments, Directory: v.worktreePath}
	if stdin != "" {
		command.Stdin = bytes.NewReader([]byte(stdin))
	}
	result, err := v.launcher.Run(ctx, command)
	return append(result.Stdout, result.Stderr...), err
}

// applyPatch applies a unified diff using git apply, to the index as well as
// the working tree, so that [PatchVerifier.Commit] commits exactly the patched
// files and nothing else the worktree holds.
func (v *PatchVerifier) applyPatch(ctx context.Context, diff string) error {
	if _, err := v.runGit(ctx, []string{"apply", "--check", "--index"}, diff); err != nil {
		return fmt.Errorf("patch check failed: %w", err)
	}
	if _, err := v.runGit(ctx, []string{"apply", "--index"}, diff); err != nil {
		return fmt.Errorf("patch apply failed: %w", err)
	}
	return nil
}

// validateProposal checks the proposal's fields are valid.
func validateProposal(proposal *harnessv1.Proposal) error {
	if proposal.AssignmentId == "" {
		return fmt.Errorf("assignment_id is required")
	}
	if len(proposal.Diff) == 0 {
		return fmt.Errorf("diff is required")
	}
	if len(proposal.Message) == 0 {
		return fmt.Errorf("message is required")
	}
	// Message should fit in a commit message.
	if len(proposal.Message) > 8000 {
		return fmt.Errorf("message exceeds maximum length")
	}
	return nil
}

// Commit creates a commit with the given message in the worktree.
func (v *PatchVerifier) Commit(ctx context.Context, message string) error {
	// Configure git for the commit.
	for _, arguments := range [][]string{
		{"config", "user.email", "harness@candace.local"},
		{"config", "user.name", "Harness Verifier"},
	} {
		if _, err := v.runGit(ctx, arguments, ""); err != nil {
			return fmt.Errorf("git config failed: %w", err)
		}
	}

	output, err := v.runGit(ctx, []string{"commit", "-m", message}, "")
	if err != nil {
		// No changes to commit is not an error.
		if bytes.Contains(output, []byte("nothing to commit")) {
			return nil
		}
		return fmt.Errorf("commit failed: %w", err)
	}

	return nil
}
