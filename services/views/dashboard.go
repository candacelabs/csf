// Copyright 2026 Candace Labs

package views

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

const (
	// DashboardFile is the dashboard's path in this package, which Grafana
	// is provisioned from and the metric panel check reads.
	DashboardFile = "dashboard/csf.json"
	// DashboardUID is the dashboard's Grafana identifier, which panel links
	// address.
	DashboardUID = "csf"
	// panelRow is the type of a dashboard row, which holds panels and
	// queries nothing.
	panelRow = "row"
)

// metricExpression finds the CSF metric names a panel's query reads: every
// family CSF exports starts with csf_.
var metricExpression = regexp.MustCompile(`\bcsf_[a-z0-9_]+`)

//go:embed dashboard/csf.json
var dashboardDocument []byte

// Dashboard is the part of the Grafana dashboard model the check and the
// panel links read.
type Dashboard struct {
	UID    string  `json:"uid"`
	Title  string  `json:"title"`
	Panels []Panel `json:"panels"`
}

// Panel is one dashboard panel, or a row.
type Panel struct {
	ID          int    `json:"id"`
	Type        string `json:"type"`
	Title       string `json:"title"`
	Description string `json:"description"`
	FieldConfig struct {
		Defaults struct {
			Unit string `json:"unit"`
		} `json:"defaults"`
	} `json:"fieldConfig"`
	Targets []struct {
		Expr string `json:"expr"`
	} `json:"targets"`
}

// Metrics is every CSF metric the panel's queries read, once each, in the
// order they first appear.
func (panel Panel) Metrics() []string {
	var names []string
	for _, target := range panel.Targets {
		for _, name := range metricExpression.FindAllString(target.Expr, -1) {
			if !slices.Contains(names, name) {
				names = append(names, name)
			}
		}
	}
	return names
}

// ReadDashboard decodes a dashboard document.
func ReadDashboard(document []byte) (Dashboard, error) {
	var decoded Dashboard
	if err := json.Unmarshal(document, &decoded); err != nil {
		return Dashboard{}, fmt.Errorf("views: decode %s: %w", DashboardFile, err)
	}
	return decoded, nil
}

// DashboardDocument is the dashboard as the repository holds it, which the
// stack provisions Grafana with.
func DashboardDocument() []byte { return append([]byte(nil), dashboardDocument...) }

// PanelFor is the first panel that queries metric, or one of a histogram's
// series, in the embedded dashboard.
func PanelFor(metric string) (Panel, bool) {
	dashboard, err := ReadDashboard(dashboardDocument)
	if err != nil {
		return Panel{}, false
	}
	for _, panel := range dashboard.Panels {
		if slices.ContainsFunc(panel.Metrics(), func(series string) bool {
			family, found := catalog.LookupSeries(series)
			return series == metric || found && family.Name == metric
		}) {
			return panel, true
		}
	}
	return Panel{}, false
}

// CheckPanels is the metric panel check: every catalog family has a panel
// that queries it, every panel queries only catalog families, and every
// panel has a title, a unit and, in its description, the definition of each
// family it queries. It returns one finding per gap, empty when there is none.
func CheckPanels(catalog Catalog, dashboard Dashboard) []string {
	var findings []string
	paneled := map[string]bool{}
	for _, panel := range dashboard.Panels {
		if panel.Type == panelRow {
			continue
		}
		label := fmt.Sprintf("panel %d %q", panel.ID, panel.Title)
		if panel.Title == "" {
			findings = append(findings, label+" has no title")
		}
		if panel.FieldConfig.Defaults.Unit == "" {
			findings = append(findings, label+" has no unit")
		}
		names := panel.Metrics()
		if len(names) == 0 {
			findings = append(findings, label+" queries no CSF metric")
		}
		for _, name := range names {
			metric, found := catalog.LookupSeries(name)
			if !found {
				findings = append(findings, fmt.Sprintf("%s queries %s, which CSF does not export: add it to %s or drop the query", label, name, CatalogFile))
				continue
			}
			paneled[metric.Name] = true
			if !strings.Contains(panel.Description, metric.Definition) {
				findings = append(findings, fmt.Sprintf("%s does not carry the definition of %s in its description", label, name))
			}
		}
	}
	for _, metric := range catalog.Metrics {
		if !paneled[metric.Name] {
			findings = append(findings, fmt.Sprintf("%s has no panel: add one to %s in the same change", metric.Name, DashboardFile))
		}
	}
	return findings
}
