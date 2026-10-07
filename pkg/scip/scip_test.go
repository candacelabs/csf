// Copyright 2026 Candace Labs

package scip_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/scip"
)

var _ = Describe("decoding a real indexer's output", func() {
	It("reads the gzipped protobuf an indexer writes", func() {
		index, err := scip.ReadFile("testdata/index.scip")
		Expect(err).NotTo(HaveOccurred())
		Expect(index.Metadata.ToolName).To(Equal("scip-go"))
		Expect(index.Metadata.ToolVersion).To(Equal("v0.1.0"))
		Expect(index.Metadata.ProjectRoot).To(Equal("file:///tmp/repo"))
	})

	It("reads the documents, symbols and occurrences the indexer recorded", func() {
		index, err := scip.ReadFile("testdata/index.scip")
		Expect(err).NotTo(HaveOccurred())
		Expect(index.Documents).To(HaveLen(1))

		document := index.Documents[0]
		Expect(document.RelativePath).To(Equal("main.go"))
		Expect(document.Language).To(Equal("go"))
		Expect(document.Occurrences).To(HaveLen(3))
		Expect(document.Occurrences[0].Symbol).To(Equal("local v"))
		Expect(document.Occurrences[0].HasRole(scip.RoleDefinition)).To(BeTrue())
		Expect(document.Occurrences[0].Range.StartLine).To(Equal(int32(0)))
		Expect(document.Occurrences[2].HasRole(scip.RoleDefinition)).To(BeFalse())

		Expect(document.Symbols).To(HaveLen(1))
		symbol := document.Symbols[0]
		Expect(symbol.Symbol).To(Equal("go . github.com/example/acme v1.0.0 Acme/"))
		Expect(symbol.Relationships).To(HaveLen(2))
		Expect(symbol.Relationships[0].Symbol).To(Equal("go . github.com/example/acme v1.0.0 Base/"))
		Expect(symbol.Relationships[0].IsImplementation).To(BeTrue())
		Expect(symbol.Relationships[0].IsReference).To(BeTrue())
		Expect(symbol.Relationships[1].IsTypeDefinition).To(BeTrue())
	})
})
