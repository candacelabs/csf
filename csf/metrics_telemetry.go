package csf

import (
	"context"
	"time"

	"github.com/candacelabs/csf/pkg/core"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

const (
	metricTelemetryReadable           = "telemetry_readable"
	metricRetainedTurns               = "retained_turns"
	metricTimedTurns                  = "timed_turns"
	metricRetainedTurnDurationSeconds = "retained_turn_duration_seconds"
	metricLatestTurnDurationSeconds   = "latest_turn_duration_seconds"
	metricProviderUsageEvents         = "provider_usage_events"
	metricProviderTokens              = "provider_tokens"
	metricProviderPremiumRequests     = "provider_premium_requests"
	metricProviderPremiumSessions     = "provider_premium_sessions"
	metricProviderUsageUnattributed   = "provider_usage_unattributed"
	metricTraceDeliveries             = "trace_deliveries"
)

// Every series is a gauge over retained PostgreSQL facts, not a process counter.
var telemetryMetricDefinitions = []struct {
	name, help string
	labels     []string
}{
	{metricTelemetryReadable, "One when a complete Workbench telemetry snapshot was read; zero otherwise.", nil},
	{metricRetainedTurns, "Retained Workbench turns including turns without known start time.", nil},
	{metricTimedTurns, "Retained turns with observed valid provider start and completion times.", nil},
	{metricRetainedTurnDurationSeconds, "Sum of known execution durations; queue time excluded. Interpret with timed_turns coverage.", nil},
	{metricLatestTurnDurationSeconds, "Most recent eligible session latest-turn execution duration; absent when unknown.", nil},
	{metricProviderUsageEvents, "Retained incremental provider model-call usage events.", nil},
	{metricProviderTokens, "Retained provider-reported token fields by kind; unknown fields absent, cache and reasoning may be subsets.", []string{"kind"}},
	{metricProviderPremiumRequests, "Sum of latest known cumulative provider premium checkpoints per session; not currency.", nil},
	{metricProviderPremiumSessions, "Sessions with a known provider premium checkpoint.", nil},
	{metricProviderUsageUnattributed, "Retained model-call measurements lacking an exact turn identity.", nil},
	{metricTraceDeliveries, "Durable trace deliveries by state. Accepted means remote ingestion acknowledgement, not indexed visibility.", []string{"state"}},
}

func WithInspectionTelemetry(source func(ctx context.Context) (api.TelemetrySnapshot, error)) InspectionOption {
	return func(inspection *Inspection) { inspection.telemetry = source }
}

func (inspection *Inspection) collectTelemetry(emit func(name string, value float64, labels ...string)) {
	if inspection.telemetry == nil {
		emit(metricTelemetryReadable, 0)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	snapshot, err := inspection.telemetry(ctx)
	emit(metricTelemetryReadable, core.BoolToFloat64(err == nil))
	if err != nil {
		return
	}
	var turns, timed, calls, unattributed, premiumSessions int64
	var duration, premium float64
	tokens := make(map[string]int64)
	var latestAt time.Time
	var latestDuration float64
	for _, session := range snapshot.Sessions {
		turns += session.TurnCount
		timed += session.DurationKnownTurnCount
		calls += session.ObservedModelCallCount
		unattributed += session.UncorrelatedUsageCount
		if session.DurationSeconds != nil {
			duration += *session.DurationSeconds
		}
		if session.PremiumRequests != nil {
			premium += *session.PremiumRequests
			premiumSessions++
		}
		if session.LatestStartedAt != nil && session.LatestCompletedAt != nil && !session.LatestCompletedAt.Before(*session.LatestStartedAt) && session.LatestCompletedAt.After(latestAt) {
			latestAt, latestDuration = *session.LatestCompletedAt, session.LatestCompletedAt.Sub(*session.LatestStartedAt).Seconds()
		}
		for _, field := range []struct {
			kind  string
			value *int64
		}{
			{"input", session.InputTokens}, {"output", session.OutputTokens}, {"cache_read", session.CacheReadTokens}, {"cache_write", session.CacheWriteTokens}, {"reasoning", session.ReasoningTokens},
		} {
			if field.value != nil {
				tokens[field.kind] += *field.value
			}
		}
	}
	emit(metricRetainedTurns, float64(turns))
	emit(metricTimedTurns, float64(timed))
	emit(metricRetainedTurnDurationSeconds, duration)
	if !latestAt.IsZero() {
		emit(metricLatestTurnDurationSeconds, latestDuration)
	}
	emit(metricProviderUsageEvents, float64(calls))
	emit(metricProviderUsageUnattributed, float64(unattributed))
	emit(metricProviderPremiumSessions, float64(premiumSessions))
	if premiumSessions > 0 {
		emit(metricProviderPremiumRequests, premium)
	}
	for kind, value := range tokens {
		emit(metricProviderTokens, float64(value), kind)
	}
	for _, trace := range snapshot.Traces {
		emit(metricTraceDeliveries, float64(trace.Count), string(trace.State))
	}
}
