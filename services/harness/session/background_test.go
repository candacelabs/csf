// Copyright 2026 Candace Labs

package session_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/session/mocks"
)

// backgroundRecords are the background result records of a run's log.
func backgroundRecords(directory string) []map[string]any {
	kept := []map[string]any{}
	for _, record := range records(directory) {
		if record[session.KeyEventType] == session.EventTypeBackgroundResult {
			kept = append(kept, record)
		}
	}
	return kept
}

var _ = Describe("a background result", func() {
	var (
		ctx    context.Context
		spec   session.TurnExecutorSpec
		runner *session.AgentSessionRunner
		state  string
		run    string
	)

	BeforeEach(func() {
		ctx = context.Background()
		controller := gomock.NewController(GinkgoT())
		launcher := NewMockILauncher(controller)
		executor := mocks.NewMockIOpenTurnExecutor(controller)
		launcher.EXPECT().Run(gomock.Any(), launched("git")).Return(proc.Result{}, nil).AnyTimes()
		executor.EXPECT().Close(gomock.Any()).Return(nil)
		state = GinkgoT().TempDir()
		run = session.RunDirectory(state, assignmentID)
		var err error
		runner, err = session.NewAgentSessionRunner(
			session.WithLauncher(launcher),
			session.WithStateDirectory(state),
			session.WithGateCommand(gateBinary, gateVerb),
			session.WithOpenTurnExecutors(func(ctx context.Context, opened session.TurnExecutorSpec) (session.IOpenTurnExecutor, error) {
				spec = opened
				return executor, nil
			}),
		)
		Expect(err).NotTo(HaveOccurred())
	})

	It("is delivered from the executor, counted as the next turn and recorded with its exit code and bounded output tail", func() {
		opened, err := runner.Open(ctx, newRecipe())
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(opened.Close(ctx)).To(Succeed()) }()
		output := filepath.Join(GinkgoT().TempDir(), "b1.output")
		Expect(os.WriteFile(output, []byte(strings.Repeat("x", 3000)+"\nFAIL services/harness\n\n[exited with code 1]\n"), 0o600)).To(Succeed())

		Expect(spec.TaskNotifications).NotTo(BeNil(), "the open executor is handed the receiver")
		spec.TaskNotifications(claudecode.TaskNotification{TaskID: "b1", ToolUseID: "toolu_1", Status: "failed",
			Summary: "Run the harness suite", OutputFile: output, Elapsed: 90 * time.Second})
		var notification claudecode.TaskNotification
		Expect(opened.BackgroundResults()).To(Receive(&notification))
		Expect(opened.BackgroundResult(ctx, notification)).To(Succeed())

		Expect(opened.State().Turns).To(Equal(1))
		recorded, err := session.ReadRunState(run)
		Expect(err).NotTo(HaveOccurred())
		Expect(recorded.Turns).To(Equal(1), "gates attribute the woken turn's decisions to it")
		results := backgroundRecords(run)
		Expect(results).To(HaveLen(1))
		Expect(results[0]).To(And(
			HaveKeyWithValue(session.KeyTurn, BeNumerically("==", 1)),
			HaveKeyWithValue(session.KeyTaskID, "b1"),
			HaveKeyWithValue(session.KeyToolUseID, "toolu_1"),
			HaveKeyWithValue(session.KeyTaskStatus, "failed"),
			HaveKeyWithValue(session.KeyTaskSummary, "Run the harness suite"),
			HaveKeyWithValue(session.KeyTaskElapsed, BeNumerically("==", 90000)),
			HaveKeyWithValue(session.KeyTaskExitCode, BeNumerically("==", 1)),
		))
		tail, _ := results[0][session.KeyTaskOutputEnd].(string)
		Expect(tail).To(HaveLen(session.OutputTailBytes))
		Expect(tail).To(HaveSuffix("FAIL services/harness\n\n[exited with code 1]\n"))

		turn, err := session.ReadTurnRecords(run)
		Expect(err).NotTo(HaveOccurred())
		Expect(turn[0].EventType).To(Equal(session.EventTypeBackgroundResult), "the woken turn starts at its background result")
	})

	It("reads the exit code from the summary when the output names none, as the executor writes it from 2.1.286", func() {
		opened, err := runner.Open(ctx, newRecipe())
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(opened.Close(ctx)).To(Succeed()) }()
		output := filepath.Join(GinkgoT().TempDir(), "b2.output")
		Expect(os.WriteFile(output, []byte("proof-done\n"), 0o600)).To(Succeed())
		Expect(opened.BackgroundResult(ctx, claudecode.TaskNotification{TaskID: "b2", Status: "completed", OutputFile: output,
			Summary: `Background command "sleep 45; echo proof-done" completed (exit code 0)`})).To(Succeed())
		Expect(backgroundRecords(run)[0]).To(And(
			HaveKeyWithValue(session.KeyTaskExitCode, BeNumerically("==", 0)),
			HaveKeyWithValue(session.KeyTaskOutputEnd, "proof-done\n")))
	})

	It("records a task with no output and no exit code without either", func() {
		opened, err := runner.Open(ctx, newRecipe())
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(opened.Close(ctx)).To(Succeed()) }()
		Expect(opened.BackgroundResult(ctx, claudecode.TaskNotification{TaskID: "a1", Status: "completed", Summary: "Review the diff",
			OutputFile: filepath.Join(state, "gone.output")})).To(Succeed())
		results := backgroundRecords(run)
		Expect(results).To(HaveLen(1))
		Expect(results[0]).To(HaveKeyWithValue(session.KeyTaskOutputEnd, ""))
		Expect(results[0]).NotTo(HaveKey(session.KeyTaskExitCode))
		Expect(results[0]).NotTo(HaveKey(session.KeyError))
	})

	It("drops a notification rather than block the executor once the buffer is full", func() {
		opened, err := runner.Open(ctx, newRecipe())
		Expect(err).NotTo(HaveOccurred())
		defer func() { Expect(opened.Close(ctx)).To(Succeed()) }()
		for range 200 {
			spec.TaskNotifications(claudecode.TaskNotification{TaskID: "b1"})
		}
		Expect(len(opened.BackgroundResults())).To(BeNumerically("<", 200))
	})

	It("is refused on a closed session", func() {
		opened, err := runner.Open(ctx, newRecipe())
		Expect(err).NotTo(HaveOccurred())
		Expect(opened.Close(ctx)).To(Succeed())
		Expect(opened.BackgroundResult(ctx, claudecode.TaskNotification{TaskID: "b1"})).To(MatchError(session.ErrSessionClosed))
	})
})

var _ = Describe("LiveBackgroundTasks", func() {
	tasks := func(ids ...string) string {
		listed := []session.BackgroundTask{}
		for _, id := range ids {
			listed = append(listed, session.BackgroundTask{TaskID: id, TaskType: "local_bash", Description: "Run " + id})
		}
		encoded, err := json.Marshal(map[string]any{"event_type": "system", "event": map[string]any{
			"type": "system", "subtype": session.SubtypeBackgroundTasksChanged, "tasks": listed}})
		Expect(err).NotTo(HaveOccurred())
		return string(encoded)
	}
	read := func(lines ...string) []session.BackgroundTask {
		directory := GinkgoT().TempDir()
		writeLog(directory, lines...)
		records, err := session.ReadRecords(directory, func(_ *session.Record) bool { return true })
		Expect(err).NotTo(HaveOccurred())
		return session.LiveBackgroundTasks(records)
	}

	It("is the latest list the executor reported", func() {
		Expect(read(tasks("b1"), tasks("b1", "b2"), turnRequested, tasks("b2"))).To(Equal(
			[]session.BackgroundTask{{TaskID: "b2", TaskType: "local_bash", Description: "Run b2"}}))
		Expect(read(tasks("b1"), tasks())).To(BeEmpty())
	})

	It("is empty before any report, and after the executor closed or was resumed", func() {
		Expect(read(turnRequested)).To(BeEmpty())
		Expect(read(tasks("b1"), `{"event_type":"harness_session_closed"}`)).To(BeEmpty())
		Expect(read(tasks("b1"), `{"event_type":"harness_run_resumed"}`)).To(BeEmpty())
		Expect(read(tasks("b1"), `{"event_type":"harness_run_resumed"}`, tasks("b3"))).To(HaveLen(1))
	})
})
