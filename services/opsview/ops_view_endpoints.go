// Copyright 2026 Candace Labs

package opsview

import (
	"context"
	"io"

	"github.com/a-h/templ"

	"github.com/candacelabs/csf/services/harness/endpoint"
)

// EndpointsRegion is the endpoints panel: every operator-facing endpoint of
// the endpoint registry at each of its addresses, and every retired address
// with where it moved, so the operator has one page that says where
// everything is.
const (
	EndpointsRegion   = "opsview.endpoints"
	endpointsTemplate = "endpoints"
)

// WithEndpoints grants the page the endpoint registry as csf serve registered
// it at start. The registry changes only when a csf serve starts or the
// operator retires an address before restarting one, so the panel is drawn
// from this value and never re-read.
func WithEndpoints(registry endpoint.Registry) Option {
	return func(view *OpsView) error {
		view.endpoints = registry
		return nil
	}
}

// endpointsView is the panel's data.
type endpointsView struct {
	Region      string
	Endpoints   []endpointRow
	Retirements []endpoint.Retirement
}

// endpointRow is one endpoint at one of its addresses.
type endpointRow struct {
	Name        string
	URL         string
	Via         string
	RedirectsTo string
	Users       string
	Since       string
}

// renderEndpoints draws the panel from the registry the page was granted.
func (view *OpsView) renderEndpoints(_ viewState) templ.Component {
	panel := endpointsView{Region: EndpointsRegion, Retirements: view.endpoints.Retirements}
	for _, served := range view.endpoints.Active() {
		for _, address := range served.Addresses {
			panel.Endpoints = append(panel.Endpoints, endpointRow{Name: served.Name, URL: served.URL(address.Address), Via: address.Via,
				RedirectsTo: address.RedirectsTo, Users: served.Users, Since: address.RegisteredOn})
		}
	}
	return templ.ComponentFunc(func(ctx context.Context, writer io.Writer) error {
		return views.ExecuteTemplate(writer, endpointsTemplate, panel)
	})
}

// endpointsUnchanged is the panel's dirty check: its data is fixed for the
// page's life.
func endpointsUnchanged(_ viewState, _ viewState) bool { return false }
