// Copyright 2026 Candace Labs

// Package atomicfile replaces a file whole: a reader opening the path sees
// the previous content or the new, never a mix, and after a crash the path
// holds one of the two. It is the one home for that write in this tree; the
// house lint's ATOMIC-WRITE rule refuses a hand-rolled temporary file renamed
// into place anywhere else.
//
// The write is github.com/google/renameio/v2 (a uniquely named temporary
// file, fsync, close, rename) with three choices renameio leaves open made
// for every caller:
//
//   - The mode is exact. renameio.WriteFile keeps the mode of a file already
//     at the path, so a secret once widened to 0644 would stay 0644 however
//     often it is rewritten with 0600; here the mode given is the mode on
//     disk, whatever was there and whatever the umask is.
//   - The temporary file is created beside the target, never in $TMPDIR, so
//     no write probes another mount and the rename never crosses one.
//   - The directory is synced after the rename, so the new name, not only the
//     new content, survives a crash.
package atomicfile

import (
	"os"
	"path/filepath"

	"github.com/google/renameio/v2"
)

// WriteFile replaces path with content at exactly mode. The directory must
// exist. Concurrent writers to one path never collide: each writes its own
// temporary file and the last rename wins whole. On failure nothing is left
// behind and whatever was at path is untouched.
func WriteFile(path string, content []byte, mode os.FileMode) error {
	directory := filepath.Dir(path)
	pending, err := renameio.NewPendingFile(path, renameio.WithTempDir(directory), renameio.WithStaticPermissions(mode))
	if err != nil {
		return err
	}
	defer func() { _ = pending.Cleanup() }()
	if _, err := pending.Write(content); err != nil {
		return err
	}
	if err := pending.CloseAtomicallyReplace(); err != nil {
		return err
	}
	return syncDirectory(directory)
}

// syncDirectory makes a rename inside directory durable.
func syncDirectory(directory string) error {
	handle, err := os.Open(directory)
	if err != nil {
		return err
	}
	syncErr := handle.Sync()
	closeErr := handle.Close()
	if syncErr != nil {
		return syncErr
	}
	return closeErr
}
