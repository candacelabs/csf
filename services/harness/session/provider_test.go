// Copyright 2026 Candace Labs

package session_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/io/net/model"
	"github.com/candacelabs/csf/io/net/model/claudecode"
	"github.com/candacelabs/csf/services/harness/provider"
	"github.com/candacelabs/csf/services/harness/session"
	"github.com/candacelabs/csf/services/harness/session/mocks"
)

// providerBaseURL is the endpoint a spec's providers.json records: a name no
// resolver serves, so a spec that reached it would fail rather than talk to a
// provider.
const providerBaseURL = "https://provider.example.invalid"

const providerKey = "sk-router-secret"

var _ = Describe("Open through a provider", func() {
	var (
		launcher *MockILauncher
		executor *mocks.MockIOpenTurnExecutor
		state    string
		run      string
		specs    []session.TurnExecutorSpec
		ctx      context.Context
	)

	BeforeEach(func() {
		ctx = context.Background()
		controller := gomock.NewController(GinkgoT())
		launcher = NewMockILauncher(controller)
		executor = mocks.NewMockIOpenTurnExecutor(controller)
		executor.EXPECT().Close(gomock.Any()).Return(nil).AnyTimes()
		state = GinkgoT().TempDir()
		run = session.RunDirectory(state, assignmentID)
		specs = nil
	})

	// routerFor records providers.json and the key file under the state
	// directory and reads the router back, the same path serve takes: the
	// entry names the endpoint, the key file, the small model and modelMap, a
	// JSON object from CSF model names to ordered provider spellings.
	routerFor := func(modelMap string) *provider.Router {
		entry := `{"router":{"base_url":"` + providerBaseURL + `","key_file":"router.key","haiku_model":"provider/haiku","model_map":` + modelMap + `}}`
		Expect(os.WriteFile(filepath.Join(state, provider.ProvidersFile), []byte(entry), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(state, "router.key"), []byte(providerKey+"\n"), 0o600)).To(Succeed())
		router, err := provider.ReadRouter(state)
		Expect(err).NotTo(HaveOccurred())
		return router
	}

	runnerFor := func(router *provider.Router) *session.AgentSessionRunner {
		runner, err := session.NewAgentSessionRunner(
			session.WithLauncher(launcher),
			session.WithStateDirectory(state),
			session.WithGateCommand(gateBinary, gateVerb),
			session.WithProvider(router),
			session.WithOpenTurnExecutors(func(ctx context.Context, opened session.TurnExecutorSpec) (session.IOpenTurnExecutor, error) {
				specs = append(specs, opened)
				return executor, nil
			}),
		)
		Expect(err).NotTo(HaveOccurred())
		return runner
	}

	// expectWorktree, expectPushRemote and expectPullRequestLookup are the git
	// and gh calls the session makes whichever provider it launches through.
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

	// modelOf is the spelling --model names in an executor's arguments.
	modelOf := func(arguments []string) string {
		index := slices.Index(arguments, "--model")
		Expect(index).NotTo(Equal(-1))
		return arguments[index+1]
	}
	served := func() *model.Proposal[claudecode.Event] {
		return &model.Proposal[claudecode.Event]{Provider: claudecode.ProviderName}
	}
	unavailable := func() *model.Proposal[claudecode.Event] {
		return &model.Proposal[claudecode.Event]{
			Provider: claudecode.ProviderName,
			Actions: []claudecode.Event{{
				Type: claudecode.EventTypeResult,
				Raw:  json.RawMessage(`{"type":"result","is_error":true,"terminal_reason":"api_error","result":"the gateway answered 503"}`),
			}},
		}
	}

	It("launches on the provider's spelling of the CSF model with the provider's environment, and logs no key", func() {
		router := routerFor(`{"sonnet":["provider/sonnet-a","provider/sonnet-b"]}`)
		runner := runnerFor(router)
		gomock.InOrder(
			expectWorktree(),
			executor.EXPECT().Propose(gomock.Any(), gomock.Any()).Return(served(), nil),
			expectPushRemote(), expectPullRequestLookup(""),
		)
		opened, err := runner.Open(ctx, newRecipe())
		Expect(err).NotTo(HaveOccurred())
		_, err = opened.Turn(ctx, "Do the task.")
		Expect(err).NotTo(HaveOccurred())
		Expect(opened.Close(ctx)).To(Succeed())

		Expect(specs).To(HaveLen(1))
		Expect(modelOf(specs[0].Arguments)).To(Equal("provider/sonnet-a"))
		Expect(specs[0].Environment).To(ConsistOf(
			provider.EnvironmentBaseURL+"="+providerBaseURL,
			provider.EnvironmentAPIKey+"="+providerKey,
			provider.EnvironmentConfigDirectory+"="+router.ConfigDirectoryPath(),
			provider.EnvironmentMaxRetries+"=1",
			provider.EnvironmentHaikuModel+"=provider/haiku",
		))

		Expect(eventTypes(records(run))).To(ContainElement(session.EventTypeProviderApplied))
		events, err := os.ReadFile(filepath.Join(run, session.EventsFile))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(events)).NotTo(ContainSubstring(providerKey))
	})

	It("retries the turn on the next spelling when the provider cannot serve the first, and records the move", func() {
		router := routerFor(`{"sonnet":["provider/sonnet-a","provider/sonnet-b"]}`)
		runner := runnerFor(router)
		gomock.InOrder(
			expectWorktree(),
			executor.EXPECT().Propose(gomock.Any(), gomock.Any()).Return(unavailable(), nil),
			expectPushRemote(), expectPullRequestLookup(""),
			executor.EXPECT().Propose(gomock.Any(), gomock.Any()).Return(served(), nil),
			expectPushRemote(), expectPullRequestLookup(""),
		)
		opened, err := runner.Open(ctx, newRecipe())
		Expect(err).NotTo(HaveOccurred())
		_, err = opened.Turn(ctx, "Do the task.")
		Expect(err).NotTo(HaveOccurred())
		Expect(opened.Close(ctx)).To(Succeed())

		Expect(specs).To(HaveLen(2))
		Expect(modelOf(specs[0].Arguments)).To(Equal("provider/sonnet-a"))
		Expect(modelOf(specs[1].Arguments)).To(Equal("provider/sonnet-b"))
		Expect(specs[1].Environment).To(ContainElement(provider.EnvironmentAPIKey + "=" + providerKey))

		var fallback map[string]any
		for _, record := range records(run) {
			if record[session.KeyEventType] == session.EventTypeProviderFallback {
				fallback = record
			}
		}
		Expect(fallback).NotTo(BeNil())
		Expect(fallback).To(HaveKeyWithValue(session.KeyProviderModel, "provider/sonnet-a"))
		Expect(fallback).To(HaveKeyWithValue(session.KeyProviderNextModel, "provider/sonnet-b"))
		Expect(fallback).To(HaveKeyWithValue(session.KeyProviderReason, ContainSubstring("503")))
	})

	It("fails the turn when the provider has no spelling left to try", func() {
		runner := runnerFor(routerFor(`{"sonnet":["provider/sonnet-a"]}`))
		gomock.InOrder(
			expectWorktree(),
			executor.EXPECT().Propose(gomock.Any(), gomock.Any()).Return(unavailable(), nil),
			expectPushRemote(), expectPullRequestLookup(""),
		)
		opened, err := runner.Open(ctx, newRecipe())
		Expect(err).NotTo(HaveOccurred())
		_, err = opened.Turn(ctx, "Do the task.")
		Expect(err).To(MatchError(session.ErrTurnFailed))
		Expect(err).To(MatchError(session.ErrProviderUnavailable))
		Expect(opened.Close(ctx)).To(Succeed())
		Expect(specs).To(HaveLen(1))
	})

	It("leaves a Copilot session on its own provider", func() {
		runner := runnerFor(routerFor(`{"sonnet":["provider/sonnet-a","provider/sonnet-b"]}`))
		recipe := newRecipe()
		recipe.Executor = string(session.ExecutorCopilot)
		gomock.InOrder(
			expectWorktree(),
			executor.EXPECT().Propose(gomock.Any(), gomock.Any()).Return(served(), nil),
			expectPushRemote(), expectPullRequestLookup(""),
		)
		opened, err := runner.Open(ctx, recipe)
		Expect(err).NotTo(HaveOccurred())
		_, err = opened.Turn(ctx, "Do the task.")
		Expect(err).NotTo(HaveOccurred())
		Expect(opened.Close(ctx)).To(Succeed())

		Expect(specs).To(HaveLen(1))
		for _, variable := range specs[0].Environment {
			Expect(variable).NotTo(HavePrefix(provider.EnvironmentBaseURL + "="))
			Expect(variable).NotTo(HavePrefix(provider.EnvironmentAPIKey + "="))
		}
		Expect(modelOf(specs[0].Arguments)).To(Equal("sonnet"))
	})

	It("refuses a nil provider router", func() {
		_, err := session.NewAgentSessionRunner(
			session.WithLauncher(launcher),
			session.WithStateDirectory(state),
			session.WithGateCommand(gateBinary, gateVerb),
			session.WithProvider(nil),
		)
		Expect(err).To(MatchError(session.ErrInvalidOption))
	})
})
