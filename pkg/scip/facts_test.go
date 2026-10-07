// Copyright 2026 Candace Labs

package scip_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/scip"
)

var _ = Describe("projecting an index into schema s0", func() {
	It("emits the fixed relations for each document, symbol and occurrence", func() {
		index := scip.Index{Documents: []scip.Document{{
			RelativePath: "a.go",
			Language:     "go",
			Symbols: []scip.SymbolInformation{{
				Symbol: "go . acme v1.0.0 acme/Run().",
				Relationships: []scip.Relationship{{
					Symbol:           "go . acme v1.0.0 acme/Doer#",
					IsImplementation: true,
				}},
			}},
			Occurrences: []scip.Occurrence{
				{Symbol: "local x", SymbolRoles: scip.RoleDefinition, Range: scip.Range{StartLine: 2}},
				{Symbol: "go . acme v1.0.0 acme/Run().", Range: scip.Range{StartLine: 4}},
				{Symbol: "go . acme v1.0.0 acme/Dep#", SymbolRoles: scip.RoleImport, Range: scip.Range{StartLine: 1}},
			},
		}}}

		facts, err := scip.Facts(index, scip.DeclaredLanguages)
		Expect(err).NotTo(HaveOccurred())
		Expect(facts).To(Equal([]scip.Fact{
			{Relation: "schema", Args: []string{"s0", "1"}},
			{Relation: "language", Args: []string{"go"}},
			{Relation: "document", Args: []string{"a.go"}, Span: scip.SourceSpan{Document: "a.go"}},
			{Relation: "symbol", Args: []string{"a.go", "go . acme v1.0.0 acme/Run().", "method", "go"}, Span: scip.SourceSpan{Document: "a.go"}},
			{Relation: "relationship", Args: []string{"a.go", "go . acme v1.0.0 acme/Run().", "implementation", "go . acme v1.0.0 acme/Doer#"}, Span: scip.SourceSpan{Document: "a.go"}},
			{Relation: "occurrence", Args: []string{"a.go", "local x", "definition", "2"}, Span: scip.SourceSpan{Document: "a.go", Line: 2}},
			{Relation: "occurrence", Args: []string{"a.go", "go . acme v1.0.0 acme/Run().", "reference", "4"}, Span: scip.SourceSpan{Document: "a.go", Line: 4}},
			{Relation: "occurrence", Args: []string{"a.go", "go . acme v1.0.0 acme/Dep#", "import", "1"}, Span: scip.SourceSpan{Document: "a.go", Line: 1}},
		}))
	})

	It("passes its own schema check", func() {
		index, err := scip.ReadFile("testdata/index.scip")
		Expect(err).NotTo(HaveOccurred())
		facts, err := scip.Facts(index, scip.DeclaredLanguages)
		Expect(err).NotTo(HaveOccurred())
		Expect(scip.Check(facts)).To(Succeed())
	})

	It("refuses a document whose language is outside the declared set", func() {
		index := scip.Index{Documents: []scip.Document{{RelativePath: "a.kt", Language: "kotlin"}}}
		_, err := scip.Facts(index, scip.DeclaredLanguages)
		Expect(err).To(MatchError(ContainSubstring("outside the declared languages")))
	})

	It("refuses a document that declares no language", func() {
		index := scip.Index{Documents: []scip.Document{{RelativePath: "a.go"}}}
		_, err := scip.Facts(index, scip.DeclaredLanguages)
		Expect(err).To(MatchError(ContainSubstring("declares no language")))
	})

	It("is deterministic over one index", func() {
		index, err := scip.ReadFile("testdata/index.scip")
		Expect(err).NotTo(HaveOccurred())
		first, err := scip.Facts(index, scip.DeclaredLanguages)
		Expect(err).NotTo(HaveOccurred())
		second, err := scip.Facts(index, scip.DeclaredLanguages)
		Expect(err).NotTo(HaveOccurred())
		Expect(first).To(Equal(second))
	})
})

var _ = Describe("the schema s0 check", func() {
	It("accepts a conforming stream", func() {
		Expect(scip.Check([]scip.Fact{{Relation: "document", Args: []string{"a.go"}}})).To(Succeed())
	})

	It("rejects an empty stream", func() {
		Expect(scip.Check(nil)).To(MatchError(ContainSubstring("no facts")))
	})

	It("rejects an unknown relation", func() {
		Expect(scip.Check([]scip.Fact{{Relation: "guesses", Args: []string{"a"}}})).To(MatchError(ContainSubstring("unknown relation")))
	})

	It("rejects the wrong arity", func() {
		Expect(scip.Check([]scip.Fact{{Relation: "document", Args: []string{"a.go", "b.go"}}})).To(MatchError(ContainSubstring("wants 1 args")))
	})

	It("rejects an empty argument", func() {
		Expect(scip.Check([]scip.Fact{{Relation: "document", Args: []string{""}}})).To(MatchError(ContainSubstring("empty argument")))
	})
})
