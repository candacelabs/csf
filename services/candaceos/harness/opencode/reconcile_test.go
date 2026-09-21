package opencode

import (
	"context"
	"sync"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

// projectionRetryCase describes one class of projected event whose publication
// the host refuses, and what the runtime must do once the host recovers.
type projectionRetryCase struct {
	// matches selects the event class the host refuses.
	matches func(event *candaceosv1.HarnessEvent) bool
	// arrange advances the provider transcript so the class is projected.
	arrange func(script *providerScript, parentMessageID string)
	// expectsIdle reports whether the turn concludes once the class lands.
	expectsIdle bool
	// expectedToolStarts is how many tool starts the arrangement produces; a
	// retried completion must not replay its already-published start.
	expectedToolStarts int
}

// expectProjectionRetry drives one refused projection to recovery: nothing is
// published while the host refuses, nothing is skipped when it recovers, and
// nothing is published twice.
func expectProjectionRetry(ctx SpecContext, testCase projectionRetryCase) {
	GinkgoHelper()
	gate := rejectUntilAllowed(testCase.matches)
	fixture := startFixture(ctx, withPublishInterceptor(gate.intercept))

	const prompt = "retry projected event"
	fixture.sendImmediate(ctx, "run-projection-retry", prompt)
	parentMessageID := fixture.script.promptMessageID(prompt)
	Expect(parentMessageID).NotTo(BeEmpty())
	testCase.arrange(fixture.script, parentMessageID)
	fixture.script.publishSessionEvent("session.status")

	Eventually(gate.countingAttempts()).Should(BeNumerically(">=", 1))
	Consistently(fixture.events.counting(testCase.matches), quiescence).Should(BeZero())
	Consistently(fixture.events.counting(isIdle), quiescence).Should(BeZero())

	gate.allow()
	fixture.script.publishSessionEvent("session.status")

	Eventually(fixture.events.counting(testCase.matches)).Should(Equal(1))
	Eventually(gate.countingAttempts()).Should(BeNumerically(">=", 2))
	Eventually(fixture.events.counting(isToolStarted)).Should(Equal(testCase.expectedToolStarts))
	if testCase.expectsIdle {
		Eventually(fixture.events.counting(isIdle)).Should(Equal(1))
	} else {
		Consistently(fixture.events.counting(isIdle), quiescence).Should(BeZero())
	}
	Consistently(fixture.events.counting(testCase.matches), quiescence).Should(Equal(1))
}

var _ = Describe("OpenCode publication retry", func() {
	It("replays a provider-accepted user after host publication recovers", func(ctx SpecContext) {
		gate := rejectOnce(isUserMessage)
		fixture := startFixture(ctx, withPublishInterceptor(gate.intercept))

		fixture.sendImmediate(ctx, "run-publish-failure", "provider accepted this")

		Eventually(fixture.events.all).Should(ContainElement(And(
			HaveField("RunId", "run-publish-failure"),
			WithTransform(userContent, Equal("provider accepted this")),
		)))
		Expect(gate.attemptCount()).To(BeNumerically(">=", 2))
	})

	It("retries an assistant delta after host publication recovers", func(ctx SpecContext) {
		expectProjectionRetry(ctx, projectionRetryCase{
			matches: deltaFor("msg_retry_delta"),
			arrange: func(script *providerScript, parentMessageID string) {
				script.streamAssistant(parentMessageID, "msg_retry_delta", "retry delta")
			},
		})
	})

	It("retries a final assistant message before publishing idle", func(ctx SpecContext) {
		expectProjectionRetry(ctx, projectionRetryCase{
			matches: finalAssistantSaying("final after retry"),
			arrange: func(script *providerScript, _ string) {
				script.completeLatest("final after retry")
			},
			expectsIdle: true,
		})
	})

	It("retries a tool completion without replaying its published start", func(ctx SpecContext) {
		expectProjectionRetry(ctx, projectionRetryCase{
			matches: toolCompletionFor("call_retry_projection"),
			arrange: func(script *providerScript, parentMessageID string) {
				script.completeWithTool(parentMessageID, "call_retry_projection")
			},
			expectsIdle:        true,
			expectedToolStarts: 1,
		})
	})

	It("retries idle after host publication recovers", func(ctx SpecContext) {
		gate := rejectUntilAllowed(isIdle)
		fixture := startFixture(ctx, withPublishInterceptor(gate.intercept))

		fixture.sendImmediate(ctx, "run-idle-retry", "retry idle")
		fixture.script.completeLatest("complete before idle retry")
		fixture.script.publishSessionEvent("session.status")

		Eventually(gate.countingAttempts()).Should(BeNumerically(">=", 1))
		Consistently(fixture.events.counting(isIdle), quiescence).Should(BeZero())

		gate.allow()
		fixture.script.publishSessionEvent("session.status")

		Eventually(fixture.events.counting(isIdle)).Should(Equal(1))
		Eventually(gate.countingAttempts()).Should(BeNumerically(">=", 2))
		Consistently(
			fixture.events.counting(finalAssistantSaying("complete before idle retry")), quiescence,
		).Should(Equal(1))
	})

	It("retries terminal publication without resubmitting or publishing idle", func(ctx SpecContext) {
		gate := rejectOnce(isError)
		fixture := startFixture(ctx, withPublishInterceptor(gate.intercept))

		fixture.sendImmediate(ctx, "run-terminal-retry", "submit once")
		fixture.script.failLatest("ProviderAuthError", "retry this publication")
		fixture.script.publishSessionEvent(sessionErrorEvent)

		Eventually(fixture.events.counting(isError)).Should(Equal(1))
		Expect(gate.attemptCount()).To(BeNumerically(">=", 2))
		Consistently(fixture.events.counting(isIdle), quiescence).Should(BeZero())
		Consistently(fixture.script.promptContents, quiescence).Should(Equal([]string{"submit once"}))
	})
})

var _ = Describe("OpenCode reconciliation ordering", func() {
	It("serializes terminal publication before accepting newer guidance", func(ctx SpecContext) {
		idleStarted := make(chan struct{})
		releaseIdle := make(chan struct{})
		var idleOnce, releaseOnce sync.Once
		release := func() { releaseOnce.Do(func() { close(releaseIdle) }) }
		fixture := startFixture(ctx, withPublishInterceptor(
			func(_ context.Context, event *candaceosv1.HarnessEvent) error {
				if !isIdle(event) {
					return nil
				}
				idleOnce.Do(func() { close(idleStarted) })
				<-releaseIdle
				return nil
			},
		))
		DeferCleanup(release)

		fixture.sendImmediate(ctx, "run-terminal-fence", "first")
		fixture.script.completeLatest("done")
		fixture.script.publishSessionEvent("session.status")
		Eventually(idleStarted).Should(BeClosed())

		sendResult := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			sendResult <- fixture.runtime.Send(ctx, testPrompt(
				"run-new", "after terminal", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE,
			))
		}()
		Consistently(fixture.script.promptContents, quiescence).Should(Equal([]string{"first"}))

		release()
		Eventually(sendResult).Should(Receive(Succeed()))
		Eventually(fixture.script.promptContents).Should(Equal([]string{"first", "after terminal"}))
	})

	It("does not complete from a stale message snapshot followed by newer idle", func(ctx SpecContext) {
		fixture := startFixture(ctx, withQueueCapacity(4))
		const runID = "run-coherent-terminal"

		fixture.sendImmediate(ctx, runID, "finish coherently")
		staleServed, releaseFresh := fixture.script.completeAfterStaleRead("coherent answer")
		DeferCleanup(releaseFresh)
		fixture.script.publishSessionEvent("session.status")

		Eventually(staleServed).Should(BeClosed())
		Consistently(fixture.events.counting(isIdle), quiescence).Should(BeZero())
		Expect(fixture.events.all()).NotTo(ContainElement(
			WithTransform(assistantContent, Equal("coherent answer")),
		))

		releaseFresh()
		Eventually(fixture.events.all).Should(ContainElement(And(
			HaveField("RunId", runID),
			WithTransform(assistantContent, Equal("coherent answer")),
		)))
		Eventually(fixture.events.counting(isIdle)).Should(Equal(1))
	})
})
