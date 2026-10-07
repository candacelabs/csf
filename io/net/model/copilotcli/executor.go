// Copyright 2026 Candace Labs

// Package copilotcli is the GitHub Copilot CLI provider: [CopilotCLIExecutor]
// drives the Copilot command line as a turn executor for one session CSF
// owns. Each turn is one Copilot process, started through the ipc/proc
// capability on the session's identifier, which Copilot creates on the first
// turn and resumes with its history on every later one.
//
// Every non-ephemeral record Copilot writes is reported to a structured log
// under the turn's trace, in the record shape the Claude Code turn executor
// writes. The two records the harness reads back are reported in Claude
// Code's stream-json shape, with Copilot's own record kept under "copilot":
// an assistant message becomes an "assistant" event whose tool calls carry
// the Claude Code tool names, and the turn's result becomes a "result" event
// carrying the token and cache usage Copilot wrote to its usage file. So a
// run's log reads the same whichever executor ran the turn.
//
// It is unrelated to ipc/model/copilot, the Copilot Workbench client.
package copilotcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/google/uuid"

	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/ipc/proc"
)

// DefaultExecutable is the Copilot command line, resolved on PATH.
const DefaultExecutable = "copilot"

// PinnedVersion is the Copilot CLI version the harness's Copilot sessions
// were measured on: its flags, record shapes, hook payloads and usage file.
const PinnedVersion = "1.0.90"

// ProviderName names the Copilot CLI provider in turn reports and proposals.
const ProviderName = "copilotcli"

// The Copilot flags this executor owns: the prompt, the session, the output
// framing and the usage file are the executor's to choose, because its
// reading of the output depends on them. A caller's extra arguments may not
// repeat them. The prompt is written to standard input, so it is bounded by
// nothing but Copilot.
const (
	flagPrompt       = "-p"
	flagPromptLong   = "--prompt"
	flagSessionID    = "--session-id"
	flagResume       = "--resume"
	flagResumeShort  = "-r"
	flagContinue     = "--continue"
	flagOutputFormat = "--output-format"
	flagUsageFile    = "--usage-output-file"
	flagNoAutoUpdate = "--no-auto-update"
	formatJSON       = "json"
)

// Copilot record types the executor reads, and the Claude Code stream-json
// types and fields it reports them as.
const (
	recordAssistantMessage = "assistant.message"
	recordToolComplete     = "tool.execution_complete"
	recordResult           = "result"

	// EventTypeAssistant is an assistant message, in Claude Code's shape.
	EventTypeAssistant = "assistant"
	// EventTypeResult is the event that ends a turn.
	EventTypeResult = "result"
	// EventTypeUser names the prompt the executor was given, in the report
	// only, and a tool's result, both in Claude Code's stream-json user
	// message shape.
	EventTypeUser = "user"

	roleUser       = "user"
	roleAssistant  = "assistant"
	blockText      = "text"
	blockToolUse   = "tool_use"
	blockResult    = "tool_result"
	subtypeSuccess = "success"
	subtypeError   = "error_during_execution"
)

// argumentNames are Copilot's names for a file tool's arguments, and the
// names Claude Code gives the same arguments.
var argumentNames = map[string]string{
	"path":      "file_path",
	"old_str":   "old_string",
	"new_str":   "new_string",
	"file_text": "content",
}

var (
	// ErrNoLauncher reports an executor constructed without the process
	// capability.
	ErrNoLauncher = errors.New("copilotcli executor: a process launcher is required")
	// ErrNoSession reports an executor constructed without a session
	// identifier.
	ErrNoSession = errors.New("copilotcli executor: a nonzero session identifier is required")
	// ErrInvalidOption reports a nil option or an option value the executor
	// cannot use.
	ErrInvalidOption = errors.New("copilotcli executor: invalid option")
	// ErrNoPrompt reports a nil turn or a turn with an empty prompt.
	ErrNoPrompt = errors.New("copilotcli executor: a turn needs a prompt")
	// ErrMalformedEvent reports an output line that is not a Copilot JSON
	// record.
	ErrMalformedEvent = errors.New("copilotcli executor: Copilot wrote a line that is not a JSON record")
	// ErrTurnIncomplete reports a Copilot process that exited without the
	// result record that ends a turn.
	ErrTurnIncomplete = errors.New("copilotcli executor: Copilot exited before reporting the turn's result")
	// ErrTurnFailed reports a result record with a nonzero exit code.
	ErrTurnFailed = errors.New("copilotcli executor: Copilot reported a failed turn")
	// ErrSessionMismatch reports a result for another session than the one
	// the executor runs.
	ErrSessionMismatch = errors.New("copilotcli executor: Copilot reported another session")
	// ErrInterrupted reports a turn whose process Interrupt stopped.
	ErrInterrupted = errors.New("copilotcli executor: the turn was interrupted")
	// ErrClosed reports a turn or an interrupt on a closed executor, and a
	// turn Close stopped.
	ErrClosed = errors.New("copilotcli executor: the executor is closed")
)

// Turn is what one Copilot turn decides on: the prompt, passed to Copilot
// unchanged.
type Turn struct {
	Prompt string
}

// Event is one record the executor reported for a turn: Type is its type and
// Raw the record as reported.
type Event struct {
	Type string
	Raw  json.RawMessage
}

// TurnError reports a turn that did not complete. Events holds every record
// reported before the failure.
type TurnError struct {
	Turn   int
	Events []Event
	Err    error
}

func (failure *TurnError) Error() string {
	return fmt.Sprintf("copilotcli executor: turn %d: %v", failure.Turn, failure.Err)
}

func (failure *TurnError) Unwrap() error { return failure.Err }

// CopilotCLIExecutor runs the turns of one Copilot session, one process per
// turn. Turns never overlap: Propose waits for the previous turn to finish.
// Interrupt stops the running turn's process; Close stops it, waits for the
// turn to return and refuses every later one. The executor starts no
// goroutine of its own: each turn's process runs, and is reaped, inside the
// Propose that started it.
type CopilotCLIExecutor struct {
	launcher     proc.ILauncher
	session      uuid.UUID
	executable   string
	directory    string
	arguments    []string
	environment  []string
	launchPrefix []string
	usageFile    string
	logger       *slog.Logger
	// turns holds the turn count between turns. It has one slot: whoever has
	// received it is the one goroutine running a turn, and sends it back when
	// the turn ends.
	turns chan int
	// lifetime ends at Close; stop ends it.
	lifetime context.Context
	stop     context.CancelCauseFunc
	// running is the cancel of the turn in flight, or nil between turns.
	running atomic.Pointer[context.CancelCauseFunc]
}

var _ model.IBrain[*Turn, Event] = (*CopilotCLIExecutor)(nil)

// CopilotCLIExecutorOption configures a [CopilotCLIExecutor].
type CopilotCLIExecutorOption func(executor *CopilotCLIExecutor) error

// WithExecutable runs Copilot from executable, a path or a name on PATH,
// instead of [DefaultExecutable].
func WithExecutable(executable string) CopilotCLIExecutorOption {
	return func(executor *CopilotCLIExecutor) error {
		if executable == "" {
			return fmt.Errorf("%w: empty executable", ErrInvalidOption)
		}
		executor.executable = executable
		return nil
	}
}

// WithDirectory runs Copilot in directory instead of the caller's working
// directory.
func WithDirectory(directory string) CopilotCLIExecutorOption {
	return func(executor *CopilotCLIExecutor) error {
		executor.directory = directory
		return nil
	}
}

// WithArguments appends arguments to every Copilot invocation, after the
// flags the executor owns, which may not be repeated.
func WithArguments(arguments ...string) CopilotCLIExecutorOption {
	return func(executor *CopilotCLIExecutor) error {
		for _, argument := range arguments {
			if slices.Contains(ownedFlags, argument) {
				return fmt.Errorf("%w: argument %s is owned by the executor", ErrInvalidOption, argument)
			}
		}
		executor.arguments = append(executor.arguments, arguments...)
		return nil
	}
}

// WithEnvironment appends variables, as NAME=value, to the environment every
// Copilot process inherits.
func WithEnvironment(variables ...string) CopilotCLIExecutorOption {
	return func(executor *CopilotCLIExecutor) error {
		for _, variable := range variables {
			name, _, found := strings.Cut(variable, "=")
			if !found || name == "" {
				return fmt.Errorf("%w: environment variable %q is not NAME=value", ErrInvalidOption, variable)
			}
		}
		executor.environment = append(executor.environment, variables...)
		return nil
	}
}

// WithLaunchPrefix wraps every Copilot process: the process started is
// prefix[0], with prefix[1:] ahead of the Copilot executable and its
// arguments. The session sandbox puts its launcher here.
func WithLaunchPrefix(prefix ...string) CopilotCLIExecutorOption {
	return func(executor *CopilotCLIExecutor) error {
		if len(prefix) == 0 || prefix[0] == "" {
			return fmt.Errorf("%w: empty launch prefix", ErrInvalidOption)
		}
		executor.launchPrefix = append([]string{}, prefix...)
		return nil
	}
}

// WithUsageFile has every turn write Copilot's usage statistics to path,
// replaced each turn, and report the tokens and cache usage it holds on the
// turn's result. Without it the result carries no token counts: Copilot's
// stream reports none.
func WithUsageFile(path string) CopilotCLIExecutorOption {
	return func(executor *CopilotCLIExecutor) error {
		if path == "" {
			return fmt.Errorf("%w: empty usage file", ErrInvalidOption)
		}
		executor.usageFile = path
		return nil
	}
}

// WithLogger reports every turn and record to logger instead of
// slog.Default().
func WithLogger(logger *slog.Logger) CopilotCLIExecutorOption {
	return func(executor *CopilotCLIExecutor) error {
		if logger == nil {
			return fmt.Errorf("%w: nil logger", ErrInvalidOption)
		}
		executor.logger = logger
		return nil
	}
}

// ownedFlags are the flags a caller's extra arguments may not carry.
var ownedFlags = []string{flagPrompt, flagPromptLong, flagSessionID, flagResume, flagResumeShort, flagContinue, flagOutputFormat, flagUsageFile}

// NewCopilotCLIExecutor builds the Copilot turn executor for session. The
// launcher is the process capability every turn's Copilot process is started
// through; the caller owns it.
func NewCopilotCLIExecutor(launcher proc.ILauncher, session uuid.UUID, options ...CopilotCLIExecutorOption) (*CopilotCLIExecutor, error) {
	if launcher == nil {
		return nil, ErrNoLauncher
	}
	if session == uuid.Nil {
		return nil, ErrNoSession
	}
	executor := &CopilotCLIExecutor{
		launcher:   launcher,
		session:    session,
		executable: DefaultExecutable,
		logger:     slog.Default(),
		turns:      make(chan int, 1),
	}
	for _, option := range options {
		if option == nil {
			return nil, fmt.Errorf("%w: nil option", ErrInvalidOption)
		}
		if err := option(executor); err != nil {
			return nil, err
		}
	}
	executor.turns <- 0
	executor.lifetime, executor.stop = context.WithCancelCause(context.Background())
	return executor, nil
}

// Session is the session identifier CSF gave this executor.
func (executor *CopilotCLIExecutor) Session() uuid.UUID { return executor.session }

// Propose runs one turn: it starts Copilot on the session with the prompt and
// returns every record reported for it, in order, ending with the result. ctx
// bounds the turn's process. A turn that does not reach a successful result
// returns a [*TurnError] carrying the records reported before the failure.
func (executor *CopilotCLIExecutor) Propose(ctx context.Context, turn *Turn) (*model.Proposal[Event], error) {
	if turn == nil || turn.Prompt == "" {
		return nil, ErrNoPrompt
	}
	var turns int
	select {
	case turns = <-executor.turns:
	case <-ctx.Done():
		return nil, context.Cause(ctx)
	}
	defer func() { executor.turns <- turns }()
	turnCtx, cancel := context.WithCancelCause(ctx)
	defer cancel(nil)
	// Published before the closed check, so a Close that ends the lifetime
	// after the check finds this turn's cancel and stops it.
	executor.running.Store(&cancel)
	defer executor.running.Store(nil)
	if executor.lifetime.Err() != nil {
		return nil, ErrClosed
	}
	turns++
	events, err := executor.runTurn(turnCtx, turns, turn.Prompt)
	if err != nil {
		return nil, &TurnError{Turn: turns, Events: events, Err: err}
	}
	return &model.Proposal[Event]{Provider: ProviderName, Actions: events}, nil
}

// runTurn launches one Copilot process for the turn and reads it to the end.
// The result is reported after the process exits, once its usage file is
// written.
func (executor *CopilotCLIExecutor) runTurn(ctx context.Context, turn int, prompt string) ([]Event, error) {
	report, err := model.NewTurnReport(ctx, executor.logger, ProviderName, executor.session, turn)
	if err != nil {
		return nil, fmt.Errorf("copilotcli executor: turn trace: %w", err)
	}
	input, err := userMessage(prompt)
	if err != nil {
		return nil, err
	}
	if executor.usageFile != "" {
		// A usage file a failed turn leaves unwritten must not report the
		// previous turn's usage as this one's.
		if err := os.Remove(executor.usageFile); err != nil && !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("copilotcli executor: clear usage file: %w", err)
		}
	}
	report.Started(executor.executable, turn > 1, 1)
	report.Event(model.DirectionIn, EventTypeUser, input)
	stream := &recordStream{report: report, session: executor.session}
	result, runErr := executor.launcher.Run(ctx, proc.Command{
		Executable:       executor.processExecutable(),
		Arguments:        executor.processArguments(),
		Directory:        executor.directory,
		ExtraEnvironment: executor.environment,
		Stdin:            strings.NewReader(prompt),
		Stdout:           stream,
	})
	stream.finish()
	stream.reportResult(executor.readUsage())
	report.Diagnostics(result.Stderr)
	err = turnOutcome(context.Cause(ctx), runErr, stream)
	report.Finished(len(stream.events), err)
	return stream.events, err
}

// readUsage is the usage file the turn wrote, or nil when there is none: a
// turn that ended early writes no usage.
func (executor *CopilotCLIExecutor) readUsage() json.RawMessage {
	if executor.usageFile == "" {
		return nil
	}
	content, err := os.ReadFile(executor.usageFile)
	if err != nil || !json.Valid(content) {
		return nil
	}
	return content
}

// turnOutcome decides whether a finished run completed its turn. A turn
// Interrupt or Close stopped says so first: the process's own error is only
// the kill.
func turnOutcome(cause error, runErr error, stream *recordStream) error {
	switch {
	case runErr != nil && (errors.Is(cause, ErrInterrupted) || errors.Is(cause, ErrClosed)):
		return fmt.Errorf("%w: %w", cause, runErr)
	case stream.failure != nil:
		return stream.failure
	case runErr != nil:
		return runErr
	case stream.result == nil:
		return ErrTurnIncomplete
	}
	return nil
}

// copilotArguments is the Copilot argument vector for one turn: the session, the
// output framing, the usage file, then the caller's extra arguments. The same
// session identifier creates the session on the first turn and resumes it
// later. The prompt arrives on standard input.
func (executor *CopilotCLIExecutor) copilotArguments() []string {
	arguments := []string{flagSessionID, executor.session.String(), flagOutputFormat, formatJSON, flagNoAutoUpdate}
	if executor.usageFile != "" {
		arguments = append(arguments, flagUsageFile, executor.usageFile)
	}
	return append(arguments, executor.arguments...)
}

// processExecutable is the program the proc capability starts: the launch
// prefix's head when a prefix is set, otherwise Copilot itself.
func (executor *CopilotCLIExecutor) processExecutable() string {
	if len(executor.launchPrefix) > 0 {
		return executor.launchPrefix[0]
	}
	return executor.executable
}

// processArguments is the full argument vector the process is started with:
// with a launch prefix, the rest of the prefix, then the Copilot executable
// and its arguments.
func (executor *CopilotCLIExecutor) processArguments() []string {
	if len(executor.launchPrefix) == 0 {
		return executor.copilotArguments()
	}
	argv := append([]string{}, executor.launchPrefix[1:]...)
	argv = append(argv, executor.executable)
	return append(argv, executor.copilotArguments()...)
}

// userMessage is prompt as one Claude Code stream-json user message.
func userMessage(prompt string) (json.RawMessage, error) {
	type content struct {
		Role    string `json:"role"`
		Content string `json:"content"`
	}
	type message struct {
		Type    string  `json:"type"`
		Message content `json:"message"`
	}
	encoded, err := json.Marshal(message{Type: EventTypeUser, Message: content{Role: roleUser, Content: prompt}})
	if err != nil {
		return nil, fmt.Errorf("copilotcli executor: encode prompt: %w", err)
	}
	return encoded, nil
}

// Interrupt stops the running turn's process; the turn returns
// [ErrInterrupted]. Between turns there is nothing to stop.
func (executor *CopilotCLIExecutor) Interrupt(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if executor.lifetime.Err() != nil {
		return ErrClosed
	}
	if cancel := executor.running.Load(); cancel != nil {
		(*cancel)(ErrInterrupted)
	}
	return nil
}

// Close stops the running turn, if any, waits for it to return and refuses
// every later turn. ctx bounds the wait. It is idempotent.
func (executor *CopilotCLIExecutor) Close(ctx context.Context) error {
	executor.stop(ErrClosed)
	if cancel := executor.running.Load(); cancel != nil {
		(*cancel)(ErrClosed)
	}
	select {
	case turns := <-executor.turns:
		executor.turns <- turns
		return nil
	case <-ctx.Done():
		return context.Cause(ctx)
	}
}

// recordHeader is the part of a Copilot JSON record the executor reads.
type recordHeader struct {
	Type      string `json:"type"`
	Ephemeral bool   `json:"ephemeral"`
}

// recordStream is Copilot's standard output. The process gateway's copying
// goroutine is its only writer, and Run joins that goroutine before it
// returns, so the executor reads the stream's fields after Run without a
// lock.
type recordStream struct {
	report  *model.TurnReport
	session uuid.UUID
	pending []byte
	lines   int
	events  []Event
	// result is Copilot's result record, held until the process exits.
	result  json.RawMessage
	failure error
}

// Write splits chunk into lines and reads each complete one. It never fails:
// an error would stop the copy and leave Copilot blocked on a full pipe, so a
// malformed line is recorded and reading continues.
func (stream *recordStream) Write(chunk []byte) (int, error) {
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

// finish reads a last line Copilot wrote without a newline.
func (stream *recordStream) finish() {
	if len(stream.pending) > 0 {
		stream.line(stream.pending)
		stream.pending = nil
	}
}

// line reads one output line. Ephemeral records (deltas, progress) are
// skipped; an assistant message is reported as an assistant event and a
// tool's completion as its tool result, the result is held for the end of the
// process, and every other record is reported as Copilot wrote it.
func (stream *recordStream) line(text []byte) {
	stream.lines++
	trimmed := bytes.TrimSpace(text)
	if len(trimmed) == 0 {
		return
	}
	var header recordHeader
	err := json.Unmarshal(trimmed, &header)
	if err == nil && header.Type == "" {
		err = errors.New("record has no type")
	}
	if err != nil {
		stream.fail(fmt.Errorf("%w: line %d: %w", ErrMalformedEvent, stream.lines, err))
		stream.report.Malformed(stream.lines, len(trimmed), err)
		return
	}
	switch {
	case header.Ephemeral:
		return
	case header.Type == recordResult:
		stream.result = bytes.Clone(trimmed)
		return
	case header.Type == recordAssistantMessage:
		event, err := assistantEvent(stream.session, trimmed)
		if err == nil {
			stream.emit(event)
			return
		}
		stream.report.Malformed(stream.lines, len(trimmed), err)
	case header.Type == recordToolComplete:
		event, err := toolResultEvent(stream.session, trimmed)
		if err == nil {
			stream.emit(event)
			return
		}
		stream.report.Malformed(stream.lines, len(trimmed), err)
	}
	stream.emit(Event{Type: header.Type, Raw: bytes.Clone(trimmed)})
}

func (stream *recordStream) emit(event Event) {
	stream.events = append(stream.events, event)
	stream.report.Event(model.DirectionOut, event.Type, event.Raw)
}

// reportResult reports the held result record as the turn's result event,
// with usage the usage file's content. A process that wrote no result reports
// none.
func (stream *recordStream) reportResult(usage json.RawMessage) {
	if stream.result == nil {
		return
	}
	event, err := resultEvent(stream.result, usage)
	if err != nil {
		stream.fail(fmt.Errorf("%w: result: %w", ErrMalformedEvent, err))
		stream.report.Malformed(stream.lines, len(stream.result), err)
		return
	}
	stream.emit(event.event)
	switch {
	case event.session != stream.session.String():
		stream.fail(fmt.Errorf("%w: %q, not %s", ErrSessionMismatch, event.session, stream.session))
	case event.exitCode != 0:
		stream.fail(fmt.Errorf("%w: exit code %d", ErrTurnFailed, event.exitCode))
	}
}

// fail keeps the first failure; later lines are still read and reported.
func (stream *recordStream) fail(err error) {
	if stream.failure == nil {
		stream.failure = err
	}
}
