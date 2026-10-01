package validate_test

import (
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
	"github.com/candacelabs/csf/pkg/widget/internal/validate"
)

var _ = Describe("The validator's base documents", func() {
	It("reports nothing about the minimal document", func() {
		Expect(classesOf(findingsFor(minimalDocument))).To(BeEmpty())
	})

	It("reports nothing about the document that uses all fourteen blocks", func() {
		Expect(classesOf(findingsFor(fullDocument))).To(BeEmpty())
	})
})

var _ = Describe("The cycle search on a deep, acyclic predicate graph", func() {
	// The P2 audit's H2 finding: the search enumerated every simple path, so a
	// graph where each composition names two others cost 2^depth. At 27 levels
	// — 54 compositions in a document of about 340 lines — that was 18 seconds
	// of wall clock on a document with no cycle in it at all, and four more
	// levels was sixteen times that.
	//
	// The budget is a second rather than a tight bound because this asserts a
	// complexity class, not a speed: the memoised search does a few thousand
	// map operations here and any machine that can run the suite at all has
	// orders of magnitude of headroom. A regression to the exponential walk
	// misses a one-second budget by a factor of twenty, so nothing about the
	// number needs to be tuned for it to keep catching that.
	It("finishes well inside a second at the depth that used to take eighteen", func() {
		source := deepPredicateDocument(27)
		started := time.Now()
		findings := findingsFor(source)
		Expect(time.Since(started)).To(BeNumerically("<", time.Second))
		Expect(classesOf(findings)).ToNot(ContainElement(string(diag.ClassPredicateCycle)),
			"the graph is a DAG, so the search must find no cycle in it")
	})

	// The memo that bought the line above is the reason this spec exists. The
	// obvious way to make the search linear — settling every predicate the
	// moment it is finished — reports one cycle per predicate instead of one
	// per cycle, which is a change to what W408 says rather than to how long it
	// takes to say it. Only a predicate that reached no cycle at all is
	// settled, so two cycles closing on one predicate are still two findings.
	It("still reports both cycles when two of them close on one predicate", func() {
		source := mutate(fullDocument, edit{"predicates\n", "predicates\n" +
			"  predicate alpha\n    requires beta\n    requires gamma\n  end\n\n" +
			"  predicate beta\n    requires alpha\n  end\n\n" +
			"  predicate gamma\n    requires alpha\n  end\n\n"})
		messages := []string{}
		for _, finding := range findingsFor(source) {
			if finding.Class == diag.ClassPredicateCycle {
				messages = append(messages, finding.Message)
			}
		}
		Expect(messages).To(HaveLen(2))
		Expect(messages[0]).To(ContainSubstring("alpha → beta → alpha"))
		Expect(messages[1]).To(ContainSubstring("alpha → gamma → alpha"))
	})
})

// One entry per class of docs/errors.md. Each edit is the smallest mistake that
// produces its class; several also produce the findings that mistake implies,
// which is why the assertion is that the class is among them rather than alone.
var _ = DescribeTable("The error catalogue",
	func(base string, expected diag.Class, edits ...edit) {
		findings := findingsFor(mutate(base, edits...))
		Expect(classesOf(findings)).To(ContainElement(string(expected)))
	},

	// W0 — document and block structure.
	Entry("W001 the document does not open with `widget`", fullDocument, diag.ClassPreambleFirstStatement,
		edit{"widget FullWidget\ndialect 0", "dialect 0\nwidget FullWidget"}),
	Entry("W002 a preamble directive is absent", fullDocument, diag.ClassPreambleDirectiveMissing,
		edit{"palette fieldStation\n", ""}),
	Entry("W003 the dialect version is not implemented", fullDocument, diag.ClassDialectVersionUnsupported,
		edit{"dialect 0", "dialect 1"}),
	Entry("W004 a block name is not one of the fourteen", fullDocument, diag.ClassBlockNameUnknown,
		edit{"state\n  field sequence", "stateful\n\nstate\n  field sequence"}),
	Entry("W005 blocks are out of canonical order", fullDocument, diag.ClassBlockOutOfOrder,
		edit{"data\n  stream watch", "state\n  field spare type flag\nend\n\ndata\n  stream watch"}),
	Entry("W006 a block appears twice", fullDocument, diag.ClassBlockDuplicated,
		edit{"data\n  stream watch", "motion\n  requires live\nend\n\ndata\n  stream watch"}),
	Entry("W007 a block contains no declarations", minimalDocument, diag.ClassBlockEmpty,
		edit{"events\n  event health", "motion\nend\n\nevents\n  event health"}),
	Entry("W008 a block is never closed", minimalDocument, diag.ClassBlockUnclosed,
		edit{"    delivers health\n  end\nend\n", "    delivers health\n  end\n"}),
	Entry("W009 an `end` closes nothing", minimalDocument, diag.ClassEndWithoutBlock,
		edit{"placements\n", "end\n\nplacements\n"}),
	Entry("W010 a statement appears at document scope", minimalDocument, diag.ClassStatementAtDocumentScope,
		edit{"placements\n", "field stray type flag\n\nplacements\n"}),
	Entry("W011 a required block is absent", minimalDocument, diag.ClassRequiredBlockMissing,
		edit{"chrome\n  title titleLabel\nend\n\n", ""}),
	Entry("W012 a declaration is nested too deep", fullDocument, diag.ClassBlockNestedTooDeep,
		edit{"  role peer\n    token accent", "  role peer\n    role inner\n      token ink\n    end\n    token accent"}),
	Entry("W013 a statement keyword is unknown inside a block", fullDocument, diag.ClassStatementKeywordUnknown,
		edit{"    token accent\n    marker large", "    token accent\n    colour ink\n    marker large"}),

	// W1 — lexical and literal.
	Entry("W101 an identifier breaks its case convention", fullDocument, diag.ClassIdentifierCase,
		edit{"widget FullWidget", "widget fullWidget"}),
	Entry("W102 a string literal is not closed", fullDocument, diag.ClassStringUnterminated,
		edit{`label titleLabel text "Full widget"`, `label titleLabel text "Full widget`}),
	Entry("W103 a template interpolation is unbalanced", fullDocument, diag.ClassInterpolationMalformed,
		edit{"{voters} voters", "{voters voters"}),
	Entry("W104 an integer is outside its range", fullDocument, diag.ClassIntegerOutOfRange,
		edit{"placement west left 20", "placement west left 200"}),
	Entry("W105 a value is outside a closed enumeration", fullDocument, diag.ClassValueNotEnumerated,
		edit{"marker large", "marker huge"}),
	Entry("W106 a declaration is missing a required clause", fullDocument, diag.ClassClauseMissing,
		edit{"    marker large\n", ""}),
	Entry("W107 a clause appears twice in one declaration", fullDocument, diag.ClassClauseDuplicated,
		edit{"    token accent\n    marker large", "    token accent\n    token ink\n    marker large"}),
	Entry("W108 the region identity is outside its pattern", fullDocument, diag.ClassRegionIdentityMalformed,
		edit{`region "widget.full"`, `region "widget full"`}),
	Entry("W109 a wire name carries a structural character", fullDocument, diag.ClassWireNameMalformed,
		edit{`wire "widget.full.toggle"`, `wire "widget;full;toggle"`}),
	Entry("W110 a clause's arguments do not match its form", fullDocument, diag.ClassClauseArgumentsMalformed,
		edit{"placement west left 20 top 50", "placement west left 20 top"}),

	// W2 — reference.
	Entry("W201 an identifier is not declared", fullDocument, diag.ClassIdentifierUndeclared,
		edit{"positiveWhen live", "positiveWhen reachable"}),
	Entry("W202 a reference points at a later declaration", fullDocument, diag.ClassForwardReference,
		edit{"  edge link\n    from nodeA\n    to nodeB\n    carries beat\n  end\n", ""},
		edit{"  node nodeA\n", "  edge link\n    from nodeA\n    to nodeB\n    carries beat\n  end\n\n  node nodeA\n"}),
	Entry("W203 an identifier is the wrong kind", fullDocument, diag.ClassIdentifierWrongKind,
		edit{"  title titleLabel", "  title beat"}),
	Entry("W204 a token name is not one of the seven", fullDocument, diag.ClassTokenNameUnknown,
		edit{"    token accent\n    marker large", "    token brand\n    marker large"}),
	Entry("W205 a signal name is not a runtime signal", fullDocument, diag.ClassSignalUnknown,
		edit{"signal slowClient", "signal slowNetwork"}),
	Entry("W206 a template interpolates a name that is not a state field", fullDocument, diag.ClassInterpolationNotStateField,
		edit{"{voters} voters", "{quorum} voters"}),
	Entry("W207 a template interpolates a flag", fullDocument, diag.ClassInterpolationOfFlag,
		edit{"{voters} voters", "{connected} voters"}),
	Entry("W208 the palette directive names a palette that does not exist", fullDocument, diag.ClassPaletteUnknown,
		edit{"palette fieldStation", "palette midnightNeon"}),

	// W3 — cardinality and duplication.
	Entry("W301 an identifier is declared twice", fullDocument, diag.ClassIdentifierDuplicated,
		edit{"  field paused type flag", "  field paused type flag\n  field connected type flag"}),
	Entry("W302 a singular statement of a block is repeated", fullDocument, diag.ClassSingularStatementRepeated,
		edit{"  title titleLabel", "  title titleLabel\n  title sourceLabel"}),
	Entry("W303 a container has fewer members than it requires", fullDocument, diag.ClassContainerUnderfilled,
		edit{"    when paused then \"paused\"\n", ""}),
	Entry("W304 two edges join the same unordered pair", fullDocument, diag.ClassEdgePairDuplicated,
		edit{"    carries beat\n  end\nend", "    carries beat\n  end\n\n  edge second\n    from nodeB\n    to nodeA\n    carries beat\n  end\nend"}),
	Entry("W305 two pulses share an edge and a channel", fullDocument, diag.ClassPulseDuplicated,
		edit{"  emphasis nodeRing", "  pulse secondBeat\n    edge link\n    channel beat\n    duration 800 milliseconds\n  end\n\n  emphasis nodeRing"}),
	Entry("W306 two nodes name one placement", fullDocument, diag.ClassPlacementShared,
		edit{"    at east", "    at west"}),
	Entry("W307 two emphases name one node", fullDocument, diag.ClassEmphasisDuplicated,
		edit{"    delay 0 milliseconds\n  end\nend\n\nindicators", "    delay 0 milliseconds\n  end\n\n  emphasis secondRing\n    node nodeA\n    duration 700 milliseconds\n  end\nend\n\nindicators"}),

	// W4 — semantic invariants.
	Entry("W401 a binding has no `otherwise`", fullDocument, diag.ClassBindingNotTotal,
		edit{"    otherwise \"running\"\n", ""}),
	Entry("W402 a label carries two sources", fullDocument, diag.ClassLabelSourceCount,
		edit{`label titleLabel text "Full widget"`, `label titleLabel text "Full widget" binds statusText`}),
	Entry("W403 an edge joins a node to itself", fullDocument, diag.ClassEdgeSelfLoop,
		edit{"    from nodeA\n    to nodeB", "    from nodeA\n    to nodeA"}),
	Entry("W404 a pulse names a channel its edge does not carry", fullDocument, diag.ClassPulseChannelNotCarried,
		edit{"channels\n  channel beat", "channels\n  channel ghost\n    direction reverse\n    token positive\n    legend beatLegendLabel\n  end\n\n  channel beat"},
		edit{"    channel beat\n    duration 800", "    channel ghost\n    duration 800"}),
	Entry("W405 a motion block with animations has no `restartOn`", fullDocument, diag.ClassMotionTickMissing,
		edit{"  restartOn sequence\n", ""}),
	Entry("W406 `restartOn` does not name a counter", fullDocument, diag.ClassTickNotCounter,
		edit{"restartOn sequence", "restartOn connected"}),
	Entry("W407 an emphasis names a node whose role forbids it", fullDocument, diag.ClassEmphasisForbiddenByRole,
		edit{"emphasis allowed", "emphasis forbidden"}),
	Entry("W408 the predicate graph has a cycle", fullDocument, diag.ClassPredicateCycle,
		edit{"predicates\n", "predicates\n  predicate up\n    requires down\n  end\n\n  predicate down\n    requires up\n  end\n\n"}),
	Entry("W409 a state field has no writer", fullDocument, diag.ClassStateFieldUnwritten,
		edit{"  field voters type count", "  field voters type count\n  field orphan type count"}),
	Entry("W410 a declaration is referenced by nothing", fullDocument, diag.ClassDeclarationUnreferenced,
		edit{"  label beatLegendLabel", "  label spareLabel text \"spare\"\n  label beatLegendLabel"}),
	Entry("W411 a control emits an undeclared event", fullDocument, diag.ClassControlEventUndeclared,
		edit{"emits togglePause", "emits toggleMotion"}),
	Entry("W413 a runtime-minted event name is declared", fullDocument, diag.ClassRuntimeMintedEvent,
		edit{`wire "widget.full.toggle"`, `wire "timer:slow_client"`}),
	Entry("W414 `restartOn` is declared with no animation", fullDocument, diag.ClassTickWithoutAnimation,
		edit{"  pulse beatPulse\n    edge link\n    channel beat\n    duration 800 milliseconds\n    delay 0 milliseconds\n  end\n\n", ""},
		edit{"  emphasis nodeRing\n    node nodeA\n    duration 700 milliseconds\n    delay 0 milliseconds\n  end\n", ""}),
	Entry("W415 a binding clause can never be reached", fullDocument, diag.ClassBindingClauseUnreachable,
		edit{"    when paused then \"paused\"", "    when paused then \"paused\"\n    when paused then \"still paused\""}),
	Entry("W416 a numeric bound names a flag", fullDocument, diag.ClassBoundFieldNotNumeric,
		edit{"requires voters atLeast 1", "requires connected atLeast 1"}),
	Entry("W417 `toggles` names a field that is not a flag", fullDocument, diag.ClassToggleFieldNotFlag,
		edit{"toggles paused", "toggles voters"}),
	Entry("W418 `ordering` names a field that is not a counter", fullDocument, diag.ClassOrderingFieldNotCounter,
		edit{"ordering sequence", "ordering voters"}),
	Entry("W419 a `signal` binds a field that is not a flag", fullDocument, diag.ClassSignalFieldNotFlag,
		edit{"field degraded type flag signal slowClient", "field degraded type counter signal slowClient"}),

	// W5 — canonical form.
	Entry("W501 a mermaid edge operator appears", fullDocument, diag.ClassMermaidEdgeOperator,
		edit{"  edge link", "  nodeA --> nodeB\n\n  edge link"}),
	Entry("W502 a node shape bracket appears", fullDocument, diag.ClassMermaidShapeBracket,
		edit{"  edge link", "  nodeA[Round]\n\n  edge link"}),
	Entry("W502 a node shape bracket on a declaration's own name", fullDocument, diag.ClassMermaidShapeBracket,
		edit{"  node nodeB\n", "  node nodeB[Round]\n"}),
	Entry("W503 a dropped mermaid keyword appears", fullDocument, diag.ClassMermaidKeyword,
		edit{"scene pair\n", "scene pair\n  direction LR\n"}),
	Entry("W504 an init directive appears", fullDocument, diag.ClassMermaidInitDirective,
		edit{"widget FullWidget", "%%{init: {\"theme\": \"dark\"}}%%\nwidget FullWidget"}),
	Entry("W505 a colour value appears where a token belongs", fullDocument, diag.ClassColourLiteral,
		edit{"    token accent\n    marker large", "    token #7a8b5f\n    marker large"}),
	Entry("W506 a time value carries a unit sigil", fullDocument, diag.ClassTimeUnitSigil,
		edit{"duration 800 milliseconds", "duration 800ms"}),
	Entry("W507 a literal appears where a label reference belongs", fullDocument, diag.ClassLiteralWhereLabelExpected,
		edit{"  title titleLabel", `  title "Full widget"`}),
	Entry("W508 a comment follows content on one line", fullDocument, diag.ClassTrailingComment,
		edit{"    token accent\n    marker large", "    token accent %% the brand colour\n    marker large"}),

	// W6 — accessibility and policy.
	Entry("W601 the chrome block has no title", fullDocument, diag.ClassTitleSlotMissing,
		edit{"  title titleLabel\n", ""}),
	Entry("W602 the scene has no description", fullDocument, diag.ClassDescriptionSlotMissing,
		edit{"  description descriptionLabel\n", ""}),
	Entry("W603 a changing picture carries a fixed description", fullDocument, diag.ClassDescriptionNotBound,
		edit{"description descriptionLabel", "description titleLabel"}),
	Entry("W604 an indicator's label resolves to empty text", fullDocument, diag.ClassIndicatorLabelEmpty,
		edit{"  label beatLegendLabel", "  label emptyLabel text \"\"\n  label beatLegendLabel"},
		edit{"    label statusLabel\n    positiveWhen", "    label emptyLabel\n    positiveWhen"}),
	Entry("W605 a literal carries an identifier a document may not name", fullDocument, diag.ClassLiteralCarriesIdentifier,
		edit{`label nodeALabel text "node-a"`, `label nodeALabel text "node-a.example.com"`}),
)

var _ = Describe("W412, the one class dialect 0 cannot produce", func() {
	// The grammar has no spelling for an event field's type: `field <wire_name>
	// writes <stateField>` takes the state field's type, so the two cannot
	// disagree. The class stays in the catalogue for the version that adds typed
	// payload fields, and this spec is the record that it is unreachable now
	// rather than untested.
	It("is not produced by an event field of any state field type", func() {
		for _, fieldType := range []string{"counter", "count", "text", "flag"} {
			source := mutate(fullDocument,
				edit{"  field voters type count", "  field written type " + fieldType + "\n  field voters type count"},
				edit{"    field voters writes voters", "    field voters writes voters\n    field written writes written"})
			Expect(classesOf(findingsFor(source))).ToNot(ContainElement(string(diag.ClassEventFieldTypeMismatch)))
		}
	})
})

var _ = Describe("Anchoring", func() {
	It("anchors a whole-document invariant at the declaration it is about (A3)", func() {
		source := mutate(fullDocument, edit{"  field voters type count", "  field voters type count\n  field orphan type count"})
		finding := firstOfClass(findingsFor(source), diag.ClassStateFieldUnwritten)
		Expect(finding.At.Line).To(Equal(11))
		Expect(finding.At.Column).To(Equal(3))
	})

	It("anchors a duplicate at the second occurrence and points at the first (A4)", func() {
		source := mutate(fullDocument, edit{"    at east", "    at west"})
		finding := firstOfClass(findingsFor(source), diag.ClassPlacementShared)
		Expect(finding.Related).To(HaveLen(1))
		Expect(finding.Related[0].At.Line).To(BeNumerically("<", finding.At.Line))
	})

	It("anchors a reference at the use and points at the declaration (A2)", func() {
		source := mutate(fullDocument, edit{"  title titleLabel", "  title beat"})
		finding := firstOfClass(findingsFor(source), diag.ClassIdentifierWrongKind)
		Expect(finding.Related).To(HaveLen(1))
		Expect(finding.Related[0].Note).To(ContainSubstring("declared here as a channel"))
	})

	It("anchors a fault inside a template at the opening brace, counted inside the literal (A8)", func() {
		source := mutate(fullDocument, edit{"{voters} voters", "{connected} voters"})
		finding := firstOfClass(findingsFor(source), diag.ClassInterpolationOfFlag)
		line := strings.Split(source, "\n")[finding.At.Line-1]
		Expect(line[finding.At.Column-1 : finding.At.Column+10]).To(Equal("{connected}"))
	})

	It("counts columns in code points rather than bytes (A7)", func() {
		source := mutate(fullDocument, edit{"Two nodes, stopped.", "Two — nodes — stopped. {connected}"})
		finding := firstOfClass(findingsFor(source), diag.ClassInterpolationOfFlag)
		line := []rune(strings.Split(source, "\n")[finding.At.Line-1])
		Expect(string(line[finding.At.Column-1 : finding.At.Column+10])).To(Equal("{connected}"))
	})

	It("anchors a cycle at its lexically first member and lists it from there (A9)", func() {
		source := mutate(fullDocument,
			edit{"predicates\n", "predicates\n  predicate up\n    requires down\n  end\n\n  predicate down\n    requires up\n  end\n\n"})
		finding := firstOfClass(findingsFor(source), diag.ClassPredicateCycle)
		Expect(finding.Message).To(ContainSubstring("up → down → up"))
		Expect(finding.At.Line).To(Equal(15))
	})
})

var _ = Describe("Message obligations", func() {
	// Every message in the catalogue owes three things: a subject, a repair, and
	// silence about anything that is not the author's document.
	It("gives every finding a fix and a non-empty message", func() {
		source := mutate(fullDocument,
			edit{"marker large", "marker huge"},
			edit{"  title titleLabel\n", ""},
			edit{"restartOn sequence", "restartOn connected"})
		findings := findingsFor(source)
		Expect(findings).ToNot(BeEmpty())
		for _, finding := range findings {
			Expect(finding.Fix).ToNot(BeEmpty())
			Expect(finding.Message).ToNot(BeEmpty())
			Expect(finding.Message).ToNot(ContainSubstring("invalid"))
			Expect(finding.Message).ToNot(ContainSubstring("malformed"))
			Expect(finding.String()).To(ContainSubstring("fixture.widget:"))
		}
	})

	It("names the class of identifier a literal carries, never the identifier", func() {
		source := mutate(fullDocument, edit{`label nodeALabel text "node-a"`, `label nodeALabel text "node-a.example.com"`})
		finding := firstOfClass(findingsFor(source), diag.ClassLiteralCarriesIdentifier)
		Expect(finding.Message).To(ContainSubstring("a host name"))
		Expect(finding.Message + finding.Fix).ToNot(ContainSubstring("node-a.example.com"))
	})

	It("uses the document name the caller gave, and constructs no path of its own", func() {
		_, findings := validate.Document("a/relative/path.widget", []byte("widget Broken\n"))
		Expect(findings).ToNot(BeEmpty())
		for _, finding := range findings {
			Expect(finding.At.File).To(Equal("a/relative/path.widget"))
		}
	})

	It("reports every finding rather than the first, sorted and byte-identical between runs", func() {
		source := mutate(fullDocument,
			edit{"marker large", "marker huge"},
			edit{"placement west left 20", "placement west left 200"},
			edit{"trigger click", "trigger hover"})
		first, second := findingsFor(source), findingsFor(source)
		Expect(len(first)).To(BeNumerically(">=", 3))
		Expect(renderFindings(first)).To(Equal(renderFindings(second)))
		for index := 1; index < len(first); index++ {
			previous, current := first[index-1], first[index]
			Expect(previous.At.Line).To(BeNumerically("<=", current.At.Line))
			if previous.At.Line == current.At.Line {
				Expect(previous.At.Column).To(BeNumerically("<=", current.At.Column))
			}
		}
	})
})

func renderFindings(findings []diag.Finding) string {
	rendered := &strings.Builder{}
	for _, finding := range findings {
		rendered.WriteString(finding.String())
	}
	return rendered.String()
}
