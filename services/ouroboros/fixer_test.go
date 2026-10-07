// Copyright 2026 Candace Labs

package ouroboros_test

import (
	"context"
	"log/slog"
	"testing/fstest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/kernel/clock"
	iofs "github.com/candacelabs/csf/io/kernel/fs"
	"github.com/candacelabs/csf/io/ipc/proc"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/services/ouroboros"
	"github.com/candacelabs/csf/services/ouroboros/mocks"
)

const (
	usdMicros        = 1_000_000
	ticketNumber     = int64(249)
	syntheticTicket  = int64(217)
	proposalID       = "IC_kwDOO1"
	fixerAssignment  = "0caf2d51-1ee7-468a-882f-ac478a437640"
	launchedResponse = "launched"
)

var (
	miningTickets = []ouroboros.Ticket{{Number: ticketNumber, Title: "NO-SELF-MERGE", Severity: "S0"}}
	proposal      = []ouroboros.Comment{{ID: proposalID, Body: specProposalBody, CreatedAt: specStart}}
)

// budgetFixture is the budget-cap spec's loop: a controllable clock and a
// gomock ledger, so the spend the cap reads is exactly what the spec says.
type budgetFixture struct {
	loop     *ouroboros.Loop
	ledger   *mocks.MockIStore
	sessions *MockIAgentSessions
	tickets  *mocks.MockITickets
	clock    *clock.ManualClock
}

func newBudgetFixture(launch bool) *budgetFixture {
	GinkgoHelper()
	controller := gomock.NewController(GinkgoT())
	spec := &budgetFixture{
		ledger:   mocks.NewMockIStore(controller),
		sessions: NewMockIAgentSessions(controller),
		tickets:  mocks.NewMockITickets(controller),
		clock:    clock.NewManualClock(specStart),
	}
	stateFiles, err := iofs.NewHostFiles(GinkgoT().TempDir())
	Expect(err).NotTo(HaveOccurred())
	spec.loop, err = ouroboros.NewLoop(
		ouroboros.WithLedger(spec.ledger),
		ouroboros.WithSessions(spec.sessions),
		ouroboros.WithLauncher(NewMockILauncher(controller)),
		ouroboros.WithTickets(spec.tickets),
		ouroboros.WithClock(spec.clock),
		ouroboros.WithCorpus("/corpus", fstest.MapFS{runLog: &fstest.MapFile{Data: []byte(logLine)}}, NewMockIWatcher(controller)),
		ouroboros.WithState(stateFiles.Directory(), stateFiles),
		ouroboros.WithRepository("/repository", fstest.MapFS{}),
		ouroboros.WithMergerIdentity(specMerger),
		ouroboros.WithFixerLaunch(launch),
		ouroboros.WithLogger(slog.New(slog.DiscardHandler)),
	)
	Expect(err).NotTo(HaveOccurred())
	spec.tickets.EXPECT().Repository().Return(specRepository).AnyTimes()
	spec.ledger.EXPECT().RunningFixers(gomock.Any()).Return(nil, nil)
	spec.tickets.EXPECT().ListMiningTickets(gomock.Any()).Return(miningTickets, nil)
	spec.tickets.EXPECT().TicketComments(gomock.Any(), ticketNumber).Return(proposal, nil)
	return spec
}

// expectLaunchable has the ledger report the ticket's proposal as passed
// and the ticket without a fixer, so only the budget decides.
func (spec *budgetFixture) expectLaunchable() {
	spec.ledger.EXPECT().Proposal(gomock.Any(), ticketNumber, proposalID).Return(ouroboros.ProposalRecord{Ticket: ticketNumber, ID: proposalID, Verdict: ouroboros.VerdictLaunch, Reason: "found"}, true, nil)
	spec.ledger.EXPECT().FixersForTicket(gomock.Any(), ticketNumber).Return(nil, nil)
}

func finished(cost int64) ouroboros.Fixer {
	return ouroboros.Fixer{AssignmentID: otherAssignment, Ticket: 1, StartedAt: specStart.Add(-time.Hour), FinishedAt: specStart.Add(-time.Minute), Outcome: ouroboros.OutcomeReady, CostUSDMicros: cost}
}

var _ = Describe("The daily fixer budget", func() {
	It("stops launches once the day's spend reaches $100", func() {
		spec := newBudgetFixture(true)
		spec.expectLaunchable()
		spec.ledger.EXPECT().FixersStartedSince(gomock.Any(), gomock.Any()).Return([]ouroboros.Fixer{finished(60 * usdMicros), finished(40 * usdMicros)}, nil).Times(2)
		Expect(spec.loop.Fixers(context.Background())).To(Succeed(), "a spent budget is a wait, not a failure")
	})

	It("counts a running fixer's expected cost against the cap", func() {
		spec := newBudgetFixture(true)
		spec.expectLaunchable()
		running := ouroboros.Fixer{AssignmentID: fixerAssignment, Ticket: 2, StartedAt: specStart.Add(-time.Minute), Outcome: ouroboros.OutcomeRunning, CostUSDMicros: 49_500_000}
		spec.ledger.EXPECT().FixersStartedSince(gomock.Any(), gomock.Any()).Return([]ouroboros.Fixer{finished(50 * usdMicros), running}, nil).Times(2)
		Expect(spec.loop.Fixers(context.Background())).To(Succeed())
	})

	It("launches one fixer through the harness while the budget allows, and records it", func() {
		spec := newBudgetFixture(true)
		spec.expectLaunchable()
		spec.ledger.EXPECT().FixersStartedSince(gomock.Any(), gomock.Any()).Return([]ouroboros.Fixer{finished(90 * usdMicros)}, nil).Times(2)
		spec.sessions.EXPECT().Submit(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, request *harnessv1.SubmitAgentSessionRequest) (*harnessv1.SubmitAgentSessionResponse, error) {
			recipe := request.GetRecipe()
			Expect(recipe.GetAgent().GetId()).To(Equal(ouroboros.FixerAgentID))
			Expect(recipe.GetModel()).To(Equal(ouroboros.DefaultFixerModel))
			Expect(recipe.GetTicketUrl()).To(Equal("https://github.com/candacelabs/csf_staging/issues/249"))
			Expect(recipe.GetWorkspace().GetBranch()).To(Equal("miner/no_self_merge"))
			Expect(recipe.GetWorkspace().GetRepositoryPath()).To(Equal("/repository"))
			Expect(recipe.GetTask()).To(ContainSubstring("services/ouroboros/miners/no_self_merge/"))
			return &harnessv1.SubmitAgentSessionResponse{Check: &harnessv1.LaunchCheck{Admitted: true, WorkerCap: 4, RunningSessions: 1}}, nil
		})
		spec.ledger.EXPECT().RecordFixer(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, fixer ouroboros.Fixer) error {
			Expect(fixer.Ticket).To(Equal(ticketNumber))
			Expect(fixer.ProposalID).To(Equal(proposalID))
			Expect(fixer.Miner).To(Equal("no_self_merge"))
			Expect(fixer.Outcome).To(Equal(ouroboros.OutcomeRunning))
			Expect(fixer.StartedAt).To(Equal(specStart))
			return nil
		})
		Expect(spec.loop.Fixers(context.Background())).To(Succeed())
	})

	It("reports the day's budget: cap, spend, the reservation per running fixer and what is left", func() {
		ledger := mocks.NewMockIStore(gomock.NewController(GinkgoT()))
		spec := newFixture(ouroboros.WithLedger(ledger))
		running := ouroboros.Fixer{AssignmentID: fixerAssignment, Ticket: 2, StartedAt: specStart.Add(-time.Minute), Outcome: ouroboros.OutcomeRunning, CostUSDMicros: 5 * usdMicros}
		ledger.EXPECT().FixersStartedSince(gomock.Any(), gomock.Any()).Return([]ouroboros.Fixer{finished(30 * usdMicros), running}, nil).Times(2)
		budget, err := spec.loop.DailyBudget(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(budget).To(Equal(ouroboros.DailyBudget{
			Day: "2026-10-04", CapUSDMicros: ouroboros.DefaultDailyBudgetUSDMicros, SpentUSDMicros: 35 * usdMicros,
			ReservedUSDMicros: 741_776, ExpectedSessionUSDMicros: 741_776, MeasuredSessions: 0,
		}), "under ten finished fixers the reservation is the measured baseline")
		Expect(budget.LeftUSDMicros()).To(Equal(int64(100*usdMicros - 35*usdMicros - 741_776)))
	})

	It("tells the merge observer about every pull request the train merged", func() {
		var observed []int64
		spec := newFixture(ouroboros.WithMergeObserver(func(_ context.Context, pull ouroboros.PullRequest) { observed = append(observed, pull.Number) }))
		spec.tickets.EXPECT().ListReadyPullRequests(gomock.Any()).Return([]ouroboros.PullRequest{{Number: 11, Title: "MINER", HeadSHA: "ccccccc", CreatedAt: specStart}}, nil)
		spec.tickets.EXPECT().PullRequestSessions(gomock.Any(), int64(11)).Return([]string{runAssignment}, nil)
		spec.launcher.EXPECT().Run(gomock.Any(), gomock.Any()).Return(proc.Result{Stdout: []byte("check-merge: passed\n")}, nil)
		Expect(spec.loop.MergeTrain(context.Background())).To(Succeed())
		Expect(observed).To(Equal([]int64{11}))
		_, err := ouroboros.NewLoop(ouroboros.WithMergeObserver(nil))
		Expect(err).To(MatchError(ouroboros.ErrInvalidOption))
	})

	It("launches nothing while fixers are off, and still pre-checks", func() {
		spec := newBudgetFixture(false)
		spec.ledger.EXPECT().Proposal(gomock.Any(), ticketNumber, proposalID).Return(ouroboros.ProposalRecord{}, false, nil)
		spec.ledger.EXPECT().RecordProposal(gomock.Any(), gomock.Any()).DoAndReturn(func(_ context.Context, record ouroboros.ProposalRecord) error {
			Expect(record.Verdict).To(Equal(ouroboros.VerdictLaunch))
			Expect(record.Reason).To(ContainSubstring("run " + runAssignment))
			Expect(record.Instances).To(Equal([]string{runAssignment}))
			return nil
		})
		spec.ledger.EXPECT().FixersForTicket(gomock.Any(), ticketNumber).Return(nil, nil)
		Expect(spec.loop.Fixers(context.Background())).To(Succeed())
	})
})

var _ = Describe("The pre-check", func() {
	It("queues a ticket whose proposal names no real instance, at no model cost", func() {
		ctx := context.Background()
		spec := newFixture(ouroboros.WithFixerLaunch(true))
		spec.tickets.EXPECT().ListMiningTickets(gomock.Any()).Return([]ouroboros.Ticket{{Number: syntheticTicket, Title: "SERVE-DRAIN", Severity: "S1"}}, nil).Times(2)
		spec.tickets.EXPECT().TicketComments(gomock.Any(), syntheticTicket).Return([]ouroboros.Comment{{ID: "IC_syn", Body: syntheticBody, CreatedAt: specStart}}, nil).Times(2)
		Expect(spec.loop.Fixers(ctx)).To(Succeed())
		queue, err := spec.ledger.ProposalsNeedingLabels(ctx)
		Expect(err).NotTo(HaveOccurred())
		Expect(queue).To(HaveLen(1))
		Expect(queue[0].Ticket).To(Equal(syntheticTicket))
		Expect(queue[0].Miner).To(Equal("serve_drain_resume"))
		Expect(queue[0].Instances).To(Equal([]string{"test-drain-ok-001"}))
		Expect(spec.loop.Fixers(ctx)).To(Succeed(), "a judged proposal is not judged again")
	})

	It("passes a ticket naming a pull request its miner reads, and refuses the ticket itself", func() {
		ctx := context.Background()
		spec := newFixture()
		body := "| field | value |\n|---|---|\n| miner | `no_self_merge` |\n| corpus | pull_request |\n\n" +
			"| labeled instance | sign | source |\n|---|---|---|\n| `#249 and #226` | + | ticket |\n"
		spec.tickets.EXPECT().ListMiningTickets(gomock.Any()).Return(miningTickets, nil)
		spec.tickets.EXPECT().TicketComments(gomock.Any(), ticketNumber).Return([]ouroboros.Comment{{ID: "IC_pr", Body: body, CreatedAt: specStart}}, nil)
		spec.tickets.EXPECT().Reference(gomock.Any(), int64(226)).Return(ouroboros.ReferencePullRequest, nil)
		Expect(spec.loop.Fixers(ctx)).To(Succeed())
		record, judged, err := spec.ledger.Proposal(ctx, ticketNumber, "IC_pr")
		Expect(err).NotTo(HaveOccurred())
		Expect(judged).To(BeTrue())
		Expect(record.Verdict).To(Equal(ouroboros.VerdictLaunch))
		Expect(record.Reason).To(Equal("named instance exists in the corpus: pull_request 226"))
	})
})

var _ = Describe("Reconciling a running fixer", func() {
	It("closes a fixer whose one turn is done and records its pull request and cost", func() {
		ctx := context.Background()
		spec := newFixture()
		Expect(spec.ledger.RecordProposal(ctx, ouroboros.ProposalRecord{Ticket: ticketNumber, ID: proposalID, Miner: "no_self_merge", Corpus: "pull_request", Instances: []string{"#226"}, Verdict: ouroboros.VerdictLaunch, Reason: "found", CheckedAt: specStart})).To(Succeed())
		Expect(spec.ledger.RecordFixer(ctx, ouroboros.Fixer{AssignmentID: fixerAssignment, Ticket: ticketNumber, ProposalID: proposalID, Miner: "no_self_merge", Model: ouroboros.DefaultFixerModel, StartedAt: specStart, Outcome: ouroboros.OutcomeRunning})).To(Succeed())
		spec.sessions.EXPECT().Get(gomock.Any(), gomock.Any()).Return(&harnessv1.GetAgentSessionResponse{Session: &harnessv1.AgentSessionState{
			AssignmentId: fixerAssignment, Phase: harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN, Turns: 1, PullRequestUrl: "https://github.com/candacelabs/csf_staging/pull/307",
		}}, nil)
		spec.sessions.EXPECT().Cancel(gomock.Any(), gomock.Any()).Return(&harnessv1.CancelAgentSessionResponse{}, nil)
		spec.tickets.EXPECT().PullRequest(gomock.Any(), int64(307)).Return(ouroboros.PullRequest{Number: 307, Draft: false}, nil)
		spec.tickets.EXPECT().ListMiningTickets(gomock.Any()).Return(nil, nil)
		Expect(spec.loop.Fixers(ctx)).To(Succeed())
		fixers, err := spec.ledger.FixersForTicket(ctx, ticketNumber)
		Expect(err).NotTo(HaveOccurred())
		Expect(fixers).To(HaveLen(1))
		Expect(fixers[0].Outcome).To(Equal(ouroboros.OutcomeReady))
		Expect(fixers[0].PullRequestURL).To(Equal("https://github.com/candacelabs/csf_staging/pull/307"))
		Expect(fixers[0].FinishedAt).To(Equal(specStart))
	})
})
