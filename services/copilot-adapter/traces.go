package copilotadapter

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	resourcepb "go.opentelemetry.io/proto/otlp/resource/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"google.golang.org/protobuf/proto"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	copilotv1 "github.com/candacelabs/csf/services/copilot-adapter/proto/candace/copilot/v1"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

const (
	traceAttrGenAiUsageInputTokens                             = "gen_ai.usage.input_tokens"
	traceAttrGenAiUsageOutputTokens                            = "gen_ai.usage.output_tokens"
	traceAttrLangfuseObservationInput                          = "langfuse.observation.input"
	traceAttrLangfuseObservationMetadataApiDurationMs          = "langfuse.observation.metadata.api_duration_ms"
	traceAttrLangfuseObservationMetadataBillingMultiplier      = "langfuse.observation.metadata.billing_multiplier"
	traceAttrLangfuseObservationMetadataCacheReadTokens        = "langfuse.observation.metadata.cache_read_tokens"
	traceAttrLangfuseObservationMetadataCacheWriteTokens       = "langfuse.observation.metadata.cache_write_tokens"
	traceAttrLangfuseObservationMetadataExecutionDurationKnown = "langfuse.observation.metadata.execution_duration_known"
	traceAttrLangfuseObservationMetadataNanoAiu                = "langfuse.observation.metadata.nano_aiu"
	traceAttrLangfuseObservationMetadataPremiumRequests        = "langfuse.observation.metadata.premium_requests"
	traceAttrLangfuseObservationMetadataProviderEvent          = "langfuse.observation.metadata.provider_event"
	traceAttrLangfuseObservationMetadataQueuedAt               = "langfuse.observation.metadata.queued_at"
	traceAttrLangfuseObservationMetadataReasoningTokens        = "langfuse.observation.metadata.reasoning_tokens"
	traceAttrLangfuseObservationMetadataSessionId              = "langfuse.observation.metadata.session_id"
	traceAttrLangfuseObservationMetadataSourceDeliveryId       = "langfuse.observation.metadata.source_delivery_id"
	traceAttrLangfuseObservationMetadataStatus                 = "langfuse.observation.metadata.status"
	traceAttrLangfuseObservationMetadataTurnId                 = "langfuse.observation.metadata.turn_id"
	traceAttrLangfuseObservationMetadataUsageKind              = "langfuse.observation.metadata.usage_kind"
	traceAttrLangfuseObservationMetadataWorkbenchUrl           = "langfuse.observation.metadata.workbench_url"
	traceAttrLangfuseObservationModelName                      = "langfuse.observation.model.name"
	traceAttrLangfuseObservationOutput                         = "langfuse.observation.output"
	traceAttrLangfuseObservationType                           = "langfuse.observation.type"
	traceAttrLangfuseTraceName                                 = "langfuse.trace.name"
	traceAttrServiceName                                       = "service.name"
	traceAttrSessionId                                         = "session.id"
	traceSchemeHTTP                                            = "http"
	traceSchemeHTTPS                                           = "https"
	traceTypeSpan                                              = "span"
	traceTypeEvent                                             = "event"
	traceTypeGeneration                                        = "generation"

	traceNameTurn            = "copilot.turn"
	traceNameUsage           = "copilot.provider_usage"
	traceDomainTurn          = "csf.turn.v1\x00"
	traceDomainSession       = "csf.session.v1\x00"
	spanDomain               = "csf.observation.v1\x00"
	traceAuthorizationHeader = "Authorization"
	traceIngestionHeader     = "x-langfuse-ingestion-version"
	traceIngestionVersion    = "4"
	traceServiceName         = "candace.csf.workbench"
)

// TraceExporter is one caller-owned goroutine over the existing SQL outbox.
// The official OTLP client owns the wire protocol; SQL owns retry and fencing.
type TraceExporter struct {
	queries storedb.Querier
	config  *copilotv1.TraceExportConfig
	client  otlptrace.Client
	started atomic.Bool
	cancel  context.CancelFunc
	done    chan struct{}
}

type TraceOption func(exporter *TraceExporter)

func WithTraceClient(client otlptrace.Client) TraceOption {
	return func(exporter *TraceExporter) { exporter.client = client }
}

func NewTraceExporter(queries storedb.Querier, config *copilotv1.TraceExportConfig, options ...TraceOption) (*TraceExporter, error) {
	if queries == nil || config == nil {
		return nil, fmt.Errorf("trace exporter requires store and configuration")
	}
	if err := copilotv1.ValidateTraceExportConfig(config); err != nil {
		return nil, err
	}
	endpoint, err := url.Parse(config.GetEndpointUrl())
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != traceSchemeHTTP && endpoint.Scheme != traceSchemeHTTPS) || endpoint.User != nil {
		return nil, fmt.Errorf("trace endpoint must be an HTTP URL without embedded credentials")
	}
	exporter := &TraceExporter{queries: queries, config: proto.Clone(config).(*copilotv1.TraceExportConfig), done: make(chan struct{})}
	for _, option := range options {
		if option != nil {
			option(exporter)
		}
	}
	if exporter.client == nil {
		exporter.client = otlptracehttp.NewClient(
			otlptracehttp.WithEndpointURL(config.GetEndpointUrl()),
			otlptracehttp.WithHeaders(map[string]string{traceAuthorizationHeader: "Basic " + base64.StdEncoding.EncodeToString([]byte(config.GetPublicKey()+":"+config.GetSecretKey())), traceIngestionHeader: traceIngestionVersion}),
			otlptracehttp.WithTimeout(time.Duration(config.GetRequestTimeoutMillis())*time.Millisecond),
			otlptracehttp.WithRetry(otlptracehttp.RetryConfig{Enabled: false}),
			otlptracehttp.WithMaxRequestSize(int(config.GetMaxRequestBytes())),
		)
	}
	return exporter, nil
}

// Start is explicit: constructing a capability never starts background work.
func (exporter *TraceExporter) Start(ctx context.Context) error {
	if !exporter.started.CompareAndSwap(false, true) {
		return fmt.Errorf("trace exporter already started")
	}
	if err := exporter.client.Start(ctx); err != nil {
		exporter.started.Store(false)
		return err
	}
	workerContext, cancel := context.WithCancel(ctx)
	exporter.cancel = cancel
	go func() {
		defer close(exporter.done)
		defer func() {
			stopContext, stop := context.WithTimeout(context.Background(), time.Duration(exporter.config.GetRequestTimeoutMillis())*time.Millisecond)
			defer stop()
			if err := exporter.client.Stop(stopContext); err != nil {
				slog.Warn("stop trace transport", "error", err)
			}
		}()
		ticker := time.NewTicker(time.Duration(exporter.config.GetPollMillis()) * time.Millisecond)
		defer ticker.Stop()
		for {
			worked, err := exporter.DeliverNext(workerContext)
			if err != nil && workerContext.Err() == nil {
				slog.Warn("deliver retained trace", "error", err)
			}
			if worked && err == nil {
				continue
			}
			select {
			case <-workerContext.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	return nil
}

func (exporter *TraceExporter) Close(ctx context.Context) error {
	if exporter.cancel == nil {
		return nil
	}
	exporter.cancel()
	select {
	case <-exporter.done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// DeliverNext acknowledges only after the official client confirms acceptance.
// A shutdown or lost acknowledgement leaves the lease recoverable; stable IDs
// make a replay refer to the same observation, not a new model invocation.
func (exporter *TraceExporter) DeliverNext(ctx context.Context) (bool, error) {
	deadline, cancel := context.WithTimeout(ctx, time.Duration(exporter.config.GetRequestTimeoutMillis())*time.Millisecond)
	defer cancel()
	delivery, err := exporter.queries.ClaimTraceDelivery(deadline, storedb.ClaimTraceDeliveryParams{LeaseSeconds: exporter.config.GetLeaseSeconds(), MaxAttempts: exporter.config.GetMaxAttempts()})
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	payload, err := exporter.observation(deadline, views.TraceDelivery(delivery))
	if err == nil {
		err = exporter.client.UploadTraces(deadline, []*tracepb.ResourceSpans{payload})
	}
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	settleContext, stop := context.WithTimeout(ctx, time.Duration(exporter.config.GetRequestTimeoutMillis())*time.Millisecond)
	defer stop()
	if err != nil {
		_, persistErr := exporter.queries.FailTraceDelivery(settleContext, storedb.FailTraceDeliveryParams{
			DeliveryID: delivery.DeliveryID, Generation: delivery.Generation, MaxAttempts: exporter.config.GetMaxAttempts(),
			RetryBaseSeconds: exporter.config.GetRetryBaseSeconds(), RetryMaxSeconds: exporter.config.GetRetryMaxSeconds(), LastError: err.Error(),
		})
		return true, errors.Join(err, persistErr)
	}
	_, err = exporter.queries.CompleteTraceDelivery(settleContext, storedb.CompleteTraceDeliveryParams{DeliveryID: delivery.DeliveryID, Generation: delivery.Generation})
	return true, err
}

func (exporter *TraceExporter) observation(ctx context.Context, delivery storedb.TraceDelivery) (*tracepb.ResourceSpans, error) {
	traceKey := traceDomainSession + delivery.SessionID.String()
	span := &tracepb.Span{Name: traceNameUsage, Kind: tracepb.Span_SPAN_KIND_INTERNAL}
	spanKey := sha256.Sum256([]byte(spanDomain + delivery.DeliveryID))
	span.SpanId = spanKey[:8]
	span.Attributes = []*commonpb.KeyValue{
		traceString(traceAttrSessionId, delivery.SessionID.String()), traceString(traceAttrLangfuseTraceName, traceServiceName),
		traceString(traceAttrLangfuseObservationMetadataSessionId, delivery.SessionID.String()),
		traceString(traceAttrLangfuseObservationMetadataSourceDeliveryId, delivery.DeliveryID),
		traceString(traceAttrLangfuseObservationMetadataWorkbenchUrl, exporter.config.GetWorkbenchUrl()+"#/sessions/"+delivery.SessionID.String()),
	}
	if delivery.TurnID != nil {
		turn, err := exporter.queries.GetTurn(ctx, *delivery.TurnID)
		if err != nil {
			return nil, err
		}
		if !turn.CompletedAt.Valid {
			return nil, fmt.Errorf("trace source turn is not complete")
		}
		traceKey = traceDomainTurn + delivery.SessionID.String() + "\x00" + turn.ID.String()
		span.Name = traceNameTurn
		start := turn.CompletedAt.Time
		if turn.StartedAt.Valid && !turn.StartedAt.Time.After(turn.CompletedAt.Time) {
			start = turn.StartedAt.Time
		}
		span.StartTimeUnixNano, span.EndTimeUnixNano = uint64(start.UnixNano()), uint64(turn.CompletedAt.Time.UnixNano())
		span.Attributes = append(span.Attributes, traceString(traceAttrLangfuseObservationType, traceTypeSpan), traceString(traceAttrLangfuseObservationMetadataTurnId, turn.ID.String()), traceString(traceAttrLangfuseObservationMetadataStatus, turn.Status), traceString(traceAttrLangfuseObservationMetadataQueuedAt, turn.CreatedAt.Format(time.RFC3339Nano)),
			&commonpb.KeyValue{Key: traceAttrLangfuseObservationMetadataExecutionDurationKnown, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_BoolValue{BoolValue: turn.StartedAt.Valid && !turn.StartedAt.Time.After(turn.CompletedAt.Time)}}})
		if err := exporter.appendTranscript(ctx, delivery.SessionID, turn.ID, span); err != nil {
			return nil, err
		}
	} else {
		usage, err := exporter.queries.GetProviderUsageEvent(ctx, storedb.GetProviderUsageEventParams{SessionID: delivery.SessionID, EventID: delivery.UsageEventID.String})
		if err != nil {
			return nil, err
		}
		start := usage.OccurredAt
		if usage.ApiDurationMs.Valid {
			start = start.Add(-time.Duration(usage.ApiDurationMs.Int64) * time.Millisecond)
		}
		span.StartTimeUnixNano, span.EndTimeUnixNano = uint64(start.UnixNano()), uint64(usage.OccurredAt.UnixNano())
		kind := traceTypeEvent
		if usage.Kind == string(api.ModelCall) {
			kind = traceTypeGeneration
		}
		span.Attributes = append(span.Attributes, traceString(traceAttrLangfuseObservationType, kind), traceString(traceAttrLangfuseObservationMetadataUsageKind, usage.Kind))
		if usage.Model.Valid {
			span.Attributes = append(span.Attributes, traceString(traceAttrLangfuseObservationModelName, usage.Model.String))
		}
		if usage.TurnID != nil {
			traceKey = traceDomainTurn + delivery.SessionID.String() + "\x00" + usage.TurnID.String()
			parent := sha256.Sum256([]byte(spanDomain + "turn:" + usage.TurnID.String()))
			span.ParentSpanId = parent[:8]
			span.Attributes = append(span.Attributes, traceString(traceAttrLangfuseObservationMetadataTurnId, usage.TurnID.String()))
		}
		// Preserve provider input/output verbatim. The SDK does not specify
		// whether cache/reasoning counts are exclusive across providers, so retain
		// those separately rather than asking Langfuse to subtract assumed subsets.
		for _, number := range []struct {
			key   string
			value *int64
		}{
			{traceAttrGenAiUsageInputTokens, usage.InputTokens.Ptr()}, {traceAttrGenAiUsageOutputTokens, usage.OutputTokens.Ptr()},
			{traceAttrLangfuseObservationMetadataCacheReadTokens, usage.CacheReadTokens.Ptr()}, {traceAttrLangfuseObservationMetadataCacheWriteTokens, usage.CacheWriteTokens.Ptr()},
			{traceAttrLangfuseObservationMetadataReasoningTokens, usage.ReasoningTokens.Ptr()}, {traceAttrLangfuseObservationMetadataApiDurationMs, usage.ApiDurationMs.Ptr()},
		} {
			if number.value != nil {
				span.Attributes = append(span.Attributes, &commonpb.KeyValue{Key: number.key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_IntValue{IntValue: *number.value}}})
			}
		}
		for _, number := range []struct {
			key   string
			value *float64
		}{
			{traceAttrLangfuseObservationMetadataPremiumRequests, usage.PremiumRequests.Ptr()}, {traceAttrLangfuseObservationMetadataBillingMultiplier, usage.BillingMultiplier.Ptr()}, {traceAttrLangfuseObservationMetadataNanoAiu, usage.NanoAiu.Ptr()},
		} {
			if number.value != nil {
				span.Attributes = append(span.Attributes, &commonpb.KeyValue{Key: number.key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_DoubleValue{DoubleValue: *number.value}}})
			}
		}
		if len(usage.ProviderEvent) != 0 {
			span.Attributes = append(span.Attributes, traceString(traceAttrLangfuseObservationMetadataProviderEvent, string(usage.ProviderEvent)))
		}
	}
	identifier := sha256.Sum256([]byte(traceKey))
	span.TraceId = identifier[:16]
	payload := &tracepb.ResourceSpans{Resource: &resourcepb.Resource{Attributes: []*commonpb.KeyValue{traceString(traceAttrServiceName, traceServiceName)}}, ScopeSpans: []*tracepb.ScopeSpans{{Scope: &commonpb.InstrumentationScope{Name: traceServiceName}, Spans: []*tracepb.Span{span}}}}
	if int64(proto.Size(payload)) > exporter.config.GetMaxRequestBytes() {
		return nil, fmt.Errorf("retained trace exceeds configured OTLP byte limit; source remains in PostgreSQL")
	}
	return payload, nil
}

func traceString(key string, value string) *commonpb.KeyValue {
	return &commonpb.KeyValue{Key: key, Value: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: value}}}
}

func (exporter *TraceExporter) appendTranscript(ctx context.Context, sessionID uuid.UUID, turnID uuid.UUID, span *tracepb.Span) error {
	page := storedb.ListTurnTraceTranscriptParams{SessionID: sessionID, TurnID: &turnID, RowLimit: int32(DefaultAdapterConfig().GetDefaultPageLimit())}
	items := []api.TranscriptItem{}
	var retainedBytes int64
	for {
		rows, err := exporter.queries.ListTurnTraceTranscript(ctx, page)
		if err != nil {
			return err
		}
		for _, row := range rows {
			retainedBytes += int64(len(row.Body))
			if retainedBytes > exporter.config.GetMaxRequestBytes() {
				return fmt.Errorf("transcript exceeds configured OTLP byte limit; full payload remains in PostgreSQL")
			}
			items = append(items, views.TranscriptItem(row))
		}
		if len(rows) < int(page.RowLimit) {
			break
		}
		page.AfterSeq = rows[len(rows)-1].Seq
	}
	input, output := []api.TranscriptItem{}, []api.TranscriptItem{}
	for _, item := range items {
		if item.Kind == api.TranscriptItemKindUserMessage {
			input = append(input, item)
		} else {
			output = append(output, item)
		}
	}
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return err
	}
	outputJSON, err := json.Marshal(output)
	if err != nil {
		return err
	}
	span.Attributes = append(span.Attributes, traceString(traceAttrLangfuseObservationInput, string(inputJSON)), traceString(traceAttrLangfuseObservationOutput, string(outputJSON)))
	return nil
}
