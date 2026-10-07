// Copyright 2026 Candace Labs

package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	stdfs "io/fs"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/pkg/atomicfile"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
)

// The resident series: what the harness and its executors hold in memory,
// sampled from the process table it is granted as /proc and written under the
// state directory for the ops view, which follows the file like the mutation
// series. The file is rewritten whole with the last ResidentWindow samples, so
// it stays small enough to read whole.
const (
	// ResidentFile is the series file at the root of the state directory.
	ResidentFile = "resident.jsonl"
	// ResidentWindow is how many samples the file keeps: a day at the default
	// interval.
	ResidentWindow = 24 * 60
	// DefaultResidentSampleInterval is how often the series is sampled. It is
	// a cadence, not a bound: nothing is decided on it.
	DefaultResidentSampleInterval = time.Minute

	residentOwner      = "resident"
	processStat        = "stat"
	processWorkingDir  = "cwd"
	statFieldPPID      = 1
	statFieldGroup     = 2
	statFieldRSSPages  = 21
	statMinimumFields  = statFieldRSSPages + 1
	residentScanBuffer = 4 << 10
)

// ResidentSample is one sample of the series.
type ResidentSample struct {
	At time.Time `json:"at"`
	// HarnessRSSBytes is the harness process's own resident set.
	HarnessRSSBytes uint64 `json:"harness_rss_bytes"`
	// ExecutorsRSSBytes is the resident set of every process in the groups the
	// harness's children lead: the executors and whatever they started.
	ExecutorsRSSBytes uint64 `json:"executors_rss_bytes"`
	// OpenSessions counts the sessions that have not ended.
	OpenSessions int `json:"open_sessions"`
	// ExecutorsAlive counts the open sessions whose executor process is open.
	ExecutorsAlive int `json:"executors_alive"`
	// Resumes counts the sessions resumed after a suspend since the
	// harness started.
	Resumes int `json:"resumes"`
	// ResumeTimeToFirstTokenMs is the latest resumed turn's time to its first
	// token; zero until one has run.
	ResumeTimeToFirstTokenMs float64 `json:"resume_ttft_ms"`
	// IdleBoundSeconds and IdleBoundQuantile are the derived bound and where
	// it sits in the Gaps measured, so the derivation travels with the series.
	IdleBoundSeconds  float64 `json:"idle_bound_s"`
	IdleBoundQuantile float64 `json:"idle_bound_quantile"`
	Gaps              int     `json:"gaps"`
}

// ReadResidentSeries decodes the series file's content, skipping a line that
// is not a sample. It is pure.
func ReadResidentSeries(content []byte) []ResidentSample {
	samples := []ResidentSample{}
	scanner := bufio.NewScanner(bytes.NewReader(content))
	scanner.Buffer(make([]byte, 0, residentScanBuffer), residentScanBuffer)
	for scanner.Scan() {
		var sample ResidentSample
		if json.Unmarshal(scanner.Bytes(), &sample) != nil || sample.At.IsZero() {
			continue
		}
		samples = append(samples, sample)
	}
	return samples
}

// processEntry is one process as the table reads it.
type processEntry struct {
	pid       int
	parent    int
	group     int
	rssBytes  uint64
	directory string
}

// processTable is the host's processes as one read of /proc saw them.
type processTable struct {
	entries []processEntry
}

// readProcessTable reads every process it may from the granted /proc; one
// that exits while being read is left out. The working directory is read
// only for the host's direct children, which is where executors are found.
func readProcessTable(processes iofs.IFiles, hostPID int, pageSize uint64) processTable {
	table := processTable{}
	entries, err := processes.ReadDir(".")
	if err != nil {
		return table
	}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil {
			continue
		}
		stat, err := processes.ReadFile(path.Join(entry.Name(), processStat))
		if err != nil {
			continue
		}
		parsed, ok := parseStat(pid, string(stat), pageSize)
		if !ok {
			continue
		}
		if parsed.parent == hostPID {
			if links, ok := processes.(stdfs.ReadLinkFS); ok {
				parsed.directory, _ = links.ReadLink(path.Join(entry.Name(), processWorkingDir))
			}
		}
		table.entries = append(table.entries, parsed)
	}
	return table
}

// parseStat reads the parent, the process group and the resident pages from
// one /proc/<pid>/stat line: the fields after the parenthesized command name,
// which may itself hold spaces and parentheses.
func parseStat(pid int, stat string, pageSize uint64) (processEntry, bool) {
	closing := strings.LastIndexByte(stat, ')')
	if closing < 0 {
		return processEntry{}, false
	}
	fields := strings.Fields(stat[closing+1:])
	if len(fields) < statMinimumFields {
		return processEntry{}, false
	}
	parent, parentErr := strconv.Atoi(fields[statFieldPPID])
	group, groupErr := strconv.Atoi(fields[statFieldGroup])
	pages, pagesErr := strconv.ParseUint(fields[statFieldRSSPages], 10, 64)
	if parentErr != nil || groupErr != nil || pagesErr != nil {
		return processEntry{}, false
	}
	return processEntry{pid: pid, parent: parent, group: group, rssBytes: pages * pageSize}, true
}

// rss is the resident set of pid, or zero when it is not in the table.
func (table processTable) rss(pid int) uint64 {
	for _, entry := range table.entries {
		if entry.pid == pid {
			return entry.rssBytes
		}
	}
	return 0
}

// childGroups is the resident set of every process in a group one of host's
// children leads: the executors, their proxies and everything they started.
func (table processTable) childGroups(host int) uint64 {
	leaders := map[int]bool{}
	for _, entry := range table.entries {
		if entry.parent == host {
			leaders[entry.pid] = true
		}
	}
	var total uint64
	for _, entry := range table.entries {
		if leaders[entry.group] {
			total += entry.rssBytes
		}
	}
	return total
}

// executorOf is the host's direct child working in worktree: the session's
// turn executor. The sandbox launcher execs the executor, so it is the same
// process either way.
func (table processTable) executorOf(host int, worktree string) (processEntry, bool) {
	for _, entry := range table.entries {
		if entry.parent == host && entry.directory == worktree {
			return entry, true
		}
	}
	return processEntry{}, false
}

// liveChildren counts the processes in the executor's group besides itself:
// what the agent started and left running, a background build or test.
func (table processTable) liveChildren(executor processEntry) int {
	count := 0
	for _, entry := range table.entries {
		if entry.group == executor.pid && entry.pid != executor.pid {
			count++
		}
	}
	return count
}

// liveChildren reports how many processes the session's executor has left
// running in its group, and whether the executor was found in the table.
func (service *AgentSessionService) liveChildren(worktree string) (int, bool) {
	table := readProcessTable(service.processes, service.hostPID, uint64(os.Getpagesize()))
	executor, found := table.executorOf(service.hostPID, worktree)
	if !found {
		return 0, false
	}
	return table.liveChildren(executor), true
}

// sampleResident runs on the service scope: one sample per interval, the
// window rewritten each time, until the scope ends.
func (service *AgentSessionService) sampleResident(ctx context.Context) error {
	seriesPath := filepath.Join(service.runner.StateDirectory(), ResidentFile)
	content, err := os.ReadFile(seriesPath)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		service.logger.Warn("harness: resident series not read", "error", err)
	}
	window := ReadResidentSeries(content)
	for {
		sample, ok := service.residentSample(ctx)
		if !ok {
			return nil
		}
		window = append(window, sample)
		if len(window) > ResidentWindow {
			window = window[len(window)-ResidentWindow:]
		}
		if err := writeResidentSeries(seriesPath, window); err != nil {
			service.logger.Warn("harness: resident series not written", "error", err)
		}
		wait, stop := service.clock.After(service.residentInterval)
		select {
		case <-wait:
		case <-ctx.Done():
			stop()
			return nil
		}
	}
}

// residentSample measures the process table and reads the registry's counts.
// It reports false once the registry has retired.
func (service *AgentSessionService) residentSample(ctx context.Context) (ResidentSample, bool) {
	table := readProcessTable(service.processes, service.hostPID, uint64(os.Getpagesize()))
	sample := ResidentSample{
		At:                service.clock.Now().UTC(),
		HarnessRSSBytes:   table.rss(service.hostPID),
		ExecutorsRSSBytes: table.childGroups(service.hostPID),
		IdleBoundSeconds:  service.idleBound.Bound.Seconds(),
		IdleBoundQuantile: service.idleBound.Quantile,
		Gaps:              service.idleBound.Gaps,
	}
	if err := service.command(ctx, func(registry *registry) error {
		for _, record := range registry.sessions {
			phase := record.state.GetPhase()
			if finished(phase) {
				continue
			}
			sample.OpenSessions++
			if phase != harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_STARTING && !record.state.GetSuspended() {
				sample.ExecutorsAlive++
			}
		}
		sample.Resumes = registry.resumes
		sample.ResumeTimeToFirstTokenMs = registry.resumeTimeToFirstTokenMs
		return nil
	}); err != nil {
		return ResidentSample{}, false
	}
	return sample, true
}

// writeResidentSeries replaces the series file whole, so the view never
// reads half of it.
func writeResidentSeries(seriesPath string, window []ResidentSample) error {
	var content bytes.Buffer
	encoder := json.NewEncoder(&content)
	for _, sample := range window {
		if err := encoder.Encode(sample); err != nil {
			return err
		}
	}
	if err := atomicfile.WriteFile(seriesPath, content.Bytes(), stateFileMode); err != nil {
		return fmt.Errorf("harness: write resident series: %w", err)
	}
	return nil
}
