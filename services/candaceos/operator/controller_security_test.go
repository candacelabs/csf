package operator

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	copilot "github.com/github/copilot-sdk/go"
	"github.com/github/copilot-sdk/go/rpc"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

var _ = Describe("Controller workspace permission boundary", func() {
	It("rejects reads and writes that escape through a workspace symlink", func() {
		parent := GinkgoT().TempDir()
		workspace := filepath.Join(parent, "workspace")
		outside := filepath.Join(parent, "outside")
		Expect(os.Mkdir(workspace, 0o700)).To(Succeed())
		Expect(os.Mkdir(outside, 0o700)).To(Succeed())
		Expect(os.Symlink(outside, filepath.Join(workspace, "escape"))).To(Succeed())
		Expect(os.Symlink(filepath.Join(parent, "future-outside"), filepath.Join(workspace, "dangling-escape"))).To(Succeed())

		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Second),
		}, nil, nil)
		harness := testCopilotHarness(controller)

		Expect(harness.safeToApprove(&copilot.PermissionRequestRead{
			Path: filepath.Join(workspace, "escape", "secret.txt"),
		})).To(BeFalse())
		Expect(harness.safeToApprove(&copilot.PermissionRequestWrite{
			FileName: filepath.Join(workspace, "escape", "new-secret.txt"),
		})).To(BeFalse())
		Expect(harness.safeToApprove(&copilot.PermissionRequestWrite{
			FileName: filepath.Join(workspace, "dangling-escape", "new-secret.txt"),
		})).To(BeFalse())
	})

	It("still permits new paths below a real workspace directory", func() {
		workspace := GinkgoT().TempDir()
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Second),
		}, nil, nil)
		harness := testCopilotHarness(controller)

		Expect(harness.safeToApprove(&copilot.PermissionRequestWrite{
			FileName: filepath.Join(workspace, "new", "nested", "app.go"),
		})).To(BeTrue())
		Expect(harness.safeToApprove(&copilot.PermissionRequestRead{
			Path: filepath.Join(workspace, "new", "nested", "app.go"),
		})).To(BeTrue())
	})
})

var _ = Describe("automatic shell permission boundary", func() {
	DescribeTable("requires operator approval without a credential-isolating sandbox",
		func(command string) {
			workspace := GinkgoT().TempDir()
			controller := newTestController(&candaceosv1.CoreConfig{Workspace: workspace}, nil, nil)
			Expect(testCopilotHarness(controller).safeToApprove(&copilot.PermissionRequestShell{
				FullCommandText: command,
				CommandSegments: []copilot.PermissionRequestShellCommandSegment{{FullCommandText: command}},
				PossiblePaths:   []string{workspace},
			})).To(BeFalse())
		},
		Entry("Go tests", "go test ./..."),
		Entry("Go test executor", "go test -exec 'sh -c env' ./..."),
		Entry("Make target", "make test"),
		Entry("npm script", "npm test"),
		Entry("apparently read-only command", "git status --short"),
	)
})

var _ = Describe("approval expiry boundary", func() {
	It("reports the deadline outcome and preserves its durable callback failure", func(ctx SpecContext) {
		now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
		queue := NewApprovalQueue(time.Minute)
		queue.now = func() time.Time { return now }
		persistenceErr := errors.New("recording expiration: database unavailable")
		queue.OnResolved = func(resolution ApprovalResolution) error { return persistenceErr }

		requested := make(chan Approval, 1)
		queue.OnRequested = func(approval Approval) error {
			requested <- approval
			now = approval.ExpiresAt
			return nil
		}
		type outcome struct {
			resolution ApprovalResolution
			err        error
		}
		decisionResult := make(chan outcome, 1)
		queue.onPublished = func() {
			resolution, err := queue.Resolve((<-requested).ID, DecisionApprove, ApprovalActorState().Operator)
			decisionResult <- outcome{resolution: resolution, err: err}
		}
		requestResult := make(chan outcome, 1)
		go func() {
			resolution, err := queue.Request(ctx, ApprovalRequest{
				Kind: "shell", Title: "Run command", Detail: "go test ./...",
				Risk: "medium", Payload: "go test ./...",
			})
			requestResult <- outcome{resolution: resolution, err: err}
		}()

		var decision outcome
		Eventually(decisionResult).Should(Receive(&decision))
		Expect(decision.resolution.Decision).To(Equal(DecisionExpired))
		Expect(decision.resolution.Actor).To(Equal(ApprovalActorState().Timeout))
		Expect(errors.Is(decision.err, ErrApprovalExpired)).To(BeTrue())
		Expect(errors.Is(decision.err, persistenceErr)).To(BeTrue())
		Expect(decision.err.Error()).To(And(
			ContainSubstring(decision.resolution.Approval.ID),
			ContainSubstring(decision.resolution.Approval.ExpiresAt.Format(time.RFC3339Nano)),
			ContainSubstring(fmt.Sprintf(`before %q could submit "approve"`, ApprovalActorState().Operator)),
		))
		var request outcome
		Eventually(requestResult).Should(Receive(&request))
		Expect(errors.Is(request.err, persistenceErr)).To(BeTrue())
		Expect(request.resolution.Decision).To(Equal(DecisionExpired))
		Expect(request.resolution.Actor).To(Equal(ApprovalActorState().Timeout))
	})
})

var _ = Describe("resumed session safety", func() {
	It("interrupts pending pre-crash work until core restores a correlated run", func() {
		workspace := GinkgoT().TempDir()
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Second),
		}, nil, nil)
		DeferCleanup(controller.Close)

		resume := controller.harness.(*copilotHarness).resumeConfig()
		Expect(resume.ContinuePendingWork).NotTo(BeNil())
		Expect(*resume.ContinuePendingWork).To(BeFalse())
	})

	It("cancels an outstanding permission when the controller lifecycle ends", func() {
		workspace := GinkgoT().TempDir()
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Minute),
		}, nil, nil)
		ctx, cancel := context.WithCancel(context.Background())
		Expect(controller.Start(ctx)).To(Succeed())
		DeferCleanup(controller.Close)
		requested := make(chan struct{})
		controller.OnApprovalRequested = func(approval Approval) error {
			close(requested)
			return nil
		}
		type outcome struct {
			decision rpc.PermissionDecision
			err      error
		}
		result := make(chan outcome, 1)
		go func() {
			decision, err := testCopilotHarness(controller).handlePermission(&copilot.PermissionRequestCustomTool{
				ToolName: "another_tool", ToolDescription: "mutate one app",
			}, copilot.PermissionInvocation{})
			result <- outcome{decision: decision, err: err}
		}()
		Eventually(requested).Should(BeClosed())

		cancel()
		var value outcome
		Eventually(result).Should(Receive(&value))
		Expect(value.err).To(MatchError(ContainSubstring("context canceled")))
		Expect(value.decision).To(BeAssignableToTypeOf(&rpc.PermissionDecisionUserNotAvailable{}))
		Expect(controller.ApprovalQueue().Pending()).To(BeEmpty())
	})

	It("does not publish an approval aborted during durable request recording", func(ctx SpecContext) {
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, ApprovalTimeout: int64(time.Minute),
		}, nil, nil)
		controller.mu.Lock()
		controller.currentRun = RunState{ID: "run-recording", Status: "running", CanAbort: true}
		controller.runPhase = runRunning
		controller.status = controllerRunning
		controller.mu.Unlock()

		recording := make(chan Approval, 1)
		release := make(chan struct{})
		var releaseOnce sync.Once
		releaseRecording := func() { releaseOnce.Do(func() { close(release) }) }
		DeferCleanup(releaseRecording)
		order := make(chan string, 2)
		controller.OnApprovalRequested = func(approval Approval) error {
			recording <- approval
			<-release
			order <- "requested"
			return nil
		}
		controller.OnApprovalResolved = func(resolution ApprovalResolution) error {
			order <- "resolved"
			return nil
		}
		type outcome struct {
			resolution ApprovalResolution
			err        error
		}
		result := make(chan outcome, 1)
		go func() {
			resolution, err := controller.ApprovalQueue().Request(context.Background(), ApprovalRequest{
				RunID: "run-recording", Kind: "deploy", Title: "Deploy app",
				Detail: "app to node", Risk: "high", Payload: "app",
			})
			result <- outcome{resolution: resolution, err: err}
		}()

		var approval Approval
		Eventually(recording).Should(Receive(&approval))
		Expect(controller.ApprovalQueue().Pending()).To(BeEmpty())
		Expect(controller.Abort(ctx)).To(Succeed())
		Expect(controller.Run().Status).To(Equal("aborted"))
		Expect(controller.ApprovalQueue().Pending()).To(BeEmpty())
		_, visible := controller.ApprovalQueue().Get(approval.ID)
		Expect(visible).To(BeFalse())

		releaseRecording()
		Eventually(order).Should(Receive(Equal("requested")))
		Eventually(order).Should(Receive(Equal("resolved")))
		var completed outcome
		Eventually(result).Should(Receive(&completed))
		Expect(completed.err).NotTo(HaveOccurred())
		Expect(completed.resolution.Decision).To(Equal(DecisionExpired))
		Expect(completed.resolution.Actor).To(Equal(ApprovalActorState().Abort))
		Expect(controller.ApprovalQueue().Pending()).To(BeEmpty())
	})
})

var _ = Describe("reconcile approval source binding", func() {
	It("presents the immutable revision and rejects changed source at dispatch", func() {
		workspace := GinkgoT().TempDir()
		reconciler := &mutableReconciler{revision: testReconcileRevision("a", "1")}
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Minute),
		}, nil, reconciler)
		Expect(controller.Start(context.Background())).To(Succeed())
		DeferCleanup(controller.Close)

		input := testReconcileInput()
		approval, decision, err := requestAndApproveReconcile(controller, input, "tool-call-source")
		Expect(err).NotTo(HaveOccurred())
		Expect(decision).To(BeAssignableToTypeOf(&rpc.PermissionDecisionApproveOnce{}))
		Expect(approval.Detail).To(MatchJSON(`{
			"input": {
				"app": "status", "project": "status", "path": "apps/status",
				"desired_state": "running", "placement_mode": "leader"
			},
			"revision": {
				"id": "status-1111111111111111", "source": "git@example.invalid:candace/status.git",
				"source_revision": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
				"content_digest": "sha256:1111111111111111111111111111111111111111111111111111111111111111",
				"compose_path": "apps/status/compose.yaml"
			}
		}`))

		reconciler.setRevision(testReconcileRevision("b", "2"))
		_, err = controller.reconcileApproved(context.Background(), input, "tool-call-source")
		Expect(err).To(MatchError(ContainSubstring("app revision changed after approval")))
		Expect(reconciler.reconcileCalls()).To(Equal(0))
	})

	It("dispatches unchanged approved input exactly once", func() {
		workspace := GinkgoT().TempDir()
		reconciler := &mutableReconciler{revision: testReconcileRevision("a", "1")}
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Minute),
		}, nil, reconciler)
		Expect(controller.Start(context.Background())).To(Succeed())
		DeferCleanup(controller.Close)

		input := testReconcileInput()
		_, decision, err := requestAndApproveReconcile(controller, input, "tool-call-once")
		Expect(err).NotTo(HaveOccurred())
		Expect(decision).To(BeAssignableToTypeOf(&rpc.PermissionDecisionApproveOnce{}))
		output, err := controller.reconcileApproved(context.Background(), input, "tool-call-once")
		Expect(err).NotTo(HaveOccurred())
		Expect(output.GetRevisionId()).To(Equal("status-1111111111111111"))
		Expect(reconciler.reconcileCalls()).To(Equal(1))

		_, err = controller.reconcileApproved(context.Background(), input, "tool-call-once")
		Expect(err).To(MatchError("reconcile dispatch has no matching one-time approval"))
		Expect(reconciler.reconcileCalls()).To(Equal(1))
	})

	It("rejects changed tool arguments before rechecking source", func() {
		workspace := GinkgoT().TempDir()
		reconciler := &mutableReconciler{revision: testReconcileRevision("a", "1")}
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Minute),
		}, nil, reconciler)
		Expect(controller.Start(context.Background())).To(Succeed())
		DeferCleanup(controller.Close)

		input := testReconcileInput()
		_, _, err := requestAndApproveReconcile(controller, input, "tool-call-input")
		Expect(err).NotTo(HaveOccurred())
		input.Project = "substituted"
		_, err = controller.reconcileApproved(context.Background(), input, "tool-call-input")
		Expect(err).To(MatchError("reconcile input changed after approval"))
		Expect(reconciler.prepareCalls()).To(Equal(1))
		Expect(reconciler.reconcileCalls()).To(Equal(0))
	})
})

type mutableReconciler struct {
	mu         sync.Mutex
	revision   *candaceosv1.ReconcileRevision
	prepares   int
	reconciles int
}

func (r *mutableReconciler) Prepare(
	ctx context.Context,
	input *candaceosv1.ReconcileIntent,
) (*candaceosv1.ReconcileRevision, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_ = input
	r.prepares++
	return proto.Clone(r.revision).(*candaceosv1.ReconcileRevision), nil
}

func (r *mutableReconciler) ReconcileApproved(
	ctx context.Context,
	input *candaceosv1.ReconcileIntent,
	expected *candaceosv1.ReconcileRevision,
) (*candaceosv1.ReconcileEvidence, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !proto.Equal(r.revision, expected) {
		return nil, errors.New("app revision changed after approval")
	}
	r.reconciles++
	return &candaceosv1.ReconcileEvidence{
		DeploymentId: input.GetProject(), RevisionId: r.revision.GetId(),
	}, nil
}

func (r *mutableReconciler) setRevision(revision *candaceosv1.ReconcileRevision) {
	r.mu.Lock()
	r.revision = proto.Clone(revision).(*candaceosv1.ReconcileRevision)
	r.mu.Unlock()
}

func (r *mutableReconciler) prepareCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.prepares
}

func (r *mutableReconciler) reconcileCalls() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.reconciles
}

func requestAndApproveReconcile(
	controller *Controller,
	input *candaceosv1.ReconcileIntent,
	toolCallID string,
) (Approval, rpc.PermissionDecision, error) {
	requested := make(chan Approval, 1)
	controller.OnApprovalRequested = func(approval Approval) error {
		requested <- approval
		return nil
	}
	type outcome struct {
		decision rpc.PermissionDecision
		err      error
	}
	result := make(chan outcome, 1)
	go func() {
		decision, err := testCopilotHarness(controller).handlePermission(&copilot.PermissionRequestCustomTool{
			ToolName: "candace_reconcile_app", ToolDescription: "mutate one app",
			Args: input, ToolCallID: &toolCallID,
		}, copilot.PermissionInvocation{})
		result <- outcome{decision: decision, err: err}
	}()
	var approval Approval
	Eventually(requested).Should(Receive(&approval))
	Eventually(func() bool {
		_, ok := controller.ApprovalQueue().Get(approval.ID)
		return ok
	}).Should(BeTrue())
	_, err := controller.ApprovalQueue().Resolve(approval.ID, DecisionApprove, "operator")
	Expect(err).NotTo(HaveOccurred())
	var value outcome
	Eventually(result).Should(Receive(&value))
	return approval, value.decision, value.err
}

func testReconcileInput() *candaceosv1.ReconcileIntent {
	return &candaceosv1.ReconcileIntent{
		App: "status", Project: "status", Path: "apps/status",
		DesiredState:  candaceosv1.DesiredState_DESIRED_STATE_RUNNING,
		PlacementMode: candaceosv1.PlacementMode_PLACEMENT_MODE_LEADER,
	}
}

func testReconcileRevision(revision, digest string) *candaceosv1.ReconcileRevision {
	return &candaceosv1.ReconcileRevision{
		Id:             "status-" + strings.Repeat(digest, 16),
		Source:         "git@example.invalid:candace/status.git",
		SourceRevision: strings.Repeat(revision, 40),
		ContentDigest:  "sha256:" + strings.Repeat(digest, 64),
		ComposePath:    "apps/status/compose.yaml",
	}
}

var _ = Describe("fleet mutation classification", func() {
	It("gates only the CandaceOS reconcile tool on Warden quorum", func() {
		Expect(mapCopilotPermission(&copilot.PermissionRequestCustomTool{ToolName: "candace_reconcile_app"}).requiresFleetQuorum).To(BeTrue())
		Expect(mapCopilotPermission(&copilot.PermissionRequestCustomTool{ToolName: "another_tool"}).requiresFleetQuorum).To(BeFalse())
		Expect(mapCopilotPermission(&copilot.PermissionRequestShell{}).requiresFleetQuorum).To(BeFalse())
	})
})

var _ = Describe("permission presentation", func() {
	DescribeTable("builds a concise action title",
		func(request copilot.PermissionRequest, expected string) {
			Expect(permissionTitle(request)).To(Equal(expected))
		},
		Entry("shell", &copilot.PermissionRequestShell{FullCommandText: "go test ./..."}, "Run: go test ./..."),
		Entry("write", &copilot.PermissionRequestWrite{FileName: "app.go"}, "Write app.go"),
		Entry("read", &copilot.PermissionRequestRead{Path: "app.go"}, "Read app.go"),
		Entry("URL", &copilot.PermissionRequestURL{URL: "https://example.invalid"}, "Open https://example.invalid"),
		Entry("custom tool", &copilot.PermissionRequestCustomTool{ToolName: "candace_reconcile_app"}, "Use candace_reconcile_app"),
		Entry("other", &copilot.PermissionRequestMCP{}, "Approve mcp"),
	)

	DescribeTable("classifies action risk",
		func(request copilot.PermissionRequest, expected string) {
			Expect(permissionRisk(request)).To(Equal(expected))
		},
		Entry("read", &copilot.PermissionRequestRead{}, "low"),
		Entry("write", &copilot.PermissionRequestWrite{}, "medium"),
		Entry("URL", &copilot.PermissionRequestURL{}, "medium"),
		Entry("shell", &copilot.PermissionRequestShell{}, "high"),
	)

	It("formats compact event values without panicking on unsupported JSON", func() {
		Expect(summarizePrompt("  first line\nsecond line  ")).To(Equal("first line"))
		Expect(summarizePrompt(strings.Repeat("x", 80))).To(HaveLen(74))
		Expect(text("message")).To(Equal("message"))
		Expect(text(7)).To(BeEmpty())
		Expect(compactJSON(nil)).To(BeEmpty())
		Expect(compactJSON(map[string]string{"status": "ok"})).To(MatchJSON(`{"status":"ok"}`))
		Expect(compactJSON(make(chan int))).NotTo(BeEmpty())
	})
})

var _ = Describe("abort state transitions", func() {
	It("does not resurrect a turn that became idle while abort failed", func() {
		workspace := GinkgoT().TempDir()
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Second),
		}, nil, nil)
		controller.currentRun = RunState{ID: "run-1", Status: "aborting"}
		controller.runPhase = runAborting
		// An idle event received while Abort is waiting takes this branch in
		// ingest; restoreRunningAfterAbortFailure must honor that completion.
		controller.status = controllerIdle
		var recordedStatus string
		controller.OnRunStatus = func(runID, status string, _ time.Time) {
			Expect(runID).To(Equal("run-1"))
			recordedStatus = status
		}

		controller.restoreRunningAfterAbortFailure()

		Expect(controller.Run().Status).To(Equal("succeeded"))
		Expect(controller.Status()).To(Equal("idle"))
		Expect(recordedStatus).To(Equal("succeeded"))
	})

	It("preserves a terminal failure when the abort RPC also fails", func() {
		workspace := GinkgoT().TempDir()
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Second),
		}, nil, nil)
		controller.currentRun = RunState{ID: "run-failed-abort", Status: runAborting.String()}
		controller.runPhase = runAborting
		controller.status = controllerAborting
		var recordedStatus string
		controller.OnRunStatus = func(runID, status string, _ time.Time) {
			Expect(runID).To(Equal("run-failed-abort"))
			recordedStatus = status
		}

		controller.ingest(eventRecord{
			ID: "terminal-during-abort", Type: "session.error", Timestamp: time.Now().UTC(),
			Data: map[string]any{"message": "provider failed while aborting"},
		})
		controller.ingest(eventRecord{
			ID: "idle-after-terminal", Type: "session.idle", Timestamp: time.Now().UTC(), Data: map[string]any{},
		})
		controller.restoreRunningAfterAbortFailure()

		Expect(controller.Run().Status).To(Equal(runFailed.String()))
		Expect(controller.Status()).To(Equal(controllerIdle.String()))
		Expect(recordedStatus).To(Equal(runFailed.String()))
	})

	It("preserves a typed terminal failure while aborting", func() {
		workspace := GinkgoT().TempDir()
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Second),
		}, nil, nil)
		controller.currentRun = RunState{ID: "run-typed-failed-abort", Status: runAborting.String()}
		controller.runPhase = runAborting
		controller.status = controllerAborting
		var recordedStatus string
		controller.OnRunStatus = func(runID, status string, _ time.Time) {
			Expect(runID).To(Equal("run-typed-failed-abort"))
			recordedStatus = status
		}

		Expect(controller.publishHarnessEvent(&candaceosv1.HarnessEvent{
			Id: "typed-terminal-during-abort", RunId: "run-typed-failed-abort", At: timestamppb.Now(),
			Payload: &candaceosv1.HarnessEvent_Error{
				Error: &candaceosv1.HarnessError{Message: "provider failed while aborting"},
			},
		})).To(Succeed())
		controller.restoreRunningAfterAbortFailure()

		Expect(controller.Run().Status).To(Equal(runFailed.String()))
		Expect(controller.Status()).To(Equal(controllerIdle.String()))
		Expect(recordedStatus).To(Equal(runFailed.String()))
	})

	It("does not overwrite a terminal failure when the abort RPC succeeds", func() {
		workspace := GinkgoT().TempDir()
		controller := newTestController(&candaceosv1.CoreConfig{
			HarnessBackend: candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO, Workspace: workspace,
			DataDir: filepath.Join(workspace, "data"), ApprovalTimeout: int64(time.Second),
		}, nil, nil)
		controller.currentRun = RunState{ID: "run-raced-abort", Status: runAborting.String()}
		controller.runPhase = runAborting
		controller.status = controllerAborting

		controller.ingest(eventRecord{
			ID: "terminal-before-abort-ack", Type: "session.error", Timestamp: time.Now().UTC(),
			Data: map[string]any{"message": "provider failed before abort acknowledgement"},
		})
		controller.finishAbort()

		Expect(controller.Run().Status).To(Equal(runFailed.String()))
		Expect(controller.Status()).To(Equal(controllerIdle.String()))
	})
})
