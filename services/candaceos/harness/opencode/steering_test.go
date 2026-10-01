package opencode

import (
	"context"
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	opencodesdk "github.com/sst/opencode-sdk-go"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

const abortedErrorName = string(opencodesdk.AssistantMessageErrorNameMessageAbortedError)

// errAbortUnavailable stands in for any provider refusal of an abort. How a
// transport status becomes an error is the adapter's contract, not steering's.
var errAbortUnavailable = errors.New("provider refused the abort")

// toolWasInterrupted reports whether a tool completion carries the operator
// interruption marker instead of a failure.
func toolWasInterrupted(event *candaceosv1.HarnessEvent) bool {
	completed := event.GetToolCompleted()
	return completed != nil &&
		completed.GetSucceeded().GetResult().GetStructValue().GetFields()["interrupted"].GetBoolValue()
}

var _ = Describe("OpenCode prompt admission", func() {
	It("publishes a provider-accepted user on the session lifecycle, not on the request", func(ctx SpecContext) {
		requestCtx, cancelRequest := context.WithCancel(ctx)
		observed := &observedPublication{}
		fixture := startFixture(ctx, withPublishInterceptor(
			func(publishCtx context.Context, event *candaceosv1.HarnessEvent) error {
				if !isUserMessage(event) {
					return nil
				}
				cancelRequest()
				observed.record(publishCtx.Err())
				return nil
			},
		))

		fixture.send(
			requestCtx, "run-lifecycle", "accepted before request cancellation",
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE,
		)

		Expect(requestCtx.Err()).To(MatchError(context.Canceled))
		seen, publishErr := observed.result()
		Expect(seen).To(BeTrue(), "the runtime never published the accepted user message")
		Expect(publishErr).NotTo(HaveOccurred())
	})

	It("rejects and removes a queued prompt the host refuses", func(ctx SpecContext) {
		gate := rejectUntilAllowed(func(event *candaceosv1.HarnessEvent) bool {
			return userContent(event) == "queued before provider"
		})
		fixture := startFixture(ctx, withPublishInterceptor(gate.intercept))
		fixture.sendImmediate(ctx, "run-queue-publication", "active provider prompt")
		Eventually(fixture.script.promptContents).Should(Equal([]string{"active provider prompt"}))

		Expect(fixture.runtime.Send(ctx, testPrompt(
			"run-queue-publication", "queued before provider",
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE,
		))).To(MatchError(errPublishUnavailable))

		Consistently(fixture.script.promptContents, quiescence).
			Should(Equal([]string{"active provider prompt"}))
	})

	It("drains its bounded queue in FIFO order without intermediate idle", func(ctx SpecContext) {
		fixture := startFixture(ctx)
		const runID = "run-queue"

		fixture.sendImmediate(ctx, runID, "first")
		fixture.enqueue(ctx, runID, "second")
		fixture.enqueue(ctx, runID, "third")
		Expect(fixture.runtime.Send(ctx, testPrompt(
			runID, "overflow", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE,
		))).To(MatchError(ErrQueueFull))
		Eventually(fixture.script.promptContents).Should(Equal([]string{"first"}))

		fixture.script.completeLatest("first answer")
		Eventually(fixture.script.promptContents).Should(Equal([]string{"first", "second"}))
		Consistently(fixture.events.counting(isIdle), quiescence).Should(BeZero())
		fixture.script.completeLatest("second answer")
		Eventually(fixture.script.promptContents).Should(Equal([]string{"first", "second", "third"}))
		Consistently(fixture.events.counting(isIdle), quiescence).Should(BeZero())

		fixture.script.completeLatest("third answer")
		Eventually(fixture.events.counting(isIdle)).Should(Equal(1))
		Expect(fixture.script.requestOrder()).To(Equal([]string{"prompt:first", "prompt:second", "prompt:third"}))
	})

	It("discards queued guidance after the correlated terminal error", func(ctx SpecContext) {
		fixture := startFixture(ctx)
		const runID = "run-queue-error"

		fixture.sendImmediate(ctx, runID, "first")
		fixture.enqueue(ctx, runID, "queued")
		Eventually(fixture.script.promptContents).Should(Equal([]string{"first"}))
		fixture.script.failLatest("ProviderAuthError", "first prompt failed")
		fixture.script.publishSessionEvent(sessionErrorEvent)

		Eventually(fixture.events.all).Should(ContainElement(And(
			HaveField("RunId", runID),
			WithTransform(failureMessage, Equal("first prompt failed")),
		)))
		Consistently(fixture.script.promptContents, quiescence).Should(Equal([]string{"first"}))
	})
})

var _ = Describe("OpenCode active-turn steering", func() {
	It("aborts before immediate steering and fences the superseded terminal", func(ctx SpecContext) {
		fixture := startFixture(ctx,
			withQueueCapacity(4),
			withScript(newProviderScript().withAbortError(abortedErrorName, "Aborted", false)),
		)
		const runID = "run-steer"

		fixture.sendImmediate(ctx, runID, "old direction")
		fixture.sendImmediate(ctx, runID, "new direction")

		Eventually(fixture.script.requestOrder).
			Should(Equal([]string{"prompt:old direction", "abort", "prompt:new direction"}))
		Consistently(fixture.events.counting(isError), quiescence).Should(BeZero())
		Consistently(fixture.events.counting(isIdle), quiescence).Should(BeZero())

		fixture.script.completeLatest("new answer")
		Eventually(fixture.events.counting(isIdle)).Should(Equal(1))
	})

	It("does not suppress a genuine replacement error", func(ctx SpecContext) {
		fixture := startFixture(ctx,
			withQueueCapacity(4),
			withScript(newProviderScript().withAbortError(abortedErrorName, "Aborted", false)),
		)
		const runID = "run-replacement-error"

		fixture.sendImmediate(ctx, runID, "old")
		fixture.sendImmediate(ctx, runID, "replacement")
		fixture.script.failLatest("ProviderAuthError", "replacement provider failed")
		fixture.script.publishSessionEvent(sessionErrorEvent)

		Eventually(fixture.events.all).Should(ContainElement(And(
			HaveField("RunId", runID),
			WithTransform(failureMessage, Equal("replacement provider failed")),
		)))
		for _, event := range fixture.events.all() {
			Expect(failureMessage(event)).NotTo(Equal("Aborted"))
		}
	})

	It("keeps a provider-owned abort terminal", func(ctx SpecContext) {
		fixture := startFixture(ctx, withQueueCapacity(4))

		fixture.sendImmediate(ctx, "run-provider-abort", "provider aborts")
		fixture.script.failLatest(abortedErrorName, "provider aborted")

		Eventually(fixture.events.all).Should(ContainElement(
			WithTransform(failureMessage, Equal("provider aborted")),
		))
	})

	It("suppresses an operator abort and reports its interrupted tool", func(ctx SpecContext) {
		fixture := startFixture(ctx,
			withQueueCapacity(4),
			withScript(newProviderScript().withAbortError(abortedErrorName, "operator aborted", true)),
		)

		fixture.sendImmediate(ctx, "run-operator-abort", "stop this")
		Expect(fixture.runtime.Abort(ctx)).To(Succeed())

		Eventually(fixture.events.counting(isIdle)).Should(Equal(1))
		Expect(fixture.events.count(isError)).To(BeZero())
		Expect(fixture.events.all()).To(ContainElement(WithTransform(toolWasInterrupted, BeTrue())))
	})

	It("clears attached queued guidance on abort and preserves its run fence", func(ctx SpecContext) {
		fixture := startFixture(ctx,
			withScript(newProviderScript().withPhase(phaseBusy)),
			withConfiguredSession(fixtureSessionID),
		)
		const runID = "run-attached-abort"

		fixture.enqueue(ctx, runID, "drop this queued guidance")
		Expect(fixture.runtime.Abort(ctx)).To(Succeed())

		Eventually(fixture.events.all).Should(ContainElement(And(
			HaveField("RunId", runID),
			WithTransform(isIdle, BeTrue()),
		)))
		Consistently(fixture.script.promptContents, quiescence).Should(BeEmpty())

		fixture.sendImmediate(ctx, "run-after-abort", "new work")
		Eventually(fixture.script.promptContents).Should(Equal([]string{"new work"}))
	})

	It("removes the intentional marker when the abort RPC fails", func(ctx SpecContext) {
		fixture := startFixture(ctx,
			withQueueCapacity(4),
			withScript(newProviderScript().withAbortFailure(errAbortUnavailable)),
		)
		const runID = "run-abort-failure"

		fixture.sendImmediate(ctx, runID, "survives failed stop")
		parentID := fixture.script.promptMessageID("survives failed stop")
		Expect(parentID).NotTo(BeEmpty())
		Expect(fixture.runtime.Abort(ctx)).To(MatchError(errAbortUnavailable))

		fixture.script.failTurn(parentID, abortedErrorName, "abort after failed stop")
		fixture.script.publishSessionEvent(sessionErrorEvent)

		Eventually(fixture.events.all).Should(ContainElement(And(
			HaveField("RunId", runID),
			WithTransform(failureMessage, Equal("abort after failed stop")),
		)))
	})
})
