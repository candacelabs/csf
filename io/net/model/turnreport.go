// Copyright 2026 Candace Labs

package model

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/candacelabs/csf/pkg/telemetry"
)

// Report vocabulary: the slog message and attribute keys every turn
// executor's turn report writes, so one run's log reads the same whichever
// executor ran the turn.
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

	// DirectionIn marks an event the turn executor was given.
	DirectionIn = "in"
	// DirectionOut marks an event the turn executor reported.
	DirectionOut = "out"
	// EventTypeMalformed names, in the report only, an output line that was
	// not an event.
	EventTypeMalformed = "malformed"

	// eventTypeSystem is the executor event type that carries progress ticks.
	eventTypeSystem = "system"
	// progressInterval is the least time between two progress ticks the
	// report writes. A model streams a thinking tick per token; written one
	// line each they made 99.7% of a session's event log (775 MB in one
	// session on 2026-10-06), so the report keeps one per interval and the
	// latest before anything else.
	progressInterval = 2 * time.Second
)

// progressSubtypes are the system event subtypes that only restate a running
// total: the latest tick carries everything the earlier ones did.
var progressSubtypes = map[string]bool{"thinking_tokens": true}

// eventHead is the part of an executor event that says what kind it is.
type eventHead struct {
	Subtype string `json:"subtype"`
}

// heldTick is a progress tick the report thinned out and has not written
// yet: the latest of its run, written before the next other event.
type heldTick struct {
	direction string
	kind      string
	raw       json.RawMessage
}

// TurnReport writes one turn's structured log: a trace per turn, a span per
// event. When the caller's context already carries a trace, the turn is a
// child span of it; otherwise the turn starts a new W3C trace. One goroutine
// at a time writes a report.
type TurnReport struct {
	ctx      context.Context
	logger   *slog.Logger
	provider string
	session  uuid.UUID
	turn     int
	traceID  string
	spanID   string
	begun    time.Time
	sequence int
	// tickWritten is when the last progress tick was written.
	tickWritten time.Time
	// held is the latest progress tick not yet written, or nil.
	held *heldTick
}

// NewTurnReport opens the report of turn on session for provider.
func NewTurnReport(ctx context.Context, logger *slog.Logger, provider string, session uuid.UUID, turn int) (*TurnReport, error) {
	turnCtx, err := turnTrace(ctx)
	if err != nil {
		return nil, err
	}
	trace, _ := telemetry.TraceFromContext(turnCtx)
	return &TurnReport{
		ctx:      turnCtx,
		logger:   logger,
		provider: provider,
		session:  session,
		turn:     turn,
		traceID:  trace.GetTraceId(),
		spanID:   trace.GetSpanId(),
		begun:    time.Now(),
	}, nil
}

// turnTrace is ctx carrying the turn's span.
func turnTrace(ctx context.Context) (context.Context, error) {
	if _, ok := telemetry.TraceFromContext(ctx); ok {
		childSpan, err := telemetry.ContextWithChildSpan(ctx)
		return childSpan.Context, err
	}
	trace, err := telemetry.NewTraceContext(telemetry.TraceFlagsSampled)
	if err != nil {
		return nil, err
	}
	return telemetry.ContextWithTrace(ctx, trace)
}

func (report *TurnReport) attributes(extra ...slog.Attr) []slog.Attr {
	return append([]slog.Attr{
		slog.String(keyTraceID, report.traceID),
		slog.String(keySessionID, report.session.String()),
		slog.Int(keyTurn, report.turn),
		slog.String(keyProvider, report.provider),
	}, extra...)
}

// Started reports the turn's start: the executable run, whether the session
// is resumed and how many inputs the turn carries.
func (report *TurnReport) Started(executable string, resume bool, inputs int) {
	report.logger.LogAttrs(report.ctx, slog.LevelInfo, reportTurnStarted, report.attributes(
		slog.String(keySpanID, report.spanID),
		slog.String(keyExecutable, executable),
		slog.Bool(keyResume, resume),
		slog.Int(keyInputs, inputs),
	)...)
}

// Event reports one event in or out under a span of its own. Progress ticks
// are thinned to one per progressInterval; the latest thinned tick is written
// before the next other event and before the turn's end, so the final running
// total is always in the log.
func (report *TurnReport) Event(direction string, kind string, raw json.RawMessage) {
	if isProgressTick(kind, raw) {
		if time.Since(report.tickWritten) < progressInterval {
			report.held = &heldTick{direction: direction, kind: kind, raw: append(json.RawMessage(nil), raw...)}
			return
		}
		report.held = nil
		report.tickWritten = time.Now()
	} else {
		report.writeHeld()
	}
	report.write(direction, kind, raw)
}

// isProgressTick reports whether an event only restates a running total.
func isProgressTick(kind string, raw json.RawMessage) bool {
	if kind != eventTypeSystem {
		return false
	}
	var head eventHead
	return json.Unmarshal(raw, &head) == nil && progressSubtypes[head.Subtype]
}

// writeHeld writes the held progress tick, if any.
func (report *TurnReport) writeHeld() {
	if report.held == nil {
		return
	}
	held := report.held
	report.held = nil
	report.tickWritten = time.Now()
	report.write(held.direction, held.kind, held.raw)
}

// write logs one event under a span of its own.
func (report *TurnReport) write(direction string, kind string, raw json.RawMessage) {
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

// Malformed reports an output line that was not an event; the line itself is
// not logged, only where it was and how long.
func (report *TurnReport) Malformed(line int, size int, err error) {
	report.writeHeld()
	report.sequence++
	report.logger.LogAttrs(report.ctx, slog.LevelWarn, reportMalformed, report.attributes(
		slog.String(keySpanID, report.eventSpan()),
		slog.String(keyParentSpanID, report.spanID),
		slog.Int(keySequence, report.sequence),
		slog.String(keyDirection, DirectionOut),
		slog.String(keyEventType, EventTypeMalformed),
		report.since(keyElapsed),
		slog.Int(keyLine, line),
		slog.Int(keyBytes, size),
		slog.String(keyError, err.Error()),
	)...)
}

// since is the time since the turn began, in milliseconds.
func (report *TurnReport) since(key string) slog.Attr {
	return slog.Float64(key, float64(time.Since(report.begun).Microseconds())/1000)
}

// eventSpan is a new child span of the turn, or empty if none can be derived.
func (report *TurnReport) eventSpan() string {
	childSpan, err := telemetry.ContextWithChildSpan(report.ctx)
	if err != nil {
		return ""
	}
	return childSpan.Trace.GetSpanId()
}

// Diagnostics reports what the executor's process wrote to standard error,
// if anything.
func (report *TurnReport) Diagnostics(stderr []byte) {
	text := bytes.TrimSpace(stderr)
	if len(text) == 0 {
		return
	}
	report.logger.LogAttrs(report.ctx, slog.LevelWarn, reportDiagnostics, report.attributes(
		slog.String(keySpanID, report.spanID),
		slog.String(keyStderr, string(text)),
	)...)
}

// Finished reports the turn's end: how many events it carried and, for a
// failed turn, why.
func (report *TurnReport) Finished(events int, err error) {
	report.writeHeld()
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
