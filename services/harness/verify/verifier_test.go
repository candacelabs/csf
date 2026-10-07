// Copyright 2026 Candace Labs

package verify

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"testing"

	"github.com/candacelabs/csf/io/ipc/proc"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

// testLauncher returns a real process launcher for the verifier under test.
func testLauncher(t *testing.T) *proc.HostLauncher {
	t.Helper()
	launcher, err := proc.NewHostLauncher()
	if err != nil {
		t.Fatalf("NewHostLauncher failed: %v", err)
	}
	return launcher
}

func TestValidateProposal(t *testing.T) {
	tests := []struct {
		name      string
		proposal  *harnessv1.Proposal
		wantError bool
	}{
		{
			name: "valid proposal",
			proposal: &harnessv1.Proposal{
				AssignmentId: "12345678-1234-5678-1234-567812345678",
				Diff:         "diff --git a/file.txt b/file.txt\n--- a/file.txt\n+++ b/file.txt\n@@ -1 +1 @@\n-old\n+new",
				Message:      "test commit",
			},
			wantError: false,
		},
		{
			name: "missing assignment_id",
			proposal: &harnessv1.Proposal{
				Diff:    "patch",
				Message: "test commit",
			},
			wantError: true,
		},
		{
			name: "missing diff",
			proposal: &harnessv1.Proposal{
				AssignmentId: "12345678-1234-5678-1234-567812345678",
				Message:      "test commit",
			},
			wantError: true,
		},
		{
			name: "missing message",
			proposal: &harnessv1.Proposal{
				AssignmentId: "12345678-1234-5678-1234-567812345678",
				Diff:         "patch",
			},
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateProposal(tt.proposal)
			if (err != nil) != tt.wantError {
				t.Errorf("validateProposal() error = %v, wantError %v", err, tt.wantError)
			}
		})
	}
}

func TestApply(t *testing.T) {
	// Create a temporary git repository for testing.
	tmpdir := t.TempDir()
	if err := initRepo(tmpdir); err != nil {
		t.Fatalf("initRepo failed: %v", err)
	}

	verifier := NewPatchVerifier(tmpdir, testLauncher(t))

	// Create a test file and commit it.
	testFile := tmpdir + "/test.txt"
	if err := os.WriteFile(testFile, []byte("original\n"), 0o644); err != nil {
		t.Fatalf("WriteFile failed: %v", err)
	}

	ctx := context.Background()
	cmd := exec.CommandContext(ctx, "git", "add", "test.txt")
	cmd.Dir = tmpdir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git add failed: %v: %s", err, string(output))
	}

	cmd = exec.CommandContext(ctx, "git", "commit", "-m", "initial")
	cmd.Dir = tmpdir
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git commit failed: %v: %s", err, string(output))
	}

	// Create a valid patch.
	patch := "--- a/test.txt\n+++ b/test.txt\n@@ -1 +1 @@\n-original\n+modified\n"

	proposal := &harnessv1.Proposal{
		AssignmentId: "12345678-1234-5678-1234-567812345678",
		Diff:         patch,
		Message:      "apply patch",
	}

	rejection := verifier.Apply(ctx, proposal)
	if rejection != nil {
		t.Errorf("Apply() returned rejection: %v", rejection)
	}

	// Verify the file was modified.
	content, err := os.ReadFile(testFile)
	if err != nil {
		t.Fatalf("ReadFile failed: %v", err)
	}
	if !bytes.Equal(content, []byte("modified\n")) {
		t.Errorf("file content = %q, want %q", string(content), "modified\n")
	}
}

func initRepo(path string) error {
	cmd := exec.Command("git", "init")
	cmd.Dir = path
	if _, err := cmd.CombinedOutput(); err != nil {
		return err
	}
	for _, cmd := range [][]string{
		{"git", "config", "user.email", "test@test.local"},
		{"git", "config", "user.name", "Test User"},
	} {
		c := exec.Command(cmd[0], cmd[1:]...)
		c.Dir = path
		if _, err := c.CombinedOutput(); err != nil {
			return err
		}
	}
	return nil
}
