// Copyright 2026 Candace Labs

package opsview

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	stdfs "io/fs"
	"maps"
	"path"
	"strings"
	"time"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/pkg/textbound"
	"github.com/candacelabs/csf/services/harness/session"
)

// Status is where a session stands, read from its event log.
type Status string

// The statuses a card shows. A session is running from its first turn
// request until the harness records the turn's end; a finished session keeps
// its turn executor open for the next message until the harness closes it
// idle, after which the session is suspended: open on its conversation with
// no executor process until the next message resumes it.
const (
	StatusRunning   Status = "running"
	StatusFinished  Status = "finished"
	StatusSuspended Status = "suspended"
	// StatusWaiting is a finished turn whose executor still runs background
	// tasks: the completion of one wakes the session with no Send, so it is
	// not stranded.
	StatusWaiting Status = "waiting"
	StatusFailed  Status = "failed"
	StatusClosed  Status = "closed"
	// StatusStalled is a session whose log says running but whose last
	// record is older than the host now serving the page: the host that ran
	// its turn stopped, and no host is running it.
	StatusStalled Status = "stalled"
)

// otherStatuses are the statuses the idle and finished list filters by.
var otherStatuses = []Status{StatusFinished, StatusWaiting, StatusSuspended, StatusFailed, StatusStalled, StatusClosed}

// PullRequestStage is how far the control plane has moved a session's pull
// request.
type PullRequestStage string

const (
	PullRequestReady PullRequestStage = "ready"
	// PullRequestMerging is a merge the control plane started and has not
	// finished: the merge path is running its checks.
	PullRequestMerging PullRequestStage = "merging"
	PullRequestMerged  PullRequestStage = "merged"
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
	// Text is the event in words; Raw is the record as the executor or the
	// harness wrote it, home directories removed, for the raw view.
	Text string `json:"text"`
	Raw  string `json:"raw,omitempty"`
}

// SessionCard is everything the ops view shows about one harness session,
// derived from its run record and its event log and nothing else. It is the
// session widget's state: a pure projection, replaced whole whenever the
// follow effect reads more of the log.
type SessionCard struct {
	Assignment     string `json:"assignment"`
	Agent          string `json:"agent"`
	Branch         string `json:"branch"`
	TicketURL      string `json:"ticket_url"`
	PullRequestURL string `json:"pull_request_url"`
	Model          string `json:"model"`
	// Executor is the turn executor the session runs on.
	Executor    string `json:"executor"`
	Status      Status `json:"status"`
	Turns       int    `json:"turns"`
	ToolCalls   int    `json:"tool_calls"`
	GateDenials int    `json:"gate_denials"`
	// Resumes counts the session's resumes after a suspend.
	Resumes int `json:"resumes"`
	// Background is how many tasks the executor last reported running in the
	// background; BackgroundTurns how many turns a background completion
	// started.
	Background      int       `json:"background"`
	BackgroundTurns int       `json:"background_turns"`
	StartedAt       time.Time `json:"started_at"`
	// Worktree is the session's working tree, which activity lines are
	// relative to. It is the fold's input, not something the page shows.
	Worktree string `json:"-"`
	// Activity is what the session is doing now, in words: its latest tool
	// call or harness step.
	Activity string `json:"activity,omitempty"`
	// Said is the first line of the last thing the session wrote to the
	// operator, and Question is whether it ends by asking something.
	Said     string `json:"said,omitempty"`
	Question bool   `json:"question,omitempty"`
	// LastDay is the UTC day of the newest record read: a card moves to
	// another day at most once a day.
	LastDay string `json:"last_day,omitempty"`
	// Partial is set when the card was folded from the end of its event log
	// only, so its counts and spend cover that window; the follow effect
	// reads the whole log when the session moves or its card is opened.
	Partial bool `json:"partial,omitempty"`
	// Problem is Error in words.
	Problem string `json:"problem,omitempty"`
	// LastAt is the follow effect's own bookkeeping for Elapsed and is not on
	// the wire: a line that moves nothing a card shows then moves nothing on
	// the page, and the executor logs such lines several times a second.
	LastAt  time.Time `json:"-"`
	Elapsed string    `json:"elapsed"`
	Error   string    `json:"error"`
	// Launch is how the session was launched, container or host, from its
	// launch receipt once it closes.
	Launch string `json:"launch,omitempty"`
	// CPU and MemoryPeak are a container or sandboxed session's cgroup
	// receipt, shown once the session closes; empty for a plain host session.
	CPU        string `json:"cpu,omitempty"`
	MemoryPeak string `json:"memory_peak,omitempty"`
	// PullRequest is where the session's pull request stands as far as the
	// control plane moved it: empty, ready, or merged.
	PullRequest PullRequestStage `json:"pull_request,omitempty"`
	// MergedOn is the UTC day the control plane merged the pull request.
	MergedOn string `json:"merged_on,omitempty"`
	// CostByDay is the session's spend in USD per UTC day, folded from its
	// result records the way the mining loop folds a fixer's cost.
	CostByDay map[string]float64 `json:"cost_by_day,omitempty"`
	// lastCost is the executor's cumulative total_cost_usd at the previous
	// result: the fold's own bookkeeping, never on the wire.
	lastCost float64
	// Notice is the outcome of the last action the page took on the card. Like
	// Expanded it is the page's, and survives the card being replaced, as do
	// the fields after it up to Recent.
	Notice string `json:"notice,omitempty"`
	// Internals shows the card's identifiers, raw counts and raw activity;
	// Composing shows its composer.
	Internals bool `json:"internals,omitempty"`
	Composing bool `json:"composing,omitempty"`
	// Confirm is the action the card is asking the operator to confirm, and
	// Pending the action whose typed result the card is waiting for.
	Confirm string `json:"confirm,omitempty"`
	Pending string `json:"pending,omitempty"`
	// Landed is set when the merges read from the forge list the card's pull
	// request as merged into main today.
	Landed bool `json:"landed,omitempty"`
	// Forge is the card's open pull request as the forge last listed it: its
	// diff stat and checks.
	Forge OpenPullRequest `json:"forge"`
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
	blockText            = "text"
	questionMark         = "?"
	turnStartedFormat    = "Turn %d started"
	turnFinishedFormat   = "Turn %d finished"
	turnDoneFormat       = "Turn %d done in %s"
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
	resumedTurnFormat    = "resumed after %.0fs suspended: first token after %.1fs"
	backgroundFormat     = "turn %d woken: %s %s after %.0fs"
	exitCodeFormat       = ", exit %d"
)

// gistKeys are the tool-input fields whose first line stands for the call,
// in the order one is preferred.
var gistKeys = []string{"description", "command", "file_path", "pattern", "prompt"}

// record is the part of one events.jsonl line this view reads.
type record struct {
	Time            time.Time       `json:"time"`
	Level           string          `json:"level"`
	Message         string          `json:"msg"`
	EventType       string          `json:"event_type"`
	Turn            int             `json:"turn"`
	Direction       string          `json:"direction"`
	Event           json.RawMessage `json:"event"`
	Gate            string          `json:"gate"`
	Decision        string          `json:"decision"`
	Reason          string          `json:"reason"`
	Error           string          `json:"error"`
	PullRequestURL  string          `json:"pull_request_url"`
	CPUMicroseconds int64           `json:"cpu_usec"`
	MemoryPeakBytes uint64          `json:"memory_peak_bytes"`
	Action          string          `json:"action"`
	Operator        bool            `json:"operator_authored"`
	TurnID          string          `json:"turn_id"`
	Launch          string          `json:"launch"`
	// A resumed turn's cost, on the harness's turn-resumed record.
	TimeToFirstTokenMs float64 `json:"time_to_first_token_ms"`
	SuspendedSeconds   float64 `json:"suspended_seconds"`
	Status             string  `json:"status"`
	Summary            string  `json:"summary"`
	TaskElapsed        float64 `json:"task_elapsed_ms"`
	ExitCode           *int    `json:"exit_code"`
}

// executorEvent is the stream-json event a turn executor record carries.
// Message is raw because it is an object on an assistant event and a string
// on a system one.
type executorEvent struct {
	Subtype  string          `json:"subtype"`
	ToolName string          `json:"tool_name"`
	URL      string          `json:"url"`
	Duration float64         `json:"duration_ms"`
	Cost     float64         `json:"total_cost_usd"`
	Message  json.RawMessage `json:"message"`
	// Tasks is the executor's background tasks on a background_tasks_changed
	// event.
	Tasks []session.BackgroundTask `json:"tasks"`
}

// assistantMessage is the message an assistant event carries.
type assistantMessage struct {
	Model   string          `json:"model"`
	Content json.RawMessage `json:"content"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Name  string          `json:"name"`
	Text  string          `json:"text"`
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
		card.LastDay = parsed.Time.UTC().Format(time.DateOnly)
	}
	card.Elapsed = elapsedLabel(card.LastAt.Sub(card.StartedAt))
	switch parsed.EventType {
	case session.EventTypeRunStarted:
		card.Status = StatusRunning
		card = remember(card, parsed, KindHarness, "Session started", parsed.Message)
	case session.EventTypeTurnRequested:
		card.Status = StatusRunning
		card.Error, card.Problem = "", ""
		card = remember(card, parsed, KindHarness, fmt.Sprintf(turnStartedFormat, parsed.Turn), parsed.Message)
	case session.EventTypeRunFinished:
		card.Status = StatusFinished
		text := fmt.Sprintf(turnFinishedFormat, parsed.Turn)
		if parsed.Level == levelError {
			card.Status = StatusFailed
			card.Error = Scrub(parsed.Error, card.Worktree)
			card.Problem = HumanError(parsed.Error, card.Worktree)
			text = card.Problem
		}
		if parsed.PullRequestURL != "" {
			card.PullRequestURL = parsed.PullRequestURL
		}
		card = remember(card, parsed, KindHarness, text, harnessText(parsed))
	case session.EventTypeSessionClosed:
		card.Status = StatusClosed
		card.Background = 0
		card = remember(card, parsed, KindHarness, "Session closed", harnessText(parsed))
	case session.EventTypeSessionSuspended:
		card.Status = StatusSuspended
		card = remember(card, parsed, KindHarness, "Suspended: idle, its real session closed until the next message", harnessText(parsed))
	case session.EventTypeSessionResumed:
		card.Status = StatusRunning
		card.Resumes++
		card = remember(card, parsed, KindHarness, "Resumed on a new real session", harnessText(parsed))
	case session.EventTypeTurnResumed:
		text := fmt.Sprintf(resumedTurnFormat, parsed.SuspendedSeconds, parsed.TimeToFirstTokenMs/1000)
		card = remember(card, parsed, KindHarness, text, text)
	case session.EventTypeBackgroundResult:
		card.BackgroundTurns++
		text := backgroundText(parsed)
		card = remember(card, parsed, KindHarness, text, text)
	case session.EventTypeSandboxReceipt:
		card.CPU = formatSeconds(parsed.CPUMicroseconds)
		card.MemoryPeak = formatBytes(parsed.MemoryPeakBytes)
		text := "Sandbox: cpu " + card.CPU + ", memory peak " + card.MemoryPeak
		card = remember(card, parsed, KindHarness, text, text)
	case session.EventTypeLaunchReceipt:
		card.Launch = parsed.Launch
		text := "Launched on the " + parsed.Launch
		if parsed.Launch == session.LaunchContainer && parsed.CPUMicroseconds > 0 {
			card.CPU = formatSeconds(parsed.CPUMicroseconds)
			card.MemoryPeak = formatBytes(parsed.MemoryPeakBytes)
			text = "Container: cpu " + card.CPU + ", memory peak " + card.MemoryPeak
		}
		card = remember(card, parsed, KindHarness, text, text)
	case session.EventTypeGateDecision:
		if parsed.Decision == decisionDeny {
			card.GateDenials++
			card = remember(card, parsed, KindGate, "A gate stopped a step: "+firstLine(HumanError(parsed.Reason, card.Worktree)), "gate "+parsed.Gate+" denied: "+firstLine(parsed.Reason))
		}
	case eventTypeAssistant:
		card = foldAssistant(card, parsed)
	case eventTypeUser:
		if parsed.Direction == directionIn {
			text := firstLine(messageText(parsed.Event))
			card = remember(card, parsed, KindMessage, "Message: "+text, "message: "+text)
		}
	case eventTypeResult:
		var event executorEvent
		_ = json.Unmarshal(parsed.Event, &event)
		card = spend(card, parsed.Time, event.Cost)
		card = remember(card, parsed, KindResult, fmt.Sprintf(turnDoneFormat, parsed.Turn, durationLabel(event.Duration)),
			fmt.Sprintf(resultDurationFormat, parsed.Turn, event.Subtype, event.Duration/1000))
	case session.EventTypeControlAction:
		card = foldAction(card, parsed)
	case eventTypeSystem:
		card = foldSystem(card, parsed)
	}
	card.Status = waiting(card.Status, card.Background)
	return card, true
}

// waiting is a finished turn's status while background tasks run, and back
// to finished once none does.
func waiting(status Status, background int) Status {
	switch {
	case status == StatusFinished && background > 0:
		return StatusWaiting
	case status == StatusWaiting && background == 0:
		return StatusFinished
	}
	return status
}

// backgroundText lists a background result: the turn it started, the task,
// how it ended and after how long.
func backgroundText(parsed record) string {
	text := fmt.Sprintf(backgroundFormat, parsed.Turn, firstLine(parsed.Summary), parsed.Status, parsed.TaskElapsed/1000)
	if parsed.ExitCode != nil {
		text += fmt.Sprintf(exitCodeFormat, *parsed.ExitCode)
	}
	return text
}

// foldAssistant counts the tool calls of one assistant message, takes the
// model from the first one and keeps the last thing the session said.
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
		switch block.Type {
		case blockToolUse:
			card.ToolCalls++
			card = remember(card, parsed, KindTool, Activity(block.Name, block.Input, card.Worktree), RawActivity(block.Name, block.Input, card.Worktree))
		case blockText:
			last := lastParagraph(block.Text)
			if last == "" {
				continue
			}
			// A question is what the operator must read; otherwise the
			// message's opening line says what the session did.
			card.Question = strings.HasSuffix(last, questionMark)
			said := block.Text
			if card.Question {
				said = last
			}
			card.Said = bounded(Scrub(plainLine(said), card.Worktree))
		}
	}
	return card
}

// plainLine is the first line of text that says something, without its
// Markdown: a heading or a label line ending in a colon is skipped for the
// line after it, and emphasis, code marks and list bullets are dropped.
func plainLine(text string) string {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	for index, line := range lines {
		plain := strings.TrimSpace(markdownMarks.Replace(strings.TrimLeft(strings.TrimSpace(line), "#>-*0123456789. ")))
		if plain == "" || strings.HasSuffix(plain, ":") && index < len(lines)-1 {
			continue
		}
		return firstLine(plain)
	}
	return ""
}

var markdownMarks = strings.NewReplacer("**", "", "__", "", "`", "")

// lastParagraph is the last non-empty paragraph of a message: where a
// session puts its question.
func lastParagraph(text string) string {
	paragraphs := strings.Split(strings.TrimSpace(text), "\n\n")
	return strings.TrimSpace(paragraphs[len(paragraphs)-1])
}

// foldSystem reads the system events a card shows: the executor refusing a
// tool, a pull request being pushed, and the executor's background tasks.
func foldSystem(card SessionCard, parsed record) SessionCard {
	var event executorEvent
	if json.Unmarshal(parsed.Event, &event) != nil {
		return card
	}
	switch event.Subtype {
	case subtypeDenied:
		card = remember(card, parsed, KindGate, "The executor refused "+event.ToolName, event.ToolName+" denied by the executor")
	case subtypePublished:
		if strings.Contains(event.URL, pullRequestPathMark) {
			card.PullRequestURL = event.URL
		}
	case session.SubtypeBackgroundTasksChanged:
		card.Background = len(event.Tasks)
	}
	return card
}

// spend attributes one result's cost to its UTC day. The executor reports
// total_cost_usd cumulatively within one process and from zero when the
// process is reopened, so a result adds what it rose by, or all of it when it
// fell: the mining loop's fixer fold, read one line at a time.
func spend(card SessionCard, at time.Time, cumulative float64) SessionCard {
	added := cumulative - card.lastCost
	if cumulative < card.lastCost {
		added = cumulative
	}
	card.lastCost = cumulative
	if added <= 0 {
		return card
	}
	byDay := make(map[string]float64, len(card.CostByDay)+1)
	maps.Copy(byDay, card.CostByDay)
	byDay[at.UTC().Format(time.DateOnly)] += added
	card.CostByDay = byDay
	return card
}

// controlWordings are the control actions in words, by action.
var controlWordings = map[string]string{
	session.ActionSubmit:       "Launched",
	session.ActionSend:         "Message sent",
	session.ActionCancel:       "Cancel requested",
	session.ActionReady:        "Pull request marked ready",
	session.ActionMergeStarted: "Merge started: the merge path runs its checks",
	session.ActionMerge:        "Pull request merged",
}

// foldAction reads one control action record: the pull request's stage moves
// on a ready, a merge starting and a merge that succeeded, and every action
// is listed.
func foldAction(card SessionCard, parsed record) SessionCard {
	failed := parsed.Level == levelError
	raw := "control: " + parsed.Action
	if parsed.Operator {
		raw += " (operator)"
	}
	if parsed.TurnID != "" {
		raw += ", turn " + parsed.TurnID
	}
	text, known := controlWordings[parsed.Action]
	if !known {
		text = "Control: " + parsed.Action
	}
	if parsed.Operator {
		text += " by the operator"
	}
	if failed {
		if card.PullRequest == PullRequestMerging {
			card.PullRequest = PullRequestReady
		}
		return remember(card, parsed, KindGate, strings.TrimSuffix(text, " by the operator")+" failed: "+HumanError(parsed.Error, card.Worktree), raw+" failed: "+firstLine(parsed.Error))
	}
	switch parsed.Action {
	case session.ActionReady:
		if card.PullRequest == "" {
			card.PullRequest = PullRequestReady
		}
	case session.ActionMergeStarted:
		card.PullRequest = PullRequestMerging
	case session.ActionMerge:
		card.PullRequest = PullRequestMerged
		card.MergedOn = parsed.Time.UTC().Format(time.DateOnly)
	}
	return remember(card, parsed, KindHarness, text, raw)
}

// remember appends one recent event, keeping the last few, and makes it the
// card's activity.
func remember(card SessionCard, parsed record, kind string, text string, raw string) SessionCard {
	recent := append([]RecentEvent(nil), card.Recent...)
	recent = append(recent, RecentEvent{Time: parsed.Time.UTC().Format(recentTimeFormat), Kind: kind, Text: Scrub(text, card.Worktree), Raw: Scrub(raw, card.Worktree)})
	if len(recent) > recentKept {
		recent = recent[len(recent)-recentKept:]
	}
	card.Recent = recent
	card.Activity = recent[len(recent)-1].Text
	return card
}

func harnessText(parsed record) string {
	if parsed.Error != "" {
		return parsed.Message + ": " + firstLine(parsed.Error)
	}
	return parsed.Message
}

// durationLabel renders a turn's duration, given in milliseconds.
func durationLabel(milliseconds float64) string {
	elapsed := time.Duration(milliseconds) * time.Millisecond
	if elapsed < time.Minute {
		return fmt.Sprintf("%.0fs", elapsed.Seconds())
	}
	return elapsedLabel(elapsed)
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
	return textbound.Prefix(line, gistLimit)
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

// formatSeconds renders a cgroup's cpu time, given in microseconds, at tenth-
// of-a-second resolution.
func formatSeconds(microseconds int64) string {
	return fmt.Sprintf("%.1fs", float64(microseconds)/1e6)
}

// formatBytes renders a byte count in binary units.
func formatBytes(bytes uint64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	divisor, exponent := uint64(unit), 0
	for quotient := bytes / unit; quotient >= unit && exponent < 3; quotient /= unit {
		divisor *= unit
		exponent++
	}
	return fmt.Sprintf("%.1f %ciB", float64(bytes)/float64(divisor), "KMGT"[exponent])
}

// tail is one session's reading position: the card folded so far and how
// much of its event log produced it. The follow effect owns one per session.
type tail struct {
	card   SessionCard
	offset int64
}

// read brings the tail up to date with the session's files: the run record
// whole, and the event log from where the last read stopped. A first read
// that is not whole folds only the end of the log, tailWindow bytes, and
// marks the card Partial; a whole read of a partial card folds the log again
// from its start. It reports whether the card changed. A run directory with
// no run record is not a session yet, and reads as unchanged.
func (position *tail) read(files iofs.IFiles, assignment string, whole bool) (bool, error) {
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
	if whole && position.card.Partial {
		*position = tail{}
	}
	position.card.Assignment = state.AssignmentID
	position.card.Agent = state.AgentID
	position.card.Executor = string(state.TurnExecutor())
	position.card.Branch = state.Branch
	position.card.TicketURL = state.TicketURL
	position.card.Turns = state.Turns
	position.card.Worktree = state.Worktree
	if err := position.readLog(files, assignment, whole); err != nil {
		return false, err
	}
	return !cardsEqual(before, position.card), nil
}

// tailWindow is how much of the end of an event log a first read folds: the
// last 20 records (recentKept) of every one of 143 logs measured on a host on
// 2026-10-05 fit in it (p99 161 KB, largest 169 KB), so an idle session's
// card shows its last events and the status they set.
const tailWindow = 256 << 10

// readLog folds every complete line past the offset. A trailing line with no
// newline is still being written and is left for the next read.
func (position *tail) readLog(files iofs.IFiles, assignment string, whole bool) error {
	file, err := files.Open(path.Join(assignment, session.EventsFile))
	if errors.Is(err, stdfs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()
	reader := bufio.NewReaderSize(file, 64<<10)
	seeker, seekable := file.(io.Seeker)
	if info, err := file.Stat(); err == nil && seekable && !whole && position.offset == 0 && info.Size() > tailWindow {
		// The first record dates the session; the window is everything else.
		if first, err := reader.ReadBytes('\n'); err == nil {
			position.card, _ = Fold(position.card, first)
		}
		if _, err := seeker.Seek(info.Size()-tailWindow, io.SeekStart); err != nil {
			return err
		}
		reader.Reset(file)
		cut, err := reader.ReadBytes('\n')
		if err != nil {
			return nil
		}
		position.offset = info.Size() - tailWindow + int64(len(cut))
		position.card.Partial = true
	} else if err := skip(file, position.offset); err != nil {
		return err
	}
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
