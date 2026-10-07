// Copyright 2026 Candace Labs

package upgrade_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/upgrade"
)

const (
	installedContent = "installed csf"
	releasedContent  = "released csf"
)

// release is one published release: the binary and its checksum asset.
type release struct {
	binary    string
	checksums string
}

func checksumLine(content string, asset string) string {
	digest := sha256.Sum256([]byte(content))
	return hex.EncodeToString(digest[:]) + "  " + asset + "\n"
}

// fetchFrom serves the releases by tag, recording each tag asked for.
func fetchFrom(releases map[string]release, asked *[]string) upgrade.Fetch {
	return func(_ context.Context, tag string, asset string) ([]byte, error) {
		*asked = append(*asked, tag)
		published, found := releases[tag]
		if !found {
			return nil, errors.New("no release " + tag)
		}
		if asset == upgrade.Asset+upgrade.ChecksumSuffix {
			return []byte(published.checksums), nil
		}
		return []byte(published.binary), nil
	}
}

// restarts answers each restart with the next error, nil when they run out,
// and records the binary's content at each.
func restarts(binary string, seen *[]string, answers ...error) upgrade.Restart {
	return func(_ context.Context) error {
		content, err := os.ReadFile(binary)
		Expect(err).NotTo(HaveOccurred())
		*seen = append(*seen, string(content))
		if len(*seen) <= len(answers) {
			return answers[len(*seen)-1]
		}
		return nil
	}
}

func contentOf(path string) string {
	content, err := os.ReadFile(path)
	Expect(err).NotTo(HaveOccurred())
	return string(content)
}

var _ = Describe("Upgrader", func() {
	var (
		binary   string
		asked    []string
		seen     []string
		releases map[string]release
	)

	BeforeEach(func() {
		binary = filepath.Join(GinkgoT().TempDir(), "csf")
		Expect(os.WriteFile(binary, []byte(installedContent), 0o755)).To(Succeed())
		asked, seen = nil, nil
		releases = map[string]release{
			upgrade.LatestTag:               {binary: releasedContent, checksums: checksumLine(releasedContent, upgrade.Asset)},
			upgrade.CommitTagPrefix + "abc": {binary: releasedContent, checksums: checksumLine(releasedContent, upgrade.Asset)},
		}
	})

	build := func(restart upgrade.Restart) *upgrade.Upgrader {
		upgrader, err := upgrade.NewUpgrader(upgrade.WithBinary(binary), upgrade.WithFetch(fetchFrom(releases, &asked)), upgrade.WithRestart(restart))
		Expect(err).NotTo(HaveOccurred())
		return upgrader
	}

	It("installs latest main, keeps the replaced binary and restarts on the new one", func() {
		result, err := build(restarts(binary, &seen)).Upgrade(context.Background(), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(result.Tag).To(Equal(upgrade.LatestTag))
		Expect(contentOf(binary)).To(Equal(releasedContent))
		Expect(contentOf(binary + upgrade.KeptSuffix)).To(Equal(installedContent))
		Expect(seen).To(Equal([]string{releasedContent}))
		info, err := os.Stat(binary)
		Expect(err).NotTo(HaveOccurred())
		Expect(info.Mode().Perm()).To(Equal(os.FileMode(0o755)))
	})

	It("downloads the release named for the commit it is given", func() {
		_, err := build(restarts(binary, &seen)).Upgrade(context.Background(), "abc")
		Expect(err).NotTo(HaveOccurred())
		Expect(asked).To(ConsistOf(upgrade.CommitTagPrefix+"abc", upgrade.CommitTagPrefix+"abc"))
	})

	It("refuses a checksum mismatch with the installed binary untouched and no restart", func() {
		releases[upgrade.LatestTag] = release{binary: "tampered", checksums: checksumLine(releasedContent, upgrade.Asset)}
		_, err := build(restarts(binary, &seen)).Upgrade(context.Background(), "")
		Expect(err).To(MatchError(upgrade.ErrChecksumMismatch))
		Expect(contentOf(binary)).To(Equal(installedContent))
		Expect(binary + upgrade.KeptSuffix).NotTo(BeAnExistingFile())
		Expect(binary + ".new").NotTo(BeAnExistingFile())
		Expect(seen).To(BeEmpty())
	})

	It("refuses a build the score check refuses, installing nothing and restarting nothing", func() {
		unscored := errors.New("no complete score on the current suite")
		var checked string
		upgrader, err := upgrade.NewUpgrader(upgrade.WithBinary(binary), upgrade.WithFetch(fetchFrom(releases, &asked)), upgrade.WithRestart(restarts(binary, &seen)),
			upgrade.WithScoreCheck(func(_ context.Context, staged string) error {
				checked = contentOf(staged)
				return unscored
			}))
		Expect(err).NotTo(HaveOccurred())
		_, err = upgrader.Upgrade(context.Background(), "")
		Expect(err).To(MatchError(unscored))
		Expect(checked).To(Equal(releasedContent))
		Expect(contentOf(binary)).To(Equal(installedContent))
		Expect(binary + upgrade.KeptSuffix).NotTo(BeAnExistingFile())
		Expect(binary + ".new").NotTo(BeAnExistingFile())
		Expect(seen).To(BeEmpty())
	})

	It("installs a build the score check admits", func() {
		upgrader, err := upgrade.NewUpgrader(upgrade.WithBinary(binary), upgrade.WithFetch(fetchFrom(releases, &asked)), upgrade.WithRestart(restarts(binary, &seen)),
			upgrade.WithScoreCheck(func(ctx context.Context, staged string) error { return nil }))
		Expect(err).NotTo(HaveOccurred())
		_, err = upgrader.Upgrade(context.Background(), "")
		Expect(err).NotTo(HaveOccurred())
		Expect(contentOf(binary)).To(Equal(releasedContent))
		_, err = upgrade.NewUpgrader(upgrade.WithScoreCheck(nil))
		Expect(err).To(MatchError(upgrade.ErrInvalidOption))
	})

	It("refuses a checksum asset that names no SHA-256 for the binary", func() {
		releases[upgrade.LatestTag] = release{binary: releasedContent, checksums: checksumLine(releasedContent, "another-asset")}
		_, err := build(restarts(binary, &seen)).Upgrade(context.Background(), "")
		Expect(err).To(MatchError(upgrade.ErrMalformedChecksum))
		Expect(contentOf(binary)).To(Equal(installedContent))
	})

	It("rolls back to the previous binary and restarts on it when the restart fails", func() {
		_, err := build(restarts(binary, &seen, errors.New("the Workbench never answered"))).Upgrade(context.Background(), "")
		Expect(err).To(MatchError(upgrade.ErrRestartFailed))
		Expect(err.Error()).To(ContainSubstring("the Workbench never answered"))
		Expect(contentOf(binary)).To(Equal(installedContent))
		Expect(seen).To(Equal([]string{releasedContent, installedContent}))
	})

	It("says so when the restart on the previous binary fails too", func() {
		_, err := build(restarts(binary, &seen, errors.New("first"), errors.New("second"))).Upgrade(context.Background(), "")
		Expect(err).To(MatchError(upgrade.ErrRestartFailed))
		Expect(err.Error()).To(ContainSubstring("second"))
	})

	It("rolls back by swapping the installed and the kept binary, then restarts", func() {
		upgrader := build(restarts(binary, &seen))
		_, err := upgrader.Upgrade(context.Background(), "")
		Expect(err).NotTo(HaveOccurred())
		_, err = upgrader.Rollback(context.Background())
		Expect(err).NotTo(HaveOccurred())
		Expect(contentOf(binary)).To(Equal(installedContent))
		Expect(contentOf(binary + upgrade.KeptSuffix)).To(Equal(releasedContent))
		Expect(seen).To(Equal([]string{releasedContent, installedContent}))
	})

	It("refuses a rollback with nothing kept, touching nothing", func() {
		_, err := build(restarts(binary, &seen)).Rollback(context.Background())
		Expect(err).To(MatchError(upgrade.ErrNothingKept))
		Expect(contentOf(binary)).To(Equal(installedContent))
		Expect(seen).To(BeEmpty())
	})

	It("restores the swap when the restart after a rollback fails", func() {
		Expect(os.WriteFile(binary+upgrade.KeptSuffix, []byte(releasedContent), 0o755)).To(Succeed())
		_, err := build(restarts(binary, &seen, errors.New("refused"))).Rollback(context.Background())
		Expect(err).To(MatchError(upgrade.ErrRestartFailed))
		Expect(contentOf(binary)).To(Equal(installedContent))
		Expect(contentOf(binary + upgrade.KeptSuffix)).To(Equal(releasedContent))
	})

	Describe("misuse", func() {
		It("refuses to build without a binary or a restart", func() {
			_, err := upgrade.NewUpgrader(upgrade.WithBinary(binary))
			Expect(err).To(MatchError(upgrade.ErrInvalidOption))
			_, err = upgrade.NewUpgrader(upgrade.WithRestart(restarts(binary, &seen)))
			Expect(err).To(MatchError(upgrade.ErrInvalidOption))
			_, err = upgrade.NewUpgrader(upgrade.WithBinary(""))
			Expect(err).To(MatchError(upgrade.ErrInvalidOption))
			_, err = upgrade.NewUpgrader(upgrade.WithFetch(nil))
			Expect(err).To(MatchError(upgrade.ErrInvalidOption))
		})

		It("refuses an upgrade when no release download was granted", func() {
			upgrader, err := upgrade.NewUpgrader(upgrade.WithBinary(binary), upgrade.WithRestart(restarts(binary, &seen)))
			Expect(err).NotTo(HaveOccurred())
			_, err = upgrader.Upgrade(context.Background(), "")
			Expect(err).To(MatchError(upgrade.ErrInvalidOption))
			Expect(contentOf(binary)).To(Equal(installedContent))
		})
	})
})
