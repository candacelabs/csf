// Copyright 2026 Candace Labs

// Package fs is the file capability: the kernel I/O tier's file boundary.
// Reading a file crosses a system call out of the process's shared memory, so
// a service reads files from the host only through a capability granted by
// the binary that owns the process.
//
// A binary constructs a [HostFiles] for one directory and passes it, as an
// [IFiles], to whatever needs it; a service receives the capability in its
// constructor and never calls os.Open, os.ReadFile or os.DirFS itself. Files
// compiled into the binary with go:embed cross no boundary and need no
// capability. The interface has the standard library's io/fs shape, so an
// in-memory tree (testing/fstest.MapFS) or an embedded one (embed.FS, or an
// io/fs.Sub of either) is granted the same way.
//
// The capability is read-only: it has no operation that creates, writes,
// renames or removes a file. [HostFiles] also reads symbolic links
// (io/fs.ReadLinkFS), which a process table granted as /proc needs.
package fs

import (
	"errors"
	"fmt"
	stdfs "io/fs"
	"os"
	"path/filepath"
	"syscall"
)

// IFiles is read-only access to one tree of files, addressed by the
// slash-separated, unrooted names io/fs defines: opening a file, reading a
// whole file and listing a directory.
type IFiles interface {
	stdfs.ReadFileFS
	stdfs.ReadDirFS
}

var (
	// ErrNoDirectory is returned by [NewHostFiles] for an empty directory
	// name, which would otherwise silently grant the working directory.
	ErrNoDirectory = errors.New("ipc/fs: no directory named")
	// ErrNotDirectory is returned by [NewHostFiles] for a path that exists
	// but is not a directory.
	ErrNotDirectory = errors.New("ipc/fs: not a directory")
	// ErrNotGranted is returned by every operation on a HostFiles that
	// [NewHostFiles] did not construct, such as a nil pointer or a zero value.
	ErrNotGranted = errors.New("ipc/fs: no directory granted; construct HostFiles with NewHostFiles")
)

// HostFiles is one directory of this host's filesystem, granted as a
// read-only capability over os.DirFS. Names are resolved beneath the
// directory and io/fs rejects ".." and rooted names with io/fs.ErrInvalid;
// like os.DirFS it follows symbolic links, so whoever grants a directory
// vouches for the links inside it. It holds no open descriptor and needs no
// closing.
type HostFiles struct {
	directory string
	files     stdfs.FS
}

// NewHostFiles grants read access to directory, which must exist and be a
// directory. A relative directory is resolved against the working directory
// once, here, so a later change of working directory does not move the grant.
func NewHostFiles(directory string) (*HostFiles, error) {
	if directory == "" {
		return nil, ErrNoDirectory
	}
	absolute, err := filepath.Abs(directory)
	if err != nil {
		return nil, fmt.Errorf("ipc/fs: resolve %s: %w", directory, err)
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return nil, fmt.Errorf("ipc/fs: grant %s: %w", absolute, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: %s", ErrNotDirectory, absolute)
	}
	return &HostFiles{directory: absolute, files: os.DirFS(absolute)}, nil
}

// Directory is the absolute host directory the capability reads, or empty
// when nothing was granted.
func (files *HostFiles) Directory() string {
	if !files.granted() {
		return ""
	}
	return files.directory
}

// Open opens the named file for reading.
func (files *HostFiles) Open(name string) (stdfs.File, error) {
	if !files.granted() {
		return nil, ErrNotGranted
	}
	file, err := files.files.Open(name)
	if err != nil {
		return nil, fmt.Errorf("ipc/fs: open %s in %s: %w", name, files.directory, err)
	}
	return file, nil
}

// ReadFile reads the whole named file.
func (files *HostFiles) ReadFile(name string) ([]byte, error) {
	if !files.granted() {
		return nil, ErrNotGranted
	}
	content, err := stdfs.ReadFile(files.files, name)
	if err != nil {
		return nil, fmt.Errorf("ipc/fs: read %s in %s: %w", name, files.directory, err)
	}
	return content, nil
}

// ReadDir lists the named directory, sorted by file name.
func (files *HostFiles) ReadDir(name string) ([]stdfs.DirEntry, error) {
	if !files.granted() {
		return nil, ErrNotGranted
	}
	entries, err := stdfs.ReadDir(files.files, name)
	if err != nil {
		return nil, fmt.Errorf("ipc/fs: list %s in %s: %w", name, files.directory, err)
	}
	return entries, nil
}

// ReadLink returns the destination of the named symbolic link, unresolved.
func (files *HostFiles) ReadLink(name string) (string, error) {
	if !files.granted() {
		return "", ErrNotGranted
	}
	target, err := stdfs.ReadLink(files.files, name)
	if err != nil {
		return "", fmt.Errorf("ipc/fs: read link %s in %s: %w", name, files.directory, err)
	}
	return target, nil
}

// Lstat describes the named file without following a final symbolic link.
func (files *HostFiles) Lstat(name string) (stdfs.FileInfo, error) {
	if !files.granted() {
		return nil, ErrNotGranted
	}
	info, err := stdfs.Lstat(files.files, name)
	if err != nil {
		return nil, fmt.Errorf("ipc/fs: lstat %s in %s: %w", name, files.directory, err)
	}
	return info, nil
}

// DiskUsage is the size of a filesystem and the space left on it.
type DiskUsage struct {
	TotalBytes uint64
	// FreeBytes is what an unprivileged process may still write.
	FreeBytes uint64
}

// DiskUsage asks the kernel about the filesystem holding the granted
// directory.
func (files *HostFiles) DiskUsage() (DiskUsage, error) {
	if !files.granted() {
		return DiskUsage{}, ErrNotGranted
	}
	var stat syscall.Statfs_t
	if err := syscall.Statfs(files.directory, &stat); err != nil {
		return DiskUsage{}, fmt.Errorf("ipc/fs: statfs %s: %w", files.directory, err)
	}
	return DiskUsage{TotalBytes: stat.Blocks * uint64(stat.Bsize), FreeBytes: stat.Bavail * uint64(stat.Bsize)}, nil
}

// granted reports whether NewHostFiles built this value.
func (files *HostFiles) granted() bool { return files != nil && files.files != nil }
