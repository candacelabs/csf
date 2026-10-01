package telemetry

import (
	"errors"
	"fmt"
	"regexp"
	"unicode/utf8"

	telemetryv1 "github.com/candacelabs/csf/proto/candace/telemetry/v1"
)

const (
	// MaxAttributes bounds the number of attributes on one LogRecord.
	MaxAttributes = 32
	// MaxAttributeKeyBytes bounds one attribute key.
	MaxAttributeKeyBytes = 64
	// MaxAttributeValueBytes bounds one attribute value.
	MaxAttributeValueBytes = 1024
	// MaxAttributeBytes bounds all attribute keys and values on one LogRecord.
	MaxAttributeBytes = 8192
)

var (
	// ErrInvalidTraceContext is the sentinel for an invalid portable trace.
	ErrInvalidTraceContext = errors.New("telemetry: invalid trace context")
	// ErrInvalidLogRecord is the sentinel for a record rejected at the JSONL
	// write boundary.
	ErrInvalidLogRecord = errors.New("telemetry: invalid log record")
	attributeKeyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]{0,63}$`)
)

// ValidateTraceContext validates the portable trace fields used by Candace.
func ValidateTraceContext(trace *telemetryv1.TraceContext) error {
	_, err := spanContextFromProto(trace)
	return err
}

// ValidateLogRecord checks the complete logging contract. Generated Liquid
// validation covers the record's scalar fields; nested messages and maps are
// deliberately checked here because refinement generation is non-recursive.
func ValidateLogRecord(record *telemetryv1.LogRecord) error {
	if record == nil {
		return fmt.Errorf("%w: nil LogRecord", ErrInvalidLogRecord)
	}
	if record.GetTimestamp() == nil {
		return fmt.Errorf("%w: timestamp is required", ErrInvalidLogRecord)
	}
	if err := record.GetTimestamp().CheckValid(); err != nil {
		return fmt.Errorf("%w: timestamp: %w", ErrInvalidLogRecord, err)
	}
	if !validSeverity(record.GetSeverity()) {
		return fmt.Errorf("%w: unsupported severity %d", ErrInvalidLogRecord, record.GetSeverity())
	}
	if err := telemetryv1.ValidateLogRecord(record); err != nil {
		return fmt.Errorf("%w: %w", ErrInvalidLogRecord, err)
	}
	if trace := record.GetTraceContext(); trace != nil {
		if err := ValidateTraceContext(trace); err != nil {
			return fmt.Errorf("%w: nested trace_context: %w", ErrInvalidLogRecord, err)
		}
	}
	if err := validateAttributes(record.GetAttributes()); err != nil {
		return fmt.Errorf("%w: attributes: %w", ErrInvalidLogRecord, err)
	}
	return nil
}

func validSeverity(severity telemetryv1.Severity) bool {
	_, known := telemetryv1.Severity_name[int32(severity)]
	return known && severity != telemetryv1.Severity_SEVERITY_UNSPECIFIED
}

func validateAttributes(attributes map[string]string) error {
	if len(attributes) > MaxAttributes {
		return fmt.Errorf("count %d exceeds %d", len(attributes), MaxAttributes)
	}
	total := 0
	for key, value := range attributes {
		if !utf8.ValidString(key) || !attributeKeyPattern.MatchString(key) {
			return fmt.Errorf("key %q must match [A-Za-z_][A-Za-z0-9_.-]{0,%d}", key, MaxAttributeKeyBytes-1)
		}
		if !utf8.ValidString(value) {
			return fmt.Errorf("value for %q is not valid UTF-8", key)
		}
		if len(value) > MaxAttributeValueBytes {
			return fmt.Errorf("value for %q is %d bytes; maximum is %d", key, len(value), MaxAttributeValueBytes)
		}
		total += len(key) + len(value)
	}
	if total > MaxAttributeBytes {
		return fmt.Errorf("total size %d bytes exceeds %d", total, MaxAttributeBytes)
	}
	return nil
}
