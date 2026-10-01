package labels_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/labels"
)

var _ = Describe("Canonical", func() {
	It("trims surrounding whitespace and lowercases", func() {
		Expect(labels.Canonical("  Docker\t")).To(Equal("docker"))
	})

	It("canonicalizes an all-whitespace label to the empty string", func() {
		Expect(labels.Canonical(" \t ")).To(BeEmpty())
	})
})

var _ = Describe("Canonicalize", func() {
	It("lowercases, deduplicates, and preserves first-seen order", func() {
		Expect(labels.Canonicalize([]string{" Docker", "candace", "docker", "GPU "})).
			To(Equal([]string{"docker", "candace", "gpu"}))
	})

	It("drops labels that canonicalize to the empty string", func() {
		Expect(labels.Canonicalize([]string{"", "  ", "one"})).To(Equal([]string{"one"}))
	})

	It("returns an empty list unchanged", func() {
		Expect(labels.Canonicalize(nil)).To(BeEmpty())
	})
})

var _ = Describe("Set", func() {
	It("holds canonical members only", func() {
		set := labels.Set([]string{"Self-Hosted", " docker ", ""})
		Expect(set).To(HaveLen(2))
		Expect(set).To(HaveKey("self-hosted"))
		Expect(set).To(HaveKey("docker"))
	})
})

var _ = Describe("ContainsAll", func() {
	It("matches required labels case-insensitively", func() {
		Expect(labels.ContainsAll([]string{"SELF-HOSTED", "Candace", "Docker", "extra"},
			"self-hosted", "candace", "docker")).To(BeTrue())
	})

	It("rejects a list missing any required label", func() {
		Expect(labels.ContainsAll([]string{"self-hosted", "candace"},
			"self-hosted", "candace", "docker")).To(BeFalse())
	})

	It("ignores required labels that canonicalize to the empty string", func() {
		Expect(labels.ContainsAll([]string{"docker"}, " ", "DOCKER")).To(BeTrue())
	})

	It("accepts any list when nothing is required", func() {
		Expect(labels.ContainsAll(nil)).To(BeTrue())
	})
})
