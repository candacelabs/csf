// Copyright 2026 Candace Labs

package githubtools

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// The GitHub series: every call by operation, actor and outcome, and the
// core rate limit and what remained of it on the latest call that reported
// one, which together are the headroom.
const (
	callsMetric         = "csf_github_calls_total"
	rateLimitMetric     = "csf_github_rate_limit"
	rateRemainingMetric = "csf_github_rate_remaining"

	labelOperation = "operation"
	labelActor     = "actor"
	labelOutcome   = "outcome"

	// maxRecordBytes bounds one line of the GitHub log.
	maxRecordBytes = 1 << 20
)

// CallCounts is the measurement over the host's GitHub log.
type CallCounts struct {
	Calls map[CallKey]int
	// RateLimit and RateRemaining are the latest record's that carried a
	// rate limit; zero when none did.
	RateLimit     int
	RateRemaining int
}

// CountCalls reads the GitHub log under stateDirectory. A missing log is no
// calls; a line that is not a record is skipped.
func CountCalls(stateDirectory string) (CallCounts, error) {
	counts := CallCounts{Calls: map[CallKey]int{}}
	file, err := os.Open(filepath.Join(stateDirectory, LogFile))
	if errors.Is(err, os.ErrNotExist) {
		return counts, nil
	}
	if err != nil {
		return counts, fmt.Errorf("github tools: open the GitHub log: %w", err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxRecordBytes)
	for scanner.Scan() {
		var record CallRecord
		if json.Unmarshal(scanner.Bytes(), &record) != nil || record.Operation == "" {
			continue
		}
		counts.Calls[CallKey{Operation: record.Operation, Actor: record.Actor, Outcome: record.Outcome}]++
		if record.RateLimit > 0 {
			counts.RateLimit, counts.RateRemaining = record.RateLimit, record.RateRemaining
		}
	}
	if err := scanner.Err(); err != nil {
		return counts, fmt.Errorf("github tools: read the GitHub log: %w", err)
	}
	return counts, nil
}
