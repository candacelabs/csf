// Copyright 2026 Candace Labs

package session

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/candacelabs/csf/pkg/affect"
	"github.com/candacelabs/csf/pkg/terms"
)

// The stream-json event types, directions and content block types the
// harness reads back from its own event log, spelled as Claude Code spells
// them.
const (
	EventTypeUser      = "user"
	EventTypeAssistant = "assistant"
	EventTypeResult    = "result"
	EventTypeSystem    = "system"

	DirectionIn  = "in"
	DirectionOut = "out"

	BlockText       = "text"
	BlockToolUse    = "tool_use"
	BlockToolResult = "tool_result"

	// MaxRecordBytes bounds one event log line read back; a tool input can
	// carry a whole file.
	MaxRecordBytes = 16 << 20
	recordBuffer   = 64 << 10
)

// Record is one event log line as the harness reads it back: the keys every
// record carries and the keys of the record kinds the harness acts on.
type Record struct {
	Time      time.Time       `json:"time"`
	Message   string          `json:"msg"`
	EventType string          `json:"event_type"`
	Turn      int             `json:"turn"`
	Direction string          `json:"direction"`
	Event     json.RawMessage `json:"event"`
	// Provider is the turn executor that reported Event.
	Provider string `json:"provider"`
	Gate     string `json:"gate"`
	Decision string `json:"decision"`
	Reason   string `json:"reason"`
	Error    string `json:"error"`
	// Action is a control action record's action: submit, send, cancel,
	// ready, merge_started or merge.
	Action  string       `json:"action"`
	Terms   []terms.Term `json:"terms"`
	Rulings []Ruling     `json:"rulings"`
	// Questions is a question gate decision's verdicts and QuestionWanted a
	// send that overrides the gate.
	Questions      []QuestionVerdict `json:"questions"`
	QuestionWanted bool              `json:"question_wanted"`
	// Affect is an operator affect record's reading; nil on every other kind.
	Affect *affect.Reading `json:"affect"`
}

// QuestionVerdict is one question an agent put to the operator, the class
// the question gate placed it in and whether the gate refused it.
type QuestionVerdict struct {
	Class   string `json:"class"`
	Blocked bool   `json:"blocked"`
}

// StreamMessage is the stream-json event an executor record carries.
type StreamMessage struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	// Model is the model a session's init event names.
	Model   string `json:"model"`
	Message struct {
		Content json.RawMessage `json:"content"`
		Model   string          `json:"model"`
		// StopReason is end_turn on the message that ends a turn's reply.
		StopReason string `json:"stop_reason"`
		Usage      *Usage `json:"usage"`
	} `json:"message"`
	Result     string  `json:"result"`
	IsError    bool    `json:"is_error"`
	DurationMS float64 `json:"duration_ms"`
	// TotalCostUSD is a result's cost so far, cumulative within one executor
	// process; nil when the executor reports none, as Copilot does not.
	TotalCostUSD *float64 `json:"total_cost_usd"`
	Usage        *Usage   `json:"usage"`
	// Tasks is the background tasks running, on a background_tasks_changed
	// system event.
	Tasks []BackgroundTask `json:"tasks"`
}

// StopReasonEndTurn is the stop reason of the message that ends a reply.
const StopReasonEndTurn = "end_turn"

// Usage is the tokens a message or a result counted.
type Usage struct {
	InputTokens   int64 `json:"input_tokens"`
	OutputTokens  int64 `json:"output_tokens"`
	CacheRead     int64 `json:"cache_read_input_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens"`
}

// Context is the tokens the model read for a message: its whole input,
// cached or not, which is how full its context was.
func (usage *Usage) Context() int64 {
	return usage.InputTokens + usage.CacheRead + usage.CacheCreation
}

// BackgroundTask is one task the turn executor runs in the background, as its
// background_tasks_changed event lists it.
type BackgroundTask struct {
	TaskID      string `json:"task_id"`
	TaskType    string `json:"task_type"`
	Description string `json:"description"`
}

// SubtypeBackgroundTasksChanged is the system event the turn executor writes
// with the whole list of its background tasks whenever one starts or ends.
const SubtypeBackgroundTasksChanged = "background_tasks_changed"

// LiveBackgroundTasks is the background tasks the turn executor last reported
// running in records: the list of its latest background_tasks_changed event.
// A closed or resumed executor runs none until it reports again.
func LiveBackgroundTasks(records []Record) []BackgroundTask {
	live := []BackgroundTask{}
	for index := range records {
		record := &records[index]
		switch record.EventType {
		case EventTypeSessionClosed, EventTypeRunResumed:
			live = []BackgroundTask{}
		case EventTypeSystem:
			if message, ok := record.Stream(); ok && message.Subtype == SubtypeBackgroundTasksChanged {
				live = append([]BackgroundTask{}, message.Tasks...)
			}
		}
	}
	return live
}

// ContentBlock is one block of a stream-json message's content: ID names a
// tool_use block's call, and ToolUseID and IsError are a tool_result block's
// call and outcome.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Input     json.RawMessage `json:"input"`
	Content   json.RawMessage `json:"content"`
	ToolUseID string          `json:"tool_use_id"`
	IsError   bool            `json:"is_error"`
}

// ResultText is a tool_result block's content as text: a string content, or
// its text blocks joined by a newline.
func (block *ContentBlock) ResultText() string {
	var text string
	if json.Unmarshal(block.Content, &text) == nil {
		return text
	}
	var blocks []ContentBlock
	if json.Unmarshal(block.Content, &blocks) != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, each := range blocks {
		if each.Type == BlockText {
			parts = append(parts, each.Text)
		}
	}
	return strings.Join(parts, "\n")
}

// ToolUse is one tool call an assistant message made.
type ToolUse struct {
	// ID is the call's tool_use_id.
	ID    string
	Name  string
	Input json.RawMessage
}

// Stream decodes the executor event the record carries; ok is false for a
// record that carries none.
func (record *Record) Stream() (message StreamMessage, ok bool) {
	if len(record.Event) == 0 || json.Unmarshal(record.Event, &message) != nil {
		return StreamMessage{}, false
	}
	return message, true
}

// Blocks is the message's content as blocks: a string content is one text
// block, and content of neither shape is no block.
func (message *StreamMessage) Blocks() []ContentBlock {
	var text string
	if json.Unmarshal(message.Message.Content, &text) == nil {
		return []ContentBlock{{Type: BlockText, Text: text}}
	}
	var blocks []ContentBlock
	if json.Unmarshal(message.Message.Content, &blocks) != nil {
		return nil
	}
	return blocks
}

// Assistant is what the assistant said in the record, its text blocks joined
// by a blank line, and the tools it called, in order. A record that is not an
// assistant event has neither.
func (record *Record) Assistant() (text []string, tools []ToolUse) {
	if record.EventType != EventTypeAssistant {
		return nil, nil
	}
	message, ok := record.Stream()
	if !ok {
		return nil, nil
	}
	for _, block := range message.Blocks() {
		switch block.Type {
		case BlockText:
			if block.Text != "" {
				text = append(text, block.Text)
			}
		case BlockToolUse:
			tools = append(tools, ToolUse{ID: block.ID, Name: block.Name, Input: block.Input})
		}
	}
	return text, tools
}

// Text is the text blocks of the executor event the record carries, in
// order: what the message said, whoever sent it.
func (record *Record) Text() []string {
	message, ok := record.Stream()
	if !ok {
		return nil
	}
	text := []string{}
	for _, block := range message.Blocks() {
		if block.Type == BlockText && block.Text != "" {
			text = append(text, block.Text)
		}
	}
	return text
}

// ReadRecords reads the event log of the run in directory and returns the
// records keep accepts, in order. A line that is not a record is skipped.
func ReadRecords(directory string, keep func(record *Record) bool) ([]Record, error) {
	records := []Record{}
	err := scanLines(filepath.Join(directory, EventsFile), func(line []byte) error {
		var record Record
		if json.Unmarshal(line, &record) != nil || !keep(&record) {
			return nil
		}
		records = append(records, record)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return records, nil
}

// ReadTurnRecords returns the records of the turn running: the last turn
// start, a turn request or a background result, and every record after it.
// No turn started is no records.
func ReadTurnRecords(directory string) ([]Record, error) {
	records, err := ReadRecords(directory, func(_ *Record) bool { return true })
	if err != nil {
		return nil, err
	}
	return TurnRecords(records), nil
}

// TurnRecords is the part of a run's records that belongs to the turn
// running, as [ReadTurnRecords] reads it.
func TurnRecords(records []Record) []Record {
	for index := len(records) - 1; index >= 0; index-- {
		if records[index].EventType == EventTypeTurnRequested || records[index].EventType == EventTypeBackgroundResult {
			return records[index:]
		}
	}
	return []Record{}
}

// scanLines hands visit every line of the file at path, without its newline.
func scanLines(path string, visit func(line []byte) error) error {
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("harness session: open event log: %w", err)
	}
	defer func() { _ = file.Close() }()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, recordBuffer), MaxRecordBytes)
	for scanner.Scan() {
		if err := visit(scanner.Bytes()); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("harness session: read event log %s: %w", path, err)
	}
	return nil
}
