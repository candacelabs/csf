// Copyright 2026 Candace Labs

package harness_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/ipc/model"
	"github.com/candacelabs/csf/ipc/model/claudecode"
	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/pkg/eventually"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/harness/mocks"
	"github.com/candacelabs/csf/services/harness/session"
	sessionmocks "github.com/candacelabs/csf/services/harness/session/mocks"
)

// settleBudget bounds a spec's wait for an owner goroutine to reach a phase;
// generous because the costs are not symmetric (CS-9).
var settleBudget = eventually.Budget{Within: 5 * time.Second}

const (
	firstAssignment   = "0caf2d51-1ee7-468a-882f-ac478a437640"
	secondAssignment  = "1d2e3f40-5161-4728-9a0b-c1d2e3f40516"
	unknownAssignment = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	hostPID           = 4242
	cores             = 8
	freeBytes         = 64 << 30
)

var frozen = time.Date(2026, 10, 2, 1, 0, 0, 0, time.UTC)

// armedTimer is one AfterFunc the service armed on the controllable clock.
type armedTimer struct {
	after time.Duration
	fire  func()
}

// fakeClock is the controllable clock: time stands still, After delivers
// when the spec ticks, and AfterFunc hands its timers to the spec.
type fakeClock struct {
	ticks  chan time.Time
	timers chan armedTimer
}

func newFakeClock() *fakeClock {
	return &fakeClock{ticks: make(chan time.Time, 64), timers: make(chan armedTimer, 64)}
}

func (clock *fakeClock) Now() time.Time { return frozen }

func (clock *fakeClock) After(_ time.Duration) (<-chan time.Time, func() bool) {
	return clock.ticks, func() bool { return true }
}

func (clock *fakeClock) AfterFunc(after time.Duration, fire func()) func() bool {
	clock.timers <- armedTimer{after: after, fire: fire}
	return func() bool { return true }
}

// executors hands one mock turn executor per opened session to the runner and
// records what each was opened with.
type executors struct {
	controller *gomock.Controller
	opened     chan openedExecutor
	fail       error
}

type openedExecutor struct {
	spec     session.TurnExecutorSpec
	executor *sessionmocks.MockIOpenTurnExecutor
}

func (factory *executors) open(_ context.Context, spec session.TurnExecutorSpec) (session.IOpenTurnExecutor, error) {
	if factory.fail != nil {
		return nil, factory.fail
	}
	executor := sessionmocks.NewMockIOpenTurnExecutor(factory.controller)
	factory.opened <- openedExecutor{spec: spec, executor: executor}
	return executor, nil
}

// turnScript scripts one executor: every Propose records its prompt and
// blocks until the spec releases it, or the turn's context ends, or an
// interrupt arrives.
type turnScript struct {
	prompts     chan string
	release     chan struct{}
	interrupted chan struct{}
	// ignoreInterrupts makes the executor stubborn: an interrupt is accepted
	// but the turn runs on until its context ends.
	ignoreInterrupts atomic.Bool
}

func newTurnScript() *turnScript {
	return &turnScript{prompts: make(chan string, 16), release: make(chan struct{}, 16), interrupted: make(chan struct{}, 16)}
}

// promptOf is the text of a turn's single user message.
func promptOf(turn *claudecode.Turn) string {
	var message struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	}
	Expect(turn.Messages).To(HaveLen(1))
	Expect(jsonUnmarshal(turn.Messages[0], &message)).To(Succeed())
	return message.Message.Content
}

func (script *turnScript) install(executor *sessionmocks.MockIOpenTurnExecutor) {
	executor.EXPECT().Propose(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, turn *claudecode.Turn) (*model.Proposal[claudecode.Event], error) {
		script.prompts <- promptOf(turn)
		select {
		case <-script.release:
			return &model.Proposal[claudecode.Event]{Provider: claudecode.ProviderName}, nil
		case <-script.interrupted:
			return &model.Proposal[claudecode.Event]{Provider: claudecode.ProviderName}, nil
		case <-ctx.Done():
			return nil, &claudecode.TurnError{Err: errors.Join(claudecode.ErrTurnAbandoned, context.Cause(ctx))}
		}
	}).AnyTimes()
	executor.EXPECT().Interrupt(gomock.Any()).DoAndReturn(func(_ context.Context) error {
		if !script.ignoreInterrupts.Load() {
			script.interrupted <- struct{}{}
		}
		return nil
	}).AnyTimes()
}

func newRecipe(assignment string, branch string, repository string) *pb.AgentAssignmentRecipe {
	return &pb.AgentAssignmentRecipe{
		AssignmentId: assignment,
		Agent:        &pb.AgentDefinition{Id: "scratch", Revision: 1, DisplayName: "Scratch", Instructions: "Work in the worktree."},
		TicketUrl:    "https://example.invalid/issues/1",
		Task:         "Add one line to notes.txt and commit it.",
		Model:        "sonnet",
		RepositoryId: "scratch",
		Workspace: &pb.AgentWorkspace{
			RepositoryPath: repository, BaseBranch: "main", Branch: branch,
			AllowedTools: []string{"Bash", "Edit"}, PullRequestTitle: "H2 scratch change",
		},
	}
}

// launched matches a program by executable and leading arguments.
func launched(executable string, arguments ...string) gomock.Matcher {
	return gomock.Cond(func(command proc.Command) bool {
		return command.Executable == executable && len(command.Arguments) >= len(arguments) &&
			slices.Equal(command.Arguments[:len(arguments)], arguments)
	})
}

var _ = Describe("AgentSessionService", func() {
	var (
		controller *gomock.Controller
		launcher   *MockILauncher
		measures   *mocks.MockIHostMeasures
		clock      *fakeClock
		factory    *executors
		state      string
		repository string
		service    *harness.AgentSessionService
		scope      *runtime.Scope
		stopped    chan struct{}
		baseline   goleak.Option
		ctx        context.Context
	)

	phaseOf := func(assignment string) harnessv1.AgentSessionPhase {
		response, err := service.Get(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: assignment})
		if err != nil {
			return harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_UNSPECIFIED
		}
		return response.GetSession().GetPhase()
	}

	awaitPhase := func(assignment string, want harnessv1.AgentSessionPhase) {
		eventually.Await(GinkgoT(), assignment+" to reach "+want.String(), settleBudget,
			func() harnessv1.AgentSessionPhase { return phaseOf(assignment) },
			func(phase harnessv1.AgentSessionPhase) bool { return phase == want })
	}

	// opened waits for the owner to open the next executor and scripts it.
	opened := func() (openedExecutor, *turnScript) {
		var next openedExecutor
		Eventually(factory.opened, settleBudget.Within).Should(Receive(&next), "an executor is opened for the session")
		script := newTurnScript()
		script.install(next.executor)
		return next, script
	}

	BeforeEach(func() {
		baseline = goleak.IgnoreCurrent()
		ctx = context.Background()
		controller = gomock.NewController(GinkgoT())
		launcher = NewMockILauncher(controller)
		measures = mocks.NewMockIHostMeasures(controller)
		clock = newFakeClock()
		factory = &executors{controller: controller, opened: make(chan openedExecutor, 8)}
		state = GinkgoT().TempDir()
		repository = GinkgoT().TempDir()
		launcher.EXPECT().Run(gomock.Any(), launched("git")).Return(proc.Result{}, nil).AnyTimes()
		launcher.EXPECT().Run(gomock.Any(), launched("gh")).Return(proc.Result{}, nil).AnyTimes()
		measures.EXPECT().Cores().Return(cores).AnyTimes()
		measures.EXPECT().LoadAverage().Return(1.5, nil).AnyTimes()
		measures.EXPECT().FreeBytes(gomock.Any()).Return(uint64(freeBytes), nil).AnyTimes()
		measures.EXPECT().DirectoryBytes(gomock.Any()).Return(uint64(1<<20), nil).AnyTimes()
		runner, err := session.NewAgentSessionRunner(
			session.WithLauncher(launcher),
			session.WithStateDirectory(state),
			session.WithGateCommand("/opt/csf/harness", "gate"),
			session.WithOpenTurnExecutors(factory.open),
		)
		Expect(err).NotTo(HaveOccurred())
		stopped = make(chan struct{})
		service, err = harness.NewAgentSessionService(
			harness.WithSessionRunner(runner),
			harness.WithHostMeasures(measures),
			harness.WithClock(clock),
			harness.WithHostPID(hostPID),
			harness.WithStopRequest(func() { close(stopped) }),
		)
		Expect(err).NotTo(HaveOccurred())
		scope = runtime.NewScope(ctx, "spec")
		Expect(service.Start(scope)).To(Succeed())
	})

	AfterEach(func() {
		Expect(scope.Close()).To(Succeed())
		Expect(goleak.Find(baseline)).To(Succeed(), "every goroutine the service started must join")
	})

	Describe("construction", func() {
		It("requires the runner and rejects options it cannot use", func() {
			_, err := harness.NewAgentSessionService()
			Expect(err).To(MatchError(harness.ErrNoRunner))
			_, err = harness.NewAgentSessionService(harness.WithClock(nil))
			Expect(err).To(MatchError(harness.ErrInvalidServiceOption))
			_, err = harness.NewAgentSessionService(nil)
			Expect(err).To(MatchError(harness.ErrInvalidServiceOption))
		})

		It("answers nothing before it is started", func() {
			runner, err := session.NewAgentSessionRunner(session.WithLauncher(launcher), session.WithStateDirectory(state), session.WithGateCommand("/opt/csf/harness", "gate"))
			Expect(err).NotTo(HaveOccurred())
			unstarted, err := harness.NewAgentSessionService(harness.WithSessionRunner(runner))
			Expect(err).NotTo(HaveOccurred())
			bounded, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			_, err = unstarted.List(bounded, &harnessv1.ListAgentSessionsRequest{})
			Expect(err).To(HaveOccurred())
		})
	})

	Describe("sessions in one process", func() {
		It("runs two submitted sessions concurrently, each on its own open executor", func() {
			first, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			firstExecutor, firstScript := opened()
			firstExecutor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			second, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(secondAssignment, "h2/second", repository)})
			Expect(err).NotTo(HaveOccurred())
			secondExecutor, secondScript := opened()
			secondExecutor.executor.EXPECT().Close(gomock.Any()).Return(nil)

			Expect(first.GetReceipt().GetBranch()).To(Equal("h2/first"))
			Expect(first.GetReceipt().GetTraceId()).To(MatchRegexp("^[0-9a-f]{32}$"))
			Expect(first.GetReceipt().GetWorktreeId()).To(Equal(filepath.Join(state, firstAssignment, session.WorktreeDirectory)))
			Expect(first.GetCheck().GetReportOnly()).To(BeTrue())
			Expect(first.GetCheck().GetAdmitted()).To(BeTrue())
			Expect(first.GetCheck().GetWorkerCap()).To(BeNumerically("==", cores-2), "8 cores minus a load of 1.5, floored")
			Expect(second.GetSession().GetAssignmentId()).To(Equal(secondAssignment))

			Eventually(firstScript.prompts, settleBudget.Within).Should(Receive(ContainSubstring("Add one line to notes.txt")), "the first turn is the task")
			Eventually(secondScript.prompts, settleBudget.Within).Should(Receive(ContainSubstring("Assignment: " + secondAssignment)))
			listed, err := service.List(ctx, &harnessv1.ListAgentSessionsRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetHostPid()).To(BeNumerically("==", hostPID))
			Expect(listed.GetSessions()).To(HaveLen(2))
			Expect(listed.GetSessions()[0].GetPhase()).To(Equal(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING))
			Expect(listed.GetSessions()[1].GetPhase()).To(Equal(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING))

			Expect(firstExecutor.spec.Environment).To(ContainElements(
				"CANDACE_BAZEL_DISK_CACHE="+filepath.Join(state, harness.BazelDiskCacheDirectory),
				"CANDACE_BAZEL_CACHE="+filepath.Join(state, firstAssignment, harness.BazelOutputDirectory)))
			Expect(secondExecutor.spec.Environment).To(ContainElement("CANDACE_BAZEL_CACHE=" + filepath.Join(state, secondAssignment, harness.BazelOutputDirectory)))
			Expect(filepath.Join(state, harness.BazelDiskCacheDirectory)).To(BeADirectory())

			firstScript.release <- struct{}{}
			secondScript.release <- struct{}{}
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN)
			awaitPhase(secondAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN)
			for _, assignment := range []string{firstAssignment, secondAssignment} {
				Expect(filepath.Join(state, assignment, session.EventsFile)).To(BeAnExistingFile())
			}
		})

		It("refuses a second session for the same assignment", func() {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, _ := opened()
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			_, err = service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/again", repository)})
			Expect(err).To(MatchError(harness.ErrSessionExists))
			Expect(err).To(MatchError(csf.ErrConflict))
		})

		It("refuses every session while admission is held, and admits again once it is released", func() {
			service.HoldAdmission("free disk is below the housekeeping floor")
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).To(MatchError(harness.ErrAdmissionHeld))
			Expect(err).To(MatchError(csf.ErrConflict))
			Expect(err).To(MatchError(ContainSubstring("below the housekeeping floor")))
			listed, err := service.List(ctx, &harnessv1.ListAgentSessionsRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetSessions()).To(BeEmpty())

			service.ReleaseAdmission()
			_, err = service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, _ := opened()
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
		})

		It("refuses a recipe it cannot prepare", func() {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{})
			Expect(err).To(MatchError(csf.ErrInvalidRequest))
			broken := newRecipe(firstAssignment, "main", repository)
			_, err = service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: broken})
			Expect(err).To(MatchError(session.ErrInvalidRecipe))
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED)
		})

		It("reports a session whose executor would not open", func() {
			factory.fail = errors.New("no claude on this host")
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).To(MatchError(ContainSubstring("no claude on this host")))
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_FAILED)
			got, err := service.Get(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: firstAssignment})
			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetSession().GetError()).To(ContainSubstring("no claude on this host"))
		})
	})

	Describe("Send", func() {
		It("queues messages as turns on the open executor, in order", func() {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, script := opened()
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Eventually(script.prompts, settleBudget.Within).Should(Receive(ContainSubstring("Add one line")))

			queued, err := service.Send(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: firstAssignment, Message: "Now address the review."})
			Expect(err).NotTo(HaveOccurred())
			Expect(queued.GetTurnId()).To(Equal("2"))
			Expect(queued.GetSession().GetQueued()).To(BeNumerically("==", 1))
			third, err := service.Send(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: firstAssignment, Message: "And then rebase."})
			Expect(err).NotTo(HaveOccurred())
			Expect(third.GetTurnId()).To(Equal("3"))

			script.release <- struct{}{}
			Eventually(script.prompts, settleBudget.Within).Should(Receive(Equal("Now address the review.")))
			script.release <- struct{}{}
			Eventually(script.prompts, settleBudget.Within).Should(Receive(Equal("And then rebase.")))
			script.release <- struct{}{}
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN)
			got, err := service.Get(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: firstAssignment})
			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetSession().GetTurns()).To(BeNumerically("==", 3))
			Expect(got.GetSession().GetQueued()).To(BeZero())
		})

		It("refuses an unknown session, a malformed request and a finished session", func() {
			_, err := service.Send(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: unknownAssignment, Message: "hello"})
			Expect(err).To(MatchError(harness.ErrUnknownSession))
			Expect(err).To(MatchError(csf.ErrNotFound))
			_, err = service.Send(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: "not-a-uuid", Message: "hello"})
			Expect(err).To(MatchError(csf.ErrInvalidRequest))
			_, err = service.Send(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: unknownAssignment})
			Expect(err).To(MatchError(csf.ErrInvalidRequest))

			_, err = service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, script := opened()
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Eventually(script.prompts, settleBudget.Within).Should(Receive(Not(BeEmpty())))
			script.release <- struct{}{}
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN)
			_, err = service.Cancel(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: firstAssignment})
			Expect(err).NotTo(HaveOccurred())
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED)
			_, err = service.Send(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: firstAssignment, Message: "too late"})
			Expect(err).To(MatchError(harness.ErrSessionFinished))
		})
	})

	Describe("Cancel", func() {
		It("stops at once between turns, closes the executor and removes the Bazel output base", func() {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, script := opened()
			closed := make(chan struct{})
			executor.executor.EXPECT().Close(gomock.Any()).DoAndReturn(func(_ context.Context) error { close(closed); return nil })
			Eventually(script.prompts, settleBudget.Within).Should(Receive(Not(BeEmpty())))
			script.release <- struct{}{}
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN)
			outputBase := filepath.Join(state, firstAssignment, harness.BazelOutputDirectory)
			Expect(os.MkdirAll(outputBase, 0o700)).To(Succeed())

			canceled, err := service.Cancel(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: firstAssignment})
			Expect(err).NotTo(HaveOccurred())
			Expect(canceled.GetSession().GetPhase()).To(Equal(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELING))
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED)
			Eventually(closed).Should(BeClosed())
			Expect(outputBase).NotTo(BeADirectory())
			Expect(clock.timers).To(BeEmpty(), "no interrupt budget was armed: the cancel landed between turns")

			again, err := service.Cancel(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: firstAssignment})
			Expect(err).NotTo(HaveOccurred())
			Expect(again.GetSession().GetPhase()).To(Equal(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED), "a second cancel reports the state as it is")
		})

		It("interrupts a running turn and stops when it reaches its safepoint", func() {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, script := opened()
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Eventually(script.prompts, settleBudget.Within).Should(Receive(Not(BeEmpty())))
			Expect(phaseOf(firstAssignment)).To(Equal(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING))

			canceled, err := service.Cancel(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: firstAssignment})
			Expect(err).NotTo(HaveOccurred())
			Expect(canceled.GetSession().GetPhase()).To(Equal(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELING))
			var armed armedTimer
			Eventually(clock.timers).Should(Receive(&armed))
			Expect(armed.after).To(Equal(harness.DefaultInterruptBudget), "the kill is armed on the interrupt budget")
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED)
			got, err := service.Get(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: firstAssignment})
			Expect(err).NotTo(HaveOccurred())
			Expect(got.GetSession().GetTurns()).To(BeNumerically("==", 1))
			Expect(got.GetSession().GetError()).To(BeEmpty())
		})

		It("kills a turn that reaches no safepoint within the budget", func() {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, script := opened()
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Eventually(script.prompts, settleBudget.Within).Should(Receive(Not(BeEmpty())))
			// This executor ignores the interrupt: Propose returns only when
			// its context ends.
			script.ignoreInterrupts.Store(true)
			_, err = service.Cancel(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: firstAssignment})
			Expect(err).NotTo(HaveOccurred())
			var armed armedTimer
			Eventually(clock.timers).Should(Receive(&armed))
			armed.fire()
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED)
		})

		It("refuses an unknown session and a malformed identifier", func() {
			_, err := service.Cancel(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: unknownAssignment})
			Expect(err).To(MatchError(harness.ErrUnknownSession))
			_, err = service.Cancel(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: "nope"})
			Expect(err).To(MatchError(csf.ErrInvalidRequest))
			_, err = service.Get(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: unknownAssignment})
			Expect(err).To(MatchError(csf.ErrNotFound))
		})
	})

	Describe("the launch check", func() {
		It("reports a host over its worker cap and admits the session anyway", func() {
			saturated := mocks.NewMockIHostMeasures(controller)
			saturated.EXPECT().Cores().Return(cores).AnyTimes()
			saturated.EXPECT().LoadAverage().Return(float64(cores)+3, nil).AnyTimes()
			saturated.EXPECT().FreeBytes(gomock.Any()).Return(uint64(1<<20), nil).AnyTimes()
			saturated.EXPECT().DirectoryBytes(gomock.Any()).Return(uint64(1<<30), nil).AnyTimes()
			runner, err := session.NewAgentSessionRunner(session.WithLauncher(launcher), session.WithStateDirectory(state),
				session.WithGateCommand("/opt/csf/harness", "gate"), session.WithOpenTurnExecutors(factory.open))
			Expect(err).NotTo(HaveOccurred())
			Expect(os.MkdirAll(filepath.Join(state, "an-earlier-run"), 0o700)).To(Succeed())
			checked, err := harness.NewAgentSessionService(harness.WithSessionRunner(runner), harness.WithHostMeasures(saturated), harness.WithClock(clock))
			Expect(err).NotTo(HaveOccurred())
			checkedScope := runtime.NewScope(ctx, "checked")
			Expect(checked.Start(checkedScope)).To(Succeed())
			defer func() { Expect(checkedScope.Close()).To(Succeed()) }()

			response, err := checked.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, script := opened()
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Eventually(script.prompts, settleBudget.Within).Should(Receive(Not(BeEmpty())))
			check := response.GetCheck()
			Expect(check.GetReportOnly()).To(BeTrue())
			Expect(check.GetAdmitted()).To(BeFalse())
			Expect(check.GetWorkerCap()).To(BeNumerically("==", 1))
			Expect(check.GetCores()).To(BeNumerically("==", cores))
			Expect(check.GetLoadOneMinute()).To(BeNumerically("==", cores+3))
			Expect(check.GetFreeBytes()).To(BeNumerically("==", 1<<20))
			Expect(check.GetFindings()).To(HaveLen(1), "one session fits the cap of one; only the disk floor fires")
			Expect(check.GetFindings()[0]).To(ContainSubstring("below the floor"))
			Expect(response.GetSession().GetPhase()).To(BeElementOf(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_RUNNING),
				"admitted despite the findings: open, or already on its first turn")
		})
	})

	Describe("Stop", func() {
		It("asks the host to shut down and counts the running sessions", func() {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, script := opened()
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Eventually(script.prompts, settleBudget.Within).Should(Receive(Not(BeEmpty())))
			response, err := service.Stop(ctx, &harnessv1.StopHarnessRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(response.GetSessionsRunning()).To(BeNumerically("==", 1))
			Expect(stopped).To(BeClosed())
		})
	})

	Describe("shutdown", func() {
		It("closes every open executor when the scope ends, turns in flight included", func() {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			first, firstScript := opened()
			_, err = service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(secondAssignment, "h2/second", repository)})
			Expect(err).NotTo(HaveOccurred())
			second, secondScript := opened()
			Eventually(firstScript.prompts, settleBudget.Within).Should(Receive(Not(BeEmpty())))
			Eventually(secondScript.prompts, settleBudget.Within).Should(Receive(Not(BeEmpty())))
			secondScript.release <- struct{}{}
			awaitPhase(secondAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN)
			closed := make(chan string, 2)
			first.executor.EXPECT().Close(gomock.Any()).DoAndReturn(func(_ context.Context) error { closed <- firstAssignment; return nil })
			second.executor.EXPECT().Close(gomock.Any()).DoAndReturn(func(_ context.Context) error { closed <- secondAssignment; return nil })

			Expect(scope.Close()).To(Succeed())
			Expect(closed).To(HaveLen(2))
			bounded, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
			defer cancel()
			_, err = service.List(bounded, &harnessv1.ListAgentSessionsRequest{})
			Expect(err).To(MatchError(harness.ErrNotStarted))
		})
	})

	Describe("resume on start", func() {
		// restart builds a second service over the same state directory, as a
		// restarted host does, and starts it on its own scope.
		restart := func() (*harness.AgentSessionService, *runtime.Scope) {
			runner, err := session.NewAgentSessionRunner(session.WithLauncher(launcher), session.WithStateDirectory(state),
				session.WithGateCommand("/opt/csf/harness", "gate"), session.WithOpenTurnExecutors(factory.open))
			Expect(err).NotTo(HaveOccurred())
			restarted, err := harness.NewAgentSessionService(harness.WithSessionRunner(runner), harness.WithHostMeasures(measures), harness.WithClock(clock))
			Expect(err).NotTo(HaveOccurred())
			restartedScope := runtime.NewScope(ctx, "restarted")
			Expect(restarted.Start(restartedScope)).To(Succeed())
			return restarted, restartedScope
		}

		It("reopens every open session after a stop, with the same ids, conversations and queue", func() {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			first, firstScript := opened()
			_, err = service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(secondAssignment, "h2/second", repository)})
			Expect(err).NotTo(HaveOccurred())
			second, secondScript := opened()
			Expect(<-firstScript.prompts).To(ContainSubstring("Add one line"))
			Expect(<-secondScript.prompts).NotTo(BeEmpty())
			secondScript.release <- struct{}{}
			awaitPhase(secondAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN)
			_, err = service.Send(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: firstAssignment, Message: "Now address the review."})
			Expect(err).NotTo(HaveOccurred())
			first.executor.EXPECT().Close(gomock.Any()).Return(nil)
			second.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Expect(scope.Close()).To(Succeed())

			restarted, restartedScope := restart()
			reopenedFirst, reopenedFirstScript := opened()
			reopenedSecond, _ := opened()
			Expect(reopenedFirst.spec.Session).To(Equal(first.spec.Session))
			Expect(reopenedFirst.spec.Resume).To(BeTrue())
			Expect(reopenedFirst.spec.Directory).To(Equal(first.spec.Directory))
			Expect(reopenedSecond.spec.Session).To(Equal(second.spec.Session))
			Expect(reopenedSecond.spec.Resume).To(BeTrue())
			Expect(<-reopenedFirstScript.prompts).To(And(HavePrefix(harness.RedeliveredPrefix), ContainSubstring("Add one line")),
				"the turn the stop cut is delivered again")
			reopenedFirstScript.release <- struct{}{}
			Expect(<-reopenedFirstScript.prompts).To(Equal("Now address the review."), "the queued message survives the stop")
			reopenedFirstScript.release <- struct{}{}

			listed, err := restarted.List(ctx, &harnessv1.ListAgentSessionsRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetSessions()).To(HaveLen(2))
			Expect([]string{listed.GetSessions()[0].GetAssignmentId(), listed.GetSessions()[1].GetAssignmentId()}).To(ConsistOf(firstAssignment, secondAssignment))
			reopenedFirst.executor.EXPECT().Close(gomock.Any()).Return(nil)
			reopenedSecond.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Expect(restartedScope.Close()).To(Succeed())
		})

		It("does not reopen a canceled session", func() {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, script := opened()
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Expect(<-script.prompts).NotTo(BeEmpty())
			script.release <- struct{}{}
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN)
			_, err = service.Cancel(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: firstAssignment})
			Expect(err).NotTo(HaveOccurred())
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED)
			Expect(filepath.Join(state, firstAssignment, harness.EndedFile)).To(BeAnExistingFile())
			Expect(scope.Close()).To(Succeed())

			restarted, restartedScope := restart()
			eventually.Await(GinkgoT(), "the restarted service to list its sessions", settleBudget,
				func() int {
					listed, err := restarted.List(ctx, &harnessv1.ListAgentSessionsRequest{})
					if err != nil {
						return -1
					}
					return len(listed.GetSessions())
				},
				func(count int) bool { return count == 0 })
			Consistently(factory.opened, 200*time.Millisecond).ShouldNot(Receive(), "no executor is reopened")
			Expect(restartedScope.Close()).To(Succeed())
		})
	})

	Describe("the event stream", func() {
		It("replays the run's records and follows them to the end", func() {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, script := opened()
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Eventually(script.prompts, settleBudget.Within).Should(Receive(Not(BeEmpty())))

			tail, err := service.OpenTail(ctx, firstAssignment, 0)
			Expect(err).NotTo(HaveOccurred())
			defer func() { Expect(tail.Close()).To(Succeed()) }()
			var types []string
			for range 3 {
				line, err := tail.Next(ctx)
				Expect(err).NotTo(HaveOccurred())
				var record struct {
					EventType string `json:"event_type"`
				}
				Expect(jsonUnmarshal(line, &record)).To(Succeed())
				types = append(types, record.EventType)
			}
			Expect(types).To(Equal([]string{session.EventTypeRunStarted, session.EventTypeWorktreeReady, session.EventTypeTurnRequested}))
			Expect(tail.Sequence()).To(Equal(3))

			script.release <- struct{}{}
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN)
			_, err = service.Cancel(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: firstAssignment})
			Expect(err).NotTo(HaveOccurred())
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED)
			var rest []string
			for {
				line, err := tail.Next(ctx)
				if errors.Is(err, io.EOF) {
					break
				}
				Expect(err).NotTo(HaveOccurred())
				var record struct {
					EventType string `json:"event_type"`
				}
				Expect(jsonUnmarshal(line, &record)).To(Succeed())
				rest = append(rest, record.EventType)
			}
			Expect(rest).To(ContainElements(session.EventTypeRunFinished, session.EventTypeSessionClosed))

			_, err = service.OpenTail(ctx, unknownAssignment, 0)
			Expect(err).To(MatchError(harness.ErrUnknownSession))
			_, err = service.OpenTail(ctx, "nope", 0)
			Expect(err).To(MatchError(harness.ErrUnknownSession))
		})
	})
})
