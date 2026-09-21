package widget

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/candacelabs/csf/pkg/widget/internal/ir"
)

// widgetCSS is everything a generated widget's markup depends on that is not a
// colour: the token classes, the scene's structure, the motion gate and the
// reduced-motion rule.
//
// It ships with the SDK rather than with each host because two of those are
// obligations rather than decoration. Nothing animates outside the motion gate
// and a viewer's reduced-motion preference is respected unconditionally
// (docs/ontology.md, Motion) — invariants that a host stylesheet could forget,
// and that a widget document has no spelling to override. The rest is here for
// the reason the class names are generated at all: a widget that adds a node, a
// stat, an edge or a pulse needs no new CSS.
//
//go:embed widget.css
var widgetCSS string

// Palette is one resolved mapping from the dialect's seven semantic token names
// to CSS values.
//
// A widget document writes a token name and never a value, so this is where the
// name becomes a colour, and it is the host that holds one: colour policy
// belongs to the design system, and a widget that could mint a colour would be a
// widget that could leave the design system (docs/ontology.md, Palette).
//
// The values are literals in this package's own source and there is no
// constructor that takes any, which is why nothing here validates one. A token
// value reaches a stylesheet as CSS rather than as text, so the moment a host
// can supply values, this type needs the validator that already exists for
// exactly that — reporting every failing entry rather than the first — and the
// honest move then is to centralize that one rather than to write a second.
type Palette struct {
	// name is the identifier a document's `palette` directive writes.
	name string

	// values are this palette's seven token values, read in the order
	// [TokenNames] fixes so that rendering is ordered by construction rather
	// than by sorting a map's keys at the point of use.
	values map[Token]string
}

// The one shipped palette, and the custom-property prefix every token resolves
// through.
//
// A widget renders under any palette that maps the seven names; there is one
// here because one is what the exemplars name, and a second palette that no
// document referenced would be vocabulary nobody uses.
const (
	// FieldStationPalette is the palette name the shipped exemplars declare. It
	// is the interpreter's own constant, because the closed set of palette
	// names is what the validator refuses a document against (W208) and one
	// name spelled in two places is one that eventually differs.
	FieldStationPalette = ir.PaletteFieldStation

	// tokenProperty prefixes each token's CSS custom property, so that a page
	// hosting a widget beside other components cannot collide with it.
	tokenProperty = "--widget-"
)

// fieldStation is the shipped palette: neutral values, dark-surface, chosen so
// the seven roles stay distinguishable rather than to match any one product.
var fieldStation = Palette{
	name: FieldStationPalette,
	values: map[Token]string{
		TokenSurface:  "#12161c",
		TokenInk:      "#e8edf4",
		TokenMuted:    "#93a1b4",
		TokenRule:     "#26303c",
		TokenAccent:   "#6aa9ff",
		TokenPositive: "#57c98a",
		TokenWarning:  "#e8b246",
	},
}

// FieldStation returns the shipped palette by name, for a host that knows which
// one it wants without asking.
func FieldStation() Palette { return fieldStation }

// PaletteByName resolves the name a document's `palette` directive wrote.
//
// An unknown name is reported rather than defaulted: a fallback palette renders
// a plausible-looking widget in the wrong colours, which is the failure mode
// hardest to notice in review (docs/dialect.md § 8).
func PaletteByName(name string) (Palette, bool) {
	if name == fieldStation.name {
		return fieldStation, true
	}
	return Palette{}, false
}

// TokenNames are the seven semantic token names, in palette order. It is the
// closed namespace a widget document writes into.
func TokenNames() []string { return ir.TokenNames() }

// Name is the identifier a document's `palette` directive writes.
func (palette Palette) Name() string { return palette.name }

// Value returns one token's CSS value, and whether this palette maps it. A
// palette that maps all seven is what makes a document portable; one that maps
// six renders a widget with a hole in it, so the caller is told rather than
// handed an empty string.
func (palette Palette) Value(token Token) (string, bool) {
	value, mapped := palette.values[token]
	return value, mapped
}

// Stylesheet is the whole of the CSS a generated widget's markup depends on: one
// palette's seven values as custom properties, followed by the token classes
// that read them, the scene's structure, the motion gate and the reduced-motion
// rule.
//
// It is one string rather than two calls because a host serving the structure
// without the tokens, or the tokens without the structure, has a widget that
// renders wrongly in a way no error reports.
func Stylesheet(palette Palette) string {
	declarations := &strings.Builder{}
	declarations.WriteString("/* The " + palette.name + " palette: the seven semantic token names, resolved. */\n")
	declarations.WriteString(":root {\n")
	for _, name := range TokenNames() {
		value, mapped := palette.Value(Token(name))
		if !mapped {
			continue
		}
		fmt.Fprintf(declarations, "  %s%s: %s;\n", tokenProperty, name, value)
	}
	declarations.WriteString("}\n\n")
	return declarations.String() + widgetCSS
}
