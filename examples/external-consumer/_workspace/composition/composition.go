// Package composition is this repository's composition root: the exact set of
// values handed to bootstrap.Run, assembled in one place so the binary and its
// suite cannot describe different products.
//
// It is a library rather than a main package for that reason alone. Everything
// in New is what an extending product's cmd/ would otherwise hold inline, and
// cmd/main.go does nothing this package does not.
package composition

import (
	"github.com/candacelabs/csf/app/candaceos-core/bootstrap"
	"github.com/candacelabs/csf/services/candaceos/component"

	"example.com/candace-external-consumer/customharness"
	"example.com/candace-external-consumer/identity"
	"example.com/candace-external-consumer/noteboard"
	"example.com/candace-external-consumer/steering"
)

// Version is what Core reports as its build. A real extending binary stamps
// this at link time; the value matters only because Core renders it in the
// sidebar.
const Version = "quillfern-console"

// Product is one assembled composition: the values behind the seams, and the
// option list that carries them.
type Product struct {
	// Components is this repository's bring-up graph in registration order:
	// the steering store, the steering service that requires it, and the
	// noteboard that requires the service. Core resolves the order; the edges
	// are declared here.
	Components []*component.Definition

	// Board is the service mounted through the HTTP seam and brought up by the
	// last of those components.
	Board *noteboard.Board

	// Options is the complete list handed to bootstrap.Run, in the order the
	// seams apply: identity, the overlay that redefines part of what renders
	// it, the sidebar entry, the page that entry links to, every component in
	// Components, and the agent harness.
	Options []bootstrap.Option
}

// New assembles the product. Every failure it can report — an invalid brand, an
// unrenderable sidebar entry, a malformed component — is reported here, before
// any infrastructure is opened.
func New() (*Product, error) {
	steeringStore, err := steering.StoreComponent()
	if err != nil {
		return nil, err
	}
	steeringService, err := steering.ServiceComponent(steeringStore)
	if err != nil {
		return nil, err
	}
	board, err := noteboard.New(steering.Instance(), identity.Brand())
	if err != nil {
		return nil, err
	}
	boardComponent, err := board.Component(steeringService)
	if err != nil {
		return nil, err
	}
	components := []*component.Definition{steeringStore, steeringService, boardComponent}

	// The component options are derived from Components rather than written out
	// beside it. A suite that asserts on the graph would prove nothing about
	// the binary if the binary registered a second, separately maintained list.
	options := []bootstrap.Option{
		bootstrap.WithBrand(identity.Brand()),
		bootstrap.WithUIOverlay(identity.Overlay()),
		bootstrap.WithNavItem(noteboard.NavItem()),
		bootstrap.WithHTTPService(board),
	}
	for _, definition := range components {
		options = append(options, bootstrap.WithComponent(definition))
	}
	options = append(options,
		bootstrap.WithHarnessFactory(customharness.NewFactory(steering.Instance())))

	return &Product{Components: components, Board: board, Options: options}, nil
}
