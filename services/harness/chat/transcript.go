// Copyright 2026 Candace Labs

package chat

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"slices"
	"strings"
	"time"

	"github.com/pmezard/go-difflib/difflib"

	"github.com/candacelabs/csf/pkg/collections"
	"github.com/candacelabs/csf/pkg/textbound"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/sessiongate"
	"github.com/candacelabs/csf/services/opsview"
)

// transcript is the session page's one model: every row it shows, the
// session's task list and the facts its status bar reads, folded from the
// event log one record at a time. Both turn executors write Claude Code's
// stream-json shape into the log (ipc/model/copilotcli translates Copilot's
// records into it), so this one fold serves both and renders the same turn
// the same way; a new executor costs one translation into that shape.
type transcript struct {
	Rows   []row
	Tasks  []task
	Status statusBar
	// Next is the sequence number the next row takes: how many rows were
	// ever added, the ones the cap dropped included.
	Next int
	// TasksRev rises whenever the task list is replaced.
	TasksRev int
}

// row is one line of the transcript: a message, a reply, a tool call, a
// gate's decision, the end of a turn or a harness line. Seq is its key for
// life, which names its live region and its element, so a patch updates the
// element in place and never restarts its animation; Rev rises whenever the
// row changes after it was added.
type row struct {
	Seq  int
	Rev  int
	Kind string
	Turn int
	Time string
	Text string
	HTML template.HTML
	// Denied marks a gate row whose gate refused.
	Denied bool
	Tool   toolCall
}

// The rows a transcript shows, which are also their CSS classes.
const (
	rowOperator = "operator"
	rowAgent    = "agent"
	rowTool     = "tool"
	rowGate     = "gate"
	rowTurn     = "turn"
	rowSystem   = "system"
)

// toolCall is one tool row: the tool and what it acts on, its input and
// output, how it ended and after how long, and an edit's change.
type toolCall struct {
	ID     string
	Name   string
	Target string
	Input  string
	// Output is the result's first lines; Rest the lines after them, which
	// the row keeps behind a disclosure, and RestLines how many there are.
	Output    string
	Rest      string
	RestLines int
	// Cut is set when the result was longer than a row keeps.
	Cut     bool
	State   string
	Started time.Time
	Elapsed string
	Diff    diff
}

// A tool call's states. A call still running when its turn ended never
// reported a result, and reads as ended.
const (
	toolRunning = "running"
	toolDone    = "done"
	toolFailed  = "failed"
	toolEnded   = "ended"
)

// diff is an edit's change as numbered lines, counted whole and shown up to
// diffLineCap lines.
type diff struct {
	Added   int
	Removed int
	Lines   []diffLine
	Hidden  int
}

// diffLine is one line of an edit's change; a line number of zero is a line
// the side does not have.
type diffLine struct {
	Kind string
	Old  int
	New  int
	Text string
}

// The kinds of diff line, which are also their CSS classes: kept, removed,
// added, and the gap between two hunks.
const (
	lineKept    = "kept"
	lineRemoved = "removed"
	lineAdded   = "added"
	lineGap     = "gap"
)

// task is one item of the session's task list.
type task struct {
	Text  string
	State string
}

// The task states, as Claude Code's TodoWrite spells them.
const (
	taskPending = "pending"
	taskActive  = "in_progress"
	taskDone    = "completed"
)

// statusBar is what the status bar reads: who runs the session, how full its
// context is, what the turn and the session cost, when the turn started and
// ended, and what the session is doing. Every field is comparable, so the
// bar's dirty check compares it whole.
type statusBar struct {
	Executor    string
	Model       string
	Context     int64
	TurnCost    float64
	SessionCost float64
	// CostKnown is set once the executor reported a cost; Copilot reports
	// none.
	CostKnown   bool
	Turn        int
	TurnStarted time.Time
	TurnEnded   time.Time
	Phase       string
	// lastCost is the executor's cumulative cost at the previous result.
	lastCost float64
}

// The phases the status bar names.
const (
	phaseIdle     = "idle"
	phaseThinking = "thinking"
	phaseTool     = "running a tool"
)

// Bounds on what one row keeps, so a long session's transcript stays a page
// a phone can hold: the region is sent whole when a row is added.
const (
	rowCap      = 300
	inputLimit  = 1 << 10
	outputLimit = 2 << 10
	// outputHead is how many lines of a result a row shows before its
	// disclosure.
	outputHead  = 6
	diffLineCap = 120
	diffContext = 3
	gateLimit   = 600
	// toolTodoWrite is the tool whose input is the session's task list.
	toolTodoWrite = "TodoWrite"
	toolEdit      = "Edit"
	toolMultiEdit = "MultiEdit"
	toolWrite     = "Write"
	harnessPrefix = "harness_"
	mcpPrefix     = "mcp__"
	// resultSuccess is the executor's subtype for a turn that ended well.
	resultSuccess = "success"
	// clockFormat is the time a row shows: hours and minutes, UTC.
	clockFormat = "15:04"
)

// folds are what each record does to the transcript, by event type. A
// harness record not listed reads as a harness line.
var folds = map[string]func(shown transcript, record *session.Record, worktree string) transcript{
	session.EventTypeUser:             foldUser,
	session.EventTypeAssistant:        foldAssistant,
	session.EventTypeResult:           foldResult,
	session.EventTypeSystem:           foldSystem,
	session.EventTypeGateDecision:     foldGate,
	session.EventTypeTurnRequested:    foldTurnStart,
	session.EventTypeBackgroundResult: foldTurnStart,
	// The transcript already shows what a control action did: the operator's
	// message, the turn, the session's end.
	session.EventTypeControlAction: func(shown transcript, _ *session.Record, _ string) transcript { return shown },
}

// apply folds one event log line into the transcript. It is pure: equal
// transcripts and equal lines fold to equal transcripts, and the transcript
// it was given is not modified.
func (shown transcript) apply(line []byte, worktree string) transcript {
	var record session.Record
	if json.Unmarshal(line, &record) != nil {
		return shown
	}
	fold, known := folds[record.EventType]
	if !known && strings.HasPrefix(record.EventType, harnessPrefix) {
		fold, known = foldHarness, true
	}
	if !known {
		return shown
	}
	shown = fold(shown, &record, worktree)
	shown.Status.Phase = phaseOf(shown)
	return shown
}

// add appends a row under the next sequence number, keeping the last rowCap.
func (shown transcript) add(next row) transcript {
	next.Seq = shown.Next
	shown.Next++
	shown.Rows = append(slices.Clip(shown.Rows), next)
	if len(shown.Rows) > rowCap {
		shown.Rows = shown.Rows[len(shown.Rows)-rowCap:]
	}
	return shown
}

// rowOf is the row of sequence number seq, if the transcript still holds it.
func (shown transcript) rowOf(seq int) (row, bool) {
	return collections.NewKeyedList(func(each row) int { return each.Seq }, shown.Rows).Get(seq)
}

func foldUser(shown transcript, record *session.Record, worktree string) transcript {
	message, ok := record.Stream()
	if !ok {
		return shown
	}
	if record.Direction == session.DirectionIn {
		text := blockText(message.Blocks())
		if text == "" {
			return shown
		}
		return shown.add(row{Kind: rowOperator, Turn: record.Turn, Time: clock(record), Text: text, HTML: renderMarkdown(text)})
	}
	for _, block := range message.Blocks() {
		if block.Type == session.BlockToolResult {
			shown = shown.finish(block.ToolUseID, record.Time, opsview.Scrub(block.ResultText(), worktree), block.IsError)
		}
	}
	return shown
}

// finish records a tool call's result on its row.
func (shown transcript) finish(id string, at time.Time, output string, failed bool) transcript {
	index := slices.IndexFunc(shown.Rows, func(each row) bool { return each.Kind == rowTool && each.Tool.ID == id })
	if id == "" || index < 0 {
		return shown
	}
	shown.Rows = slices.Clone(shown.Rows)
	call := &shown.Rows[index].Tool
	call.State = toolDone
	if failed {
		call.State = toolFailed
	}
	call.Elapsed = elapsedWords(at.Sub(call.Started))
	call.Output, call.Rest, call.RestLines, call.Cut = splitOutput(output)
	shown.Rows[index].Rev++
	return shown
}

func foldAssistant(shown transcript, record *session.Record, worktree string) transcript {
	message, ok := record.Stream()
	if !ok {
		return shown
	}
	if record.Provider != "" {
		shown.Status.Executor = record.Provider
	}
	if message.Message.Model != "" {
		shown.Status.Model = message.Message.Model
	}
	if usage := message.Message.Usage; usage != nil && usage.Context() > 0 {
		shown.Status.Context = usage.Context()
	}
	for _, block := range message.Blocks() {
		switch {
		case block.Type == session.BlockText && strings.TrimSpace(block.Text) != "":
			text := strings.ToValidUTF8(block.Text, "�")
			shown = shown.add(row{Kind: rowAgent, Turn: record.Turn, Time: clock(record), Text: text, HTML: renderMarkdown(text)})
		case block.Type == session.BlockToolUse && block.Name == toolTodoWrite:
			shown.Tasks = tasksOf(block.Input)
			shown.TasksRev++
		case block.Type == session.BlockToolUse:
			shown = shown.add(row{Kind: rowTool, Turn: record.Turn, Time: clock(record), Tool: callOf(block, record.Time, worktree)})
		}
	}
	return shown
}

// callOf is a tool row as the call starts.
func callOf(block session.ContentBlock, at time.Time, worktree string) toolCall {
	return toolCall{
		ID: block.ID, Name: toolLabel(block.Name), Target: opsview.Target(block.Name, block.Input, worktree),
		Input: textbound.Prefix(opsview.Scrub(indented(block.Input), worktree), inputLimit),
		State: toolRunning, Started: at, Diff: diffOf(block.Name, block.Input),
	}
}

// toolLabel is a tool's name as a row shows it: an MCP tool as its server
// and operation.
func toolLabel(name string) string {
	if server, operation, found := strings.Cut(strings.TrimPrefix(name, mcpPrefix), "__"); strings.HasPrefix(name, mcpPrefix) && found {
		return server + " · " + operation
	}
	return name
}

// indented is a tool's input as indented JSON with its keys sorted, or as
// sent when it is not JSON. Sorting is what makes one call read the same from
// either executor: Copilot's translation writes the keys in sorted order and
// Claude Code in the model's.
func indented(raw json.RawMessage) string {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	// A tool's input is whatever shape the tool chose.
	var input any
	if decoder.Decode(&input) != nil {
		return string(raw)
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if encoder.Encode(input) != nil {
		return string(raw)
	}
	return strings.TrimSuffix(buffer.String(), "\n")
}

// splitOutput is a result as a row keeps it: its first outputHead lines, the
// rest and how many lines that is, and whether it was cut at outputLimit.
func splitOutput(output string) (string, string, int, bool) {
	kept := textbound.Prefix(strings.TrimRight(output, "\n"), outputLimit)
	cut := kept != strings.TrimRight(output, "\n")
	lines := strings.Split(kept, "\n")
	if len(lines) <= outputHead {
		return kept, "", 0, cut
	}
	return strings.Join(lines[:outputHead], "\n"), strings.Join(lines[outputHead:], "\n"), len(lines) - outputHead, cut
}

// editInput is the part of an edit's input its change is read from, in
// Claude Code's names, which Copilot's records are translated into.
type editInput struct {
	OldString string `json:"old_string"`
	NewString string `json:"new_string"`
	Content   string `json:"content"`
	Edits     []struct {
		OldString string `json:"old_string"`
		NewString string `json:"new_string"`
	} `json:"edits"`
}

// diffOf is an edit's change: an Edit's old and new text, each of a
// MultiEdit's edits in turn, or a Write's content, every line added. The
// change is read from the call's input, which both executors record, so an
// edit shows the same from either; its line numbers count from the edited
// text, not the file.
func diffOf(tool string, raw json.RawMessage) diff {
	var input editInput
	if json.Unmarshal(raw, &input) != nil {
		return diff{}
	}
	var change diff
	switch tool {
	case toolEdit:
		change = change.with(input.OldString, input.NewString)
	case toolMultiEdit:
		for _, edit := range input.Edits {
			change = change.with(edit.OldString, edit.NewString)
		}
	case toolWrite:
		change = change.with("", input.Content)
	}
	return change
}

// with adds one change, before to after, in hunks of diffContext kept lines
// around each run of changed lines.
func (change diff) with(before string, after string) diff {
	old, updated := textLines(before), textLines(after)
	for index, group := range difflib.NewMatcher(old, updated).GetGroupedOpCodes(diffContext) {
		if index > 0 || len(change.Lines) > 0 {
			change = change.line(diffLine{Kind: lineGap})
		}
		for _, operation := range group {
			if operation.Tag == 'e' {
				for offset := range operation.I2 - operation.I1 {
					change = change.line(diffLine{Kind: lineKept, Old: operation.I1 + offset + 1, New: operation.J1 + offset + 1, Text: old[operation.I1+offset]})
				}
				continue
			}
			for at := operation.I1; at < operation.I2; at++ {
				change.Removed++
				change = change.line(diffLine{Kind: lineRemoved, Old: at + 1, Text: old[at]})
			}
			for at := operation.J1; at < operation.J2; at++ {
				change.Added++
				change = change.line(diffLine{Kind: lineAdded, New: at + 1, Text: updated[at]})
			}
		}
	}
	return change
}

// line appends one line, or counts it hidden past diffLineCap.
func (change diff) line(next diffLine) diff {
	if len(change.Lines) >= diffLineCap {
		change.Hidden++
		return change
	}
	change.Lines = append(slices.Clip(change.Lines), next)
	return change
}

// textLines is text as lines, without their newlines; empty text has none.
func textLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(strings.TrimSuffix(strings.ToValidUTF8(text, "�"), "\n"), "\n")
}

// todoInput is TodoWrite's input: the whole task list, every time.
type todoInput struct {
	Todos []struct {
		Content    string `json:"content"`
		Status     string `json:"status"`
		ActiveForm string `json:"activeForm"`
	} `json:"todos"`
}

// tasksOf is the task list a TodoWrite call sets; the task being worked on
// reads as what is being done.
func tasksOf(raw json.RawMessage) []task {
	var input todoInput
	if json.Unmarshal(raw, &input) != nil {
		return nil
	}
	tasks := make([]task, 0, len(input.Todos))
	for _, todo := range input.Todos {
		text := todo.Content
		if todo.Status == taskActive && todo.ActiveForm != "" {
			text = todo.ActiveForm
		}
		tasks = append(tasks, task{Text: text, State: todo.Status})
	}
	return tasks
}

func foldResult(shown transcript, record *session.Record, _ string) transcript {
	message, _ := record.Stream()
	shown.Status.TurnEnded = record.Time
	if cost := message.TotalCostUSD; cost != nil {
		// The executor reports its cost cumulatively within one process and
		// from zero when the process is reopened, so a turn cost what the
		// total rose by, or all of it when the total fell.
		added := *cost - shown.Status.lastCost
		if *cost < shown.Status.lastCost {
			added = *cost
		}
		shown.Status.lastCost = *cost
		shown.Status.TurnCost, shown.Status.SessionCost, shown.Status.CostKnown = added, shown.Status.SessionCost+added, true
	}
	if slices.ContainsFunc(shown.Rows, func(each row) bool { return each.Kind == rowTool && each.Tool.State == toolRunning }) {
		shown.Rows = slices.Clone(shown.Rows)
		for index := range shown.Rows {
			if shown.Rows[index].Kind == rowTool && shown.Rows[index].Tool.State == toolRunning {
				shown.Rows[index].Tool.State = toolEnded
				shown.Rows[index].Rev++
			}
		}
	}
	return shown.add(row{Kind: rowTurn, Turn: record.Turn, Time: clock(record), Text: fmt.Sprintf("Turn %d %s in %s", record.Turn, resultWord(message.Subtype), durationWords(message.DurationMS))})
}

// systemEvent is the part of a system event the transcript reads beyond the
// stream message: the tool the executor refused.
type systemEvent struct {
	ToolName string `json:"tool_name"`
}

// subtypeDenied is the system event of a tool the executor itself refused.
const subtypeDenied = "permission_denied"

func foldSystem(shown transcript, record *session.Record, _ string) transcript {
	message, ok := record.Stream()
	if !ok {
		return shown
	}
	if record.Provider != "" {
		shown.Status.Executor = record.Provider
	}
	if message.Model != "" && shown.Status.Model == "" {
		shown.Status.Model = message.Model
	}
	if message.Subtype != subtypeDenied {
		return shown
	}
	var event systemEvent
	_ = json.Unmarshal(record.Event, &event)
	return shown.add(row{Kind: rowGate, Denied: true, Turn: record.Turn, Time: clock(record), Text: "The executor refused " + event.ToolName})
}

// shownGates are the gates whose every decision is a row; any other gate is
// shown only when it refuses.
var shownGates = map[string]string{
	sessiongate.GateReply:    "Reply gate",
	sessiongate.GateCommit:   "Commit gate",
	sessiongate.GateQuestion: "Question gate",
}

// decisionWords are a gate's decisions in words.
var decisionWords = map[string]string{
	sessiongate.DecisionAllow:     "allowed",
	sessiongate.DecisionDeny:      "refused",
	sessiongate.DecisionOpened:    "opened the draft pull request",
	sessiongate.DecisionExists:    "the pull request is open",
	sessiongate.DecisionFailed:    "failed",
	sessiongate.DecisionLimit:     "let it through at its limit",
	sessiongate.DecisionMalformed: "could not read the hook input",
}

func foldGate(shown transcript, record *session.Record, worktree string) transcript {
	name, shownAlways := shownGates[record.Gate]
	denied := record.Decision == sessiongate.DecisionDeny
	if record.Decision == sessiongate.DecisionSkip || !shownAlways && !denied {
		return shown
	}
	if name == "" {
		name = "The " + record.Gate + " gate"
	}
	words, known := decisionWords[record.Decision]
	if !known {
		words = record.Decision
	}
	text := name + " " + words
	if blocked := blockedQuestions(record.Questions); len(record.Questions) > 0 {
		text += fmt.Sprintf(" (%d of %d questions refused)", blocked, len(record.Questions))
	}
	if record.Reason != "" {
		text += ": " + opsview.HumanError(record.Reason, worktree)
	}
	return shown.add(row{Kind: rowGate, Denied: denied || record.Decision == sessiongate.DecisionFailed, Turn: record.Turn, Time: clock(record), Text: textbound.Prefix(text, gateLimit)})
}

// blockedQuestions counts the questions a gate refused.
func blockedQuestions(verdicts []session.QuestionVerdict) int {
	blocked := 0
	for _, verdict := range verdicts {
		if verdict.Blocked {
			blocked++
		}
	}
	return blocked
}

func foldTurnStart(shown transcript, record *session.Record, worktree string) transcript {
	shown.Status.Turn, shown.Status.TurnStarted, shown.Status.TurnEnded, shown.Status.TurnCost = record.Turn, record.Time, time.Time{}, 0
	return foldHarness(shown, record, worktree)
}

func foldHarness(shown transcript, record *session.Record, worktree string) transcript {
	text := record.Message
	if record.Error != "" {
		text = opsview.HumanError(record.Error, worktree)
	}
	return shown.add(row{Kind: rowSystem, Turn: record.Turn, Time: clock(record), Text: textbound.Prefix(text, gateLimit)})
}

// phaseOf is what the session is doing: idle between turns, running a tool
// while a call of the turn has no result, and thinking otherwise. A gate
// deciding leaves no record until it has decided, so the log cannot show it.
func phaseOf(shown transcript) string {
	status := shown.Status
	if status.TurnStarted.IsZero() || !status.TurnEnded.Before(status.TurnStarted) {
		return phaseIdle
	}
	for index := len(shown.Rows) - 1; index >= 0 && shown.Rows[index].Kind != rowTurn; index-- {
		if shown.Rows[index].Kind == rowTool && shown.Rows[index].Tool.State == toolRunning {
			return phaseTool
		}
	}
	return phaseThinking
}

// blockText is a message's text blocks, joined by a newline.
func blockText(blocks []session.ContentBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		if block.Type == session.BlockText && block.Text != "" {
			parts = append(parts, block.Text)
		}
	}
	return strings.ToValidUTF8(strings.Join(parts, "\n"), "�")
}

func clock(record *session.Record) string { return record.Time.UTC().Format(clockFormat) }

// resultWord is a turn's outcome in a word.
func resultWord(subtype string) string {
	if subtype == resultSuccess || subtype == "" {
		return "finished"
	}
	return "ended (" + strings.ReplaceAll(subtype, "_", " ") + ")"
}

// durationWords is a turn's duration, given in milliseconds.
func durationWords(milliseconds float64) string {
	return elapsedWords(time.Duration(milliseconds) * time.Millisecond)
}

// elapsedWords is a duration as a row shows it: tenths of a second under
// ten seconds, seconds under a minute, minutes and seconds above.
func elapsedWords(elapsed time.Duration) string {
	switch {
	case elapsed < 0:
		return ""
	case elapsed < 10*time.Second:
		return fmt.Sprintf("%.1fs", elapsed.Seconds())
	case elapsed < time.Minute:
		return fmt.Sprintf("%.0fs", elapsed.Seconds())
	}
	return fmt.Sprintf("%dm %02ds", int(elapsed.Minutes()), int(elapsed.Seconds())%60)
}
