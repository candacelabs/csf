// Copyright 2026 Candace Labs

package atomicfile_test

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/atomicfile"
)

func TestAtomicfile(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "pkg/atomicfile Suite")
}

// names lists what directory holds, so a spec can see a leftover temporary.
func names(directory string) []string {
	GinkgoHelper()
	entries, err := os.ReadDir(directory)
	Expect(err).NotTo(HaveOccurred())
	listed := []string{}
	for _, entry := range entries {
		listed = append(listed, entry.Name())
	}
	return listed
}

var _ = Describe("WriteFile", func() {
	var directory, path string

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		path = filepath.Join(directory, "record.json")
	})

	It("creates the file with the content and nothing beside it", func() {
		Expect(atomicfile.WriteFile(path, []byte("first"), 0o600)).To(Succeed())
		Expect(os.ReadFile(path)).To(Equal([]byte("first")))
		Expect(names(directory)).To(Equal([]string{"record.json"}))
	})

	It("replaces an existing file whole", func() {
		Expect(atomicfile.WriteFile(path, []byte("a much longer first version"), 0o600)).To(Succeed())
		Expect(atomicfile.WriteFile(path, []byte("short"), 0o600)).To(Succeed())
		Expect(os.ReadFile(path)).To(Equal([]byte("short")))
	})

	It("narrows a widened file to the mode given, where renameio.WriteFile would keep 0644", func() {
		Expect(os.WriteFile(path, []byte("secret"), 0o644)).To(Succeed())
		Expect(os.Chmod(path, 0o644)).To(Succeed())
		Expect(atomicfile.WriteFile(path, []byte("secret"), 0o600)).To(Succeed())
		info, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o600)))
	})

	It("writes the mode exactly, umask or not", func() {
		Expect(atomicfile.WriteFile(path, []byte("#!/bin/sh\n"), 0o775)).To(Succeed())
		info, err := os.Stat(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o775)))
	})

	It("fails without a directory and creates nothing", func() {
		err := atomicfile.WriteFile(filepath.Join(directory, "missing", "record.json"), []byte("x"), 0o600)
		Expect(err).To(MatchError(os.ErrNotExist))
		Expect(names(directory)).To(BeEmpty())
	})

	It("leaves the target and the directory as they were when the rename fails", func() {
		Expect(os.Mkdir(path, 0o700)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(path, "inside"), []byte("kept"), 0o600)).To(Succeed())
		Expect(atomicfile.WriteFile(path, []byte("x"), 0o600)).NotTo(Succeed())
		Expect(names(directory)).To(Equal([]string{"record.json"}))
		Expect(os.ReadFile(filepath.Join(path, "inside"))).To(Equal([]byte("kept")))
	})

	It("lets concurrent writers to one path each land whole, with no shared temporary name", func() {
		const writers = 16
		var group sync.WaitGroup
		failures := make(chan error, writers)
		for writer := range writers {
			group.Go(func() {
				content := strings.Repeat(string(rune('a'+writer)), 4096)
				if err := atomicfile.WriteFile(path, []byte(content), 0o600); err != nil {
					failures <- err
				}
			})
		}
		group.Wait()
		close(failures)
		Expect(failures).To(BeEmpty())
		content, err := os.ReadFile(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(content).To(HaveLen(4096))
		Expect(strings.Count(string(content), string(content[:1]))).To(Equal(4096))
		Expect(names(directory)).To(Equal([]string{"record.json"}))
	})
})
