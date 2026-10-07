// Copyright 2026 Candace Labs

package sandbox

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// The launcher and proxy command-line flags this package builds (CS-13).
const (
	flagPolicy      = "--policy"
	argSeparator    = "--"
	flagSocket      = "--socket"
	flagUpstream    = "--upstream"
	flagSession     = "--session"
	flagAllowImage  = "--allow-image"
	flagSessionPath = "--session-path"
)

// The cgroup v2 limit files and the controller set a session cgroup needs. They
// are values, named here once (CS-13), and must read the same way the launcher
// writes them.
const (
	cpuPeriodMicroseconds = 100_000
	defaultCPUCoreCap     = 8
	defaultMemoryCapBytes = 16 << 30
	defaultPidsMax        = 2048
	cpuStatUsageKey       = "usage_usec"
	gibibyte              = 1 << 30
)

// Capacity is the host capacity a session's bounds are derived from.
type Capacity struct {
	// Cores is the number of logical CPUs.
	Cores int
	// MemoryBytes is the host's total memory.
	MemoryBytes uint64
}

// Limits are the cgroup v2 bounds written into a session's cgroup. The JSON
// shape matches the launcher's policy exactly, so the Rust launcher reads what
// this writes.
type Limits struct {
	CPUMax             string `json:"cpu_max"`
	MemoryMaxBytes     uint64 `json:"memory_max_bytes"`
	MemorySwapMaxBytes uint64 `json:"memory_swap_max_bytes"`
	PidsMax            uint64 `json:"pids_max"`
	Derivation         string `json:"derivation"`
}

// DeriveLimits computes a session's bounds from host capacity and records, in
// Derivation, exactly how each was chosen, so a receipt carries the basis for
// every limit. There is no per-session cgroup peak history before this slice,
// so the bounds are a conservative share of host capacity; the cpu and memory
// receipts this slice records are what later tightens them.
func DeriveLimits(capacity Capacity) Limits {
	cores := capacity.Cores
	if cores < 1 {
		cores = 1
	}
	cpuCores := cores
	if cpuCores > defaultCPUCoreCap {
		cpuCores = defaultCPUCoreCap
	}
	memoryMax := uint64(defaultMemoryCapBytes)
	if half := capacity.MemoryBytes / 2; half > 0 && half < memoryMax {
		memoryMax = half
	}
	return Limits{
		CPUMax:             fmt.Sprintf("%d %d", cpuCores*cpuPeriodMicroseconds, cpuPeriodMicroseconds),
		MemoryMaxBytes:     memoryMax,
		MemorySwapMaxBytes: 0,
		PidsMax:            defaultPidsMax,
		Derivation: fmt.Sprintf(
			"host %d cores, %d GiB: cpu.max %d of %d cores, memory.max %d GiB (min of 16 GiB and half of host), swap off, pids.max %d bounds a fork bomb above a build's few-hundred processes; tighten from memory.peak and cpu usage_usec receipts",
			cores, capacity.MemoryBytes/gibibyte, cpuCores, cores, memoryMax/gibibyte, defaultPidsMax),
	}
}

// SessionPolicy is the launcher's policy for one session. Its JSON field names
// match the Rust launcher's SessionPolicy exactly.
type SessionPolicy struct {
	SessionID         string   `json:"session_id"`
	Cgroup            string   `json:"cgroup"`
	Limits            Limits   `json:"limits"`
	ReadWritePaths    []string `json:"read_write_paths"`
	ReadOnlyPaths     []string `json:"read_only_paths"`
	DockerProxySocket string   `json:"docker_proxy_socket,omitempty"`
}

// marshalPolicy renders a policy as indented JSON the launcher reads.
func marshalPolicy(policy SessionPolicy) ([]byte, error) {
	content, err := json.MarshalIndent(policy, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("sandbox: encode policy: %w", err)
	}
	return content, nil
}

// Usage is what a session's cgroup spent, read for its receipt before the
// cgroup is removed.
type Usage struct {
	// CPU is the total on-CPU time, from cpu.stat's usage_usec.
	CPU time.Duration
	// MemoryPeakBytes is the high-water mark, from memory.peak.
	MemoryPeakBytes uint64
}

// ParseUsage reads a receipt from the contents of cpu.stat and memory.peak.
func ParseUsage(cpuStat []byte, memoryPeak []byte) (Usage, error) {
	usage := Usage{}
	scanner := bufio.NewScanner(bytes.NewReader(cpuStat))
	found := false
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[0] == cpuStatUsageKey {
			microseconds, err := strconv.ParseInt(fields[1], 10, 64)
			if err != nil {
				return Usage{}, fmt.Errorf("sandbox: parse cpu.stat %s: %w", cpuStatUsageKey, err)
			}
			usage.CPU = time.Duration(microseconds) * time.Microsecond
			found = true
		}
	}
	if !found {
		return Usage{}, fmt.Errorf("sandbox: cpu.stat has no %s", cpuStatUsageKey)
	}
	peak, err := strconv.ParseUint(strings.TrimSpace(string(memoryPeak)), 10, 64)
	if err != nil {
		return Usage{}, fmt.Errorf("sandbox: parse memory.peak: %w", err)
	}
	usage.MemoryPeakBytes = peak
	return usage, nil
}
