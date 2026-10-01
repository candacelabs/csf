package webui

import (
	"errors"
	"fmt"
	"html/template"
	"strings"
	"unicode"
)

// DefaultProductName and DefaultAgentName are the stock CandaceOS identity.
// They are the fallbacks the browser client also carries, so a page rendered
// without an explicit brand reads exactly as it always has.
const (
	DefaultProductName = "CandaceOS"
	DefaultAgentName   = "Claw"
)

// DefaultWordmark is the stock CandaceOS lockup: the rotated diamond mark
// followed by the two-tone product name. Its classes are styled by the
// embedded app.css.
const DefaultWordmark = template.HTML(
	`<span class="brand-mark" aria-hidden="true"><span></span><span></span><span></span></span>` +
		`<span>Candace<span class="brand-os">OS</span></span>`,
)

// maxBrandNameLength bounds the two brand-bearing strings. They appear in page
// titles, aria-labels, and inline sentences, so a name long enough to reflow
// the shell is a configuration mistake rather than a supported brand.
const maxBrandNameLength = 64

// ErrInvalidBrandName reports a product or agent name that is not renderable
// plain text.
var ErrInvalidBrandName = errors.New("webui: invalid brand name")

// Brand is the identity this package renders. It is the whole of what an
// embedding product replaces: the two brand-bearing strings, the lockup markup,
// and the design tokens the stylesheet reads.
//
// The zero Brand is the stock CandaceOS identity: every unset field falls back
// to its DefaultBrand value, so a caller may name only what it wants to change.
type Brand struct {
	// ProductName is the system's name — the page title, the operator-facing
	// subject of the UI's sentences, and the snapshot's System.Name default.
	ProductName string

	// AgentName is the name of the agent that acts for the operator.
	AgentName string

	// Wordmark is the lockup markup rendered inside the sidebar and chat brand
	// links.
	//
	// SECURITY: this value is operator-trusted markup. It is emitted into the
	// page verbatim, without escaping, exactly like a template the operator
	// wrote. Never populate it from a browser request, a fleet node, an agent,
	// or any other untrusted source; a hostile fragment can carry arbitrary
	// markup. It cannot smuggle script past the page's Content-Security-Policy,
	// which permits only same-origin scripts, but it can still restyle or
	// deface the shell. Supply a fragment reviewed in the same way as the
	// embedded templates.
	//
	// An unset Wordmark falls back to the stock lockup when no ProductName was
	// supplied either, and to the escaped ProductName otherwise, so naming a
	// product without drawing one never leaves the shell wearing another
	// product's mark.
	Wordmark template.HTML

	// Palette overrides the stylesheet's :root design tokens. Unset entries
	// keep the embedded app.css value.
	Palette Palette
}

// DefaultBrand returns the stock CandaceOS identity: the shipped names, the
// shipped wordmark, and no palette overrides.
func DefaultBrand() Brand {
	return Brand{
		ProductName: DefaultProductName,
		AgentName:   DefaultAgentName,
		Wordmark:    DefaultWordmark,
	}
}

// Validate reports whether the supplied fields are usable. Unset fields are
// valid: they resolve to their DefaultBrand values.
func (b Brand) Validate() error {
	return errors.Join(
		validateBrandName("product name", b.ProductName),
		validateBrandName("agent name", b.AgentName),
		b.Palette.Validate(),
	)
}

// Resolved returns the brand with every unset field filled from DefaultBrand
// and the names trimmed. Composition roots that hold one Brand and hand it to
// several packages should resolve it once, so each of them renders the same
// identity rather than repeating the fallbacks.
func (b Brand) Resolved() Brand {
	resolved := Brand{
		ProductName: strings.TrimSpace(b.ProductName),
		AgentName:   strings.TrimSpace(b.AgentName),
		Wordmark:    b.Wordmark,
		Palette:     b.Palette,
	}
	named := resolved.ProductName != ""
	if !named {
		resolved.ProductName = DefaultProductName
	}
	if resolved.AgentName == "" {
		resolved.AgentName = DefaultAgentName
	}
	if resolved.Wordmark == "" {
		resolved.Wordmark = DefaultWordmark
		if named {
			resolved.Wordmark = template.HTML(template.HTMLEscapeString(resolved.ProductName))
		}
	}
	return resolved
}

func validateBrandName(field, value string) error {
	return validatePlainText(ErrInvalidBrandName, field, value, maxBrandNameLength)
}

// validatePlainText reports whether value is bounded, renderable plain text.
// It is the one check behind every operator-supplied string this package emits
// into a page — brand names and navigation text alike — so a caller cannot
// find a field that quietly accepts a control character or an unbounded run.
// An empty value is valid: absence is how a caller keeps a shipped default.
func validatePlainText(sentinel error, field, value string, maxRunes int) error {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return nil
	}
	if len([]rune(trimmed)) > maxRunes {
		return fmt.Errorf("%w: %s is longer than %d characters", sentinel, field, maxRunes)
	}
	for _, character := range trimmed {
		if unicode.IsControl(character) {
			return fmt.Errorf("%w: %s contains a control character", sentinel, field)
		}
	}
	return nil
}
