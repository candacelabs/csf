package nodeexec

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/pkg/boundedbuffer"
	boundedbufferv1 "github.com/candacelabs/csf/pkg/boundedbuffer/v1"
	"github.com/candacelabs/csf/pkg/telemetry"
	deployv1 "github.com/candacelabs/csf/proto/candace/deploy/v1"
	telemetryv1 "github.com/candacelabs/csf/proto/candace/telemetry/v1"
	"google.golang.org/protobuf/proto"
)

var composeFileNames = []string{
	"compose.yaml",
	"compose.yml",
	"docker-compose.yaml",
	"docker-compose.yml",
}

const commandOutputLimit = 8 << 10

// ErrStoppedAssignmentUnsupported means STOPPED is outside this executor's
// approved Compose command surface.
var ErrStoppedAssignmentUnsupported = errors.New("STOPPED assignments are unsupported")

// IExecutor validates, plans, and executes a Compose reconciliation.
type IExecutor interface {
	Plan(ctx context.Context, assignment *deployv1.Assignment) (Plan, error)
	Execute(ctx context.Context, plan Plan) error
	DryRun() bool
	Workspace() string
}

// IComposeProcessExecutor is the operating-system process boundary for Docker
// Compose. DockerComposeRunner owns command policy; implementations only
// resolve and execute the complete invocation they receive.
type IComposeProcessExecutor interface {
	Resolve(executable string) (string, error)
	Run(ctx context.Context, invocation ComposeInvocation) (string, error)
}

// ComposeInvocation is a direct Docker Compose process invocation.
type ComposeInvocation struct {
	Argv      []string
	Directory string
}

// DockerComposeRunnerOption replaces an owned process-boundary dependency.
type DockerComposeRunnerOption func(dependencies *dockerComposeRunnerDependencies)

type dockerComposeRunnerDependencies struct {
	composeProcess IComposeProcessExecutor
}

// WithComposeProcessExecutor supplies the Docker Compose process boundary.
// It is primarily useful when embedding the runner or testing command policy.
func WithComposeProcessExecutor(executor IComposeProcessExecutor) DockerComposeRunnerOption {
	return func(dependencies *dockerComposeRunnerDependencies) {
		dependencies.composeProcess = executor
	}
}

// launcherComposeProcessExecutor runs Compose through the process-boundary
// capability the runner was granted.
type launcherComposeProcessExecutor struct{ launcher proc.ILauncher }

// DockerComposeRunner invokes the Docker CLI directly, constrained to one
// resolved workspace root.
type DockerComposeRunner struct {
	dockerBin      string
	gitBin         string
	workspace      string
	revisionRoot   string
	revisionLimits *deployv1.RevisionLimits
	sourceSync     *deployv1.SourceSync
	logger         *telemetry.JSONLLogger
	launcher       proc.ILauncher
	composeProcess IComposeProcessExecutor
	dryRun         bool
	materializeMu  sync.Mutex
}

// NewDockerComposeRunner validates the workspace and, outside dry-run mode,
// verifies that the Docker CLI is available. Every child process, Git and
// Compose alike, starts through launcher.
func NewDockerComposeRunner(
	launcher proc.ILauncher,
	dockerBin, workspace, revisionRoot string,
	revisionLimits *deployv1.RevisionLimits,
	dryRun bool,
	options ...DockerComposeRunnerOption,
) (*DockerComposeRunner, error) {
	return NewDockerComposeRunnerWithSourceSync(
		launcher, dockerBin, workspace, revisionRoot, revisionLimits, nil, nil, dryRun, options...,
	)
}

// NewDockerComposeRunnerWithSourceSync is NewDockerComposeRunner with an
// optional read-only Git remote used to acquire approved commits.
func NewDockerComposeRunnerWithSourceSync(
	launcher proc.ILauncher,
	dockerBin, workspace, revisionRoot string,
	revisionLimits *deployv1.RevisionLimits,
	sourceSync *deployv1.SourceSync,
	logger *telemetry.JSONLLogger,
	dryRun bool,
	options ...DockerComposeRunnerOption,
) (*DockerComposeRunner, error) {
	if launcher == nil {
		return nil, fmt.Errorf("Docker Compose runner requires a process launcher")
	}
	dependencies := dockerComposeRunnerDependencies{composeProcess: launcherComposeProcessExecutor{launcher: launcher}}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("Docker Compose runner option is required")
		}
		option(&dependencies)
	}
	if dependencies.composeProcess == nil {
		return nil, fmt.Errorf("Docker Compose process executor is required")
	}
	if err := deployv1.ValidateRevisionLimits(revisionLimits); err != nil {
		return nil, fmt.Errorf("invalid revision limits: %w", err)
	}
	revisionLimits = proto.Clone(revisionLimits).(*deployv1.RevisionLimits)
	if sourceSync != nil {
		if err := deployv1.ValidateSourceSync(sourceSync); err != nil {
			return nil, fmt.Errorf("invalid source sync: %w", err)
		}
		if !filepath.IsAbs(sourceSync.GetRepository()) {
			return nil, fmt.Errorf("source repository %q must be absolute", sourceSync.GetRepository())
		}
		sourceSync = proto.Clone(sourceSync).(*deployv1.SourceSync)
	}
	resolvedWorkspace, err := resolveAgentWorkspace(workspace)
	if err != nil {
		return nil, err
	}
	resolvedRevisions, err := prepareRevisionCacheDirectory(revisionRoot)
	if err != nil {
		return nil, err
	}
	if err := ensureRevisionCacheSeparateFromWorkspace(resolvedWorkspace, resolvedRevisions); err != nil {
		return nil, err
	}
	gitBin, err := launcher.LookPath("git")
	if err != nil {
		return nil, fmt.Errorf("finding Git CLI: %w", err)
	}
	var sourceRepositoryInitialized bool
	sourceSync, sourceRepositoryInitialized, err = prepareSourceSync(launcher, gitBin, resolvedWorkspace, resolvedRevisions, sourceSync)
	if err != nil {
		return nil, err
	}
	if !dryRun {
		resolvedBin, err := dependencies.composeProcess.Resolve(dockerBin)
		if err != nil {
			return nil, fmt.Errorf("finding Docker CLI %q: %w", dockerBin, err)
		}
		dockerBin = resolvedBin
	}
	runner := &DockerComposeRunner{
		dockerBin: dockerBin, gitBin: gitBin, workspace: resolvedWorkspace,
		revisionRoot: resolvedRevisions, revisionLimits: revisionLimits,
		sourceSync: sourceSync, logger: logger, launcher: launcher,
		composeProcess: dependencies.composeProcess,
		dryRun:         dryRun,
	}
	if sourceSync != nil {
		event, message := "nodeexec.source_repository.reused", "existing source repository ready"
		if sourceRepositoryInitialized {
			event, message = "nodeexec.source_repository.initialized", "source repository initialized"
		}
		runner.logSourceEvent(
			context.Background(), telemetryv1.Severity_SEVERITY_INFO,
			event, message,
			map[string]string{"repository": sourceSync.GetRepository()},
		)
	}
	return runner, nil
}

func resolveAgentWorkspace(workspace string) (string, error) {
	resolved, err := filepath.EvalSymlinks(workspace)
	if err != nil {
		return "", fmt.Errorf("resolving workspace %q: %w", workspace, err)
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", fmt.Errorf("stating workspace %q: %w", resolved, err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workspace %q is not a directory", resolved)
	}
	return resolved, nil
}

func prepareRevisionCacheDirectory(revisionRoot string) (string, error) {
	if !filepath.IsAbs(revisionRoot) {
		return "", fmt.Errorf("revision root %q must be absolute", revisionRoot)
	}
	if err := os.MkdirAll(revisionRoot, 0o700); err != nil {
		return "", fmt.Errorf("creating revision root %q: %w", revisionRoot, err)
	}
	resolved, err := filepath.EvalSymlinks(revisionRoot)
	if err != nil {
		return "", fmt.Errorf("resolving revision root %q: %w", revisionRoot, err)
	}
	return resolved, nil
}

func ensureRevisionCacheSeparateFromWorkspace(workspace, revisions string) error {
	revisionsFromWorkspace, err := filepath.Rel(workspace, revisions)
	if err != nil {
		return fmt.Errorf("comparing workspace and revision root: %w", err)
	}
	workspaceFromRevisions, err := filepath.Rel(revisions, workspace)
	if err != nil {
		return fmt.Errorf("comparing revision root and workspace: %w", err)
	}
	if filepath.IsLocal(revisionsFromWorkspace) || filepath.IsLocal(workspaceFromRevisions) {
		return fmt.Errorf("revision root must be separate from the mutable workspace")
	}
	return nil
}

func prepareSourceSync(
	launcher proc.ILauncher,
	gitBin, workspace, revisions string,
	sourceSync *deployv1.SourceSync,
) (*deployv1.SourceSync, bool, error) {
	if sourceSync == nil {
		return nil, false, nil
	}
	if sourceSync.GetRemote() == "" {
		return nil, false, nil
	}
	resolvedRemote, err := resolveConfiguredSourceRemote(launcher, gitBin, workspace, sourceSync.GetRemote())
	if err != nil {
		return nil, false, err
	}
	sourceSync.Remote = resolvedRemote
	if err := deployv1.ValidateSourceSync(sourceSync); err != nil {
		return nil, false, fmt.Errorf("invalid resolved source sync: %w", err)
	}
	resolvedSource, err := resolveSourceRepository(sourceSync.GetRepository())
	if err != nil {
		return nil, false, err
	}
	if err := ensureSourceRepositorySeparateFromExecutionRoots(workspace, revisions, resolvedSource); err != nil {
		return nil, false, err
	}
	initialized, err := initializeBareSourceRepository(launcher, gitBin, resolvedSource)
	if err != nil {
		return nil, false, err
	}
	sourceSync.Repository = resolvedSource
	return sourceSync, initialized, nil
}

func ensureSourceRepositorySeparateFromExecutionRoots(workspace, revisions, source string) error {
	sourceFromWorkspace, sourceWorkspaceErr := filepath.Rel(workspace, source)
	workspaceFromSource, workspaceSourceErr := filepath.Rel(source, workspace)
	sourceFromRevisions, sourceRevisionsErr := filepath.Rel(revisions, source)
	revisionsFromSource, revisionsSourceErr := filepath.Rel(source, revisions)
	if sourceWorkspaceErr != nil || workspaceSourceErr != nil || sourceRevisionsErr != nil || revisionsSourceErr != nil {
		return fmt.Errorf("comparing source repository with execution roots")
	}
	if filepath.IsLocal(sourceFromWorkspace) || filepath.IsLocal(workspaceFromSource) ||
		filepath.IsLocal(sourceFromRevisions) || filepath.IsLocal(revisionsFromSource) {
		return fmt.Errorf("source repository must be separate from the workspace and revision cache")
	}
	return nil
}

// DryRun reports whether command execution is disabled.
func (r *DockerComposeRunner) DryRun() bool { return r.dryRun }

// Workspace returns the canonical workspace path.
func (r *DockerComposeRunner) Workspace() string { return r.workspace }

// Plan returns the exact approved Compose command plan for one running
// assignment. STOPPED fails closed because its required Compose mutation is
// outside the executor's approved command surface.
func (r *DockerComposeRunner) Plan(ctx context.Context, assignment *deployv1.Assignment) (Plan, error) {
	if err := deployv1.ValidateAssignment(assignment); err != nil {
		return Plan{}, err
	}
	if assignment.GetDesiredState() == deployv1.DesiredState_DESIRED_STATE_STOPPED {
		return Plan{}, ErrStoppedAssignmentUnsupported
	}
	return r.planRunningAssignment(ctx, assignment)
}

func (r *DockerComposeRunner) planRunningAssignment(ctx context.Context, assignment *deployv1.Assignment) (Plan, error) {
	appDir, err := r.materializeRevision(ctx, assignment)
	if err != nil {
		return Plan{}, err
	}
	composeFile, err := findComposeFileInMaterializedRevision(appDir)
	if err != nil {
		return Plan{}, err
	}
	return r.composePlan(appDir, composeFile, assignment, "up", "-d", "--remove-orphans", assignment.GetApp()), nil
}

func (r *DockerComposeRunner) composePlan(
	projectDirectory, composeFile string,
	assignment *deployv1.Assignment,
	actionArguments ...string,
) Plan {
	base := []string{
		r.dockerBin,
		"compose",
		"--project-directory", projectDirectory,
		"--project-name", assignment.GetProject(),
		"--file", composeFile,
	}
	preflight := append(append([]string(nil), base...), "config", "--quiet")
	action := append(append([]string(nil), base...), actionArguments...)
	return Plan{Commands: []Command{{Argv: preflight}, {Argv: action}}}
}

// Execute validates the internally generated plan, runs `docker compose config
// --quiet`, and only then runs the convergence command. Dry-run mode performs
// the same read-only Compose validation but never reaches the mutating command.
func (r *DockerComposeRunner) Execute(ctx context.Context, plan Plan) error {
	preflight, action, err := r.validatedComposeCommands(plan)
	if err != nil {
		return err
	}
	if err := r.executeComposeCommand(ctx, "preflight", preflight); err != nil {
		return err
	}
	if r.dryRun {
		return nil
	}
	return r.executeComposeCommand(ctx, "convergence action", action)
}

func (r *DockerComposeRunner) validatedComposeCommands(plan Plan) (Command, Command, error) {
	if len(plan.Commands) != 2 {
		return Command{}, Command{}, fmt.Errorf("invalid Compose plan: expected preflight and convergence action")
	}
	preflight := plan.Commands[0]
	action := plan.Commands[1]
	if !r.isDirectDockerCommand(preflight) {
		return Command{}, Command{}, fmt.Errorf("invalid Compose preflight command")
	}
	if !r.isDirectDockerCommand(action) {
		return Command{}, Command{}, fmt.Errorf("invalid Compose convergence command")
	}
	return preflight, action, nil
}

func (r *DockerComposeRunner) isDirectDockerCommand(command Command) bool {
	return len(command.Argv) > 0 && command.Argv[0] == r.dockerBin
}

func (r *DockerComposeRunner) executeComposeCommand(ctx context.Context, phase string, command Command) error {
	output, err := r.composeProcess.Run(ctx, ComposeInvocation{
		Argv:      append([]string(nil), command.Argv...),
		Directory: r.workspace,
	})
	if err != nil {
		message := strings.TrimSpace(output)
		if message == "" {
			return fmt.Errorf("Compose %s failed: %w", phase, err)
		}
		return fmt.Errorf("Compose %s failed: %w: %s", phase, err, message)
	}
	return nil
}

func (executor launcherComposeProcessExecutor) Resolve(executable string) (string, error) {
	return executor.launcher.LookPath(executable)
}

func (executor launcherComposeProcessExecutor) Run(ctx context.Context, invocation ComposeInvocation) (string, error) {
	output, err := newCommandOutputBuffer()
	if err != nil {
		return "", fmt.Errorf("constructing Compose output buffer: %w", err)
	}
	_, err = executor.launcher.Run(ctx, proc.Command{
		Executable: invocation.Argv[0], Arguments: invocation.Argv[1:], Directory: invocation.Directory,
		Stdout: output, Stderr: output,
	})
	return output.String(), err
}

func newCommandOutputBuffer() (*boundedbuffer.Buffer, error) {
	return boundedbuffer.New(&boundedbufferv1.Retention{MaxBytes: commandOutputLimit})
}

func findComposeFileInMaterializedRevision(appDir string) (string, error) {
	for _, name := range composeFileNames {
		candidate := filepath.Join(appDir, name)
		resolved, found, err := resolveContainedRegularComposeFile(appDir, candidate)
		if err != nil {
			return "", err
		}
		if found {
			return resolved, nil
		}
	}
	return "", fmt.Errorf("app directory %q has no Compose file", appDir)
}

func resolveContainedRegularComposeFile(appDir, candidate string) (string, bool, error) {
	info, err := os.Stat(candidate)
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("stating Compose file %q: %w", candidate, err)
	}
	if !info.Mode().IsRegular() {
		return "", false, fmt.Errorf("Compose file %q is not a regular file", candidate)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return "", false, fmt.Errorf("resolving Compose file %q: %w", candidate, err)
	}
	relative, err := filepath.Rel(appDir, resolved)
	if err != nil || !filepath.IsLocal(relative) {
		return "", false, fmt.Errorf("Compose file %q escapes materialized app revision", candidate)
	}
	return resolved, true, nil
}
