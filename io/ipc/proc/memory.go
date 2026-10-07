// Copyright 2026 Candace Labs

package proc

import (
	"fmt"
	"strconv"
	"strings"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
)

// The kernel's memory accounting in the process table, one "Key: N kB" line
// per measure.
const (
	MemoryInfoFile  = "meminfo"
	MemoryTotal     = "MemTotal:"
	MemoryAvailable = "MemAvailable:"
	kibibyte        = 1024
)

// MemoryField is one "Key: N kB" line of meminfo content, in bytes, or zero
// when the line is absent.
func MemoryField(meminfo string, key string) uint64 {
	for _, line := range strings.Split(meminfo, "\n") {
		rest, found := strings.CutPrefix(line, key)
		if !found {
			continue
		}
		if fields := strings.Fields(rest); len(fields) > 0 {
			if kib, err := strconv.ParseUint(fields[0], 10, 64); err == nil {
				return kib * kibibyte
			}
		}
	}
	return 0
}

// ReadMemoryAvailable is the memory the kernel estimates is available for a
// new workload without swapping, read from the granted process table.
func ReadMemoryAvailable(processes iofs.IFiles) (uint64, error) {
	content, err := processes.ReadFile(MemoryInfoFile)
	if err != nil {
		return 0, err
	}
	available := MemoryField(string(content), MemoryAvailable)
	if available == 0 {
		return 0, fmt.Errorf("ipc/proc: %s reports no %s", MemoryInfoFile, MemoryAvailable)
	}
	return available, nil
}
