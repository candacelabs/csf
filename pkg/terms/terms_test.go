// Copyright 2026 Candace Labs

package terms_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/terms"
)

var _ = Describe("Extract", func() {
	It("keeps the words that are not code, identifiers, short or common English, once per stem", func() {
		text := "no dude i think cgroup sandboxing; cgroups are `cgroup.kill` in /sys/fs/cgroup for ClaudeCodeSession at 7d3d7a2e-2c55-4f45 ```\nsandbox everything\n```"
		Expect(terms.Extract(text)).To(Equal([]terms.Term{"cgroup", "sandboxing"}))
	})

	It("lowercases and splits hyphenated and apostrophe-joined words", func() {
		Expect(terms.Extract("Lehman-Yao ordering with B-link trees, the operator's crablocks")).To(Equal(
			[]terms.Term{"lehman", "yao", "crablocks"}))
	})

	It("collapses stretched letters and reads nothing from an empty text", func() {
		Expect(terms.Extract("fineeeee, use initramfs")).To(Equal([]terms.Term{"initramfs"}))
		Expect(terms.Extract("")).To(BeEmpty())
		Expect(terms.Extract("the a of")).To(BeEmpty())
	})

	It("drops inflections of common words through the stem", func() {
		Expect(terms.Extract("violated quoting labelled preceded")).To(BeEmpty())
	})
})

var _ = Describe("Vocabulary", func() {
	It("reports the novel terms of a text against what it has seen, one per stem", func() {
		vocabulary := terms.NewVocabulary()
		vocabulary.Add(terms.Extract("every process spawned by a session belongs to its cgroup")...)
		Expect(vocabulary.Len()).To(Equal(2))
		Expect(vocabulary.Has("Cgroups")).To(BeTrue(), "a spelling with the same stem")
		Expect(vocabulary.Has("spawn")).To(BeTrue())
		Expect(vocabulary.Novel("no dude i think cgroup sandboxing")).To(Equal([]terms.Term{"sandboxing"}))
		Expect(vocabulary.Novel("initramfs is the minikernel, like my analogies")).To(Equal(
			[]terms.Term{"initramfs", "minikernel", "analogies"}))
		Expect(vocabulary.Has("initramfs")).To(BeFalse(), "Novel adds nothing")
	})

	It("starts empty, so every term of the first text is novel", func() {
		vocabulary := terms.NewVocabulary()
		Expect(vocabulary.Len()).To(BeZero())
		Expect(vocabulary.Novel("no dude i think cgroup sandboxing")).To(Equal([]terms.Term{"cgroup", "sandboxing"}))
		Expect(vocabulary.Novel("")).To(BeEmpty())
	})
})

var _ = Describe("Same", func() {
	It("matches two spellings of one term and no others", func() {
		Expect(terms.Same("Cgroups", "cgroup")).To(BeTrue())
		Expect(terms.Same(" analogies ", "analogy")).To(BeTrue())
		Expect(terms.Same("cgroup", "group")).To(BeFalse())
		Expect(terms.Same("sandboxing", "sandbox")).To(BeTrue())
	})
})
