package copilotbridge

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"

	"github.com/github/copilot-sdk/go/rpc"
	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
)

var _ = Describe("resolution registry", func() {
	It("delivers one exact SDK decision and retains it until durable acknowledgement", func() {
		registry := newResolutionRegistry()
		DeferCleanup(registry.stop)
		identifier := uuid.New()
		Expect(registry.open(identifier, "sdk-request-a")).To(BeTrue())
		var calls atomic.Int32
		registry.bind(func(_ context.Context, request *rpc.PermissionDecisionRequest) (*rpc.PermissionRequestResult, error) {
			calls.Add(1)
			Expect(request.RequestID).To(Equal("sdk-request-a"))
			Expect(request.Result).To(BeAssignableToTypeOf(&rpc.PermissionDecisionApproveOnce{}))
			approved := request.Result.(*rpc.PermissionDecisionApproveOnce)
			Expect(approved.ApprovedInteractively).NotTo(BeNil())
			Expect(*approved.ApprovedInteractively).To(BeTrue())
			Expect(request.DecisionContext).NotTo(BeNil())
			Expect(request.DecisionContext.Outcome).To(Equal(rpc.PermissionDecisionOutcomePromptedUser))
			Expect(request.DecisionContext.Source).To(Equal(rpc.PermissionDecisionSourceHumanResponse))
			Expect(request.DecisionContext.Surface).To(Equal(rpc.PermissionDecisionSurfaceSDK))
			return &rpc.PermissionRequestResult{Success: true}, nil
		})

		decision := copilotadapter.BridgeResolution{RequestID: identifier, Decision: "approve"}
		Expect(registry.resolve(context.Background(), decision)).To(Succeed())
		Expect(registry.resolve(context.Background(), decision)).To(Succeed())
		Expect(calls.Load()).To(Equal(int32(1)))
		Expect(registry.resolve(context.Background(), copilotadapter.BridgeResolution{
			RequestID: identifier, Decision: "deny",
		})).To(MatchError(errResolutionConflict))

		registry.acknowledge(identifier)
		Expect(registry.resolve(context.Background(), decision)).To(MatchError(errResolutionMissing))
	})

	It("serializes concurrent identical retries around one SDK RPC", func() {
		registry := newResolutionRegistry()
		DeferCleanup(registry.stop)
		identifier := uuid.New()
		Expect(registry.open(identifier, "sdk-request-a")).To(BeTrue())
		entered := make(chan struct{})
		release := make(chan struct{})
		var calls atomic.Int32
		registry.bind(func(_ context.Context, _ *rpc.PermissionDecisionRequest) (*rpc.PermissionRequestResult, error) {
			if calls.Add(1) == 1 {
				close(entered)
			}
			<-release
			return &rpc.PermissionRequestResult{Success: true}, nil
		})
		decision := copilotadapter.BridgeResolution{RequestID: identifier, Decision: "deny"}
		errors := make(chan error, 8)
		var callers sync.WaitGroup
		for range 8 {
			callers.Add(1)
			go func() {
				defer callers.Done()
				errors <- registry.resolve(context.Background(), decision)
			}()
		}
		Eventually(entered).Should(BeClosed())
		Consistently(func() int32 { return calls.Load() }).Should(Equal(int32(1)))
		close(release)
		callers.Wait()
		close(errors)
		for err := range errors {
			Expect(err).NotTo(HaveOccurred())
		}
		Expect(calls.Load()).To(Equal(int32(1)))
	})

	It("rejects a conflicting concurrent decision without starting another RPC", func() {
		registry := newResolutionRegistry()
		DeferCleanup(registry.stop)
		identifier := uuid.New()
		Expect(registry.open(identifier, "sdk-request-a")).To(BeTrue())
		entered := make(chan struct{})
		release := make(chan struct{})
		registry.bind(func(_ context.Context, _ *rpc.PermissionDecisionRequest) (*rpc.PermissionRequestResult, error) {
			close(entered)
			<-release
			return &rpc.PermissionRequestResult{Success: true}, nil
		})
		first := make(chan error, 1)
		go func() {
			first <- registry.resolve(context.Background(), copilotadapter.BridgeResolution{
				RequestID: identifier, Decision: "approve",
			})
		}()
		Eventually(entered).Should(BeClosed())
		Expect(registry.resolve(context.Background(), copilotadapter.BridgeResolution{
			RequestID: identifier, Decision: "deny",
		})).To(MatchError(errResolutionConflict))
		close(release)
		Expect(<-first).To(Succeed())
	})

	It("keeps unsuccessful SDK delivery retryable", func() {
		for _, firstResult := range []struct {
			name   string
			result *rpc.PermissionRequestResult
			err    error
		}{
			{name: "false result", result: &rpc.PermissionRequestResult{Success: false}},
			{name: "nil result"},
			{name: "transport error", err: errors.New("transport unavailable")},
		} {
			firstResult := firstResult
			By(firstResult.name)
			registry := newResolutionRegistry()
			identifier := uuid.New()
			Expect(registry.open(identifier, "sdk-request-a")).To(BeTrue())
			var calls atomic.Int32
			registry.bind(func(_ context.Context, _ *rpc.PermissionDecisionRequest) (*rpc.PermissionRequestResult, error) {
				if calls.Add(1) == 1 {
					return firstResult.result, firstResult.err
				}
				return &rpc.PermissionRequestResult{Success: true}, nil
			})
			decision := copilotadapter.BridgeResolution{RequestID: identifier, Decision: "deny"}
			firstErr := registry.resolve(context.Background(), decision)
			if firstResult.err != nil {
				Expect(firstErr).To(MatchError(firstResult.err))
			} else {
				Expect(firstErr).To(MatchError(errResolutionNotApplied))
			}
			Expect(registry.resolve(context.Background(), decision)).To(Succeed())
			Expect(calls.Load()).To(Equal(int32(2)))
			registry.stop()
		}
	})

	It("converges when permission.completed is projected before the direct RPC returns", func() {
		registry := newResolutionRegistry()
		DeferCleanup(registry.stop)
		identifier := uuid.New()
		Expect(registry.open(identifier, "sdk-request-a")).To(BeTrue())
		registry.bind(func(_ context.Context, _ *rpc.PermissionDecisionRequest) (*rpc.PermissionRequestResult, error) {
			completedID, observed := registry.complete("sdk-request-a", "approve")
			Expect(observed).To(BeTrue())
			Expect(completedID).To(Equal(identifier))
			registry.acknowledge(identifier)
			return &rpc.PermissionRequestResult{Success: false}, nil
		})
		decision := copilotadapter.BridgeResolution{RequestID: identifier, Decision: "approve"}
		Expect(registry.resolve(context.Background(), decision)).To(Succeed())
		Expect(registry.resolve(context.Background(), decision)).To(MatchError(errResolutionMissing))
	})

	It("records an external completion without invoking the local RPC", func() {
		registry := newResolutionRegistry()
		DeferCleanup(registry.stop)
		identifier := uuid.New()
		Expect(registry.open(identifier, "sdk-request-a")).To(BeTrue())
		var calls atomic.Int32
		registry.bind(func(_ context.Context, _ *rpc.PermissionDecisionRequest) (*rpc.PermissionRequestResult, error) {
			calls.Add(1)
			return &rpc.PermissionRequestResult{Success: true}, nil
		})
		completedID, observed := registry.complete("sdk-request-a", "deny")
		Expect(observed).To(BeTrue())
		Expect(completedID).To(Equal(identifier))
		_, duplicate := registry.complete("sdk-request-a", "deny")
		Expect(duplicate).To(BeFalse())
		Expect(registry.resolve(context.Background(), copilotadapter.BridgeResolution{
			RequestID: identifier, Decision: "deny",
		})).To(Succeed())
		Expect(registry.resolve(context.Background(), copilotadapter.BridgeResolution{
			RequestID: identifier, Decision: "approve",
		})).To(MatchError(errResolutionConflict))
		Expect(calls.Load()).To(BeZero())
	})

	It("rejects duplicate identities and SDK request mappings", func() {
		registry := newResolutionRegistry()
		DeferCleanup(registry.stop)
		first, second := uuid.New(), uuid.New()
		Expect(registry.open(first, "sdk-request-a")).To(BeTrue())
		Expect(registry.open(first, "sdk-request-a")).To(BeFalse())
		_, err := registry.open(first, "sdk-request-b")
		Expect(err).To(MatchError(errResolutionConflict))
		_, err = registry.open(second, "sdk-request-a")
		Expect(err).To(MatchError(errResolutionConflict))
	})

	It("fails closed when stopped or when no exact mapping or handler exists", func() {
		registry := newResolutionRegistry()
		identifier := uuid.New()
		decision := copilotadapter.BridgeResolution{RequestID: identifier, Decision: "deny"}
		Expect(registry.resolve(context.Background(), decision)).To(MatchError(errResolutionMissing))
		Expect(registry.open(identifier, "sdk-request-a")).To(BeTrue())
		Expect(registry.resolve(context.Background(), decision)).To(MatchError(errResolutionHandlerUnavailable))
		registry.stop()
		Expect(registry.resolve(context.Background(), decision)).To(MatchError(errResolutionAbandoned))
		_, err := registry.open(uuid.New(), "sdk-request-b")
		Expect(err).To(MatchError(errResolutionAbandoned))
	})
})
