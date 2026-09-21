package dashbored

import "strconv"

// This file is the whole of the seam between the pipeline and the card. Nothing
// in telemetry.go, collector.go or view.go names a wire field, a region or an
// event, and nothing in the generated widget knows there is a fan-in behind it.

// ReportFields is one metrics view as the scrapeReport event carries it.
func ReportFields(view MetricsView) map[string]string {
	return map[string]string{
		DashboredEventScrapeReportFieldScrapeSequence:   strconv.FormatUint(view.Sequence, 10),
		DashboredEventScrapeReportFieldFiringAlert:      view.FiringAlert,
		DashboredEventScrapeReportFieldCollectorsUp:     strconv.Itoa(view.CollectorsUp),
		DashboredEventScrapeReportFieldSamplesPerSecond: strconv.Itoa(view.SamplesPerSecond),
		DashboredEventScrapeReportFieldRetentionDays:    strconv.Itoa(view.RetentionDays),
		DashboredEventScrapeReportFieldQueryWindowHours: strconv.Itoa(view.QueryWindowHours),
		DashboredEventScrapeReportFieldAggregatorUp:     strconv.FormatBool(view.AggregatorUp),
		DashboredEventScrapeReportFieldBreaching:        strconv.FormatBool(view.Breaching),
	}
}
