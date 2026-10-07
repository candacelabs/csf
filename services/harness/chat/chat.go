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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/candacelabs/csf/pkg/collections"
	"github.com/candacelabs/csf/pkg/gotth/live"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/opsview"
	"github.com/candacelabs/csf/services/views"
)

// Routes: the page for one session and the live mount under it. The live
// handler routes by path suffix, so one application serves every session.
const (
	PagePath      = opsview.ChatPathPrefix + ":assignment"
	LivePath      = PagePath + liveSuffix
	liveAssetPath = LivePath + "/*asset"
	liveSuffix    = "/live"
	pagePrefix    = opsview.ChatPathPrefix

	fragmentStatus     = "chat.status"
	fragmentTasks      = "chat.tasks"
	fragmentTranscript = "chat.transcript"
	fragmentBar        = "chat.bar"
	fragmentComposer   = "chat.composer"
	// rowRegionPrefix starts each row's region, under the transcript's: the
	// row's sequence number follows.
	rowRegionPrefix = fragmentTranscript + ":r"

	// Events a browser may send. Cancel acts only after the session's name
	// was confirmed: eventAskCancel asks (Esc does too), eventKeep withdraws
	// the question. eventToggle opens or closes the row fieldRow names.
	eventSend      = "chat.send"
	eventCancel    = "chat.cancel"
	eventAskCancel = "chat.ask_cancel"
	eventKeep      = "chat.keep"
	eventToggle    = "chat.toggle"
	fieldRow       = "row"
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

// sourceWords are the effects as a notice names them.
var sourceWords = map[string]string{sourceFollow: "Following the session", sourceSend: "Send", sourceCancel: "Cancel"}

// assignmentKey carries the assignment from the request into Init.
type assignmentKey struct{}

// Chat is the live application and its routes, and the collector of how long
// a record takes to reach its transcript.
type Chat struct {
	service *harness.AgentSessionService
	live    *live.App[chatState, live.AnonymousIdentity]
	logger  *slog.Logger
	settle  prometheus.Histogram
}

var (
	_ runtime.IService     = (*Chat)(nil)
	_ prometheus.Collector = (*Chat)(nil)
)

// settleBuckets are the settle series' buckets: from a record's timestamp in
// the event log to the page's live session accepting it, per record and per
// connected page, which services/views catalogues and panels. They span a
// record that lands in milliseconds to one that waited on a slow tail.
var settleBuckets = []float64{0.005, 0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5}

// NewChat builds the chat over service. origins is the browser Origin
// allowlist, one per address the host serves; nothing else is accepted.
func NewChat(service *harness.AgentSessionService, origins []string, logger *slog.Logger) (*Chat, error) {
	if service == nil {
		return nil, ErrNoService
	}
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	chat := &Chat{service: service, logger: logger, settle: prometheus.NewHistogram(views.HistogramOpts(views.MetricChatSettle, settleBuckets))}
	app, err := live.New(live.Config[chatState, live.AnonymousIdentity]{
		Init:   chat.initialize,
		Reduce: chat.reduce,
		Fragments: []live.Fragment[chatState]{
			{ID: fragmentStatus, Render: renderStatus, Dirty: statusChanged},
			{ID: fragmentTasks, Render: renderTasks, Dirty: tasksChanged},
			// Each row is a region of its own: a row that changes patches
			// alone, and a new row re-sends the transcript, whose morph adds
			// the one element and leaves every other where it is.
			{ID: fragmentTranscript, Render: renderTranscript, Dirty: transcriptChanged, Children: rowRegions},
			{ID: fragmentBar, Render: renderBar, Dirty: barChanged},
			{ID: fragmentComposer, Render: renderComposer, Dirty: composerChanged},
		},
		Events:       []string{eventSend, eventCancel, eventAskCancel, eventKeep, eventToggle},
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

// Describe sends the settle series' description.
func (chat *Chat) Describe(descriptions chan<- *prometheus.Desc) { chat.settle.Describe(descriptions) }

// Collect sends the settle series.
func (chat *Chat) Collect(metrics chan<- prometheus.Metric) { chat.settle.Collect(metrics) }

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
	Transcript transcript
	// Queued is what the operator sent that the session has not read yet,
	// oldest first.
	Queued []string
	// Open is the rows this viewer opened, by sequence number. It is the
	// server's, so a patch to an open row renders it open: the morph never
	// closes what the viewer opened.
	Open     []int
	Cursor   int
	Notice   string
	Pending  bool
	Finished bool
	// Confirm is set while the page asks the operator to confirm a cancel.
	Confirm bool
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
	// Real is the real session's state: alive while the virtual session is
	// starting, running, open or canceling and not suspended; suspended while
	// it is idle with its executor closed; closed once the virtual session
	// ended.
	Real     string
	Worktree string
}

// The real session's states, as the header says them.
const (
	realAlive     = "alive"
	realSuspended = "suspended"
	realClosed    = "closed"
)

// liveVirtualPhases keep a real session alive unless the session is
// suspended.
var liveVirtualPhases = map[harnessv1.AgentSessionPhase]bool{
	harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_STARTING:  true,
	harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING:   true,
	harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN:      true,
	harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELING: true,
}

func viewOf(state *harnessv1.AgentSessionState) sessionView {
	real := realClosed
	switch {
	case state.GetSuspended():
		real = realSuspended
	case liveVirtualPhases[state.GetPhase()]:
		real = realAlive
	}
	return sessionView{
		AgentID:        state.GetAgentId(),
		Phase:          strings.ToLower(strings.TrimPrefix(state.GetPhase().String(), "AGENT_SESSION_PHASE_")),
		Turns:          state.GetTurns(),
		Queued:         state.GetQueued(),
		Branch:         state.GetBranch(),
		PullRequestURL: state.GetPullRequestUrl(),
		Error:          opsview.HumanError(state.GetError(), state.GetWorktree()),
		Real:           real,
		Worktree:       state.GetWorktree(),
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
		state.Transcript = state.Transcript.apply(line, state.Session.Worktree)
	}
	state.Cursor = tail.Sequence()
	return state, []live.Effect[live.AnonymousIdentity]{{Source: sourceFollow, Run: chat.follow(assignment, state.Cursor)}}, nil
}

// follow streams the session's records into the connection from cursor on,
// one frame at a time: every record one look at the log finds (the tail
// looks every harness.DefaultTailInterval) goes out as one event, so as one
// patch, followed by the session's state.
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
			frame := [][]byte{line}
			for more := tail.Available(); more != nil; more = tail.Available() {
				frame = append(frame, more)
			}
			if err := emit(live.Event{Name: eventRecord, Fields: live.NewFields(map[string]string{fieldLine: string(bytes.Join(frame, recordSeparator))})}); err != nil {
				return err
			}
			for _, record := range frame {
				var stamped struct {
					Time time.Time `json:"time"`
				}
				if json.Unmarshal(record, &stamped) == nil && !stamped.Time.IsZero() {
					chat.settle.Observe(time.Since(stamped.Time).Seconds())
				}
			}
			if err := chat.emitState(ctx, assignment, emit); err != nil {
				return err
			}
		}
	}
}

// recordSeparator joins a frame's records in one event: a record is one JSON
// line and holds no raw newline.
var recordSeparator = []byte("\n")

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
		for _, line := range bytes.Split([]byte(event.Fields.Get(fieldLine)), recordSeparator) {
			before := state.Transcript.Next
			state.Transcript = state.Transcript.apply(line, state.Session.Worktree)
			state.Cursor++
			if rows := state.Transcript.Rows; state.Transcript.Next != before && rows[len(rows)-1].Kind == rowOperator {
				state.Queued = read(state.Queued, rows[len(rows)-1].Text)
			}
		}
		return state, nil
	case eventToggle:
		state.Open = toggled(state.Open, event.Fields.Get(fieldRow))
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
		state.Pending, state.Notice = false, ""
		state.Queued = append(slices.Clip(state.Queued), event.Fields.Get(fieldMessage))
		return state, nil
	case eventAskCancel:
		state.Confirm = true
		return state, nil
	case eventKeep:
		state.Confirm = false
		return state, nil
	case eventCancel:
		if !state.Confirm {
			// A cancel the operator has not confirmed asks first.
			state.Confirm = true
			return state, nil
		}
		state.Confirm = false
		state.Notice = "Cancel requested; the session stops at its next safepoint."
		return state, []live.Effect[live.AnonymousIdentity]{{Source: sourceCancel, Run: chat.cancel(state.Assignment)}}
	case live.EffectFailedEvent:
		state.Pending = false
		source := event.Fields.Get(live.EffectFailedSourceField)
		if word, known := sourceWords[source]; known {
			source = word
		}
		state.Notice = source + " failed: " + opsview.HumanError(event.Fields.Get(live.EffectFailedErrorField), state.Session.Worktree)
		return state, nil
	}
	return state, nil
}

func (chat *Chat) send(assignment string, message string) func(ctx context.Context, session live.Session[live.AnonymousIdentity], emit live.Emitter) error {
	return func(ctx context.Context, _ live.Session[live.AnonymousIdentity], emit live.Emitter) error {
		// The chat is the operator's console: what it sends is the operator's
		// own words, so the harness vets their terms.
		response, err := chat.service.Send(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: assignment, Message: message, OperatorAuthored: true})
		if err != nil {
			return err
		}
		return emit(live.Event{Name: eventSent, Fields: live.NewFields(map[string]string{fieldTurn: response.GetTurnId(), fieldMessage: message})})
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
	return previous.Session != next.Session || previous.Notice != next.Notice || previous.Finished != next.Finished || previous.Confirm != next.Confirm
}

// transcriptChanged is the transcript's own markup beyond its rows: the
// queue. A row added or dropped re-renders it through its regions.
func transcriptChanged(previous, next chatState) bool {
	return !slices.Equal(previous.Queued, next.Queued)
}

func tasksChanged(previous, next chatState) bool {
	return previous.Transcript.TasksRev != next.Transcript.TasksRev
}

// rowRegions are the transcript's rows as regions, in order.
func rowRegions(state chatState) []live.Fragment[chatState] {
	regions := make([]live.Fragment[chatState], 0, len(state.Transcript.Rows))
	for _, shown := range state.Transcript.Rows {
		regions = append(regions, live.Fragment[chatState]{ID: rowRegion(shown.Seq), Render: renderRow(shown.Seq), Dirty: rowChanged(shown.Seq)})
	}
	return regions
}

func rowRegion(seq int) string { return rowRegionPrefix + strconv.Itoa(seq) }

// rowChanged is whether the row of seq changed or was opened or closed.
func rowChanged(seq int) func(previous, next chatState) bool {
	return func(previous, next chatState) bool {
		before, _ := previous.Transcript.rowOf(seq)
		after, _ := next.Transcript.rowOf(seq)
		return before.Rev != after.Rev || slices.Contains(previous.Open, seq) != slices.Contains(next.Open, seq)
	}
}

// toggled is open with the row named by key opened, or closed if it was
// open. A key that names no row's sequence number changes nothing.
func toggled(open []int, key string) []int {
	seq, err := strconv.Atoi(key)
	if err != nil {
		return open
	}
	return collections.Set[int](open).Toggle(seq)
}

func barChanged(previous, next chatState) bool {
	return previous.Transcript.Status != next.Transcript.Status || previous.Session.Phase != next.Session.Phase
}

func composerChanged(previous, next chatState) bool {
	return previous.Pending != next.Pending || previous.Session.Phase != next.Session.Phase || previous.Finished != next.Finished
}

// read is queued without the first message equal to text: the one the
// session has now read.
func read(queued []string, text string) []string {
	index := slices.IndexFunc(queued, func(message string) bool { return strings.TrimSpace(message) == strings.TrimSpace(text) })
	if index < 0 {
		return queued
	}
	return slices.Delete(slices.Clone(queued), index, index+1)
}
