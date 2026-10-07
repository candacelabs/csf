// Copyright 2026 Candace Labs

package bootstrap_test

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/bootstrap"
)

// readArchive reads a tar.gz archive back into its members in order, so a spec
// can assert both the frozen manifest and the tree it carries, the same way a
// runner will.
func readArchive(path string) ([]bootstrap.DigestEntry, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	gzipReader, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return nil, err
	}
	defer gzipReader.Close()

	var entries []bootstrap.DigestEntry
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, err
		}
		if header.Typeflag == tar.TypeDir {
			entries = append(entries, bootstrap.DigestEntry{
				Name:      strings.TrimSuffix(header.Name, "/"),
				Directory: true,
			})
			continue
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			return nil, err
		}
		entries = append(entries, bootstrap.DigestEntry{Name: header.Name, Content: body})
	}
	return entries, nil
}

// emit writes the repository into one archive and returns its path.
func emit(repo, destination string) string {
	target, err := bootstrap.TarGz(context.Background(), bootstrap.EmitRequest{
		Repo:           repo,
		Destination:    destination,
		SourceRevision: "abc123",
		Created:        "2026-10-06T00:00:00Z",
	})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return target
}

var _ = Describe("the bootstrap archive", func() {
	var (
		dir  string
		repo string
	)

	BeforeEach(func() {
		dir = GinkgoT().TempDir()
		repo = filepath.Join(dir, "repo")
		Expect(os.MkdirAll(filepath.Join(repo, "svc"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(repo, "svc", "main.go"), []byte("package main\n"), 0o644)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(repo, "README.md"), []byte("# repo\n"), 0o644)).To(Succeed())
	})

	It("emits a frozen version 1 manifest as the first member", func() {
		target := emit(repo, "out.tar.gz")

		entries, err := readArchive(target)
		Expect(err).NotTo(HaveOccurred())
		Expect(entries).NotTo(BeEmpty())
		Expect(entries[0].Name).To(Equal(bootstrap.ManifestName))

		var manifest bootstrap.Manifest
		Expect(json.Unmarshal(entries[0].Content, &manifest)).To(Succeed())
		Expect(manifest.FormatVersion).To(Equal(bootstrap.FormatVersion))
		Expect(manifest.Entrypoint).To(Equal(bootstrap.EntrypointName))
		Expect(manifest.Platform).To(Equal(bootstrap.Platforms))
		Expect(manifest.SourceRevision).To(Equal("abc123"))
		Expect(manifest.Created).To(Equal("2026-10-06T00:00:00Z"))

		// The manifest's digest is the digest of the payload: every member but
		// the manifest, in order. A reader refuses an archive altered after it
		// was written by recomputing exactly this.
		Expect(manifest.SHA256).To(Equal(bootstrap.PayloadDigest(entries[1:])))
	})

	It("emits the same bytes for the same inputs", func() {
		first, err := os.ReadFile(emit(repo, "first.tar.gz"))
		Expect(err).NotTo(HaveOccurred())
		second, err := os.ReadFile(emit(repo, "second.tar.gz"))
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(Equal(first))
	})

	It("carries a tree equal to the one it was emitted from", func() {
		// stage_equal: extract what the archive carries — every member but the
		// manifest — into a fresh directory, re-emit that directory, and the two
		// archives are the same bytes. The archive's payload is the whole tree, so
		// stage 1 and stage 2 agree.
		first, err := os.ReadFile(emit(repo, "stage1.tar.gz"))
		Expect(err).NotTo(HaveOccurred())
		entries, err := readArchive(emit(repo, "payload.tar.gz"))
		Expect(err).NotTo(HaveOccurred())

		carried := filepath.Join(dir, "carried")
		Expect(os.MkdirAll(carried, 0o755)).To(Succeed())
		for _, entry := range entries[1:] {
			target := filepath.Join(carried, filepath.FromSlash(entry.Name))
			if entry.Directory {
				Expect(os.MkdirAll(target, 0o755)).To(Succeed())
				continue
			}
			// The fixture carries no executable member, so the archive's only
			// preserved bit — executable — is off for every file here.
			Expect(os.WriteFile(target, entry.Content, 0o644)).To(Succeed())
		}

		second, err := os.ReadFile(emit(carried, "stage2.tar.gz"))
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(Equal(first))
	})
})
