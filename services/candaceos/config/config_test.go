package config_test

import (
	"strconv"
	"strings"
	"time"

	configlib "github.com/candacelabs/csf/pkg/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos/config"
)

const (
	fixtureDatabaseURL      = "postgres://example.invalid/candaceos"
	fixtureGitHubToken      = "copilot-token"
	fixtureOllamaURL        = "http://127.0.0.1:11434"
	fixtureOllamaModel      = "qwen3:8b"
	fixtureOpenCodeModel    = "openrouter/openai/gpt-5.4-nano"
	fixtureOpenCodePassword = "local-secret"
	invalidSyntax           = "not-valid"
	invalidURL              = "://invalid"
)

type environmentSetting struct {
	name  string
	value string
}

func setting(name, value string) environmentSetting {
	return environmentSetting{name: name, value: value}
}

func loader(settings ...environmentSetting) config.Loader {
	values := make(map[string]string, len(settings))
	for _, configured := range settings {
		values[configured.name] = configured.value
	}
	return config.NewLoader(configlib.NewEnvironment(func(name string) (string, bool) {
		value, found := values[name]
		return value, found
	}))
}

func loadBackend(
	backend string,
	settings ...environmentSetting,
) (*candaceosv1.CoreConfig, error) {
	base := []environmentSetting{
		setting(config.EnvironmentHarnessBackend, backend),
		setting(config.EnvironmentCoreDatabaseURL, fixtureDatabaseURL),
	}
	return loader(append(base, settings...)...).Load()
}

func parsedDuration(value string) time.Duration {
	duration, err := time.ParseDuration(value)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return duration
}

func parsedInt32(value string) int32 {
	integer, err := strconv.ParseInt(value, 10, 32)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return int32(integer)
}

var _ = Describe("Core configuration", func() {
	It("loads Candacefile defaults for the demo profile", func() {
		resolved, err := loadBackend(config.ProfileDemoHarnessBackend)
		Expect(err).NotTo(HaveOccurred())

		Expect(resolved.GetHarnessBackend()).To(Equal(candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO))
		Expect(resolved.GetBind()).To(Equal(config.DefaultCoreBind))
		Expect(resolved.GetDataDir()).To(Equal(config.DefaultCoreDataDir))
		Expect(resolved.GetWorkspace()).To(Equal(config.DefaultCoreWorkspace))
		Expect(resolved.GetWardenUrl()).To(Equal(config.DefaultCoreWardenURL))
		Expect(resolved.GetAgentPort()).To(Equal(parsedInt32(config.DefaultCoreAgentPort)))
		Expect(config.ApprovalTimeout(resolved)).To(Equal(parsedDuration(config.DefaultCoreApprovalTimeout)))
		Expect(config.FleetPollInterval(resolved)).To(Equal(parsedDuration(config.DefaultCoreFleetPollInterval)))
		Expect(resolved.GetCopilotCli()).To(BeEmpty())
		Expect(resolved.GetOllama()).To(BeNil())
		Expect(resolved.GetOpencode()).To(BeNil())
	})

	It("parses overrides and projects deterministic copied node labels", func() {
		const (
			configuredBind        = "0.0.0.0:7781"
			configuredAgentPort   = "8095"
			configuredApproval    = "4m"
			configuredFleetPoll   = "3s"
			configuredNodeLabels  = `{"node-b":{"region":"east"},"node-a":{"region":"west"}}`
			configuredGitHubAlias = "unused-token"
		)
		resolved, err := loadBackend(
			config.ProfileCopilotHarnessBackend,
			setting(config.EnvironmentCoreBind, configuredBind),
			setting(config.EnvironmentCoreNodeLabels, configuredNodeLabels),
			setting(config.EnvironmentCoreAgentPort, configuredAgentPort),
			setting(config.EnvironmentCoreApprovalTimeout, configuredApproval),
			setting(config.EnvironmentCoreFleetPollInterval, configuredFleetPoll),
			setting(config.EnvironmentCopilotGitHubToken, fixtureGitHubToken),
			setting(config.EnvironmentGHToken, configuredGitHubAlias),
			setting(config.EnvironmentGitHubToken, configuredGitHubAlias),
		)
		Expect(err).NotTo(HaveOccurred())

		Expect(resolved.GetBind()).To(Equal(configuredBind))
		Expect(resolved.GetAgentPort()).To(Equal(parsedInt32(configuredAgentPort)))
		Expect(resolved.GetGithubToken()).To(Equal(fixtureGitHubToken))
		Expect(config.ApprovalTimeout(resolved)).To(Equal(parsedDuration(configuredApproval)))
		Expect(config.FleetPollInterval(resolved)).To(Equal(parsedDuration(configuredFleetPoll)))
		Expect([]string{
			resolved.GetNodeLabels()[0].GetId(),
			resolved.GetNodeLabels()[1].GetId(),
		}).To(Equal([]string{"node-a", "node-b"}))

		projected := config.NodeLabels(resolved)
		Expect(projected).To(Equal(map[string]map[string]string{
			"node-a": {"region": "west"},
			"node-b": {"region": "east"},
		}))
		projected["node-a"]["region"] = "changed"
		Expect(resolved.GetNodeLabels()[0].GetLabels()["region"]).To(Equal("west"))
	})

	DescribeTable("labels malformed environment input with its generated name",
		func(configured environmentSetting) {
			_, err := loadBackend(config.ProfileDemoHarnessBackend, configured)
			Expect(err).To(MatchError(ContainSubstring(configured.name)))
		},
		Entry("duration", setting(config.EnvironmentCoreApprovalTimeout, invalidSyntax)),
		Entry("integer", setting(config.EnvironmentCoreAgentPort, invalidSyntax)),
		Entry("node labels", setting(config.EnvironmentCoreNodeLabels, invalidSyntax)),
	)

	DescribeTable("keeps operating-system and cross-field validation at the adapter",
		func(mutate func(config *candaceosv1.CoreConfig), environmentName string) {
			resolved := validConfig()
			mutate(resolved)
			Expect(config.Validate(resolved)).To(MatchError(ContainSubstring(environmentName)))
		},
		Entry("data directory", func(resolved *candaceosv1.CoreConfig) {
			resolved.DataDir = "relative-data"
		}, config.EnvironmentCoreDataDir),
		Entry("workspace", func(resolved *candaceosv1.CoreConfig) {
			resolved.Workspace = "relative-workspace"
		}, config.EnvironmentCoreWorkspace),
		Entry("Warden URL", func(resolved *candaceosv1.CoreConfig) {
			resolved.WardenUrl = invalidURL
		}, config.EnvironmentCoreWardenURL),
		Entry("agent URL", func(resolved *candaceosv1.CoreConfig) {
			resolved.AgentUrl = invalidURL
		}, config.EnvironmentCoreAgentURL),
		Entry("bind", func(resolved *candaceosv1.CoreConfig) {
			resolved.Bind = invalidSyntax
		}, config.EnvironmentCoreBind),
		Entry("empty label set", func(resolved *candaceosv1.CoreConfig) {
			resolved.NodeLabels = []*candaceosv1.Node{{Id: config.DefaultAgentNodeID}}
		}, config.EnvironmentCoreNodeLabels),
		Entry("duplicate node", func(resolved *candaceosv1.CoreConfig) {
			resolved.NodeLabels = []*candaceosv1.Node{
				{Id: config.DefaultAgentNodeID, Labels: map[string]string{"region": "west"}},
				{Id: config.DefaultAgentNodeID, Labels: map[string]string{"region": "east"}},
			}
		}, config.EnvironmentCoreNodeLabels),
		Entry("Copilot executable", func(resolved *candaceosv1.CoreConfig) {
			resolved.HarnessBackend = candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI
			resolved.CopilotCli = ""
		}, config.EnvironmentCopilotCLI),
		Entry("Copilot URL", func(resolved *candaceosv1.CoreConfig) {
			resolved.HarnessBackend = candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI
			resolved.CopilotUrl = invalidURL
		}, config.EnvironmentCopilotURL),
		Entry("Copilot connection token", func(resolved *candaceosv1.CoreConfig) {
			resolved.HarnessBackend = candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI
			resolved.CopilotUrl = config.ProfileCopilotCopilotURL
		}, config.EnvironmentCopilotConnectionToken),
	)

	Describe("backend selection", func() {
		It("uses the generated default", func() {
			resolved, err := loader(
				setting(config.EnvironmentCoreDatabaseURL, fixtureDatabaseURL),
			).Load()
			Expect(err).NotTo(HaveOccurred())
			Expect(config.HarnessBackendName(resolved.GetHarnessBackend())).To(Equal(config.DefaultHarnessBackend))
		})

		DescribeTable("normalizes legacy profiles only at the environment boundary",
			func(legacy string, expected candaceosv1.HarnessBackend) {
				resolved, err := loader(
					setting(config.EnvironmentLegacyMode, legacy),
					setting(config.EnvironmentCoreDatabaseURL, fixtureDatabaseURL),
				).Load()
				Expect(err).NotTo(HaveOccurred())
				Expect(resolved.GetHarnessBackend()).To(Equal(expected))
			},
			Entry("demo", config.ProfileDemo, candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO),
			Entry("Copilot", config.ProfileCopilot, candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI),
		)

		It("accepts matching canonical and legacy selectors", func() {
			resolved, err := loadBackend(
				config.ProfileCopilotHarnessBackend,
				setting(config.EnvironmentLegacyMode, config.ProfileCopilot),
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(resolved.GetHarnessBackend()).To(Equal(candaceosv1.HarnessBackend_HARNESS_BACKEND_COPILOT_CLI))
		})

		It("rejects unknown, embedded, and conflicting environment selectors", func() {
			_, err := loadBackend(invalidSyntax)
			Expect(err).To(MatchError(And(
				ContainSubstring(config.EnvironmentHarnessBackend),
				ContainSubstring(strconv.Quote(config.ProfileCopilotHarnessBackend)),
				ContainSubstring(strconv.Quote(config.ProfileDemoHarnessBackend)),
				ContainSubstring(strconv.Quote(config.HarnessBackendName(candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA))),
				ContainSubstring(strconv.Quote(config.ProfileOpenCodeHarnessBackend)),
			)))

			_, err = loadBackend(config.HarnessBackendName(candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED))
			Expect(err).To(HaveOccurred())

			_, err = loadBackend(
				config.ProfileDemoHarnessBackend,
				setting(config.EnvironmentLegacyMode, config.ProfileCopilot),
			)
			Expect(err).To(MatchError(config.EnvironmentHarnessBackend + " conflicts with legacy " + config.EnvironmentLegacyMode))
		})

		It("keeps a compiled-in implementation authoritative", func() {
			resolved, err := loader(
				setting(config.EnvironmentHarnessBackend, invalidSyntax),
				setting(config.EnvironmentCoreDatabaseURL, fixtureDatabaseURL),
			).LoadForHarness(candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED)
			Expect(err).NotTo(HaveOccurred())
			Expect(resolved.GetHarnessBackend()).To(Equal(candaceosv1.HarnessBackend_HARNESS_BACKEND_EMBEDDED))
			Expect(resolved.GetCopilotCli()).To(BeEmpty())
			Expect(resolved.GetOllama()).To(BeNil())
			Expect(resolved.GetOpencode()).To(BeNil())
		})
	})

	Describe("provider-specific policy", func() {
		It("loads Ollama only when selected", func() {
			modelDigest := strings.Repeat("a", 64)
			resolved, err := loadBackend(
				config.HarnessBackendName(candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA),
				setting(config.EnvironmentOllamaURL, fixtureOllamaURL),
				setting(config.EnvironmentOllamaModel, fixtureOllamaModel),
				setting(config.EnvironmentOllamaModelDigest, modelDigest),
			)
			Expect(err).NotTo(HaveOccurred())

			ollama := resolved.GetOllama()
			Expect(ollama.GetUrl()).To(Equal(fixtureOllamaURL))
			Expect(ollama.GetModel()).To(Equal(fixtureOllamaModel))
			Expect(ollama.GetModelDigest()).To(Equal(modelDigest))
			Expect(ollama.GetContextTokens()).To(Equal(parsedInt32(config.DefaultOllamaContextTokens)))
			Expect(ollama.GetMaxToolCalls()).To(Equal(parsedInt32(config.DefaultOllamaMaxToolCalls)))
			Expect(time.Duration(ollama.GetTurnTimeout())).To(Equal(parsedDuration(config.DefaultOllamaTurnTimeout)))
			Expect(resolved.GetOpencode()).To(BeNil())
			Expect(resolved.GetCopilotCli()).To(BeEmpty())
		})

		DescribeTable("labels missing Ollama contract fields",
			func(settings []environmentSetting, environmentName string) {
				_, err := loadBackend(
					config.HarnessBackendName(candaceosv1.HarnessBackend_HARNESS_BACKEND_OLLAMA),
					settings...,
				)
				Expect(err).To(MatchError(ContainSubstring(environmentName)))
			},
			Entry("URL", []environmentSetting{
				setting(config.EnvironmentOllamaModel, fixtureOllamaModel),
				setting(config.EnvironmentOllamaModelDigest, strings.Repeat("a", 64)),
			}, config.EnvironmentOllamaURL),
			Entry("model", []environmentSetting{
				setting(config.EnvironmentOllamaURL, fixtureOllamaURL),
				setting(config.EnvironmentOllamaModelDigest, strings.Repeat("a", 64)),
			}, config.EnvironmentOllamaModel),
			Entry("digest", []environmentSetting{
				setting(config.EnvironmentOllamaURL, fixtureOllamaURL),
				setting(config.EnvironmentOllamaModel, fixtureOllamaModel),
			}, config.EnvironmentOllamaModelDigest),
		)

		It("loads a bounded private OpenCode contract", func() {
			const (
				configuredUsername       = "candace"
				configuredSession        = "ses_exact"
				configuredRequestTimeout = "7s"
				configuredPollInterval   = "250ms"
				configuredQueueCapacity  = "12"
			)
			resolved, err := loadBackend(
				config.ProfileOpenCodeHarnessBackend,
				setting(config.EnvironmentOpenCodeURL, config.ProfileOpenCodeOpenCodeURL),
				setting(config.EnvironmentOpenCodeUsername, configuredUsername),
				setting(config.EnvironmentOpenCodePassword, fixtureOpenCodePassword),
				setting(config.EnvironmentOpenCodeSessionID, configuredSession),
				setting(config.EnvironmentOpenCodeRequestTimeout, configuredRequestTimeout),
				setting(config.EnvironmentOpenCodePollInterval, configuredPollInterval),
				setting(config.EnvironmentOpenCodeQueueCapacity, configuredQueueCapacity),
				setting(config.EnvironmentOpenCodeModel, fixtureOpenCodeModel),
			)
			Expect(err).NotTo(HaveOccurred())

			openCode := resolved.GetOpencode()
			Expect(openCode.GetUrl()).To(Equal(config.ProfileOpenCodeOpenCodeURL))
			Expect(openCode.GetUsername()).To(Equal(configuredUsername))
			Expect(openCode.GetPassword()).To(Equal(fixtureOpenCodePassword))
			Expect(openCode.GetSessionId()).To(Equal(configuredSession))
			Expect(time.Duration(openCode.GetRequestTimeout())).To(Equal(parsedDuration(configuredRequestTimeout)))
			Expect(time.Duration(openCode.GetPollInterval())).To(Equal(parsedDuration(configuredPollInterval)))
			Expect(openCode.GetQueueCapacity()).To(Equal(parsedInt32(configuredQueueCapacity)))
			Expect(openCode.GetModel()).To(Equal(fixtureOpenCodeModel))
			Expect(resolved.GetOllama()).To(BeNil())
			Expect(resolved.GetCopilotCli()).To(BeEmpty())
		})

		It("uses generated OpenCode defaults", func() {
			resolved, err := loadBackend(
				config.ProfileOpenCodeHarnessBackend,
				setting(config.EnvironmentOpenCodePassword, fixtureOpenCodePassword),
				setting(config.EnvironmentOpenCodeModel, fixtureOpenCodeModel),
			)
			Expect(err).NotTo(HaveOccurred())

			openCode := resolved.GetOpencode()
			Expect(openCode.GetUrl()).To(Equal(config.DefaultOpenCodeURL))
			Expect(openCode.GetUsername()).To(Equal(config.DefaultOpenCodeUsername))
			Expect(time.Duration(openCode.GetRequestTimeout())).To(Equal(parsedDuration(config.DefaultOpenCodeRequestTimeout)))
			Expect(time.Duration(openCode.GetPollInterval())).To(Equal(parsedDuration(config.DefaultOpenCodePollInterval)))
			Expect(openCode.GetQueueCapacity()).To(Equal(parsedInt32(config.DefaultOpenCodeQueueCapacity)))
		})

		DescribeTable("rejects invalid selected OpenCode policy",
			func(settings []environmentSetting, message string) {
				_, err := loadBackend(config.ProfileOpenCodeHarnessBackend, settings...)
				Expect(err).To(MatchError(ContainSubstring(message)))
			},
			Entry("password", []environmentSetting{
				setting(config.EnvironmentOpenCodeModel, fixtureOpenCodeModel),
			}, config.EnvironmentOpenCodePassword),
			Entry("model", []environmentSetting{
				setting(config.EnvironmentOpenCodePassword, fixtureOpenCodePassword),
			}, config.EnvironmentOpenCodeModel),
			Entry("public origin", []environmentSetting{
				setting(config.EnvironmentOpenCodeURL, "https://opencode.example.com"),
				setting(config.EnvironmentOpenCodePassword, fixtureOpenCodePassword),
				setting(config.EnvironmentOpenCodeModel, fixtureOpenCodeModel),
			}, "must target a private host"),
			Entry("poll interval", []environmentSetting{
				setting(config.EnvironmentOpenCodePassword, fixtureOpenCodePassword),
				setting(config.EnvironmentOpenCodeModel, fixtureOpenCodeModel),
				setting(config.EnvironmentOpenCodePollInterval, "20ms"),
			}, config.EnvironmentOpenCodePollInterval),
			Entry("typed syntax", []environmentSetting{
				setting(config.EnvironmentOpenCodePassword, fixtureOpenCodePassword),
				setting(config.EnvironmentOpenCodeModel, fixtureOpenCodeModel),
				setting(config.EnvironmentOpenCodeRequestTimeout, invalidSyntax),
			}, config.EnvironmentOpenCodeRequestTimeout),
		)

		It("does not parse policy for unselected providers", func() {
			resolved, err := loadBackend(
				config.ProfileDemoHarnessBackend,
				setting(config.EnvironmentOllamaContextTokens, invalidSyntax),
				setting(config.EnvironmentOllamaTurnTimeout, invalidSyntax),
				setting(config.EnvironmentOpenCodeURL, "https://public.example.com/path"),
				setting(config.EnvironmentOpenCodeRequestTimeout, invalidSyntax),
				setting(config.EnvironmentOpenCodeQueueCapacity, invalidSyntax),
				setting(config.EnvironmentCopilotURL, invalidSyntax),
			)
			Expect(err).NotTo(HaveOccurred())
			Expect(resolved.GetOllama()).To(BeNil())
			Expect(resolved.GetOpencode()).To(BeNil())
			Expect(resolved.GetCopilotUrl()).To(BeEmpty())
		})
	})
})

func validConfig() *candaceosv1.CoreConfig {
	return &candaceosv1.CoreConfig{
		HarnessBackend:  candaceosv1.HarnessBackend_HARNESS_BACKEND_DEMO,
		Bind:            config.DefaultCoreBind,
		DataDir:         config.DefaultCoreDataDir,
		Workspace:       config.DefaultCoreWorkspace,
		DatabaseUrl:     fixtureDatabaseURL,
		WardenUrl:       config.DefaultCoreWardenURL,
		AgentPort:       parsedInt32(config.DefaultCoreAgentPort),
		ApprovalTimeout: int64(parsedDuration(config.DefaultCoreApprovalTimeout)),
		FleetPollInterval: &candaceosv1.PersistenceTiming{
			FleetPollIntervalNanoseconds: int64(parsedDuration(config.DefaultCoreFleetPollInterval)),
		},
	}
}
