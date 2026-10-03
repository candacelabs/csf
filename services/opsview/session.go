// Copyright 2026 Candace Labs

package opsview

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"path"
	"strings"
	"time"

	ipcfs "github.com/candacelabs/csf/ipc/fs"
	"github.com/candacelabs/csf/services/harness/session"
)

// Status is where a session stands, read from its event log.
type Status string

// The statuses a card shows. A session is running from its first turn
// request until the harness records the turn's end; a finished session keeps
// its turn executor open for the next message until the harness closes it.
const (
	StatusRunning  Status = "running"
	StatusFinished Status = "finished"
	StatusFailed   Status = "failed"
	StatusClosed   Status = "closed"
)

// The kinds of recent event a card lists, which are also its CSS classes.
const (
	KindTool    = "tool"
	KindGate    = "gate"
	KindHarness = "harness"
	KindResult  = "result"
	KindMessage = "message"
)

// RecentEvent is one of the last few typed events a card shows.
type RecentEvent struct {
	Time string `json:"time"`
	Kind string `json:"kind"`
	Text string `json:"text"`
}

// SessionCard is everything the ops view shows about one harness session,
// derived from its run record and its event log and nothing else. It is the
// session widget's state: a pure projection, replaced whole whenever the
// follow effect reads more of the log.
type SessionCard struct {
	Assignment     string    `json:"assignment"`
	Agent          string    `json:"agent"`
	Branch         string    `json:"branch"`
	TicketURL      string    `json:"ticket_url"`
	PullRequestURL string    `json:"pull_request_url"`
	Model          string    `json:"model"`
	Status         Status    `json:"status"`
	Turns          int       `json:"turns"`
	ToolCalls      int       `json:"tool_calls"`
	GateDenials    int       `json:"gate_denials"`
	StartedAt      time.Time `json:"started_at"`
	// LastAt is the follow effect's own bookkeeping for Elapsed and is not on
	// the wire: a line that moves nothing a card shows then moves nothing on
	// the page, and the executor logs such lines several times a second.
	LastAt  time.Time `json:"-"`
	Elapsed string    `json:"elapsed"`
	Error   string    `json:"error"`
	// Recent is the last few typed events, newest last. A card shows the
	// last recentShown of them until the browser asks for the rest.
	Recent []RecentEvent `json:"recent"`
	// Expanded is the one thing a browser decides: whether the card shows
	// every recent event it holds. It survives the card being replaced.
	Expanded bool `json:"expanded"`
}

// The record keys and values this view reads. The harness and its turn
// executor spell them; services/harness/session names the harness's own.
const (
	levelError = "ERROR"

	eventTypeAssistant   = "assistant"
	eventTypeUser        = "user"
	eventTypeResult      = "result"
	eventTypeSystem      = "system"
	directionIn          = "in"
	subtypeDenied        = "permission_denied"
	subtypePublished     = "code_change_published"
	blockToolUse         = "tool_use"
	decisionDeny         = "deny"
	pullRequestPathMark  = "/pull/"
	recentKept           = 20
	recentShown          = 5
	gistLimit            = 96
	elapsedUnderMinute   = "<1m"
	elapsedMinuteFormat  = "%dm"
	elapsedHourFormat    = "%dh %02dm"
	recentTimeFormat     = time.TimeOnly
	resultDurationFormat = "turn %d: %s after %.0fs"
)

// gistKeys are the tool-input fields whose first line stands for the call,
// in the order one is preferred.
var gistKeys = []string{"description", "command", "file_path", "pattern", "prompt"}

// record is the part of one events.jsonl line this view reads.
type record struct {
	Time           time.Time       `json:"time"`
	Level          string          `json:"level"`
	Message        string          `json:"msg"`
	EventType      string          `json:"event_type"`
	Turn           int             `json:"turn"`
	Direction      string          `json:"direction"`
	Event          json.RawMessage `json:"event"`
	Gate           string          `json:"gate"`
	Decision       string          `json:"decision"`
	Reason         string          `json:"reason"`
	Error          string          `json:"error"`
	PullRequestURL string          `json:"pull_request_url"`
}

// executorEvent is the stream-json event a turn executor record carries.
// Message is raw because it is an object on an assistant event and a string
// on a system one.
type executorEvent struct {
	Subtype  string          `json:"subtype"`
	ToolName string          `json:"tool_name"`
	URL      string          `json:"url"`
	Duration float64         `json:"duration_ms"`
	Message  json.RawMessage `json:"message"`
}

// assistantMessage is the message an assistant event carries.
type assistantMessage struct {
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Input json.RawMessage `json:"input"`
}

// Fold applies one event log line to the card and reports whether it read a
// record. It is pure: equal cards and equal lines fold to equal cards.
func Fold(card SessionCard, line []byte) (SessionCard, bool) {
	var parsed record
	if json.Unmarshal(line, &parsed) != nil || parsed.Time.IsZero() {
		return card, false
	}
	if card.StartedAt.IsZero() || parsed.Time.Before(card.StartedAt) {
		card.StartedAt = parsed.Time
	}
	if parsed.Time.After(card.LastAt) {
		card.LastAt = parsed.Time
	}
	card.Elapsed = elapsedLabel(card.LastAt.Sub(card.StartedAt))
	switch parsed.EventType {
	case session.EventTypeRunStarted, session.EventTypeTurnRequested:
		card.Status = StatusRunning
		card = remember(card, parsed, KindHarness, parsed.Message)
	case session.EventTypeRunFinished:
		card.Status = StatusFinished
		if parsed.Level == levelError {
			card.Status = StatusFailed
			card.Error = parsed.Error
		}
		if parsed.PullRequestURL != "" {
			card.PullRequestURL = parsed.PullRequestURL
		}
		card = remember(card, parsed, KindHarness, harnessText(parsed))
	case session.EventTypeSessionClosed:
		card.Status = StatusClosed
		card = remember(card, parsed, KindHarness, harnessText(parsed))
	case session.EventTypeGateDecision:
		if parsed.Decision == decisionDeny {
			card.GateDenials++
			card = remember(card, parsed, KindGate, "gate "+parsed.Gate+" denied: "+firstLine(parsed.Reason))
		}
	case eventTypeAssistant:
		card = foldAssistant(card, parsed)
	case eventTypeUser:
		if parsed.Direction == directionIn {
			card = remember(card, parsed, KindMessage, "message: "+firstLine(messageText(parsed.Event)))
		}
	case eventTypeResult:
		var event executorEvent
		_ = json.Unmarshal(parsed.Event, &event)
		card = remember(card, parsed, KindResult, fmt.Sprintf(resultDurationFormat, parsed.Turn, event.Subtype, event.Duration/1000))
	case eventTypeSystem:
		card = foldSystem(card, parsed)
	}
	return card, true
}

// foldAssistant counts the tool calls of one assistant message and takes the
// model from the first one.
func foldAssistant(card SessionCard, parsed record) SessionCard {
	var event executorEvent
	var message assistantMessage
	if json.Unmarshal(parsed.Event, &event) != nil || json.Unmarshal(event.Message, &message) != nil {
		return card
	}
	if card.Model == "" {
		card.Model = message.Model
	}
	var blocks []contentBlock
	if json.Unmarshal(message.Content, &blocks) != nil {
		return card
	}
	for _, block := range blocks {
		if block.Type != blockToolUse {
			continue
		}
		card.ToolCalls++
		card = remember(card, parsed, KindTool, block.Name+": "+gist(block.Input))
	}
	return card
}

// foldSystem reads the two system events a card shows: the executor refusing
// a tool, and a pull request being pushed.
func foldSystem(card SessionCard, parsed record) SessionCard {
	var event executorEvent
	if json.Unmarshal(parsed.Event, &event) != nil {
		return card
	}
	switch event.Subtype {
	case subtypeDenied:
		card = remember(card, parsed, KindGate, event.ToolName+" denied by the executor")
	case subtypePublished:
		if strings.Contains(event.URL, pullRequestPathMark) {
			card.PullRequestURL = event.URL
		}
	}
	return card
}

// remember appends one recent event, keeping the last few.
func remember(card SessionCard, parsed record, kind string, text string) SessionCard {
	recent := append([]RecentEvent(nil), card.Recent...)
	recent = append(recent, RecentEvent{Time: parsed.Time.UTC().Format(recentTimeFormat), Kind: kind, Text: text})
	if len(recent) > recentKept {
		recent = recent[len(recent)-recentKept:]
	}
	card.Recent = recent
	return card
}

func harnessText(parsed record) string {
	if parsed.Error != "" {
		return parsed.Message + ": " + firstLine(parsed.Error)
	}
	return parsed.Message
}

// messageText is a user record's content when it is one string.
func messageText(raw json.RawMessage) string {
	var event struct {
		Message struct {
			Content json.RawMessage `json:"content"`
		} `json:"message"`
	}
	var text string
	if json.Unmarshal(raw, &event) != nil || json.Unmarshal(event.Message.Content, &text) != nil {
		return ""
	}
	return text
}

// gist is the one-line stand-in for a tool call's input.
func gist(raw json.RawMessage) string {
	var input map[string]json.RawMessage
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}
	for _, key := range gistKeys {
		var value string
		if json.Unmarshal(input[key], &value) == nil && value != "" {
			return firstLine(value)
		}
	}
	return ""
}

// firstLine is the first line of text, bounded.
func firstLine(text string) string {
	line, _, _ := strings.Cut(strings.TrimSpace(text), "\n")
	if len(line) > gistLimit {
		return line[:gistLimit] + "…"
	}
	return line
}

// elapsedLabel renders a duration at minute resolution, so a card's state
// moves once a minute rather than on every line the executor logs.
func elapsedLabel(elapsed time.Duration) string {
	if elapsed < time.Minute {
		return elapsedUnderMinute
	}
	hours := elapsed / time.Hour
	minutes := (elapsed % time.Hour) / time.Minute
	if hours == 0 {
		return fmt.Sprintf(elapsedMinuteFormat, minutes)
	}
	return fmt.Sprintf(elapsedHourFormat, hours, minutes)
}

// tail is one session's reading position: the card folded so far and how
// much of its event log produced it. The follow effect owns one per session.
type tail struct {
	card   SessionCard
	offset int64
}

// read brings the tail up to date with the session's files: the run record
// whole, and the event log from where the last read stopped. It reports
// whether the card changed. A run directory with no run record is not a
// session yet, and reads as unchanged.
func (position *tail) read(files ipcfs.IFiles, assignment string) (bool, error) {
	before := position.card
	content, err := files.ReadFile(path.Join(assignment, session.RunStateFile))
	if errors.Is(err, stdfs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	var state session.RunState
	if err := json.Unmarshal(content, &state); err != nil {
		return false, fmt.Errorf("ops view: decode the run record of %s: %w", assignment, err)
	}
	position.card.Assignment = state.AssignmentID
	position.card.Agent = state.AgentID
	position.card.Branch = state.Branch
	position.card.TicketURL = state.TicketURL
	position.card.Turns = state.Turns
	if err := position.readLog(files, assignment); err != nil {
		return false, err
	}
	return !cardsEqual(before, position.card), nil
}

// readLog folds every complete line past the offset. A trailing line with no
// newline is still being written and is left for the next read.
func (position *tail) readLog(files ipcfs.IFiles, assignment string) error {
	file, err := files.Open(path.Join(assignment, session.EventsFile))
	if errors.Is(err, stdfs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	if err := skip(file, position.offset); err != nil {
		return err
	}
	reader := bufio.NewReaderSize(file, 64<<10)
	for {
		line, err := reader.ReadBytes('\n')
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return fmt.Errorf("ops view: read the event log of %s: %w", assignment, err)
		}
		position.offset += int64(len(line))
		position.card, _ = Fold(position.card, line)
	}
}

// skip moves past the bytes already folded: by seeking when the file can,
// and by reading them otherwise, as an in-memory tree's file does.
func skip(file stdfs.File, offset int64) error {
	if offset == 0 {
		return nil
	}
	if seeker, can := file.(io.Seeker); can {
		_, err := seeker.Seek(offset, io.SeekStart)
		return err
	}
	_, err := io.CopyN(io.Discard, file, offset)
	return err
}

// cardsEqual compares two cards by their wire form, which is also how the
// widget's dirty check sees them.
func cardsEqual(previous SessionCard, next SessionCard) bool {
	before, _ := json.Marshal(previous)
	after, _ := json.Marshal(next)
	return string(before) == string(after)
}
