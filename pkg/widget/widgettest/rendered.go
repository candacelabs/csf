package widgettest

import (
	"regexp"
	"strings"
)

// Rendered is one widget's live region as a viewer receives it.
//
// Every method is a question a card assertion actually asks. None of them
// builds a document tree: see the package comment for why that is a boundary
// rather than an omission.
type Rendered string

// rootElement matches the tag name of the first element in a fragment.
var rootElement = regexp.MustCompile(`^<([A-Za-z][A-Za-z0-9-]*)`)

// classAttribute matches one class attribute's whole value.
var classAttribute = regexp.MustCompile(`class="([^"]*)"`)

// identityAttribute matches one id attribute's value.
var identityAttribute = regexp.MustCompile(`id="([^"]*)"`)

// labelledByAttribute matches one aria-labelledby attribute's value.
var labelledByAttribute = regexp.MustCompile(`aria-labelledby="([^"]*)"`)

// String is the markup itself, for a failure message that has to show it.
func (rendered Rendered) String() string { return string(rendered) }

// Has reports whether the markup contains a fragment.
func (rendered Rendered) Has(fragment string) bool {
	return strings.Contains(string(rendered), fragment)
}

// Count is how many times a fragment appears.
//
// It is separate from [Rendered.Has] because "exactly once" is the assertion a
// region identity needs: a card that named its own region twice would patch
// correctly and be impossible to address.
func (rendered Rendered) Count(fragment string) int {
	return strings.Count(string(rendered), fragment)
}

// InOrder reports whether every fragment appears, each after the one before it.
//
// Declaration order is a promise the widget language makes — stats, legend
// entries and indicators all render in the order the document declares them —
// and a set of independent Has assertions cannot see it.
func (rendered Rendered) InOrder(fragments ...string) bool {
	remaining := string(rendered)
	for _, fragment := range fragments {
		at := strings.Index(remaining, fragment)
		if at < 0 {
			return false
		}
		remaining = remaining[at+len(fragment):]
	}
	return true
}

// Elements is how many elements carry a class, matched as a whole class token
// rather than as a substring.
//
// The distinction is the whole reason this is not a Count: "widget-pulse"
// appears inside "widget-pulse-forward", so a substring count of a class name
// reports one element as two or three.
func (rendered Rendered) Elements(class string) int {
	found := 0
	for _, attribute := range classAttribute.FindAllStringSubmatch(string(rendered), -1) {
		for _, token := range strings.Fields(attribute[1]) {
			if token == class {
				found++
				break
			}
		}
	}
	return found
}

// Landmark is what a screen-reader user finds when they jump to this widget.
type Landmark struct {
	// Element is the root element's tag name.
	Element string

	// LabelledBy is the root's aria-labelledby, or empty when it carries none.
	LabelledBy string

	// Named reports whether some element in the fragment carries the id
	// LabelledBy names. Both halves are required rather than decorative:
	// HTML-AAM maps a nameless <aside> inside sectioning content to `generic`,
	// so a landmark with no accessible name is not a landmark.
	Named bool
}

// Landmark reads the fragment's root element and its accessible name.
//
// The second return is false for markup with no element at all, which is what a
// widget whose render produced nothing looks like — a case worth telling apart
// from a root that simply has no label.
func (rendered Rendered) Landmark() (Landmark, bool) {
	markup := strings.TrimSpace(string(rendered))
	root := rootElement.FindStringSubmatch(markup)
	if root == nil {
		return Landmark{}, false
	}

	landmark := Landmark{Element: root[1]}
	if labelledBy := labelledByAttribute.FindStringSubmatch(markup); labelledBy != nil {
		landmark.LabelledBy = labelledBy[1]
	}
	if landmark.LabelledBy == "" {
		return landmark, true
	}
	for _, identity := range identityAttribute.FindAllStringSubmatch(markup, -1) {
		if identity[1] == landmark.LabelledBy {
			landmark.Named = true
			break
		}
	}
	return landmark, true
}

// Pulses is how many pulse elements the scene carries.
//
// A pulse is emitted whether or not the motion gate is open — the gate is the
// root's data-motion attribute and the stylesheet reads it — so this counts the
// scene's declared motion rather than what is currently moving. A card
// assertion that means "nothing is animating" reads [Rendered.MotionOpen].
func (rendered Rendered) Pulses() int { return rendered.Elements("widget-pulse") }

// MotionOpen reports whether the motion gate is open on this render.
func (rendered Rendered) MotionOpen() bool { return rendered.Has(`data-motion="true"`) }
