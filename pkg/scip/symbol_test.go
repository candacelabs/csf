// Copyright 2026 Candace Labs

package scip_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/scip"
)

var _ = Describe("the symbol grammar", func() {
	DescribeTable("parses a symbol into scheme, package and descriptors",
		func(text, scheme, manager, name, version, suffix string) {
			symbol, err := scip.ParseSymbol(text)
			Expect(err).NotTo(HaveOccurred())
			Expect(symbol.Local).To(BeFalse())
			Expect(symbol.Scheme).To(Equal(scheme))
			Expect(symbol.Manager).To(Equal(manager))
			Expect(symbol.Package).To(Equal(name))
			Expect(symbol.Version).To(Equal(version))
			Expect(symbol.Descriptors[len(symbol.Descriptors)-1].Suffix.String()).To(Equal(suffix))
		},
		Entry("a go namespace", "scip-go gomod github.com/sourcegraph/scip v0.6.0 cmd/scip/", "scip-go", "gomod", "github.com/sourcegraph/scip", "v0.6.0", "namespace"),
		Entry("a typescript class method", "scip-typescript npm pkg 1.0.0 src/`file.ts`/Foo#bar().", "scip-typescript", "npm", "pkg", "1.0.0", "method"),
		Entry("a rust function", "rust-analyzer cargo acme 0.1.0 acme/fn greet().", "rust-analyzer", "cargo", "acme", "0.1.0", "method"),
		Entry("a python module term", "scip-python python acme 0.1.0 acme/mod.", "scip-python", "python", "acme", "0.1.0", "term"),
		Entry("a java type", "scip-java maven acme 1.0 com/acme/App#", "scip-java", "maven", "acme", "1.0", "type"),
	)

	It("parses a local symbol", func() {
		symbol, err := scip.ParseSymbol("local 3")
		Expect(err).NotTo(HaveOccurred())
		Expect(symbol.Local).To(BeTrue())
		Expect(symbol.LocalID).To(Equal("3"))
	})

	It("reads a method's disambiguator", func() {
		symbol, err := scip.ParseSymbol("scip-go gomod acme v1.0.0 acme/Foo#bar(+).")
		Expect(err).NotTo(HaveOccurred())
		last := symbol.Descriptors[len(symbol.Descriptors)-1]
		Expect(last.Suffix).To(Equal(scip.SuffixMethod))
		Expect(last.Disambiguator).To(Equal("+"))
	})

	It("reports the last descriptor's suffix, and unspecified when it does not parse", func() {
		Expect(scip.SymbolSuffix("scip-go gomod acme v1.0.0 acme/Foo#")).To(Equal("type"))
		Expect(scip.SymbolSuffix("not a symbol")).To(Equal("unspecified"))
	})

	DescribeTable("refuses a malformed symbol",
		func(text string) {
			_, err := scip.ParseSymbol(text)
			Expect(err).To(HaveOccurred())
		},
		Entry("no scheme", " "),
		Entry("no descriptor", "scip-go gomod acme v1.0.0"),
		Entry("an unterminated parameter", "scip-go gomod acme v1.0.0 acme/(x"),
	)
})
