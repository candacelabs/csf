package opencode

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

var _ = Describe("OpenCode runtime lifecycle", func() {
	It("uses an unbuffered command mailbox", func(ctx SpecContext) {
		fixture := buildFixture()

		occupied := make(chan struct{})
		release := make(chan struct{})
		Expect(fixture.runtime.submit(ctx, nil, func(state *sessionState) bool {
			close(occupied)
			<-release
			return false
		})).To(BeTrue())
		<-occupied
		defer close(release)

		// The session's mailbox is pkg/mailbox, whose own suite pins the
		// channel's capacity at zero. What this level asserts is the
		// consequence the session depends on: while the command goroutine is
		// inside one command there is no slot to drop work into, so a
		// submission that will not wait is refused rather than silently
		// queued behind an invisible backlog. A buffered mailbox would accept
		// the second one here.
		impatient, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
		defer cancel()
		Expect(fixture.runtime.submit(impatient, nil, func(state *sessionState) bool {
			return false
		})).To(BeFalse())
	})

	It("rejects a server version outside the pinned contract", func(ctx SpecContext) {
		fixture := buildFixture(withScript(newProviderScript().withVersion("1.19.0")))

		_, err := fixture.runtime.Start(ctx)
		Expect(err).To(MatchError(ErrVersionMismatch))
		Expect(err).To(MatchError(ContainSubstring(`pinned "1.18.21"`)))
	})

	It("retries a transiently incoherent startup hydration", func(ctx SpecContext) {
		startFixture(ctx, withScript(newProviderScript().withPhaseScript(phaseBusy, phaseIdle)))
	})

	It("refuses a second start", func(ctx SpecContext) {
		fixture := startFixture(ctx)

		_, err := fixture.runtime.Start(ctx)
		Expect(err).To(MatchError(ErrAlreadyStarted))
		Expect(fixture.runtime.Activate(ctx)).To(MatchError(ErrAlreadyActivated))
	})

	It("attaches the exact configured session and publishes only run-correlated typed events", func(ctx SpecContext) {
		completed := float64(time.Now().Add(-time.Minute).UnixMilli())
		script := newProviderScript().withTranscript(
			transcriptMessage(userMessage("msg_old_user", "historical request")),
			transcriptMessage(assistantMessage(
				"msg_old_assistant", "msg_old_user", "historical answer", &completed, nil,
			)),
		)
		fixture := startFixture(ctx, withScript(script), withConfiguredSession(fixtureSessionID))

		Eventually(script.eventStreamConnected()).Should(BeClosed())
		Expect(script.createdSessions()).To(BeZero())
		Expect(script.sessionReadIDs()).To(ContainElement(fixtureSessionID))
		Expect(fixture.events.all()).NotTo(ContainElement(Or(
			WithTransform(userContent, Equal("historical request")),
			WithTransform(assistantContent, Equal("historical answer")),
		)))

		const runID = "run-live"
		fixture.sendImmediate(ctx, runID, "live request")
		Eventually(script.promptContents).Should(Equal([]string{"live request"}))
		parentID := script.promptMessageID("live request")
		script.streamAssistant(parentID, "msg_live_assistant", "working")
		script.publishSessionEvent("message.part.delta")
		Eventually(fixture.events.all).Should(ContainElement(And(
			HaveField("RunId", runID),
			WithTransform(deltaContent, Equal("working")),
		)))

		script.completeAssistant("msg_live_assistant", "working complete")
		script.publishSessionEvent("session.status")
		Eventually(fixture.events.all).Should(ContainElement(And(
			HaveField("RunId", runID),
			WithTransform(assistantContent, Equal("working complete")),
		)))
		Eventually(fixture.events.counting(isIdle)).Should(Equal(1))
		for _, event := range fixture.events.all() {
			if event.GetSessionStarted() == nil {
				Expect(event.GetRunId()).To(Equal(runID), "event %s escaped its exact run fence", event.GetId())
			}
		}
	})

	It("submits every prompt with the Core system invariants and the configured model", func(ctx SpecContext) {
		fixture := startFixture(ctx)

		fixture.sendImmediate(ctx, "run-contract", "check the workspace")

		Eventually(fixture.script.submittedPrompts).Should(HaveLen(1))
		submitted := fixture.script.submittedPrompts()[0]
		Expect(submitted.System).To(HavePrefix("You are Claw"))
		Expect(submitted.System).To(ContainSubstring("CandaceOS Core retains"))
		Expect(submitted.Model).To(Equal(fixtureModel))
		Expect(submitted.Text).To(Equal("check the workspace"))
		Expect(submitted.MessageID).NotTo(BeEmpty())
	})

	It("cancels a blocked queued publication during close", func(ctx SpecContext) {
		publicationStarted := make(chan struct{})
		var startedOnce sync.Once
		fixture := startFixture(ctx, withPublishInterceptor(
			func(publishCtx context.Context, event *candaceosv1.HarnessEvent) error {
				if userContent(event) != "blocked queued publication" {
					return nil
				}
				startedOnce.Do(func() { close(publicationStarted) })
				<-publishCtx.Done()
				return publishCtx.Err()
			},
		))
		fixture.sendImmediate(ctx, "run-close-publication", "active")

		sendResult := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			sendResult <- fixture.runtime.Send(ctx, testPrompt(
				"run-close-publication", "blocked queued publication",
				candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE,
			))
		}()
		Eventually(publicationStarted).Should(BeClosed())
		closeResult := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			closeResult <- fixture.runtime.Close()
		}()

		Eventually(sendResult).Should(Receive(MatchError(ContainSubstring(context.Canceled.Error()))))
		Eventually(closeResult).Should(Receive(Succeed()))
	})

	It("cancels a blocked provider submission during close", func(ctx SpecContext) {
		script, held := newProviderScript().withHeldPrompts()
		fixture := startFixture(ctx, withScript(script))

		sendResult := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			sendResult <- fixture.runtime.Send(ctx, testPrompt(
				"run-close-provider", "blocked provider submission",
				candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE,
			))
		}()
		Eventually(held).Should(BeClosed())
		closeResult := make(chan error, 1)
		go func() {
			defer GinkgoRecover()
			closeResult <- fixture.runtime.Close()
		}()

		Eventually(sendResult).Should(Receive(MatchError(ContainSubstring(context.Canceled.Error()))))
		Eventually(closeResult).Should(Receive(Succeed()))
	})

	It("serializes concurrent send abort and close calls", func(ctx SpecContext) {
		fixture := startFixture(ctx, withQueueCapacity(3))
		fixture.sendImmediate(ctx, "run-concurrent", "active")

		const senders = 24
		start := make(chan struct{})
		results := make(chan error, senders+2)
		for index := range senders {
			go func() {
				defer GinkgoRecover()
				<-start
				results <- fixture.runtime.Send(ctx, testPrompt(
					"run-concurrent", fmt.Sprintf("queued-%02d", index),
					candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE,
				))
			}()
		}
		go func() {
			defer GinkgoRecover()
			<-start
			results <- fixture.runtime.Abort(ctx)
		}()
		go func() {
			defer GinkgoRecover()
			<-start
			results <- fixture.runtime.Close()
		}()
		close(start)

		for range senders + 2 {
			var err error
			Eventually(results).Should(Receive(&err))
			Expect(
				err == nil || errors.Is(err, ErrQueueFull) ||
					errors.Is(err, ErrSessionUnavailable) || errors.Is(err, context.Canceled),
			).To(BeTrue(), "unexpected error %v", err)
		}
		Expect(fixture.runtime.Close()).To(Succeed())
	})

	It("reports every operation unavailable after close", func(ctx SpecContext) {
		fixture := startFixture(ctx)

		Expect(fixture.runtime.Close()).To(Succeed())
		Expect(fixture.runtime.Close()).To(Succeed())

		Expect(fixture.runtime.Send(ctx, testPrompt(
			"run-after-close", "too late", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE,
		))).To(MatchError(ErrSessionUnavailable))
		Expect(fixture.runtime.Abort(ctx)).To(MatchError(ErrSessionUnavailable))
		_, err := fixture.runtime.Start(ctx)
		Expect(err).To(MatchError(ErrClosed))
	})
})
