package main

import (
	"fmt"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("CSF serve configuration", func() {
	It("uses environment values for the same flags as the binary", func() {
		environment := map[string]string{
			environmentListen:                  "127.0.0.1:19011",
			environmentWorkbenchThemeDirectory: "/workspace/theme",
			environmentEvents:                  "/state/events.jsonl",
			environmentConsumerRoot:            "/workspace/consumer",
			environmentConsumerRevision:        "reviewed-revision",
			environmentCopilotHistorySource:    "/workspace/copilot-home",
		}
		config, err := parseServeConfig("serve", nil, func(name string) (string, bool) {
			value, found := environment[name]
			return value, found
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(config.listen).To(Equal("127.0.0.1:19011"))
		Expect(config.workbenchThemeDirectory).To(Equal("/workspace/theme"))
		Expect(config.events).To(Equal("/state/events.jsonl"))
		Expect(config.consumerRoot).To(Equal("/workspace/consumer"))
		Expect(config.consumerRevision).To(Equal("reviewed-revision"))
		Expect(config.copilotHistorySource).To(Equal("/workspace/copilot-home"))
		Expect(config.searchURL).To(Equal(defaultServeSearchURL))
	})

	It("gives explicit flags precedence over environment values", func() {
		config, err := parseServeConfig("serve", []string{"--listen", "127.0.0.1:19012", "--workbench-theme-dir", "/flag/theme"}, func(name string) (string, bool) {
			environment := map[string]string{
				environmentListen:                  "127.0.0.1:19011",
				environmentWorkbenchThemeDirectory: "/environment/theme",
			}
			value, found := environment[name]
			return value, found
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(config.listen).To(Equal("127.0.0.1:19012"))
		Expect(config.workbenchThemeDirectory).To(Equal("/flag/theme"))
	})

	It("validates knowledge settings after flag precedence is applied", func() {
		config, err := parseServeConfig("serve", []string{"--search-url", defaultServeSearchURL}, func(name string) (string, bool) {
			if name == environmentSearchURL {
				return "http://search.example.invalid", true
			}
			return "", false
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(config.searchURL).To(Equal(defaultServeSearchURL))
	})

	It("rejects a source watch without retained receipt storage", func() {
		_, err := parseServeConfig("serve", nil, func(name string) (string, bool) {
			if name == environmentWatchRoot {
				return "/workspace/consumer", true
			}
			return "", false
		})
		Expect(err).To(MatchError(fmt.Sprintf("%s requires %s", environmentWatchRoot, environmentReceipts)))
	})

	It("rejects a Workbench database without its repository", func() {
		_, err := parseServeConfig("serve", nil, func(name string) (string, bool) {
			if name == environmentWorkbenchDatabase {
				return "/private/workbench.json", true
			}
			return "", false
		})
		Expect(err).To(MatchError(fmt.Sprintf("%s requires %s", environmentWorkbenchDatabase, environmentWorkbenchRepository)))
	})

	It("rejects child settings that the selected runtime would otherwise ignore", func() {
		for _, environment := range []string{
			environmentWorkbenchRepository,
			environmentWorkbenchWorktrees,
			environmentWorkbenchUI,
			environmentWorkbenchToken,
			environmentWorkbenchTraceConfig,
			environmentLocalSimulationConfig,
			environmentSimulationConfig,
			environmentConsumerRevision,
		} {
			_, err := parseServeConfig("serve", nil, func(name string) (string, bool) {
				if name == environment {
					return "/configured", true
				}
				return "", false
			})
			Expect(err).To(MatchError(ContainSubstring("require")))
		}
	})

	It("rejects a non-default knowledge setting without the knowledge store", func() {
		_, err := parseServeConfig("serve", nil, func(name string) (string, bool) {
			if name == environmentSearchURL {
				return "http://search.example.invalid", true
			}
			return "", false
		})
		Expect(err).To(MatchError(fmt.Sprintf("knowledge settings require %s", environmentDatabaseConfig)))
	})

	It("allows the documented default index in a minimal environment", func() {
		config, err := parseServeConfig("serve", nil, func(name string) (string, bool) {
			if name == environmentSearchIndex {
				return defaultServeSearchIndex, true
			}
			return "", false
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(config.searchIndex).To(Equal(defaultServeSearchIndex))
	})

	It("allows simulation trace export with local simulation and the knowledge store", func() {
		config, err := parseServeConfig("serve", nil, func(name string) (string, bool) {
			environment := map[string]string{
				environmentDatabaseConfig:        "/private/database.json",
				environmentLocalSimulationConfig: "/private/local-simulation.json",
				environmentWorkbenchTraceConfig:  "/private/trace.json",
			}
			value, found := environment[name]
			return value, found
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(config.workbenchTraceConfig).To(Equal("/private/trace.json"))
	})
})
