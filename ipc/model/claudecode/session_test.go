// Copyright 2026 Candace Labs

package claudecode_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/ipc/model/claudecode"
	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/pkg/eventually"
)

// openBudget bounds a spec's wait for the fake process to react; generous
// because the costs are not symmetric (CS-9).
var openBudget = eventually.Budget{Within: 5 * time.Second}

const (
	secondUserInput = `{"type":"user","message":{"role":"user","content":"Reply with pong again"}}`
	secondResult    = `{"type":"result","subtype":"success","is_error":false,"result":"pong again","session_id":"3f0c6d3e-7a51-4c55-9a8e-0b6e2f1d9a01"}`
	idleLine        = `{"type":"system","subtype":"task_summary","session_id":"3f0c6d3e-7a51-4c55-9a8e-0b6e2f1d9a01"}`
)

// openProcess stands in for one Claude Code process kept open: it answers
// every input line with the scripted lines for that turn, in order, and
// exits when its input closes or its context ends.
type openProcess struct {
	answers [][]string
	stdin   []string
	command proc.Command
	exited  chan struct{}
	// interrupts counts control requests; the spec reads it while the fake
	// process writes it.
	interrupts atomic.Int32
}

func newOpenProcess(answers ...[]string) *openProcess {
	return &openProcess{answers: answers, exited: make(chan struct{})}
}

func (process *openProcess) run(ctx context.Context, command proc.Command) (proc.Result, error) {
	defer close(process.exited)
	process.command = command
	scanner := bufio.NewScanner(command.Stdin)
	turn := 0
	for scanner.Scan() {
		line := scanner.Text()
		process.stdin = append(process.stdin, line)
		if strings.Contains(line, `"control_request"`) {
			process.interrupts.Add(1)
			_, _ = io.WriteString(command.Stdout, `{"type":"control_response","response":{"subtype":"success"}}`+"\n")
			continue
		}
		if turn < len(process.answers) {
			for _, answer := range process.answers[turn] {
				_, _ = io.WriteString(command.Stdout, answer+"\n")
			}
		}
		turn++
		if ctx.Err() != nil {
			return proc.Result{ExitCode: -1}, ctx.Err()
		}
	}
	return proc.Result{Stderr: []byte("bye\n")}, nil
}

var _ = Describe("ClaudeCodeSession", func() {
	var (
		controller *gomock.Controller
		launcher   *MockILauncher
		logs       *bytes.Buffer
		brain      *claudecode.ClaudeCodeBrain
		baseline   goleak.Option
	)

	BeforeEach(func() {
		baseline = goleak.IgnoreCurrent()
		controller = gomock.NewController(GinkgoT())
		launcher = NewMockILauncher(controller)
		logs = &bytes.Buffer{}
		var err error
		brain, err = claudecode.NewClaudeCodeBrain(launcher, sessionID,
			claudecode.WithLogger(slog.New(slog.NewJSONHandler(logs, nil))),
			claudecode.WithEnvironment("CANDACE_BAZEL_CACHE=/runs/one/bazel"))
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(goleak.Find(baseline)).To(Succeed(), "every goroutine the session started must join")
	})

	turnOf := func(input string) *claudecode.Turn {
		return &claudecode.Turn{Messages: []json.RawMessage{json.RawMessage(input)}}
	}

	It("runs several turns on one process and closes it", func() {
		process := newOpenProcess([]string{initLine, assistantLine, resultLine}, []string{secondResult})
		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(process.run)

		open, err := brain.Open(context.Background())
		Expect(err).NotTo(HaveOccurred())
		first, err := open.Propose(context.Background(), turnOf(userInput))
		Expect(err).NotTo(HaveOccurred())
		Expect(rawLines(first.Actions)).To(Equal([]string{initLine, assistantLine, resultLine}))
		second, err := open.Propose(context.Background(), turnOf(secondUserInput))
		Expect(err).NotTo(HaveOccurred())
		Expect(rawLines(second.Actions)).To(Equal([]string{secondResult}))
		Expect(open.Close(context.Background())).To(Succeed())

		Expect(process.stdin).To(Equal([]string{userInput, secondUserInput}), "both turns went to the one process")
		Expect(process.command.Arguments).To(Equal(append(append([]string{}, ownedPrefix...), "--session-id", sessionID.String())))
		Expect(process.command.ExtraEnvironment).To(Equal([]string{"CANDACE_BAZEL_CACHE=/runs/one/bazel"}))
		started := messages(logRecords(logs), "turn executor turn started")
		Expect(started).To(HaveLen(2))
		Expect(started[1]).To(HaveKeyWithValue("turn", BeNumerically("==", 2)))
		Expect(messages(logRecords(logs), "turn executor diagnostics")).To(HaveLen(1), "stderr is reported when the process exits")
	})

	It("reports events written between turns under turn zero", func() {
		process := newOpenProcess([]string{initLine, resultLine, idleLine})
		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(process.run)

		open, err := brain.Open(context.Background())
		Expect(err).NotTo(HaveOccurred())
		proposal, err := open.Propose(context.Background(), turnOf(userInput))
		Expect(err).NotTo(HaveOccurred())
		Expect(rawLines(proposal.Actions)).To(Equal([]string{initLine, resultLine}), "the turn ends at its result")
		Expect(open.Close(context.Background())).To(Succeed())

		idle := []map[string]any{}
		for _, record := range messages(logRecords(logs), "turn executor event") {
			if record["turn"] == float64(0) {
				idle = append(idle, record)
			}
		}
		Expect(idle).To(HaveLen(1))
		Expect(idle[0]).To(HaveKeyWithValue("event_type", "system"))
	})

	It("delivers an interrupt as a control request on the open input", func() {
		process := newOpenProcess([]string{initLine, resultLine})
		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(process.run)

		open, err := brain.Open(context.Background())
		Expect(err).NotTo(HaveOccurred())
		_, err = open.Propose(context.Background(), turnOf(userInput))
		Expect(err).NotTo(HaveOccurred())
		Expect(open.Interrupt(context.Background())).To(Succeed())
		eventually.Await(GinkgoT(), "the interrupt to reach the process", openBudget, func() int32 { return process.interrupts.Load() }, func(count int32) bool { return count == 1 })
		Expect(open.Close(context.Background())).To(Succeed())

		var request map[string]any
		Expect(json.Unmarshal([]byte(process.stdin[1]), &request)).To(Succeed())
		Expect(request).To(HaveKeyWithValue("type", "control_request"))
		Expect(request["request"]).To(HaveKeyWithValue("subtype", "interrupt"))
		logged := []map[string]any{}
		for _, record := range messages(logRecords(logs), "turn executor event") {
			if record["event_type"] == claudecode.EventTypeControlResponse || record["event_type"] == "control_request" {
				logged = append(logged, record)
			}
		}
		Expect(logged).To(HaveLen(2), "the interrupt and its answer are both in the log")
		Expect(logged[0]).To(HaveKeyWithValue("direction", "in"))
		Expect(logged[0]).To(HaveKeyWithValue("turn", BeNumerically("==", 0)), "sent between turns")
	})

	It("reports a turn the process ended before its result", func() {
		process := newOpenProcess([]string{initLine})
		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, command proc.Command) (proc.Result, error) {
			_, _ = io.ReadAll(io.LimitReader(command.Stdin, int64(len(userInput)+1)))
			_, _ = io.WriteString(command.Stdout, initLine+"\n")
			defer close(process.exited)
			return proc.Result{}, nil
		})

		open, err := brain.Open(context.Background())
		Expect(err).NotTo(HaveOccurred())
		_, err = open.Propose(context.Background(), turnOf(userInput))
		Expect(err).To(MatchError(claudecode.ErrTurnIncomplete))
		var failure *claudecode.TurnError
		Expect(errors.As(err, &failure)).To(BeTrue())
		Expect(rawLines(failure.Events)).To(Equal([]string{initLine}))
		Expect(open.Close(context.Background())).To(Succeed())
	})

	It("refuses a turn after the process is closed", func() {
		process := newOpenProcess()
		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(process.run)

		open, err := brain.Open(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(open.Close(context.Background())).To(Succeed())
		_, err = open.Propose(context.Background(), turnOf(userInput))
		Expect(err).To(MatchError(claudecode.ErrSessionClosed))
		Expect(open.Interrupt(context.Background())).To(MatchError(claudecode.ErrSessionClosed))
		Expect(open.Close(context.Background())).To(Succeed(), "Close is idempotent")
	})

	It("kills the process when the close budget ends before it exits", func() {
		blocked := make(chan struct{})
		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, command proc.Command) (proc.Result, error) {
			close(blocked)
			<-ctx.Done()
			return proc.Result{ExitCode: -1}, ctx.Err()
		})

		open, err := brain.Open(context.Background())
		Expect(err).NotTo(HaveOccurred())
		<-blocked
		closing, cancel := context.WithCancel(context.Background())
		cancel()
		err = open.Close(closing)
		Expect(err).To(MatchError(claudecode.ErrSessionClosed))
		Expect(err).To(MatchError(context.Canceled))
	})

	It("abandons a turn whose caller stops waiting", func() {
		stuck := make(chan struct{})
		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, command proc.Command) (proc.Result, error) {
			_, _ = io.ReadAll(io.LimitReader(command.Stdin, int64(len(userInput)+1)))
			_, _ = io.WriteString(command.Stdout, initLine+"\n")
			close(stuck)
			<-ctx.Done()
			return proc.Result{ExitCode: -1}, ctx.Err()
		})

		open, err := brain.Open(context.Background())
		Expect(err).NotTo(HaveOccurred())
		waiting, stopWaiting := context.WithCancel(context.Background())
		go func() { <-stuck; stopWaiting() }()
		_, err = open.Propose(waiting, turnOf(userInput))
		Expect(err).To(MatchError(claudecode.ErrTurnAbandoned))
		closing, cancel := context.WithCancel(context.Background())
		cancel()
		_ = open.Close(closing)
	})
})
