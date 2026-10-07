// Copyright 2026 Candace Labs

package claudecode_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"strings"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/telemetry"
)

var _ = Describe("ClaudeCodeBrain", func() {
	var (
		controller *gomock.Controller
		launcher   *MockILauncher
		logs       *bytes.Buffer
		logger     *slog.Logger
		turn       *claudecode.Turn
	)

	BeforeEach(func() {
		controller = gomock.NewController(GinkgoT())
		launcher = NewMockILauncher(controller)
		logs = &bytes.Buffer{}
		logger = slog.New(slog.NewJSONHandler(logs, nil))
		turn = &claudecode.Turn{Messages: []json.RawMessage{json.RawMessage(userInput)}}
	})

	newBrain := func(options ...claudecode.ClaudeCodeBrainOption) *claudecode.ClaudeCodeBrain {
		brain, err := claudecode.NewClaudeCodeBrain(launcher, sessionID, append([]claudecode.ClaudeCodeBrainOption{claudecode.WithLogger(logger)}, options...)...)
		Expect(err).NotTo(HaveOccurred())
		return brain
	}

	Describe("construction", func() {
		It("requires the process capability", func() {
			_, err := claudecode.NewClaudeCodeBrain(nil, sessionID)
			Expect(err).To(MatchError(claudecode.ErrNoLauncher))
		})

		It("requires a session", func() {
			_, err := claudecode.NewClaudeCodeBrain(launcher, uuid.Nil)
			Expect(err).To(MatchError(claudecode.ErrNoSession))
		})

		DescribeTable("rejects options it cannot use",
			func(option claudecode.ClaudeCodeBrainOption) {
				_, err := claudecode.NewClaudeCodeBrain(launcher, sessionID, option)
				Expect(err).To(MatchError(claudecode.ErrInvalidOption))
			},
			Entry("a nil option", nil),
			Entry("a nil logger", claudecode.WithLogger(nil)),
			Entry("an empty executable", claudecode.WithExecutable("")),
			Entry("the session flag", claudecode.WithArguments("--session-id", "x")),
			Entry("the resume flag", claudecode.WithArguments("--resume")),
			Entry("the output format", claudecode.WithArguments("--output-format", "text")),
			Entry("print mode", claudecode.WithArguments("-p")),
		)

		It("reports the session it was given", func() {
			Expect(newBrain().Session()).To(Equal(sessionID))
		})
	})

	Describe("a turn", func() {
		It("launches Claude Code on the session and forwards input and events unchanged", func() {
			run := &claudeRun{}
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer(initLine, assistantLine, resultLine))
			brain := newBrain(claudecode.WithDirectory("/srv/work"), claudecode.WithArguments("--tools", ""))

			proposal, err := brain.Propose(context.Background(), turn)

			Expect(err).NotTo(HaveOccurred())
			Expect(run.command.Executable).To(Equal(claudecode.DefaultExecutable))
			Expect(run.command.Directory).To(Equal("/srv/work"))
			Expect(run.command.Arguments).To(Equal(append(append(append([]string{}, ownedPrefix...), "--session-id", sessionID.String()), "--tools", "")))
			Expect(string(run.stdin)).To(Equal(userInput + "\n"))
			Expect(proposal.Provider).To(Equal(claudecode.ProviderName))
			Expect(rawLines(proposal.Actions)).To(Equal([]string{initLine, assistantLine, resultLine}))
			Expect(proposal.Actions[2].Type).To(Equal(claudecode.EventTypeResult))
		})

		It("writes each input message on its own compact line", func() {
			run := &claudeRun{}
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer(initLine, resultLine))
			turn.Messages = []json.RawMessage{
				json.RawMessage("{\n  \"type\": \"user\",\n  \"message\": {\"role\": \"user\", \"content\": \"one\"}\n}"),
				json.RawMessage(userInput),
			}

			_, err := newBrain().Propose(context.Background(), turn)

			Expect(err).NotTo(HaveOccurred())
			Expect(string(run.stdin)).To(Equal(`{"type":"user","message":{"role":"user","content":"one"}}` + "\n" + userInput + "\n"))
		})

		It("resumes the session once Claude Code has opened it", func() {
			first, second := &claudeRun{}, &claudeRun{}
			gomock.InOrder(
				launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(first.answer(initLine, resultLine)),
				launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(second.answer(initLine, resultLine)),
			)
			brain := newBrain()

			_, err := brain.Propose(context.Background(), turn)
			Expect(err).NotTo(HaveOccurred())
			_, err = brain.Propose(context.Background(), turn)
			Expect(err).NotTo(HaveOccurred())

			Expect(first.command.Arguments).To(ContainElements("--session-id", sessionID.String()))
			Expect(first.command.Arguments).NotTo(ContainElement("--resume"))
			Expect(second.command.Arguments).To(Equal(append(append([]string{}, ownedPrefix...), "--resume", sessionID.String())))
		})

		It("resumes from the first turn when the session already exists", func() {
			run := &claudeRun{}
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer(initLine, resultLine))

			_, err := newBrain(claudecode.WithResumedSession()).Propose(context.Background(), turn)

			Expect(err).NotTo(HaveOccurred())
			Expect(run.command.Arguments).To(ContainElements("--resume", sessionID.String()))
		})

		It("runs the configured executable", func() {
			run := &claudeRun{}
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer(initLine, resultLine))

			_, err := newBrain(claudecode.WithExecutable("/opt/claude/bin/claude")).Propose(context.Background(), turn)

			Expect(err).NotTo(HaveOccurred())
			Expect(run.command.Executable).To(Equal("/opt/claude/bin/claude"))
		})
	})

	Describe("the report", func() {
		It("logs every event in and out with the trace, session, turn, type and timing", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn((&claudeRun{}).answer(initLine, assistantLine, resultLine))

			_, err := newBrain().Propose(context.Background(), turn)
			Expect(err).NotTo(HaveOccurred())

			records := logRecords(logs)
			started := messages(records, "turn executor turn started")
			finished := messages(records, "turn executor turn finished")
			events := messages(records, "turn executor event")
			Expect(started).To(HaveLen(1))
			Expect(finished).To(HaveLen(1))
			Expect(events).To(HaveLen(4))
			traceID := started[0]["trace_id"]
			Expect(traceID).To(MatchRegexp("^[0-9a-f]{32}$"))
			spans := map[any]bool{}
			for index, record := range events {
				Expect(record).To(HaveKeyWithValue("trace_id", traceID))
				Expect(record).To(HaveKeyWithValue("session_id", sessionID.String()))
				Expect(record).To(HaveKeyWithValue("turn", BeNumerically("==", 1)))
				Expect(record).To(HaveKeyWithValue("sequence", BeNumerically("==", index+1)))
				Expect(record).To(HaveKeyWithValue("parent_span_id", started[0]["span_id"]))
				Expect(record).To(HaveKeyWithValue("elapsed_ms", BeNumerically(">=", 0)))
				Expect(record["span_id"]).To(MatchRegexp("^[0-9a-f]{16}$"))
				spans[record["span_id"]] = true
			}
			Expect(spans).To(HaveLen(4), "every event has a span of its own")
			Expect(events[0]).To(HaveKeyWithValue("direction", "in"))
			Expect(events[0]).To(HaveKeyWithValue("event_type", "user"))
			Expect(events[1]).To(HaveKeyWithValue("direction", "out"))
			Expect(events[1]).To(HaveKeyWithValue("event_type", "system"))
			Expect(events[3]).To(HaveKeyWithValue("event_type", "result"))
			Expect(events[3]["event"]).To(HaveKeyWithValue("result", "pong"))
			Expect(finished[0]).To(HaveKeyWithValue("events", BeNumerically("==", 3)))
			Expect(finished[0]).To(HaveKeyWithValue("duration_ms", BeNumerically(">=", 0)))
			Expect(finished[0]).NotTo(HaveKey("error"))
		})

		It("continues the trace the context carries", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn((&claudeRun{}).answer(initLine, resultLine))
			trace, err := telemetry.NewTraceContext(telemetry.TraceFlagsSampled)
			Expect(err).NotTo(HaveOccurred())
			ctx, err := telemetry.ContextWithTrace(context.Background(), trace)
			Expect(err).NotTo(HaveOccurred())

			_, err = newBrain().Propose(ctx, turn)
			Expect(err).NotTo(HaveOccurred())

			records := logRecords(logs)
			Expect(records).NotTo(BeEmpty())
			for _, record := range records {
				Expect(record).To(HaveKeyWithValue("trace_id", trace.GetTraceId()))
				Expect(record["span_id"]).NotTo(Equal(trace.GetSpanId()))
			}
		})

		It("starts a new trace for every turn", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn((&claudeRun{}).answer(initLine, resultLine)).Times(2)
			brain := newBrain()
			_, err := brain.Propose(context.Background(), turn)
			Expect(err).NotTo(HaveOccurred())
			_, err = brain.Propose(context.Background(), turn)
			Expect(err).NotTo(HaveOccurred())

			started := messages(logRecords(logs), "turn executor turn started")
			Expect(started).To(HaveLen(2))
			Expect(started[1]).To(HaveKeyWithValue("turn", BeNumerically("==", 2)))
			Expect(started[1]).To(HaveKeyWithValue("resume", true))
			Expect(started[0]["trace_id"]).NotTo(Equal(started[1]["trace_id"]))
		})

		It("reports what Claude Code wrote to standard error", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, command proc.Command) (proc.Result, error) {
				_, err := io.WriteString(command.Stdout, initLine+"\n"+resultLine+"\n")
				Expect(err).NotTo(HaveOccurred())
				return proc.Result{Stderr: []byte("warning: slow network\n")}, nil
			})

			_, err := newBrain().Propose(context.Background(), turn)
			Expect(err).NotTo(HaveOccurred())

			diagnostics := messages(logRecords(logs), "turn executor diagnostics")
			Expect(diagnostics).To(HaveLen(1))
			Expect(diagnostics[0]).To(HaveKeyWithValue("stderr", "warning: slow network"))
		})
	})

	Describe("undefined usage", func() {
		It("refuses a nil turn without launching anything", func() {
			_, err := newBrain().Propose(context.Background(), nil)
			Expect(err).To(MatchError(claudecode.ErrNoTurn))
		})

		It("refuses a turn with no messages", func() {
			_, err := newBrain().Propose(context.Background(), &claudecode.Turn{})
			Expect(err).To(MatchError(claudecode.ErrNoTurn))
		})

		DescribeTable("refuses an input message that is not a JSON object",
			func(message string) {
				_, err := newBrain().Propose(context.Background(), &claudecode.Turn{Messages: []json.RawMessage{json.RawMessage(message)}})
				Expect(err).To(MatchError(claudecode.ErrMalformedInput))
			},
			Entry("plain text", "hello"),
			Entry("a JSON array", `[{"type":"user"}]`),
			Entry("truncated JSON", `{"type":"user"`),
			Entry("empty", ""),
		)

		It("returns the events seen so far when Claude Code fails mid-turn", func() {
			crashed := errors.New("ipc/proc: claude: exit status 1")
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, command proc.Command) (proc.Result, error) {
				_, err := io.WriteString(command.Stdout, initLine+"\n"+assistantLine+"\n")
				Expect(err).NotTo(HaveOccurred())
				return proc.Result{ExitCode: 1}, crashed
			})

			proposal, err := newBrain().Propose(context.Background(), turn)

			Expect(proposal).To(BeNil())
			Expect(err).To(MatchError(crashed))
			var turnError *claudecode.TurnError
			Expect(errors.As(err, &turnError)).To(BeTrue())
			Expect(turnError.Turn).To(Equal(1))
			Expect(rawLines(turnError.Events)).To(Equal([]string{initLine, assistantLine}))
			finished := messages(logRecords(logs), "turn executor turn finished")
			Expect(finished).To(HaveLen(1))
			Expect(finished[0]).To(HaveKeyWithValue("level", "ERROR"))
			Expect(finished[0]["error"]).To(ContainSubstring("exit status 1"))
		})

		It("reports an incomplete turn when Claude Code exits cleanly without a result", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn((&claudeRun{}).answer(initLine, assistantLine))

			_, err := newBrain().Propose(context.Background(), turn)

			Expect(err).To(MatchError(claudecode.ErrTurnIncomplete))
		})

		It("resumes after a failed turn in which Claude Code had opened the session", func() {
			second := &claudeRun{}
			gomock.InOrder(
				launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn((&claudeRun{}).answer(initLine)),
				launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(second.answer(initLine, resultLine)),
			)
			brain := newBrain()

			_, err := brain.Propose(context.Background(), turn)
			Expect(err).To(MatchError(claudecode.ErrTurnIncomplete))
			_, err = brain.Propose(context.Background(), turn)
			Expect(err).NotTo(HaveOccurred())

			Expect(second.command.Arguments).To(ContainElements("--resume", sessionID.String()))
		})

		It("reports a malformed stream-json line by number and keeps every real event", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn((&claudeRun{}).answer(initLine, "not json at all", `{"no_type":true}`, resultLine))

			_, err := newBrain().Propose(context.Background(), turn)

			Expect(err).To(MatchError(claudecode.ErrMalformedEvent))
			var malformed *claudecode.MalformedEventError
			Expect(errors.As(err, &malformed)).To(BeTrue())
			Expect(malformed.Line).To(Equal(2))
			var turnError *claudecode.TurnError
			Expect(errors.As(err, &turnError)).To(BeTrue())
			Expect(rawLines(turnError.Events)).To(Equal([]string{initLine, resultLine}))
			reports := messages(logRecords(logs), "turn executor malformed event")
			Expect(reports).To(HaveLen(2))
			Expect(reports[0]).To(HaveKeyWithValue("line", BeNumerically("==", 2)))
			Expect(reports[0]).NotTo(HaveKey("event"), "a malformed line is not logged verbatim")
		})

		It("refuses a run that opened a different session", func() {
			other := strings.Replace(initLine, sessionID.String(), uuid.NewString(), 1)
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn((&claudeRun{}).answer(other, resultLine))

			_, err := newBrain().Propose(context.Background(), turn)

			Expect(err).To(MatchError(claudecode.ErrSessionMismatch))
		})

		It("returns the cancellation when the context ends during a turn", func() {
			ctx, cancel := context.WithCancel(context.Background())
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(runCtx context.Context, command proc.Command) (proc.Result, error) {
				_, err := io.WriteString(command.Stdout, initLine+"\n")
				Expect(err).NotTo(HaveOccurred())
				cancel()
				return proc.Result{ExitCode: -1}, runCtx.Err()
			})

			_, err := newBrain().Propose(ctx, turn)

			Expect(err).To(MatchError(context.Canceled))
		})

		Describe("overlapping turns", func() {
			var baseline goleak.Option

			BeforeEach(func() { baseline = goleak.IgnoreCurrent() })
			AfterEach(func() { goleak.VerifyNone(GinkgoT(), baseline) })

			It("never runs two turns of one session at once, and a canceled waiter gives up", func() {
				entered := make(chan struct{})
				release := make(chan struct{})
				launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, command proc.Command) (proc.Result, error) {
					close(entered)
					<-release
					_, err := io.WriteString(command.Stdout, initLine+"\n"+resultLine+"\n")
					return proc.Result{}, err
				})
				brain := newBrain()
				firstDone := make(chan error, 1)
				go func() {
					defer GinkgoRecover()
					_, err := brain.Propose(context.Background(), turn)
					firstDone <- err
				}()
				<-entered

				waiting, cancel := context.WithCancel(context.Background())
				cancel()
				_, err := brain.Propose(waiting, turn)
				Expect(err).To(MatchError(context.Canceled))

				close(release)
				Expect(<-firstDone).To(Succeed())
			})
		})
	})
})
