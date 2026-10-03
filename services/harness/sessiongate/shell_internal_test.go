// Copyright 2026 Candace Labs

package sessiongate

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"mvdan.cc/sh/v3/syntax"
)

// firstCall parses source and returns its first command call.
func firstCall(source string) *syntax.CallExpr {
	file, err := syntax.NewParser(syntax.Variant(syntax.LangBash)).Parse(strings.NewReader(source), "")
	Expect(err).NotTo(HaveOccurred())
	var call *syntax.CallExpr
	syntax.Walk(file, func(node syntax.Node) bool {
		if found, ok := node.(*syntax.CallExpr); ok && call == nil {
			call = found
		}
		return call == nil
	})
	Expect(call).NotTo(BeNil())
	return call
}

var _ = Describe("command words", func() {
	DescribeTable("name the command past its wrappers",
		func(source string, name string, arguments []string) {
			gotName, gotArguments := commandWords(firstCall(source))
			Expect(gotName).To(Equal(name))
			Expect(gotArguments).To(Equal(arguments))
		},
		Entry("a plain call", `sleep 5`, "sleep", []string{"5"}),
		Entry("an absolute path", `/usr/bin/pgrep -f x`, "pgrep", []string{"-f", "x"}),
		Entry("env with an assignment", `env -i FOO=1 sleep 2`, "sleep", []string{"2"}),
		Entry("quoted parts", `"sl"'eep' 3`, "sleep", []string{"3"}),
		Entry("an expansion is not literal", `$cmd 3`, "", []string{"3"}),
		Entry("only wrappers", `nohup`, "", nil),
	)

	It("reads a shell's script as its first operand after -c", func() {
		script, ok := shellScript([]string{"-lc", "sleep 1"})
		Expect(ok).To(BeTrue())
		Expect(script).To(Equal("sleep 1"))
		_, ok = shellScript([]string{"script.sh"})
		Expect(ok).To(BeFalse())
		_, ok = shellScript([]string{"-c"})
		Expect(ok).To(BeFalse())
	})

	It("reads git's subcommand past its global options", func() {
		Expect(gitSubcommand([]string{"-C", "/w", "--no-pager", "commit", "-m", "x"})).To(Equal("commit"))
		Expect(gitSubcommand([]string{"--version"})).To(BeEmpty())
	})

	It("reads gh's URL from the last line it printed", func() {
		Expect(lastLine("Creating draft pull request\nhttps://example.invalid/pull/1\n")).To(Equal("https://example.invalid/pull/1"))
	})
})
