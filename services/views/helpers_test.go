// Copyright 2026 Candace Labs

package views_test

import (
	"strings"
	"testing/fstest"
	"time"
)

// day is the fixture's date; every record falls on it.
var day = time.Date(2026, 10, 4, 0, 0, 0, 0, time.UTC)

// at is the fixture's instant hour:minute on day.
func at(hour int, minute int) time.Time {
	return day.Add(time.Duration(hour)*time.Hour + time.Duration(minute)*time.Minute)
}

// stamp is at as the event log writes it.
func stamp(hour int, minute int) string { return at(hour, minute).Format(time.RFC3339Nano) }

// eventLog is one run's event log: three results (the third after the
// executor restarted), a reply refusal, a commit allowance, a failed merge,
// a ready and an operator message.
var eventLog = strings.Join([]string{
	`{"time":"` + stamp(10, 5) + `","event_type":"result","provider":"claudecode","event":{"total_cost_usd":1.0,"usage":{"input_tokens":1,"cache_creation_input_tokens":2,"cache_read_input_tokens":3,"output_tokens":4}}}`,
	`{"time":"` + stamp(10, 30) + `","event_type":"result","provider":"claudecode","event":{"total_cost_usd":1.5,"usage":{"input_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":0,"output_tokens":1}}}`,
	`{"time":"` + stamp(11, 10) + `","event_type":"result","provider":"claudecode","event":{"total_cost_usd":0.25,"usage":{"input_tokens":1,"cache_creation_input_tokens":0,"cache_read_input_tokens":10,"output_tokens":0}}}`,
	`{"time":"` + stamp(11, 15) + `","event_type":"session_gate_decision","gate":"reply","decision":"deny","rules":["commitment"]}`,
	`{"time":"` + stamp(11, 20) + `","event_type":"session_gate_decision","gate":"commit","decision":"allow"}`,
	`{"time":"` + stamp(11, 30) + `","level":"ERROR","event_type":"harness_control_action","action":"merge"}`,
	`{"time":"` + stamp(11, 31) + `","level":"INFO","event_type":"harness_control_action","action":"ready"}`,
	`{"time":"` + stamp(11, 40) + `","level":"INFO","event_type":"harness_control_action","action":"send","operator_authored":true}`,
	`not a record`,
}, "\n")

const runState = `{"assignment_id":"run-1","agent_id":"csf-metrics","model":"claude-opus-5-5"}`

// stateDirectory is a corpus of one run.
func stateDirectory() fstest.MapFS {
	return fstest.MapFS{
		"run-1/events.jsonl": {Data: []byte(eventLog)},
		"run-1/run.json":     {Data: []byte(runState)},
		"harness.json":       {Data: []byte(`{}`)},
		"empty/notes.txt":    {Data: []byte(`no event log`)},
	}
}
