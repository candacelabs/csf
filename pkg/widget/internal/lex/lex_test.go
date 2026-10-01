package lex_test

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
	"github.com/candacelabs/csf/pkg/widget/internal/lex"
)

func scan(source string) ([]lex.Line, []diag.Finding) {
	return lex.Scan("fixture.widget", []byte(source))
}

func classesOf(findings []diag.Finding) []string {
	classes := make([]string, 0, len(findings))
	for _, finding := range findings {
		classes = append(classes, string(finding.Class))
	}
	return classes
}

var _ = Describe("Scanning lines", func() {
	It("drops blank lines and whole-line comments, and numbers what is left", func() {
		lines, findings := scan("%% a comment\n\n   \nwidget NodeStatus\n\n  dialect 0\n")
		Expect(findings).To(BeEmpty())
		Expect(lines).To(HaveLen(2))
		Expect(lines[0].Number).To(Equal(4))
		Expect(lines[1].Number).To(Equal(6))
		Expect(lines[1].Tokens[0].At.Column).To(Equal(3))
	})

	It("reads a document whose lines end with CRLF", func() {
		lines, findings := scan("widget NodeStatus\r\ndialect 0\r\n")
		Expect(findings).To(BeEmpty())
		Expect(lines).To(HaveLen(2))
		Expect(lines[0].Tokens[1].Text).To(Equal("NodeStatus"))
	})

	It("reads a document an editor saved with a byte-order mark", func() {
		lines, findings := scan("\uFEFF%% a comment\nwidget NodeStatus\n")
		Expect(findings).To(BeEmpty())
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Number).To(Equal(2))
		Expect(lines[0].Tokens[0].Text).To(Equal("widget"))
	})

	It("keeps column 1 meaning column 1 on a line that carried a byte-order mark", func() {
		lines, findings := scan("\uFEFFwidget NodeStatus\n")
		Expect(findings).To(BeEmpty())
		Expect(lines[0].Tokens[0].At.Column).To(Equal(1))
		Expect(lines[0].Tokens[1].At.Column).To(Equal(8))
	})

	It("strips only a leading mark, so one mid-document stays a token the grammar rejects", func() {
		lines, findings := scan("widget NodeStatus\n\uFEFFdialect 0\n")
		Expect(findings).To(BeEmpty())
		Expect(lines).To(HaveLen(2))
		Expect(lines[1].Tokens[0].Text).To(Equal("\uFEFFdialect"))
	})

	It("reads an empty document as no lines at all", func() {
		lines, findings := scan("")
		Expect(lines).To(BeEmpty())
		Expect(findings).To(BeEmpty())
	})

	It("keeps a comment that annotates the statement below it out of the tokens", func() {
		lines, _ := scan("%% W409 — nothing writes `orphan`.\n  field orphan type count\n")
		Expect(lines).To(HaveLen(1))
		Expect(lines[0].Tokens).To(HaveLen(4))
	})
})

var _ = Describe("Quoted text", func() {
	It("keeps spaces, and reports the contents without the delimiters", func() {
		lines, findings := scan(`region "widget.cluster heartbeats"`)
		Expect(findings).To(BeEmpty())
		Expect(lines[0].Tokens).To(HaveLen(2))
		Expect(lines[0].Tokens[1].Kind).To(Equal(lex.KindString))
		Expect(lines[0].Tokens[1].Text).To(Equal("widget.cluster heartbeats"))
		Expect(lines[0].Tokens[1].Span().EndColumn - lines[0].Tokens[1].At.Column).To(Equal(27))
	})

	It("treats a `%%` inside a literal as text, because there is no trailing comment form", func() {
		lines, findings := scan(`label rateLabel text "100%% of voters"`)
		Expect(findings).To(BeEmpty())
		Expect(lines[0].Tokens[3].Text).To(Equal("100%% of voters"))
	})

	It("reports a literal that is not closed before the end of the line", func() {
		lines, findings := scan(`label titleLabel text "Node status` + "\n")
		Expect(classesOf(findings)).To(Equal([]string{"W102"}))
		Expect(findings[0].At.Column).To(Equal(23))
		Expect(lines[0].Tokens[3].Text).To(Equal("Node status"))
	})

	It("counts columns in code points, so a multi-byte literal does not shift what follows", func() {
		lines, _ := scan(`  label termLabel text "term — 3" binds nothing`)
		Expect(lines[0].Tokens[4].Text).To(Equal("binds"))
		Expect(lines[0].Tokens[4].At.Column).To(Equal(35))
	})
})

var _ = Describe("Comment forms", func() {
	It("reports a comment that follows content on the same line", func() {
		_, findings := scan("  token accent %% the brand colour\n")
		Expect(classesOf(findings)).To(Equal([]string{"W508"}))
		Expect(findings[0].At.Column).To(Equal(16))
	})

	It("reports a mermaid init directive, which is a comment nowhere else", func() {
		_, findings := scan("%%{init: {\"theme\": \"dark\"}}%%\nwidget NodeStatus\n")
		Expect(classesOf(findings)).To(Equal([]string{"W504"}))
		Expect(findings[0].At).To(Equal(diag.SourcePosition{File: "fixture.widget", Line: 1, Column: 1}))
	})
})

var _ = DescribeTable("Token kinds",
	func(text string, expected lex.Kind) {
		lines, _ := scan("clause " + text)
		Expect(lines[0].Tokens[1].Kind).To(Equal(expected))
	},
	Entry("an identifier", "nodeA", lex.KindWord),
	Entry("an identifier that is also a keyword elsewhere", "node", lex.KindWord),
	Entry("a wire field name", "alive_voters", lex.KindWord),
	Entry("an integer", "820", lex.KindInteger),
	Entry("a negative integer", "-3", lex.KindInteger),
	Entry("a millisecond sigil", "820ms", lex.KindTimeLiteral),
	Entry("a second sigil", "0.82s", lex.KindTimeLiteral),
	Entry("a mermaid arrow", "-->", lex.KindMermaidEdge),
	Entry("a mermaid open link", "---", lex.KindMermaidEdge),
	Entry("a mermaid thick arrow", "==>", lex.KindMermaidEdge),
	Entry("a mermaid cross arrow", "--x", lex.KindMermaidEdge),
	Entry("a mermaid circle arrow", "--o", lex.KindMermaidEdge),
	Entry("a mermaid bidirectional arrow", "<-->", lex.KindMermaidEdge),
	Entry("a mermaid invisible link", "~~~", lex.KindMermaidEdge),
	Entry("a mermaid labelled arrow", "-->|beats|", lex.KindMermaidEdge),
	Entry("a square node shape", "nodeA[Label]", lex.KindShapeBracket),
	Entry("a round node shape", "nodeA(Label)", lex.KindShapeBracket),
	Entry("a rhombus node shape", "nodeA{Label}", lex.KindShapeBracket),
	Entry("a hex colour", "#7a8b5f", lex.KindColour),
	Entry("a short hex colour", "#fff", lex.KindColour),
	Entry("an rgb call", "rgb(122,139,95)", lex.KindColour),
	Entry("a custom property reference", "var(--archive)", lex.KindColour),
	Entry("a quoted literal", `"text"`, lex.KindString),
	Entry("anything else", "±", lex.KindOther),
)

var _ = Describe("Token spans", func() {
	It("closes a span one past the token's last code point", func() {
		lines, _ := scan("widget NodeStatus")
		span := lines[0].Tokens[1].Span()
		Expect(span.StartColumn).To(Equal(8))
		Expect(span.EndColumn).To(Equal(18))
		Expect(span.StartLine).To(Equal(span.EndLine))
		Expect(span.Start()).To(Equal(lines[0].Tokens[1].At))
	})
})
