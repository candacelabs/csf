// Copyright 2026 Candace Labs

package session

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/candacelabs/csf/io/ipc/docker"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/kernel/sandbox"
	"github.com/candacelabs/csf/services/harness/endpoint"
)

//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -destination=mocks/mock_session_containers.go -package=mocks github.com/candacelabs/csf/services/harness/session ISessionContainers

// Container sessions: each session's executor runs inside one container of
// the session image, started through the container capability and removed
// when the session closes.
const (
	// ContainerUser is the user every session container runs as.
	ContainerUser = "1000:1000"
	// ContainerNetwork is the Docker network mode a session container joins:
	// the default bridge, which reaches the internet and the tailnet.
	ContainerNetwork = "default"
	// ContainerExecutor is the executor's name on the session image's PATH.
	ContainerExecutor = "claude"
	// ContainerCopilotExecutor is the Copilot CLI's name on the session
	// image's PATH, for a recipe that chooses Copilot. It authenticates with
	// gh's credential, which every session container mounts.
	ContainerCopilotExecutor = "copilot"
	// DefaultDockerExecutable attaches the executor to its container.
	DefaultDockerExecutable = "docker"
	// SessionImageFile is where a repository pins its session image.
	SessionImageFile = "bazel/session_image.txt"
	// DefaultSessionImage is the fallback for a worktree that pins no image:
	// the tag bazel/session_image/build.sh gives the last image it built on
	// this host.
	DefaultSessionImage = "csf-session:latest"
	// LabelAssignment is the label every session container carries: its
	// assignment, which is its run directory's name.
	LabelAssignment = "csf.assignment"
)

// The launch receipt and the container's start, as the event log records
// them.
const (
	// EventTypeContainerStarted records the session container and how long it
	// took to start.
	EventTypeContainerStarted = "harness_container_started"
	// EventTypeLaunchReceipt records, when a session closes, how it was
	// launched (KeyLaunch) and, for a container session, what its container
	// spent. A host session is one the operator chose explicitly.
	EventTypeLaunchReceipt = "harness_launch_receipt"
	// KeyLaunch names how a session was launched: LaunchContainer or
	// LaunchHost.
	KeyLaunch       = "launch"
	LaunchContainer = "container"
	LaunchHost      = "host"
	keyContainerID  = "container_id"
	keyImage        = "image"
	keyStartMillis  = "start_ms"
)

// dockerExecAttach attaches the executor to the container: `docker exec -i
// --workdir <worktree>`, then one --env per variable and the container.
var dockerExecAttach = []string{"exec", "-i", "--workdir"}

// dockerEnv passes one variable into the attached executor.
const dockerEnv = "--env"

// The session's own directories under its run directory: the home holds the
// executor's state and the mounted credentials; the Go directory holds the
// build and module caches, removed when the session closes.
const (
	containerHomeDirectory = "home"
	// conversationStoreHome is where the executor keeps its saved
	// conversations under its home.
	conversationStoreHome = ".claude/projects"
	containerGoDirectory  = "go"
	goBuildCache          = "build"
	goModuleCache         = "mod"
	gitDirectoryName      = ".git"
)

// The environment a session container gets beyond what the harness granted.
const (
	envHome       = "HOME"
	envGoCache    = "GOCACHE"
	envGoModCache = "GOMODCACHE"
	envGoFlags    = "GOFLAGS"
	// goModCacheWritable keeps the module cache removable.
	goModCacheWritable  = "-modcacherw"
	envGhNoUpdateNotice = "GH_NO_UPDATE_NOTIFIER"
	envTrue             = "1"
)

// Git configured through the environment, since no host config file is
// mounted: the gh credential helper and the identity git resolves on the
// host.
const (
	envGitConfigCount   = "GIT_CONFIG_COUNT"
	envGitConfigKey     = "GIT_CONFIG_KEY_"
	envGitConfigValue   = "GIT_CONFIG_VALUE_"
	gitCredentialKey    = "credential.https://github.com.helper"
	gitCredentialHelper = "!gh auth git-credential"
	gitUserName         = "user.name"
	gitUserEmail        = "user.email"
	gitVar              = "var"
	gitCommitterIdent   = "GIT_COMMITTER_IDENT"
)

// The per-session container limits, derived on 2026-10-05 from measured
// session peaks on the 32-core, 67 GB build host. Inputs: a 45-minute sample
// of all 46 host sessions every 5 s (executor process tree plus the cgroups of
// the Bazel containers mounting its run directory), and the receipts of three
// container sessions (a cold in-container Bazel test plus go test -race: 5.49
// GB and 5.98 GB peaks; a real gofmt slice: 1.23 GB).
//   - memory: largest peak 5.98 GB = 5.57 GiB, x1.5 headroom = 8.36, rounded
//     up to 9 GiB (the sampled median is 0.25 GiB, so peaks rarely coincide);
//   - cpu: largest 5 s rate 25.0 cores, rounded up to a multiple of 4: 28,
//     leaving 4 of the 32 cores to the host;
//   - pids: largest task count 838 (threads included, which pids.max counts),
//     x2 rounded up to a power of two: 2048.
const (
	DefaultContainerMemoryBytes int64 = 9 << 30
	DefaultContainerNanoCPUs    int64 = 28_000_000_000
	DefaultContainerPidsLimit   int64 = 2048
)

// ISessionContainers is the container capability a container session needs:
// *docker.ContainerHost in production, a double in specs.
type ISessionContainers interface {
	StartDetached(ctx context.Context, spec docker.SandboxSpec) (docker.StartedContainer, error)
	ContainerUsage(started docker.StartedContainer) (sandbox.Usage, error)
	RemoveContainer(ctx context.Context, id string) error
}

// Credential is one host credential mounted read-only into the session's home.
type Credential struct {
	// Source is the host path.
	Source string
	// Home is the path under the session's home it is mounted at, such as
	// .config/gh.
	Home string
}

// ContainerSettings is how every session container is built.
type ContainerSettings struct {
	// Image is the session image for a worktree that pins none in
	// [SessionImageFile].
	Image string
	// Docker is the docker CLI the executor is attached through with
	// `docker exec -i`; the harness keeps the socket, the session never sees
	// it.
	Docker string
	// MemoryBytes, NanoCPUs and PidsLimit are the per-session limits; zero
	// takes the derived default.
	MemoryBytes int64
	NanoCPUs    int64
	PidsLimit   int64
	// Credentials are the executor's and gh's, mounted read-only.
	Credentials []Credential
	// ConversationStore is the host directory the executor saves its
	// conversations under, one directory per working directory (such as
	// ~/.claude/projects). The worktree's own directory is mounted read-write
	// into the session's home, so a container session resumes a conversation
	// a host session saved, and the other way round. Empty mounts none.
	ConversationStore string
}

// WithContainerSessions runs every session the runner opens inside its own
// container of the session image, with no Docker socket.
func WithContainerSessions(containers ISessionContainers, settings ContainerSettings) AgentSessionRunnerOption {
	return func(runner *AgentSessionRunner) error {
		switch {
		case containers == nil:
			return fmt.Errorf("%w: nil session containers", ErrInvalidOption)
		case settings.MemoryBytes < 0 || settings.NanoCPUs < 0 || settings.PidsLimit < 0:
			return fmt.Errorf("%w: session container limits must not be negative", ErrInvalidOption)
		}
		if settings.Docker == "" {
			settings.Docker = DefaultDockerExecutable
		}
		if settings.Image == "" {
			settings.Image = DefaultSessionImage
		}
		if settings.MemoryBytes == 0 {
			settings.MemoryBytes = DefaultContainerMemoryBytes
		}
		if settings.NanoCPUs == 0 {
			settings.NanoCPUs = DefaultContainerNanoCPUs
		}
		if settings.PidsLimit == 0 {
			settings.PidsLimit = DefaultContainerPidsLimit
		}
		runner.containers = containers
		runner.containerSettings = settings
		return nil
	}
}

// containment is a session's running container and the launch prefix that
// attaches its executor to it.
type containment struct {
	containers   ISessionContainers
	started      docker.StartedContainer
	goCache      string
	logger       *slog.Logger
	launchPrefix []string
}

// contain starts the session's container with its mounts and limits and
// returns the `docker exec` prefix the executor is opened behind. The
// environment the harness granted (the Bazel caches) is passed into the
// container, where every path keeps its host spelling: each is mounted at its
// own path.
func (runner *AgentSessionRunner) contain(ctx context.Context, state *RunState, directory string, environment []string, log *EventLog) (*containment, error) {
	settings := runner.containerSettings
	image := sessionImage(state.Worktree, settings.Image)
	home := filepath.Join(directory, containerHomeDirectory)
	goCache := filepath.Join(directory, containerGoDirectory)
	tmp := filepath.Join(directory, sandboxTmpDir)
	registry := filepath.Join(filepath.Dir(directory), endpoint.Directory)
	mounts := containerMounts(state, directory, registry, environment, home, settings)
	if err := prepareContainerPaths(home, tmp, registry, mounts, settings.Credentials); err != nil {
		return nil, err
	}
	if settings.ConversationStore != "" {
		key := conversationKey(state.Worktree)
		if err := adoptConversations(filepath.Join(home, conversationStoreHome, key), filepath.Join(settings.ConversationStore, key)); err != nil {
			return nil, err
		}
	}
	inside := append(append([]string{}, environment...),
		envHome+"="+home,
		envTmpDir+"="+tmp,
		envGoCache+"="+filepath.Join(goCache, goBuildCache),
		envGoModCache+"="+filepath.Join(goCache, goModuleCache),
		envGoFlags+"="+goModCacheWritable,
		envGhNoUpdateNotice+"="+envTrue)
	inside = append(inside, runner.gitEnvironment(ctx, state)...)
	begun := time.Now()
	started, err := runner.containers.StartDetached(ctx, docker.SandboxSpec{
		Image:        image,
		User:         ContainerUser,
		Environment:  inside,
		Mounts:       mounts,
		Labels:       map[string]string{LabelAssignment: state.AssignmentID},
		Network:      ContainerNetwork,
		MemoryBytes:  settings.MemoryBytes,
		NanoCPUs:     settings.NanoCPUs,
		PidsLimit:    settings.PidsLimit,
		WritableRoot: true,
	})
	if err != nil {
		return nil, fmt.Errorf("harness session: start the session container: %w", err)
	}
	log.Record(ctx, state.SessionID, state.Turns, EventTypeContainerStarted, "session container started",
		slog.String(keyContainerID, started.ID), slog.String(keyImage, image), slog.Int64(keyStartMillis, time.Since(begun).Milliseconds()))
	prefix := append(append([]string{settings.Docker}, dockerExecAttach...), state.Worktree)
	for _, variable := range inside {
		prefix = append(prefix, dockerEnv, variable)
	}
	return &containment{
		containers:   runner.containers,
		started:      started,
		goCache:      goCache,
		logger:       log.Logger(),
		launchPrefix: append(prefix, started.ID),
	}, nil
}

// close records the container's usage and removes it, which kills every
// process in it, double-forked ones included. The executor has already been
// closed by the caller, so the usage is final.
func (contained *containment) close(ctx context.Context, log *EventLog, sessionID string, turns int) {
	receipt := []slog.Attr{slog.String(KeyLaunch, LaunchContainer), slog.String(keyContainerID, contained.started.ID)}
	if usage, err := contained.containers.ContainerUsage(contained.started); err == nil {
		receipt = append(receipt, slog.Int64(keyCPUMicroseconds, usage.CPU.Microseconds()),
			slog.Uint64(keyMemoryPeakBytes, usage.MemoryPeakBytes))
	} else {
		contained.logger.Warn("harness session: container usage not read", "error", err)
	}
	log.Record(ctx, sessionID, turns, EventTypeLaunchReceipt, "session launch receipt", receipt...)
	if err := contained.containers.RemoveContainer(ctx, contained.started.ID); err != nil {
		contained.logger.Warn("harness session: session container not removed", "error", err)
	}
	if err := os.RemoveAll(contained.goCache); err != nil {
		contained.logger.Warn("harness session: session Go cache not removed", "error", err)
	}
}

// sessionImage is the session image the worktree pins in
// bazel/session_image.txt, so each branch runs in the image it was written
// for; a worktree that pins none, such as one created before the pin existed,
// runs in fallback.
func sessionImage(worktree string, fallback string) string {
	content, err := os.ReadFile(filepath.Join(worktree, SessionImageFile))
	if err == nil && strings.TrimSpace(string(content)) != "" {
		return strings.TrimSpace(string(content))
	}
	return fallback
}

// conversationKey is the directory name the executor saves a working
// directory's conversations under: the path with every character that is not
// an ASCII letter or digit replaced by '-'.
func conversationKey(workingDirectory string) string {
	return strings.Map(func(character rune) rune {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') {
			return character
		}
		return '-'
	}, workingDirectory)
}

// adoptConversations moves into the shared store the conversations an earlier
// container session saved in its private home, which the store's mount would
// otherwise hide. A conversation the store already holds is left where it is.
func adoptConversations(private string, store string) error {
	entries, err := os.ReadDir(private)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("harness session: read saved conversations: %w", err)
	}
	if err := os.MkdirAll(store, runDirectoryMode); err != nil {
		return fmt.Errorf("harness session: create the conversation store: %w", err)
	}
	for _, entry := range entries {
		target := filepath.Join(store, entry.Name())
		if _, err := os.Lstat(target); err == nil {
			continue
		}
		if err := os.Rename(filepath.Join(private, entry.Name()), target); err != nil {
			return fmt.Errorf("harness session: adopt saved conversation %s: %w", entry.Name(), err)
		}
	}
	return nil
}

// prepareContainerPaths creates the session's home, its private tmp, the
// endpoint registry's directory, every writable mount's source (a shared
// cache no session has used yet) and target, and the mount point of every
// credential, as the harness's own user, before Docker would refuse a missing
// source or create a missing mount point as root.
func prepareContainerPaths(home string, tmp string, registry string, mounts []docker.Mount, credentials []Credential) error {
	directories := []string{home, tmp, registry}
	for _, mount := range mounts {
		if !mount.ReadOnly {
			directories = append(directories, mount.Source, mount.Target)
		}
	}
	for _, directory := range directories {
		if err := os.MkdirAll(directory, runDirectoryMode); err != nil {
			return fmt.Errorf("harness session: create %s: %w", directory, err)
		}
	}
	for _, credential := range credentials {
		source, err := os.Stat(credential.Source)
		if err != nil {
			return fmt.Errorf("harness session: credential: %w", err)
		}
		target := filepath.Join(home, credential.Home)
		if source.IsDir() {
			err = os.MkdirAll(target, runDirectoryMode)
		} else if err = os.MkdirAll(filepath.Dir(target), runDirectoryMode); err == nil {
			err = os.WriteFile(target, nil, runFileMode)
		}
		if err != nil {
			return fmt.Errorf("harness session: credential mount point %s: %w", target, err)
		}
	}
	return nil
}

// containerMounts is the exact set a session container sees, each at its own
// host path: the run directory (which holds the worktree, the Bazel output
// base, the Go cache, the private tmp and the home), the main clone's .git,
// the endpoint registry's directory read-only, for the endpoint gate, the
// shared caches the harness granted through the environment, the worktree's
// directory of the executor's conversation store inside the home, and the
// credentials, read-only, inside the home.
func containerMounts(state *RunState, directory string, registry string, environment []string, home string, settings ContainerSettings) []docker.Mount {
	mounts := []docker.Mount{
		{Source: directory, Target: directory},
		{Source: filepath.Join(state.Repository, gitDirectoryName), Target: filepath.Join(state.Repository, gitDirectoryName)},
		{Source: registry, Target: registry, ReadOnly: true},
	}
	for _, variable := range environment {
		_, value, found := strings.Cut(variable, "=")
		if found && filepath.IsAbs(value) && !within(value, directory) {
			mounts = append(mounts, docker.Mount{Source: value, Target: value})
		}
	}
	if settings.ConversationStore != "" {
		key := conversationKey(state.Worktree)
		mounts = append(mounts, docker.Mount{Source: filepath.Join(settings.ConversationStore, key), Target: filepath.Join(home, conversationStoreHome, key)})
	}
	for _, credential := range settings.Credentials {
		mounts = append(mounts, docker.Mount{Source: credential.Source, Target: filepath.Join(home, credential.Home), ReadOnly: true})
	}
	return mounts
}

// parseIdent splits git's "Name <email> timestamp zone" into its name and
// email.
func parseIdent(ident string) (string, string, bool) {
	name, rest, found := strings.Cut(ident, " <")
	if !found {
		return "", "", false
	}
	email, _, found := strings.Cut(rest, ">")
	name = strings.TrimSpace(name)
	return name, email, found && name != "" && email != ""
}

// within reports whether path is directory or below it.
func within(path string, directory string) bool {
	relative, err := filepath.Rel(directory, path)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// gitEnvironment configures git inside the container without the host's
// config files: the gh credential helper, and the commit identity git
// resolves on the host for the worktree, which may come from the host user's
// account rather than any config file. An identity git cannot resolve is left
// out.
func (runner *AgentSessionRunner) gitEnvironment(ctx context.Context, state *RunState) []string {
	pairs := [][2]string{{gitCredentialKey, ""}, {gitCredentialKey, gitCredentialHelper}}
	result, err := runner.launcher.Run(ctx, proc.Command{
		Executable: gitExecutable,
		Arguments:  []string{gitDirectory, state.Worktree, gitVar, gitCommitterIdent},
		Directory:  state.Worktree,
	})
	if name, email, found := parseIdent(string(result.Stdout)); err == nil && found {
		pairs = append(pairs, [2]string{gitUserName, name}, [2]string{gitUserEmail, email})
	}
	variables := []string{envGitConfigCount + "=" + strconv.Itoa(len(pairs))}
	for index, pair := range pairs {
		variables = append(variables,
			envGitConfigKey+strconv.Itoa(index)+"="+pair[0],
			envGitConfigValue+strconv.Itoa(index)+"="+pair[1])
	}
	return variables
}
