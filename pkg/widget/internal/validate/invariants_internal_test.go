package validate

import (
	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
	"github.com/candacelabs/csf/pkg/widget/internal/ir"
)

// The completeness specs the sweep registry makes possible. None of them could
// be asked of the call list of sibling methods this pass used to be: nothing in
// the language enumerates a method set, so "is every invariant still wired in,
// and do two of them claim one error class?" had no answer a test could give,
// and the answer was whoever edited the call list last.
//
// These specs live in the package rather than beside the catalogue specs in
// validate_test because the registry is what they assert about, and exporting
// it so a spec can read it would make the seam an API instead of an assertion.

// sweepOwnedClasses is the whole-document group of docs/errors.md: the classes
// that cannot be decided until the document is whole, which is what the sweep
// is for. It is written out rather than derived from the registry — a spec that
// reads its expectation out of the thing under test asserts nothing.
//
// Six of the catalogue's 70 classes, and the partition is the point. The other
// 64 belong to the lexer, the parser, the preamble reader and the build pass,
// each of which decides its findings from one token, one statement or one
// block and never needs the rest of the document; the sweep does not own them
// and this list does not pretend otherwise. W303 appears on both sides of the
// partition: the build pass reports it for a block that is short of members,
// the sweep reports it for a scene whose edges have too few nodes to join. One
// class, two containers, and the registry row claims only what its own check
// reports.
var sweepOwnedClasses = []diag.Class{
	diag.ClassContainerUnderfilled,     // W303, for the scene
	diag.ClassPredicateCycle,           // W408
	diag.ClassStateFieldUnwritten,      // W409
	diag.ClassDeclarationUnreferenced,  // W410
	diag.ClassBindingClauseUnreachable, // W415
	diag.ClassDescriptionNotBound,      // W603
}

var _ = Describe("The whole-document invariant registry", func() {
	It("gives every class it claims exactly one owner", func() {
		owners := map[diag.Class][]string{}
		for _, registered := range wholeDocumentInvariants {
			for _, class := range registered.owns {
				owners[class] = append(owners[class], registered.name)
			}
		}
		for class, claiming := range owners {
			Expect(claiming).To(HaveLen(1), "class %s is claimed by %v, and one class has one owner", class, claiming)
		}
	})

	It("owns exactly the whole-document group of the error catalogue", func() {
		Expect(sweepClasses()).To(ConsistOf(sweepOwnedClasses))
	})

	It("registers every row with a name, a claim and a check", func() {
		names := map[string]bool{}
		for _, registered := range wholeDocumentInvariants {
			Expect(registered.name).ToNot(BeEmpty())
			Expect(names).ToNot(HaveKey(registered.name), "two rows are named %s", registered.name)
			names[registered.name] = true
			Expect(registered.owns).ToNot(BeEmpty(), "row %s claims no class, so nothing can check what it reports", registered.name)
			Expect(registered.check).ToNot(BeNil(), "row %s registers no check", registered.name)
		}
	})

	// The claim is metadata until something compares it against what the check
	// actually reports. This runs every row against one document built to trip
	// all six, and asserts both directions: no row reports a class it did not
	// claim, and no row's claim goes untested because the document never
	// reached it.
	It("reports only the classes the registering row claims", func() {
		view := documentTrippingEverySweepClass()
		reported := []diag.Class{}
		for _, registered := range wholeDocumentInvariants {
			findings := registered.check(view)
			Expect(findings).ToNot(BeEmpty(), "row %s reported nothing, so its claim is untested", registered.name)
			for _, finding := range findings {
				Expect(registered.owns).To(ContainElement(finding.Class),
					"row %s reported %s, which it does not claim", registered.name, finding.Class)
				reported = append(reported, finding.Class)
			}
		}
		Expect(reported).To(ConsistOf(sweepOwnedClasses))
	})

	It("gives every finding of the sweep a message and a fix", func() {
		view := documentTrippingEverySweepClass()
		for _, registered := range wholeDocumentInvariants {
			for _, finding := range registered.check(view) {
				Expect(finding.Message).ToNot(BeEmpty(), registered.name)
				Expect(finding.Fix).ToNot(BeEmpty(), registered.name)
				Expect(finding.At.Line).To(BeNumerically(">", 0), registered.name)
			}
		}
	})
})

// documentTrippingEverySweepClass is one hand-built view that trips all six
// registered invariants exactly once each. It is built here rather than parsed
// from a source document so that a row reporting nothing is a failure of the
// row, never of a fixture document that drifted away from what it was written
// to break.
func documentTrippingEverySweepClass() documentView {
	written := &ir.StateField{
		Name:    "paused",
		Type:    ir.FieldFlag,
		Writers: []ir.Writer{{Kind: ir.WriterEventToggle, Span: spanAt(31)}},
		Span:    spanAt(4),
	}
	unwritten := &ir.StateField{Name: "orphan", Type: ir.FieldCount, Span: spanAt(5)}

	paused := &ir.Predicate{Name: "paused", Kind: ir.PredicateAtomic, Field: written, Span: spanAt(4)}
	up := &ir.Predicate{Name: "up", Kind: ir.PredicateComposed, Span: spanAt(8)}
	down := &ir.Predicate{Name: "down", Kind: ir.PredicateComposed, Span: spanAt(11)}
	up.Requires = []*ir.Predicate{down}
	down.Requires = []*ir.Predicate{up}

	// Two clauses guarding on one atomic predicate: the second can never be the
	// first match.
	statusText := &ir.Binding{
		Name: "statusText",
		Clauses: []*ir.BindingClause{
			{Polarity: ir.GuardWhen, Predicate: paused, Span: spanAt(16)},
			{Polarity: ir.GuardWhen, Predicate: paused, Span: spanAt(17)},
		},
		Span: spanAt(15),
	}

	// A literal description, while the document declares predicates.
	description := &ir.Label{Name: "sceneDescriptionLabel", SourceKind: ir.LabelLiteral, Span: spanAt(21)}

	// One node and one edge: an edge needs two nodes to join.
	nodeA := &ir.Node{Name: "nodeA", Span: spanAt(26)}
	scene := &ir.Scene{
		Name:            "solo",
		DescriptionSlot: &ir.Slot{Kind: ir.SlotDescription, Label: description, Span: spanAt(25)},
		Nodes:           []*ir.Node{nodeA},
		Edges:           []*ir.Edge{{Name: "link", From: nodeA, To: nodeA, Span: spanAt(29)}},
		Span:            spanAt(24),
	}

	document := &ir.Document{
		Name:           "SweepFixture",
		DialectVersion: DialectVersion,
		StateFields:    []*ir.StateField{written, unwritten},
		Predicates:     []*ir.Predicate{paused, up, down},
		Bindings:       []*ir.Binding{statusText},
		Labels:         []*ir.Label{description},
		Scene:          scene,
		Span:           spanAt(1),
	}

	return documentView{
		document: document,
		declarations: []declaredSymbol{
			// Declared, built, and named by nothing.
			{name: "peer", kind: kindRole, lineAt: positionAt(19), resolved: true, referenced: false},
			// Named by a label, so the sweep leaves it alone.
			{name: "statusText", kind: kindBinding, lineAt: positionAt(15), resolved: true, referenced: true},
			// Never built, so whatever went wrong with it is already reported.
			{name: "halfBuilt", kind: kindChannel, lineAt: positionAt(20), resolved: false, referenced: false},
		},
	}
}

func spanAt(line int) diag.SourceSpan {
	return diag.SourceSpan{File: "fixture.widget", StartLine: line, StartColumn: 3}
}

func positionAt(line int) diag.SourcePosition {
	return diag.SourcePosition{File: "fixture.widget", Line: line, Column: 3}
}
