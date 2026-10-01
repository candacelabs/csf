package candaceos_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	"github.com/candacelabs/csf/services/candaceos"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("canonical app source digest", func() {
	write := func(root, name string, contents []byte, mode os.FileMode) {
		path := filepath.Join(root, name)
		Expect(os.MkdirAll(filepath.Dir(path), 0o755)).To(Succeed())
		Expect(os.WriteFile(path, contents, mode)).To(Succeed())
	}

	It("uses unambiguous length-prefixed records", func() {
		oneRecord := GinkgoT().TempDir()
		twoRecords := GinkgoT().TempDir()
		// The prior path-NUL-content-NUL encoding serialized these two trees
		// identically: "a\0b\0c\0\0".
		write(oneRecord, "a", []byte("b\x00c\x00"), 0o644)
		write(twoRecords, "a", []byte("b"), 0o644)
		write(twoRecords, "c", nil, 0o644)

		first, err := candaceos.DigestAppSource(context.Background(), oneRecord)
		Expect(err).NotTo(HaveOccurred())
		second, err := candaceos.DigestAppSource(context.Background(), twoRecords)
		Expect(err).NotTo(HaveOccurred())
		Expect(first).NotTo(Equal(second))
	})

	It("binds the normalized Git executable bit", func() {
		nonExecutable := GinkgoT().TempDir()
		executable := GinkgoT().TempDir()
		write(nonExecutable, "run", []byte("#!/bin/sh\n"), 0o644)
		write(executable, "run", []byte("#!/bin/sh\n"), 0o755)

		first, err := candaceos.DigestAppSource(context.Background(), nonExecutable)
		Expect(err).NotTo(HaveOccurred())
		second, err := candaceos.DigestAppSource(context.Background(), executable)
		Expect(err).NotTo(HaveOccurred())
		Expect(first).NotTo(Equal(second))
	})

	It("rejects source paths outside the materialization contract", func() {
		root := GinkgoT().TempDir()
		write(root, strings.Repeat("a/", 64)+"file", []byte("x"), 0o644)

		_, err := candaceos.DigestAppSource(context.Background(), root)

		Expect(err).To(MatchError(ContainSubstring("AppSourceEntry.path")))
	})

	It("derives the entry ceiling from the file ceiling", func() {
		root := GinkgoT().TempDir()
		write(root, "a/b/c", []byte("x"), 0o644)

		_, err := candaceos.DigestAppSourceWithLimits(context.Background(), root, &candaceosv1.AppSourceLimits{
			MaxFiles: 1,
			MaxBytes: 1,
		})

		Expect(err).To(MatchError(ContainSubstring("2-entry revision limit")))
	})
})
