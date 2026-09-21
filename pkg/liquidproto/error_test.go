package liquidproto_test

import (
	"bytes"
	"strings"

	"github.com/candacelabs/csf/pkg/liquidproto"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Error", func() {
	DescribeTable("redacts rejected content while retaining the trusted value",
		func(value any, wantLength, secret string) {
			err := &liquidproto.Error{
				Message:   "candace.messaging.v1.Envelope",
				Field:     "subject",
				Predicate: "len(this) > 0",
				Value:     value,
			}

			for _, want := range []string{
				"candace.messaging.v1.Envelope.subject",
				"len(this) > 0",
				wantLength,
			} {
				Expect(err.Error()).To(ContainSubstring(want))
			}
			Expect(err.Error()).NotTo(ContainSubstring(secret))
			Expect(err.Value).To(Equal(value))
		},
		Entry("string", strings.Repeat("super-secret-token", 1_000), "18000 bytes", "super-secret-token"),
		Entry("bytes", bytes.Repeat([]byte{0xde, 0xad, 0xbe, 0xef}, 1_000), "4000 bytes", "deadbeef"),
	)
})
