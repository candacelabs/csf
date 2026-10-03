package opencode

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("OpenCode transcript projection", func() {
	It("consumes late historical projections before the current run", func(ctx SpecContext) {
		fence := &runFence{}
		fixture := startFixture(ctx, withPublishInterceptor(fence.intercept))

		fixture.sendImmediate(ctx, "run-old", "historical")
		historicalParentID := fixture.script.promptMessageID("historical")
		Expect(historicalParentID).NotTo(BeEmpty())
		fixture.script.completeLatest("historical complete")
		Eventually(fixture.events.counting(isIdle)).Should(Equal(1))

		fixture.sendImmediate(ctx, "run-current", "current")
		currentParentID := fixture.script.promptMessageID("current")
		Expect(currentParentID).NotTo(BeEmpty())
		fence.pin("run-current")
		fixture.script.appendHistoricalReplies(historicalParentID)
		fixture.script.streamAssistant(currentParentID, "msg_current_delta", "current progress")
		fixture.script.publishSessionEvent("message.updated")

		Eventually(fixture.events.counting(deltaFor("msg_current_delta"))).Should(Equal(1))
		Expect(fence.rejectionCount()).To(BeZero(), "a superseded run reached the host")
		for _, event := range fixture.events.all() {
			Expect(event.GetAssistantDelta().GetMessageId()).NotTo(Equal("msg_late_delta"))
			Expect(event.GetAssistantMessage().GetMessageId()).NotTo(Equal("msg_late_final"))
			Expect(event.GetToolStarted().GetToolCallId()).NotTo(Equal("call_late_tool"))
			Expect(event.GetToolCompleted().GetToolCallId()).NotTo(Equal("call_late_tool"))
		}
	})

	It("does not let a late historical error fail the replacement", func(ctx SpecContext) {
		fixture := startFixture(ctx,
			withQueueCapacity(4),
			withScript(newProviderScript().withAbortError(abortedErrorName, "Aborted", false)),
		)
		const runID = "run-historical-error"

		fixture.sendImmediate(ctx, runID, "old")
		supersededParentID := fixture.script.promptMessageID("old")
		Expect(supersededParentID).NotTo(BeEmpty())
		fixture.sendImmediate(ctx, runID, "replacement")
		Eventually(fixture.script.requestOrder).
			Should(Equal([]string{"prompt:old", "abort", "prompt:replacement"}))

		fixture.script.appendError(supersededParentID, "UnknownError", "late historical failure")
		fixture.script.publishSessionEvent("message.updated")

		Consistently(fixture.events.all, quiescence).ShouldNot(ContainElement(
			WithTransform(failureMessage, Equal("late historical failure")),
		))
		fixture.script.completeLatest("replacement answer")
		Eventually(fixture.events.counting(isIdle)).Should(Equal(1))
	})
})
