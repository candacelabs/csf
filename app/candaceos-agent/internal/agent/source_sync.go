package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	telemetryv1 "github.com/candacelabs/csf/proto/candace/telemetry/v1"
)

const (
	sourceHeadsRefspec = "+refs/heads/*:refs/candaceos/source/heads/*"
	sourceTagsRefspec  = "+refs/tags/*:refs/candaceos/source/tags/*"
)

func resolveConfiguredSourceRemote(gitBin, workspace, configured string) (string, error) {
	command := exec.Command(
		gitBin,
		"-c", "safe.directory="+workspace,
		"-C", workspace,
		"remote", "get-url", configured,
	)
	resolved, err := newCommandOutputBuffer()
	if err != nil {
		return "", fmt.Errorf("constructing source-remote output buffer: %w", err)
	}
	command.Stdout = resolved
	if err := command.Run(); err == nil && strings.TrimSpace(resolved.String()) != "" {
		return strings.TrimSpace(resolved.String()), nil
	}
	return configured, nil
}

// syncSourceRevision fetches only refs and objects. It never checks out or
// merges remote content into the mutable workspace.
func (r *DockerComposeRunner) syncSourceRevision(ctx context.Context, revision string) error {
	if r.sourceSync == nil {
		return nil
	}

	fetchTimeout := time.Duration(r.sourceSync.GetFetchTimeoutNanoseconds())
	syncContext, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	started := time.Now()
	r.logSourceEvent(
		ctx, telemetryv1.Severity_SEVERITY_INFO,
		"agent.source_sync.started", "source synchronization started",
		map[string]string{
			"repository": r.sourceSync.GetRepository(),
			"revision":   revision,
		},
	)
	if err := r.fetchSourceRefs(syncContext); err != nil {
		return r.sourceSyncFailure(ctx, syncContext, revision, started, fetchTimeout, "fetch", "fetching configured source remote", err)
	}
	if err := r.verifyFetchedSourceRevision(syncContext, revision); err != nil {
		return r.sourceSyncFailure(ctx, syncContext, revision, started, fetchTimeout, "verify", "verifying fetched source revision", err)
	}
	r.logSourceEvent(
		ctx, telemetryv1.Severity_SEVERITY_INFO,
		"agent.source_sync.completed", "source synchronization completed",
		map[string]string{
			"repository":  r.sourceSync.GetRepository(),
			"revision":    revision,
			"duration_ms": strconv.FormatInt(time.Since(started).Milliseconds(), 10),
		},
	)
	return nil
}

func (r *DockerComposeRunner) sourceSyncFailure(
	requestContext, timeoutContext context.Context,
	revision string,
	started time.Time,
	timeout time.Duration,
	operation, description string,
	commandErr error,
) error {
	err := sourceSyncCommandError(requestContext, timeoutContext, timeout, description, commandErr)
	r.logSourceEvent(
		requestContext, telemetryv1.Severity_SEVERITY_ERROR,
		"agent.source_sync.failed", "source synchronization failed",
		map[string]string{
			"repository":  r.sourceSync.GetRepository(),
			"revision":    revision,
			"operation":   operation,
			"duration_ms": strconv.FormatInt(time.Since(started).Milliseconds(), 10),
		},
	)
	return err
}

func (r *DockerComposeRunner) fetchSourceRefs(ctx context.Context) error {
	command := exec.CommandContext(
		ctx,
		r.gitBin,
		"-c", "safe.directory="+r.sourceSync.GetRepository(),
		"-C", r.sourceSync.GetRepository(),
		"fetch",
		"--no-tags",
		"--no-write-fetch-head",
		"--force",
		"--no-recurse-submodules",
		r.sourceSync.GetRemote(),
		sourceHeadsRefspec,
		sourceTagsRefspec,
	)
	command.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	return command.Run()
}

func (r *DockerComposeRunner) verifyFetchedSourceRevision(ctx context.Context, revision string) error {
	verify := exec.CommandContext(
		ctx,
		r.gitBin,
		"-c", "safe.directory="+r.sourceSync.GetRepository(),
		"-C", r.sourceSync.GetRepository(),
		"rev-parse", "--verify", revision+"^{commit}",
	)
	resolved, err := newCommandOutputBuffer()
	if err != nil {
		return fmt.Errorf("constructing source-verification output buffer: %w", err)
	}
	verify.Stdout = resolved
	if err := verify.Run(); err != nil {
		return fmt.Errorf("configured source remote does not contain approved commit %s: %w", revision, err)
	}
	if strings.TrimSpace(resolved.String()) != revision {
		return fmt.Errorf("configured source remote did not resolve the exact approved commit %s", revision)
	}
	return nil
}

func sourceSyncCommandError(
	requestContext, timeoutContext context.Context,
	timeout time.Duration,
	operation string,
	commandErr error,
) error {
	if err := requestContext.Err(); err != nil {
		return fmt.Errorf("%s: %w", operation, err)
	}
	if errors.Is(timeoutContext.Err(), context.DeadlineExceeded) {
		return fmt.Errorf("%s exceeded %s: %w", operation, timeout, context.DeadlineExceeded)
	}
	return fmt.Errorf("%s: %w", operation, commandErr)
}

func resolveSourceRepository(configured string) (string, error) {
	existing := filepath.Clean(configured)
	var missing []string
	for {
		if _, err := os.Lstat(existing); err == nil {
			break
		} else if !os.IsNotExist(err) {
			return "", fmt.Errorf("stating source repository path %q: %w", existing, err)
		}
		parent := filepath.Dir(existing)
		if parent == existing {
			return "", fmt.Errorf("source repository path %q has no existing ancestor", configured)
		}
		missing = append(missing, filepath.Base(existing))
		existing = parent
	}
	resolved, err := filepath.EvalSymlinks(existing)
	if err != nil {
		return "", fmt.Errorf("resolving source repository path %q: %w", existing, err)
	}
	for index := len(missing) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, missing[index])
	}
	return resolved, nil
}

func initializeBareSourceRepository(gitBin, repository string) (bool, error) {
	if err := ensureSourceRepositoryDirectory(repository); err != nil {
		return false, err
	}
	empty, err := sourceRepositoryIsEmpty(repository)
	if err != nil {
		return false, err
	}
	if empty {
		if err := initializeEmptyBareSourceRepository(gitBin, repository); err != nil {
			return false, err
		}
	}
	if err := os.Chmod(repository, 0o700); err != nil {
		return false, fmt.Errorf("securing source repository %q: %w", repository, err)
	}
	if err := verifyBareSourceRepository(gitBin, repository); err != nil {
		return false, err
	}
	return empty, nil
}

func ensureSourceRepositoryDirectory(repository string) error {
	if err := os.MkdirAll(filepath.Dir(repository), 0o700); err != nil {
		return fmt.Errorf("creating source repository parent %q: %w", filepath.Dir(repository), err)
	}
	info, err := os.Stat(repository)
	if os.IsNotExist(err) {
		if err := os.Mkdir(repository, 0o700); err != nil {
			return fmt.Errorf("creating source repository %q: %w", repository, err)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("stating source repository %q: %w", repository, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source repository %q is not a directory", repository)
	}
	return nil
}

func sourceRepositoryIsEmpty(repository string) (bool, error) {
	entries, err := os.ReadDir(repository)
	if err != nil {
		return false, fmt.Errorf("reading source repository %q: %w", repository, err)
	}
	return len(entries) == 0, nil
}

func initializeEmptyBareSourceRepository(gitBin, repository string) error {
	command := exec.Command(gitBin, "init", "--bare", "--quiet", repository)
	if err := command.Run(); err != nil {
		return fmt.Errorf("initializing bare source repository: %w", err)
	}
	return nil
}

func verifyBareSourceRepository(gitBin, repository string) error {
	command := exec.Command(
		gitBin,
		"-c", "safe.directory="+repository,
		"-C", repository,
		"rev-parse", "--is-bare-repository",
	)
	bare, err := newCommandOutputBuffer()
	if err != nil {
		return fmt.Errorf("constructing bare-repository output buffer: %w", err)
	}
	command.Stdout = bare
	if err := command.Run(); err != nil {
		return fmt.Errorf("source repository %q is not a bare Git repository: %w", repository, err)
	}
	if strings.TrimSpace(bare.String()) != "true" {
		return fmt.Errorf("source repository %q is not a bare Git repository", repository)
	}
	return nil
}

func (r *DockerComposeRunner) logSourceEvent(
	ctx context.Context,
	severity telemetryv1.Severity,
	event, message string,
	attributes map[string]string,
) {
	if r.logger == nil {
		return
	}
	if err := r.logger.Log(ctx, severity, event, message, attributes); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "writing source telemetry event %q: %v\n", event, err)
	}
}
