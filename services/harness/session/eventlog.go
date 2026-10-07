// Copyright 2026 Candace Labs

package session

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"github.com/candacelabs/csf/pkg/telemetry"
	telemetryv1 "github.com/candacelabs/csf/proto/candace/telemetry/v1"
)

// The keys every event log record carries. The turn executor's own records
// (ipc/model/claudecode) use the same spellings, so one log reads as one
// trace.
const (
	KeyTraceID      = "trace_id"
	KeySpanID       = "span_id"
	KeyParentSpanID = "parent_span_id"
	KeySessionID    = "session_id"
	KeyTurn         = "turn"
	KeySequence     = "sequence"
	KeyEventType    = "event_type"
	KeyElapsed      = "elapsed_ms"
	KeyError        = "error"
	// KeyExecutor is the turn executor a run started on, on its run started
	// record.
	KeyExecutor = "executor"

	keyAssignmentID     = "assignment_id"
	keyAgentID          = "agent_id"
	keyRecipeSHA256     = "recipe_sha256"
	keyWorktree         = "worktree"
	keyBranch           = "branch"
	keyResume           = "resume"
	keyPullRequestURL   = "pull_request_url"
	keyBuildContainerID = "build_container_id"
)

// Event types the harness itself writes; the turn executor writes the
// stream-json types of the events it forwards.
const (
	EventTypeRunStarted          = "harness_run_started"
	EventTypeWorktreeReady       = "harness_worktree_ready"
	EventTypeBuildContainerReady = "harness_build_container_ready"
	EventTypeTurnRequested       = "harness_turn_requested"
	EventTypeRunFinished         = "harness_run_finished"
	EventTypeGateDecision        = "session_gate_decision"
	EventTypeReceiptWritten      = "harness_receipt"
	// EventTypeControlAction records one control-plane operation on the
	// session — submit, send, cancel, ready, merge_started or merge — whichever client called
	// it: the CLI, an MCP client or the Workbench page.
	EventTypeControlAction = "harness_control_action"
	// EventTypeGitHubCall records one GitHub operation the session called
	// through CSF's typed GitHub tools, with its outcome.
	EventTypeGitHubCall = "github_call"
)

// The fields and values of a control action record.
const (
	KeyAction           = "action"
	KeyOperatorAuthored = "operator_authored"
	// KeyQuestionWanted marks a send by which the operator overrides the
	// question gate: the operator wanted the question it last refused.
	KeyQuestionWanted = "question_wanted"
	KeyTurnID         = "turn_id"
	KeyPullRequestURL = keyPullRequestURL

	ActionSubmit = "submit"
	ActionSend   = "send"
	ActionCancel = "cancel"
	ActionReady  = "ready"
	ActionMerge  = "merge"
	// ActionMergeStarted is written when a merge begins, before the merge
	// path runs, so a reader of the log knows one is in flight until the
	// merge record that ends it.
	ActionMergeStarted = "merge_started"
)

// EventLog appends one run's records to its events.jsonl. The runner and
// every gate process open it for appending; each record is one write of one
// line, so records from the session and its hooks interleave whole.
type EventLog struct {
	file     *os.File
	logger   *slog.Logger
	trace    *telemetryv1.TraceContext
	sequence int
	begun    time.Time
}

// OpenEventLog opens the event log of the run in directory for appending,
// under trace, the run's root span.
func OpenEventLog(directory string, trace *telemetryv1.TraceContext) (*EventLog, error) {
	if err := telemetry.ValidateTraceContext(trace); err != nil {
		return nil, fmt.Errorf("harness session: event log trace: %w", err)
	}
	file, err := os.OpenFile(filepath.Join(directory, EventsFile), os.O_CREATE|os.O_APPEND|os.O_WRONLY, runFileMode)
	if err != nil {
		return nil, fmt.Errorf("harness session: open event log: %w", err)
	}
	return &EventLog{file: file, logger: slog.New(slog.NewJSONHandler(file, nil)), trace: trace, begun: time.Now()}, nil
}

// Logger is the structured logger writing into the event log; the turn
// executor reports every event it forwards through it.
func (log *EventLog) Logger() *slog.Logger { return log.logger }

// Context is ctx carrying the run's trace, so spans derived from it, the turn
// executor's included, belong to the run.
func (log *EventLog) Context(ctx context.Context) (context.Context, error) {
	return telemetry.ContextWithTrace(ctx, log.trace)
}

// Record writes one record of eventType under a new child span of the run.
func (log *EventLog) Record(ctx context.Context, session string, turn int, eventType string, message string, attributes ...slog.Attr) {
	log.write(ctx, slog.LevelInfo, session, turn, eventType, message, attributes)
}

// Failure writes one error record of eventType under a new child span.
func (log *EventLog) Failure(ctx context.Context, session string, turn int, eventType string, message string, err error, attributes ...slog.Attr) {
	log.write(ctx, slog.LevelError, session, turn, eventType, message, append(attributes, slog.String(KeyError, err.Error())))
}

func (log *EventLog) write(ctx context.Context, level slog.Level, session string, turn int, eventType string, message string, attributes []slog.Attr) {
	log.sequence++
	span := ""
	if traced, err := telemetry.ContextWithTrace(ctx, log.trace); err == nil {
		if childSpan, err := telemetry.ContextWithChildSpan(traced); err == nil {
			span = childSpan.Trace.GetSpanId()
		}
	}
	record := append([]slog.Attr{
		slog.String(KeyTraceID, log.trace.GetTraceId()),
		slog.String(KeySpanID, span),
		slog.String(KeyParentSpanID, log.trace.GetSpanId()),
		slog.String(KeySessionID, session),
		slog.Int(KeyTurn, turn),
		slog.Int(KeySequence, log.sequence),
		slog.String(KeyEventType, eventType),
		slog.Float64(KeyElapsed, float64(time.Since(log.begun).Microseconds())/1000),
	}, attributes...)
	log.logger.LogAttrs(ctx, level, message, record...)
}

// Close closes the log file.
func (log *EventLog) Close() error { return log.file.Close() }

// RunTrace is the run's root span recorded in state.
func RunTrace(state *RunState) *telemetryv1.TraceContext {
	return &telemetryv1.TraceContext{TraceId: state.TraceID, SpanId: state.SpanID, TraceFlags: telemetry.TraceFlagsSampled}
}
