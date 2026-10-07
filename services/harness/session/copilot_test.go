// Copyright 2026 Candace Labs

package session_test

import (
	"context"
	"io"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/proc"
	pb "github.com/candacelabs/csf/proto/candace/brainspine/v1"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/session/mocks"
)

const (
	copilotBinary = "/opt/copilot/index.js"
	mcpURL        = "http://127.0.0.1:14120/mcp"
)

// copilotRecipe is the shared recipe run on Copilot, with an MCP tool among
// its rules.
func copilotRecipe() *pb.AgentAssignmentRecipe {
	recipe := newRecipe()
	recipe.Executor = string(session.ExecutorCopilot)
	recipe.Model = "claude-haiku-4.5"
	recipe.Workspace.AllowedTools = []string{"Bash", "Edit", "mcp__csf__GetWorkbenchTheme"}
	return recipe
}

var _ = Describe("A recipe that chooses Copilot", func() {
	var (
		ctx      context.Context
		launcher *MockILauncher
		router   *mocks.MockIRouter
		runner   *session.AgentSessionRunner
		state    string
		run      string
		routed   session.RouteRequest
		turns    []proc.Command
		prompts  []string
	)

	BeforeEach(func() {
		ctx = context.Background()
		controller := gomock.NewController(GinkgoT())
		launcher = NewMockILauncher(controller)
		router = mocks.NewMockIRouter(controller)
		turns, prompts = nil, nil
		// The worktree, its hooks path, its initial commit and the push remote.
		launcher.EXPECT().Run(gomock.Any(), launched("git")).Return(proc.Result{Stdout: []byte(remoteURL)}, nil).AnyTimes()
		launcher.EXPECT().Run(gomock.Any(), launched("gh")).Return(proc.Result{Stdout: []byte(pullRequest)}, nil).AnyTimes()
		// Copilot itself: one process per turn, answering with its result.
		launcher.EXPECT().Run(gomock.Any(), launched(copilotBinary)).DoAndReturn(func(ctx context.Context, command proc.Command) (proc.Result, error) {
			turns = append(turns, command)
			input, err := io.ReadAll(command.Stdin)
			Expect(err).NotTo(HaveOccurred())
			prompts = append(prompts, string(input))
			_, err = io.WriteString(command.Stdout, `{"type":"result","sessionId":"`+argumentAfter(command.Arguments, "--session-id")+`","exitCode":0,"usage":{"sessionDurationMs":10}}`+"\n")
			return proc.Result{}, err
		}).AnyTimes()
		router.EXPECT().Route(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, request session.RouteRequest) (string, error) {
			routed = request
			return request.Session, nil
		}).AnyTimes()
		state = GinkgoT().TempDir()
		run = session.RunDirectory(state, assignmentID)
		var err error
		runner, err = session.NewAgentSessionRunner(
			session.WithLauncher(launcher),
			session.WithStateDirectory(state),
			session.WithGateCommand(gateBinary, gateVerb),
			session.WithCopilotExecutable(copilotBinary),
			session.WithMCPServer(mcpURL),
			session.WithRouter(router),
		)
		Expect(err).NotTo(HaveOccurred())
	})

	It("runs its turns on the Copilot CLI with the gates, the translated allowlist, the csf MCP server and the instructions", func() {
		opened, err := runner.Open(ctx, copilotRecipe())
		Expect(err).NotTo(HaveOccurred())
		receipt, err := opened.Turn(ctx, "Add one line.")
		Expect(err).NotTo(HaveOccurred())
		Expect(opened.Close(ctx)).To(Succeed())

		Expect(turns).To(HaveLen(1))
		command := turns[0]
		plugin := filepath.Join(run, "copilot", "plugin")
		instructionsDirectory := filepath.Join(run, "copilot", "instructions")
		Expect(command.Directory).To(Equal(filepath.Join(run, session.WorktreeDirectory)))
		Expect(command.Arguments).To(Equal([]string{
			"--session-id", receipt.GetSessionId(), "--output-format", "json", "--no-auto-update",
			"--usage-output-file", filepath.Join(run, "copilot", "usage.json"),
			"--model", "claude-haiku-4.5", "--plugin-dir", plugin, "--no-ask-user", "--disable-builtin-mcps",
			"--additional-mcp-config", `{"mcpServers":{"csf":{"type":"http","url":"` + mcpURL + `","headers":{"X-CSF-Assignment":"` + assignmentID + `"},"tools":["*"]}}}`,
			"--allow-all-paths",
			"--available-tools", "bash", "read_bash", "write_bash", "stop_bash", "list_bash", "edit", "csf-GetWorkbenchTheme",
			"--allow-tool", "shell", "write", "csf(GetWorkbenchTheme)",
		}))
		Expect(command.ExtraEnvironment).To(ContainElement("COPILOT_CUSTOM_INSTRUCTIONS_DIRS=" + instructionsDirectory))
		Expect(prompts).To(ConsistOf(HavePrefix("Add one line.")))

		settings, err := os.ReadFile(filepath.Join(run, session.SettingsFile))
		Expect(err).NotTo(HaveOccurred())
		hooks, err := os.ReadFile(filepath.Join(plugin, "hooks", "hooks.json"))
		Expect(err).NotTo(HaveOccurred())
		Expect(hooks).To(Equal(settings), "Copilot runs the very gates a Claude Code session runs")
		Expect(filepath.Join(plugin, ".claude-plugin", "plugin.json")).To(BeARegularFile())
		agent, err := os.ReadFile(filepath.Join(instructionsDirectory, ".github", "instructions", "agent.instructions.md"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(agent)).To(Equal("---\napplyTo: \"**\"\n---\n" + instructions + "\n"))

		Expect(receipt.GetExecutor()).To(Equal("copilot"))
		recorded, err := session.ReadRunState(run)
		Expect(err).NotTo(HaveOccurred())
		Expect(recorded.TurnExecutor()).To(Equal(session.ExecutorCopilot))
		Expect(records(run)[0]).To(HaveKeyWithValue(session.KeyExecutor, "copilot"))
	})

	It("is routed under an identity that names Copilot, so it never attaches a Claude Code conversation", func() {
		opened, err := runner.Open(ctx, copilotRecipe())
		Expect(err).NotTo(HaveOccurred())
		Expect(opened.Close(ctx)).To(Succeed())

		Expect(routed.Agent).To(Equal("copilot:scratch/" + instructionsHash))
	})

	It("resumes the conversation on a reopened run under the same session identifier", func() {
		opened, err := runner.Open(ctx, copilotRecipe())
		Expect(err).NotTo(HaveOccurred())
		_, err = opened.Turn(ctx, "First.")
		Expect(err).NotTo(HaveOccurred())
		Expect(opened.Close(ctx)).To(Succeed())

		reopened, err := runner.Reopen(ctx, copilotRecipe())
		Expect(err).NotTo(HaveOccurred())
		_, err = reopened.Turn(ctx, "Second.")
		Expect(err).NotTo(HaveOccurred())
		Expect(reopened.Close(ctx)).To(Succeed())

		Expect(turns).To(HaveLen(2))
		Expect(argumentAfter(turns[1].Arguments, "--session-id")).To(Equal(argumentAfter(turns[0].Arguments, "--session-id")))
	})

	DescribeTable("refuses a recipe it cannot run faithfully, before creating anything",
		func(change func(recipe *pb.AgentAssignmentRecipe)) {
			recipe := copilotRecipe()
			change(recipe)
			_, err := runner.Open(ctx, recipe)
			Expect(err).To(MatchError(session.ErrInvalidRecipe))
			Expect(run).NotTo(BeADirectory())
		},
		Entry("a tool rule with no Copilot equivalent", func(recipe *pb.AgentAssignmentRecipe) {
			recipe.Workspace.AllowedTools = append(recipe.Workspace.AllowedTools, "WebSearch")
		}),
		Entry("an executor the harness does not run", func(recipe *pb.AgentAssignmentRecipe) { recipe.Executor = "opencode" }),
	)

	It("leaves a recipe without an executor on Claude Code", func() {
		Expect(session.ExecutorOf(newRecipe())).To(Equal(session.ExecutorClaudeCode))
		Expect((&session.RunState{}).TurnExecutor()).To(Equal(session.ExecutorClaudeCode), "a run recorded before executors were chosen")
	})
})

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
