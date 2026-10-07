// Copyright 2026 Candace Labs

package harness_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/io/ipc/docker"
	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/kernel/sandbox"
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

// heldOpenBudget is how long a reopened run is watched for staying open.
var heldOpenBudget = eventually.Budget{Within: 500 * time.Millisecond}

const (
	firstAssignment   = "0caf2d51-1ee7-468a-882f-ac478a437640"
	secondAssignment  = "1d2e3f40-5161-4728-9a0b-c1d2e3f40516"
	thirdAssignment   = "2e3f4051-6172-4839-8a1b-d2e3f4051627"
	unknownAssignment = "ffffffff-ffff-4fff-8fff-ffffffffffff"
	hostPID           = 4242
	cores             = 8
	freeBytes         = 64 << 30
	// pullRequestURL is what gh pr list answers for every session's branch.
	pullRequestURL = "https://github.com/candacelabs/scratch/pull/7"
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
// records what each was opened with. The executor's script is installed
// before the owner can call it: an owner reopening a run with a queued turn
// proposes within microseconds of the open, before a spec polling for the
// executor could script it, and gomock would fail the unexpected call on the
// owner's goroutine.
type executors struct {
	controller *gomock.Controller
	opened     chan openedExecutor
	fail       error
}

type openedExecutor struct {
	spec     session.TurnExecutorSpec
	executor *sessionmocks.MockIOpenTurnExecutor
	script   *turnScript
}

func (factory *executors) open(_ context.Context, spec session.TurnExecutorSpec) (session.IOpenTurnExecutor, error) {
	if factory.fail != nil {
		return nil, factory.fail
	}
	executor := sessionmocks.NewMockIOpenTurnExecutor(factory.controller)
	script := newTurnScript()
	script.install(executor)
	factory.opened <- openedExecutor{spec: spec, executor: executor, script: script}
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

// allowModels records a model policy allowing models in the state
// directory, as the operator would edit it, before the service starts.
func allowModels(state string, models ...string) {
	GinkgoHelper()
	policy := harness.ModelPolicy{}
	for _, model := range models {
		policy.Allowed = append(policy.Allowed, harness.AllowedModel{Model: model, Ruling: "spec", RuledBy: "spec", RuledOn: "2026-10-05"})
	}
	Expect(harness.RecordModelPolicy(state, policy)).To(Succeed())
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
		// ghCalls receives every gh argument vector the launcher ran.
		ghCalls chan []string
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

	// opened waits for the owner to open the next executor and returns it with
	// the script the factory installed on it.
	opened := func() (openedExecutor, *turnScript) {
		var next openedExecutor
		Eventually(factory.opened, settleBudget.Within).Should(Receive(&next), "an executor is opened for the session")
		return next, next.script
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
		allowModels(state, "sonnet", "claude-haiku-4.5")
		repository = GinkgoT().TempDir()
		launcher.EXPECT().Run(gomock.Any(), launched("git")).Return(proc.Result{}, nil).AnyTimes()
		ghCalls = make(chan []string, 256)
		launcher.EXPECT().Run(gomock.Any(), launched("gh")).DoAndReturn(func(_ context.Context, command proc.Command) (proc.Result, error) {
			select {
			case ghCalls <- command.Arguments:
			default:
			}
			if slices.Contains(command.Arguments, "list") {
				return proc.Result{Stdout: []byte(pullRequestURL + "\n")}, nil
			}
			return proc.Result{}, nil
		}).AnyTimes()
		measures.EXPECT().Cores().Return(cores).AnyTimes()
		measures.EXPECT().LoadAverage().Return(1.5, nil).AnyTimes()
		measures.EXPECT().FreeBytes(gomock.Any()).Return(uint64(freeBytes), nil).AnyTimes()
		measures.EXPECT().DirectoryBytes(gomock.Any()).Return(uint64(1<<20), nil).AnyTimes()
		measures.EXPECT().Pressure(gomock.Any()).Return(proc.Pressure{}, nil).AnyTimes()
		measures.EXPECT().MemoryAvailable().Return(uint64(freeBytes), nil).AnyTimes()
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
			harness.WithLauncher(launcher),
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
			Expect(first.GetReceipt().GetExecutor()).To(Equal("claude-code"), "the receipt names the executor the recipe chose")
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

		It("counts a turn a background completion started between turns, and numbers the next message after it", func() {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, script := opened()
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Eventually(script.prompts, settleBudget.Within).Should(Receive())
			script.release <- struct{}{}
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN)

			executor.spec.TaskNotifications(claudecode.TaskNotification{TaskID: "b1", ToolUseID: "toolu_1", Status: "completed", Summary: "Run the suite"})
			eventually.Await(GinkgoT(), "the woken turn to be counted", settleBudget,
				func() uint32 {
					response, err := service.Get(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: firstAssignment})
					Expect(err).NotTo(HaveOccurred())
					return response.GetSession().GetTurns()
				},
				func(turns uint32) bool { return turns == 2 })
			results, err := session.ReadRecords(filepath.Join(state, firstAssignment), func(record *session.Record) bool {
				return record.EventType == session.EventTypeBackgroundResult
			})
			Expect(err).NotTo(HaveOccurred())
			Expect(results).To(HaveLen(1))
			Expect(results[0].Turn).To(Equal(2))

			sent, err := service.Send(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: firstAssignment, Message: "Merge it."})
			Expect(err).NotTo(HaveOccurred())
			Expect(sent.GetTurnId()).To(Equal("3"))
			Eventually(script.prompts, settleBudget.Within).Should(Receive(Equal("Merge it.")))
			script.release <- struct{}{}
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN)
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
			_, err = service.Check(ctx)
			Expect(err).To(MatchError(harness.ErrAdmissionHeld), "the check a dispatcher reads reports the hold too")
			Expect(err).To(MatchError(ContainSubstring("below the housekeeping floor")))

			service.ReleaseAdmission()
			check, err := service.Check(ctx)
			Expect(err).NotTo(HaveOccurred())
			Expect(check.GetWorkerCap()).To(Equal(harness.WorkerCap(uint32(cores), 1.5)), "the cores under the measured load")
			Expect(check.GetRunningSessions()).To(BeZero())
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

	Describe("the inbox", func() {
		// holdFirstTurn submits one session and leaves its first turn running,
		// so every message sent next queues behind it and stays listed. The
		// held turn ignores interrupts: an interrupt-class message is ordered
		// ahead of the queued ones instead of ending the turn.
		holdFirstTurn := func() {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, script := opened()
			script.ignoreInterrupts.Store(true)
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Eventually(script.prompts, settleBudget.Within).Should(Receive(ContainSubstring("Add one line")))
		}
		send := func(message string, class harnessv1.MessagePriorityClass) *harnessv1.InboxMessage {
			sent, err := service.Send(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: firstAssignment, Message: message, PriorityClass: class})
			Expect(err).NotTo(HaveOccurred())
			return sent.GetReceipt()
		}
		receiptsOf := func(messages []*harnessv1.InboxMessage) []string {
			receipts := []string{}
			for _, message := range messages {
				receipts = append(receipts, message.GetReceiptId())
			}
			return receipts
		}
		queue := harnessv1.MessagePriorityClass_MESSAGE_PRIORITY_CLASS_QUEUE
		interrupt := harnessv1.MessagePriorityClass_MESSAGE_PRIORITY_CLASS_INTERRUPT

		It("gives every message its own receipt and a rising sequence, and lists an interrupt ahead of the queued messages", func() {
			holdFirstTurn()
			first := send("message 1", queue)
			second := send("message 2", interrupt)
			third := send("message 3", queue)
			Expect([]string{first.GetReceiptId(), second.GetReceiptId(), third.GetReceiptId()}).To(HaveEach(Not(BeEmpty())))
			Expect(map[string]bool{first.GetReceiptId(): true, second.GetReceiptId(): true, third.GetReceiptId(): true}).To(HaveLen(3), "receipt identifiers are unique")
			Expect(second.GetSequence()).To(BeNumerically(">", first.GetSequence()))
			Expect(third.GetSequence()).To(BeNumerically(">", second.GetSequence()))

			listed, err := service.ListInbox(ctx, &harnessv1.ListInboxRequest{AssignmentId: firstAssignment})
			Expect(err).NotTo(HaveOccurred())
			Expect(receiptsOf(listed.GetMessages())).To(Equal([]string{second.GetReceiptId(), first.GetReceiptId(), third.GetReceiptId()}))
			Expect(listed.GetMessages()).To(HaveEach(HaveField("State", harnessv1.MessageState_MESSAGE_STATE_QUEUED)))
		})

		It("moves a message to an index and to the top without changing its receipt", func() {
			holdFirstTurn()
			receipts := []string{send("message 1", queue).GetReceiptId(), send("message 2", queue).GetReceiptId(), send("message 3", queue).GetReceiptId()}

			moved, err := service.MoveInboxMessage(ctx, &harnessv1.MoveInboxMessageRequest{AssignmentId: firstAssignment, ReceiptId: receipts[1], Index: 0})
			Expect(err).NotTo(HaveOccurred())
			Expect(receiptsOf(moved.GetMessages())).To(Equal([]string{receipts[1], receipts[0], receipts[2]}))

			topped, err := service.TopInboxMessage(ctx, &harnessv1.TopInboxMessageRequest{AssignmentId: firstAssignment, ReceiptId: receipts[2]})
			Expect(err).NotTo(HaveOccurred())
			Expect(receiptsOf(topped.GetMessages())).To(Equal([]string{receipts[2], receipts[1], receipts[0]}))
		})

		It("stamps a receipt with the service clock's instant and the queued state", func() {
			holdFirstTurn()
			receipt := send("priority message", interrupt)
			Expect(receipt.GetSentAt().AsTime()).To(BeTemporally("==", clock.Now()))
			Expect(receipt.GetState()).To(Equal(harnessv1.MessageState_MESSAGE_STATE_QUEUED))
		})

		It("gives concurrent senders distinct receipts", func() {
			holdFirstTurn()
			const senders = 10
			receipts := make(chan string, senders)
			for index := 0; index < senders; index++ {
				go func(index int) {
					defer GinkgoRecover()
					class := queue
					if index%2 == 1 {
						class = interrupt
					}
					receipts <- send("concurrent message", class).GetReceiptId()
				}(index)
			}
			seen := map[string]bool{}
			for index := 0; index < senders; index++ {
				var receipt string
				Eventually(receipts, settleBudget.Within).Should(Receive(&receipt))
				seen[receipt] = true
			}
			Expect(seen).To(HaveLen(senders))
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
			saturated.EXPECT().Pressure(gomock.Any()).Return(proc.Pressure{}, nil).AnyTimes()
			saturated.EXPECT().MemoryAvailable().Return(uint64(freeBytes), nil).AnyTimes()
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
			Eventually(restarted.Resumed(), settleBudget.Within).Should(BeClosed(), "the host is ready only once every open run is resubmitted")
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

		It("holds the resumes the launch check does not admit, records them, and resumes them in order as admission frees", func() {
			assignments := []string{firstAssignment, secondAssignment, thirdAssignment}
			var originals []openedExecutor
			for index, assignment := range assignments {
				_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(assignment, fmt.Sprintf("h2/run-%d", index), repository)})
				Expect(err).NotTo(HaveOccurred())
				original, script := opened()
				Expect(<-script.prompts).NotTo(BeEmpty())
				script.release <- struct{}{}
				awaitPhase(assignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN)
				original.executor.EXPECT().Close(gomock.Any()).Return(nil)
				originals = append(originals, original)
			}
			Expect(originals).To(HaveLen(len(assignments)))
			Expect(scope.Close()).To(Succeed())

			// 8 cores under a load of 5.5 leave room for two sessions.
			var load atomic.Pointer[float64]
			saturated := 5.5
			load.Store(&saturated)
			loaded := mocks.NewMockIHostMeasures(controller)
			loaded.EXPECT().Cores().Return(cores).AnyTimes()
			loaded.EXPECT().LoadAverage().DoAndReturn(func() (float64, error) { return *load.Load(), nil }).AnyTimes()
			loaded.EXPECT().FreeBytes(gomock.Any()).Return(uint64(freeBytes), nil).AnyTimes()
			loaded.EXPECT().DirectoryBytes(gomock.Any()).Return(uint64(1<<20), nil).AnyTimes()
			loaded.EXPECT().Pressure(gomock.Any()).Return(proc.Pressure{}, nil).AnyTimes()
			loaded.EXPECT().MemoryAvailable().Return(uint64(freeBytes), nil).AnyTimes()
			runner, err := session.NewAgentSessionRunner(session.WithLauncher(launcher), session.WithStateDirectory(state),
				session.WithGateCommand("/opt/csf/harness", "gate"), session.WithOpenTurnExecutors(factory.open))
			Expect(err).NotTo(HaveOccurred())
			restarted, err := harness.NewAgentSessionService(harness.WithSessionRunner(runner), harness.WithHostMeasures(loaded), harness.WithClock(clock))
			Expect(err).NotTo(HaveOccurred())
			restartedScope := runtime.NewScope(ctx, "restarted")
			Expect(restarted.Start(restartedScope)).To(Succeed())

			reopened := map[string]openedExecutor{}
			for range 2 {
				next, _ := opened()
				reopened[filepath.Base(filepath.Dir(next.spec.Directory))] = next
			}
			readQueue := func() harness.ResumeQueue {
				var queue harness.ResumeQueue
				content, err := os.ReadFile(filepath.Join(state, harness.ResumeQueueFile))
				if err == nil {
					_ = json.Unmarshal(content, &queue)
				}
				return queue
			}
			held := eventually.Await(GinkgoT(), "the held resume to be recorded", settleBudget, readQueue,
				func(queue harness.ResumeQueue) bool { return queue.Resumed == 2 && len(queue.Held) == 1 })
			Expect(held.Held[0].Position).To(Equal(1))
			Expect(reopened).To(HaveLen(2))
			Expect(reopened).NotTo(HaveKey(held.Held[0].AssignmentID), "the held run is the one not reopened")
			Expect(assignments).To(ContainElement(held.Held[0].AssignmentID))
			Expect(held.Held[0].Reason).To(ContainSubstring("leaves room for 2 sessions"))
			Consistently(factory.opened, 200*time.Millisecond).ShouldNot(Receive(), "admitted=false means not launched")

			idle := 1.0
			load.Store(&idle)
			clock.ticks <- frozen
			last, _ := opened()
			Expect(filepath.Base(filepath.Dir(last.spec.Directory))).To(Equal(held.Held[0].AssignmentID))
			reopened[held.Held[0].AssignmentID] = last
			eventually.Await(GinkgoT(), "the resume queue to drain", settleBudget, readQueue,
				func(queue harness.ResumeQueue) bool { return queue.Resumed == 3 && len(queue.Held) == 0 })

			for _, executor := range reopened {
				executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			}
			Expect(restartedScope.Close()).To(Succeed())
		})

		It("reopens a run whose worktree pins no session image in the fallback image, and keeps it open", func() {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "h2/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			first, firstScript := opened()
			Expect(<-firstScript.prompts).NotTo(BeEmpty())
			firstScript.release <- struct{}{}
			awaitPhase(firstAssignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN)
			first.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Expect(scope.Close()).To(Succeed())
			Expect(filepath.Join(state, firstAssignment, session.WorktreeDirectory, session.SessionImageFile)).NotTo(BeAnExistingFile(),
				"the worktree was created before the session image was pinned")

			containers := sessionmocks.NewMockISessionContainers(controller)
			started := make(chan docker.SandboxSpec, 1)
			containers.EXPECT().StartDetached(gomock.Any(), gomock.Any()).DoAndReturn(
				func(_ context.Context, spec docker.SandboxSpec) (docker.StartedContainer, error) {
					started <- spec
					return docker.StartedContainer{ID: "c0ffee"}, nil
				})
			runner, err := session.NewAgentSessionRunner(session.WithLauncher(launcher), session.WithStateDirectory(state),
				session.WithGateCommand("csf", "gate"), session.WithOpenTurnExecutors(factory.open),
				session.WithContainerSessions(containers, session.ContainerSettings{}))
			Expect(err).NotTo(HaveOccurred())
			restarted, err := harness.NewAgentSessionService(harness.WithSessionRunner(runner), harness.WithHostMeasures(measures), harness.WithClock(clock))
			Expect(err).NotTo(HaveOccurred())
			restartedScope := runtime.NewScope(ctx, "restarted")
			Expect(restarted.Start(restartedScope)).To(Succeed())

			reopened, reopenedScript := opened()
			Expect(reopened.spec.Resume).To(BeTrue())
			Expect((<-started).Image).To(Equal(session.DefaultSessionImage))
			Expect(reopenedScript.prompts).NotTo(Receive(), "the finished turn is not delivered again")
			phase := func() harnessv1.AgentSessionPhase {
				got, err := restarted.Get(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: firstAssignment})
				if err != nil {
					return harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_UNSPECIFIED
				}
				return got.GetSession().GetPhase()
			}
			isOpen := func(phase harnessv1.AgentSessionPhase) bool {
				return phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN
			}
			eventually.Await(GinkgoT(), "the reopened run to be open", settleBudget, phase, isOpen)
			eventually.Consistently(GinkgoT(), "the reopened run to stay open", heldOpenBudget, phase, isOpen)

			reopened.executor.EXPECT().Close(gomock.Any()).Return(nil)
			containers.EXPECT().ContainerUsage(gomock.Any()).Return(sandbox.Usage{}, nil)
			containers.EXPECT().RemoveContainer(gomock.Any(), "c0ffee").Return(nil)
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
			for range 4 {
				line, err := tail.Next(ctx)
				Expect(err).NotTo(HaveOccurred())
				var record struct {
					EventType string `json:"event_type"`
				}
				Expect(jsonUnmarshal(line, &record)).To(Succeed())
				types = append(types, record.EventType)
			}
			Expect(types).To(Equal([]string{session.EventTypeRunStarted, session.EventTypeWorktreeReady, session.EventTypeControlAction, session.EventTypeTurnRequested}))
			Expect(tail.Sequence()).To(Equal(4))

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
			Expect(rest).To(ContainElements(session.EventTypeControlAction, session.EventTypeRunFinished, session.EventTypeSessionClosed))

			_, err = service.OpenTail(ctx, unknownAssignment, 0)
			Expect(err).To(MatchError(harness.ErrUnknownSession))
			_, err = service.OpenTail(ctx, "nope", 0)
			Expect(err).To(MatchError(harness.ErrUnknownSession))
		})
	})

	Describe("the control plane", func() {
		// controlRecord is the part of a control action record the specs read.
		type controlRecord struct {
			Level       string `json:"level"`
			EventType   string `json:"event_type"`
			Action      string `json:"action"`
			Operator    bool   `json:"operator_authored"`
			Wanted      bool   `json:"question_wanted"`
			TurnID      string `json:"turn_id"`
			PullRequest string `json:"pull_request_url"`
			Error       string `json:"error"`
		}
		controlRecords := func(assignment string) []controlRecord {
			content, err := os.ReadFile(filepath.Join(state, assignment, session.EventsFile))
			Expect(err).NotTo(HaveOccurred())
			var records []controlRecord
			for _, line := range bytes.Split(content, []byte("\n")) {
				var record controlRecord
				if jsonUnmarshal(line, &record) == nil && record.EventType == session.EventTypeControlAction {
					records = append(records, record)
				}
			}
			return records
		}
		// openWithPullRequest submits a session and finishes its first turn,
		// whose receipt carries the pull request gh lists for its branch.
		openWithPullRequest := func(assignment string) openedExecutor {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(assignment, "wb/"+assignment[:8], repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, script := opened()
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Eventually(script.prompts, settleBudget.Within).Should(Receive())
			script.release <- struct{}{}
			awaitPhase(assignment, harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN)
			return executor
		}

		It("checks a recipe's admission without admitting it, and says why admission is held", func() {
			checked, err := service.CheckAdmission(ctx, &harnessv1.CheckAgentSessionAdmissionRequest{Recipe: newRecipe(firstAssignment, "wb/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			Expect(checked.GetCheck().GetWorkerCap()).To(BeNumerically("==", cores-2))
			Expect(checked.GetCheck().GetReportOnly()).To(BeTrue())
			Expect(checked.GetHeld()).To(BeEmpty())
			listed, err := service.List(ctx, &harnessv1.ListAgentSessionsRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(listed.GetSessions()).To(BeEmpty(), "a check admits nothing")

			service.HoldAdmission("free disk is below the housekeeping floor")
			checked, err = service.CheckAdmission(ctx, &harnessv1.CheckAgentSessionAdmissionRequest{Recipe: newRecipe(firstAssignment, "wb/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			Expect(checked.GetHeld()).To(Equal("free disk is below the housekeeping floor"))

			_, err = service.CheckAdmission(ctx, &harnessv1.CheckAgentSessionAdmissionRequest{})
			Expect(err).To(MatchError(csf.ErrInvalidRequest))
			_, err = service.CheckAdmission(ctx, &harnessv1.CheckAgentSessionAdmissionRequest{Recipe: &pb.AgentAssignmentRecipe{AssignmentId: "nope"}})
			Expect(err).To(MatchError(csf.ErrInvalidRequest))
		})

		It("records a ruling for every session's later turns and returns the rulings in force", func() {
			recorded, err := service.RecordRuling(ctx, &harnessv1.RecordRulingRequest{Ruling: &harnessv1.Ruling{
				RulingId: "csf-home", Statement: "CSF is its one home.", Excludes: []string{"foundation Postgres"},
				Quote: "CSF is everything", RuledOn: "2026-10-05",
			}})
			Expect(err).NotTo(HaveOccurred())
			replaced, err := service.RecordRuling(ctx, &harnessv1.RecordRulingRequest{Ruling: &harnessv1.Ruling{
				RulingId: "csf-independent", Statement: "CSF is independent of the platform.", Excludes: []string{"foundation Postgres", "platform data plane"}, Supersedes: "csf-home",
				Quote: "CSF is independent", RuledOn: "2026-10-05",
			}})
			Expect(err).NotTo(HaveOccurred())
			Expect(recorded.GetInForce()).To(HaveLen(1))
			Expect(replaced.GetInForce()).To(HaveExactElements(And(
				HaveField("RulingId", "csf-independent"), HaveField("Supersedes", "csf-home"),
				HaveField("Excludes", ConsistOf("foundation Postgres", "platform data plane")))))
			inForce, err := session.RulingsInForce(state)
			Expect(err).NotTo(HaveOccurred())
			Expect(inForce).To(HaveExactElements(HaveField("RecordedAt", BeTemporally("==", clock.Now()))))
		})

		It("refuses a ruling that is missing, excludes nothing, excludes a blank or carries a malformed identifier", func() {
			for _, request := range []*harnessv1.RecordRulingRequest{
				{},
				{Ruling: &harnessv1.Ruling{RulingId: "r1", Statement: "One home."}},
				{Ruling: &harnessv1.Ruling{RulingId: "r1", Statement: "One home.", Excludes: []string{" "}}},
				{Ruling: &harnessv1.Ruling{RulingId: "../r1", Statement: "One home.", Excludes: []string{"elsewhere"}}},
			} {
				_, err := service.RecordRuling(ctx, request)
				Expect(err).To(MatchError(csf.ErrInvalidRequest))
			}
			Expect(session.RulingsInForce(state)).To(BeEmpty())
		})

		It("records every control action in the session's event log as a typed record", func() {
			openWithPullRequest(firstAssignment)
			sent, err := service.Send(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: firstAssignment, Message: "Rebase on main.", OperatorAuthored: true, QuestionWanted: true})
			Expect(err).NotTo(HaveOccurred())
			_, err = service.Ready(ctx, &harnessv1.ReadyAgentSessionPullRequestRequest{AssignmentId: firstAssignment})
			Expect(err).NotTo(HaveOccurred())
			merged, err := service.Merge(ctx, &harnessv1.MergeAgentSessionPullRequestRequest{AssignmentId: firstAssignment})
			Expect(err).NotTo(HaveOccurred())
			Expect(merged.GetSession().GetPullRequestUrl()).To(Equal(pullRequestURL))
			_, err = service.Cancel(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: firstAssignment})
			Expect(err).NotTo(HaveOccurred())

			records := controlRecords(firstAssignment)
			actions := make([]string, 0, len(records))
			for _, record := range records {
				actions = append(actions, record.Action)
				Expect(record.Level).To(Equal("INFO"))
			}
			Expect(actions).To(Equal([]string{session.ActionSubmit, session.ActionSend, session.ActionReady, session.ActionMergeStarted, session.ActionMerge, session.ActionCancel}))
			Expect(records[1].Operator).To(BeTrue())
			Expect(records[1].Wanted).To(BeTrue(), "the operator overrode the question gate")
			Expect(records[1].TurnID).To(Equal(sent.GetTurnId()))
			Expect(records[2].PullRequest).To(Equal(pullRequestURL))
			Expect(records[3].PullRequest).To(Equal(pullRequestURL))
			Expect(records[4].PullRequest).To(Equal(pullRequestURL))

			var calls [][]string
			for len(ghCalls) > 0 {
				calls = append(calls, <-ghCalls)
			}
			Expect(calls).To(ContainElement([]string{"pr", "ready", pullRequestURL}))
			Expect(calls).To(ContainElement([]string{"pr", "merge", pullRequestURL, "--squash"}), "a worktree without the merge script is merged by gh")
		})

		It("merges through the worktree's merge path, and records its refusal with the report", func() {
			openWithPullRequest(firstAssignment)
			worktree := filepath.Join(state, firstAssignment, session.WorktreeDirectory)
			Expect(os.MkdirAll(filepath.Join(worktree, "tools"), 0o700)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(worktree, harness.MergeScript), []byte("#!/bin/sh\n"), 0o700)).To(Succeed())
			launcher.EXPECT().Run(gomock.Any(), launched("bash", harness.MergeScript, "7")).DoAndReturn(func(_ context.Context, command proc.Command) (proc.Result, error) {
				Expect(command.Directory).To(Equal(worktree))
				Expect(command.ExtraEnvironment).To(ContainElement("CANDACE_BAZEL_CACHE=" + filepath.Join(state, harness.MergeBazelDirectory)))
				return proc.Result{Stdout: []byte("merge-pr: REFUSED: the ontology penalty rose\n"), ExitCode: 1}, errors.New("exit status 1")
			})

			_, err := service.Merge(ctx, &harnessv1.MergeAgentSessionPullRequestRequest{AssignmentId: firstAssignment})
			Expect(err).To(MatchError(ContainSubstring("REFUSED: the ontology penalty rose")))
			records := controlRecords(firstAssignment)
			Expect(records).To(HaveLen(3))
			Expect(records[1].Action).To(Equal(session.ActionMergeStarted))
			Expect(records[2].Action).To(Equal(session.ActionMerge))
			Expect(records[2].Level).To(Equal("ERROR"))
			Expect(records[2].Error).To(Equal("exit status 1"))
		})

		It("keeps merging when the caller stops waiting, and records the outcome for whoever reads the log next", func() {
			openWithPullRequest(firstAssignment)
			worktree := filepath.Join(state, firstAssignment, session.WorktreeDirectory)
			Expect(os.MkdirAll(filepath.Join(worktree, "tools"), 0o700)).To(Succeed())
			Expect(os.WriteFile(filepath.Join(worktree, harness.MergeScript), []byte("#!/bin/sh\n"), 0o700)).To(Succeed())
			started, release := make(chan struct{}), make(chan struct{})
			launcher.EXPECT().Run(gomock.Any(), launched("bash", harness.MergeScript, "7")).DoAndReturn(func(runCtx context.Context, _ proc.Command) (proc.Result, error) {
				close(started)
				<-release
				Expect(runCtx.Err()).NotTo(HaveOccurred(), "the merge runs on the service's scope, not the caller's")
				return proc.Result{}, nil
			})
			caller, stop := context.WithCancel(ctx)
			returned := make(chan error, 1)
			go func() {
				_, err := service.Merge(caller, &harnessv1.MergeAgentSessionPullRequestRequest{AssignmentId: firstAssignment})
				returned <- err
			}()
			Eventually(started).Should(BeClosed())
			stop()
			Eventually(returned).Should(Receive(MatchError(ContainSubstring("continues on the host"))))
			close(release)
			Eventually(func() []string {
				var actions []string
				for _, record := range controlRecords(firstAssignment) {
					actions = append(actions, record.Action)
				}
				return actions
			}).Should(Equal([]string{session.ActionSubmit, session.ActionMergeStarted, session.ActionMerge}))
		})

		It("serves list, send and cancel to an MCP client, and the same typed records land as from the page", func() {
			openWithPullRequest(firstAssignment)
			api, err := csf.New(csf.WithAgentSessions(service))
			Expect(err).NotTo(HaveOccurred())
			client := mcp.NewClient(&mcp.Implementation{Name: "workbench-spec", Version: "0"}, nil)
			connection, err := client.Connect(ctx, &mcp.StreamableClientTransport{
				Endpoint:   "http://csf.invalid/mcp",
				HTTPClient: &http.Client{Transport: handlerTransport{handler: api.MCPHandler()}},
			}, nil)
			Expect(err).NotTo(HaveOccurred())
			defer func() { Expect(connection.Close()).To(Succeed()) }()

			tools, err := connection.ListTools(ctx, nil)
			Expect(err).NotTo(HaveOccurred())
			var names []string
			for _, tool := range tools.Tools {
				names = append(names, tool.Name)
			}
			Expect(names).To(ContainElements("ListAgentSessions", "GetAgentSession", "SubmitAgentSession", "SendAgentSessionMessage", "CancelAgentSession",
				"CheckAgentSessionAdmission", "ReadyAgentSessionPullRequest", "MergeAgentSessionPullRequest"))

			call := func(name string, arguments map[string]any) string {
				result, err := connection.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: arguments})
				Expect(err).NotTo(HaveOccurred())
				Expect(result.Content).To(HaveLen(1))
				text := result.Content[0].(*mcp.TextContent).Text
				Expect(result.IsError).To(BeFalse(), text)
				return text
			}
			Expect(call("ListAgentSessions", map[string]any{})).To(ContainSubstring(firstAssignment))
			Expect(call("SendAgentSessionMessage", map[string]any{"assignmentId": firstAssignment, "message": "Rebase on main.", "operatorAuthored": true})).To(ContainSubstring(`"turnId":"2"`))
			Expect(call("CancelAgentSession", map[string]any{"assignmentId": firstAssignment})).To(ContainSubstring("AGENT_SESSION_PHASE_CANCEL"))
			refused, err := connection.CallTool(ctx, &mcp.CallToolParams{Name: "CancelAgentSession", Arguments: map[string]any{"assignmentId": "nope"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(refused.IsError).To(BeTrue(), "a malformed assignment is the operation's refusal, not a transport failure")

			records := controlRecords(firstAssignment)
			Expect(records).To(HaveLen(3))
			Expect(records[1].Action).To(Equal(session.ActionSend))
			Expect(records[1].Operator).To(BeTrue())
			Expect(records[1].TurnID).To(Equal("2"))
			Expect(records[2].Action).To(Equal(session.ActionCancel))
		})

		It("refuses ready and merge with no pull request, for an unknown session, and on a host that granted no launcher", func() {
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "wb/first", repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, script := opened()
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Eventually(script.prompts, settleBudget.Within).Should(Receive())
			_, err = service.Ready(ctx, &harnessv1.ReadyAgentSessionPullRequestRequest{AssignmentId: firstAssignment})
			Expect(err).To(MatchError(harness.ErrNoPullRequest), "the first turn has not opened one yet")
			Expect(err).To(MatchError(csf.ErrConflict))
			_, err = service.Merge(ctx, &harnessv1.MergeAgentSessionPullRequestRequest{AssignmentId: unknownAssignment})
			Expect(err).To(MatchError(harness.ErrUnknownSession))
			_, err = service.Merge(ctx, &harnessv1.MergeAgentSessionPullRequestRequest{AssignmentId: "nope"})
			Expect(err).To(MatchError(csf.ErrInvalidRequest))
			script.release <- struct{}{}

			runner, err := session.NewAgentSessionRunner(session.WithLauncher(launcher), session.WithStateDirectory(state), session.WithGateCommand("/opt/csf/harness", "gate"))
			Expect(err).NotTo(HaveOccurred())
			bare, err := harness.NewAgentSessionService(harness.WithSessionRunner(runner))
			Expect(err).NotTo(HaveOccurred())
			_, err = bare.Ready(ctx, &harnessv1.ReadyAgentSessionPullRequestRequest{AssignmentId: firstAssignment})
			Expect(err).To(MatchError(harness.ErrNoLauncher))
			_, err = harness.NewAgentSessionService(harness.WithSessionRunner(runner), harness.WithLauncher(nil))
			Expect(err).To(MatchError(harness.ErrInvalidServiceOption))
		})
	})

	Describe("the default executor", func() {
		It("runs a recipe naming no executor on the default the operator switched to, and records the switch", func() {
			current, err := service.GetExecutorDefault(ctx, &harnessv1.GetAgentExecutorDefaultRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(current.GetExecutorDefault().GetExecutor()).To(Equal("claude-code"))
			Expect(current.GetExecutorDefault().GetModel()).To(BeEmpty(), "each recipe keeps its own model")

			switched, err := service.SetExecutorDefault(ctx, &harnessv1.SetAgentExecutorDefaultRequest{
				ExecutorDefault: &harnessv1.AgentExecutorDefault{Executor: "copilot", Model: "claude-haiku-4.5"}})
			Expect(err).NotTo(HaveOccurred())
			Expect(switched.GetPrevious().GetExecutor()).To(Equal("claude-code"))
			Expect(filepath.Join(state, harness.ExecutorDefaultFile)).To(BeARegularFile(), "a restarted host keeps the switch")

			submitted, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "x/default", repository)})
			Expect(err).NotTo(HaveOccurred())
			executor, script := opened()
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Eventually(script.prompts, settleBudget.Within).Should(Receive())
			Expect(executor.spec.Executor).To(Equal(session.ExecutorCopilot))
			Expect(submitted.GetReceipt().GetExecutor()).To(Equal("copilot"))
			Expect(submitted.GetReceipt().GetPlan().GetRecipe().GetModel()).To(Equal("claude-haiku-4.5"))

			explicit := newRecipe(secondAssignment, "x/explicit", repository)
			explicit.Executor = "claude-code"
			_, err = service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: explicit})
			Expect(err).NotTo(HaveOccurred())
			second, secondScript := opened()
			second.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Eventually(secondScript.prompts, settleBudget.Within).Should(Receive())
			Expect(second.spec.Executor).To(Equal(session.ExecutorClaudeCode), "a recipe naming its executor runs as written")
			script.release <- struct{}{}
			secondScript.release <- struct{}{}
		})

		It("refuses a default that moves recipes off Claude Code without naming the model, and keeps the old one", func() {
			_, err := service.SetExecutorDefault(ctx, &harnessv1.SetAgentExecutorDefaultRequest{
				ExecutorDefault: &harnessv1.AgentExecutorDefault{Executor: "copilot"}})
			Expect(err).To(MatchError(harness.ErrNoDefaultModel))
			current, err := service.GetExecutorDefault(ctx, &harnessv1.GetAgentExecutorDefaultRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(current.GetExecutorDefault().GetExecutor()).To(Equal("claude-code"))
			_, err = service.SetExecutorDefault(ctx, &harnessv1.SetAgentExecutorDefaultRequest{
				ExecutorDefault: &harnessv1.AgentExecutorDefault{Executor: "opencode", Model: "x"}})
			Expect(err).To(MatchError(csf.ErrInvalidRequest))
		})
	})

	Describe("the model policy", func() {
		fable := func(assignment string) *pb.AgentAssignmentRecipe {
			recipe := newRecipe(assignment, "x/fable", repository)
			recipe.Model = "claude-fable-5-1"
			return recipe
		}
		refusal := `the model is not allowed on this host: "claude-fable-5-1"; allowed: claude-opus-5-5 (operator, 2026-10-05: "every real session runs claude-opus-5-5. Never Fable.")`

		It("refuses a recipe on a model the ruling does not allow, naming the allowed model and the ruling, and admits the allowed one", func() {
			Expect(harness.RecordModelPolicy(state, harness.DefaultModelPolicy)).To(Succeed())

			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: fable(firstAssignment)})
			Expect(err).To(MatchError(harness.ErrModelNotAllowed))
			Expect(err).To(MatchError(csf.ErrInvalidRequest))
			Expect(err.Error()).To(ContainSubstring(refusal))
			Expect(filepath.Join(state, firstAssignment)).NotTo(BeADirectory(), "nothing is created for a refused recipe")
			_, err = service.CheckAdmission(ctx, &harnessv1.CheckAgentSessionAdmissionRequest{Recipe: fable(firstAssignment)})
			Expect(err).To(MatchError(harness.ErrModelNotAllowed))

			opus := newRecipe(secondAssignment, "x/opus", repository)
			opus.Model = "claude-opus-5-5"
			_, err = service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: opus})
			Expect(err).NotTo(HaveOccurred())
			executor, script := opened()
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			Eventually(script.prompts, settleBudget.Within).Should(Receive())
			script.release <- struct{}{}
		})

		It("never resumes a recorded run on a model the policy no longer allows", func() {
			Expect(harness.RecordModelPolicy(state, harness.DefaultModelPolicy)).To(Succeed())
			directory := filepath.Join(state, firstAssignment)
			Expect(os.MkdirAll(directory, 0o700)).To(Succeed())
			Expect(session.WriteRunState(directory, &session.RunState{AssignmentID: firstAssignment, Model: "claude-fable-5-1"})).To(Succeed())

			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: fable(firstAssignment)})

			Expect(err).To(MatchError(harness.ErrModelNotAllowed))
			Expect(err.Error()).To(ContainSubstring(refusal))
		})

		It("refuses switching the default to a model the policy does not allow", func() {
			Expect(harness.RecordModelPolicy(state, harness.DefaultModelPolicy)).To(Succeed())

			_, err := service.SetExecutorDefault(ctx, &harnessv1.SetAgentExecutorDefaultRequest{
				ExecutorDefault: &harnessv1.AgentExecutorDefault{Executor: "claude-code", Model: "claude-fable-5-1"}})

			Expect(err).To(MatchError(harness.ErrModelNotAllowed))
			current, err := service.GetExecutorDefault(ctx, &harnessv1.GetAgentExecutorDefaultRequest{})
			Expect(err).NotTo(HaveOccurred())
			Expect(current.GetExecutorDefault().GetModel()).To(BeEmpty(), "the default is unchanged")
		})

		It("refuses everything, with a typed reason, under a policy that allows nothing or cannot be read", func() {
			Expect(harness.RecordModelPolicy(state, harness.ModelPolicy{})).To(Succeed())
			_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "x/none", repository)})
			Expect(err).To(MatchError(harness.ErrNoAllowedModels))

			Expect(os.WriteFile(filepath.Join(state, harness.AllowedModelsFile), []byte("not json"), 0o600)).To(Succeed())
			_, err = service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "x/none", repository)})
			Expect(err).To(MatchError(harness.ErrModelPolicy), "an unreadable policy refuses rather than fails open")
		})

		It("seeds the operator's ruling into a state directory that records no policy", func() {
			fresh := GinkgoT().TempDir()
			runner, err := session.NewAgentSessionRunner(session.WithLauncher(launcher), session.WithStateDirectory(fresh), session.WithGateCommand("/opt/csf/harness", "gate"))
			Expect(err).NotTo(HaveOccurred())
			seeded, err := harness.NewAgentSessionService(harness.WithSessionRunner(runner), harness.WithHostMeasures(measures))
			Expect(err).NotTo(HaveOccurred())
			seededScope := runtime.NewScope(ctx, "seeded")
			Expect(seeded.Start(seededScope)).To(Succeed())

			content, err := os.ReadFile(filepath.Join(fresh, harness.AllowedModelsFile))
			Expect(err).NotTo(HaveOccurred())
			Expect(string(content)).To(And(ContainSubstring(`"model": "claude-opus-5-5"`), ContainSubstring("Never Fable.")))
			Expect(seededScope.Close()).To(Succeed())
		})
	})

	Describe("RecordRuling", func() {
		opusOnly := func() *harnessv1.Ruling {
			return &harnessv1.Ruling{
				RulingId: "opus-only", Statement: "Every real session runs claude-opus-5-5. Never Fable.", Excludes: []string{"fable"},
				Quote: "DON'T USE FABLE JUST USE OPUS", RuledOn: "2026-10-05", Scope: "every real session",
				Why: "two recipes resumed on Fable", EnforcedBy: "allowed-models refusal at submit (COPILOT-PARITY #343)",
			}
		}

		It("records the operator's words, the day, the scope, the reason and the gate, and returns them in force", func() {
			pending := &harnessv1.Ruling{RulingId: "no-native-tools", Statement: "No executor-native tools.", Quote: "dont ever use your native tools again",
				RuledOn: "2026-10-05", PendingGate: "PreToolUse native-tool refusal (YOU-DO #424)"}
			_, err := service.RecordRuling(ctx, &harnessv1.RecordRulingRequest{Ruling: opusOnly()})
			Expect(err).NotTo(HaveOccurred())
			response, err := service.RecordRuling(ctx, &harnessv1.RecordRulingRequest{Ruling: pending})
			Expect(err).NotTo(HaveOccurred())

			Expect(response.GetInForce()).To(HaveLen(2))
			Expect(response.GetInForce()[0].GetQuote()).To(Equal("DON'T USE FABLE JUST USE OPUS"))
			Expect(response.GetInForce()[0].GetEnforcedBy()).To(Equal("allowed-models refusal at submit (COPILOT-PARITY #343)"))
			Expect(response.GetInForce()[1].GetPendingGate()).To(Equal("PreToolUse native-tool refusal (YOU-DO #424)"))
			Expect(response.GetInForce()[1].GetExcludes()).To(BeEmpty(), "a ruling need not exclude an alternative")
			inForce, err := session.RulingsInForce(state)
			Expect(err).NotTo(HaveOccurred())
			Expect(inForce[0].RecordedAt).To(Equal(frozen))
			Expect(session.CoverageOf(inForce)).To(Equal(session.RulingCoverage{Enforced: 1, Total: 2}))
		})

		It("refuses a ruling without the operator's words, without a day, with a malformed day or with a blank excluded alternative", func() {
			for _, mutate := range []func(ruling *harnessv1.Ruling){
				func(ruling *harnessv1.Ruling) { ruling.Quote = "" },
				func(ruling *harnessv1.Ruling) { ruling.RuledOn = "" },
				func(ruling *harnessv1.Ruling) { ruling.RuledOn = "Oct 5" },
				func(ruling *harnessv1.Ruling) { ruling.Excludes = []string{"fable", " "} },
			} {
				ruling := opusOnly()
				mutate(ruling)
				_, err := service.RecordRuling(ctx, &harnessv1.RecordRulingRequest{Ruling: ruling})
				Expect(err).To(MatchError(csf.ErrInvalidRequest))
			}
			Expect(filepath.Join(state, session.RulingsFile)).NotTo(BeAnExistingFile(), "nothing is recorded for a refused ruling")
		})
	})
})
