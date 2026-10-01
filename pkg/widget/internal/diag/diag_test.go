package diag_test

import (
	"strings"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
)

func at(line int, column int) diag.SourcePosition {
	return diag.SourcePosition{File: "doc.widget", Line: line, Column: column}
}

var _ = Describe("The message template", func() {
	It("prints the anchor, the class, the message and the fix, each closed with a full stop", func() {
		finding := diag.New(diag.ClassBindingNotTotal, at(56, 3),
			"binding \"statusText\" has no `otherwise` clause",
			"add `otherwise \"<text>\"` as the last line of the binding")
		Expect(finding.String()).To(Equal(
			"doc.widget:56:3: W401: binding \"statusText\" has no `otherwise` clause.\n" +
				"    fix: add `otherwise \"<text>\"` as the last line of the binding.\n"))
	})

	It("prints a secondary anchor between the message and the fix", func() {
		finding := diag.New(diag.ClassPlacementShared, at(159, 5), "placement \"here\" already holds node \"nodeA\"", "add a second placement").
			WithRelated(at(150, 5), "\"here\" is used here by node \"nodeA\"")
		lines := strings.Split(strings.TrimSuffix(finding.String(), "\n"), "\n")
		Expect(lines).To(HaveLen(3))
		Expect(lines[1]).To(Equal("    doc.widget:150:5: \"here\" is used here by node \"nodeA\""))
		Expect(lines[2]).To(HavePrefix("    fix: "))
	})

	It("leaves a message that already ends in a full stop alone", func() {
		finding := diag.New(diag.ClassEdgeSelfLoop, at(1, 1), "edge \"loop\" joins node \"nodeA\" to itself.", "point `to` at another node.")
		Expect(finding.Message).To(HaveSuffix("itself."))
		Expect(finding.Message).ToNot(HaveSuffix(".."))
		Expect(finding.Fix).ToNot(HaveSuffix(".."))
	})

	It("copies rather than shares a finding's secondary anchors", func() {
		base := diag.New(diag.ClassEdgeSelfLoop, at(1, 1), "message", "fix").WithRelated(at(2, 1), "first")
		branch := base.WithRelated(at(3, 1), "second")
		Expect(base.Related).To(HaveLen(1))
		Expect(branch.Related).To(HaveLen(2))
	})
})

var _ = Describe("Sorting", func() {
	It("orders by line, then column, then class", func() {
		findings := []diag.Finding{
			diag.New(diag.ClassStateFieldUnwritten, at(9, 3), "third", "fix"),
			diag.New(diag.ClassIntegerOutOfRange, at(4, 20), "second", "fix"),
			diag.New(diag.ClassDeclarationUnreferenced, at(4, 3), "first", "fix"),
			diag.New(diag.ClassBindingNotTotal, at(4, 3), "also first", "fix"),
		}
		diag.Sort(findings)
		Expect(findings[0].Class).To(Equal(diag.ClassBindingNotTotal))
		Expect(findings[1].Class).To(Equal(diag.ClassDeclarationUnreferenced))
		Expect(findings[2].Message).To(Equal("second."))
		Expect(findings[3].Message).To(Equal("third."))
	})
})

var _ = Describe("Quoting author text", func() {
	It("quotes short text whole", func() {
		Expect(diag.Quote("node-a")).To(Equal(`"node-a"`))
	})

	It("truncates long text and elides the rest, counting code points", func() {
		quoted := diag.Quote(strings.Repeat("é", 60))
		Expect(quoted).To(HaveSuffix("…"))
		Expect([]rune(quoted)).To(HaveLen(diag.QuoteLimit + 3))
	})
})

var _ = Describe("Enumerating a closed set", func() {
	It("lists a set of eight or fewer in full", func() {
		Expect(diag.Enumerate([]string{"forward", "reverse"}, "sideways")).To(Equal("`forward` or `reverse`"))
		Expect(diag.Enumerate([]string{"click"}, "hover")).To(Equal("`click`"))
		Expect(diag.Enumerate([]string{"surface", "ink", "muted", "rule", "accent", "positive", "warning"}, "brand")).
			To(Equal("`surface`, `ink`, `muted`, `rule`, `accent`, `positive` or `warning`"))
	})

	It("lists the three nearest of a larger set, then the count of the rest", func() {
		blocks := []string{"state", "predicates", "bindings", "labels", "chrome", "roles", "channels", "placements", "scene", "motion"}
		// Ties break alphabetically, so the same set always renders the same way.
		Expect(diag.Enumerate(blocks, "stat")).To(Equal("`state`, `scene` or `motion`, and 7 more"))
	})

	It("says so when nothing of the kind is declared", func() {
		Expect(diag.Enumerate(nil, "anything")).To(Equal("none are declared"))
	})

	It("renders an ordered list in its own order", func() {
		Expect(diag.Sequence([]string{"widget", "dialect", "region", "palette"})).
			To(Equal("`widget`, `dialect`, `region`, `palette`"))
	})
})

var _ = DescribeTable("Edit distance",
	func(first string, second string, expected int) {
		Expect(diag.EditDistance(first, second)).To(Equal(expected))
		Expect(diag.EditDistance(second, first)).To(Equal(expected))
	},
	Entry("identical", "scene", "scene", 0),
	Entry("one substitution", "scene", "scone", 1),
	Entry("one deletion", "state", "stat", 1),
	Entry("empty against a word", "", "state", 5),
	Entry("code points rather than bytes", "é", "e", 1),
)

var _ = Describe("Spans", func() {
	It("opens at the position a finding about the construct anchors at", func() {
		span := diag.SourceSpan{File: "doc.widget", StartLine: 4, StartColumn: 3, EndLine: 9, EndColumn: 6}
		Expect(span.Start()).To(Equal(at(4, 3)))
		Expect(span.Start().String()).To(Equal("doc.widget:4:3"))
	})
})
