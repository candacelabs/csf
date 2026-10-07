package main

import (
	"bytes"
	"context"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	bootstrapservice "github.com/candacelabs/csf/services/bootstrap"
)

// bootstrapSource is the declaration the two bootstrap verbs are pinned
// against: the words a caller types, declared once in CSF and dispatched here.
const bootstrapSource = "../../../csf/bootstrap/bootstrap.csf"

var _ = Describe("Bootstrap", func() {
	It("declares both bootstrap verbs in CSF", func() {
		declaration, err := os.ReadFile(bootstrapSource)
		Expect(err).NotTo(HaveOccurred())
		source := string(declaration)
		// The kind the two verb rows share, and both commands: the local verb a
		// caller runs over a directory, and the positional archive verb any later
		// release's caller types.
		Expect(source).To(ContainSubstring("kind verb"))
		Expect(source).To(ContainSubstring("csf bootstrap -repo <dir> -local"))
		Expect(source).To(ContainSubstring("csf <archive.tar.gz> [args...]"))
	})

	It("runs the loop locally and emits the archive beside the repository", func() {
		dir := GinkgoT().TempDir()
		repo := filepath.Join(dir, "repo")
		Expect(os.MkdirAll(filepath.Join(repo, "svc"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(repo, "svc", "main.go"), []byte("package main\n"), 0o644)).To(Succeed())

		var output bytes.Buffer
		Expect(bootstrap(context.Background(), []string{"-repo", repo, "-local"}, &output)).To(Succeed())
		Expect(output.String()).To(ContainSubstring("archive "))
		Expect(filepath.Join(dir, bootstrapservice.ArchiveName)).To(BeARegularFile())
	})

	It("refuses a run with nothing to run over", func() {
		var output bytes.Buffer
		Expect(bootstrap(context.Background(), []string{"-local"}, &output)).To(MatchError(errBootstrapNeedsRepo))
	})

	It("refuses a run that does not say -local", func() {
		var output bytes.Buffer
		Expect(bootstrap(context.Background(), []string{"-repo", GinkgoT().TempDir()}, &output)).
			To(MatchError(ContainSubstring("pass -local")))
	})
})

var _ = Describe("Archive dispatch", func() {
	It("takes a .tar.gz first word as an archive", func() {
		// The whole positional rule: a first word ending in .tar.gz is an archive
		// rather than a command, so any binary from this release on runs a newer
		// one by naming it.
		Expect(isArchive("bootstrap.tar.gz")).To(BeTrue())
		Expect(isArchive("/tmp/new_one.tar.gz")).To(BeTrue())
		Expect(isArchive("bootstrap")).To(BeFalse())
		Expect(isArchive("serve")).To(BeFalse())
	})
})
