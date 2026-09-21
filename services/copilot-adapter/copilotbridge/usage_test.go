package copilotbridge

import (
	"encoding/json"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

var _ = Describe("provider usage translation", func() {
	var at time.Time
	BeforeEach(func() { at = time.Date(2026, 9, 16, 10, 0, 0, 123, time.UTC) })

	It("preserves actual model-call fields without treating billing multipliers as premium requests", func() {
		data := &rpc.AssistantUsageData{
			Model: "provider-model", APICallID: proto.String("api-call"), ProviderCallID: proto.String("provider-call"),
			ServiceRequestID: proto.String("service-request"), InputTokens: proto.Int64(120), OutputTokens: proto.Int64(32),
			CacheReadTokens: proto.Int64(80), CacheWriteTokens: proto.Int64(12), ReasoningTokens: proto.Int64(7),
			Duration: proto.Int64(1420), Cost: proto.Float64(0.33),
			CopilotUsage: &rpc.AssistantUsageCopilotUsage{TotalNanoAiu: 1250.5},
		}
		event, found := translateEvent(copilot.SessionEvent{Timestamp: at, Data: data}, nil, nil)
		Expect(found).To(BeTrue())
		Expect(event.Kind).To(Equal(copilotadapter.BridgeEventUsage))
		Expect(event.OccurredAt).To(Equal(at))
		Expect(event.TurnID).To(BeNil())
		Expect(event.Usage).To(Equal(&api.UsageObservation{
			Kind: api.ModelCall, Model: proto.String("provider-model"), ApiCallId: proto.String("api-call"),
			ProviderCallId: proto.String("provider-call"), ServiceRequestId: proto.String("service-request"),
			InputTokens: proto.Int64(120), OutputTokens: proto.Int64(32), CacheReadTokens: proto.Int64(80),
			CacheWriteTokens: proto.Int64(12), ReasoningTokens: proto.Int64(7), ApiDurationMs: proto.Int64(1420),
			BillingMultiplier: proto.Float64(0.33), NanoAiu: proto.Float64(1250.5),
		}))
	})

	It("keeps missing fields nil while retaining explicitly reported zeros", func() {
		missing, found := translateEvent(copilot.SessionEvent{Timestamp: at,
			Data: &rpc.AssistantUsageData{Model: "provider-model"}}, nil, nil)
		Expect(found).To(BeTrue())
		Expect(missing.Usage).To(Equal(&api.UsageObservation{Kind: api.ModelCall, Model: proto.String("provider-model")}))
		zero, found := translateEvent(copilot.SessionEvent{Timestamp: at, Data: &rpc.AssistantUsageData{
			Model: "provider-model", InputTokens: proto.Int64(0), OutputTokens: proto.Int64(0),
			CacheReadTokens: proto.Int64(0), CacheWriteTokens: proto.Int64(0), ReasoningTokens: proto.Int64(0),
			Duration: proto.Int64(0), Cost: proto.Float64(0), CopilotUsage: &rpc.AssistantUsageCopilotUsage{},
		}}, nil, nil)
		Expect(found).To(BeTrue())
		Expect(zero.Usage).To(Equal(&api.UsageObservation{Kind: api.ModelCall, Model: proto.String("provider-model"),
			InputTokens: proto.Int64(0), OutputTokens: proto.Int64(0), CacheReadTokens: proto.Int64(0),
			CacheWriteTokens: proto.Int64(0), ReasoningTokens: proto.Int64(0), ApiDurationMs: proto.Int64(0),
			BillingMultiplier: proto.Float64(0), NanoAiu: proto.Float64(0)}))
	})

	It("translates a durable checkpoint as cumulative session usage even when its premium field is unknown", func() {
		event, found := translateEvent(copilot.SessionEvent{Timestamp: at,
			Data: &rpc.SessionUsageCheckpointData{TotalNanoAiu: 1900, TotalPremiumRequests: proto.Float64(2.5)}}, nil, nil)
		Expect(found).To(BeTrue())
		Expect(event.Usage).To(Equal(&api.UsageObservation{Kind: api.SessionCheckpoint,
			NanoAiu: proto.Float64(1900), PremiumRequests: proto.Float64(2.5)}))
		Expect(event.TurnID).To(BeNil())
		unknown, found := translateEvent(copilot.SessionEvent{Timestamp: at,
			Data: &rpc.SessionUsageCheckpointData{}}, nil, nil)
		Expect(found).To(BeTrue())
		Expect(unknown.Usage).To(Equal(&api.UsageObservation{Kind: api.SessionCheckpoint, NanoAiu: proto.Float64(0)}))
	})

	It("retains known-zero shutdown accounting and preserves an error shutdown alongside its checkpoint", func() {
		for _, shutdownType := range []rpc.ShutdownType{rpc.ShutdownTypeRoutine, rpc.ShutdownTypeError} {
			events := translateEvents(copilot.SessionEvent{Timestamp: at, Data: &rpc.SessionShutdownData{
				ShutdownType: shutdownType, ErrorReason: proto.String("provider stopped"),
				TotalPremiumRequests: proto.Float64(0), TotalNanoAiu: proto.Float64(0),
				TotalAPIDurationMs: 4000, CurrentTokens: proto.Int64(1000),
			}}, nil, nil)
			Expect(events).To(ContainElement(And(HaveField("Kind", Equal(copilotadapter.BridgeEventUsage)),
				HaveField("Usage", Equal(&api.UsageObservation{Kind: api.SessionCheckpoint,
					PremiumRequests: proto.Float64(0), NanoAiu: proto.Float64(0)})))))
			if shutdownType == rpc.ShutdownTypeError {
				Expect(events).To(ContainElement(And(HaveField("Kind", Equal(copilotadapter.BridgeEventFailed)),
					HaveField("Text", Equal("provider stopped")))))
			} else {
				Expect(events).To(HaveLen(1))
			}
		}
	})

	DescribeTable("attributes subagent usage only through retained explicit identities", func(identity string, correlated bool) {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		Expect(turns.enqueue(firstID)).To(BeTrue())
		Expect(turns.enqueue(secondID)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-first")
		Expect(found).To(BeTrue())
		events := make(chan copilotadapter.BridgeEvent, 8)
		translator := newEventTranslator(events, turns)
		translator.handle(copilot.SessionEvent{Data: &rpc.ToolExecutionStartData{
			ToolCallID: "spawn-first", ToolName: "task", TurnID: proto.String("sdk-first"),
		}})
		translator.handle(copilot.SessionEvent{AgentID: proto.String("known-agent"),
			Data: &rpc.SubagentStartedData{ToolCallID: "spawn-first", AgentName: "research"}})
		<-events
		<-events
		_, found = startTestTurn(turns, "sdk-second")
		Expect(found).To(BeTrue())
		source := copilot.SessionEvent{ID: "late-usage", Timestamp: at,
			Data: &rpc.AssistantUsageData{Model: "provider-model", InputTokens: proto.Int64(42)}}
		switch identity {
		case "known-tool":
			source.Data.(*rpc.AssistantUsageData).ParentToolCallID = proto.String("spawn-first")
		case "known-agent", "unknown-agent":
			source.AgentID = &identity
		case "unknown-tool":
			source.Data.(*rpc.AssistantUsageData).ParentToolCallID = proto.String("missing-tool")
		}
		translator.handle(source)
		usage := <-events
		Expect(usage.Kind).To(Equal(copilotadapter.BridgeEventUsage))
		if correlated {
			Expect(usage.TurnID).To(HaveValue(Equal(firstID)))
		} else {
			Expect(usage.TurnID).To(BeNil(), "the currently running second turn does not establish billing provenance")
		}
		active, found := turns.active()
		Expect(found).To(BeTrue())
		Expect(active).To(Equal(secondID))
	}, Entry("known parent tool", "known-tool", true), Entry("known subagent", "known-agent", true),
		Entry("unknown parent tool", "unknown-tool", false), Entry("unknown subagent", "unknown-agent", false),
		Entry("root usage without an explicit identity", "root", false))

	It("retains late usage after idle without guessing a turn and preserves the source payload for durable accounting", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		Expect(turns.enqueue(identifier)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-finished")
		Expect(found).To(BeTrue())
		events := make(chan copilotadapter.BridgeEvent, 4)
		translator := newEventTranslator(events, turns)
		translator.handle(copilot.SessionEvent{Data: &rpc.SessionIdleData{}})
		Expect((<-events).Kind).To(Equal(copilotadapter.BridgeEventTurnCompleted))
		source := copilot.SessionEvent{ID: "usage-after-idle", Timestamp: at,
			Data: &rpc.AssistantUsageData{Model: "provider-model", InputTokens: proto.Int64(23),
				OutputTokens: proto.Int64(0), FinishReason: proto.String("stop")}}
		translator.handle(source)
		usage := <-events
		Expect(usage.ID).To(Equal(source.ID))
		Expect(usage.OccurredAt).To(Equal(at))
		Expect(usage.Kind).To(Equal(copilotadapter.BridgeEventUsage))
		Expect(usage.TurnID).To(BeNil())
		Expect(usage.Usage.InputTokens).To(HaveValue(Equal(int64(23))))
		raw, err := json.Marshal(source)
		Expect(err).NotTo(HaveOccurred())
		Expect(usage.UsagePayload).To(MatchJSON(raw))
		translator.handle(source)
		Expect(events).To(HaveLen(0), "replayed usage must not double the retained observation")
	})

	It("accepts one late ephemeral usage after the durable head advances", func() {
		events := make(chan copilotadapter.BridgeEvent, 2)
		translator := newEventTranslator(events, nil)
		rootID, firstID, successorID := "root", "turn-one", "turn-two"
		translator.handle(copilot.SessionEvent{ID: rootID, Data: &rpc.SessionInfoData{InfoType: "notification"}})
		translator.handle(copilot.SessionEvent{ID: firstID, ParentID: &rootID, Data: &rpc.SessionInfoData{InfoType: "notification"}})
		translator.handle(copilot.SessionEvent{ID: successorID, ParentID: &firstID, Data: &rpc.SessionInfoData{InfoType: "notification"}})
		ephemeral := true
		late := copilot.SessionEvent{ID: "late-usage", ParentID: &firstID, Ephemeral: &ephemeral,
			Data: &rpc.AssistantUsageData{Model: "provider-model", InputTokens: proto.Int64(23)}}
		translator.handle(late)
		Expect(events).To(HaveLen(1))
		lateBridgeEvent := <-events
		Expect(lateBridgeEvent.Kind).To(Equal(copilotadapter.BridgeEventUsage))
		Expect(lateBridgeEvent.TurnID).To(BeNil())
		translator.handle(late)
		Expect(events).To(HaveLen(0), "usage ID replay must be suppressed independently")
		stale := copilot.SessionEvent{ID: "stale-control", ParentID: &firstID,
			Data: &rpc.SessionInfoData{InfoType: "notification", Message: "stale"}}
		translator.handle(stale)
		Expect(translator.eventIDs.head).To(Equal(successorID), "late usage must not reopen the old control branch")
		next := copilot.SessionEvent{ID: "next-control", ParentID: &successorID,
			Data: &rpc.SessionInfoData{InfoType: "notification", Message: "next"}}
		translator.handle(next)
		Expect(translator.eventIDs.head).To(Equal("next-control"))
	})

	It("preserves an ephemeral usage alias for its durable successor", func() {
		events := make(chan copilotadapter.BridgeEvent, 2)
		translator := newEventTranslator(events, nil)
		rootID, headID := "root", "head"
		translator.handle(copilot.SessionEvent{ID: rootID, Data: &rpc.SessionInfoData{InfoType: "notification"}})
		translator.handle(copilot.SessionEvent{ID: headID, ParentID: &rootID, Data: &rpc.SessionInfoData{InfoType: "notification"}})
		ephemeral := true
		usage := copilot.SessionEvent{ID: "current-usage", ParentID: &headID, Ephemeral: &ephemeral,
			Data: &rpc.AssistantUsageData{Model: "provider-model", InputTokens: proto.Int64(7)}}
		translator.handle(usage)
		Expect((<-events).Kind).To(Equal(copilotadapter.BridgeEventUsage))
		next := copilot.SessionEvent{ID: "usage-successor", ParentID: &usage.ID,
			Data: &rpc.SessionInfoData{InfoType: "notification"}}
		translator.handle(next)
		Expect(translator.eventIDs.head).To(Equal(next.ID))
	})

	It("preserves a durable usage checkpoint for its durable successor", func() {
		events := make(chan copilotadapter.BridgeEvent, 2)
		translator := newEventTranslator(events, nil)
		rootID, headID := "root", "head"
		translator.handle(copilot.SessionEvent{ID: rootID, Data: &rpc.SessionInfoData{InfoType: "notification"}})
		translator.handle(copilot.SessionEvent{ID: headID, ParentID: &rootID, Data: &rpc.SessionInfoData{InfoType: "notification"}})
		checkpoint := copilot.SessionEvent{ID: "checkpoint", ParentID: &headID,
			Data: &rpc.SessionUsageCheckpointData{TotalNanoAiu: 9}}
		translator.handle(checkpoint)
		Expect((<-events).Kind).To(Equal(copilotadapter.BridgeEventUsage))
		next := copilot.SessionEvent{ID: "checkpoint-successor", ParentID: &checkpoint.ID,
			Data: &rpc.SessionInfoData{InfoType: "notification"}}
		translator.handle(next)
		Expect(translator.eventIDs.head).To(Equal(next.ID))
	})
})
