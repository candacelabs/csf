// Copyright 2026 Candace Labs

// Package chat is the Workbench chat for harness sessions: a gotth-live page
// that shows one session's transcript as its event log grows and sends the
// operator's messages into the open session through the harness service, in
// process. It is served from the harness host app; it owns no listener.
//
// # Measured on a phone
//
// mobile_browser_test.go drives the page in headless Chromium emulating a
// phone (390x844, device scale 3, touch) over a throttled network (100 ms
// latency, 1.6 Mbps down, 750 kbps up, shaping the page's requests; the
// WebSocket's frames pass it untouched), with the renderer warmed by one
// prior load. Milliseconds, median of five where several were taken:
//
//	page                      first paint  live   record to paint  send round trip
//	before (2026-10-02)       140          246    172 (64–228)     36
//	after  (2026-10-02)       144          260    165 (78–238)     25
//
// The first paint figures are from navigation start, 138 and 141 ms after
// the request left the browser; the page was never slow, it was clumsy.
// docs/mobile-before.png and docs/mobile-after.png are the page as measured.
// What changed on a phone: the composer is pinned above the keyboard and the
// viewport does not jump when it opens; the transcript is anchored to its
// newest message unless the operator scrolls up; text is 16 px or larger
// with 44 px tap targets; Markdown and code render, and a code block
// scrolls sideways instead of widening the page. Replies arrive at the
// executor's granularity, one message per record, each morphed in without
// a reflow of the rest.
package chat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/candacelabs/csf/pkg/gotth/live"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/harness"
)

// Routes: the page for one session and the live mount under it. The live
// handler routes by path suffix, so one application serves every session.
const (
	PagePath      = "/chat/:assignment"
	LivePath      = "/chat/:assignment/live"
	liveAssetPath = LivePath + "/*asset"
	liveSuffix    = "/live"
	pagePrefix    = "/chat/"

	fragmentStatus     = "chat.status"
	fragmentTranscript = "chat.transcript"
	fragmentComposer   = "chat.composer"

	// Events a browser may send.
	eventSend   = "chat.send"
	eventCancel = "chat.cancel"
	// Events the effects emit; never registered, so a browser cannot forge a
	// transcript record.
	eventRecord   = "chat.record"
	eventState    = "chat.state"
	eventSent     = "chat.sent"
	eventFinished = "chat.finished"

	fieldMessage = "message"
	fieldLine    = "line"
	fieldState   = "state"
	fieldTurn    = "turn"

	sourceFollow = "chat.follow"
	sourceSend   = "chat.send"
	sourceCancel = "chat.cancel"

	// transcriptCap bounds the rendered transcript; older entries drop off.
	transcriptCap = 400
	// assignmentParameter is the route parameter naming the session.
	assignmentParameter = "assignment"
)

var (
	// ErrNoService reports a chat built without the harness service.
	ErrNoService = errors.New("harness chat: the session service is required")
	// ErrNoAssignment reports a page or connection whose path names no
	// assignment.
	ErrNoAssignment = errors.New("harness chat: the request names no assignment")
)

// assignmentKey carries the assignment from the request into Init.
type assignmentKey struct{}

// Chat is the live application and its routes.
type Chat struct {
	service *harness.AgentSessionService
	live    *live.App[chatState, live.AnonymousIdentity]
	logger  *slog.Logger
}

var _ runtime.IService = (*Chat)(nil)

// NewChat builds the chat over service. origins is the browser Origin
// allowlist, one per address the host serves; nothing else is accepted.
func NewChat(service *harness.AgentSessionService, origins []string, logger *slog.Logger) (*Chat, error) {
	if service == nil {
		return nil, ErrNoService
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	chat := &Chat{service: service, logger: logger}
	app, err := live.New(live.Config[chatState, live.AnonymousIdentity]{
		Init:   chat.initialize,
		Reduce: chat.reduce,
		Fragments: []live.Fragment[chatState]{
			{ID: fragmentStatus, Render: renderStatus, Dirty: statusChanged},
			{ID: fragmentTranscript, Render: renderTranscript, Dirty: transcriptChanged},
			{ID: fragmentComposer, Render: renderComposer, Dirty: composerChanged},
		},
		Events:       []string{eventSend, eventCancel},
		Origins:      origins,
		Authenticate: live.Anonymous,
		Authorize:    live.AllowAll[live.AnonymousIdentity],
		CSRF:         live.NoCSRFCheck,
		Logger:       logger,
	})
	if err != nil {
		return nil, err
	}
	chat.live = app
	return chat, nil
}

// Start starts the live application's connection scope.
func (chat *Chat) Start(scope *runtime.Scope) error { return chat.live.Start(scope) }

// Register mounts the page and the live routes on the caller's router.
func (chat *Chat) Register(router gin.IRouter) {
	page := chat.live.PageHandler(renderPage)
	upgrade := chat.live.Handler()
	router.GET(PagePath, chat.withAssignment(page))
	router.GET(LivePath, chat.withAssignment(upgrade))
	router.GET(liveAssetPath, chat.withAssignment(upgrade))
}

// withAssignment puts the route's assignment into the request context, which
// is the context Init receives for both the page and the upgrade.
func (chat *Chat) withAssignment(handler http.Handler) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		assignment := ctx.Param(assignmentParameter)
		request := ctx.Request.WithContext(context.WithValue(ctx.Request.Context(), assignmentKey{}, assignment))
		handler.ServeHTTP(ctx.Writer, request)
	}
}

// PageURL is the chat page of one assignment under base, the host's address.
func PageURL(base string, assignment string) string {
	return strings.TrimRight(base, "/") + pagePrefix + assignment
}

// chatState is one connection's view: the session's state, the transcript
// read so far and where the next record is.
type chatState struct {
	Assignment string
	Mount      string
	Session    sessionView
	Entries    []entry
	Cursor     int
	Notice     string
	Pending    bool
	Finished   bool
}

// sessionView is the part of the session state the page shows.
type sessionView struct {
	AgentID        string
	Phase          string
	Turns          uint32
	Queued         uint32
	Branch         string
	PullRequestURL string
	Error          string
}

func viewOf(state *harnessv1.AgentSessionState) sessionView {
	return sessionView{
		AgentID:        state.GetAgentId(),
		Phase:          strings.ToLower(strings.TrimPrefix(state.GetPhase().String(), "AGENT_SESSION_PHASE_")),
		Turns:          state.GetTurns(),
		Queued:         state.GetQueued(),
		Branch:         state.GetBranch(),
		PullRequestURL: state.GetPullRequestUrl(),
		Error:          state.GetError(),
	}
}

func (chat *Chat) initialize(ctx context.Context, _ live.Session[live.AnonymousIdentity]) (chatState, []live.Effect[live.AnonymousIdentity], error) {
	assignment, _ := ctx.Value(assignmentKey{}).(string)
	if assignment == "" {
		return chatState{}, nil, ErrNoAssignment
	}
	state := chatState{Assignment: assignment, Mount: pagePrefix + assignment + liveSuffix}
	current, err := chat.service.Get(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: assignment})
	if err != nil {
		state.Notice = err.Error()
	} else {
		state.Session = viewOf(current.GetSession())
	}
	tail, err := chat.service.OpenTail(ctx, assignment, 0)
	if err != nil {
		state.Notice = err.Error()
		return state, nil, nil
	}
	defer func() { _ = tail.Close() }()
	for line := tail.Available(); line != nil; line = tail.Available() {
		state.Entries = appendEntry(state.Entries, entryOf(line))
	}
	state.Cursor = tail.Sequence()
	return state, []live.Effect[live.AnonymousIdentity]{{Source: sourceFollow, Run: chat.follow(assignment, state.Cursor)}}, nil
}

// follow streams the session's records into the connection from cursor on,
// and the session's state whenever a record arrived.
func (chat *Chat) follow(assignment string, cursor int) func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
	return func(ctx context.Context, _ live.Session[live.AnonymousIdentity], emit live.Emitter) error {
		tail, err := chat.service.OpenTail(ctx, assignment, cursor)
		if err != nil {
			return err
		}
		defer func() { _ = tail.Close() }()
		for {
			line, err := tail.Next(ctx)
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil
			}
			if err != nil {
				if err := emit(live.Event{Name: eventFinished}); err != nil {
					return err
				}
				return nil
			}
			if err := emit(live.Event{Name: eventRecord, Fields: live.NewFields(map[string]string{fieldLine: string(line)})}); err != nil {
				return err
			}
			if err := chat.emitState(ctx, assignment, emit); err != nil {
				return err
			}
		}
	}
}

func (chat *Chat) emitState(ctx context.Context, assignment string, emit live.Emitter) error {
	current, err := chat.service.Get(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: assignment})
	if err != nil {
		return nil
	}
	encoded, err := json.Marshal(viewOf(current.GetSession()))
	if err != nil {
		return err
	}
	return emit(live.Event{Name: eventState, Fields: live.NewFields(map[string]string{fieldState: string(encoded)})})
}

// reduce is the pure transition; every effect it returns closes over the
// service the chat owns.
func (chat *Chat) reduce(state chatState, event live.Event) (chatState, []live.Effect[live.AnonymousIdentity]) {
	switch event.Name {
	case eventRecord:
		state.Entries = appendEntry(state.Entries, entryOf([]byte(event.Fields.Get(fieldLine))))
		state.Cursor++
		return state, nil
	case eventState:
		var view sessionView
		if json.Unmarshal([]byte(event.Fields.Get(fieldState)), &view) == nil {
			state.Session = view
		}
		return state, nil
	case eventFinished:
		state.Finished = true
		return state, nil
	case eventSend:
		message := strings.TrimSpace(event.Fields.Get(fieldMessage))
		if message == "" {
			state.Notice = "Write a message first."
			return state, nil
		}
		state.Pending, state.Notice = true, ""
		return state, []live.Effect[live.AnonymousIdentity]{{Source: sourceSend, Run: chat.send(state.Assignment, message)}}
	case eventSent:
		state.Pending = false
		state.Notice = "Queued as turn " + event.Fields.Get(fieldTurn) + "."
		return state, nil
	case eventCancel:
		state.Notice = "Cancel requested; the session stops at its next safepoint."
		return state, []live.Effect[live.AnonymousIdentity]{{Source: sourceCancel, Run: chat.cancel(state.Assignment)}}
	case live.EffectFailedEvent:
		state.Pending = false
		state.Notice = event.Fields.Get(live.EffectFailedSourceField) + " failed: " + event.Fields.Get(live.EffectFailedErrorField)
		return state, nil
	}
	return state, nil
}

func (chat *Chat) send(assignment string, message string) func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
	return func(ctx context.Context, _ live.Session[live.AnonymousIdentity], emit live.Emitter) error {
		response, err := chat.service.Send(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: assignment, Message: message})
		if err != nil {
			return err
		}
		return emit(live.Event{Name: eventSent, Fields: live.NewFields(map[string]string{fieldTurn: response.GetTurnId()})})
	}
}

func (chat *Chat) cancel(assignment string) func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
	return func(ctx context.Context, _ live.Session[live.AnonymousIdentity], emit live.Emitter) error {
		if _, err := chat.service.Cancel(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: assignment}); err != nil {
			return err
		}
		return chat.emitState(ctx, assignment, emit)
	}
}

func statusChanged(previous, next chatState) bool {
	return previous.Session != next.Session || previous.Notice != next.Notice || previous.Finished != next.Finished
}

func transcriptChanged(previous, next chatState) bool {
	return previous.Cursor != next.Cursor || len(previous.Entries) != len(next.Entries)
}

func composerChanged(previous, next chatState) bool {
	return previous.Pending != next.Pending || previous.Session.Phase != next.Session.Phase || previous.Finished != next.Finished
}

// entry is one transcript line. What the operator or the agent wrote is
// rendered from Markdown into HTML once, when the record is read; a tool
// result, a gate's reason and a harness record stay plain text; an assistant
// entry also lists the tools it called.
type entry struct {
	Kind  string
	Turn  int
	Time  string
	Text  string
	HTML  template.HTML
	Tools []string
}

// Transcript kinds, which are also the CSS classes.
const (
	kindUser      = "user"
	kindAssistant = "assistant"
	kindTool      = "tool"
	kindResult    = "result"
	kindGate      = "gate"
	kindHarness   = "harness"
)

// record is the part of an events.jsonl line the transcript reads.
type record struct {
	Time      time.Time       `json:"time"`
	Message   string          `json:"msg"`
	EventType string          `json:"event_type"`
	Turn      int             `json:"turn"`
	Direction string          `json:"direction"`
	Event     json.RawMessage `json:"event"`
	Decision  string          `json:"decision"`
	Reason    string          `json:"reason"`
	Error     string          `json:"error"`
}

// streamMessage is the stream-json message an executor event carries.
type streamMessage struct {
	Type    string `json:"type"`
	Subtype string `json:"subtype"`
	Message struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
	Result     string  `json:"result"`
	IsError    bool    `json:"is_error"`
	DurationMS float64 `json:"duration_ms"`
}

type contentBlock struct {
	Type    string          `json:"type"`
	Text    string          `json:"text"`
	Name    string          `json:"name"`
	Input   json.RawMessage `json:"input"`
	Content json.RawMessage `json:"content"`
}

// entryOf reads one record into a transcript entry; records the transcript
// does not show return the zero entry.
func entryOf(line []byte) entry {
	var parsed record
	if json.Unmarshal(line, &parsed) != nil {
		return entry{}
	}
	stamp := parsed.Time.UTC().Format(time.TimeOnly)
	switch {
	case parsed.EventType == "user" && parsed.Direction == "in":
		text := contentText(parsed.Event)
		return entry{Kind: kindUser, Turn: parsed.Turn, Time: stamp, Text: text, HTML: renderMarkdown(text)}
	case parsed.EventType == "user":
		return entry{Kind: kindTool, Turn: parsed.Turn, Time: stamp, Text: "tool result: " + truncate(contentText(parsed.Event), 240)}
	case parsed.EventType == "assistant":
		text, tools := assistantText(parsed.Event)
		shown := entry{Kind: kindAssistant, Turn: parsed.Turn, Time: stamp, Text: text, Tools: tools}
		if text != "" {
			shown.HTML = renderMarkdown(text)
		}
		return shown
	case parsed.EventType == "result":
		var message streamMessage
		_ = json.Unmarshal(parsed.Event, &message)
		return entry{Kind: kindResult, Turn: parsed.Turn, Time: stamp, Text: fmt.Sprintf("turn %d finished: %s after %.0fs", parsed.Turn, message.Subtype, message.DurationMS/1000)}
	case parsed.EventType == "session_gate_decision" && parsed.Decision == "deny":
		return entry{Kind: kindGate, Turn: parsed.Turn, Time: stamp, Text: parsed.Reason}
	case strings.HasPrefix(parsed.EventType, "harness_"):
		text := parsed.Message
		if parsed.Error != "" {
			text += ": " + parsed.Error
		}
		return entry{Kind: kindHarness, Turn: parsed.Turn, Time: stamp, Text: text}
	}
	return entry{}
}

// contentText is a message's content: a string, or the text of its blocks.
func contentText(raw json.RawMessage) string {
	var message streamMessage
	if json.Unmarshal(raw, &message) != nil {
		return ""
	}
	var text string
	if json.Unmarshal(message.Message.Content, &text) == nil {
		return text
	}
	var blocks []contentBlock
	if json.Unmarshal(message.Message.Content, &blocks) != nil {
		return ""
	}
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch {
		case block.Text != "":
			parts = append(parts, block.Text)
		case block.Type == "tool_result":
			var nested string
			if json.Unmarshal(block.Content, &nested) == nil {
				parts = append(parts, nested)
			}
		}
	}
	return strings.Join(parts, "\n")
}

// assistantText is what the assistant said, and the tools it called as one
// line each.
func assistantText(raw json.RawMessage) (string, []string) {
	var message streamMessage
	if json.Unmarshal(raw, &message) != nil {
		return "", nil
	}
	var blocks []contentBlock
	if json.Unmarshal(message.Message.Content, &blocks) != nil {
		return "", nil
	}
	parts := make([]string, 0, len(blocks))
	var tools []string
	for _, block := range blocks {
		switch block.Type {
		case "text":
			if block.Text != "" {
				parts = append(parts, block.Text)
			}
		case "tool_use":
			tools = append(tools, "→ "+block.Name+" "+truncate(toolInput(block.Input), 200))
		}
	}
	return strings.Join(parts, "\n\n"), tools
}

// toolInput is the one-line gist of a tool call's input.
func toolInput(raw json.RawMessage) string {
	var input map[string]json.RawMessage
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}
	for _, key := range []string{"description", "command", "file_path", "pattern", "prompt"} {
		var value string
		if json.Unmarshal(input[key], &value) == nil && value != "" {
			return strings.SplitN(value, "\n", 2)[0]
		}
	}
	return ""
}

func truncate(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return text[:limit] + "…"
}

// appendEntry adds a shown entry and keeps the transcript bounded. An
// assistant record that neither said nor called anything is not shown.
func appendEntry(entries []entry, next entry) []entry {
	if next.Kind == "" || (next.Kind == kindAssistant && next.Text == "" && len(next.Tools) == 0) {
		return entries
	}
	entries = append(entries, next)
	if len(entries) > transcriptCap {
		entries = entries[len(entries)-transcriptCap:]
	}
	return entries
}
