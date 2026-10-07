// Copyright 2026 Candace Labs

package harness_test

import (
	"bytes"
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing/fstest"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/eventually"
	harnessv1 "github.com/candacelabs/csf/proto/candace/harness/v1"
	"github.com/candacelabs/csf/runtime"
	"github.com/candacelabs/csf/services/harness"
	"github.com/candacelabs/csf/services/harness/mocks"
	"github.com/candacelabs/csf/services/harness/session"
)

// The seeded run records: three gaps of 30 s, 60 s and an hour, whose knee
// (probed with the chord method) is the minute at quantile 2/3. The process
// table shows the harness, the session's executor working in its worktree
// and, when the spec wants one, a background child in the executor's group.
const (
	seedAssignment  = "2e3f4051-6172-4839-9a0b-c1d2e3f40516"
	seedLog         = `{"time":"2026-10-04T10:00:00Z","event_type":"harness_run_finished"}` + "\n" + `{"time":"2026-10-04T10:00:30Z","event_type":"harness_turn_requested"}` + "\n" + `{"time":"2026-10-04T10:10:00Z","event_type":"harness_run_finished"}` + "\n" + `{"time":"2026-10-04T10:11:00Z","event_type":"harness_turn_requested"}` + "\n" + `{"time":"2026-10-04T11:00:00Z","event_type":"harness_run_finished"}` + "\n" + `{"time":"2026-10-04T12:00:00Z","event_type":"harness_turn_requested"}` + "\n"
	executorPID     = "5000"
	backgroundPID   = "5001"
	harnessStatLine = "4242 (csf) S 1 4242 4242 0 -1 4194304 0 0 0 0 0 0 0 0 20 0 1 0 0 0 100 0\n"
	executorStat    = "5000 (claude) S 4242 5000 4242 0 -1 4194304 0 0 0 0 0 0 0 0 20 0 1 0 0 0 1000 0\n"
	backgroundStat  = "5001 (bash) S 5000 5000 4242 0 -1 4194304 0 0 0 0 0 0 0 0 20 0 1 0 0 0 10 0\n"
)

// processTable is /proc as the spec shows it to the service.
func processTable(worktree string, withExecutor bool, withChild bool) fstest.MapFS {
	table := fstest.MapFS{"4242/stat": &fstest.MapFile{Data: []byte(harnessStatLine)}}
	if withExecutor {
		table[executorPID+"/stat"] = &fstest.MapFile{Data: []byte(executorStat)}
		table[executorPID+"/cwd"] = &fstest.MapFile{Mode: fs.ModeSymlink, Data: []byte(worktree)}
	}
	if withChild {
		table[backgroundPID+"/stat"] = &fstest.MapFile{Data: []byte(backgroundStat)}
	}
	return table
}

// logRecords decodes the run's event log.
func logRecords(state string, assignment string) []map[string]any {
	GinkgoHelper()
	content, err := os.ReadFile(filepath.Join(state, assignment, session.EventsFile))
	Expect(err).NotTo(HaveOccurred())
	var records []map[string]any
	for _, line := range bytes.Split(content, []byte("\n")) {
		record := map[string]any{}
		if jsonUnmarshal(line, &record) == nil {
			records = append(records, record)
		}
	}
	return records
}

func logEventTypes(records []map[string]any) []string {
	types := make([]string, 0, len(records))
	for _, record := range records {
		kind, _ := record[session.KeyEventType].(string)
		types = append(types, kind)
	}
	return types
}

var _ = Describe("idle close", func() {
	var (
		controller *gomock.Controller
		launcher   *MockILauncher
		measures   *mocks.MockIHostMeasures
		clock      *fakeClock
		factory    *executors
		state      string
		repository string
		worktree   string
		baseline   goleak.Option
		ctx        context.Context
	)

	// start builds the service over state with the given process table and
	// starts it on a scope; stop closes that scope and proves every goroutine
	// the service started joined.
	start := func(table fstest.MapFS) (*harness.AgentSessionService, func()) {
		GinkgoHelper()
		runner, err := session.NewAgentSessionRunner(
			session.WithLauncher(launcher),
			session.WithStateDirectory(state),
			session.WithGateCommand("/opt/csf/harness", "gate"),
			session.WithOpenTurnExecutors(factory.open),
		)
		Expect(err).NotTo(HaveOccurred())
		service, err := harness.NewAgentSessionService(
			harness.WithSessionRunner(runner),
			harness.WithHostMeasures(measures),
			harness.WithClock(clock),
			harness.WithHostPID(hostPID),
			harness.WithProcessTable(table),
			harness.WithResidentSampleInterval(time.Minute),
		)
		Expect(err).NotTo(HaveOccurred())
		scope := runtime.NewScope(ctx, "idle")
		Expect(service.Start(scope)).To(Succeed())
		return service, func() {
			GinkgoHelper()
			Expect(scope.Close()).To(Succeed())
			Expect(goleak.Find(baseline)).To(Succeed(), "every goroutine the service started must join")
		}
	}

	sessionOf := func(service *harness.AgentSessionService) *harnessv1.AgentSessionState {
		response, err := service.Get(ctx, &harnessv1.GetAgentSessionRequest{AssignmentId: firstAssignment})
		if err != nil {
			return nil
		}
		return response.GetSession()
	}

	// openFirstTurn submits the session and finishes its first turn, so the
	// owner is between turns with the idle bound armed.
	openFirstTurn := func(service *harness.AgentSessionService) openedExecutor {
		GinkgoHelper()
		_, err := service.Submit(ctx, &harnessv1.SubmitAgentSessionRequest{Recipe: newRecipe(firstAssignment, "light/first", repository)})
		Expect(err).NotTo(HaveOccurred())
		var executor openedExecutor
		Eventually(factory.opened, settleBudget.Within).Should(Receive(&executor))
		Eventually(executor.script.prompts, settleBudget.Within).Should(Receive(ContainSubstring("Add one line")))
		executor.script.release <- struct{}{}
		eventually.Await(GinkgoT(), "the first turn to end", settleBudget,
			func() harnessv1.AgentSessionPhase { return sessionOf(service).GetPhase() },
			func(phase harnessv1.AgentSessionPhase) bool {
				return phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN
			})
		return executor
	}

	latestSample := func() harness.ResidentSample {
		content, err := os.ReadFile(filepath.Join(state, harness.ResidentFile))
		if err != nil {
			return harness.ResidentSample{}
		}
		samples := harness.ReadResidentSeries(content)
		if len(samples) == 0 {
			return harness.ResidentSample{}
		}
		return samples[len(samples)-1]
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
		allowModels(state, "sonnet")
		repository = GinkgoT().TempDir()
		worktree = filepath.Join(state, firstAssignment, session.WorktreeDirectory)
		launcher.EXPECT().Run(gomock.Any(), launched("git")).Return(proc.Result{}, nil).AnyTimes()
		launcher.EXPECT().Run(gomock.Any(), launched("gh")).Return(proc.Result{}, nil).AnyTimes()
		measures.EXPECT().Cores().Return(cores).AnyTimes()
		measures.EXPECT().LoadAverage().Return(1.5, nil).AnyTimes()
		measures.EXPECT().FreeBytes(gomock.Any()).Return(uint64(freeBytes), nil).AnyTimes()
		measures.EXPECT().DirectoryBytes(gomock.Any()).Return(uint64(1<<20), nil).AnyTimes()
		measures.EXPECT().Pressure(gomock.Any()).Return(proc.Pressure{}, nil).AnyTimes()
		measures.EXPECT().MemoryAvailable().Return(uint64(freeBytes), nil).AnyTimes()
		Expect(os.MkdirAll(filepath.Join(state, seedAssignment), 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(state, seedAssignment, session.EventsFile), []byte(seedLog), 0o600)).To(Succeed())
	})

	It("derives the bound from the recorded gaps, suspends the idle session, keeps it open and resumes it for the next message", func() {
		service, stop := start(processTable(worktree, true, false))
		DeferCleanup(stop)
		bound := service.IdleBound()
		Expect(bound.Bound).To(Equal(time.Minute), "the knee of 30 s, 60 s and an hour")
		Expect(bound.Quantile).To(BeNumerically("~", 2.0/3, 1e-9))
		Expect(bound.Gaps).To(Equal(3))
		Expect(bound.Fallback).To(BeFalse())
		eventually.Await(GinkgoT(), "the first sample, written at start with the derivation", settleBudget, latestSample,
			func(sample harness.ResidentSample) bool { return sample.Gaps == 3 && sample.IdleBoundSeconds == 60 })

		executor := openFirstTurn(service)
		var armed armedTimer
		Eventually(clock.timers, settleBudget.Within).Should(Receive(&armed))
		Expect(armed.after).To(Equal(time.Minute), "the idle bound is armed between turns")
		closed := make(chan struct{})
		executor.executor.EXPECT().Close(gomock.Any()).DoAndReturn(func(_ context.Context) error { close(closed); return nil })
		armed.fire()
		Eventually(closed, settleBudget.Within).Should(BeClosed(), "the executor process is closed")
		eventually.Await(GinkgoT(), "the session to report itself suspended", settleBudget,
			func() *harnessv1.AgentSessionState { return sessionOf(service) },
			func(current *harnessv1.AgentSessionState) bool { return current.GetSuspended() })
		Expect(sessionOf(service).GetPhase()).To(Equal(harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN), "the session stays open")
		Expect(clock.timers).To(BeEmpty(), "no bound is armed while the executor is closed")

		clock.ticks <- frozen
		eventually.Await(GinkgoT(), "the resident series to count no executor alive", settleBudget, latestSample,
			func(sample harness.ResidentSample) bool {
				return sample.OpenSessions == 1 && sample.ExecutorsAlive == 0
			})
		Expect(latestSample().HarnessRSSBytes).To(Equal(uint64(100 * os.Getpagesize())))
		Expect(latestSample().ExecutorsRSSBytes).To(Equal(uint64(1000*os.Getpagesize())), "the table still lists the executor the spec drew")

		_, err := service.Send(ctx, &harnessv1.SendAgentSessionMessageRequest{AssignmentId: firstAssignment, Message: "Now address the review."})
		Expect(err).NotTo(HaveOccurred())
		var resumedExecutor openedExecutor
		Eventually(factory.opened, settleBudget.Within).Should(Receive(&resumedExecutor), "a new executor is opened for the message")
		Expect(resumedExecutor.spec.Resume).To(BeTrue(), "on the recorded conversation")
		Expect(resumedExecutor.spec.Session).To(Equal(executor.spec.Session))
		Expect(resumedExecutor.spec.Directory).To(Equal(executor.spec.Directory))
		Eventually(resumedExecutor.script.prompts, settleBudget.Within).Should(Receive(Equal("Now address the review.")))
		Expect(sessionOf(service).GetSuspended()).To(BeFalse())
		resumedExecutor.script.release <- struct{}{}
		eventually.Await(GinkgoT(), "the resumed turn to end", settleBudget,
			func() uint32 { return sessionOf(service).GetTurns() }, func(turns uint32) bool { return turns == 2 })
		eventually.Await(GinkgoT(), "the session to be between turns again", settleBudget,
			func() harnessv1.AgentSessionPhase { return sessionOf(service).GetPhase() },
			func(phase harnessv1.AgentSessionPhase) bool {
				return phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_OPEN
			})
		Eventually(clock.timers, settleBudget.Within).Should(Receive(&armed), "the bound is armed again after the turn")

		records := logRecords(state, firstAssignment)
		types := logEventTypes(records)
		suspendedAt := slices.Index(types, session.EventTypeSessionSuspended)
		resumedAt := slices.Index(types, session.EventTypeSessionResumed)
		turnResumed := slices.Index(types, session.EventTypeTurnResumed)
		Expect(suspendedAt).To(BeNumerically(">", 0))
		Expect(resumedAt).To(BeNumerically(">", suspendedAt))
		Expect(turnResumed).To(BeNumerically(">", resumedAt))
		Expect(records[suspendedAt]).To(HaveKeyWithValue(session.KeyIdleSeconds, BeNumerically("==", 60)))
		Expect(records[turnResumed]).To(HaveKey(session.KeyTimeToFirstToken))
		Expect(types).NotTo(ContainElement(session.EventTypeSessionClosed), "a suspend is not a close of the conversation")

		clock.ticks <- frozen
		eventually.Await(GinkgoT(), "the resident series to count the resume", settleBudget, latestSample,
			func(sample harness.ResidentSample) bool { return sample.Resumes == 1 && sample.ExecutorsAlive == 1 })
		resumedExecutor.executor.EXPECT().Close(gomock.Any()).Return(nil)
	})

	It("keeps the executor open while a background child of its lives, and when the table does not show it", func() {
		for _, table := range []fstest.MapFS{processTable(worktree, true, true), processTable(worktree, false, false)} {
			factory.opened = make(chan openedExecutor, 8)
			service, stop := start(table)
			executor := openFirstTurn(service)
			var armed armedTimer
			Eventually(clock.timers, settleBudget.Within).Should(Receive(&armed))
			armed.fire()
			Consistently(func() bool { return sessionOf(service).GetSuspended() }, 200*time.Millisecond).Should(BeFalse(), "the executor is kept")
			Eventually(clock.timers, settleBudget.Within).Should(Receive(&armed), "the bound is armed again")
			executor.executor.EXPECT().Close(gomock.Any()).Return(nil)
			_, err := service.Cancel(ctx, &harnessv1.CancelAgentSessionRequest{AssignmentId: firstAssignment})
			Expect(err).NotTo(HaveOccurred())
			eventually.Await(GinkgoT(), "the session to end", settleBudget,
				func() harnessv1.AgentSessionPhase { return sessionOf(service).GetPhase() },
				func(phase harnessv1.AgentSessionPhase) bool {
					return phase == harnessv1.AgentSessionPhase_AGENT_SESSION_PHASE_CANCELED
				})
			stop()
			Expect(os.RemoveAll(filepath.Join(state, firstAssignment))).To(Succeed())
		}
	})

	It("refuses a nil process table and a non-positive sample interval", func() {
		_, err := harness.NewAgentSessionService(harness.WithProcessTable(nil))
		Expect(err).To(MatchError(harness.ErrInvalidServiceOption))
		_, err = harness.NewAgentSessionService(harness.WithResidentSampleInterval(0))
		Expect(err).To(MatchError(harness.ErrInvalidServiceOption))
	})
})
