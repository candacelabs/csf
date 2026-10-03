// Copyright 2026 Candace Labs

//go:build unix

package proc

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

const (
	procDirectory    = "/proc"
	procStatFilename = "stat"
	// sessionField is the session id's index among the fields after the
	// parenthesized command name in /proc/<pid>/stat: state, ppid, pgrp,
	// session.
	sessionField = 3
)

// isolateGroup puts the child in a process group of its own, whose id is the
// child's pid, so one signal reaches everything it starts.
func isolateGroup(child *exec.Cmd) {
	if child.SysProcAttr == nil {
		child.SysProcAttr = &syscall.SysProcAttr{}
	}
	child.SysProcAttr.Setpgid = true
}

// killGroup kills the child's whole process group.
func killGroup(process *os.Process) error {
	if process == nil {
		return nil
	}
	return ignoreGone(syscall.Kill(-process.Pid, syscall.SIGKILL))
}

// killSession kills every process in the session the child leads, then its
// group. An interactive shell moves background jobs into process groups of
// their own while they keep its session id, so the group alone would leave
// them running. Every pid selected belongs to this session.
func killSession(process *os.Process) error {
	if process == nil {
		return nil
	}
	session := process.Pid
	if entries, err := os.ReadDir(procDirectory); err == nil {
		for _, entry := range entries {
			pid, parseErr := strconv.Atoi(entry.Name())
			if parseErr != nil || pid == session {
				continue
			}
			if member, found := sessionOf(pid); found && member == session {
				_ = syscall.Kill(pid, syscall.SIGKILL)
			}
		}
	}
	return ignoreGone(syscall.Kill(-session, syscall.SIGKILL))
}

// sessionOf reads one process's session id from /proc.
func sessionOf(pid int) (int, bool) {
	body, err := os.ReadFile(filepath.Join(procDirectory, strconv.Itoa(pid), procStatFilename))
	if err != nil {
		return 0, false
	}
	closeName := strings.LastIndexByte(string(body), ')')
	if closeName < 0 {
		return 0, false
	}
	fields := strings.Fields(string(body[closeName+1:]))
	if len(fields) <= sessionField {
		return 0, false
	}
	session, err := strconv.Atoi(fields[sessionField])
	return session, err == nil
}

// ignoreGone treats a group that has already exited as killed.
func ignoreGone(err error) error {
	if errors.Is(err, syscall.ESRCH) {
		return nil
	}
	return err
}
