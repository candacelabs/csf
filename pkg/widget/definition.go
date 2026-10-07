package widget

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"path"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/candacelabs/csf/pkg/gotth/live"
	widgetrefinementv1 "github.com/candacelabs/csf/pkg/widget/refinement/v1"
)

// A widget definition is a directory of data a host installs while it runs:
// the dialect document, the binding from the document's stream to the fields
// of a source the host has, and fixtures the widget must render. Nothing in it
// is code. The host checks it before installing it, and a definition with any
// reason against it is refused and never drawn.
const (
	// DefinitionFile is the dialect document.
	DefinitionFile = "definition.widget"
	// BindingFile is the [SourceBinding], as JSON.
	BindingFile = "binding.json"
	// FixturesFile is the [Fixture] list, as JSON.
	FixturesFile = "fixtures.json"
)

// SourceBinding fills the event one of the document's streams delivers from
// the source the stream names: each of the event's wire fields reads one field
// of the source's document.
type SourceBinding struct {
	// Stream is the document's stream; its source is the one the host reads.
	Stream string `json:"stream"`
	// Fields bind every wire field of the delivered event, in any order.
	Fields []FieldBinding `json:"fields"`
}

// FieldBinding reads one wire field from one source field.
type FieldBinding struct {
	// Wire is the event's wire field name.
	Wire string `json:"wire"`
	// Path is the source field, its JSON names joined by dots: fixers.budget_usd.
	Path string `json:"path"`
}

// Fixture is one source document and the text the widget must show for it.
type Fixture struct {
	Name string `json:"name"`
	// Source is a document of the bound source, exactly as the host reads it.
	Source json.RawMessage `json:"source"`
	// Expect is text the rendered widget must contain, each as written.
	Expect []string `json:"expect"`
}

// SourceKind is what one source field holds, as its JSON spells it.
type SourceKind string

// The four kinds a binding can read. An object or a list is not one value and
// is not bindable.
const (
	SourceBool    SourceKind = "bool"
	SourceInteger SourceKind = "integer"
	SourceNumber  SourceKind = "number"
	SourceString  SourceKind = "string"
)

// SourceSchema is the bindable fields of one source, by path.
type SourceSchema struct {
	fields map[string]SourceKind
}

// Kind is one field's kind, and whether the source has the field.
func (schema SourceSchema) Kind(fieldPath string) (SourceKind, bool) {
	kind, known := schema.fields[fieldPath]
	return kind, known
}

// Paths are every bindable field, sorted.
func (schema SourceSchema) Paths() []string {
	paths := make([]string, 0, len(schema.fields))
	for fieldPath := range schema.fields {
		paths = append(paths, fieldPath)
	}
	slices.Sort(paths)
	return paths
}

// Sources are the sources a host can bind, by the name a stream's source
// declares.
type Sources map[string]SourceSchema

// Names are the source names, sorted.
func (sources Sources) Names() []string {
	names := make([]string, 0, len(sources))
	for name := range sources {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

var timeType = reflect.TypeFor[time.Time]()

// jsonTag is the struct tag key encoding/json names a field by.
const jsonTag = "json"

// SchemaOf derives a source's schema from the Go type its owner decodes it
// into, so the schema is the owner's declaration and cannot drift from it. It
// reads the encoding/json names; a time is a string, a pointer is its element
// (null reads as the zero value), and a list, a map or an untagged field is
// not bindable.
func SchemaOf[T any]() SourceSchema {
	schema := SourceSchema{fields: map[string]SourceKind{}}
	collectFields(reflect.TypeFor[T](), "", schema.fields)
	return schema
}

// collectFields walks one struct type. It is reflection because the schema's
// whole point is to read the owner's declared type, whatever it is.
func collectFields(structType reflect.Type, prefix string, fields map[string]SourceKind) {
	for position := range structType.NumField() {
		field := structType.Field(position)
		name, _, _ := strings.Cut(field.Tag.Get(jsonTag), ",")
		if !field.IsExported() || name == "" || name == "-" {
			continue
		}
		fieldType := field.Type
		if fieldType.Kind() == reflect.Pointer {
			fieldType = fieldType.Elem()
		}
		fieldPath := prefix + name
		switch {
		case fieldType == timeType || fieldType.Kind() == reflect.String:
			fields[fieldPath] = SourceString
		case fieldType.Kind() == reflect.Bool:
			fields[fieldPath] = SourceBool
		case fieldType.Kind() >= reflect.Int && fieldType.Kind() <= reflect.Uint64:
			fields[fieldPath] = SourceInteger
		case fieldType.Kind() == reflect.Float32 || fieldType.Kind() == reflect.Float64:
			fields[fieldPath] = SourceNumber
		case fieldType.Kind() == reflect.Struct:
			collectFields(fieldType, fieldPath+".", fields)
		}
	}
}

// readable reports which source kinds each state field type reads: a flag a
// bool; a counter or a count an integer, or a number truncated toward zero;
// text any of the three scalars.
var readable = map[FieldType][]SourceKind{
	FieldFlag:    {SourceBool},
	FieldCounter: {SourceInteger, SourceNumber},
	FieldCount:   {SourceInteger, SourceNumber},
	FieldText:    {SourceString, SourceInteger, SourceNumber},
}

// absent is how a text field shows a source field that is null or missing.
const absent = "–"

// numberFormat is how a text field shows a number: two decimals, which is what
// every number the shipped sources carry (dollars, rates, factors) reads as.
const numberFormat = "%.2f"

// CheckedDefinition is a definition every check passed: an interpreted widget
// and the binding that fills it.
type CheckedDefinition[I live.IIdentity] struct {
	// Name is the definition's directory name.
	Name string
	// Source is the source the bound stream names.
	Source  string
	Widget  *InterpretedWidget[I]
	binding SourceBinding
	event   *EventDeclaration
	// kinds are the schema's kind of each bound wire field, which decide how a
	// value reads: JSON spells the number 100.0 as 100.
	kinds map[string]SourceKind
}

// Values reads one source document through the binding into the widget's
// values. A field the document does not carry, or carries as null, reads as
// its zero value: false, 0, or "–" for text.
func (checked *CheckedDefinition[I]) Values(content []byte) (Values, error) {
	event, err := checked.Event(content)
	if err != nil {
		return Values{}, err
	}
	values, _ := checked.Widget.Reduce(checked.Widget.Initial(), event)
	return values, nil
}

// Event reads one source document through the binding into the event the
// bound stream delivers.
func (checked *CheckedDefinition[I]) Event(content []byte) (live.Event, error) {
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.UseNumber()
	var document map[string]any // a JSON object of the source's own shape, walked by path
	if err := decoder.Decode(&document); err != nil {
		return live.Event{}, fmt.Errorf("widget %s: decode %s: %w", checked.Name, checked.Source, err)
	}
	fields := map[string]string{}
	for _, binding := range checked.binding.Fields {
		eventField := checked.event.Fields[slices.IndexFunc(checked.event.Fields, func(field *EventField) bool { return field.WireName == binding.Wire })]
		fields[binding.Wire] = sourceText(eventField.Type, checked.kinds[binding.Wire], lookup(document, binding.Path))
	}
	return live.Event{Name: checked.event.Wire, FragmentID: checked.Widget.document.Region, Fields: live.NewFields(fields)}, nil
}

// lookup walks one dotted path; a missing step is nil.
func lookup(document map[string]any, fieldPath string) any {
	var value any = document
	for step := range strings.SplitSeq(fieldPath, ".") {
		object, isObject := value.(map[string]any)
		if !isObject {
			return nil
		}
		value = object[step]
	}
	return value
}

// sourceText is one source value, of the schema's kind, as the wire text its
// state field parses.
func sourceText(fieldType FieldType, kind SourceKind, value any) string {
	switch typed := value.(type) {
	case bool:
		return strconv.FormatBool(typed)
	case string:
		return typed
	case json.Number:
		number, _ := typed.Float64()
		if fieldType == FieldText && kind == SourceNumber {
			return fmt.Sprintf(numberFormat, number)
		}
		return strconv.FormatInt(int64(number), 10)
	}
	if fieldType == FieldText {
		return absent
	}
	return zeroValue(&StateField{Type: fieldType})
}

// The reasons a check gives beyond the dialect's own findings.
const (
	reasonMissing       = "%s: not read: %v"
	reasonDecode        = "%s: not a %s: %v"
	reasonRefinement    = "%s: refinement does not hold: %v"
	reasonInterpretable = "%s: %s"
	reasonNoStream      = "%s: stream %q is not declared in %s; declare it or bind one of: %s"
	reasonNoSource      = "%s: stream %s reads source %q, which this host does not have; use one of: %s"
	reasonUnbound       = "%s: wire field %s of event %s is not bound; bind it to a field of %s"
	reasonUnknownWire   = "%s: wire field %q is not a field of event %s"
	reasonTwice         = "%s: wire field %s is bound twice"
	reasonNoPath        = "%s: field %s: %q is not a field of %s; its fields are: %s"
	reasonKind          = "%s: field %s: %s holds a %s, which a %s field cannot read"
	reasonNoFixture     = "%s: no fixture; give at least one source document and the text it must show"
	reasonFixtureSource = "%s: fixture %q: %v"
	reasonFixtureShows  = "%s: fixture %q: the widget does not show %q"
)

// CheckDefinition checks the definition in directory name of definitions and
// returns it when every check passed, or every reason against it: the document
// parses and validates, the dialect's refinements hold of it, an interpreted
// widget draws all of it, every field the binding reads exists in its source's
// schema with a kind its state field can read, and every fixture renders the
// text it expects. It is pure: it reads definitions and nothing else.
func CheckDefinition[I live.IIdentity](definitions fs.FS, name string, sources Sources) (*CheckedDefinition[I], []string) {
	documentPath, bindingPath, fixturesPath := path.Join(name, DefinitionFile), path.Join(name, BindingFile), path.Join(name, FixturesFile)
	source, err := fs.ReadFile(definitions, documentPath)
	if err != nil {
		return nil, []string{fmt.Sprintf(reasonMissing, documentPath, err)}
	}
	document, findings := Interpret(documentPath, source)
	if len(findings) > 0 {
		reasons := make([]string, 0, len(findings))
		for _, finding := range findings {
			reasons = append(reasons, strings.TrimSpace(finding.String()))
		}
		return nil, reasons
	}
	if reasons := refinements(documentPath, document); len(reasons) > 0 {
		return nil, reasons
	}
	widget, err := NewInterpretedWidget[I](document, nil)
	if err != nil {
		var reasons []string
		for _, reason := range Interpretable(document) {
			reasons = append(reasons, fmt.Sprintf(reasonInterpretable, documentPath, reason))
		}
		return nil, reasons
	}
	checked := &CheckedDefinition[I]{Name: name, Widget: widget}
	if reasons := checked.bind(definitions, bindingPath, sources); len(reasons) > 0 {
		return nil, reasons
	}
	if reasons := checked.renderFixtures(definitions, fixturesPath); len(reasons) > 0 {
		return nil, reasons
	}
	return checked, nil
}

// refinements holds the dialect's Liquid Proto refinements over the
// document's local values: its region, placements, identifiers and wire names.
// The validator already refused any value they reject, so a reason here is
// the two layers disagreeing, which the refinement agreement spec forbids.
func refinements(documentPath string, document *Document) []string {
	var failures []error
	failures = append(failures, widgetrefinementv1.ValidateRegionIdentity(&widgetrefinementv1.RegionIdentity{Value: document.Region}))
	for _, placement := range document.Placements {
		failures = append(failures, widgetrefinementv1.ValidatePlacement(&widgetrefinementv1.Placement{Left: int32(placement.Left), Top: int32(placement.Top)}))
	}
	for _, field := range document.StateFields {
		failures = append(failures, widgetrefinementv1.ValidateIdentifier(&widgetrefinementv1.Identifier{Value: field.Name}))
	}
	for _, event := range document.Events {
		failures = append(failures, widgetrefinementv1.ValidateWireName(&widgetrefinementv1.WireName{Value: event.Wire}))
		for _, field := range event.Fields {
			failures = append(failures, widgetrefinementv1.ValidateEventFieldWireName(&widgetrefinementv1.EventFieldWireName{Value: field.WireName}))
		}
	}
	var reasons []string
	for _, failure := range failures {
		if failure != nil {
			reasons = append(reasons, fmt.Sprintf(reasonRefinement, documentPath, failure))
		}
	}
	return reasons
}

// bind reads the binding and checks it against the document and the sources.
func (checked *CheckedDefinition[I]) bind(definitions fs.FS, bindingPath string, sources Sources) []string {
	content, err := fs.ReadFile(definitions, bindingPath)
	if err != nil {
		return []string{fmt.Sprintf(reasonMissing, bindingPath, err)}
	}
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&checked.binding); err != nil {
		return []string{fmt.Sprintf(reasonDecode, bindingPath, "source binding", err)}
	}
	document := checked.Widget.document
	declared := slices.IndexFunc(document.Streams, func(stream *Stream) bool { return stream.Name == checked.binding.Stream })
	if declared < 0 {
		names := make([]string, 0, len(document.Streams))
		for _, stream := range document.Streams {
			names = append(names, stream.Name)
		}
		return []string{fmt.Sprintf(reasonNoStream, bindingPath, checked.binding.Stream, DefinitionFile, strings.Join(names, ", "))}
	}
	stream := document.Streams[declared]
	schema, known := sources[stream.Source]
	if !known {
		return []string{fmt.Sprintf(reasonNoSource, bindingPath, stream.Name, stream.Source, strings.Join(sources.Names(), ", "))}
	}
	checked.Source, checked.event, checked.kinds = stream.Source, stream.Delivers, map[string]SourceKind{}
	var reasons []string
	bound := map[string]bool{}
	for _, binding := range checked.binding.Fields {
		field := slices.IndexFunc(checked.event.Fields, func(field *EventField) bool { return field.WireName == binding.Wire })
		switch {
		case field < 0:
			reasons = append(reasons, fmt.Sprintf(reasonUnknownWire, bindingPath, binding.Wire, checked.event.Name))
			continue
		case bound[binding.Wire]:
			reasons = append(reasons, fmt.Sprintf(reasonTwice, bindingPath, binding.Wire))
			continue
		}
		bound[binding.Wire] = true
		kind, exists := schema.Kind(binding.Path)
		checked.kinds[binding.Wire] = kind
		if !exists {
			reasons = append(reasons, fmt.Sprintf(reasonNoPath, bindingPath, binding.Wire, binding.Path, stream.Source, strings.Join(schema.Paths(), ", ")))
			continue
		}
		if fieldType := checked.event.Fields[field].Type; !slices.Contains(readable[fieldType], kind) {
			reasons = append(reasons, fmt.Sprintf(reasonKind, bindingPath, binding.Wire, binding.Path, kind, fieldType))
		}
	}
	for _, field := range checked.event.Fields {
		if !bound[field.WireName] {
			reasons = append(reasons, fmt.Sprintf(reasonUnbound, bindingPath, field.WireName, checked.event.Name, stream.Source))
		}
	}
	return reasons
}

// renderFixtures renders every fixture through the binding and checks it
// shows the text it expects.
func (checked *CheckedDefinition[I]) renderFixtures(definitions fs.FS, fixturesPath string) []string {
	content, err := fs.ReadFile(definitions, fixturesPath)
	if err != nil {
		return []string{fmt.Sprintf(reasonMissing, fixturesPath, err)}
	}
	var fixtures []Fixture
	decoder := json.NewDecoder(bytes.NewReader(content))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&fixtures); err != nil {
		return []string{fmt.Sprintf(reasonDecode, fixturesPath, "fixture list", err)}
	}
	if len(fixtures) == 0 {
		return []string{fmt.Sprintf(reasonNoFixture, fixturesPath)}
	}
	var reasons []string
	for _, fixture := range fixtures {
		rendered, err := checked.Render(fixture.Source)
		if err != nil {
			reasons = append(reasons, fmt.Sprintf(reasonFixtureSource, fixturesPath, fixture.Name, err))
			continue
		}
		for _, expected := range fixture.Expect {
			if !strings.Contains(rendered, template.HTMLEscapeString(expected)) {
				reasons = append(reasons, fmt.Sprintf(reasonFixtureShows, fixturesPath, fixture.Name, expected))
			}
		}
	}
	return reasons
}

// Render draws the widget for one source document.
func (checked *CheckedDefinition[I]) Render(content []byte) (string, error) {
	values, err := checked.Values(content)
	if err != nil {
		return "", err
	}
	var rendered bytes.Buffer
	if err := checked.Widget.Render(values).Render(context.Background(), &rendered); err != nil {
		return "", errors.Join(fmt.Errorf("widget %s: render", checked.Name), err)
	}
	return rendered.String(), nil
}
