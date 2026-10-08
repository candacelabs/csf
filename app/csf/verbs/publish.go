// Copyright 2026 Candace Labs

package verbs

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/candacelabs/csf/io/ipc/proc"
)

// csf publish [-source REPO] [-destination REPO] [-ref REF] VERSION is the
// launch button: it publishes the source ref, in whatever state it is, as
// public release vVERSION. It snapshots the ref's tracked tree into a pull
// request on the destination, merges it as an administrator with no check or
// review in the way, tags the merge vVERSION and export-<sha12>, and creates
// the GitHub Release with docs/release/notes-vVERSION.md as its body. Every
// step goes through git and an authenticated gh; the first failure stops it.
const (
	verbPublish               = "publish"
	publishSourceFlag         = "source"
	publishDestinationFlag    = "destination"
	publishRefFlag            = "ref"
	defaultPublishSource      = "candacelabs/csf_staging"
	defaultPublishDestination = "candacelabs/csf"
	publishBase               = "main"
	exportMarkerName          = ".candace-export.json"
	exportManager             = "csf-release"
	exportSchemaVersion       = 2
	exportSourcePath          = "."
	exportTagPrefix           = "export-"
	releaseNotesPattern       = "docs/release/notes-v%s.md"
	sourceDirectory           = "source"
	destinationDirectory      = "destination"
	snapshotArchive           = "snapshot.tar"
	fallbackNotesName         = "notes.md"
	shortRevision             = 12
	tarExecutable             = "tar"
)

var (
	semanticVersion       = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)
	errNoPublishVersion   = errors.New("usage: csf publish [-source REPO] [-destination REPO] [-ref REF] VERSION")
	errPublishVersionForm = errors.New("VERSION is MAJOR.MINOR.PATCH, such as 0.4.0")
)

// exportMarker is .candace-export.json, the provenance record every public
// snapshot carries at its root.
type exportMarker struct {
	DestinationRepository string `json:"destination_repository"`
	ManagedBy             string `json:"managed_by"`
	SchemaVersion         int    `json:"schema_version"`
	SourcePath            string `json:"source_path"`
	SourceRepository      string `json:"source_repository"`
	SourceRevision        string `json:"source_revision"`
	SourceTreeOID         string `json:"source_tree_oid"`
}

// publication is one launch: the release and where it reads and writes.
type publication struct {
	version     string
	source      string
	destination string
	ref         string
	workspace   string
}

func (release publication) tag() string    { return "v" + release.version }
func (release publication) branch() string { return "release-v" + release.version }
func (release publication) sourceDir() string {
	return filepath.Join(release.workspace, sourceDirectory)
}
func (release publication) destinationDir() string {
	return filepath.Join(release.workspace, destinationDirectory)
}

// publishVerb is `csf publish`. workspace is an empty directory it may fill.
func publishVerb(ctx context.Context, launcher proc.ILauncher, workspace string, arguments []string, output io.Writer, diagnostics io.Writer) error {
	flags := flag.NewFlagSet(commandName+" "+verbPublish, flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	source := flags.String(publishSourceFlag, defaultPublishSource, "repository the snapshot is taken from")
	destination := flags.String(publishDestinationFlag, defaultPublishDestination, "public repository the release is published to")
	ref := flags.String(publishRefFlag, "", "branch or tag of the source to publish (default release/vVERSION)")
	if err := flags.Parse(arguments); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errNoPublishVersion
	}
	version := strings.TrimPrefix(flags.Arg(0), "v")
	if !semanticVersion.MatchString(version) {
		return errPublishVersionForm
	}
	release := publication{version: version, source: *source, destination: *destination, ref: *ref, workspace: workspace}
	if release.ref == "" {
		release.ref = "release/" + release.tag()
	}
	return release.launch(ctx, launcher, output)
}

// launch runs every step in order and stops at the first failure.
func (release publication) launch(ctx context.Context, launcher proc.ILauncher, output io.Writer) error {
	say := func(format string, values ...any) {
		fmt.Fprintf(output, "%s %s: %s\n", commandName, verbPublish, fmt.Sprintf(format, values...))
	}
	run := func(directory string, executable string, arguments ...string) (string, error) {
		result, err := launcher.Run(ctx, proc.Command{Executable: executable, Arguments: arguments, Directory: directory})
		if err != nil {
			return "", fmt.Errorf("%s %s: %w", executable, strings.Join(arguments, " "), err)
		}
		return strings.TrimSpace(string(result.Stdout)), nil
	}
	source, destination := release.sourceDir(), release.destinationDir()

	say("cloning %s at %s", release.source, release.ref)
	if _, err := run(release.workspace, ghExecutable, "repo", "clone", release.source, source, "--", "--depth", "1", "--branch", release.ref); err != nil {
		return err
	}
	if _, err := run(release.workspace, ghExecutable, "repo", "clone", release.destination, destination, "--", "--depth", "1"); err != nil {
		return err
	}
	revision, err := run(source, gitExecutable, "rev-parse", "HEAD")
	if err != nil {
		return err
	}
	tree, err := run(source, gitExecutable, "rev-parse", "HEAD^{tree}")
	if err != nil {
		return err
	}

	say("snapshotting %s (tree %s) into %s", revision[:shortRevision], tree[:shortRevision], release.destination)
	archive := filepath.Join(release.workspace, snapshotArchive)
	snapshot := [][]string{
		{destination, gitExecutable, "checkout", "-q", "-b", release.branch()},
		{destination, gitExecutable, "rm", "-rq", "--ignore-unmatch", "."},
		{source, gitExecutable, "archive", "--format=tar", "-o", archive, "HEAD"},
		{destination, tarExecutable, "-xf", archive},
	}
	for _, step := range snapshot {
		if _, err := run(step[0], step[1], step[2:]...); err != nil {
			return err
		}
	}
	if err := release.writeMarker(revision, tree); err != nil {
		return err
	}
	notes, err := release.notes(revision)
	if err != nil {
		return err
	}
	message := fmt.Sprintf("Snapshot of %s `%s` at `%s` (tree `%s`).", release.source, release.ref, revision[:shortRevision], tree[:shortRevision])
	commit := [][]string{
		{destination, gitExecutable, "add", "-A"},
		{destination, gitExecutable, "commit", "-q", "-m", "Release " + release.tag(), "-m", message},
		{destination, gitExecutable, "push", "-q", "origin", release.branch()},
		{destination, ghExecutable, ghPR, "create", ghRepository, release.destination, "--base", publishBase, ghHead, release.branch(), "--title", "Release " + release.tag(), "--body-file", notes},
		{destination, ghExecutable, ghPR, "merge", release.branch(), ghRepository, release.destination, "--squash", "--admin", "--delete-branch"},
		{destination, gitExecutable, "fetch", "-q", "--depth", "1", "origin", publishBase},
	}
	say("opening and merging the release pull request")
	for _, step := range commit {
		if _, err := run(step[0], step[1], step[2:]...); err != nil {
			return err
		}
	}
	merged, err := run(destination, gitExecutable, "rev-parse", "FETCH_HEAD")
	if err != nil {
		return err
	}
	exportTag := exportTagPrefix + merged[:shortRevision]
	say("tagging %s and %s at %s", release.tag(), exportTag, merged[:shortRevision])
	publish := [][]string{
		{destination, gitExecutable, "tag", release.tag(), merged},
		{destination, gitExecutable, "tag", exportTag, merged},
		{destination, gitExecutable, "push", "-q", "origin", release.tag(), exportTag},
		{destination, ghExecutable, "release", "create", release.tag(), ghRepository, release.destination, "--verify-tag", "--title", release.tag(), "--notes-file", notes},
	}
	for _, step := range publish {
		if _, err := run(step[0], step[1], step[2:]...); err != nil {
			return err
		}
	}
	say("published https://github.com/%s/releases/tag/%s", release.destination, release.tag())
	return nil
}

// writeMarker records which source revision and tree this snapshot is.
func (release publication) writeMarker(revision string, tree string) error {
	marker, err := json.MarshalIndent(exportMarker{
		DestinationRepository: release.destination,
		ManagedBy:             exportManager,
		SchemaVersion:         exportSchemaVersion,
		SourcePath:            exportSourcePath,
		SourceRepository:      release.source,
		SourceRevision:        revision,
		SourceTreeOID:         tree,
	}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(release.destinationDir(), exportMarkerName), append(marker, '\n'), 0o644)
}

// notes is the release body: the version's notes from the source when it has
// them, and one line naming the snapshot when it does not.
func (release publication) notes(revision string) (string, error) {
	written := filepath.Join(release.sourceDir(), fmt.Sprintf(releaseNotesPattern, release.version))
	if _, err := os.Stat(written); err == nil {
		return written, nil
	}
	fallback := filepath.Join(release.workspace, fallbackNotesName)
	line := fmt.Sprintf("Release %s: a snapshot of %s `%s` at `%s`.\n", release.tag(), release.source, release.ref, revision)
	return fallback, os.WriteFile(fallback, []byte(line), 0o644)
}
