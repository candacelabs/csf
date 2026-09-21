// Package ir holds the widget interpreter's typed intermediate
// representation: one resolved, ordered, total record per document.
//
// Five properties hold of every value here, and a generator may rely on all
// five (docs/dialect.md § 10.1):
//
//   - Resolved. Every reference is a handle — a pointer to the record it names
//     — never a name. A generator cannot meet an unknown identifier, so it needs
//     no error path for one and no fallback.
//   - Total. No field's absence means "work it out". The interpreter
//     materialises defaults, so the IR says the same thing whether or not the
//     author wrote the default.
//   - Ordered. Every collection is a sequence in declaration order. A render
//     must be byte-identical for equal state, so an IR holding a set would make
//     that depend on map iteration order.
//   - Anchored. Every record carries a SourceSpan.
//   - Closed. Nothing here names a file path, a host, an address or a
//     credential. What is not in the document cannot be in the IR.
//
// EdgeGeometry, Legend and DirtyProjection are computed rather than parsed:
// they are here so a generator need not re-derive them, and absent from the
// grammar so an author cannot contradict them.
package ir

import "github.com/candacelabs/csf/pkg/widget/internal/diag"

// FieldType is a state field's declared type.
type FieldType string

// The four state field types. A counter is monotonic and is the only type a
// tick may name; a count is an ordinary number.
const (
	FieldFlag    FieldType = "flag"
	FieldCounter FieldType = "counter"
	FieldCount   FieldType = "count"
	FieldText    FieldType = "text"
)

// FieldTypes are the four state field types, in the order a message lists them.
func FieldTypes() []string {
	return []string{string(FieldFlag), string(FieldCounter), string(FieldCount), string(FieldText)}
}

// Token is one of the seven semantic token names. The namespace is closed: a
// widget writes a name only, and there is no inline colour and no `var(--…)`
// escape hatch.
type Token string

// The seven semantic tokens.
const (
	TokenSurface  Token = "surface"
	TokenInk      Token = "ink"
	TokenMuted    Token = "muted"
	TokenRule     Token = "rule"
	TokenAccent   Token = "accent"
	TokenPositive Token = "positive"
	TokenWarning  Token = "warning"
)

// TokenNames are the seven semantic tokens, in palette order.
func TokenNames() []string {
	return []string{
		string(TokenSurface), string(TokenInk), string(TokenMuted), string(TokenRule),
		string(TokenAccent), string(TokenPositive), string(TokenWarning),
	}
}

// PaletteFieldStation is the palette the SDK ships, and the one every exemplar
// declares.
//
// The set of palette names lives here rather than beside the values, for the
// reason the token names do: it is a closed namespace a document writes into,
// so the interpreter has to know it to refuse a name that is not in it, and the
// interpreter cannot reach the package that holds the values. A palette added
// there without a name here would be one no document could declare.
const PaletteFieldStation = "fieldStation"

// PaletteNames are the palettes a `palette` directive may name, in the order a
// message lists them.
func PaletteNames() []string {
	return []string{PaletteFieldStation}
}

// SignalSlowClient is the one runtime-minted signal of dialect 0: true while
// the client is behind, false after it recovers.
const SignalSlowClient = "slowClient"

// PredicateKind distinguishes a flag field used as a predicate from a
// composition declared in the predicates block.
type PredicateKind string

// The two predicate forms.
const (
	PredicateAtomic   PredicateKind = "atomic"
	PredicateComposed PredicateKind = "composed"
)

// Comparison is a numeric bound's operator.
type Comparison string

// The two bound comparisons. They ship together because half of a symmetric
// pair is the kind of gap an author fills by inventing a spelling.
const (
	ComparisonAtLeast Comparison = "atLeast"
	ComparisonAtMost  Comparison = "atMost"
)

// GuardPolarity is a binding clause's polarity. The two polarities are two
// constructs, not two spellings of one.
type GuardPolarity string

// The two guard polarities.
const (
	GuardWhen    GuardPolarity = "when"
	GuardWhenNot GuardPolarity = "whenNot"
)

// LabelSourceKind is which of a label's two sources it carries.
type LabelSourceKind string

// The two label sources. A label carries exactly one.
const (
	LabelLiteral LabelSourceKind = "literal"
	LabelBound   LabelSourceKind = "binding"
)

// SlotKind is one of the four closed chrome positions.
type SlotKind string

// The four slots. The set is closed by the dialect version.
const (
	SlotTitle       SlotKind = "title"
	SlotSource      SlotKind = "source"
	SlotDescription SlotKind = "description"
	SlotStat        SlotKind = "stat"
)

// Marker is a role's marker size.
type Marker string

// The two marker sizes.
const (
	MarkerLarge Marker = "large"
	MarkerSmall Marker = "small"
)

// Direction is a channel's travel direction. A channel is never bidirectional:
// two directions are two channels.
type Direction string

// The two channel directions.
const (
	DirectionForward Direction = "forward"
	DirectionReverse Direction = "reverse"
)

// Trigger is a control's DOM interaction.
type Trigger string

// The four triggers.
const (
	TriggerClick  Trigger = "click"
	TriggerChange Trigger = "change"
	TriggerInput  Trigger = "input"
	TriggerSubmit Trigger = "submit"
)

// Triggers are the four DOM triggers, in the order a message lists them.
func Triggers() []string {
	return []string{string(TriggerClick), string(TriggerChange), string(TriggerInput), string(TriggerSubmit)}
}

// WriterKind is how a state field is written. There are exactly three writers,
// and a field written by none of them is an error.
type WriterKind string

// The three writers.
const (
	WriterEventField  WriterKind = "eventField"
	WriterEventToggle WriterKind = "eventToggle"
	WriterSignal      WriterKind = "signal"
)

// Writer is one resolved writer of a state field.
type Writer struct {
	Kind   WriterKind
	Event  *EventDeclaration
	Field  *EventField
	Signal string
	Span   diag.SourceSpan
}

// StateField is one named, typed field of the widget's state.
type StateField struct {
	Name    string
	Type    FieldType
	Signal  string
	Writers []Writer
	Span    diag.SourceSpan
}

// Numeric reports whether the field may carry a numeric bound.
func (field *StateField) Numeric() bool {
	return field.Type == FieldCounter || field.Type == FieldCount
}

// Interpolable reports whether the field may be interpolated into a template.
// A flag may not: rendering a boolean as text is a decision for a guard.
func (field *StateField) Interpolable() bool {
	return field.Type != FieldFlag
}

// NumericBound is a comparison of a numeric state field against an integer
// literal. It never compares two fields, so no evaluation order exists.
type NumericBound struct {
	Field      *StateField
	Comparison Comparison
	Value      int
	Span       diag.SourceSpan
}

// Predicate is a named boolean over state, from either of two declaration
// forms: a flag field under its own name, or a composition.
type Predicate struct {
	Name     string
	Kind     PredicateKind
	Field    *StateField
	Requires []*Predicate
	Forbids  []*Predicate
	Bounds   []NumericBound
	Span     diag.SourceSpan
}

// TemplateSegment is one run of a text template: literal text, or one
// interpolated state field.
type TemplateSegment struct {
	Literal string
	Field   *StateField
}

// TextTemplate is a sequence of literal runs and interpolations.
type TextTemplate struct {
	Segments []TemplateSegment
	Span     diag.SourceSpan
}

// Empty reports whether the template renders no text at all under every state.
func (template *TextTemplate) Empty() bool {
	if template == nil {
		return true
	}
	for _, segment := range template.Segments {
		if segment.Field != nil || segment.Literal != "" {
			return false
		}
	}
	return true
}

// BindingClause is one guarded arm of a binding.
type BindingClause struct {
	Polarity  GuardPolarity
	Predicate *Predicate
	Template  *TextTemplate
	Span      diag.SourceSpan
}

// Binding is a named, ordered, total decision from state to one text value.
// The first matching clause wins; Otherwise is always present.
type Binding struct {
	Name      string
	Clauses   []*BindingClause
	Otherwise *TextTemplate
	Span      diag.SourceSpan
}

// Label is a named text slot holding exactly one source.
type Label struct {
	Name       string
	SourceKind LabelSourceKind
	Literal    *TextTemplate
	Binding    *Binding
	Span       diag.SourceSpan
}

// Slot is a filled position in the widget's chrome.
type Slot struct {
	Kind    SlotKind
	Label   *Label
	Ordinal int
	Span    diag.SourceSpan
}

// Role is a named node kind fixing appearance and animation eligibility.
type Role struct {
	Name            string
	Token           Token
	Marker          Marker
	EmphasisAllowed bool
	Span            diag.SourceSpan
}

// Channel is a named kind of traffic an edge can carry.
type Channel struct {
	Name        string
	Direction   Direction
	Token       Token
	LegendLabel *Label
	Span        diag.SourceSpan
}

// Placement is a named point in the scene's normalized box, in percent.
type Placement struct {
	Name string
	Left int
	Top  int
	Span diag.SourceSpan
}

// Orbit is a decorative ambient ellipse belonging to the scene.
type Orbit struct {
	Name  string
	Token Token
	Span  diag.SourceSpan
}

// Node is a named participant drawn in the scene.
type Node struct {
	Name         string
	Role         *Role
	Placement    *Placement
	TitleLabel   *Label
	CaptionLabel *Label
	Span         diag.SourceSpan
}

// EdgeGeometry is an edge's length and angle. Both are computed from the two
// endpoints' placements and are never authored: LengthPercent is the distance
// across the normalized scene box, AngleDegrees the clockwise angle from the
// positive left axis, in (-180, 180].
type EdgeGeometry struct {
	LengthPercent float64
	AngleDegrees  float64
}

// Edge is an ordered pair of distinct nodes carrying one or more channels.
type Edge struct {
	Name     string
	From     *Node
	To       *Node
	Channels []*Channel
	Geometry EdgeGeometry
	Span     diag.SourceSpan
}

// Pulse is one finite animation of one channel on one edge.
type Pulse struct {
	Name                 string
	Edge                 *Edge
	Channel              *Channel
	DurationMilliseconds int
	DelayMilliseconds    int
	Span                 diag.SourceSpan
}

// Emphasis is one finite animation on one node.
type Emphasis struct {
	Name                 string
	Node                 *Node
	DurationMilliseconds int
	DelayMilliseconds    int
	Span                 diag.SourceSpan
}

// Motion gates and schedules every animation a widget has.
//
// HostStatusGate is always true: the host's own connection status is part of
// the compiled gate whether or not the author mentioned it, because an
// animation running against a dead connection is a lie about liveness. It is
// carried explicitly so a generator reads the obligation rather than
// remembering it.
type Motion struct {
	Requires       []*Predicate
	Forbids        []*Predicate
	RestartOn      *StateField
	Pulses         []*Pulse
	Emphases       []*Emphasis
	HostStatusGate bool
	Span           diag.SourceSpan
}

// Scene is the widget's single drawing area and single accessibility boundary.
type Scene struct {
	Name            string
	DescriptionSlot *Slot
	Orbits          []*Orbit
	Nodes           []*Node
	Edges           []*Edge
	Span            diag.SourceSpan
}

// Indicator is a named status mark whose tone one predicate selects.
type Indicator struct {
	Name      string
	Label     *Label
	Predicate *Predicate
	Span      diag.SourceSpan
}

// Control turns one DOM interaction into one declared event.
type Control struct {
	Name         string
	CaptionLabel *Label
	Trigger      Trigger
	Event        *EventDeclaration
	PressedWhen  *Predicate
	Span         diag.SourceSpan
}

// EventField binds one wire payload field to one state field.
type EventField struct {
	WireName string
	Writes   *StateField
	Type     FieldType
	Span     diag.SourceSpan
}

// EventDeclaration is one inbound event the widget accepts, with the wire name
// the host registers. The declared set is exhaustive and default-deny.
type EventDeclaration struct {
	Name    string
	Wire    string
	Toggles *StateField
	Fields  []*EventField
	Span    diag.SourceSpan
}

// Stream is a named long-running subscription delivering one event. It is a
// declaration, never a connection: it carries a source name and nothing else.
type Stream struct {
	Name     string
	Source   string
	Delivers *EventDeclaration
	Ordering *StateField
	Span     diag.SourceSpan
}

// LegendEntry is one row of the computed footer legend.
type LegendEntry struct {
	Channel *Channel
	Label   *Label
}

// Legend is the computed projection of the declared channels, in declaration
// order. It is never authored, so it cannot disagree with the picture.
type Legend struct {
	Entries []LegendEntry
}

// DirtyProjection is the computed set of state fields the widget's rendered
// output depends on, in state declaration order: every field its bindings and
// predicates read, plus the tick its motion re-arms on, whose value the markup
// carries as a per-tick identity. Under-declaring it is a correctness bug,
// which is why an author is not asked to maintain it.
type DirtyProjection struct {
	Fields []*StateField
}

// Document is one resolved widget.
type Document struct {
	Name            string
	DialectVersion  int
	Region          string
	Palette         string
	StateFields     []*StateField
	Predicates      []*Predicate
	Bindings        []*Binding
	Labels          []*Label
	Slots           []*Slot
	Roles           []*Role
	Channels        []*Channel
	Placements      []*Placement
	Scene           *Scene
	Motion          *Motion
	Indicators      []*Indicator
	Controls        []*Control
	Events          []*EventDeclaration
	Streams         []*Stream
	Legend          Legend
	DirtyProjection DirtyProjection
	Span            diag.SourceSpan
}

// Slot returns the widget's first slot of a kind, which is the only one for
// every kind but stat.
func (document *Document) Slot(kind SlotKind) (*Slot, bool) {
	for _, slot := range document.Slots {
		if slot.Kind == kind {
			return slot, true
		}
	}
	return nil, false
}
