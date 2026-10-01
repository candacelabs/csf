package uigen

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode"

	"github.com/candacelabs/csf/pkg/widget/internal/ir"
)

// ErrUnexportable is returned for a document identifier with no exported Go
// spelling — one beginning with an underscore, which the dialect's identifier
// pattern allows and Go's export rule does not.
var ErrUnexportable = errors.New("uigen: a document identifier has no exported Go spelling")

// ErrNameCollision is returned when two document identifiers, distinct in the
// dialect's flat namespace, would emit one Go identifier.
//
// The dialect's own namespace cannot prevent it: `statusText` and `StatusText`
// are two names there and one exported name here. Reporting it is what keeps
// the generator from emitting a file that will not compile and blaming templ
// for it.
var ErrNameCollision = errors.New("uigen: two document identifiers emit one Go identifier")

// names is every Go identifier one document emits, derived once so that the
// scaffold and the view cannot spell the same thing two ways.
//
// The maps are keyed by IR pointer and are only ever read through a key the
// caller already holds. Nothing iterates them: emission walks the document's
// ordered sequences, because a generator that ranged a map would hand back the
// determinism the IR's ordering exists to provide.
type names struct {
	widgetType    string
	stateType     string
	viewFunc      string
	viewAtFunc    string
	constructor   string
	constructorAt string
	nameConst     string
	regionConst   string
	paletteConst  string
	titleIDConst  string

	// The derivations a widget's motion adds to its state type. They are
	// empty for a widget with no motion block, and are claimed only for one
	// that has it: a document with no motion may name a label `motionActive`,
	// and refusing it would be refusing a name nothing is using.
	motionActiveFunc string
	motionActiveText string
	motionTickFunc   string
	motionTickAtFunc string

	fields         map[*ir.StateField]string
	bindings       map[*ir.Binding]string
	labels         map[*ir.Label]string
	predicates     map[*ir.Predicate]string
	events         map[*ir.EventDeclaration]string
	eventFields    map[*ir.EventField]string
	indicatorTones map[*ir.Indicator]string
	controlPressed map[*ir.Control]string
}

// The suffixes the derivations a document does not name take, on the state type.
// They are constants rather than literals at their use sites because each is
// spelled twice — once where the method is claimed and once where it is emitted
// — and two spellings of one name is a generator that emits a call to a method
// it did not write.
const (
	motionActiveName = "MotionActive"
	motionTextSuffix = "Text"
	motionTickName   = "MotionTickID"
	instanceSuffix   = "At"
	toneSuffix       = "Tone"
	pressedSuffix    = "PressedText"
)

// newNames derives every identifier and reports the first fault.
func newNames(document *ir.Document) (*names, error) {
	widgetType, exportError := exported(document.Name)
	if exportError != nil {
		return nil, exportError
	}

	derived := &names{
		widgetType:     widgetType,
		stateType:      widgetType + "State",
		viewFunc:       widgetType + "View",
		viewAtFunc:     widgetType + "View" + instanceSuffix,
		constructor:    "New" + widgetType,
		constructorAt:  "New" + widgetType + instanceSuffix,
		nameConst:      widgetType + "Name",
		regionConst:    widgetType + "Region",
		paletteConst:   widgetType + "Palette",
		titleIDConst:   widgetType + "TitleID",
		fields:         map[*ir.StateField]string{},
		bindings:       map[*ir.Binding]string{},
		labels:         map[*ir.Label]string{},
		predicates:     map[*ir.Predicate]string{},
		events:         map[*ir.EventDeclaration]string{},
		eventFields:    map[*ir.EventField]string{},
		indicatorTones: map[*ir.Indicator]string{},
		controlPressed: map[*ir.Control]string{},
	}

	// Members of the state type share one namespace: a struct field and a
	// method on the same type cannot have one name.
	members := newNamespace("the state type")
	for _, field := range document.StateFields {
		name, fieldError := exported(field.Name)
		if fieldError != nil {
			return nil, fieldError
		}
		if claimError := members.claim(name, field.Name); claimError != nil {
			return nil, claimError
		}
		derived.fields[field] = name
	}
	for _, predicate := range document.Predicates {
		// An atomic predicate is a flag field under its own name, so it is
		// already the struct field above and emits no method of its own.
		if predicate.Kind == ir.PredicateAtomic {
			derived.predicates[predicate] = derived.fields[predicate.Field]
			continue
		}
		name, predicateError := exported(predicate.Name)
		if predicateError != nil {
			return nil, predicateError
		}
		if claimError := members.claim(name, predicate.Name); claimError != nil {
			return nil, claimError
		}
		derived.predicates[predicate] = name
	}
	for _, binding := range document.Bindings {
		name, bindingError := exported(binding.Name)
		if bindingError != nil {
			return nil, bindingError
		}
		if claimError := members.claim(name, binding.Name); claimError != nil {
			return nil, claimError
		}
		derived.bindings[binding] = name
	}
	for _, label := range document.Labels {
		name, labelError := exported(label.Name)
		if labelError != nil {
			return nil, labelError
		}
		if claimError := members.claim(name, label.Name); claimError != nil {
			return nil, claimError
		}
		derived.labels[label] = name
	}

	// The derivations the document does not name, claimed in the same namespace
	// and only when the construct that needs them is present. Each is a method
	// on the state type, so each can collide with a field, a binding or a label
	// spelled the same way — and a collision reported here is a message naming
	// both sides, rather than a generated file templ blames for not compiling.
	if document.Motion != nil {
		for _, fixed := range []string{motionActiveName, motionActiveName + motionTextSuffix} {
			if claimError := members.claim(fixed, "the motion block"); claimError != nil {
				return nil, claimError
			}
		}
		derived.motionActiveFunc = motionActiveName
		derived.motionActiveText = motionActiveName + motionTextSuffix
		if document.Motion.RestartOn != nil {
			if claimError := members.claim(motionTickName, "the motion block's restartOn"); claimError != nil {
				return nil, claimError
			}
			if claimError := members.claim(motionTickName+instanceSuffix, "the motion block's restartOn"); claimError != nil {
				return nil, claimError
			}
			derived.motionTickFunc = motionTickName
			derived.motionTickAtFunc = motionTickName + instanceSuffix
		}
	}
	for _, indicator := range document.Indicators {
		name, indicatorError := exported(indicator.Name)
		if indicatorError != nil {
			return nil, indicatorError
		}
		if claimError := members.claim(name+toneSuffix, indicator.Name); claimError != nil {
			return nil, claimError
		}
		derived.indicatorTones[indicator] = name + toneSuffix
	}
	for _, control := range document.Controls {
		if control.PressedWhen == nil {
			continue
		}
		name, controlError := exported(control.Name)
		if controlError != nil {
			return nil, controlError
		}
		if claimError := members.claim(name+pressedSuffix, control.Name); claimError != nil {
			return nil, claimError
		}
		derived.controlPressed[control] = name + pressedSuffix
	}

	// Package-level identifiers, including the fixed ones the widget's own name
	// produces: a document whose event is called `name` would otherwise emit a
	// second NodeStatusName.
	packageLevel := newNamespace("the generated package")
	for _, fixed := range []string{
		derived.widgetType, derived.stateType, derived.viewFunc, derived.viewAtFunc,
		derived.constructor, derived.constructorAt,
		derived.nameConst, derived.regionConst, derived.paletteConst, derived.titleIDConst,
	} {
		if claimError := packageLevel.claim(fixed, document.Name); claimError != nil {
			return nil, claimError
		}
	}
	for _, event := range document.Events {
		name, eventError := exported(event.Name)
		if eventError != nil {
			return nil, eventError
		}
		constant := widgetType + "Event" + name
		if claimError := packageLevel.claim(constant, event.Name); claimError != nil {
			return nil, claimError
		}
		derived.events[event] = constant

		// One constant per payload field, named under its own event rather
		// than under the widget. Two events may legitimately carry a field of
		// the same wire name — a sequence, a cursor — and a per-widget name
		// would turn that document into a collision the author cannot fix
		// without renaming their wire.
		for _, field := range event.Fields {
			spelled, fieldError := exportedWire(field.WireName)
			if fieldError != nil {
				return nil, fieldError
			}
			fieldConstant := constant + "Field" + spelled
			if claimError := packageLevel.claim(fieldConstant, field.WireName); claimError != nil {
				return nil, claimError
			}
			derived.eventFields[field] = fieldConstant
		}
	}

	return derived, nil
}

// namespace records which Go identifier each document identifier claimed, so a
// collision names both sides rather than only the loser.
type namespace struct {
	where  string
	claims map[string][]string
}

func newNamespace(where string) *namespace {
	return &namespace{where: where, claims: map[string][]string{}}
}

// claim records that a document identifier emits a Go identifier, reporting a
// collision with whatever claimed it first.
func (space *namespace) claim(goName, documentName string) error {
	if existing, taken := space.claims[goName]; taken {
		sources := append(append([]string{}, existing...), documentName)
		sort.Strings(sources)
		return fmt.Errorf("%w: %s in %s, from %s",
			ErrNameCollision, goName, space.where, strings.Join(sources, " and "))
	}
	space.claims[goName] = []string{documentName}
	return nil
}

// exported returns a document identifier's exported Go spelling.
func exported(name string) (string, error) {
	runes := []rune(name)
	if len(runes) == 0 || !unicode.IsLetter(runes[0]) {
		return "", fmt.Errorf("%w: %q", ErrUnexportable, name)
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes), nil
}

// exportedWire returns a wire field name's exported Go spelling.
//
// It is a second spelling function rather than a case of the first because the
// two namespaces are spelled differently and the dialect says so: a document
// identifier is lowerCamelCase and a wire field name is lower_snake_case, which
// is the rule the language holds rather than a convention this generator
// invented. Each underscore-separated segment contributes its first rune in
// upper case, so `alive_voters` names AliveVoters — and two document names that
// collapse to one Go identifier are caught by the same namespace claim every
// other identifier goes through.
func exportedWire(name string) (string, error) {
	runes := []rune(name)
	if len(runes) == 0 || !unicode.IsLetter(runes[0]) {
		return "", fmt.Errorf("%w: %q", ErrUnexportable, name)
	}
	spelled := &strings.Builder{}
	for _, segment := range strings.Split(name, "_") {
		if segment == "" {
			continue
		}
		segmentRunes := []rune(segment)
		segmentRunes[0] = unicode.ToUpper(segmentRunes[0])
		spelled.WriteString(string(segmentRunes))
	}
	return spelled.String(), nil
}
