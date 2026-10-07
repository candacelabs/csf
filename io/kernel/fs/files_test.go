// Copyright 2026 Candace Labs

package fs_test

import (
	"embed"
	stdfs "io/fs"
	"path/filepath"
	"testing/fstest"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	iofs "github.com/candacelabs/csf/io/kernel/fs"
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
	_ iofs.IFiles = embed.FS{}
	_ iofs.IFiles = fstest.MapFS{}
	_ iofs.IFiles = (*iofs.HostFiles)(nil)
)

var _ = Describe("Granting a host directory", func() {
	It("grants an existing directory and lists it", func() {
		directory := GinkgoT().TempDir()
		files, err := iofs.NewHostFiles(directory)
		Expect(err).NotTo(HaveOccurred())
		Expect(files.Directory()).To(Equal(directory))

		entries, err := files.ReadDir(".")
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(BeEmpty())
	})

	It("refuses an empty directory name instead of granting the working directory", func() {
		files, err := iofs.NewHostFiles("")
		Expect(err).To(MatchError(iofs.ErrNoDirectory))
		Expect(files).To(BeNil())
	})

	It("refuses a directory that does not exist", func() {
		_, err := iofs.NewHostFiles(filepath.Join(GinkgoT().TempDir(), "absent"))
		Expect(err).To(MatchError(stdfs.ErrNotExist))
		Expect(err).To(MatchError(ContainSubstring("ipc/fs: grant")))
	})
})

var _ = Describe("Using a granted directory", func() {
	var (
		directory string
		files     *iofs.HostFiles
	)

	BeforeEach(func() {
		directory = GinkgoT().TempDir()
		var err error
		files, err = iofs.NewHostFiles(directory)
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
		func(files *iofs.HostFiles) {
			Expect(files.Directory()).To(BeEmpty())
			_, err := files.ReadFile(missingName)
			Expect(err).To(MatchError(iofs.ErrNotGranted))
			_, err = files.Open(missingName)
			Expect(err).To(MatchError(iofs.ErrNotGranted))
			_, err = files.ReadDir(".")
			Expect(err).To(MatchError(iofs.ErrNotGranted))
		},
		Entry("a nil pointer", (*iofs.HostFiles)(nil)),
		Entry("a zero value", &iofs.HostFiles{}),
	)
})

var _ = Describe("IFiles", func() {
	It("is satisfied by an in-memory tree, so a test grants one directly", func() {
		var memory iofs.IFiles = fstest.MapFS{nestedNote: &fstest.MapFile{Data: []byte(noteText)}}
		content, err := memory.ReadFile(nestedNote)
		Expect(err).NotTo(HaveOccurred())
		Expect(string(content)).To(Equal(noteText))

		entries, err := memory.ReadDir(nestedName)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).To(HaveLen(1))
	})
})
