// Copyright 2026 Candace Labs

package intake

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/candacelabs/csf/io/kernel/clock"
	"github.com/candacelabs/csf/pkg/telemetry"
	intakev1 "github.com/candacelabs/csf/proto/candace/intake/v1"
	telemetryv1 "github.com/candacelabs/csf/proto/candace/telemetry/v1"
	"github.com/candacelabs/csf/services/harness/session"
)

// The GitHub event stream is a run directory of its own under the state
// directory, <state>/<StreamAssignment>, holding an events.jsonl and no run
// record. So the readers of every run's event log read it unchanged: csf
// events -assignment <StreamAssignment> prints it, and the mining loop, which
// mines <corpus>/*/events.jsonl, mines it. Having no run record, it is never
// resumed as a session and never shown as a session's card.
const (
	// StreamAssignment names the stream's run directory.
	StreamAssignment = "00000000-0000-0000-0000-000000000401"
	// StreamSession is the session identifier its records carry.
	StreamSession = "github"
	// DeliveriesDirectory, inside the stream's directory, keeps each
	// accepted delivery's raw body as <delivery>.json: the corpus copy, and
	// the record that the delivery was received.
	DeliveriesDirectory = "deliveries"

	payloadSuffix  = ".json"
	directoryMode  = 0o700
	payloadMode    = 0o600
	payloadFlags   = os.O_CREATE | os.O_EXCL | os.O_WRONLY
	streamTraceID  = "00000000000000000000000000000401"
	streamRootSpan = "0000000000000401"
)

// The stream's record types.
const (
	// EventTypeDelivery records one delivery and its outcome.
	EventTypeDelivery = "github_delivery"
	// EventTypeEvent records one typed event a delivery carried.
	EventTypeEvent = "github_event"
	// EventTypeRouted records what a route did with one event.
	EventTypeRouted = "github_event_routed"
)

// The stream records' attribute keys.
const (
	KeyDelivery   = "delivery"
	KeyEvent      = "github_event"
	KeyOutcome    = "outcome"
	KeyRefusal    = "refusal"
	KeyKind       = "kind"
	KeyEventID    = "event_id"
	KeyRepository = "repository"
	KeySubject    = "subject"
	KeyNumber     = "number"
	KeyBranch     = "branch"
	KeyBase       = "base"
	KeyConclusion = "conclusion"
	KeyActor      = "actor"
	KeyURL        = "url"
	KeySummary    = "summary"
	KeyOccurredAt = "occurred_at"
	KeyLatency    = "latency_seconds"
	KeyPayload    = "payload"
	KeyTarget     = "target"
	KeyDetail     = "detail"
	subjectPrefix = "SUBJECT_KIND_"
	maxHeaderEcho = 64
)

// Stream appends the GitHub event stream and keeps the deliveries' raw
// bodies. Every record is one whole line appended to the log, so concurrent
// deliveries interleave whole records.
type Stream struct {
	directory string
	clock     clock.IClock
	trace     *telemetryv1.TraceContext
}

// NewStream builds the stream of the state directory, creating its directory.
func NewStream(stateDirectory string, source clock.IClock) (*Stream, error) {
	if stateDirectory == "" {
		return nil, ErrNoStateDirectory
	}
	if source == nil {
		return nil, errors.New("intake: NewStream needs a clock")
	}
	directory := session.RunDirectory(stateDirectory, StreamAssignment)
	if err := os.MkdirAll(filepath.Join(directory, DeliveriesDirectory), directoryMode); err != nil {
		return nil, fmt.Errorf("intake: create the GitHub event stream: %w", err)
	}
	trace := &telemetryv1.TraceContext{TraceId: streamTraceID, SpanId: streamRootSpan, TraceFlags: telemetry.TraceFlagsSampled}
	return &Stream{directory: directory, clock: source, trace: trace}, nil
}

// Directory is the stream's run directory.
func (stream *Stream) Directory() string { return stream.directory }

func (stream *Stream) now() time.Time { return stream.clock.Now() }

// Keep stores a delivery's raw body unless one with its GUID is kept already;
// kept is false for a duplicate.
func (stream *Stream) Keep(delivery string, body []byte) (kept bool, err error) {
	path := payloadPath(stream.directory, delivery)
	file, err := os.OpenFile(path, payloadFlags, payloadMode)
	if errors.Is(err, os.ErrExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if _, err := file.Write(body); err != nil {
		_ = file.Close()
		return false, errors.Join(err, os.Remove(path))
	}
	if err := file.Close(); err != nil {
		return false, errors.Join(err, os.Remove(path))
	}
	// The copy is stamped with the stream's clock: LastKept, which bounds
	// recovery, reads the same clock recovery measures with.
	received := stream.now()
	if err := os.Chtimes(path, received, received); err != nil {
		return false, errors.Join(err, os.Remove(path))
	}
	return true, nil
}

// Kept reports whether a delivery with this GUID was received.
func (stream *Stream) Kept(delivery string) bool {
	return deliveryPattern.MatchString(delivery) && fileExists(payloadPath(stream.directory, delivery))
}

// LastKept is when the newest kept delivery was received; zero when none is.
func (stream *Stream) LastKept() (time.Time, error) {
	entries, err := os.ReadDir(filepath.Join(stream.directory, DeliveriesDirectory))
	if err != nil {
		return time.Time{}, err
	}
	var newest time.Time
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}
	return newest, nil
}

// RecordDelivery appends one delivery's outcome.
func (stream *Stream) RecordDelivery(ctx context.Context, answer Answer, cause error) {
	attributes := []slog.Attr{
		slog.String(KeyDelivery, excerptTo(answer.Delivery, maxHeaderEcho)),
		slog.String(KeyEvent, excerptTo(answer.Event, maxHeaderEcho)),
		slog.String(KeyOutcome, string(answer.Outcome)),
	}
	if answer.Refusal != "" {
		attributes = append(attributes, slog.String(KeyRefusal, string(answer.Refusal)))
	}
	message := fmt.Sprintf("github delivery %s: %s", answer.Event, answer.Outcome)
	stream.append(ctx, EventTypeDelivery, message, cause, attributes)
}

// RecordEvent appends one typed event, with how long after it happened it was
// recorded.
func (stream *Stream) RecordEvent(ctx context.Context, event *intakev1.Event) {
	subject := event.GetSubject()
	attributes := []slog.Attr{
		slog.String(KeyDelivery, event.GetDelivery()),
		slog.String(KeyKind, kindWord(event.GetKind())),
		slog.String(KeyEventID, event.GetId()),
		slog.String(KeyRepository, subject.GetRepository()),
		slog.String(KeySubject, strings.ToLower(strings.TrimPrefix(subject.GetKind().String(), subjectPrefix))),
		slog.Uint64(KeyNumber, subject.GetNumber()),
		slog.String(KeyBranch, event.GetBranch()),
		slog.String(KeyBase, event.GetBase()),
		slog.String(KeyConclusion, event.GetConclusion()),
		slog.String(KeyActor, event.GetActor()),
		slog.String(KeyURL, event.GetUrl()),
		slog.String(KeySummary, event.GetSummary()),
		slog.Time(KeyOccurredAt, event.GetOccurredAt().AsTime()),
		slog.Float64(KeyLatency, Latency(event, stream.now()).Seconds()),
		slog.String(KeyPayload, filepath.Join(DeliveriesDirectory, event.GetDelivery()+payloadSuffix)),
	}
	stream.append(ctx, EventTypeEvent, EventLine(event), nil, attributes)
}

// RecordRouted appends what one route did with one event.
func (stream *Stream) RecordRouted(ctx context.Context, event *intakev1.Event, routed Routed, cause error) {
	attributes := []slog.Attr{
		slog.String(KeyDelivery, event.GetDelivery()),
		slog.String(KeyKind, kindWord(event.GetKind())),
		slog.String(KeyEventID, event.GetId()),
		slog.String(KeyTarget, routed.Target),
		slog.String(KeyDetail, routed.Detail),
	}
	message := fmt.Sprintf("%s routed to %s %s", kindWord(event.GetKind()), routed.Target, routed.Detail)
	stream.append(ctx, EventTypeRouted, message, cause, attributes)
}

// append writes one record; a record that cannot be written is lost, never
// the delivery's outcome.
func (stream *Stream) append(ctx context.Context, eventType string, message string, cause error, attributes []slog.Attr) {
	log, err := session.OpenEventLog(stream.directory, stream.trace)
	if err != nil {
		return
	}
	defer func() { _ = log.Close() }()
	if cause != nil {
		log.Failure(ctx, StreamSession, 0, eventType, message, cause, attributes...)
		return
	}
	log.Record(ctx, StreamSession, 0, eventType, message, attributes...)
}

// Latency is how long after the event happened it was recorded at now; zero
// for an event that names no time.
func Latency(event *intakev1.Event, now time.Time) time.Duration {
	occurred := event.GetOccurredAt()
	if occurred == nil || occurred.AsTime().IsZero() || occurred.AsTime().Unix() <= 0 {
		return 0
	}
	return max(0, now.Sub(occurred.AsTime()))
}

// EventLine is one line naming an event: "pull_request_merged
// candacelabs/csf#12 by octocat: title".
func EventLine(event *intakev1.Event) string {
	subject := event.GetSubject()
	where := subject.GetRepository()
	if subject.GetNumber() != 0 {
		where = fmt.Sprintf("%s#%d", where, subject.GetNumber())
	}
	if event.GetBranch() != "" {
		where += " (" + event.GetBranch() + ")"
	}
	return fmt.Sprintf("%s %s by %s: %s", kindWord(event.GetKind()), where, event.GetActor(), firstLine(event.GetSummary()))
}

// excerptTo cuts an untrusted header to limit bytes before it is recorded.
func excerptTo(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	return strings.ToValidUTF8(text[:limit], "")
}
