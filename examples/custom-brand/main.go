// Command custom-brand is CandaceOS Core wearing another product's identity.
//
// Harborlight is invented for this example. It is not a real product, company,
// or service, and it exists only to show how far the operator UI bends without
// forking it: a different name, a different agent, a different mark, a
// different palette, an extra sidebar entry, and a page of the embedding
// product's own — all of it through the composition root's public options, with
// no template of Core's edited and no route of Core's moved.
//
// # The seams
//
// Everything this example changes is one option in seams:
//
//	bootstrap.WithBrand        the two brand-bearing strings, the wordmark,
//	                           and the design tokens the stylesheet reads
//	bootstrap.WithUIOverlay    one filesystem resolved before the embedded
//	                           one; here it carries the wordmark's glyph
//	bootstrap.WithNavItem      one sidebar entry, after Core's own four
//	bootstrap.WithHTTPService  the page that entry links to
//
// Core keeps everything else: its routes, including the /claws/... paths, its
// snapshot contract, its API, and every string in the UI that does not name the
// product or the agent. See identity.go for what each value is and README.md
// for why it is shaped that way.
//
// # Running it
//
// This is a complete Core, so it needs what Core needs — a PostgreSQL URL, a
// writable data directory and workspace, a Warden URL, and a harness selection.
// It reads exactly the same configuration as the stock command; nothing here
// adds a setting of its own.
//
//	go run ./examples/custom-brand
//
// The rebranded shell is then at Core's usual bind address, and the extra page
// is at /harbor-log.
package main

import (
	"fmt"
	"os"

	"github.com/candacelabs/csf/app/candaceos-core/bootstrap"
)

// version is what Core reports as its build. A real embedding binary stamps
// this at link time; the value matters only because Core renders it in the
// sidebar, which is one more thing this example does not have to invent.
var version = "custom-brand-example"

func main() {
	if err := bootstrap.Run(version, seams()...); err != nil {
		fmt.Fprintln(os.Stderr, "custom-brand:", err)
		os.Exit(1)
	}
}

// seams is the whole of this example's configuration, in the order the UI
// applies it: identity first, then the overlay that supplies the mark the
// identity points at, then the sidebar entry, then the service that answers it.
//
// It is a function rather than a literal inside main so that the smoke test can
// assert on the same values Core is handed, rather than on a second copy of
// them written to be asserted on.
func seams() []bootstrap.Option {
	return []bootstrap.Option{
		bootstrap.WithBrand(brand()),
		bootstrap.WithUIOverlay(overlayTree),
		bootstrap.WithNavItem(sidebarEntry()),
		bootstrap.WithHTTPService(harborLog{}),
	}
}
