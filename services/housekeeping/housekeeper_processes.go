// Copyright 2026 Candace Labs

package housekeeping

import (
	"context"
	"errors"
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/harness/session"
)

// The process table's names, under the granted /proc.
const (
	processSelf        = "self"
	processStat        = "stat"
	processWorkingDir  = "cwd"
	processCommandLine = "cmdline"
	signalTerminate    = "-TERM"
	signalKill         = "-KILL"
	argumentSeparator  = "\x00"
	// initPID adopts every process whose parent exited, so a group leader
	// whose parent is init was orphaned: nothing that still runs owns it.
	initPID = 1
	// killGrace is how long a terminated group has to exit before it is
	// killed.
	killGrace = 10 * time.Second
)

// process is one entry of the process table.
type process struct {
	pid       int
	parent    int
	group     int
	directory string
	arguments []string
}

// processTable is the host's processes as one occurrence read them, and
// the ones it has signaled (or, in a dry run, planned to).
type processTable struct {
	self      int
	processes []process
	signaled  map[int]bool
}

// readProcessTable reads every process it may: one that exits or is not
// readable while being read is left out.
func (housekeeper *Housekeeper) readProcessTable() processTable {
	table := processTable{signaled: map[int]bool{}}
	if self, err := housekeeper.processes.ReadLink(processSelf); err == nil {
		table.self, _ = strconv.Atoi(self)
	}
	entries, err := housekeeper.processes.ReadDir(".")
	if err != nil {
		return table
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := housekeeper.processes.ReadFile(path.Join(entry.Name(), processStat))
		if err != nil {
			continue
		}
		parent, group, ok := parentAndGroup(string(stat))
		if !ok {
			continue
		}
		directory, _ := housekeeper.processes.ReadLink(path.Join(entry.Name(), processWorkingDir))
		commandLine, _ := housekeeper.processes.ReadFile(path.Join(entry.Name(), processCommandLine))
		table.processes = append(table.processes, process{
			pid:       pid,
			parent:    parent,
			group:     group,
			directory: directory,
			arguments: strings.Split(strings.TrimRight(string(commandLine), argumentSeparator), argumentSeparator),
		})
	}
	return table
}

// parentAndGroup reads the parent and the process group from a
// /proc/<pid>/stat line: the second and third fields after the parenthesized
// command name.
func parentAndGroup(stat string) (int, int, bool) {
	closing := strings.LastIndexByte(stat, ')')
	if closing < 0 {
		return 0, 0, false
	}
	fields := strings.Fields(stat[closing+1:])
	if len(fields) < 3 {
		return 0, 0, false
	}
	parent, parentErr := strconv.Atoi(fields[1])
	group, groupErr := strconv.Atoi(fields[2])
	return parent, group, parentErr == nil && groupErr == nil
}

// uses reports whether the process works in directory or names it in its
// arguments, as a Bazel server names its output base.
func (entry process) uses(directory string) bool {
	prefix := directory + "/"
	if strings.Contains(entry.directory+"/", prefix) {
		return true
	}
	for _, argument := range entry.arguments {
		if strings.Contains(argument+"/", prefix) {
			return true
		}
	}
	return false
}

// groupOf is pid's process group, or pid itself when it is not in the
// table, so a protected process is protected even unread.
func (table processTable) groupOf(pid int) int {
	for _, entry := range table.processes {
		if entry.pid == pid {
			return entry.group
		}
	}
	return pid
}

// holding lists the processes still using directory that were not
// signaled.
func (table processTable) holding(directory string) []int {
	var pids []int
	for _, entry := range table.processes {
		if entry.uses(directory) && !table.signaled[entry.pid] {
			pids = append(pids, entry.pid)
		}
	}
	return pids
}

// reapProcesses terminates what is left of an ended session's processes: a
// group that works in the session's run directory or names it, and whose
// leader either no longer runs or was orphaned to init. The harness starts
// each session's turn executor as the leader of a group of its own, so its
// children are left in a leaderless group; a command the session detached (a
// background script, a service, a daemonized build server) leads a group of
// its own whose parent exited, so init adopted it. Orphans are reaped by
// default (#205): nothing owns them once their session ended. A group whose
// leader's parent still runs is never signaled, nor the harness host, this
// process, or a group any member of which works for a running session. A
// group still running [killGrace] after SIGTERM gets SIGKILL.
func (housekeeper *Housekeeper) reapProcesses(ctx context.Context, at pass, id string, runDirectory string, sessions census, table processTable) error {
	protected := map[int]bool{table.groupOf(sessions.hostPID): true, table.groupOf(table.self): true}
	var activeDirectories []string
	for _, state := range sessions.active {
		activeDirectories = append(activeDirectories, session.RunDirectory(housekeeper.stateDirectory, state.GetAssignmentId()))
	}
	running := map[int]process{}
	for _, entry := range table.processes {
		running[entry.pid] = entry
		if slices.ContainsFunc(activeDirectories, entry.uses) {
			protected[entry.group] = true
		}
	}
	var failures []error
	var terminated []int
	for _, entry := range table.processes {
		group := entry.group
		if !entry.uses(runDirectory) || group <= initPID || protected[group] || table.signaled[entry.pid] {
			continue
		}
		owner := "leader gone"
		if leader, live := running[group]; live {
			if leader.parent != initPID {
				continue
			}
			owner = "orphaned to init"
		}
		members := table.members(group)
		for _, member := range members {
			table.signaled[member] = true
		}
		terminated = append(terminated, group)
		failures = append(failures, housekeeper.delete(ctx, at, Record{
			Kind:    KindProcess,
			What:    fmt.Sprintf("process group %d (%d processes, %s)", group, len(members), owner),
			Session: id,
			Detail:  filepath.Clean(runDirectory),
		}, func(ctx context.Context) (uint64, error) {
			return 0, housekeeper.signalGroup(ctx, signalTerminate, group)
		}))
	}
	if len(terminated) > 0 && !housekeeper.dryRun {
		failures = append(failures, housekeeper.killSurvivors(ctx, terminated))
	}
	return errors.Join(failures...)
}

// killSurvivors kills the terminated groups that still run once
// [killGrace] has passed. It waits only when one survived the signal.
func (housekeeper *Housekeeper) killSurvivors(ctx context.Context, groups []int) error {
	if len(housekeeper.readProcessTable().surviving(groups)) == 0 {
		return nil
	}
	select {
	case <-housekeeper.clock.After(killGrace):
	case <-ctx.Done():
		return context.Cause(ctx)
	}
	var failures []error
	for _, group := range housekeeper.readProcessTable().surviving(groups) {
		housekeeper.logger.Warn("housekeeping: process group survived SIGTERM", "group", group, "grace", killGrace)
		failures = append(failures, housekeeper.signalGroup(ctx, signalKill, group))
	}
	return errors.Join(failures...)
}

// signalGroup sends signal to every member of group.
func (housekeeper *Housekeeper) signalGroup(ctx context.Context, signal string, group int) error {
	_, err := housekeeper.launcher.Run(ctx, proc.Command{Executable: programKill, Arguments: []string{signal, "--", "-" + strconv.Itoa(group)}})
	return err
}

// surviving lists the groups that still have a member.
func (table processTable) surviving(groups []int) []int {
	return slices.DeleteFunc(slices.Clone(groups), func(group int) bool { return len(table.members(group)) == 0 })
}

func (table processTable) members(group int) []int {
	var pids []int
	for _, entry := range table.processes {
		if entry.group == group {
			pids = append(pids, entry.pid)
		}
	}
	return pids
}
