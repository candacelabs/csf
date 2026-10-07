// Copyright 2026 Candace Labs

//go:build !unix

package proc

import (
	"errors"
	"os"
	"os/exec"
)

// isolateGroup has no process groups to create on this platform.
func isolateGroup(_ *exec.Cmd) {}

// killGroup kills the child itself: without process groups there is no
// portable way to reach what it started.
func killGroup(process *os.Process) error {
	if process == nil {
		return nil
	}
	if err := process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	return nil
}

// killSession is killGroup on a platform without sessions.
func killSession(process *os.Process) error { return killGroup(process) }
