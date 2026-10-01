package copilotbridge

import (
	"context"
	"errors"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	api "github.com/candacelabs/csf/services/copilot-adapter/gen/api"
)

func startTestTurn(turns *turnCorrelator, sdkTurnID string) (uuid.UUID, bool) {
	transition := turns.start(sdkTurnID, sdkTurnID)
	return transition.owner, transition.found
}

var _ = Describe("turn correlation", func() {
	It("keeps every SDK tool-loop iteration on its interaction owner", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		Expect(turns.enqueue(firstID)).To(BeTrue())
		Expect(turns.enqueue(secondID)).To(BeTrue())

		first := turns.start("interaction-a", "0")
		Expect(first.found).To(BeTrue())
		Expect(first.owner).To(Equal(firstID))
		Expect(first.started).To(Equal([]uuid.UUID{firstID}))
		turns.end("0")
		continuation := turns.start("interaction-a", "1")
		Expect(continuation.found).To(BeTrue())
		Expect(continuation.owner).To(Equal(firstID))
		Expect(continuation.started).To(BeEmpty())
		Expect(continuation.completed).To(BeEmpty())

		turns.observeUserMessage("interaction-b", userMessageDeliveryQueued)
		next := turns.start("interaction-b", "0")
		Expect(next.found).To(BeTrue())
		Expect(next.owner).To(Equal(secondID))
		Expect(next.completed).To(Equal([]uuid.UUID{firstID}))
		Expect(next.started).To(Equal([]uuid.UUID{secondID}))
	})

	It("advances rapid steering owners in SDK user-message order", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		active, queued := uuid.New(), uuid.New()
		firstSteer, secondSteer := uuid.New(), uuid.New()
		Expect(turns.enqueue(active)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-active")
		Expect(found).To(BeTrue())
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error { return nil })
		delivery, err := sender.send(context.Background(), copilotadapter.BridgePrompt{
			TurnID: queued, Mode: string(api.Queue),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryAccepted))
		delivery, err = sender.send(context.Background(), copilotadapter.BridgePrompt{
			TurnID: firstSteer, Mode: string(api.Steer),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryAccepted))
		delivery, err = sender.send(context.Background(), copilotadapter.BridgePrompt{
			TurnID: secondSteer, Mode: string(api.Steer),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryAccepted))

		first := turns.observeUserMessage("sdk-active", userMessageDeliverySteering)
		Expect(first.completed).To(Equal([]uuid.UUID{active}))
		Expect(first.started).To(Equal([]uuid.UUID{firstSteer}))
		second := turns.observeUserMessage("sdk-active", userMessageDeliverySteering)
		Expect(second.completed).To(Equal([]uuid.UUID{firstSteer}))
		Expect(second.started).To(Equal([]uuid.UUID{secondSteer}))
		turns.observeUserMessage("sdk-queued", userMessageDeliveryQueued)
		started := turns.start("sdk-queued", "0")
		Expect(started.found).To(BeTrue())
		Expect(started.owner).To(Equal(queued))
		Expect(started.completed).To(Equal([]uuid.UUID{secondSteer}))
	})

	It("starts and supersedes steering rows even when no prior owner is active", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstSteer, secondSteer := uuid.New(), uuid.New()
		Expect(turns.enqueueImmediate(firstSteer)).To(BeTrue())
		Expect(turns.enqueueImmediate(secondSteer)).To(BeTrue())

		first := turns.observeUserMessage("interaction", userMessageDeliverySteering)
		Expect(first.completed).To(BeEmpty())
		Expect(first.started).To(Equal([]uuid.UUID{firstSteer}))
		second := turns.observeUserMessage("interaction", userMessageDeliverySteering)
		Expect(second.completed).To(Equal([]uuid.UUID{firstSteer}))
		Expect(second.started).To(Equal([]uuid.UUID{secondSteer}))
	})

	It("prebinds idle and queued user messages in serialized submission order across lanes", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		queued, immediate := uuid.New(), uuid.New()
		Expect(turns.enqueue(queued)).To(BeTrue())
		Expect(turns.enqueueImmediate(immediate)).To(BeTrue())
		turns.observeUserMessage("first", userMessageDeliveryQueued)
		Expect(turns.start("first", "0").owner).To(Equal(queued))
		turns.idle()
		turns.observeUserMessage("second", userMessageDeliveryIdle)
		Expect(turns.start("second", "0").owner).To(Equal(immediate))

		reversed := newTurnCorrelator()
		DeferCleanup(reversed.stop)
		Expect(reversed.enqueueImmediate(immediate)).To(BeTrue())
		Expect(reversed.enqueue(queued)).To(BeTrue())
		reversed.observeUserMessage("first", userMessageDeliveryQueued)
		Expect(reversed.start("first", "0").owner).To(Equal(immediate))

		fallback := newTurnCorrelator()
		DeferCleanup(fallback.stop)
		Expect(fallback.enqueue(queued)).To(BeTrue())
		Expect(fallback.enqueueImmediate(immediate)).To(BeTrue())
		Expect(fallback.start("without-user-event", "0").owner).To(Equal(queued))

		reversedFallback := newTurnCorrelator()
		DeferCleanup(reversedFallback.stop)
		Expect(reversedFallback.enqueueImmediate(immediate)).To(BeTrue())
		Expect(reversedFallback.enqueue(queued)).To(BeTrue())
		Expect(reversedFallback.start("without-user-event", "0").owner).To(Equal(immediate))
	})

	It("treats a prebound queued interaction as the boundary after a restored running prompt", func() {
		restoredID, queuedID := uuid.New(), uuid.New()
		turns := newRestoredTurnCorrelator([]copilotadapter.BridgeRestoredTurn{
			{ID: restoredID, Mode: api.Queue, Status: api.TurnStatusRunning},
			{ID: queuedID, Mode: api.Queue, Status: api.TurnStatusQueued},
		})
		DeferCleanup(turns.stop)
		transition := turns.observeUserMessage("interaction-queued", userMessageDeliveryQueued)
		Expect(transition.completed).To(Equal([]uuid.UUID{restoredID}))
		Expect(transition.started).To(Equal([]uuid.UUID{queuedID}))
		started := turns.start("interaction-queued", "0")
		Expect(started.owner).To(Equal(queuedID))
		Expect(started.completed).To(BeEmpty())
	})

	It("infers a missing interaction only from an exact prebind or the active loop", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		Expect(turns.enqueue(firstID)).To(BeTrue())
		Expect(turns.enqueue(secondID)).To(BeTrue())
		Expect(turns.start("interaction-a", "0").owner).To(Equal(firstID))
		turns.end("0")
		continuation := turns.start("", "1")
		Expect(continuation.owner).To(Equal(firstID))
		Expect(continuation.completed).To(BeEmpty())

		turns.observeUserMessage("interaction-b", userMessageDeliveryQueued)
		queued := turns.start("", "0")
		Expect(queued.owner).To(Equal(secondID))
		Expect(queued.completed).To(Equal([]uuid.UUID{firstID}))
	})

	It("preserves an interaction-less queued user-message boundary in FIFO order", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		Expect(turns.enqueue(firstID)).To(BeTrue())
		Expect(turns.enqueue(secondID)).To(BeTrue())
		Expect(turns.start("interaction-a", "0").owner).To(Equal(firstID))

		boundary := turns.observeUserMessage("", userMessageDeliveryQueued)
		Expect(boundary.completed).To(BeEmpty(), "the active SDK iteration still owns output until the next start")
		Expect(boundary.started).To(BeEmpty())
		started := turns.start("", "0")
		Expect(started.owner).To(Equal(secondID))
		Expect(started.completed).To(Equal([]uuid.UUID{firstID}))
		Expect(started.started).To(Equal([]uuid.UUID{secondID}))

		completed := turns.idle()
		Expect(completed.completed).To(Equal([]uuid.UUID{secondID}))
		Expect(completed.sessionBusy).To(BeFalse())
	})

	It("rekeys anonymous prebinds from explicit starts in FIFO order", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		activeID, firstQueuedID, secondQueuedID := uuid.New(), uuid.New(), uuid.New()
		Expect(turns.enqueue(activeID)).To(BeTrue())
		Expect(turns.enqueue(firstQueuedID)).To(BeTrue())
		Expect(turns.enqueue(secondQueuedID)).To(BeTrue())
		Expect(turns.start("interaction-active", "0").owner).To(Equal(activeID))

		turns.observeUserMessage("", userMessageDeliveryQueued)
		turns.observeUserMessage("", userMessageDeliveryQueued)
		first := turns.start("interaction-first", "0")
		Expect(first.found).To(BeTrue())
		Expect(first.owner).To(Equal(firstQueuedID))
		Expect(first.completed).To(Equal([]uuid.UUID{activeID}))
		Expect(first.started).To(Equal([]uuid.UUID{firstQueuedID}))

		second := turns.start("interaction-second", "0")
		Expect(second.found).To(BeTrue())
		Expect(second.owner).To(Equal(secondQueuedID))
		Expect(second.completed).To(Equal([]uuid.UUID{firstQueuedID}))
		Expect(second.started).To(Equal([]uuid.UUID{secondQueuedID}))

		completed := turns.idle()
		Expect(completed.completed).To(Equal([]uuid.UUID{secondQueuedID}))
		Expect(completed.sessionBusy).To(BeFalse())
	})

	It("prefers an exact prebind without consuming anonymous fallback", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		activeID, anonymousID, explicitID := uuid.New(), uuid.New(), uuid.New()
		Expect(turns.enqueue(activeID)).To(BeTrue())
		Expect(turns.enqueue(anonymousID)).To(BeTrue())
		Expect(turns.enqueue(explicitID)).To(BeTrue())
		Expect(turns.start("interaction-active", "0").owner).To(Equal(activeID))

		turns.observeUserMessage("", userMessageDeliveryQueued)
		turns.observeUserMessage("interaction-explicit", userMessageDeliveryQueued)
		exact := turns.start("interaction-explicit", "0")
		Expect(exact.found).To(BeTrue())
		Expect(exact.owner).To(Equal(explicitID))

		fallback := turns.start("interaction-anonymous", "0")
		Expect(fallback.found).To(BeTrue())
		Expect(fallback.owner).To(Equal(anonymousID))
		Expect(turns.idle().sessionBusy).To(BeFalse())
	})

	It("rekeys an anonymous current owner when the explicit start follows turn end", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		activeID, queuedID := uuid.New(), uuid.New()
		Expect(turns.enqueue(activeID)).To(BeTrue())
		Expect(turns.enqueue(queuedID)).To(BeTrue())
		Expect(turns.start("interaction-active", "0").owner).To(Equal(activeID))

		turns.end("0")
		boundary := turns.observeUserMessage("", userMessageDeliveryQueued)
		Expect(boundary.completed).To(Equal([]uuid.UUID{activeID}))
		Expect(boundary.started).To(Equal([]uuid.UUID{queuedID}))
		started := turns.start("interaction-queued", "0")
		Expect(started.found).To(BeTrue())
		Expect(started.owner).To(Equal(queuedID))
		Expect(started.completed).To(BeEmpty())
		Expect(started.started).To(BeEmpty())

		completed := turns.idle()
		Expect(completed.completed).To(Equal([]uuid.UUID{queuedID}))
		Expect(completed.sessionBusy).To(BeFalse())
	})

	It("consumes a queued anonymous prebind before enriching an anonymous current owner", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		activeID, queuedID := uuid.New(), uuid.New()
		Expect(turns.enqueue(activeID)).To(BeTrue())
		Expect(turns.enqueue(queuedID)).To(BeTrue())

		turns.observeUserMessage("", userMessageDeliveryIdle)
		Expect(turns.start("", "0").owner).To(Equal(activeID))
		turns.observeUserMessage("", userMessageDeliveryQueued)
		turns.end("0")

		started := turns.start("interaction-queued", "0")
		Expect(started.found).To(BeTrue())
		Expect(started.owner).To(Equal(queuedID))
		Expect(started.completed).To(Equal([]uuid.UUID{activeID}))
		Expect(started.started).To(Equal([]uuid.UUID{queuedID}))
		Expect(turns.idle().sessionBusy).To(BeFalse())
	})

	DescribeTable("keeps an anonymous continuation before consuming its queued anonymous prebind",
		func(continuationInteraction string, queuedInteraction string) {
			turns := newTurnCorrelator()
			DeferCleanup(turns.stop)
			activeID, queuedID := uuid.New(), uuid.New()
			Expect(turns.enqueue(activeID)).To(BeTrue())
			Expect(turns.enqueue(queuedID)).To(BeTrue())

			turns.observeUserMessage("", userMessageDeliveryIdle)
			Expect(turns.start("", "0").owner).To(Equal(activeID))
			turns.observeUserMessage("", userMessageDeliveryQueued)
			turns.end("0")

			continuation := turns.start(continuationInteraction, "1")
			Expect(continuation.found).To(BeTrue())
			Expect(continuation.owner).To(Equal(activeID))
			Expect(continuation.completed).To(BeEmpty())
			Expect(continuation.started).To(BeEmpty())
			turns.end("1")

			started := turns.start(queuedInteraction, "0")
			Expect(started.found).To(BeTrue())
			Expect(started.owner).To(Equal(queuedID))
			Expect(started.completed).To(Equal([]uuid.UUID{activeID}))
			Expect(started.started).To(Equal([]uuid.UUID{queuedID}))
			Expect(turns.idle().sessionBusy).To(BeFalse())
		},
		Entry("when later starts supply interaction ids", "interaction-active", "interaction-queued"),
		Entry("when later starts omit interaction ids", "", ""),
	)

	It("normalizes unidentified direct and restored starts for later explicit output", func() {
		directID := uuid.New()
		direct := newTurnCorrelator()
		DeferCleanup(direct.stop)
		Expect(direct.enqueue(directID)).To(BeTrue())
		Expect(direct.start("", "0").owner).To(Equal(directID))
		mapped, found := direct.lookup("interaction-direct", "0")
		Expect(found).To(BeTrue())
		Expect(mapped).To(Equal(directID))

		restoredID := uuid.New()
		restored := newRestoredTurnCorrelator([]copilotadapter.BridgeRestoredTurn{{
			ID: restoredID, Mode: api.Queue, Status: api.TurnStatusRunning,
		}})
		DeferCleanup(restored.stop)
		started := restored.start("", "0")
		Expect(started.found).To(BeTrue())
		Expect(started.owner).To(Equal(restoredID))
		mapped, found = restored.lookup("interaction-restored", "0")
		Expect(found).To(BeTrue())
		Expect(mapped).To(Equal(restoredID))
	})

	It("retains lifecycle tombstones when root idle observes queued work", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		activeID, queuedID := uuid.New(), uuid.New()
		Expect(turns.enqueue(activeID)).To(BeTrue())
		Expect(turns.enqueue(queuedID)).To(BeTrue())
		Expect(turns.start("interaction-active", "0").owner).To(Equal(activeID))
		turns.observeUserMessage("interaction-queued", userMessageDeliveryQueued)

		idle := turns.idle()
		Expect(idle.completed).To(Equal([]uuid.UUID{activeID}))
		Expect(idle.sessionBusy).To(BeTrue())
		var retained bool
		Expect(turns.run(func(state *turnCorrelationState) {
			retained = state.closedInteractions["interaction-active"] &&
				state.started[activeID] && state.terminal[activeID]
		})).To(BeTrue())
		Expect(retained).To(BeTrue())
		Expect(turns.start("interaction-queued", "0").owner).To(Equal(queuedID))
		Expect(turns.idle().sessionBusy).To(BeFalse())
	})

	It("bounds lifecycle correlation state at every no-work root idle", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		for range 10_000 {
			identifier := uuid.New()
			Expect(turns.enqueue(identifier)).To(BeTrue())
			Expect(turns.start(identifier.String(), "0").owner).To(Equal(identifier))
			turns.end("0")
			Expect(turns.idle().completed).To(Equal([]uuid.UUID{identifier}))
		}

		var bySDK, closedInteractions, started, terminal int
		Expect(turns.run(func(state *turnCorrelationState) {
			bySDK = len(state.bySDK)
			closedInteractions = len(state.closedInteractions)
			started = len(state.started)
			terminal = len(state.terminal)
		})).To(BeTrue())
		Expect([]int{bySDK, closedInteractions, started, terminal}).To(Equal([]int{0, 0, 0, 0}))
	})

	It("serializes enqueue with the SDK invocation under concurrent sends", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		invoked := make(chan uuid.UUID, 2)
		releaseFirst := make(chan struct{})
		sender := newTurnSender(turns, func(_ context.Context, prompt copilotadapter.BridgePrompt) error {
			invoked <- prompt.TurnID
			if prompt.TurnID == firstID {
				<-releaseFirst
			}
			return nil
		})
		type sendResult struct {
			delivery copilotadapter.BridgePromptDelivery
			err      error
		}
		firstDone, secondDone := make(chan sendResult, 1), make(chan sendResult, 1)
		go func() {
			delivery, err := sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: firstID})
			firstDone <- sendResult{delivery: delivery, err: err}
		}()
		Expect(<-invoked).To(Equal(firstID))
		secondStarted := make(chan struct{})
		go func() {
			close(secondStarted)
			delivery, err := sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: secondID})
			secondDone <- sendResult{delivery: delivery, err: err}
		}()
		<-secondStarted
		close(releaseFirst)
		firstResult := <-firstDone
		Expect(firstResult.err).NotTo(HaveOccurred())
		Expect(firstResult.delivery).To(Equal(copilotadapter.BridgePromptDeliveryAccepted))
		Expect(<-invoked).To(Equal(secondID))
		secondResult := <-secondDone
		Expect(secondResult.err).NotTo(HaveOccurred())
		Expect(secondResult.delivery).To(Equal(copilotadapter.BridgePromptDeliveryAccepted))

		mapped, found := startTestTurn(turns, "sdk-first")
		Expect(found).To(BeTrue())
		Expect(mapped).To(Equal(firstID))
		mapped, found = startTestTurn(turns, "sdk-second")
		Expect(found).To(BeTrue())
		Expect(mapped).To(Equal(secondID))
	})

	It("reports a definite pre-dispatch rejection when the correlator is closed", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		invoked := false
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error {
			invoked = true
			return nil
		})
		turns.stop()

		delivery, err := sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: uuid.New()})
		Expect(err).To(MatchError("copilot bridge: the session is closed"))
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryRejected))
		Expect(invoked).To(BeFalse())
	})

	It("keeps an ambiguous send correlatable when turn_start arrives after the error", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error {
			return context.DeadlineExceeded
		})

		delivery, err := sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: identifier})
		Expect(err).To(MatchError(context.DeadlineExceeded))
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryUnknown))
		mapped, found := startTestTurn(turns, "sdk-late")
		Expect(found).To(BeTrue())
		Expect(mapped).To(Equal(identifier))
	})

	It("keeps an ambiguous send correlatable when turn_start arrives before the error", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error {
			mapped, found := startTestTurn(turns, "sdk-early")
			Expect(found).To(BeTrue())
			Expect(mapped).To(Equal(identifier))
			return context.Canceled
		})

		delivery, err := sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: identifier})
		Expect(err).To(MatchError(context.Canceled))
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryUnknown))
		turns.end("sdk-early")
		completed := turns.idle()
		Expect(completed.completed).To(Equal([]uuid.UUID{identifier}))
	})

	It("does not redeliver or shift past an unresolved prompt", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		invocations := 0
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error {
			invocations++
			return context.DeadlineExceeded
		})

		delivery, err := sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: firstID})
		Expect(err).To(MatchError(context.DeadlineExceeded))
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryUnknown))
		delivery, err = sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: firstID})
		Expect(err).NotTo(HaveOccurred())
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryUnknown))
		delivery, err = sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: secondID})
		Expect(err).To(MatchError("copilot bridge: an earlier prompt delivery is unresolved"))
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryRejected))
		Expect(invocations).To(Equal(1))

		mapped, found := startTestTurn(turns, "sdk-first")
		Expect(found).To(BeTrue())
		Expect(mapped).To(Equal(firstID))
		delivery, err = sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: firstID})
		Expect(err).NotTo(HaveOccurred())
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryAccepted))
		Expect(invocations).To(Equal(1))
	})

	It("acknowledges a repeated accepted turn without invoking the SDK twice", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		invocations := 0
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error {
			invocations++
			return nil
		})

		delivery, err := sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: identifier})
		Expect(err).NotTo(HaveOccurred())
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryAccepted))
		delivery, err = sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: identifier})
		Expect(err).NotTo(HaveOccurred())
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryAccepted))
		Expect(invocations).To(Equal(1))
	})

	It("retains an accepted tombstone when SDK start and end beat the durable acknowledgement", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		invocations := 0
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error {
			invocations++
			return nil
		})

		delivery, err := sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: identifier})
		Expect(err).NotTo(HaveOccurred())
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryAccepted))
		started, found := startTestTurn(turns, "sdk-fast")
		Expect(found).To(BeTrue())
		Expect(started).To(Equal(identifier))
		turns.end("sdk-fast")
		completed := turns.idle()
		Expect(completed.completed).To(Equal([]uuid.UUID{identifier}))

		delivery, err = sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: identifier})
		Expect(err).NotTo(HaveOccurred())
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryAccepted))
		Expect(invocations).To(Equal(1))
		turns.acknowledgeDelivery(identifier)
	})

	It("does not resurrect an acknowledged tombstone when SDK start and end arrive later", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error { return nil })

		delivery, err := sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: identifier})
		Expect(err).NotTo(HaveOccurred())
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryAccepted))
		turns.acknowledgeDelivery(identifier)

		started, found := startTestTurn(turns, "sdk-after-ack")
		Expect(found).To(BeTrue())
		Expect(started).To(Equal(identifier))
		turns.end("sdk-after-ack")
		completed := turns.idle()
		Expect(completed.completed).To(Equal([]uuid.UUID{identifier}))
		_, found = turns.delivery(identifier)
		Expect(found).To(BeFalse())
	})

	It("serializes abort against a newly submitted turn and returns the event-time target", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		Expect(turns.enqueue(firstID)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-a")
		Expect(found).To(BeTrue())
		invoked := make(chan uuid.UUID, 1)
		sender := newTurnSender(turns, func(_ context.Context, prompt copilotadapter.BridgePrompt) error {
			invoked <- prompt.TurnID
			return nil
		})
		waiter := &turnTerminationWaiter{}
		abortCalled := make(chan struct{})
		releaseAbort := make(chan struct{})
		abortResult := make(chan uuid.UUID, 1)
		abortErrors := make(chan error, 1)
		go func() {
			identifier, err := sender.abort(context.Background(), firstID, func(_ context.Context) error {
				close(abortCalled)
				<-releaseAbort
				return nil
			}, waiter)
			abortResult <- identifier
			abortErrors <- err
		}()
		<-abortCalled
		sendDone := make(chan error, 1)
		go func() {
			delivery, err := sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: secondID})
			Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryAccepted))
			sendDone <- err
		}()
		Consistently(invoked).ShouldNot(Receive())
		terminated, _, found := turns.terminateActive()
		Expect(found).To(BeTrue())
		waiter.publish(terminated)
		close(releaseAbort)
		Expect(<-abortErrors).To(Succeed())
		Expect(<-abortResult).To(Equal(firstID))
		Expect(<-invoked).To(Equal(secondID))
		Expect(<-sendDone).To(Succeed())
	})

	It("keeps a delayed abort event on its original turn before ordinary completion", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		Expect(turns.enqueue(firstID)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-a")
		Expect(found).To(BeTrue())
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error { return nil })
		waiter := &turnTerminationWaiter{}
		abortContext, cancelAbort := context.WithCancel(context.Background())
		invoked := false
		_, err := sender.abort(abortContext, firstID, func(_ context.Context) error {
			invoked = true
			cancelAbort()
			return abortContext.Err()
		}, waiter)
		Expect(err).To(MatchError(context.Canceled))
		Expect(invoked).To(BeTrue())

		delivery, err := sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: secondID})
		Expect(err).NotTo(HaveOccurred())
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryAccepted))
		aborted, busy, found := turns.terminateAborted()
		Expect(found).To(BeTrue())
		Expect(aborted).To(Equal(firstID))
		Expect(busy).To(BeTrue())
		started, found := startTestTurn(turns, "sdk-b")
		Expect(found).To(BeTrue())
		Expect(started).To(Equal(secondID))
		active, found := turns.active()
		Expect(found).To(BeTrue())
		Expect(active).To(Equal(secondID))
	})

	It("rejects an idle abort without invoking the SDK", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		invoked := false
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error { return nil })
		_, err := sender.abort(context.Background(), uuid.New(), func(_ context.Context) error {
			invoked = true
			return nil
		}, &turnTerminationWaiter{})
		Expect(err).To(MatchError(copilotadapter.ErrNoActiveTurn))
		Expect(invoked).To(BeFalse())
	})

	It("rejects a stale abort target without invoking the SDK", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		activeTurnID := uuid.New()
		Expect(turns.enqueue(activeTurnID)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-active")
		Expect(found).To(BeTrue())
		invoked := false
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error { return nil })
		_, err := sender.abort(context.Background(), uuid.New(), func(_ context.Context) error {
			invoked = true
			return nil
		}, &turnTerminationWaiter{})
		Expect(err).To(MatchError(copilotadapter.ErrAbortTargetMismatch))
		Expect(invoked).To(BeFalse())
		active, found := turns.active()
		Expect(found).To(BeTrue())
		Expect(active).To(Equal(activeTurnID))
	})

	It("retains an ambiguous abort target until its delayed event or ordinary completion", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		Expect(turns.enqueue(firstID)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-a")
		Expect(found).To(BeTrue())
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error { return nil })
		waiter := &turnTerminationWaiter{}
		invocations := 0
		_, err := sender.abort(context.Background(), firstID, func(_ context.Context) error {
			invocations++
			return errors.New("rpc rejected")
		}, waiter)
		Expect(err).To(MatchError("rpc rejected"))

		_, err = sender.abort(context.Background(), firstID, func(_ context.Context) error {
			invocations++
			return nil
		}, waiter)
		Expect(err).To(MatchError(copilotadapter.ErrAbortPending))
		Expect(invocations).To(Equal(1))

		delivery, err := sender.send(context.Background(), copilotadapter.BridgePrompt{TurnID: secondID})
		Expect(err).NotTo(HaveOccurred())
		Expect(delivery).To(Equal(copilotadapter.BridgePromptDeliveryAccepted))
		aborted, busy, found := turns.terminateAborted()
		Expect(found).To(BeTrue())
		Expect(aborted).To(Equal(firstID))
		Expect(busy).To(BeTrue())
		started, found := startTestTurn(turns, "sdk-b")
		Expect(found).To(BeTrue())
		Expect(started).To(Equal(secondID))
		active, found := turns.active()
		Expect(found).To(BeTrue())
		Expect(active).To(Equal(secondID))
	})

	It("completes normally and releases an ambiguous abort at an owner handoff", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		firstID, secondID := uuid.New(), uuid.New()
		Expect(turns.enqueue(firstID)).To(BeTrue())
		Expect(turns.start("interaction-a", "0").owner).To(Equal(firstID))
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error { return nil })
		_, err := sender.abort(context.Background(), firstID, func(_ context.Context) error {
			return errors.New("abort response was lost")
		}, &turnTerminationWaiter{})
		Expect(err).To(MatchError("abort response was lost"))

		Expect(turns.enqueue(secondID)).To(BeTrue())
		turns.end("0")
		transition := turns.observeUserMessage("interaction-b", userMessageDeliveryQueued)
		Expect(transition.completed).To(Equal([]uuid.UUID{firstID}))
		Expect(transition.started).To(Equal([]uuid.UUID{secondID}))
		_, _, found := turns.terminateAborted()
		Expect(found).To(BeFalse(), "late AbortData must not target the normally completed owner")

		target, started, err := turns.beginAbort(secondID)
		Expect(err).NotTo(HaveOccurred())
		Expect(started).To(BeTrue())
		Expect(target).To(Equal(secondID))
		turns.cancelAbort(secondID)
	})

	It("completes normally and releases an ambiguous abort at root idle", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		Expect(turns.enqueue(identifier)).To(BeTrue())
		Expect(turns.start("interaction", "0").owner).To(Equal(identifier))
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error { return nil })
		_, err := sender.abort(context.Background(), identifier, func(_ context.Context) error {
			return context.DeadlineExceeded
		}, &turnTerminationWaiter{})
		Expect(err).To(MatchError(context.DeadlineExceeded))

		turns.end("0")
		transition := turns.idle()
		Expect(transition.completed).To(Equal([]uuid.UUID{identifier}))
		Expect(transition.sessionBusy).To(BeFalse())
		_, _, found := turns.terminateAborted()
		Expect(found).To(BeFalse())
		_, started, err := turns.beginAbort(identifier)
		Expect(err).NotTo(HaveOccurred())
		Expect(started).To(BeFalse(), "normal completion leaves no active abort target")
	})

	It("accepts terminal SDK evidence that arrives before an abort error", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		Expect(turns.enqueue(identifier)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-a")
		Expect(found).To(BeTrue())
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error { return nil })
		waiter := &turnTerminationWaiter{}

		aborted, err := sender.abort(context.Background(), identifier, func(_ context.Context) error {
			terminated, _, eventFound := turns.terminateAborted()
			Expect(eventFound).To(BeTrue())
			waiter.publish(terminated)
			return errors.New("response lost after event")
		}, waiter)
		Expect(err).NotTo(HaveOccurred())
		Expect(aborted).To(Equal(identifier))
	})

	It("releases only a definitely pre-invocation cancellation", func() {
		turns := newTurnCorrelator()
		DeferCleanup(turns.stop)
		identifier := uuid.New()
		Expect(turns.enqueue(identifier)).To(BeTrue())
		_, found := startTestTurn(turns, "sdk-a")
		Expect(found).To(BeTrue())
		sender := newTurnSender(turns, func(_ context.Context, _ copilotadapter.BridgePrompt) error { return nil })
		waiter := &turnTerminationWaiter{}
		cancelled, cancel := context.WithCancel(context.Background())
		cancel()
		invocations := 0
		_, err := sender.abort(cancelled, identifier, func(_ context.Context) error {
			invocations++
			return nil
		}, waiter)
		Expect(err).To(MatchError(context.Canceled))
		Expect(invocations).To(BeZero())

		retried, err := sender.abort(context.Background(), identifier, func(_ context.Context) error {
			invocations++
			aborted, _, eventFound := turns.terminateAborted()
			Expect(eventFound).To(BeTrue())
			waiter.publish(aborted)
			return nil
		}, waiter)
		Expect(err).NotTo(HaveOccurred())
		Expect(retried).To(Equal(identifier))
		Expect(invocations).To(Equal(1))
	})

})
