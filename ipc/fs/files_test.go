// Copyright 2026 Candace Labs

package fs_test

import (
	"embed"
	stdfs "io/fs"
	"path/filepath"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	ipcfs "github.com/candacelabs/csf/ipc/fs"
)

// Integration specs: what a binary granting the capability, and a service
// holding it, can and cannot do through the exported API alone. The only
// host state they touch is an empty directory the test framework creates.
const (
	missingName = "missing.txt"
	escapeName  = "../outside.txt"
	rootedName  = "/etc/hostname"
	nestedName  = "nested"
	nestedNote  = "nested/inner.txt"
	noteText    = "read through the capability"
)

// Files compiled into a binary satisfy the capability's shape, so a service's
// embedded default and a granted directory are read by the same code.
var (
	_ ipcfs.IFiles = embed.FS{}
	_ ipcfs.IFiles = fstest.MapFS{}
	_ ipcfs.IFiles = (*ipcfs.HostFiles)(nil)
)

var _ = Describe("Granting a host directory", func() {
	It("grants an existing directory and lists it", func() {
		directory := GinkgoT().TempDir()
		files, err := ipcfs.NewHostFiles(directory)
		Expect(err).NotTo(HaveOccurred())
		Expect(files.Directory()).To(Equal(directory))

		entries, err := files.ReadDir(".")
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("refuses an empty directory name instead of granting the working directory", func() {
		files, err := ipcfs.NewHostFiles("")
		Expect(err).To(MatchError(ipcfs.ErrNoDirectory))
		Expect(files).To(BeNil())
	})

	It("refuses a directory that does not exist", func() {
		_, err := ipcfs.NewHostFiles(filepath.Join(GinkgoT().TempDir(), "absent"))
		Expect(err).To(MatchError(stdfs.ErrNotExist))
		Expect(err).To(MatchError(ContainSubstring("ipc/fs: grant")))
	})
})

var _ = Describe("Using a granted directory", func() {
	var (
		directory string
		files     *ipcfs.HostFiles
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		var err error
		files, err = ipcfs.NewHostFiles(directory)
		Expect(err).NotTo(HaveOccurred())
	})

	It("names the directory in a failure and keeps the io/fs error class", func() {
		_, err := files.ReadFile(missingName)
		Expect(err).To(MatchError(stdfs.ErrNotExist))
		Expect(err).To(MatchError(ContainSubstring("ipc/fs: read " + missingName + " in " + directory)))
		_, err = files.Open(missingName)
		Expect(err).To(MatchError(stdfs.ErrNotExist))
		_, err = files.ReadDir(missingName)
		Expect(err).To(MatchError(stdfs.ErrNotExist))
	})

	DescribeTable("rejects a name that would leave the granted directory",
		func(name string) {
			_, err := files.ReadFile(name)
			Expect(err).To(MatchError(stdfs.ErrInvalid))
			_, err = files.Open(name)
			Expect(err).To(MatchError(stdfs.ErrInvalid))
			_, err = files.ReadDir(name)
			Expect(err).To(MatchError(stdfs.ErrInvalid))
		},
		Entry("a parent reference", escapeName),
		Entry("a rooted path", rootedName),
	)
})

var _ = Describe("A HostFiles nobody granted", func() {
	DescribeTable("answers every operation with ErrNotGranted instead of panicking",
		func(files *ipcfs.HostFiles) {
			Expect(files.Directory()).To(BeEmpty())
			_, err := files.ReadFile(missingName)
			Expect(err).To(MatchError(ipcfs.ErrNotGranted))
			_, err = files.Open(missingName)
			Expect(err).To(MatchError(ipcfs.ErrNotGranted))
			_, err = files.ReadDir(".")
			Expect(err).To(MatchError(ipcfs.ErrNotGranted))
		},
		Entry("a nil pointer", (*ipcfs.HostFiles)(nil)),
		Entry("a zero value", &ipcfs.HostFiles{}),
	)
})

var _ = Describe("IFiles", func() {
	It("is satisfied by an in-memory tree, so a test grants one directly", func() {
		var memory ipcfs.IFiles = fstest.MapFS{nestedNote: &fstest.MapFile{Data: []byte(noteText)}}
		content, err := memory.ReadFile(nestedNote)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal(noteText))

		entries, err := memory.ReadDir(nestedName)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1))
	})
})
