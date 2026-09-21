//go:build integration

package copilotadapter_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	_ "github.com/jackc/pgx/v5/stdlib"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"github.com/onsi/gomega/gstruct"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	tracepb "go.opentelemetry.io/proto/otlp/trace/v1"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/candacelabs/csf/pkg/cron"
	"github.com/candacelabs/csf/pkg/patience"

	"github.com/candacelabs/csf/pkg/httpserver"
	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
	copilotv1 "github.com/candacelabs/csf/services/copilot-adapter/proto/candace/copilot/v1"
	"github.com/candacelabs/csf/services/copilot-adapter/store"
	"github.com/candacelabs/csf/services/copilot-adapter/storedb"
)

var _ = Describe("PostgreSQL telemetry", func() {
	It("projects bridge facts through HTTP and delivers retained payloads with fenced acknowledgements", func(ctx SpecContext) {
		// This fixed endpoint is the disposable CI database's shared container
		// network namespace. No environment override can select a live database.
		const fixtureDSN = "postgres://telemetry-test:telemetry-test@localhost:5432/telemetry-test?sslmode=disable"
		projectionBudget := patience.Budget{Within: 30 * time.Second, Interval: 25 * time.Millisecond}
		db, err := sql.Open("pgx", fixtureDSN)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(db.Close)
		Expect(patience.Await(GinkgoTB(), "disposable PostgreSQL readiness", projectionBudget,
			func() error { return db.PingContext(ctx) }, func(err error) bool { return err == nil })).To(Succeed())
		Expect(store.ApplyMigrations(ctx, db)).To(Succeed())
		persistence, err := store.NewPostgresStore(db)
		Expect(err).NotTo(HaveOccurred())
		queries := persistence.Queries

		controller := gomock.NewController(GinkgoT())
		bridge, worktrees, terminals := NewMockICopilotBridge(controller), NewMockIWorktreeManager(controller), NewMockITerminalManager(controller)
		terminals.EXPECT().Close().Return(nil)
		prepared := copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{ID: "fixture", Root: "/fixture", DefaultRef: "HEAD"}, Path: "/fixture/worktree", BaseRef: "HEAD",
		}
		worktrees.EXPECT().Prepare(gomock.Any(), gomock.Any()).Return(prepared, nil)
		worktrees.EXPECT().Reuse(gomock.Any(), "fixture", "/fixture/worktree").Return(prepared, nil).AnyTimes()
		events := make(chan copilotadapter.BridgeEvent, 32)
		sent := make(chan copilotadapter.BridgePrompt, 1)
		var active atomic.Pointer[uuid.UUID]
		bridge.EXPECT().CreateSession(gomock.Any(), gomock.Any()).Return(copilotadapter.BridgeSession{
			Events: events,
			Send: func(_ context.Context, prompt copilotadapter.BridgePrompt) (copilotadapter.BridgePromptDelivery, error) {
				active.Store(&prompt.TurnID)
				sent <- prompt
				return copilotadapter.BridgePromptDeliveryAccepted, nil
			},
			ActiveTurn: func() (uuid.UUID, bool) {
				if turnID := active.Load(); turnID != nil {
					return *turnID, true
				}
				return uuid.Nil, false
			},
			Close: func(_ context.Context) error { return nil },
		}, nil)
		adapter, err := copilotadapter.NewCopilotAdapter(copilotadapter.WithBridge(bridge), copilotadapter.WithStore(persistence),
			copilotadapter.WithWorktreeManager(worktrees), copilotadapter.WithTerminalManager(terminals), copilotadapter.WithScheduleStore(cron.NewMemoryStore()))
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(adapter.Close)
		engine := httpserver.NewEngine("telemetry-postgres-fixture")
		Expect(adapter.Register(engine)).To(Succeed())
		server := httptest.NewServer(engine)
		DeferCleanup(server.Close)
		client, err := api.NewClientWithResponses(server.URL)
		Expect(err).NotTo(HaveOccurred())
		var body api.CreateSessionJSONRequestBody
		Expect(body.FromNewWorktreeSessionRequest(api.NewWorktreeSessionRequest{IdempotencyKey: uuid.New(), Model: "fixture-model", RepositoryId: "fixture"})).To(Succeed())
		created, err := client.CreateSessionWithResponse(ctx, body)
		Expect(err).NotTo(HaveOccurred())
		Expect(created.StatusCode()).To(Equal(http.StatusCreated), string(created.Body))
		Expect(created.JSON201).NotTo(BeNil())
		sessionID := *created.JSON201.Id
		readTelemetry := func() api.SessionTelemetry {
			response, err := client.GetTelemetryWithResponse(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(response.StatusCode()).To(Equal(http.StatusOK), string(response.Body))
			Expect(response.JSON200).NotTo(BeNil())
			Expect(response.JSON200.Sessions).To(HaveLen(1))
			return response.JSON200.Sessions[0]
		}
		empty := readTelemetry()
		Expect(empty.TurnCount).To(BeZero())
		Expect(empty.LatestTurnId).To(BeNil())
		Expect(empty.LatestTurnStatus).To(BeNil())
		Expect(empty.DurationSeconds).To(BeNil())
		Expect(empty.InputTokens).To(BeNil())
		Expect(empty.OutputTokens).To(BeNil())
		Expect(empty.PremiumRequests).To(BeNil())
		Expect(empty.LatestUsageAt).To(BeNil())

		prompted, err := client.SubmitPromptWithResponse(ctx, sessionID, api.SubmitPromptJSONRequestBody{IdempotencyKey: uuid.New(), Text: "retained prompt", Mode: api.Queue})
		Expect(err).NotTo(HaveOccurred())
		Expect(prompted.StatusCode()).To(Equal(http.StatusAccepted), string(prompted.Body))
		var prompt copilotadapter.BridgePrompt
		Eventually(sent).WithTimeout(projectionBudget.Within).Should(Receive(&prompt))
		start := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
		started := copilotadapter.BridgeEvent{ID: "start", Kind: copilotadapter.BridgeEventTurnStarted, TurnID: &prompt.TurnID, OccurredAt: start}
		events <- started
		events <- started
		unknown := copilotadapter.BridgeEvent{ID: "usage-unknown", Kind: copilotadapter.BridgeEventUsage, TurnID: &prompt.TurnID, OccurredAt: start.Add(time.Second),
			Usage: &api.UsageObservation{Kind: api.ModelCall}, UsagePayload: json.RawMessage(`{"id":"usage-unknown","type":"assistant.usage","data":{}}`)}
		events <- unknown
		observedUnknown := patience.Await(GinkgoTB(), "unknown provider observation", projectionBudget, readTelemetry,
			func(row api.SessionTelemetry) bool { return row.ObservedModelCallCount == 1 })
		Expect(observedUnknown.KnownStartedTurnCount).To(Equal(int64(1)))
		Expect(observedUnknown.LatestStartedAt).To(gstruct.PointTo(BeTemporally("==", start)))
		Expect(observedUnknown.DurationSeconds).To(BeNil())
		Expect(observedUnknown.InputTokens).To(BeNil())
		Expect(observedUnknown.OutputTokens).To(BeNil())
		zeroCount, multiplier := int64(0), float64(2)
		zero := copilotadapter.BridgeEvent{ID: "usage-zero", Kind: copilotadapter.BridgeEventUsage, TurnID: &prompt.TurnID, OccurredAt: start.Add(2 * time.Second),
			Usage:        &api.UsageObservation{Kind: api.ModelCall, InputTokens: &zeroCount, OutputTokens: &zeroCount, CacheWriteTokens: &zeroCount, BillingMultiplier: &multiplier},
			UsagePayload: json.RawMessage(`{"id":"usage-zero","type":"assistant.usage","data":{"inputTokens":0,"outputTokens":0,"cost":2}}`)}
		events <- zero
		observedZero := patience.Await(GinkgoTB(), "measured zero provider observation", projectionBudget, readTelemetry,
			func(row api.SessionTelemetry) bool { return row.ObservedModelCallCount == 2 })
		Expect(observedZero.InputTokens).To(gstruct.PointTo(BeZero()))
		Expect(observedZero.OutputTokens).To(gstruct.PointTo(BeZero()))
		Expect(observedZero.PremiumRequests).To(BeNil())
		events <- copilotadapter.BridgeEvent{ID: "reply", Kind: copilotadapter.BridgeEventAssistantMessage, TurnID: &prompt.TurnID, OccurredAt: start.Add(3 * time.Second), Text: "retained reply"}
		events <- copilotadapter.BridgeEvent{ID: "tool-call", Kind: copilotadapter.BridgeEventToolCall, TurnID: &prompt.TurnID, OccurredAt: start.Add(3 * time.Second), ToolName: "fixture-tool", ToolCallID: "fixture-call", Text: `{"command":"printf fixture"}`}
		events <- copilotadapter.BridgeEvent{ID: "tool-result", Kind: copilotadapter.BridgeEventToolResult, TurnID: &prompt.TurnID, OccurredAt: start.Add(3 * time.Second), ToolName: "fixture-tool", ToolCallID: "fixture-call", Text: "retained tool result"}
		completed := start.Add(4 * time.Second)
		events <- copilotadapter.BridgeEvent{ID: "completed", Kind: copilotadapter.BridgeEventTurnCompleted, TurnID: &prompt.TurnID, OccurredAt: completed}
		patience.Await(GinkgoTB(), "completed turn projection", projectionBudget, readTelemetry,
			func(row api.SessionTelemetry) bool { return row.KnownCompletedTurnCount == 1 })
		input, output, cacheRead, duration := int64(11), int64(7), int64(4), int64(1200)
		late := copilotadapter.BridgeEvent{ID: "usage-late", Kind: copilotadapter.BridgeEventUsage, OccurredAt: start.Add(5 * time.Second),
			Usage:        &api.UsageObservation{Kind: api.ModelCall, InputTokens: &input, OutputTokens: &output, CacheReadTokens: &cacheRead, ApiDurationMs: &duration},
			UsagePayload: json.RawMessage(`{"id":"usage-late","type":"assistant.usage","data":{"inputTokens":11,"outputTokens":7,"cacheReadTokens":4,"duration":1200}}`)}
		premiumOld, premiumNew := float64(2), float64(3)
		checkpoints := []copilotadapter.BridgeEvent{
			{ID: "checkpoint-old", Kind: copilotadapter.BridgeEventUsage, OccurredAt: start.Add(6 * time.Second), Usage: &api.UsageObservation{Kind: api.SessionCheckpoint, PremiumRequests: &premiumOld}, UsagePayload: json.RawMessage(`{"id":"checkpoint-old","type":"session.usage_info","data":{"totalPremiumRequests":2}}`)},
			{ID: "checkpoint-new", Kind: copilotadapter.BridgeEventUsage, OccurredAt: start.Add(7 * time.Second), Usage: &api.UsageObservation{Kind: api.SessionCheckpoint, PremiumRequests: &premiumNew}, UsagePayload: json.RawMessage(`{"id":"checkpoint-new","type":"session.usage_info","data":{"totalPremiumRequests":3}}`)},
			{ID: "checkpoint-unknown", Kind: copilotadapter.BridgeEventUsage, OccurredAt: start.Add(8 * time.Second), Usage: &api.UsageObservation{Kind: api.SessionCheckpoint}, UsagePayload: json.RawMessage(`{"id":"checkpoint-unknown","type":"session.shutdown","data":{}}`)},
		}
		events <- late
		for _, replay := range []copilotadapter.BridgeEvent{unknown, zero, late, checkpoints[0], checkpoints[1], checkpoints[0], checkpoints[1]} {
			events <- replay
		}
		conflicting := zero
		conflicting.UsagePayload = json.RawMessage(`{"id":"usage-zero","data":{"inputTokens":99}}`)
		events <- conflicting
		events <- checkpoints[2]
		latest := checkpoints[2].OccurredAt
		totals := patience.Await(GinkgoTB(), "late usage and checkpoint replay", projectionBudget, readTelemetry,
			func(row api.SessionTelemetry) bool {
				return row.LatestUsageAt != nil && row.LatestUsageAt.Equal(latest)
			})
		Expect(totals.TurnCount).To(Equal(int64(1)))
		Expect(totals.KnownStartedTurnCount).To(Equal(int64(1)))
		Expect(totals.KnownCompletedTurnCount).To(Equal(int64(1)))
		Expect(totals.DurationKnownTurnCount).To(Equal(int64(1)))
		Expect(totals.DurationSeconds).To(gstruct.PointTo(Equal(float64(4))))
		Expect(totals.LatestTurnId).To(gstruct.PointTo(Equal(prompt.TurnID)))
		Expect(totals.LatestCompletedAt).To(gstruct.PointTo(BeTemporally("==", completed)))
		Expect(totals.LatestTurnStatus).To(gstruct.PointTo(Equal(string(api.TurnStatusCompleted))))
		Expect(totals.ObservedModelCallCount).To(Equal(int64(3)))
		Expect(totals.UncorrelatedUsageCount).To(Equal(int64(1)))
		Expect(totals.InputTokens).To(gstruct.PointTo(Equal(input)))
		Expect(totals.OutputTokens).To(gstruct.PointTo(Equal(output)))
		Expect(totals.CacheReadTokens).To(gstruct.PointTo(Equal(cacheRead)))
		Expect(totals.CacheWriteTokens).To(gstruct.PointTo(BeZero()))
		Expect(totals.ReasoningTokens).To(BeNil())
		Expect(totals.ApiDurationMs).To(gstruct.PointTo(Equal(duration)))
		Expect(totals.PremiumRequests).To(gstruct.PointTo(Equal(premiumNew)))
		stored, err := queries.GetProviderUsageEvent(ctx, storedb.GetProviderUsageEventParams{SessionID: sessionID, EventID: zero.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(string(stored.ProviderEvent)).To(MatchJSON(string(zero.UsagePayload)))
		Expect(stored.InputTokens.Valid).To(BeTrue())
		Expect(stored.InputTokens.Int64).To(BeZero())
		Expect(stored.BillingMultiplier.Float64).To(Equal(multiplier))
		Expect(stored.PremiumRequests.Valid).To(BeFalse())
		uncorrelated, err := queries.GetProviderUsageEvent(ctx, storedb.GetProviderUsageEventParams{SessionID: sessionID, EventID: late.ID})
		Expect(err).NotTo(HaveOccurred())
		Expect(uncorrelated.TurnID).To(BeNil())

		// Requeue one real lease. Its prior generation must not acknowledge the
		// subsequent upload; the exporter must acknowledge the new lease itself.
		firstLease, err := queries.ClaimTraceDelivery(ctx, storedb.ClaimTraceDeliveryParams{LeaseSeconds: 60, MaxAttempts: 5})
		Expect(err).NotTo(HaveOccurred())
		_, err = queries.FailTraceDelivery(ctx, storedb.FailTraceDeliveryParams{DeliveryID: firstLease.DeliveryID, Generation: firstLease.Generation, MaxAttempts: 5, RetryBaseSeconds: 1, RetryMaxSeconds: 1, LastError: "fixture retry"})
		Expect(err).NotTo(HaveOccurred())
		transport := NewMockClient(controller)
		delivered := map[string]*tracepb.Span{}
		transport.EXPECT().UploadTraces(gomock.Any(), gomock.Any()).DoAndReturn(func(uploadContext context.Context, payload []*tracepb.ResourceSpans) error {
			Expect(payload).To(HaveLen(1))
			span := payload[0].ScopeSpans[0].Spans[0]
			attributes := map[string]*commonpb.AnyValue{}
			for _, attribute := range span.Attributes {
				attributes[attribute.Key] = attribute.Value
			}
			deliveryID := attributes["langfuse.observation.metadata.source_delivery_id"].GetStringValue()
			Expect(delivered).NotTo(HaveKey(deliveryID))
			delivered[deliveryID] = span
			if deliveryID == firstLease.DeliveryID {
				_, staleErr := queries.CompleteTraceDelivery(uploadContext, storedb.CompleteTraceDeliveryParams{DeliveryID: deliveryID, Generation: firstLease.Generation})
				Expect(errors.Is(staleErr, sql.ErrNoRows)).To(BeTrue())
			}
			if deliveryID == "turn:"+prompt.TurnID.String() {
				Expect(span.EndTimeUnixNano - span.StartTimeUnixNano).To(Equal(uint64(4 * time.Second)))
				Expect(attributes["langfuse.observation.input"].GetStringValue()).To(ContainSubstring("retained prompt"))
				Expect(attributes["langfuse.observation.output"].GetStringValue()).To(And(ContainSubstring("retained reply"), ContainSubstring("retained tool result"), ContainSubstring("printf fixture")))
			}
			if deliveryID == "usage:"+sessionID.String()+":"+zero.ID {
				Expect(attributes).To(HaveKey("gen_ai.usage.input_tokens"))
				Expect(attributes["gen_ai.usage.input_tokens"].GetIntValue()).To(BeZero())
				Expect(attributes["langfuse.observation.metadata.provider_event"].GetStringValue()).To(MatchJSON(string(zero.UsagePayload)))
				Expect(attributes).NotTo(HaveKey("langfuse.observation.metadata.premium_requests"))
			}
			if deliveryID == "usage:"+sessionID.String()+":"+unknown.ID {
				Expect(attributes).NotTo(HaveKey("gen_ai.usage.input_tokens"))
			}
			return nil
		}).Times(7)
		configuration := &copilotv1.TraceExportConfig{}
		document, err := os.ReadFile("config/traces.defaults.json")
		Expect(err).NotTo(HaveOccurred())
		Expect(protojson.Unmarshal(document, configuration)).To(Succeed())
		configuration.EndpointUrl, configuration.PublicKey, configuration.SecretKey = "http://collector.invalid/v1/traces", "fixture-public", "fixture-secret"
		exporter, err := copilotadapter.NewTraceExporter(queries, configuration, copilotadapter.WithTraceClient(transport))
		Expect(err).NotTo(HaveOccurred())
		patience.Await(GinkgoTB(), "all seven retained trace deliveries accepted", projectionBudget, func() int64 {
			_, deliveryErr := exporter.DeliverNext(ctx)
			Expect(deliveryErr).NotTo(HaveOccurred())
			counts, countErr := queries.CountTraceDeliveries(ctx)
			Expect(countErr).NotTo(HaveOccurred())
			for _, count := range counts {
				if count.Status == string(api.TraceAccepted) {
					return count.Count
				}
			}
			return 0
		}, func(accepted int64) bool { return accepted == 7 })
		Expect(delivered).To(HaveLen(7))
		worked, err := exporter.DeliverNext(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(worked).To(BeFalse())
		response, err := client.GetTelemetryWithResponse(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(response.JSON200.Traces).To(ContainElement(api.TraceDeliveryCount{State: api.TraceAccepted, Count: 7}))
	}, SpecTimeout(90*time.Second))
})
