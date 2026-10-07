package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	archive "github.com/candacelabs/csf/csf/bootstrap/archive"
	"github.com/candacelabs/csf/io/ipc/proc"
	bootstrapservice "github.com/candacelabs/csf/services/bootstrap"
)

// The two bootstrap verbs, declared once in csf/bootstrap/bootstrap.csf and
// dispatched here. A golden test in this package pins that dispatch against the
// declaration, so the words a caller types and the branch that answers them
// cannot drift.
const (
	// bootstrapCommand is the local verb's fixed word:
	// `csf bootstrap -repo <dir> -local`.
	bootstrapCommand = "bootstrap"
	// archiveSuffix makes a positional first word an archive rather than a
	// command: `csf <archive.tar.gz> [args...]`.
	archiveSuffix = ".tar.gz"

	bootstrapRepoFlag  = "repo"
	bootstrapLocalFlag = "local"
)

// errBootstrapNeedsRepo refuses a bootstrap run with nothing to run over.
var errBootstrapNeedsRepo = errors.New("bootstrap needs -repo <dir>")

// bootstrap is the local verb: it runs the bootstrap loop over one repository
// directory in this process and prints the archive it emits beside the
// repository. Running locally means no job, no network and no database, so the
// command works on a checkout with nothing else installed.
func bootstrap(ctx context.Context, arguments []string, output io.Writer) error {
	repo, err := parseBootstrapRequest(arguments)
	if err != nil {
		return err
	}
	result, err := bootstrapservice.NewLoop(bootstrapservice.Measure, bootstrapservice.NoFix).Run(ctx, repo)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(output, "archive %s\ndone %t\niterations %d\n", result.Archive, result.Done, result.Iterations)
	return err
}

// parseBootstrapRequest reads the local verb's flags: the repository to run
// over, and the -local word that says the loop runs here rather than as a job.
// The job path is not landed, so a run without -local is refused rather than
// silently done locally.
func parseBootstrapRequest(arguments []string) (string, error) {
	flags := flag.NewFlagSet(bootstrapCommand, flag.ContinueOnError)
	repo := flags.String(bootstrapRepoFlag, "", "the repository directory to run the bootstrap loop over")
	local := flags.Bool(bootstrapLocalFlag, false, "run the loop in this process, with no job and no network")
	if err := flags.Parse(arguments); err != nil {
		return "", err
	}
	if flags.NArg() != 0 {
		return "", fmt.Errorf("bootstrap takes flags only; unexpected %q", flags.Arg(0))
	}
	if *repo == "" {
		return "", errBootstrapNeedsRepo
	}
	if !*local {
		return "", errors.New("bootstrap runs as a job by default, which is not landed; pass -local to run in this process")
	}
	return *repo, nil
}

// isArchive reports whether a command word names a bootstrap archive: the first
// word after the binary, ending in .tar.gz. It is the whole dispatch rule for
// the positional verb, so any binary from this release on runs a newer archive
// by naming it.
func isArchive(name string) bool {
	return strings.HasSuffix(name, archiveSuffix)
}

// runArchive is the positional verb, `csf <archive.tar.gz> [args...]`: it
// verifies the archive against its manifest, extracts it into a digest-keyed
// cache and runs its bin/csf entrypoint with the caller's remaining words,
// returning that entrypoint's exit code. Verification comes first, so a tampered
// archive runs nothing.
func runArchive(ctx context.Context, path string, arguments []string, stdin io.Reader, stdout, stderr io.Writer) (int, error) {
	launcher, err := proc.NewHostLauncher()
	if err != nil {
		return 1, err
	}
	code, err := archive.NewRunner(launcher, archiveCache()).Run(ctx, path, arguments, stdin, stdout, stderr)
	if code < 0 {
		// The runner reports a failure before an entrypoint exists as a negative
		// code; a shell sees the ordinary failure code instead.
		code = 1
	}
	return code, err
}

// archiveCache is the directory extracted archives live under: the user cache
// directory when there is one and the temporary directory otherwise, under a
// fixed subtree so a caller can find and clear it. Extraction is keyed by the
// payload digest, so a second run of the same archive reuses the first.
func archiveCache() string {
	root, err := os.UserCacheDir()
	if err != nil {
		root = os.TempDir()
	}
	return filepath.Join(root, "csf", "archives")
}
