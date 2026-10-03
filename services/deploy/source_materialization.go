package deploy

import (
	"archive/tar"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/candacelabs/csf/ipc/proc"
	"github.com/candacelabs/csf/pkg/boundedbuffer"
	boundedbufferv1 "github.com/candacelabs/csf/pkg/boundedbuffer/v1"

	deployv1 "github.com/candacelabs/csf/proto/candace/deploy/v1"
)

const gitDiagnosticLimit = 8 << 10

// MaterializeGitAppSource extracts one app subtree from an exact Git commit
// into an existing empty directory and returns its canonical content digest.
// Git runs through launcher, the process-boundary capability.
func MaterializeGitAppSource(
	ctx context.Context,
	launcher proc.ILauncher,
	gitBin string,
	repositoryRoot string,
	revision string,
	relativePath string,
	destination string,
) (string, error) {
	return MaterializeGitAppSourceWithLimits(
		ctx, launcher, gitBin, repositoryRoot, revision, relativePath, destination,
		DefaultAppSourceLimits(),
	)
}

// MaterializeGitAppSourceWithLimits applies an explicit resource policy before
// archive bytes reach disk.
func MaterializeGitAppSourceWithLimits(
	ctx context.Context,
	launcher proc.ILauncher,
	gitBin string,
	repositoryRoot string,
	revision string,
	relativePath string,
	destination string,
	limits *deployv1.AppSourceLimits,
) (string, error) {
	selection := &deployv1.AppSourceSelection{Revision: revision, Path: relativePath}
	if err := deployv1.ValidateAppSourceSelection(selection); err != nil {
		return "", fmt.Errorf("materializing app revision: invalid source selection: %w", err)
	}
	budget, err := newAppSourceBudget(limits)
	if err != nil {
		return "", fmt.Errorf("materializing app revision: invalid limits: %w", err)
	}
	if strings.TrimSpace(gitBin) == "" {
		return "", fmt.Errorf("materializing app revision: Git executable is required")
	}
	if launcher == nil {
		return "", fmt.Errorf("materializing app revision: process launcher is required")
	}
	resolvedRepository, err := filepath.EvalSymlinks(repositoryRoot)
	if err != nil {
		return "", fmt.Errorf("materializing app revision: resolving repository: %w", err)
	}
	destinationRoot, err := openEmptyAppSourceRoot(destination)
	if err != nil {
		return "", fmt.Errorf("materializing app revision: %w", err)
	}
	defer func() { _ = destinationRoot.Close() }()

	if err := requireExactGitCommit(ctx, launcher, gitBin, resolvedRepository, revision); err != nil {
		return "", fmt.Errorf("materializing app revision: %w", err)
	}
	if err := materializeGitArchive(ctx, launcher, gitBin, resolvedRepository, selection, destinationRoot, budget); err != nil {
		return "", fmt.Errorf("materializing app revision: %w", err)
	}
	digestBudget, err := newAppSourceBudget(limits)
	if err != nil {
		return "", fmt.Errorf("materializing app revision: invalid digest limits: %w", err)
	}
	return digestAppSourceRoot(ctx, destinationRoot, digestBudget)
}

func openEmptyAppSourceRoot(destination string) (*os.Root, error) {
	root, err := os.OpenRoot(destination)
	if err != nil {
		return nil, fmt.Errorf("opening destination: %w", err)
	}
	entries, err := fs.ReadDir(root.FS(), ".")
	if err != nil {
		_ = root.Close()
		return nil, fmt.Errorf("reading destination: %w", err)
	}
	if len(entries) != 0 {
		_ = root.Close()
		return nil, fmt.Errorf("destination must be empty")
	}
	return root, nil
}

func requireExactGitCommit(ctx context.Context, launcher proc.ILauncher, gitBin, repositoryRoot, revision string) error {
	result, err := launcher.Run(ctx, gitInRepository(gitBin, repositoryRoot, "rev-parse", "--verify", revision+"^{commit}"))
	if err != nil {
		// A proc.ExitError already carries Git's bounded diagnostics.
		return fmt.Errorf("resolving commit: %w", err)
	}
	if strings.TrimSpace(string(result.Stdout)) != revision {
		return fmt.Errorf("object %q is not the exact commit requested", revision)
	}
	return nil
}

func materializeGitArchive(
	ctx context.Context,
	launcher proc.ILauncher,
	gitBin string,
	repositoryRoot string,
	selection *deployv1.AppSourceSelection,
	destination *os.Root,
	budget *appSourceBudget,
) error {
	treeish := selection.GetRevision()
	if selection.GetPath() != "." {
		treeish += ":" + selection.GetPath()
	}
	commandContext, cancel := context.WithCancel(ctx)
	defer cancel()
	diagnostics, err := boundedbuffer.New(&boundedbufferv1.Retention{MaxBytes: gitDiagnosticLimit})
	if err != nil {
		return fmt.Errorf("configuring Git diagnostics: %w", err)
	}
	command := gitInRepository(gitBin, repositoryRoot, "archive", "--format=tar", treeish)
	command.StdoutPipe, command.Stderr = true, diagnostics
	archive, err := launcher.Start(commandContext, command)
	if err != nil {
		return fmt.Errorf("starting Git archive: %w", err)
	}
	stdout := archive.Stdout()

	extractErr := extractAppArchive(ctx, stdout, destination, budget)
	if extractErr != nil {
		cancel()
	}
	// tar stops at its logical end marker. Drain Git's record padding before
	// Wait so neither side can block on a full stdout pipe.
	_, drainErr := io.Copy(io.Discard, stdout)
	if drainErr != nil {
		cancel()
	}
	_, waitErr := archive.Wait()
	if extractErr != nil {
		return fmt.Errorf("extracting Git archive: %w", extractErr)
	}
	if waitErr != nil {
		if message := strings.TrimSpace(diagnostics.String()); message != "" {
			waitErr = fmt.Errorf("%w: %s", waitErr, message)
		}
		return fmt.Errorf("Git archive failed: %w", waitErr)
	}
	if drainErr != nil {
		return fmt.Errorf("draining Git archive: %w", drainErr)
	}
	return nil
}

func gitInRepository(gitBin, repositoryRoot string, arguments ...string) proc.Command {
	commandArguments := []string{"-c", "safe.directory=" + repositoryRoot, "-C", repositoryRoot}
	return proc.Command{Executable: gitBin, Arguments: append(commandArguments, arguments...)}
}

func extractAppArchive(ctx context.Context, source io.Reader, destination *os.Root, budget *appSourceBudget) error {
	archive := tar.NewReader(source)
	seen := make(map[string]struct{})
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return err
		}
		name := strings.TrimSuffix(header.Name, "/")
		if name == "" {
			continue
		}
		if _, duplicate := seen[name]; duplicate {
			return fmt.Errorf("archive contains duplicate entry %q", header.Name)
		}
		seen[name] = struct{}{}
		if err := budget.admitPath(name); err != nil {
			return err
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := destination.MkdirAll(name, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := budget.admitFile(header.Size); err != nil {
				return err
			}
			if err := writeAppArchiveFile(destination, archive, header, name); err != nil {
				return err
			}
		default:
			return fmt.Errorf("archive contains unsupported entry %q with type %d", header.Name, header.Typeflag)
		}
	}
}

func writeAppArchiveFile(destination *os.Root, archive io.Reader, header *tar.Header, name string) error {
	parent := path.Dir(name)
	if parent != "." {
		if err := destination.MkdirAll(parent, 0o755); err != nil {
			return err
		}
	}
	file, err := destination.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, os.FileMode(header.Mode).Perm())
	if err != nil {
		return err
	}
	_, copyErr := io.CopyN(file, archive, header.Size)
	closeErr := file.Close()
	if copyErr != nil {
		return copyErr
	}
	return closeErr
}
