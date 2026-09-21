// Package worktreeadapter implements configured git repository and worktree
// operations for the Copilot adapter.
package worktreeadapter

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/candacelabs/csf/pkg/boundedbuffer"
	boundedbufferv1 "github.com/candacelabs/csf/pkg/boundedbuffer/v1"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
)

const (
	defaultPatchBytes  = 1 << 20
	defaultStatusBytes = 1 << 20
	commandErrorBytes  = 8 << 10
	gitAbsolutePaths   = "--path-format=absolute"
	gitCommonDirectory = "--git-common-dir"
	gitTopLevel        = "--show-toplevel"
	managedPrefix      = "chat-"
	managedBranch      = "codex/chat-"
)

// Config declares the only repository roots the browser may select.
type Config struct {
	Repositories []copilotadapter.Repository
	WorktreeRoot string
	PatchBytes   int
}

// WorktreeManager is the concrete git/filesystem boundary.
type WorktreeManager struct {
	repositories []copilotadapter.Repository
	byID         map[string]copilotadapter.Repository
	worktreeRoot string
	patchBytes   int
}

var _ copilotadapter.IWorktreeManager = (*WorktreeManager)(nil)

// NewWorktreeManager validates repository roots and the managed-worktree root.
func NewWorktreeManager(config Config) (*WorktreeManager, error) {
	if len(config.Repositories) == 0 {
		return nil, fmt.Errorf("worktree adapter: at least one repository is required")
	}
	root, err := filepath.Abs(config.WorktreeRoot)
	if err != nil || config.WorktreeRoot == "" {
		return nil, fmt.Errorf("worktree adapter: absolute worktree root is required")
	}
	root, err = canonicalPath(root)
	if err != nil {
		return nil, fmt.Errorf("worktree adapter: canonical worktree root: %w", err)
	}
	manager := &WorktreeManager{
		repositories: make([]copilotadapter.Repository, 0, len(config.Repositories)),
		byID:         map[string]copilotadapter.Repository{}, worktreeRoot: root, patchBytes: config.PatchBytes,
	}
	if manager.patchBytes <= 0 {
		manager.patchBytes = defaultPatchBytes
	}
	for _, repository := range config.Repositories {
		if repository.ID == "" || repository.DisplayName == "" || repository.Root == "" || repository.DefaultRef == "" {
			return nil, fmt.Errorf("worktree adapter: each repository needs id, display name, root and default ref")
		}
		if _, duplicate := manager.byID[repository.ID]; duplicate {
			return nil, fmt.Errorf("worktree adapter: duplicate repository id %q", repository.ID)
		}
		canonical, err := filepath.EvalSymlinks(repository.Root)
		if err != nil {
			return nil, fmt.Errorf("worktree adapter: repository %q: %w", repository.ID, err)
		}
		canonical, err = filepath.Abs(canonical)
		if err != nil {
			return nil, err
		}
		repository.Root = canonical
		manager.repositories = append(manager.repositories, repository)
		manager.byID[repository.ID] = repository
	}
	return manager, nil
}

// Repositories returns a defensive copy of configured roots.
func (manager *WorktreeManager) Repositories() []copilotadapter.Repository {
	return append([]copilotadapter.Repository(nil), manager.repositories...)
}

// Prepare validates the ref and creates a managed git worktree by default.
func (manager *WorktreeManager) Prepare(ctx context.Context, request copilotadapter.WorktreeRequest) (copilotadapter.PreparedWorktree, error) {
	repository, found := manager.byID[request.RepositoryID]
	if !found {
		return copilotadapter.PreparedWorktree{}, fmt.Errorf("worktree adapter: unknown repository %q", request.RepositoryID)
	}
	if request.Mode == "reuseCurrentWorktree" {
		if request.BaseRef != "" {
			return copilotadapter.PreparedWorktree{}, fmt.Errorf("worktree adapter: baseRef is only valid for a new worktree")
		}
		return copilotadapter.PreparedWorktree{Repository: repository, Path: repository.Root, BaseRef: "HEAD"}, nil
	}
	if request.Mode != "newWorktree" {
		return copilotadapter.PreparedWorktree{}, fmt.Errorf("worktree adapter: unsupported mode %q", request.Mode)
	}
	baseRef := request.BaseRef
	if baseRef == "" {
		baseRef = repository.DefaultRef
	}
	if strings.HasPrefix(baseRef, "-") {
		return copilotadapter.PreparedWorktree{}, fmt.Errorf("worktree adapter: invalid base ref")
	}
	if err := os.MkdirAll(manager.worktreeRoot, 0o750); err != nil {
		return copilotadapter.PreparedWorktree{}, fmt.Errorf("worktree adapter: create managed root: %w", err)
	}
	canonicalRoot, err := filepath.EvalSymlinks(manager.worktreeRoot)
	if err != nil {
		return copilotadapter.PreparedWorktree{}, fmt.Errorf("worktree adapter: verify managed root: %w", err)
	}
	if canonicalRoot != manager.worktreeRoot {
		return copilotadapter.PreparedWorktree{}, fmt.Errorf("%w: managed root changed after configuration", copilotadapter.ErrInvalidWorktree)
	}
	identifier := request.SessionID.String()
	path := filepath.Join(manager.worktreeRoot, managedPrefix+identifier)
	branch := managedBranch + identifier
	if err := manager.ensureManagedWorktree(ctx, repository, path, branch, baseRef); err != nil {
		return copilotadapter.PreparedWorktree{}, err
	}
	return copilotadapter.PreparedWorktree{
		Repository: repository, Path: path, BaseRef: baseRef, Managed: true,
	}, nil
}

// ensureManagedWorktree creates the deterministic worktree once or adopts the
// exact residue left by an interrupted creation. A path or branch with any
// other identity is refused rather than silently repurposed.
func (manager *WorktreeManager) ensureManagedWorktree(
	ctx context.Context,
	repository copilotadapter.Repository,
	path string,
	branch string,
	baseRef string,
) error {
	branchOutput, err := manager.git(ctx, repository.Root, "for-each-ref", "--count=1", "--format=%(objectname)", "refs/heads/"+branch)
	if err != nil {
		return fmt.Errorf("worktree adapter: inspect managed branch: %w", err)
	}
	branchCommit := strings.TrimSpace(string(branchOutput))
	pathExists := false
	if _, statErr := os.Lstat(path); statErr == nil {
		pathExists = true
	} else if !os.IsNotExist(statErr) {
		return fmt.Errorf("worktree adapter: inspect managed path: %w", statErr)
	}
	if pathExists {
		return manager.validateManagedResidue(ctx, repository, path, branch)
	}
	if branchCommit != "" {
		if _, err := manager.git(ctx, repository.Root, "worktree", "add", path, branch); err != nil {
			if reconcileErr := manager.validateManagedResidue(ctx, repository, path, branch); reconcileErr != nil {
				return errors.Join(fmt.Errorf("worktree adapter: restore managed worktree: %w", err), reconcileErr)
			}
			return nil
		}
		return manager.validateManagedResidue(ctx, repository, path, branch)
	}
	if _, err := manager.git(ctx, repository.Root, "rev-parse", "--verify", baseRef+"^{commit}"); err != nil {
		return fmt.Errorf("worktree adapter: resolve base ref %q: %w", baseRef, err)
	}
	if _, err := manager.git(ctx, repository.Root, "worktree", "add", "-b", branch, path, baseRef); err != nil {
		if reconcileErr := manager.validateManagedResidue(ctx, repository, path, branch); reconcileErr != nil {
			return errors.Join(fmt.Errorf("worktree adapter: create worktree: %w", err), reconcileErr)
		}
		return nil
	}
	return manager.validateManagedResidue(ctx, repository, path, branch)
}

func (manager *WorktreeManager) validateManagedResidue(
	ctx context.Context,
	repository copilotadapter.Repository,
	path string,
	branch string,
) error {
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		return fmt.Errorf("%w: resolve managed worktree: %v", copilotadapter.ErrInvalidWorktree, err)
	}
	absolute, err := filepath.Abs(path)
	if err != nil || canonical != filepath.Clean(absolute) {
		return fmt.Errorf("%w: managed path resolves outside its deterministic identity", copilotadapter.ErrInvalidWorktree)
	}
	listing, truncated, err := manager.gitBounded(ctx, repository.Root, defaultStatusBytes, "worktree", "list", "--porcelain")
	if err != nil {
		return err
	}
	registered, prunable := worktreeListingStatus(listing, canonical)
	if truncated || !registered {
		return fmt.Errorf("%w: managed path is not the registered deterministic worktree", copilotadapter.ErrInvalidWorktree)
	}
	if prunable {
		return fmt.Errorf("%w: managed path has a prunable Git registration", copilotadapter.ErrInvalidWorktree)
	}
	if err := manager.validateManagedIdentity(ctx, repository, canonical, branch); err != nil {
		return err
	}
	return nil
}

// canonicalPath resolves every existing symlink while preserving a
// not-yet-created suffix. Managed roots commonly end in a session-specific
// directory, so EvalSymlinks on the whole configured path is insufficient.
func canonicalPath(path string) (string, error) {
	current := path
	missing := make([]string, 0)
	for {
		canonical, err := filepath.EvalSymlinks(current)
		if err == nil {
			for index := len(missing) - 1; index >= 0; index-- {
				canonical = filepath.Join(canonical, missing[index])
			}
			return filepath.Clean(canonical), nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		parent := filepath.Dir(current)
		if parent == current {
			return "", err
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// Reuse accepts only the configured checkout or a path Git reports as one of
// that repository's worktrees beneath the managed root. The path comes from
// server persistence, but legacy rows are deliberately treated as untrusted.
func (manager *WorktreeManager) Reuse(ctx context.Context, repositoryID string, path string) (copilotadapter.PreparedWorktree, error) {
	repository, found := manager.byID[repositoryID]
	if !found {
		return copilotadapter.PreparedWorktree{}, fmt.Errorf("%w: unknown repository %q", copilotadapter.ErrInvalidWorktree, repositoryID)
	}
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		if os.IsNotExist(err) {
			return copilotadapter.PreparedWorktree{}, fmt.Errorf("%w: existing path is missing", copilotadapter.ErrInvalidWorktree)
		}
		return copilotadapter.PreparedWorktree{}, fmt.Errorf("worktree adapter: existing path: %w", err)
	}
	canonical, err = filepath.Abs(canonical)
	if err != nil {
		return copilotadapter.PreparedWorktree{}, err
	}
	if canonical == repository.Root {
		return copilotadapter.PreparedWorktree{Repository: repository, Path: canonical, BaseRef: "HEAD"}, nil
	}
	relative, err := filepath.Rel(manager.worktreeRoot, canonical)
	if err != nil {
		return copilotadapter.PreparedWorktree{}, fmt.Errorf("worktree adapter: compare existing path to managed root: %w", err)
	}
	if relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return copilotadapter.PreparedWorktree{}, fmt.Errorf("%w: existing path is outside the managed root", copilotadapter.ErrInvalidWorktree)
	}
	listing, truncated, err := manager.gitBounded(ctx, repository.Root, defaultStatusBytes, "worktree", "list", "--porcelain")
	if err != nil {
		return copilotadapter.PreparedWorktree{}, err
	}
	if truncated {
		return copilotadapter.PreparedWorktree{}, fmt.Errorf("worktree adapter: registered worktree listing exceeded its validation bound")
	}
	registered, prunable := worktreeListingStatus(listing, canonical)
	if !registered {
		return copilotadapter.PreparedWorktree{}, fmt.Errorf("%w: existing path is not a registered managed worktree", copilotadapter.ErrInvalidWorktree)
	}
	if prunable {
		return copilotadapter.PreparedWorktree{}, fmt.Errorf("%w: existing path has a prunable Git registration", copilotadapter.ErrInvalidWorktree)
	}
	name := filepath.Base(canonical)
	if !strings.HasPrefix(name, managedPrefix) || name == managedPrefix {
		return copilotadapter.PreparedWorktree{}, fmt.Errorf("%w: existing path has no deterministic managed identity", copilotadapter.ErrInvalidWorktree)
	}
	if err := manager.validateManagedIdentity(ctx, repository, canonical, managedBranch+strings.TrimPrefix(name, managedPrefix)); err != nil {
		return copilotadapter.PreparedWorktree{}, err
	}
	return copilotadapter.PreparedWorktree{Repository: repository, Path: canonical, BaseRef: "HEAD", Managed: true}, nil
}

func (manager *WorktreeManager) validateManagedIdentity(
	ctx context.Context,
	repository copilotadapter.Repository,
	path string,
	branch string,
) error {
	topLevel, err := manager.canonicalGitPath(ctx, path, gitTopLevel)
	if err != nil {
		return fmt.Errorf("worktree adapter: inspect managed worktree top-level: %w", err)
	}
	if topLevel != path {
		return fmt.Errorf("%w: managed path is not its Git worktree top-level", copilotadapter.ErrInvalidWorktree)
	}
	expectedCommonDirectory, err := manager.canonicalGitPath(ctx, repository.Root, gitCommonDirectory)
	if err != nil {
		return fmt.Errorf("worktree adapter: inspect repository Git common directory: %w", err)
	}
	actualCommonDirectory, err := manager.canonicalGitPath(ctx, path, gitCommonDirectory)
	if err != nil {
		return fmt.Errorf("worktree adapter: inspect managed Git common directory: %w", err)
	}
	if actualCommonDirectory != expectedCommonDirectory {
		return fmt.Errorf("%w: managed path belongs to another Git common directory", copilotadapter.ErrInvalidWorktree)
	}
	actualBranch, err := manager.git(ctx, path, "branch", "--show-current")
	if err != nil {
		return fmt.Errorf("worktree adapter: inspect managed worktree branch: %w", err)
	}
	if strings.TrimSpace(string(actualBranch)) != branch {
		return fmt.Errorf("%w: managed worktree uses branch %q, expected %q", copilotadapter.ErrInvalidWorktree, strings.TrimSpace(string(actualBranch)), branch)
	}
	return nil
}

func (manager *WorktreeManager) canonicalGitPath(ctx context.Context, directory string, argument string) (string, error) {
	output, err := manager.git(ctx, directory, "rev-parse", gitAbsolutePaths, argument)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(strings.TrimSpace(string(output)))
	if err != nil {
		return "", err
	}
	return filepath.Abs(canonical)
}

// Inspect returns fresh git metadata or a missing state when the path vanished.
func (manager *WorktreeManager) Inspect(ctx context.Context, path string) (copilotadapter.WorktreeSnapshot, error) {
	if _, err := os.Stat(path); errorsIsNotExist(err) {
		return copilotadapter.WorktreeSnapshot{State: "missing"}, nil
	} else if err != nil {
		return copilotadapter.WorktreeSnapshot{}, err
	}
	head, err := manager.git(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return copilotadapter.WorktreeSnapshot{}, worktreeCommandError(path, err)
	}
	branch, err := manager.git(ctx, path, "branch", "--show-current")
	if err != nil {
		return copilotadapter.WorktreeSnapshot{}, worktreeCommandError(path, err)
	}
	status, _, err := manager.gitBounded(ctx, path, 1, "status", "--porcelain=v1")
	if err != nil {
		return copilotadapter.WorktreeSnapshot{}, worktreeCommandError(path, err)
	}
	return copilotadapter.WorktreeSnapshot{
		Branch: strings.TrimSpace(string(branch)), HeadSHA: strings.TrimSpace(string(head)),
		Clean: len(status) == 0, State: "active",
	}, nil
}

// Changes returns porcelain file states and a bounded binary-capable patch.
func (manager *WorktreeManager) Changes(ctx context.Context, path string) (copilotadapter.WorktreeChanges, error) {
	snapshot, err := manager.Inspect(ctx, path)
	if err != nil {
		return copilotadapter.WorktreeChanges{}, err
	}
	if snapshot.State == "missing" {
		return copilotadapter.WorktreeChanges{}, fmt.Errorf("%w: worktree path is missing", copilotadapter.ErrInvalidWorktree)
	}
	changes, stable, err := manager.captureChanges(ctx, path)
	if err != nil {
		return copilotadapter.WorktreeChanges{}, err
	}
	if stable {
		return changes, nil
	}
	changes, stable, err = manager.captureChanges(ctx, path)
	if err != nil {
		return copilotadapter.WorktreeChanges{}, err
	}
	if stable {
		return changes, nil
	}
	return copilotadapter.WorktreeChanges{}, fmt.Errorf("worktree adapter: HEAD changed during both attempts to capture changes")
}

func (manager *WorktreeManager) captureChanges(ctx context.Context, path string) (copilotadapter.WorktreeChanges, bool, error) {
	head, err := manager.git(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return copilotadapter.WorktreeChanges{}, false, worktreeCommandError(path, err)
	}
	headSHA := strings.TrimSpace(string(head))
	status, statusTruncated, err := manager.gitBounded(ctx, path, defaultStatusBytes, "status", "--porcelain=v1", "-z")
	if err != nil {
		return copilotadapter.WorktreeChanges{}, false, worktreeCommandError(path, err)
	}
	patch, patchTruncated, err := manager.gitBounded(ctx, path, manager.patchBytes, "diff", headSHA, "--no-ext-diff", "--binary")
	if err != nil {
		return copilotadapter.WorktreeChanges{}, false, worktreeCommandError(path, err)
	}
	untracked, untrackedListTruncated, err := manager.gitBounded(ctx, path, defaultStatusBytes, "ls-files", "--others", "--exclude-standard", "-z")
	if err != nil {
		return copilotadapter.WorktreeChanges{}, false, worktreeCommandError(path, err)
	}
	patch, untrackedPatchTruncated, err := manager.appendUntrackedPatches(ctx, path, patch, untracked)
	if err != nil {
		return copilotadapter.WorktreeChanges{}, false, worktreeCommandError(path, err)
	}
	finalHead, err := manager.git(ctx, path, "rev-parse", "HEAD")
	if err != nil {
		return copilotadapter.WorktreeChanges{}, false, worktreeCommandError(path, err)
	}
	files := parseStatus(status)
	return copilotadapter.WorktreeChanges{
		HeadSHA: headSHA, Clean: len(status) == 0, Files: files,
		Patch: string(patch), Truncated: statusTruncated || patchTruncated || untrackedListTruncated || untrackedPatchTruncated,
		Captured: time.Now().UTC(),
	}, strings.TrimSpace(string(finalHead)) == headSHA, nil
}

func worktreeCommandError(path string, commandErr error) error {
	if _, err := os.Stat(path); errorsIsNotExist(err) {
		return fmt.Errorf("%w: worktree path disappeared during inspection", copilotadapter.ErrInvalidWorktree)
	}
	return commandErr
}

func (manager *WorktreeManager) appendUntrackedPatches(ctx context.Context, directory string, patch []byte, paths []byte) ([]byte, bool, error) {
	truncated := false
	for _, name := range bytes.Split(paths, []byte{0}) {
		if len(name) == 0 {
			continue
		}
		remaining := manager.patchBytes - len(patch)
		if remaining <= 0 {
			return patch, true, nil
		}
		addition, additionTruncated, err := manager.gitNoIndexBounded(ctx, directory, remaining, string(name))
		if err != nil {
			return nil, false, err
		}
		patch = append(patch, addition...)
		truncated = truncated || additionTruncated
		if additionTruncated {
			return patch, true, nil
		}
	}
	return patch, truncated, nil
}

// Release removes only an adapter-managed path beneath the configured root.
func (manager *WorktreeManager) Release(ctx context.Context, worktree copilotadapter.PreparedWorktree) error {
	if !worktree.Managed {
		return nil
	}
	canonicalRoot, err := filepath.EvalSymlinks(manager.worktreeRoot)
	if err != nil || canonicalRoot != manager.worktreeRoot {
		return fmt.Errorf("worktree adapter: refusing to release after managed root changed")
	}
	canonicalPath, err := filepath.EvalSymlinks(worktree.Path)
	if err != nil {
		return fmt.Errorf("worktree adapter: refusing to release unresolved path: %w", err)
	}
	relative, err := filepath.Rel(canonicalRoot, canonicalPath)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) || filepath.IsAbs(relative) {
		return fmt.Errorf("worktree adapter: refusing to release path outside managed root")
	}
	requestedPath, err := filepath.Abs(worktree.Path)
	if err != nil || canonicalPath != filepath.Clean(requestedPath) {
		return fmt.Errorf("worktree adapter: refusing to release a path that resolves to another worktree")
	}
	_, err = manager.git(ctx, worktree.Repository.Root, "worktree", "remove", "--force", canonicalPath)
	return err
}

func (manager *WorktreeManager) git(ctx context.Context, directory string, arguments ...string) ([]byte, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, arguments...)...)
	stderr, err := boundedbuffer.New(&boundedbufferv1.Retention{MaxBytes: commandErrorBytes})
	if err != nil {
		return nil, err
	}
	command.Stderr = stderr
	output, err := command.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, stderr.String())
	}
	return output, nil
}

func (manager *WorktreeManager) gitBounded(ctx context.Context, directory string, maxBytes int, arguments ...string) ([]byte, bool, error) {
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, arguments...)...)
	stdout, err := boundedbuffer.New(&boundedbufferv1.Retention{MaxBytes: int64(maxBytes)})
	if err != nil {
		return nil, false, err
	}
	stderr, err := boundedbuffer.New(&boundedbufferv1.Retention{MaxBytes: commandErrorBytes})
	if err != nil {
		return nil, false, err
	}
	command.Stdout, command.Stderr = stdout, stderr
	if err := command.Run(); err != nil {
		return nil, stdout.Truncated(), fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, stderr.String())
	}
	return append([]byte(nil), stdout.Bytes()...), stdout.Truncated(), nil
}

func (manager *WorktreeManager) gitNoIndexBounded(ctx context.Context, directory string, maxBytes int, path string) ([]byte, bool, error) {
	arguments := []string{"diff", "--no-index", "--no-ext-diff", "--binary", "--", "/dev/null", path}
	command := exec.CommandContext(ctx, "git", append([]string{"-C", directory}, arguments...)...)
	stdout, err := boundedbuffer.New(&boundedbufferv1.Retention{MaxBytes: int64(maxBytes)})
	if err != nil {
		return nil, false, err
	}
	stderr, err := boundedbuffer.New(&boundedbufferv1.Retention{MaxBytes: commandErrorBytes})
	if err != nil {
		return nil, false, err
	}
	command.Stdout, command.Stderr = stdout, stderr
	err = command.Run()
	var exitError *exec.ExitError
	if err != nil && (!errors.As(err, &exitError) || exitError.ExitCode() != 1) {
		return nil, stdout.Truncated(), fmt.Errorf("git %s: %w: %s", strings.Join(arguments, " "), err, stderr.String())
	}
	return append([]byte(nil), stdout.Bytes()...), stdout.Truncated(), nil
}

func parseStatus(body []byte) []copilotadapter.WorktreeChange {
	entries := bytes.Split(body, []byte{0})
	changes := make([]copilotadapter.WorktreeChange, 0, len(entries))
	for index := 0; index < len(entries); index++ {
		entry := entries[index]
		if len(entry) < 4 {
			continue
		}
		change := copilotadapter.WorktreeChange{
			IndexState: gitState(entry[0]), WorktreeState: gitState(entry[1]), Path: string(entry[3:]),
		}
		if (entry[0] == 'R' || entry[0] == 'C') && index+1 < len(entries) {
			index++
			change.PreviousPath = string(entries[index])
		}
		changes = append(changes, change)
	}
	return changes
}

func gitState(value byte) string {
	switch value {
	case ' ':
		return "unmodified"
	case 'M', 'T':
		return "modified"
	case 'A':
		return "added"
	case 'D':
		return "deleted"
	case 'R':
		return "renamed"
	case 'C':
		return "copied"
	case 'U':
		return "unmerged"
	case '?':
		return "untracked"
	case '!':
		return "ignored"
	default:
		return "unmodified"
	}
}

func worktreeListingStatus(listing []byte, path string) (bool, bool) {
	for _, record := range bytes.Split(listing, []byte("\n\n")) {
		registered, prunable := false, false
		for _, line := range bytes.Split(record, []byte{'\n'}) {
			registered = registered || bytes.Equal(line, []byte("worktree "+path))
			prunable = prunable || bytes.Equal(line, []byte("prunable")) || bytes.HasPrefix(line, []byte("prunable "))
		}
		if registered {
			return true, prunable
		}
	}
	return false, false
}

func errorsIsNotExist(err error) bool { return err != nil && os.IsNotExist(err) }
