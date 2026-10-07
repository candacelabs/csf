// Copyright 2026 Candace Labs

package bootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/candacelabs/csf/pkg/atomicfile"
)

// The archive format is frozen at version 1. A later bootstrapper release may
// add members to an archive, but it never changes what a version 1 manifest
// field means, so a csf binary from any earlier release runs a newer archive.
// The brief calls this the boot sector: frozen forever.
const (
	// FormatVersion is the manifest's version. A reader that does not know a
	// version refuses the archive rather than guess.
	FormatVersion = 1
	// ManifestName is the manifest's member name. It is the archive's first
	// member, so a reader learns the format before it reads any repository
	// byte.
	ManifestName = "manifest.json"
	// EntrypointName is the executable a runner runs, relative to the
	// extracted archive root. It is a manifest field, so a later format may
	// move it without a reader change.
	EntrypointName = "bin/csf"
)

// Platforms are the platforms a version 1 archive may run on, as
// <goos>_<goarch>. A reader refuses an archive it cannot run.
var Platforms = []string{"linux_amd64"}

// Manifest is the frozen version 1 archive manifest. Every field is fixed by
// the format; a later version adds fields, never reinterprets these. SHA256 is
// the digest of the archive's payload — every member but the manifest — so a
// reader refuses a tampered archive before it extracts a byte.
type Manifest struct {
	FormatVersion  int      `json:"format_version"`
	SourceRevision string   `json:"source_revision"`
	Created        string   `json:"created"`
	SHA256         string   `json:"sha256"`
	Entrypoint     string   `json:"entrypoint"`
	Platform       []string `json:"platform"`
}

// DigestEntry is one archive member as the payload digest sees it: its slash
// name, whether it is a directory, and its content (a directory's is empty).
// The emitter and the runner both build these, so the digest each computes is
// one function of the same bytes.
type DigestEntry struct {
	Name      string
	Directory bool
	Content   []byte
}

// PayloadDigest is the frozen framing of the payload digest: each entry
// contributes its name, its kind and its content length, then its content, in
// the slice's order. The emitter sorts members by name, so the digest is a
// function of the tree alone and a rewrite of the manifest cannot be forged
// against altered content.
func PayloadDigest(entries []DigestEntry) string {
	hasher := sha256.New()
	for _, entry := range entries {
		kind := "f"
		if entry.Directory {
			kind = "d"
		}
		fmt.Fprintf(hasher, "%s\x00%s\x00%d\x00", entry.Name, kind, len(entry.Content))
		hasher.Write(entry.Content)
	}
	return hex.EncodeToString(hasher.Sum(nil))
}

// EmitRequest is the emitter's input: the repository to archive, the
// destination beside its parent directory (the loop's archive name), and the
// two manifest facts the tree does not carry — the source revision and the
// created timestamp. Same tree and same two facts is the same bytes.
type EmitRequest struct {
	Repo           string
	Destination    string
	SourceRevision string
	Created        string
}

// Emitter writes a repository into a deterministic archive and returns its
// path.
type Emitter func(ctx context.Context, request EmitRequest) (string, error)

// member is one collected archive member, before it is written.
type member struct {
	DigestEntry
	mode fs.FileMode
}

// TarGz writes the repository at request.Repo as a deterministic tar.gz
// archive at the destination beside its parent directory, and returns that
// path. The archive's first member is the version 1 manifest; the rest is the
// repository's tree, one member per regular file and directory, sorted by
// name, with fixed ownership, timestamps and gzip header. So two runs over the
// same tree, revision and timestamp are byte-identical, and the manifest's
// sha256 refuses an archive altered after it was written. The archive is built
// in memory and written whole through pkg/atomicfile, so it never leaves a
// half-written archive.
func TarGz(ctx context.Context, request EmitRequest) (string, error) {
	root := filepath.Clean(request.Repo)
	members, err := collectMembers(ctx, root)
	if err != nil {
		return "", err
	}
	entries := make([]DigestEntry, 0, len(members))
	for _, m := range members {
		entries = append(entries, m.DigestEntry)
	}
	manifest := Manifest{
		FormatVersion:  FormatVersion,
		SourceRevision: request.SourceRevision,
		Created:        request.Created,
		SHA256:         PayloadDigest(entries),
		Entrypoint:     EntrypointName,
		Platform:       append([]string(nil), Platforms...),
	}
	manifestJSON, err := json.Marshal(manifest)
	if err != nil {
		return "", err
	}
	manifestJSON = append(manifestJSON, '\n')

	var buffer bytes.Buffer
	gzipWriter, err := gzip.NewWriterLevel(&buffer, gzip.BestCompression)
	if err != nil {
		return "", err
	}
	// A fixed gzip header: no name, no timestamp, an unknown OS. The default
	// writer stamps the current time, which would make two runs differ.
	gzipWriter.Header.ModTime = time.Time{}
	gzipWriter.Header.OS = 255

	tarWriter := tar.NewWriter(gzipWriter)
	if err := writeMember(tarWriter, member{DigestEntry: DigestEntry{Name: ManifestName, Content: manifestJSON}, mode: 0o644}); err != nil {
		return "", err
	}
	for _, m := range members {
		if err := writeMember(tarWriter, m); err != nil {
			return "", err
		}
	}
	if err := tarWriter.Close(); err != nil {
		return "", err
	}
	if err := gzipWriter.Close(); err != nil {
		return "", err
	}

	target := filepath.Join(filepath.Dir(root), request.Destination)
	if err := atomicfile.WriteFile(target, buffer.Bytes(), 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", target, err)
	}
	return target, nil
}

// collectMembers walks the repository and returns its regular files and
// directories as members sorted by name. A bootstrap repository is a tree of
// regular files — the same shape [Loop.snapshot] copies — so any other entry,
// a symlink or a device, is refused and the archive and the snapshot always
// carry the same tree.
func collectMembers(ctx context.Context, root string) ([]member, error) {
	var members []member
	err := filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		relative, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		name := filepath.ToSlash(relative)
		switch {
		case entry.IsDir():
			members = append(members, member{DigestEntry: DigestEntry{Name: name, Directory: true}, mode: 0o755})
			return nil
		case entry.Type().IsRegular():
			content, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			// Preserve only the executable bit: a file's other permission bits
			// are not part of the archive's meaning, and normalizing them keeps
			// the archive a function of the tree's content.
			mode := fs.FileMode(0o644)
			if info.Mode().Perm()&0o111 != 0 {
				mode = 0o755
			}
			members = append(members, member{DigestEntry: DigestEntry{Name: name, Content: content}, mode: mode})
			return nil
		default:
			return fmt.Errorf("archive member %s: only regular files and directories are archived", name)
		}
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(members, func(i, j int) bool { return members[i].Name < members[j].Name })
	return members, nil
}

// writeMember writes one member with fixed ownership and timestamp, so the
// archive is a function of the tree alone.
func writeMember(writer *tar.Writer, m member) error {
	header := &tar.Header{
		Name:    m.Name,
		Mode:    int64(m.mode.Perm()),
		ModTime: time.Unix(0, 0).UTC(),
		Uid:     0,
		Gid:     0,
	}
	if m.Directory {
		header.Typeflag = tar.TypeDir
		header.Name += "/"
	} else {
		header.Typeflag = tar.TypeReg
		header.Size = int64(len(m.Content))
	}
	if err := writer.WriteHeader(header); err != nil {
		return err
	}
	if !m.Directory {
		if _, err := writer.Write(m.Content); err != nil {
			return err
		}
	}
	return nil
}
