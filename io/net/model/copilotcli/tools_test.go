// Copyright 2026 Candace Labs

package copilotcli_test

import (
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/io/net/model/copilotcli"
)

var _ = Describe("TranslateToolRules", func() {
	shell := []string{"bash", "read_bash", "write_bash", "stop_bash", "list_bash"}

	DescribeTable("translates a Claude Code rule into the tools Copilot shows and the permissions it grants",
		func(rules []string, available []string, allowed []string) {
			grants, err := copilotcli.TranslateToolRules(rules)
			Expect(err).NotTo(HaveOccurred())
			Expect(grants.Available).To(Equal(available))
			Expect(grants.Allowed).To(Equal(allowed))
			Expect(grants.AllPaths).To(Equal(slices.Contains(available, "bash")), "only a shell grant reaches every path")
		},
		Entry("the shell", []string{"Bash"}, shell, []string{"shell"}),
		Entry("the shell for one command", []string{"Bash(git add:*)"}, shell, []string{"shell(git add:*)"}),
		Entry("reads, which Copilot runs unasked", []string{"Read", "Glob", "Grep"}, []string{"view", "glob", "grep"}, []string{}),
		Entry("writes, granted once", []string{"Write", "Edit", "MultiEdit"}, []string{"create", "edit"}, []string{"write"}),
		Entry("an MCP tool", []string{"mcp__csf__ListAgentSessions"}, []string{"csf-ListAgentSessions"}, []string{"csf(ListAgentSessions)"}),
		Entry("the ouroboros fixer's rules",
			[]string{"Read", "Write", "Edit", "Glob", "Grep", "Bash(tools/bazel.sh test:*)", "Bash(git commit:*)"},
			append([]string{"view", "create", "edit", "glob", "grep"}, shell...),
			[]string{"write", "shell(tools/bazel.sh test:*)", "shell(git commit:*)"}),
		Entry("nothing", []string{}, []string{}, []string{}),
	)

	DescribeTable("refuses a rule with no Copilot equivalent rather than drop or widen it",
		func(rule string) {
			_, err := copilotcli.TranslateToolRules([]string{"Bash", rule})
			Expect(err).To(MatchError(copilotcli.ErrUntranslatableTool))
			Expect(err.Error()).To(ContainSubstring(rule))
		},
		Entry("a tool Copilot has no counterpart of", "WebSearch"),
		Entry("a pattern on a tool other than the shell", "Edit(src/**)"),
		Entry("an unclosed pattern", "Bash(git add:*"),
		Entry("a whole MCP server", "mcp__csf"),
	)
})
