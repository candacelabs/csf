// Copyright 2026 Candace Labs

package upgrade

import (
	"fmt"
	"strings"
)

// BinarySource says where the binary an upgrade replaces was found.
type BinarySource string

// The places an upgrade finds the binary it replaces, in the order it looks.
const (
	SourceFlag           BinarySource = "from -binary"
	SourceRecord         BinarySource = "from harness.json"
	SourceProcess        BinarySource = "from /proc/<pid>/exe"
	SourceProcessDeleted BinarySource = `from /proc/<pid>/exe, " (deleted)" stripped`
	SourceSelf           BinarySource = "this csf, as no host is running"

	// DeletedSuffix is what Linux appends to /proc/<pid>/exe once the file
	// the process was started from has been replaced or removed.
	DeletedSuffix = " (deleted)"
)

// BinaryCandidates are the places the installed binary may be named.
type BinaryCandidates struct {
	// Flag is -binary, already absolute; empty when not given.
	Flag string
	// Recorded is the binary path the running host recorded at start.
	Recorded string
	// Running is whether a host is running.
	Running bool
	// ProcessExecutable reads the running host's /proc/<pid>/exe link.
	ProcessExecutable func() (string, error)
	// Self is this command's own executable.
	Self func() (string, error)
}

// ChooseBinary picks the binary an upgrade replaces: -binary, else the path
// the running host recorded, else its /proc/<pid>/exe link with the suffix
// Linux adds to a replaced file stripped, else this command's own. A host's
// executable replaced underneath it still names the path it was started
// from, which is the path to install at.
func ChooseBinary(candidates BinaryCandidates) (string, BinarySource, error) {
	switch {
	case candidates.Flag != "":
		return candidates.Flag, SourceFlag, nil
	case candidates.Recorded != "":
		return candidates.Recorded, SourceRecord, nil
	case candidates.Running:
		if candidates.ProcessExecutable == nil {
			return "", "", fmt.Errorf("%w: no process table to read the host's executable from", ErrInvalidOption)
		}
		link, err := candidates.ProcessExecutable()
		if err != nil {
			return "", "", fmt.Errorf("read the host's executable: %w", err)
		}
		if path, deleted := strings.CutSuffix(link, DeletedSuffix); deleted {
			return path, SourceProcessDeleted, nil
		}
		return link, SourceProcess, nil
	}
	if candidates.Self == nil {
		return "", "", fmt.Errorf("%w: no host is running and this command's executable is unknown", ErrInvalidOption)
	}
	self, err := candidates.Self()
	return self, SourceSelf, err
}

// ExecutablePath is a /proc/<pid>/exe link as a path: the suffix Linux adds
// to a replaced file stripped.
func ExecutablePath(link string) string {
	return strings.TrimSuffix(link, DeletedSuffix)
}
