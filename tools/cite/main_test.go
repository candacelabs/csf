package main

import (
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
)

func TestCite(t *testing.T) {
	RegisterFailHandler(Fail)
	RunSpecs(t, "Cite Suite")
}

var _ = Describe("cite", func() {
	Describe("canonicalize", func() {
		It("canonicalizes DOI keys", func() {
			Expect(canonicalize("10.1145/222124.222136")).To(Equal("doi:10.1145/222124.222136"))
		})

		It("extracts DOI from text", func() {
			Expect(canonicalize("see 10.1145/222124.222136 in the paper")).To(Equal("doi:10.1145/222124.222136"))
		})

		It("lowercases DOI", func() {
			Expect(canonicalize("10.1016/0306-4379(78)90001-7")).To(Equal("doi:10.1016/0306-4379(78)90001-7"))
		})

		It("canonicalizes arXiv keys", func() {
			Expect(canonicalize("2310.03714v2")).To(Equal("arxiv:2310.03714"))
		})

		It("extracts arXiv from text", func() {
			Expect(canonicalize("see paper 2310.03714v1 for details")).To(Equal("arxiv:2310.03714"))
		})

		It("canonicalizes URL keys", func() {
			Expect(canonicalize("https://www.w3.org/TR/2013/REC-prov-dm-20130430/")).To(Equal("url:https://www.w3.org/TR/2013/REC-prov-dm-20130430"))
		})

		It("strips trailing slash from URL", func() {
			Expect(canonicalize("https://example.com/path/")).To(Equal("url:https://example.com/path"))
		})

		It("handles multiword input for URL", func() {
			Expect(canonicalize("https://example.com/path extra text")).To(Equal("url:https://example.com/path"))
		})
	})

	Describe("isNumeric", func() {
		It("returns true for numeric strings", func() {
			Expect(isNumeric("123")).To(BeTrue())
		})

		It("returns false for non-numeric strings", func() {
			Expect(isNumeric("abc")).To(BeFalse())
		})

		It("returns false for empty strings", func() {
			Expect(isNumeric("")).To(BeFalse())
		})
	})

	Describe("vacuous", func() {
		It("returns true for nil entry", func() {
			Expect(vacuous(nil)).To(BeTrue())
		})

		It("returns true for entry with empty title", func() {
			Expect(vacuous(&Citation{Title: "", Authors: "A", Year: 2020, Where: "X"})).To(BeTrue())
		})

		It("returns true for entry with empty where", func() {
			Expect(vacuous(&Citation{Title: "T", Authors: "A", Year: 2020, Where: ""})).To(BeTrue())
		})

		It("returns false for complete entry", func() {
			Expect(vacuous(&Citation{Title: "T", Authors: "A", Year: 2020, Where: "X"})).To(BeFalse())
		})
	})

	Describe("renderBibTeX", func() {
		It("generates valid BibTeX format", func() {
			e := &Citation{
				Title:   "Example Paper",
				Authors: "Jane Doe, John Smith",
				Year:    2020,
				Where:   "Journal Name",
				URL:     "https://example.com",
			}
			result := renderBibTeX("mykey", e)
			Expect(result).To(ContainSubstring("@article{mykey,"))
			Expect(result).To(ContainSubstring("author = {Jane Doe, John Smith}"))
			Expect(result).To(ContainSubstring("title = {Example Paper}"))
			Expect(result).To(ContainSubstring("year = {2020}"))
		})

		It("handles nil entry", func() {
			result := renderBibTeX("key", nil)
			Expect(result).To(Equal(""))
		})

		It("handles missing metadata", func() {
			e := &Citation{Title: "", Authors: "", Year: nil, Where: "", URL: ""}
			result := renderBibTeX("key", e)
			Expect(result).To(ContainSubstring("author = {Unknown}"))
			Expect(result).To(ContainSubstring("title = {Untitled}"))
			Expect(result).To(ContainSubstring("year = {0}"))
		})
	})
})
