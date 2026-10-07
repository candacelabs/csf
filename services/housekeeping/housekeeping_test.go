// Copyright 2026 Candace Labs

package housekeeping_test

import (
	"context"
	"fmt"
	stdfs "io/fs"
	"slices"
	"strconv"
	"strings"
	"testing/fstest"
	"time"

	"github.com/moby/moby/api/types/build"
	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/api/types/image"
	"github.com/moby/moby/client"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/io/ipc/docker"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/eventually"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime"
	cronservice "github.com/candacelabs/csf/services/cron"
	"github.com/candacelabs/csf/services/cron/crontest"
	"github.com/candacelabs/csf/services/housekeeping"
	"github.com/candacelabs/csf/services/housekeeping/mocks"
)

const (
	state          = "/state"
	repository     = "/repository"
	endedID        = "ended-session"
	activeID       = "active-session"
	unrecordedID   = "unrecorded-run"
	endedRun       = state + "/" + endedID
	activeRun      = state + "/" + activeID
	endedWorktree  = endedRun + "/worktree"
	activeWorktree = activeRun + "/worktree"
	outputBase     = endedRun + "/bazel"
	pullRequest    = "https://example.invalid/pull/1"
	hostPID        = 900
	selfPID        = 901
)

var start = time.Date(2026, time.October, 2, 12, 0, 0, 0, time.UTC)

// graceBudget is how long a spec waits for a pass to reach, and then leave,
// its wait on the manual clock.
var graceBudget = eventually.Budget{Within: 10 * time.Second}

// host is the machine the specs grant: a launcher double that answers the
// programs housekeeping runs, the Docker and admission doubles, the state
// directory and the process table as in-memory trees, and the record sink.
type host struct {
	launcher   *MockILauncher
	containers *mocks.MockIContainers
	admission  *mocks.MockIAdmission
	state      fstest.MapFS
	processes  fstest.MapFS
	clock      *clock.ManualClock
	sessions   *harnessv1.ListAgentSessionsResponse
	records    []housekeeping.Record
	commands   []proc.Command
	free       []uint64
	sizes      map[string]uint64
	unpushed   map[string]string
	prState    string
	// git answers a git command by its arguments after -C DIR.
	git map[string]answer
	// groups is each added process's group; a group in stubborn ignores
	// SIGTERM, every other one exits on it.
	groups   map[int]int
	stubborn map[int]bool
}

// answer is what one scripted program prints, or how it fails.
type answer struct {
	stdout string
	err    error
}

func newHost() *host {
	controller := gomock.NewController(GinkgoT())
	machine := &host{
		launcher:   NewMockILauncher(controller),
		containers: mocks.NewMockIContainers(controller),
		admission:  mocks.NewMockIAdmission(controller),
		clock:      clock.NewManualClock(start),
		free:       []uint64{1 << 40},
		sizes:      map[string]uint64{endedWorktree: 40 << 20, outputBase: 2 << 30, endedRun: 3 << 30, activeRun: 1 << 30},
		unpushed:   map[string]string{},
		git:        map[string]answer{},
		prState:    "MERGED",
		groups:     map[int]int{},
		stubborn:   map[int]bool{},
		state: fstest.MapFS{
			endedID + "/run.json":                  {Data: []byte(`{"repository":"` + repository + `"}`)},
			endedID + "/events.jsonl":              {Data: []byte("{}\n")},
			endedID + "/worktree/main.go":          {Data: []byte("package main")},
			endedID + "/bazel/execroot/out":        {Data: []byte("artifact")},
			endedID + "/ocaml-toolchain/bin/ocaml": {Data: []byte("toolchain")},
			activeID + "/run.json":                 {Data: []byte(`{"repository":"` + repository + `"}`)},
			activeID + "/worktree/main.go":         {Data: []byte("package main")},
			activeID + "/bazel/execroot/out":       {Data: []byte("artifact")},
			unrecordedID + "/worktree/main.go":     {Data: []byte("package main")},
			"bazel-disk-cache/cas/aa/oldest":       {Data: make([]byte, 300), ModTime: start.Add(-3 * time.Hour)},
			"bazel-disk-cache/cas/bb/middle":       {Data: make([]byte, 300), ModTime: start.Add(-2 * time.Hour)},
			"bazel-disk-cache/ac/cc/newest":        {Data: make([]byte, 300), ModTime: start.Add(-time.Hour)},
		},
		processes: fstest.MapFS{
			"self":                             {Data: []byte(fmt.Sprint(selfPID)), Mode: stdfs.ModeSymlink},
			fmt.Sprintf("%d/stat", hostPID):    {Data: []byte(fmt.Sprintf("%d (harness) S 1 %d %d", hostPID, hostPID, hostPID))},
			fmt.Sprintf("%d/cwd", hostPID):     {Data: []byte("/elsewhere"), Mode: stdfs.ModeSymlink},
			fmt.Sprintf("%d/cmdline", hostPID): {Data: []byte("harness\x00serve\x00")},
			fmt.Sprintf("%d/stat", selfPID):    {Data: []byte(fmt.Sprintf("%d (harness) S 1 %d %d", selfPID, selfPID, selfPID))},
			fmt.Sprintf("%d/cwd", selfPID):     {Data: []byte("/elsewhere"), Mode: stdfs.ModeSymlink},
			fmt.Sprintf("%d/cmdline", selfPID): {Data: []byte("harness\x00housekeeping\x00")},
		},
		sessions: &harnessv1.ListAgentSessionsResponse{
			HostPid: hostPID,
			Sessions: []*harnessv1.AgentSessionState{
				{AssignmentId: endedID, Phase: harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED,
					Worktree: endedWorktree, Branch: "dev/ended", PullRequestUrl: pullRequest},
				{AssignmentId: activeID, Phase: harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING,
					Worktree: activeWorktree, Branch: "dev/active"},
			},
		},
	}
	machine.launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(machine.run).AnyTimes()
	return machine
}

// run answers one program the way this host would.
func (machine *host) run(_ context.Context, command proc.Command) (proc.Result, error) {
	machine.commands = append(machine.commands, command)
	arguments := strings.Join(command.Arguments, " ")
	switch command.Executable {
	case "df":
		free := machine.free[0]
		if len(machine.free) > 1 {
			machine.free = machine.free[1:]
		}
		return proc.Result{Stdout: []byte(fmt.Sprintf("Avail\n%d\n", free))}, nil
	case "du":
		target := command.Arguments[len(command.Arguments)-1]
		return proc.Result{Stdout: []byte(fmt.Sprintf("%d\t%s\n", machine.sizes[target], target))}, nil
	case "gh":
		return proc.Result{Stdout: []byte(machine.prState + "\n")}, nil
	case "kill":
		machine.signal(command.Arguments[0], command.Arguments[len(command.Arguments)-1])
	case "git":
		if scripted, exists := machine.git[strings.Join(command.Arguments[2:], " ")]; exists {
			return proc.Result{Stdout: []byte(scripted.stdout)}, scripted.err
		}
		switch {
		case strings.Contains(arguments, " log "):
			return proc.Result{Stdout: []byte(machine.unpushed[command.Arguments[1]])}, nil
		case strings.Contains(arguments, "branch --list"):
			return proc.Result{Stdout: []byte("  " + command.Arguments[len(command.Arguments)-1] + "\n")}, nil
		}
	}
	return proc.Result{}, nil
}

// addProcess adds a process init adopted.
func (machine *host) addProcess(pid int, group int, directory string, arguments ...string) {
	machine.addChild(pid, 1, group, directory, arguments...)
}

func (machine *host) addChild(pid int, parent int, group int, directory string, arguments ...string) {
	machine.groups[pid] = group
	machine.processes[fmt.Sprintf("%d/stat", pid)] = &fstest.MapFile{Data: []byte(fmt.Sprintf("%d (worker) S %d %d %d", pid, parent, group, group))}
	machine.processes[fmt.Sprintf("%d/cwd", pid)] = &fstest.MapFile{Data: []byte(directory), Mode: stdfs.ModeSymlink}
	machine.processes[fmt.Sprintf("%d/cmdline", pid)] = &fstest.MapFile{Data: []byte(strings.Join(arguments, "\x00") + "\x00")}
}

// signal delivers kill's signal to target, a negated group: every member
// exits on SIGKILL, and on SIGTERM unless its group is stubborn.
func (machine *host) signal(signal string, target string) {
	group, err := strconv.Atoi(strings.TrimPrefix(target, "-"))
	Expect(err).NotTo(HaveOccurred())
	if signal == "-TERM" && machine.stubborn[group] {
		return
	}
	for pid, member := range machine.groups {
		if member != group {
			continue
		}
		delete(machine.groups, pid)
		for _, name := range []string{"stat", "cwd", "cmdline"} {
			delete(machine.processes, fmt.Sprintf("%d/%s", pid, name))
		}
	}
}

func (machine *host) housekeeper(options ...housekeeping.HousekeeperOption) *housekeeping.Housekeeper {
	housekeeper, err := housekeeping.NewHousekeeper(append([]housekeeping.HousekeeperOption{
		housekeeping.WithState(state, machine.state),
		housekeeping.WithSessions(func(_ context.Context) (*harnessv1.ListAgentSessionsResponse, error) { return machine.sessions, nil }),
		housekeeping.WithLauncher(machine.launcher),
		housekeeping.WithContainers(machine.containers),
		housekeeping.WithProcessTable(machine.processes),
		housekeeping.WithClock(machine.clock),
		housekeeping.WithRecordSink(func(record housekeeping.Record) error {
			machine.records = append(machine.records, record)
			return nil
		}),
	}, options...)...)
	Expect(err).NotTo(HaveOccurred())
	return housekeeper
}

// ran reports whether any program ran with these leading arguments.
func (machine *host) ran(executable string, arguments ...string) bool {
	return slices.ContainsFunc(machine.commands, func(command proc.Command) bool {
		return command.Executable == executable && len(command.Arguments) >= len(arguments) &&
			slices.Equal(command.Arguments[:len(arguments)], arguments)
	})
}

func (machine *host) ranNaming(executable string, path string) bool {
	return slices.ContainsFunc(machine.commands, func(command proc.Command) bool {
		return command.Executable == executable && slices.Contains(command.Arguments, path)
	})
}

func (machine *host) recorded(recordType housekeeping.RecordType, what string) *housekeeping.Record {
	for index := range machine.records {
		if machine.records[index].Type == recordType && machine.records[index].What == what {
			return &machine.records[index]
		}
	}
	return nil
}

func (machine *host) expectNoDocker() {
	machine.containers.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return(client.ContainerListResult{}, nil).AnyTimes()
	machine.containers.EXPECT().ImageList(gomock.Any(), gomock.Any()).Return(client.ImageListResult{}, nil).AnyTimes()
	machine.containers.EXPECT().DiskUsage(gomock.Any(), gomock.Any()).Return(client.DiskUsageResult{}, nil).AnyTimes()
}

var _ = Describe("the sessions trigger", func() {
	var machine *host

	BeforeEach(func() { machine = newHost() })

	It("removes an ended session's worktree, output base and merged branch, with a receipt for each", func() {
		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerSessions)).To(Succeed())

		Expect(machine.ran("git", "-C", repository, "worktree", "remove", "--force", endedWorktree)).To(BeTrue())
		Expect(machine.ran("rm", "-rf", "--one-file-system", "--", outputBase)).To(BeTrue())
		Expect(machine.ran("git", "-C", repository, "branch", "-D", "dev/ended")).To(BeTrue())

		worktree := machine.recorded(housekeeping.RecordDeletion, endedWorktree)
		Expect(worktree).NotTo(BeNil())
		Expect(worktree.Kind).To(Equal(housekeeping.KindWorktree))
		Expect(worktree.Session).To(Equal(endedID))
		Expect(worktree.Bytes).To(Equal(uint64(40 << 20)))
		Expect(worktree.Trigger).To(Equal(housekeeping.TriggerSessions))
		Expect(worktree.Occurrence).To(HavePrefix("manual/" + housekeeping.TriggerSessions))
		Expect(worktree.DryRun).To(BeFalse())
		Expect(machine.recorded(housekeeping.RecordDeletion, outputBase)).To(HaveField("Bytes", uint64(2<<30)))
		Expect(machine.recorded(housekeeping.RecordDeletion, "dev/ended")).To(HaveField("Kind", housekeeping.KindBranch))
	})

	It("never touches an active session's worktree or output base", func() {
		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerSessions)).To(Succeed())

		Expect(machine.ranNaming("git", activeWorktree)).To(BeFalse())
		Expect(machine.ranNaming("rm", activeRun+"/bazel")).To(BeFalse())
		Expect(machine.ranNaming("git", "dev/active")).To(BeFalse())
	})

	It("reports an unrecorded run directory and an unrecorded entry of an ended run, and deletes neither", func() {
		machine.sizes[state+"/"+unrecordedID] = 7 << 20
		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerSessions)).To(Succeed())

		Expect(machine.recorded(housekeeping.RecordFinding, state+"/"+unrecordedID)).To(HaveField("Bytes", uint64(7<<20)))
		Expect(machine.recorded(housekeeping.RecordFinding, endedRun+"/ocaml-toolchain")).NotTo(BeNil())
		Expect(machine.ranNaming("rm", state+"/"+unrecordedID)).To(BeFalse())
		Expect(machine.ranNaming("rm", endedRun+"/ocaml-toolchain")).To(BeFalse())
		Expect(machine.ranNaming("git", state+"/"+unrecordedID+"/worktree")).To(BeFalse())
	})

	It("keeps a worktree with commits on no remote, and its branch", func() {
		machine.unpushed[endedWorktree] = "abc1234 work nobody pushed\n"
		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerSessions)).To(Succeed())

		Expect(machine.ran("git", "-C", repository, "worktree", "remove")).To(BeFalse())
		Expect(machine.ranNaming("git", "-D")).To(BeFalse())
		Expect(machine.recorded(housekeeping.RecordFinding, endedWorktree)).To(HaveField("Detail", ContainSubstring("no remote")))
	})

	It("keeps the branch of a pull request that is still open", func() {
		machine.prState = "OPEN"
		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerSessions)).To(Succeed())
		Expect(machine.ranNaming("git", "-D")).To(BeFalse())
	})

	It("terminates what is left of an ended session's process group, and only that group", func() {
		// Group 500's leader, the session's executor, is gone.
		machine.addProcess(501, 500, endedWorktree, "bash")
		machine.addProcess(502, 500, "/", "go", "build", "-o", outputBase+"/bin")
		machine.addProcess(601, 600, activeWorktree, "bash")
		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerSessions)).To(Succeed())

		Expect(machine.ran("kill", "-TERM", "--", "-500")).To(BeTrue())
		Expect(machine.ranNaming("kill", "-600")).To(BeFalse())
		Expect(machine.recorded(housekeeping.RecordDeletion, "process group 500 (2 processes, leader gone)")).To(HaveField("Session", endedID))
		Expect(machine.ran("git", "-C", repository, "worktree", "remove", "--force", endedWorktree)).To(BeTrue())
	})

	It("terminates a tree an ended session detached once init adopted it, then removes the worktree", func() {
		// The script the session backgrounded leads group 700; its parent,
		// the executor, exited, so its parent is init.
		machine.addProcess(700, 700, endedWorktree, "bash", "migrate.sh")
		machine.addProcess(701, 700, "/", "gh", "issue", "create", "--body-file", endedWorktree+"/body.md")
		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerSessions)).To(Succeed())

		Expect(machine.ran("kill", "-TERM", "--", "-700")).To(BeTrue())
		Expect(machine.ran("kill", "-KILL")).To(BeFalse())
		Expect(machine.recorded(housekeeping.RecordDeletion, "process group 700 (2 processes, orphaned to init)")).To(HaveField("Session", endedID))
		Expect(machine.ran("git", "-C", repository, "worktree", "remove", "--force", endedWorktree)).To(BeTrue())
	})

	It("kills an orphaned group that survives SIGTERM once the grace has passed", func() {
		machine.addProcess(700, 700, endedWorktree, "bash", "-c", "trap '' TERM; sleep infinity")
		machine.stubborn[700] = true
		housekeeper := machine.housekeeper()
		finished := make(chan error, 1)
		go func() { finished <- housekeeper.Run(context.Background(), housekeeping.TriggerSessions) }()

		eventually.Await(GinkgoTB(), "the reaper to wait out the grace", graceBudget, machine.clock.Waiting,
			func(waiting int) bool { return waiting == 1 })
		machine.clock.Advance(time.Minute)

		Eventually(finished).WithTimeout(graceBudget.Within).Should(Receive(Succeed()))
		Expect(machine.ran("kill", "-TERM", "--", "-700")).To(BeTrue())
		Expect(machine.ran("kill", "-KILL", "--", "-700")).To(BeTrue())
	})

	It("never signals a detached group whose parent still runs, and keeps the worktree it works in", func() {
		machine.addChild(800, 950, 800, endedWorktree, "/opt/bin/csf", "view")
		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerSessions)).To(Succeed())

		Expect(machine.ran("kill")).To(BeFalse())
		Expect(machine.ran("git", "-C", repository, "worktree", "remove")).To(BeFalse())
		Expect(machine.recorded(housekeeping.RecordFinding, endedRun)).To(HaveField("Detail", ContainSubstring("process 800")))
	})

	It("never signals an orphan that also works for a running session", func() {
		machine.addProcess(700, 700, endedWorktree, "bash")
		machine.addProcess(701, 700, activeWorktree, "bash")
		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerSessions)).To(Succeed())

		Expect(machine.ran("kill")).To(BeFalse())
	})

	It("never signals the harness host and keeps the run directory it works in", func() {
		machine.addProcess(hostPID, hostPID, endedWorktree+" (deleted)", "harness", "serve")
		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerSessions)).To(Succeed())

		Expect(machine.ranNaming("kill", fmt.Sprint(hostPID))).To(BeFalse())
		Expect(machine.ranNaming("kill", fmt.Sprint(-hostPID))).To(BeFalse())
		Expect(machine.ran("git", "-C", repository, "worktree", "remove")).To(BeFalse())
		Expect(machine.ranNaming("rm", outputBase)).To(BeFalse())
		Expect(machine.recorded(housekeeping.RecordFinding, endedRun)).To(HaveField("Detail", ContainSubstring(fmt.Sprint(hostPID))))
	})

	It("removes a root-owned remainder in a container that mounts only the output base", func() {
		removals := 0
		machine.launcher = NewMockILauncher(gomock.NewController(GinkgoT()))
		machine.launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, command proc.Command) (proc.Result, error) {
			if command.Executable == "rm" && slices.Contains(command.Arguments, outputBase) {
				removals++
				if removals == 1 {
					return proc.Result{ExitCode: 1}, &proc.ExitError{Executable: "rm", Code: 1}
				}
			}
			return machine.run(ctx, command)
		}).AnyTimes()
		machine.containers.EXPECT().RunSandboxed(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, spec docker.SandboxSpec) (docker.SandboxResult, error) {
			Expect(spec.Mounts).To(HaveLen(1))
			Expect(spec.Mounts[0].Source).To(Equal(outputBase))
			return docker.SandboxResult{}, nil
		})
		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerSessions)).To(Succeed())
		Expect(removals).To(Equal(2))
		Expect(machine.recorded(housekeeping.RecordDeletion, outputBase)).To(HaveField("Detail", BeEmpty()))
	})

	It("plans without deleting in a dry run", func() {
		machine.addProcess(501, 500, endedWorktree, "bash")
		Expect(machine.housekeeper(housekeeping.WithDryRun()).Run(context.Background(), housekeeping.TriggerSessions)).To(Succeed())

		Expect(machine.ran("rm")).To(BeFalse())
		Expect(machine.ran("kill")).To(BeFalse())
		Expect(machine.ran("git", "-C", repository, "worktree", "remove")).To(BeFalse())
		Expect(machine.ranNaming("git", "-D")).To(BeFalse())
		Expect(machine.recorded(housekeeping.RecordDeletion, endedWorktree)).To(HaveField("DryRun", BeTrue()))
		Expect(machine.recorded(housekeeping.RecordDeletion, outputBase)).To(HaveField("Bytes", uint64(2<<30)))
	})
})

var _ = Describe("the docker trigger", func() {
	var machine *host

	BeforeEach(func() { machine = newHost() })

	It("removes exited runner containers, dangling images and the build cache, never volumes or running containers", func() {
		machine.containers.EXPECT().ContainerList(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, options client.ContainerListOptions) (client.ContainerListResult, error) {
			Expect(options.All).To(BeTrue())
			return client.ContainerListResult{Items: []container.Summary{
				{ID: "exited-runner", Names: []string{"/candace-docker-runner-7"}, State: container.StateExited, SizeRw: 5 << 20},
				{ID: "running-runner", Names: []string{"/candace-docker-runner-8"}, State: container.StateRunning, SizeRw: 9 << 20},
				{ID: "lookalike", Names: []string{"/other-candace-docker-runner"}, State: container.StateExited, SizeRw: 9 << 20},
			}}, nil
		})
		machine.containers.EXPECT().ContainerRemove(gomock.Any(), "exited-runner", client.ContainerRemoveOptions{}).Return(client.ContainerRemoveResult{}, nil)
		machine.containers.EXPECT().ImageList(gomock.Any(), gomock.Any()).Return(client.ImageListResult{Items: []image.Summary{{Size: 100}, {Size: 200}}}, nil)
		machine.containers.EXPECT().ImagePrune(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, options client.ImagePruneOptions) (client.ImagePruneResult, error) {
			Expect(options.Filters).To(HaveKeyWithValue("dangling", map[string]bool{"true": true}))
			return client.ImagePruneResult{Report: image.PruneReport{SpaceReclaimed: 300}}, nil
		})
		machine.containers.EXPECT().DiskUsage(gomock.Any(), gomock.Any()).Return(client.DiskUsageResult{
			BuildCache: client.BuildCacheDiskUsage{TotalCount: 4, TotalSize: 4 << 30, Reclaimable: 3 << 30}}, nil)
		machine.containers.EXPECT().BuildCachePrune(gomock.Any(), client.BuildCachePruneOptions{All: true}).
			Return(client.BuildCachePruneResult{Report: build.CachePruneReport{SpaceReclaimed: 3 << 30}}, nil)

		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerDocker)).To(Succeed())

		Expect(machine.recorded(housekeeping.RecordDeletion, "candace-docker-runner-7")).To(HaveField("Bytes", uint64(5<<20)))
		Expect(machine.recorded(housekeeping.RecordDeletion, "2 dangling images")).To(HaveField("Bytes", uint64(300)))
		Expect(machine.records).To(ContainElement(HaveField("Kind", housekeeping.KindBuildCache)))
	})

	It("plans without removing in a dry run", func() {
		machine.containers.EXPECT().ContainerList(gomock.Any(), gomock.Any()).Return(client.ContainerListResult{Items: []container.Summary{
			{ID: "exited-runner", Names: []string{"/candace-docker-runner-7"}, State: container.StateExited, SizeRw: 5 << 20},
		}}, nil)
		machine.containers.EXPECT().ImageList(gomock.Any(), gomock.Any()).Return(client.ImageListResult{Items: []image.Summary{{Size: 100}}}, nil)
		machine.containers.EXPECT().DiskUsage(gomock.Any(), gomock.Any()).Return(client.DiskUsageResult{
			BuildCache: client.BuildCacheDiskUsage{TotalCount: 1, TotalSize: 10, Reclaimable: 10}}, nil)

		Expect(machine.housekeeper(housekeeping.WithDryRun()).Run(context.Background(), housekeeping.TriggerDocker)).To(Succeed())
		Expect(machine.records).To(HaveLen(3))
		Expect(machine.records).To(HaveEach(HaveField("DryRun", BeTrue())))
	})
})

var _ = Describe("the disk_floor trigger", func() {
	var machine *host

	BeforeEach(func() { machine = newHost() })

	It("records the derived floor and releases admission while free disk is above it", func() {
		machine.admission.EXPECT().ReleaseAdmission()
		Expect(machine.housekeeper(housekeeping.WithAdmission(machine.admission)).Run(context.Background(), housekeeping.TriggerDiskFloor)).To(Succeed())

		floor := machine.recorded(housekeeping.RecordFloor, state)
		Expect(floor).NotTo(BeNil())
		Expect(floor.Detail).To(ContainSubstring(fmt.Sprintf("per-session peak %d bytes (run directory of %s) x (1 running + 1 headroom) = %d bytes", uint64(3<<30), endedID, uint64(6<<30))))
		Expect(machine.ran("rm")).To(BeFalse())
	})

	It("holds admission below the floor and reclaims in order until free disk is above it", func() {
		machine.free = []uint64{1 << 30, 2 << 30, 7 << 30}
		machine.expectNoDocker()
		held := machine.admission.EXPECT().HoldAdmission(gomock.Any())
		machine.admission.EXPECT().ReleaseAdmission().After(held)
		Expect(machine.housekeeper(housekeeping.WithAdmission(machine.admission)).Run(context.Background(), housekeeping.TriggerDiskFloor)).To(Succeed())

		Expect(machine.recorded(housekeeping.RecordDeletion, endedWorktree)).To(HaveField("Trigger", housekeeping.TriggerDiskFloor))
		Expect(machine.ranNaming("rm", "-f")).To(BeFalse())
	})

	It("keeps admission held and fails the occurrence when every step leaves it below the floor", func() {
		machine.free = []uint64{1 << 30}
		machine.expectNoDocker()
		machine.admission.EXPECT().HoldAdmission(gomock.Any())
		err := machine.housekeeper(housekeeping.WithAdmission(machine.admission)).Run(context.Background(), housekeeping.TriggerDiskFloor)
		Expect(err).To(MatchError(housekeeping.ErrBelowFloor))
	})
})

var _ = Describe("the shared_cache trigger", func() {
	It("trims the oldest entries until the cache is within half the space above the floor", func() {
		machine := newHost()
		machine.sizes = map[string]uint64{endedRun: 100}
		// floor 200 (peak 100 x 2); free 0 and 900 bytes cached: target 350.
		machine.free = []uint64{0}
		Expect(machine.housekeeper().Run(context.Background(), housekeeping.TriggerSharedCache)).To(Succeed())

		Expect(machine.ranNaming("rm", state+"/bazel-disk-cache/cas/aa/oldest")).To(BeTrue())
		Expect(machine.ranNaming("rm", state+"/bazel-disk-cache/cas/bb/middle")).To(BeTrue())
		Expect(machine.ranNaming("rm", state+"/bazel-disk-cache/ac/cc/newest")).To(BeFalse())
		Expect(machine.records).To(ContainElement(HaveField("Bytes", uint64(600))))
	})
})

var _ = Describe("the housekeeper", func() {
	It("refuses to be built without its capabilities or with a nil option", func() {
		_, err := housekeeping.NewHousekeeper()
		Expect(err).To(MatchError(housekeeping.ErrMissingCapability))
		_, err = housekeeping.NewHousekeeper(nil)
		Expect(err).To(MatchError(housekeeping.ErrInvalidOption))
		_, err = housekeeping.NewHousekeeper(housekeeping.WithState("", nil))
		Expect(err).To(MatchError(housekeeping.ErrInvalidOption))
	})

	It("refuses to run a trigger it does not declare", func() {
		Expect(newHost().housekeeper().Run(context.Background(), "housekeeping.everything")).To(MatchError(housekeeping.ErrUnknownTrigger))
	})

	It("fires disk_floor from the cron service every five minutes", func() {
		machine := newHost()
		machine.admission.EXPECT().ReleaseAdmission().AnyTimes()
		// The scheduler writes records from its own goroutine.
		fired := make(chan housekeeping.Record, 64)
		housekeeper := machine.housekeeper(housekeeping.WithAdmission(machine.admission),
			housekeeping.WithRecordSink(func(record housekeeping.Record) error {
				select {
				case fired <- record:
				default:
				}
				return nil
			}))
		triggers, err := housekeeper.Triggers()
		Expect(err).NotTo(HaveOccurred())
		scheduler, err := cronservice.NewScheduler(append([]cronservice.Option{
			cronservice.WithStore(crontest.OpenStore(GinkgoT()).Store), cronservice.WithClock(machine.clock)}, triggers...)...)
		Expect(err).NotTo(HaveOccurred())
		hostRuntime, err := runtime.NewHostRuntime()
		Expect(err).NotTo(HaveOccurred())
		Expect(hostRuntime.Mount("cron", scheduler)).To(Succeed())
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() { done <- hostRuntime.Run(ctx) }()
		DeferCleanup(func() {
			cancel()
			Eventually(done).Should(Receive(BeNil()))
		})

		Eventually(machine.clock.Waiting).Should(BeNumerically(">", 0))
		machine.clock.Advance(5 * time.Minute)
		Eventually(fired).Should(Receive(And(
			HaveField("Trigger", housekeeping.TriggerDiskFloor),
			HaveField("Type", housekeeping.RecordFloor))))
	})
})
