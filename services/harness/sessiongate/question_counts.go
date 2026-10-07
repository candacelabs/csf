// Copyright 2026 Candace Labs

package sessiongate

import (
	"fmt"
	"path/filepath"

	"github.com/candacelabs/csf/services/harness/session"
)

// QuestionCounts is the question gate's measurement over every run under a
// state directory.
type QuestionCounts struct {
	Questions map[QuestionKey]int
	Overrides int
}

// CountQuestions reads every run's event log under stateDirectory. A run
// whose log cannot be read contributes nothing.
func CountQuestions(stateDirectory string) (QuestionCounts, error) {
	counts := QuestionCounts{Questions: map[QuestionKey]int{}}
	paths, err := filepath.Glob(filepath.Join(stateDirectory, "*", session.EventsFile))
	if err != nil {
		return counts, fmt.Errorf("session gate: list event logs: %w", err)
	}
	for _, path := range paths {
		records, err := session.ReadRecords(filepath.Dir(path), func(record *session.Record) bool {
			return len(record.Questions) > 0 || record.QuestionWanted
		})
		if err != nil {
			continue
		}
		for _, record := range records {
			if record.QuestionWanted && record.EventType == session.EventTypeControlAction {
				counts.Overrides++
			}
			if record.EventType != session.EventTypeGateDecision {
				continue
			}
			for _, verdict := range record.Questions {
				counts.Questions[QuestionKey{Gate: record.Gate, Class: verdict.Class, Verdict: verdictOf(record.Decision, verdict)}]++
			}
		}
	}
	return counts, nil
}

func verdictOf(decision string, verdict session.QuestionVerdict) string {
	switch {
	case !verdict.Blocked:
		return VerdictPassed
	case decision == DecisionLimit:
		return VerdictLimit
	}
	return VerdictBlocked
}
