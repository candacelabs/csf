// Copyright 2026 Candace Labs

// Package sandbox is the harness's per-session confinement capability, host
// tier: it owns the session cgroups under the harness's delegated cgroup
// subtree and writes the policy the Rust launcher enforces.
//
// The harness runs each session's turn executor behind the launcher
// ([Session.LaunchPrefix]), which joins the session's cgroup, applies the
// Landlock and seccomp policy and execs the executor. This package creates that
// cgroup and writes that policy; it kills the whole subtree on cancel
// ([Session.Kill]) and reads the cpu and memory receipts ([Session.Usage])
// before removing it ([Session.Remove]).
//
// It reaches the cgroup filesystem through os file APIs, which is why it lives
// under ipc/ (CS-16). The cgroup root and the self-cgroup path are injectable so
// a spec drives the whole capability over a temporary directory.
package sandbox

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Cgroup filesystem names and the controllers a session cgroup needs. These are
// values (CS-13), named once here.
const (
	cgroupProcsFile          = "cgroup.procs"
	cgroupKillFile           = "cgroup.kill"
	cgroupControllersFile    = "cgroup.controllers"
	cgroupSubtreeControlFile = "cgroup.subtree_control"
	cpuStatFile              = "cpu.stat"
	memoryPeakFile           = "memory.peak"

	defaultCgroupRoot     = "/sys/fs/cgroup"
	defaultProcSelfCgroup = "/proc/self/cgroup"
	procSelfCgroupPrefix  = "0::"
	procRoot              = "/proc"
	procCgroupFile        = "cgroup"

	sessionsLeaf      = "sessions"
	supervisorLeaf    = "supervisor"
	enableControllers = "+cpu +memory +pids"

	cgroupDirMode  = 0o755
	policyFileMode = 0o600
	privateTmpMode = 0o700
	killSignal     = "1"

	meminfoPath    = "/proc/meminfo"
	meminfoKey     = "MemTotal:"
	meminfoKiBUnit = 1024
)

// requiredControllers are the controllers the delegated subtree must offer for
// the per-session limits to bind.
var requiredControllers = []string{"cpu", "memory", "pids"}

var (
	// ErrNotDelegated reports a harness that cannot create session cgroups,
	// naming the operator's fix.
	ErrNotDelegated = errors.New("sandbox: the harness is not in a delegated cgroup; the operator must restart it with " +
		"`systemd-run --user --scope -p Delegate=yes --unit csf-harness csf serve <its flags>`")
	// ErrNoLauncher reports a manager built without the launcher binary.
	ErrNoLauncher = errors.New("sandbox: a launcher binary path is required")
	// ErrNotPrepared reports Open before Prepare established the sessions cgroup.
	ErrNotPrepared = errors.New("sandbox: Prepare must establish the sessions cgroup before a session opens")
)

// Manager owns the session cgroups and writes launcher policies. It is the
// harness's sandbox capability, granted the launcher and proxy binaries and the
// host capacity the per-session limits derive from.
type Manager struct {
	cgroupRoot    string
	procSelf      string
	launcher      string
	proxy         string
	allowedImages []string
	readOnlyRoots []string
	capacity      Capacity
	sessionsRoot  string
}

// ManagerOption configures a [Manager].
type ManagerOption func(manager *Manager) error

// WithProxy grants the Docker proxy binary a session points DOCKER_HOST at.
func WithProxy(path string) ManagerOption {
	return func(manager *Manager) error {
		manager.proxy = path
		return nil
	}
}

// WithAllowedImages lists the image references a session's containers may run:
// the digests the repository pins.
func WithAllowedImages(images ...string) ManagerOption {
	return func(manager *Manager) error {
		manager.allowedImages = append([]string{}, images...)
		return nil
	}
}

// WithReadOnlyRoots lists the system and toolchain paths a session may read and
// execute but not write.
func WithReadOnlyRoots(roots ...string) ManagerOption {
	return func(manager *Manager) error {
		manager.readOnlyRoots = append([]string{}, roots...)
		return nil
	}
}

// WithCapacity sets the host capacity the per-session limits derive from,
// instead of reading it from the host.
func WithCapacity(capacity Capacity) ManagerOption {
	return func(manager *Manager) error {
		manager.capacity = capacity
		return nil
	}
}

// WithCgroupRoot and WithProcSelfCgroup are test seams: they point the manager
// at a directory and file standing in for the cgroup filesystem.
func WithCgroupRoot(root string) ManagerOption {
	return func(manager *Manager) error {
		manager.cgroupRoot = root
		return nil
	}
}

func WithProcSelfCgroup(path string) ManagerOption {
	return func(manager *Manager) error {
		manager.procSelf = path
		return nil
	}
}

// WithSessionsRoot sets the cgroup directory per-session cgroups are created
// under directly, instead of letting [Manager.Prepare] establish it. It is for a
// caller that already owns a prepared sessions cgroup, and for specs that drive
// Open over a temporary directory.
func WithSessionsRoot(path string) ManagerOption {
	return func(manager *Manager) error {
		manager.sessionsRoot = path
		return nil
	}
}

// NewManager builds the sandbox capability. The launcher path is required; the
// capacity defaults to the host's when not set.
func NewManager(launcher string, options ...ManagerOption) (*Manager, error) {
	if launcher == "" {
		return nil, ErrNoLauncher
	}
	manager := &Manager{
		cgroupRoot: defaultCgroupRoot,
		procSelf:   defaultProcSelfCgroup,
		launcher:   launcher,
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("sandbox: nil option")
		}
		if err := option(manager); err != nil {
			return nil, err
		}
	}
	if manager.capacity.Cores == 0 && manager.capacity.MemoryBytes == 0 {
		manager.capacity = hostCapacity()
	}
	return manager, nil
}

// Prepare establishes the sessions cgroup under the harness's own delegated
// cgroup: it moves the harness into a supervisor leaf, enables the controllers
// on its subtree, and creates the sessions subtree the per-session cgroups live
// under. It reports [ErrNotDelegated] when the harness's cgroup does not offer
// the controllers, which is the case until the operator restarts it delegated.
func (manager *Manager) Prepare() error {
	own, err := delegatedCgroup(manager.procSelf, manager.cgroupRoot)
	if err != nil {
		return err
	}
	supervisor := filepath.Join(own, supervisorLeaf)
	if err := os.Mkdir(supervisor, cgroupDirMode); err != nil && !os.IsExist(err) {
		return fmt.Errorf("%w: create %s: %w", ErrNotDelegated, supervisor, err)
	}
	if err := os.WriteFile(filepath.Join(supervisor, cgroupProcsFile), []byte(strconv.Itoa(os.Getpid())), 0); err != nil {
		return fmt.Errorf("%w: move the harness into %s: %w", ErrNotDelegated, supervisor, err)
	}
	if err := os.WriteFile(filepath.Join(own, cgroupSubtreeControlFile), []byte(enableControllers), 0); err != nil {
		return fmt.Errorf("%w: enable controllers on %s: %w", ErrNotDelegated, own, err)
	}
	sessions := filepath.Join(own, sessionsLeaf)
	if err := os.Mkdir(sessions, cgroupDirMode); err != nil && !os.IsExist(err) {
		return fmt.Errorf("%w: create %s: %w", ErrNotDelegated, sessions, err)
	}
	if err := os.WriteFile(filepath.Join(sessions, cgroupSubtreeControlFile), []byte(enableControllers), 0); err != nil {
		return fmt.Errorf("%w: enable controllers on %s: %w", ErrNotDelegated, sessions, err)
	}
	manager.sessionsRoot = sessions
	return nil
}

// SessionParams is one session's confinement request.
type SessionParams struct {
	// ID is the assignment identifier; it names the cgroup and labels the
	// session's containers.
	ID string
	// PolicyPath is where the launcher's policy JSON is written, under the run
	// directory.
	PolicyPath string
	// ReadWrite are the paths the session may write: its worktree, run
	// directory and caches. They are also the paths a bind mount's source may
	// lie within.
	ReadWrite []string
	// PrivateTmp, when set, is a per-session writable tmp this capability
	// creates and adds to the writable set, so the executor's TMPDIR need not
	// expose the host's.
	PrivateTmp string
	// ReadOnly are extra read-only paths beyond the manager's system roots.
	ReadOnly []string
	// ProxySocket is the Docker proxy socket for this session; empty grants no
	// container access.
	ProxySocket string
}

// Open creates the session's cgroup and writes its launcher policy, returning
// the handle that builds the launch prefix and later cancels and measures it.
func (manager *Manager) Open(params SessionParams) (*Session, error) {
	if manager.sessionsRoot == "" {
		return nil, ErrNotPrepared
	}
	if params.ID == "" || params.PolicyPath == "" {
		return nil, fmt.Errorf("sandbox: a session needs an id and a policy path")
	}
	cgroup := filepath.Join(manager.sessionsRoot, params.ID)
	if err := os.Mkdir(cgroup, cgroupDirMode); err != nil && !os.IsExist(err) {
		return nil, fmt.Errorf("sandbox: create session cgroup %s: %w", cgroup, err)
	}
	readWrite := append([]string{}, params.ReadWrite...)
	if params.PrivateTmp != "" {
		if err := os.MkdirAll(params.PrivateTmp, privateTmpMode); err != nil {
			return nil, fmt.Errorf("sandbox: create private tmp %s: %w", params.PrivateTmp, err)
		}
		readWrite = append(readWrite, params.PrivateTmp)
	}
	policy := SessionPolicy{
		SessionID:         params.ID,
		Cgroup:            cgroup,
		Limits:            DeriveLimits(manager.capacity),
		ReadWritePaths:    readWrite,
		ReadOnlyPaths:     append(append([]string{}, manager.readOnlyRoots...), params.ReadOnly...),
		DockerProxySocket: params.ProxySocket,
	}
	if err := writePolicy(params.PolicyPath, policy); err != nil {
		return nil, err
	}
	return &Session{
		id:            params.ID,
		cgroup:        cgroup,
		policyPath:    params.PolicyPath,
		launcher:      manager.launcher,
		proxy:         manager.proxy,
		proxySocket:   params.ProxySocket,
		allowedImages: manager.allowedImages,
		sessionPaths:  params.ReadWrite,
	}, nil
}

// Session is one session's sandbox: its cgroup, its policy and the commands and
// receipts around it.
type Session struct {
	id            string
	cgroup        string
	policyPath    string
	launcher      string
	proxy         string
	proxySocket   string
	allowedImages []string
	sessionPaths  []string
}

// Cgroup is the session's cgroup directory.
func (session *Session) Cgroup() string { return session.cgroup }

// LaunchPrefix is the argv prefix the harness puts in front of the turn
// executor: the launcher, the policy and the `--` that ends the launcher's own
// arguments.
func (session *Session) LaunchPrefix() []string {
	return []string{session.launcher, flagPolicy, session.policyPath, argSeparator}
}

// DockerProxyCommand is the per-session Docker proxy invocation: the socket the
// session's DOCKER_HOST points at, the real Engine socket, the session label,
// the pinned images and the paths binds may target.
func (session *Session) DockerProxyCommand(upstream string) []string {
	command := []string{session.proxy, flagSocket, session.proxySocket, flagUpstream, upstream, flagSession, session.id}
	for _, image := range session.allowedImages {
		command = append(command, flagAllowImage, image)
	}
	for _, path := range session.sessionPaths {
		command = append(command, flagSessionPath, path)
	}
	return command
}

// Kill sends SIGKILL to every process in the session's cgroup with one write to
// cgroup.kill. A cgroup already gone is treated as killed.
func (session *Session) Kill() error {
	err := os.WriteFile(filepath.Join(session.cgroup, cgroupKillFile), []byte(killSignal), 0)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("sandbox: kill session cgroup %s: %w", session.cgroup, err)
	}
	return nil
}

// Usage reads the session's cpu and memory receipt. Call it after the executor
// has exited and before [Session.Remove].
func (session *Session) Usage() (Usage, error) { return ReadCgroupUsage(session.cgroup) }

// ReadCgroupUsage reads the cpu and memory receipt of the cgroup directory
// cgroup.
func ReadCgroupUsage(cgroup string) (Usage, error) {
	cpuStat, err := os.ReadFile(filepath.Join(cgroup, cpuStatFile))
	if err != nil {
		return Usage{}, fmt.Errorf("sandbox: read %s: %w", cpuStatFile, err)
	}
	memoryPeak, err := os.ReadFile(filepath.Join(cgroup, memoryPeakFile))
	if err != nil {
		return Usage{}, fmt.Errorf("sandbox: read %s: %w", memoryPeakFile, err)
	}
	return ParseUsage(cpuStat, memoryPeak)
}

// ProcessCgroupUsage reads the receipt of the cgroup process pid runs in, as
// /proc/<pid>/cgroup names it under the host's cgroup root: a container's
// cgroup, read through its init process.
func ProcessCgroupUsage(pid int) (Usage, error) {
	path := filepath.Join(procRoot, strconv.Itoa(pid), procCgroupFile)
	content, err := os.ReadFile(path)
	if err != nil {
		return Usage{}, fmt.Errorf("sandbox: read %s: %w", path, err)
	}
	suffix, found := cgroupSuffix(string(content))
	if !found {
		return Usage{}, fmt.Errorf("sandbox: %s has no unified (0::) entry", path)
	}
	return ReadCgroupUsage(filepath.Join(defaultCgroupRoot, suffix))
}

// Remove deletes the session's cgroup. It must be empty of processes, so Kill
// precedes it and the executor is reaped between them.
func (session *Session) Remove() error {
	if err := os.Remove(session.cgroup); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("sandbox: remove session cgroup %s: %w", session.cgroup, err)
	}
	return nil
}

// delegatedCgroup resolves the harness's own cgroup directory and confirms it
// offers the controllers the session limits need.
func delegatedCgroup(procSelf string, cgroupRoot string) (string, error) {
	content, err := os.ReadFile(procSelf)
	if err != nil {
		return "", fmt.Errorf("%w: read %s: %w", ErrNotDelegated, procSelf, err)
	}
	suffix, found := cgroupSuffix(string(content))
	if !found {
		return "", fmt.Errorf("%w: %s has no unified (0::) entry", ErrNotDelegated, procSelf)
	}
	own := filepath.Join(cgroupRoot, suffix)
	controllers, err := os.ReadFile(filepath.Join(own, cgroupControllersFile))
	if err != nil {
		return "", fmt.Errorf("%w: read controllers of %s: %w", ErrNotDelegated, own, err)
	}
	available := strings.Fields(string(controllers))
	for _, required := range requiredControllers {
		if !contains(available, required) {
			return "", fmt.Errorf("%w: %s does not offer the %s controller", ErrNotDelegated, own, required)
		}
	}
	return own, nil
}

// cgroupSuffix is the path of the unified (0::) entry in /proc/<pid>/cgroup.
func cgroupSuffix(procSelfCgroup string) (string, bool) {
	for _, line := range strings.Split(procSelfCgroup, "\n") {
		if suffix, found := strings.CutPrefix(strings.TrimSpace(line), procSelfCgroupPrefix); found {
			return suffix, true
		}
	}
	return "", false
}

func contains(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func writePolicy(path string, policy SessionPolicy) error {
	content, err := marshalPolicy(policy)
	if err != nil {
		return err
	}
	if err := os.WriteFile(path, content, policyFileMode); err != nil {
		return fmt.Errorf("sandbox: write policy %s: %w", path, err)
	}
	return nil
}

// hostCapacity reads the host's cores and memory. A memory read that fails
// leaves the memory budget at zero, which DeriveLimits reads as the 16 GiB cap.
func hostCapacity() Capacity {
	capacity := Capacity{Cores: runtime.NumCPU()}
	content, err := os.ReadFile(meminfoPath)
	if err != nil {
		return capacity
	}
	for _, line := range strings.Split(string(content), "\n") {
		if !strings.HasPrefix(line, meminfoKey) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) >= 2 {
			if kib, err := strconv.ParseUint(fields[1], 10, 64); err == nil {
				capacity.MemoryBytes = kib * meminfoKiBUnit
			}
		}
	}
	return capacity
}
