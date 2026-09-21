package agent

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
	telemetryv1 "github.com/candacelabs/csf/proto/candace/telemetry/v1"
	"github.com/candacelabs/csf/services/candaceos"
)

// revisionCachePathHashDomain versions path-derived cache keys. Changing it
// intentionally invalidates existing materialized-path cache names.
const revisionCachePathHashDomain = "candaceos.app-source.path.v1\x00"

func (r *DockerComposeRunner) materializeRevision(ctx context.Context, assignment *candaceosv1.Assignment) (string, error) {
	r.materializeMu.Lock()
	defer r.materializeMu.Unlock()

	target := r.revisionCachePath(assignment)
	reusable, err := reuseOrRemoveInvalidRevision(ctx, target, assignment.GetContentSha256())
	if err != nil {
		return "", err
	}
	if reusable {
		r.logSourceEvent(
			ctx, telemetryv1.Severity_SEVERITY_INFO,
			"agent.source_materialization.reused", "materialized revision reused",
			map[string]string{
				"revision":    assignment.GetSourceRevision(),
				"source_path": assignment.GetPath(),
				"cache_path":  target,
			},
		)
		return target, nil
	}
	entries, bytesUsed, err := revisionCacheUsage(r.revisionRoot, r.revisionLimits)
	if err != nil {
		return "", err
	}
	if entries >= r.revisionLimits.GetMaxEntries() || bytesUsed >= r.revisionLimits.GetMaxBytes() {
		return "", revisionCacheCapacityError(r.revisionRoot, entries, bytesUsed, r.revisionLimits)
	}
	sourceRepository, err := r.sourceRepositoryForRevision(ctx, assignment.GetSourceRevision())
	if err != nil {
		return "", err
	}
	remainingBytes := r.revisionLimits.GetMaxBytes() - bytesUsed
	materializationLimits := candaceos.DefaultAppSourceLimits()
	materializationLimits.MaxBytes = min(remainingBytes, materializationLimits.GetMaxBytes())

	temporary, err := os.MkdirTemp(r.revisionRoot, ".revision-*")
	if err != nil {
		return "", fmt.Errorf("creating app revision staging directory: %w", err)
	}
	defer func() { _ = removeMaterializedTree(temporary) }()
	digest, err := candaceos.MaterializeGitAppSourceWithLimits(
		ctx, r.gitBin, sourceRepository, assignment.GetSourceRevision(), assignment.GetPath(), temporary,
		materializationLimits,
	)
	if err != nil {
		return "", fmt.Errorf(
			"materializing app revision with %d cache bytes remaining; remove unused snapshots under %q while reconciliation is stopped: %w",
			remainingBytes, r.revisionRoot, err,
		)
	}
	if digest != assignment.GetContentSha256() {
		return "", fmt.Errorf(
			"materialized app revision digest mismatch: approved %s, node produced %s",
			assignment.GetContentSha256(), digest,
		)
	}
	if err := sealMaterializedTree(temporary); err != nil {
		return "", err
	}
	installed, err := installMaterializedRevision(ctx, temporary, target, assignment.GetContentSha256())
	if err != nil {
		return "", err
	}
	r.logSourceEvent(
		ctx, telemetryv1.Severity_SEVERITY_INFO,
		"agent.source_materialization.installed", "materialized revision installed",
		map[string]string{
			"repository":  sourceRepository,
			"revision":    assignment.GetSourceRevision(),
			"source_path": assignment.GetPath(),
			"cache_path":  installed,
		},
	)
	return installed, nil
}

func (r *DockerComposeRunner) revisionCachePath(assignment *candaceosv1.Assignment) string {
	pathDigest := sha256.Sum256([]byte(revisionCachePathHashDomain + assignment.GetPath()))
	key := assignment.GetSourceRevision() + "-" + strings.TrimPrefix(assignment.GetContentSha256(), "sha256:") + "-" + hex.EncodeToString(pathDigest[:])
	return filepath.Join(r.revisionRoot, key)
}

func reuseOrRemoveInvalidRevision(ctx context.Context, target, expectedDigest string) (bool, error) {
	_, err := os.Lstat(target)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("stating materialized app revision: %w", err)
	}
	verificationErr := verifyMaterializedRevision(ctx, target, expectedDigest)
	if verificationErr == nil {
		return true, nil
	}
	if err := removeMaterializedTree(target); err != nil {
		return false, fmt.Errorf("repairing invalid cached app revision (%v): %w", verificationErr, err)
	}
	return false, nil
}

func (r *DockerComposeRunner) sourceRepositoryForRevision(ctx context.Context, revision string) (string, error) {
	if r.sourceSync == nil {
		return r.workspace, nil
	}
	if err := r.syncSourceRevision(ctx, revision); err != nil {
		return "", fmt.Errorf("synchronizing app revision: %w", err)
	}
	return r.sourceSync.GetRepository(), nil
}

func installMaterializedRevision(ctx context.Context, temporary, target, expectedDigest string) (string, error) {
	if err := os.Rename(temporary, target); err != nil {
		if _, statErr := os.Lstat(target); statErr == nil {
			return target, verifyMaterializedRevision(ctx, target, expectedDigest)
		}
		return "", fmt.Errorf("installing materialized app revision: %w", err)
	}
	return target, nil
}

func revisionCacheUsage(root string, limits *candaceosv1.RevisionLimits) (int64, int64, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return 0, 0, fmt.Errorf("reading revision cache: %w", err)
	}
	entryCount := int64(len(entries))
	if entryCount >= limits.GetMaxEntries() {
		return entryCount, 0, nil
	}
	var bytesUsed int64
	err = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("revision cache contains unsupported non-regular path %q", path)
		}
		if info.Size() < 0 || info.Size() > limits.GetMaxBytes()-bytesUsed {
			bytesUsed = limits.GetMaxBytes()
			return fs.SkipAll
		}
		bytesUsed += info.Size()
		return nil
	})
	if err != nil {
		return 0, 0, fmt.Errorf("measuring revision cache: %w", err)
	}
	return entryCount, bytesUsed, nil
}

func revisionCacheCapacityError(root string, entries, bytesUsed int64, limits *candaceosv1.RevisionLimits) error {
	return fmt.Errorf(
		"revision cache capacity exhausted (%d/%d entries, %d/%d bytes); remove unused snapshots under %q while reconciliation is stopped",
		entries, limits.GetMaxEntries(), bytesUsed, limits.GetMaxBytes(), root,
	)
}

func verifyMaterializedRevision(ctx context.Context, root, expectedDigest string) error {
	info, err := os.Lstat(root)
	if err != nil {
		return fmt.Errorf("stating cached app revision: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("cached app revision is not a real directory")
	}
	digest, err := candaceos.DigestAppSource(ctx, root)
	if err != nil {
		return fmt.Errorf("verifying cached app revision: %w", err)
	}
	if digest != expectedDigest {
		return fmt.Errorf("cached app revision digest mismatch: approved %s, cached %s", expectedDigest, digest)
	}
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if info.Mode().Perm()&0o222 != 0 {
			return fmt.Errorf("cached app revision contains writable path %q", path)
		}
		return nil
	})
}

func sealMaterializedTree(root string) error {
	var directories []string
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			directories = append(directories, path)
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		mode := os.FileMode(0o444)
		if info.Mode().Perm()&0o111 != 0 {
			mode = 0o555
		}
		return os.Chmod(path, mode)
	})
	if err != nil {
		return fmt.Errorf("sealing materialized app revision: %w", err)
	}
	for index := len(directories) - 1; index >= 0; index-- {
		if err := os.Chmod(directories[index], 0o555); err != nil {
			return fmt.Errorf("sealing materialized app revision: %w", err)
		}
	}
	return nil
}

func removeMaterializedTree(root string) error {
	_ = filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr == nil && entry.IsDir() {
			_ = os.Chmod(path, 0o700)
		}
		return nil
	})
	if err := os.RemoveAll(root); err != nil {
		return fmt.Errorf("removing materialized app revision %q: %w", root, err)
	}
	return nil
}
