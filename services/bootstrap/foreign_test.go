// Copyright 2026 Candace Labs

package bootstrap_test

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/bootstrap"
)

// The hermetic fixture the brief names: a foreign Go repository carrying the
// four chief violations, committed under csf/bootstrap/testdata/foreign_go.
// The loop must reach chief_violations(0) against it locally, with no job, no
// network and no database.
const foreignFixture = "../../csf/bootstrap/testdata/foreign_go"

// copyTree copies every regular file and directory under root into target.
func copyTree(root, target string) error {
	return filepath.WalkDir(root, func(p string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		relative, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		destination := filepath.Join(target, relative)
		if entry.IsDir() {
			return os.MkdirAll(destination, 0o755)
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(destination, content, 0o644)
	})
}

// clearForeignMarkers removes the four chief markers from the copied fixture,
// one per iteration, driving every directory clean.
func clearForeignMarkers(repo string) error {
	for _, name := range []string{
		"legacy/fanout_at_most_16",
		"tooling/cli_purpose_declared",
		"grammar/grammar_decision_fits_one_pick",
		"vendored/every_directory_declared",
	} {
		if err := os.Remove(filepath.Join(repo, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	return nil
}

var _ = Describe("the bootstrap loop over the foreign fixture", func() {
	It("reaches chief_violations(0) locally", func() {
		repo := filepath.Join(GinkgoT().TempDir(), "repo")
		Expect(copyTree(foreignFixture, repo)).To(Succeed())

		loop := bootstrap.NewLoop(bootstrap.Measure, func(_ context.Context, run *bootstrap.Run) error {
			return clearForeignMarkers(run.Repo)
		})

		result, err := loop.Run(context.Background(), repo)
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Done).To(BeTrue())
		Expect(result.Final.AllHold()).To(BeTrue(), "the fixture reaches a zero-violation reading")
		Expect(result.Final.Chief).NotTo(BeEmpty(), "the fixture's directories were all measured")
		Expect(result.Archive).To(BeARegularFile())
		// Every marker the fixture carried is gone from the tree.
		for _, name := range []string{
			"legacy/fanout_at_most_16",
			"tooling/cli_purpose_declared",
			"grammar/grammar_decision_fits_one_pick",
			"vendored/every_directory_declared",
		} {
			_, err := os.Stat(filepath.Join(repo, name))
			Expect(err).To(HaveOccurred(), name)
		}
	})

	It("is hermetic and carries exactly the four named violations", func() {
		repo := filepath.Join(GinkgoT().TempDir(), "repo")
		Expect(copyTree(foreignFixture, repo)).To(Succeed())

		reading, err := bootstrap.Measure(context.Background(), repo)
		Expect(err).NotTo(HaveOccurred())
		Expect(reading.AllHold()).To(BeFalse(), "the fixture is not already clean")
		// One marker per directory, and only these four: the fixture is hermetic, so
		// nothing else in it reads as a violation. fanout_gt_16 in legacy,
		// undeclared_cli in tooling, free_text_grammar in grammar and undeclared_dirs
		// in vendored.
		Expect(reading.Chief).To(Equal(map[string]map[string]int{
			"grammar":  {"grammar_decision_fits_one_pick": 1},
			"legacy":   {"fanout_at_most_16": 1},
			"tooling":  {"cli_purpose_declared": 1},
			"vendored": {"every_directory_declared": 1},
		}))
	})
})
