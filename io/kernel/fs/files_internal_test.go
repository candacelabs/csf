// Copyright 2026 Candace Labs

package fs

import (
	"io"
	stdfs "io/fs"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

// Unit specs of HostFiles over a real directory tree. The fixture is written
// here, inside ipc/fs, because this package is the file capability.
const (
	unitNote       = "note.txt"
	unitText       = "read through the capability"
	unitNested     = "nested"
	unitNestedNote = "nested/inner.txt"
	unitPattern    = "*.txt"
)

func unitTree() string {
	GinkgoHelper()
	directory := GinkgoT().TempDir()
	Expect(os.WriteFile(filepath.Join(directory, unitNote), []byte(unitText), 0o600)).To(Succeed())
	Expect(os.Mkdir(filepath.Join(directory, unitNested), 0o700)).To(Succeed())
	Expect(os.WriteFile(filepath.Join(directory, unitNestedNote), []byte(unitText), 0o600)).To(Succeed())
	return directory
}

var _ = Describe("HostFiles over a real tree", func() {
	It("records the absolute directory and reads, opens and lists beneath it", func() {
		directory := unitTree()
		files, err := NewHostFiles(directory)
		Expect(err).NotTo(HaveOccurred())
		Expect(files.directory).To(Equal(directory))
		Expect(files.granted()).To(BeTrue())

		content, err := files.ReadFile(unitNote)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal(unitText))

		opened, err := files.Open(unitNestedNote)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(opened.Close)
		nested, err := io.ReadAll(opened)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(nested)).To(Equal(unitText))

		entries, err := files.ReadDir(".")
		Expect(err).NotTo(HaveOccurred())
		names := []string{}
		for _, entry := range entries {
			names = append(names, entry.Name())
		}
		Expect(names).To(Equal([]string{unitNested, unitNote}))

		matches, err := stdfs.Glob(files, unitPattern)
		Expect(err).NotTo(HaveOccurred())
		Expect(matches).To(Equal([]string{unitNote}))
	})

	It("resolves a relative directory once, at the grant", func() {
		directory := unitTree()
		original, err := os.Getwd()
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(os.Chdir, original)
		Expect(os.Chdir(filepath.Dir(directory))).To(Succeed())

		files, err := NewHostFiles(filepath.Base(directory))
		Expect(err).NotTo(HaveOccurred())
		Expect(files.Directory()).To(Equal(directory))

		Expect(os.Chdir(os.TempDir())).To(Succeed())
		content, err := files.ReadFile(unitNote)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal(unitText))
	})

	It("refuses to grant a path that is a file", func() {
		_, err := NewHostFiles(filepath.Join(unitTree(), unitNote))
		Expect(err).To(MatchError(ErrNotDirectory))
	})

	It("cannot write through a file it opened", func() {
		files, err := NewHostFiles(unitTree())
		Expect(err).NotTo(HaveOccurred())
		opened, err := files.Open(unitNote)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(opened.Close)
		// os.DirFS hands back an *os.File, which has a Write method; the
		// descriptor behind it is opened read-only, so the write fails.
		writer, writable := opened.(io.Writer)
		Expect(writable).To(BeTrue())
		_, err = writer.Write([]byte(unitText))
		Expect(err).To(HaveOccurred())
	})
})
