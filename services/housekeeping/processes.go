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

	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/services/harness/session"
)

// The process table's names, under the granted /proc.
const (
	processSelf        = "self"
	processStat        = "stat"
	processWorkingDir  = "cwd"
	processCommandLine = "cmdline"
	signalTerminate    = "-TERM"
	argumentSeparator  = "\x00"
)

// process is one entry of the process table.
type process struct {
	pid       int
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
		group, ok := processGroup(string(stat))
		if !ok {
			continue
		}
		directory, _ := housekeeper.processes.ReadLink(path.Join(entry.Name(), processWorkingDir))
		commandLine, _ := housekeeper.processes.ReadFile(path.Join(entry.Name(), processCommandLine))
		table.processes = append(table.processes, process{
			pid:       pid,
			group:     group,
			directory: directory,
			arguments: strings.Split(strings.TrimRight(string(commandLine), argumentSeparator), argumentSeparator),
		})
	}
	return table
}

// processGroup reads the process group from a /proc/<pid>/stat line: the
// third field after the parenthesized command name.
func processGroup(stat string) (int, bool) {
	closing := strings.LastIndexByte(stat, ')')
	if closing < 0 {
		return 0, false
	}
	fields := strings.Fields(stat[closing+1:])
	if len(fields) < 3 {
		return 0, false
	}
	group, err := strconv.Atoi(fields[2])
	return group, err == nil
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

// reapProcesses terminates what is left of an ended session's process
// group. The harness starts each session's turn executor as the leader of a
// group of its own, and its children stay in that group; once the session
// ended the leader is gone, so a leftover is a member of a group whose
// leader no longer runs that works in the session's run directory or names
// it. A process that leads a live group of its own (a service a session
// detached, a daemonized build server) is not a leftover and is never
// signaled; neither is the harness host, this process, or a group any
// member of which works for a running session.
func (housekeeper *Housekeeper) reapProcesses(ctx context.Context, at pass, id string, runDirectory string, sessions census, table processTable) error {
	protected := map[int]bool{table.groupOf(sessions.hostPID): true, table.groupOf(table.self): true}
	var activeDirectories []string
	for _, state := range sessions.active {
		activeDirectories = append(activeDirectories, session.RunDirectory(housekeeper.stateDirectory, state.GetAssignmentId()))
	}
	live := map[int]bool{}
	for _, entry := range table.processes {
		live[entry.pid] = true
		if slices.ContainsFunc(activeDirectories, entry.uses) {
			protected[entry.group] = true
		}
	}
	var failures []error
	for _, entry := range table.processes {
		group := entry.group
		if !entry.uses(runDirectory) || group <= 1 || live[group] || protected[group] || table.signaled[entry.pid] {
			continue
		}
		members := table.members(group)
		for _, member := range members {
			table.signaled[member] = true
		}
		target := "-" + strconv.Itoa(group)
		failures = append(failures, housekeeper.delete(ctx, at, Record{
			Kind:    KindProcess,
			What:    fmt.Sprintf("process group %d (%d processes, leader gone)", group, len(members)),
			Session: id,
			Detail:  filepath.Clean(runDirectory),
		}, func(ctx context.Context) (uint64, error) {
			_, err := housekeeper.launcher.Run(ctx, proc.Command{Executable: programKill, Arguments: []string{signalTerminate, "--", target}})
			return 0, err
		}))
	}
	return errors.Join(failures...)
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
