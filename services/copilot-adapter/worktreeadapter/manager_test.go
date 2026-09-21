package worktreeadapter_test

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	copilotadapter "github.com/candacelabs/csf/services/copilot-adapter"
	"github.com/candacelabs/csf/services/copilot-adapter/worktreeadapter"
)

var _ = Describe("WorktreeManager", func() {
	var (
		ctx         context.Context
		repository  string
		managedRoot string
		manager     *worktreeadapter.WorktreeManager
	)

	BeforeEach(func() {
		ctx = context.Background()
		temporary := GinkgoT().TempDir()
		repository = filepath.Join(temporary, "repository")
		managedRoot = filepath.Join(temporary, "worktrees")
		Expect(os.MkdirAll(repository, 0o750)).To(Succeed())
		runGit(repository, "init", "--initial-branch=main")
		runGit(repository, "config", "user.name", "Candace Test")
		runGit(repository, "config", "user.email", "test@example.invalid")
		Expect(os.WriteFile(filepath.Join(repository, "README.md"), []byte("initial\n"), 0o600)).To(Succeed())
		runGit(repository, "add", "README.md")
		runGit(repository, "commit", "-m", "initial")
		var err error
		manager, err = worktreeadapter.NewWorktreeManager(worktreeadapter.Config{
			Repositories: []copilotadapter.Repository{{
				ID: "repo", DisplayName: "Repository", Root: repository, DefaultRef: "main",
			}},
			WorktreeRoot: managedRoot,
			PatchBytes:   16,
		})
		Expect(err).NotTo(HaveOccurred())
	})

	It("reuses only the configured canonical root when explicitly requested", func() {
		prepared, err := manager.Prepare(ctx, copilotadapter.WorktreeRequest{
			RepositoryID: "repo", Mode: "reuseCurrentWorktree", SessionID: uuid.New(),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(prepared.Path).To(Equal(repository))
		Expect(prepared.Managed).To(BeFalse())
		Expect(manager.Release(ctx, prepared)).To(Succeed())

		_, err = manager.Prepare(ctx, copilotadapter.WorktreeRequest{
			RepositoryID: "repo", Mode: "reuseCurrentWorktree", BaseRef: "main", SessionID: uuid.New(),
		})
		Expect(err).To(MatchError(ContainSubstring("baseRef")))
	})

	It("creates, inspects, diffs and safely releases an isolated worktree", func() {
		prepared, err := manager.Prepare(ctx, copilotadapter.WorktreeRequest{
			RepositoryID: "repo", Mode: "newWorktree", BaseRef: "main", SessionID: uuid.New(),
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = manager.Release(context.Background(), prepared) })
		Expect(prepared.Managed).To(BeTrue())
		Expect(prepared.Path).To(HavePrefix(managedRoot + string(filepath.Separator)))

		snapshot, err := manager.Inspect(ctx, prepared.Path)
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.State).To(Equal("active"))
		Expect(snapshot.Branch).To(HavePrefix("codex/chat-"))
		Expect(snapshot.HeadSHA).NotTo(BeEmpty())
		Expect(snapshot.Clean).To(BeTrue())

		Expect(os.WriteFile(filepath.Join(prepared.Path, "README.md"), []byte(strings.Repeat("large diff\n", 200_000)), 0o600)).To(Succeed())
		changes, err := manager.Changes(ctx, prepared.Path)
		Expect(err).NotTo(HaveOccurred())
		Expect(changes.Clean).To(BeFalse())
		Expect(changes.Files).To(ContainElement(And(
			HaveField("Path", Equal("README.md")),
			HaveField("WorktreeState", Equal("modified")),
		)))
		Expect(changes.Truncated).To(BeTrue())
		Expect(changes.Patch).To(HaveLen(16))

		Expect(manager.Release(ctx, prepared)).To(Succeed())
		missing, err := manager.Inspect(ctx, prepared.Path)
		Expect(err).NotTo(HaveOccurred())
		Expect(missing.State).To(Equal("missing"))
		prepared.Managed = true
		prepared.Path = repository
		Expect(manager.Release(ctx, prepared)).To(MatchError(ContainSubstring("outside managed root")))
	})

	It("reconciles the full session identity after the base ref moves", func() {
		sessionID := uuid.MustParse("12345678-1234-4234-8234-123456789abc")
		request := copilotadapter.WorktreeRequest{
			RepositoryID: "repo", Mode: "newWorktree", BaseRef: "main", SessionID: sessionID,
		}
		prepared, err := manager.Prepare(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = manager.Release(context.Background(), prepared) })
		Expect(filepath.Base(prepared.Path)).To(Equal("chat-" + sessionID.String()))
		originalHead := gitOutput(prepared.Path, "rev-parse", "HEAD")

		Expect(os.WriteFile(filepath.Join(repository, "README.md"), []byte("base moved\n"), 0o600)).To(Succeed())
		runGit(repository, "add", "README.md")
		runGit(repository, "commit", "-m", "move base")
		Expect(gitOutput(repository, "rev-parse", "HEAD")).NotTo(Equal(originalHead))

		reconciled, err := manager.Prepare(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		Expect(reconciled.Path).To(Equal(prepared.Path))
		Expect(gitOutput(reconciled.Path, "rev-parse", "HEAD")).To(Equal(originalHead))
	})

	It("restores branch-only residue without creating a second identity", func() {
		sessionID := uuid.New()
		request := copilotadapter.WorktreeRequest{
			RepositoryID: "repo", Mode: "newWorktree", BaseRef: "main", SessionID: sessionID,
		}
		prepared, err := manager.Prepare(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		head := gitOutput(prepared.Path, "rev-parse", "HEAD")
		runGit(repository, "worktree", "remove", prepared.Path)
		Expect(prepared.Path).NotTo(BeADirectory())

		restored, err := manager.Prepare(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = manager.Release(context.Background(), restored) })
		Expect(restored.Path).To(Equal(prepared.Path))
		Expect(gitOutput(restored.Path, "rev-parse", "HEAD")).To(Equal(head))
		Expect(gitOutput(restored.Path, "branch", "--show-current")).To(Equal("codex/chat-" + sessionID.String()))
	})

	It("refuses deterministic path residue owned by another branch", func() {
		sessionID := uuid.New()
		path := filepath.Join(managedRoot, "chat-"+sessionID.String())
		Expect(os.MkdirAll(managedRoot, 0o750)).To(Succeed())
		runGit(repository, "worktree", "add", "-b", "codex/chat-wrong-owner", path, "main")
		DeferCleanup(func() {
			_ = exec.Command("git", "-C", repository, "worktree", "remove", "--force", path).Run()
			_ = exec.Command("git", "-C", repository, "branch", "-D", "codex/chat-wrong-owner").Run()
		})

		_, err := manager.Prepare(ctx, copilotadapter.WorktreeRequest{
			RepositoryID: "repo", Mode: "newWorktree", BaseRef: "main", SessionID: sessionID,
		})
		Expect(err).To(MatchError(ContainSubstring("uses branch")))
	})

	It("creates and reuses managed worktrees beneath a canonicalized symlink root", func() {
		realParent := GinkgoT().TempDir()
		aliasContainer := GinkgoT().TempDir()
		aliasParent := filepath.Join(aliasContainer, "managed-alias")
		Expect(os.Symlink(realParent, aliasParent)).To(Succeed())
		configuredRoot := filepath.Join(aliasParent, "future", "worktrees")
		canonicalRoot := filepath.Join(realParent, "future", "worktrees")
		canonicalManager, err := worktreeadapter.NewWorktreeManager(worktreeadapter.Config{
			Repositories: []copilotadapter.Repository{{
				ID: "repo", DisplayName: "Repository", Root: repository, DefaultRef: "main",
			}},
			WorktreeRoot: configuredRoot,
		})
		Expect(err).NotTo(HaveOccurred())

		prepared, err := canonicalManager.Prepare(ctx, copilotadapter.WorktreeRequest{
			RepositoryID: "repo", Mode: "newWorktree", BaseRef: "main", SessionID: uuid.New(),
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = canonicalManager.Release(context.Background(), prepared) })
		Expect(prepared.Path).To(HavePrefix(canonicalRoot + string(filepath.Separator)))
		reused, err := canonicalManager.Reuse(ctx, "repo", prepared.Path)
		Expect(err).NotTo(HaveOccurred())
		Expect(reused.Path).To(Equal(prepared.Path))
		snapshot, err := canonicalManager.Inspect(ctx, reused.Path)
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.State).To(Equal("active"))

		sibling := filepath.Join(realParent, "future", "worktrees-sibling", "forged")
		Expect(os.MkdirAll(sibling, 0o750)).To(Succeed())
		_, err = canonicalManager.Reuse(ctx, "repo", sibling)
		Expect(errors.Is(err, copilotadapter.ErrInvalidWorktree)).To(BeTrue())
	})

	It("rejects an unregistered checkout even when a legacy row points beneath the managed root", func() {
		forged := filepath.Join(managedRoot, "forged")
		Expect(os.MkdirAll(forged, 0o750)).To(Succeed())
		runGit(forged, "init", "--initial-branch=main")
		_, err := manager.Reuse(ctx, "repo", forged)
		Expect(err).To(MatchError(ContainSubstring("not a registered managed worktree")))
		Expect(errors.Is(err, copilotadapter.ErrInvalidWorktree)).To(BeTrue())
	})

	It("rejects a prunable registration before inspecting its replacement directory", func() {
		sessionID := uuid.New()
		request := copilotadapter.WorktreeRequest{
			RepositoryID: "repo", Mode: "newWorktree", BaseRef: "main", SessionID: sessionID,
		}
		prepared, err := manager.Prepare(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		branch := gitOutput(prepared.Path, "branch", "--show-current")
		Expect(os.RemoveAll(prepared.Path)).To(Succeed())

		runGit(managedRoot, "init", "--initial-branch="+branch)
		runGit(managedRoot, "config", "user.name", "Candace Test")
		runGit(managedRoot, "config", "user.email", "test@example.invalid")
		Expect(os.WriteFile(filepath.Join(managedRoot, "README.md"), []byte("replacement parent\n"), 0o600)).To(Succeed())
		runGit(managedRoot, "add", "README.md")
		runGit(managedRoot, "commit", "-m", "replacement parent")
		Expect(os.MkdirAll(prepared.Path, 0o750)).To(Succeed())

		listing := gitOutput(repository, "worktree", "list", "--porcelain")
		Expect(listing).To(ContainSubstring("worktree " + prepared.Path))
		Expect(listing).To(ContainSubstring("prunable"))

		_, err = manager.Reuse(ctx, "repo", prepared.Path)
		Expect(err).To(MatchError(ContainSubstring("prunable Git registration")))
		Expect(errors.Is(err, copilotadapter.ErrInvalidWorktree)).To(BeTrue())

		_, err = manager.Prepare(ctx, request)
		Expect(err).To(MatchError(ContainSubstring("prunable Git registration")))
		Expect(errors.Is(err, copilotadapter.ErrInvalidWorktree)).To(BeTrue())
	})

	It("rejects a registered path whose exact replacement belongs to another repository", func() {
		sessionID := uuid.New()
		request := copilotadapter.WorktreeRequest{
			RepositoryID: "repo", Mode: "newWorktree", BaseRef: "main", SessionID: sessionID,
		}
		prepared, err := manager.Prepare(ctx, request)
		Expect(err).NotTo(HaveOccurred())
		branch := gitOutput(prepared.Path, "branch", "--show-current")
		Expect(os.RemoveAll(prepared.Path)).To(Succeed())

		Expect(os.MkdirAll(prepared.Path, 0o750)).To(Succeed())
		runGit(prepared.Path, "init", "--initial-branch="+branch)
		runGit(prepared.Path, "config", "user.name", "Candace Test")
		runGit(prepared.Path, "config", "user.email", "test@example.invalid")
		Expect(os.WriteFile(filepath.Join(prepared.Path, "README.md"), []byte("replacement\n"), 0o600)).To(Succeed())
		runGit(prepared.Path, "add", "README.md")
		runGit(prepared.Path, "commit", "-m", "replacement")

		_, err = manager.Reuse(ctx, "repo", prepared.Path)
		Expect(err).To(MatchError(ContainSubstring("another Git common directory")))
		Expect(errors.Is(err, copilotadapter.ErrInvalidWorktree)).To(BeTrue())

		_, err = manager.Prepare(ctx, request)
		Expect(err).To(MatchError(ContainSubstring("another Git common directory")))
		Expect(errors.Is(err, copilotadapter.ErrInvalidWorktree)).To(BeTrue())
	})

	It("refuses to release a managed-looking symlink that escapes the canonical root", func() {
		outside := GinkgoT().TempDir()
		escape := filepath.Join(managedRoot, "escaped")
		Expect(os.MkdirAll(managedRoot, 0o750)).To(Succeed())
		Expect(os.Symlink(outside, escape)).To(Succeed())

		err := manager.Release(ctx, copilotadapter.PreparedWorktree{
			Repository: copilotadapter.Repository{Root: repository}, Path: escape, Managed: true,
		})
		Expect(err).To(MatchError(ContainSubstring("outside managed root")))
	})

	It("refuses to release an in-root alias for another managed worktree", func() {
		prepared, err := manager.Prepare(ctx, copilotadapter.WorktreeRequest{
			RepositoryID: "repo", Mode: "newWorktree", BaseRef: "main", SessionID: uuid.New(),
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = manager.Release(context.Background(), prepared) })
		alias := filepath.Join(managedRoot, "chat-alias")
		Expect(os.Symlink(prepared.Path, alias)).To(Succeed())

		err = manager.Release(ctx, copilotadapter.PreparedWorktree{
			Repository: prepared.Repository, Path: alias, Managed: true,
		})
		Expect(err).To(MatchError(ContainSubstring("resolves to another worktree")))
		snapshot, err := manager.Inspect(ctx, prepared.Path)
		Expect(err).NotTo(HaveOccurred())
		Expect(snapshot.State).To(Equal("active"))
	})

	It("classifies only definitive boundary failures as invalid worktrees", func() {
		_, err := manager.Reuse(ctx, "unknown", repository)
		Expect(errors.Is(err, copilotadapter.ErrInvalidWorktree)).To(BeTrue())

		_, err = manager.Reuse(ctx, "repo", filepath.Join(managedRoot, "missing"))
		Expect(errors.Is(err, copilotadapter.ErrInvalidWorktree)).To(BeTrue())

		prepared, err := manager.Prepare(ctx, copilotadapter.WorktreeRequest{
			RepositoryID: "repo", Mode: "newWorktree", BaseRef: "main", SessionID: uuid.New(),
		})
		Expect(err).NotTo(HaveOccurred())
		DeferCleanup(func() { _ = manager.Release(context.Background(), prepared) })
		cancelled, cancel := context.WithCancel(ctx)
		cancel()
		_, err = manager.Reuse(cancelled, "repo", prepared.Path)
		Expect(err).To(HaveOccurred())
		Expect(errors.Is(err, copilotadapter.ErrInvalidWorktree)).To(BeFalse())
	})

	It("reports a worktree file-to-symlink type change as modified", func() {
		readme := filepath.Join(repository, "README.md")
		Expect(os.Remove(readme)).To(Succeed())
		Expect(os.Symlink("target.txt", readme)).To(Succeed())

		changes, err := manager.Changes(ctx, repository)
		Expect(err).NotTo(HaveOccurred())
		Expect(changes.Clean).To(BeFalse())
		Expect(changes.Files).To(ContainElement(And(
			HaveField("Path", Equal("README.md")),
			HaveField("IndexState", Equal("unmodified")),
			HaveField("WorktreeState", Equal("modified")),
		)))
	})

	DescribeTable("derives clean state from the same porcelain capture as files",
		func(initiallyDirty bool, mutationMode string, expectedClean bool, expectedFiles int) {
			readme := filepath.Join(repository, "README.md")
			if initiallyDirty {
				Expect(os.WriteFile(readme, []byte("dirty before inspect\n"), 0o600)).To(Succeed())
			}
			installGitInterposer(mutationMode, readme)

			changes, err := manager.Changes(ctx, repository)
			Expect(err).NotTo(HaveOccurred())
			Expect(changes.Clean).To(Equal(expectedClean))
			Expect(changes.Files).To(HaveLen(expectedFiles))
		},
		Entry("when the tree becomes dirty after Inspect", false, "dirty", false, 1),
		Entry("when the tree becomes clean after Inspect", true, "clean", true, 0),
	)

	It("retries a mid-capture HEAD change and returns one coherent snapshot", func() {
		originalHead := gitOutput(repository, "rev-parse", "HEAD")
		installGitInterposer("commit-once", filepath.Join(repository, "README.md"))

		changes, err := manager.Changes(ctx, repository)
		Expect(err).NotTo(HaveOccurred())
		currentHead := gitOutput(repository, "rev-parse", "HEAD")
		Expect(currentHead).NotTo(Equal(originalHead))
		Expect(changes.HeadSHA).To(Equal(currentHead))
		Expect(changes.Clean).To(BeTrue())
		Expect(changes.Files).To(BeEmpty())
		Expect(changes.Patch).To(BeEmpty())
	})

	It("stops after one retry when HEAD keeps changing during capture", func() {
		originalHead := gitOutput(repository, "rev-parse", "HEAD")
		installGitInterposer("commit-always", filepath.Join(repository, "README.md"))

		_, err := manager.Changes(ctx, repository)
		Expect(err).To(MatchError(ContainSubstring("HEAD changed during both attempts")))
		Expect(gitOutput(repository, "rev-list", "--count", originalHead+"..HEAD")).To(Equal("2"))
	})

	DescribeTable("classifies a path disappearing after the initial stat as invalid",
		func(readChanges bool) {
			installGitInterposer("disappear", filepath.Join(repository, "README.md"))
			var err error
			if readChanges {
				_, err = manager.Changes(ctx, repository)
			} else {
				_, err = manager.Inspect(ctx, repository)
			}
			Expect(errors.Is(err, copilotadapter.ErrInvalidWorktree)).To(BeTrue())
		},
		Entry("during Inspect", false),
		Entry("during Changes", true),
	)

	It("includes untracked text and binary files inside the global patch bound", func() {
		untrackedManager, err := worktreeadapter.NewWorktreeManager(worktreeadapter.Config{
			Repositories: []copilotadapter.Repository{{
				ID: "repo", DisplayName: "Repository", Root: repository, DefaultRef: "main",
			}},
			WorktreeRoot: managedRoot,
			PatchBytes:   4096,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(repository, "notes.txt"), []byte("untracked review content\n"), 0o600)).To(Succeed())
		Expect(os.WriteFile(filepath.Join(repository, "payload.bin"), []byte{0, 1, 2, 3}, 0o600)).To(Succeed())
		changes, err := untrackedManager.Changes(ctx, repository)
		Expect(err).NotTo(HaveOccurred())
		Expect(changes.Patch).To(ContainSubstring("untracked review content"))
		Expect(changes.Patch).To(ContainSubstring("GIT binary patch"))

		boundedManager, err := worktreeadapter.NewWorktreeManager(worktreeadapter.Config{
			Repositories: []copilotadapter.Repository{{
				ID: "repo", DisplayName: "Repository", Root: repository, DefaultRef: "main",
			}},
			WorktreeRoot: managedRoot,
			PatchBytes:   128,
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(os.WriteFile(filepath.Join(repository, "large.txt"), []byte(strings.Repeat("large untracked line\n", 1000)), 0o600)).To(Succeed())
		bounded, err := boundedManager.Changes(ctx, repository)
		Expect(err).NotTo(HaveOccurred())
		Expect(bounded.Truncated).To(BeTrue())
		Expect(bounded.Patch).To(HaveLen(128))
	})
})

func runGit(directory string, arguments ...string) {
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(output))
}

func gitOutput(directory string, arguments ...string) string {
	command := exec.Command("git", append([]string{"-C", directory}, arguments...)...)
	output, err := command.CombinedOutput()
	Expect(err).NotTo(HaveOccurred(), string(output))
	return strings.TrimSpace(string(output))
}

func installGitInterposer(mode string, target string) {
	GinkgoHelper()
	realGit, err := exec.LookPath("git")
	Expect(err).NotTo(HaveOccurred())
	bin := GinkgoT().TempDir()
	interposer := filepath.Join(bin, "git")
	script := `#!/bin/sh
if [ "$GIT_TEST_MODE" = "disappear" ] && [ "$3" = "rev-parse" ] && [ "$4" = "HEAD" ]; then
  mv -- "$2" "$GIT_TEST_MOVED_PATH"
fi
"$GIT_TEST_REAL" "$@"
result=$?
if [ "$3" = "status" ] && [ "$4" = "--porcelain=v1" ] && [ "$#" -eq 4 ]; then
  case "$GIT_TEST_MODE" in
    dirty) printf 'changed after inspect\n' > "$GIT_TEST_TARGET" ;;
    clean) printf 'initial\n' > "$GIT_TEST_TARGET" ;;
  esac
fi
if [ "$result" -eq 0 ] && [ "$3" = "status" ] && [ "$4" = "--porcelain=v1" ] && [ "$5" = "-z" ]; then
  commit=false
  case "$GIT_TEST_MODE" in
    commit-once)
      if [ ! -e "$GIT_TEST_MARKER" ]; then
        touch "$GIT_TEST_MARKER"
        commit=true
      fi
      ;;
    commit-always) commit=true ;;
  esac
  if [ "$commit" = true ]; then
    printf 'committed during capture\n' >> "$GIT_TEST_TARGET"
    "$GIT_TEST_REAL" -C "$2" add -- "$GIT_TEST_RELATIVE" >/dev/null 2>&1 || exit 90
    "$GIT_TEST_REAL" -C "$2" commit -m 'mid-capture commit' >/dev/null 2>&1 || exit 91
  fi
fi
exit "$result"
`
	Expect(os.WriteFile(interposer, []byte(script), 0o700)).To(Succeed())
	GinkgoT().Setenv("GIT_TEST_REAL", realGit)
	GinkgoT().Setenv("GIT_TEST_MODE", mode)
	GinkgoT().Setenv("GIT_TEST_TARGET", target)
	GinkgoT().Setenv("GIT_TEST_RELATIVE", filepath.Base(target))
	GinkgoT().Setenv("GIT_TEST_MARKER", filepath.Join(bin, "mutated"))
	GinkgoT().Setenv("GIT_TEST_MOVED_PATH", filepath.Dir(target)+"-moved")
	GinkgoT().Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}
