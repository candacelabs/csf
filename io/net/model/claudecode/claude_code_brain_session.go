// Copyright 2026 Candace Labs

package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/mailbox"
)

var (
	// ErrSessionClosed reports a turn or an interrupt on an open session whose
	// Claude Code process has exited or been closed.
	ErrSessionClosed = errors.New("claudecode session: the Claude Code process is closed")
	// ErrTurnAbandoned reports a turn whose caller stopped waiting before
	// Claude Code reported the result; the process may still be working on it.
	ErrTurnAbandoned = errors.New("claudecode session: the turn was abandoned before its result")
)

// Stream-json control messages the open session writes.
const (
	messageTypeControlRequest = "control_request"
	controlSubtypeInterrupt   = "interrupt"
	// EventTypeControlResponse answers a control request; it is forwarded like
	// every other event.
	EventTypeControlResponse = "control_response"
)

// ClaudeCodeSession is one Claude Code process kept open across turns: the
// stream-json input stays open, every turn's messages are written to it, and
// the events of each turn are read to its result. It is the turn executor an
// open harness session holds, so a later Send is a turn on the same process
// rather than a new one resuming the session.
//
// Open starts the process; Propose runs a turn on it; Interrupt asks Claude
// Code to end the running turn at its next safepoint; Close ends the input,
// waits for the process to exit and joins every goroutine the session
// started. Turns never overlap: Propose waits for the previous turn.
type ClaudeCodeSession struct {
	brain  *ClaudeCodeBrain
	stdin  *io.PipeWriter
	kill   context.CancelCauseFunc
	stream *mailbox.Mailbox[streamState]
	// state serializes turns and Close, like the brain's: one slot, held by
	// the one goroutine running a turn.
	state chan sessionState
	// exited is closed once the process has been reaped and its result
	// recorded; the recording happens before the close, so readers of result
	// and runErr wait on it.
	exited chan struct{}
	result proc.Result
	runErr error
}

var _ model.IBrain[*Turn, Event] = (*ClaudeCodeSession)(nil)

// streamState is the stream owner's state: the turn being read, if any, and
// where its result goes. It lives on the mailbox goroutine alone.
type streamState struct {
	session uuid.UUID
	logger  *slog.Logger
	report  *model.TurnReport
	reply   chan<- turnResult
	events  []Event
	failure error
	lines   int
	// idle reports events Claude Code writes between turns, under turn 0.
	idle *model.TurnReport
	// notify receives the task notifications read between turns; started is
	// when each task still running was seen to start, by now.
	notify  func(notification TaskNotification)
	started map[string]time.Time
	now     func() time.Time
}

// turnResult is what the stream owner hands the goroutine waiting for a turn.
type turnResult struct {
	events []Event
	err    error
}

// Open starts Claude Code on the brain's session and keeps it open. The
// process is bounded by the session's own lifetime, not by ctx: ctx only
// carries the trace and bounds the start itself.
func (brain *ClaudeCodeBrain) Open(ctx context.Context) (*ClaudeCodeSession, error) {
	var state sessionState
	select {
	case state = <-brain.state:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	brain.state <- state
	idle, err := model.NewTurnReport(ctx, brain.logger, ProviderName, brain.session, 0)
	if err != nil {
		return nil, fmt.Errorf("claudecode session: session trace: %w", err)
	}
	reader, writer := io.Pipe()
	processContext, kill := context.WithCancelCause(context.WithoutCancel(ctx))
	open := &ClaudeCodeSession{
		brain:  brain,
		stdin:  writer,
		kill:   kill,
		stream: mailbox.New[streamState](),
		state:  make(chan sessionState, 1),
		exited: make(chan struct{}),
	}
	open.state <- state
	// The stream owner: every output line, every turn boundary and the exit
	// pass through it, one at a time.
	go open.stream.Run(&streamState{session: brain.session, logger: brain.logger, idle: idle,
		notify: brain.notifyTasks, started: map[string]time.Time{}, now: brain.now})
	// The process: Run returns once Claude Code has exited and its output has
	// been copied, which is after the input was closed or the process killed.
	go func() {
		defer close(open.exited)
		result, runErr := brain.launcher.Run(processContext, proc.Command{
			Executable:       brain.processExecutable(),
			Arguments:        brain.processArguments(state),
			Directory:        brain.directory,
			ExtraEnvironment: brain.environment,
			Stdin:            reader,
			Stdout:           &lineWriter{stream: open.stream},
		})
		_ = reader.CloseWithError(io.EOF)
		open.result, open.runErr = result, runErr
		// The exit command retires the stream owner; exited closes only once
		// it has stopped, so every record it writes precedes a reader's
		// return from Close.
		if open.stream.Submit(func(state *streamState) bool {
			state.exit(result, runErr)
			return true
		}) {
			<-open.stream.Stopped()
		}
	}()
	return open, nil
}

// Session is the session identifier Claude Code was opened on.
func (open *ClaudeCodeSession) Session() uuid.UUID { return open.brain.session }

// Propose runs one turn on the open process: it writes the turn's messages and
// returns every event Claude Code reported until the result event, unchanged
// and in order. A turn the process ends before its result, or that ctx
// abandons, returns a [*TurnError] carrying the events seen so far.
func (open *ClaudeCodeSession) Propose(ctx context.Context, turn *Turn) (*model.Proposal[Event], error) {
	input, err := encodeTurn(turn)
	if err != nil {
		return nil, err
	}
	var state sessionState
	select {
	case state = <-open.state:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	state.turns++
	events, err := open.runTurn(ctx, state.turns, turn, input)
	open.state <- state
	if err != nil {
		return nil, &TurnError{Turn: state.turns, Events: events, Err: err}
	}
	return &model.Proposal[Event]{Provider: ProviderName, Actions: events}, nil
}

func (open *ClaudeCodeSession) runTurn(ctx context.Context, turn int, messages *Turn, input []byte) ([]Event, error) {
	report, err := model.NewTurnReport(ctx, open.brain.logger, ProviderName, open.brain.session, turn)
	if err != nil {
		return nil, err
	}
	reply := make(chan turnResult, 1)
	if !open.stream.Submit(func(state *streamState) bool { state.begin(report, reply); return false }) {
		return nil, ErrSessionClosed
	}
	report.Started(open.brain.executable, turn > 1, len(messages.Messages))
	for _, message := range messages.Messages {
		report.Event(model.DirectionIn, eventType(message), message)
	}
	if _, err := open.stdin.Write(input); err != nil {
		outcome := open.abandon(reply)
		report.Finished(len(outcome.events), ErrSessionClosed)
		return outcome.events, fmt.Errorf("%w: %w", ErrSessionClosed, err)
	}
	var outcome turnResult
	select {
	case outcome = <-reply:
	case <-ctx.Done():
		outcome = open.abandon(reply)
		outcome.err = fmt.Errorf("%w: %w", ErrTurnAbandoned, context.Cause(ctx))
	}
	report.Finished(len(outcome.events), outcome.err)
	return outcome.events, outcome.err
}

// abandon takes the turn back from the stream owner and returns what it had
// read; when the owner already answered, that answer is returned instead.
func (open *ClaudeCodeSession) abandon(reply chan turnResult) turnResult {
	if !open.stream.Submit(func(state *streamState) bool { state.abandon(); return false }) {
		// The stream stopped: its exit answered every open turn.
		return <-reply
	}
	return <-reply
}

// Interrupt asks Claude Code to end the running turn at its next safepoint, a
// tool boundary; the turn then reports its result like any other. It is the
// cancel a harness session delivers while a turn is in flight.
func (open *ClaudeCodeSession) Interrupt(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	request, err := json.Marshal(controlRequest{
		Type:      messageTypeControlRequest,
		RequestID: uuid.New().String(),
		Request:   controlBody{Subtype: controlSubtypeInterrupt},
	})
	if err != nil {
		return fmt.Errorf("claudecode session: encode interrupt: %w", err)
	}
	// Reported under the running turn, or under turn 0 between turns, so
	// the log shows where the cancel reached the executor.
	if !open.stream.Submit(func(state *streamState) bool {
		state.reportFor().Event(model.DirectionIn, messageTypeControlRequest, request)
		return false
	}) {
		return ErrSessionClosed
	}
	if _, err := open.stdin.Write(append(request, '\n')); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionClosed, err)
	}
	return nil
}

type controlRequest struct {
	Type      string      `json:"type"`
	RequestID string      `json:"request_id"`
	Request   controlBody `json:"request"`
}

type controlBody struct {
	Subtype string `json:"subtype"`
}

// Inject writes a message to the running turn's input at the next safepoint.
// The message is formatted as a stream-json user event and written to the
// process's stdin. An error is returned if the session is closed or the write
// fails.
func (open *ClaudeCodeSession) Inject(ctx context.Context, message string) error {
	if message == "" {
		return fmt.Errorf("claudecode session: inject requires a non-empty message")
	}
	userMessage := map[string]interface{}{
		"type": "user",
		"message": map[string]string{
			"role":    "user",
			"content": message,
		},
	}
	encoded, err := json.Marshal(userMessage)
	if err != nil {
		return fmt.Errorf("claudecode session: encode inject message: %w", err)
	}
	if !open.stream.Submit(func(state *streamState) bool {
		state.reportFor().Event(model.DirectionIn, "user", encoded)
		return false
	}) {
		return ErrSessionClosed
	}
	if _, err := open.stdin.Write(append(encoded, '\n')); err != nil {
		return fmt.Errorf("%w: %w", ErrSessionClosed, err)
	}
	return nil
}

// Close ends the input, which makes Claude Code exit once its current turn is
// done, and waits for the process. When ctx ends first the process group is
// killed and the wait completes on that. Close joins every goroutine the
// session started; it is idempotent.
func (open *ClaudeCodeSession) Close(ctx context.Context) error {
	var state sessionState
	select {
	case state = <-open.state:
	case <-ctx.Done():
		open.kill(context.Cause(ctx))
		<-open.exited
		return open.exitError()
	}
	defer func() { open.state <- state }()
	_ = open.stdin.Close()
	select {
	case <-open.exited:
	case <-ctx.Done():
		open.kill(context.Cause(ctx))
		<-open.exited
	}
	return open.exitError()
}

// Exited is closed once the process has exited, however it ended.
func (open *ClaudeCodeSession) Exited() <-chan struct{} { return open.exited }

// exitError is how the process ended, read after exited closed.
func (open *ClaudeCodeSession) exitError() error {
	if open.runErr == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrSessionClosed, open.runErr)
}

// lineWriter is the process's standard output: the copying goroutine writes
// chunks, and every complete line becomes one stream owner command.
type lineWriter struct {
	stream  *mailbox.Mailbox[streamState]
	pending []byte
}

func (writer *lineWriter) Write(chunk []byte) (int, error) {
	writer.pending = append(writer.pending, chunk...)
	for {
		end := bytes.IndexByte(writer.pending, '\n')
		if end < 0 {
			return len(chunk), nil
		}
		line := bytes.Clone(writer.pending[:end])
		writer.pending = writer.pending[end+1:]
		// A stopped stream has already answered its turn; later output is
		// dropped rather than blocking the process's exit.
		_ = writer.stream.Submit(func(state *streamState) bool { state.line(line); return false })
	}
}

// begin starts reading a turn: its events go to reply at the result.
func (state *streamState) begin(report *model.TurnReport, reply chan<- turnResult) {
	if state.reply != nil {
		// Turns are serialized by the session's state slot, so an open reply
		// here is a turn the previous caller abandoned; answer it so nothing
		// waits forever.
		state.reply <- turnResult{events: state.events, err: ErrTurnAbandoned}
	}
	state.report, state.reply, state.events, state.failure = report, reply, nil, nil
}

// abandon returns the turn's events so far to its waiter and stops reading
// for it; later lines are reported under turn 0.
func (state *streamState) abandon() {
	if state.reply == nil {
		return
	}
	state.reply <- turnResult{events: state.events, err: ErrTurnAbandoned}
	state.report, state.reply, state.events, state.failure = nil, nil, nil, nil
}

// reportFor is the running turn's report, or the idle report between turns.
func (state *streamState) reportFor() *model.TurnReport {
	if state.report == nil {
		return state.idle
	}
	return state.report
}

// line reads one output line: it is reported under the running turn, or under
// turn 0 between turns, and the result event ends the turn.
func (state *streamState) line(text []byte) {
	state.lines++
	trimmed := bytes.TrimSpace(text)
	if len(trimmed) == 0 {
		return
	}
	report := state.reportFor()
	event, header, err := parseEvent(trimmed)
	if err != nil {
		report.Malformed(state.lines, len(trimmed), err)
		if state.failure == nil {
			state.failure = &MalformedEventError{Line: state.lines, Err: err}
		}
		return
	}
	report.Event(model.DirectionOut, event.Type, event.Raw)
	state.task(header, event.Raw)
	if state.reply == nil {
		return
	}
	state.events = append(state.events, event)
	switch {
	case header.Type == EventTypeSystem && header.Subtype == subtypeInit && header.SessionID != state.session.String():
		state.failure = fmt.Errorf("%w: %q, not %s", ErrSessionMismatch, header.SessionID, state.session)
	case header.Type == EventTypeResult:
		state.reply <- turnResult{events: state.events, err: state.failure}
		state.report, state.reply, state.events, state.failure = nil, nil, nil, nil
	}
}

// exit records the process's end: a turn still being read did not complete.
func (state *streamState) exit(result proc.Result, runErr error) {
	state.idle.Diagnostics(result.Stderr)
	if state.reply == nil {
		return
	}
	err := runErr
	if err == nil {
		err = ErrTurnIncomplete
	}
	state.reply <- turnResult{events: state.events, err: err}
	state.report, state.reply, state.events = nil, nil, nil
}
