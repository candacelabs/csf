package csf

import (
	"context"
	"errors"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

var _ = Describe("retained telemetry inspection", func() {
	It("serves sums, partial timing coverage and independent provider fields as gauges on the existing route", func() {
		firstStart := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
		firstEnd := firstStart.Add(5 * time.Second)
		latestStart := firstStart.Add(time.Minute)
		latestEnd := latestStart.Add(30 * time.Second)
		activeStart := latestEnd.Add(time.Minute)
		snapshot := api.TelemetrySnapshot{Sessions: []api.SessionTelemetry{
			{TurnCount: 2, DurationKnownTurnCount: 2, DurationSeconds: proto.Float64(10),
				LatestStartedAt: &firstStart, LatestCompletedAt: &firstEnd,
				ObservedModelCallCount: 2, UncorrelatedUsageCount: 1, InputTokens: proto.Int64(100),
				OutputTokens: proto.Int64(0), CacheReadTokens: proto.Int64(40), ReasoningTokens: proto.Int64(5), PremiumRequests: proto.Float64(0)},
			{TurnCount: 3, DurationKnownTurnCount: 1, DurationSeconds: proto.Float64(30),
				LatestStartedAt: &latestStart, LatestCompletedAt: &latestEnd,
				ObservedModelCallCount: 1, InputTokens: proto.Int64(50), OutputTokens: proto.Int64(7), PremiumRequests: proto.Float64(2.5)},
			{TurnCount: 4, LatestStartedAt: &activeStart},
		}, Traces: []api.TraceDeliveryCount{
			{State: api.TracePending, Count: 3}, {State: api.TraceRunning, Count: 1},
			{State: api.TraceAccepted, Count: 7}, {State: api.TraceFailed, Count: 2},
		}}
		calls := 0
		inspection := NewInspection(WithInspectionTelemetry(func(ctx context.Context) (api.TelemetrySnapshot, error) {
			calls++
			_, bounded := ctx.Deadline()
			Expect(bounded).To(BeTrue())
			return snapshot, nil
		}))
		families := scrapeInspection(inspection)
		Expect(calls).To(Equal(1))
		expected := map[string]float64{
			"telemetry_readable": 1, "retained_turns": 9, "timed_turns": 3, "retained_turn_duration_seconds": 40,
			"latest_turn_duration_seconds": 30, "provider_usage_events": 3, "provider_usage_unattributed": 1,
			"provider_premium_requests": 2.5, "provider_premium_sessions": 2,
		}
		for name, value := range expected {
			Expect(inspectionGauges(families, name, "")).To(Equal(map[string]float64{"": value}), name)
		}
		Expect(inspectionGauges(families, "provider_tokens", "kind")).To(Equal(map[string]float64{"input": 150, "output": 7, "cache_read": 40, "reasoning": 5}))
		Expect(inspectionGauges(families, "trace_deliveries", "state")).To(Equal(map[string]float64{"pending": 3, "running": 1, "accepted": 7, "failed": 2}))
		// Provider ordering is not chronology, and a scrape must not accumulate totals.
		slices.Reverse(snapshot.Sessions)
		reordered := scrapeInspection(inspection)
		for name := range expected {
			Expect(inspectionGauges(reordered, name, "")).To(Equal(inspectionGauges(families, name, "")), name)
		}
		Expect(inspectionGauges(reordered, "provider_tokens", "kind")).To(Equal(inspectionGauges(families, "provider_tokens", "kind")))
		// Retention can remove sessions. The same collector must expose the new snapshot.
		snapshot.Sessions = nil
		retained := scrapeInspection(inspection)
		Expect(inspectionGauges(retained, "retained_turns", "")).To(Equal(map[string]float64{"": 0}))
		Expect(retained).NotTo(HaveKey(inspectionPrefix + "provider_tokens"))
		Expect(retained).NotTo(HaveKey(inspectionPrefix + "latest_turn_duration_seconds"))
	})

	It("keeps explicitly known zero usage and zero execution duration", func() {
		at := time.Date(2026, 9, 16, 10, 0, 0, 0, time.UTC)
		inspection := NewInspection(WithInspectionTelemetry(func(ctx context.Context) (api.TelemetrySnapshot, error) {
			return api.TelemetrySnapshot{Sessions: []api.SessionTelemetry{{TurnCount: 1, DurationKnownTurnCount: 1,
				DurationSeconds: proto.Float64(0), LatestStartedAt: &at, LatestCompletedAt: &at,
				InputTokens: proto.Int64(0), OutputTokens: proto.Int64(0), PremiumRequests: proto.Float64(0)}},
				Traces: []api.TraceDeliveryCount{{State: api.TracePending, Count: 0}, {State: api.TraceRunning, Count: 0},
					{State: api.TraceAccepted, Count: 0}, {State: api.TraceFailed, Count: 0}}}, nil
		}))
		families := scrapeInspection(inspection)
		for name, value := range map[string]float64{"latest_turn_duration_seconds": 0, "retained_turn_duration_seconds": 0,
			"timed_turns": 1, "provider_premium_requests": 0, "provider_premium_sessions": 1} {
			Expect(inspectionGauges(families, name, "")).To(Equal(map[string]float64{"": value}), name)
		}
		Expect(inspectionGauges(families, "provider_tokens", "kind")).To(Equal(map[string]float64{"input": 0, "output": 0}))
		Expect(inspectionGauges(families, "trace_deliveries", "state")).To(Equal(map[string]float64{"pending": 0, "running": 0, "accepted": 0, "failed": 0}))
	})

	It("does not manufacture optional measurements from retained sessions without evidence", func() {
		inspection := NewInspection(WithInspectionTelemetry(func(ctx context.Context) (api.TelemetrySnapshot, error) {
			return api.TelemetrySnapshot{Sessions: []api.SessionTelemetry{{TurnCount: 10}}}, nil
		}))
		families := scrapeInspection(inspection)
		for name, value := range map[string]float64{"telemetry_readable": 1, "retained_turns": 10, "timed_turns": 0,
			"retained_turn_duration_seconds": 0, "provider_usage_events": 0, "provider_premium_sessions": 0} {
			Expect(inspectionGauges(families, name, "")).To(Equal(map[string]float64{"": value}), name)
		}
		for _, name := range []string{"latest_turn_duration_seconds", "provider_tokens", "provider_premium_requests"} {
			Expect(families).NotTo(HaveKey(inspectionPrefix + name))
		}
	})

	DescribeTable("publishes only unreadability when the telemetry source is absent or fails", func(configured bool) {
		options := []InspectionOption{}
		if configured {
			options = append(options, WithInspectionTelemetry(func(ctx context.Context) (api.TelemetrySnapshot, error) {
				return api.TelemetrySnapshot{Sessions: []api.SessionTelemetry{{TurnCount: 99, InputTokens: proto.Int64(42)}}}, errors.New("store unavailable")
			}))
		}
		families := scrapeInspection(NewInspection(options...))
		Expect(inspectionGauges(families, "telemetry_readable", "")).To(Equal(map[string]float64{"": 0}))
		for _, definition := range telemetryMetricDefinitions {
			if definition.name != metricTelemetryReadable {
				Expect(families).NotTo(HaveKey(inspectionPrefix + definition.name))
			}
		}
	}, Entry("no provider", false), Entry("partial snapshot and an error", true))
})
