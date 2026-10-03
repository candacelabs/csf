package webui

import (
	"errors"
	"fmt"
	"strings"
	"unicode"
)

// maxPaletteValueLength bounds one token value. The longest shipped token is a
// two-layer box-shadow, so this leaves generous headroom while keeping a
// pathological value out of every page's stylesheet.
const maxPaletteValueLength = 256

// ErrInvalidPaletteValue reports a palette entry that is not a safe CSS token
// value. The wrapped message names the token and what disqualified it.
var ErrInvalidPaletteValue = errors.New("webui: invalid palette value")

// Palette overrides the design tokens the embedded stylesheet declares on
// :root. Every field is one CSS custom property; an empty field keeps the
// shipped value.
//
// Values are validated rather than escaped, because a custom property value is
// substituted into the stylesheet as CSS rather than as text. Validate rejects
// anything that could end the declaration, end the rule, open a comment, or
// reference an external resource, so one token cannot smuggle a second
// declaration or a network fetch into the page.
type Palette struct {
	// Surfaces.
	Canvas     string // --canvas
	CanvasDeep string // --canvas-deep
	Card       string // --card
	CardStrong string // --card-strong

	// Text.
	Ink     string // --ink
	InkSoft string // --ink-soft
	Muted   string // --muted
	Faint   string // --faint

	// SidebarInk is the text color of the operator sidebar, which sits on
	// Forest rather than on Canvas and therefore does not follow Ink.
	SidebarInk string // --sidebar-ink

	// Rules and separators.
	Line       string // --line
	LineStrong string // --line-strong

	// Accents.
	Forest      string // --forest
	ForestLight string // --forest-light
	Mint        string // --mint
	MintStrong  string // --mint-strong

	// BrandAccent is the accent the stock wordmark paints its second half
	// with. A Wordmark that does not use the shipped markup never renders it.
	BrandAccent string // --brand-accent

	// Status colors.
	Green           string // --green
	Amber           string // --amber
	AmberBackground string // --amber-bg
	AmberLine       string // --amber-line
	Red             string // --red
	RedBackground   string // --red-bg
	Blue            string // --blue

	// Elevation, shape, and type.
	Shadow      string // --shadow
	ShadowSmall string // --shadow-small
	Radius      string // --radius
	RadiusSmall string // --radius-small
	Mono        string // --mono
}

// paletteEntry is one token name bound to its configured value.
type paletteEntry struct {
	Token string
	Value string
}

// entries lists every token in stylesheet declaration order, including unset
// ones, so both validation and rendering read the same single inventory.
func (p Palette) entries() []paletteEntry {
	return []paletteEntry{
		{"--canvas", p.Canvas},
		{"--canvas-deep", p.CanvasDeep},
		{"--card", p.Card},
		{"--card-strong", p.CardStrong},
		{"--ink", p.Ink},
		{"--ink-soft", p.InkSoft},
		{"--muted", p.Muted},
		{"--faint", p.Faint},
		{"--sidebar-ink", p.SidebarInk},
		{"--line", p.Line},
		{"--line-strong", p.LineStrong},
		{"--forest", p.Forest},
		{"--forest-light", p.ForestLight},
		{"--mint", p.Mint},
		{"--mint-strong", p.MintStrong},
		{"--brand-accent", p.BrandAccent},
		{"--green", p.Green},
		{"--amber", p.Amber},
		{"--amber-bg", p.AmberBackground},
		{"--amber-line", p.AmberLine},
		{"--red", p.Red},
		{"--red-bg", p.RedBackground},
		{"--blue", p.Blue},
		{"--shadow", p.Shadow},
		{"--shadow-small", p.ShadowSmall},
		{"--radius", p.Radius},
		{"--radius-small", p.RadiusSmall},
		{"--mono", p.Mono},
	}
}

// Validate reports every entry that is not a safe CSS token value. An empty
// palette, and every empty entry within a palette, is valid.
func (p Palette) Validate() error {
	var failures []error
	for _, entry := range p.entries() {
		if err := validatePaletteValue(entry.Token, entry.Value); err != nil {
			failures = append(failures, err)
		}
	}
	return errors.Join(failures...)
}

// Stylesheet renders the configured tokens as one :root rule. It returns the
// empty string when no token is set, which is what the stock brand serves: the
// page still links the stylesheet, and the embedded app.css values stand.
//
// Callers must Validate first; Stylesheet renders whatever it is given.
func (p Palette) Stylesheet() string {
	var declarations strings.Builder
	for _, entry := range p.entries() {
		value := strings.TrimSpace(entry.Value)
		if value == "" {
			continue
		}
		fmt.Fprintf(&declarations, "  %s: %s;\n", entry.Token, value)
	}
	if declarations.Len() == 0 {
		return ""
	}
	return ":root {\n" + declarations.String() + "}\n"
}

// forbiddenPaletteSubstrings are the sequences that would let a token value
// escape its own declaration.
var forbiddenPaletteSubstrings = []struct {
	Sequence string
	Reason   string
}{
	{"{", "opens a rule"},
	{"}", "closes the rule"},
	{";", "ends the declaration"},
	{"<", "is markup"},
	{">", "is markup"},
	{"@", "opens an at-rule"},
	{"\\", "is a CSS escape"},
	{"/*", "opens a comment"},
	{"*/", "closes a comment"},
}

// forbiddenPaletteFunctions are the CSS functions that would make a token fetch
// or execute something instead of describing a value.
var forbiddenPaletteFunctions = []string{"url(", "image-set(", "expression("}

func validatePaletteValue(token, value string) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if len(trimmed) > maxPaletteValueLength {
		return fmt.Errorf(
			"%w: %s is longer than %d bytes", ErrInvalidPaletteValue, token, maxPaletteValueLength,
		)
	}
	for _, character := range trimmed {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: %s contains a control character", ErrInvalidPaletteValue, token)
		}
	}
	for _, forbidden := range forbiddenPaletteSubstrings {
		if strings.Contains(trimmed, forbidden.Sequence) {
			return fmt.Errorf(
				"%w: %s contains %q, which %s",
				ErrInvalidPaletteValue, token, forbidden.Sequence, forbidden.Reason,
			)
		}
	}
	folded := strings.ToLower(trimmed)
	for _, function := range forbiddenPaletteFunctions {
		if strings.Contains(folded, function) {
			return fmt.Errorf(
				"%w: %s references an external resource with %s)",
				ErrInvalidPaletteValue, token, function,
			)
		}
	}
	return balancedPaletteValue(token, trimmed)
}

// balancedPaletteValue rejects a value whose parentheses or quotes do not
// close. An unclosed group swallows the declarations that follow it, which is
// the same escape a bare brace would give.
func balancedPaletteValue(token, value string) error {
	depth := 0
	var quote rune
	for _, character := range value {
		switch {
		case quote != 0:
			if character == quote {
				quote = 0
			}
		case character == '"' || character == '\'':
			quote = character
		case character == '(':
			depth++
		case character == ')':
			depth--
			if depth < 0 {
				return fmt.Errorf("%w: %s closes a group it never opened", ErrInvalidPaletteValue, token)
			}
		}
	}
	if quote != 0 {
		return fmt.Errorf("%w: %s leaves a string unterminated", ErrInvalidPaletteValue, token)
	}
	if depth != 0 {
		return fmt.Errorf("%w: %s leaves a group unclosed", ErrInvalidPaletteValue, token)
	}
	return nil
}
