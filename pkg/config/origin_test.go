package config_test

import (
	"github.com/candacelabs/csf/pkg/config"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = DescribeTable("private origins",
	func(origin string, valid bool) {
		err := config.ValidatePrivateOrigin(origin)
		if valid {
			Expect(err).NotTo(HaveOccurred())
			return
		}
		Expect(err).To(HaveOccurred())
	},
	Entry("loopback", "http://127.0.0.1:4096", true),
	Entry("private DNS", "http://opencode:4096", true),
	// 100.64.0.1 is the first address of the CGNAT range (100.64.0.0/10)
	// tailnets are carved out of, and belongs to no node: it exercises the
	// same CGNAT branch without naming a real host.
	Entry("tailnet", "https://100.64.0.1", true),
	Entry("public host", "https://opencode.example.com", false),
	Entry("path", "http://opencode:4096/session", false),
	Entry("credentials", "http://user:secret@opencode:4096", false),
	Entry("unsupported scheme", "ssh://opencode:4096", false),
)
