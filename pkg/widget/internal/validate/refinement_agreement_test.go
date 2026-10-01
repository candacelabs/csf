package validate_test

import (
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
	"github.com/candacelabs/csf/pkg/widget/internal/validate"
	widgetrefinementv1 "github.com/candacelabs/csf/pkg/widget/refinement/v1"
)

// The proof that the two layers of docs/ontology.md's local invariants agree.
//
// dialect.md § 10.3 keeps the IR hand-written and the W-class validator the
// author-facing diagnostic; candace/pkg/widget/refinement/v1 adds a Liquid Proto
// refinement contract that PROVES each locally-expressible invariant holds. The
// two are only a layering — rather than a second, weaker validator — if they
// reject exactly the same values. This is where that is proven: for every
// invariant that has both a refinement and a W-class, a value the refinement
// rejects is a value the validator rejects with its class, and a value one
// accepts the other accepts.
//
// This is the "production boundary that consumes generated code" go/CLAUDE.md
// names: it does not unit-test the generated Validate* in isolation (that would
// be testing checked-in generated code), it asserts the boundary property that
// the refinement contract and the validator are one specification written twice.
// ontology.md's `Enforced by:` link on each of these invariants cites both the
// refinement and this spec, and tools/tests/test_ontology_enforcement.py fails
// if either citation goes missing.
//
// The mapping from a document value to a refinement message is the honest part:
// the refinement operates on a scalar the interpreter would have put in the IR,
// so each case pulls exactly that scalar out and feeds it to Validate*. A
// closed-enum name that the grammar does not know maps to the enum's unspecified
// zero, which the refinement's `this >= 1 && this <= N` rejects — the same value
// the validator refuses as an unknown name.

// The closed-enum name tables map a dialect spelling to the refinement enum
// value. A spelling the dialect does not have maps to the unspecified zero,
// which is what a refused name resolves to and what the range refinement
// rejects.
var (
	tokenEnums = map[string]widgetrefinementv1.Token{
		"surface":  widgetrefinementv1.Token_TOKEN_SURFACE,
		"ink":      widgetrefinementv1.Token_TOKEN_INK,
		"muted":    widgetrefinementv1.Token_TOKEN_MUTED,
		"rule":     widgetrefinementv1.Token_TOKEN_RULE,
		"accent":   widgetrefinementv1.Token_TOKEN_ACCENT,
		"positive": widgetrefinementv1.Token_TOKEN_POSITIVE,
		"warning":  widgetrefinementv1.Token_TOKEN_WARNING,
	}
	markerEnums = map[string]widgetrefinementv1.Marker{
		"large": widgetrefinementv1.Marker_MARKER_LARGE,
		"small": widgetrefinementv1.Marker_MARKER_SMALL,
	}
	directionEnums = map[string]widgetrefinementv1.Direction{
		"forward": widgetrefinementv1.Direction_DIRECTION_FORWARD,
		"reverse": widgetrefinementv1.Direction_DIRECTION_REVERSE,
	}
	triggerEnums = map[string]widgetrefinementv1.Trigger{
		"click":  widgetrefinementv1.Trigger_TRIGGER_CLICK,
		"change": widgetrefinementv1.Trigger_TRIGGER_CHANGE,
		"input":  widgetrefinementv1.Trigger_TRIGGER_INPUT,
		"submit": widgetrefinementv1.Trigger_TRIGGER_SUBMIT,
	}
	fieldTypeEnums = map[string]widgetrefinementv1.FieldType{
		"flag":    widgetrefinementv1.FieldType_FIELD_TYPE_FLAG,
		"counter": widgetrefinementv1.FieldType_FIELD_TYPE_COUNTER,
		"count":   widgetrefinementv1.FieldType_FIELD_TYPE_COUNT,
		"text":    widgetrefinementv1.FieldType_FIELD_TYPE_TEXT,
	}
)

// hasClass reports whether validating one document produced one class. It is
// what "the validator rejects this value with class W" means, and it is
// deliberately membership rather than "only this class": a mutation may produce
// findings the mutation implies as well, and agreement is about the target
// class, not about it being alone.
func hasClass(source string, class diag.Class) bool {
	for _, finding := range findingsFor(source) {
		if finding.Class == class {
			return true
		}
	}
	return false
}

func atoi(text string) int {
	value, err := strconv.Atoi(text)
	Expect(err).ToNot(HaveOccurred(), "an agreement sample that is not an integer: %q", text)
	return value
}

// agreementCase is one invariant that both layers hold. refineRejects runs the
// generated contract on the sample; docRejects builds a document carrying the
// same sample and asks whether the validator reported the class. valid and
// invalid are the samples the two must agree on.
type agreementCase struct {
	name          string
	class         diag.Class
	refineRejects func(sample string) bool
	docRejects    func(sample string) bool
	valid         []string
	invalid       []string
}

// mutatedHasClass is the common docRejects shape: replace one span of
// fullDocument with the sample spliced in, and report whether the class fired.
func mutatedHasClass(from string, to func(sample string) string, class diag.Class) func(sample string) bool {
	return func(sample string) bool {
		return hasClass(mutate(fullDocument, edit{from, to(sample)}), class)
	}
}

var agreementCases = []agreementCase{
	{
		name:  "region identity — Widget invariant, W108, RegionIdentity.value",
		class: diag.ClassRegionIdentityMalformed,
		refineRejects: func(sample string) bool {
			return widgetrefinementv1.ValidateRegionIdentity(&widgetrefinementv1.RegionIdentity{Value: sample}) != nil
		},
		docRejects: mutatedHasClass(`region "widget.full"`, func(sample string) string {
			return `region "` + sample + `"`
		}, diag.ClassRegionIdentityMalformed),
		valid:   []string{"widget.full", "A", "a-b_c:d.e", "widgetX"},
		invalid: []string{"widget full", "", "a/b", "café"},
	},
	{
		name:  "placement left — Placement invariant, W104, Placement.left",
		class: diag.ClassIntegerOutOfRange,
		refineRejects: func(sample string) bool {
			return widgetrefinementv1.ValidatePlacement(&widgetrefinementv1.Placement{Left: int32(atoi(sample)), Top: 50}) != nil
		},
		docRejects: mutatedHasClass("placement west left 20 top 50", func(sample string) string {
			return "placement west left " + sample + " top 50"
		}, diag.ClassIntegerOutOfRange),
		valid:   []string{"0", "100", "50"},
		invalid: []string{"101", "200"},
	},
	{
		name:  "placement top — Placement invariant, W104, Placement.top",
		class: diag.ClassIntegerOutOfRange,
		refineRejects: func(sample string) bool {
			return widgetrefinementv1.ValidatePlacement(&widgetrefinementv1.Placement{Left: 20, Top: int32(atoi(sample))}) != nil
		},
		docRejects: mutatedHasClass("placement west left 20 top 50", func(sample string) string {
			return "placement west left 20 top " + sample
		}, diag.ClassIntegerOutOfRange),
		valid:   []string{"0", "100", "50"},
		invalid: []string{"101", "200"},
	},
	{
		name:  "pulse duration — Pulse invariant, W104, PulseTiming.duration_milliseconds",
		class: diag.ClassIntegerOutOfRange,
		refineRejects: func(sample string) bool {
			return widgetrefinementv1.ValidatePulseTiming(&widgetrefinementv1.PulseTiming{DurationMilliseconds: int32(atoi(sample))}) != nil
		},
		docRejects: mutatedHasClass("duration 800 milliseconds", func(sample string) string {
			return "duration " + sample + " milliseconds"
		}, diag.ClassIntegerOutOfRange),
		valid:   []string{"1", "800"},
		invalid: []string{"0"},
	},
	{
		name:  "emphasis duration — Emphasis invariant, W104, EmphasisTiming.duration_milliseconds",
		class: diag.ClassIntegerOutOfRange,
		refineRejects: func(sample string) bool {
			return widgetrefinementv1.ValidateEmphasisTiming(&widgetrefinementv1.EmphasisTiming{DurationMilliseconds: int32(atoi(sample))}) != nil
		},
		docRejects: mutatedHasClass("duration 700 milliseconds", func(sample string) string {
			return "duration " + sample + " milliseconds"
		}, diag.ClassIntegerOutOfRange),
		valid:   []string{"1", "700"},
		invalid: []string{"0"},
	},
	{
		name:  "wire name — EventDeclaration/Control invariant, W109, WireName.value",
		class: diag.ClassWireNameMalformed,
		refineRejects: func(sample string) bool {
			return widgetrefinementv1.ValidateWireName(&widgetrefinementv1.WireName{Value: sample}) != nil
		},
		docRejects: mutatedHasClass(`wire "widget.full.toggle"`, func(sample string) string {
			return `wire "` + sample + `"`
		}, diag.ClassWireNameMalformed),
		valid:   []string{"widget.full.toggle", "a", "x.y"},
		invalid: []string{"", "a:b", "a;b"},
	},
	{
		name:  "event field wire name — EventField invariant, W101, EventFieldWireName.value",
		class: diag.ClassIdentifierCase,
		refineRejects: func(sample string) bool {
			return widgetrefinementv1.ValidateEventFieldWireName(&widgetrefinementv1.EventFieldWireName{Value: sample}) != nil
		},
		docRejects: mutatedHasClass("field voters writes voters", func(sample string) string {
			return "field " + sample + " writes voters"
		}, diag.ClassIdentifierCase),
		valid:   []string{"voters", "a_b", "ping1"},
		invalid: []string{"Voters", "a__b", "a_"},
	},
	{
		name:  "identifier case — StateField invariant, W101, Identifier.value",
		class: diag.ClassIdentifierCase,
		refineRejects: func(sample string) bool {
			return widgetrefinementv1.ValidateIdentifier(&widgetrefinementv1.Identifier{Value: sample}) != nil
		},
		docRejects: mutatedHasClass("field voters type count", func(sample string) string {
			return "field " + sample + " type count"
		}, diag.ClassIdentifierCase),
		valid:   []string{"voters", "aB", "x1"},
		invalid: []string{"Voters", "_x", "a_b"},
	},
	{
		name:  "token name — Token invariant, W204, TokenValue.value",
		class: diag.ClassTokenNameUnknown,
		refineRejects: func(sample string) bool {
			return widgetrefinementv1.ValidateTokenValue(&widgetrefinementv1.TokenValue{Value: tokenEnums[sample]}) != nil
		},
		docRejects: mutatedHasClass("token accent", func(sample string) string {
			return "token " + sample
		}, diag.ClassTokenNameUnknown),
		valid:   []string{"surface", "ink", "accent", "warning"},
		invalid: []string{"brand", "midnight"},
	},
	{
		name:  "marker — Role invariant, W105, MarkerValue.value",
		class: diag.ClassValueNotEnumerated,
		refineRejects: func(sample string) bool {
			return widgetrefinementv1.ValidateMarkerValue(&widgetrefinementv1.MarkerValue{Value: markerEnums[sample]}) != nil
		},
		docRejects: mutatedHasClass("marker large", func(sample string) string {
			return "marker " + sample
		}, diag.ClassValueNotEnumerated),
		valid:   []string{"large", "small"},
		invalid: []string{"huge", "big"},
	},
	{
		name:  "channel direction — Channel invariant, W105, DirectionValue.value",
		class: diag.ClassValueNotEnumerated,
		refineRejects: func(sample string) bool {
			return widgetrefinementv1.ValidateDirectionValue(&widgetrefinementv1.DirectionValue{Value: directionEnums[sample]}) != nil
		},
		docRejects: mutatedHasClass("direction forward", func(sample string) string {
			return "direction " + sample
		}, diag.ClassValueNotEnumerated),
		valid:   []string{"forward", "reverse"},
		invalid: []string{"sideways", "both"},
	},
	{
		name:  "control trigger — Control cardinality, W105, TriggerValue.value",
		class: diag.ClassValueNotEnumerated,
		refineRejects: func(sample string) bool {
			return widgetrefinementv1.ValidateTriggerValue(&widgetrefinementv1.TriggerValue{Value: triggerEnums[sample]}) != nil
		},
		docRejects: mutatedHasClass("trigger click", func(sample string) string {
			return "trigger " + sample
		}, diag.ClassValueNotEnumerated),
		valid:   []string{"click", "change", "input", "submit"},
		invalid: []string{"hover", "hold"},
	},
	{
		name:  "state field type — StateField declared type, W105, FieldTypeValue.value",
		class: diag.ClassValueNotEnumerated,
		refineRejects: func(sample string) bool {
			return widgetrefinementv1.ValidateFieldTypeValue(&widgetrefinementv1.FieldTypeValue{Value: fieldTypeEnums[sample]}) != nil
		},
		docRejects: mutatedHasClass("field voters type count", func(sample string) string {
			return "field voters type " + sample
		}, diag.ClassValueNotEnumerated),
		valid:   []string{"flag", "counter", "count", "text"},
		invalid: []string{"number", "bool"},
	},
}

var _ = Describe("The refinement contract and the validator agree on every local invariant", func() {
	for _, testCase := range agreementCases {
		testCase := testCase
		Describe(testCase.name, func() {
			It("accepts every value both layers call valid", func() {
				for _, sample := range testCase.valid {
					Expect(testCase.refineRejects(sample)).To(BeFalse(),
						"the refinement rejects %q, which the validator accepts", sample)
					Expect(testCase.docRejects(sample)).To(BeFalse(),
						"the validator reports %s for %q, which the refinement accepts", testCase.class, sample)
				}
			})
			It("rejects every value both layers call invalid, with the same class", func() {
				for _, sample := range testCase.invalid {
					Expect(testCase.refineRejects(sample)).To(BeTrue(),
						"the refinement accepts %q, which the validator rejects with %s", sample, testCase.class)
					Expect(testCase.docRejects(sample)).To(BeTrue(),
						"the validator does not report %s for %q, which the refinement rejects", testCase.class, sample)
				}
			})
		})
	}
})

var _ = Describe("The invariants enforced by IR construction rather than by a class", func() {
	// A few ontology invariants are held not by a refusal but by the shape the
	// interpreter builds. They are provable all the same: the built IR carries the
	// property for a document that could otherwise violate it. ontology.md cites
	// this spec for them.
	It("compiles the host connection status into every motion gate (Motion invariant)", func() {
		// The author of fullDocument never writes the host-status gate; the
		// interpreter puts it there. A widget cannot animate against a dead
		// connection because HostStatusGate is true by construction, not by the
		// author remembering to ask for it.
		document, findings := validate.Document("fixture.widget", []byte(fullDocument))
		Expect(findings).To(BeEmpty())
		Expect(document.Motion).ToNot(BeNil())
		Expect(document.Motion.HostStatusGate).To(BeTrue(),
			"the compiled motion gate must always carry the host connection status")
	})
})

var _ = Describe("The refinement floors the grammar cannot spell", func() {
	// duration and delay have a floor the refinement proves and the grammar
	// cannot violate: a negative millisecond value has no token the parser
	// accepts, so there is no document to disagree with. The contract still
	// carries the floor, which is what makes it a specification of the IR rather
	// than a restatement of the grammar.
	It("rejects a negative pulse delay the dialect has no spelling for", func() {
		Expect(widgetrefinementv1.ValidatePulseTiming(
			&widgetrefinementv1.PulseTiming{DurationMilliseconds: 1, DelayMilliseconds: -1})).To(HaveOccurred())
	})
	It("rejects a negative emphasis delay the dialect has no spelling for", func() {
		Expect(widgetrefinementv1.ValidateEmphasisTiming(
			&widgetrefinementv1.EmphasisTiming{DurationMilliseconds: 1, DelayMilliseconds: -1})).To(HaveOccurred())
	})
})
