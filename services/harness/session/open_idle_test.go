// Copyright 2026 Candace Labs

package session_test

import (
	"context"
	"errors"
	"slices"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/session/mocks"
)

var _ = Describe("a suspended session", func() {
	var (
		ctx             context.Context
		executor        *mocks.MockIOpenTurnExecutor
		resumedExecutor *mocks.MockIOpenTurnExecutor
		runner          *session.AgentSessionRunner
		run             string
		specs           []session.TurnExecutorSpec
		// refuse, when set, is what the second open fails with.
		refuse error
	)
	proposal := &model.Proposal[claudecode.Event]{Provider: claudecode.ProviderName}

	BeforeEach(func() {
		ctx = context.Background()
		controller := gomock.NewController(GinkgoT())
		launcher := NewMockILauncher(controller)
		executor = mocks.NewMockIOpenTurnExecutor(controller)
		resumedExecutor = mocks.NewMockIOpenTurnExecutor(controller)
		launcher.EXPECT().Run(gomock.Any(), launched("git")).Return(proc.Result{}, nil).AnyTimes()
		launcher.EXPECT().Run(gomock.Any(), launched("gh")).Return(proc.Result{}, nil).AnyTimes()
		specs, refuse = nil, nil
		state := GinkgoT().TempDir()
		run = session.RunDirectory(state, assignmentID)
		var err error
		runner, err = session.NewAgentSessionRunner(
			session.WithLauncher(launcher),
			session.WithStateDirectory(state),
			session.WithGateCommand(gateBinary, gateVerb),
			session.WithOpenTurnExecutors(func(ctx context.Context, opened session.TurnExecutorSpec) (session.IOpenTurnExecutor, error) {
				specs = append(specs, opened)
				if len(specs) == 1 {
					return executor, nil
				}
				if refuse != nil {
					return nil, refuse
				}
				return resumedExecutor, nil
			}),
		)
		Expect(err).NotTo(HaveOccurred())
	})

	It("suspends by closing its executor, refuses a turn until resumed, and resumes on the same conversation", func() {
		opened, err := runner.Open(ctx, newRecipe())
		Expect(err).NotTo(HaveOccurred())
		executor.EXPECT().Propose(gomock.Any(), gomock.Any()).Return(proposal, nil)
		_, err = opened.Turn(ctx, "first")
		Expect(err).NotTo(HaveOccurred())
		Expect(opened.Suspended()).To(BeFalse())

		executor.EXPECT().Close(gomock.Any()).Return(nil)
		Expect(opened.Suspend(ctx, 11*time.Minute)).To(Succeed())
		Expect(opened.Suspended()).To(BeTrue())
		Expect(opened.Suspend(ctx, time.Minute)).To(Succeed(), "a suspended session is left as it is")
		_, err = opened.Turn(ctx, "while idle")
		Expect(err).To(MatchError(session.ErrSuspended))
		Expect(opened.Interrupt(ctx)).To(Succeed(), "nothing runs, so nothing is interrupted")

		Expect(opened.Resume(ctx, 20*time.Minute)).To(Succeed())
		Expect(opened.Suspended()).To(BeFalse())
		Expect(opened.Resume(ctx, time.Minute)).To(Succeed(), "an open executor is left as it is")
		Expect(specs).To(HaveLen(2))
		Expect(specs[1].Resume).To(BeTrue(), "the recorded conversation is resumed")
		Expect(specs[1].Session).To(Equal(specs[0].Session))
		Expect(specs[1].Directory).To(Equal(specs[0].Directory))
		Expect(specs[1].Arguments).To(Equal(specs[0].Arguments))
		Expect(specs[1].Environment).To(Equal(specs[0].Environment))

		resumedExecutor.EXPECT().Propose(gomock.Any(), gomock.Any()).Return(proposal, nil).Times(2)
		receipt, err := opened.Turn(ctx, "after the resume")
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.GetResumed().GetSuspendedSeconds()).To(Equal(1200.0))
		Expect(receipt.GetResumed().GetTimeToFirstTokenMs()).To(BeZero(), "the double reports no assistant event")
		later, err := opened.Turn(ctx, "a later turn")
		Expect(err).NotTo(HaveOccurred())
		Expect(later.GetResumed()).To(BeNil(), "only the first turn after a resume carries it")

		resumedExecutor.EXPECT().Close(gomock.Any()).Return(nil)
		Expect(opened.Close(ctx)).To(Succeed())
		logged := records(run)
		types := eventTypes(logged)
		suspendedAt := slices.Index(types, session.EventTypeSessionSuspended)
		resumedAt := slices.Index(types, session.EventTypeSessionResumed)
		turnResumed := slices.Index(types, session.EventTypeTurnResumed)
		Expect(suspendedAt).To(BeNumerically(">", 0))
		Expect(resumedAt).To(BeNumerically(">", suspendedAt))
		Expect(turnResumed).To(BeNumerically(">", resumedAt))
		Expect(logged[suspendedAt]).To(HaveKeyWithValue(session.KeyIdleSeconds, BeNumerically("==", 660)))
		Expect(logged[resumedAt]).To(HaveKeyWithValue(session.KeySuspendedSeconds, BeNumerically("==", 1200)))
		Expect(logged[turnResumed]).To(HaveKey(session.KeyTimeToFirstToken))
		closedAt := slices.Index(types, session.EventTypeSessionClosed)
		Expect(closedAt).To(BeNumerically(">", turnResumed), "the conversation closes once, at Close")
		Expect(slices.Index(types[closedAt+1:], session.EventTypeSessionClosed)).To(Equal(-1), "a suspend is not a close of the conversation")
	})

	It("reports a resume the factory refuses, and refuses to suspend or resume a closed session", func() {
		opened, err := runner.Open(ctx, newRecipe())
		Expect(err).NotTo(HaveOccurred())
		executor.EXPECT().Close(gomock.Any()).Return(nil)
		Expect(opened.Suspend(ctx, time.Minute)).To(Succeed())

		refuse = errors.New("no claude on this host")
		Expect(opened.Resume(ctx, time.Minute)).To(MatchError(ContainSubstring("no claude on this host")))
		Expect(opened.Suspended()).To(BeTrue())
		Expect(eventTypes(records(run))).To(ContainElement(session.EventTypeSessionResumed), "the refusal is recorded")

		Expect(opened.Close(ctx)).To(Succeed(), "closing a suspended session closes no executor")
		Expect(opened.Suspend(ctx, time.Minute)).To(MatchError(session.ErrSessionClosed))
		Expect(opened.Resume(ctx, time.Minute)).To(MatchError(session.ErrSessionClosed))
	})
})
