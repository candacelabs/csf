// Copyright 2026 Candace Labs

package views_test

import (
	"slices"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/candacelabs/csf/services/views"
)

func embedded() (views.Catalog, views.Dashboard) {
	dashboard, err := views.ReadDashboard(views.DashboardDocument())
	Expect(err).NotTo(HaveOccurred())
	return views.Catalog{Metrics: views.Metrics()}, dashboard
}

var _ = Describe("CheckPanels", func() {
	It("finds no gap between the catalog and the dashboard the repository holds", func() {
		Expect(views.CheckPanels(embedded())).To(BeEmpty())
	})

	It("fails on a metric no panel queries", func() {
		catalog, dashboard := embedded()
		catalog.Metrics = append(catalog.Metrics, views.Metric{Name: "csf_unpaneled_total", Type: views.Counter, Definition: "Nothing shows this."})
		Expect(views.CheckPanels(catalog, dashboard)).To(ConsistOf(
			"csf_unpaneled_total has no panel: add one to dashboard/csf.json in the same change"))
	})

	It("fails on a panel that queries a metric CSF does not export, and on one without a unit or the definition", func() {
		catalog, dashboard := embedded()
		dashboard.Panels[1].Targets[0].Expr = "sum(csf_nowhere_total)"
		dashboard.Panels[2].FieldConfig.Defaults.Unit = ""
		dashboard.Panels[2].Description = "Turns."
		Expect(views.CheckPanels(catalog, dashboard)).To(ConsistOf(
			`panel 1 "Sessions by phase" queries csf_nowhere_total, which CSF does not export: add it to catalog.json or drop the query`,
			`panel 2 "Turns per day, by agent" has no unit`,
			`panel 2 "Turns per day, by agent" does not carry the definition of csf_session_turns_total in its description`,
			"csf_sessions has no panel: add one to dashboard/csf.json in the same change",
		))
	})

	It("reads a histogram's bucket, sum and count series as the histogram, and only a histogram's", func() {
		catalog, dashboard := embedded()
		for _, series := range []string{views.MetricChatSettle + "_bucket", views.MetricChatSettle + "_sum", views.MetricChatSettle + "_count"} {
			family, found := catalog.LookupSeries(series)
			Expect(found).To(BeTrue(), series)
			Expect(family.Name).To(Equal(views.MetricChatSettle), series)
		}
		_, found := catalog.LookupSeries(views.MetricSessions + "_bucket")
		Expect(found).To(BeFalse(), "a gauge has no bucket series")
		panel, found := views.PanelFor(views.MetricChatSettle)
		Expect(found).To(BeTrue())
		Expect(panel.Title).To(Equal("Transcript settle time per event (TRANSCRIPT)"))
		settle := slices.IndexFunc(dashboard.Panels, func(candidate views.Panel) bool {
			return slices.Contains(candidate.Metrics(), views.MetricChatSettle+"_bucket")
		})
		Expect(settle).NotTo(Equal(-1))
		dashboard.Panels[settle].Targets = dashboard.Panels[settle].Targets[:0]
		Expect(views.CheckPanels(catalog, dashboard)).To(ContainElement(views.MetricChatSettle + " has no panel: add one to dashboard/csf.json in the same change"))
	})

	It("finds the panel each Workbench tile links to", func() {
		for metric, title := range map[string]string{
			views.MetricSessions: "Sessions by phase", views.MetricMerges: "Merges per day, by slice",
			views.MetricCost: "Spend per day, by model", views.MetricStruggleRate: "Struggles per 1k tool calls, this week",
			views.MetricCompounding: "Compounding factor of the struggle rate",
		} {
			panel, found := views.PanelFor(metric)
			Expect(found).To(BeTrue(), metric)
			Expect(panel.Title).To(Equal(title), metric)
		}
	})
})
