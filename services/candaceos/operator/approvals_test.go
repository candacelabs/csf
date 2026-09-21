package operator_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/operator"
)

func TestOperator(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "CandaceOS Operator Suite")
}

func newTestController(cfg *candaceosv1.CoreConfig, _ any) *operator.Controller {
	controller, err := operator.NewController(cfg, nil, nil)
	Expect(err).NotTo(HaveOccurred())
	return controller
}

var _ = Describe("Approval queue", func() {
	It("binds a first-answer-wins decision to the exact payload digest", func(ctx SpecContext) {
		queue := operator.NewApprovalQueue(time.Minute)
		requested := make(chan operator.Approval, 1)
		queue.OnRequested = func(approval operator.Approval) error { requested <- approval; return nil }
		result := make(chan operator.ApprovalResolution, 1)
		go func() {
			resolution, _ := queue.Request(ctx, operator.ApprovalRequest{
				Kind: "deploy", Title: "Deploy notes", Detail: "notes@sha256:abc to n1",
				Risk: "high", Payload: map[string]string{"digest": "sha256:abc"}, RequiresFleetQuorum: true,
			})
			result <- resolution
		}()

		approval := <-requested
		Expect(approval.PayloadSHA256).To(HaveLen(64))
		var pending operator.Approval
		Eventually(func() bool {
			var ok bool
			pending, ok = queue.Get(approval.ID)
			return ok
		}).Should(BeTrue())
		Expect(pending.RequiresFleetQuorum).To(BeTrue())
		resolved, err := queue.Resolve(approval.ID, operator.DecisionApprove, operator.ApprovalActorState().Operator)
		Expect(err).NotTo(HaveOccurred())
		Expect(resolved.Approval.PayloadSHA256).To(Equal(approval.PayloadSHA256))
		Expect((<-result).Decision).To(Equal(operator.DecisionApprove))
		_, err = queue.Resolve(approval.ID, operator.DecisionReject, operator.ApprovalActorState().Operator)
		var notPending *operator.ApprovalNotPendingError
		Expect(errors.As(err, &notPending)).To(BeTrue())
		Expect(notPending.ID).To(Equal(approval.ID))
		_, exists := queue.Get(approval.ID)
		Expect(exists).To(BeFalse())
	})

	It("identifies an approval that is not owned by the pending queue", func() {
		missingApprovalID := uuid.NewString()
		queue := operator.NewApprovalQueue(time.Minute)

		resolution, err := queue.Resolve(
			missingApprovalID,
			operator.DecisionApprove,
			operator.ApprovalActorState().Operator,
		)

		Expect(resolution).To(BeZero())
		var notPending *operator.ApprovalNotPendingError
		Expect(errors.As(err, &notPending)).To(BeTrue())
		Expect(notPending.ID).To(Equal(missingApprovalID))
	})

	It("expires unattended requests instead of approving them", func() {
		queue := operator.NewApprovalQueue(time.Millisecond)
		resolution, err := queue.Request(context.Background(), operator.ApprovalRequest{
			Kind: "shell", Title: "Run command", Detail: "unknown command",
			Risk: "medium", Payload: "do a thing",
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(resolution.Decision).To(Equal(operator.DecisionExpired))
		Expect(queue.Pending()).To(BeEmpty())
	})

	It("does not advertise an expiry later than the requesting turn deadline", func() {
		queue := operator.NewApprovalQueue(time.Minute)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		deadline, hasDeadline := ctx.Deadline()
		Expect(hasDeadline).To(BeTrue())
		requested := make(chan operator.Approval, 1)
		queue.OnRequested = func(approval operator.Approval) error {
			requested <- approval
			return nil
		}
		completed := make(chan struct{})
		go func() {
			defer close(completed)
			_, _ = queue.Request(ctx, operator.ApprovalRequest{
				Kind: "deploy", Title: "Deploy app", Detail: "app to node", Risk: "high", Payload: "app",
			})
		}()

		approval := <-requested
		Expect(approval.ExpiresAt.After(deadline)).To(BeFalse())
		Eventually(func() bool {
			_, exists := queue.Get(approval.ID)
			return exists
		}).Should(BeTrue())
		_, err := queue.Resolve(approval.ID, operator.DecisionReject, operator.ApprovalActorState().Operator)
		Expect(err).NotTo(HaveOccurred())
		Eventually(completed).Should(BeClosed())
	})

	It("fails closed and releases the waiter when resolution persistence fails", func(ctx SpecContext) {
		queue := operator.NewApprovalQueue(time.Minute)
		requested := make(chan operator.Approval, 1)
		queue.OnRequested = func(approval operator.Approval) error { requested <- approval; return nil }
		persistenceErr := errors.New("database unavailable")
		queue.OnResolved = func(resolution operator.ApprovalResolution) error { return persistenceErr }
		type outcome struct {
			resolution operator.ApprovalResolution
			err        error
		}
		result := make(chan outcome, 1)
		go func() {
			resolution, err := queue.Request(ctx, operator.ApprovalRequest{
				Kind: "deploy", Title: "Deploy notes", Detail: "notes to n1",
				Risk: "high", Payload: map[string]string{"digest": "sha256:abc"},
			})
			result <- outcome{resolution: resolution, err: err}
		}()

		approval := <-requested
		Eventually(func() bool {
			_, ok := queue.Get(approval.ID)
			return ok
		}).Should(BeTrue())
		_, err := queue.Resolve(approval.ID, operator.DecisionApprove, operator.ApprovalActorState().Operator)
		Expect(errors.Is(err, persistenceErr)).To(BeTrue())
		Eventually(result).Should(Receive(And(
			WithTransform(func(value outcome) error { return value.err }, MatchError(persistenceErr)),
			WithTransform(func(value outcome) operator.ApprovalDecision { return value.resolution.Decision }, Equal(operator.DecisionApprove)),
		)))
		Expect(queue.Pending()).To(BeEmpty(), "a failed durable resolution must not remain approvable after its SDK waiter exits")
	})

	It("expires only the approvals belonging to an aborted run", func(ctx SpecContext) {
		queue := operator.NewApprovalQueue(time.Minute)
		requested := make(chan operator.Approval, 2)
		queue.OnRequested = func(approval operator.Approval) error { requested <- approval; return nil }
		type outcome struct {
			resolution operator.ApprovalResolution
			err        error
		}
		result := make(chan outcome, 1)
		go func() {
			resolution, err := queue.Request(ctx, operator.ApprovalRequest{
				RunID: "run-aborted", Kind: "deploy", Title: "Deploy notes", Detail: "notes to n1",
				Risk: "high", Payload: "notes",
			})
			result <- outcome{resolution: resolution, err: err}
		}()
		go func() {
			_, _ = queue.Request(ctx, operator.ApprovalRequest{
				RunID: "run-other", Kind: "deploy", Title: "Deploy photos", Detail: "photos to n2",
				Risk: "high", Payload: "photos",
			})
		}()
		Eventually(requested).Should(HaveLen(2))
		Eventually(queue.Pending).Should(HaveLen(2))

		Expect(queue.ExpireRun("run-aborted", operator.ApprovalActorState().Abort)).To(Succeed())
		var expired outcome
		Eventually(result).Should(Receive(&expired))
		Expect(expired.err).NotTo(HaveOccurred())
		Expect(expired.resolution.Decision).To(Equal(operator.DecisionExpired))
		Expect(expired.resolution.Actor).To(Equal(operator.ApprovalActorState().Abort))
		pending := queue.Pending()
		Expect(pending).To(HaveLen(1))
		Expect(pending[0].RunID).To(Equal("run-other"))
		_, err := queue.Resolve(pending[0].ID, operator.DecisionReject, operator.ApprovalActorState().Operator)
		Expect(err).NotTo(HaveOccurred())
	})

	It("publishes an approval only after durable request recording completes", func(ctx SpecContext) {
		queue := operator.NewApprovalQueue(time.Minute)
		recording := make(chan operator.Approval, 1)
		release := make(chan struct{})
		queue.OnRequested = func(approval operator.Approval) error {
			recording <- approval
			<-release
			return nil
		}
		type outcome struct {
			resolution operator.ApprovalResolution
			err        error
		}
		result := make(chan outcome, 1)
		go func() {
			resolution, err := queue.Request(ctx, operator.ApprovalRequest{
				Kind: "deploy", Title: "Deploy notes", Detail: "notes to n1",
				Risk: "high", Payload: "notes",
			})
			result <- outcome{resolution: resolution, err: err}
		}()

		approval := <-recording
		Consistently(queue.Pending).Should(BeEmpty())
		_, ok := queue.Get(approval.ID)
		Expect(ok).To(BeFalse())
		_, err := queue.Resolve(approval.ID, operator.DecisionApprove, operator.ApprovalActorState().Operator)
		Expect(errors.Is(err, operator.ErrApprovalRecording)).To(BeTrue())
		Expect(err.Error()).To(ContainSubstring(approval.ID))

		close(release)
		Eventually(func() bool {
			_, ready := queue.Get(approval.ID)
			return ready
		}).Should(BeTrue())
		_, err = queue.Resolve(approval.ID, operator.DecisionApprove, operator.ApprovalActorState().Operator)
		Expect(err).NotTo(HaveOccurred())
		var completed outcome
		Eventually(result).Should(Receive(&completed))
		Expect(completed.err).NotTo(HaveOccurred())
		Expect(completed.resolution.Decision).To(Equal(operator.DecisionApprove))
	})
})

var _ = Describe("Controller", func() {
	It("runs the same observable lifecycle in safe demo mode", func(ctx SpecContext) {
		workspace := GinkgoT().TempDir()
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Second),
		}, nil)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)
		runID, err := controller.Send(ctx, "Build me a tiny status page", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE)
		Expect(err).NotTo(HaveOccurred())
		Expect(runID).NotTo(BeEmpty())
		Eventually(func() string { return controller.Run().Status }).Should(Equal("succeeded"))
		Expect(controller.Run().Entries).To(ContainElement(WithTransform(
			func(entry operator.TimelineEntry) string { return entry.Name }, Equal("Claw"),
		)))
	})

	It("does not orphan a running turn when another prompt arrives", func(ctx SpecContext) {
		workspace := GinkgoT().TempDir()
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Second),
		}, nil)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)

		firstID, err := controller.Send(ctx, "Build the first app", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		secondID, err := controller.Send(ctx, "Replace it with another app", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(secondID).To(BeEmpty())
		Expect(err).To(MatchError("the agent is already running or stopping a turn"))
		Expect(controller.Run().ID).To(Equal(firstID))
		Eventually(func() string { return controller.Run().Status }).Should(Equal("succeeded"))

		thirdID, err := controller.Send(ctx, "Now build another app", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		Expect(thirdID).NotTo(BeEmpty())
		Expect(thirdID).NotTo(Equal(firstID))
	})

	It("waits for the aborted turn's idle event before accepting another prompt", func(ctx SpecContext) {
		workspace := GinkgoT().TempDir()
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Second),
		}, nil)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)

		_, err := controller.Send(ctx, "Build something slowly", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		Expect(controller.Abort(ctx)).To(Succeed())
		Expect(controller.Run().Status).To(Equal("aborted"))
		_, err = controller.Send(ctx, "Start too early", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).To(MatchError("the agent is already running or stopping a turn"))
		Eventually(controller.Status).Should(Equal("idle"))

		_, err = controller.Send(ctx, "Start after idle", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
	})

	It("expires the aborted run's outstanding approval waiter", func(ctx SpecContext) {
		workspace := GinkgoT().TempDir()
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Minute),
		}, nil)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)
		runID, err := controller.Send(ctx, "Prepare a deployment", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		requested := make(chan operator.Approval, 1)
		controller.OnApprovalRequested = func(approval operator.Approval) error {
			requested <- approval
			return nil
		}
		type outcome struct {
			resolution operator.ApprovalResolution
			err        error
		}
		result := make(chan outcome, 1)
		go func() {
			resolution, err := controller.ApprovalQueue().Request(ctx, operator.ApprovalRequest{
				RunID: runID, Kind: "deploy", Title: "Deploy app", Detail: "app to node",
				Risk: "high", Payload: "app",
			})
			result <- outcome{resolution: resolution, err: err}
		}()
		Eventually(requested).Should(Receive())
		Eventually(controller.ApprovalQueue().Pending).Should(HaveLen(1))

		Expect(controller.Abort(ctx)).To(Succeed())
		var expired outcome
		Eventually(result).Should(Receive(&expired))
		Expect(expired.err).NotTo(HaveOccurred())
		Expect(expired.resolution.Decision).To(Equal(operator.DecisionExpired))
		Expect(controller.ApprovalQueue().Pending()).To(BeEmpty())
	})

	It("refuses to abort when there is no active run", func(ctx SpecContext) {
		workspace := GinkgoT().TempDir()
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Second),
		}, nil)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)

		Expect(controller.Abort(ctx)).To(MatchError("no active agent run"))
	})

	It("does not accept another turn before completion persistence finishes", func(ctx SpecContext) {
		workspace := GinkgoT().TempDir()
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Second),
		}, nil)
		persisting := make(chan struct{})
		release := make(chan struct{})
		controller.OnRunStatus = func(_, _ string, _ time.Time) {
			close(persisting)
			<-release
		}
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)

		_, err := controller.Send(ctx, "Build the durable app", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		Eventually(persisting).Should(BeClosed())
		Expect(controller.Status()).To(Equal("persisting"))
		_, err = controller.Send(ctx, "Race the receipt", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).To(MatchError("the agent is already running or stopping a turn"))

		close(release)
		Eventually(controller.Status).Should(Equal("idle"))
	})
})
