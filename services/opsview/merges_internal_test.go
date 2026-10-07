// Copyright 2026 Candace Labs

package opsview

import (
	"encoding/json"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeTable("a pull request's checks, rolled up into one word",
	func(listing string, expected string) {
		var checks []statusCheck
		Expect(json.Unmarshal([]byte(listing), &checks)).To(Succeed())
		Expect(rollup(checks)).To(Equal(expected))
	},
	Entry("no checks", `[]`, ""),
	Entry("all passed", `[{"status":"COMPLETED","conclusion":"SUCCESS"},{"state":"SUCCESS"}]`, ChecksPassing),
	Entry("one still running", `[{"status":"IN_PROGRESS","conclusion":""},{"status":"COMPLETED","conclusion":"SUCCESS"}]`, ChecksPending),
	Entry("one failed, whatever else runs", `[{"status":"IN_PROGRESS"},{"status":"COMPLETED","conclusion":"FAILURE"}]`, ChecksFailing),
	Entry("a commit status in error", `[{"state":"ERROR"}]`, ChecksFailing),
)
