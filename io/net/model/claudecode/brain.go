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

	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/ipc/proc"
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
	EventTypeMalformed = model.EventTypeMalformed
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
	// launchPrefix, when set, wraps every Claude Code process: the process
	// started is launchPrefix[0] with launchPrefix[1:] ahead of the Claude Code
	// executable and its arguments. The harness uses it to place the session
	// sandbox launcher in front of the executor, started through the same proc
	// capability.
	launchPrefix []string
	// environment is appended to the environment every Claude Code process
	// inherits: the Bazel cache locations a session builds with, for one.
	environment []string
	logger      *slog.Logger
	// notifyTasks, when set, receives the task notifications an open session
	// reads between turns.
	notifyTasks func(notification TaskNotification)
	// now is the clock a task's elapsed time is read from.
	now func() time.Time
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

// WithLaunchPrefix wraps every Claude Code process in a launcher. The process
// the brain starts becomes prefix[0], its arguments become prefix[1:] followed
// by the Claude Code executable and the brain's own arguments. The harness uses
// it to place the per-session sandbox launcher in front of the turn executor,
// so the executor and everything it forks start confined.
func WithLaunchPrefix(prefix ...string) ClaudeCodeBrainOption {
	return func(brain *ClaudeCodeBrain) error {
		if len(prefix) == 0 || prefix[0] == "" {
			return fmt.Errorf("%w: empty launch prefix", ErrInvalidOption)
		}
		brain.launchPrefix = append([]string{}, prefix...)
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

// WithClock replaces the clock a task's elapsed time is read from.
func WithClock(now func() time.Time) ClaudeCodeBrainOption {
	return func(brain *ClaudeCodeBrain) error {
		if now == nil {
			return fmt.Errorf("%w: nil clock", ErrInvalidOption)
		}
		brain.now = now
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
		now:        time.Now,
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

// Inject is not supported on a one-shot brain; use an open session instead.
func (brain *ClaudeCodeBrain) Inject(ctx context.Context, message string) error {
	return fmt.Errorf("claudecode brain: inject is only available on open sessions; use brain.Open()")
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
	report, err := model.NewTurnReport(ctx, brain.logger, ProviderName, brain.session, state.turns)
	if err != nil {
		return nil, false, err
	}
	report.Started(brain.executable, state.established, len(turn.Messages))
	for _, message := range turn.Messages {
		report.Event(model.DirectionIn, eventType(message), message)
	}
	stream := newEventStream(brain.session, report)
	result, runErr := brain.launcher.Run(ctx, proc.Command{
		Executable:       brain.processExecutable(),
		Arguments:        brain.processArguments(state),
		Directory:        brain.directory,
		ExtraEnvironment: brain.environment,
		Stdin:            bytes.NewReader(input),
		Stdout:           stream,
	})
	stream.finish()
	report.Diagnostics(result.Stderr)
	err = turnOutcome(runErr, stream)
	report.Finished(len(stream.events), err)
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

// processExecutable is the program the proc capability starts: the launch
// prefix's head when a prefix is set, otherwise Claude Code itself.
func (brain *ClaudeCodeBrain) processExecutable() string {
	if len(brain.launchPrefix) > 0 {
		return brain.launchPrefix[0]
	}
	return brain.executable
}

// processArguments is the full argument vector the process is started with:
// without a launch prefix, the Claude Code arguments; with one, the rest of the
// prefix, then the Claude Code executable and its arguments.
func (brain *ClaudeCodeBrain) processArguments(state sessionState) []string {
	claudeArguments := brain.argumentsFor(state)
	if len(brain.launchPrefix) == 0 {
		return claudeArguments
	}
	argv := append([]string{}, brain.launchPrefix[1:]...)
	argv = append(argv, brain.executable)
	return append(argv, claudeArguments...)
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
	report      *model.TurnReport
	pending     []byte
	lines       int
	events      []Event
	resulted    bool
	established bool
	failure     error
}

func newEventStream(session uuid.UUID, report *model.TurnReport) *eventStream {
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
		stream.report.Malformed(stream.lines, len(trimmed), err)
		return
	}
	stream.events = append(stream.events, event)
	stream.report.Event(model.DirectionOut, event.Type, event.Raw)
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
