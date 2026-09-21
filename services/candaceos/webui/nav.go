package webui

import (
	"errors"
	"fmt"
	"html/template"
	"strings"

	candaceosv1 "github.com/candacelabs/csf/proto/candace/candaceos/v1"
)

// The built-in view names. Each names one <section data-view="…"> the shipped
// index page renders, and one sidebar entry that switches to it in place rather
// than navigating. They are exported so an embedding product can point an extra
// entry at a view it already knows, and so a test can name one without copying
// a string literal.
const (
	NavViewHome     = "home"
	NavViewApps     = "apps"
	NavViewFleet    = "fleet"
	NavViewActivity = "activity"
)

// The data attributes the browser client keeps in sync with the snapshot. Only
// the two built-in counted views carry one; a registered item is a plain link
// and carries no live count.
const (
	navAppCountAttribute  = template.HTMLAttr("data-app-count")
	navNodeCountAttribute = template.HTMLAttr("data-node-count")
)

// Navigation text bounds. A label long enough to reflow the sidebar, or a glyph
// that is really a sentence, is a configuration mistake rather than a supported
// navigation item.
const (
	maxNavLabelLength = 64
	maxNavGlyphLength = 16
	maxNavHrefLength  = 512
)

// ErrInvalidNavItem reports a navigation item that cannot be rendered as one
// labeled sidebar link.
var ErrInvalidNavItem = errors.New("webui: invalid navigation item")

// NavItem is one entry in the operator sidebar's primary navigation.
//
// The shipped entries are ordinary NavItems: WithNavItem appends to the same
// slice the defaults occupy, so a registered entry renders with the same
// markup, the same keyboard behavior, and the same aria semantics as Home,
// Apps, Fleet, and Activity.
type NavItem struct {
	// Label is the visible text of the link. It is required and is escaped
	// like any other page text.
	Label string

	// Href is the link target. It is required. A relative or same-document
	// target ("#apps", "/reports") is the normal case; the template's URL
	// filter neutralizes a scheme the browser must not follow.
	Href string

	// Glyph is the decorative mark rendered before the label. It is optional
	// and aria-hidden, so it carries no information the label does not.
	Glyph string

	// View optionally names an in-page section rendered as
	// <section data-view="…">. When it names one, the browser client switches
	// to that section in place and marks this entry current; when it is empty
	// or names no rendered section, the link is ordinary navigation. Only
	// letters, digits, hyphens, and underscores are accepted, so the name
	// stays usable as both a URL fragment and an attribute value.
	View string
}

// DefaultNavItems returns the shipped sidebar entries in render order: the four
// built-in views with their glyphs. Callers that want to reorder or drop an
// entry override the "primaryNav" template through WithUIOverlay; WithNavItem
// only appends.
func DefaultNavItems() []NavItem {
	return []NavItem{
		{Label: "Home", Href: "#" + NavViewHome, Glyph: "⌂", View: NavViewHome},
		{Label: "Apps", Href: "#" + NavViewApps, Glyph: "□", View: NavViewApps},
		{Label: "Fleet", Href: "#" + NavViewFleet, Glyph: "⌁", View: NavViewFleet},
		{Label: "Activity", Href: "#" + NavViewActivity, Glyph: "↺", View: NavViewActivity},
	}
}

// Validate reports whether the item can be rendered as one sidebar link.
func (n NavItem) Validate() error {
	if strings.TrimSpace(n.Label) == "" {
		return fmt.Errorf("%w: label is required", ErrInvalidNavItem)
	}
	if strings.TrimSpace(n.Href) == "" {
		return fmt.Errorf("%w: href is required", ErrInvalidNavItem)
	}
	return errors.Join(
		validatePlainText(ErrInvalidNavItem, "label", n.Label, maxNavLabelLength),
		validatePlainText(ErrInvalidNavItem, "href", n.Href, maxNavHrefLength),
		validatePlainText(ErrInvalidNavItem, "glyph", n.Glyph, maxNavGlyphLength),
		validateNavView(n.View),
	)
}

// trimmed returns the item with its surrounding whitespace removed, so two
// callers that differ only in padding register the same entry.
func (n NavItem) trimmed() NavItem {
	return NavItem{
		Label: strings.TrimSpace(n.Label),
		Href:  strings.TrimSpace(n.Href),
		Glyph: strings.TrimSpace(n.Glyph),
		View:  strings.TrimSpace(n.View),
	}
}

// navEntry is one rendered navigation link: the registered item plus the state
// only the server knows. It is deliberately unexported — the live counts and
// the current-entry marking are this package's rendering policy, not a knob.
type navEntry struct {
	NavItem

	// Active marks the entry the page opens on, which is the first entry that
	// names a view.
	Active bool

	// CountAttribute is empty unless this entry carries a live count; when it
	// is set it is the data attribute the browser client updates in place.
	CountAttribute template.HTMLAttr

	// Count is the server-rendered value behind CountAttribute.
	Count int
}

// navEntries binds the registered items to one snapshot. The first entry that
// names a view is the one the page opens on, which reproduces the shipped
// behavior of landing on Home.
func navEntries(items []NavItem, snapshot *candaceosv1.WebUISnapshot) []navEntry {
	entries := make([]navEntry, 0, len(items))
	marked := false
	for _, item := range items {
		entry := navEntry{NavItem: item}
		if !marked && item.View != "" {
			entry.Active = true
			marked = true
		}
		switch item.View {
		case NavViewApps:
			entry.CountAttribute = navAppCountAttribute
			entry.Count = len(snapshot.GetApps())
		case NavViewFleet:
			entry.CountAttribute = navNodeCountAttribute
			entry.Count = len(snapshot.GetFleet().GetNodes())
		}
		entries = append(entries, entry)
	}
	return entries
}

func validateNavView(view string) error {
	trimmed := strings.TrimSpace(view)
	if trimmed == "" {
		return nil
	}
	for index, character := range trimmed {
		switch {
		case character >= 'a' && character <= 'z',
			character >= 'A' && character <= 'Z':
		case index > 0 && (character >= '0' && character <= '9' ||
			character == '-' || character == '_'):
		default:
			return fmt.Errorf(
				"%w: view must start with a letter and hold only letters, digits, hyphens, and underscores",
				ErrInvalidNavItem,
			)
		}
	}
	return nil
}
