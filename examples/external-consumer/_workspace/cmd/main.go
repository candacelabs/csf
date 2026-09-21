// Command custom-candaceos is this repository's own Core binary.
//
// It is a complete extending product, linked from a repository that has never
// seen candace's source tree: every candace package it uses arrives through
// @candace// labels resolved from one pinned archive. Nothing is loaded at
// runtime — Core's extension points are compile-time seams, and this binary is
// what choosing all of them at once looks like:
//
//	bootstrap.WithBrand           an invented product's name, agent, wordmark,
//	                              and design tokens
//	bootstrap.WithUIOverlay       one shipped template block, redefined
//	bootstrap.WithNavItem         one sidebar entry, after Core's own four
//	bootstrap.WithHTTPService     the page that entry links to
//	bootstrap.WithComponent       three components of this repository's own,
//	                              resolved in the order their edges imply
//	bootstrap.WithHarnessFactory  a full harness implementation compiled
//	                              outside the CandaceOS tree
//
// Core keeps everything else: its routes, including the /claws/... paths, its
// snapshot contract, its API, its persistence, and every string in the UI that
// does not name the product or the agent.
//
// # Running it
//
// This is a complete Core, so it needs what Core needs — a PostgreSQL URL, a
// writable data directory and workspace, a Warden URL, and a harness selection.
// It reads exactly the same configuration as the stock command; nothing here
// adds a setting of its own. The rebranded shell is then at Core's usual bind
// address and the extra page is at /field-notes.
package main

import (
	"fmt"
	"os"

	"github.com/candacelabs/csf/app/candaceos-core/bootstrap"

	"example.com/candace-external-consumer/composition"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "custom-candaceos:", err)
		os.Exit(1)
	}
}

// run keeps the composition and the process's exit policy apart, so a failure
// to compose reports the same way a failure to serve does.
func run() error {
	product, err := composition.New()
	if err != nil {
		return err
	}
	return bootstrap.Run(composition.Version, product.Options...)
}
