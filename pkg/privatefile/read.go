// Package privatefile reads bounded, owner-only configuration and secret files.
package privatefile

import (
	"errors"
	"io"
	"math"
	"os"
)

// Read reads a regular file whose permission bits deny group and other access.
// Symlinks are allowed; the opened target is checked. This does not inspect ACLs
// or establish file ownership. Errors omit paths and file contents so callers
// can safely add their own configuration context.
func Read(path string, maxBytes int64) ([]byte, error) {
	if maxBytes <= 0 || maxBytes == math.MaxInt64 {
		return nil, errors.New("private file size limit is invalid")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("private file cannot be opened")
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 {
		return nil, errors.New("private file must be regular with owner-only permissions")
	}
	content, err := io.ReadAll(io.LimitReader(file, maxBytes+1))
	if err != nil || int64(len(content)) > maxBytes {
		return nil, errors.New("private file cannot be read within the size limit")
	}
	return content, nil
}
