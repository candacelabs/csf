package main

import (
	"embed"
	"fmt"
	"html/template"
	"io/fs"

	"github.com/candacelabs/csf/services/candaceos/browserroutes"
	"github.com/candacelabs/csf/services/candaceos/webui"
)

// The two brand-bearing strings. These are the only pieces of UI copy the seam
// makes data: the product name replaces "CandaceOS" in titles, aria-labels, and
// the sentences that name the system, and the agent name replaces "Claw" where
// the UI names the thing acting for the operator. Every other string in the
// shipped pages stays literal, which is why a rebrand is this small.
const (
	productName = "Harborlight"
	agentName   = "Skiff"
)

// glyphAsset is the one file this example's overlay adds. It is named here
// rather than spelled into the wordmark markup because two things must agree on
// it: the embedded file below and the URL the mark is fetched from.
const glyphAsset = "wordmark.svg"

// overlayRoot is the directory holding the overlay tree. The web UI resolves
// "templates/…" and "assets/…" from the root of the filesystem it is given, so
// the embedded tree is re-rooted here rather than shipped one level deep.
const overlayRoot = "overlay"

//go:embed overlay
var overlayFiles embed.FS

// overlayTree is the embedded overlay rooted where the web UI expects it. The
// fs.Sub cannot fail — the directory is embedded on the line above, so a
// failure here would mean the binary was built without it.
var overlayTree = func() fs.FS {
	tree, err := fs.Sub(overlayFiles, overlayRoot)
	if err != nil {
		panic("custom-brand: the embedded overlay has no " + overlayRoot + " root: " + err.Error())
	}
	return tree
}()

// wordmark is the lockup rendered in the sidebar and on the chat page.
//
// It is markup, not text: the web UI emits it verbatim, exactly like a template
// the operator wrote, so it is written here as a reviewed constant of this
// program and never assembled from anything a request, a node, or an agent
// supplied. The page's Content-Security-Policy is untouched by it — script-src
// and style-src stay 'self', so this fragment cannot carry a script, and it
// cannot carry an inline style either. That is the reason it is an <img> with
// width and height attributes rather than a styled <span>: presentational
// attributes are markup, an inline style block would simply not apply.
//
// The mark's URL comes from browserroutes rather than a literal, because the
// asset URL space belongs to that package and an overlay asset lives in exactly
// the same space as an embedded one.
//
// The same fragment is rendered on the dark sidebar and on the light chat
// topbar, and the lettering inherits each surface's color from the shipped
// stylesheet. A glyph for both therefore has to read on both, which is why this
// one is a mid-tone blue rather than the near-white a sidebar-only mark could
// use.
var wordmark = template.HTML(fmt.Sprintf( // #nosec G203 -- a reviewed constant fragment of this program.
	`<img src=%q alt="" width="18" height="18"><span>%s</span>`,
	browserroutes.AssetPath(glyphAsset), productName,
))

// brand is the identity Core is assembled with. Core reads it in two places —
// it stamps the two names into every snapshot it produces, and the web UI
// renders the wordmark and serves the palette — so both sides of the page agree
// about who they are without this program saying it twice.
func brand() webui.Brand {
	return webui.Brand{
		ProductName: productName,
		AgentName:   agentName,
		Wordmark:    wordmark,
		Palette:     palette(),
	}
}

// palette replaces the design tokens the shipped stylesheet declares on :root.
//
// It is delivered as a generated same-origin stylesheet linked after app.css,
// so these declarations win without a CSS rebuild and without an inline style
// block; the page's style-src stays 'self'. Values are validated rather than
// escaped, because a custom property value is substituted as CSS: anything that
// could end the declaration, end the rule, open a comment, or fetch a remote
// resource fails assembly instead of reaching a page.
//
// Only the tokens this identity actually changes are set. An unset token keeps
// its shipped value, which is why the shadows, the radii, and the monospace
// stack are absent here rather than copied.
func palette() webui.Palette {
	return webui.Palette{
		// Surfaces: a cooler paper than the shipped warm one.
		Canvas:     "#f2f4f8",
		CanvasDeep: "#e5eaf2",
		Card:       "#fbfcfe",
		CardStrong: "#ffffff",

		// Text.
		Ink:     "#101a2c",
		InkSoft: "#33415c",
		Muted:   "#5a6b88",
		Faint:   "#9aa7bd",

		// Rules and separators.
		Line:       "#d8dee9",
		LineStrong: "#c3ccdc",

		// Accents. --forest paints the sidebar and every strong surface, so it
		// is the token that carries most of the rebrand.
		Forest:      "#12304f",
		ForestLight: "#1c4670",
		Mint:        "#dbe9f6",
		MintStrong:  "#8fc7e8",

		// Status colors, kept in the same green/amber/red roles the UI reads
		// them in. A palette that recolored "failed" to something calm would be
		// a rebrand that lies.
		Green:           "#1f7a63",
		Amber:           "#96660f",
		AmberBackground: "#fbf1dd",
		AmberLine:       "#e8d3a1",
		Red:             "#a83a33",
		RedBackground:   "#fae7e4",
		Blue:            "#2b6cb0",
	}
}

// sidebarEntry is the one navigation item this example adds, after Core's own
// Home, Apps, Fleet, and Activity. It renders with the same markup, keyboard
// behavior, and aria semantics as those four.
//
// It carries no View, so it is a plain link rather than an in-page view switch:
// nothing on the operator page renders a section by that name. The Href is the
// same constant the service registers, because nothing checks that a sidebar
// entry and a route agree — the only thing that can keep them in step is that
// there is one of them.
func sidebarEntry() webui.NavItem {
	return webui.NavItem{Label: "Harbor Log", Href: harborLogPath, Glyph: "⚓"}
}
