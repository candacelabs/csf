package telemetry_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/candacelabs/csf/pkg/telemetry"
	telemetryv1 "github.com/candacelabs/csf/proto/candace/telemetry/v1"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	traceIDPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	spanIDPattern  = regexp.MustCompile(`^[0-9a-f]{16}$`)
)

func TestTelemetry(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Telemetry Suite")
}

func validTrace() *telemetryv1.TraceContext {
	return &telemetryv1.TraceContext{
		TraceId:    "0123456789abcdef0123456789abcdef",
		SpanId:     "0123456789abcdef",
		TraceFlags: telemetry.TraceFlagsSampled,
	}
}

func validRecord() *telemetryv1.LogRecord {
	return &telemetryv1.LogRecord{
		Timestamp:    timestamppb.New(time.Date(2026, time.August, 4, 12, 30, 0, 123000000, time.UTC)),
		Severity:     telemetryv1.Severity_SEVERITY_INFO,
		Service:      "jobs",
		Component:    "consumer",
		Event:        "job.completed",
		Message:      "processed",
		TraceContext: validTrace(),
		Attributes:   map[string]string{"attempt": "1", "job_id": "42"},
	}
}

func largeAttributes(count, valueSize int) map[string]string {
	attributes := make(map[string]string, count)
	for index := range count {
		attributes[fmt.Sprintf("key_%02d", index)] = strings.Repeat("x", valueSize)
	}
	return attributes
}

var _ = Describe("trace contexts", func() {
	It("generates non-zero W3C identifiers", func() {
		trace, err := telemetry.NewTraceContext(telemetry.TraceFlagsSampled)
		Expect(err).NotTo(HaveOccurred())
		Expect(traceIDPattern.MatchString(trace.GetTraceId())).To(BeTrue())
		Expect(trace.GetTraceId()).NotTo(Equal(strings.Repeat("0", 32)))
		Expect(spanIDPattern.MatchString(trace.GetSpanId())).To(BeTrue())
		Expect(trace.GetSpanId()).NotTo(Equal(strings.Repeat("0", 16)))
		_, err = telemetry.NewTraceContext(256)
		Expect(errors.Is(err, telemetry.ErrInvalidTraceContext)).To(BeTrue())
	})

	DescribeTable("rejects malformed values",
		func(mutate func(traceContext *telemetryv1.TraceContext)) {
			trace := validTrace()
			mutate(trace)
			Expect(errors.Is(telemetry.ValidateTraceContext(trace), telemetry.ErrInvalidTraceContext)).To(BeTrue())
		},
		Entry("uppercase trace ID", func(trace *telemetryv1.TraceContext) { trace.TraceId = strings.Repeat("A", 32) }),
		Entry("zero trace ID", func(trace *telemetryv1.TraceContext) { trace.TraceId = strings.Repeat("0", 32) }),
		Entry("short span ID", func(trace *telemetryv1.TraceContext) { trace.SpanId = "1234" }),
		Entry("zero span ID", func(trace *telemetryv1.TraceContext) { trace.SpanId = strings.Repeat("0", 16) }),
		Entry("overflowed flags", func(trace *telemetryv1.TraceContext) { trace.TraceFlags = 256 }),
	)

	It("isolates propagated values and derives a child span", func() {
		parent := validTrace()
		ctx, err := telemetry.ContextWithTrace(context.Background(), parent)
		Expect(err).NotTo(HaveOccurred())
		parent.TraceId = strings.Repeat("0", 32)
		stored, ok := telemetry.TraceFromContext(ctx)
		Expect(ok).To(BeTrue())
		Expect(stored.GetTraceId()).To(Equal("0123456789abcdef0123456789abcdef"))

		childContext, child, err := telemetry.ContextWithChildSpan(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(child.GetTraceId()).To(Equal(stored.GetTraceId()))
		Expect(child.GetSpanId()).NotTo(Equal(stored.GetSpanId()))
		Expect(child.GetTraceFlags()).To(Equal(stored.GetTraceFlags()))
		child.SpanId = strings.Repeat("0", 16)
		storedChild, ok := telemetry.TraceFromContext(childContext)
		Expect(ok).To(BeTrue())
		Expect(storedChild.GetSpanId()).NotTo(Equal(child.GetSpanId()))
	})
})

var _ = Describe("log validation", func() {
	It("checks nested trace contexts", func() {
		record := validRecord()
		record.TraceContext.TraceId = strings.Repeat("A", 32)
		err := telemetry.ValidateLogRecord(record)
		Expect(errors.Is(err, telemetry.ErrInvalidLogRecord)).To(BeTrue())
		Expect(errors.Is(err, telemetry.ErrInvalidTraceContext)).To(BeTrue())
	})

	DescribeTable("bounds structured attributes",
		func(attributes map[string]string) {
			record := validRecord()
			record.Attributes = attributes
			Expect(errors.Is(telemetry.ValidateLogRecord(record), telemetry.ErrInvalidLogRecord)).To(BeTrue())
		},
		Entry("invalid key", map[string]string{"request id": "123"}),
		Entry("key too long", map[string]string{strings.Repeat("k", telemetry.MaxAttributeKeyBytes+1): "123"}),
		Entry("oversize value", map[string]string{"request_id": strings.Repeat("x", telemetry.MaxAttributeValueBytes+1)}),
		Entry("oversize total", largeAttributes(9, telemetry.MaxAttributeValueBytes)),
		Entry("too many", largeAttributes(telemetry.MaxAttributes+1, 1)),
	)

	It("accepts generated non-zero severities and rejects unknown values", func() {
		for _, severity := range []telemetryv1.Severity{
			telemetryv1.Severity_SEVERITY_TRACE,
			telemetryv1.Severity_SEVERITY_DEBUG,
			telemetryv1.Severity_SEVERITY_INFO,
			telemetryv1.Severity_SEVERITY_WARN,
			telemetryv1.Severity_SEVERITY_ERROR,
			telemetryv1.Severity_SEVERITY_FATAL,
		} {
			record := validRecord()
			record.Severity = severity
			Expect(telemetry.ValidateLogRecord(record)).To(Succeed())
		}
		for _, severity := range []telemetryv1.Severity{telemetryv1.Severity_SEVERITY_UNSPECIFIED, 99} {
			record := validRecord()
			record.Severity = severity
			Expect(errors.Is(telemetry.ValidateLogRecord(record), telemetry.ErrInvalidLogRecord)).To(BeTrue())
		}
	})
})

var _ = Describe("JSONL logging", func() {
	It("writes stable protobuf field names and round-trips one physical line", func() {
		var output bytes.Buffer
		logger, err := telemetry.NewJSONLLogger(&output, "unused", "")
		Expect(err).NotTo(HaveOccurred())
		record := validRecord()
		record.Message = "first line\nsecond line"
		Expect(logger.WriteRecord(record)).To(Succeed())
		Expect(strings.Count(output.String(), "\n")).To(Equal(1))
		Expect(output.String()).To(ContainSubstring(`"trace_context"`))
		Expect(output.String()).To(ContainSubstring(`"trace_id"`))
		Expect(output.String()).NotTo(ContainSubstring(`"traceContext"`))
		decoded := new(telemetryv1.LogRecord)
		Expect(protojson.Unmarshal(bytes.TrimSpace(output.Bytes()), decoded)).To(Succeed())
		Expect(proto.Equal(record, decoded)).To(BeTrue())
	})

	It("adds the current trace to convenience records", func() {
		var output bytes.Buffer
		logger, err := telemetry.NewJSONLLogger(&output, "worker", "consumer")
		Expect(err).NotTo(HaveOccurred())
		trace := validTrace()
		ctx, err := telemetry.ContextWithTrace(context.Background(), trace)
		Expect(err).NotTo(HaveOccurred())
		Expect(logger.Log(ctx, telemetryv1.Severity_SEVERITY_INFO, "job.completed", "done", map[string]string{"job_id": "42"})).To(Succeed())
		decoded := new(telemetryv1.LogRecord)
		Expect(protojson.Unmarshal(bytes.TrimSpace(output.Bytes()), decoded)).To(Succeed())
		Expect(proto.Equal(decoded.GetTraceContext(), trace)).To(BeTrue())
		Expect(decoded.GetTimestamp().AsTime()).NotTo(BeZero())
	})

	It("serializes concurrent records", func() {
		var output bytes.Buffer
		logger, err := telemetry.NewJSONLLogger(&output, "worker", "pool")
		Expect(err).NotTo(HaveOccurred())
		const workers = 64
		var group sync.WaitGroup
		for worker := range workers {
			group.Add(1)
			go func() {
				defer GinkgoRecover()
				defer group.Done()
				Expect(logger.Log(
					context.Background(),
					telemetryv1.Severity_SEVERITY_DEBUG,
					fmt.Sprintf("worker.%d", worker),
					"finished",
					nil,
				)).To(Succeed())
			}()
		}
		group.Wait()
		lines := strings.Split(strings.TrimSuffix(output.String(), "\n"), "\n")
		Expect(lines).To(HaveLen(workers))
		for _, line := range lines {
			Expect(protojson.Unmarshal([]byte(line), new(telemetryv1.LogRecord))).To(Succeed())
		}
	})

	It("rejects invalid construction and records", func() {
		_, err := telemetry.NewJSONLLogger(nil, "worker", "")
		Expect(errors.Is(err, telemetry.ErrNilWriter)).To(BeTrue())
		var output bytes.Buffer
		logger, err := telemetry.NewJSONLLogger(&output, "worker", "")
		Expect(err).NotTo(HaveOccurred())
		Expect(errors.Is(logger.WriteRecord(nil), telemetry.ErrInvalidLogRecord)).To(BeTrue())
		Expect(errors.Is(logger.Log(nil, telemetryv1.Severity_SEVERITY_INFO, "event", "", nil), telemetry.ErrNilContext)).To(BeTrue())
	})
})
