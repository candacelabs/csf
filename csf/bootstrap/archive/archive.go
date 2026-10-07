// Copyright 2026 Candace Labs

// Package archive runs a bootstrap archive: it verifies a version 1 archive,
// extracts it into a cache keyed by its payload digest, and runs the
// entrypoint it carries. It is the reader side of services/bootstrap's
// emitter, so a csf binary of this release runs a newer archive — the boot
// sector the brief freezes.
//
// The format is frozen at version 1. A later format adds members or manifest
// fields; it never changes a field this reader reads, so a reader built today
// runs an archive built later.
package archive

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/candacelabs/csf/io/ipc/proc"
	"github.com/candacelabs/csf/pkg/atomicfile"
	"github.com/candacelabs/csf/services/bootstrap"
)

// Member is one archive member after reading: its slash name, whether it is a
// directory, its permission bits and its content (a directory's is empty).
type Member struct {
	Name      string
	Directory bool
	Mode      fs.FileMode
	Content   []byte
}

// Archive is a verified version 1 bootstrap archive: its frozen manifest and
// the payload members the manifest's digest covers, in the order the archive
// carried them. The manifest itself is not a payload member.
type Archive struct {
	Manifest bootstrap.Manifest
	Members  []Member
}

// Read verifies the archive at path and returns it. It refuses an archive whose
// first member is not the version 1 manifest, whose format version this reader
// does not know, whose payload digest is not the manifest's, or whose platform
// this host cannot run — all before any byte is extracted.
func Read(path string) (*Archive, error) {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	gzipReader, err := gzip.NewReader(bytes.NewReader(content))
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	defer gzipReader.Close()

	var members []Member
	reader := tar.NewReader(gzipReader)
	for {
		header, err := reader.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		mode := fs.FileMode(header.Mode).Perm()
		if header.Typeflag == tar.TypeDir {
			members = append(members, Member{
				Name:      strings.TrimSuffix(header.Name, "/"),
				Directory: true,
				Mode:      mode,
			})
			continue
		}
		body, err := io.ReadAll(reader)
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", path, err)
		}
		members = append(members, Member{Name: header.Name, Mode: mode, Content: body})
	}
	if len(members) == 0 || members[0].Name != bootstrap.ManifestName {
		return nil, fmt.Errorf("read %s: the first member is not %s", path, bootstrap.ManifestName)
	}

	var manifest bootstrap.Manifest
	if err := json.Unmarshal(members[0].Content, &manifest); err != nil {
		return nil, fmt.Errorf("read %s: manifest: %w", path, err)
	}
	if manifest.FormatVersion != bootstrap.FormatVersion {
		return nil, fmt.Errorf("read %s: format version %d, this reader knows %d", path, manifest.FormatVersion, bootstrap.FormatVersion)
	}
	payload := members[1:]
	if digest := bootstrap.PayloadDigest(digestEntries(payload)); digest != manifest.SHA256 {
		return nil, fmt.Errorf("read %s: payload digest %s does not match the manifest's %s", path, digest, manifest.SHA256)
	}
	if err := runnable(manifest); err != nil {
		return nil, fmt.Errorf("read %s: %w", path, err)
	}
	return &Archive{Manifest: manifest, Members: payload}, nil
}

// digestEntries is the payload as the digest sees it: name, kind and content,
// the same shape the emitter digests.
func digestEntries(members []Member) []bootstrap.DigestEntry {
	entries := make([]bootstrap.DigestEntry, 0, len(members))
	for _, member := range members {
		entries = append(entries, bootstrap.DigestEntry{
			Name:      member.Name,
			Directory: member.Directory,
			Content:   member.Content,
		})
	}
	return entries
}

// runnable refuses an archive whose declared platforms exclude this host.
func runnable(manifest bootstrap.Manifest) error {
	platform := runtime.GOOS + "_" + runtime.GOARCH
	for _, candidate := range manifest.Platform {
		if candidate == platform {
			return nil
		}
	}
	return fmt.Errorf("archive runs on %v, not on %s", manifest.Platform, platform)
}

// Runner verifies, extracts and runs bootstrap archives.
type Runner struct {
	launcher proc.ILauncher
	cache    string
}

// NewRunner returns a runner that runs entrypoints through launcher and
// extracts archives under cache.
func NewRunner(launcher proc.ILauncher, cache string) *Runner {
	return &Runner{launcher: launcher, cache: cache}
}

// Run verifies the archive at path, extracts it into the cache and runs its
// entrypoint with args, wiring the caller's streams through, and returns the
// entrypoint's exit code. The archive is verified before anything is extracted,
// so a tampered archive runs nothing.
func (r *Runner) Run(ctx context.Context, path string, args []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	if r.launcher == nil {
		return -1, fmt.Errorf("archive runner has no launcher")
	}
	verified, err := Read(path)
	if err != nil {
		return -1, err
	}
	root, err := r.Extract(verified)
	if err != nil {
		return -1, err
	}
	entrypoint := filepath.Join(root, filepath.FromSlash(verified.Manifest.Entrypoint))
	result, err := r.launcher.Run(ctx, proc.Command{
		Executable: entrypoint,
		Arguments:  args,
		Directory:  root,
		Stdin:      stdin,
		Stdout:     stdout,
		Stderr:     stderr,
	})
	if err != nil {
		return result.ExitCode, err
	}
	return result.ExitCode, nil
}

// Extract writes the archive's members under the runner's cache, in a directory
// named by the payload digest, and returns that directory. Extracting the same
// archive twice is one directory: a second call reuses the first. The tree is
// built under a temporary sibling and renamed into place, so a reader never
// sees a half-extracted archive, and an archive that escapes its root — a name
// that is absolute or climbs out with ".." — is refused rather than written.
func (r *Runner) Extract(verified *Archive) (string, error) {
	if r.cache == "" {
		return "", fmt.Errorf("archive runner has no cache")
	}
	root := filepath.Join(r.cache, verified.Manifest.SHA256)
	if info, err := os.Stat(root); err == nil && info.IsDir() {
		return root, nil
	}
	if err := os.MkdirAll(r.cache, 0o755); err != nil {
		return "", err
	}
	temporary, err := os.MkdirTemp(r.cache, verified.Manifest.SHA256+".partial-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(temporary)

	for _, member := range verified.Members {
		target, err := safeJoin(temporary, member.Name)
		if err != nil {
			return "", err
		}
		if member.Directory {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return "", err
			}
			continue
		}
		// A file's parent may not appear as its own member; create it.
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return "", err
		}
		if err := atomicfile.WriteFile(target, member.Content, memberMode(member.Mode)); err != nil {
			return "", err
		}
	}
	if err := os.Rename(temporary, root); err != nil {
		// Another extractor won the rename: its tree is whole, use it.
		if info, statErr := os.Stat(root); statErr == nil && info.IsDir() {
			return root, nil
		}
		return "", err
	}
	return root, nil
}

// memberMode keeps the executable bit and normalizes the rest, so an extracted
// entrypoint runs and a file without a mode still gets a readable one.
func memberMode(mode fs.FileMode) fs.FileMode {
	if mode.Perm()&0o111 != 0 {
		return 0o755
	}
	return 0o644
}

// safeJoin joins a member's slash name under root, refusing a name that is
// absolute or escapes root. An archive is data, so a member named ../x or
// /etc/x is refused rather than written outside the cache.
func safeJoin(root, name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("archive member has an empty name")
	}
	cleaned := path.Clean(name)
	if path.IsAbs(cleaned) || cleaned == ".." || strings.HasPrefix(cleaned, "../") {
		return "", fmt.Errorf("archive member %q escapes the archive root", name)
	}
	return filepath.Join(root, filepath.FromSlash(cleaned)), nil
}
