// Copyright 2026 Candace Labs

package archive_test

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
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/csf/bootstrap/archive"
	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/services/bootstrap"
)

// emit writes the fixture repository into a version 1 archive beside it and
// returns the archive's path.
func emit(repo string) string {
	target, err := bootstrap.TarGz(context.Background(), bootstrap.EmitRequest{
		Repo:           repo,
		Destination:    "bootstrap.tar.gz",
		SourceRevision: "abc123",
		Created:        "2026-10-06T00:00:00Z",
	})
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return target
}

// read parses an archive back into its manifest and payload entries, so a spec
// can alter one and reframe it.
func read(path string) (bootstrap.Manifest, []bootstrap.DigestEntry) {
	content, err := os.ReadFile(path)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	gzipReader, err := gzip.NewReader(bytes.NewReader(content))
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	defer func() { ExpectWithOffset(1, gzipReader.Close()).To(Succeed()) }()

	var entries []bootstrap.DigestEntry
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		if header.Typeflag == tar.TypeDir {
			entries = append(entries, bootstrap.DigestEntry{Name: strings.TrimSuffix(header.Name, "/"), Directory: true})
			continue
		}
		body, err := io.ReadAll(reader)
		ExpectWithOffset(1, err).NotTo(HaveOccurred())
		entries = append(entries, bootstrap.DigestEntry{Name: header.Name, Content: body})
	}
	var manifest bootstrap.Manifest
	ExpectWithOffset(1, json.Unmarshal(entries[0].Content, &manifest)).To(Succeed())
	return manifest, entries[1:]
}

// manifestJSON marshals a manifest the way the emitter frames it, so a spec can
// hand the reader a manifest it wrote rather than one the emitter produced.
func manifestJSON(manifest bootstrap.Manifest) []byte {
	encoded, err := json.Marshal(manifest)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	return append(encoded, '\n')
}

// write frames members into a gzip tar exactly as the emitter does — fixed
// ownership, timestamp and gzip header — so a spec can hand the reader an
// archive that is not the emitter's, including one whose manifest does not
// digest its payload.
func write(path string, members []bootstrap.DigestEntry) {
	var buffer bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
	ExpectWithOffset(1, err).NotTo(HaveOccurred())
	gzipWriter.Header.ModTime = time.Time{}
	gzipWriter.Header.OS = 255
	tarWriter := tar.NewWriter(gzipWriter)
	for _, member := range members {
		header := &tar.Header{Name: member.Name, Mode: 0o644, ModTime: time.Unix(0, 0).UTC(), Uid: 0, Gid: 0}
		if member.Directory {
			header.Typeflag = tar.TypeDir
			header.Name += "/"
		} else {
			header.Typeflag = tar.TypeReg
			header.Size = int64(len(member.Content))
		}
		ExpectWithOffset(1, tarWriter.WriteHeader(header)).To(Succeed())
		if !member.Directory {
			_, err := tarWriter.Write(member.Content)
			ExpectWithOffset(1, err).NotTo(HaveOccurred())
		}
	}
	ExpectWithOffset(1, tarWriter.Close()).To(Succeed())
	ExpectWithOffset(1, gzipWriter.Close()).To(Succeed())
	ExpectWithOffset(1, os.WriteFile(path, buffer.Bytes(), 0o644)).To(Succeed())
}

// framed writes a manifest and payload entries into an archive at path, the
// manifest first.
func framed(path string, manifest bootstrap.Manifest, entries []bootstrap.DigestEntry) {
	write(path, append([]bootstrap.DigestEntry{{Name: bootstrap.ManifestName, Content: manifestJSON(manifest)}}, entries...))
}

var _ = Describe("the bootstrap archive runner", func() {
	var (
		ctrl     *gomock.Controller
		launcher *MockILauncher
		dir      string
		repo     string
		cache    string
		runner   *archive.Runner
	)

	BeforeEach(func() {
		ctrl = gomock.NewController(GinkgoT())
		dir = GinkgoT().TempDir()
		repo = filepath.Join(dir, "repo")
		cache = filepath.Join(dir, "cache")
		// A repository carrying the entrypoint a bootstrap archive runs.
		Expect(os.MkdirAll(filepath.Join(repo, "bin"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(repo, "bin", "csf"), []byte("#!/bin/sh\n"), 0o755)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(repo, "README.md"), []byte("# repo\n"), 0o644)).To(Succeed())
		launcher = NewMockILauncher(ctrl)
		runner = archive.NewRunner(launcher, cache)
	})

	It("reads the archive the emitter wrote and runs the entrypoint it carries", func() {
		path := emit(repo)
		verified, err := archive.Read(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(verified.Manifest.FormatVersion).To(Equal(bootstrap.FormatVersion))
		Expect(verified.Manifest.Entrypoint).To(Equal(bootstrap.EntrypointName))

		var command proc.Command
		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(
			func(_ context.Context, given proc.Command) (proc.Result, error) {
				command = given
				return proc.Result{ExitCode: 0}, nil
			})

		stdin := strings.NewReader("")
		var stdout, stderr bytes.Buffer
		code, err := runner.Run(context.Background(), path, []string{"status"}, stdin, &stdout, &stderr)
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(0))
		Expect(command.Arguments).To(Equal([]string{"status"}))
		Expect(command.Executable).To(Equal(filepath.Join(cache, verified.Manifest.SHA256, "bin", "csf")))
		Expect(command.Directory).To(Equal(filepath.Join(cache, verified.Manifest.SHA256)))
		Expect(command.Stdin).To(BeIdenticalTo(stdin))
		Expect(command.Stdout).To(BeIdenticalTo(&stdout))

		info, err := os.Stat(command.Executable)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm() & 0o111).NotTo(BeZero())
	})

	It("returns the entrypoint's exit code", func() {
		path := emit(repo)
		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).Return(
			proc.Result{ExitCode: 2}, &proc.ExitError{Executable: "bin/csf", Code: 2})

		code, err := runner.Run(context.Background(), path, nil, nil, io.Discard, io.Discard)
		Expect(err).To(HaveOccurred())
		Expect(code).To(Equal(2))
	})

	It("refuses an archive whose payload no longer matches its manifest", func() {
		manifest, entries := read(emit(repo))
		for i := range entries {
			if !entries[i].Directory {
				entries[i].Content = append(entries[i].Content, []byte("tampered")...)
				break
			}
		}
		tampered := filepath.Join(dir, "tampered.tar.gz")
		framed(tampered, manifest, entries)

		_, err := archive.Read(tampered)
		Expect(err).To(MatchError(ContainSubstring("payload digest")))

		// A runner refuses it too: nothing is extracted and nothing runs.
		_, err = runner.Run(context.Background(), tampered, nil, nil, io.Discard, io.Discard)
		Expect(err).To(MatchError(ContainSubstring("payload digest")))
		Expect(filepath.Join(cache, manifest.SHA256)).NotTo(BeADirectory())
	})

	It("refuses an archive whose first member is not the manifest", func() {
		manifest := bootstrap.Manifest{FormatVersion: bootstrap.FormatVersion, Entrypoint: bootstrap.EntrypointName, Platform: bootstrap.Platforms}
		path := filepath.Join(dir, "misordered.tar.gz")
		write(path, []bootstrap.DigestEntry{
			{Name: "README.md", Content: []byte("# repo\n")},
			{Name: bootstrap.ManifestName, Content: manifestJSON(manifest)},
		})

		_, err := archive.Read(path)
		Expect(err).To(MatchError(ContainSubstring("the first member")))
	})

	It("refuses an archive whose format version it does not know", func() {
		manifest, entries := read(emit(repo))
		manifest.FormatVersion = bootstrap.FormatVersion + 1
		path := filepath.Join(dir, "future.tar.gz")
		framed(path, manifest, entries)

		_, err := archive.Read(path)
		Expect(err).To(MatchError(ContainSubstring("format version")))
	})

	It("refuses an archive this host cannot run", func() {
		manifest, entries := read(emit(repo))
		manifest.Platform = []string{"plan9_arm"}
		path := filepath.Join(dir, "foreign.tar.gz")
		framed(path, manifest, entries)

		_, err := archive.Read(path)
		Expect(err).To(MatchError(ContainSubstring("plan9_arm")))
	})

	It("runs an archive a later release built", func() {
		// The boot sector is frozen: a later format adds members and manifest
		// fields, never changes version 1's. So this reader runs an archive
		// carrying a member and a manifest field it has never heard of.
		manifest, entries := read(emit(repo))
		entries = append(entries,
			bootstrap.DigestEntry{Name: "facts/s1", Content: []byte("rounds 1\n")},
			bootstrap.DigestEntry{Name: "certificate.json", Content: []byte("{}\n")})
		manifest.SHA256 = bootstrap.PayloadDigest(entries)
		later := bytes.Replace(manifestJSON(manifest), []byte("{"), []byte(`{"scoreboard":{"cost_per_fix":0.5},`), 1)
		path := filepath.Join(dir, "later.tar.gz")
		write(path, append([]bootstrap.DigestEntry{{Name: bootstrap.ManifestName, Content: later}}, entries...))

		verified, err := archive.Read(path)
		Expect(err).NotTo(HaveOccurred())
		Expect(verified.Manifest.FormatVersion).To(Equal(bootstrap.FormatVersion))
		Expect(verified.Members).To(ContainElement(HaveField("Name", "certificate.json")))

		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).Return(proc.Result{ExitCode: 0}, nil)
		code, err := runner.Run(context.Background(), path, []string{"serve"}, nil, io.Discard, io.Discard)
		Expect(err).NotTo(HaveOccurred())
		Expect(code).To(Equal(0))
	})

	It("extracts an archive once, however many times it runs", func() {
		path := emit(repo)
		verified, err := archive.Read(path)
		Expect(err).NotTo(HaveOccurred())

		first, err := runner.Extract(verified)
		Expect(err).NotTo(HaveOccurred())
		// A marker in the first extraction survives a second: the cache is keyed
		// by the payload digest and reused, not rebuilt.
		marker := filepath.Join(first, "marker")
		Expect(os.WriteFile(marker, []byte("x"), 0o644)).To(Succeed())

		second, err := runner.Extract(verified)
		Expect(err).NotTo(HaveOccurred())
		Expect(second).To(Equal(first))
		Expect(marker).To(BeAnExistingFile())
	})

	It("refuses a member that escapes the archive root", func() {
		manifest, entries := read(emit(repo))
		entries = append(entries, bootstrap.DigestEntry{Name: "../escape", Content: []byte("x")})
		manifest.SHA256 = bootstrap.PayloadDigest(entries)
		path := filepath.Join(dir, "escaping.tar.gz")
		framed(path, manifest, entries)

		verified, err := archive.Read(path)
		Expect(err).NotTo(HaveOccurred())
		_, err = runner.Extract(verified)
		Expect(err).To(MatchError(ContainSubstring("escapes the archive root")))
		Expect(filepath.Join(dir, "escape")).NotTo(BeAnExistingFile())
	})
})
