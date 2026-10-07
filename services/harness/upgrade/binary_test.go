// Copyright 2026 Candace Labs

package upgrade_test

import (
	"errors"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/harness/upgrade"
)

const (
	installed = "/usr/local/bin/csf"
	self      = "/tmp/upgrade/csf"
)

// replacedLink is the /proc/<pid>/exe link of a host whose executable was
// replaced after it started.
func replacedLink() (string, error) { return installed + upgrade.DeletedSuffix, nil }

func selfPath() (string, error) { return self, nil }

var _ = Describe("ChooseBinary", func() {
	It("prefers the path the host recorded over a /proc link carrying the deleted suffix", func() {
		path, source, err := upgrade.ChooseBinary(upgrade.BinaryCandidates{Recorded: installed, Running: true, ProcessExecutable: replacedLink, Self: selfPath})
		Expect(err).NotTo(HaveOccurred())
		Expect(path).To(Equal(installed))
		Expect(source).To(Equal(upgrade.SourceRecord))
	})

	It("strips the deleted suffix from the /proc link of a host that recorded no path", func() {
		path, source, err := upgrade.ChooseBinary(upgrade.BinaryCandidates{Running: true, ProcessExecutable: replacedLink, Self: selfPath})
		Expect(err).NotTo(HaveOccurred())
		Expect(path).To(Equal(installed))
		Expect(source).To(Equal(upgrade.SourceProcessDeleted))
	})

	It("uses an intact /proc link as it is", func() {
		path, source, err := upgrade.ChooseBinary(upgrade.BinaryCandidates{Running: true,
			ProcessExecutable: func() (string, error) { return installed, nil }, Self: selfPath})
		Expect(err).NotTo(HaveOccurred())
		Expect(path).To(Equal(installed))
		Expect(source).To(Equal(upgrade.SourceProcess))
	})

	It("lets -binary win over every other source", func() {
		path, source, err := upgrade.ChooseBinary(upgrade.BinaryCandidates{Flag: "/opt/csf", Recorded: installed, Running: true, ProcessExecutable: replacedLink, Self: selfPath})
		Expect(err).NotTo(HaveOccurred())
		Expect(path).To(Equal("/opt/csf"))
		Expect(source).To(Equal(upgrade.SourceFlag))
	})

	It("falls back to this command's own executable when no host runs", func() {
		path, source, err := upgrade.ChooseBinary(upgrade.BinaryCandidates{Self: selfPath})
		Expect(err).NotTo(HaveOccurred())
		Expect(path).To(Equal(self))
		Expect(source).To(Equal(upgrade.SourceSelf))
	})

	It("reports an unreadable /proc link", func() {
		_, _, err := upgrade.ChooseBinary(upgrade.BinaryCandidates{Running: true,
			ProcessExecutable: func() (string, error) { return "", errors.New("permission denied") }})
		Expect(err).To(MatchError(ContainSubstring("permission denied")))
	})

	It("refuses a choice it has nothing to read from", func() {
		_, _, err := upgrade.ChooseBinary(upgrade.BinaryCandidates{Running: true})
		Expect(err).To(MatchError(upgrade.ErrInvalidOption))
		_, _, err = upgrade.ChooseBinary(upgrade.BinaryCandidates{})
		Expect(err).To(MatchError(upgrade.ErrInvalidOption))
	})

	It("reads a /proc link as a path", func() {
		Expect(upgrade.ExecutablePath(installed + upgrade.DeletedSuffix)).To(Equal(installed))
		Expect(upgrade.ExecutablePath(installed)).To(Equal(installed))
	})
})
