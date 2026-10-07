// Copyright 2026 Candace Labs

package session

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/kernel/sandbox"
)

// Sandbox files under a run directory and the receipt event.
const (
	// DefaultDockerUpstream is the host's Docker Engine socket the per-session
	// proxy forwards allowed calls to.
	DefaultDockerUpstream = "/var/run/docker.sock"
	// proxySocketFile is the session's Docker proxy socket, under its run
	// directory; the launcher points DOCKER_HOST at it.
	proxySocketFile = "docker.sock"
	// sandboxPolicyFile is the launcher policy, under the run directory.
	sandboxPolicyFile = "sandbox-policy.json"
	// sandboxTmpDir is the session's private writable tmp, under the run
	// directory; the executor's TMPDIR points at it.
	sandboxTmpDir = "tmp"
	envTmpDir     = "TMPDIR"

	// EventTypeSandboxReceipt records a confined session's cgroup usage when it
	// closes, so the ops view can show what it spent.
	EventTypeSandboxReceipt = "harness_sandbox_receipt"
	keyCPUMicroseconds      = "cpu_usec"
	keyMemoryPeakBytes      = "memory_peak_bytes"
)

// WithSandbox confines every session the runner opens: it is granted the
// sandbox capability and the Docker Engine socket the per-session proxy
// forwards to. Without it, sessions run unsandboxed, which is the default until
// the sandbox acceptance passes on a host.
func WithSandbox(manager *sandbox.Manager, dockerUpstream string) AgentSessionRunnerOption {
	return func(runner *AgentSessionRunner) error {
		if manager == nil {
			return fmt.Errorf("%w: nil sandbox manager", ErrInvalidOption)
		}
		if dockerUpstream == "" {
			dockerUpstream = DefaultDockerUpstream
		}
		runner.sandbox = manager
		runner.dockerUpstream = dockerUpstream
		return nil
	}
}

// confinement is everything a sandboxed session needs wired into its executor
// and torn down when it closes.
type confinement struct {
	session      *sandbox.Session
	proxy        *proc.Process
	stopProxy    context.CancelFunc
	logger       *slog.Logger
	launchPrefix []string
	environment  []string
}

// confine creates the session's cgroup and policy, starts its Docker proxy, and
// returns the launch prefix and extra environment the executor is opened with.
// The proxy runs on a context of its own so it outlives the open call and is
// stopped by [confinement.close].
func (runner *AgentSessionRunner) confine(state *RunState, directory string, environment []string, logger *slog.Logger) (*confinement, error) {
	tmp := filepath.Join(directory, sandboxTmpDir)
	proxySocket := filepath.Join(directory, proxySocketFile)
	session, err := runner.sandbox.Open(sandbox.SessionParams{
		ID:          state.AssignmentID,
		PolicyPath:  filepath.Join(directory, sandboxPolicyFile),
		ReadWrite:   readWritePaths(state.Worktree, directory, environment),
		PrivateTmp:  tmp,
		ProxySocket: proxySocket,
	})
	if err != nil {
		return nil, err
	}
	proxyContext, stopProxy := context.WithCancel(context.WithoutCancel(context.Background()))
	command := session.DockerProxyCommand(runner.dockerUpstream)
	proxy, err := runner.launcher.Start(proxyContext, proc.Command{
		Executable: command[0],
		Arguments:  command[1:],
		Stderr:     proxyDiagnostics{logger: logger, assignment: state.AssignmentID},
	})
	if err != nil {
		stopProxy()
		_ = session.Kill()
		_ = session.Remove()
		return nil, fmt.Errorf("harness session: start the docker proxy: %w", err)
	}
	return &confinement{
		session:      session,
		proxy:        proxy,
		stopProxy:    stopProxy,
		logger:       logger,
		launchPrefix: session.LaunchPrefix(),
		environment:  []string{envTmpDir + "=" + tmp},
	}, nil
}

// close stops the proxy and ends the session's cgroup: it kills the whole
// subtree, records the usage receipt, and removes the cgroup. The executor has
// already been closed by the caller, so the kill is a backstop for anything it
// left and the usage is final.
func (confined *confinement) close(ctx context.Context, log *EventLog, sessionID string, turns int) {
	if err := confined.session.Kill(); err != nil {
		confined.logger.Warn("harness session: sandbox cgroup not killed", "error", err)
	}
	if usage, err := confined.session.Usage(); err == nil {
		log.Record(ctx, sessionID, turns, EventTypeSandboxReceipt, "sandbox usage",
			slog.Int64(keyCPUMicroseconds, usage.CPU.Microseconds()),
			slog.Uint64(keyMemoryPeakBytes, usage.MemoryPeakBytes))
	} else {
		confined.logger.Warn("harness session: sandbox usage not read", "error", err)
	}
	confined.stopProxy()
	_, _ = confined.proxy.Wait()
	if err := confined.session.Remove(); err != nil {
		confined.logger.Warn("harness session: sandbox cgroup not removed", "error", err)
	}
}

// readWritePaths are the paths a confined session may write: its worktree, its
// run directory, and the cache directories the harness granted through the
// environment (their values are absolute paths). The private tmp is added by
// the sandbox capability.
func readWritePaths(worktree string, directory string, environment []string) []string {
	paths := []string{worktree, directory}
	for _, variable := range environment {
		_, value, found := strings.Cut(variable, "=")
		if found && filepath.IsAbs(value) {
			paths = append(paths, value)
		}
	}
	return paths
}

// proxyDiagnostics forwards the proxy's standard error to the run's logger,
// line by line, under the assignment.
type proxyDiagnostics struct {
	logger     *slog.Logger
	assignment string
}

func (diagnostics proxyDiagnostics) Write(chunk []byte) (int, error) {
	for _, line := range strings.Split(strings.TrimRight(string(chunk), "\n"), "\n") {
		if strings.TrimSpace(line) != "" {
			diagnostics.logger.Info("harness session: docker proxy", "assignment", diagnostics.assignment, "line", line)
		}
	}
	return len(chunk), nil
}
