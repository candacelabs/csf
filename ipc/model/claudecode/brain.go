// Copyright 2026 Candace Labs

package claudecode

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/candacelabs/csf/ipc/model"
	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/pkg/telemetry"
)

// DefaultExecutable is the Claude Code command line, resolved on PATH.
const DefaultExecutable = "claude"

// The Claude Code flags this brain owns. A caller's extra arguments may not
// repeat them: the session, the print mode and the stream framing are the
// brain's to choose, because the brain's own reading of the output depends on
// them.
const (
	flagPrint        = "-p"
	flagPrintLong    = "--print"
	flagInputFormat  = "--input-format"
	flagOutputFormat = "--output-format"
	flagVerbose      = "--verbose"
	flagSessionID    = "--session-id"
	flagResume       = "--resume"
	flagResumeShort  = "-r"
	flagContinue     = "--continue"
	flagContinueAbbr = "-c"
	formatStreamJSON = "stream-json"
)

// Stream-json event types and subtypes the brain reads. It reads them only to
// report and to know when a turn is done; every event is forwarded unchanged.
const (
	// EventTypeSystem is a Claude Code system event; its init subtype opens a
	// run and names the session.
	EventTypeSystem = "system"
	// EventTypeResult is the event that ends a turn.
	EventTypeResult = "result"
	// EventTypeMalformed names, in the report only, an output line that was
	// not a stream-json event. No [Event] ever carries it.
	EventTypeMalformed = "malformed"
	subtypeInit        = "init"
)

var (
	// ErrNoLauncher reports a brain constructed without the process
	// capability.
	ErrNoLauncher = errors.New("claudecode brain: a process launcher is required")
	// ErrInvalidOption reports a nil option or an option value the brain
	// cannot use.
	ErrInvalidOption = errors.New("claudecode brain: invalid option")
	// ErrNoTurn reports a nil turn or a turn with no input message.
	ErrNoTurn = errors.New("claudecode brain: a turn needs at least one input message")
	// ErrMalformedInput reports an input message that is not a JSON object.
	ErrMalformedInput = errors.New("claudecode brain: input message is not a JSON object")
	// ErrMalformedEvent reports an output line that is not a stream-json
	// event.
	ErrMalformedEvent = errors.New("claudecode brain: Claude Code wrote a line that is not a stream-json event")
	// ErrTurnIncomplete reports a Claude Code process that exited without the
	// result event that ends a turn.
	ErrTurnIncomplete = errors.New("claudecode brain: Claude Code exited before reporting the turn's result")
	// ErrSessionMismatch reports a Claude Code run that opened a session other
	// than the one CSF gave it.
	ErrSessionMismatch = errors.New("claudecode brain: Claude Code opened a different session")
)

// Turn is the context the Claude Code brain decides on: the stream-json input
// messages of one turn, each a JSON object such as
// {"type":"user","message":{"role":"user","content":"..."}}. They are
// written to Claude Code unchanged, one per line.
type Turn struct {
	Messages []json.RawMessage
}

// Event is one stream-json event Claude Code reported during a turn. Raw is
// the line exactly as Claude Code wrote it; Type is its "type" field, read for
// the report.
type Event struct {
	Type string
	Raw  json.RawMessage
}

// TurnError reports a turn that did not complete. Events holds every event
// Claude Code reported before the failure, unchanged, so nothing it said is
// lost with the error.
type TurnError struct {
	Turn   int
	Events []Event
	Err    error
}

func (failure *TurnError) Error() string {
	return fmt.Sprintf("claudecode brain: turn %d: %v", failure.Turn, failure.Err)
}

func (failure *TurnError) Unwrap() error { return failure.Err }

// MalformedEventError locates the first output line that was not a
// stream-json event. It wraps [ErrMalformedEvent].
type MalformedEventError struct {
	// Line is the 1-based output line number.
	Line int
	Err  error
}

func (failure *MalformedEventError) Error() string {
	return fmt.Sprintf("%v: line %d: %v", ErrMalformedEvent, failure.Line, failure.Err)
}

func (failure *MalformedEventError) Unwrap() []error { return []error{ErrMalformedEvent, failure.Err} }

// ClaudeCodeBrain drives Claude Code as a turn executor for one session CSF
// owns. Each Propose is one turn: one Claude Code process, launched through
// the granted process capability, fed the turn's messages and read to its
// result event. Inputs and events pass through unchanged; every one is
// reported to the brain's logger with the turn's trace.
//
// Turns of one session never overlap: Propose waits for the previous turn of
// the same brain to finish, and a context canceled while waiting abandons the
// wait.
type ClaudeCodeBrain struct {
	launcher   proc.ILauncher
	session    uuid.UUID
	executable string
	directory  string
	arguments  []string
	// environment is appended to the environment every Claude Code process
	// inherits: the Bazel cache locations a session builds with, for one.
	environment []string
	logger      *slog.Logger
	// state holds the session's turn state between turns. It has one slot:
	// whoever has received the state is the one goroutine running a turn,
	// and sends it back when the turn ends.
	state chan sessionState
}

// sessionState is what one turn hands to the next.
type sessionState struct {
	turns int
	// established reports that Claude Code has already opened the session,
	// so the next run resumes it instead of creating it.
	established bool
}

var _ model.IBrain[*Turn, Event] = (*ClaudeCodeBrain)(nil)

// ClaudeCodeBrainOption configures a [ClaudeCodeBrain].
type ClaudeCodeBrainOption func(brain *ClaudeCodeBrain) error

// WithExecutable runs Claude Code from executable, a path or a name on PATH,
// instead of [DefaultExecutable].
func WithExecutable(executable string) ClaudeCodeBrainOption {
	return func(brain *ClaudeCodeBrain) error {
		if executable == "" {
			return fmt.Errorf("%w: empty executable", ErrInvalidOption)
		}
		brain.executable = executable
		return nil
	}
}

// WithDirectory runs Claude Code in directory instead of the caller's working
// directory.
func WithDirectory(directory string) ClaudeCodeBrainOption {
	return func(brain *ClaudeCodeBrain) error {
		brain.directory = directory
		return nil
	}
}

// WithArguments appends arguments to every Claude Code invocation, after the
// flags the brain owns. They pass through unchanged; the brain's own flags
// (print mode, stream formats, verbose, session selection) may not be repeated.
func WithArguments(arguments ...string) ClaudeCodeBrainOption {
	return func(brain *ClaudeCodeBrain) error {
		for _, argument := range arguments {
			if slices.Contains(ownedFlags, argument) {
				return fmt.Errorf("%w: argument %s is owned by the brain", ErrInvalidOption, argument)
			}
		}
		brain.arguments = append(brain.arguments, arguments...)
		return nil
	}
}

// WithEnvironment appends variables, as NAME=value, to the environment every
// Claude Code process inherits.
func WithEnvironment(variables ...string) ClaudeCodeBrainOption {
	return func(brain *ClaudeCodeBrain) error {
		for _, variable := range variables {
			name, _, found := strings.Cut(variable, "=")
			if !found || name == "" {
				return fmt.Errorf("%w: environment variable %q is not NAME=value", ErrInvalidOption, variable)
			}
		}
		brain.environment = append(brain.environment, variables...)
		return nil
	}
}

// WithLogger reports every turn and event to logger instead of slog.Default().
func WithLogger(logger *slog.Logger) ClaudeCodeBrainOption {
	return func(brain *ClaudeCodeBrain) error {
		if logger == nil {
			return fmt.Errorf("%w: nil logger", ErrInvalidOption)
		}
		brain.logger = logger
		return nil
	}
}

// WithResumedSession declares that Claude Code already holds the session, so
// the first turn resumes it rather than creating it.
func WithResumedSession() ClaudeCodeBrainOption {
	return func(brain *ClaudeCodeBrain) error {
		state := <-brain.state
		state.established = true
		brain.state <- state
		return nil
	}
}

// ownedFlags are the flags a caller's extra arguments may not carry.
var ownedFlags = []string{
	flagPrint, flagPrintLong, flagInputFormat, flagOutputFormat, flagVerbose,
	flagSessionID, flagResume, flagResumeShort, flagContinue, flagContinueAbbr,
}

// NewClaudeCodeBrain builds the Claude Code turn executor for session. The
// launcher is the process capability every turn's Claude Code process is
// started through; the caller owns it.
func NewClaudeCodeBrain(launcher proc.ILauncher, session uuid.UUID, options ...ClaudeCodeBrainOption) (*ClaudeCodeBrain, error) {
	if launcher == nil {
		return nil, ErrNoLauncher
	}
	if session == uuid.Nil {
		return nil, ErrNoSession
	}
	brain := &ClaudeCodeBrain{
		launcher:   launcher,
		session:    session,
		executable: DefaultExecutable,
		logger:     slog.Default(),
		state:      make(chan sessionState, 1),
	}
	brain.state <- sessionState{}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(brain); err != nil {
			return nil, err
		}
	}
	return brain, nil
}

// Session is the session identifier CSF gave this brain.
func (brain *ClaudeCodeBrain) Session() uuid.UUID { return brain.session }

// Propose runs one turn: it starts Claude Code on the session, writes the
// turn's messages and returns every event Claude Code reported, unchanged and
// in order, ending with the result event. A turn that does not reach its
// result returns a [*TurnError] carrying the events seen before the failure.
func (brain *ClaudeCodeBrain) Propose(ctx context.Context, turn *Turn) (*model.Proposal[Event], error) {
	input, err := encodeTurn(turn)
	if err != nil {
		return nil, err
	}
	var state sessionState
	select {
	case state = <-brain.state:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	state.turns++
	events, established, err := brain.runTurn(ctx, state, turn, input)
	state.established = state.established || established
	brain.state <- state
	if err != nil {
		return nil, &TurnError{Turn: state.turns, Events: events, Err: err}
	}
	return &model.Proposal[Event]{Provider: ProviderName, Actions: events}, nil
}

// encodeTurn frames the turn's messages as stream-json input: one compact
// JSON object per line. Compacting removes only insignificant whitespace,
// which the line framing requires.
func encodeTurn(turn *Turn) ([]byte, error) {
	if turn == nil || len(turn.Messages) == 0 {
		return nil, ErrNoTurn
	}
	var input bytes.Buffer
	for index, message := range turn.Messages {
		trimmed := bytes.TrimSpace(message)
		if len(trimmed) == 0 || trimmed[0] != '{' || !json.Valid(trimmed) {
			return nil, fmt.Errorf("%w: message %d", ErrMalformedInput, index)
		}
		if err := json.Compact(&input, trimmed); err != nil {
			return nil, fmt.Errorf("%w: message %d: %w", ErrMalformedInput, index, err)
		}
		input.WriteByte('\n')
	}
	return input.Bytes(), nil
}

// runTurn launches one Claude Code process for the turn and reads it to the
// end. It reports whether Claude Code opened the session, so the next turn
// resumes it even when this one failed afterwards.
func (brain *ClaudeCodeBrain) runTurn(ctx context.Context, state sessionState, turn *Turn, input []byte) ([]Event, bool, error) {
	report, err := newTurnReport(ctx, brain.logger, brain.session, state.turns)
	if err != nil {
		return nil, false, err
	}
	report.started(brain.executable, state.established, len(turn.Messages))
	for _, message := range turn.Messages {
		report.event(directionIn, eventType(message), message)
	}
	stream := newEventStream(brain.session, report)
	result, runErr := brain.launcher.Run(ctx, proc.Command{
		Executable:       brain.executable,
		Arguments:        brain.argumentsFor(state),
		Directory:        brain.directory,
		ExtraEnvironment: brain.environment,
		Stdin:            bytes.NewReader(input),
		Stdout:           stream,
	})
	stream.finish()
	report.diagnostics(result.Stderr)
	err = turnOutcome(runErr, stream)
	report.finished(len(stream.events), err)
	return stream.events, stream.established, err
}

// turnOutcome decides whether a finished run completed its turn.
func turnOutcome(runErr error, stream *eventStream) error {
	switch {
	case runErr != nil:
		return runErr
	case stream.failure != nil:
		return stream.failure
	case !stream.resulted:
		return ErrTurnIncomplete
	}
	return nil
}

// argumentsFor is the argument vector for one turn: the owned flags, the
// session selection, then the caller's extra arguments.
func (brain *ClaudeCodeBrain) argumentsFor(state sessionState) []string {
	selection := flagSessionID
	if state.established {
		selection = flagResume
	}
	arguments := []string{
		flagPrint,
		flagInputFormat, formatStreamJSON,
		flagOutputFormat, formatStreamJSON,
		flagVerbose,
		selection, brain.session.String(),
	}
	return append(arguments, brain.arguments...)
}

// eventHeader is the part of a stream-json event the brain reads.
type eventHeader struct {
	Type      string `json:"type"`
	Subtype   string `json:"subtype"`
	SessionID string `json:"session_id"`
}

// eventType is a message's "type" field, or empty when it has none.
func eventType(message json.RawMessage) string {
	var header eventHeader
	if json.Unmarshal(message, &header) != nil {
		return ""
	}
	return header.Type
}

// eventStream is Claude Code's standard output. The process gateway's copying
// goroutine is its only writer, and Run joins that goroutine before it
// returns, so the brain reads the stream's fields after Run without a lock.
type eventStream struct {
	session     uuid.UUID
	report      *turnReport
	pending     []byte
	lines       int
	events      []Event
	resulted    bool
	established bool
	failure     error
}

func newEventStream(session uuid.UUID, report *turnReport) *eventStream {
	return &eventStream{session: session, report: report}
}

// Write splits chunk into lines and reads each complete one. It never fails:
// an error would stop the copy and leave Claude Code blocked on a full pipe,
// so a malformed line is recorded and reading continues.
func (stream *eventStream) Write(chunk []byte) (int, error) {
	stream.pending = append(stream.pending, chunk...)
	for {
		end := bytes.IndexByte(stream.pending, '\n')
		if end < 0 {
			return len(chunk), nil
		}
		stream.line(stream.pending[:end])
		stream.pending = stream.pending[end+1:]
	}
}

// finish reads a last line Claude Code wrote without a newline.
func (stream *eventStream) finish() {
	if len(stream.pending) > 0 {
		stream.line(stream.pending)
		stream.pending = nil
	}
}

func (stream *eventStream) line(text []byte) {
	stream.lines++
	trimmed := bytes.TrimSpace(text)
	if len(trimmed) == 0 {
		return
	}
	event, header, err := parseEvent(trimmed)
	if err != nil {
		stream.fail(&MalformedEventError{Line: stream.lines, Err: err})
		stream.report.malformed(stream.lines, len(trimmed), err)
		return
	}
	stream.events = append(stream.events, event)
	stream.report.event(directionOut, event.Type, event.Raw)
	switch {
	case header.Type == EventTypeResult:
		stream.resulted = true
	case header.Type == EventTypeSystem && header.Subtype == subtypeInit:
		stream.opened(header.SessionID)
	}
}

// parseEvent reads one trimmed output line as a stream-json event.
func parseEvent(trimmed []byte) (Event, eventHeader, error) {
	var header eventHeader
	err := json.Unmarshal(trimmed, &header)
	if err == nil && header.Type == "" {
		err = errors.New("event has no type")
	}
	if err != nil {
		return Event{}, header, err
	}
	return Event{Type: header.Type, Raw: bytes.Clone(trimmed)}, header, nil
}

// opened records the session Claude Code's init event names.
func (stream *eventStream) opened(session string) {
	if session != stream.session.String() {
		stream.fail(fmt.Errorf("%w: %q, not %s", ErrSessionMismatch, session, stream.session))
		return
	}
	stream.established = true
}

// fail keeps the first failure; later lines are still read and reported.
func (stream *eventStream) fail(err error) {
	if stream.failure == nil {
		stream.failure = err
	}
}

// Report vocabulary: the slog message and attribute keys of a turn report.
const (
	reportTurnStarted  = "turn executor turn started"
	reportTurnFinished = "turn executor turn finished"
	reportEvent        = "turn executor event"
	reportMalformed    = "turn executor malformed event"
	reportDiagnostics  = "turn executor diagnostics"

	keyTraceID      = "trace_id"
	keySpanID       = "span_id"
	keyParentSpanID = "parent_span_id"
	keyProvider     = "provider"
	keySessionID    = "session_id"
	keyTurn         = "turn"
	keySequence     = "sequence"
	keyDirection    = "direction"
	keyEventType    = "event_type"
	keyEvent        = "event"
	keyElapsed      = "elapsed_ms"
	keyDuration     = "duration_ms"
	keyExecutable   = "executable"
	keyResume       = "resume"
	keyInputs       = "inputs"
	keyEvents       = "events"
	keyLine         = "line"
	keyBytes        = "bytes"
	keyError        = "error"
	keyStderr       = "stderr"

	directionIn  = "in"
	directionOut = "out"
)

// turnReport writes one turn's structured log: a trace per turn, a span per
// event. When the caller's context already carries a trace, the turn is a
// child span of it; otherwise the turn starts a new W3C trace.
type turnReport struct {
	ctx      context.Context
	logger   *slog.Logger
	session  uuid.UUID
	turn     int
	traceID  string
	spanID   string
	begun    time.Time
	sequence int
}

func newTurnReport(ctx context.Context, logger *slog.Logger, session uuid.UUID, turn int) (*turnReport, error) {
	turnCtx, err := turnTrace(ctx)
	if err != nil {
		return nil, fmt.Errorf("claudecode brain: turn trace: %w", err)
	}
	trace, _ := telemetry.TraceFromContext(turnCtx)
	return &turnReport{
		ctx:     turnCtx,
		logger:  logger,
		session: session,
		turn:    turn,
		traceID: trace.GetTraceId(),
		spanID:  trace.GetSpanId(),
		begun:   time.Now(),
	}, nil
}

// turnTrace is ctx carrying the turn's span.
func turnTrace(ctx context.Context) (context.Context, error) {
	if _, ok := telemetry.TraceFromContext(ctx); ok {
		turnCtx, _, err := telemetry.ContextWithChildSpan(ctx)
		return turnCtx, err
	}
	trace, err := telemetry.NewTraceContext(telemetry.TraceFlagsSampled)
	if err != nil {
		return nil, err
	}
	return telemetry.ContextWithTrace(ctx, trace)
}

func (report *turnReport) attributes(extra ...slog.Attr) []slog.Attr {
	return append([]slog.Attr{
		slog.String(keyTraceID, report.traceID),
		slog.String(keySessionID, report.session.String()),
		slog.Int(keyTurn, report.turn),
		slog.String(keyProvider, ProviderName),
	}, extra...)
}

func (report *turnReport) started(executable string, resume bool, inputs int) {
	report.logger.LogAttrs(report.ctx, slog.LevelInfo, reportTurnStarted, report.attributes(
		slog.String(keySpanID, report.spanID),
		slog.String(keyExecutable, executable),
		slog.Bool(keyResume, resume),
		slog.Int(keyInputs, inputs),
	)...)
}

// event reports one event in or out under a span of its own.
func (report *turnReport) event(direction string, kind string, raw json.RawMessage) {
	report.sequence++
	report.logger.LogAttrs(report.ctx, slog.LevelInfo, reportEvent, report.attributes(
		slog.String(keySpanID, report.eventSpan()),
		slog.String(keyParentSpanID, report.spanID),
		slog.Int(keySequence, report.sequence),
		slog.String(keyDirection, direction),
		slog.String(keyEventType, kind),
		report.since(keyElapsed),
		slog.Any(keyEvent, raw),
	)...)
}

// malformed reports an output line that was not an event; the line itself is
// not logged, only where it was and how long.
func (report *turnReport) malformed(line int, size int, err error) {
	report.sequence++
	report.logger.LogAttrs(report.ctx, slog.LevelWarn, reportMalformed, report.attributes(
		slog.String(keySpanID, report.eventSpan()),
		slog.String(keyParentSpanID, report.spanID),
		slog.Int(keySequence, report.sequence),
		slog.String(keyDirection, directionOut),
		slog.String(keyEventType, EventTypeMalformed),
		report.since(keyElapsed),
		slog.Int(keyLine, line),
		slog.Int(keyBytes, size),
		slog.String(keyError, err.Error()),
	)...)
}

// since is the time since the turn began, in milliseconds.
func (report *turnReport) since(key string) slog.Attr {
	return slog.Float64(key, float64(time.Since(report.begun).Microseconds())/1000)
}

// eventSpan is a new child span of the turn, or empty if none can be derived.
func (report *turnReport) eventSpan() string {
	_, span, err := telemetry.ContextWithChildSpan(report.ctx)
	if err != nil {
		return ""
	}
	return span.GetSpanId()
}

// diagnostics reports what Claude Code wrote to standard error, if anything.
func (report *turnReport) diagnostics(stderr []byte) {
	text := bytes.TrimSpace(stderr)
	if len(text) == 0 {
		return
	}
	report.logger.LogAttrs(report.ctx, slog.LevelWarn, reportDiagnostics, report.attributes(
		slog.String(keySpanID, report.spanID),
		slog.String(keyStderr, string(text)),
	)...)
}

func (report *turnReport) finished(events int, err error) {
	level := slog.LevelInfo
	extra := []slog.Attr{
		slog.String(keySpanID, report.spanID),
		report.since(keyDuration),
		slog.Int(keyEvents, events),
	}
	if err != nil {
		level = slog.LevelError
		extra = append(extra, slog.String(keyError, err.Error()))
	}
	report.logger.LogAttrs(report.ctx, level, reportTurnFinished, report.attributes(extra...)...)
}
