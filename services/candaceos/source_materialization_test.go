package candaceos_test

import (
	"archive/tar"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/candacelabs/csf/services/candaceos"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("Git app source materialization", func() {
	It("drains archive padding before waiting for Git", func() {
		repository, revision := committedMaterializationRepository("notes/compose.yaml", "services: {}\n")

		realGit, err := exec.LookPath("git")
		Expect(err).NotTo(HaveOccurred())
		GinkgoT().Setenv("CANDACEOS_TEST_REAL_GIT", realGit)
		gitWrapper := filepath.Join(GinkgoT().TempDir(), "git-with-padding")
		Expect(os.WriteFile(gitWrapper, []byte(`#!/bin/sh
archive=false
for argument in "$@"; do
  if [ "$argument" = archive ]; then
    archive=true
  fi
done
"$CANDACEOS_TEST_REAL_GIT" "$@" || exit $?
if $archive; then
  index=0
  while [ "$index" -lt 4096 ]; do
    printf '%1024s' ''
    index=$((index + 1))
  done
fi
`), 0o700)).To(Succeed())

		destination := GinkgoT().TempDir()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		digest, err := candaceos.MaterializeGitAppSource(
			ctx, gitWrapper, repository, revision, "notes", destination,
		)

		Expect(err).NotTo(HaveOccurred())
		Expect(digest).To(MatchRegexp(`^sha256:[0-9a-f]{64}$`))
		contents, err := os.ReadFile(filepath.Join(destination, "compose.yaml"))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(contents)).To(Equal("services: {}\n"))
	})

	It("does not follow a destination symlink outside its opened root", func() {
		repository, revision := committedMaterializationRepository("notes/compose.yaml", "services: {}\n")
		archivePath := filepath.Join(GinkgoT().TempDir(), "source.tar")
		writeMaterializationArchive(archivePath, "escape/file", "outside")

		realGit, err := exec.LookPath("git")
		Expect(err).NotTo(HaveOccurred())
		GinkgoT().Setenv("CANDACEOS_TEST_REAL_GIT", realGit)
		GinkgoT().Setenv("CANDACEOS_TEST_ARCHIVE", archivePath)
		ready := filepath.Join(GinkgoT().TempDir(), "ready")
		proceed := filepath.Join(GinkgoT().TempDir(), "proceed")
		GinkgoT().Setenv("CANDACEOS_TEST_READY", ready)
		GinkgoT().Setenv("CANDACEOS_TEST_PROCEED", proceed)
		gitWrapper := filepath.Join(GinkgoT().TempDir(), "git-with-controlled-archive")
		Expect(os.WriteFile(gitWrapper, []byte(`#!/bin/sh
archive=false
for argument in "$@"; do
  if [ "$argument" = archive ]; then
    archive=true
  fi
done
if ! $archive; then
  exec "$CANDACEOS_TEST_REAL_GIT" "$@"
fi
: > "$CANDACEOS_TEST_READY"
while [ ! -e "$CANDACEOS_TEST_PROCEED" ]; do
  sleep 0.01
done
cat "$CANDACEOS_TEST_ARCHIVE"
`), 0o700)).To(Succeed())

		destination := GinkgoT().TempDir()
		outside := GinkgoT().TempDir()
		type result struct {
			digest string
			err    error
		}
		results := make(chan result, 1)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		go func() {
			digest, err := candaceos.MaterializeGitAppSource(
				ctx, gitWrapper, repository, revision, ".", destination,
			)
			results <- result{digest: digest, err: err}
		}()

		Eventually(func() bool {
			_, err := os.Stat(ready)
			return err == nil
		}, 2*time.Second, 10*time.Millisecond).Should(BeTrue())
		Expect(os.Symlink(outside, filepath.Join(destination, "escape"))).To(Succeed())
		Expect(os.WriteFile(proceed, nil, 0o600)).To(Succeed())
		var outcome result
		Eventually(results, 2*time.Second).Should(Receive(&outcome))
		Expect(outcome.err).To(HaveOccurred())
		_, statErr := os.Stat(filepath.Join(outside, "file"))
		Expect(os.IsNotExist(statErr)).To(BeTrue())
	})
})

func committedMaterializationRepository(relativePath, contents string) (string, string) {
	repository := GinkgoT().TempDir()
	target := filepath.Join(repository, filepath.FromSlash(relativePath))
	Expect(os.MkdirAll(filepath.Dir(target), 0o755)).To(Succeed())
	Expect(os.WriteFile(target, []byte(contents), 0o644)).To(Succeed())
	runMaterializationGit(repository, "init", "-q", "-b", "main")
	runMaterializationGit(repository, "config", "user.name", "CandaceOS Test")
	runMaterializationGit(repository, "config", "user.email", "candaceos-test@example.invalid")
	runMaterializationGit(repository, "add", "--", relativePath)
	runMaterializationGit(repository, "commit", "-q", "-m", "test: add source")
	return repository, runMaterializationGit(repository, "rev-parse", "HEAD")
}

func writeMaterializationArchive(archivePath, name, contents string) {
	file, err := os.Create(archivePath)
	Expect(err).NotTo(HaveOccurred())
	archive := tar.NewWriter(file)
	Expect(archive.WriteHeader(&tar.Header{
		Name: name, Mode: 0o644, Size: int64(len(contents)), Typeflag: tar.TypeReg,
	})).To(Succeed())
	_, err = archive.Write([]byte(contents))
	Expect(err).NotTo(HaveOccurred())
	Expect(archive.Close()).To(Succeed())
	Expect(file.Close()).To(Succeed())
}

func runMaterializationGit(repository string, args ...string) string {
	commandArgs := append([]string{"-C", repository}, args...)
	output, err := exec.Command("git", commandArgs...).CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), "git %s failed: %s", strings.Join(args, " "), output)
	return strings.TrimSpace(string(output))
}
