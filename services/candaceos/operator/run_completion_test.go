package operator

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

var _ = Describe("run completion", func() {
	startedController := func(ctx SpecContext) *Controller {
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO,
		}, nil, nil)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)
		return controller
	}

	It("keeps a run open across an agentic loop that ends with work still queued", func(ctx SpecContext) {
		controller := startedController(ctx)
		_, err := controller.Send(ctx, "first turn", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())

		// A provider emits assistant.idle between queued follow-ups and while
		// background agents are still running. Completing the run here would
		// let the queued prompt execute with no run of its own.
		controller.ingest(eventRecord{
			ID: "assistant-idle-1", Type: eventKindAssistantIdle,
			Timestamp: time.Now().UTC(), Data: map[string]any{},
		})
		Expect(controller.Run().Status).To(Equal(runRunning.String()))
		Expect(controller.Run().CanAbort).To(BeTrue())
		Expect(controller.Status()).To(Equal(controllerRunning.String()))

		controller.ingest(eventRecord{
			ID: "session-idle-1", Type: eventKindSessionIdle,
			Timestamp: time.Now().UTC(), Data: map[string]any{},
		})
		Expect(controller.Run().Status).To(Equal(runSucceeded.String()))
		Eventually(controller.Status).Should(Equal(controllerIdle.String()))
	})

	It("annotates each operator message with how the provider admitted it", func(ctx SpecContext) {
		controller := startedController(ctx)
		_, err := controller.Send(ctx, "first turn", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())

		at := time.Now().UTC()
		for index, delivery := range []any{"idle", "steering", "queued", nil} {
			controller.ingest(eventRecord{
				ID: "user-" + string(rune('a'+index)), Type: eventKindUserMessage,
				Timestamp: at.Add(time.Duration(index) * time.Millisecond),
				Data:      map[string]any{"content": "guidance", "delivery": delivery},
			})
		}

		var statuses []string
		for _, entry := range controller.Run().Entries {
			if strings.HasPrefix(entry.ID, "user-") {
				statuses = append(statuses, entry.Status)
			}
		}
		Expect(statuses).To(Equal([]string{"", "steering", "queued", ""}))
	})
})

var _ = Describe("provider condition notices", func() {
	startedRun := func(ctx SpecContext) *Controller {
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO,
		}, nil, nil)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)
		_, err := controller.Send(ctx, "start a turn", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		return controller
	}

	It("fails the active run when the agent runtime stops", func(ctx SpecContext) {
		controller := startedRun(ctx)
		controller.ingest(eventRecord{
			ID: "shutdown-1", Type: eventKindSessionShutdown, Timestamp: time.Now().UTC(),
			Data: map[string]any{"shutdownType": "error", "errorReason": "runtime crashed"},
		})

		Expect(controller.Run().Status).To(Equal(runFailed.String()))
		Expect(controller.Run().CanAbort).To(BeFalse())
		Expect(controller.Run().Entries).To(ContainElement(SatisfyAll(
			HaveField("Kind", "notice"),
			HaveField("Status", "stopped"),
			HaveField("Text", ContainSubstring("runtime crashed")),
		)))
	})

	It("surfaces warnings and dropped context without ending the run", func(ctx SpecContext) {
		controller := startedRun(ctx)
		controller.ingest(eventRecord{
			ID: "warning-1", Type: eventKindSessionWarning, Timestamp: time.Now().UTC(),
			Data: map[string]any{"message": "premium requests are nearly exhausted", "warningType": "subscription"},
		})
		controller.ingest(eventRecord{
			ID: "compaction-1", Type: eventKindSessionCompactionComplete,
			Timestamp: time.Now().UTC(), Data: map[string]any{"messagesRemoved": 12},
		})
		controller.ingest(eventRecord{
			ID: "truncation-1", Type: eventKindSessionTruncation,
			Timestamp: time.Now().UTC(), Data: map[string]any{"messagesRemovedDuringTruncation": 3},
		})

		Expect(controller.Run().Status).To(Equal(runRunning.String()))
		var statuses []string
		for _, entry := range controller.Run().Entries {
			if entry.Kind == "notice" {
				statuses = append(statuses, entry.Status)
			}
		}
		Expect(statuses).To(ConsistOf("warning", "compacted", "truncated"))
		Expect(controller.Run().Entries).To(ContainElement(SatisfyAll(
			HaveField("Status", "warning"),
			HaveField("Text", Equal("premium requests are nearly exhausted")),
		)))
	})
})
