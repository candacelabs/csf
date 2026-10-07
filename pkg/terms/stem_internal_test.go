// Copyright 2026 Candace Labs

package terms

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

var _ = Describe("the Porter stemmer", func() {
	DescribeTable("stems the paper's examples", func(word string, stem string) {
		Expect(Stem(word)).To(Equal(stem))
	},
		// Step 1a.
		Entry("caresses", "caresses", "caress"), Entry("ponies", "ponies", "poni"), Entry("ties", "ties", "ti"),
		Entry("caress", "caress", "caress"), Entry("cats", "cats", "cat"),
		// Step 1b.
		Entry("feed", "feed", "feed"), Entry("agreed", "agreed", "agre"), Entry("plastered", "plastered", "plaster"),
		Entry("bled", "bled", "bled"), Entry("motoring", "motoring", "motor"), Entry("sing", "sing", "sing"),
		Entry("conflated", "conflated", "conflat"), Entry("troubled", "troubled", "troubl"), Entry("sized", "sized", "size"),
		Entry("hopping", "hopping", "hop"), Entry("tanned", "tanned", "tan"), Entry("falling", "falling", "fall"),
		Entry("hissing", "hissing", "hiss"), Entry("fizzed", "fizzed", "fizz"), Entry("failing", "failing", "fail"),
		Entry("filing", "filing", "file"),
		// Step 1c.
		Entry("happy", "happy", "happi"), Entry("sky", "sky", "sky"),
		// Step 2.
		Entry("relational", "relational", "relat"), Entry("conditional", "conditional", "condit"),
		Entry("rational", "rational", "ration"), Entry("digitizer", "digitizer", "digit"),
		Entry("operator", "operator", "oper"), Entry("feudalism", "feudalism", "feudal"),
		Entry("decisiveness", "decisiveness", "decis"), Entry("hopefulness", "hopefulness", "hope"),
		Entry("callousness", "callousness", "callous"), Entry("formality", "formality", "formal"),
		Entry("sensitivity", "sensitivity", "sensit"), Entry("sensibility", "sensibility", "sensibl"),
		// Step 3.
		Entry("triplicate", "triplicate", "triplic"), Entry("formative", "formative", "form"),
		Entry("formalize", "formalize", "formal"), Entry("electricity", "electricity", "electr"),
		Entry("electrical", "electrical", "electr"), Entry("hopeful", "hopeful", "hope"), Entry("goodness", "goodness", "good"),
		// Step 4.
		Entry("revival", "revival", "reviv"), Entry("allowance", "allowance", "allow"), Entry("inference", "inference", "infer"),
		Entry("airliner", "airliner", "airlin"), Entry("gyroscopic", "gyroscopic", "gyroscop"),
		Entry("adjustable", "adjustable", "adjust"), Entry("defensible", "defensible", "defens"),
		Entry("irritant", "irritant", "irrit"), Entry("replacement", "replacement", "replac"),
		Entry("adjustment", "adjustment", "adjust"), Entry("dependent", "dependent", "depend"),
		Entry("adoption", "adoption", "adopt"), Entry("homologous", "homologous", "homolog"),
		Entry("communism", "communism", "commun"), Entry("activate", "activate", "activ"),
		Entry("angularity", "angularity", "angular"), Entry("effective", "effective", "effect"),
		Entry("bowdlerize", "bowdlerize", "bowdler"),
		// Step 5.
		Entry("probate", "probate", "probat"), Entry("rate", "rate", "rate"), Entry("cease", "cease", "ceas"),
		Entry("controll", "controll", "control"), Entry("roll", "roll", "roll"),
		// What the harness relies on.
		Entry("cgroups and cgroup agree", "cgroups", "cgroup"), Entry("analogies", "analogies", "analogi"),
		Entry("analogy", "analogy", "analogi"), Entry("sandboxing", "sandboxing", "sandbox"),
	)

	It("leaves short words and non-letters alone", func() {
		Expect(Stem("is")).To(Equal("is"))
		Expect(Stem("a")).To(Equal("a"))
		Expect(Stem("")).To(Equal(""))
		Expect(Stem("x86")).To(Equal("x86"))
		Expect(Stem("Caresses")).To(Equal("Caresses"))
	})
})

var _ = Describe("word normalization", func() {
	It("collapses a run of three or more of one letter to one", func() {
		Expect(collapseRepeats("fineeeee")).To(Equal("fine"))
		Expect(collapseRepeats("reeeee")).To(Equal("re"))
		Expect(collapseRepeats("committee")).To(Equal("committee"))
		Expect(collapseRepeats("")).To(Equal(""))
	})

	It("reads camelCase as an identifier and acronyms as words", func() {
		Expect(isCamelCase("ClaudeCodeSession")).To(BeTrue())
		Expect(isCamelCase("persistQueue")).To(BeTrue())
		Expect(isCamelCase("CSF")).To(BeFalse())
		Expect(isCamelCase("Cgroups")).To(BeFalse())
	})

	It("loads the stoplist with its stems and skips comments", func() {
		set := loadStoplist("# provenance\nthe\nviolate\n\n")
		Expect(set.has("the", Stem("the"))).To(BeTrue())
		Expect(set.has("violated", Stem("violated"))).To(BeTrue(), "an inflection of a common word is common")
		Expect(set.has("cgroup", Stem("cgroup"))).To(BeFalse())
		Expect(set.has("#", Stem("#"))).To(BeFalse())
	})

	It("ships the fitted stoplist", func() {
		Expect(len(stoplist.words)).To(Equal(10000))
		Expect(stoplist.has("the", Stem("the"))).To(BeTrue())
		Expect(stoplist.has("cgroup", Stem("cgroup"))).To(BeFalse())
	})
})
