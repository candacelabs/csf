package widget

import (
	"fmt"
	"os"

	"github.com/candacelabs/csf/pkg/widget/internal/diag"
	"github.com/candacelabs/csf/pkg/widget/internal/ir"
	"github.com/candacelabs/csf/pkg/widget/internal/validate"
)

// DialectVersion is the one dialect version this interpreter implements. A
// document declaring any other version is refused rather than guessed at: a
// parser that recovers by assuming what was meant generates a widget the author
// did not write.
const DialectVersion = validate.DialectVersion

// Interpret parses and validates one widget document and returns the resolved
// IR together with every finding, sorted by position. documentName is used
// verbatim in every finding's anchor — no absolute path is constructed and no
// working directory is printed, because a validator's output is pasted into
// issues, transcripts and pull requests.
//
// The returned document is always non-nil and always safe to inspect; it is
// sound only when no finding is returned.
func Interpret(documentName string, source []byte) (*Document, []Finding) {
	return validate.Document(documentName, source)
}

// InterpretFile reads a document from disk and interprets it. The error is
// returned only when the file could not be read at all, which is a failure of
// its own: an unrun check must never read as a pass.
func InterpretFile(path string) (*Document, []Finding, error) {
	source, readError := os.ReadFile(path)
	if readError != nil {
		return nil, nil, fmt.Errorf("read widget document %s: %w", path, readError)
	}
	document, findings := Interpret(path, source)
	return document, findings, nil
}

// The diagnostic surface. A finding is data rather than a Go error, because
// every one of them is reported and none of them stops the interpreter.
type (
	// Finding is one location-anchored report from the error catalogue.
	Finding = diag.Finding
	// Class identifies a finding's error class in docs/errors.md.
	Class = diag.Class
	// Related is a finding's secondary anchor: the declaration a reference
	// points at, or the first of two conflicting constructs.
	Related = diag.Related
	// SourcePosition is one anchor. Columns are 1-based, in code points.
	SourcePosition = diag.SourcePosition
	// SourceSpan is a construct's extent, carried by every IR record.
	SourceSpan = diag.SourceSpan
)

// The IR. Every type below is documented on the record itself.
type (
	Document         = ir.Document
	StateField       = ir.StateField
	Writer           = ir.Writer
	Predicate        = ir.Predicate
	NumericBound     = ir.NumericBound
	Binding          = ir.Binding
	BindingClause    = ir.BindingClause
	TextTemplate     = ir.TextTemplate
	TemplateSegment  = ir.TemplateSegment
	Label            = ir.Label
	Slot             = ir.Slot
	Role             = ir.Role
	Channel          = ir.Channel
	Placement        = ir.Placement
	Scene            = ir.Scene
	Orbit            = ir.Orbit
	Node             = ir.Node
	Edge             = ir.Edge
	EdgeGeometry     = ir.EdgeGeometry
	Motion           = ir.Motion
	Pulse            = ir.Pulse
	Emphasis         = ir.Emphasis
	Indicator        = ir.Indicator
	Control          = ir.Control
	EventDeclaration = ir.EventDeclaration
	EventField       = ir.EventField
	Stream           = ir.Stream
	Legend           = ir.Legend
	LegendEntry      = ir.LegendEntry
	DirtyProjection  = ir.DirtyProjection
)

// The IR's closed sets.
type (
	FieldType       = ir.FieldType
	Token           = ir.Token
	PredicateKind   = ir.PredicateKind
	Comparison      = ir.Comparison
	GuardPolarity   = ir.GuardPolarity
	LabelSourceKind = ir.LabelSourceKind
	SlotKind        = ir.SlotKind
	Marker          = ir.Marker
	Direction       = ir.Direction
	Trigger         = ir.Trigger
	WriterKind      = ir.WriterKind
)

// The four state field types.
const (
	FieldFlag    = ir.FieldFlag
	FieldCounter = ir.FieldCounter
	FieldCount   = ir.FieldCount
	FieldText    = ir.FieldText
)

// The seven semantic tokens. The namespace is closed: a widget writes a token
// name, never a value, so one document renders under any palette that maps the
// seven.
const (
	TokenSurface  = ir.TokenSurface
	TokenInk      = ir.TokenInk
	TokenMuted    = ir.TokenMuted
	TokenRule     = ir.TokenRule
	TokenAccent   = ir.TokenAccent
	TokenPositive = ir.TokenPositive
	TokenWarning  = ir.TokenWarning
)

// The four chrome slots, the two markers, the two channel directions, the two
// guard polarities, the two label sources, the two predicate forms, the two
// bound comparisons, the four triggers and the three writers.
const (
	SlotTitle         = ir.SlotTitle
	SlotSource        = ir.SlotSource
	SlotDescription   = ir.SlotDescription
	SlotStat          = ir.SlotStat
	MarkerLarge       = ir.MarkerLarge
	MarkerSmall       = ir.MarkerSmall
	DirectionForward  = ir.DirectionForward
	DirectionReverse  = ir.DirectionReverse
	GuardWhen         = ir.GuardWhen
	GuardWhenNot      = ir.GuardWhenNot
	LabelLiteral      = ir.LabelLiteral
	LabelBound        = ir.LabelBound
	PredicateAtomic   = ir.PredicateAtomic
	PredicateComposed = ir.PredicateComposed
	ComparisonAtLeast = ir.ComparisonAtLeast
	ComparisonAtMost  = ir.ComparisonAtMost
	TriggerClick      = ir.TriggerClick
	TriggerChange     = ir.TriggerChange
	TriggerInput      = ir.TriggerInput
	TriggerSubmit     = ir.TriggerSubmit
	WriterEventField  = ir.WriterEventField
	WriterEventToggle = ir.WriterEventToggle
	WriterSignal      = ir.WriterSignal
	SignalSlowClient  = ir.SignalSlowClient
)
