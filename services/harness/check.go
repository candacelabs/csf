// Copyright 2026 Candace Labs

package harness

import (
	"bufio"
	"context"
	"fmt"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

// IHostMeasures is what the launch check reads from the host: the measures
// its two bounds are derived from. The host implementation reads the kernel;
// specs grant a double.
//
//go:generate go run go.uber.org/mock/mockgen@v0.6.0 -source=check.go -destination=mocks/mock_check.go -package=mocks
type IHostMeasures interface {
	// Cores is the number of CPUs the scheduler may run threads on.
	Cores() int
	// LoadAverage is the one-minute run-queue average.
	LoadAverage() (float64, error)
	// FreeBytes is the space available to this process on path's filesystem.
	FreeBytes(path string) (uint64, error)
	// DirectoryBytes is the size of every regular file under directory.
	DirectoryBytes(directory string) (uint64, error)
}

// IClock is the time source the service measures with: the system clock in
// production, a controllable one in specs.
type IClock interface {
	Now() time.Time
	// After delivers once after d, unless the returned stop ran first.
	After(d time.Duration) (<-chan time.Time, func() bool)
	// AfterFunc runs f after d, unless the returned stop ran first.
	AfterFunc(d time.Duration, f func()) (stop func() bool)
}

// HostMeasures reads this host's kernel.
type HostMeasures struct{}

const loadAverageFile = "/proc/loadavg"

// Cores is the Go runtime's CPU count.
func (HostMeasures) Cores() int { return runtime.NumCPU() }

// LoadAverage reads the first field of /proc/loadavg.
func (HostMeasures) LoadAverage() (float64, error) {
	file, err := os.Open(loadAverageFile)
	if err != nil {
		return 0, fmt.Errorf("harness check: %w", err)
	}
	defer func() { _ = file.Close() }()
	line, err := bufio.NewReader(file).ReadString('\n')
	if err != nil && line == "" {
		return 0, fmt.Errorf("harness check: read %s: %w", loadAverageFile, err)
	}
	fields := strings.Fields(line)
	if len(fields) == 0 {
		return 0, fmt.Errorf("harness check: %s is empty", loadAverageFile)
	}
	load, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, fmt.Errorf("harness check: %s: %w", loadAverageFile, err)
	}
	return load, nil
}

// FreeBytes asks the filesystem holding path.
func (HostMeasures) FreeBytes(path string) (uint64, error) {
	var stat syscall.Statfs_t
	if err := syscall.Statfs(path, &stat); err != nil {
		return 0, fmt.Errorf("harness check: statfs %s: %w", path, err)
	}
	return stat.Bavail * uint64(stat.Bsize), nil
}

// DirectoryBytes sums the regular files under directory; a directory that
// does not exist is empty.
func (HostMeasures) DirectoryBytes(directory string) (uint64, error) {
	var total uint64
	err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path == directory && os.IsNotExist(err) {
				return filepath.SkipAll
			}
			return err
		}
		if !entry.Type().IsRegular() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += uint64(info.Size())
		return nil
	})
	if err != nil {
		return 0, fmt.Errorf("harness check: measure %s: %w", directory, err)
	}
	return total, nil
}

// SystemClock is the wall clock.
type SystemClock struct{}

func (SystemClock) Now() time.Time { return time.Now() }

func (SystemClock) After(d time.Duration) (<-chan time.Time, func() bool) {
	timer := time.NewTimer(d)
	return timer.C, timer.Stop
}

func (SystemClock) AfterFunc(d time.Duration, f func()) func() bool {
	return time.AfterFunc(d, f).Stop
}

// launchCheck derives the two launch bounds from what the host measures right
// now and reports whether a new session would be admitted.
//
// Worker cap: a session's turn executor is at least one runnable thread while
// it works, so the host admits as many more sessions as it has idle cores:
// cores minus the one-minute load, floored at one so the first session on a
// saturated host is still reported rather than refused outright. Disk floor:
// a session's run directory is the space it needs; the floor is twice the
// largest run directory measured so far, so free space must cover a new run
// the size of the biggest one seen plus as much again for its Bazel output
// base. Both are candidate bounds: until sessions have been measured under
// this check, it is report-only and every session is admitted.
func launchCheck(measures IHostMeasures, stateDirectory string, running uint32, largestRun uint64) *harnessv1.LaunchCheck {
	check := &harnessv1.LaunchCheck{
		Cores:           uint32(measures.Cores()),
		RunningSessions: running,
		ReportOnly:      true,
	}
	load, err := measures.LoadAverage()
	if err != nil {
		check.Findings = append(check.Findings, err.Error())
	}
	check.LoadOneMinute = load
	free, err := measures.FreeBytes(stateDirectory)
	if err != nil {
		check.Findings = append(check.Findings, err.Error())
	}
	check.FreeBytes = free
	check.WorkerCap = WorkerCap(check.Cores, load)
	check.DiskFloorBytes = 2 * largestRun
	if running+1 > check.WorkerCap {
		check.Findings = append(check.Findings, fmt.Sprintf("load %.2f on %d cores leaves room for %d sessions; %d running", load, check.Cores, check.WorkerCap, running))
	}
	switch {
	case largestRun == 0:
		check.Findings = append(check.Findings, "no run directory measured yet, so the disk floor is unknown")
	case free < check.DiskFloorBytes:
		check.Findings = append(check.Findings, fmt.Sprintf("%d free bytes are below the floor of %d (twice the largest run directory, %d)", free, check.DiskFloorBytes, largestRun))
	}
	check.Admitted = len(check.Findings) == 0
	return check
}

// WorkerCap is how many sessions cores under a one-minute load admit: the
// idle cores, floored at one. It is the concurrency the launch check reports
// and the cap the dispatch service schedules within.
func WorkerCap(cores uint32, load float64) uint32 {
	return uint32(math.Max(1, math.Floor(float64(cores)-load)))
}

// measureRuns is the size of the largest run directory under stateDirectory:
// the data the disk floor is derived from.
func measureRuns(ctx context.Context, measures IHostMeasures, stateDirectory string) (uint64, error) {
	entries, err := os.ReadDir(stateDirectory)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("harness check: list runs: %w", err)
	}
	var largest uint64
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return largest, err
		}
		if !entry.IsDir() {
			continue
		}
		size, err := measures.DirectoryBytes(filepath.Join(stateDirectory, entry.Name()))
		if err != nil {
			return largest, err
		}
		largest = max(largest, size)
	}
	return largest, nil
}
