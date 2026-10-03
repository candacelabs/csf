// Copyright 2026 Candace Labs

package argv_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/argv"
)

var full = argv.Flag{Short: 'f', Long: "full"}

var _ = Describe("HasFlag", func() {
	DescribeTable("finds the flag the way getopt_long would",
		func(arguments []string, expected bool) {
			Expect(argv.HasFlag(arguments, full)).To(Equal(expected))
		},
		Entry("the short form", []string{"-f", "pattern"}, true),
		Entry("bundled first", []string{"-fl", "pattern"}, true),
		Entry("bundled last", []string{"-af", "pattern"}, true),
		Entry("the long form", []string{"--full", "pattern"}, true),
		Entry("the long form with a value", []string{"--full=yes"}, true),
		Entry("after an operand", []string{"pattern", "-f"}, true),
		Entry("absent", []string{"-l", "pattern"}, false),
		Entry("a longer long name", []string{"--fullness"}, false),
		Entry("an operand that contains the letter", []string{"fred"}, false),
	)

	Describe("undefined usage", func() {
		It("finds nothing in no arguments", func() {
			Expect(argv.HasFlag(nil, full)).To(BeFalse())
			Expect(argv.HasFlag([]string{}, full)).To(BeFalse())
		})

		It("matches nothing with a zero flag", func() {
			Expect(argv.HasFlag([]string{"-f", "--full", "-", "--"}, argv.Flag{})).To(BeFalse())
		})

		It("reads a lone dash as an operand", func() {
			Expect(argv.HasFlag([]string{"-"}, full)).To(BeFalse())
			Expect(argv.HasFlag([]string{"-"}, argv.Flag{Short: '-'})).To(BeFalse())
		})

		It("stops at the terminator", func() {
			Expect(argv.HasFlag([]string{"--", "-f"}, full)).To(BeFalse())
			Expect(argv.HasFlag([]string{"-l", "--", "--full"}, full)).To(BeFalse())
			Expect(argv.HasFlag([]string{"-f", "--", "x"}, full)).To(BeTrue())
		})

		It("reads only the form a flag has", func() {
			Expect(argv.HasFlag([]string{"-f"}, argv.Flag{Long: "full"})).To(BeFalse())
			Expect(argv.HasFlag([]string{"--full"}, argv.Flag{Short: 'f'})).To(BeFalse())
		})

		It("reads a value that looks like a flag as the flag, the documented limit", func() {
			Expect(argv.HasFlag([]string{"-p", "-f"}, full)).To(BeTrue())
		})
	})
})
