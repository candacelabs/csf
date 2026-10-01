package operator_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	harnesssdk "github.com/candacelabs/csf/services/candaceos/harness"
	"github.com/candacelabs/csf/services/candaceos/operator"
)

type recordingHarness struct {
	host      harnesssdk.IHost
	prompt    *candaceosv1.HarnessPrompt
	prompts   []*candaceosv1.HarnessPrompt
	hold      bool
	aborts    int
	activated bool
	closed    bool
}

func (h *recordingHarness) Start(ctx context.Context) (*candaceosv1.HarnessSession, error) {
	return &candaceosv1.HarnessSession{Id: "external-session"}, ctx.Err()
}

func (h *recordingHarness) Activate(ctx context.Context) error {
	h.activated = true
	return h.host.Publish(ctx, &candaceosv1.HarnessEvent{
		Id: "external-start", At: timestamppb.Now(),
		Payload: &candaceosv1.HarnessEvent_SessionStarted{
			SessionStarted: &candaceosv1.HarnessSessionStarted{Message: "External harness ready."},
		},
	})
}

func (h *recordingHarness) Send(ctx context.Context, prompt *candaceosv1.HarnessPrompt) error {
	h.prompt = proto.Clone(prompt).(*candaceosv1.HarnessPrompt)
	h.prompts = append(h.prompts, proto.Clone(prompt).(*candaceosv1.HarnessPrompt))
	if h.hold {
		return nil
	}
	events := []*candaceosv1.HarnessEvent{
		{
			Id: "external-user-" + prompt.GetRunId(), RunId: prompt.GetRunId(), At: timestamppb.Now(),
			Payload: &candaceosv1.HarnessEvent_UserMessage{
				UserMessage: &candaceosv1.HarnessUserMessage{Content: prompt.GetContent()},
			},
		},
		{
			Id: "external-reply-" + prompt.GetRunId(), RunId: prompt.GetRunId(), At: timestamppb.Now(),
			Payload: &candaceosv1.HarnessEvent_AssistantMessage{
				AssistantMessage: &candaceosv1.HarnessAssistantMessage{
					MessageId: "reply-" + prompt.GetRunId(), Content: "Handled by an external implementation.",
				},
			},
		},
		{
			Id: "external-idle-" + prompt.GetRunId(), RunId: prompt.GetRunId(), At: timestamppb.Now(),
			Payload: &candaceosv1.HarnessEvent_Idle{Idle: &candaceosv1.HarnessIdle{}},
		},
	}
	for _, event := range events {
		if err := h.host.Publish(ctx, event); err != nil {
			return err
		}
	}
	return nil
}

func (h *recordingHarness) Abort(ctx context.Context) error {
	h.aborts++
	return ctx.Err()
}

func (h *recordingHarness) Close() error {
	h.closed = true
	return nil
}

var _ = Describe("compiled-in harness", func() {
	newRecordingController := func(runtime *recordingHarness) *operator.Controller {
		cfg := &candaceosv1.CoreConfig{
			HarnessBackend:  candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
			ApprovalTimeout: int64(time.Minute), Workspace: GinkgoT().TempDir(),
		}
		factory := harnesssdk.FactoryFunc(func(_ *candaceosv1.HarnessContext, host harnesssdk.IHost) (*harnesssdk.Instance, error) {
			runtime.host = host
			return &harnesssdk.Instance{
				Runtime: runtime,
				Identity: &candaceosv1.HarnessRuntimeIdentity{
					Backend: candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED, Implementation: "external-echo",
				},
			}, nil
		})
		controller, err := operator.NewControllerWithHarness(cfg, nil, nil, factory)
		Expect(err).NotTo(HaveOccurred())
		Expect(controller.Start(context.Background())).To(Succeed())
		DeferCleanup(controller.Close)
		return controller
	}

	It("steers only the exact active session and run without starting another run", func() {
		runtime := &recordingHarness{hold: true}
		controller := newRecordingController(runtime)
		started := 0
		controller.OnRunStarted = func(event operator.RunStarted) error { started++; return nil }

		runID, err := controller.Send(context.Background(), "start", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		Expect(controller.Steer(
			context.Background(), "external-session", runID, "queue this",
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE,
		)).To(Succeed())
		Expect(controller.SendToSession(
			context.Background(), "external-session", runID, "interrupt now",
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE,
		)).To(Equal(runID))
		Expect(runtime.prompts).To(HaveLen(3))
		Expect(runtime.prompts[1].GetDelivery()).To(Equal(candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE))
		Expect(runtime.prompts[2].GetDelivery()).To(Equal(candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE))
		Expect(started).To(Equal(1))

		Expect(controller.Steer(
			context.Background(), "stale-session", runID, "wrong session",
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE,
		)).To(MatchError(operator.ErrSessionConflict))
		Expect(controller.Steer(
			context.Background(), "external-session", "stale-run", "wrong run",
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE,
		)).To(MatchError(operator.ErrRunConflict))
		Expect(runtime.prompts).To(HaveLen(3))
	})

	It("starts an idle follow-up in the same session and retains the conversation", func() {
		runtime := &recordingHarness{}
		controller := newRecordingController(runtime)
		started := 0
		controller.OnRunStarted = func(event operator.RunStarted) error { started++; return nil }

		firstRunID, err := controller.Send(context.Background(), "first turn", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		secondRunID, err := controller.SendToSession(
			context.Background(), "external-session", firstRunID, "second turn",
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(secondRunID).NotTo(Equal(firstRunID))
		Expect(controller.Run().SessionID).To(Equal("external-session"))
		Expect(controller.Run().Entries).To(ContainElements(
			HaveField("Text", "first turn"),
			HaveField("Text", "second turn"),
		))
		Expect(started).To(Equal(2))
	})

	It("does not let an old run abort a newer active execution", func() {
		runtime := &recordingHarness{}
		controller := newRecordingController(runtime)
		firstRunID, err := controller.Send(context.Background(), "first turn", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())

		runtime.hold = true
		secondRunID, err := controller.SendToSession(
			context.Background(), "external-session", firstRunID, "second turn",
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE,
		)
		Expect(err).NotTo(HaveOccurred())
		Expect(secondRunID).NotTo(Equal(firstRunID))
		Expect(controller.AbortRun(context.Background(), "external-session", firstRunID)).To(MatchError(operator.ErrRunConflict))
		Expect(runtime.aborts).To(Equal(0))
	})

	It("inherits Controller lifecycle and UI projection through the public SDK", func() {
		runtime := &recordingHarness{}
		cfg := &candaceosv1.CoreConfig{
			HarnessBackend:  candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
			ApprovalTimeout: int64(time.Minute), Workspace: GinkgoT().TempDir(),
		}
		factory := harnesssdk.FactoryFunc(func(received *candaceosv1.HarnessContext, host harnesssdk.IHost) (*harnesssdk.Instance, error) {
			Expect(received.GetWorkspace()).To(Equal(cfg.GetWorkspace()))
			runtime.host = host
			return &harnesssdk.Instance{
				Runtime: runtime,
				Identity: &candaceosv1.HarnessRuntimeIdentity{
					Backend:        candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
					Implementation: "external-echo",
					Capabilities: []candaceosv1.HarnessCapability{
						candaceosv1.HarnessCapability_HARNESS_CAPABILITY_RECONCILE,
					},
				},
			}, nil
		})
		controller, err := operator.NewControllerWithHarness(cfg, nil, nil, factory)
		Expect(err).NotTo(HaveOccurred())

		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)
		Expect(runtime.activated).To(BeTrue())
		Expect(controller.HarnessIdentity().GetImplementation()).To(Equal("external-echo"))
		_, err = runtime.host.FleetStatus(ctx)
		Expect(err).To(MatchError("fleet status is unavailable"))
		_, err = runtime.host.Reconcile(ctx, &candaceosv1.HarnessReconcileRequest{
			ToolCallId: "external-tool",
			Intent: &candaceosv1.ReconcileIntent{
				App: "notes", Project: "candaceos-notes", Path: "notes",
				DesiredState:  candaceosv1.DesiredState_DESIRED_STATE_RUNNING,
				PlacementMode: candaceosv1.PlacementMode_PLACEMENT_MODE_LABELS,
				Labels:        map[string]string{"role": "worker"}, Replicas: 1,
			},
		})
		Expect(err).To(MatchError("reconciler is unavailable"))

		runID, err := controller.Send(ctx, "Build from the external harness", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())
		Expect(runtime.prompt.GetRunId()).To(Equal(runID))
		Expect(runtime.prompt.GetDelivery()).To(Equal(candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE))
		Expect(controller.Run().Status).To(Equal("succeeded"))
		Expect(controller.Run().Entries).To(ContainElement(
			HaveField("Text", "Handled by an external implementation."),
		))
	})

	It("accepts terminal publication synchronously from Runtime.Send", func(ctx SpecContext) {
		runtime := &recordingHarness{}
		controller := newRecordingController(runtime)
		type sendResult struct {
			runID string
			err   error
		}
		result := make(chan sendResult, 1)
		go func() {
			runID, err := controller.Send(
				ctx,
				"publish before Send returns",
				candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE,
			)
			result <- sendResult{runID: runID, err: err}
		}()

		var completed sendResult
		Eventually(ctx, result).Should(Receive(&completed))
		Expect(completed.err).NotTo(HaveOccurred())
		Expect(completed.runID).NotTo(BeEmpty())
		Expect(controller.Run().Status).To(Equal("succeeded"))
	})

	It("projects typed events and derives failure from the event oneof", func(ctx SpecContext) {
		runtime := &recordingHarness{hold: true}
		controller := newRecordingController(runtime)
		runID, err := controller.Send(
			ctx,
			"exercise typed publication",
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE,
		)
		Expect(err).NotTo(HaveOccurred())
		publish := func(event *candaceosv1.HarnessEvent) {
			event.RunId = runID
			event.At = timestamppb.Now()
			Expect(runtime.host.Publish(ctx, event)).To(Succeed())
		}

		publish(&candaceosv1.HarnessEvent{
			Id: "typed-tool-start",
			Payload: &candaceosv1.HarnessEvent_ToolStarted{
				ToolStarted: &candaceosv1.HarnessToolStarted{
					ToolCallId: "typed-tool", ToolName: "inspect",
					Arguments: structpb.NewStringValue("README.md"),
				},
			},
		})
		publish(&candaceosv1.HarnessEvent{
			Id: "typed-tool-complete",
			Payload: &candaceosv1.HarnessEvent_ToolCompleted{
				ToolCompleted: &candaceosv1.HarnessToolCompleted{
					ToolCallId: "typed-tool", ToolName: "inspect",
					Outcome: &candaceosv1.HarnessToolCompleted_Succeeded{
						Succeeded: &candaceosv1.HarnessToolSucceeded{Result: structpb.NewStringValue("ok")},
					},
				},
			},
		})
		publish(&candaceosv1.HarnessEvent{
			Id: "typed-delta",
			Payload: &candaceosv1.HarnessEvent_AssistantDelta{
				AssistantDelta: &candaceosv1.HarnessAssistantDelta{
					MessageId: "typed-answer", Content: "partial",
				},
			},
		})
		Expect(controller.Run().Entries).To(ContainElement(And(
			HaveField("Text", "partial"),
			HaveField("Status", "streaming"),
		)))
		publish(&candaceosv1.HarnessEvent{
			Id: "typed-answer-final",
			Payload: &candaceosv1.HarnessEvent_AssistantMessage{
				AssistantMessage: &candaceosv1.HarnessAssistantMessage{
					MessageId: "typed-answer", Content: "final answer",
				},
			},
		})
		publish(&candaceosv1.HarnessEvent{
			Id: "typed-error",
			Payload: &candaceosv1.HarnessEvent_Error{
				Error: &candaceosv1.HarnessError{Message: "typed failure"},
			},
		})

		Expect(controller.Run()).To(And(
			HaveField("Status", "failed"),
			HaveField("Entries", And(
				ContainElement(And(
					HaveField("Kind", "tool"),
					HaveField("Status", "complete"),
					HaveField("Detail", ContainSubstring(`"result":"ok"`)),
				)),
				ContainElement(HaveField("Text", "final answer")),
				ContainElement(And(
					HaveField("Kind", "error"),
					HaveField("Text", "typed failure"),
				)),
				Not(ContainElement(HaveField("Status", "streaming"))),
			)),
		))
	})

	It("linearizes stale terminal publication against the next run", func(ctx SpecContext) {
		runtime := &recordingHarness{hold: true}
		controller := newRecordingController(runtime)
		runID, err := controller.Send(
			ctx,
			"initial run",
			candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE,
		)
		Expect(err).NotTo(HaveOccurred())
		publishIdle := func(id, eventID string) error {
			return runtime.host.Publish(ctx, &candaceosv1.HarnessEvent{
				Id: eventID, RunId: id, At: timestamppb.Now(),
				Payload: &candaceosv1.HarnessEvent_Idle{Idle: &candaceosv1.HarnessIdle{}},
			})
		}
		Expect(publishIdle(runID, "initial-idle")).To(Succeed())

		for iteration := range 256 {
			start := make(chan struct{})
			published := make(chan error, 1)
			type nextResult struct {
				runID string
				err   error
			}
			next := make(chan nextResult, 1)
			go func(staleRunID string) {
				<-start
				published <- publishIdle(staleRunID, fmt.Sprintf("stale-idle-%d", iteration))
			}(runID)
			go func(previousRunID string) {
				<-start
				nextRunID, sendErr := controller.SendToSession(
					ctx,
					"external-session",
					previousRunID,
					fmt.Sprintf("next run %d", iteration),
					candaceosv1.HarnessDelivery_HARNESS_DELIVERY_ENQUEUE,
				)
				next <- nextResult{runID: nextRunID, err: sendErr}
			}(runID)
			close(start)

			var publishErr error
			var started nextResult
			Eventually(ctx, published).Should(Receive(&publishErr))
			Eventually(ctx, next).Should(Receive(&started))
			Expect(started.err).NotTo(HaveOccurred())
			Expect(started.runID).NotTo(Equal(runID))
			if publishErr != nil {
				Expect(publishErr).To(MatchError("publishing harness event: run_id is not the active run"))
			}
			Expect(controller.Run()).To(And(
				HaveField("ID", started.runID),
				HaveField("Status", "running"),
			))
			runID = started.runID
			Expect(publishIdle(runID, fmt.Sprintf("current-idle-%d", iteration))).To(Succeed())
		}
	})

	It("rejects a factory that lies about the embedded backend identity", func() {
		mockController := gomock.NewController(GinkgoT())
		runtime := NewMockIRuntime(mockController)
		runtime.EXPECT().Close().Return(nil)
		factory := harnesssdk.FactoryFunc(func(harnessContext *candaceosv1.HarnessContext, host harnesssdk.IHost) (*harnesssdk.Instance, error) {
			Expect(harnessContext.GetWorkspace()).NotTo(BeEmpty())
			Expect(host).NotTo(BeNil())
			return &harnesssdk.Instance{
				Runtime: runtime,
				Identity: &candaceosv1.HarnessRuntimeIdentity{
					Backend:        candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO,
					Implementation: "not-embedded",
				},
			}, nil
		})
		_, err := operator.NewControllerWithHarness(&candaceosv1.CoreConfig{
			HarnessBackend:  candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
			ApprovalTimeout: int64(time.Minute), Workspace: GinkgoT().TempDir(),
		}, nil, nil, factory)

		Expect(err).To(MatchError("embedded harness identity must use HARNESS_BACKEND_EMBEDDED"))
	})

	It("closes a runtime returned alongside a factory error", func() {
		mockController := gomock.NewController(GinkgoT())
		runtime := NewMockIRuntime(mockController)
		factoryErr := errors.New("factory rejected the configuration")
		closeErr := errors.New("provider cleanup failed")
		runtime.EXPECT().Close().Return(closeErr)
		factory := harnesssdk.FactoryFunc(func(harnessContext *candaceosv1.HarnessContext, host harnesssdk.IHost) (*harnesssdk.Instance, error) {
			Expect(harnessContext.GetWorkspace()).NotTo(BeEmpty())
			Expect(host).NotTo(BeNil())
			return &harnesssdk.Instance{Runtime: runtime}, factoryErr
		})

		_, err := operator.NewControllerWithHarness(&candaceosv1.CoreConfig{
			HarnessBackend:  candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
			ApprovalTimeout: int64(time.Minute), Workspace: GinkgoT().TempDir(),
		}, nil, nil, factory)

		Expect(errors.Is(err, factoryErr)).To(BeTrue())
		Expect(errors.Is(err, closeErr)).To(BeTrue())
		Expect(err).To(MatchError(And(
			ContainSubstring("constructing embedded harness"),
			ContainSubstring("closing rejected embedded harness"),
		)))
	})

	It("closes a runtime whose start fails and preserves both errors", func() {
		mockController := gomock.NewController(GinkgoT())
		runtime := NewMockIRuntime(mockController)
		startErr := errors.New("provider start failed")
		closeErr := errors.New("provider cleanup failed")
		gomock.InOrder(
			runtime.EXPECT().Start(gomock.Any()).Return(nil, startErr),
			runtime.EXPECT().Close().Return(closeErr),
		)
		factory := harnesssdk.FactoryFunc(func(harnessContext *candaceosv1.HarnessContext, host harnesssdk.IHost) (*harnesssdk.Instance, error) {
			Expect(harnessContext.GetWorkspace()).NotTo(BeEmpty())
			Expect(host).NotTo(BeNil())
			return &harnesssdk.Instance{Runtime: runtime, Identity: embeddedHarnessIdentity()}, nil
		})
		controller, err := operator.NewControllerWithHarness(&candaceosv1.CoreConfig{
			HarnessBackend:  candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
			ApprovalTimeout: int64(time.Minute), Workspace: GinkgoT().TempDir(),
		}, nil, nil, factory)
		Expect(err).NotTo(HaveOccurred())

		err = controller.Start(context.Background())

		Expect(errors.Is(err, startErr)).To(BeTrue())
		Expect(errors.Is(err, closeErr)).To(BeTrue())
		Expect(controller.Status()).To(Equal("stopped"))
	})

	It("closes a started runtime that returns an invalid session", func() {
		mockController := gomock.NewController(GinkgoT())
		runtime := NewMockIRuntime(mockController)
		gomock.InOrder(
			runtime.EXPECT().Start(gomock.Any()).Return(&candaceosv1.HarnessSession{}, nil),
			runtime.EXPECT().Close().Return(nil),
		)
		factory := harnesssdk.FactoryFunc(func(harnessContext *candaceosv1.HarnessContext, host harnesssdk.IHost) (*harnesssdk.Instance, error) {
			Expect(harnessContext.GetWorkspace()).NotTo(BeEmpty())
			Expect(host).NotTo(BeNil())
			return &harnesssdk.Instance{Runtime: runtime, Identity: embeddedHarnessIdentity()}, nil
		})
		controller, err := operator.NewControllerWithHarness(&candaceosv1.CoreConfig{
			HarnessBackend:  candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
			ApprovalTimeout: int64(time.Minute), Workspace: GinkgoT().TempDir(),
		}, nil, nil, factory)
		Expect(err).NotTo(HaveOccurred())

		err = controller.Start(context.Background())

		Expect(err).To(MatchError(ContainSubstring("embedded harness session")))
		Expect(controller.Status()).To(Equal("stopped"))
	})

	It("closes a runtime whose activation fails and preserves both errors", func() {
		mockController := gomock.NewController(GinkgoT())
		runtime := NewMockIRuntime(mockController)
		activateErr := errors.New("provider activation failed")
		closeErr := errors.New("provider cleanup failed")
		gomock.InOrder(
			runtime.EXPECT().Start(gomock.Any()).Return(&candaceosv1.HarnessSession{Id: "embedded-session"}, nil),
			runtime.EXPECT().Activate(gomock.Any()).Return(activateErr),
			runtime.EXPECT().Close().Return(closeErr),
		)
		factory := harnesssdk.FactoryFunc(func(harnessContext *candaceosv1.HarnessContext, host harnesssdk.IHost) (*harnesssdk.Instance, error) {
			Expect(harnessContext.GetWorkspace()).NotTo(BeEmpty())
			Expect(host).NotTo(BeNil())
			return &harnesssdk.Instance{Runtime: runtime, Identity: embeddedHarnessIdentity()}, nil
		})
		controller, err := operator.NewControllerWithHarness(&candaceosv1.CoreConfig{
			HarnessBackend:  candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
			ApprovalTimeout: int64(time.Minute), Workspace: GinkgoT().TempDir(),
		}, nil, nil, factory)
		Expect(err).NotTo(HaveOccurred())

		err = controller.Start(context.Background())

		Expect(errors.Is(err, activateErr)).To(BeTrue())
		Expect(errors.Is(err, closeErr)).To(BeTrue())
		Expect(controller.Status()).To(Equal("stopped"))
	})

	It("binds approval and dispatch to an owned reconcile request snapshot", func() {
		mockController := gomock.NewController(GinkgoT())
		runtime := NewMockIRuntime(mockController)
		reconciler := NewMockIReconciler(mockController)
		var providerHost harnesssdk.IHost
		gomock.InOrder(
			runtime.EXPECT().Start(gomock.Any()).Return(&candaceosv1.HarnessSession{Id: "embedded-session"}, nil),
			runtime.EXPECT().Activate(gomock.Any()).Return(nil),
		)
		runtime.EXPECT().Close().Return(nil)
		factory := harnesssdk.FactoryFunc(func(harnessContext *candaceosv1.HarnessContext, host harnesssdk.IHost) (*harnesssdk.Instance, error) {
			Expect(harnessContext.GetWorkspace()).NotTo(BeEmpty())
			providerHost = host
			return &harnesssdk.Instance{Runtime: runtime, Identity: embeddedHarnessIdentity()}, nil
		})
		input := &candaceosv1.ReconcileIntent{
			App: "notes", Project: "candaceos-notes", Path: "notes",
			DesiredState:  candaceosv1.DesiredState_DESIRED_STATE_RUNNING,
			PlacementMode: candaceosv1.PlacementMode_PLACEMENT_MODE_LABELS,
			Labels:        map[string]string{"role": "worker"}, Replicas: 1,
		}
		revision := &candaceosv1.ReconcileRevision{
			Id: "notes-revision", Source: "git@example.invalid:candace/notes.git",
			SourceRevision: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
			ContentDigest:  "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
			ComposePath:    "notes/compose.yaml",
		}
		reconciler.EXPECT().Prepare(gomock.Any(), input).Return(revision, nil)
		reconciler.EXPECT().ReconcileApproved(gomock.Any(), input, revision).Return(&candaceosv1.ReconcileEvidence{
			DeploymentId: "notes-deployment", RevisionId: revision.GetId(),
		}, nil)
		controller, err := operator.NewControllerWithHarness(&candaceosv1.CoreConfig{
			HarnessBackend:  candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
			ApprovalTimeout: int64(time.Minute), Workspace: GinkgoT().TempDir(),
		}, nil, reconciler, factory)
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)

		requested := make(chan operator.Approval, 1)
		controller.ApprovalQueue().OnRequested = func(approval operator.Approval) error {
			requested <- approval
			return nil
		}
		request := &candaceosv1.HarnessReconcileRequest{
			ToolCallId: "original-tool-call",
			Intent:     proto.Clone(input).(*candaceosv1.ReconcileIntent),
		}
		type reconcileResult struct {
			response *candaceosv1.ReconcileEvidence
			err      error
		}
		result := make(chan reconcileResult, 1)
		go func() {
			response, reconcileErr := providerHost.Reconcile(ctx, request)
			result <- reconcileResult{response: response, err: reconcileErr}
		}()

		var approval operator.Approval
		Eventually(requested).Should(Receive(&approval))
		Eventually(func() bool {
			_, pending := controller.ApprovalQueue().Get(approval.ID)
			return pending
		}).Should(BeTrue())
		request.ToolCallId = "mutated-tool-call"
		request.Intent.Project = "mutated-project"
		_, err = controller.ApprovalQueue().Resolve(approval.ID, operator.DecisionApprove, "test")
		Expect(err).NotTo(HaveOccurred())

		var completed reconcileResult
		Eventually(result).Should(Receive(&completed))
		Expect(completed.err).NotTo(HaveOccurred())
		Expect(completed.response.GetDeploymentId()).To(Equal("notes-deployment"))
	})

	It("enforces the handwritten event-ingress contract", func() {
		mockController := gomock.NewController(GinkgoT())
		runtime := NewMockIRuntime(mockController)
		var providerHost harnesssdk.IHost
		gomock.InOrder(
			runtime.EXPECT().Start(gomock.Any()).Return(&candaceosv1.HarnessSession{Id: "embedded-session"}, nil),
			runtime.EXPECT().Activate(gomock.Any()).Return(nil),
			runtime.EXPECT().Send(gomock.Any(), gomock.Any()).Return(nil),
		)
		runtime.EXPECT().Close().Return(nil)
		factory := harnesssdk.FactoryFunc(func(harnessContext *candaceosv1.HarnessContext, host harnesssdk.IHost) (*harnesssdk.Instance, error) {
			Expect(harnessContext.GetWorkspace()).NotTo(BeEmpty())
			providerHost = host
			return &harnesssdk.Instance{Runtime: runtime, Identity: embeddedHarnessIdentity()}, nil
		})
		controller, err := operator.NewControllerWithHarness(&candaceosv1.CoreConfig{
			HarnessBackend:  candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
			ApprovalTimeout: int64(time.Minute), Workspace: GinkgoT().TempDir(),
		}, nil, nil, factory)
		Expect(err).NotTo(HaveOccurred())
		ctx, cancel := context.WithCancel(context.Background())
		DeferCleanup(cancel)
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)
		runID, err := controller.Send(ctx, "hold the run open", candaceosv1.HarnessDelivery_HARNESS_DELIVERY_IMMEDIATE)
		Expect(err).NotTo(HaveOccurred())

		baseEvent := func(id string) *candaceosv1.HarnessEvent {
			return &candaceosv1.HarnessEvent{Id: id, RunId: runID, At: timestamppb.Now()}
		}
		publishError := func(event *candaceosv1.HarnessEvent, message string) {
			err := providerHost.Publish(ctx, event)
			Expect(err).To(MatchError(ContainSubstring(message)))
		}

		publishError(baseEvent("missing-payload"), "payload is required")

		invalidNested := baseEvent("invalid-nested-message")
		invalidNested.Payload = &candaceosv1.HarnessEvent_AssistantMessage{
			AssistantMessage: &candaceosv1.HarnessAssistantMessage{},
		}
		publishError(invalidNested, "HarnessAssistantMessage.message_id")

		wrongSessionRun := baseEvent("wrong-session-run")
		wrongSessionRun.Payload = &candaceosv1.HarnessEvent_SessionStarted{
			SessionStarted: &candaceosv1.HarnessSessionStarted{Message: "ready"},
		}
		publishError(wrongSessionRun, "run_id must be empty")

		oversized := baseEvent("oversized-arguments")
		oversized.Payload = &candaceosv1.HarnessEvent_ToolStarted{
			ToolStarted: &candaceosv1.HarnessToolStarted{
				ToolCallId: "oversized", ToolName: "inspect",
				Arguments: structpb.NewStringValue(strings.Repeat("x", 256*1024)),
			},
		}
		publishError(oversized, "structured detail exceeds 262144 bytes")

		nonFinite := baseEvent("non-finite-arguments")
		nonFinite.Payload = &candaceosv1.HarnessEvent_ToolStarted{
			ToolStarted: &candaceosv1.HarnessToolStarted{
				ToolCallId: "non-finite", ToolName: "inspect",
				Arguments: structpb.NewNumberValue(math.Inf(1)),
			},
		}
		publishError(nonFinite, "structured detail contains a non-finite number")

		missingOutcome := baseEvent("missing-outcome")
		missingOutcome.Payload = &candaceosv1.HarnessEvent_ToolCompleted{
			ToolCompleted: &candaceosv1.HarnessToolCompleted{
				ToolCallId: "missing-outcome", ToolName: "inspect",
			},
		}
		publishError(missingOutcome, "tool outcome is required")

		succeeded := baseEvent("successful-null-result")
		succeeded.Payload = &candaceosv1.HarnessEvent_ToolCompleted{
			ToolCompleted: &candaceosv1.HarnessToolCompleted{
				ToolCallId: "successful-null-result", ToolName: "inspect",
				Outcome: &candaceosv1.HarnessToolCompleted_Succeeded{
					Succeeded: &candaceosv1.HarnessToolSucceeded{Result: structpb.NewNullValue()},
				},
			},
		}
		Expect(providerHost.Publish(ctx, succeeded)).To(Succeed())

		failed := baseEvent("failed-result")
		failed.Payload = &candaceosv1.HarnessEvent_ToolCompleted{
			ToolCompleted: &candaceosv1.HarnessToolCompleted{
				ToolCallId: "failed-result", ToolName: "inspect",
				Outcome: &candaceosv1.HarnessToolCompleted_Failed{
					Failed: &candaceosv1.HarnessToolFailed{Message: "provider failure"},
				},
			},
		}
		Expect(providerHost.Publish(ctx, failed)).To(Succeed())
	})
})

func embeddedHarnessIdentity() *candaceosv1.HarnessRuntimeIdentity {
	return &candaceosv1.HarnessRuntimeIdentity{
		Backend:        candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED,
		Implementation: "test-embedded",
	}
}

var _ harnesssdk.IRuntime = (*recordingHarness)(nil)
