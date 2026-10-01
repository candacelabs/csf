package telemetry

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	telemetryv1 "github.com/candacelabs/csf/proto/candace/telemetry/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	// ErrNilWriter indicates that a JSONL logger was constructed without a
	// destination.
	ErrNilWriter = errors.New("telemetry: nil JSONL writer")
)

// JSONLLogger writes one validated LogRecord per physical line. A logger is
// safe for concurrent use even when its destination io.Writer is not.
type JSONLLogger struct {
	mu        sync.Mutex
	writer    io.Writer
	service   string
	component string
}

// NewJSONLLogger constructs a logger with service identity applied to every
// record emitted through Log. component may be empty.
func NewJSONLLogger(writer io.Writer, service, component string) (*JSONLLogger, error) {
	if writer == nil {
		return nil, ErrNilWriter
	}
	if len(service) == 0 || len(service) > 128 {
		return nil, fmt.Errorf("telemetry: logger service length must be between 1 and 128 bytes")
	}
	if len(component) > 128 {
		return nil, fmt.Errorf("telemetry: logger component exceeds 128 bytes")
	}
	return &JSONLLogger{
		writer:    writer,
		service:   service,
		component: component,
	}, nil
}

// Log builds and writes a record, automatically adding the current timestamp
// and any TraceContext carried by ctx. The attributes map is copied.
func (logger *JSONLLogger) Log(
	ctx context.Context,
	severity telemetryv1.Severity,
	event string,
	message string,
	attributes map[string]string,
) error {
	if ctx == nil {
		return ErrNilContext
	}
	record := &telemetryv1.LogRecord{
		Timestamp:  timestamppb.New(time.Now()),
		Severity:   severity,
		Service:    logger.service,
		Component:  logger.component,
		Event:      event,
		Message:    message,
		Attributes: attributes,
	}
	if trace, ok := TraceFromContext(ctx); ok {
		record.TraceContext = trace
	}
	return logger.WriteRecord(record)
}

// WriteRecord validates and writes record without modifying it. Proto field
// names are retained in JSON so Loki queries match the canonical schema.
func (logger *JSONLLogger) WriteRecord(record *telemetryv1.LogRecord) error {
	if record == nil {
		return fmt.Errorf("telemetry: write record: %w", ErrInvalidLogRecord)
	}
	snapshot := proto.Clone(record).(*telemetryv1.LogRecord)
	if err := ValidateLogRecord(snapshot); err != nil {
		return fmt.Errorf("telemetry: write record: %w", err)
	}
	encoded, err := (protojson.MarshalOptions{UseProtoNames: true}).Marshal(snapshot)
	if err != nil {
		return fmt.Errorf("telemetry: marshal JSONL record: %w", err)
	}
	encoded = append(encoded, '\n')

	logger.mu.Lock()
	defer logger.mu.Unlock()
	if _, err := io.Copy(logger.writer, bytes.NewReader(encoded)); err != nil {
		return fmt.Errorf("telemetry: write JSONL record: %w", err)
	}
	return nil
}
