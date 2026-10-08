// Copyright 2026 Candace Labs

package sessiongate_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/proc"
	iogithub "github.com/candacelabs/csf/io/net/github"
	"github.com/candacelabs/csf/pkg/telemetry"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/sessiongate"
)

const (
	worktree    = "/runs/assignment/worktree"
	branch      = "h1/scratch"
	baseBranch  = "main"
	prTitle     = "H1 scratch change"
	sessionID   = "7d3d7a2e-2c55-5f45-9d0b-2a7a4d6c1e10"
	toolUseID   = "toolu_01"
	pullRequest = "https://github.com/candacelabs/repository/pull/9"
	remoteURL   = "https://github.com/candacelabs/repository.git"
)

// hookInput is a hook call as Claude Code writes it.
func hookInput(event string, tool string, command string, background bool) []byte {
	input := map[string]any{ // the hook input is an open JSON object
		"session_id":      sessionID,
		"hook_event_name": event,
		"tool_name":       tool,
		"tool_use_id":     toolUseID,
		"tool_input":      map[string]any{"command": command, "run_in_background": background},
	}
	encoded, err := json.Marshal(input)
	Expect(err).NotTo(HaveOccurred())
	return encoded
}

// gateRecords decodes the event log's session gate records.
func gateRecords(directory string) []map[string]any {
	file, err := os.Open(filepath.Join(directory, session.EventsFile))
	Expect(err).NotTo(HaveOccurred())
	defer func() { Expect(file.Close()).To(Succeed()) }()
	var records []map[string]any
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		// A slog JSON record is an object of unknown shape.
		record := map[string]any{}
		Expect(json.Unmarshal(scanner.Bytes(), &record)).To(Succeed())
		if record[session.KeyEventType] == session.EventTypeGateDecision {
			records = append(records, record)
		}
	}
	Expect(scanner.Err()).NotTo(HaveOccurred())
	return records
}

// command matches a launched program by executable and leading arguments.
func command(executable string, arguments ...string) gomock.Matcher {
	return gomock.Cond(func(launched proc.Command) bool {
		return launched.Executable == executable && launched.Directory == worktree &&
			len(launched.Arguments) >= len(arguments) && slices.Equal(launched.Arguments[:len(arguments)], arguments)
	})
}

func output(stdout string) proc.Result { return proc.Result{Stdout: []byte(stdout)} }

// appendLines appends lines to the event log of the run in directory, as the
// harness writes the turn executor's events into it.
func appendLines(directory string, lines ...string) {
	file, err := os.OpenFile(filepath.Join(directory, session.EventsFile), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	Expect(err).NotTo(HaveOccurred())
	for _, line := range lines {
		_, err := file.WriteString(line + "\n")
		Expect(err).NotTo(HaveOccurred())
	}
	Expect(file.Close()).To(Succeed())
}

var _ = Describe("SessionGate", func() {
	var (
		controller *gomock.Controller
		launcher   *MockILauncher
		directory  string
		state      *session.RunState
		gate       *sessiongate.SessionGate
		ctx        context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		controller = gomock.NewController(GinkgoT())
		launcher = NewMockILauncher(controller)
		directory = GinkgoT().TempDir()
		trace, err := telemetry.NewTraceContext(telemetry.TraceFlagsSampled)
		Expect(err).NotTo(HaveOccurred())
		state = &session.RunState{
			AssignmentID: "0caf2d51-1ee7-468a-882f-ac478a437640", AgentID: "scratch", TicketURL: "https://example.invalid/issues/1",
			SessionID: sessionID, TraceID: trace.GetTraceId(), SpanID: trace.GetSpanId(),
			Repository: "/repository", Worktree: worktree, Branch: branch, BaseBranch: baseBranch,
			PullRequestTitle: prTitle, Turns: 1,
		}
		content, err := json.Marshal(state)
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(directory, session.RunStateFile), content, 0o600)).To(Succeed())
		gate, err = sessiongate.NewSessionGate(sessiongate.WithLauncher(launcher), sessiongate.WithRunDirectory(directory))
		Expect(err).NotTo(HaveOccurred())
	})

	Describe("construction", func() {
		It("requires the process capability", func() {
			_, err := sessiongate.NewSessionGate(sessiongate.WithRunDirectory(directory))
			Expect(err).To(MatchError(sessiongate.ErrNoLauncher))
			_, err = sessiongate.NewSessionGate(sessiongate.WithLauncher(nil))
			Expect(err).To(MatchError(sessiongate.ErrNoLauncher))
		})

		It("requires the run directory", func() {
			_, err := sessiongate.NewSessionGate(sessiongate.WithLauncher(launcher))
			Expect(err).To(MatchError(sessiongate.ErrNoRunDirectory))
		})

		It("rejects a nil option", func() {
			_, err := sessiongate.NewSessionGate(nil)
			Expect(err).To(MatchError(sessiongate.ErrInvalidOption))
		})

	})

	Describe("operator mode, for a directory with no recorded run", func() {
		var operator *sessiongate.SessionGate

		BeforeEach(func() {
			var err error
			operator, err = sessiongate.NewSessionGate(sessiongate.WithLauncher(launcher), sessiongate.WithRunDirectory(GinkgoT().TempDir()),
				sessiongate.WithEndpointRegistry(servingRegistry()))
			Expect(err).NotTo(HaveOccurred())
		})

		It("denies a stop of csf serve", func() {
			decision, err := operator.Handle(ctx, session.HookPreToolUse, hookInput(session.HookPreToolUse, session.ToolBash, "csf stop", false))
			Expect(err).NotTo(HaveOccurred())
			Expect(decision.HookSpecificOutput.PermissionDecision).To(Equal("deny"))
			Expect(decision.HookSpecificOutput.PermissionDecisionReason).To(ContainSubstring("Workbench at http://127.0.0.1:14120/"))
		})

		It("answers nothing else: no wait gate, no commit gate, no reply gate", func() {
			for event, command := range map[string]string{session.HookPreToolUse: "sleep 5", session.HookPostToolUse: "git commit -m x", session.HookStop: ""} {
				decision, err := operator.Handle(ctx, event, hookInput(event, session.ToolBash, command, false))
				Expect(err).NotTo(HaveOccurred())
				Expect(decision).To(BeNil())
			}
		})

		It("fails a malformed hook input", func() {
			_, err := operator.Handle(ctx, session.HookPreToolUse, []byte("{"))
			Expect(err).To(MatchError(sessiongate.ErrMalformedHookInput))
		})
	})

	Describe("the endpoint gate", func() {
		BeforeEach(func() {
			var err error
			gate, err = sessiongate.NewSessionGate(sessiongate.WithLauncher(launcher), sessiongate.WithRunDirectory(directory),
				sessiongate.WithEndpointRegistry(servingRegistry()))
			Expect(err).NotTo(HaveOccurred())
		})

		It("denies csf stop without a restart, names the endpoints and their users, and logs the denial", func() {
			decision, err := gate.Handle(ctx, session.HookPreToolUse, hookInput(session.HookPreToolUse, session.ToolBash, "csf stop", false))
			Expect(err).NotTo(HaveOccurred())
			Expect(decision.HookSpecificOutput.PermissionDecision).To(Equal("deny"))
			Expect(decision.HookSpecificOutput.PermissionDecisionReason).To(And(
				ContainSubstring("Rejected by the CSF endpoint gate"), ContainSubstring("used by the operator, in a browser")))
			records := gateRecords(directory)
			Expect(records).To(HaveLen(2))
			Expect(records[0]).To(HaveKeyWithValue("gate", sessiongate.GateWait))
			Expect(records[1]).To(And(HaveKeyWithValue("gate", sessiongate.GateEndpoint), HaveKeyWithValue("decision", sessiongate.DecisionDeny),
				HaveKeyWithValue("reason", decision.HookSpecificOutput.PermissionDecisionReason)))
		})

		It("lets a restart through and logs nothing of its own", func() {
			decision, err := gate.Handle(ctx, session.HookPreToolUse, hookInput(session.HookPreToolUse, session.ToolBash, "csf stop && csf serve -detach", false))
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
			Expect(gateRecords(directory)).To(ConsistOf(HaveKeyWithValue("gate", sessiongate.GateWait)))
		})
	})

	Describe("the wait gate", func() {
		It("denies a shell poll loop, names the replacement and logs the decision under the run's trace", func() {
			decision, err := gate.Handle(ctx, session.HookPreToolUse,
				hookInput(session.HookPreToolUse, session.ToolBash, `until ! pgrep -f s5final.sh >/dev/null; do sleep 10; done; cat x.log`, false))
			Expect(err).NotTo(HaveOccurred())
			Expect(decision.HookSpecificOutput.HookEventName).To(Equal(session.HookPreToolUse))
			Expect(decision.HookSpecificOutput.PermissionDecision).To(Equal("deny"))
			Expect(decision.HookSpecificOutput.PermissionDecisionReason).To(And(
				ContainSubstring("poll_loop"), ContainSubstring("pgrep_full"),
				ContainSubstring("`csf await harness-stopped -pid PID -deadline DURATION`")))
			records := gateRecords(directory)
			Expect(records).To(HaveLen(1))
			Expect(records[0]).To(HaveKeyWithValue(session.KeyTraceID, state.TraceID))
			Expect(records[0]).To(HaveKeyWithValue(session.KeyParentSpanID, state.SpanID))
			Expect(records[0]).To(HaveKeyWithValue(session.KeySessionID, sessionID))
			Expect(records[0]).To(HaveKeyWithValue(session.KeyTurn, BeNumerically("==", 1)))
			Expect(records[0]).To(HaveKeyWithValue("decision", sessiongate.DecisionDeny))
			Expect(records[0]).To(HaveKeyWithValue("gate", sessiongate.GateWait))
			Expect(records[0][session.KeySpanID]).To(MatchRegexp(`^[0-9a-f]{16}$`))
			Expect(records[0]).To(HaveKey(session.KeyElapsed))
		})

		It("allows an ordinary command and logs it", func() {
			decision, err := gate.Handle(ctx, session.HookPreToolUse, hookInput(session.HookPreToolUse, session.ToolBash, `go test ./...`, false))
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
			Expect(gateRecords(directory)).To(ConsistOf(HaveKeyWithValue("decision", sessiongate.DecisionAllow)))
		})

		It("allows a sleep the turn does not wait for, but never a poll loop", func() {
			decision, err := gate.Handle(ctx, session.HookPreToolUse, hookInput(session.HookPreToolUse, session.ToolBash, `sleep 30`, true))
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
			decision, err = gate.Handle(ctx, session.HookPreToolUse, hookInput(session.HookPreToolUse, session.ToolBash, `while true; do sleep 1; done`, true))
			Expect(err).NotTo(HaveOccurred())
			Expect(decision.HookSpecificOutput.PermissionDecision).To(Equal("deny"))
		})

		It("skips a tool other than Bash", func() {
			decision, err := gate.Handle(ctx, session.HookPreToolUse, hookInput(session.HookPreToolUse, "Edit", "", false))
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
			Expect(gateRecords(directory)).To(ConsistOf(HaveKeyWithValue("decision", sessiongate.DecisionSkip)))
		})

		It("allows and logs a command the shell parser cannot read", func() {
			decision, err := gate.Handle(ctx, session.HookPreToolUse, hookInput(session.HookPreToolUse, session.ToolBash, `while true; do`, false))
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
			Expect(gateRecords(directory)).To(ConsistOf(And(
				HaveKeyWithValue("decision", sessiongate.DecisionAllow), HaveKey("reason"))))
		})
	})

	Describe("the ready gate", func() {
		readyInput := func() []byte {
			return hookInput(session.HookPreToolUse, session.ToolReadyPullRequest, "", false)
		}
		checkMerge := func() gomock.Matcher { return command("bash", "tools/check-merge.sh") }

		It("runs the merge path's checks in the worktree and allows the ready tool on a pull request that passes", func() {
			launcher.EXPECT().Run(gomock.Any(), checkMerge()).Return(output("check-merge: passed\n"), nil)
			decision, err := gate.Handle(ctx, session.HookPreToolUse, readyInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
			Expect(gateRecords(directory)).To(ContainElement(And(
				HaveKeyWithValue("gate", sessiongate.GateReady), HaveKeyWithValue("decision", sessiongate.DecisionAllow))))
		})

		It("denies the ready tool on a consistency regression and quotes the report", func() {
			launcher.EXPECT().Run(gomock.Any(), checkMerge()).Return(
				proc.Result{ExitCode: 1, Stdout: []byte("| cs-16 | 48 | 49 | +1 |\n"), Stderr: []byte("check-merge: REFUSED: ontology-ratchet\n")},
				errors.New("ipc/proc: bash: exit status 1"))
			decision, err := gate.Handle(ctx, session.HookPreToolUse, readyInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision.HookSpecificOutput.PermissionDecision).To(Equal("deny"))
			Expect(decision.HookSpecificOutput.PermissionDecisionReason).To(And(
				ContainSubstring("| cs-16 | 48 | 49 | +1 |"), ContainSubstring("REFUSED: ontology-ratchet")))
			Expect(gateRecords(directory)).To(ContainElement(And(
				HaveKeyWithValue("gate", sessiongate.GateReady), HaveKeyWithValue("decision", sessiongate.DecisionDeny),
				HaveKey(session.KeyError))))
		})

		Describe("with the proof check", func() {
			const body = "**Verdict:** Good: replays struggle less.\n"
			var checked []string

			BeforeEach(func() {
				checked = nil
				var err error
				gate, err = sessiongate.NewSessionGate(sessiongate.WithLauncher(launcher), sessiongate.WithRunDirectory(directory),
					sessiongate.WithCitationCheck(func(_ context.Context, read string) error {
						checked = append(checked, read)
						if read == body {
							return errors.New("evaluate: an improvement is claimed without a suite citation")
						}
						return nil
					}))
				Expect(err).NotTo(HaveOccurred())
				launcher.EXPECT().Run(gomock.Any(), command("git")).Return(output("https://github.com/example/repo.git\n"), nil).AnyTimes()
			})

			It("denies marking ready a pull request that claims an improvement without a citation, before the merge path runs", func() {
				launcher.EXPECT().Run(gomock.Any(), command("gh", "pr", "view", branch)).Return(output(body), nil)
				decision, err := gate.Handle(ctx, session.HookPreToolUse, readyInput())
				Expect(err).NotTo(HaveOccurred())
				Expect(decision.HookSpecificOutput.PermissionDecision).To(Equal("deny"))
				Expect(decision.HookSpecificOutput.PermissionDecisionReason).To(And(
					ContainSubstring("evaluation suite citation"), ContainSubstring("csf eval show")))
				Expect(checked).To(Equal([]string{body}))
				Expect(gateRecords(directory)).To(ContainElement(And(
					HaveKeyWithValue("gate", sessiongate.GateReady), HaveKeyWithValue("decision", sessiongate.DecisionDeny))))
			})

			It("goes on to the merge path for a body the proof check passes", func() {
				launcher.EXPECT().Run(gomock.Any(), command("gh", "pr", "view", branch)).Return(output("**Verdict:** Inconclusive, unscored.\n"), nil)
				launcher.EXPECT().Run(gomock.Any(), checkMerge()).Return(output("check-merge: passed\n"), nil)
				decision, err := gate.Handle(ctx, session.HookPreToolUse, readyInput())
				Expect(err).NotTo(HaveOccurred())
				Expect(decision).To(BeNil())
			})
		})
	})

	Describe("the GitHub gate", func() {
		bash := func(text string) []byte { return hookInput(session.HookPreToolUse, session.ToolBash, text, false) }

		DescribeTable("refuses a gh command that reaches GitHub and names the tool that replaces it",
			func(text string, tool string) {
				decision, err := gate.Handle(ctx, session.HookPreToolUse, bash(text))
				Expect(err).NotTo(HaveOccurred())
				Expect(decision.HookSpecificOutput.PermissionDecision).To(Equal("deny"))
				Expect(decision.HookSpecificOutput.PermissionDecisionReason).To(ContainSubstring(tool))
				Expect(gateRecords(directory)).To(ContainElement(And(
					HaveKeyWithValue("gate", sessiongate.GateGitHub), HaveKeyWithValue("decision", sessiongate.DecisionDeny))))
			},
			Entry("a merge", `gh pr merge 16 --squash --admin`, "the MCP tool mcp__csf__PullsMerge (csf github PullsMerge from a script)"),
			Entry("marking ready", `gh pr ready 16`, "mcp__csf__MarkPullRequestReady"),
			Entry("filing an issue inside bash -c", `bash -c 'gh issue create --title t --body b'`, "mcp__csf__IssuesCreate"),
			Entry("closing an issue", `gh issue close 7 --comment done`, "mcp__csf__IssuesUpdate"),
			Entry("the raw API", `gh api repos/o/r/issues/7`, "csf github -list names them"),
		)

		DescribeTable("leaves gh commands that do not reach issues, pull requests or the API alone",
			func(text string) {
				decision, err := gate.Handle(ctx, session.HookPreToolUse, bash(text))
				Expect(err).NotTo(HaveOccurred())
				Expect(decision).To(BeNil())
			},
			Entry("watching a run", `gh run watch 42 --exit-status`),
			Entry("the words in text", `echo "gh pr merge 16"`),
		)
	})

	Describe("the MCP gate", func() {
		call := func(tool string) []byte { return hookInput(session.HookPreToolUse, tool, "", false) }

		DescribeTable("refuses a tool of any MCP server but csf, naming the server and csf",
			func(tool string, server string) {
				decision, err := gate.Handle(ctx, session.HookPreToolUse, call(tool))
				Expect(err).NotTo(HaveOccurred())
				Expect(decision.HookSpecificOutput.PermissionDecision).To(Equal("deny"))
				Expect(decision.HookSpecificOutput.PermissionDecisionReason).To(And(
					ContainSubstring(string(sessiongate.RuleForeignMCP)),
					ContainSubstring(`the MCP server "`+server+`"`),
					ContainSubstring("csf is the only MCP server")))
				Expect(gateRecords(directory)).To(ContainElement(And(
					HaveKeyWithValue("gate", sessiongate.GateMCP), HaveKeyWithValue("decision", sessiongate.DecisionDeny))))
			},
			Entry("GitHub's own server", "mcp__github__issue_write", "github"),
			Entry("a mail server", "mcp__Gmail__send_message", "Gmail"),
			Entry("a server whose name starts like csf's", "mcp__csfx__anything", "csfx"),
		)

		DescribeTable("leaves csf's own tools and built-in tools alone",
			func(tool string) {
				decision, err := gate.Handle(ctx, session.HookPreToolUse, call(tool))
				Expect(err).NotTo(HaveOccurred())
				Expect(decision).To(BeNil())
			},
			Entry("a csf GitHub tool", "mcp__csf__IssuesGet"),
			Entry("a built-in tool", "Read"),
		)
	})

	Describe("the reply gate", func() {
		const (
			turnRequested = `{"event_type":"harness_turn_requested","turn":1}`
			unvetted      = `{"event_type":"unvetted_terms","turn":1,"terms":["cgroup","sandboxing"]}`
			agreeing      = "**Verdict:** Agreed. Cgroups plus the kernel's own sandboxing features fit better than containers here."
			promising     = "I've also saved your standing request: from now on I'll flag any analogy that's wrong in a way that matters."
		)
		stopInput := func() []byte {
			input, err := json.Marshal(map[string]any{"session_id": sessionID, "hook_event_name": session.HookStop})
			Expect(err).NotTo(HaveOccurred())
			return input
		}
		appendLog := func(lines ...string) { appendLines(directory, lines...) }
		assistant := func(text string, tools ...session.ToolUse) string {
			blocks := []map[string]any{{"type": session.BlockText, "text": text}}
			for _, tool := range tools {
				blocks = append(blocks, map[string]any{"type": session.BlockToolUse, "name": tool.Name, "input": tool.Input})
			}
			record := map[string]any{"event_type": session.EventTypeAssistant, "turn": 1, "direction": session.DirectionOut,
				"event": map[string]any{"type": session.EventTypeAssistant, "message": map[string]any{"content": blocks}}}
			encoded, err := json.Marshal(record)
			Expect(err).NotTo(HaveOccurred())
			return string(encoded)
		}
		replyRecords := func() []map[string]any {
			kept := []map[string]any{}
			for _, record := range gateRecords(directory) {
				if record["gate"] == sessiongate.GateReply {
					kept = append(kept, record)
				}
			}
			return kept
		}

		It("refuses an agreeing reply to unvetted terms with no research check and makes the turn continue", func() {
			appendLog(turnRequested, unvetted, assistant("You mean kernel-level isolation. Checking."), assistant(agreeing))
			decision, err := gate.Handle(ctx, session.HookStop, stopInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision.Decision).To(Equal("block"))
			Expect(decision.HookSpecificOutput).To(BeNil())
			Expect(decision.Reason).To(And(
				HavePrefix("Rejected by the CSF reply gate. research_check: no complete research check for the unvetted term(s) cgroup, sandboxing."),
				ContainSubstring("fenced block tagged research-check holding "+session.ResearchCheckSkeleton("cgroup"))))
			Expect(replyRecords()).To(ConsistOf(And(
				HaveKeyWithValue("decision", sessiongate.DecisionDeny),
				HaveKeyWithValue("rules", ConsistOf("research_check")),
				HaveKeyWithValue("refusals", BeNumerically("==", 0)),
				HaveKeyWithValue(session.KeyTraceID, state.TraceID))))
		})

		It("passes the reply once it carries a complete research check per term, however the term is spelled", func() {
			appendLog(turnRequested, unvetted, assistant(agreeing+"\n\n"+check("Cgroups")+check("sandbox")))
			decision, err := gate.Handle(ctx, session.HookStop, stopInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
			Expect(replyRecords()).To(ConsistOf(HaveKeyWithValue("decision", sessiongate.DecisionAllow)))
		})

		It("refuses a commitment whose only artifact is a memory-file write, and passes it with a commit in the turn", func() {
			memory := edit("Write", "/srv/someone/.claude/projects/x/memory/analogies.md")
			appendLog(turnRequested, assistant("Measuring first.", memory), assistant(promising))
			decision, err := gate.Handle(ctx, session.HookStop, stopInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision.Decision).To(Equal("block"))
			Expect(decision.Reason).To(ContainSubstring(`commitment: "I've also saved your standing request: from now on I'll flag any analogy`))
			Expect(replyRecords()).To(ConsistOf(HaveKeyWithValue("rules", ConsistOf("commitment"))))

			appendLog(assistant("Committed the gate change.", bash(`git commit -F /tmp/message`)), assistant(promising))
			decision, err = gate.Handle(ctx, session.HookStop, stopInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
			Expect(replyRecords()[1]).To(HaveKeyWithValue("decision", sessiongate.DecisionAllow))
		})

		It("refuses an over-long reply when the turn's operator affect reads high strain, and passes it to a calm message", func() {
			strained := `{"event_type":"operator_affect","turn":1,"target_turn":0,"affect":{"strain":"high","kind":"directive","correction":true}}`
			calm := `{"event_type":"operator_affect","turn":2,"target_turn":1,"affect":{"strain":"low","kind":"directive"}}`
			long := strings.Repeat("word ", 801)
			appendLog(turnRequested, strained, assistant(long))
			decision, err := gate.Handle(ctx, session.HookStop, stopInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision.Decision).To(Equal("block"))
			Expect(decision.Reason).To(Equal("Rejected by the CSF reply gate. strain: the operator's strain reads high and the reply runs 801 words; answer in at most 800."))
			Expect(replyRecords()).To(ConsistOf(HaveKeyWithValue("rules", ConsistOf("strain"))))

			appendLog(`{"event_type":"harness_turn_requested","turn":2}`, calm, assistant(long))
			decision, err = gate.Handle(ctx, session.HookStop, stopInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
		})

		It("passes a reply after the turn's refusals reach the limit and records the limit", func() {
			appendLog(turnRequested, unvetted, assistant(agreeing))
			for range 2 {
				decision, err := gate.Handle(ctx, session.HookStop, stopInput())
				Expect(err).NotTo(HaveOccurred())
				Expect(decision.Decision).To(Equal("block"))
			}
			decision, err := gate.Handle(ctx, session.HookStop, stopInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
			Expect(replyRecords()[2]).To(And(HaveKeyWithValue("decision", sessiongate.DecisionLimit), HaveKeyWithValue("refusals", BeNumerically("==", 2))))
		})

		It("judges only the turn running, not an earlier one", func() {
			appendLog(turnRequested, unvetted, assistant(agreeing), `{"event_type":"harness_turn_requested","turn":2}`, assistant("Rebased."))
			decision, err := gate.Handle(ctx, session.HookStop, stopInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
			Expect(replyRecords()).To(ConsistOf(HaveKeyWithValue("decision", sessiongate.DecisionAllow)))
		})

		It("passes a wait while a task started in an earlier turn still runs, and refuses it once none does", func() {
			const (
				running  = `{"event_type":"system","turn":1,"event":{"type":"system","subtype":"background_tasks_changed","tasks":[{"task_id":"b1","task_type":"local_bash","description":"Run the proof"}]}}`
				finished = `{"event_type":"system","turn":0,"event":{"type":"system","subtype":"background_tasks_changed","tasks":[]}}`
				waiting  = "The proof run is on its first tickets. I'm waiting on its completion."
			)
			appendLog(turnRequested, running, assistant("Started the proof."), `{"event_type":"harness_turn_requested","turn":2}`, assistant(waiting))
			decision, err := gate.Handle(ctx, session.HookStop, stopInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())

			appendLog(finished, `{"event_type":"harness_turn_requested","turn":3}`, assistant(waiting))
			decision, err = gate.Handle(ctx, session.HookStop, stopInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision.Decision).To(Equal("block"))
			Expect(decision.Reason).To(HavePrefix(`Rejected by the CSF reply gate. wait_without_child: "I'm waiting on its completion" waits on a background result`))
			Expect(replyRecords()[1]).To(HaveKeyWithValue("rules", ConsistOf("wait_without_child")))
		})

		It("judges a turn a background result woke from that result, not the turn before it", func() {
			appendLog(turnRequested, unvetted, assistant(agreeing),
				`{"event_type":"harness_background_result","turn":2,"task_id":"b1","status":"completed"}`, assistant("The suite passed; merged."))
			decision, err := gate.Handle(ctx, session.HookStop, stopInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
			Expect(replyRecords()).To(ConsistOf(HaveKeyWithValue("decision", sessiongate.DecisionAllow)))
		})

		It("skips a turn whose reply the log does not hold yet", func() {
			appendLog(turnRequested, unvetted)
			decision, err := gate.Handle(ctx, session.HookStop, stopInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
			Expect(replyRecords()).To(ConsistOf(And(HaveKeyWithValue("decision", sessiongate.DecisionSkip), HaveKey("reason"))))
		})
	})

	Describe("the search gate", func() {
		const turnRequested = `{"event_type":"harness_turn_requested","turn":1}`
		var (
			// The tool inputs, shaped as a run's events.jsonl records them.
			grep = map[string]any{"pattern": `mutation\.jsonl|MutationFile`, "output_mode": "content", "path": "/src"}
			rg   = map[string]any{"command": "rg -n FindShellWaits services/harness", "description": "Find the wait gate"}
			glob = map[string]any{"pattern": "**/*.go"}
			read = map[string]any{"file_path": "/src/services/harness/sessiongate/gate.go"}
		)
		// called is one tool call as the turn executor's assistant event
		// carries it into the event log.
		called := func(id string, tool string, input map[string]any) string {
			record := map[string]any{ // an event log record is an open JSON object
				"msg": "turn executor event", "turn": 1, "direction": session.DirectionOut, "event_type": session.EventTypeAssistant,
				"event": map[string]any{"type": session.EventTypeAssistant, "parent_tool_use_id": nil, "message": map[string]any{
					"role": "assistant", "content": []map[string]any{{
						"type": session.BlockToolUse, "id": id, "name": tool, "input": input, "caller": map[string]any{"type": "direct"},
					}},
				}},
			}
			encoded, err := json.Marshal(record)
			Expect(err).NotTo(HaveOccurred())
			return string(encoded)
		}
		judge := func(id string, tool string, input map[string]any) *sessiongate.HookOutput {
			encoded, err := json.Marshal(map[string]any{ // the hook input is an open JSON object
				"session_id": sessionID, "hook_event_name": session.HookPreToolUse, "tool_name": tool, "tool_use_id": id, "tool_input": input,
			})
			Expect(err).NotTo(HaveOccurred())
			decision, err := gate.Handle(ctx, session.HookPreToolUse, encoded)
			Expect(err).NotTo(HaveOccurred())
			return decision
		}
		searchRecords := func() []map[string]any {
			kept := []map[string]any{}
			for _, record := range gateRecords(directory) {
				if record["gate"] == sessiongate.GateSearch {
					kept = append(kept, record)
				}
			}
			return kept
		}

		It("allows two searches in a row and logs nothing of its own", func() {
			appendLines(directory, turnRequested, called("toolu_a", session.ToolGrep, grep))
			Expect(judge("toolu_b", session.ToolBash, rg)).To(BeNil())
			Expect(searchRecords()).To(BeEmpty())
		})

		It("denies the third search in a row, quoting each and naming csf search, and logs the denial", func() {
			appendLines(directory, turnRequested, called("toolu_a", session.ToolGrep, grep), called("toolu_b", session.ToolBash, rg),
				called("toolu_c", session.ToolGlob, glob))
			decision := judge("toolu_c", session.ToolGlob, glob)
			Expect(decision.HookSpecificOutput.HookEventName).To(Equal(session.HookPreToolUse))
			Expect(decision.HookSpecificOutput.PermissionDecision).To(Equal("deny"))
			Expect(decision.HookSpecificOutput.PermissionDecisionReason).To(Equal(`Rejected by the CSF session gate. search_chain: 3 grep/find calls in a row ` +
				`(Grep "mutation\\.jsonl|MutationFile" in "/src", Bash "rg -n FindShellWaits services/harness", Glob "**/*.go"); ` +
				`ask the index instead: csf search "<what you are looking for>" prints ranked path:line hits with why`))
			Expect(searchRecords()).To(ConsistOf(And(
				HaveKeyWithValue("decision", sessiongate.DecisionDeny),
				HaveKeyWithValue("rules", ConsistOf(string(sessiongate.RuleSearchChain))),
				HaveKeyWithValue("reason", decision.HookSpecificOutput.PermissionDecisionReason),
				HaveKeyWithValue("tool_use_id", "toolu_c"),
				HaveKeyWithValue(session.KeyTraceID, state.TraceID))))
		})

		It("counts the call being judged once whether or not the log holds it yet, and keeps denying while the chain runs", func() {
			appendLines(directory, turnRequested, called("toolu_a", session.ToolGrep, grep), called("toolu_b", session.ToolGlob, glob))
			Expect(judge("toolu_c", session.ToolGrep, grep).HookSpecificOutput.PermissionDecision).To(Equal("deny"))
			appendLines(directory, called("toolu_c", session.ToolGrep, grep))
			Expect(judge("toolu_d", session.ToolBash, rg).HookSpecificOutput.PermissionDecisionReason).To(ContainSubstring("4 grep/find calls in a row"))
		})

		It("lets any other tool call between searches break the chain", func() {
			appendLines(directory, turnRequested, called("toolu_a", session.ToolGrep, grep), called("toolu_b", "Read", read),
				called("toolu_c", session.ToolGrep, grep))
			Expect(judge("toolu_d", session.ToolGlob, glob)).To(BeNil())
		})

		It("never counts csf search, and lets it break the chain", func() {
			ask := map[string]any{"command": `csf search "where the wait gate parses bash" | head -20`}
			appendLines(directory, turnRequested, called("toolu_a", session.ToolGrep, grep), called("toolu_b", session.ToolGlob, glob))
			Expect(judge("toolu_c", session.ToolBash, ask)).To(BeNil())
			appendLines(directory, called("toolu_c", session.ToolBash, ask), called("toolu_d", session.ToolGrep, grep))
			Expect(judge("toolu_e", session.ToolBash, rg)).To(BeNil())
			Expect(searchRecords()).To(BeEmpty())
		})

		It("counts a search inside a pipeline and a nested bash -c", func() {
			nested := map[string]any{"command": `cd /src && bash -c 'git ls-files | head && rg -n foo services'`}
			appendLines(directory, turnRequested, called("toolu_a", session.ToolBash, map[string]any{"command": `find . -name '*.go' | wc -l`}),
				called("toolu_b", session.ToolGrep, grep))
			decision := judge("toolu_c", session.ToolBash, nested)
			Expect(decision.HookSpecificOutput.PermissionDecisionReason).To(ContainSubstring(
				`(Bash "find . -name '*.go' | wc -l", Grep "mutation\\.jsonl|MutationFile" in "/src", Bash "cd /src && bash -c 'git ls-files | head && rg -n foo services'")`))
		})

		It("judges only the turn running", func() {
			appendLines(directory, turnRequested, called("toolu_a", session.ToolGrep, grep), called("toolu_b", session.ToolGrep, grep),
				`{"event_type":"harness_turn_requested","turn":2}`)
			Expect(judge("toolu_c", session.ToolGrep, grep)).To(BeNil())
		})

		It("allows the call and logs why when the event log cannot be read", func() {
			appendLines(directory, turnRequested, called("toolu_a", session.ToolGrep, grep), called("toolu_b", session.ToolGrep, grep))
			events := filepath.Join(directory, session.EventsFile)
			Expect(os.Chmod(events, 0o200)).To(Succeed())
			Expect(judge("toolu_c", session.ToolGlob, glob)).To(BeNil())
			Expect(os.Chmod(events, 0o600)).To(Succeed())
			Expect(searchRecords()).To(ConsistOf(And(
				HaveKeyWithValue("decision", sessiongate.DecisionAllow), HaveKeyWithValue(session.KeyError, ContainSubstring("permission denied")))))
		})
	})

	Describe("undefined hook calls", func() {
		It("rejects malformed hook input with the defined error and logs it", func() {
			_, err := gate.Handle(ctx, session.HookPreToolUse, []byte(`{"tool_name": `))
			Expect(err).To(MatchError(sessiongate.ErrMalformedHookInput))
			Expect(gateRecords(directory)).To(ConsistOf(And(
				HaveKeyWithValue("decision", sessiongate.DecisionMalformed),
				HaveKeyWithValue(session.KeyTraceID, state.TraceID),
				HaveKey(session.KeyError))))
		})

		It("rejects a hook event it installs no gate on", func() {
			_, err := gate.Handle(ctx, "SessionStart", hookInput("SessionStart", session.ToolBash, "ls", false))
			Expect(err).To(MatchError(sessiongate.ErrUnknownEvent))
		})
	})

	Describe("the commit gate", func() {
		var httpClient *MockIHTTPClient

		commitInput := func() []byte {
			return hookInput(session.HookPostToolUse, session.ToolBash, `git add notes.txt && git commit -m "add notes"`, false)
		}
		// github answers the protocol client's requests in order, checking
		// each is the one named, and returns the bodies sent.
		github := func(replies ...[3]string) *[]string {
			sent := &[]string{}
			calls := make([]any, 0, len(replies))
			for _, reply := range replies {
				calls = append(calls, httpClient.EXPECT().Do(gomock.Any()).DoAndReturn(func(request *http.Request) (*http.Response, error) {
					defer GinkgoRecover()
					seen := request.Method + " " + request.URL.Path
					if query := request.URL.Query(); len(query) > 0 {
						seen += "?" + query.Encode()
					}
					Expect(seen).To(Equal(reply[0]))
					body := ""
					if request.Body != nil {
						content, err := io.ReadAll(request.Body)
						Expect(err).NotTo(HaveOccurred())
						body = string(content)
					}
					*sent = append(*sent, body)
					status, err := strconv.Atoi(reply[1])
					Expect(err).NotTo(HaveOccurred())
					return &http.Response{StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}}, Body: io.NopCloser(strings.NewReader(reply[2])), Request: request}, nil
				}))
			}
			gomock.InOrder(calls...)
			return sent
		}
		pushed := func(ahead string) []any {
			return []any{
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "rev-list", "--count", "main..HEAD")).Return(output(ahead), nil),
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "push", "--set-upstream", "origin", branch)).Return(output(""), nil),
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "remote", "get-url", "origin")).Return(output(remoteURL+"\n"), nil),
			}
		}
		const openPulls = "GET /repos/candacelabs/repository/pulls?head=candacelabs%3Ah1%2Fscratch&state=open"

		BeforeEach(func() {
			httpClient = NewMockIHTTPClient(controller)
			client, err := iogithub.NewGitHubClient(httpClient, "ghs_spec")
			Expect(err).NotTo(HaveOccurred())
			gate, err = sessiongate.NewSessionGate(sessiongate.WithLauncher(launcher), sessiongate.WithRunDirectory(directory), sessiongate.WithGitHub(client))
			Expect(err).NotTo(HaveOccurred())
		})

		It("ignores a command that does not commit", func() {
			decision, err := gate.Handle(ctx, session.HookPostToolUse, hookInput(session.HookPostToolUse, session.ToolBash, `git status`, false))
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
			Expect(gateRecords(directory)).To(ConsistOf(HaveKeyWithValue("decision", sessiongate.DecisionSkip)))
		})

		It("pushes the branch and opens its draft pull request, on the repository it pushed to, after the first commit", func() {
			gomock.InOrder(pushed("1\n")...)
			sent := github([3]string{openPulls, "200", "[]"}, [3]string{"POST /repos/candacelabs/repository/pulls", "201", `{"html_url":"` + pullRequest + `"}`})
			decision, err := gate.Handle(ctx, session.HookPostToolUse, commitInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision.HookSpecificOutput.HookEventName).To(Equal(session.HookPostToolUse))
			Expect(decision.HookSpecificOutput.AdditionalContext).To(ContainSubstring(pullRequest))
			var created map[string]any
			Expect(json.Unmarshal([]byte((*sent)[1]), &created)).To(Succeed())
			Expect(created).To(SatisfyAll(HaveKeyWithValue("draft", true), HaveKeyWithValue("base", baseBranch),
				HaveKeyWithValue("head", branch), HaveKeyWithValue("title", prTitle), HaveKeyWithValue("body", ContainSubstring(state.AssignmentID))))
			Expect(gateRecords(directory)).To(ConsistOf(And(
				HaveKeyWithValue("decision", sessiongate.DecisionOpened),
				HaveKeyWithValue("pull_request_url", pullRequest),
				HaveKeyWithValue(session.KeyTraceID, state.TraceID))))
		})

		It("opens the pull request against the base branch, not its remote-tracking name", func() {
			state.BaseBranch = "origin/main"
			content, err := json.Marshal(state)
			Expect(err).NotTo(HaveOccurred())
			Expect(os.WriteFile(filepath.Join(directory, session.RunStateFile), content, 0o600)).To(Succeed())
			gomock.InOrder(
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "rev-list", "--count", "origin/main..HEAD")).Return(output("1"), nil),
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "push")).Return(output(""), nil),
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "remote", "get-url", "origin")).Return(output(remoteURL+"\n"), nil),
			)
			sent := github([3]string{openPulls, "200", "[]"}, [3]string{"POST /repos/candacelabs/repository/pulls", "201", `{"html_url":"` + pullRequest + `"}`})
			_, err = gate.Handle(ctx, session.HookPostToolUse, commitInput())
			Expect(err).NotTo(HaveOccurred())
			Expect((*sent)[1]).To(ContainSubstring(`"base":"main"`))
		})

		It("opens no second pull request when one is open", func() {
			gomock.InOrder(pushed("2")...)
			github([3]string{openPulls, "200", `[{"html_url":"` + pullRequest + `"}]`})
			decision, err := gate.Handle(ctx, session.HookPostToolUse, commitInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision.HookSpecificOutput.AdditionalContext).To(ContainSubstring(pullRequest))
			Expect(gateRecords(directory)).To(ConsistOf(HaveKeyWithValue("decision", sessiongate.DecisionExists)))
		})

		It("does nothing when the commit left the branch level with its base", func() {
			launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "rev-list")).Return(output("0"), nil)
			decision, err := gate.Handle(ctx, session.HookPostToolUse, commitInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
		})

		It("reports a failed push with the defined error and tells the agent", func() {
			gomock.InOrder(
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "rev-list")).Return(output("1"), nil),
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "push")).Return(proc.Result{ExitCode: 128}, errors.New("remote rejected")),
			)
			decision, err := gate.Handle(ctx, session.HookPostToolUse, commitInput())
			Expect(err).To(MatchError(sessiongate.ErrPush))
			Expect(decision.HookSpecificOutput.AdditionalContext).To(ContainSubstring("yourself"))
			Expect(gateRecords(directory)).To(ConsistOf(And(HaveKeyWithValue("decision", sessiongate.DecisionFailed), HaveKey(session.KeyError))))
		})

		It("reports GitHub's refusal with the defined error and tells the agent", func() {
			gomock.InOrder(pushed("1")...)
			github([3]string{openPulls, "200", "[]"}, [3]string{"POST /repos/candacelabs/repository/pulls", "422", `{"message":"Validation Failed","errors":[{"message":"No commits between main and h1/scratch"}]}`})
			decision, err := gate.Handle(ctx, session.HookPostToolUse, commitInput())
			Expect(err).To(MatchError(sessiongate.ErrPullRequest))
			Expect(decision.HookSpecificOutput.AdditionalContext).To(ContainSubstring("open its draft pull request yourself"))
			Expect(gateRecords(directory)).To(ConsistOf(And(
				HaveKeyWithValue("decision", sessiongate.DecisionFailed),
				HaveKeyWithValue(session.KeyError, ContainSubstring("No commits between main and h1/scratch")))))
		})

		It("reports a push remote git cannot name with the defined error and tells the agent", func() {
			gomock.InOrder(
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "rev-list")).Return(output("1"), nil),
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "push")).Return(output(""), nil),
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "remote", "get-url", "origin")).
					Return(proc.Result{ExitCode: 2}, errors.New("error: No such remote 'origin'")),
			)
			decision, err := gate.Handle(ctx, session.HookPostToolUse, commitInput())
			Expect(err).To(MatchError(sessiongate.ErrPullRequest))
			Expect(err).To(MatchError(session.ErrPushRemote))
			Expect(decision.HookSpecificOutput.AdditionalContext).To(ContainSubstring("open its draft pull request yourself"))
			Expect(gateRecords(directory)).To(ConsistOf(And(
				HaveKeyWithValue("decision", sessiongate.DecisionFailed),
				HaveKeyWithValue(session.KeyError, ContainSubstring("No such remote")))))
		})

		It("reports a gate granted no GitHub client with the defined error and never starts gh", func() {
			bare, err := sessiongate.NewSessionGate(sessiongate.WithLauncher(launcher), sessiongate.WithRunDirectory(directory))
			Expect(err).NotTo(HaveOccurred())
			launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "rev-list")).Return(output("1"), nil)
			decision, err := bare.Handle(ctx, session.HookPostToolUse, commitInput())
			Expect(err).To(MatchError(sessiongate.ErrNoGitHub))
			Expect(decision.HookSpecificOutput.AdditionalContext).To(ContainSubstring("GITHUB_TOKEN"))
		})
	})
})
