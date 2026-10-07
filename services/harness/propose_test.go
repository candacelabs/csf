// Copyright 2026 Candace Labs

package harness_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/candacelabs/csf/io/ipc/proc"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/harness/verify"
)

// testLauncher returns a real process launcher for the verifier under test.
func testLauncher(tb testing.TB) *proc.HostLauncher {
	tb.Helper()
	launcher, err := proc.NewHostLauncher()
	if err != nil {
		tb.Fatalf("NewHostLauncher failed: %v", err)
	}
	return launcher
}

// TestProposePatchApplicationCold measures the time to apply a patch in a fresh worktree.
func BenchmarkProposePatchApplicationCold(b *testing.B) {
	tmpdir := b.TempDir()
	verifier := verify.NewPatchVerifier(tmpdir, testLauncher(b))
	ctx := context.Background()

	// Initialize repo
	if err := initGitRepo(tmpdir); err != nil {
		b.Fatalf("initGitRepo failed: %v", err)
	}

	// Create initial file
	testFile := filepath.Join(tmpdir, "test.txt")
	if err := os.WriteFile(testFile, []byte("original"), 0o644); err != nil {
		b.Fatalf("WriteFile failed: %v", err)
	}

	// Initial commit
	if err := gitCommit(tmpdir, "test.txt", "initial"); err != nil {
		b.Fatalf("gitCommit failed: %v", err)
	}

	// Create patch
	patch := "--- a/test.txt\n+++ b/test.txt\n@@ -1 +1 @@\n-original\n+modified"
	proposal := &harnessv1.Proposal{
		AssignmentId: "12345678-1234-5678-1234-567812345678",
		Diff:         patch,
		Message:      "apply patch",
		ProposedAt:   timestamppb.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset file for each iteration
		if err := os.WriteFile(testFile, []byte("original"), 0o644); err != nil {
			b.Fatalf("WriteFile failed: %v", err)
		}
		rejection := verifier.Apply(ctx, proposal)
		if rejection != nil {
			b.Fatalf("Apply failed: %v", rejection)
		}
	}
	b.StopTimer()
}

// TestProposePatchApplicationWarm measures the time to apply a patch after warming up git state.
func BenchmarkProposePatchApplicationWarm(b *testing.B) {
	tmpdir := b.TempDir()
	verifier := verify.NewPatchVerifier(tmpdir, testLauncher(b))
	ctx := context.Background()

	// Initialize repo
	if err := initGitRepo(tmpdir); err != nil {
		b.Fatalf("initGitRepo failed: %v", err)
	}

	// Create initial file
	testFile := filepath.Join(tmpdir, "test.txt")
	if err := os.WriteFile(testFile, []byte("original"), 0o644); err != nil {
		b.Fatalf("WriteFile failed: %v", err)
	}

	// Initial commit
	if err := gitCommit(tmpdir, "test.txt", "initial"); err != nil {
		b.Fatalf("gitCommit failed: %v", err)
	}

	// Warm up: apply a patch once to populate git caches
	warmPatch := "--- a/test.txt\n+++ b/test.txt\n@@ -1 +1 @@\n-original\n+warm"
	warmProposal := &harnessv1.Proposal{
		AssignmentId: "12345678-1234-5678-1234-567812345678",
		Diff:         warmPatch,
		Message:      "warm up",
		ProposedAt:   timestamppb.Now(),
	}
	rejection := verifier.Apply(ctx, warmProposal)
	if rejection != nil {
		b.Fatalf("Warm-up failed: %v", rejection)
	}

	// Reset file
	if err := os.WriteFile(testFile, []byte("warm"), 0o644); err != nil {
		b.Fatalf("WriteFile failed: %v", err)
	}

	// Create actual test patch
	patch := "--- a/test.txt\n+++ b/test.txt\n@@ -1 +1 @@\n-warm\n+modified"
	proposal := &harnessv1.Proposal{
		AssignmentId: "12345678-1234-5678-1234-567812345678",
		Diff:         patch,
		Message:      "apply patch warm",
		ProposedAt:   timestamppb.Now(),
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Reset file for each iteration
		if err := os.WriteFile(testFile, []byte("warm"), 0o644); err != nil {
			b.Fatalf("WriteFile failed: %v", err)
		}
		rejection := verifier.Apply(ctx, proposal)
		if rejection != nil {
			b.Fatalf("Apply failed: %v", rejection)
		}
	}
	b.StopTimer()
}

// TestProposeBurstApply measures the time to apply multiple patches in sequence (burst).
func BenchmarkProposeBurstApply(b *testing.B) {
	tmpdir := b.TempDir()
	verifier := verify.NewPatchVerifier(tmpdir, testLauncher(b))
	ctx := context.Background()

	// Initialize repo
	if err := initGitRepo(tmpdir); err != nil {
		b.Fatalf("initGitRepo failed: %v", err)
	}

	// Create initial file
	testFile := filepath.Join(tmpdir, "test.txt")
	if err := os.WriteFile(testFile, []byte("line1\n"), 0o644); err != nil {
		b.Fatalf("WriteFile failed: %v", err)
	}

	// Initial commit
	if err := gitCommit(tmpdir, "test.txt", "initial"); err != nil {
		b.Fatalf("gitCommit failed: %v", err)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Apply 10 patches in burst
		for j := 0; j < 10; j++ {
			content, err := os.ReadFile(testFile)
			if err != nil {
				b.Fatalf("ReadFile failed: %v", err)
			}

			patch := fmt.Sprintf("--- a/test.txt\n+++ b/test.txt\n@@ -1,%d +1,%d @@\n %s+line%d\n", j+1, j+2, content[:len(content)-1], j+2)

			proposal := &harnessv1.Proposal{
				AssignmentId: "12345678-1234-5678-1234-567812345678",
				Diff:         patch,
				Message:      fmt.Sprintf("add line %d", j+2),
				ProposedAt:   timestamppb.Now(),
			}

			rejection := verifier.Apply(ctx, proposal)
			if rejection != nil {
				b.Logf("Apply %d failed: %v", j, rejection)
			}

			// Commit the change for the next patch
			if err := gitCommit(tmpdir, "test.txt", proposal.Message); err != nil {
				b.Logf("gitCommit %d failed: %v", j, err)
			}
		}
	}
	b.StopTimer()
}

// TestProposeRejectionInvalidMode tests that proposal is rejected when workspace is not in patch mode.
func TestProposeRejectionInvalidMode(t *testing.T) {
	tmpdir := t.TempDir()
	verifier := verify.NewPatchVerifier(tmpdir, testLauncher(t))
	ctx := context.Background()

	// Initialize repo
	if err := initGitRepo(tmpdir); err != nil {
		t.Fatalf("initGitRepo failed: %v", err)
	}

	// Create initial file
	testFile := filepath.Join(tmpdir, "test.txt")
	if err := os.WriteFile(testFile, []byte("original"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Initial commit
	if err := gitCommit(tmpdir, "test.txt", "initial"); err != nil {
		t.Fatalf("gitCommit failed: %v", err)
	}

	// Create patch
	patch := "--- a/test.txt\n+++ b/test.txt\n@@ -1 +1 @@\n-original\n+modified"
	proposal := &harnessv1.Proposal{
		AssignmentId: "12345678-1234-5678-1234-567812345678",
		Diff:         patch,
		Message:      "apply patch",
		ProposedAt:   timestamppb.Now(),
	}

	rejection := verifier.Apply(ctx, proposal)
	if rejection == nil {
		t.Fatalf("Expected rejection, but apply succeeded")
	}
}

// TestProposeRejectionInvalidPatch tests that proposal is rejected on invalid patch.
func TestProposeRejectionInvalidPatch(t *testing.T) {
	tmpdir := t.TempDir()
	verifier := verify.NewPatchVerifier(tmpdir, testLauncher(t))
	ctx := context.Background()

	// Initialize repo
	if err := initGitRepo(tmpdir); err != nil {
		t.Fatalf("initGitRepo failed: %v", err)
	}

	// Create initial file
	testFile := filepath.Join(tmpdir, "test.txt")
	if err := os.WriteFile(testFile, []byte("original"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Initial commit
	if err := gitCommit(tmpdir, "test.txt", "initial"); err != nil {
		t.Fatalf("gitCommit failed: %v", err)
	}

	// Create invalid patch (file doesn't match)
	patch := "--- a/nonexistent.txt\n+++ b/nonexistent.txt\n@@ -1 +1 @@\n-original\n+modified"
	proposal := &harnessv1.Proposal{
		AssignmentId: "12345678-1234-5678-1234-567812345678",
		Diff:         patch,
		Message:      "apply invalid patch",
		ProposedAt:   timestamppb.Now(),
	}

	rejection := verifier.Apply(ctx, proposal)
	if rejection == nil {
		t.Fatalf("Expected rejection for invalid patch, but apply succeeded")
	}
	if rejection.Code != harnessv1.Rejection_CODE_APPLY_FAILED {
		t.Errorf("Expected CODE_APPLY_FAILED, got %v", rejection.Code)
	}
}

// TestProposeCommitCreation tests that Commit creates a commit with the proposal message.
func TestProposeCommitCreation(t *testing.T) {
	tmpdir := t.TempDir()
	verifier := verify.NewPatchVerifier(tmpdir, testLauncher(t))
	ctx := context.Background()

	// Initialize repo
	if err := initGitRepo(tmpdir); err != nil {
		t.Fatalf("initGitRepo failed: %v", err)
	}

	// Create initial file
	testFile := filepath.Join(tmpdir, "test.txt")
	if err := os.WriteFile(testFile, []byte("original\n"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	// Initial commit
	if err := gitCommit(tmpdir, "test.txt", "initial"); err != nil {
		t.Fatalf("gitCommit failed: %v", err)
	}

	// Create and apply patch
	patch := "--- a/test.txt\n+++ b/test.txt\n@@ -1 +1 @@\n-original\n+modified\n"
	proposal := &harnessv1.Proposal{
		AssignmentId: "12345678-1234-5678-1234-567812345678",
		Diff:         patch,
		Message:      "apply patch",
		ProposedAt:   timestamppb.Now(),
	}

	rejection := verifier.Apply(ctx, proposal)
	if rejection != nil {
		t.Fatalf("Apply failed: %v", rejection)
	}

	// Create commit
	if err := verifier.Commit(ctx, proposal.Message); err != nil {
		t.Fatalf("Commit failed: %v", err)
	}

	// Verify commit was created
	cmd := exec.CommandContext(ctx, "git", "log", "-1", "--format=%B")
	cmd.Dir = tmpdir
	output, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git log failed: %v", err)
	}

	if !bytes.Contains(output, []byte(proposal.Message)) {
		t.Errorf("commit message = %q, want to contain %q", string(output), proposal.Message)
	}

	// The commit holds the patch: the applied change was staged for it.
	cmd = exec.CommandContext(ctx, "git", "show", "HEAD:test.txt")
	cmd.Dir = tmpdir
	committed, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git show failed: %v: %s", err, string(committed))
	}
	if !bytes.Equal(committed, []byte("modified\n")) {
		t.Errorf("committed test.txt = %q, want %q", string(committed), "modified\n")
	}
}

func initGitRepo(path string) error {
	cmd := exec.Command("git", "init")
	cmd.Dir = path
	if _, err := cmd.CombinedOutput(); err != nil {
		return err
	}
	for _, cmdArgs := range [][]string{
		{"git", "config", "user.email", "test@test.local"},
		{"git", "config", "user.name", "Test User"},
	} {
		c := exec.Command(cmdArgs[0], cmdArgs[1:]...)
		c.Dir = path
		if _, err := c.CombinedOutput(); err != nil {
			return err
		}
	}
	return nil
}

func gitCommit(path, file, message string) error {
	cmd := exec.Command("git", "add", file)
	cmd.Dir = path
	if _, err := cmd.CombinedOutput(); err != nil {
		return err
	}
	cmd = exec.Command("git", "commit", "-m", message)
	cmd.Dir = path
	_, err := cmd.CombinedOutput()
	return err
}
