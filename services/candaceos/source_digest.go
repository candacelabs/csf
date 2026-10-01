package candaceos

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"google.golang.org/protobuf/proto"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

const (
	appSourceDigestDomainV2 = "candaceos.app-source.digest.v2\x00"
	defaultAppSourceFiles   = 20_000
	defaultAppSourceBytes   = 256 << 20
)

// DefaultAppSourceLimits returns the per-revision materialization policy.
func DefaultAppSourceLimits() *candaceosv1.AppSourceLimits {
	return &candaceosv1.AppSourceLimits{
		MaxFiles: defaultAppSourceFiles,
		MaxBytes: defaultAppSourceBytes,
	}
}

type appSourceBudget struct {
	limits  *candaceosv1.AppSourceLimits
	entries map[string]struct{}
	files   int64
	bytes   int64
}

func newAppSourceBudget(limits *candaceosv1.AppSourceLimits) (*appSourceBudget, error) {
	if err := candaceosv1.ValidateAppSourceLimits(limits); err != nil {
		return nil, err
	}
	return &appSourceBudget{
		limits:  proto.Clone(limits).(*candaceosv1.AppSourceLimits),
		entries: make(map[string]struct{}),
	}, nil
}

func (budget *appSourceBudget) admitPath(relative string) error {
	if err := candaceosv1.ValidateAppSourceEntry(&candaceosv1.AppSourceEntry{Path: relative}); err != nil {
		return fmt.Errorf("app source path %q: %w", relative, err)
	}
	current := ""
	for _, component := range strings.Split(relative, "/") {
		if current == "" {
			current = component
		} else {
			current += "/" + component
		}
		if _, exists := budget.entries[current]; exists {
			continue
		}
		if int64(len(budget.entries)) >= 2*budget.limits.GetMaxFiles() {
			return fmt.Errorf("app source exceeds the %d-entry revision limit", 2*budget.limits.GetMaxFiles())
		}
		budget.entries[current] = struct{}{}
	}
	return nil
}

func (budget *appSourceBudget) admitFile(size int64) error {
	if size < 0 || budget.files >= budget.limits.GetMaxFiles() || size > budget.limits.GetMaxBytes()-budget.bytes {
		return fmt.Errorf(
			"app source exceeds the %d-file or %d-byte revision limit",
			budget.limits.GetMaxFiles(), budget.limits.GetMaxBytes(),
		)
	}
	budget.files++
	budget.bytes += size
	return nil
}

// DigestAppSource returns the canonical digest binding an approval to the
// regular files materialized by a node agent.
func DigestAppSource(ctx context.Context, root string) (string, error) {
	return DigestAppSourceWithLimits(ctx, root, DefaultAppSourceLimits())
}

// DigestAppSourceWithLimits returns the canonical digest under an explicit
// per-revision resource policy.
func DigestAppSourceWithLimits(
	ctx context.Context,
	root string,
	limits *candaceosv1.AppSourceLimits,
) (string, error) {
	budget, err := newAppSourceBudget(limits)
	if err != nil {
		return "", fmt.Errorf("hashing app revision: invalid limits: %w", err)
	}
	rootDirectory, err := os.OpenRoot(root)
	if err != nil {
		return "", fmt.Errorf("hashing app revision: opening source root: %w", err)
	}
	defer func() { _ = rootDirectory.Close() }()
	return digestAppSourceRoot(ctx, rootDirectory, budget)
}

func digestAppSourceRoot(ctx context.Context, rootDirectory *os.Root, budget *appSourceBudget) (string, error) {
	hash := sha256.New()
	_, _ = io.WriteString(hash, appSourceDigestDomainV2)
	walkErr := fs.WalkDir(rootDirectory.FS(), ".", func(relative string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if relative == "." {
			return nil
		}
		if err := budget.admitPath(relative); err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("app source contains unsupported non-regular file %q", relative)
		}
		if err := budget.admitFile(info.Size()); err != nil {
			return err
		}
		return writeAppSourceDigestRecord(hash, rootDirectory, relative, info)
	})
	if walkErr != nil {
		return "", fmt.Errorf("hashing app revision: %w", walkErr)
	}
	return "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func writeAppSourceDigestRecord(destination io.Writer, root *os.Root, relative string, expected fs.FileInfo) error {
	file, err := root.Open(relative)
	if err != nil {
		return err
	}
	defer func() { _ = file.Close() }()

	opened, err := file.Stat()
	if err != nil {
		return err
	}
	if !sameAppSourceFile(opened, expected) {
		return fmt.Errorf("app source file %q changed while hashing", relative)
	}
	_, _ = destination.Write([]byte{1})
	writeDigestUint64(destination, uint64(len(relative)))
	_, _ = io.WriteString(destination, relative)
	if executable(opened.Mode()) {
		_, _ = destination.Write([]byte{1})
	} else {
		_, _ = destination.Write([]byte{0})
	}
	writeDigestUint64(destination, uint64(opened.Size()))
	if _, err := io.CopyN(destination, file, opened.Size()); err != nil {
		return err
	}
	var extra [1]byte
	extraBytes, extraErr := file.Read(extra[:])
	after, statErr := file.Stat()
	if statErr != nil {
		return statErr
	}
	if extraBytes != 0 || extraErr != io.EOF || !sameAppSourceFile(after, opened) {
		return fmt.Errorf("app source file %q changed while hashing", relative)
	}
	return nil
}

func sameAppSourceFile(actual, expected fs.FileInfo) bool {
	return actual.Mode().IsRegular() &&
		actual.Size() == expected.Size() &&
		executable(actual.Mode()) == executable(expected.Mode())
}

func executable(mode fs.FileMode) bool {
	return mode.Perm()&0o111 != 0
}

func writeDigestUint64(destination io.Writer, value uint64) {
	var encoded [8]byte
	binary.BigEndian.PutUint64(encoded[:], value)
	_, _ = destination.Write(encoded[:])
}
