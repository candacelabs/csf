// Copyright 2026 Candace Labs

package session_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	"github.com/candacelabs/csf/csf"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/pkg/telemetry"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/session/mocks"
)

const (
	assignmentID = "0caf2d51-1ee7-468a-882f-ac478a437640"
	repository   = "/repository"
	branch       = "h1/scratch"
	baseBranch   = "main"
	instructions = "Work in the worktree. Commit once."
	pullRequest  = "https://github.com/candacelabs/repository/pull/9"
	remoteURL    = "https://github.com/candacelabs/repository.git"
	gateBinary   = "/opt/csf/harness"
	gateVerb     = "gate"
)

func newRecipe() *pb.AgentAssignmentRecipe {
	return &pb.AgentAssignmentRecipe{
		AssignmentId: assignmentID,
		Agent:        &pb.AgentDefinition{Id: "scratch", Revision: 1, DisplayName: "Scratch", Instructions: instructions},
		TicketUrl:    "https://example.invalid/issues/1",
		Task:         "Add one line to notes.txt and commit it.",
		Model:        "sonnet",
		RepositoryId: "scratch",
		Workspace: &pb.AgentWorkspace{
			RepositoryPath: repository, BaseBranch: baseBranch, Branch: branch,
			AllowedTools: []string{"Bash", "Edit"}, PullRequestTitle: "H1 scratch change",
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

// records decodes the run's event log.
func records(directory string) []map[string]any {
	file, err := os.Open(filepath.Join(directory, session.EventsFile))
	Expect(err).NotTo(HaveOccurred())
	defer func() { Expect(file.Close()).To(Succeed()) }()
	var decoded []map[string]any
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 1<<16), 1<<20)
	for scanner.Scan() {
		// A slog JSON record is an object of unknown shape.
		record := map[string]any{}
		Expect(json.Unmarshal(scanner.Bytes(), &record)).To(Succeed())
		decoded = append(decoded, record)
	}
	Expect(scanner.Err()).NotTo(HaveOccurred())
	return decoded
}

func eventTypes(decoded []map[string]any) []string {
	types := []string{}
	for _, record := range decoded {
		types = append(types, record[session.KeyEventType].(string))
	}
	return types
}

// promptOf is the text of a turn's single user message.
func promptOf(turn *claudecode.Turn) string {
	Expect(turn.Messages).To(HaveLen(1))
	var message struct {
		Type    string `json:"type"`
		Message struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"message"`
	}
	Expect(json.Unmarshal(turn.Messages[0], &message)).To(Succeed())
	Expect(message.Type).To(Equal("user"))
	return message.Message.Content
}

var _ = Describe("AgentSessionRunner", func() {
	var (
		controller *gomock.Controller
		launcher   *MockILauncher
		executor   *mocks.MockITurnExecutor
		state      string
		specs      []session.TurnExecutorSpec
		runner     *session.AgentSessionRunner
		ctx        context.Context
		recipe     *pb.AgentAssignmentRecipe
		run        string
	)

	BeforeEach(func() {
		ctx = context.Background()
		controller = gomock.NewController(GinkgoT())
		launcher = NewMockILauncher(controller)
		executor = mocks.NewMockITurnExecutor(controller)
		state = GinkgoT().TempDir()
		specs = nil
		recipe = newRecipe()
		run = session.RunDirectory(state, assignmentID)
		var err error
		runner, err = session.NewAgentSessionRunner(
			session.WithLauncher(launcher),
			session.WithStateDirectory(state),
			session.WithGateCommand(gateBinary, gateVerb),
			session.WithTurnExecutors(func(spec session.TurnExecutorSpec) (session.ITurnExecutor, error) {
				specs = append(specs, spec)
				return executor, nil
			}),
		)
		Expect(err).NotTo(HaveOccurred())
	})

	// expectWorktree expects the worktree, the repository's per-worktree
	// config, the worktree's own hooks path and its initial commit, in that
	// order, and returns the last of them.
	expectWorktree := func() *gomock.Call {
		worktree := filepath.Join(run, session.WorktreeDirectory)
		add := launcher.EXPECT().Run(gomock.Any(), launched("git", "-C", repository, "worktree", "add", "-b", branch,
			worktree, baseBranch)).Return(proc.Result{}, nil)
		perWorktree := launcher.EXPECT().Run(gomock.Any(), launched("git", "-C", repository, "config", "extensions.worktreeConfig",
			"true")).Return(proc.Result{}, nil).After(add)
		hooks := launcher.EXPECT().Run(gomock.Any(), launched("git", "-C", worktree, "config", "--worktree", "core.hooksPath",
			filepath.Join(run, "hooks"))).Return(proc.Result{}, nil).After(perWorktree)
		return launcher.EXPECT().Run(gomock.Any(), launched("git", "-C", worktree, "commit", "--allow-empty")).
			Return(proc.Result{}, nil).After(hooks)
	}
	expectPushRemote := func() *gomock.Call {
		return launcher.EXPECT().Run(gomock.Any(), launched("git", "-C", filepath.Join(run, session.WorktreeDirectory), "remote", "get-url", "origin")).
			Return(proc.Result{Stdout: []byte(remoteURL + "\n")}, nil)
	}
	expectPullRequestLookup := func(url string) *gomock.Call {
		return launcher.EXPECT().Run(gomock.Any(), launched("gh", "pr", "list", "--repo", remoteURL, "--head", branch)).
			Return(proc.Result{Stdout: []byte(url + "\n")}, nil)
	}
	proposal := &model.Proposal[claudecode.Event]{Provider: claudecode.ProviderName}

	Describe("construction", func() {
		DescribeTable("rejects an incomplete option set with the defined error",
			func(expected error, options ...session.AgentSessionRunnerOption) {
				_, err := session.NewAgentSessionRunner(options...)
				Expect(err).To(MatchError(expected))
			},
			Entry("no launcher", session.ErrNoLauncher, session.WithStateDirectory("/state"), session.WithGateCommand(gateBinary)),
			Entry("a nil launcher", session.ErrNoLauncher, session.WithLauncher(nil)),
			Entry("a relative state directory", session.ErrNoStateDirectory, session.WithStateDirectory("state")),
			Entry("an empty gate command", session.ErrNoGateCommand, session.WithGateCommand()),
			Entry("a nil option", session.ErrInvalidOption, nil),
			Entry("a nil turn executor factory", session.ErrInvalidOption, session.WithTurnExecutors(nil)),
			Entry("an empty Claude Code executable", session.ErrInvalidOption, session.WithClaudeExecutable("")),
			Entry("an empty MCP server URL", session.ErrInvalidOption, session.WithMCPServer("")),
		)

		It("requires the state directory", func() {
			_, err := session.NewAgentSessionRunner(session.WithLauncher(launcher), session.WithGateCommand(gateBinary))
			Expect(err).To(MatchError(session.ErrNoStateDirectory))
		})

		It("requires the gate command", func() {
			_, err := session.NewAgentSessionRunner(session.WithLauncher(launcher), session.WithStateDirectory(state))
			Expect(err).To(MatchError(session.ErrNoGateCommand))
		})
	})

	Describe("Run", func() {
		It("gives every turn the host's MCP server when it was granted one", func() {
			var err error
			runner, err = session.NewAgentSessionRunner(
				session.WithLauncher(launcher),
				session.WithStateDirectory(state),
				session.WithGateCommand(gateBinary, gateVerb),
				session.WithMCPServer("http://127.0.0.1:14120/mcp"),
				session.WithTurnExecutors(func(spec session.TurnExecutorSpec) (session.ITurnExecutor, error) {
					specs = append(specs, spec)
					return executor, nil
				}),
			)
			Expect(err).NotTo(HaveOccurred())
			gomock.InOrder(expectWorktree(), executor.EXPECT().Propose(gomock.Any(), gomock.Any()).Return(proposal, nil),
				expectPushRemote(), expectPullRequestLookup(pullRequest))
			_, err = runner.Run(ctx, recipe)
			Expect(err).NotTo(HaveOccurred())
			Expect(specs).To(HaveLen(1))
			Expect(specs[0].Arguments[len(specs[0].Arguments)-2:]).To(Equal([]string{
				"--mcp-config", `{"mcpServers":{"csf":{"type":"http","url":"http://127.0.0.1:14120/mcp","headers":{"X-CSF-Assignment":"` + recipe.GetAssignmentId() + `"}}}}`,
			}))
		})

		It("starts the session in its own worktree with the gates installed and returns the receipt", func() {
			var turnTrace string
			gomock.InOrder(
				expectWorktree(),
				executor.EXPECT().Propose(gomock.Any(), gomock.Any()).DoAndReturn(
					func(ctx context.Context, turn *claudecode.Turn) (*model.Proposal[claudecode.Event], error) {
						trace, ok := telemetry.TraceFromContext(ctx)
						Expect(ok).To(BeTrue())
						turnTrace = trace.GetTraceId()
						Expect(promptOf(turn)).To(And(ContainSubstring("Assignment: "+assignmentID), ContainSubstring(recipe.GetTask())))
						return proposal, nil
					}),
				expectPushRemote(), expectPullRequestLookup(pullRequest),
			)
			receipt, err := runner.Run(ctx, recipe)
			Expect(err).NotTo(HaveOccurred())

			plan, err := csf.PrepareAgentAssignment(recipe)
			Expect(err).NotTo(HaveOccurred())
			Expect(proto.Equal(receipt.GetPlan(), plan)).To(BeTrue())
			Expect(receipt.GetSessionId()).To(Equal(plan.GetSessionKey()))
			Expect(receipt.GetTurnId()).To(Equal("1"))
			Expect(receipt.GetWorktreeId()).To(Equal(filepath.Join(run, session.WorktreeDirectory)))
			Expect(receipt.GetBranch()).To(Equal(branch))
			Expect(receipt.GetPullRequestUrl()).To(Equal(pullRequest))
			Expect(receipt.GetTraceId()).To(MatchRegexp(`^[0-9a-f]{32}$`))
			Expect(turnTrace).To(Equal(receipt.GetTraceId()))

			Expect(specs).To(HaveLen(1))
			Expect(specs[0].Session.String()).To(Equal(plan.GetSessionKey()))
			Expect(specs[0].Directory).To(Equal(receipt.GetWorktreeId()))
			Expect(specs[0].Resume).To(BeFalse())
			Expect(specs[0].Arguments).To(Equal([]string{
				"--settings", filepath.Join(run, session.SettingsFile),
				"--model", "sonnet",
				"--permission-mode", "dontAsk",
				"--append-system-prompt", instructions,
				"--allowedTools", "Bash,Edit",
				"--strict-mcp-config",
			}), "no MCP server but csf's own reaches a session (#654)")

			settings, err := os.ReadFile(filepath.Join(run, session.SettingsFile))
			Expect(err).NotTo(HaveOccurred())
			var hooks struct {
				Hooks map[string][]struct {
					Matcher string `json:"matcher"`
					Hooks   []struct {
						Type    string `json:"type"`
						Command string `json:"command"`
					} `json:"hooks"`
				} `json:"hooks"`
			}
			Expect(json.Unmarshal(settings, &hooks)).To(Succeed())
			Expect(hooks.Hooks).To(HaveKey(session.HookPreToolUse))
			Expect(hooks.Hooks).To(HaveKey(session.HookPostToolUse))
			Expect(hooks.Hooks).To(HaveKey(session.HookStop))
			for event, matchers := range hooks.Hooks {
				Expect(matchers).To(HaveLen(1))
				switch event {
				case session.HookStop:
					Expect(matchers[0].Matcher).To(BeEmpty(), "Stop fires on every stop")
				case session.HookPreToolUse:
					Expect(matchers[0].Matcher).To(Equal(session.ToolBash + "|" + session.ToolAskUserQuestion + "|" + session.ToolReadyPullRequest + "|" + session.ToolGrep + "|" + session.ToolGlob))
				default:
					Expect(matchers[0].Matcher).To(Equal(session.ToolBash))
				}
				Expect(matchers[0].Hooks[0].Type).To(Equal("command"))
				Expect(matchers[0].Hooks[0].Command).To(Equal(strings.Join([]string{gateBinary, gateVerb, event, run}, " ")))
			}

			recorded, err := session.ReadRunState(run)
			Expect(err).NotTo(HaveOccurred())
			Expect(recorded.Turns).To(Equal(1))
			Expect(recorded.TraceID).To(Equal(receipt.GetTraceId()))

			logged := records(run)
			Expect(eventTypes(logged)).To(Equal([]string{
				session.EventTypeRunStarted, session.EventTypeWorktreeReady, session.EventTypeTurnRequested, session.EventTypeRunFinished,
			}))
			for _, record := range logged {
				Expect(record).To(HaveKeyWithValue(session.KeyTraceID, receipt.GetTraceId()))
				Expect(record).To(HaveKeyWithValue(session.KeySessionID, plan.GetSessionKey()))
				Expect(record[session.KeySpanID]).To(MatchRegexp(`^[0-9a-f]{16}$`))
				Expect(record).To(HaveKey(session.KeyTurn))
				Expect(record).To(HaveKey(session.KeySequence))
				Expect(record).To(HaveKey(session.KeyElapsed))
			}
		})

		It("logs every Claude Code event under the run's trace on the default turn executor", func() {
			plan, err := csf.PrepareAgentAssignment(recipe)
			Expect(err).NotTo(HaveOccurred())
			sessionID := plan.GetSessionKey()
			claude, err := session.NewAgentSessionRunner(
				session.WithLauncher(launcher),
				session.WithStateDirectory(state),
				session.WithGateCommand(gateBinary, gateVerb),
				session.WithClaudeExecutable("claude-test"),
			)
			Expect(err).NotTo(HaveOccurred())
			gomock.InOrder(
				expectWorktree(),
				launcher.EXPECT().Run(gomock.Any(), launched("claude-test", "-p")).DoAndReturn(
					func(ctx context.Context, command proc.Command) (proc.Result, error) {
						Expect(command.Directory).To(Equal(filepath.Join(run, session.WorktreeDirectory)))
						Expect(command.Arguments).To(ContainElements("--session-id", sessionID, "--settings", "--allowedTools"))
						_, err := io.ReadAll(command.Stdin)
						Expect(err).NotTo(HaveOccurred())
						for _, line := range []string{
							`{"type":"system","subtype":"init","session_id":"` + sessionID + `"}`,
							`{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"done"}]},"session_id":"` + sessionID + `"}`,
							`{"type":"result","subtype":"success","is_error":false,"session_id":"` + sessionID + `"}`,
						} {
							_, err := io.WriteString(command.Stdout, line+"\n")
							Expect(err).NotTo(HaveOccurred())
						}
						return proc.Result{}, nil
					}),
				expectPushRemote(), expectPullRequestLookup(""),
			)
			receipt, err := claude.Run(ctx, recipe)
			Expect(err).NotTo(HaveOccurred())
			Expect(receipt.GetPullRequestUrl()).To(BeEmpty())
			var forwarded []string
			for _, record := range records(run) {
				Expect(record).To(HaveKeyWithValue(session.KeyTraceID, receipt.GetTraceId()))
				if record["msg"] == "turn executor event" && record["direction"] == "out" {
					forwarded = append(forwarded, record[session.KeyEventType].(string))
					Expect(record).To(HaveKeyWithValue(session.KeySessionID, sessionID))
					Expect(record).To(HaveKeyWithValue(session.KeyTurn, BeNumerically("==", 1)))
					Expect(record).To(HaveKey(session.KeySequence))
					Expect(record).To(HaveKey(session.KeyElapsed))
					Expect(record[session.KeySpanID]).To(MatchRegexp(`^[0-9a-f]{16}$`))
				}
			}
			Expect(forwarded).To(Equal([]string{"system", "assistant", "result"}))
		})

		DescribeTable("rejects a recipe the harness cannot run",
			func(edit func(recipe *pb.AgentAssignmentRecipe)) {
				edit(recipe)
				_, err := runner.Run(ctx, recipe)
				Expect(err).To(MatchError(session.ErrInvalidRecipe))
			},
			Entry("no workspace", func(recipe *pb.AgentAssignmentRecipe) { recipe.Workspace = nil }),
			Entry("no allowed tools", func(recipe *pb.AgentAssignmentRecipe) { recipe.Workspace.AllowedTools = nil }),
			Entry("a blank tool", func(recipe *pb.AgentAssignmentRecipe) { recipe.Workspace.AllowedTools = []string{" "} }),
			Entry("tools joined by a comma", func(recipe *pb.AgentAssignmentRecipe) { recipe.Workspace.AllowedTools = []string{"Bash,Edit"} }),
			Entry("the base as the work branch", func(recipe *pb.AgentAssignmentRecipe) { recipe.Workspace.Branch = baseBranch }),
			Entry("a relative repository", func(recipe *pb.AgentAssignmentRecipe) { recipe.Workspace.RepositoryPath = "repository" }),
			Entry("a branch that is an option", func(recipe *pb.AgentAssignmentRecipe) { recipe.Workspace.Branch = "-delete" }),
			Entry("no pull request title", func(recipe *pb.AgentAssignmentRecipe) { recipe.Workspace.PullRequestTitle = "" }),
			Entry("no task", func(recipe *pb.AgentAssignmentRecipe) { recipe.Task = "" }),
			Entry("a malformed assignment", func(recipe *pb.AgentAssignmentRecipe) { recipe.AssignmentId = "not-a-uuid" }),
		)

		It("refuses a worktree path it did not record", func() {
			Expect(os.MkdirAll(filepath.Join(run, session.WorktreeDirectory), 0o700)).To(Succeed())
			_, err := runner.Run(ctx, recipe)
			Expect(err).To(MatchError(session.ErrWorktreeCollision))
		})

		It("refuses to start a second session for the same assignment", func() {
			gomock.InOrder(expectWorktree(), executor.EXPECT().Propose(gomock.Any(), gomock.Any()).Return(proposal, nil), expectPushRemote(), expectPullRequestLookup(""))
			_, err := runner.Run(ctx, recipe)
			Expect(err).NotTo(HaveOccurred())
			_, err = runner.Run(ctx, recipe)
			Expect(err).To(MatchError(session.ErrSessionExists))
		})

		It("reports a worktree git could not create and records no run", func() {
			launcher.EXPECT().Run(gomock.Any(), launched("git")).Return(proc.Result{ExitCode: 128}, errors.New("fatal: a branch named 'h1/scratch' already exists"))
			_, err := runner.Run(ctx, recipe)
			Expect(err).To(MatchError(session.ErrWorktree))
			_, err = session.ReadRunState(run)
			Expect(err).To(MatchError(session.ErrNoRunState))
		})

		It("returns the receipt with the defined error when the turn fails mid-turn", func() {
			failure := &claudecode.TurnError{Turn: 1, Err: claudecode.ErrTurnIncomplete}
			gomock.InOrder(expectWorktree(), executor.EXPECT().Propose(gomock.Any(), gomock.Any()).Return(nil, failure), expectPushRemote(), expectPullRequestLookup(""))
			receipt, err := runner.Run(ctx, recipe)
			Expect(err).To(MatchError(session.ErrTurnFailed))
			Expect(err).To(MatchError(claudecode.ErrTurnIncomplete))
			Expect(receipt.GetSessionId()).NotTo(BeEmpty())
			Expect(receipt.GetWorktreeId()).To(Equal(filepath.Join(run, session.WorktreeDirectory)))
			last := records(run)[len(records(run))-1]
			Expect(last).To(HaveKeyWithValue(session.KeyEventType, session.EventTypeRunFinished))
			Expect(last).To(HaveKeyWithValue(session.KeyError, ContainSubstring("result")))
		})

		It("still returns the receipt when the pull request lookup fails", func() {
			gomock.InOrder(
				expectWorktree(),
				executor.EXPECT().Propose(gomock.Any(), gomock.Any()).Return(proposal, nil),
				expectPushRemote(),
				launcher.EXPECT().Run(gomock.Any(), launched("gh")).Return(proc.Result{ExitCode: 1}, errors.New("gh: no remote")),
			)
			receipt, err := runner.Run(ctx, recipe)
			Expect(err).NotTo(HaveOccurred())
			Expect(receipt.GetPullRequestUrl()).To(BeEmpty())
		})
	})

	Describe("Resume", func() {
		It("runs a follow-up turn on the same session, worktree and trace", func() {
			gomock.InOrder(
				expectWorktree(),
				executor.EXPECT().Propose(gomock.Any(), gomock.Any()).Return(proposal, nil),
				expectPushRemote(), expectPullRequestLookup(""),
				executor.EXPECT().Propose(gomock.Any(), gomock.Any()).DoAndReturn(
					func(ctx context.Context, turn *claudecode.Turn) (*model.Proposal[claudecode.Event], error) {
						Expect(promptOf(turn)).To(Equal("Address the review."))
						return proposal, nil
					}),
				expectPushRemote(), expectPullRequestLookup(pullRequest),
			)
			first, err := runner.Run(ctx, recipe)
			Expect(err).NotTo(HaveOccurred())
			second, err := runner.Resume(ctx, recipe, "Address the review.")
			Expect(err).NotTo(HaveOccurred())
			Expect(second.GetSessionId()).To(Equal(first.GetSessionId()))
			Expect(second.GetTraceId()).To(Equal(first.GetTraceId()))
			Expect(second.GetTurnId()).To(Equal("2"))
			Expect(second.GetPullRequestUrl()).To(Equal(pullRequest))
			Expect(specs).To(HaveLen(2))
			Expect(specs[1].Resume).To(BeTrue())
			Expect(specs[1].Session).To(Equal(specs[0].Session))
		})

		It("refuses an assignment that was never run", func() {
			_, err := runner.Resume(ctx, recipe, "continue")
			Expect(err).To(MatchError(session.ErrNoRunState))
		})

		It("refuses an empty prompt", func() {
			_, err := runner.Resume(ctx, recipe, "  ")
			Expect(err).To(MatchError(session.ErrNoPrompt))
		})

		It("refuses a recipe that is not the one the run recorded", func() {
			gomock.InOrder(expectWorktree(), executor.EXPECT().Propose(gomock.Any(), gomock.Any()).Return(proposal, nil), expectPushRemote(), expectPullRequestLookup(""))
			_, err := runner.Run(ctx, recipe)
			Expect(err).NotTo(HaveOccurred())
			recipe.Workspace.Branch = "h1/other"
			_, err = runner.Resume(ctx, recipe, "continue")
			Expect(err).To(MatchError(session.ErrRunMismatch))
		})
	})
})

var _ = Describe("AgentSessionRunner in a real git worktree", func() {
	It("opens the session, whose .git is a file, and every agent commit carries the trailers", func() {
		ctx := context.Background()
		host, err := proc.NewHostLauncher()
		Expect(err).NotTo(HaveOccurred())
		git := func(directory string, arguments ...string) string {
			result, err := host.Run(ctx, proc.Command{Executable: "git", Arguments: append([]string{"-C", directory}, arguments...), Directory: directory})
			Expect(err).NotTo(HaveOccurred())
			return string(result.Stdout)
		}
		repo := GinkgoT().TempDir()
		git(repo, "init", "--initial-branch", baseBranch)
		git(repo, "config", "user.name", "Spec")
		git(repo, "config", "user.email", "spec@example.invalid")
		git(repo, "commit", "--allow-empty", "-m", "base")

		state := GinkgoT().TempDir()
		executor := mocks.NewMockITurnExecutor(gomock.NewController(GinkgoT()))
		runner, err := session.NewAgentSessionRunner(
			session.WithLauncher(host),
			session.WithStateDirectory(state),
			session.WithGateCommand(gateBinary, gateVerb),
			session.WithTurnExecutors(func(spec session.TurnExecutorSpec) (session.ITurnExecutor, error) { return executor, nil }),
		)
		Expect(err).NotTo(HaveOccurred())
		recipe := newRecipe()
		recipe.Workspace.RepositoryPath = repo
		worktree := filepath.Join(session.RunDirectory(state, assignmentID), session.WorktreeDirectory)
		executor.EXPECT().Propose(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, turn *claudecode.Turn) (*model.Proposal[claudecode.Event], error) {
				git(worktree, "commit", "--allow-empty", "-m", "agent change")
				return &model.Proposal[claudecode.Event]{Provider: claudecode.ProviderName}, nil
			})

		receipt, err := runner.Run(ctx, recipe)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt.GetWorktreeId()).To(Equal(worktree))
		dotGit, err := os.Lstat(filepath.Join(worktree, ".git"))
		Expect(err).NotTo(HaveOccurred())
		Expect(dotGit.Mode().IsRegular()).To(BeTrue())
		Expect(git(worktree, "log", "-1", "--format=%B")).To(And(
			ContainSubstring("CSF-Session: "+assignmentID),
			ContainSubstring("CSF-Turn: 1"),
			ContainSubstring("CSF-Model: sonnet"),
		))
	})

	It("sets core.hooksPath per worktree, not in the shared config", func() {
		ctx := context.Background()
		host, err := proc.NewHostLauncher()
		Expect(err).NotTo(HaveOccurred())
		git := func(directory string, arguments ...string) string {
			result, err := host.Run(ctx, proc.Command{Executable: "git", Arguments: append([]string{"-C", directory}, arguments...), Directory: directory})
			Expect(err).NotTo(HaveOccurred())
			return strings.TrimSpace(string(result.Stdout))
		}
		repo := GinkgoT().TempDir()
		git(repo, "init", "--initial-branch", baseBranch)
		git(repo, "config", "user.name", "Spec")
		git(repo, "config", "user.email", "spec@example.invalid")
		git(repo, "commit", "--allow-empty", "-m", "base")

		state := GinkgoT().TempDir()
		executor := mocks.NewMockITurnExecutor(gomock.NewController(GinkgoT()))
		runner, err := session.NewAgentSessionRunner(
			session.WithLauncher(host),
			session.WithStateDirectory(state),
			session.WithGateCommand(gateBinary, gateVerb),
			session.WithTurnExecutors(func(spec session.TurnExecutorSpec) (session.ITurnExecutor, error) { return executor, nil }),
		)
		Expect(err).NotTo(HaveOccurred())

		recipe1 := newRecipe()
		recipe1.Workspace.RepositoryPath = repo
		worktree1 := filepath.Join(session.RunDirectory(state, recipe1.AssignmentId), session.WorktreeDirectory)
		executor.EXPECT().Propose(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, turn *claudecode.Turn) (*model.Proposal[claudecode.Event], error) {
				git(worktree1, "commit", "--allow-empty", "-m", "agent change")
				return &model.Proposal[claudecode.Event]{Provider: claudecode.ProviderName}, nil
			})

		receipt1, err := runner.Run(ctx, recipe1)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt1.GetWorktreeId()).To(Equal(worktree1))

		recipe2 := newRecipe()
		recipe2.Workspace.RepositoryPath = repo
		recipe2.AssignmentId = "5b0f1c7e-3d2a-4e6b-9a41-8c7d2e9f6a13"
		recipe2.Workspace.Branch = "h1/scratch-two"
		worktree2 := filepath.Join(session.RunDirectory(state, recipe2.AssignmentId), session.WorktreeDirectory)
		executor.EXPECT().Propose(gomock.Any(), gomock.Any()).DoAndReturn(
			func(ctx context.Context, turn *claudecode.Turn) (*model.Proposal[claudecode.Event], error) {
				git(worktree2, "commit", "--allow-empty", "-m", "agent change")
				return &model.Proposal[claudecode.Event]{Provider: claudecode.ProviderName}, nil
			})

		receipt2, err := runner.Run(ctx, recipe2)
		Expect(err).NotTo(HaveOccurred())
		Expect(receipt2.GetWorktreeId()).To(Equal(worktree2))

		hooksPath1 := git(worktree1, "config", "--show-scope", "--get", "core.hooksPath")
		hooksPath2 := git(worktree2, "config", "--show-scope", "--get", "core.hooksPath")

		Expect(hooksPath1).To(ContainSubstring("worktree"))
		Expect(hooksPath2).To(ContainSubstring("worktree"))
		Expect(hooksPath1).NotTo(Equal(hooksPath2))

		Expect(strings.ToLower(git(repo, "config", "--local", "--list"))).NotTo(ContainSubstring("core.hookspath"),
			"the shared config holds no hooks path")
	})
})
