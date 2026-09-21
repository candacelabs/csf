package copilotbridge

import (
	"context"
	"fmt"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	. "github.com/onsi/gomega/gstruct"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

// translateEvent is the pure seam between the SDK's event vocabulary and the
// adapter's; these specs are the contract the doc comment promises.
var _ = Describe("translateEvent", func() {
	var (
		toolNames map[copilotadapter.ToolCallID]copilotadapter.ToolName
		at        time.Time
	)

	BeforeEach(func() {
		toolNames = map[copilotadapter.ToolCallID]copilotadapter.ToolName{}
		at = time.Date(2026, 9, 4, 21, 0, 0, 0, time.UTC)
	})

	It("turns an assistant delta into a delta from copilot", func() {
		event, ok := translateEvent(copilot.SessionEvent{Timestamp: at, Data: &rpc.AssistantMessageDeltaData{DeltaContent: "Hel"}}, toolNames, nil)
		Expect(ok).To(BeTrue())
		Expect(event.Kind).To(Equal(copilotadapter.BridgeEventAssistantDelta))
		Expect(event.Text).To(Equal("Hel"))
		Expect(event.Author).To(Equal(copilotAuthor))
		Expect(event.OccurredAt).To(Equal(at))
	})

	It("pairs a tool result with its call by the call id, then forgets the id", func() {
		call, ok := translateEvent(copilot.SessionEvent{Timestamp: at, Data: &rpc.ToolExecutionStartData{ToolCallID: "c1", ToolName: "bash", Arguments: map[string]any{"cmd": "ls"}}}, toolNames, nil)
		Expect(ok).To(BeTrue())
		Expect(call.Kind).To(Equal(copilotadapter.BridgeEventToolCall))
		Expect(call.ToolName).To(Equal(copilotadapter.ToolName("bash")))
		Expect(call.ToolCallID).To(Equal(copilotadapter.ToolCallID("c1")))
		Expect(call.Text).To(ContainSubstring(`"cmd":"ls"`))
		Expect(toolNames).To(HaveKeyWithValue(copilotadapter.ToolCallID("c1"), copilotadapter.ToolName("bash")))

		result, ok := translateEvent(copilot.SessionEvent{Timestamp: at, Data: &rpc.ToolExecutionCompleteData{ToolCallID: "c1", Success: true}}, toolNames, nil)
		Expect(ok).To(BeTrue())
		Expect(result.Kind).To(Equal(copilotadapter.BridgeEventToolResult))
		Expect(result.ToolName).To(Equal(copilotadapter.ToolName("bash")))
		Expect(result.ToolCallID).To(Equal(copilotadapter.ToolCallID("c1")))
		Expect(toolNames).To(BeEmpty())
	})

	It("ignores idle and fails only the active turn on a nonrecoverable query error", func() {
		_, ok := translateEvent(copilot.SessionEvent{Timestamp: at, Data: &rpc.SessionIdleData{}}, toolNames, nil)
		Expect(ok).To(BeFalse())

		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		Expect(turns.enqueue(identifier)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-a")
		Expect(found).To(BeTrue())
		failed, ok := translateEvent(copilot.SessionEvent{Timestamp: at, Data: &rpc.SessionErrorData{Message: "query failed", ErrorType: "query"}}, toolNames, turns)
		Expect(ok).To(BeTrue())
		Expect(failed.Kind).To(Equal(copilotadapter.BridgeEventTurnFailed))
		Expect(failed.TurnID).To(PointTo(Equal(identifier)))
		Expect(failed.Text).To(Equal("query failed"))
		_, found = turns.active()
		Expect(found).To(BeFalse())
	})

	It("fails an auto-switch-eligible rate limit without an auto-switch request contract", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		Expect(turns.enqueue(identifier)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-a")
		Expect(found).To(BeTrue())
		eligible := true
		failed, ok := translateEvent(copilot.SessionEvent{Timestamp: at, Data: &rpc.SessionErrorData{
			Message: "rate limited", ErrorType: "rate_limit", EligibleForAutoSwitch: &eligible,
		}}, toolNames, turns)
		Expect(ok).To(BeTrue())
		Expect(failed.Kind).To(Equal(copilotadapter.BridgeEventTurnFailed))
		Expect(failed.TurnID).To(PointTo(Equal(identifier)))
		Expect(failed.Text).To(Equal("rate limited"))
		Expect(failed.SessionBusy).To(BeFalse())
		_, found = turns.active()
		Expect(found).To(BeFalse())

		_, ok = translateEvent(copilot.SessionEvent{
			Timestamp: at, Data: &rpc.AssistantTurnEndData{TurnID: "sdk-a"},
		}, toolNames, turns)
		Expect(ok).To(BeFalse(), "a late completion must not overwrite the terminal failure")
	})

	It("does not let a subagent session error or SDK iteration end terminate the active root prompt", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		Expect(turns.enqueue(identifier)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-root")
		Expect(found).To(BeTrue())
		agentID, eligible := "subagent-1", true

		_, ok := translateEvent(copilot.SessionEvent{
			Timestamp: at,
			AgentID:   &agentID,
			Data: &rpc.SessionErrorData{
				Message: "subagent rate limited", ErrorType: "rate_limit", EligibleForAutoSwitch: &eligible,
			},
		}, toolNames, turns)
		Expect(ok).To(BeFalse())
		active, found := turns.active()
		Expect(found).To(BeTrue())
		Expect(active).To(Equal(identifier))

		_, ok = translateEvent(copilot.SessionEvent{
			Timestamp: at, Data: &rpc.AssistantTurnEndData{TurnID: "sdk-root"},
		}, toolNames, turns)
		Expect(ok).To(BeFalse())
		active, found = turns.active()
		Expect(found).To(BeTrue())
		Expect(active).To(Equal(identifier))

		completed, ok := translateEvent(copilot.SessionEvent{
			Timestamp: at, Data: &rpc.SessionIdleData{},
		}, toolNames, turns)
		Expect(ok).To(BeTrue())
		Expect(completed.Kind).To(Equal(copilotadapter.BridgeEventTurnCompleted))
		Expect(completed.TurnID).To(PointTo(Equal(identifier)))
	})

	It("maps only an error shutdown to a stable session failure", func() {
		reason := "copilot process crashed"
		events := make(chan copilotadapter.BridgeEvent, 1)
		translator := newEventTranslator(events, nil)
		translator.handle(copilot.SessionEvent{
			ID: "sdk-shutdown-error", Timestamp: at,
			Data: &rpc.SessionShutdownData{ShutdownType: rpc.ShutdownTypeError, ErrorReason: &reason},
		})

		failed := <-events
		Expect(failed.ID).To(Equal("sdk-shutdown-error"))
		Expect(failed.Kind).To(Equal(copilotadapter.BridgeEventFailed))
		Expect(failed.OccurredAt).To(Equal(at))
		Expect(failed.Text).To(Equal(reason))

		_, ok := translateEvent(copilot.SessionEvent{
			ID: "sdk-shutdown-routine", Timestamp: at,
			Data: &rpc.SessionShutdownData{ShutdownType: rpc.ShutdownTypeRoutine},
		}, toolNames, nil)
		Expect(ok).To(BeFalse())
	})

	It("ignores an event kind the adapter does not project", func() {
		_, ok := translateEvent(copilot.SessionEvent{Timestamp: at, Data: &rpc.AssistantTurnEndData{}}, toolNames, nil)
		Expect(ok).To(BeFalse())
	})

	It("keeps a tool loop on one prompt and advances a queued prompt only at its distinct interaction", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		Expect(turns.enqueue(firstID)).To(BeTrue())
		Expect(turns.enqueue(secondID)).To(BeTrue())
		firstInteraction, secondInteraction := "interaction-a", "interaction-b"
		idleDelivery, queuedDelivery := rpc.UserMessageDeliveryIdle, rpc.UserMessageDeliveryQueued
		events := make(chan copilotadapter.BridgeEvent, 10)
		translator := newEventTranslator(events, turns)

		translator.handle(copilot.SessionEvent{ID: "user-a", Data: &rpc.UserMessageData{
			InteractionID: &firstInteraction, Delivery: &idleDelivery,
		}})
		translator.handle(copilot.SessionEvent{ID: "a-start-0", Data: &rpc.AssistantTurnStartData{
			InteractionID: &firstInteraction, TurnID: "0",
		}})
		Expect(<-events).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnStarted)),
			HaveField("TurnID", PointTo(Equal(firstID))),
		))

		firstSDKTurn := "0"
		translator.handle(copilot.SessionEvent{ID: "a-tool", Data: &rpc.ToolExecutionStartData{
			ToolCallID: "tool-a", ToolName: "bash", TurnID: &firstSDKTurn,
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(firstID)))
		translator.handle(copilot.SessionEvent{ID: "a-end-0", Data: &rpc.AssistantTurnEndData{TurnID: "0"}})
		translator.handle(copilot.SessionEvent{ID: "a-start-1", Data: &rpc.AssistantTurnStartData{
			InteractionID: &firstInteraction, TurnID: "1",
		}})
		Expect(events).To(HaveLen(0), "an SDK tool-loop continuation must not start another durable turn")
		secondSDKTurn := "1"
		translator.handle(copilot.SessionEvent{ID: "a-message", Data: &rpc.AssistantMessageData{
			InteractionID: &firstInteraction, TurnID: &secondSDKTurn, MessageID: "message-a", Content: "done",
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(firstID)))
		translator.handle(copilot.SessionEvent{ID: "a-end-1", Data: &rpc.AssistantTurnEndData{TurnID: "1"}})

		translator.handle(copilot.SessionEvent{ID: "user-b", Data: &rpc.UserMessageData{
			InteractionID: &secondInteraction, Delivery: &queuedDelivery,
		}})
		translator.handle(copilot.SessionEvent{ID: "b-start-0", Data: &rpc.AssistantTurnStartData{
			InteractionID: &secondInteraction, TurnID: "0",
		}})
		completed, started := <-events, <-events
		Expect(completed).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnCompleted)),
			HaveField("TurnID", PointTo(Equal(firstID))),
			HaveField("SessionBusy", BeTrue()),
		))
		Expect(started).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnStarted)),
			HaveField("TurnID", PointTo(Equal(secondID))),
		))
		Expect(completed.ID).NotTo(Equal(started.ID))

		secondSDKTurn = "0"
		translator.handle(copilot.SessionEvent{ID: "b-message", Data: &rpc.AssistantMessageData{
			InteractionID: &secondInteraction, TurnID: &secondSDKTurn, MessageID: "message-b", Content: "done",
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(secondID)), "reused SDK turn 0 must resolve through interaction B")
		translator.handle(copilot.SessionEvent{ID: "b-end-0", Data: &rpc.AssistantTurnEndData{TurnID: "0"}})
		translator.handle(copilot.SessionEvent{ID: "all-idle", Data: &rpc.SessionIdleData{}})
		Expect(<-events).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnCompleted)),
			HaveField("TurnID", PointTo(Equal(secondID))),
		))
	})

	It("advances a queued prompt when both delivery and turn start omit interaction ids", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		Expect(turns.enqueue(firstID)).To(BeTrue())
		Expect(turns.enqueue(secondID)).To(BeTrue())
		firstInteraction := "interaction-a"
		queuedDelivery := rpc.UserMessageDeliveryQueued
		events := make(chan copilotadapter.BridgeEvent, 8)
		translator := newEventTranslator(events, turns)

		translator.handle(copilot.SessionEvent{ID: "start-a", Data: &rpc.AssistantTurnStartData{
			InteractionID: &firstInteraction, TurnID: "0",
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(firstID)))
		translator.handle(copilot.SessionEvent{ID: "user-b", Data: &rpc.UserMessageData{
			Delivery: &queuedDelivery,
		}})
		Expect(events).To(HaveLen(0), "the delivery boundary waits for the active iteration to hand off")
		translator.handle(copilot.SessionEvent{ID: "start-b", Data: &rpc.AssistantTurnStartData{TurnID: "0"}})

		completed, started := <-events, <-events
		Expect(completed).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnCompleted)),
			HaveField("TurnID", PointTo(Equal(firstID))),
			HaveField("SessionBusy", BeTrue()),
		))
		Expect(started).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnStarted)),
			HaveField("TurnID", PointTo(Equal(secondID))),
		))

		sdkTurn := "0"
		translator.handle(copilot.SessionEvent{ID: "message-b", Data: &rpc.AssistantMessageData{
			TurnID: &sdkTurn, MessageID: "message-b", Content: "queued response",
		}})
		Expect(<-events).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventAssistantMessage)),
			HaveField("TurnID", PointTo(Equal(secondID))),
		))
		translator.handle(copilot.SessionEvent{ID: "idle-b", Data: &rpc.SessionIdleData{}})
		Expect(<-events).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnCompleted)),
			HaveField("TurnID", PointTo(Equal(secondID))),
			HaveField("SessionBusy", BeFalse()),
		))
	})

	It("rekeys an anonymous active SDK turn when later output supplies its interaction id", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		Expect(turns.enqueue(identifier)).To(BeTrue())
		queuedDelivery := rpc.UserMessageDeliveryQueued
		interaction := "interaction-explicit"
		sdkTurn := "0"
		events := make(chan copilotadapter.BridgeEvent, 4)
		translator := newEventTranslator(events, turns)

		translator.handle(copilot.SessionEvent{ID: "user", Data: &rpc.UserMessageData{
			Delivery: &queuedDelivery,
		}})
		translator.handle(copilot.SessionEvent{ID: "start", Data: &rpc.AssistantTurnStartData{TurnID: sdkTurn}})
		Expect(<-events).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnStarted)),
			HaveField("TurnID", PointTo(Equal(identifier))),
		))

		translator.handle(copilot.SessionEvent{ID: "explicit-message", Data: &rpc.AssistantMessageData{
			InteractionID: &interaction, TurnID: &sdkTurn,
			MessageID: "explicit-message", Content: "explicit output",
		}})
		Expect(<-events).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventAssistantMessage)),
			HaveField("TurnID", PointTo(Equal(identifier))),
		))
		mapped, found := turns.lookup(interaction, sdkTurn)
		Expect(found).To(BeTrue())
		Expect(mapped).To(Equal(identifier))
		_, found = turns.lookup("interaction-unrelated", sdkTurn)
		Expect(found).To(BeFalse(), "an explicit active key cannot be reused for another interaction")

		translator.handle(copilot.SessionEvent{ID: "anonymous-message", Data: &rpc.AssistantMessageData{
			TurnID: &sdkTurn, MessageID: "anonymous-message", Content: "anonymous output",
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(identifier)))
	})

	It("does not rekey an anonymous active turn from a closed interaction", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		closedID, activeID := uuid.New(), uuid.New()
		Expect(turns.enqueueImmediate(closedID)).To(BeTrue())
		Expect(turns.enqueue(activeID)).To(BeTrue())
		closedInteraction := "interaction-closed"
		queuedDelivery := rpc.UserMessageDeliveryQueued
		steeringDelivery := rpc.UserMessageDeliverySteering
		sdkTurn := "0"
		events := make(chan copilotadapter.BridgeEvent, 6)
		translator := newEventTranslator(events, turns)

		translator.handle(copilot.SessionEvent{ID: "closed-user", Data: &rpc.UserMessageData{
			InteractionID: &closedInteraction, Delivery: &steeringDelivery,
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(closedID)))
		translator.handle(copilot.SessionEvent{ID: "active-user", Data: &rpc.UserMessageData{
			Delivery: &queuedDelivery,
		}})
		Expect(<-events).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnCompleted)),
			HaveField("TurnID", PointTo(Equal(closedID))),
		))
		Expect(<-events).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnStarted)),
			HaveField("TurnID", PointTo(Equal(activeID))),
		))
		translator.handle(copilot.SessionEvent{ID: "active-start", Data: &rpc.AssistantTurnStartData{TurnID: sdkTurn}})
		Expect(events).To(HaveLen(0))
		translator.handle(copilot.SessionEvent{ID: "late-closed-message", Data: &rpc.AssistantMessageData{
			InteractionID: &closedInteraction, TurnID: &sdkTurn,
			MessageID: "late-closed-message", Content: "late output",
		}})
		Expect((<-events).TurnID).To(BeNil())

		translator.handle(copilot.SessionEvent{ID: "active-message", Data: &rpc.AssistantMessageData{
			TurnID: &sdkTurn, MessageID: "active-message", Content: "active output",
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(activeID)))
	})

	It("hands one SDK continuation from its prompt through every rapid steer before output", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		promptID, firstSteerID, secondSteerID := uuid.New(), uuid.New(), uuid.New()
		Expect(turns.enqueue(promptID)).To(BeTrue())
		interaction := "interaction-steered"
		steeringDelivery := rpc.UserMessageDeliverySteering
		events := make(chan copilotadapter.BridgeEvent, 10)
		translator := newEventTranslator(events, turns)

		translator.handle(copilot.SessionEvent{ID: "prompt-start", Data: &rpc.AssistantTurnStartData{
			InteractionID: &interaction, TurnID: "0",
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(promptID)))
		Expect(turns.enqueueImmediate(firstSteerID)).To(BeTrue())
		Expect(turns.enqueueImmediate(secondSteerID)).To(BeTrue())
		translator.handle(copilot.SessionEvent{ID: "prompt-end", Data: &rpc.AssistantTurnEndData{TurnID: "0"}})
		translator.handle(copilot.SessionEvent{ID: "continuation-start", Data: &rpc.AssistantTurnStartData{
			InteractionID: &interaction, TurnID: "1",
		}})
		Expect(events).To(HaveLen(0), "a start before the SDK steering events still belongs to the original prompt")

		translator.handle(copilot.SessionEvent{ID: "steer-one", Data: &rpc.UserMessageData{
			InteractionID: &interaction, Delivery: &steeringDelivery,
		}})
		firstCompleted, firstStarted := <-events, <-events
		Expect(firstCompleted).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnCompleted)),
			HaveField("TurnID", PointTo(Equal(promptID))),
		))
		Expect(firstStarted).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnStarted)),
			HaveField("TurnID", PointTo(Equal(firstSteerID))),
		))

		translator.handle(copilot.SessionEvent{ID: "steer-two", Data: &rpc.UserMessageData{
			InteractionID: &interaction, Delivery: &steeringDelivery,
		}})
		Expect(<-events).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnCompleted)),
			HaveField("TurnID", PointTo(Equal(firstSteerID))),
		))
		Expect(<-events).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnStarted)),
			HaveField("TurnID", PointTo(Equal(secondSteerID))),
		))

		continuationSDKTurn := "1"
		translator.handle(copilot.SessionEvent{ID: "steered-message", Data: &rpc.AssistantMessageData{
			InteractionID: &interaction, TurnID: &continuationSDKTurn,
			MessageID: "steered-message", Content: "latest steering won",
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(secondSteerID)))
		translator.handle(copilot.SessionEvent{ID: "steered-end", Data: &rpc.AssistantTurnEndData{TurnID: "1"}})
		Expect(events).To(HaveLen(0), "turn_end closes only the SDK iteration")
		translator.handle(copilot.SessionEvent{ID: "steered-idle", Data: &rpc.SessionIdleData{}})
		Expect(<-events).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnCompleted)),
			HaveField("TurnID", PointTo(Equal(secondSteerID))),
		))
	})

	It("rebinds steering without an interaction id to the active SDK iteration", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		promptID, steerID := uuid.New(), uuid.New()
		Expect(turns.enqueue(promptID)).To(BeTrue())
		interaction := "interaction-steered-without-user-id"
		steeringDelivery := rpc.UserMessageDeliverySteering
		events := make(chan copilotadapter.BridgeEvent, 6)
		translator := newEventTranslator(events, turns)

		translator.handle(copilot.SessionEvent{ID: "prompt-start", Data: &rpc.AssistantTurnStartData{
			InteractionID: &interaction, TurnID: "0",
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(promptID)))
		Expect(turns.enqueueImmediate(steerID)).To(BeTrue())

		translator.handle(copilot.SessionEvent{ID: "steer-without-interaction", Data: &rpc.UserMessageData{
			Delivery: &steeringDelivery,
		}})
		Expect(<-events).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnCompleted)),
			HaveField("TurnID", PointTo(Equal(promptID))),
		))
		Expect(<-events).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnStarted)),
			HaveField("TurnID", PointTo(Equal(steerID))),
		))

		sdkTurn := "0"
		translator.handle(copilot.SessionEvent{ID: "steered-message", Data: &rpc.AssistantMessageData{
			InteractionID: &interaction, TurnID: &sdkTurn,
			MessageID: "steered-message", Content: "steering owns the final output",
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(steerID)))
		translator.handle(copilot.SessionEvent{ID: "steered-idle", Data: &rpc.SessionIdleData{}})
		Expect(<-events).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnCompleted)),
			HaveField("TurnID", PointTo(Equal(steerID))),
		))
	})

	It("does not let autopilot or sourced user events consume an adapter steering row", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		promptID, steerID := uuid.New(), uuid.New()
		Expect(turns.enqueue(promptID)).To(BeTrue())
		interaction := "interaction-user-source"
		steeringDelivery := rpc.UserMessageDeliverySteering
		events := make(chan copilotadapter.BridgeEvent, 6)
		translator := newEventTranslator(events, turns)
		translator.handle(copilot.SessionEvent{ID: "start", Data: &rpc.AssistantTurnStartData{
			InteractionID: &interaction, TurnID: "0",
		}})
		<-events
		Expect(turns.enqueueImmediate(steerID)).To(BeTrue())
		autopilot := true
		translator.handle(copilot.SessionEvent{ID: "autopilot", Data: &rpc.UserMessageData{
			InteractionID: &interaction, Delivery: &steeringDelivery, IsAutopilotContinuation: &autopilot,
		}})
		source := "skill-hidden"
		translator.handle(copilot.SessionEvent{ID: "sourced", Data: &rpc.UserMessageData{
			InteractionID: &interaction, Delivery: &steeringDelivery, Source: &source,
		}})
		Expect(events).To(HaveLen(0))
		active, found := turns.active()
		Expect(found).To(BeTrue())
		Expect(active).To(Equal(promptID))

		translator.handle(copilot.SessionEvent{ID: "adapter", Data: &rpc.UserMessageData{
			InteractionID: &interaction, Delivery: &steeringDelivery,
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(promptID)))
		Expect((<-events).TurnID).To(PointTo(Equal(steerID)))
	})

	It("retains message tool and subagent provenance when steering replaces the foreground owner", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		promptID, steerID := uuid.New(), uuid.New()
		Expect(turns.enqueue(promptID)).To(BeTrue())
		interaction := "interaction-provenance"
		sdkTurn := "0"
		steeringDelivery := rpc.UserMessageDeliverySteering
		events := make(chan copilotadapter.BridgeEvent, 16)
		translator := newEventTranslator(events, turns)

		translator.handle(copilot.SessionEvent{ID: "start", Data: &rpc.AssistantTurnStartData{
			InteractionID: &interaction, TurnID: sdkTurn,
		}})
		<-events
		translator.handle(copilot.SessionEvent{ID: "message-start", Data: &rpc.AssistantMessageStartData{MessageID: "message-a"}})
		translator.handle(copilot.SessionEvent{ID: "tool-start", Data: &rpc.ToolExecutionStartData{
			ToolCallID: "tool-a", ToolName: "task", TurnID: &sdkTurn,
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(promptID)))
		translator.handle(copilot.SessionEvent{ID: "subagent-start", Data: &rpc.SubagentStartedData{
			ToolCallID: "tool-a", AgentName: "audit", AgentDisplayName: "Audit",
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(promptID)))

		Expect(turns.enqueueImmediate(steerID)).To(BeTrue())
		translator.handle(copilot.SessionEvent{ID: "steer", Data: &rpc.UserMessageData{
			InteractionID: &interaction, Delivery: &steeringDelivery,
		}})
		<-events
		<-events
		translator.handle(copilot.SessionEvent{ID: "late-delta", Data: &rpc.AssistantMessageDeltaData{
			MessageID: "message-a", DeltaContent: "late",
		}})
		translator.handle(copilot.SessionEvent{ID: "late-message", Data: &rpc.AssistantMessageData{
			InteractionID: &interaction, TurnID: &sdkTurn, MessageID: "message-a", Content: "late done",
		}})
		translator.handle(copilot.SessionEvent{ID: "late-tool", Data: &rpc.ToolExecutionCompleteData{
			InteractionID: &interaction, TurnID: &sdkTurn, ToolCallID: "tool-a", Success: true,
		}})
		translator.handle(copilot.SessionEvent{ID: "late-subagent", Data: &rpc.SubagentCompletedData{
			ToolCallID: "tool-a", AgentName: "audit", AgentDisplayName: "Audit",
		}})

		for range 4 {
			Expect((<-events).TurnID).To(PointTo(Equal(promptID)))
		}
		active, found := turns.active()
		Expect(found).To(BeTrue())
		Expect(active).To(Equal(steerID))
	})

	It("deduplicates terminal source events before they can mutate a newer prompt", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		Expect(turns.enqueue(firstID)).To(BeTrue())
		Expect(turns.enqueue(secondID)).To(BeTrue())
		firstInteraction, secondInteraction := "interaction-error-a", "interaction-error-b"
		queuedDelivery := rpc.UserMessageDeliveryQueued
		events := make(chan copilotadapter.BridgeEvent, 8)
		translator := newEventTranslator(events, turns)

		translator.handle(copilot.SessionEvent{ID: "start-a", Data: &rpc.AssistantTurnStartData{
			InteractionID: &firstInteraction, TurnID: "0",
		}})
		<-events
		errorEvent := copilot.SessionEvent{ID: "terminal-a", Data: &rpc.SessionErrorData{Message: "failed", ErrorType: "query"}}
		translator.handle(errorEvent)
		Expect(<-events).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnFailed)),
			HaveField("TurnID", PointTo(Equal(firstID))),
		))
		translator.handle(copilot.SessionEvent{ID: "user-b", Data: &rpc.UserMessageData{
			InteractionID: &secondInteraction, Delivery: &queuedDelivery,
		}})
		translator.handle(copilot.SessionEvent{ID: "start-b", Data: &rpc.AssistantTurnStartData{
			InteractionID: &secondInteraction, TurnID: "0",
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(secondID)))
		translator.handle(errorEvent)
		Expect(events).To(HaveLen(0))
		active, found := turns.active()
		Expect(found).To(BeTrue())
		Expect(active).To(Equal(secondID))
	})

	It("keeps correlating a durable turn across an interleaved ephemeral sibling", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		Expect(turns.enqueue(identifier)).To(BeTrue())
		events := make(chan copilotadapter.BridgeEvent, 1)
		translator := newEventTranslator(events, turns)
		sharedParent := "durable-parent"
		translator.handle(copilot.SessionEvent{
			ID:   sharedParent,
			Data: &rpc.SessionStartData{SessionID: "session-a"},
		})
		ephemeral := true
		liveInfo := copilot.SessionEvent{
			ID: "live-info", ParentID: &sharedParent, Ephemeral: &ephemeral,
			Data: &rpc.SessionInfoData{InfoType: "notification", Message: "live only"},
		}
		translator.handle(liveInfo)
		Expect(translator.eventIDs.head).To(Equal(sharedParent))
		Expect(translator.eventIDs.recent).To(HaveLen(2))
		translator.handle(liveInfo)
		Expect(translator.eventIDs.recent).To(HaveLen(2))
		interaction, delivery := "interaction-live", rpc.UserMessageDeliveryIdle
		translator.handle(copilot.SessionEvent{
			ID: "user-message", ParentID: &sharedParent,
			Data: &rpc.UserMessageData{InteractionID: &interaction, Delivery: &delivery},
		})
		userMessageID := "user-message"
		translator.handle(copilot.SessionEvent{
			ID: "turn-start", ParentID: &userMessageID,
			Data: &rpc.AssistantTurnStartData{InteractionID: &interaction, TurnID: "0"},
		})

		var started copilotadapter.BridgeEvent
		Eventually(events).Should(Receive(&started))
		Expect(started).To(And(
			HaveField("Kind", Equal(copilotadapter.BridgeEventTurnStarted)),
			HaveField("TurnID", PointTo(Equal(identifier))),
		))
	})

	It("accepts both durable successor forms around an ephemeral event", func() {
		for _, successorParent := range []string{"durable-0", "ephemeral-1"} {
			cursor := sdkEventIDCursor{}
			Expect(cursor.accept(copilot.SessionEvent{ID: "durable-root"})).To(BeTrue())
			rootID := "durable-root"
			Expect(cursor.accept(copilot.SessionEvent{ID: "durable-0", ParentID: &rootID})).To(BeTrue())
			durableID, ephemeral := "durable-0", true
			Expect(cursor.accept(copilot.SessionEvent{
				ID: "ephemeral-1", ParentID: &durableID, Ephemeral: &ephemeral,
			})).To(BeTrue())
			Expect(cursor.head).To(Equal(durableID))
			Expect(cursor.ephemeralAnchors).To(HaveKeyWithValue("ephemeral-1", durableID))

			Expect(cursor.accept(copilot.SessionEvent{
				ID: "durable-1", ParentID: &successorParent,
			})).To(BeTrue(), "durable successor parent %q", successorParent)
			Expect(cursor.head).To(Equal("durable-1"))
		}
	})

	It("evicts expired ephemeral aliases without relaxing durable replay rejection", func() {
		cursor := sdkEventIDCursor{}
		Expect(cursor.accept(copilot.SessionEvent{ID: "durable-root"})).To(BeTrue())
		rootID := "durable-root"
		Expect(cursor.accept(copilot.SessionEvent{ID: "durable-head", ParentID: &rootID})).To(BeTrue())
		durableHead, ephemeral := "durable-head", true
		for index := 0; index < sdkEventIDWindowSize+8; index++ {
			identifier := fmt.Sprintf("ephemeral-%d", index)
			Expect(cursor.accept(copilot.SessionEvent{
				ID: identifier, ParentID: &durableHead, Ephemeral: &ephemeral,
			})).To(BeTrue())
		}

		Expect(cursor.recent).To(HaveLen(sdkEventIDWindowSize))
		Expect(cursor.ephemeralAnchors).To(HaveLen(sdkEventIDWindowSize))
		expiredAlias := "ephemeral-0"
		Expect(cursor.accept(copilot.SessionEvent{
			ID: "stale-successor", ParentID: &expiredAlias,
		})).To(BeFalse())
		Expect(cursor.accept(copilot.SessionEvent{
			ID: durableHead, ParentID: &rootID,
		})).To(BeFalse())
		Expect(cursor.accept(copilot.SessionEvent{ID: durableHead})).To(BeFalse(),
			"the durable head remains a replay even after ephemeral siblings evict it from the ring")
		Expect(cursor.head).To(Equal(durableHead))
	})

	It("bounds event-id memory and rejects linked replays of arbitrary age across idle", func() {
		cursor := sdkEventIDCursor{}
		first := copilot.SessionEvent{
			ID: "event-0", Data: &rpc.AssistantMessageDeltaData{MessageID: "message", DeltaContent: "0"},
		}
		Expect(cursor.accept(first)).To(BeTrue())
		previous := first.ID
		for index := 1; index < sdkEventIDWindowSize*8; index++ {
			parent := previous
			identifier := fmt.Sprintf("event-%d", index)
			Expect(cursor.accept(copilot.SessionEvent{
				ID: identifier, ParentID: &parent,
				Data: &rpc.AssistantMessageDeltaData{MessageID: "message", DeltaContent: "x"},
			})).To(BeTrue())
			previous = identifier
		}
		Expect(cursor.recent).To(HaveLen(sdkEventIDWindowSize))

		idleParent := previous
		idle := copilot.SessionEvent{ID: "idle", ParentID: &idleParent, Data: &rpc.SessionIdleData{}}
		Expect(cursor.accept(idle)).To(BeTrue())
		Expect(cursor.accept(first)).To(BeFalse(), "an evicted root event is still stale after idle")
		staleParent := "event-0"
		Expect(cursor.accept(copilot.SessionEvent{
			ID: "stale-branch", ParentID: &staleParent,
			Data: &rpc.AssistantMessageDeltaData{MessageID: "message", DeltaContent: "stale"},
		})).To(BeFalse())
		Expect(cursor.accept(idle)).To(BeFalse())
		Expect(cursor.head).To(Equal("idle"))
		Expect(cursor.recent).To(HaveLen(sdkEventIDWindowSize))

		// Resume creates a new translator. Its first live event may point to an
		// unavailable pre-restart parent; relational source receipts suppress any
		// already-projected event while this cursor owns the new live chain.
		resumed := sdkEventIDCursor{}
		resumeParent := cursor.head
		resumeEvent := copilot.SessionEvent{
			ID: "resumed-event", ParentID: &resumeParent,
			Data: &rpc.AssistantMessageDeltaData{MessageID: "message", DeltaContent: "resumed"},
		}
		Expect(resumed.accept(resumeEvent)).To(BeTrue())
		Expect(resumed.accept(resumeEvent)).To(BeFalse())
		Expect(resumed.recent).To(HaveLen(1))
	})

	It("evicts completed tool ownership across continuously busy queued handoffs", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		events := make(chan copilotadapter.BridgeEvent, 4)
		translator := newEventTranslator(events, turns)
		for index := 0; index < sdkEventIDWindowSize*2; index++ {
			identifier := uuid.New()
			Expect(turns.enqueue(identifier)).To(BeTrue())
			interaction := fmt.Sprintf("interaction-%d", index)
			translator.handle(copilot.SessionEvent{Data: &rpc.AssistantTurnStartData{
				InteractionID: &interaction, TurnID: "0",
			}})
			if index > 0 {
				<-events
			}
			<-events
			sdkTurnID := "0"
			toolCallID := fmt.Sprintf("tool-%d", index)
			translator.handle(copilot.SessionEvent{Data: &rpc.ToolExecutionStartData{
				ToolCallID: toolCallID, ToolName: "bash", TurnID: &sdkTurnID,
			}})
			<-events
			translator.handle(copilot.SessionEvent{Data: &rpc.ToolExecutionCompleteData{
				InteractionID: &interaction, TurnID: &sdkTurnID,
				ToolCallID: toolCallID, Success: true,
			}})
			<-events
			translator.handle(copilot.SessionEvent{Data: &rpc.AssistantTurnEndData{TurnID: sdkTurnID}})
		}

		Expect(translator.toolTurns).To(BeEmpty())
		Expect(translator.subagentTools).To(BeEmpty())
	})

	It("restores the active turn before FIFO steer and queue lanes", func() {
		activeID, queuedID := uuid.New(), uuid.New()
		firstSteerID, secondSteerID := uuid.New(), uuid.New()
		turns := newRestoredTurnCorrelator([]copilotadapter.BridgeRestoredTurn{
			{ID: activeID, Mode: api.Queue, Status: api.TurnStatusRunning},
			{ID: queuedID, Mode: api.Queue, Status: api.TurnStatusQueued},
			{ID: firstSteerID, Mode: api.Steer, Status: api.TurnStatusQueued},
			{ID: secondSteerID, Mode: api.Steer, Status: api.TurnStatusQueued},
		})
		DeferCleanup(turns.stop)
		interaction, queuedInteraction := "interaction-active", "interaction-queued"
		steeringDelivery, queuedDelivery := rpc.UserMessageDeliverySteering, rpc.UserMessageDeliveryQueued
		events := make(chan copilotadapter.BridgeEvent, 12)
		translator := newEventTranslator(events, turns)

		translator.handle(copilot.SessionEvent{ID: "restored-start", Data: &rpc.AssistantTurnStartData{
			InteractionID: &interaction, TurnID: "0",
		}})
		Expect(events).To(HaveLen(0), "a restored running row is already started")
		translator.handle(copilot.SessionEvent{ID: "steer-one", Data: &rpc.UserMessageData{
			InteractionID: &interaction, Delivery: &steeringDelivery,
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(activeID)))
		Expect((<-events).TurnID).To(PointTo(Equal(firstSteerID)))
		translator.handle(copilot.SessionEvent{ID: "steer-two", Data: &rpc.UserMessageData{
			InteractionID: &interaction, Delivery: &steeringDelivery,
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(firstSteerID)))
		Expect((<-events).TurnID).To(PointTo(Equal(secondSteerID)))

		translator.handle(copilot.SessionEvent{ID: "queued-user", Data: &rpc.UserMessageData{
			InteractionID: &queuedInteraction, Delivery: &queuedDelivery,
		}})
		translator.handle(copilot.SessionEvent{ID: "queued-start", Data: &rpc.AssistantTurnStartData{
			InteractionID: &queuedInteraction, TurnID: "0",
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(secondSteerID)))
		Expect((<-events).TurnID).To(PointTo(Equal(queuedID)))
	})

	It("keeps a restart-visible unknown delivery correlated without reinvoking it", func() {
		unknownID, queuedID := uuid.New(), uuid.New()
		turns := newRestoredTurnCorrelator([]copilotadapter.BridgeRestoredTurn{
			{ID: unknownID, Mode: api.Queue, Status: api.TurnStatusQueued, DeliveryUnknown: true},
			{ID: queuedID, Mode: api.Queue, Status: api.TurnStatusQueued},
		})
		DeferCleanup(turns.stop)
		invoked := false
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error {
			invoked = true
			return nil
		})

		delivery, err := sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: unknownID})
		Expect(err).NotTo(HaveOccurred())
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryUnknown))
		Expect(invoked).To(BeFalse())
		firstInteraction, nextInteraction := "interaction-unknown", "interaction-next"
		idleDelivery, queuedDelivery := rpc.UserMessageDeliveryIdle, rpc.UserMessageDeliveryQueued
		events := make(chan copilotadapter.BridgeEvent, 4)
		translator := newEventTranslator(events, turns)
		translator.handle(copilot.SessionEvent{ID: "unknown-user", Data: &rpc.UserMessageData{
			InteractionID: &firstInteraction, Delivery: &idleDelivery,
		}})
		translator.handle(copilot.SessionEvent{ID: "unknown-start", Data: &rpc.AssistantTurnStartData{
			InteractionID: &firstInteraction, TurnID: "0",
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(unknownID)))
		translator.handle(copilot.SessionEvent{ID: "unknown-end", Data: &rpc.AssistantTurnEndData{TurnID: "0"}})
		translator.handle(copilot.SessionEvent{ID: "next-user", Data: &rpc.UserMessageData{
			InteractionID: &nextInteraction, Delivery: &queuedDelivery,
		}})
		translator.handle(copilot.SessionEvent{ID: "next-start", Data: &rpc.AssistantTurnStartData{
			InteractionID: &nextInteraction, TurnID: "0",
		}})
		Expect((<-events).TurnID).To(PointTo(Equal(unknownID)))
		Expect((<-events).TurnID).To(PointTo(Equal(queuedID)))
	})

	It("stamps now on an event with no timestamp", func() {
		event, _ := translateEvent(copilot.SessionEvent{Data: &rpc.AssistantMessageDeltaData{DeltaContent: "x"}}, toolNames, nil)
		Expect(event.OccurredAt).NotTo(BeZero())
	})

	It("preserves the SDK event id on projected messages and deltas", func() {
		events := make(chan copilotadapter.BridgeEvent, 2)
		translator := newEventTranslator(events, nil)
		translator.handle(copilot.SessionEvent{ID: "sdk-delta", Data: &rpc.AssistantMessageDeltaData{MessageID: "message-1", DeltaContent: "d"}})
		translator.handle(copilot.SessionEvent{ID: "sdk-message", Data: &rpc.AssistantMessageData{MessageID: "message-1", Content: "done"}})

		Expect((<-events).ID).To(Equal("sdk-delta"))
		Expect((<-events).ID).To(Equal("sdk-message"))
	})

	It("clears stale correlation history on every root idle even without an active prompt", func() {
		events := make(chan copilotadapter.BridgeEvent, 1)
		translator := newEventTranslator(events, nil)
		identifier := uuid.New()
		translator.toolNames["tool"] = "bash"
		translator.toolTurns["tool"] = identifier
		translator.agentTurns["agent"] = identifier
		translator.messageTurns["message"] = identifier
		translator.pendingDeltas["message"] = []copilotadapter.BridgeEvent{{Text: "stale"}}

		translator.handle(copilot.SessionEvent{ID: "idle-without-owner", Data: &rpc.SessionIdleData{}})
		Expect(events).To(HaveLen(0))
		Expect(translator.toolNames).To(BeEmpty())
		Expect(translator.toolTurns).To(BeEmpty())
		Expect(translator.agentTurns).To(BeEmpty())
		Expect(translator.messageTurns).To(BeEmpty())
		Expect(translator.pendingDeltas).To(BeEmpty())
	})

	It("flushes a delta buffered before message start ahead of later deltas without waiting for a final message", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		turnID := uuid.New()
		Expect(turns.enqueue(turnID)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-turn")
		Expect(found).To(BeTrue())
		events := make(chan copilotadapter.BridgeEvent, 2)
		translator := newEventTranslator(events, turns)
		firstAt := at
		secondAt := at.Add(time.Millisecond)

		translator.handle(copilot.SessionEvent{
			ID: "delta-before-start", Timestamp: firstAt,
			Data: &rpc.AssistantMessageDeltaData{MessageID: "message-1", DeltaContent: "A"},
		})
		Expect(events).To(HaveLen(0))
		translator.handle(copilot.SessionEvent{
			ID: "message-start", Timestamp: at,
			Data: &rpc.AssistantMessageStartData{MessageID: "message-1"},
		})
		translator.handle(copilot.SessionEvent{
			ID: "delta-after-start", Timestamp: secondAt,
			Data: &rpc.AssistantMessageDeltaData{MessageID: "message-1", DeltaContent: "B"},
		})

		first, second := <-events, <-events
		Expect(first).To(And(
			HaveField("ID", Equal("delta-before-start")),
			HaveField("OccurredAt", Equal(firstAt)),
			HaveField("Text", Equal("A")),
			HaveField("TurnID", PointTo(Equal(turnID))),
		))
		Expect(second).To(And(
			HaveField("ID", Equal("delta-after-start")),
			HaveField("OccurredAt", Equal(secondAt)),
			HaveField("Text", Equal("B")),
			HaveField("TurnID", PointTo(Equal(turnID))),
		))
		Expect(events).To(HaveLen(0), "message completion must not be required to flush the first delta")
	})

	It("maps SDK assistant intent with AgentID to progress on the spawning turn", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		Expect(turns.enqueue(firstID)).To(BeTrue())
		Expect(turns.enqueue(secondID)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-first")
		Expect(found).To(BeTrue())
		events := make(chan copilotadapter.BridgeEvent, 4)
		translator := newEventTranslator(events, turns)
		sdkFirst := "sdk-first"
		agentID := "agent-live-1"
		translator.handle(copilot.SessionEvent{Data: &rpc.ToolExecutionStartData{
			ToolCallID: "spawn-1", ToolName: "task", TurnID: &sdkFirst,
		}})
		translator.handle(copilot.SessionEvent{AgentID: &agentID, Data: &rpc.SubagentStartedData{
			ToolCallID: "spawn-1", AgentName: "audit", AgentDisplayName: "Audit worker",
		}})
		_, found = startTestTurn(turns, "sdk-second")
		Expect(found).To(BeTrue())
		translator.handle(copilot.SessionEvent{
			ID: "intent-1", AgentID: &agentID, Timestamp: at,
			Data: &rpc.AssistantIntentData{Intent: "Checking component exports"},
		})

		<-events // root tool call
		<-events // subagent lifecycle
		progress := <-events
		Expect(progress).To(And(
			HaveField("ID", Equal("intent-1")),
			HaveField("Kind", Equal(copilotadapter.BridgeEventSubagentProgress)),
			HaveField("AgentID", Equal(agentID)),
			HaveField("DisplayName", BeEmpty()),
			HaveField("Text", Equal("Checking component exports")),
			HaveField("TurnID", PointTo(Equal(firstID))),
		))
	})

	It("uses explicit SDK ids and message ids across overlapping turns", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		Expect(turns.enqueue(firstID)).To(BeTrue())
		Expect(turns.enqueue(secondID)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-a")
		Expect(found).To(BeTrue())

		events := make(chan copilotadapter.BridgeEvent, 8)
		translator := newEventTranslator(events, turns)
		translator.handle(copilot.SessionEvent{Data: &rpc.AssistantMessageStartData{MessageID: "message-a"}})
		translator.handle(copilot.SessionEvent{Data: &rpc.AssistantMessageDeltaData{MessageID: "message-a", DeltaContent: "late A"}})
		_, found = startTestTurn(turns, "sdk-b")
		Expect(found).To(BeTrue())
		sdkA, sdkB := "sdk-a", "sdk-b"
		translator.handle(copilot.SessionEvent{Data: &rpc.AssistantMessageData{MessageID: "message-a", Content: "A", TurnID: &sdkA}})
		translator.handle(copilot.SessionEvent{Data: &rpc.ToolExecutionStartData{ToolCallID: "tool-a", ToolName: "bash", TurnID: &sdkA}})
		translator.handle(copilot.SessionEvent{Data: &rpc.ToolExecutionStartData{ToolCallID: "tool-b", ToolName: "bash", TurnID: &sdkB}})

		Expect((<-events).TurnID).To(PointTo(Equal(firstID)))
		Expect((<-events).TurnID).To(PointTo(Equal(firstID)))
		Expect((<-events).TurnID).To(PointTo(Equal(firstID)))
		Expect((<-events).TurnID).To(PointTo(Equal(secondID)))
	})

	It("keeps late subagent lifecycle on the spawning turn", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		Expect(turns.enqueue(firstID)).To(BeTrue())
		Expect(turns.enqueue(secondID)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-a")
		Expect(found).To(BeTrue())
		events := make(chan copilotadapter.BridgeEvent, 4)
		translator := newEventTranslator(events, turns)
		sdkA := "sdk-a"
		translator.handle(copilot.SessionEvent{Data: &rpc.ToolExecutionStartData{ToolCallID: "spawn-a", ToolName: "task", TurnID: &sdkA}})
		_, found = startTestTurn(turns, "sdk-b")
		Expect(found).To(BeTrue())
		translator.handle(copilot.SessionEvent{Data: &rpc.SubagentStartedData{ToolCallID: "spawn-a", AgentName: "audit", AgentDisplayName: "Audit"}})
		translator.handle(copilot.SessionEvent{Data: &rpc.SubagentCompletedData{ToolCallID: "spawn-a", AgentName: "audit", AgentDisplayName: "Audit"}})

		Expect((<-events).TurnID).To(PointTo(Equal(firstID)))
		Expect((<-events).TurnID).To(PointTo(Equal(firstID)))
		Expect((<-events).TurnID).To(PointTo(Equal(firstID)))
	})

	It("maps abort at event time and ignores its later turn end", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		Expect(turns.enqueue(identifier)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-a")
		Expect(found).To(BeTrue())
		target, found, err := turns.beginAbort(identifier)
		Expect(err).NotTo(HaveOccurred())
		Expect(found).To(BeTrue())
		Expect(target).To(Equal(identifier))
		agentID := "subagent-1"
		_, ok := translateEvent(copilot.SessionEvent{AgentID: &agentID, Data: &rpc.AbortData{}}, toolNames, turns)
		Expect(ok).To(BeFalse(), "a subagent abort must not consume the selected root abort target")
		aborted, ok := translateEvent(copilot.SessionEvent{Data: &rpc.AbortData{}}, toolNames, turns)
		Expect(ok).To(BeTrue())
		Expect(aborted.Kind).To(Equal(copilotadapter.BridgeEventTurnAborted))
		Expect(aborted.TurnID).To(PointTo(Equal(identifier)))
		_, ok = translateEvent(copilot.SessionEvent{Data: &rpc.AssistantTurnEndData{TurnID: "sdk-a"}}, toolNames, turns)
		Expect(ok).To(BeFalse())
	})

	It("notifies an abort waiter before a saturated event stream can block", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		Expect(turns.enqueue(identifier)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-a")
		Expect(found).To(BeTrue())
		events := make(chan copilotadapter.BridgeEvent, 1)
		events <- copilotadapter.BridgeEvent{Kind: copilotadapter.BridgeEventAssistantDelta}
		waiter := &turnTerminationWaiter{}
		translator := newEventTranslator(events, turns, waiter.publish)
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error { return nil })
		abortResult := make(chan uuid.UUID, 1)
		abortErrors := make(chan error, 1)
		go func() {
			aborted, err := sender.abort(context.Background(), identifier, func(_ context.Context) error {
				go translator.handle(copilot.SessionEvent{Data: &rpc.AbortData{}})
				return nil
			}, waiter)
			abortResult <- aborted
			abortErrors <- err
		}()
		Eventually(abortResult).Should(Receive(Equal(identifier)))
		Expect(<-abortErrors).NotTo(HaveOccurred())
		Expect((<-events).Kind).To(Equal(copilotadapter.BridgeEventAssistantDelta))
		Eventually(events).Should(Receive(HaveField("Kind", Equal(copilotadapter.BridgeEventTurnAborted))))
	})

	Describe("permission event translation", func() {
		It("opens one deterministic request on the exact tool turn", func() {
			turns := newTurnCorrelator()
			DeferCleanup(turns.stop)
			turnID, sessionID := uuid.New(), uuid.New()
			Expect(turns.enqueue(turnID)).To(BeTrue())
			_, found := startTestTurn(turns, "sdk-turn")
			Expect(found).To(BeTrue())
			events := make(chan copilotadapter.BridgeEvent, 3)
			resolutions := newResolutionRegistry()
			DeferCleanup(resolutions.stop)
			translator := newEventTranslator(events, turns)
			translator.handlePermissions(sessionID, resolutions)
			sdkTurnID := "sdk-turn"
			translator.handle(copilot.SessionEvent{ID: "tool-start", Timestamp: at, Data: &rpc.ToolExecutionStartData{
				ToolCallID: "tool-a", ToolName: "bash", TurnID: &sdkTurnID,
			}})
			Expect((<-events).Kind).To(Equal(copilotadapter.BridgeEventToolCall))
			toolCallID := "tool-a"
			permission := copilot.SessionEvent{ID: "permission-event-a", Timestamp: at.Add(time.Millisecond), Data: &rpc.PermissionRequestedData{
				RequestID: "sdk-permission-a",
				PermissionRequest: &rpc.PermissionRequestShell{
					ToolCallID: &toolCallID, FullCommandText: "git push", Intention: "publish the branch",
				},
			}}
			translator.handle(permission)
			opened := <-events
			expectedID := uuid.NewSHA1(sessionID, []byte("copilot.permission.event\x00"+permission.ID))
			Expect(opened).To(And(
				HaveField("ID", Equal(permission.ID)),
				HaveField("Kind", Equal(copilotadapter.BridgeEventRequestOpened)),
				HaveField("RequestID", PointTo(Equal(expectedID))),
				HaveField("RequestKind", Equal(copilotadapter.BridgeRequestPermission)),
				HaveField("TurnID", PointTo(Equal(turnID))),
				HaveField("ToolName", Equal(copilotadapter.ToolName("shell"))),
				HaveField("OccurredAt", Equal(permission.Timestamp)),
			))
			Expect(opened.Text).To(ContainSubstring("git push"))

			translator.handle(permission)
			Expect(events).To(HaveLen(0), "an SDK replay must not open another operator request")
		})

		It("surfaces unknown explicit tool and agent ownership immediately without guessing the active turn", func() {
			for _, event := range []copilot.SessionEvent{
				{ID: "unknown-tool", Data: &rpc.PermissionRequestedData{
					RequestID:         "sdk-unknown-tool",
					PermissionRequest: &rpc.PermissionRequestRead{ToolCallID: stringPointer("missing-tool"), Path: "/tmp/a"},
				}},
				{ID: "unknown-agent", AgentID: stringPointer("missing-agent"), Data: &rpc.PermissionRequestedData{
					RequestID:         "sdk-unknown-agent",
					PermissionRequest: &rpc.PermissionRequestRead{Path: "/tmp/b"},
				}},
			} {
				turns := newTurnCorrelator()
				turnID := uuid.New()
				Expect(turns.enqueue(turnID)).To(BeTrue())
				_, found := startTestTurn(turns, "active-sdk-turn")
				Expect(found).To(BeTrue())
				events := make(chan copilotadapter.BridgeEvent, 1)
				resolutions := newResolutionRegistry()
				translator := newEventTranslator(events, turns)
				translator.handlePermissions(uuid.New(), resolutions)

				translator.handle(event)
				Expect(<-events).To(And(
					HaveField("Kind", Equal(copilotadapter.BridgeEventRequestOpened)),
					HaveField("TurnID", BeNil()),
				))
				Expect(events).To(HaveLen(0), "unknown provenance must not wait for a later ownership event")
				resolutions.stop()
				turns.stop()
			}
		})

		It("uses exact subagent ownership and the root turn only when no explicit child owner exists", func() {
			turns := newTurnCorrelator()
			DeferCleanup(turns.stop)
			rootTurnID, agentTurnID := uuid.New(), uuid.New()
			Expect(turns.enqueue(rootTurnID)).To(BeTrue())
			_, found := startTestTurn(turns, "sdk-root")
			Expect(found).To(BeTrue())
			events := make(chan copilotadapter.BridgeEvent, 2)
			resolutions := newResolutionRegistry()
			DeferCleanup(resolutions.stop)
			translator := newEventTranslator(events, turns)
			translator.handlePermissions(uuid.New(), resolutions)
			translator.agentTurns["agent-a"] = agentTurnID

			translator.handle(copilot.SessionEvent{ID: "agent-permission", AgentID: stringPointer("agent-a"), Data: &rpc.PermissionRequestedData{
				RequestID: "sdk-agent", PermissionRequest: &rpc.PermissionRequestRead{Path: "/tmp/a"},
			}})
			Expect((<-events).TurnID).To(PointTo(Equal(agentTurnID)))

			translator.handle(copilot.SessionEvent{ID: "root-permission", Data: &rpc.PermissionRequestedData{
				RequestID: "sdk-root", PermissionRequest: &rpc.PermissionRequestRead{Path: "/tmp/b"},
			}})
			Expect((<-events).TurnID).To(PointTo(Equal(rootTurnID)))
		})

		It("does not open an operator request already resolved by a hook", func() {
			events := make(chan copilotadapter.BridgeEvent, 1)
			resolutions := newResolutionRegistry()
			DeferCleanup(resolutions.stop)
			translator := newEventTranslator(events, nil)
			translator.handlePermissions(uuid.New(), resolutions)
			resolved := true
			translator.handle(copilot.SessionEvent{ID: "hook-resolved", Data: &rpc.PermissionRequestedData{
				RequestID: "sdk-hook", ResolvedByHook: &resolved,
				PermissionRequest: &rpc.PermissionRequestRead{Path: "/tmp/a"},
			}})
			Expect(events).To(HaveLen(0))
			Expect(resolutions.bySDK).To(BeEmpty())
		})

		It("projects one exact completion and maps SDK approval and denial outcomes", func() {
			for _, fixture := range []struct {
				name     string
				result   rpc.PermissionResult
				decision string
			}{
				{name: "approved", result: &rpc.PermissionApproved{}, decision: "approve"},
				{name: "denied", result: &rpc.PermissionDeniedInteractivelyByUser{}, decision: "deny"},
			} {
				By(fixture.name)
				events := make(chan copilotadapter.BridgeEvent, 2)
				resolutions := newResolutionRegistry()
				translator := newEventTranslator(events, nil)
				sessionID := uuid.New()
				translator.handlePermissions(sessionID, resolutions)
				translator.handle(copilot.SessionEvent{ID: "opened-" + fixture.name, Data: &rpc.PermissionRequestedData{
					RequestID:         "sdk-" + fixture.name,
					PermissionRequest: &rpc.PermissionRequestRead{Path: "/tmp/a"},
				}})
				opened := <-events
				translator.handle(copilot.SessionEvent{ID: "completed-" + fixture.name, Timestamp: at, Data: &rpc.PermissionCompletedData{
					RequestID: "sdk-" + fixture.name, Result: fixture.result,
				}})
				completed := <-events
				Expect(completed).To(And(
					HaveField("ID", Equal("completed-"+fixture.name)),
					HaveField("Kind", Equal(copilotadapter.BridgeEventRequestCompleted)),
					HaveField("RequestID", Equal(opened.RequestID)),
					HaveField("ResolutionDecision", Equal(fixture.decision)),
					HaveField("OccurredAt", Equal(at)),
				))
				translator.handle(copilot.SessionEvent{ID: "completed-replay-" + fixture.name, Data: &rpc.PermissionCompletedData{
					RequestID: "sdk-" + fixture.name, Result: fixture.result,
				}})
				Expect(events).To(HaveLen(0))
				resolutions.stop()
			}
		})
	})
})

func stringPointer(value string) *string {
	return &value
}
