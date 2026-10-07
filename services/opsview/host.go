// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"errors"
	"fmt"
	stdfs "io/fs"
	"path"
	"runtime"
	"slices"
	"strconv"
	"strings"

	"github.com/moby/moby/api/types/container"
	"github.com/moby/moby/client"

	"github.com/candacelabs/csf/io/ipc/docker"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/harness/session"
)

// IHostContainers is the container capability the host panel is granted:
// list every container, the four state changes, and a usage sample.
// *docker.ContainerHost satisfies it. It has no remove and no volume
// operation, so nothing built on it can remove a container or touch a volume.
//
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=host.go -destination=mock_host_test.go -package=opsview_test
type IHostContainers interface {
	ContainerList(ctx context.Context, options client.ContainerListOptions) (client.ContainerListResult, error)
	ContainerStart(ctx context.Context, id string, options client.ContainerStartOptions) (client.ContainerStartResult, error)
	ContainerStop(ctx context.Context, id string, options client.ContainerStopOptions) (client.ContainerStopResult, error)
	ContainerPause(ctx context.Context, id string, options client.ContainerPauseOptions) (client.ContainerPauseResult, error)
	ContainerUnpause(ctx context.Context, id string, options client.ContainerUnpauseOptions) (client.ContainerUnpauseResult, error)
	SampleContainer(ctx context.Context, id string) (docker.ContainerSample, error)
}

// IHostDisk is the filesystem the state directory lives on; *iofs.HostFiles
// satisfies it.
type IHostDisk interface {
	DiskUsage() (iofs.DiskUsage, error)
}

// ContainerAction is one of the four state changes the panel makes.
type ContainerAction string

const (
	ActionStart   ContainerAction = "start"
	ActionStop    ContainerAction = "stop"
	ActionPause   ContainerAction = "pause"
	ActionUnpause ContainerAction = "unpause"
)

// transition is what an action needs and leaves: the states it applies to,
// the state it ends in, and whether it takes something away, which is what
// asks for a confirm.
type transition struct {
	from        []container.ContainerState
	to          container.ContainerState
	destructive bool
}

var transitions = map[ContainerAction]transition{
	ActionStart:   {from: []container.ContainerState{container.StateExited, container.StateCreated}, to: container.StateRunning},
	ActionStop:    {from: []container.ContainerState{container.StateRunning, container.StatePaused, container.StateRestarting}, to: container.StateExited, destructive: true},
	ActionPause:   {from: []container.ContainerState{container.StateRunning}, to: container.StatePaused, destructive: true},
	ActionUnpause: {from: []container.ContainerState{container.StatePaused}, to: container.StateRunning},
}

// reverse is the action that undoes each action.
var reverse = map[ContainerAction]ContainerAction{
	ActionStart: ActionStop, ActionStop: ActionStart, ActionPause: ActionUnpause, ActionUnpause: ActionPause,
}

// HostGroup is which side of the panel a container is on.
type HostGroup string

const (
	// GroupCSF is CSF work: the harness's own sessions and their build
	// containers, recognized from the harness's run directories.
	GroupCSF HostGroup = "csf"
	// GroupOther is every other container on the machine.
	GroupOther HostGroup = "other"
)

// HostGroups are the groups in the order the panel shows them.
var HostGroups = []HostGroup{GroupCSF, GroupOther}

// The kernel files the gauges read from the process table, and the load
// guard. The guard's factor is the operator's (#360): a run queue half again
// longer than the cores is when the panel offers to shed work.
const (
	loadAverageFile = "loadavg"
	memoryFile      = "meminfo"
	memoryTotal     = "MemTotal:"
	memoryAvailable = "MemAvailable:"
	kibibyte        = 1024
	loadGuardFactor = 1.5
	unmeasured      = -1
	// The kernel's pressure stall information, one file per resource under
	// pressureDirectory; the gauges read the share of the last 10 s some task
	// waited on it.
	PressureCPU       = "cpu"
	PressureMemory    = "memory"
	PressureIO        = "io"
	pressureDirectory = "pressure/"
	pressureSome      = "some"
	pressureAvg10     = "avg10="
)

// PressureResources are the resources the pressure gauges read, in the order
// the panel shows them.
var PressureResources = []string{PressureCPU, PressureMemory, PressureIO}

// ErrNoHostContainers reports host operations built without the container
// capability.
var ErrNoHostContainers = errors.New("host operations: the container capability is required")

// HostGauges are the machine's own numbers: load against cores, memory and
// the state directory's disk.
type HostGauges struct {
	Cores                int     `json:"cores"`
	LoadOneMinute        float64 `json:"load_one_minute"`
	MemoryTotalBytes     uint64  `json:"memory_total_bytes"`
	MemoryAvailableBytes uint64  `json:"memory_available_bytes"`
	DiskTotalBytes       uint64  `json:"disk_total_bytes"`
	DiskFreeBytes        uint64  `json:"disk_free_bytes"`
	// The pressure gauges are the percent of the last 10 s in which some task
	// waited for cpu, memory or io, or -1 where the kernel reports none.
	PressureCPU    float64 `json:"pressure_cpu_some_avg10"`
	PressureMemory float64 `json:"pressure_memory_some_avg10"`
	PressureIO     float64 `json:"pressure_io_some_avg10"`
	// Overloaded is true when the load passed the guard: 1.5 times the cores.
	Overloaded bool `json:"overloaded"`
	// Findings are the gauges that could not be read.
	Findings []string `json:"findings,omitempty"`
}

// HostContainer is one container on the machine.
type HostContainer struct {
	ID      string    `json:"id"`
	Name    string    `json:"name"`
	Image   string    `json:"image"`
	Project string    `json:"project,omitempty" jsonschema:"the Compose project, when the container has one"`
	State   string    `json:"state"`
	Status  string    `json:"status"`
	Group   HostGroup `json:"group"`
	// Assignment is the harness run a CSF work container belongs to.
	Assignment string `json:"assignment,omitempty"`
	Protected  bool   `json:"protected"`
	// CPUPercent is the share of one cpu used since the previous reading, or
	// -1 before there are two readings.
	CPUPercent  float64 `json:"cpu_percent"`
	MemoryBytes uint64  `json:"memory_bytes"`
}

// HostSnapshot is the machine at one reading.
type HostSnapshot struct {
	Gauges     HostGauges      `json:"gauges"`
	Containers []HostContainer `json:"containers"`
	// Resumes is the resume queue the last restart recorded: the open runs it
	// resumed and the ones the launch check holds, or nil before any restart.
	Resumes *harness.ResumeQueue `json:"resumes,omitempty"`
}

// container finds one container by name.
func (snapshot HostSnapshot) container(name string) (HostContainer, bool) {
	for _, found := range snapshot.Containers {
		if found.Name == name {
			return found, true
		}
	}
	return HostContainer{}, false
}

// ContainerChange is one state change of one container.
type ContainerChange struct {
	Name   string          `json:"name"`
	Action ContainerAction `json:"action"`
	// From is the state the container was in when the change was planned.
	From string `json:"from"`
}

// readGauges reads load and memory from the process table and the disk from
// the state directory's filesystem; a reading that fails is a finding rather
// than an error, so the panel still shows the containers.
func readGauges(processes iofs.IFiles, disk IHostDisk) HostGauges {
	gauges := HostGauges{Cores: runtime.NumCPU(), PressureCPU: unmeasured, PressureMemory: unmeasured, PressureIO: unmeasured}
	if processes != nil {
		gauges.PressureCPU = PressureSomeAvg10(processes, PressureCPU)
		gauges.PressureMemory = PressureSomeAvg10(processes, PressureMemory)
		gauges.PressureIO = PressureSomeAvg10(processes, PressureIO)
		if load, err := processes.ReadFile(loadAverageFile); err != nil {
			gauges.Findings = append(gauges.Findings, err.Error())
		} else if fields := strings.Fields(string(load)); len(fields) > 0 {
			gauges.LoadOneMinute, _ = strconv.ParseFloat(fields[0], 64)
		}
		if memory, err := processes.ReadFile(memoryFile); err != nil {
			gauges.Findings = append(gauges.Findings, err.Error())
		} else {
			gauges.MemoryTotalBytes, gauges.MemoryAvailableBytes = memoryField(string(memory), memoryTotal), memoryField(string(memory), memoryAvailable)
		}
	}
	if disk != nil {
		if usage, err := disk.DiskUsage(); err != nil {
			gauges.Findings = append(gauges.Findings, err.Error())
		} else {
			gauges.DiskTotalBytes, gauges.DiskFreeBytes = usage.TotalBytes, usage.FreeBytes
		}
	}
	gauges.Overloaded = gauges.LoadOneMinute > loadGuardFactor*float64(gauges.Cores)
	return gauges
}

// PressureSomeAvg10 reads the "some avg10=" field of one resource's
// /proc/pressure file from the process table, as a percent, or -1 when the
// kernel reports no pressure information for it.
func PressureSomeAvg10(processes iofs.IFiles, resource string) float64 {
	content, err := processes.ReadFile(pressureDirectory + resource)
	if err != nil {
		return unmeasured
	}
	for _, line := range strings.Split(string(content), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != pressureSome {
			continue
		}
		if value, found := strings.CutPrefix(fields[1], pressureAvg10); found {
			if parsed, err := strconv.ParseFloat(value, 64); err == nil {
				return parsed
			}
		}
	}
	return unmeasured
}

// memoryField reads one "Key: N kB" line of /proc/meminfo, in bytes.
func memoryField(meminfo string, key string) uint64 {
	for _, line := range strings.Split(meminfo, "\n") {
		if fields := strings.Fields(line); len(fields) >= 2 && fields[0] == key {
			kib, _ := strconv.ParseUint(fields[1], 10, 64)
			return kib * kibibyte
		}
	}
	return 0
}

// classify reads one container from the Engine's summary. It is CSF work when
// the harness's records claim it: it carries a session container's
// assignment label naming a run directory, or it mounts a path inside one,
// which is how a session's build containers are recognized.
func classify(summary container.Summary, stateDirectory string, runs map[string]bool, protected map[string]bool) HostContainer {
	found := HostContainer{
		ID: summary.ID, Image: summary.Image, Project: summary.Labels[composeProjectLabel],
		State: string(summary.State), Status: summary.Status, Group: GroupOther, CPUPercent: unmeasured,
	}
	if len(summary.Names) > 0 {
		found.Name = strings.TrimPrefix(summary.Names[0], "/")
	}
	found.Protected = protected[found.Name]
	if assignment := summary.Labels[session.LabelAssignment]; runs[assignment] {
		found.Group, found.Assignment = GroupCSF, assignment
		return found
	}
	if stateDirectory == "" {
		return found
	}
	for _, mount := range summary.Mounts {
		relative, inside := strings.CutPrefix(mount.Source, stateDirectory+"/")
		if !inside {
			continue
		}
		if assignment, _, _ := strings.Cut(relative, "/"); runs[assignment] {
			found.Group, found.Assignment = GroupCSF, assignment
			return found
		}
	}
	return found
}

// composeProjectLabel is the label Docker Compose puts its project name in.
const composeProjectLabel = "com.docker.compose.project"

// cpuPercent is the share of one cpu a container used between two samples.
func cpuPercent(previous docker.ContainerSample, next docker.ContainerSample) float64 {
	if previous.SystemNanoseconds == 0 || next.SystemNanoseconds <= previous.SystemNanoseconds || next.CPUNanoseconds < previous.CPUNanoseconds {
		return unmeasured
	}
	cpus := float64(next.OnlineCPUs)
	if cpus == 0 {
		cpus = float64(runtime.NumCPU())
	}
	return float64(next.CPUNanoseconds-previous.CPUNanoseconds) / float64(next.SystemNanoseconds-previous.SystemNanoseconds) * cpus * 100
}

// orderContainers puts running and paused containers first, then by name.
func orderContainers(containers []HostContainer) {
	slices.SortStableFunc(containers, func(a, b HostContainer) int {
		if rank := liveRank(a.State) - liveRank(b.State); rank != 0 {
			return rank
		}
		return strings.Compare(a.Name, b.Name)
	})
}

func liveRank(state string) int {
	switch container.ContainerState(state) {
	case container.StateRunning, container.StateRestarting:
		return 0
	case container.StatePaused:
		return 1
	}
	return 2
}

// ErrProtected reports a change to a container the operator protected.
var ErrProtected = errors.New("protected by the operator")

// planControl plans one action on named containers. A container already
// where the action would leave it is not a change; one the action cannot
// apply to, one that does not exist and one the operator protected refuse
// the whole plan.
func planControl(snapshot HostSnapshot, action ContainerAction, names []string) ([]ContainerChange, error) {
	rule, known := transitions[action]
	if !known {
		return nil, fmt.Errorf("%q is not start, stop, pause or unpause", action)
	}
	if len(names) == 0 {
		return nil, errors.New("name at least one container")
	}
	var changes []ContainerChange
	for _, name := range names {
		found, exists := snapshot.container(name)
		switch {
		case !exists:
			return nil, fmt.Errorf("no container is named %q", name)
		case found.Protected && action != ActionStart && action != ActionUnpause:
			return nil, fmt.Errorf("%s: %w", name, ErrProtected)
		case found.State == string(rule.to):
			continue
		case !slices.Contains(rule.from, container.ContainerState(found.State)):
			return nil, fmt.Errorf("%s is %s; %s applies to a container that is %s", name, found.State, action, joinStates(rule.from))
		}
		changes = append(changes, ContainerChange{Name: name, Action: action, From: found.State})
	}
	return changes, nil
}

// groupNames is every container of group the action applies to, the
// protected ones left out of anything that takes them away.
func groupNames(snapshot HostSnapshot, group HostGroup, action ContainerAction) []string {
	rule := transitions[action]
	var names []string
	for _, found := range snapshot.Containers {
		if found.Group != group || !slices.Contains(rule.from, container.ContainerState(found.State)) || (rule.destructive && found.Protected) {
			continue
		}
		names = append(names, found.Name)
	}
	return names
}

// planProfile is the diff a profile makes: each container it keeps that is
// not running starts (or unpauses), and when it stops the rest, each running
// or paused container it does not keep stops, the operator's protected ones
// and, with KeepCSFWork, CSF work left alone.
func planProfile(snapshot HostSnapshot, profile HostProfile) []ContainerChange {
	keep := map[string]bool{}
	for _, name := range profile.Keep {
		keep[name] = true
	}
	var changes []ContainerChange
	for _, found := range snapshot.Containers {
		state := container.ContainerState(found.State)
		switch {
		case keep[found.Name] && state == container.StatePaused:
			changes = append(changes, ContainerChange{Name: found.Name, Action: ActionUnpause, From: found.State})
		case keep[found.Name] && slices.Contains(transitions[ActionStart].from, state):
			changes = append(changes, ContainerChange{Name: found.Name, Action: ActionStart, From: found.State})
		case keep[found.Name], !profile.StopOthers, found.Protected, profile.KeepCSFWork && found.Group == GroupCSF:
		case slices.Contains(transitions[ActionStop].from, state):
			changes = append(changes, ContainerChange{Name: found.Name, Action: ActionStop, From: found.State})
		}
	}
	return changes
}

// planUndo reverses an applied change set: what it stopped starts, what it
// started stops. A container that is gone, or already back where it was, is
// not a change.
func planUndo(snapshot HostSnapshot, applied []ContainerChange) []ContainerChange {
	var changes []ContainerChange
	for _, change := range applied {
		action := reverse[change.Action]
		found, exists := snapshot.container(change.Name)
		if !exists || !slices.Contains(transitions[action].from, container.ContainerState(found.State)) {
			continue
		}
		changes = append(changes, ContainerChange{Name: change.Name, Action: action, From: found.State})
	}
	return changes
}

// needsConfirm names what a change set asks the operator to confirm: every
// container in it when any change takes something away or it changes more
// than one container, and nothing for one start or unpause.
func needsConfirm(changes []ContainerChange) []string {
	destructive := false
	for _, change := range changes {
		destructive = destructive || transitions[change.Action].destructive
	}
	if !destructive && len(changes) < 2 {
		return nil
	}
	return changeNames(changes)
}

func changeNames(changes []ContainerChange) []string {
	names := make([]string, 0, len(changes))
	for _, change := range changes {
		names = append(names, change.Name)
	}
	slices.Sort(names)
	return names
}

// describe says what a change set does, in the diff's words.
func describe(changes []ContainerChange) string {
	if len(changes) == 0 {
		return "Nothing to change: the host already matches."
	}
	var stops, starts, pauses []string
	for _, change := range changes {
		switch change.Action {
		case ActionStop:
			stops = append(stops, change.Name)
		case ActionPause:
			pauses = append(pauses, change.Name)
		default:
			starts = append(starts, change.Name)
		}
	}
	var parts []string
	for _, part := range []struct {
		verb  string
		names []string
	}{{"stops", stops}, {"pauses", pauses}, {"starts", starts}} {
		if len(part.names) > 0 {
			slices.Sort(part.names)
			parts = append(parts, fmt.Sprintf("%s %d: %s", part.verb, len(part.names), strings.Join(part.names, ", ")))
		}
	}
	return strings.Join(parts, "; ") + "."
}

func joinStates(states []container.ContainerState) string {
	names := make([]string, 0, len(states))
	for _, state := range states {
		names = append(names, string(state))
	}
	return strings.Join(names, " or ")
}

// runDirectories names the harness's run directories, each a directory
// holding a run record: the records CSF work is recognized from.
func runDirectories(runs iofs.IFiles) map[string]bool {
	names := map[string]bool{}
	if runs == nil {
		return names
	}
	entries, err := runs.ReadDir(rootName)
	if err != nil {
		return names
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, err := stdfs.Stat(runs, path.Join(entry.Name(), session.RunStateFile)); err == nil {
			names[entry.Name()] = true
		}
	}
	return names
}
