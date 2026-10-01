package privatefile_test

import (
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/candacelabs/csf/pkg/privatefile"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestPrivateFile(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Private file suite")
}

var _ = Describe("Read", func() {
	It("accepts an owner-only regular file at the size limit", func() {
		path := filepath.Join(GinkgoT().TempDir(), "settings")
		Expect(os.WriteFile(path, []byte("secret"), 0o600)).To(Succeed())
		content, err := privatefile.Read(path, 6)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal("secret"))
	})

	DescribeTable("rejects oversized or exposed files without leaking their path or content",
		func(mode os.FileMode, limit int64) {
			path := filepath.Join(GinkgoT().TempDir(), "private-configuration")
			Expect(os.WriteFile(path, []byte("secret"), 0o600)).To(Succeed())
			Expect(os.Chmod(path, mode)).To(Succeed())
			content, err := privatefile.Read(path, limit)
			Expect(err).To(HaveOccurred())
			Expect(content).To(BeNil())
			Expect(err.Error()).NotTo(ContainSubstring(path))
			Expect(err.Error()).NotTo(ContainSubstring("secret"))
		},
		Entry("oversized", os.FileMode(0o600), int64(5)),
		Entry("group-readable", os.FileMode(0o640), int64(6)),
		Entry("other-readable", os.FileMode(0o604), int64(6)),
		Entry("zero limit", os.FileMode(0o600), int64(0)),
		Entry("negative limit", os.FileMode(0o600), int64(-1)),
		Entry("overflowing limit", os.FileMode(0o600), int64(math.MaxInt64)),
	)

	It("rejects directories and missing files", func() {
		directory := GinkgoT().TempDir()
		_, err := privatefile.Read(directory, 100)
		Expect(err).To(HaveOccurred())
		_, err = privatefile.Read(filepath.Join(directory, "missing"), 100)
		Expect(err).To(HaveOccurred())
	})
})
