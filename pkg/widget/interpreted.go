package widget

import (
	"context"
	_ "embed"
	"errors"
	"fmt"
	"html/template"
	"io"
	"slices"
	"strconv"
	"strings"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/pkg/gotth/live"
	"github.com/candacelabs/csf/pkg/widget/internal/uigen"
)

// An interpreted widget is a checked document drawn at run time: the IR is the
// program, and nothing is generated or compiled. It is how a widget definition
// goes live in a running host, because Go cannot load code into a process and
// unload it again — a definition is data, and data can be replaced.
//
// It draws the static subset of the dialect: chrome, scene, legend, stats and
// indicators, every label resolved from its bindings. What needs a generated
// widget's machinery is refused by [Interpretable] with the reason, so an
// interpreted widget never silently drops part of the document it was given.

// The constructs dialect 0 has that an interpreted widget does not draw.
var (
	// ErrNotInterpretable reports a document an interpreted widget cannot draw
	// whole; the joined reasons name each construct.
	ErrNotInterpretable = errors.New("widget: the document uses constructs an interpreted widget does not draw")
	// ErrUnsound reports a document that came with findings.
	ErrUnsound = errors.New("widget: an interpreted widget needs a document with no findings")
)

// The reasons Interpretable gives, one per refused construct.
const (
	refuseMotion   = "motion: an interpreted widget does not animate; remove the motion block"
	refuseControls = "controls: an interpreted widget accepts no browser event; remove the controls block"
	refuseSignal   = "field %s: signal fields are runtime-minted and an interpreted widget has no runtime signal; give it an event writer"
	refuseToggle   = "event %s: toggles a field, and only a browser control could send it; write the field from a stream event"
	refuseUnstream = "event %s: no stream delivers it, so nothing could write it; declare a stream that delivers it"
)

// Interpretable reports every construct of a sound document an interpreted
// widget would not draw, or nothing when it draws the whole document.
func Interpretable(document *Document) []string {
	var reasons []string
	if document.Motion != nil {
		reasons = append(reasons, refuseMotion)
	}
	if len(document.Controls) > 0 {
		reasons = append(reasons, refuseControls)
	}
	for _, field := range document.StateFields {
		if field.Signal != "" {
			reasons = append(reasons, fmt.Sprintf(refuseSignal, field.Name))
		}
	}
	for _, event := range document.Events {
		if event.Toggles != nil {
			reasons = append(reasons, fmt.Sprintf(refuseToggle, event.Name))
		}
		if !slices.ContainsFunc(document.Streams, func(stream *Stream) bool { return stream.Delivers == event }) {
			reasons = append(reasons, fmt.Sprintf(refuseUnstream, event.Name))
		}
	}
	return reasons
}

// Values is an interpreted widget's state: each state field's value as the
// text a snapshot spells it — "true" or "false" for a flag, base-10 digits for
// a counter or a count, the string itself for text — in declaration order. It
// is immutable: a reducer returns a new one.
type Values struct {
	fields []string
}

// zeroValue is a field's value before any event wrote it.
func zeroValue(field *StateField) string {
	switch field.Type {
	case FieldFlag:
		return strconv.FormatBool(false)
	case FieldCounter, FieldCount:
		return strconv.Itoa(0)
	}
	return ""
}

// parseValue reads one wire field as its state field's type, or reports that
// it is not one.
func parseValue(fieldType FieldType, raw string) (string, bool) {
	switch fieldType {
	case FieldFlag:
		flag, err := strconv.ParseBool(raw)
		return strconv.FormatBool(flag), err == nil
	case FieldCounter, FieldCount:
		number, err := strconv.ParseInt(raw, 10, 64)
		return strconv.FormatInt(number, 10), err == nil
	}
	return raw, true
}

// InterpretedWidget draws one sound, interpretable document. I is the host's
// identity type, threaded through and never read.
type InterpretedWidget[I live.IIdentity] struct {
	document *Document
	index    map[*StateField]int
}

var _ IWidget[Values, live.AnonymousIdentity] = (*InterpretedWidget[live.AnonymousIdentity])(nil)

// NewInterpretedWidget draws document, which must have come with no findings
// and be [Interpretable].
func NewInterpretedWidget[I live.IIdentity](document *Document, findings []Finding) (*InterpretedWidget[I], error) {
	if document == nil || len(findings) > 0 {
		return nil, ErrUnsound
	}
	if reasons := Interpretable(document); len(reasons) > 0 {
		return nil, fmt.Errorf("%w: %s", ErrNotInterpretable, strings.Join(reasons, "; "))
	}
	index := make(map[*StateField]int, len(document.StateFields))
	for position, field := range document.StateFields {
		index[field] = position
	}
	return &InterpretedWidget[I]{document: document, index: index}, nil
}

// Document is the document the widget draws.
func (widget *InterpretedWidget[I]) Document() *Document { return widget.document }

// Register declares the document's region, its stream-delivered events as
// internal, its streams and its payloads.
func (widget *InterpretedWidget[I]) Register() Registration {
	registration := Registration{Name: widget.document.Name, Region: widget.document.Region}
	for _, event := range widget.document.Events {
		registration.Internal = append(registration.Internal, event.Wire)
		payload := EventPayload{Event: event.Wire}
		for _, field := range event.Fields {
			payload.Fields = append(payload.Fields, field.WireName)
		}
		if len(payload.Fields) > 0 {
			registration.Payloads = append(registration.Payloads, payload)
		}
	}
	for _, stream := range widget.document.Streams {
		registration.Streams = append(registration.Streams, StreamDeclaration{Name: stream.Name, Source: stream.Source, Delivers: stream.Delivers.Wire})
	}
	return registration
}

// Initial is every field at its zero value: false, 0 or empty text.
func (widget *InterpretedWidget[I]) Initial() Values {
	values := Values{fields: make([]string, len(widget.document.StateFields))}
	for position, field := range widget.document.StateFields {
		values.fields[position] = zeroValue(field)
	}
	return values
}

// Mount opens a session's copy at the initial values; the host delivers the
// stream's events.
func (widget *InterpretedWidget[I]) Mount(ctx context.Context, session live.Session[I]) (Values, []live.Effect[I], error) {
	return widget.Initial(), nil, nil
}

// Reduce applies one declared event whole, or not at all: a wire field that is
// not its state field's type leaves every field as it was.
func (widget *InterpretedWidget[I]) Reduce(state Values, event live.Event) (Values, []live.Effect[I]) {
	declared := slices.IndexFunc(widget.document.Events, func(candidate *EventDeclaration) bool { return candidate.Wire == event.Name })
	if declared < 0 {
		return state, nil
	}
	next := Values{fields: slices.Clone(state.fields)}
	for _, field := range widget.document.Events[declared].Fields {
		value, valid := parseValue(field.Type, event.Fields.Get(field.WireName))
		if !valid {
			return state, nil
		}
		next.fields[widget.index[field.Writes]] = value
	}
	return next, nil
}

// Unmount holds nothing to release.
func (widget *InterpretedWidget[I]) Unmount(ctx context.Context, session live.Session[I], state Values) {
}

// Snapshot is every field by name, in declaration order.
func (widget *InterpretedWidget[I]) Snapshot(state Values) Snapshot {
	snapshot := Snapshot{Widget: widget.document.Name}
	for position, field := range widget.document.StateFields {
		snapshot.Fields = append(snapshot.Fields, SnapshotField{Name: field.Name, Value: widget.value(state, position)})
	}
	return snapshot
}

// value is one field's value, or its zero value for a state that never held
// one (the zero Values).
func (widget *InterpretedWidget[I]) value(state Values, position int) string {
	if position < len(state.fields) {
		return state.fields[position]
	}
	return zeroValue(widget.document.StateFields[position])
}

// holds evaluates one predicate over state.
func (widget *InterpretedWidget[I]) holds(predicate *Predicate, state Values) bool {
	if predicate.Kind == PredicateAtomic {
		return widget.value(state, widget.index[predicate.Field]) == strconv.FormatBool(true)
	}
	for _, required := range predicate.Requires {
		if !widget.holds(required, state) {
			return false
		}
	}
	for _, forbidden := range predicate.Forbids {
		if widget.holds(forbidden, state) {
			return false
		}
	}
	for _, bound := range predicate.Bounds {
		number, _ := strconv.ParseInt(widget.value(state, widget.index[bound.Field]), 10, 64)
		if bound.Comparison == ComparisonAtLeast && number < int64(bound.Value) ||
			bound.Comparison == ComparisonAtMost && number > int64(bound.Value) {
			return false
		}
	}
	return true
}

// text renders one template over state.
func (widget *InterpretedWidget[I]) text(template *TextTemplate, state Values) string {
	var rendered strings.Builder
	for _, segment := range template.Segments {
		if segment.Field != nil {
			rendered.WriteString(widget.value(state, widget.index[segment.Field]))
			continue
		}
		rendered.WriteString(segment.Literal)
	}
	return rendered.String()
}

// label resolves one label: its literal template, or its binding's first
// matching clause, or the binding's otherwise.
func (widget *InterpretedWidget[I]) label(label *Label, state Values) string {
	if label == nil {
		return ""
	}
	if label.SourceKind == LabelLiteral {
		return widget.text(label.Literal, state)
	}
	for _, clause := range label.Binding.Clauses {
		if widget.holds(clause.Predicate, state) == (clause.Polarity == GuardWhen) {
			return widget.text(clause.Template, state)
		}
	}
	return widget.text(label.Binding.Otherwise, state)
}

// The interpreted markup. It is the generated view's markup, drawn by
// html/template, which escapes every label: label text is data, never markup.
//
//go:embed interpreted.html
var interpretedSource string

var interpretedViews = template.Must(template.New("interpreted").Parse(interpretedSource))

const (
	interpretedTemplate = "widget"
	titleIDSuffix       = ".title"
)

type interpretedView struct {
	Name, Palette, Region, TitleID string
	Source, Title, Description     string
	Extent, Centre                 int
	Orbits                         []orbitView
	Edges                          []edgeView
	Nodes                          []nodeView
	Legend                         []legendView
	Stats                          []string
	Indicators                     []indicatorView
}

type orbitView struct {
	Token                      Token
	RadiusX, RadiusY, Rotation int
}

type edgeView struct {
	Left, Top     int
	Angle, Length string
}

type nodeView struct {
	Role                       string
	Marker                     Marker
	Token                      Token
	Left, Top, Radius          int
	TitleOffset, CaptionOffset int
	Title, Caption             string
	HasCaption                 bool
}

type legendView struct {
	Token Token
	Text  string
}

type indicatorView struct {
	Tone Token
	Text string
}

// Render draws the widget's region in the generated view's markup.
func (widget *InterpretedWidget[I]) Render(state Values) templ.Component {
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		return interpretedViews.ExecuteTemplate(writer, interpretedTemplate, widget.view(state))
	})
}

func (widget *InterpretedWidget[I]) view(state Values) interpretedView {
	document := widget.document
	view := interpretedView{
		Name: document.Name, Palette: document.Palette, Region: document.Region, TitleID: document.Region + titleIDSuffix,
		Description: widget.label(document.Scene.DescriptionSlot.Label, state),
		Extent:      uigen.SceneExtent, Centre: uigen.SceneExtent / 2,
	}
	for _, slot := range document.Slots {
		switch slot.Kind {
		case SlotSource:
			view.Source = widget.label(slot.Label, state)
		case SlotTitle:
			view.Title = widget.label(slot.Label, state)
		case SlotStat:
			view.Stats = append(view.Stats, widget.label(slot.Label, state))
		}
	}
	for index, orbit := range document.Scene.Orbits {
		radiusX, radiusY, rotation := uigen.OrbitGeometry(index)
		view.Orbits = append(view.Orbits, orbitView{Token: orbit.Token, RadiusX: radiusX, RadiusY: radiusY, Rotation: rotation})
	}
	for _, edge := range document.Scene.Edges {
		view.Edges = append(view.Edges, edgeView{
			Left: edge.From.Placement.Left, Top: edge.From.Placement.Top,
			Angle: uigen.FormatNumber(edge.Geometry.AngleDegrees), Length: uigen.FormatNumber(edge.Geometry.LengthPercent),
		})
	}
	for _, node := range document.Scene.Nodes {
		radius, titleOffset, captionOffset := uigen.MarkerGeometry(node.Role.Marker)
		view.Nodes = append(view.Nodes, nodeView{
			Role: node.Role.Name, Marker: node.Role.Marker, Token: node.Role.Token,
			Left: node.Placement.Left, Top: node.Placement.Top, Radius: radius, TitleOffset: titleOffset, CaptionOffset: captionOffset,
			Title: widget.label(node.TitleLabel, state), Caption: widget.label(node.CaptionLabel, state), HasCaption: node.CaptionLabel != nil,
		})
	}
	for _, entry := range document.Legend.Entries {
		view.Legend = append(view.Legend, legendView{Token: entry.Channel.Token, Text: widget.label(entry.Label, state)})
	}
	for _, indicator := range document.Indicators {
		tone := TokenWarning
		if widget.holds(indicator.Predicate, state) {
			tone = TokenPositive
		}
		view.Indicators = append(view.Indicators, indicatorView{Tone: tone, Text: widget.label(indicator.Label, state)})
	}
	return view
}
