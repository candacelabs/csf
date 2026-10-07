// Copyright 2026 Candace Labs

package copilotcli_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/goleak"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/net/model/copilotcli"
	"github.com/candacelabs/csf/io/ipc/proc"
)

// sessionID is the session every spec gives its executor.
var sessionID = uuid.MustParse("3f0c6d3e-7a51-4c55-9a8e-0b6e2f1d9a02")

// Copilot JSON records as copilot 1.0.90 writes them with --output-format
// json, trimmed to the fields the specs care about.
const (
	deltaLine     = `{"type":"assistant.message_delta","data":{"deltaContent":"po"},"ephemeral":true,"id":"d1","timestamp":"2026-10-05T00:00:00Z"}`
	userLine      = `{"type":"user.message","data":{"content":"Reply with pong"},"id":"u1","timestamp":"2026-10-05T00:00:00Z"}`
	assistantLine = `{"type":"assistant.message","data":{"model":"claude-haiku-4.5","content":"Editing the note.","toolRequests":[` +
		`{"toolCallId":"toolu_1","name":"bash","arguments":{"command":"echo hello","description":"Run echo hello"}},` +
		`{"toolCallId":"toolu_2","name":"edit","arguments":{"path":"/work/tree/note.md","old_str":"a","new_str":"b"}},` +
		`{"toolCallId":"toolu_3","name":"csf-GetWorkbenchTheme","arguments":{}}]},"id":"a1","timestamp":"2026-10-05T00:00:01Z"}`
	// The tool completions, in the Copilot SDK's session event shape.
	toolDoneLine   = `{"type":"tool.execution_complete","data":{"toolCallId":"toolu_1","success":true,"result":{"content":"hello","detailedContent":"hello\n"}},"id":"t1","timestamp":"2026-10-05T00:00:01Z"}`
	toolFailedLine = `{"type":"tool.execution_complete","data":{"toolCallId":"toolu_2","success":false,"error":{"message":"no match for old_str"}},"id":"t2","timestamp":"2026-10-05T00:00:01Z"}`
	resultLine     = `{"type":"result","timestamp":"2026-10-05T00:00:02Z","sessionId":"3f0c6d3e-7a51-4c55-9a8e-0b6e2f1d9a02","exitCode":0,"usage":{"premiumRequests":0.66,"sessionDurationMs":5661}}`
	failedLine     = `{"type":"result","timestamp":"2026-10-05T00:00:02Z","sessionId":"3f0c6d3e-7a51-4c55-9a8e-0b6e2f1d9a02","exitCode":1,"usage":{"sessionDurationMs":120}}`
	foreignLine    = `{"type":"result","timestamp":"2026-10-05T00:00:02Z","sessionId":"0b0b0b0b-7a51-4c55-9a8e-0b6e2f1d9a02","exitCode":0,"usage":{"sessionDurationMs":120}}`
	usageFile      = `{"totalPremiumRequestCost":0.66,"totalNanoAiu":2323725000,"tokenDetails":{"input":{"tokenCount":27},"cache_read":{"tokenCount":28540},"cache_write":{"tokenCount":14857},"output":{"tokenCount":357}}}`
	prompt         = "Reply with pong"
)

// copilotRun stands in for one Copilot process: it records the command and
// the prompt read from its standard input, writes lines to its standard
// output and, when usage is set, writes it to the usage file it was named.
type copilotRun struct {
	commands []proc.Command
	prompts  []string
	usage    string
}

func (run *copilotRun) answer(lines ...string) func(ctx context.Context, command proc.Command) (proc.Result, error) {
	return func(ctx context.Context, command proc.Command) (proc.Result, error) {
		run.commands = append(run.commands, command)
		input, err := io.ReadAll(command.Stdin)
		Expect(err).NotTo(HaveOccurred())
		run.prompts = append(run.prompts, string(input))
		for _, line := range lines {
			_, err := io.WriteString(command.Stdout, line+"\n")
			Expect(err).NotTo(HaveOccurred())
		}
		if run.usage != "" {
			Expect(os.WriteFile(argumentAfter(command.Arguments, "--usage-output-file"), []byte(run.usage), 0o600)).To(Succeed())
		}
		return proc.Result{}, nil
	}
}

// argumentAfter is the argument following flag.
func argumentAfter(arguments []string, flag string) string {
	for index, argument := range arguments[:len(arguments)-1] {
		if argument == flag {
			return arguments[index+1]
		}
	}
	Fail("no " + flag + " in the command line")
	return ""
}

// logRecords decodes every JSON slog record written to buffer.
func logRecords(buffer *bytes.Buffer) []map[string]any {
	var records []map[string]any
	scanner := bufio.NewScanner(bytes.NewReader(buffer.Bytes()))
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<22)
	for scanner.Scan() {
		// A JSON log record is an object of unknown shape: slog chooses it.
		var record map[string]any
		Expect(json.Unmarshal(scanner.Bytes(), &record)).To(Succeed())
		records = append(records, record)
	}
	return records
}

// events are the records whose slog message is the turn executor event.
func events(records []map[string]any) []map[string]any {
	var matched []map[string]any
	for _, record := range records {
		if record["msg"] == "turn executor event" {
			matched = append(matched, record)
		}
	}
	return matched
}

// decoded is one reported event's JSON, decoded: its shape is the stream-json
// one the spec asserts, field by field.
func decoded(event copilotcli.Event) map[string]any {
	var value map[string]any
	Expect(json.Unmarshal(event.Raw, &value)).To(Succeed())
	return value
}

var _ = Describe("CopilotCLIExecutor", func() {
	var (
		controller *gomock.Controller
		launcher   *MockILauncher
		logs       *bytes.Buffer
		executor   *copilotcli.CopilotCLIExecutor
		run        *copilotRun
		usagePath  string
		ctx        context.Context
		baseline   goleak.Option
	)

	BeforeEach(func() {
		baseline = goleak.IgnoreCurrent()
		ctx = context.Background()
		controller = gomock.NewController(GinkgoT())
		launcher = NewMockILauncher(controller)
		logs = &bytes.Buffer{}
		run = &copilotRun{}
		usagePath = filepath.Join(GinkgoT().TempDir(), "usage.json")
		var err error
		executor, err = copilotcli.NewCopilotCLIExecutor(launcher, sessionID,
			copilotcli.WithExecutable("/opt/copilot"),
			copilotcli.WithDirectory("/work/tree"),
			copilotcli.WithArguments("--model", "claude-haiku-4.5", "--no-ask-user"),
			copilotcli.WithEnvironment("CANDACE_BAZEL_CACHE=/cache"),
			copilotcli.WithUsageFile(usagePath),
			copilotcli.WithLogger(slog.New(slog.NewJSONHandler(logs, nil))),
		)
		Expect(err).NotTo(HaveOccurred())
	})

	AfterEach(func() {
		Expect(executor.Close(ctx)).To(Succeed())
		Expect(goleak.Find(baseline)).To(Succeed(), "every goroutine a spec started must join")
	})

	Describe("construction", func() {
		DescribeTable("rejects what it cannot use with the defined error",
			func(expected error, withLauncher bool, session uuid.UUID, options ...copilotcli.CopilotCLIExecutorOption) {
				var granted proc.ILauncher
				if withLauncher {
					granted = launcher
				}
				_, err := copilotcli.NewCopilotCLIExecutor(granted, session, options...)
				Expect(err).To(MatchError(expected))
			},
			Entry("no launcher", copilotcli.ErrNoLauncher, false, sessionID),
			Entry("no session", copilotcli.ErrNoSession, true, uuid.Nil),
			Entry("a nil option", copilotcli.ErrInvalidOption, true, sessionID, nil),
			Entry("an empty executable", copilotcli.ErrInvalidOption, true, sessionID, copilotcli.WithExecutable("")),
			Entry("an owned flag", copilotcli.ErrInvalidOption, true, sessionID, copilotcli.WithArguments("--session-id")),
			Entry("the prompt flag", copilotcli.ErrInvalidOption, true, sessionID, copilotcli.WithArguments("-p")),
			Entry("the usage file flag", copilotcli.ErrInvalidOption, true, sessionID, copilotcli.WithArguments("--usage-output-file")),
			Entry("a variable without a value", copilotcli.ErrInvalidOption, true, sessionID, copilotcli.WithEnvironment("NAME")),
			Entry("an empty launch prefix", copilotcli.ErrInvalidOption, true, sessionID, copilotcli.WithLaunchPrefix()),
			Entry("an empty usage file", copilotcli.ErrInvalidOption, true, sessionID, copilotcli.WithUsageFile("")),
			Entry("a nil logger", copilotcli.ErrInvalidOption, true, sessionID, copilotcli.WithLogger(nil)),
		)

		It("reports the session it was given", func() {
			Expect(executor.Session()).To(Equal(sessionID))
		})
	})

	Describe("Propose", func() {
		It("runs one Copilot process on the session with the prompt on its standard input", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer(deltaLine, userLine, assistantLine, resultLine))

			proposal, err := executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})
			Expect(err).NotTo(HaveOccurred())

			Expect(run.commands).To(HaveLen(1))
			command := run.commands[0]
			Expect(command.Executable).To(Equal("/opt/copilot"))
			Expect(command.Directory).To(Equal("/work/tree"))
			Expect(command.ExtraEnvironment).To(Equal([]string{"CANDACE_BAZEL_CACHE=/cache"}))
			Expect(run.prompts).To(Equal([]string{prompt}))
			Expect(command.Arguments).To(Equal([]string{
				"--session-id", sessionID.String(), "--output-format", "json", "--no-auto-update", "--usage-output-file", usagePath,
				"--model", "claude-haiku-4.5", "--no-ask-user",
			}))
			Expect(proposal.Provider).To(Equal(copilotcli.ProviderName))
			types := make([]string, 0, len(proposal.Actions))
			for _, event := range proposal.Actions {
				types = append(types, event.Type)
			}
			Expect(types).To(Equal([]string{"user.message", copilotcli.EventTypeAssistant, copilotcli.EventTypeResult}), "ephemeral records are skipped")
			Expect(string(proposal.Actions[0].Raw)).To(Equal(userLine), "a record the harness does not read back is reported as Copilot wrote it")
		})

		It("reports an assistant message as a Claude Code assistant event, tool calls under Claude Code's names", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer(assistantLine, resultLine))

			proposal, err := executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})
			Expect(err).NotTo(HaveOccurred())

			assistant := decoded(proposal.Actions[0])
			Expect(assistant).To(HaveKeyWithValue("type", "assistant"))
			Expect(assistant).To(HaveKeyWithValue("session_id", sessionID.String()))
			Expect(assistant).To(HaveKey("copilot"), "Copilot's own record is kept")
			Expect(assistant["message"]).To(Equal(map[string]any{
				"role": "assistant", "model": "claude-haiku-4.5",
				"content": []any{
					map[string]any{"type": "text", "text": "Editing the note."},
					map[string]any{"type": "tool_use", "id": "toolu_1", "name": "Bash", "input": map[string]any{"command": "echo hello", "description": "Run echo hello"}},
					map[string]any{"type": "tool_use", "id": "toolu_2", "name": "Edit", "input": map[string]any{"file_path": "/work/tree/note.md", "old_string": "a", "new_string": "b"}},
					map[string]any{"type": "tool_use", "id": "toolu_3", "name": "mcp__csf__GetWorkbenchTheme"},
				},
			}))
		})

		It("reports a tool's completion as a Claude Code tool result, a failure as an error result", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer(assistantLine, toolDoneLine, toolFailedLine, resultLine))

			proposal, err := executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})
			Expect(err).NotTo(HaveOccurred())

			Expect(proposal.Actions[1].Type).To(Equal(copilotcli.EventTypeUser))
			done := decoded(proposal.Actions[1])
			Expect(done).To(HaveKey("copilot"), "Copilot's own record is kept")
			Expect(done["message"]).To(Equal(map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "hello\n", "is_error": false},
			}}))
			Expect(decoded(proposal.Actions[2])["message"]).To(Equal(map[string]any{"role": "user", "content": []any{
				map[string]any{"type": "tool_result", "tool_use_id": "toolu_2", "content": "no match for old_str", "is_error": true},
			}}))
		})

		It("reports the result after the process exits, with the tokens and cache usage of its usage file", func() {
			run.usage = usageFile
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer(assistantLine, resultLine))

			proposal, err := executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})
			Expect(err).NotTo(HaveOccurred())

			result := decoded(proposal.Actions[1])
			Expect(result).To(And(
				HaveKeyWithValue("type", "result"),
				HaveKeyWithValue("subtype", "success"),
				HaveKeyWithValue("is_error", false),
				HaveKeyWithValue("session_id", sessionID.String()),
				HaveKeyWithValue("duration_ms", BeNumerically("==", 5661)),
				// JSON numbers decode as float64.
				HaveKeyWithValue("usage", map[string]any{
					"input_tokens": 27.0, "output_tokens": 357.0,
					"cache_read_input_tokens": 28540.0, "cache_creation_input_tokens": 14857.0,
				}),
				HaveKey("copilot"),
				HaveKeyWithValue("copilot_usage", HaveKeyWithValue("totalNanoAiu", BeNumerically("==", 2323725000))),
				Not(HaveKey("total_cost_usd")),
			))
		})

		It("reports a result without usage when the turn wrote no usage file, and never a previous turn's", func() {
			Expect(os.WriteFile(usagePath, []byte(usageFile), 0o600)).To(Succeed())
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer(resultLine))

			proposal, err := executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})
			Expect(err).NotTo(HaveOccurred())

			result := decoded(proposal.Actions[0])
			Expect(result).NotTo(HaveKey("usage"))
			Expect(result).NotTo(HaveKey("copilot_usage"))
		})

		It("resumes the session on a later turn with the same command line and the new prompt", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer(resultLine)).Times(2)

			_, err := executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})
			Expect(err).NotTo(HaveOccurred())
			_, err = executor.Propose(ctx, &copilotcli.Turn{Prompt: "And again"})
			Expect(err).NotTo(HaveOccurred())

			Expect(run.commands).To(HaveLen(2))
			Expect(run.commands[1].Arguments).To(Equal(run.commands[0].Arguments))
			Expect(run.prompts).To(Equal([]string{prompt, "And again"}))
		})

		It("starts the launch prefix with Copilot and its arguments after it", func() {
			prefixed, err := copilotcli.NewCopilotCLIExecutor(launcher, sessionID,
				copilotcli.WithExecutable("/opt/copilot"), copilotcli.WithLaunchPrefix("/opt/launcher", "--policy", "/run/policy.json", "--"))
			Expect(err).NotTo(HaveOccurred())
			DeferCleanup(func() { Expect(prefixed.Close(ctx)).To(Succeed()) })
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer(resultLine))

			_, err = prefixed.Propose(ctx, &copilotcli.Turn{Prompt: prompt})
			Expect(err).NotTo(HaveOccurred())

			Expect(run.commands[0].Executable).To(Equal("/opt/launcher"))
			Expect(run.commands[0].Arguments).To(Equal([]string{
				"--policy", "/run/policy.json", "--", "/opt/copilot", "--session-id", sessionID.String(), "--output-format", "json", "--no-auto-update",
			}))
		})

		It("reports the prompt in and every record out in the turn executor's record shape", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer(deltaLine, assistantLine, resultLine))

			_, err := executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})
			Expect(err).NotTo(HaveOccurred())

			reported := events(logRecords(logs))
			Expect(reported).To(HaveLen(3))
			Expect(reported[0]).To(And(
				HaveKeyWithValue("direction", "in"),
				HaveKeyWithValue("event_type", "user"),
				HaveKeyWithValue("event", map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": prompt}}),
			))
			Expect(reported[1]).To(And(HaveKeyWithValue("direction", "out"), HaveKeyWithValue("event_type", "assistant")))
			Expect(reported[2]).To(And(HaveKeyWithValue("direction", "out"), HaveKeyWithValue("event_type", "result")))
			for _, record := range reported {
				Expect(record).To(And(
					HaveKeyWithValue("session_id", sessionID.String()),
					HaveKeyWithValue("turn", BeNumerically("==", 1)),
					HaveKeyWithValue("provider", copilotcli.ProviderName),
					HaveKey("trace_id"), HaveKey("span_id"), HaveKey("parent_span_id"),
				))
			}
		})

		It("fails a turn whose result reports a nonzero exit code, as an error result", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer(assistantLine, failedLine))

			_, err := executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})

			Expect(err).To(MatchError(copilotcli.ErrTurnFailed))
			var failure *copilotcli.TurnError
			Expect(errors.As(err, &failure)).To(BeTrue())
			Expect(failure.Turn).To(Equal(1))
			Expect(failure.Events).To(HaveLen(2), "the records seen before the failure are kept")
			Expect(decoded(failure.Events[1])).To(And(HaveKeyWithValue("subtype", "error_during_execution"), HaveKeyWithValue("is_error", true)))
		})

		It("fails a turn whose result names another session", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer(foreignLine))

			_, err := executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})

			Expect(err).To(MatchError(copilotcli.ErrSessionMismatch))
		})

		It("fails a turn whose process exits without a result", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer(assistantLine))

			_, err := executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})

			Expect(err).To(MatchError(copilotcli.ErrTurnIncomplete))
		})

		It("fails a turn whose process fails, with the process's error", func() {
			exit := errors.New("exit status 1")
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).Return(proc.Result{Stderr: []byte(`Error: Model "claude-sonnet-4.6" from --model flag is not available.`)}, exit)

			_, err := executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})

			Expect(err).To(MatchError(exit))
			Expect(logs.String()).To(ContainSubstring("is not available"), "standard error is reported")
		})

		It("fails a turn with a malformed line but keeps reading every record", func() {
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(run.answer("not json", `{"data":{}}`, assistantLine, resultLine))

			_, err := executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})

			Expect(err).To(MatchError(copilotcli.ErrMalformedEvent))
			var failure *copilotcli.TurnError
			Expect(errors.As(err, &failure)).To(BeTrue())
			Expect(failure.Events).To(HaveLen(2))
			Expect(logs.String()).NotTo(ContainSubstring("not json"), "a malformed line is not logged verbatim")
		})

		It("rejects a turn without a prompt with the defined error and starts nothing", func() {
			_, err := executor.Propose(ctx, &copilotcli.Turn{})
			Expect(err).To(MatchError(copilotcli.ErrNoPrompt))
			_, err = executor.Propose(ctx, nil)
			Expect(err).To(MatchError(copilotcli.ErrNoPrompt))
		})
	})

	Describe("Interrupt", func() {
		It("stops the running turn's process and the turn reports the interrupt", func() {
			started := make(chan struct{})
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, command proc.Command) (proc.Result, error) {
				close(started)
				<-ctx.Done()
				return proc.Result{ExitCode: -1}, errors.New("signal: killed")
			})
			outcome := make(chan error, 1)
			go func() {
				_, err := executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})
				outcome <- err
			}()
			<-started

			Expect(executor.Interrupt(ctx)).To(Succeed())

			Expect(<-outcome).To(MatchError(copilotcli.ErrInterrupted))
		})

		It("is a no-op between turns", func() {
			Expect(executor.Interrupt(ctx)).To(Succeed())
		})
	})

	Describe("Close", func() {
		It("stops the running turn and waits for it to return", func() {
			started := make(chan struct{})
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, command proc.Command) (proc.Result, error) {
				close(started)
				<-ctx.Done()
				return proc.Result{ExitCode: -1}, errors.New("signal: killed")
			})
			outcome := make(chan error, 1)
			go func() {
				_, err := executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})
				outcome <- err
			}()
			<-started

			Expect(executor.Close(ctx)).To(Succeed())

			Expect(<-outcome).To(MatchError(copilotcli.ErrClosed))
		})

		It("refuses a turn and an interrupt after Close with the defined error", func() {
			Expect(executor.Close(ctx)).To(Succeed())

			_, err := executor.Propose(ctx, &copilotcli.Turn{Prompt: prompt})
			Expect(err).To(MatchError(copilotcli.ErrClosed))
			Expect(executor.Interrupt(ctx)).To(MatchError(copilotcli.ErrClosed))
		})

		It("is idempotent", func() {
			Expect(executor.Close(ctx)).To(Succeed())
			Expect(executor.Close(ctx)).To(Succeed())
		})
	})
})
