// Copyright 2026 Candace Labs

package argv

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Flag.matches", func() {
	full := Flag{Short: 'f', Long: "full"}

	DescribeTable("reads one argument",
		func(argument string, expected bool) {
			Expect(full.matches(argument)).To(Equal(expected))
		},
		Entry("short", "-f", true),
		Entry("bundled", "-xfz", true),
		Entry("long", "--full", true),
		Entry("long with a value", "--full=", true),
		Entry("long with another name", "--fuller", false),
		Entry("lone dash", "-", false),
		Entry("terminator", "--", false),
		Entry("operand", "file", false),
		Entry("empty", "", false),
	)
})
