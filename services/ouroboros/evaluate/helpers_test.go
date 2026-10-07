// Copyright 2026 Candace Labs

package evaluate_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/candacelabs/csf/services/ouroboros/evaluate"
)

// specStart is the instant every fixture is dated from.
var specStart = time.Date(2026, 10, 5, 7, 0, 0, 0, time.UTC)

// eventLog is a harness event log of calls distinct tool calls, the ones in
// failing answered with an error, one line per call and one per result.
func eventLog(calls int, failing ...int) []byte {
	var lines []string
	for index := range calls {
		at := specStart.Add(time.Duration(index) * time.Second).Format(time.RFC3339Nano)
		use, _ := json.Marshal(map[string]any{
			"time": at, "event_type": "assistant",
			"event": map[string]any{"message": map[string]any{"content": []map[string]any{
				{"type": "tool_use", "name": "Bash", "input": map[string]any{"command": fmt.Sprintf("echo %d", index)}},
			}}},
		})
		isError := false
		for _, failed := range failing {
			isError = isError || failed == index
		}
		result, _ := json.Marshal(map[string]any{
			"time": at, "event_type": "user",
			"event": map[string]any{"message": map[string]any{"content": []map[string]any{
				{"type": "tool_result", "is_error": isError},
			}}},
		})
		lines = append(lines, string(use), string(result))
	}
	return []byte(strings.Join(lines, "\n") + "\n")
}

// recipe is a run's recipe.json naming ticket, with an inline task.
func recipe(ticket int64) []byte {
	content, _ := json.Marshal(map[string]any{
		"assignmentId": "11111111-2222-4333-8444-555555555555",
		"agent":        map[string]any{"id": "spec", "revision": 1},
		"ticketUrl":    fmt.Sprintf("https://github.com/example/repo/issues/%d", ticket),
		"task":         "the brief",
		"model":        "claude-opus-5-5",
		"workspace": map[string]any{
			"repositoryPath": "/original", "baseBranch": "origin/main", "branch": "dev/spec",
			"briefPath": "brief.md", "allowedTools": []string{"Bash"},
		},
	})
	return content
}

// ticket is a pool ticket whose original run read calls with episodes.
func ticket(number int64, calls int64, episodes int64) evaluate.Ticket {
	return evaluate.Ticket{
		Number: number, PullRequest: number + 1000, BaseCommit: fmt.Sprintf("%040d", number), MergeCommit: fmt.Sprintf("%040d", number+1),
		Files: []string{"a.go", "b.go"}, Assignment: fmt.Sprintf("run-%d", number), ToolCalls: calls, Episodes: episodes,
	}
}

// replay is build's replay of ticket on version, read at calls and episodes.
func replay(build string, version int, number int64, calls int64, episodes int64) evaluate.Replay {
	return evaluate.Replay{
		Build: build, SuiteVersion: version, Ticket: number, Node: evaluate.NodeHost, Assignment: fmt.Sprintf("replay-%d", number),
		ToolCalls: calls, Episodes: episodes, Recall: 0.5, CostUSDMicros: 2_000_000, Seconds: 60, RecordedAt: specStart,
	}
}
