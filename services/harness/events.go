// Copyright 2026 Candace Labs

package harness

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/candacelabs/csf/pkg/httpserver"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/harness/session"
)

// The live event stream of one session: its events.jsonl, every record the
// session, its turn executor and its gates wrote, followed as it grows.
const (
	// EventsPath serves one session's records as server-sent events. The
	// query parameter from skips that many records.
	EventsPath = "/api/harness/sessions/:assignment/events"

	assignmentParameter = "assignment"
	fromParameter       = "from"

	// DefaultTailInterval is how often a tail that reached the end of the
	// file looks for records the gates, in their own processes, appended.
	DefaultTailInterval = 250 * time.Millisecond
	maxRecordBytes      = 4 << 20
)

// ErrNoEvents reports a session whose event log does not exist.
var ErrNoEvents = fmt.Errorf("%w: no event log for this assignment", ErrUnknownSession)

// EventTail reads one session's records in order and waits for more while
// the session is live. One goroutine reads it.
type EventTail struct {
	service    *AgentSessionService
	assignment string
	file       *os.File
	reader     *bufio.Reader
	partial    []byte
	sequence   int
	drained    bool
}

// OpenTail opens the event log of assignmentID, skipping the first from
// records. A session this harness is still opening is waited for; a session
// it does not hold is read from disk as it is, which is how a finished run
// from an earlier process is replayed.
func (service *AgentSessionService) OpenTail(ctx context.Context, assignmentID string, from int) (*EventTail, error) {
	if err := harnessv1.ValidateGetAgentSessionRequest(&harnessv1.GetAgentSessionRequest{AssignmentId: assignmentID}); err != nil {
		return nil, fmt.Errorf("%w: %w", ErrUnknownSession, err)
	}
	if from < 0 {
		return nil, fmt.Errorf("%w: from must not be negative", ErrUnknownSession)
	}
	var opened <-chan struct{}
	_ = service.command(ctx, func(table *registry) error {
		if record, exists := table.sessions[assignmentID]; exists {
			opened = record.opened
		}
		return nil
	})
	if opened != nil {
		select {
		case <-opened:
		case <-ctx.Done():
			return nil, context.Cause(ctx)
		}
	}
	path := filepath.Join(session.RunDirectory(service.runner.StateDirectory(), assignmentID), session.EventsFile)
	file, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("%w: %s", ErrNoEvents, assignmentID)
	}
	if err != nil {
		return nil, fmt.Errorf("harness: open event log: %w", err)
	}
	tail := &EventTail{service: service, assignment: assignmentID, file: file, reader: bufio.NewReaderSize(file, 64<<10)}
	for tail.sequence < from {
		if _, err := tail.Next(ctx); err != nil {
			_ = tail.Close()
			return nil, err
		}
	}
	return tail, nil
}

// Sequence is the number of records read so far: the next record's position.
func (tail *EventTail) Sequence() int { return tail.sequence }

// Available returns the next record already in the file, or nil at its
// current end; it never waits.
func (tail *EventTail) Available() []byte {
	line := tail.readLine()
	if line != nil {
		tail.sequence++
	}
	return line
}

// Next returns the next record, waiting for it while the session is live.
// io.EOF reports that the session has finished and every record was read.
func (tail *EventTail) Next(ctx context.Context) ([]byte, error) {
	for {
		if line := tail.readLine(); line != nil {
			tail.sequence++
			return line, nil
		}
		if tail.drained {
			return nil, io.EOF
		}
		if tail.finished(ctx) {
			// One more read after the end was recorded, so the finishing
			// records themselves are delivered.
			tail.drained = true
			continue
		}
		wait, stop := tail.service.clock.After(DefaultTailInterval)
		select {
		case <-wait:
		case <-ctx.Done():
			stop()
			return nil, context.Cause(ctx)
		}
	}
}

// readLine returns the next complete line, or nil at the current end of the
// file; a partial last line is kept until its newline arrives.
func (tail *EventTail) readLine() []byte {
	for {
		chunk, err := tail.reader.ReadBytes('\n')
		tail.partial = append(tail.partial, chunk...)
		if err == nil {
			line := bytes.TrimRight(tail.partial, "\r\n")
			tail.partial = nil
			if len(line) == 0 {
				continue
			}
			return bytes.Clone(line)
		}
		return nil
	}
}

// finished reports whether the session has ended; a session this process
// does not hold is finished by definition.
func (tail *EventTail) finished(ctx context.Context) bool {
	done := true
	_ = tail.service.command(ctx, func(table *registry) error {
		if record, exists := table.sessions[tail.assignment]; exists {
			done = finished(record.state.GetPhase())
		}
		return nil
	})
	return done
}

// Close closes the log file.
func (tail *EventTail) Close() error { return tail.file.Close() }

// Register mounts the event stream route on the caller's router. The
// generated operations are registered by the CSF service the harness is
// mounted behind.
func (service *AgentSessionService) Register(router gin.IRouter) {
	router.GET(EventsPath, service.serveEvents)
}

func (service *AgentSessionService) serveEvents(ctx *gin.Context) {
	from := 0
	if raw := ctx.Query(fromParameter); raw != "" {
		parsed, err := strconv.Atoi(raw)
		if err != nil || parsed < 0 {
			ctx.String(http.StatusBadRequest, "from must be a non-negative integer")
			return
		}
		from = parsed
	}
	tail, err := service.OpenTail(ctx.Request.Context(), ctx.Param(assignmentParameter), from)
	if err != nil {
		ctx.String(eventStreamStatus(err), "%s", err.Error())
		return
	}
	defer func() { _ = tail.Close() }()
	httpserver.EventStream(ctx, func(writer io.Writer) bool {
		line, err := tail.Next(ctx.Request.Context())
		if err != nil {
			return false
		}
		if len(line) > maxRecordBytes || !json.Valid(line) {
			return true
		}
		return httpserver.EncodeEvent(writer, strconv.Itoa(tail.Sequence()), json.RawMessage(line)) == nil
	})
}

func eventStreamStatus(err error) int {
	switch {
	case errors.Is(err, ErrUnknownSession):
		return http.StatusNotFound
	case errors.Is(err, ErrNotStarted):
		return http.StatusServiceUnavailable
	default:
		return http.StatusInternalServerError
	}
}
