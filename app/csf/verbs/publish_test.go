// Copyright 2026 Candace Labs

package verbs

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"go.uber.org/mock/gomock"

	"github.com/candacelabs/csf/io/ipc/proc"
)

var _ = Describe("csf publish", func() {
	const (
		revision = "2f31d3fc8503520b71a22c10a218438d980ab5a8"
		tree     = "8de7c2cf85cd6141de1510636f146e27bb16789e"
		merged   = "aaaabbbbccccddddeeeeffff0000111122223333"
	)
	var (
		controller  *gomock.Controller
		launcher    *MockILauncher
		workspace   string
		output      bytes.Buffer
		diagnostics bytes.Buffer
		launches    []string
		failOn      string
		withNotes   bool
	)
	// respond plays git and gh: clones make their directory, rev-parse answers
	// the three revisions, and the step named by failOn fails.
	respond := func(_ context.Context, command proc.Command) (proc.Result, error) {
		line := command.Executable + " " + strings.Join(command.Arguments, " ")
		launches = append(launches, line)
		if failOn != "" && strings.HasPrefix(line, failOn) {
			return proc.Result{}, &proc.ExitError{Executable: command.Executable, Code: 1}
		}
		switch {
		case strings.HasPrefix(line, "gh repo clone"):
			Expect(os.MkdirAll(command.Arguments[3], 0o755)).To(Succeed())
			if withNotes && command.Arguments[3] == filepath.Join(workspace, sourceDirectory) {
				notes := filepath.Join(command.Arguments[3], "docs", "release")
				Expect(os.MkdirAll(notes, 0o755)).To(Succeed())
				Expect(os.WriteFile(filepath.Join(notes, "notes-v0.4.0.md"), []byte("# v0.4.0\n"), 0o644)).To(Succeed())
			}
		case line == "git rev-parse HEAD":
			return proc.Result{Stdout: []byte(revision + "\n")}, nil
		case line == "git rev-parse HEAD^{tree}":
			return proc.Result{Stdout: []byte(tree + "\n")}, nil
		case line == "git rev-parse FETCH_HEAD":
			return proc.Result{Stdout: []byte(merged + "\n")}, nil
		}
		return proc.Result{}, nil
	}
	run := func(arguments ...string) error {
		return publishVerb(context.Background(), launcher, workspace, arguments, &output, &diagnostics)
	}

	BeforeEach(func() {
		controller = gomock.NewController(GinkgoT())
		launcher = NewMockILauncher(controller)
		launcher.EXPECT().Run(gomock.Any(), gomock.Any()).DoAndReturn(respond).AnyTimes()
		workspace = GinkgoT().TempDir()
		output.Reset()
		diagnostics.Reset()
		launches, failOn, withNotes = nil, "", true
	})

	It("snapshots release/vVERSION, merges it as an administrator, tags it and creates the Release", func() {
		Expect(run("v0.4.0")).To(Succeed())
		source, destination := filepath.Join(workspace, sourceDirectory), filepath.Join(workspace, destinationDirectory)
		notes := filepath.Join(source, "docs", "release", "notes-v0.4.0.md")
		archive := filepath.Join(workspace, snapshotArchive)
		Expect(launches).To(Equal([]string{
			"gh repo clone candacelabs/csf_staging " + source + " -- --depth 1 --branch release/v0.4.0",
			"gh repo clone candacelabs/csf " + destination + " -- --depth 1",
			"git rev-parse HEAD",
			"git rev-parse HEAD^{tree}",
			"git checkout -q -b release-v0.4.0",
			"git rm -rq --ignore-unmatch .",
			"git archive --format=tar -o " + archive + " HEAD",
			"tar -xf " + archive,
			"git add -A",
			"git commit -q -m Release v0.4.0 -m Snapshot of candacelabs/csf_staging `release/v0.4.0` at `2f31d3fc8503` (tree `8de7c2cf85cd`).",
			"git push -q origin release-v0.4.0",
			"gh pr create --repo candacelabs/csf --base main --head release-v0.4.0 --title Release v0.4.0 --body-file " + notes,
			"gh pr merge release-v0.4.0 --repo candacelabs/csf --squash --admin --delete-branch",
			"git fetch -q --depth 1 origin main",
			"git rev-parse FETCH_HEAD",
			"git tag v0.4.0 " + merged,
			"git tag export-aaaabbbbcccc " + merged,
			"git push -q origin v0.4.0 export-aaaabbbbcccc",
			"gh release create v0.4.0 --repo candacelabs/csf --verify-tag --title v0.4.0 --notes-file " + notes,
		}))
		contents, err := os.ReadFile(filepath.Join(destination, exportMarkerName))
		Expect(err).NotTo(HaveOccurred())
		var marker exportMarker
		Expect(json.Unmarshal(contents, &marker)).To(Succeed())
		Expect(marker).To(Equal(exportMarker{
			DestinationRepository: "candacelabs/csf", ManagedBy: "csf-release", SchemaVersion: 2, SourcePath: ".",
			SourceRepository: "candacelabs/csf_staging", SourceRevision: revision, SourceTreeOID: tree,
		}))
		Expect(output.String()).To(HaveSuffix("csf publish: published https://github.com/candacelabs/csf/releases/tag/v0.4.0\n"))
	})

	It("publishes any ref and writes a one-line body when the source has no notes for the version", func() {
		withNotes = false
		Expect(run("-ref", "main", "0.4.1")).To(Succeed())
		Expect(launches[0]).To(HaveSuffix("--branch main"))
		body, err := os.ReadFile(filepath.Join(workspace, fallbackNotesName))
		Expect(err).NotTo(HaveOccurred())
		Expect(string(body)).To(Equal("Release v0.4.1: a snapshot of candacelabs/csf_staging `main` at `" + revision + "`.\n"))
	})

	It("stops at the first failed step, so a refused merge is never tagged or released", func() {
		failOn = "gh pr merge"
		err := run("0.4.0")
		Expect(err).To(MatchError(ContainSubstring("gh pr merge release-v0.4.0")))
		Expect(launches[len(launches)-1]).To(HavePrefix("gh pr merge"))
	})

	It("refuses a missing or malformed version before running anything", func() {
		Expect(run()).To(MatchError(errNoPublishVersion))
		Expect(run("0.4")).To(MatchError(errPublishVersionForm))
		Expect(run("latest")).To(MatchError(errPublishVersionForm))
		Expect(launches).To(BeEmpty())
	})
})
