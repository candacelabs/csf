// Copyright 2026 Candace Labs

package housekeeping

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/candacelabs/csf/ipc/docker"
	"github.com/candacelabs/csf/ipc/proc"
	cronservice "github.com/candacelabs/csf/services/cron"
)

// RecordType says what a [Record] reports.
type RecordType string

const (
	// RecordDeletion is one deletion, or in a dry run one planned deletion:
	// Bytes is what it freed.
	RecordDeletion RecordType = "deletion"
	// RecordFinding is something measured and left because the harness
	// cannot attribute it to an ended session: Bytes is what it holds.
	RecordFinding RecordType = "finding"
	// RecordFloor is a disk_floor measurement: Bytes is the free disk and
	// Detail the floor's derivation.
	RecordFloor RecordType = "floor"
)

// Kind is what a deletion removed.
type Kind string

const (
	KindWorktree        Kind = "worktree"
	KindBazelOutputBase Kind = "bazel_output_base"
	KindBranch          Kind = "branch"
	KindProcess         Kind = "process"
	KindRunnerContainer Kind = "runner_container"
	KindDanglingImages  Kind = "dangling_images"
	KindBuildCache      Kind = "build_cache"
	KindSharedCache     Kind = "shared_cache_entries"
)

// Record is one typed receipt in the harness state directory: a deletion,
// a finding or a floor measurement, with the trigger and occurrence that
// wrote it.
type Record struct {
	Time       time.Time  `json:"time"`
	Type       RecordType `json:"type"`
	Trigger    string     `json:"trigger"`
	Occurrence string     `json:"occurrence"`
	Kind       Kind       `json:"kind,omitempty"`
	What       string     `json:"what"`
	Session    string     `json:"owner_session,omitempty"`
	Bytes      uint64     `json:"bytes"`
	DryRun     bool       `json:"dry_run,omitempty"`
	Detail     string     `json:"detail,omitempty"`
}

// Programs every operation runs through the process capability.
const (
	programDiskFree  = "df"
	programDiskUsage = "du"
	programRemove    = "rm"
	programFind      = "find"
	programKill      = "kill"
	programGit       = "git"
	programGitHub    = "gh"

	sandboxTarget = "/reclaim"
	sandboxUser   = "0:0"
)

// pass is one operation's occurrence: every record it writes carries it.
type pass struct {
	trigger    string
	occurrence string
}

func passOf(occurrence cronservice.Occurrence) pass {
	return pass{trigger: occurrence.TriggerName, occurrence: occurrence.ID}
}

func (housekeeper *Housekeeper) record(at pass, record Record) error {
	record.Time = housekeeper.clock.Now().UTC()
	record.Trigger, record.Occurrence = at.trigger, at.occurrence
	if record.Type == RecordDeletion {
		record.DryRun = housekeeper.dryRun
	}
	if err := housekeeper.sink(record); err != nil {
		return fmt.Errorf("housekeeping: write record: %w", err)
	}
	return nil
}

// finding records something left in place with its size.
func (housekeeper *Housekeeper) finding(ctx context.Context, at pass, path string, reason string) error {
	return housekeeper.record(at, Record{Type: RecordFinding, What: path, Bytes: housekeeper.pathBytes(ctx, path), Detail: reason})
}

// delete logs the planned deletion, runs it unless this is a dry run, and
// records its outcome; remove reports the bytes it freed.
func (housekeeper *Housekeeper) delete(ctx context.Context, at pass, planned Record, remove func(ctx context.Context) (uint64, error)) error {
	housekeeper.logger.Info("housekeeping: deleting", "trigger", at.trigger, "kind", planned.Kind, "what", planned.What,
		"owner_session", planned.Session, "bytes", planned.Bytes, "dry_run", housekeeper.dryRun)
	planned.Type = RecordDeletion
	if !housekeeper.dryRun {
		freed, err := remove(ctx)
		planned.Bytes = freed
		if err != nil {
			planned.Bytes = 0
			planned.Detail = err.Error()
			return errors.Join(err, housekeeper.record(at, planned))
		}
	}
	return housekeeper.record(at, planned)
}

// freeBytes is the space available on the state directory's filesystem.
func (housekeeper *Housekeeper) freeBytes(ctx context.Context) (uint64, error) {
	result, err := housekeeper.launcher.Run(ctx, proc.Command{
		Executable: programDiskFree,
		Arguments:  []string{"--block-size=1", "--output=avail", housekeeper.stateDirectory},
	})
	if err != nil {
		return 0, fmt.Errorf("housekeeping: measure free disk: %w", err)
	}
	fields := strings.Fields(string(result.Stdout))
	if len(fields) == 0 {
		return 0, fmt.Errorf("housekeeping: df printed nothing for %s", housekeeper.stateDirectory)
	}
	free, err := strconv.ParseUint(fields[len(fields)-1], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("housekeeping: df output: %w", err)
	}
	return free, nil
}

// pathBytes is the disk path occupies on its own filesystem. du reports
// what it could read even when part of the tree is unreadable, so a partial
// measure is kept; a path that cannot be measured at all is zero.
func (housekeeper *Housekeeper) pathBytes(ctx context.Context, path string) uint64 {
	result, _ := housekeeper.launcher.Run(ctx, proc.Command{
		Executable: programDiskUsage,
		Arguments:  []string{"--summarize", "--block-size=1", "--one-file-system", "--", path},
	})
	fields := strings.Fields(string(result.Stdout))
	if len(fields) == 0 {
		return 0
	}
	size, err := strconv.ParseUint(fields[0], 10, 64)
	if err != nil {
		return 0
	}
	return size
}

// removeTree removes one recorded path. What this user cannot remove (the
// root-owned files a build container left) is removed in a container that
// mounts only that path, and then the emptied tree is removed again.
func (housekeeper *Housekeeper) removeTree(ctx context.Context, path string) error {
	removal := proc.Command{Executable: programRemove, Arguments: []string{"-rf", "--one-file-system", "--", path}}
	_, err := housekeeper.launcher.Run(ctx, removal)
	if err == nil {
		return nil
	}
	if _, sandboxErr := housekeeper.containers.RunSandboxed(ctx, docker.SandboxSpec{
		Image:   housekeeper.sandboxImage,
		Command: []string{programFind, sandboxTarget, "-mindepth", "1", "-delete"},
		User:    sandboxUser,
		Mounts:  []docker.Mount{{Source: path, Target: sandboxTarget}},
	}); sandboxErr != nil {
		return fmt.Errorf("housekeeping: remove %s: %w", path, errors.Join(err, sandboxErr))
	}
	if _, err := housekeeper.launcher.Run(ctx, removal); err != nil {
		return fmt.Errorf("housekeeping: remove %s after the scoped container: %w", path, err)
	}
	return nil
}
