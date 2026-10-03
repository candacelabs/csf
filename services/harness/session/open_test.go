// Copyright 2026 Candace Labs

package session_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/session/mocks"
)

// instructionsHash is the first twelve hex digits of the SHA-256 of the
// recipe's instructions: the second half of the agent identity.
var instructionsHash = func() string {
	sum := sha256.Sum256([]byte(instructions))
	return hex.EncodeToString(sum[:6])
}()

var _ = Describe("Open with a router", func() {
	const attached = "5a9c7f1e-2b3d-4e6f-8a0b-1c2d3e4f5a6b"
	var (
		ctx      context.Context
		router   *mocks.MockIRouter
		executor *mocks.MockIOpenTurnExecutor
		spec     session.TurnExecutorSpec
		runner   *session.AgentSessionRunner
		state    string
	)

	BeforeEach(func() {
		ctx = context.Background()
		controller := gomock.NewController(GinkgoT())
		launcher := NewMockILauncher(controller)
		router = mocks.NewMockIRouter(controller)
		executor = mocks.NewMockIOpenTurnExecutor(controller)
		// The worktree, its hooks path and its initial commit.
		launcher.EXPECT().Run(gomock.Any(), launched("git")).Return(proc.Result{}, nil).AnyTimes()
		state = GinkgoT().TempDir()
		var err error
		runner, err = session.NewAgentSessionRunner(
			session.WithLauncher(launcher),
			session.WithStateDirectory(state),
			session.WithGateCommand(gateBinary, gateVerb),
			session.WithRouter(router),
			session.WithOpenTurnExecutors(func(ctx context.Context, opened session.TurnExecutorSpec) (session.IOpenTurnExecutor, error) {
				spec = opened
				return executor, nil
			}),
		)
		Expect(err).NotTo(HaveOccurred())
		executor.EXPECT().Close(gomock.Any()).Return(nil)
	})

	open := func() {
		opened, err := runner.Open(ctx, newRecipe())
		Expect(err).NotTo(HaveOccurred())
		Expect(opened.Close(ctx)).To(Succeed())
	}

	It("resumes the existing real session the router attaches it to, and records it as the run's session", func() {
		router.EXPECT().Route(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request session.RouteRequest) (string, error) {
			plan, err := csf.PrepareAgentAssignment(newRecipe())
			Expect(err).NotTo(HaveOccurred())
			Expect(request).To(Equal(session.RouteRequest{
				Virtual: assignmentID, Session: plan.GetSessionKey(), Model: "sonnet", Agent: "scratch/" + instructionsHash,
				Key: "https://example.invalid/issues/1", Summary: "H1 scratch change",
			}))
			return attached, nil
		})
		open()
		Expect(spec.Session.String()).To(Equal(attached))
		Expect(spec.Resume).To(BeTrue())
		recorded, err := session.ReadRunState(filepath.Join(state, assignmentID))
		Expect(err).NotTo(HaveOccurred())
		Expect(recorded.SessionID).To(Equal(attached))
	})

	It("opens a new conversation when the router answers with the run's own session", func() {
		router.EXPECT().Route(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request session.RouteRequest) (string, error) {
			return request.Session, nil
		})
		open()
		Expect(spec.Resume).To(BeFalse())
	})
})
