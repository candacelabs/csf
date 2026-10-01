package uigen

import (
	"errors"
	"fmt"
	"go/token"
	"os"
	"path/filepath"
	"strings"

	"github.com/candacelabs/csf/pkg/widget/internal/ir"
)

// Artifact is one generated file: a path relative to the output directory and
// its exact bytes.
type Artifact struct {
	// Path is relative to the output directory and is part of the generated
	// output: gen.sh compares it, so a renamed artifact is a diff rather than a
	// silently orphaned file.
	Path string

	// Data is the file's exact bytes.
	Data []byte
}

// Options are the decisions the document does not carry.
//
// A widget document names no path, no package and no import, by construction —
// that is what makes one publishable — so the one thing generation needs and
// the document cannot supply is where the emitted Go belongs.
type Options struct {
	// Package is the Go package name both emitted files declare.
	Package string
}

// ErrPackage is returned for an output package name Go could not compile.
var ErrPackage = errors.New("uigen: the output package name is not a Go identifier")

// ErrUnsound is returned for a document missing something the interpreter
// guarantees a sound one has.
//
// It is a refusal rather than a nil dereference. The IR is returned whether or
// not a document validated, so a caller that generated without reading the
// findings hands this package a partial document, and saying so is more use
// than a stack trace naming a field.
var ErrUnsound = errors.New("uigen: the document is not sound; refuse its findings before generating")

// UnsupportedError reports the constructs this generator does not yet emit. It
// names every one of them rather than the first, on the same argument the
// interpreter's findings are made on: an author who fixes one and re-runs to
// find the next learns the tool tells them a fraction of the truth.
type UnsupportedError struct {
	// Widget is the document's own name.
	Widget string
	// Constructs are the unsupported constructs, in the order [Refusals] lists
	// them.
	Constructs []string
}

func (unsupportedError *UnsupportedError) Error() string {
	return fmt.Sprintf("uigen: widget %s uses %s, which this generator does not emit yet",
		unsupportedError.Widget, strings.Join(unsupportedError.Constructs, ", "))
}

// Generate produces every artifact for one resolved document, without writing
// anything.
//
// The document must be sound: interpreting it must have produced no finding.
// Generating from an unsound document is generating from a guess, and this
// package has no way to tell the two apart — the IR is returned either way, so
// the caller holding the findings is the only one who can refuse.
func Generate(document *ir.Document, options Options) ([]Artifact, error) {
	if !token.IsIdentifier(options.Package) || token.IsKeyword(options.Package) {
		return nil, fmt.Errorf("%w: %q", ErrPackage, options.Package)
	}
	if constructs := unsupported(document); len(constructs) > 0 {
		return nil, &UnsupportedError{Widget: document.Name, Constructs: constructs}
	}
	if soundnessError := sound(document); soundnessError != nil {
		return nil, soundnessError
	}

	identifiers, namingError := newNames(document)
	if namingError != nil {
		return nil, namingError
	}

	scaffold, scaffoldError := emitScaffold(document, identifiers, options)
	if scaffoldError != nil {
		return nil, scaffoldError
	}

	return []Artifact{
		{Path: viewFileName, Data: emitView(document, identifiers, options)},
		{Path: scaffoldFileName, Data: scaffold},
	}, nil
}

// The two files one widget generates. The templ source comes first because it
// is the one a reader opens: the Go scaffold is plumbing, and the view is the
// widget.
const (
	viewFileName     = "view.templ"
	scaffoldFileName = "widget.gen.go"
)

// Write writes artifacts under directory, creating directories as needed.
func Write(directory string, artifacts []Artifact) error {
	for _, artifact := range artifacts {
		path := filepath.Join(directory, artifact.Path)
		if directoryError := os.MkdirAll(filepath.Dir(path), 0o755); directoryError != nil {
			return directoryError
		}
		if writeError := os.WriteFile(path, artifact.Data, 0o644); writeError != nil {
			return writeError
		}
	}
	return nil
}

// sound reports what a document is missing that the interpreter guarantees a
// validated one has, so a caller that skipped the findings is told which
// guarantee it skipped rather than shown a panic.
func sound(document *ir.Document) error {
	switch {
	case document.Name == "":
		return fmt.Errorf("%w: the document has no widget name", ErrUnsound)
	case document.Region == "":
		return fmt.Errorf("%w: widget %s declares no region", ErrUnsound, document.Name)
	case len(document.StateFields) == 0:
		return fmt.Errorf("%w: widget %s declares no state", ErrUnsound, document.Name)
	case document.Scene == nil:
		return fmt.Errorf("%w: widget %s declares no scene", ErrUnsound, document.Name)
	case document.Scene.DescriptionSlot == nil || document.Scene.DescriptionSlot.Label == nil:
		return fmt.Errorf("%w: widget %s leaves its scene description unfilled", ErrUnsound, document.Name)
	}
	if _, filled := document.Slot(ir.SlotTitle); !filled {
		return fmt.Errorf("%w: widget %s fills no title slot", ErrUnsound, document.Name)
	}
	return nil
}

// refusal is one construct this generator does not emit, together with the test
// for a document that uses it.
//
// It is a named function value in a list rather than another branch of one
// function so that the refusals can be *enumerated*: a spec asserts that every
// entry here fires on a document using it and that nothing else is refused, and
// this package's own documentation is generated from the same list rather than
// describing it from memory. A chain of ifs can be read; only data can be
// counted.
type refusal struct {
	// construct names what is refused, in the dialect's own vocabulary, so the
	// message names something an author can find in their document.
	construct string

	// applies reports whether one document uses it.
	applies func(document *ir.Document) bool
}

// refusals is every construct this generator refuses, in the order a message
// lists them.
//
// One entry per trigger rather than one for "a control this generator cannot
// draw", because the three are refused for one reason and an author fixing it
// needs to know which control. The reason: a control declares a caption, a
// trigger and an event, and nothing that says what kind of element it is. A
// click is a button and needs nothing more; a change or an input needs a form
// control the dialect has no way to describe, and a submit needs a form. Binding
// `change` to a button would emit a binding that can never fire — a silent
// no-op, which is worse than a refusal an author can read.
var refusals = []refusal{
	{construct: "a control with a change trigger", applies: controlTriggered(ir.TriggerChange)},
	{construct: "a control with an input trigger", applies: controlTriggered(ir.TriggerInput)},
	{construct: "a control with a submit trigger", applies: controlTriggered(ir.TriggerSubmit)},
}

// Refusals names every construct this generator refuses, in the order a message
// lists them. It is exported so that a caller can print what the generator will
// not do without provoking it into refusing.
func Refusals() []string {
	constructs := make([]string, 0, len(refusals))
	for _, entry := range refusals {
		constructs = append(constructs, entry.construct)
	}
	return constructs
}

// controlTriggered reports a document holding a control bound to one DOM
// trigger.
func controlTriggered(trigger ir.Trigger) func(document *ir.Document) bool {
	return func(document *ir.Document) bool {
		for _, control := range document.Controls {
			if control.Trigger == trigger {
				return true
			}
		}
		return false
	}
}

// unsupported returns the constructs in a document this generator does not emit.
func unsupported(document *ir.Document) []string {
	var constructs []string
	for _, entry := range refusals {
		if entry.applies(document) {
			constructs = append(constructs, entry.construct)
		}
	}
	return constructs
}
