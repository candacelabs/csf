// Copyright 2026 Candace Labs

package session

import (
	"context"
	"fmt"
	"os"

	"github.com/candacelabs/csf/io/ipc/proc"
)

// createBuildContainer starts a long-lived build container for the session and
// returns its ID. It runs detached, because docker exec, which tools/bazel.sh
// uses to run a build in it, refuses a container that was created but never
// started. The container has the necessary mounts and runs with --network none
// to avoid per-command network namespace overhead. It runs as the caller's
// numeric user and group, which need no passwd entry: a CI container runs as
// the host's uid, which its image does not know.
func createBuildContainer(ctx context.Context, launcher proc.ILauncher, image string, worktree string, cacheRoot string) (string, error) {
	cmd := proc.Command{
		Executable: "docker",
		Arguments: []string{
			"run",
			"--detach",
			"--network", "none",
			"--user", fmt.Sprintf("%d:%d", os.Getuid(), os.Getgid()),
			"--env", "HOME=/bazel-home",
			"--env", "USER=bazel",
			"--volume", fmt.Sprintf("%s/home:/bazel-home", cacheRoot),
			"--volume", fmt.Sprintf("%s/output:/bazel-output", cacheRoot),
			"--volume", fmt.Sprintf("%s:%s", worktree, worktree),
			"--workdir", worktree,
			"--entrypoint", "sleep",
			image,
			"infinity",
		},
	}

	result, err := launcher.Run(ctx, cmd)
	if err != nil {
		return "", fmt.Errorf("harness session: create build container: %w", err)
	}

	return string(result.Stdout), nil
}

// cleanupBuildContainer removes the build container when the session ends.
func cleanupBuildContainer(ctx context.Context, launcher proc.ILauncher, containerID string) error {
	if containerID == "" {
		return nil
	}
	cmd := proc.Command{
		Executable: "docker",
		Arguments:  []string{"rm", "-f", containerID},
	}
	if _, err := launcher.Run(ctx, cmd); err != nil {
		return fmt.Errorf("harness session: cleanup build container: %w", err)
	}
	return nil
}
