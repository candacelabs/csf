// Copyright 2026 Candace Labs

package provider

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestProvider(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "services/harness/provider Suite")
}

// writeFile writes content with mode, creating no parent: every spec starts in
// its own temporary directory.
func writeFile(path string, content string, mode os.FileMode) {
	Expect(os.WriteFile(path, []byte(content), mode)).To(Succeed())
}

var _ = Describe("Provider", func() {
	var directory string
	BeforeEach(func() { directory = GinkgoT().TempDir() })

	Describe("NewRouter", func() {
		valid := func() RouterEntry {
			return RouterEntry{
				BaseURL:    "https://provider.example.invalid",
				KeyFile:    "router.key",
				HaikuModel: "anthropic/claude-haiku-4-5",
				ModelMap: map[string][]string{
					"claude-opus-5-5": {"anthropic/claude-opus-5.5", "anthropic/claude-opus-5"},
				},
			}
		}

		It("builds a router from a complete entry", func() {
			router, err := NewRouter(valid(), directory)
			Expect(err).NotTo(HaveOccurred())
			Expect(router.BaseURL()).To(Equal("https://provider.example.invalid"))
			Expect(router.ConfigDirectoryPath()).To(Equal(filepath.Join(directory, ConfigDirectory)))
		})

		DescribeTable("rejects an entry that cannot serve a turn",
			func(mutate func(entry *RouterEntry)) {
				entry := valid()
				mutate(&entry)
				_, err := NewRouter(entry, directory)
				Expect(err).To(MatchError(ContainSubstring(ErrInvalidRouter.Error())))
			},
			Entry("no base url", func(entry *RouterEntry) { entry.BaseURL = " " }),
			Entry("no key file", func(entry *RouterEntry) { entry.KeyFile = "" }),
			Entry("no model map", func(entry *RouterEntry) { entry.ModelMap = nil }),
			Entry("a model with no spelling", func(entry *RouterEntry) { entry.ModelMap["claude-opus-5-5"] = []string{"  "} }),
		)

		It("trims the spellings and drops the blank ones", func() {
			entry := valid()
			entry.ModelMap["claude-opus-5-5"] = []string{" anthropic/a ", " ", "anthropic/b"}
			router, err := NewRouter(entry, directory)
			Expect(err).NotTo(HaveOccurred())
			Expect(router.Spellings("claude-opus-5-5")).To(Equal([]string{"anthropic/a", "anthropic/b"}))
		})
	})

	Describe("ReadRouter", func() {
		write := func(content string) {
			writeFile(filepath.Join(directory, ProvidersFile), content, 0o600)
		}

		It("reports no router when the file is absent", func() {
			_, err := ReadRouter(directory)
			Expect(err).To(MatchError(ContainSubstring(ErrNoRouter.Error())))
		})

		It("reports no router when the file carries no router entry", func() {
			write(`{"burst": {"cap_usd": 5}}`)
			_, err := ReadRouter(directory)
			Expect(err).To(MatchError(ContainSubstring(ErrNoRouter.Error())))
		})

		It("reads the router entry, leaving other top-level keys to other readers", func() {
			write(`{"burst": {"cap_usd": 5}, "router": {"base_url": "https://provider.example.invalid", "key_file": "router.key", "haiku_model": "h", "model_map": {"m": ["p"]}}}`)
			router, err := ReadRouter(directory)
			Expect(err).NotTo(HaveOccurred())
			Expect(router.BaseURL()).To(Equal("https://provider.example.invalid"))
			Expect(router.Spellings("m")).To(Equal([]string{"p"}))
		})

		It("refuses a router entry that is not valid JSON", func() {
			write(`{"router": `)
			_, err := ReadRouter(directory)
			Expect(err).To(MatchError(ContainSubstring(ErrInvalidRouter.Error())))
		})

		It("refuses a providers file anyone can read", func() {
			write(`{"router": {"base_url": "b", "key_file": "k", "model_map": {"m": ["p"]}}}`)
			Expect(os.Chmod(filepath.Join(directory, ProvidersFile), 0o644)).To(Succeed())
			_, err := ReadRouter(directory)
			Expect(err).To(MatchError(ContainSubstring(ErrInvalidRouter.Error())))
		})
	})

	Describe("Spellings and Attempts", func() {
		var router *Router
		BeforeEach(func() {
			var err error
			router, err = NewRouter(RouterEntry{
				BaseURL: "https://provider.example.invalid", KeyFile: "router.key",
				ModelMap: map[string][]string{"claude-opus-5-5": {"anthropic/claude-opus-5.5", "anthropic/claude-opus-5"}},
			}, directory)
			Expect(err).NotTo(HaveOccurred())
		})

		It("returns the mapped spellings in order", func() {
			Expect(router.Spellings("claude-opus-5-5")).To(Equal([]string{"anthropic/claude-opus-5.5", "anthropic/claude-opus-5"}))
			Expect(router.Attempts("claude-opus-5-5")).To(Equal(2))
		})

		It("serves an unmapped model by its own name", func() {
			Expect(router.Spellings("sonnet")).To(Equal([]string{"sonnet"}))
			Expect(router.Attempts("sonnet")).To(Equal(1))
		})
	})

	Describe("Launch", func() {
		var router *Router
		makeRouter := func(keyFile string) {
			var err error
			router, err = NewRouter(RouterEntry{
				BaseURL: "https://provider.example.invalid", KeyFile: keyFile, HaikuModel: "anthropic/claude-haiku-4-5",
				ModelMap: map[string][]string{"m": {"p"}},
			}, directory)
			Expect(err).NotTo(HaveOccurred())
		}

		It("carries the endpoint, the key read now, the haiku model and the config directory, and no key otherwise", func() {
			writeFile(filepath.Join(directory, "router.key"), "sk-secret\n", 0o600)
			makeRouter("router.key")
			environment, err := router.Launch()
			Expect(err).NotTo(HaveOccurred())
			Expect(environment).To(ConsistOf(
				EnvironmentBaseURL+"=https://provider.example.invalid",
				EnvironmentAPIKey+"=sk-secret",
				EnvironmentHaikuModel+"=anthropic/claude-haiku-4-5",
				EnvironmentConfigDirectory+"="+router.ConfigDirectoryPath(),
				EnvironmentMaxRetries+"="+executorRetries,
			))
		})

		It("omits the haiku model when the entry names none", func() {
			writeFile(filepath.Join(directory, "router.key"), "sk-secret", 0o600)
			makeRouter("router.key")
			router.haikuModel = ""
			environment, err := router.Launch()
			Expect(err).NotTo(HaveOccurred())
			for _, variable := range environment {
				Expect(variable).NotTo(HavePrefix(EnvironmentHaikuModel + "="))
			}
		})

		DescribeTable("reports a key it cannot read",
			func(keyFile string, content string, write bool, chmod bool) {
				if write {
					writeFile(filepath.Join(directory, keyFile), content, 0o600)
					if chmod {
						Expect(os.Chmod(filepath.Join(directory, keyFile), 0o644)).To(Succeed())
					}
				}
				makeRouter(keyFile)
				_, err := router.Launch()
				Expect(err).To(MatchError(ContainSubstring(ErrNoKey.Error())))
			},
			Entry("missing", "router.key", "", false, false),
			Entry("empty", "router.key", "  \n", true, false),
			Entry("world-readable", "router.key", "sk-secret", true, true),
		)

		It("reads an absolute key file outside the state directory", func() {
			keyFile := filepath.Join(GinkgoT().TempDir(), "key")
			writeFile(keyFile, "sk-secret", 0o600)
			makeRouter(keyFile)
			environment, err := router.Launch()
			Expect(err).NotTo(HaveOccurred())
			Expect(environment).To(ContainElement(EnvironmentAPIKey + "=sk-secret"))
		})
	})

	Describe("PrepareConfigDirectory", func() {
		var router *Router
		var history string
		BeforeEach(func() {
			var err error
			router, err = NewRouter(RouterEntry{
				BaseURL: "https://provider.example.invalid", KeyFile: "router.key", ModelMap: map[string][]string{"m": {"p"}},
			}, directory)
			Expect(err).NotTo(HaveOccurred())
			history = filepath.Join(directory, "history")
		})

		It("creates the directory owner-only and links projects to the history", func() {
			Expect(router.PrepareConfigDirectory(history)).To(Succeed())
			info, err := os.Stat(router.ConfigDirectoryPath())
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode().Perm()).To(Equal(configDirectoryMode))
			target, err := os.Readlink(filepath.Join(router.ConfigDirectoryPath(), ProjectsDirectory))
			Expect(err).NotTo(HaveOccurred())
			Expect(target).To(Equal(history))
		})

		It("is idempotent", func() {
			Expect(router.PrepareConfigDirectory(history)).To(Succeed())
			Expect(router.PrepareConfigDirectory(history)).To(Succeed())
		})

		It("replaces a link pointing elsewhere", func() {
			other := filepath.Join(directory, "other")
			Expect(os.MkdirAll(router.ConfigDirectoryPath(), configDirectoryMode)).To(Succeed())
			Expect(os.Symlink(other, filepath.Join(router.ConfigDirectoryPath(), ProjectsDirectory))).To(Succeed())
			Expect(router.PrepareConfigDirectory(history)).To(Succeed())
			target, err := os.Readlink(filepath.Join(router.ConfigDirectoryPath(), ProjectsDirectory))
			Expect(err).NotTo(HaveOccurred())
			Expect(target).To(Equal(history))
		})

		It("leaves a real projects directory alone", func() {
			projects := filepath.Join(router.ConfigDirectoryPath(), ProjectsDirectory)
			Expect(os.MkdirAll(projects, configDirectoryMode)).To(Succeed())
			Expect(router.PrepareConfigDirectory(history)).To(Succeed())
			info, err := os.Lstat(projects)
			Expect(err).NotTo(HaveOccurred())
			Expect(info.Mode() & os.ModeSymlink).To(BeZero())
		})

		It("refuses a directory carrying the executor's own login", func() {
			Expect(os.MkdirAll(router.ConfigDirectoryPath(), configDirectoryMode)).To(Succeed())
			writeFile(filepath.Join(router.ConfigDirectoryPath(), CredentialsFile), "{}", configFileMode)
			err := router.PrepareConfigDirectory(history)
			Expect(err).To(MatchError(ContainSubstring(ErrLoginPresent.Error())))
		})

		It("links nothing when no history is given", func() {
			Expect(router.PrepareConfigDirectory("")).To(Succeed())
			_, err := os.Lstat(filepath.Join(router.ConfigDirectoryPath(), ProjectsDirectory))
			Expect(errors.Is(err, os.ErrNotExist)).To(BeTrue())
		})
	})
})
