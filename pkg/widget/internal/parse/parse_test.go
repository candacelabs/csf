package parse_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
	"github.com/candacelabs/csf/pkg/widget/internal/parse"
)

const preamble = `widget NodeStatus
dialect 0
region "widget.node-status"
palette fieldStation
`

func parseSource(source string) (parse.Document, []string) {
	document, findings := parse.Parse("fixture.widget", []byte(source))
	classes := make([]string, 0, len(findings))
	for _, finding := range findings {
		classes = append(classes, string(finding.Class))
	}
	return document, classes
}

func blockNames(document parse.Document) []string {
	names := make([]string, 0, len(document.Blocks))
	for _, block := range document.Blocks {
		names = append(names, block.Spec.Name)
	}
	return names
}

var _ = Describe("Document shape", func() {
	It("separates the preamble from the blocks", func() {
		document, classes := parseSource(preamble + "state\n  field reachable type flag\nend\n")
		Expect(classes).To(ContainElement("W011"))
		Expect(document.Preamble).To(HaveLen(4))
		Expect(blockNames(document)).To(Equal([]string{"state"}))
		Expect(document.Blocks[0].Statements).To(HaveLen(1))
		Expect(document.Blocks[0].Statements[0].Keyword.Text).To(Equal("field"))
		Expect(document.Blocks[0].Statements[0].Arguments).To(HaveLen(3))
	})

	It("reports the first statement of a document even when it is a block", func() {
		document, _ := parseSource("state\nend\n")
		first, present := document.FirstStatement()
		Expect(present).To(BeTrue())
		Expect(first.Keyword.Text).To(Equal("state"))
	})

	It("has no first statement and a line-one anchor when the document is empty", func() {
		document, _ := parseSource("%% nothing but a comment\n")
		_, present := document.FirstStatement()
		Expect(present).To(BeFalse())
		Expect(document.LastPreamblePosition()).To(Equal(diag.SourcePosition{File: "fixture.widget", Line: 1, Column: 1}))
	})

	It("nests a declaration inside its block and closes both at their `end`", func() {
		document, _ := parseSource(preamble + "roles\n  role peer\n    token accent\n  end\nend\n")
		Expect(document.Blocks[0].Declarations).To(HaveLen(1))
		declaration := document.Blocks[0].Declarations[0]
		Expect(declaration.Header.Arguments[0].Text).To(Equal("peer"))
		Expect(declaration.Statements).To(HaveLen(1))
		Expect(declaration.Span().StartLine).To(Equal(6))
		Expect(declaration.Span().EndLine).To(Equal(8))
		Expect(document.Blocks[0].Span().EndLine).To(Equal(9))
	})

	It("keeps a clause keyword that names a nested declaration elsewhere as a clause", func() {
		// `edge` and `node` open blocks inside `scene` and are clauses inside
		// `motion`, which is why the opener set is per container.
		document, classes := parseSource(preamble + "motion\n  pulse beat\n    edge link\n    channel beat\n  end\nend\n")
		Expect(classes).ToNot(ContainElement("W008"))
		Expect(document.Blocks[0].Declarations[0].Statements).To(HaveLen(2))
	})
})

var _ = DescribeTable("Structural findings",
	func(source string, expected []string) {
		_, classes := parseSource(preamble + source)
		for _, class := range expected {
			Expect(classes).To(ContainElement(class))
		}
	},
	Entry("an unknown block name", "stateful\n", []string{"W004"}),
	Entry("a statement at document scope", "field stray type flag\n", []string{"W010"}),
	Entry("a stray `end`", "end\n", []string{"W009"}),
	Entry("a dropped mermaid keyword at document scope", "subgraph cluster\n", []string{"W503"}),
	Entry("a block that is never closed", "state\n  field reachable type flag\n", []string{"W008"}),
	Entry("a declaration that is never closed", "roles\n  role peer\n    token accent\n", []string{"W008"}),
	Entry("an empty block", "state\nend\n", []string{"W007"}),
	Entry("a repeated block", "state\n  field a type flag\nend\n\nstate\n  field b type flag\nend\n", []string{"W006"}),
	Entry("blocks out of canonical order", "chrome\n  title titleLabel\nend\n\nlabels\n  label titleLabel text \"t\"\nend\n", []string{"W005"}),
	Entry("a declaration nested too deep", "roles\n  role peer\n    role inner\n      token ink\n    end\n  end\nend\n", []string{"W012"}),
)

var _ = Describe("Recovery", func() {
	It("closes an unclosed block at the next block header rather than swallowing the rest", func() {
		document, classes := parseSource(preamble + "state\n  field reachable type flag\n\nlabels\n  label titleLabel text \"t\"\nend\n")
		Expect(classes).To(ContainElement("W008"))
		Expect(blockNames(document)).To(Equal([]string{"state", "labels"}))
		Expect(document.Blocks[1].Statements).To(HaveLen(1))
	})

	It("closes an unclosed declaration at the next block header too", func() {
		document, classes := parseSource(preamble + "roles\n  role peer\n    token accent\n\nplacements\n  placement centre left 50 top 50\nend\n")
		Expect(classes).To(ContainElement("W008"))
		Expect(blockNames(document)).To(Equal([]string{"roles", "placements"}))
	})

	It("keeps reading after a declaration nested too deep", func() {
		document, _ := parseSource(preamble + "roles\n  role peer\n    role inner\n      token ink\n    end\n    token accent\n    marker large\n  end\nend\n")
		Expect(document.Blocks[0].Declarations[0].Statements).To(HaveLen(2))
	})

	It("reports every required block that is absent, at the last preamble line", func() {
		document, classes := parseSource(preamble)
		count := 0
		for _, class := range classes {
			if class == "W011" {
				count++
			}
		}
		Expect(count).To(Equal(6))
		Expect(document.LastPreamblePosition().Line).To(Equal(4))
	})
})

var _ = Describe("The block table", func() {
	It("names fourteen blocks in canonical order", func() {
		names := parse.BlockNames()
		Expect(names).To(HaveLen(14))
		Expect(names[0]).To(Equal("state"))
		Expect(names[13]).To(Equal("data"))
		for index, spec := range parse.BlockSpecs() {
			Expect(spec.Ordinal).To(Equal(index + 1))
		}
	})

	It("names every dropped mermaid keyword with the construct that replaced it", func() {
		for _, keyword := range []string{"direction", "classDef", "class", "style", "subgraph", "linkStyle"} {
			replacement, dropped := parse.DroppedMermaidKeyword(keyword)
			Expect(dropped).To(BeTrue())
			Expect(replacement).ToNot(BeEmpty())
		}
		_, dropped := parse.DroppedMermaidKeyword("role")
		Expect(dropped).To(BeFalse())
	})

	It("tells a statement keyword apart from an unknown word", func() {
		Expect(parse.IsStatementKeyword("carries")).To(BeTrue())
		Expect(parse.IsStatementKeyword("carrys")).To(BeFalse())
		_, isBlock := parse.LookupBlock("scene")
		Expect(isBlock).To(BeTrue())
		_, isBlock = parse.LookupBlock("scenes")
		Expect(isBlock).To(BeFalse())
	})
})
