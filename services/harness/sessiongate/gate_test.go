// Copyright 2026 Candace Labs

package sessiongate_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/ipc/proc"
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
	pullRequest = "https://github.com/example/repository/pull/9"
	remoteURL   = "https://github.com/example/repository.git"
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

		It("refuses a directory with no recorded run", func() {
			empty, err := sessiongate.NewSessionGate(sessiongate.WithLauncher(launcher), sessiongate.WithRunDirectory(GinkgoT().TempDir()))
			Expect(err).NotTo(HaveOccurred())
			_, err = empty.Handle(ctx, session.HookPreToolUse, hookInput(session.HookPreToolUse, session.ToolBash, "ls", false))
			Expect(err).To(MatchError(session.ErrNoRunState))
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
				ContainSubstring("run_in_background"), ContainSubstring("Monitor")))
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
		readyInput := func(command string) []byte {
			return hookInput(session.HookPreToolUse, session.ToolBash, command, false)
		}
		checkMerge := func() gomock.Matcher { return command("bash", "tools/check-merge.sh") }

		It("runs the merge path's checks in the worktree and allows a pull request that passes", func() {
			launcher.EXPECT().Run(gomock.Any(), checkMerge()).Return(output("check-merge: passed\n"), nil)
			decision, err := gate.Handle(ctx, session.HookPreToolUse, readyInput(`gh pr ready 16`))
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
			Expect(gateRecords(directory)).To(ContainElement(And(
				HaveKeyWithValue("gate", sessiongate.GateReady), HaveKeyWithValue("decision", sessiongate.DecisionAllow))))
		})

		It("denies marking ready on a consistency regression and quotes the report", func() {
			launcher.EXPECT().Run(gomock.Any(), checkMerge()).Return(
				proc.Result{ExitCode: 1, Stdout: []byte("| cs-16 | 48 | 49 | +1 |\n"), Stderr: []byte("check-merge: REFUSED: ontology-ratchet\n")},
				errors.New("ipc/proc: bash: exit status 1"))
			decision, err := gate.Handle(ctx, session.HookPreToolUse, readyInput(`bash -c 'gh pr ready 16'`))
			Expect(err).NotTo(HaveOccurred())
			Expect(decision.HookSpecificOutput.PermissionDecision).To(Equal("deny"))
			Expect(decision.HookSpecificOutput.PermissionDecisionReason).To(And(
				ContainSubstring("| cs-16 | 48 | 49 | +1 |"), ContainSubstring("REFUSED: ontology-ratchet")))
			Expect(gateRecords(directory)).To(ContainElement(And(
				HaveKeyWithValue("gate", sessiongate.GateReady), HaveKeyWithValue("decision", sessiongate.DecisionDeny),
				HaveKey(session.KeyError))))
		})

		DescribeTable("leaves other commands to the wait gate alone",
			func(text string) {
				decision, err := gate.Handle(ctx, session.HookPreToolUse, readyInput(text))
				Expect(err).NotTo(HaveOccurred())
				Expect(decision).To(BeNil())
				Expect(gateRecords(directory)).To(ConsistOf(HaveKeyWithValue("gate", sessiongate.GateWait)))
			},
			Entry("returning a pull request to draft", `gh pr ready 16 --undo`),
			Entry("another gh pr verb", `gh pr view 16`),
			Entry("the word ready in text", `echo "gh pr ready"`),
		)
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
		commitInput := func() []byte {
			return hookInput(session.HookPostToolUse, session.ToolBash, `git add notes.txt && git commit -m "add notes"`, false)
		}

		It("ignores a command that does not commit", func() {
			decision, err := gate.Handle(ctx, session.HookPostToolUse, hookInput(session.HookPostToolUse, session.ToolBash, `git status`, false))
			Expect(err).NotTo(HaveOccurred())
			Expect(decision).To(BeNil())
			Expect(gateRecords(directory)).To(ConsistOf(HaveKeyWithValue("decision", sessiongate.DecisionSkip)))
		})

		It("pushes the branch and opens its draft pull request after the first commit", func() {
			gomock.InOrder(
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "rev-list", "--count", "main..HEAD")).Return(output("1\n"), nil),
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "push", "--set-upstream", "origin", branch)).Return(output(""), nil),
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "remote", "get-url", "origin")).Return(output(remoteURL+"\n"), nil),
				launcher.EXPECT().Run(gomock.Any(), command("gh", "pr", "list", "--repo", remoteURL, "--head", branch, "--state", "open")).Return(output("\n"), nil),
				launcher.EXPECT().Run(gomock.Any(), command("gh", "pr", "create", "--repo", remoteURL, "--draft", "--base", baseBranch, "--head", branch, "--title", prTitle)).
					Return(output("Creating draft pull request\n"+pullRequest+"\n"), nil),
			)
			decision, err := gate.Handle(ctx, session.HookPostToolUse, commitInput())
			Expect(err).NotTo(HaveOccurred())
			Expect(decision.HookSpecificOutput.HookEventName).To(Equal(session.HookPostToolUse))
			Expect(decision.HookSpecificOutput.AdditionalContext).To(ContainSubstring(pullRequest))
			Expect(gateRecords(directory)).To(ConsistOf(And(
				HaveKeyWithValue("decision", sessiongate.DecisionOpened),
				HaveKeyWithValue("pull_request_url", pullRequest),
				HaveKeyWithValue(session.KeyTraceID, state.TraceID))))
		})

		It("opens no second pull request when one is open", func() {
			gomock.InOrder(
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "rev-list")).Return(output("2"), nil),
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "push")).Return(output(""), nil),
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "remote", "get-url", "origin")).Return(output(remoteURL+"\n"), nil),
				launcher.EXPECT().Run(gomock.Any(), command("gh", "pr", "list")).Return(output(pullRequest+"\n"), nil),
			)
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

		It("reports a gh failure with the defined error and tells the agent", func() {
			gomock.InOrder(
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "rev-list")).Return(output("1"), nil),
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "push")).Return(output(""), nil),
				launcher.EXPECT().Run(gomock.Any(), command("git", "-C", worktree, "remote", "get-url", "origin")).Return(output(remoteURL+"\n"), nil),
				launcher.EXPECT().Run(gomock.Any(), command("gh", "pr", "list")).Return(output(""), nil),
				launcher.EXPECT().Run(gomock.Any(), command("gh", "pr", "create")).Return(proc.Result{ExitCode: 1}, errors.New("gh: not a GitHub repository")),
			)
			decision, err := gate.Handle(ctx, session.HookPostToolUse, commitInput())
			Expect(err).To(MatchError(sessiongate.ErrPullRequest))
			Expect(decision.HookSpecificOutput.AdditionalContext).To(ContainSubstring("open its draft pull request yourself"))
			Expect(gateRecords(directory)).To(ConsistOf(And(
				HaveKeyWithValue("decision", sessiongate.DecisionFailed),
				HaveKeyWithValue(session.KeyError, ContainSubstring("not a GitHub repository")))))
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

		It("starts every gh child in the run's worktree, on the repository the branch was pushed to", func() {
			var started []proc.Command
			launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, launched proc.Command) (proc.Result, error) {
				started = append(started, launched)
				switch {
				case slices.Contains(launched.Arguments, "rev-list"):
					return output("1\n"), nil
				case slices.Contains(launched.Arguments, "get-url"):
					return output(remoteURL + "\n"), nil
				case slices.Contains(launched.Arguments, "create"):
					return output(pullRequest + "\n"), nil
				}
				return output(""), nil
			}).Times(5)
			_, err := gate.Handle(ctx, session.HookPostToolUse, commitInput())
			Expect(err).NotTo(HaveOccurred())
			var gh []proc.Command
			for _, launched := range started {
				if launched.Executable == "gh" {
					gh = append(gh, launched)
				}
			}
			Expect(gh).To(HaveLen(2))
			for _, launched := range gh {
				Expect(launched.Directory).To(Equal(worktree))
				Expect(launched.Arguments[2:4]).To(Equal([]string{"--repo", remoteURL}))
			}
		})
	})
})
