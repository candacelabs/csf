// Package identity is this repository's own product identity: the two
// brand-bearing names, the lockup rendered in the shell, the design tokens the
// operator stylesheet reads, and the one shipped template block its overlay
// redefines.
//
// Quillfern is invented for this example. It is not a real product, company, or
// service; it exists so that the seams a rebrand goes through are exercised
// from a repository that only ever sees candace as a downloaded archive.
//
// Nothing here knows what the product's features are. The composition root
// hands this identity both to Core and to the service that serves the extra
// page, so the shell and that page cannot disagree about who they are.
package identity

import (
	"embed"
	"html/template"
	"io/fs"

	"github.com/candacelabs/csf/services/candaceos/webui"
)

// The two brand-bearing strings. They are the only UI copy the brand seam makes
// data: the product name replaces the shipped one in titles, aria-labels, and
// the sentences that name the system, and the agent name replaces the shipped
// one where the UI names the thing acting for the operator.
const (
	ProductName = "Quillfern"
	AgentName   = "Bramble"
)

// overlayRoot is the directory holding the overlay tree. The web UI resolves
// "templates/…" and "assets/…" from the root of the filesystem it is handed, so
// the embedded tree is re-rooted here rather than handed over one level deep.
const overlayRoot = "overlay"

//go:embed overlay
var overlayFiles embed.FS

// overlayTree is the embedded overlay rooted where the web UI expects it. The
// fs.Sub cannot fail — the directory is embedded on the line above, so a
// failure here would mean the binary was built without it.
var overlayTree = func() fs.FS {
	tree, err := fs.Sub(overlayFiles, overlayRoot)
	if err != nil {
		panic("identity: the embedded overlay has no " + overlayRoot + " root: " + err.Error())
	}
	return tree
}()

// Overlay returns the filesystem the operator UI resolves before its embedded
// one. This one carries a single file: templates/status_pill.html, redefining
// the shipped "statusPill" block so every status chip in the product also
// carries the raw status as a data attribute.
//
// That is the whole overlay. A name the overlay does not carry keeps shipping
// from Core, so replacing one block leaves every other template and every
// asset exactly as candace published them.
func Overlay() fs.FS { return overlayTree }

// Wordmark is the lockup rendered in the sidebar and on the chat page.
//
// It is markup, not text: the web UI emits it verbatim, exactly like a template
// the operator wrote, so it is written here as a reviewed constant of this
// program and is never assembled from a browser request, a fleet node, or an
// agent. It reuses the shipped lockup's own classes, which is why it needs no
// overlay asset at all: the mark's tiles are painted by the shipped stylesheet,
// and the second half of the name takes --brand-accent from the palette below.
const Wordmark = template.HTML( // #nosec G203 -- a reviewed constant fragment of this program.
	`<span class="brand-mark" aria-hidden="true"><span></span><span></span><span></span></span>` +
		`<span>Quill<span class="brand-os">fern</span></span>`)

// Brand is the identity Core is assembled with. Core reads it in two places —
// it stamps the two names into every snapshot it produces, and the web UI
// renders the wordmark and serves the palette — so both halves of the page
// agree about who they are without this repository saying it twice.
func Brand() webui.Brand {
	return webui.Brand{
		ProductName: ProductName,
		AgentName:   AgentName,
		Wordmark:    Wordmark,
		Palette:     palette(),
	}
}

// palette replaces the design tokens the shipped stylesheet declares on :root.
//
// The tokens are delivered as a generated same-origin stylesheet linked after
// app.css, so these declarations win without a CSS rebuild and without an
// inline style block; the page's style-src stays 'self'. Values are validated
// rather than escaped, because a custom property value is substituted as CSS:
// anything that could end the declaration, end the rule, open a comment, or
// fetch a remote resource fails assembly instead of reaching a page.
//
// Only the tokens this identity changes are set, so the shadows, the radii, and
// the monospace stack are absent here rather than copied.
func palette() webui.Palette {
	return webui.Palette{
		// Surfaces: a warm paper rather than the shipped one.
		Canvas:     "#f6f4ee",
		CanvasDeep: "#ebe7dc",
		Card:       "#fdfcf8",
		CardStrong: "#ffffff",

		// Text.
		Ink:     "#1d2119",
		InkSoft: "#3c4436",
		Muted:   "#6b7263",
		Faint:   "#a3a897",

		// The sidebar sits on --forest rather than on --canvas, so its text
		// color is its own token rather than a shade of --ink.
		SidebarInk: "#e8f0e2",

		// Rules and separators.
		Line:       "#ddd9cc",
		LineStrong: "#c7c2b1",

		// Accents. --forest paints the sidebar and every strong surface, so it
		// carries most of the rebrand; --brand-accent is the second half of the
		// wordmark above, which is the only place the shipped lockup markup
		// reads it.
		Forest:      "#24402c",
		ForestLight: "#365c40",
		Mint:        "#e0ebd9",
		MintStrong:  "#a9cf9e",
		BrandAccent: "#b8d98a",

		// Status colors, kept in the same green/amber/red roles the UI reads
		// them in. A palette that recolored "failed" to something calm would be
		// a rebrand that lies.
		Green:           "#2f6b45",
		Amber:           "#8a6410",
		AmberBackground: "#f7efd9",
		AmberLine:       "#e3d3a4",
		Red:             "#a33a30",
		RedBackground:   "#f8e6e3",
		Blue:            "#2f5f8f",
	}
}
