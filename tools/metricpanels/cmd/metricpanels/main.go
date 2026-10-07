// Copyright 2026 Candace Labs

// Command metricpanels is the metric/panel check on the merge path: no metric
// without a panel, no panel without a metric. It reads the catalog of every
// family csf serve exports and the CSF dashboard from the tree at -root and
// prints one line per gap ([views.CheckPanels]). Exit 0 passes, 1 is a gap,
// 2 a file it could not read.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/candacelabs/csf/services/views"
)

const (
	viewsDirectory = "services/views"
	exitGap        = 1
	exitUnread     = 2
)

func main() {
	root := flag.String("root", ".", "checkout whose catalog and dashboard are checked")
	flag.Parse()
	catalogDocument, err := os.ReadFile(filepath.Join(*root, viewsDirectory, views.CatalogFile))
	if err != nil {
		fail(err)
	}
	dashboardDocument, err := os.ReadFile(filepath.Join(*root, viewsDirectory, views.DashboardFile))
	if err != nil {
		fail(err)
	}
	catalog, err := views.ReadCatalog(catalogDocument)
	if err != nil {
		fail(err)
	}
	dashboard, err := views.ReadDashboard(dashboardDocument)
	if err != nil {
		fail(err)
	}
	findings := views.CheckPanels(catalog, dashboard)
	for _, finding := range findings {
		fmt.Println(finding)
	}
	if len(findings) > 0 {
		fmt.Fprintf(os.Stderr, "metricpanels: %d gap(s) between %s and %s\n", len(findings), views.CatalogFile, views.DashboardFile)
		os.Exit(exitGap)
	}
	fmt.Printf("metricpanels: passed: %d metrics, each on a panel; every panel queries a catalog metric\n", len(catalog.Metrics))
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "metricpanels:", err)
	os.Exit(exitUnread)
}
