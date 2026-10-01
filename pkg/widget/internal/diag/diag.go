// Package diag carries the widget validator's location-anchored findings and
// the identifiers of the error catalogue every finding belongs to.
//
// A finding is data, never a Go error: the validator runs both its passes to
// completion and reports every finding, so the caller receives a sorted slice
// rather than the first failure. The rendering rules — the message template,
// the truncation of quoted author text, the enumeration of closed sets — live
// here because the validator's output is a publication surface, and one
// implementation of those rules is the only way they stay true.
//
// The catalogue itself is candace/pkg/widget/docs/errors.md. Every class
// constant below names its identifier there.
package diag

import (
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"
)

// Class identifies one error class of the catalogue. Its string value is the
// identifier printed in a message and cited in docs/errors.md.
type Class string

// The document and block structure classes.
const (
	ClassPreambleFirstStatement    Class = "W001"
	ClassPreambleDirectiveMissing  Class = "W002"
	ClassDialectVersionUnsupported Class = "W003"
	ClassBlockNameUnknown          Class = "W004"
	ClassBlockOutOfOrder           Class = "W005"
	ClassBlockDuplicated           Class = "W006"
	ClassBlockEmpty                Class = "W007"
	ClassBlockUnclosed             Class = "W008"
	ClassEndWithoutBlock           Class = "W009"
	ClassStatementAtDocumentScope  Class = "W010"
	ClassRequiredBlockMissing      Class = "W011"
	ClassBlockNestedTooDeep        Class = "W012"
	ClassStatementKeywordUnknown   Class = "W013"
)

// The lexical and literal classes.
const (
	ClassIdentifierCase           Class = "W101"
	ClassStringUnterminated       Class = "W102"
	ClassInterpolationMalformed   Class = "W103"
	ClassIntegerOutOfRange        Class = "W104"
	ClassValueNotEnumerated       Class = "W105"
	ClassClauseMissing            Class = "W106"
	ClassClauseDuplicated         Class = "W107"
	ClassRegionIdentityMalformed  Class = "W108"
	ClassWireNameMalformed        Class = "W109"
	ClassClauseArgumentsMalformed Class = "W110"
)

// The reference classes.
const (
	ClassIdentifierUndeclared       Class = "W201"
	ClassForwardReference           Class = "W202"
	ClassIdentifierWrongKind        Class = "W203"
	ClassTokenNameUnknown           Class = "W204"
	ClassSignalUnknown              Class = "W205"
	ClassInterpolationNotStateField Class = "W206"
	ClassInterpolationOfFlag        Class = "W207"
	ClassPaletteUnknown             Class = "W208"
)

// The cardinality and duplication classes.
const (
	ClassIdentifierDuplicated      Class = "W301"
	ClassSingularStatementRepeated Class = "W302"
	ClassContainerUnderfilled      Class = "W303"
	ClassEdgePairDuplicated        Class = "W304"
	ClassPulseDuplicated           Class = "W305"
	ClassPlacementShared           Class = "W306"
	ClassEmphasisDuplicated        Class = "W307"
)

// The semantic invariant classes.
const (
	ClassBindingNotTotal          Class = "W401"
	ClassLabelSourceCount         Class = "W402"
	ClassEdgeSelfLoop             Class = "W403"
	ClassPulseChannelNotCarried   Class = "W404"
	ClassMotionTickMissing        Class = "W405"
	ClassTickNotCounter           Class = "W406"
	ClassEmphasisForbiddenByRole  Class = "W407"
	ClassPredicateCycle           Class = "W408"
	ClassStateFieldUnwritten      Class = "W409"
	ClassDeclarationUnreferenced  Class = "W410"
	ClassControlEventUndeclared   Class = "W411"
	ClassEventFieldTypeMismatch   Class = "W412"
	ClassRuntimeMintedEvent       Class = "W413"
	ClassTickWithoutAnimation     Class = "W414"
	ClassBindingClauseUnreachable Class = "W415"
	ClassBoundFieldNotNumeric     Class = "W416"
	ClassToggleFieldNotFlag       Class = "W417"
	ClassOrderingFieldNotCounter  Class = "W418"
	ClassSignalFieldNotFlag       Class = "W419"
)

// The canonical-form classes: the four Anka rules, enforced.
const (
	ClassMermaidEdgeOperator       Class = "W501"
	ClassMermaidShapeBracket       Class = "W502"
	ClassMermaidKeyword            Class = "W503"
	ClassMermaidInitDirective      Class = "W504"
	ClassColourLiteral             Class = "W505"
	ClassTimeUnitSigil             Class = "W506"
	ClassLiteralWhereLabelExpected Class = "W507"
	ClassTrailingComment           Class = "W508"
)

// The accessibility and policy classes.
const (
	ClassTitleSlotMissing         Class = "W601"
	ClassDescriptionSlotMissing   Class = "W602"
	ClassDescriptionNotBound      Class = "W603"
	ClassIndicatorLabelEmpty      Class = "W604"
	ClassLiteralCarriesIdentifier Class = "W605"
)

// SourcePosition is one anchor in a document. Columns are 1-based and counted
// in Unicode code points, per anchoring rule A7. File is the path the author
// gave, unmodified: no absolute path is ever constructed (message rule M5).
type SourcePosition struct {
	File   string
	Line   int
	Column int
}

// String renders the anchor as `document:line:column`.
func (position SourcePosition) String() string {
	return fmt.Sprintf("%s:%d:%d", position.File, position.Line, position.Column)
}

// SourceSpan is the extent of one construct, carried by every IR record so a
// generator can trace emitted output back to the statement that produced it.
type SourceSpan struct {
	File        string
	StartLine   int
	StartColumn int
	EndLine     int
	EndColumn   int
}

// Start is the span's opening anchor, which is where a finding about the
// construct is reported.
func (span SourceSpan) Start() SourcePosition {
	return SourcePosition{File: span.File, Line: span.StartLine, Column: span.StartColumn}
}

// Related is a secondary anchor: the declaration a reference error points back
// at (A2), or the first occurrence a duplicate is compared against (A4).
type Related struct {
	At   SourcePosition
	Note string
}

// Finding is one location-anchored report. Message states what is wrong in the
// present indicative and names its subject; Fix is one imperative sentence
// naming the exact spelling to write.
type Finding struct {
	Class   Class
	At      SourcePosition
	Message string
	Fix     string
	Related []Related
}

// String renders the finding in the catalogue's message template.
func (finding Finding) String() string {
	builder := &strings.Builder{}
	fmt.Fprintf(builder, "%s: %s: %s\n", finding.At, finding.Class, finding.Message)
	for _, related := range finding.Related {
		fmt.Fprintf(builder, "    %s: %s\n", related.At, related.Note)
	}
	fmt.Fprintf(builder, "    fix: %s\n", finding.Fix)
	return builder.String()
}

// New builds a finding anchored at one position. It closes the message and the
// fix with a full stop when the caller has not, because the template in
// docs/errors.md ends both with one and a rule every call site has to remember
// is a rule that holds until somebody forgets.
func New(class Class, at SourcePosition, message string, fix string) Finding {
	return Finding{Class: class, At: at, Message: sentence(message), Fix: sentence(fix)}
}

func sentence(text string) string {
	if text == "" || strings.HasSuffix(text, ".") {
		return text
	}
	return text + "."
}

// WithRelated returns a copy of the finding carrying one more secondary anchor.
func (finding Finding) WithRelated(at SourcePosition, note string) Finding {
	related := make([]Related, 0, len(finding.Related)+1)
	related = append(related, finding.Related...)
	related = append(related, Related{At: at, Note: note})
	finding.Related = related
	return finding
}

// Sort orders findings by (line, column, class) so two runs over one document
// print byte-identical output.
func Sort(findings []Finding) {
	sort.SliceStable(findings, func(left int, right int) bool {
		first, second := findings[left], findings[right]
		switch {
		case first.At.Line != second.At.Line:
			return first.At.Line < second.At.Line
		case first.At.Column != second.At.Column:
			return first.At.Column < second.At.Column
		default:
			return first.Class < second.Class
		}
	})
}

// QuoteLimit is the longest author-written literal a message may echo, per
// message rule M6. Longer text is truncated and elided.
const QuoteLimit = 40

// Quote renders author-written text as a double-quoted literal, truncated to
// QuoteLimit code points with an ellipsis. A message never grows without bound
// with text the author controls.
func Quote(text string) string {
	if utf8.RuneCountInString(text) <= QuoteLimit {
		return fmt.Sprintf("%q", text)
	}
	runes := []rune(text)
	return fmt.Sprintf("%q…", string(runes[:QuoteLimit]))
}

// Sequence renders an ordered list in its own order, for a message about a
// sequence rather than about a choice. Enumerate is for a closed set the author
// picks from; this is for one they have to write in order.
func Sequence(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "`"+value+"`")
	}
	return strings.Join(quoted, ", ")
}

// EnumerationLimit is the largest closed set a message lists in full, per
// message rule M4.
const EnumerationLimit = 8

// Enumerate renders a closed set for a message: in full when it has
// EnumerationLimit members or fewer, otherwise the three nearest to the value
// the author wrote followed by the count of the rest.
func Enumerate(candidates []string, written string) string {
	if len(candidates) == 0 {
		return "none are declared"
	}
	if len(candidates) <= EnumerationLimit {
		return joinBackquoted(candidates)
	}
	nearest := nearestThree(candidates, written)
	return fmt.Sprintf("%s, and %d more", joinBackquoted(nearest), len(candidates)-len(nearest))
}

func joinBackquoted(values []string) string {
	quoted := make([]string, 0, len(values))
	for _, value := range values {
		quoted = append(quoted, "`"+value+"`")
	}
	switch len(quoted) {
	case 1:
		return quoted[0]
	case 2:
		return quoted[0] + " or " + quoted[1]
	default:
		return strings.Join(quoted[:len(quoted)-1], ", ") + " or " + quoted[len(quoted)-1]
	}
}

func nearestThree(candidates []string, written string) []string {
	ranked := make([]string, len(candidates))
	copy(ranked, candidates)
	sort.SliceStable(ranked, func(left int, right int) bool {
		leftDistance := EditDistance(ranked[left], written)
		rightDistance := EditDistance(ranked[right], written)
		if leftDistance != rightDistance {
			return leftDistance < rightDistance
		}
		return ranked[left] < ranked[right]
	})
	return ranked[:3]
}

// EditDistance is the Levenshtein distance between two strings, counted in
// Unicode code points. It ranks the members of a closed set by how near they
// are to what the author wrote.
func EditDistance(first string, second string) int {
	firstRunes, secondRunes := []rune(first), []rune(second)
	previous := make([]int, len(secondRunes)+1)
	current := make([]int, len(secondRunes)+1)
	for index := range previous {
		previous[index] = index
	}
	for firstIndex := 1; firstIndex <= len(firstRunes); firstIndex++ {
		current[0] = firstIndex
		for secondIndex := 1; secondIndex <= len(secondRunes); secondIndex++ {
			substitution := previous[secondIndex-1]
			if firstRunes[firstIndex-1] != secondRunes[secondIndex-1] {
				substitution++
			}
			current[secondIndex] = minimum(previous[secondIndex]+1, current[secondIndex-1]+1, substitution)
		}
		previous, current = current, previous
	}
	return previous[len(secondRunes)]
}

func minimum(first int, second int, third int) int {
	smallest := first
	if second < smallest {
		smallest = second
	}
	if third < smallest {
		smallest = third
	}
	return smallest
}
