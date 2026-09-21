// Command custom-ui-page is stock CandaceOS Core with one page added.
//
// It is the smallest useful shape of the operator UI's extension seams, and it
// is deliberately not a rebrand: the identity, the palette, the templates, and
// every string in the shipped pages are exactly Core's. Two options are all it
// takes to put a page of your own inside that shell.
//
//	bootstrap.WithNavItem      one entry in the operator sidebar
//	bootstrap.WithHTTPService  the page that entry links to
//
// The entry renders with the same markup, keyboard behavior, and aria semantics
// as the shipped Home, Apps, Fleet, and Activity, and it lands after them. The
// page is mounted on the same Gin engine Core builds, so it inherits Core's
// security headers, and Core's own routes — including the /claws/... paths —
// are untouched.
//
// If you also want a different name, mark, or palette, examples/custom-brand
// adds bootstrap.WithBrand and bootstrap.WithUIOverlay to these two.
//
// # Running it
//
// This is a complete Core and needs what Core needs: a PostgreSQL URL, a
// writable data directory and workspace, a Warden URL, and a harness
// selection. Nothing here adds a setting of its own.
//
//	go run ./examples/custom-ui-page
package main

import (
	"fmt"
	"os"

	"github.com/candacelabs/csf/app/candaceos-core/bootstrap"
	"github.com/candacelabs/csf/services/candaceos/webui"
)

// version is what Core reports as its build. A real embedding binary stamps
// this at link time.
var version = "custom-ui-page-example"

// entry is the sidebar link this example adds.
//
// Label and Href are the only required fields. Glyph is decorative and
// aria-hidden, so it carries nothing the label does not. View is left unset,
// which makes this a plain link: the built-in entries name a section the
// operator page renders and switch to it in place, and there is no such section
// for a page served from somewhere else.
//
// Href is the same constant the service registers below, because nothing checks
// that a sidebar entry and a route agree.
var entry = webui.NavItem{Label: "Runbooks", Href: runbookPath, Glyph: "▤"}

func main() {
	err := bootstrap.Run(version,
		bootstrap.WithNavItem(entry),
		bootstrap.WithHTTPService(runbooks{}),
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, "custom-ui-page:", err)
		os.Exit(1)
	}
}
