// Copyright 2026 Candace Labs

// Package progress projects each harness session into one session_progress
// row — the assignment, agent, phase, turn, queue depth, tool calls, commits
// ahead, changed files, model, last activity and last note — and folds the
// session's event log into the readable lines a tail follows. Both csf status
// and csf tail read this one projection: status renders the row as a table,
// tail renders the lines, and neither reads the log a second way.
//
// A session here is a virtual session: one assignment the harness runs. It
// resolves to the real session — the executor conversation that carries the
// server-side prompt cache — through the router's table under
// <state>/routing, so two virtual sessions shown on one conversation are
// sharing that cache.
package progress

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/candacelabs/csf/pkg/textbound"
	"github.com/candacelabs/csf/services/harness/session"
)

// Kind is the kind of one tail line.
type Kind string

// The kinds a line carries, one per kind of thing a session does.
const (
	// KindTurn is a turn starting, ending or a message sent to a session.
	KindTurn Kind = "turn"
	// KindTool is one tool call the session made.
	KindTool Kind = "tool"
	// KindGate is a session gate's decision.
	KindGate Kind = "gate"
	// KindMerge is a pull request moving: marked ready, a merge starting or a
	// merge landing.
	KindMerge Kind = "merge"
	// KindError is a failure a record reported.
	KindError Kind = "error"
)

// Kinds is every line kind, in the order csf tail's -kinds accepts them.
func Kinds() []Kind { return []Kind{KindTurn, KindTool, KindGate, KindMerge, KindError} }

// ParseKind reads one kind by its spelling.
func ParseKind(name string) (Kind, error) {
	for _, kind := range Kinds() {
		if string(kind) == name {
			return kind, nil
		}
	}
	return "", fmt.Errorf("progress: unknown kind %q; want one of %s", name, strings.Join(kindNames(), ", "))
}

// ParseKinds reads a comma-separated kind list into the set of kinds to show.
// An empty list means every kind.
func ParseKinds(names string) (map[Kind]bool, error) {
	keep := map[Kind]bool{}
	for _, name := range strings.Split(names, ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		kind, err := ParseKind(name)
		if err != nil {
			return nil, err
		}
		keep[kind] = true
	}
	if len(keep) == 0 {
		for _, kind := range Kinds() {
			keep[kind] = true
		}
	}
	return keep, nil
}

func kindNames() []string {
	names := make([]string, 0, len(Kinds()))
	for _, kind := range Kinds() {
		names = append(names, string(kind))
	}
	return names
}

// Phase is a session's phase as this projection reads it from the state
// directory. The state directory records a run's whole event log and the end
// its runner wrote, so this reads a run between turns as open and a turn in
// flight as running; the phases only the running host holds — starting and
// canceling — are the host's to report, through csf list.
type Phase string

const (
	// PhaseRunning is a run between a turn's request and its finish.
	PhaseRunning Phase = "running"
	// PhaseOpen is a run with no turn in flight.
	PhaseOpen Phase = "open"
	// PhaseClosed is a run whose turn executor closed.
	PhaseClosed Phase = "closed"
	// PhaseCanceled is a run that was canceled.
	PhaseCanceled Phase = "canceled"
	// PhaseFailed is a run that failed.
	PhaseFailed Phase = "failed"
)

// lineTimeFormat is how a line's time and a row's last activity render: the
// clock time, which is what a tail reads.
const lineTimeFormat = "15:04:05"

// lineLimit bounds one line's and one note's text in bytes.
const (
	lineLimit = 200
	noteLimit = 100
)

// Line is one line of the tail: when it happened, the agent that wrote it,
// which kind it is and what it says. It renders as the brief's
// render line "{time} {agent} {kind} {text}".
type Line struct {
	Time  time.Time
	Agent string
	Kind  Kind
	Text  string
}

// String renders the line for the terminal: readable, labelled by agent, and
// never JSON.
func (line Line) String() string {
	return fmt.Sprintf("%s %s %s %s", line.Time.Format(lineTimeFormat), line.Agent, line.Kind, line.Text)
}

// Progress is one row of the session_progress relation: what one session is
// doing, read from its run directory. It is the one projection both csf status
// and csf tail read.
type Progress struct {
	Assignment   string
	Agent        string
	Phase        Phase
	Turn         int
	Queued       int
	ToolCalls    int
	CommitsAhead int
	ChangedFiles int
	Model        string
	LastActivity time.Time
	LastNote     string
	// worktree and baseBranch are the git inputs [Work] reads the counts from;
	// they are not part of the relation, only the way to its two git fields.
	worktree   string
	baseBranch string
}

// builder collects the lines of one record, stamping each with the record's
// time and the session's agent.
type builder struct {
	agent string
	at    time.Time
	lines []Line
}

func (out *builder) add(kind Kind, text string) {
	text = oneLine(text)
	if text == "" {
		return
	}
	out.lines = append(out.lines, Line{Time: out.at, Agent: out.agent, Kind: kind, Text: text})
}

// Fold applies one event log record to a row and returns the row with the
// lines the record adds. It is pure: a record folds to the same row and the
// same lines every time. The row's Agent names every line it yields, so a
// caller sets it from the run record before folding.
func Fold(row Progress, record *session.Record) (Progress, []Line) {
	if record.Time.After(row.LastActivity) {
		row.LastActivity = record.Time
	}
	if record.Turn > row.Turn {
		row.Turn = record.Turn
	}
	out := &builder{agent: row.Agent, at: record.Time}
	switch record.EventType {
	case session.EventTypeRunStarted:
		row.Phase = PhaseOpen
		out.add(KindTurn, "session started")
	case session.EventTypeRunResumed:
		row.Phase = PhaseOpen
		out.add(KindTurn, "run resumed")
	case session.EventTypeTurnRequested:
		row.Phase = PhaseRunning
		out.add(KindTurn, "turn "+strconv.Itoa(record.Turn))
	case session.EventTypeBackgroundResult, session.EventTypeTurnResumed:
		row.Phase = PhaseRunning
		out.add(KindTurn, record.Message)
	case session.EventTypeRunFinished:
		row.Phase = PhaseOpen
		if record.Error == "" {
			out.add(KindTurn, "turn "+strconv.Itoa(record.Turn)+" done")
		}
	case session.EventTypeSessionClosed:
		row.Phase = PhaseClosed
		out.add(KindTurn, "session closed")
	case session.EventTypeSessionSuspended:
		row.Phase = PhaseOpen
		out.add(KindTurn, "suspended: idle")
	case session.EventTypeSessionResumed:
		row.Phase = PhaseOpen
		out.add(KindTurn, "resumed")
	case session.EventTypeGateDecision:
		out.add(KindGate, gateText(record))
	case session.EventTypeControlAction:
		kind, text := actionLine(record.Action)
		out.add(kind, text)
	case session.EventTypeAssistant:
		row = foldAssistant(row, record, out)
	case session.EventTypeUser:
		if record.Direction == session.DirectionIn {
			out.add(KindTurn, "message: "+firstLine(firstText(record)))
		}
	}
	if record.Error != "" {
		out.add(KindError, record.Error)
	}
	return row, out.lines
}

// foldAssistant counts one assistant message's tool calls, takes the model
// from it, keeps the last thing the session said as the row's note, and — on
// the message that ends a turn — adds the turn's line.
func foldAssistant(row Progress, record *session.Record, out *builder) Progress {
	text, tools := record.Assistant()
	if message, ok := record.Stream(); ok && row.Model == "" {
		row.Model = message.Message.Model
	}
	for _, tool := range tools {
		row.ToolCalls++
		out.add(KindTool, tool.Name+" "+toolGist(tool.Input))
	}
	if len(text) == 0 {
		return row
	}
	said := firstLine(text[len(text)-1])
	if said == "" {
		return row
	}
	row.LastNote = textbound.Prefix(said, noteLimit)
	if message, ok := record.Stream(); ok && message.Message.StopReason == session.StopReasonEndTurn {
		out.add(KindTurn, said)
	}
	return row
}

// firstText is the text blocks of a record's event, joined by a space.
func firstText(record *session.Record) string { return strings.Join(record.Text(), " ") }

// gateText names a gate decision: the gate, its decision and why.
func gateText(record *session.Record) string {
	text := record.Gate + " " + record.Decision
	if record.Reason != "" {
		text += ": " + record.Reason
	}
	return text
}

// actionLine is the kind and text of one control action on a session.
func actionLine(action string) (Kind, string) {
	switch action {
	case session.ActionSubmit:
		return KindTurn, "launched"
	case session.ActionSend:
		return KindTurn, "message sent"
	case session.ActionCancel:
		return KindTurn, "cancel requested"
	case session.ActionReady:
		return KindMerge, "pull request marked ready"
	case session.ActionMergeStarted:
		return KindMerge, "merge started"
	case session.ActionMerge:
		return KindMerge, "pull request merged"
	}
	return KindTurn, "control action: " + action
}

// toolInput is the tool-input fields a tool line names, in the order one is
// preferred: what the call is for, or the command, path or pattern it acts on.
type toolInput struct {
	Description string `json:"description"`
	Command     string `json:"command"`
	FilePath    string `json:"file_path"`
	Pattern     string `json:"pattern"`
	Prompt      string `json:"prompt"`
}

// toolGist is the first line of the tool-input field that best stands for a
// call, and empty when the input names none of them.
func toolGist(input json.RawMessage) string {
	var parsed toolInput
	if len(input) == 0 || json.Unmarshal(input, &parsed) != nil {
		return ""
	}
	for _, field := range []string{parsed.Description, parsed.Command, parsed.FilePath, parsed.Pattern, parsed.Prompt} {
		if gist := firstLine(field); gist != "" {
			return gist
		}
	}
	return ""
}

// oneLine collapses text to one bounded line: newlines and runs of spaces
// become single spaces, and the result is cut on a character boundary.
func oneLine(text string) string {
	return textbound.Prefix(strings.Join(strings.Fields(text), " "), lineLimit)
}

// firstLine is the first non-empty line of text, trimmed.
func firstLine(text string) string {
	for _, line := range strings.Split(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
